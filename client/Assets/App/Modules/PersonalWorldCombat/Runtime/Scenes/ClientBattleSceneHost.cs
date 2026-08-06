using System;
using IHomeland.Client.PersonalWorldCombat.Application;
using IHomeland.Client.AppShell.Runtime.Presentation.Hosts;
using IHomeland.Client.Core.Runtime.Scenes;
using IHomeland.Client.PersonalWorldCombat.Runtime.Input;
using UnityEngine;

using UnityEngine.Scripting.APIUpdating;

namespace IHomeland.Client.PersonalWorldCombat.Runtime.Scenes
{
    /// <summary>
    /// 在 PersonalWorldScene 主线程连接 semantic input 与 immutable battle presentation。
    /// </summary>
    /// <remarks>
    /// Host 不持有 Session、target、socket 或 authority snapshot；Scene teardown 只解除本地
    /// Input/Actor/HUD/Camera binding，不自行关闭或复活 App Scope battle owner。
    /// </remarks>
    [DisallowMultipleComponent]
    [DefaultExecutionOrder(100)]
    [MovedFrom(true, "IHomeland.Client.Scenes.PersonalWorld", "IHomeland.Client.Runtime", null)]
    public sealed class ClientBattleSceneHost : MonoBehaviour
    {
        /// <summary>保存 Scene Scope actor view 的唯一 registry。</summary>
        [SerializeField]
        private ClientActorViewRegistry _actors;

        /// <summary>保存 Scene-bound battle uGUI overlay。</summary>
        [SerializeField]
        private ClientBattleHudHost _hud;

        /// <summary>保存 Cinemachine 3.1.7 camera intent adapter。</summary>
        [SerializeField]
        private CinemachineCameraHost _camera;

        /// <summary>将 Aim 设备单位转换为 millidegree 的显式表现灵敏度。</summary>
        [SerializeField]
        [Min(1f)]
        private float _aimMillidegreesPerUnit = 100f;

        /// <summary>保存 current SceneLifetime。</summary>
        private SceneLifetime _lifetime;

        /// <summary>保存 App Scope battle runtime 窄 facade。</summary>
        private ClientBattleRuntimeCoordinator _runtime;

        /// <summary>保存唯一 App Scope Input owner 的窄采样端口。</summary>
        private IClientBattleInputSource _input;

        /// <summary>保存 current battle generation；连接或 replacement 期间为 0。</summary>
        private long _battleGeneration;

        /// <summary>保存上一帧已处理的battle availability。</summary>
        private ClientBattleAvailability _availability =
            ClientBattleAvailability.Inactive;

        /// <summary>保存累计 semantic aim yaw，单位 millidegree。</summary>
        private int _aimYawMillidegrees;

        /// <summary>保存累计 semantic aim pitch，单位 millidegree。</summary>
        private int _aimPitchMillidegrees;

#if DEVELOPMENT_BUILD || UNITY_EDITOR
        /// <summary>累计current Scene binding的render frame数。</summary>
        private int _diagnosticRenderFrames;

        /// <summary>累计current Scene binding的render frame时长，单位毫秒。</summary>
        private double _diagnosticRenderMilliseconds;

        /// <summary>保存current Scene binding的最长render frame，单位毫秒。</summary>
        private float _diagnosticMaximumRenderMilliseconds;

        /// <summary>累计超过33.33 ms的render frame数量。</summary>
        private int _diagnosticFramesOverThirtyThreeMilliseconds;

        /// <summary>累计超过50 ms的render frame数量。</summary>
        private int _diagnosticFramesOverFiftyMilliseconds;

        /// <summary>累计超过100 ms的render frame数量。</summary>
        private int _diagnosticFramesOverOneHundredMilliseconds;

        /// <summary>保存最近一帧Gameplay input owner是否可用。</summary>
        private bool _diagnosticGameplayInputAvailable;
#endif

        /// <summary>验证全部 Scene sub-host 引用与 aim 量化配置。</summary>
        internal void ValidateConfiguration()
        {
            if (_actors == null ||
                _hud == null ||
                _camera == null ||
                _aimMillidegreesPerUnit < 1f)
            {
                throw new InvalidOperationException(
                    "ClientBattleSceneHost 缺少 Actor/HUD/Camera 或 Aim 配置。");
            }

            if (_actors.gameObject.scene != gameObject.scene ||
                _hud.gameObject.scene != gameObject.scene ||
                _camera.gameObject.scene != gameObject.scene)
            {
                throw new InvalidOperationException(
                    "ClientBattleSceneHost sub-host 必须属于 current Scene。");
            }

            _actors.ValidateConfiguration();
            _hud.ValidateConfiguration();
            _camera.ValidateConfiguration();
        }

