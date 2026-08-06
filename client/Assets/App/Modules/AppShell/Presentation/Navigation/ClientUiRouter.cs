using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Core.Foundation.Lifetime;

namespace IHomeland.Client.AppShell.Presentation.Navigation
{
    /// <summary>
    /// 线性化全部纯 UI route 转换，并原子拥有 active route、modal 栈和 navigation generation。
    /// </summary>
    /// <remarks>
    /// Router 不持有业务事实或 Unity 控件。Host 操作始终位于单一 transition gate 内，状态只在
    /// candidate 完成 show 并尝试 focus 后提交；focus 不可用和 subscriber 异常不会回滚已提交事实，
    /// 但会返回可观察的 post-commit failure。
    /// </remarks>
    internal sealed class ClientUiRouter : IAppLifetimeParticipant
    {
        /// <summary>保护 lifecycle、active owner 与 snapshot 的短暂原子读写；持锁时不调用外部代码。</summary>
        private readonly object _sync = new object();

        /// <summary>保存构造时完整冻结的 route/Host registry。</summary>
        private readonly ClientUiRegistry _registry;

        /// <summary>保存唯一 Input System、cursor 与 gameplay gate owner。</summary>
        private readonly IClientUiInputCoordinator _inputCoordinator;

        /// <summary>拥有有界等待计数与唯一串行 transition gate。</summary>
        private readonly ClientUiTransitionQueue _transitionQueue;

        /// <summary>限制 caller cancellation 后回滚与提交后 Host 清理的总等待时间。</summary>
        private readonly TimeSpan _cleanupTimeout;

        /// <summary>唯一拥有 active/cached routes、snapshot 与 navigation generation。</summary>
        private readonly ClientUiRouteState _routeState = new ClientUiRouteState();

        /// <summary>纯计算 layer replacement 与 SceneBound policy。</summary>
        private readonly ClientUiTransitionPlanner _planner =
            new ClientUiTransitionPlanner();

        /// <summary>纯计算最高交互 owner 与 input plan。</summary>
        private readonly ClientUiInteractionResolver _interactionResolver =
            new ClientUiInteractionResolver();

        /// <summary>执行 Host initialize/bind/show 与 input/focus 副作用。</summary>
        private readonly ClientUiTransitionTransaction _transitionTransaction;

        /// <summary>在停止开始时取消当前 candidate 和所有页面 binding。</summary>
        private CancellationTokenSource _lifetimeCancellation;

        /// <summary>表示 router 已完成 AppLifetime 初始化并可接收导航。</summary>
        private bool _running;

        /// <summary>表示 stop 已开始，必须在等待 gate 前拒绝新导航。</summary>
        private bool _stopping;

        /// <summary>表示 router 已完成终态清理，不能再次初始化。</summary>
        private bool _stopped;

        /// <summary>保存由短锁保护且在 stop 时统一解除的 snapshot subscribers。</summary>
        private Action<ClientUiRouteSnapshot> _snapshotChanged;

        /// <summary>
        /// 创建 App Scope 唯一 router。
        /// </summary>
        /// <param name="registry">已在副作用前冻结验证的 route/Host registry。</param>
        /// <param name="inputCoordinator">唯一 Input System 与 focus 协调边界。</param>
        /// <param name="maximumQueuedTransitions">等待 transition gate 的硬上限。</param>
        /// <param name="cleanupTimeout">不受 caller cancellation 影响的回滚与提交后清理总时限。</param>
        /// <exception cref="ArgumentNullException">依赖为空时抛出。</exception>
        /// <exception cref="ArgumentOutOfRangeException">队列上限或清理时限非正数时抛出。</exception>
        internal ClientUiRouter(
            ClientUiRegistry registry,
            IClientUiInputCoordinator inputCoordinator,
            int maximumQueuedTransitions,
            TimeSpan cleanupTimeout)
        {
            _registry = registry ?? throw new ArgumentNullException(nameof(registry));
            _inputCoordinator = inputCoordinator ?? throw new ArgumentNullException(nameof(inputCoordinator));
            if (maximumQueuedTransitions <= 0)
            {
                throw new ArgumentOutOfRangeException(nameof(maximumQueuedTransitions));
            }

            if (cleanupTimeout <= TimeSpan.Zero)
            {
                throw new ArgumentOutOfRangeException(nameof(cleanupTimeout));
            }

            _transitionQueue = new ClientUiTransitionQueue(maximumQueuedTransitions);
            _transitionTransaction = new ClientUiTransitionTransaction(
                _inputCoordinator,
                _interactionResolver);
            _cleanupTimeout = cleanupTimeout;
        }

        /// <summary>
        /// 在 route snapshot 原子提交后通知 presentation consumer；单个异常映射为提交后失败，stop 会解除全部订阅。
        /// </summary>
        internal event Action<ClientUiRouteSnapshot> SnapshotChanged
        {
            add
            {
                lock (_sync)
                {
                    if (_stopping || _stopped)
                    {
                        throw new InvalidOperationException("已停止的 ClientUiRouter 不能登记 snapshot subscriber。");
                    }

                    _snapshotChanged += value;
                }
            }

            remove
            {
                lock (_sync)
                {
                    _snapshotChanged -= value;
                }
            }
        }

