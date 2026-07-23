using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Gameplay;
using IHomeland.Client.Application.Ports;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Foundation.Lifetime;
using IHomeland.Client.Foundation.Time;
using IHomeland.Client.Application.Contracts;

namespace IHomeland.Client.Application.World
{
    /// <summary>
    /// 线性化 own-world、join visit、visiting 与 safe-return 的唯一 App Scope flow owner。
    /// </summary>
    internal sealed class WorldAdmissionCoordinator :
        IAppLifetimeParticipant,
        IClientConnectionRecoveryOperations,
        IWorldAdmissionFlowCommitPort
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
        private readonly IClientGameplayChannelPort _gameplayChannel;

        /// <summary>拥有 primary/current PersonalWorld projection。</summary>
        private readonly PersonalWorldService _personalWorldService;

        /// <summary>拥有 VisitSession projection、commands 与 safe-return callback。</summary>
        private readonly VisitSessionService _visitSessionService;

        /// <summary>纯计算合法 target 迁移与双 generation 提交 gate。</summary>
        private readonly WorldTargetStateMachine _stateMachine =
            new WorldTargetStateMachine();

        /// <summary>统一 HTTP、Gameplay 与 recovery 的低敏失败分类。</summary>
        private readonly WorldAdmissionFailureMapper _failureMapper =
            new WorldAdmissionFailureMapper();

        /// <summary>OwnWorld bootstrap/admission/connect use case。</summary>
        private readonly EnterOwnWorldFlow _enterOwnWorldFlow;

        /// <summary>Accept/JOIN VisitWorld use case。</summary>
        private readonly EnterVisitWorldFlow _enterVisitWorldFlow;

        /// <summary>Visitor leave 与 OwnWorld return use case。</summary>
        private readonly ReturnToOwnWorldFlow _returnToOwnWorldFlow;

