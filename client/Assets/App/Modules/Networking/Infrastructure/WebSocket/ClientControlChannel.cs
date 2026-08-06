using System;
using System.Collections.Generic;
using System.Net;
using System.Net.WebSockets;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Session.Application;
using IHomeland.Client.Networking.Application.Control;
using IHomeland.Client.AppShell.Application.Configuration;
using IHomeland.Client.Core.Foundation.Lifetime;
using IHomeland.Client.Core.Foundation.Threading;
using IHomeland.Client.Core.Foundation.Time;
using IHomeland.Client.Networking.Application.Contracts;
using IHomeland.Client.Networking.Infrastructure.Http;
using IHomeland.Client.Networking.Infrastructure.WebSocket;

namespace IHomeland.Client.Networking.Infrastructure.WebSocket
{
    /// <summary>
    /// 作为 App Scope 唯一 WSS control owner 协调 ticket、receive、主线程分发与有限恢复。
    /// </summary>
    /// <remarks>
    /// Mutable lifecycle state 由 <see cref="_sync"/> 保护，锁内不执行 HTTP、socket、delay 或
    /// subscriber callback。每个显式 run 只有一个 receive pump；run generation 与 ticket source
    /// generation 分别阻止旧网络 completion 和旧 session invalidation 覆盖后发状态。
    /// </remarks>
    internal sealed class ClientControlChannel :
        IAppLifetimeParticipant,
        IControlConnectionAttemptHost
    {
        /// <summary>
        /// 保护 lifecycle snapshot、run generation、active socket 与 task identity。
        /// </summary>
        private readonly object _sync = new object();

        /// <summary>拥有 ticket、URI、handshake 与单次 socket 资源。</summary>
        private readonly ControlConnectionAttempt _connectionAttempt;

        /// <summary>
        /// 把网络 completion 有界转移到捕获的 Unity 主线程。
        /// </summary>
        /// <summary>
        /// 提供不阻塞线程且可测试的有限 backoff。
        /// </summary>
        private readonly IClientDelay _delay;

        /// <summary>
        /// 保存每次瞬时故障后的固定重连延迟；长度即额外尝试上限。
        /// </summary>
        private readonly ControlRetryPolicy _retryPolicy;

        /// <summary>有界主线程 typed PUSH dispatcher。</summary>
        private readonly ControlPushDispatcher _pushDispatcher;

        /// <summary>
        /// AppLifetime 初始化时创建、停止时取消的根信号。
        /// </summary>
        private CancellationTokenSource _lifetimeCancellation;

        /// <summary>
        /// 保存当前显式 run task；同一时刻不允许第二个 receive pump。
        /// </summary>
        private Task _runTask;

        /// <summary>
        /// 保存当前 run 首次进入 Connected 或在此前稳定失败的单次完成信号。
        /// </summary>
        /// <remarks>
        /// 认证流程依赖该信号建立“可接收 WSS push”屏障，避免玩家已经进入世界但定向邀请仍被
        /// 服务端判定为 offline。它不表示连接永久存活，后续断线仍由 run 终态处理。
        /// </remarks>
        private TaskCompletionSource<bool> _connectionReadiness;

        /// <summary>
        /// 每次显式 run 递增，使迟到 attempt completion 无法提交新状态。
        /// </summary>
        private long _runGeneration;

        /// <summary>
        /// 保存当前 attempt 独占 socket，仅供停止路径发起有界 close。
        /// </summary>
        private IClientWebSocket _activeSocket;

        /// <summary>只取消current connection attempt且不终止整个显式run的owner。</summary>
        private CancellationTokenSource _activeAttemptCancellation;

        /// <summary>
        /// 保存当前安全可观察状态。
        /// </summary>
        private ClientControlChannelSnapshot _snapshot = new ClientControlChannelSnapshot(
            ClientControlChannelState.Created,
            ClientControlCloseReason.None,
            0,
            0);

        /// <summary>
        /// 保存幂等 App Scope stop completion。
        /// </summary>
        private Task _stopTask;

        /// <summary>
        /// 创建 control channel owner。
        /// </summary>
        /// <param name="environment">已严格验证且不含 secret 的环境快照。</param>
        /// <param name="configurationStore">App Scope 唯一 Configuration owner。</param>
        /// <param name="sessionCoordinator">App Scope 唯一 Session owner。</param>
        /// <param name="socketFactory">每次 attempt 的窄 WebSocket factory。</param>
        /// <param name="codec">封闭 WSS control codec。</param>
        /// <param name="protocolAdapter">Generated payload 到 Application notification 的唯一 adapter。</param>
        /// <param name="dispatcher">有界 Unity 主线程 dispatcher。</param>
        /// <param name="delay">可测试 backoff owner。</param>
        /// <param name="retryDelays">每次瞬时失败后的正数固定延迟。</param>
        /// <param name="gameplaySessionInvalidation">Session authority 接受更高 epoch 后同步撤销匹配 gameplay generation 的可选回调。</param>
        /// <exception cref="ArgumentException">Retry policy 包含非正数 delay 时抛出。</exception>
        /// <exception cref="ArgumentNullException">任一依赖或 policy 为空时抛出。</exception>
        internal ClientControlChannel(
            ClientEnvironment environment,
            ClientConfigurationStore configurationStore,
            SessionCoordinator sessionCoordinator,
            IClientWebSocketFactory socketFactory,
            ClientControlCodec codec,
            ClientControlProtocolAdapter protocolAdapter,
            IClientMainThreadDispatcher dispatcher,
            IClientDelay delay,
            IReadOnlyList<TimeSpan> retryDelays,
            Action<long> gameplaySessionInvalidation = null)
        {
            var receivePump = new ControlReceivePump(codec, protocolAdapter);
            if (dispatcher == null)
            {
                throw new ArgumentNullException(nameof(dispatcher));
            }

            _delay = delay ?? throw new ArgumentNullException(nameof(delay));
            _pushDispatcher = new ControlPushDispatcher(dispatcher);
            _connectionAttempt = new ControlConnectionAttempt(
                environment,
                configurationStore,
                sessionCoordinator,
                socketFactory,
                receivePump,
                gameplaySessionInvalidation);
            _retryPolicy = new ControlRetryPolicy(retryDelays);
        }

        /// <summary>
        /// 在主线程投递合法 generated control PUSH；网络线程不会直接调用 subscriber。
        /// </summary>
        internal event Action<ClientControlNotification> PushReceived;

        /// <summary>在generation-bound生命周期快照提交后通知唯一恢复owner。</summary>
        internal event Action<ClientControlChannelSnapshot> HealthChanged;

        /// <summary>
        /// 获取不包含 endpoint、ticket、session 或 payload 的当前状态快照。
        /// </summary>
        internal ClientControlChannelSnapshot Snapshot
        {
            get
            {
                lock (_sync)
                {
                    return _snapshot;
                }
            }
        }

#if DEVELOPMENT_BUILD || UNITY_EDITOR
        /// <summary>获取资格运行可观察的current control run owner数量。</summary>
        internal int QualificationRunOwnerCount
        {
            get
            {
                lock (_sync)
                {
                    return _runTask != null && !_runTask.IsCompleted ? 1 : 0;
                }
            }
        }

        /// <summary>获取资格范围内control subscriber总数。</summary>
        internal int QualificationSubscriptionCount
        {
            get
            {
                lock (_sync)
                {
                    return CountSubscribers(PushReceived) + CountSubscribers(HealthChanged);
                }
            }
        }

        /// <summary>为Development资格运行中断current socket，使既有receive/retry路径观察transport失败。</summary>
        /// <returns>存在已连接current socket并已请求中断时返回true。</returns>
        internal bool InjectQualificationTransportDisconnect()
        {
            CancellationTokenSource attempt;
            lock (_sync)
            {
                if (_snapshot.State != ClientControlChannelState.Connected ||
                    _activeSocket == null ||
                    _activeAttemptCancellation == null)
                {
                    return false;
                }

                attempt = _activeAttemptCancellation;
            }

            attempt.Cancel();
            return true;
        }
#endif

        /// <summary>
        /// 启用显式运行入口但不签发 ticket 或建立连接。
        /// </summary>
        /// <param name="cancellationToken">初始化前检查的 AppLifetime 取消信号。</param>
        /// <returns>Owner 已进入 Idle 时完成的任务。</returns>
        /// <exception cref="InvalidOperationException">重复初始化或停止后重启时抛出。</exception>
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
            ClientControlChannelSnapshot committed;
            cancellationToken.ThrowIfCancellationRequested();
            lock (_sync)
            {
                if (_lifetimeCancellation != null ||
                    _snapshot.State == ClientControlChannelState.Stopped)
                {
                    throw new InvalidOperationException("ClientControlChannel 不能重复初始化或停止后重启。");
                }

                _lifetimeCancellation = new CancellationTokenSource();
                _snapshot = new ClientControlChannelSnapshot(
                    ClientControlChannelState.Idle,
                    ClientControlCloseReason.None,
                    _runGeneration,
                    0);
                committed = _snapshot;
            }

            NotifyHealthChanged(committed);
            return Task.CompletedTask;
        }

