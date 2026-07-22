using System;
using System.IO;
using System.Linq;
using System.Net;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using IHomeland.Client.Core.Composition;
using IHomeland.Client.Core.Configuration;
using IHomeland.Client.Core.Lifetime;
using IHomeland.Client.Infrastructure.Http;
using IHomeland.Client.Presentation.Hosts;
using IHomeland.Client.Presentation.Hosts.UGUI;
using IHomeland.Client.Presentation.Hosts.UIToolkit;
using NUnit.Framework;
using UnityEngine;
using UnityEngine.InputSystem;

namespace IHomeland.Client.Tests.EditMode
{
    /// <summary>
    /// 冻结客户端环境安全边界、HTTP operation catalog 与显式 JSON codec 契约。
    /// </summary>
    public sealed class ClientHttpContractTests
    {
        /// <summary>
        /// 验证 Production 环境只接受 HTTPS base URI。
        /// </summary>
        [Test]
        public void ProductionEnvironmentRequiresHttps()
        {
            Assert.Throws<InvalidOperationException>(() => ClientEnvironment.Create(
                ClientEnvironmentKind.Production,
                "http://127.0.0.1:8080/",
                "0.1.0",
                1));

            var environment = ClientEnvironment.Create(
                ClientEnvironmentKind.Production,
                "https://api.example.invalid/",
                "0.1.0",
                1);

            Assert.That(environment.HttpBaseUri, Is.EqualTo(new Uri("https://api.example.invalid/")));
        }

        /// <summary>
        /// 验证 Local/Test 明文例外仍严格限制在 loopback。
        /// </summary>
        /// <param name="kind">允许本地明文的显式环境类别。</param>
        [TestCase(ClientEnvironmentKind.Local)]
        [TestCase(ClientEnvironmentKind.Test)]
        public void DevelopmentHttpRequiresLoopback(ClientEnvironmentKind kind)
        {
            var environment = ClientEnvironment.Create(kind, "http://127.0.0.1:8080", "0.1.0", 1);
            Assert.That(environment.HttpBaseUri.AbsoluteUri, Is.EqualTo("http://127.0.0.1:8080/"));

            Assert.Throws<InvalidOperationException>(() => ClientEnvironment.Create(
                kind,
                "http://api.example.invalid/",
                "0.1.0",
                1));
        }

        /// <summary>
        /// 验证 base URI 不得携带业务 path、query、fragment 或 userinfo。
        /// </summary>
        /// <param name="uri">应被启动前校验拒绝的 URI。</param>
        [TestCase("https://api.example.invalid/v1")]
        [TestCase("https://api.example.invalid/?region=test")]
        [TestCase("https://api.example.invalid/#fragment")]
        [TestCase("https://user:password@api.example.invalid/")]
        [TestCase("https://api.example.invalid:0/")]
        [TestCase("file:///tmp/api")]
        public void EnvironmentRejectsAmbiguousBaseUri(string uri)
        {
            Assert.Throws<InvalidOperationException>(() => ClientEnvironment.Create(
                ClientEnvironmentKind.Production,
                uri,
                "0.1.0",
                1));
        }

