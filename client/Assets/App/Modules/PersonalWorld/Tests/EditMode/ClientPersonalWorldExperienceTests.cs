using System;
using System.Net.WebSockets;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.AppShell.Application.Bootstrap;
using IHomeland.Client.Networking.Application.Control;
using IHomeland.Client.Networking.Application.Gameplay;
using IHomeland.Client.Session.Application;
using IHomeland.Client.PersonalWorld.Application;
using IHomeland.Client.AppShell.Application.Configuration;
using IHomeland.Client.Core.Foundation.Lifetime;
using IHomeland.Client.Core.Foundation.Time;
using IHomeland.Client.Networking.Application.Contracts;
using IHomeland.Client.Networking.Application.Ports;
using IHomeland.Client.Networking.Infrastructure.Http;
using IHomeland.Client.Networking.Infrastructure.Tcp;
using IHomeland.Client.Core.Infrastructure.Time;
using IHomeland.Client.Networking.Infrastructure.WebSocket;
using IHomeland.Client.AppShell.Presentation.Navigation;
using IHomeland.Client.PersonalWorld.Presentation;
using IHomeland.Client.PersonalWorld.Runtime.Scenes;
using IHomeland.Client.Session.Tests.EditMode;
using NUnit.Framework;

namespace IHomeland.Client.PersonalWorld.Tests.EditMode
{
    /// <summary>使用无 listener 对象图验证个人世界 Experience 的入口、取消和通知边界。</summary>
    public sealed class ClientPersonalWorldExperienceTests
    {
        /// <summary>验证初始化只提交本地 Login route，不调用任一 HTTP operation。</summary>
        [Test]
        public async Task ColdStartOpensOnlyLoginWithoutNetworkSideEffect()
        {
            var fixture = await ExperienceFixture.CreateAsync();
            try
            {
                Assert.That(fixture.Api.TotalCalls, Is.Zero);
                Assert.That(fixture.Router.CurrentSnapshot.Items, Has.Count.EqualTo(1));
                Assert.That(
                    fixture.Router.CurrentSnapshot.Items[0].Definition.RouteId,
                    Is.EqualTo(ClientUiRouteId.Login));
                Assert.That(fixture.Experience.ViewState.Phase, Is.EqualTo(ClientPersonalWorldPhase.Login));
                Assert.That(fixture.Experience.ViewState.Login.Visible, Is.True);
            }
            finally
            {
                await fixture.StopAsync();
            }
        }

        /// <summary>验证 pending 认证保持 single-flight，caller cancel 后下一次动作不被永久拒绝。</summary>
        [Test]
        public async Task CallerCancellationReleasesAuthenticationSingleFlight()
        {
            var fixture = await ExperienceFixture.CreateAsync();
            try
            {
                fixture.Api.BlockVersion = true;
                using (var cancellation = new CancellationTokenSource())
                {
                    var first = fixture.Experience.LoginAsync(
                        "player",
                        "temporary-password",
                        cancellation.Token);
                    await fixture.Api.WaitForVersionCallAsync();

                    var duplicate = await fixture.Experience.LoginAsync(
                        "player",
                        "other-password",
                        CancellationToken.None);
                    Assert.That(duplicate.Failure, Is.EqualTo(ClientPersonalWorldFailure.Permission));
                    Assert.That(fixture.Experience.ViewState.Phase, Is.EqualTo(ClientPersonalWorldPhase.Authenticating));

                    cancellation.Cancel();
                    var cancelled = await first;
                    Assert.That(cancelled.Failure, Is.EqualTo(ClientPersonalWorldFailure.CallerCancelled));
                    Assert.That(fixture.Experience.ViewState.ActiveIntent, Is.EqualTo(ClientPersonalWorldIntent.None));
                }

                fixture.Api.BlockVersion = false;
                var retry = await fixture.Experience.LoginAsync(
                    "player",
                    "temporary-password",
                    CancellationToken.None);
                Assert.That(retry.Failure, Is.EqualTo(ClientPersonalWorldFailure.Transport));
                Assert.That(fixture.Api.TotalCalls, Is.EqualTo(2));
            }
            finally
            {
                await fixture.StopAsync();
            }
        }

        /// <summary>验证 register/login 都严格位于成功 bootstrap 之后，认证失败保持可重试且不提交 Session。</summary>
        [Test]
        public async Task AuthenticationFailureKeepsLoginVisibleAndAllowsNextAuthenticationKind()
        {
            var fixture = await ExperienceFixture.CreateAsync();
            try
            {
                fixture.Api.BootstrapSucceeds = true;

                var register = await fixture.Experience.RegisterAsync(
                    "player",
                    "temporary-password",
                    "Fixture",
                    CancellationToken.None);
                var login = await fixture.Experience.LoginAsync(
                    "player",
                    "temporary-password",
                    CancellationToken.None);

                Assert.That(register.Failure, Is.EqualTo(ClientPersonalWorldFailure.Transport));
                Assert.That(login.Failure, Is.EqualTo(ClientPersonalWorldFailure.Transport));
                Assert.That(fixture.Api.RegisterCalls, Is.EqualTo(1));
                Assert.That(fixture.Api.LoginCalls, Is.EqualTo(1));
                Assert.That(fixture.Api.ConfigurationCalls, Is.EqualTo(2));
                Assert.That(fixture.Session.TryGetCurrent(out _), Is.False);
                Assert.That(fixture.Experience.ViewState.Login.Visible, Is.True);
                Assert.That(fixture.Experience.ViewState.ActiveIntent, Is.EqualTo(ClientPersonalWorldIntent.None));
            }
            finally
            {
                await fixture.StopAsync();
            }
        }

