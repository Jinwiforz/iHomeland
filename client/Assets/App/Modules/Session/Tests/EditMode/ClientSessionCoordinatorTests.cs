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
            var configCompletion = new TaskCompletionSource<ClientGatewayResult<ClientBootstrapConfiguration>>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            api.VersionHandler = _ => Task.FromResult(ClientGatewayResult<ClientVersionInfo>.Success(
                new ClientVersionInfo(1, "0.1.0", "0.1.0")));
            api.ConfigurationHandler = _ => configCompletion.Task;
            var store = new ClientConfigurationStore();
            await store.InitializeAsync(CancellationToken.None);
            var service = new ClientBootstrapService(CreateEnvironment(), api, store);

            var bootstrap = service.BootstrapAsync(CancellationToken.None);
            Assert.That(api.Calls, Is.EqualTo(new[] { "version", "config" }));
            Assert.That(store.State, Is.EqualTo(ClientConfigurationState.Empty));
            configCompletion.SetResult(ClientGatewayResult<ClientBootstrapConfiguration>.Success(
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
            var configCompletion = new TaskCompletionSource<ClientGatewayResult<ClientBootstrapConfiguration>>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            var api = new FakeHttpApi
            {
                VersionHandler = _ => Task.FromResult(ClientGatewayResult<ClientVersionInfo>.Success(
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
                Assert.That(cancelled.Failure.Kind, Is.EqualTo(ClientGatewayFailureKind.CallerCancelled));
                Assert.That(active.IsCompleted, Is.False);
            }

            configCompletion.SetResult(ClientGatewayResult<ClientBootstrapConfiguration>.Success(
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
                VersionHandler = _ => Task.FromResult(ClientGatewayResult<ClientVersionInfo>.Success(
                    new ClientVersionInfo(2, "9.0.0", "9.0.0"))),
            };
            var store = new ClientConfigurationStore();
            await store.InitializeAsync(CancellationToken.None);
            var service = new ClientBootstrapService(CreateEnvironment(), api, store);

            var result = await service.BootstrapAsync(CancellationToken.None);

            Assert.That(result.Failure.Kind, Is.EqualTo(ClientGatewayFailureKind.LocalPolicy));
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
            secondCompletion.SetResult(ClientGatewayResult<ClientAuthentication>.Success(
                CreateAuthentication("account-second", "session-second", "access-second", "refresh-second")));
            var secondResult = await second;
            firstCompletion.SetResult(ClientGatewayResult<ClientAuthentication>.Success(
                CreateAuthentication("account-first", "session-first", "access-first", "refresh-first")));
            var firstResult = await first;

            Assert.That(secondResult.IsSuccess, Is.True);
            Assert.That(firstResult.Failure.Kind, Is.EqualTo(ClientGatewayFailureKind.LocalPolicy));
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
            var refreshCompletion = new TaskCompletionSource<ClientGatewayResult<ClientTokenPair>>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            var api = new FakeHttpApi
            {
                LoginHandler = (_, __, ___) => Task.FromResult(ClientGatewayResult<ClientAuthentication>.Success(
                    CreateAuthentication("account", "session", "access", "refresh"))),
                RefreshHandler = (_, __) => refreshCompletion.Task,
            };
            var coordinator = await CreateCoordinatorAsync(api, new FakeClock(1000));
            await coordinator.LoginAsync("fixture-user", "password", CancellationToken.None);

            var first = coordinator.RefreshAsync(CancellationToken.None);
            var second = coordinator.RefreshAsync(CancellationToken.None);
            Assert.That(second, Is.SameAs(first));
            Assert.That(api.RefreshCount, Is.EqualTo(1));
            await coordinator.ForgetAsync(CancellationToken.None);
            refreshCompletion.SetResult(ClientGatewayResult<ClientTokenPair>.Success(
                new ClientTokenPair("new-access", "new-refresh", 3000, 4000)));

            var result = await first;
            Assert.That(result.Failure.Kind, Is.EqualTo(ClientGatewayFailureKind.LocalPolicy));
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
            var refreshCompletion = new TaskCompletionSource<ClientGatewayResult<ClientTokenPair>>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            var api = new FakeHttpApi
            {
                LoginHandler = (_, __, ___) => Task.FromResult(ClientGatewayResult<ClientAuthentication>.Success(
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
                Assert.That(cancelled.Failure.Kind, Is.EqualTo(ClientGatewayFailureKind.CallerCancelled));
                Assert.That(active.IsCompleted, Is.False);
            }

            refreshCompletion.SetResult(ClientGatewayResult<ClientTokenPair>.Success(
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
                LoginHandler = (_, __, ___) => Task.FromResult(ClientGatewayResult<ClientAuthentication>.Success(
                    CreateAuthentication("account", "session", "access", "refresh"))),
                RefreshHandler = (_, __) => Task.FromResult(ClientGatewayResult<ClientTokenPair>.Failed(
                    new ClientGatewayFailure(ClientGatewayFailureKind.Transport, "refreshSession"))),
            };
            var coordinator = await CreateCoordinatorAsync(api, new FakeClock(1000));
            await coordinator.LoginAsync("fixture-user", "password", CancellationToken.None);

            var result = await coordinator.RefreshAsync(CancellationToken.None);

            Assert.That(result.Failure.Kind, Is.EqualTo(ClientGatewayFailureKind.Transport));
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
                LoginHandler = (_, __, ___) => Task.FromResult(ClientGatewayResult<ClientAuthentication>.Success(
                    CreateAuthentication("account", "session", "access", "refresh"))),
                LogoutHandler = (_, __) => Task.FromResult(ClientGatewayResult<ClientGatewayEmpty>.Failed(
                    new ClientGatewayFailure(ClientGatewayFailureKind.Timeout, "logoutSession"))),
            };
            var coordinator = await CreateCoordinatorAsync(api, new FakeClock(1000));
            await coordinator.LoginAsync("fixture-user", "password", CancellationToken.None);

            var result = await coordinator.LogoutAsync(CancellationToken.None);

            Assert.That(result.Failure.Kind, Is.EqualTo(ClientGatewayFailureKind.Timeout));
            Assert.That(coordinator.State, Is.EqualTo(ClientSessionOwnerState.Unresolved));
            var ticket = await coordinator.IssueConnectionTicketAsync(
                ClientEndpointChannel.TlsTcp,
                CancellationToken.None);
            Assert.That(ticket.Failure.Kind, Is.EqualTo(ClientGatewayFailureKind.LocalPolicy));

            var relogin = await coordinator.LoginAsync(
                "fixture-user",
                "password",
                CancellationToken.None);
            Assert.That(relogin.IsSuccess, Is.True);
            Assert.That(coordinator.State, Is.EqualTo(ClientSessionOwnerState.Authenticated));

            await coordinator.LogoutAsync(CancellationToken.None);
            Assert.That(coordinator.State, Is.EqualTo(ClientSessionOwnerState.Unresolved));
            await coordinator.ForgetAsync(CancellationToken.None);
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
                LoginHandler = (_, __, ___) => Task.FromResult(ClientGatewayResult<ClientAuthentication>.Success(
                    CreateAuthentication("account", "session", "access", "refresh"))),
                TicketHandler = (_, channel, __) => Task.FromResult(
                    ClientGatewayResult<ClientConnectionTicket>.Success(new ClientConnectionTicket(
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
                LoginHandler = (_, __, ___) => Task.FromResult(ClientGatewayResult<ClientAuthentication>.Success(
                    CreateAuthentication("account", "session", "expired-access", "refresh"))),
                RefreshHandler = (_, __) => Task.FromResult(ClientGatewayResult<ClientTokenPair>.Success(
                    new ClientTokenPair("fresh-access", "fresh-refresh", 4500, 5000))),
                TicketHandler = (access, channel, _) =>
                {
                    observedAccess = access;
                    return Task.FromResult(ClientGatewayResult<ClientConnectionTicket>.Success(
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
            var acceptCompletion = new TaskCompletionSource<ClientGatewayResult<ClientVisitReservation>>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            var authenticationQueue = new Queue<ClientAuthentication>();
            authenticationQueue.Enqueue(CreateAuthentication("account-a", "session-a", "access-a", "refresh-a"));
            authenticationQueue.Enqueue(CreateAuthentication("account-b", "session-b", "access-b", "refresh-b"));
            var acceptCalls = 0;
            var api = new FakeHttpApi
            {
                LoginHandler = (_, _, _) => Task.FromResult(
                    ClientGatewayResult<ClientAuthentication>.Success(authenticationQueue.Dequeue())),
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
            acceptCompletion.SetResult(ClientGatewayResult<ClientVisitReservation>.Success(
                new ClientVisitReservation("visit-a", 5, 2000)));

            var late = await pending;
            Assert.That(late.IsSuccess, Is.False);
            Assert.That(late.Failure.Kind, Is.EqualTo(ClientGatewayFailureKind.LocalPolicy));
            Assert.That(acceptCalls, Is.EqualTo(1));

            api.AcceptHandler = (_, _, _, _) => Task.FromResult(
                ClientGatewayResult<ClientVisitReservation>.Success(
                    new ClientVisitReservation("visit-a", 5, 1000)));
            var expired = await coordinator.AcceptVisitInviteAsync(
                request,
                "fixture-accept-key-0002",
                CancellationToken.None);
            Assert.That(expired.IsSuccess, Is.False);
            Assert.That(expired.Failure.Kind, Is.EqualTo(ClientGatewayFailureKind.MalformedResponse));

            api.AcceptHandler = (_, _, _, _) => Task.FromResult(
                ClientGatewayResult<ClientVisitReservation>.Failed(
                    new ClientGatewayFailure(ClientGatewayFailureKind.Transport, "acceptVisitInvite")));
            var unknown = await coordinator.AcceptVisitInviteAsync(
                request,
                "fixture-accept-key-0003",
                CancellationToken.None);
            Assert.That(unknown.IsSuccess, Is.False);
            Assert.That(unknown.Failure.Kind, Is.EqualTo(ClientGatewayFailureKind.Transport));
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
                0,
                2000);
            var api = new FakeHttpApi
            {
                LoginHandler = (_, __, ___) => Task.FromResult(ClientGatewayResult<ClientAuthentication>.Success(
                    CreateAuthentication("account", "session", "access", "refresh"))),
                AdmissionHandler = (_, __, ___, ____) => Task.FromResult(
                    ClientGatewayResult<ClientWorldAdmission>.Success(admission)),
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
                    0,
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
                    ClientGatewayResult<ClientAuthentication>.Success(
                        CreateAuthentication("account-a", "session-a", "access-a", "refresh-a"))),
            };
            var coordinator = await CreateCoordinatorAsync(api, new FakeClock(1000));
            var login = await coordinator.LoginAsync("user", "password", CancellationToken.None);
            Assert.That(login.IsSuccess, Is.True);
            var invalidationCount = 0;
            var invalidatedGeneration = 0L;
            coordinator.Invalidated += generation =>
            {
                invalidationCount++;
                invalidatedGeneration = generation;
            };

            var invalidated = await coordinator.TryInvalidateFromControlAsync(
                login.Value.Generation,
                2);

            Assert.That(invalidated, Is.True);
            Assert.That(coordinator.State, Is.EqualTo(ClientSessionOwnerState.Unauthenticated));
            Assert.That(coordinator.TryGetCurrent(out _), Is.False);
            Assert.That(invalidationCount, Is.EqualTo(1));
            Assert.That(invalidatedGeneration, Is.GreaterThan(login.Value.Generation));
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
                    ClientGatewayResult<ClientAuthentication>.Success(
                        CreateAuthentication("account-a", "session-a", "access-a", "refresh-a"))),
            };
            var coordinator = await CreateCoordinatorAsync(api, new FakeClock(1000));
            var login = await coordinator.LoginAsync("user", "password", CancellationToken.None);
            Assert.That(login.IsSuccess, Is.True);

            var invalidated = await coordinator.TryInvalidateFromControlAsync(
                login.Value.Generation,
                1);

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
                    ClientGatewayResult<ClientAuthentication>.Success(authenticationQueue.Dequeue())),
            };
            var coordinator = await CreateCoordinatorAsync(api, new FakeClock(1000));
            var first = await coordinator.LoginAsync("user-a", "password", CancellationToken.None);
            var second = await coordinator.LoginAsync("user-b", "password", CancellationToken.None);

            var invalidated = await coordinator.TryInvalidateFromControlAsync(
                first.Value.Generation,
                99);

            Assert.That(invalidated, Is.False);
            Assert.That(coordinator.State, Is.EqualTo(ClientSessionOwnerState.Authenticated));
            Assert.That(coordinator.TryGetCurrent(out var current), Is.True);
            Assert.That(current.Generation, Is.EqualTo(second.Value.Generation));
            Assert.That(current.Session.SessionID, Is.EqualTo("session-b"));
        }

        /// <summary>验证login只有在secure replace完成后才发布Authenticated snapshot。</summary>
        [Test]
        public async Task Login_PersistsRefreshLineage_BeforePublishingSnapshot()
        {
            var replaceEntered = new TaskCompletionSource<bool>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            var allowReplace = new TaskCompletionSource<bool>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            var secureStore = new FakeClientSecureSessionStore
            {
                ReplaceHandler = async (_, __) =>
                {
                    replaceEntered.TrySetResult(true);
                    await allowReplace.Task;
                    return ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>.Success(
                        ClientSecureSessionStoreEmpty.Value);
                },
            };
            var api = new FakeHttpApi
            {
                LoginHandler = (_, __, ___) => Task.FromResult(
                    ClientGatewayResult<ClientAuthentication>.Success(
                        CreateAuthentication("account", "session", "access", "refresh"))),
            };
            var coordinator = await CreateCoordinatorAsync(
                api,
                new FakeClock(1000),
                secureStore);

            var login = coordinator.LoginAsync("user", "password", CancellationToken.None);
            await replaceEntered.Task;

            Assert.That(login.IsCompleted, Is.False);
            Assert.That(coordinator.TryGetCurrent(out _), Is.False);
            allowReplace.TrySetResult(true);
            var result = await login;

            Assert.That(result.IsSuccess, Is.True);
            Assert.That(secureStore.GetRecord().RefreshToken, Is.EqualTo("refresh"));
            Assert.That(coordinator.TryGetCurrent(out var current), Is.True);
            Assert.That(current.Generation, Is.EqualTo(result.Value.Generation));
        }

        /// <summary>验证login安全存储失败时不发布credential，并尽力retire服务端候选。</summary>
        [Test]
        public async Task Login_ReplaceFailure_FailsClosed()
        {
            var logoutCount = 0;
            var secureStore = new FakeClientSecureSessionStore
            {
                ReplaceOutcome = ClientSecureSessionStoreOutcome.StorageFailure,
            };
            var api = new FakeHttpApi
            {
                LoginHandler = (_, __, ___) => Task.FromResult(
                    ClientGatewayResult<ClientAuthentication>.Success(
                        CreateAuthentication("account", "session", "access", "refresh"))),
                LogoutHandler = (_, __) =>
                {
                    logoutCount++;
                    return Task.FromResult(ClientGatewayResult<ClientGatewayEmpty>.Success(
                        new ClientGatewayEmpty()));
                },
            };
            var coordinator = await CreateCoordinatorAsync(
                api,
                new FakeClock(1000),
                secureStore);

            var result = await coordinator.LoginAsync(
                "user",
                "password",
                CancellationToken.None);

            Assert.That(result.Failure.Kind, Is.EqualTo(ClientGatewayFailureKind.SecureStorage));
            Assert.That(coordinator.TryGetCurrent(out _), Is.False);
            Assert.That(secureStore.GetRecord(), Is.Null);
            Assert.That(logoutCount, Is.EqualTo(1));
        }

        /// <summary>验证第二个production profile writer得到稳定ownership失败而非内部错误。</summary>
        [Test]
        public async Task Login_ProfileInUse_PreservesStableFailure()
        {
            var secureStore = new FakeClientSecureSessionStore
            {
                ReplaceOutcome = ClientSecureSessionStoreOutcome.ProfileInUse,
            };
            var api = new FakeHttpApi
            {
                LoginHandler = (_, __, ___) => Task.FromResult(
                    ClientGatewayResult<ClientAuthentication>.Success(
                        CreateAuthentication("account", "session", "access", "refresh"))),
                LogoutHandler = (_, __) => Task.FromResult(
                    ClientGatewayResult<ClientGatewayEmpty>.Success(new ClientGatewayEmpty())),
            };
            var coordinator = await CreateCoordinatorAsync(
                api,
                new FakeClock(1000),
                secureStore);

            var result = await coordinator.LoginAsync(
                "user",
                "password",
                CancellationToken.None);

            Assert.That(
                result.Failure.Kind,
                Is.EqualTo(ClientGatewayFailureKind.SecureStorageProfileInUse));
            Assert.That(coordinator.TryGetCurrent(out _), Is.False);
        }

        /// <summary>验证refresh replace失败会同时撤销内存与旧持久lineage。</summary>
        [Test]
        public async Task Refresh_ReplaceFailure_RetiresOldLineage()
        {
            var secureStore = new FakeClientSecureSessionStore();
            var api = new FakeHttpApi
            {
                LoginHandler = (_, __, ___) => Task.FromResult(
                    ClientGatewayResult<ClientAuthentication>.Success(
                        CreateAuthentication("account", "session", "access", "refresh"))),
                RefreshHandler = (_, __) => Task.FromResult(
                    ClientGatewayResult<ClientTokenPair>.Success(
                        new ClientTokenPair("access-new", "refresh-new", 4500, 5000))),
            };
            var coordinator = await CreateCoordinatorAsync(
                api,
                new FakeClock(1000),
                secureStore);
            await coordinator.LoginAsync("user", "password", CancellationToken.None);
            secureStore.ReplaceOutcome = ClientSecureSessionStoreOutcome.StorageFailure;

            var result = await coordinator.RefreshAsync(CancellationToken.None);

            Assert.That(result.Failure.Kind, Is.EqualTo(ClientGatewayFailureKind.SecureStorage));
            Assert.That(coordinator.State, Is.EqualTo(ClientSessionOwnerState.Unresolved));
            Assert.That(coordinator.TryGetCurrent(out _), Is.False);
            Assert.That(secureStore.GetRecord(), Is.Null);
            Assert.That(secureStore.DeleteCount, Is.EqualTo(1));
        }

        /// <summary>验证logout删除失败仍撤销内存，并明确暴露secure storage终态。</summary>
        [Test]
        public async Task Logout_DeleteFailure_IsUnresolvedStorageFailure()
        {
            var secureStore = new FakeClientSecureSessionStore();
            var api = new FakeHttpApi
            {
                LoginHandler = (_, __, ___) => Task.FromResult(
                    ClientGatewayResult<ClientAuthentication>.Success(
                        CreateAuthentication("account", "session", "access", "refresh"))),
                LogoutHandler = (_, __) => Task.FromResult(
                    ClientGatewayResult<ClientGatewayEmpty>.Success(new ClientGatewayEmpty())),
            };
            var coordinator = await CreateCoordinatorAsync(
                api,
                new FakeClock(1000),
                secureStore);
            await coordinator.LoginAsync("user", "password", CancellationToken.None);
            secureStore.DeleteOutcome = ClientSecureSessionStoreOutcome.StorageFailure;

            var result = await coordinator.LogoutAsync(CancellationToken.None);

            Assert.That(result.Failure.Kind, Is.EqualTo(ClientGatewayFailureKind.SecureStorage));
            Assert.That(coordinator.State, Is.EqualTo(ClientSessionOwnerState.Unresolved));
            Assert.That(coordinator.TryGetCurrent(out _), Is.False);
        }

        /// <summary>验证更高epoch清理失败时不恢复旧credential，并进入可诊断Unresolved。</summary>
        [Test]
        public async Task ControlInvalidation_DeleteFailure_RemainsFailClosed()
        {
            var secureStore = new FakeClientSecureSessionStore();
            var api = new FakeHttpApi
            {
                LoginHandler = (_, __, ___) => Task.FromResult(
                    ClientGatewayResult<ClientAuthentication>.Success(
                        CreateAuthentication("account", "session", "access", "refresh"))),
            };
            var coordinator = await CreateCoordinatorAsync(
                api,
                new FakeClock(1000),
                secureStore);
            var login = await coordinator.LoginAsync("user", "password", CancellationToken.None);
            secureStore.DeleteOutcome = ClientSecureSessionStoreOutcome.StorageFailure;

            var invalidated = await coordinator.TryInvalidateFromControlAsync(
                login.Value.Generation,
                2);

            Assert.That(invalidated, Is.True);
            Assert.That(coordinator.State, Is.EqualTo(ClientSessionOwnerState.Unresolved));
            Assert.That(coordinator.TryGetCurrent(out _), Is.False);
        }

        /// <summary>验证停止立即撤销current generation，且迟到refresh不能恢复Session或产生第二次失效通知。</summary>
        [Test]
        public async Task Stop_RejectsLateRefreshAndPublishesSingleInvalidation()
        {
            var refreshCompletion = new TaskCompletionSource<ClientGatewayResult<ClientTokenPair>>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            var api = new FakeHttpApi
            {
                LoginHandler = (_, __, ___) => Task.FromResult(
                    ClientGatewayResult<ClientAuthentication>.Success(
                        CreateAuthentication("account", "session", "access", "refresh"))),
                RefreshHandler = (_, __) => refreshCompletion.Task,
            };
            var coordinator = await CreateCoordinatorAsync(api, new FakeClock(1000));
            var login = await coordinator.LoginAsync("user", "password", CancellationToken.None);
            var invalidated = new List<long>();
            coordinator.Invalidated += invalidated.Add;

            var refresh = coordinator.RefreshAsync(CancellationToken.None);
            await coordinator.StopAsync(CancellationToken.None);

            Assert.That(coordinator.State, Is.EqualTo(ClientSessionOwnerState.Stopped));
            Assert.That(coordinator.TryGetCurrent(out _), Is.False);
            Assert.That(invalidated, Has.Count.EqualTo(1));
            Assert.That(invalidated[0], Is.GreaterThan(login.Value.Generation));

            refreshCompletion.SetResult(ClientGatewayResult<ClientTokenPair>.Success(
                new ClientTokenPair("late-access", "late-refresh", 3000, 4000)));
            var late = await refresh;

            Assert.That(late.Failure.Kind, Is.EqualTo(ClientGatewayFailureKind.LocalPolicy));
            Assert.That(coordinator.State, Is.EqualTo(ClientSessionOwnerState.Stopped));
            Assert.That(coordinator.TryGetCurrent(out _), Is.False);
            Assert.That(invalidated, Has.Count.EqualTo(1));
        }

        /// <summary>验证旧refresh晚到不能覆盖后发login已经持久化的新lineage。</summary>
        [Test]
        public async Task NewLogin_WinsAgainstLateRefreshPersistence()
        {
            var refreshCompletion = new TaskCompletionSource<ClientGatewayResult<ClientTokenPair>>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            var authenticationQueue = new Queue<ClientAuthentication>();
            authenticationQueue.Enqueue(
                CreateAuthentication("account-a", "session-a", "access-a", "refresh-a"));
            authenticationQueue.Enqueue(
                CreateAuthentication("account-b", "session-b", "access-b", "refresh-b"));
            var secureStore = new FakeClientSecureSessionStore();
            var api = new FakeHttpApi
            {
                LoginHandler = (_, __, ___) => Task.FromResult(
                    ClientGatewayResult<ClientAuthentication>.Success(authenticationQueue.Dequeue())),
                RefreshHandler = (_, __) => refreshCompletion.Task,
            };
            var coordinator = await CreateCoordinatorAsync(
                api,
                new FakeClock(1000),
                secureStore);
            await coordinator.LoginAsync("user-a", "password", CancellationToken.None);

            var refresh = coordinator.RefreshAsync(CancellationToken.None);
            var relogin = await coordinator.LoginAsync(
                "user-b",
                "password",
                CancellationToken.None);
            refreshCompletion.SetResult(ClientGatewayResult<ClientTokenPair>.Success(
                new ClientTokenPair("access-late", "refresh-late", 4500, 5000)));
            var late = await refresh;

            Assert.That(relogin.IsSuccess, Is.True);
            Assert.That(late.Failure.Kind, Is.EqualTo(ClientGatewayFailureKind.LocalPolicy));
            Assert.That(coordinator.TryGetCurrent(out var current), Is.True);
            Assert.That(current.Session.SessionID, Is.EqualTo("session-b"));
            Assert.That(secureStore.GetRecord().RefreshToken, Is.EqualTo("refresh-b"));
        }

        /// <summary>
        /// 创建初始化完成且配置 Ready 的 Session coordinator。
        /// </summary>
        /// <param name="api">受测试控制的强类型 HTTP API。</param>
        /// <param name="clock">受测试控制的 UTC 时钟。</param>
        /// <param name="secureStore">可选的受控secure store；为空时创建默认fake。</param>
        /// <returns>允许认证调用的 coordinator。</returns>
        private static async Task<SessionCoordinator> CreateCoordinatorAsync(
            FakeHttpApi api,
            IClientClock clock,
            FakeClientSecureSessionStore secureStore = null)
        {
            var store = new ClientConfigurationStore();
            await store.InitializeAsync(CancellationToken.None);
            store.Publish(new ClientConfigurationSnapshot(
                new ClientVersionInfo(1, "0.1.0", "0.1.0"),
                CreateConfiguration()));
            var coordinator = new SessionCoordinator(
                store,
                api,
                clock,
                secureStore ?? new FakeClientSecureSessionStore(),
                FakeClientSecureSessionStore.EnvironmentBinding);
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
        private static TaskCompletionSource<ClientGatewayResult<ClientAuthentication>> NewAuthenticationCompletion()
        {
            return new TaskCompletionSource<ClientGatewayResult<ClientAuthentication>>(
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
        private sealed class FakeHttpApi : IClientBootstrapGateway, IClientSessionGateway
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
            internal Func<CancellationToken, Task<ClientGatewayResult<ClientVersionInfo>>> VersionHandler { get; set; }

            /// <summary>
            /// 获取或设置 config 响应函数。
            /// </summary>
            internal Func<CancellationToken, Task<ClientGatewayResult<ClientBootstrapConfiguration>>> ConfigurationHandler { get; set; }

            /// <summary>
            /// 获取或设置 login 响应函数。
            /// </summary>
            internal Func<string, string, CancellationToken, Task<ClientGatewayResult<ClientAuthentication>>> LoginHandler { get; set; }

            /// <summary>
            /// 获取或设置 refresh 响应函数。
            /// </summary>
            internal Func<string, CancellationToken, Task<ClientGatewayResult<ClientTokenPair>>> RefreshHandler { get; set; }

            /// <summary>
            /// 获取或设置 logout 响应函数。
            /// </summary>
            internal Func<string, CancellationToken, Task<ClientGatewayResult<ClientGatewayEmpty>>> LogoutHandler { get; set; }

            /// <summary>
            /// 获取或设置 ticket 响应函数。
            /// </summary>
            internal Func<string, ClientEndpointChannel, CancellationToken, Task<ClientGatewayResult<ClientConnectionTicket>>> TicketHandler { get; set; }

            /// <summary>
            /// 获取或设置 invite accept 响应函数。
            /// </summary>
            internal Func<string, ClientVisitInviteAcceptRequest, string, CancellationToken, Task<ClientGatewayResult<ClientVisitReservation>>> AcceptHandler { get; set; }

            /// <summary>
            /// 获取或设置 world admission 响应函数。
            /// </summary>
            internal Func<string, ClientWorldAdmissionTarget, string, CancellationToken, Task<ClientGatewayResult<ClientWorldAdmission>>> AdmissionHandler { get; set; }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientVersionInfo>> GetVersionAsync(CancellationToken cancellationToken)
            {
                Calls.Add("version");
                return VersionHandler(cancellationToken);
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientBootstrapConfiguration>> GetBootstrapConfigurationAsync(
                CancellationToken cancellationToken)
            {
                Calls.Add("config");
                return ConfigurationHandler(cancellationToken);
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientAuthentication>> RegisterAsync(ClientRegisterGatewayRequest request, CancellationToken cancellationToken)
            {
                return Task.FromResult(ClientGatewayResult<ClientAuthentication>.Failed(
                    new ClientGatewayFailure(ClientGatewayFailureKind.LocalPolicy, "registerAccount")));
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientAuthentication>> LoginAsync(ClientLoginGatewayRequest request, CancellationToken cancellationToken)
            {
                return LoginHandler(
                    request.Username,
                    Take(request.Password, ClientCredentialPurpose.LoginPassword),
                    cancellationToken);
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientTokenPair>> RefreshAsync(ClientCredentialGatewayRequest request, CancellationToken cancellationToken)
            {
                RefreshCount++;
                return RefreshHandler(
                    Take(request.Credential, ClientCredentialPurpose.RefreshSession),
                    cancellationToken);
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientGatewayEmpty>> LogoutAsync(ClientCredentialGatewayRequest request, CancellationToken cancellationToken)
            {
                return LogoutHandler != null
                    ? LogoutHandler(
                        Take(
                            request.Credential,
                            ClientCredentialPurpose.HttpAuthorization),
                        cancellationToken)
                    : Task.FromResult(ClientGatewayResult<ClientGatewayEmpty>.Success(
                        new ClientGatewayEmpty()));
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientConnectionTicket>> IssueConnectionTicketAsync(ClientConnectionTicketGatewayRequest request, CancellationToken cancellationToken)
            {
                return TicketHandler(
                    Take(
                        request.Authorization,
                        ClientCredentialPurpose.HttpAuthorization),
                    request.Channel,
                    cancellationToken);
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientWorldBootstrap>> GetWorldBootstrapAsync(ClientCredentialGatewayRequest request, CancellationToken cancellationToken)
            {
                return Task.FromResult(ClientGatewayResult<ClientWorldBootstrap>.Failed(
                    new ClientGatewayFailure(ClientGatewayFailureKind.LocalPolicy, "getWorldBootstrap")));
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientVisitReservation>> AcceptVisitInviteAsync(ClientAcceptVisitInviteGatewayRequest request, CancellationToken cancellationToken)
            {
                return AcceptHandler != null
                    ? AcceptHandler(
                        Take(
                            request.Authorization,
                            ClientCredentialPurpose.HttpAuthorization),
                        request.Invite,
                        request.IdempotencyKey,
                        cancellationToken)
                    : Task.FromResult(ClientGatewayResult<ClientVisitReservation>.Failed(
                        new ClientGatewayFailure(ClientGatewayFailureKind.LocalPolicy, "acceptVisitInvite")));
            }

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientWorldAdmission>> IssueWorldAdmissionAsync(ClientWorldAdmissionGatewayRequest request, CancellationToken cancellationToken)
            {
                return AdmissionHandler != null
                    ? AdmissionHandler(
                        Take(
                            request.Authorization,
                            ClientCredentialPurpose.HttpAuthorization),
                        request.Target,
                        request.IdempotencyKey,
                        cancellationToken)
                    : Task.FromResult(ClientGatewayResult<ClientWorldAdmission>.Failed(
                        new ClientGatewayFailure(ClientGatewayFailureKind.LocalPolicy, "issueWorldAdmission")));
            }

            /// <summary>从 gateway request 验证并取得测试 credential。</summary>
            /// <param name="lease">待验证的单次 lease。</param>
            /// <param name="purpose">当前 operation 要求的 purpose。</param>
            /// <returns>测试 handler 需要的 opaque credential。</returns>
            private static string Take(
                ClientCredentialLease lease,
                ClientCredentialPurpose purpose)
            {
                Assert.That(lease.TryTake(purpose, out var credential), Is.True);
                return credential;
            }
        }
    }
}