        /// <summary>
        /// 验证 catalog 只暴露当前 capability 的十个 operation，并冻结核心 metadata。
        /// </summary>
        [Test]
        public void CatalogFreezesTenOperations()
        {
            Assert.That(ClientHttpOperationCatalog.All, Has.Count.EqualTo(10));
            AssertDescriptor(
                ClientHttpOperationCatalog.GetVersion,
                "getVersion",
                HttpMethod.Get,
                "/v1/version",
                ClientHttpAuthentication.Anonymous,
                ClientHttpBodyPolicy.None,
                0,
                HttpStatusCode.OK,
                true,
                3000,
                16 * 1024);
            AssertDescriptor(
                ClientHttpOperationCatalog.RegisterAccount,
                "registerAccount",
                HttpMethod.Post,
                "/v1/auth/register",
                ClientHttpAuthentication.Anonymous,
                ClientHttpBodyPolicy.Json,
                4096,
                HttpStatusCode.Created,
                true,
                10000,
                64 * 1024);
            AssertDescriptor(
                ClientHttpOperationCatalog.GetBootstrapConfig,
                "getBootstrapConfig",
                HttpMethod.Get,
                "/v1/config",
                ClientHttpAuthentication.Anonymous,
                ClientHttpBodyPolicy.None,
                0,
                HttpStatusCode.OK,
                true,
                3000,
                64 * 1024);
            AssertDescriptor(
                ClientHttpOperationCatalog.LoginAccount,
                "loginAccount",
                HttpMethod.Post,
                "/v1/auth/login",
                ClientHttpAuthentication.Anonymous,
                ClientHttpBodyPolicy.Json,
                4096,
                HttpStatusCode.OK,
                true,
                10000,
                64 * 1024);
            AssertDescriptor(
                ClientHttpOperationCatalog.RefreshSession,
                "refreshSession",
                HttpMethod.Post,
                "/v1/auth/refresh",
                ClientHttpAuthentication.Anonymous,
                ClientHttpBodyPolicy.Json,
                4096,
                HttpStatusCode.OK,
                true,
                5000,
                16 * 1024);
            AssertDescriptor(
                ClientHttpOperationCatalog.LogoutSession,
                "logoutSession",
                HttpMethod.Post,
                "/v1/auth/logout",
                ClientHttpAuthentication.Bearer,
                ClientHttpBodyPolicy.None,
                0,
                HttpStatusCode.NoContent,
                false,
                5000,
                16 * 1024);
            AssertDescriptor(
                ClientHttpOperationCatalog.IssueConnectionTicket,
                "issueConnectionTicket",
                HttpMethod.Post,
                "/v1/session/tickets",
                ClientHttpAuthentication.Bearer,
                ClientHttpBodyPolicy.Json,
                2048,
                HttpStatusCode.Created,
                true,
                5000,
                16 * 1024);
            AssertDescriptor(
                ClientHttpOperationCatalog.GetWorldBootstrap,
                "getWorldBootstrap",
                HttpMethod.Get,
                "/v1/world/bootstrap",
                ClientHttpAuthentication.Bearer,
                ClientHttpBodyPolicy.None,
                0,
                HttpStatusCode.OK,
                true,
                5000,
                64 * 1024);
            AssertDescriptor(
                ClientHttpOperationCatalog.AcceptVisitInvite,
                "acceptVisitInvite",
                HttpMethod.Post,
                "/v1/visits/{visitSessionId}/invites/{inviteId}/accept",
                ClientHttpAuthentication.Bearer,
                ClientHttpBodyPolicy.Json,
                4096,
                HttpStatusCode.OK,
                true,
                5000,
                16 * 1024);
            AssertDescriptor(
                ClientHttpOperationCatalog.IssueWorldAdmission,
                "issueWorldAdmission",
                HttpMethod.Post,
                "/v1/world/admissions",
                ClientHttpAuthentication.Bearer,
                ClientHttpBodyPolicy.Json,
                4096,
                HttpStatusCode.Created,
                true,
                5000,
                16 * 1024);

            Assert.That(
                ClientHttpOperationCatalog.All.Select(item => item.OperationID).Distinct().Count(),
                Is.EqualTo(10));
            Assert.That(
                ClientHttpOperationCatalog.All.Count(item => item.RelativePath.IndexOf('{') >= 0),
                Is.EqualTo(1));
        }

        /// <summary>
        /// 验证 fixture login request 只生成冻结字段，且安全类型输出不含 credential。
        /// </summary>
        [Test]
        public void CodecMatchesLoginFixtureAndRedactsCredentials()
        {
            const string password = "fixture-password-not-secret";
            var codec = new ClientHttpCodec();
            var body = codec.EncodeLogin("fixture-user", password);

            using (var document = JsonDocument.Parse(body))
            {
                var root = document.RootElement;
                Assert.That(root.EnumerateObject().Count(), Is.EqualTo(2));
                Assert.That(root.GetProperty("username").GetString(), Is.EqualTo("fixture-user"));
                Assert.That(root.GetProperty("password").GetString(), Is.EqualTo(password));
            }

            var tokens = new ClientTokenPair("access-secret", "refresh-secret", 20, 30);
            Assert.That(tokens.ToString(), Does.Not.Contain("access-secret"));
            Assert.That(tokens.ToString(), Does.Not.Contain("refresh-secret"));
        }

