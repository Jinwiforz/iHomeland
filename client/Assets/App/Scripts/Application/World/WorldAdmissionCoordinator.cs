using System;
using System.Security.Cryptography;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Gameplay;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Core.Lifetime;
using IHomeland.Client.Infrastructure.Http;
using IHomeland.Client.Infrastructure.Tcp;
using IHomeland.Protocol.Visit.V1;
using IHomeland.Protocol.World.V1;

namespace IHomeland.Client.Application.World
{
    /// <summary>
    /// 线性化 own-world、join visit、visiting 与 safe-return 的唯一 App Scope flow owner。
    /// </summary>
    internal sealed class WorldAdmissionCoordinator :
        IAppLifetimeParticipant,
        IClientConnectionRecoveryOperations
    {
        /// <summary>限制失败 target 已线性化关闭后的 I/O owner 观察窗口。</summary>
        private static readonly TimeSpan FailedConnectionCleanupTimeout = TimeSpan.FromSeconds(5);

        /// <summary>保护状态、单一 intent 与 generation。</summary>
        private readonly object _sync = new object();

        /// <summary>提供 current session generation 与 HTTP capability。</summary>
        private readonly SessionCoordinator _sessionCoordinator;

        /// <summary>提供Visitor reconnect绝对deadline的唯一可测试时间来源。</summary>
        private readonly IClientClock _clock;

        /// <summary>拥有 TLS/TCP connection、admission credential 与首帧约束。</summary>
        private readonly ClientGameplayChannel _gameplayChannel;

        /// <summary>拥有 primary/current PersonalWorld projection。</summary>
        private readonly PersonalWorldService _personalWorldService;

        /// <summary>拥有 VisitSession projection、commands 与 safe-return callback。</summary>
        private readonly VisitSessionService _visitSessionService;

        /// <summary>停止时撤销当前 flow 的根信号。</summary>
        private CancellationTokenSource _lifetimeCancellation;

        /// <summary>保证同一时刻最多一个 target transition。</summary>
        private bool _intentActive;

        /// <summary>记录是否已登记 Service subscriber。</summary>
        private bool _subscribed;

        /// <summary>保存不含 endpoint、credential 或 payload 的 flow 快照。</summary>
        private ClientWorldFlowSnapshot _snapshot = new ClientWorldFlowSnapshot(
            ClientWorldFlowState.Inactive,
            0,
            ClientWorldFlowFailure.None,
            null,
            null);

        /// <summary>最近稳定OwnWorld或Visiting target的无credential恢复descriptor。</summary>
        private ClientRecoveryTargetDescriptor _recoveryTarget;

        /// <summary>创建不自动联网的 world target coordinator。</summary>
        /// <param name="sessionCoordinator">唯一 Session owner。</param>
        /// <param name="clock">统一Unix毫秒时钟。</param>
        /// <param name="gameplayChannel">唯一 gameplay owner。</param>
        /// <param name="personalWorldService">PersonalWorld projection owner。</param>
        /// <param name="visitSessionService">VisitSession projection owner。</param>
        internal WorldAdmissionCoordinator(
            SessionCoordinator sessionCoordinator,
            IClientClock clock,
            ClientGameplayChannel gameplayChannel,
            PersonalWorldService personalWorldService,
            VisitSessionService visitSessionService)
        {
            _sessionCoordinator = sessionCoordinator ?? throw new ArgumentNullException(nameof(sessionCoordinator));
            _clock = clock ?? throw new ArgumentNullException(nameof(clock));
            _gameplayChannel = gameplayChannel ?? throw new ArgumentNullException(nameof(gameplayChannel));
            _personalWorldService = personalWorldService ?? throw new ArgumentNullException(nameof(personalWorldService));
            _visitSessionService = visitSessionService ?? throw new ArgumentNullException(nameof(visitSessionService));
        }

        /// <summary>在低敏 flow snapshot 已提交后通知消费者。</summary>
        internal event Action<ClientWorldFlowSnapshot> Changed;

        /// <summary>获取 current flow 状态。</summary>
        internal ClientWorldFlowSnapshot Snapshot
        {
            get
            {
                lock (_sync)
                {
                    return _snapshot;
                }
            }
        }

        /// <summary>只创建本地取消所有权并登记 subscriber，不产生网络副作用。</summary>
        /// <param name="cancellationToken">AppLifetime 初始化信号。</param>
        /// <returns>本地初始化完成的任务。</returns>
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            lock (_sync)
            {
                if (_lifetimeCancellation != null || _snapshot.State == ClientWorldFlowState.Stopped)
                {
                    throw new InvalidOperationException("WorldAdmissionCoordinator 不能重复初始化或停止后重启。");
                }

                _lifetimeCancellation = new CancellationTokenSource();
                _personalWorldService.ProjectionConflict += OnProjectionConflict;
                _personalWorldService.Changed += OnPersonalWorldChanged;
                _visitSessionService.ProjectionConflict += OnProjectionConflict;
                _visitSessionService.Changed += OnVisitSessionChanged;
                _visitSessionService.SafeReturnReceived += OnSafeReturn;
                _gameplayChannel.UnexpectedDisconnect += OnGameplayUnexpectedDisconnect;
                _subscribed = true;
            }

            return Task.CompletedTask;
        }

        /// <summary>显式解析并进入 current actor 自己的 PersonalWorld。</summary>
        /// <param name="cancellationToken">取消 caller 等待。</param>
        /// <returns>完整 target 已提交为 OwnWorld 时返回 true。</returns>
        internal async Task<bool> EnterOwnWorldAsync(CancellationToken cancellationToken)
        {
            var source = Snapshot.State;
            if (source != ClientWorldFlowState.Inactive &&
                source != ClientWorldFlowState.ConnectionLost)
            {
                return false;
            }

            if (!TryBegin(
                    source,
                    ClientWorldFlowState.ResolvingOwnWorld,
                    null,
                    null,
                    out var intent))
            {
                return false;
            }

            return await ResolveOwnWorldAsync(intent, cancellationToken);
        }

