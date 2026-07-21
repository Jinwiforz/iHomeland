using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Presentation.Navigation;
using TMPro;
using UnityEngine;
using UnityEngine.UI;

namespace IHomeland.Client.Presentation.PersonalWorld
{
    /// <summary>
    /// 把直接引用的最小 uGUI WorldHud 绑定到当前 Scene generation。
    /// </summary>
    [DisallowMultipleComponent]
    public sealed class ClientPersonalWorldHudView : ClientUiProductBindingBehaviour, IClientUiProductBinding
    {
        /// <summary>保存角色 TextMeshPro 文本直接引用。</summary>
        [SerializeField]
        private TMP_Text _roleText;

        /// <summary>保存世界标识 TextMeshPro 文本直接引用。</summary>
        [SerializeField]
        private TMP_Text _worldText;

        /// <summary>保存访问会话 TextMeshPro 文本直接引用。</summary>
        [SerializeField]
        private TMP_Text _visitText;

        /// <summary>保存打开 WorldVisit 页按钮直接引用。</summary>
        [SerializeField]
        private Button _worldVisitButton;

        /// <summary>保存 Visitor 主动离开按钮直接引用。</summary>
        [SerializeField]
        private Button _leaveButton;

        /// <summary>保存由 Composition 注入的唯一 Experience。</summary>
        private ClientPersonalWorldExperience _experience;

        /// <summary>保存 current scene-bound route binding。</summary>
        private ClientUiRouteBinding _binding;

        /// <summary>取消 HUD generation 的 command 等待。</summary>
        private CancellationTokenSource _bindingCancellation;

        /// <summary>为程序化 fixture 在激活前配置与 Inspector 等价的 HUD 直接引用。</summary>
        /// <param name="roleText">角色文本。</param>
        /// <param name="worldText">世界文本。</param>
        /// <param name="visitText">访问会话文本。</param>
        /// <param name="worldVisitButton">打开访问页按钮。</param>
        /// <param name="leaveButton">Visitor 离开按钮。</param>
        /// <exception cref="ArgumentNullException">任一引用为空时抛出。</exception>
        /// <exception cref="InvalidOperationException">组件已绑定或 GameObject 已激活时抛出。</exception>
        internal void ConfigureBeforeActivation(
            TMP_Text roleText,
            TMP_Text worldText,
            TMP_Text visitText,
            Button worldVisitButton,
            Button leaveButton)
        {
            if (_binding != null || isActiveAndEnabled)
            {
                throw new InvalidOperationException("WorldHud 只能在激活和绑定前配置。");
            }

            _roleText = roleText ?? throw new ArgumentNullException(nameof(roleText));
            _worldText = worldText ?? throw new ArgumentNullException(nameof(worldText));
            _visitText = visitText ?? throw new ArgumentNullException(nameof(visitText));
            _worldVisitButton = worldVisitButton ?? throw new ArgumentNullException(nameof(worldVisitButton));
            _leaveButton = leaveButton ?? throw new ArgumentNullException(nameof(leaveButton));
        }

        /// <summary>在 AppLifetime 启动前接收唯一产品上下文。</summary>
        /// <param name="context">必须是当前 Composition 创建的个人世界 Experience。</param>
        void IClientUiProductBinding.Configure(IClientUiProductContext context)
        {
            if (_experience != null || _binding != null)
            {
                throw new InvalidOperationException("WorldHud 只能在 bind 前配置一次。");
            }

            _experience = context as ClientPersonalWorldExperience ??
                throw new ArgumentException("WorldHud 只接受 ClientPersonalWorldExperience。", nameof(context));
        }

        /// <summary>绑定 current Scene generation、View State 与按钮 callback。</summary>
        /// <param name="binding">必须是正 Scene generation 的 WorldHud binding。</param>
        /// <param name="cancellationToken">Candidate 或 App 停止取消信号。</param>
        /// <returns>HUD 已呈现 current state 时完成。</returns>
        Task IClientUiProductBinding.BindAsync(
            ClientUiRouteBinding binding,
            CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            ValidateConfiguration();
            if (_experience == null || _binding != null || binding == null ||
                binding.RouteId != ClientUiRouteId.WorldHud || binding.SceneGeneration <= 0)
            {
                throw new InvalidOperationException("WorldHud 缺少 Experience 或 Scene binding 不匹配。");
            }

            _binding = binding;
            _bindingCancellation = CancellationTokenSource.CreateLinkedTokenSource(
                binding.CancellationToken,
                cancellationToken);
            try
            {
                _worldVisitButton.onClick.AddListener(OnWorldVisitClicked);
                _leaveButton.onClick.AddListener(OnLeaveClicked);
                _experience.ViewStateChanged += OnViewStateChanged;
                Render(_experience.ViewState);
                return Task.CompletedTask;
            }
            catch
            {
                ClearBinding();
                throw;
            }
        }

