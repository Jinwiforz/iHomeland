using System;
using System.Net;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.AppShell.Application.Configuration;
using IHomeland.Client.Networking.Application.Contracts;
using IHomeland.Client.Networking.Application.Control;
using IHomeland.Client.Session.Application;

namespace IHomeland.Client.Networking.Infrastructure.WebSocket
{
    /// <summary>由 Control lifecycle owner 提供的单次 attempt 提交边界。</summary>
    internal interface IControlConnectionAttemptHost
    {
        /// <summary>在 run generation 仍 current 时登记独占 socket 与取消所有权。</summary>
        bool TryClaimAttempt(
            long runGeneration,
            IClientWebSocket socket,
            CancellationTokenSource attemptCancellation);

        /// <summary>只释放仍属于当前 attempt 的 socket 注册。</summary>
        void ReleaseAttempt(IClientWebSocket socket);

        /// <summary>把已协商连接提交为 Connected。</summary>
        void MarkAttemptConnected(long runGeneration, int attempt);

        /// <summary>有界投递一个已验证 typed PUSH。</summary>
        bool TryPostAttemptPush(
            long connectionGeneration,
            ClientControlNotification notification);
    }

    /// <summary>
    /// 拥有 Control ticket 单次 take、URI policy、socket handshake 与 attempt 资源释放。
    /// </summary>
    internal sealed class ControlConnectionAttempt
    {
        /// <summary>限制单次 WebSocket upgrade 等待。</summary>
        private static readonly TimeSpan ConnectTimeout = TimeSpan.FromSeconds(10);

        /// <summary>提供明文 loopback 与 Production 安全策略。</summary>
        private readonly ClientEnvironment _environment;

        /// <summary>提供冻结 realtime frame cap。</summary>
        private readonly ClientConfigurationStore _configurationStore;

        /// <summary>签发并单次交付 current Session ticket。</summary>
        private readonly SessionCoordinator _sessionCoordinator;

        /// <summary>为每次 attempt 创建独立 socket。</summary>
        private readonly IClientWebSocketFactory _socketFactory;

        /// <summary>拥有 fragment、sequence 与 push 分类。</summary>
        private readonly ControlReceivePump _receivePump;

        /// <summary>同步撤销相同来源 Session generation 的 gameplay。</summary>
        private readonly Action<long> _gameplaySessionInvalidation;

        /// <summary>创建单次 Control connection flow。</summary>
        internal ControlConnectionAttempt(
            ClientEnvironment environment,
            ClientConfigurationStore configurationStore,
            SessionCoordinator sessionCoordinator,
            IClientWebSocketFactory socketFactory,
            ControlReceivePump receivePump,
            Action<long> gameplaySessionInvalidation)
        {
            _environment = environment ?? throw new ArgumentNullException(nameof(environment));
            _configurationStore = configurationStore ??
                throw new ArgumentNullException(nameof(configurationStore));
            _sessionCoordinator = sessionCoordinator ??
                throw new ArgumentNullException(nameof(sessionCoordinator));
            _socketFactory = socketFactory ??
                throw new ArgumentNullException(nameof(socketFactory));
            _receivePump = receivePump ?? throw new ArgumentNullException(nameof(receivePump));
            _gameplaySessionInvalidation = gameplaySessionInvalidation;
        }

