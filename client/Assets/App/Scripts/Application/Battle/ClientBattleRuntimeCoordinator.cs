using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Foundation.Lifetime;
using IHomeland.Client.Foundation.Time;

namespace IHomeland.Client.Application.Battle
{
    /// <summary>
    /// 保存 battle runtime owners 的低敏一致快照。
    /// </summary>
    internal sealed class ClientBattleRuntimeSnapshot
    {
        /// <summary>
        /// 创建不含 endpoint、ticket、payload 或玩家 identity 的 runtime 快照。
        /// </summary>
        internal ClientBattleRuntimeSnapshot(
            ClientBattleAvailability availability,
            ClientBattleFailure failure,
            long battleGeneration,
            ClientBattleTargetKind? targetKind,
            ClientBattleRole role,
            int actorSlot,
            bool baselineReady,
            bool inputEnabled)
        {
            if (!Enum.IsDefined(typeof(ClientBattleAvailability), availability) ||
                battleGeneration < 0 ||
                actorSlot < -1 ||
                actorSlot > 7 ||
                (battleGeneration == 0 &&
                 (role != ClientBattleRole.None ||
                  actorSlot != -1 ||
                  baselineReady ||
                  inputEnabled)))
            {
                throw new ArgumentException(
                    "Client battle runtime snapshot is invalid.");
            }

            Availability = availability;
            Failure = failure;
            BattleGeneration = battleGeneration;
            TargetKind = targetKind;
            Role = role;
            ActorSlot = actorSlot;
            BaselineReady = baselineReady;
            InputEnabled = inputEnabled;
        }

        /// <summary>获取 Scene/HUD 可消费的低敏可用性。</summary>
        internal ClientBattleAvailability Availability { get; }

        /// <summary>获取最近稳定 failure。</summary>
        internal ClientBattleFailure Failure { get; }

        /// <summary>获取 current battle generation。</summary>
        internal long BattleGeneration { get; }

        /// <summary>获取 current target kind。</summary>
        internal ClientBattleTargetKind? TargetKind { get; }

        /// <summary>获取 authenticated actor role。</summary>
        internal ClientBattleRole Role { get; }

        /// <summary>获取 authenticated actor slot。</summary>
        internal int ActorSlot { get; }

        /// <summary>获取 current full baseline 是否已原子发布。</summary>
        internal bool BaselineReady { get; }

        /// <summary>获取 semantic input gate 是否已打开。</summary>
        internal bool InputEnabled { get; }
    }

#if DEVELOPMENT_BUILD || UNITY_EDITOR
    /// <summary>保存真实Development Player可读取的battle权威与预测一致性快照。</summary>
    internal sealed class ClientBattleRuntimeQualificationSnapshot
    {
        /// <summary>创建不含credential、endpoint、payload或玩家业务identity的资格快照。</summary>
        internal ClientBattleRuntimeQualificationSnapshot(
            ClientBattleRuntimeSnapshot runtime,
            ulong serverTick,
            ulong lastSentInputTick,
            ulong lastAcknowledgedInputTick,
            ulong continuityAcknowledgementAnchor,
            ulong acknowledgementAcceptanceFrontier,
            int entityCount,
            ulong localEntityID,
            ClientBattleTransform authorityTransform,
            ClientBattleTransform predictedTransform,
            bool authorityGrounded,
            bool predictedGrounded,
            ulong reconciliationCount,
            int lastReconciliationPositionDeltaMillimeters,
            int maximumReconciliationPositionDeltaMillimeters,
            int lastReconciliationYawDeltaMillidegrees,
            ulong lastJumpInputTick,
            ulong lastJumpSimulationTick,
            bool lastJumpPredicted,
            bool resyncPending)
        {
            Runtime = runtime ?? throw new ArgumentNullException(nameof(runtime));
            if (entityCount < 0 ||
                entityCount > ClientBattlePolicy.Current.MaximumEntities ||
                (runtime.BattleGeneration == 0 &&
                 (serverTick != 0 ||
                  lastSentInputTick != 0 ||
                   lastAcknowledgedInputTick != 0 ||
                   continuityAcknowledgementAnchor != 0 ||
                   acknowledgementAcceptanceFrontier != 0 ||
                 entityCount != 0 ||
                 localEntityID != 0 ||
                 authorityGrounded ||
                  predictedGrounded ||
                  reconciliationCount != 0 ||
                  lastReconciliationPositionDeltaMillimeters != 0 ||
                  maximumReconciliationPositionDeltaMillimeters != 0 ||
                  lastReconciliationYawDeltaMillidegrees != 0 ||
                  lastJumpInputTick != 0 ||
                  lastJumpSimulationTick != 0 ||
                  lastJumpPredicted ||
                  resyncPending)) ||
                continuityAcknowledgementAnchor >
                    lastAcknowledgedInputTick ||
                lastAcknowledgedInputTick >
                    acknowledgementAcceptanceFrontier ||
                lastSentInputTick >
                    acknowledgementAcceptanceFrontier ||
                continuityAcknowledgementAnchor >
                    acknowledgementAcceptanceFrontier ||
                (runtime.BaselineReady &&
                 localEntityID != (ulong)(runtime.ActorSlot + 1)) ||
                lastReconciliationPositionDeltaMillimeters < 0 ||
                maximumReconciliationPositionDeltaMillimeters <
                    lastReconciliationPositionDeltaMillimeters ||
                lastReconciliationYawDeltaMillidegrees < 0 ||
                lastReconciliationYawDeltaMillidegrees > 180000 ||
                (lastJumpInputTick == 0) !=
                    (lastJumpSimulationTick == 0) ||
                (lastJumpInputTick == 0 && lastJumpPredicted))
            {
                throw new ArgumentException(
                    "Client battle qualification snapshot is invalid.");
            }

            ServerTick = serverTick;
            LastSentInputTick = lastSentInputTick;
            LastAcknowledgedInputTick = lastAcknowledgedInputTick;
            ContinuityAcknowledgementAnchor =
                continuityAcknowledgementAnchor;
            AcknowledgementAcceptanceFrontier =
                acknowledgementAcceptanceFrontier;
            EntityCount = entityCount;
            LocalEntityID = localEntityID;
            AuthorityTransform = authorityTransform;
            PredictedTransform = predictedTransform;
            AuthorityGrounded = authorityGrounded;
            PredictedGrounded = predictedGrounded;
            ReconciliationCount = reconciliationCount;
            LastReconciliationPositionDeltaMillimeters =
                lastReconciliationPositionDeltaMillimeters;
            MaximumReconciliationPositionDeltaMillimeters =
                maximumReconciliationPositionDeltaMillimeters;
            LastReconciliationYawDeltaMillidegrees =
                lastReconciliationYawDeltaMillidegrees;
            LastJumpInputTick = lastJumpInputTick;
            LastJumpSimulationTick = lastJumpSimulationTick;
            LastJumpPredicted = lastJumpPredicted;
            ResyncPending = resyncPending;
        }

