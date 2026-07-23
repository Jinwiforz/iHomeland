using System;
using System.Security.Cryptography;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Contracts;
using IHomeland.Client.Application.Gameplay;
using IHomeland.Client.Application.Ports;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Foundation.Time;

namespace IHomeland.Client.Application.World
{
    /// <summary>供无最终状态的 world flows 提交候选结果的窄 owner 边界。</summary>
    internal interface IWorldAdmissionFlowCommitPort
    {
        /// <summary>组合 caller 与 AppLifetime cancellation。</summary>
        CancellationTokenSource LinkFlow(CancellationToken caller);

        /// <summary>判断 intent 仍 current。</summary>
        bool IsFlowCurrent(WorldTargetIntentLease intent);

        /// <summary>接受成功授权操作导致的同 lineage Session generation 前进。</summary>
        bool IsFlowCurrentAfterAuthorizedOperation(
            WorldTargetIntentLease intent,
            bool operationSucceeded);

        /// <summary>提交 own-world flow 失败。</summary>
        void FinishOwnFlowFailure(
            WorldTargetIntentLease intent,
            ClientWorldFlowFailure failure);

        /// <summary>提交尚未离开 own target 的 join 失败。</summary>
        void FinishJoinFlowFailure(
            WorldTargetIntentLease intent,
            ClientWorldFlowFailure failure);

        /// <summary>提交已离开旧 target 后的安全返回。</summary>
        void BeginReturningFlow(
            WorldTargetIntentLease intent,
            ClientWorldFlowFailure failure);

        /// <summary>提交 return flow 失败。</summary>
        void FinishReturnFlowFailure(
            WorldTargetIntentLease intent,
            ClientWorldFlowFailure failure);

        /// <summary>提交稳定 OwnWorld/Visiting target。</summary>
        bool FinishFlowSuccess(
            WorldTargetIntentLease intent,
            ClientWorldFlowState state,
            string visitSessionID);

        /// <summary>关闭已建立但尚未提交的 gameplay generation。</summary>
        Task CloseFailedFlowConnectionAsync();

        /// <summary>提交 recovery flow 的非成功终态。</summary>
        void FinishRecoveryFlowFailure(
            WorldTargetIntentLease intent,
            ClientWorldFlowState state,
            ClientWorldFlowFailure failure);

        /// <summary>校验恢复后的 projection 仍对应冻结 target。</summary>
        bool ValidateRecoveredProjection(ClientRecoveryTargetDescriptor descriptor);

        /// <summary>归一化 recovery admission 失败并处理权威 target 失效。</summary>
        Task<ClientConnectionRecoveryResultKind> HandleRecoveryAdmissionFailureAsync(
            WorldTargetIntentLease intent,
            ClientGatewayResult<ClientWorldAdmissionLease> result,
            CancellationToken cancellationToken);

        /// <summary>归一化 RECONNECT 首帧失败并处理权威 target 失效。</summary>
        Task<ClientConnectionRecoveryResultKind> HandleReconnectFailureAsync(
            WorldTargetIntentLease intent,
            ClientGameplayResult<ClientVisitSessionProjection> result,
            CancellationToken cancellationToken);

        /// <summary>让已失效 Visitor target 在同一恢复预算内安全返回 OwnWorld。</summary>
        Task<ClientConnectionRecoveryResultKind> ReturnOwnAfterRecoveryRejectionAsync(
            WorldTargetIntentLease intent,
            CancellationToken cancellationToken);
    }

    /// <summary>编排 bootstrap、own admission、connect 与完整 snapshot。</summary>
    internal sealed class EnterOwnWorldFlow
    {
        /// <summary>唯一 Session gateway facade。</summary>
        private readonly SessionCoordinator _session;

        /// <summary>唯一 Gameplay typed port。</summary>
        private readonly IClientGameplayChannelPort _gameplay;

        /// <summary>唯一 PersonalWorld projection owner。</summary>
        private readonly PersonalWorldService _world;

        /// <summary>唯一 VisitSession projection owner。</summary>
        private readonly VisitSessionService _visit;

        /// <summary>统一失败映射。</summary>
        private readonly WorldAdmissionFailureMapper _failures;

