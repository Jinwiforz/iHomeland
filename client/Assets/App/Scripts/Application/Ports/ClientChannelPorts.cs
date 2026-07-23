using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Control;
using IHomeland.Client.Application.Gameplay;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Application.World;
using IHomeland.Client.Foundation.Lifetime;

namespace IHomeland.Client.Application.Ports
{
    /// <summary>
    /// 定义 Application 可观察和运行 WSS control channel 的窄边界。
    /// </summary>
    internal interface IClientControlChannelPort : IAppLifetimeParticipant
    {
        /// <summary>在收到已校验、无 generated payload 的 typed push 后发生。</summary>
        event Action<ClientControlNotification> PushReceived;

        /// <summary>在 channel health snapshot 提交后发生。</summary>
        event Action<ClientControlHealthSnapshot> HealthChanged;

        /// <summary>获取当前低敏 channel health。</summary>
        ClientControlHealthSnapshot Snapshot { get; }

        /// <summary>显式运行当前 Session 的 control connection。</summary>
        /// <param name="cancellationToken">结束当前 run 的信号。</param>
        /// <returns>当前 run 完整结束时完成。</returns>
        Task RunAsync(CancellationToken cancellationToken);

        /// <summary>等待 current run 完成握手或进入 terminal。</summary>
        /// <param name="cancellationToken">调用方等待取消信号。</param>
        /// <returns>Current run 已连接时返回 true。</returns>
        Task<bool> WaitUntilConnectedAsync(CancellationToken cancellationToken);
    }

    /// <summary>
    /// 定义 Application 可使用的 TLS/TCP gameplay typed operations。
    /// </summary>
    /// <remarks>
    /// 该 port 不暴露 message ID、generated message、frame、socket 或泛型 send。
    /// </remarks>
    internal interface IClientGameplayChannelPort : IAppLifetimeParticipant
    {
        /// <summary>在完整 world replacement projection 到达时发生。</summary>
        event Action<ClientPersonalWorldProjection> WorldSnapshotReceived;

        /// <summary>在完整 VisitSession replacement projection 到达时发生。</summary>
        event Action<ClientVisitSessionProjection> VisitSnapshotReceived;

        /// <summary>在权威 safe-return projection 到达时发生。</summary>
        event Action<ClientSafeReturnProjection> SafeReturnReceived;

        /// <summary>在 current generation 意外终止时发生。</summary>
        event Action<ClientGameplayHealthSnapshot> UnexpectedDisconnect;

        /// <summary>获取当前低敏 connection health。</summary>
        ClientGameplayHealthSnapshot Snapshot { get; }

        /// <summary>使用当前 Session 签发的一次性 admission 建立 gameplay connection。</summary>
        /// <param name="admission">绑定 Session generation 的单次 admission lease。</param>
        /// <param name="cancellationToken">连接 deadline 与调用取消。</param>
        /// <returns>Connection 已进入 pending command 状态时返回 true。</returns>
        Task<bool> ConnectAsync(
            ClientWorldAdmissionLease admission,
            CancellationToken cancellationToken);

        /// <summary>以 Visitor JOIN 作为首 command 激活 pending connection。</summary>
        /// <param name="expectedRevision">Reservation 权威 revision。</param>
        /// <param name="cancellationToken">调用方等待取消信号。</param>
        /// <returns>完整 VisitSession candidate projection。</returns>
        Task<ClientGameplayResult<ClientVisitSessionProjection>> JoinVisitAsync(
            ulong expectedRevision,
            CancellationToken cancellationToken);

        /// <summary>以 Visitor RECONNECT 作为首 command 激活 pending connection。</summary>
        /// <param name="expectedRevision">Current membership 权威 revision。</param>
        /// <param name="cancellationToken">调用方等待取消信号。</param>
        /// <returns>完整 VisitSession candidate projection。</returns>
        Task<ClientGameplayResult<ClientVisitSessionProjection>> ReconnectVisitAsync(
            ulong expectedRevision,
            CancellationToken cancellationToken);

