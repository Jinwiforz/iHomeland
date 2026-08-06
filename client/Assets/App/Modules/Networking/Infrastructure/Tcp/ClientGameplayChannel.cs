using System;
using System.Collections.Generic;
using System.Security.Cryptography;
using System.Threading;
using System.Threading.Tasks;
using Google.Protobuf;
using IHomeland.Client.Session.Application;
using IHomeland.Client.Networking.Application.Gameplay;
using IHomeland.Client.AppShell.Application.Configuration;
using IHomeland.Client.Core.Foundation.Lifetime;
using IHomeland.Client.Core.Foundation.Threading;
using IHomeland.Client.Core.Foundation.Time;
using IHomeland.Client.Networking.Application.Contracts;
using IHomeland.Client.Networking.Infrastructure.Http;
using IHomeland.Client.Networking.Infrastructure.Tcp;
using IHomeland.Protocol.Common.V1;
using IHomeland.Protocol.Visit.V1;
using IHomeland.Protocol.World.V1;

namespace IHomeland.Client.Networking.Infrastructure.Tcp
{
    /// <summary>
    /// 作为 App Scope 唯一 gameplay owner 协调双凭据、I/O、pending correlation 与 typed PUSH。
    /// </summary>
    /// <remarks>
    /// Mutable state 由 <see cref="_sync"/> 保护，锁内不执行 HTTP、socket、subscriber callback 或 await。
    /// 每个 generation 恰好一个 reader pump 与一个 serialized writer；任何失败均撤销整个 generation。
    /// </remarks>
    internal sealed class ClientGameplayChannel : IAppLifetimeParticipant
    {
        /// <summary>限制每个 connection 同时等待的 operation 数量。</summary>
        private const int PendingCapacity = 128;

        /// <summary>限制 serialized writer 等待 frame 数量。</summary>
        private const int WriterItemCapacity = 64;

        /// <summary>限制 serialized writer 编码后总字节数。</summary>
        private const int WriterByteCapacity = 128 * 1024;

        /// <summary>保护 lifecycle、active connection、pending 与 writer queue。</summary>
        private readonly object _sync = new object();

        /// <summary>提供 realtime frame 配置 hard cap。</summary>
        private readonly ClientConfigurationStore _configurationStore;

        /// <summary>拥有 admission、ticket、TLS connect 与 preface write。</summary>
        private readonly GameplayConnectionAttempt _connectionAttempt;

        /// <summary>编码/解码冻结 envelope contract。</summary>
        private readonly ClientGameplayCodec _codec;

        /// <summary>拥有 frame decode、S2C sequence 与 terminal 分类。</summary>
        private readonly GameplayReaderPump _readerPump;

        /// <summary>将 PUSH callback 有界转移到 Unity 主线程。</summary>
        private readonly IClientMainThreadDispatcher _dispatcher;

        /// <summary>拥有 response/error 与三个 PUSH 的固定 typed route。</summary>
        private readonly GameplayRouteDispatcher _routeDispatcher;

        /// <summary>拥有 generation-scoped idle timer 与 heartbeat terminal 决议。</summary>
        private readonly GameplayHeartbeat _heartbeat;

        /// <summary>拥有有界 FIFO frame queue 与 writer signal。</summary>
        private readonly GameplayWriter _writer =
            new GameplayWriter(WriterItemCapacity, WriterByteCapacity);

        /// <summary>按不可预测 correlation 索引有界 pending。</summary>
        private readonly GameplayPendingRegistry _pending =
            new GameplayPendingRegistry(PendingCapacity);

        /// <summary>AppLifetime 初始化后拥有的根取消信号。</summary>
        private CancellationTokenSource _lifetimeCancellation;

        /// <summary>Current connection generation 的独立取消信号。</summary>
        private CancellationTokenSource _connectionCancellation;

        /// <summary>Current generation 独占 connection。</summary>
        private IClientGameplayConnection _connection;

        /// <summary>Current receive pump。</summary>
        private Task _readerTask = Task.CompletedTask;

        /// <summary>Current serialized writer。</summary>
        private Task _writerTask = Task.CompletedTask;

        /// <summary>Current active generation 的唯一 heartbeat owner。</summary>
        private Task _heartbeatTask = Task.CompletedTask;

        /// <summary>Current generation C2S 下一个 sequence。</summary>
        private ulong _nextClientSequence;

        /// <summary>阻止旧异步 completion 回写后发连接。</summary>
        private long _generation;

        /// <summary>Current connection 来源 session generation。</summary>
        private long _sourceSessionGeneration;

        /// <summary>JOIN/RECONNECT 首帧验证完成前保存同一 admission；完成后立即清除引用。</summary>
        private string _pendingAdmission;

        /// <summary>Current connection 的服务端权威 purpose。</summary>
        private ClientWorldAdmissionPurpose _purpose;

        /// <summary>Safe-return 后阻止旧 target mutation。</summary>
        private bool _acceptingMutations;

