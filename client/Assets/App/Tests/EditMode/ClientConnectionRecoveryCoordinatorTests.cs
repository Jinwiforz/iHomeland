using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Application.World;
using IHomeland.Client.Application.Configuration;
using IHomeland.Client.Foundation.Time;
using IHomeland.Client.Application.Contracts;
using IHomeland.Client.Application.Ports;
using IHomeland.Client.Infrastructure.Http;
using NUnit.Framework;

namespace IHomeland.Client.Tests.EditMode
{
    /// <summary>以可控operation与deadline验证唯一连接恢复状态机的竞态边界。</summary>
    public sealed class ClientConnectionRecoveryCoordinatorTests
    {
        /// <summary>验证automatic single-flight与Scene第四重gate。</summary>
        [Test]
        public async Task AutomaticRecoveryIsSingleFlightAndRequiresSceneGate()
        {
            var fixture = await RecoveryFixture.CreateAsync();
            try
            {
                fixture.Operations.WorldResult = PendingResult();
                Assert.That(fixture.Coordinator.BeginAutomaticWorldRecovery(7), Is.True);
                Assert.That(fixture.Coordinator.BeginAutomaticWorldRecovery(7), Is.False);
                Assert.That(fixture.Operations.RecoverCalls, Is.EqualTo(1));

                fixture.Operations.CurrentTargetGeneration = 12;
                fixture.Operations.WorldResult.SetResult(
                    ClientConnectionRecoveryResultKind.Succeeded);
                await WaitUntilAsync(() =>
                    fixture.Coordinator.Snapshot.Phase ==
                    ClientConnectionRecoveryPhase.AwaitingSceneCommit);

                var intent = fixture.Coordinator.Snapshot.IntentGeneration;
                Assert.That(fixture.Coordinator.ConfirmSceneCommit(intent, 11), Is.False);
                Assert.That(fixture.Coordinator.ConfirmSceneCommit(intent, 12), Is.True);
                Assert.That(
                    fixture.Coordinator.Snapshot.Phase,
                    Is.EqualTo(ClientConnectionRecoveryPhase.Idle));
            }
            finally
            {
                await fixture.StopAsync();
            }
        }

        /// <summary>验证同一服务端Session上的access refresh不会阻断冻结世界目标恢复。</summary>
        [Test]
        public async Task SessionRefreshKeepsFrozenTargetRecoverable()
        {
            var fixture = await RecoveryFixture.CreateAsync();
            try
            {
                var sourceGeneration = fixture.Operations.Target.SessionGeneration;
                var refresh = await fixture.Session.RefreshAsync(CancellationToken.None);
                Assert.That(refresh.IsSuccess, Is.True);
                Assert.That(refresh.Value.Generation, Is.GreaterThan(sourceGeneration));
                Assert.That(fixture.Operations.Target.IsBoundTo(refresh.Value), Is.True);
                var replacementSession = new ClientSessionSnapshot(
                    refresh.Value.Account,
                    new ClientSessionSummary("replacement_session", 2, 100_000),
                    refresh.Value.Tokens,
                    refresh.Value.Generation + 1);
                Assert.That(fixture.Operations.Target.IsBoundTo(replacementSession), Is.False);
                Assert.That(
                    fixture.Operations.Target.TryRebindTo(replacementSession, out _),
                    Is.False);

                Assert.That(fixture.Coordinator.BeginAutomaticWorldRecovery(70), Is.True);
                await WaitUntilAsync(() =>
                    fixture.Coordinator.Snapshot.Phase ==
                    ClientConnectionRecoveryPhase.AwaitingSceneCommit);
            }
            finally
            {
                await fixture.StopAsync();
            }
        }

