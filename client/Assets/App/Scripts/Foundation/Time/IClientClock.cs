namespace IHomeland.Client.Foundation.Time
{
    /// <summary>
    /// 定义不依赖 Unity、业务模块或 transport 的 UTC 时钟边界。
    /// </summary>
    internal interface IClientClock
    {
        /// <summary>
        /// 获取当前 Unix 时间，单位为毫秒。
        /// </summary>
        long UtcNowMilliseconds { get; }
    }
}
