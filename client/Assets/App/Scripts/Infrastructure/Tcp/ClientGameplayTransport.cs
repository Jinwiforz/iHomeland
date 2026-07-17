using System;
using System.IO;
using System.Net;
using System.Net.Security;
using System.Net.Sockets;
using System.Security.Authentication;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Core.Configuration;
using IHomeland.Client.Infrastructure.Http;

namespace IHomeland.Client.Infrastructure.Tcp
{
    /// <summary>
    /// 定义 gameplay channel 独占的 exact stream 连接边界。
    /// </summary>
    internal interface IClientGameplayConnection : IDisposable
    {
        /// <summary>从当前 stream 读取部分字节。</summary>
        /// <param name="buffer">目标 buffer。</param>
        /// <param name="offset">写入起始位置。</param>
        /// <param name="count">最多读取字节数。</param>
        /// <param name="cancellationToken">连接生命周期信号。</param>
        /// <returns>实际读取字节数；零表示 peer 关闭。</returns>
        Task<int> ReadAsync(byte[] buffer, int offset, int count, CancellationToken cancellationToken);

        /// <summary>完整写入一个已经 framing 的 buffer。</summary>
        /// <param name="buffer">不可在写入期间修改的 frame。</param>
        /// <param name="cancellationToken">连接生命周期信号。</param>
        /// <returns>写入完成时结束的任务。</returns>
        Task WriteAsync(byte[] buffer, CancellationToken cancellationToken);
    }

    /// <summary>
    /// 为每次显式 connect 创建独立 TCP/TLS connection。
    /// </summary>
    internal interface IClientGameplayConnectionFactory
    {
        /// <summary>按环境与 endpoint policy 建立 gameplay transport。</summary>
        /// <param name="endpoint">World admission 与 ticket 共同绑定的 endpoint。</param>
        /// <param name="cancellationToken">显式连接 deadline 与 App Scope 信号。</param>
        /// <returns>由 gameplay channel 独占并释放的 connection。</returns>
        Task<IClientGameplayConnection> ConnectAsync(
            ClientEndpoint endpoint,
            CancellationToken cancellationToken);
    }

    /// <summary>
    /// 使用 BCL TcpClient/SslStream 实现 Windows/Mono gameplay transport。
    /// </summary>
    internal sealed class SystemClientGameplayConnectionFactory : IClientGameplayConnectionFactory
    {
        /// <summary>
        /// Unity netstandard profile 尚未命名该 enum member；12288 是 BCL 固定 TLS 1.3 protocol value。
        /// </summary>
        private const SslProtocols Tls13 = (SslProtocols)12288;

        /// <summary>决定 TLS 强制与 loopback plaintext 例外。</summary>
        private readonly ClientEnvironment _environment;

        /// <summary>创建绑定冻结环境的 factory。</summary>
        /// <param name="environment">App Scope 已验证环境。</param>
        internal SystemClientGameplayConnectionFactory(ClientEnvironment environment)
        {
            _environment = environment ?? throw new ArgumentNullException(nameof(environment));
        }

        /// <inheritdoc />
        public async Task<IClientGameplayConnection> ConnectAsync(
            ClientEndpoint endpoint,
            CancellationToken cancellationToken)
        {
            if (endpoint == null || endpoint.Channel != ClientEndpointChannel.TlsTcp)
            {
                throw new InvalidOperationException("Gameplay endpoint 必须是 TLS_TCP。");
            }

            var plaintext = _environment.EnvironmentKind != ClientEnvironmentKind.Production &&
                            IsLoopbackHost(endpoint.Host);
            var client = new TcpClient(AddressFamily.InterNetworkV6);
            client.Client.DualMode = true;
            client.NoDelay = true;
            try
            {
                await AwaitWithCancellation(
                    client.ConnectAsync(endpoint.Host, endpoint.Port),
                    cancellationToken,
                    client);
                Stream stream = client.GetStream();
                if (!plaintext)
                {
                    var ssl = new SslStream(stream, false);
                    await AwaitWithCancellation(
                        ssl.AuthenticateAsClientAsync(endpoint.Host),
                        cancellationToken,
                        client);
                    if (ssl.SslProtocol != Tls13)
                    {
                        ssl.Dispose();
                        throw new AuthenticationException("Gameplay transport 未协商 TLS 1.3。");
                    }

                    stream = ssl;
                }

                return new SystemClientGameplayConnection(client, stream);
            }
            catch
            {
                client.Dispose();
                throw;
            }
        }