        /// <summary>验证第二个production profile writer得到明确提示而不是未分类内部错误。</summary>
        [Test]
        public async Task ProfileInUseRemainsStableThroughLoginPresentation()
        {
            var secureStore = new FakeClientSecureSessionStore
            {
                ReplaceOutcome = ClientSecureSessionStoreOutcome.ProfileInUse,
            };
            var fixture = await ExperienceFixture.CreateAsync(
                secureSessionStore: secureStore);
            try
            {
                fixture.Api.BootstrapSucceeds = true;
                fixture.Api.AuthenticationSucceeds = true;

                var result = await fixture.Experience.LoginAsync(
                    "player",
                    "temporary-password",
                    CancellationToken.None);

                Assert.That(result.Failure, Is.EqualTo(ClientPersonalWorldFailure.ProfileInUse));
                Assert.That(
                    fixture.Experience.ViewState.Login.Failure,
                    Is.EqualTo(ClientPersonalWorldFailure.ProfileInUse));
                Assert.That(fixture.Session.TryGetCurrent(out _), Is.False);
            }
            finally
            {
                await fixture.StopAsync();
            }
        }

        /// <summary>验证成功认证先提交唯一 Session，再以 control readiness 阻止离线进入 own-world。</summary>
        [Test]
        public async Task SuccessfulLoginCommitsSessionButControlFailureBlocksOwnWorld()
        {
            var fixture = await ExperienceFixture.CreateAsync();
            try
            {
                fixture.Api.BootstrapSucceeds = true;
                fixture.Api.AuthenticationSucceeds = true;

                var result = await fixture.Experience.LoginAsync(
                    "player",
                    "temporary-password",
                    CancellationToken.None);

                Assert.That(result.Failure, Is.EqualTo(ClientPersonalWorldFailure.Transport));
                Assert.That(fixture.Session.TryGetCurrent(out var session), Is.True);
                Assert.That(session.Account.AccountID, Is.EqualTo("account-fixture"));
                Assert.That(fixture.Api.LoginCalls, Is.EqualTo(1));
                Assert.That(fixture.Api.ConnectionTicketCalls, Is.EqualTo(1));
                Assert.That(fixture.Api.WorldBootstrapCalls, Is.Zero);
                Assert.That(fixture.Experience.ViewState.Login.Visible, Is.False);
            }
            finally
            {
                await fixture.StopAsync();
            }
        }

        /// <summary>
        /// 验证 world flow 提交后由状态切换触发的 route cancellation 不会取消同一接受/进入流程。
        /// </summary>
        [Test]
        public async Task WorldFlowCommitWinsRouteCancellationRaisedByTransition()
        {
            var fixture = await ExperienceFixture.CreateAsync();
            try
            {
                fixture.Api.BootstrapSucceeds = true;
                fixture.Api.AuthenticationSucceeds = true;
                await fixture.Experience.LoginAsync(
                    "player",
                    "temporary-password",
                    CancellationToken.None);
                Assert.That(fixture.Session.TryGetCurrent(out _), Is.True);

                using (var routeCancellation = new CancellationTokenSource())
                {
                    fixture.Admission.Changed += snapshot =>
                    {
                        if (snapshot.State == ClientWorldFlowState.ResolvingOwnWorld)
                        {
                            routeCancellation.Cancel();
                        }
                    };

                    var result = await fixture.Experience.RetryEnterOwnWorldAsync(
                        routeCancellation.Token);

                    Assert.That(routeCancellation.IsCancellationRequested, Is.True);
                    Assert.That(result.Failure, Is.EqualTo(ClientPersonalWorldFailure.Transport));
                    Assert.That(fixture.Api.WorldBootstrapCalls, Is.EqualTo(1));
                }
            }
            finally
            {
                await fixture.StopAsync();
            }
        }

        /// <summary>验证 Shell 的玩家标识来自 primary world owner，而不是认证账号标识。</summary>
        [Test]
        public async Task ShellUsesPrimaryWorldOwnerAsPlayerID()
        {
            var fixture = await ExperienceFixture.CreateAsync();
            try
            {
                fixture.Api.BootstrapSucceeds = true;
                fixture.Api.AuthenticationSucceeds = true;
                await fixture.Experience.LoginAsync(
                    "player",
                    "temporary-password",
                    CancellationToken.None);

                var applied = fixture.World.ApplyBootstrap(
                    new ClientWorldBootstrap(
                        new ClientPersonalWorldSummary(
                            "world-fixture",
                            "player-fixture",
                            ClientPersonalWorldLifecycle.Active,
                            1,
                            1_000),
                        new ClientWorldAssignment(
                            "world-fixture",
                            "instance-fixture",
                            new ClientEndpoint(ClientEndpointChannel.TlsTcp, "127.0.0.1", 8444),
                            1,
                            20_000)));

                Assert.That(applied, Is.EqualTo(ClientProjectionApplyResult.Applied));
                Assert.That(fixture.Experience.ViewState.Shell.PlayerID, Is.EqualTo("player-fixture"));
                Assert.That(fixture.Experience.ViewState.Shell.PlayerID, Is.Not.EqualTo("account-fixture"));
            }
            finally
            {
                await fixture.StopAsync();
            }
        }

        /// <summary>验证认证后的 control 终态故障收敛为低敏 ConnectionLost，而不是未观察异常。</summary>
        [Test]
        public async Task ControlRunFaultEntersConnectionLostWithoutClearingCurrentSession()
        {
            var fixture = await ExperienceFixture.CreateAsync();
            try
            {
                fixture.Api.BootstrapSucceeds = true;
                fixture.Api.AuthenticationSucceeds = true;
                await fixture.Experience.LoginAsync(
                    "player",
                    "temporary-password",
                    CancellationToken.None);

                await WaitForPhaseAsync(
                    fixture.Experience,
                    ClientPersonalWorldPhase.ConnectionLost);

                Assert.That(fixture.Session.TryGetCurrent(out _), Is.True);
                Assert.That(
                    fixture.Experience.ViewState.Shell.Failure,
                    Is.EqualTo(ClientPersonalWorldFailure.Transport));
            }
            finally
            {
                await fixture.StopAsync();
            }
        }

