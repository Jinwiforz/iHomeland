using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.AppShell.Application.Bootstrap;
using IHomeland.Client.Session.Application;
using IHomeland.Client.AppShell.Application.Configuration;
using IHomeland.Client.Core.Foundation.Time;
using IHomeland.Client.Networking.Application.Contracts;
using IHomeland.Client.Networking.Application.Ports;
using IHomeland.Client.Networking.Infrastructure.Http;
using NUnit.Framework;

namespace IHomeland.Client.Session.Tests.EditMode
{
    /// <summary>验证启动restore固定顺序、一次性、终态映射与安全提交。</summary>
    public sealed class ClientSessionRestoreCoordinatorTests
    {
        /// <summary>确认合法record只refresh一次，先替换record再发布current snapshot。</summary>
        [Test]
        public async Task ValidRecord_RestoresOnceInFixedOrder()
        {
            var fixture = await CreateFixtureAsync(seedRecord: true);

            await fixture.Restore.InitializeAsync(CancellationToken.None);

            Assert.That(
                fixture.Api.Calls,
                Is.EqualTo(new[] { "version", "config", "refresh" }));
            Assert.That(fixture.Restore.Result.Outcome, Is.EqualTo(ClientSessionRestoreOutcome.Restored));
            Assert.That(fixture.Session.TryGetCurrent(out var current), Is.True);
            Assert.That(current.Tokens.RefreshToken, Is.EqualTo("refresh-rotated"));
            Assert.That(fixture.Store.GetRecord().RefreshToken, Is.EqualTo("refresh-rotated"));
            Assert.That(fixture.Store.ReadCount, Is.EqualTo(1));
            Assert.That(fixture.Store.ReplaceCount, Is.EqualTo(2));
            Assert.ThrowsAsync<InvalidOperationException>(async () =>
                await fixture.Restore.InitializeAsync(CancellationToken.None));
        }

        /// <summary>确认没有record时不执行bootstrap、refresh或账号猜测。</summary>
        [Test]
        public async Task MissingRecord_IsNotAvailableWithoutNetwork()
        {
            var fixture = await CreateFixtureAsync(seedRecord: false);

            await fixture.Restore.InitializeAsync(CancellationToken.None);

            Assert.That(
                fixture.Restore.Result.Outcome,
                Is.EqualTo(ClientSessionRestoreOutcome.NotAvailable));
            Assert.That(fixture.Api.Calls, Is.Empty);
            Assert.That(fixture.Session.TryGetCurrent(out _), Is.False);
        }

        /// <summary>确认损坏、解保护或profile ownership失败不会发送refresh。</summary>
        [TestCase((int)ClientSecureSessionStoreOutcome.InvalidRecord)]
        [TestCase((int)ClientSecureSessionStoreOutcome.ProtectionFailure)]
        public async Task ReadFailure_IsStorageFailureWithoutNetwork(
            int readOutcomeValue)
        {
            var fixture = await CreateFixtureAsync(seedRecord: false);
            fixture.Store.ReadFailureOutcome =
                (ClientSecureSessionStoreOutcome)readOutcomeValue;

            await fixture.Restore.InitializeAsync(CancellationToken.None);

            Assert.That(
                fixture.Restore.Result.Outcome,
                Is.EqualTo(ClientSessionRestoreOutcome.StorageFailure));
            Assert.That(fixture.Api.Calls, Is.Empty);
        }

        /// <summary>确认第二个production writer保留稳定profile ownership终态且不访问网络。</summary>
        [Test]
        public async Task ProfileInUse_PreservesOwnershipFailureWithoutNetwork()
        {
            var fixture = await CreateFixtureAsync(seedRecord: false);
            fixture.Store.ReadFailureOutcome = ClientSecureSessionStoreOutcome.ProfileInUse;

            await fixture.Restore.InitializeAsync(CancellationToken.None);

            Assert.That(
                fixture.Restore.Result.Outcome,
                Is.EqualTo(ClientSessionRestoreOutcome.ProfileInUse));
            Assert.That(fixture.Api.Calls, Is.Empty);
        }

