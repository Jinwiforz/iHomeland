using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;

namespace IHomeland.Client.Application.World
{
    /// <summary>
    /// 标识权威投影尝试对当前 Service 产生的稳定结果。
    /// </summary>
    internal enum ClientProjectionApplyResult
    {
        /// <summary>更高 revision 或首次有效投影已原子提交。</summary>
        Applied = 0,

        /// <summary>同 revision 且语义等价，当前投影保持不变。</summary>
        Duplicate = 1,

        /// <summary>低 revision 输入已丢弃。</summary>
        Stale = 2,

        /// <summary>同 revision 内容冲突，当前 target 必须 fail closed。</summary>
        Conflict = 3,

        /// <summary>输入违反 identity、enum、数量、deadline 或 binding contract。</summary>
        Rejected = 4,

        /// <summary>有界集合达到硬上限且没有过期条目可回收。</summary>
        Overflow = 5,
    }

    /// <summary>
    /// 标识当前 VisitSession 投影相对于本客户端的权威角色。
    /// </summary>
    internal enum ClientVisitRole
    {
        /// <summary>尚未由 admission 或 own-world flow 建立角色。</summary>
        None = 0,

        /// <summary>当前客户端拥有 immutable PersonalWorld Owner 权限。</summary>
        Owner = 1,

        /// <summary>当前客户端只持有受控 Visitor membership。</summary>
        Visitor = 2,
    }

    /// <summary>
    /// 标识 PersonalWorld 持久投影的封闭生命周期。
    /// </summary>
    internal enum ClientWorldLifecycle
    {
        /// <summary>持久世界允许由 current assignment 承载。</summary>
        Active = 1,

        /// <summary>持久世界已终止且不得继续进入。</summary>
        Archived = 2,
    }

    /// <summary>
    /// 标识 VisitSession 控制面生命周期。
    /// </summary>
    internal enum ClientVisitLifecycle
    {
        /// <summary>允许 Owner command 与目标 Visitor accept/join。</summary>
        Open = 1,

        /// <summary>Owner 暂时不可用且不允许新增资格。</summary>
        OwnerGrace = 2,

        /// <summary>VisitSession 已终止且不能恢复。</summary>
        Closed = 3,
    }

    /// <summary>
    /// 标识客户端可见的 Visitor membership 阶段。
    /// </summary>
    internal enum ClientVisitMembershipState
    {
        /// <summary>Accept 已提交但尚未完成 join。</summary>
        Reserved = 1,

        /// <summary>Visitor 已绑定 current gameplay connection。</summary>
        Joined = 2,

        /// <summary>Visitor 位于有界 reconnect window。</summary>
        Reconnecting = 3,
    }

    /// <summary>
    /// 标识定向 invite 的公开状态。
    /// </summary>
    internal enum ClientVisitInviteState
    {
        /// <summary>目标 Visitor 仍可 accept。</summary>
        Pending = 1,

        /// <summary>Invite 已转换为 reservation。</summary>
        Accepted = 2,

        /// <summary>服务端已确认该 invite 不再可接受，仅用于精确退役本地 identity。</summary>
        Retired = 3,
    }

    /// <summary>
    /// 标识服务端确定的安全返回原因。
    /// </summary>
    internal enum ClientSafeReturnReason
    {
        /// <summary>Visitor 主动离开。</summary>
        VoluntaryLeave = 1,

        /// <summary>Immutable Owner 移除 Visitor。</summary>
        Kicked = 2,

        /// <summary>Owner 显式关闭访问。</summary>
        OwnerClosed = 3,

        /// <summary>Owner grace 到期仍未恢复。</summary>
        OwnerUnavailable = 4,

        /// <summary>VisitSession 到达绝对 expiry。</summary>
        SessionExpired = 5,

        /// <summary>Current assignment 已丢失或替换。</summary>
        AssignmentChanged = 6,

        /// <summary>Visitor reconnect window 已到期。</summary>
        VisitorReconnectExpired = 7,

        /// <summary>运行态依赖无法继续证明访问资格。</summary>
        DependencyLost = 8,
    }

    /// <summary>
    /// 标识服务端选择的安全返回目标类别。
    /// </summary>
    internal enum ClientSafeReturnDestination
    {
        /// <summary>重新解析当前玩家自己的 PersonalWorld。</summary>
        OwnPersonalWorld = 1,

        /// <summary>Own-world 不可用时进入受信安全入口。</summary>
        SafeEntry = 2,
    }

