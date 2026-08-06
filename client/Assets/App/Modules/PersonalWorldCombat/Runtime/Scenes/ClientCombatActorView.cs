using System;
using IHomeland.Client.PersonalWorldCombat.Application;
using TMPro;
using UnityEngine;

using UnityEngine.Scripting.APIUpdating;

namespace IHomeland.Client.PersonalWorldCombat.Runtime.Scenes
{
    /// <summary>把 immutable actor state 与 current cue 写入一个 Prefab 的纯表现组件。</summary>
    /// <remarks>
    /// 本组件不拥有 entity lifecycle、damage、hit、death 或网络命令；Collider、Animation Event
    /// 与 VFX collision 均不得作为其输入或输出。
    /// </remarks>
    [DisallowMultipleComponent]
    [MovedFrom(true, "IHomeland.Client.Scenes.PersonalWorld", "IHomeland.Client.Runtime", null)]
    public sealed class ClientCombatActorView : MonoBehaviour
    {
        /// <summary>保存全部 production controller 必须包含的 dead 参数 hash。</summary>
        private static readonly int DeadParameter = Animator.StringToHash("Dead");

        /// <summary>保存全部 production controller 必须包含的 ability trigger hash。</summary>
        private static readonly int AbilityParameter = Animator.StringToHash("Ability");

        /// <summary>保存全部 production controller 必须包含的 locomotion 参数 hash。</summary>
        private static readonly int MovingParameter = Animator.StringToHash("Moving");

        /// <summary>保存只驱动表现的 Animator。</summary>
        [SerializeField]
        private Animator _animator;

        /// <summary>保存 player 武器 display 的唯一实例父节点。</summary>
        [SerializeField]
        private Transform _weaponAnchor;

        /// <summary>保存 current generation cue 的 Scene-owned AudioSource。</summary>
        [SerializeField]
        private AudioSource _audioSource;

        /// <summary>保存只读 world-health 文本；可为空以支持无 HUD projectile。</summary>
        [SerializeField]
        private TMP_Text _worldHealthText;

        /// <summary>保存 Boss phase 可读性强调对象；非 Boss 可为空。</summary>
        [SerializeField]
        private GameObject _phaseAccent;

        /// <summary>保存当前已实例化的 weapon display。</summary>
        private GameObject _weaponInstance;

        /// <summary>保存当前表现已消费的 weapon ID。</summary>
        private uint _weaponID;

        /// <summary>验证 Prefab 只具有直接表现引用且包含冻结 Animator 参数。</summary>
        /// <param name="weaponRequired">Player Prefab 是否必须提供 weapon anchor。</param>
        /// <exception cref="InvalidOperationException">引用或 Animator contract 漂移时抛出。</exception>
        public void ValidateConfiguration(bool weaponRequired)
        {
            if (_animator == null ||
                _audioSource == null ||
                _worldHealthText == null ||
                _animator.applyRootMotion ||
                _audioSource.playOnAwake ||
                (weaponRequired && _weaponAnchor == null))
            {
                throw new InvalidOperationException(
                    "ClientCombatActorView 引用、Root Motion、AudioSource 或 weapon anchor 配置无效。");
            }

            var validateAnimatorParameters = gameObject.scene.IsValid();
#if UNITY_EDITOR
            validateAnimatorParameters = true;
#endif
            if (validateAnimatorParameters &&
                (!HasParameter(_animator, DeadParameter, AnimatorControllerParameterType.Bool) ||
                 !HasParameter(_animator, AbilityParameter, AnimatorControllerParameterType.Trigger) ||
                 !HasParameter(_animator, MovingParameter, AnimatorControllerParameterType.Bool)))
            {
                throw new InvalidOperationException(
                    "ClientCombatActorView Animator 必须包含 Dead/Bool、Ability/Trigger 与 Moving/Bool。");
            }
        }

        /// <summary>应用 current authority snapshot 派生的持续表现状态。</summary>
        /// <param name="state">Current generation immutable actor state。</param>
        /// <param name="catalog">唯一 production resource catalog。</param>
        /// <returns>资源与 Animator contract 完整时返回 true。</returns>
        internal bool TryApplyState(
            ClientActorViewState state,
            ClientCombatResourceCatalog catalog)
        {
            if (state == null || catalog == null || _animator == null || _audioSource == null)
            {
                return false;
            }

            var abilityID = AbilityForState(state);
            if (!catalog.TryGetAbilityAnimator(abilityID, out var controller))
            {
                return false;
            }

            if (_animator.runtimeAnimatorController != controller)
            {
                _animator.runtimeAnimatorController = controller;
            }

            if (!HasParameter(_animator, DeadParameter, AnimatorControllerParameterType.Bool) ||
                !HasParameter(_animator, AbilityParameter, AnimatorControllerParameterType.Trigger) ||
                !HasParameter(_animator, MovingParameter, AnimatorControllerParameterType.Bool))
            {
                return false;
            }

            if (state.ArchetypeID == ClientBattleContentIdentity.PlayerArchetype &&
                !TryApplyWeapon(state.EquippedWeaponID, catalog))
            {
                return false;
            }

            _animator.SetBool(DeadParameter, state.Dead);
            _animator.SetBool(MovingParameter, state.Moving && !state.Dead);
            if (_worldHealthText != null)
            {
                _worldHealthText.text =
                    $"HP {state.HealthMilli / 1000f:0.###}/{state.MaxHealthMilli / 1000f:0.###}";
            }

            if (_phaseAccent != null)
            {
                _phaseAccent.SetActive(!state.Dead && state.Phase > 1);
            }

            return true;
        }

