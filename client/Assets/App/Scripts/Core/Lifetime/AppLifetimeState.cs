namespace IHomeland.Client.Core.Lifetime
{
    /// <summary>
    /// 表示一套 App Scope 对象图的单向生命周期状态。
    /// </summary>
    internal enum AppLifetimeState
    {
        /// <summary>
        /// 对象图已创建，但尚未开始初始化。
        /// </summary>
        Created,

        /// <summary>
        /// 生命周期参与者正在按登记顺序初始化。
        /// </summary>
        Initializing,

        /// <summary>
        /// 全部参与者初始化成功，允许 tick 和主线程工作执行。
        /// </summary>
        Running,

        /// <summary>
        /// 已开始逆序停止，不再接受新的运行时工作。
        /// </summary>
        Stopping,

        /// <summary>
        /// 全部可执行清理均已尝试，旧对象图不得再次启动。
        /// </summary>
        Stopped,

        /// <summary>
        /// 初始化失败且已完成尽力回滚，旧对象图不得再次启动。
        /// </summary>
        Failed,
    }
}
