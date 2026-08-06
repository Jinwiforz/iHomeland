using System;

namespace IHomeland.Client.PersonalWorld.Application
{
    /// <summary>
    /// 纯计算 VisitSession revision、immutable identity 与 assignment replacement 决议。
    /// </summary>
    internal sealed class VisitSessionProjectionReducer
    {
        /// <summary>比较 current 与 incoming 完整 VisitSession projection。</summary>
        internal ClientProjectionApplyResult Compare(
            ClientVisitSessionProjection current,
            ClientVisitSessionProjection incoming)
        {
            if (incoming == null)
            {
                return ClientProjectionApplyResult.Rejected;
            }

            if (current == null)
            {
                return ClientProjectionApplyResult.Applied;
            }

            if (!string.Equals(
                    current.VisitSessionID,
                    incoming.VisitSessionID,
                    StringComparison.Ordinal))
            {
                return ClientProjectionApplyResult.Conflict;
            }

            if (incoming.Revision < current.Revision)
            {
                return ClientProjectionApplyResult.Stale;
            }

            if (incoming.Revision == current.Revision)
            {
                if (!current.IsEquivalentIgnoringAssignment(incoming))
                {
                    return ClientProjectionApplyResult.Conflict;
                }

                return CompareAssignment(current.Assignment, incoming.Assignment);
            }

            if (!string.Equals(
                    current.OwnerPlayerID,
                    incoming.OwnerPlayerID,
                    StringComparison.Ordinal) ||
                current.CreatedAtMilliseconds != incoming.CreatedAtMilliseconds ||
                current.ExpiresAtMilliseconds != incoming.ExpiresAtMilliseconds ||
                current.Role != incoming.Role)
            {
                return ClientProjectionApplyResult.Conflict;
            }

            var assignment = CompareAssignment(
                current.Assignment,
                incoming.Assignment);
            return assignment == ClientProjectionApplyResult.Conflict ||
                   assignment == ClientProjectionApplyResult.Stale
                ? assignment
                : ClientProjectionApplyResult.Applied;
        }

        /// <summary>比较 VisitSession placement 的独立 assignment generation。</summary>
        internal ClientProjectionApplyResult CompareAssignment(
            ClientWorldAssignmentProjection current,
            ClientWorldAssignmentProjection incoming)
        {
            if (current == null || incoming == null ||
                !string.Equals(
                    current.PersonalWorldID,
                    incoming.PersonalWorldID,
                    StringComparison.Ordinal))
            {
                return ClientProjectionApplyResult.Conflict;
            }

            if (incoming.Generation < current.Generation)
            {
                return ClientProjectionApplyResult.Stale;
            }

            if (incoming.Generation > current.Generation)
            {
                return ClientProjectionApplyResult.Applied;
            }

            if (!current.HasSameIdentity(incoming))
            {
                return ClientProjectionApplyResult.Conflict;
            }

            if (incoming.LeaseExpiresAtMilliseconds <
                current.LeaseExpiresAtMilliseconds)
            {
                return ClientProjectionApplyResult.Stale;
            }

            return incoming.LeaseExpiresAtMilliseconds ==
                   current.LeaseExpiresAtMilliseconds
                ? ClientProjectionApplyResult.Duplicate
                : ClientProjectionApplyResult.Applied;
        }
    }
}