        /// <summary>创建 own-world use case。</summary>
        internal EnterOwnWorldFlow(
            SessionCoordinator session,
            IClientGameplayChannelPort gameplay,
            PersonalWorldService world,
            VisitSessionService visit,
            WorldAdmissionFailureMapper failures)
        {
            _session = session ?? throw new ArgumentNullException(nameof(session));
            _gameplay = gameplay ?? throw new ArgumentNullException(nameof(gameplay));
            _world = world ?? throw new ArgumentNullException(nameof(world));
            _visit = visit ?? throw new ArgumentNullException(nameof(visit));
            _failures = failures ?? throw new ArgumentNullException(nameof(failures));
        }

        /// <summary>执行一笔 own-world intent，不保存最终 target。</summary>
        internal async Task<bool> ExecuteAsync(
            IWorldAdmissionFlowCommitPort owner,
            WorldTargetIntentLease intent,
            CancellationToken cancellationToken,
            ClientRecoveryTargetDescriptor recoveryTarget = null)
        {
            using (var linked = owner.LinkFlow(cancellationToken))
            {
                var bootstrap = await _session.GetWorldBootstrapAsync(linked.Token);
                if (!owner.IsFlowCurrentAfterAuthorizedOperation(
                        intent,
                        bootstrap.IsSuccess) ||
                    !bootstrap.IsSuccess ||
                    !IsApplySuccess(_world.ApplyBootstrap(bootstrap.Value)))
                {
                    owner.FinishOwnFlowFailure(
                        intent,
                        _failures.MapGateway(bootstrap));
                    return false;
                }

                var admission = await _session.IssueWorldAdmissionAsync(
                    ClientWorldAdmissionTarget.OwnWorld(),
                    WorldFlowKeys.New(),
                    linked.Token);
                if (!owner.IsFlowCurrentAfterAuthorizedOperation(
                        intent,
                        admission.IsSuccess) ||
                    !admission.IsSuccess ||
                    admission.Value.Role != ClientWorldRole.Owner ||
                    admission.Value.Purpose != ClientWorldAdmissionPurpose.OwnWorld)
                {
                    owner.FinishOwnFlowFailure(
                        intent,
                        _failures.MapGateway(admission));
                    return false;
                }

                if (_gameplay.Snapshot.Connected)
                {
                    try
                    {
                        await _gameplay.CloseAsync(linked.Token);
                    }
                    catch (OperationCanceledException)
                    {
                        owner.FinishOwnFlowFailure(
                            intent,
                            ClientWorldFlowFailure.CallerCancelled);
                        return false;
                    }
                }

                if (!owner.IsFlowCurrent(intent) ||
                    !await _gameplay.ConnectAsync(admission.Value, linked.Token))
                {
                    owner.FinishOwnFlowFailure(
                        intent,
                        ClientWorldFlowFailure.Transport);
                    return false;
                }

                _world.ClearCurrentTarget();
                _visit.SetTargetRole(ClientVisitRole.Owner, null);
                var world = await _gameplay.GetWorldSnapshotAsync(linked.Token);
                var worldApplied = world.IsSuccess &&
                                   world.Value != null &&
                                   IsApplySuccess(_world.ApplyWorldSnapshot(world.Value));
                if (!owner.IsFlowCurrent(intent) ||
                    !worldApplied ||
                    _world.Snapshot.CurrentWorld?.Assignment == null)
                {
                    await owner.CloseFailedFlowConnectionAsync();
                    owner.FinishOwnFlowFailure(
                        intent,
                        _failures.MapGameplay(world));
                    return false;
                }

                if (recoveryTarget != null &&
                    !string.IsNullOrEmpty(recoveryTarget.VisitSessionID))
                {
                    var visit = await _gameplay.GetVisitSnapshotAsync(
                        ClientVisitRole.Owner,
                        linked.Token);
                    if (visit?.ServerError != null &&
                        IsAuthoritativeRecoveryUnavailable(visit.ServerError.Code))
                    {
                        _visit.ClearTargetRole();
                        _world.ClearCurrentTarget();
                        await owner.CloseFailedFlowConnectionAsync();
                        return await ExecuteAsync(
                            owner,
                            intent,
                            linked.Token,
                            recoveryTarget: null);
                    }

                    var visitApplied = visit.IsSuccess &&
                                       visit.Value != null &&
                                       IsApplySuccess(
                                           _visit.ApplySnapshot(
                                               visit.Value,
                                               ClientVisitRole.Owner));
                    var currentVisit = _visit.Snapshot.Current;
                    var currentWorld = _world.Snapshot.CurrentWorld;
                    if (!owner.IsFlowCurrent(intent) ||
                        !visitApplied ||
                        currentVisit == null ||
                        !string.Equals(
                            currentVisit.VisitSessionID,
                            recoveryTarget.VisitSessionID,
                            StringComparison.Ordinal) ||
                        currentVisit.Revision < recoveryTarget.VisitRevision ||
                        currentWorld?.Assignment == null ||
                        !currentVisit.Assignment.HasSameIdentity(currentWorld.Assignment))
                    {
                        await owner.CloseFailedFlowConnectionAsync();
                        _visit.ClearTargetRole();
                        _world.ClearCurrentTarget();
                        owner.FinishOwnFlowFailure(
                            intent,
                            visit.IsSuccess
                                ? ClientWorldFlowFailure.Protocol
                                : _failures.MapGameplay(visit));
                        return false;
                    }
                }

                var committed = owner.FinishFlowSuccess(
                    intent,
                    ClientWorldFlowState.OwnWorld,
                    null);
                if (!committed)
                {
                    await owner.CloseFailedFlowConnectionAsync();
                }

                return committed;
            }
        }