        /// <summary>
        /// 绑定 current SceneLifetime 与 App Scope 窄端口，不复制任何业务最终事实。
        /// </summary>
        /// <param name="lifetime">Current Scene generation owner lease。</param>
        /// <param name="runtime">唯一 client battle runtime facade。</param>
        /// <param name="input">唯一 App Scope Input System owner。</param>
        internal void Bind(
            SceneLifetime lifetime,
            ClientBattleRuntimeCoordinator runtime,
            IClientBattleInputSource input)
        {
            ValidateConfiguration();
            if (_lifetime != null)
            {
                throw new InvalidOperationException(
                    "ClientBattleSceneHost 不能重复绑定 SceneLifetime。");
            }

            _lifetime = lifetime ?? throw new ArgumentNullException(nameof(lifetime));
            _runtime = runtime ?? throw new ArgumentNullException(nameof(runtime));
            _input = input ?? throw new ArgumentNullException(nameof(input));
            if (!_lifetime.CanCommit)
            {
                Unbind();
                throw new InvalidOperationException(
                    "ClientBattleSceneHost 不能绑定已失效 generation。");
            }

            var snapshot = _runtime.Snapshot();
            _battleGeneration = snapshot.BattleGeneration;
            _availability = snapshot.Availability;
            _actors.Bind(_lifetime.Generation, _battleGeneration);
            _hud.Bind(_lifetime.Generation);
            _camera.Bind(_lifetime.Generation);
#if DEVELOPMENT_BUILD || UNITY_EDITOR
            ResetRenderDiagnostics();
#endif
        }

        /// <summary>
        /// 在 Unity 主线程提交一次 input sample，并消费至多一个一致 presentation state。
        /// </summary>
        private void Update()
        {
            if (_lifetime == null || !_lifetime.CanCommit ||
                _runtime == null || _input == null)
            {
                return;
            }

            var snapshot = _runtime.Snapshot();
            var generationChanged =
                snapshot.BattleGeneration != _battleGeneration;
            var reachedTerminalPresentation =
                snapshot.Availability != _availability &&
                (snapshot.Availability ==
                    ClientBattleAvailability.Inactive ||
                 snapshot.Availability ==
                    ClientBattleAvailability.Unavailable);
            if (generationChanged || reachedTerminalPresentation)
            {
                _battleGeneration = snapshot.BattleGeneration;
                _aimYawMillidegrees = 0;
                _aimPitchMillidegrees = 0;
                var preserveActors =
                    snapshot.Availability ==
                        ClientBattleAvailability.Retrying ||
                    snapshot.Availability ==
                        ClientBattleAvailability.LoadingBaseline;
                _actors.ReplaceBattleGeneration(
                    _battleGeneration,
                    preserveActors);
                if (!preserveActors)
                {
                    _hud.DiscardCommittedState();
                }
            }

            _availability = snapshot.Availability;
            _hud.ApplyRuntime(snapshot);
            if (snapshot.InputEnabled && _battleGeneration > 0)
            {
                var inputFrame = _input.CaptureBattleInput();
                var sample = inputFrame.Sample;
                if (inputFrame.GameplayAvailable)
                {
                    AccumulateAim(sample.Aim);
                }

                var worldMove = inputFrame.GameplayAvailable
                    ? sample.ProjectMoveToWorld(_aimYawMillidegrees)
                    : Vector2.zero;
                var semantic = new ClientBattleSemanticInput(
                    Mathf.RoundToInt(worldMove.x * 1000f),
                    Mathf.RoundToInt(worldMove.y * 1000f),
                    _aimYawMillidegrees,
                    _aimPitchMillidegrees,
                    inputFrame.GameplayAvailable && sample.JumpPressed,
                    inputFrame.GameplayAvailable && sample.PrimaryPressed,
                    inputFrame.GameplayAvailable && sample.SecondaryPressed,
                    inputFrame.GameplayAvailable && sample.InteractPressed,
                    inputFrame.GameplayAvailable && sample.InteractPressed
                        ? 1u
                        : 0u,
                    inputFrame.GameplayAvailable &&
                        sample.SwitchWeaponPressed);
                _runtime.TrySetInput(_battleGeneration, semantic);
#if DEVELOPMENT_BUILD || UNITY_EDITOR
                _diagnosticGameplayInputAvailable =
                    inputFrame.GameplayAvailable;
#endif
            }

            if (_runtime.TryConsumePresentation(out var state) &&
                state.Hud.BattleGeneration == _battleGeneration &&
                _actors.TryApply(state))
            {
                _hud.Apply(state.Hud);
                _camera.Apply(state.Camera, _actors);
            }

            _camera.Tick(_actors, _aimYawMillidegrees);
#if DEVELOPMENT_BUILD || UNITY_EDITOR
            CaptureRenderDiagnostics(Time.unscaledDeltaTime);
#endif
        }

