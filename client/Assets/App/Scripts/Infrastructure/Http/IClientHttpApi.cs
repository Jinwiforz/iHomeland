using System.Threading;
using System.Threading.Tasks;

namespace IHomeland.Client.Infrastructure.Http
{
    /// <summary>
    /// 定义 C1 首段唯一允许上层调用的八个强类型 HTTP operation。
    /// </summary>
    /// <remarks>
    /// 接口由 application 消费侧拥有测试替换边界，不暴露任意 method/path/body。Bearer token 只由
    /// Session owner 借给认证方法，不能由 UI、Scene 或 payload 构造。
    /// </remarks>
    internal interface IClientHttpApi
    {
        /// <summary>
        /// 查询认证前版本兼容信息。
        /// </summary>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>版本投影、服务端错误或稳定本地失败。</returns>
        Task<ClientHttpResult<ClientVersionInfo>> GetVersionAsync(CancellationToken cancellationToken);

        /// <summary>
        /// 查询认证前公开 endpoint 与资源预算。
        /// </summary>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>启动配置、服务端错误或稳定本地失败。</returns>
        Task<ClientHttpResult<ClientBootstrapConfiguration>> GetBootstrapConfigurationAsync(
            CancellationToken cancellationToken);

        /// <summary>
        /// 创建账号与新 session。
        /// </summary>
        /// <param name="username">符合公开 grammar 的 username。</param>
        /// <param name="password">只存活于当前调用的原始 password。</param>
        /// <param name="displayName">待服务端规范化的显示名。</param>
        /// <param name="cancellationToken">调用方取消等待的信号；取消不证明服务端未提交。</param>
        /// <returns>原子认证结果、服务端错误或稳定本地失败。</returns>
        Task<ClientHttpResult<ClientAuthentication>> RegisterAsync(
            string username,
            string password,
            string displayName,
            CancellationToken cancellationToken);

        /// <summary>
        /// 使用账号凭据创建新 session。
        /// </summary>
        /// <param name="username">符合公开 grammar 的 username。</param>
        /// <param name="password">只存活于当前调用的原始 password。</param>
        /// <param name="cancellationToken">调用方取消等待的信号；取消不证明服务端未提交。</param>
        /// <returns>原子认证结果、服务端错误或稳定本地失败。</returns>
        Task<ClientHttpResult<ClientAuthentication>> LoginAsync(
            string username,
            string password,
            CancellationToken cancellationToken);

        /// <summary>
        /// 使用当前 opaque refresh token 轮换 token pair。
        /// </summary>
        /// <param name="refreshToken">Session owner 当前 snapshot 的 refresh token。</param>
        /// <param name="cancellationToken">调用方取消等待的信号；取消不证明服务端未提交。</param>
        /// <returns>新 token pair、服务端错误或稳定本地失败。</returns>
        Task<ClientHttpResult<ClientTokenPair>> RefreshAsync(
            string refreshToken,
            CancellationToken cancellationToken);

        /// <summary>
        /// 使当前 access token 对应的 session epoch 失效。
        /// </summary>
        /// <param name="accessToken">Session owner 当前 snapshot 的 access token。</param>
        /// <param name="cancellationToken">调用方取消等待的信号；取消可能产生 commit-unknown。</param>
        /// <returns>204 成功、服务端错误或稳定本地失败。</returns>
        Task<ClientHttpResult<ClientHttpEmpty>> LogoutAsync(
            string accessToken,
            CancellationToken cancellationToken);

        /// <summary>
        /// 为单一实时 channel 签发短期 connection ticket。
        /// </summary>
        /// <param name="accessToken">Session owner 当前 snapshot 的 access token。</param>
        /// <param name="channel">WSS 或 TLS_TCP channel。</param>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>短期 opaque ticket、服务端错误或稳定本地失败。</returns>
        Task<ClientHttpResult<ClientConnectionTicket>> IssueConnectionTicketAsync(
            string accessToken,
            ClientEndpointChannel channel,
            CancellationToken cancellationToken);

        /// <summary>
        /// 查询认证 actor 自己的 PersonalWorld 与 current assignment 安全投影。
        /// </summary>
        /// <param name="accessToken">Session owner 当前 snapshot 的 access token。</param>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>一次性 world bootstrap 投影、服务端错误或稳定本地失败。</returns>
        Task<ClientHttpResult<ClientWorldBootstrap>> GetWorldBootstrapAsync(
            string accessToken,
            CancellationToken cancellationToken);
    }
}
