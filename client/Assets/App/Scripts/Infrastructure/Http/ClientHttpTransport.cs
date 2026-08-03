using System;
using System.IO;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Contracts;
using IHomeland.Client.Application.Configuration;
using IHomeland.Client.Foundation.Lifetime;

namespace IHomeland.Client.Infrastructure.Http
{
    /// <summary>
    /// 复用单一 HttpClient，有界发送冻结 operation，并由 AppLifetime 拥有全部 in-flight 请求。
    /// </summary>
    /// <remarks>
    /// 锁只保护 lifecycle state、active count 与 drain completion，不在锁内执行 I/O。Stop 先撤销
    /// lifetime token，再等待 active 请求离开 finally，确保迟到 completion 不越过旧 App Scope。
    /// </remarks>
    internal sealed class ClientHttpTransport : IAppLifetimeParticipant
    {
        /// <summary>
        /// 标识 transport 不可逆的 App Scope 生命周期。
        /// </summary>
        private enum TransportState
        {
            /// <summary>
            /// 尚未允许发送请求。
            /// </summary>
            Created = 0,

            /// <summary>
            /// 已初始化并允许发送请求。
            /// </summary>
            Running = 1,

            /// <summary>
            /// 正在取消并排空请求。
            /// </summary>
            Stopping = 2,

            /// <summary>
            /// 资源已释放且不能重新启动。
            /// </summary>
            Stopped = 3,
        }

        /// <summary>
        /// 保存流式读取使用的固定 buffer 大小，避免按不可信 Content-Length 分配。
        /// </summary>
        private const int ReadBufferBytes = 4096;

        /// <summary>
        /// 保护 lifecycle state、active count 和 drain completion 引用。
        /// </summary>
        private readonly object _sync = new object();

        /// <summary>
        /// 保存已验证 base URI，并且永不从响应或 payload 改写。
        /// </summary>
        private readonly ClientEnvironment _environment;

        /// <summary>
        /// 保存单一连接池 owner；Timeout 关闭以统一使用 operation linked deadline。
        /// </summary>
        private readonly HttpClient _httpClient;

        /// <summary>
        /// 停止时取消全部 active 请求，且不会被任一单独调用方拥有。
        /// </summary>
        private readonly CancellationTokenSource _lifetimeCancellation = new CancellationTokenSource();

        /// <summary>
        /// 保存当前不可逆 lifecycle state，只能在 <see cref="_sync"/> 内访问。
        /// </summary>
        private TransportState _state = TransportState.Created;

        /// <summary>
        /// 保存已经取得请求所有权但尚未离开 finally 的调用数量。
        /// </summary>
        private int _activeRequests;

        /// <summary>
        /// 保存当前 active 批次归零信号；没有 active 请求时为空。
        /// </summary>
        private TaskCompletionSource<bool> _drained;

        /// <summary>
        /// 保存首次停止调用创建的共享任务，使并发 Stop 不会重复取消或释放资源。
        /// </summary>
        private Task _stopTask;

        /// <summary>
        /// 表示共享 HttpClient 与 cancellation source 已经释放。
        /// </summary>
        private bool _disposed;

        /// <summary>
        /// 创建拥有默认安全 HttpClientHandler 的 production transport。
        /// </summary>
        /// <param name="environment">已在任何网络副作用前验证的环境快照。</param>
        internal ClientHttpTransport(ClientEnvironment environment)
            : this(environment, CreateDefaultHandler(), disposeHandler: true)
        {
        }

        /// <summary>
        /// 创建使用可控 handler 的 transport，供 contract tests 注入确定性响应。
        /// </summary>
        /// <param name="environment">已验证且不会被 transport 修改的环境快照。</param>
        /// <param name="handler">拥有 SendAsync 行为的非空 handler。</param>
        /// <param name="disposeHandler">Transport 停止时是否一并释放 handler。</param>
        /// <exception cref="ArgumentNullException">Environment 或 handler 为空时抛出。</exception>
        internal ClientHttpTransport(
            ClientEnvironment environment,
            HttpMessageHandler handler,
            bool disposeHandler)
        {
            _environment = environment ?? throw new ArgumentNullException(nameof(environment));
            if (handler == null)
            {
                throw new ArgumentNullException(nameof(handler));
            }

            _httpClient = new HttpClient(handler, disposeHandler)
            {
                BaseAddress = _environment.HttpBaseUri,
                Timeout = Timeout.InfiniteTimeSpan,
            };
        }

