using System;

namespace IHomeland.Client.Networking.Infrastructure.Http
{
    /// <summary>
    /// 表示远端 JSON、status 或 contract projection 无法通过冻结协议验证。
    /// </summary>
    /// <remarks>
    /// 消息只描述字段与约束，不包含响应原文、输入值或 credential。HTTP 边界必须捕获该异常并
    /// 映射为 MalformedResponse，不能把内部 JSON exception 直接交给 UI。
    /// </remarks>
    internal sealed class ClientHttpContractException : Exception
    {
        /// <summary>
        /// 创建包含安全约束说明的 contract exception。
        /// </summary>
        /// <param name="message">不含远端原始值的稳定失败说明。</param>
        internal ClientHttpContractException(string message)
            : base(message)
        {
        }

        /// <summary>
        /// 创建包装内部解析 cause 的 contract exception。
        /// </summary>
        /// <param name="message">不含远端原始值的稳定失败说明。</param>
        /// <param name="innerException">仅供内部诊断且不得跨 HTTP 边界暴露的解析异常。</param>
        internal ClientHttpContractException(string message, Exception innerException)
            : base(message, innerException)
        {
        }
    }
}
