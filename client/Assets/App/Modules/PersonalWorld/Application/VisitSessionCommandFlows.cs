using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Networking.Application.Gameplay;
using IHomeland.Client.Networking.Application.Ports;

namespace IHomeland.Client.PersonalWorld.Application
{
    /// <summary>
    /// 执行登记的 VisitSession typed commands，不保存 VisitSession 或 inbox 最终事实。
    /// </summary>
    internal sealed class VisitSessionCommandFlows
    {
        /// <summary>保存只提供登记 operation 的 gameplay port。</summary>
        private readonly IClientGameplayChannelPort _gameplay;

        /// <summary>创建 VisitSession command flows。</summary>
        internal VisitSessionCommandFlows(IClientGameplayChannelPort gameplay)
        {
            _gameplay = gameplay ?? throw new ArgumentNullException(nameof(gameplay));
        }

        /// <summary>执行 Open command。</summary>
        internal Task<ClientGameplayResult<ClientVisitMutationCandidate>> OpenAsync(
            CancellationToken cancellationToken)
        {
            return _gameplay.OpenVisitAsync(cancellationToken);
        }

        /// <summary>执行 CreateInvite command。</summary>
        internal Task<ClientGameplayResult<ClientVisitMutationCandidate>> CreateInviteAsync(
            ClientCreateVisitInviteRequest request,
            CancellationToken cancellationToken)
        {
            return _gameplay.CreateVisitInviteAsync(request, cancellationToken);
        }

        /// <summary>执行 RevokeInvite command。</summary>
        internal Task<ClientGameplayResult<ClientVisitMutationCandidate>> RevokeAsync(
            ClientRevisionedIdentityRequest request,
            CancellationToken cancellationToken)
        {
            return _gameplay.RevokeVisitInviteAsync(request, cancellationToken);
        }

        /// <summary>执行 Kick command。</summary>
        internal Task<ClientGameplayResult<ClientVisitMutationCandidate>> KickAsync(
            ClientRevisionedIdentityRequest request,
            CancellationToken cancellationToken)
        {
            return _gameplay.KickVisitMemberAsync(request, cancellationToken);
        }

        /// <summary>执行 Close command。</summary>
        internal Task<ClientGameplayResult<ClientVisitMutationCandidate>> CloseAsync(
            ulong expectedRevision,
            CancellationToken cancellationToken)
        {
            return _gameplay.CloseVisitAsync(expectedRevision, cancellationToken);
        }

        /// <summary>执行 Leave command。</summary>
        internal Task<ClientGameplayResult<ClientVisitMutationCandidate>> LeaveAsync(
            ulong expectedRevision,
            CancellationToken cancellationToken)
        {
            return _gameplay.LeaveVisitAsync(expectedRevision, cancellationToken);
        }
    }
}
