using System.Collections;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Presentation.Hosts;
using IHomeland.Client.Presentation.Hosts.UGUI;
using IHomeland.Client.Presentation.Hosts.UIToolkit;
using IHomeland.Client.Presentation.Navigation;
using NUnit.Framework;
using UnityEngine;
using UnityEngine.EventSystems;
using UnityEngine.InputSystem;
using UnityEngine.InputSystem.LowLevel;
using UnityEngine.TestTools;
using UnityEngine.UI;
using UnityEngine.UIElements;
using UguiButton = UnityEngine.UI.Button;
using UnityCursor = UnityEngine.Cursor;

namespace IHomeland.Client.Tests.PlayMode
{
    /// <summary>
    /// 通过程序化 GameObject 验证 Input clone、双 framework layer、focus、raycast 与 teardown。
    /// </summary>
    public sealed class ClientUiHostPlayModeTests
    {
        /// <summary>
        /// 程序化 Host 阶段同步完成；一秒上限用于让清理死锁快速暴露而不拖慢测试。
        /// </summary>
        private static readonly System.TimeSpan TestCleanupTimeout = System.TimeSpan.FromSeconds(1);

        /// <summary>保存每个测试创建且需要在显式停止后同步销毁的 GameObject。</summary>
        private readonly List<GameObject> _gameObjects = new List<GameObject>();

        /// <summary>保存每个测试创建且需要显式销毁的 ScriptableObject。</summary>
        private readonly List<ScriptableObject> _assets = new List<ScriptableObject>();

        /// <summary>保存测试前的后台输入策略，避免修改泄漏到其他 PlayMode fixture。</summary>
        private InputSettings.BackgroundBehavior _originalBackgroundBehavior;

#if UNITY_EDITOR
        /// <summary>保存测试前的 Editor Game View 输入路由策略。</summary>
        private InputSettings.EditorInputBehaviorInPlayMode _originalEditorInputBehavior;
#endif

        /// <summary>让程序化虚拟设备不依赖 Game View、远程桌面或窗口焦点。</summary>
        [SetUp]
        public void ConfigureDeterministicInputRouting()
        {
            _originalBackgroundBehavior = InputSystem.settings.backgroundBehavior;
            InputSystem.settings.backgroundBehavior = InputSettings.BackgroundBehavior.IgnoreFocus;
#if UNITY_EDITOR
            _originalEditorInputBehavior = InputSystem.settings.editorInputBehaviorInPlayMode;
            InputSystem.settings.editorInputBehaviorInPlayMode =
                InputSettings.EditorInputBehaviorInPlayMode.AllDeviceInputAlwaysGoesToGameView;
#endif
        }

        /// <summary>恢复项目输入设置，保证测试不改变后续 Player 或 Editor 行为。</summary>
        [TearDown]
        public void RestoreInputRouting()
        {
            InputSystem.settings.backgroundBehavior = _originalBackgroundBehavior;
#if UNITY_EDITOR
            InputSystem.settings.editorInputBehaviorInPlayMode = _originalEditorInputBehavior;
#endif
        }

        /// <summary>
        /// 每个测试后销毁全部程序化对象并恢复 cursor baseline，防止跨测试残留 owner。
        /// </summary>
        /// <returns>等待 Input owner 的 runtime clone 完成延迟销毁的枚举器。</returns>
        [UnityTearDown]
        public IEnumerator TearDownFixtures()
        {
            foreach (var gameObject in _gameObjects)
            {
                if (gameObject == null ||
                    !gameObject.TryGetComponent<ClientUiHostRoot>(out var hostRoot))
                {
                    continue;
                }

                var stop = hostRoot.StopAsync(CancellationToken.None);
                Assert.That(stop.IsCompletedSuccessfully, Is.True);
            }

            foreach (var gameObject in _gameObjects)
            {
                if (gameObject != null)
                {
                    Object.DestroyImmediate(gameObject);
                }
            }

            foreach (var asset in _assets)
            {
                if (asset != null)
                {
                    Object.DestroyImmediate(asset);
                }
            }

            _gameObjects.Clear();
            _assets.Clear();
            UnityCursor.lockState = CursorLockMode.None;
            UnityCursor.visible = true;
            yield return null;
        }