        /// <summary>
        /// 显式运行唯一 control connection，并在有限瞬时故障预算内恢复。
        /// </summary>
        /// <param name="cancellationToken">取消本次 run，但不清除仍 current 的 HTTP session。</param>
        /// <returns>本次 run 进入稳定终态或被取消时完成的任务。</returns>
        /// <exception cref="InvalidOperationException">未初始化、已停止或已有 active run 时抛出。</exception>
        internal Task RunAsync(CancellationToken cancellationToken)
        {
            Task runTask;
            ClientControlChannelSnapshot committed;
            lock (_sync)
            {
                if (_lifetimeCancellation == null ||
                    _snapshot.State == ClientControlChannelState.Stopped)
                {
                    throw new InvalidOperationException("ClientControlChannel 当前生命周期不允许运行。");
                }

                if (_runTask != null &&
                    !_runTask.IsCompleted &&
                    !CanSupersedeTerminalRunLocked())
                {
                    throw new InvalidOperationException("ClientControlChannel 已存在 active run。");
                }

                cancellationToken.ThrowIfCancellationRequested();
                var linkedCancellation = CancellationTokenSource.CreateLinkedTokenSource(
                    _lifetimeCancellation.Token,
                    cancellationToken);
                var runGeneration = ++_runGeneration;
                _snapshot = new ClientControlChannelSnapshot(
                    ClientControlChannelState.Connecting,
                    ClientControlCloseReason.None,
                    ComposeHealthGeneration(runGeneration, 1),
                    1);
                _connectionReadiness = new TaskCompletionSource<bool>(
                    TaskCreationOptions.RunContinuationsAsynchronously);
                _runTask = RunCoreAsync(runGeneration, linkedCancellation);
                runTask = _runTask;
                committed = _snapshot;
            }

            NotifyHealthChanged(committed);
            return runTask;
        }