    /// <summary>
    /// 标识 world target flow 的封闭 App Scope 状态。
    /// </summary>
    internal enum ClientWorldFlowState
    {
        /// <summary>尚未请求解析任何 world target。</summary>
        Inactive = 0,

        /// <summary>正在解析并连接当前玩家自己的世界。</summary>
        ResolvingOwnWorld = 1,

        /// <summary>当前可靠通道绑定自己的 PersonalWorld。</summary>
        OwnWorld = 2,

        /// <summary>正在接受 invite 并加入 Owner WorldInstance。</summary>
        JoiningVisit = 3,

        /// <summary>当前可靠通道绑定 Visitor target。</summary>
        Visiting = 4,

        /// <summary>旧 Visitor target 已不可写且正在安全返回。</summary>
        ReturningOwnWorld = 5,

        /// <summary>Current gameplay connection 非预期终止，旧 target 已不可交互。</summary>
        ConnectionLost = 6,

        /// <summary>唯一恢复owner正在重建断线前冻结target。</summary>
        RecoveringTarget = 7,

        /// <summary>App Scope 已停止且不允许再次转换。</summary>
        Stopped = 8,
    }

    /// <summary>
    /// 标识 world flow 可安全展示的低敏失败类别。
    /// </summary>
    internal enum ClientWorldFlowFailure
    {
        /// <summary>尚无失败。</summary>
        None = 0,

        /// <summary>当前 session、role 或状态不允许操作。</summary>
        Policy = 1,

        /// <summary>调用方取消等待。</summary>
        CallerCancelled = 2,

        /// <summary>Transport 或 operation deadline 失败。</summary>
        Transport = 3,

        /// <summary>服务端以结构有效错误拒绝操作。</summary>
        Rejected = 4,

        /// <summary>Response/PUSH 违反冻结合同。</summary>
        Protocol = 5,

        /// <summary>请求可能已提交但客户端无法判定。</summary>
        CommitUnknown = 6,

        /// <summary>App Scope 已停止。</summary>
        Stopped = 7,

        /// <summary>目标 invite 已被撤销、过期、消费或由终态统一退役。</summary>
        InviteUnavailable = 8,
    }

    /// <summary>
    /// 保存不包含 credential 的 TLS/TCP world endpoint 投影。
    /// </summary>
    internal sealed class ClientWorldEndpointProjection
    {
        /// <summary>创建已验证 host 与 port 投影。</summary>
        /// <param name="host">长度不超过 253 的非空 host。</param>
        /// <param name="port">范围为 1-65535 的 TCP port。</param>
        internal ClientWorldEndpointProjection(string host, int port)
        {
            Host = host ?? throw new ArgumentNullException(nameof(host));
            Port = port;
        }

        /// <summary>获取受信 endpoint host。</summary>
        internal string Host { get; }

        /// <summary>获取 TCP port。</summary>
        internal int Port { get; }

        /// <summary>比较两个 endpoint 的全部公开语义。</summary>
        /// <param name="other">待比较 endpoint。</param>
        /// <returns>Host 与 port 均一致时返回 true。</returns>
        internal bool IsEquivalent(ClientWorldEndpointProjection other)
        {
            return other != null &&
                   string.Equals(Host, other.Host, StringComparison.OrdinalIgnoreCase) &&
                   Port == other.Port;
        }
    }

    /// <summary>
    /// 保存 current WorldInstance 的客户端安全 assignment 投影。
    /// </summary>
    internal sealed class ClientWorldAssignmentProjection
    {
        /// <summary>创建不包含 node、fence 或 credential 的 assignment。</summary>
        /// <param name="personalWorldID">Assignment 所属 PersonalWorld。</param>
        /// <param name="worldInstanceID">不可复活的 WorldInstance 标识。</param>
        /// <param name="endpoint">受信 TLS/TCP endpoint。</param>
        /// <param name="generation">从 1 开始的公开 assignment generation。</param>
        /// <param name="leaseExpiresAtMilliseconds">Lease Unix expiry，单位为毫秒。</param>
        internal ClientWorldAssignmentProjection(
            string personalWorldID,
            string worldInstanceID,
            ClientWorldEndpointProjection endpoint,
            ulong generation,
            long leaseExpiresAtMilliseconds)
        {
            PersonalWorldID = personalWorldID ?? throw new ArgumentNullException(nameof(personalWorldID));
            WorldInstanceID = worldInstanceID ?? throw new ArgumentNullException(nameof(worldInstanceID));
            Endpoint = endpoint ?? throw new ArgumentNullException(nameof(endpoint));
            Generation = generation;
            LeaseExpiresAtMilliseconds = leaseExpiresAtMilliseconds;
        }

