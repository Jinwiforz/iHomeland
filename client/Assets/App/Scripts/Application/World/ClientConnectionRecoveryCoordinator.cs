using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Control;
using IHomeland.Client.Application.Gameplay;
using IHomeland.Client.Application.Ports;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Foundation.Lifetime;

namespace IHomeland.Client.Application.World
{
    /// <summary>
    /// 线性化WSS control与gameplay target的automatic/manual恢复意图。
    /// </summary>
    /// <remarks>
    /// 本owner只保存generation、阶段与冻结descriptor；Session、World、Visit和Scene最终事实仍由既有
    /// owner持有。所有成功提交同时验证session、intent与target，Scene由表现层显式确认第四重gate。
    /// </remarks>
    internal sealed class ClientConnectionRecoveryCoordinator : IAppLifetimeParticipant
    {
        /// <summary>保护snapshot、intent task与lifetime owner。</summary>
        private readonly object _sync = new object();

        /// <summary>读取current Session generation。</summary>
        private readonly SessionCoordinator _sessionCoordinator;

        /// <summary>发布generation-bound WSS健康状态。</summary>
        private readonly IClientControlChannelPort _controlChannel;

        /// <summary>发布已完成资源清理的gameplay非预期终态。</summary>
        private readonly IClientGameplayChannelPort _gameplayChannel;

        /// <summary>执行窄权威恢复操作。</summary>
        private readonly IClientConnectionRecoveryOperations _operations;

        /// <summary>纯计算恢复 phase single-flight 与迟到提交。</summary>
        private readonly ConnectionRecoveryStateMachine _stateMachine =
            new ConnectionRecoveryStateMachine();

        /// <summary>编排 Control reconciliation。</summary>
        private readonly RecoverControlFlow _recoverControlFlow =
            new RecoverControlFlow();

        /// <summary>编排 Gameplay target recovery。</summary>
        private readonly RecoverGameplayFlow _recoverGameplayFlow =
            new RecoverGameplayFlow();

        /// <summary>每笔恢复intent的总deadline。</summary>
        private readonly TimeSpan _deadline;

        /// <summary>创建总deadline token；测试可显式推进而不使用真实sleep。</summary>
        private readonly Func<TimeSpan, CancellationTokenSource> _deadlineFactory;

        /// <summary>App Scope停止信号。</summary>
        private CancellationTokenSource _lifetimeCancellation;

        /// <summary>当前唯一world/control reconciliation task。</summary>
        private Task _activeTask;

        /// <summary>覆盖control、world与Scene gate整笔intent的唯一总deadline owner。</summary>
        private CancellationTokenSource _intentDeadlineCancellation;

        /// <summary>等待Scene commit时把同一总deadline转换为terminal提交。</summary>
        private CancellationTokenRegistration _sceneDeadlineRegistration;

        /// <summary>只在active或等待Scene commit时保存冻结target。</summary>
        private ClientRecoveryTargetDescriptor _activeTarget;

        /// <summary>Control先恢复时暂存已终止gameplay generation，确保成功收敛后继续恢复world。</summary>
        private long _pendingWorldSourceGeneration;

        /// <summary>每笔恢复递增，拒绝旧completion。</summary>
        private long _intentGeneration;

        /// <summary>是否已经登记typed channel lifecycle事件。</summary>
        private bool _subscribed;

        /// <summary>当前不可变恢复状态。</summary>
        private ClientConnectionRecoverySnapshot _snapshot =
            new ClientConnectionRecoverySnapshot(
                ClientConnectionRecoveryPhase.Idle,
                0,
                0,
                0,
                ClientConnectionRecoveryResultKind.None,
                manual: false);

        /// <summary>创建唯一恢复owner。</summary>
        /// <param name="sessionCoordinator">唯一Session owner。</param>
        /// <param name="controlChannel">唯一WSS control owner。</param>
        /// <param name="gameplayChannel">唯一TLS/TCP gameplay owner。</param>
        /// <param name="operations">窄恢复操作adapter。</param>
        /// <param name="deadline">每笔恢复的正总预算。</param>
        internal ClientConnectionRecoveryCoordinator(
            SessionCoordinator sessionCoordinator,
            IClientControlChannelPort controlChannel,
            IClientGameplayChannelPort gameplayChannel,
            IClientConnectionRecoveryOperations operations,
            TimeSpan deadline)
            : this(sessionCoordinator, operations, deadline)
        {
            _controlChannel = controlChannel ?? throw new ArgumentNullException(nameof(controlChannel));
            _gameplayChannel = gameplayChannel ?? throw new ArgumentNullException(nameof(gameplayChannel));
        }

        /// <summary>创建不订阅真实channel的纯状态机测试实例。</summary>
        /// <param name="sessionCoordinator">唯一Session owner。</param>
        /// <param name="operations">可控窄恢复操作。</param>
        /// <param name="deadline">每笔恢复的正总预算。</param>
        internal ClientConnectionRecoveryCoordinator(
            SessionCoordinator sessionCoordinator,
            IClientConnectionRecoveryOperations operations,
            TimeSpan deadline)
            : this(
                sessionCoordinator,
                operations,
                deadline,
                value => new CancellationTokenSource(value))
        {
        }

