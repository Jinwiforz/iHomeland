using System;
using System.Collections.Generic;
using System.Text;
using IHomeland.Client.Infrastructure.Http;
using IHomeland.Protocol.Session.V1;
using IHomeland.Protocol.Visit.V1;
using IHomeland.Protocol.World.V1;

namespace IHomeland.Client.Application.World
{
    /// <summary>
    /// 表示服务端公开投影违反客户端冻结合同。
    /// </summary>
    internal sealed class ClientWorldProjectionException : Exception
    {
        /// <summary>创建不携带 payload、endpoint 或 credential 的稳定校验异常。</summary>
        /// <param name="message">低敏合同失败说明。</param>
        internal ClientWorldProjectionException(string message)
            : base(message)
        {
        }
    }

    /// <summary>
    /// 在网络边界把 generated/HTTP model 完整复制为 application-owned 不可变投影。
    /// </summary>
    internal static class ClientWorldProjectionMapper
    {
        /// <summary>协议 identity 的最大 UTF-8 字节数。</summary>
        private const int IdentityMaximumBytes = 128;

        /// <summary>公开 endpoint host 的最大 UTF-8 字节数。</summary>
        private const int HostMaximumBytes = 253;

        /// <summary>将 own-world HTTP bootstrap 转换为完整 primary world 投影。</summary>
        /// <param name="bootstrap">Session owner 验证后的 HTTP 结果。</param>
        /// <returns>完整不可变 PersonalWorld 投影。</returns>
        internal static ClientPersonalWorldProjection FromBootstrap(ClientWorldBootstrap bootstrap)
        {
            if (bootstrap?.World == null)
            {
                throw Invalid("World bootstrap 缺少 world。");
            }

            var world = bootstrap.World;
            var worldID = RequireIdentity(world.PersonalWorldID, "PersonalWorldID");
            var ownerID = RequireIdentity(world.OwnerPlayerID, "OwnerPlayerID");
            var lifecycle = world.Lifecycle == ClientPersonalWorldLifecycle.Active
                ? ClientWorldLifecycle.Active
                : world.Lifecycle == ClientPersonalWorldLifecycle.Archived
                    ? ClientWorldLifecycle.Archived
                    : throw Invalid("World lifecycle 未登记。");
            var revision = RequirePositive(world.Revision, "world revision");
            RequirePositiveTimestamp(world.CreatedAtMilliseconds, "world createdAt");
            var assignment = bootstrap.Assignment == null
                ? null
                : FromHttpAssignment(bootstrap.Assignment, worldID);
            if (lifecycle == ClientWorldLifecycle.Archived && assignment != null)
            {
                throw Invalid("Archived PersonalWorld 不能携带 active assignment。");
            }

            return new ClientPersonalWorldProjection(
                worldID,
                ownerID,
                lifecycle,
                revision,
                world.CreatedAtMilliseconds,
                assignment);
        }

        /// <summary>将 TLS/TCP 完整 world snapshot 转换为不可变 replacement。</summary>
        /// <param name="snapshot">Generated 完整快照。</param>
        /// <returns>完整不可变 PersonalWorld 投影。</returns>
        internal static ClientPersonalWorldProjection FromWorldSnapshot(WorldSnapshot snapshot)
        {
            if (snapshot?.World == null)
            {
                throw Invalid("World snapshot 缺少 world。");
            }

            var world = snapshot.World;
            var worldID = RequireIdentity(world.PersonalWorldId, "PersonalWorldID");
            var lifecycle = world.Lifecycle == PersonalWorldLifecycle.Active
                ? ClientWorldLifecycle.Active
                : world.Lifecycle == PersonalWorldLifecycle.Archived
                    ? ClientWorldLifecycle.Archived
                    : throw Invalid("World lifecycle 未登记。");
            if (world.Revision == 0)
            {
                throw Invalid("World revision 必须为正数。");
            }

            RequirePositiveTimestamp(world.CreatedAtMs, "world createdAt");
            var assignment = snapshot.Assignment == null
                ? null
                : FromGeneratedAssignment(snapshot.Assignment, worldID);
            if (lifecycle == ClientWorldLifecycle.Archived && assignment != null)
            {
                throw Invalid("Archived PersonalWorld 不能携带 active assignment。");
            }

            return new ClientPersonalWorldProjection(
                worldID,
                RequireIdentity(world.OwnerPlayerId, "OwnerPlayerID"),
                lifecycle,
                world.Revision,
                world.CreatedAtMs,
                assignment);
        }