        /// <summary>验证manual intent只在automatic稳定失败后取得所有权。</summary>
        [Test]
        public async Task ManualRecoveryStartsOnlyAfterAutomaticTerminal()
        {
            var fixture = await RecoveryFixture.CreateAsync();
            try
            {
                fixture.Operations.WorldResult = PendingResult();
                Assert.That(fixture.Coordinator.BeginAutomaticWorldRecovery(8), Is.True);
                Assert.That(fixture.Coordinator.BeginManualWorldRecovery(), Is.False);
                var terminal = new TaskCompletionSource<ClientConnectionRecoverySnapshot>(
                    TaskCreationOptions.RunContinuationsAsynchronously);
                fixture.Coordinator.Changed += snapshot =>
                {
                    if (snapshot.Phase == ClientConnectionRecoveryPhase.ConnectionLost)
                    {
                        terminal.TrySetResult(snapshot);
                    }
                };
                fixture.Operations.WorldResult.SetResult(
                    ClientConnectionRecoveryResultKind.Transport);
                await terminal.Task;

                fixture.Operations.WorldResult = PendingResult();
                Assert.That(fixture.Coordinator.BeginManualWorldRecovery(), Is.True);
                Assert.That(fixture.Coordinator.BeginManualWorldRecovery(), Is.False);
                Assert.That(fixture.Coordinator.Snapshot.Manual, Is.True);
                Assert.That(fixture.Operations.RecoverCalls, Is.EqualTo(2));
            }
            finally
            {
                fixture.Operations.WorldResult.TrySetResult(
                    ClientConnectionRecoveryResultKind.Stopped);
                await fixture.StopAsync();
            }
        }

        /// <summary>验证可控总deadline产生唯一terminal而无需真实sleep。</summary>
        [Test]
        public async Task DeadlineProducesStableTerminalResult()
        {
            var deadline = new CancellationTokenSource();
            var fixture = await RecoveryFixture.CreateAsync(_ => deadline);
            try
            {
                var started = new TaskCompletionSource<bool>(
                    TaskCreationOptions.RunContinuationsAsynchronously);
                var terminal = new TaskCompletionSource<ClientConnectionRecoverySnapshot>(
                    TaskCreationOptions.RunContinuationsAsynchronously);
                fixture.Coordinator.Changed += snapshot =>
                {
                    if (snapshot.Phase == ClientConnectionRecoveryPhase.ConnectionLost)
                    {
                        terminal.TrySetResult(snapshot);
                    }
                };
                fixture.Operations.WorldHandler = async (_, token) =>
                {
                    started.TrySetResult(true);
                    await WaitForCancellationAsync(token);
                    token.ThrowIfCancellationRequested();
                    return ClientConnectionRecoveryResultKind.Succeeded;
                };
                Assert.That(fixture.Coordinator.BeginAutomaticWorldRecovery(9), Is.True);
                await started.Task;
                deadline.Cancel();
                var committed = await terminal.Task;
                Assert.That(
                    committed.Result,
                    Is.EqualTo(ClientConnectionRecoveryResultKind.Deadline));
            }
            finally
            {
                await fixture.StopAsync();
            }
        }

        /// <summary>验证control、world与Scene gate共享同一个总deadline owner而不是逐阶段重置预算。</summary>
        [Test]
        public async Task ControlWorldAndSceneShareOneIntentDeadline()
        {
            var deadline = new CancellationTokenSource();
            var deadlineOwners = 0;
            var fixture = await RecoveryFixture.CreateAsync(_ =>
            {
                deadlineOwners++;
                return deadlineOwners == 1
                    ? deadline
                    : throw new InvalidOperationException("同一intent不得创建第二个deadline。");
            });
            try
            {
                fixture.Operations.ControlResult = PendingResult();
                fixture.Operations.WorldResult = PendingResult();
                fixture.Coordinator.BeginControlRecovery(41);
                var control = fixture.Coordinator.CompleteControlRecoveryAsync(42);
                Assert.That(fixture.Coordinator.BeginAutomaticWorldRecovery(43), Is.True);

                fixture.Operations.ControlResult.SetResult(
                    ClientConnectionRecoveryResultKind.Succeeded);
                await control;
                Assert.That(deadlineOwners, Is.EqualTo(1));
                fixture.Operations.WorldResult.SetResult(
                    ClientConnectionRecoveryResultKind.Succeeded);
                await WaitUntilAsync(() =>
                    fixture.Coordinator.Snapshot.Phase ==
                    ClientConnectionRecoveryPhase.AwaitingSceneCommit);

                deadline.Cancel();
                await WaitUntilAsync(() =>
                    fixture.Coordinator.Snapshot.Phase ==
                    ClientConnectionRecoveryPhase.ConnectionLost);
                Assert.That(
                    fixture.Coordinator.Snapshot.Result,
                    Is.EqualTo(ClientConnectionRecoveryResultKind.Deadline));
                Assert.That(deadlineOwners, Is.EqualTo(1));
            }
            finally
            {
                await fixture.StopAsync();
            }
        }