        /// <summary>验证caller取消只结束页面等待，不会取消已转交App Scope的恢复owner。</summary>
        [Test]
        public async Task ConnectionRecoveryCallerCancellationDoesNotCancelAppScopeOwner()
        {
            var socketFactory = new SequencedWebSocketFactory();
            var fixture = await ExperienceFixture.CreateAsync(socketFactory);
            try
            {
                fixture.Api.BootstrapSucceeds = true;
                fixture.Api.AuthenticationSucceeds = true;
                await fixture.Experience.LoginAsync(
                    "player",
                    "temporary-password",
                    CancellationToken.None);
                await WaitForPhaseAsync(
                    fixture.Experience,
                    ClientPersonalWorldPhase.ConnectionLost);

                using (var recoveryCancellation = new CancellationTokenSource())
                {
                    var recovery = fixture.Experience.RetryConnectionAsync(recoveryCancellation.Token);
                    await socketFactory.WaitForRecoveryConnectAsync();

                    var duplicate = await fixture.Experience.RetryConnectionAsync(CancellationToken.None);
                    Assert.That(duplicate.Failure, Is.EqualTo(ClientPersonalWorldFailure.Permission));
                    Assert.That(
                        fixture.Experience.ViewState.ActiveIntent,
                        Is.EqualTo(ClientPersonalWorldIntent.Reconnect));

                    recoveryCancellation.Cancel();
                    var result = await recovery;

                    Assert.That(result.Failure, Is.EqualTo(ClientPersonalWorldFailure.CallerCancelled));
                    Assert.That(
                        fixture.Experience.ViewState.ActiveIntent,
                        Is.EqualTo(ClientPersonalWorldIntent.None));
                    Assert.That(
                        fixture.Experience.ViewState.Phase,
                        Is.EqualTo(ClientPersonalWorldPhase.RecoveringControl));
                    Assert.That(
                        socketFactory.RecoveryCancellationObserved,
                        Is.False,
                        "页面caller取消不得终止App Scope持有的control恢复。");
                }
            }
            finally
            {
                await fixture.StopAsync();
            }
        }

        /// <summary>验证 Session owner 清除当前 lineage 后，无需按钮即可退役 target 并返回 Login。</summary>
        [Test]
        public async Task SessionInvalidationAutomaticallyReturnsToLoginAndRejectsOldPresentation()
        {
            var fixture = await ExperienceFixture.CreateAsync();
            try
            {
                fixture.Api.BootstrapSucceeds = true;
                fixture.Api.AuthenticationSucceeds = true;
                await fixture.Experience.LoginAsync(
                    "player",
                    "temporary-password",
                    CancellationToken.None);
                await WaitForPhaseAsync(
                    fixture.Experience,
                    ClientPersonalWorldPhase.ConnectionLost);
                var oldGeneration = fixture.Experience.ViewState.PresentationGeneration;
                var unloadCalls = fixture.SceneTransition.UnloadCalls;

                await fixture.Session.ForgetAsync(CancellationToken.None);
                var drain = fixture.Dispatcher.Drain(maximumCallbacks: 16);
                Assert.That(drain.Errors, Is.Empty);
                Assert.That(
                    drain.ExecutedCount,
                    Is.LessThan(16),
                    "Session失效收敛不得通过world-flow通知重新填满同一dispatcher批次。");
                await WaitForPhaseAsync(
                    fixture.Experience,
                    ClientPersonalWorldPhase.Login);
                await Task.Yield();
                var settledDrain = fixture.Dispatcher.Drain(maximumCallbacks: 16);

                Assert.That(fixture.Session.TryGetCurrent(out _), Is.False);
                Assert.That(
                    fixture.Experience.ViewState.PresentationGeneration,
                    Is.GreaterThan(oldGeneration));
                Assert.That(fixture.Experience.ViewState.Login.Visible, Is.True);
                Assert.That(
                    fixture.Admission.Snapshot.State,
                    Is.EqualTo(ClientWorldFlowState.Inactive));
                Assert.That(fixture.World.Snapshot.CurrentWorld, Is.Null);
                Assert.That(
                    fixture.Router.CurrentSnapshot.Items[0].Definition.RouteId,
                    Is.EqualTo(ClientUiRouteId.Login));
                Assert.That(settledDrain.Errors, Is.Empty);
                Assert.That(
                    settledDrain.ExecutedCount,
                    Is.Zero,
                    "Login提交后不得遗留第二笔Session失效表现事务。");
                Assert.That(
                    fixture.SceneTransition.UnloadCalls,
                    Is.EqualTo(unloadCalls + 1),
                    "同一Session失效generation只能卸载一次内容Scene。");
            }
            finally
            {
                await fixture.StopAsync();
            }
        }

        /// <summary>验证成功 logout 清除唯一 Session、返回 Login 且不会遗留 active intent。</summary>
        [Test]
        public async Task SuccessfulLogoutReturnsToLoginAndReleasesIntent()
        {
            var fixture = await ExperienceFixture.CreateAsync();
            try
            {
                fixture.Api.BootstrapSucceeds = true;
                fixture.Api.AuthenticationSucceeds = true;
                fixture.Api.LogoutSucceeds = true;
                await fixture.Experience.LoginAsync(
                    "player",
                    "temporary-password",
                    CancellationToken.None);
                await WaitForPhaseAsync(
                    fixture.Experience,
                    ClientPersonalWorldPhase.ConnectionLost);

                var result = await fixture.Experience.LogoutAsync(CancellationToken.None);

                Assert.That(result.Succeeded, Is.True);
                Assert.That(fixture.Session.TryGetCurrent(out _), Is.False);
                Assert.That(fixture.Experience.ViewState.Phase, Is.EqualTo(ClientPersonalWorldPhase.Login));
                Assert.That(fixture.Experience.ViewState.ActiveIntent, Is.EqualTo(ClientPersonalWorldIntent.None));
                Assert.That(fixture.Experience.ViewState.Login.Visible, Is.True);
            }
            finally
            {
                await fixture.StopAsync();
            }
        }