        /// <summary>将 generated VisitSession snapshot 转换为完整不可变 replacement。</summary>
        /// <param name="snapshot">Generated 完整 VisitSession 快照。</param>
        /// <param name="role">由 current admission flow 建立的角色。</param>
        /// <returns>包含稳定排序 Visitor 集合的不可变投影。</returns>
        internal static ClientVisitSessionProjection FromVisitSnapshot(
            VisitSessionSnapshot snapshot,
            ClientVisitRole role)
        {
            if (snapshot?.Assignment == null)
            {
                throw Invalid("VisitSession snapshot 缺少 assignment。");
            }

            if (role != ClientVisitRole.Owner && role != ClientVisitRole.Visitor)
            {
                throw Invalid("VisitSession role 未登记。");
            }

            var visitID = RequireIdentity(snapshot.VisitSessionId, "VisitSessionID");
            var ownerID = RequireIdentity(snapshot.OwnerPlayerId, "OwnerPlayerID");
            var lifecycle = MapVisitLifecycle(snapshot.Lifecycle);
            if (snapshot.Revision == 0 || snapshot.Capacity < 1 || snapshot.Capacity > 32)
            {
                throw Invalid("VisitSession revision 或 capacity 无效。");
            }

            if (snapshot.Visitors.Count > 32 || snapshot.Visitors.Count > snapshot.Capacity)
            {
                throw Invalid("VisitSession visitor 数量超过上限。");
            }

            RequirePositiveTimestamp(snapshot.CreatedAtMs, "visit createdAt");
            RequirePositiveTimestamp(snapshot.ExpiresAtMs, "visit expiresAt");
            if (snapshot.ExpiresAtMs <= snapshot.CreatedAtMs)
            {
                throw Invalid("VisitSession expiry 必须晚于创建时间。");
            }

            if ((lifecycle == ClientVisitLifecycle.OwnerGrace) !=
                (snapshot.OwnerGraceExpiresAtMs > 0))
            {
                throw Invalid("Owner grace deadline 与 lifecycle 不一致。");
            }

            var visitors = new List<ClientVisitVisitorProjection>(snapshot.Visitors.Count);
            string previousID = null;
            foreach (var visitor in snapshot.Visitors)
            {
                if (visitor == null)
                {
                    throw Invalid("VisitSession visitor 不能为空。");
                }

                var playerID = RequireIdentity(visitor.PlayerId, "Visitor PlayerID");
                if (string.Equals(playerID, ownerID, StringComparison.Ordinal) ||
                    (previousID != null && string.CompareOrdinal(previousID, playerID) >= 0))
                {
                    throw Invalid("VisitSession visitors 必须稳定排序、唯一且不包含 Owner。");
                }

                visitors.Add(new ClientVisitVisitorProjection(
                    playerID,
                    MapMembershipState(visitor.State)));
                previousID = playerID;
            }

            return new ClientVisitSessionProjection(
                visitID,
                ownerID,
                FromGeneratedAssignment(snapshot.Assignment, snapshot.Assignment.PersonalWorldId),
                lifecycle,
                snapshot.Revision,
                snapshot.Capacity,
                visitors,
                snapshot.CreatedAtMs,
                snapshot.ExpiresAtMs,
                snapshot.OwnerGraceExpiresAtMs,
                role);
        }

        /// <summary>转换定向 WSS invite，保留服务端 target identity 与 expiry。</summary>
        /// <param name="push">Generated invite PUSH。</param>
        /// <returns>不可变 invite 投影。</returns>
        internal static ClientVisitInviteProjection FromInvitePush(VisitInvitePush push)
        {
            if (push?.Invite == null)
            {
                throw Invalid("Visit invite PUSH 缺少 invite。");
            }

            return FromInviteSummary(push.Invite, push.OwnerPlayerId);
        }