        /// <summary>获取 assignment 所属 PersonalWorld 标识。</summary>
        internal string PersonalWorldID { get; }

        /// <summary>获取 current WorldInstance 标识。</summary>
        internal string WorldInstanceID { get; }

        /// <summary>获取不含 credential 的 endpoint。</summary>
        internal ClientWorldEndpointProjection Endpoint { get; }

        /// <summary>获取公开 assignment generation。</summary>
        internal ulong Generation { get; }

        /// <summary>获取 lease Unix expiry，单位为毫秒。</summary>
        internal long LeaseExpiresAtMilliseconds { get; }

        /// <summary>比较两个 assignment 的全部公开语义。</summary>
        /// <param name="other">待比较 assignment。</param>
        /// <returns>全部字段一致时返回 true。</returns>
        internal bool IsEquivalent(ClientWorldAssignmentProjection other)
        {
            return other != null &&
                   string.Equals(PersonalWorldID, other.PersonalWorldID, StringComparison.Ordinal) &&
                   string.Equals(WorldInstanceID, other.WorldInstanceID, StringComparison.Ordinal) &&
                   Endpoint.IsEquivalent(other.Endpoint) &&
                   Generation == other.Generation &&
                   LeaseExpiresAtMilliseconds == other.LeaseExpiresAtMilliseconds;
        }

        /// <summary>比较服务端公开的 assignment identity，不把可续期 lease deadline 当作 identity。</summary>
        /// <param name="other">待比较 assignment。</param>
        /// <returns>World、instance、endpoint 与 generation 均一致时返回 true。</returns>
        /// <remarks>
        /// 服务端 AssignmentStamp 明确不包含 expiry；同一 assignment 续租时这些字段保持不变，
        /// 只有 <see cref="LeaseExpiresAtMilliseconds"/> 单调前进。
        /// </remarks>
        internal bool HasSameIdentity(ClientWorldAssignmentProjection other)
        {
            return other != null &&
                   string.Equals(PersonalWorldID, other.PersonalWorldID, StringComparison.Ordinal) &&
                   string.Equals(WorldInstanceID, other.WorldInstanceID, StringComparison.Ordinal) &&
                   Endpoint.IsEquivalent(other.Endpoint) &&
                   Generation == other.Generation;
        }
    }

    /// <summary>
    /// 保存 PersonalWorld 与可选 current assignment 的完整不可变投影。
    /// </summary>
    internal sealed class ClientPersonalWorldProjection
    {
        /// <summary>创建完整 world replacement 投影。</summary>
        /// <param name="personalWorldID">稳定 PersonalWorld 标识。</param>
        /// <param name="ownerPlayerID">不可转移 Owner Player 标识。</param>
        /// <param name="lifecycle">持久 world 生命周期。</param>
        /// <param name="revision">从 1 开始的 world revision。</param>
        /// <param name="createdAtMilliseconds">创建 Unix 时间，单位为毫秒。</param>
        /// <param name="assignment">可选 current assignment；为空表示必须清除旧值。</param>
        internal ClientPersonalWorldProjection(
            string personalWorldID,
            string ownerPlayerID,
            ClientWorldLifecycle lifecycle,
            ulong revision,
            long createdAtMilliseconds,
            ClientWorldAssignmentProjection assignment)
        {
            PersonalWorldID = personalWorldID ?? throw new ArgumentNullException(nameof(personalWorldID));
            OwnerPlayerID = ownerPlayerID ?? throw new ArgumentNullException(nameof(ownerPlayerID));
            Lifecycle = lifecycle;
            Revision = revision;
            CreatedAtMilliseconds = createdAtMilliseconds;
            Assignment = assignment;
        }

        /// <summary>获取稳定 PersonalWorld 标识。</summary>
        internal string PersonalWorldID { get; }

        /// <summary>获取 immutable Owner Player 标识。</summary>
        internal string OwnerPlayerID { get; }

        /// <summary>获取持久 world 生命周期。</summary>
        internal ClientWorldLifecycle Lifecycle { get; }

        /// <summary>获取最高已提交 world revision。</summary>
        internal ulong Revision { get; }