        /// <summary>判断旧run是否已经发布可由下一代安全接管的稳定网络终态。</summary>
        /// <remarks>
        /// RunCore会先发布Disconnected，再进入finally释放Task identity。该终态是socket已释放后的
        /// 线性化点；允许下一代在这段极短清理窗口内接管，旧代际会因generation不匹配而无法清除
        /// 新run。Requested关闭不属于玩家可重试的网络故障，必须等待旧run完整退出。
        /// </remarks>
        /// <returns>旧run只剩无网络副作用的清理尾声时返回true。</returns>
        private bool CanSupersedeTerminalRunLocked()
        {
            return _snapshot.State == ClientControlChannelState.Disconnected &&
                   _snapshot.CloseReason != ClientControlCloseReason.Requested;
        }

        /// <summary>等待当前显式 run 首次具备接收 control push 的能力。</summary>
        /// <param name="cancellationToken">调用方停止等待的信号，不会停止 control run。</param>
        /// <returns>首次进入 Connected 返回 true；此前进入稳定终态返回 false。</returns>
        /// <exception cref="InvalidOperationException">当前没有显式 run 时抛出。</exception>
        internal async Task<bool> WaitUntilConnectedAsync(CancellationToken cancellationToken)
        {
            Task<bool> readiness;
            lock (_sync)
            {
                if (_snapshot.State == ClientControlChannelState.Connected)
                {
                    return true;
                }

                readiness = _connectionReadiness?.Task ??
                    throw new InvalidOperationException("ClientControlChannel 当前没有可等待的 active run。");
            }

            if (!cancellationToken.CanBeCanceled)
            {
                return await readiness;
            }

            var cancellation = new TaskCompletionSource<bool>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            using (cancellationToken.Register(() => cancellation.TrySetCanceled()))
            {
                var completed = await Task.WhenAny(readiness, cancellation.Task);
                return await completed;
            }
        }