        /// <summary>接受指定 inbox invite 并加入其 VisitSession。</summary>
        /// <param name="visitSessionID">Inbox 中的 VisitSessionID。</param>
        /// <param name="inviteID">Inbox 中的 InviteID。</param>
        /// <param name="cancellationToken">取消 caller 等待；不会更换 key 自动重发。</param>
        /// <returns>完整 target 已提交为 Visiting 时返回 true。</returns>
        internal async Task<bool> JoinVisitAsync(
            string visitSessionID,
            string inviteID,
            CancellationToken cancellationToken)
        {
            var invite = FindInvite(visitSessionID, inviteID);
            if (invite == null || invite.State != ClientVisitInviteState.Pending)
            {
                return RejectUnavailableInvite();
            }

            if (!TryBegin(
                    ClientWorldFlowState.OwnWorld,
                    ClientWorldFlowState.JoiningVisit,
                    visitSessionID,
                    null,
                    out var intent))
            {
                return false;
            }

            var acceptKey = NewIdempotencyKey();
            var admissionKey = NewIdempotencyKey();
            using (var linked = Link(cancellationToken))
            {
                var accept = await _sessionCoordinator.AcceptVisitInviteAsync(
                    new ClientVisitInviteAcceptRequest(
                        invite.VisitSessionID,
                        invite.InviteID,
                        checked((long)invite.CreatedRevision)),
                    acceptKey,
                    linked.Token);
                if (!IsCurrentAfterAuthorizedOperation(intent, accept.IsSuccess))
                {
                    return false;
                }

                if (!accept.IsSuccess)
                {
                    if (IsAuthoritativeInviteUnavailable(accept))
                    {
                        _visitSessionService.RetireRejectedInvite(
                            invite.VisitSessionID,
                            invite.InviteID);
                        FinishJoinFailure(intent, ClientWorldFlowFailure.InviteUnavailable);
                    }
                    else
                    {
                        FinishJoinFailure(intent, MapAcceptFailure(accept));
                    }

                    return false;
                }

                var reservation = accept.Value;
                if (!string.Equals(reservation.VisitSessionID, invite.VisitSessionID, StringComparison.Ordinal) ||
                    reservation.Revision <= checked((long)invite.CreatedRevision))
                {
                    FinishJoinFailure(intent, ClientWorldFlowFailure.Protocol);
                    return false;
                }

                // 成功 accept 已权威证明 pending invite 被消费；后续 admission/JOIN 即使失败也不能
                // 让旧 identity 重新出现在可接受 inbox。Commit-unknown 路径不会到达这里。
                _visitSessionService.RetireAcceptedInvite(invite.VisitSessionID, invite.InviteID);

                var admission = await _sessionCoordinator.IssueWorldAdmissionAsync(
                    ClientWorldAdmissionTarget.VisitWorld(reservation.VisitSessionID),
                    admissionKey,
                    linked.Token);
                if (!IsCurrentAfterAuthorizedOperation(intent, admission.IsSuccess) ||
                    !admission.IsSuccess ||
                    admission.Value.Role != ClientWorldRole.Visitor ||
                    admission.Value.Purpose != ClientWorldAdmissionPurpose.Join)
                {
                    FinishJoinFailure(intent, MapHttpFailure(admission));
                    return false;
                }

                try
                {
                    await _gameplayChannel.CloseAsync(linked.Token);
                }
                catch (OperationCanceledException)
                {
                    FinishJoinFailure(intent, ClientWorldFlowFailure.CallerCancelled);
                    return false;
                }
                _personalWorldService.ClearCurrentTarget();
                if (!IsCurrent(intent) ||
                    !await _gameplayChannel.ConnectAsync(admission.Value, linked.Token))
                {
                    BeginReturningAfterJoinFailure(intent, ClientWorldFlowFailure.Transport);
                    return false;
                }

                _visitSessionService.SetTargetRole(ClientVisitRole.Visitor, reservation.VisitSessionID);
                var join = await _gameplayChannel.JoinPendingVisitAsync(
                    admission.Value.VisitRevision,
                    linked.Token);
                if (!IsCurrent(intent) || !join.IsSuccess || join.Value.Result?.Snapshot == null)
                {
                    BeginReturningAfterJoinFailure(intent, MapGameplayFailure(join));
                    return false;
                }

                var visitApply = _visitSessionService.ApplySnapshot(
                    join.Value.Result.Snapshot,
                    ClientVisitRole.Visitor);
                if (!IsApplySuccess(visitApply))
                {
                    BeginReturningAfterJoinFailure(intent, ClientWorldFlowFailure.Protocol);
                    return false;
                }

                var world = await _gameplayChannel.SendAsync(
                    ClientGameplayCatalog.WorldSnapshot,
                    new WorldSnapshotRequest(),
                    linked.Token);
                var worldApplied = world.IsSuccess && world.Value.Snapshot != null &&
                                   IsApplySuccess(_personalWorldService.ApplyWorldSnapshot(world.Value.Snapshot));
                var currentWorld = _personalWorldService.Snapshot.CurrentWorld;
                var currentVisit = _visitSessionService.Snapshot.Current;
                if (!IsCurrent(intent) || !worldApplied ||
                    currentWorld?.Assignment == null || currentVisit == null ||
                    !currentWorld.Assignment.HasSameIdentity(currentVisit.Assignment))
                {
                    BeginReturningAfterJoinFailure(intent, MapGameplayFailure(world));
                    return false;
                }

                return FinishSuccess(
                    intent,
                    ClientWorldFlowState.Visiting,
                    reservation.VisitSessionID,
                    null);
            }
        }

        /// <summary>让 current Visitor 主动离开并进入 own-world 返回流程。</summary>
        /// <param name="cancellationToken">取消 caller 等待。</param>
        /// <returns>已重新进入 OwnWorld 时返回 true。</returns>
        internal async Task<bool> LeaveVisitAsync(CancellationToken cancellationToken)
        {
            if (!TryBegin(
                    ClientWorldFlowState.Visiting,
                    ClientWorldFlowState.ReturningOwnWorld,
                    Snapshot.VisitSessionID,
                    null,
                    out var intent))
            {
                return false;
            }

            using (var linked = Link(cancellationToken))
            {
                var leave = await _visitSessionService.LeaveAsync(linked.Token);
                if (!IsCurrent(intent) || !leave.IsSuccess)
                {
                    FinishReturnFailure(intent, MapGameplayFailure(leave));
                    return false;
                }

                try
                {
                    await _gameplayChannel.CloseAsync(linked.Token);
                }
                catch (OperationCanceledException)
                {
                    FinishReturnFailure(intent, ClientWorldFlowFailure.CallerCancelled);
                    return false;
                }
                _visitSessionService.ClearTargetRole();
                _personalWorldService.ClearCurrentTarget();
                return await ResolveOwnWorldAsync(intent, linked.Token);
            }
        }

        /// <summary>显式重试已关闭 Visitor target 后失败的 own-world 返回。</summary>
        /// <param name="cancellationToken">取消 caller 等待。</param>
        /// <returns>已重新进入 OwnWorld 时返回 true。</returns>
        internal async Task<bool> RetryReturnAsync(CancellationToken cancellationToken)
        {
            WorldFlowIntent intent;
            lock (_sync)
            {
                if (_snapshot.State != ClientWorldFlowState.ReturningOwnWorld || _intentActive ||
                    !TryCaptureSessionGeneration(out var sessionGeneration))
                {
                    return false;
                }

                _intentActive = true;
                var generation = _snapshot.TargetGeneration + 1;
                intent = new WorldFlowIntent(generation, sessionGeneration);
                SetSnapshotLocked(
                    ClientWorldFlowState.ReturningOwnWorld,
                    generation,
                    ClientWorldFlowFailure.None,
                    null,
                    _snapshot.SafeReturn);
            }

            Notify(Snapshot);
            return await ResolveOwnWorldAsync(intent, cancellationToken);
        }

        /// <inheritdoc />
        bool IClientConnectionRecoveryOperations.TryCaptureTarget(
            out ClientRecoveryTargetDescriptor descriptor)
        {
            RefreshRecoveryTargetIfStable();
            if (!_sessionCoordinator.TryGetCurrent(out var session))
            {
                descriptor = null;
                return false;
            }

            lock (_sync)
            {
                if (_recoveryTarget == null ||
                    !_recoveryTarget.TryRebindTo(session, out descriptor))
                {
                    descriptor = null;
                    return false;
                }

                _recoveryTarget = descriptor;
                return descriptor != null &&
                       (_snapshot.State == ClientWorldFlowState.OwnWorld ||
                        _snapshot.State == ClientWorldFlowState.Visiting ||
                        _snapshot.State == ClientWorldFlowState.ConnectionLost ||
                        _snapshot.State == ClientWorldFlowState.RecoveringTarget);
            }
        }

        /// <inheritdoc />
        void IClientConnectionRecoveryOperations.InvalidateControlOnlyState()
        {
            _personalWorldService.InvalidateControlProjection();
            _visitSessionService.InvalidateControlProjection();
        }

        /// <inheritdoc />
        async Task<ClientConnectionRecoveryResultKind>
            IClientConnectionRecoveryOperations.ReconcileControlAsync(
                ClientRecoveryTargetDescriptor descriptor,
                CancellationToken cancellationToken)
        {
            if (descriptor == null ||
                _gameplayChannel.Snapshot.State != ClientGameplayChannelState.Active)
            {
                return ClientConnectionRecoveryResultKind.Policy;
            }

            var world = await _gameplayChannel.SendAsync(
                ClientGameplayCatalog.WorldSnapshot,
                new WorldSnapshotRequest(),
                cancellationToken);
            if (!world.IsSuccess || world.Value.Snapshot == null ||
                !IsApplySuccess(_personalWorldService.ApplyWorldSnapshot(world.Value.Snapshot)))
            {
                return MapRecoveryGameplayFailure(world);
            }

            if (string.IsNullOrEmpty(descriptor.VisitSessionID))
            {
                return ValidateDescriptorAgainstCurrent(descriptor, requireNewTargetGeneration: false)
                    ? ClientConnectionRecoveryResultKind.Succeeded
                    : ClientConnectionRecoveryResultKind.Protocol;
            }

            var visit = await _gameplayChannel.SendAsync(
                ClientGameplayCatalog.VisitSnapshot,
                new VisitSnapshotRequest(),
                cancellationToken);
            var role = descriptor.Kind == ClientRecoveryTargetKind.Visiting
                ? ClientVisitRole.Visitor
                : ClientVisitRole.Owner;
            if (!visit.IsSuccess || visit.Value.Snapshot == null ||
                !IsApplySuccess(_visitSessionService.ApplySnapshot(visit.Value.Snapshot, role)))
            {
                return MapRecoveryGameplayFailure(visit);
            }

            return ValidateDescriptorAgainstCurrent(descriptor, requireNewTargetGeneration: false)
                ? ClientConnectionRecoveryResultKind.Succeeded
                : ClientConnectionRecoveryResultKind.Protocol;
        }

