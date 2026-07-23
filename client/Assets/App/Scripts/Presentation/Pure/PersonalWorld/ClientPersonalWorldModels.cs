using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.Threading;
using System.Threading.Tasks;

namespace IHomeland.Client.Presentation.PersonalWorld
{
    /// <summary>
    /// 标识个人世界首个产品竖切当前可展示的纯封闭阶段。
    /// </summary>
    internal enum ClientPersonalWorldPhase
    {
        /// <summary>Experience 尚未完成 App Scope 初始化。</summary>
        Inactive = 0,

        /// <summary>正在一次性读取并轮换安全保存的Session lineage。</summary>
        RestoringSession = 1,

        /// <summary>只显示本地登录页，尚未产生网络副作用。</summary>
        Login = 2,

        /// <summary>正在执行显式 bootstrap 与认证。</summary>
        Authenticating = 3,

        /// <summary>正在解析、连接或加载自己的 PersonalWorld。</summary>
        EnteringOwnWorld = 4,

        /// <summary>当前内容场景承载自己的 PersonalWorld。</summary>
        OwnWorld = 5,

        /// <summary>正在接受定向邀请并切换到 Visitor target。</summary>
        JoiningVisit = 6,

        /// <summary>当前内容场景承载受控 Visitor target。</summary>
        Visiting = 7,

        /// <summary>旧 Visitor target 已失效，正在安全返回自己的世界。</summary>
        ReturningOwnWorld = 8,

        /// <summary>WSS正在有限恢复且依赖control完整性的动作已冻结。</summary>
        RecoveringControl = 9,

        /// <summary>Gameplay旧target已失效且正在权威重建。</summary>
        RecoveringWorld = 10,

        /// <summary>网络target已提交，等待Scene与HUD确认同一generation。</summary>
        AwaitingScene = 11,

        /// <summary>连接中断且需要玩家显式重试或退出。</summary>
        ConnectionLost = 12,

        /// <summary>App Scope 已停止，拒绝全部迟到提交。</summary>
        Stopped = 13,
    }

    /// <summary>
    /// 标识允许呈现给玩家的低敏失败类别。
    /// </summary>
    internal enum ClientPersonalWorldFailure
    {
        /// <summary>尚无失败。</summary>
        None = 0,

        /// <summary>本地输入不满足公开格式约束。</summary>
        Validation = 1,

        /// <summary>当前身份、角色或阶段不允许该动作。</summary>
        Permission = 2,

        /// <summary>当前客户端与服务端协议不兼容。</summary>
        ProtocolIncompatible = 3,

        /// <summary>Session 已失效，需要重新登录。</summary>
        Unauthenticated = 4,

        /// <summary>权威 revision 已前进，需要刷新后重试。</summary>
        RevisionConflict = 5,

        /// <summary>目标世界、访问会话或邀请不存在。</summary>
        NotFound = 6,

        /// <summary>服务端限流，当前动作暂不可提交。</summary>
        RateLimited = 7,

        /// <summary>依赖服务暂不可用。</summary>
        DependencyUnavailable = 8,

        /// <summary>网络或 operation deadline 失败。</summary>
        Transport = 9,

        /// <summary>请求可能已提交，客户端无法判定结果。</summary>
        CommitUnknown = 10,

        /// <summary>调用方主动取消当前等待。</summary>
        CallerCancelled = 11,

        /// <summary>未分类内部失败；不得附带异常或敏感文本。</summary>
        Internal = 12,

        /// <summary>App Scope 已停止。</summary>
        Stopped = 13,

        /// <summary>选中的邀请已被撤销、过期、消费或由终态统一退役。</summary>
        InviteUnavailable = 14,

        /// <summary>平台安全存储不可访问或无法原子提交。</summary>
        SecureStorage = 15,

        /// <summary>另一客户端进程正在使用同一本地登录profile。</summary>
        ProfileInUse = 16,
    }

    /// <summary>
    /// 标识用于 single-flight 与按钮禁用状态的语义动作类别。
    /// </summary>
    internal enum ClientPersonalWorldIntent
    {
        /// <summary>当前没有活动动作。</summary>
        None = 0,

