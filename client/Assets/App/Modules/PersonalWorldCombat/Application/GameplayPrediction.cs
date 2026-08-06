using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using IHomeland.Client.Core.Foundation.Lifetime;

namespace IHomeland.Client.PersonalWorldCombat.Application
{
    /// <summary>
    /// 保存 local actor 的最小 kinematic prediction state。
    /// </summary>
    internal readonly struct ClientPredictedActorState
    {
        /// <summary>
        /// 创建与特定 InputTick 对齐的预测状态。
        /// </summary>
        internal ClientPredictedActorState(
            ulong inputTick,
            ClientBattleTransform transform,
            bool grounded)
        {
            InputTick = inputTick;
            Transform = transform;
            Grounded = grounded;
        }

        /// <summary>获取该状态已应用的最大 InputTick。</summary>
        internal ulong InputTick { get; }

        /// <summary>获取不含 Unity Physics 事实的量化 transform。</summary>
        internal ClientBattleTransform Transform { get; }

        /// <summary>获取本地预测使用的接地候选，不替代 authority。</summary>
        internal bool Grounded { get; }
    }

    /// <summary>
    /// 保存 prediction owner 的低敏 immutable 快照。
    /// </summary>
    internal sealed class ClientGameplayPredictionSnapshot
    {
        /// <summary>
        /// 创建有界 prediction 快照。
        /// </summary>
        internal ClientGameplayPredictionSnapshot(
            long battleGeneration,
            bool inputEnabled,
            bool baselineRequired,
            bool clockOverrun,
            ulong lastSentInputTick,
            ulong lastAcknowledgedInputTick,
            ulong continuityAcknowledgementAnchor,
            ulong acknowledgementAcceptanceFrontier,
            int inputHistoryItems,
            int predictedHistoryItems,
            ulong reconciliationCount,
            int lastReconciliationPositionDeltaMillimeters,
            int maximumReconciliationPositionDeltaMillimeters,
            int lastReconciliationYawDeltaMillidegrees,
            ulong lastJumpInputTick,
            ulong lastJumpSimulationTick,
            bool lastJumpPredicted,
            ClientPredictedActorState state,
            ClientBattleTransform presentationTransform)
        {
            if (battleGeneration < 0 ||
                continuityAcknowledgementAnchor >
                    lastAcknowledgedInputTick ||
                lastAcknowledgedInputTick >
                    acknowledgementAcceptanceFrontier ||
                lastSentInputTick >
                    acknowledgementAcceptanceFrontier ||
                continuityAcknowledgementAnchor >
                    acknowledgementAcceptanceFrontier ||
                inputHistoryItems < 0 ||
                inputHistoryItems > ClientBattlePolicy.Current.HistoryTicks ||
                predictedHistoryItems < 0 ||
                predictedHistoryItems > ClientBattlePolicy.Current.HistoryTicks ||
                lastReconciliationPositionDeltaMillimeters < 0 ||
                maximumReconciliationPositionDeltaMillimeters <
                    lastReconciliationPositionDeltaMillimeters ||
                lastReconciliationYawDeltaMillidegrees < 0 ||
                lastReconciliationYawDeltaMillidegrees > 180000 ||
                (lastJumpInputTick == 0) !=
                    (lastJumpSimulationTick == 0))
            {
                throw new ArgumentException("Client gameplay prediction snapshot is invalid.");
            }

            BattleGeneration = battleGeneration;
            InputEnabled = inputEnabled;
            BaselineRequired = baselineRequired;
            ClockOverrun = clockOverrun;
            LastSentInputTick = lastSentInputTick;
            LastAcknowledgedInputTick = lastAcknowledgedInputTick;
            ContinuityAcknowledgementAnchor =
                continuityAcknowledgementAnchor;
            AcknowledgementAcceptanceFrontier =
                acknowledgementAcceptanceFrontier;
            InputHistoryItems = inputHistoryItems;
            PredictedHistoryItems = predictedHistoryItems;
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
            State = state;
            PresentationTransform = presentationTransform;
        }

        /// <summary>获取 current battle generation。</summary>
        internal long BattleGeneration { get; }

        /// <summary>获取是否允许生成 input。</summary>
        internal bool InputEnabled { get; }

        /// <summary>获取是否必须等待新的 authority baseline。</summary>
        internal bool BaselineRequired { get; }

        /// <summary>获取 current generation 是否发生 clock overrun。</summary>
        internal bool ClockOverrun { get; }

        /// <summary>获取已成功交给 send queue 的 input frontier。</summary>
        internal ulong LastSentInputTick { get; }

        /// <summary>获取完整 snapshot set 已确认的 input frontier。</summary>
        internal ulong LastAcknowledgedInputTick { get; }

        /// <summary>获取 lost-continuity 恢复时采用的服务器终结前沿。</summary>
        internal ulong ContinuityAcknowledgementAnchor { get; }

        /// <summary>获取已发送或由本地时钟前向跳过的最大InputTick。</summary>
        internal ulong AcknowledgementAcceptanceFrontier { get; }

        /// <summary>获取未确认 input history 项数。</summary>
        internal int InputHistoryItems { get; }

        /// <summary>获取 predicted state history 项数。</summary>
        internal int PredictedHistoryItems { get; }

        /// <summary>获取current generation成功reconciliation次数。</summary>
        internal ulong ReconciliationCount { get; }

        /// <summary>获取最近一次reconciliation前后local target位置差，单位毫米。</summary>
        internal int LastReconciliationPositionDeltaMillimeters { get; }

        /// <summary>获取current generation最大的local target位置差，单位毫米。</summary>
        internal int MaximumReconciliationPositionDeltaMillimeters { get; }

        /// <summary>获取最近一次reconciliation前后yaw差，单位millidegree。</summary>
        internal int LastReconciliationYawDeltaMillidegrees { get; }

        /// <summary>获取最近一次成功发送jump edge的InputTick；零表示尚未发送。</summary>
        internal ulong LastJumpInputTick { get; }

        /// <summary>获取最近一次jump edge映射的SimulationTick；零表示尚未发送。</summary>
        internal ulong LastJumpSimulationTick { get; }

        /// <summary>获取最近一次jump edge是否产生airborne local prediction。</summary>
        internal bool LastJumpPredicted { get; }

        /// <summary>获取 current local predicted state。</summary>
        internal ClientPredictedActorState State { get; }

        /// <summary>
        /// 获取按唯一fixed-timestep相位重建的current local表现transform。
        /// </summary>
        /// <remarks>
        /// 该值只服务Scene表现，不替代prediction horizon、authority或碰撞事实。
        /// </remarks>
        internal ClientBattleTransform PresentationTransform { get; }
    }

    /// <summary>
    /// 唯一拥有 local input cadence、ack frontier 与 predicted history 的纯 C# owner。
    /// </summary>
    /// <remarks>
    /// Scene host 只提交 semantic sample；该 owner 在固定25 ms cadence生成command，并把映射到
    /// 同一50 ms SimulationTick的frame从共同起点fold后只积分一次。发送成功后才推进sent
    /// frontier。每次authority reconcile只向前校准尚未生成的InputTick，避免本地时钟漂移把
    /// 新input映射到已提交Tick。任何clock overrun、history/backpressure overflow或非法ack都
    /// 关闭input gate并等待current generation的authority full baseline。
    /// </remarks>
    internal sealed class GameplayPrediction : IAppTickable
    {
        /// <summary>保护 Scene sample、history 与 generation state。</summary>
        private readonly object _sync = new object();

