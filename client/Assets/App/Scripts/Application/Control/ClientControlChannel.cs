using System;
using System.Collections.Generic;
using System.Net;
using System.Net.WebSockets;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Core.Configuration;
using IHomeland.Client.Core.Lifetime;
using IHomeland.Client.Infrastructure.Http;
using IHomeland.Client.Infrastructure.WebSocket;
using IHomeland.Protocol.Control.V1;

namespace IHomeland.Client.Application.Control
{
    /// <summary>
    /// 提供 control channel 有限重连等待的可测试时间边界。
    /// </summary>
    internal interface IClientControlDelay
    {
        /// <summary>
        /// 等待一次可取消 backoff。
        /// </summary>
        /// <param name="delay">固定正数等待时长。</param>
        /// <param name="cancellationToken">显式运行或 App Scope 取消信号。</param>
        /// <returns>等待结束时完成的任务。</returns>
        Task DelayAsync(TimeSpan delay, CancellationToken cancellationToken);
    }

    /// <summary>
    /// 使用 BCL timer 实现 control channel backoff。
    /// </summary>
    internal sealed class SystemClientControlDelay : IClientControlDelay
    {
        /// <summary>
        /// 执行不会阻塞 Unity 主线程的异步等待。
        /// </summary>
        /// <param name="delay">固定正数等待时长。</param>
        /// <param name="cancellationToken">显式运行或 App Scope 取消信号。</param>
        /// <returns>等待结束时完成的任务。</returns>
        public Task DelayAsync(TimeSpan delay, CancellationToken cancellationToken)
        {
            return Task.Delay(delay, cancellationToken);
        }
    }

    /// <summary>
    /// 作为 App Scope 唯一 WSS control owner 协调 ticket、receive、主线程分发与有限恢复。
    /// </summary>
    /// <remarks>
    /// Mutable lifecycle state 由 <see cref="_sync"/> 保护，锁内不执行 HTTP、socket、delay 或
    /// subscriber callback。每个显式 run 只有一个 receive pump；run generation 与 ticket source
    /// generation 分别阻止旧网络 completion 和旧 session invalidation 覆盖后发状态。
    /// </remarks>
    internal sealed class ClientControlChannel : IAppLifetimeParticipant
    {
        /// <summary>
        /// 限制单次 WebSocket upgrade 等待，避免显式 run 永久停在 Connecting。
        /// </summary>
        private static readonly TimeSpan ConnectTimeout = TimeSpan.FromSeconds(10);

        /// <summary>
        /// 保存 receive/close 结果在恢复状态机中的内部分类。
        /// </summary>
        private enum AttemptOutcomeKind
        {
            /// <summary>
            /// 当前 operation 被显式或 App Scope 取消。
            /// </summary>
            Requested = 0,

            /// <summary>
            /// Socket connect/receive 出现可恢复 transport failure。
            /// </summary>
            TransportFailure = 1,

            /// <summary>
            /// Peer 在无失效语义时关闭连接。
            /// </summary>
            PeerClosed = 2,

            /// <summary>
            /// 冻结 frame、envelope、route 或 subprotocol 被违反。
            /// </summary>
            ProtocolFailure = 3,

            /// <summary>
            /// 主线程有界队列拒绝 control PUSH。
            /// </summary>
            MainThreadBackpressure = 4,

            /// <summary>
            /// Session owner 已提交 forced logout/invalidation。
            /// </summary>
            SessionInvalidated = 5,

            /// <summary>
            /// 旧连接试图失效已经被替换的 session generation。
            /// </summary>
            SupersededConnection = 6,

            /// <summary>
            /// 配置、session、ticket 或握手 policy 不允许连接。
            /// </summary>
            PolicyRejected = 7,
        }

        /// <summary>
        /// 保护 lifecycle snapshot、run generation、active socket 与 task identity。
        /// </summary>
        private readonly object _sync = new object();

        /// <summary>
        /// 决定 `ws` loopback 例外与 Production `wss` 安全策略。
        /// </summary>
        private readonly ClientEnvironment _environment;

        /// <summary>
        /// 提供同一次 bootstrap 发布的 realtime frame 上限。
        /// </summary>
        private readonly ClientConfigurationStore _configurationStore;

        /// <summary>
        /// 唯一拥有 session lineage、ticket 与 control invalidation 的 owner。
        /// </summary>
        private readonly SessionCoordinator _sessionCoordinator;

        /// <summary>
        /// 为每次 attempt 创建不继承旧 ticket/header 的独立 socket。
        /// </summary>
        private readonly IClientWebSocketFactory _socketFactory;