        /// <summary>创建具有可控deadline seam的纯状态机测试实例。</summary>
        /// <param name="sessionCoordinator">唯一Session owner。</param>
        /// <param name="operations">可控窄恢复操作。</param>
        /// <param name="deadline">每笔恢复的正总预算。</param>
        /// <param name="deadlineFactory">创建单笔deadline cancellation owner。</param>
        internal ClientConnectionRecoveryCoordinator(
            SessionCoordinator sessionCoordinator,
            IClientConnectionRecoveryOperations operations,
            TimeSpan deadline,
            Func<TimeSpan, CancellationTokenSource> deadlineFactory)
        {
            _sessionCoordinator = sessionCoordinator ??
                throw new ArgumentNullException(nameof(sessionCoordinator));
            _operations = operations ?? throw new ArgumentNullException(nameof(operations));
            if (deadline <= TimeSpan.Zero)
            {
                throw new ArgumentException("Connection recovery deadline必须为正数。", nameof(deadline));
            }

            _deadline = deadline;
            _deadlineFactory = deadlineFactory ?? throw new ArgumentNullException(nameof(deadlineFactory));
        }

        /// <summary>在不可变恢复状态提交后通知表现owner。</summary>
        internal event Action<ClientConnectionRecoverySnapshot> Changed;

        /// <summary>获取current恢复状态。</summary>
        internal ClientConnectionRecoverySnapshot Snapshot
        {
            get
            {
                lock (_sync)
                {
                    return _snapshot;
                }
            }
        }

#if DEVELOPMENT_BUILD || UNITY_EDITOR
        /// <summary>获取资格运行可观察的current恢复intent owner数量。</summary>
        internal int QualificationIntentOwnerCount
        {
            get
            {
                lock (_sync)
                {
                    return _snapshot.Phase == ClientConnectionRecoveryPhase.RecoveringControl ||
                           _snapshot.Phase == ClientConnectionRecoveryPhase.RecoveringWorld ||
                           _snapshot.Phase == ClientConnectionRecoveryPhase.AwaitingSceneCommit
                        ? 1
                        : 0;
                }
            }
        }

        /// <summary>获取资格范围内恢复状态subscriber数量。</summary>
        internal int QualificationSubscriptionCount
        {
            get
            {
                lock (_sync)
                {
                    return Changed?.GetInvocationList().Length ?? 0;
                }
            }
        }
#endif

        /// <summary>启用恢复入口但不创建网络副作用。</summary>
        /// <param name="cancellationToken">AppLifetime初始化信号。</param>
        /// <returns>本地初始化完成。</returns>
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            lock (_sync)
            {
                if (_lifetimeCancellation != null ||
                    _snapshot.Phase == ClientConnectionRecoveryPhase.Stopped)
                {
                    throw new InvalidOperationException(
                        "ClientConnectionRecoveryCoordinator不能重复初始化或停止后重启。");
                }

                _lifetimeCancellation = new CancellationTokenSource();
                if (_controlChannel != null && _gameplayChannel != null)
                {
                    _controlChannel.HealthChanged += OnControlHealthChanged;
                    _gameplayChannel.UnexpectedDisconnect += OnGameplayUnexpectedDisconnect;
                    _subscribed = true;
                }
            }

            return Task.CompletedTask;
        }

        /// <summary>记录control进入adapter-owned有限恢复并冻结control-only能力。</summary>
        /// <param name="controlGeneration">发生瞬时故障的control run generation。</param>
        internal void BeginControlRecovery(long controlGeneration)
        {
            BeginControlRecovery(controlGeneration, manual: false);
        }

        /// <summary>记录一笔automatic或manual control恢复，并冻结control-only能力。</summary>
        /// <param name="controlGeneration">发生故障或新run开始时的control generation。</param>
        /// <param name="manual">是否由terminal状态下的玩家显式发起。</param>
        private bool BeginControlRecovery(long controlGeneration, bool manual)
        {
            if (controlGeneration <= 0)
            {
                return false;
            }

            ClientConnectionRecoverySnapshot committed = null;
            long intent = 0;
            lock (_sync)
            {
                if (!CanBeginLocked() ||
                    !_stateMachine.CanBeginControl(_snapshot, manual))
                {
                    return false;
                }

                if (_snapshot.Phase == ClientConnectionRecoveryPhase.RecoveringControl &&
                    (_snapshot.SourceChannelGeneration == controlGeneration ||
                     _activeTask != null && !_activeTask.IsCompleted))
                {
                    return false;
                }

                _intentGeneration++;
                intent = _intentGeneration;
                _pendingWorldSourceGeneration = 0;
                if (!TryStartIntentDeadlineLocked())
                {
                    _snapshot = new ClientConnectionRecoverySnapshot(
                        ClientConnectionRecoveryPhase.ConnectionLost,
                        intent,
                        controlGeneration,
                        0,
                        ClientConnectionRecoveryResultKind.Internal,
                        manual);
                }
                else
                {
                    _snapshot = new ClientConnectionRecoverySnapshot(
                        ClientConnectionRecoveryPhase.RecoveringControl,
                        intent,
                        controlGeneration,
                        0,
                        ClientConnectionRecoveryResultKind.None,
                        manual);
                }
                committed = _snapshot;
            }

            if (committed.Phase == ClientConnectionRecoveryPhase.ConnectionLost)
            {
                Notify(committed);
                return false;
            }

            try
            {
                _operations.InvalidateControlOnlyState();
            }
            catch (Exception)
            {
                committed = CommitTerminal(
                    intent,
                    ClientConnectionRecoveryPhase.RecoveringControl,
                    ClientConnectionRecoveryResultKind.Internal);
            }

            Notify(committed);
            return committed.Phase == ClientConnectionRecoveryPhase.RecoveringControl;
        }

