using IHomeland.Client.PersonalWorld.Application;
using IHomeland.Client.Networking.Infrastructure.Tcp;
using IHomeland.Protocol.Visit.V1;
using IHomeland.Protocol.World.V1;

namespace IHomeland.Client.PersonalWorld.Tests.EditMode
{
    /// <summary>
    /// 仅供 protocol fixture 测试把 generated payload 显式经过 Infrastructure mapper 后送入 Application。
    /// </summary>
    internal static class TestProtocolProjectionExtensions
    {
        /// <summary>映射并提交 generated world snapshot。</summary>
        internal static ClientProjectionApplyResult ApplyWorldSnapshot(
            this PersonalWorldService service,
            WorldSnapshot snapshot)
        {
            try
            {
                return service.ApplyWorldSnapshot(
                    ClientWorldProjectionMapper.FromWorldSnapshot(snapshot));
            }
            catch (ClientWorldProjectionException)
            {
                return ClientProjectionApplyResult.Rejected;
            }
        }

        /// <summary>映射并提交 generated assignment hint。</summary>
        internal static ClientProjectionApplyResult ApplyAssignmentHint(
            this PersonalWorldService service,
            WorldAssignmentChangedPush push)
        {
            try
            {
                var assignment = ClientWorldProjectionMapper.FromAssignmentHint(
                    push,
                    out var personalWorldID);
                return service.ApplyAssignmentHint(personalWorldID, assignment);
            }
            catch (ClientWorldProjectionException)
            {
                return ClientProjectionApplyResult.Rejected;
            }
        }

        /// <summary>映射并提交 generated VisitSession snapshot。</summary>
        internal static ClientProjectionApplyResult ApplySnapshot(
            this VisitSessionService service,
            VisitSessionSnapshot snapshot,
            ClientVisitRole role)
        {
            try
            {
                return service.ApplySnapshot(
                    ClientWorldProjectionMapper.FromVisitSnapshot(snapshot, role),
                    role);
            }
            catch (ClientWorldProjectionException)
            {
                return ClientProjectionApplyResult.Rejected;
            }
        }

        /// <summary>映射并提交 generated invite PUSH。</summary>
        internal static ClientProjectionApplyResult ApplyInvite(
            this VisitSessionService service,
            VisitInvitePush push)
        {
            try
            {
                return service.ApplyInviteProjection(
                    ClientWorldProjectionMapper.FromInvitePush(push),
                    outgoing: false);
            }
            catch (ClientWorldProjectionException)
            {
                return ClientProjectionApplyResult.Rejected;
            }
        }

        /// <summary>映射并提交 generated Owner availability PUSH。</summary>
        internal static ClientProjectionApplyResult ApplyOwnerAvailability(
            this VisitSessionService service,
            VisitOwnerAvailabilityPush push)
        {
            try
            {
                return service.ApplyControlHint(
                    ClientWorldProjectionMapper.FromOwnerAvailability(push));
            }
            catch (ClientWorldProjectionException)
            {
                return ClientProjectionApplyResult.Rejected;
            }
        }

        /// <summary>映射并提交 generated close notice PUSH。</summary>
        internal static ClientProjectionApplyResult ApplyClosedNotice(
            this VisitSessionService service,
            VisitClosedNoticePush push)
        {
            try
            {
                return service.ApplyControlHint(
                    ClientWorldProjectionMapper.FromClosedNotice(push));
            }
            catch (ClientWorldProjectionException)
            {
                return ClientProjectionApplyResult.Rejected;
            }
        }

        /// <summary>映射并提交 generated safe-return directive。</summary>
        internal static bool ApplySafeReturn(
            this VisitSessionService service,
            SafeReturnDirective directive)
        {
            try
            {
                return service.ApplySafeReturn(
                    ClientWorldProjectionMapper.FromSafeReturn(directive));
            }
            catch (ClientWorldProjectionException)
            {
                return false;
            }
        }
    }
}