        /// <summary>获取创建 Unix 时间，单位为毫秒。</summary>
        internal long CreatedAtMilliseconds { get; }

        /// <summary>获取可选 current assignment；为空时不得沿用旧 endpoint。</summary>
        internal ClientWorldAssignmentProjection Assignment { get; }

        /// <summary>比较两个完整 world replacement 的全部公开语义。</summary>
        /// <param name="other">待比较 world。</param>
        /// <returns>全部字段一致时返回 true。</returns>
        internal bool IsEquivalent(ClientPersonalWorldProjection other)
        {
            return other != null &&
                   string.Equals(PersonalWorldID, other.PersonalWorldID, StringComparison.Ordinal) &&
                   string.Equals(OwnerPlayerID, other.OwnerPlayerID, StringComparison.Ordinal) &&
                   Lifecycle == other.Lifecycle &&
                   Revision == other.Revision &&
                   CreatedAtMilliseconds == other.CreatedAtMilliseconds &&
                   (Assignment == null
                       ? other.Assignment == null
                       : Assignment.IsEquivalent(other.Assignment));
        }

        /// <summary>只比较 PersonalWorld aggregate 事实，不把独立代际的 assignment 混入 world revision。</summary>
        /// <param name="other">待比较 world。</param>
        /// <returns>World identity、Owner、lifecycle、revision 与创建时间一致时返回 true。</returns>
        internal bool HasSameWorldFacts(ClientPersonalWorldProjection other)
        {
            return other != null &&
                   string.Equals(PersonalWorldID, other.PersonalWorldID, StringComparison.Ordinal) &&
                   string.Equals(OwnerPlayerID, other.OwnerPlayerID, StringComparison.Ordinal) &&
                   Lifecycle == other.Lifecycle &&
                   Revision == other.Revision &&
                   CreatedAtMilliseconds == other.CreatedAtMilliseconds;
        }
    }

    /// <summary>
    /// 保存单个 Visitor 的公开 membership 摘要。
    /// </summary>
    internal sealed class ClientVisitVisitorProjection
    {
        /// <summary>创建 Visitor membership 投影。</summary>
        /// <param name="playerID">Visitor Player 标识。</param>
        /// <param name="state">公开 membership 阶段。</param>
        internal ClientVisitVisitorProjection(string playerID, ClientVisitMembershipState state)
        {
            PlayerID = playerID ?? throw new ArgumentNullException(nameof(playerID));
            State = state;
        }

        /// <summary>获取 Visitor Player 标识。</summary>
        internal string PlayerID { get; }

        /// <summary>获取公开 membership 阶段。</summary>
        internal ClientVisitMembershipState State { get; }

        /// <summary>比较 Visitor identity 与 membership state。</summary>
        /// <param name="other">待比较 Visitor。</param>
        /// <returns>全部字段一致时返回 true。</returns>
        internal bool IsEquivalent(ClientVisitVisitorProjection other)
        {
            return other != null &&
                   string.Equals(PlayerID, other.PlayerID, StringComparison.Ordinal) &&
                   State == other.State;
        }
    }

    /// <summary>
    /// 保存 VisitSession 完整 replacement 与当前客户端角色。
    /// </summary>
    internal sealed class ClientVisitSessionProjection
    {
        /// <summary>创建完整 VisitSession 投影。</summary>
        /// <param name="visitSessionID">VisitSession aggregate 标识。</param>
        /// <param name="ownerPlayerID">不可转移 Owner Player 标识。</param>
        /// <param name="assignment">创建时绑定的 WorldInstance assignment。</param>
        /// <param name="lifecycle">VisitSession 控制面生命周期。</param>
        /// <param name="revision">从 1 开始的 aggregate revision。</param>
        /// <param name="capacity">不含 Owner 的 Visitor 上限。</param>
        /// <param name="visitors">按 PlayerID 稳定排序的 Visitor 摘要。</param>
        /// <param name="createdAtMilliseconds">创建 Unix 时间，单位为毫秒。</param>
        /// <param name="expiresAtMilliseconds">Session Unix expiry，单位为毫秒。</param>
        /// <param name="ownerGraceExpiresAtMilliseconds">Owner grace Unix expiry；非 grace 时为 0。</param>
        /// <param name="role">当前客户端由 flow 建立的权威角色。</param>
        internal ClientVisitSessionProjection(
            string visitSessionID,
            string ownerPlayerID,
            ClientWorldAssignmentProjection assignment,
            ClientVisitLifecycle lifecycle,
            ulong revision,
            uint capacity,
            IReadOnlyList<ClientVisitVisitorProjection> visitors,
            long createdAtMilliseconds,
            long expiresAtMilliseconds,
            long ownerGraceExpiresAtMilliseconds,
            ClientVisitRole role)
        {
            VisitSessionID = visitSessionID ?? throw new ArgumentNullException(nameof(visitSessionID));
            OwnerPlayerID = ownerPlayerID ?? throw new ArgumentNullException(nameof(ownerPlayerID));
            Assignment = assignment ?? throw new ArgumentNullException(nameof(assignment));
            Lifecycle = lifecycle;
            Revision = revision;
            Capacity = capacity;
            Visitors = new ReadOnlyCollection<ClientVisitVisitorProjection>(
                new List<ClientVisitVisitorProjection>(visitors ?? throw new ArgumentNullException(nameof(visitors))));
            CreatedAtMilliseconds = createdAtMilliseconds;
            ExpiresAtMilliseconds = expiresAtMilliseconds;
            OwnerGraceExpiresAtMilliseconds = ownerGraceExpiresAtMilliseconds;
            Role = role;
        }