        /// <summary>获取同一捕获窗口的runtime lifecycle快照。</summary>
        internal ClientBattleRuntimeSnapshot Runtime { get; }

        /// <summary>获取已提交authority server Tick。</summary>
        internal ulong ServerTick { get; }

        /// <summary>获取已进入current send queue的最大InputTick。</summary>
        internal ulong LastSentInputTick { get; }

        /// <summary>获取服务器snapshot已确认的最大InputTick。</summary>
        internal ulong LastAcknowledgedInputTick { get; }

        /// <summary>获取lost-continuity恢复时服务器已终结的InputTick前沿。</summary>
        internal ulong ContinuityAcknowledgementAnchor { get; }

        /// <summary>获取已发送或由本地时钟前向跳过的最大InputTick。</summary>
        internal ulong AcknowledgementAcceptanceFrontier { get; }

        /// <summary>获取current authority actor集合数量。</summary>
        internal int EntityCount { get; }

        /// <summary>获取actor slot映射得到的local entity identity。</summary>
        internal ulong LocalEntityID { get; }

        /// <summary>获取current local actor权威transform。</summary>
        internal ClientBattleTransform AuthorityTransform { get; }

        /// <summary>获取current local actor预测transform。</summary>
        internal ClientBattleTransform PredictedTransform { get; }

        /// <summary>获取current local actor权威grounded状态。</summary>
        internal bool AuthorityGrounded { get; }

        /// <summary>获取current local actor预测grounded候选。</summary>
        internal bool PredictedGrounded { get; }

        /// <summary>获取current generation成功reconciliation次数。</summary>
        internal ulong ReconciliationCount { get; }

        /// <summary>获取最近一次reconciliation的local target位置差，单位毫米。</summary>
        internal int LastReconciliationPositionDeltaMillimeters { get; }

        /// <summary>获取current generation最大的local target位置差，单位毫米。</summary>
        internal int MaximumReconciliationPositionDeltaMillimeters { get; }

        /// <summary>获取最近一次reconciliation的yaw差，单位millidegree。</summary>
        internal int LastReconciliationYawDeltaMillidegrees { get; }

        /// <summary>获取最近一次成功发送jump edge的InputTick。</summary>
        internal ulong LastJumpInputTick { get; }

        /// <summary>获取最近一次jump edge映射的SimulationTick。</summary>
        internal ulong LastJumpSimulationTick { get; }

        /// <summary>获取最近一次jump edge是否产生airborne local prediction。</summary>
        internal bool LastJumpPredicted { get; }

        /// <summary>获取current generation是否等待single-flight resync。</summary>
        internal bool ResyncPending { get; }
    }
#endif

