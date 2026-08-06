using System;
using System.Net.WebSockets;
using System.Threading;
using System.Threading.Tasks;

namespace IHomeland.Client.Networking.Infrastructure.WebSocket
{
    /// <summary>
    /// 保存单次 WSS upgrade 所需且默认脱敏的冻结请求。
    /// </summary>
    internal sealed class ClientWebSocketConnectRequest
    {
        /// <summary>
        /// 创建严格的 control connection request。
        /// </summary>
        /// <param name="endpoint">只含 scheme/host/port 与冻结 path 的绝对 URI。</param>
        /// <param name="ticketCredential">32 位小写十六进制一次性 ticket。</param>
        /// <exception cref="ArgumentException">URI 或 ticket 不满足冻结握手语法时抛出。</exception>
        /// <exception cref="ArgumentNullException">URI 或 ticket 为空引用时抛出。</exception>
        internal ClientWebSocketConnectRequest(Uri endpoint, string ticketCredential)
        {
            Endpoint = endpoint ?? throw new ArgumentNullException(nameof(endpoint));
            if (!Endpoint.IsAbsoluteUri ||
                (Endpoint.Scheme != "ws" && Endpoint.Scheme != "wss") ||
                Endpoint.AbsolutePath != ControlPath ||
                !string.IsNullOrEmpty(Endpoint.UserInfo) ||
                !string.IsNullOrEmpty(Endpoint.Query) ||
                !string.IsNullOrEmpty(Endpoint.Fragment))
            {
                throw new ArgumentException("WSS control endpoint 不满足冻结 URI 契约。", nameof(endpoint));
            }

            if (!IsTicketCredential(ticketCredential))
            {
                throw new ArgumentException("WSS ticket 不满足冻结语法。", nameof(ticketCredential));
            }

            TicketCredential = ticketCredential;
        }

        /// <summary>
        /// 获取冻结 control path。
        /// </summary>
        internal const string ControlPath = "/v1/control";

        /// <summary>
        /// 获取冻结 WSS subprotocol。
        /// </summary>
        internal const string ControlSubprotocol = "ihomeland.control.v1";

        /// <summary>
        /// 获取不得由 payload 或 fallback 改写的 ticket endpoint。
        /// </summary>
        internal Uri Endpoint { get; }

        /// <summary>
        /// 获取只能写入当前 upgrade Authorization header 的 credential。
        /// </summary>
        internal string TicketCredential { get; }

        /// <summary>
        /// 返回不包含 ticket、host 或完整 URI 的安全摘要。
        /// </summary>
        /// <returns>固定 path 与 scheme。</returns>
        public override string ToString()
        {
            return $"ClientWebSocketConnectRequest[REDACTED] scheme={Endpoint.Scheme} path={ControlPath}";
        }

        /// <summary>
        /// 验证 16-byte nonce 的 32 位小写十六进制投影。
        /// </summary>
        /// <param name="credential">不可信 ticket 文本。</param>
        /// <returns>语法精确匹配时返回 true。</returns>
        private static bool IsTicketCredential(string credential)
        {
            if (credential == null || credential.Length != 32)
            {
                return false;
            }

            for (var index = 0; index < credential.Length; index++)
            {
                var character = credential[index];
                if (!((character >= '0' && character <= '9') ||
                      (character >= 'a' && character <= 'f')))
                {
                    return false;
                }
            }

            return true;
        }
    }

    /// <summary>
    /// 表示已移除底层 endpoint、header 与异常文本的 WebSocket transport failure。
    /// </summary>
    internal sealed class ClientWebSocketTransportException : Exception
    {
        /// <summary>
        /// 创建稳定低敏 transport failure。
        /// </summary>
        internal ClientWebSocketTransportException()
            : base("WSS control transport failure。")
        {
        }
    }

    /// <summary>
    /// 创建基于 BCL `ClientWebSocket` 的单次 attempt adapter。
    /// </summary>
    internal sealed class SystemClientWebSocketFactory : IClientWebSocketFactory
    {
        /// <summary>
        /// 创建没有继承任何旧 request/ticket 状态的新 adapter。
        /// </summary>
        /// <returns>单次 connection attempt 独占的 adapter。</returns>
        public IClientWebSocket Create()
        {
            return new SystemClientWebSocket();
        }
    }

    /// <summary>
    /// 将 `System.Net.WebSockets.ClientWebSocket` 收窄为只接收 control PUSH 的 adapter。
    /// </summary>
    internal sealed class SystemClientWebSocket : IClientWebSocket
    {
        /// <summary>
        /// 平台 keepalive 间隔；heartbeat control frame 不暴露给业务层。
        /// </summary>
        private static readonly TimeSpan KeepAliveInterval = TimeSpan.FromSeconds(15);