        /// <summary>保存冻结 policy。</summary>
        private readonly ClientBattlePolicy _policy;

        /// <summary>保存唯一 battle gameplay send port。</summary>
        private readonly IClientBattleGameplayPort _network;

        /// <summary>保存已成功发送且尚在有限历史内的 frame。</summary>
        private readonly List<ClientBattleInputFrame> _inputHistory =
            new List<ClientBattleInputFrame>();

        /// <summary>保存每个已发送 InputTick 后的 predicted state。</summary>
        private readonly List<ClientPredictedActorState> _predictedHistory =
            new List<ClientPredictedActorState>();

        /// <summary>保存 current generation，零表示未激活。</summary>
        private long _battleGeneration;

        /// <summary>保存下一 one-based InputTick。</summary>
        private ulong _nextInputTick = 1;

        /// <summary>保存下一 one-based command sequence。</summary>
        private ulong _nextCommandSequence = 1;

        /// <summary>保存已成功进入 send queue 的最大 InputTick。</summary>
        private ulong _lastSentInputTick;

        /// <summary>保存完整 snapshot set 单调确认的最大 InputTick。</summary>
        private ulong _lastAcknowledgedInputTick;

        /// <summary>保存 lost-continuity 恢复时服务器已终结的 input frontier。</summary>
        private ulong _continuityAcknowledgementAnchor;

        /// <summary>保存客户端已发送或显式跳过、可接受authority终结的InputTick前沿。</summary>
        private ulong _acknowledgementAcceptanceFrontier;

        /// <summary>保存客户端已原子应用的最新 server Tick。</summary>
        private ulong _latestObservedServerTick;

        /// <summary>保存尚未生成 InputTick 的 monotonic frame 累积毫秒。</summary>
        private double _accumulatorMilliseconds;

        /// <summary>保存 Scene host 最新 continuous sample 与尚未消费 edge。</summary>
        private ClientBattleSemanticInput _pendingInput;

        /// <summary>保存 current predicted actor state。</summary>
        private ClientPredictedActorState _currentState;

        /// <summary>保存current local表现段开始时已提交的连续pose。</summary>
        private ClientBattleTransform _presentationSegmentStart;

        /// <summary>保存current local表现段对应的predicted horizon endpoint。</summary>
        private ClientBattleTransform _presentationSegmentEnd;

        /// <summary>保存current表现段已经消费的render时间，单位毫秒。</summary>
        private double _presentationElapsedMilliseconds;

        /// <summary>保存current表现段覆盖的SimulationTick时间，单位毫秒。</summary>
        private double _presentationDurationMilliseconds;

        /// <summary>保存current表现endpoint对应的SimulationTick。</summary>
        private ulong _presentationTargetSimulationTick;

        /// <summary>保存current authority publication后的render时间，单位毫秒。</summary>
        private double _presentationAuthorityElapsedMilliseconds;

        /// <summary>保存authority horizon已知的最后连续semantic sample。</summary>
        private ClientBattleSemanticInput _authorityContinuousInput;

        /// <summary>保存最后连续sample被server timeline应用的SimulationTick。</summary>
        private ulong _authorityContinuousSimulationTick;

        /// <summary>保存最近一次 authority reconciliation 的重演基点。</summary>
        private ClientPredictedActorState _authoritativeState;

        /// <summary>表示 full baseline 与 Scene gate 均允许 input。</summary>
        private bool _inputEnabled;

        /// <summary>表示 prediction 连续性已失效，必须等待 baseline。</summary>
        private bool _baselineRequired;

        /// <summary>表示current generation的local actor已由authority判定死亡。</summary>
        private bool _authorityDead;

        /// <summary>表示 current generation 曾发生 frame cadence overrun。</summary>
        private bool _clockOverrun;

        /// <summary>保存current generation成功reconciliation次数。</summary>
        private ulong _reconciliationCount;

        /// <summary>保存最近一次reconciliation引起的local target位置差，单位毫米。</summary>
        private int _lastReconciliationPositionDeltaMillimeters;

        /// <summary>保存current generation最大的local target位置差，单位毫米。</summary>
        private int _maximumReconciliationPositionDeltaMillimeters;

        /// <summary>保存最近一次reconciliation引起的yaw差，单位millidegree。</summary>
        private int _lastReconciliationYawDeltaMillidegrees;

        /// <summary>保存最近一次成功发送jump edge的InputTick。</summary>
        private ulong _lastJumpInputTick;

        /// <summary>保存最近一次jump edge映射的SimulationTick。</summary>
        private ulong _lastJumpSimulationTick;

        /// <summary>表示最近一次jump edge是否产生airborne local prediction。</summary>
        private bool _lastJumpPredicted;

        /// <summary>
        /// 创建不依赖 Unity API 的 prediction owner。
        /// </summary>
        internal GameplayPrediction(
            ClientBattlePolicy policy,
            IClientBattleGameplayPort network)
        {
            _policy = policy ?? throw new ArgumentNullException(nameof(policy));
            _network = network ?? throw new ArgumentNullException(nameof(network));
        }

        /// <summary>
        /// 以 authenticated accept 与完整 local baseline 原子激活 successor generation。
        /// </summary>
        /// <param name="battleGeneration">新认证transport generation。</param>
        /// <param name="authoritativeTransform">首个full baseline的local transform。</param>
        /// <param name="grounded">首个full baseline的authority grounded。</param>
        /// <param name="latestServerTick">首个full baseline的committed ServerTick。</param>
        /// <param name="lastProcessedInputTick">
        /// 同actor跨transport successor仍连续的authority acknowledgement anchor。
        /// </param>
        /// <param name="authorityDead">首个full baseline是否已确认local actor死亡。</param>
        internal void Activate(
            long battleGeneration,
            ClientBattleTransform authoritativeTransform,
            bool grounded,
            ulong latestServerTick,
            ulong lastProcessedInputTick = 0,
            bool authorityDead = false)
        {
            if (battleGeneration <= 0 || latestServerTick == 0)
            {
                throw new ArgumentOutOfRangeException(nameof(battleGeneration));
            }

            lock (_sync)
            {
                if (battleGeneration <= _battleGeneration)
                {
                    throw new InvalidOperationException(
                        "GameplayPrediction requires a successor battle generation.");
                }

                ResetLocked();
                _battleGeneration = battleGeneration;
                _latestObservedServerTick = latestServerTick;
                _nextInputTick =
                    _policy.FirstInputTickAfterBaseline(latestServerTick);
                _lastAcknowledgedInputTick =
                    lastProcessedInputTick;
                _continuityAcknowledgementAnchor =
                    lastProcessedInputTick;
                if (_nextInputTick <=
                    _continuityAcknowledgementAnchor)
                {
                    _nextInputTick = checked(
                        _continuityAcknowledgementAnchor + 1);
                }

                _acknowledgementAcceptanceFrontier = Math.Max(
                    _continuityAcknowledgementAnchor,
                    _nextInputTick - 1);
                _authoritativeState = new ClientPredictedActorState(
                    inputTick: 0,
                    authoritativeTransform,
                    grounded);
                _currentState = _authoritativeState;
                FreezePresentationLocked(
                    authoritativeTransform,
                    latestServerTick);
                _authorityDead = authorityDead;
                _inputEnabled = !authorityDead;
            }
        }