        /// <summary>保存低敏可观察状态。</summary>
        private ClientGameplayChannelSnapshot _snapshot = new ClientGameplayChannelSnapshot(
            ClientGameplayChannelState.Created,
            ClientGameplayCloseReason.None,
            0);

        /// <summary>创建不自动联网的 gameplay owner。</summary>
        /// <param name="configurationStore">公开配置 owner。</param>
        /// <param name="sessionCoordinator">统一 Session owner。</param>
        /// <param name="connectionFactory">TCP/TLS transport factory。</param>
        /// <param name="codec">冻结 gameplay codec。</param>
        /// <param name="dispatcher">Unity 主线程有界 dispatcher。</param>
        /// <param name="heartbeatDelay">可撤销 heartbeat scheduler。</param>
        internal ClientGameplayChannel(
            ClientConfigurationStore configurationStore,
            SessionCoordinator sessionCoordinator,
            IClientGameplayConnectionFactory connectionFactory,
            ClientGameplayCodec codec,
            IClientMainThreadDispatcher dispatcher,
            IClientDelay heartbeatDelay)
        {
            _configurationStore = configurationStore ?? throw new ArgumentNullException(nameof(configurationStore));
            _connectionAttempt = new GameplayConnectionAttempt(
                sessionCoordinator,
                connectionFactory);
            _codec = codec ?? throw new ArgumentNullException(nameof(codec));
            _readerPump = new GameplayReaderPump(_configurationStore, _codec);
            _dispatcher = dispatcher ?? throw new ArgumentNullException(nameof(dispatcher));
            _routeDispatcher = new GameplayRouteDispatcher(_codec, _dispatcher);
            _heartbeat = new GameplayHeartbeat(heartbeatDelay);
        }

        /// <summary>在主线程投递 PersonalWorld 完整替换 PUSH。</summary>
        internal event Action<WorldSnapshotPush> WorldSnapshotReceived;

        /// <summary>在主线程投递 VisitSession 完整替换 PUSH。</summary>
        internal event Action<VisitSnapshotPush> VisitSnapshotReceived;

        /// <summary>在主线程投递权威 safe-return PUSH。</summary>
        internal event Action<VisitSafeReturnPush> SafeReturnReceived;

        /// <summary>在主线程发布 current generation 的非预期 terminal disconnect。</summary>
        internal event Action<ClientGameplayChannelSnapshot> UnexpectedDisconnect;

        /// <summary>在主线程发布不含敏感值的 Development 诊断。</summary>
        internal event Action<ClientGameplayDiagnostic> DiagnosticRecorded;

        /// <summary>获取不包含 endpoint、credential、payload 或 correlation 的状态快照。</summary>
        internal ClientGameplayChannelSnapshot Snapshot
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
        /// <summary>获取资格运行可观察的current socket owner数量。</summary>
        internal int QualificationSocketOwnerCount
        {
            get
            {
                lock (_sync)
                {
                    return _connection != null ? 1 : 0;
                }
            }
        }

        /// <summary>获取资格运行可观察的current heartbeat owner数量。</summary>
        internal int QualificationHeartbeatOwnerCount
        {
            get
            {
                lock (_sync)
                {
                    return _heartbeatTask != null && !_heartbeatTask.IsCompleted ? 1 : 0;
                }
            }
        }

        /// <summary>获取资格运行可观察的current correlation pending数量。</summary>
        internal int QualificationPendingOperationCount
        {
            get
            {
                lock (_sync)
                {
                    return _pending.Count;
                }
            }
        }

        /// <summary>获取资格范围内gameplay subscriber总数。</summary>
        internal int QualificationSubscriptionCount
        {
            get
            {
                lock (_sync)
                {
                    return CountSubscribers(WorldSnapshotReceived) +
                           CountSubscribers(VisitSnapshotReceived) +
                           CountSubscribers(SafeReturnReceived) +
                           CountSubscribers(UnexpectedDisconnect) +
                           CountSubscribers(DiagnosticRecorded);
                }
            }
        }

        /// <summary>为Development资格运行提交一次current generation的transport terminal事实。</summary>
        /// <returns>Current generation处于Active并已开始关闭时返回true。</returns>
        internal bool InjectQualificationTransportDisconnect()
        {
            long generation;
            lock (_sync)
            {
                if (_snapshot.State != ClientGameplayChannelState.Active)
                {
                    return false;
                }

                generation = _generation;
            }

            BeginClose(generation, ClientGameplayCloseReason.Transport);
            return true;
        }
#endif

        /// <summary>
        /// 由唯一 protocol adapter 在 generated-to-Application 映射失败时终止 current generation。
        /// </summary>
        internal void InvalidateProtocolInput()
        {
            long generation;
            lock (_sync)
            {
                generation = _generation;
            }

            BeginClose(generation, ClientGameplayCloseReason.Protocol);
        }

