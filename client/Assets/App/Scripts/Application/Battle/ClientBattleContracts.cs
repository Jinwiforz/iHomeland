using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Contracts;
using IHomeland.Client.Foundation.Lifetime;

namespace IHomeland.Client.Application.Battle
{
    /// <summary>
    /// 标识 BattleTicket 与 connection 绑定的封闭 world target。
    /// </summary>
    internal enum ClientBattleTargetKind
    {
        /// <summary>当前玩家自己的 PersonalWorld assignment。</summary>
        OwnWorld = 1,

        /// <summary>当前玩家已获资格的 VisitSession assignment。</summary>
        VisitWorld = 2,
    }

    /// <summary>
    /// 标识 authenticated battle actor 的公开角色。
    /// </summary>
    internal enum ClientBattleRole
    {
        /// <summary>尚未由 ServerAccept 冻结角色。</summary>
        None = 0,

        /// <summary>PersonalWorld immutable Owner。</summary>
        Owner = 1,

        /// <summary>受控 VisitSession Visitor。</summary>
        Visitor = 2,
    }

    /// <summary>
    /// 保存不含 endpoint、credential、ticket 或 generated 类型的 battle target intent。
    /// </summary>
    internal sealed class ClientBattleTargetIntent
    {
        /// <summary>
        /// 初始化经过完整 world/assignment 验证的 target intent。
        /// </summary>
        /// <param name="kind">OwnWorld 或 VisitWorld。</param>
        /// <param name="sessionGeneration">统一 Session owner 的 current generation。</param>
        /// <param name="personalWorldID">非空 PersonalWorld identity。</param>
        /// <param name="worldInstanceID">不可复活的 current WorldInstance identity。</param>
        /// <param name="assignmentGeneration">非零 assignment generation。</param>
        /// <param name="visitSessionID">VisitWorld 的非空 VisitSession identity；OwnWorld 为空。</param>
        /// <param name="visitRevision">VisitWorld 的非零 revision；OwnWorld 为零。</param>
        private ClientBattleTargetIntent(
            ClientBattleTargetKind kind,
            long sessionGeneration,
            string personalWorldID,
            string worldInstanceID,
            ulong assignmentGeneration,
            string visitSessionID,
            ulong visitRevision)
        {
            Kind = kind;
            SessionGeneration = sessionGeneration;
            PersonalWorldID = RequireIdentity(
                personalWorldID,
                nameof(personalWorldID));
            WorldInstanceID = RequireIdentity(
                worldInstanceID,
                nameof(worldInstanceID));
            if (sessionGeneration <= 0)
            {
                throw new ArgumentOutOfRangeException(nameof(sessionGeneration));
            }

            if (assignmentGeneration == 0)
            {
                throw new ArgumentOutOfRangeException(nameof(assignmentGeneration));
            }

            if (kind == ClientBattleTargetKind.OwnWorld)
            {
                if (!string.IsNullOrEmpty(visitSessionID) || visitRevision != 0)
                {
                    throw new ArgumentException(
                        "own-world battle target cannot include VisitSession identity");
                }
            }
            else if (kind == ClientBattleTargetKind.VisitWorld)
            {
                VisitSessionID = RequireIdentity(
                    visitSessionID,
                    nameof(visitSessionID));
                if (visitRevision == 0)
                {
                    throw new ArgumentOutOfRangeException(nameof(visitRevision));
                }
            }
            else
            {
                throw new ArgumentOutOfRangeException(nameof(kind));
            }

            AssignmentGeneration = assignmentGeneration;
            VisitRevision = visitRevision;
        }

        /// <summary>
        /// 获取 target 类别。
        /// </summary>
        internal ClientBattleTargetKind Kind { get; }

        /// <summary>
        /// 获取统一 Session owner 的 current generation。
        /// </summary>
        internal long SessionGeneration { get; }

        /// <summary>
        /// 获取 target 所属 PersonalWorld identity。
        /// </summary>
        internal string PersonalWorldID { get; }

