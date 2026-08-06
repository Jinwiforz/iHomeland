using System;
using System.Threading;

namespace IHomeland.Client.Networking.Application.Contracts
{
    /// <summary>
    /// 标识 opaque credential 只能交给哪一种已登记 gateway operation。
    /// </summary>
    internal enum ClientCredentialPurpose
    {
        /// <summary>账号注册使用的 password。</summary>
        RegisterPassword = 0,

        /// <summary>账号登录使用的 password。</summary>
        LoginPassword = 1,

        /// <summary>Session 轮换使用的 refresh token。</summary>
        RefreshSession = 2,

        /// <summary>带认证 HTTP operation 使用的 access token。</summary>
        HttpAuthorization = 3,
    }

    /// <summary>
    /// 保存只能由匹配 Infrastructure adapter 单次取得的 opaque credential。
    /// </summary>
    /// <remarks>
    /// 该 lease 不提供 credential 属性，避免 port、flow 或诊断代码把 secret 当普通字符串传播。
    /// </remarks>
    internal sealed class ClientCredentialLease
    {
        /// <summary>保存尚未交付的 credential。</summary>
        private readonly string _credential;

        /// <summary>使用原子位保证 credential 最多成功交付一次。</summary>
        private int _taken;

        /// <summary>创建绑定固定 operation purpose 的单次 lease。</summary>
        /// <param name="credential">不得输出的 opaque credential。</param>
        /// <param name="purpose">唯一允许取得 credential 的 operation purpose。</param>
        /// <exception cref="ArgumentException">Credential 为空时抛出。</exception>
        internal ClientCredentialLease(string credential, ClientCredentialPurpose purpose)
        {
            if (string.IsNullOrEmpty(credential))
            {
                throw new ArgumentException("Credential 不能为空。", nameof(credential));
            }

            _credential = credential;
            Purpose = purpose;
        }

        /// <summary>获取该 lease 绑定的 operation purpose。</summary>
        internal ClientCredentialPurpose Purpose { get; }

        /// <summary>由匹配 adapter 原子取得 credential。</summary>
        /// <param name="expectedPurpose">调用 operation 要求的 purpose。</param>
        /// <param name="credential">成功时返回唯一 credential 使用权。</param>
        /// <returns>Purpose 匹配且尚未交付时返回 true。</returns>
        internal bool TryTake(
            ClientCredentialPurpose expectedPurpose,
            out string credential)
        {
            credential = null;
            if (Purpose != expectedPurpose ||
                Interlocked.CompareExchange(ref _taken, 1, 0) != 0)
            {
                return false;
            }

            credential = _credential;
            return true;
        }

        /// <summary>返回固定脱敏摘要。</summary>
        /// <returns>不包含 credential 的 purpose。</returns>
        public override string ToString()
        {
            return $"ClientCredentialLease[REDACTED] purpose={Purpose}";
        }
    }

    /// <summary>
    /// 保存注册 operation 所需的公开字段和单次 password lease。
    /// </summary>
    internal sealed class ClientRegisterGatewayRequest
    {
        /// <summary>创建不可变注册请求。</summary>
        /// <param name="username">公开 username。</param>
        /// <param name="password">仅限注册 operation 的 password lease。</param>
        /// <param name="displayName">公开 display name。</param>
        internal ClientRegisterGatewayRequest(
            string username,
            ClientCredentialLease password,
            string displayName)
        {
            Username = username ?? throw new ArgumentNullException(nameof(username));
            Password = password ?? throw new ArgumentNullException(nameof(password));
            DisplayName = displayName ?? throw new ArgumentNullException(nameof(displayName));
        }

        /// <summary>获取公开 username。</summary>
        internal string Username { get; }

        /// <summary>获取仅限注册 adapter 单次取得的 password。</summary>
        internal ClientCredentialLease Password { get; }

        /// <summary>获取公开 display name。</summary>
        internal string DisplayName { get; }
    }

    /// <summary>
    /// 保存登录 operation 所需的公开 username 和单次 password lease。
    /// </summary>
    internal sealed class ClientLoginGatewayRequest
    {
        /// <summary>创建不可变登录请求。</summary>
        /// <param name="username">公开 username。</param>
        /// <param name="password">仅限登录 operation 的 password lease。</param>
        internal ClientLoginGatewayRequest(string username, ClientCredentialLease password)
        {
            Username = username ?? throw new ArgumentNullException(nameof(username));
            Password = password ?? throw new ArgumentNullException(nameof(password));
        }

