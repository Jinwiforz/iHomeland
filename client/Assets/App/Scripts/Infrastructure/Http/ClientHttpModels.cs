using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;

namespace IHomeland.Client.Infrastructure.Http
{
    /// <summary>
    /// 标识公开配置与 ticket 允许声明的客户端实时通道。
    /// </summary>
    internal enum ClientEndpointChannel
    {
        /// <summary>
        /// WebSocket control channel。
        /// </summary>
        Wss = 0,

        /// <summary>
        /// TLS/TCP reliable gameplay channel。
        /// </summary>
        TlsTcp = 1,
    }

    /// <summary>
    /// 保存服务端公开且不包含 credential 的单个连接 endpoint。
    /// </summary>
    internal sealed class ClientEndpoint
    {
        /// <summary>
        /// 创建已验证的 endpoint 投影。
        /// </summary>
        /// <param name="channel">endpoint 唯一允许的实时通道。</param>
        /// <param name="host">长度不超过 253 的非空 host。</param>
        /// <param name="port">1 到 65535 的 TCP port。</param>
        internal ClientEndpoint(ClientEndpointChannel channel, string host, int port)
        {
            Channel = channel;
            Host = host ?? throw new ArgumentNullException(nameof(host));
            Port = port;
        }

        /// <summary>
        /// 获取该 endpoint 允许建立的唯一通道。
        /// </summary>
        internal ClientEndpointChannel Channel { get; }

        /// <summary>
        /// 获取服务端提供并已通过长度校验的 host。
        /// </summary>
        internal string Host { get; }

        /// <summary>
        /// 获取服务端提供的 TCP port。
        /// </summary>
        internal int Port { get; }
    }

    /// <summary>
    /// 保存服务端公开的 HTTP 请求与 realtime frame 字节预算。
    /// </summary>
    internal sealed class ClientPublicLimits
    {
        /// <summary>
        /// 创建已验证的公开资源预算。
        /// </summary>
        /// <param name="httpBodyBytes">服务端允许的 HTTP request body 最大字节数。</param>
        /// <param name="realtimeFrameBytes">实时协议允许的单 frame 最大字节数。</param>
        internal ClientPublicLimits(int httpBodyBytes, int realtimeFrameBytes)
        {
            HttpBodyBytes = httpBodyBytes;
            RealtimeFrameBytes = realtimeFrameBytes;
        }

        /// <summary>
        /// 获取服务端 HTTP request body 上限，单位为字节；不得误用为 response 上限。
        /// </summary>
        internal int HttpBodyBytes { get; }

        /// <summary>
        /// 获取 realtime frame 上限，单位为字节。
        /// </summary>
        internal int RealtimeFrameBytes { get; }
    }

    /// <summary>
    /// 保存认证前取得的服务端版本与最低客户端要求。
    /// </summary>
    internal sealed class ClientVersionInfo
    {
        /// <summary>
        /// 创建结构已验证的版本投影。
        /// </summary>
        /// <param name="protocolVersion">服务端当前正整数协议版本。</param>
        /// <param name="minimumClientVersion">服务端接受的最低客户端版本。</param>
        /// <param name="serverVersion">服务端公开构建版本。</param>
        internal ClientVersionInfo(int protocolVersion, string minimumClientVersion, string serverVersion)
        {
            ProtocolVersion = protocolVersion;
            MinimumClientVersion = minimumClientVersion ?? throw new ArgumentNullException(nameof(minimumClientVersion));
            ServerVersion = serverVersion ?? throw new ArgumentNullException(nameof(serverVersion));
        }

        /// <summary>
        /// 获取服务端要求精确匹配的协议版本。
        /// </summary>
        internal int ProtocolVersion { get; }

        /// <summary>
        /// 获取客户端必须达到的最低版本。
        /// </summary>
        internal string MinimumClientVersion { get; }

        /// <summary>
        /// 获取只用于安全展示和诊断的服务端版本。
        /// </summary>
        internal string ServerVersion { get; }
    }

