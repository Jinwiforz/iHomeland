using System.Threading;
using System.Threading.Tasks;

namespace IHomeland.Client.Core.Lifetime
{
    /// <summary>
    /// 定义由 App Scope 生命周期统一初始化和逆序停止的窄边界。
    /// </summary>
    /// <remarks>
    /// 实现不得自行发现全局服务。初始化成功后必须能接受一次停止；停止需要响应传入的
    /// deadline token，并在部分初始化资源存在时执行幂等清理。
    /// </remarks>
    internal interface IAppLifetimeParticipant
    {
        /// <summary>
        /// 初始化当前参与者拥有的运行时资源。
        /// </summary>
        /// <param name="cancellationToken">取消本次启动等待；取消后仍须由生命周期 owner 决定回滚。</param>
        /// <returns>资源可安全提供给后续参与者时完成的任务。</returns>
        Task InitializeAsync(CancellationToken cancellationToken);

        /// <summary>
        /// 停止并释放当前参与者已经取得的资源。
        /// </summary>
        /// <param name="cancellationToken">共享清理 deadline；实现应在取消后尽快结束等待。</param>
        /// <returns>当前参与者的清理已完成或已明确失败时结束的任务。</returns>
        Task StopAsync(CancellationToken cancellationToken);
    }
}