    /// <summary>
    /// 编排 target、connection、replica、prediction、interpolation 与 presentation generation。
    /// </summary>
    /// <remarks>
    /// World、VisitSession 与 Session 最终事实仍由既有 owners 持有；本 owner 只保存 battle 派生状态。
    /// </remarks>
    internal sealed class ClientBattleRuntimeCoordinator :
        IAppLifetimeParticipant,
        IClientBattleInboundSink
    {
        /// <summary>失败 attempt 间的最小间隔，防止本地依赖失败形成 catch-up burst。</summary>
        private static readonly TimeSpan RecoveryRetryDelay =
            TimeSpan.FromMilliseconds(100);

        /// <summary>保护 runtime 快照、generation 与 transition cancellation。</summary>
        private readonly object _sync = new object();

        /// <summary>串行化 target replacement、retry 与 AppLifetime stop。</summary>
        private readonly SemaphoreSlim _transition =
            new SemaphoreSlim(1, 1);

        /// <summary>读取唯一 stable world target。</summary>
        private readonly IClientBattleTargetSource _targets;

        /// <summary>控制唯一 ticket/UDP/secure/KCP connection owner。</summary>
        private readonly IClientBattleConnectionPort _connection;

        /// <summary>拥有 authority snapshot/lifecycle/resync state。</summary>
        private readonly GameplayReplica _replica;

        /// <summary>拥有 semantic input 与 local predicted state。</summary>
        private readonly GameplayPrediction _prediction;

        /// <summary>拥有 remote actor sample timeline。</summary>
        private readonly GameplayInterpolation _interpolation;

        /// <summary>为 retry deadline 与 interpolation elapsed 提供统一时间。</summary>
        private readonly IClientClock _clock;

        /// <summary>复用产品唯一 recovery owner 的 battle-only single-flight。</summary>
        private readonly IClientBattleRecoveryScheduler _recovery;

        /// <summary>App Scope lifetime cancellation owner。</summary>
        private CancellationTokenSource _lifetimeCancellation;

        /// <summary>取消被 successor target/recovery 替换的 operation。</summary>
        private CancellationTokenSource _targetCancellation;

        /// <summary>每次 target/recovery intent 单调递增，拒绝迟到 completion。</summary>
        private long _intentGeneration;

        /// <summary>Current activated target；不保存 endpoint 或 credential。</summary>
        private ClientBattleTargetIntent _target;

        /// <summary>Current authenticated battle generation。</summary>
        private long _battleGeneration;

        /// <summary>Current authenticated role。</summary>
        private ClientBattleRole _role;

        /// <summary>Current authenticated actor slot。</summary>
        private int _actorSlot = -1;

        /// <summary>Current Scene/HUD availability。</summary>
        private ClientBattleAvailability _availability =
            ClientBattleAvailability.Inactive;

        /// <summary>最近稳定低敏失败。</summary>
        private ClientBattleFailure _failure;

        /// <summary>Full baseline 已原子发布且 prediction 已激活。</summary>
        private bool _baselineReady;

        /// <summary>最近 authority snapshot 到达的 Unix 毫秒。</summary>
        private long _lastSnapshotAtMilliseconds;

        /// <summary>下一次 presentation consume 是否显示 correction。</summary>
        private bool _correctionVisible;

        /// <summary>Scene 选择的纯 camera mode；不可参与 authority。</summary>
        private ClientBattleCameraMode _cameraMode =
            ClientBattleCameraMode.Exploration;

        /// <summary>表示 target source subscriber 已登记。</summary>
        private bool _subscribed;

        /// <summary>表示 App Scope 已不可逆停止。</summary>
        private bool _stopped;

#if DEVELOPMENT_BUILD || UNITY_EDITOR
        /// <summary>表示真实Development Player资格场景暂时拥有semantic input sample。</summary>
        private bool _qualificationInputOverride;
#endif

        /// <summary>
        /// 创建 App Scope 唯一 battle runtime owner。
        /// </summary>
        internal ClientBattleRuntimeCoordinator(
            IClientBattleTargetSource targets,
            IClientBattleConnectionPort connection,
            GameplayReplica replica,
            GameplayPrediction prediction,
            GameplayInterpolation interpolation,
            IClientClock clock,
            IClientBattleRecoveryScheduler recovery)
        {
            _targets = targets ?? throw new ArgumentNullException(nameof(targets));
            _connection = connection ??
                throw new ArgumentNullException(nameof(connection));
            _replica = replica ?? throw new ArgumentNullException(nameof(replica));
            _prediction = prediction ??
                throw new ArgumentNullException(nameof(prediction));
            _interpolation = interpolation ??
                throw new ArgumentNullException(nameof(interpolation));
            _clock = clock ?? throw new ArgumentNullException(nameof(clock));
            _recovery = recovery ??
                throw new ArgumentNullException(nameof(recovery));
        }

        /// <summary>
        /// 登记 target replacement subscriber；初始化本身不创建 ticket 或 socket。
        /// </summary>
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            lock (_sync)
            {
                if (_lifetimeCancellation != null || _stopped)
                {
                    throw new InvalidOperationException(
                        "ClientBattleRuntimeCoordinator cannot initialize twice");
                }

                _lifetimeCancellation = new CancellationTokenSource();
                _targets.Changed += OnTargetChanged;
                _subscribed = true;
            }

            if (_targets.TryCapture(out _))
            {
                _ = ScheduleSynchronize(forceReconnect: false);
            }

            return Task.CompletedTask;
        }