        /// <summary>验证Stop优先于迟到成功且只发布Stopped。</summary>
        [Test]
        public async Task ShutdownRejectsLateRecoveryCompletion()
        {
            var fixture = await RecoveryFixture.CreateAsync();
            fixture.Operations.WorldResult = PendingResult();
            Assert.That(fixture.Coordinator.BeginAutomaticWorldRecovery(10), Is.True);

            var stop = fixture.Coordinator.StopAsync(CancellationToken.None);
            fixture.Operations.WorldResult.SetResult(
                ClientConnectionRecoveryResultKind.Succeeded);
            await stop;

            Assert.That(
                fixture.Coordinator.Snapshot.Phase,
                Is.EqualTo(ClientConnectionRecoveryPhase.Stopped));
            Assert.That(
                fixture.Coordinator.Snapshot.Result,
                Is.EqualTo(ClientConnectionRecoveryResultKind.Stopped));
        }

        /// <summary>验证重复control lifecycle事件不创建第二个reconciliation。</summary>
        [Test]
        public async Task DuplicateControlEventsShareOneReconciliation()
        {
            var fixture = await RecoveryFixture.CreateAsync();
            try
            {
                fixture.Operations.ControlResult = PendingResult();
                fixture.Coordinator.BeginControlRecovery(101);
                fixture.Coordinator.BeginControlRecovery(101);
                var first = fixture.Coordinator.CompleteControlRecoveryAsync(101);
                var duplicate = fixture.Coordinator.CompleteControlRecoveryAsync(101);
                Assert.That(duplicate, Is.SameAs(first));
                Assert.That(fixture.Operations.InvalidateCalls, Is.EqualTo(1));
                Assert.That(fixture.Operations.ReconcileCalls, Is.EqualTo(1));

                fixture.Operations.ControlResult.SetResult(
                    ClientConnectionRecoveryResultKind.Succeeded);
                await first;
                Assert.That(
                    fixture.Coordinator.Snapshot.Phase,
                    Is.EqualTo(ClientConnectionRecoveryPhase.Idle));
            }
            finally
            {
                await fixture.StopAsync();
            }
        }

        /// <summary>验证WSS与gameplay同时失败时先收敛control，再以同一intent恢复已排队world。</summary>
        [Test]
        public async Task SimultaneousChannelFailuresContinueIntoQueuedWorldRecovery()
        {
            var fixture = await RecoveryFixture.CreateAsync();
            try
            {
                fixture.Operations.ControlResult = PendingResult();
                fixture.Operations.WorldResult = PendingResult();
                fixture.Coordinator.BeginControlRecovery(101);
                var control = fixture.Coordinator.CompleteControlRecoveryAsync(102);

                Assert.That(fixture.Coordinator.BeginAutomaticWorldRecovery(202), Is.True);
                Assert.That(fixture.Coordinator.BeginAutomaticWorldRecovery(202), Is.False);
                Assert.That(fixture.Operations.RecoverCalls, Is.Zero);

                var awaitingScene = new TaskCompletionSource<ClientConnectionRecoverySnapshot>(
                    TaskCreationOptions.RunContinuationsAsynchronously);
                fixture.Coordinator.Changed += snapshot =>
                {
                    if (snapshot.Phase == ClientConnectionRecoveryPhase.AwaitingSceneCommit)
                    {
                        awaitingScene.TrySetResult(snapshot);
                    }
                };
                fixture.Operations.ControlResult.SetResult(
                    ClientConnectionRecoveryResultKind.Succeeded);
                await control;
                Assert.That(fixture.Operations.RecoverCalls, Is.EqualTo(1));
                Assert.That(
                    fixture.Coordinator.Snapshot.Phase,
                    Is.EqualTo(ClientConnectionRecoveryPhase.RecoveringWorld));
                Assert.That(fixture.Coordinator.Snapshot.SourceChannelGeneration, Is.EqualTo(202));

                fixture.Operations.WorldResult.SetResult(
                    ClientConnectionRecoveryResultKind.Succeeded);
                var committed = await awaitingScene.Task;
                Assert.That(
                    fixture.Coordinator.ConfirmSceneCommit(
                        committed.IntentGeneration,
                        committed.TargetGeneration),
                    Is.True);
            }
            finally
            {
                fixture.Operations.ControlResult?.TrySetResult(
                    ClientConnectionRecoveryResultKind.Stopped);
                fixture.Operations.WorldResult?.TrySetResult(
                    ClientConnectionRecoveryResultKind.Stopped);
                await fixture.StopAsync();
            }
        }

