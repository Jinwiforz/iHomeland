namespace IHomeland.Client.Application.Contracts
{
    /// <summary>
    /// 提供 Application failure correlation 使用的固定 operation identity。
    /// </summary>
    internal static class ClientOperationIDs
    {
        /// <summary>版本查询。</summary>
        internal const string GetVersion = "getVersion";
        /// <summary>公开配置查询。</summary>
        internal const string GetBootstrapConfiguration = "getBootstrapConfiguration";
        /// <summary>账号注册。</summary>
        internal const string RegisterAccount = "registerAccount";
        /// <summary>账号登录。</summary>
        internal const string LoginAccount = "loginAccount";
        /// <summary>Session refresh。</summary>
        internal const string RefreshSession = "refreshSession";
        /// <summary>Session logout。</summary>
        internal const string LogoutSession = "logoutSession";
        /// <summary>Connection ticket 签发。</summary>
        internal const string IssueConnectionTicket = "issueConnectionTicket";
        /// <summary>PersonalWorld bootstrap。</summary>
        internal const string GetWorldBootstrap = "getWorldBootstrap";
        /// <summary>Visit invite 接受。</summary>
        internal const string AcceptVisitInvite = "acceptVisitInvite";
        /// <summary>World admission 签发。</summary>
        internal const string IssueWorldAdmission = "issueWorldAdmission";
    }
}
