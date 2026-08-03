using System;
using System.Collections.Generic;
using System.Net.Sockets;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Battle;
using UnityEngine;

namespace IHomeland.Client.Infrastructure.Battle
{
    /// <summary>
    /// 唯一拥有 BattleTicket attempt、UDP socket、secure channel、KCP 与 send/receive pumps。
    /// </summary>
    internal sealed class BattleNetworkClient :
        IClientBattleConnectionPort,
        IClientBattleGameplayPort
    {
        /// <summary>保存认证成功后首个完整 baseline 的最长等待时间。</summary>
        internal const long InitialBaselineDeadlineMilliseconds = 5000;

        /// <summary>串行化 activation/deactivation/AppLifetime transition。</summary>
        private readonly SemaphoreSlim _lifecycle =
            new SemaphoreSlim(1, 1);

        /// <summary>保护 current generation state 与 managed queues。</summary>
        private readonly object _gate = new object();

        /// <summary>串行化 native KCP primitive 调用。</summary>
        private readonly object _kcpGate = new object();

        /// <summary>保存独立 connect attempt owner。</summary>
        private readonly IClientBattleConnectAttempt _connectAttempt;

        /// <summary>保存 generated protocol 唯一 adapter。</summary>
        private readonly ClientBattleProtocolAdapter _protocol;

        /// <summary>保存有界主线程 inbound adapter。</summary>
        private readonly ClientBattleInboundRouter _inbound;

        /// <summary>唤醒 serialized send pump。</summary>
        private readonly SemaphoreSlim _sendSignal =
            new SemaphoreSlim(0);

        /// <summary>保存 caller-owned protected datagram queue。</summary>
        private readonly Queue<byte[]> _sendQueue =
            new Queue<byte[]>();

        /// <summary>保存 AppLifetime 已初始化标志。</summary>
        private bool _initialized;

        /// <summary>保存 AppLifetime 不可逆停止标志。</summary>
        private bool _stopped;

        /// <summary>保存 current connection lifecycle。</summary>
        private ClientBattleConnectionState _state =
            ClientBattleConnectionState.Created;

        /// <summary>保存最近稳定 failure。</summary>
        private ClientBattleFailure _failure;

        /// <summary>保存每次 activation 单调递增的 generation。</summary>
        private long _generation;

        /// <summary>保存 current target kind。</summary>
        private ClientBattleTargetKind? _targetKind;

        /// <summary>保存 authenticated role。</summary>
        private ClientBattleRole _role;

        /// <summary>保存 authenticated actor slot；未建立时为 -1。</summary>
        private int _actorSlot = -1;

        /// <summary>保存最新已提交 server Tick。</summary>
        private ulong _latestServerTick;

        /// <summary>保存首个完整 baseline 的绝对 monotonic deadline。</summary>
        private long _baselineDeadlineMilliseconds;

        /// <summary>保存下一个 raw application sequence。</summary>
        private ulong _nextRawApplicationSequence = 1;

        /// <summary>保存最后接受的 KCP application sequence。</summary>
        private ulong _lastKcpReceivedSequence;

        /// <summary>保存 current connected UDP socket。</summary>
        private IClientBattleDatagramSocket _socket;

        /// <summary>保存 current secure traffic owner。</summary>
        private ClientBattleSecureChannel _secure;

        /// <summary>保存 current native KCP lease。</summary>
        private ClientBattleKcpLease _kcp;

        /// <summary>保存 generation-scoped pump cancellation。</summary>
        private CancellationTokenSource _sessionCancellation;

        /// <summary>保存 serialized send pump。</summary>
        private Task _sendPump;

        /// <summary>保存唯一 receive pump。</summary>
        private Task _receivePump;

        /// <summary>保存 fixed 10ms KCP/rekey pump。</summary>
        private Task _kcpPump;

        /// <summary>保存 current runtime monotonic-derived clock。</summary>
        private ClientBattleAttemptClock _clock;

        /// <summary>保存 current KCP update 的 monotonic 起点。</summary>
        private long _kcpStartedMilliseconds;

        /// <summary>保存 resync KCP inflight application deadline。</summary>
        private long _kcpInflightDeadlineMilliseconds;

        /// <summary>保存等待 Server rekey commit 的 nonce。</summary>
        private byte[] _pendingRekeyNonce;

        /// <summary>保存等待提交的 exact next traffic epoch。</summary>
        private uint _pendingRekeyEpoch;

        /// <summary>保存 rekey response deadline。</summary>
        private long _pendingRekeyDeadlineMilliseconds;

        /// <summary>保存 current authenticated rebind request nonce。</summary>
        private byte[] _pendingRebindNonce;

        /// <summary>保存 rebind 开始时的 endpoint generation。</summary>
        private uint _pendingRebindCurrentGeneration;

        /// <summary>保存只允许提交的 next endpoint generation。</summary>
        private uint _pendingRebindNextGeneration;

        /// <summary>保存 rebind challenge/commit 总 deadline。</summary>
        private long _pendingRebindDeadlineMilliseconds;

        /// <summary>标记 exact challenge 已从 candidate path 回传。</summary>
        private bool _pendingRebindConfirmationSent;

        /// <summary>保存 graceful close acknowledgement。</summary>
        private TaskCompletionSource<bool> _closeAcknowledgement;

        /// <summary>保存 current generation 是否已发布 terminal。</summary>
        private bool _terminalPublished;

#if DEVELOPMENT_BUILD || UNITY_EDITOR
        /// <summary>获取资格测试可观察的current UDP socket owner数量。</summary>
        internal int QualificationSocketOwnerCount
        {
            get
            {
                lock (_gate)
                {
                    return _socket == null ? 0 : 1;
                }
            }
        }

        /// <summary>获取资格测试可观察的current background pump owner数量。</summary>
        internal int QualificationPumpOwnerCount
        {
            get
            {
                lock (_gate)
                {
                    var count = 0;
                    count += _sendPump == null ? 0 : 1;
                    count += _receivePump == null ? 0 : 1;
                    count += _kcpPump == null ? 0 : 1;
                    return count;
                }
            }
        }

        /// <summary>为Development资格场景终结current UDP generation并走production恢复入口。</summary>
        /// <returns>Current generation处于可终结状态并已提交故障时为true。</returns>
        internal bool InjectQualificationTransportDisconnect()
        {
            long generation;
            lock (_gate)
            {
                if (_stopped ||
                    _socket == null ||
                    (_state != ClientBattleConnectionState.Active &&
                     _state != ClientBattleConnectionState.AwaitingBaseline))
                {
                    return false;
                }

                generation = _generation;
            }

            FailCurrent(generation, ClientBattleFailure.Transport);
            return true;
        }

        /// <summary>为测试把 current 首个 baseline 等待推进到精确 deadline。</summary>
        /// <returns>Current generation 正在等待 baseline 且已提交 timeout 时为 true。</returns>
        internal bool InjectQualificationBaselineTimeout()
        {
            long generation;
            long deadline;
            lock (_gate)
            {
                if (_stopped ||
                    _state !=
                        ClientBattleConnectionState.AwaitingBaseline ||
                    _baselineDeadlineMilliseconds == 0)
                {
                    return false;
                }

                generation = _generation;
                deadline = _baselineDeadlineMilliseconds;
            }

            return !DriveBaselineDeadline(
                generation,
                deadline);
        }
#endif

        /// <summary>
        /// 创建 App Scope 唯一 battle network owner。
        /// </summary>
        /// <param name="connectAttempt">Ticket/UDP handshake attempt。</param>
        /// <param name="protocol">Generated protocol adapter。</param>
        /// <param name="inbound">有界main-thread inbound adapter。</param>
        internal BattleNetworkClient(
            IClientBattleConnectAttempt connectAttempt,
            ClientBattleProtocolAdapter protocol,
            ClientBattleInboundRouter inbound)
        {
            _connectAttempt = connectAttempt ??
                throw new ArgumentNullException(nameof(connectAttempt));
            _protocol = protocol ??
                throw new ArgumentNullException(nameof(protocol));
            _inbound = inbound ??
                throw new ArgumentNullException(nameof(inbound));
            _inbound.SnapshotCommitted += OnSnapshotCommitted;
            _inbound.DispatchFailed += OnDispatchFailed;
        }

        /// <summary>
        /// 启用显式 activation，不产生网络副作用。
        /// </summary>
        /// <param name="cancellationToken">AppLifetime startup cancellation。</param>
        /// <returns>同步完成任务。</returns>
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            lock (_gate)
            {
                if (_initialized || _stopped)
                {
                    throw new InvalidOperationException(
                        "BattleNetworkClient cannot initialize twice");
                }

                _initialized = true;
                _state = ClientBattleConnectionState.Ready;
                _failure = ClientBattleFailure.None;
                return Task.CompletedTask;
            }
        }