        /// <summary>
        /// 严格解码冻结 WSS routes 与 generated payload。
        /// </summary>
        private readonly ClientControlCodec _codec;

        /// <summary>
        /// 把网络 completion 有界转移到捕获的 Unity 主线程。
        /// </summary>
        private readonly MainThreadDispatcher _dispatcher;

        /// <summary>
        /// 提供不阻塞线程且可测试的有限 backoff。
        /// </summary>
        private readonly IClientControlDelay _delay;

        /// <summary>
        /// 保存每次瞬时故障后的固定重连延迟；长度即额外尝试上限。
        /// </summary>
        private readonly TimeSpan[] _retryDelays;

        /// <summary>
        /// 在统一 Session owner 接受更高 epoch 后同步撤销匹配 gameplay generation。
        /// </summary>
        private readonly Action<long> _gameplaySessionInvalidation;

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

        /// <summary>
        /// 保存当前安全可观察状态。
        /// </summary>
        private ClientControlChannelSnapshot _snapshot = new ClientControlChannelSnapshot(
            ClientControlChannelState.Created,
            ClientControlCloseReason.None,
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
            MainThreadDispatcher dispatcher,
            IClientControlDelay delay,
            IReadOnlyList<TimeSpan> retryDelays,
            Action<long> gameplaySessionInvalidation = null)
        {
            _environment = environment ?? throw new ArgumentNullException(nameof(environment));
            _configurationStore = configurationStore ?? throw new ArgumentNullException(nameof(configurationStore));
            _sessionCoordinator = sessionCoordinator ?? throw new ArgumentNullException(nameof(sessionCoordinator));
            _socketFactory = socketFactory ?? throw new ArgumentNullException(nameof(socketFactory));
            _codec = codec ?? throw new ArgumentNullException(nameof(codec));
            _dispatcher = dispatcher ?? throw new ArgumentNullException(nameof(dispatcher));
            _delay = delay ?? throw new ArgumentNullException(nameof(delay));
            _gameplaySessionInvalidation = gameplaySessionInvalidation;
            if (retryDelays == null)
            {
                throw new ArgumentNullException(nameof(retryDelays));
            }

            _retryDelays = new TimeSpan[retryDelays.Count];
            for (var index = 0; index < retryDelays.Count; index++)
            {
                if (retryDelays[index] <= TimeSpan.Zero)
                {
                    throw new ArgumentException("Control retry delay 必须为正数。", nameof(retryDelays));
                }

                _retryDelays[index] = retryDelays[index];
            }
        }

        /// <summary>
        /// 在主线程投递合法 generated control PUSH；网络线程不会直接调用 subscriber。
        /// </summary>
        internal event Action<ClientControlPush> PushReceived;

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

