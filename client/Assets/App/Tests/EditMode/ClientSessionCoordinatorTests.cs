using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Bootstrap;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Core.Configuration;
using IHomeland.Client.Infrastructure.Http;
using NUnit.Framework;

namespace IHomeland.Client.Tests.EditMode
{
    /// <summary>
    /// 验证 Configuration 原子发布、Session generation、single-flight、logout 与 ticket 所有权。
    /// </summary>
    public sealed class ClientSessionCoordinatorTests
    {
        /// <summary>
        /// 验证 bootstrap 严格按 version/config 顺序运行，且两步成功前不发布半成品。
        /// </summary>
        /// <returns>等待显式 bootstrap 与断言完成的任务。</returns>
        [Test]
        public async Task BootstrapPublishesOnlyCompleteCompatibleSnapshot()
        {
            var api = new FakeHttpApi();
            var configCompletion = new TaskCompletionSource<ClientHttpResult<ClientBootstrapConfiguration>>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            api.VersionHandler = _ => Task.FromResult(ClientHttpResult<ClientVersionInfo>.Success(
                new ClientVersionInfo(1, "0.1.0", "0.1.0")));
            api.ConfigurationHandler = _ => configCompletion.Task;
            var store = new ClientConfigurationStore();
            await store.InitializeAsync(CancellationToken.None);
            var service = new ClientBootstrapService(CreateEnvironment(), api, store);

            var bootstrap = service.BootstrapAsync(CancellationToken.None);
            Assert.That(api.Calls, Is.EqualTo(new[] { "version", "config" }));
            Assert.That(store.State, Is.EqualTo(ClientConfigurationState.Empty));
            configCompletion.SetResult(ClientHttpResult<ClientBootstrapConfiguration>.Success(
                CreateConfiguration()));

            var result = await bootstrap;
            Assert.That(result.IsSuccess, Is.True);
            Assert.That(store.TryGetCurrent(out var snapshot), Is.True);
            Assert.That(snapshot.Version.ProtocolVersion, Is.EqualTo(1));
        }

        /// <summary>
        /// 验证并发 bootstrap 的后续调用方可取消自己的 gate 等待且不会打断首个调用。
        /// </summary>
        /// <returns>等待取消结果与首个 bootstrap 完成的任务。</returns>
        [Test]
        public async Task BootstrapWaiterCancellationDoesNotInterruptActiveBootstrap()
        {
            var configCompletion = new TaskCompletionSource<ClientHttpResult<ClientBootstrapConfiguration>>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            var api = new FakeHttpApi
            {
                VersionHandler = _ => Task.FromResult(ClientHttpResult<ClientVersionInfo>.Success(
                    new ClientVersionInfo(1, "0.1.0", "0.1.0"))),
                ConfigurationHandler = _ => configCompletion.Task,
            };
            var store = new ClientConfigurationStore();
            await store.InitializeAsync(CancellationToken.None);
            var service = new ClientBootstrapService(CreateEnvironment(), api, store);

            var active = service.BootstrapAsync(CancellationToken.None);
            using (var waiterCancellation = new CancellationTokenSource())
            {
                var waiter = service.BootstrapAsync(waiterCancellation.Token);
                waiterCancellation.Cancel();

                var cancelled = await waiter;
                Assert.That(cancelled.Failure.Kind, Is.EqualTo(ClientHttpFailureKind.CallerCancelled));
                Assert.That(active.IsCompleted, Is.False);
            }

            configCompletion.SetResult(ClientHttpResult<ClientBootstrapConfiguration>.Success(
                CreateConfiguration()));
            Assert.That((await active).IsSuccess, Is.True);
            Assert.That(api.Calls, Is.EqualTo(new[] { "version", "config" }));
        }

        /// <summary>
        /// 验证协议或最低客户端版本不兼容时撤销旧配置资格。
        /// </summary>
        /// <returns>等待兼容性检查完成的任务。</returns>
        [Test]
        public async Task BootstrapRejectsIncompatibleVersionBeforeConfig()
        {
            var api = new FakeHttpApi
            {
                VersionHandler = _ => Task.FromResult(ClientHttpResult<ClientVersionInfo>.Success(
                    new ClientVersionInfo(2, "9.0.0", "9.0.0"))),
            };
            var store = new ClientConfigurationStore();
            await store.InitializeAsync(CancellationToken.None);
            var service = new ClientBootstrapService(CreateEnvironment(), api, store);

            var result = await service.BootstrapAsync(CancellationToken.None);

            Assert.That(result.Failure.Kind, Is.EqualTo(ClientHttpFailureKind.LocalPolicy));
            Assert.That(store.State, Is.EqualTo(ClientConfigurationState.Incompatible));
            Assert.That(api.Calls, Is.EqualTo(new[] { "version" }));
        }