        /// <summary>
        /// 提交 current Scene generation 的 semantic input sample。
        /// </summary>
        /// <param name="battleGeneration">Scene lease 冻结的 battle generation。</param>
        /// <param name="input">不含 transform/hit/damage/reward authority 的封闭输入。</param>
        /// <returns>Current input gate 接受 sample 时为 true。</returns>
        internal bool TrySetInput(
            long battleGeneration,
            ClientBattleSemanticInput input)
        {
            lock (_sync)
            {
                if (_stopped ||
                    !_baselineReady ||
                    _availability != ClientBattleAvailability.Active ||
                    battleGeneration != _battleGeneration
#if DEVELOPMENT_BUILD || UNITY_EDITOR
                    || _qualificationInputOverride
#endif
                   )
                {
                    return false;
                }
            }

            return _prediction.TrySetInput(battleGeneration, input);
        }

#if DEVELOPMENT_BUILD || UNITY_EDITOR
        /// <summary>由真实Development Player资格场景临时提交semantic input，不引入权威字段。</summary>
        /// <param name="battleGeneration">必须匹配current active generation。</param>
        /// <param name="input">与Scene host相同的封闭semantic input。</param>
        /// <returns>资格override取得current generation并接受sample时为true。</returns>
        internal bool TrySetQualificationInput(
            long battleGeneration,
            ClientBattleSemanticInput input)
        {
            lock (_sync)
            {
                if (_stopped ||
                    !_baselineReady ||
                    _availability != ClientBattleAvailability.Active ||
                    battleGeneration != _battleGeneration)
                {
                    return false;
                }

                _qualificationInputOverride = true;
            }

            return _prediction.TrySetInput(battleGeneration, input);
        }

        /// <summary>释放资格input override并提交零连续输入，避免跨场景或generation延续。</summary>
        /// <param name="battleGeneration">必须匹配current generation。</param>
        internal void ClearQualificationInput(long battleGeneration)
        {
            lock (_sync)
            {
                if (battleGeneration != _battleGeneration)
                {
                    return;
                }

                _qualificationInputOverride = false;
            }

            _prediction.TrySetInput(
                battleGeneration,
                new ClientBattleSemanticInput(
                    0,
                    0,
                    0,
                    0,
                    jumpPressed: false,
                    primaryPressed: false,
                    secondaryPressed: false,
                    interactPressed: false,
                    interactionSlot: 0));
        }
#endif

        /// <summary>
        /// 设置只影响表现的 camera mode。
        /// </summary>
        /// <param name="battleGeneration">Scene lease 冻结的 generation。</param>
        /// <param name="mode">封闭 camera intent mode。</param>
        /// <returns>Current generation 接受时为 true。</returns>
        internal bool TrySetCameraMode(
            long battleGeneration,
            ClientBattleCameraMode mode)
        {
            if (!Enum.IsDefined(typeof(ClientBattleCameraMode), mode))
            {
                return false;
            }

            lock (_sync)
            {
                if (_stopped || battleGeneration != _battleGeneration)
                {
                    return false;
                }

                _cameraMode = mode;
                return true;
            }
        }

