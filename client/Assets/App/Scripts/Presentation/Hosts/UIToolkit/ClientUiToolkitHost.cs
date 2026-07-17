using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Presentation.Navigation;
using UnityEngine;
using UnityEngine.UIElements;

namespace IHomeland.Client.Presentation.Hosts.UIToolkit
{
    /// <summary>
    /// 把一个直接序列化的 UIDocument 适配为单 route UI Toolkit Host。
    /// </summary>
    /// <remarks>该 Host 不加载资源、不查找其他对象，也不持有业务或网络状态。</remarks>
    [DisallowMultipleComponent]
    public sealed class ClientUiToolkitHost : MonoBehaviour, IClientUiViewHost
    {
        /// <summary>保存该 Host 唯一承载的封闭 route identity。</summary>
        [SerializeField]
        private ClientUiRouteId _routeId;

        /// <summary>保存该 Host 直接拥有的 UIDocument。</summary>
        [SerializeField]
        private UIDocument _document;

        /// <summary>保存可选默认 focus 元素的稳定 UXML name。</summary>
        [SerializeField]
        private string _defaultFocusElementName = string.Empty;

        /// <summary>保存当前 route binding；解除绑定后必须为空。</summary>
        private ClientUiRouteBinding _binding;

        /// <summary>保存当前初始化所在 Unity 主线程。</summary>
        private int _mainThreadId;

        /// <summary>表示该 Host 当前 generation 已初始化。</summary>
        private bool _initialized;

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
        /// <param name="document">当前 GameObject 拥有的 UIDocument。</param>
        /// <param name="defaultFocusElementName">可为空的默认 focus UXML name。</param>
        internal void ConfigureBeforeActivation(
            ClientUiRouteId routeId,
            UIDocument document,
            string defaultFocusElementName)
        {
            if (_initialized || isActiveAndEnabled)
            {
                throw new InvalidOperationException("ClientUiToolkitHost 只能在激活和初始化前配置。");
            }

            _routeId = routeId;
            _document = document;
            _defaultFocusElementName = defaultFocusElementName ?? string.Empty;
        }

        /// <summary>验证引用并建立新 Host generation，默认保持隐藏和不可交互。</summary>
        /// <param name="cancellationToken">candidate 或 App 停止取消。</param>
        /// <returns>Host 可 bind 时完成的任务。</returns>
        Task IClientUiViewHost.InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (_initialized)
            {
                throw new InvalidOperationException("ClientUiToolkitHost 不能重复初始化。");
            }

            if (_routeId == ClientUiRouteId.None || _document == null)
            {
                throw new InvalidOperationException("ClientUiToolkitHost route 或 UIDocument 配置非法。");
            }

            _mainThreadId = Environment.CurrentManagedThreadId;
            SetVisibleAndInteractive(visible: false, interactive: false);
            HostGeneration++;
            _initialized = true;
            return Task.CompletedTask;
        }

        /// <summary>绑定仅包含 generation 与取消边界的 route 上下文。</summary>
        /// <param name="binding">当前 route binding。</param>
        /// <param name="cancellationToken">bind 等待取消。</param>
        /// <returns>binding 已保存时完成的任务。</returns>
        Task IClientUiViewHost.BindAsync(
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
            return Task.CompletedTask;
        }

        /// <summary>按 registry layer slot 显示 UIDocument。</summary>
        /// <param name="layer">不可由 Host 提高的稳定 layer。</param>
        /// <param name="cancellationToken">show 等待取消。</param>
        /// <returns>UIDocument 已显示时完成的任务。</returns>
        Task IClientUiViewHost.ShowAsync(
            ClientUiLayer layer,
            CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            EnsureBound();
            _document.sortingOrder = (int)layer;
            SetVisibleAndInteractive(visible: true, interactive: false);
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

        /// <summary>停止 picking 并隐藏 UIDocument。</summary>
        /// <param name="cancellationToken">hide 等待取消。</param>
        /// <returns>view 已隐藏时完成的任务。</returns>
        Task IClientUiViewHost.HideAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            EnsureUsable();
            SetVisibleAndInteractive(visible: false, interactive: false);
            return Task.CompletedTask;
        }

        /// <summary>解除 route binding，不保留页面订阅或数据。</summary>
        /// <param name="cancellationToken">unbind 等待取消。</param>
        /// <returns>binding 已清除时完成的任务。</returns>
        Task IClientUiViewHost.UnbindAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            EnsureUsable();
            _binding = null;
            return Task.CompletedTask;
        }

        /// <summary>幂等隐藏并释放当前 Host generation。</summary>
        /// <param name="cancellationToken">共享清理 deadline。</param>
        /// <returns>当前 generation 已释放时完成的任务。</returns>
        Task IClientUiViewHost.DisposeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (!_initialized)
            {
                return Task.CompletedTask;
            }

            EnsureMainThread();
            SetVisibleAndInteractive(visible: false, interactive: false);
            _binding = null;
            _initialized = false;
            return Task.CompletedTask;
        }

        /// <summary>同时提交 UIDocument 可见性、picking 与 enabled 状态。</summary>
        /// <param name="visible">目标可见性。</param>
        /// <param name="interactive">目标交互状态。</param>
        private void SetVisibleAndInteractive(bool visible, bool interactive)
        {
            var root = RequireRoot();
            root.style.display = visible ? DisplayStyle.Flex : DisplayStyle.None;
            root.pickingMode = interactive ? PickingMode.Position : PickingMode.Ignore;
            root.SetEnabled(interactive);
        }

        /// <summary>取得有效 UIDocument rootVisualElement。</summary>
        /// <returns>当前 UIDocument 根元素。</returns>
        private VisualElement RequireRoot()
        {
            EnsureMainThread();
            var root = _document.rootVisualElement;
            if (root == null)
            {
                throw new InvalidOperationException("ClientUiToolkitHost UIDocument 尚未创建 rootVisualElement。");
            }

            return root;
        }

        /// <summary>验证元素仍属于当前 panel、可见、启用且允许 focus。</summary>
        /// <param name="root">当前 UIDocument 根元素。</param>
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

        /// <summary>拒绝后台线程操作 UIDocument 与 VisualElement。</summary>
        private void EnsureMainThread()
        {
            if (Environment.CurrentManagedThreadId != _mainThreadId)
            {
                throw new InvalidOperationException("ClientUiToolkitHost 只能在初始化它的 Unity 主线程使用。");
            }
        }
    }
}