        /// <summary>获取最近一次原子提交的不可变 route snapshot。</summary>
        internal ClientUiRouteSnapshot CurrentSnapshot
        {
            get
            {
                lock (_sync)
                {
                    return _routeState.Snapshot;
                }
            }
        }

#if DEVELOPMENT_BUILD || UNITY_EDITOR
        /// <summary>获取资格范围内route snapshot subscriber数量。</summary>
        internal int QualificationSubscriptionCount
        {
            get
            {
                lock (_sync)
                {
                    return _snapshotChanged?.GetInvocationList().Length ?? 0;
                }
            }
        }
#endif

        /// <summary>
        /// 进入可接收显式导航的空状态；不会打开 route 或访问业务 Service。
        /// </summary>
        /// <param name="cancellationToken">取消当前初始化等待。</param>
        /// <returns>router 已进入 Running 时完成的任务。</returns>
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            lock (_sync)
            {
                if (_stopped || _stopping)
                {
                    throw new InvalidOperationException("已停止的 ClientUiRouter 不能重新初始化。");
                }

                if (_running)
                {
                    return Task.CompletedTask;
                }

                _lifetimeCancellation = new CancellationTokenSource();
                _running = true;
                _routeState.Snapshot = ClientUiRouteSnapshot.Empty;
            }

            return Task.CompletedTask;
        }

        /// <summary>
        /// 显式打开 route，并在所有提交前 candidate 阶段成功后提交唯一 owner。
        /// </summary>
        /// <param name="routeId">已登记的封闭 route identity。</param>
        /// <param name="sceneGeneration">SceneBound route 的 current generation；其他 route 必须为零。</param>
        /// <param name="cancellationToken">调用方等待取消，只在提交前有效。</param>
        /// <returns>稳定 transition result。</returns>
        internal async Task<ClientUiTransitionResult> OpenAsync(
            ClientUiRouteId routeId,
            long sceneGeneration,
            CancellationToken cancellationToken)
        {
            ClientUiDiagnostics.Trace(
                nameof(ClientUiRouter),
                "open_requested",
                $"route={routeId} scene_generation={sceneGeneration} current_generation={CurrentSnapshot.Generation}");
            var admission = await EnterTransitionAsync(cancellationToken);
            if (!admission.Entered)
            {
                ClientUiDiagnostics.Trace(
                    nameof(ClientUiRouter),
                    "open_rejected",
                    $"route={routeId} code={admission.Result.Code}");
                return admission.Result;
            }

            try
            {
                var result = await OpenCoreAsync(routeId, sceneGeneration, cancellationToken);
                ClientUiDiagnostics.Trace(
                    nameof(ClientUiRouter),
                    "open_completed",
                    $"route={routeId} code={result.Code} committed={result.Committed} " +
                    $"current_generation={CurrentSnapshot.Generation}");
                return result;
            }
            finally
            {
                _transitionQueue.Exit();
            }
        }

        /// <summary>
        /// 显式关闭 route；不存在的 route 视为幂等成功，非栈顶 modal 拒绝越级关闭。
        /// </summary>
        /// <param name="routeId">待关闭 route identity。</param>
        /// <param name="cancellationToken">调用方等待取消。</param>
        /// <returns>稳定 transition result。</returns>
        internal async Task<ClientUiTransitionResult> CloseAsync(
            ClientUiRouteId routeId,
            CancellationToken cancellationToken)
        {
            ClientUiDiagnostics.Trace(
                nameof(ClientUiRouter),
                "close_requested",
                $"route={routeId} current_generation={CurrentSnapshot.Generation}");
            var admission = await EnterTransitionAsync(cancellationToken);
            if (!admission.Entered)
            {
                ClientUiDiagnostics.Trace(
                    nameof(ClientUiRouter),
                    "close_rejected",
                    $"route={routeId} code={admission.Result.Code}");
                return admission.Result;
            }

            try
            {
                var result = await CloseCoreAsync(routeId, cancellationToken, publishSnapshot: true);
                ClientUiDiagnostics.Trace(
                    nameof(ClientUiRouter),
                    "close_completed",
                    $"route={routeId} code={result.Code} committed={result.Committed} " +
                    $"current_generation={CurrentSnapshot.Generation}");
                return result;
            }
            finally
            {
                _transitionQueue.Exit();
            }
        }