        /// <summary>
        /// 获取不可复活的 current WorldInstance identity。
        /// </summary>
        internal string WorldInstanceID { get; }

        /// <summary>
        /// 获取 current assignment generation。
        /// </summary>
        internal ulong AssignmentGeneration { get; }

        /// <summary>
        /// 获取 VisitWorld 的 VisitSession identity；OwnWorld 为空。
        /// </summary>
        internal string VisitSessionID { get; }

        /// <summary>
        /// 获取 VisitWorld 的 current revision；OwnWorld 为零。
        /// </summary>
        internal ulong VisitRevision { get; }

        /// <summary>
        /// 创建 own-world battle target。
        /// </summary>
        /// <param name="sessionGeneration">统一 Session generation。</param>
        /// <param name="personalWorldID">PersonalWorld identity。</param>
        /// <param name="worldInstanceID">WorldInstance identity。</param>
        /// <param name="assignmentGeneration">Assignment generation。</param>
        /// <returns>不含 credential 的 immutable intent。</returns>
        internal static ClientBattleTargetIntent OwnWorld(
            long sessionGeneration,
            string personalWorldID,
            string worldInstanceID,
            ulong assignmentGeneration)
        {
            return new ClientBattleTargetIntent(
                ClientBattleTargetKind.OwnWorld,
                sessionGeneration,
                personalWorldID,
                worldInstanceID,
                assignmentGeneration,
                null,
                0);
        }

        /// <summary>
        /// 创建 visit-world battle target。
        /// </summary>
        /// <param name="sessionGeneration">统一 Session generation。</param>
        /// <param name="personalWorldID">Owner PersonalWorld identity。</param>
        /// <param name="worldInstanceID">WorldInstance identity。</param>
        /// <param name="assignmentGeneration">Assignment generation。</param>
        /// <param name="visitSessionID">VisitSession identity。</param>
        /// <param name="visitRevision">Current VisitSession revision。</param>
        /// <returns>不含 credential 的 immutable intent。</returns>
        internal static ClientBattleTargetIntent VisitWorld(
            long sessionGeneration,
            string personalWorldID,
            string worldInstanceID,
            ulong assignmentGeneration,
            string visitSessionID,
            ulong visitRevision)
        {
            return new ClientBattleTargetIntent(
                ClientBattleTargetKind.VisitWorld,
                sessionGeneration,
                personalWorldID,
                worldInstanceID,
                assignmentGeneration,
                visitSessionID,
                visitRevision);
        }

        /// <summary>
        /// 比较用于 ticket/session binding 的全部 target identity。
        /// </summary>
        /// <param name="other">待比较 intent。</param>
        /// <returns>全部 identity 与 generation 一致时为 true。</returns>
        internal bool IsEquivalent(ClientBattleTargetIntent other)
        {
            return other != null &&
                   Kind == other.Kind &&
                   SessionGeneration == other.SessionGeneration &&
                   string.Equals(
                       PersonalWorldID,
                       other.PersonalWorldID,
                       StringComparison.Ordinal) &&
                   string.Equals(
                       WorldInstanceID,
                       other.WorldInstanceID,
                       StringComparison.Ordinal) &&
                   AssignmentGeneration == other.AssignmentGeneration &&
                   string.Equals(
                       VisitSessionID,
                       other.VisitSessionID,
                       StringComparison.Ordinal) &&
                   VisitRevision == other.VisitRevision;
        }

        /// <summary>
        /// 验证 protocol identity 的非空与长度边界。
        /// </summary>
        /// <param name="value">待验证 identity。</param>
        /// <param name="parameterName">异常参数名。</param>
        /// <returns>原始 canonical identity。</returns>
        private static string RequireIdentity(string value, string parameterName)
        {
            if (string.IsNullOrWhiteSpace(value) || value.Length > 128)
            {
                throw new ArgumentException(
                    "client battle target identity is invalid",
                    parameterName);
            }

            return value;
        }
    }

    /// <summary>
    /// 标识唯一 BattleNetworkClient 的安全可观察生命周期。
    /// </summary>
    internal enum ClientBattleConnectionState
    {
        /// <summary>尚未由 AppLifetime 初始化。</summary>
        Created = 0,