        /// <summary>
        /// 保存单次 attempt 的平台 socket。
        /// </summary>
        private readonly ClientWebSocket _socket = new ClientWebSocket();

        /// <summary>
        /// 防止 dispose 后继续调用平台对象。
        /// </summary>
        private bool _disposed;

        /// <summary>
        /// 创建 adapter 并设置平台 heartbeat；不产生网络副作用。
        /// </summary>
        internal SystemClientWebSocket()
        {
            _socket.Options.KeepAliveInterval = KeepAliveInterval;
        }

        /// <summary>
        /// 获取平台 socket 当前状态。
        /// </summary>
        public WebSocketState State => _socket.State;

        /// <summary>
        /// 获取 upgrade 后实际协商的 subprotocol。
        /// </summary>
        public string SubProtocol => _socket.SubProtocol;

        /// <summary>
        /// 以固定 subprotocol 与 Ticket Authorization 执行一次 upgrade。
        /// </summary>
        /// <param name="request">已严格验证且不得复用的握手请求。</param>
        /// <param name="cancellationToken">连接 deadline 或 App Scope 取消信号。</param>
        /// <returns>Upgrade 完成时结束的任务。</returns>
        /// <exception cref="ClientWebSocketTransportException">平台连接失败时抛出稳定低敏错误。</exception>
        public async Task ConnectAsync(
            ClientWebSocketConnectRequest request,
            CancellationToken cancellationToken)
        {
            if (request == null)
            {
                throw new ArgumentNullException(nameof(request));
            }

            ThrowIfDisposed();
            _socket.Options.AddSubProtocol(ClientWebSocketConnectRequest.ControlSubprotocol);
            _socket.Options.SetRequestHeader("Authorization", $"Ticket {request.TicketCredential}");
            try
            {
                await _socket.ConnectAsync(request.Endpoint, cancellationToken);
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
            {
                throw;
            }
            catch (Exception)
            {
                throw new ClientWebSocketTransportException();
            }
        }

        /// <summary>
        /// 从平台 socket 读取下一段 bytes，并丢弃不可信 close description。
        /// </summary>
        /// <param name="buffer">Receive pump 提供的剩余有界 segment。</param>
        /// <param name="cancellationToken">运行或 App Scope 取消信号。</param>
        /// <returns>平台无关 receive 结果。</returns>
        /// <exception cref="ClientWebSocketTransportException">平台读取失败时抛出稳定低敏错误。</exception>
        public async Task<ClientWebSocketReadResult> ReceiveAsync(
            ArraySegment<byte> buffer,
            CancellationToken cancellationToken)
        {
            ThrowIfDisposed();
            try
            {
                var result = await _socket.ReceiveAsync(buffer, cancellationToken);
                return new ClientWebSocketReadResult(
                    result.Count,
                    result.MessageType,
                    result.EndOfMessage,
                    result.CloseStatus);
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
            {
                throw;
            }
            catch (Exception)
            {
                throw new ClientWebSocketTransportException();
            }
        }

        /// <summary>
        /// 在 socket 状态允许时发送无 description 的 normal close output。
        /// </summary>
        /// <param name="cancellationToken">有界 close deadline。</param>
        /// <returns>Close output 完成或无需发送时结束的任务。</returns>
        public async Task CloseAsync(CancellationToken cancellationToken)
        {
            if (_disposed ||
                (_socket.State != WebSocketState.Open && _socket.State != WebSocketState.CloseReceived))
            {
                return;
            }

            try
            {
                await _socket.CloseOutputAsync(
                    WebSocketCloseStatus.NormalClosure,
                    string.Empty,
                    cancellationToken);
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
            {
                throw;
            }
            catch (Exception)
            {
                throw new ClientWebSocketTransportException();
            }
        }

        /// <summary>
        /// 释放平台 socket；重复调用保持幂等。
        /// </summary>
        public void Dispose()
        {
            if (_disposed)
            {
                return;
            }

            _disposed = true;
            _socket.Dispose();
        }

        /// <summary>
        /// 防止 dispose 后重用已经消费 ticket 的 attempt。
        /// </summary>
        /// <exception cref="ObjectDisposedException">Adapter 已释放时抛出。</exception>
        private void ThrowIfDisposed()
        {
            if (_disposed)
            {
                throw new ObjectDisposedException(nameof(SystemClientWebSocket));
            }
        }
    }
}