        /// <summary>
        /// 读取不产生 network 或 owner mutation 的低敏 runtime 快照。
        /// </summary>
        internal ClientBattleRuntimeSnapshot Snapshot()
        {
            lock (_sync)
            {
                var prediction = _prediction.Snapshot();
                return new ClientBattleRuntimeSnapshot(
                    _availability,
                    _failure,
                    _battleGeneration,
                    _target?.Kind,
                    _role,
                    _actorSlot,
                    _baselineReady,
                    _baselineReady &&
                    prediction.BattleGeneration == _battleGeneration &&
                    prediction.InputEnabled &&
                    !prediction.BaselineRequired);
            }
        }

#if DEVELOPMENT_BUILD || UNITY_EDITOR
        /// <summary>为真实Development Player资格场景捕获同代authority、ack与prediction状态。</summary>
        /// <param name="snapshot">成功时返回同一battle generation的低敏快照。</param>
        /// <returns>捕获期间generation发生替换时为false。</returns>
        internal bool TryCaptureQualificationSnapshot(
            out ClientBattleRuntimeQualificationSnapshot snapshot)
        {
            ClientBattleRuntimeSnapshot runtime;
            lock (_sync)
            {
                var prediction = _prediction.Snapshot();
                runtime = new ClientBattleRuntimeSnapshot(
                    _availability,
                    _failure,
                    _battleGeneration,
                    _target?.Kind,
                    _role,
                    _actorSlot,
                    _baselineReady,
                    _baselineReady &&
                    prediction.BattleGeneration == _battleGeneration &&
                    prediction.InputEnabled &&
                    !prediction.BaselineRequired);
            }

            var replica = _replica.Snapshot();
            var predicted = _prediction.Snapshot();
            lock (_sync)
            {
                if (_battleGeneration != runtime.BattleGeneration ||
                    _availability != runtime.Availability ||
                    replica.BattleGeneration != runtime.BattleGeneration ||
                    predicted.BattleGeneration != runtime.BattleGeneration)
                {
                    snapshot = null;
                    return false;
                }
            }

            ClientBattleEntityState local = null;
            if (runtime.BaselineReady && !replica.TryGetLocalEntity(out local))
            {
                snapshot = null;
                return false;
            }

            snapshot = new ClientBattleRuntimeQualificationSnapshot(
                runtime,
                replica.ServerTick,
                predicted.LastSentInputTick,
                predicted.LastAcknowledgedInputTick,
                predicted.ContinuityAcknowledgementAnchor,
                predicted.AcknowledgementAcceptanceFrontier,
                replica.Entities.Count,
                replica.LocalEntityID,
                local == null ? default : local.Transform,
                predicted.State.Transform,
                local != null && local.Grounded,
                predicted.State.Grounded,
                predicted.ReconciliationCount,
                predicted.LastReconciliationPositionDeltaMillimeters,
                predicted.MaximumReconciliationPositionDeltaMillimeters,
                predicted.LastReconciliationYawDeltaMillidegrees,
                predicted.LastJumpInputTick,
                predicted.LastJumpSimulationTick,
                predicted.LastJumpPredicted,
                replica.ResyncPending);
            return true;
        }
#endif

        /// <summary>
        /// 在 Scene 主线程消费一次 immutable actor/HUD/cue/camera presentation state。
        /// </summary>
        /// <param name="state">成功时返回 current generation 的一致投影。</param>
        /// <returns>Full baseline 尚未就绪或 generation 已退役时为 false。</returns>
        internal bool TryConsumePresentation(
            out ClientGameplayPresentationState state)
        {
            state = null;
            long generation;
            long snapshotAt;
            bool correction;
            ClientBattleAvailability availability;
            ClientBattleCameraMode cameraMode;
            lock (_sync)
            {
                if (_stopped || !_baselineReady || _battleGeneration == 0)
                {
                    return false;
                }

                generation = _battleGeneration;
                snapshotAt = _lastSnapshotAtMilliseconds;
                correction = _correctionVisible;
                _correctionVisible = false;
                availability = _availability;
                cameraMode = _cameraMode;
            }

            var replica = _replica.Snapshot();
            var prediction = _prediction.Snapshot();
            if (replica.BattleGeneration != generation ||
                prediction.BattleGeneration != generation ||
                replica.ServerTick == 0)
            {
                return false;
            }

            var elapsed = Math.Max(
                0,
                _clock.UtcNowMilliseconds - snapshotAt);
            var maximumTick = (ulong)(
                long.MaxValue /
                ClientBattlePolicy.Current.SimulationStepMilliseconds);
            var clampedTick = Math.Min(replica.ServerTick, maximumTick);
            var authorityNow = checked(
                (long)clampedTick *
                ClientBattlePolicy.Current.SimulationStepMilliseconds +
                Math.Min(
                    elapsed,
                    ClientBattlePolicy.Current.
                        MaximumExtrapolationMilliseconds));
            var remotes = _interpolation.Evaluate(
                generation,
                authorityNow);
            var abilities = _replica.DrainAbilityEvents(generation);
            state = GameplayPresentationProjector.Project(
                replica,
                prediction,
                remotes,
                abilities,
                availability,
                cameraMode,
                correction);
            return true;
        }