        /// <summary>获取 VisitSession aggregate 标识。</summary>
        internal string VisitSessionID { get; }

        /// <summary>获取 immutable Owner Player 标识。</summary>
        internal string OwnerPlayerID { get; }

        /// <summary>获取创建时绑定的 assignment。</summary>
        internal ClientWorldAssignmentProjection Assignment { get; }

        /// <summary>获取控制面生命周期。</summary>
        internal ClientVisitLifecycle Lifecycle { get; }

        /// <summary>获取最高已提交 aggregate revision。</summary>
        internal ulong Revision { get; }

        /// <summary>获取不含 Owner 的 Visitor capacity。</summary>
        internal uint Capacity { get; }

        /// <summary>获取不可修改的 Visitor 摘要集合。</summary>
        internal IReadOnlyList<ClientVisitVisitorProjection> Visitors { get; }

        /// <summary>获取创建 Unix 时间，单位为毫秒。</summary>
        internal long CreatedAtMilliseconds { get; }

        /// <summary>获取 session Unix expiry，单位为毫秒。</summary>
        internal long ExpiresAtMilliseconds { get; }

        /// <summary>获取 Owner grace Unix expiry；非 grace 时为 0。</summary>
        internal long OwnerGraceExpiresAtMilliseconds { get; }

        /// <summary>获取当前客户端相对于该 snapshot 的权威角色。</summary>
        internal ClientVisitRole Role { get; }

        /// <summary>比较两个完整 VisitSession replacement 的全部公开语义。</summary>
        /// <param name="other">待比较 VisitSession。</param>
        /// <returns>全部字段和稳定排序 visitor 集合一致时返回 true。</returns>
        internal bool IsEquivalent(ClientVisitSessionProjection other)
        {
            if (other == null ||
                !string.Equals(VisitSessionID, other.VisitSessionID, StringComparison.Ordinal) ||
                !string.Equals(OwnerPlayerID, other.OwnerPlayerID, StringComparison.Ordinal) ||
                !Assignment.IsEquivalent(other.Assignment) ||
                Lifecycle != other.Lifecycle || Revision != other.Revision || Capacity != other.Capacity ||
                CreatedAtMilliseconds != other.CreatedAtMilliseconds ||
                ExpiresAtMilliseconds != other.ExpiresAtMilliseconds ||
                OwnerGraceExpiresAtMilliseconds != other.OwnerGraceExpiresAtMilliseconds ||
                Role != other.Role || Visitors.Count != other.Visitors.Count)
            {
                return false;
            }

            for (var index = 0; index < Visitors.Count; index++)
            {
                if (!Visitors[index].IsEquivalent(other.Visitors[index]))
                {
                    return false;
                }
            }

            return true;
        }

