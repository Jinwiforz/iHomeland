using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;

namespace IHomeland.Client.Core.Lifetime
{
    /// <summary>
    /// 保存一次主线程 drain 的执行数量与 callback 错误快照。
    /// </summary>
    internal readonly struct MainThreadDrainResult
    {
        /// <summary>
        /// 创建一次 drain 的不可变结果。
        /// </summary>
        /// <param name="executedCount">已从队列取出并尝试执行的 callback 数量。</param>
        /// <param name="errors">callback 执行期间按发生顺序收集的异常。</param>
        /// <exception cref="ArgumentException">执行数量为负数或错误集合包含空元素时抛出。</exception>
        /// <exception cref="ArgumentNullException">错误集合为空引用时抛出。</exception>
        internal MainThreadDrainResult(int executedCount, IReadOnlyList<Exception> errors)
        {
            if (executedCount < 0)
            {
                throw new ArgumentException("执行数量不能为负数。", nameof(executedCount));
            }

            if (errors == null)
            {
                throw new ArgumentNullException(nameof(errors));
            }

            var errorSnapshot = new Exception[errors.Count];
            for (var index = 0; index < errors.Count; index++)
            {
                errorSnapshot[index] = errors[index] ??
                    throw new ArgumentException($"错误索引 {index} 不能为空。", nameof(errors));
            }

            ExecutedCount = executedCount;
            Errors = new ReadOnlyCollection<Exception>(errorSnapshot);
        }

        /// <summary>
        /// 获取已从队列取出并尝试执行的 callback 数量。
        /// </summary>
        internal int ExecutedCount { get; }

        /// <summary>
        /// 获取 callback 执行期间按发生顺序复制的只读错误快照。
        /// </summary>
        internal IReadOnlyList<Exception> Errors { get; }
    }
}
