using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Networking.Application.Contracts;

namespace IHomeland.Client.Networking.Application.Ports
{
    /// <summary>
    /// 定义认证前启动阶段允许调用的两个固定 operation。
    /// </summary>
    internal interface IClientBootstrapGateway
    {
        /// <summary>查询认证前版本兼容信息。</summary>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>版本投影或封闭失败。</returns>
        Task<ClientGatewayResult<ClientVersionInfo>> GetVersionAsync(
            CancellationToken cancellationToken);

        /// <summary>查询认证前公开 endpoint 与资源预算。</summary>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>启动配置或封闭失败。</returns>
        Task<ClientGatewayResult<ClientBootstrapConfiguration>> GetBootstrapConfigurationAsync(
            CancellationToken cancellationToken);
    }

    /// <summary>
    /// 定义 Session owner 唯一允许调用的认证、lineage 与短期连接授权 operation。
    /// </summary>
    /// <remarks>
    /// 该 port 不暴露 HTTP method、path、JSON、message ID 或任意发送入口。
    /// </remarks>
    internal interface IClientSessionGateway
    {
        /// <summary>创建账号与新 Session。</summary>
        /// <param name="request">公开账号字段与单次 password lease。</param>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>候选认证结果或封闭失败。</returns>
        Task<ClientGatewayResult<ClientAuthentication>> RegisterAsync(
            ClientRegisterGatewayRequest request,
            CancellationToken cancellationToken);

        /// <summary>使用账号凭据创建新 Session。</summary>
        /// <param name="request">公开 username 与单次 password lease。</param>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>候选认证结果或封闭失败。</returns>
        Task<ClientGatewayResult<ClientAuthentication>> LoginAsync(
            ClientLoginGatewayRequest request,
            CancellationToken cancellationToken);

        /// <summary>轮换 Session owner 当前 refresh lineage。</summary>
        /// <param name="request">仅允许 refresh adapter 取得的 opaque lineage lease。</param>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>候选 token pair 或封闭失败。</returns>
        Task<ClientGatewayResult<ClientTokenPair>> RefreshAsync(
            ClientCredentialGatewayRequest request,
            CancellationToken cancellationToken);

        /// <summary>使当前 Session epoch 失效。</summary>
        /// <param name="request">仅允许 logout adapter 取得的 authorization lease。</param>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>空成功或封闭失败。</returns>
        Task<ClientGatewayResult<ClientGatewayEmpty>> LogoutAsync(
            ClientCredentialGatewayRequest request,
            CancellationToken cancellationToken);

        /// <summary>签发指定通道的一次性 connection ticket。</summary>
        /// <param name="request">Authorization lease 与固定 channel。</param>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>一次性 ticket 候选或封闭失败。</returns>
        Task<ClientGatewayResult<ClientConnectionTicket>> IssueConnectionTicketAsync(
            ClientConnectionTicketGatewayRequest request,
            CancellationToken cancellationToken);

        /// <summary>查询 actor 自己的 PersonalWorld 与 assignment 投影。</summary>
        /// <param name="request">仅允许 bootstrap adapter 取得的 authorization lease。</param>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>World bootstrap 或封闭失败。</returns>
        Task<ClientGatewayResult<ClientWorldBootstrap>> GetWorldBootstrapAsync(
            ClientCredentialGatewayRequest request,
            CancellationToken cancellationToken);

        /// <summary>接受一张具有确定 revision 的定向邀请。</summary>
        /// <param name="request">Authorization、invite identity、revision 与幂等 key。</param>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>Visitor reservation 或封闭失败。</returns>
        Task<ClientGatewayResult<ClientVisitReservation>> AcceptVisitInviteAsync(
            ClientAcceptVisitInviteGatewayRequest request,
            CancellationToken cancellationToken);

        /// <summary>为 own-world 或 current Visitor membership 签发一次性 admission。</summary>
        /// <param name="request">Authorization、封闭 target 与幂等 key。</param>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>一次性 admission 候选或封闭失败。</returns>
        Task<ClientGatewayResult<ClientWorldAdmission>> IssueWorldAdmissionAsync(
            ClientWorldAdmissionGatewayRequest request,
            CancellationToken cancellationToken);
    }
}