        /// <summary>
        /// 验证后发 login intent 优先，迟到旧响应不能覆盖 current session。
        /// </summary>
        /// <returns>等待两个乱序 login completion 完成的任务。</returns>
        [Test]
        public async Task LatestLoginIntentWinsOutOfOrderCompletion()
        {
            var firstCompletion = NewAuthenticationCompletion();
            var secondCompletion = NewAuthenticationCompletion();
            var api = new FakeHttpApi
            {
                LoginHandler = (username, _, __) => username == "first-user"
                    ? firstCompletion.Task
                    : secondCompletion.Task,
            };
            var coordinator = await CreateCoordinatorAsync(api, new FakeClock(1000));

            var first = coordinator.LoginAsync("first-user", "password", CancellationToken.None);
            var second = coordinator.LoginAsync("second-user", "password", CancellationToken.None);
            secondCompletion.SetResult(ClientHttpResult<ClientAuthentication>.Success(
                CreateAuthentication("account-second", "session-second", "access-second", "refresh-second")));
            var secondResult = await second;
            firstCompletion.SetResult(ClientHttpResult<ClientAuthentication>.Success(
                CreateAuthentication("account-first", "session-first", "access-first", "refresh-first")));
            var firstResult = await first;

            Assert.That(secondResult.IsSuccess, Is.True);
            Assert.That(firstResult.Failure.Kind, Is.EqualTo(ClientHttpFailureKind.LocalPolicy));
            Assert.That(coordinator.TryGetCurrent(out var current), Is.True);
            Assert.That(current.Account.AccountID, Is.EqualTo("account-second"));
            Assert.That(current.ToString(), Does.Not.Contain("access-second"));
        }

        /// <summary>
        /// 验证并发 refresh 共享单一请求，且 Forget 后迟到结果不能恢复 credential。
        /// </summary>
        /// <returns>等待共享 refresh 与 generation guard 完成的任务。</returns>
        [Test]
        public async Task RefreshIsSingleFlightAndLateCompletionCannotRestoreSession()
        {
            var refreshCompletion = new TaskCompletionSource<ClientHttpResult<ClientTokenPair>>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            var api = new FakeHttpApi
            {
                LoginHandler = (_, __, ___) => Task.FromResult(ClientHttpResult<ClientAuthentication>.Success(
                    CreateAuthentication("account", "session", "access", "refresh"))),
                RefreshHandler = (_, __) => refreshCompletion.Task,
            };
            var coordinator = await CreateCoordinatorAsync(api, new FakeClock(1000));
            await coordinator.LoginAsync("fixture-user", "password", CancellationToken.None);

            var first = coordinator.RefreshAsync(CancellationToken.None);
            var second = coordinator.RefreshAsync(CancellationToken.None);
            Assert.That(second, Is.SameAs(first));
            Assert.That(api.RefreshCount, Is.EqualTo(1));
            coordinator.Forget();
            refreshCompletion.SetResult(ClientHttpResult<ClientTokenPair>.Success(
                new ClientTokenPair("new-access", "new-refresh", 3000, 4000)));

            var result = await first;
            Assert.That(result.Failure.Kind, Is.EqualTo(ClientHttpFailureKind.LocalPolicy));
            Assert.That(coordinator.State, Is.EqualTo(ClientSessionOwnerState.Unauthenticated));
            Assert.That(coordinator.TryGetCurrent(out _), Is.False);
        }

        /// <summary>
        /// 验证 refresh 后续等待方取消时不撤销首个调用拥有的 single-flight 请求。
        /// </summary>
        /// <returns>等待独立取消与共享 refresh 完成的任务。</returns>
        [Test]
        public async Task RefreshWaiterCancellationDoesNotCancelSharedRequest()
        {
            var refreshCompletion = new TaskCompletionSource<ClientHttpResult<ClientTokenPair>>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            var api = new FakeHttpApi
            {
                LoginHandler = (_, __, ___) => Task.FromResult(ClientHttpResult<ClientAuthentication>.Success(
                    CreateAuthentication("account", "session", "access", "refresh"))),
                RefreshHandler = (_, __) => refreshCompletion.Task,
            };
            var coordinator = await CreateCoordinatorAsync(api, new FakeClock(1000));
            await coordinator.LoginAsync("fixture-user", "password", CancellationToken.None);

            var active = coordinator.RefreshAsync(CancellationToken.None);
            using (var waiterCancellation = new CancellationTokenSource())
            {
                var waiter = coordinator.RefreshAsync(waiterCancellation.Token);
                waiterCancellation.Cancel();

                var cancelled = await waiter;
                Assert.That(cancelled.Failure.Kind, Is.EqualTo(ClientHttpFailureKind.CallerCancelled));
                Assert.That(active.IsCompleted, Is.False);
            }

            refreshCompletion.SetResult(ClientHttpResult<ClientTokenPair>.Success(
                new ClientTokenPair("new-access", "new-refresh", 3000, 4000)));
            Assert.That((await active).IsSuccess, Is.True);
            Assert.That(api.RefreshCount, Is.EqualTo(1));
        }

