using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Presentation.Hosts.UGUI;
using IHomeland.Client.Presentation.Hosts.UIToolkit;
using IHomeland.Client.Presentation.Navigation;
using UnityEngine;
using UnityEngine.InputSystem;

namespace IHomeland.Client.Presentation.Hosts
{
    /// <summary>
    /// 作为持久 App Scope 内唯一 Input System owner，并显式汇总双 UI framework Host。
    /// </summary>
    /// <remarks>
    /// 该组件只拥有 action map、cursor 和 Host 引用，不查找场景对象，也不保存账号、连接或世界事实。
    /// production Host 列表可以为空；后续页面必须通过直接序列化引用显式加入。
    /// </remarks>
    [DisallowMultipleComponent]
    public sealed class ClientUiHostRoot : MonoBehaviour, IClientUiInputCoordinator
    {
        /// <summary>保存项目级 Input System 资产模板；运行时只操作其私有 clone。</summary>
        [SerializeField]
        [Tooltip("只读 Input System 模板；运行时 clone 后由该组件唯一控制 Player/UI action map。")]
        private InputActionAsset _inputActions;

        /// <summary>保存直接序列化的 UI Toolkit route Host。</summary>
        [SerializeField]
        [Tooltip("显式登记的 UI Toolkit route Host；当前没有产品页面时保持为空。")]
        private ClientUiToolkitHost[] _uiToolkitHosts = Array.Empty<ClientUiToolkitHost>();

        /// <summary>保存直接序列化的 uGUI route Host。</summary>
        [SerializeField]
        [Tooltip("显式登记的 uGUI route Host；当前没有产品页面时保持为空。")]
        private ClientUguiHost[] _uguiHosts = Array.Empty<ClientUguiHost>();

        /// <summary>保存运行时私有 action asset clone。</summary>
        private InputActionAsset _runtimeInputActions;

        /// <summary>保存私有 clone 中唯一 Player action map。</summary>
        private InputActionMap _playerActionMap;

        /// <summary>保存私有 clone 中唯一 UI action map。</summary>
        private InputActionMap _uiActionMap;

        /// <summary>保存初始化所在 Unity 主线程，阻止后台线程操作 Unity API。</summary>
        private int _mainThreadId;

        /// <summary>表示当前 Input owner 已完成初始化。</summary>
        private bool _initialized;

        /// <summary>表示当前 Input owner 已永久停止。</summary>
        private bool _stopped;

        /// <summary>获取最近一次原子提交的输入状态。</summary>
        ClientUiInputState IClientUiInputCoordinator.CurrentState => CurrentState;

        /// <summary>获取最近一次原子提交的输入状态，供同程序集测试观察。</summary>
        internal ClientUiInputState CurrentState { get; private set; } = ClientUiInputState.Gameplay;

        /// <summary>获取 runtime clone 的 Player action map 是否启用，供生命周期测试观察。</summary>
        internal bool IsPlayerActionMapEnabled => _playerActionMap != null && _playerActionMap.enabled;

        /// <summary>获取 runtime clone 的 UI action map 是否启用，供生命周期测试观察。</summary>
        internal bool IsUiActionMapEnabled => _uiActionMap != null && _uiActionMap.enabled;

        /// <summary>
        /// 在构造对象图前验证 Input 资产和所有显式 Host 引用，不产生 Unity 运行副作用。
        /// </summary>
        /// <exception cref="InvalidOperationException">缺少资产、数组、Host 或存在重复 route/Host 时抛出。</exception>
        internal void ValidateConfiguration()
        {
            if (_inputActions == null)
            {
                throw new InvalidOperationException("ClientUiHostRoot 缺少 Input System 资产直接引用。");
            }

            if (_uiToolkitHosts == null || _uguiHosts == null)
            {
                throw new InvalidOperationException("ClientUiHostRoot Host 列表不能为 null。");
            }

            var routes = new HashSet<ClientUiRouteId>();
            var instances = new HashSet<IClientUiViewHost>();
            ValidateHosts(_uiToolkitHosts, routes, instances, ClientUiFrameworkOwner.UiToolkit);
            ValidateHosts(_uguiHosts, routes, instances, ClientUiFrameworkOwner.Ugui);
        }