        /// <summary>
        /// 创建 successor generation，并在 ServerAccept 后启动唯一 pumps。
        /// </summary>
        /// <param name="intent">Frozen current Session/world target。</param>
        /// <param name="cancellationToken">Caller/higher lifecycle cancellation。</param>
        /// <returns>Current low-sensitive connection snapshot。</returns>
        public async Task<ClientBattleConnectionSnapshot> ActivateAsync(
            ClientBattleTargetIntent intent,
            CancellationToken cancellationToken)
        {
            if (intent == null)
            {
                throw new ArgumentNullException(nameof(intent));
            }

            await _lifecycle.WaitAsync(cancellationToken);
            try
            {
                await StopCurrentAsync(
                    ClientBattleFailure.TargetReplaced,
                    graceful: false,
                    cancellationToken);
                long generation;
                lock (_gate)
                {
                    if (!_initialized || _stopped)
                    {
                        return SnapshotLocked();
                    }

                    generation = checked(++_generation);
                    _state = ClientBattleConnectionState.IssuingTicket;
                    _failure = ClientBattleFailure.None;
                    _targetKind = intent.Kind;
                    _role = ClientBattleRole.None;
                    _actorSlot = -1;
                    _latestServerTick = 0;
                    _nextRawApplicationSequence = 1;
                    _lastKcpReceivedSequence = 0;
                    _terminalPublished = false;
                }

                var result = await _connectAttempt.ExecuteAsync(
                    generation,
                    intent,
                    cancellationToken);
                if (!result.IsSuccess)
                {
                    lock (_gate)
                    {
                        if (_generation == generation && !_stopped)
                        {
                            _state = ClientBattleConnectionState.Ready;
                            _failure = result.Failure;
                        }
                    }

                    PublishTerminalOnce(generation, result.Failure);
                    return Snapshot();
                }

                using (result.Connection)
                {
                    lock (_gate)
                    {
                        if (_generation != generation ||
                            _stopped ||
                            _state !=
                            ClientBattleConnectionState.IssuingTicket)
                        {
                            _failure =
                                ClientBattleFailure.TargetReplaced;
                            return SnapshotLocked();
                        }

                        _socket = result.Connection.TakeSocket();
                        _secure = result.Connection.TakeSecure();
                        _kcp = result.Connection.TakeKcp();
                        _role = result.Connection.Role;
                        _actorSlot = result.Connection.ActorSlot;
                        _clock = new ClientBattleAttemptClock();
                        _kcpStartedMilliseconds =
                            _clock.NowMilliseconds;
                        _baselineDeadlineMilliseconds = checked(
                            _kcpStartedMilliseconds +
                            InitialBaselineDeadlineMilliseconds);
                        _sessionCancellation =
                            new CancellationTokenSource();
                        _state =
                            ClientBattleConnectionState.AwaitingBaseline;
                    }
                }

                return Snapshot();
            }
            finally
            {
                _lifecycle.Release();
            }
        }

        /// <summary>
        /// 在 Application generation owners 就绪后启动唯一 send/receive/KCP pumps。
        /// </summary>
        /// <param name="battleGeneration">Authenticated current battle generation。</param>
        /// <returns>首次启动 current generation 成功时为 true。</returns>
        public bool TryStartTraffic(long battleGeneration)
        {
            lock (_gate)
            {
                if (_stopped ||
                    !_initialized ||
                    battleGeneration <= 0 ||
                    _generation != battleGeneration ||
                    _state != ClientBattleConnectionState.AwaitingBaseline ||
                    _socket == null ||
                    _secure == null ||
                    _kcp == null ||
                    _sessionCancellation == null ||
                    _sendPump != null ||
                    _receivePump != null ||
                    _kcpPump != null)
                {
                    return false;
                }

                StartPumpsLocked(
                    battleGeneration,
                    _sessionCancellation.Token);
                return true;
            }
        }

        /// <summary>
        /// 线性化结束 current generation，并尽力完成 authenticated normal close。
        /// </summary>
        /// <param name="failure">Stable close reason。</param>
        /// <param name="cancellationToken">Shared shutdown deadline。</param>
        /// <returns>全部 generation resources 已释放时完成。</returns>
        public async Task DeactivateAsync(
            ClientBattleFailure failure,
            CancellationToken cancellationToken)
        {
            await _lifecycle.WaitAsync(cancellationToken);
            try
            {
                await StopCurrentAsync(
                    failure,
                    graceful: failure !=
                    ClientBattleFailure.Security &&
                    failure != ClientBattleFailure.Protocol &&
                    failure != ClientBattleFailure.Shutdown,
                    cancellationToken);
            }
            finally
            {
                _lifecycle.Release();
            }
        }

        /// <summary>
        /// 读取不产生网络副作用的 immutable snapshot。
        /// </summary>
        /// <returns>Current low-sensitive state。</returns>
        public ClientBattleConnectionSnapshot Snapshot()
        {
            lock (_gate)
            {
                return SnapshotLocked();
            }
        }

        /// <summary>
        /// 以 current session key 从 active path 登记 server-facing candidate endpoint。
        /// </summary>
        /// <remarks>
        /// Candidate 必须来自 Infrastructure network-path observer 或资格 fault gateway；
        /// 本方法不从 Scene、payload 或本地私网 endpoint 猜测 NAT 映射。
        /// </remarks>
        /// <param name="battleGeneration">必须匹配 current battle generation。</param>
        /// <param name="canonicalAddress">16-byte IPv4-mapped 或 IPv6 server-facing address。</param>
        /// <param name="port">Server-facing candidate UDP port。</param>
        /// <returns>Authenticated request 已进入 serialized send queue 时为 true。</returns>
        internal bool TryBeginEndpointRebind(
            long battleGeneration,
            byte[] canonicalAddress,
            ushort port)
        {
            ClientBattleSecureChannel secure;
            ClientBattleAttemptClock clock;
            uint currentEndpointGeneration;
            lock (_gate)
            {
                if (_generation != battleGeneration ||
                    (_state != ClientBattleConnectionState.Active &&
                     _state != ClientBattleConnectionState.AwaitingBaseline) ||
                    _secure == null ||
                    _clock == null ||
                    _pendingRebindNonce != null ||
                    _pendingRekeyNonce != null)
                {
                    return false;
                }

                secure = _secure;
                clock = _clock;
                currentEndpointGeneration =
                    secure.EndpointGeneration;
                if (currentEndpointGeneration == 0 ||
                    currentEndpointGeneration == uint.MaxValue)
                {
                    return false;
                }
            }

            var nonce = new byte[16];
            byte[] plaintext = null;
            byte[] datagram = null;
            try
            {
                ClientBattleNativeCrypto.RandomFill(nonce);
                plaintext =
                    ClientBattleControlCodec.EncodeRebindRequest(
                        canonicalAddress,
                        port,
                        nonce);
                var now = clock.NowMilliseconds;
                if (!secure.TrySeal(
                        ClientBattlePacketKind.Control,
                        plaintext,
                        now,
                        out datagram))
                {
                    FailCurrent(
                        battleGeneration,
                        ClientBattleFailure.Security);
                    return false;
                }

                var queued = false;
                lock (_gate)
                {
                    if (_generation == battleGeneration &&
                        ReferenceEquals(_secure, secure) &&
                        _pendingRebindNonce == null &&
                        _pendingRekeyNonce == null &&
                        (_state == ClientBattleConnectionState.Active ||
                         _state ==
                         ClientBattleConnectionState.AwaitingBaseline) &&
                        TryQueueDatagramLocked(datagram))
                    {
                        _pendingRebindNonce = nonce;
                        nonce = null;
                        _pendingRebindCurrentGeneration =
                            currentEndpointGeneration;
                        _pendingRebindNextGeneration =
                            currentEndpointGeneration + 1;
                        _pendingRebindDeadlineMilliseconds = checked(
                            now +
                            ClientBattleSecureChannel.
                                RolloverDeadlineMilliseconds);
                        _pendingRebindConfirmationSent = false;
                        _state =
                            ClientBattleConnectionState.Rebinding;
                        datagram = null;
                        queued = true;
                    }
                }

                if (!queued)
                {
                    FailCurrent(
                        battleGeneration,
                        ClientBattleFailure.Backpressure);
                }

                return queued;
            }
            catch (ArgumentException)
            {
                return false;
            }
            catch (InvalidOperationException)
            {
                FailCurrent(
                    battleGeneration,
                    ClientBattleFailure.Security);
                return false;
            }
            finally
            {
                Clear(ref nonce);
                Clear(plaintext);
                Clear(datagram);
            }
        }