        /// <summary>
        /// 启用显式运行入口但不签发 ticket 或建立连接。
        /// </summary>
        /// <param name="cancellationToken">初始化前检查的 AppLifetime 取消信号。</param>
        /// <returns>Owner 已进入 Idle 时完成的任务。</returns>
        /// <exception cref="InvalidOperationException">重复初始化或停止后重启时抛出。</exception>
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
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
                    0);
            }

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
            lock (_sync)
            {
                if (_lifetimeCancellation == null ||
                    _snapshot.State == ClientControlChannelState.Stopped)
                {
                    throw new InvalidOperationException("ClientControlChannel 当前生命周期不允许运行。");
                }

                if (_runTask != null && !_runTask.IsCompleted)
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
                    1);
                _connectionReadiness = new TaskCompletionSource<bool>(
                    TaskCreationOptions.RunContinuationsAsynchronously);
                _runTask = RunCoreAsync(runGeneration, linkedCancellation);
                return _runTask;
            }
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
                    _snapshot.Attempt);
                lifetimeCancellation = _lifetimeCancellation;
                runTask = _runTask;
                activeSocket = _activeSocket;
                connectionReadiness = _connectionReadiness;
            }

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

                    var outcome = await RunAttemptAsync(runGeneration, cancellationToken);
                    if (outcome == AttemptOutcomeKind.Requested)
                    {
                        TransitionIfCurrent(
                            runGeneration,
                            ClientControlChannelState.Disconnected,
                            ClientControlCloseReason.Requested,
                            attempt);
                        return;
                    }

                    if (!IsRecoverable(outcome))
                    {
                        CommitTerminalOutcome(runGeneration, outcome, attempt);
                        return;
                    }

                    if (retryIndex >= _retryDelays.Length)
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
                        MapCloseReason(outcome),
                        attempt);
                    await _delay.DelayAsync(_retryDelays[retryIndex], cancellationToken);
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
        /// <param name="cancellationToken">当前 run 的组合取消信号。</param>
        /// <returns>决定是否恢复的稳定 attempt outcome。</returns>
        private async Task<AttemptOutcomeKind> RunAttemptAsync(
            long runGeneration,
            CancellationToken cancellationToken)
        {
            if (!_configurationStore.TryGetCurrent(out var configuration) ||
                configuration.Configuration.Limits.RealtimeFrameBytes <= 0)
            {
                return AttemptOutcomeKind.PolicyRejected;
            }

            var ticketResult = await _sessionCoordinator.IssueConnectionTicketAsync(
                ClientEndpointChannel.Wss,
                cancellationToken);
            if (!ticketResult.IsSuccess ||
                !_sessionCoordinator.TryTakeConnectionTicket(ticketResult.Value, out var ticketUse) ||
                !IsControlTicket(ticketUse))
            {
                return AttemptOutcomeKind.PolicyRejected;
            }

            ClientWebSocketConnectRequest request;
            try
            {
                request = BuildConnectRequest(ticketUse);
            }
            catch (ArgumentException)
            {
                return AttemptOutcomeKind.PolicyRejected;
            }

            var socket = _socketFactory.Create();
            if (socket == null)
            {
                return AttemptOutcomeKind.PolicyRejected;
            }

            lock (_sync)
            {
                if (runGeneration != _runGeneration ||
                    _snapshot.State == ClientControlChannelState.Stopped)
                {
                    socket.Dispose();
                    return AttemptOutcomeKind.Requested;
                }

                _activeSocket = socket;
            }

            try
            {
                using (var connectCancellation = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken))
                {
                    connectCancellation.CancelAfter(ConnectTimeout);
                    try
                    {
                        await socket.ConnectAsync(request, connectCancellation.Token);
                    }
                    catch (OperationCanceledException) when (!cancellationToken.IsCancellationRequested)
                    {
                        return AttemptOutcomeKind.TransportFailure;
                    }
                }

                if (!string.Equals(
                        socket.SubProtocol,
                        ClientWebSocketConnectRequest.ControlSubprotocol,
                        StringComparison.Ordinal))
                {
                    return AttemptOutcomeKind.ProtocolFailure;
                }

                TransitionIfCurrent(
                    runGeneration,
                    ClientControlChannelState.Connected,
                    ClientControlCloseReason.None,
                    Snapshot.Attempt);
                return await ReceiveAsync(
                    socket,
                    runGeneration,
                    ticketUse.SourceGeneration,
                    configuration.Configuration.Limits.RealtimeFrameBytes,
                    cancellationToken);
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
            {
                return AttemptOutcomeKind.Requested;
            }
            catch (ClientWebSocketTransportException)
            {
                return AttemptOutcomeKind.TransportFailure;
            }
            finally
            {
                lock (_sync)
                {
                    if (ReferenceEquals(_activeSocket, socket))
                    {
                        _activeSocket = null;
                    }
                }

                socket.Dispose();
            }
        }

        /// <summary>
        /// 运行单 connection receive pump，完成 fragment 重组、sequence 与主线程投递。
        /// </summary>
        /// <param name="socket">当前 attempt 独占且已协商 subprotocol 的 socket。</param>
        /// <param name="runGeneration">阻止旧 run 排队或执行迟到主线程 callback 的代际。</param>
        /// <param name="sourceSessionGeneration">Ticket 取得时仍 current 的 session generation。</param>
        /// <param name="maximumFrameBytes">Bootstrap 公布的全局 frame 上限。</param>
        /// <param name="cancellationToken">当前 run 的组合取消信号。</param>
        /// <returns>Receive pump 的稳定结束分类。</returns>
        private async Task<AttemptOutcomeKind> ReceiveAsync(
            IClientWebSocket socket,
            long runGeneration,
            long sourceSessionGeneration,
            int maximumFrameBytes,
            CancellationToken cancellationToken)
        {
            var frame = new byte[maximumFrameBytes];
            ulong expectedSequence = 1;
            while (true)
            {
                var length = 0;
                while (true)
                {
                    if (length >= frame.Length)
                    {
                        return AttemptOutcomeKind.ProtocolFailure;
                    }

                    var result = await socket.ReceiveAsync(
                        new ArraySegment<byte>(frame, length, frame.Length - length),
                        cancellationToken);
                    if (result.MessageType == WebSocketMessageType.Close)
                    {
                        return IsProtocolClose(result.CloseStatus)
                            ? AttemptOutcomeKind.ProtocolFailure
                            : AttemptOutcomeKind.PeerClosed;
                    }

                    if (result.MessageType != WebSocketMessageType.Binary ||
                        result.Count < 0 ||
                        result.Count > frame.Length - length ||
                        (result.Count == 0 && !result.EndOfMessage))
                    {
                        return AttemptOutcomeKind.ProtocolFailure;
                    }

                    length += result.Count;
                    if (!result.EndOfMessage)
                    {
                        continue;
                    }

                    break;
                }

                ClientControlPush push;
                try
                {
                    push = _codec.Decode(frame, length, maximumFrameBytes, expectedSequence);
                }
                catch (ClientControlProtocolException)
                {
                    return AttemptOutcomeKind.ProtocolFailure;
                }

                expectedSequence++;
                if (TryGetInvalidatedEpoch(push, out var invalidatedEpoch))
                {
                    var invalidated = _sessionCoordinator.TryInvalidateFromControl(
                        sourceSessionGeneration,
                        invalidatedEpoch);
                    if (invalidated)
                    {
                        _gameplaySessionInvalidation?.Invoke(sourceSessionGeneration);
                    }
                    // Session authority transition 已经决定终态；通知投递失败不能把结果降级为背压。
                    _ = TryPostPush(runGeneration, push);
                    return invalidated
                        ? AttemptOutcomeKind.SessionInvalidated
                        : AttemptOutcomeKind.SupersededConnection;
                }

                if (!TryPostPush(runGeneration, push))
                {
                    return AttemptOutcomeKind.MainThreadBackpressure;
                }
            }
        }

        /// <summary>
        /// 把强类型 PUSH 转移到主线程，并在执行前再次拒绝旧 run 的 callback。
        /// </summary>
        /// <param name="runGeneration">产生该 PUSH 的显式 run 代际。</param>
        /// <param name="push">已通过完整 codec 校验的不可变消息。</param>
        /// <returns>Callback 已进入有界主线程队列时返回 true。</returns>
        private bool TryPostPush(long runGeneration, ClientControlPush push)
        {
            lock (_sync)
            {
                if (runGeneration != _runGeneration ||
                    _snapshot.State == ClientControlChannelState.Stopped)
                {
                    return false;
                }
            }

            return _dispatcher.TryPost(
                () => DispatchPushIfCurrent(runGeneration, push)) == DispatchPostResult.Accepted;
        }

        /// <summary>
        /// 仅允许仍属于 current run 的已排队 PUSH 调用 subscriber。
        /// </summary>
        /// <param name="runGeneration">排队时捕获的显式 run 代际。</param>
        /// <param name="push">待通知的不可变 control PUSH。</param>
        private void DispatchPushIfCurrent(long runGeneration, ClientControlPush push)
        {
            Action<ClientControlPush> subscriber;
            lock (_sync)
            {
                if (runGeneration != _runGeneration ||
                    _snapshot.State == ClientControlChannelState.Stopped)
                {
                    return;
                }

                subscriber = PushReceived;
            }

            subscriber?.Invoke(push);
        }

        /// <summary>
        /// 从仅有的两类权威失效 PUSH 提取新 session epoch。
        /// </summary>
        /// <param name="push">已按 route 精确解析的 control PUSH。</param>
        /// <param name="invalidatedEpoch">成功时返回服务端公布的新 epoch。</param>
        /// <returns>Payload 是 forced logout 或 session invalidated 时返回 true。</returns>
        private static bool TryGetInvalidatedEpoch(
            ClientControlPush push,
            out ulong invalidatedEpoch)
        {
            if (push.Payload is ForcedLogoutPush forcedLogout)
            {
                invalidatedEpoch = forcedLogout.SessionEpoch;
                return true;
            }

            if (push.Payload is SessionInvalidatedPush sessionInvalidated)
            {
                invalidatedEpoch = sessionInvalidated.SessionEpoch;
                return true;
            }

            invalidatedEpoch = 0;
            return false;
        }

        /// <summary>
        /// 从 ticket 绑定 endpoint 与当前环境安全策略构造冻结 control request。
        /// </summary>
        /// <param name="ticketUse">已从 current lease 单次取得的 ticket 所有权。</param>
        /// <returns>固定 path、scheme 与 credential 的 connect request。</returns>
        /// <exception cref="ArgumentException">明文 endpoint 不是 Local/Test loopback 时抛出。</exception>
        private ClientWebSocketConnectRequest BuildConnectRequest(ClientConnectionTicketUse ticketUse)
        {
            var useSecureWebSocket = string.Equals(
                _environment.HttpBaseUri.Scheme,
                Uri.UriSchemeHttps,
                StringComparison.OrdinalIgnoreCase);
            var builder = new UriBuilder(
                useSecureWebSocket ? "wss" : "ws",
                ticketUse.Endpoint.Host,
                ticketUse.Endpoint.Port,
                ClientWebSocketConnectRequest.ControlPath);
            var endpoint = builder.Uri;
            if (!useSecureWebSocket &&
                (_environment.EnvironmentKind == ClientEnvironmentKind.Production ||
                 !IsLoopback(endpoint.Host)))
            {
                throw new ArgumentException("明文 WSS endpoint 只允许 Local/Test loopback。");
            }

            return new ClientWebSocketConnectRequest(endpoint, ticketUse.Credential);
        }

        /// <summary>
        /// 验证 ticket 只允许 WSS endpoint 与 CONTROL scope。
        /// </summary>
        /// <param name="ticketUse">已单次取得的 ticket 使用权。</param>
        /// <returns>Channel 与唯一 scope 精确匹配时返回 true。</returns>
        private static bool IsControlTicket(ClientConnectionTicketUse ticketUse)
        {
            return ticketUse.Endpoint.Channel == ClientEndpointChannel.Wss &&
                   ticketUse.Scopes.Count == 1 &&
                   ticketUse.Scopes[0] == ClientConnectionScope.Control;
        }

        /// <summary>
        /// 判断 host 是否为 loopback IP 或保留的 localhost 名称。
        /// </summary>
        /// <param name="host">Ticket endpoint 中的已验证 host。</param>
        /// <returns>Host 只能解析为本机回环时返回 true。</returns>
        private static bool IsLoopback(string host)
        {
            if (string.Equals(host, "localhost", StringComparison.OrdinalIgnoreCase))
            {
                return true;
            }

            return IPAddress.TryParse(host, out var address) && IPAddress.IsLoopback(address);
        }

        /// <summary>
        /// 判断 peer close code 是否表示不可恢复的协议或 policy failure。
        /// </summary>
        /// <param name="closeStatus">平台提供的可选标准 close status。</param>
        /// <returns>该连接不应自动重试时返回 true。</returns>
        private static bool IsProtocolClose(WebSocketCloseStatus? closeStatus)
        {
            return closeStatus == WebSocketCloseStatus.ProtocolError ||
                   closeStatus == WebSocketCloseStatus.InvalidMessageType ||
                   closeStatus == WebSocketCloseStatus.InvalidPayloadData ||
                   closeStatus == WebSocketCloseStatus.PolicyViolation ||
                   closeStatus == WebSocketCloseStatus.MessageTooBig ||
                   closeStatus == WebSocketCloseStatus.MandatoryExtension;
        }

        /// <summary>
        /// 判断 attempt outcome 是否允许消耗有限重连预算。
        /// </summary>
        /// <param name="kind">当前 attempt 固定结束分类。</param>
        /// <returns>Transport 或普通 peer close 时返回 true。</returns>
        private static bool IsRecoverable(AttemptOutcomeKind kind)
        {
            return kind == AttemptOutcomeKind.TransportFailure ||
                   kind == AttemptOutcomeKind.PeerClosed;
        }

        /// <summary>
        /// 将内部 attempt 分类映射为公开安全关闭原因。
        /// </summary>
        /// <param name="kind">当前 attempt 固定结束分类。</param>
        /// <returns>不包含底层异常或 peer description 的原因。</returns>
        private static ClientControlCloseReason MapCloseReason(AttemptOutcomeKind kind)
        {
            switch (kind)
            {
                case AttemptOutcomeKind.Requested:
                    return ClientControlCloseReason.Requested;
                case AttemptOutcomeKind.TransportFailure:
                    return ClientControlCloseReason.TransportFailure;
                case AttemptOutcomeKind.PeerClosed:
                    return ClientControlCloseReason.PeerClosed;
                case AttemptOutcomeKind.ProtocolFailure:
                    return ClientControlCloseReason.ProtocolFailure;
                case AttemptOutcomeKind.MainThreadBackpressure:
                    return ClientControlCloseReason.MainThreadBackpressure;
                case AttemptOutcomeKind.SessionInvalidated:
                    return ClientControlCloseReason.SessionInvalidated;
                case AttemptOutcomeKind.SupersededConnection:
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
            AttemptOutcomeKind kind,
            int attempt)
        {
            TransitionIfCurrent(
                runGeneration,
                kind == AttemptOutcomeKind.SessionInvalidated
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
            lock (_sync)
            {
                if (runGeneration == _runGeneration &&
                    _snapshot.State != ClientControlChannelState.Stopped)
                {
                    _snapshot = new ClientControlChannelSnapshot(state, closeReason, attempt);
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

            if (readinessResult.HasValue)
            {
                readiness?.TrySetResult(readinessResult.Value);
            }
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