    /// <summary>
    /// 保存认证前取得并完整验证的公开 endpoint 与资源限制。
    /// </summary>
    internal sealed class ClientBootstrapConfiguration
    {
        /// <summary>
        /// 创建不允许调用方修改集合的启动配置。
        /// </summary>
        /// <param name="endpoints">最多八个已验证 endpoint。</param>
        /// <param name="limits">服务端公开资源预算。</param>
        internal ClientBootstrapConfiguration(
            IReadOnlyList<ClientEndpoint> endpoints,
            ClientPublicLimits limits)
        {
            Endpoints = new ReadOnlyCollection<ClientEndpoint>(
                new List<ClientEndpoint>(endpoints ?? throw new ArgumentNullException(nameof(endpoints))));
            Limits = limits ?? throw new ArgumentNullException(nameof(limits));
        }

        /// <summary>
        /// 获取服务端公开的不可变 endpoint 快照。
        /// </summary>
        internal IReadOnlyList<ClientEndpoint> Endpoints { get; }

        /// <summary>
        /// 获取与该 endpoint 快照一同提交的资源预算。
        /// </summary>
        internal ClientPublicLimits Limits { get; }
    }

    /// <summary>
    /// 保存认证响应中的账号安全摘要。
    /// </summary>
    internal sealed class ClientAccountSummary
    {
        /// <summary>
        /// 创建完整账号摘要。
        /// </summary>
        /// <param name="accountID">服务端分配的账号标识。</param>
        /// <param name="displayName">当前公开显示名。</param>
        /// <param name="createdAtMilliseconds">账号创建 Unix 时间，单位为毫秒。</param>
        internal ClientAccountSummary(string accountID, string displayName, long createdAtMilliseconds)
        {
            AccountID = accountID ?? throw new ArgumentNullException(nameof(accountID));
            DisplayName = displayName ?? throw new ArgumentNullException(nameof(displayName));
            CreatedAtMilliseconds = createdAtMilliseconds;
        }

        /// <summary>
        /// 获取服务端权威账号标识。
        /// </summary>
        internal string AccountID { get; }

        /// <summary>
        /// 获取服务端规范化后的显示名。
        /// </summary>
        internal string DisplayName { get; }

        /// <summary>
        /// 获取账号创建 Unix 时间，单位为毫秒。
        /// </summary>
        internal long CreatedAtMilliseconds { get; }
    }

    /// <summary>
    /// 保存认证响应中的 session identity、epoch 与绝对 expiry。
    /// </summary>
    internal sealed class ClientSessionSummary
    {
        /// <summary>
        /// 创建完整 session 摘要。
        /// </summary>
        /// <param name="sessionID">服务端分配的 session 标识。</param>
        /// <param name="sessionEpoch">从 1 开始的权威 session epoch。</param>
        /// <param name="expiresAtMilliseconds">Session 绝对 Unix expiry，单位为毫秒。</param>
        internal ClientSessionSummary(string sessionID, long sessionEpoch, long expiresAtMilliseconds)
        {
            SessionID = sessionID ?? throw new ArgumentNullException(nameof(sessionID));
            SessionEpoch = sessionEpoch;
            ExpiresAtMilliseconds = expiresAtMilliseconds;
        }

        /// <summary>
        /// 获取服务端权威 session 标识。
        /// </summary>
        internal string SessionID { get; }

        /// <summary>
        /// 获取随 logout/强制失效单调变化的 session epoch。
        /// </summary>
        internal long SessionEpoch { get; }

        /// <summary>
        /// 获取 session 绝对 Unix expiry，单位为毫秒。
        /// </summary>
        internal long ExpiresAtMilliseconds { get; }
    }