        /// <summary>在新control generation Connected后启动一次完整snapshot收敛。</summary>
        /// <param name="controlGeneration">新Connected run generation。</param>
        /// <returns>收敛完成或被更高优先级intent替换时结束。</returns>
        internal Task CompleteControlRecoveryAsync(long controlGeneration)
        {
            long expectedIntent;
            lock (_sync)
            {
                if (_snapshot.Phase != ClientConnectionRecoveryPhase.RecoveringControl ||
                    controlGeneration < _snapshot.SourceChannelGeneration)
                {
                    return Task.CompletedTask;
                }

                if (_activeTask != null && !_activeTask.IsCompleted)
                {
                    return _activeTask;
                }

                expectedIntent = _snapshot.IntentGeneration;
            }

            ClientRecoveryTargetDescriptor target;
            try
            {
                if (!_operations.TryCaptureTarget(out target))
                {
                    var withoutTarget = CommitControlWithoutTarget(
                        expectedIntent,
                        controlGeneration);
                    Notify(withoutTarget);
                    return Task.CompletedTask;
                }
            }
            catch (Exception)
            {
                var terminal = CommitTerminal(
                    expectedIntent,
                    ClientConnectionRecoveryPhase.RecoveringControl,
                    ClientConnectionRecoveryResultKind.Internal);
                Notify(terminal);
                return Task.CompletedTask;
            }

            Task activeTask;
            ClientConnectionRecoverySnapshot committed = null;
            lock (_sync)
            {
                if (_snapshot.Phase != ClientConnectionRecoveryPhase.RecoveringControl ||
                    _snapshot.IntentGeneration != expectedIntent ||
                    controlGeneration < _snapshot.SourceChannelGeneration)
                {
                    return Task.CompletedTask;
                }

                if (_activeTask != null && !_activeTask.IsCompleted)
                {
                    return _activeTask;
                }

                var intent = _snapshot.IntentGeneration;
                _activeTarget = target;
                if (_snapshot.Manual &&
                    (_gameplayChannel == null ||
                     !_gameplayChannel.Snapshot.Active))
                {
                    // 整服故障时control会先于gameplay恢复；此时不能用control-only投影校验
                    // 要求尚未重建的gameplay已经Active，而应在同一intent内继续恢复冻结world target。
                    _pendingWorldSourceGeneration = 0;
                    _snapshot = new ClientConnectionRecoverySnapshot(
                        ClientConnectionRecoveryPhase.RecoveringWorld,
                        intent,
                        controlGeneration,
                        target.TargetGeneration,
                        ClientConnectionRecoveryResultKind.None,
                        manual: true);
                    committed = _snapshot;
                    _activeTask = CompleteWorldCoreAsync(
                        intent,
                        controlGeneration,
                        target,
                        manual: true);
                }
                else
                {
                    _activeTask = CompleteControlCoreAsync(intent, controlGeneration, target);
                }

                activeTask = _activeTask;
            }

            Notify(committed);
            return activeTask;
        }

        /// <summary>使尚未进入snapshot reconciliation的control恢复稳定失败。</summary>
        private void FailControlRecovery(
            long controlGeneration,
            ClientConnectionRecoveryResultKind result)
        {
            ClientConnectionRecoverySnapshot committed = null;
            lock (_sync)
            {
                if (_snapshot.Phase == ClientConnectionRecoveryPhase.RecoveringControl &&
                    controlGeneration == _snapshot.SourceChannelGeneration &&
                    (_activeTask == null || _activeTask.IsCompleted))
                {
                    committed = CommitTerminalLocked(result);
                }
            }

            Notify(committed);
        }

        /// <summary>为gameplay非预期断开创建唯一automatic恢复intent。</summary>
        /// <param name="gameplayGeneration">已完成关闭的gameplay generation。</param>
        /// <returns>成功取得intent时返回true。</returns>
        internal bool BeginAutomaticWorldRecovery(long gameplayGeneration)
        {
            return BeginWorldRecovery(gameplayGeneration, manual: false);
        }

        /// <summary>只在稳定ConnectionLost后创建下一代manual恢复intent。</summary>
        /// <returns>成功取得唯一manual intent时返回true。</returns>
        internal bool BeginManualWorldRecovery()
        {
            long sourceGeneration;
            lock (_sync)
            {
                if (_snapshot.Phase != ClientConnectionRecoveryPhase.ConnectionLost)
                {
                    return false;
                }

                sourceGeneration = _snapshot.SourceChannelGeneration;
            }

            return BeginWorldRecovery(sourceGeneration, manual: true);
        }