        /// <summary>创建账号并建立 Session。</summary>
        Register = 1,

        /// <summary>登录并建立 Session。</summary>
        Login = 2,

        /// <summary>解析或重试进入自己的世界。</summary>
        EnterOwnWorld = 3,

        /// <summary>打开 Owner VisitSession。</summary>
        OpenVisit = 4,

        /// <summary>创建定向邀请。</summary>
        CreateInvite = 5,

        /// <summary>撤销定向邀请。</summary>
        RevokeInvite = 6,

        /// <summary>接受邀请并进入 Visitor target。</summary>
        AcceptInvite = 7,

        /// <summary>Owner 移除指定 Visitor。</summary>
        KickVisitor = 8,

        /// <summary>Owner 关闭当前 VisitSession。</summary>
        CloseVisit = 9,

        /// <summary>Visitor 主动离开当前 target。</summary>
        LeaveVisit = 10,

        /// <summary>重试权威安全返回。</summary>
        RetryReturn = 11,

        /// <summary>显式重启已经进入稳定断开状态的 control run。</summary>
        Reconnect = 12,

        /// <summary>退出并清理当前 Session。</summary>
        Logout = 13,
    }

    /// <summary>
    /// 保存一次语义动作的稳定低敏结果。
    /// </summary>
    internal readonly struct ClientPersonalWorldActionResult
    {
        /// <summary>创建语义动作结果。</summary>
        /// <param name="succeeded">动作是否完成并提交。</param>
        /// <param name="failure">失败时的封闭低敏类别。</param>
        private ClientPersonalWorldActionResult(bool succeeded, ClientPersonalWorldFailure failure)
        {
            Succeeded = succeeded;
            Failure = failure;
        }

        /// <summary>获取动作是否完成并提交。</summary>
        internal bool Succeeded { get; }

        /// <summary>获取失败时的封闭低敏类别。</summary>
        internal ClientPersonalWorldFailure Failure { get; }

        /// <summary>创建成功结果。</summary>
        /// <returns>不携带业务事实的成功结果。</returns>
        internal static ClientPersonalWorldActionResult Success()
        {
            return new ClientPersonalWorldActionResult(true, ClientPersonalWorldFailure.None);
        }

        /// <summary>创建失败结果。</summary>
        /// <param name="failure">非 None 的稳定失败类别。</param>
        /// <returns>不携带异常或敏感文本的失败结果。</returns>
        /// <exception cref="ArgumentOutOfRangeException">失败类别未登记或为 None 时抛出。</exception>
        internal static ClientPersonalWorldActionResult Failed(ClientPersonalWorldFailure failure)
        {
            if (failure == ClientPersonalWorldFailure.None ||
                !Enum.IsDefined(typeof(ClientPersonalWorldFailure), failure))
            {
                throw new ArgumentOutOfRangeException(nameof(failure), failure, "Action failure 必须是登记的非空类别。");
            }

            return new ClientPersonalWorldActionResult(false, failure);
        }
    }

    /// <summary>
    /// 保存登录页需要的不可变低敏切片；password 永远不进入该模型。
    /// </summary>
    internal sealed class ClientLoginViewState
    {
        /// <summary>创建登录页状态。</summary>
        /// <param name="visible">页面是否应当显示。</param>
        /// <param name="busy">认证动作是否正在执行。</param>
        /// <param name="failure">最近低敏失败类别。</param>
        internal ClientLoginViewState(bool visible, bool busy, ClientPersonalWorldFailure failure)
        {
            Visible = visible;
            Busy = busy;
            Failure = failure;
        }

        /// <summary>获取页面是否应当显示。</summary>
        internal bool Visible { get; }

        /// <summary>获取认证控件是否应当禁用。</summary>
        internal bool Busy { get; }

        /// <summary>获取最近低敏失败类别。</summary>
        internal ClientPersonalWorldFailure Failure { get; }
    }