    /// <summary>
    /// 保存不得输出或持久化的 opaque access/refresh token pair。
    /// </summary>
    internal sealed class ClientTokenPair
    {
        /// <summary>
        /// 创建服务端已签发的 token pair。
        /// </summary>
        /// <param name="accessToken">只允许写入 Bearer header 的 opaque access token。</param>
        /// <param name="refreshToken">只允许写入 refresh request 的 opaque refresh token。</param>
        /// <param name="accessExpiresAtMilliseconds">Access token 绝对 Unix expiry，单位为毫秒。</param>
        /// <param name="refreshExpiresAtMilliseconds">Refresh token 绝对 Unix expiry，单位为毫秒。</param>
        internal ClientTokenPair(
            string accessToken,
            string refreshToken,
            long accessExpiresAtMilliseconds,
            long refreshExpiresAtMilliseconds)
        {
            AccessToken = accessToken ?? throw new ArgumentNullException(nameof(accessToken));
            RefreshToken = refreshToken ?? throw new ArgumentNullException(nameof(refreshToken));
            AccessExpiresAtMilliseconds = accessExpiresAtMilliseconds;
            RefreshExpiresAtMilliseconds = refreshExpiresAtMilliseconds;
        }

        /// <summary>
        /// 获取只能由 HTTP adapter 借用的 access token；调用方不得记录或持久化。
        /// </summary>
        internal string AccessToken { get; }

        /// <summary>
        /// 获取只能由 refresh operation 借用的 refresh token；调用方不得记录或持久化。
        /// </summary>
        internal string RefreshToken { get; }

        /// <summary>
        /// 获取 access token 绝对 Unix expiry，单位为毫秒。
        /// </summary>
        internal long AccessExpiresAtMilliseconds { get; }

        /// <summary>
        /// 获取 refresh token 绝对 Unix expiry，单位为毫秒。
        /// </summary>
        internal long RefreshExpiresAtMilliseconds { get; }

        /// <summary>
        /// 返回固定脱敏文本，阻止日志插值意外输出 token。
        /// </summary>
        /// <returns>不包含 token 或 expiry 的类型标签。</returns>
        public override string ToString()
        {
            return "ClientTokenPair[REDACTED]";
        }
    }

    /// <summary>
    /// 保存 register/login 原子返回的账号、session、token 与 endpoint 快照。
    /// </summary>
    internal sealed class ClientAuthentication
    {
        /// <summary>
        /// 创建不可变认证结果。
        /// </summary>
        /// <param name="account">账号摘要。</param>
        /// <param name="session">Session 摘要。</param>
        /// <param name="tokens">敏感 token pair。</param>
        /// <param name="endpoints">与该认证响应一同返回的 endpoint 快照。</param>
        internal ClientAuthentication(
            ClientAccountSummary account,
            ClientSessionSummary session,
            ClientTokenPair tokens,
            IReadOnlyList<ClientEndpoint> endpoints)
        {
            Account = account ?? throw new ArgumentNullException(nameof(account));
            Session = session ?? throw new ArgumentNullException(nameof(session));
            Tokens = tokens ?? throw new ArgumentNullException(nameof(tokens));
            Endpoints = new ReadOnlyCollection<ClientEndpoint>(
                new List<ClientEndpoint>(endpoints ?? throw new ArgumentNullException(nameof(endpoints))));
        }

        /// <summary>
        /// 获取认证 actor 的账号摘要。
        /// </summary>
        internal ClientAccountSummary Account { get; }

        /// <summary>
        /// 获取当前 session identity 与 epoch。
        /// </summary>
        internal ClientSessionSummary Session { get; }

        /// <summary>
        /// 获取不得输出或持久化的 token pair。
        /// </summary>
        internal ClientTokenPair Tokens { get; }

        /// <summary>
        /// 获取服务端随认证返回的 endpoint 只读快照。
        /// </summary>
        internal IReadOnlyList<ClientEndpoint> Endpoints { get; }

        /// <summary>
        /// 返回不包含 password、token 或不可信文本标识的安全 session correlation。
        /// </summary>
        /// <returns>只包含数值 session epoch 的文本。</returns>
        public override string ToString()
        {
            return $"ClientAuthentication sessionEpoch={Session.SessionEpoch}";
        }
    }

    /// <summary>
    /// 标识 connection ticket 允许后续通道使用的固定 scope。
    /// </summary>
    internal enum ClientConnectionScope
    {
        /// <summary>
        /// 允许建立 WSS control channel。
        /// </summary>
        Control = 0,

