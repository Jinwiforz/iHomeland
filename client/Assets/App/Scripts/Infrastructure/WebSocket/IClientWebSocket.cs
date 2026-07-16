using System;
using System.Net.WebSockets;
using System.Threading;
using System.Threading.Tasks;

namespace IHomeland.Client.Infrastructure.WebSocket
{
    /// <summary>
    /// 为只接收 control PUSH 提供可测试的最小 WebSocket 边界。
    /// </summary>
    /// <remarks>
    /// 该接口刻意不提供 application `SendAsync`；客户端在 WSS control 上发送 text/binary frame
    /// 属于协议错误。平台 heartbeat control frame 不经过该业务 API。
    /// </remarks>
    internal interface IClientWebSocket : IDisposable
    {
        /// <summary>
        /// 获取平台 socket 当前状态。
        /// </summary>
        WebSocketState State { get; }

        /// <summary>
        /// 获取 upgrade 后实际协商的 subprotocol。
        /// </summary>
        string SubProtocol { get; }

        /// <summary>
        /// 使用已冻结 request 建立单次 WebSocket connection。
        /// </summary>
        /// <param name="request">包含 endpoint 与单次 ticket 的安全握手请求。</param>
        /// <param name="cancellationToken">App Scope 或显式运行取消信号。</param>
        /// <returns>Upgrade 完成时结束的任务。</returns>
        Task ConnectAsync(ClientWebSocketConnectRequest request, CancellationToken cancellationToken);

        /// <summary>
        /// 把下一段 WebSocket message bytes 写入调用方有界 buffer。
        /// </summary>
        /// <param name="buffer">Receive pump 拥有且剩余容量非空的 buffer segment。</param>
        /// <param name="cancellationToken">App Scope 或显式运行取消信号。</param>
        /// <returns>不保留 peer description 的 receive 结果。</returns>
        Task<ClientWebSocketReadResult> ReceiveAsync(
            ArraySegment<byte> buffer,
            CancellationToken cancellationToken);

        /// <summary>
        /// 在 deadline 内尝试发送协议关闭，不等待或发送 application payload。
        /// </summary>
        /// <param name="cancellationToken">有界 close deadline。</param>
        /// <returns>Close output 已完成或 socket 已无需关闭时结束的任务。</returns>
        Task CloseAsync(CancellationToken cancellationToken);
    }

    /// <summary>
    /// 创建彼此独立且只能用于一次 connection attempt 的 WebSocket adapter。
    /// </summary>
    internal interface IClientWebSocketFactory
    {
        /// <summary>
        /// 创建尚未连接且没有保存旧 ticket 的 socket。
        /// </summary>
        /// <returns>单次 connection attempt 独占的 adapter。</returns>
        IClientWebSocket Create();
    }
}