        /// <summary>运行一笔 ticket、connect、receive 与资源释放完整 attempt。</summary>
        internal async Task<ControlAttemptOutcomeKind> RunAsync(
            IControlConnectionAttemptHost host,
            long runGeneration,
            int attempt,
            long connectionGeneration,
            CancellationToken cancellationToken)
        {
            if (host == null)
            {
                throw new ArgumentNullException(nameof(host));
            }

            if (!_configurationStore.TryGetCurrent(out var configuration) ||
                configuration.Configuration.Limits.RealtimeFrameBytes <= 0)
            {
                return ControlAttemptOutcomeKind.PolicyRejected;
            }

            var ticketResult = await _sessionCoordinator.IssueConnectionTicketAsync(
                ClientEndpointChannel.Wss,
                cancellationToken);
            if (!ticketResult.IsSuccess ||
                !_sessionCoordinator.TryTakeConnectionTicket(ticketResult.Value, out var ticketUse) ||
                !IsControlTicket(ticketUse))
            {
                return ControlAttemptOutcomeKind.PolicyRejected;
            }

            ClientWebSocketConnectRequest request;
            try
            {
                request = BuildConnectRequest(ticketUse);
            }
            catch (ArgumentException)
            {
                return ControlAttemptOutcomeKind.PolicyRejected;
            }

            var socket = _socketFactory.Create();
            if (socket == null)
            {
                return ControlAttemptOutcomeKind.PolicyRejected;
            }

            using (var attemptCancellation =
                   CancellationTokenSource.CreateLinkedTokenSource(cancellationToken))
            {
                if (!host.TryClaimAttempt(runGeneration, socket, attemptCancellation))
                {
                    socket.Dispose();
                    return ControlAttemptOutcomeKind.Requested;
                }

                try
                {
                    using (var connectCancellation =
                           CancellationTokenSource.CreateLinkedTokenSource(
                               attemptCancellation.Token))
                    {
                        connectCancellation.CancelAfter(ConnectTimeout);
                        try
                        {
                            await socket.ConnectAsync(request, connectCancellation.Token);
                        }
                        catch (OperationCanceledException)
                            when (!cancellationToken.IsCancellationRequested)
                        {
                            return ControlAttemptOutcomeKind.TransportFailure;
                        }
                    }

                    if (!string.Equals(
                            socket.SubProtocol,
                            ClientWebSocketConnectRequest.ControlSubprotocol,
                            StringComparison.Ordinal))
                    {
                        return ControlAttemptOutcomeKind.ProtocolFailure;
                    }

                    host.MarkAttemptConnected(runGeneration, attempt);
                    return await _receivePump.RunAsync(
                        socket,
                        connectionGeneration,
                        ticketUse.SourceGeneration,
                        configuration.Configuration.Limits.RealtimeFrameBytes,
                        _sessionCoordinator.TryInvalidateFromControlAsync,
                        _gameplaySessionInvalidation,
                        host.TryPostAttemptPush,
                        attemptCancellation.Token);
                }
                catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
                {
                    return ControlAttemptOutcomeKind.Requested;
                }
                catch (OperationCanceledException) when (attemptCancellation.IsCancellationRequested)
                {
                    return ControlAttemptOutcomeKind.TransportFailure;
                }
                catch (ClientWebSocketTransportException)
                {
                    return ControlAttemptOutcomeKind.TransportFailure;
                }
                finally
                {
                    host.ReleaseAttempt(socket);
                    socket.Dispose();
                }
            }
        }

        /// <summary>从 ticket endpoint 与环境策略构造冻结连接请求。</summary>
        private ClientWebSocketConnectRequest BuildConnectRequest(
            ClientConnectionTicketUse ticketUse)
        {
            var secure = string.Equals(
                _environment.HttpBaseUri.Scheme,
                Uri.UriSchemeHttps,
                StringComparison.OrdinalIgnoreCase);
            var endpoint = new UriBuilder(
                secure ? "wss" : "ws",
                ticketUse.Endpoint.Host,
                ticketUse.Endpoint.Port,
                ClientWebSocketConnectRequest.ControlPath).Uri;
            if (!secure &&
                (_environment.EnvironmentKind == ClientEnvironmentKind.Production ||
                 !IsLoopback(endpoint.Host)))
            {
                throw new ArgumentException("明文 WSS endpoint 只允许 Local/Test loopback。");
            }

            return new ClientWebSocketConnectRequest(endpoint, ticketUse.Credential);
        }

        /// <summary>验证 ticket 只允许 WSS 与 CONTROL scope。</summary>
        private static bool IsControlTicket(ClientConnectionTicketUse ticketUse)
        {
            return ticketUse.Endpoint.Channel == ClientEndpointChannel.Wss &&
                   ticketUse.Scopes.Count == 1 &&
                   ticketUse.Scopes[0] == ClientConnectionScope.Control;
        }

        /// <summary>判断 host 是否为 loopback IP 或 localhost。</summary>
        private static bool IsLoopback(string host)
        {
            if (string.Equals(host, "localhost", StringComparison.OrdinalIgnoreCase))
            {
                return true;
            }

            return IPAddress.TryParse(host, out var address) &&
                   IPAddress.IsLoopback(address);
        }
    }
}