        /// <summary>允许显式 activation，但没有 ticket、socket 或 native context。</summary>
        Ready = 1,

        /// <summary>正在以 current target 签发 BattleTicket。</summary>
        IssuingTicket = 2,

        /// <summary>正在建立 authenticated UDP session。</summary>
        Handshaking = 3,

        /// <summary>ServerAccept 已验证，正在等待 current full baseline。</summary>
        AwaitingBaseline = 4,

        /// <summary>Current generation 可发送 input 并消费 snapshot/event。</summary>
        Active = 5,

        /// <summary>正在验证 authenticated endpoint rebind。</summary>
        Rebinding = 6,

        /// <summary>正在切换 traffic key epoch。</summary>
        Rekeying = 7,

        /// <summary>Current generation 正在线性化关闭。</summary>
        Closing = 8,

        /// <summary>App Scope 已停止且不可重启。</summary>
        Stopped = 9,
    }

    /// <summary>
    /// 标识 battle connection 的稳定低敏失败或关闭原因。
    /// </summary>
    internal enum ClientBattleFailure
    {
        /// <summary>尚无失败。</summary>
        None = 0,

        /// <summary>Session、target 或 lifecycle 不允许 activation。</summary>
        Policy = 1,

        /// <summary>调用方取消 current attempt。</summary>
        CallerCancelled = 2,

        /// <summary>Ticket、handshake、rekey 或 close deadline 到期。</summary>
        Timeout = 3,

        /// <summary>HTTP/UDP I/O 失败。</summary>
        Transport = 4,

        /// <summary>Response、handshake、header、route 或 Protobuf 违反契约。</summary>
        Protocol = 5,

        /// <summary>AEAD、proof、cookie、replay 或 binding 验证失败。</summary>
        Security = 6,

        /// <summary>Send/receive/dispatcher/session/KCP queue 达到 hard limit。</summary>
        Backpressure = 7,

        /// <summary>Ticket response 可能已提交但无法安全判定。</summary>
        CommitUnknown = 8,

        /// <summary>World/assignment/Visit target 已被更高事实替换。</summary>
        TargetReplaced = 9,

        /// <summary>统一 Session owner 已接受更高 epoch。</summary>
        SessionInvalidated = 10,

        /// <summary>Current generation 由 AppLifetime 逆序停止。</summary>
        Shutdown = 11,
    }

    /// <summary>
    /// 保存不含 endpoint、ticket、secret、payload 或玩家 identity 的 connection 快照。
    /// </summary>
    internal sealed class ClientBattleConnectionSnapshot
    {
        /// <summary>
        /// 创建 immutable 低敏 connection 快照。
        /// </summary>
        /// <param name="state">Current lifecycle state。</param>
        /// <param name="failure">最近稳定失败或关闭原因。</param>
        /// <param name="generation">每次 activation 递增的 battle generation。</param>
        /// <param name="targetKind">Current target 类别；未 activation 时为空。</param>
        /// <param name="role">ServerAccept 冻结的角色。</param>
        /// <param name="actorSlot">0-7 actor slot；尚未 accept 时为 -1。</param>
        /// <param name="trafficEpoch">Current non-secret traffic epoch。</param>
        /// <param name="endpointGeneration">Current non-secret endpoint generation。</param>
        /// <param name="latestServerTick">已原子发布的最大 server Tick。</param>
        /// <param name="sendQueueItems">Managed send queue 当前项数。</param>
        /// <param name="receiveQueueItems">Managed receive queue 当前项数。</param>
        internal ClientBattleConnectionSnapshot(
            ClientBattleConnectionState state,
            ClientBattleFailure failure,
            long generation,
            ClientBattleTargetKind? targetKind,
            ClientBattleRole role,
            int actorSlot,
            uint trafficEpoch,
            uint endpointGeneration,
            ulong latestServerTick,
            int sendQueueItems,
            int receiveQueueItems)
        {
            if (generation < 0 ||
                actorSlot < -1 ||
                actorSlot > 7 ||
                sendQueueItems < 0 ||
                sendQueueItems > 256 ||
                receiveQueueItems < 0 ||
                receiveQueueItems > 256)
            {
                throw new ArgumentOutOfRangeException(
                    nameof(generation),
                    "client battle connection snapshot violates hard bounds");
            }

            State = state;
            Failure = failure;
            Generation = generation;
            TargetKind = targetKind;
            Role = role;
            ActorSlot = actorSlot;
            TrafficEpoch = trafficEpoch;
            EndpointGeneration = endpointGeneration;
            LatestServerTick = latestServerTick;
            SendQueueItems = sendQueueItems;
            ReceiveQueueItems = receiveQueueItems;
        }

