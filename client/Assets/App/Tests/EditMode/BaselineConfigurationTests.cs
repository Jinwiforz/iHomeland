using System.IO;
using System.Linq;
using System.Text.RegularExpressions;
using IHomeland.Client.Core.Bootstrap;
using IHomeland.Client.Presentation.Hosts;
using IHomeland.Client.Presentation.Hosts.UGUI;
using IHomeland.Client.Presentation.Hosts.UIToolkit;
using IHomeland.Client.Scenes.PersonalWorld;
using NUnit.Framework;
using UnityEditor;
using UnityEditor.SceneManagement;
using UnityEngine;
using UnityEngine.EventSystems;
using UnityEngine.InputSystem;
using UnityEngine.SceneManagement;
using UnityEngine.UIElements;

namespace IHomeland.Client.Tests.EditMode
{
    /// <summary>
    /// 验证 Unity Editor、Package、项目标识、启动场景和资源边界没有偏离 C0 基线。
    /// </summary>
    public sealed class BaselineConfigurationTests
    {
        /// <summary>
        /// 定义 BootstrapScene 在 Unity 工程内的稳定路径。
        /// </summary>
        private const string BootstrapScenePath = "Assets/App/Scenes/BootstrapScene.unity";

        /// <summary>
        /// 定义首期个人世界内容 Scene 在 Unity 工程内的稳定路径。
        /// </summary>
        private const string PersonalWorldScenePath = "Assets/App/Scenes/PersonalWorldScene.unity";

        /// <summary>
        /// 定义首期 UI Toolkit 页面共享样式表的稳定路径。
        /// </summary>
        private const string PersonalWorldThemePath =
            "Assets/App/UI/PersonalWorld/PersonalWorldTheme.uss";

        /// <summary>
        /// 定义首期 WorldHud Prefab 的稳定路径。
        /// </summary>
        private const string WorldHudPrefabPath =
            "Assets/App/UI/PersonalWorld/Prefabs/WorldHud.prefab";

        /// <summary>
        /// 定义唯一项目 Input System 资产的稳定路径。
        /// </summary>
        private const string InputActionsPath = "Assets/InputSystem_Actions.inputactions";

        /// <summary>
        /// 定义 BootstrapScene 直接引用的 UI Toolkit 页面资产。
        /// </summary>
        private static readonly string[] ProductUxmlPaths =
        {
            "Assets/App/UI/PersonalWorld/LoginView.uxml",
            "Assets/App/UI/PersonalWorld/ShellView.uxml",
            "Assets/App/UI/PersonalWorld/WorldVisitView.uxml",
            "Assets/App/UI/PersonalWorld/ConnectionLostView.uxml",
        };

        /// <summary>
        /// 定义 Windows Player 使用的公司名称。
        /// </summary>
        private const string ExpectedCompanyName = "Jinwiforz";

        /// <summary>
        /// 定义 Windows Player 使用的产品名称。
        /// </summary>
        private const string ExpectedProductName = "iHomeland";

        /// <summary>
        /// 从 versions.yaml 提取 Unity 版本，确保唯一版本 owner 与生态声明一致。
        /// </summary>
        [Test]
        public void ProjectVersionMatchesVersionCatalog()
        {
            var versionCatalog = File.ReadAllText(Path.Combine(RepositoryRoot, "versions.yaml"));
            var match = Regex.Match(
                versionCatalog,
                @"client:\s*\r?\n\s+unity:\s*\r?\n\s+version:\s*""([^""]+)""",
                RegexOptions.CultureInvariant);

            Assert.That(match.Success, Is.True, "versions.yaml 缺少 client.unity.version。");
            Assert.That(UnityEngine.Application.unityVersion, Is.EqualTo(match.Groups[1].Value));

            var projectVersion = File.ReadAllText(
                Path.Combine(ClientRoot, "ProjectSettings", "ProjectVersion.txt"));
            Assert.That(projectVersion, Does.Contain($"m_EditorVersion: {match.Groups[1].Value}"));
        }