        /// <summary>判断 projection gate 是否接受 applied/duplicate。</summary>
        private static bool IsApplySuccess(ClientProjectionApplyResult result)
        {
            return result == ClientProjectionApplyResult.Applied ||
                   result == ClientProjectionApplyResult.Duplicate;
        }

        /// <summary>识别权威证明旧 target 已不可恢复的错误。</summary>
        private static bool IsAuthoritativeRecoveryUnavailable(long code)
        {
            return code == 2002 || code == 2100 || code == 2104 ||
                   code == 2107 || code == 2108 || code == 2109;
        }
    }

    /// <summary>编排 accept reservation、JOIN 与 Visit/World snapshot 提交候选。</summary>
    internal sealed class EnterVisitWorldFlow
    {
        /// <summary>Session gateway facade。</summary>
        private readonly SessionCoordinator _session;

        /// <summary>Gameplay typed port。</summary>
        private readonly IClientGameplayChannelPort _gameplay;

        /// <summary>PersonalWorld projection owner。</summary>
        private readonly PersonalWorldService _world;

        /// <summary>VisitSession projection/inbox owner。</summary>
        private readonly VisitSessionService _visit;

        /// <summary>统一失败映射。</summary>
        private readonly WorldAdmissionFailureMapper _failures;

        /// <summary>创建 visit-world use case。</summary>
        internal EnterVisitWorldFlow(
            SessionCoordinator session,
            IClientGameplayChannelPort gameplay,
            PersonalWorldService world,
            VisitSessionService visit,
            WorldAdmissionFailureMapper failures)
        {
            _session = session ?? throw new ArgumentNullException(nameof(session));
            _gameplay = gameplay ?? throw new ArgumentNullException(nameof(gameplay));
            _world = world ?? throw new ArgumentNullException(nameof(world));
            _visit = visit ?? throw new ArgumentNullException(nameof(visit));
            _failures = failures ?? throw new ArgumentNullException(nameof(failures));
        }