        /// <inheritdoc />
        async Task<ClientConnectionRecoveryResultKind>
            IClientConnectionRecoveryOperations.RecoverWorldAsync(
                ClientRecoveryTargetDescriptor descriptor,
                CancellationToken cancellationToken)
        {
            if (descriptor == null ||
                !_sessionCoordinator.TryGetCurrent(out var session) ||
                !descriptor.TryRebindTo(session, out var currentDescriptor) ||
                !TryBegin(
                    ClientWorldFlowState.ConnectionLost,
                    ClientWorldFlowState.RecoveringTarget,
                    currentDescriptor.VisitSessionID,
                    null,
                    out var intent) ||
                intent.SessionGeneration != currentDescriptor.SessionGeneration)
            {
                return ClientConnectionRecoveryResultKind.Policy;
            }

            return currentDescriptor.Kind == ClientRecoveryTargetKind.OwnWorld
                ? await RecoverOwnWorldAsync(intent, currentDescriptor, cancellationToken)
                : await RecoverVisitAsync(intent, currentDescriptor, cancellationToken);
        }

        /// <inheritdoc />
        bool IClientConnectionRecoveryOperations.TryValidateRecoveredTarget(
            ClientRecoveryTargetDescriptor descriptor,
            out long currentTargetGeneration)
        {
            bool descriptorIsCurrent;
            lock (_sync)
            {
                currentTargetGeneration = _snapshot.TargetGeneration;
                descriptorIsCurrent = ReferenceEquals(descriptor, _recoveryTarget);
            }

            return ValidateDescriptorAgainstCurrent(
                descriptor,
                requireNewTargetGeneration: !descriptorIsCurrent);
        }

        /// <summary>以现有own-world计划恢复断线目标并映射稳定结果。</summary>
        /// <param name="intent">已取得的恢复flow intent。</param>
        /// <param name="descriptor">断线前冻结的OwnWorld与可选Owner VisitSession事实。</param>
        /// <param name="cancellationToken">恢复owner总deadline与停止信号。</param>
        /// <returns>世界与可选Owner VisitSession均完成权威收敛后的稳定结果。</returns>
        private async Task<ClientConnectionRecoveryResultKind> RecoverOwnWorldAsync(
            WorldFlowIntent intent,
            ClientRecoveryTargetDescriptor descriptor,
            CancellationToken cancellationToken)
        {
            var recovered = await ResolveOwnWorldAsync(
                intent,
                cancellationToken,
                descriptor);
            if (!recovered)
            {
                return MapRecoveryFlowFailure(Snapshot.Failure);
            }

            if (!string.IsNullOrEmpty(descriptor.VisitSessionID))
            {
                RefreshRecoveryTargetIfStable();
                lock (_sync)
                {
                    if (_recoveryTarget != null &&
                        _recoveryTarget.Kind == ClientRecoveryTargetKind.OwnWorld &&
                        string.IsNullOrEmpty(_recoveryTarget.VisitSessionID))
                    {
                        // 旧Owner VisitSession被新server generation权威退役后，协调器必须
                        // 重新捕获已提交的OwnWorld descriptor，而不能校验旧冻结目标。
                        return ClientConnectionRecoveryResultKind.ReturningOwnWorld;
                    }
                }
            }

            return ClientConnectionRecoveryResultKind.Succeeded;
        }

        /// <summary>使用新admission与唯一typed RECONNECT首帧恢复Visitor membership。</summary>
        private async Task<ClientConnectionRecoveryResultKind> RecoverVisitAsync(
            WorldFlowIntent intent,
            ClientRecoveryTargetDescriptor descriptor,
            CancellationToken cancellationToken)
        {
            using (var linked = Link(cancellationToken))
            {
                if (_clock.UtcNowMilliseconds >= descriptor.ReconnectExpiresAtMilliseconds)
                {
                    return await ReturnOwnAfterRecoveryRejectionAsync(intent, linked.Token);
                }

                var admission = await _sessionCoordinator.IssueWorldAdmissionAsync(
                    ClientWorldAdmissionTarget.VisitWorld(descriptor.VisitSessionID),
                    NewIdempotencyKey(),
                    linked.Token);
                if (!IsCurrentAfterAuthorizedOperation(intent, admission.IsSuccess) ||
                    !admission.IsSuccess ||
                    admission.Value.Role != ClientWorldRole.Visitor ||
                    admission.Value.Purpose != ClientWorldAdmissionPurpose.Reconnect)
                {
                    return await FinishRecoveryAdmissionFailureAsync(
                        intent,
                        admission,
                        linked.Token);
                }

                if (!await _gameplayChannel.ConnectAsync(admission.Value, linked.Token))
                {
                    FinishFailure(
                        intent,
                        ClientWorldFlowState.ConnectionLost,
                        ClientWorldFlowFailure.Transport,
                        null);
                    return ClientConnectionRecoveryResultKind.Transport;
                }

                _visitSessionService.SetTargetRole(
                    ClientVisitRole.Visitor,
                    descriptor.VisitSessionID);
                var reconnect = await _gameplayChannel.ReconnectPendingVisitAsync(
                    admission.Value.VisitRevision,
                    linked.Token);
                if (!IsCurrent(intent) || !reconnect.IsSuccess ||
                    reconnect.Value.Result?.Snapshot == null ||
                    !IsApplySuccess(_visitSessionService.ApplySnapshot(
                        reconnect.Value.Result.Snapshot,
                        ClientVisitRole.Visitor)))
                {
                    return await FinishReconnectFailureAsync(intent, reconnect, linked.Token);
                }

                var world = await _gameplayChannel.SendAsync(
                    ClientGameplayCatalog.WorldSnapshot,
                    new WorldSnapshotRequest(),
                    linked.Token);
                if (!IsCurrent(intent) || !world.IsSuccess || world.Value.Snapshot == null ||
                    !IsApplySuccess(_personalWorldService.ApplyWorldSnapshot(world.Value.Snapshot)))
                {
                    await CloseFailedConnectionAsync();
                    FinishFailure(
                        intent,
                        ClientWorldFlowState.ConnectionLost,
                        MapGameplayFailure(world),
                        null);
                    return MapRecoveryGameplayFailure(world);
                }

                if (!ValidateRecoveredProjection(descriptor))
                {
                    await CloseFailedConnectionAsync();
                    FinishFailure(
                        intent,
                        ClientWorldFlowState.ConnectionLost,
                        ClientWorldFlowFailure.Protocol,
                        null);
                    return ClientConnectionRecoveryResultKind.Protocol;
                }

                return FinishSuccess(
                    intent,
                    ClientWorldFlowState.Visiting,
                    descriptor.VisitSessionID,
                    null)
                    ? ClientConnectionRecoveryResultKind.Succeeded
                    : ClientConnectionRecoveryResultKind.Protocol;
            }
        }

        /// <summary>
        /// 在唯一 Session lineage 失效后原子退役当前 world intent、target 与业务投影。
        /// </summary>
        /// <remarks>
        /// 该边界不尝试远端 leave 或重建 OwnWorld，因为调用时已经没有可授权 Session。
        /// 迟到的 HTTP/gameplay completion 会被 intent 与 target generation 拒绝。
        /// </remarks>
        internal void InvalidateSession()
        {
            ClientWorldFlowSnapshot committed;
            lock (_sync)
            {
                if (_snapshot.State == ClientWorldFlowState.Stopped)
                {
                    return;
                }

                _intentActive = false;
                _recoveryTarget = null;
                SetSnapshotLocked(
                    ClientWorldFlowState.Inactive,
                    _snapshot.TargetGeneration + 1,
                    ClientWorldFlowFailure.None,
                    null,
                    null);
                committed = _snapshot;
            }

            _visitSessionService.ClearTargetRole();
            _personalWorldService.ClearCurrentTarget();
            Notify(committed);
        }

