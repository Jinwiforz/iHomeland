using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Presentation.Navigation;
using UnityEngine;
using UnityEngine.UIElements;

namespace IHomeland.Client.Presentation.PersonalWorld
{
    /// <summary>
    /// 把一个直接引用的产品 PanelRenderer 绑定到个人世界 Experience。
    /// </summary>
    /// <remarks>
    /// 每个实例只承载一个 Login、Shell、WorldVisit 或 ConnectionLost route。控件通过稳定 UXML
    /// name 取得；页面只接收不可变 View State 与语义动作，不访问 transport 或 Composition。
    /// </remarks>
    [DisallowMultipleComponent]
    public sealed class ClientPersonalWorldUiToolkitView : ClientUiProductBindingBehaviour, IClientUiProductBinding
    {
        /// <summary>保存该页面唯一承载的产品 route。</summary>
        [SerializeField]
        [Tooltip("只允许 Login、Shell、WorldVisit 或 ConnectionLost。")]
        private ClientUiRouteId _routeId;

        /// <summary>保存与同 route Host 共用的 PanelRenderer 直接引用。</summary>
        [SerializeField]
        [Tooltip("包含该产品页面全部稳定 name 控件的 PanelRenderer。")]
        private PanelRenderer _panelRenderer;

        /// <summary>缓存 PanelRenderer 当前 generation 创建的根元素。</summary>
        private VisualElement _rootVisualElement;

        /// <summary>表示已经向 PanelRenderer 注册且尚未解除 UI reload callback。</summary>
        private bool _reloadCallbackRegistered;

        /// <summary>保存由 Composition 显式注入的唯一产品 Experience。</summary>
        private ClientPersonalWorldExperience _experience;

        /// <summary>保存当前 route binding。</summary>
        private ClientUiRouteBinding _binding;

        /// <summary>取消当前页面全部异步 command 等待。</summary>
        private CancellationTokenSource _bindingCancellation;

        /// <summary>保存已登记按钮与 callback，unbind 时必须对称解除。</summary>
        private readonly List<ButtonHook> _buttonHooks = new List<ButtonHook>();

        /// <summary>保存 Login register mode 的对称解绑 callback。</summary>
        private EventCallback<ChangeEvent<bool>> _registerModeChanged;

        /// <summary>保存 WorldVisit 目标玩家输入变化的对称解绑 callback。</summary>
        private EventCallback<ChangeEvent<string>> _targetPlayerChanged;

        /// <summary>Login 页面 adapter。</summary>
        private readonly ClientLoginUiToolkitPageAdapter _loginPage =
            new ClientLoginUiToolkitPageAdapter();

        /// <summary>Shell 页面 adapter。</summary>
        private readonly ClientShellUiToolkitPageAdapter _shellPage =
            new ClientShellUiToolkitPageAdapter();

        /// <summary>WorldVisit 页面 adapter 与局部 selection owner。</summary>
        private readonly ClientWorldVisitUiToolkitPageAdapter _worldVisitPage =
            new ClientWorldVisitUiToolkitPageAdapter();

        /// <summary>ConnectionLost 页面 adapter。</summary>
        private readonly ClientConnectionLostUiToolkitPageAdapter
            _connectionLostPage =
                new ClientConnectionLostUiToolkitPageAdapter();

        /// <summary>为程序化 fixture 在激活前配置与 Inspector 等价的 route 与 PanelRenderer。</summary>
        /// <param name="routeId">Login、Shell、WorldVisit 或 ConnectionLost。</param>
        /// <param name="panelRenderer">包含对应稳定 name 控件的 PanelRenderer。</param>
        /// <exception cref="ArgumentNullException">PanelRenderer 为空时抛出。</exception>
        /// <exception cref="InvalidOperationException">组件已绑定或 GameObject 已激活时抛出。</exception>
        internal void ConfigureBeforeActivation(ClientUiRouteId routeId, PanelRenderer panelRenderer)
        {
            if (_binding != null || isActiveAndEnabled)
            {
                throw new InvalidOperationException("产品 UI Toolkit View 只能在激活和绑定前配置。");
            }

            _routeId = routeId;
            _panelRenderer = panelRenderer ?? throw new ArgumentNullException(nameof(panelRenderer));
        }