        /// <summary>
        /// 保护唯一 Input owner 只切换 runtime clone，并在停止后恢复 cursor 与禁用全部 map。
        /// </summary>
        /// <returns>等待一帧 Unity 对象激活和清理的枚举器。</returns>
        [UnityTest]
        public IEnumerator InputOwnerClonesAssetAndRestoresSafeBaseline()
        {
            var inputAsset = CreateInputAsset();
            var hostRoot = CreateHostRoot(inputAsset);
            hostRoot.gameObject.SetActive(true);

            var initialize = hostRoot.InitializeAsync(CancellationToken.None);
            Assert.That(initialize.IsCompletedSuccessfully, Is.True);
            Assert.That(inputAsset.FindActionMap("Player").enabled, Is.False);
            Assert.That(inputAsset.FindActionMap("UI").enabled, Is.False);
            Assert.That(hostRoot.IsPlayerActionMapEnabled, Is.True);
            Assert.That(hostRoot.IsUiActionMapEnabled, Is.False);
            if (UnityEngine.Application.isFocused)
            {
                Assert.That(UnityCursor.lockState, Is.EqualTo(CursorLockMode.Locked));
            }

            var input = (IClientUiInputCoordinator)hostRoot;
            var apply = input.ApplyAsync(
                new ClientUiInputState(ClientUiInputMode.Ui, ClientUiRouteId.Login),
                CancellationToken.None);
            Assert.That(apply.IsCompletedSuccessfully, Is.True);
            Assert.That(hostRoot.IsPlayerActionMapEnabled, Is.False);
            Assert.That(hostRoot.IsUiActionMapEnabled, Is.True);
            Assert.That(UnityCursor.visible, Is.True);
            Assert.That(UnityCursor.lockState, Is.EqualTo(CursorLockMode.None));

            var stop = hostRoot.StopAsync(CancellationToken.None);
            Assert.That(stop.IsCompletedSuccessfully, Is.True);
            Assert.That(hostRoot.IsPlayerActionMapEnabled, Is.False);
            Assert.That(hostRoot.IsUiActionMapEnabled, Is.False);
            Assert.That(UnityCursor.visible, Is.True);
            Assert.That(UnityCursor.lockState, Is.EqualTo(CursorLockMode.None));
            yield return null;
        }

