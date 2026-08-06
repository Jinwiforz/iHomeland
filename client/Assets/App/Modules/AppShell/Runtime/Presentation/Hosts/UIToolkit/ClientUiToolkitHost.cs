using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.AppShell.Presentation.Navigation;
using UnityEngine;
using UnityEngine.UIElements;

using UnityEngine.Scripting.APIUpdating;

namespace IHomeland.Client.AppShell.Runtime.Presentation.Hosts.UIToolkit
{
    /// <summary>
    /// 把一个直接序列化的 PanelRenderer 适配为单 route UI Toolkit Host。
    /// </summary>
    /// <remarks>该 Host 不加载资源、不查找其他对象，也不持有业务或网络状态。</remarks>
    [DisallowMultipleComponent]
    [MovedFrom(true, "IHomeland.Client.Presentation.Hosts.UIToolkit", "IHomeland.Client.Runtime", null)]
    public sealed class ClientUiToolkitHost : MonoBehaviour, IClientUiViewHost
    {
        /// <summary>保存该 Host 唯一承载的封闭 route identity。</summary>
        [SerializeField]
        private ClientUiRouteId _routeId;

        /// <summary>保存该 Host 直接拥有的 PanelRenderer。</summary>
        [SerializeField]
        private PanelRenderer _panelRenderer;

        /// <summary>缓存 PanelRenderer 当前 generation 创建的根元素。</summary>
        private VisualElement _rootVisualElement;

        /// <summary>表示已经向 PanelRenderer 注册且尚未解除 UI reload callback。</summary>
        private bool _reloadCallbackRegistered;

        /// <summary>保存 reload 后需要恢复的目标可见性。</summary>
        private bool _visible;

        /// <summary>保存 reload 后需要恢复的目标交互状态。</summary>
        private bool _interactive;

        /// <summary>保存可选默认 focus 元素的稳定 UXML name。</summary>
        [SerializeField]
        private string _defaultFocusElementName = string.Empty;

        /// <summary>保存可选产品页面 binding 的直接序列化组件引用。</summary>
        [SerializeField]
        [Tooltip("同一 route 的产品页面 binding；通用 fixture 可以为空。")]
        private ClientUiProductBindingBehaviour _productBindingComponent;

        /// <summary>缓存已经验证的产品页面 binding。</summary>
        private IClientUiProductBinding _productBinding;

        /// <summary>保存当前 route binding；解除绑定后必须为空。</summary>
        private ClientUiRouteBinding _binding;

        /// <summary>保存当前初始化所在 Unity 主线程。</summary>
        private int _mainThreadId;

        /// <summary>表示该 Host 当前 generation 已初始化。</summary>
        private bool _initialized;

        /// <summary>防止等待 PanelRenderer 首次创建根元素期间并发启动第二次初始化。</summary>
        private bool _initializing;

        /// <summary>等待 PanelRenderer 首次发布非空根元素；完成、取消或销毁后必须清空。</summary>
        private TaskCompletionSource<VisualElement> _initialRootCompletion;

        /// <summary>获取该 Host 唯一承载的 route identity。</summary>
        ClientUiRouteId IClientUiViewHost.RouteId => _routeId;

        /// <summary>获取该 Host 唯一承载的 route identity，供 Composition 校验。</summary>
        internal ClientUiRouteId RouteId => _routeId;

        /// <summary>获取固定 UI Toolkit framework owner。</summary>
        ClientUiFrameworkOwner IClientUiViewHost.FrameworkOwner => ClientUiFrameworkOwner.UiToolkit;

        /// <summary>获取每次成功初始化递增的 Host generation。</summary>
        long IClientUiViewHost.HostGeneration => HostGeneration;

        /// <summary>获取每次成功初始化递增的 Host generation。</summary>
        internal long HostGeneration { get; private set; }