        /// <summary>
        /// 允许建立 TLS/TCP gameplay channel，但不替代 world admission。
        /// </summary>
        Gameplay = 1,
    }

    /// <summary>
    /// 保存服务端签发的短期单通道 opaque connection ticket。
    /// </summary>
    internal sealed class ClientConnectionTicket
    {
        /// <summary>
        /// 创建完整 ticket 投影。
        /// </summary>
        /// <param name="ticket">不得输出或复用的 opaque credential。</param>
        /// <param name="endpoint">credential 绑定的受信 endpoint。</param>
        /// <param name="scopes">服务端固定授予的非空 scope 集合。</param>
        /// <param name="expiresAtMilliseconds">Ticket 绝对 Unix expiry，单位为毫秒。</param>
        internal ClientConnectionTicket(
            string ticket,
            ClientEndpoint endpoint,
            IReadOnlyList<ClientConnectionScope> scopes,
            long expiresAtMilliseconds)
        {
            Ticket = ticket ?? throw new ArgumentNullException(nameof(ticket));
            Endpoint = endpoint ?? throw new ArgumentNullException(nameof(endpoint));
            Scopes = new ReadOnlyCollection<ClientConnectionScope>(
                new List<ClientConnectionScope>(scopes ?? throw new ArgumentNullException(nameof(scopes))));
            ExpiresAtMilliseconds = expiresAtMilliseconds;
        }

        /// <summary>
        /// 获取只能交付一次给匹配 channel 的 opaque ticket。
        /// </summary>
        internal string Ticket { get; }

        /// <summary>
        /// 获取 credential 绑定且不得由 payload 覆盖的 endpoint。
        /// </summary>
        internal ClientEndpoint Endpoint { get; }

        /// <summary>
        /// 获取服务端授予的不可变 scope 集合。
        /// </summary>
        internal IReadOnlyList<ClientConnectionScope> Scopes { get; }

        /// <summary>
        /// 获取 ticket 绝对 Unix expiry，单位为毫秒。
        /// </summary>
        internal long ExpiresAtMilliseconds { get; }

        /// <summary>
        /// 返回不包含 credential 的安全 endpoint/expiry 摘要。
        /// </summary>
        /// <returns>通道、host、port 与 expiry 的文本。</returns>
        public override string ToString()
        {
            return $"ClientConnectionTicket[REDACTED] channel={Endpoint.Channel} host={Endpoint.Host} port={Endpoint.Port} expiresAtMs={ExpiresAtMilliseconds}";
        }
    }

    /// <summary>
    /// 标识 PersonalWorld 的公开持久生命周期。
    /// </summary>
    internal enum ClientPersonalWorldLifecycle
    {
        /// <summary>
        /// 世界允许解析 current active assignment。
        /// </summary>
        Active = 0,

        /// <summary>
        /// 世界已归档且不得继续进入。
        /// </summary>
        Archived = 1,
    }

    /// <summary>
    /// 保存 own-world bootstrap 中的 PersonalWorld 安全摘要。
    /// </summary>
    internal sealed class ClientPersonalWorldSummary
    {
        /// <summary>
        /// 创建完整 PersonalWorld 摘要。
        /// </summary>
        /// <param name="personalWorldID">PersonalWorld 标识。</param>
        /// <param name="ownerPlayerID">不可转移 Owner 的 Player 标识。</param>
        /// <param name="lifecycle">公开持久生命周期。</param>
        /// <param name="revision">从 1 开始的单调 world revision。</param>
        /// <param name="createdAtMilliseconds">创建 Unix 时间，单位为毫秒。</param>
        internal ClientPersonalWorldSummary(
            string personalWorldID,
            string ownerPlayerID,
            ClientPersonalWorldLifecycle lifecycle,
            long revision,
            long createdAtMilliseconds)
        {
            PersonalWorldID = personalWorldID ?? throw new ArgumentNullException(nameof(personalWorldID));
            OwnerPlayerID = ownerPlayerID ?? throw new ArgumentNullException(nameof(ownerPlayerID));
            Lifecycle = lifecycle;
            Revision = revision;
            CreatedAtMilliseconds = createdAtMilliseconds;
        }