        /// <summary>
        /// 编码并排队 current generation 的 raw input bundle。
        /// </summary>
        /// <param name="battleGeneration">必须匹配 active generation。</param>
        /// <param name="bundle">Immutable semantic input bundle。</param>
        /// <returns>成功取得 send queue ownership 时为 true。</returns>
        public bool TrySendInput(
            long battleGeneration,
            ClientBattleInputBundle bundle)
        {
            if (bundle == null)
            {
                return false;
            }

            ulong applicationSequence;
            ClientBattleSecureChannel secure;
            ClientBattleAttemptClock clock;
            lock (_gate)
            {
                if (_state != ClientBattleConnectionState.Active ||
                    _generation != battleGeneration ||
                    _secure == null ||
                    _nextRawApplicationSequence == ulong.MaxValue)
                {
                    return false;
                }

                applicationSequence = _nextRawApplicationSequence++;
                secure = _secure;
                clock = _clock;
            }

            byte[] payload = null;
            byte[] plaintext = null;
            byte[] datagram = null;
            try
            {
                payload = _protocol.EncodeInputBundle(bundle);
                plaintext = ClientBattleRouteCodec.EncodeRawClient(
                    ClientBattleRouteCatalog.Input,
                    applicationSequence,
                    payload);
                if (!secure.TrySeal(
                        ClientBattlePacketKind.Raw,
                        plaintext,
                        clock.NowMilliseconds,
                        out datagram))
                {
                    return false;
                }

                lock (_gate)
                {
                    if (_generation != battleGeneration ||
                        _state != ClientBattleConnectionState.Active ||
                        !ReferenceEquals(_secure, secure) ||
                        !TryQueueDatagramLocked(datagram))
                    {
                        return false;
                    }

                    datagram = null;
                    return true;
                }
            }
            catch (ArgumentException)
            {
                return false;
            }
            catch (InvalidOperationException)
            {
                return false;
            }
            finally
            {
                Clear(payload);
                Clear(plaintext);
                Clear(datagram);
            }
        }

        /// <summary>
        /// 把 single-flight resync request 交给 current KCP context。
        /// </summary>
        /// <param name="request">Current generation request。</param>
        /// <returns>KCP queue 与 application expiry 已取得 ownership 时为 true。</returns>
        public bool TryRequestResync(ClientBattleResyncRequest request)
        {
            if (request == null)
            {
                return false;
            }

            ClientBattleKcpLease kcp;
            long now;
            lock (_gate)
            {
                if ((_state !=
                     ClientBattleConnectionState.Active &&
                     _state !=
                     ClientBattleConnectionState.AwaitingBaseline) ||
                    request.BattleGeneration != _generation ||
                    _kcp == null ||
                    _kcpInflightDeadlineMilliseconds != 0)
                {
                    return false;
                }

                kcp = _kcp;
                now = _clock.NowMilliseconds;
            }

            byte[] payload = null;
            byte[] message = null;
            try
            {
                payload = _protocol.EncodeResyncRequest(request);
                message = ClientBattleRouteCodec.EncodeResyncRequest(
                    request.RequestSequence,
                    payload);
                lock (_kcpGate)
                {
                    kcp.Send(message);
                }

                lock (_gate)
                {
                    if (!ReferenceEquals(_kcp, kcp) ||
                        request.BattleGeneration != _generation)
                    {
                        return false;
                    }

                    _kcpInflightDeadlineMilliseconds = checked(
                        now +
                        ClientBattleRouteCatalog.ResyncRequest.
                            ExpiryMilliseconds);
                    return true;
                }
            }
            catch (ClientBattleNativeException)
            {
                return false;
            }
            catch (ArgumentException)
            {
                return false;
            }
            finally
            {
                Clear(payload);
                Clear(message);
            }
        }

        /// <summary>
        /// 由 AppLifetime 不可逆停止 owner。
        /// </summary>
        /// <param name="cancellationToken">Shared shutdown deadline。</param>
        /// <returns>Current session 与 pumps 已结束时完成。</returns>
        public async Task StopAsync(CancellationToken cancellationToken)
        {
            await _lifecycle.WaitAsync(cancellationToken);
            try
            {
                lock (_gate)
                {
                    if (_stopped)
                    {
                        return;
                    }

                    _stopped = true;
                }

                await StopCurrentAsync(
                    ClientBattleFailure.Shutdown,
                    graceful: false,
                    cancellationToken);
                lock (_gate)
                {
                    _initialized = false;
                    _state = ClientBattleConnectionState.Stopped;
                }

                _inbound.SnapshotCommitted -= OnSnapshotCommitted;
                _inbound.DispatchFailed -= OnDispatchFailed;
            }
            finally
            {
                _lifecycle.Release();
            }
        }

        /// <summary>
        /// 启动 current generation 的三个唯一 background pumps。
        /// </summary>
        /// <param name="generation">Frozen generation。</param>
        /// <param name="cancellationToken">Session cancellation。</param>
        private void StartPumpsLocked(
            long generation,
            CancellationToken cancellationToken)
        {
            _sendPump = RunSendPumpAsync(
                generation,
                cancellationToken);
            _receivePump = RunReceivePumpAsync(
                generation,
                cancellationToken);
            _kcpPump = RunKcpPumpAsync(
                generation,
                cancellationToken);
        }

        /// <summary>
        /// 串行发送 managed queue 中的 protected datagram。
        /// </summary>
        /// <param name="generation">Frozen generation。</param>
        /// <param name="cancellationToken">Session cancellation。</param>
        /// <returns>取消或 terminal 时完成。</returns>
        private async Task RunSendPumpAsync(
            long generation,
            CancellationToken cancellationToken)
        {
            try
            {
                for (;;)
                {
                    await _sendSignal.WaitAsync(cancellationToken);
                    for (;;)
                    {
                        byte[] datagram;
                        IClientBattleDatagramSocket socket;
                        lock (_gate)
                        {
                            if (_generation != generation ||
                                _sendQueue.Count == 0)
                            {
                                break;
                            }

                            datagram = _sendQueue.Dequeue();
                            socket = _socket;
                        }

                        try
                        {
                            if (socket == null ||
                                await socket.SendAsync(
                                    datagram,
                                    cancellationToken) !=
                                datagram.Length)
                            {
                                throw new InvalidOperationException(
                                    "client battle UDP send was partial");
                            }
                        }
                        finally
                        {
                            Clear(datagram);
                        }
                    }
                }
            }
            catch (OperationCanceledException)
                when (cancellationToken.IsCancellationRequested)
            {
            }
            catch (SocketException)
            {
                FailCurrent(generation, ClientBattleFailure.Transport);
            }
            catch (Exception)
            {
                FailCurrent(generation, ClientBattleFailure.Transport);
            }
        }