        /// <summary>停止新 flow、解除 subscriber，并使迟到 completion 失效。</summary>
        /// <param name="cancellationToken">AppLifetime 共享停止信号。</param>
        /// <returns>本地 flow owner 已停止时完成。</returns>
        public Task StopAsync(CancellationToken cancellationToken)
        {
            CancellationTokenSource lifetime;
            ClientWorldFlowSnapshot committed;
            lock (_sync)
            {
                if (_snapshot.State == ClientWorldFlowState.Stopped)
                {
                    return Task.CompletedTask;
                }

                if (_subscribed)
                {
                    _personalWorldService.ProjectionConflict -= OnProjectionConflict;
                    _personalWorldService.Changed -= OnPersonalWorldChanged;
                    _visitSessionService.ProjectionConflict -= OnProjectionConflict;
                    _visitSessionService.Changed -= OnVisitSessionChanged;
                    _visitSessionService.SafeReturnReceived -= OnSafeReturn;
                    _gameplayChannel.UnexpectedDisconnect -= OnGameplayUnexpectedDisconnect;
                    _subscribed = false;
                }

                lifetime = _lifetimeCancellation;
                _lifetimeCancellation = null;
                _intentActive = false;
                _recoveryTarget = null;
                _visitSessionService.ClearTargetRole();
                SetSnapshotLocked(
                    ClientWorldFlowState.Stopped,
                    _snapshot.TargetGeneration + 1,
                    ClientWorldFlowFailure.Stopped,
                    null,
                    _snapshot.SafeReturn);
                committed = _snapshot;
            }

            lifetime?.Cancel();
            lifetime?.Dispose();
            Notify(committed);
            return Task.CompletedTask;
        }

        /// <summary>执行 bootstrap、own admission、connect 与完整 snapshot 流程。</summary>
        /// <param name="intent">已取得的唯一 flow intent。</param>
        /// <param name="cancellationToken">Caller 或嵌套 return 信号。</param>
        /// <param name="recoveryTarget">Gameplay恢复时冻结的target；普通进入OwnWorld时为空。</param>
        /// <returns>进入 OwnWorld 时返回 true。</returns>
        private async Task<bool> ResolveOwnWorldAsync(
            WorldFlowIntent intent,
            CancellationToken cancellationToken,
            ClientRecoveryTargetDescriptor recoveryTarget = null)
        {
            using (var linked = Link(cancellationToken))
            {
                var bootstrap = await _sessionCoordinator.GetWorldBootstrapAsync(linked.Token);
                if (!IsCurrentAfterAuthorizedOperation(intent, bootstrap.IsSuccess) ||
                    !bootstrap.IsSuccess ||
                    !IsApplySuccess(_personalWorldService.ApplyBootstrap(bootstrap.Value)))
                {
                    FinishOwnFailure(intent, MapHttpFailure(bootstrap));
                    return false;
                }

                var admission = await _sessionCoordinator.IssueWorldAdmissionAsync(
                    ClientWorldAdmissionTarget.OwnWorld(),
                    NewIdempotencyKey(),
                    linked.Token);
                if (!IsCurrentAfterAuthorizedOperation(intent, admission.IsSuccess) ||
                    !admission.IsSuccess ||
                    admission.Value.Role != ClientWorldRole.Owner ||
                    admission.Value.Purpose != ClientWorldAdmissionPurpose.OwnWorld)
                {
                    FinishOwnFailure(intent, MapHttpFailure(admission));
                    return false;
                }

                var channelState = _gameplayChannel.Snapshot.State;
                if (channelState == ClientGameplayChannelState.Active ||
                    channelState == ClientGameplayChannelState.Pending ||
                    channelState == ClientGameplayChannelState.Connecting)
                {
                    try
                    {
                        await _gameplayChannel.CloseAsync(linked.Token);
                    }
                    catch (OperationCanceledException)
                    {
                        FinishOwnFailure(intent, ClientWorldFlowFailure.CallerCancelled);
                        return false;
                    }
                }

                if (!IsCurrent(intent) ||
                    !await _gameplayChannel.ConnectAsync(admission.Value, linked.Token))
                {
                    FinishOwnFailure(intent, ClientWorldFlowFailure.Transport);
                    return false;
                }

                _personalWorldService.ClearCurrentTarget();
                _visitSessionService.SetTargetRole(ClientVisitRole.Owner, null);
                var world = await _gameplayChannel.SendAsync(
                    ClientGameplayCatalog.WorldSnapshot,
                    new WorldSnapshotRequest(),
                    linked.Token);
                var worldApplied = world.IsSuccess && world.Value.Snapshot != null &&
                                   IsApplySuccess(_personalWorldService.ApplyWorldSnapshot(world.Value.Snapshot));
                if (!IsCurrent(intent) || !worldApplied ||
                    _personalWorldService.Snapshot.CurrentWorld?.Assignment == null)
                {
                    await CloseFailedConnectionAsync();
                    FinishOwnFailure(intent, MapGameplayFailure(world));
                    return false;
                }

                if (recoveryTarget != null &&
                    !string.IsNullOrEmpty(recoveryTarget.VisitSessionID))
                {
                    var visit = await _gameplayChannel.SendAsync(
                        ClientGameplayCatalog.VisitSnapshot,
                        new VisitSnapshotRequest(),
                        linked.Token);
                    if (visit?.ServerError != null &&
                        IsAuthoritativeRecoveryUnavailable(visit.ServerError.Code))
                    {
                        // 该错误帧之后server会关闭携带旧访问target的connection。先退役旧投影，
                        // 再在同一intent/deadline内建立一次不携带VisitSession的OwnWorld连接；
                        // 这是由权威拒绝触发的单次状态迁移，不是按时间或次数猜测的重试。
                        _visitSessionService.ClearTargetRole();
                        _personalWorldService.ClearCurrentTarget();
                        await CloseFailedConnectionAsync();
                        return await ResolveOwnWorldAsync(
                            intent,
                            linked.Token,
                            recoveryTarget: null);
                    }
                    else
                    {
                        var visitApplied = visit.IsSuccess && visit.Value.Snapshot != null &&
                                           IsApplySuccess(_visitSessionService.ApplySnapshot(
                                               visit.Value.Snapshot,
                                               ClientVisitRole.Owner));
                        var currentVisit = _visitSessionService.Snapshot.Current;
                        var currentWorld = _personalWorldService.Snapshot.CurrentWorld;
                        if (!IsCurrent(intent) || !visitApplied || currentVisit == null ||
                            !string.Equals(
                                currentVisit.VisitSessionID,
                                recoveryTarget.VisitSessionID,
                                StringComparison.Ordinal) ||
                            currentVisit.Revision < recoveryTarget.VisitRevision ||
                            currentWorld?.Assignment == null ||
                            !currentVisit.Assignment.HasSameIdentity(currentWorld.Assignment))
                        {
                            await CloseFailedConnectionAsync();
                            _visitSessionService.ClearTargetRole();
                            _personalWorldService.ClearCurrentTarget();
                            FinishOwnFailure(
                                intent,
                                visit.IsSuccess
                                    ? ClientWorldFlowFailure.Protocol
                                    : MapGameplayFailure(visit));
                            return false;
                        }
                    }
                }

                var committed = FinishSuccess(intent, ClientWorldFlowState.OwnWorld, null, null);
                if (!committed)
                {
                    await CloseFailedConnectionAsync();
                }

                return committed;
            }
        }

        /// <summary>关闭已建立但未提交 target 的 gameplay generation，并有界观察全部 owner 退出。</summary>
        /// <returns>Connection 已进入不可发送状态，或观察 deadline 到期时完成。</returns>
        private async Task CloseFailedConnectionAsync()
        {
            using (var timeout = new CancellationTokenSource(FailedConnectionCleanupTimeout))
            {
                try
                {
                    await _gameplayChannel.CloseAsync(timeout.Token);
                }
                catch (OperationCanceledException) when (timeout.IsCancellationRequested)
                {
                    // CloseAsync 已在线性化临界区撤销 connection；这里只放弃继续等待异常 transport owner。
                }
            }
        }

