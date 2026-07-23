using System;
using IHomeland.Client.Foundation.Time;

namespace IHomeland.Client.Infrastructure.Time
{
    /// <summary>
    /// 使用系统 UTC 时间实现客户端绝对 deadline 与 expiry 判断。
    /// </summary>
    internal sealed class SystemClientClock : IClientClock
    {
        /// <inheritdoc />
        public long UtcNowMilliseconds => DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
    }
}
