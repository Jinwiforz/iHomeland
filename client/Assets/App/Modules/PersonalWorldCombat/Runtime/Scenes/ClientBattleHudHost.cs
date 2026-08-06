using System;
using IHomeland.Client.PersonalWorldCombat.Application;
using TMPro;
using UnityEngine;

using UnityEngine.Scripting.APIUpdating;

namespace IHomeland.Client.PersonalWorldCombat.Runtime.Scenes
{
    /// <summary>把 immutable battle HUD projection 写入 Scene-bound uGUI overlay。</summary>
    /// <remarks>本 Host 不是 logical screen owner，不切换 UI Toolkit route、EventSystem 或输入焦点。</remarks>
    [DisallowMultipleComponent]
    [MovedFrom(true, "IHomeland.Client.Scenes.PersonalWorld", "IHomeland.Client.Runtime", null)]
    public sealed class ClientBattleHudHost : MonoBehaviour
    {
        /// <summary>保存 battle overlay 的可见性与交互屏蔽根。</summary>
        [SerializeField]
        private CanvasGroup _root;

        /// <summary>保存低敏 battle availability 文本。</summary>
        [SerializeField]
        private TMP_Text _statusText;

        /// <summary>保存 local authority health 文本。</summary>
        [SerializeField]
        private TMP_Text _healthText;

        /// <summary>保存 local authority weapon 文本。</summary>
        [SerializeField]
        private TMP_Text _weaponText;

        /// <summary>保存 current primary availability 文本。</summary>
        [SerializeField]
        private TMP_Text _skillText;

        /// <summary>保存 Boss authority health 文本。</summary>
        [SerializeField]
        private TMP_Text _bossHealthText;

        /// <summary>保存 Boss authority phase/dead 文本。</summary>
        [SerializeField]
        private TMP_Text _bossStateText;

        /// <summary>保存当前绑定的 Scene generation。</summary>
        private long _sceneGeneration;

        /// <summary>保存最近已提交的 status 文本。</summary>
        private string _lastStatusText;

        /// <summary>保存最近已提交的 health 文本。</summary>
        private string _lastHealthText;

        /// <summary>保存最近已提交的 weapon 文本。</summary>
        private string _lastWeaponText;

        /// <summary>保存最近已提交的 skill 文本。</summary>
        private string _lastSkillText;

        /// <summary>保存最近已提交的 Boss health 文本。</summary>
        private string _lastBossHealthText;

        /// <summary>保存最近已提交的 Boss state 文本。</summary>
        private string _lastBossStateText;

        /// <summary>保存最近一次权威 HUD projection 的生命值文本。</summary>
        private string _committedHealthText;

        /// <summary>保存最近一次权威 HUD projection 的 weapon 文本。</summary>
        private string _committedWeaponText;

        /// <summary>保存最近一次权威 HUD projection 的 skill 文本。</summary>
        private string _committedSkillText;

        /// <summary>保存最近一次权威 HUD projection 的 Boss health 文本。</summary>
        private string _committedBossHealthText;

        /// <summary>保存最近一次权威 HUD projection 的 Boss state 文本。</summary>
        private string _committedBossStateText;

        /// <summary>验证 overlay 只包含直接且同 Scene 的引用。</summary>
        internal void ValidateConfiguration()
        {
            if (_root == null ||
                _statusText == null ||
                _healthText == null ||
                _weaponText == null ||
                _skillText == null ||
                _bossHealthText == null ||
                _bossStateText == null)
            {
                throw new InvalidOperationException(
                    "ClientBattleHudHost 缺少 CanvasGroup 或 battle TMP_Text 引用。");
            }

            if (_root.gameObject.scene != gameObject.scene ||
                _statusText.gameObject.scene != gameObject.scene ||
                _healthText.gameObject.scene != gameObject.scene ||
                _weaponText.gameObject.scene != gameObject.scene ||
                _skillText.gameObject.scene != gameObject.scene ||
                _bossHealthText.gameObject.scene != gameObject.scene ||
                _bossStateText.gameObject.scene != gameObject.scene)
            {
                throw new InvalidOperationException(
                    "ClientBattleHudHost 引用必须属于 current Scene。");
            }
        }

        /// <summary>绑定 current Scene generation 并提交 loading baseline。</summary>
        /// <param name="sceneGeneration">SceneLifetime 分配的正 generation。</param>
        internal void Bind(long sceneGeneration)
        {
            ValidateConfiguration();
            if (_sceneGeneration != 0 || sceneGeneration <= 0)
            {
                throw new InvalidOperationException(
                    "ClientBattleHudHost 不能重复绑定或绑定无效 generation。");
            }

            _sceneGeneration = sceneGeneration;
            _root.alpha = 1f;
            _root.interactable = false;
            _root.blocksRaycasts = false;
            ClearCachedText();
            SetWaitingText();
            SetStatusText("Entering battle...");
        }

