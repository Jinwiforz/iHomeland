using System;
using System.Collections.Generic;
using IHomeland.Client.PersonalWorldCombat.Application;
using UnityEngine;

using UnityEngine.Scripting.APIUpdating;

namespace IHomeland.Client.PersonalWorldCombat.Runtime.Scenes
{
    /// <summary>标识 production presentation catalog 中受支持的 Unity 资源种类。</summary>
    public enum ClientCombatResourceKind
    {
        /// <summary>可实例化的角色、武器或投射物表现。</summary>
        Display = 1,

        /// <summary>只驱动表现状态机的 Animator Controller。</summary>
        Animator = 2,

        /// <summary>只由 current generation cue 触发的视觉效果。</summary>
        Vfx = 3,

        /// <summary>只由 current generation cue 触发的音频。</summary>
        Audio = 4,

        /// <summary>Scene-bound uGUI 表现资源。</summary>
        Hud = 5,

        /// <summary>只表达构图意图的 Cinemachine 资源。</summary>
        Camera = 6,
    }

    /// <summary>保存一个 logical key 与 Unity asset 的只读 build parity 投影。</summary>
    public readonly struct ClientCombatResourceReference
    {
        /// <summary>创建一个不包含 gameplay 数值的资源引用。</summary>
        /// <param name="logicalKey">Production presentation logical key。</param>
        /// <param name="kind">资源种类。</param>
        /// <param name="asset">由 Unity 序列化的直接资产引用。</param>
        public ClientCombatResourceReference(
            string logicalKey,
            ClientCombatResourceKind kind,
            UnityEngine.Object asset)
        {
            LogicalKey = logicalKey ?? throw new ArgumentNullException(nameof(logicalKey));
            Kind = kind;
            Asset = asset;
        }

        /// <summary>获取 production presentation logical key。</summary>
        public string LogicalKey { get; }

        /// <summary>获取资源种类。</summary>
        public ClientCombatResourceKind Kind { get; }

        /// <summary>获取 Unity 直接资产引用。</summary>
        public UnityEngine.Object Asset { get; }
    }

    /// <summary>保存一个 production semantic identity 与 numeric wire ID 的只读映射。</summary>
    public readonly struct ClientCombatNumericMapping
    {
        /// <summary>创建一个不可由加载顺序或字符串 hash 推导的冻结映射。</summary>
        /// <param name="kind">Production semantic kind。</param>
        /// <param name="semanticID">Production semantic identity。</param>
        /// <param name="numericID">非零 uint32 wire ID。</param>
        public ClientCombatNumericMapping(
            string kind,
            string semanticID,
            uint numericID)
        {
            Kind = kind ?? throw new ArgumentNullException(nameof(kind));
            SemanticID = semanticID ?? throw new ArgumentNullException(nameof(semanticID));
            NumericID = numericID;
        }

        /// <summary>获取 production semantic kind。</summary>
        public string Kind { get; }

        /// <summary>获取 production semantic identity。</summary>
        public string SemanticID { get; }

        /// <summary>获取冻结 numeric wire ID。</summary>
        public uint NumericID { get; }
    }

    /// <summary>
    /// 把 production logical keys 与 numeric mapping 绑定到唯一一组 tracked Unity assets。
    /// </summary>
    /// <remarks>
    /// Catalog 只保存表现引用和跨端 identity，不保存 damage、cooldown、AI、hit、death、
    /// reward、Session 或在线 world state。运行时不扫描 Resources，也不按资产名猜测回退。
    /// </remarks>
    [CreateAssetMenu(
        fileName = "ClientCombatResourceCatalog",
        menuName = "IHomeland/Combat/Client Combat Resource Catalog")]
    [MovedFrom(true, "IHomeland.Client.Scenes.PersonalWorld", "IHomeland.Client.Runtime", null)]
    public sealed class ClientCombatResourceCatalog : ScriptableObject
    {
        /// <summary>冻结 production package identity。</summary>
        public const string ProductionPackageID = "personal-world-combat-v1";

        /// <summary>冻结 server arena map identity。</summary>
        public const string ProductionMapID = "personal-world-combat/map/arena";

