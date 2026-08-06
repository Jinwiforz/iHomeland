using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.AppShell.Presentation.Navigation;
using UnityEngine;
using UnityEngine.EventSystems;
using UnityEngine.UI;

using UnityEngine.Scripting.APIUpdating;

namespace IHomeland.Client.AppShell.Runtime.Presentation.Hosts.UGUI
{
    /// <summary>
    /// 把直接序列化的 Canvas/CanvasGroup/EventSystem 适配为单 route uGUI Host。
    /// </summary>
    /// <remarks>该 Host 只协调表现生命周期，不创建 EventSystem、不加载 Prefab，也不保存业务事实。</remarks>
    [DisallowMultipleComponent]
    [MovedFrom(true, "IHomeland.Client.Presentation.Hosts.UGUI", "IHomeland.Client.Runtime", null)]
    public sealed class ClientUguiHost : MonoBehaviour, IClientUiViewHost
    {
        /// <summary>保存该 Host 唯一承载的封闭 route identity。</summary>
        [SerializeField]
        private ClientUiRouteId _routeId;

        /// <summary>保存该 Host 直接拥有的 Canvas。</summary>
        [SerializeField]
        private Canvas _canvas;

        /// <summary>保存统一控制 alpha、raycast 与 interactable 的 CanvasGroup。</summary>
        [SerializeField]
        private CanvasGroup _canvasGroup;

        /// <summary>保存唯一共享 EventSystem 的直接引用。</summary>
        [SerializeField]
        private EventSystem _eventSystem;

        /// <summary>保存可选默认 focus Selectable。</summary>
        [SerializeField]
        private Selectable _defaultFocus;

        /// <summary>保存可选产品页面 binding 的直接序列化组件引用。</summary>
        [SerializeField]
        [Tooltip("同一 route 的产品页面 binding；通用 fixture 可以为空。")]
        private ClientUiProductBindingBehaviour _productBindingComponent;

        /// <summary>缓存已经验证的产品页面 binding。</summary>
        private IClientUiProductBinding _productBinding;

        /// <summary>保存当前 route binding。</summary>
        private ClientUiRouteBinding _binding;

        /// <summary>保存当前初始化所在 Unity 主线程。</summary>
        private int _mainThreadId;

        /// <summary>表示该 Host 当前 generation 已初始化。</summary>
        private bool _initialized;

        /// <summary>获取该 Host 唯一承载的 route identity。</summary>
        ClientUiRouteId IClientUiViewHost.RouteId => _routeId;

        /// <summary>获取该 Host 唯一承载的 route identity，供 Composition 校验。</summary>
        internal ClientUiRouteId RouteId => _routeId;

        /// <summary>获取固定 uGUI framework owner。</summary>
        ClientUiFrameworkOwner IClientUiViewHost.FrameworkOwner => ClientUiFrameworkOwner.Ugui;

        /// <summary>获取每次成功初始化递增的 Host generation。</summary>
        long IClientUiViewHost.HostGeneration => HostGeneration;

        /// <summary>获取每次成功初始化递增的 Host generation。</summary>
        internal long HostGeneration { get; private set; }

        /// <summary>
        /// 为程序化 PlayMode fixture 在激活前配置直接引用。
        /// </summary>
        /// <param name="routeId">唯一 route identity。</param>
        /// <param name="canvas">测试 Canvas。</param>
        /// <param name="canvasGroup">测试 CanvasGroup。</param>
        /// <param name="eventSystem">测试唯一 EventSystem。</param>
        /// <param name="defaultFocus">可为空的默认 Selectable。</param>
        internal void ConfigureBeforeActivation(
            ClientUiRouteId routeId,
            Canvas canvas,
            CanvasGroup canvasGroup,
            EventSystem eventSystem,
            Selectable defaultFocus)
        {
            if (_initialized || isActiveAndEnabled)
            {
                throw new InvalidOperationException("ClientUguiHost 只能在激活和初始化前配置。");
            }

            _routeId = routeId;
            _canvas = canvas;
            _canvasGroup = canvasGroup;
            _eventSystem = eventSystem;
            _defaultFocus = defaultFocus;
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
                throw new InvalidOperationException("ClientUguiHost 只能在激活和初始化前配置产品 binding。");
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
                throw new InvalidOperationException("uGUI 产品 binding 只能在 Host 初始化前配置。");
            }