        /// <summary>解除 View State、按钮 callback 与 scene token。</summary>
        /// <param name="cancellationToken">Scene 或 App 清理 deadline。</param>
        /// <returns>HUD 不再允许提交 command 时完成。</returns>
        Task IClientUiProductBinding.UnbindAsync(CancellationToken cancellationToken)
        {
            if (_binding == null)
            {
                return Task.CompletedTask;
            }

            ClearBinding();
            return Task.CompletedTask;
        }

        /// <summary>对称释放 View State、按钮 callback 与 scene cancellation。</summary>
        private void ClearBinding()
        {
            _experience.ViewStateChanged -= OnViewStateChanged;
            _worldVisitButton.onClick.RemoveListener(OnWorldVisitClicked);
            _leaveButton.onClick.RemoveListener(OnLeaveClicked);
            _bindingCancellation?.Cancel();
            _bindingCancellation?.Dispose();
            _bindingCancellation = null;
            _binding = null;
        }

        /// <summary>验证 HUD 全部直接引用。</summary>
        /// <exception cref="InvalidOperationException">任一表现引用缺失时抛出。</exception>
        internal void ValidateConfiguration()
        {
            if (_roleText == null || _worldText == null || _visitText == null ||
                _worldVisitButton == null || _leaveButton == null)
            {
                throw new InvalidOperationException("WorldHud 缺少 TextMeshPro 或 Button 直接引用。");
            }
        }

        /// <summary>观察打开 WorldVisit 语义动作。</summary>
        private void OnWorldVisitClicked()
        {
            _ = ObserveAsync(_experience.ShowWorldVisitAsync);
        }

        /// <summary>观察 Visitor 主动离开语义动作。</summary>
        private void OnLeaveClicked()
        {
            _ = ObserveAsync(_experience.LeaveVisitAsync);
        }

        /// <summary>捕获异常并服从 current Scene generation cancellation。</summary>
        /// <param name="action">HUD 窄语义动作。</param>
        /// <returns>动作完成或 scene token 取消时结束。</returns>
        private async Task ObserveAsync(
            Func<CancellationToken, Task<ClientPersonalWorldActionResult>> action)
        {
            var cancellation = _bindingCancellation;
            if (cancellation == null || cancellation.IsCancellationRequested)
            {
                return;
            }

            try
            {
                await action(cancellation.Token);
            }
            catch (OperationCanceledException)
            {
                // Scene generation 已失效，取消只结束 HUD 等待。
            }
            catch (Exception exception)
            {
                // Experience 已把远端失败降敏；到达此处的是接线或编程错误，必须可观测。
                Debug.LogException(exception, this);
            }
        }

        /// <summary>只在 route 与 Scene generation 仍 current 时呈现 HUD。</summary>
        /// <param name="state">Experience 发布的不可变 View State。</param>
        private void OnViewStateChanged(ClientPersonalWorldViewState state)
        {
            if (_binding == null || _binding.CancellationToken.IsCancellationRequested ||
                state.SceneGeneration != _binding.SceneGeneration)
            {
                return;
            }

            Render(state);
        }

        /// <summary>呈现 current role、world 与 visit 摘要，并按角色控制按钮。</summary>
        /// <param name="state">完整不可变 View State。</param>
        private void Render(ClientPersonalWorldViewState state)
        {
            var hud = state.WorldHud;
            _roleText.text = hud.IsOwner ? "Owner" : hud.IsVisitor ? "Visitor" : "None";
            _worldText.text = string.IsNullOrEmpty(hud.WorldInstanceID)
                ? hud.PersonalWorldID
                : $"{hud.PersonalWorldID} / {hud.WorldInstanceID}";
            _visitText.text = hud.VisitSessionID;
            var busy = state.ActiveIntent != ClientPersonalWorldIntent.None;
            _worldVisitButton.interactable = !busy;
            _leaveButton.gameObject.SetActive(hud.IsVisitor);
            _leaveButton.interactable = hud.IsVisitor && !busy;
        }
    }
}
