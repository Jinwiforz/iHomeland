using System;
using System.Diagnostics;
using System.Net.Sockets;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.PersonalWorldCombat.Application;
using IHomeland.Client.Networking.Application.Contracts;

namespace IHomeland.Client.PersonalWorldCombat.Infrastructure
{
    /// <summary>
    /// 提供 attempt 内由 monotonic elapsed 推进、但与 ticket Unix expiry 同域的时钟。
    /// </summary>
    internal sealed class ClientBattleAttemptClock
    {
        /// <summary>保存构造时的 Unix 毫秒。</summary>
        private readonly long _startedUnixMilliseconds =
            DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();

        /// <summary>保存构造时的 monotonic timestamp。</summary>
        private readonly long _startedTimestamp = Stopwatch.GetTimestamp();

        /// <summary>
        /// 获取不受 wall-clock 回拨影响的 current Unix-domain 毫秒。
        /// </summary>
        internal long NowMilliseconds
        {
            get
            {
                var elapsed = Stopwatch.GetTimestamp() - _startedTimestamp;
                return checked(
                    _startedUnixMilliseconds +
                    (long)(
                        elapsed * 1000.0 /
                        Stopwatch.Frequency));
            }
        }
    }

    /// <summary>
    /// 保存成功 attempt 的唯一 UDP、secure 与 KCP resources。
    /// </summary>
    internal sealed class ClientBattleEstablishedConnection : IDisposable
    {
        /// <summary>保存 connected UDP socket。</summary>
        private IClientBattleDatagramSocket _socket;

        /// <summary>保存 secure traffic owner。</summary>
        private ClientBattleSecureChannel _secure;

        /// <summary>保存 generation-scoped native KCP lease。</summary>
        private ClientBattleKcpLease _kcp;

        /// <summary>
        /// 创建全部底层资源已经建立的 connection。
        /// </summary>
        /// <param name="socket">Connected UDP socket。</param>
        /// <param name="secure">Authenticated secure channel。</param>
        /// <param name="kcp">Session-derived KCP context。</param>
        /// <param name="role">ServerAccept role。</param>
        /// <param name="actorSlot">0..7 actor slot。</param>
        /// <param name="targetRevision">Ticket frozen target revision。</param>
        internal ClientBattleEstablishedConnection(
            IClientBattleDatagramSocket socket,
            ClientBattleSecureChannel secure,
            ClientBattleKcpLease kcp,
            ClientBattleRole role,
            byte actorSlot,
            ulong targetRevision)
        {
            _socket = socket ?? throw new ArgumentNullException(nameof(socket));
            _secure = secure ?? throw new ArgumentNullException(nameof(secure));
            _kcp = kcp ?? throw new ArgumentNullException(nameof(kcp));
            Role = role;
            ActorSlot = actorSlot;
            TargetRevision = targetRevision;
        }

        /// <summary>获取 authenticated role。</summary>
        internal ClientBattleRole Role { get; }

        /// <summary>获取 authenticated actor slot。</summary>
        internal byte ActorSlot { get; }

        /// <summary>获取签发时冻结的 SimulationTarget revision。</summary>
        internal ulong TargetRevision { get; }

        /// <summary>
        /// 一次性转移 UDP socket 给 BattleNetworkClient。
        /// </summary>
        /// <returns>唯一 connected socket owner。</returns>
        internal IClientBattleDatagramSocket TakeSocket()
        {
            return System.Threading.Interlocked.Exchange(
                       ref _socket,
                       null) ??
                   throw new ObjectDisposedException(
                       nameof(ClientBattleEstablishedConnection));
        }

        /// <summary>
        /// 一次性转移 secure channel 给 BattleNetworkClient。
        /// </summary>
        /// <returns>唯一 secure channel owner。</returns>
        internal ClientBattleSecureChannel TakeSecure()
        {
            return System.Threading.Interlocked.Exchange(
                       ref _secure,
                       null) ??
                   throw new ObjectDisposedException(
                       nameof(ClientBattleEstablishedConnection));
        }

        /// <summary>
        /// 一次性转移 KCP lease 给 BattleNetworkClient。
        /// </summary>
        /// <returns>唯一 generation-scoped KCP owner。</returns>
        internal ClientBattleKcpLease TakeKcp()
        {
            return System.Threading.Interlocked.Exchange(
                       ref _kcp,
                       null) ??
                   throw new ObjectDisposedException(
                       nameof(ClientBattleEstablishedConnection));
        }

