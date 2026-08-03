using System;
using System.Net;
using System.Net.Sockets;
using System.Threading;
using System.Threading.Tasks;

namespace IHomeland.Client.Infrastructure.Battle
{
    /// <summary>
    /// 定义 connect attempt 与唯一 receive pump 可替换的 connected UDP socket。
    /// </summary>
    internal interface IClientBattleDatagramSocket : IDisposable
    {
        /// <summary>
        /// 连接服务端签发的 trusted UDP endpoint。
        /// </summary>
        /// <param name="host">Validated endpoint host。</param>
        /// <param name="port">Validated endpoint port。</param>
        /// <param name="cancellationToken">Attempt deadline/cancellation。</param>
        /// <returns>Connected endpoint 已冻结时完成。</returns>
        Task ConnectAsync(
            string host,
            int port,
            CancellationToken cancellationToken);

        /// <summary>
        /// 发送一个完整 datagram。
        /// </summary>
        /// <param name="datagram">Nonempty caller-owned bytes。</param>
        /// <param name="cancellationToken">Attempt/session cancellation。</param>
        /// <returns>实际发送字节数。</returns>
        Task<int> SendAsync(
            byte[] datagram,
            CancellationToken cancellationToken);

        /// <summary>
        /// 接收一个完整 datagram，并拒绝 IP fragmentation ceiling 以上的数据。
        /// </summary>
        /// <param name="maximumBytes">允许的 datagram hard limit。</param>
        /// <param name="cancellationToken">Attempt/session cancellation。</param>
        /// <returns>Nonempty caller-owned datagram。</returns>
        Task<byte[]> ReceiveAsync(
            int maximumBytes,
            CancellationToken cancellationToken);
    }

    /// <summary>
    /// 创建 production connected UDP socket，测试可注入 fake factory。
    /// </summary>
    internal interface IClientBattleDatagramSocketFactory
    {
        /// <summary>
        /// 创建尚未解析或连接 endpoint 的独占 socket。
        /// </summary>
        /// <returns>由 attempt 或 established connection 释放的 socket。</returns>
        IClientBattleDatagramSocket Create();
    }

    /// <summary>
    /// 创建 Windows/Unity production connected UDP socket。
    /// </summary>
    internal sealed class ClientBattleDatagramSocketFactory :
        IClientBattleDatagramSocketFactory
    {
        /// <summary>
        /// 创建单一尚未连接的 socket owner。
        /// </summary>
        /// <returns>新 production adapter。</returns>
        public IClientBattleDatagramSocket Create()
        {
            return new ClientBattleDatagramSocket();
        }
    }