        /// <summary>
        /// 保护 Player/Menu 只在 Gameplay mode 发布一次产品意图，UI mode 与停止状态均不透传。
        /// </summary>
        /// <returns>等待 Input System 消费虚拟键盘事件的枚举器。</returns>
        [UnityTest]
        public IEnumerator GameplayMenuAndUiCancelOnlyPublishForTheirOwningModes()
        {
            var keyboard = InputSystem.AddDevice<Keyboard>();
            try
            {
                var inputAsset = CreateInputAsset();
                var hostRoot = CreateHostRoot(inputAsset);
                var requests = 0;
                var cancellations = 0;
                var cancelledRoute = ClientUiRouteId.None;
                var input = (IClientUiInputCoordinator)hostRoot;
                hostRoot.GameplayMenuRequested += () =>
                {
                    requests++;
                    Assert.That(
                        input.ApplyAsync(
                            new ClientUiInputState(ClientUiInputMode.Ui, ClientUiRouteId.WorldVisit),
                            CancellationToken.None).IsCompletedSuccessfully,
                        Is.True);
                };
                hostRoot.UiCancelRequested += routeId =>
                {
                    cancellations++;
                    cancelledRoute = routeId;
                    Assert.That(
                        input.ApplyAsync(ClientUiInputState.Gameplay, CancellationToken.None).IsCompletedSuccessfully,
                        Is.True);
                };
                hostRoot.gameObject.SetActive(true);

                Assert.That(hostRoot.InitializeAsync(CancellationToken.None).IsCompletedSuccessfully, Is.True);
                PressAndReleaseTab(keyboard);
                PressAndReleaseEscape(keyboard);
                Assert.That(requests, Is.Zero, "Input callback 内不得同步提交并切换 action map。");
                yield return null;
                Assert.That(requests, Is.EqualTo(1));
                Assert.That(cancellations, Is.Zero);
                Assert.That(input.CurrentState.Mode, Is.EqualTo(ClientUiInputMode.Ui));
                Assert.That(input.CurrentState.InteractiveRouteId, Is.EqualTo(ClientUiRouteId.WorldVisit));
                var uiInputFrame = hostRoot.CaptureBattleInput();
                Assert.That(uiInputFrame.GameplayAvailable, Is.False);
                Assert.That(uiInputFrame.Sample.Move, Is.EqualTo(Vector2.zero));
                Assert.That(uiInputFrame.Sample.JumpPressed, Is.False);

                // Owner 切换后的首帧只用于确认相关物理按键已释放。
                yield return null;
                PressAndReleaseTab(keyboard);
                PressAndReleaseEscape(keyboard);
                Assert.That(requests, Is.EqualTo(1));
                Assert.That(cancellations, Is.Zero, "Input callback 内不得同步关闭当前 route。");
                yield return null;
                Assert.That(cancellations, Is.EqualTo(1));
                Assert.That(cancelledRoute, Is.EqualTo(ClientUiRouteId.WorldVisit));
                Assert.That(input.CurrentState, Is.EqualTo(ClientUiInputState.Gameplay));

                // 新Player map必须经过一次Input System update与物理按键释放后才重新提供样本。
                Assert.That(hostRoot.CaptureBattleInput().GameplayAvailable, Is.False);
                yield return null;
                Assert.That(hostRoot.CaptureBattleInput().GameplayAvailable, Is.True);

                Assert.That(hostRoot.StopAsync(CancellationToken.None).IsCompletedSuccessfully, Is.True);
                PressAndReleaseTab(keyboard);
                PressAndReleaseEscape(keyboard);
                yield return null;
                Assert.That(requests, Is.EqualTo(1));
                Assert.That(cancellations, Is.EqualTo(1));
            }
            finally
            {
                InputSystem.RemoveDevice(keyboard);
            }

            yield return null;
        }

        /// <summary>
        /// 保护 owner 切换必须等待共享物理按键释放，阻止 Menu/Cancel 反复开关同一 overlay。
        /// </summary>
        /// <returns>等待 Input System 初始状态检查与帧末产品意图分发的枚举器。</returns>
        [UnityTest]
        public IEnumerator SharedMenuAndCancelBindingCannotCreateRouteFeedbackLoop()
        {
            var keyboard = InputSystem.AddDevice<Keyboard>();
            try
            {
                var inputAsset = CreateInputAsset(sharedMenuAndCancelBinding: true);
                var hostRoot = CreateHostRoot(inputAsset);
                var requests = 0;
                var cancellations = 0;
                var input = (IClientUiInputCoordinator)hostRoot;
                hostRoot.GameplayMenuRequested += () =>
                {
                    requests++;
                    Assert.That(
                        input.ApplyAsync(
                            new ClientUiInputState(ClientUiInputMode.Ui, ClientUiRouteId.WorldVisit),
                            CancellationToken.None).IsCompletedSuccessfully,
                        Is.True);
                };
                hostRoot.UiCancelRequested += _ =>
                {
                    cancellations++;
                    Assert.That(
                        input.ApplyAsync(ClientUiInputState.Gameplay, CancellationToken.None).IsCompletedSuccessfully,
                        Is.True);
                };
                hostRoot.gameObject.SetActive(true);

                Assert.That(hostRoot.InitializeAsync(CancellationToken.None).IsCompletedSuccessfully, Is.True);
                PressTab(keyboard);
                yield return null;
                Assert.That(requests, Is.EqualTo(1));
                Assert.That(cancellations, Is.Zero);
                Assert.That(input.CurrentState.Mode, Is.EqualTo(ClientUiInputMode.Ui));

                // UI map 的同一 Tab 仍处于按下状态，不能被初始状态检查提升为 Cancel。
                yield return null;
                yield return null;
                Assert.That(requests, Is.EqualTo(1));
                Assert.That(cancellations, Is.Zero);

                ReleaseKeyboard(keyboard);
                yield return null;
                PressTab(keyboard);
                yield return null;
                Assert.That(cancellations, Is.EqualTo(1));
                Assert.That(input.CurrentState.Mode, Is.EqualTo(ClientUiInputMode.Gameplay));

                // 返回 Player map 后仍按下的同一 Tab 也不能立即重新打开 overlay。
                yield return null;
                yield return null;
                Assert.That(requests, Is.EqualTo(1));
                Assert.That(cancellations, Is.EqualTo(1));

                ReleaseKeyboard(keyboard);
                yield return null;
                PressTab(keyboard);
                yield return null;
                Assert.That(requests, Is.EqualTo(2));
                Assert.That(cancellations, Is.EqualTo(1));

                ReleaseKeyboard(keyboard);
                Assert.That(hostRoot.StopAsync(CancellationToken.None).IsCompletedSuccessfully, Is.True);
            }
            finally
            {
                InputSystem.RemoveDevice(keyboard);
            }

            yield return null;
        }