        /// <summary>
        /// 接收、认证、replay-gate 并分发唯一 connected socket 的 datagram。
        /// </summary>
        /// <param name="generation">Frozen generation。</param>
        /// <param name="cancellationToken">Session cancellation。</param>
        /// <returns>取消或 terminal 时完成。</returns>
        private async Task RunReceivePumpAsync(
            long generation,
            CancellationToken cancellationToken)
        {
            try
            {
                for (;;)
                {
                    IClientBattleDatagramSocket socket;
                    ClientBattleSecureChannel secure;
                    ClientBattleAttemptClock clock;
                    lock (_gate)
                    {
                        if (_generation != generation)
                        {
                            return;
                        }

                        socket = _socket;
                        secure = _secure;
                        clock = _clock;
                    }

                    var datagram = await socket.ReceiveAsync(
                        ClientBattleSecureChannel.MaximumDatagramBytes,
                        cancellationToken);
                    ClientBattleOpenPacket opened;
                    try
                    {
                        opened = secure.Open(
                            datagram,
                            clock.NowMilliseconds);
                    }
                    finally
                    {
                        Clear(datagram);
                    }

                    if (!HandleOpenDisposition(generation, opened))
                    {
                        if (opened.Disposition ==
                            ClientBattleOpenDisposition.Duplicate ||
                            opened.Disposition ==
                            ClientBattleOpenDisposition.TooOld)
                        {
                            continue;
                        }

                        return;
                    }

                    try
                    {
                        switch (opened.Kind)
                        {
                            case ClientBattlePacketKind.Raw:
                                if (!HandleRaw(
                                        generation,
                                        opened.Plaintext))
                                {
                                    FailCurrent(
                                        generation,
                                        ClientBattleFailure.Protocol);
                                    return;
                                }

                                break;
                            case ClientBattlePacketKind.Kcp:
                                lock (_kcpGate)
                                {
                                    _kcp.Input(opened.Plaintext);
                                }

                                break;
                            case ClientBattlePacketKind.Control:
                                if (!HandleControl(
                                        generation,
                                        opened.Plaintext))
                                {
                                    FailCurrent(
                                        generation,
                                        ClientBattleFailure.Security);
                                    return;
                                }

                                break;
                            default:
                                FailCurrent(
                                    generation,
                                    ClientBattleFailure.Protocol);
                                return;
                        }
                    }
                    finally
                    {
                        Clear(opened.Plaintext);
                    }
                }
            }
            catch (OperationCanceledException)
                when (cancellationToken.IsCancellationRequested)
            {
            }
            catch (SocketException exception)
            {
                ReportTerminalDiagnostic(
                    generation,
                    "receive-socket",
                    exception);
                FailCurrent(generation, ClientBattleFailure.Transport);
            }
            catch (ClientBattleNativeException exception)
            {
                ReportTerminalDiagnostic(
                    generation,
                    "receive-native",
                    exception);
                FailCurrent(generation, ClientBattleFailure.Protocol);
            }
            catch (ClientBattleInboundBackpressureException exception)
            {
                ReportTerminalDiagnostic(
                    generation,
                    "receive-inbound-backpressure",
                    exception);
                FailCurrent(generation, ClientBattleFailure.Backpressure);
            }
            catch (Exception exception)
            {
                ReportTerminalDiagnostic(
                    generation,
                    "receive-unhandled",
                    exception);
                FailCurrent(generation, ClientBattleFailure.Protocol);
            }
        }

        /// <summary>
        /// 每 10ms 推进 KCP、收集 output/message，并驱动 rekey deadline。
        /// </summary>
        /// <param name="generation">Frozen generation。</param>
        /// <param name="cancellationToken">Session cancellation。</param>
        /// <returns>取消或 terminal 时完成。</returns>
        private async Task RunKcpPumpAsync(
            long generation,
            CancellationToken cancellationToken)
        {
            var outputBuffer = new byte[1024];
            var messageBuffer = new byte[1000];
            try
            {
                for (;;)
                {
                    ClientBattleKcpLease kcp;
                    ClientBattleSecureChannel secure;
                    ClientBattleAttemptClock clock;
                    lock (_gate)
                    {
                        if (_generation != generation)
                        {
                            return;
                        }

                        kcp = _kcp;
                        secure = _secure;
                        clock = _clock;
                    }

                    var now = clock.NowMilliseconds;
                    var relative = checked(
                        (uint)Math.Min(
                            uint.MaxValue,
                            now - _kcpStartedMilliseconds));
                    var outputs = new List<byte[]>();
                    var messages = new List<byte[]>();
                    uint waiting;
                    lock (_kcpGate)
                    {
                        kcp.Update(relative);
                        while (kcp.TryReadOutput(
                                   outputBuffer,
                                   out var outputLength))
                        {
                            outputs.Add(
                                Copy(outputBuffer, outputLength));
                        }

                        while (kcp.TryReceive(
                                   messageBuffer,
                                   out var messageLength))
                        {
                            messages.Add(
                                Copy(messageBuffer, messageLength));
                        }

                        waiting = kcp.WaitingSegments();
                    }

                    foreach (var segment in outputs)
                    {
                        byte[] datagram = null;
                        try
                        {
                            if (!secure.TrySeal(
                                    ClientBattlePacketKind.Kcp,
                                    segment,
                                    now,
                                    out datagram))
                            {
                                FailCurrent(
                                    generation,
                                    ClientBattleFailure.Security);
                                return;
                            }

                            var queued = false;
                            lock (_gate)
                            {
                                if (_generation == generation &&
                                    TryQueueDatagramLocked(datagram))
                                {
                                    datagram = null;
                                    queued = true;
                                }
                            }

                            if (!queued)
                            {
                                FailCurrent(
                                    generation,
                                    ClientBattleFailure.Backpressure);
                                return;
                            }
                        }
                        finally
                        {
                            Clear(segment);
                            Clear(datagram);
                        }
                    }

                    foreach (var message in messages)
                    {
                        try
                        {
                            if (!HandleKcpMessage(generation, message))
                            {
                                FailCurrent(
                                    generation,
                                    ClientBattleFailure.Protocol);
                                return;
                            }
                        }
                        finally
                        {
                            Clear(message);
                        }
                    }

                    var inflightExpired = false;
                    lock (_gate)
                    {
                        if (waiting == 0)
                        {
                            _kcpInflightDeadlineMilliseconds = 0;
                        }
                        else if (
                            _kcpInflightDeadlineMilliseconds != 0 &&
                            now >= _kcpInflightDeadlineMilliseconds)
                        {
                            inflightExpired = true;
                        }
                    }

                    if (inflightExpired)
                    {
                        FailCurrent(
                            generation,
                            ClientBattleFailure.Backpressure);
                        return;
                    }

                    if (!DriveBaselineDeadline(generation, now))
                    {
                        return;
                    }

                    if (!DriveRebind(generation, now))
                    {
                        return;
                    }

                    if (!DriveRekey(generation, secure, now))
                    {
                        return;
                    }

                    await Task.Delay(10, cancellationToken);
                }
            }
            catch (OperationCanceledException)
                when (cancellationToken.IsCancellationRequested)
            {
            }
            catch (ClientBattleNativeException exception)
            {
                ReportTerminalDiagnostic(
                    generation,
                    "kcp-native",
                    exception);
                FailCurrent(generation, ClientBattleFailure.Backpressure);
            }
            catch (ClientBattleInboundBackpressureException exception)
            {
                ReportTerminalDiagnostic(
                    generation,
                    "kcp-inbound-backpressure",
                    exception);
                FailCurrent(generation, ClientBattleFailure.Backpressure);
            }
            catch (Exception exception)
            {
                ReportTerminalDiagnostic(
                    generation,
                    "kcp-unhandled",
                    exception);
                FailCurrent(generation, ClientBattleFailure.Protocol);
            }
            finally
            {
                Clear(outputBuffer);
                Clear(messageBuffer);
            }
        }

