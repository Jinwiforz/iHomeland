using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Core.Foundation.Time;

namespace IHomeland.Client.Core.Infrastructure.Time
{
    /// <summary>
    /// 使用 BCL timer 实现不阻塞 Unity 主线程的通用等待 adapter。
    /// </summary>
    internal sealed class SystemClientDelay : IClientDelay
    {
        /// <inheritdoc />
        public Task DelayAsync(TimeSpan delay, CancellationToken cancellationToken)
        {
            return Task.Delay(delay, cancellationToken);
        }
    }
}