        /// <summary>
        /// 验证 version 与 ticket fixture 解码，并拒绝未知枚举和缺失 required field。
        /// </summary>
        [Test]
        public void CodecValidatesRequiredFieldsAndEnums()
        {
            var codec = new ClientHttpCodec();
            var version = codec.DecodeVersion(Utf8(
                "{\"minimumClientVersion\":\"0.1.0\",\"protocolVersion\":1,\"serverVersion\":\"0.1.0\",\"future\":true}"));
            Assert.That(version.ProtocolVersion, Is.EqualTo(1));
            Assert.That(version.MinimumClientVersion, Is.EqualTo("0.1.0"));

            var ticket = codec.DecodeConnectionTicket(Utf8(
                "{\"endpoint\":{\"channel\":\"TLS_TCP\",\"host\":\"game.example.invalid\",\"port\":4433},\"expiresAtMs\":1700000030000,\"scopes\":[\"GAMEPLAY\"],\"ticket\":\"opaque-ticket-value-with-safe-length\"}"));
            Assert.That(ticket.Endpoint.Channel, Is.EqualTo(ClientEndpointChannel.TlsTcp));
            Assert.That(ticket.Scopes, Is.EqualTo(new[] { ClientConnectionScope.Gameplay }));
            Assert.That(ticket.ToString(), Does.Not.Contain("opaque-ticket-value-with-safe-length"));

            Assert.Throws<ClientHttpContractException>(() => codec.DecodeVersion(Utf8(
                "{\"protocolVersion\":1,\"serverVersion\":\"0.1.0\"}")));
            Assert.Throws<ClientHttpContractException>(() => codec.DecodeVersion(Utf8(
                "{\"minimumClientVersion\":\"0.1.0\",\"protocolVersion\":1,\"protocolVersion\":2,\"serverVersion\":\"0.1.0\"}")));
            Assert.Throws<ClientHttpContractException>(() => codec.DecodeConnectionTicket(Utf8(
                "{\"endpoint\":{\"channel\":\"UDP\",\"host\":\"game.example.invalid\",\"port\":4433},\"expiresAtMs\":1700000030000,\"scopes\":[\"GAMEPLAY\"],\"ticket\":\"opaque-ticket-value-with-safe-length\"}")));
            Assert.Throws<ClientHttpContractException>(() => codec.DecodeWorldBootstrap(Utf8(
                "{\"world\":{\"personalWorldId\":\"pworld-owner\",\"ownerPlayerId\":\"player-owner\",\"lifecycle\":\"ACTIVE\",\"revision\":1,\"createdAtMs\":1},\"assignment\":{\"personalWorldId\":\"pworld-other\",\"worldInstanceId\":\"winst-current\",\"endpoint\":{\"channel\":\"TLS_TCP\",\"host\":\"game.example.invalid\",\"port\":4433},\"generation\":1,\"leaseExpiresAtMs\":2}}")));
        }

        /// <summary>
        /// 验证 admission request 不允许 actor 注入，response 保持 role/purpose 与 credential 脱敏。
        /// </summary>
        [Test]
        public void CodecFreezesWorldAdmissionContract()
        {
            var codec = new ClientHttpCodec();
            using (var own = JsonDocument.Parse(codec.EncodeWorldAdmission(
                       ClientWorldAdmissionTarget.OwnWorld())))
            {
                Assert.That(own.RootElement.EnumerateObject().Count(), Is.EqualTo(1));
                Assert.That(own.RootElement.GetProperty("kind").GetString(), Is.EqualTo("OWN_WORLD"));
            }

            using (var visit = JsonDocument.Parse(codec.EncodeWorldAdmission(
                       ClientWorldAdmissionTarget.VisitWorld("visit_fixture_one"))))
            {
                Assert.That(visit.RootElement.EnumerateObject().Count(), Is.EqualTo(2));
                Assert.That(visit.RootElement.GetProperty("visitSessionId").GetString(), Is.EqualTo("visit_fixture_one"));
            }

            var admission = codec.DecodeWorldAdmission(Utf8(
                "{\"credential\":\"wad1_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\",\"endpoint\":{\"channel\":\"TLS_TCP\",\"host\":\"game.example.invalid\",\"port\":4433},\"expiresAtMs\":1700000030000,\"purpose\":\"OWN_WORLD\",\"role\":\"OWNER\",\"visitRevision\":0}"));
            Assert.That(admission.Purpose, Is.EqualTo(ClientWorldAdmissionPurpose.OwnWorld));
            Assert.That(admission.ToString(), Does.Not.Contain(admission.Credential));

            Assert.Throws<ArgumentException>(() => codec.EncodeWorldAdmission(
                ClientWorldAdmissionTarget.VisitWorld("visit/invalid")));
            Assert.Throws<ClientHttpContractException>(() => codec.DecodeWorldAdmission(Utf8(
                "{\"credential\":\"wad1_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\",\"endpoint\":{\"channel\":\"TLS_TCP\",\"host\":\"game.example.invalid\",\"port\":4433},\"expiresAtMs\":1700000030000,\"purpose\":\"JOIN\",\"role\":\"OWNER\",\"visitRevision\":7}")));
        }