        /// <summary>
        /// 验证 Package 源和 lock 同时存在必要能力，且 C0 未提前安装 Addressables。
        /// </summary>
        [Test]
        public void PackageBaselineContainsRequiredClientFoundations()
        {
            var manifest = File.ReadAllText(Path.Combine(ClientRoot, "Packages", "manifest.json"));
            var packageLock = File.ReadAllText(Path.Combine(ClientRoot, "Packages", "packages-lock.json"));
            string[] requiredPackages =
            {
                "com.unity.inputsystem",
                "com.unity.render-pipelines.universal",
                "com.unity.test-framework",
                "com.unity.ugui",
            };

            foreach (var package in requiredPackages)
            {
                Assert.That(manifest, Does.Contain($"\"{package}\""), $"manifest 缺少 {package}。");
                Assert.That(packageLock, Does.Contain($"\"{package}\""), $"packages-lock 缺少 {package}。");
            }

            Assert.That(manifest, Does.Not.Contain("com.unity.addressables"));
            Assert.That(manifest, Does.Not.Contain("com.cysharp.unitask"));
            Assert.That(manifest, Does.Not.Contain("zenject"));
            Assert.That(manifest, Does.Not.Contain("vcontainer"));
        }

        /// <summary>
        /// 验证 Windows Player 使用稳定项目身份而非 Unity 模板名称。
        /// </summary>
        [Test]
        public void WindowsPlayerIdentityUsesProjectNames()
        {
            Assert.That(PlayerSettings.companyName, Is.EqualTo(ExpectedCompanyName));
            Assert.That(PlayerSettings.productName, Is.EqualTo(ExpectedProductName));
        }

        /// <summary>
        /// 验证 C0 未绑定 Unity Cloud Project，避免构建或运行时隐式访问未使用的 Unity Services。
        /// </summary>
        [Test]
        public void UnityServicesRemainDisconnectedForC0()
        {
            var projectSettings = File.ReadAllText(
                Path.Combine(ClientRoot, "ProjectSettings", "ProjectSettings.asset"));
            var connectSettings = File.ReadAllText(
                Path.Combine(ClientRoot, "ProjectSettings", "UnityConnectSettings.asset"));

            Assert.That(
                projectSettings,
                Does.Match(@"(?m)^  cloudProjectId:\s*$"),
                "C0 不应绑定 Unity Cloud Project。");
            Assert.That(
                connectSettings,
                Does.Match(@"(?m)^  m_Enabled: 0\s*$"),
                "C0 应保持 Unity Services 总开关关闭。");
        }

        /// <summary>
        /// 验证 BootstrapScene 位于 index 0，PersonalWorldScene 位于 index 1，且没有额外启用 Scene。
        /// </summary>
        [Test]
        public void BuildScenesKeepBootstrapFirstAndPersonalWorldSecond()
        {
            var enabledScenes = EditorBuildSettings.scenes.Where(scene => scene.enabled).ToArray();

            Assert.That(enabledScenes, Has.Length.EqualTo(2));
            Assert.That(enabledScenes[0].path, Is.EqualTo(BootstrapScenePath));
            Assert.That(enabledScenes[1].path, Is.EqualTo(PersonalWorldScenePath));
        }