        /// <summary>
        /// 关闭指定 Scene Scope generation 的全部 SceneBound route，阻止旧场景 callback 回写。
        /// </summary>
        /// <param name="sceneGeneration">已经失效的正 Scene Scope generation。</param>
        /// <param name="cancellationToken">调用方等待取消。</param>
        /// <returns>全部匹配 route 均已收敛时的稳定结果。</returns>
        internal async Task<ClientUiTransitionResult> InvalidateSceneAsync(
            long sceneGeneration,
            CancellationToken cancellationToken)
        {
            if (sceneGeneration <= 0)
            {
                return ClientUiTransitionResult.Rejected(
                    ClientUiTransitionCode.InvalidSceneGeneration);
            }

            var admission = await EnterTransitionAsync(cancellationToken);
            if (!admission.Entered)
            {
                return admission.Result;
            }

            try
            {
                var targets = _routeState.ActiveRoutes
                    .Where(route =>
                        route.Definition.Lifecycle == ClientUiLifecycle.SceneBound &&
                        route.Binding.SceneGeneration == sceneGeneration)
                    .Select(route => route.Definition.RouteId)
                    .Reverse()
                    .ToArray();
                var postCommitFailure = false;
                foreach (var target in targets)
                {
                    var result = await CloseCoreAsync(target, cancellationToken, publishSnapshot: false);
                    postCommitFailure |=
                        result.Code == ClientUiTransitionCode.CommittedWithPostCommitFailure;
                    if (!result.Committed && !result.IsSuccess)
                    {
                        return result;
                    }
                }

                if (targets.Length == 0)
                {
                    return ClientUiTransitionResult.UnchangedSuccess;
                }

                postCommitFailure |= PublishSnapshot(CurrentSnapshot);
                return postCommitFailure
                    ? ClientUiTransitionResult.PostCommitFailure
                    : ClientUiTransitionResult.CommittedSuccess;
            }
            finally
            {
                _transitionQueue.Exit();
            }
        }

        /// <summary>
        /// 验证迟到 callback 是否仍匹配 active route、navigation、Host 和 Scene generation。
        /// </summary>
        /// <param name="binding">callback 捕获的不可变 route binding。</param>
        /// <param name="hostGeneration">callback 捕获的 Host generation。</param>
        /// <returns>仍有资格写入当前 view 时返回 true。</returns>
        internal bool CanCommit(ClientUiRouteBinding binding, long hostGeneration)
        {
            if (binding == null || binding.CancellationToken.IsCancellationRequested)
            {
                return false;
            }

            lock (_sync)
            {
                if (!_running || _stopping || _stopped)
                {
                    return false;
                }

                var route = _routeState.ActiveRoutes.FirstOrDefault(
                    current => current.Definition.RouteId == binding.RouteId);
                return route != null &&
                    ReferenceEquals(route.Binding, binding) &&
                    route.Binding.NavigationGeneration == binding.NavigationGeneration &&
                    route.Binding.SceneGeneration == binding.SceneGeneration &&
                    route.Host.HostGeneration == hostGeneration;
            }
        }

        /// <summary>
        /// 先永久拒绝新导航并取消当前 candidate，再串行逆序释放 active/cached Host。
        /// </summary>
        /// <param name="cancellationToken">共享 AppLifetime 清理 deadline。</param>
        /// <returns>全部可执行 UI 清理均已尝试时完成的任务。</returns>
        public async Task StopAsync(CancellationToken cancellationToken)
        {
            CancellationTokenSource lifetimeCancellation;
            lock (_sync)
            {
                if (_stopped)
                {
                    return;
                }

                if (!_running && !_stopping)
                {
                    _snapshotChanged = null;
                    _stopped = true;
                    return;
                }

                _stopping = true;
                _running = false;
                lifetimeCancellation = _lifetimeCancellation;
            }

            var errors = new List<Exception>();
            TryCancel(lifetimeCancellation, errors);
            var gateEntered = false;
            try
            {
                await _transitionQueue.EnterForStopAsync(cancellationToken);
                gateEntered = true;
                for (var index = _routeState.ActiveRoutes.Count - 1; index >= 0; index--)
                {
                    await CleanupRouteForStopAsync(_routeState.ActiveRoutes[index], cancellationToken, errors);
                }

                foreach (var cached in _routeState.CachedRoutes.Values)
                {
                    await TryCleanupAsync(
                        () => cached.Host.DisposeAsync(cancellationToken),
                        errors);
                }

                await TryCleanupAsync(
                    () => _inputCoordinator.ApplyAsync(ClientUiInputState.Gameplay, cancellationToken),
                    errors);

                _routeState.CachedRoutes.Clear();
                CommitActiveRoutes(
                    _routeState.NavigationGeneration,
                    Array.Empty<ClientUiActiveRoute>(),
                    requireRunning: false);
                lock (_sync)
                {
                    _snapshotChanged = null;
                }
            }
            finally
            {
                if (gateEntered)
                {
                    _transitionQueue.Exit();
                    lifetimeCancellation?.Dispose();
                    lock (_sync)
                    {
                        _stopping = false;
                        _stopped = true;
                        _lifetimeCancellation = null;
                    }
                }
            }

            if (errors.Count > 0)
            {
                throw new AggregateException("ClientUiRouter 停止时一个或多个资源清理失败。", errors);
            }
        }