        /// <summary>只接受 literal loopback 或 localhost，不在 plaintext policy 中触发 DNS。</summary>
        /// <param name="host">Endpoint host。</param>
        /// <returns>明确为 loopback 时返回 true。</returns>
        private static bool IsLoopbackHost(string host)
        {
            return string.Equals(host, "localhost", StringComparison.OrdinalIgnoreCase) ||
                   (IPAddress.TryParse(host, out var address) && IPAddress.IsLoopback(address));
        }

        /// <summary>为不接受 CancellationToken 的 Mono socket/TLS API 增加可取消等待。</summary>
        /// <param name="operation">底层 connect 或 TLS task。</param>
        /// <param name="cancellationToken">外层 deadline/lifetime 信号。</param>
        /// <param name="client">取消时关闭以打断底层 operation 的 socket。</param>
        /// <returns>Operation 正常结束时完成。</returns>
        private static async Task AwaitWithCancellation(
            Task operation,
            CancellationToken cancellationToken,
            TcpClient client)
        {
            var cancellation = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
            using (cancellationToken.Register(() => cancellation.TrySetResult(true)))
            {
                var completed = await Task.WhenAny(operation, cancellation.Task);
                if (!ReferenceEquals(completed, operation))
                {
                    client.Dispose();
                    ObserveLateFault(operation);
                    cancellationToken.ThrowIfCancellationRequested();
                }

                await operation;
            }
        }

        /// <summary>观察 socket dispose 后迟到的底层 fault，避免产生未观察 task。</summary>
        /// <param name="operation">已不再由调用方等待的 connect 或 TLS task。</param>
        private static void ObserveLateFault(Task operation)
        {
            _ = operation.ContinueWith(
                completed =>
                {
                    _ = completed.Exception;
                },
                CancellationToken.None,
                TaskContinuationOptions.OnlyOnFaulted | TaskContinuationOptions.ExecuteSynchronously,
                TaskScheduler.Default);
        }
    }

    /// <summary>
    /// 以唯一 Stream 提供 gameplay connection 的部分读与完整写。
    /// </summary>
    internal sealed class SystemClientGameplayConnection : IClientGameplayConnection
    {
        /// <summary>拥有 OS socket 生命周期。</summary>
        private readonly TcpClient _client;

        /// <summary>Plaintext loopback 或完成 TLS 的唯一 stream。</summary>
        private readonly Stream _stream;

        /// <summary>创建 channel 独占连接。</summary>
        /// <param name="client">已连接 TcpClient。</param>
        /// <param name="stream">已完成安全 policy 的 stream。</param>
        internal SystemClientGameplayConnection(TcpClient client, Stream stream)
        {
            _client = client ?? throw new ArgumentNullException(nameof(client));
            _stream = stream ?? throw new ArgumentNullException(nameof(stream));
        }

        /// <inheritdoc />
        public Task<int> ReadAsync(
            byte[] buffer,
            int offset,
            int count,
            CancellationToken cancellationToken)
        {
            return _stream.ReadAsync(buffer, offset, count, cancellationToken);
        }

        /// <inheritdoc />
        public async Task WriteAsync(byte[] buffer, CancellationToken cancellationToken)
        {
            if (buffer == null)
            {
                throw new ArgumentNullException(nameof(buffer));
            }

            await _stream.WriteAsync(buffer, 0, buffer.Length, cancellationToken);
            await _stream.FlushAsync(cancellationToken);
        }

        /// <summary>关闭 stream 与 socket；重复调用由 BCL 幂等处理。</summary>
        public void Dispose()
        {
            _stream.Dispose();
            _client.Dispose();
        }
    }
}
