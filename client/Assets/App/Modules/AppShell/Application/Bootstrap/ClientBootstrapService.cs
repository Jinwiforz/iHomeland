using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Networking.Application.Contracts;
using IHomeland.Client.Networking.Application.Ports;
using IHomeland.Client.AppShell.Application.Configuration;

namespace IHomeland.Client.AppShell.Application.Bootstrap
{
    /// <summary>
    /// 按 version 后 config 的固定顺序建立客户端认证前启动投影。
    /// </summary>
    internal sealed class ClientBootstrapService
    {
        /// <summary>
        /// 序列化显式 bootstrap，防止并发响应以完成顺序覆盖较新结论。
        /// </summary>
        private readonly SemaphoreSlim _gate = new SemaphoreSlim(1, 1);

        /// <summary>
        /// 保存客户端 build identity 与受信 HTTP base URI。
        /// </summary>
        private readonly ClientEnvironment _environment;

        /// <summary>
        /// 保存只暴露冻结 operation 的 HTTP API。
        /// </summary>
        private readonly IClientBootstrapGateway _gateway;

        /// <summary>
        /// 保存 App Scope 唯一 Configuration owner。
        /// </summary>
        private readonly ClientConfigurationStore _configurationStore;

        /// <summary>
        /// 创建显式 bootstrap 用例。
        /// </summary>
        /// <param name="environment">已验证客户端环境与 build identity。</param>
        /// <param name="gateway">认证前固定 operation 边界。</param>
        /// <param name="configurationStore">唯一配置发布 owner。</param>
        /// <exception cref="ArgumentNullException">任一依赖为空时抛出。</exception>
        internal ClientBootstrapService(
            ClientEnvironment environment,
            IClientBootstrapGateway gateway,
            ClientConfigurationStore configurationStore)
        {
            _environment = environment ?? throw new ArgumentNullException(nameof(environment));
            _gateway = gateway ?? throw new ArgumentNullException(nameof(gateway));
            _configurationStore = configurationStore ?? throw new ArgumentNullException(nameof(configurationStore));
        }

        /// <summary>
        /// 查询并验证 version/config，只有两步都成功时才原子发布配置。
        /// </summary>
        /// <param name="cancellationToken">取消本次等待；已成功旧配置不会被部分结果覆盖。</param>
        /// <returns>完整启动快照、服务端错误或稳定本地失败。</returns>
        internal async Task<ClientGatewayResult<ClientConfigurationSnapshot>> BootstrapAsync(
            CancellationToken cancellationToken)
        {
            var enteredGate = false;
            try
            {
                try
                {
                    await _gate.WaitAsync(cancellationToken);
                    enteredGate = true;
                }
                catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
                {
                    return ClientGatewayResult<ClientConfigurationSnapshot>.Failed(new ClientGatewayFailure(
                        ClientGatewayFailureKind.CallerCancelled,
                        ClientOperationIDs.GetVersion));
                }

                var versionResult = await _gateway.GetVersionAsync(cancellationToken);
                if (!versionResult.IsSuccess)
                {
                    return ConvertFailure<ClientVersionInfo>(versionResult);
                }

                if (!IsCompatible(versionResult.Value))
                {
                    _configurationStore.MarkIncompatible();
                    return ClientGatewayResult<ClientConfigurationSnapshot>.Failed(new ClientGatewayFailure(
                        ClientGatewayFailureKind.LocalPolicy,
                        ClientOperationIDs.GetVersion));
                }

                var configurationResult = await _gateway.GetBootstrapConfigurationAsync(cancellationToken);
                if (!configurationResult.IsSuccess)
                {
                    return ConvertFailure<ClientBootstrapConfiguration>(configurationResult);
                }

                var snapshot = new ClientConfigurationSnapshot(
                    versionResult.Value,
                    configurationResult.Value);
                _configurationStore.Publish(snapshot);
                return ClientGatewayResult<ClientConfigurationSnapshot>.Success(snapshot);
            }
            finally
            {
                if (enteredGate)
                {
                    _gate.Release();
                }
            }
        }

        /// <summary>
        /// 比较锁定协议版本与三段数值客户端版本。
        /// </summary>
        /// <param name="version">服务端公开 version response。</param>
        /// <returns>协议精确匹配且当前客户端不低于 minimum 时返回 true。</returns>
        private bool IsCompatible(ClientVersionInfo version)
        {
            if (version.ProtocolVersion != _environment.ProtocolVersion)
            {
                return false;
            }

            return TryParseThreePartVersion(_environment.ClientVersion, out var current) &&
                   TryParseThreePartVersion(version.MinimumClientVersion, out var minimum) &&
                   current.CompareTo(minimum) >= 0;
        }

        /// <summary>
        /// 解析项目当前使用的 `major.minor.patch` 非负数值版本。
        /// </summary>
        /// <param name="text">长度已受 OpenAPI 或环境配置约束的版本文本。</param>
        /// <param name="version">成功时返回可比较的 BCL Version。</param>
        /// <returns>文本精确包含三个非负整数段时返回 true。</returns>
        private static bool TryParseThreePartVersion(string text, out Version version)
        {
            version = null;
            var parts = text?.Split('.');
            if (parts == null || parts.Length != 3)
            {
                return false;
            }

            if (!int.TryParse(parts[0], out var major) ||
                !int.TryParse(parts[1], out var minor) ||
                !int.TryParse(parts[2], out var patch) ||
                major < 0 || minor < 0 || patch < 0)
            {
                return false;
            }

            version = new Version(major, minor, patch);
            return true;
        }

        /// <summary>
        /// 保留上游服务端错误或本地失败，同时改变成功泛型类型。
        /// </summary>
        /// <typeparam name="TSource">上游成功投影类型。</typeparam>
        /// <param name="source">已确认非成功的上游结果。</param>
        /// <returns>不丢失安全错误语义的 bootstrap 结果。</returns>
        private static ClientGatewayResult<ClientConfigurationSnapshot> ConvertFailure<TSource>(
            ClientGatewayResult<TSource> source)
        {
            return source.ServerError != null
                ? ClientGatewayResult<ClientConfigurationSnapshot>.Rejected(source.ServerError)
                : ClientGatewayResult<ClientConfigurationSnapshot>.Failed(source.Failure);
        }
    }
}