        /// <summary>转换 mutation response 或 PUSH 共享的定向邀请摘要。</summary>
        /// <param name="invite">Generated 公开邀请摘要。</param>
        /// <param name="ownerPlayerID">由 response snapshot 或 PUSH envelope 证明的 Owner Player 标识。</param>
        /// <returns>不可变 invite 投影。</returns>
        internal static ClientVisitInviteProjection FromInviteSummary(
            VisitInviteSummary invite,
            string ownerPlayerID)
        {
            if (invite == null)
            {
                throw Invalid("Visit invite summary 不能为空。");
            }

            if (invite.CreatedRevision == 0)
            {
                throw Invalid("Invite created revision 必须为正数。");
            }

            RequirePositiveTimestamp(invite.ExpiresAtMs, "invite expiresAt");
            return new ClientVisitInviteProjection(
                RequireIdentity(invite.InviteId, "InviteID"),
                RequireIdentity(invite.VisitSessionId, "VisitSessionID"),
                RequireIdentity(ownerPlayerID, "OwnerPlayerID"),
                RequireIdentity(invite.TargetVisitorId, "TargetVisitorID"),
                MapInviteState(invite.State),
                invite.CreatedRevision,
                invite.ExpiresAtMs);
        }

        /// <summary>转换 WSS assignment hint；缺失 assignment 表示完整撤销提示。</summary>
        /// <param name="push">Generated assignment changed PUSH。</param>
        /// <param name="personalWorldID">转换后受信 PersonalWorldID。</param>
        /// <returns>可选 assignment hint。</returns>
        internal static ClientWorldAssignmentProjection FromAssignmentHint(
            WorldAssignmentChangedPush push,
            out string personalWorldID)
        {
            if (push == null)
            {
                throw Invalid("Assignment PUSH 不能为空。");
            }

            personalWorldID = RequireIdentity(push.PersonalWorldId, "PersonalWorldID");
            return push.Assignment == null
                ? null
                : FromGeneratedAssignment(push.Assignment, personalWorldID);
        }

        /// <summary>转换 Owner availability control hint，不冒充完整 VisitSession snapshot。</summary>
        /// <param name="push">Generated availability PUSH。</param>
        /// <returns>不可变控制面提示。</returns>
        internal static ClientVisitControlHint FromOwnerAvailability(VisitOwnerAvailabilityPush push)
        {
            if (push == null || push.Revision == 0 ||
                (push.Available && push.GraceExpiresAtMs != 0) ||
                (!push.Available && push.GraceExpiresAtMs <= 0))
            {
                throw Invalid("Owner availability PUSH 无效。");
            }

            return new ClientVisitControlHint(
                RequireIdentity(push.VisitSessionId, "VisitSessionID"),
                push.Revision,
                push.Available,
                push.GraceExpiresAtMs,
                null);
        }

        /// <summary>转换 terminal close control hint，不伪造 gameplay safe-return。</summary>
        /// <param name="push">Generated close notice PUSH。</param>
        /// <returns>不可变控制面提示。</returns>
        internal static ClientVisitControlHint FromClosedNotice(VisitClosedNoticePush push)
        {
            if (push == null || push.Revision == 0)
            {
                throw Invalid("Visit closed notice 无效。");
            }

            return new ClientVisitControlHint(
                RequireIdentity(push.VisitSessionId, "VisitSessionID"),
                push.Revision,
                null,
                0,
                MapSafeReturnReason(push.Reason));
        }

        /// <summary>转换 gameplay safe-return 权威指令。</summary>
        /// <param name="directive">Generated 权威返回指令。</param>
        /// <returns>不可变安全返回投影。</returns>
        internal static ClientSafeReturnProjection FromSafeReturn(SafeReturnDirective directive)
        {
            if (directive == null)
            {
                throw Invalid("Safe-return directive 不能为空。");
            }

            var preferred = MapSafeReturnDestination(directive.Preferred);
            var fallback = MapSafeReturnDestination(directive.Fallback);
            if (preferred == fallback)
            {
                throw Invalid("Safe-return preferred 与 fallback 必须不同。");
            }

            if (directive.Revision == 0)
            {
                throw Invalid("Safe-return revision 无效。");
            }

            return new ClientSafeReturnProjection(
                RequireIdentity(directive.VisitSessionId, "VisitSessionID"),
                RequireIdentity(directive.VisitorId, "VisitorID"),
                MapSafeReturnReason(directive.Reason),
                preferred,
                fallback,
                directive.Revision);
        }