        /// <summary>查询 current target 的完整 PersonalWorld projection。</summary>
        /// <param name="cancellationToken">调用方等待取消信号。</param>
        /// <returns>完整 replacement projection。</returns>
        Task<ClientGameplayResult<ClientPersonalWorldProjection>> GetWorldSnapshotAsync(
            CancellationToken cancellationToken);

        /// <summary>查询 current target 的完整 VisitSession projection。</summary>
        /// <param name="role">调用方已经由 target owner 确定的角色。</param>
        /// <param name="cancellationToken">调用方等待取消信号。</param>
        /// <returns>完整 replacement projection。</returns>
        Task<ClientGameplayResult<ClientVisitSessionProjection>> GetVisitSnapshotAsync(
            ClientVisitRole role,
            CancellationToken cancellationToken);

        /// <summary>开启或读取 Owner 当前 active VisitSession。</summary>
        /// <param name="cancellationToken">调用方等待取消信号。</param>
        /// <returns>提交后的完整 VisitSession projection。</returns>
        Task<ClientGameplayResult<ClientVisitMutationCandidate>> OpenVisitAsync(
            CancellationToken cancellationToken);

        /// <summary>创建定向邀请。</summary>
        /// <param name="request">Target、lifetime 与 expected revision。</param>
        /// <param name="cancellationToken">调用方等待取消信号。</param>
        /// <returns>提交后的 VisitSession 与 invite candidate。</returns>
        Task<ClientGameplayResult<ClientVisitMutationCandidate>> CreateVisitInviteAsync(
            ClientCreateVisitInviteRequest request,
            CancellationToken cancellationToken);

        /// <summary>撤销 pending 邀请。</summary>
        /// <param name="request">Invite identity 与 expected revision。</param>
        /// <param name="cancellationToken">调用方等待取消信号。</param>
        /// <returns>提交后的 VisitSession candidate。</returns>
        Task<ClientGameplayResult<ClientVisitMutationCandidate>> RevokeVisitInviteAsync(
            ClientRevisionedIdentityRequest request,
            CancellationToken cancellationToken);

        /// <summary>移除指定 Visitor。</summary>
        /// <param name="request">Visitor identity 与 expected revision。</param>
        /// <param name="cancellationToken">调用方等待取消信号。</param>
        /// <returns>提交后的 VisitSession candidate。</returns>
        Task<ClientGameplayResult<ClientVisitMutationCandidate>> KickVisitMemberAsync(
            ClientRevisionedIdentityRequest request,
            CancellationToken cancellationToken);

        /// <summary>关闭 Owner 当前 VisitSession。</summary>
        /// <param name="expectedRevision">Current aggregate revision。</param>
        /// <param name="cancellationToken">调用方等待取消信号。</param>
        /// <returns>提交后的 terminal VisitSession candidate。</returns>
        Task<ClientGameplayResult<ClientVisitMutationCandidate>> CloseVisitAsync(
            ulong expectedRevision,
            CancellationToken cancellationToken);

        /// <summary>离开 Visitor 当前 VisitSession。</summary>
        /// <param name="expectedRevision">Current aggregate revision。</param>
        /// <param name="cancellationToken">调用方等待取消信号。</param>
        /// <returns>提交后的 VisitSession 与 safe-return candidate。</returns>
        Task<ClientGameplayResult<ClientVisitMutationCandidate>> LeaveVisitAsync(
            ulong expectedRevision,
            CancellationToken cancellationToken);

        /// <summary>使绑定旧 Session generation 的 connection 失效。</summary>
        /// <param name="sourceSessionGeneration">已失效的本地 Session generation。</param>
        void InvalidateSession(long sourceSessionGeneration);

        /// <summary>显式关闭 current connection generation。</summary>
        /// <param name="cancellationToken">关闭 deadline。</param>
        /// <returns>全部 generation 资源收敛后完成。</returns>
        Task CloseAsync(CancellationToken cancellationToken);
    }
}
