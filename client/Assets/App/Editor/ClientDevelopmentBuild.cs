using System;
using System.IO;
using System.Linq;
using UnityEditor;
using UnityEditor.Build.Reporting;

namespace IHomeland.Client.Editor
{
    /// <summary>提供可由命令行重复执行的 Windows Development Player 构建入口。</summary>
    internal static class ClientDevelopmentBuild
    {
        /// <summary>从启用的 Build Settings 场景构建 Windows 64-bit Development Player。</summary>
        /// <exception cref="InvalidOperationException">缺少输出参数、启用场景或构建失败时抛出。</exception>
        public static void BuildWindowsDevelopment()
        {
            var output = ReadArgument("-ihomelandBuildOutput");
            if (string.IsNullOrWhiteSpace(output))
            {
                throw new InvalidOperationException("命令行缺少 -ihomelandBuildOutput。");
            }

            var scenes = EditorBuildSettings.scenes
                .Where(scene => scene.enabled)
                .Select(scene => scene.path)
                .ToArray();
            if (scenes.Length == 0)
            {
                throw new InvalidOperationException("Windows Development Build 缺少启用场景。");
            }

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
                BuildOptions.Development);
            if (report.summary.result != BuildResult.Succeeded)
            {
                throw new InvalidOperationException(
                    $"Windows Development Build 失败：{report.summary.result}。");
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
