using System;
using IHomeland.Client.Core.Foundation.Time;

namespace IHomeland.Client.Core.Infrastructure.Time
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