        /// <summary>
        /// 返回防御性复制的显式 Host 快照，供 Composition 构造不可变 registry。
        /// </summary>
        /// <returns>按 UI Toolkit、uGUI 序列化顺序排列的 Host 快照。</returns>
        internal IReadOnlyList<IClientUiViewHost> GetHosts()
        {
            ValidateConfiguration();
            var hosts = new IClientUiViewHost[_uiToolkitHosts.Length + _uguiHosts.Length];
            var index = 0;
            foreach (var host in _uiToolkitHosts)
            {
                hosts[index++] = host;
            }

            foreach (var host in _uguiHosts)
            {
                hosts[index++] = host;
            }

            return hosts;
        }

        /// <summary>
        /// 为程序化 PlayMode fixture 在激活前提供与 Inspector 等价的直接引用。
        /// </summary>
        /// <param name="inputActions">包含唯一 Player/UI action map 的测试资产模板。</param>
        /// <param name="uiToolkitHosts">测试用 UI Toolkit Host。</param>
        /// <param name="uguiHosts">测试用 uGUI Host。</param>
        /// <exception cref="InvalidOperationException">组件已初始化或 GameObject 已激活时抛出。</exception>
        internal void ConfigureBeforeActivation(
            InputActionAsset inputActions,
            ClientUiToolkitHost[] uiToolkitHosts,
            ClientUguiHost[] uguiHosts)
        {
            if (_initialized || isActiveAndEnabled)
            {
                throw new InvalidOperationException("ClientUiHostRoot 只能在激活和初始化前配置。");
            }

            _inputActions = inputActions;
            _uiToolkitHosts = uiToolkitHosts;
            _uguiHosts = uguiHosts;
            ValidateConfiguration();
        }

        /// <summary>
        /// clone Input System 资产并提交 gameplay baseline，原始资产保持只读。
        /// </summary>
        /// <param name="cancellationToken">AppLifetime 启动取消。</param>
        /// <returns>唯一 action map owner 就绪时完成的任务。</returns>
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (_stopped)
            {
                throw new InvalidOperationException("ClientUiHostRoot 已停止，不能重新初始化。");
            }

            if (_initialized)
            {
                throw new InvalidOperationException("ClientUiHostRoot 不能重复初始化。");
            }

            ValidateConfiguration();
            _mainThreadId = Environment.CurrentManagedThreadId;
            _runtimeInputActions = Instantiate(_inputActions);
            _runtimeInputActions.name = $"{_inputActions.name} (Runtime Clone)";
            _runtimeInputActions.hideFlags = HideFlags.DontSave;
            try
            {
                _playerActionMap = _runtimeInputActions.FindActionMap("Player", throwIfNotFound: true);
                _uiActionMap = _runtimeInputActions.FindActionMap("UI", throwIfNotFound: true);
                _runtimeInputActions.Disable();
                ApplyState(ClientUiInputState.Gameplay);
                _initialized = true;
                return Task.CompletedTask;
            }
            catch
            {
                CurrentState = ClientUiInputState.Gameplay;
                Cursor.visible = true;
                Cursor.lockState = CursorLockMode.None;
                _initialized = false;
                DestroyRuntimeInputClone();
                throw;
            }
        }

        /// <summary>
        /// 原子切换 Player/UI action map、cursor 可见性与锁定状态。
        /// </summary>
        /// <param name="state">已由 router snapshot 派生的目标输入状态。</param>
        /// <param name="cancellationToken">navigation 或停止取消。</param>
        /// <returns>全部输入状态一致时完成的任务。</returns>
        Task IClientUiInputCoordinator.ApplyAsync(
            ClientUiInputState state,
            CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (state == null)
            {
                throw new ArgumentNullException(nameof(state));
            }

            EnsureUsableOnMainThread();
            ApplyState(state);
            return Task.CompletedTask;
        }

