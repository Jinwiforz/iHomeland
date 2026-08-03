using System;
using System.Collections.Generic;
using IHomeland.Client.Application.Battle;
using UnityEngine;

namespace IHomeland.Client.Scenes.PersonalWorld
{
    /// <summary>
    /// 在 current Scene generation 内拥有 entity view 的创建、替换、更新与销毁。
    /// </summary>
    /// <remarks>
    /// 本组件只消费 immutable presentation state；它不读取网络、Scene tag 或 payload
    /// 来猜测 local actor，也不把 Transform 回写为权威事实。
    /// </remarks>
    [DisallowMultipleComponent]
    public sealed class ClientActorViewRegistry : MonoBehaviour
    {
        /// <summary>保存 local actor 临界阻尼表现收敛的时间常数，单位秒。</summary>
        private const float LocalSmoothingSeconds = 0.05f;

        /// <summary>
        /// 保存local render target允许的最大线性外推年龄，单位秒。
        /// </summary>
        /// <remarks>
        /// 75 ms等于一个50 ms simulation step加半个step到达抖动余量；
        /// 超限即停止外推，避免snapshot中断时画面无界漂移。
        /// </remarks>
        private const float LocalMaximumExtrapolationSeconds = 0.075f;

        /// <summary>保存local水平render motor连续吸收大误差的最大速度，单位米每秒。</summary>
        private const float LocalHorizontalCorrectionMetersPerSecond = 0.75f;

        /// <summary>保存所有 B0.7 actor 共用的产品 Prefab。</summary>
        [SerializeField]
        [Tooltip("不包含业务 owner 的 generic actor Prefab。")]
        private GameObject _actorPrefab;

        /// <summary>保存 Scene Scope actor instances 的唯一父节点。</summary>
        [SerializeField]
        [Tooltip("全部运行时 actor view 必须创建在此 Scene root 下。")]
        private Transform _actorRoot;

        /// <summary>按 entity identity 保存 current lifecycle generation 的 view。</summary>
        private readonly Dictionary<ulong, ActorEntry> _actors =
            new Dictionary<ulong, ActorEntry>();

        /// <summary>复用 current apply 的实体集合，避免每帧创建无界临时容器。</summary>
        private readonly HashSet<ulong> _currentEntities =
            new HashSet<ulong>();

        /// <summary>复用本帧应销毁的 entity identity 集合。</summary>
        private readonly List<ulong> _retiredEntities =
            new List<ulong>();

        /// <summary>保存当前绑定的 Scene generation。</summary>
        private long _sceneGeneration;

        /// <summary>保存当前绑定的 battle generation。</summary>
        private long _battleGeneration;

#if DEVELOPMENT_BUILD || UNITY_EDITOR
        /// <summary>获取真实Player资格可观察的current Scene actor实例数量。</summary>
        internal int QualificationActorCount => _actors.Count;

        /// <summary>获取真实Player资格可观察的current local actor实例数量。</summary>
        internal int QualificationLocalActorCount
        {
            get
            {
                var count = 0;
                foreach (var entry in _actors.Values)
                {
                    if (entry.Local && entry.Instance != null)
                    {
                        count++;
                    }
                }

                return count;
            }
        }
#endif

        /// <summary>验证 Prefab 与 actor root 是直接且同 Scene 的引用。</summary>
        /// <exception cref="InvalidOperationException">引用缺失、root 跨 Scene 或 Prefab 已实例化在 Scene 时抛出。</exception>
        internal void ValidateConfiguration()
        {
            if (_actorPrefab == null || _actorRoot == null)
            {
                throw new InvalidOperationException(
                    "ClientActorViewRegistry 缺少 Actor Prefab 或 ActorRoot。");
            }

            if (_actorRoot.gameObject.scene != gameObject.scene)
            {
                throw new InvalidOperationException(
                    "ClientActorViewRegistry ActorRoot 必须属于 current Scene。");
            }

            if (_actorPrefab.scene.IsValid())
            {
                throw new InvalidOperationException(
                    "ClientActorViewRegistry 必须引用 Prefab asset，不能把 Scene object 当作模板。");
            }
        }