        /// <summary>
        /// 在已取得 transition gate 后执行 open transaction。
        /// </summary>
        /// <param name="routeId">待打开 route。</param>
        /// <param name="sceneGeneration">调用方提供的 Scene Scope generation。</param>
        /// <param name="callerCancellation">调用方取消。</param>
        /// <returns>稳定 transaction result。</returns>
        private async Task<ClientUiTransitionResult> OpenCoreAsync(
            ClientUiRouteId routeId,
            long sceneGeneration,
            CancellationToken callerCancellation)
        {
            if (!IsRunning())
            {
                return ClientUiTransitionResult.Rejected(ClientUiTransitionCode.Stopped);
            }

            if (!_registry.TryGetDefinition(routeId, out var definition) ||
                !_registry.TryGetHost(routeId, out var host))
            {
                return ClientUiTransitionResult.Rejected(ClientUiTransitionCode.NotRegistered);
            }

            if (!_planner.IsSceneBindingValid(definition, sceneGeneration))
            {
                return ClientUiTransitionResult.Rejected(
                    ClientUiTransitionCode.InvalidSceneGeneration);
            }

            if (_routeState.ActiveRoutes.Any(route => route.Definition.RouteId == routeId))
            {
                return ClientUiTransitionResult.UnchangedSuccess;
            }

            var replaced = _planner.FindConflictingOwner(
                _routeState.ActiveRoutes,
                definition);
            var previousInteractive =
                _interactionResolver.FindInteractive(_routeState.ActiveRoutes);
            var previousInput = _inputCoordinator.CurrentState;
            var generation = _routeState.NextGeneration();
            var pageCancellation = CancellationTokenSource.CreateLinkedTokenSource(
                _lifetimeCancellation.Token);
            using (var operationCancellation = CancellationTokenSource.CreateLinkedTokenSource(
                callerCancellation,
                _lifetimeCancellation.Token))
            {
                var operationToken = operationCancellation.Token;
                var cached = _routeState.CachedRoutes.TryGetValue(routeId, out var cachedRoute);
                if (cached)
                {
                    _routeState.CachedRoutes.Remove(routeId);
                }

                var candidate = new ClientUiActiveRoute(
                    definition,
                    host,
                    new ClientUiRouteBinding(routeId, generation, sceneGeneration, pageCancellation.Token),
                    pageCancellation);
                var nextRoutes = new List<ClientUiActiveRoute>(_routeState.ActiveRoutes);
                if (replaced != null)
                {
                    nextRoutes.Remove(replaced);
                }

                nextRoutes.Add(candidate);
                _interactionResolver.ApplyOwnership(nextRoutes);
                var progress = new ClientUiTransitionProgress();
                try
                {
                    await _transitionTransaction.PrepareOpenAsync(
                        candidate,
                        cached,
                        previousInteractive,
                        progress,
                        operationToken);

                    if (!CommitActiveRoutes(generation, nextRoutes, requireRunning: true))
                    {
                        await RollbackCandidateAsync(
                            candidate,
                            progress.InitializedByTransaction,
                            progress.Bound,
                            progress.Shown,
                            cachedRoute,
                            previousInteractive,
                            previousInput,
                            progress.PreviousFocus,
                            progress.OldInteractionSuspended,
                            restorePreviousOwner: false);
                        return ClientUiTransitionResult.Rejected(ClientUiTransitionCode.Stopped);
                    }
                }
                catch (OperationCanceledException) when (operationToken.IsCancellationRequested)
                {
                    var restorePreviousOwner = IsRunning();
                    await RollbackCandidateAsync(
                        candidate,
                        progress.InitializedByTransaction,
                        progress.Bound,
                        progress.Shown,
                        cachedRoute,
                        previousInteractive,
                        previousInput,
                        progress.PreviousFocus,
                        progress.OldInteractionSuspended,
                        restorePreviousOwner);
                    return restorePreviousOwner
                        ? ClientUiTransitionResult.Rejected(ClientUiTransitionCode.Cancelled)
                        : ClientUiTransitionResult.Rejected(ClientUiTransitionCode.Stopped);
                }
                catch (Exception)
                {
                    var restorePreviousOwner = IsRunning();
                    await RollbackCandidateAsync(
                        candidate,
                        progress.InitializedByTransaction,
                        progress.Bound,
                        progress.Shown,
                        cachedRoute,
                        previousInteractive,
                        previousInput,
                        progress.PreviousFocus,
                        progress.OldInteractionSuspended,
                        restorePreviousOwner);
                    return restorePreviousOwner
                        ? ClientUiTransitionResult.Rejected(ClientUiTransitionCode.HostFailure)
                        : ClientUiTransitionResult.Rejected(ClientUiTransitionCode.Stopped);
                }

                var cleanupErrors = new List<Exception>();
                if (replaced != null)
                {
                    await CleanupReplacedRouteAsync(replaced, cleanupErrors);
                }

                var notificationFailure = PublishSnapshot(CurrentSnapshot);
                return cleanupErrors.Count == 0 &&
                       !notificationFailure &&
                       !progress.FocusUnavailable
                    ? ClientUiTransitionResult.CommittedSuccess
                    : ClientUiTransitionResult.PostCommitFailure;
            }
        }