        /// <summary>
        /// 验证 Gameplay 菜单使用独立 Player/Menu action，并同时支持键盘与手柄标准菜单键。
        /// </summary>
        [Test]
        public void GameplayMenuAndUiCancelUseDedicatedActionContracts()
        {
            var inputActions = AssetDatabase.LoadAssetAtPath<InputActionAsset>(InputActionsPath);
            Assert.That(inputActions, Is.Not.Null, $"缺少 {InputActionsPath}。");

            var player = inputActions.FindActionMap("Player", throwIfNotFound: true);
            var menu = player.FindAction("Menu", throwIfNotFound: true);
            Assert.That(menu.type, Is.EqualTo(InputActionType.Button));
            Assert.That(menu.interactions, Is.Empty, "Player/Menu 不应继承 Hold 等持续交互。");

            var bindingPaths = menu.bindings
                .Where(binding => !binding.isComposite && !binding.isPartOfComposite)
                .Select(binding => binding.path)
                .ToArray();
            Assert.That(bindingPaths, Does.Contain("<Keyboard>/tab"));
            Assert.That(bindingPaths, Does.Contain("<Gamepad>/start"));

            var ui = inputActions.FindActionMap("UI", throwIfNotFound: true);
            var cancel = ui.FindAction("Cancel", throwIfNotFound: true);
            Assert.That(cancel.type, Is.EqualTo(InputActionType.Button));
            Assert.That(
                cancel.bindings.Select(binding => binding.path),
                Does.Contain("*/{Cancel}"),
                "UI/Cancel 应继续使用 Input System 的跨设备标准取消控件。");
        }

        /// <summary>
        /// 验证产品 UXML、共享 USS 与 WorldHud Prefab 都有稳定 GUID，并由 BootstrapScene 直接依赖。
        /// </summary>
        [Test]
        public void ProductAssetsAreLoadableAndTrackedByDirectReferences()
        {
            AssertTrackedAsset<StyleSheet>(PersonalWorldThemePath);
            AssertTrackedAsset<GameObject>(WorldHudPrefabPath);

            var bootstrapDependencies = AssetDatabase.GetDependencies(BootstrapScenePath, true);
            Assert.That(bootstrapDependencies, Does.Contain(WorldHudPrefabPath));
            foreach (var uxmlPath in ProductUxmlPaths)
            {
                AssertTrackedAsset<VisualTreeAsset>(uxmlPath);
                Assert.That(bootstrapDependencies, Does.Contain(uxmlPath));
                Assert.That(
                    AssetDatabase.GetDependencies(uxmlPath, true),
                    Does.Contain(PersonalWorldThemePath),
                    $"{uxmlPath} 缺少共享 USS 直接引用。");
            }
        }

