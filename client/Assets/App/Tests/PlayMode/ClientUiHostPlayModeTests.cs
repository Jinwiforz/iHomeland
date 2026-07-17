using System.Collections;
using System.Collections.Generic;
using System.Threading;
using IHomeland.Client.Presentation.Hosts;
using IHomeland.Client.Presentation.Hosts.UGUI;
using IHomeland.Client.Presentation.Hosts.UIToolkit;
using IHomeland.Client.Presentation.Navigation;
using NUnit.Framework;
using UnityEngine;
using UnityEngine.EventSystems;
using UnityEngine.InputSystem;
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

        /// <summary>保存每个测试创建且需要延迟销毁的 GameObject。</summary>
        private readonly List<GameObject> _gameObjects = new List<GameObject>();

        /// <summary>保存每个测试创建且需要显式销毁的 ScriptableObject。</summary>
        private readonly List<ScriptableObject> _assets = new List<ScriptableObject>();

        /// <summary>
        /// 每个测试后销毁全部程序化对象并恢复 cursor baseline，防止跨测试残留 owner。
        /// </summary>
        /// <returns>等待 Unity 完成延迟销毁的枚举器。</returns>
        [UnityTearDown]
        public IEnumerator TearDownFixtures()
        {
            foreach (var gameObject in _gameObjects)
            {
                if (gameObject != null)
                {
                    Object.Destroy(gameObject);
                }
            }

            foreach (var asset in _assets)
            {
                if (asset != null)
                {
                    Object.Destroy(asset);
                }
            }

            _gameObjects.Clear();
            _assets.Clear();
            UnityCursor.visible = true;
            UnityCursor.lockState = CursorLockMode.None;
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
            Assert.That(UnityCursor.lockState, Is.EqualTo(CursorLockMode.Locked));

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
        /// 保护 UI Toolkit screen 与 uGUI modal 共享 route、input、layer、raycast 和 focus 恢复语义。
        /// </summary>
        /// <returns>等待 UIDocument panel 建立并执行完整 route transaction 的枚举器。</returns>
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
            var document = toolkitObject.AddComponent<UIDocument>();
            document.panelSettings = panelSettings;
            var toolkitHost = toolkitObject.AddComponent<ClientUiToolkitHost>();
            toolkitHost.ConfigureBeforeActivation(ClientUiRouteId.Login, document, "DefaultAction");

            var modalToolkitObject = Track(new GameObject("ToolkitModal"));
            modalToolkitObject.SetActive(false);
            var modalDocument = modalToolkitObject.AddComponent<UIDocument>();
            modalDocument.panelSettings = panelSettings;
            var modalToolkitHost = modalToolkitObject.AddComponent<ClientUiToolkitHost>();
            modalToolkitHost.ConfigureBeforeActivation(
                ClientUiRouteId.ConnectionLost,
                modalDocument,
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
            yield return null;

            var toolkitDefault = new UnityEngine.UIElements.Button
            {
                name = "DefaultAction",
                focusable = true,
            };
            document.rootVisualElement.Add(toolkitDefault);
            var modalToolkitDefault = new UnityEngine.UIElements.Button
            {
                name = "ModalAction",
                focusable = true,
            };
            modalDocument.rootVisualElement.Add(modalToolkitDefault);

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
            Assert.That(document.sortingOrder, Is.EqualTo((int)ClientUiLayer.Screen));
            Assert.That(document.rootVisualElement.enabledSelf, Is.True);
            Assert.That(
                document.rootVisualElement.panel.focusController.focusedElement,
                Is.SameAs(toolkitDefault));

            var openOverlay = router.OpenAsync(ClientUiRouteId.Settings, 0, CancellationToken.None);
            Assert.That(openOverlay.IsCompletedSuccessfully, Is.True);
            Assert.That(openOverlay.Result.IsSuccess, Is.True);
            Assert.That(canvas.sortingOrder, Is.EqualTo((int)ClientUiLayer.Overlay));
            Assert.That(canvasGroup.blocksRaycasts, Is.True);
            Assert.That(eventSystem.currentSelectedGameObject, Is.SameAs(buttonObject));
            Assert.That(document.rootVisualElement.enabledSelf, Is.False);

            // EventSystem 允许选择任意 GameObject；Host 必须拒绝恢复不含 Selectable 的旧 token。
            eventSystem.SetSelectedGameObject(invalidFocusObject);

            var openModal = router.OpenAsync(ClientUiRouteId.ConnectionLost, 0, CancellationToken.None);
            Assert.That(openModal.IsCompletedSuccessfully, Is.True);
            Assert.That(openModal.Result.IsSuccess, Is.True);
            Assert.That(modalDocument.sortingOrder, Is.EqualTo((int)ClientUiLayer.Modal));
            Assert.That(modalDocument.rootVisualElement.enabledSelf, Is.True);
            Assert.That(
                modalDocument.rootVisualElement.panel.focusController.focusedElement,
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
            Assert.That(document.rootVisualElement.enabledSelf, Is.True);
            Assert.That(
                document.rootVisualElement.panel.focusController.focusedElement,
                Is.SameAs(toolkitDefault));

            Assert.That(router.StopAsync(CancellationToken.None).IsCompletedSuccessfully, Is.True);
            Assert.That(hostRoot.StopAsync(CancellationToken.None).IsCompletedSuccessfully, Is.True);
        }

        /// <summary>创建不含 binding 的最小 Player/UI InputActionAsset 模板。</summary>
        /// <returns>由 teardown 显式销毁的测试资产。</returns>
        private InputActionAsset CreateInputAsset()
        {
            var inputAsset = ScriptableObject.CreateInstance<InputActionAsset>();
            inputAsset.AddActionMap("Player").AddAction("Move");
            inputAsset.AddActionMap("UI").AddAction("Navigate");
            _assets.Add(inputAsset);
            return inputAsset;
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