        /// <summary>在 baseline 不可消费时显示低敏 runtime availability。</summary>
        /// <param name="snapshot">App Scope runtime 的不可变观察快照。</param>
        internal void ApplyRuntime(ClientBattleRuntimeSnapshot snapshot)
        {
            if (_sceneGeneration == 0 || snapshot == null)
            {
                return;
            }

            SetStatusText(
                FormatAvailability(
                    snapshot.Availability,
                    snapshot.Failure,
                    _committedHealthText != null));
            if (!snapshot.BaselineReady)
            {
                SetHealthText(LastKnown(_committedHealthText, "HP waiting"));
                SetWeaponText(LastKnown(_committedWeaponText, "Weapon waiting"));
                SetSkillText(LastKnown(_committedSkillText, "Primary waiting"));
                SetBossHealthText(LastKnown(_committedBossHealthText, "Boss HP waiting"));
                SetBossStateText(LastKnown(_committedBossStateText, "Boss state waiting"));
            }
        }

        /// <summary>应用与 actor/camera 同 generation 的 HUD projection。</summary>
        /// <param name="state">Current generation 的 immutable HUD state。</param>
        internal void Apply(ClientBattleHudViewState state)
        {
            if (_sceneGeneration == 0 || state == null)
            {
                return;
            }

            SetStatusText(
                FormatAvailability(
                    state.Availability,
                    ClientBattleFailure.None,
                    hasCommittedState: true));
            _committedHealthText =
                $"HP {state.LocalHealthMilli / 1000f:0.###}/{state.LocalMaxHealthMilli / 1000f:0.###}";
            _committedWeaponText = state.EquippedWeaponID ==
                ClientBattleContentIdentity.FanWeapon
                    ? "Weapon Fan"
                    : "Weapon Sword";
            _committedSkillText = state.InputEnabled
                ? "Primary Ready"
                : "Primary Locked";
            _committedBossHealthText = state.BossMaxHealthMilli == 0
                ? "Boss absent"
                : $"Boss HP {state.BossHealthMilli / 1000f:0.###}/{state.BossMaxHealthMilli / 1000f:0.###}";
            _committedBossStateText = state.BossMaxHealthMilli == 0
                ? "Boss state absent"
                : state.BossDead
                    ? $"Boss phase {state.BossPhase} Dead"
                    : $"Boss phase {state.BossPhase} Alive";
            SetHealthText(_committedHealthText);
            SetWeaponText(_committedWeaponText);
            SetSkillText(_committedSkillText);
            SetBossHealthText(_committedBossHealthText);
            SetBossStateText(_committedBossStateText);
        }

        /// <summary>在目标替换或终态失败时丢弃不再属于 current world 的 HUD 事实。</summary>
        internal void DiscardCommittedState()
        {
            if (_sceneGeneration == 0)
            {
                return;
            }

            _committedHealthText = null;
            _committedWeaponText = null;
            _committedSkillText = null;
            _committedBossHealthText = null;
            _committedBossStateText = null;
            SetWaitingText();
        }

        /// <summary>隐藏 overlay 并清除所有 Scene generation 文本。</summary>
        internal void Unbind()
        {
            _sceneGeneration = 0;
            if (_root != null)
            {
                _root.alpha = 0f;
                _root.interactable = false;
                _root.blocksRaycasts = false;
            }

            SetStatusText(string.Empty);
            SetHealthText(string.Empty);
            SetWeaponText(string.Empty);
            SetSkillText(string.Empty);
            SetBossHealthText(string.Empty);
            SetBossStateText(string.Empty);
            ClearCachedText();
        }

        /// <summary>在 Unity 销毁阶段清除迟到表现写入资格。</summary>
        private void OnDestroy()
        {
            Unbind();
        }

        /// <summary>提交无 current baseline 时的稳定等待文案。</summary>
        private void SetWaitingText()
        {
            SetHealthText("HP waiting");
            SetWeaponText("Weapon waiting");
            SetSkillText("Primary waiting");
            SetBossHealthText("Boss HP waiting");
            SetBossStateText("Boss state waiting");
        }

        /// <summary>清除重复写入缓存与最后可信 projection。</summary>
        private void ClearCachedText()
        {
            _lastStatusText = null;
            _lastHealthText = null;
            _lastWeaponText = null;
            _lastSkillText = null;
            _lastBossHealthText = null;
            _lastBossStateText = null;
            _committedHealthText = null;
            _committedWeaponText = null;
            _committedSkillText = null;
            _committedBossHealthText = null;
            _committedBossStateText = null;
        }

        /// <summary>为恢复期间的可信文案追加稳定 last-known 标记。</summary>
        /// <param name="committed">最近可信文案；可为空。</param>
        /// <param name="waiting">从未提交 projection 时的等待文案。</param>
        /// <returns>不含 transport 细节的 Basic Latin 文案。</returns>
        private static string LastKnown(string committed, string waiting)
        {
            return committed == null ? waiting : $"{committed} (last known)";
        }

