using System;
using IHomeland.Client.Core.Configuration;

namespace IHomeland.Client.Infrastructure.Security
{
    /// <summary>
    /// 解析 secure session 的本地 profile namespace。
    /// </summary>
    internal static class ClientSecureSessionProfile
    {
        /// <summary>Production 和普通 Player 使用的唯一 profile。</summary>
        internal const string DefaultProfile = "default";

#if DEVELOPMENT_BUILD || UNITY_EDITOR
        /// <summary>仅Development Player或Editor资格运行识别的隔离参数。</summary>
        private const string DataProfileArgument = "-ihomelandDataProfile";

        /// <summary>仅Development Player或Editor资格运行识别的绝对存储根参数。</summary>
        private const string QualificationStorageRootArgument = "-ihomelandQualificationStorageRoot";
#endif

        /// <summary>解析当前进程允许使用的 profile。</summary>
        /// <param name="environmentKind">已验证环境类别。</param>
        /// <param name="arguments">进程参数快照。</param>
        /// <param name="isDebugBuild">是否为 Editor 或 Development Player。</param>
        /// <returns>Default 或受控资格 profile。</returns>
        /// <exception cref="ArgumentException">Debug 参数缺值、重复或 grammar 非法时抛出。</exception>
        internal static string Resolve(
            ClientEnvironmentKind environmentKind,
            string[] arguments,
            bool isDebugBuild)
        {
#if DEVELOPMENT_BUILD || UNITY_EDITOR
            if (!isDebugBuild || environmentKind == ClientEnvironmentKind.Production)
            {
                return DefaultProfile;
            }

            if (arguments == null)
            {
                throw new ArgumentNullException(nameof(arguments));
            }

            string selected = null;
            for (var index = 0; index < arguments.Length; index++)
            {
                if (!string.Equals(arguments[index], DataProfileArgument, StringComparison.Ordinal))
                {
                    continue;
                }

                if (selected != null || index + 1 >= arguments.Length)
                {
                    throw new ArgumentException("客户端 data profile 参数重复或缺少值。", nameof(arguments));
                }

                selected = arguments[++index];
            }

            if (selected == null)
            {
                return DefaultProfile;
            }

            if (!IsValid(selected))
            {
                throw new ArgumentException("客户端 data profile grammar 无效。", nameof(arguments));
            }

            return selected;
#else
            // Release产物不解析、也不携带资格profile参数词汇。
            return DefaultProfile;
#endif
        }

        /// <summary>解析当前进程允许使用的secure session存储根。</summary>
        /// <param name="environmentKind">已验证环境类别。</param>
        /// <param name="arguments">进程参数快照。</param>
        /// <param name="isDebugBuild">是否为Editor或Development Player。</param>
        /// <param name="defaultRoot">Unity提供的产品默认持久数据根。</param>
        /// <returns>默认根，或资格入口显式提供的绝对run根。</returns>
        /// <exception cref="ArgumentException">资格参数缺值、重复或不是绝对路径时抛出。</exception>
        internal static string ResolveStorageRoot(
            ClientEnvironmentKind environmentKind,
            string[] arguments,
            bool isDebugBuild,
            string defaultRoot)
        {
            if (string.IsNullOrWhiteSpace(defaultRoot))
            {
                throw new ArgumentException("默认持久数据根不能为空。", nameof(defaultRoot));
            }

#if DEVELOPMENT_BUILD || UNITY_EDITOR
            if (!isDebugBuild || environmentKind == ClientEnvironmentKind.Production)
            {
                return defaultRoot;
            }

            if (arguments == null)
            {
                throw new ArgumentNullException(nameof(arguments));
            }

            string selected = null;
            for (var index = 0; index < arguments.Length; index++)
            {
                if (!string.Equals(
                        arguments[index],
                        QualificationStorageRootArgument,
                        StringComparison.Ordinal))
                {
                    continue;
                }

                if (selected != null || index + 1 >= arguments.Length)
                {
                    throw new ArgumentException("资格存储根参数重复或缺少值。", nameof(arguments));
                }

                selected = arguments[++index];
            }

            if (selected == null)
            {
                return defaultRoot;
            }

            if (!System.IO.Path.IsPathFullyQualified(selected))
            {
                throw new ArgumentException("资格存储根必须是绝对路径。", nameof(arguments));
            }

            return System.IO.Path.GetFullPath(selected);
#else
            // Release产物不解析、也不携带资格存储根参数词汇。
            return defaultRoot;
#endif
        }

        /// <summary>验证 profile 只使用有限小写字符集。</summary>
        /// <param name="value">待验证 profile。</param>
        /// <returns>长度为1到32且只含小写字母、数字、连字符时返回true。</returns>
        internal static bool IsValid(string value)
        {
            if (string.IsNullOrEmpty(value) || value.Length > 32)
            {
                return false;
            }

            foreach (var character in value)
            {
                if (!((character >= 'a' && character <= 'z') ||
                      (character >= '0' && character <= '9') ||
                      character == '-'))
                {
                    return false;
                }
            }

            return true;
        }
    }
}
