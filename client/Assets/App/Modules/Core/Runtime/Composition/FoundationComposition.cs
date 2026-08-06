using System;
using IHomeland.Client.Core.Foundation.Lifetime;
using IHomeland.Client.Core.Foundation.Threading;
using IHomeland.Client.Core.Foundation.Time;
using IHomeland.Client.Core.Infrastructure.Time;

namespace IHomeland.Client.Core.Runtime.Composition
{
    /// <summary>创建 App Scope 的通用时钟、延时与主线程投递基础设施。</summary>
    internal static class FoundationComposition
    {
        /// <summary>创建只供后续模块装配使用的 Foundation bundle。</summary>
        internal static FoundationCompositionBundle Create(
            int mainThreadQueueCapacity)
        {
            if (mainThreadQueueCapacity <= 0)
            {
                throw new ArgumentOutOfRangeException(nameof(mainThreadQueueCapacity));
            }

            return new FoundationCompositionBundle(
                new MainThreadDispatcher(
                    Environment.CurrentManagedThreadId,
                    mainThreadQueueCapacity),
                new SystemClientClock(),
                new SystemClientDelay());
        }
    }

    /// <summary>封闭 Foundation 模块的跨模块输出，不提供运行期解析。</summary>
    internal sealed class FoundationCompositionBundle
    {
        /// <summary>创建不可变 Foundation bundle。</summary>
        internal FoundationCompositionBundle(
            MainThreadDispatcher dispatcher,
            IClientClock clock,
            IClientDelay delay)
        {
            Dispatcher = dispatcher ?? throw new ArgumentNullException(nameof(dispatcher));
            Clock = clock ?? throw new ArgumentNullException(nameof(clock));
            Delay = delay ?? throw new ArgumentNullException(nameof(delay));
        }

        /// <summary>获取唯一主线程 dispatcher。</summary>
        internal MainThreadDispatcher Dispatcher { get; }

        /// <summary>获取可测试 UTC clock。</summary>
        internal IClientClock Clock { get; }

        /// <summary>获取 channel retry/heartbeat delay。</summary>
        internal IClientDelay Delay { get; }
    }
}