    /// <summary>
    /// 保存持久 Shell 需要的账号与 world-flow 低敏切片。
    /// </summary>
    internal sealed class ClientShellViewState
    {
        /// <summary>创建 Shell 状态。</summary>
        /// <param name="visible">Shell 是否应当显示。</param>
        /// <param name="playerID">认证玩家标识；未认证时为空。</param>
        /// <param name="displayName">服务端规范化显示名；未认证时为空。</param>
        /// <param name="phase">当前产品阶段。</param>
        /// <param name="failure">最近低敏失败类别。</param>
        internal ClientShellViewState(
            bool visible,
            string playerID,
            string displayName,
            ClientPersonalWorldPhase phase,
            ClientPersonalWorldFailure failure)
        {
            Visible = visible;
            PlayerID = playerID ?? string.Empty;
            DisplayName = displayName ?? string.Empty;
            Phase = phase;
            Failure = failure;
        }

        /// <summary>获取 Shell 是否应当显示。</summary>
        internal bool Visible { get; }

        /// <summary>获取认证玩家标识。</summary>
        internal string PlayerID { get; }

        /// <summary>获取服务端规范化显示名。</summary>
        internal string DisplayName { get; }

        /// <summary>获取当前产品阶段。</summary>
        internal ClientPersonalWorldPhase Phase { get; }

        /// <summary>获取最近低敏失败类别。</summary>
        internal ClientPersonalWorldFailure Failure { get; }
    }

    /// <summary>
    /// 保存单个定向邀请的不可变页面摘要。
    /// </summary>
    internal sealed class ClientVisitInviteViewState
    {
        /// <summary>创建邀请页面摘要。</summary>
        /// <param name="inviteID">邀请标识。</param>
        /// <param name="visitSessionID">所属访问会话标识。</param>
        /// <param name="ownerPlayerID">Owner 玩家标识。</param>
        /// <param name="targetVisitorID">定向 Visitor 玩家标识。</param>
        /// <param name="expiresAtMilliseconds">邀请 Unix 到期时间，单位为毫秒。</param>
        internal ClientVisitInviteViewState(
            string inviteID,
            string visitSessionID,
            string ownerPlayerID,
            string targetVisitorID,
            long expiresAtMilliseconds)
        {
            InviteID = inviteID ?? throw new ArgumentNullException(nameof(inviteID));
            VisitSessionID = visitSessionID ?? throw new ArgumentNullException(nameof(visitSessionID));
            OwnerPlayerID = ownerPlayerID ?? throw new ArgumentNullException(nameof(ownerPlayerID));
            TargetVisitorID = targetVisitorID ?? throw new ArgumentNullException(nameof(targetVisitorID));
            ExpiresAtMilliseconds = expiresAtMilliseconds;
        }

        /// <summary>获取邀请标识。</summary>
        internal string InviteID { get; }

        /// <summary>获取所属访问会话标识。</summary>
        internal string VisitSessionID { get; }

        /// <summary>获取 Owner 玩家标识。</summary>
        internal string OwnerPlayerID { get; }

        /// <summary>获取定向 Visitor 玩家标识。</summary>
        internal string TargetVisitorID { get; }

        /// <summary>获取邀请 Unix 到期时间，单位为毫秒。</summary>
        internal long ExpiresAtMilliseconds { get; }
    }

    /// <summary>保存由当前权威 target、role、lifecycle 与 single-flight 派生的页面动作能力。</summary>
    internal sealed class ClientWorldVisitActionState
    {
        /// <summary>创建封闭的 WorldVisit 动作能力集合。</summary>
        /// <param name="canOpenVisit">是否可以开放或解析 Owner VisitSession。</param>
        /// <param name="canCreateInvite">是否可以为目标玩家创建邀请。</param>
        /// <param name="canRevokeInvite">是否可以撤销当前集合中的邀请。</param>
        /// <param name="canKickVisitor">是否可以移除当前集合中的 Visitor。</param>
        /// <param name="canCloseVisit">是否可以关闭 active Owner VisitSession。</param>
        /// <param name="canAcceptInvite">是否可以接受当前 inbox 中的邀请。</param>
        /// <param name="canLeaveVisit">是否可以离开 current Visitor target。</param>
        internal ClientWorldVisitActionState(
            bool canOpenVisit,
            bool canCreateInvite,
            bool canRevokeInvite,
            bool canKickVisitor,
            bool canCloseVisit,
            bool canAcceptInvite,
            bool canLeaveVisit)
        {
            CanOpenVisit = canOpenVisit;
            CanCreateInvite = canCreateInvite;
            CanRevokeInvite = canRevokeInvite;
            CanKickVisitor = canKickVisitor;
            CanCloseVisit = canCloseVisit;
            CanAcceptInvite = canAcceptInvite;
            CanLeaveVisit = canLeaveVisit;
        }