        /// <summary>获取公开 username。</summary>
        internal string Username { get; }

        /// <summary>获取仅限登录 adapter 单次取得的 password。</summary>
        internal ClientCredentialLease Password { get; }
    }

    /// <summary>
    /// 保存只携带单次 opaque credential 的 gateway 请求。
    /// </summary>
    internal sealed class ClientCredentialGatewayRequest
    {
        /// <summary>创建不可变 credential 请求。</summary>
        /// <param name="credential">与目标 operation purpose 匹配的 lease。</param>
        internal ClientCredentialGatewayRequest(ClientCredentialLease credential)
        {
            Credential = credential ?? throw new ArgumentNullException(nameof(credential));
        }

        /// <summary>获取只允许 adapter 单次取得的 credential。</summary>
        internal ClientCredentialLease Credential { get; }
    }

    /// <summary>
    /// 保存 connection ticket operation 的授权与固定 channel。
    /// </summary>
    internal sealed class ClientConnectionTicketGatewayRequest
    {
        /// <summary>创建不可变 ticket 请求。</summary>
        /// <param name="authorization">单次 HTTP authorization lease。</param>
        /// <param name="channel">固定 WSS 或 TLS/TCP channel。</param>
        internal ClientConnectionTicketGatewayRequest(
            ClientCredentialLease authorization,
            ClientEndpointChannel channel)
        {
            Authorization = authorization ??
                throw new ArgumentNullException(nameof(authorization));
            Channel = channel;
        }

        /// <summary>获取单次 HTTP authorization lease。</summary>
        internal ClientCredentialLease Authorization { get; }

        /// <summary>获取 ticket 绑定 channel。</summary>
        internal ClientEndpointChannel Channel { get; }
    }

    /// <summary>
    /// 保存接受邀请 operation 的授权、身份、revision 与幂等 key。
    /// </summary>
    internal sealed class ClientAcceptVisitInviteGatewayRequest
    {
        /// <summary>创建不可变接受邀请请求。</summary>
        /// <param name="authorization">单次 HTTP authorization lease。</param>
        /// <param name="invite">封闭 invite identity 与 expected revision。</param>
        /// <param name="idempotencyKey">当前 intent 的稳定 key。</param>
        internal ClientAcceptVisitInviteGatewayRequest(
            ClientCredentialLease authorization,
            ClientVisitInviteAcceptRequest invite,
            string idempotencyKey)
        {
            Authorization = authorization ??
                throw new ArgumentNullException(nameof(authorization));
            Invite = invite ?? throw new ArgumentNullException(nameof(invite));
            IdempotencyKey = idempotencyKey ??
                throw new ArgumentNullException(nameof(idempotencyKey));
        }

        /// <summary>获取单次 HTTP authorization lease。</summary>
        internal ClientCredentialLease Authorization { get; }

        /// <summary>获取封闭 invite identity 与 revision。</summary>
        internal ClientVisitInviteAcceptRequest Invite { get; }

        /// <summary>获取当前 intent 的稳定幂等 key。</summary>
        internal string IdempotencyKey { get; }
    }

    /// <summary>
    /// 保存 world admission operation 的授权、封闭 target 与幂等 key。
    /// </summary>
    internal sealed class ClientWorldAdmissionGatewayRequest
    {
        /// <summary>创建不可变 admission 请求。</summary>
        /// <param name="authorization">单次 HTTP authorization lease。</param>
        /// <param name="target">封闭 world target。</param>
        /// <param name="idempotencyKey">当前 intent 的稳定 key。</param>
        internal ClientWorldAdmissionGatewayRequest(
            ClientCredentialLease authorization,
            ClientWorldAdmissionTarget target,
            string idempotencyKey)
        {
            Authorization = authorization ??
                throw new ArgumentNullException(nameof(authorization));
            Target = target ?? throw new ArgumentNullException(nameof(target));
            IdempotencyKey = idempotencyKey ??
                throw new ArgumentNullException(nameof(idempotencyKey));
        }

        /// <summary>获取单次 HTTP authorization lease。</summary>
        internal ClientCredentialLease Authorization { get; }

        /// <summary>获取封闭 world target。</summary>
        internal ClientWorldAdmissionTarget Target { get; }

        /// <summary>获取当前 intent 的稳定幂等 key。</summary>
        internal string IdempotencyKey { get; }
    }
}