        /// <summary>在锁内尝试取得唯一 flow intent 并递增 target generation。</summary>
        /// <param name="required">允许的 source state。</param>
        /// <param name="next">准备提交的 transition state。</param>
        /// <param name="visitSessionID">可选 Visitor target。</param>
        /// <param name="safeReturn">可选权威 safe-return。</param>
        /// <param name="intent">成功时返回 generation/session gate。</param>
        /// <returns>成功取得唯一 intent 时返回 true。</returns>
        private bool TryBegin(
            ClientWorldFlowState required,
            ClientWorldFlowState next,
            string visitSessionID,
            ClientSafeReturnProjection safeReturn,
            out WorldFlowIntent intent)
        {
            ClientWorldFlowSnapshot committed;
            lock (_sync)
            {
                if (_snapshot.State != required || _intentActive ||
                    !TryCaptureSessionGeneration(out var sessionGeneration))
                {
                    intent = null;
                    return false;
                }

                _intentActive = true;
                var targetGeneration = _snapshot.TargetGeneration + 1;
                intent = new WorldFlowIntent(targetGeneration, sessionGeneration);
                SetSnapshotLocked(
                    next,
                    targetGeneration,
                    ClientWorldFlowFailure.None,
                    visitSessionID,
                    safeReturn);
                committed = _snapshot;
            }

            Notify(committed);
            return true;
        }

        /// <summary>提交一个仍 current intent 的成功 terminal state。</summary>
        /// <param name="intent">待提交 intent。</param>
        /// <param name="state">OwnWorld 或 Visiting。</param>
        /// <param name="visitSessionID">Visitor target；OwnWorld 时为空。</param>
        /// <param name="safeReturn">可选权威返回投影。</param>
        /// <returns>Intent 仍 current 且已提交时返回 true。</returns>
        private bool FinishSuccess(
            WorldFlowIntent intent,
            ClientWorldFlowState state,
            string visitSessionID,
            ClientSafeReturnProjection safeReturn)
        {
            if (!TryBuildRecoveryTarget(
                    state,
                    intent,
                    visitSessionID,
                    out var recoveryTarget))
            {
                return false;
            }

            ClientWorldFlowSnapshot committed;
            lock (_sync)
            {
                if (!IsCurrentLocked(intent))
                {
                    return false;
                }

                _intentActive = false;
                _recoveryTarget = recoveryTarget;
                SetSnapshotLocked(
                    state,
                    intent.TargetGeneration,
                    ClientWorldFlowFailure.None,
                    visitSessionID,
                    safeReturn);
                committed = _snapshot;
            }

            Notify(committed);
            return true;
        }

        /// <summary>在 join 尚未关闭 own target 时恢复 OwnWorld。</summary>
        /// <param name="intent">Join intent。</param>
        /// <param name="failure">低敏失败。</param>
        private void FinishJoinFailure(WorldFlowIntent intent, ClientWorldFlowFailure failure)
        {
            FinishFailure(intent, ClientWorldFlowState.OwnWorld, failure, null);
        }

        /// <summary>
        /// 将已过期、已撤销或不再存在的 inbox invite 提交为稳定拒绝，避免接受按钮静默无响应。
        /// </summary>
        /// <returns>固定返回 false，供 join action 直接结束。</returns>
        private bool RejectUnavailableInvite()
        {
            ClientWorldFlowSnapshot committed;
            lock (_sync)
            {
                if (_snapshot.State != ClientWorldFlowState.OwnWorld || _intentActive)
                {
                    return false;
                }

                SetSnapshotLocked(
                    ClientWorldFlowState.OwnWorld,
                    _snapshot.TargetGeneration,
                    ClientWorldFlowFailure.InviteUnavailable,
                    null,
                    _snapshot.SafeReturn);
                committed = _snapshot;
            }

            Notify(committed);
            return false;
        }

        /// <summary>在旧 target 已关闭后保持 ReturningOwnWorld，绝不复活 Visiting。</summary>
        /// <param name="intent">Join intent。</param>
        /// <param name="failure">低敏失败。</param>
        private void BeginReturningAfterJoinFailure(WorldFlowIntent intent, ClientWorldFlowFailure failure)
        {
            _visitSessionService.ClearTargetRole();
            FinishFailure(intent, ClientWorldFlowState.ReturningOwnWorld, failure, null);
        }

        /// <summary>提交 own-world 解析失败；return flow 保持 ReturningOwnWorld。</summary>
        /// <param name="intent">Own-world intent。</param>
        /// <param name="failure">低敏失败。</param>
        private void FinishOwnFailure(WorldFlowIntent intent, ClientWorldFlowFailure failure)
        {
            var current = Snapshot.State;
            var state = current == ClientWorldFlowState.ResolvingOwnWorld
                ? ClientWorldFlowState.Inactive
                : current == ClientWorldFlowState.RecoveringTarget
                    ? ClientWorldFlowState.ConnectionLost
                    : ClientWorldFlowState.ReturningOwnWorld;
            FinishFailure(intent, state, failure, null);
        }

        /// <summary>提交 return mutation/解析失败。</summary>
        /// <param name="intent">Return intent。</param>
        /// <param name="failure">低敏失败。</param>
        private void FinishReturnFailure(WorldFlowIntent intent, ClientWorldFlowFailure failure)
        {
            FinishFailure(intent, ClientWorldFlowState.ReturningOwnWorld, failure, Snapshot.SafeReturn);
        }

        /// <summary>提交一个仍 current intent 的稳定失败。</summary>
        /// <param name="intent">待提交 intent。</param>
        /// <param name="state">失败后的安全状态。</param>
        /// <param name="failure">低敏失败。</param>
        /// <param name="safeReturn">可选权威返回投影。</param>
        private void FinishFailure(
            WorldFlowIntent intent,
            ClientWorldFlowState state,
            ClientWorldFlowFailure failure,
            ClientSafeReturnProjection safeReturn)
        {
            ClientWorldFlowSnapshot committed;
            lock (_sync)
            {
                if (!IsCurrentLocked(intent))
                {
                    return;
                }

                _intentActive = false;
                if (state != ClientWorldFlowState.OwnWorld &&
                    state != ClientWorldFlowState.Visiting &&
                    state != ClientWorldFlowState.ConnectionLost)
                {
                    _recoveryTarget = null;
                }
                SetSnapshotLocked(state, intent.TargetGeneration, failure, null, safeReturn);
                committed = _snapshot;
            }

            Notify(committed);
        }

        /// <summary>响应 Service 同 revision 冲突，关闭旧 mutation gate 并使 intent 失效。</summary>
        private void OnProjectionConflict()
        {
            ClientWorldFlowSnapshot committed = null;
            lock (_sync)
            {
                if (_snapshot.State == ClientWorldFlowState.Stopped)
                {
                    return;
                }

                _intentActive = false;
                _recoveryTarget = null;
                var state = _snapshot.State == ClientWorldFlowState.Visiting ||
                            _snapshot.State == ClientWorldFlowState.JoiningVisit
                    ? ClientWorldFlowState.ReturningOwnWorld
                    : ClientWorldFlowState.Inactive;
                SetSnapshotLocked(
                    state,
                    _snapshot.TargetGeneration + 1,
                    ClientWorldFlowFailure.Protocol,
                    null,
                    _snapshot.SafeReturn);
                committed = _snapshot;
            }

            _visitSessionService.ClearTargetRole();
            Notify(committed);
        }

        /// <summary>让 terminal world target 在 gameplay 非预期断开时立即进入不可交互失败态。</summary>
        /// <param name="channel">Gameplay owner 发布的 final Ready snapshot 与低敏关闭原因。</param>
        private void OnGameplayUnexpectedDisconnect(ClientGameplayChannelSnapshot channel)
        {
            RefreshRecoveryTargetIfStable();
            ClientWorldFlowSnapshot committed;
            lock (_sync)
            {
                if (_intentActive ||
                    (_snapshot.State != ClientWorldFlowState.OwnWorld &&
                     _snapshot.State != ClientWorldFlowState.Visiting))
                {
                    return;
                }

                SetSnapshotLocked(
                    ClientWorldFlowState.ConnectionLost,
                    _snapshot.TargetGeneration + 1,
                    channel.CloseReason == ClientGameplayCloseReason.Protocol
                        ? ClientWorldFlowFailure.Protocol
                        : ClientWorldFlowFailure.Transport,
                    null,
                    _snapshot.SafeReturn);
                committed = _snapshot;
            }

            _visitSessionService.ClearTargetRole();
            _personalWorldService.ClearCurrentTarget();
            Notify(committed);
        }

        /// <summary>接收 active Visitor target 的权威 safe-return 并自动启动 own-world 解析。</summary>
        /// <param name="safeReturn">不可由 caller 改写的返回指令。</param>
        /// <remarks>仅作为 event handler 使用；异步取消和运行异常由返回流程收敛为低敏 flow failure。</remarks>
        private async void OnSafeReturn(ClientSafeReturnProjection safeReturn)
        {
            await StartAuthoritativeReturnAsync(safeReturn.VisitSessionID, safeReturn);
        }

