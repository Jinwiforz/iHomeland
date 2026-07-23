using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Bootstrap;
using IHomeland.Client.Application.Control;
using IHomeland.Client.Application.Gameplay;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Application.World;
using IHomeland.Client.Application.Ports;
using IHomeland.Client.Foundation.Lifetime;
using IHomeland.Client.Foundation.Threading;
using IHomeland.Client.Application.Contracts;
using IHomeland.Client.Presentation.Navigation;

namespace IHomeland.Client.Presentation.PersonalWorld
{
    /// <summary>
    /// 协调个人世界首个产品竖切的纯页面意图、表现代际和低敏 View State。
    /// </summary>
    /// <remarks>
    /// Session、PersonalWorld、VisitSession 与 world target 最终事实仍由既有 owner 保存；
    /// 本类型不缓存 token、ticket、admission、endpoint 或 generated message，也不决定权威角色。
    /// </remarks>
    internal sealed class ClientPersonalWorldExperience :
        IAppLifetimeParticipant,
        IClientLoginActions,
        IClientShellActions,
        IClientWorldVisitActions,
        IClientWorldHudActions,
        IClientUiProductContext,
        IPersonalWorldSceneTransactionHost
    {
        /// <summary>首期定向邀请有效期，单位为毫秒，并与服务端 v1 policy 上限保持一致。</summary>
        internal const uint InviteLifetimeMilliseconds = 30 * 60 * 1000;

        /// <summary>限制 control ready、own-world、Scene 与 route 共同组成的显式恢复事务。</summary>
        internal static readonly TimeSpan DefaultConnectionRecoveryTimeout = TimeSpan.FromSeconds(45);

        /// <summary>保护表现代际、活动 intent 与 subscriber 快照。</summary>
        private readonly object _sync = new object();

        /// <summary>串行化最高 authority 的 Session invalidation 到 Login。</summary>
        private readonly SessionInvalidationPresentationTransaction
            _sessionInvalidationTransaction =
                new SessionInvalidationPresentationTransaction();

        /// <summary>串行化 recovery snapshot 到 Scene/HUD/ConnectionLost。</summary>
        private readonly RecoveryPresentationTransaction
            _recoveryPresentationTransaction =
                new RecoveryPresentationTransaction();

        /// <summary>执行显式 version/config bootstrap。</summary>
        private readonly ClientBootstrapService _bootstrapService;

        /// <summary>保存唯一认证事实并执行认证动作。</summary>
        private readonly SessionCoordinator _sessionCoordinator;

        /// <summary>保存production启动restore终态；isolated fixture为空。</summary>
        private readonly ClientSessionRestoreCoordinator _sessionRestoreCoordinator;

        /// <summary>保存 automatic/manual 通道恢复的唯一 intent owner；产品与测试必须同构。</summary>
        private readonly ClientConnectionRecoveryCoordinator _connectionRecoveryCoordinator;

        /// <summary>把channel与恢复owner的后台通知收敛到唯一Unity主线程。</summary>
        private readonly IClientMainThreadDispatcher _mainThreadDispatcher;

        /// <summary>运行唯一 WSS control connection。</summary>
        private readonly IClientControlChannelPort _controlChannel;

        /// <summary>提供 PersonalWorld 权威投影。</summary>
        private readonly PersonalWorldService _personalWorldService;

        /// <summary>提供 VisitSession 权威投影与 Owner/Visitor command。</summary>
        private readonly VisitSessionService _visitSessionService;

        /// <summary>拥有 own/visit/return target 转换。</summary>
        private readonly WorldAdmissionCoordinator _worldAdmissionCoordinator;

        /// <summary>拥有全部产品 route 的唯一导航状态。</summary>
        private readonly ClientUiRouter _uiRouter;

        /// <summary>拥有登记内容 Scene、Context 与 Scene generation 的唯一转换边界。</summary>
        private readonly IClientWorldSceneTransition _sceneTransition;

        /// <summary>串行执行 Scene invalidation、load/unload 与 Shell/HUD commit。</summary>
        private readonly PersonalWorldSceneTransaction _sceneTransaction;

        /// <summary>纯派生 Login/Shell/WorldVisit/HUD View State。</summary>
        private readonly PersonalWorldViewStateProjector _viewStateProjector =
            new PersonalWorldViewStateProjector();

        /// <summary>限制一次显式恢复事务，不用于延时重试或 tick 修正。</summary>
        private readonly TimeSpan _connectionRecoveryTimeout;

        /// <summary>取消全部页面 intent 与 control run observation。</summary>
        private CancellationTokenSource _lifetimeCancellation;

        /// <summary>唯一拥有 presentation generation、intent、failure 与 View State commit。</summary>
        private readonly PersonalWorldPresentationState _presentationState =
            new PersonalWorldPresentationState();

        /// <summary>标识权威 snapshot subscriber 是否已经连接。</summary>
        private bool _subscribed;

        /// <summary>合并后台恢复通知，保证队列中最多只有一个表现刷新callback。</summary>
        private int _recoveryDispatchPending;

        /// <summary>保存锁内复制、锁外调用的页面状态 subscriber。</summary>
        private Action<ClientPersonalWorldViewState> _viewStateChanged;

        /// <summary>
        /// 创建显式连接既有 owner 的唯一 Experience。
        /// </summary>
        /// <param name="bootstrapService">认证前 bootstrap 用例。</param>
        /// <param name="sessionCoordinator">唯一 Session owner。</param>
        /// <param name="sessionRestoreCoordinator">Production启动restore owner；isolated fixture为空。</param>
        /// <param name="connectionRecoveryCoordinator">唯一连接恢复 owner；产品与测试使用同一状态机。</param>
        /// <param name="mainThreadDispatcher">唯一Unity主线程投递owner。</param>
        /// <param name="controlChannel">唯一 control channel owner。</param>
        /// <param name="personalWorldService">PersonalWorld projection owner。</param>
        /// <param name="visitSessionService">VisitSession projection owner。</param>
        /// <param name="worldAdmissionCoordinator">World target flow owner。</param>
        /// <param name="uiRouter">唯一产品 route owner。</param>
        /// <param name="sceneTransition">唯一登记内容场景转换边界。</param>
        /// <param name="connectionRecoveryTimeout">整笔显式恢复事务 deadline。</param>
        /// <exception cref="ArgumentNullException">任一依赖为空时抛出。</exception>
        /// <exception cref="ArgumentOutOfRangeException">恢复 deadline 非正数时抛出。</exception>
        internal ClientPersonalWorldExperience(
            ClientBootstrapService bootstrapService,
            SessionCoordinator sessionCoordinator,
            ClientSessionRestoreCoordinator sessionRestoreCoordinator,
            ClientConnectionRecoveryCoordinator connectionRecoveryCoordinator,
            IClientMainThreadDispatcher mainThreadDispatcher,
            IClientControlChannelPort controlChannel,
            PersonalWorldService personalWorldService,
            VisitSessionService visitSessionService,
            WorldAdmissionCoordinator worldAdmissionCoordinator,
            ClientUiRouter uiRouter,
            IClientWorldSceneTransition sceneTransition,
            TimeSpan connectionRecoveryTimeout)
        {
            _bootstrapService = bootstrapService ?? throw new ArgumentNullException(nameof(bootstrapService));
            _sessionCoordinator = sessionCoordinator ?? throw new ArgumentNullException(nameof(sessionCoordinator));
            _sessionRestoreCoordinator = sessionRestoreCoordinator;
            _connectionRecoveryCoordinator = connectionRecoveryCoordinator ??
                throw new ArgumentNullException(nameof(connectionRecoveryCoordinator));
            _mainThreadDispatcher = mainThreadDispatcher ??
                throw new ArgumentNullException(nameof(mainThreadDispatcher));
            _controlChannel = controlChannel ?? throw new ArgumentNullException(nameof(controlChannel));
            _personalWorldService = personalWorldService ?? throw new ArgumentNullException(nameof(personalWorldService));
            _visitSessionService = visitSessionService ?? throw new ArgumentNullException(nameof(visitSessionService));
            _worldAdmissionCoordinator = worldAdmissionCoordinator ?? throw new ArgumentNullException(nameof(worldAdmissionCoordinator));
            _uiRouter = uiRouter ?? throw new ArgumentNullException(nameof(uiRouter));
            _sceneTransition = sceneTransition ?? throw new ArgumentNullException(nameof(sceneTransition));
            _sceneTransaction = new PersonalWorldSceneTransaction(
                _uiRouter,
                _sceneTransition);
            if (connectionRecoveryTimeout <= TimeSpan.Zero)
            {
                throw new ArgumentOutOfRangeException(nameof(connectionRecoveryTimeout));
            }

            _connectionRecoveryTimeout = connectionRecoveryTimeout;
        }

        /// <summary>
        /// 在新不可变 View State 提交后通知页面；单个 subscriber 异常不会阻断其他页面。
        /// </summary>
        internal event Action<ClientPersonalWorldViewState> ViewStateChanged
        {
            add
            {
                lock (_sync)
                {
                    if (!_presentationState.Running)
                    {
                        throw new InvalidOperationException("未运行的 Experience 不能登记页面 subscriber。");
                    }

                    _viewStateChanged += value;
                }
            }

            remove
            {
                lock (_sync)
                {
                    _viewStateChanged -= value;
                }
            }
        }

        /// <summary>获取最近一次原子提交的不可变低敏页面状态。</summary>
        internal ClientPersonalWorldViewState ViewState
        {
            get
            {
                lock (_sync)
                {
                    return _presentationState.ViewState;
                }
            }
        }

        /// <summary>
        /// 映射一次性restore结果；冷启动打开Login，成功restore则直接建立control并进入OwnWorld。
        /// </summary>
        /// <param name="cancellationToken">取消 Experience 初始化等待。</param>
        /// <returns>本地登录入口已经提交时完成。</returns>
        /// <exception cref="InvalidOperationException">重复初始化或停止后重启时抛出。</exception>
        public async Task InitializeAsync(CancellationToken cancellationToken)
        {
            lock (_sync)
            {
                if (_presentationState.Running || _lifetimeCancellation != null)
                {
                    throw new InvalidOperationException("ClientPersonalWorldExperience 不能重复初始化或停止后重启。");
                }

                cancellationToken.ThrowIfCancellationRequested();
                _lifetimeCancellation = new CancellationTokenSource();
                _presentationState.Running = true;
                _presentationState.PresentationGeneration++;
                _presentationState.RestoringSession = _sessionRestoreCoordinator != null;
                SubscribeLocked();
                RebuildViewStateLocked();
            }

            PublishCurrent();
            var restore = _sessionRestoreCoordinator?.Result;
            if (restore != null && restore.Outcome == ClientSessionRestoreOutcome.Restored)
            {
                await CompleteStartupRestoreAsync(cancellationToken);
                return;
            }

            lock (_sync)
            {
                _presentationState.RestoringSession = false;
                _presentationState.Failure =
                    PersonalWorldFailureMapper.FromRestore(restore?.Outcome);
                _presentationState.FailureIntent = ClientPersonalWorldIntent.None;
                RebuildViewStateLocked();
            }

            var opened = await _uiRouter.OpenAsync(ClientUiRouteId.Login, 0, cancellationToken);
            if (!opened.Committed && !opened.IsSuccess)
            {
                throw new InvalidOperationException($"Login route 初始化失败：{opened.Code}。");
            }

            PublishCurrent();
        }

        /// <summary>在不重复bootstrap或refresh的前提下完成已恢复Session的产品启动。</summary>
        /// <param name="cancellationToken">AppLifetime初始化取消信号。</param>
        /// <returns>OwnWorld与Scene已提交，或稳定失败界面已提交时完成。</returns>
        private async Task CompleteStartupRestoreAsync(CancellationToken cancellationToken)
        {
            long generation;
            CancellationToken lifetime;
            lock (_sync)
            {
                if (!_presentationState.Running || !_sessionCoordinator.TryGetCurrent(out _))
                {
                    throw new InvalidOperationException("Restored结果缺少current Session。");
                }

                _presentationState.RestoringSession = false;
                _presentationState.Failure = ClientPersonalWorldFailure.None;
                _presentationState.FailureIntent = ClientPersonalWorldIntent.None;
                generation = _presentationState.PresentationGeneration;
                lifetime = _lifetimeCancellation.Token;
                RebuildViewStateLocked();
            }

            var opened = await _uiRouter.OpenAsync(ClientUiRouteId.Shell, 0, cancellationToken);
            if (!opened.Committed && !opened.IsSuccess)
            {
                throw new InvalidOperationException($"Shell route初始化失败：{opened.Code}。");
            }

            PublishCurrent();
            using (var deadline = new CancellationTokenSource(_connectionRecoveryTimeout))
            using (var linked = CancellationTokenSource.CreateLinkedTokenSource(
                       cancellationToken,
                       lifetime,
                       deadline.Token))
            {
                try
                {
                    var controlRun = _controlChannel.RunAsync(lifetime);
                    _ = ObserveControlRunAsync(controlRun, generation, cancellationOwner: null);
                    if (!await _controlChannel.WaitUntilConnectedAsync(linked.Token))
                    {
                        await EnterConnectionLostAsync(generation);
                        return;
                    }

                    var entered = await _worldAdmissionCoordinator.EnterOwnWorldAsync(linked.Token);
                    if (entered && await _sceneTransaction.SynchronizeAsync(this, generation, linked.Token))
                    {
                        return;
                    }

                    var failure = entered
                        ? ClientPersonalWorldFailure.DependencyUnavailable
                        : PersonalWorldFailureMapper.FromWorld(
                            _worldAdmissionCoordinator.Snapshot.Failure);
                    if (failure == ClientPersonalWorldFailure.Transport)
                    {
                        await EnterConnectionLostAsync(generation);
                        return;
                    }

                    SetFailure(failure);
                }
                catch (OperationCanceledException)
                {
                    if (!cancellationToken.IsCancellationRequested &&
                        !lifetime.IsCancellationRequested)
                    {
                        await EnterConnectionLostAsync(generation);
                    }
                    else
                    {
                        throw;
                    }
                }
                catch (Exception)
                {
                    await EnterConnectionLostAsync(generation);
                }
            }
        }

        /// <summary>
        /// 创建账号并按 bootstrap、Session、control run、own-world 的固定顺序进入产品流程。
        /// </summary>
        /// <param name="username">待服务端校验的账号名。</param>
        /// <param name="password">仅当前调用参数临时持有的原始 password。</param>
        /// <param name="displayName">待服务端规范化的显示名。</param>
        /// <param name="cancellationToken">页面隐藏或调用方取消等待的信号。</param>
        /// <returns>不包含 credential 的稳定结果。</returns>
        public Task<ClientPersonalWorldActionResult> RegisterAsync(
            string username,
            string password,
            string displayName,
            CancellationToken cancellationToken)
        {
            return AuthenticateAsync(
                ClientPersonalWorldIntent.Register,
                token => _sessionCoordinator.RegisterAsync(username, password, displayName, token),
                cancellationToken);
        }

        /// <summary>
        /// 登录并按 bootstrap、Session、control run、own-world 的固定顺序进入产品流程。
        /// </summary>
        /// <param name="username">待服务端校验的账号名。</param>
        /// <param name="password">仅当前调用参数临时持有的原始 password。</param>
        /// <param name="cancellationToken">页面隐藏或调用方取消等待的信号。</param>
        /// <returns>不包含 credential 的稳定结果。</returns>
        public Task<ClientPersonalWorldActionResult> LoginAsync(
            string username,
            string password,
            CancellationToken cancellationToken)
        {
            return AuthenticateAsync(
                ClientPersonalWorldIntent.Login,
                token => _sessionCoordinator.LoginAsync(username, password, token),
                cancellationToken);
        }

        /// <summary>重试进入自己的 PersonalWorld。</summary>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        public Task<ClientPersonalWorldActionResult> RetryEnterOwnWorldAsync(CancellationToken cancellationToken)
        {
            return RunWorldActionAsync(
                ClientPersonalWorldIntent.EnterOwnWorld,
                _worldAdmissionCoordinator.EnterOwnWorldAsync,
                cancellationToken);
        }

        /// <summary>重试权威安全返回。</summary>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        public Task<ClientPersonalWorldActionResult> RetryReturnAsync(CancellationToken cancellationToken)
        {
            return RunWorldActionAsync(
                ClientPersonalWorldIntent.RetryReturn,
                _worldAdmissionCoordinator.RetryReturnAsync,
                cancellationToken);
        }

        /// <summary>显式重建 control、own-world target 与 Scene/HUD，并在全部提交后关闭断开提示。</summary>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>连接重新进入 Connected 或稳定失败时的低敏结果。</returns>
        public Task<ClientPersonalWorldActionResult> RetryConnectionAsync(
            CancellationToken cancellationToken)
        {
            return RetryConnectionCoordinatedAsync(cancellationToken);
        }

        /// <summary>通过唯一恢复owner执行manual single-flight并等待Scene第四重gate。</summary>
        private async Task<ClientPersonalWorldActionResult> RetryConnectionCoordinatedAsync(
            CancellationToken cancellationToken)
        {
            if (!TryBeginIntent(ClientPersonalWorldIntent.Reconnect, out var generation, out var token))
            {
                return RejectBusyOrStopped();
            }

            using (var linked = CancellationTokenSource.CreateLinkedTokenSource(token, cancellationToken))
            {
                try
                {
                    if (!_connectionRecoveryCoordinator.BeginManualRecovery())
                    {
                        return FinishIntent(generation, ClientPersonalWorldFailure.Transport);
                    }

                    var intentGeneration = _connectionRecoveryCoordinator.Snapshot.IntentGeneration;
                    var settled = await _connectionRecoveryCoordinator.WaitForSettledAsync(
                        intentGeneration,
                        linked.Token);
                    if (settled.Phase == ClientConnectionRecoveryPhase.AwaitingSceneCommit)
                    {
                        var committed = await CommitRecoverySceneAsync(
                            settled,
                            generation,
                            linked.Token);
                        return FinishIntent(
                            generation,
                            committed
                                ? ClientPersonalWorldFailure.None
                                : ClientPersonalWorldFailure.DependencyUnavailable);
                    }

                    if (settled.Phase == ClientConnectionRecoveryPhase.Idle)
                    {
                        // 没有冻结target表示断开发生在首次OwnWorld提交前；恢复control后走正常OwnWorld入口。
                        var flow = _worldAdmissionCoordinator.Snapshot;
                        if (flow.State != ClientWorldFlowState.OwnWorld &&
                            flow.State != ClientWorldFlowState.Visiting)
                        {
                            var entered = await _worldAdmissionCoordinator.EnterOwnWorldAsync(linked.Token);
                            if (!entered || !await _sceneTransaction.SynchronizeAsync(this, generation, linked.Token))
                            {
                                return FinishIntent(
                                    generation,
                                    entered
                                        ? ClientPersonalWorldFailure.DependencyUnavailable
                                        : PersonalWorldFailureMapper.FromWorld(
                                            _worldAdmissionCoordinator.Snapshot.Failure));
                            }
                        }

                        lock (_sync)
                        {
                            if (_presentationState.Running && generation == _presentationState.PresentationGeneration)
                            {
                                _presentationState.ConnectionLost = false;
                            }
                        }

                        await CloseRouteAsync(ClientUiRouteId.ConnectionLost, token);
                        return FinishIntent(generation, ClientPersonalWorldFailure.None);
                    }

                    return FinishIntent(
                        generation,
                        PersonalWorldFailureMapper.FromRecovery(settled.Result));
                }
                catch (OperationCanceledException)
                {
                    return FinishCancellation(generation, cancellationToken);
                }
                catch (Exception)
                {
                    return FinishIntent(generation, ClientPersonalWorldFailure.Internal);
                }
            }
        }

        /// <summary>退出并回到只具备本地副作用的 Login route。</summary>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        public async Task<ClientPersonalWorldActionResult> LogoutAsync(CancellationToken cancellationToken)
        {
            if (!TryBeginIntent(ClientPersonalWorldIntent.Logout, out var generation, out var token))
            {
                return RejectBusyOrStopped();
            }

            using (var linked = CancellationTokenSource.CreateLinkedTokenSource(token, cancellationToken))
            {
                try
                {
                    var result = await _sessionCoordinator.LogoutAsync(linked.Token);
                    if (!IsCurrent(generation))
                    {
                        // 服务端可先通过 control push 使 Session 失效，ObserveControlRunAsync 随即推进
                        // presentation generation 并返回 Login。只要 Experience 仍在运行且唯一 Session
                        // 已清除，旧 logout intent 的产品目标已经达成，不应把正常竞态误报为 Stopped。
                        if (IsRunning() && !_sessionCoordinator.TryGetCurrent(out _))
                        {
                            return ClientPersonalWorldActionResult.Success();
                        }

                        return FinishIntent(generation, ClientPersonalWorldFailure.Stopped);
                    }

                    if (!result.IsSuccess)
                    {
                        return FinishIntent(
                            generation,
                            PersonalWorldFailureMapper.FromGateway(result));
                    }

                    // Session 已清理后必须完成本地回收；页面 token 不再有权中断该阶段。
                    await ReturnToLoginAsync(generation);
                    // ReturnToLoginAsync 会推进 presentation generation 并终结当前 intent；
                    // 此处不能再用旧代际调用 FinishIntent，否则成功退出会被误报为 Stopped。
                    return ClientPersonalWorldActionResult.Success();
                }
                catch (OperationCanceledException)
                {
                    return FinishCancellation(generation, cancellationToken);
                }
                catch (Exception)
                {
                    return FinishIntent(generation, ClientPersonalWorldFailure.Internal);
                }
            }
        }

        /// <summary>打开 Owner VisitSession。</summary>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        public Task<ClientPersonalWorldActionResult> OpenVisitAsync(CancellationToken cancellationToken)
        {
            return RunGameplayActionAsync(
                ClientPersonalWorldIntent.OpenVisit,
                _visitSessionService.OpenAsync,
                cancellationToken);
        }

        /// <summary>为指定玩家创建定向邀请。</summary>
        /// <param name="targetVisitorID">目标 Visitor 玩家标识。</param>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        public Task<ClientPersonalWorldActionResult> CreateInviteAsync(
            string targetVisitorID,
            CancellationToken cancellationToken)
        {
            return RunGameplayActionAsync(
                ClientPersonalWorldIntent.CreateInvite,
                token => _visitSessionService.CreateInviteAsync(
                    targetVisitorID,
                    InviteLifetimeMilliseconds,
                    token),
                cancellationToken);
        }