        /// <summary>获取 current lifecycle state。</summary>
        internal ClientBattleConnectionState State { get; }

        /// <summary>获取最近稳定失败或关闭原因。</summary>
        internal ClientBattleFailure Failure { get; }

        /// <summary>获取 current battle generation。</summary>
        internal long Generation { get; }

        /// <summary>获取 current target 类别。</summary>
        internal ClientBattleTargetKind? TargetKind { get; }

        /// <summary>获取 ServerAccept 冻结的角色。</summary>
        internal ClientBattleRole Role { get; }

        /// <summary>获取 0-7 actor slot；尚未 accept 时为 -1。</summary>
        internal int ActorSlot { get; }

        /// <summary>获取 current non-secret traffic epoch。</summary>
        internal uint TrafficEpoch { get; }

        /// <summary>获取 current non-secret endpoint generation。</summary>
        internal uint EndpointGeneration { get; }

        /// <summary>获取已原子发布的最大 server Tick。</summary>
        internal ulong LatestServerTick { get; }

        /// <summary>获取 managed send queue 当前项数。</summary>
        internal int SendQueueItems { get; }

        /// <summary>获取 managed receive queue 当前项数。</summary>
        internal int ReceiveQueueItems { get; }
    }

    /// <summary>
    /// 定义 Application 可见的唯一 battle connection command/snapshot port。
    /// </summary>
    internal interface IClientBattleConnectionPort : IAppLifetimeParticipant
    {
        /// <summary>
        /// 在 current Session/target 上创建 successor battle generation。
        /// </summary>
        /// <param name="intent">不含 credential 的 immutable target intent。</param>
        /// <param name="cancellationToken">Caller 与更高 lifecycle 的取消信号。</param>
        /// <returns>成功时 current low-sensitive connection snapshot。</returns>
        Task<ClientBattleConnectionSnapshot> ActivateAsync(
            ClientBattleTargetIntent intent,
            CancellationToken cancellationToken);

        /// <summary>
        /// 在 Application 已激活同 generation replica owner 后启动唯一 traffic pumps。
        /// </summary>
        /// <param name="battleGeneration">必须匹配刚完成 authenticated accept 的 generation。</param>
        /// <returns>首次启动成功时为 true；旧代、重复或未就绪调用为 false。</returns>
        bool TryStartTraffic(long battleGeneration);

        /// <summary>
        /// 线性化结束 current generation；重复调用安全。
        /// </summary>
        /// <param name="failure">稳定关闭原因。</param>
        /// <param name="cancellationToken">共享停止 deadline。</param>
        /// <returns>资源已释放且旧 callback 已失效时完成。</returns>
        Task DeactivateAsync(
            ClientBattleFailure failure,
            CancellationToken cancellationToken);

        /// <summary>
        /// 读取不产生网络副作用的 immutable 快照。
        /// </summary>
        /// <returns>Current low-sensitive connection snapshot。</returns>
        ClientBattleConnectionSnapshot Snapshot();
    }