        /// <summary>
        /// 验证 invite accept 只绑定两个 path identity 与 expected revision，并拒绝错配 reservation。
        /// </summary>
        [Test]
        public void CodecFreezesVisitInviteAcceptContract()
        {
            var codec = new ClientHttpCodec();
            var request = new ClientVisitInviteAcceptRequest("visit:fixture", "invite_fixture", 7);

            Assert.That(
                codec.BuildVisitInviteAcceptPath(request),
                Is.EqualTo("/v1/visits/visit%3Afixture/invites/invite_fixture/accept"));
            using (var body = JsonDocument.Parse(codec.EncodeVisitInviteAccept(request)))
            {
                Assert.That(body.RootElement.EnumerateObject().Count(), Is.EqualTo(1));
                Assert.That(body.RootElement.GetProperty("expectedRevision").GetInt64(), Is.EqualTo(7));
            }

            var reservation = codec.DecodeVisitInviteAccept(
                Utf8("{\"reservation\":{\"visitSessionId\":\"visit:fixture\",\"revision\":8,\"reservationExpiresAtMs\":9000}}"),
                request);
            Assert.That(reservation.VisitSessionID, Is.EqualTo("visit:fixture"));
            Assert.That(reservation.Revision, Is.EqualTo(8));

            Assert.Throws<ArgumentException>(() => codec.EncodeVisitInviteAccept(
                new ClientVisitInviteAcceptRequest("visit/invalid", "invite", 7)));
            Assert.Throws<ArgumentException>(() => codec.BuildVisitInviteAcceptPath(
                new ClientVisitInviteAcceptRequest("visit", "invite", 0)));
            Assert.Throws<ClientHttpContractException>(() => codec.DecodeVisitInviteAccept(
                Utf8("{\"reservation\":{\"visitSessionId\":\"visit-other\",\"revision\":8,\"reservationExpiresAtMs\":9000}}"),
                request));
            Assert.Throws<ClientHttpContractException>(() => codec.DecodeVisitInviteAccept(
                Utf8("{\"reservation\":{\"visitSessionId\":\"visit:fixture\",\"revision\":7,\"reservationExpiresAtMs\":9000}}"),
                request));
        }

        /// <summary>
        /// 验证已知错误必须匹配 registry，未知错误仍以安全 Unknown 投影返回。
        /// </summary>
        [Test]
        public void ErrorCodecFreezesKnownRegistryAndToleratesUnknownCode()
        {
            var codec = new ClientHttpCodec();
            var known = codec.DecodeServerError(
                Utf8("{\"code\":102,\"messageKey\":\"error.auth.invalid_credentials\",\"requestId\":\"fixture-request-id\",\"retryable\":false}"),
                HttpStatusCode.Unauthorized);
            Assert.That(known.Category, Is.EqualTo(ClientServerErrorCategory.Authentication));

            var unknown = codec.DecodeServerError(
                Utf8("{\"code\":999999,\"messageKey\":\"error.future.unknown\",\"requestId\":\"fixture-request-id\",\"retryable\":false}"),
                HttpStatusCode.BadRequest);
            Assert.That(unknown.Category, Is.EqualTo(ClientServerErrorCategory.Unknown));
            Assert.That(unknown.ToString(), Does.Not.Contain("password"));

            Assert.Throws<ClientHttpContractException>(() => codec.DecodeServerError(
                Utf8("{\"code\":102,\"messageKey\":\"error.wrong\",\"requestId\":\"fixture-request-id\",\"retryable\":false}"),
                HttpStatusCode.Unauthorized));
            Assert.Throws<ClientHttpContractException>(() => codec.DecodeServerError(
                Utf8("{\"code\":999999,\"messageKey\":\"error.future.unknown\",\"requestId\":\"request\\nforged\",\"retryable\":false}"),
                HttpStatusCode.BadRequest));
        }