        /// <summary>
        /// 处理 authenticated raw snapshot 并在首个完整 full 发布后开启 input。
        /// </summary>
        /// <param name="generation">Frozen generation。</param>
        /// <param name="plaintext">Caller-owned route frame。</param>
        /// <returns>Envelope、Protobuf 与 sink commit 合法时为 true。</returns>
        private bool HandleRaw(long generation, byte[] plaintext)
        {
            if (!ClientBattleRouteCodec.TryDecodeRawServer(
                    plaintext,
                    out var frame))
            {
                ReportTerminalDiagnostic(
                    generation,
                    "raw-route-decode",
                    null);
                return false;
            }

            try
            {
                if (!_protocol.TryDecodeSnapshot(
                        generation,
                        frame,
                        out var partition))
                {
                    ReportTerminalDiagnostic(
                        generation,
                        "raw-snapshot-decode",
                        null);
                    return false;
                }

                var commit = _inbound.AcceptSnapshot(partition);

                return commit ==
                           ClientBattleSnapshotCommit.Pending ||
                       commit ==
                           ClientBattleSnapshotCommit.Published ||
                       commit ==
                           ClientBattleSnapshotCommit.Duplicate ||
                       commit ==
                           ClientBattleSnapshotCommit.ResyncRequested;
            }
            finally
            {
                Clear(frame.Payload);
            }
        }

        /// <summary>
        /// 在主线程Application owner提交完整partition后推进只读connection frontier。
        /// </summary>
        /// <param name="partition">已提交或被幂等拒绝的partition。</param>
        /// <param name="commit">Replica closed commit结果。</param>
        private void OnSnapshotCommitted(
            ClientBattleSnapshotPartition partition,
            ClientBattleSnapshotCommit commit)
        {
            if (partition == null)
            {
                return;
            }

            if (commit != ClientBattleSnapshotCommit.Pending &&
                commit != ClientBattleSnapshotCommit.Published &&
                commit != ClientBattleSnapshotCommit.Duplicate &&
                commit != ClientBattleSnapshotCommit.ResyncRequested)
            {
                FailCurrent(
                    partition.BattleGeneration,
                    ClientBattleFailure.Protocol);
                return;
            }

            if (commit != ClientBattleSnapshotCommit.Published)
            {
                return;
            }

            lock (_gate)
            {
                if (_generation != partition.BattleGeneration ||
                    _terminalPublished)
                {
                    return;
                }

                _latestServerTick = Math.Max(
                    _latestServerTick,
                    partition.ServerTick);
                if (partition.Kind == ClientBattleSnapshotKind.Full &&
                    _state ==
                    ClientBattleConnectionState.AwaitingBaseline)
                {
                    _baselineDeadlineMilliseconds = 0;
                    _state = ClientBattleConnectionState.Active;
                }
            }
        }

        /// <summary>把main-thread sink failure收敛到current generation唯一terminal。</summary>
        /// <param name="battleGeneration">发生失败的generation。</param>
        /// <param name="failure">Protocol或Backpressure稳定原因。</param>
        private void OnDispatchFailed(
            long battleGeneration,
            ClientBattleFailure failure)
        {
            FailCurrent(battleGeneration, failure);
        }

        /// <summary>
        /// 处理 KCP reassembly 后的 reliable Application message。
        /// </summary>
        /// <param name="generation">Frozen generation。</param>
        /// <param name="message">Caller-owned KCP message。</param>
        /// <returns>Closed route、sequence 与 typed sink commit 合法时为 true。</returns>
        private bool HandleKcpMessage(long generation, byte[] message)
        {
            if (!ClientBattleRouteCodec.TryDecodeKcpServer(
                    message,
                    out var frame))
            {
                ReportTerminalDiagnostic(
                    generation,
                    "kcp-route-decode",
                    null);
                return false;
            }

            try
            {
                lock (_gate)
                {
                    if (_generation != generation ||
                        frame.ApplicationSequence <=
                        _lastKcpReceivedSequence)
                    {
                        ReportTerminalDiagnostic(
                            generation,
                            "kcp-application-sequence",
                            null);
                        return false;
                    }

                    _lastKcpReceivedSequence =
                        frame.ApplicationSequence;
                }

                if (ReferenceEquals(
                        frame.Route,
                        ClientBattleRouteCatalog.AbilityEvent))
                {
                    return _protocol.TryDecodeAbilityEvent(
                               generation,
                               frame,
                               out var ability) &&
                           _inbound.AcceptAbilityEvent(ability);
                }

                if (ReferenceEquals(
                        frame.Route,
                        ClientBattleRouteCatalog.EntityLifecycle))
                {
                    return _protocol.TryDecodeEntityLifecycle(
                               generation,
                               frame,
                               out var lifecycle) &&
                           _inbound.AcceptEntityLifecycle(lifecycle);
                }

                if (ReferenceEquals(
                        frame.Route,
                        ClientBattleRouteCatalog.ResyncResponse))
                {
                    return _protocol.TryDecodeResyncResponse(
                               generation,
                               frame,
                               out var response) &&
                           _inbound.AcceptResyncResponse(
                               response,
                               _clock.NowMilliseconds);
                }

                return false;
            }
            finally
            {
                Clear(frame.Payload);
            }
        }

        /// <summary>
        /// 在 Development/Editor 中记录不含凭据、payload 或玩家身份的 terminal 阶段。
        /// </summary>
        /// <param name="generation">发生失败的 frozen generation。</param>
        /// <param name="stage">封闭的本地处理阶段。</param>
        /// <param name="exception">可选的本地异常；只输出类型与受控消息。</param>
        private void ReportTerminalDiagnostic(
            long generation,
            string stage,
            Exception exception)
        {
#if DEVELOPMENT_BUILD || UNITY_EDITOR
            lock (_gate)
            {
                if (_generation != generation ||
                    _terminalPublished)
                {
                    return;
                }
            }

            var exceptionType = exception?.GetType().Name ?? "none";
            var reason = exception?.Message ?? "rejected";
            Debug.LogError(
                "[IHOMELAND_BATTLE_TERMINAL] " +
                $"generation={generation} stage={stage} " +
                $"exception={exceptionType} reason={reason}");
#endif
        }