        /// <summary>启用显式 connect 入口，但不签发 credential 或创建 socket。</summary>
        /// <param name="cancellationToken">初始化前检查的 AppLifetime 信号。</param>
        /// <returns>Channel 已进入 Ready 时完成。</returns>
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            lock (_sync)
            {
                if (_snapshot.State != ClientGameplayChannelState.Created)
                {
                    throw new InvalidOperationException("Gameplay channel 不能重复初始化。");
                }

                _lifetimeCancellation = new CancellationTokenSource();
                SetSnapshotLocked(ClientGameplayChannelState.Ready, ClientGameplayCloseReason.None);
            }

            return Task.CompletedTask;
        }

        /// <summary>显式消费 admission lease、签发匹配 ticket 并建立一个 gameplay generation。</summary>
        /// <param name="admissionLease">当前 session generation 的一次性 admission lease。</param>
        /// <param name="cancellationToken">调用方连接取消信号。</param>
        /// <returns>完成 preface 写入并启动 I/O owner 时返回 true。</returns>
        internal async Task<bool> ConnectAsync(
            ClientWorldAdmissionLease admissionLease,
            CancellationToken cancellationToken)
        {
            if (admissionLease == null)
            {
                throw new ArgumentNullException(nameof(admissionLease));
            }

            long generation;
            lock (_sync)
            {
                // 旧 connection 虽已撤销资源，pump 仍可能正在退出；先观察完成，避免覆盖 App shutdown 需要等待的 task。
                if (_snapshot.State != ClientGameplayChannelState.Ready ||
                    !_readerTask.IsCompleted ||
                    !_writerTask.IsCompleted ||
                    !_heartbeatTask.IsCompleted ||
                    !_configurationStore.TryGetCurrent(out _))
                {
                    return false;
                }

                generation = ++_generation;
                SetSnapshotLocked(ClientGameplayChannelState.Connecting, ClientGameplayCloseReason.None);
            }

            CancellationToken lifetimeToken;
            lock (_sync)
            {
                lifetimeToken = _lifetimeCancellation.Token;
            }

            var attempt = await _connectionAttempt.RunAsync(
                admissionLease,
                lifetimeToken,
                cancellationToken);
            if (!attempt.IsSuccess)
            {
                RecordDiagnostic(
                    generation,
                    attempt.Stage,
                    attempt.Reason,
                    attempt.Exception);
                ResetConnecting(generation, attempt.Reason);
                return false;
            }

            using (var candidate = attempt.Candidate)
            {
                var connectionCancellation =
                    CancellationTokenSource.CreateLinkedTokenSource(lifetimeToken);
                lock (_sync)
                {
                    if (_generation != generation ||
                        _snapshot.State != ClientGameplayChannelState.Connecting)
                    {
                        connectionCancellation.Dispose();
                        return false;
                    }

                    _connection = candidate.TakeConnection();
                    _connectionCancellation = connectionCancellation;
                    _sourceSessionGeneration = candidate.SourceSessionGeneration;
                    _purpose = candidate.Purpose;
                    _pendingAdmission = candidate.PendingAdmission;
                    _acceptingMutations = true;
                    _nextClientSequence = 1;
                    var state = _purpose == ClientWorldAdmissionPurpose.OwnWorld
                        ? ClientGameplayChannelState.Active
                        : ClientGameplayChannelState.Pending;
                    SetSnapshotLocked(state, ClientGameplayCloseReason.None);
                    _readerTask = ReaderPumpAsync(generation, _connectionCancellation.Token);
                    _writerTask = WriterPumpAsync(generation, _connectionCancellation.Token);
                    if (state == ClientGameplayChannelState.Active)
                    {
                        StartHeartbeatLocked(generation, _connectionCancellation.Token);
                    }
                }
            }

            return true;
        }