        /// <summary>
        /// 按 KCP、secure、socket 顺序释放尚未转移的资源。
        /// </summary>
        public void Dispose()
        {
            var kcp = System.Threading.Interlocked.Exchange(
                ref _kcp,
                null);
            var secure = System.Threading.Interlocked.Exchange(
                ref _secure,
                null);
            var socket = System.Threading.Interlocked.Exchange(
                ref _socket,
                null);
            kcp?.Dispose();
            secure?.Dispose();
            socket?.Dispose();
        }
    }

    /// <summary>
    /// 保存 connect attempt 的成功 connection 或稳定低敏失败。
    /// </summary>
    internal sealed class ClientBattleConnectResult
    {
        /// <summary>
        /// 创建封闭成功/失败结果。
        /// </summary>
        /// <param name="connection">成功 connection。</param>
        /// <param name="failure">失败原因。</param>
        private ClientBattleConnectResult(
            ClientBattleEstablishedConnection connection,
            ClientBattleFailure failure)
        {
            Connection = connection;
            Failure = failure;
        }

        /// <summary>获取结果是否成功。</summary>
        internal bool IsSuccess
        {
            get
            {
                return Connection != null;
            }
        }

        /// <summary>获取成功 connection。</summary>
        internal ClientBattleEstablishedConnection Connection { get; }

        /// <summary>获取稳定失败原因。</summary>
        internal ClientBattleFailure Failure { get; }

        /// <summary>
        /// 创建成功结果并转移 connection ownership。
        /// </summary>
        /// <param name="connection">Established connection。</param>
        /// <returns>成功结果。</returns>
        internal static ClientBattleConnectResult Success(
            ClientBattleEstablishedConnection connection)
        {
            return new ClientBattleConnectResult(
                connection ??
                throw new ArgumentNullException(nameof(connection)),
                ClientBattleFailure.None);
        }

        /// <summary>
        /// 创建不含内部异常或 endpoint 的失败结果。
        /// </summary>
        /// <param name="failure">非 None failure。</param>
        /// <returns>失败结果。</returns>
        internal static ClientBattleConnectResult Failed(
            ClientBattleFailure failure)
        {
            if (failure == ClientBattleFailure.None)
            {
                throw new ArgumentOutOfRangeException(nameof(failure));
            }

            return new ClientBattleConnectResult(null, failure);
        }
    }

    /// <summary>
    /// 抽象 generation-scoped BattleTicket/handshake attempt，供 connection owner 注入确定性测试结果。
    /// </summary>
    internal interface IClientBattleConnectAttempt
    {
        /// <summary>
        /// 执行一次封闭 activation attempt。
        /// </summary>
        /// <param name="battleGeneration">Successor battle generation。</param>
        /// <param name="target">Frozen Session/world target。</param>
        /// <param name="cancellationToken">Caller 与 higher lifecycle cancellation。</param>
        /// <returns>Established resources 或稳定失败。</returns>
        Task<ClientBattleConnectResult> ExecuteAsync(
            long battleGeneration,
            ClientBattleTargetIntent target,
            CancellationToken cancellationToken);
    }