        /// <summary>
        /// 处理 server-only control response，并原子推进 rekey/close。
        /// </summary>
        /// <param name="generation">Frozen generation。</param>
        /// <param name="plaintext">Authenticated IHBC plaintext。</param>
        /// <returns>Expected current transition 成功时为 true。</returns>
        private bool HandleControl(long generation, byte[] plaintext)
        {
            if (!ClientBattleControlCodec.TryDecodeServer(
                    plaintext,
                    out var message))
            {
                return false;
            }

            try
            {
                if (message.Kind ==
                    ClientBattleControlKind.RebindChallenge)
                {
                    return HandleRebindChallenge(
                        generation,
                        message.Payload);
                }

                if (message.Kind ==
                    ClientBattleControlKind.RebindCommitted)
                {
                    return HandleRebindCommitted(
                        generation,
                        message.Payload);
                }

                if (message.Kind ==
                    ClientBattleControlKind.RekeyCommitted)
                {
                    byte[] expected;
                    uint nextEpoch;
                    long now;
                    ClientBattleSecureChannel secure;
                    lock (_gate)
                    {
                        if (_generation != generation ||
                            _pendingRekeyNonce == null ||
                            _state !=
                            ClientBattleConnectionState.Rekeying)
                        {
                            return false;
                        }

                        expected = _pendingRekeyNonce;
                        nextEpoch = _pendingRekeyEpoch;
                        secure = _secure;
                        now = _clock.NowMilliseconds;
                    }

                    if (!ClientBattleNativeCrypto.ConstantTimeEqual(
                            expected,
                            message.Payload) ||
                        !secure.CommitRollover(
                            expected,
                            nextEpoch,
                            now))
                    {
                        return false;
                    }

                    lock (_gate)
                    {
                        if (_generation != generation)
                        {
                            return false;
                        }

                        Clear(ref _pendingRekeyNonce);
                        _pendingRekeyEpoch = 0;
                        _pendingRekeyDeadlineMilliseconds = 0;
                        _state = _latestServerTick == 0
                            ? ClientBattleConnectionState.AwaitingBaseline
                            : ClientBattleConnectionState.Active;
                    }

                    return true;
                }

                if (message.Kind ==
                    ClientBattleControlKind.CloseAcknowledged)
                {
                    if (message.Payload[0] !=
                        ClientBattleControlCodec.ClientRequestedClose)
                    {
                        return false;
                    }

                    lock (_gate)
                    {
                        if (_generation != generation ||
                            _state !=
                            ClientBattleConnectionState.Closing)
                        {
                            return false;
                        }

                        _closeAcknowledgement?.TrySetResult(true);
                    }

                    return true;
                }

                return false;
            }
            finally
            {
                Clear(message.Payload);
            }
        }

        /// <summary>
        /// 验证 server 从 candidate path 返回的 challenge 并排队 exact confirmation。
        /// </summary>
        /// <param name="generation">Frozen battle generation。</param>
        /// <param name="payload">Authenticated 48-byte challenge payload。</param>
        /// <returns>Generation、nonce、deadline、cookie 与 queue 全部有效时为 true。</returns>
        private bool HandleRebindChallenge(
            long generation,
            byte[] payload)
        {
            byte[] expectedNonce;
            uint expectedCurrent;
            uint expectedNext;
            long localDeadline;
            long now;
            ClientBattleSecureChannel secure;
            lock (_gate)
            {
                if (_generation != generation ||
                    _state != ClientBattleConnectionState.Rebinding ||
                    _pendingRebindNonce == null ||
                    _pendingRebindConfirmationSent ||
                    _clock == null ||
                    _secure == null)
                {
                    return false;
                }

                expectedNonce = _pendingRebindNonce;
                expectedCurrent =
                    _pendingRebindCurrentGeneration;
                expectedNext = _pendingRebindNextGeneration;
                localDeadline =
                    _pendingRebindDeadlineMilliseconds;
                now = _clock.NowMilliseconds;
                secure = _secure;
            }

            if (payload == null ||
                payload.Length != 48 ||
                ReadUInt32BigEndian(payload, 0) !=
                expectedCurrent ||
                ReadUInt32BigEndian(payload, 4) !=
                expectedNext ||
                ReadUInt64BigEndian(payload, 24) <=
                checked((ulong)now) ||
                now >= localDeadline ||
                IsAllZero(payload, 32, 16))
            {
                return false;
            }

            var challengeNonce = Copy(payload, 8, 16);
            byte[] plaintext = null;
            byte[] datagram = null;
            try
            {
                if (!ClientBattleNativeCrypto.ConstantTimeEqual(
                        expectedNonce,
                        challengeNonce))
                {
                    return false;
                }

                plaintext =
                    ClientBattleControlCodec.EncodeRebindConfirm(
                        payload);
                if (!secure.TrySeal(
                        ClientBattlePacketKind.Control,
                        plaintext,
                        now,
                        out datagram))
                {
                    return false;
                }

                lock (_gate)
                {
                    if (_generation != generation ||
                        _state !=
                        ClientBattleConnectionState.Rebinding ||
                        _pendingRebindConfirmationSent ||
                        !ReferenceEquals(_secure, secure) ||
                        !TryQueueDatagramLocked(datagram))
                    {
                        return false;
                    }

                    _pendingRebindConfirmationSent = true;
                    datagram = null;
                    return true;
                }
            }
            finally
            {
                Clear(challengeNonce);
                Clear(plaintext);
                Clear(datagram);
            }
        }

        /// <summary>
        /// 验证 server pre-commit acknowledgement 后只推进 endpoint generation。
        /// </summary>
        /// <param name="generation">Frozen battle generation。</param>
        /// <param name="payload">Authenticated 20-byte committed payload。</param>
        /// <returns>Exact next generation 与 request nonce 原子提交时为 true。</returns>
        private bool HandleRebindCommitted(
            long generation,
            byte[] payload)
        {
            byte[] expectedNonce;
            uint expectedCurrent;
            uint expectedNext;
            ClientBattleSecureChannel secure;
            lock (_gate)
            {
                if (_generation != generation ||
                    _state != ClientBattleConnectionState.Rebinding ||
                    _pendingRebindNonce == null ||
                    !_pendingRebindConfirmationSent ||
                    _secure == null)
                {
                    return false;
                }

                expectedNonce = _pendingRebindNonce;
                expectedCurrent =
                    _pendingRebindCurrentGeneration;
                expectedNext = _pendingRebindNextGeneration;
                secure = _secure;
            }

            if (payload == null ||
                payload.Length != 20 ||
                ReadUInt32BigEndian(payload, 0) !=
                expectedNext)
            {
                return false;
            }

            var committedNonce = Copy(payload, 4, 16);
            try
            {
                if (!ClientBattleNativeCrypto.ConstantTimeEqual(
                        expectedNonce,
                        committedNonce) ||
                    !secure.CommitEndpointGeneration(
                        expectedCurrent,
                        expectedNext))
                {
                    return false;
                }

                lock (_gate)
                {
                    if (_generation != generation ||
                        !ReferenceEquals(_secure, secure))
                    {
                        return false;
                    }

                    Clear(ref _pendingRebindNonce);
                    _pendingRebindCurrentGeneration = 0;
                    _pendingRebindNextGeneration = 0;
                    _pendingRebindDeadlineMilliseconds = 0;
                    _pendingRebindConfirmationSent = false;
                    _state = _latestServerTick == 0
                        ? ClientBattleConnectionState.AwaitingBaseline
                        : ClientBattleConnectionState.Active;
                    return true;
                }
            }
            finally
            {
                Clear(committedNonce);
            }
        }

        /// <summary>
        /// 把 secure open disposition 映射为 replay suppression 或 terminal。
        /// </summary>
        /// <param name="generation">Frozen generation。</param>
        /// <param name="opened">Secure open result。</param>
        /// <returns>只有 Accepted 时为 true。</returns>
        private bool HandleOpenDisposition(
            long generation,
            ClientBattleOpenPacket opened)
        {
            switch (opened.Disposition)
            {
                case ClientBattleOpenDisposition.Accepted:
                    return true;
                case ClientBattleOpenDisposition.Duplicate:
                case ClientBattleOpenDisposition.TooOld:
                    return false;
                case ClientBattleOpenDisposition.AuthenticationFailed:
                case ClientBattleOpenDisposition.FutureJump:
                case ClientBattleOpenDisposition.Closed:
                    FailCurrent(
                        generation,
                        ClientBattleFailure.Security);
                    return false;
                default:
                    FailCurrent(
                        generation,
                        ClientBattleFailure.Protocol);
                    return false;
            }
        }

        /// <summary>把缺失首个 full snapshot 收敛为可恢复 timeout，而非无限 loading。</summary>
        /// <param name="generation">Frozen generation。</param>
        /// <param name="nowMilliseconds">Current runtime monotonic time。</param>
        /// <returns>Baseline 已到达或仍在 deadline 内时为 true。</returns>
        private bool DriveBaselineDeadline(
            long generation,
            long nowMilliseconds)
        {
            var expired = false;
            lock (_gate)
            {
                if (_generation != generation)
                {
                    return false;
                }

                expired =
                    _latestServerTick == 0 &&
                    _baselineDeadlineMilliseconds != 0 &&
                    nowMilliseconds >=
                        _baselineDeadlineMilliseconds;
            }

            if (!expired)
            {
                return true;
            }

            FailCurrent(
                generation,
                ClientBattleFailure.Timeout);
            return false;
        }