        /// <summary>验证单个 subscriber 抛错不会阻断其他 subscriber 或低敏失败提交。</summary>
        [Test]
        public async Task SubscriberFailureIsIsolatedFromStateCommit()
        {
            var fixture = await ExperienceFixture.CreateAsync();
            try
            {
                var observed = 0;
                fixture.Experience.ViewStateChanged += _ => throw new InvalidOperationException("subscriber failure");
                fixture.Experience.ViewStateChanged += _ => observed++;

                var result = await fixture.Experience.LoginAsync(
                    "player",
                    "temporary-password",
                    CancellationToken.None);

                Assert.That(result.Failure, Is.EqualTo(ClientPersonalWorldFailure.Transport));
                Assert.That(observed, Is.GreaterThanOrEqualTo(2));
                Assert.That(fixture.Experience.ViewState.Login.Failure, Is.EqualTo(ClientPersonalWorldFailure.Transport));
            }
            finally
            {
                await fixture.StopAsync();
            }
        }

        /// <summary>验证Experience只撤销自身状态，由Router唯一清理route且迟到动作不能复活Login。</summary>
        [Test]
        public async Task StopRevokesExperienceAndLeavesRouteTeardownToRouter()
        {
            var fixture = await ExperienceFixture.CreateAsync();
            await fixture.Experience.StopAsync(CancellationToken.None);
            fixture.ExperienceStopped = true;

            var late = await fixture.Experience.LoginAsync(
                "player",
                "temporary-password",
                CancellationToken.None);

            Assert.That(late.Failure, Is.EqualTo(ClientPersonalWorldFailure.Stopped));
            Assert.That(fixture.Experience.ViewState.Phase, Is.EqualTo(ClientPersonalWorldPhase.Stopped));
            Assert.That(fixture.Router.CurrentSnapshot.Items, Has.Count.EqualTo(1));
            Assert.That(
                fixture.Router.CurrentSnapshot.Items[0].Definition.RouteId,
                Is.EqualTo(ClientUiRouteId.Login));

            await fixture.Router.StopAsync(CancellationToken.None);
            Assert.That(fixture.Router.CurrentSnapshot.Items, Is.Empty);
            await fixture.StopAsync();
        }

        /// <summary>通过不可变View State事件等待目标产品阶段，不使用sleep或轮询猜测。</summary>
        /// <param name="experience">被测唯一产品Experience。</param>
        /// <param name="expected">期望提交的稳定产品阶段。</param>
        /// <returns>目标阶段已经由Experience提交时完成的任务。</returns>
        private static async Task WaitForPhaseAsync(
            ClientPersonalWorldExperience experience,
            ClientPersonalWorldPhase expected)
        {
            var completion = new TaskCompletionSource<bool>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            void OnChanged(ClientPersonalWorldViewState state)
            {
                if (state.Phase == expected)
                {
                    completion.TrySetResult(true);
                }
            }

            experience.ViewStateChanged += OnChanged;
            try
            {
                if (experience.ViewState.Phase == expected)
                {
                    return;
                }

                await completion.Task;
            }
            finally
            {
                experience.ViewStateChanged -= OnChanged;
            }
        }

        /// <summary>组合只登记 Login route 且不会建立真实连接的最小 Experience 对象图。</summary>
        private sealed class ExperienceFixture
        {
            /// <summary>创建 fixture。</summary>
            private ExperienceFixture(
                RecordingHttpApi api,
                MainThreadDispatcher dispatcher,
                ClientConfigurationStore configuration,
                SessionCoordinator session,
                ClientGameplayChannel gameplay,
                ClientGameplayChannelPortAdapter gameplayPort,
                ClientControlChannel control,
                ClientControlChannelPortAdapter controlPort,
                PersonalWorldService world,
                VisitSessionService visit,
                WorldAdmissionCoordinator admission,
                ClientConnectionRecoveryCoordinator recovery,
                FixtureSceneTransition sceneTransition,
                ClientUiRouter router,
                ClientPersonalWorldExperience experience)
            {
                Api = api;
                Dispatcher = dispatcher;
                Configuration = configuration;
                Session = session;
                Gameplay = gameplay;
                GameplayPort = gameplayPort;
                Control = control;
                ControlPort = controlPort;
                World = world;
                Visit = visit;
                Admission = admission;
                Recovery = recovery;
                SceneTransition = sceneTransition;
                Router = router;
                Experience = experience;
            }

            /// <summary>获取确定性 HTTP fake。</summary>
            internal RecordingHttpApi Api { get; }

            /// <summary>获取测试主线程 dispatcher。</summary>
            internal MainThreadDispatcher Dispatcher { get; }

            /// <summary>获取配置 owner。</summary>
            internal ClientConfigurationStore Configuration { get; }

            /// <summary>获取唯一 Session owner。</summary>
            internal SessionCoordinator Session { get; }

            /// <summary>获取 gameplay channel owner。</summary>
            internal ClientGameplayChannel Gameplay { get; }

            /// <summary>获取 Application 使用的 typed gameplay port。</summary>
            internal ClientGameplayChannelPortAdapter GameplayPort { get; }

            /// <summary>获取 control channel owner。</summary>
            internal ClientControlChannel Control { get; }

            /// <summary>获取 Application 使用的 typed control port。</summary>
            internal ClientControlChannelPortAdapter ControlPort { get; }

            /// <summary>获取 PersonalWorld projection owner。</summary>
            internal PersonalWorldService World { get; }

            /// <summary>获取 VisitSession projection owner。</summary>
            internal VisitSessionService Visit { get; }

            /// <summary>获取 world flow owner。</summary>
            internal WorldAdmissionCoordinator Admission { get; }

            /// <summary>获取与产品组合一致的唯一连接恢复 owner。</summary>
            internal ClientConnectionRecoveryCoordinator Recovery { get; }

            /// <summary>获取测试 Scene transition。</summary>
            internal FixtureSceneTransition SceneTransition { get; }

            /// <summary>获取真实纯 C# Router。</summary>
            internal ClientUiRouter Router { get; }

            /// <summary>获取被测 Experience。</summary>
            internal ClientPersonalWorldExperience Experience { get; }

            /// <summary>标识测试是否已经单独停止 Experience。</summary>
            internal bool ExperienceStopped { get; set; }