        /// <summary>把 HTTP assignment 转换为统一 application projection。</summary>
        /// <param name="assignment">HTTP assignment。</param>
        /// <param name="expectedWorldID">外层 world identity。</param>
        /// <returns>已验证 assignment。</returns>
        private static ClientWorldAssignmentProjection FromHttpAssignment(
            ClientWorldAssignment assignment,
            string expectedWorldID)
        {
            var worldID = RequireIdentity(assignment.PersonalWorldID, "Assignment PersonalWorldID");
            if (!string.Equals(worldID, expectedWorldID, StringComparison.Ordinal) ||
                assignment.Endpoint == null ||
                assignment.Endpoint.Channel != ClientEndpointChannel.TlsTcp)
            {
                throw Invalid("HTTP assignment binding 或 channel 无效。");
            }

            return new ClientWorldAssignmentProjection(
                worldID,
                RequireIdentity(assignment.WorldInstanceID, "WorldInstanceID"),
                MapEndpoint(assignment.Endpoint.Host, assignment.Endpoint.Port),
                RequirePositive(assignment.Generation, "assignment generation"),
                RequirePositiveTimestamp(assignment.LeaseExpiresAtMilliseconds, "assignment lease"));
        }

        /// <summary>把 generated assignment 转换为统一 application projection。</summary>
        /// <param name="assignment">Generated assignment。</param>
        /// <param name="expectedWorldID">外层绑定 world identity。</param>
        /// <returns>已验证 assignment。</returns>
        private static ClientWorldAssignmentProjection FromGeneratedAssignment(
            WorldAssignment assignment,
            string expectedWorldID)
        {
            if (assignment?.Endpoint == null ||
                assignment.Endpoint.Channel != TransportChannel.TlsTcp ||
                assignment.Endpoint.Port > 65535)
            {
                throw Invalid("Generated assignment 缺少 TLS/TCP endpoint。");
            }

            var worldID = RequireIdentity(assignment.PersonalWorldId, "Assignment PersonalWorldID");
            expectedWorldID = RequireIdentity(expectedWorldID, "Expected PersonalWorldID");
            if (!string.Equals(worldID, expectedWorldID, StringComparison.Ordinal) || assignment.Generation == 0)
            {
                throw Invalid("Generated assignment binding 或 generation 无效。");
            }

            RequirePositiveTimestamp(assignment.LeaseExpiresAtMs, "assignment lease");
            return new ClientWorldAssignmentProjection(
                worldID,
                RequireIdentity(assignment.WorldInstanceId, "WorldInstanceID"),
                MapEndpoint(assignment.Endpoint.Host, (int)assignment.Endpoint.Port),
                assignment.Generation,
                assignment.LeaseExpiresAtMs);
        }

        /// <summary>验证 host/port 并创建不可变 endpoint。</summary>
        /// <param name="host">受信 manifest host。</param>
        /// <param name="port">TCP port。</param>
        /// <returns>已验证 endpoint。</returns>
        private static ClientWorldEndpointProjection MapEndpoint(string host, int port)
        {
            if (string.IsNullOrWhiteSpace(host) || Encoding.UTF8.GetByteCount(host) > HostMaximumBytes ||
                port < 1 || port > 65535)
            {
                throw Invalid("World endpoint 无效。");
            }

            return new ClientWorldEndpointProjection(host, port);
        }

        /// <summary>判断公开 identity 是否符合非空、长度与安全 ASCII 字符集约束。</summary>
        /// <param name="value">待验证 identity。</param>
        /// <returns>Identity 可安全进入协议 command 或投影时返回 true。</returns>
        internal static bool IsValidIdentity(string value)
        {
            if (string.IsNullOrEmpty(value) || value.Length > IdentityMaximumBytes)
            {
                return false;
            }

            foreach (var character in value)
            {
                var valid = character >= 'a' && character <= 'z' ||
                            character >= 'A' && character <= 'Z' ||
                            character >= '0' && character <= '9' ||
                            character == '.' || character == '_' || character == ':' || character == '-';
                if (!valid)
                {
                    return false;
                }
            }

            return true;
        }

        /// <summary>验证公开 identity 的非空、长度与安全 ASCII 字符集。</summary>
        /// <param name="value">待验证 identity。</param>
        /// <param name="name">低敏字段名。</param>
        /// <returns>原 identity。</returns>
        private static string RequireIdentity(string value, string name)
        {
            if (!IsValidIdentity(value))
            {
                throw Invalid(name + " 无效。");
            }

            return value;
        }

        /// <summary>验证 signed long 正数并安全转换为 ulong。</summary>
        /// <param name="value">待验证数值。</param>
        /// <param name="name">低敏字段名。</param>
        /// <returns>正 ulong。</returns>
        private static ulong RequirePositive(long value, string name)
        {
            if (value <= 0)
            {
                throw Invalid(name + " 必须为正数。");
            }

            return checked((ulong)value);
        }