        /// <summary>验证整服故障后的manual control恢复直接重建已断开的world，而不执行control-only校验。</summary>
        [Test]
        public async Task ManualControlRecoveryBypassesControlOnlyReconciliationWhenWorldIsInactive()
        {
            var fixture = await RecoveryFixture.CreateAsync();
            try
            {
                fixture.Operations.WorldResult = PendingResult();
                Assert.That(fixture.Coordinator.BeginAutomaticWorldRecovery(301), Is.True);
                fixture.Operations.WorldResult.SetResult(
                    ClientConnectionRecoveryResultKind.Transport);
                await WaitUntilAsync(() =>
                    fixture.Coordinator.Snapshot.Phase ==
                    ClientConnectionRecoveryPhase.ConnectionLost);

                fixture.Operations.WorldResult = PendingResult();
                Assert.That(fixture.Coordinator.BeginManualControlRecovery(302), Is.True);
                var recovery = fixture.Coordinator.CompleteControlRecoveryAsync(303);

                Assert.That(fixture.Operations.ReconcileCalls, Is.Zero);
                Assert.That(fixture.Operations.RecoverCalls, Is.EqualTo(2));
                Assert.That(
                    fixture.Coordinator.Snapshot.Phase,
                    Is.EqualTo(ClientConnectionRecoveryPhase.RecoveringWorld));

                fixture.Operations.CurrentTargetGeneration = 14;
                fixture.Operations.WorldResult.SetResult(
                    ClientConnectionRecoveryResultKind.Succeeded);
                await recovery;
                Assert.That(
                    fixture.Coordinator.Snapshot.Phase,
                    Is.EqualTo(ClientConnectionRecoveryPhase.AwaitingSceneCommit));
                Assert.That(
                    fixture.Coordinator.ConfirmSceneCommit(
                        fixture.Coordinator.Snapshot.IntentGeneration,
                        14),
                    Is.True);
            }
            finally
            {
                fixture.Operations.WorldResult?.TrySetResult(
                    ClientConnectionRecoveryResultKind.Stopped);
                await fixture.StopAsync();
            }
        }

        /// <summary>验证三轮恢复始终回到单一Idle且operation计数线性守恒。</summary>
        [Test]
        public async Task ThreeRecoveryRoundsReturnToSingleIdleOwner()
        {
            var fixture = await RecoveryFixture.CreateAsync();
            try
            {
                for (var round = 1; round <= 3; round++)
                {
                    fixture.Operations.WorldResult = PendingResult();
                    var awaitingScene = new TaskCompletionSource<ClientConnectionRecoverySnapshot>(
                        TaskCreationOptions.RunContinuationsAsynchronously);
                    void Observe(ClientConnectionRecoverySnapshot snapshot)
                    {
                        if (snapshot.Phase == ClientConnectionRecoveryPhase.AwaitingSceneCommit)
                        {
                            awaitingScene.TrySetResult(snapshot);
                        }
                    }

                    fixture.Coordinator.Changed += Observe;
                    try
                    {
                        Assert.That(
                            fixture.Coordinator.BeginAutomaticWorldRecovery(20 + round),
                            Is.True);
                        fixture.Operations.WorldResult.SetResult(
                            ClientConnectionRecoveryResultKind.Succeeded);
                        var committed = await awaitingScene.Task;
                        Assert.That(
                            fixture.Coordinator.ConfirmSceneCommit(
                                committed.IntentGeneration,
                                committed.TargetGeneration),
                            Is.True);
                        Assert.That(
                            fixture.Coordinator.Snapshot.Phase,
                            Is.EqualTo(ClientConnectionRecoveryPhase.Idle));
                    }
                    finally
                    {
                        fixture.Coordinator.Changed -= Observe;
                    }
                }

                Assert.That(fixture.Operations.RecoverCalls, Is.EqualTo(3));
            }
            finally
            {
                await fixture.StopAsync();
            }
        }

