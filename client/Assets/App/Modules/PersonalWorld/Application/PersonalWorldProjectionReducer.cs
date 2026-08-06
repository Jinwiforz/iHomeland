using System;

namespace IHomeland.Client.PersonalWorld.Application
{
    /// <summary>
    /// 纯计算 PersonalWorld revision 与 assignment generation replacement 决议。
    /// </summary>
    internal sealed class PersonalWorldProjectionReducer
    {
        /// <summary>比较 current 与 incoming 完整 world projection。</summary>
        internal ClientProjectionApplyResult Compare(
            ClientPersonalWorldProjection current,
            ClientPersonalWorldProjection incoming)
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
                    current.PersonalWorldID,
                    incoming.PersonalWorldID,
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
                if (!current.HasSameWorldFacts(incoming))
                {
                    return ClientProjectionApplyResult.Conflict;
                }

                return CompareAssignment(current.Assignment, incoming.Assignment);
            }

            if (!string.Equals(
                    current.OwnerPlayerID,
                    incoming.OwnerPlayerID,
                    StringComparison.Ordinal) ||
                current.CreatedAtMilliseconds != incoming.CreatedAtMilliseconds)
            {
                return ClientProjectionApplyResult.Conflict;
            }

            return ClientProjectionApplyResult.Applied;
        }

        /// <summary>比较 assignment generation、identity 与 lease renewal。</summary>
        internal ClientProjectionApplyResult CompareAssignment(
            ClientWorldAssignmentProjection current,
            ClientWorldAssignmentProjection incoming)
        {
            if (incoming == null)
            {
                return current == null
                    ? ClientProjectionApplyResult.Duplicate
                    : ClientProjectionApplyResult.Applied;
            }

            if (current == null)
            {
                return ClientProjectionApplyResult.Applied;
            }

            if (incoming.Generation < current.Generation)
            {
                return ClientProjectionApplyResult.Stale;
            }

            if (incoming.Generation == current.Generation)
            {
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

            return ClientProjectionApplyResult.Applied;
        }
    }
}