        /// <summary>获取是否可以开放 Owner VisitSession。</summary>
        internal bool CanOpenVisit { get; }

        /// <summary>获取是否可以创建定向邀请。</summary>
        internal bool CanCreateInvite { get; }

        /// <summary>获取是否存在可撤销邀请。</summary>
        internal bool CanRevokeInvite { get; }

        /// <summary>获取是否存在可移除 Visitor。</summary>
        internal bool CanKickVisitor { get; }

        /// <summary>获取是否可以关闭 active Owner VisitSession。</summary>
        internal bool CanCloseVisit { get; }

        /// <summary>获取是否存在可接受 inbox invite。</summary>
        internal bool CanAcceptInvite { get; }

        /// <summary>获取是否可以离开 current Visitor target。</summary>
        internal bool CanLeaveVisit { get; }

        /// <summary>获取全部动作均禁用的稳定空能力。</summary>
        internal static ClientWorldVisitActionState None { get; } =
            new ClientWorldVisitActionState(false, false, false, false, false, false, false);
    }

    /// <summary>
    /// 保存 VisitSession 页面需要的不可变投影切片。
    /// </summary>
    internal sealed class ClientWorldVisitViewState
    {
        /// <summary>创建 VisitSession 页面状态。</summary>
        /// <param name="visible">页面是否应当显示。</param>
        /// <param name="isOwner">当前权威角色是否为 Owner。</param>
        /// <param name="isVisitor">当前权威角色是否为 Visitor。</param>
        /// <param name="visitSessionID">当前访问会话标识。</param>
        /// <param name="revision">当前权威 aggregate revision。</param>
        /// <param name="expiresAtMilliseconds">会话 Unix 到期时间，单位为毫秒。</param>
        /// <param name="ownerGraceExpiresAtMilliseconds">Owner grace Unix 到期时间，单位为毫秒。</param>
        /// <param name="visitorPlayerIDs">按稳定顺序排列的 Visitor 玩家标识。</param>
        /// <param name="invites">按稳定顺序排列的有效邀请。</param>
        /// <param name="actions">由权威状态派生的动作能力。</param>
        internal ClientWorldVisitViewState(
            bool visible,
            bool isOwner,
            bool isVisitor,
            string visitSessionID,
            ulong revision,
            long expiresAtMilliseconds,
            long ownerGraceExpiresAtMilliseconds,
            IReadOnlyList<string> visitorPlayerIDs,
            IReadOnlyList<ClientVisitInviteViewState> invites,
            ClientWorldVisitActionState actions)
        {
            Visible = visible;
            IsOwner = isOwner;
            IsVisitor = isVisitor;
            VisitSessionID = visitSessionID ?? string.Empty;
            Revision = revision;
            ExpiresAtMilliseconds = expiresAtMilliseconds;
            OwnerGraceExpiresAtMilliseconds = ownerGraceExpiresAtMilliseconds;
            VisitorPlayerIDs = Copy(visitorPlayerIDs, nameof(visitorPlayerIDs));
            Invites = Copy(invites, nameof(invites));
            Actions = actions ?? throw new ArgumentNullException(nameof(actions));
        }

        /// <summary>获取页面是否应当显示。</summary>
        internal bool Visible { get; }

        /// <summary>获取当前权威角色是否为 Owner。</summary>
        internal bool IsOwner { get; }

        /// <summary>获取当前权威角色是否为 Visitor。</summary>
        internal bool IsVisitor { get; }

        /// <summary>获取当前访问会话标识。</summary>
        internal string VisitSessionID { get; }

        /// <summary>获取当前权威 aggregate revision。</summary>
        internal ulong Revision { get; }

        /// <summary>获取会话 Unix 到期时间，单位为毫秒。</summary>
        internal long ExpiresAtMilliseconds { get; }

