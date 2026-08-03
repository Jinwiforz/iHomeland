using System;
using System.Net;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Battle;
using IHomeland.Client.Application.Contracts;
using IHomeland.Client.Infrastructure.Http;

namespace IHomeland.Client.Infrastructure.Battle
{
    /// <summary>
    /// 把 BattleTicket HTTP operation 与共享 transport 隔离为可验证的 raw body 所有权边界。
    /// </summary>
    internal interface IClientBattleTicketTransport
    {
        /// <summary>
        /// 发送唯一 BattleTicket operation，并把有界 raw response body 所有权转移给 caller。
        /// </summary>
        /// <param name="operation">冻结的 BattleTicket operation。</param>
        /// <param name="requestBody">Caller-owned closed selector bytes。</param>
        /// <param name="bearerToken">单次 HTTP authorization。</param>
        /// <param name="cancellationToken">Attempt cancellation。</param>
        /// <param name="idempotencyKey">Attempt-stable 幂等 identity。</param>
        /// <returns>完整 raw response 或稳定 transport failure。</returns>
        Task<ClientHttpRawResult> SendAsync(
            ClientHttpOperation operation,
            byte[] requestBody,
            string bearerToken,
            CancellationToken cancellationToken,
            string idempotencyKey);
    }

    /// <summary>
    /// 把 AppLifetime 唯一 ClientHttpTransport 适配到 BattleTicket 专用窄边界。
    /// </summary>
    internal sealed class ClientBattleTicketTransport :
        IClientBattleTicketTransport
    {
        /// <summary>保存唯一共享 HTTP transport。</summary>
        private readonly ClientHttpTransport _transport;

        /// <summary>
        /// 创建不拥有底层 transport 生命周期的 adapter。
        /// </summary>
        /// <param name="transport">AppLifetime 统一 HTTP transport。</param>
        internal ClientBattleTicketTransport(ClientHttpTransport transport)
        {
            _transport = transport ?? throw new ArgumentNullException(nameof(transport));
        }

        /// <inheritdoc />
        public Task<ClientHttpRawResult> SendAsync(
            ClientHttpOperation operation,
            byte[] requestBody,
            string bearerToken,
            CancellationToken cancellationToken,
            string idempotencyKey)
        {
            return _transport.SendAsync(
                operation,
                requestBody,
                bearerToken,
                cancellationToken,
                idempotencyKey);
        }
    }

    /// <summary>
    /// 允许 connect attempt 在测试中替换票据签发，同时保持 production codec 与 HTTP owner 封闭。
    /// </summary>
    internal interface IClientBattleTicketGateway
    {
        /// <summary>
        /// 使用单次 authorization 与稳定幂等 identity 签发 BattleTicket。
        /// </summary>
        /// <param name="request">绑定 current target 的签发请求。</param>
        /// <param name="cancellationToken">Current connect attempt cancellation。</param>
        /// <returns>Ticket material、服务端拒绝或稳定本地失败三选一。</returns>
        Task<ClientGatewayResult<ClientBattleTicketMaterial>> IssueAsync(
            ClientBattleTicketGatewayRequest request,
            CancellationToken cancellationToken);
    }