        /// <inheritdoc />
        public ClientBattleSnapshotCommit AcceptSnapshot(
            ClientBattleSnapshotPartition partition)
        {
            if (partition == null || !IsCurrent(partition.BattleGeneration))
            {
                return ClientBattleSnapshotCommit.Duplicate;
            }

            var commit = _replica.AcceptSnapshot(partition);
            if (commit != ClientBattleSnapshotCommit.Published)
            {
                return commit;
            }

            var replica = _replica.Snapshot();
            if (!replica.TryGetLocalEntity(out var local))
            {
                throw new ClientBattleReplicaProtocolException(
                    "Published snapshot does not contain the bound local actor.");
            }

            var grounded = local.Grounded;
            bool correction;
            lock (_sync)
            {
                if (partition.BattleGeneration != _battleGeneration)
                {
                    return ClientBattleSnapshotCommit.Duplicate;
                }

                if (!_baselineReady)
                {
                    if (partition.Kind != ClientBattleSnapshotKind.Full)
                    {
                        throw new ClientBattleReplicaProtocolException(
                            "Initial published snapshot must be full.");
                    }

                    _prediction.Activate(
                        _battleGeneration,
                        local.Transform,
                        grounded,
                        replica.ServerTick,
                        replica.LastProcessedInputTick);
                    _baselineReady = true;
                    correction = false;
                }
                else
                {
                    correction = _prediction.Reconcile(
                        _battleGeneration,
                        replica.LastProcessedInputTick,
                        replica.ServerTick,
                        local.Transform,
                        grounded);
                }

                _correctionVisible |= correction;
                _lastSnapshotAtMilliseconds =
                    _clock.UtcNowMilliseconds;
                _availability = ClientBattleAvailability.Active;
                _failure = ClientBattleFailure.None;
            }

            if (partition.Kind == ClientBattleSnapshotKind.Full)
            {
                _interpolation.ReplaceSamples(
                    partition.BattleGeneration,
                    replica.ServerTick,
                    replica.Entities);
            }
            else
            {
                AddRemoteSamples(replica);
            }

            return commit;
        }

        /// <inheritdoc />
        public bool AcceptEntityLifecycle(
            ClientBattleEntityLifecycle lifecycle)
        {
            if (lifecycle == null ||
                !IsCurrent(lifecycle.BattleGeneration))
            {
                return false;
            }

            _replica.AcceptLifecycle(lifecycle);
            if (lifecycle.Kind == ClientBattleEntityLifecycleKind.Spawn)
            {
                _interpolation.AddSample(
                    lifecycle.BattleGeneration,
                    lifecycle.ServerTick,
                    lifecycle.InitialState);
            }
            else
            {
                _interpolation.Remove(
                    lifecycle.BattleGeneration,
                    lifecycle.EntityID,
                    lifecycle.EntityGeneration);
            }

            return true;
        }

        /// <inheritdoc />
        public bool AcceptAbilityEvent(
            ClientBattleAbilityEvent abilityEvent)
        {
            if (abilityEvent == null ||
                !IsCurrent(abilityEvent.BattleGeneration))
            {
                return false;
            }

            _replica.AcceptAbilityEvent(abilityEvent);
            return true;
        }

        /// <inheritdoc />
        public bool AcceptResyncResponse(
            ClientBattleResyncResponse response,
            long nowMilliseconds)
        {
            if (response == null ||
                nowMilliseconds < 0 ||
                !IsCurrent(response.BattleGeneration))
            {
                return false;
            }

            _replica.AcceptResyncResponse(response, nowMilliseconds);
            return true;
        }

        /// <inheritdoc />
        public void PublishTerminal(
            long battleGeneration,
            ClientBattleFailure failure)
        {
            if (failure == ClientBattleFailure.None)
            {
                return;
            }

            var recover = false;
            lock (_sync)
            {
                if (_stopped || battleGeneration != _battleGeneration)
                {
                    return;
                }

                DeactivateGameplayLocked(battleGeneration);
                _availability = IsRecoverable(failure)
                    ? ClientBattleAvailability.Retrying
                    : ClientBattleAvailability.Unavailable;
                _failure = failure;
                recover = IsRecoverable(failure);
            }

            if (recover)
            {
                _ = _recovery.RunBattleRecoveryAsync(
                    battleGeneration,
                    RecoverCurrentAsync);
            }
        }

        /// <summary>
        /// 先退役 Scene/gameplay generation，再关闭 socket/native context。
        /// </summary>
        public async Task StopAsync(CancellationToken cancellationToken)
        {
            _recovery.PreemptBattleRecovery();
            CancellationTokenSource lifetime;
            CancellationTokenSource target;
            lock (_sync)
            {
                if (_stopped)
                {
                    return;
                }

                _stopped = true;
                _intentGeneration++;
                if (_subscribed)
                {
                    _targets.Changed -= OnTargetChanged;
                    _subscribed = false;
                }

                lifetime = _lifetimeCancellation;
                target = _targetCancellation;
                _targetCancellation = null;
            }

            target?.Cancel();
            lifetime?.Cancel();
            await _transition.WaitAsync(cancellationToken);
            try
            {
                long generation;
                lock (_sync)
                {
                    generation = _battleGeneration;
                    DeactivateGameplayLocked(generation);
                    _target = null;
                    _availability = ClientBattleAvailability.Inactive;
                    _failure = ClientBattleFailure.Shutdown;
                }

                await _connection.DeactivateAsync(
                    ClientBattleFailure.Shutdown,
                    cancellationToken);
            }
            finally
            {
                _transition.Release();
                target?.Dispose();
                lifetime?.Dispose();
            }
        }