        /// <summary>获取 Owner grace Unix 到期时间，单位为毫秒。</summary>
        internal long OwnerGraceExpiresAtMilliseconds { get; }

        /// <summary>获取按稳定顺序排列的 Visitor 玩家标识。</summary>
        internal IReadOnlyList<string> VisitorPlayerIDs { get; }

        /// <summary>获取按稳定顺序排列的有效邀请。</summary>
        internal IReadOnlyList<ClientVisitInviteViewState> Invites { get; }

        /// <summary>获取不依赖 View 猜测的当前动作能力。</summary>
        internal ClientWorldVisitActionState Actions { get; }

        /// <summary>防御性复制页面集合，阻止 View 改写共享状态。</summary>
        /// <typeparam name="T">不可变页面元素类型。</typeparam>
        /// <param name="source">待复制集合。</param>
        /// <param name="parameterName">异常中使用的参数名。</param>
        /// <returns>只读集合快照。</returns>
        private static IReadOnlyList<T> Copy<T>(IReadOnlyList<T> source, string parameterName)
        {
            if (source == null)
            {
                throw new ArgumentNullException(parameterName);
            }

            var copy = new T[source.Count];
            for (var index = 0; index < source.Count; index++)
            {
                copy[index] = source[index];
                if (ReferenceEquals(copy[index], null))
                {
                    throw new ArgumentNullException(parameterName, $"页面集合索引 {index} 不能为空。");
                }
            }

            return new ReadOnlyCollection<T>(copy);
        }
    }

    /// <summary>
    /// 保存场景 HUD 只读展示需要的无 credential 切片。
    /// </summary>
    internal sealed class ClientWorldHudViewState
    {
        /// <summary>创建 HUD 状态。</summary>
        /// <param name="visible">HUD 是否应当显示。</param>
        /// <param name="isOwner">当前是否为 Owner。</param>
        /// <param name="isVisitor">当前是否为 Visitor。</param>
        /// <param name="personalWorldID">当前 PersonalWorld 标识。</param>
        /// <param name="worldInstanceID">当前 WorldInstance 标识。</param>
        /// <param name="visitSessionID">Visitor 或已打开 Owner 会话标识。</param>
        internal ClientWorldHudViewState(
            bool visible,
            bool isOwner,
            bool isVisitor,
            string personalWorldID,
            string worldInstanceID,
            string visitSessionID)
        {
            Visible = visible;
            IsOwner = isOwner;
            IsVisitor = isVisitor;
            PersonalWorldID = personalWorldID ?? string.Empty;
            WorldInstanceID = worldInstanceID ?? string.Empty;
            VisitSessionID = visitSessionID ?? string.Empty;
        }

        /// <summary>获取 HUD 是否应当显示。</summary>
        internal bool Visible { get; }

        /// <summary>获取当前是否为 Owner。</summary>
        internal bool IsOwner { get; }

        /// <summary>获取当前是否为 Visitor。</summary>
        internal bool IsVisitor { get; }

        /// <summary>获取当前 PersonalWorld 标识。</summary>
        internal string PersonalWorldID { get; }

        /// <summary>获取当前 WorldInstance 标识。</summary>
        internal string WorldInstanceID { get; }

        /// <summary>获取 Visitor 或已打开 Owner 会话标识。</summary>
        internal string VisitSessionID { get; }
    }