        /// <summary>保存 catalog 自声明的 production package identity。</summary>
        [SerializeField]
        [Tooltip("必须精确等于 personal-world-combat-v1。")]
        private string _packageID = ProductionPackageID;

        /// <summary>保存 Scene 可视映射对应的 server map identity。</summary>
        [SerializeField]
        [Tooltip("只用于 parity；Scene Transform 不是 authority source。")]
        private string _mapID = ProductionMapID;

        /// <summary>保存 player actor Prefab。</summary>
        [Header("Actor and projectile displays")]
        [SerializeField]
        private GameObject _playerDisplay;

        /// <summary>保存 ordinary monster actor Prefab。</summary>
        [SerializeField]
        private GameObject _monsterDisplay;

        /// <summary>保存 Boss actor Prefab。</summary>
        [SerializeField]
        private GameObject _bossDisplay;

        /// <summary>保存 fan projectile Prefab。</summary>
        [SerializeField]
        private GameObject _fanProjectileDisplay;

        /// <summary>保存 sword display Prefab。</summary>
        [Header("Weapon displays")]
        [SerializeField]
        private GameObject _swordDisplay;

        /// <summary>保存 fan display Prefab。</summary>
        [SerializeField]
        private GameObject _fanDisplay;

        /// <summary>保存 sword primary Animator Controller。</summary>
        [Header("Ability animators")]
        [SerializeField]
        private RuntimeAnimatorController _swordAnimator;

        /// <summary>保存 fan primary Animator Controller。</summary>
        [SerializeField]
        private RuntimeAnimatorController _fanAnimator;

        /// <summary>保存 ordinary monster strike Animator Controller。</summary>
        [SerializeField]
        private RuntimeAnimatorController _monsterAnimator;

        /// <summary>保存 Boss slam Animator Controller。</summary>
        [SerializeField]
        private RuntimeAnimatorController _bossAnimator;

        /// <summary>保存 authority target cue 对应的 damage VFX Prefab。</summary>
        [Header("Cue resources")]
        [SerializeField]
        private GameObject _damageVfx;

        /// <summary>保存 current generation combat cue 共用的 AudioClip。</summary>
        [SerializeField]
        private AudioClip _combatAudio;

        /// <summary>保存 player state HUD Prefab。</summary>
        [Header("HUD and camera resources")]
        [SerializeField]
        private GameObject _playerHud;

        /// <summary>保存 Boss state HUD Prefab。</summary>
        [SerializeField]
        private GameObject _bossHud;

        /// <summary>保存 melee camera rig Prefab。</summary>
        [SerializeField]
        private GameObject _meleeCamera;

        /// <summary>保存 ranged camera rig Prefab。</summary>
        [SerializeField]
        private GameObject _rangedCamera;

        /// <summary>获取 catalog 自声明的 production package identity。</summary>
        public string PackageID => _packageID;

        /// <summary>获取 Scene 可视映射对应的 server map identity。</summary>
        public string MapID => _mapID;

        /// <summary>获取 damage VFX Prefab。</summary>
        internal GameObject DamageVfx => _damageVfx;

        /// <summary>获取 combat cue AudioClip。</summary>
        internal AudioClip CombatAudio => _combatAudio;

        /// <summary>返回全部 16 个 production logical resource binding。</summary>
        /// <returns>顺序稳定且不包含 authority 数值的资源投影。</returns>
        public IReadOnlyList<ClientCombatResourceReference> GetResourceReferences()
        {
            return new[]
            {
                Resource("actor.player.display", ClientCombatResourceKind.Display, _playerDisplay),
                Resource("actor.monster.display", ClientCombatResourceKind.Display, _monsterDisplay),
                Resource("actor.boss.display", ClientCombatResourceKind.Display, _bossDisplay),
                Resource("weapon.sword.display", ClientCombatResourceKind.Display, _swordDisplay),
                Resource("weapon.fan.display", ClientCombatResourceKind.Display, _fanDisplay),
                Resource("projectile.fan.display", ClientCombatResourceKind.Display, _fanProjectileDisplay),
                Resource("ability.sword.primary", ClientCombatResourceKind.Animator, _swordAnimator),
                Resource("ability.fan.primary", ClientCombatResourceKind.Animator, _fanAnimator),
                Resource("ability.monster.strike", ClientCombatResourceKind.Animator, _monsterAnimator),
                Resource("ability.boss.slam", ClientCombatResourceKind.Animator, _bossAnimator),
                Resource("effect.damage.vfx", ClientCombatResourceKind.Vfx, _damageVfx),
                Resource("cue.combat.audio", ClientCombatResourceKind.Audio, _combatAudio),
                Resource("actor.state.hud", ClientCombatResourceKind.Hud, _playerHud),
                Resource("boss.state.hud", ClientCombatResourceKind.Hud, _bossHud),
                Resource("camera.melee.intent", ClientCombatResourceKind.Camera, _meleeCamera),
                Resource("camera.ranged.intent", ClientCombatResourceKind.Camera, _rangedCamera),
            };
        }