            /// <summary>创建与产品同构但不建立真实连接的最小对象图。</summary>
            /// <param name="webSocketFactory">可选的确定性 control socket factory。</param>
            /// <param name="secureSessionStore">可选的确定性secure store fake。</param>
            /// <returns>Login route 已提交的 fixture。</returns>
            internal static async Task<ExperienceFixture> CreateAsync(
                IClientWebSocketFactory webSocketFactory = null,
                FakeClientSecureSessionStore secureSessionStore = null)
            {
                var environment = ClientEnvironment.Create(
                    ClientEnvironmentKind.Test,
                    "http://127.0.0.1:8080/",
                    "0.1.0",
                    1);
                var api = new RecordingHttpApi();
                var configuration = new ClientConfigurationStore();
                var clock = new FixedClock();
                var session = new SessionCoordinator(
                    configuration,
                    api,
                    clock,
                    secureSessionStore ?? new FakeClientSecureSessionStore(),
                    FakeClientSecureSessionStore.EnvironmentBinding);
                var dispatcher = new MainThreadDispatcher(Environment.CurrentManagedThreadId, 16);
                var gameplay = new ClientGameplayChannel(
                    configuration,
                    session,
                    new RejectingGameplayConnectionFactory(),
                    new ClientGameplayCodec(),
                    dispatcher,
                    new SystemClientDelay());
                var gameplayPort = new ClientGameplayChannelPortAdapter(
                    gameplay,
                    new ClientGameplayProtocolAdapter());
                var control = new ClientControlChannel(
                    environment,
                    configuration,
                    session,
                    webSocketFactory ?? new RejectingWebSocketFactory(),
                    new ClientControlCodec(new ClientControlCatalog()),
                    new ClientControlProtocolAdapter(),
                    dispatcher,
                    new ImmediateDelay(),
                    Array.Empty<TimeSpan>(),
                    gameplayPort.InvalidateSession);
                var controlPort = new ClientControlChannelPortAdapter(control);
                var world = new PersonalWorldService(controlPort, gameplayPort);
                var visit = new VisitSessionService(controlPort, gameplayPort, clock);
                var admission = new WorldAdmissionCoordinator(
                    session,
                    clock,
                    gameplayPort,
                    world,
                    visit);
                var recovery = new ClientConnectionRecoveryCoordinator(
                    session,
                    controlPort,
                    gameplayPort,
                    admission,
                    ClientPersonalWorldExperience.DefaultConnectionRecoveryTimeout);
                var host = new FixtureViewHost(ClientUiRouteId.Login);
                var registry = new ClientUiRegistry(
                    new[]
                    {
                        new ClientUiRouteDefinition(
                            ClientUiRouteId.Login,
                            ClientUiFrameworkOwner.UiToolkit,
                            ClientUiLayer.Screen,
                            ClientUiInputMode.Text,
                            ClientUiLifecycle.Cached),
                    },
                    new IClientUiViewHost[] { host });
                var router = new ClientUiRouter(
                    registry,
                    new FixtureInputCoordinator(),
                    maximumQueuedTransitions: 4,
                    cleanupTimeout: TimeSpan.FromSeconds(1));
                var sceneTransition = new FixtureSceneTransition();
                await dispatcher.InitializeAsync(CancellationToken.None);
                await configuration.InitializeAsync(CancellationToken.None);
                await session.InitializeAsync(CancellationToken.None);
                await gameplayPort.InitializeAsync(CancellationToken.None);
                await controlPort.InitializeAsync(CancellationToken.None);
                await world.InitializeAsync(CancellationToken.None);
                await visit.InitializeAsync(CancellationToken.None);
                await admission.InitializeAsync(CancellationToken.None);
                await recovery.InitializeAsync(CancellationToken.None);
                await sceneTransition.InitializeAsync(CancellationToken.None);
                await router.InitializeAsync(CancellationToken.None);
                var experience = new ClientPersonalWorldExperience(
                    new ClientBootstrapService(environment, api, configuration),
                    session,
                    null,
                    recovery,
                    dispatcher,
                    controlPort,
                    world,
                    visit,
                    admission,
                    router,
                    sceneTransition,
                    ClientPersonalWorldExperience.DefaultConnectionRecoveryTimeout);
                await experience.InitializeAsync(CancellationToken.None);
                return new ExperienceFixture(
                    api,
                    dispatcher,
                    configuration,
                    session,
                    gameplay,
                    gameplayPort,
                    control,
                    controlPort,
                    world,
                    visit,
                    admission,
                    recovery,
                    sceneTransition,
                    router,
                    experience);
            }

            /// <summary>按 Experience、Router 顺序停止已初始化对象。</summary>
            /// <returns>测试对象图已清理时完成。</returns>
            internal async Task StopAsync()
            {
                if (!ExperienceStopped)
                {
                    await Experience.StopAsync(CancellationToken.None);
                    ExperienceStopped = true;
                }

                await Router.StopAsync(CancellationToken.None);
                await SceneTransition.StopAsync(CancellationToken.None);
                await Recovery.StopAsync(CancellationToken.None);
                await Admission.StopAsync(CancellationToken.None);
                await Visit.StopAsync(CancellationToken.None);
                await World.StopAsync(CancellationToken.None);
                await ControlPort.StopAsync(CancellationToken.None);
                await GameplayPort.StopAsync(CancellationToken.None);
                await Session.StopAsync(CancellationToken.None);
                await Configuration.StopAsync(CancellationToken.None);
                await Dispatcher.StopAsync(CancellationToken.None);
            }
        }

        /// <summary>提供可取消 version 阻塞与稳定 transport failure 的 HTTP fake。</summary>
        private sealed class RecordingHttpApi : IClientBootstrapGateway, IClientSessionGateway
        {
            /// <summary>通知测试已有 version 调用进入 fake。</summary>
            private TaskCompletionSource<bool> _versionCalled = NewSignal();

            /// <summary>获取或设置 version 是否等待 caller cancellation。</summary>
            internal bool BlockVersion { get; set; }

            /// <summary>获取或设置 version/config 是否返回兼容成功投影。</summary>
            internal bool BootstrapSucceeds { get; set; }