    /// <summary>
    /// 保存 Experience 对页面原子发布的完整不可变低敏状态。
    /// </summary>
    internal sealed class ClientPersonalWorldViewState
    {
        /// <summary>创建完整页面状态。</summary>
        /// <param name="presentationGeneration">阻止旧页面回写的表现代际。</param>
        /// <param name="targetGeneration">来自权威 world flow 的目标代际。</param>
        /// <param name="sceneGeneration">已提交内容场景代际；未提交时为 0。</param>
        /// <param name="phase">当前产品阶段。</param>
        /// <param name="activeIntent">当前 single-flight 动作。</param>
        /// <param name="login">登录页切片。</param>
        /// <param name="shell">Shell 切片。</param>
        /// <param name="worldVisit">访问页切片。</param>
        /// <param name="worldHud">HUD 切片。</param>
        internal ClientPersonalWorldViewState(
            long presentationGeneration,
            long targetGeneration,
            long sceneGeneration,
            ClientPersonalWorldPhase phase,
            ClientPersonalWorldIntent activeIntent,
            ClientLoginViewState login,
            ClientShellViewState shell,
            ClientWorldVisitViewState worldVisit,
            ClientWorldHudViewState worldHud)
        {
            PresentationGeneration = presentationGeneration;
            TargetGeneration = targetGeneration;
            SceneGeneration = sceneGeneration;
            Phase = phase;
            ActiveIntent = activeIntent;
            Login = login ?? throw new ArgumentNullException(nameof(login));
            Shell = shell ?? throw new ArgumentNullException(nameof(shell));
            WorldVisit = worldVisit ?? throw new ArgumentNullException(nameof(worldVisit));
            WorldHud = worldHud ?? throw new ArgumentNullException(nameof(worldHud));
        }

        /// <summary>获取阻止旧页面回写的表现代际。</summary>
        internal long PresentationGeneration { get; }

        /// <summary>获取来自权威 world flow 的目标代际。</summary>
        internal long TargetGeneration { get; }

        /// <summary>获取已提交内容场景代际；未提交时为 0。</summary>
        internal long SceneGeneration { get; }

        /// <summary>获取当前产品阶段。</summary>
        internal ClientPersonalWorldPhase Phase { get; }

        /// <summary>获取当前 single-flight 动作。</summary>
        internal ClientPersonalWorldIntent ActiveIntent { get; }

        /// <summary>获取登录页切片。</summary>
        internal ClientLoginViewState Login { get; }

        /// <summary>获取 Shell 切片。</summary>
        internal ClientShellViewState Shell { get; }

        /// <summary>获取访问页切片。</summary>
        internal ClientWorldVisitViewState WorldVisit { get; }

        /// <summary>获取 HUD 切片。</summary>
        internal ClientWorldHudViewState WorldHud { get; }

        /// <summary>获取完全未启动且不显示任何页面的基线状态。</summary>
        internal static ClientPersonalWorldViewState Inactive { get; } = new ClientPersonalWorldViewState(
            0,
            0,
            0,
            ClientPersonalWorldPhase.Inactive,
            ClientPersonalWorldIntent.None,
            new ClientLoginViewState(false, false, ClientPersonalWorldFailure.None),
            new ClientShellViewState(false, string.Empty, string.Empty, ClientPersonalWorldPhase.Inactive, ClientPersonalWorldFailure.None),
            new ClientWorldVisitViewState(false, false, false, string.Empty, 0, 0, 0, Array.Empty<string>(), Array.Empty<ClientVisitInviteViewState>(), ClientWorldVisitActionState.None),
            new ClientWorldHudViewState(false, false, false, string.Empty, string.Empty, string.Empty));
    }

    /// <summary>
    /// 定义登录页允许提交的窄语义动作。
    /// </summary>
    internal interface IClientLoginActions
    {
        /// <summary>创建账号并进入自己的 PersonalWorld。</summary>
        /// <param name="username">待服务端校验的账号名。</param>
        /// <param name="password">仅当前调用临时持有的原始 password。</param>
        /// <param name="displayName">待服务端规范化的显示名。</param>
        /// <param name="cancellationToken">页面隐藏或调用方取消等待的信号。</param>
        /// <returns>不包含 credential 的稳定结果。</returns>
        Task<ClientPersonalWorldActionResult> RegisterAsync(
            string username,
            string password,
            string displayName,
            CancellationToken cancellationToken);

        /// <summary>登录并进入自己的 PersonalWorld。</summary>
        /// <param name="username">待服务端校验的账号名。</param>
        /// <param name="password">仅当前调用临时持有的原始 password。</param>
        /// <param name="cancellationToken">页面隐藏或调用方取消等待的信号。</param>
        /// <returns>不包含 credential 的稳定结果。</returns>
        Task<ClientPersonalWorldActionResult> LoginAsync(
            string username,
            string password,
            CancellationToken cancellationToken);
    }