        /// <summary>
        /// 在稳定ConnectionLost后取得唯一manual intent；control健康时恢复冻结target，断开时先启动新run。
        /// </summary>
        /// <returns>成功取得manual single-flight时返回true。</returns>
        internal bool BeginManualRecovery()
        {
            CancellationToken lifetime;
            lock (_sync)
            {
                if (_snapshot.Phase != ClientConnectionRecoveryPhase.ConnectionLost ||
                    !CanBeginLocked())
                {
                    return false;
                }

                lifetime = _lifetimeCancellation.Token;
            }

            if (_controlChannel == null ||
                _controlChannel.Snapshot.Connected)
            {
                return BeginManualWorldRecovery();
            }

            Task runTask;
            try
            {
                runTask = _controlChannel.RunAsync(lifetime);
            }
            catch (InvalidOperationException)
            {
                return false;
            }
            catch (OperationCanceledException)
            {
                return false;
            }

            var controlGeneration = _controlChannel.Snapshot.Generation;
            var accepted = BeginManualControlRecovery(controlGeneration);
            if (accepted)
            {
                _ = ObserveManualControlRunAsync(runTask, controlGeneration);
            }

            return accepted;
        }

        /// <summary>提交已启动control run对应的manual恢复意图。</summary>
        /// <param name="controlGeneration">新control run的generation。</param>
        /// <returns>成功取得manual single-flight时返回true。</returns>
        internal bool BeginManualControlRecovery(long controlGeneration)
        {
            return BeginControlRecovery(controlGeneration, manual: true);
        }

        /// <summary>等待指定intent进入Idle、AwaitingSceneCommit、ConnectionLost或Stopped。</summary>
        /// <param name="intentGeneration">Begin调用同步提交的intent generation。</param>
        /// <param name="cancellationToken">只取消调用方等待，不取消coordinator owner。</param>
        /// <returns>匹配intent的最新稳定snapshot。</returns>
        internal async Task<ClientConnectionRecoverySnapshot> WaitForSettledAsync(
            long intentGeneration,
            CancellationToken cancellationToken)
        {
            var completion = new TaskCompletionSource<ClientConnectionRecoverySnapshot>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            Action<ClientConnectionRecoverySnapshot> handler = snapshot =>
            {
                if (snapshot.IntentGeneration == intentGeneration && IsSettled(snapshot.Phase))
                {
                    completion.TrySetResult(snapshot);
                }
            };

            Changed += handler;
            try
            {
                handler(Snapshot);
                if (!cancellationToken.CanBeCanceled)
                {
                    return await completion.Task;
                }

                using (cancellationToken.Register(() => completion.TrySetCanceled()))
                {
                    return await completion.Task;
                }
            }
            finally
            {
                Changed -= handler;
            }
        }

        /// <summary>由Scene owner确认target与Scene generation均已提交。</summary>
        /// <param name="intentGeneration">触发Scene同步的恢复intent。</param>
        /// <param name="targetGeneration">Scene绑定的current target generation。</param>
        /// <returns>四重gate全部匹配并回到Idle时返回true。</returns>
        internal bool ConfirmSceneCommit(long intentGeneration, long targetGeneration)
        {
            ClientConnectionRecoverySnapshot committed;
            lock (_sync)
            {
                if (_snapshot.Phase != ClientConnectionRecoveryPhase.AwaitingSceneCommit ||
                    _snapshot.IntentGeneration != intentGeneration ||
                    _snapshot.TargetGeneration != targetGeneration)
                {
                    return false;
                }

                _activeTarget = null;
                _pendingWorldSourceGeneration = 0;
                ReleaseIntentDeadlineLocked();
                _snapshot = new ClientConnectionRecoverySnapshot(
                    ClientConnectionRecoveryPhase.Idle,
                    intentGeneration,
                    _snapshot.SourceChannelGeneration,
                    targetGeneration,
                    ClientConnectionRecoveryResultKind.Succeeded,
                    _snapshot.Manual);
                committed = _snapshot;
            }

            Notify(committed);
            return true;
        }

        /// <summary>在Scene或HUD无法提交时使current恢复稳定失败。</summary>
        /// <param name="intentGeneration">触发Scene同步的恢复intent。</param>
        /// <param name="targetGeneration">本次Scene绑定的target generation。</param>
        /// <param name="result">Dependency、deadline或internal稳定失败。</param>
        /// <returns>仅current四重gate匹配时返回true。</returns>
        internal bool FailSceneCommit(
            long intentGeneration,
            long targetGeneration,
            ClientConnectionRecoveryResultKind result)
        {
            if (result == ClientConnectionRecoveryResultKind.None ||
                result == ClientConnectionRecoveryResultKind.Succeeded ||
                result == ClientConnectionRecoveryResultKind.ReturningOwnWorld)
            {
                throw new ArgumentOutOfRangeException(nameof(result));
            }

            ClientConnectionRecoverySnapshot committed;
            lock (_sync)
            {
                if (_snapshot.Phase != ClientConnectionRecoveryPhase.AwaitingSceneCommit ||
                    _snapshot.IntentGeneration != intentGeneration ||
                    _snapshot.TargetGeneration != targetGeneration)
                {
                    return false;
                }

                committed = CommitTerminalLocked(result);
            }

            Notify(committed);
            return true;
        }