        /// <summary>
        /// 驱动 authenticated rebind 的单一总 deadline。
        /// </summary>
        /// <param name="generation">Frozen generation。</param>
        /// <param name="nowMilliseconds">Current runtime time。</param>
        /// <returns>尚未到期或当前没有 rebind 时为 true。</returns>
        private bool DriveRebind(
            long generation,
            long nowMilliseconds)
        {
            var expired = false;
            lock (_gate)
            {
                if (_generation != generation)
                {
                    return false;
                }

                expired =
                    _pendingRebindNonce != null &&
                    nowMilliseconds >=
                    _pendingRebindDeadlineMilliseconds;
            }

            if (!expired)
            {
                return true;
            }

            FailCurrent(
                generation,
                ClientBattleFailure.Timeout);
            return false;
        }

        /// <summary>
        /// 达到 trigger 时排队一次 rekey proposal，并执行 3 秒 response deadline。
        /// </summary>
        /// <param name="generation">Frozen generation。</param>
        /// <param name="secure">Current secure owner。</param>
        /// <param name="nowMilliseconds">Current runtime time。</param>
        /// <returns>可以继续 pump 时为 true。</returns>
        private bool DriveRekey(
            long generation,
            ClientBattleSecureChannel secure,
            long nowMilliseconds)
        {
            var rekeyExpired = false;
            lock (_gate)
            {
                if (_generation != generation)
                {
                    return false;
                }

                if (_pendingRebindNonce != null ||
                    _state ==
                    ClientBattleConnectionState.Rebinding)
                {
                    return true;
                }

                if (_pendingRekeyNonce != null)
                {
                    if (nowMilliseconds >=
                        _pendingRekeyDeadlineMilliseconds)
                    {
                        rekeyExpired = true;
                    }
                    else
                    {
                        return true;
                    }
                }
            }

            if (rekeyExpired)
            {
                FailCurrent(
                    generation,
                    ClientBattleFailure.Timeout);
                return false;
            }

            if (!secure.RolloverRequired(nowMilliseconds))
            {
                return true;
            }

            var nonce = new byte[ClientBattleNativeCrypto.KeyBytes];
            byte[] plaintext = null;
            byte[] datagram = null;
            try
            {
                ClientBattleNativeCrypto.RandomFill(nonce);
                plaintext =
                    ClientBattleControlCodec.EncodeRekeyProposal(nonce);
                if (!secure.TrySeal(
                        ClientBattlePacketKind.Control,
                        plaintext,
                        nowMilliseconds,
                        out datagram))
                {
                    FailCurrent(
                        generation,
                        ClientBattleFailure.Security);
                    return false;
                }

                var queued = false;
                lock (_gate)
                {
                    if (_generation == generation &&
                        _secure == secure &&
                        _pendingRekeyNonce == null &&
                        secure.KeyEpoch != uint.MaxValue &&
                        TryQueueDatagramLocked(datagram))
                    {
                        _pendingRekeyNonce = nonce;
                        nonce = null;
                        _pendingRekeyEpoch = secure.KeyEpoch + 1;
                        _pendingRekeyDeadlineMilliseconds = checked(
                            nowMilliseconds +
                            ClientBattleSecureChannel.
                                RolloverDeadlineMilliseconds);
                        _state = ClientBattleConnectionState.Rekeying;
                        datagram = null;
                        queued = true;
                    }
                }

                if (!queued)
                {
                    FailCurrent(
                        generation,
                        ClientBattleFailure.Backpressure);
                    return false;
                }

                return true;
            }
            finally
            {
                Clear(nonce);
                Clear(plaintext);
                Clear(datagram);
            }
        }

        /// <summary>
        /// 停止 pumps、清空 queue 并逆序释放 current generation resources。
        /// </summary>
        /// <param name="failure">Stable close reason。</param>
        /// <param name="graceful">是否尽力等待 authenticated close ack。</param>
        /// <param name="cancellationToken">Shared cleanup deadline。</param>
        /// <returns>Current resources 已释放时完成。</returns>
        private async Task StopCurrentAsync(
            ClientBattleFailure failure,
            bool graceful,
            CancellationToken cancellationToken)
        {
            Task sendPump;
            Task receivePump;
            Task kcpPump;
            CancellationTokenSource sessionCancellation;
            IClientBattleDatagramSocket socket;
            ClientBattleSecureChannel secure;
            ClientBattleKcpLease kcp;
            Task closeWait = null;
            long generation;
            lock (_gate)
            {
                generation = _generation;
                if (_socket == null &&
                    _secure == null &&
                    _kcp == null &&
                    _sessionCancellation == null)
                {
                    if (!_stopped && _initialized)
                    {
                        _state = ClientBattleConnectionState.Ready;
                        if (failure != ClientBattleFailure.TargetReplaced)
                        {
                            _failure = failure;
                        }
                    }

                    return;
                }

                _state = ClientBattleConnectionState.Closing;
                _failure = failure;
                if (graceful && _secure != null && _socket != null)
                {
                    closeWait = TryQueueCloseLocked();
                }

                sendPump = _sendPump;
                receivePump = _receivePump;
                kcpPump = _kcpPump;
                sessionCancellation = _sessionCancellation;
                socket = _socket;
                secure = _secure;
                kcp = _kcp;
            }

            if (closeWait != null)
            {
                using (var closeDeadline =
                           CancellationTokenSource.
                               CreateLinkedTokenSource(
                                   cancellationToken))
                {
                    closeDeadline.CancelAfter(
                        TimeSpan.FromMilliseconds(
                            ClientBattleSecureChannel.
                                RolloverDeadlineMilliseconds));
                    try
                    {
                        await WaitWithCancellationAsync(
                            closeWait,
                            closeDeadline.Token);
                    }
                    catch (OperationCanceledException)
                    {
                    }
                }
            }

            PublishTerminalOnce(generation, failure);
            sessionCancellation?.Cancel();
            socket?.Dispose();
            _sendSignal.Release();
            await ObservePumpAsync(sendPump);
            await ObservePumpAsync(receivePump);
            await ObservePumpAsync(kcpPump);

            lock (_gate)
            {
                if (_generation == generation)
                {
                    while (_sendQueue.Count > 0)
                    {
                        Clear(_sendQueue.Dequeue());
                    }

                    Clear(ref _pendingRekeyNonce);
                    _pendingRekeyEpoch = 0;
                    _pendingRekeyDeadlineMilliseconds = 0;
                    Clear(ref _pendingRebindNonce);
                    _pendingRebindCurrentGeneration = 0;
                    _pendingRebindNextGeneration = 0;
                    _pendingRebindDeadlineMilliseconds = 0;
                    _pendingRebindConfirmationSent = false;
                    _kcpInflightDeadlineMilliseconds = 0;
                    _closeAcknowledgement = null;
                    _socket = null;
                    _secure = null;
                    _kcp = null;
                    _sessionCancellation = null;
                    _sendPump = null;
                    _receivePump = null;
                    _kcpPump = null;
                    _clock = null;
                    _role = ClientBattleRole.None;
                    _actorSlot = -1;
                    _latestServerTick = 0;
                    _baselineDeadlineMilliseconds = 0;
                    if (!_stopped)
                    {
                        _state = ClientBattleConnectionState.Ready;
                    }
                }
            }

            kcp?.Dispose();
            secure?.Dispose();
            sessionCancellation?.Dispose();
        }