        /// <summary>为 current Scene 与 battle generation 建立空 registry。</summary>
        /// <param name="sceneGeneration">SceneLifetime 分配的 current generation。</param>
        /// <param name="battleGeneration">已认证 battle generation；连接中允许为 0。</param>
        internal void Bind(long sceneGeneration, long battleGeneration)
        {
            ValidateConfiguration();
            if (_sceneGeneration != 0 || sceneGeneration <= 0 || battleGeneration < 0)
            {
                throw new InvalidOperationException(
                    "ClientActorViewRegistry binding generation 非法。");
            }

            _sceneGeneration = sceneGeneration;
            _battleGeneration = battleGeneration;
        }

        /// <summary>
        /// 原子替换 successor battle generation，并按恢复语义保留或清除旧 view。
        /// </summary>
        /// <param name="battleGeneration">新 battle generation；断开时为 0。</param>
        /// <param name="preserveActors">
        /// 短时恢复为 true，使最后可信画面持续可见；目标切换或终态失败为 false。
        /// </param>
        internal void ReplaceBattleGeneration(
            long battleGeneration,
            bool preserveActors)
        {
            if (_sceneGeneration == 0 || battleGeneration < 0)
            {
                return;
            }

            if (!preserveActors)
            {
                ClearActors();
            }

            _battleGeneration = battleGeneration;
        }

        /// <summary>
        /// 应用按 entity identity 排序的 immutable actor projection。
        /// </summary>
        /// <param name="state">Current generation 的一致 presentation state。</param>
        /// <returns>Generation current 且全部 actor 已更新时返回 true。</returns>
        internal bool TryApply(ClientGameplayPresentationState state)
        {
            if (state == null ||
                _sceneGeneration == 0 ||
                _battleGeneration <= 0 ||
                state.Hud.BattleGeneration != _battleGeneration)
            {
                return false;
            }

            _currentEntities.Clear();
            for (var index = 0; index < state.Actors.Count; index++)
            {
                var actor = state.Actors[index];
                if (actor.BattleGeneration != _battleGeneration ||
                    !_currentEntities.Add(actor.EntityID))
                {
                    return false;
                }

                if (!_actors.TryGetValue(actor.EntityID, out var entry) ||
                    entry.EntityGeneration != actor.EntityGeneration)
                {
                    if (entry != null && entry.Instance != null)
                    {
                        Destroy(entry.Instance);
                    }

                    var instance = Instantiate(_actorPrefab, _actorRoot, false);
                    instance.name = $"ActorView-{actor.EntityID}-{actor.EntityGeneration}";
                    entry = new ActorEntry(
                        actor.EntityGeneration,
                        instance,
                        actor.Local,
                        actor.Transform);
                    _actors[actor.EntityID] = entry;
                    entry.SnapToTarget();
                }
                else
                {
                    entry.SetTarget(
                        actor.Local,
                        actor.Transform);
                    if (!actor.Local)
                    {
                        entry.SnapToTarget();
                    }
                }
            }

            _retiredEntities.Clear();
            foreach (var pair in _actors)
            {
                if (!_currentEntities.Contains(pair.Key))
                {
                    _retiredEntities.Add(pair.Key);
                }
            }

            for (var index = 0; index < _retiredEntities.Count; index++)
            {
                var entityID = _retiredEntities[index];
                var retired = _actors[entityID];
                _actors.Remove(entityID);
                if (retired.Instance != null)
                {
                    Destroy(retired.Instance);
                }
            }

            return true;
        }

        /// <summary>
        /// 在 render timeline 推进 local actor 的 frame-rate-independent 表现收敛。
        /// </summary>
        /// <param name="unscaledDeltaTimeSeconds">当前真实渲染帧时长，单位秒。</param>
        internal void Tick(float unscaledDeltaTimeSeconds)
        {
            TickInternal(
                unscaledDeltaTimeSeconds,
                Vector2.zero,
                useSemanticHorizontalMotion: false);
        }

        /// <summary>
        /// 在render timeline以current semantic world move驱动local水平表现。
        /// </summary>
        /// <param name="unscaledDeltaTimeSeconds">当前真实渲染帧时长，单位秒。</param>
        /// <param name="semanticWorldMove">按current aim投影的world X/Z move。</param>
        /// <param name="gameplayAvailable">Gameplay输入owner当前是否可提交continuous move。</param>
        internal void Tick(
            float unscaledDeltaTimeSeconds,
            Vector2 semanticWorldMove,
            bool gameplayAvailable)
        {
            TickInternal(
                unscaledDeltaTimeSeconds,
                gameplayAvailable ? semanticWorldMove : Vector2.zero,
                useSemanticHorizontalMotion: true);
        }