        /// <summary>
        /// 为程序化 PlayMode fixture 在激活前配置直接引用。
        /// </summary>
        /// <param name="routeId">唯一 route identity。</param>
        /// <param name="panelRenderer">当前 GameObject 拥有的 PanelRenderer。</param>
        /// <param name="defaultFocusElementName">可为空的默认 focus UXML name。</param>
        internal void ConfigureBeforeActivation(
            ClientUiRouteId routeId,
            PanelRenderer panelRenderer,
            string defaultFocusElementName)
        {
            if (_initialized || isActiveAndEnabled)
            {
                throw new InvalidOperationException("ClientUiToolkitHost 只能在激活和初始化前配置。");
            }

            _routeId = routeId;
            _panelRenderer = panelRenderer;
            _defaultFocusElementName = defaultFocusElementName ?? string.Empty;
        }

        /// <summary>为程序化 fixture 在激活前配置与 Inspector 等价的产品 binding 引用。</summary>
        /// <param name="productBindingComponent">实现产品 binding 接口的同 route 组件。</param>
        /// <exception cref="ArgumentNullException">组件为空时抛出。</exception>
        /// <exception cref="InvalidOperationException">Host 已初始化或 GameObject 已激活时抛出。</exception>
        internal void ConfigureProductBindingBeforeActivation(
            ClientUiProductBindingBehaviour productBindingComponent)
        {
            if (_initialized || isActiveAndEnabled)
            {
                throw new InvalidOperationException("ClientUiToolkitHost 只能在激活和初始化前配置产品 binding。");
            }

            _productBindingComponent = productBindingComponent ??
                throw new ArgumentNullException(nameof(productBindingComponent));
            ResolveProductBinding();
        }

        /// <summary>在 AppLifetime 启动前向可选产品页面显式注入上下文。</summary>
        /// <param name="context">Composition 创建的窄产品上下文。</param>
        internal void ConfigureProductContext(IClientUiProductContext context)
        {
            if (_initialized)
            {
                throw new InvalidOperationException("UI Toolkit 产品 binding 只能在 Host 初始化前配置。");
            }

            ResolveProductBinding();
            if (_productBinding == null)
            {
                throw new InvalidOperationException("Production UI Toolkit Host 缺少产品 binding 直接引用。");
            }

            _productBinding.Configure(context ?? throw new ArgumentNullException(nameof(context)));
        }

        /// <summary>验证引用并建立新 Host generation，默认保持隐藏和不可交互。</summary>
        /// <param name="cancellationToken">candidate 或 App 停止取消。</param>
        /// <returns>Host 可 bind 时完成的任务。</returns>
        async Task IClientUiViewHost.InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (_initialized || _initializing)
            {
                throw new InvalidOperationException("ClientUiToolkitHost 不能重复或并发初始化。");
            }

            if (_routeId == ClientUiRouteId.None ||
                _panelRenderer == null ||
                _panelRenderer.panelSettings == null ||
                _panelRenderer.visualTreeAsset == null)
            {
                throw new InvalidOperationException(
                    "ClientUiToolkitHost route、PanelRenderer、Panel Settings 或 Source Asset 配置非法。");
            }

            _initializing = true;
            try
            {
                EnsureReloadCallbackRegistered();
                ResolveProductBinding();

                _mainThreadId = Environment.CurrentManagedThreadId;
                await WaitForInitialRootAsync(cancellationToken);
                SetVisibleAndInteractive(visible: false, interactive: false);
                HostGeneration++;
                _initialized = true;
            }
            finally
            {
                _initializing = false;
            }
        }

        /// <summary>绑定仅包含 generation 与取消边界的 route 上下文。</summary>
        /// <param name="binding">当前 route binding。</param>
        /// <param name="cancellationToken">bind 等待取消。</param>
        /// <returns>binding 已保存时完成的任务。</returns>
        async Task IClientUiViewHost.BindAsync(
            ClientUiRouteBinding binding,
            CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            EnsureUsable();
            if (binding == null || binding.RouteId != _routeId || _binding != null)
            {
                throw new InvalidOperationException("ClientUiToolkitHost binding 与当前 route/generation 不匹配。");
            }

            _binding = binding;
            try
            {
                if (_productBinding != null)
                {
                    await _productBinding.BindAsync(binding, cancellationToken);
                }
            }
            catch
            {
                _binding = null;
                throw;
            }
        }