        /// <summary>返回 production package 冻结的 10 个 numeric wire mapping。</summary>
        /// <returns>按 semantic kind 与 numeric ID 排序的稳定映射。</returns>
        public IReadOnlyList<ClientCombatNumericMapping> GetNumericMappings()
        {
            return new[]
            {
                Mapping("actor", "personal-world-combat/actor/player", ClientBattleContentIdentity.PlayerArchetype),
                Mapping("actor", "personal-world-combat/actor/ordinary-monster", ClientBattleContentIdentity.MonsterArchetype),
                Mapping("actor", "personal-world-combat/actor/boss", ClientBattleContentIdentity.BossArchetype),
                Mapping("weapon", "personal-world-combat/weapon/sword", ClientBattleContentIdentity.SwordWeapon),
                Mapping("weapon", "personal-world-combat/weapon/fan", ClientBattleContentIdentity.FanWeapon),
                Mapping("ability", "personal-world-combat/ability/sword-primary", ClientBattleContentIdentity.SwordAbility),
                Mapping("ability", "personal-world-combat/ability/fan-primary", ClientBattleContentIdentity.FanAbility),
                Mapping("ability", "personal-world-combat/ability/monster-strike", ClientBattleContentIdentity.MonsterAbility),
                Mapping("ability", "personal-world-combat/ability/boss-slam", ClientBattleContentIdentity.BossAbility),
                Mapping("projectile", "personal-world-combat/projectile/fan-blade", ClientBattleContentIdentity.FanProjectileArchetype),
            };
        }

