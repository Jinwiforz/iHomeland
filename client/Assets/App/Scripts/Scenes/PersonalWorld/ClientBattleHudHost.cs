using System;
using IHomeland.Client.Application.Battle;
using TMPro;
using UnityEngine;

namespace IHomeland.Client.Scenes.PersonalWorld
{
    /// <summary>
    /// 把 immutable battle HUD projection 写入 Scene-bound uGUI overlay。
    /// </summary>
    /// <remarks>
    /// 本 Host 不是 logical screen owner，不切换 UI Toolkit route、EventSystem 或输入焦点。
    /// </remarks>
    [DisallowMultipleComponent]
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

        /// <summary>保存当前绑定的 Scene generation。</summary>
        private long _sceneGeneration;

        /// <summary>保存最近已提交的 status 文本，避免重复使 TMP mesh 失效。</summary>
        private string _lastStatusText;

        /// <summary>保存最近已提交的 health 文本，避免重复使 TMP mesh 失效。</summary>
        private string _lastHealthText;

        /// <summary>保存最近一次权威 HUD projection 的生命值文本。</summary>
        private string _committedHealthText;

        /// <summary>验证 overlay 只包含直接且同 Scene 的引用。</summary>
        internal void ValidateConfiguration()
        {
            if (_root == null || _statusText == null || _healthText == null)
            {
                throw new InvalidOperationException(
                    "ClientBattleHudHost 缺少 CanvasGroup 或 TMP_Text 引用。");
            }

            if (_root.gameObject.scene != gameObject.scene ||
                _statusText.gameObject.scene != gameObject.scene ||
                _healthText.gameObject.scene != gameObject.scene)
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
            _lastStatusText = null;
            _lastHealthText = null;
            _committedHealthText = null;
            SetStatusText("Entering battle...");
            SetHealthText("HP waiting");
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
                SetHealthText(
                    _committedHealthText == null
                        ? "HP waiting"
                        : $"{_committedHealthText} (last known)");
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
                $"HP {state.LocalHealthMilli / 1000f:0.###}";
            SetHealthText(_committedHealthText);
        }

        /// <summary>在目标替换或终态失败时丢弃不再属于current world的HUD事实。</summary>
        internal void DiscardCommittedState()
        {
            if (_sceneGeneration == 0)
            {
                return;
            }

            _committedHealthText = null;
            SetHealthText("HP waiting");
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

            if (_statusText != null)
            {
                SetStatusText(string.Empty);
            }

            if (_healthText != null)
            {
                SetHealthText(string.Empty);
            }

            _lastStatusText = null;
            _lastHealthText = null;
            _committedHealthText = null;
        }

        /// <summary>在 Unity 销毁阶段清除迟到表现写入资格。</summary>
        private void OnDestroy()
        {
            Unbind();
        }

        /// <summary>把封闭 availability 映射为不含 transport 细节的产品文案。</summary>
        /// <param name="availability">Application 提供的低敏可用性。</param>
        /// <param name="failure">仅在终态选择玩家动作的低敏原因。</param>
        /// <param name="hasCommittedState">是否已有可标记为last known的可信HUD状态。</param>
        /// <returns>当前TMP基础字体可显示的稳定Basic Latin文案。</returns>
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
            if (_statusText == null ||
                string.Equals(
                    _lastStatusText,
                    value,
                    StringComparison.Ordinal))
            {
                return;
            }

            _lastStatusText = value;
            _statusText.text = value;
        }

        /// <summary>仅在 health 文本变化时提交 TMP 更新。</summary>
        /// <param name="value">待显示的 authority health 文本。</param>
        private void SetHealthText(string value)
        {
            if (_healthText == null ||
                string.Equals(
                    _lastHealthText,
                    value,
                    StringComparison.Ordinal))
            {
                return;
            }

            _lastHealthText = value;
            _healthText.text = value;
        }
    }
}