            /// <summary>获取或设置 register/login 是否提交完整认证投影。</summary>
            internal bool AuthenticationSucceeds { get; set; }

            /// <summary>获取或设置 logout 是否返回权威成功。</summary>
            internal bool LogoutSucceeds { get; set; }

            /// <summary>获取全部 HTTP 调用次数。</summary>
            internal int TotalCalls { get; private set; }

            /// <summary>获取 config 调用次数。</summary>
            internal int ConfigurationCalls { get; private set; }

            /// <summary>获取 register 调用次数。</summary>
            internal int RegisterCalls { get; private set; }

            /// <summary>获取 login 调用次数。</summary>
            internal int LoginCalls { get; private set; }

            /// <summary>获取 control ticket 调用次数。</summary>
            internal int ConnectionTicketCalls { get; private set; }

            /// <summary>获取 own-world bootstrap 调用次数。</summary>
            internal int WorldBootstrapCalls { get; private set; }

            /// <summary>等待至少一次 version 调用进入 fake。</summary>
            /// <returns>调用已进入时完成。</returns>
            internal Task WaitForVersionCallAsync()
            {
                return _versionCalled.Task;
            }

            /// <summary>按配置阻塞或返回稳定 transport failure。</summary>
            public async Task<ClientGatewayResult<ClientVersionInfo>> GetVersionAsync(
                CancellationToken cancellationToken)
            {
                TotalCalls++;
                _versionCalled.TrySetResult(true);
                if (BlockVersion)
                {
                    await Task.Delay(Timeout.Infinite, cancellationToken);
                }

                _versionCalled = NewSignal();
                return BootstrapSucceeds
                    ? ClientGatewayResult<ClientVersionInfo>.Success(
                        new ClientVersionInfo(1, "0.1.0", "0.1.0"))
                    : Failure<ClientVersionInfo>("getVersion");
            }

            /// <summary>按脚本返回兼容配置或稳定 transport failure。</summary>
            public Task<ClientGatewayResult<ClientBootstrapConfiguration>> GetBootstrapConfigurationAsync(CancellationToken cancellationToken)
            {
                TotalCalls++;
                ConfigurationCalls++;
                var result = BootstrapSucceeds
                    ? ClientGatewayResult<ClientBootstrapConfiguration>.Success(
                        new ClientBootstrapConfiguration(
                            new[]
                            {
                                new ClientEndpoint(ClientEndpointChannel.Wss, "127.0.0.1", 8443),
                                new ClientEndpoint(ClientEndpointChannel.TlsTcp, "127.0.0.1", 8444),
                            },
                            new ClientPublicLimits(4096, 65536)))
                    : Failure<ClientBootstrapConfiguration>("getBootstrapConfiguration");
                return Task.FromResult(result);
            }

            /// <summary>按脚本返回 register 认证投影或稳定 transport failure。</summary>
            public Task<ClientGatewayResult<ClientAuthentication>> RegisterAsync(ClientRegisterGatewayRequest request, CancellationToken cancellationToken)
            {
                TotalCalls++;
                RegisterCalls++;
                return Task.FromResult(AuthenticationResult("registerAccount"));
            }

            /// <summary>按脚本返回 login 认证投影或稳定 transport failure。</summary>
            public Task<ClientGatewayResult<ClientAuthentication>> LoginAsync(ClientLoginGatewayRequest request, CancellationToken cancellationToken)
            {
                TotalCalls++;
                LoginCalls++;
                return Task.FromResult(AuthenticationResult("loginAccount"));
            }

            /// <summary>未使用 operation 返回稳定失败。</summary>
            public Task<ClientGatewayResult<ClientTokenPair>> RefreshAsync(ClientCredentialGatewayRequest request, CancellationToken cancellationToken) =>
                Unused<ClientTokenPair>("refreshSession");

            /// <summary>按脚本返回 logout 成功或稳定 transport failure。</summary>
            public Task<ClientGatewayResult<ClientGatewayEmpty>> LogoutAsync(ClientCredentialGatewayRequest request, CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                TotalCalls++;
                return Task.FromResult(
                    LogoutSucceeds
                        ? ClientGatewayResult<ClientGatewayEmpty>.Success(new ClientGatewayEmpty())
                        : Failure<ClientGatewayEmpty>("logoutSession"));
            }

            /// <summary>认证成功后提供一次 control ticket，使 run 能进入 socket failure 路径。</summary>
            public Task<ClientGatewayResult<ClientConnectionTicket>> IssueConnectionTicketAsync(ClientConnectionTicketGatewayRequest request, CancellationToken cancellationToken)
            {
                TotalCalls++;
                ConnectionTicketCalls++;
                return Task.FromResult(ClientGatewayResult<ClientConnectionTicket>.Success(
                    new ClientConnectionTicket(
                        "00000000000000000000000000000001",
                        new ClientEndpoint(ClientEndpointChannel.Wss, "127.0.0.1", 8443),
                        new[] { ClientConnectionScope.Control },
                        5_000)));
            }

            /// <summary>记录 own-world 解析并返回稳定 transport failure。</summary>
            public Task<ClientGatewayResult<ClientWorldBootstrap>> GetWorldBootstrapAsync(ClientCredentialGatewayRequest request, CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                TotalCalls++;
                WorldBootstrapCalls++;
                return Task.FromResult(Failure<ClientWorldBootstrap>("getWorldBootstrap"));
            }

            /// <summary>未使用 operation 返回稳定失败。</summary>
            public Task<ClientGatewayResult<ClientVisitReservation>> AcceptVisitInviteAsync(ClientAcceptVisitInviteGatewayRequest request, CancellationToken cancellationToken) =>
                Unused<ClientVisitReservation>("acceptVisitInvite");

            /// <summary>未使用 operation 返回稳定失败。</summary>
            public Task<ClientGatewayResult<ClientWorldAdmission>> IssueWorldAdmissionAsync(ClientWorldAdmissionGatewayRequest request, CancellationToken cancellationToken) =>
                Unused<ClientWorldAdmission>("issueWorldAdmission");