        /// <summary>
        /// 验证 BootstrapScene 与 PersonalWorldScene 的应用根、Host、EventSystem 和 Context 唯一性。
        /// </summary>
        [Test]
        public void ProductScenesKeepSingleOwnersAndCompleteHostReferences()
        {
            var bootstrapScene = OpenSceneForValidation(BootstrapScenePath, out var closeBootstrapScene);
            var personalWorldScene = OpenSceneForValidation(
                PersonalWorldScenePath,
                out var closePersonalWorldScene);
            try
            {
                var appRoots = GetComponentsInScene<AppRoot>(bootstrapScene);
                var hostRoots = GetComponentsInScene<ClientUiHostRoot>(bootstrapScene);
                var eventSystems = GetComponentsInScene<EventSystem>(bootstrapScene);
                var toolkitHosts = GetComponentsInScene<ClientUiToolkitHost>(bootstrapScene);
                var uguiHosts = GetComponentsInScene<ClientUguiHost>(bootstrapScene);

                Assert.That(appRoots, Has.Length.EqualTo(1));
                Assert.That(hostRoots, Has.Length.EqualTo(1));
                Assert.That(eventSystems, Has.Length.EqualTo(1));
                Assert.That(toolkitHosts, Has.Length.EqualTo(4));
                Assert.That(uguiHosts, Has.Length.EqualTo(1));
                hostRoots[0].ValidateConfiguration();

                foreach (var toolkitHost in toolkitHosts)
                {
                    var serializedHost = new SerializedObject(toolkitHost);
                    Assert.That(
                        serializedHost.FindProperty("_panelRenderer").objectReferenceValue,
                        Is.Not.Null,
                        $"{toolkitHost.name} 缺少 PanelRenderer 直接引用。");
                    Assert.That(
                        serializedHost.FindProperty("_productBindingComponent").objectReferenceValue,
                        Is.Not.Null,
                        $"{toolkitHost.name} 缺少产品 binding 直接引用。");
                }

                var serializedUguiHost = new SerializedObject(uguiHosts[0]);
                Assert.That(
                    serializedUguiHost.FindProperty("_eventSystem").objectReferenceValue,
                    Is.SameAs(eventSystems[0]),
                    "WorldHud 必须引用 BootstrapScene 唯一 EventSystem。");
                Assert.That(
                    serializedUguiHost.FindProperty("_productBindingComponent").objectReferenceValue,
                    Is.Not.Null,
                    "WorldHud 缺少产品 binding 直接引用。");

                Assert.That(GetComponentsInScene<AppRoot>(personalWorldScene), Is.Empty);
                var contexts = GetComponentsInScene<PersonalWorldSceneContext>(personalWorldScene);
                Assert.That(contexts, Has.Length.EqualTo(1));
                contexts[0].ValidateConfiguration();
                var referenceEnvironments =
                    GetComponentsInScene<PersonalWorldReferenceEnvironment>(
                        personalWorldScene);
                Assert.That(referenceEnvironments, Has.Length.EqualTo(1));
                Assert.That(
                    referenceEnvironments[0].
                        GetComponentsInChildren<Collider>(includeInactive: true),
                    Is.Empty,
                    "PersonalWorld世界参照不得创建客户端Collider事实。");

                var thirdPersonFollows =
                    GetComponentsInScene<Component>(personalWorldScene)
                        .Where(component =>
                            component.GetType().FullName ==
                            "Unity.Cinemachine.CinemachineThirdPersonFollow")
                        .ToArray();
                Assert.That(thirdPersonFollows, Has.Length.EqualTo(4));
                var explorationFollow = thirdPersonFollows.Single(
                    follow => follow.gameObject.name == "ExplorationRig");
                var serializedExploration =
                    new SerializedObject(explorationFollow);
                Assert.That(
                    serializedExploration.FindProperty("CameraDistance")
                        .floatValue,
                    Is.EqualTo(4.5f).Within(0.001f));
                Assert.That(
                    serializedExploration.FindProperty("ShoulderOffset")
                        .vector3Value.y,
                    Is.EqualTo(1.25f).Within(0.001f));
                Assert.That(
                    serializedExploration.FindProperty("VerticalArmLength")
                        .floatValue,
                    Is.EqualTo(0.35f).Within(0.001f));
                Assert.That(
                    serializedExploration.FindProperty("Damping")
                        .vector3Value.y,
                    Is.EqualTo(0.1f).Within(0.001f));
                Assert.That(
                    thirdPersonFollows.All(
                        follow =>
                        {
                            var serializedFollow =
                                new SerializedObject(follow);
                            return
                                serializedFollow
                                    .FindProperty("CameraDistance")
                                    .floatValue >= 3.2f &&
                                serializedFollow
                                    .FindProperty("ShoulderOffset")
                                    .vector3Value.y >= 1.15f &&
                                serializedFollow
                                    .FindProperty("Damping")
                                    .vector3Value.y <= 0.15f;
                        }),
                    Is.True,
                    "Battle camera rigs 必须保持可读距离、高位肩点与低垂直阻尼。");
            }
            finally
            {
                if (closePersonalWorldScene)
                {
                    EditorSceneManager.CloseScene(personalWorldScene, removeScene: true);
                }

                if (closeBootstrapScene)
                {
                    EditorSceneManager.CloseScene(bootstrapScene, removeScene: true);
                }
            }
        }

        /// <summary>
        /// 验证 Unity 源资产保持 ForceText，且项目自有 App 边界没有 Resources 核心目录。
        /// </summary>
        [Test]
        public void AssetSerializationAndResourceBoundaryStayExplicit()
        {
            Assert.That(EditorSettings.serializationMode, Is.EqualTo(SerializationMode.ForceText));

            var resourcesDirectories = Directory.GetDirectories(
                Path.Combine(UnityEngine.Application.dataPath, "App"),
                "Resources",
                SearchOption.AllDirectories);
            Assert.That(resourcesDirectories, Is.Empty);
        }