        /// <summary>在 AppLifetime 启动前接收唯一产品上下文。</summary>
        /// <param name="context">必须是当前 Composition 创建的个人世界 Experience。</param>
        void IClientUiProductBinding.Configure(IClientUiProductContext context)
        {
            if (_experience != null || _binding != null)
            {
                throw new InvalidOperationException("产品 UI Toolkit View 只能在 bind 前配置一次。");
            }

            _experience = context as ClientPersonalWorldExperience ??
                throw new ArgumentException("产品 UI Toolkit View 只接受 ClientPersonalWorldExperience。", nameof(context));
        }

        /// <summary>绑定 route token、页面控件与 View State subscriber。</summary>
        /// <param name="binding">Router 创建的当前 route binding。</param>
        /// <param name="cancellationToken">Candidate 或 App 停止取消信号。</param>
        /// <returns>页面可以呈现当前状态时完成。</returns>
        Task IClientUiProductBinding.BindAsync(
            ClientUiRouteBinding binding,
            CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            ValidateConfiguration();
            if (_experience == null || _binding != null || binding == null || binding.RouteId != _routeId)
            {
                throw new InvalidOperationException("产品 UI Toolkit View 缺少 Experience 或 route binding 不匹配。");
            }

            _binding = binding;
            _bindingCancellation = CancellationTokenSource.CreateLinkedTokenSource(
                binding.CancellationToken,
                cancellationToken);
            try
            {
                AttachRootControls();
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

        /// <summary>解除 subscriber、按钮 callback 与 password 临时输入。</summary>
        /// <param name="cancellationToken">Route hide 或 App 停止取消信号。</param>
        /// <returns>页面不再允许提交 command 时完成。</returns>
        Task IClientUiProductBinding.UnbindAsync(CancellationToken cancellationToken)
        {
            if (_binding == null)
            {
                return Task.CompletedTask;
            }

            ClearBinding();
            return Task.CompletedTask;
        }

        /// <summary>对称释放 subscriber、控件 callback、password 与 route cancellation。</summary>
        private void ClearBinding()
        {
            _experience.ViewStateChanged -= OnViewStateChanged;
            DetachRootControls(_rootVisualElement, clearPassword: true);
            _bindingCancellation?.Cancel();
            _bindingCancellation?.Dispose();
            _bindingCancellation = null;
            _binding = null;
        }

        /// <summary>把当前 generation 的根元素接入产品 callback。</summary>
        /// <remarks>
        /// PanelRenderer 可在 route binding 存活期间重建 VisualElement 树。产品 callback 必须跟随
        /// 新根元素迁移，但不能因此取消 route token 或正在等待的 gameplay command。
        /// </remarks>
        private void AttachRootControls()
        {
            RegisterControls();
            if (_routeId == ClientUiRouteId.WorldVisit)
            {
                var targetPlayer = Require<TextField>("target-player-input");
                _targetPlayerChanged = _ => Render(_experience.ViewState);
                targetPlayer.RegisterValueChangedCallback(_targetPlayerChanged);
                return;
            }

            if (_routeId != ClientUiRouteId.Login)
            {
                return;
            }

            var password = Require<TextField>("password-input");
            password.isPasswordField = true;
            var registerToggle = Require<Toggle>("register-toggle");
            _registerModeChanged = change => ApplyRegisterMode(change.newValue);
            registerToggle.RegisterValueChangedCallback(_registerModeChanged);
            ApplyRegisterMode(registerToggle.value);
        }

        /// <summary>从指定旧根元素对称解除产品 callback。</summary>
        /// <param name="rootVisualElement">登记 callback 时所属的根元素。</param>
        /// <param name="clearPassword">是否清除 Login password 临时输入。</param>
        private void DetachRootControls(VisualElement rootVisualElement, bool clearPassword)
        {
            foreach (var hook in _buttonHooks)
            {
                hook.Button.clicked -= hook.Callback;
            }

            _buttonHooks.Clear();
            if (_routeId == ClientUiRouteId.Login && _registerModeChanged != null)
            {
                var registerToggle = rootVisualElement?.Q<Toggle>("register-toggle");
                registerToggle?.UnregisterValueChangedCallback(_registerModeChanged);
                _registerModeChanged = null;
            }

            else if (_routeId == ClientUiRouteId.WorldVisit && _targetPlayerChanged != null)
            {
                var targetPlayer = rootVisualElement?.Q<TextField>("target-player-input");
                targetPlayer?.UnregisterValueChangedCallback(_targetPlayerChanged);
                _targetPlayerChanged = null;
            }

            if (clearPassword)
            {
                ClearPassword(rootVisualElement);
            }
        }

        /// <summary>检查 route、PanelRenderer 与对应页面必需控件。</summary>
        /// <exception cref="InvalidOperationException">引用、route 或必需控件缺失时抛出。</exception>
        internal void ValidateConfiguration()
        {
            EnsureReloadCallbackRegistered();
            if (_panelRenderer == null || _rootVisualElement == null)
            {
                throw new InvalidOperationException("产品 UI Toolkit View 缺少 PanelRenderer 或根元素。");
            }

            switch (_routeId)
            {
                case ClientUiRouteId.Login:
                    _loginPage.Validate(_rootVisualElement);
                    break;
                case ClientUiRouteId.Shell:
                    _shellPage.Validate(_rootVisualElement);
                    break;
                case ClientUiRouteId.WorldVisit:
                    _worldVisitPage.Validate(_rootVisualElement);
                    break;
                case ClientUiRouteId.ConnectionLost:
                    _connectionLostPage.Validate(_rootVisualElement);
                    break;
                default:
                    throw new InvalidOperationException("产品 UI Toolkit View route 未登记或应由 uGUI 承载。");
            }
        }

        /// <summary>根据 route 登记唯一一组控件 callback。</summary>
        private void RegisterControls()
        {
            switch (_routeId)
            {
                case ClientUiRouteId.Login:
                    Hook("submit-button", SubmitAuthenticationAsync);
                    break;
                case ClientUiRouteId.Shell:
                    Hook("retry-button", RetryShellAsync);
                    Hook("logout-button", _experience.LogoutAsync);
                    break;
                case ClientUiRouteId.WorldVisit:
                    Hook("open-visit-button", _experience.OpenVisitAsync);
                    Hook("create-invite-button", CreateInviteAsync);
                    Hook("revoke-invite-button", RevokeInviteAsync);
                    Hook("accept-invite-button", AcceptInviteAsync);
                    Hook("kick-visitor-button", KickVisitorAsync);
                    Hook("close-visit-button", _experience.CloseVisitAsync);
                    Hook("leave-visit-button", _experience.LeaveVisitAsync);
                    break;
                case ClientUiRouteId.ConnectionLost:
                    Hook("retry-button", _experience.RetryConnectionAsync);
                    Hook("logout-button", _experience.LogoutAsync);
                    break;
            }
        }

        /// <summary>登记一个按钮到受 route cancellation 约束的语义动作。</summary>
        /// <param name="buttonName">稳定 UXML button name。</param>
        /// <param name="action">只接收页面 token 的窄语义动作。</param>
        private void Hook(
            string buttonName,
            Func<CancellationToken, Task<ClientPersonalWorldActionResult>> action)
        {
            var button = Require<Button>(buttonName);
            Action callback = () => _ = ObserveActionAsync(action);
            button.clicked += callback;
            _buttonHooks.Add(new ButtonHook(button, callback));
        }

        /// <summary>观察按钮动作并确保异常不从 Unity event callback 逃逸。</summary>
        /// <param name="action">当前按钮语义动作。</param>
        /// <returns>动作完成或页面取消时结束。</returns>
        private async Task ObserveActionAsync(
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
                // Route 已隐藏；取消只结束当前页面等待，不生成第二次 command。
            }
            catch (Exception exception)
            {
                // Experience 已把远端失败降敏；到达此处的是接线或编程错误，必须可观测。
                Debug.LogException(exception, this);
            }
        }

        /// <summary>读取 Login 临时输入并在调用完成后清除 password。</summary>
        /// <param name="cancellationToken">当前 route cancellation。</param>
        /// <returns>认证语义动作结果。</returns>
        private async Task<ClientPersonalWorldActionResult> SubmitAuthenticationAsync(
            CancellationToken cancellationToken)
        {
            var username = Require<TextField>("username-input").value;
            var passwordField = Require<TextField>("password-input");
            var password = passwordField.value;
            var register = Require<Toggle>("register-toggle").value;
            try
            {
                var displayName = Require<TextField>("display-name-input").value;
                if (string.IsNullOrWhiteSpace(username) || string.IsNullOrEmpty(password) ||
                    (register && string.IsNullOrWhiteSpace(displayName)))
                {
                    Require<Label>("error-label").text =
                        FailureText(ClientPersonalWorldFailure.Validation);
                    return ClientPersonalWorldActionResult.Failed(
                        ClientPersonalWorldFailure.Validation);
                }

                return register
                    ? await _experience.RegisterAsync(
                        username,
                        password,
                        displayName,
                        cancellationToken)
                    : await _experience.LoginAsync(username, password, cancellationToken);
            }
            finally
            {
                password = string.Empty;
                passwordField.value = string.Empty;
            }
        }

        /// <summary>切换 register-only 显示名输入，并在返回 login mode 时清除临时文本。</summary>
        /// <param name="registerMode">是否处于创建账号模式。</param>
        private void ApplyRegisterMode(bool registerMode)
        {
            var displayName = Require<TextField>("display-name-input");
            displayName.style.display = registerMode ? DisplayStyle.Flex : DisplayStyle.None;
            if (!registerMode)
            {
                displayName.value = string.Empty;
            }
        }

        /// <summary>根据当前阶段选择进入 own-world 或安全返回重试。</summary>
        /// <param name="cancellationToken">当前 route cancellation。</param>
        /// <returns>重试语义动作结果。</returns>
        private Task<ClientPersonalWorldActionResult> RetryShellAsync(CancellationToken cancellationToken)
        {
            return _experience.ViewState.Phase == ClientPersonalWorldPhase.ReturningOwnWorld
                ? _experience.RetryReturnAsync(cancellationToken)
                : _experience.RetryEnterOwnWorldAsync(cancellationToken);
        }

        /// <summary>从目标玩家输入创建定向邀请。</summary>
        /// <param name="cancellationToken">当前 route cancellation。</param>
        /// <returns>创建邀请结果。</returns>
        private async Task<ClientPersonalWorldActionResult> CreateInviteAsync(CancellationToken cancellationToken)
        {
            var result = await _experience.CreateInviteAsync(
                NormalizeIdentityInput(Require<TextField>("target-player-input").value),
                cancellationToken);
            if (result.Succeeded && _binding != null && !_binding.CancellationToken.IsCancellationRequested)
            {
                Require<TextField>("target-player-input").SetValueWithoutNotify(string.Empty);
            }

            return result;
        }

        /// <summary>从邀请输入撤销定向邀请。</summary>
        /// <param name="cancellationToken">当前 route cancellation。</param>
        /// <returns>撤销邀请结果。</returns>
        private Task<ClientPersonalWorldActionResult> RevokeInviteAsync(CancellationToken cancellationToken)
        {
            return _experience.RevokeInviteAsync(
                _worldVisitPage.SelectedInviteID,
                cancellationToken);
        }

        /// <summary>从访问会话与邀请输入接受定向邀请。</summary>
        /// <param name="cancellationToken">当前 route cancellation。</param>
        /// <returns>进入 Visitor target 结果。</returns>
        private Task<ClientPersonalWorldActionResult> AcceptInviteAsync(CancellationToken cancellationToken)
        {
            return _experience.AcceptInviteAsync(
                _worldVisitPage.SelectedInviteVisitSessionID,
                _worldVisitPage.SelectedInviteID,
                cancellationToken);
        }

        /// <summary>从目标玩家输入移除 Visitor。</summary>
        /// <param name="cancellationToken">当前 route cancellation。</param>
        /// <returns>移除 Visitor 结果。</returns>
        private Task<ClientPersonalWorldActionResult> KickVisitorAsync(CancellationToken cancellationToken)
        {
            return _experience.KickVisitorAsync(
                _worldVisitPage.SelectedVisitorPlayerID,
                cancellationToken);
        }

        /// <summary>移除人工复制 identity 时可能携带的首尾空白，不改变 identity 内部内容。</summary>
        /// <param name="value">来自 UI Toolkit TextField 的临时文本。</param>
        /// <returns>可交给严格 identity validator 的规范输入。</returns>
        internal static string NormalizeIdentityInput(string value)
        {
            return value?.Trim() ?? string.Empty;
        }

        /// <summary>只在当前 route binding 仍有效时呈现新状态。</summary>
        /// <param name="state">Experience 原子发布的不可变状态。</param>
        private void OnViewStateChanged(ClientPersonalWorldViewState state)
        {
            if (_binding == null || _binding.CancellationToken.IsCancellationRequested)
            {
                return;
            }

            Render(state);
        }

        /// <summary>按当前 route 只读取对应页面切片。</summary>
        /// <param name="state">完整不可变页面状态。</param>
        private void Render(ClientPersonalWorldViewState state)
        {
            switch (_routeId)
            {
                case ClientUiRouteId.Login:
                    _loginPage.Render(_rootVisualElement, state, FailureText);
                    break;
                case ClientUiRouteId.Shell:
                    _shellPage.Render(_rootVisualElement, state, FailureText);
                    break;
                case ClientUiRouteId.ConnectionLost:
                    _connectionLostPage.Render(
                        _rootVisualElement,
                        state,
                        RecoveryTitle,
                        RecoveryDescription,
                        FailureText);
                    break;
                case ClientUiRouteId.WorldVisit:
                    _worldVisitPage.Render(
                        _rootVisualElement,
                        state,
                        () => Render(_experience.ViewState),
                        FailureText);
                    break;
            }
        }

        /// <summary>把恢复阶段映射为ConnectionLost modal标题。</summary>
        private static string RecoveryTitle(ClientPersonalWorldPhase phase)
        {
            switch (phase)
            {
                case ClientPersonalWorldPhase.RecoveringControl:
                    return "正在恢复控制连接";
                case ClientPersonalWorldPhase.RecoveringWorld:
                    return "正在恢复世界连接";
                case ClientPersonalWorldPhase.AwaitingScene:
                    return "正在同步世界";
                default:
                    return "连接已中断";
            }
        }

        /// <summary>把恢复阶段映射为不承诺未提交事实的低敏说明。</summary>
        private static string RecoveryDescription(ClientPersonalWorldPhase phase)
        {
            switch (phase)
            {
                case ClientPersonalWorldPhase.RecoveringControl:
                    return "正在重新核对服务器状态，依赖邀请列表的操作已暂停。";
                case ClientPersonalWorldPhase.RecoveringWorld:
                    return "旧世界连接已失效，正在向服务器恢复当前目标。";
                case ClientPersonalWorldPhase.AwaitingScene:
                    return "服务器状态已确认，正在加载最新世界。";
                default:
                    return "与服务器的连接已经断开，可以重试或退出登录。";
            }
        }


        /// <summary>生成包含接受邀请所需标识的稳定摘要。</summary>
        /// <param name="invite">由权威 inbox snapshot 派生的邀请状态。</param>
        /// <returns>同时包含访问会话、邀请和目标玩家标识的本地展示文本。</returns>
        /// <remarks>
        /// 接受邀请需要同时提交 VisitSessionID 与 InviteID，因此列表必须公开两者，不能只显示邀请标识并迫使
        /// 玩家从日志、数据库或调试工具补齐产品操作所需信息。
        /// </remarks>
        internal static string FormatInviteSummary(ClientVisitInviteViewState invite)
        {
            if (invite == null)
            {
                throw new ArgumentNullException(nameof(invite));
            }

            return $"会话 {invite.VisitSessionID} | 邀请 {invite.InviteID} | 目标 {invite.TargetVisitorID}";
        }

        /// <summary>按完整 identity 将旧 invite selection 收敛到当前 replacement。</summary>
        /// <param name="invites">当前有效 invite 集合。</param>
        /// <param name="selectedVisitSessionID">上一 replacement 选择的 VisitSessionID。</param>
        /// <param name="selectedInviteID">上一 replacement 选择的 InviteID。</param>
        /// <param name="visitSessionID">收敛后的 VisitSessionID；没有确定选择时为空。</param>
        /// <param name="inviteID">收敛后的 InviteID；没有确定选择时为空。</param>
        /// <returns>旧选择仍存在或当前恰有一个确定选项时返回 true。</returns>
        /// <remarks>
        /// 多项 replacement 不按列表顺序静默选择；旧 identity 消失后立即失效。单项可以确定性选中，
        /// 让常见流程不要求玩家手抄协议 correlation。
        /// </remarks>
        internal static bool ReconcileInviteSelection(
            IReadOnlyList<ClientVisitInviteViewState> invites,
            string selectedVisitSessionID,
            string selectedInviteID,
            out string visitSessionID,
            out string inviteID)
        {
            if (invites == null)
            {
                throw new ArgumentNullException(nameof(invites));
            }

            visitSessionID = string.Empty;
            inviteID = string.Empty;
            foreach (var invite in invites)
            {
                if (string.Equals(
                        invite.VisitSessionID,
                        selectedVisitSessionID,
                        StringComparison.Ordinal) &&
                    string.Equals(invite.InviteID, selectedInviteID, StringComparison.Ordinal))
                {
                    visitSessionID = invite.VisitSessionID;
                    inviteID = invite.InviteID;
                    return true;
                }
            }

            if (invites.Count != 1)
            {
                return false;
            }

            var only = invites[0];
            visitSessionID = only.VisitSessionID;
            inviteID = only.InviteID;
            return true;
        }

        /// <summary>按 current member replacement 收敛页面局部 Visitor selection。</summary>
        /// <param name="visitors">当前稳定 Visitor identity 集合。</param>
        /// <param name="selectedVisitorPlayerID">上一 replacement 的选择。</param>
        /// <returns>仍存在的旧选择、唯一选项或空字符串。</returns>
        internal static string ReconcileVisitorSelection(
            IReadOnlyList<string> visitors,
            string selectedVisitorPlayerID)
        {
            if (visitors == null)
            {
                throw new ArgumentNullException(nameof(visitors));
            }

            foreach (var visitor in visitors)
            {
                if (string.Equals(visitor, selectedVisitorPlayerID, StringComparison.Ordinal))
                {
                    return visitor;
                }
            }

            return visitors.Count == 1 ? visitors[0] : string.Empty;
        }


        /// <summary>用稳定顺序的纯文本 Label 替换列表内容。</summary>
        /// <param name="container">UXML 直接提供的列表容器。</param>
        /// <param name="items">不可变页面文本集合。</param>
        /// <remarks>
        /// response 与 PUSH 可能先后发布等价快照；内容未变化时保留现有节点，避免 UI Toolkit
        /// 因反复清空并重建可视树而产生闪烁、焦点扰动和无意义分配。
        /// </remarks>
        internal static void ReplaceLabels(VisualElement container, IReadOnlyList<string> items)
        {
            if (container == null)
            {
                throw new ArgumentNullException(nameof(container));
            }

            if (items == null)
            {
                throw new ArgumentNullException(nameof(items));
            }

            if (container.childCount == items.Count)
            {
                var index = 0;
                var unchanged = true;
                foreach (var child in container.Children())
                {
                    if (!(child is Label label) ||
                        !string.Equals(label.text, items[index], StringComparison.Ordinal))
                    {
                        unchanged = false;
                        break;
                    }

                    index++;
                }

                if (unchanged)
                {
                    return;
                }
            }

            container.Clear();
            foreach (var item in items)
            {
                container.Add(new Label(item));
            }
        }

        /// <summary>把封闭 failure 映射为玩家可理解的稳定中文低敏文案，不显示异常、远端文本或内部 key。</summary>
        /// <param name="failure">低敏失败类别。</param>
        /// <returns>空文本或稳定中文低敏文案。</returns>
        internal static string FailureText(ClientPersonalWorldFailure failure)
        {
            switch (failure)
            {
                case ClientPersonalWorldFailure.None:
                    return string.Empty;
                case ClientPersonalWorldFailure.Validation:
                    return "输入内容无效，请检查后重试。";
                case ClientPersonalWorldFailure.Permission:
                    return "当前状态不允许执行此操作。";
                case ClientPersonalWorldFailure.ProtocolIncompatible:
                    return "客户端版本与服务器不兼容，请更新客户端。";
                case ClientPersonalWorldFailure.Unauthenticated:
                    return "登录状态已失效，请重新登录。";
                case ClientPersonalWorldFailure.RevisionConflict:
                    return "状态已经更新，请确认最新内容后重试。";
                case ClientPersonalWorldFailure.NotFound:
                    return "目标已不存在或已经失效。";
                case ClientPersonalWorldFailure.RateLimited:
                    return "操作过于频繁，请稍后重试。";
                case ClientPersonalWorldFailure.DependencyUnavailable:
                    return "服务暂时不可用，请稍后重试。";
                case ClientPersonalWorldFailure.Transport:
                    return "网络连接失败，请检查服务器或网络后重试。";
                case ClientPersonalWorldFailure.CommitUnknown:
                    return "操作结果尚未确认，请刷新状态后再操作。";
                case ClientPersonalWorldFailure.CallerCancelled:
                    return "操作已取消。";
                case ClientPersonalWorldFailure.Internal:
                    return "出现内部错误，请重试。";
                case ClientPersonalWorldFailure.Stopped:
                    return "客户端已经停止。";
                case ClientPersonalWorldFailure.InviteUnavailable:
                    return "邀请已撤销或失效，请选择最新邀请。";
                case ClientPersonalWorldFailure.SecureStorage:
                    return "无法安全保存登录状态，请检查系统权限后重试。";
                case ClientPersonalWorldFailure.ProfileInUse:
                    return "已有另一个客户端正在使用本机登录状态，请先关闭它再重新启动。";
                default:
                    return "出现未分类错误，请重试。";
            }
        }

        /// <summary>取得当前 PanelRenderer 中必需的稳定 name 控件。</summary>
        /// <typeparam name="T">期望 UI Toolkit 元素类型。</typeparam>
        /// <param name="name">稳定 UXML name。</param>
        /// <returns>匹配控件。</returns>
        /// <exception cref="InvalidOperationException">控件缺失或类型错误时抛出。</exception>
        private T Require<T>(string name)
            where T : VisualElement
        {
            var value = _rootVisualElement?.Q<T>(name);
            return value ?? throw new InvalidOperationException($"产品页面缺少 {typeof(T).Name}#{name}。");
        }

        /// <summary>清除 Login password 控件的临时输入。</summary>
        private void ClearPassword(VisualElement rootVisualElement)
        {
            if (_routeId != ClientUiRouteId.Login || rootVisualElement == null)
            {
                return;
            }

            var password = rootVisualElement.Q<TextField>("password-input");
            if (password != null)
            {
                password.value = string.Empty;
            }
        }

        /// <summary>组件初始化时优先订阅 PanelRenderer 初始加载与后续 UI reload。</summary>
        private void Awake()
        {
            EnsureReloadCallbackRegistered();
        }

        /// <summary>组件销毁时解除 callback，拒绝继续查询已经失效的元素。</summary>
        private void OnDestroy()
        {
            if (_panelRenderer != null && _reloadCallbackRegistered)
            {
                _panelRenderer.UnregisterUIReloadCallback(OnUiReloaded);
            }

            _reloadCallbackRegistered = false;
            _rootVisualElement = null;
        }

        /// <summary>幂等注册 PanelRenderer UI reload callback。</summary>
        private void EnsureReloadCallbackRegistered()
        {
            if (_panelRenderer == null || _reloadCallbackRegistered)
            {
                return;
            }

            _panelRenderer.RegisterUIReloadCallback(OnUiReloaded);
            _reloadCallbackRegistered = true;
        }

        /// <summary>接收 PanelRenderer 初始加载或资源 reload 后的新根元素。</summary>
        /// <param name="panelRenderer">触发 reload 的直接引用组件。</param>
        /// <param name="rootVisualElement">该 renderer 当前 generation 的根元素。</param>
        /// <param name="version">PanelRenderer 单调递增的 UI reload 版本。</param>
        private void OnUiReloaded(
            PanelRenderer panelRenderer,
            VisualElement rootVisualElement,
            int version)
        {
            if (panelRenderer != _panelRenderer || ReferenceEquals(_rootVisualElement, rootVisualElement))
            {
                return;
            }

            var previousRoot = _rootVisualElement;
            if (_binding != null)
            {
                DetachRootControls(previousRoot, clearPassword: false);
            }

            _rootVisualElement = rootVisualElement;
            if (_binding == null || rootVisualElement == null)
            {
                return;
            }

            AttachRootControls();
            Render(_experience.ViewState);
        }

        /// <summary>保存一个需要对称解除的 UI Toolkit Button callback。</summary>
        private sealed class ButtonHook
        {
            /// <summary>创建不可变 callback 登记。</summary>
            /// <param name="button">已登记 Button。</param>
            /// <param name="callback">已加入 clicked 的 callback。</param>
            internal ButtonHook(Button button, Action callback)
            {
                Button = button ?? throw new ArgumentNullException(nameof(button));
                Callback = callback ?? throw new ArgumentNullException(nameof(callback));
            }

            /// <summary>获取已登记 Button。</summary>
            internal Button Button { get; }

            /// <summary>获取已加入 clicked 的 callback。</summary>
            internal Action Callback { get; }
        }
    }
}