    /// <summary>
    /// 定义 Shell 允许提交的窄语义动作。
    /// </summary>
    internal interface IClientShellActions
    {
        /// <summary>重试进入自己的 PersonalWorld。</summary>
        /// <param name="cancellationToken">页面隐藏或调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        Task<ClientPersonalWorldActionResult> RetryEnterOwnWorldAsync(CancellationToken cancellationToken);

        /// <summary>重试权威安全返回。</summary>
        /// <param name="cancellationToken">页面隐藏或调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        Task<ClientPersonalWorldActionResult> RetryReturnAsync(CancellationToken cancellationToken);

        /// <summary>重试已经耗尽有限恢复预算的 control connection。</summary>
        /// <param name="cancellationToken">页面隐藏或调用方取消等待的信号。</param>
        /// <returns>连接重新进入 Connected 或稳定失败时的低敏结果。</returns>
        Task<ClientPersonalWorldActionResult> RetryConnectionAsync(CancellationToken cancellationToken);

        /// <summary>退出并清理当前 Session。</summary>
        /// <param name="cancellationToken">页面隐藏或调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        Task<ClientPersonalWorldActionResult> LogoutAsync(CancellationToken cancellationToken);
    }

    /// <summary>
    /// 定义访问页允许提交的窄 Owner/Visitor 语义动作。
    /// </summary>
    internal interface IClientWorldVisitActions
    {
        /// <summary>打开 Owner VisitSession。</summary>
        /// <param name="cancellationToken">页面隐藏或调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        Task<ClientPersonalWorldActionResult> OpenVisitAsync(CancellationToken cancellationToken);

        /// <summary>为指定玩家创建定向邀请。</summary>
        /// <param name="targetVisitorID">目标 Visitor 玩家标识。</param>
        /// <param name="cancellationToken">页面隐藏或调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        Task<ClientPersonalWorldActionResult> CreateInviteAsync(
            string targetVisitorID,
            CancellationToken cancellationToken);

        /// <summary>撤销指定邀请。</summary>
        /// <param name="inviteID">待撤销邀请标识。</param>
        /// <param name="cancellationToken">页面隐藏或调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        Task<ClientPersonalWorldActionResult> RevokeInviteAsync(
            string inviteID,
            CancellationToken cancellationToken);

        /// <summary>接受指定邀请并加入 Visitor target。</summary>
        /// <param name="visitSessionID">邀请所属访问会话标识。</param>
        /// <param name="inviteID">待接受邀请标识。</param>
        /// <param name="cancellationToken">页面隐藏或调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        Task<ClientPersonalWorldActionResult> AcceptInviteAsync(
            string visitSessionID,
            string inviteID,
            CancellationToken cancellationToken);

        /// <summary>Owner 移除指定 Visitor。</summary>
        /// <param name="visitorPlayerID">待移除 Visitor 玩家标识。</param>
        /// <param name="cancellationToken">页面隐藏或调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        Task<ClientPersonalWorldActionResult> KickVisitorAsync(
            string visitorPlayerID,
            CancellationToken cancellationToken);

        /// <summary>Owner 关闭当前 VisitSession。</summary>
        /// <param name="cancellationToken">页面隐藏或调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        Task<ClientPersonalWorldActionResult> CloseVisitAsync(CancellationToken cancellationToken);

        /// <summary>Visitor 主动离开当前 target。</summary>
        /// <param name="cancellationToken">页面隐藏或调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        Task<ClientPersonalWorldActionResult> LeaveVisitAsync(CancellationToken cancellationToken);
    }

    /// <summary>
    /// 定义 WorldHud 允许提交的最小场景语义动作。
    /// </summary>
    internal interface IClientWorldHudActions
    {
        /// <summary>请求打开 VisitSession 产品页。</summary>
        /// <param name="cancellationToken">Scene generation 失效或调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        Task<ClientPersonalWorldActionResult> ShowWorldVisitAsync(CancellationToken cancellationToken);

        /// <summary>Visitor 主动离开当前 target。</summary>
        /// <param name="cancellationToken">Scene generation 失效或调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        Task<ClientPersonalWorldActionResult> LeaveVisitAsync(CancellationToken cancellationToken);
    }
}
