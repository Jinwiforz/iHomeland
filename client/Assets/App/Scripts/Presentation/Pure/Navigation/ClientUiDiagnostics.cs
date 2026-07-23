using System.Diagnostics;
using System.Threading;

namespace IHomeland.Client.Presentation.Navigation
{
    /// <summary>为 Development Build 与 Editor 提供有界、低敏的客户端 UI 事件诊断。</summary>
    /// <remarks>
    /// 只记录 route、输入模式、generation 与生命周期边界，不记录账号、credential、ticket 或业务 payload。
    /// Release Player 会在编译期移除调用，避免把临时诊断成本带入正式包。
    /// </remarks>
    internal static class ClientUiDiagnostics
    {
        /// <summary>保存当前进程内单调递增的诊断事件序号。</summary>
        private static long _sequence;

        /// <summary>记录一个事件驱动的 UI 状态边界。</summary>
        /// <param name="component">产生事件的稳定组件名。</param>
        /// <param name="operation">稳定操作名。</param>
        /// <param name="detail">不得包含 credential、ticket 或业务 payload 的诊断字段。</param>
        [Conditional("UNITY_EDITOR")]
        [Conditional("DEVELOPMENT_BUILD")]
        internal static void Trace(string component, string operation, string detail)
        {
            var sequence = Interlocked.Increment(ref _sequence);
            System.Diagnostics.Trace.WriteLine(
                $"[IHOMELAND_UI] sequence={sequence} " +
                $"thread={System.Environment.CurrentManagedThreadId} component={component} " +
                $"operation={operation} {detail}");
        }
    }
}