        /// <summary>
        /// 保护 UI Toolkit screen 与 uGUI modal 共享 route、input、layer、raycast 和 focus 恢复语义。
        /// </summary>
        /// <returns>等待 PanelRenderer panel 建立并执行完整 route transaction 的枚举器。</returns>
        [UnityTest]
        public IEnumerator ToolkitScreenUguiOverlayAndToolkitModalShareOneRouterOwner()
        {
            var eventSystemObject = Track(new GameObject("ClientUiEventSystem"));
            var eventSystem = eventSystemObject.AddComponent<EventSystem>();

            var panelSettings = ScriptableObject.CreateInstance<PanelSettings>();
            _assets.Add(panelSettings);
            var themeStyleSheet = ScriptableObject.CreateInstance<ThemeStyleSheet>();
            _assets.Add(themeStyleSheet);
            panelSettings.themeStyleSheet = themeStyleSheet;
            var toolkitObject = Track(new GameObject("ToolkitScreen"));
            toolkitObject.SetActive(false);
            var visualTreeAsset = ScriptableObject.CreateInstance<VisualTreeAsset>();
            _assets.Add(visualTreeAsset);
            var panelRenderer = toolkitObject.AddComponent<PanelRenderer>();
            panelRenderer.panelSettings = panelSettings;
            panelRenderer.visualTreeAsset = visualTreeAsset;
            VisualElement toolkitRoot = null;
            var toolkitReady = new TaskCompletionSource<bool>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            panelRenderer.RegisterUIReloadCallback((_, root, __) =>
            {
                toolkitRoot = root;
                toolkitReady.TrySetResult(true);
            });
            var toolkitHost = toolkitObject.AddComponent<ClientUiToolkitHost>();
            toolkitHost.ConfigureBeforeActivation(ClientUiRouteId.Login, panelRenderer, "DefaultAction");

            var modalToolkitObject = Track(new GameObject("ToolkitModal"));
            modalToolkitObject.SetActive(false);
            var modalVisualTreeAsset = ScriptableObject.CreateInstance<VisualTreeAsset>();
            _assets.Add(modalVisualTreeAsset);
            var modalPanelRenderer = modalToolkitObject.AddComponent<PanelRenderer>();
            modalPanelRenderer.panelSettings = panelSettings;
            modalPanelRenderer.visualTreeAsset = modalVisualTreeAsset;
            VisualElement modalToolkitRoot = null;
            var modalToolkitReady = new TaskCompletionSource<bool>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            modalPanelRenderer.RegisterUIReloadCallback((_, root, __) =>
            {
                modalToolkitRoot = root;
                modalToolkitReady.TrySetResult(true);
            });
            var modalToolkitHost = modalToolkitObject.AddComponent<ClientUiToolkitHost>();
            modalToolkitHost.ConfigureBeforeActivation(
                ClientUiRouteId.ConnectionLost,
                modalPanelRenderer,
                "ModalAction");

            var canvasObject = Track(new GameObject("UguiModal"));
            canvasObject.SetActive(false);
            var canvas = canvasObject.AddComponent<Canvas>();
            canvas.renderMode = RenderMode.ScreenSpaceOverlay;
            var canvasGroup = canvasObject.AddComponent<CanvasGroup>();
            var buttonObject = Track(new GameObject("ModalDefault"));
            buttonObject.transform.SetParent(canvasObject.transform, worldPositionStays: false);
            var modalButton = buttonObject.AddComponent<UguiButton>();
            var invalidFocusObject = Track(new GameObject("InvalidFocusWithoutSelectable"));
            invalidFocusObject.transform.SetParent(canvasObject.transform, worldPositionStays: false);
            var uguiHost = canvasObject.AddComponent<ClientUguiHost>();
            uguiHost.ConfigureBeforeActivation(
                ClientUiRouteId.Settings,
                canvas,
                canvasGroup,
                eventSystem,
                modalButton);

            var inputAsset = CreateInputAsset();
            var hostRoot = CreateHostRoot(
                inputAsset,
                new[] { toolkitHost, modalToolkitHost },
                new[] { uguiHost });
            toolkitObject.SetActive(true);
            modalToolkitObject.SetActive(true);
            canvasObject.SetActive(true);
            hostRoot.gameObject.SetActive(true);
            var panelsReady = Task.WhenAll(toolkitReady.Task, modalToolkitReady.Task);
            while (!panelsReady.IsCompleted)
            {
                yield return null;
            }

            Assert.That(
                panelsReady.IsCompletedSuccessfully,
                Is.True,
                panelsReady.Exception?.ToString());

            Assert.That(toolkitRoot, Is.Not.Null);
            Assert.That(modalToolkitRoot, Is.Not.Null);
            Assert.That(toolkitRoot.style.display.value, Is.EqualTo(DisplayStyle.None));
            Assert.That(toolkitRoot.pickingMode, Is.EqualTo(PickingMode.Ignore));
            Assert.That(toolkitRoot.enabledSelf, Is.False);
            Assert.That(modalToolkitRoot.style.display.value, Is.EqualTo(DisplayStyle.None));
            Assert.That(modalToolkitRoot.pickingMode, Is.EqualTo(PickingMode.Ignore));
            Assert.That(modalToolkitRoot.enabledSelf, Is.False);

            var toolkitDefault = new UnityEngine.UIElements.Button
            {
                name = "DefaultAction",
                focusable = true,
            };
            toolkitRoot.Add(toolkitDefault);
            var modalToolkitDefault = new UnityEngine.UIElements.Button
            {
                name = "ModalAction",
                focusable = true,
            };
            modalToolkitRoot.Add(modalToolkitDefault);

            Assert.That(hostRoot.InitializeAsync(CancellationToken.None).IsCompletedSuccessfully, Is.True);
            var definitions = new[]
            {
                new ClientUiRouteDefinition(
                    ClientUiRouteId.Login,
                    ClientUiFrameworkOwner.UiToolkit,
                    ClientUiLayer.Screen,
                    ClientUiInputMode.Ui,
                    ClientUiLifecycle.Cached),
                new ClientUiRouteDefinition(
                    ClientUiRouteId.Settings,
                    ClientUiFrameworkOwner.Ugui,
                    ClientUiLayer.Overlay,
                    ClientUiInputMode.Ui,
                    ClientUiLifecycle.Cached),
                new ClientUiRouteDefinition(
                    ClientUiRouteId.ConnectionLost,
                    ClientUiFrameworkOwner.UiToolkit,
                    ClientUiLayer.Modal,
                    ClientUiInputMode.Modal,
                    ClientUiLifecycle.Recreate),
            };
            var router = new ClientUiRouter(
                new ClientUiRegistry(definitions, hostRoot.GetHosts()),
                hostRoot,
                maximumQueuedTransitions: 4,
                cleanupTimeout: TestCleanupTimeout);
            Assert.That(router.InitializeAsync(CancellationToken.None).IsCompletedSuccessfully, Is.True);

            var openScreen = router.OpenAsync(ClientUiRouteId.Login, 0, CancellationToken.None);
            Assert.That(openScreen.IsCompletedSuccessfully, Is.True);
            Assert.That(openScreen.Result.IsSuccess, Is.True);
            Assert.That(panelRenderer.sortingOrder, Is.EqualTo((int)ClientUiLayer.Screen));
            Assert.That(toolkitRoot.enabledSelf, Is.True);
            Assert.That(
                toolkitRoot.panel.focusController.focusedElement,
                Is.SameAs(toolkitDefault));

            var openOverlay = router.OpenAsync(ClientUiRouteId.Settings, 0, CancellationToken.None);
            Assert.That(openOverlay.IsCompletedSuccessfully, Is.True);
            Assert.That(openOverlay.Result.IsSuccess, Is.True);
            Assert.That(canvas.sortingOrder, Is.EqualTo((int)ClientUiLayer.Overlay));
            Assert.That(canvasGroup.blocksRaycasts, Is.True);
            Assert.That(eventSystem.currentSelectedGameObject, Is.SameAs(buttonObject));
            Assert.That(toolkitRoot.enabledSelf, Is.False);

            // EventSystem 允许选择任意 GameObject；Host 必须拒绝恢复不含 Selectable 的旧 token。
            eventSystem.SetSelectedGameObject(invalidFocusObject);

            var openModal = router.OpenAsync(ClientUiRouteId.ConnectionLost, 0, CancellationToken.None);
            Assert.That(openModal.IsCompletedSuccessfully, Is.True);
            Assert.That(openModal.Result.IsSuccess, Is.True);
            Assert.That(modalPanelRenderer.sortingOrder, Is.EqualTo((int)ClientUiLayer.Modal));
            Assert.That(modalToolkitRoot.enabledSelf, Is.True);
            Assert.That(
                modalToolkitRoot.panel.focusController.focusedElement,
                Is.SameAs(modalToolkitDefault));
            Assert.That(canvasGroup.blocksRaycasts, Is.False);

            var closeModal = router.CloseAsync(ClientUiRouteId.ConnectionLost, CancellationToken.None);
            Assert.That(closeModal.IsCompletedSuccessfully, Is.True);
            Assert.That(closeModal.Result.IsSuccess, Is.True);
            Assert.That(canvas.enabled, Is.True);
            Assert.That(canvasGroup.blocksRaycasts, Is.True);
            Assert.That(eventSystem.currentSelectedGameObject, Is.SameAs(buttonObject));

            var closeOverlay = router.CloseAsync(ClientUiRouteId.Settings, CancellationToken.None);
            Assert.That(closeOverlay.IsCompletedSuccessfully, Is.True);
            Assert.That(closeOverlay.Result.IsSuccess, Is.True);
            Assert.That(canvas.enabled, Is.False);
            Assert.That(canvasGroup.blocksRaycasts, Is.False);
            Assert.That(toolkitRoot.enabledSelf, Is.True);
            Assert.That(
                toolkitRoot.panel.focusController.focusedElement,
                Is.SameAs(toolkitDefault));

            Assert.That(router.StopAsync(CancellationToken.None).IsCompletedSuccessfully, Is.True);
            Assert.That(hostRoot.StopAsync(CancellationToken.None).IsCompletedSuccessfully, Is.True);
        }