        /// <summary>
        /// 提交 Scene Input host 的最新 semantic sample。
        /// </summary>
        /// <remarks>
        /// 离散 edge 在下一个 InputTick 前使用 OR 合并，避免渲染帧快于 input cadence 时丢失。
        /// </remarks>
        internal bool TrySetInput(long battleGeneration, ClientBattleSemanticInput input)
        {
            lock (_sync)
            {
                if (!_inputEnabled ||
                    _baselineRequired ||
                    battleGeneration != _battleGeneration)
                {
                    return false;
                }

                _pendingInput = new ClientBattleSemanticInput(
                    input.MoveXMilli,
                    input.MoveYMilli,
                    input.AimYawMillidegrees,
                    input.AimPitchMillidegrees,
                    _pendingInput.JumpPressed || input.JumpPressed,
                    _pendingInput.PrimaryPressed || input.PrimaryPressed,
                    _pendingInput.SecondaryPressed || input.SecondaryPressed,
                    _pendingInput.InteractPressed || input.InteractPressed,
                    input.InteractPressed
                        ? input.InteractionSlot
                        : _pendingInput.InteractionSlot,
                    _pendingInput.SwitchWeaponPressed ||
                        input.SwitchWeaponPressed);
                return true;
            }
        }

        /// <summary>
        /// 按冻结 25 ms cadence 有界生成 input，禁止单帧 catch-up burst。
        /// </summary>
        public void Tick(float unscaledDeltaTimeSeconds)
        {
            if (float.IsNaN(unscaledDeltaTimeSeconds) ||
                float.IsInfinity(unscaledDeltaTimeSeconds) ||
                unscaledDeltaTimeSeconds < 0)
            {
                throw new ArgumentOutOfRangeException(nameof(unscaledDeltaTimeSeconds));
            }

            lock (_sync)
            {
                AdvancePresentationLocked(
                    unscaledDeltaTimeSeconds * 1000.0);
                if (!_inputEnabled || _baselineRequired || _battleGeneration == 0)
                {
                    return;
                }

                _accumulatorMilliseconds += unscaledDeltaTimeSeconds * 1000.0;
                var availableSteps = (int)Math.Floor(
                    _accumulatorMilliseconds / _policy.InputStepMilliseconds);
                if (availableSteps == 0)
                {
                    return;
                }

                if (!CanProduceWithinAuthorityHorizonLocked())
                {
                    ClampAuthorityPacedAccumulatorLocked();
                    return;
                }

                if (availableSteps > _policy.MaximumInputStepsPerFrame)
                {
                    InvalidateContinuityLocked(clockOverrun: true);
                    return;
                }

                for (var step = 0; step < availableSteps; step++)
                {
                    if (!CanProduceWithinAuthorityHorizonLocked())
                    {
                        ClampAuthorityPacedAccumulatorLocked();
                        break;
                    }

                    if (!ProduceInputLocked())
                    {
                        return;
                    }

                    _accumulatorMilliseconds -= _policy.InputStepMilliseconds;
                    _pendingInput = _pendingInput.WithoutEdges();
                }
            }
        }

        /// <summary>
        /// 判断尚未生成的InputTick是否仍在最新已观察authority的冻结early window内。
        /// </summary>
        /// <remarks>
        /// 该gate只约束未来输入生成，不修改已发送history、ack或pending edge。客户端本地
        /// 时钟短时快于simulation时必须等待新snapshot，不能持续发送必然TooEarly的输入。
        /// </remarks>
        private bool CanProduceWithinAuthorityHorizonLocked()
        {
            return _nextInputTick != 0 &&
                _latestObservedServerTick != 0 &&
                _nextInputTick <=
                    _policy.LastInputTickAtObservedHorizon(
                        _latestObservedServerTick);
        }

        /// <summary>
        /// 在authority pacing等待期间只保留一个InputTick的clock credit。
        ///
        /// Snapshot按10 Hz发布而input按40 Hz采样；等待期间若保留最大frame burst额度，
        /// 每次authority放行都会在同一render frame补发四个sample，并把离散prediction
        /// horizon伪装成可见时间。一个InputTick credit足以在放行后立即恢复，同时禁止
        /// authority cadence制造catch-up burst。
        /// </summary>
        private void ClampAuthorityPacedAccumulatorLocked()
        {
            _accumulatorMilliseconds = Math.Min(
                _accumulatorMilliseconds,
                _policy.InputStepMilliseconds);
        }

        /// <summary>
        /// 在完整 snapshot partition set 原子发布后推进 ack 并重演未确认输入。
        /// </summary>
        /// <param name="battleGeneration">必须匹配 current generation。</param>
        /// <param name="lastProcessedInputTick">Field 7 显式存在的 ack，允许初始零。</param>
        /// <param name="latestServerTick">完整集合共享的 authority Tick。</param>
        /// <param name="authoritativeState">Current local actor 的 authority state。</param>
        /// <param name="grounded">Authority state 对应的接地事实。</param>
        /// <param name="authorityDead">Authority是否已确认local actor死亡。</param>
        /// <returns>阈值外 hard correction 时为 true；非法 ack 抛出 protocol exception。</returns>
        internal bool Reconcile(
            long battleGeneration,
            ulong lastProcessedInputTick,
            ulong latestServerTick,
            ClientBattleTransform authoritativeState,
            bool grounded,
            bool authorityDead = false)
        {
            lock (_sync)
            {
                if (battleGeneration != _battleGeneration ||
                    _battleGeneration == 0)
                {
                    return false;
                }

                var continuityReset = _baselineRequired;
                var acceptedFrontier =
                    Math.Max(
                        _acknowledgementAcceptanceFrontier,
                        _policy.LastGapExpiredInputTick(
                            latestServerTick));
                if (lastProcessedInputTick < _lastAcknowledgedInputTick ||
                    (!continuityReset &&
                        lastProcessedInputTick > acceptedFrontier) ||
                    latestServerTick < _latestObservedServerTick)
                {
                    InvalidateContinuityLocked(clockOverrun: false);
                    throw new ClientBattlePredictionProtocolException(
                        "Snapshot acknowledgement violates the current sent frontier: " +
                        $"ack={lastProcessedInputTick} " +
                        $"last_ack={_lastAcknowledgedInputTick} " +
                        $"accepted={acceptedFrontier} " +
                        $"server_tick={latestServerTick} " +
                        $"latest_server_tick={_latestObservedServerTick} " +
                        $"continuity_reset={continuityReset}.");
                }

                var before = _currentState.Transform;
                _authorityDead |= authorityDead;
                if (_authorityDead)
                {
                    CommitAuthorityDeathLocked(
                        continuityReset,
                        lastProcessedInputTick,
                        latestServerTick,
                        authoritativeState,
                        grounded);
                    RecordReconciliationLocked(
                        before,
                        _currentState.Transform);
                    return IsHardCorrection(
                        before,
                        _currentState.Transform);
                }

                if (continuityReset)
                {
                    _inputHistory.Clear();
                    _predictedHistory.Clear();
                    _lastAcknowledgedInputTick = lastProcessedInputTick;
                    _continuityAcknowledgementAnchor =
                        lastProcessedInputTick;
                    _latestObservedServerTick = latestServerTick;
                    _authoritativeState = new ClientPredictedActorState(
                        lastProcessedInputTick,
                        authoritativeState,
                        grounded);
                    _currentState = _authoritativeState;
                    _authorityContinuousInput = default;
                    _authorityContinuousSimulationTick = 0;
                    FreezePresentationLocked(
                        authoritativeState,
                        latestServerTick);
                    var nextInputTick =
                        _policy.FirstInputTickAfterBaseline(
                            latestServerTick);
                    if (nextInputTick <=
                        _continuityAcknowledgementAnchor)
                    {
                        nextInputTick = checked(
                            _continuityAcknowledgementAnchor + 1);
                    }

                    if (nextInputTick <= _lastSentInputTick)
                    {
                        nextInputTick = checked(
                            _lastSentInputTick + 1);
                    }

                    _nextInputTick = nextInputTick;
                    _acknowledgementAcceptanceFrontier = Math.Max(
                        Math.Max(
                            _continuityAcknowledgementAnchor,
                            _lastSentInputTick),
                        _nextInputTick - 1);
                    _baselineRequired = false;
                    _clockOverrun = false;
                    _inputEnabled = true;
                    _accumulatorMilliseconds = 0;
                    RecordReconciliationLocked(
                        before,
                        _currentState.Transform);
                    return IsHardCorrection(
                        before,
                        _currentState.Transform);
                }

                _lastAcknowledgedInputTick = lastProcessedInputTick;
                AdvanceAuthorityContinuousInputLocked(latestServerTick);
                _latestObservedServerTick = latestServerTick;
                _presentationAuthorityElapsedMilliseconds = 0;
                PruneAcknowledgedLocked(lastProcessedInputTick);
                AlignUnsentInputHorizonLocked(latestServerTick);

                _authoritativeState = new ClientPredictedActorState(
                    lastProcessedInputTick,
                    authoritativeState,
                    grounded);
                RebuildPredictionLocked();
                RecordReconciliationLocked(
                    before,
                    _currentState.Transform);
                return IsHardCorrection(before, _currentState.Transform);
            }
        }