        /// <summary>
        /// 进入 Running 状态但不发送任何请求。
        /// </summary>
        /// <param name="cancellationToken">初始化开始前检查的 AppLifetime 取消信号。</param>
        /// <returns>Transport 已允许显式 operation 时完成的任务。</returns>
        /// <exception cref="InvalidOperationException">重复初始化或停止后重启时抛出。</exception>
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            lock (_sync)
            {
                if (_state != TransportState.Created)
                {
                    throw new InvalidOperationException("ClientHttpTransport 不能重复初始化或停止后重启。");
                }

                _state = TransportState.Running;
            }

            return Task.CompletedTask;
        }

        /// <summary>
        /// 发送单个冻结 operation，并有界读取完整响应。
        /// </summary>
        /// <param name="operation">Catalog 提供的不可变 descriptor。</param>
        /// <param name="requestBody">JSON operation 的 UTF-8 body；无 body operation 传空。</param>
        /// <param name="bearerToken">Bearer operation 的当前 access token；匿名 operation 传空。</param>
        /// <param name="cancellationToken">调用方取消等待的信号；取消不证明远端未收到请求。</param>
        /// <returns>完整 raw response 或稳定本地失败；不会自动重试。</returns>
        /// <exception cref="ArgumentNullException">Operation 为空时抛出。</exception>
        internal async Task<ClientHttpRawResult> SendAsync(
            ClientHttpOperation operation,
            byte[] requestBody,
            string bearerToken,
            CancellationToken cancellationToken)
        {
            return await SendAsync(operation, requestBody, bearerToken, cancellationToken, null, null);
        }

        /// <summary>
        /// 发送可选带冻结 Idempotency-Key 的单个 operation。
        /// </summary>
        /// <param name="operation">Catalog 提供的不可变 descriptor。</param>
        /// <param name="requestBody">JSON operation 的 UTF-8 body。</param>
        /// <param name="bearerToken">Bearer operation 的当前 access token。</param>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <param name="idempotencyKey">仅冻结幂等 operation 使用的安全 ASCII key。</param>
        /// <returns>完整 raw response 或稳定本地失败。</returns>
        internal async Task<ClientHttpRawResult> SendAsync(
            ClientHttpOperation operation,
            byte[] requestBody,
            string bearerToken,
            CancellationToken cancellationToken,
            string idempotencyKey)
        {
            return await SendAsync(
                operation,
                requestBody,
                bearerToken,
                cancellationToken,
                idempotencyKey,
                null);
        }