        /// <summary>停止新intent、取消current恢复并等待唯一owner退出。</summary>
        /// <param name="cancellationToken">AppLifetime停止deadline。</param>
        /// <returns>Active task退出并提交Stopped时完成。</returns>
        public async Task StopAsync(CancellationToken cancellationToken)
        {
            Task active;
            CancellationTokenSource lifetime;
            CancellationTokenSource intentDeadline;
            ClientConnectionRecoverySnapshot committed;
            lock (_sync)
            {
                if (_snapshot.Phase == ClientConnectionRecoveryPhase.Stopped)
                {
                    return;
                }

                _intentGeneration++;
                lifetime = _lifetimeCancellation;
                intentDeadline = _intentDeadlineCancellation;
                _intentDeadlineCancellation = null;
                _sceneDeadlineRegistration.Dispose();
                _sceneDeadlineRegistration = default;
                active = _activeTask;
                if (_subscribed)
                {
                    _controlChannel.HealthChanged -= OnControlHealthChanged;
                    _gameplayChannel.UnexpectedDisconnect -= OnGameplayUnexpectedDisconnect;
                    _subscribed = false;
                }
                _activeTarget = null;
                _snapshot = new ClientConnectionRecoverySnapshot(
                    ClientConnectionRecoveryPhase.Stopped,
                    _intentGeneration,
                    _snapshot.SourceChannelGeneration,
                    0,
                    ClientConnectionRecoveryResultKind.Stopped,
                    manual: false);
                committed = _snapshot;
            }

            lifetime?.Cancel();
            intentDeadline?.Cancel();
            Notify(committed);
            if (active != null)
            {
                await AwaitWithCancellationAsync(active, cancellationToken);
            }

            lifetime?.Dispose();
            intentDeadline?.Dispose();
        }

        /// <summary>创建automatic或manual world恢复task。</summary>
        /// <param name="sourceGeneration">触发恢复的gameplay generation。</param>
        /// <param name="manual">是否为terminal后的玩家intent。</param>
        /// <returns>成功取得single-flight时返回true。</returns>
        private bool BeginWorldRecovery(long sourceGeneration, bool manual)
        {
            ClientRecoveryTargetDescriptor target;
            ClientSessionSnapshot session;
            try
            {
                if (!_operations.TryCaptureTarget(out target) ||
                    !_sessionCoordinator.TryGetCurrent(out session))
                {
                    return false;
                }
            }
            catch (Exception)
            {
                return false;
            }

            ClientConnectionRecoverySnapshot committed;
            lock (_sync)
            {
                if (!CanBeginLocked() ||
                    !_stateMachine.CanBeginGameplay(_snapshot, manual) ||
                    !target.IsBoundTo(session))
                {
                    return false;
                }

                if (!manual &&
                    _snapshot.Phase == ClientConnectionRecoveryPhase.RecoveringControl)
                {
                    if (_pendingWorldSourceGeneration != 0)
                    {
                        return false;
                    }

                    _pendingWorldSourceGeneration = sourceGeneration;
                    return true;
                }

                if (_activeTask != null && !_activeTask.IsCompleted)
                {
                    return false;
                }

                _intentGeneration++;
                _pendingWorldSourceGeneration = 0;
                _activeTarget = target;
                if (!TryStartIntentDeadlineLocked())
                {
                    _activeTarget = null;
                    _snapshot = new ClientConnectionRecoverySnapshot(
                        ClientConnectionRecoveryPhase.ConnectionLost,
                        _intentGeneration,
                        sourceGeneration,
                        target.TargetGeneration,
                        ClientConnectionRecoveryResultKind.Internal,
                        manual);
                    committed = _snapshot;
                }
                else
                {
                    _snapshot = new ClientConnectionRecoverySnapshot(
                        ClientConnectionRecoveryPhase.RecoveringWorld,
                        _intentGeneration,
                        sourceGeneration,
                        target.TargetGeneration,
                        ClientConnectionRecoveryResultKind.None,
                        manual);
                    committed = _snapshot;
                    _activeTask = CompleteWorldCoreAsync(
                        _intentGeneration,
                        sourceGeneration,
                        target,
                        manual);
                }
            }

            Notify(committed);
            return true;
        }

