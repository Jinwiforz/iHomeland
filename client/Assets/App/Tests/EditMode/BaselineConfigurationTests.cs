using System.IO;
using System.Linq;
using System.Text.RegularExpressions;
using NUnit.Framework;
using UnityEditor;
using UnityEngine;

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
            Assert.That(Application.unityVersion, Is.EqualTo(match.Groups[1].Value));

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
        /// 验证 BootstrapScene 是唯一启用且位于索引零的 Player 启动场景。
        /// </summary>
        [Test]
        public void BootstrapSceneIsTheOnlyEnabledBuildScene()
        {
            var enabledScenes = EditorBuildSettings.scenes.Where(scene => scene.enabled).ToArray();

            Assert.That(enabledScenes, Has.Length.EqualTo(1));
            Assert.That(enabledScenes[0].path, Is.EqualTo(BootstrapScenePath));
        }

        /// <summary>
        /// 验证 Unity 源资产保持 ForceText，且 C0 没有 Resources 核心目录。
        /// </summary>
        [Test]
        public void AssetSerializationAndResourceBoundaryStayExplicit()
        {
            Assert.That(EditorSettings.serializationMode, Is.EqualTo(SerializationMode.ForceText));

            var resourcesDirectories = Directory.GetDirectories(
                Application.dataPath,
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
        /// 获取 Unity 工程根目录的规范绝对路径。
        /// </summary>
        private static string ClientRoot => Directory.GetParent(Application.dataPath)?.FullName ??
            throw new DirectoryNotFoundException("无法从 Application.dataPath 解析 Unity 工程根目录。");

        /// <summary>
        /// 获取包含 versions.yaml 和 .gitignore 的仓库根目录。
        /// </summary>
        private static string RepositoryRoot => Directory.GetParent(ClientRoot)?.FullName ??
            throw new DirectoryNotFoundException("无法从 Unity 工程根目录解析仓库根目录。");
    }
}