        /// <summary>
        /// 解除 Scene owners 与 App Scope facade 的引用，不关闭 battle connection。
        /// </summary>
        internal void Unbind()
        {
#if DEVELOPMENT_BUILD || UNITY_EDITOR
            FlushPredictionDiagnostics();
#endif
            _camera?.Unbind();
            _hud?.Unbind();
            _actors?.Unbind();
            _battleGeneration = 0;
            _availability = ClientBattleAvailability.Inactive;
            _aimYawMillidegrees = 0;
            _aimPitchMillidegrees = 0;
            _input = null;
            _runtime = null;
            _lifetime = null;
#if DEVELOPMENT_BUILD || UNITY_EDITOR
            ResetRenderDiagnostics();
#endif
        }

        /// <summary>在 Unity 销毁阶段先退役全部 Scene Scope binding。</summary>
        private void OnDestroy()
        {
            Unbind();
        }

        /// <summary>累积并规范化纯 semantic aim，不使用 Camera Transform 作为 authority。</summary>
        /// <param name="aimDelta">唯一 Input owner 的本帧设备增量。</param>
        private void AccumulateAim(Vector2 aimDelta)
        {
            var yawDelta = Mathf.RoundToInt(
                aimDelta.x * _aimMillidegreesPerUnit);
            var pitchDelta = Mathf.RoundToInt(
                aimDelta.y * _aimMillidegreesPerUnit);
            var yaw = ((long)_aimYawMillidegrees + yawDelta) % 360000L;
            if (yaw < -180000L)
            {
                yaw += 360000L;
            }
            else if (yaw >= 180000L)
            {
                yaw -= 360000L;
            }

            _aimYawMillidegrees = (int)yaw;
            _aimPitchMillidegrees = Mathf.Clamp(
                _aimPitchMillidegrees + pitchDelta,
                -90000,
                90000);
        }

#if DEVELOPMENT_BUILD || UNITY_EDITOR
        /// <summary>
        /// Scene解绑后一次输出不含identity、endpoint或payload的runtime与预测稳定性摘要。
        /// </summary>
        /// <remarks>
        /// 该日志只用于Editor/Development手感排障；Release不会编译此路径。Prediction lead
        /// 表示current local horizon相对authority的位置，不等同于reconciliation误差。
        /// </remarks>
        private void FlushPredictionDiagnostics()
        {
            if (_runtime == null || _diagnosticRenderFrames == 0)
            {
                return;
            }

            var runtimeSnapshot = _runtime.Snapshot();
            if (!runtimeSnapshot.BaselineReady)
            {
                Debug.LogFormat(
                    LogType.Log,
                    LogOption.NoStacktrace,
                    null,
                    "{0}",
                    "[IHOMELAND_BATTLE_RUNTIME] " +
                    $"availability={runtimeSnapshot.Availability} " +
                    $"failure={runtimeSnapshot.Failure} " +
                    $"battle_generation={runtimeSnapshot.BattleGeneration} " +
                    "baseline_ready=False " +
                    $"input_enabled={runtimeSnapshot.InputEnabled} " +
                    $"render_frames={_diagnosticRenderFrames}");
                return;
            }

            if (!_runtime.TryCaptureQualificationSnapshot(
                    out var diagnostics))
            {
                return;
            }

            var sentFrontier = Math.Max(
                diagnostics.LastSentInputTick,
                diagnostics.ContinuityAcknowledgementAnchor);
            var unacknowledged = sentFrontier >=
                    diagnostics.LastAcknowledgedInputTick
                ? sentFrontier -
                    diagnostics.LastAcknowledgedInputTick
                : 0;
            var predictionLeadX =
                (long)diagnostics.PredictedTransform.PositionXMillimeters -
                diagnostics.AuthorityTransform.PositionXMillimeters;
            var predictionLeadY =
                (long)diagnostics.PredictedTransform.PositionYMillimeters -
                diagnostics.AuthorityTransform.PositionYMillimeters;
            var predictionLeadZ =
                (long)diagnostics.PredictedTransform.PositionZMillimeters -
                diagnostics.AuthorityTransform.PositionZMillimeters;
            var lastSentSimulationTick =
                diagnostics.LastSentInputTick == 0
                    ? 0
                    : ClientBattlePolicy.Current.MapInputToSimulationTick(
                        diagnostics.LastSentInputTick);
            var inputScheduleLeadTicks = SignedTickDelta(
                lastSentSimulationTick,
                diagnostics.ServerTick);
            var averageRenderMilliseconds = _diagnosticRenderFrames == 0
                ? 0.0
                : _diagnosticRenderMilliseconds / _diagnosticRenderFrames;
            var message =
                "[IHOMELAND_BATTLE_PREDICTION] " +
                $"server_tick={diagnostics.ServerTick} " +
                $"last_sent_simulation_tick={lastSentSimulationTick} " +
                $"input_schedule_lead_ticks={inputScheduleLeadTicks} " +
                "ack_acceptance_frontier=" +
                $"{diagnostics.AcknowledgementAcceptanceFrontier} " +
                $"unacknowledged_inputs={unacknowledged} " +
                $"reconciliations={diagnostics.ReconciliationCount} " +
                "target_rebase_mm=" +
                $"{diagnostics.LastReconciliationPositionDeltaMillimeters} " +
                "max_target_rebase_mm=" +
                $"{diagnostics.MaximumReconciliationPositionDeltaMillimeters} " +
                "target_rebase_yaw_mdeg=" +
                $"{diagnostics.LastReconciliationYawDeltaMillidegrees} " +
                $"prediction_lead_x_mm={predictionLeadX} " +
                $"prediction_lead_y_mm={predictionLeadY} " +
                $"prediction_lead_z_mm={predictionLeadZ} " +
                $"last_jump_input_tick={diagnostics.LastJumpInputTick} " +
                "last_jump_simulation_tick=" +
                $"{diagnostics.LastJumpSimulationTick} " +
                $"jump_predicted={diagnostics.LastJumpPredicted} " +
                $"authority_grounded={diagnostics.AuthorityGrounded} " +
                $"predicted_grounded={diagnostics.PredictedGrounded} " +
                $"render_frames={_diagnosticRenderFrames} " +
                $"render_frame_avg_ms={averageRenderMilliseconds:F2} " +
                $"render_frame_max_ms={_diagnosticMaximumRenderMilliseconds:F2} " +
                "render_frames_over_33_ms=" +
                $"{_diagnosticFramesOverThirtyThreeMilliseconds} " +
                "render_frames_over_50_ms=" +
                $"{_diagnosticFramesOverFiftyMilliseconds} " +
                "render_frames_over_100_ms=" +
                $"{_diagnosticFramesOverOneHundredMilliseconds} " +
                $"gameplay_input_available={_diagnosticGameplayInputAvailable}";
            Debug.LogFormat(
                LogType.Log,
                LogOption.NoStacktrace,
                null,
                "{0}",
                message);
        }