        /// <summary>
        /// 发送可选带冻结 path parameter 与 Idempotency-Key 的单个 operation。
        /// </summary>
        /// <param name="operation">Catalog 提供的不可变 descriptor。</param>
        /// <param name="requestBody">JSON operation 的 UTF-8 body。</param>
        /// <param name="bearerToken">Bearer operation 的当前 access token。</param>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <param name="idempotencyKey">仅冻结幂等 operation 使用的安全 ASCII key。</param>
        /// <param name="requestPath">仅 acceptVisitInvite 使用的已绑定根相对 path。</param>
        /// <returns>完整 raw response 或稳定本地失败。</returns>
        internal async Task<ClientHttpRawResult> SendAsync(
            ClientHttpOperation operation,
            byte[] requestBody,
            string bearerToken,
            CancellationToken cancellationToken,
            string idempotencyKey,
            string requestPath)
        {
            if (operation == null)
            {
                throw new ArgumentNullException(nameof(operation));
            }

            var localFailure = ValidateRequest(
                operation,
                requestBody,
                bearerToken,
                idempotencyKey,
                requestPath);
            if (localFailure != null)
            {
                return ClientHttpRawResult.Failed(localFailure);
            }

            if (!TryBeginRequest())
            {
                return ClientHttpRawResult.Failed(new ClientGatewayFailure(
                    ClientGatewayFailureKind.Stopped,
                    operation.OperationID));
            }

            using (var deadlineCancellation = new CancellationTokenSource(operation.Timeout))
            using (var linkedCancellation = CancellationTokenSource.CreateLinkedTokenSource(
                       cancellationToken,
                       deadlineCancellation.Token,
                       _lifetimeCancellation.Token))
            using (var request = CreateRequest(
                       operation,
                       requestBody,
                       bearerToken,
                       idempotencyKey,
                       requestPath))
            {
                try
                {
                    using (var response = await _httpClient.SendAsync(
                               request,
                               HttpCompletionOption.ResponseHeadersRead,
                               linkedCancellation.Token))
                    {
                        if (response.Content.Headers.ContentLength.HasValue &&
                            response.Content.Headers.ContentLength.Value > operation.ResponseBodyLimitBytes)
                        {
                            return ClientHttpRawResult.Failed(new ClientGatewayFailure(
                                ClientGatewayFailureKind.ResponseTooLarge,
                                operation.OperationID));
                        }

                        var body = await ReadBodyAsync(
                            response,
                            operation.ResponseBodyLimitBytes,
                            linkedCancellation.Token);
                        if (body == null)
                        {
                            return ClientHttpRawResult.Failed(new ClientGatewayFailure(
                                ClientGatewayFailureKind.ResponseTooLarge,
                                operation.OperationID));
                        }

                        var mediaType = response.Content.Headers.ContentType?.MediaType;
                        var retryAfter = response.Headers.RetryAfter?.Delta;
                        return ClientHttpRawResult.Received(new ClientHttpRawResponse(
                            response.StatusCode,
                            mediaType,
                            body,
                            retryAfter));
                    }
                }
                catch (OperationCanceledException)
                {
                    return ClientHttpRawResult.Failed(new ClientGatewayFailure(
                        ClassifyCancellation(cancellationToken, deadlineCancellation.Token),
                        operation.OperationID));
                }
                catch (HttpRequestException)
                {
                    return ClientHttpRawResult.Failed(new ClientGatewayFailure(
                        ClientGatewayFailureKind.Transport,
                        operation.OperationID));
                }
                catch (IOException)
                {
                    return ClientHttpRawResult.Failed(new ClientGatewayFailure(
                        ClientGatewayFailureKind.Transport,
                        operation.OperationID));
                }
                finally
                {
                    EndRequest();
                }
            }
        }

        /// <summary>
        /// 停止接受新请求、取消 active 请求并在共享 deadline 内排空所有 finally。
        /// </summary>
        /// <param name="cancellationToken">AppLifetime 为当前 participant 分配的停止 deadline。</param>
        /// <returns>Active request 全部归零且 HttpClient 已释放时完成的任务。</returns>
        public Task StopAsync(CancellationToken cancellationToken)
        {
            Task drainTask;
            TaskCompletionSource<bool> stopCompletion;
            lock (_sync)
            {
                if (_stopTask != null)
                {
                    return _stopTask;
                }

                _state = TransportState.Stopping;
                drainTask = _activeRequests == 0
                    ? Task.CompletedTask
                    : _drained.Task;
                stopCompletion = new TaskCompletionSource<bool>(
                    TaskCreationOptions.RunContinuationsAsynchronously);
                _stopTask = stopCompletion.Task;
            }

            _ = CompleteStopAsync(drainTask, cancellationToken, stopCompletion);
            return stopCompletion.Task;
        }

        /// <summary>
        /// 在 lifecycle 锁外启动唯一停止流程，并把异常转交给共享任务观察。
        /// </summary>
        /// <param name="drainTask">首次停止时捕获的 active request 排空信号。</param>
        /// <param name="cancellationToken">AppLifetime 为该 participant 分配的停止 deadline。</param>
        /// <param name="completion">并发 Stop 共同等待的 completion owner。</param>
        /// <returns>内部桥接流程完成时结束的任务；结果由 completion 公开。</returns>
        private async Task CompleteStopAsync(
            Task drainTask,
            CancellationToken cancellationToken,
            TaskCompletionSource<bool> completion)
        {
            try
            {
                await StopCoreAsync(drainTask, cancellationToken);
                completion.TrySetResult(true);
            }
            catch (Exception error)
            {
                completion.TrySetException(error);
            }
        }