        /// <summary>
        /// 验证 Composition 显式连接唯一 HTTP bootstrap/session graph，且单个 builder 不可复用。
        /// </summary>
        [Test]
        public void CompositionBuildsOneTypedHttpGraph()
        {
            var environment = ClientEnvironment.Create(
                ClientEnvironmentKind.Test,
                "http://127.0.0.1:8080/",
                "0.1.0",
                1);
            var composition = new AppComposition();
            var hostObject = new GameObject("CompositionUiHostRoot");
            hostObject.SetActive(false);
            var inputAsset = ScriptableObject.CreateInstance<InputActionAsset>();
            var player = inputAsset.AddActionMap("Player");
            player.AddAction("Move");
            player.AddAction("Menu", InputActionType.Button);
            var ui = inputAsset.AddActionMap("UI");
            ui.AddAction("Navigate");
            ui.AddAction("Cancel", InputActionType.Button);
            var uiHostRoot = hostObject.AddComponent<ClientUiHostRoot>();
            uiHostRoot.ConfigureBeforeActivation(
                inputAsset,
                Array.Empty<ClientUiToolkitHost>(),
                Array.Empty<ClientUguiHost>());

            try
            {
                var result = composition.Build(environment, uiHostRoot);

                Assert.That(result.BootstrapService, Is.Not.Null);
                Assert.That(result.SessionCoordinator, Is.Not.Null);
                Assert.That(result.UiRouter.CurrentSnapshot.Items, Is.Empty);
                Assert.That(result.Lifetime.State, Is.EqualTo(AppLifetimeState.Created));
                Assert.Throws<InvalidOperationException>(() => composition.Build(environment, uiHostRoot));
            }
            finally
            {
                UnityEngine.Object.DestroyImmediate(hostObject);
                UnityEngine.Object.DestroyImmediate(inputAsset);
            }
        }