        /// <summary>执行一笔 pending invite join intent。</summary>
        internal async Task<bool> ExecuteAsync(
            IWorldAdmissionFlowCommitPort owner,
            WorldTargetIntentLease intent,
            ClientVisitInviteProjection invite,
            CancellationToken cancellationToken)
        {
            using (var linked = owner.LinkFlow(cancellationToken))
            {
                var accept = await _session.AcceptVisitInviteAsync(
                    new ClientVisitInviteAcceptRequest(
                        invite.VisitSessionID,
                        invite.InviteID,
                        checked((long)invite.CreatedRevision)),
                    WorldFlowKeys.New(),
                    linked.Token);
                if (!owner.IsFlowCurrentAfterAuthorizedOperation(intent, accept.IsSuccess))
                {
                    return false;
                }

                if (!accept.IsSuccess)
                {
                    if (IsAuthoritativeInviteUnavailable(accept))
                    {
                        _visit.RetireRejectedInvite(
                            invite.VisitSessionID,
                            invite.InviteID);
                        owner.FinishJoinFlowFailure(
                            intent,
                            ClientWorldFlowFailure.InviteUnavailable);
                    }
                    else
                    {
                        owner.FinishJoinFlowFailure(
                            intent,
                            _failures.MapAccept(accept));
                    }

                    return false;
                }

                var reservation = accept.Value;
                if (!string.Equals(
                        reservation.VisitSessionID,
                        invite.VisitSessionID,
                        StringComparison.Ordinal) ||
                    reservation.Revision <= checked((long)invite.CreatedRevision))
                {
                    owner.FinishJoinFlowFailure(
                        intent,
                        ClientWorldFlowFailure.Protocol);
                    return false;
                }

                _visit.RetireAcceptedInvite(invite.VisitSessionID, invite.InviteID);
                var admission = await _session.IssueWorldAdmissionAsync(
                    ClientWorldAdmissionTarget.VisitWorld(
                        reservation.VisitSessionID),
                    WorldFlowKeys.New(),
                    linked.Token);
                if (!owner.IsFlowCurrentAfterAuthorizedOperation(
                        intent,
                        admission.IsSuccess) ||
                    !admission.IsSuccess ||
                    admission.Value.Role != ClientWorldRole.Visitor ||
                    admission.Value.Purpose != ClientWorldAdmissionPurpose.Join)
                {
                    owner.FinishJoinFlowFailure(
                        intent,
                        _failures.MapGateway(admission));
                    return false;
                }

                try
                {
                    await _gameplay.CloseAsync(linked.Token);
                }
                catch (OperationCanceledException)
                {
                    owner.FinishJoinFlowFailure(
                        intent,
                        ClientWorldFlowFailure.CallerCancelled);
                    return false;
                }

                _world.ClearCurrentTarget();
                if (!owner.IsFlowCurrent(intent) ||
                    !await _gameplay.ConnectAsync(admission.Value, linked.Token))
                {
                    owner.BeginReturningFlow(
                        intent,
                        ClientWorldFlowFailure.Transport);
                    return false;
                }

                _visit.SetTargetRole(
                    ClientVisitRole.Visitor,
                    reservation.VisitSessionID);
                var join = await _gameplay.JoinVisitAsync(
                    admission.Value.VisitRevision,
                    linked.Token);
                if (!owner.IsFlowCurrent(intent) ||
                    !join.IsSuccess ||
                    join.Value == null)
                {
                    owner.BeginReturningFlow(
                        intent,
                        _failures.MapGameplay(join));
                    return false;
                }

                if (!IsApplySuccess(
                        _visit.ApplySnapshot(
                            join.Value,
                            ClientVisitRole.Visitor)))
                {
                    owner.BeginReturningFlow(
                        intent,
                        ClientWorldFlowFailure.Protocol);
                    return false;
                }

                var world = await _gameplay.GetWorldSnapshotAsync(linked.Token);
                var worldApplied = world.IsSuccess &&
                                   world.Value != null &&
                                   IsApplySuccess(_world.ApplyWorldSnapshot(world.Value));
                var currentWorld = _world.Snapshot.CurrentWorld;
                var currentVisit = _visit.Snapshot.Current;
                if (!owner.IsFlowCurrent(intent) ||
                    !worldApplied ||
                    currentWorld?.Assignment == null ||
                    currentVisit == null ||
                    !currentWorld.Assignment.HasSameIdentity(currentVisit.Assignment))
                {
                    owner.BeginReturningFlow(
                        intent,
                        _failures.MapGameplay(world));
                    return false;
                }

                return owner.FinishFlowSuccess(
                    intent,
                    ClientWorldFlowState.Visiting,
                    reservation.VisitSessionID);
            }
        }

        /// <summary>识别确定性 invite 退役错误。</summary>
        private static bool IsAuthoritativeInviteUnavailable(
            ClientGatewayResult<ClientVisitReservation> result)
        {
            var code = result?.ServerError?.Code;
            return code == 101 || code == 200 || code == 2100 ||
                   code == 2101 || code == 2102 || code == 2104 ||
                   code == 2105;
        }

        /// <summary>判断 projection gate 是否接受 applied/duplicate。</summary>
        private static bool IsApplySuccess(ClientProjectionApplyResult result)
        {
            return result == ClientProjectionApplyResult.Applied ||
                   result == ClientProjectionApplyResult.Duplicate;
        }
    }