        /// <summary>
        /// 幂等停止 control owner，先取消/关闭 WSS，再允许 Session 与 HTTP 逆序释放。
        /// </summary>
        /// <param name="cancellationToken">AppLifetime 提供的有界停止 deadline。</param>
        /// <returns>Socket、receive 与 backoff 已结束时完成的任务。</returns>
        public Task StopAsync(CancellationToken cancellationToken)
        {
            TaskCompletionSource<bool> completion;
            CancellationTokenSource lifetimeCancellation;
            Task runTask;
            IClientWebSocket activeSocket;
            TaskCompletionSource<bool> connectionReadiness;
            ClientControlChannelSnapshot committed;
            lock (_sync)
            {
                if (_stopTask != null)
                {
                    return _stopTask;
                }

                completion = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
                _stopTask = completion.Task;
                _runGeneration++;
                _snapshot = new ClientControlChannelSnapshot(
                    ClientControlChannelState.Stopped,
                    ClientControlCloseReason.Requested,
                    ComposeHealthGeneration(_runGeneration, _snapshot.Attempt),
                    _snapshot.Attempt);
                committed = _snapshot;
                lifetimeCancellation = _lifetimeCancellation;
                runTask = _runTask;
                activeSocket = _activeSocket;
                connectionReadiness = _connectionReadiness;
            }

            NotifyHealthChanged(committed);
            connectionReadiness?.TrySetResult(false);

            _ = StopCoreAsync(
                lifetimeCancellation,
                runTask,
                activeSocket,
                cancellationToken,
                completion);
            return completion.Task;
        }