        /// <summary>执行control snapshot reconciliation并拒绝迟到completion。</summary>
        private async Task CompleteControlCoreAsync(
            long intent,
            long controlGeneration,
            ClientRecoveryTargetDescriptor target)
        {
            var result = await ExecuteBoundedAsync(
                token => _recoverControlFlow.ExecuteAsync(
                    _operations,
                    target,
                    token));
            ClientConnectionRecoverySnapshot committed;
            Task continuedWorldRecovery = null;
            lock (_sync)
            {
                if (!IsCurrentIntentLocked(intent, ClientConnectionRecoveryPhase.RecoveringControl))
                {
                    return;
                }

                var manual = _snapshot.Manual;
                var queuedWorldGeneration = _pendingWorldSourceGeneration;
                _pendingWorldSourceGeneration = 0;
                if (result == ClientConnectionRecoveryResultKind.Succeeded &&
                    (manual || queuedWorldGeneration > 0))
                {
                    _activeTarget = target;
                    _snapshot = new ClientConnectionRecoverySnapshot(
                        ClientConnectionRecoveryPhase.RecoveringWorld,
                        intent,
                        manual ? controlGeneration : queuedWorldGeneration,
                        target.TargetGeneration,
                        ClientConnectionRecoveryResultKind.None,
                        manual);
                    continuedWorldRecovery = CompleteWorldCoreAsync(
                        intent,
                        manual ? controlGeneration : queuedWorldGeneration,
                        target,
                        manual);
                    _activeTask = continuedWorldRecovery;
                }
                else
                {
                    _activeTarget = null;
                    _activeTask = null;
                    ReleaseIntentDeadlineLocked();
                    _snapshot = result == ClientConnectionRecoveryResultKind.Succeeded
                        ? new ClientConnectionRecoverySnapshot(
                            ClientConnectionRecoveryPhase.Idle,
                            intent,
                            controlGeneration,
                            target.TargetGeneration,
                            result,
                            manual)
                        : new ClientConnectionRecoverySnapshot(
                            ClientConnectionRecoveryPhase.ConnectionLost,
                            intent,
                            controlGeneration,
                            target.TargetGeneration,
                            result,
                            manual);
                }

                committed = _snapshot;
            }

            Notify(committed);
            if (continuedWorldRecovery == null)
            {
                ReconcileLatestControlHealth(controlGeneration);
            }
        }

        /// <summary>观察manual control run的异常终态，确保调用方不会留下未观察Task。</summary>
        /// <param name="runTask">本次manual run。</param>
        /// <param name="controlGeneration">本次run的首个health generation。</param>
        private async Task ObserveManualControlRunAsync(Task runTask, long controlGeneration)
        {
            try
            {
                await runTask;
            }
            catch (OperationCanceledException)
            {
                // AppLifetime停止会提交Stopped；不把所有权取消误报为网络故障。
            }
            catch (Exception)
            {
                FailControlRecovery(
                    controlGeneration,
                    ClientConnectionRecoveryResultKind.Internal);
            }
        }

        /// <summary>执行world恢复并在成功后等待Scene第四重gate。</summary>
        private async Task CompleteWorldCoreAsync(
            long intent,
            long sourceGeneration,
            ClientRecoveryTargetDescriptor target,
            bool manual)
        {
            var result = await ExecuteBoundedAsync(
                token => _recoverGameplayFlow.ExecuteAsync(
                    _operations,
                    target,
                    token));
            var validationTarget = target;
            if (result == ClientConnectionRecoveryResultKind.ReturningOwnWorld &&
                (!_operations.TryCaptureTarget(out validationTarget) ||
                 validationTarget.Kind != ClientRecoveryTargetKind.OwnWorld ||
                 !target.HasSameSessionLineage(validationTarget)))
            {
                result = ClientConnectionRecoveryResultKind.Protocol;
            }

            ClientConnectionRecoverySnapshot committed;
            lock (_sync)
            {
                if (!IsCurrentIntentLocked(intent, ClientConnectionRecoveryPhase.RecoveringWorld))
                {
                    return;
                }

                _activeTask = null;
                if ((result == ClientConnectionRecoveryResultKind.Succeeded ||
                     result == ClientConnectionRecoveryResultKind.ReturningOwnWorld) &&
                    _sessionCoordinator.TryGetCurrent(out var session) &&
                     target.IsBoundTo(session) &&
                    _operations.TryValidateRecoveredTarget(
                        validationTarget,
                        out var targetGeneration))
                {
                    _activeTarget = validationTarget;
                    _snapshot = new ClientConnectionRecoverySnapshot(
                        ClientConnectionRecoveryPhase.AwaitingSceneCommit,
                        intent,
                        sourceGeneration,
                        targetGeneration,
                        result,
                        manual);
                }
                else
                {
                    _activeTarget = null;
                    ReleaseIntentDeadlineLocked();
                    _snapshot = new ClientConnectionRecoverySnapshot(
                        ClientConnectionRecoveryPhase.ConnectionLost,
                        intent,
                        sourceGeneration,
                        target.TargetGeneration,
                        result == ClientConnectionRecoveryResultKind.Succeeded ||
                        result == ClientConnectionRecoveryResultKind.ReturningOwnWorld
                            ? ClientConnectionRecoveryResultKind.Protocol
                            : result,
                        manual);
                }

                committed = _snapshot;
            }

            Notify(committed);
            if (committed.Phase == ClientConnectionRecoveryPhase.AwaitingSceneCommit)
            {
                ArmSceneDeadline(
                    committed.IntentGeneration,
                    committed.TargetGeneration);
            }
        }