        /// <summary>验证唯一 catalog 的 identity、完整引用与互异资源约束。</summary>
        /// <exception cref="InvalidOperationException">缺项、identity 或重复 display 漂移时抛出。</exception>
        public void ValidateCompleteness()
        {
            if (!string.Equals(_packageID, ProductionPackageID, StringComparison.Ordinal) ||
                !string.Equals(_mapID, ProductionMapID, StringComparison.Ordinal))
            {
                throw new InvalidOperationException(
                    "ClientCombatResourceCatalog production identity 漂移。");
            }

            var resources = GetResourceReferences();
            var keys = new HashSet<string>(StringComparer.Ordinal);
            for (var index = 0; index < resources.Count; index++)
            {
                var resource = resources[index];
                if (!keys.Add(resource.LogicalKey) || resource.Asset == null)
                {
                    throw new InvalidOperationException(
                        $"ClientCombatResourceCatalog 缺少或重复资源：{resource.LogicalKey}。");
                }
            }

            RequireDistinct(
                "actor/projectile displays",
                _playerDisplay,
                _monsterDisplay,
                _bossDisplay,
                _fanProjectileDisplay);
            RequireDistinct("weapon displays", _swordDisplay, _fanDisplay);
            RequireDistinct(
                "ability animators",
                _swordAnimator,
                _fanAnimator,
                _monsterAnimator,
                _bossAnimator);
            RequireDistinct("HUD resources", _playerHud, _bossHud);
            RequireDistinct("camera resources", _meleeCamera, _rangedCamera);
            ValidateDisplayPrefab(
                _playerDisplay,
                requiresActorView: true,
                requiresWeaponAnchor: true);
            ValidateDisplayPrefab(
                _monsterDisplay,
                requiresActorView: true,
                requiresWeaponAnchor: false);
            ValidateDisplayPrefab(
                _bossDisplay,
                requiresActorView: true,
                requiresWeaponAnchor: false);
            ValidateDisplayPrefab(
                _fanProjectileDisplay,
                requiresActorView: false,
                requiresWeaponAnchor: false);
            ValidatePassivePrefab(_swordDisplay, "sword display");
            ValidatePassivePrefab(_fanDisplay, "fan display");
            ValidatePassivePrefab(_damageVfx, "damage VFX");
            ValidatePassivePrefab(_playerHud, "player HUD");
            ValidatePassivePrefab(_bossHud, "Boss HUD");
            ValidatePassivePrefab(_meleeCamera, "melee camera");
            ValidatePassivePrefab(_rangedCamera, "ranged camera");
            if (_playerHud.GetComponentsInChildren<Canvas>(true).Length != 0 ||
                _bossHud.GetComponentsInChildren<Canvas>(true).Length != 0)
            {
                throw new InvalidOperationException(
                    "Combat HUD Prefab 必须是现有 Scene Canvas 下的 panel，不能创建第二个 Canvas。");
            }

            if (_meleeCamera.GetComponentsInChildren<Camera>(true).Length != 0 ||
                _rangedCamera.GetComponentsInChildren<Camera>(true).Length != 0 ||
                _meleeCamera.GetComponentsInChildren<AudioListener>(true).Length != 0 ||
                _rangedCamera.GetComponentsInChildren<AudioListener>(true).Length != 0)
            {
                throw new InvalidOperationException(
                    "Combat camera rig Prefab 禁止包含 Camera 或 AudioListener owner。");
            }
            var particleSystems =
                _damageVfx.GetComponentsInChildren<ParticleSystem>(true);
            if (particleSystems.Length == 0)
            {
                throw new InvalidOperationException(
                    "ClientCombatResourceCatalog damage VFX 缺少 ParticleSystem。");
            }

            for (var index = 0; index < particleSystems.Length; index++)
            {
                var main = particleSystems[index].main;
                if (main.loop ||
                    main.playOnAwake ||
                    particleSystems[index].collision.enabled ||
                    particleSystems[index].trigger.enabled)
                {
                    throw new InvalidOperationException(
                        "ClientCombatResourceCatalog damage VFX 必须是非循环、非自动播放且无 collision/trigger authority。");
                }
            }
        }

        /// <summary>按 production archetype ID 解析唯一 actor/projectile Prefab。</summary>
        /// <param name="archetypeID">Frozen numeric archetype mapping。</param>
        /// <param name="prefab">成功时返回直接 Prefab 引用。</param>
        /// <returns>Mapping 已登记且资源非空时返回 true。</returns>
        internal bool TryGetActorPrefab(uint archetypeID, out GameObject prefab)
        {
            switch (archetypeID)
            {
                case ClientBattleContentIdentity.PlayerArchetype:
                    prefab = _playerDisplay;
                    break;
                case ClientBattleContentIdentity.MonsterArchetype:
                    prefab = _monsterDisplay;
                    break;
                case ClientBattleContentIdentity.BossArchetype:
                    prefab = _bossDisplay;
                    break;
                case ClientBattleContentIdentity.FanProjectileArchetype:
                    prefab = _fanProjectileDisplay;
                    break;
                default:
                    prefab = null;
                    break;
            }

            return prefab != null;
        }

        /// <summary>按 current weapon ID 解析唯一 display Prefab。</summary>
        /// <param name="weaponID">Frozen numeric weapon mapping。</param>
        /// <param name="prefab">成功时返回直接 Prefab 引用。</param>
        /// <returns>Mapping 已登记且资源非空时返回 true。</returns>
        internal bool TryGetWeaponPrefab(uint weaponID, out GameObject prefab)
        {
            prefab = weaponID == ClientBattleContentIdentity.SwordWeapon
                ? _swordDisplay
                : weaponID == ClientBattleContentIdentity.FanWeapon
                    ? _fanDisplay
                    : null;
            return prefab != null;
        }