            ResolveProductBinding();
            if (_productBinding == null)
            {
                throw new InvalidOperationException("Production uGUI Host 缺少产品 binding 直接引用。");
            }

            _productBinding.Configure(context ?? throw new ArgumentNullException(nameof(context)));
        }

        /// <summary>验证直接引用并建立新 Host generation，默认保持隐藏和不可交互。</summary>
        /// <param name="cancellationToken">candidate 或 App 停止取消。</param>
        /// <returns>Host 可 bind 时完成的任务。</returns>
        Task IClientUiViewHost.InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (_initialized)
            {
                throw new InvalidOperationException("ClientUguiHost 不能重复初始化。");
            }

            if (_routeId == ClientUiRouteId.None || _canvas == null || _canvasGroup == null || _eventSystem == null)
            {
                throw new InvalidOperationException("ClientUguiHost route、Canvas、CanvasGroup 或 EventSystem 配置非法。");
            }

            ResolveProductBinding();

            if (!_canvasGroup.transform.IsChildOf(_canvas.transform))
            {
                throw new InvalidOperationException("ClientUguiHost CanvasGroup 必须属于登记 Canvas。");
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
        async Task IClientUiViewHost.BindAsync(
            ClientUiRouteBinding binding,
            CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            EnsureUsable();
            if (binding == null || binding.RouteId != _routeId || _binding != null)
            {
                throw new InvalidOperationException("ClientUguiHost binding 与当前 route/generation 不匹配。");
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

        /// <summary>按 registry layer slot 显示 Canvas。</summary>
        /// <param name="layer">不可由 Host 提高的稳定 layer。</param>
        /// <param name="cancellationToken">show 等待取消。</param>
        /// <returns>Canvas 已显示时完成的任务。</returns>
        Task IClientUiViewHost.ShowAsync(
            ClientUiLayer layer,
            CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            EnsureBound();
            _canvas.overrideSorting = true;
            _canvas.sortingOrder = (int)layer;
            SetVisibleAndInteractive(visible: true, interactive: false);
            return Task.CompletedTask;
        }

        /// <summary>统一切换 CanvasGroup raycast 与 interactable 状态。</summary>
        /// <param name="interactive">是否允许 pointer/navigation。</param>
        /// <param name="cancellationToken">切换等待取消。</param>
        /// <returns>交互状态已生效时完成的任务。</returns>
        Task IClientUiViewHost.SetInteractiveAsync(
            bool interactive,
            CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            EnsureBound();
            _canvasGroup.blocksRaycasts = interactive;
            _canvasGroup.interactable = interactive;
            return Task.CompletedTask;
        }

        /// <summary>捕获当前 EventSystem selection，并绑定当前 route 与 Host generation。</summary>
        /// <returns>selection 不属于当前 Canvas 时 Value 为空的 token。</returns>
        ClientUiFocusToken IClientUiViewHost.CaptureFocus()
        {
            EnsureBound();
            var selected = _eventSystem.currentSelectedGameObject;
            if (selected != null && !selected.transform.IsChildOf(_canvas.transform))
            {
                selected = null;
            }

            return new ClientUiFocusToken(_routeId, HostGeneration, selected);
        }

        /// <summary>选择配置且可交互的默认 Selectable。</summary>
        /// <param name="cancellationToken">focus 等待取消。</param>
        /// <returns>默认 Selectable 有效并完成选择时返回 true。</returns>
        Task<bool> IClientUiViewHost.FocusDefaultAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            EnsureBound();
            if (!IsValidFocus(_defaultFocus == null ? null : _defaultFocus.gameObject) ||
                !_defaultFocus.IsInteractable())
            {
                return Task.FromResult(false);
            }

            _eventSystem.SetSelectedGameObject(_defaultFocus.gameObject);
            return Task.FromResult(true);
        }

        /// <summary>只恢复 identity、generation、active hierarchy 与 Canvas 归属仍匹配的 token。</summary>
        /// <param name="token">先前由该 Host 捕获的 token。</param>
        /// <param name="cancellationToken">focus 等待取消。</param>
        /// <returns>原 selection 仍有效并完成恢复时返回 true。</returns>
        Task<bool> IClientUiViewHost.RestoreFocusAsync(
            ClientUiFocusToken token,
            CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            EnsureBound();
            var target = token?.Value as GameObject;
            if (token == null || token.OwnerRouteId != _routeId ||
                token.HostGeneration != HostGeneration || !IsValidFocus(target))
            {
                return Task.FromResult(false);
            }

            _eventSystem.SetSelectedGameObject(target);
            return Task.FromResult(true);
        }

        /// <summary>停止 raycast 并隐藏 Canvas。</summary>
        /// <param name="cancellationToken">hide 等待取消。</param>
        /// <returns>view 已隐藏时完成的任务。</returns>
        Task IClientUiViewHost.HideAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            EnsureUsable();
            if (_eventSystem.currentSelectedGameObject != null &&
                _eventSystem.currentSelectedGameObject.transform.IsChildOf(_canvas.transform))
            {
                _eventSystem.SetSelectedGameObject(null);
            }

            SetVisibleAndInteractive(visible: false, interactive: false);
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
                throw new InvalidOperationException("uGUI 产品 binding 组件未实现 IClientUiProductBinding。");
            }
        }