        /// <summary>执行有总deadline的单次恢复operation并封闭异常。</summary>
        private async Task<ClientConnectionRecoveryResultKind> ExecuteBoundedAsync(
            Func<CancellationToken, Task<ClientConnectionRecoveryResultKind>> operation)
        {
            CancellationToken lifetime;
            CancellationToken intentDeadline;
            lock (_sync)
            {
                lifetime = _lifetimeCancellation?.Token ?? new CancellationToken(canceled: true);
                intentDeadline = _intentDeadlineCancellation?.Token ??
                                 new CancellationToken(canceled: true);
            }

            using (var linked = CancellationTokenSource.CreateLinkedTokenSource(
                       lifetime,
                       intentDeadline))
            {
                try
                {
                    return await operation(linked.Token);
                }
                catch (OperationCanceledException)
                {
                    return lifetime.IsCancellationRequested
                        ? ClientConnectionRecoveryResultKind.Stopped
                        : ClientConnectionRecoveryResultKind.Deadline;
                }
                catch (Exception)
                {
                    return ClientConnectionRecoveryResultKind.Internal;
                }
            }
        }

        /// <summary>为新intent创建唯一总deadline；factory失败时不开放恢复副作用。</summary>
        /// <returns>成功取得非空deadline owner时返回true。</returns>
        private bool TryStartIntentDeadlineLocked()
        {
            CancellationTokenSource deadline;
            try
            {
                deadline = _deadlineFactory(_deadline);
            }
            catch (Exception)
            {
                return false;
            }

            if (deadline == null)
            {
                return false;
            }

            ReleaseIntentDeadlineLocked();
            _intentDeadlineCancellation = deadline;
            return true;
        }

        /// <summary>让Scene第四重gate继续受本笔intent既有总deadline约束。</summary>
        /// <param name="intentGeneration">等待中的恢复intent。</param>
        /// <param name="targetGeneration">等待中的target generation。</param>
        private void ArmSceneDeadline(long intentGeneration, long targetGeneration)
        {
            CancellationToken token;
            lock (_sync)
            {
                if (_snapshot.Phase != ClientConnectionRecoveryPhase.AwaitingSceneCommit ||
                    _snapshot.IntentGeneration != intentGeneration ||
                    _snapshot.TargetGeneration != targetGeneration ||
                    _intentDeadlineCancellation == null)
                {
                    return;
                }

                token = _intentDeadlineCancellation.Token;
            }

            var registration = token.Register(() =>
                FailSceneCommit(
                    intentGeneration,
                    targetGeneration,
                    ClientConnectionRecoveryResultKind.Deadline));
            lock (_sync)
            {
                if (_snapshot.Phase == ClientConnectionRecoveryPhase.AwaitingSceneCommit &&
                    _snapshot.IntentGeneration == intentGeneration &&
                    _snapshot.TargetGeneration == targetGeneration &&
                    _intentDeadlineCancellation != null)
                {
                    _sceneDeadlineRegistration.Dispose();
                    _sceneDeadlineRegistration = registration;
                }
                else
                {
                    registration.Dispose();
                }
            }
        }

        /// <summary>释放当前intent deadline与Scene callback，不影响App Scope lifetime。</summary>
        private void ReleaseIntentDeadlineLocked()
        {
            _sceneDeadlineRegistration.Dispose();
            _sceneDeadlineRegistration = default;
            _intentDeadlineCancellation?.Dispose();
            _intentDeadlineCancellation = null;
        }

        /// <summary>检查lifecycle允许创建新intent。</summary>
        private bool CanBeginLocked()
        {
            return _lifetimeCancellation != null &&
                   !_lifetimeCancellation.IsCancellationRequested &&
                   _snapshot.Phase != ClientConnectionRecoveryPhase.Stopped;
        }

        /// <summary>判断恢复是否已经进入可由表现层继续处理的稳定边界。</summary>
        private static bool IsSettled(ClientConnectionRecoveryPhase phase)
        {
            return phase == ClientConnectionRecoveryPhase.Idle ||
                   phase == ClientConnectionRecoveryPhase.AwaitingSceneCommit ||
                   phase == ClientConnectionRecoveryPhase.ConnectionLost ||
                   phase == ClientConnectionRecoveryPhase.Stopped;
        }

        /// <summary>检查completion的intent与阶段均current。</summary>
        private bool IsCurrentIntentLocked(
            long intent,
            ClientConnectionRecoveryPhase phase)
        {
            return _stateMachine.CanCommit(_snapshot, intent, phase);
        }

        /// <summary>锁内提交唯一terminal snapshot。</summary>
        private ClientConnectionRecoverySnapshot CommitTerminalLocked(
            ClientConnectionRecoveryResultKind result)
        {
            _activeTarget = null;
            _activeTask = null;
            _pendingWorldSourceGeneration = 0;
            ReleaseIntentDeadlineLocked();
            _snapshot = new ClientConnectionRecoverySnapshot(
                ClientConnectionRecoveryPhase.ConnectionLost,
                _snapshot.IntentGeneration,
                _snapshot.SourceChannelGeneration,
                _snapshot.TargetGeneration,
                result,
                _snapshot.Manual);
            return _snapshot;
        }