        /// <summary>在完整 world replacement 撤销 assignment 时启动受控返回。</summary>
        /// <param name="snapshot">PersonalWorld Service 不可变 snapshot。</param>
        /// <remarks>仅作为 event handler 使用；异步取消和运行异常由返回流程收敛为低敏 flow failure。</remarks>
        private async void OnPersonalWorldChanged(ClientPersonalWorldServiceSnapshot snapshot)
        {
            RefreshRecoveryTargetIfStable();
            var flow = Snapshot;
            if (flow.State == ClientWorldFlowState.Visiting &&
                snapshot.CurrentWorld != null && snapshot.CurrentWorld.Assignment == null)
            {
                await StartAuthoritativeReturnAsync(flow.VisitSessionID, null);
            }
        }

        /// <summary>在完整 VisitSession replacement 进入 Closed 时启动受控返回。</summary>
        /// <param name="snapshot">VisitSession Service 不可变 snapshot。</param>
        /// <remarks>仅作为 event handler 使用；异步取消和运行异常由返回流程收敛为低敏 flow failure。</remarks>
        private async void OnVisitSessionChanged(ClientVisitSessionServiceSnapshot snapshot)
        {
            RefreshRecoveryTargetIfStable();
            var flow = Snapshot;
            if (flow.State == ClientWorldFlowState.Visiting &&
                snapshot.Current != null && snapshot.Current.Lifecycle == ClientVisitLifecycle.Closed)
            {
                await StartAuthoritativeReturnAsync(flow.VisitSessionID, null);
            }
        }

        /// <summary>用稳定 target 的最新完整 Service 投影刷新无 credential 恢复描述。</summary>
        /// <remarks>
        /// Owner 可在进入 OwnWorld 后再开放 VisitSession；恢复描述必须随完整 gameplay replacement
        /// 更新，但 flow 离开稳定 target 后保持冻结，不能用断线后的局部状态重写恢复目标。
        /// </remarks>
        private void RefreshRecoveryTargetIfStable()
        {
            ClientWorldFlowSnapshot flow;
            lock (_sync)
            {
                if (_intentActive ||
                    (_snapshot.State != ClientWorldFlowState.OwnWorld &&
                     _snapshot.State != ClientWorldFlowState.Visiting))
                {
                    return;
                }

                flow = _snapshot;
            }

            if (!_sessionCoordinator.TryGetCurrent(out var session) ||
                !TryBuildRecoveryTarget(
                    flow.State,
                    new WorldFlowIntent(flow.TargetGeneration, session.Generation),
                    flow.VisitSessionID,
                    out var refreshed))
            {
                return;
            }

            lock (_sync)
            {
                if (ReferenceEquals(_snapshot, flow) && !_intentActive)
                {
                    _recoveryTarget = refreshed;
                }
            }
        }

        /// <summary>关闭旧 Visitor target 并重新解析 own-world。</summary>
        /// <param name="visitSessionID">必须匹配 current Visitor target。</param>
        /// <param name="safeReturn">可选 gameplay 权威返回投影。</param>
        /// <returns>返回流程完成时结束；所有异常均收敛为低敏 flow failure。</returns>
        private async Task StartAuthoritativeReturnAsync(
            string visitSessionID,
            ClientSafeReturnProjection safeReturn)
        {
            if (!TryBegin(
                    ClientWorldFlowState.Visiting,
                    ClientWorldFlowState.ReturningOwnWorld,
                    visitSessionID,
                    safeReturn,
                    out var intent))
            {
                return;
            }

            try
            {
                using (var linked = Link(CancellationToken.None))
                {
                    await _gameplayChannel.CloseAsync(linked.Token);
                    _visitSessionService.ClearTargetRole();
                    _personalWorldService.ClearCurrentTarget();
                    await ResolveOwnWorldAsync(intent, linked.Token);
                }
            }
            catch (OperationCanceledException)
            {
                FinishReturnFailure(intent, ClientWorldFlowFailure.CallerCancelled);
            }
            catch
            {
                FinishReturnFailure(intent, ClientWorldFlowFailure.Transport);
            }
        }

        /// <summary>从已提交projection构造无credential恢复descriptor。</summary>
        private bool TryBuildRecoveryTarget(
            ClientWorldFlowState state,
            WorldFlowIntent intent,
            string visitSessionID,
            out ClientRecoveryTargetDescriptor descriptor)
        {
            descriptor = null;
            if (!_sessionCoordinator.TryGetCurrent(out var session) ||
                session.Generation != intent.SessionGeneration)
            {
                return false;
            }

            var world = _personalWorldService.Snapshot.CurrentWorld;
            var assignment = world?.Assignment;
            if (world == null || assignment == null)
            {
                return false;
            }

            var visit = _visitSessionService.Snapshot.Current;
            if (state == ClientWorldFlowState.Visiting)
            {
                if (visit == null || visit.Role != ClientVisitRole.Visitor ||
                    !string.Equals(visit.VisitSessionID, visitSessionID, StringComparison.Ordinal) ||
                    !visit.Assignment.HasSameIdentity(assignment))
                {
                    return false;
                }

                descriptor = new ClientRecoveryTargetDescriptor(
                    ClientRecoveryTargetKind.Visiting,
                    intent.SessionGeneration,
                    session.Session.SessionID,
                    session.Session.SessionEpoch,
                    intent.TargetGeneration,
                    world.PersonalWorldID,
                    assignment.WorldInstanceID,
                    visit.VisitSessionID,
                    world.Revision,
                    visit.Revision,
                    assignment.Generation,
                    visit.ExpiresAtMilliseconds);
                return true;
            }

            if (state != ClientWorldFlowState.OwnWorld)
            {
                return false;
            }

            var ownerVisit = visit != null && visit.Role == ClientVisitRole.Owner &&
                             visit.Assignment.HasSameIdentity(assignment)
                ? visit
                : null;
            descriptor = new ClientRecoveryTargetDescriptor(
                ClientRecoveryTargetKind.OwnWorld,
                intent.SessionGeneration,
                session.Session.SessionID,
                session.Session.SessionEpoch,
                intent.TargetGeneration,
                world.PersonalWorldID,
                assignment.WorldInstanceID,
                ownerVisit?.VisitSessionID,
                world.Revision,
                ownerVisit?.Revision ?? 0,
                assignment.Generation,
                0);
            return true;
        }

        /// <summary>验证current projection没有倒退或混入其他target identity。</summary>
        private bool ValidateRecoveredProjection(ClientRecoveryTargetDescriptor descriptor)
        {
            if (descriptor == null)
            {
                return false;
            }

            var world = _personalWorldService.Snapshot.CurrentWorld;
            var assignment = world?.Assignment;
            if (world == null || assignment == null ||
                !string.Equals(
                    world.PersonalWorldID,
                    descriptor.PersonalWorldID,
                    StringComparison.Ordinal) ||
                world.Revision < descriptor.WorldRevision ||
                assignment.Generation < descriptor.AssignmentGeneration ||
                assignment.Generation == descriptor.AssignmentGeneration &&
                !string.Equals(
                    assignment.WorldInstanceID,
                    descriptor.WorldInstanceID,
                    StringComparison.Ordinal))
            {
                return false;
            }

            if (string.IsNullOrEmpty(descriptor.VisitSessionID))
            {
                return descriptor.Kind == ClientRecoveryTargetKind.OwnWorld;
            }

            var visit = _visitSessionService.Snapshot.Current;
            var requiredRole = descriptor.Kind == ClientRecoveryTargetKind.Visiting
                ? ClientVisitRole.Visitor
                : ClientVisitRole.Owner;
            return visit != null && visit.Role == requiredRole &&
                   string.Equals(
                       visit.VisitSessionID,
                       descriptor.VisitSessionID,
                       StringComparison.Ordinal) &&
                   visit.Revision >= descriptor.VisitRevision &&
                   visit.Assignment.HasSameIdentity(assignment);
        }