    /// <summary>
    /// 允许 battle Infrastructure 为同一 connect attempt 取得 fresh 单次 HTTP authorization。
    /// </summary>
    internal interface IClientBattleAuthorizationSource
    {
        /// <summary>
        /// 在 expected Session generation 上取得 current access token lease。
        /// </summary>
        /// <param name="expectedSessionGeneration">Activation 冻结的 Session generation。</param>
        /// <param name="cancellationToken">Current connect attempt cancellation。</param>
        /// <returns>单次 authorization lease、服务端拒绝或稳定本地失败。</returns>
        Task<ClientGatewayResult<ClientCredentialLease>>
            AcquireBattleAuthorizationAsync(
                long expectedSessionGeneration,
                CancellationToken cancellationToken);
    }

    /// <summary>
    /// 允许唯一 connection recovery owner 编排 battle-only single-flight 与总 deadline。
    /// </summary>
    internal interface IClientBattleRecoveryScheduler
    {
        /// <summary>
        /// 在 current world target 不变时运行或复用唯一 battle-only recovery。
        /// </summary>
        /// <param name="sourceBattleGeneration">触发 terminal 的正 battle generation。</param>
        /// <param name="operation">只拥有 battle 派生状态的可取消恢复操作。</param>
        /// <returns>建立 successor generation 时为 true；被抢占或失败时为 false。</returns>
        Task<bool> RunBattleRecoveryAsync(
            long sourceBattleGeneration,
            Func<CancellationToken, Task<bool>> operation);

        /// <summary>
        /// 由 world/control/safe-return/Session authority 抢占 current battle-only plan。
        /// </summary>
        void PreemptBattleRecovery();
    }

    /// <summary>
    /// 向 battle runtime 暴露唯一 world target owner 的窄、无 credential 快照边界。
    /// </summary>
    internal interface IClientBattleTargetSource
    {
        /// <summary>
        /// 在 stable target、replacement、safe return 或 Session invalidation 后通知重新捕获。
        /// </summary>
        event Action Changed;

        /// <summary>
        /// 原子捕获 current stable Session/world/assignment/Visit binding。
        /// </summary>
        /// <param name="intent">成功时返回不含 endpoint 与 credential 的 immutable target。</param>
        /// <returns>当前存在可建立 battle session 的 stable target 时为 true。</returns>
        bool TryCapture(out ClientBattleTargetIntent intent);
    }

    /// <summary>
    /// 定义 Infrastructure receive pump 向 current Application gameplay owners 提交的窄入口。
    /// </summary>
    internal interface IClientBattleInboundSink
    {
        /// <summary>
        /// 提交一个已完成 wire/protobuf 校验的 snapshot partition。
        /// </summary>
        /// <param name="partition">Current generation immutable partition。</param>
        /// <returns>Replica 的 closed commit 结果。</returns>
        ClientBattleSnapshotCommit AcceptSnapshot(
            ClientBattleSnapshotPartition partition);

        /// <summary>
        /// 提交 reliable entity lifecycle event。
        /// </summary>
        /// <param name="lifecycle">Current generation immutable event。</param>
        /// <returns>Current generation 接受时为 true。</returns>
        bool AcceptEntityLifecycle(
            ClientBattleEntityLifecycle lifecycle);

        /// <summary>
        /// 提交 reliable ability event。
        /// </summary>
        /// <param name="abilityEvent">Current generation immutable event。</param>
        /// <returns>Current generation 接受时为 true。</returns>
        bool AcceptAbilityEvent(
            ClientBattleAbilityEvent abilityEvent);

        /// <summary>
        /// 提交 reliable resync response。
        /// </summary>
        /// <param name="response">Current generation immutable response。</param>
        /// <param name="nowMilliseconds">与 runtime recovery 同域的 monotonic-derived 时间。</param>
        /// <returns>Current single-flight request 接受时为 true。</returns>
        bool AcceptResyncResponse(
            ClientBattleResyncResponse response,
            long nowMilliseconds);

        /// <summary>
        /// 发布 current generation 的唯一 terminal；旧 generation 必须忽略。
        /// </summary>
        /// <param name="battleGeneration">发生终态的 generation。</param>
        /// <param name="failure">稳定低敏失败。</param>
        void PublishTerminal(
            long battleGeneration,
            ClientBattleFailure failure);
    }
}