        /// <summary>
        /// 验证仓库忽略 generated C# 和 Unity 本地状态，但不忽略正常源资产。
        /// </summary>
        [Test]
        public void RepositoryIgnoreRulesProtectGeneratedAndLocalState()
        {
            var gitIgnore = File.ReadAllText(Path.Combine(RepositoryRoot, ".gitignore"));

            Assert.That(gitIgnore, Does.Contain("/client/Assets/App/Generated/"));
            Assert.That(gitIgnore, Does.Contain("/client/[Ll]ibrary/"));
            Assert.That(gitIgnore, Does.Contain("/client/[Tt]emp/"));
            Assert.That(gitIgnore, Does.Contain("/client/[Uu]ser[Ss]ettings/"));
            Assert.That(gitIgnore, Does.Not.Contain("/client/Assets/App/Scripts/"));
            Assert.That(gitIgnore, Does.Not.Contain("/client/Assets/App/Scenes/"));
        }

        /// <summary>
        /// 验证资产可按期望类型加载且具有由 `.meta` 持有的稳定 GUID。
        /// </summary>
        /// <typeparam name="T">期望的 Unity 资产类型。</typeparam>
        /// <param name="assetPath">相对 Unity 工程的稳定资产路径。</param>
        private static void AssertTrackedAsset<T>(string assetPath)
            where T : UnityEngine.Object
        {
            Assert.That(
                AssetDatabase.AssetPathToGUID(assetPath),
                Is.Not.Empty,
                $"{assetPath} 缺少稳定 GUID 或 `.meta`。");
            Assert.That(
                AssetDatabase.LoadAssetAtPath<T>(assetPath),
                Is.Not.Null,
                $"{assetPath} 缺失或类型不是 {typeof(T).Name}。");
        }

        /// <summary>
        /// 取得已加载 Scene，或以 additive 方式临时打开并返回需要关闭的所有权标记。
        /// </summary>
        /// <param name="scenePath">登记的 Unity Scene 资产路径。</param>
        /// <param name="mustClose">测试是否拥有本次临时打开的 Scene。</param>
        /// <returns>已加载且可读取 root objects 的 Scene。</returns>
        private static Scene OpenSceneForValidation(string scenePath, out bool mustClose)
        {
            var scene = SceneManager.GetSceneByPath(scenePath);
            if (scene.IsValid() && scene.isLoaded)
            {
                mustClose = false;
                return scene;
            }

            mustClose = true;
            return EditorSceneManager.OpenScene(scenePath, OpenSceneMode.Additive);
        }

        /// <summary>
        /// 只在给定 Scene 的 root hierarchy 内收集组件，不执行跨 Scene 全局扫描。
        /// </summary>
        /// <typeparam name="T">待收集的 Unity Component 类型。</typeparam>
        /// <param name="scene">已加载的目标 Scene。</param>
        /// <returns>包含 inactive GameObject 的确定性组件数组。</returns>
        private static T[] GetComponentsInScene<T>(Scene scene)
            where T : Component
        {
            return scene.GetRootGameObjects()
                .SelectMany(root => root.GetComponentsInChildren<T>(includeInactive: true))
                .ToArray();
        }

        /// <summary>
        /// 获取 Unity 工程根目录的规范绝对路径。
        /// </summary>
        private static string ClientRoot => Directory.GetParent(UnityEngine.Application.dataPath)?.FullName ??
            throw new DirectoryNotFoundException("无法从 Application.dataPath 解析 Unity 工程根目录。");

        /// <summary>
        /// 获取包含 versions.yaml 和 .gitignore 的仓库根目录。
        /// </summary>
        private static string RepositoryRoot => Directory.GetParent(ClientRoot)?.FullName ??
            throw new DirectoryNotFoundException("无法从 Unity 工程根目录解析仓库根目录。");
    }
}
