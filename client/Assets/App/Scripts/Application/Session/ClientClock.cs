using System;

namespace IHomeland.Client.Application.Session
{
    /// <summary>
    /// 定义 Session owner 判断绝对 credential expiry 的可替换 UTC 时钟。
    /// </summary>
    internal interface IClientClock
    {
        /// <summary>
        /// 获取当前 Unix 时间，单位为毫秒。
        /// </summary>
        long UtcNowMilliseconds { get; }
    }

    /// <summary>
    /// 使用系统 UTC 时间提供 production credential expiry 判断。
    /// </summary>
    internal sealed class SystemClientClock : IClientClock
    {
        /// <inheritdoc />
        public long UtcNowMilliseconds => DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
    }
}
