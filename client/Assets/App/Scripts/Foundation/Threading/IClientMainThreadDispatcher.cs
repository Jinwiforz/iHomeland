using System;
using IHomeland.Client.Foundation.Lifetime;

namespace IHomeland.Client.Foundation.Threading
{
    /// <summary>
    /// 定义向唯一客户端主线程提交有界 callback 的通用边界。
    /// </summary>
    internal interface IClientMainThreadDispatcher
    {
        /// <summary>
        /// 提交受普通容量限制的 callback。
        /// </summary>
        /// <param name="callback">不得携带 credential 或可变 transport buffer 的工作项。</param>
        /// <returns>是否接受、过载或生命周期已停止。</returns>
        DispatchPostResult TryPost(Action callback);

        /// <summary>
        /// 提交必须在 terminal 收敛前执行的保留容量 callback。
        /// </summary>
        /// <param name="callback">只用于关闭、失效或 recovery terminal 的工作项。</param>
        /// <returns>是否接受、过载或生命周期已停止。</returns>
        DispatchPostResult TryPostCritical(Action callback);
    }
}