        /// <summary>按 ability ID 解析唯一 Animator Controller。</summary>
        /// <param name="abilityID">Frozen numeric ability mapping。</param>
        /// <param name="controller">成功时返回直接 Animator Controller 引用。</param>
        /// <returns>Mapping 已登记且资源非空时返回 true。</returns>
        internal bool TryGetAbilityAnimator(
            uint abilityID,
            out RuntimeAnimatorController controller)
        {
            switch (abilityID)
            {
                case ClientBattleContentIdentity.SwordAbility:
                    controller = _swordAnimator;
                    break;
                case ClientBattleContentIdentity.FanAbility:
                    controller = _fanAnimator;
                    break;
                case ClientBattleContentIdentity.MonsterAbility:
                    controller = _monsterAnimator;
                    break;
                case ClientBattleContentIdentity.BossAbility:
                    controller = _bossAnimator;
                    break;
                default:
                    controller = null;
                    break;
            }

            return controller != null;
        }

        /// <summary>把强类型字段投影为 logical resource reference。</summary>
        /// <param name="key">Production logical key。</param>
        /// <param name="kind">资源种类。</param>
        /// <param name="asset">Unity 直接引用。</param>
        /// <returns>不可变资源投影。</returns>
        private static ClientCombatResourceReference Resource(
            string key,
            ClientCombatResourceKind kind,
            UnityEngine.Object asset)
        {
            return new ClientCombatResourceReference(key, kind, asset);
        }

        /// <summary>把冻结常量投影为 build parity numeric mapping。</summary>
        /// <param name="kind">Semantic kind。</param>
        /// <param name="semanticID">Semantic identity。</param>
        /// <param name="numericID">Numeric wire ID。</param>
        /// <returns>不可变 numeric mapping。</returns>
        private static ClientCombatNumericMapping Mapping(
            string kind,
            string semanticID,
            uint numericID)
        {
            return new ClientCombatNumericMapping(kind, semanticID, numericID);
        }

        /// <summary>拒绝不同 logical keys 意外共享同一必须互异的 Unity asset。</summary>
        /// <param name="group">低敏资源组名称。</param>
        /// <param name="assets">必须全部互异的资源。</param>
        private static void RequireDistinct(
            string group,
            params UnityEngine.Object[] assets)
        {
            var distinct = new HashSet<UnityEngine.Object>();
            for (var index = 0; index < assets.Length; index++)
            {
                if (assets[index] == null || !distinct.Add(assets[index]))
                {
                    throw new InvalidOperationException(
                        $"ClientCombatResourceCatalog {group} 缺失或重复。");
                }
            }
        }

        /// <summary>验证 actor/projectile Prefab 不携带客户端 physics authority。</summary>
        /// <param name="prefab">待验证 Prefab asset。</param>
        /// <param name="requiresActorView">是否必须且只能有一个强类型表现组件。</param>
        /// <param name="requiresWeaponAnchor">表现组件是否必须提供 player weapon anchor。</param>
        private static void ValidateDisplayPrefab(
            GameObject prefab,
            bool requiresActorView,
            bool requiresWeaponAnchor)
        {
            ValidatePassivePrefab(prefab, "actor/projectile display");
            var views = prefab.GetComponentsInChildren<ClientCombatActorView>(true);
            if ((requiresActorView && views.Length != 1) ||
                (!requiresActorView && views.Length != 0))
            {
                throw new InvalidOperationException(
                    "Combat actor Prefab 的 ClientCombatActorView 数量无效。");
            }

            if (requiresActorView)
            {
                views[0].ValidateConfiguration(requiresWeaponAnchor);
            }
        }

        /// <summary>验证纯表现 Prefab 是 asset 且不包含 Collider/Rigidbody。</summary>
        /// <param name="prefab">待验证 Prefab asset。</param>
        /// <param name="label">低敏错误标签。</param>
        private static void ValidatePassivePrefab(GameObject prefab, string label)
        {
            if (prefab == null ||
                prefab.scene.IsValid() ||
                prefab.GetComponentsInChildren<Collider>(true).Length != 0 ||
                prefab.GetComponentsInChildren<Rigidbody>(true).Length != 0)
            {
                throw new InvalidOperationException(
                    $"ClientCombatResourceCatalog {label} 不是无 physics authority 的 Prefab asset。");
            }
        }
    }
}
