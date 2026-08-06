using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;

namespace IHomeland.Client.Core.Foundation.Lifetime
{
    /// <summary>
    /// 统一拥有 App Scope 参与者的顺序初始化、失败回滚和幂等逆序停止。
    /// </summary>
    /// <remarks>
    /// 参与者列表在构造时复制并冻结。公开方法允许并发调用，但实际启动和停止各只执行一次；
    /// 参与者 callback 不在状态锁内执行，避免外部代码重入生命周期临界区。
    /// </remarks>
    internal sealed class AppLifetime
    {
        /// <summary>
        /// 保护状态、共享任务引用和状态迁移不变量。
        /// </summary>
        private readonly object _sync = new object();

        /// <summary>
        /// 保存按初始化顺序冻结的生命周期参与者快照。
        /// </summary>
        private readonly IAppLifetimeParticipant[] _participants;

        /// <summary>
        /// 保存已经初始化成功且仍需清理的参与者栈。
        /// </summary>
        private readonly List<IAppLifetimeParticipant> _initializedParticipants = new List<IAppLifetimeParticipant>();

        /// <summary>
        /// 限制初始化失败后的整组回滚等待时间。
        /// </summary>
        private readonly TimeSpan _rollbackTimeout;

        /// <summary>
        /// 限制正常逆序停止的整组等待时间。
        /// </summary>
        private readonly TimeSpan _shutdownTimeout;

        /// <summary>
        /// 保存当前单向生命周期状态，只能在 <see cref="_sync"/> 内修改。
        /// </summary>
        private AppLifetimeState _state = AppLifetimeState.Created;

        /// <summary>
        /// 保存所有重复启动调用共享的任务。
        /// </summary>
        private Task _startTask;

        /// <summary>
        /// 保存所有重复或并发停止调用共享的任务。
        /// </summary>
        private Task _stopTask;

        /// <summary>
        /// 创建拥有固定参与者顺序和清理 deadline 的 App Scope 生命周期。
        /// </summary>
        /// <param name="participants">按依赖顺序初始化、按逆序停止的参与者集合。</param>
        /// <param name="rollbackTimeout">初始化失败后整组回滚允许等待的最长时间。</param>
        /// <param name="shutdownTimeout">正常停止时整组清理允许等待的最长时间。</param>
        /// <exception cref="ArgumentException">任一 timeout 非正数时抛出。</exception>
        /// <exception cref="ArgumentNullException">参与者集合或其中任一成员为空时抛出。</exception>
        internal AppLifetime(
            IReadOnlyList<IAppLifetimeParticipant> participants,
            TimeSpan rollbackTimeout,
            TimeSpan shutdownTimeout)
        {
            if (participants == null)
            {
                throw new ArgumentNullException(nameof(participants));
            }

            if (rollbackTimeout <= TimeSpan.Zero)
            {
                throw new ArgumentException("回滚 timeout 必须为正数。", nameof(rollbackTimeout));
            }

            if (shutdownTimeout <= TimeSpan.Zero)
            {
                throw new ArgumentException("停止 timeout 必须为正数。", nameof(shutdownTimeout));
            }

            _participants = new IAppLifetimeParticipant[participants.Count];
            for (var index = 0; index < participants.Count; index++)
            {
                _participants[index] = participants[index] ??
                    throw new ArgumentNullException(nameof(participants), $"生命周期参与者索引 {index} 不能为空。");
            }

            _rollbackTimeout = rollbackTimeout;
            _shutdownTimeout = shutdownTimeout;
        }

        /// <summary>
        /// 获取当前生命周期状态快照。
        /// </summary>
        /// <remarks>状态读取是线程安全的，但返回后可能立即发生下一次合法迁移。</remarks>
        internal AppLifetimeState State
        {
            get
            {
                lock (_sync)
                {
                    return _state;
                }
            }
        }