        /// <summary>比较除current assignment外的完整VisitSession replacement语义。</summary>
        /// <param name="other">待比较VisitSession。</param>
        /// <returns>Aggregate事实、角色与稳定排序Visitor集合一致时返回true。</returns>
        internal bool IsEquivalentIgnoringAssignment(ClientVisitSessionProjection other)
        {
            if (other == null ||
                !string.Equals(VisitSessionID, other.VisitSessionID, StringComparison.Ordinal) ||
                !string.Equals(OwnerPlayerID, other.OwnerPlayerID, StringComparison.Ordinal) ||
                Lifecycle != other.Lifecycle || Revision != other.Revision || Capacity != other.Capacity ||
                CreatedAtMilliseconds != other.CreatedAtMilliseconds ||
                ExpiresAtMilliseconds != other.ExpiresAtMilliseconds ||
                OwnerGraceExpiresAtMilliseconds != other.OwnerGraceExpiresAtMilliseconds ||
                Role != other.Role || Visitors.Count != other.Visitors.Count)
            {
                return false;
            }

            for (var index = 0; index < Visitors.Count; index++)
            {
                if (!Visitors[index].IsEquivalent(other.Visitors[index]))
                {
                    return false;
                }
            }

            return true;
        }
    }

    /// <summary>
    /// 保存定向 Visit invite 的不可变 inbox 投影。
    /// </summary>
    internal sealed class ClientVisitInviteProjection
    {
        /// <summary>创建定向 invite 投影。</summary>
        /// <param name="inviteID">Invite 标识。</param>
        /// <param name="visitSessionID">所属 VisitSession 标识。</param>
        /// <param name="ownerPlayerID">不可转移 Owner Player 标识。</param>
        /// <param name="targetVisitorID">服务端定向目标 Visitor 标识。</param>
        /// <param name="state">Pending 或 Accepted。</param>
        /// <param name="createdRevision">创建 invite 后的 aggregate revision。</param>
        /// <param name="expiresAtMilliseconds">Invite Unix expiry，单位为毫秒。</param>
        internal ClientVisitInviteProjection(
            string inviteID,
            string visitSessionID,
            string ownerPlayerID,
            string targetVisitorID,
            ClientVisitInviteState state,
            ulong createdRevision,
            long expiresAtMilliseconds)
        {
            InviteID = inviteID ?? throw new ArgumentNullException(nameof(inviteID));
            VisitSessionID = visitSessionID ?? throw new ArgumentNullException(nameof(visitSessionID));
            OwnerPlayerID = ownerPlayerID ?? throw new ArgumentNullException(nameof(ownerPlayerID));
            TargetVisitorID = targetVisitorID ?? throw new ArgumentNullException(nameof(targetVisitorID));
            State = state;
            CreatedRevision = createdRevision;
            ExpiresAtMilliseconds = expiresAtMilliseconds;
        }

        /// <summary>获取 invite 标识。</summary>
        internal string InviteID { get; }

        /// <summary>获取所属 VisitSession 标识。</summary>
        internal string VisitSessionID { get; }

        /// <summary>获取 immutable Owner Player 标识。</summary>
        internal string OwnerPlayerID { get; }

        /// <summary>获取唯一目标 Visitor 标识。</summary>
        internal string TargetVisitorID { get; }

        /// <summary>获取 invite 公开状态。</summary>
        internal ClientVisitInviteState State { get; }

        /// <summary>获取 invite 创建后的 aggregate revision。</summary>
        internal ulong CreatedRevision { get; }

        /// <summary>获取 invite Unix expiry，单位为毫秒。</summary>
        internal long ExpiresAtMilliseconds { get; }

        /// <summary>比较两个 invite 的全部公开语义。</summary>
        /// <param name="other">待比较 invite。</param>
        /// <returns>全部字段一致时返回 true。</returns>
        internal bool IsEquivalent(ClientVisitInviteProjection other)
        {
            return other != null &&
                   string.Equals(InviteID, other.InviteID, StringComparison.Ordinal) &&
                   string.Equals(VisitSessionID, other.VisitSessionID, StringComparison.Ordinal) &&
                   string.Equals(OwnerPlayerID, other.OwnerPlayerID, StringComparison.Ordinal) &&
                   string.Equals(TargetVisitorID, other.TargetVisitorID, StringComparison.Ordinal) &&
                   State == other.State && CreatedRevision == other.CreatedRevision &&
                   ExpiresAtMilliseconds == other.ExpiresAtMilliseconds;
        }
    }