        /// <summary>
        /// 在已取得 transition gate 后执行 close transaction。
        /// </summary>
        /// <param name="routeId">待关闭 route。</param>
        /// <param name="callerCancellation">调用方取消。</param>
        /// <param name="publishSnapshot">是否在本次 close 后立即通知 subscriber。</param>
        /// <returns>稳定 transaction result。</returns>
        private async Task<ClientUiTransitionResult> CloseCoreAsync(
            ClientUiRouteId routeId,
            CancellationToken callerCancellation,
            bool publishSnapshot)
        {
            if (!IsRunning())
            {
                return ClientUiTransitionResult.Rejected(ClientUiTransitionCode.Stopped);
            }

            var closing = _routeState.ActiveRoutes.FirstOrDefault(route => route.Definition.RouteId == routeId);
            if (closing == null)
            {
                return ClientUiTransitionResult.UnchangedSuccess;
            }

            if (closing.Definition.Layer == ClientUiLayer.Modal &&
                !ReferenceEquals(
                    closing,
                    _interactionResolver.FindTop(
                        _routeState.ActiveRoutes,
                        ClientUiLayer.Modal)))
            {
                return ClientUiTransitionResult.Rejected(ClientUiTransitionCode.PolicyRejected);
            }

            var priorInput = _inputCoordinator.CurrentState;
            var priorFocus = closing.Host.CaptureFocus();
            var nextRoutes = new List<ClientUiActiveRoute>(_routeState.ActiveRoutes);
            nextRoutes.Remove(closing);
            _interactionResolver.ApplyOwnership(nextRoutes);
            var nextInteractive = _interactionResolver.FindInteractive(nextRoutes);
            var focusUnavailable = false;
            using (var operationCancellation = CancellationTokenSource.CreateLinkedTokenSource(
                callerCancellation,
                _lifetimeCancellation.Token))
            {
                var operationToken = operationCancellation.Token;
                try
                {
                    await closing.Host.SetInteractiveAsync(false, operationToken);
                    await closing.Host.HideAsync(operationToken);
                    await closing.Host.UnbindAsync(operationToken);
                    var nextInput = nextInteractive == null
                        ? ClientUiInputState.Gameplay
                        : _interactionResolver.CreateInputState(
                            nextInteractive.Definition);
                    await _inputCoordinator.ApplyAsync(nextInput, operationToken);
                    if (nextInteractive != null)
                    {
                        await nextInteractive.Host.SetInteractiveAsync(true, operationToken);
                        if (nextInteractive.Definition.InputMode != ClientUiInputMode.Gameplay)
                        {
                            var restored = await nextInteractive.Host.RestoreFocusAsync(
                                nextInteractive.SuspendedFocus,
                                operationToken);
                            if (!restored)
                            {
                                focusUnavailable =
                                    !await nextInteractive.Host.FocusDefaultAsync(operationToken);
                            }
                        }
                    }
                }
                catch (OperationCanceledException) when (operationToken.IsCancellationRequested)
                {
                    var restoreClosingRoute = IsRunning();
                    if (restoreClosingRoute)
                    {
                        await RestoreClosingRouteAsync(
                            closing,
                            nextInteractive,
                            priorInput,
                            priorFocus);
                    }

                    return restoreClosingRoute
                        ? ClientUiTransitionResult.Rejected(ClientUiTransitionCode.Cancelled)
                        : ClientUiTransitionResult.Rejected(ClientUiTransitionCode.Stopped);
                }
                catch (Exception)
                {
                    var restoreClosingRoute = IsRunning();
                    if (restoreClosingRoute)
                    {
                        await RestoreClosingRouteAsync(
                            closing,
                            nextInteractive,
                            priorInput,
                            priorFocus);
                    }

                    return restoreClosingRoute
                        ? ClientUiTransitionResult.Rejected(ClientUiTransitionCode.HostFailure)
                        : ClientUiTransitionResult.Rejected(ClientUiTransitionCode.Stopped);
                }
            }

            var cleanupErrors = new List<Exception>();
            TryCancel(closing.PageCancellation, cleanupErrors);
            var generation = _routeState.NextGeneration();
            if (!CommitActiveRoutes(generation, nextRoutes, requireRunning: true))
            {
                return ClientUiTransitionResult.Rejected(ClientUiTransitionCode.Stopped);
            }

            if (closing.Definition.Lifecycle == ClientUiLifecycle.Cached)
            {
                _routeState.CachedRoutes[closing.Definition.RouteId] =
                    new ClientUiCachedRoute(closing.Definition, closing.Host);
            }
            else
            {
                using (var cleanupCancellation = CreateCleanupCancellation())
                {
                    await TryCleanupAsync(
                        () => closing.Host.DisposeAsync(cleanupCancellation.Token),
                        cleanupErrors);
                }
            }

            closing.PageCancellation.Dispose();
            var notificationFailure = publishSnapshot && PublishSnapshot(CurrentSnapshot);
            return cleanupErrors.Count == 0 && !notificationFailure && !focusUnavailable
                ? ClientUiTransitionResult.CommittedSuccess
                : ClientUiTransitionResult.PostCommitFailure;
        }