        /// <summary>发送冻结 operation 并等待唯一 response/error。</summary>
        /// <typeparam name="TRequest">生成 request/command 类型。</typeparam>
        /// <typeparam name="TResponse">生成 response 类型。</typeparam>
        /// <param name="operation">Catalog 提供的强类型 descriptor。</param>
        /// <param name="payload">本次生成 payload。</param>
        /// <param name="cancellationToken">只取消当前调用方等待。</param>
        /// <returns>成功、服务端拒绝或稳定本地失败。</returns>
        internal async Task<ClientGameplayResult<TResponse>> SendAsync<TRequest, TResponse>(
            ClientGameplayOperation<TRequest, TResponse> operation,
            TRequest payload,
            CancellationToken cancellationToken)
            where TRequest : class, IMessage<TRequest>
            where TResponse : class, IMessage<TResponse>
        {
            if (operation == null || payload == null)
            {
                throw new ArgumentNullException(operation == null ? nameof(operation) : nameof(payload));
            }

            GameplayPendingOperation pending = null;
            long backpressureGeneration = 0;
            long pendingGeneration = 0;
            lock (_sync)
            {
                if (!CanSendLocked(operation.RequestMessageID, payload))
                {
                    return ClientGameplayResult<TResponse>.Failed(ClientGameplayFailureKind.Policy);
                }

                var correlation = NewCorrelationID();
                var encoded = _codec.Encode(
                    operation,
                    payload,
                    correlation,
                    _nextClientSequence,
                    DateTimeOffset.UtcNow.ToUnixTimeMilliseconds());
                var frame = ClientGameplayFramer.Frame(encoded);
                if (!_writer.CanEnqueue(frame.Length))
                {
                    backpressureGeneration = _generation;
                }
                else
                {
                    _nextClientSequence++;
                    var key = Convert.ToBase64String(correlation);
                    var activates = _snapshot.State == ClientGameplayChannelState.Pending;
                    pending = new GameplayPendingOperation(
                        operation.ResponseMessageID,
                        operation.RequestKind,
                        envelope => _codec.DecodeResponse(operation, envelope),
                        activates);
                    if (!_pending.TryAdd(key, pending))
                    {
                        return ClientGameplayResult<TResponse>.Failed(
                            ClientGameplayFailureKind.Backpressure);
                    }
                    pendingGeneration = _generation;
                    if (activates)
                    {
                        // 首个 JOIN/RECONNECT command 一旦取得发送所有权，后续 command 必须等待结果。
                        _pendingAdmission = null;
                    }

                    _writer.Enqueue(frame);
                }
            }

            if (backpressureGeneration != 0)
            {
                BeginClose(backpressureGeneration, ClientGameplayCloseReason.Backpressure);
                return ClientGameplayResult<TResponse>.Failed(ClientGameplayFailureKind.Backpressure);
            }

            using (var timeout = new CancellationTokenSource(operation.Timeout))
            using (var linked = CancellationTokenSource.CreateLinkedTokenSource(
                       cancellationToken,
                       timeout.Token))
            {
                var cancellationSignal = new TaskCompletionSource<bool>(
                    TaskCreationOptions.RunContinuationsAsynchronously);
                using (linked.Token.Register(() => cancellationSignal.TrySetResult(true)))
                {
                    var completed = await Task.WhenAny(pending.Completion.Task, cancellationSignal.Task);
                    if (!ReferenceEquals(completed, pending.Completion.Task))
                    {
                        var failure = cancellationToken.IsCancellationRequested
                            ? ClientGameplayFailureKind.CallerCancelled
                            : ClientGameplayFailureKind.Timeout;
                        if (pending.Completion.TrySetResult(GameplayPendingCompletion.Failed(failure)))
                        {
                            if (failure == ClientGameplayFailureKind.Timeout)
                            {
                                RecordDiagnostic(
                                    pendingGeneration,
                                    ClientGameplayDiagnosticStage.Pending,
                                    ClientGameplayCloseReason.Timeout,
                                    null);
                            }

                            return ClientGameplayResult<TResponse>.Failed(failure);
                        }
                    }
                }
            }

            var completion = await pending.Completion.Task;
            if (completion.Value is TResponse value)
            {
                return ClientGameplayResult<TResponse>.Success(value);
            }

            if (completion.Error != null)
            {
                return ClientGameplayResult<TResponse>.Rejected(completion.Error);
            }

            return ClientGameplayResult<TResponse>.Failed(completion.Failure);
        }

        /// <summary>
        /// 使用 channel 内部保存的一次性 JOIN admission 构造首个 command，避免 credential 逸出到 coordinator。
        /// </summary>
        /// <param name="expectedRevision">Reservation 后 current VisitSession revision。</param>
        /// <param name="cancellationToken">只取消调用方等待，不重发 mutation。</param>
        /// <returns>强类型 JOIN result。</returns>
        internal Task<ClientGameplayResult<VisitJoinResponse>> JoinPendingVisitAsync(
            ulong expectedRevision,
            CancellationToken cancellationToken)
        {
            string admission;
            lock (_sync)
            {
                if (expectedRevision == 0 ||
                    _snapshot.State != ClientGameplayChannelState.Pending ||
                    _purpose != ClientWorldAdmissionPurpose.Join ||
                    string.IsNullOrEmpty(_pendingAdmission))
                {
                    return Task.FromResult(
                        ClientGameplayResult<VisitJoinResponse>.Failed(ClientGameplayFailureKind.Policy));
                }

                admission = _pendingAdmission;
            }

            return SendAsync(
                ClientGameplayCatalog.VisitJoin,
                new VisitJoinCommand
                {
                    AdmissionCredential = admission,
                    ExpectedRevision = expectedRevision,
                },
                cancellationToken);
        }

