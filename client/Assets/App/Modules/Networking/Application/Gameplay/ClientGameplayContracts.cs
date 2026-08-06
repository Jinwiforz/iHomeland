using System;
using IHomeland.Client.PersonalWorld.Application;

namespace IHomeland.Client.Networking.Application.Gameplay
{
    /// <summary>标识 gameplay connection 的低敏 terminal 分类。</summary>
    internal enum ClientGameplayDisconnectKind
    {
        /// <summary>尚未发生 terminal。</summary>
        None = 0,
        /// <summary>调用方、shutdown 或权威业务返回触发关闭。</summary>
        Requested = 1,
        /// <summary>Frame、route、correlation 或 projection 违反合同。</summary>
        Protocol = 2,
        /// <summary>Socket、TLS、timeout、heartbeat 或 backpressure 失败。</summary>
        Transport = 3,
        /// <summary>Session authority 使 connection 失效。</summary>
        SessionInvalidated = 4,
    }

    /// <summary>保存 gameplay channel 的平台无关低敏 health。</summary>
    internal sealed class ClientGameplayHealthSnapshot
    {
        /// <summary>创建 health snapshot。</summary>
        internal ClientGameplayHealthSnapshot(
            bool connected,
            bool active,
            long generation,
            ClientGameplayDisconnectKind disconnectKind)
        {
            Connected = connected;
            Active = active;
            Generation = generation;
            DisconnectKind = disconnectKind;
        }

        /// <summary>获取 current generation 是否拥有连接资源。</summary>
        internal bool Connected { get; }
        /// <summary>获取 current generation 是否允许登记 operation。</summary>
        internal bool Active { get; }
        /// <summary>获取 current connection generation。</summary>
        internal long Generation { get; }
        /// <summary>获取 recent terminal 的稳定低敏分类。</summary>
        internal ClientGameplayDisconnectKind DisconnectKind { get; }
    }

    /// <summary>保存创建定向邀请的强类型参数。</summary>
    internal sealed class ClientCreateVisitInviteRequest
    {
        /// <summary>创建不可变邀请请求。</summary>
        internal ClientCreateVisitInviteRequest(string targetVisitorID, uint lifetimeMilliseconds, ulong expectedRevision)
        {
            TargetVisitorID = targetVisitorID ??
                throw new ArgumentNullException(nameof(targetVisitorID));
            LifetimeMilliseconds = lifetimeMilliseconds;
            ExpectedRevision = expectedRevision;
        }

        /// <summary>获取目标 Visitor identity。</summary>
        internal string TargetVisitorID { get; }
        /// <summary>获取请求有效期毫秒数。</summary>
        internal uint LifetimeMilliseconds { get; }
        /// <summary>获取 optimistic concurrency revision。</summary>
        internal ulong ExpectedRevision { get; }
    }

    /// <summary>保存带 aggregate revision 的单个业务 identity。</summary>
    internal sealed class ClientRevisionedIdentityRequest
    {
        /// <summary>创建不可变 identity 请求。</summary>
        internal ClientRevisionedIdentityRequest(string identity, ulong expectedRevision)
        {
            Identity = identity ?? throw new ArgumentNullException(nameof(identity));
            ExpectedRevision = expectedRevision;
        }

        /// <summary>获取 invite 或 Visitor identity。</summary>
        internal string Identity { get; }
        /// <summary>获取 optimistic concurrency revision。</summary>
        internal ulong ExpectedRevision { get; }
    }

    /// <summary>保存 Visit mutation 提交后的完整 candidate projection。</summary>
    internal sealed class ClientVisitMutationCandidate
    {
        /// <summary>创建 mutation candidate。</summary>
        internal ClientVisitMutationCandidate(
            ClientVisitSessionProjection snapshot,
            ClientVisitInviteProjection invite,
            ClientSafeReturnProjection safeReturn)
        {
            Snapshot = snapshot;
            Invite = invite;
            SafeReturn = safeReturn;
        }

        /// <summary>获取可选完整 VisitSession replacement。</summary>
        internal ClientVisitSessionProjection Snapshot { get; }
        /// <summary>获取 create-invite operation 的可选邀请。</summary>
        internal ClientVisitInviteProjection Invite { get; }
        /// <summary>获取 close/leave 的可选 safe-return。</summary>
        internal ClientSafeReturnProjection SafeReturn { get; }
    }
}
