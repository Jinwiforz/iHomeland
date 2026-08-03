using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Presentation.Hosts.UGUI;
using IHomeland.Client.Presentation.Hosts.UIToolkit;
using IHomeland.Client.Presentation.Navigation;
using UnityEngine;
using UnityEngine.InputSystem;
using UnityEngine.InputSystem.Controls;

namespace IHomeland.Client.Presentation.Hosts
{
    /// <summary>
    /// 作为持久 App Scope 内唯一 Input System owner，并显式汇总双 UI framework Host。
    /// </summary>
    /// <remarks>
    /// 该组件只拥有 action map、cursor 和 Host 引用，不查找场景对象，也不保存账号、连接或世界事实。
    /// Isolated fixture 的 Host 列表可以为空；production 必须通过直接序列化引用登记完整产品 Host。
    /// </remarks>
    [DisallowMultipleComponent]
    public sealed class ClientUiHostRoot :
        MonoBehaviour,
        IClientUiInputCoordinator,
        IClientBattleInputSource
    {
        /// <summary>保存项目级 Input System 资产模板；运行时只操作其私有 clone。</summary>
        [SerializeField]
        [Tooltip("只读 Input System 模板；运行时 clone 后由该组件唯一控制 Player/UI action map。")]
        private InputActionAsset _inputActions;

        /// <summary>保存直接序列化的 UI Toolkit route Host。</summary>
        [SerializeField]
        [Tooltip("显式登记的 UI Toolkit route Host；production 必须包含全部已交付页面。")]
        private ClientUiToolkitHost[] _uiToolkitHosts = Array.Empty<ClientUiToolkitHost>();

        /// <summary>保存直接序列化的 uGUI route Host。</summary>
        [SerializeField]
        [Tooltip("显式登记的 uGUI route Host；production 必须包含 WorldHud。")]
        private ClientUguiHost[] _uguiHosts = Array.Empty<ClientUguiHost>();

        /// <summary>保存运行时私有 action asset clone。</summary>
        private InputActionAsset _runtimeInputActions;

        /// <summary>保存私有 clone 中唯一 Player action map。</summary>
        private InputActionMap _playerActionMap;

        /// <summary>保存私有 clone 中唯一 UI action map。</summary>
        private InputActionMap _uiActionMap;

        /// <summary>保存 Player map 中打开产品菜单的独立语义 action。</summary>
        private InputAction _gameplayMenuAction;

        /// <summary>保存 UI map 中返回当前产品页面的标准取消 action。</summary>
        private InputAction _uiCancelAction;

        /// <summary>保存 Player map 中唯一 battle Move action。</summary>
        private InputAction _battleMoveAction;

        /// <summary>保存 Player map 中唯一 battle Aim action。</summary>
        private InputAction _battleAimAction;

        /// <summary>保存 Player map 中唯一 battle Jump action。</summary>
        private InputAction _battleJumpAction;

        /// <summary>保存 Player map 中唯一 battle Primary action。</summary>
        private InputAction _battlePrimaryAction;

        /// <summary>保存 Player map 中唯一 battle Secondary action。</summary>
        private InputAction _battleSecondaryAction;

        /// <summary>保存 Player map 中唯一 battle Interact action。</summary>
        private InputAction _battleInteractAction;

        /// <summary>保存初始化所在 Unity 主线程，阻止后台线程操作 Unity API。</summary>
        private int _mainThreadId;

        /// <summary>表示当前 Input owner 已完成初始化。</summary>
        private bool _initialized;

        /// <summary>表示当前 Input owner 已永久停止。</summary>
        private bool _stopped;

        /// <summary>表示输入模式或窗口焦点变化后仍需在当前帧末确认一次 cursor 状态。</summary>
        private bool _cursorCommitPending;

        /// <summary>表示 Player/Menu 已在 Input System 回调中产生，等待帧末提交产品意图。</summary>
        private bool _gameplayMenuRequestPending;

        /// <summary>表示 UI/Cancel 已在 Input System 回调中产生，等待帧末提交产品意图。</summary>
        private bool _uiCancelRequestPending;

        /// <summary>保存 UI/Cancel 发生时的交互 route，拒绝把迟到输入提交给后继页面。</summary>
        private ClientUiRouteId _pendingUiCancelRouteId = ClientUiRouteId.None;

        /// <summary>表示输入 owner 切换后仍在等待 Menu/Cancel 相关物理按键全部释放。</summary>
        private bool _inputReleaseGateActive;

        /// <summary>保存 release gate 建立的帧，保证新 action map 至少经过一次 Input System update。</summary>
        private int _inputReleaseGateFrame = -1;

        /// <summary>
        /// 表示Gameplay map刚获得owner，下一可用样本必须抑制重锁cursor产生的aim/edge瞬变。
        /// </summary>
        private bool _gameplaySampleWarmupPending;

        /// <summary>获取最近一次原子提交的输入状态。</summary>
        ClientUiInputState IClientUiInputCoordinator.CurrentState => CurrentState;

        /// <summary>获取最近一次原子提交的输入状态，供同程序集测试观察。</summary>
        internal ClientUiInputState CurrentState { get; private set; } = ClientUiInputState.Gameplay;

        /// <summary>获取 runtime clone 的 Player action map 是否启用，供生命周期测试观察。</summary>
        internal bool IsPlayerActionMapEnabled => _playerActionMap != null && _playerActionMap.enabled;

        /// <summary>获取 runtime clone 的 UI action map 是否启用，供生命周期测试观察。</summary>
        internal bool IsUiActionMapEnabled => _uiActionMap != null && _uiActionMap.enabled;

        /// <summary>
        /// 在 Gameplay mode 收到一次菜单动作时通知显式连接的产品入口。
        /// </summary>
        /// <remarks>
        /// 该事件只把 Input System action 转换为无参数语义意图，不广播键位、设备或业务状态。
        /// </remarks>
        internal event Action GameplayMenuRequested;

        /// <summary>
        /// 在非 Gameplay mode 收到 UI/Cancel 时通知显式连接的当前 route 处理者。
        /// </summary>
        internal event Action<ClientUiRouteId> UiCancelRequested;

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
        /// 验证 production Input asset 包含 B0.7 所需的封闭 Player semantic actions。
        /// </summary>
        /// <exception cref="InvalidOperationException">缺少 Player map、任一 action 或 control type 漂移时抛出。</exception>
        internal void ValidateBattleInputConfiguration()
        {
            if (_inputActions == null)
            {
                throw new InvalidOperationException(
                    "ClientUiHostRoot 缺少 Input System 资产直接引用。");
            }

            var player = _inputActions.FindActionMap(
                "Player",
                throwIfNotFound: true);
            ValidateBattleAction(
                player,
                "Move",
                InputActionType.Value,
                "Vector2");
            ValidateBattleAction(
                player,
                "Aim",
                InputActionType.Value,
                "Vector2");
            ValidateBattleAction(
                player,
                "Jump",
                InputActionType.Button,
                "Button");
            ValidateBattleAction(
                player,
                "Primary",
                InputActionType.Button,
                "Button");
            ValidateBattleAction(
                player,
                "Secondary",
                InputActionType.Button,
                "Button");
            ValidateBattleAction(
                player,
                "Interact",
                InputActionType.Button,
                "Button");
        }

        /// <summary>
        /// 捕获唯一Gameplay input owner的当前可用性与battle semantic sample。
        /// </summary>
        /// <returns>可采样时携带当前样本；UI、失焦或transition gate期间为明确不可用帧。</returns>
        internal ClientBattleSceneInputFrame CaptureBattleInput()
        {
            if (!_initialized ||
                _stopped ||
                _inputReleaseGateActive ||
                CurrentState.Mode != ClientUiInputMode.Gameplay ||
                !UnityEngine.Application.isFocused ||
                _battleMoveAction == null ||
                _battleAimAction == null ||
                _battleJumpAction == null ||
                _battlePrimaryAction == null ||
                _battleSecondaryAction == null ||
                _battleInteractAction == null)
            {
                return default;
            }

            EnsureMainThread();
            var suppressTransitionEdges = _gameplaySampleWarmupPending;
            _gameplaySampleWarmupPending = false;
            return new ClientBattleSceneInputFrame(
                gameplayAvailable: true,
                sample: new ClientBattleSceneInputSample(
                    _battleMoveAction.ReadValue<Vector2>(),
                    suppressTransitionEdges
                        ? Vector2.zero
                        : _battleAimAction.ReadValue<Vector2>(),
                    !suppressTransitionEdges &&
                        _battleJumpAction.WasPressedThisFrame(),
                    !suppressTransitionEdges &&
                        _battlePrimaryAction.WasPressedThisFrame(),
                    !suppressTransitionEdges &&
                        _battleSecondaryAction.WasPressedThisFrame(),
                    !suppressTransitionEdges &&
                        _battleInteractAction.WasPressedThisFrame()));
        }

        /// <summary>通过 battle Scene 窄端口验证 production semantic actions。</summary>
        void IClientBattleInputSource.ValidateBattleInputConfiguration()
        {
            ValidateBattleInputConfiguration();
        }

        /// <summary>通过battle Scene窄端口读取current Gameplay input owner帧。</summary>
        /// <returns>包含明确可用性与非权威semantic sample的值快照。</returns>
        ClientBattleSceneInputFrame IClientBattleInputSource.CaptureBattleInput()
        {
            return CaptureBattleInput();
        }

        /// <summary>
        /// 在 Host 初始化前把唯一产品上下文显式注入全部直接引用的页面 binding。
        /// </summary>
        /// <param name="context">Composition 创建的窄产品上下文。</param>
        /// <exception cref="ArgumentNullException">上下文为空时抛出。</exception>
        internal void ConfigureProductBindings(IClientUiProductContext context)
        {
            if (context == null)
            {
                throw new ArgumentNullException(nameof(context));
            }

            ValidateConfiguration();
            foreach (var host in _uiToolkitHosts)
            {
                host.ConfigureProductContext(context);
            }

            foreach (var host in _uguiHosts)
            {
                host.ConfigureProductContext(context);
            }
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
                _gameplayMenuAction = _playerActionMap.FindAction("Menu", throwIfNotFound: true);
                _uiCancelAction = _uiActionMap.FindAction("Cancel", throwIfNotFound: true);
                _battleMoveAction = _playerActionMap.FindAction(
                    "Move",
                    throwIfNotFound: false);
                _battleAimAction = _playerActionMap.FindAction(
                    "Aim",
                    throwIfNotFound: false);
                _battleJumpAction = _playerActionMap.FindAction(
                    "Jump",
                    throwIfNotFound: false);
                _battlePrimaryAction = _playerActionMap.FindAction(
                    "Primary",
                    throwIfNotFound: false);
                _battleSecondaryAction = _playerActionMap.FindAction(
                    "Secondary",
                    throwIfNotFound: false);
                _battleInteractAction = _playerActionMap.FindAction(
                    "Interact",
                    throwIfNotFound: false);
                _gameplayMenuAction.performed += OnGameplayMenuPerformed;
                _uiCancelAction.performed += OnUiCancelPerformed;
                _runtimeInputActions.Disable();
                ApplyState(ClientUiInputState.Gameplay);
                _gameplaySampleWarmupPending = true;
                _initialized = true;
                return Task.CompletedTask;
            }
            catch
            {
                CurrentState = ClientUiInputState.Gameplay;
                Cursor.lockState = CursorLockMode.None;
                Cursor.visible = true;
                _cursorCommitPending = false;
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

            Cursor.lockState = CursorLockMode.None;
            Cursor.visible = true;
            CurrentState = ClientUiInputState.Gameplay;
            _cursorCommitPending = false;
            _inputReleaseGateActive = false;
            _inputReleaseGateFrame = -1;
            _gameplaySampleWarmupPending = false;
            ClearPendingInputIntent();
            DestroyRuntimeInputClone();
            GameplayMenuRequested = null;
            UiCancelRequested = null;
            _initialized = false;
            _stopped = true;
            return Task.CompletedTask;
        }

        /// <summary>
        /// 在输入模式或窗口焦点变化后的首个帧末确认一次 cursor 状态。
        /// </summary>
        private void LateUpdate()
        {
            if (TryReleaseInputTransitionGate())
            {
                DispatchPendingInputIntent();
            }

            if (!_cursorCommitPending || !_initialized || _stopped || !UnityEngine.Application.isFocused)
            {
                return;
            }

            CommitCursorState(CurrentState.Mode == ClientUiInputMode.Gameplay);
            _cursorCommitPending = false;
        }

        /// <summary>窗口重新获得焦点时重新提交当前输入模式的 cursor policy。</summary>
        /// <param name="hasFocus">当前 Player 是否获得输入焦点。</param>
        private void OnApplicationFocus(bool hasFocus)
        {
            if (!hasFocus || !_initialized || _stopped)
            {
                return;
            }

            CommitCursorState(CurrentState.Mode == ClientUiInputMode.Gameplay);
            _cursorCommitPending = true;
        }

        /// <summary>
        /// 仅在 Gameplay mode 把 Player/Menu 的 performed 回调提升为产品菜单意图。
        /// </summary>
        /// <param name="context">Input System 提交的 action callback 上下文。</param>
        private void OnGameplayMenuPerformed(InputAction.CallbackContext context)
        {
            ClientUiDiagnostics.Trace(
                nameof(ClientUiHostRoot),
                "gameplay_menu_performed",
                $"mode={CurrentState.Mode} route={CurrentState.InteractiveRouteId} release_gate={_inputReleaseGateActive}");
            if (!_initialized || _stopped || _inputReleaseGateActive ||
                CurrentState.Mode != ClientUiInputMode.Gameplay)
            {
                return;
            }

            // Input System 正在遍历 callback 时切换 action map，可能让新 map 立即消费同一设备状态。
            // 这里只记录语义输入，帧末离开 Input System 调用栈后再允许 Router 改变 map 与页面。
            _gameplayMenuRequestPending = true;
        }

        /// <summary>
        /// 把 UI/Cancel 提升为带当前 route identity 的返回意图，不在输入 owner 内决定业务导航。
        /// </summary>
        /// <param name="context">Input System 提交的 action callback 上下文。</param>
        private void OnUiCancelPerformed(InputAction.CallbackContext context)
        {
            ClientUiDiagnostics.Trace(
                nameof(ClientUiHostRoot),
                "ui_cancel_performed",
                $"mode={CurrentState.Mode} route={CurrentState.InteractiveRouteId} release_gate={_inputReleaseGateActive}");
            if (!_initialized || _stopped || _inputReleaseGateActive ||
                CurrentState.Mode == ClientUiInputMode.Gameplay)
            {
                return;
            }

            // 捕获发生时的 route；帧末若 owner 已变化则丢弃，避免迟到 Cancel 关闭新页面。
            _uiCancelRequestPending = true;
            _pendingUiCancelRouteId = CurrentState.InteractiveRouteId;
        }

        /// <summary>在帧末离开 Input System callback 栈后提交至多一个仍属于当前输入 owner 的语义意图。</summary>
        /// <remarks>
        /// Router 会在订阅回调内切换 Player/UI action map。若直接从 InputAction.performed 调用 Router，
        /// 新启用的 map 可能消费尚未释放的同一帧设备状态，形成 Menu/Cancel 反复开关页面的反馈环。
        /// </remarks>
        private void DispatchPendingInputIntent()
        {
            if (!_initialized || _stopped)
            {
                ClearPendingInputIntent();
                return;
            }

            var menuPending = _gameplayMenuRequestPending;
            var cancelPending = _uiCancelRequestPending;
            var cancelledRoute = _pendingUiCancelRouteId;
            ClearPendingInputIntent();

            if (menuPending && CurrentState.Mode == ClientUiInputMode.Gameplay)
            {
                ClientUiDiagnostics.Trace(
                    nameof(ClientUiHostRoot),
                    "gameplay_menu_dispatched",
                    $"mode={CurrentState.Mode} route={CurrentState.InteractiveRouteId}");
                NotifyGameplayMenuRequested();
                return;
            }

            if (cancelPending &&
                CurrentState.Mode != ClientUiInputMode.Gameplay &&
                CurrentState.InteractiveRouteId == cancelledRoute)
            {
                ClientUiDiagnostics.Trace(
                    nameof(ClientUiHostRoot),
                    "ui_cancel_dispatched",
                    $"mode={CurrentState.Mode} route={cancelledRoute}");
                NotifyUiCancelRequested(cancelledRoute);
            }
        }

        /// <summary>通知 Gameplay 菜单订阅者，并隔离单个订阅者异常。</summary>
        private void NotifyGameplayMenuRequested()
        {
            var subscribers = GameplayMenuRequested;
            if (subscribers == null)
            {
                return;
            }

            foreach (Action subscriber in subscribers.GetInvocationList())
            {
                try
                {
                    subscriber();
                }
                catch (Exception exception)
                {
                    Debug.LogException(exception, this);
                }
            }
        }

        /// <summary>通知 UI Cancel 订阅者，并隔离单个订阅者异常。</summary>
        /// <param name="routeId">产生 Cancel 时且当前仍持有交互权的 route。</param>
        private void NotifyUiCancelRequested(ClientUiRouteId routeId)
        {
            var subscribers = UiCancelRequested;
            if (subscribers == null)
            {
                return;
            }

            foreach (Action<ClientUiRouteId> subscriber in subscribers.GetInvocationList())
            {
                try
                {
                    subscriber(routeId);
                }
                catch (Exception exception)
                {
                    Debug.LogException(exception, this);
                }
            }
        }

        /// <summary>清除尚未提交的帧级语义输入，避免停止或 owner 变化后回写。</summary>
        private void ClearPendingInputIntent()
        {
            _gameplayMenuRequestPending = false;
            _uiCancelRequestPending = false;
            _pendingUiCancelRouteId = ClientUiRouteId.None;
        }

        /// <summary>
        /// 仅在 owner 切换后的下一帧且 Menu/Cancel 全部释放后解除输入边沿门。
        /// </summary>
        /// <remarks>
        /// Input System 启用 action map 时会检查当前设备状态。若切换发生在按键仍按下期间，
        /// 新 map 可能立即产生 performed；必须等待一次完整 release，不能用时间窗口猜测用户意图。
        /// </remarks>
        /// <returns>当前帧是否允许分发新的 Menu/Cancel 产品意图。</returns>
        private bool TryReleaseInputTransitionGate()
        {
            if (!_inputReleaseGateActive)
            {
                return true;
            }

            ClearPendingInputIntent();
            if (Time.frameCount <= _inputReleaseGateFrame || HasPressedControl(_gameplayMenuAction) ||
                HasPressedControl(_uiCancelAction))
            {
                return false;
            }

            _inputReleaseGateActive = false;
            _inputReleaseGateFrame = -1;
            ClientUiDiagnostics.Trace(
                nameof(ClientUiHostRoot),
                "input_release_gate_opened",
                $"mode={CurrentState.Mode} route={CurrentState.InteractiveRouteId}");
            return true;
        }

        /// <summary>检查 action 的任一 ButtonControl 是否仍处于按下状态。</summary>
        /// <param name="action">Menu 或 Cancel action；允许在停止清理期间为 null。</param>
        /// <returns>至少一个绑定按钮尚未释放时返回 true。</returns>
        private static bool HasPressedControl(InputAction action)
        {
            if (action == null)
            {
                return false;
            }

            foreach (var control in action.controls)
            {
                if (control is ButtonControl button && button.isPressed)
                {
                    return true;
                }
            }

            return false;
        }

        /// <summary>验证一个 production battle action 的唯一 identity、action type 与 control type。</summary>
        /// <param name="player">Input asset 中唯一 Player action map。</param>
        /// <param name="actionName">冻结 semantic action 名称。</param>
        /// <param name="expectedActionType">冻结 Action Type。</param>
        /// <param name="expectedControlType">Value action 必须显式声明、Button action 可由类型推导的 control type。</param>
        private static void ValidateBattleAction(
            InputActionMap player,
            string actionName,
            InputActionType expectedActionType,
            string expectedControlType)
        {
            var action = player.FindAction(actionName, throwIfNotFound: false);
            if (action == null)
            {
                throw new InvalidOperationException(
                    $"Player/{actionName} action 缺失。");
            }

            var expectedControlMatches =
                string.Equals(
                    action.expectedControlType,
                    expectedControlType,
                    StringComparison.Ordinal) ||
                (expectedActionType == InputActionType.Button &&
                 string.IsNullOrEmpty(action.expectedControlType));
            if (action.type != expectedActionType || !expectedControlMatches)
            {
                throw new InvalidOperationException(
                    $"Player/{actionName} 必须声明 {expectedActionType}/{expectedControlType} input contract。");
            }
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
            var ownerChanged = CurrentState.Mode != state.Mode ||
                               CurrentState.InteractiveRouteId != state.InteractiveRouteId;
            ClientUiDiagnostics.Trace(
                nameof(ClientUiHostRoot),
                "input_state_applying",
                $"previous_mode={CurrentState.Mode} previous_route={CurrentState.InteractiveRouteId} " +
                $"target_mode={state.Mode} target_route={state.InteractiveRouteId} owner_changed={ownerChanged}");
            var gameplay = state.Mode == ClientUiInputMode.Gameplay;
            _playerActionMap.Disable();
            _uiActionMap.Disable();
            if (gameplay)
            {
                _playerActionMap.Enable();
            }
            else
            {
                _uiActionMap.Enable();
            }

            CurrentState = state;
            if (ownerChanged)
            {
                // 切换 map 前产生但尚未分发的输入不属于新的 owner。
                ClearPendingInputIntent();
                _inputReleaseGateActive = true;
                _inputReleaseGateFrame = Time.frameCount;
                _gameplaySampleWarmupPending = gameplay;
            }

            ClientUiDiagnostics.Trace(
                nameof(ClientUiHostRoot),
                "input_state_applied",
                $"mode={CurrentState.Mode} route={CurrentState.InteractiveRouteId} release_gate={_inputReleaseGateActive}");

            CommitCursorState(gameplay);
            _cursorCommitPending = true;
        }

        /// <summary>
        /// 按唯一输入 owner 的目标模式提交 cursor；UI 必须先解锁再显示，Gameplay 则锁定并隐藏。
        /// </summary>
        /// <param name="gameplay">是否提交 Gameplay cursor policy。</param>
        private static void CommitCursorState(bool gameplay)
        {
            if (gameplay)
            {
                if (Cursor.lockState != CursorLockMode.Locked)
                {
                    Cursor.lockState = CursorLockMode.Locked;
                }

                if (Cursor.visible)
                {
                    Cursor.visible = false;
                }

                return;
            }

            // Locked 状态强制隐藏 cursor；必须先解锁，再提交可见性。
            if (Cursor.lockState != CursorLockMode.None)
            {
                Cursor.lockState = CursorLockMode.None;
            }

            if (!Cursor.visible)
            {
                Cursor.visible = true;
            }
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
            if (_gameplayMenuAction != null)
            {
                _gameplayMenuAction.performed -= OnGameplayMenuPerformed;
            }

            if (_uiCancelAction != null)
            {
                _uiCancelAction.performed -= OnUiCancelPerformed;
            }

            if (_runtimeInputActions != null)
            {
                Destroy(_runtimeInputActions);
            }

            _runtimeInputActions = null;
            _playerActionMap = null;
            _uiActionMap = null;
            _gameplayMenuAction = null;
            _uiCancelAction = null;
            _battleMoveAction = null;
            _battleAimAction = null;
            _battleJumpAction = null;
            _battlePrimaryAction = null;
            _battleSecondaryAction = null;
            _battleInteractAction = null;
        }
    }
}
