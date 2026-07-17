using System;
using System.Net;
using System.Threading;
using System.Threading.Tasks;

namespace IHomeland.Client.Infrastructure.Http
{
    /// <summary>
    /// 把九个强类型 application 调用映射到 operation catalog、显式 codec 与有界 transport。
    /// </summary>
    internal sealed class ClientHttpApi : IClientHttpApi
    {
        /// <summary>
        /// 保存唯一 transport；所有 operation 复用同一连接池和 AppLifetime cancellation。
        /// </summary>
        private readonly ClientHttpTransport _transport;

        /// <summary>
        /// 保存不依赖 reflection 的显式 JSON codec。
        /// </summary>
        private readonly ClientHttpCodec _codec;

        /// <summary>
        /// 创建不暴露通用发送入口的强类型 API。
        /// </summary>
        /// <param name="transport">AppLifetime 拥有的有界 transport。</param>
        /// <param name="codec">显式 request/response codec。</param>
        /// <exception cref="ArgumentNullException">任一依赖为空时抛出。</exception>
        internal ClientHttpApi(ClientHttpTransport transport, ClientHttpCodec codec)
        {
            _transport = transport ?? throw new ArgumentNullException(nameof(transport));
            _codec = codec ?? throw new ArgumentNullException(nameof(codec));
        }

        /// <inheritdoc />
        public Task<ClientHttpResult<ClientVersionInfo>> GetVersionAsync(
            CancellationToken cancellationToken)
        {
            return ExecuteAsync(
                ClientHttpOperationCatalog.GetVersion,
                requestBody: null,
                bearerToken: null,
                _codec.DecodeVersion,
                cancellationToken);
        }

        /// <inheritdoc />
        public Task<ClientHttpResult<ClientBootstrapConfiguration>> GetBootstrapConfigurationAsync(
            CancellationToken cancellationToken)
        {
            return ExecuteAsync(
                ClientHttpOperationCatalog.GetBootstrapConfig,
                requestBody: null,
                bearerToken: null,
                _codec.DecodeBootstrapConfiguration,
                cancellationToken);
        }

        /// <inheritdoc />
        public Task<ClientHttpResult<ClientAuthentication>> RegisterAsync(
            string username,
            string password,
            string displayName,
            CancellationToken cancellationToken)
        {
            return ExecuteAsync(
                ClientHttpOperationCatalog.RegisterAccount,
                _codec.EncodeRegister(username, password, displayName),
                bearerToken: null,
                _codec.DecodeAuthentication,
                cancellationToken);
        }

        /// <inheritdoc />
        public Task<ClientHttpResult<ClientAuthentication>> LoginAsync(
            string username,
            string password,
            CancellationToken cancellationToken)
        {
            return ExecuteAsync(
                ClientHttpOperationCatalog.LoginAccount,
                _codec.EncodeLogin(username, password),
                bearerToken: null,
                _codec.DecodeAuthentication,
                cancellationToken);
        }

        /// <inheritdoc />
        public Task<ClientHttpResult<ClientTokenPair>> RefreshAsync(
            string refreshToken,
            CancellationToken cancellationToken)
        {
            return ExecuteAsync(
                ClientHttpOperationCatalog.RefreshSession,
                _codec.EncodeRefresh(refreshToken),
                bearerToken: null,
                _codec.DecodeTokenPair,
                cancellationToken);
        }

        /// <inheritdoc />
        public Task<ClientHttpResult<ClientHttpEmpty>> LogoutAsync(
            string accessToken,
            CancellationToken cancellationToken)
        {
            return ExecuteEmptyAsync(
                ClientHttpOperationCatalog.LogoutSession,
                requestBody: null,
                accessToken,
                cancellationToken);
        }