        /// <summary>验证同一battle generation复用唯一恢复task且总deadline稳定结束该task。</summary>
        [Test]
        public async Task BattleRecoveryIsSingleFlightAndUsesCoordinatorDeadline()
        {
            var deadline = new CancellationTokenSource();
            var deadlineOwners = 0;
            var fixture = await RecoveryFixture.CreateAsync(_ =>
            {
                deadlineOwners++;
                return deadlineOwners == 1
                    ? deadline
                    : throw new InvalidOperationException(
                        "同一battle recovery不得创建第二个deadline。");
            });
            try
            {
                var entered = new TaskCompletionSource<bool>(
                    TaskCreationOptions.RunContinuationsAsynchronously);
                var operationCalls = 0;
                async Task<bool> RecoverAsync(CancellationToken cancellationToken)
                {
                    operationCalls++;
                    entered.TrySetResult(true);
                    await WaitForCancellationAsync(cancellationToken);
                    cancellationToken.ThrowIfCancellationRequested();
                    return true;
                }

                var first = fixture.Coordinator.RunBattleRecoveryAsync(
                    501,
                    RecoverAsync);
                var duplicate = fixture.Coordinator.RunBattleRecoveryAsync(
                    501,
                    RecoverAsync);
                var conflicting = fixture.Coordinator.RunBattleRecoveryAsync(
                    502,
                    RecoverAsync);

                Assert.That(duplicate, Is.SameAs(first));
                Assert.That(await conflicting, Is.False);
                await entered.Task;
                Assert.That(operationCalls, Is.EqualTo(1));
                Assert.That(deadlineOwners, Is.EqualTo(1));
                Assert.That(fixture.Coordinator.QualificationIntentOwnerCount, Is.EqualTo(1));

                deadline.Cancel();
                Assert.That(await first, Is.False);
                Assert.That(fixture.Coordinator.QualificationIntentOwnerCount, Is.Zero);
            }
            finally
            {
                await fixture.StopAsync();
            }
        }

        /// <summary>验证权威world恢复抢占battle-only重试并继续持有唯一恢复intent。</summary>
        [Test]
        public async Task WorldRecoveryPreemptsBattleOnlyRecovery()
        {
            var fixture = await RecoveryFixture.CreateAsync();
            try
            {
                var entered = new TaskCompletionSource<bool>(
                    TaskCreationOptions.RunContinuationsAsynchronously);
                var canceled = new TaskCompletionSource<bool>(
                    TaskCreationOptions.RunContinuationsAsynchronously);
                var battle = fixture.Coordinator.RunBattleRecoveryAsync(
                    601,
                    async cancellationToken =>
                    {
                        entered.TrySetResult(true);
                        await WaitForCancellationAsync(cancellationToken);
                        canceled.TrySetResult(true);
                        cancellationToken.ThrowIfCancellationRequested();
                        return true;
                    });
                await entered.Task;

                fixture.Operations.WorldResult = PendingResult();
                Assert.That(
                    fixture.Coordinator.BeginAutomaticWorldRecovery(701),
                    Is.True);
                await canceled.Task;
                Assert.That(await battle, Is.False);
                Assert.That(
                    fixture.Coordinator.Snapshot.Phase,
                    Is.EqualTo(ClientConnectionRecoveryPhase.RecoveringWorld));
                Assert.That(fixture.Coordinator.QualificationIntentOwnerCount, Is.EqualTo(1));
                Assert.That(fixture.Operations.RecoverCalls, Is.EqualTo(1));
            }
            finally
            {
                fixture.Operations.WorldResult?.TrySetResult(
                    ClientConnectionRecoveryResultKind.Stopped);
                await fixture.StopAsync();
            }
        }

        /// <summary>创建异步continuation的可控结果。</summary>
        private static TaskCompletionSource<ClientConnectionRecoveryResultKind> PendingResult()
        {
            return new TaskCompletionSource<ClientConnectionRecoveryResultKind>(
                TaskCreationOptions.RunContinuationsAsynchronously);
        }

        /// <summary>在有限yield内等待纯C#状态提交。</summary>
        private static async Task WaitUntilAsync(Func<bool> predicate)
        {
            for (var attempt = 0; attempt < 64; attempt++)
            {
                if (predicate())
                {
                    return;
                }

                await Task.Yield();
            }

            Assert.Fail("恢复状态未在受控continuation内提交。");
        }

        /// <summary>把取消token转换为不依赖真实时间的任务。</summary>
        private static Task WaitForCancellationAsync(CancellationToken cancellationToken)
        {
            var completion = new TaskCompletionSource<bool>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            cancellationToken.Register(() => completion.TrySetResult(true));
            return completion.Task;
        }