        /// <summary>Visitor target admission/RECONNECT 恢复 use case。</summary>
        private readonly ReconnectWorldTargetFlow _reconnectWorldTargetFlow;

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
            IClientGameplayChannelPort gameplayChannel,
            PersonalWorldService personalWorldService,
            VisitSessionService visitSessionService)
        {
            _sessionCoordinator = sessionCoordinator ?? throw new ArgumentNullException(nameof(sessionCoordinator));
            _clock = clock ?? throw new ArgumentNullException(nameof(clock));
            _gameplayChannel = gameplayChannel ?? throw new ArgumentNullException(nameof(gameplayChannel));
            _personalWorldService = personalWorldService ?? throw new ArgumentNullException(nameof(personalWorldService));
            _visitSessionService = visitSessionService ?? throw new ArgumentNullException(nameof(visitSessionService));
            _enterOwnWorldFlow = new EnterOwnWorldFlow(
                _sessionCoordinator,
                _gameplayChannel,
                _personalWorldService,
                _visitSessionService,
                _failureMapper);
            _enterVisitWorldFlow = new EnterVisitWorldFlow(
                _sessionCoordinator,
                _gameplayChannel,
                _personalWorldService,
                _visitSessionService,
                _failureMapper);
            _returnToOwnWorldFlow = new ReturnToOwnWorldFlow(
                _gameplayChannel,
                _personalWorldService,
                _visitSessionService,
                _enterOwnWorldFlow,
                _failureMapper);
            _reconnectWorldTargetFlow = new ReconnectWorldTargetFlow(
                _clock,
                _sessionCoordinator,
                _gameplayChannel,
                _personalWorldService,
                _visitSessionService,
                _failureMapper);
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

            return await _enterOwnWorldFlow.ExecuteAsync(
                this,
                intent,
                cancellationToken);
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

            return await _enterVisitWorldFlow.ExecuteAsync(
                this,
                intent,
                invite,
                cancellationToken);
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

            return await _returnToOwnWorldFlow.ExecuteAsync(
                this,
                intent,
                cancellationToken);
        }

        /// <summary>显式重试已关闭 Visitor target 后失败的 own-world 返回。</summary>
        /// <param name="cancellationToken">取消 caller 等待。</param>
        /// <returns>已重新进入 OwnWorld 时返回 true。</returns>
        internal async Task<bool> RetryReturnAsync(CancellationToken cancellationToken)
        {
            WorldTargetIntentLease intent;
            lock (_sync)
            {
                if (!TryCaptureSessionGeneration(out var sessionGeneration) ||
                    !_stateMachine.TryBegin(
                        _snapshot,
                        _intentActive,
                        ClientWorldFlowState.ReturningOwnWorld,
                        ClientWorldFlowState.ReturningOwnWorld,
                        sessionGeneration,
                        out intent))
                {
                    return false;
                }

                _intentActive = true;
                SetSnapshotLocked(
                    ClientWorldFlowState.ReturningOwnWorld,
                    intent.TargetGeneration,
                    ClientWorldFlowFailure.None,
                    null,
                    _snapshot.SafeReturn);
            }

            Notify(Snapshot);
            return await _enterOwnWorldFlow.ExecuteAsync(
                this,
                intent,
                cancellationToken);
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
                !_gameplayChannel.Snapshot.Active)
            {
                return ClientConnectionRecoveryResultKind.Policy;
            }

            var world = await _gameplayChannel.GetWorldSnapshotAsync(cancellationToken);
            if (!world.IsSuccess || world.Value == null ||
                !IsApplySuccess(_personalWorldService.ApplyWorldSnapshot(world.Value)))
            {
                return _failureMapper.MapRecoveryGameplay(world);
            }

            if (string.IsNullOrEmpty(descriptor.VisitSessionID))
            {
                return ValidateDescriptorAgainstCurrent(descriptor, requireNewTargetGeneration: false)
                    ? ClientConnectionRecoveryResultKind.Succeeded
                    : ClientConnectionRecoveryResultKind.Protocol;
            }

            var role = descriptor.Kind == ClientRecoveryTargetKind.Visiting
                ? ClientVisitRole.Visitor
                : ClientVisitRole.Owner;
            var visit = await _gameplayChannel.GetVisitSnapshotAsync(
                role,
                cancellationToken);
            if (!visit.IsSuccess || visit.Value == null ||
                !IsApplySuccess(_visitSessionService.ApplySnapshot(visit.Value, role)))
            {
                return _failureMapper.MapRecoveryGameplay(visit);
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
                : await _reconnectWorldTargetFlow.ExecuteAsync(
                    this,
                    intent,
                    currentDescriptor,
                    cancellationToken);
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
            WorldTargetIntentLease intent,
            ClientRecoveryTargetDescriptor descriptor,
            CancellationToken cancellationToken)
        {
            var recovered = await _enterOwnWorldFlow.ExecuteAsync(
                this,
                intent,
                cancellationToken,
                descriptor);
            if (!recovered)
            {
                return _failureMapper.MapRecoveryFlow(Snapshot.Failure);
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
            out WorldTargetIntentLease intent)
        {
            intent = null;
            ClientWorldFlowSnapshot committed;
            lock (_sync)
            {
                if (!TryCaptureSessionGeneration(out var sessionGeneration) ||
                    !_stateMachine.TryBegin(
                        _snapshot,
                        _intentActive,
                        required,
                        next,
                        sessionGeneration,
                        out intent))
                {
                    return false;
                }

                _intentActive = true;
                SetSnapshotLocked(
                    next,
                    intent.TargetGeneration,
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
            WorldTargetIntentLease intent,
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
        private void FinishJoinFailure(WorldTargetIntentLease intent, ClientWorldFlowFailure failure)
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
        private void BeginReturningAfterJoinFailure(WorldTargetIntentLease intent, ClientWorldFlowFailure failure)
        {
            _visitSessionService.ClearTargetRole();
            FinishFailure(intent, ClientWorldFlowState.ReturningOwnWorld, failure, null);
        }

        /// <summary>提交 own-world 解析失败；return flow 保持 ReturningOwnWorld。</summary>
        /// <param name="intent">Own-world intent。</param>
        /// <param name="failure">低敏失败。</param>
        private void FinishOwnFailure(WorldTargetIntentLease intent, ClientWorldFlowFailure failure)
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
        private void FinishReturnFailure(WorldTargetIntentLease intent, ClientWorldFlowFailure failure)
        {
            FinishFailure(intent, ClientWorldFlowState.ReturningOwnWorld, failure, Snapshot.SafeReturn);
        }

        /// <summary>提交一个仍 current intent 的稳定失败。</summary>
        /// <param name="intent">待提交 intent。</param>
        /// <param name="state">失败后的安全状态。</param>
        /// <param name="failure">低敏失败。</param>
        /// <param name="safeReturn">可选权威返回投影。</param>
        private void FinishFailure(
            WorldTargetIntentLease intent,
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
        private void OnGameplayUnexpectedDisconnect(ClientGameplayHealthSnapshot channel)
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
                    channel.DisconnectKind == ClientGameplayDisconnectKind.Protocol
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
                    new WorldTargetIntentLease(flow.TargetGeneration, session.Generation),
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
                    await _enterOwnWorldFlow.ExecuteAsync(
                        this,
                        intent,
                        linked.Token);
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
            WorldTargetIntentLease intent,
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
            WorldTargetIntentLease intent,
            ClientGatewayResult<ClientWorldAdmissionLease> result,
            CancellationToken cancellationToken)
        {
            if (result?.ServerError != null &&
                IsAuthoritativeRecoveryUnavailable(result.ServerError.Code))
            {
                return await ReturnOwnAfterRecoveryRejectionAsync(intent, cancellationToken);
            }

            var mapped = _failureMapper.MapRecoveryGateway(result);
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
            WorldTargetIntentLease intent,
            ClientGameplayResult<ClientVisitSessionProjection> result,
            CancellationToken cancellationToken)
        {
            await CloseFailedConnectionAsync();
            if (result?.ServerError != null &&
                IsAuthoritativeRecoveryUnavailable(result.ServerError.Code))
            {
                return await ReturnOwnAfterRecoveryRejectionAsync(intent, cancellationToken);
            }

            var mapped = _failureMapper.MapRecoveryGameplay(result);
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
            WorldTargetIntentLease intent,
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
                return _failureMapper.MapRecoveryFlow(Snapshot.Failure);
            }

            return ClientConnectionRecoveryResultKind.ReturningOwnWorld;
        }

        /// <summary>识别明确证明旧VisitSession或其world assignment不可恢复的服务端错误。</summary>
        private static bool IsAuthoritativeRecoveryUnavailable(long code)
        {
            return code == 2002 || code == 2100 || code == 2104 || code == 2107 ||
                   code == 2108 || code == 2109;
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

        /// <inheritdoc />
        CancellationTokenSource IWorldAdmissionFlowCommitPort.LinkFlow(
            CancellationToken caller)
        {
            return Link(caller);
        }

        /// <inheritdoc />
        bool IWorldAdmissionFlowCommitPort.IsFlowCurrent(
            WorldTargetIntentLease intent)
        {
            return IsCurrent(intent);
        }

        /// <inheritdoc />
        bool IWorldAdmissionFlowCommitPort.IsFlowCurrentAfterAuthorizedOperation(
            WorldTargetIntentLease intent,
            bool operationSucceeded)
        {
            return IsCurrentAfterAuthorizedOperation(intent, operationSucceeded);
        }

        /// <inheritdoc />
        void IWorldAdmissionFlowCommitPort.FinishOwnFlowFailure(
            WorldTargetIntentLease intent,
            ClientWorldFlowFailure failure)
        {
            FinishOwnFailure(intent, failure);
        }

        /// <inheritdoc />
        void IWorldAdmissionFlowCommitPort.FinishJoinFlowFailure(
            WorldTargetIntentLease intent,
            ClientWorldFlowFailure failure)
        {
            FinishJoinFailure(intent, failure);
        }

        /// <inheritdoc />
        void IWorldAdmissionFlowCommitPort.BeginReturningFlow(
            WorldTargetIntentLease intent,
            ClientWorldFlowFailure failure)
        {
            BeginReturningAfterJoinFailure(intent, failure);
        }

        /// <inheritdoc />
        void IWorldAdmissionFlowCommitPort.FinishReturnFlowFailure(
            WorldTargetIntentLease intent,
            ClientWorldFlowFailure failure)
        {
            FinishReturnFailure(intent, failure);
        }

        /// <inheritdoc />
        bool IWorldAdmissionFlowCommitPort.FinishFlowSuccess(
            WorldTargetIntentLease intent,
            ClientWorldFlowState state,
            string visitSessionID)
        {
            return FinishSuccess(intent, state, visitSessionID, null);
        }

        /// <inheritdoc />
        Task IWorldAdmissionFlowCommitPort.CloseFailedFlowConnectionAsync()
        {
            return CloseFailedConnectionAsync();
        }

        /// <inheritdoc />
        void IWorldAdmissionFlowCommitPort.FinishRecoveryFlowFailure(
            WorldTargetIntentLease intent,
            ClientWorldFlowState state,
            ClientWorldFlowFailure failure)
        {
            FinishFailure(intent, state, failure, null);
        }

        /// <inheritdoc />
        bool IWorldAdmissionFlowCommitPort.ValidateRecoveredProjection(
            ClientRecoveryTargetDescriptor descriptor)
        {
            return ValidateRecoveredProjection(descriptor);
        }

        /// <inheritdoc />
        Task<ClientConnectionRecoveryResultKind>
            IWorldAdmissionFlowCommitPort.HandleRecoveryAdmissionFailureAsync(
                WorldTargetIntentLease intent,
                ClientGatewayResult<ClientWorldAdmissionLease> result,
                CancellationToken cancellationToken)
        {
            return FinishRecoveryAdmissionFailureAsync(intent, result, cancellationToken);
        }

        /// <inheritdoc />
        Task<ClientConnectionRecoveryResultKind>
            IWorldAdmissionFlowCommitPort.HandleReconnectFailureAsync(
                WorldTargetIntentLease intent,
                ClientGameplayResult<ClientVisitSessionProjection> result,
                CancellationToken cancellationToken)
        {
            return FinishReconnectFailureAsync(intent, result, cancellationToken);
        }

        /// <inheritdoc />
        Task<ClientConnectionRecoveryResultKind>
            IWorldAdmissionFlowCommitPort.ReturnOwnAfterRecoveryRejectionAsync(
                WorldTargetIntentLease intent,
                CancellationToken cancellationToken)
        {
            return ReturnOwnAfterRecoveryRejectionAsync(intent, cancellationToken);
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
        private bool IsCurrent(WorldTargetIntentLease intent)
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
            WorldTargetIntentLease intent,
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
        private bool IsCurrentLocked(WorldTargetIntentLease intent)
        {
            return TryCaptureSessionGeneration(out var generation) &&
                   _stateMachine.CanCommit(
                       _snapshot,
                       _intentActive,
                       intent,
                       generation);
        }

        /// <summary>判断 projection gate 是否允许 flow 继续。</summary>
        /// <param name="result">Projection apply result。</param>
        /// <returns>Applied 或 Duplicate 时返回 true。</returns>
        private static bool IsApplySuccess(ClientProjectionApplyResult result)
        {
            return result == ClientProjectionApplyResult.Applied ||
                   result == ClientProjectionApplyResult.Duplicate;
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

    }
}