        /// <summary>
        /// 关闭 current generation 并清除全部 history/sample。
        /// </summary>
        internal void Deactivate(long battleGeneration)
        {
            lock (_sync)
            {
                if (battleGeneration != _battleGeneration)
                {
                    return;
                }

                ResetLocked();
                _battleGeneration = 0;
            }
        }

        /// <summary>
        /// 读取不产生 send 副作用的低敏 prediction 快照。
        /// </summary>
        internal ClientGameplayPredictionSnapshot Snapshot()
        {
            lock (_sync)
            {
                return new ClientGameplayPredictionSnapshot(
                    _battleGeneration,
                    _inputEnabled,
                    _baselineRequired,
                    _clockOverrun,
                    _lastSentInputTick,
                    _lastAcknowledgedInputTick,
                    _continuityAcknowledgementAnchor,
                    _acknowledgementAcceptanceFrontier,
                    _inputHistory.Count,
                    _predictedHistory.Count,
                    _reconciliationCount,
                    _lastReconciliationPositionDeltaMillimeters,
                    _maximumReconciliationPositionDeltaMillimeters,
                    _lastReconciliationYawDeltaMillidegrees,
                    _lastJumpInputTick,
                    _lastJumpSimulationTick,
                    _lastJumpPredicted,
                    _currentState,
                    CreatePresentationTransformLocked());
            }
        }

        /// <summary>
        /// 返回 current 未确认 input history 的不可变测试投影。
        /// </summary>
        internal IReadOnlyList<ClientBattleInputFrame> InputHistorySnapshot()
        {
            lock (_sync)
            {
                return new ReadOnlyCollection<ClientBattleInputFrame>(
                    _inputHistory.ToArray());
            }
        }

        /// <summary>
        /// 生成一个 frame、有限冗余 bundle 与下一 predicted state。
        /// </summary>
        private bool ProduceInputLocked()
        {
            if (_nextInputTick == 0 ||
                _nextCommandSequence == 0 ||
                _inputHistory.Count >= _policy.HistoryTicks ||
                _predictedHistory.Count >= _policy.HistoryTicks)
            {
                InvalidateContinuityLocked(clockOverrun: false);
                return false;
            }

            var frame = CreateFrameLocked(_nextInputTick, _pendingInput);
            var candidates = new List<ClientBattleInputFrame>(
                Math.Min(_policy.InputBundleDepth, _inputHistory.Count + 1));
            var first = _inputHistory.Count;
            var expectedInputTick = frame.InputTick;
            while (first > 0 &&
                   _inputHistory.Count - first <
                       _policy.InputBundleDepth - 1)
            {
                var previous = _inputHistory[first - 1];
                if (previous.InputTick != expectedInputTick - 1)
                {
                    break;
                }

                first--;
                expectedInputTick = previous.InputTick;
            }

            for (var index = first; index < _inputHistory.Count; index++)
            {
                candidates.Add(_inputHistory[index]);
            }

            candidates.Add(frame);
            while (CountCommands(candidates) > 8 && candidates.Count > 1)
            {
                candidates.RemoveAt(0);
            }

            var bundle = new ClientBattleInputBundle(
                frame.InputTick,
                _latestObservedServerTick,
                candidates);
            if (!_network.TrySendInput(_battleGeneration, bundle))
            {
                InvalidateContinuityLocked(clockOverrun: false);
                return false;
            }

            _inputHistory.Add(frame);
            RebuildPredictionLocked();
            if (_pendingInput.JumpPressed)
            {
                _lastJumpInputTick = frame.InputTick;
                _lastJumpSimulationTick = frame.SimulationTick;
                _lastJumpPredicted =
                    !_currentState.Grounded &&
                    _currentState.Transform.
                        VelocityYMillimetersPerSecond > 0;
            }

            _lastSentInputTick = frame.InputTick;
            _acknowledgementAcceptanceFrontier = Math.Max(
                _acknowledgementAcceptanceFrontier,
                frame.InputTick);
            if (_nextInputTick == ulong.MaxValue)
            {
                _nextInputTick = 0;
            }
            else
            {
                _nextInputTick++;
            }

            return true;
        }

        /// <summary>
        /// 把尚未生成的InputTick只向前推进到current authority的冻结lead horizon。
        /// </summary>
        /// <remarks>
        /// 已发送history、ack、sequence与pending edge均保持原所有权。跳过的InputTick由server
        /// 既有gap expiry收口；新bundle只会从连续history尾段构造，不能跨越该空洞。
        /// </remarks>
        /// <param name="latestServerTick">本次完整authority publication的ServerTick。</param>
        private void AlignUnsentInputHorizonLocked(ulong latestServerTick)
        {
            if (_nextInputTick == 0)
            {
                return;
            }

            var minimumInputTick =
                _policy.FirstInputTickAfterBaseline(latestServerTick);
            if (_nextInputTick < minimumInputTick)
            {
                _nextInputTick = minimumInputTick;
                _acknowledgementAcceptanceFrontier = Math.Max(
                    _acknowledgementAcceptanceFrontier,
                    minimumInputTick - 1);
            }
        }