        /// <summary>创建只含产品输入契约所需 action 的最小 Player/UI InputActionAsset 模板。</summary>
        /// <returns>由 teardown 显式销毁的测试资产。</returns>
        private InputActionAsset CreateInputAsset(bool sharedMenuAndCancelBinding = false)
        {
            var inputAsset = ScriptableObject.CreateInstance<InputActionAsset>();
            var player = inputAsset.AddActionMap("Player");
            player.AddAction(
                "Move",
                InputActionType.Value,
                expectedControlLayout: "Vector2");
            player.AddAction(
                "Aim",
                InputActionType.Value,
                expectedControlLayout: "Vector2");
            player.AddAction("Jump", InputActionType.Button);
            player.AddAction("Primary", InputActionType.Button);
            player.AddAction("Secondary", InputActionType.Button);
            player.AddAction("Interact", InputActionType.Button);
            player.AddAction("Menu", InputActionType.Button).AddBinding("<Keyboard>/tab");
            var ui = inputAsset.AddActionMap("UI");
            ui.AddAction("Navigate");
            ui.AddAction("Cancel", InputActionType.Button).AddBinding(
                sharedMenuAndCancelBinding ? "<Keyboard>/tab" : "<Keyboard>/escape");
            _assets.Add(inputAsset);
            return inputAsset;
        }