        /// <summary>
        /// 验证 refresh commit-unknown 会撤销可能已经失效的旧 token lineage。
        /// </summary>
        /// <returns>等待认证、refresh failure 与 fail-closed 迁移完成的任务。</returns>
        [Test]
        public async Task RefreshCommitUnknownInvalidatesOldSnapshot()
        {
            var api = new FakeHttpApi
            {
                LoginHandler = (_, __, ___) => Task.FromResult(ClientHttpResult<ClientAuthentication>.Success(
                    CreateAuthentication("account", "session", "access", "refresh"))),
                RefreshHandler = (_, __) => Task.FromResult(ClientHttpResult<ClientTokenPair>.Failed(
                    new ClientHttpFailure(ClientHttpFailureKind.Transport, "refreshSession"))),
            };
            var coordinator = await CreateCoordinatorAsync(api, new FakeClock(1000));
            await coordinator.LoginAsync("fixture-user", "password", CancellationToken.None);

            var result = await coordinator.RefreshAsync(CancellationToken.None);

            Assert.That(result.Failure.Kind, Is.EqualTo(ClientHttpFailureKind.Transport));
            Assert.That(coordinator.State, Is.EqualTo(ClientSessionOwnerState.Unresolved));
            Assert.That(coordinator.TryGetCurrent(out _), Is.False);
        }

        /// <summary>
        /// 验证 logout commit-unknown 进入 fail-closed unresolved，并可由新 login 或 Forget 显式解决。
        /// </summary>
        /// <returns>等待 login、logout 与状态迁移完成的任务。</returns>
        [Test]
        public async Task LogoutCommitUnknownRequiresExplicitResolution()
        {
            var api = new FakeHttpApi
            {
                LoginHandler = (_, __, ___) => Task.FromResult(ClientHttpResult<ClientAuthentication>.Success(
                    CreateAuthentication("account", "session", "access", "refresh"))),
                LogoutHandler = (_, __) => Task.FromResult(ClientHttpResult<ClientHttpEmpty>.Failed(
                    new ClientHttpFailure(ClientHttpFailureKind.Timeout, "logoutSession"))),
            };
            var coordinator = await CreateCoordinatorAsync(api, new FakeClock(1000));
            await coordinator.LoginAsync("fixture-user", "password", CancellationToken.None);

            var result = await coordinator.LogoutAsync(CancellationToken.None);

            Assert.That(result.Failure.Kind, Is.EqualTo(ClientHttpFailureKind.Timeout));
            Assert.That(coordinator.State, Is.EqualTo(ClientSessionOwnerState.Unresolved));
            var ticket = await coordinator.IssueConnectionTicketAsync(
                ClientEndpointChannel.TlsTcp,
                CancellationToken.None);
            Assert.That(ticket.Failure.Kind, Is.EqualTo(ClientHttpFailureKind.LocalPolicy));

            var relogin = await coordinator.LoginAsync(
                "fixture-user",
                "password",
                CancellationToken.None);
            Assert.That(relogin.IsSuccess, Is.True);
            Assert.That(coordinator.State, Is.EqualTo(ClientSessionOwnerState.Authenticated));

            await coordinator.LogoutAsync(CancellationToken.None);
            Assert.That(coordinator.State, Is.EqualTo(ClientSessionOwnerState.Unresolved));
            coordinator.Forget();
            Assert.That(coordinator.State, Is.EqualTo(ClientSessionOwnerState.Unauthenticated));
        }