        /// <summary>
        /// 获取服务端权威 PersonalWorld 标识。
        /// </summary>
        internal string PersonalWorldID { get; }

        /// <summary>
        /// 获取不可由客户端转移或推断的 Owner Player 标识。
        /// </summary>
        internal string OwnerPlayerID { get; }

        /// <summary>
        /// 获取公开持久生命周期。
        /// </summary>
        internal ClientPersonalWorldLifecycle Lifecycle { get; }

        /// <summary>
        /// 获取 world 单调 revision。
        /// </summary>
        internal long Revision { get; }

        /// <summary>
        /// 获取创建 Unix 时间，单位为毫秒。
        /// </summary>
        internal long CreatedAtMilliseconds { get; }
    }

    /// <summary>
    /// 保存 own-world bootstrap 中可公开给客户端的 current assignment 投影。
    /// </summary>
    internal sealed class ClientWorldAssignment
    {
        /// <summary>
        /// 创建不包含 node、fence 或完整 AssignmentStamp 的安全投影。
        /// </summary>
        /// <param name="personalWorldID">Assignment 所属 PersonalWorld。</param>
        /// <param name="worldInstanceID">Current WorldInstance 标识。</param>
        /// <param name="endpoint">只允许 TLS/TCP 的受信 endpoint。</param>
        /// <param name="generation">从 1 开始的 assignment generation。</param>
        /// <param name="leaseExpiresAtMilliseconds">Assignment lease 的 Unix expiry，单位为毫秒。</param>
        internal ClientWorldAssignment(
            string personalWorldID,
            string worldInstanceID,
            ClientEndpoint endpoint,
            long generation,
            long leaseExpiresAtMilliseconds)
        {
            PersonalWorldID = personalWorldID ?? throw new ArgumentNullException(nameof(personalWorldID));
            WorldInstanceID = worldInstanceID ?? throw new ArgumentNullException(nameof(worldInstanceID));
            Endpoint = endpoint ?? throw new ArgumentNullException(nameof(endpoint));
            Generation = generation;
            LeaseExpiresAtMilliseconds = leaseExpiresAtMilliseconds;
        }

        /// <summary>
        /// 获取 assignment 所属 PersonalWorld 标识。
        /// </summary>
        internal string PersonalWorldID { get; }

        /// <summary>
        /// 获取当前 WorldInstance 标识；该值本身不授予写资格。
        /// </summary>
        internal string WorldInstanceID { get; }

        /// <summary>
        /// 获取服务端公开的 TLS/TCP endpoint。
        /// </summary>
        internal ClientEndpoint Endpoint { get; }

        /// <summary>
        /// 获取 assignment generation；不得替代完整服务端 stamp。
        /// </summary>
        internal long Generation { get; }

        /// <summary>
        /// 获取 lease Unix expiry，单位为毫秒。
        /// </summary>
        internal long LeaseExpiresAtMilliseconds { get; }
    }

    /// <summary>
    /// 保存 HTTP own-world bootstrap 的一次性安全查询结果。
    /// </summary>
    internal sealed class ClientWorldBootstrap
    {
        /// <summary>
        /// 创建 world 与可选 current assignment 投影。
        /// </summary>
        /// <param name="world">认证 actor 的 primary PersonalWorld。</param>
        /// <param name="assignment">当前 active assignment；服务端允许缺失时为空。</param>
        internal ClientWorldBootstrap(
            ClientPersonalWorldSummary world,
            ClientWorldAssignment assignment)
        {
            World = world ?? throw new ArgumentNullException(nameof(world));
            Assignment = assignment;
        }

        /// <summary>
        /// 获取认证 actor 的 primary PersonalWorld 安全摘要。
        /// </summary>
        internal ClientPersonalWorldSummary World { get; }

        /// <summary>
        /// 获取响应时的 current assignment；缺失时为空且客户端不得自行补全。
        /// </summary>
        internal ClientWorldAssignment Assignment { get; }
    }
}
