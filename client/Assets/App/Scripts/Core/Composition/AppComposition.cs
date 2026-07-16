using System;
using IHomeland.Client.Core.Lifetime;
using IHomeland.Client.Scenes.Contexts;

namespace IHomeland.Client.Core.Composition
{
    /// <summary>
    /// 在唯一入口显式创建并连接 C0 App Scope 对象图。
    /// </summary>
    /// <remarks>
    /// 该类型是当前 change 唯一了解 concrete types 的位置。它不缓存对象图、不提供按类型查询，
    /// 每个实例最多构造一次结果，重复 bootstrap 由 AppRoot 唯一性争用在调用前拒绝。
    /// </remarks>
    internal sealed class AppComposition
    {
        /// <summary>
        /// 限制跨线程 callback 积压，避免未来生产者无界占用 App Scope 内存。
        /// </summary>
        private const int MainThreadQueueCapacity = 1024;

        /// <summary>
        /// 限制单帧 callback 数量，避免一次积压独占 Unity 主线程。
        /// </summary>
        private const int MaximumDispatchesPerFrame = 128;

        /// <summary>
        /// 限制初始化失败后的逆序回滚总等待时间。
        /// </summary>
        private static readonly TimeSpan RollbackTimeout = TimeSpan.FromSeconds(5);

        /// <summary>
        /// 限制正常退出时的逆序停止总等待时间。
        /// </summary>
        private static readonly TimeSpan ShutdownTimeout = TimeSpan.FromSeconds(5);

        /// <summary>
        /// 表示当前 Composition 已构造对象图，阻止同一实例重复 Build。
        /// </summary>
        private bool _built;

        /// <summary>
        /// 在当前 Unity 主线程创建 C0 对象图和冻结的执行顺序。
        /// </summary>
        /// <returns>只供唯一 AppRoot 持有和驱动的不可变 composition 结果。</returns>
        /// <exception cref="InvalidOperationException">同一 AppComposition 实例重复 Build 时抛出。</exception>
        internal AppCompositionResult Build()
        {
            if (_built)
            {
                throw new InvalidOperationException("同一 AppComposition 不能重复构造 App Scope。");
            }

            _built = true;
            var dispatcher = new MainThreadDispatcher(
                Environment.CurrentManagedThreadId,
                MainThreadQueueCapacity);
            var sceneLifetimeOwner = new SceneLifetimeOwner();

            // 初始化顺序使 Dispatcher 先可用；逆序停止会先取消 Scene Scope，再拒绝主线程回写。
            IAppLifetimeParticipant[] participants =
            {
                dispatcher,
                sceneLifetimeOwner,
            };

            var lifetime = new AppLifetime(participants, RollbackTimeout, ShutdownTimeout);
            return new AppCompositionResult(
                lifetime,
                dispatcher,
                Array.Empty<IAppTickable>(),
                MaximumDispatchesPerFrame);
        }
    }
}