        /// <summary>
        /// 按冻结顺序初始化全部参与者，并让重复启动调用共享同一结果。
        /// </summary>
        /// <param name="cancellationToken">取消启动等待；已成功项仍使用独立回滚 deadline 清理。</param>
        /// <returns>全部参与者进入 Running，或失败回滚完成时结束的共享任务。</returns>
        /// <exception cref="AppStartupException">参与者初始化失败或启动被取消时通过返回任务抛出。</exception>
        /// <exception cref="InvalidOperationException">旧对象图已经停止或失败后再次启动时抛出。</exception>
        internal Task StartAsync(CancellationToken cancellationToken)
        {
            TaskCompletionSource<bool> startupCompletion;
            lock (_sync)
            {
                switch (_state)
                {
                    case AppLifetimeState.Created:
                        _state = AppLifetimeState.Initializing;
                        startupCompletion = new TaskCompletionSource<bool>(
                            TaskCreationOptions.RunContinuationsAsynchronously);
                        _startTask = startupCompletion.Task;
                        break;
                    case AppLifetimeState.Initializing:
                        return _startTask;
                    case AppLifetimeState.Running:
                        return _startTask;
                    default:
                        throw new InvalidOperationException($"状态 {_state} 的 App Scope 不能再次启动。");
                }
            }

            _ = CompleteStartupAsync(startupCompletion, cancellationToken);
            return _startTask;
        }

        /// <summary>
        /// 逆序停止全部已初始化参与者，并让重复或并发调用共享同一结果。
        /// </summary>
        /// <remarks>
        /// 尚未开始初始化时会在状态锁内直接进入 Stopped，阻止并发启动越过停止请求；
        /// 已运行时同样先发布 Stopping，再在锁外调用参与者。
        /// </remarks>
        /// <returns>全部可执行清理均已尝试后结束的共享任务。</returns>
        /// <exception cref="AppShutdownException">一个或多个参与者停止失败或超时时通过返回任务抛出。</exception>
        /// <exception cref="InvalidOperationException">内部状态与共享停止任务不一致时同步抛出。</exception>
        internal Task StopAsync()
        {
            TaskCompletionSource<bool> stopCompletion;
            Task startupTask;
            lock (_sync)
            {
                if (_stopTask != null)
                {
                    return _stopTask;
                }

                switch (_state)
                {
                    case AppLifetimeState.Created:
                        _state = AppLifetimeState.Stopped;
                        _stopTask = Task.CompletedTask;
                        return _stopTask;
                    case AppLifetimeState.Failed:
                    case AppLifetimeState.Stopped:
                        _stopTask = Task.CompletedTask;
                        return _stopTask;
                    case AppLifetimeState.Initializing:
                        startupTask = _startTask;
                        break;
                    case AppLifetimeState.Running:
                        _state = AppLifetimeState.Stopping;
                        startupTask = null;
                        break;
                    default:
                        throw new InvalidOperationException($"状态 {_state} 缺少共享停止任务。");
                }

                stopCompletion = new TaskCompletionSource<bool>(
                    TaskCreationOptions.RunContinuationsAsynchronously);
                _stopTask = stopCompletion.Task;
            }

            _ = CompleteStopAsync(stopCompletion, startupTask);
            return _stopTask;
        }

        /// <summary>
        /// 在状态锁外执行启动，并把唯一结果发布给全部共享调用方。
        /// </summary>
        /// <param name="completion">在状态锁内创建的共享启动完成源。</param>
        /// <param name="cancellationToken">调用方控制的启动取消信号。</param>
        /// <returns>启动结果已经发布到 completion 时结束的内部任务。</returns>
        private async Task CompleteStartupAsync(
            TaskCompletionSource<bool> completion,
            CancellationToken cancellationToken)
        {
            try
            {
                await StartCoreAsync(cancellationToken);
                completion.TrySetResult(true);
            }
            catch (Exception startupError)
            {
                completion.TrySetException(startupError);
            }
        }