        /// <summary>
        /// 为一个 InputTick 创建 move、aim 与可选 edge command。
        /// </summary>
        private ClientBattleInputFrame CreateFrameLocked(
            ulong inputTick,
            ClientBattleSemanticInput input)
        {
            var simulationTick = _policy.MapInputToSimulationTick(inputTick);
            var commands = new List<ClientBattleInputCommand>(7)
            {
                CreateCommand(
                    inputTick,
                    simulationTick,
                    ClientBattleInputKind.Move,
                    input.MoveXMilli,
                    input.MoveYMilli,
                    0,
                    0,
                    0),
                CreateCommand(
                    inputTick,
                    simulationTick,
                    ClientBattleInputKind.Aim,
                    0,
                    0,
                    input.AimYawMillidegrees,
                    input.AimPitchMillidegrees,
                    0),
            };
            if (input.JumpPressed)
            {
                commands.Add(CreateEdge(inputTick, simulationTick, ClientBattleInputKind.Jump));
            }

            if (input.PrimaryPressed)
            {
                commands.Add(CreateEdge(
                    inputTick,
                    simulationTick,
                    ClientBattleInputKind.PrimaryAbility));
            }

            if (input.SecondaryPressed)
            {
                commands.Add(CreateEdge(
                    inputTick,
                    simulationTick,
                    ClientBattleInputKind.SecondaryAbility));
            }

            if (input.InteractPressed)
            {
                commands.Add(CreateCommand(
                    inputTick,
                    simulationTick,
                    ClientBattleInputKind.Interact,
                    0,
                    0,
                    0,
                    0,
                    input.InteractionSlot));
            }

            if (input.SwitchWeaponPressed)
            {
                commands.Add(CreateEdge(
                    inputTick,
                    simulationTick,
                    ClientBattleInputKind.SwitchWeapon));
            }

            return new ClientBattleInputFrame(inputTick, simulationTick, commands);
        }

        /// <summary>
        /// 创建不带参数的离散 edge command。
        /// </summary>
        private ClientBattleInputCommand CreateEdge(
            ulong inputTick,
            ulong simulationTick,
            ClientBattleInputKind kind)
        {
            return CreateCommand(
                inputTick,
                simulationTick,
                kind,
                0,
                0,
                0,
                0,
                0);
        }

        /// <summary>
        /// 分配单调 sequence 并创建一条 command。
        /// </summary>
        private ClientBattleInputCommand CreateCommand(
            ulong inputTick,
            ulong simulationTick,
            ClientBattleInputKind kind,
            int moveX,
            int moveY,
            int aimYaw,
            int aimPitch,
            uint interactionSlot)
        {
            if (_nextCommandSequence == 0)
            {
                throw new InvalidOperationException(
                    "Client battle command sequence is exhausted.");
            }

            var sequence = _nextCommandSequence;
            _nextCommandSequence =
                sequence == ulong.MaxValue ? 0 : sequence + 1;
            return new ClientBattleInputCommand(
                inputTick,
                simulationTick,
                sequence,
                kind,
                moveX,
                moveY,
                aimYaw,
                aimPitch,
                interactionSlot);
        }

        /// <summary>
        /// 计算候选 bundle 中的总 command 数。
        /// </summary>
        private static int CountCommands(IReadOnlyList<ClientBattleInputFrame> frames)
        {
            var total = 0;
            for (var index = 0; index < frames.Count; index++)
            {
                total = checked(total + frames[index].Commands.Count);
            }

            return total;
        }

        /// <summary>
        /// 从最近authority基点按SimulationTick组重建全部未确认prediction。
        /// </summary>
        /// <remarks>
        /// 同组首个frame可以立即预测整次50 ms step；后续frame只从该组起点重算，使25 ms
        /// input采样不会被错误地当作第二次Movement积分。映射Tick不晚于latest authority
        /// Tick的history可能只是被连续ack gap保留，不能从current authority state重复积分。
        /// </remarks>
        private void RebuildPredictionLocked()
        {
            _predictedHistory.Clear();
            var replay = _authoritativeState;
            var continuous = _authorityContinuousInput;
            var lastContinuousTick =
                _authorityContinuousSimulationTick;
            var replayedSimulationTick = _latestObservedServerTick;
            var horizonSimulationTick = _latestObservedServerTick;
            var index = 0;
            while (index < _inputHistory.Count)
            {
                var frame = _inputHistory[index];
                if (frame.SimulationTick <= _latestObservedServerTick)
                {
                    replay = new ClientPredictedActorState(
                        frame.InputTick,
                        replay.Transform,
                        replay.Grounded);
                    _predictedHistory.Add(replay);
                    index++;
                    continue;
                }

                var simulationTick = frame.SimulationTick;
                if (simulationTick <= replayedSimulationTick)
                {
                    throw new InvalidOperationException(
                        "Prediction history SimulationTick order is invalid.");
                }

                while (replayedSimulationTick + 1UL < simulationTick)
                {
                    replayedSimulationTick++;
                    var held = IsContinuousHeldAtTick(
                        lastContinuousTick,
                        replayedSimulationTick);
                    replay = IntegrateSimulationTick(
                        replay,
                        replay.InputTick,
                        held ? continuous.MoveXMilli : 0,
                        held ? continuous.MoveYMilli : 0,
                        replay.Transform.YawMillidegrees,
                        jump: false);
                }

                var groupStart = replay;
                var groupStartIndex = index;
                var groupEndIndex = index;
                while (groupEndIndex + 1 < _inputHistory.Count &&
                       _inputHistory[groupEndIndex + 1].SimulationTick ==
                            simulationTick)
                {
                    groupEndIndex++;
                }

                var moveX = IsContinuousHeldAtTick(
                        lastContinuousTick,
                        simulationTick)
                    ? continuous.MoveXMilli
                    : 0;
                var moveY = IsContinuousHeldAtTick(
                        lastContinuousTick,
                        simulationTick)
                    ? continuous.MoveYMilli
                    : 0;
                var yaw = groupStart.Transform.YawMillidegrees;
                var jump = false;
                for (var frameIndex = groupStartIndex;
                     frameIndex <= groupEndIndex;
                     frameIndex++)
                {
                    var candidate = _inputHistory[frameIndex];
                    ApplyContinuousFrame(
                        candidate,
                        ref continuous,
                        ref lastContinuousTick,
                        ref moveX,
                        ref moveY,
                        ref yaw,
                        ref jump);
                    replay = IntegrateSimulationTick(
                        groupStart,
                        candidate.InputTick,
                        moveX,
                        moveY,
                        yaw,
                        jump);
                    _predictedHistory.Add(replay);
                }

                replayedSimulationTick = simulationTick;
                horizonSimulationTick = simulationTick;
                index = groupEndIndex + 1;
            }

            _currentState = replay;
            RetargetPresentationLocked(
                replay.Transform,
                horizonSimulationTick);
        }

        /// <summary>
        /// 把已到达authority horizon的sent sample推进为continuous hold基点。
        /// </summary>
        /// <param name="latestServerTick">本次published authority Tick。</param>
        private void AdvanceAuthorityContinuousInputLocked(
            ulong latestServerTick)
        {
            var continuous = _authorityContinuousInput;
            var lastContinuousTick =
                _authorityContinuousSimulationTick;
            for (var index = 0; index < _inputHistory.Count; index++)
            {
                var frame = _inputHistory[index];
                if (frame.SimulationTick > latestServerTick ||
                    frame.SimulationTick < lastContinuousTick)
                {
                    continue;
                }

                var moveX = continuous.MoveXMilli;
                var moveY = continuous.MoveYMilli;
                var yaw = continuous.AimYawMillidegrees;
                var jump = false;
                ApplyContinuousFrame(
                    frame,
                    ref continuous,
                    ref lastContinuousTick,
                    ref moveX,
                    ref moveY,
                    ref yaw,
                    ref jump);
            }

            _authorityContinuousInput = continuous;
            _authorityContinuousSimulationTick = lastContinuousTick;
        }

