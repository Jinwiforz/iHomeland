using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Core.Foundation.Lifetime;
using NUnit.Framework;

namespace IHomeland.Client.Core.Tests.EditMode
{
    /// <summary>
    /// 验证 App Scope 生命周期的顺序、回滚、幂等停止和 deadline 契约。
    /// </summary>
    public sealed class AppLifetimeTests
    {
        /// <summary>
        /// 为无故障测试提供足够长但不会掩盖挂起的清理 deadline。
        /// </summary>
        private static readonly TimeSpan NormalTimeout = TimeSpan.FromSeconds(2);

        /// <summary>
        /// 保护初始化按登记顺序执行且正常停止严格逆序。
        /// </summary>
        /// <returns>等待初始化、停止和顺序断言完成的任务。</returns>
        [Test]
        public async Task StartAndStopUseForwardThenReverseOrder()
        {
            var events = new List<string>();
            var first = CreateParticipant("first", events);
            var second = CreateParticipant("second", events);
            var lifetime = CreateLifetime(new[] { first, second });

            await lifetime.StartAsync(CancellationToken.None);
            await lifetime.StopAsync();

            Assert.That(
                events,
                Is.EqualTo(new[] { "first:init", "second:init", "second:stop", "first:stop" }));
            Assert.That(lifetime.State, Is.EqualTo(AppLifetimeState.Stopped));
        }

        /// <summary>
        /// 保护初始化进行期间的重复启动调用共享同一任务而不重复执行参与者。
        /// </summary>
        /// <returns>等待并发启动与清理完成的任务。</returns>
        [Test]
        public async Task ConcurrentStartCallsShareOneResult()
        {
            var initializeGate = new TaskCompletionSource<bool>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            var initializeCount = 0;
            var participant = new FakeParticipant(
                async _ =>
                {
                    Interlocked.Increment(ref initializeCount);
                    await initializeGate.Task;
                },
                _ => Task.CompletedTask);
            var lifetime = CreateLifetime(new[] { participant });

            var firstStart = lifetime.StartAsync(CancellationToken.None);
            var secondStart = lifetime.StartAsync(CancellationToken.None);

            Assert.That(secondStart, Is.SameAs(firstStart));
            initializeGate.SetResult(true);
            await Task.WhenAll(firstStart, secondStart);
            Assert.That(initializeCount, Is.EqualTo(1));
            Assert.That(lifetime.State, Is.EqualTo(AppLifetimeState.Running));
            await lifetime.StopAsync();
        }

        /// <summary>
        /// 保护中途初始化失败只回滚已经成功的前置参与者。
        /// </summary>
        /// <returns>等待失败回滚和断言完成的任务。</returns>
        [Test]
        public async Task StartupFailureRollsBackOnlySuccessfulParticipants()
        {
            var events = new List<string>();
            var first = CreateParticipant("first", events);
            var startupError = new InvalidOperationException("injected startup failure");
            var second = new FakeParticipant(
                _ =>
                {
                    events.Add("second:init");
                    return Task.FromException(startupError);
                },
                _ =>
                {
                    events.Add("second:stop");
                    return Task.CompletedTask;
                });
            var third = CreateParticipant("third", events);
            var lifetime = CreateLifetime(new[] { first, second, third });

            var error = await CaptureExpectedExceptionAsync<AppStartupException>(
                () => lifetime.StartAsync(CancellationToken.None));

            Assert.That(error, Is.Not.Null);
            Assert.That(error.StartupError, Is.SameAs(startupError));
            Assert.That(error.RollbackErrors, Is.Empty);
            Assert.That(events, Is.EqualTo(new[] { "first:init", "second:init", "first:stop" }));
            Assert.That(lifetime.State, Is.EqualTo(AppLifetimeState.Failed));
        }

        /// <summary>
        /// 保护启动错误和回滚错误同时保留，避免清理失败覆盖根因。
        /// </summary>
        /// <returns>等待失败回滚和错误聚合断言完成的任务。</returns>
        [Test]
        public async Task StartupFailurePreservesRollbackErrors()
        {
            var startupError = new InvalidOperationException("injected startup failure");
            var rollbackError = new InvalidOperationException("injected rollback failure");
            var first = new FakeParticipant(
                _ => Task.CompletedTask,
                _ => Task.FromException(rollbackError));
            var second = new FakeParticipant(
                _ => Task.FromException(startupError),
                _ => Task.CompletedTask);
            var lifetime = CreateLifetime(new[] { first, second });

            var error = await CaptureExpectedExceptionAsync<AppStartupException>(
                () => lifetime.StartAsync(CancellationToken.None));

            Assert.That(error, Is.Not.Null);
            Assert.That(error.StartupError, Is.SameAs(startupError));
            Assert.That(error.RollbackErrors, Has.Count.EqualTo(1));
            Assert.That(error.RollbackErrors[0], Is.SameAs(rollbackError));
        }