        /// <summary>
        /// 验证 ticket 绑定 channel、session generation 与 expiry，并且最多交付一次。
        /// </summary>
        /// <returns>等待认证、签发和单次交付断言完成的任务。</returns>
        [Test]
        public async Task TicketLeaseIsGenerationBoundExpiringAndSingleUse()
        {
            var clock = new FakeClock(1000);
            var api = new FakeHttpApi
            {
                LoginHandler = (_, __, ___) => Task.FromResult(ClientHttpResult<ClientAuthentication>.Success(
                    CreateAuthentication("account", "session", "access", "refresh"))),
                TicketHandler = (_, channel, __) => Task.FromResult(
                    ClientHttpResult<ClientConnectionTicket>.Success(new ClientConnectionTicket(
                        "opaque-ticket",
                        new ClientEndpoint(channel, "game.example.invalid", 4433),
                        new[] { ClientConnectionScope.Gameplay },
                        2000))),
            };
            var coordinator = await CreateCoordinatorAsync(api, clock);
            await coordinator.LoginAsync("fixture-user", "password", CancellationToken.None);

            var issue = await coordinator.IssueConnectionTicketAsync(
                ClientEndpointChannel.TlsTcp,
                CancellationToken.None);
            Assert.That(issue.IsSuccess, Is.True);
            Assert.That(coordinator.TryTakeConnectionTicket(issue.Value, out var ticketUse), Is.True);
            Assert.That(ticketUse.Credential, Is.EqualTo("opaque-ticket"));
            Assert.That(ticketUse.ToString(), Does.Not.Contain("opaque-ticket"));
            Assert.That(coordinator.TryTakeConnectionTicket(issue.Value, out _), Is.False);

            var expiring = await coordinator.IssueConnectionTicketAsync(
                ClientEndpointChannel.TlsTcp,
                CancellationToken.None);
            clock.UtcNowMilliseconds = 2000;
            Assert.That(coordinator.TryTakeConnectionTicket(expiring.Value, out _), Is.False);
        }

        /// <summary>验证 Bearer operation 不发送已知过期 access，而是先共享 Session refresh。</summary>
        /// <returns>等待 refresh、ticket 与 generation 断言完成的任务。</returns>
        [Test]
        public async Task ExpiredAccessRefreshesBeforeConnectionTicket()
        {
            var clock = new FakeClock(3000);
            string observedAccess = null;
            var api = new FakeHttpApi
            {
                LoginHandler = (_, __, ___) => Task.FromResult(ClientHttpResult<ClientAuthentication>.Success(
                    CreateAuthentication("account", "session", "expired-access", "refresh"))),
                RefreshHandler = (_, __) => Task.FromResult(ClientHttpResult<ClientTokenPair>.Success(
                    new ClientTokenPair("fresh-access", "fresh-refresh", 5000, 6000))),
                TicketHandler = (access, channel, _) =>
                {
                    observedAccess = access;
                    return Task.FromResult(ClientHttpResult<ClientConnectionTicket>.Success(
                        new ClientConnectionTicket(
                            "opaque-ticket",
                            new ClientEndpoint(channel, "game.example.invalid", 4433),
                            new[] { ClientConnectionScope.Gameplay },
                            4000)));
                },
            };
            var coordinator = await CreateCoordinatorAsync(api, clock);
            var login = await coordinator.LoginAsync("fixture-user", "password", CancellationToken.None);

            var issued = await coordinator.IssueConnectionTicketAsync(
                ClientEndpointChannel.TlsTcp,
                CancellationToken.None);

            Assert.That(issued.IsSuccess, Is.True);
            Assert.That(api.RefreshCount, Is.EqualTo(1));
            Assert.That(observedAccess, Is.EqualTo("fresh-access"));
            Assert.That(coordinator.TryTakeConnectionTicket(issued.Value, out var ticketUse), Is.True);
            Assert.That(ticketUse.SourceGeneration, Is.GreaterThan(login.Value.Generation));
        }