        /// <summary>
        /// 执行唯一一次 transport 取消、排空与资源释放。
        /// </summary>
        /// <param name="drainTask">首次停止时捕获的 active request 排空信号。</param>
        /// <param name="cancellationToken">AppLifetime 为该 participant 分配的停止 deadline。</param>
        /// <returns>资源释放完成时结束的共享停止任务。</returns>
        private async Task StopCoreAsync(Task drainTask, CancellationToken cancellationToken)
        {
            _lifetimeCancellation.Cancel();
            try
            {
                await AwaitWithCancellationAsync(drainTask, cancellationToken);
            }
            finally
            {
                lock (_sync)
                {
                    _state = TransportState.Stopped;
                    if (!_disposed)
                    {
                        _disposed = true;
                        _httpClient.Dispose();
                        _lifetimeCancellation.Dispose();
                    }
                }
            }
        }

        /// <summary>
        /// 创建禁止自动 redirect 的默认 handler，避免 credential 被转发到未登记 endpoint。
        /// </summary>
        /// <returns>由 transport 停止时释放的安全 handler。</returns>
        private static HttpMessageHandler CreateDefaultHandler()
        {
            return new HttpClientHandler
            {
                AllowAutoRedirect = false,
            };
        }

        /// <summary>
        /// 验证 body 与 credential 是否匹配 operation descriptor。
        /// </summary>
        /// <param name="operation">冻结 descriptor。</param>
        /// <param name="requestBody">待发送 body。</param>
        /// <param name="bearerToken">待发送 credential。</param>
        /// <param name="idempotencyKey">可选幂等 header。</param>
        /// <param name="requestPath">可选冻结 path parameter 绑定结果。</param>
        /// <returns>违反本地策略时返回失败，否则为空。</returns>
        private static ClientGatewayFailure ValidateRequest(
            ClientHttpOperation operation,
            byte[] requestBody,
            string bearerToken,
            string idempotencyKey,
            string requestPath)
        {
            var hasBody = requestBody != null && requestBody.Length > 0;
            if (operation.RequestBodyPolicy == ClientHttpBodyPolicy.None && hasBody)
            {
                return new ClientGatewayFailure(ClientGatewayFailureKind.LocalPolicy, operation.OperationID);
            }

            if (operation.RequestBodyPolicy == ClientHttpBodyPolicy.Json && !hasBody)
            {
                return new ClientGatewayFailure(ClientGatewayFailureKind.LocalPolicy, operation.OperationID);
            }

            if (hasBody && requestBody.Length > operation.RequestBodyLimitBytes)
            {
                return new ClientGatewayFailure(ClientGatewayFailureKind.LocalPolicy, operation.OperationID);
            }

            var hasBearer = !string.IsNullOrEmpty(bearerToken);
            if ((operation.Authentication == ClientHttpAuthentication.Bearer) != hasBearer)
            {
                return new ClientGatewayFailure(ClientGatewayFailureKind.LocalPolicy, operation.OperationID);
            }

            var requiresIdempotencyKey = ReferenceEquals(operation, ClientHttpOperationCatalog.AcceptVisitInvite) ||
                                         ReferenceEquals(operation, ClientHttpOperationCatalog.IssueWorldAdmission) ||
                                         ReferenceEquals(operation, ClientHttpOperationCatalog.IssueBattleTicket);
            if (requiresIdempotencyKey != !string.IsNullOrEmpty(idempotencyKey) ||
                (requiresIdempotencyKey && !IsValidIdempotencyKey(idempotencyKey)))
            {
                return new ClientGatewayFailure(ClientGatewayFailureKind.LocalPolicy, operation.OperationID);
            }

            var requiresBoundPath = ReferenceEquals(operation, ClientHttpOperationCatalog.AcceptVisitInvite);
            if (requiresBoundPath != !string.IsNullOrEmpty(requestPath) ||
                (requiresBoundPath && !IsValidVisitInviteAcceptPath(requestPath)))
            {
                return new ClientGatewayFailure(ClientGatewayFailureKind.LocalPolicy, operation.OperationID);
            }

            return null;
        }