        /// <summary>把一帧continuous/aim与jump edge折叠进current SimulationTick。</summary>
        /// <param name="frame">同组current input frame。</param>
        /// <param name="continuous">成功时返回最新continuous semantic sample。</param>
        /// <param name="lastContinuousTick">成功时返回sample应用Tick。</param>
        /// <param name="moveX">成功时返回fold后的Move X。</param>
        /// <param name="moveY">成功时返回fold后的Move Y。</param>
        /// <param name="yaw">成功时返回fold后的yaw。</param>
        /// <param name="jump">成功时返回同组OR jump。</param>
        private static void ApplyContinuousFrame(
            ClientBattleInputFrame frame,
            ref ClientBattleSemanticInput continuous,
            ref ulong lastContinuousTick,
            ref int moveX,
            ref int moveY,
            ref int yaw,
            ref bool jump)
        {
            var aimPitch = continuous.AimPitchMillidegrees;
            foreach (var command in frame.Commands)
            {
                switch (command.Kind)
                {
                    case ClientBattleInputKind.Move:
                        moveX = command.MoveXMilli;
                        moveY = command.MoveYMilli;
                        break;
                    case ClientBattleInputKind.Aim:
                        yaw = command.AimYawMillidegrees;
                        aimPitch = command.AimPitchMillidegrees;
                        break;
                    case ClientBattleInputKind.Jump:
                        jump = true;
                        break;
                }
            }

            continuous = new ClientBattleSemanticInput(
                moveX,
                moveY,
                yaw,
                aimPitch,
                jumpPressed: false,
                primaryPressed: false,
                secondaryPressed: false,
                interactPressed: false,
                interactionSlot: 0);
            lastContinuousTick = frame.SimulationTick;
        }

        /// <summary>判断server continuous-hold窗口在指定Tick是否仍有效。</summary>
        /// <param name="sampleTick">最后sample应用Tick；零表示不存在。</param>
        /// <param name="simulationTick">待预测的SimulationTick。</param>
        /// <returns>与C++ InputTimeline相同的闭区间hold结果。</returns>
        private bool IsContinuousHeldAtTick(
            ulong sampleTick,
            ulong simulationTick)
        {
            return sampleTick != 0 &&
                simulationTick >= sampleTick &&
                simulationTick - sampleTick <=
                    (ulong)_policy.ContinuousHoldTicks;
        }

        /// <summary>按authority锚点后的render时间推进current表现段。</summary>
        /// <param name="deltaMilliseconds">Current frame monotonic delta。</param>
        private void AdvancePresentationLocked(double deltaMilliseconds)
        {
            if (_battleGeneration == 0 || deltaMilliseconds <= 0)
            {
                return;
            }

            _presentationAuthorityElapsedMilliseconds = Math.Min(
                double.MaxValue - deltaMilliseconds,
                _presentationAuthorityElapsedMilliseconds) +
                deltaMilliseconds;
            _presentationElapsedMilliseconds = Math.Min(
                _presentationDurationMilliseconds,
                _presentationElapsedMilliseconds + deltaMilliseconds);
        }

        /// <summary>把new predicted horizon接到current连续表现pose。</summary>
        /// <param name="target">New predicted horizon endpoint。</param>
        /// <param name="simulationTick">Endpoint对应SimulationTick。</param>
        private void RetargetPresentationLocked(
            ClientBattleTransform target,
            ulong simulationTick)
        {
            if (simulationTick == _presentationTargetSimulationTick &&
                HasSamePose(_presentationSegmentEnd, target))
            {
                return;
            }

            var current = CreatePresentationTransformLocked();
            var horizonTicks = simulationTick > _latestObservedServerTick
                ? simulationTick - _latestObservedServerTick
                : 0UL;
            var horizonMilliseconds = Math.Min(
                (double)horizonTicks *
                    _policy.SimulationStepMilliseconds,
                _policy.SimulationStepMilliseconds *
                    (double)_policy.ContinuousHoldTicks);
            var duration = Math.Max(
                _policy.InputStepMilliseconds,
                horizonMilliseconds -
                    _presentationAuthorityElapsedMilliseconds);
            _presentationSegmentStart = current;
            _presentationSegmentEnd = target;
            _presentationElapsedMilliseconds = 0;
            _presentationDurationMilliseconds = duration;
            _presentationTargetSimulationTick = simulationTick;
        }

        /// <summary>读取current prediction-owned连续表现pose。</summary>
        /// <returns>只供Scene消费的量化transform。</returns>
        private ClientBattleTransform CreatePresentationTransformLocked()
        {
            var progress = _presentationDurationMilliseconds <= 0
                ? 1.0
                : Math.Max(
                    0.0,
                    Math.Min(
                        1.0,
                        _presentationElapsedMilliseconds /
                            _presentationDurationMilliseconds));
            return InterpolatePresentationTransform(
                _presentationSegmentStart,
                _presentationSegmentEnd,
                progress);
        }

        /// <summary>把local表现冻结在可信transform并重置authority render锚点。</summary>
        /// <param name="transform">冻结后的唯一Scene表现输入。</param>
        /// <param name="simulationTick">该transform对应的authority Tick。</param>
        private void FreezePresentationLocked(
            ClientBattleTransform transform,
            ulong simulationTick)
        {
            _presentationSegmentStart = transform;
            _presentationSegmentEnd = transform;
            _presentationElapsedMilliseconds = 0;
            _presentationDurationMilliseconds = 0;
            _presentationTargetSimulationTick = simulationTick;
            _presentationAuthorityElapsedMilliseconds = 0;
        }

        /// <summary>比较两个量化pose endpoint，velocity不定义可见pose。</summary>
        /// <param name="first">Current endpoint。</param>
        /// <param name="second">Candidate endpoint。</param>
        /// <returns>Position与yaw完全一致时返回true。</returns>
        private static bool HasSamePose(
            ClientBattleTransform first,
            ClientBattleTransform second)
        {
            return first.PositionXMillimeters == second.PositionXMillimeters &&
                first.PositionYMillimeters == second.PositionYMillimeters &&
                first.PositionZMillimeters == second.PositionZMillimeters &&
                first.YawMillidegrees == second.YawMillidegrees;
        }