        /// <summary>推进local Actor且保持测试/恢复路径的sample-only fallback。</summary>
        /// <param name="unscaledDeltaTimeSeconds">当前真实渲染帧时长，单位秒。</param>
        /// <param name="semanticWorldMove">Current world X/Z continuous move。</param>
        /// <param name="useSemanticHorizontalMotion">是否由Gameplay owner驱动水平render motor。</param>
        private void TickInternal(
            float unscaledDeltaTimeSeconds,
            Vector2 semanticWorldMove,
            bool useSemanticHorizontalMotion)
        {
            if (_sceneGeneration == 0 ||
                unscaledDeltaTimeSeconds <= 0f ||
                float.IsNaN(unscaledDeltaTimeSeconds) ||
                float.IsInfinity(unscaledDeltaTimeSeconds))
            {
                return;
            }

            foreach (var entry in _actors.Values)
            {
                if (entry.Local)
                {
                    entry.SmoothToTarget(
                        unscaledDeltaTimeSeconds,
                        semanticWorldMove,
                        useSemanticHorizontalMotion);
                }
            }
        }

        /// <summary>尝试取得 camera follow 可用的 current actor Transform。</summary>
        /// <param name="entityID">Presentation 明确给出的 follow entity identity。</param>
        /// <param name="actorTransform">成功时返回 current generation 的 actor Transform。</param>
        /// <returns>Actor view 存在时返回 true。</returns>
        internal bool TryGetActorTransform(
            ulong entityID,
            out Transform actorTransform)
        {
            if (_actors.TryGetValue(entityID, out var entry) &&
                entry.Instance != null)
            {
                actorTransform = entry.Instance.transform;
                return true;
            }

            actorTransform = null;
            return false;
        }

        /// <summary>销毁 Scene Scope 全部 actor view，不处置 App Scope battle owner。</summary>
        internal void Unbind()
        {
            ClearActors();
            _battleGeneration = 0;
            _sceneGeneration = 0;
        }

        /// <summary>在 Unity 销毁阶段拒绝 successor frame 回写。</summary>
        private void OnDestroy()
        {
            Unbind();
        }

        /// <summary>把毫米 position 投影转换为 Unity 米坐标。</summary>
        /// <param name="source">Application 提供的量化只读 transform。</param>
        /// <returns>只用于表现的 Unity position。</returns>
        private static Vector3 ToPosition(ClientBattleTransform source)
        {
            return new Vector3(
                source.PositionXMillimeters * 0.001f,
                source.PositionYMillimeters * 0.001f,
                source.PositionZMillimeters * 0.001f);
        }

        /// <summary>把 millidegree yaw 投影转换为 Unity 表现 rotation。</summary>
        /// <param name="source">Application 提供的量化只读 transform。</param>
        /// <returns>只用于表现的 Unity rotation。</returns>
        private static Quaternion ToRotation(ClientBattleTransform source)
        {
            return Quaternion.Euler(
                0f,
                source.YawMillidegrees * 0.001f,
                0f);
        }

        /// <summary>把mm/s world velocity投影为Unity米/秒向量。</summary>
        /// <param name="source">Application提供的量化只读transform。</param>
        /// <returns>只用于local render timeline的world velocity。</returns>
        private static Vector3 ToVelocity(ClientBattleTransform source)
        {
            return new Vector3(
                source.VelocityXMillimetersPerSecond * 0.001f,
                source.VelocityYMillimetersPerSecond * 0.001f,
                source.VelocityZMillimetersPerSecond * 0.001f);
        }

        /// <summary>释放 registry 持有的全部 Unity instances 和临时集合。</summary>
        private void ClearActors()
        {
            foreach (var entry in _actors.Values)
            {
                if (entry.Instance != null)
                {
                    Destroy(entry.Instance);
                }
            }

            _actors.Clear();
            _currentEntities.Clear();
            _retiredEntities.Clear();
        }