        /// <summary>
        /// 有界登记并等待 transition gate；停止、容量和取消都映射为稳定结果。
        /// </summary>
        /// <param name="cancellationToken">调用方取消。</param>
        /// <returns>是否已经取得 gate 以及拒绝结果。</returns>
        private async Task<ClientUiTransitionAdmission> EnterTransitionAsync(CancellationToken cancellationToken)
        {
            CancellationToken lifetimeToken;
            lock (_sync)
            {
                if (!_running || _stopping || _stopped || _lifetimeCancellation == null)
                {
                    return ClientUiTransitionAdmission.Rejected(ClientUiTransitionCode.Stopped);
                }

                lifetimeToken = _lifetimeCancellation.Token;
            }

            return await _transitionQueue.EnterAsync(
                cancellationToken,
                lifetimeToken);
        }

        /// <summary>
        /// 在同一短锁内替换 active owner 并提交按 layer 和打开顺序投影的不可变 snapshot。
        /// </summary>
        /// <param name="generation">本次提交 generation。</param>
        /// <param name="routes">已完成交互裁决的 active routes。</param>
        /// <param name="requireRunning">是否在同一锁内拒绝与 stop 竞态的业务提交。</param>
        /// <returns>状态已提交时返回 true；stop 已抢先开始时返回 false。</returns>
        private bool CommitActiveRoutes(
            long generation,
            IReadOnlyList<ClientUiActiveRoute> routes,
            bool requireRunning)
        {
            var routeCopy = routes.ToArray();
            var ordered = routeCopy
                .Select((route, index) => new { route, index })
                .OrderBy(item => (int)item.route.Definition.Layer)
                .ThenBy(item => item.index)
                .Select(item => new ClientUiRouteSnapshotItem(
                    item.route.Definition,
                    item.route.Binding.NavigationGeneration,
                    item.route.Binding.SceneGeneration,
                    item.route.Interactive))
                .ToArray();
            var snapshot = new ClientUiRouteSnapshot(generation, ordered);
            lock (_sync)
            {
                if (requireRunning && (!_running || _stopping || _stopped))
                {
                    return false;
                }

                _routeState.ActiveRoutes.Clear();
                _routeState.ActiveRoutes.AddRange(routeCopy);
                _routeState.Snapshot = snapshot;
                ClientUiDiagnostics.Trace(
                    nameof(ClientUiRouter),
                    "snapshot_committed",
                    $"navigation_generation={generation} routes={FormatSnapshot(snapshot)}");
                return true;
            }
        }

        /// <summary>把 route snapshot 投影为不含业务 identity 的紧凑诊断字段。</summary>
        /// <param name="snapshot">已经提交的不可变 route snapshot。</param>
        /// <returns>按 layer 排序的 route 与交互 owner 标记。</returns>
        private static string FormatSnapshot(ClientUiRouteSnapshot snapshot)
        {
            return string.Join(
                ",",
                snapshot.Items.Select(item =>
                    item.Interactive
                        ? $"{item.Definition.RouteId}:interactive"
                        : item.Definition.RouteId.ToString()));
        }

        /// <summary>
        /// 在锁外逐个通知 snapshot subscriber，并把任一异常映射为可观察的提交后失败。
        /// </summary>
        /// <param name="snapshot">已经提交的不可变 snapshot。</param>
        /// <returns>至少一个 subscriber 失败时返回 true。</returns>
        private bool PublishSnapshot(ClientUiRouteSnapshot snapshot)
        {
            Action<ClientUiRouteSnapshot> subscribers;
            lock (_sync)
            {
                subscribers = _snapshotChanged;
            }
            if (subscribers == null)
            {
                return false;
            }

            var failed = false;
            foreach (Action<ClientUiRouteSnapshot> subscriber in subscribers.GetInvocationList())
            {
                try
                {
                    subscriber(snapshot);
                }
                catch (Exception)
                {
                    // 已提交 snapshot 不回滚；返回值确保异常不会被误报为普通成功。
                    failed = true;
                }
            }

            return failed;
        }

        /// <summary>
        /// 判断 router 当前是否仍允许新或正在执行的 transition 提交。
        /// </summary>
        /// <returns>处于 Running 且尚未停止时返回 true。</returns>
        private bool IsRunning()
        {
            lock (_sync)
            {
                return _running && !_stopping && !_stopped;
            }
        }

