using System;
using System.Collections.Generic;
using System.Threading;
using IHomeland.Client.Networking.Application.Contracts;

namespace IHomeland.Client.Session.Application
{
    /// <summary>
    /// 标识唯一 Session owner 当前是否允许认证调用。
    /// </summary>
    internal enum ClientSessionOwnerState
    {
        /// <summary>
        /// 尚未持有认证 session。
        /// </summary>
        Unauthenticated = 0,

        /// <summary>
        /// 持有完整且可借用的当前 session snapshot。
        /// </summary>
        Authenticated = 1,

        /// <summary>
        /// Refresh/logout 提交结果未知，必须通过新的 register/login 或 Forget 显式解决。
        /// </summary>
        Unresolved = 2,

        /// <summary>
        /// App Scope 已停止且拒绝全部状态提交。
        /// </summary>
        Stopped = 3,
    }

    /// <summary>
    /// 保存唯一 Session owner 原子发布的账号、session、token 与本地 generation。
    /// </summary>
    internal sealed class ClientSessionSnapshot
    {
        /// <summary>
        /// 创建完整认证 snapshot。
        /// </summary>
        /// <param name="account">当前账号安全摘要。</param>
        /// <param name="session">当前 session identity、epoch 与 expiry。</param>
        /// <param name="tokens">不得输出或持久化的 token pair。</param>
        /// <param name="generation">Session owner 每次替换或清理时递增的本地 generation。</param>
        internal ClientSessionSnapshot(
            ClientAccountSummary account,
            ClientSessionSummary session,
            ClientTokenPair tokens,
            long generation)
        {
            Account = account ?? throw new ArgumentNullException(nameof(account));
            Session = session ?? throw new ArgumentNullException(nameof(session));
            Tokens = tokens ?? throw new ArgumentNullException(nameof(tokens));
            Generation = generation;
        }

        /// <summary>
        /// 获取当前账号安全摘要。
        /// </summary>
        internal ClientAccountSummary Account { get; }

        /// <summary>
        /// 获取当前服务端 session identity 与 epoch。
        /// </summary>
        internal ClientSessionSummary Session { get; }

        /// <summary>
        /// 获取只能由 Session owner 借给强类型 HTTP operation 的 token pair。
        /// </summary>
        internal ClientTokenPair Tokens { get; }

        /// <summary>
        /// 获取防止旧 async completion 覆盖新 session 的本地 generation。
        /// </summary>
        internal long Generation { get; }

        /// <summary>
        /// 返回不包含 token 或不可信文本标识的安全 session correlation。
        /// </summary>
        /// <returns>数值 session epoch 与本地 generation。</returns>
        public override string ToString()
        {
            return $"ClientSessionSnapshot sessionEpoch={Session.SessionEpoch} generation={Generation}";
        }
    }

    /// <summary>
    /// 保存来源 session generation 与单次交付状态的 connection ticket lease。
    /// </summary>
    internal sealed class ClientConnectionTicketLease
    {
        /// <summary>
        /// 保存不得输出的完整 HTTP ticket 响应。
        /// </summary>
        private readonly ClientConnectionTicket _ticket;

        /// <summary>
        /// 保存签发时 Session owner 的 generation，轮换后立即失效。
        /// </summary>
        private readonly long _sourceGeneration;

        /// <summary>
        /// 使用原子位保证 credential 最多成功交付一次。
        /// </summary>
        private int _taken;

        /// <summary>
        /// 创建绑定当前 session generation 的短期 lease。
        /// </summary>
        /// <param name="ticket">服务端签发的完整 ticket 响应。</param>
        /// <param name="sourceGeneration">签发调用捕获的 current session generation。</param>
        internal ClientConnectionTicketLease(
            ClientConnectionTicket ticket,
            long sourceGeneration)
        {
            _ticket = ticket ?? throw new ArgumentNullException(nameof(ticket));
            _sourceGeneration = sourceGeneration;
        }