        /// <summary>确认服务端拒绝旧refresh时删除record并保持Unauthenticated。</summary>
        [Test]
        public async Task RejectedRefresh_RetiresRecord()
        {
            var fixture = await CreateFixtureAsync(seedRecord: true);
            fixture.Api.RefreshHandler = (_, __) => Task.FromResult(
                ClientGatewayResult<ClientTokenPair>.Rejected(new ClientServerError(
                    100,
                    ClientServerErrorCategory.Authentication,
                    "session.unauthenticated",
                    "request-fixture",
                    retryable: false,
                    retryAfter: null,
                    Array.Empty<ClientErrorDetail>())));

            await fixture.Restore.InitializeAsync(CancellationToken.None);

            Assert.That(
                fixture.Restore.Result.Outcome,
                Is.EqualTo(ClientSessionRestoreOutcome.Rejected));
            Assert.That(fixture.Session.State, Is.EqualTo(ClientSessionOwnerState.Unauthenticated));
            Assert.That(fixture.Store.GetRecord(), Is.Null);
            Assert.That(fixture.Store.DeleteCount, Is.EqualTo(1));
        }

        /// <summary>确认非认证类服务端拒绝不会销毁尚未消费的refresh lineage。</summary>
        [Test]
        public async Task TransientServerRejection_PreservesUnusedRecord()
        {
            var fixture = await CreateFixtureAsync(seedRecord: true);
            fixture.Api.RefreshHandler = (_, __) => Task.FromResult(
                ClientGatewayResult<ClientTokenPair>.Rejected(new ClientServerError(
                    500,
                    ClientServerErrorCategory.Dependency,
                    "dependency.unavailable",
                    "request-fixture",
                    retryable: true,
                    retryAfter: TimeSpan.FromSeconds(1),
                    Array.Empty<ClientErrorDetail>())));

            await fixture.Restore.InitializeAsync(CancellationToken.None);

            Assert.That(
                fixture.Restore.Result.Outcome,
                Is.EqualTo(ClientSessionRestoreOutcome.Unresolved));
            Assert.That(fixture.Session.State, Is.EqualTo(ClientSessionOwnerState.Unauthenticated));
            Assert.That(fixture.Store.GetRecord(), Is.Not.Null);
            Assert.That(fixture.Store.DeleteCount, Is.EqualTo(0));
        }

        /// <summary>确认发送前本地策略失败保留record且不会自动重复refresh。</summary>
        [Test]
        public async Task RefreshLocalPolicyFailure_PreservesUnusedRecord()
        {
            var fixture = await CreateFixtureAsync(seedRecord: true);
            fixture.Api.RefreshHandler = (_, __) => Task.FromResult(
                ClientGatewayResult<ClientTokenPair>.Failed(new ClientGatewayFailure(
                    ClientGatewayFailureKind.LocalPolicy,
                    ClientHttpOperationCatalog.RefreshSession.OperationID)));

            await fixture.Restore.InitializeAsync(CancellationToken.None);

            Assert.That(
                fixture.Restore.Result.Outcome,
                Is.EqualTo(ClientSessionRestoreOutcome.Unresolved));
            Assert.That(fixture.Api.Calls, Is.EqualTo(new[] { "version", "config", "refresh" }));
            Assert.That(fixture.Store.GetRecord(), Is.Not.Null);
            Assert.That(fixture.Store.DeleteCount, Is.EqualTo(0));
        }

        /// <summary>确认refresh timeout/transport等commit-unknown删除旧lineage并进入Unresolved。</summary>
        [Test]
        public async Task RefreshCommitUnknown_IsUnresolvedAndRetired()
        {
            var fixture = await CreateFixtureAsync(seedRecord: true);
            fixture.Api.RefreshHandler = (_, __) => Task.FromResult(
                ClientGatewayResult<ClientTokenPair>.Failed(new ClientGatewayFailure(
                    ClientGatewayFailureKind.Transport,
                    ClientHttpOperationCatalog.RefreshSession.OperationID)));

            await fixture.Restore.InitializeAsync(CancellationToken.None);

            Assert.That(
                fixture.Restore.Result.Outcome,
                Is.EqualTo(ClientSessionRestoreOutcome.Unresolved));
            Assert.That(fixture.Session.State, Is.EqualTo(ClientSessionOwnerState.Unresolved));
            Assert.That(fixture.Store.GetRecord(), Is.Null);
        }