        /// <summary>
        /// 回滚尚未提交的 candidate，并恢复被暂停 owner 的 input/focus。
        /// </summary>
        /// <param name="candidate">待回滚 candidate。</param>
        /// <param name="initializedByTransaction">是否由本次 transaction 初始化。</param>
        /// <param name="bound">是否已经 bind。</param>
        /// <param name="shown">是否已经 show。</param>
        /// <param name="cachedRoute">candidate 是否来源于 cache。</param>
        /// <param name="previousInteractive">提交前交互 owner。</param>
        /// <param name="previousInput">提交前输入状态。</param>
        /// <param name="previousFocus">提交前 focus token。</param>
        /// <param name="oldInteractionSuspended">旧 owner 是否已暂停交互。</param>
        /// <param name="restorePreviousOwner">router 仍运行时才恢复旧 owner；stop 已开始时只清理 candidate。</param>
        /// <returns>全部可执行回滚均已尝试时完成的任务。</returns>
        private async Task RollbackCandidateAsync(
            ClientUiActiveRoute candidate,
            bool initializedByTransaction,
            bool bound,
            bool shown,
            ClientUiCachedRoute cachedRoute,
            ClientUiActiveRoute previousInteractive,
            ClientUiInputState previousInput,
            ClientUiFocusToken previousFocus,
            bool oldInteractionSuspended,
            bool restorePreviousOwner)
        {
            var errors = new List<Exception>();
            using (var cleanupCancellation = CreateCleanupCancellation())
            {
                var cleanupToken = cleanupCancellation.Token;
                TryCancel(candidate.PageCancellation, errors);
                if (shown)
                {
                    await TryCleanupAsync(
                        () => candidate.Host.SetInteractiveAsync(false, cleanupToken),
                        errors);
                    await TryCleanupAsync(
                        () => candidate.Host.HideAsync(cleanupToken),
                        errors);
                }

                if (bound)
                {
                    await TryCleanupAsync(
                        () => candidate.Host.UnbindAsync(cleanupToken),
                        errors);
                }

                if (initializedByTransaction)
                {
                    await TryCleanupAsync(
                        () => candidate.Host.DisposeAsync(cleanupToken),
                        errors);
                }
                else if (cachedRoute != null && errors.Count == 0)
                {
                    _routeState.CachedRoutes[candidate.Definition.RouteId] = cachedRoute;
                }
                else if (cachedRoute != null)
                {
                    // 清理未完整完成的 cached Host 不再复用，避免下次 open 继承残留 binding 或可见状态。
                    await TryCleanupAsync(
                        () => candidate.Host.DisposeAsync(cleanupToken),
                        errors);
                }

                if (restorePreviousOwner)
                {
                    await TryCleanupAsync(
                        () => _inputCoordinator.ApplyAsync(previousInput, cleanupToken),
                        errors);
                }

                if (restorePreviousOwner && previousInteractive != null && oldInteractionSuspended)
                {
                    await TryCleanupAsync(
                        () => previousInteractive.Host.SetInteractiveAsync(true, cleanupToken),
                        errors);
                    if (previousInput.Mode != ClientUiInputMode.Gameplay)
                    {
                        var restored = false;
                        try
                        {
                            restored = await previousInteractive.Host.RestoreFocusAsync(
                                previousFocus,
                                cleanupToken);
                        }
                        catch (Exception error)
                        {
                            // 回滚继续尝试 default focus，原始 transaction 结果保持 HostFailure。
                            errors.Add(error);
                        }

                        if (!restored)
                        {
                            await TryCleanupAsync(
                                async () =>
                                {
                                    await previousInteractive.Host.FocusDefaultAsync(cleanupToken);
                                },
                                errors);
                        }
                    }
                }
            }

            candidate.PageCancellation.Dispose();
        }

        /// <summary>
        /// 提交替换后清理旧 route，并按 lifecycle 缓存或 dispose Host。
        /// </summary>
        /// <param name="replaced">已从 active snapshot 移除的旧 owner。</param>
        /// <param name="errors">收集提交后清理错误。</param>
        /// <returns>全部清理均已尝试时完成的任务。</returns>
        private async Task CleanupReplacedRouteAsync(ClientUiActiveRoute replaced, IList<Exception> errors)
        {
            var errorCountBeforeCleanup = errors.Count;
            using (var cleanupCancellation = CreateCleanupCancellation())
            {
                var cleanupToken = cleanupCancellation.Token;
                TryCancel(replaced.PageCancellation, errors);
                await TryCleanupAsync(
                    () => replaced.Host.SetInteractiveAsync(false, cleanupToken),
                    errors);
                await TryCleanupAsync(
                    () => replaced.Host.HideAsync(cleanupToken),
                    errors);
                await TryCleanupAsync(
                    () => replaced.Host.UnbindAsync(cleanupToken),
                    errors);
                if (replaced.Definition.Lifecycle == ClientUiLifecycle.Cached &&
                    errors.Count == errorCountBeforeCleanup)
                {
                    _routeState.CachedRoutes[replaced.Definition.RouteId] =
                        new ClientUiCachedRoute(replaced.Definition, replaced.Host);
                }
                else
                {
                    await TryCleanupAsync(
                        () => replaced.Host.DisposeAsync(cleanupToken),
                        errors);
                }

            }

            replaced.PageCancellation.Dispose();
        }