        /// <summary>同时验证source session、flow generation与current projection。</summary>
        private bool ValidateDescriptorAgainstCurrent(
            ClientRecoveryTargetDescriptor descriptor,
            bool requireNewTargetGeneration)
        {
            ClientWorldFlowSnapshot flow;
            ClientRecoveryTargetDescriptor current;
            lock (_sync)
            {
                flow = _snapshot;
                current = _recoveryTarget;
            }

            if (!_sessionCoordinator.TryGetCurrent(out var session) || descriptor == null ||
                !descriptor.IsBoundTo(session) || current == null ||
                (flow.State != ClientWorldFlowState.OwnWorld &&
                 flow.State != ClientWorldFlowState.Visiting) ||
                requireNewTargetGeneration && flow.TargetGeneration <= descriptor.TargetGeneration)
            {
                return false;
            }

            return ValidateRecoveredProjection(descriptor);
        }

        /// <summary>把权威Visitor失效转换为安全返回OwnWorld。</summary>
        private async Task<ClientConnectionRecoveryResultKind> FinishRecoveryAdmissionFailureAsync(
            WorldFlowIntent intent,
            ClientHttpResult<ClientWorldAdmissionLease> result,
            CancellationToken cancellationToken)
        {
            if (result?.ServerError != null &&
                IsAuthoritativeRecoveryUnavailable(result.ServerError.Code))
            {
                return await ReturnOwnAfterRecoveryRejectionAsync(intent, cancellationToken);
            }

            var mapped = MapRecoveryHttpFailure(result);
            FinishFailure(
                intent,
                ClientWorldFlowState.ConnectionLost,
                mapped == ClientConnectionRecoveryResultKind.Protocol
                    ? ClientWorldFlowFailure.Protocol
                    : ClientWorldFlowFailure.Transport,
                null);
            return mapped;
        }

        /// <summary>处理RECONNECT首帧的拒绝、断开或协议失败。</summary>
        private async Task<ClientConnectionRecoveryResultKind> FinishReconnectFailureAsync(
            WorldFlowIntent intent,
            ClientGameplayResult<VisitReconnectResponse> result,
            CancellationToken cancellationToken)
        {
            await CloseFailedConnectionAsync();
            if (result?.ServerError != null &&
                IsAuthoritativeRecoveryUnavailable(result.ServerError.Code))
            {
                return await ReturnOwnAfterRecoveryRejectionAsync(intent, cancellationToken);
            }

            var mapped = MapRecoveryGameplayFailure(result);
            FinishFailure(
                intent,
                ClientWorldFlowState.ConnectionLost,
                mapped == ClientConnectionRecoveryResultKind.Protocol
                    ? ClientWorldFlowFailure.Protocol
                    : ClientWorldFlowFailure.Transport,
                null);
            return mapped;
        }

        /// <summary>退役无效Visitor target并在同一恢复预算内进入OwnWorld。</summary>
        private async Task<ClientConnectionRecoveryResultKind> ReturnOwnAfterRecoveryRejectionAsync(
            WorldFlowIntent intent,
            CancellationToken cancellationToken)
        {
            _visitSessionService.ClearTargetRole();
            _personalWorldService.ClearCurrentTarget();
            FinishFailure(
                intent,
                ClientWorldFlowState.ReturningOwnWorld,
                ClientWorldFlowFailure.Rejected,
                null);
            if (!await RetryReturnAsync(cancellationToken))
            {
                return MapRecoveryFlowFailure(Snapshot.Failure);
            }

            return ClientConnectionRecoveryResultKind.ReturningOwnWorld;
        }

        /// <summary>识别明确证明旧VisitSession或其world assignment不可恢复的服务端错误。</summary>
        private static bool IsAuthoritativeRecoveryUnavailable(long code)
        {
            return code == 2002 || code == 2100 || code == 2104 || code == 2107 ||
                   code == 2108 || code == 2109;
        }

        /// <summary>将HTTP恢复失败收敛为固定低敏分类。</summary>
        private static ClientConnectionRecoveryResultKind MapRecoveryHttpFailure<T>(
            ClientHttpResult<T> result)
        {
            if (result?.ServerError != null)
            {
                return result.ServerError.Category == ClientServerErrorCategory.Authentication
                    ? ClientConnectionRecoveryResultKind.Authentication
                    : ClientConnectionRecoveryResultKind.Policy;
            }

            switch (result?.Failure?.Kind)
            {
                case ClientHttpFailureKind.Timeout:
                case ClientHttpFailureKind.Transport:
                    return ClientConnectionRecoveryResultKind.Transport;
                case ClientHttpFailureKind.MalformedResponse:
                case ClientHttpFailureKind.ResponseTooLarge:
                    return ClientConnectionRecoveryResultKind.Protocol;
                case ClientHttpFailureKind.Stopped:
                    return ClientConnectionRecoveryResultKind.Stopped;
                default:
                    return ClientConnectionRecoveryResultKind.Policy;
            }
        }

        /// <summary>将gameplay恢复失败收敛为固定低敏分类。</summary>
        private static ClientConnectionRecoveryResultKind MapRecoveryGameplayFailure<T>(
            ClientGameplayResult<T> result)
            where T : class
        {
            if (result?.ServerError != null)
            {
                return result.ServerError.Code >= 100 && result.ServerError.Code <= 103
                    ? ClientConnectionRecoveryResultKind.Authentication
                    : ClientConnectionRecoveryResultKind.Policy;
            }

            switch (result?.Failure)
            {
                case ClientGameplayFailureKind.Timeout:
                case ClientGameplayFailureKind.Disconnected:
                    return ClientConnectionRecoveryResultKind.Transport;
                case ClientGameplayFailureKind.Protocol:
                    return ClientConnectionRecoveryResultKind.Protocol;
                default:
                    return ClientConnectionRecoveryResultKind.Policy;
            }
        }

        /// <summary>将world flow失败收敛为恢复分类。</summary>
        private static ClientConnectionRecoveryResultKind MapRecoveryFlowFailure(
            ClientWorldFlowFailure failure)
        {
            switch (failure)
            {
                case ClientWorldFlowFailure.Transport:
                case ClientWorldFlowFailure.CommitUnknown:
                    return ClientConnectionRecoveryResultKind.Transport;
                case ClientWorldFlowFailure.Protocol:
                    return ClientConnectionRecoveryResultKind.Protocol;
                case ClientWorldFlowFailure.Stopped:
                    return ClientConnectionRecoveryResultKind.Stopped;
                default:
                    return ClientConnectionRecoveryResultKind.Policy;
            }
        }

        /// <summary>查找当前未过期 invite inbox 条目。</summary>
        /// <param name="visitSessionID">VisitSessionID。</param>
        /// <param name="inviteID">InviteID。</param>
        /// <returns>匹配 invite；不存在时为空。</returns>
        private ClientVisitInviteProjection FindInvite(string visitSessionID, string inviteID)
        {
            foreach (var invite in _visitSessionService.Snapshot.Invites)
            {
                if (string.Equals(invite.VisitSessionID, visitSessionID, StringComparison.Ordinal) &&
                    string.Equals(invite.InviteID, inviteID, StringComparison.Ordinal))
                {
                    return invite;
                }
            }

            return null;
        }

        /// <summary>创建同时绑定 caller 与 AppLifetime 的取消信号。</summary>
        /// <param name="caller">Caller cancellation。</param>
        /// <returns>Linked cancellation owner。</returns>
        private CancellationTokenSource Link(CancellationToken caller)
        {
            lock (_sync)
            {
                if (_lifetimeCancellation == null)
                {
                    var stopped = new CancellationTokenSource();
                    stopped.Cancel();
                    return stopped;
                }

                return CancellationTokenSource.CreateLinkedTokenSource(
                    caller,
                    _lifetimeCancellation.Token);
            }
        }

        /// <summary>捕获 current authenticated session generation。</summary>
        /// <param name="generation">成功时返回 generation。</param>
        /// <returns>Session current 时返回 true。</returns>
        private bool TryCaptureSessionGeneration(out long generation)
        {
            if (_sessionCoordinator.TryGetCurrent(out var session))
            {
                generation = session.Generation;
                return true;
            }

            generation = 0;
            return false;
        }

        /// <summary>同时比较 target 与 session generation。</summary>
        /// <param name="intent">待验证 intent。</param>
        /// <returns>仍 current 时返回 true。</returns>
        private bool IsCurrent(WorldFlowIntent intent)
        {
            lock (_sync)
            {
                return IsCurrentLocked(intent);
            }
        }