        /// <summary>组合已认证Session与可控恢复operation。</summary>
        private sealed class RecoveryFixture
        {
            /// <summary>创建完整fixture。</summary>
            private RecoveryFixture(
                SessionCoordinator session,
                ClientConnectionRecoveryCoordinator coordinator,
                FakeRecoveryOperations operations)
            {
                Session = session;
                Coordinator = coordinator;
                Operations = operations;
            }

            /// <summary>已认证Session owner。</summary>
            internal SessionCoordinator Session { get; }

            /// <summary>待测恢复owner。</summary>
            internal ClientConnectionRecoveryCoordinator Coordinator { get; }

            /// <summary>可控窄operation。</summary>
            internal FakeRecoveryOperations Operations { get; }

            /// <summary>创建具有可选deadline seam的fixture。</summary>
            internal static async Task<RecoveryFixture> CreateAsync(
                Func<TimeSpan, CancellationTokenSource> deadlineFactory = null)
            {
                var configuration = new ClientConfigurationStore();
                await configuration.InitializeAsync(CancellationToken.None);
                configuration.Publish(new ClientConfigurationSnapshot(
                    new ClientVersionInfo(1, "0.1.0", "fixture"),
                    new ClientBootstrapConfiguration(
                        new[]
                        {
                            new ClientEndpoint(ClientEndpointChannel.Wss, "control.invalid", 443),
                            new ClientEndpoint(ClientEndpointChannel.TlsTcp, "game.invalid", 4433),
                        },
                        new ClientPublicLimits(4096, 65536))));
                var session = new SessionCoordinator(
                    configuration,
                    new AuthenticationApi(),
                    new FixtureClock(),
                    new FakeClientSecureSessionStore(),
                    FakeClientSecureSessionStore.EnvironmentBinding);
                await session.InitializeAsync(CancellationToken.None);
                var login = await session.LoginAsync(
                    "fixture",
                    "temporary-password",
                    CancellationToken.None);
                Assert.That(login.IsSuccess, Is.True);

                var operations = new FakeRecoveryOperations(login.Value.Generation);
                var coordinator = deadlineFactory == null
                    ? new ClientConnectionRecoveryCoordinator(
                        session,
                        operations,
                        TimeSpan.FromSeconds(45))
                    : new ClientConnectionRecoveryCoordinator(
                        session,
                        operations,
                        TimeSpan.FromSeconds(45),
                        deadlineFactory);
                await coordinator.InitializeAsync(CancellationToken.None);
                return new RecoveryFixture(session, coordinator, operations);
            }

            /// <summary>逆序停止fixture owner。</summary>
            internal async Task StopAsync()
            {
                await Coordinator.StopAsync(CancellationToken.None);
                await Session.StopAsync(CancellationToken.None);
            }
        }

        /// <summary>提供可控target、operation completion与计数。</summary>
        private sealed class FakeRecoveryOperations : IClientConnectionRecoveryOperations
        {
            /// <summary>创建匹配Session generation的OwnWorld target。</summary>
            internal FakeRecoveryOperations(long sessionGeneration)
            {
                Target = new ClientRecoveryTargetDescriptor(
                    ClientRecoveryTargetKind.OwnWorld,
                    sessionGeneration,
                    "session_fixture",
                    1,
                    3,
                    "pworld_fixture",
                    "winst_fixture",
                    null,
                    2,
                    0,
                    1,
                    0);
                CurrentTargetGeneration = 4;
            }

            /// <summary>当前冻结target。</summary>
            internal ClientRecoveryTargetDescriptor Target { get; }

            /// <summary>恢复后current target generation。</summary>
            internal long CurrentTargetGeneration { get; set; }

            /// <summary>Control completion。</summary>
            internal TaskCompletionSource<ClientConnectionRecoveryResultKind> ControlResult { get; set; }

            /// <summary>World completion。</summary>
            internal TaskCompletionSource<ClientConnectionRecoveryResultKind> WorldResult { get; set; }

            /// <summary>可选World operation实现。</summary>
            internal Func<ClientRecoveryTargetDescriptor, CancellationToken,
                Task<ClientConnectionRecoveryResultKind>> WorldHandler { get; set; }

            /// <summary>Control invalidation次数。</summary>
            internal int InvalidateCalls { get; private set; }

            /// <summary>Control reconciliation次数。</summary>
            internal int ReconcileCalls { get; private set; }

            /// <summary>World recovery次数。</summary>
            internal int RecoverCalls { get; private set; }

            /// <inheritdoc />
            public bool TryCaptureTarget(out ClientRecoveryTargetDescriptor descriptor)
            {
                descriptor = Target;
                return true;
            }