    /// <summary>
    /// 只消费 issueBattleTicket operation，并把 raw secret body 交给专用 streaming codec。
    /// </summary>
    internal sealed class ClientBattleTicketHttpApi :
        IClientBattleTicketGateway
    {
        /// <summary>
        /// 保存 AppLifetime 拥有的统一 HTTP transport。
        /// </summary>
        private readonly IClientBattleTicketTransport _transport;

        /// <summary>
        /// 保存不经通用 DTO 的专用 ticket codec。
        /// </summary>
        private readonly ClientBattleTicketCodec _codec;

        /// <summary>
        /// 保存只用于公开 ErrorResponse 的通用 mapper。
        /// </summary>
        private readonly ClientHttpContractMapper _errorMapper;

        /// <summary>
        /// 创建固定 operation API。
        /// </summary>
        /// <param name="transport">AppLifetime 统一 HTTP transport。</param>
        /// <param name="codec">专用 BattleTicket codec。</param>
        /// <param name="errorMapper">只解码非 secret ErrorResponse 的 mapper。</param>
        internal ClientBattleTicketHttpApi(
            ClientHttpTransport transport,
            ClientBattleTicketCodec codec,
            ClientHttpContractMapper errorMapper)
            : this(
                new ClientBattleTicketTransport(transport),
                codec,
                errorMapper)
        {
        }

        /// <summary>
        /// 创建使用可控 raw transport 的专用 ticket API。
        /// </summary>
        /// <param name="transport">只转移有界 raw response 的窄 transport。</param>
        /// <param name="codec">专用 BattleTicket codec。</param>
        /// <param name="errorMapper">只解码非 secret ErrorResponse 的 mapper。</param>
        internal ClientBattleTicketHttpApi(
            IClientBattleTicketTransport transport,
            ClientBattleTicketCodec codec,
            ClientHttpContractMapper errorMapper)
        {
            _transport = transport ?? throw new ArgumentNullException(nameof(transport));
            _codec = codec ?? throw new ArgumentNullException(nameof(codec));
            _errorMapper = errorMapper ?? throw new ArgumentNullException(nameof(errorMapper));
        }

        /// <summary>
        /// 签发一次 BattleTicket；不自动重试 commit-unknown response。
        /// </summary>
        /// <param name="request">Authorization、closed target 与稳定幂等 key。</param>
        /// <param name="cancellationToken">Current connect attempt cancellation。</param>
        /// <returns>Ticket material、服务端拒绝或稳定本地失败三选一。</returns>
        public async Task<ClientGatewayResult<ClientBattleTicketMaterial>> IssueAsync(
            ClientBattleTicketGatewayRequest request,
            CancellationToken cancellationToken)
        {
            if (request == null ||
                !request.Authorization.TryTake(
                    ClientCredentialPurpose.HttpAuthorization,
                    out var accessToken))
            {
                return LocalPolicyFailure();
            }

            byte[] requestBody;
            try
            {
                requestBody = _codec.EncodeRequest(request.Target);
            }
            catch (ArgumentException)
            {
                return LocalPolicyFailure();
            }

            try
            {
                var raw = await _transport.SendAsync(
                    ClientHttpOperationCatalog.IssueBattleTicket,
                    requestBody,
                    accessToken,
                    cancellationToken,
                    request.IdempotencyKey);
                if (!raw.HasResponse)
                {
                    return ClientGatewayResult<ClientBattleTicketMaterial>.Failed(
                        raw.Failure);
                }

                var response = raw.Response;
                try
                {
                    if (response.StatusCode == HttpStatusCode.Created)
                    {
                        if (!IsJson(response.MediaType) || response.Body.Length == 0)
                        {
                            return Malformed();
                        }

                        return ClientGatewayResult<ClientBattleTicketMaterial>.Success(
                            _codec.Decode(response.Body, request.Target));
                    }

                    var statusCode = (int)response.StatusCode;
                    if (statusCode < 400 ||
                        statusCode > 599 ||
                        !IsJson(response.MediaType) ||
                        response.Body.Length == 0)
                    {
                        return Malformed();
                    }

                    var error = _errorMapper.DecodeServerError(
                        response.Body,
                        response.StatusCode);
                    if (response.RetryAfter.HasValue)
                    {
                        if (response.RetryAfter.Value < TimeSpan.Zero ||
                            response.RetryAfter.Value > TimeSpan.FromSeconds(60) ||
                            (error.RetryAfter.HasValue &&
                             error.RetryAfter.Value != response.RetryAfter.Value))
                        {
                            return Malformed();
                        }

                        if (!error.RetryAfter.HasValue)
                        {
                            error = new ClientServerError(
                                error.Code,
                                error.Category,
                                error.MessageKey,
                                error.RequestID,
                                error.Retryable,
                                response.RetryAfter,
                                error.Details);
                        }
                    }

                    return ClientGatewayResult<ClientBattleTicketMaterial>.Rejected(
                        error);
                }
                catch (ClientBattleTicketCodecException)
                {
                    return Malformed();
                }
                catch (ClientHttpContractException)
                {
                    return Malformed();
                }
                finally
                {
                    Array.Clear(response.Body, 0, response.Body.Length);
                }
            }
            finally
            {
                Array.Clear(requestBody, 0, requestBody.Length);
            }
        }

        /// <summary>
        /// 创建不含 credential 的 local policy failure。
        /// </summary>
        /// <returns>已完成失败结果。</returns>
        private static ClientGatewayResult<ClientBattleTicketMaterial>
            LocalPolicyFailure()
        {
            return ClientGatewayResult<ClientBattleTicketMaterial>.Failed(
                new ClientGatewayFailure(
                    ClientGatewayFailureKind.LocalPolicy,
                    ClientHttpOperationCatalog.IssueBattleTicket.OperationID));
        }

        /// <summary>
        /// 创建不包含 raw body 或 codec cause 的 malformed failure。
        /// </summary>
        /// <returns>已完成失败结果。</returns>
        private static ClientGatewayResult<ClientBattleTicketMaterial> Malformed()
        {
            return ClientGatewayResult<ClientBattleTicketMaterial>.Failed(
                new ClientGatewayFailure(
                    ClientGatewayFailureKind.MalformedResponse,
                    ClientHttpOperationCatalog.IssueBattleTicket.OperationID));
        }

        /// <summary>
        /// 判断 media type 是否精确为 JSON。
        /// </summary>
        /// <param name="mediaType">不含 charset 的 response media type。</param>
        /// <returns>精确匹配 application/json 时为 true。</returns>
        private static bool IsJson(string mediaType)
        {
            return string.Equals(
                mediaType,
                "application/json",
                StringComparison.OrdinalIgnoreCase);
        }
    }
}