    /// <summary>编排 Visitor leave、transport close 与 OwnWorld 重新解析。</summary>
    internal sealed class ReturnToOwnWorldFlow
    {
        /// <summary>Gameplay typed port。</summary>
        private readonly IClientGameplayChannelPort _gameplay;

        /// <summary>PersonalWorld projection owner。</summary>
        private readonly PersonalWorldService _world;

        /// <summary>VisitSession projection owner。</summary>
        private readonly VisitSessionService _visit;

        /// <summary>OwnWorld flow。</summary>
        private readonly EnterOwnWorldFlow _enterOwnWorld;

        /// <summary>统一失败映射。</summary>
        private readonly WorldAdmissionFailureMapper _failures;

        /// <summary>创建 return use case。</summary>
        internal ReturnToOwnWorldFlow(
            IClientGameplayChannelPort gameplay,
            PersonalWorldService world,
            VisitSessionService visit,
            EnterOwnWorldFlow enterOwnWorld,
            WorldAdmissionFailureMapper failures)
        {
            _gameplay = gameplay ?? throw new ArgumentNullException(nameof(gameplay));
            _world = world ?? throw new ArgumentNullException(nameof(world));
            _visit = visit ?? throw new ArgumentNullException(nameof(visit));
            _enterOwnWorld = enterOwnWorld ??
                throw new ArgumentNullException(nameof(enterOwnWorld));
            _failures = failures ?? throw new ArgumentNullException(nameof(failures));
        }

        /// <summary>执行主动 leave 后的安全返回。</summary>
        internal async Task<bool> ExecuteAsync(
            IWorldAdmissionFlowCommitPort owner,
            WorldTargetIntentLease intent,
            CancellationToken cancellationToken)
        {
            using (var linked = owner.LinkFlow(cancellationToken))
            {
                var leave = await _visit.LeaveAsync(linked.Token);
                if (!owner.IsFlowCurrent(intent) || !leave.IsSuccess)
                {
                    owner.FinishReturnFlowFailure(
                        intent,
                        _failures.MapGameplay(leave));
                    return false;
                }

                try
                {
                    await _gameplay.CloseAsync(linked.Token);
                }
                catch (OperationCanceledException)
                {
                    owner.FinishReturnFlowFailure(
                        intent,
                        ClientWorldFlowFailure.CallerCancelled);
                    return false;
                }

                _visit.ClearTargetRole();
                _world.ClearCurrentTarget();
                return await _enterOwnWorld.ExecuteAsync(
                    owner,
                    intent,
                    linked.Token);
            }
        }
    }

    /// <summary>生成不含输入值的 CSPRNG idempotency key。</summary>
    internal sealed class ReconnectWorldTargetFlow
    {
        /// <summary>统一 Unix 毫秒时钟。</summary>
        private readonly IClientClock _clock;

        /// <summary>唯一 Session gateway facade。</summary>
        private readonly SessionCoordinator _session;

        /// <summary>唯一 Gameplay typed port。</summary>
        private readonly IClientGameplayChannelPort _gameplay;

        /// <summary>PersonalWorld projection owner。</summary>
        private readonly PersonalWorldService _world;

        /// <summary>VisitSession projection owner。</summary>
        private readonly VisitSessionService _visit;

        /// <summary>统一 recovery 失败分类。</summary>
        private readonly WorldAdmissionFailureMapper _failures;

        /// <summary>创建 Visitor target 重连 use case。</summary>
        internal ReconnectWorldTargetFlow(
            IClientClock clock,
            SessionCoordinator session,
            IClientGameplayChannelPort gameplay,
            PersonalWorldService world,
            VisitSessionService visit,
            WorldAdmissionFailureMapper failures)
        {
            _clock = clock ?? throw new ArgumentNullException(nameof(clock));
            _session = session ?? throw new ArgumentNullException(nameof(session));
            _gameplay = gameplay ?? throw new ArgumentNullException(nameof(gameplay));
            _world = world ?? throw new ArgumentNullException(nameof(world));
            _visit = visit ?? throw new ArgumentNullException(nameof(visit));
            _failures = failures ?? throw new ArgumentNullException(nameof(failures));
        }