            /// <inheritdoc />
            public void InvalidateControlOnlyState()
            {
                InvalidateCalls++;
            }

            /// <inheritdoc />
            public Task<ClientConnectionRecoveryResultKind> ReconcileControlAsync(
                ClientRecoveryTargetDescriptor descriptor,
                CancellationToken cancellationToken)
            {
                ReconcileCalls++;
                return ControlResult?.Task ?? Task.FromResult(
                    ClientConnectionRecoveryResultKind.Succeeded);
            }

            /// <inheritdoc />
            public Task<ClientConnectionRecoveryResultKind> RecoverWorldAsync(
                ClientRecoveryTargetDescriptor descriptor,
                CancellationToken cancellationToken)
            {
                RecoverCalls++;
                return WorldHandler != null
                    ? WorldHandler(descriptor, cancellationToken)
                    : WorldResult?.Task ?? Task.FromResult(
                        ClientConnectionRecoveryResultKind.Succeeded);
            }

            /// <inheritdoc />
            public bool TryValidateRecoveredTarget(
                ClientRecoveryTargetDescriptor descriptor,
                out long currentTargetGeneration)
            {
                currentTargetGeneration = CurrentTargetGeneration;
                return true;
            }
        }

        /// <summary>提供固定Unix毫秒值。</summary>
        private sealed class FixtureClock : IClientClock
        {
            /// <inheritdoc />
            public long UtcNowMilliseconds => 1_000;
        }

        /// <summary>只实现测试登录与同Session token轮换，其余HTTP operation稳定拒绝。</summary>
        private sealed class AuthenticationApi : IClientBootstrapGateway, IClientSessionGateway
        {
            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientVersionInfo>> GetVersionAsync(CancellationToken token) =>
                Unsupported<ClientVersionInfo>("version");

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientBootstrapConfiguration>> GetBootstrapConfigurationAsync(CancellationToken token) =>
                Unsupported<ClientBootstrapConfiguration>("config");

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientAuthentication>> RegisterAsync(ClientRegisterGatewayRequest request, CancellationToken token) =>
                Unsupported<ClientAuthentication>("register");

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientAuthentication>> LoginAsync(ClientLoginGatewayRequest request, CancellationToken token) =>
                Task.FromResult(ClientGatewayResult<ClientAuthentication>.Success(
                    new ClientAuthentication(
                        new ClientAccountSummary("account_fixture", "Fixture", 1),
                        new ClientSessionSummary("session_fixture", 1, 100_000),
                        new ClientTokenPair("access_fixture", "refresh_fixture", 90_000, 99_000),
                        Array.Empty<ClientEndpoint>())));

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientTokenPair>> RefreshAsync(ClientCredentialGatewayRequest request, CancellationToken token) =>
                Task.FromResult(ClientGatewayResult<ClientTokenPair>.Success(
                    new ClientTokenPair(
                        "access_refreshed",
                        "refresh_rotated",
                        95_000,
                        99_000)));

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientGatewayEmpty>> LogoutAsync(ClientCredentialGatewayRequest request, CancellationToken token) => Unsupported<ClientGatewayEmpty>("logout");

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientConnectionTicket>> IssueConnectionTicketAsync(ClientConnectionTicketGatewayRequest request, CancellationToken token) => Unsupported<ClientConnectionTicket>("ticket");

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientWorldBootstrap>> GetWorldBootstrapAsync(ClientCredentialGatewayRequest request, CancellationToken token) => Unsupported<ClientWorldBootstrap>("world");

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientVisitReservation>> AcceptVisitInviteAsync(ClientAcceptVisitInviteGatewayRequest request, CancellationToken token) => Unsupported<ClientVisitReservation>("accept");

            /// <inheritdoc />
            public Task<ClientGatewayResult<ClientWorldAdmission>> IssueWorldAdmissionAsync(ClientWorldAdmissionGatewayRequest request, CancellationToken token) => Unsupported<ClientWorldAdmission>("admission");

            /// <summary>返回不触发网络的固定policy失败。</summary>
            private static Task<ClientGatewayResult<T>> Unsupported<T>(string operation)
            {
                return Task.FromResult(ClientGatewayResult<T>.Failed(
                    new ClientGatewayFailure(ClientGatewayFailureKind.LocalPolicy, operation)));
            }
        }
    }
}