    /// <summary>
    /// 保存不冒充完整 snapshot 的 Visit control hint。
    /// </summary>
    internal sealed class ClientVisitControlHint
    {
        /// <summary>创建 owner availability 或 terminal close hint。</summary>
        /// <param name="visitSessionID">Hint 所属 VisitSession。</param>
        /// <param name="revision">产生 hint 的 aggregate revision。</param>
        /// <param name="ownerAvailable">可选 Owner availability；close hint 时为空。</param>
        /// <param name="graceExpiresAtMilliseconds">Owner unavailable grace deadline；其他情况为 0。</param>
        /// <param name="closedReason">可选 terminal close 原因；availability hint 时为空。</param>
        internal ClientVisitControlHint(
            string visitSessionID,
            ulong revision,
            bool? ownerAvailable,
            long graceExpiresAtMilliseconds,
            ClientSafeReturnReason? closedReason)
        {
            VisitSessionID = visitSessionID ?? throw new ArgumentNullException(nameof(visitSessionID));
            Revision = revision;
            OwnerAvailable = ownerAvailable;
            GraceExpiresAtMilliseconds = graceExpiresAtMilliseconds;
            ClosedReason = closedReason;
        }

        /// <summary>获取 hint 所属 VisitSession 标识。</summary>
        internal string VisitSessionID { get; }

        /// <summary>获取产生 hint 的 aggregate revision。</summary>
        internal ulong Revision { get; }

        /// <summary>获取可选 Owner availability。</summary>
        internal bool? OwnerAvailable { get; }

        /// <summary>获取 Owner grace Unix expiry；不适用时为 0。</summary>
        internal long GraceExpiresAtMilliseconds { get; }

        /// <summary>获取可选 terminal close 原因。</summary>
        internal ClientSafeReturnReason? ClosedReason { get; }
    }

    /// <summary>
    /// 保存 gameplay channel 交付的权威安全返回指令。
    /// </summary>
    internal sealed class ClientSafeReturnProjection
    {
        /// <summary>创建不可由 caller 改写的安全返回投影。</summary>
        /// <param name="visitSessionID">产生返回的 VisitSession。</param>
        /// <param name="visitorID">服务端声明的目标 Visitor。</param>
        /// <param name="reason">权威返回原因。</param>
        /// <param name="preferred">首选返回目标。</param>
        /// <param name="fallback">首选不可用时的 fallback。</param>
        /// <param name="revision">产生该返回结果的已提交 aggregate 版本。</param>
        internal ClientSafeReturnProjection(
            string visitSessionID,
            string visitorID,
            ClientSafeReturnReason reason,
            ClientSafeReturnDestination preferred,
            ClientSafeReturnDestination fallback,
            ulong revision)
        {
            VisitSessionID = visitSessionID ?? throw new ArgumentNullException(nameof(visitSessionID));
            VisitorID = visitorID ?? throw new ArgumentNullException(nameof(visitorID));
            Reason = reason;
            Preferred = preferred;
            Fallback = fallback;
            Revision = revision;
        }

        /// <summary>获取产生返回的 VisitSession 标识。</summary>
        internal string VisitSessionID { get; }

        /// <summary>获取服务端声明的目标 Visitor 标识。</summary>
        internal string VisitorID { get; }

        /// <summary>获取权威返回原因。</summary>
        internal ClientSafeReturnReason Reason { get; }

        /// <summary>获取首选返回目标。</summary>
        internal ClientSafeReturnDestination Preferred { get; }

        /// <summary>获取 fallback 返回目标。</summary>
        internal ClientSafeReturnDestination Fallback { get; }

        /// <summary>获取产生该指令的已提交 aggregate 版本。</summary>
        internal ulong Revision { get; }
    }

    /// <summary>
    /// 保存 PersonalWorld Service 的两类事实与 control hint 状态。
    /// </summary>
    internal sealed class ClientPersonalWorldServiceSnapshot
    {
        /// <summary>创建不可变 Service snapshot。</summary>
        /// <param name="primaryWorld">认证 actor 的 primary PersonalWorld。</param>
        /// <param name="currentWorld">Current gameplay target 的 world。</param>
        /// <param name="assignmentHint">WSS 公布但尚未由完整 snapshot确认的 assignment。</param>
        /// <param name="needsRefresh">Control hint 或协议冲突是否要求重新解析。</param>
        internal ClientPersonalWorldServiceSnapshot(
            ClientPersonalWorldProjection primaryWorld,
            ClientPersonalWorldProjection currentWorld,
            ClientWorldAssignmentProjection assignmentHint,
            bool needsRefresh)
        {
            PrimaryWorld = primaryWorld;
            CurrentWorld = currentWorld;
            AssignmentHint = assignmentHint;
            NeedsRefresh = needsRefresh;
        }

        /// <summary>获取认证 actor 的 primary PersonalWorld。</summary>
        internal ClientPersonalWorldProjection PrimaryWorld { get; }