        /// <summary>验证 Unix millisecond timestamp 为正数。</summary>
        /// <param name="value">Unix millisecond。</param>
        /// <param name="name">低敏字段名。</param>
        /// <returns>原时间值。</returns>
        private static long RequirePositiveTimestamp(long value, string name)
        {
            if (value <= 0)
            {
                throw Invalid(name + " 必须为正数。");
            }

            return value;
        }

        /// <summary>映射封闭 Visit lifecycle。</summary>
        /// <param name="value">Generated enum。</param>
        /// <returns>Application enum。</returns>
        private static ClientVisitLifecycle MapVisitLifecycle(VisitLifecycle value)
        {
            switch (value)
            {
                case VisitLifecycle.Open: return ClientVisitLifecycle.Open;
                case VisitLifecycle.OwnerGrace: return ClientVisitLifecycle.OwnerGrace;
                case VisitLifecycle.Closed: return ClientVisitLifecycle.Closed;
                default: throw Invalid("Visit lifecycle 未登记。");
            }
        }

        /// <summary>映射封闭 membership state。</summary>
        /// <param name="value">Generated enum。</param>
        /// <returns>Application enum。</returns>
        private static ClientVisitMembershipState MapMembershipState(VisitMembershipState value)
        {
            switch (value)
            {
                case VisitMembershipState.Reserved: return ClientVisitMembershipState.Reserved;
                case VisitMembershipState.Joined: return ClientVisitMembershipState.Joined;
                case VisitMembershipState.Reconnecting: return ClientVisitMembershipState.Reconnecting;
                default: throw Invalid("Visit membership state 未登记。");
            }
        }

        /// <summary>映射封闭 invite state。</summary>
        /// <param name="value">Generated enum。</param>
        /// <returns>Application enum。</returns>
        private static ClientVisitInviteState MapInviteState(VisitInviteState value)
        {
            switch (value)
            {
                case VisitInviteState.Pending: return ClientVisitInviteState.Pending;
                case VisitInviteState.Accepted: return ClientVisitInviteState.Accepted;
                case VisitInviteState.Retired: return ClientVisitInviteState.Retired;
                default: throw Invalid("Visit invite state 未登记。");
            }
        }

        /// <summary>映射封闭 safe-return reason。</summary>
        /// <param name="value">Generated enum。</param>
        /// <returns>Application enum。</returns>
        private static ClientSafeReturnReason MapSafeReturnReason(SafeReturnReason value)
        {
            switch (value)
            {
                case SafeReturnReason.VoluntaryLeave: return ClientSafeReturnReason.VoluntaryLeave;
                case SafeReturnReason.Kicked: return ClientSafeReturnReason.Kicked;
                case SafeReturnReason.OwnerClosed: return ClientSafeReturnReason.OwnerClosed;
                case SafeReturnReason.OwnerUnavailable: return ClientSafeReturnReason.OwnerUnavailable;
                case SafeReturnReason.SessionExpired: return ClientSafeReturnReason.SessionExpired;
                case SafeReturnReason.AssignmentChanged: return ClientSafeReturnReason.AssignmentChanged;
                case SafeReturnReason.VisitorReconnectExpired: return ClientSafeReturnReason.VisitorReconnectExpired;
                case SafeReturnReason.DependencyLost: return ClientSafeReturnReason.DependencyLost;
                default: throw Invalid("Safe-return reason 未登记。");
            }
        }

        /// <summary>映射封闭 safe-return destination。</summary>
        /// <param name="value">Generated enum。</param>
        /// <returns>Application enum。</returns>
        private static ClientSafeReturnDestination MapSafeReturnDestination(SafeReturnDestination value)
        {
            switch (value)
            {
                case SafeReturnDestination.OwnPersonalWorld: return ClientSafeReturnDestination.OwnPersonalWorld;
                case SafeReturnDestination.SafeEntry: return ClientSafeReturnDestination.SafeEntry;
                default: throw Invalid("Safe-return destination 未登记。");
            }
        }

        /// <summary>创建统一低敏 projection exception。</summary>
        /// <param name="message">失败说明。</param>
        /// <returns>Projection exception。</returns>
        private static ClientWorldProjectionException Invalid(string message)
        {
            return new ClientWorldProjectionException(message);
        }
    }
}
