using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Core.Foundation.Lifetime;

namespace IHomeland.Client.AppShell.Presentation.Navigation
{
    /// <summary>
    /// 标记由 Composition 显式创建并允许交给产品页面的窄上下文。
    /// </summary>
    internal interface IClientUiProductContext
    {
    }

    /// <summary>
    /// 定义纯 Presentation 与具体 Runtime 产品页面之间的显式 binding seam。
    /// </summary>
    internal interface IClientUiProductBinding
    {
        /// <summary>在 AppLifetime 启动前注入产品上下文，不能从全局查找。</summary>
        /// <param name="context">Composition 创建的窄产品上下文。</param>
        void Configure(IClientUiProductContext context);

        /// <summary>绑定当前 route generation 与取消边界。</summary>
        /// <param name="binding">Router 创建的不可变 route binding。</param>
        /// <param name="cancellationToken">Candidate 或 App 停止取消信号。</param>
        /// <returns>页面订阅和控件 callback 已连接时完成。</returns>
        Task BindAsync(ClientUiRouteBinding binding, CancellationToken cancellationToken);

        /// <summary>解除页面订阅、控件 callback 与临时输入。</summary>
        /// <param name="cancellationToken">Route hide 或 App 停止取消信号。</param>
        /// <returns>页面不再允许提交 command 时完成。</returns>
        Task UnbindAsync(CancellationToken cancellationToken);
    }

    /// <summary>
    /// 定义 route router 对单个 UI Toolkit 或 uGUI view 所需的最小生命周期边界。
    /// </summary>
    internal interface IClientUiViewHost
    {
        /// <summary>获取该 Host 唯一承载的 route identity。</summary>
        ClientUiRouteId RouteId { get; }

        /// <summary>获取该 Host 的唯一 framework owner。</summary>
        ClientUiFrameworkOwner FrameworkOwner { get; }

        /// <summary>获取每次成功初始化后递增的 Host generation。</summary>
        long HostGeneration { get; }

        /// <summary>
        /// 初始化 Host 自身拥有的 Unity 资源，不绑定业务状态。
        /// </summary>
        /// <param name="cancellationToken">candidate 或 App 停止时取消。</param>
        /// <returns>Host 可安全 bind 时完成的任务。</returns>
        Task InitializeAsync(CancellationToken cancellationToken);

        /// <summary>
        /// 绑定只包含 generation/cancellation 的页面边界。
        /// </summary>
        /// <param name="binding">当前不可变 route binding。</param>
        /// <param name="cancellationToken">本次 bind 的等待取消。</param>
        /// <returns>Host 已完成 binding 时完成的任务。</returns>
        Task BindAsync(ClientUiRouteBinding binding, CancellationToken cancellationToken);

        /// <summary>
        /// 按登记 layer 显示 Host；Host 不能自行提高层级。
        /// </summary>
        /// <param name="layer">route definition 登记的稳定 layer。</param>
        /// <param name="cancellationToken">本次 show 的等待取消。</param>
        /// <returns>view 已显示但尚不一定获得交互时完成的任务。</returns>
        Task ShowAsync(ClientUiLayer layer, CancellationToken cancellationToken);

        /// <summary>
        /// 切换该 Host 是否接收 pointer、navigation 或业务 command。
        /// </summary>
        /// <param name="interactive">是否允许当前 Host 交互。</param>
        /// <param name="cancellationToken">本次切换的等待取消。</param>
        /// <returns>交互状态已生效时完成的任务。</returns>
        Task SetInteractiveAsync(bool interactive, CancellationToken cancellationToken);

        /// <summary>
        /// 捕获只允许当前 Host generation 恢复的不透明 focus token。
        /// </summary>
        /// <returns>当前 focus token；没有 focus 时 token 的 Value 为空。</returns>
        ClientUiFocusToken CaptureFocus();

        /// <summary>
        /// 聚焦该 Host 显式登记且仍有效的默认元素。
        /// </summary>
        /// <param name="cancellationToken">本次 focus 的等待取消。</param>
        /// <returns>已聚焦有效默认元素时返回 true；不存在时返回 false。</returns>
        Task<bool> FocusDefaultAsync(CancellationToken cancellationToken);

        /// <summary>
        /// 只在 token identity/generation 和 Unity 对象仍有效时恢复 focus。
        /// </summary>
        /// <param name="token">由该 Host 先前捕获的不透明 token。</param>
        /// <param name="cancellationToken">本次恢复的等待取消。</param>
        /// <returns>成功恢复原 focus 时返回 true。</returns>
        Task<bool> RestoreFocusAsync(ClientUiFocusToken token, CancellationToken cancellationToken);

        /// <summary>
        /// 停止交互并隐藏当前 view。
        /// </summary>
        /// <param name="cancellationToken">本次 hide 的等待取消。</param>
        /// <returns>view 已不可见时完成的任务。</returns>
        Task HideAsync(CancellationToken cancellationToken);

        /// <summary>
        /// 解除当前 route binding 和全部页面状态订阅。
        /// </summary>
        /// <param name="cancellationToken">本次 unbind 的等待取消。</param>
        /// <returns>binding 已解除时完成的任务。</returns>
        Task UnbindAsync(CancellationToken cancellationToken);

        /// <summary>
        /// 幂等释放 Host 当前 generation 取得的资源。
        /// </summary>
        /// <param name="cancellationToken">共享清理 deadline。</param>
        /// <returns>当前 generation 已释放时完成的任务。</returns>
        Task DisposeAsync(CancellationToken cancellationToken);
    }

    /// <summary>
    /// 定义唯一 Input System、cursor 与 gameplay gate owner 的窄边界。
    /// </summary>
    internal interface IClientUiInputCoordinator : IAppLifetimeParticipant
    {
        /// <summary>
        /// 原子提交当前 framework-independent input state。
        /// </summary>
        /// <param name="state">由已验证 route snapshot 派生的输入状态。</param>
        /// <param name="cancellationToken">navigation 或 App 停止时取消。</param>
        /// <returns>action map、cursor 与 gameplay gate 已一致时完成的任务。</returns>
        Task ApplyAsync(ClientUiInputState state, CancellationToken cancellationToken);

        /// <summary>获取最近一次成功提交的不可变输入状态。</summary>
        ClientUiInputState CurrentState { get; }
    }
}