        /// <summary>
        /// 保护并发停止共享同一任务且每个参与者最多清理一次。
        /// </summary>
        /// <returns>等待并发停止与幂等断言完成的任务。</returns>
        [Test]
        public async Task ConcurrentStopCallsShareOneResult()
        {
            var stopGate = new TaskCompletionSource<bool>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            var stopCount = 0;
            var participant = new FakeParticipant(
                _ => Task.CompletedTask,
                async _ =>
                {
                    Interlocked.Increment(ref stopCount);
                    await stopGate.Task;
                });
            var lifetime = CreateLifetime(new[] { participant });
            await lifetime.StartAsync(CancellationToken.None);

            var firstStop = lifetime.StopAsync();
            var secondStop = lifetime.StopAsync();

            Assert.That(secondStop, Is.SameAs(firstStop));
            stopGate.SetResult(true);
            await Task.WhenAll(firstStop, secondStop);
            Assert.That(stopCount, Is.EqualTo(1));
            Assert.That(lifetime.State, Is.EqualTo(AppLifetimeState.Stopped));
        }

        /// <summary>
        /// 保护初始化进行期间收到停止请求时先完成启动决议，再只清理成功项一次。
        /// </summary>
        /// <returns>等待受控初始化、停止和顺序断言完成的任务。</returns>
        [Test]
        public async Task StopDuringStartupWaitsThenCleansOnce()
        {
            var events = new List<string>();
            var initializeGate = new TaskCompletionSource<bool>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            var stopGate = new TaskCompletionSource<bool>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            var participant = new FakeParticipant(
                async _ =>
                {
                    events.Add("init:start");
                    await initializeGate.Task;
                    events.Add("init:end");
                },
                async _ =>
                {
                    events.Add("stop:start");
                    await stopGate.Task;
                    events.Add("stop:end");
                });
            var lifetime = CreateLifetime(new[] { participant });

            var startup = lifetime.StartAsync(CancellationToken.None);
            var shutdown = lifetime.StopAsync();
            Assert.That(shutdown.IsCompleted, Is.False);

            initializeGate.SetResult(true);
            await startup;
            Assert.That(lifetime.State, Is.EqualTo(AppLifetimeState.Stopping));
            Assert.That(shutdown.IsCompleted, Is.False);

            stopGate.SetResult(true);
            await shutdown;

            Assert.That(
                events,
                Is.EqualTo(new[] { "init:start", "init:end", "stop:start", "stop:end" }));
            Assert.That(lifetime.State, Is.EqualTo(AppLifetimeState.Stopped));
        }

        /// <summary>
        /// 保护尚未启动的对象图收到停止请求后原子进入终态，不能与并发启动交错复活。
        /// </summary>
        /// <returns>等待停止结果和终态断言完成的任务。</returns>
        [Test]
        public async Task StopBeforeStartMakesObjectGraphTerminal()
        {
            var initializeCount = 0;
            var participant = new FakeParticipant(
                _ =>
                {
                    initializeCount++;
                    return Task.CompletedTask;
                },
                _ => Task.CompletedTask);
            var lifetime = CreateLifetime(new[] { participant });

            await lifetime.StopAsync();

            Assert.That(initializeCount, Is.EqualTo(0));
            Assert.That(lifetime.State, Is.EqualTo(AppLifetimeState.Stopped));
            Assert.Throws<InvalidOperationException>(() => lifetime.StartAsync(CancellationToken.None));
        }

        /// <summary>
        /// 保护单个清理失败不会跳过更早初始化的其他参与者。
        /// </summary>
        /// <returns>等待尽力清理和错误聚合断言完成的任务。</returns>
        [Test]
        public async Task ShutdownContinuesAfterParticipantFailure()
        {
            var events = new List<string>();
            var first = CreateParticipant("first", events);
            var stopError = new InvalidOperationException("injected stop failure");
            var second = new FakeParticipant(
                _ =>
                {
                    events.Add("second:init");
                    return Task.CompletedTask;
                },
                _ =>
                {
                    events.Add("second:stop");
                    return Task.FromException(stopError);
                });
            var lifetime = CreateLifetime(new[] { first, second });
            await lifetime.StartAsync(CancellationToken.None);

            var error = await CaptureExpectedExceptionAsync<AppShutdownException>(lifetime.StopAsync);

            Assert.That(error, Is.Not.Null);
            Assert.That(error.Errors, Has.Count.EqualTo(1));
            Assert.That(error.Errors[0], Is.SameAs(stopError));
            Assert.That(events, Is.EqualTo(new[] { "first:init", "second:init", "second:stop", "first:stop" }));
            Assert.That(lifetime.State, Is.EqualTo(AppLifetimeState.Stopped));
        }