        /// <summary>
        /// 获取 ticket 绑定且不得由后续 payload 覆盖的 endpoint。
        /// </summary>
        internal ClientEndpoint Endpoint => _ticket.Endpoint;

        /// <summary>
        /// 获取服务端授予的不可变 scope 集合。
        /// </summary>
        internal IReadOnlyList<ClientConnectionScope> Scopes => _ticket.Scopes;

        /// <summary>
        /// 获取 ticket 绝对 Unix expiry，单位为毫秒。
        /// </summary>
        internal long ExpiresAtMilliseconds => _ticket.ExpiresAtMilliseconds;

        /// <summary>
        /// 在 current generation 与 expiry 同时有效时原子取得 credential。
        /// </summary>
        /// <param name="currentGeneration">Session owner 锁内读取的 current generation。</param>
        /// <param name="utcNowMilliseconds">当前 Unix 时间，单位为毫秒。</param>
        /// <param name="ticketUse">成功时返回唯一 credential 所有权。</param>
        /// <returns>Generation、expiry 与单次交付均有效时返回 true。</returns>
        internal bool TryTake(
            long currentGeneration,
            long utcNowMilliseconds,
            out ClientConnectionTicketUse ticketUse)
        {
            ticketUse = null;
            if (currentGeneration != _sourceGeneration ||
                utcNowMilliseconds >= _ticket.ExpiresAtMilliseconds)
            {
                return false;
            }

            if (Interlocked.CompareExchange(ref _taken, 1, 0) != 0)
            {
                return false;
            }

            ticketUse = new ClientConnectionTicketUse(
                _ticket.Ticket,
                _ticket.Endpoint,
                _ticket.Scopes,
                _ticket.ExpiresAtMilliseconds,
                _sourceGeneration);
            return true;
        }

        /// <summary>
        /// 返回固定脱敏文本，防止 lease 被日志插值时输出 ticket。
        /// </summary>
        /// <returns>只包含 endpoint 与 expiry 的安全摘要。</returns>
        public override string ToString()
        {
            return $"ClientConnectionTicketLease[REDACTED] channel={Endpoint.Channel} expiresAtMs={ExpiresAtMilliseconds}";
        }
    }

    /// <summary>
    /// 表示已经从 lease 单次取得、等待交给匹配 channel 的 ticket credential。
    /// </summary>
    internal sealed class ClientConnectionTicketUse
    {
        /// <summary>
        /// 创建由后续 channel 临时拥有的 ticket 使用权。
        /// </summary>
        /// <param name="credential">不得输出或复用的 opaque ticket。</param>
        /// <param name="endpoint">Credential 绑定 endpoint。</param>
        /// <param name="scopes">Credential 固定 scope。</param>
        /// <param name="expiresAtMilliseconds">Ticket 绝对 Unix expiry，单位为毫秒。</param>
        /// <param name="sourceGeneration">签发与单次取得时仍 current 的本地 session generation。</param>
        internal ClientConnectionTicketUse(
            string credential,
            ClientEndpoint endpoint,
            IReadOnlyList<ClientConnectionScope> scopes,
            long expiresAtMilliseconds,
            long sourceGeneration)
        {
            Credential = credential ?? throw new ArgumentNullException(nameof(credential));
            Endpoint = endpoint ?? throw new ArgumentNullException(nameof(endpoint));
            Scopes = scopes ?? throw new ArgumentNullException(nameof(scopes));
            ExpiresAtMilliseconds = expiresAtMilliseconds;
            SourceGeneration = sourceGeneration;
        }

        /// <summary>
        /// 获取只能交给匹配 channel authentication 的 opaque ticket。
        /// </summary>
        internal string Credential { get; }

        /// <summary>
        /// 获取 ticket 绑定 endpoint。
        /// </summary>
        internal ClientEndpoint Endpoint { get; }

        /// <summary>
        /// 获取 ticket 固定 scope。
        /// </summary>
        internal IReadOnlyList<ClientConnectionScope> Scopes { get; }