        /// <summary>同时提交 Canvas 可见性、alpha、raycast 与 interactable 状态。</summary>
        /// <param name="visible">目标可见性。</param>
        /// <param name="interactive">目标交互状态。</param>
        private void SetVisibleAndInteractive(bool visible, bool interactive)
        {
            EnsureMainThread();
            _canvas.enabled = visible;
            _canvasGroup.alpha = visible ? 1f : 0f;
            _canvasGroup.blocksRaycasts = interactive;
            _canvasGroup.interactable = interactive;
        }

        /// <summary>验证目标 active 且属于当前 Canvas hierarchy。</summary>
        /// <param name="target">待验证 focus GameObject。</param>
        /// <returns>仍可安全交给 EventSystem 时返回 true。</returns>
        private bool IsValidFocus(GameObject target)
        {
            if (target == null ||
                !target.activeInHierarchy ||
                !target.transform.IsChildOf(_canvas.transform))
            {
                return false;
            }

            var selectable = target.GetComponent<Selectable>();
            return selectable != null && selectable.IsActive() && selectable.IsInteractable();
        }

        /// <summary>验证 Host generation 已初始化且调用位于 Unity 主线程。</summary>
        private void EnsureUsable()
        {
            if (!_initialized)
            {
                throw new InvalidOperationException("ClientUguiHost 尚未初始化。");
            }

            EnsureMainThread();
        }

        /// <summary>验证当前 Host 已绑定 route。</summary>
        private void EnsureBound()
        {
            EnsureUsable();
            if (_binding == null)
            {
                throw new InvalidOperationException("ClientUguiHost 尚未绑定 route。");
            }
        }

        /// <summary>拒绝后台线程操作 Canvas、EventSystem 与 GameObject。</summary>
        private void EnsureMainThread()
        {
            if (Environment.CurrentManagedThreadId != _mainThreadId)
            {
                throw new InvalidOperationException("ClientUguiHost 只能在初始化它的 Unity 主线程使用。");
            }
        }
    }
}