        /// <summary>保存一个 entity lifecycle generation 与对应 Unity instance。</summary>
        private sealed class ActorEntry
        {
            /// <summary>创建 current actor view entry。</summary>
            internal ActorEntry(
                uint entityGeneration,
                GameObject instance,
                bool local,
                ClientBattleTransform sample)
            {
                EntityGeneration = entityGeneration;
                Instance = instance;
                Local = local;
                _sample = sample;
            }

            /// <summary>获取拒绝旧 identity 复活的 lifecycle generation。</summary>
            internal uint EntityGeneration { get; }

            /// <summary>获取仅由 registry 拥有的 Unity instance。</summary>
            internal GameObject Instance { get; }

            /// <summary>获取当前是否使用 local prediction 表现路径。</summary>
            internal bool Local { get; private set; }

            /// <summary>保存最新local prediction或remote interpolation的量化sample。</summary>
            private ClientBattleTransform _sample;

            /// <summary>
            /// 保存最新实际变化sample在local render timeline上的年龄，单位秒。
            /// </summary>
            private float _sampleAgeSeconds;

            /// <summary>保存跨 render frame 连续的位置平滑速度。</summary>
            private Vector3 _positionSmoothingVelocity;

            /// <summary>保存跨 render frame 连续的 yaw 平滑角速度，单位度每秒。</summary>
            private float _yawSmoothingVelocityDegreesPerSecond;

            /// <summary>保存local垂直表现的临界阻尼速度，单位米每秒。</summary>
            private float _verticalSmoothingVelocity;

            /// <summary>更新不可变 presentation frame 派生的目标。</summary>
            internal void SetTarget(
                bool local,
                ClientBattleTransform sample)
            {
                if (Local != local)
                {
                    _positionSmoothingVelocity = Vector3.zero;
                    _yawSmoothingVelocityDegreesPerSecond = 0f;
                    _verticalSmoothingVelocity = 0f;
                    _sampleAgeSeconds = 0f;
                }

                Local = local;
                var motionChanged = !MotionSamplesMatch(_sample, sample);
                _sample = sample;
                if (motionChanged)
                {
                    _sampleAgeSeconds = 0f;
                }
            }

            /// <summary>首次生成或 remote interpolation 时立即采用已收敛目标。</summary>
            internal void SnapToTarget()
            {
                if (Instance != null)
                {
                    Instance.transform.SetPositionAndRotation(
                        ToPosition(_sample),
                        ToRotation(_sample));
                    _positionSmoothingVelocity = Vector3.zero;
                    _yawSmoothingVelocityDegreesPerSecond = 0f;
                    _verticalSmoothingVelocity = 0f;
                    _sampleAgeSeconds = 0f;
                }
            }

            /// <summary>以跨帧连续的render motor收敛local presentation Transform。</summary>
            /// <param name="unscaledDeltaTimeSeconds">当前真实渲染帧时长，单位秒。</param>
            /// <param name="semanticWorldMove">Current world X/Z continuous move。</param>
            /// <param name="useSemanticHorizontalMotion">是否由Gameplay owner驱动水平表现。</param>
            internal void SmoothToTarget(
                float unscaledDeltaTimeSeconds,
                Vector2 semanticWorldMove,
                bool useSemanticHorizontalMotion)
            {
                if (Instance == null)
                {
                    return;
                }

                _sampleAgeSeconds = Mathf.Min(
                    _sampleAgeSeconds + unscaledDeltaTimeSeconds,
                    LocalMaximumExtrapolationSeconds);
                var samplePosition = ToPosition(_sample);
                var renderTargetPosition =
                    samplePosition + ToVelocity(_sample) * _sampleAgeSeconds;
                var semanticCorrectionTarget = new Vector3(
                    samplePosition.x,
                    renderTargetPosition.y,
                    samplePosition.z);
                var targetRotation = ToRotation(_sample);
                var actorTransform = Instance.transform;
                var position = useSemanticHorizontalMotion
                    ? AdvanceSemanticHorizontalMotion(
                        actorTransform.position,
                        semanticCorrectionTarget,
                        semanticWorldMove,
                        unscaledDeltaTimeSeconds)
                    : Vector3.SmoothDamp(
                        actorTransform.position,
                        renderTargetPosition,
                        ref _positionSmoothingVelocity,
                        LocalSmoothingSeconds,
                        Mathf.Infinity,
                        unscaledDeltaTimeSeconds);
                actorTransform.SetPositionAndRotation(
                    position,
                    Quaternion.Euler(
                        0f,
                        Mathf.SmoothDampAngle(
                            actorTransform.eulerAngles.y,
                            targetRotation.eulerAngles.y,
                            ref _yawSmoothingVelocityDegreesPerSecond,
                            LocalSmoothingSeconds,
                            Mathf.Infinity,
                            unscaledDeltaTimeSeconds),
                        0f));
            }