        /// <summary>
        /// 构造只包含 catalog path、JSON content 与可选 Bearer header 的单次请求。
        /// </summary>
        /// <param name="operation">冻结 descriptor。</param>
        /// <param name="requestBody">已通过上限验证的 JSON body。</param>
        /// <param name="bearerToken">当前 session access token。</param>
        /// <param name="idempotencyKey">可选幂等 header。</param>
        /// <param name="requestPath">可选冻结 path parameter 绑定结果。</param>
        /// <returns>由调用方 using 释放的请求。</returns>
        private static HttpRequestMessage CreateRequest(
            ClientHttpOperation operation,
            byte[] requestBody,
            string bearerToken,
            string idempotencyKey,
            string requestPath)
        {
            var request = new HttpRequestMessage(
                operation.Method,
                string.IsNullOrEmpty(requestPath) ? operation.RelativePath : requestPath);
            request.Headers.Accept.Add(new MediaTypeWithQualityHeaderValue("application/json"));
            if (operation.Authentication == ClientHttpAuthentication.Bearer)
            {
                request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", bearerToken);
            }

            if (!string.IsNullOrEmpty(idempotencyKey))
            {
                request.Headers.Add("Idempotency-Key", idempotencyKey);
            }

            if (operation.RequestBodyPolicy == ClientHttpBodyPolicy.Json)
            {
                request.Content = new ByteArrayContent(requestBody);
                request.Content.Headers.ContentType = new MediaTypeHeaderValue("application/json")
                {
                    CharSet = "utf-8",
                };
            }

            return request;
        }

        /// <summary>
        /// 验证 OpenAPI Idempotency-Key 的长度与安全 ASCII grammar。
        /// </summary>
        /// <param name="value">待发送 header value。</param>
        /// <returns>长度 16-128 且只含允许字符时返回 true。</returns>
        private static bool IsValidIdempotencyKey(string value)
        {
            if (value == null || value.Length < 16 || value.Length > 128)
            {
                return false;
            }

            foreach (var character in value)
            {
                if (!(character >= 'A' && character <= 'Z') &&
                    !(character >= 'a' && character <= 'z') &&
                    !(character >= '0' && character <= '9') &&
                    character != '.' && character != '_' && character != ':' && character != '-')
                {
                    return false;
                }
            }

            return true;
        }

        /// <summary>
        /// 验证 acceptVisitInvite 的 path 只能由两个规范 identity segment 构成。
        /// </summary>
        /// <param name="value">待发送根相对 path。</param>
        /// <returns>Path 与冻结模板完全匹配时返回 true。</returns>
        private static bool IsValidVisitInviteAcceptPath(string value)
        {
            if (string.IsNullOrEmpty(value))
            {
                return false;
            }

            var segments = value.Split('/');
            return segments.Length == 7 &&
                   segments[0].Length == 0 &&
                   string.Equals(segments[1], "v1", StringComparison.Ordinal) &&
                   string.Equals(segments[2], "visits", StringComparison.Ordinal) &&
                   IsCanonicalIdentitySegment(segments[3]) &&
                   string.Equals(segments[4], "invites", StringComparison.Ordinal) &&
                   IsCanonicalIdentitySegment(segments[5]) &&
                   string.Equals(segments[6], "accept", StringComparison.Ordinal);
        }

        /// <summary>
        /// 验证 path segment 解码后仍符合公开 identity grammar，且编码是唯一规范形式。
        /// </summary>
        /// <param name="segment">待验证 URI path segment。</param>
        /// <returns>Segment 为规范安全 identity 时返回 true。</returns>
        private static bool IsCanonicalIdentitySegment(string segment)
        {
            if (string.IsNullOrEmpty(segment))
            {
                return false;
            }

            string identity;
            try
            {
                identity = Uri.UnescapeDataString(segment);
            }
            catch (UriFormatException)
            {
                return false;
            }

            if (identity.Length < 1 || identity.Length > 128 ||
                !string.Equals(Uri.EscapeDataString(identity), segment, StringComparison.Ordinal))
            {
                return false;
            }

            foreach (var character in identity)
            {
                if (!(character >= 'A' && character <= 'Z') &&
                    !(character >= 'a' && character <= 'z') &&
                    !(character >= '0' && character <= '9') &&
                    character != '.' && character != '_' && character != ':' && character != '-')
                {
                    return false;
                }
            }

            return true;
        }