        /// <summary>按 registry layer slot 显示 PanelRenderer。</summary>
        /// <param name="layer">不可由 Host 提高的稳定 layer。</param>
        /// <param name="cancellationToken">show 等待取消。</param>
        /// <returns>PanelRenderer 已显示时完成的任务。</returns>
        Task IClientUiViewHost.ShowAsync(
            ClientUiLayer layer,
            CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            EnsureBound();
            _panelRenderer.sortingOrder = (int)layer;
            SetVisibleAndInteractive(visible: true, interactive: false);
            ClientUiDiagnostics.Trace(
                nameof(ClientUiToolkitHost),
                "host_shown",
                $"route={_routeId} host_generation={HostGeneration} layer={layer}");
            return Task.CompletedTask;
        }

        /// <summary>统一切换 UI Toolkit picking 与 enabled 状态。</summary>
        /// <param name="interactive">是否允许 pointer/navigation。</param>
        /// <param name="cancellationToken">切换等待取消。</param>
        /// <returns>交互状态已生效时完成的任务。</returns>
        Task IClientUiViewHost.SetInteractiveAsync(
            bool interactive,
            CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            EnsureBound();
            var root = RequireRoot();
            root.pickingMode = interactive ? PickingMode.Position : PickingMode.Ignore;
            root.SetEnabled(interactive);
            return Task.CompletedTask;
        }

        /// <summary>捕获当前 panel focus，并绑定当前 route 与 Host generation。</summary>
        /// <returns>没有有效 panel focus 时 Value 为空的 token。</returns>
        ClientUiFocusToken IClientUiViewHost.CaptureFocus()
        {
            EnsureBound();
            var root = RequireRoot();
            var focused = root.panel?.focusController?.focusedElement as VisualElement;
            return new ClientUiFocusToken(_routeId, HostGeneration, focused);
        }

        /// <summary>聚焦配置的默认 UXML 元素。</summary>
        /// <param name="cancellationToken">focus 等待取消。</param>
        /// <returns>目标存在、可聚焦且仍属于当前 panel 时返回 true。</returns>
        Task<bool> IClientUiViewHost.FocusDefaultAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            EnsureBound();
            if (string.IsNullOrWhiteSpace(_defaultFocusElementName))
            {
                return Task.FromResult(false);
            }

            var root = RequireRoot();
            var target = root.Q<VisualElement>(_defaultFocusElementName);
            if (!IsValidFocus(root, target))
            {
                return Task.FromResult(false);
            }