        /// <summary>
        /// 确认 invite accept 只提交 current generation 的未过期 reservation，且未知结果不自动重试。
        /// </summary>
        [Test]
        public async Task VisitInviteAcceptIsGenerationBoundExpiringAndNotRetried()
        {
            var clock = new FakeClock(1000);
            var acceptCompletion = new TaskCompletionSource<ClientHttpResult<ClientVisitReservation>>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            var authenticationQueue = new Queue<ClientAuthentication>();
            authenticationQueue.Enqueue(CreateAuthentication("account-a", "session-a", "access-a", "refresh-a"));
            authenticationQueue.Enqueue(CreateAuthentication("account-b", "session-b", "access-b", "refresh-b"));
            var acceptCalls = 0;
            var api = new FakeHttpApi
            {
                LoginHandler = (_, _, _) => Task.FromResult(
                    ClientHttpResult<ClientAuthentication>.Success(authenticationQueue.Dequeue())),
                AcceptHandler = (_, _, _, _) =>
                {
                    acceptCalls++;
                    return acceptCompletion.Task;
                },
            };
            var coordinator = await CreateCoordinatorAsync(api, clock);
            await coordinator.LoginAsync("user-a", "password", CancellationToken.None);
            var request = new ClientVisitInviteAcceptRequest("visit-a", "invite-a", 4);

            var pending = coordinator.AcceptVisitInviteAsync(
                request,
                "fixture-accept-key-0001",
                CancellationToken.None);
            await coordinator.LoginAsync("user-b", "password", CancellationToken.None);
            acceptCompletion.SetResult(ClientHttpResult<ClientVisitReservation>.Success(
                new ClientVisitReservation("visit-a", 5, 2000)));

            var late = await pending;
            Assert.That(late.IsSuccess, Is.False);
            Assert.That(late.Failure.Kind, Is.EqualTo(ClientHttpFailureKind.LocalPolicy));
            Assert.That(acceptCalls, Is.EqualTo(1));

            api.AcceptHandler = (_, _, _, _) => Task.FromResult(
                ClientHttpResult<ClientVisitReservation>.Success(
                    new ClientVisitReservation("visit-a", 5, 1000)));
            var expired = await coordinator.AcceptVisitInviteAsync(
                request,
                "fixture-accept-key-0002",
                CancellationToken.None);
            Assert.That(expired.IsSuccess, Is.False);
            Assert.That(expired.Failure.Kind, Is.EqualTo(ClientHttpFailureKind.MalformedResponse));

            api.AcceptHandler = (_, _, _, _) => Task.FromResult(
                ClientHttpResult<ClientVisitReservation>.Failed(
                    new ClientHttpFailure(ClientHttpFailureKind.Transport, "acceptVisitInvite")));
            var unknown = await coordinator.AcceptVisitInviteAsync(
                request,
                "fixture-accept-key-0003",
                CancellationToken.None);
            Assert.That(unknown.IsSuccess, Is.False);
            Assert.That(unknown.Failure.Kind, Is.EqualTo(ClientHttpFailureKind.Transport));
            Assert.That(acceptCalls, Is.EqualTo(1));
            Assert.That(coordinator.State, Is.EqualTo(ClientSessionOwnerState.Authenticated));
        }

        /// <summary>
        /// 验证 world admission 绑定 session generation、expiry、role/purpose 且最多交付一次。
        /// </summary>
        /// <returns>等待认证、签发与单次交付完成的任务。</returns>
        [Test]
        public async Task WorldAdmissionLeaseIsGenerationBoundExpiringAndSingleUse()
        {
            var clock = new FakeClock(1000);
            var admission = new ClientWorldAdmission(
                "wad1_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
                new ClientEndpoint(ClientEndpointChannel.TlsTcp, "game.example.invalid", 4433),
                ClientWorldRole.Owner,
                ClientWorldAdmissionPurpose.OwnWorld,
                2000);
            var api = new FakeHttpApi
            {
                LoginHandler = (_, __, ___) => Task.FromResult(ClientHttpResult<ClientAuthentication>.Success(
                    CreateAuthentication("account", "session", "access", "refresh"))),
                AdmissionHandler = (_, __, ___, ____) => Task.FromResult(
                    ClientHttpResult<ClientWorldAdmission>.Success(admission)),
            };
            var coordinator = await CreateCoordinatorAsync(api, clock);
            var login = await coordinator.LoginAsync("fixture-user", "password", CancellationToken.None);

            var issue = await coordinator.IssueWorldAdmissionAsync(
                ClientWorldAdmissionTarget.OwnWorld(),
                "fixture-admission-key-01",
                CancellationToken.None);
            Assert.That(issue.IsSuccess, Is.True);
            Assert.That(coordinator.TryTakeWorldAdmission(issue.Value, out var use), Is.True);
            Assert.That(use.Purpose, Is.EqualTo(ClientWorldAdmissionPurpose.OwnWorld));
            Assert.That(use.ToString(), Does.Not.Contain(use.Credential));
            Assert.That(coordinator.TryTakeWorldAdmission(issue.Value, out _), Is.False);

            var expiring = await coordinator.IssueWorldAdmissionAsync(
                ClientWorldAdmissionTarget.OwnWorld(),
                "fixture-admission-key-02",
                CancellationToken.None);
            clock.UtcNowMilliseconds = 2000;
            Assert.That(coordinator.TryTakeWorldAdmission(expiring.Value, out _), Is.False);

            clock.UtcNowMilliseconds = 1000;
            var invalidBinding = new ClientWorldAdmissionLease(
                new ClientWorldAdmission(
                    "wad1_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
                    new ClientEndpoint(ClientEndpointChannel.TlsTcp, "game.example.invalid", 4433),
                    ClientWorldRole.Owner,
                    ClientWorldAdmissionPurpose.Join,
                    2000),
                login.Value.Generation);
            Assert.That(coordinator.TryTakeWorldAdmission(invalidBinding, out _), Is.False);
        }