        /// <summary>
        /// 获取 ticket 绝对 Unix expiry，单位为毫秒。
        /// </summary>
        internal long ExpiresAtMilliseconds { get; }

        /// <summary>
        /// 获取连接结果与 control invalidation 用于阻断旧 connection callback 的来源 generation。
        /// </summary>
        internal long SourceGeneration { get; }

        /// <summary>
        /// 返回固定脱敏文本，防止 channel 诊断输出 credential。
        /// </summary>
        /// <returns>不包含 ticket 的 endpoint 与 expiry 摘要。</returns>
        public override string ToString()
        {
            return $"ClientConnectionTicketUse[REDACTED] channel={Endpoint.Channel} expiresAtMs={ExpiresAtMilliseconds}";
        }
    }

    /// <summary>
    /// 保存来源 session generation 与单次交付状态的 world admission lease。
    /// </summary>
    internal sealed class ClientWorldAdmissionLease
    {
        /// <summary>保存不得输出的完整 HTTP admission 响应。</summary>
        private readonly ClientWorldAdmission _admission;

        /// <summary>保存签发时 Session owner 的 generation。</summary>
        private readonly long _sourceGeneration;

        /// <summary>使用原子位保证 credential 最多成功交付一次。</summary>
        private int _taken;

        /// <summary>创建绑定当前 session generation 的短期 lease。</summary>
        /// <param name="admission">服务端签发的完整 admission 响应。</param>
        /// <param name="sourceGeneration">签发调用捕获的 current generation。</param>
        internal ClientWorldAdmissionLease(ClientWorldAdmission admission, long sourceGeneration)
        {
            _admission = admission ?? throw new ArgumentNullException(nameof(admission));
            _sourceGeneration = sourceGeneration;
        }

        /// <summary>获取 credential 绑定 endpoint。</summary>
        internal ClientEndpoint Endpoint => _admission.Endpoint;

        /// <summary>获取服务端权威 role。</summary>
        internal ClientWorldRole Role => _admission.Role;

        /// <summary>获取服务端权威 purpose。</summary>
        internal ClientWorldAdmissionPurpose Purpose => _admission.Purpose;

        /// <summary>获取 Visitor 首帧必须使用的权威 VisitSession revision；Owner 为零。</summary>
        internal ulong VisitRevision => _admission.VisitRevision;

        /// <summary>获取绝对 Unix expiry，单位为毫秒。</summary>
        internal long ExpiresAtMilliseconds => _admission.ExpiresAtMilliseconds;

        /// <summary>在 generation、expiry 与 admission contract 有效时原子取得 credential。</summary>
        /// <param name="currentGeneration">Session owner 当前 generation。</param>
        /// <param name="utcNowMilliseconds">当前 Unix 时间，单位为毫秒。</param>
        /// <param name="admissionUse">成功时返回唯一 credential 使用权。</param>
        /// <returns>Generation、expiry、binding 与单次交付均有效时返回 true。</returns>
        internal bool TryTake(
            long currentGeneration,
            long utcNowMilliseconds,
            out ClientWorldAdmissionUse admissionUse)
        {
            admissionUse = null;
            if (currentGeneration != _sourceGeneration ||
                utcNowMilliseconds >= _admission.ExpiresAtMilliseconds ||
                !IsDeliverable(_admission))
            {
                return false;
            }

            if (Interlocked.CompareExchange(ref _taken, 1, 0) != 0)
            {
                return false;
            }

            admissionUse = new ClientWorldAdmissionUse(
                _admission.Credential,
                _admission.Endpoint,
                _admission.Role,
                _admission.Purpose,
                _admission.ExpiresAtMilliseconds,
                _sourceGeneration);
            return true;
        }