        /// <summary>向指定虚拟键盘提交 Tab 按下但保持未释放。</summary>
        /// <param name="keyboard">由当前测试独占并在 finally 中移除的虚拟键盘。</param>
        private static void PressTab(Keyboard keyboard)
        {
            InputSystem.QueueStateEvent(keyboard, new KeyboardState(Key.Tab));
            InputSystem.Update();
        }

        /// <summary>向指定虚拟键盘提交全部按键释放。</summary>
        /// <param name="keyboard">由当前测试独占并在 finally 中移除的虚拟键盘。</param>
        private static void ReleaseKeyboard(Keyboard keyboard)
        {
            InputSystem.QueueStateEvent(keyboard, new KeyboardState());
            InputSystem.Update();
        }

        /// <summary>向指定虚拟键盘提交一次完整 Tab 按下与释放。</summary>
        /// <param name="keyboard">由当前测试独占并在 finally 中移除的虚拟键盘。</param>
        private static void PressAndReleaseTab(Keyboard keyboard)
        {
            InputSystem.QueueStateEvent(keyboard, new KeyboardState(Key.Tab));
            InputSystem.Update();
            InputSystem.QueueStateEvent(keyboard, new KeyboardState());
            InputSystem.Update();
        }

        /// <summary>向指定虚拟键盘提交一次完整 Escape 按下与释放。</summary>
        /// <param name="keyboard">由当前测试独占并在 finally 中移除的虚拟键盘。</param>
        private static void PressAndReleaseEscape(Keyboard keyboard)
        {
            InputSystem.QueueStateEvent(keyboard, new KeyboardState(Key.Escape));
            InputSystem.Update();
            InputSystem.QueueStateEvent(keyboard, new KeyboardState());
            InputSystem.Update();
        }

