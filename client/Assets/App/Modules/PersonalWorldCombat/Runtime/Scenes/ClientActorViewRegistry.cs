using System;
using System.Collections.Generic;
using IHomeland.Client.PersonalWorldCombat.Application;
using UnityEngine;

using UnityEngine.Scripting.APIUpdating;

namespace IHomeland.Client.PersonalWorldCombat.Runtime.Scenes
{
    /// <summary>
    /// 在 current Scene generation 内拥有 entity view 的创建、替换、更新与销毁。
    /// </summary>
    /// <remarks>
    /// 本组件只消费 immutable presentation state；它不读取网络、Scene tag 或 payload
    /// 来猜测 local actor，也不把 Transform 回写为权威事实。
    /// </remarks>
    [DisallowMultipleComponent]
    [MovedFrom(true, "IHomeland.Client.Scenes.PersonalWorld", "IHomeland.Client.Runtime", null)]
    public sealed class ClientActorViewRegistry : MonoBehaviour
    {
        /// <summary>保存 production semantic/numeric mapping 对应的唯一资源目录。</summary>
        [SerializeField]
        [Tooltip("直接引用唯一 tracked ClientCombatResourceCatalog asset。")]
        private ClientCombatResourceCatalog _resourceCatalog;

        /// <summary>保存只供旧程序化测试 fixture 使用的 generic Prefab。</summary>
        [SerializeField]
        [Tooltip("Production Scene 必须使用 ResourceCatalog；此引用只保留迁移兼容。")]
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

        /// <summary>保存 current battle generation 已消费的可靠 cue identity。</summary>
        private readonly HashSet<ulong> _consumedCueEventIDs =
            new HashSet<ulong>();

        /// <summary>保存必须随 Scene generation 一并退役的瞬时 VFX instances。</summary>
        private readonly List<GameObject> _transientEffects =
            new List<GameObject>();

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

        /// <summary>验证 catalog 与 actor root 是直接且同 Scene 的引用。</summary>
        /// <exception cref="InvalidOperationException">引用、catalog 或 root 漂移时抛出。</exception>
        internal void ValidateConfiguration()
        {
            if (_resourceCatalog == null || _actorRoot == null)
            {
                throw new InvalidOperationException(
                    "ClientActorViewRegistry 缺少 ResourceCatalog 或 ActorRoot。");
            }

            _resourceCatalog.ValidateCompleteness();

            if (_actorRoot.gameObject.scene != gameObject.scene)
            {
                throw new InvalidOperationException(
                    "ClientActorViewRegistry ActorRoot 必须属于 current Scene。");
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

            ClearCuesAndEffects();

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
                    !_currentEntities.Add(actor.EntityID) ||
                    !TryResolveActorPrefab(actor.ArchetypeID, out _))
                {
                    return false;
                }

                if (_actors.TryGetValue(actor.EntityID, out var current) &&
                    current.EntityGeneration == actor.EntityGeneration &&
                    current.ArchetypeID != actor.ArchetypeID)
                {
                    return false;
                }
            }