        /// <summary>
        /// 在状态锁外执行停止，并把唯一结果发布给全部共享调用方。
        /// </summary>
        /// <param name="completion">在状态锁内创建的共享停止完成源。</param>
        /// <param name="startupTask">停止请求发生时的共享启动任务，可以为空。</param>
        /// <returns>停止结果已经发布到 completion 时结束的内部任务。</returns>
        private async Task CompleteStopAsync(TaskCompletionSource<bool> completion, Task startupTask)
        {
            try
            {
                await StopAfterStartupAsync(startupTask);
                completion.TrySetResult(true);
            }
            catch (Exception stopError)
            {
                completion.TrySetException(stopError);
            }
        }

        /// <summary>
        /// 执行实际初始化，并在任一步失败后使用独立 deadline 回滚成功项。
        /// </summary>
        /// <param name="cancellationToken">调用方控制的启动取消信号。</param>
        /// <returns>全部参与者成功初始化时完成的任务。</returns>
        /// <exception cref="AppStartupException">初始化失败或取消且逆序回滚已经结束时抛出。</exception>
        private async Task StartCoreAsync(CancellationToken cancellationToken)
        {
            try
            {
                foreach (var participant in _participants)
                {
                    cancellationToken.ThrowIfCancellationRequested();
                    var initialization = participant.InitializeAsync(cancellationToken);
                    if (initialization == null)
                    {
                        throw new InvalidOperationException($"{GetParticipantName(participant)} 返回了空初始化任务。");
                    }

                    await initialization;
                    _initializedParticipants.Add(participant);
                }

                lock (_sync)
                {
                    // 停止可能在最后一个初始化 await 期间到达；此时不能重新发布可执行工作的 Running。
                    _state = _stopTask == null
                        ? AppLifetimeState.Running
                        : AppLifetimeState.Stopping;
                }
            }
            catch (Exception startupError)
            {
                var rollbackErrors = await StopInitializedParticipantsAsync(_rollbackTimeout);
                lock (_sync)
                {
                    _state = AppLifetimeState.Failed;
                }

                throw new AppStartupException(startupError, rollbackErrors);
            }
        }

        /// <summary>
        /// 等待正在进行的启动结束，再执行唯一一次正常停止。
        /// </summary>
        /// <param name="startupTask">停止请求发生时已经存在的共享启动任务，可以为空。</param>
        /// <returns>停止完成或聚合停止错误后结束的任务。</returns>
        /// <exception cref="AppShutdownException">一个或多个参与者停止失败或超时时抛出。</exception>
        private async Task StopAfterStartupAsync(Task startupTask)
        {
            if (startupTask != null)
            {
                try
                {
                    await startupTask;
                }
                catch (AppStartupException)
                {
                    // 启动失败路径已经完成回滚并把状态设置为 Failed，无需重复清理。
                    return;
                }
            }

            lock (_sync)
            {
                if (_state == AppLifetimeState.Failed || _state == AppLifetimeState.Stopped)
                {
                    return;
                }

                if (_state == AppLifetimeState.Running)
                {
                    _state = AppLifetimeState.Stopping;
                }
                else if (_state != AppLifetimeState.Stopping)
                {
                    throw new InvalidOperationException($"状态 {_state} 的 App Scope 无法完成停止。");
                }
            }

            var errors = await StopInitializedParticipantsAsync(_shutdownTimeout);
            lock (_sync)
            {
                _state = AppLifetimeState.Stopped;
            }

            if (errors.Count > 0)
            {
                throw new AppShutdownException(errors);
            }
        }