        /// <summary>
        /// 确认 current control connection 的更高 epoch 会清除对应 session lineage。
        /// </summary>
        [Test]
        public async Task ControlInvalidation_CurrentGeneration_ClearsSession()
        {
            var api = new FakeHttpApi
            {
                LoginHandler = (_, _, _) => Task.FromResult(
                    ClientHttpResult<ClientAuthentication>.Success(
                        CreateAuthentication("account-a", "session-a", "access-a", "refresh-a"))),
            };
            var coordinator = await CreateCoordinatorAsync(api, new FakeClock(1000));
            var login = await coordinator.LoginAsync("user", "password", CancellationToken.None);
            Assert.That(login.IsSuccess, Is.True);

            var invalidated = coordinator.TryInvalidateFromControl(login.Value.Generation, 2);

            Assert.That(invalidated, Is.True);
            Assert.That(coordinator.State, Is.EqualTo(ClientSessionOwnerState.Unauthenticated));
            Assert.That(coordinator.TryGetCurrent(out _), Is.False);
        }

        /// <summary>
        /// 确认同 generation 的非递增 epoch 不能伪造一次新的权威失效。
        /// </summary>
        [Test]
        public async Task ControlInvalidation_NonIncreasingEpoch_PreservesSession()
        {
            var api = new FakeHttpApi
            {
                LoginHandler = (_, _, _) => Task.FromResult(
                    ClientHttpResult<ClientAuthentication>.Success(
                        CreateAuthentication("account-a", "session-a", "access-a", "refresh-a"))),
            };
            var coordinator = await CreateCoordinatorAsync(api, new FakeClock(1000));
            var login = await coordinator.LoginAsync("user", "password", CancellationToken.None);
            Assert.That(login.IsSuccess, Is.True);

            var invalidated = coordinator.TryInvalidateFromControl(login.Value.Generation, 1);

            Assert.That(invalidated, Is.False);
            Assert.That(coordinator.State, Is.EqualTo(ClientSessionOwnerState.Authenticated));
            Assert.That(coordinator.TryGetCurrent(out var current), Is.True);
            Assert.That(current.Generation, Is.EqualTo(login.Value.Generation));
        }

        /// <summary>
        /// 确认旧 WSS 的迟到 invalidation 不能清除后发登录建立的新 session。
        /// </summary>
        [Test]
        public async Task ControlInvalidation_OldGeneration_PreservesNewLogin()
        {
            var authenticationQueue = new Queue<ClientAuthentication>();
            authenticationQueue.Enqueue(CreateAuthentication("account-a", "session-a", "access-a", "refresh-a"));
            authenticationQueue.Enqueue(CreateAuthentication("account-b", "session-b", "access-b", "refresh-b"));
            var api = new FakeHttpApi
            {
                LoginHandler = (_, _, _) => Task.FromResult(
                    ClientHttpResult<ClientAuthentication>.Success(authenticationQueue.Dequeue())),
            };
            var coordinator = await CreateCoordinatorAsync(api, new FakeClock(1000));
            var first = await coordinator.LoginAsync("user-a", "password", CancellationToken.None);
            var second = await coordinator.LoginAsync("user-b", "password", CancellationToken.None);

            var invalidated = coordinator.TryInvalidateFromControl(first.Value.Generation, 99);

            Assert.That(invalidated, Is.False);
            Assert.That(coordinator.State, Is.EqualTo(ClientSessionOwnerState.Authenticated));
            Assert.That(coordinator.TryGetCurrent(out var current), Is.True);
            Assert.That(current.Generation, Is.EqualTo(second.Value.Generation));
            Assert.That(current.Session.SessionID, Is.EqualTo("session-b"));
        }

        /// <summary>
        /// 创建初始化完成且配置 Ready 的 Session coordinator。
        /// </summary>
        /// <param name="api">受测试控制的强类型 HTTP API。</param>
        /// <param name="clock">受测试控制的 UTC 时钟。</param>
        /// <returns>允许认证调用的 coordinator。</returns>
        private static async Task<SessionCoordinator> CreateCoordinatorAsync(
            FakeHttpApi api,
            IClientClock clock)
        {
            var store = new ClientConfigurationStore();
            await store.InitializeAsync(CancellationToken.None);
            store.Publish(new ClientConfigurationSnapshot(
                new ClientVersionInfo(1, "0.1.0", "0.1.0"),
                CreateConfiguration()));
            var coordinator = new SessionCoordinator(store, api, clock);
            await coordinator.InitializeAsync(CancellationToken.None);
            return coordinator;
        }