        /// <summary>
        /// 使用固定 buffer 读取响应，并在超过 hard cap 时放弃整个结果。
        /// </summary>
        /// <param name="response">已取得 headers 的响应。</param>
        /// <param name="maximumBytes">Operation response hard cap。</param>
        /// <param name="cancellationToken">Caller、deadline 与 lifetime 的 linked token。</param>
        /// <returns>完整 body；超过上限时为空。</returns>
        private static async Task<byte[]> ReadBodyAsync(
            HttpResponseMessage response,
            int maximumBytes,
            CancellationToken cancellationToken)
        {
            using (var stream = await response.Content.ReadAsStreamAsync())
            using (var buffer = new MemoryStream())
            {
                var chunk = new byte[ReadBufferBytes];
                try
                {
                    while (true)
                    {
                        var count = await stream.ReadAsync(
                            chunk,
                            0,
                            chunk.Length,
                            cancellationToken);
                        if (count == 0)
                        {
                            return buffer.ToArray();
                        }

                        if (buffer.Length + count > maximumBytes)
                        {
                            return null;
                        }

                        buffer.Write(chunk, 0, count);
                    }
                }
                finally
                {
                    Array.Clear(chunk, 0, chunk.Length);
                    if (buffer.TryGetBuffer(out var segment) &&
                        segment.Array != null)
                    {
                        Array.Clear(
                            segment.Array,
                            segment.Offset,
                            segment.Count);
                    }
                }
            }
        }

        /// <summary>
        /// 在锁内取得单次请求所有权，并为新的 active 批次创建 drain signal。
        /// </summary>
        /// <returns>Transport 仍为 Running 时返回 true。</returns>
        private bool TryBeginRequest()
        {
            lock (_sync)
            {
                if (_state != TransportState.Running)
                {
                    return false;
                }

                if (_activeRequests == 0)
                {
                    _drained = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
                }

                _activeRequests++;
                return true;
            }
        }

        /// <summary>
        /// 对称释放请求所有权，并在 active count 归零时唤醒 Stop。
        /// </summary>
        private void EndRequest()
        {
            TaskCompletionSource<bool> drained = null;
            lock (_sync)
            {
                _activeRequests--;
                if (_activeRequests == 0)
                {
                    drained = _drained;
                    _drained = null;
                }
            }

            drained?.TrySetResult(true);
        }

        /// <summary>
        /// 按调用方、AppLifetime、deadline 优先级稳定分类取消原因。
        /// </summary>
        /// <param name="callerToken">调用方取消信号。</param>
        /// <param name="deadlineToken">Operation deadline 信号。</param>
        /// <returns>不会泄漏内部 exception 的 failure kind。</returns>
        private ClientGatewayFailureKind ClassifyCancellation(
            CancellationToken callerToken,
            CancellationToken deadlineToken)
        {
            if (callerToken.IsCancellationRequested)
            {
                return ClientGatewayFailureKind.CallerCancelled;
            }

            if (_lifetimeCancellation.IsCancellationRequested)
            {
                return ClientGatewayFailureKind.Stopped;
            }

            return deadlineToken.IsCancellationRequested
                ? ClientGatewayFailureKind.Timeout
                : ClientGatewayFailureKind.Transport;
        }

        /// <summary>
        /// 等待 drain task，同时让 AppLifetime deadline 可以中止等待并触发 finally 释放。
        /// </summary>
        /// <param name="task">Active count 归零任务。</param>
        /// <param name="cancellationToken">停止 deadline。</param>
        /// <returns>Drain 完成时结束的任务。</returns>
        private static async Task AwaitWithCancellationAsync(
            Task task,
            CancellationToken cancellationToken)
        {
            if (task.IsCompleted)
            {
                await task;
                return;
            }

            var cancellationCompletion = new TaskCompletionSource<bool>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            using (cancellationToken.Register(() => cancellationCompletion.TrySetCanceled()))
            {
                var completed = await Task.WhenAny(task, cancellationCompletion.Task);
                await completed;
            }
        }
    }
}