        /// <inheritdoc />
        public Task<ClientHttpResult<ClientConnectionTicket>> IssueConnectionTicketAsync(
            string accessToken,
            ClientEndpointChannel channel,
            CancellationToken cancellationToken)
        {
            return ExecuteAsync(
                ClientHttpOperationCatalog.IssueConnectionTicket,
                _codec.EncodeTicket(channel),
                accessToken,
                _codec.DecodeConnectionTicket,
                cancellationToken);
        }

        /// <inheritdoc />
        public Task<ClientHttpResult<ClientWorldBootstrap>> GetWorldBootstrapAsync(
            string accessToken,
            CancellationToken cancellationToken)
        {
            return ExecuteAsync(
                ClientHttpOperationCatalog.GetWorldBootstrap,
                requestBody: null,
                accessToken,
                _codec.DecodeWorldBootstrap,
                cancellationToken);
        }

        /// <inheritdoc />
        public Task<ClientHttpResult<ClientWorldAdmission>> IssueWorldAdmissionAsync(
            string accessToken,
            ClientWorldAdmissionTarget target,
            string idempotencyKey,
            CancellationToken cancellationToken)
        {
            return ExecuteAsync(
                ClientHttpOperationCatalog.IssueWorldAdmission,
                _codec.EncodeWorldAdmission(target),
                accessToken,
                _codec.DecodeWorldAdmission,
                cancellationToken,
                idempotencyKey);
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
        /// <returns>成功、服务端错误或本地失败三选一结果。</returns>
        private async Task<ClientHttpResult<T>> ExecuteAsync<T>(
            ClientHttpOperation operation,
            byte[] requestBody,
            string bearerToken,
            Func<ReadOnlyMemory<byte>, T> decodeSuccess,
            CancellationToken cancellationToken,
            string idempotencyKey = null)
        {
            var raw = await _transport.SendAsync(
                operation,
                requestBody,
                bearerToken,
                cancellationToken,
                idempotencyKey);
            if (!raw.HasResponse)
            {
                return ClientHttpResult<T>.Failed(raw.Failure);
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

                    return ClientHttpResult<T>.Success(decodeSuccess(response.Body));
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
        private async Task<ClientHttpResult<ClientHttpEmpty>> ExecuteEmptyAsync(
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
                return ClientHttpResult<ClientHttpEmpty>.Failed(raw.Failure);
            }

            var response = raw.Response;
            if (response.StatusCode == operation.SuccessStatus)
            {
                return response.Body.Length == 0
                    ? ClientHttpResult<ClientHttpEmpty>.Success(new ClientHttpEmpty())
                    : Malformed<ClientHttpEmpty>(operation, response.StatusCode);
            }

            try
            {
                return DecodeRejected<ClientHttpEmpty>(operation, response);
            }
            catch (ClientHttpContractException)
            {
                return Malformed<ClientHttpEmpty>(operation, response.StatusCode);
            }
        }

        /// <summary>
        /// 解码 4xx/5xx ErrorResponse，并拒绝 redirect 或其他非错误 status。
        /// </summary>
        /// <typeparam name="T">原 operation 成功投影类型。</typeparam>
        /// <param name="operation">冻结 descriptor。</param>
        /// <param name="response">完整有界响应。</param>
        /// <returns>结构有效的服务端拒绝或 malformed failure。</returns>
        private ClientHttpResult<T> DecodeRejected<T>(
            ClientHttpOperation operation,
            ClientHttpRawResponse response)
        {
            var status = (int)response.StatusCode;
            if (status < 400 || status > 599 || !IsJson(response.MediaType) || response.Body.Length == 0)
            {
                return Malformed<T>(operation, response.StatusCode);
            }

            var serverError = _codec.DecodeServerError(response.Body, response.StatusCode);
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

            return ClientHttpResult<T>.Rejected(serverError);
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
        private static ClientHttpResult<T> Malformed<T>(
            ClientHttpOperation operation,
            HttpStatusCode statusCode)
        {
            return ClientHttpResult<T>.Failed(new ClientHttpFailure(
                ClientHttpFailureKind.MalformedResponse,
                operation.OperationID,
                statusCode));
        }
    }
}
