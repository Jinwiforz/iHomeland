using System;
using System.Collections.Generic;
using System.Security.Cryptography;
using System.Threading;
using System.Threading.Tasks;
using Google.Protobuf;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Core.Configuration;
using IHomeland.Client.Core.Lifetime;
using IHomeland.Client.Infrastructure.Http;
using IHomeland.Client.Infrastructure.Tcp;
using IHomeland.Protocol.Common.V1;
using IHomeland.Protocol.Visit.V1;
using IHomeland.Protocol.World.V1;

namespace IHomeland.Client.Application.Gameplay
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
        /// <summary>限制单次 transport connect 与 TLS handshake。</summary>
        private static readonly TimeSpan ConnectTimeout = TimeSpan.FromSeconds(10);

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

        /// <summary>唯一拥有 session lineage、ticket 与 admission lease 的 owner。</summary>
        private readonly SessionCoordinator _sessionCoordinator;

        /// <summary>为每个 generation 创建独立 TCP/TLS connection。</summary>
        private readonly IClientGameplayConnectionFactory _connectionFactory;

        /// <summary>编码/解码冻结 envelope contract。</summary>
        private readonly ClientGameplayCodec _codec;

        /// <summary>将 PUSH callback 有界转移到 Unity 主线程。</summary>
        private readonly MainThreadDispatcher _dispatcher;

        /// <summary>保存等待 serialized writer 的 frame。</summary>
        private readonly Queue<byte[]> _writerQueue = new Queue<byte[]>();

        /// <summary>通知唯一 writer 有新 frame 或关闭。</summary>
        private readonly SemaphoreSlim _writerSignal = new SemaphoreSlim(0);

        /// <summary>按不可预测 correlation 索引有界 pending。</summary>
        private readonly Dictionary<string, PendingOperation> _pending =
            new Dictionary<string, PendingOperation>(StringComparer.Ordinal);

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

        /// <summary>Current generation C2S 下一个 sequence。</summary>
        private ulong _nextClientSequence;

        /// <summary>Current generation S2C 下一个 expected sequence。</summary>
        private ulong _nextServerSequence;

        /// <summary>Writer queue 当前编码字节总数。</summary>
        private int _writerBytes;

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
        internal ClientGameplayChannel(
            ClientConfigurationStore configurationStore,
            SessionCoordinator sessionCoordinator,
            IClientGameplayConnectionFactory connectionFactory,
            ClientGameplayCodec codec,
            MainThreadDispatcher dispatcher)
        {
            _configurationStore = configurationStore ?? throw new ArgumentNullException(nameof(configurationStore));
            _sessionCoordinator = sessionCoordinator ?? throw new ArgumentNullException(nameof(sessionCoordinator));
            _connectionFactory = connectionFactory ?? throw new ArgumentNullException(nameof(connectionFactory));
            _codec = codec ?? throw new ArgumentNullException(nameof(codec));
            _dispatcher = dispatcher ?? throw new ArgumentNullException(nameof(dispatcher));
        }

        /// <summary>在主线程投递 PersonalWorld 完整替换 PUSH。</summary>
        internal event Action<WorldSnapshotPush> WorldSnapshotReceived;

        /// <summary>在主线程投递 VisitSession 完整替换 PUSH。</summary>
        internal event Action<VisitSnapshotPush> VisitSnapshotReceived;

        /// <summary>在主线程投递权威 safe-return PUSH。</summary>
        internal event Action<VisitSafeReturnPush> SafeReturnReceived;

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
                    !_configurationStore.TryGetCurrent(out _))
                {
                    return false;
                }

                generation = ++_generation;
                SetSnapshotLocked(ClientGameplayChannelState.Connecting, ClientGameplayCloseReason.None);
            }

            if (!_sessionCoordinator.TryTakeWorldAdmission(admissionLease, out var admissionUse) ||
                !IsAdmissionBindingValid(admissionUse))
            {
                ResetConnecting(generation, ClientGameplayCloseReason.Protocol);
                return false;
            }

            ClientHttpResult<ClientConnectionTicketLease> ticketResult;
            try
            {
                ticketResult = await _sessionCoordinator.IssueConnectionTicketAsync(
                    ClientEndpointChannel.TlsTcp,
                    cancellationToken);
            }
            catch (OperationCanceledException)
            {
                ResetConnecting(generation, ClientGameplayCloseReason.Caller);
                return false;
            }
            catch
            {
                ResetConnecting(generation, ClientGameplayCloseReason.Transport);
                return false;
            }

            if (!ticketResult.IsSuccess ||
                !_sessionCoordinator.TryTakeConnectionTicket(ticketResult.Value, out var ticketUse) ||
                !EndpointsEqual(ticketUse.Endpoint, admissionUse.Endpoint) ||
                ticketUse.SourceGeneration != admissionUse.SourceGeneration)
            {
                ResetConnecting(generation, ClientGameplayCloseReason.Protocol);
                return false;
            }

            byte[] preface;
            try
            {
                preface = ClientGameplayPreface.Encode(
                    ticketUse.Credential,
                    admissionUse.Credential,
                    admissionUse.Purpose);
            }
            catch (ClientGameplayProtocolException)
            {
                ResetConnecting(generation, ClientGameplayCloseReason.Protocol);
                return false;
            }

            IClientGameplayConnection connection = null;
            CancellationTokenSource connectionCancellation = null;
            try
            {
                using (var timeout = new CancellationTokenSource(ConnectTimeout))
                using (var linked = CancellationTokenSource.CreateLinkedTokenSource(
                           cancellationToken,
                           timeout.Token,
                           _lifetimeCancellation.Token))
                {
                    connection = await _connectionFactory.ConnectAsync(
                        admissionUse.Endpoint,
                        linked.Token);
                    await connection.WriteAsync(preface, linked.Token);
                }

                connectionCancellation = CancellationTokenSource.CreateLinkedTokenSource(
                    _lifetimeCancellation.Token);
                lock (_sync)
                {
                    if (_generation != generation || _snapshot.State != ClientGameplayChannelState.Connecting)
                    {
                        return false;
                    }

                    _connection = connection;
                    connection = null;
                    _connectionCancellation = connectionCancellation;
                    connectionCancellation = null;
                    _sourceSessionGeneration = admissionUse.SourceGeneration;
                    _purpose = admissionUse.Purpose;
                    _pendingAdmission = admissionUse.Purpose == ClientWorldAdmissionPurpose.OwnWorld
                        ? null
                        : admissionUse.Credential;
                    _acceptingMutations = true;
                    _nextClientSequence = 1;
                    _nextServerSequence = 1;
                    var state = _purpose == ClientWorldAdmissionPurpose.OwnWorld
                        ? ClientGameplayChannelState.Active
                        : ClientGameplayChannelState.Pending;
                    SetSnapshotLocked(state, ClientGameplayCloseReason.None);
                    _readerTask = ReaderPumpAsync(generation, _connectionCancellation.Token);
                    _writerTask = WriterPumpAsync(generation, _connectionCancellation.Token);
                }

                return true;
            }
            catch (OperationCanceledException)
            {
                ResetConnecting(
                    generation,
                    cancellationToken.IsCancellationRequested
                        ? ClientGameplayCloseReason.Caller
                        : ClientGameplayCloseReason.Timeout);
                return false;
            }
            catch
            {
                ResetConnecting(generation, ClientGameplayCloseReason.Transport);
                return false;
            }
            finally
            {
                connection?.Dispose();
                connectionCancellation?.Dispose();
            }
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

            PendingOperation pending;
            lock (_sync)
            {
                if (!CanSendLocked(operation.RequestMessageID, payload))
                {
                    return ClientGameplayResult<TResponse>.Failed(ClientGameplayFailureKind.Policy);
                }

                if (_pending.Count >= PendingCapacity)
                {
                    return ClientGameplayResult<TResponse>.Failed(ClientGameplayFailureKind.Backpressure);
                }

                var correlation = NewCorrelationID();
                var encoded = _codec.Encode(
                    operation,
                    payload,
                    correlation,
                    _nextClientSequence,
                    DateTimeOffset.UtcNow.ToUnixTimeMilliseconds());
                var frame = ClientGameplayFramer.Frame(encoded);
                if (_writerQueue.Count >= WriterItemCapacity ||
                    _writerBytes > WriterByteCapacity - frame.Length)
                {
                    BeginCloseLocked(ClientGameplayCloseReason.Backpressure);
                    return ClientGameplayResult<TResponse>.Failed(ClientGameplayFailureKind.Backpressure);
                }

                _nextClientSequence++;
                var key = Convert.ToBase64String(correlation);
                var activates = _snapshot.State == ClientGameplayChannelState.Pending;
                pending = new PendingOperation(
                    operation.ResponseMessageID,
                    operation.RequestKind,
                    envelope => _codec.DecodeResponse(operation, envelope),
                    activates);
                _pending.Add(key, pending);
                if (activates)
                {
                    // 首个 JOIN/RECONNECT command 一旦取得发送所有权，后续 command 必须等待结果。
                    _pendingAdmission = null;
                }
                _writerQueue.Enqueue(frame);
                _writerBytes += frame.Length;
                _writerSignal.Release();
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
                        if (pending.Completion.TrySetResult(PendingCompletion.Failed(failure)))
                        {
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
            lock (_sync)
            {
                BeginCloseLocked(ClientGameplayCloseReason.Caller);
                reader = _readerTask;
                writer = _writerTask;
            }

            await AwaitPumpsAsync(reader, writer, cancellationToken);
        }

        /// <summary>停止 owner、拒绝新发送并有界观察全部 I/O task。</summary>
        /// <param name="cancellationToken">AppLifetime 共享停止 deadline。</param>
        /// <returns>资源与 pending 均释放时完成。</returns>
        public async Task StopAsync(CancellationToken cancellationToken)
        {
            Task reader;
            Task writer;
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
                lifetime = _lifetimeCancellation;
                _lifetimeCancellation = null;
            }

            lifetime?.Cancel();
            await AwaitPumpsAsync(reader, writer, cancellationToken);
            lifetime?.Dispose();
            _writerSignal.Dispose();
        }

        /// <summary>唯一 reader 按 sequence 解码并分发 response/error/push。</summary>
        private async Task ReaderPumpAsync(long generation, CancellationToken cancellationToken)
        {
            try
            {
                while (!cancellationToken.IsCancellationRequested)
                {
                    int maximumFrameBytes;
                    if (!_configurationStore.TryGetCurrent(out var configuration))
                    {
                        throw new ClientGameplayProtocolException("Gameplay config 不可用。");
                    }

                    maximumFrameBytes = Math.Min(
                        configuration.Configuration.Limits.RealtimeFrameBytes,
                        ClientGameplayFramer.MaximumFrameBytes);
                    var body = await ClientGameplayFramer.ReadFrameAsync(
                        CurrentConnection(generation),
                        maximumFrameBytes,
                        cancellationToken);
                    ClientGameplayEnvelope envelope;
                    lock (_sync)
                    {
                        if (_generation != generation)
                        {
                            return;
                        }

                        envelope = _codec.Decode(body, _nextServerSequence);
                        _nextServerSequence++;
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
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
            {
            }
            catch (ClientGameplayProtocolException)
            {
                BeginClose(generation, ClientGameplayCloseReason.Protocol);
            }
            catch (System.IO.EndOfStreamException)
            {
                BeginClose(generation, ClientGameplayCloseReason.Remote);
            }
            catch
            {
                BeginClose(generation, ClientGameplayCloseReason.Transport);
            }
        }

        /// <summary>唯一 writer 顺序写入完整 frame，禁止字节交错。</summary>
        private async Task WriterPumpAsync(long generation, CancellationToken cancellationToken)
        {
            try
            {
                while (!cancellationToken.IsCancellationRequested)
                {
                    await _writerSignal.WaitAsync(cancellationToken);
                    byte[] frame;
                    lock (_sync)
                    {
                        if (_generation != generation || _writerQueue.Count == 0)
                        {
                            continue;
                        }

                        frame = _writerQueue.Dequeue();
                        _writerBytes -= frame.Length;
                    }

                    await CurrentConnection(generation).WriteAsync(frame, cancellationToken);
                }
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
            {
            }
            catch
            {
                BeginClose(generation, ClientGameplayCloseReason.Transport);
            }
        }

        /// <summary>匹配 pending correlation 并恰好完成一次。</summary>
        private void HandleCompletion(long generation, ClientGameplayEnvelope envelope)
        {
            PendingOperation pending;
            lock (_sync)
            {
                if (_generation != generation ||
                    !_pending.TryGetValue(Convert.ToBase64String(envelope.CorrelationID), out pending) ||
                    pending.ResponseMessageID != envelope.MessageID ||
                    pending.CorrelationKind != envelope.CorrelationKind)
                {
                    throw new ClientGameplayProtocolException("Gameplay response correlation 未登记或 route 不匹配。");
                }

                _pending.Remove(Convert.ToBase64String(envelope.CorrelationID));
            }

            try
            {
                var completion = envelope.Kind == MessageKind.Error
                    ? PendingCompletion.Rejected(_codec.DecodeError(envelope))
                    : PendingCompletion.Succeeded(pending.ParseResponse(envelope));
                if (pending.ActivatesConnection && envelope.Kind == MessageKind.Response)
                {
                    lock (_sync)
                    {
                        if (_generation == generation &&
                            _snapshot.State == ClientGameplayChannelState.Pending)
                        {
                            _pendingAdmission = null;
                            SetSnapshotLocked(ClientGameplayChannelState.Active, ClientGameplayCloseReason.None);
                        }
                    }
                }

                pending.Completion.TrySetResult(completion);
            }
            catch (ClientGameplayProtocolException)
            {
                pending.Completion.TrySetResult(PendingCompletion.Failed(ClientGameplayFailureKind.Protocol));
                BeginClose(generation, ClientGameplayCloseReason.Protocol);
            }
        }

        /// <summary>使用冻结 parser 解码并有界投递三个可信 PUSH。</summary>
        private void HandlePush(long generation, ClientGameplayEnvelope envelope)
        {
            Action callback;
            try
            {
                if (envelope.MessageID == ClientGameplayCatalog.WorldSnapshotPushRoute.MessageID)
                {
                    var world = ClientGameplayCatalog.WorldSnapshotPushRoute.Parser.ParseFrom(envelope.Payload);
                    callback = () => WorldSnapshotReceived?.Invoke(world);
                }
                else if (envelope.MessageID == ClientGameplayCatalog.VisitSnapshotPushRoute.MessageID)
                {
                    var visit = ClientGameplayCatalog.VisitSnapshotPushRoute.Parser.ParseFrom(envelope.Payload);
                    callback = () => VisitSnapshotReceived?.Invoke(visit);
                }
                else if (envelope.MessageID == ClientGameplayCatalog.VisitSafeReturnPushRoute.MessageID)
                {
                    var safeReturn = ClientGameplayCatalog.VisitSafeReturnPushRoute.Parser.ParseFrom(envelope.Payload);
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
                }
                else
                {
                    throw new ClientGameplayProtocolException("Gameplay PUSH route 未登记。");
                }
            }
            catch (InvalidProtocolBufferException error)
            {
                throw new ClientGameplayProtocolException("Gameplay PUSH payload 无效。", error);
            }

            if (_dispatcher.TryPost(callback) != DispatchPostResult.Accepted)
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

            var isMutation = requestMessageID != ClientGameplayCatalog.WorldSnapshot.RequestMessageID &&
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
            lock (_sync)
            {
                if (_generation == generation)
                {
                    BeginCloseLocked(reason);
                }
            }
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
            _writerQueue.Clear();
            _writerBytes = 0;
            foreach (var pending in _pending.Values)
            {
                pending.Completion.TrySetResult(
                    PendingCompletion.Failed(ClientGameplayFailureKind.Disconnected));
            }

            _pending.Clear();
            SetSnapshotLocked(ClientGameplayChannelState.Ready, reason);
        }

        /// <summary>有界等待 reader/writer，停止 deadline 到期时不吞掉取消。</summary>
        private static async Task AwaitPumpsAsync(
            Task reader,
            Task writer,
            CancellationToken cancellationToken)
        {
            var all = Task.WhenAll(reader ?? Task.CompletedTask, writer ?? Task.CompletedTask);
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

        /// <summary>比较 ticket/admission 的 channel、host 与 port binding。</summary>
        private static bool EndpointsEqual(ClientEndpoint left, ClientEndpoint right)
        {
            return left != null && right != null &&
                   left.Channel == right.Channel &&
                   left.Port == right.Port &&
                   string.Equals(left.Host, right.Host, StringComparison.OrdinalIgnoreCase);
        }

        /// <summary>验证 admission 的 channel、role 与 purpose 封闭配对。</summary>
        /// <param name="admission">已从 current Session owner 单次取得的 admission。</param>
        /// <returns>Owner/OWN_WORLD 或 Visitor/JOIN|RECONNECT 且使用 TLS_TCP 时返回 true。</returns>
        private static bool IsAdmissionBindingValid(ClientWorldAdmissionUse admission)
        {
            if (admission == null || admission.Endpoint.Channel != ClientEndpointChannel.TlsTcp)
            {
                return false;
            }

            return admission.Purpose == ClientWorldAdmissionPurpose.OwnWorld
                ? admission.Role == ClientWorldRole.Owner
                : admission.Role == ClientWorldRole.Visitor &&
                  (admission.Purpose == ClientWorldAdmissionPurpose.Join ||
                   admission.Purpose == ClientWorldAdmissionPurpose.Reconnect);
        }

        /// <summary>替换不可变低敏 snapshot；调用方持有 `_sync`。</summary>
        private void SetSnapshotLocked(
            ClientGameplayChannelState state,
            ClientGameplayCloseReason reason)
        {
            _snapshot = new ClientGameplayChannelSnapshot(state, reason, _generation);
        }

        /// <summary>保存 pending descriptor、parser 与 exactly-once completion。</summary>
        private sealed class PendingOperation
        {
            /// <summary>创建 pending entry。</summary>
            internal PendingOperation(
                uint responseMessageID,
                MessageKind correlationKind,
                Func<ClientGameplayEnvelope, object> parseResponse,
                bool activatesConnection)
            {
                ResponseMessageID = responseMessageID;
                CorrelationKind = correlationKind;
                ParseResponse = parseResponse ?? throw new ArgumentNullException(nameof(parseResponse));
                ActivatesConnection = activatesConnection;
                Completion = new TaskCompletionSource<PendingCompletion>(TaskCreationOptions.RunContinuationsAsynchronously);
            }

            /// <summary>获取唯一 expected response ID。</summary>
            internal uint ResponseMessageID { get; }

            /// <summary>获取 response/error 必须沿用的 Request 或 Command correlation 字段。</summary>
            internal MessageKind CorrelationKind { get; }

            /// <summary>获取固定 generated response parser。</summary>
            internal Func<ClientGameplayEnvelope, object> ParseResponse { get; }

            /// <summary>报告成功 response 是否把 pending target 提升为 Active。</summary>
            internal bool ActivatesConnection { get; }

            /// <summary>获取 exactly-once completion owner。</summary>
            internal TaskCompletionSource<PendingCompletion> Completion { get; }
        }

        /// <summary>保存 pending 成功、服务端拒绝或本地失败。</summary>
        private sealed class PendingCompletion
        {
            /// <summary>创建内部三选一结果。</summary>
            private PendingCompletion(object value, ErrorPayload error, ClientGameplayFailureKind failure)
            {
                Value = value;
                Error = error;
                Failure = failure;
            }

            /// <summary>获取成功 response。</summary>
            internal object Value { get; }

            /// <summary>获取服务端公开错误。</summary>
            internal ErrorPayload Error { get; }

            /// <summary>获取本地失败。</summary>
            internal ClientGameplayFailureKind Failure { get; }

            /// <summary>创建成功结果。</summary>
            internal static PendingCompletion Succeeded(object value)
            {
                return new PendingCompletion(value ?? throw new ArgumentNullException(nameof(value)), null, default);
            }

            /// <summary>创建服务端拒绝结果。</summary>
            internal static PendingCompletion Rejected(ErrorPayload error)
            {
                return new PendingCompletion(null, error ?? throw new ArgumentNullException(nameof(error)), default);
            }

            /// <summary>创建本地失败结果。</summary>
            internal static PendingCompletion Failed(ClientGameplayFailureKind failure)
            {
                return new PendingCompletion(null, null, failure);
            }
        }
    }
}