            target.Focus();
            return Task.FromResult(true);
        }

        /// <summary>只恢复 identity、generation、panel 与树归属仍匹配的 focus token。</summary>
        /// <param name="token">先前由该 Host 捕获的 token。</param>
        /// <param name="cancellationToken">focus 等待取消。</param>
        /// <returns>原元素仍有效并完成聚焦时返回 true。</returns>
        Task<bool> IClientUiViewHost.RestoreFocusAsync(
            ClientUiFocusToken token,
            CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            EnsureBound();
            var root = RequireRoot();
            var target = token?.Value as VisualElement;
            if (token == null || token.OwnerRouteId != _routeId ||
                token.HostGeneration != HostGeneration || target == null ||
                !IsValidFocus(root, target))
            {
                return Task.FromResult(false);
            }

            target.Focus();
            return Task.FromResult(true);
        }

        /// <summary>停止 picking 并隐藏 PanelRenderer。</summary>
        /// <param name="cancellationToken">hide 等待取消。</param>
        /// <returns>view 已隐藏时完成的任务。</returns>
        Task IClientUiViewHost.HideAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            EnsureUsable();
            SetVisibleAndInteractive(visible: false, interactive: false);
            ClientUiDiagnostics.Trace(
                nameof(ClientUiToolkitHost),
                "host_hidden",
                $"route={_routeId} host_generation={HostGeneration}");
            return Task.CompletedTask;
        }

        /// <summary>解除 route binding，不保留页面订阅或数据。</summary>
        /// <param name="cancellationToken">unbind 等待取消。</param>
        /// <returns>binding 已清除时完成的任务。</returns>
        async Task IClientUiViewHost.UnbindAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            EnsureUsable();
            try
            {
                if (_productBinding != null && _binding != null)
                {
                    await _productBinding.UnbindAsync(cancellationToken);
                }
            }
            finally
            {
                _binding = null;
            }
        }

        /// <summary>幂等隐藏并释放当前 Host generation。</summary>
        /// <param name="cancellationToken">共享清理 deadline。</param>
        /// <returns>当前 generation 已释放时完成的任务。</returns>
        async Task IClientUiViewHost.DisposeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (!_initialized)
            {
                return;
            }

            EnsureMainThread();
            if (_productBinding != null && _binding != null)
            {
                try
                {
                    await _productBinding.UnbindAsync(cancellationToken);
                }
                finally
                {
                    _binding = null;
                }
            }

            SetVisibleAndInteractive(visible: false, interactive: false);
            _binding = null;
            _initialized = false;
        }

        /// <summary>验证并缓存经过 Inspector 类型边界约束的可选产品 binding。</summary>
        private void ResolveProductBinding()
        {
            if (_productBindingComponent == null)
            {
                _productBinding = null;
                return;
            }

            _productBinding = _productBindingComponent as IClientUiProductBinding;
            if (_productBinding == null)
            {
                throw new InvalidOperationException("UI Toolkit 产品 binding 组件未实现 IClientUiProductBinding。");
            }
        }

        /// <summary>同时提交 PanelRenderer 可见性、picking 与 enabled 状态。</summary>
        /// <param name="visible">目标可见性。</param>
        /// <param name="interactive">目标交互状态。</param>
        private void SetVisibleAndInteractive(bool visible, bool interactive)
        {
            _visible = visible;
            _interactive = interactive;
            var root = RequireRoot();
            ApplyVisibleAndInteractive(root);
        }

        /// <summary>取得有效 PanelRenderer 根元素。</summary>
        /// <returns>当前 PanelRenderer generation 的根元素。</returns>
        private VisualElement RequireRoot()
        {
            EnsureMainThread();
            var root = _rootVisualElement;
            if (root == null)
            {
                throw new InvalidOperationException("ClientUiToolkitHost PanelRenderer 尚未创建根元素。");
            }

            return root;
        }

        /// <summary>
        /// 等待 PanelRenderer 发布首次非空根元素，消除 AppBootstrap.Awake 与 panel 创建的时序竞争。
        /// </summary>
        /// <param name="cancellationToken">App 停止或 route candidate 取消信号。</param>
        /// <returns>初始 UI reload callback 已提供根元素时完成。</returns>
        private async Task WaitForInitialRootAsync(CancellationToken cancellationToken)
        {
            EnsureMainThread();
            if (_rootVisualElement != null)
            {
                return;
            }

            var completion = new TaskCompletionSource<VisualElement>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            _initialRootCompletion = completion;
            using (cancellationToken.Register(() => completion.TrySetCanceled()))
            {
                try
                {
                    await completion.Task;
                }
                finally
                {
                    if (ReferenceEquals(_initialRootCompletion, completion))
                    {
                        _initialRootCompletion = null;
                    }
                }
            }

            cancellationToken.ThrowIfCancellationRequested();
            EnsureMainThread();
        }

        /// <summary>向当前根元素应用 Host 已提交的可见性与交互状态。</summary>
        /// <param name="root">PanelRenderer 当前 generation 的根元素。</param>
        private void ApplyVisibleAndInteractive(VisualElement root)
        {
            root.style.display = _visible ? DisplayStyle.Flex : DisplayStyle.None;
            root.pickingMode = _interactive ? PickingMode.Position : PickingMode.Ignore;
            root.SetEnabled(_interactive);
        }

        /// <summary>组件初始化时优先订阅 PanelRenderer 初始加载与后续 UI reload。</summary>
        private void Awake()
        {
            EnsureReloadCallbackRegistered();
        }

        /// <summary>组件销毁时解除 callback，拒绝继续使用已经失效的根元素。</summary>
        private void OnDestroy()
        {
            if (_panelRenderer != null && _reloadCallbackRegistered)
            {
                _panelRenderer.UnregisterUIReloadCallback(OnUiReloaded);
            }

            _reloadCallbackRegistered = false;
            _rootVisualElement = null;
            _initialRootCompletion?.TrySetException(
                new ObjectDisposedException(nameof(ClientUiToolkitHost)));
            _initialRootCompletion = null;
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
        /// <remarks>
        /// Router 按需初始化 Host，因此尚未打开的 route 也必须立即应用默认隐藏状态，
        /// 不能等待 InitializeAsync 才阻止 Source Asset 泄漏到画面。
        /// </remarks>
        /// <param name="panelRenderer">触发 reload 的直接引用组件。</param>
        /// <param name="rootVisualElement">该 renderer 当前 generation 的根元素。</param>
        /// <param name="version">PanelRenderer 单调递增的 UI reload 版本。</param>
        private void OnUiReloaded(
            PanelRenderer panelRenderer,
            VisualElement rootVisualElement,
            int version)
        {
            if (panelRenderer != _panelRenderer)
            {
                return;
            }

            _rootVisualElement = rootVisualElement;
            ClientUiDiagnostics.Trace(
                nameof(ClientUiToolkitHost),
                "panel_reloaded",
                $"route={_routeId} host_generation={HostGeneration} reload_version={version} " +
                $"has_root={rootVisualElement != null} visible={_visible} interactive={_interactive}");
            if (rootVisualElement != null)
            {
                _initialRootCompletion?.TrySetResult(rootVisualElement);
            }

            if (rootVisualElement != null)
            {
                ApplyVisibleAndInteractive(rootVisualElement);
            }
        }

        /// <summary>验证元素仍属于当前 panel、可见、启用且允许 focus。</summary>
        /// <param name="root">当前 PanelRenderer 根元素。</param>
        /// <param name="target">待验证元素。</param>
        /// <returns>元素仍可安全获得 focus 时返回 true。</returns>
        private static bool IsValidFocus(VisualElement root, VisualElement target)
        {
            return target != null &&
                target.focusable &&
                target.enabledInHierarchy &&
                target.visible &&
                target.resolvedStyle.display != DisplayStyle.None &&
                target.panel != null &&
                target.panel == root.panel &&
                root.Contains(target);
        }

        /// <summary>验证 Host generation 已初始化且调用位于 Unity 主线程。</summary>
        private void EnsureUsable()
        {
            if (!_initialized)
            {
                throw new InvalidOperationException("ClientUiToolkitHost 尚未初始化。");
            }

            EnsureMainThread();
        }

        /// <summary>验证当前 Host 已绑定 route。</summary>
        private void EnsureBound()
        {
            EnsureUsable();
            if (_binding == null)
            {
                throw new InvalidOperationException("ClientUiToolkitHost 尚未绑定 route。");
            }
        }

        /// <summary>拒绝后台线程操作 PanelRenderer 与 VisualElement。</summary>
        private void EnsureMainThread()
        {
            if (Environment.CurrentManagedThreadId != _mainThreadId)
            {
                throw new InvalidOperationException("ClientUiToolkitHost 只能在初始化它的 Unity 主线程使用。");
            }
        }
    }
}