        /// <summary>恢复冻结的 Visitor target，最终事实只通过 commit port 提交。</summary>
        internal async Task<ClientConnectionRecoveryResultKind> ExecuteAsync(
            IWorldAdmissionFlowCommitPort owner,
            WorldTargetIntentLease intent,
            ClientRecoveryTargetDescriptor descriptor,
            CancellationToken cancellationToken)
        {
            if (owner == null)
            {
                throw new ArgumentNullException(nameof(owner));
            }

            if (descriptor == null)
            {
                throw new ArgumentNullException(nameof(descriptor));
            }

            using (var linked = owner.LinkFlow(cancellationToken))
            {
                if (_clock.UtcNowMilliseconds >= descriptor.ReconnectExpiresAtMilliseconds)
                {
                    return await owner.ReturnOwnAfterRecoveryRejectionAsync(
                        intent,
                        linked.Token);
                }

                var admission = await _session.IssueWorldAdmissionAsync(
                    ClientWorldAdmissionTarget.VisitWorld(descriptor.VisitSessionID),
                    WorldFlowKeys.New(),
                    linked.Token);
                if (!owner.IsFlowCurrentAfterAuthorizedOperation(intent, admission.IsSuccess) ||
                    !admission.IsSuccess ||
                    admission.Value.Role != ClientWorldRole.Visitor ||
                    admission.Value.Purpose != ClientWorldAdmissionPurpose.Reconnect)
                {
                    return await owner.HandleRecoveryAdmissionFailureAsync(
                        intent,
                        admission,
                        linked.Token);
                }

                if (!await _gameplay.ConnectAsync(admission.Value, linked.Token))
                {
                    owner.FinishRecoveryFlowFailure(
                        intent,
                        ClientWorldFlowState.ConnectionLost,
                        ClientWorldFlowFailure.Transport);
                    return ClientConnectionRecoveryResultKind.Transport;
                }

                _visit.SetTargetRole(ClientVisitRole.Visitor, descriptor.VisitSessionID);
                var reconnect = await _gameplay.ReconnectVisitAsync(
                    admission.Value.VisitRevision,
                    linked.Token);
                if (!owner.IsFlowCurrent(intent) || !reconnect.IsSuccess ||
                    reconnect.Value == null ||
                    !IsApplySuccess(_visit.ApplySnapshot(
                        reconnect.Value,
                        ClientVisitRole.Visitor)))
                {
                    return await owner.HandleReconnectFailureAsync(
                        intent,
                        reconnect,
                        linked.Token);
                }

                var world = await _gameplay.GetWorldSnapshotAsync(linked.Token);
                if (!owner.IsFlowCurrent(intent) || !world.IsSuccess || world.Value == null ||
                    !IsApplySuccess(_world.ApplyWorldSnapshot(world.Value)))
                {
                    await owner.CloseFailedFlowConnectionAsync();
                    owner.FinishRecoveryFlowFailure(
                        intent,
                        ClientWorldFlowState.ConnectionLost,
                        _failures.MapGameplay(world));
                    return _failures.MapRecoveryGameplay(world);
                }

                if (!owner.ValidateRecoveredProjection(descriptor))
                {
                    await owner.CloseFailedFlowConnectionAsync();
                    owner.FinishRecoveryFlowFailure(
                        intent,
                        ClientWorldFlowState.ConnectionLost,
                        ClientWorldFlowFailure.Protocol);
                    return ClientConnectionRecoveryResultKind.Protocol;
                }

                return owner.FinishFlowSuccess(
                    intent,
                    ClientWorldFlowState.Visiting,
                    descriptor.VisitSessionID)
                    ? ClientConnectionRecoveryResultKind.Succeeded
                    : ClientConnectionRecoveryResultKind.Protocol;
            }
        }

        /// <summary>把 projection reducer 的结果收敛为可提交成功。</summary>
        private static bool IsApplySuccess(ClientProjectionApplyResult result)
        {
            return result == ClientProjectionApplyResult.Applied ||
                   result == ClientProjectionApplyResult.Duplicate;
        }
    }

    /// <summary>生成不含输入值的 CSPRNG idempotency key。</summary>
    internal static class WorldFlowKeys
    {
        /// <summary>创建 128-bit 小写十六进制 key。</summary>
        internal static string New()
        {
            var bytes = new byte[16];
            using (var random = RandomNumberGenerator.Create())
            {
                random.GetBytes(bytes);
            }

            return BitConverter.ToString(bytes)
                .Replace("-", string.Empty)
                .ToLowerInvariant();
        }
    }
}