        /// <summary>按最短yaw弧在两个量化SimulationTick端点间插值。</summary>
        /// <param name="start">Current segment起点。</param>
        /// <param name="end">Current segment终点。</param>
        /// <param name="progress">已clamp到闭区间的fixed-timestep相位。</param>
        /// <returns>毫米与millidegree量化的Scene表现transform。</returns>
        private static ClientBattleTransform InterpolatePresentationTransform(
            ClientBattleTransform start,
            ClientBattleTransform end,
            double progress)
        {
            var yawDelta = end.YawMillidegrees - start.YawMillidegrees;
            if (yawDelta >= 180000)
            {
                yawDelta -= 360000;
            }
            else if (yawDelta < -180000)
            {
                yawDelta += 360000;
            }

            return new ClientBattleTransform(
                InterpolateInteger(
                    start.PositionXMillimeters,
                    end.PositionXMillimeters,
                    progress),
                InterpolateInteger(
                    start.PositionYMillimeters,
                    end.PositionYMillimeters,
                    progress),
                InterpolateInteger(
                    start.PositionZMillimeters,
                    end.PositionZMillimeters,
                    progress),
                InterpolateInteger(
                    start.YawMillidegrees,
                    checked(start.YawMillidegrees + yawDelta),
                    progress),
                InterpolateInteger(
                    start.VelocityXMillimetersPerSecond,
                    end.VelocityXMillimetersPerSecond,
                    progress),
                InterpolateInteger(
                    start.VelocityYMillimetersPerSecond,
                    end.VelocityYMillimetersPerSecond,
                    progress),
                InterpolateInteger(
                    start.VelocityZMillimetersPerSecond,
                    end.VelocityZMillimetersPerSecond,
                    progress));
        }

        /// <summary>以确定性四舍五入插值一个有符号整数值。</summary>
        /// <param name="start">起点。</param>
        /// <param name="end">终点。</param>
        /// <param name="progress">闭区间相位。</param>
        /// <returns>量化后的插值结果。</returns>
        private static int InterpolateInteger(
            int start,
            int end,
            double progress)
        {
            var value = start + (((long)end - start) * progress);
            return checked((int)Math.Round(
                value,
                MidpointRounding.AwayFromZero));
        }

        /// <summary>
        /// 以server fold与50 ms整数kinematic规则预测一个SimulationTick。
        /// </summary>
        /// <param name="current">该SimulationTick开始前的authority或predicted state。</param>
        /// <param name="inputTick">该结果对应的latest InputTick。</param>
        /// <param name="moveX">Fold或hold后的Move X。</param>
        /// <param name="moveY">Fold或hold后的Move Y。</param>
        /// <param name="yaw">Fold或hold后的yaw。</param>
        /// <param name="jump">本SimulationTick是否包含jump edge。</param>
        /// <returns>应用一次完整SimulationTick后的state。</returns>
        private ClientPredictedActorState IntegrateSimulationTick(
            ClientPredictedActorState current,
            ulong inputTick,
            int moveX,
            int moveY,
            int yaw,
            bool jump)
        {
            ClampMove(ref moveX, ref moveY);
            var transform = current.Transform;
            var targetX = Scale(
                moveX,
                _policy.MaximumHorizontalSpeedMillimetersPerSecond,
                1000);
            var targetZ = Scale(
                moveY,
                _policy.MaximumHorizontalSpeedMillimetersPerSecond,
                1000);
            var acceleration = Scale(
                _policy.AccelerationMillimetersPerSecondSquared,
                _policy.SimulationStepMilliseconds,
                1000);
            var deceleration = Scale(
                _policy.DecelerationMillimetersPerSecondSquared,
                _policy.SimulationStepMilliseconds,
                1000);
            var velocityX = Approach(
                transform.VelocityXMillimetersPerSecond,
                targetX,
                moveX == 0 ? deceleration : acceleration);
            var velocityZ = Approach(
                transform.VelocityZMillimetersPerSecond,
                targetZ,
                moveY == 0 ? deceleration : acceleration);
            var grounded = current.Grounded;
            var velocityY = transform.VelocityYMillimetersPerSecond;
            if (jump && grounded)
            {
                velocityY = _policy.JumpSpeedMillimetersPerSecond;
                grounded = false;
            }
            else if (!grounded)
            {
                velocityY = checked(
                    velocityY -
                    Scale(
                        _policy.GravityMillimetersPerSecondSquared,
                        _policy.SimulationStepMilliseconds,
                        1000));
            }

            var deltaX = Scale(
                velocityX,
                _policy.SimulationStepMilliseconds,
                1000);
            var deltaY = Scale(
                velocityY,
                _policy.SimulationStepMilliseconds,
                1000);
            var deltaZ = Scale(
                velocityZ,
                _policy.SimulationStepMilliseconds,
                1000);
            var positionX = checked(
                transform.PositionXMillimeters + deltaX);
            var positionY = checked(
                transform.PositionYMillimeters + deltaY);
            var positionZ = checked(
                transform.PositionZMillimeters + deltaZ);
            if (transform.PositionYMillimeters >= 0 &&
                positionY < 0)
            {
                var fraction = GroundCrossingFraction(
                    transform.PositionYMillimeters,
                    positionY);
                positionX = checked(
                    transform.PositionXMillimeters +
                    ScaleByFraction(deltaX, fraction));
                positionY = checked(
                    transform.PositionYMillimeters +
                    ScaleByFraction(deltaY, fraction));
                positionZ = checked(
                    transform.PositionZMillimeters +
                    ScaleByFraction(deltaZ, fraction));
                if (deltaY < 0)
                {
                    velocityY = 0;
                }
            }

            grounded =
                velocityY <= 0 &&
                positionY >= 0 &&
                positionY <= 1;
            if (grounded && velocityY < 0)
            {
                velocityY = 0;
            }

            var next = new ClientBattleTransform(
                positionX,
                positionY,
                positionZ,
                yaw,
                velocityX,
                velocityY,
                velocityZ);
            return new ClientPredictedActorState(
                inputTick,
                next,
                grounded);
        }

        /// <summary>
        /// 判断 authority 重建前后的 transform 是否越过冻结 correction 阈值。
        /// </summary>
        private bool IsHardCorrection(
            ClientBattleTransform before,
            ClientBattleTransform after)
        {
            var thresholdSquared =
                (long)_policy.CorrectionPositionMillimeters *
                _policy.CorrectionPositionMillimeters;
            return before.PositionDistanceSquared(after) >
                    thresholdSquared ||
                before.YawDistance(after) >
                    _policy.CorrectionAngleMillidegrees;
        }

        /// <summary>
        /// 记录不含identity或payload的reconciliation表现差异。
        /// </summary>
        /// <param name="before">Authority提交前Scene可见的local prediction target。</param>
        /// <param name="after">从新authority基点重演后的local prediction target。</param>
        private void RecordReconciliationLocked(
            ClientBattleTransform before,
            ClientBattleTransform after)
        {
            _reconciliationCount = checked(_reconciliationCount + 1);
            var distance = Math.Sqrt(before.PositionDistanceSquared(after));
            _lastReconciliationPositionDeltaMillimeters =
                distance >= int.MaxValue
                    ? int.MaxValue
                    : (int)Math.Ceiling(distance);
            _maximumReconciliationPositionDeltaMillimeters = Math.Max(
                _maximumReconciliationPositionDeltaMillimeters,
                _lastReconciliationPositionDeltaMillimeters);
            _lastReconciliationYawDeltaMillidegrees =
                before.YawDistance(after);
        }

        /// <summary>
        /// 把二维 input clamp 到半径 1000 的圆。
        /// </summary>
        private static void ClampMove(ref int x, ref int y)
        {
            x = Math.Max(-1000, Math.Min(1000, x));
            y = Math.Max(-1000, Math.Min(1000, y));
            var xMagnitude = (ulong)Math.Abs((long)x);
            var yMagnitude = (ulong)Math.Abs((long)y);
            var lengthSquared =
                (xMagnitude * xMagnitude) +
                (yMagnitude * yMagnitude);
            if (lengthSquared <= 1000000UL)
            {
                return;
            }

            var length = checked((int)IntegerSquareRoot(lengthSquared));
            x = Scale(x, 1000, length);
            y = Scale(y, 1000, length);
        }

