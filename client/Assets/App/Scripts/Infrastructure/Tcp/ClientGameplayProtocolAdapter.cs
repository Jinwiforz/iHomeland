using System;
using IHomeland.Client.Application.Gameplay;
using IHomeland.Client.Application.World;
using IHomeland.Protocol.Visit.V1;
using IHomeland.Protocol.World.V1;

namespace IHomeland.Client.Infrastructure.Tcp
{
    /// <summary>
    /// 把 TLS/TCP generated response/PUSH 映射为无 Protocol 依赖的 Application contract。
    /// </summary>
    internal sealed class ClientGameplayProtocolAdapter
    {
        /// <summary>映射完整 PersonalWorld replacement。</summary>
        internal ClientPersonalWorldProjection MapWorld(WorldSnapshot snapshot)
        {
            return ClientWorldProjectionMapper.FromWorldSnapshot(snapshot);
        }

        /// <summary>映射带 current target role 的完整 VisitSession replacement。</summary>
        internal ClientVisitSessionProjection MapVisit(
            VisitSessionSnapshot snapshot,
            ClientVisitRole role)
        {
            return ClientWorldProjectionMapper.FromVisitSnapshot(snapshot, role);
        }

        /// <summary>映射 Visit mutation 的 snapshot、可选 invite 与可选 safe-return。</summary>
        internal ClientVisitMutationCandidate MapMutation(
            VisitMutationResult mutation,
            ClientVisitRole role,
            VisitInviteSummary invite = null)
        {
            if (mutation?.Snapshot == null)
            {
                throw new ClientWorldProjectionException(
                    "Visit mutation 缺少完整 snapshot。");
            }

            var snapshot = MapVisit(mutation.Snapshot, role);
            ClientVisitInviteProjection inviteProjection = null;
            if (invite != null)
            {
                inviteProjection = ClientWorldProjectionMapper.FromInviteSummary(
                    invite,
                    snapshot.OwnerPlayerID);
            }

            ClientSafeReturnProjection safeReturn = null;
            if (mutation.SafeReturns.Count == 1)
            {
                safeReturn = ClientWorldProjectionMapper.FromSafeReturn(
                    mutation.SafeReturns[0]);
            }

            return new ClientVisitMutationCandidate(
                snapshot,
                inviteProjection,
                safeReturn);
        }

        /// <summary>映射 gameplay safe-return PUSH。</summary>
        internal ClientSafeReturnProjection MapSafeReturn(
            SafeReturnDirective directive)
        {
            return ClientWorldProjectionMapper.FromSafeReturn(directive);
        }
    }
}