        /// <summary>创建 inactive 且完成直接引用配置的 Host root。</summary>
        /// <param name="inputAsset">测试 Input System 模板。</param>
        /// <param name="toolkitHosts">显式 UI Toolkit Host。</param>
        /// <param name="uguiHosts">显式 uGUI Host。</param>
        /// <returns>尚未初始化的 Host root。</returns>
        private ClientUiHostRoot CreateHostRoot(
            InputActionAsset inputAsset,
            ClientUiToolkitHost[] toolkitHosts = null,
            ClientUguiHost[] uguiHosts = null)
        {
            var gameObject = Track(new GameObject("ClientUiHostRoot"));
            gameObject.SetActive(false);
            var hostRoot = gameObject.AddComponent<ClientUiHostRoot>();
            hostRoot.ConfigureBeforeActivation(
                inputAsset,
                toolkitHosts ?? System.Array.Empty<ClientUiToolkitHost>(),
                uguiHosts ?? System.Array.Empty<ClientUguiHost>());
            return hostRoot;
        }

        /// <summary>登记一个需要 teardown 销毁的测试 GameObject。</summary>
        /// <param name="gameObject">测试对象。</param>
        /// <returns>原对象，便于链式构造 fixture。</returns>
        private GameObject Track(GameObject gameObject)
        {
            _gameObjects.Add(gameObject);
            return gameObject;
        }
    }
}
