using System;
using System.IO;
using System.Linq;
using System.Net;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Core.Configuration;
using IHomeland.Client.Infrastructure.Http;
using NUnit.Framework;

namespace IHomeland.Client.Tests.EditMode
{
    /// <summary>
    /// 验证共享 HTTP transport 的边界、取消分类、停止排空与强类型响应映射。
    /// </summary>
    public sealed class ClientHttpTransportTests
    {
        /// <summary>
        /// 验证初始化不联网，显式调用才发送冻结 method/path/header。
        /// </summary>
        /// <returns>等待请求捕获与 transport 停止完成的任务。</returns>
        [Test]
        public async Task InitializeIsOfflineAndRequestUsesFrozenMetadata()
        {
            HttpRequestMessage captured = null;
            var handler = new DelegateHandler((request, _) =>
            {
                captured = request;
                return Task.FromResult(JsonResponse(
                    HttpStatusCode.OK,
                    "{\"minimumClientVersion\":\"0.1.0\",\"protocolVersion\":1,\"serverVersion\":\"0.1.0\"}"));
            });
            var transport = CreateTransport(handler);
            await transport.InitializeAsync(CancellationToken.None);
            Assert.That(handler.SendCount, Is.EqualTo(0));

            var api = new ClientHttpApi(transport, new ClientHttpCodec());
            var result = await api.GetVersionAsync(CancellationToken.None);

            Assert.That(result.IsSuccess, Is.True);
            Assert.That(handler.SendCount, Is.EqualTo(1));
            Assert.That(captured.Method, Is.EqualTo(HttpMethod.Get));
            Assert.That(captured.RequestUri.AbsoluteUri, Is.EqualTo("http://127.0.0.1:8080/v1/version"));
            Assert.That(captured.Headers.Authorization, Is.Null);
            await transport.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 验证 Bearer operation 只通过 Authorization header 传递 token，且 204 必须为空 body。
        /// </summary>
        /// <returns>等待 logout 与停止完成的任务。</returns>
        [Test]
        public async Task LogoutUsesBearerAndRequiresEmptyNoContentBody()
        {
            const string accessToken = "access-token-must-not-leak";
            string authorization = null;
            var handler = new DelegateHandler((request, _) =>
            {
                authorization = request.Headers.Authorization?.ToString();
                return Task.FromResult(new HttpResponseMessage(HttpStatusCode.NoContent)
                {
                    Content = new ByteArrayContent(Array.Empty<byte>()),
                });
            });
            var transport = CreateTransport(handler);
            await transport.InitializeAsync(CancellationToken.None);
            var api = new ClientHttpApi(transport, new ClientHttpCodec());

            var result = await api.LogoutAsync(accessToken, CancellationToken.None);

            Assert.That(result.IsSuccess, Is.True);
            Assert.That(authorization, Is.EqualTo($"Bearer {accessToken}"));
            await transport.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 验证 world admission 精确使用 POST、Bearer、Idempotency-Key 与封闭 JSON body。
        /// </summary>
        /// <returns>等待强类型请求与响应解码完成的任务。</returns>
        [Test]
        public async Task WorldAdmissionUsesFrozenHeaderAndSchema()
        {
            const string key = "fixture-admission-key-01";
            string method = null;
            string path = null;
            string idempotencyKey = null;
            string body = null;
            var handler = new DelegateHandler(async (request, cancellationToken) =>
            {
                method = request.Method.Method;
                path = request.RequestUri.AbsolutePath;
                idempotencyKey = request.Headers.GetValues("Idempotency-Key").Single();
                body = await request.Content.ReadAsStringAsync();
                return JsonResponse(
                    HttpStatusCode.Created,
                    "{\"credential\":\"wad1_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\",\"endpoint\":{\"channel\":\"TLS_TCP\",\"host\":\"127.0.0.1\",\"port\":4433},\"expiresAtMs\":2000,\"purpose\":\"OWN_WORLD\",\"role\":\"OWNER\"}");
            });
            var transport = CreateTransport(handler);
            await transport.InitializeAsync(CancellationToken.None);
            var api = new ClientHttpApi(transport, new ClientHttpCodec());

            var result = await api.IssueWorldAdmissionAsync(
                "access-token",
                ClientWorldAdmissionTarget.OwnWorld(),
                key,
                CancellationToken.None);

            Assert.That(result.IsSuccess, Is.True);
            Assert.That(method, Is.EqualTo("POST"));
            Assert.That(path, Is.EqualTo("/v1/world/admissions"));
            Assert.That(idempotencyKey, Is.EqualTo(key));
            Assert.That(body, Is.EqualTo("{\"kind\":\"OWN_WORLD\"}"));
            await transport.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 验证 invite accept 精确绑定 path、Bearer、Idempotency-Key 与唯一 body 字段。
        /// </summary>
        /// <returns>等待强类型请求与 reservation 解码完成的任务。</returns>
        [Test]
        public async Task VisitInviteAcceptUsesFrozenPathHeaderAndSchema()
        {
            const string key = "fixture-accept-key-0001";
            string authorization = null;
            string idempotencyKey = null;
            string body = null;
            Uri requestUri = null;
            var handler = new DelegateHandler(async (request, cancellationToken) =>
            {
                requestUri = request.RequestUri;
                authorization = request.Headers.Authorization?.ToString();
                idempotencyKey = request.Headers.GetValues("Idempotency-Key").Single();
                body = await request.Content.ReadAsStringAsync();
                return JsonResponse(
                    HttpStatusCode.OK,
                    "{\"reservation\":{\"visitSessionId\":\"visit:fixture\",\"revision\":5,\"reservationExpiresAtMs\":9000}}");
            });
            var transport = CreateTransport(handler);
            await transport.InitializeAsync(CancellationToken.None);
            var api = new ClientHttpApi(transport, new ClientHttpCodec());

            var result = await api.AcceptVisitInviteAsync(
                "access-token",
                new ClientVisitInviteAcceptRequest("visit:fixture", "invite_fixture", 4),
                key,
                CancellationToken.None);

            Assert.That(result.IsSuccess, Is.True);
            Assert.That(
                requestUri.AbsolutePath,
                Is.EqualTo("/v1/visits/visit%3Afixture/invites/invite_fixture/accept"));
            Assert.That(authorization, Is.EqualTo("Bearer access-token"));
            Assert.That(idempotencyKey, Is.EqualTo(key));
            using (var document = JsonDocument.Parse(body))
            {
                Assert.That(document.RootElement.EnumerateObject().Count(), Is.EqualTo(1));
                Assert.That(document.RootElement.GetProperty("expectedRevision").GetInt64(), Is.EqualTo(4));
            }

            await transport.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 验证非法 accept identity 在创建 HTTP request 前收敛为稳定本地策略失败。
        /// </summary>
        /// <returns>等待本地拒绝与 transport 停止完成的任务。</returns>
        [Test]
        public async Task InvalidVisitInviteAcceptIsRejectedBeforeRequestCreation()
        {
            var calls = 0;
            var handler = new DelegateHandler((_, __) =>
            {
                calls++;
                return Task.FromResult(JsonResponse(HttpStatusCode.OK, "{}"));
            });
            var transport = CreateTransport(handler);
            await transport.InitializeAsync(CancellationToken.None);
            var api = new ClientHttpApi(transport, new ClientHttpCodec());

            var result = await api.AcceptVisitInviteAsync(
                "access-token",
                new ClientVisitInviteAcceptRequest("visit/escape", "invite_fixture", 4),
                "fixture-accept-key-0002",
                CancellationToken.None);

            Assert.That(result.IsSuccess, Is.False);
            Assert.That(result.Failure.Kind, Is.EqualTo(ClientHttpFailureKind.LocalPolicy));
            Assert.That(calls, Is.Zero);
            await transport.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 验证超出 operation response hard cap 的响应在 JSON 解析前被拒绝。
        /// </summary>
        /// <returns>等待有界读取与停止完成的任务。</returns>
        [Test]
        public async Task OversizedResponseIsRejectedBeforeCodec()
        {
            var handler = new DelegateHandler((_, __) => Task.FromResult(new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new ByteArrayContent(new byte[(16 * 1024) + 1]),
            }));
            var transport = CreateTransport(handler);
            await transport.InitializeAsync(CancellationToken.None);

            var raw = await transport.SendAsync(
                ClientHttpOperationCatalog.GetVersion,
                requestBody: null,
                bearerToken: null,
                CancellationToken.None);

            Assert.That(raw.HasResponse, Is.False);
            Assert.That(raw.Failure.Kind, Is.EqualTo(ClientHttpFailureKind.ResponseTooLarge));
            Assert.That(raw.Failure.ToString(), Does.Not.Contain("byte["));
            await transport.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 验证 caller cancellation 与 operation deadline 形成不同稳定失败类别。
        /// </summary>
        /// <returns>等待两种 linked cancellation 完成的任务。</returns>
        [Test]
        public async Task CancellationOwnerIsClassified()
        {
            var handler = new DelegateHandler(async (_, token) =>
            {
                await Task.Delay(Timeout.Infinite, token);
                throw new InvalidOperationException("取消后不应继续执行。");
            });
            var transport = CreateTransport(handler);
            await transport.InitializeAsync(CancellationToken.None);
            using (var callerCancellation = new CancellationTokenSource())
            {
                callerCancellation.Cancel();
                var callerResult = await transport.SendAsync(
                    ClientHttpOperationCatalog.GetVersion,
                    null,
                    null,
                    callerCancellation.Token);
                Assert.That(callerResult.Failure.Kind, Is.EqualTo(ClientHttpFailureKind.CallerCancelled));
            }

            var shortOperation = new ClientHttpOperation(
                "shortDeadline",
                HttpMethod.Get,
                "/deadline",
                ClientHttpAuthentication.Anonymous,
                ClientHttpBodyPolicy.None,
                0,
                HttpStatusCode.OK,
                true,
                TimeSpan.FromMilliseconds(20),
                1024);
            var timeoutResult = await transport.SendAsync(
                shortOperation,
                null,
                null,
                CancellationToken.None);
            Assert.That(timeoutResult.Failure.Kind, Is.EqualTo(ClientHttpFailureKind.Timeout));
            await transport.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 验证并发 Stop 共享一个任务、取消 in-flight 并只释放 handler 一次。
        /// </summary>
        /// <returns>等待请求进入 handler、共享停止与迟到完成拒绝。</returns>
        [Test]
        public async Task ConcurrentStopCancelsInFlightAndDisposesOnce()
        {
            var entered = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
            var handler = new DelegateHandler(async (_, token) =>
            {
                entered.TrySetResult(true);
                await Task.Delay(Timeout.Infinite, token);
                return JsonResponse(HttpStatusCode.OK, "{}");
            });
            var transport = CreateTransport(handler);
            await transport.InitializeAsync(CancellationToken.None);
            var request = transport.SendAsync(
                ClientHttpOperationCatalog.GetVersion,
                null,
                null,
                CancellationToken.None);
            await entered.Task;

            var firstStop = transport.StopAsync(CancellationToken.None);
            var secondStop = transport.StopAsync(CancellationToken.None);
            Assert.That(secondStop, Is.SameAs(firstStop));
            await firstStop;

            var requestResult = await request;
            Assert.That(requestResult.Failure.Kind, Is.EqualTo(ClientHttpFailureKind.Stopped));
            Assert.That(handler.DisposeCount, Is.EqualTo(1));
            var late = await transport.SendAsync(
                ClientHttpOperationCatalog.GetVersion,
                null,
                null,
                CancellationToken.None);
            Assert.That(late.Failure.Kind, Is.EqualTo(ClientHttpFailureKind.Stopped));
        }

        /// <summary>
        /// 验证强类型 API 对 Content-Type、status/body 和 Retry-After 矛盾执行 fail closed。
        /// </summary>
        /// <returns>等待错误映射与停止完成的任务。</returns>
        [Test]
        public async Task ApiRejectsMalformedStatusAndRetryAfter()
        {
            var call = 0;
            var handler = new DelegateHandler((_, __) =>
            {
                call++;
                if (call == 1)
                {
                    return Task.FromResult(new HttpResponseMessage(HttpStatusCode.OK)
                    {
                        Content = new StringContent("{}", Encoding.UTF8, "text/plain"),
                    });
                }

                var response = JsonResponse(
                    HttpStatusCode.ServiceUnavailable,
                    "{\"code\":500,\"messageKey\":\"error.dependency.unavailable\",\"requestId\":\"fixture-request-id\",\"retryable\":true,\"retryAfterMs\":1000}");
                response.Headers.RetryAfter = new System.Net.Http.Headers.RetryConditionHeaderValue(
                    TimeSpan.FromSeconds(2));
                return Task.FromResult(response);
            });
            var transport = CreateTransport(handler);
            await transport.InitializeAsync(CancellationToken.None);
            var api = new ClientHttpApi(transport, new ClientHttpCodec());

            var contentTypeFailure = await api.GetVersionAsync(CancellationToken.None);
            Assert.That(contentTypeFailure.Failure.Kind, Is.EqualTo(ClientHttpFailureKind.MalformedResponse));
            var retryAfterFailure = await api.GetWorldBootstrapAsync(
                "access-token",
                CancellationToken.None);
            Assert.That(retryAfterFailure.Failure.Kind, Is.EqualTo(ClientHttpFailureKind.MalformedResponse));
            await transport.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 创建使用 loopback local 环境的测试 transport。
        /// </summary>
        /// <param name="handler">受测试控制且由 transport 拥有的 handler。</param>
        /// <returns>尚未初始化的 transport。</returns>
        private static ClientHttpTransport CreateTransport(HttpMessageHandler handler)
        {
            var environment = ClientEnvironment.Create(
                ClientEnvironmentKind.Test,
                "http://127.0.0.1:8080/",
                "0.1.0",
                1);
            return new ClientHttpTransport(environment, handler, disposeHandler: true);
        }

        /// <summary>
        /// 创建携带 application/json body 的测试响应。
        /// </summary>
        /// <param name="statusCode">HTTP status。</param>
        /// <param name="json">受测试控制的 JSON 文本。</param>
        /// <returns>由调用方或 transport 释放的响应。</returns>
        private static HttpResponseMessage JsonResponse(HttpStatusCode statusCode, string json)
        {
            return new HttpResponseMessage(statusCode)
            {
                Content = new StringContent(json, Encoding.UTF8, "application/json"),
            };
        }

        /// <summary>
        /// 把 HttpClient 调用转交给确定性测试 delegate，并记录发送与释放次数。
        /// </summary>
        private sealed class DelegateHandler : HttpMessageHandler
        {
            /// <summary>
            /// 保存测试提供的异步响应行为。
            /// </summary>
            private readonly Func<HttpRequestMessage, CancellationToken, Task<HttpResponseMessage>> _send;

            /// <summary>
            /// 创建 delegate 驱动的 handler。
            /// </summary>
            /// <param name="send">每次请求调用一次的响应函数。</param>
            internal DelegateHandler(
                Func<HttpRequestMessage, CancellationToken, Task<HttpResponseMessage>> send)
            {
                _send = send ?? throw new ArgumentNullException(nameof(send));
            }

            /// <summary>
            /// 获取 SendAsync 被调用的次数。
            /// </summary>
            internal int SendCount { get; private set; }

            /// <summary>
            /// 获取 Dispose 被调用的次数。
            /// </summary>
            internal int DisposeCount { get; private set; }

            /// <summary>
            /// 记录请求并执行测试 delegate。
            /// </summary>
            /// <param name="request">HttpClient 生成的请求。</param>
            /// <param name="cancellationToken">Transport 组合的取消信号。</param>
            /// <returns>测试 delegate 返回的响应。</returns>
            protected override Task<HttpResponseMessage> SendAsync(
                HttpRequestMessage request,
                CancellationToken cancellationToken)
            {
                SendCount++;
                return _send(request, cancellationToken);
            }

            /// <summary>
            /// 记录 handler 由 transport 释放，供资源所有权断言。
            /// </summary>
            /// <param name="disposing">是否执行托管资源释放。</param>
            protected override void Dispose(bool disposing)
            {
                if (disposing)
                {
                    DisposeCount++;
                }

                base.Dispose(disposing);
            }
        }
    }
}