        /// <summary>
        /// 创建测试使用的兼容 local 环境。
        /// </summary>
        /// <returns>协议版本为 1 的环境。</returns>
        private static ClientEnvironment CreateEnvironment()
        {
            return ClientEnvironment.Create(
                ClientEnvironmentKind.Test,
                "http://127.0.0.1:8080/",
                "0.1.0",
                1);
        }

        /// <summary>
        /// 创建含 WSS/TLS-TCP endpoint 与公开预算的配置投影。
        /// </summary>
        /// <returns>可供认证前置条件使用的完整配置。</returns>
        private static ClientBootstrapConfiguration CreateConfiguration()
        {
            return new ClientBootstrapConfiguration(
                new[]
                {
                    new ClientEndpoint(ClientEndpointChannel.Wss, "control.example.invalid", 443),
                    new ClientEndpoint(ClientEndpointChannel.TlsTcp, "game.example.invalid", 4433),
                },
                new ClientPublicLimits(4096, 65536));
        }

        /// <summary>
        /// 创建不泄漏 token 的完整认证结果。
        /// </summary>
        /// <param name="accountID">测试账号标识。</param>
        /// <param name="sessionID">测试 session 标识。</param>
        /// <param name="accessToken">测试 access token。</param>
        /// <param name="refreshToken">测试 refresh token。</param>
        /// <returns>不可变认证投影。</returns>
        private static ClientAuthentication CreateAuthentication(
            string accountID,
            string sessionID,
            string accessToken,
            string refreshToken)
        {
            return new ClientAuthentication(
                new ClientAccountSummary(accountID, "Fixture", 1),
                new ClientSessionSummary(sessionID, 1, 5000),
                new ClientTokenPair(accessToken, refreshToken, 3000, 4000),
                Array.Empty<ClientEndpoint>());
        }

        /// <summary>
        /// 创建异步认证 completion，便于控制并发返回顺序。
        /// </summary>
        /// <returns>异步连续执行的认证结果 completion。</returns>
        private static TaskCompletionSource<ClientHttpResult<ClientAuthentication>> NewAuthenticationCompletion()
        {
            return new TaskCompletionSource<ClientHttpResult<ClientAuthentication>>(
                TaskCreationOptions.RunContinuationsAsynchronously);
        }

        /// <summary>
        /// 提供可变 Unix 毫秒值的测试时钟。
        /// </summary>
        private sealed class FakeClock : IClientClock
        {
            /// <summary>
            /// 创建指定当前时间的测试时钟。
            /// </summary>
            /// <param name="utcNowMilliseconds">初始 Unix 时间，单位为毫秒。</param>
            internal FakeClock(long utcNowMilliseconds)
            {
                UtcNowMilliseconds = utcNowMilliseconds;
            }

            /// <summary>
            /// 获取或设置当前测试 Unix 时间，单位为毫秒。
            /// </summary>
            public long UtcNowMilliseconds { get; internal set; }
        }

        /// <summary>
        /// 为 application tests 提供十个强类型 operation 的确定性替换边界。
        /// </summary>
        private sealed class FakeHttpApi : IClientHttpApi
        {
            /// <summary>
            /// 保存按发生顺序记录的 operation 名称。
            /// </summary>
            internal List<string> Calls { get; } = new List<string>();

            /// <summary>
            /// 获取 refresh 调用次数。
            /// </summary>
            internal int RefreshCount { get; private set; }

            /// <summary>
            /// 获取或设置 version 响应函数。
            /// </summary>
            internal Func<CancellationToken, Task<ClientHttpResult<ClientVersionInfo>>> VersionHandler { get; set; }

            /// <summary>
            /// 获取或设置 config 响应函数。
            /// </summary>
            internal Func<CancellationToken, Task<ClientHttpResult<ClientBootstrapConfiguration>>> ConfigurationHandler { get; set; }

            /// <summary>
            /// 获取或设置 login 响应函数。
            /// </summary>
            internal Func<string, string, CancellationToken, Task<ClientHttpResult<ClientAuthentication>>> LoginHandler { get; set; }

            /// <summary>
            /// 获取或设置 refresh 响应函数。
            /// </summary>
            internal Func<string, CancellationToken, Task<ClientHttpResult<ClientTokenPair>>> RefreshHandler { get; set; }

