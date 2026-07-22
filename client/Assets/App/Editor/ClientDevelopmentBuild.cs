using System;
using System.IO;
using System.Linq;
using UnityEditor;
using UnityEditor.Build.Reporting;

namespace IHomeland.Client.Editor
{
    /// <summary>提供Development与Release共用校验和输出规则的唯一Windows Player构建owner。</summary>
    internal static class ClientDevelopmentBuild
    {
        /// <summary>定义所有Windows Player必须使用的公司身份。</summary>
        private const string ExpectedCompanyName = "Jinwiforz";

        /// <summary>定义所有Windows Player必须使用的产品身份。</summary>
        private const string ExpectedProductName = "iHomeland";

        /// <summary>定义Windows Player唯一允许的有序场景基线。</summary>
        private static readonly string[] ExpectedScenes =
        {
            "Assets/App/Scenes/BootstrapScene.unity",
            "Assets/App/Scenes/PersonalWorldScene.unity",
        };

        /// <summary>从启用的 Build Settings 场景构建 Windows 64-bit Development Player。</summary>
        /// <exception cref="InvalidOperationException">缺少输出参数、启用场景或构建失败时抛出。</exception>
        public static void BuildWindowsDevelopment()
        {
            BuildWindows(BuildOptions.Development);
        }

        /// <summary>从同一启用场景与product identity构建Windows 64-bit Release Player。</summary>
        /// <exception cref="InvalidOperationException">缺少输出参数、启用场景或构建失败时抛出。</exception>
        public static void BuildWindowsRelease()
        {
            BuildWindows(BuildOptions.None);
        }

        /// <summary>执行两种profile共享的场景、输出和结果校验。</summary>
        /// <param name="options">只允许None或Development。</param>
        private static void BuildWindows(BuildOptions options)
        {
            if (options != BuildOptions.None && options != BuildOptions.Development)
            {
                throw new ArgumentOutOfRangeException(nameof(options));
            }

            var output = ReadArgument("-ihomelandBuildOutput");
            if (string.IsNullOrWhiteSpace(output))
            {
                throw new InvalidOperationException("命令行缺少 -ihomelandBuildOutput。");
            }

            var scenes = EditorBuildSettings.scenes
                .Where(scene => scene.enabled)
                .Select(scene => scene.path)
                .ToArray();
            ValidateProductBaseline(scenes);

            var fullOutput = Path.GetFullPath(output);
            var executablePath = string.Equals(
                Path.GetExtension(fullOutput),
                ".exe",
                StringComparison.OrdinalIgnoreCase)
                ? fullOutput
                : Path.Combine(fullOutput, "iHomeland.exe");
            Directory.CreateDirectory(Path.GetDirectoryName(executablePath) ??
                                      throw new InvalidOperationException("构建输出目录非法。"));
            var report = BuildPipeline.BuildPlayer(
                scenes,
                executablePath,
                BuildTarget.StandaloneWindows64,
                options);
            if (report.summary.result != BuildResult.Succeeded)
            {
                throw new InvalidOperationException(
                    $"Windows Player构建失败：{report.summary.result}。");
            }
        }

        /// <summary>在进入BuildPipeline前验证两种profile共用的产品身份与场景顺序。</summary>
        /// <param name="scenes">从Build Settings读取的启用场景有序快照。</param>
        /// <exception cref="InvalidOperationException">产品身份、版本或场景基线漂移时抛出。</exception>
        private static void ValidateProductBaseline(string[] scenes)
        {
            if (!string.Equals(
                    PlayerSettings.companyName,
                    ExpectedCompanyName,
                    StringComparison.Ordinal) ||
                !string.Equals(
                    PlayerSettings.productName,
                    ExpectedProductName,
                    StringComparison.Ordinal) ||
                string.IsNullOrWhiteSpace(PlayerSettings.bundleVersion))
            {
                throw new InvalidOperationException("Windows Player产品身份基线无效。");
            }

            if (!scenes.SequenceEqual(ExpectedScenes, StringComparer.Ordinal))
            {
                throw new InvalidOperationException("Windows Player启用场景基线无效。");
            }
        }

        /// <summary>读取紧随指定名称之后的命令行参数值。</summary>
        /// <param name="name">包含前导连字符的参数名。</param>
        /// <returns>参数值；不存在时返回空字符串。</returns>
        private static string ReadArgument(string name)
        {
            var arguments = Environment.GetCommandLineArgs();
            for (var index = 0; index < arguments.Length - 1; index++)
            {
                if (string.Equals(arguments[index], name, StringComparison.Ordinal))
                {
                    return arguments[index + 1];
                }
            }

            return string.Empty;
        }
    }
}