        /// <summary>把封闭 availability 映射为不含 transport 细节的产品文案。</summary>
        /// <param name="availability">Application 提供的低敏可用性。</param>
        /// <param name="failure">仅在终态选择玩家动作的低敏原因。</param>
        /// <param name="hasCommittedState">是否已有可标记为 last known 的可信状态。</param>
        /// <returns>当前 TMP 基础字体可显示的稳定 Basic Latin 文案。</returns>
        private static string FormatAvailability(
            ClientBattleAvailability availability,
            ClientBattleFailure failure,
            bool hasCommittedState)
        {
            switch (availability)
            {
                case ClientBattleAvailability.Inactive:
                    return "Not in battle";
                case ClientBattleAvailability.Connecting:
                    return hasCommittedState
                        ? "Connection lost. Reconnecting..."
                        : "Entering battle...";
                case ClientBattleAvailability.LoadingBaseline:
                    return hasCommittedState
                        ? "Connection restored. Loading latest state..."
                        : "Loading character...";
                case ClientBattleAvailability.Active:
                    return "Battle ready";
                case ClientBattleAvailability.Retrying:
                    return "Connection lost. Reconnecting...";
                case ClientBattleAvailability.Unavailable:
                    return FormatUnavailable(failure);
                default:
                    return "Battle state unavailable";
            }
        }

        /// <summary>把 closed failure 映射为玩家可执行且不泄漏内部细节的终态文案。</summary>
        /// <param name="failure">Application runtime 提供的低敏失败。</param>
        /// <returns>只包含 Basic Latin 的稳定玩家文案。</returns>
        private static string FormatUnavailable(ClientBattleFailure failure)
        {
            switch (failure)
            {
                case ClientBattleFailure.Policy:
                    return "Battle is not available in this world.";
                case ClientBattleFailure.CallerCancelled:
                case ClientBattleFailure.TargetReplaced:
                    return "Battle entry was cancelled.";
                case ClientBattleFailure.Timeout:
                case ClientBattleFailure.Transport:
                    return "Connection failed. Re-enter the world.";
                case ClientBattleFailure.Protocol:
                case ClientBattleFailure.Backpressure:
                case ClientBattleFailure.CommitUnknown:
                    return "Battle failed to load. Re-enter the world.";
                case ClientBattleFailure.Security:
                case ClientBattleFailure.SessionInvalidated:
                    return "Session expired. Sign in again.";
                case ClientBattleFailure.Shutdown:
                    return "Battle closed.";
                case ClientBattleFailure.None:
                default:
                    return "Battle unavailable. Re-enter the world.";
            }
        }

        /// <summary>仅在 status 文本变化时提交 TMP 更新。</summary>
        /// <param name="value">待显示的稳定产品文本。</param>
        private void SetStatusText(string value)
        {
            SetText(_statusText, ref _lastStatusText, value);
        }

        /// <summary>仅在 health 文本变化时提交 TMP 更新。</summary>
        /// <param name="value">待显示的 authority health 文本。</param>
        private void SetHealthText(string value)
        {
            SetText(_healthText, ref _lastHealthText, value);
        }

        /// <summary>仅在 weapon 文本变化时提交 TMP 更新。</summary>
        /// <param name="value">待显示的 authority weapon 文本。</param>
        private void SetWeaponText(string value)
        {
            SetText(_weaponText, ref _lastWeaponText, value);
        }

        /// <summary>仅在 skill 文本变化时提交 TMP 更新。</summary>
        /// <param name="value">待显示的 current primary availability。</param>
        private void SetSkillText(string value)
        {
            SetText(_skillText, ref _lastSkillText, value);
        }

        /// <summary>仅在 Boss health 文本变化时提交 TMP 更新。</summary>
        /// <param name="value">待显示的 authority Boss health 文本。</param>
        private void SetBossHealthText(string value)
        {
            SetText(_bossHealthText, ref _lastBossHealthText, value);
        }

        /// <summary>仅在 Boss state 文本变化时提交 TMP 更新。</summary>
        /// <param name="value">待显示的 authority Boss phase/dead 文本。</param>
        private void SetBossStateText(string value)
        {
            SetText(_bossStateText, ref _lastBossStateText, value);
        }

        /// <summary>只在值变化时更新一个可选 TMP_Text。</summary>
        /// <param name="target">Scene-bound TMP target。</param>
        /// <param name="lastValue">对应字段的最近提交缓存。</param>
        /// <param name="value">待提交文案。</param>
        private static void SetText(
            TMP_Text target,
            ref string lastValue,
            string value)
        {
            if (target == null || string.Equals(lastValue, value, StringComparison.Ordinal))
            {
                return;
            }

            lastValue = value;
            target.text = value;
        }
    }
}