        /// <summary>获取 current gameplay target 的 world。</summary>
        internal ClientPersonalWorldProjection CurrentWorld { get; }

        /// <summary>获取只供收敛判断的 WSS assignment hint。</summary>
        internal ClientWorldAssignmentProjection AssignmentHint { get; }

        /// <summary>获取是否必须重新读取完整权威投影。</summary>
        internal bool NeedsRefresh { get; }
    }

    /// <summary>
    /// 保存 VisitSession Service 的当前完整投影、inbox、Owner 发出邀请与 control hint。
    /// </summary>
    internal sealed class ClientVisitSessionServiceSnapshot
    {
        /// <summary>创建不可变 Service snapshot。</summary>
        /// <param name="current">Current VisitSession 完整投影。</param>
        /// <param name="invites">按稳定 key 排序的有效 inbox invite 集合。</param>
        /// <param name="outgoingInvites">按稳定 key 排序的有效 Owner 发出邀请集合。</param>
        /// <param name="controlHint">可选 control hint。</param>
        /// <param name="needsRefresh">是否必须请求完整 VisitSession snapshot。</param>
        internal ClientVisitSessionServiceSnapshot(
            ClientVisitSessionProjection current,
            IReadOnlyList<ClientVisitInviteProjection> invites,
            IReadOnlyList<ClientVisitInviteProjection> outgoingInvites,
            ClientVisitControlHint controlHint,
            bool needsRefresh)
        {
            Current = current;
            Invites = new ReadOnlyCollection<ClientVisitInviteProjection>(
                new List<ClientVisitInviteProjection>(invites ?? throw new ArgumentNullException(nameof(invites))));
            OutgoingInvites = new ReadOnlyCollection<ClientVisitInviteProjection>(
                new List<ClientVisitInviteProjection>(
                    outgoingInvites ?? throw new ArgumentNullException(nameof(outgoingInvites))));
            ControlHint = controlHint;
            NeedsRefresh = needsRefresh;
        }

        /// <summary>获取 current VisitSession 完整投影。</summary>
        internal ClientVisitSessionProjection Current { get; }

        /// <summary>获取只允许 Visitor 接受的不可修改有效 inbox invite 集合。</summary>
        internal IReadOnlyList<ClientVisitInviteProjection> Invites { get; }

        /// <summary>获取只用于 Owner 管理与展示的不可修改有效发出邀请集合。</summary>
        internal IReadOnlyList<ClientVisitInviteProjection> OutgoingInvites { get; }

        /// <summary>获取不冒充完整 snapshot 的 control hint。</summary>
        internal ClientVisitControlHint ControlHint { get; }

        /// <summary>获取是否必须请求完整 VisitSession snapshot。</summary>
        internal bool NeedsRefresh { get; }
    }

    /// <summary>
    /// 保存 world flow 的低敏只读状态。
    /// </summary>
    internal sealed class ClientWorldFlowSnapshot
    {
        /// <summary>创建不包含 endpoint、credential 或 payload 的 flow snapshot。</summary>
        /// <param name="state">当前封闭 flow 状态。</param>
        /// <param name="targetGeneration">每次转换递增的本地 generation。</param>
        /// <param name="failure">最近稳定失败类别。</param>
        /// <param name="visitSessionID">Current Visitor target；其他状态为空。</param>
        /// <param name="safeReturn">可选权威返回投影。</param>
        internal ClientWorldFlowSnapshot(
            ClientWorldFlowState state,
            long targetGeneration,
            ClientWorldFlowFailure failure,
            string visitSessionID,
            ClientSafeReturnProjection safeReturn)
        {
            State = state;
            TargetGeneration = targetGeneration;
            Failure = failure;
            VisitSessionID = visitSessionID;
            SafeReturn = safeReturn;
        }

        /// <summary>获取 current flow 状态。</summary>
        internal ClientWorldFlowState State { get; }

        /// <summary>获取阻止旧异步结果提交的 target generation。</summary>
        internal long TargetGeneration { get; }

        /// <summary>获取最近稳定低敏失败类别。</summary>
        internal ClientWorldFlowFailure Failure { get; }

        /// <summary>获取 current Visitor target VisitSessionID。</summary>
        internal string VisitSessionID { get; }

        /// <summary>获取可选权威安全返回投影。</summary>
        internal ClientSafeReturnProjection SafeReturn { get; }
    }
}
