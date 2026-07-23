using System;

namespace IHomeland.Client.Application.World
{
    /// <summary>纯计算同 identity invite 的 revision replacement 决议。</summary>
    internal sealed class VisitInviteInboxReducer
    {
        /// <summary>比较 current 与 incoming invite candidate。</summary>
        internal ClientProjectionApplyResult Compare(
            ClientVisitInviteProjection current,
            ClientVisitInviteProjection incoming)
        {
            if (incoming == null)
            {
                return ClientProjectionApplyResult.Rejected;
            }

            if (current == null)
            {
                return ClientProjectionApplyResult.Applied;
            }

            if (incoming.CreatedRevision < current.CreatedRevision)
            {
                return ClientProjectionApplyResult.Stale;
            }

            if (incoming.CreatedRevision == current.CreatedRevision)
            {
                return current.IsEquivalent(incoming)
                    ? ClientProjectionApplyResult.Duplicate
                    : ClientProjectionApplyResult.Conflict;
            }

            return ClientProjectionApplyResult.Applied;
        }
    }

    /// <summary>纯计算 invite expiry、replacement 与 member-consumption retirement。</summary>
    internal sealed class VisitInviteRetirementPolicy
    {
        /// <summary>判断 pending invite 在当前时间是否已失效。</summary>
        internal bool IsExpired(
            ClientVisitInviteProjection invite,
            long utcNowMilliseconds)
        {
            return invite != null &&
                   invite.State == ClientVisitInviteState.Pending &&
                   invite.ExpiresAtMilliseconds <= utcNowMilliseconds;
        }

        /// <summary>判断 incoming 是否证明旧 pending target identity 已被替换。</summary>
        internal bool IsSuperseded(
            ClientVisitInviteProjection current,
            ClientVisitInviteProjection incoming)
        {
            return current != null && incoming != null &&
                   current.State == ClientVisitInviteState.Pending &&
                   current.CreatedRevision < incoming.CreatedRevision &&
                   string.Equals(
                       current.VisitSessionID,
                       incoming.VisitSessionID,
                       StringComparison.Ordinal) &&
                   string.Equals(
                       current.OwnerPlayerID,
                       incoming.OwnerPlayerID,
                       StringComparison.Ordinal) &&
                   string.Equals(
                       current.TargetVisitorID,
                       incoming.TargetVisitorID,
                       StringComparison.Ordinal);
        }

        /// <summary>判断 Owner member replacement 是否已经消费 outgoing invite。</summary>
        internal bool IsConsumedBy(
            ClientVisitInviteProjection invite,
            ClientVisitSessionProjection snapshot)
        {
            if (invite == null || snapshot == null ||
                !string.Equals(
                    invite.VisitSessionID,
                    snapshot.VisitSessionID,
                    StringComparison.Ordinal))
            {
                return false;
            }

            foreach (var visitor in snapshot.Visitors)
            {
                if (string.Equals(
                        visitor.PlayerID,
                        invite.TargetVisitorID,
                        StringComparison.Ordinal))
                {
                    return true;
                }
            }

            return false;
        }
    }
}