        /// <summary>
        /// 执行一次显式 run 的连接/恢复循环并在退出时清理 task identity。
        /// </summary>
        /// <param name="runGeneration">本次 run 的唯一代际。</param>
        /// <param name="linkedCancellation">组合显式与 App Scope 取消的 owner。</param>
        /// <returns>本次 run 到达稳定终态时完成的任务。</returns>
        private async Task RunCoreAsync(
            long runGeneration,
            CancellationTokenSource linkedCancellation)
        {
            await Task.Yield();
            var cancellationToken = linkedCancellation.Token;
            var retryIndex = 0;
            try
            {
                while (true)
                {
                    cancellationToken.ThrowIfCancellationRequested();
                    var attempt = retryIndex + 1;
                    TransitionIfCurrent(
                        runGeneration,
                        ClientControlChannelState.Connecting,
                        ClientControlCloseReason.None,
                        attempt);

                    var outcome = await RunAttemptAsync(
                        runGeneration,
                        attempt,
                        cancellationToken);
                    if (outcome == ControlAttemptOutcomeKind.Requested)
                    {
                        TransitionIfCurrent(
                            runGeneration,
                            ClientControlChannelState.Disconnected,
                            ClientControlCloseReason.Requested,
                            attempt);
                        return;
                    }

                    var closeReason = MapCloseReason(outcome);
                    if (!IsRecoverable(outcome))
                    {
                        CommitTerminalOutcome(runGeneration, outcome, attempt);
                        return;
                    }

                    if (!_retryPolicy.TryGetDelay(
                            closeReason,
                            retryIndex,
                            out var retryDelay))
                    {
                        TransitionIfCurrent(
                            runGeneration,
                            ClientControlChannelState.Disconnected,
                            ClientControlCloseReason.RetryExhausted,
                            attempt);
                        return;
                    }

                    TransitionIfCurrent(
                        runGeneration,
                        ClientControlChannelState.Recovering,
                        closeReason,
                        attempt);
                    await _delay.DelayAsync(retryDelay, cancellationToken);
                    retryIndex++;
                }
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
            {
                TransitionIfCurrent(
                    runGeneration,
                    ClientControlChannelState.Disconnected,
                    ClientControlCloseReason.Requested,
                    retryIndex + 1);
            }
            finally
            {
                linkedCancellation.Dispose();
                lock (_sync)
                {
                    if (runGeneration == _runGeneration)
                    {
                        _runTask = null;
                    }
                }
            }
        }

        /// <summary>
        /// 签发并单次交付 ticket，建立 socket，然后运行唯一 receive pump。
        /// </summary>
        /// <param name="runGeneration">阻止旧 attempt 注册 active socket 的 run 代际。</param>
        /// <param name="attempt">当前run内从1递增的connection attempt。</param>
        /// <param name="cancellationToken">当前 run 的组合取消信号。</param>
        /// <returns>决定是否恢复的稳定 attempt outcome。</returns>
        private async Task<ControlAttemptOutcomeKind> RunAttemptAsync(
            long runGeneration,
            int attempt,
            CancellationToken cancellationToken)
        {
            return await _connectionAttempt.RunAsync(
                this,
                runGeneration,
                attempt,
                ComposeHealthGeneration(runGeneration, attempt),
                cancellationToken);
        }

        /// <inheritdoc />
        bool IControlConnectionAttemptHost.TryClaimAttempt(
            long runGeneration,
            IClientWebSocket socket,
            CancellationTokenSource attemptCancellation)
        {
            lock (_sync)
            {
                if (runGeneration != _runGeneration ||
                    _snapshot.State == ClientControlChannelState.Stopped)
                {
                    return false;
                }

                _activeSocket = socket;
                _activeAttemptCancellation = attemptCancellation;
                return true;
            }
        }

        /// <inheritdoc />
        void IControlConnectionAttemptHost.ReleaseAttempt(IClientWebSocket socket)
        {
            lock (_sync)
            {
                if (ReferenceEquals(_activeSocket, socket))
                {
                    _activeSocket = null;
                    _activeAttemptCancellation = null;
                }
            }
        }

        /// <inheritdoc />
        void IControlConnectionAttemptHost.MarkAttemptConnected(
            long runGeneration,
            int attempt)
        {
            TransitionIfCurrent(
                runGeneration,
                ClientControlChannelState.Connected,
                ClientControlCloseReason.None,
                attempt);
        }

        /// <inheritdoc />
        bool IControlConnectionAttemptHost.TryPostAttemptPush(
            long connectionGeneration,
            ClientControlNotification notification)
        {
            return TryPostPush(connectionGeneration, notification);
        }

        /// <summary>
        /// 把强类型 PUSH 转移到主线程，并在执行前再次拒绝旧 run 的 callback。
        /// </summary>
        /// <param name="connectionGeneration">产生PUSH的具体run/attempt健康代际。</param>
        /// <param name="push">已通过完整 codec 校验的不可变消息。</param>
        /// <returns>Callback 已进入有界主线程队列时返回 true。</returns>
        private bool TryPostPush(
            long connectionGeneration,
            ClientControlNotification push)
        {
            lock (_sync)
            {
                if (_snapshot.Generation != connectionGeneration ||
                    _snapshot.State != ClientControlChannelState.Connected)
                {
                    return false;
                }
            }

            return _pushDispatcher.TryPost(
                connectionGeneration,
                push,
                IsCurrentConnectionGeneration,
                notification => DispatchPushIfCurrent(
                    connectionGeneration,
                    notification));
        }

        /// <summary>判断已排队 callback 是否仍属于 current connection generation。</summary>
        private bool IsCurrentConnectionGeneration(long connectionGeneration)
        {
            lock (_sync)
            {
                return _snapshot.Generation == connectionGeneration;
            }
        }

        /// <summary>
        /// 仅允许仍属于 current run 的已排队 PUSH 调用 subscriber。
        /// </summary>
        /// <param name="connectionGeneration">排队时捕获的具体run/attempt健康代际。</param>
        /// <param name="push">待通知的不可变 control PUSH。</param>
        private void DispatchPushIfCurrent(
            long connectionGeneration,
            ClientControlNotification push)
        {
            Action<ClientControlNotification> subscriber;
            lock (_sync)
            {
                if (_snapshot.Generation != connectionGeneration)
                {
                    return;
                }

                subscriber = PushReceived;
            }

            subscriber?.Invoke(push);
        }

        /// <summary>
        /// 判断 attempt outcome 是否允许消耗有限重连预算。
        /// </summary>
        /// <param name="kind">当前 attempt 固定结束分类。</param>
        /// <returns>Transport 或普通 peer close 时返回 true。</returns>
        private static bool IsRecoverable(ControlAttemptOutcomeKind kind)
        {
            return kind == ControlAttemptOutcomeKind.TransportFailure ||
                   kind == ControlAttemptOutcomeKind.PeerClosed;
        }

        /// <summary>
        /// 将内部 attempt 分类映射为公开安全关闭原因。
        /// </summary>
        /// <param name="kind">当前 attempt 固定结束分类。</param>
        /// <returns>不包含底层异常或 peer description 的原因。</returns>
        private static ClientControlCloseReason MapCloseReason(ControlAttemptOutcomeKind kind)
        {
            switch (kind)
            {
                case ControlAttemptOutcomeKind.Requested:
                    return ClientControlCloseReason.Requested;
                case ControlAttemptOutcomeKind.TransportFailure:
                    return ClientControlCloseReason.TransportFailure;
                case ControlAttemptOutcomeKind.PeerClosed:
                    return ClientControlCloseReason.PeerClosed;
                case ControlAttemptOutcomeKind.ProtocolFailure:
                    return ClientControlCloseReason.ProtocolFailure;
                case ControlAttemptOutcomeKind.MainThreadBackpressure:
                    return ClientControlCloseReason.MainThreadBackpressure;
                case ControlAttemptOutcomeKind.SessionInvalidated:
                    return ClientControlCloseReason.SessionInvalidated;
                case ControlAttemptOutcomeKind.SupersededConnection:
                    return ClientControlCloseReason.SupersededConnection;
                default:
                    return ClientControlCloseReason.PolicyRejected;
            }
        }

        /// <summary>
        /// 提交不可恢复 attempt 的稳定终态。
        /// </summary>
        /// <param name="runGeneration">本次 run 代际。</param>
        /// <param name="kind">不可恢复结束分类。</param>
        /// <param name="attempt">当前尝试序号。</param>
        private void CommitTerminalOutcome(
            long runGeneration,
            ControlAttemptOutcomeKind kind,
            int attempt)
        {
            TransitionIfCurrent(
                runGeneration,
                kind == ControlAttemptOutcomeKind.SessionInvalidated
                    ? ClientControlChannelState.SessionInvalidated
                    : ClientControlChannelState.Disconnected,
                MapCloseReason(kind),
                attempt);
        }

        /// <summary>
        /// 仅允许 current run generation 更新安全状态快照。
        /// </summary>
        /// <param name="runGeneration">发起更新的 run 代际。</param>
        /// <param name="state">待提交状态。</param>
        /// <param name="closeReason">待提交稳定原因。</param>
        /// <param name="attempt">当前尝试序号。</param>
        private void TransitionIfCurrent(
            long runGeneration,
            ClientControlChannelState state,
            ClientControlCloseReason closeReason,
            int attempt)
        {
            TaskCompletionSource<bool> readiness = null;
            bool? readinessResult = null;
            ClientControlChannelSnapshot committed = null;
            lock (_sync)
            {
                if (runGeneration == _runGeneration &&
                    _snapshot.State != ClientControlChannelState.Stopped)
                {
                    _snapshot = new ClientControlChannelSnapshot(
                        state,
                        closeReason,
                        ComposeHealthGeneration(runGeneration, attempt),
                        attempt);
                    committed = _snapshot;
                    if (state == ClientControlChannelState.Connected)
                    {
                        readiness = _connectionReadiness;
                        readinessResult = true;
                    }
                    else if (state == ClientControlChannelState.Disconnected ||
                             state == ClientControlChannelState.SessionInvalidated)
                    {
                        readiness = _connectionReadiness;
                        readinessResult = false;
                    }
                }
            }

            NotifyHealthChanged(committed);
            if (readinessResult.HasValue)
            {
                readiness?.TrySetResult(readinessResult.Value);
            }
        }

        /// <summary>在内部锁外发布低敏health snapshot。</summary>
        /// <param name="snapshot">已提交的不可变快照；未提交时为空。</param>
        private void NotifyHealthChanged(ClientControlChannelSnapshot snapshot)
        {
            if (snapshot != null)
            {
                HealthChanged?.Invoke(snapshot);
            }
        }

#if DEVELOPMENT_BUILD || UNITY_EDITOR
        /// <summary>计算单个event当前subscriber数量。</summary>
        /// <param name="subscribers">可空multicast delegate。</param>
        /// <returns>当前invocation list长度。</returns>
        private static int CountSubscribers(Delegate subscribers)
        {
            return subscribers?.GetInvocationList().Length ?? 0;
        }
#endif

        /// <summary>将run generation与attempt序号组合为单调递增的通道健康代际。</summary>
        /// <param name="runGeneration">当前control run generation。</param>
        /// <param name="attempt">当前run内从零开始递增的attempt序号。</param>
        /// <returns>可用于拒绝旧回调的复合健康代际；run尚未建立时返回零。</returns>
        private static long ComposeHealthGeneration(long runGeneration, int attempt)
        {
            return runGeneration <= 0
                ? 0
                : (runGeneration << 32) | unchecked((uint)Math.Max(attempt, 0));
        }

        /// <summary>
        /// 执行 stop cancellation、协议 close 与 run 收敛，并完成幂等 stop task。
        /// </summary>
        /// <param name="lifetimeCancellation">可为空的 App Scope 根取消 owner。</param>
        /// <param name="runTask">停止开始时捕获的可选 active run。</param>
        /// <param name="activeSocket">停止开始时捕获的可选 active socket。</param>
        /// <param name="cancellationToken">有界停止 deadline。</param>
        /// <param name="completion">全部重复 Stop 调用共享的 completion owner。</param>
        /// <returns>内部异步清理任务。</returns>
        private static async Task StopCoreAsync(
            CancellationTokenSource lifetimeCancellation,
            Task runTask,
            IClientWebSocket activeSocket,
            CancellationToken cancellationToken,
            TaskCompletionSource<bool> completion)
        {
            try
            {
                lifetimeCancellation?.Cancel();
                if (activeSocket != null)
                {
                    try
                    {
                        await activeSocket.CloseAsync(cancellationToken);
                    }
                    catch (ClientWebSocketTransportException)
                    {
                        // Socket dispose 与 receive cancellation 仍会完成所有权清理。
                    }
                }

                if (runTask != null)
                {
                    await runTask;
                }

                lifetimeCancellation?.Dispose();
                completion.TrySetResult(true);
            }
            catch (Exception exception)
            {
                completion.TrySetException(exception);
            }
        }
    }
}