        /// <summary>
        /// 直接消费共享 HTTP fixtures，验证客户端已接入的响应和请求仍与跨端事实一致。
        /// </summary>
        [Test]
        public void SharedHttpFixturesMatchExplicitClientCodecs()
        {
            var fixturePath = Path.Combine(
                FindRepositoryRoot(),
                "shared",
                "contracts",
                "fixtures",
                "http",
                "cases.json");
            using (var document = JsonDocument.Parse(File.ReadAllText(fixturePath)))
            {
                var cases = document.RootElement.GetProperty("cases")
                    .EnumerateArray()
                    .ToDictionary(item => item.GetProperty("name").GetString(), StringComparer.Ordinal);
                var codec = new ClientHttpCodec();

                var version = codec.DecodeVersion(Utf8(cases["version-success"]
                    .GetProperty("response").GetProperty("body").GetRawText()));
                Assert.That(version.ProtocolVersion, Is.EqualTo(1));

                var loginRequest = cases["login-invalid-credentials"]
                    .GetProperty("request").GetProperty("body");
                using (var encodedLogin = JsonDocument.Parse(codec.EncodeLogin(
                           loginRequest.GetProperty("username").GetString(),
                           loginRequest.GetProperty("password").GetString())))
                {
                    Assert.That(
                        encodedLogin.RootElement.GetProperty("username").GetString(),
                        Is.EqualTo(loginRequest.GetProperty("username").GetString()));
                }

                var registerRequest = cases["register-username-conflict"]
                    .GetProperty("request").GetProperty("body");
                using (var encodedRegister = JsonDocument.Parse(codec.EncodeRegister(
                           registerRequest.GetProperty("username").GetString(),
                           registerRequest.GetProperty("password").GetString(),
                           registerRequest.GetProperty("displayName").GetString())))
                {
                    Assert.That(encodedRegister.RootElement.EnumerateObject().Count(), Is.EqualTo(3));
                }

                var ticket = codec.DecodeConnectionTicket(Utf8(cases["ticket-channel-boundary"]
                    .GetProperty("response").GetProperty("body").GetRawText()));
                Assert.That(ticket.Endpoint.Channel, Is.EqualTo(ClientEndpointChannel.TlsTcp));

                var world = codec.DecodeWorldBootstrap(Utf8(cases["world-bootstrap-success"]
                    .GetProperty("response").GetProperty("body").GetRawText()));
                Assert.That(world.World.PersonalWorldID, Is.EqualTo("pworld_fixture_owner"));
                Assert.That(world.Assignment.WorldInstanceID, Is.EqualTo("winst_fixture_current"));

                var acceptCase = cases["visit-accept-success"];
                var acceptFixture = acceptCase.GetProperty("request");
                var acceptRequest = new ClientVisitInviteAcceptRequest(
                    "visit_fixture_one",
                    "invite_fixture_one",
                    acceptFixture.GetProperty("body").GetProperty("expectedRevision").GetInt64());
                using (var encodedAccept = JsonDocument.Parse(codec.EncodeVisitInviteAccept(acceptRequest)))
                {
                    Assert.That(
                        encodedAccept.RootElement.GetProperty("expectedRevision").GetInt64(),
                        Is.EqualTo(acceptRequest.ExpectedRevision));
                }

                var reservation = codec.DecodeVisitInviteAccept(
                    Utf8(acceptCase.GetProperty("response").GetProperty("body").GetRawText()),
                    acceptRequest);
                Assert.That(reservation.VisitSessionID, Is.EqualTo(acceptRequest.VisitSessionID));

                var admissionCase = cases["own-world-admission-success"];
                using (var encodedAdmission = JsonDocument.Parse(codec.EncodeWorldAdmission(
                           ClientWorldAdmissionTarget.OwnWorld())))
                {
                    var fixtureAdmission = admissionCase.GetProperty("request").GetProperty("body");
                    Assert.That(encodedAdmission.RootElement.EnumerateObject().Count(), Is.EqualTo(1));
                    Assert.That(
                        encodedAdmission.RootElement.GetProperty("kind").GetString(),
                        Is.EqualTo(fixtureAdmission.GetProperty("kind").GetString()));
                }

                var admission = codec.DecodeWorldAdmission(Utf8(admissionCase
                    .GetProperty("response").GetProperty("body").GetRawText()));
                Assert.That(admission.Role, Is.EqualTo(ClientWorldRole.Owner));
            }
        }

        /// <summary>
        /// 直接消费共享 error registry，冻结全部已知 code 的客户端恢复投影。
        /// </summary>
        [Test]
        public void SharedErrorRegistryMatchesClientProjection()
        {
            var registryPath = Path.Combine(
                FindRepositoryRoot(),
                "shared",
                "contracts",
                "registry",
                "errors.json");
            using (var document = JsonDocument.Parse(File.ReadAllText(registryPath)))
            {
                var errors = document.RootElement.GetProperty("errors").EnumerateArray().ToArray();
                Assert.That(errors, Has.Length.EqualTo(28));
                foreach (var item in errors)
                {
                    var code = item.GetProperty("code").GetInt32();
                    Assert.That(ClientErrorRegistry.TryGet(code, out var known), Is.True, $"缺少错误码 {code}。");
                    Assert.That(known.MessageKey, Is.EqualTo(item.GetProperty("messageKey").GetString()));
                    Assert.That(known.Retryable, Is.EqualTo(item.GetProperty("retryable").GetBoolean()));
                    Assert.That((int)known.StatusCode, Is.EqualTo(item.GetProperty("httpStatus").GetInt32()));
                    Assert.That(
                        known.Category,
                        Is.EqualTo(ParseErrorCategory(item.GetProperty("category").GetString())));
                }
            }
        }

