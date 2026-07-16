using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using IHomeland.Client.Core.Lifetime;

namespace IHomeland.Client.Core.Composition
{
    /// <summary>
    /// 保存 AppComposition 创建并转交给唯一 AppRoot 的不可变 C0 对象图。
    /// </summary>
    /// <remarks>
    /// 该结果不是 service locator，只暴露 AppRoot 实际驱动和拥有的生命周期能力。
    /// feature 不能通过它按类型查找任意服务。
    /// </remarks>
    internal sealed class AppCompositionResult
    {
        /// <summary>
        /// 保存 Composition 完成时冻结的 tickable 快照。
        /// </summary>
        private readonly ReadOnlyCollection<IAppTickable> _tickables;

        /// <summary>
        /// 创建只包含 C0 实际运行边界的对象图结果。
        /// </summary>
        /// <param name="lifetime">统一拥有 App Scope 初始化和逆序停止的生命周期。</param>
        /// <param name="dispatcher">由 AppRoot Update 有界排空的主线程队列。</param>
        /// <param name="tickables">Composition 明确登记并冻结的逐帧对象。</param>
        /// <param name="maximumDispatchesPerFrame">单帧最多执行的主线程 callback 数量。</param>
        /// <exception cref="ArgumentException">单帧 callback 上限非正数时抛出。</exception>
        /// <exception cref="ArgumentNullException">任一对象、集合引用或 tickable 元素为 null 时抛出。</exception>
        internal AppCompositionResult(
            AppLifetime lifetime,
            MainThreadDispatcher dispatcher,
            IReadOnlyList<IAppTickable> tickables,
            int maximumDispatchesPerFrame)
        {
            Lifetime = lifetime ?? throw new ArgumentNullException(nameof(lifetime));
            Dispatcher = dispatcher ?? throw new ArgumentNullException(nameof(dispatcher));
            if (tickables == null)
            {
                throw new ArgumentNullException(nameof(tickables));
            }

            if (maximumDispatchesPerFrame <= 0)
            {
                throw new ArgumentException("单帧 callback 上限必须为正数。", nameof(maximumDispatchesPerFrame));
            }

            var copy = new IAppTickable[tickables.Count];
            for (var index = 0; index < tickables.Count; index++)
            {
                copy[index] = tickables[index] ??
                    throw new ArgumentNullException(nameof(tickables), $"tickable 索引 {index} 不能为空。");
            }

            _tickables = new ReadOnlyCollection<IAppTickable>(copy);
            MaximumDispatchesPerFrame = maximumDispatchesPerFrame;
        }

        /// <summary>
        /// 获取 App Scope 的唯一生命周期 owner。
        /// </summary>
        internal AppLifetime Lifetime { get; }

        /// <summary>
        /// 获取由 AppRoot 在 Unity 主线程排空的有界 Dispatcher。
        /// </summary>
        internal MainThreadDispatcher Dispatcher { get; }

        /// <summary>
        /// 获取 Composition 完成时冻结的逐帧对象快照。
        /// </summary>
        internal IReadOnlyList<IAppTickable> Tickables => _tickables;

        /// <summary>
        /// 获取单帧主线程 callback 执行硬上限。
        /// </summary>
        internal int MaximumDispatchesPerFrame { get; }
    }
}
