namespace IHomeland.Client.Core.Configuration
{
    /// <summary>
    /// 集中保存由当前客户端构建锁定、且不能由部署资产改写的契约基线。
    /// </summary>
    internal static class ClientContractBaseline
    {
        /// <summary>
        /// 获取当前客户端支持的实时与 HTTP 公共协议版本。
        /// </summary>
        internal const int ProtocolVersion = 1;
    }
}