        /// <summary>
        /// Close 提交前失败时尽力重新显示并恢复原 route。
        /// </summary>
        /// <param name="closing">仍属于当前 snapshot 的 route。</param>
        /// <param name="temporarilyInteractive">关闭过程中临时取得交互、回滚时必须重新禁用的 route。</param>
        /// <param name="priorInput">关闭前输入状态。</param>
        /// <param name="priorFocus">关闭前 focus token。</param>
        /// <returns>全部恢复动作均已尝试时完成的任务。</returns>
        private async Task RestoreClosingRouteAsync(
            ClientUiActiveRoute closing,
            ClientUiActiveRoute temporarilyInteractive,
            ClientUiInputState priorInput,
            ClientUiFocusToken priorFocus)
        {
            var errors = new List<Exception>();
            using (var cleanupCancellation = CreateCleanupCancellation())
            {
                var cleanupToken = cleanupCancellation.Token;
                if (temporarilyInteractive != null)
                {
                    await TryCleanupAsync(
                        () => temporarilyInteractive.Host.SetInteractiveAsync(false, cleanupToken),
                        errors);
                }

                await TryCleanupAsync(
                    () => closing.Host.BindAsync(closing.Binding, cleanupToken),
                    errors);
                await TryCleanupAsync(
                    () => closing.Host.ShowAsync(closing.Definition.Layer, cleanupToken),
                    errors);
                await TryCleanupAsync(
                    () => _inputCoordinator.ApplyAsync(priorInput, cleanupToken),
                    errors);
                await TryCleanupAsync(
                    () => closing.Host.SetInteractiveAsync(true, cleanupToken),
                    errors);
                if (priorInput.Mode != ClientUiInputMode.Gameplay)
                {
                    var restored = false;
                    try
                    {
                        restored = await closing.Host.RestoreFocusAsync(priorFocus, cleanupToken);
                    }
                    catch (Exception error)
                    {
                        // 原 close failure 保持主结果，继续尝试 default focus。
                        errors.Add(error);
                    }

                    if (!restored)
                    {
                        await TryCleanupAsync(
                            async () =>
                            {
                                await closing.Host.FocusDefaultAsync(cleanupToken);
                            },
                            errors);
                    }
                }
            }
        }

        /// <summary>
        /// 在 App stop 中逆序清理单个 active route，并聚合而不短路后续资源。
        /// </summary>
        /// <param name="route">待清理 active route。</param>
        /// <param name="cancellationToken">共享清理 deadline。</param>
        /// <param name="errors">聚合清理错误。</param>
        /// <returns>该 route 全部清理动作已尝试时完成的任务。</returns>
        private static async Task CleanupRouteForStopAsync(
            ClientUiActiveRoute route,
            CancellationToken cancellationToken,
            IList<Exception> errors)
        {
            TryCancel(route.PageCancellation, errors);
            await TryCleanupAsync(
                () => route.Host.SetInteractiveAsync(false, cancellationToken),
                errors);
            await TryCleanupAsync(() => route.Host.HideAsync(cancellationToken), errors);
            await TryCleanupAsync(() => route.Host.UnbindAsync(cancellationToken), errors);
            await TryCleanupAsync(() => route.Host.DisposeAsync(cancellationToken), errors);
            route.PageCancellation.Dispose();
        }

        /// <summary>
        /// 执行单个清理动作并把错误加入聚合集合。
        /// </summary>
        /// <param name="action">待执行异步清理。</param>
        /// <param name="errors">聚合错误集合。</param>
        /// <returns>动作成功或错误已记录时完成的任务。</returns>
        private static async Task TryCleanupAsync(Func<Task> action, IList<Exception> errors)
        {
            try
            {
                await action();
            }
            catch (Exception error)
            {
                errors.Add(error);
            }
        }

        /// <summary>
        /// 取消页面或 App lifetime，并把恶意或错误 cancellation callback 映射为清理失败。
        /// </summary>
        /// <param name="source">待取消的 token source；为空时无动作。</param>
        /// <param name="errors">接收 callback 聚合异常的集合。</param>
        private static void TryCancel(CancellationTokenSource source, IList<Exception> errors)
        {
            if (source == null)
            {
                return;
            }

            try
            {
                source.Cancel();
            }
            catch (Exception error)
            {
                errors.Add(error);
            }
        }

        /// <summary>创建不受原 navigation caller 取消影响的有界清理 token source。</summary>
        /// <returns>由调用方释放的 cleanup deadline owner。</returns>
        private CancellationTokenSource CreateCleanupCancellation()
        {
            return new CancellationTokenSource(_cleanupTimeout);
        }

    }
}