            /// <summary>
            /// 获取或设置 logout 响应函数。
            /// </summary>
            internal Func<string, CancellationToken, Task<ClientHttpResult<ClientHttpEmpty>>> LogoutHandler { get; set; }

            /// <summary>
            /// 获取或设置 ticket 响应函数。
            /// </summary>
            internal Func<string, ClientEndpointChannel, CancellationToken, Task<ClientHttpResult<ClientConnectionTicket>>> TicketHandler { get; set; }

            /// <summary>
            /// 获取或设置 invite accept 响应函数。
            /// </summary>
            internal Func<string, ClientVisitInviteAcceptRequest, string, CancellationToken, Task<ClientHttpResult<ClientVisitReservation>>> AcceptHandler { get; set; }

            /// <summary>
            /// 获取或设置 world admission 响应函数。
            /// </summary>
            internal Func<string, ClientWorldAdmissionTarget, string, CancellationToken, Task<ClientHttpResult<ClientWorldAdmission>>> AdmissionHandler { get; set; }

            /// <inheritdoc />
            public Task<ClientHttpResult<ClientVersionInfo>> GetVersionAsync(CancellationToken cancellationToken)
            {
                Calls.Add("version");
                return VersionHandler(cancellationToken);
            }

            /// <inheritdoc />
            public Task<ClientHttpResult<ClientBootstrapConfiguration>> GetBootstrapConfigurationAsync(
                CancellationToken cancellationToken)
            {
                Calls.Add("config");
                return ConfigurationHandler(cancellationToken);
            }

            /// <inheritdoc />
            public Task<ClientHttpResult<ClientAuthentication>> RegisterAsync(
                string username,
                string password,
                string displayName,
                CancellationToken cancellationToken)
            {
                return Task.FromResult(ClientHttpResult<ClientAuthentication>.Failed(
                    new ClientHttpFailure(ClientHttpFailureKind.LocalPolicy, "registerAccount")));
            }

            /// <inheritdoc />
            public Task<ClientHttpResult<ClientAuthentication>> LoginAsync(
                string username,
                string password,
                CancellationToken cancellationToken)
            {
                return LoginHandler(username, password, cancellationToken);
            }

            /// <inheritdoc />
            public Task<ClientHttpResult<ClientTokenPair>> RefreshAsync(
                string refreshToken,
                CancellationToken cancellationToken)
            {
                RefreshCount++;
                return RefreshHandler(refreshToken, cancellationToken);
            }

            /// <inheritdoc />
            public Task<ClientHttpResult<ClientHttpEmpty>> LogoutAsync(
                string accessToken,
                CancellationToken cancellationToken)
            {
                return LogoutHandler(accessToken, cancellationToken);
            }

            /// <inheritdoc />
            public Task<ClientHttpResult<ClientConnectionTicket>> IssueConnectionTicketAsync(
                string accessToken,
                ClientEndpointChannel channel,
                CancellationToken cancellationToken)
            {
                return TicketHandler(accessToken, channel, cancellationToken);
            }

            /// <inheritdoc />
            public Task<ClientHttpResult<ClientWorldBootstrap>> GetWorldBootstrapAsync(
                string accessToken,
                CancellationToken cancellationToken)
            {
                return Task.FromResult(ClientHttpResult<ClientWorldBootstrap>.Failed(
                    new ClientHttpFailure(ClientHttpFailureKind.LocalPolicy, "getWorldBootstrap")));
            }

            /// <inheritdoc />
            public Task<ClientHttpResult<ClientVisitReservation>> AcceptVisitInviteAsync(
                string accessToken,
                ClientVisitInviteAcceptRequest request,
                string idempotencyKey,
                CancellationToken cancellationToken)
            {
                return AcceptHandler != null
                    ? AcceptHandler(accessToken, request, idempotencyKey, cancellationToken)
                    : Task.FromResult(ClientHttpResult<ClientVisitReservation>.Failed(
                        new ClientHttpFailure(ClientHttpFailureKind.LocalPolicy, "acceptVisitInvite")));
            }

            /// <inheritdoc />
            public Task<ClientHttpResult<ClientWorldAdmission>> IssueWorldAdmissionAsync(
                string accessToken,
                ClientWorldAdmissionTarget target,
                string idempotencyKey,
                CancellationToken cancellationToken)
            {
                return AdmissionHandler != null
                    ? AdmissionHandler(accessToken, target, idempotencyKey, cancellationToken)
                    : Task.FromResult(ClientHttpResult<ClientWorldAdmission>.Failed(
                        new ClientHttpFailure(ClientHttpFailureKind.LocalPolicy, "issueWorldAdmission")));
            }
        }
    }
}
