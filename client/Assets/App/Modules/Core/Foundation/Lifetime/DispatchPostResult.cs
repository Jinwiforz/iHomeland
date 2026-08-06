namespace IHomeland.Client.Core.Foundation.Lifetime
{
    /// <summary>
    /// 表示向 App Scope 主线程队列投递工作的确定结果。
    /// </summary>
    internal enum DispatchPostResult
    {
        /// <summary>
        /// callback 已进入队列，将由后续 Unity 主线程 Update 执行。
        /// </summary>
        Accepted,

        /// <summary>
        /// 队列已达到固定容量，callback 未被保留或执行。
        /// </summary>
        QueueFull,

        /// <summary>
        /// Dispatcher 尚未运行或已停止，callback 未被保留或执行。
        /// </summary>
        Stopped,
    }
}