        /// <summary>在 world/session target change 后创建 successor intent。</summary>
        private void OnTargetChanged()
        {
            _recovery.PreemptBattleRecovery();
            _ = ScheduleSynchronize(forceReconnect: false);
        }

        /// <summary>
        /// 取消旧 intent 并异步执行 current target replacement 或 battle-only retry。
        /// </summary>
        private Task<bool> ScheduleSynchronize(
            bool forceReconnect,
            CancellationToken cancellationToken = default)
        {
            CancellationTokenSource previous;
            CancellationTokenSource current;
            long intentGeneration;
            lock (_sync)
            {
                if (_stopped || _lifetimeCancellation == null)
                {
                    return Task.FromResult(false);
                }

                _intentGeneration++;
                intentGeneration = _intentGeneration;
                previous = _targetCancellation;
                current = CancellationTokenSource.CreateLinkedTokenSource(
                    _lifetimeCancellation.Token,
                    cancellationToken);

                _targetCancellation = current;
            }

            previous?.Cancel();
            previous?.Dispose();
            return SynchronizeAsync(
                intentGeneration,
                forceReconnect,
                current.Token);
        }

        /// <summary>
        /// 在唯一 recovery owner 的总 deadline 内建立 successor generation，且不复用旧 ticket/history。
        /// </summary>
        /// <param name="cancellationToken">World/control authority 可抢占的总 deadline token。</param>
        /// <returns>Successor traffic pumps 已启动时为 true。</returns>
        private async Task<bool> RecoverCurrentAsync(
            CancellationToken cancellationToken)
        {
            while (!cancellationToken.IsCancellationRequested)
            {
                if (await ScheduleSynchronize(
                        forceReconnect: true,
                        cancellationToken))
                {
                    return true;
                }

                lock (_sync)
                {
                    if (_stopped || !IsRecoverable(_failure))
                    {
                        return false;
                    }
                }

                await Task.Delay(
                    RecoveryRetryDelay,
                    cancellationToken);
            }

            return false;
        }

        /// <summary>
        /// 串行化应用 current target，迟到结果只清理自身 connection generation。
        /// </summary>
        private async Task<bool> SynchronizeAsync(
            long intentGeneration,
            bool forceReconnect,
            CancellationToken cancellationToken)
        {
            try
            {
                await _transition.WaitAsync(cancellationToken);
                try
                {
                    if (!IsCurrentIntent(intentGeneration))
                    {
                        return false;
                    }

                    if (!_targets.TryCapture(out var target))
                    {
                        await DeactivateCurrentAsync(
                            ClientBattleFailure.TargetReplaced,
                            cancellationToken);
                        return false;
                    }

                    lock (_sync)
                    {
                        if (!forceReconnect &&
                            _target != null &&
                            _target.IsEquivalent(target) &&
                            _battleGeneration != 0)
                        {
                            return true;
                        }

                        var previousGeneration = _battleGeneration;
                        DeactivateGameplayLocked(previousGeneration);
                        _target = target;
                        _availability = forceReconnect
                            ? ClientBattleAvailability.Retrying
                            : ClientBattleAvailability.Connecting;
                        _failure = ClientBattleFailure.None;
                    }

                    await _connection.DeactivateAsync(
                        ClientBattleFailure.TargetReplaced,
                        cancellationToken);
                    var connection = await _connection.ActivateAsync(
                        target,
                        cancellationToken);
                    if (!IsCurrentIntent(intentGeneration) ||
                        !_targets.TryCapture(out var currentTarget) ||
                        !IsSameTargetLineage(target, currentTarget))
                    {
                        await _connection.DeactivateAsync(
                            ClientBattleFailure.TargetReplaced,
                            cancellationToken);
                        return false;
                    }

                    if (connection.State !=
                            ClientBattleConnectionState.AwaitingBaseline ||
                        connection.Generation <= 0 ||
                        connection.ActorSlot < 0 ||
                        connection.Role == ClientBattleRole.None)
                    {
                        lock (_sync)
                        {
                            if (IsCurrentIntentLocked(intentGeneration))
                            {
                                _target = currentTarget;
                                _availability =
                                    ClientBattleAvailability.Unavailable;
                                _failure = connection.Failure ==
                                    ClientBattleFailure.None
                                        ? ClientBattleFailure.Transport
                                        : connection.Failure;
                            }
                        }

                        return false;
                    }

                    _replica.Activate(
                        connection.Generation,
                        connection.ActorSlot);
                    _interpolation.Activate(
                        connection.Generation,
                        checked((ulong)connection.ActorSlot + 1));
                    lock (_sync)
                    {
                        if (!IsCurrentIntentLocked(intentGeneration))
                        {
                            _replica.Deactivate(connection.Generation);
                            _interpolation.Deactivate(connection.Generation);
                            return false;
                        }

                        _target = currentTarget;
                        _battleGeneration = connection.Generation;
                        _role = connection.Role;
                        _actorSlot = connection.ActorSlot;
                        _baselineReady = false;
                        _lastSnapshotAtMilliseconds =
                            _clock.UtcNowMilliseconds;
                        _availability =
                            ClientBattleAvailability.LoadingBaseline;
                        _failure = ClientBattleFailure.None;
                    }

                    if (!_connection.TryStartTraffic(connection.Generation))
                    {
                        await DeactivateCurrentAsync(
                            ClientBattleFailure.Protocol,
                            cancellationToken);
                        return false;
                    }

                    return true;
                }
                finally
                {
                    _transition.Release();
                }
            }
            catch (OperationCanceledException)
            {
                return false;
            }
            catch (Exception)
            {
                lock (_sync)
                {
                    if (IsCurrentIntentLocked(intentGeneration) && !_stopped)
                    {
                        var generation = _battleGeneration;
                        DeactivateGameplayLocked(generation);
                        _availability =
                            ClientBattleAvailability.Unavailable;
                        _failure = ClientBattleFailure.Protocol;
                    }
                }

                return false;
            }
        }