            /// <summary>记录意外调用并返回稳定失败。</summary>
            private Task<ClientGatewayResult<T>> Unused<T>(string operationID)
            {
                TotalCalls++;
                return Task.FromResult(Failure<T>(operationID));
            }

            /// <summary>创建完整认证投影或当前脚本指定的稳定失败。</summary>
            /// <param name="operationID">失败时登记的 operation identity。</param>
            /// <returns>认证 HTTP 结果。</returns>
            private ClientGatewayResult<ClientAuthentication> AuthenticationResult(string operationID)
            {
                return AuthenticationSucceeds
                    ? ClientGatewayResult<ClientAuthentication>.Success(
                        new ClientAuthentication(
                            new ClientAccountSummary("account-fixture", "Fixture", 1),
                            new ClientSessionSummary("session-fixture", 1, 9_000),
                            new ClientTokenPair("access-fixture", "refresh-fixture", 7_000, 8_000),
                            Array.Empty<ClientEndpoint>()))
                    : Failure<ClientAuthentication>(operationID);
            }

            /// <summary>创建 transport failure。</summary>
            private static ClientGatewayResult<T> Failure<T>(string operationID)
            {
                return ClientGatewayResult<T>.Failed(new ClientGatewayFailure(
                    ClientGatewayFailureKind.Transport,
                    operationID));
            }

            /// <summary>创建异步 continuations 隔离的测试信号。</summary>
            private static TaskCompletionSource<bool> NewSignal()
            {
                return new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
            }
        }

        /// <summary>实现无 Unity 对象的单 route Host。</summary>
        private sealed class FixtureViewHost : IClientUiViewHost
        {
            /// <summary>创建指定 route Host。</summary>
            internal FixtureViewHost(ClientUiRouteId routeId)
            {
                RouteId = routeId;
            }

            /// <summary>获取 route identity。</summary>
            public ClientUiRouteId RouteId { get; }

            /// <summary>获取固定 UI Toolkit owner。</summary>
            public ClientUiFrameworkOwner FrameworkOwner => ClientUiFrameworkOwner.UiToolkit;

            /// <summary>获取 Host generation。</summary>
            public long HostGeneration { get; private set; }

            /// <summary>建立 Host generation。</summary>
            public Task InitializeAsync(CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                HostGeneration++;
                return Task.CompletedTask;
            }

            /// <summary>接受 route binding。</summary>
            public Task BindAsync(ClientUiRouteBinding binding, CancellationToken cancellationToken) => Task.CompletedTask;

            /// <summary>接受 layer。</summary>
            public Task ShowAsync(ClientUiLayer layer, CancellationToken cancellationToken) => Task.CompletedTask;

            /// <summary>接受交互状态。</summary>
            public Task SetInteractiveAsync(bool interactive, CancellationToken cancellationToken) => Task.CompletedTask;

            /// <summary>返回空 focus token。</summary>
            public ClientUiFocusToken CaptureFocus() => new ClientUiFocusToken(RouteId, HostGeneration, null);

            /// <summary>Fixture 不提供默认 focus。</summary>
            public Task<bool> FocusDefaultAsync(CancellationToken cancellationToken) => Task.FromResult(false);

            /// <summary>Fixture 不恢复 focus。</summary>
            public Task<bool> RestoreFocusAsync(ClientUiFocusToken token, CancellationToken cancellationToken) => Task.FromResult(false);

            /// <summary>幂等隐藏。</summary>
            public Task HideAsync(CancellationToken cancellationToken) => Task.CompletedTask;

            /// <summary>幂等解绑。</summary>
            public Task UnbindAsync(CancellationToken cancellationToken) => Task.CompletedTask;

            /// <summary>幂等释放。</summary>
            public Task DisposeAsync(CancellationToken cancellationToken) => Task.CompletedTask;
        }

        /// <summary>记录纯 C# Router 所需的 input state。</summary>
        private sealed class FixtureInputCoordinator : IClientUiInputCoordinator
        {
            /// <summary>获取最近状态。</summary>
            public ClientUiInputState CurrentState { get; private set; } = ClientUiInputState.Gameplay;

            /// <summary>初始化无副作用。</summary>
            public Task InitializeAsync(CancellationToken cancellationToken) => Task.CompletedTask;

            /// <summary>提交当前输入状态。</summary>
            public Task ApplyAsync(ClientUiInputState state, CancellationToken cancellationToken)
            {
                CurrentState = state;
                return Task.CompletedTask;
            }

            /// <summary>停止无副作用。</summary>
            public Task StopAsync(CancellationToken cancellationToken) => Task.CompletedTask;
        }

        /// <summary>提供不加载 Unity Scene 的基线 transition。</summary>
        private sealed class FixtureSceneTransition : IClientWorldSceneTransition
        {
            /// <summary>获取空场景快照。</summary>
            public ClientWorldSceneSnapshot Snapshot { get; } = ClientWorldSceneSnapshot.Empty;

            /// <summary>获取测试观察到的内容Scene卸载次数。</summary>
            internal int UnloadCalls { get; private set; }

            /// <summary>初始化无副作用。</summary>
            public Task InitializeAsync(CancellationToken cancellationToken) => Task.CompletedTask;

            /// <summary>测试不允许场景加载。</summary>
            public Task<ClientWorldSceneTransitionCode> LoadAsync(ClientWorldSceneId sceneId, long targetGeneration, ClientPersonalWorldViewState viewState, CancellationToken cancellationToken) =>
                Task.FromResult(ClientWorldSceneTransitionCode.LoadFailed);

            /// <summary>卸载保持成功。</summary>
            public Task<ClientWorldSceneTransitionCode> UnloadAsync(CancellationToken cancellationToken)
            {
                UnloadCalls++;
                return Task.FromResult(ClientWorldSceneTransitionCode.Succeeded);
            }

            /// <summary>没有 current Context，拒绝投影。</summary>
            public bool TryApply(ClientPersonalWorldViewState viewState) => false;

            /// <summary>停止无副作用。</summary>
            public Task StopAsync(CancellationToken cancellationToken) => Task.CompletedTask;
        }