        /// <summary>确认bootstrap失败保留尚未使用的record并返回可重试Unresolved。</summary>
        [Test]
        public async Task BootstrapFailure_PreservesUnusedRecord()
        {
            var fixture = await CreateFixtureAsync(seedRecord: true);
            fixture.Api.VersionHandler = _ => Task.FromResult(
                ClientGatewayResult<ClientVersionInfo>.Failed(new ClientGatewayFailure(
                    ClientGatewayFailureKind.Transport,
                    ClientHttpOperationCatalog.GetVersion.OperationID)));

            await fixture.Restore.InitializeAsync(CancellationToken.None);

            Assert.That(
                fixture.Restore.Result.Outcome,
                Is.EqualTo(ClientSessionRestoreOutcome.Unresolved));
            Assert.That(fixture.Api.Calls, Is.EqualTo(new[] { "version" }));
            Assert.That(fixture.Store.GetRecord(), Is.Not.Null);
            Assert.That(fixture.Store.DeleteCount, Is.EqualTo(0));
        }

        /// <summary>创建已初始化store、configuration与Session的restore fixture。</summary>
        /// <param name="seedRecord">是否在启动前保存current record。</param>
        /// <returns>尚未运行restore的fixture。</returns>
        private static async Task<RestoreFixture> CreateFixtureAsync(bool seedRecord)
        {
            var store = new FakeClientSecureSessionStore();
            await store.InitializeAsync(CancellationToken.None);
            if (seedRecord)
            {
                await store.ReplaceAsync(CreateRecord(), CancellationToken.None);
            }

            var api = new FakeRestoreHttpApi();
            var configuration = new ClientConfigurationStore();
            await configuration.InitializeAsync(CancellationToken.None);
            var bootstrap = new ClientBootstrapService(
                ClientEnvironment.Create(
                    ClientEnvironmentKind.Test,
                    "http://127.0.0.1:8080/",
                    "0.1.0",
                    1),
                api,
                configuration);
            var session = new SessionCoordinator(
                configuration,
                api,
                new FakeClock(1000),
                store,
                FakeClientSecureSessionStore.EnvironmentBinding);
            await session.InitializeAsync(CancellationToken.None);
            var restore = new ClientSessionRestoreCoordinator(
                store,
                bootstrap,
                session,
                TimeSpan.FromSeconds(1));
            return new RestoreFixture(store, api, session, restore);
        }

        /// <summary>创建尚未轮换的有效启动record。</summary>
        /// <returns>Expiry晚于测试时钟的schema v1 record。</returns>
        private static ClientSecureSessionRecord CreateRecord()
        {
            return new ClientSecureSessionRecord(
                ClientSecureSessionRecord.CurrentSchemaVersion,
                FakeClientSecureSessionStore.EnvironmentBinding,
                new ClientAccountSummary("account", "Fixture", 1),
                new ClientSessionSummary("session", 1, 5000),
                "refresh-original",
                4000);
        }

        /// <summary>聚合单个测试拥有的restore对象图。</summary>
        private sealed class RestoreFixture
        {
            /// <summary>创建fixture。</summary>
            /// <param name="store">可观察secure store。</param>
            /// <param name="api">可观察HTTP fake。</param>
            /// <param name="session">唯一Session owner。</param>
            /// <param name="restore">唯一restore owner。</param>
            internal RestoreFixture(
                FakeClientSecureSessionStore store,
                FakeRestoreHttpApi api,
                SessionCoordinator session,
                ClientSessionRestoreCoordinator restore)
            {
                Store = store;
                Api = api;
                Session = session;
                Restore = restore;
            }

            /// <summary>获取secure store。</summary>
            internal FakeClientSecureSessionStore Store { get; }

            /// <summary>获取HTTP fake。</summary>
            internal FakeRestoreHttpApi Api { get; }

            /// <summary>获取Session owner。</summary>
            internal SessionCoordinator Session { get; }

            /// <summary>获取restore owner。</summary>
            internal ClientSessionRestoreCoordinator Restore { get; }
        }

        /// <summary>提供可变Unix millisecond测试时钟。</summary>
        private sealed class FakeClock : IClientClock
        {
            /// <summary>创建固定时钟。</summary>
            /// <param name="nowMilliseconds">当前Unix毫秒。</param>
            internal FakeClock(long nowMilliseconds)
            {
                UtcNowMilliseconds = nowMilliseconds;
            }

            /// <inheritdoc />
            public long UtcNowMilliseconds { get; }
        }

        /// <summary>只为启动restore登记version/config/refresh的HTTP fake。</summary>
        private sealed class FakeRestoreHttpApi : IClientBootstrapGateway, IClientSessionGateway
        {
            /// <summary>按发生顺序保存operation名称。</summary>
            internal List<string> Calls { get; } = new List<string>();