            for (var index = 0; index < state.Actors.Count; index++)
            {
                var actor = state.Actors[index];

                if (!_actors.TryGetValue(actor.EntityID, out var entry) ||
                    entry.EntityGeneration != actor.EntityGeneration)
                {
                    if (entry != null && entry.Instance != null)
                    {
                        Destroy(entry.Instance);
                    }

                    if (!TryResolveActorPrefab(actor.ArchetypeID, out var prefab))
                    {
                        return false;
                    }

                    var instance = Instantiate(prefab, _actorRoot, false);
                    instance.name = $"ActorView-{actor.EntityID}-{actor.EntityGeneration}";
                    entry = new ActorEntry(
                        actor.EntityGeneration,
                        actor.ArchetypeID,
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
                    entry.SnapToTarget();
                }

                if (!entry.TryApplyPresentation(actor, _resourceCatalog))
                {
                    return false;
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

            ApplyCues(state.Cues);
            return true;
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
            ClearCuesAndEffects();
        }

        /// <summary>解析 production Prefab；仅允许测试 fixture 使用显式 legacy template。</summary>
        /// <param name="archetypeID">Frozen production archetype ID。</param>
        /// <param name="prefab">成功时返回实例化模板。</param>
        /// <returns>Catalog mapping 或显式测试模板存在时返回 true。</returns>
        private bool TryResolveActorPrefab(uint archetypeID, out GameObject prefab)
        {
            if (_resourceCatalog != null)
            {
                return _resourceCatalog.TryGetActorPrefab(archetypeID, out prefab);
            }

            prefab = _actorPrefab;
            return prefab != null;
        }

        /// <summary>消费 current generation cues，并对缺失或旧 source 做无副作用丢弃。</summary>
        /// <param name="cues">Projector 已排序且有界的 cues。</param>
        private void ApplyCues(IReadOnlyList<ClientGameplayCue> cues)
        {
            if (_resourceCatalog == null || cues == null)
            {
                return;
            }

            for (var index = 0; index < cues.Count; index++)
            {
                var cue = cues[index];
                if (cue == null ||
                    cue.BattleGeneration != _battleGeneration ||
                    !_consumedCueEventIDs.Add(cue.EventID) ||
                    !_actors.TryGetValue(cue.SourceEntityID, out var source) ||
                    source.EntityGeneration != cue.SourceEntityGeneration)
                {
                    continue;
                }

                source.TryApplyCue(cue, _resourceCatalog);
                if (cue.Phase == ClientBattleAbilityPhase.Committed)
                {
                    CreateDamageEffects(cue.TargetEntityIDs);
                }
            }
        }

        /// <summary>在 authority event 的规范 target 集上创建不带碰撞的纯表现 VFX。</summary>
        /// <param name="targetEntityIDs">已排序且去重的 authority target identities。</param>
        private void CreateDamageEffects(IReadOnlyList<ulong> targetEntityIDs)
        {
            if (_resourceCatalog == null ||
                _resourceCatalog.DamageVfx == null ||
                targetEntityIDs == null)
            {
                return;
            }

            for (var index = 0; index < targetEntityIDs.Count; index++)
            {
                if (!_actors.TryGetValue(targetEntityIDs[index], out var target) ||
                    target.Instance == null)
                {
                    continue;
                }

                var effect = Instantiate(
                    _resourceCatalog.DamageVfx,
                    target.Instance.transform.position,
                    Quaternion.identity,
                    _actorRoot);
                effect.name = $"DamageVfx-{targetEntityIDs[index]}";
                var particles = effect.GetComponentsInChildren<ParticleSystem>(true);
                for (var particleIndex = 0; particleIndex < particles.Length; particleIndex++)
                {
                    particles[particleIndex].Play(withChildren: true);
                }

                _transientEffects.Add(effect);
                Destroy(effect, 2f);
            }
        }

        /// <summary>使 predecessor generation 的 cue identity、Audio 与 VFX 全部失效。</summary>
        private void ClearCuesAndEffects()
        {
            _consumedCueEventIDs.Clear();
            for (var index = 0; index < _transientEffects.Count; index++)
            {
                if (_transientEffects[index] != null)
                {
                    _transientEffects[index].SetActive(false);
                    Destroy(_transientEffects[index]);
                }
            }

            _transientEffects.Clear();
        }

        /// <summary>保存一个 entity lifecycle generation 与对应 Unity instance。</summary>
        private sealed class ActorEntry
        {
            /// <summary>创建 current actor view entry。</summary>
            internal ActorEntry(
                uint entityGeneration,
                uint archetypeID,
                GameObject instance,
                bool local,
                ClientBattleTransform sample)
            {
                EntityGeneration = entityGeneration;
                ArchetypeID = archetypeID;
                Instance = instance;
                _view = instance == null
                    ? null
                    : instance.GetComponent<ClientCombatActorView>();
                Local = local;
                _sample = sample;
            }

            /// <summary>获取拒绝旧 identity 复活的 lifecycle generation。</summary>
            internal uint EntityGeneration { get; }

            /// <summary>获取拒绝同 generation archetype 漂移的 production mapping。</summary>
            internal uint ArchetypeID { get; }

            /// <summary>获取仅由 registry 拥有的 Unity instance。</summary>
            internal GameObject Instance { get; }

            /// <summary>获取当前是否使用 local prediction 表现路径。</summary>
            internal bool Local { get; private set; }

            /// <summary>保存可选强类型 actor view；projectile 与 legacy fixture 可为空。</summary>
            private readonly ClientCombatActorView _view;

            /// <summary>保存最新local prediction或remote interpolation的量化sample。</summary>
            private ClientBattleTransform _sample;

            /// <summary>应用 health、weapon、dead、Animator 与 world HUD 表现。</summary>
            /// <param name="state">Current immutable actor state。</param>
            /// <param name="catalog">Production catalog；legacy fixture 可为空。</param>
            /// <returns>当前 archetype 所需表现组件完整时返回 true。</returns>
            internal bool TryApplyPresentation(
                ClientActorViewState state,
                ClientCombatResourceCatalog catalog)
            {
                if (catalog == null)
                {
                    return true;
                }

                if (ArchetypeID == ClientBattleContentIdentity.FanProjectileArchetype)
                {
                    return _view == null;
                }

                return _view != null && _view.TryApplyState(state, catalog);
            }

            /// <summary>把一次 current generation cue 提交给纯表现组件。</summary>
            /// <param name="cue">已由 registry 去重的 cue。</param>
            /// <param name="catalog">Production catalog。</param>
            internal void TryApplyCue(
                ClientGameplayCue cue,
                ClientCombatResourceCatalog catalog)
            {
                _view?.TryApplyCue(cue, catalog);
            }

            /// <summary>更新不可变 presentation frame 派生的目标。</summary>
            internal void SetTarget(
                bool local,
                ClientBattleTransform sample)
            {
                Local = local;
                _sample = sample;
            }

            /// <summary>首次生成或 remote interpolation 时立即采用已收敛目标。</summary>
            internal void SnapToTarget()
            {
                if (Instance != null)
                {
                    Instance.transform.SetPositionAndRotation(
                        ToPosition(_sample),
                        ToRotation(_sample));
                }
            }

        }
    }
}