        /// <summary>
        /// 在共享 deadline 内逆序尝试全部成功项，并清空旧对象图的清理栈。
        /// </summary>
        /// <param name="timeout">整组逆序清理允许等待的最长时间。</param>
        /// <returns>按发生顺序收集的停止、取消或 timeout 错误。</returns>
        private async Task<IReadOnlyList<Exception>> StopInitializedParticipantsAsync(TimeSpan timeout)
        {
            var errors = new List<Exception>();
            using (var deadline = new CancellationTokenSource(timeout))
            {
                for (var index = _initializedParticipants.Count - 1; index >= 0; index--)
                {
                    var participant = _initializedParticipants[index];
                    try
                    {
                        var stopTask = participant.StopAsync(deadline.Token);
                        if (stopTask == null)
                        {
                            throw new InvalidOperationException($"{GetParticipantName(participant)} 返回了空停止任务。");
                        }

                        await AwaitWithinDeadlineAsync(stopTask, deadline.Token, participant);
                    }
                    catch (OperationCanceledException cancellationError) when (deadline.IsCancellationRequested)
                    {
                        errors.Add(CreateTimeoutError(participant, timeout, cancellationError));
                    }
                    catch (TimeoutException timeoutError)
                    {
                        errors.Add(timeoutError);
                    }
                    catch (Exception stopError)
                    {
                        errors.Add(stopError);
                    }
                }
            }

            _initializedParticipants.Clear();
            return errors;
        }

        /// <summary>
        /// 等待单个清理任务或共享 deadline，且观察 deadline 后迟到的任务异常。
        /// </summary>
        /// <param name="operation">参与者返回的清理任务。</param>
        /// <param name="deadlineToken">整组清理共享的 deadline token。</param>
        /// <param name="participant">用于 timeout 诊断的参与者。</param>
        /// <returns>清理任务在 deadline 前结束时完成的任务。</returns>
        /// <exception cref="TimeoutException">共享 deadline 先于清理任务结束时抛出。</exception>
        private static async Task AwaitWithinDeadlineAsync(
            Task operation,
            CancellationToken deadlineToken,
            IAppLifetimeParticipant participant)
        {
            if (operation.IsCompleted)
            {
                await operation;
                return;
            }

            var deadlineSignal = Task.Delay(Timeout.InfiniteTimeSpan, deadlineToken);
            var completed = await Task.WhenAny(operation, deadlineSignal);
            if (completed != operation)
            {
                ObserveLateFailure(operation);
                throw new TimeoutException($"{GetParticipantName(participant)} 未在 App Scope 清理 deadline 内停止。");
            }

            await operation;
        }

        /// <summary>
        /// 观察已经超出 deadline 的任务，避免其迟到异常成为未观察任务异常。
        /// </summary>
        /// <param name="operation">调用方已停止等待但底层仍可能结束的任务。</param>
        private static void ObserveLateFailure(Task operation)
        {
            _ = operation.ContinueWith(
                completed => _ = completed.Exception,
                CancellationToken.None,
                TaskContinuationOptions.OnlyOnFaulted | TaskContinuationOptions.ExecuteSynchronously,
                TaskScheduler.Default);
        }

        /// <summary>
        /// 为共享 deadline 已取消的参与者创建包含 timeout 与原始取消信息的诊断错误。
        /// </summary>
        /// <param name="participant">未在 deadline 内完成清理的参与者。</param>
        /// <param name="timeout">整组清理配置的最长等待时间。</param>
        /// <param name="innerError">参与者观察到 deadline 后返回的取消错误。</param>
        /// <returns>包含参与者类型和 deadline 的 timeout 错误。</returns>
        private static TimeoutException CreateTimeoutError(
            IAppLifetimeParticipant participant,
            TimeSpan timeout,
            Exception innerError)
        {
            return new TimeoutException(
                $"{GetParticipantName(participant)} 未在 App Scope 清理 deadline {timeout.TotalMilliseconds:0}ms 内停止。",
                innerError);
        }

        /// <summary>
        /// 返回不依赖可变业务状态的参与者类型名称，用于生命周期诊断。
        /// </summary>
        /// <param name="participant">需要描述的生命周期参与者。</param>
        /// <returns>参与者的完整类型名；运行时未提供时回退为短类型名。</returns>
        private static string GetParticipantName(IAppLifetimeParticipant participant)
        {
            var type = participant.GetType();
            return type.FullName ?? type.Name;
        }
    }
}