        /// <summary>
        /// 排队 authenticated close request 并返回 acknowledgement signal。
        /// </summary>
        /// <returns>成功排队时返回 shared acknowledgement task。</returns>
        private Task TryQueueCloseLocked()
        {
            byte[] plaintext = null;
            byte[] datagram = null;
            try
            {
                plaintext =
                    ClientBattleControlCodec.EncodeCloseRequest();
                if (!_secure.TrySeal(
                        ClientBattlePacketKind.Control,
                        plaintext,
                        _clock.NowMilliseconds,
                        out datagram) ||
                    !TryQueueDatagramLocked(datagram))
                {
                    return null;
                }

                datagram = null;
                _closeAcknowledgement =
                    new TaskCompletionSource<bool>(
                        TaskCreationOptions.RunContinuationsAsynchronously);
                return _closeAcknowledgement.Task;
            }
            finally
            {
                Clear(plaintext);
                Clear(datagram);
            }
        }

        /// <summary>
        /// 在 hard cap 内取得 datagram queue ownership 并唤醒 send pump。
        /// </summary>
        /// <param name="datagram">Ownership 成功时转移给 queue。</param>
        /// <returns>Queue 尚未达到 256 items 时为 true。</returns>
        private bool TryQueueDatagramLocked(byte[] datagram)
        {
            if (datagram == null ||
                _sendQueue.Count >=
                ClientBattlePolicy.Current.MaximumQueueItems)
            {
                return false;
            }

            _sendQueue.Enqueue(datagram);
            _sendSignal.Release();
            return true;
        }

        /// <summary>
        /// 原子标记 current terminal、取消 I/O 并只发布一次低敏 callback。
        /// </summary>
        /// <param name="generation">发生失败的 frozen generation。</param>
        /// <param name="failure">Stable non-None reason。</param>
        private void FailCurrent(
            long generation,
            ClientBattleFailure failure)
        {
            CancellationTokenSource cancellation = null;
            IClientBattleDatagramSocket socket = null;
            lock (_gate)
            {
                if (_generation != generation ||
                    _terminalPublished ||
                    failure == ClientBattleFailure.None)
                {
                    return;
                }

                _terminalPublished = true;
                _failure = failure;
                _state = ClientBattleConnectionState.Closing;
                cancellation = _sessionCancellation;
                socket = _socket;
            }

            try
            {
                _inbound.PublishTerminal(generation, failure);
            }
            finally
            {
                cancellation?.Cancel();
                socket?.Dispose();
                _sendSignal.Release();
            }
        }

        /// <summary>
        /// 对 activation 失败执行 generation-fenced 单次 terminal publish。
        /// </summary>
        /// <param name="generation">Failed generation。</param>
        /// <param name="failure">Stable failure。</param>
        private void PublishTerminalOnce(
            long generation,
            ClientBattleFailure failure)
        {
            lock (_gate)
            {
                if (_generation != generation || _terminalPublished)
                {
                    return;
                }

                _terminalPublished = true;
            }

            _inbound.PublishTerminal(generation, failure);
        }

        /// <summary>
        /// 在持锁状态创建 low-sensitive immutable snapshot。
        /// </summary>
        /// <returns>Current connection snapshot。</returns>
        private ClientBattleConnectionSnapshot SnapshotLocked()
        {
            return new ClientBattleConnectionSnapshot(
                _state,
                _failure,
                _generation,
                _targetKind,
                _role,
                _actorSlot,
                _secure?.KeyEpoch ?? 0,
                _secure?.EndpointGeneration ?? 0,
                _latestServerTick,
                _sendQueue.Count,
                _inbound.PendingItems);
        }

        /// <summary>
        /// 等待 pump 完成并吸收其已由 terminal callback 表达的内部异常。
        /// </summary>
        /// <param name="pump">可选 generation pump。</param>
        /// <returns>Pump 已结束时完成。</returns>
        private static async Task ObservePumpAsync(Task pump)
        {
            if (pump == null)
            {
                return;
            }

            try
            {
                await pump;
            }
            catch (OperationCanceledException)
            {
            }
            catch (ObjectDisposedException)
            {
            }
        }

        /// <summary>
        /// 等待 task 或 cancellation signal。
        /// </summary>
        /// <param name="task">待等待 operation。</param>
        /// <param name="cancellationToken">Deadline/caller cancellation。</param>
        /// <returns>Operation 完成时结束。</returns>
        private static async Task WaitWithCancellationAsync(
            Task task,
            CancellationToken cancellationToken)
        {
            if (task.IsCompleted)
            {
                await task;
                return;
            }

            var cancelled = new TaskCompletionSource<bool>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            using (cancellationToken.Register(
                       () => cancelled.TrySetResult(true)))
            {
                if (!ReferenceEquals(
                        await Task.WhenAny(task, cancelled.Task),
                        task))
                {
                    cancellationToken.ThrowIfCancellationRequested();
                }

                await task;
            }
        }

        /// <summary>
        /// 复制 reusable native buffer 的 exact prefix。
        /// </summary>
        /// <param name="source">Reusable source buffer。</param>
        /// <param name="length">Validated positive prefix width。</param>
        /// <returns>Caller-owned exact buffer。</returns>
        private static byte[] Copy(byte[] source, int length)
        {
            var output = new byte[length];
            Buffer.BlockCopy(source, 0, output, 0, length);
            return output;
        }

        /// <summary>
        /// 复制 source 的 exact bounded slice。
        /// </summary>
        /// <param name="source">Caller-owned source。</param>
        /// <param name="offset">Zero-based start offset。</param>
        /// <param name="length">Positive slice width。</param>
        /// <returns>Caller-owned exact buffer。</returns>
        private static byte[] Copy(
            byte[] source,
            int offset,
            int length)
        {
            var output = new byte[length];
            Buffer.BlockCopy(
                source,
                offset,
                output,
                0,
                length);
            return output;
        }

        /// <summary>
        /// 读取 canonical big-endian UInt32。
        /// </summary>
        /// <param name="buffer">Validated source。</param>
        /// <param name="offset">四字节起点。</param>
        /// <returns>Decoded value。</returns>
        private static uint ReadUInt32BigEndian(
            byte[] buffer,
            int offset)
        {
            return
                ((uint)buffer[offset] << 24) |
                ((uint)buffer[offset + 1] << 16) |
                ((uint)buffer[offset + 2] << 8) |
                buffer[offset + 3];
        }

        /// <summary>
        /// 读取 canonical big-endian UInt64。
        /// </summary>
        /// <param name="buffer">Validated source。</param>
        /// <param name="offset">八字节起点。</param>
        /// <returns>Decoded value。</returns>
        private static ulong ReadUInt64BigEndian(
            byte[] buffer,
            int offset)
        {
            var high = ReadUInt32BigEndian(buffer, offset);
            var low = ReadUInt32BigEndian(buffer, offset + 4);
            return ((ulong)high << 32) | low;
        }

        /// <summary>
        /// 判断 source 的 bounded slice 是否全部为零。
        /// </summary>
        /// <param name="source">Validated source。</param>
        /// <param name="offset">Slice 起点。</param>
        /// <param name="length">Slice 宽度。</param>
        /// <returns>全部为零时为 true。</returns>
        private static bool IsAllZero(
            byte[] source,
            int offset,
            int length)
        {
            var aggregate = 0;
            for (var index = 0; index < length; index++)
            {
                aggregate |= source[offset + index];
            }

            return aggregate == 0;
        }

        /// <summary>
        /// 清零临时 payload/datagram buffer；空值安全。
        /// </summary>
        /// <param name="value">待清零 bytes。</param>
        private static void Clear(byte[] value)
        {
            if (value != null)
            {
                Array.Clear(value, 0, value.Length);
            }
        }

        /// <summary>
        /// 通过 native secure-zero 清零并置空 secret field。
        /// </summary>
        /// <param name="value">待释放 secret field。</param>
        private static void Clear(ref byte[] value)
        {
            var current = value;
            value = null;
            if (current != null)
            {
                ClientBattleNativeCrypto.SecureZero(current);
            }
        }
    }
}