    /// <summary>
    /// 封装 System.Net.Sockets.Socket，并用 Dispose 终止无法直接取消的 legacy async I/O。
    /// </summary>
    internal sealed class ClientBattleDatagramSocket :
        IClientBattleDatagramSocket
    {
        /// <summary>串行化 connect 与 dispose 状态。</summary>
        private readonly object _gate = new object();

        /// <summary>保存 current underlying UDP socket。</summary>
        private Socket _socket;

        /// <summary>保存不可逆 disposed 状态。</summary>
        private bool _disposed;

        /// <summary>
        /// 解析 trusted host 并只提交一个 connected UDP endpoint。
        /// </summary>
        /// <param name="host">Validated endpoint host。</param>
        /// <param name="port">Validated endpoint port。</param>
        /// <param name="cancellationToken">Attempt cancellation。</param>
        /// <returns>Connect 完成时结束。</returns>
        public async Task ConnectAsync(
            string host,
            int port,
            CancellationToken cancellationToken)
        {
            if (string.IsNullOrWhiteSpace(host) ||
                port < 1 ||
                port > ushort.MaxValue)
            {
                throw new ArgumentException(
                    "client battle endpoint is invalid");
            }

            cancellationToken.ThrowIfCancellationRequested();
            var addresses = await Dns.GetHostAddressesAsync(host);
            cancellationToken.ThrowIfCancellationRequested();
            IPAddress selected = null;
            foreach (var address in addresses)
            {
                if (address.AddressFamily == AddressFamily.InterNetwork ||
                    address.AddressFamily ==
                    AddressFamily.InterNetworkV6)
                {
                    selected = address;
                    break;
                }
            }

            if (selected == null)
            {
                throw new SocketException(
                    (int)SocketError.HostNotFound);
            }

            Socket socket;
            lock (_gate)
            {
                ThrowIfDisposedLocked();
                if (_socket != null)
                {
                    throw new InvalidOperationException(
                        "client battle UDP socket is already connected");
                }

                socket = new Socket(
                    selected.AddressFamily,
                    SocketType.Dgram,
                    ProtocolType.Udp);
                _socket = socket;
            }

            try
            {
                await AwaitCancelableAsync(
                    Task.Factory.FromAsync(
                        socket.BeginConnect,
                        socket.EndConnect,
                        new IPEndPoint(selected, port),
                        null),
                    cancellationToken);
            }
            catch
            {
                Dispose();
                throw;
            }
        }

        /// <summary>
        /// 发送单一完整 datagram。
        /// </summary>
        /// <param name="datagram">Nonempty UDP payload。</param>
        /// <param name="cancellationToken">Session cancellation。</param>
        /// <returns>实际发送字节数。</returns>
        public Task<int> SendAsync(
            byte[] datagram,
            CancellationToken cancellationToken)
        {
            if (datagram == null ||
                datagram.Length == 0 ||
                datagram.Length >
                ClientBattleSecureChannel.MaximumDatagramBytes)
            {
                throw new ArgumentException(
                    "client battle UDP datagram is invalid",
                    nameof(datagram));
            }

            var socket = RequireSocket();
            var operation = Task.Factory.FromAsync(
                (callback, state) => socket.BeginSend(
                    datagram,
                    0,
                    datagram.Length,
                    SocketFlags.None,
                    callback,
                    state),
                socket.EndSend,
                null);
            return AwaitCancelableAsync(operation, cancellationToken);
        }

        /// <summary>
        /// 接收单一 datagram；额外一字节用于检测超过 hard limit 的 payload。
        /// </summary>
        /// <param name="maximumBytes">Positive datagram ceiling。</param>
        /// <param name="cancellationToken">Session cancellation。</param>
        /// <returns>精确长度 caller-owned datagram。</returns>
        public async Task<byte[]> ReceiveAsync(
            int maximumBytes,
            CancellationToken cancellationToken)
        {
            if (maximumBytes <= 0 ||
                maximumBytes >
                ClientBattleSecureChannel.MaximumDatagramBytes)
            {
                throw new ArgumentOutOfRangeException(nameof(maximumBytes));
            }

            var socket = RequireSocket();
            var buffer = new byte[maximumBytes + 1];
            var bytes = await AwaitCancelableAsync(
                Task.Factory.FromAsync(
                    (callback, state) => socket.BeginReceive(
                        buffer,
                        0,
                        buffer.Length,
                        SocketFlags.None,
                        callback,
                        state),
                    socket.EndReceive,
                    null),
                cancellationToken);
            if (bytes <= 0 || bytes > maximumBytes)
            {
                Array.Clear(buffer, 0, buffer.Length);
                throw new InvalidOperationException(
                    "client battle UDP datagram exceeds the hard limit");
            }

            var output = new byte[bytes];
            Buffer.BlockCopy(buffer, 0, output, 0, bytes);
            Array.Clear(buffer, 0, buffer.Length);
            return output;
        }

        /// <summary>
        /// 关闭 underlying socket 并使全部 pending I/O 有界结束。
        /// </summary>
        public void Dispose()
        {
            Socket socket;
            lock (_gate)
            {
                if (_disposed)
                {
                    return;
                }

                _disposed = true;
                socket = _socket;
                _socket = null;
            }

            if (socket != null)
            {
                try
                {
                    socket.Close();
                }
                finally
                {
                    socket.Dispose();
                }
            }
        }

        /// <summary>
        /// 返回 current connected socket 或拒绝未连接/已释放复用。
        /// </summary>
        /// <returns>Current socket owner。</returns>
        private Socket RequireSocket()
        {
            lock (_gate)
            {
                ThrowIfDisposedLocked();
                if (_socket == null || !_socket.Connected)
                {
                    throw new InvalidOperationException(
                        "client battle UDP socket is not connected");
                }

                return _socket;
            }
        }

        /// <summary>
        /// 在锁内拒绝 disposed adapter。
        /// </summary>
        private void ThrowIfDisposedLocked()
        {
            if (_disposed)
            {
                throw new ObjectDisposedException(
                    nameof(ClientBattleDatagramSocket));
            }
        }

        /// <summary>
        /// 等待 legacy socket async operation，并在 cancellation 时关闭独占 socket。
        /// </summary>
        /// <typeparam name="T">Operation result 类型。</typeparam>
        /// <param name="operation">Underlying socket task。</param>
        /// <param name="cancellationToken">Caller cancellation。</param>
        /// <returns>Operation result。</returns>
        private async Task<T> AwaitCancelableAsync<T>(
            Task<T> operation,
            CancellationToken cancellationToken)
        {
            if (operation.IsCompleted)
            {
                return await operation;
            }

            var cancelled = new TaskCompletionSource<bool>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            using (cancellationToken.Register(
                       () => cancelled.TrySetResult(true)))
            {
                var completed = await Task.WhenAny(
                    operation,
                    cancelled.Task);
                if (!ReferenceEquals(completed, operation))
                {
                    Dispose();
                    cancellationToken.ThrowIfCancellationRequested();
                }

                return await operation;
            }
        }

        /// <summary>
        /// 等待无结果 legacy socket operation，并在 cancellation 时关闭独占 socket。
        /// </summary>
        /// <param name="operation">Underlying socket task。</param>
        /// <param name="cancellationToken">Caller cancellation。</param>
        /// <returns>Operation 完成时结束。</returns>
        private async Task AwaitCancelableAsync(
            Task operation,
            CancellationToken cancellationToken)
        {
            if (operation.IsCompleted)
            {
                await operation;
                return;
            }

            var cancelled = new TaskCompletionSource<bool>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            using (cancellationToken.Register(
                       () => cancelled.TrySetResult(true)))
            {
                var completed = await Task.WhenAny(
                    operation,
                    cancelled.Task);
                if (!ReferenceEquals(completed, operation))
                {
                    Dispose();
                    cancellationToken.ThrowIfCancellationRequested();
                }

                await operation;
            }
        }
    }
}