        /// <summary>把一个real render frame计入current Scene binding的定长聚合。</summary>
        /// <param name="unscaledDeltaTimeSeconds">当前帧真实时长，单位秒。</param>
        private void CaptureRenderDiagnostics(float unscaledDeltaTimeSeconds)
        {
            if (unscaledDeltaTimeSeconds < 0f ||
                float.IsNaN(unscaledDeltaTimeSeconds) ||
                float.IsInfinity(unscaledDeltaTimeSeconds))
            {
                return;
            }

            var milliseconds = unscaledDeltaTimeSeconds * 1000f;
            _diagnosticRenderFrames++;
            _diagnosticRenderMilliseconds += milliseconds;
            _diagnosticMaximumRenderMilliseconds = Mathf.Max(
                _diagnosticMaximumRenderMilliseconds,
                milliseconds);
            if (milliseconds > 33.33f)
            {
                _diagnosticFramesOverThirtyThreeMilliseconds++;
            }

            if (milliseconds > 50f)
            {
                _diagnosticFramesOverFiftyMilliseconds++;
            }

            if (milliseconds > 100f)
            {
                _diagnosticFramesOverOneHundredMilliseconds++;
            }
        }

        /// <summary>重置低频render frame诊断窗口，不改变任何runtime行为。</summary>
        private void ResetRenderDiagnostics()
        {
            _diagnosticRenderFrames = 0;
            _diagnosticRenderMilliseconds = 0.0;
            _diagnosticMaximumRenderMilliseconds = 0f;
            _diagnosticFramesOverThirtyThreeMilliseconds = 0;
            _diagnosticFramesOverFiftyMilliseconds = 0;
            _diagnosticFramesOverOneHundredMilliseconds = 0;
            _diagnosticGameplayInputAvailable = false;
        }

        /// <summary>计算两个无符号Tick的饱和有符号差，避免诊断路径溢出。</summary>
        /// <param name="value">被比较的Tick。</param>
        /// <param name="reference">作为零点的Tick。</param>
        /// <returns>饱和到Int64范围的`value - reference`。</returns>
        private static long SignedTickDelta(ulong value, ulong reference)
        {
            if (value >= reference)
            {
                return (long)Math.Min(
                    value - reference,
                    (ulong)long.MaxValue);
            }

            return -(long)Math.Min(
                reference - value,
                (ulong)long.MaxValue);
        }
#endif
    }
}