            /// <summary>获取或设置version handler。</summary>
            internal Func<CancellationToken, Task<ClientGatewayResult<ClientVersionInfo>>> VersionHandler { get; set; }

            /// <summary>获取或设置refresh handler。</summary>
            internal Func<string, CancellationToken, Task<ClientGatewayResult<ClientTokenPair>>> RefreshHandler { get; set; }

            /// <summary>创建默认成功fake。</summary>
            internal FakeRestoreHttpApi()
            {
                VersionHandler = _ => Task.FromResult(ClientGatewayResult<ClientVersionInfo>.Success(
                    new ClientVersionInfo(1, "0.1.0", "0.1.0")));
                RefreshHandler = (_, __) => Task.FromResult(ClientGatewayResult<ClientTokenPair>.Success(
                    new ClientTokenPair("access-rotated", "refresh-rotated", 3000, 4000)));
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientVersionInfo>> GetVersionAsync(
                CancellationToken cancellationToken)
            {
                Calls.Add("version");
                return VersionHandler(cancellationToken);
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientBootstrapConfiguration>> GetBootstrapConfigurationAsync(
                CancellationToken cancellationToken)
            {
                Calls.Add("config");
                return Task.FromResult(ClientGatewayResult<ClientBootstrapConfiguration>.Success(
                    new ClientBootstrapConfiguration(
                        new[]
                        {
                            new ClientEndpoint(ClientEndpointChannel.Wss, "control.example.invalid", 443),
                            new ClientEndpoint(ClientEndpointChannel.TlsTcp, "game.example.invalid", 4433),
                        },
                        new ClientPublicLimits(4096, 65536))));
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientTokenPair>> RefreshAsync(ClientCredentialGatewayRequest request, CancellationToken cancellationToken)
            {
                Calls.Add("refresh");
                Assert.That(
                    request.Credential.TryTake(
                        ClientCredentialPurpose.RefreshSession,
                        out var refreshToken),
                    Is.True);
                return RefreshHandler(refreshToken, cancellationToken);
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientAuthentication>> RegisterAsync(ClientRegisterGatewayRequest request, CancellationToken cancellationToken)
            {
                return LocalFailure<ClientAuthentication>(
                    ClientHttpOperationCatalog.RegisterAccount.OperationID);
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientAuthentication>> LoginAsync(ClientLoginGatewayRequest request, CancellationToken cancellationToken)
            {
                return LocalFailure<ClientAuthentication>(
                    ClientHttpOperationCatalog.LoginAccount.OperationID);
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientGatewayEmpty>> LogoutAsync(ClientCredentialGatewayRequest request, CancellationToken cancellationToken)
            {
                return LocalFailure<ClientGatewayEmpty>(
                    ClientHttpOperationCatalog.LogoutSession.OperationID);
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientConnectionTicket>> IssueConnectionTicketAsync(ClientConnectionTicketGatewayRequest request, CancellationToken cancellationToken)
            {
                return LocalFailure<ClientConnectionTicket>(
                    ClientHttpOperationCatalog.IssueConnectionTicket.OperationID);
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientWorldBootstrap>> GetWorldBootstrapAsync(ClientCredentialGatewayRequest request, CancellationToken cancellationToken)
            {
                return LocalFailure<ClientWorldBootstrap>(
                    ClientHttpOperationCatalog.GetWorldBootstrap.OperationID);
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientVisitReservation>> AcceptVisitInviteAsync(ClientAcceptVisitInviteGatewayRequest request, CancellationToken cancellationToken)
            {
                return LocalFailure<ClientVisitReservation>(
                    ClientHttpOperationCatalog.AcceptVisitInvite.OperationID);
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientWorldAdmission>> IssueWorldAdmissionAsync(ClientWorldAdmissionGatewayRequest request, CancellationToken cancellationToken)
            {
                return LocalFailure<ClientWorldAdmission>(
                    ClientHttpOperationCatalog.IssueWorldAdmission.OperationID);
            }

            /// <summary>创建未登记operation的稳定本地失败。</summary>
            /// <typeparam name="T">成功投影类型。</typeparam>
            /// <param name="operationID">冻结operationId。</param>
            /// <returns>已完成失败task。</returns>
            private static Task<ClientGatewayResult<T>> LocalFailure<T>(string operationID)
            {
                return Task.FromResult(ClientGatewayResult<T>.Failed(new ClientGatewayFailure(
                    ClientGatewayFailureKind.LocalPolicy,
                    operationID)));
            }
        }
    }
}