        /// <summary>
        /// 使用 channel 内部保存的一次性 RECONNECT admission 构造首个 command，避免 credential 逸出到 Service。
        /// </summary>
        /// <param name="expectedRevision">Reconnect window 对应 current revision。</param>
        /// <param name="cancellationToken">只取消调用方等待，不重发 mutation。</param>
        /// <returns>强类型 RECONNECT result。</returns>
        internal Task<ClientGameplayResult<VisitReconnectResponse>> ReconnectPendingVisitAsync(
            ulong expectedRevision,
            CancellationToken cancellationToken)
        {
            string admission;
            lock (_sync)
            {
                if (expectedRevision == 0 ||
                    _snapshot.State != ClientGameplayChannelState.Pending ||
                    _purpose != ClientWorldAdmissionPurpose.Reconnect ||
                    string.IsNullOrEmpty(_pendingAdmission))
                {
                    return Task.FromResult(
                        ClientGameplayResult<VisitReconnectResponse>.Failed(ClientGameplayFailureKind.Policy));
                }

                admission = _pendingAdmission;
            }

            return SendAsync(
                ClientGameplayCatalog.VisitReconnect,
                new VisitReconnectCommand
                {
                    AdmissionCredential = admission,
                    ExpectedRevision = expectedRevision,
                },
                cancellationToken);
        }

        /// <summary>由 WSS control 在统一 Session owner 接受更高 epoch 后关闭匹配 gameplay generation。</summary>
        /// <param name="sourceSessionGeneration">被失效 control connection 的来源 generation。</param>
        internal void InvalidateSession(long sourceSessionGeneration)
        {
            lock (_sync)
            {
                if (_sourceSessionGeneration == sourceSessionGeneration)
                {
                    BeginCloseLocked(ClientGameplayCloseReason.SessionInvalidated);
                }
            }
        }

        /// <summary>显式结束当前 connection 并允许后续新 generation。</summary>
        /// <param name="cancellationToken">有界等待 reader/writer 退出的信号。</param>
        /// <returns>I/O task 已观察时完成。</returns>
        internal async Task CloseAsync(CancellationToken cancellationToken)
        {
            Task reader;
            Task writer;
            Task heartbeat;
            lock (_sync)
            {
                BeginCloseLocked(ClientGameplayCloseReason.Caller);
                reader = _readerTask;
                writer = _writerTask;
                heartbeat = _heartbeatTask;
            }

            await AwaitOwnersAsync(reader, writer, heartbeat, cancellationToken);
        }

        /// <summary>停止 owner、拒绝新发送并有界观察全部 I/O task。</summary>
        /// <param name="cancellationToken">AppLifetime 共享停止 deadline。</param>
        /// <returns>资源与 pending 均释放时完成。</returns>
        public async Task StopAsync(CancellationToken cancellationToken)
        {
            Task reader;
            Task writer;
            Task heartbeat;
            CancellationTokenSource lifetime;
            lock (_sync)
            {
                if (_snapshot.State == ClientGameplayChannelState.Stopped)
                {
                    return;
                }

                BeginCloseLocked(ClientGameplayCloseReason.Shutdown);
                SetSnapshotLocked(ClientGameplayChannelState.Stopped, ClientGameplayCloseReason.Shutdown);
                reader = _readerTask;
                writer = _writerTask;
                heartbeat = _heartbeatTask;
                lifetime = _lifetimeCancellation;
                _lifetimeCancellation = null;
            }

            lifetime?.Cancel();
            await AwaitOwnersAsync(reader, writer, heartbeat, cancellationToken);
            lifetime?.Dispose();
            _writer.Dispose();
        }

        /// <summary>唯一 reader 按 sequence 解码并分发 response/error/push。</summary>
        private async Task ReaderPumpAsync(long generation, CancellationToken cancellationToken)
        {
            await _readerPump.RunAsync(
                generation,
                CurrentConnection,
                DispatchEnvelope,
                RecordDiagnostic,
                BeginClose,
                cancellationToken);
        }

        /// <summary>把已按 generation 解码的 envelope 交给唯一 typed route。</summary>
        private void DispatchEnvelope(long generation, ClientGameplayEnvelope envelope)
        {
            lock (_sync)
            {
                if (_generation != generation)
                {
                    return;
                }
            }

            if (envelope.Kind == MessageKind.Push)
            {
                HandlePush(generation, envelope);
            }
            else
            {
                HandleCompletion(generation, envelope);
            }
        }