        /// <summary>退役 current gameplay owners 并关闭 connection resources。</summary>
        private async Task DeactivateCurrentAsync(
            ClientBattleFailure failure,
            CancellationToken cancellationToken)
        {
            lock (_sync)
            {
                var generation = _battleGeneration;
                DeactivateGameplayLocked(generation);
                _target = null;
                _availability = ClientBattleAvailability.Inactive;
                _failure = failure;
            }

            await _connection.DeactivateAsync(
                failure,
                cancellationToken);
        }

        /// <summary>在 runtime 锁内先关闭 input，再退役纯 gameplay owners。</summary>
        private void DeactivateGameplayLocked(long battleGeneration)
        {
#if DEVELOPMENT_BUILD || UNITY_EDITOR
            _qualificationInputOverride = false;
#endif
            if (battleGeneration > 0)
            {
                _prediction.Deactivate(battleGeneration);
                _interpolation.Deactivate(battleGeneration);
                _replica.Deactivate(battleGeneration);
            }

            _battleGeneration = 0;
            _role = ClientBattleRole.None;
            _actorSlot = -1;
            _baselineReady = false;
            _correctionVisible = false;
            _cameraMode = ClientBattleCameraMode.Exploration;
            _lastSnapshotAtMilliseconds = 0;
        }

        /// <summary>把 current replica entity set 写入 remote interpolation buffers。</summary>
        private void AddRemoteSamples(ClientGameplayReplicaSnapshot replica)
        {
            for (var index = 0; index < replica.Entities.Count; index++)
            {
                _interpolation.AddSample(
                    replica.BattleGeneration,
                    replica.ServerTick,
                    replica.Entities[index]);
            }
        }

        /// <summary>判断 generation 是否仍由 current runtime owner 接受。</summary>
        private bool IsCurrent(long battleGeneration)
        {
            lock (_sync)
            {
                return !_stopped &&
                       battleGeneration > 0 &&
                       battleGeneration == _battleGeneration;
            }
        }

        /// <summary>判断 async intent 是否仍 current。</summary>
        private bool IsCurrentIntent(long intentGeneration)
        {
            lock (_sync)
            {
                return IsCurrentIntentLocked(intentGeneration);
            }
        }

        /// <summary>在 runtime 锁内判断 async intent 是否仍 current。</summary>
        private bool IsCurrentIntentLocked(long intentGeneration)
        {
            return !_stopped &&
                   intentGeneration > 0 &&
                   intentGeneration == _intentGeneration;
        }

        /// <summary>
        /// 接受同一 world/Visit identity 上由 token refresh 推进的 Session generation。
        /// </summary>
        private static bool IsSameTargetLineage(
            ClientBattleTargetIntent requested,
            ClientBattleTargetIntent current)
        {
            return requested != null &&
                   current != null &&
                   current.SessionGeneration >= requested.SessionGeneration &&
                   requested.Kind == current.Kind &&
                   string.Equals(
                       requested.PersonalWorldID,
                       current.PersonalWorldID,
                       StringComparison.Ordinal) &&
                   string.Equals(
                       requested.WorldInstanceID,
                       current.WorldInstanceID,
                       StringComparison.Ordinal) &&
                   requested.AssignmentGeneration ==
                       current.AssignmentGeneration &&
                   string.Equals(
                       requested.VisitSessionID,
                       current.VisitSessionID,
                       StringComparison.Ordinal) &&
                   requested.VisitRevision == current.VisitRevision;
        }

        /// <summary>识别可以在 world membership 不变时执行 battle-only retry 的失败。</summary>
        private static bool IsRecoverable(ClientBattleFailure failure)
        {
            return failure == ClientBattleFailure.Timeout ||
                   failure == ClientBattleFailure.Transport ||
                   failure == ClientBattleFailure.Backpressure;
        }
    }
}