        /// <summary>在 credential 离开 Session owner 前防御性复核 opaque grammar 与 role/purpose binding。</summary>
        /// <param name="admission">HTTP codec 已解析的不可变 admission。</param>
        /// <returns>Credential、endpoint 与封闭角色用途均仍有效时返回 true。</returns>
        private static bool IsDeliverable(ClientWorldAdmission admission)
        {
            if (admission.Endpoint.Channel != ClientEndpointChannel.TlsTcp ||
                admission.Credential.Length < 32 || admission.Credential.Length > 4096)
            {
                return false;
            }

            foreach (var character in admission.Credential)
            {
                var isAsciiLetterOrDigit =
                    (character >= 'A' && character <= 'Z') ||
                    (character >= 'a' && character <= 'z') ||
                    (character >= '0' && character <= '9');
                if (!isAsciiLetterOrDigit &&
                    character != '.' && character != '_' && character != '~' && character != '-')
                {
                    return false;
                }
            }

            return admission.Purpose == ClientWorldAdmissionPurpose.OwnWorld
                ? admission.Role == ClientWorldRole.Owner && admission.VisitRevision == 0
                : admission.Role == ClientWorldRole.Visitor &&
                  admission.VisitRevision > 0 &&
                  (admission.Purpose == ClientWorldAdmissionPurpose.Join ||
                   admission.Purpose == ClientWorldAdmissionPurpose.Reconnect);
        }

        /// <summary>返回固定脱敏摘要。</summary>
        /// <returns>不包含 admission credential 的 purpose 与 expiry。</returns>
        public override string ToString()
        {
            return $"ClientWorldAdmissionLease[REDACTED] purpose={Purpose} expiresAtMs={ExpiresAtMilliseconds}";
        }
    }

    /// <summary>
    /// 表示已经从 lease 单次取得、等待交给 gameplay channel 的 admission。
    /// </summary>
    internal sealed class ClientWorldAdmissionUse
    {
        /// <summary>创建唯一 admission 使用权。</summary>
        /// <param name="credential">不得输出或复用的 opaque credential。</param>
        /// <param name="endpoint">Credential 绑定 endpoint。</param>
        /// <param name="role">Credential 绑定角色。</param>
        /// <param name="purpose">Credential 绑定用途。</param>
        /// <param name="expiresAtMilliseconds">绝对 Unix expiry，单位为毫秒。</param>
        /// <param name="sourceGeneration">来源 session generation。</param>
        internal ClientWorldAdmissionUse(
            string credential,
            ClientEndpoint endpoint,
            ClientWorldRole role,
            ClientWorldAdmissionPurpose purpose,
            long expiresAtMilliseconds,
            long sourceGeneration)
        {
            Credential = credential ?? throw new ArgumentNullException(nameof(credential));
            Endpoint = endpoint ?? throw new ArgumentNullException(nameof(endpoint));
            Role = role;
            Purpose = purpose;
            ExpiresAtMilliseconds = expiresAtMilliseconds;
            SourceGeneration = sourceGeneration;
        }

        /// <summary>获取只能交给 gameplay authentication 的 opaque credential。</summary>
        internal string Credential { get; }

        /// <summary>获取 credential 绑定 endpoint。</summary>
        internal ClientEndpoint Endpoint { get; }

        /// <summary>获取 credential 绑定角色。</summary>
        internal ClientWorldRole Role { get; }

        /// <summary>获取 credential 绑定用途。</summary>
        internal ClientWorldAdmissionPurpose Purpose { get; }

        /// <summary>获取绝对 Unix expiry，单位为毫秒。</summary>
        internal long ExpiresAtMilliseconds { get; }

        /// <summary>获取来源 session generation。</summary>
        internal long SourceGeneration { get; }

        /// <summary>返回固定脱敏摘要。</summary>
        /// <returns>不包含 credential 的 purpose 与 expiry。</returns>
        public override string ToString()
        {
            return $"ClientWorldAdmissionUse[REDACTED] purpose={Purpose} expiresAtMs={ExpiresAtMilliseconds}";
        }
    }
}