            /// <summary>
            /// 以current semantic move连续推进水平位置，并有界吸收prediction误差。
            /// </summary>
            /// <param name="current">Current render position。</param>
            /// <param name="predictionTarget">Current prediction派生的三轴目标。</param>
            /// <param name="semanticWorldMove">按current aim投影的world X/Z move。</param>
            /// <param name="unscaledDeltaTimeSeconds">当前真实渲染帧时长，单位秒。</param>
            /// <returns>不把旧velocity外推越过松键停止点的下一render position。</returns>
            private Vector3 AdvanceSemanticHorizontalMotion(
                Vector3 current,
                Vector3 predictionTarget,
                Vector2 semanticWorldMove,
                float unscaledDeltaTimeSeconds)
            {
                var clampedMove = Vector2.ClampMagnitude(
                    semanticWorldMove,
                    1f);
                var speedMetersPerSecond =
                    ClientBattlePolicy.Current.
                        MaximumHorizontalSpeedMillimetersPerSecond *
                    0.001f;
                var next = new Vector3(
                    current.x +
                        clampedMove.x * speedMetersPerSecond *
                        unscaledDeltaTimeSeconds,
                    Mathf.SmoothDamp(
                        current.y,
                        predictionTarget.y,
                        ref _verticalSmoothingVelocity,
                        LocalSmoothingSeconds,
                        Mathf.Infinity,
                        unscaledDeltaTimeSeconds),
                    current.z +
                        clampedMove.y * speedMetersPerSecond *
                        unscaledDeltaTimeSeconds);
                var horizontalError = new Vector2(
                    predictionTarget.x - next.x,
                    predictionTarget.z - next.z);
                var errorMagnitude = horizontalError.magnitude;
                var normalStepDistanceMeters =
                    ClientBattlePolicy.Current.
                        MaximumHorizontalSpeedMillimetersPerSecond *
                    ClientBattlePolicy.Current.SimulationStepMilliseconds *
                    0.000001f;
                if (errorMagnitude <= normalStepDistanceMeters)
                {
                    return next;
                }

                var correctionDistance = Mathf.Min(
                    errorMagnitude - normalStepDistanceMeters,
                    LocalHorizontalCorrectionMetersPerSecond *
                        unscaledDeltaTimeSeconds);
                var correction =
                    horizontalError / errorMagnitude * correctionDistance;
                next.x += correction.x;
                next.z += correction.y;
                return next;
            }

            /// <summary>
            /// 按量化值判断是否为同一motion sample，避免浮点近似比较重置年龄。
            /// </summary>
            /// <remarks>
            /// Yaw可以比position更频繁地更新，但不能因此中断水平位置render timeline。
            /// </remarks>
            /// <param name="first">已经被entry消费的sample。</param>
            /// <param name="second">当前presentation提供的sample。</param>
            /// <returns>全部position与velocity scalar相同时返回true。</returns>
            private static bool MotionSamplesMatch(
                ClientBattleTransform first,
                ClientBattleTransform second)
            {
                return first.PositionXMillimeters == second.PositionXMillimeters &&
                       first.PositionYMillimeters == second.PositionYMillimeters &&
                       first.PositionZMillimeters == second.PositionZMillimeters &&
                       first.VelocityXMillimetersPerSecond ==
                           second.VelocityXMillimetersPerSecond &&
                       first.VelocityYMillimetersPerSecond ==
                           second.VelocityYMillimetersPerSecond &&
                       first.VelocityZMillimetersPerSecond ==
                           second.VelocityZMillimetersPerSecond;
            }
        }
    }
}