        /// <summary>
        /// 禁用私有 action map、恢复安全 cursor 并销毁运行时 clone。
        /// </summary>
        /// <param name="cancellationToken">共享 AppLifetime 清理 deadline。</param>
        /// <returns>Input System 运行资源已释放时完成的任务。</returns>
        public Task StopAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (_stopped)
            {
                return Task.CompletedTask;
            }

            if (_initialized)
            {
                EnsureMainThread();
                _runtimeInputActions.Disable();
            }

            Cursor.visible = true;
            Cursor.lockState = CursorLockMode.None;
            CurrentState = ClientUiInputState.Gameplay;
            DestroyRuntimeInputClone();
            _initialized = false;
            _stopped = true;
            return Task.CompletedTask;
        }

        /// <summary>
        /// 对一个 framework Host 数组执行 null、identity、instance 与 framework 一致性验证。
        /// </summary>
        /// <typeparam name="THost">具体 Unity Host 类型。</typeparam>
        /// <param name="hosts">直接序列化 Host 数组。</param>
        /// <param name="routes">跨 framework route 唯一性集合。</param>
        /// <param name="instances">跨 framework Host instance 唯一性集合。</param>
        /// <param name="expectedFramework">数组允许的唯一 framework。</param>
        private static void ValidateHosts<THost>(
            IReadOnlyList<THost> hosts,
            ISet<ClientUiRouteId> routes,
            ISet<IClientUiViewHost> instances,
            ClientUiFrameworkOwner expectedFramework)
            where THost : MonoBehaviour, IClientUiViewHost
        {
            for (var index = 0; index < hosts.Count; index++)
            {
                var host = hosts[index];
                if (host == null)
                {
                    throw new InvalidOperationException($"{expectedFramework} Host 索引 {index} 不能为空。");
                }

                if (!Enum.IsDefined(typeof(ClientUiRouteId), host.RouteId) ||
                    host.RouteId == ClientUiRouteId.None ||
                    host.FrameworkOwner != expectedFramework)
                {
                    throw new InvalidOperationException($"{expectedFramework} Host 索引 {index} 的 route/framework 配置非法。");
                }

                if (!routes.Add(host.RouteId))
                {
                    throw new InvalidOperationException($"UI route {host.RouteId} 被重复登记。");
                }

                if (!instances.Add(host))
                {
                    throw new InvalidOperationException($"UI Host {host.name} 被重复登记。");
                }
            }
        }

        /// <summary>提交单个不可变 input state，不在中途暴露半完成状态。</summary>
        /// <param name="state">目标输入状态。</param>
        private void ApplyState(ClientUiInputState state)
        {
            var gameplay = state.Mode == ClientUiInputMode.Gameplay;
            _playerActionMap.Disable();
            _uiActionMap.Disable();
            if (gameplay)
            {
                _playerActionMap.Enable();
                Cursor.visible = false;
                Cursor.lockState = CursorLockMode.Locked;
            }
            else
            {
                _uiActionMap.Enable();
                Cursor.visible = true;
                Cursor.lockState = CursorLockMode.None;
            }

            CurrentState = state;
        }

        /// <summary>验证 owner 已初始化、未停止且调用位于初始化主线程。</summary>
        private void EnsureUsableOnMainThread()
        {
            if (!_initialized || _stopped)
            {
                throw new InvalidOperationException("ClientUiHostRoot 尚未初始化或已经停止。");
            }

            EnsureMainThread();
        }

        /// <summary>拒绝从后台线程调用 Unity Input System 与 cursor API。</summary>
        private void EnsureMainThread()
        {
            if (Environment.CurrentManagedThreadId != _mainThreadId)
            {
                throw new InvalidOperationException("ClientUiHostRoot 只能在初始化它的 Unity 主线程使用。");
            }
        }

        /// <summary>销毁 runtime clone 并清空全部派生 action map 引用。</summary>
        private void DestroyRuntimeInputClone()
        {
            if (_runtimeInputActions != null)
            {
                Destroy(_runtimeInputActions);
            }

            _runtimeInputActions = null;
            _playerActionMap = null;
            _uiActionMap = null;
        }
    }
}