        /// <summary>消费一次 current generation ability cue。</summary>
        /// <param name="cue">已由 registry 做 generation 与 event 去重的 cue。</param>
        /// <param name="catalog">唯一 production resource catalog。</param>
        /// <returns>Animator 与 Audio 资源可消费时返回 true。</returns>
        internal bool TryApplyCue(
            ClientGameplayCue cue,
            ClientCombatResourceCatalog catalog)
        {
            if (cue == null || catalog == null || _animator == null || _audioSource == null ||
                !catalog.TryGetAbilityAnimator(cue.AbilityID, out var controller))
            {
                return false;
            }

            if (_animator.runtimeAnimatorController != controller)
            {
                _animator.runtimeAnimatorController = controller;
            }

            if (!HasParameter(_animator, AbilityParameter, AnimatorControllerParameterType.Trigger))
            {
                return false;
            }

            if (cue.Phase == ClientBattleAbilityPhase.Started)
            {
                _animator.SetTrigger(AbilityParameter);
                _audioSource.PlayOneShot(catalog.CombatAudio);
            }

            return true;
        }

        /// <summary>停止 Scene teardown 后不再有效的音频并退役 weapon display。</summary>
        private void OnDestroy()
        {
            if (_audioSource != null)
            {
                _audioSource.Stop();
            }

            RetireWeapon();
        }

        /// <summary>按 current weapon state 原子替换纯表现 display。</summary>
        /// <param name="weaponID">Current authority weapon mapping。</param>
        /// <param name="catalog">唯一 production resource catalog。</param>
        /// <returns>Weapon mapping、anchor 与 Prefab 完整时返回 true。</returns>
        private bool TryApplyWeapon(
            uint weaponID,
            ClientCombatResourceCatalog catalog)
        {
            if (_weaponAnchor == null ||
                !catalog.TryGetWeaponPrefab(weaponID, out var prefab))
            {
                return false;
            }

            if (_weaponID == weaponID && _weaponInstance != null)
            {
                return true;
            }

            RetireWeapon();
            _weaponInstance = Instantiate(prefab, _weaponAnchor, false);
            _weaponInstance.name = $"WeaponDisplay-{weaponID}";
            _weaponID = weaponID;
            return true;
        }

        /// <summary>立即失活并按 Unity lifecycle 销毁旧 weapon display。</summary>
        private void RetireWeapon()
        {
            if (_weaponInstance != null)
            {
                _weaponInstance.SetActive(false);
                Destroy(_weaponInstance);
            }

            _weaponInstance = null;
            _weaponID = 0;
        }

        /// <summary>按 actor archetype 与 current weapon 选择已冻结 primary ability。</summary>
        /// <param name="state">Current immutable actor state。</param>
        /// <returns>Production numeric ability ID。</returns>
        private static uint AbilityForState(ClientActorViewState state)
        {
            switch (state.ArchetypeID)
            {
                case ClientBattleContentIdentity.PlayerArchetype:
                    return state.EquippedWeaponID == ClientBattleContentIdentity.FanWeapon
                        ? ClientBattleContentIdentity.FanAbility
                        : ClientBattleContentIdentity.SwordAbility;
                case ClientBattleContentIdentity.MonsterArchetype:
                    return ClientBattleContentIdentity.MonsterAbility;
                case ClientBattleContentIdentity.BossArchetype:
                    return ClientBattleContentIdentity.BossAbility;
                default:
                    return 0;
            }
        }

        /// <summary>验证 current Animator 包含指定名称 hash 与参数类型。</summary>
        /// <param name="animator">待验证 Animator。</param>
        /// <param name="nameHash">冻结参数名称 hash。</param>
        /// <param name="type">冻结参数类型。</param>
        /// <returns>找到精确参数时返回 true。</returns>
        private static bool HasParameter(
            Animator animator,
            int nameHash,
            AnimatorControllerParameterType type)
        {
            if (animator == null)
            {
                return false;
            }

            var parameters = animator.parameters;
            for (var index = 0; index < parameters.Length; index++)
            {
                if (parameters[index].nameHash == nameHash && parameters[index].type == type)
                {
                    return true;
                }
            }

            return false;
        }

    }
}