        /// <summary>唯一 writer 顺序写入完整 frame，禁止字节交错。</summary>
        private async Task WriterPumpAsync(long generation, CancellationToken cancellationToken)
        {
            try
            {
                while (!cancellationToken.IsCancellationRequested)
                {
                    await _writer.WaitAsync(cancellationToken);
                    byte[] frame;
                    lock (_sync)
                    {
                        if (_generation != generation ||
                            !_writer.TryDequeue(out frame))
                        {
                            continue;
                        }
                    }

                    await CurrentConnection(generation).WriteAsync(frame, cancellationToken);
                }
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
            {
            }
            catch (Exception exception)
            {
                RecordDiagnostic(
                    generation,
                    ClientGameplayDiagnosticStage.Writer,
                    ClientGameplayCloseReason.Transport,
                    exception);
                BeginClose(generation, ClientGameplayCloseReason.Transport);
            }
        }

        /// <summary>为 current active generation 启动唯一 heartbeat owner；调用方持有 <see cref="_sync"/>。</summary>
        /// <param name="generation">Current connection generation。</param>
        /// <param name="cancellationToken">与 connection generation 同生命周期的取消信号。</param>
        private void StartHeartbeatLocked(long generation, CancellationToken cancellationToken)
        {
            if (_generation != generation ||
                _snapshot.State != ClientGameplayChannelState.Active ||
                !_heartbeatTask.IsCompleted)
            {
                return;
            }

            var heartbeat = _heartbeat.RunAsync(
                () => IsHeartbeatActive(generation),
                token => SendAsync(
                    ClientGameplayCatalog.GameplayHeartbeat,
                    new GameplayHeartbeatRequest(),
                    token),
                cancellationToken);
            _heartbeatTask = heartbeat;
            _ = ObserveHeartbeatAsync(generation, heartbeat);
        }

        /// <summary>判断 heartbeat 仍属于 current active、可写 generation。</summary>
        private bool IsHeartbeatActive(long generation)
        {
            lock (_sync)
            {
                return _generation == generation &&
                       _snapshot.State == ClientGameplayChannelState.Active &&
                       _acceptingMutations;
            }
        }

        /// <summary>在 heartbeat owner 已退出后提交 terminal close，避免 notification 等待自身。</summary>
        /// <param name="generation">被观察的 connection generation。</param>
        /// <param name="heartbeat">唯一 heartbeat owner task。</param>
        /// <returns>Owner 正常撤销或 terminal close 已提交时完成。</returns>
        private async Task ObserveHeartbeatAsync(
            long generation,
            Task<GameplayHeartbeatResult> heartbeat)
        {
            var result = await heartbeat;
            if (result.Reason != ClientGameplayCloseReason.None)
            {
                RecordDiagnostic(
                    generation,
                    ClientGameplayDiagnosticStage.Heartbeat,
                    result.Reason,
                    result.Exception);
                BeginClose(generation, result.Reason);
            }
        }

        /// <summary>匹配 pending correlation 并恰好完成一次。</summary>
        private void HandleCompletion(long generation, ClientGameplayEnvelope envelope)
        {
            GameplayPendingOperation pending;
            lock (_sync)
            {
                if (_generation != generation ||
                    !_pending.TryTake(
                        Convert.ToBase64String(envelope.CorrelationID),
                        envelope.MessageID,
                        envelope.CorrelationKind,
                        out pending))
                {
                    throw new ClientGameplayProtocolException("Gameplay response correlation 未登记或 route 不匹配。");
                }
            }

            try
            {
                var completion = _routeDispatcher.DecodeCompletion(envelope, pending);
                if (pending.ActivatesConnection && envelope.Kind == MessageKind.Response)
                {
                    lock (_sync)
                    {
                        if (_generation == generation &&
                            _snapshot.State == ClientGameplayChannelState.Pending)
                        {
                            _pendingAdmission = null;
                            SetSnapshotLocked(ClientGameplayChannelState.Active, ClientGameplayCloseReason.None);
                            StartHeartbeatLocked(generation, _connectionCancellation.Token);
                        }
                    }
                }

                pending.Completion.TrySetResult(completion);
            }
            catch (ClientGameplayProtocolException)
            {
                pending.Completion.TrySetResult(GameplayPendingCompletion.Failed(ClientGameplayFailureKind.Protocol));
                BeginClose(generation, ClientGameplayCloseReason.Protocol);
            }
        }

        /// <summary>使用冻结 parser 解码并有界投递三个可信 PUSH。</summary>
        private void HandlePush(long generation, ClientGameplayEnvelope envelope)
        {
            var route = _routeDispatcher.DecodePush(envelope);
            Action callback;
            switch (route.Kind)
            {
                case GameplayPushRouteKind.WorldSnapshot:
                    var world = (WorldSnapshotPush)route.Value;
                    callback = () => WorldSnapshotReceived?.Invoke(world);
                    break;
                case GameplayPushRouteKind.VisitSnapshot:
                    var visit = (VisitSnapshotPush)route.Value;
                    callback = () => VisitSnapshotReceived?.Invoke(visit);
                    break;
                case GameplayPushRouteKind.SafeReturn:
                    var safeReturn = (VisitSafeReturnPush)route.Value;
                    lock (_sync)
                    {
                        if (_generation != generation)
                        {
                            return;
                        }

                        _acceptingMutations = false;
                    }

                    callback = () =>
                    {
                        try
                        {
                            SafeReturnReceived?.Invoke(safeReturn);
                        }
                        finally
                        {
                            // Subscriber 失败不能阻止旧 target 的权威关闭。
                            BeginClose(generation, ClientGameplayCloseReason.ApplicationReturn);
                        }
                    };
                    break;
                default:
                    throw new ClientGameplayProtocolException(
                        "Gameplay PUSH route 未登记。");
            }

            if (!_routeDispatcher.TryPost(callback))
            {
                BeginClose(generation, ClientGameplayCloseReason.Backpressure);
            }
        }

        /// <summary>检查当前 state、safe-return gate 与 JOIN/RECONNECT 首帧约束。</summary>
        private bool CanSendLocked<TRequest>(uint requestMessageID, TRequest payload)
            where TRequest : class
        {
            if (_snapshot.State != ClientGameplayChannelState.Active &&
                _snapshot.State != ClientGameplayChannelState.Pending)
            {
                return false;
            }

            var isMutation = requestMessageID != ClientGameplayCatalog.GameplayHeartbeat.RequestMessageID &&
                             requestMessageID != ClientGameplayCatalog.WorldSnapshot.RequestMessageID &&
                             requestMessageID != ClientGameplayCatalog.VisitSnapshot.RequestMessageID;
            if (isMutation && !_acceptingMutations)
            {
                return false;
            }

            if (_snapshot.State == ClientGameplayChannelState.Active)
            {
                return true;
            }

            return _purpose switch
            {
                ClientWorldAdmissionPurpose.Join =>
                    requestMessageID == ClientGameplayCatalog.VisitJoin.RequestMessageID &&
                    payload is VisitJoinCommand join &&
                    string.Equals(join.AdmissionCredential, _pendingAdmission, StringComparison.Ordinal),
                ClientWorldAdmissionPurpose.Reconnect =>
                    requestMessageID == ClientGameplayCatalog.VisitReconnect.RequestMessageID &&
                    payload is VisitReconnectCommand reconnect &&
                    string.Equals(reconnect.AdmissionCredential, _pendingAdmission, StringComparison.Ordinal),
                _ => false,
            };
        }

        /// <summary>从 current generation 取得唯一 connection。</summary>
        private IClientGameplayConnection CurrentConnection(long generation)
        {
            lock (_sync)
            {
                if (_generation != generation || _connection == null)
                {
                    throw new OperationCanceledException("Gameplay generation 已替换。");
                }

                return _connection;
            }
        }

        /// <summary>在 connect 失败且 generation 仍 current 时恢复 Ready。</summary>
        private void ResetConnecting(long generation, ClientGameplayCloseReason reason)
        {
            lock (_sync)
            {
                if (_generation == generation && _snapshot.State == ClientGameplayChannelState.Connecting)
                {
                    SetSnapshotLocked(ClientGameplayChannelState.Ready, reason);
                }
            }
        }

        /// <summary>从 pump 安全进入统一关闭临界区。</summary>
        private void BeginClose(long generation, ClientGameplayCloseReason reason)
        {
            Task reader = null;
            Task writer = null;
            Task heartbeat = null;
            var notifyUnexpectedDisconnect = false;
            var closeAccepted = false;
            lock (_sync)
            {
                if (_generation == generation)
                {
                    var wasConnected = _snapshot.State == ClientGameplayChannelState.Pending ||
                                       _snapshot.State == ClientGameplayChannelState.Active;
                    closeAccepted = _snapshot.State != ClientGameplayChannelState.Created &&
                                    _snapshot.State != ClientGameplayChannelState.Ready &&
                                    _snapshot.State != ClientGameplayChannelState.Stopped;
                    BeginCloseLocked(reason);
                    if (wasConnected && IsUnexpectedDisconnect(reason))
                    {
                        reader = _readerTask;
                        writer = _writerTask;
                        heartbeat = _heartbeatTask;
                        notifyUnexpectedDisconnect = true;
                    }
                }
            }

            if (closeAccepted)
            {
                RecordDiagnostic(
                    generation,
                    ClientGameplayDiagnosticStage.Close,
                    reason,
                    null);
            }

            if (notifyUnexpectedDisconnect)
            {
                _ = NotifyUnexpectedDisconnectAfterOwnersAsync(generation, reason, reader, writer, heartbeat);
            }
        }

        /// <summary>以普通有界主线程队列投递低敏诊断；拥塞时丢弃诊断，不占用 terminal 保留槽。</summary>
        /// <param name="generation">事件所属 connection generation。</param>
        /// <param name="stage">固定生命周期阶段。</param>
        /// <param name="reason">稳定关闭分类。</param>
        /// <param name="exception">可选异常，仅提取 CLR type，不读取 message 或 stack。</param>
        private void RecordDiagnostic(
            long generation,
            ClientGameplayDiagnosticStage stage,
            ClientGameplayCloseReason reason,
            Exception exception)
        {
            var diagnostic = new ClientGameplayDiagnostic(
                generation,
                stage,
                reason,
                exception?.GetType().FullName);
            _dispatcher.TryPost(() =>
            {
                try
                {
                    DiagnosticRecorded?.Invoke(diagnostic);
                }
                catch
                {
                    // 诊断 subscriber 不能成为 connection 生命周期依赖。
                }
            });
        }

        /// <summary>等待已撤销 generation 的 reader、writer 与 heartbeat owner 退出，再发布可立即显式重连的 terminal 状态。</summary>
        /// <param name="generation">发生断开的 connection generation。</param>
        /// <param name="reason">稳定关闭原因。</param>
        /// <param name="reader">该 generation 的 reader owner。</param>
        /// <param name="writer">该 generation 的 writer owner。</param>
        /// <param name="heartbeat">该 generation 的 heartbeat owner。</param>
        /// <returns>三个 owner 都结束且主线程 terminal callback 已取得所有权时完成。</returns>
        private async Task NotifyUnexpectedDisconnectAfterOwnersAsync(
            long generation,
            ClientGameplayCloseReason reason,
            Task reader,
            Task writer,
            Task heartbeat)
        {
            await Task.WhenAll(
                reader ?? Task.CompletedTask,
                writer ?? Task.CompletedTask,
                heartbeat ?? Task.CompletedTask);
            ClientGameplayChannelSnapshot disconnected;
            lock (_sync)
            {
                if (_generation != generation ||
                    _snapshot.State != ClientGameplayChannelState.Ready ||
                    _snapshot.CloseReason != reason)
                {
                    return;
                }

                disconnected = _snapshot;
            }

            _dispatcher.TryPostCritical(() => UnexpectedDisconnect?.Invoke(disconnected));
        }

        /// <summary>判断关闭原因是否表示服务器或链路的非预期 terminal failure。</summary>
        /// <param name="reason">Channel owner 提交的稳定关闭原因。</param>
        /// <returns>需要使产品 target 立即失效时返回 true。</returns>
        private static bool IsUnexpectedDisconnect(ClientGameplayCloseReason reason)
        {
            return reason == ClientGameplayCloseReason.Remote ||
                   reason == ClientGameplayCloseReason.Protocol ||
                   reason == ClientGameplayCloseReason.Timeout ||
                   reason == ClientGameplayCloseReason.HeartbeatTimeout ||
                   reason == ClientGameplayCloseReason.Backpressure ||
                   reason == ClientGameplayCloseReason.Transport;
        }

        /// <summary>线性化撤销 connection、queue 与 pending；调用方持有 `_sync`。</summary>
        private void BeginCloseLocked(ClientGameplayCloseReason reason)
        {
            if (_snapshot.State == ClientGameplayChannelState.Created ||
                _snapshot.State == ClientGameplayChannelState.Ready ||
                _snapshot.State == ClientGameplayChannelState.Stopped)
            {
                return;
            }

            SetSnapshotLocked(ClientGameplayChannelState.Closing, reason);
            _acceptingMutations = false;
            _pendingAdmission = null;
            _connectionCancellation?.Cancel();
            _connectionCancellation?.Dispose();
            _connectionCancellation = null;
            _connection?.Dispose();
            _connection = null;
            _writer.Clear();
            _pending.FailAll(ClientGameplayFailureKind.Disconnected);
            SetSnapshotLocked(ClientGameplayChannelState.Ready, reason);
        }

        /// <summary>有界等待 reader/writer，停止 deadline 到期时不吞掉取消。</summary>
        private static async Task AwaitOwnersAsync(
            Task reader,
            Task writer,
            Task heartbeat,
            CancellationToken cancellationToken)
        {
            var all = Task.WhenAll(
                reader ?? Task.CompletedTask,
                writer ?? Task.CompletedTask,
                heartbeat ?? Task.CompletedTask);
            if (all.IsCompleted)
            {
                await all;
                return;
            }

            var cancelled = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
            using (cancellationToken.Register(() => cancelled.TrySetResult(true)))
            {
                var completed = await Task.WhenAny(all, cancelled.Task);
                if (!ReferenceEquals(completed, all))
                {
                    cancellationToken.ThrowIfCancellationRequested();
                }
            }

            await all;
        }

        /// <summary>生成 16-byte CSPRNG correlation。</summary>
        private static byte[] NewCorrelationID()
        {
            var correlation = new byte[16];
            using (var generator = RandomNumberGenerator.Create())
            {
                generator.GetBytes(correlation);
            }

            return correlation;
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

        /// <summary>在channel同步边界内提交当前generation的不可变健康快照。</summary>
        /// <param name="state">待发布的gameplay channel状态。</param>
        /// <param name="reason">该状态对应的稳定关闭原因。</param>
        private void SetSnapshotLocked(
            ClientGameplayChannelState state,
            ClientGameplayCloseReason reason)
        {
            _snapshot = new ClientGameplayChannelSnapshot(state, reason, _generation);
        }

    }
}
