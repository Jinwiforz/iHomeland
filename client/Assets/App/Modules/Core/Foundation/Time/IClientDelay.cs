using System;
using System.Threading;
using System.Threading.Tasks;

namespace IHomeland.Client.Core.Foundation.Time
{
    /// <summary>
    /// 定义不依赖 Unity frame tick 的可取消等待边界。
    /// </summary>
    internal interface IClientDelay
    {
        /// <summary>
        /// 等待指定时间，调用方生命周期撤销时立即结束。
        /// </summary>
        /// <param name="delay">必须由调用方 policy 验证的等待时长。</param>
        /// <param name="cancellationToken">当前 operation 或 generation 的取消信号。</param>
        /// <returns>等待到期或取消时完成的任务。</returns>
        Task DelayAsync(TimeSpan delay, CancellationToken cancellationToken);
    }
}