        /// <summary>撤销指定邀请。</summary>
        /// <param name="inviteID">待撤销邀请标识。</param>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        public Task<ClientPersonalWorldActionResult> RevokeInviteAsync(
            string inviteID,
            CancellationToken cancellationToken)
        {
            return RunGameplayActionAsync(
                ClientPersonalWorldIntent.RevokeInvite,
                token => _visitSessionService.RevokeInviteAsync(inviteID, token),
                cancellationToken);
        }

        /// <summary>接受指定邀请并进入 Visitor target。</summary>
        /// <param name="visitSessionID">邀请所属访问会话标识。</param>
        /// <param name="inviteID">待接受邀请标识。</param>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        public Task<ClientPersonalWorldActionResult> AcceptInviteAsync(
            string visitSessionID,
            string inviteID,
            CancellationToken cancellationToken)
        {
            return RunWorldActionAsync(
                ClientPersonalWorldIntent.AcceptInvite,
                token => _worldAdmissionCoordinator.JoinVisitAsync(visitSessionID, inviteID, token),
                cancellationToken);
        }

        /// <summary>Owner 移除指定 Visitor。</summary>
        /// <param name="visitorPlayerID">待移除 Visitor 玩家标识。</param>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        public Task<ClientPersonalWorldActionResult> KickVisitorAsync(
            string visitorPlayerID,
            CancellationToken cancellationToken)
        {
            return RunGameplayActionAsync(
                ClientPersonalWorldIntent.KickVisitor,
                token => _visitSessionService.KickAsync(visitorPlayerID, token),
                cancellationToken);
        }

        /// <summary>Owner 关闭当前 VisitSession。</summary>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        public async Task<ClientPersonalWorldActionResult> CloseVisitAsync(CancellationToken cancellationToken)
        {
            var result = await RunGameplayActionAsync(
                ClientPersonalWorldIntent.CloseVisit,
                _visitSessionService.CloseAsync,
                cancellationToken);
            if (result.Succeeded)
            {
                // 关闭当前 route 会取消其 binding token；清理不能再由该 token 自我取消。
                await CloseRouteAsync(ClientUiRouteId.WorldVisit, CancellationToken.None);
            }

            return result;
        }

        /// <summary>Visitor 主动离开当前 target 并按权威路径返回自己的世界。</summary>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        public Task<ClientPersonalWorldActionResult> LeaveVisitAsync(CancellationToken cancellationToken)
        {
            return RunWorldActionAsync(
                ClientPersonalWorldIntent.LeaveVisit,
                _worldAdmissionCoordinator.LeaveVisitAsync,
                cancellationToken);
        }

        /// <summary>请求打开 VisitSession 产品页。</summary>
        /// <param name="cancellationToken">Scene generation 失效或调用方取消等待的信号。</param>
        /// <returns>稳定低敏结果。</returns>
        public async Task<ClientPersonalWorldActionResult> ShowWorldVisitAsync(CancellationToken cancellationToken)
        {
            ClientUiDiagnostics.Trace(
                nameof(ClientPersonalWorldExperience),
                "world_visit_show_requested",
                $"running={IsRunning()} phase={ViewState.Phase}");
            if (!IsRunning())
            {
                return ClientPersonalWorldActionResult.Failed(ClientPersonalWorldFailure.Stopped);
            }

            try
            {
                var result = await _uiRouter.OpenAsync(ClientUiRouteId.WorldVisit, 0, cancellationToken);
                ClientUiDiagnostics.Trace(
                    nameof(ClientPersonalWorldExperience),
                    "world_visit_show_completed",
                    $"code={result.Code} committed={result.Committed}");
                return result.IsSuccess
                    ? ClientPersonalWorldActionResult.Success()
                    : ClientPersonalWorldActionResult.Failed(
                        PersonalWorldFailureMapper.FromUi(result));
            }
            catch (OperationCanceledException)
            {
                return ClientPersonalWorldActionResult.Failed(
                    cancellationToken.IsCancellationRequested
                        ? ClientPersonalWorldFailure.CallerCancelled
                        : ClientPersonalWorldFailure.Stopped);
            }
            catch (Exception)
            {
                return ClientPersonalWorldActionResult.Failed(ClientPersonalWorldFailure.Internal);
            }
        }

        /// <summary>
        /// 接收 Gameplay 菜单输入并通过既有 Router 打开访问管理页。
        /// </summary>
        /// <remarks>
        /// Router 拥有停止取消与重复 route 收敛；返回的稳定失败不升级为未观察异常。
        /// </remarks>
        internal void RequestWorldVisitFromGameplayMenu()
        {
            ClientUiDiagnostics.Trace(
                nameof(ClientPersonalWorldExperience),
                "gameplay_menu_received",
                $"phase={ViewState.Phase}");
            _ = ObserveUiIntentAsync(ShowWorldVisitAsync(CancellationToken.None));
        }

        /// <summary>
        /// 接收当前 UI route 的标准取消意图；首期只允许关闭非权威事实的访问管理 overlay。
        /// </summary>
        /// <param name="routeId">触发 UI/Cancel 时由唯一输入 owner 提交的当前 route。</param>
        internal void RequestUiCancel(ClientUiRouteId routeId)
        {
            ClientUiDiagnostics.Trace(
                nameof(ClientPersonalWorldExperience),
                "ui_cancel_received",
                $"route={routeId} running={IsRunning()}");
            if (routeId == ClientUiRouteId.WorldVisit && IsRunning())
            {
                _ = ObserveUiIntentAsync(
                    CloseRouteAsync(ClientUiRouteId.WorldVisit, CancellationToken.None));
            }
        }

        /// <summary>
        /// 先撤销 intent/subscriber，再关闭当前产品 routes，阻止迟到回写复活页面。
        /// </summary>
        /// <param name="cancellationToken">AppLifetime 共享停止 deadline。</param>
        /// <returns>Experience 本地所有权已经释放时完成。</returns>
        public async Task StopAsync(CancellationToken cancellationToken)
        {
            CancellationTokenSource lifetime;
            lock (_sync)
            {
                if (!_presentationState.Running)
                {
                    return;
                }

                _presentationState.Running = false;
                _presentationState.PresentationGeneration++;
                _presentationState.ActiveIntent = ClientPersonalWorldIntent.None;
                _presentationState.Failure = ClientPersonalWorldFailure.Stopped;
                _presentationState.FailureIntent = ClientPersonalWorldIntent.None;
                _presentationState.ConnectionLost = false;
                _presentationState.RestoringSession = false;
                _presentationState.PendingSessionInvalidationGeneration = 0;
                UnsubscribeLocked();
                _viewStateChanged = null;
                lifetime = _lifetimeCancellation;
                _presentationState.ViewState = ProjectViewStateLocked(
                    ClientPersonalWorldPhase.Stopped,
                    _connectionRecoveryCoordinator.Snapshot);
            }

            lifetime?.Cancel();
            await CloseRouteAsync(ClientUiRouteId.ConnectionLost, cancellationToken);
            await CloseRouteAsync(ClientUiRouteId.WorldVisit, cancellationToken);
            await CloseRouteAsync(ClientUiRouteId.WorldHud, cancellationToken);
            await CloseRouteAsync(ClientUiRouteId.Shell, cancellationToken);
            await CloseRouteAsync(ClientUiRouteId.Login, cancellationToken);
            lifetime?.Dispose();
        }

        /// <summary>执行认证固定顺序，并以 presentation generation 拒绝迟到结果。</summary>
        /// <param name="intent">Register 或 Login。</param>
        /// <param name="authenticate">只在 bootstrap 成功后调用的 Session action。</param>
        /// <param name="cancellationToken">页面或调用方取消信号。</param>
        /// <returns>稳定低敏结果。</returns>
        private async Task<ClientPersonalWorldActionResult> AuthenticateAsync(
            ClientPersonalWorldIntent intent,
            Func<CancellationToken, Task<ClientGatewayResult<ClientSessionSnapshot>>> authenticate,
            CancellationToken cancellationToken)
        {
            if (!TryBeginIntent(intent, out var generation, out var token))
            {
                return RejectBusyOrStopped();
            }

            using (var linked = CancellationTokenSource.CreateLinkedTokenSource(token, cancellationToken))
            {
                try
                {
                    var bootstrap = await _bootstrapService.BootstrapAsync(linked.Token);
                    if (!IsCurrent(generation))
                    {
                        return FinishIntent(generation, ClientPersonalWorldFailure.Stopped);
                    }

                    if (!bootstrap.IsSuccess)
                    {
                        return FinishIntent(
                            generation,
                            PersonalWorldFailureMapper.FromGateway(bootstrap));
                    }

                    var authentication = await authenticate(linked.Token);
                    if (!IsCurrent(generation))
                    {
                        return FinishIntent(generation, ClientPersonalWorldFailure.Stopped);
                    }

                    if (!authentication.IsSuccess)
                    {
                        return FinishIntent(
                            generation,
                            PersonalWorldFailureMapper.FromGateway(authentication));
                    }

                    var controlRun = _controlChannel.RunAsync(token);
                    _ = ObserveControlRunAsync(controlRun, generation, cancellationOwner: null);
                    if (!await _controlChannel.WaitUntilConnectedAsync(linked.Token))
                    {
                        return FinishIntent(generation, ClientPersonalWorldFailure.Transport);
                    }

                    // Session 提交后，关闭 Login 会取消其 route token；后续收敛只服从 App lifetime。
                    await _uiRouter.OpenAsync(ClientUiRouteId.Shell, 0, token);
                    await CloseRouteAsync(ClientUiRouteId.Login, token);
                    var entered = await _worldAdmissionCoordinator.EnterOwnWorldAsync(token);
                    var sceneReady = entered && await _sceneTransaction.SynchronizeAsync(this, generation, token);
                    var failure = entered
                        ? (sceneReady ? ClientPersonalWorldFailure.None : ClientPersonalWorldFailure.DependencyUnavailable)
                        : PersonalWorldFailureMapper.FromWorld(
                            _worldAdmissionCoordinator.Snapshot.Failure);
                    return FinishIntent(generation, failure);
                }
                catch (OperationCanceledException)
                {
                    return FinishCancellation(generation, cancellationToken);
                }
                catch (Exception)
                {
                    return FinishIntent(generation, ClientPersonalWorldFailure.Internal);
                }
            }
        }

        /// <summary>执行返回 bool 的 world-flow 动作。</summary>
        /// <param name="intent">当前动作类别。</param>
        /// <param name="action">由既有 WorldAdmissionCoordinator 拥有的动作。</param>
        /// <param name="cancellationToken">调用方取消信号。</param>
        /// <returns>稳定低敏结果。</returns>
        private async Task<ClientPersonalWorldActionResult> RunWorldActionAsync(
            ClientPersonalWorldIntent intent,
            Func<CancellationToken, Task<bool>> action,
            CancellationToken cancellationToken)
        {
            if (!TryBeginIntent(intent, out var generation, out var token))
            {
                return RejectBusyOrStopped();
            }

            if (cancellationToken.IsCancellationRequested)
            {
                return FinishCancellation(generation, cancellationToken);
            }

            try
            {
                // World command 一旦取得 intent，就可能通过状态发布关闭发起它的 route；此后只允许
                // App Scope/Session lifecycle 取消，避免 route binding token 把自己的 HTTP/TCP 流程取消。
                var succeeded = await action(token);
                var sceneReady = succeeded && await _sceneTransaction.SynchronizeAsync(this, generation, token);
                var failure = succeeded
                    ? (sceneReady ? ClientPersonalWorldFailure.None : ClientPersonalWorldFailure.DependencyUnavailable)
                    : PersonalWorldFailureMapper.FromWorld(
                        _worldAdmissionCoordinator.Snapshot.Failure);
                return FinishIntent(generation, failure);
            }
            catch (OperationCanceledException)
            {
                return FinishCancellation(generation, cancellationToken);
            }
            catch (Exception)
            {
                return FinishIntent(generation, ClientPersonalWorldFailure.Internal);
            }
        }

        /// <summary>执行任意强类型 gameplay command，并统一映射其封闭结果。</summary>
        /// <typeparam name="T">强类型 generated response；不会进入 View State。</typeparam>
        /// <param name="intent">当前动作类别。</param>
        /// <param name="action">由既有 VisitSessionService 拥有的动作。</param>
        /// <param name="cancellationToken">调用方取消信号。</param>
        /// <returns>稳定低敏结果。</returns>
        private async Task<ClientPersonalWorldActionResult> RunGameplayActionAsync<T>(
            ClientPersonalWorldIntent intent,
            Func<CancellationToken, Task<ClientGameplayResult<T>>> action,
            CancellationToken cancellationToken)
            where T : class
        {
            if (!TryBeginIntent(intent, out var generation, out var token))
            {
                return RejectBusyOrStopped();
            }

            using (var linked = CancellationTokenSource.CreateLinkedTokenSource(token, cancellationToken))
            {
                try
                {
                    var result = await action(linked.Token);
                    return FinishIntent(
                        generation,
                        result.IsSuccess
                            ? ClientPersonalWorldFailure.None
                            : PersonalWorldFailureMapper.FromGameplay(result.Failure));
                }
                catch (OperationCanceledException)
                {
                    return FinishCancellation(generation, cancellationToken);
                }
                catch (Exception)
                {
                    return FinishIntent(generation, ClientPersonalWorldFailure.Internal);
                }
            }
        }

        /// <summary>观察唯一 control run 的稳定终态，并在 Session 失效时回到 Login。</summary>
        /// <param name="runTask">由唯一 control owner 返回的 active run。</param>
        /// <param name="generation">启动 run 时的表现代际。</param>
        /// <param name="cancellationOwner">需要在 run 终态后释放的可选 cancellation owner。</param>
        /// <returns>Run 结束后的收敛任务。</returns>
        private async Task ObserveControlRunAsync(
            Task runTask,
            long generation,
            CancellationTokenSource cancellationOwner)
        {
            try
            {
                try
                {
                    await runTask;
                }
                catch (OperationCanceledException)
                {
                    return;
                }
                catch (Exception)
                {
                    if (IsCurrent(generation))
                    {
                        await EnterConnectionLostAsync(generation);
                    }

                    return;
                }

                if (!IsCurrent(generation))
                {
                    return;
                }

                if (!_sessionCoordinator.TryGetCurrent(out _))
                {
                    await ReturnToLoginAsync(generation);
                    return;
                }

                await EnterConnectionLostAsync(generation);
            }
            finally
            {
                cancellationOwner?.Dispose();
            }
        }

        /// <summary>撤销恢复阶段尚未提交的 control run，并按所有权决定是否同步释放 source。</summary>
        /// <param name="cancellation">恢复事务创建的 control run cancellation owner。</param>
        /// <param name="dispose">尚未转交观察器时为 true。</param>
        private static void CancelControlRun(CancellationTokenSource cancellation, bool dispose)
        {
            if (cancellation == null)
            {
                return;
            }

            try
            {
                cancellation.Cancel();
            }
            catch (ObjectDisposedException)
            {
                // Run 已同步进入终态并由观察器释放；该恢复事务无需重复撤销。
            }
            finally
            {
                if (dispose)
                {
                    cancellation.Dispose();
                }
            }
        }

        /// <summary>原子取得当前唯一 intent 所有权。</summary>
        /// <param name="intent">待执行语义动作。</param>
        /// <param name="generation">成功时返回当前表现代际。</param>
        /// <param name="lifetimeToken">成功时返回 App Scope 取消信号。</param>
        /// <returns>Experience 运行且没有 active intent 时返回 true。</returns>
        private bool TryBeginIntent(
            ClientPersonalWorldIntent intent,
            out long generation,
            out CancellationToken lifetimeToken)
        {
            lock (_sync)
            {
                if (!_presentationState.TryBeginIntent(intent, out generation))
                {
                    lifetimeToken = new CancellationToken(canceled: true);
                    return false;
                }

                lifetimeToken = _lifetimeCancellation.Token;
                RebuildViewStateLocked();
            }

            PublishCurrent();
            return true;
        }

        /// <summary>完成 current intent 并发布权威 snapshot 派生状态。</summary>
        /// <param name="generation">Intent 捕获的表现代际。</param>
        /// <param name="failure">稳定低敏失败；None 表示成功。</param>
        /// <returns>对应的语义动作结果。</returns>
        private ClientPersonalWorldActionResult FinishIntent(
            long generation,
            ClientPersonalWorldFailure failure)
        {
            lock (_sync)
            {
                if (!_presentationState.Running || generation != _presentationState.PresentationGeneration)
                {
                    return ClientPersonalWorldActionResult.Failed(ClientPersonalWorldFailure.Stopped);
                }

                var completedIntent = _presentationState.ActiveIntent;
                _presentationState.ActiveIntent = ClientPersonalWorldIntent.None;
                _presentationState.Failure = failure;
                _presentationState.FailureIntent = failure == ClientPersonalWorldFailure.None
                    ? ClientPersonalWorldIntent.None
                    : completedIntent;
                RebuildViewStateLocked();
            }

            PublishCurrent();
            return failure == ClientPersonalWorldFailure.None
                ? ClientPersonalWorldActionResult.Success()
                : ClientPersonalWorldActionResult.Failed(failure);
        }

        /// <summary>在 caller cancel 与 App stop 之间选择稳定结果，并对称释放 active intent。</summary>
        /// <param name="generation">被取消动作捕获的表现代际。</param>
        /// <param name="callerCancellationToken">调用方传入的取消信号。</param>
        /// <returns>CallerCancelled 或 Stopped 结果。</returns>
        private ClientPersonalWorldActionResult FinishCancellation(
            long generation,
            CancellationToken callerCancellationToken)
        {
            var failure = callerCancellationToken.IsCancellationRequested
                ? ClientPersonalWorldFailure.CallerCancelled
                : ClientPersonalWorldFailure.Stopped;
            return FinishIntent(generation, failure);
        }

        /// <summary>清理当前产品表现并以新代际返回 Login。</summary>
        /// <param name="generation">发起清理时的表现代际。</param>
        /// <returns>Login route 已打开时完成。</returns>
        private async Task ReturnToLoginAsync(long generation)
        {
            lock (_sync)
            {
                if (!_presentationState.Running ||
                    generation != _presentationState.PresentationGeneration ||
                    _presentationState.ReturningToLogin)
                {
                    return;
                }

                _presentationState.ReturningToLogin = true;
                _presentationState.PendingSessionInvalidationGeneration = 0;
                _presentationState.PresentationGeneration++;
                _presentationState.ActiveIntent = ClientPersonalWorldIntent.None;
                _presentationState.Failure = ClientPersonalWorldFailure.None;
                _presentationState.FailureIntent = ClientPersonalWorldIntent.None;
                _presentationState.ConnectionLost = false;
                RebuildViewStateLocked();
                generation = _presentationState.PresentationGeneration;
            }

            try
            {
                _worldAdmissionCoordinator.InvalidateSession();
                await _sessionInvalidationTransaction.ExecuteAsync(
                    () => _sceneTransaction.ConvergeToLoginAsync(
                        this,
                        generation,
                        CancellationToken.None),
                    CancellationToken.None);
            }
            finally
            {
                lock (_sync)
                {
                    _presentationState.ReturningToLogin = false;
                    RebuildViewStateLocked();
                }

                PublishCurrent();
            }
        }

        /// <summary>订阅既有权威 owner 的状态边界。</summary>
        private void SubscribeLocked()
        {
            if (_subscribed)
            {
                return;
            }

            _sessionCoordinator.Invalidated += OnSessionInvalidated;
            _personalWorldService.Changed += OnPersonalWorldChanged;
            _visitSessionService.Changed += OnVisitSessionChanged;
            _worldAdmissionCoordinator.Changed += OnWorldFlowChanged;
            _connectionRecoveryCoordinator.Changed += OnConnectionRecoveryChanged;

            _subscribed = true;
        }

        /// <summary>解除全部权威 owner subscriber。</summary>
        private void UnsubscribeLocked()
        {
            if (!_subscribed)
            {
                return;
            }

            _sessionCoordinator.Invalidated -= OnSessionInvalidated;
            _personalWorldService.Changed -= OnPersonalWorldChanged;
            _visitSessionService.Changed -= OnVisitSessionChanged;
            _worldAdmissionCoordinator.Changed -= OnWorldFlowChanged;
            _connectionRecoveryCoordinator.Changed -= OnConnectionRecoveryChanged;

            _subscribed = false;
        }

        /// <summary>把任意线程发布的 Session 失效边界合并后投递到 Unity 主线程。</summary>
        /// <param name="generation">Session owner 清除 lineage 后的 generation。</param>
        private void OnSessionInvalidated(long generation)
        {
            if (generation <= 0)
            {
                return;
            }

            lock (_sync)
            {
                if (!_presentationState.Running || generation <= _presentationState.PendingSessionInvalidationGeneration)
                {
                    return;
                }

                _presentationState.PendingSessionInvalidationGeneration = generation;
            }

            QueueAuthoritativePresentationConvergence();
        }

        /// <summary>按最新 Session authority 退役 target、Scene 与全部产品 route，并回到 Login。</summary>
        /// <returns>失效表现已经提交或被后续显式登录取代时完成。</returns>
        private async Task ObserveSessionInvalidationAsync()
        {
            try
            {
                long generation;
                lock (_sync)
                {
                    if (!_presentationState.Running ||
                        _sessionCoordinator.TryGetCurrent(out _) ||
                        _presentationState.PendingSessionInvalidationGeneration <= 0)
                    {
                        return;
                    }

                    _presentationState.PendingSessionInvalidationGeneration = 0;
                    generation = _presentationState.PresentationGeneration;
                }

                await ReturnToLoginAsync(generation);
            }
            catch (Exception)
            {
                if (IsRunning() && !_sessionCoordinator.TryGetCurrent(out _))
                {
                    RebuildAndPublish();
                }
            }
        }

        /// <summary>收敛 PersonalWorld 快照变化。</summary>
        /// <param name="snapshot">权威 Service 发布的不可变快照。</param>
        private void OnPersonalWorldChanged(ClientPersonalWorldServiceSnapshot snapshot)
        {
            ClearFailureForAuthority(worldAuthority: true);
            RebuildAndPublish();
        }

        /// <summary>收敛 VisitSession 快照变化。</summary>
        /// <param name="snapshot">权威 Service 发布的不可变快照。</param>
        private void OnVisitSessionChanged(ClientVisitSessionServiceSnapshot snapshot)
        {
            ClearFailureForAuthority(worldAuthority: false);
            RebuildAndPublish();
        }

        /// <summary>收敛 world target 状态变化。</summary>
        /// <param name="snapshot">权威 Coordinator 发布的不可变快照。</param>
        private void OnWorldFlowChanged(ClientWorldFlowSnapshot snapshot)
        {
            ClearFailureForAuthority(worldAuthority: true);
            RebuildAndPublish();
            long generation;
            bool synchronizeScene;
            lock (_sync)
            {
                generation = _presentationState.PresentationGeneration;
                // Login收敛事务已经持有完整Scene/route所有权；Session失效引发的Inactive通知
                // 仍刷新权威投影，但不能并行启动第二笔Scene卸载。
                synchronizeScene = !_presentationState.ReturningToLogin;
            }

            if (synchronizeScene)
            {
                _ = ObserveSceneSynchronizationAsync(generation, snapshot.TargetGeneration);
            }
        }

        /// <summary>把任意channel线程发布的恢复snapshot合并后投递到Unity主线程。</summary>
        /// <param name="snapshot">已由恢复owner提交的不可变snapshot；内容由callback执行时重读。</param>
        private void OnConnectionRecoveryChanged(ClientConnectionRecoverySnapshot snapshot)
        {
            QueueAuthoritativePresentationConvergence();
        }

        /// <summary>
        /// 合并 Session 与 connection recovery 通知，只排队一个重读全部权威 owner 的主线程 callback。
        /// </summary>
        private void QueueAuthoritativePresentationConvergence()
        {
            if (Interlocked.Exchange(ref _recoveryDispatchPending, 1) != 0)
            {
                return;
            }

            Action callback = () =>
            {
                Interlocked.Exchange(ref _recoveryDispatchPending, 0);
                if (!_sessionCoordinator.TryGetCurrent(out _))
                {
                    _ = ObserveSessionInvalidationAsync();
                }
                else
                {
                    _ = ObserveConnectionRecoveryAsync(_connectionRecoveryCoordinator.Snapshot);
                }
            };
            var posted = _mainThreadDispatcher.TryPost(callback);
            if (posted == DispatchPostResult.QueueFull)
            {
                posted = _mainThreadDispatcher.TryPostCritical(callback);
            }

            if (posted != DispatchPostResult.Accepted)
            {
                Interlocked.Exchange(ref _recoveryDispatchPending, 0);
            }
        }

        /// <summary>在Unity主线程把恢复阶段映射为能力、route与Scene提交。</summary>
        /// <param name="snapshot">当前恢复owner snapshot。</param>
        /// <returns>表现收敛完成时结束。</returns>
        private Task ObserveConnectionRecoveryAsync(
            ClientConnectionRecoverySnapshot snapshot)
        {
            return _recoveryPresentationTransaction.ExecuteAsync(
                () => ObserveConnectionRecoveryCoreAsync(snapshot),
                CancellationToken.None);
        }

        /// <summary>在已取得 recovery transaction 后提交 route、Scene 与 View State。</summary>
        private async Task ObserveConnectionRecoveryCoreAsync(
            ClientConnectionRecoverySnapshot snapshot)
        {
            long generation = 0;
            bool hasSession;
            try
            {
                lock (_sync)
                {
                    if (!_presentationState.Running)
                    {
                        return;
                    }

                    generation = _presentationState.PresentationGeneration;
                }

                if (!IsCurrentRecoverySnapshot(snapshot, generation))
                {
                    return;
                }

                lock (_sync)
                {
                    hasSession = _sessionCoordinator.TryGetCurrent(out _);
                    _presentationState.ConnectionLost = hasSession &&
                                      (snapshot.Phase == ClientConnectionRecoveryPhase.RecoveringWorld ||
                                       snapshot.Phase == ClientConnectionRecoveryPhase.AwaitingSceneCommit ||
                                       snapshot.Phase == ClientConnectionRecoveryPhase.ConnectionLost);
                    _presentationState.Failure = hasSession &&
                               snapshot.Phase == ClientConnectionRecoveryPhase.ConnectionLost
                        ? PersonalWorldFailureMapper.FromRecovery(snapshot.Result)
                        : ClientPersonalWorldFailure.None;
                    _presentationState.FailureIntent = hasSession && _presentationState.Failure != ClientPersonalWorldFailure.None
                        ? ClientPersonalWorldIntent.Reconnect
                        : ClientPersonalWorldIntent.None;
                    RebuildViewStateLocked();
                }

                PublishCurrent();
                if (!hasSession)
                {
                    await ReturnToLoginAsync(generation);
                    return;
                }

                switch (snapshot.Phase)
                {
                    case ClientConnectionRecoveryPhase.RecoveringWorld:
                        await _uiRouter.OpenAsync(
                            ClientUiRouteId.ConnectionLost,
                            sceneGeneration: 0,
                            CancellationToken.None);
                        if (!IsCurrentRecoverySnapshot(snapshot, generation))
                        {
                            return;
                        }

                        await _sceneTransaction.SynchronizeAsync(this, generation, CancellationToken.None);
                        break;
                    case ClientConnectionRecoveryPhase.AwaitingSceneCommit:
                        await _uiRouter.OpenAsync(
                            ClientUiRouteId.ConnectionLost,
                            sceneGeneration: 0,
                            CancellationToken.None);
                        if (!IsCurrentRecoverySnapshot(snapshot, generation))
                        {
                            return;
                        }

                        await CommitRecoverySceneAsync(snapshot, generation, CancellationToken.None);
                        break;
                    case ClientConnectionRecoveryPhase.ConnectionLost:
                        await _sceneTransaction.SynchronizeAsync(this, generation, CancellationToken.None);
                        if (!IsCurrentRecoverySnapshot(snapshot, generation))
                        {
                            return;
                        }

                        await _uiRouter.OpenAsync(
                            ClientUiRouteId.ConnectionLost,
                            sceneGeneration: 0,
                            CancellationToken.None);
                        break;
                    case ClientConnectionRecoveryPhase.Idle:
                        await CloseRouteAsync(ClientUiRouteId.ConnectionLost, CancellationToken.None);
                        break;
                }
            }
            catch (Exception)
            {
                if (snapshot.Phase == ClientConnectionRecoveryPhase.AwaitingSceneCommit &&
                    IsCurrentRecoverySnapshot(snapshot, generation))
                {
                    _connectionRecoveryCoordinator.FailSceneCommit(
                        snapshot.IntentGeneration,
                        snapshot.TargetGeneration,
                        ClientConnectionRecoveryResultKind.Internal);
                }
            }
        }

        /// <summary>加载current target Scene/HUD并提交恢复第四重gate。</summary>
        /// <param name="snapshot">AwaitingSceneCommit snapshot。</param>
        /// <param name="generation">当前表现代际。</param>
        /// <param name="cancellationToken">调用方等待取消信号。</param>
        /// <returns>Scene gate已提交时返回true。</returns>
        private async Task<bool> CommitRecoverySceneAsync(
            ClientConnectionRecoverySnapshot snapshot,
            long generation,
            CancellationToken cancellationToken)
        {
            if (snapshot.Phase != ClientConnectionRecoveryPhase.AwaitingSceneCommit)
            {
                return false;
            }

            if (!await _sceneTransaction.SynchronizeAsync(this, generation, cancellationToken))
            {
                _connectionRecoveryCoordinator.FailSceneCommit(
                    snapshot.IntentGeneration,
                    snapshot.TargetGeneration,
                    ClientConnectionRecoveryResultKind.Internal);
                return false;
            }

            if (!_connectionRecoveryCoordinator.ConfirmSceneCommit(
                    snapshot.IntentGeneration,
                    snapshot.TargetGeneration))
            {
                return _connectionRecoveryCoordinator.Snapshot.Phase ==
                       ClientConnectionRecoveryPhase.Idle;
            }

            lock (_sync)
            {
                if (_presentationState.Running && generation == _presentationState.PresentationGeneration)
                {
                    _presentationState.ConnectionLost = false;
                    _presentationState.Failure = ClientPersonalWorldFailure.None;
                    _presentationState.FailureIntent = ClientPersonalWorldIntent.None;
                    RebuildViewStateLocked();
                }
            }

            PublishCurrent();
            await CloseRouteAsync(ClientUiRouteId.ConnectionLost, cancellationToken);
            return true;
        }

        /// <summary>在对应权威 replacement 到达后清除已经过期的 action failure。</summary>
        /// <param name="worldAuthority">true 表示 world flow/PersonalWorld；false 表示 VisitSession。</param>
        /// <remarks>
        /// 清除只由新 owner snapshot 触发，不使用时间、frame tick 或自动重试。当前 active intent 的
        /// 结果仍由其 completion 提交，避免中途 replacement 抹掉尚未完成的 command 状态。
        /// </remarks>
        private void ClearFailureForAuthority(bool worldAuthority)
        {
            lock (_sync)
            {
                if (_presentationState.ActiveIntent != ClientPersonalWorldIntent.None ||
                    _presentationState.Failure == ClientPersonalWorldFailure.None)
                {
                    return;
                }

                var matches = worldAuthority
                    ? IsWorldAuthorityIntent(_presentationState.FailureIntent)
                    : IsVisitAuthorityIntent(_presentationState.FailureIntent);
                if (matches)
                {
                    _presentationState.Failure = ClientPersonalWorldFailure.None;
                    _presentationState.FailureIntent = ClientPersonalWorldIntent.None;
                }
            }
        }

        /// <summary>判断失败是否由 world target authority 管理的动作产生。</summary>
        /// <param name="intent">最近完成的 action。</param>
        /// <returns>新 world replacement 可使该失败失效时返回 true。</returns>
        private static bool IsWorldAuthorityIntent(ClientPersonalWorldIntent intent)
        {
            return intent == ClientPersonalWorldIntent.EnterOwnWorld ||
                   intent == ClientPersonalWorldIntent.AcceptInvite ||
                   intent == ClientPersonalWorldIntent.LeaveVisit ||
                   intent == ClientPersonalWorldIntent.RetryReturn;
        }

        /// <summary>判断失败是否由 VisitSession authority 管理的动作产生。</summary>
        /// <param name="intent">最近完成的 action。</param>
        /// <returns>新 VisitSession replacement 可使该失败失效时返回 true。</returns>
        private static bool IsVisitAuthorityIntent(ClientPersonalWorldIntent intent)
        {
            return intent == ClientPersonalWorldIntent.OpenVisit ||
                   intent == ClientPersonalWorldIntent.CreateInvite ||
                   intent == ClientPersonalWorldIntent.RevokeInvite ||
                   intent == ClientPersonalWorldIntent.AcceptInvite ||
                   intent == ClientPersonalWorldIntent.KickVisitor ||
                   intent == ClientPersonalWorldIntent.CloseVisit ||
                   intent == ClientPersonalWorldIntent.LeaveVisit;
        }

        /// <summary>从既有 owner 当前快照重建并发布页面投影。</summary>
        private void RebuildAndPublish()
        {
            ClientPersonalWorldViewState state;
            lock (_sync)
            {
                if (!_presentationState.Running)
                {
                    return;
                }

                RebuildViewStateLocked();
                state = _presentationState.ViewState;
            }

            _sceneTransition.TryApply(state);
            PublishCurrent();
        }

        /// <summary>从既有 owner 当前快照重建页面投影；调用方必须持有锁。</summary>
        private void RebuildViewStateLocked()
        {
            var recovery = _connectionRecoveryCoordinator.Snapshot;
            var hasSession = _sessionCoordinator.TryGetCurrent(out _);
            var phase = _presentationState.RestoringSession
                ? ClientPersonalWorldPhase.RestoringSession
                : _presentationState.ActiveIntent == ClientPersonalWorldIntent.Login ||
                  _presentationState.ActiveIntent == ClientPersonalWorldIntent.Register
                    ? ClientPersonalWorldPhase.Authenticating
                    : hasSession && recovery.Phase != ClientConnectionRecoveryPhase.Idle
                        ? MapRecoveryPhase(recovery.Phase)
                        : _presentationState.ConnectionLost
                            ? ClientPersonalWorldPhase.ConnectionLost
                            : MapPhase(_worldAdmissionCoordinator.Snapshot.State);
            _presentationState.ViewState = ProjectViewStateLocked(phase, recovery);
        }

        /// <summary>从冻结 owner snapshots 运行纯 View State projector。</summary>
        private ClientPersonalWorldViewState ProjectViewStateLocked(
            ClientPersonalWorldPhase phase,
            ClientConnectionRecoverySnapshot recovery)
        {
            _sessionCoordinator.TryGetCurrent(out var session);
            return _viewStateProjector.Project(
                new PersonalWorldProjectionInput(
                    _presentationState.PresentationGeneration,
                    phase,
                    _presentationState.ActiveIntent,
                    _presentationState.Failure,
                    _presentationState.ConnectionLost,
                    session,
                    _worldAdmissionCoordinator.Snapshot,
                    _personalWorldService.Snapshot,
                    _visitSessionService.Snapshot,
                    recovery,
                    _sceneTransition.Snapshot));
        }

        /// <summary>锁外隔离并通知全部页面 subscriber。</summary>
        private void PublishCurrent()
        {
            Action<ClientPersonalWorldViewState> subscribers;
            ClientPersonalWorldViewState state;
            lock (_sync)
            {
                subscribers = _viewStateChanged;
                state = _presentationState.ViewState;
            }

            if (subscribers == null)
            {
                return;
            }

            foreach (Action<ClientPersonalWorldViewState> subscriber in subscribers.GetInvocationList())
            {
                try
                {
                    subscriber(state);
                }
                catch (Exception)
                {
                    // Subscriber 已隔离；具体异常由 Unity 调用边界记录，不能阻断其他页面状态提交。
                }
            }
        }

        /// <summary>
        /// 观察由权威 PUSH 或 safe-return 触发的场景同步，防止 async void 与未观察异常。
        /// </summary>
        /// <param name="generation">事件处理捕获的表总代际。</param>
        /// <param name="targetGeneration">触发同步的权威 target generation。</param>
        /// <returns>场景与 route 收敛后的观察任务。</returns>
        private async Task ObserveSceneSynchronizationAsync(
            long generation,
            long targetGeneration)
        {
            try
            {
                await _sceneTransaction.SynchronizeAsync(this, generation, CancellationToken.None);
            }
            catch (Exception)
            {
                if (IsCurrentWorldPresentation(generation, targetGeneration) &&
                    !HasCommittedSceneForTarget(targetGeneration))
                {
                    SetFailure(ClientPersonalWorldFailure.DependencyUnavailable);
                }
            }
        }

        /// <summary>检查异步Scene观察仍绑定当前presentation与target代际。</summary>
        /// <param name="generation">观察任务捕获的表总代际。</param>
        /// <param name="targetGeneration">观察任务捕获的target generation。</param>
        /// <returns>两个代际和Experience生命周期均仍current时返回true。</returns>
        private bool IsCurrentWorldPresentation(long generation, long targetGeneration)
        {
            if (!IsCurrent(generation))
            {
                return false;
            }

            return _worldAdmissionCoordinator.Snapshot.TargetGeneration == targetGeneration &&
                   IsCurrent(generation);
        }

        /// <summary>判断当前PersonalWorld Scene已经提交指定权威target。</summary>
        /// <param name="targetGeneration">待校验的current target generation。</param>
        /// <returns>世界终态与Scene identity完全一致时返回true。</returns>
        private bool HasCommittedSceneForTarget(long targetGeneration)
        {
            var flow = _worldAdmissionCoordinator.Snapshot;
            var scene = _sceneTransition.Snapshot;
            return flow.TargetGeneration == targetGeneration &&
                   (flow.State == ClientWorldFlowState.OwnWorld ||
                    flow.State == ClientWorldFlowState.Visiting) &&
                   scene.SceneId == ClientWorldSceneId.PersonalWorld &&
                   scene.TargetGeneration == targetGeneration &&
                   scene.SceneGeneration > 0;
        }

        /// <summary>观察输入事件触发的异步导航，避免 Unity 回调产生未观察异常。</summary>
        /// <param name="operation">已由当前 Experience 发起的导航任务。</param>
        /// <returns>导航完成或已映射为低敏失败时完成。</returns>
        private async Task ObserveUiIntentAsync(Task operation)
        {
            try
            {
                await operation;
            }
            catch (OperationCanceledException)
            {
                // 页面或 App 生命周期取消属于正常终止，不覆盖更高代际 View State。
            }
            catch (Exception)
            {
                if (IsRunning())
                {
                    SetFailure(ClientPersonalWorldFailure.DependencyUnavailable);
                }
            }
        }

        /// <summary>提交稳定断开状态并打开唯一 ConnectionLost modal。</summary>
        /// <param name="generation">结束的 control run 捕获的表现代际。</param>
        /// <returns>Modal 打开尝试完成时结束。</returns>
        private async Task EnterConnectionLostAsync(long generation)
        {
            lock (_sync)
            {
                if (!_presentationState.Running || generation != _presentationState.PresentationGeneration)
                {
                    return;
                }

                _presentationState.ConnectionLost = true;
                _presentationState.Failure = ClientPersonalWorldFailure.Transport;
                _presentationState.FailureIntent = ClientPersonalWorldIntent.Reconnect;
                // Reconnect 事务本身仍拥有 single-flight，必须由 FinishIntent 对称释放；
                // 其他动作则因 control 终态失去继续提交的前提，可以立即终结。
                if (_presentationState.ActiveIntent != ClientPersonalWorldIntent.Reconnect)
                {
                    _presentationState.ActiveIntent = ClientPersonalWorldIntent.None;
                }

                RebuildViewStateLocked();
            }

            PublishCurrent();
            await _uiRouter.OpenAsync(
                ClientUiRouteId.ConnectionLost,
                sceneGeneration: 0,
                CancellationToken.None);
        }

        /// <summary>设置低敏失败并重建页面状态。</summary>
        /// <param name="failure">待展示的封闭失败类别。</param>
        private void SetFailure(ClientPersonalWorldFailure failure)
        {
            lock (_sync)
            {
                if (!_presentationState.Running)
                {
                    return;
                }

                _presentationState.Failure = failure;
                _presentationState.FailureIntent = ClientPersonalWorldIntent.None;
                RebuildViewStateLocked();
            }

            PublishCurrent();
        }

        /// <summary>检查异步结果的表现代际仍 current。</summary>
        /// <param name="generation">异步动作捕获的表现代际。</param>
        /// <returns>Experience 仍运行且代际匹配时返回 true。</returns>
        private bool IsCurrent(long generation)
        {
            lock (_sync)
            {
                return _presentationState.IsCurrent(generation);
            }
        }

        /// <inheritdoc />
        bool IPersonalWorldSceneTransactionHost.IsPresentationCurrent(long generation)
        {
            return IsCurrent(generation);
        }

        /// <inheritdoc />
        ClientWorldFlowSnapshot IPersonalWorldSceneTransactionHost.CurrentWorldFlow =>
            _worldAdmissionCoordinator.Snapshot;

        /// <inheritdoc />
        bool IPersonalWorldSceneTransactionHost.HasCurrentSession =>
            _sessionCoordinator.TryGetCurrent(out _);

        /// <inheritdoc />
        ClientPersonalWorldViewState
            IPersonalWorldSceneTransactionHost.RebuildPresentation()
        {
            lock (_sync)
            {
                RebuildViewStateLocked();
                return _presentationState.ViewState;
            }
        }

        /// <inheritdoc />
        void IPersonalWorldSceneTransactionHost.PublishPresentation()
        {
            PublishCurrent();
        }

        /// <summary>检查恢复观察仍对应current Coordinator snapshot与表现代际。</summary>
        /// <param name="snapshot">观察任务捕获的不可变恢复快照。</param>
        /// <param name="generation">观察任务捕获的表现代际。</param>
        /// <returns>快照、表现代际和Experience生命周期都仍current时返回true。</returns>
        private bool IsCurrentRecoverySnapshot(
            ClientConnectionRecoverySnapshot snapshot,
            long generation)
        {
            if (!IsCurrent(generation))
            {
                return false;
            }

            return ReferenceEquals(_connectionRecoveryCoordinator.Snapshot, snapshot) &&
                   IsCurrent(generation);
        }

        /// <summary>检查 Experience 是否允许页面动作。</summary>
        /// <returns>当前 App Scope 仍运行时返回 true。</returns>
        private bool IsRunning()
        {
            lock (_sync)
            {
                return _presentationState.Running;
            }
        }

        /// <summary>生成 busy 或 stopped 的稳定拒绝。</summary>
        /// <returns>当前生命周期对应的低敏失败。</returns>
        private ClientPersonalWorldActionResult RejectBusyOrStopped()
        {
            return ClientPersonalWorldActionResult.Failed(
                IsRunning() ? ClientPersonalWorldFailure.Permission : ClientPersonalWorldFailure.Stopped);
        }

        /// <summary>幂等关闭已登记或尚未接线的 route。</summary>
        /// <param name="routeId">待关闭产品 route。</param>
        /// <param name="cancellationToken">清理 deadline。</param>
        /// <returns>Router 完成关闭尝试时完成。</returns>
        private async Task CloseRouteAsync(ClientUiRouteId routeId, CancellationToken cancellationToken)
        {
            var result = await _uiRouter.CloseAsync(routeId, cancellationToken);
            if (result.Code == ClientUiTransitionCode.NotRegistered)
            {
                return;
            }
        }

        /// <summary>将权威 world flow 状态映射为产品阶段。</summary>
        /// <param name="state">既有 Coordinator 状态。</param>
        /// <returns>封闭产品阶段。</returns>
        private static ClientPersonalWorldPhase MapPhase(ClientWorldFlowState state)
        {
            switch (state)
            {
                case ClientWorldFlowState.Inactive:
                    return ClientPersonalWorldPhase.Login;
                case ClientWorldFlowState.ResolvingOwnWorld:
                    return ClientPersonalWorldPhase.EnteringOwnWorld;
                case ClientWorldFlowState.OwnWorld:
                    return ClientPersonalWorldPhase.OwnWorld;
                case ClientWorldFlowState.JoiningVisit:
                    return ClientPersonalWorldPhase.JoiningVisit;
                case ClientWorldFlowState.Visiting:
                    return ClientPersonalWorldPhase.Visiting;
                case ClientWorldFlowState.ReturningOwnWorld:
                    return ClientPersonalWorldPhase.ReturningOwnWorld;
                case ClientWorldFlowState.RecoveringTarget:
                    return ClientPersonalWorldPhase.RecoveringWorld;
                case ClientWorldFlowState.ConnectionLost:
                    return ClientPersonalWorldPhase.ConnectionLost;
                case ClientWorldFlowState.Stopped:
                    return ClientPersonalWorldPhase.Stopped;
                default:
                    return ClientPersonalWorldPhase.ConnectionLost;
            }
        }

        /// <summary>将唯一恢复owner阶段映射为产品阶段。</summary>
        /// <param name="phase">generation-bound恢复阶段。</param>
        /// <returns>页面可展示的封闭阶段。</returns>
        private static ClientPersonalWorldPhase MapRecoveryPhase(
            ClientConnectionRecoveryPhase phase)
        {
            switch (phase)
            {
                case ClientConnectionRecoveryPhase.RecoveringControl:
                    return ClientPersonalWorldPhase.RecoveringControl;
                case ClientConnectionRecoveryPhase.RecoveringWorld:
                    return ClientPersonalWorldPhase.RecoveringWorld;
                case ClientConnectionRecoveryPhase.AwaitingSceneCommit:
                    return ClientPersonalWorldPhase.AwaitingScene;
                case ClientConnectionRecoveryPhase.ConnectionLost:
                    return ClientPersonalWorldPhase.ConnectionLost;
                case ClientConnectionRecoveryPhase.Stopped:
                    return ClientPersonalWorldPhase.Stopped;
                default:
                    return ClientPersonalWorldPhase.ConnectionLost;
            }
        }

    }
}