        /// <summary>只在指定current intent仍有效时提交terminal snapshot。</summary>
        private ClientConnectionRecoverySnapshot CommitTerminal(
            long intent,
            ClientConnectionRecoveryPhase phase,
            ClientConnectionRecoveryResultKind result)
        {
            lock (_sync)
            {
                return IsCurrentIntentLocked(intent, phase)
                    ? CommitTerminalLocked(result)
                    : null;
            }
        }

        /// <summary>在尚未进入任何world时允许control本身独立恢复成功。</summary>
        private ClientConnectionRecoverySnapshot CommitControlWithoutTarget(
            long intent,
            long controlGeneration)
        {
            lock (_sync)
            {
                if (!IsCurrentIntentLocked(
                        intent,
                        ClientConnectionRecoveryPhase.RecoveringControl))
                {
                    return null;
                }

                _activeTask = null;
                _activeTarget = null;
                _pendingWorldSourceGeneration = 0;
                ReleaseIntentDeadlineLocked();
                _snapshot = new ClientConnectionRecoverySnapshot(
                    ClientConnectionRecoveryPhase.Idle,
                    intent,
                    controlGeneration,
                    0,
                    ClientConnectionRecoveryResultKind.Succeeded,
                    _snapshot.Manual);
                return _snapshot;
            }
        }

        /// <summary>把control adapter状态变化收敛为独立恢复intent。</summary>
        private void OnControlHealthChanged(ClientControlHealthSnapshot channel)
        {
            switch (channel.Phase)
            {
                case ClientControlHealthPhase.Recovering:
                    BeginControlRecovery(channel.Generation);
                    break;
                case ClientControlHealthPhase.Connected:
                    if (Snapshot.Phase == ClientConnectionRecoveryPhase.ConnectionLost)
                    {
                        BeginControlRecovery(channel.Generation);
                    }

                    _ = CompleteControlRecoveryAsync(channel.Generation);
                    break;
                case ClientControlHealthPhase.SessionInvalidated:
                    BeginControlRecovery(channel.Generation);
                    FailControlRecovery(
                        channel.Generation,
                        ClientConnectionRecoveryResultKind.Authentication);
                    break;
                case ClientControlHealthPhase.Disconnected:
                    if (channel.DisconnectKind != ClientControlDisconnectKind.Requested &&
                        channel.DisconnectKind != ClientControlDisconnectKind.Superseded)
                    {
                        BeginControlRecovery(channel.Generation);
                        FailControlRecovery(
                            channel.Generation,
                            MapControlFailure(channel.DisconnectKind));
                    }

                    break;
            }
        }

        /// <summary>在gameplay已撤销旧owner后启动唯一automatic恢复。</summary>
        private void OnGameplayUnexpectedDisconnect(ClientGameplayHealthSnapshot channel)
        {
            BeginAutomaticWorldRecovery(channel.Generation);
        }

        /// <summary>补收旧snapshot reconciliation期间到达的下一代control health。</summary>
        private void ReconcileLatestControlHealth(long completedGeneration)
        {
            if (_controlChannel == null)
            {
                return;
            }

            var latest = _controlChannel.Snapshot;
            if (latest.Generation <= completedGeneration)
            {
                return;
            }

            if (latest.Phase == ClientControlHealthPhase.Recovering)
            {
                BeginControlRecovery(latest.Generation);
                return;
            }

            if (latest.Phase == ClientControlHealthPhase.Connected)
            {
                BeginControlRecovery(latest.Generation);
                _ = CompleteControlRecoveryAsync(latest.Generation);
                return;
            }

            if (latest.Phase == ClientControlHealthPhase.Disconnected &&
                latest.DisconnectKind != ClientControlDisconnectKind.Requested &&
                latest.DisconnectKind != ClientControlDisconnectKind.Superseded)
            {
                BeginControlRecovery(latest.Generation);
                FailControlRecovery(
                    latest.Generation,
                    MapControlFailure(latest.DisconnectKind));
            }
        }

        /// <summary>将control关闭原因映射为稳定恢复结果。</summary>
        private static ClientConnectionRecoveryResultKind MapControlFailure(
            ClientControlDisconnectKind reason)
        {
            switch (reason)
            {
                case ClientControlDisconnectKind.Transport:
                    return ClientConnectionRecoveryResultKind.Transport;
                case ClientControlDisconnectKind.Protocol:
                    return ClientConnectionRecoveryResultKind.Protocol;
                case ClientControlDisconnectKind.SessionInvalidated:
                    return ClientConnectionRecoveryResultKind.Authentication;
                default:
                    return ClientConnectionRecoveryResultKind.Policy;
            }
        }

        /// <summary>在锁外发布不可变snapshot。</summary>
        private void Notify(ClientConnectionRecoverySnapshot snapshot)
        {
            if (snapshot != null)
            {
                Changed?.Invoke(snapshot);
            }
        }

        /// <summary>以调用方deadline等待active owner，不取消第二次。</summary>
        private static async Task AwaitWithCancellationAsync(
            Task task,
            CancellationToken cancellationToken)
        {
            if (task.IsCompleted)
            {
                await task;
                return;
            }

            var signal = new TaskCompletionSource<bool>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            using (cancellationToken.Register(() => signal.TrySetCanceled()))
            {
                await await Task.WhenAny(task, signal.Task);
            }
        }
    }
}
