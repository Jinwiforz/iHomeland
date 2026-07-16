using System;

namespace IHomeland.Client.Core.Configuration
{
    /// <summary>
    /// 保存 App Scope 启动时冻结且不含 secret 的客户端环境与构建身份。
    /// </summary>
    internal sealed class ClientEnvironment
    {
        /// <summary>
        /// 创建已经通过全部安全交叉验证的环境快照。
        /// </summary>
        /// <param name="environmentKind">决定 loopback 明文例外的环境类别。</param>
        /// <param name="httpBaseUri">不含业务 path 的公开 HTTP API 根地址。</param>
        /// <param name="clientVersion">用于 minimum client compatibility 的非空版本。</param>
        /// <param name="protocolVersion">客户端锁定的正整数协议版本。</param>
        private ClientEnvironment(
            ClientEnvironmentKind environmentKind,
            Uri httpBaseUri,
            string clientVersion,
            int protocolVersion)
        {
            EnvironmentKind = environmentKind;
            HttpBaseUri = httpBaseUri;
            ClientVersion = clientVersion;
            ProtocolVersion = protocolVersion;
        }

        /// <summary>
        /// 获取决定网络安全策略的环境类别。
        /// </summary>
        internal ClientEnvironmentKind EnvironmentKind { get; }

        /// <summary>
        /// 获取只用于拼接冻结相对 path 的公开 HTTP API 根地址。
        /// </summary>
        internal Uri HttpBaseUri { get; }

        /// <summary>
        /// 获取 PlayerSettings/Application 提供的当前客户端版本。
        /// </summary>
        internal string ClientVersion { get; }

        /// <summary>
        /// 获取客户端锁定并用于服务端兼容性比较的协议版本。
        /// </summary>
        internal int ProtocolVersion { get; }

        /// <summary>
        /// 从不可信序列化字段创建严格环境快照，确保失败发生在任何网络副作用之前。
        /// </summary>
        /// <param name="environmentKind">配置声明的环境类别。</param>
        /// <param name="httpBaseUri">配置声明的 HTTP API 根地址文本。</param>
        /// <param name="clientVersion">当前客户端版本文本。</param>
        /// <param name="protocolVersion">当前客户端协议版本。</param>
        /// <returns>已完成 scheme、host、path 与 build identity 校验的环境。</returns>
        /// <exception cref="InvalidOperationException">任一字段不符合契约时抛出；消息不会回显原始配置值。</exception>
        internal static ClientEnvironment Create(
            ClientEnvironmentKind environmentKind,
            string httpBaseUri,
            string clientVersion,
            int protocolVersion)
        {
            if (!Enum.IsDefined(typeof(ClientEnvironmentKind), environmentKind))
            {
                throw new InvalidOperationException("客户端环境类别无效。");
            }

            if (string.IsNullOrWhiteSpace(clientVersion) || clientVersion.Length > 32)
            {
                throw new InvalidOperationException("客户端版本必须是长度不超过 32 的非空值。");
            }

            if (protocolVersion <= 0)
            {
                throw new InvalidOperationException("客户端协议版本必须为正数。");
            }

            if (!Uri.TryCreate(httpBaseUri, UriKind.Absolute, out var uri))
            {
                throw new InvalidOperationException("HTTP base URI 必须是绝对地址。");
            }

            var isHttps = string.Equals(uri.Scheme, Uri.UriSchemeHttps, StringComparison.OrdinalIgnoreCase);
            var isHttp = string.Equals(uri.Scheme, Uri.UriSchemeHttp, StringComparison.OrdinalIgnoreCase);
            if (!isHttps && !isHttp)
            {
                throw new InvalidOperationException("HTTP base URI 只允许 http 或 https scheme。");
            }

            if (uri.Port <= 0 || uri.Port > 65535)
            {
                throw new InvalidOperationException("HTTP base URI port 必须在 1 到 65535 之间。");
            }

            if (environmentKind == ClientEnvironmentKind.Production && !isHttps)
            {
                throw new InvalidOperationException("Production HTTP base URI 必须使用 HTTPS。");
            }

            if (isHttp && (environmentKind == ClientEnvironmentKind.Production || !uri.IsLoopback))
            {
                throw new InvalidOperationException("明文 HTTP 只允许显式 Local/Test 环境的 loopback 地址。");
            }

            if (!string.IsNullOrEmpty(uri.UserInfo) ||
                !string.IsNullOrEmpty(uri.Query) ||
                !string.IsNullOrEmpty(uri.Fragment) ||
                (uri.AbsolutePath != "/" && uri.AbsolutePath.Length != 0))
            {
                throw new InvalidOperationException("HTTP base URI 不得包含 userinfo、query、fragment 或业务 path。");
            }

            var normalizedBaseUri = new UriBuilder(uri)
            {
                Path = "/",
                Query = string.Empty,
                Fragment = string.Empty,
            }.Uri;

            return new ClientEnvironment(
                environmentKind,
                normalizedBaseUri,
                clientVersion,
                protocolVersion);
        }
    }
}