        /// <summary>
        /// 返回与server Movement相同的floor整数平方根。
        /// </summary>
        /// <param name="value">非负二维输入长度平方。</param>
        /// <returns>不大于真实平方根的最大整数。</returns>
        private static ulong IntegerSquareRoot(ulong value)
        {
            var result = 0UL;
            var bit = 1UL << 62;
            while (bit > value)
            {
                bit >>= 2;
            }

            var remainder = value;
            while (bit != 0)
            {
                if (remainder >= result + bit)
                {
                    remainder -= result + bit;
                    result = (result >> 1) + bit;
                }
                else
                {
                    result >>= 1;
                }

                bit >>= 2;
            }

            return result;
        }

        /// <summary>
        /// 量化一次从非负Y穿越current平地的百万分比fraction。
        /// </summary>
        /// <param name="startY">SimulationTick开始位置Y，单位毫米且非负。</param>
        /// <param name="endY">未裁剪candidate位置Y，单位毫米且小于零。</param>
        /// <returns>范围为[0, 1000000]的toward-zero crossing fraction。</returns>
        private static int GroundCrossingFraction(int startY, int endY)
        {
            var distance = (long)startY - endY;
            var scaled = ((long)startY * 1000000L) / distance;
            return checked((int)Math.Max(0L, Math.Min(1000000L, scaled)));
        }

        /// <summary>
        /// 使用server flat-ground adapter的百万分比toward-zero缩放位移。
        /// </summary>
        /// <param name="value">单次50 ms step的轴向位移，单位毫米。</param>
        /// <param name="fraction">范围为[0, 1000000]的crossing fraction。</param>
        /// <returns>碰撞前可提交的轴向位移，单位毫米。</returns>
        private static int ScaleByFraction(int value, int fraction)
        {
            return checked((int)(((long)value * fraction) / 1000000L));
        }

        /// <summary>
        /// 执行 toward-zero fixed unit scaling。
        /// </summary>
        private static int Scale(int value, int multiplier, int divisor)
        {
            return checked((int)(((long)value * multiplier) / divisor));
        }

        /// <summary>
        /// 以不越过 target 的固定 delta 接近目标。
        /// </summary>
        private static int Approach(int current, int target, int maximumDelta)
        {
            if (current < target)
            {
                return Math.Min(checked(current + maximumDelta), target);
            }

            if (current > target)
            {
                return Math.Max(checked(current - maximumDelta), target);
            }

            return current;
        }

        /// <summary>
        /// 删除已确认 frame，保留最多 16 个未确认项。
        /// </summary>
        private void PruneAcknowledgedLocked(ulong acknowledged)
        {
            _inputHistory.RemoveAll(frame => frame.InputTick <= acknowledged);
            _predictedHistory.RemoveAll(state => state.InputTick <= acknowledged);
        }

        /// <summary>
        /// 关闭 input gate并丢弃无法安全重演的 cadence。
        ///
        /// Render clock overrun 仍保留已采样 semantic edge；authority re-anchor 后只在
        /// 新 timeline 发送一次，避免卡顿帧吞掉玩家已按下的 Jump/ability。
        /// </summary>
        private void InvalidateContinuityLocked(bool clockOverrun)
        {
            var frozenPresentation = CreatePresentationTransformLocked();
            _inputEnabled = false;
            _baselineRequired = true;
            _clockOverrun |= clockOverrun;
            _inputHistory.Clear();
            _predictedHistory.Clear();
            _accumulatorMilliseconds = 0;
            FreezePresentationLocked(
                frozenPresentation,
                _presentationTargetSimulationTick);
            if (!clockOverrun)
            {
                _pendingInput = default;
            }
        }

        /// <summary>
        /// 原子提交current generation的authority死亡终态并丢弃全部本地输入连续性。
        /// </summary>
        /// <param name="continuityReset">提交前是否正在等待新的authority baseline。</param>
        /// <param name="lastProcessedInputTick">Authority已终结的input frontier。</param>
        /// <param name="latestServerTick">本次死亡事实对应的committed ServerTick。</param>
        /// <param name="authoritativeState">死亡actor的最终authority transform。</param>
        /// <param name="grounded">死亡actor的authority grounded事实。</param>
        private void CommitAuthorityDeathLocked(
            bool continuityReset,
            ulong lastProcessedInputTick,
            ulong latestServerTick,
            ClientBattleTransform authoritativeState,
            bool grounded)
        {
            _inputHistory.Clear();
            _predictedHistory.Clear();
            _pendingInput = default;
            _accumulatorMilliseconds = 0;
            _lastAcknowledgedInputTick = lastProcessedInputTick;
            if (continuityReset)
            {
                _continuityAcknowledgementAnchor = lastProcessedInputTick;
            }

            _acknowledgementAcceptanceFrontier = Math.Max(
                _acknowledgementAcceptanceFrontier,
                Math.Max(lastProcessedInputTick, _lastSentInputTick));
            _latestObservedServerTick = latestServerTick;
            _authoritativeState = new ClientPredictedActorState(
                lastProcessedInputTick,
                authoritativeState,
                grounded);
            _currentState = _authoritativeState;
            _authorityContinuousInput = default;
            _authorityContinuousSimulationTick = 0;
            FreezePresentationLocked(
                authoritativeState,
                latestServerTick);
            _inputEnabled = false;
            _baselineRequired = false;
            _clockOverrun = false;
        }

        /// <summary>
        /// 清除 current generation 的所有 mutable state。
        /// </summary>
        private void ResetLocked()
        {
            _inputHistory.Clear();
            _predictedHistory.Clear();
            _nextInputTick = 1;
            _nextCommandSequence = 1;
            _lastSentInputTick = 0;
            _lastAcknowledgedInputTick = 0;
            _continuityAcknowledgementAnchor = 0;
            _acknowledgementAcceptanceFrontier = 0;
            _latestObservedServerTick = 0;
            _accumulatorMilliseconds = 0;
            _pendingInput = default;
            _currentState = default;
            _authoritativeState = default;
            _presentationSegmentStart = default;
            _presentationSegmentEnd = default;
            _presentationElapsedMilliseconds = 0;
            _presentationDurationMilliseconds = 0;
            _presentationTargetSimulationTick = 0;
            _presentationAuthorityElapsedMilliseconds = 0;
            _authorityContinuousInput = default;
            _authorityContinuousSimulationTick = 0;
            _inputEnabled = false;
            _baselineRequired = false;
            _authorityDead = false;
            _clockOverrun = false;
            _reconciliationCount = 0;
            _lastReconciliationPositionDeltaMillimeters = 0;
            _maximumReconciliationPositionDeltaMillimeters = 0;
            _lastReconciliationYawDeltaMillidegrees = 0;
            _lastJumpInputTick = 0;
            _lastJumpSimulationTick = 0;
            _lastJumpPredicted = false;
        }
    }

    /// <summary>
    /// 表示 snapshot acknowledgement 违反 current prediction generation。
    /// </summary>
    internal sealed class ClientBattlePredictionProtocolException : Exception
    {
        /// <summary>
        /// 创建不包含 payload 或玩家 identity 的稳定协议失败。
        /// </summary>
        internal ClientBattlePredictionProtocolException(string message)
            : base(message)
        {
        }
    }
}