    /// <summary>
    /// 以稳定 idempotency identity 合并 ticket issuance、UDP connect 与 handshake。
    /// </summary>
    internal sealed class ClientBattleConnectAttempt :
        IClientBattleConnectAttempt
    {
        /// <summary>保存 fresh Session authorization source。</summary>
        private readonly IClientBattleAuthorizationSource _authorization;

        /// <summary>保存专用 BattleTicket HTTP API。</summary>
        private readonly IClientBattleTicketGateway _tickets;

        /// <summary>保存 production/fake socket factory。</summary>
        private readonly IClientBattleDatagramSocketFactory _sockets;

        /// <summary>保存 App Scope native provider。</summary>
        private readonly ClientBattleNativeProvider _native;

        /// <summary>
        /// 创建独立 attempt factory。
        /// </summary>
        /// <param name="authorization">唯一 Session owner adapter。</param>
        /// <param name="tickets">专用 ticket API。</param>
        /// <param name="sockets">Connected UDP socket factory。</param>
        /// <param name="native">Initialized native provider。</param>
        internal ClientBattleConnectAttempt(
            IClientBattleAuthorizationSource authorization,
            IClientBattleTicketGateway tickets,
            IClientBattleDatagramSocketFactory sockets,
            ClientBattleNativeProvider native)
        {
            _authorization = authorization ??
                throw new ArgumentNullException(nameof(authorization));
            _tickets = tickets ?? throw new ArgumentNullException(nameof(tickets));
            _sockets = sockets ?? throw new ArgumentNullException(nameof(sockets));
            _native = native ?? throw new ArgumentNullException(nameof(native));
        }

        /// <summary>
        /// 执行一次 generation-scoped activation；response loss 最多以相同 key 重试一次。
        /// </summary>
        /// <param name="battleGeneration">Successor battle generation。</param>
        /// <param name="target">Frozen Session/world target intent。</param>
        /// <param name="cancellationToken">Caller 与 higher lifecycle cancellation。</param>
        /// <returns>Established resources 或稳定失败。</returns>
        public async Task<ClientBattleConnectResult> ExecuteAsync(
            long battleGeneration,
            ClientBattleTargetIntent target,
            CancellationToken cancellationToken)
        {
            if (battleGeneration <= 0 || target == null)
            {
                return ClientBattleConnectResult.Failed(
                    ClientBattleFailure.Policy);
            }

            var clock = new ClientBattleAttemptClock();
            var idempotencyKey = CreateIdempotencyKey();
            ClientBattleTicketMaterial ticket = null;
            IClientBattleDatagramSocket socket = null;
            ClientBattleHandshake handshake = null;
            ClientBattleSessionParameters parameters = null;
            ClientBattleSecureChannel secure = null;
            ClientBattleKcpLease kcp = null;
            try
            {
                var ticketResult = await IssueWithBoundedRetryAsync(
                    target,
                    idempotencyKey,
                    cancellationToken);
                if (!ticketResult.IsSuccess)
                {
                    return ClientBattleConnectResult.Failed(
                        ticketResult.Failure);
                }

                ticket = ticketResult.Ticket;
                var targetRevision = ticket.TargetRevision;
                socket = _sockets.Create();
                await socket.ConnectAsync(
                    ticket.Host,
                    ticket.Port,
                    cancellationToken);

                var handshakeStarted = clock.NowMilliseconds;
                var handshakeDeadline = checked(
                    handshakeStarted +
                    ClientBattleHandshake.HandshakeTimeoutMilliseconds);
                handshake = new ClientBattleHandshake(
                    ticket,
                    target,
                    handshakeStarted);
                ticket = null;
                var hello = handshake.CreateClientHello();
                try
                {
                    await SendExactAsync(
                        socket,
                        hello,
                        cancellationToken);
                }
                finally
                {
                    Array.Clear(hello, 0, hello.Length);
                }

                var auth = await ReceiveAuthenticatedCandidateAsync(
                    socket,
                    handshakeDeadline,
                    ClientBattleHandshake.MatchesRetryEnvelope,
                    candidate =>
                        handshake.TryAcceptRetry(
                            candidate,
                            clock.NowMilliseconds,
                            out var accepted)
                            ? accepted
                            : null,
                    clock,
                    cancellationToken);

                try
                {
                    await SendExactAsync(
                        socket,
                        auth,
                        cancellationToken);
                }
                finally
                {
                    Array.Clear(auth, 0, auth.Length);
                }

                parameters = await ReceiveAuthenticatedCandidateAsync(
                    socket,
                    handshakeDeadline,
                    ClientBattleHandshake.MatchesServerAcceptEnvelope,
                    candidate =>
                        handshake.TryAcceptServer(
                            candidate,
                            clock.NowMilliseconds,
                            out var accepted)
                            ? accepted
                            : null,
                    clock,
                    cancellationToken);

                var conversation = parameters.DeriveKcpConversation();
                var role = parameters.Role;
                var actorSlot = parameters.ActorSlot;
                secure = new ClientBattleSecureChannel(
                    parameters,
                    clock.NowMilliseconds);
                kcp = _native.CreateKcp(
                    checked((ulong)battleGeneration),
                    conversation);
                var established =
                    new ClientBattleEstablishedConnection(
                        socket,
                        secure,
                        kcp,
                        role,
                        actorSlot,
                        targetRevision);
                socket = null;
                secure = null;
                kcp = null;
                return ClientBattleConnectResult.Success(established);
            }
            catch (OperationCanceledException)
            {
                return ClientBattleConnectResult.Failed(
                    cancellationToken.IsCancellationRequested
                        ? ClientBattleFailure.CallerCancelled
                        : ClientBattleFailure.Timeout);
            }
            catch (SocketException)
            {
                return ClientBattleConnectResult.Failed(
                    ClientBattleFailure.Transport);
            }
            catch (ClientBattleNativeException)
            {
                return ClientBattleConnectResult.Failed(
                    ClientBattleFailure.Security);
            }
            catch (ArgumentException)
            {
                return ClientBattleConnectResult.Failed(
                    ClientBattleFailure.Protocol);
            }
            catch (InvalidOperationException)
            {
                return ClientBattleConnectResult.Failed(
                    ClientBattleFailure.Protocol);
            }
            finally
            {
                ticket?.Dispose();
                handshake?.Dispose();
                parameters?.Dispose();
                kcp?.Dispose();
                secure?.Dispose();
                socket?.Dispose();
            }
        }

        /// <summary>
        /// 使用同一 idempotency key 签发 ticket，Transport/Timeout 最多重试一次。
        /// </summary>
        /// <param name="target">Frozen target intent。</param>
        /// <param name="idempotencyKey">Attempt-stable safe ASCII identity。</param>
        /// <param name="cancellationToken">Attempt cancellation。</param>
        /// <returns>Ticket 或稳定 failure。</returns>
        private async Task<TicketAttemptResult> IssueWithBoundedRetryAsync(
            ClientBattleTargetIntent target,
            string idempotencyKey,
            CancellationToken cancellationToken)
        {
            for (var attempt = 0; attempt < 2; attempt++)
            {
                var authorization =
                    await _authorization.AcquireBattleAuthorizationAsync(
                        target.SessionGeneration,
                        cancellationToken);
                if (!authorization.IsSuccess)
                {
                    return TicketAttemptResult.Failed(
                        MapGatewayFailure(authorization));
                }

                var result = await _tickets.IssueAsync(
                    new ClientBattleTicketGatewayRequest(
                        authorization.Value,
                        target,
                        idempotencyKey),
                    cancellationToken);
                if (result.IsSuccess)
                {
                    return TicketAttemptResult.Success(result.Value);
                }

                var retryableLoss =
                    result.Failure != null &&
                    (result.Failure.Kind ==
                     ClientGatewayFailureKind.Transport ||
                     result.Failure.Kind ==
                     ClientGatewayFailureKind.Timeout);
                if (attempt == 0 && retryableLoss)
                {
                    continue;
                }

                return TicketAttemptResult.Failed(
                    retryableLoss
                        ? ClientBattleFailure.CommitUnknown
                        : MapGatewayFailure(result));
            }

            return TicketAttemptResult.Failed(
                ClientBattleFailure.CommitUnknown);
        }

        /// <summary>
        /// 接收并忽略无法由 current handshake transcript 认证的 UDP candidate。
        /// </summary>
        /// <typeparam name="TAccepted">成功认证后转移给 caller 的资源类型。</typeparam>
        /// <param name="socket">Current connected socket。</param>
        /// <param name="deadlineMilliseconds">Absolute attempt deadline。</param>
        /// <param name="matchesEnvelope">公开 fixed envelope classifier。</param>
        /// <param name="tryAccept">
        /// 只在 candidate 属于 current transcript 时返回资源，否则返回 null。
        /// </param>
        /// <param name="clock">Attempt monotonic-derived clock。</param>
        /// <param name="cancellationToken">Higher cancellation。</param>
        /// <returns>Current transcript 唯一接受的 caller-owned资源。</returns>
        internal static async Task<TAccepted>
            ReceiveAuthenticatedCandidateAsync<TAccepted>(
            IClientBattleDatagramSocket socket,
            long deadlineMilliseconds,
            Func<byte[], bool> matchesEnvelope,
            Func<byte[], TAccepted> tryAccept,
            ClientBattleAttemptClock clock,
            CancellationToken cancellationToken)
            where TAccepted : class
        {
            if (socket == null ||
                matchesEnvelope == null ||
                tryAccept == null ||
                clock == null)
            {
                throw new ArgumentNullException(
                    "client battle handshake receiver dependency is missing");
            }

            for (;;)
            {
                cancellationToken.ThrowIfCancellationRequested();
                var remaining =
                    deadlineMilliseconds - clock.NowMilliseconds;
                if (remaining <= 0)
                {
                    throw new OperationCanceledException(
                        "client battle handshake timed out");
                }

                using (var deadline = CancellationTokenSource.
                           CreateLinkedTokenSource(cancellationToken))
                {
                    deadline.CancelAfter(
                        TimeSpan.FromMilliseconds(remaining));
                    byte[] datagram;
                    try
                    {
                        datagram = await socket.ReceiveAsync(
                            ClientBattleSecureChannel.MaximumDatagramBytes,
                            deadline.Token);
                    }
                    catch (OperationCanceledException)
                        when (!cancellationToken.IsCancellationRequested)
                    {
                        throw new OperationCanceledException(
                            "client battle handshake timed out");
                    }

                    try
                    {
                        if (matchesEnvelope(datagram))
                        {
                            var accepted = tryAccept(datagram);
                            if (accepted != null)
                            {
                                return accepted;
                            }
                        }
                    }
                    finally
                    {
                        Array.Clear(datagram, 0, datagram.Length);
                    }
                }
            }
        }

        /// <summary>
        /// 发送完整 handshake datagram，并拒绝 partial send。
        /// </summary>
        /// <param name="socket">Current connected socket。</param>
        /// <param name="datagram">Fixed handshake datagram。</param>
        /// <param name="cancellationToken">Attempt cancellation。</param>
        /// <returns>完整发送时完成。</returns>
        private static async Task SendExactAsync(
            IClientBattleDatagramSocket socket,
            byte[] datagram,
            CancellationToken cancellationToken)
        {
            if (await socket.SendAsync(
                    datagram,
                    cancellationToken) != datagram.Length)
            {
                throw new SocketException(
                    (int)SocketError.MessageSize);
            }
        }

        /// <summary>
        /// 生成 256-bit entropy 的 safe ASCII idempotency identity。
        /// </summary>
        /// <returns>43-char base64url key。</returns>
        private static string CreateIdempotencyKey()
        {
            var bytes = new byte[32];
            try
            {
                ClientBattleNativeCrypto.RandomFill(bytes);
                return Convert.ToBase64String(bytes)
                    .TrimEnd('=')
                    .Replace('+', '-')
                    .Replace('/', '_');
            }
            finally
            {
                ClientBattleNativeCrypto.SecureZero(bytes);
            }
        }

        /// <summary>
        /// 把 gateway result 映射为不暴露 server details 的 battle failure。
        /// </summary>
        /// <typeparam name="T">Gateway success value 类型。</typeparam>
        /// <param name="result">非成功 gateway result。</param>
        /// <returns>稳定 battle failure。</returns>
        private static ClientBattleFailure MapGatewayFailure<T>(
            ClientGatewayResult<T> result)
        {
            if (result == null)
            {
                return ClientBattleFailure.Protocol;
            }

            if (result.ServerError != null)
            {
                switch (result.ServerError.Code)
                {
                    case 100:
                        return ClientBattleFailure.SessionInvalidated;
                    case 3002:
                        return ClientBattleFailure.TargetReplaced;
                    default:
                        return result.ServerError.Retryable
                            ? ClientBattleFailure.Transport
                            : ClientBattleFailure.Policy;
                }
            }

            if (result.Failure == null)
            {
                return ClientBattleFailure.Protocol;
            }

            switch (result.Failure.Kind)
            {
                case ClientGatewayFailureKind.CallerCancelled:
                    return ClientBattleFailure.CallerCancelled;
                case ClientGatewayFailureKind.Timeout:
                case ClientGatewayFailureKind.Transport:
                    return ClientBattleFailure.Transport;
                case ClientGatewayFailureKind.Stopped:
                    return ClientBattleFailure.Shutdown;
                case ClientGatewayFailureKind.ResponseTooLarge:
                case ClientGatewayFailureKind.MalformedResponse:
                    return ClientBattleFailure.Protocol;
                default:
                    return ClientBattleFailure.Policy;
            }
        }

        /// <summary>
        /// 保存 ticket issuance 的内部成功/failure 二选一结果。
        /// </summary>
        private sealed class TicketAttemptResult
        {
            /// <summary>
            /// 创建内部结果。
            /// </summary>
            /// <param name="ticket">成功 ticket。</param>
            /// <param name="failure">失败原因。</param>
            private TicketAttemptResult(
                ClientBattleTicketMaterial ticket,
                ClientBattleFailure failure)
            {
                Ticket = ticket;
                Failure = failure;
            }

            /// <summary>获取结果是否成功。</summary>
            internal bool IsSuccess
            {
                get
                {
                    return Ticket != null;
                }
            }

            /// <summary>获取成功 ticket。</summary>
            internal ClientBattleTicketMaterial Ticket { get; }

            /// <summary>获取失败原因。</summary>
            internal ClientBattleFailure Failure { get; }

            /// <summary>
            /// 创建成功 ticket result。
            /// </summary>
            /// <param name="ticket">Ticket ownership。</param>
            /// <returns>成功结果。</returns>
            internal static TicketAttemptResult Success(
                ClientBattleTicketMaterial ticket)
            {
                return new TicketAttemptResult(
                    ticket ??
                    throw new ArgumentNullException(nameof(ticket)),
                    ClientBattleFailure.None);
            }

            /// <summary>
            /// 创建失败 result。
            /// </summary>
            /// <param name="failure">Non-None failure。</param>
            /// <returns>失败结果。</returns>
            internal static TicketAttemptResult Failed(
                ClientBattleFailure failure)
            {
                return new TicketAttemptResult(null, failure);
            }
        }
    }
}