        /// <summary>
        /// 断言单个 descriptor 的冻结 metadata。
        /// </summary>
        /// <param name="actual">Catalog 中的 descriptor。</param>
        /// <param name="operationID">预期 operationId。</param>
        /// <param name="method">预期 HTTP method。</param>
        /// <param name="path">预期相对 path。</param>
        /// <param name="authentication">预期认证策略。</param>
        /// <param name="bodyPolicy">预期请求 body 策略。</param>
        /// <param name="requestLimitBytes">预期请求上限，单位为字节。</param>
        /// <param name="status">预期成功 status。</param>
        /// <param name="successHasBody">预期成功响应是否必须包含 JSON body。</param>
        /// <param name="timeoutMilliseconds">预期 deadline，单位为毫秒。</param>
        /// <param name="responseLimitBytes">预期响应硬上限，单位为字节。</param>
        private static void AssertDescriptor(
            ClientHttpOperation actual,
            string operationID,
            HttpMethod method,
            string path,
            ClientHttpAuthentication authentication,
            ClientHttpBodyPolicy bodyPolicy,
            int requestLimitBytes,
            HttpStatusCode status,
            bool successHasBody,
            int timeoutMilliseconds,
            int responseLimitBytes)
        {
            Assert.That(actual.OperationID, Is.EqualTo(operationID));
            Assert.That(actual.Method, Is.EqualTo(method));
            Assert.That(actual.RelativePath, Is.EqualTo(path));
            Assert.That(actual.Authentication, Is.EqualTo(authentication));
            Assert.That(actual.RequestBodyPolicy, Is.EqualTo(bodyPolicy));
            Assert.That(actual.RequestBodyLimitBytes, Is.EqualTo(requestLimitBytes));
            Assert.That(actual.SuccessStatus, Is.EqualTo(status));
            Assert.That(actual.SuccessHasBody, Is.EqualTo(successHasBody));
            Assert.That(actual.Timeout, Is.EqualTo(TimeSpan.FromMilliseconds(timeoutMilliseconds)));
            Assert.That(actual.ResponseBodyLimitBytes, Is.EqualTo(responseLimitBytes));
        }

        /// <summary>
        /// 把测试 JSON 转换为 codec 接收的 UTF-8 内存。
        /// </summary>
        /// <param name="json">受测试控制的 JSON 文本。</param>
        /// <returns>UTF-8 字节内存。</returns>
        private static ReadOnlyMemory<byte> Utf8(string json)
        {
            return Encoding.UTF8.GetBytes(json);
        }

        /// <summary>
        /// 从测试工作目录向上定位包含 shared/contracts 的仓库根目录。
        /// </summary>
        /// <returns>规范仓库根目录绝对路径。</returns>
        /// <exception cref="DirectoryNotFoundException">无法定位共享契约时抛出。</exception>
        private static string FindRepositoryRoot()
        {
            var directory = new DirectoryInfo(Environment.CurrentDirectory);
            while (directory != null)
            {
                if (File.Exists(Path.Combine(
                        directory.FullName,
                        "shared",
                        "contracts",
                        "fixtures",
                        "http",
                        "cases.json")))
                {
                    return directory.FullName;
                }

                directory = directory.Parent;
            }

            throw new DirectoryNotFoundException("无法定位 shared/contracts HTTP fixtures。");
        }

        /// <summary>
        /// 把共享 registry category 转换为客户端封闭恢复类别。
        /// </summary>
        /// <param name="category">Registry 中的稳定大写 category。</param>
        /// <returns>对应客户端 enum。</returns>
        /// <exception cref="InvalidDataException">Registry 出现未知 category 时抛出。</exception>
        private static ClientServerErrorCategory ParseErrorCategory(string category)
        {
            return category switch
            {
                "PROTOCOL" => ClientServerErrorCategory.Protocol,
                "AUTH" => ClientServerErrorCategory.Authentication,
                "VALIDATION" => ClientServerErrorCategory.Validation,
                "CONFLICT" => ClientServerErrorCategory.Conflict,
                "NOT_FOUND" => ClientServerErrorCategory.NotFound,
                "RATE_LIMIT" => ClientServerErrorCategory.RateLimit,
                "DEPENDENCY" => ClientServerErrorCategory.Dependency,
                "INTERNAL" => ClientServerErrorCategory.Internal,
                _ => throw new InvalidDataException("共享 error registry 包含未知 category。"),
            };
        }
    }
}