        /// <summary>拒绝所有实际 gameplay 连接。</summary>
        private sealed class RejectingGameplayConnectionFactory : IClientGameplayConnectionFactory
        {
            /// <summary>测试若意外连接则立即失败。</summary>
            public Task<IClientGameplayConnection> ConnectAsync(ClientEndpoint endpoint, CancellationToken cancellationToken) =>
                Task.FromException<IClientGameplayConnection>(new InvalidOperationException("测试禁止 gameplay 连接。"));
        }

        /// <summary>拒绝创建实际 WebSocket。</summary>
        private sealed class RejectingWebSocketFactory : IClientWebSocketFactory
        {
            /// <summary>创建在 Connect 阶段返回稳定 transport failure 的 socket。</summary>
            public IClientWebSocket Create()
            {
                return new RejectingWebSocket();
            }
        }

        /// <summary>模拟平台 adapter 在连接阶段返回已降敏 transport failure。</summary>
        private sealed class RejectingWebSocket : IClientWebSocket
        {
            /// <summary>连接从未建立。</summary>
            public WebSocketState State => WebSocketState.None;

            /// <summary>未连接时没有协商 subprotocol。</summary>
            public string SubProtocol => null;

            /// <summary>返回与真实 adapter 相同的稳定连接失败。</summary>
            public Task ConnectAsync(
                ClientWebSocketConnectRequest request,
                CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                return Task.FromException(new ClientWebSocketTransportException());
            }

            /// <summary>连接失败后不得进入 receive。</summary>
            public Task<ClientWebSocketReadResult> ReceiveAsync(
                ArraySegment<byte> buffer,
                CancellationToken cancellationToken)
            {
                return Task.FromException<ClientWebSocketReadResult>(
                    new InvalidOperationException("连接失败后不应读取 WebSocket。"));
            }

            /// <summary>未连接 socket 无需发送 close。</summary>
            public Task CloseAsync(CancellationToken cancellationToken)
            {
                return Task.CompletedTask;
            }

            /// <summary>测试 socket 不持有平台资源。</summary>
            public void Dispose()
            {
            }
        }

        /// <summary>第一次连接返回稳定失败，第二次连接保持 pending 直到恢复 owner 取消。</summary>
        private sealed class SequencedWebSocketFactory : IClientWebSocketFactory
        {
            /// <summary>记录已经创建的 socket 数量。</summary>
            private int _created;

            /// <summary>保存唯一恢复阶段 socket。</summary>
            private readonly HangingWebSocket _recoverySocket = new HangingWebSocket();

            /// <summary>为首次 control run 返回失败 socket，之后返回可观察取消的 pending socket。</summary>
            public IClientWebSocket Create()
            {
                return Interlocked.Increment(ref _created) == 1
                    ? new RejectingWebSocket()
                    : _recoverySocket;
            }

            /// <summary>等待显式恢复进入平台连接阶段。</summary>
            /// <returns>恢复 socket 已开始连接时完成。</returns>
            internal Task WaitForRecoveryConnectAsync()
            {
                return _recoverySocket.WaitForConnectAsync();
            }

            /// <summary>获取恢复 socket 是否已被 owner 取消。</summary>
            internal bool RecoveryCancellationObserved => _recoverySocket.CancellationObserved;
        }

        /// <summary>模拟一直等待但严格服从 cancellation 的平台 WebSocket。</summary>
        private sealed class HangingWebSocket : IClientWebSocket
        {
            /// <summary>表示 ConnectAsync 已开始。</summary>
            private readonly TaskCompletionSource<bool> _connectStarted =
                new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);

            /// <summary>表示 ConnectAsync 已观察到取消。</summary>
            private readonly TaskCompletionSource<bool> _cancellationObserved =
                new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);

            /// <summary>连接未成功前保持 None。</summary>
            public WebSocketState State => WebSocketState.None;

            /// <summary>连接未成功时没有协商 subprotocol。</summary>
            public string SubProtocol => null;

            /// <summary>获取平台连接是否已观察到 owner cancellation。</summary>
            internal bool CancellationObserved => _cancellationObserved.Task.IsCompleted;

            /// <summary>保持 pending，直到调用方传入的恢复 deadline 取消。</summary>
            public async Task ConnectAsync(
                ClientWebSocketConnectRequest request,
                CancellationToken cancellationToken)
            {
                _connectStarted.TrySetResult(true);
                try
                {
                    await Task.Delay(Timeout.InfiniteTimeSpan, cancellationToken);
                }
                finally
                {
                    if (cancellationToken.IsCancellationRequested)
                    {
                        _cancellationObserved.TrySetResult(true);
                    }
                }
            }

            /// <summary>pending 连接不允许进入 receive。</summary>
            public Task<ClientWebSocketReadResult> ReceiveAsync(
                ArraySegment<byte> buffer,
                CancellationToken cancellationToken)
            {
                return Task.FromException<ClientWebSocketReadResult>(
                    new InvalidOperationException("pending 连接不应读取 WebSocket。"));
            }

            /// <summary>未建立的测试 socket 无需发送 close。</summary>
            public Task CloseAsync(CancellationToken cancellationToken)
            {
                return Task.CompletedTask;
            }

            /// <summary>等待测试 socket 进入连接阶段。</summary>
            /// <returns>ConnectAsync 已开始时完成。</returns>
            internal Task WaitForConnectAsync()
            {
                return _connectStarted.Task;
            }

            /// <summary>测试 socket 不持有平台资源。</summary>
            public void Dispose()
            {
            }
        }

        /// <summary>提供不等待的 control backoff。</summary>
        private sealed class ImmediateDelay : IClientDelay
        {
            /// <summary>直接完成测试等待。</summary>
            public Task DelayAsync(TimeSpan delay, CancellationToken cancellationToken) => Task.CompletedTask;
        }

        /// <summary>提供固定 Unix 毫秒时间。</summary>
        private sealed class FixedClock : IClientClock
        {
            /// <summary>获取固定测试时间。</summary>
            public long UtcNowMilliseconds => 1_000;
        }
    }
}
