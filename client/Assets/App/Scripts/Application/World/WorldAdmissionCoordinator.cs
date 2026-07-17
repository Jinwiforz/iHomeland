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
    internal sealed class WorldAdmissionCoordinator : IAppLifetimeParticipant
    {
        /// <summary>保护状态、单一 intent 与 generation。</summary>
        private readonly object _sync = new object();

        /// <summary>提供 current session generation 与 HTTP capability。</summary>
        private readonly SessionCoordinator _sessionCoordinator;

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

        /// <summary>创建不自动联网的 world target coordinator。</summary>
        /// <param name="sessionCoordinator">唯一 Session owner。</param>
        /// <param name="gameplayChannel">唯一 gameplay owner。</param>
        /// <param name="personalWorldService">PersonalWorld projection owner。</param>
        /// <param name="visitSessionService">VisitSession projection owner。</param>
        internal WorldAdmissionCoordinator(
            SessionCoordinator sessionCoordinator,
            ClientGameplayChannel gameplayChannel,
            PersonalWorldService personalWorldService,
            VisitSessionService visitSessionService)
        {
            _sessionCoordinator = sessionCoordinator ?? throw new ArgumentNullException(nameof(sessionCoordinator));
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
                _subscribed = true;
            }

            return Task.CompletedTask;
        }

        /// <summary>显式解析并进入 current actor 自己的 PersonalWorld。</summary>
        /// <param name="cancellationToken">取消 caller 等待。</param>
        /// <returns>完整 target 已提交为 OwnWorld 时返回 true。</returns>
        internal async Task<bool> EnterOwnWorldAsync(CancellationToken cancellationToken)
        {
            if (!TryBegin(
                    ClientWorldFlowState.Inactive,
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
                return false;
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
                if (!IsCurrent(intent) || !accept.IsSuccess)
                {
                    FinishJoinFailure(intent, MapAcceptFailure(accept));
                    return false;
                }

                var reservation = accept.Value;
                if (!string.Equals(reservation.VisitSessionID, invite.VisitSessionID, StringComparison.Ordinal) ||
                    reservation.Revision <= checked((long)invite.CreatedRevision))
                {
                    FinishJoinFailure(intent, ClientWorldFlowFailure.Protocol);
                    return false;
                }

                var admission = await _sessionCoordinator.IssueWorldAdmissionAsync(
                    ClientWorldAdmissionTarget.VisitWorld(reservation.VisitSessionID),
                    admissionKey,
                    linked.Token);
                if (!IsCurrent(intent) || !admission.IsSuccess ||
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
                    checked((ulong)reservation.Revision),
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
                    !currentWorld.Assignment.IsEquivalent(currentVisit.Assignment))
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
                    _subscribed = false;
                }

                lifetime = _lifetimeCancellation;
                _lifetimeCancellation = null;
                _intentActive = false;
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
        /// <returns>进入 OwnWorld 时返回 true。</returns>
        private async Task<bool> ResolveOwnWorldAsync(
            WorldFlowIntent intent,
            CancellationToken cancellationToken)
        {
            using (var linked = Link(cancellationToken))
            {
                var bootstrap = await _sessionCoordinator.GetWorldBootstrapAsync(linked.Token);
                if (!IsCurrent(intent) || !bootstrap.IsSuccess ||
                    !IsApplySuccess(_personalWorldService.ApplyBootstrap(bootstrap.Value)))
                {
                    FinishOwnFailure(intent, MapHttpFailure(bootstrap));
                    return false;
                }

                var admission = await _sessionCoordinator.IssueWorldAdmissionAsync(
                    ClientWorldAdmissionTarget.OwnWorld(),
                    NewIdempotencyKey(),
                    linked.Token);
                if (!IsCurrent(intent) || !admission.IsSuccess ||
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
                    FinishOwnFailure(intent, MapGameplayFailure(world));
                    return false;
                }

                return FinishSuccess(intent, ClientWorldFlowState.OwnWorld, null, null);
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
            ClientWorldFlowSnapshot committed;
            lock (_sync)
            {
                if (!IsCurrentLocked(intent))
                {
                    return false;
                }

                _intentActive = false;
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
            var state = Snapshot.State == ClientWorldFlowState.ResolvingOwnWorld
                ? ClientWorldFlowState.Inactive
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
            var flow = Snapshot;
            if (flow.State == ClientWorldFlowState.Visiting &&
                snapshot.Current != null && snapshot.Current.Lifecycle == ClientVisitLifecycle.Closed)
            {
                await StartAuthoritativeReturnAsync(flow.VisitSessionID, null);
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
            internal long SessionGeneration { get; }
        }
    }
}