        /// <summary>在成功授权操作完成后接受同一 refresh lineage 的 Session generation 前进。</summary>
        /// <param name="intent">发起授权操作的 current flow intent。</param>
        /// <param name="operationSucceeded">Session owner 已确认操作属于其 current lineage 时为 true。</param>
        /// <returns>Intent 仍拥有 target 且已绑定 current Session generation 时返回 true。</returns>
        /// <remarks>
        /// Session owner 会在发送 Bearer 请求前完成 single-flight refresh，并拒绝被新登录取代的
        /// 迟到结果。因此这里只允许成功操作推进同一 intent 的 Session gate；失败结果仍按原 generation
        /// 校验，不能借此跨账号或跨 lineage 继续 world flow。
        /// </remarks>
        private bool IsCurrentAfterAuthorizedOperation(
            WorldFlowIntent intent,
            bool operationSucceeded)
        {
            if (!operationSucceeded)
            {
                return IsCurrent(intent);
            }

            lock (_sync)
            {
                if (_snapshot.State == ClientWorldFlowState.Stopped || !_intentActive ||
                    _snapshot.TargetGeneration != intent.TargetGeneration ||
                    !_sessionCoordinator.TryGetCurrent(out var session) ||
                    session.Generation < intent.SessionGeneration)
                {
                    return false;
                }

                intent.AdvanceSessionGeneration(session.Generation);
                return true;
            }
        }

        /// <summary>在 coordinator 锁内比较 target 与 session generation。</summary>
        /// <param name="intent">待验证 intent。</param>
        /// <returns>仍 current 时返回 true。</returns>
        private bool IsCurrentLocked(WorldFlowIntent intent)
        {
            return _snapshot.State != ClientWorldFlowState.Stopped &&
                   _intentActive &&
                   _snapshot.TargetGeneration == intent.TargetGeneration &&
                   TryCaptureSessionGeneration(out var generation) &&
                   generation == intent.SessionGeneration;
        }

        /// <summary>判断 projection gate 是否允许 flow 继续。</summary>
        /// <param name="result">Projection apply result。</param>
        /// <returns>Applied 或 Duplicate 时返回 true。</returns>
        private static bool IsApplySuccess(ClientProjectionApplyResult result)
        {
            return result == ClientProjectionApplyResult.Applied ||
                   result == ClientProjectionApplyResult.Duplicate;
        }

        /// <summary>把 accept 的取消/timeout/transport 映射为 commit-unknown。</summary>
        /// <param name="result">Accept result。</param>
        /// <returns>稳定 flow failure。</returns>
        private static ClientWorldFlowFailure MapAcceptFailure(
            ClientHttpResult<ClientVisitReservation> result)
        {
            if (result?.Failure != null &&
                (result.Failure.Kind == ClientHttpFailureKind.CallerCancelled ||
                 result.Failure.Kind == ClientHttpFailureKind.Timeout ||
                 result.Failure.Kind == ClientHttpFailureKind.Transport))
            {
                return ClientWorldFlowFailure.CommitUnknown;
            }

            return MapHttpFailure(result);
        }

        /// <summary>识别足以证明指定 invite 已不可接受的服务端确定性拒绝。</summary>
        /// <param name="result">Accept invite HTTP result。</param>
        /// <returns>仅在错误码属于冻结退役白名单时返回 true。</returns>
        private static bool IsAuthoritativeInviteUnavailable(
            ClientHttpResult<ClientVisitReservation> result)
        {
            var code = result?.ServerError?.Code;
            return code == 101 ||
                   code == 200 ||
                   code == 2100 ||
                   code == 2101 ||
                   code == 2102 ||
                   code == 2104 ||
                   code == 2105;
        }

        /// <summary>映射任意 HTTP result 为低敏 flow failure。</summary>
        /// <typeparam name="T">HTTP success value。</typeparam>
        /// <param name="result">HTTP result。</param>
        /// <returns>稳定 flow failure。</returns>
        private static ClientWorldFlowFailure MapHttpFailure<T>(ClientHttpResult<T> result)
        {
            if (result == null || result.IsSuccess)
            {
                return ClientWorldFlowFailure.Protocol;
            }

            if (result.ServerError != null)
            {
                return ClientWorldFlowFailure.Rejected;
            }

            return result.Failure.Kind == ClientHttpFailureKind.CallerCancelled
                ? ClientWorldFlowFailure.CallerCancelled
                : result.Failure.Kind == ClientHttpFailureKind.MalformedResponse ||
                  result.Failure.Kind == ClientHttpFailureKind.ResponseTooLarge
                    ? ClientWorldFlowFailure.Protocol
                    : result.Failure.Kind == ClientHttpFailureKind.LocalPolicy ||
                      result.Failure.Kind == ClientHttpFailureKind.SecureStorage ||
                      result.Failure.Kind == ClientHttpFailureKind.Stopped
                        ? ClientWorldFlowFailure.Policy
                        : ClientWorldFlowFailure.Transport;
        }

        /// <summary>映射任意 gameplay result 为低敏 flow failure。</summary>
        /// <typeparam name="T">Gameplay response type。</typeparam>
        /// <param name="result">Gameplay result。</param>
        /// <returns>稳定 flow failure。</returns>
        private static ClientWorldFlowFailure MapGameplayFailure<T>(ClientGameplayResult<T> result)
            where T : class
        {
            if (result == null || result.IsSuccess)
            {
                return ClientWorldFlowFailure.Protocol;
            }

            if (result.ServerError != null)
            {
                return ClientWorldFlowFailure.Rejected;
            }

            return result.Failure == ClientGameplayFailureKind.CallerCancelled
                ? ClientWorldFlowFailure.CallerCancelled
                : result.Failure == ClientGameplayFailureKind.Protocol
                    ? ClientWorldFlowFailure.Protocol
                    : result.Failure == ClientGameplayFailureKind.Policy
                        ? ClientWorldFlowFailure.Policy
                        : ClientWorldFlowFailure.Transport;
        }

        /// <summary>生成 128-bit CSPRNG 且符合 header grammar 的 intent identity。</summary>
        /// <returns>32 字符小写十六进制 idempotency key。</returns>
        private static string NewIdempotencyKey()
        {
            var bytes = new byte[16];
            using (var random = RandomNumberGenerator.Create())
            {
                random.GetBytes(bytes);
            }

            return BitConverter.ToString(bytes).Replace("-", string.Empty).ToLowerInvariant();
        }

        /// <summary>在锁内替换低敏 flow snapshot。</summary>
        /// <param name="state">封闭状态。</param>
        /// <param name="generation">Target generation。</param>
        /// <param name="failure">稳定失败。</param>
        /// <param name="visitSessionID">可选 Visitor target。</param>
        /// <param name="safeReturn">可选权威返回。</param>
        private void SetSnapshotLocked(
            ClientWorldFlowState state,
            long generation,
            ClientWorldFlowFailure failure,
            string visitSessionID,
            ClientSafeReturnProjection safeReturn)
        {
            _snapshot = new ClientWorldFlowSnapshot(
                state,
                generation,
                failure,
                visitSessionID,
                safeReturn);
        }

        /// <summary>在锁外通知 subscriber；异常不回滚已提交 flow state。</summary>
        /// <param name="snapshot">已提交不可变 snapshot。</param>
        private void Notify(ClientWorldFlowSnapshot snapshot)
        {
            Changed?.Invoke(snapshot);
        }

        /// <summary>保存一个 target/session 双 generation gate。</summary>
        private sealed class WorldFlowIntent
        {
            /// <summary>创建不可变 intent gate。</summary>
            /// <param name="targetGeneration">本地 target generation。</param>
            /// <param name="sessionGeneration">Session owner generation。</param>
            internal WorldFlowIntent(long targetGeneration, long sessionGeneration)
            {
                TargetGeneration = targetGeneration;
                SessionGeneration = sessionGeneration;
            }

            /// <summary>获取本地 target generation。</summary>
            internal long TargetGeneration { get; }

            /// <summary>获取 Session owner generation。</summary>
            internal long SessionGeneration { get; private set; }

            /// <summary>把同一 refresh lineage 的 gate 单调推进到 Session owner 已提交 generation。</summary>
            /// <param name="generation">不得小于 current gate 的 Session generation。</param>
            internal void AdvanceSessionGeneration(long generation)
            {
                if (generation < SessionGeneration)
                {
                    throw new ArgumentOutOfRangeException(nameof(generation));
                }

                SessionGeneration = generation;
            }
        }
    }
}
