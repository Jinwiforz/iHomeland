using System;
using System.Net;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Contracts;
using IHomeland.Client.Application.Ports;

namespace IHomeland.Client.Infrastructure.Http
{
    /// <summary>
    /// 把冻结的强类型 Application 调用映射到 operation catalog、contract mapper 与有界 transport。
    /// </summary>
    internal sealed class ClientHttpApi :
        IClientBootstrapGateway,
        IClientSessionGateway
    {
        /// <summary>
        /// 保存唯一 transport；所有 operation 复用同一连接池和 AppLifetime cancellation。
        /// </summary>
        private readonly ClientHttpTransport _transport;

        /// <summary>
        /// 保存不依赖 reflection 的显式 JSON/Application contract mapper。
        /// </summary>
        private readonly ClientHttpContractMapper _mapper;

        /// <summary>
        /// 创建不暴露通用发送入口的强类型 API。
        /// </summary>
        /// <param name="transport">AppLifetime 拥有的有界 transport。</param>
        /// <param name="mapper">显式 request/response contract mapper。</param>
        /// <exception cref="ArgumentNullException">任一依赖为空时抛出。</exception>
        internal ClientHttpApi(ClientHttpTransport transport, ClientHttpContractMapper mapper)
        {
            _transport = transport ?? throw new ArgumentNullException(nameof(transport));
            _mapper = mapper ?? throw new ArgumentNullException(nameof(mapper));
        }

        /// <inheritdoc />
        public Task<ClientGatewayResult<ClientVersionInfo>> GetVersionAsync(
            CancellationToken cancellationToken)
        {
            return ExecuteAsync(
                ClientHttpOperationCatalog.GetVersion,
                requestBody: null,
                bearerToken: null,
                _mapper.DecodeVersion,
                cancellationToken);
        }

        /// <inheritdoc />
        public Task<ClientGatewayResult<ClientBootstrapConfiguration>> GetBootstrapConfigurationAsync(
            CancellationToken cancellationToken)
        {
            return ExecuteAsync(
                ClientHttpOperationCatalog.GetBootstrapConfig,
                requestBody: null,
                bearerToken: null,
                _mapper.DecodeBootstrapConfiguration,
                cancellationToken);
        }

        /// <inheritdoc />
        public Task<ClientGatewayResult<ClientAuthentication>> RegisterAsync(
            ClientRegisterGatewayRequest request,
            CancellationToken cancellationToken)
        {
            if (request == null ||
                !request.Password.TryTake(
                    ClientCredentialPurpose.RegisterPassword,
                    out var password))
            {
                return LocalPolicyFailure<ClientAuthentication>(
                    ClientHttpOperationCatalog.RegisterAccount);
            }

            return ExecuteAsync(
                ClientHttpOperationCatalog.RegisterAccount,
                _mapper.EncodeRegister(request.Username, password, request.DisplayName),
                bearerToken: null,
                _mapper.DecodeAuthentication,
                cancellationToken);
        }

        /// <inheritdoc />
        public Task<ClientGatewayResult<ClientAuthentication>> LoginAsync(
            ClientLoginGatewayRequest request,
            CancellationToken cancellationToken)
        {
            if (request == null ||
                !request.Password.TryTake(
                    ClientCredentialPurpose.LoginPassword,
                    out var password))
            {
                return LocalPolicyFailure<ClientAuthentication>(
                    ClientHttpOperationCatalog.LoginAccount);
            }

            return ExecuteAsync(
                ClientHttpOperationCatalog.LoginAccount,
                _mapper.EncodeLogin(request.Username, password),
                bearerToken: null,
                _mapper.DecodeAuthentication,
                cancellationToken);
        }

        /// <inheritdoc />
        public Task<ClientGatewayResult<ClientTokenPair>> RefreshAsync(
            ClientCredentialGatewayRequest request,
            CancellationToken cancellationToken)
        {
            if (!TryTakeCredential(
                    request,
                    ClientCredentialPurpose.RefreshSession,
                    out var refreshToken))
            {
                return LocalPolicyFailure<ClientTokenPair>(
                    ClientHttpOperationCatalog.RefreshSession);
            }

            return ExecuteAsync(
                ClientHttpOperationCatalog.RefreshSession,
                _mapper.EncodeRefresh(refreshToken),
                bearerToken: null,
                _mapper.DecodeTokenPair,
                cancellationToken);
        }

        /// <inheritdoc />
        public Task<ClientGatewayResult<ClientGatewayEmpty>> LogoutAsync(
            ClientCredentialGatewayRequest request,
            CancellationToken cancellationToken)
        {
            if (!TryTakeCredential(
                    request,
                    ClientCredentialPurpose.HttpAuthorization,
                    out var accessToken))
            {
                return LocalPolicyFailure<ClientGatewayEmpty>(
                    ClientHttpOperationCatalog.LogoutSession);
            }

            return ExecuteEmptyAsync(
                ClientHttpOperationCatalog.LogoutSession,
                requestBody: null,
                accessToken,
                cancellationToken);
        }

        /// <inheritdoc />
        public Task<ClientGatewayResult<ClientConnectionTicket>> IssueConnectionTicketAsync(
            ClientConnectionTicketGatewayRequest request,
            CancellationToken cancellationToken)
        {
            if (request == null ||
                !request.Authorization.TryTake(
                    ClientCredentialPurpose.HttpAuthorization,
                    out var accessToken))
            {
                return LocalPolicyFailure<ClientConnectionTicket>(
                    ClientHttpOperationCatalog.IssueConnectionTicket);
            }

            return ExecuteAsync(
                ClientHttpOperationCatalog.IssueConnectionTicket,
                _mapper.EncodeTicket(request.Channel),
                accessToken,
                _mapper.DecodeConnectionTicket,
                cancellationToken);
        }

        /// <inheritdoc />
        public Task<ClientGatewayResult<ClientWorldBootstrap>> GetWorldBootstrapAsync(
            ClientCredentialGatewayRequest request,
            CancellationToken cancellationToken)
        {
            if (!TryTakeCredential(
                    request,
                    ClientCredentialPurpose.HttpAuthorization,
                    out var accessToken))
            {
                return LocalPolicyFailure<ClientWorldBootstrap>(
                    ClientHttpOperationCatalog.GetWorldBootstrap);
            }

            return ExecuteAsync(
                ClientHttpOperationCatalog.GetWorldBootstrap,
                requestBody: null,
                accessToken,
                _mapper.DecodeWorldBootstrap,
                cancellationToken);
        }

        /// <inheritdoc />
        public Task<ClientGatewayResult<ClientVisitReservation>> AcceptVisitInviteAsync(
            ClientAcceptVisitInviteGatewayRequest request,
            CancellationToken cancellationToken)
        {
            if (request == null ||
                !request.Authorization.TryTake(
                    ClientCredentialPurpose.HttpAuthorization,
                    out var accessToken))
            {
                return LocalPolicyFailure<ClientVisitReservation>(
                    ClientHttpOperationCatalog.AcceptVisitInvite);
            }

            string requestPath;
            byte[] requestBody;
            try
            {
                requestPath = _mapper.BuildVisitInviteAcceptPath(request.Invite);
                requestBody = _mapper.EncodeVisitInviteAccept(request.Invite);
            }
            catch (ArgumentException)
            {
                return Task.FromResult(ClientGatewayResult<ClientVisitReservation>.Failed(
                    new ClientGatewayFailure(
                        ClientGatewayFailureKind.LocalPolicy,
                        ClientHttpOperationCatalog.AcceptVisitInvite.OperationID)));
            }

            return ExecuteAsync(
                ClientHttpOperationCatalog.AcceptVisitInvite,
                requestBody,
                accessToken,
                body => _mapper.DecodeVisitInviteAccept(body, request.Invite),
                cancellationToken,
                request.IdempotencyKey,
                requestPath);
        }

        /// <inheritdoc />
        public Task<ClientGatewayResult<ClientWorldAdmission>> IssueWorldAdmissionAsync(
            ClientWorldAdmissionGatewayRequest request,
            CancellationToken cancellationToken)
        {
            if (request == null ||
                !request.Authorization.TryTake(
                    ClientCredentialPurpose.HttpAuthorization,
                    out var accessToken))
            {
                return LocalPolicyFailure<ClientWorldAdmission>(
                    ClientHttpOperationCatalog.IssueWorldAdmission);
            }

            return ExecuteAsync(
                ClientHttpOperationCatalog.IssueWorldAdmission,
                _mapper.EncodeWorldAdmission(request.Target),
                accessToken,
                _mapper.DecodeWorldAdmission,
                cancellationToken,
                request.IdempotencyKey);
        }

        /// <summary>从只含 credential 的请求取得匹配 operation purpose 的 secret。</summary>
        /// <param name="request">封闭 gateway 请求。</param>
        /// <param name="purpose">目标 operation 要求的 purpose。</param>
        /// <param name="credential">成功时返回唯一 credential 使用权。</param>
        /// <returns>请求有效、purpose 匹配且 lease 尚未使用时返回 true。</returns>
        private static bool TryTakeCredential(
            ClientCredentialGatewayRequest request,
            ClientCredentialPurpose purpose,
            out string credential)
        {
            credential = null;
            return request != null &&
                   request.Credential.TryTake(purpose, out credential);
        }

        /// <summary>创建 credential lease 无效时的稳定本地策略失败。</summary>
        /// <typeparam name="T">目标 operation 的成功投影类型。</typeparam>
        /// <param name="operation">固定 operation descriptor。</param>
        /// <returns>不包含 secret 的已完成失败任务。</returns>
        private static Task<ClientGatewayResult<T>> LocalPolicyFailure<T>(
            ClientHttpOperation operation)
        {
            return Task.FromResult(
                ClientGatewayResult<T>.Failed(
                    new ClientGatewayFailure(
                        ClientGatewayFailureKind.LocalPolicy,
                        operation.OperationID)));
        }

        /// <summary>
        /// 执行必须返回 JSON payload 的 operation，并统一解码成功与 ErrorResponse。
        /// </summary>
        /// <typeparam name="T">成功投影类型。</typeparam>
        /// <param name="operation">冻结 descriptor。</param>
        /// <param name="requestBody">可选有界 JSON request body。</param>
        /// <param name="bearerToken">认证 operation 的 access token。</param>
        /// <param name="decodeSuccess">显式成功投影函数。</param>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <param name="idempotencyKey">仅需要幂等 header 的 operation 提供；其他 operation 必须为空。</param>
        /// <param name="requestPath">仅带 path parameter 的冻结 operation 提供；其他 operation 必须为空。</param>
        /// <returns>成功、服务端错误或本地失败三选一结果。</returns>
        private async Task<ClientGatewayResult<T>> ExecuteAsync<T>(
            ClientHttpOperation operation,
            byte[] requestBody,
            string bearerToken,
            Func<ReadOnlyMemory<byte>, T> decodeSuccess,
            CancellationToken cancellationToken,
            string idempotencyKey = null,
            string requestPath = null)
        {
            var raw = await _transport.SendAsync(
                operation,
                requestBody,
                bearerToken,
                cancellationToken,
                idempotencyKey,
                requestPath);
            if (!raw.HasResponse)
            {
                return ClientGatewayResult<T>.Failed(raw.Failure);
            }

            var response = raw.Response;
            try
            {
                if (response.StatusCode == operation.SuccessStatus)
                {
                    if (!operation.SuccessHasBody || !IsJson(response.MediaType) || response.Body.Length == 0)
                    {
                        return Malformed<T>(operation, response.StatusCode);
                    }

                    return ClientGatewayResult<T>.Success(decodeSuccess(response.Body));
                }

                return DecodeRejected<T>(operation, response);
            }
            catch (ClientHttpContractException)
            {
                return Malformed<T>(operation, response.StatusCode);
            }
        }

        /// <summary>
        /// 执行成功时必须为无 body status 的 operation。
        /// </summary>
        /// <param name="operation">冻结 descriptor。</param>
        /// <param name="requestBody">当前 logout operation 为空。</param>
        /// <param name="bearerToken">当前 access token。</param>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>空成功、服务端错误或本地失败。</returns>
        private async Task<ClientGatewayResult<ClientGatewayEmpty>> ExecuteEmptyAsync(
            ClientHttpOperation operation,
            byte[] requestBody,
            string bearerToken,
            CancellationToken cancellationToken)
        {
            var raw = await _transport.SendAsync(
                operation,
                requestBody,
                bearerToken,
                cancellationToken);
            if (!raw.HasResponse)
            {
                return ClientGatewayResult<ClientGatewayEmpty>.Failed(raw.Failure);
            }

            var response = raw.Response;
            if (response.StatusCode == operation.SuccessStatus)
            {
                return response.Body.Length == 0
                    ? ClientGatewayResult<ClientGatewayEmpty>.Success(new ClientGatewayEmpty())
                    : Malformed<ClientGatewayEmpty>(operation, response.StatusCode);
            }

            try
            {
                return DecodeRejected<ClientGatewayEmpty>(operation, response);
            }
            catch (ClientHttpContractException)
            {
                return Malformed<ClientGatewayEmpty>(operation, response.StatusCode);
            }
        }

        /// <summary>
        /// 解码 4xx/5xx ErrorResponse，并拒绝 redirect 或其他非错误 status。
        /// </summary>
        /// <typeparam name="T">原 operation 成功投影类型。</typeparam>
        /// <param name="operation">冻结 descriptor。</param>
        /// <param name="response">完整有界响应。</param>
        /// <returns>结构有效的服务端拒绝或 malformed failure。</returns>
        private ClientGatewayResult<T> DecodeRejected<T>(
            ClientHttpOperation operation,
            ClientHttpRawResponse response)
        {
            var status = (int)response.StatusCode;
            if (status < 400 || status > 599 || !IsJson(response.MediaType) || response.Body.Length == 0)
            {
                return Malformed<T>(operation, response.StatusCode);
            }

            var serverError = _mapper.DecodeServerError(response.Body, response.StatusCode);
            if (response.RetryAfter.HasValue)
            {
                if (response.RetryAfter.Value < TimeSpan.Zero ||
                    response.RetryAfter.Value > TimeSpan.FromSeconds(60) ||
                    (serverError.RetryAfter.HasValue && serverError.RetryAfter.Value != response.RetryAfter.Value))
                {
                    return Malformed<T>(operation, response.StatusCode);
                }

                if (!serverError.RetryAfter.HasValue)
                {
                    serverError = new ClientServerError(
                        serverError.Code,
                        serverError.Category,
                        serverError.MessageKey,
                        serverError.RequestID,
                        serverError.Retryable,
                        response.RetryAfter,
                        serverError.Details);
                }
            }

            return ClientGatewayResult<T>.Rejected(serverError);
        }

        /// <summary>
        /// 判断 Content-Type 是否是允许携带结构化契约的 JSON media type。
        /// </summary>
        /// <param name="mediaType">不含 charset 参数的 media type。</param>
        /// <returns>精确为 application/json 时返回 true。</returns>
        private static bool IsJson(string mediaType)
        {
            return string.Equals(mediaType, "application/json", StringComparison.OrdinalIgnoreCase);
        }

        /// <summary>
        /// 创建不包含响应 body 或内部解析错误的 malformed 结果。
        /// </summary>
        /// <typeparam name="T">原 operation 成功投影类型。</typeparam>
        /// <param name="operation">发生 contract 漂移的 descriptor。</param>
        /// <param name="statusCode">实际 HTTP status。</param>
        /// <returns>稳定本地失败。</returns>
        private static ClientGatewayResult<T> Malformed<T>(
            ClientHttpOperation operation,
            HttpStatusCode statusCode)
        {
            return ClientGatewayResult<T>.Failed(new ClientGatewayFailure(
                ClientGatewayFailureKind.MalformedResponse,
                operation.OperationID));
        }
    }
}