        /// <summary>
        /// 保护忽略取消的参与者不能让 App Scope 停止无限等待。
        /// </summary>
        /// <returns>等待 deadline 到期和 timeout 断言完成的任务。</returns>
        [Test]
        public async Task ShutdownDeadlineReportsHungParticipant()
        {
            var neverCompletes = new TaskCompletionSource<bool>();
            var participant = new FakeParticipant(
                _ => Task.CompletedTask,
                _ => neverCompletes.Task);
            var timeout = TimeSpan.FromMilliseconds(100);
            var lifetime = new AppLifetime(new[] { participant }, NormalTimeout, timeout);
            await lifetime.StartAsync(CancellationToken.None);

            var error = await CaptureExpectedExceptionAsync<AppShutdownException>(lifetime.StopAsync);

            Assert.That(error, Is.Not.Null);
            Assert.That(error.Errors, Has.Count.EqualTo(1));
            Assert.That(error.Errors[0], Is.TypeOf<TimeoutException>());
            Assert.That(lifetime.State, Is.EqualTo(AppLifetimeState.Stopped));
        }

        /// <summary>
        /// 保护停止后的旧对象图无法重新进入 Running。
        /// </summary>
        /// <returns>等待正常启动、停止和终态断言完成的任务。</returns>
        [Test]
        public async Task StoppedLifetimeCannotRestart()
        {
            var participant = new FakeParticipant(_ => Task.CompletedTask, _ => Task.CompletedTask);
            var lifetime = CreateLifetime(new[] { participant });
            await lifetime.StartAsync(CancellationToken.None);
            await lifetime.StopAsync();

            Assert.Throws<InvalidOperationException>(() => lifetime.StartAsync(CancellationToken.None));
        }

        /// <summary>
        /// 创建把初始化与停止顺序写入共享事件列表的确定性参与者。
        /// </summary>
        /// <param name="name">用于区分参与者的稳定测试名称。</param>
        /// <param name="events">按调用顺序接收事件的测试列表。</param>
        /// <returns>不执行 I/O 且立即完成的生命周期参与者。</returns>
        private static FakeParticipant CreateParticipant(string name, ICollection<string> events)
        {
            return new FakeParticipant(
                _ =>
                {
                    events.Add($"{name}:init");
                    return Task.CompletedTask;
                },
                _ =>
                {
                    events.Add($"{name}:stop");
                    return Task.CompletedTask;
                });
        }

        /// <summary>
        /// 使用测试默认 deadline 创建生命周期，避免每个行为测试重复配置。
        /// </summary>
        /// <param name="participants">按初始化顺序排列的 fake 参与者。</param>
        /// <returns>使用固定测试 deadline 的 AppLifetime。</returns>
        private static AppLifetime CreateLifetime(IReadOnlyList<IAppLifetimeParticipant> participants)
        {
            return new AppLifetime(participants, NormalTimeout, NormalTimeout);
        }

        /// <summary>
        /// 以真实异步等待捕获预期异常，避免 NUnit 同步等待阻塞 Unity 主线程 continuation。
        /// </summary>
        /// <typeparam name="TException">调用必须抛出的异常类型。</typeparam>
        /// <param name="action">返回待验证异步操作的非空 delegate。</param>
        /// <returns>异步操作抛出的预期异常实例。</returns>
        private static async Task<TException> CaptureExpectedExceptionAsync<TException>(Func<Task> action)
            where TException : Exception
        {
            if (action == null)
            {
                throw new ArgumentNullException(nameof(action));
            }

            try
            {
                await action();
            }
            catch (TException expectedError)
            {
                return expectedError;
            }

            Assert.Fail($"预期异步操作抛出 {typeof(TException).FullName}，但操作成功完成。");
            return null;
        }

        /// <summary>
        /// 通过注入 Task delegate 精确控制生命周期成功、失败和挂起行为。
        /// </summary>
        private sealed class FakeParticipant : IAppLifetimeParticipant
        {
            /// <summary>
            /// 保存初始化阶段的故障注入或顺序记录 delegate。
            /// </summary>
            private readonly Func<CancellationToken, Task> _initialize;

            /// <summary>
            /// 保存停止阶段的故障注入或顺序记录 delegate。
            /// </summary>
            private readonly Func<CancellationToken, Task> _stop;

            /// <summary>
            /// 创建行为完全由测试 delegate 控制的参与者。
            /// </summary>
            /// <param name="initialize">初始化调用时执行的非空 delegate。</param>
            /// <param name="stop">停止调用时执行的非空 delegate。</param>
            internal FakeParticipant(
                Func<CancellationToken, Task> initialize,
                Func<CancellationToken, Task> stop)
            {
                _initialize = initialize ?? throw new ArgumentNullException(nameof(initialize));
                _stop = stop ?? throw new ArgumentNullException(nameof(stop));
            }

            /// <summary>
            /// 执行测试注入的初始化行为。
            /// </summary>
            /// <param name="cancellationToken">由被测生命周期传入的启动取消信号。</param>
            /// <returns>注入 delegate 返回的任务。</returns>
            public Task InitializeAsync(CancellationToken cancellationToken)
            {
                return _initialize(cancellationToken);
            }

            /// <summary>
            /// 执行测试注入的停止行为。
            /// </summary>
            /// <param name="cancellationToken">由被测生命周期传入的共享 deadline。</param>
            /// <returns>注入 delegate 返回的任务。</returns>
            public Task StopAsync(CancellationToken cancellationToken)
            {
                return _stop(cancellationToken);
            }
        }
    }
}
