using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Presentation.Navigation;
using NUnit.Framework;

namespace IHomeland.Client.Tests.EditMode
{
    /// <summary>
    /// 验证 UI registry、事务化 route、modal、容量、generation 与停止边界。
    /// </summary>
    public sealed class ClientUiRouterTests
    {
        /// <summary>
        /// Fake Host 阶段同步完成；一秒上限用于让清理死锁快速暴露而不拖慢测试。
        /// </summary>
        private static readonly TimeSpan TestCleanupTimeout = TimeSpan.FromSeconds(1);

        /// <summary>
        /// 保护 production 空 registry 可独立启动且不创建占位 route。
        /// </summary>
        /// <returns>等待 input/router 生命周期完成的任务。</returns>
        [Test]
        public async Task EmptyRegistryStartsWithoutActiveRoute()
        {
            var input = new FakeInputCoordinator();
            var router = await CreateRouterAsync(
                Array.Empty<ClientUiRouteDefinition>(),
                Array.Empty<IClientUiViewHost>(),
                input);

            Assert.That(router.CurrentSnapshot.Items, Is.Empty);
            Assert.That(input.CurrentState.Mode, Is.EqualTo(ClientUiInputMode.Gameplay));
            await router.StopAsync(CancellationToken.None);
            await input.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 保护模型在进入 registry/router 前拒绝未登记 enum、非法 generation 与矛盾 input owner。
        /// </summary>
        [Test]
        public void ModelsRejectInvalidEnumsGenerationsAndInputOwnership()
        {
            Assert.Throws<ArgumentOutOfRangeException>(() => Definition(
                (ClientUiRouteId)999,
                ClientUiFrameworkOwner.UiToolkit,
                ClientUiLayer.Screen,
                ClientUiInputMode.Ui,
                ClientUiLifecycle.Cached));
            Assert.Throws<ArgumentOutOfRangeException>(() => Definition(
                ClientUiRouteId.Login,
                (ClientUiFrameworkOwner)999,
                ClientUiLayer.Screen,
                ClientUiInputMode.Ui,
                ClientUiLifecycle.Cached));
            Assert.Throws<ArgumentOutOfRangeException>(() => Definition(
                ClientUiRouteId.Login,
                ClientUiFrameworkOwner.UiToolkit,
                (ClientUiLayer)999,
                ClientUiInputMode.Ui,
                ClientUiLifecycle.Cached));
            Assert.Throws<ArgumentOutOfRangeException>(() => Definition(
                ClientUiRouteId.Login,
                ClientUiFrameworkOwner.UiToolkit,
                ClientUiLayer.Screen,
                (ClientUiInputMode)999,
                ClientUiLifecycle.Cached));
            Assert.Throws<ArgumentOutOfRangeException>(() => Definition(
                ClientUiRouteId.Login,
                ClientUiFrameworkOwner.UiToolkit,
                ClientUiLayer.Screen,
                ClientUiInputMode.Ui,
                (ClientUiLifecycle)999));
            Assert.Throws<ArgumentOutOfRangeException>(() => new ClientUiRouteBinding(
                (ClientUiRouteId)999,
                navigationGeneration: 1,
                sceneGeneration: 0,
                CancellationToken.None));
            Assert.Throws<ArgumentOutOfRangeException>(() => new ClientUiFocusToken(
                ClientUiRouteId.Login,
                hostGeneration: 0,
                value: null));
            Assert.Throws<ArgumentException>(() => new ClientUiInputState(
                ClientUiInputMode.Gameplay,
                ClientUiRouteId.Login));
            Assert.Throws<ArgumentException>(() => new ClientUiInputState(
                ClientUiInputMode.Ui,
                ClientUiRouteId.None));
        }

        /// <summary>
        /// 保护 registry 在任何 Host 副作用前拒绝重复 identity 与 Host 重用。
        /// </summary>
        [Test]
        public void RegistryRejectsDuplicateIdentityAndHostReuse()
        {
            var login = Definition(
                ClientUiRouteId.Login,
                ClientUiFrameworkOwner.UiToolkit,
                ClientUiLayer.Screen,
                ClientUiInputMode.Ui,
                ClientUiLifecycle.Cached);
            var duplicate = Definition(
                ClientUiRouteId.Login,
                ClientUiFrameworkOwner.UiToolkit,
                ClientUiLayer.Screen,
                ClientUiInputMode.Ui,
                ClientUiLifecycle.Cached);
            var host = new FakeHost(ClientUiRouteId.Login, ClientUiFrameworkOwner.UiToolkit);

            Assert.Throws<ArgumentException>(() => new ClientUiRegistry(
                new[] { login, duplicate },
                new IClientUiViewHost[] { host }));
            Assert.Throws<ArgumentException>(() => new ClientUiRegistry(
                new[] { login },
                new IClientUiViewHost[] { host, host }));
            Assert.Throws<ArgumentException>(() => new ClientUiRegistry(
                new[] { login },
                new IClientUiViewHost[]
                {
                    new FakeHost((ClientUiRouteId)999, ClientUiFrameworkOwner.UiToolkit),
                }));
            Assert.That(host.InitializeCount, Is.Zero);
        }

        /// <summary>
        /// 保护 registry 拒绝 framework 错配，并防御性复制 definition。
        /// </summary>
        [Test]
        public void RegistryRejectsFrameworkMismatchAndCopiesDefinitions()
        {
            var source = Definition(
                ClientUiRouteId.Settings,
                ClientUiFrameworkOwner.UiToolkit,
                ClientUiLayer.Overlay,
                ClientUiInputMode.Ui,
                ClientUiLifecycle.Recreate);
            var mismatched = new FakeHost(ClientUiRouteId.Settings, ClientUiFrameworkOwner.Ugui);

            Assert.Throws<ArgumentException>(() => new ClientUiRegistry(
                new[] { source },
                new IClientUiViewHost[] { mismatched }));

            var host = new FakeHost(ClientUiRouteId.Settings, ClientUiFrameworkOwner.UiToolkit);
            var registry = new ClientUiRegistry(
                new[] { source },
                new IClientUiViewHost[] { host });
            Assert.That(registry.Definitions[0], Is.Not.SameAs(source));
            Assert.That(registry.Definitions[0].RouteId, Is.EqualTo(source.RouteId));
        }

        /// <summary>
        /// 保护未登记 route 与非法 Scene generation 在 Host 调用前稳定拒绝。
        /// </summary>
        /// <returns>等待 router 初始化和清理完成的任务。</returns>
        [Test]
        public async Task UnregisteredAndInvalidSceneRouteFailBeforeHost()
        {
            var world = Definition(
                ClientUiRouteId.WorldVisit,
                ClientUiFrameworkOwner.Ugui,
                ClientUiLayer.Hud,
                ClientUiInputMode.Gameplay,
                ClientUiLifecycle.SceneBound);
            var host = new FakeHost(ClientUiRouteId.WorldVisit, ClientUiFrameworkOwner.Ugui);
            var input = new FakeInputCoordinator();
            var router = await CreateRouterAsync(new[] { world }, new IClientUiViewHost[] { host }, input);

            var missing = await router.OpenAsync(
                ClientUiRouteId.Login,
                sceneGeneration: 0,
                CancellationToken.None);
            var invalidScene = await router.OpenAsync(
                ClientUiRouteId.WorldVisit,
                sceneGeneration: 0,
                CancellationToken.None);

            Assert.That(missing.Code, Is.EqualTo(ClientUiTransitionCode.NotRegistered));
            Assert.That(invalidScene.Code, Is.EqualTo(ClientUiTransitionCode.InvalidSceneGeneration));
            Assert.That(host.InitializeCount, Is.Zero);
            await router.StopAsync(CancellationToken.None);
            await input.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 保护 cached screen 替换后只重新 bind，不重复初始化 Host。
        /// </summary>
        /// <returns>等待三次导航和清理完成的任务。</returns>
        [Test]
        public async Task CachedScreenRebindsWithoutSecondInitialization()
        {
            var login = Definition(
                ClientUiRouteId.Login,
                ClientUiFrameworkOwner.UiToolkit,
                ClientUiLayer.Screen,
                ClientUiInputMode.Ui,
                ClientUiLifecycle.Cached);
            var shell = Definition(
                ClientUiRouteId.Shell,
                ClientUiFrameworkOwner.UiToolkit,
                ClientUiLayer.Screen,
                ClientUiInputMode.Ui,
                ClientUiLifecycle.Recreate);
            var loginHost = new FakeHost(ClientUiRouteId.Login, ClientUiFrameworkOwner.UiToolkit);
            var shellHost = new FakeHost(ClientUiRouteId.Shell, ClientUiFrameworkOwner.UiToolkit);
            var input = new FakeInputCoordinator();
            var router = await CreateRouterAsync(
                new[] { login, shell },
                new IClientUiViewHost[] { loginHost, shellHost },
                input);

            Assert.That((await router.OpenAsync(ClientUiRouteId.Login, 0, CancellationToken.None)).IsSuccess);
            Assert.That((await router.OpenAsync(ClientUiRouteId.Shell, 0, CancellationToken.None)).IsSuccess);
            Assert.That((await router.OpenAsync(ClientUiRouteId.Login, 0, CancellationToken.None)).IsSuccess);

            Assert.That(loginHost.InitializeCount, Is.EqualTo(1));
            Assert.That(loginHost.BindCount, Is.EqualTo(2));
            Assert.That(shellHost.DisposeCount, Is.EqualTo(1));
            Assert.That(router.CurrentSnapshot.Items.Count, Is.EqualTo(1));
            Assert.That(router.CurrentSnapshot.Items[0].Definition.RouteId, Is.EqualTo(ClientUiRouteId.Login));
            await router.StopAsync(CancellationToken.None);
            await input.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 保护 modal 栈只允许关闭栈顶，并在逐层关闭后恢复下层交互与 focus。
        /// </summary>
        /// <returns>等待 modal 导航和清理完成的任务。</returns>
        [Test]
        public async Task ModalStackRejectsOutOfOrderCloseAndRestoresOwner()
        {
            var screen = Definition(
                ClientUiRouteId.Shell,
                ClientUiFrameworkOwner.UiToolkit,
                ClientUiLayer.Screen,
                ClientUiInputMode.Ui,
                ClientUiLifecycle.Cached);
            var firstModal = Definition(
                ClientUiRouteId.ConnectionLost,
                ClientUiFrameworkOwner.Ugui,
                ClientUiLayer.Modal,
                ClientUiInputMode.Modal,
                ClientUiLifecycle.Recreate);
            var secondModal = Definition(
                ClientUiRouteId.Settings,
                ClientUiFrameworkOwner.UiToolkit,
                ClientUiLayer.Modal,
                ClientUiInputMode.Modal,
                ClientUiLifecycle.Recreate);
            var screenHost = new FakeHost(ClientUiRouteId.Shell, ClientUiFrameworkOwner.UiToolkit);
            var firstHost = new FakeHost(ClientUiRouteId.ConnectionLost, ClientUiFrameworkOwner.Ugui);
            var secondHost = new FakeHost(ClientUiRouteId.Settings, ClientUiFrameworkOwner.UiToolkit);
            var input = new FakeInputCoordinator();
            var router = await CreateRouterAsync(
                new[] { screen, firstModal, secondModal },
                new IClientUiViewHost[] { screenHost, firstHost, secondHost },
                input);

            await router.OpenAsync(ClientUiRouteId.Shell, 0, CancellationToken.None);
            await router.OpenAsync(ClientUiRouteId.ConnectionLost, 0, CancellationToken.None);
            await router.OpenAsync(ClientUiRouteId.Settings, 0, CancellationToken.None);

            var rejected = await router.CloseAsync(
                ClientUiRouteId.ConnectionLost,
                CancellationToken.None);
            Assert.That(rejected.Code, Is.EqualTo(ClientUiTransitionCode.PolicyRejected));
            Assert.That((await router.CloseAsync(ClientUiRouteId.Settings, CancellationToken.None)).IsSuccess);
            Assert.That(firstHost.Interactive, Is.True);
            Assert.That((await router.CloseAsync(ClientUiRouteId.ConnectionLost, CancellationToken.None)).IsSuccess);
            Assert.That(screenHost.Interactive, Is.True);
            Assert.That(input.CurrentState.InteractiveRouteId, Is.EqualTo(ClientUiRouteId.Shell));
            await router.StopAsync(CancellationToken.None);
            await input.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 保护低层 route 在 modal 存在时可以显示但不会抢夺 input 或 focus。
        /// </summary>
        /// <returns>等待 route 导航和清理完成的任务。</returns>
        [Test]
        public async Task LowerLayerOpenedUnderModalRemainsNonInteractive()
        {
            var modal = Definition(
                ClientUiRouteId.ConnectionLost,
                ClientUiFrameworkOwner.Ugui,
                ClientUiLayer.Modal,
                ClientUiInputMode.Modal,
                ClientUiLifecycle.Cached);
            var screen = Definition(
                ClientUiRouteId.Login,
                ClientUiFrameworkOwner.UiToolkit,
                ClientUiLayer.Screen,
                ClientUiInputMode.Ui,
                ClientUiLifecycle.Cached);
            var modalHost = new FakeHost(ClientUiRouteId.ConnectionLost, ClientUiFrameworkOwner.Ugui);
            var screenHost = new FakeHost(ClientUiRouteId.Login, ClientUiFrameworkOwner.UiToolkit);
            var input = new FakeInputCoordinator();
            var router = await CreateRouterAsync(
                new[] { modal, screen },
                new IClientUiViewHost[] { modalHost, screenHost },
                input);

            await router.OpenAsync(ClientUiRouteId.ConnectionLost, 0, CancellationToken.None);
            await router.OpenAsync(ClientUiRouteId.Login, 0, CancellationToken.None);

            Assert.That(modalHost.Interactive, Is.True);
            Assert.That(screenHost.Interactive, Is.False);
            Assert.That(screenHost.FocusDefaultCount, Is.Zero);
            Assert.That(input.CurrentState.InteractiveRouteId, Is.EqualTo(ClientUiRouteId.ConnectionLost));
            await router.StopAsync(CancellationToken.None);
            await input.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 保护 candidate show 失败时旧 owner、input、focus 与 snapshot 全部恢复。
        /// </summary>
        /// <returns>等待失败 transaction 回滚和清理完成的任务。</returns>
        [Test]
        public async Task ShowFailureRollsBackCandidateAndRestoresPreviousRoute()
        {
            var login = Definition(
                ClientUiRouteId.Login,
                ClientUiFrameworkOwner.UiToolkit,
                ClientUiLayer.Screen,
                ClientUiInputMode.Ui,
                ClientUiLifecycle.Cached);
            var shell = Definition(
                ClientUiRouteId.Shell,
                ClientUiFrameworkOwner.Ugui,
                ClientUiLayer.Screen,
                ClientUiInputMode.Ui,
                ClientUiLifecycle.Recreate);
            var loginHost = new FakeHost(ClientUiRouteId.Login, ClientUiFrameworkOwner.UiToolkit);
            var shellHost = new FakeHost(ClientUiRouteId.Shell, ClientUiFrameworkOwner.Ugui)
            {
                FailShow = true,
            };
            var input = new FakeInputCoordinator();
            var router = await CreateRouterAsync(
                new[] { login, shell },
                new IClientUiViewHost[] { loginHost, shellHost },
                input);
            await router.OpenAsync(ClientUiRouteId.Login, 0, CancellationToken.None);

            var failed = await router.OpenAsync(ClientUiRouteId.Shell, 0, CancellationToken.None);

            Assert.That(failed.Code, Is.EqualTo(ClientUiTransitionCode.HostFailure));
            Assert.That(router.CurrentSnapshot.Items.Count, Is.EqualTo(1));
            Assert.That(router.CurrentSnapshot.Items[0].Definition.RouteId, Is.EqualTo(ClientUiRouteId.Login));
            Assert.That(loginHost.Interactive, Is.True);
            Assert.That(loginHost.RestoreFocusCount + loginHost.FocusDefaultCount, Is.GreaterThan(1));
            Assert.That(shellHost.DisposeCount, Is.EqualTo(1));
            Assert.That(input.CurrentState.InteractiveRouteId, Is.EqualTo(ClientUiRouteId.Login));
            await router.StopAsync(CancellationToken.None);
            await input.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 保护 transition 等待队列达到硬上限时立即拒绝额外调用。
        /// </summary>
        /// <returns>等待阻塞 Host、容量拒绝与清理完成的任务。</returns>
        [Test]
        public async Task TransitionQueueRejectsRequestsBeyondCapacity()
        {
            var login = Definition(
                ClientUiRouteId.Login,
                ClientUiFrameworkOwner.UiToolkit,
                ClientUiLayer.Screen,
                ClientUiInputMode.Ui,
                ClientUiLifecycle.Cached);
            var host = new FakeHost(ClientUiRouteId.Login, ClientUiFrameworkOwner.UiToolkit)
            {
                BindGate = new TaskCompletionSource<bool>(),
            };
            var input = new FakeInputCoordinator();
            var router = await CreateRouterAsync(
                new[] { login },
                new IClientUiViewHost[] { host },
                input,
                maximumQueuedTransitions: 1);

            var first = router.OpenAsync(ClientUiRouteId.Login, 0, CancellationToken.None);
            await host.BindEntered.Task;
            var second = router.OpenAsync(ClientUiRouteId.Login, 0, CancellationToken.None);
            await Task.Yield();
            var overloaded = await router.OpenAsync(ClientUiRouteId.Login, 0, CancellationToken.None);

            Assert.That(overloaded.Code, Is.EqualTo(ClientUiTransitionCode.Overloaded));
            host.BindGate.SetResult(true);
            Assert.That((await first).IsSuccess);
            Assert.That((await second).IsSuccess);
            await router.StopAsync(CancellationToken.None);
            await input.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 保护调用方在 candidate 提交前取消时完成逆序回滚，不留下 active route 或 Host generation。
        /// </summary>
        /// <returns>等待受控 bind、取消、回滚和清理完成的任务。</returns>
        [Test]
        public async Task CallerCancellationBeforeCommitRollsBackCandidate()
        {
            var login = Definition(
                ClientUiRouteId.Login,
                ClientUiFrameworkOwner.UiToolkit,
                ClientUiLayer.Screen,
                ClientUiInputMode.Ui,
                ClientUiLifecycle.Cached);
            var host = new FakeHost(ClientUiRouteId.Login, ClientUiFrameworkOwner.UiToolkit)
            {
                BindGate = new TaskCompletionSource<bool>(),
            };
            var input = new FakeInputCoordinator();
            var router = await CreateRouterAsync(new[] { login }, new IClientUiViewHost[] { host }, input);
            using (var cancellation = new CancellationTokenSource())
            {
                var open = router.OpenAsync(ClientUiRouteId.Login, 0, cancellation.Token);
                await host.BindEntered.Task;
                cancellation.Cancel();
                var result = await open;

                Assert.That(result.Code, Is.EqualTo(ClientUiTransitionCode.Cancelled));
                Assert.That(result.Committed, Is.False);
                Assert.That(router.CurrentSnapshot.Items, Is.Empty);
                Assert.That(host.DisposeCount, Is.EqualTo(1));
            }

            await router.StopAsync(CancellationToken.None);
            await input.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 保护 snapshot 已提交后由 subscriber 触发的 caller cancellation 不回滚已提交 route。
        /// </summary>
        /// <returns>等待提交、锁外通知与清理完成的任务。</returns>
        [Test]
        public async Task CommitWinsCancellationRaisedBySnapshotSubscriber()
        {
            var login = Definition(
                ClientUiRouteId.Login,
                ClientUiFrameworkOwner.UiToolkit,
                ClientUiLayer.Screen,
                ClientUiInputMode.Ui,
                ClientUiLifecycle.Recreate);
            var host = new FakeHost(ClientUiRouteId.Login, ClientUiFrameworkOwner.UiToolkit);
            var input = new FakeInputCoordinator();
            var router = await CreateRouterAsync(new[] { login }, new IClientUiViewHost[] { host }, input);
            using (var cancellation = new CancellationTokenSource())
            {
                router.SnapshotChanged += _ => cancellation.Cancel();
                var result = await router.OpenAsync(ClientUiRouteId.Login, 0, cancellation.Token);

                Assert.That(result.IsSuccess, Is.True);
                Assert.That(result.Committed, Is.True);
                Assert.That(router.CurrentSnapshot.Items.Count, Is.EqualTo(1));
            }

            await router.StopAsync(CancellationToken.None);
            await input.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 保护重复 open 与重复 close 为幂等成功，不重复初始化或生成第二个 active owner。
        /// </summary>
        /// <returns>等待重复导航和清理完成的任务。</returns>
        [Test]
        public async Task DuplicateOpenAndCloseAreIdempotent()
        {
            var login = Definition(
                ClientUiRouteId.Login,
                ClientUiFrameworkOwner.UiToolkit,
                ClientUiLayer.Screen,
                ClientUiInputMode.Ui,
                ClientUiLifecycle.Recreate);
            var host = new FakeHost(ClientUiRouteId.Login, ClientUiFrameworkOwner.UiToolkit);
            var input = new FakeInputCoordinator();
            var router = await CreateRouterAsync(new[] { login }, new IClientUiViewHost[] { host }, input);

            var firstOpen = await router.OpenAsync(ClientUiRouteId.Login, 0, CancellationToken.None);
            var duplicateOpen = await router.OpenAsync(ClientUiRouteId.Login, 0, CancellationToken.None);
            Assert.That(firstOpen.IsSuccess, Is.True);
            Assert.That(firstOpen.Committed, Is.True);
            Assert.That(duplicateOpen.IsSuccess, Is.True);
            Assert.That(duplicateOpen.Committed, Is.False);
            Assert.That(host.InitializeCount, Is.EqualTo(1));
            Assert.That(router.CurrentSnapshot.Items.Count, Is.EqualTo(1));
            var firstClose = await router.CloseAsync(ClientUiRouteId.Login, CancellationToken.None);
            var duplicateClose = await router.CloseAsync(ClientUiRouteId.Login, CancellationToken.None);
            Assert.That(firstClose.IsSuccess, Is.True);
            Assert.That(firstClose.Committed, Is.True);
            Assert.That(duplicateClose.IsSuccess, Is.True);
            Assert.That(duplicateClose.Committed, Is.False);
            Assert.That(router.CurrentSnapshot.Items, Is.Empty);

            await router.StopAsync(CancellationToken.None);
            await router.StopAsync(CancellationToken.None);
            await input.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 保护 SceneBound route 失效后旧 binding 与 Host generation 不能继续提交。
        /// </summary>
        /// <returns>等待 scene route 打开、失效和清理完成的任务。</returns>
        [Test]
        public async Task SceneInvalidationCancelsBindingAndRejectsLateCommit()
        {
            var world = Definition(
                ClientUiRouteId.WorldVisit,
                ClientUiFrameworkOwner.Ugui,
                ClientUiLayer.Hud,
                ClientUiInputMode.Gameplay,
                ClientUiLifecycle.SceneBound);
            var host = new FakeHost(ClientUiRouteId.WorldVisit, ClientUiFrameworkOwner.Ugui);
            var input = new FakeInputCoordinator();
            var router = await CreateRouterAsync(new[] { world }, new IClientUiViewHost[] { host }, input);
            var open = await router.OpenAsync(ClientUiRouteId.WorldVisit, 42, CancellationToken.None);
            var binding = host.LastBinding;
            var hostGeneration = host.HostGeneration;

            Assert.That(open.IsSuccess, Is.True);
            Assert.That(input.CurrentState.Mode, Is.EqualTo(ClientUiInputMode.Gameplay));
            Assert.That(input.CurrentState.InteractiveRouteId, Is.EqualTo(ClientUiRouteId.None));
            Assert.That(router.CanCommit(binding, hostGeneration), Is.True);
            var invalidation = await router.InvalidateSceneAsync(42, CancellationToken.None);
            Assert.That(
                invalidation.IsSuccess,
                Is.True,
                $"Scene invalidation 应普通成功，实际 code={invalidation.Code}、committed={invalidation.Committed}。");
            Assert.That(binding.CancellationToken.IsCancellationRequested, Is.True);
            Assert.That(router.CanCommit(binding, hostGeneration), Is.False);
            Assert.That(router.CurrentSnapshot.Items, Is.Empty);
            await router.StopAsync(CancellationToken.None);
            await input.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 保护 subscriber 异常不回滚已提交 snapshot，但必须形成可观察的提交后失败。
        /// </summary>
        /// <returns>等待导航、subscriber、停止和拒绝完成的任务。</returns>
        [Test]
        public async Task SubscriberFailureDoesNotRollbackAndStopRejectsNavigation()
        {
            var login = Definition(
                ClientUiRouteId.Login,
                ClientUiFrameworkOwner.UiToolkit,
                ClientUiLayer.Screen,
                ClientUiInputMode.Ui,
                ClientUiLifecycle.Recreate);
            var host = new FakeHost(ClientUiRouteId.Login, ClientUiFrameworkOwner.UiToolkit);
            var input = new FakeInputCoordinator();
            var router = await CreateRouterAsync(new[] { login }, new IClientUiViewHost[] { host }, input);
            router.SnapshotChanged += _ => throw new InvalidOperationException("fixture subscriber failure");

            var result = await router.OpenAsync(ClientUiRouteId.Login, 0, CancellationToken.None);
            Assert.That(result.Code, Is.EqualTo(ClientUiTransitionCode.CommittedWithPostCommitFailure));
            Assert.That(result.Committed, Is.True);
            Assert.That(router.CurrentSnapshot.Items.Count, Is.EqualTo(1));
            await router.StopAsync(CancellationToken.None);
            var stopped = await router.OpenAsync(ClientUiRouteId.Login, 0, CancellationToken.None);
            Assert.That(stopped.Code, Is.EqualTo(ClientUiTransitionCode.Stopped));
            Assert.Throws<InvalidOperationException>(() =>
                router.SnapshotChanged += _ => { });
            await input.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 保护 UI route 缺少可用默认 focus 时仍提交可见页面，但返回可观察的后置失败。
        /// </summary>
        /// <returns>等待导航与清理完成的任务。</returns>
        [Test]
        public async Task MissingDefaultFocusIsObservableAfterCommit()
        {
            var login = Definition(
                ClientUiRouteId.Login,
                ClientUiFrameworkOwner.UiToolkit,
                ClientUiLayer.Screen,
                ClientUiInputMode.Ui,
                ClientUiLifecycle.Recreate);
            var host = new FakeHost(ClientUiRouteId.Login, ClientUiFrameworkOwner.UiToolkit)
            {
                DefaultFocusAvailable = false,
            };
            var input = new FakeInputCoordinator();
            var router = await CreateRouterAsync(new[] { login }, new IClientUiViewHost[] { host }, input);

            var result = await router.OpenAsync(ClientUiRouteId.Login, 0, CancellationToken.None);

            Assert.That(result.Code, Is.EqualTo(ClientUiTransitionCode.CommittedWithPostCommitFailure));
            Assert.That(result.Committed, Is.True);
            Assert.That(router.CurrentSnapshot.Items.Count, Is.EqualTo(1));
            await router.StopAsync(CancellationToken.None);
            await input.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 保护 stop 等待 gate 时被 deadline 取消后仍可再次调用并完成终态清理。
        /// </summary>
        /// <returns>等待受控 transition、两次 stop 与清理完成的任务。</returns>
        [Test]
        public async Task CancelledStopCanBeRetriedToFinishCleanup()
        {
            var login = Definition(
                ClientUiRouteId.Login,
                ClientUiFrameworkOwner.UiToolkit,
                ClientUiLayer.Screen,
                ClientUiInputMode.Ui,
                ClientUiLifecycle.Recreate);
            var host = new FakeHost(ClientUiRouteId.Login, ClientUiFrameworkOwner.UiToolkit)
            {
                BindGate = new TaskCompletionSource<bool>(),
            };
            var input = new FakeInputCoordinator();
            var router = await CreateRouterAsync(new[] { login }, new IClientUiViewHost[] { host }, input);
            var open = router.OpenAsync(ClientUiRouteId.Login, 0, CancellationToken.None);
            await host.BindEntered.Task;
            using (var stopCancellation = new CancellationTokenSource())
            {
                stopCancellation.Cancel();
                Assert.CatchAsync<OperationCanceledException>(async () =>
                    await router.StopAsync(stopCancellation.Token));
            }

            var openResult = await open;
            Assert.That(openResult.Code, Is.EqualTo(ClientUiTransitionCode.Stopped));
            await router.StopAsync(CancellationToken.None);
            Assert.That(router.CurrentSnapshot.Items, Is.Empty);
            await input.StopAsync(CancellationToken.None);
        }

        /// <summary>
        /// 创建并启动使用 fake input 的 router fixture。
        /// </summary>
        /// <param name="definitions">完整测试 definitions。</param>
        /// <param name="hosts">完整测试 Hosts。</param>
        /// <param name="input">fake input owner。</param>
        /// <param name="maximumQueuedTransitions">transition 等待硬上限。</param>
        /// <returns>已完成 input/router 初始化的 fixture。</returns>
        private static async Task<ClientUiRouter> CreateRouterAsync(
            IReadOnlyList<ClientUiRouteDefinition> definitions,
            IReadOnlyList<IClientUiViewHost> hosts,
            FakeInputCoordinator input,
            int maximumQueuedTransitions = 4)
        {
            await input.InitializeAsync(CancellationToken.None);
            var router = new ClientUiRouter(
                new ClientUiRegistry(definitions, hosts),
                input,
                maximumQueuedTransitions,
                cleanupTimeout: TestCleanupTimeout);
            await router.InitializeAsync(CancellationToken.None);
            return router;
        }

        /// <summary>
        /// 创建测试 route definition。
        /// </summary>
        /// <param name="routeId">route identity。</param>
        /// <param name="framework">framework owner。</param>
        /// <param name="layer">route layer。</param>
        /// <param name="input">input mode。</param>
        /// <param name="lifecycle">lifecycle policy。</param>
        /// <returns>已验证不可变 definition。</returns>
        private static ClientUiRouteDefinition Definition(
            ClientUiRouteId routeId,
            ClientUiFrameworkOwner framework,
            ClientUiLayer layer,
            ClientUiInputMode input,
            ClientUiLifecycle lifecycle)
        {
            return new ClientUiRouteDefinition(routeId, framework, layer, input, lifecycle);
        }

        /// <summary>
        /// 提供可控制失败与等待顺序的纯 C# Host fixture。
        /// </summary>
        private sealed class FakeHost : IClientUiViewHost
        {
            /// <summary>保存当前是否已初始化。</summary>
            private bool _initialized;

            /// <summary>保存当前是否已 bind。</summary>
            private bool _bound;

            /// <summary>
            /// 创建绑定固定 identity/framework 的 fake Host。
            /// </summary>
            /// <param name="routeId">唯一 route identity。</param>
            /// <param name="frameworkOwner">唯一 framework owner。</param>
            internal FakeHost(ClientUiRouteId routeId, ClientUiFrameworkOwner frameworkOwner)
            {
                RouteId = routeId;
                FrameworkOwner = frameworkOwner;
                BindEntered = new TaskCompletionSource<bool>();
            }

            /// <summary>获取固定 route identity。</summary>
            public ClientUiRouteId RouteId { get; }

            /// <summary>获取固定 framework owner。</summary>
            public ClientUiFrameworkOwner FrameworkOwner { get; }

            /// <summary>获取每次初始化递增的 Host generation。</summary>
            public long HostGeneration { get; private set; }

            /// <summary>获取 Initialize 调用次数。</summary>
            internal int InitializeCount { get; private set; }

            /// <summary>获取 Bind 调用次数。</summary>
            internal int BindCount { get; private set; }

            /// <summary>获取 Dispose 调用次数。</summary>
            internal int DisposeCount { get; private set; }

            /// <summary>获取 FocusDefault 调用次数。</summary>
            internal int FocusDefaultCount { get; private set; }

            /// <summary>获取 RestoreFocus 调用次数。</summary>
            internal int RestoreFocusCount { get; private set; }

            /// <summary>获取当前是否拥有交互资格。</summary>
            internal bool Interactive { get; private set; }

            /// <summary>获取最近一次 binding。</summary>
            internal ClientUiRouteBinding LastBinding { get; private set; }

            /// <summary>获取或设置 show 阶段是否抛出 fixture failure。</summary>
            internal bool FailShow { get; set; }

            /// <summary>获取或设置默认 focus 阶段是否存在有效目标。</summary>
            internal bool DefaultFocusAvailable { get; set; } = true;

            /// <summary>获取或设置 bind 阶段的可控等待 gate。</summary>
            internal TaskCompletionSource<bool> BindGate { get; set; }

            /// <summary>获取 bind 已进入的可等待信号。</summary>
            internal TaskCompletionSource<bool> BindEntered { get; }

            /// <summary>
            /// 初始化 fake generation。
            /// </summary>
            /// <param name="cancellationToken">fixture 取消。</param>
            /// <returns>已初始化任务。</returns>
            public Task InitializeAsync(CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                if (_initialized)
                {
                    throw new InvalidOperationException("fixture Host 已初始化。");
                }

                _initialized = true;
                HostGeneration++;
                InitializeCount++;
                return Task.CompletedTask;
            }

            /// <summary>
            /// 绑定并按需等待 fixture gate。
            /// </summary>
            /// <param name="binding">route binding。</param>
            /// <param name="cancellationToken">fixture 取消。</param>
            /// <returns>binding 已完成时的任务。</returns>
            public async Task BindAsync(
                ClientUiRouteBinding binding,
                CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                if (!_initialized || _bound)
                {
                    throw new InvalidOperationException("fixture Host bind 状态非法。");
                }

                BindEntered.TrySetResult(true);
                if (BindGate != null)
                {
                    await AwaitWithCancellationAsync(BindGate.Task, cancellationToken);
                }

                _bound = true;
                LastBinding = binding;
                BindCount++;
            }

            /// <summary>
            /// 显示 fake view 或抛出受控 failure。
            /// </summary>
            /// <param name="layer">登记 layer。</param>
            /// <param name="cancellationToken">fixture 取消。</param>
            /// <returns>已显示任务。</returns>
            public Task ShowAsync(ClientUiLayer layer, CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                if (!_bound)
                {
                    throw new InvalidOperationException("fixture Host 尚未 bind。");
                }

                if (FailShow)
                {
                    throw new InvalidOperationException("fixture show failure");
                }

                return Task.CompletedTask;
            }

            /// <summary>
            /// 切换 fake view 交互状态。
            /// </summary>
            /// <param name="interactive">目标交互状态。</param>
            /// <param name="cancellationToken">fixture 取消。</param>
            /// <returns>状态已提交任务。</returns>
            public Task SetInteractiveAsync(bool interactive, CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                Interactive = interactive;
                return Task.CompletedTask;
            }

            /// <summary>
            /// 捕获当前 fake focus token。
            /// </summary>
            /// <returns>绑定当前 Host generation 的 token。</returns>
            public ClientUiFocusToken CaptureFocus()
            {
                return new ClientUiFocusToken(RouteId, HostGeneration, value: this);
            }

            /// <summary>
            /// 记录默认 focus 请求。
            /// </summary>
            /// <param name="cancellationToken">fixture 取消。</param>
            /// <returns>始终成功的 focus 任务。</returns>
            public Task<bool> FocusDefaultAsync(CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                FocusDefaultCount++;
                return Task.FromResult(DefaultFocusAvailable);
            }

            /// <summary>
            /// 只恢复匹配 identity/generation 的 fake token。
            /// </summary>
            /// <param name="token">待恢复 token。</param>
            /// <param name="cancellationToken">fixture 取消。</param>
            /// <returns>token 匹配时返回 true。</returns>
            public Task<bool> RestoreFocusAsync(
                ClientUiFocusToken token,
                CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                RestoreFocusCount++;
                return Task.FromResult(
                    token != null &&
                    token.OwnerRouteId == RouteId &&
                    token.HostGeneration == HostGeneration);
            }

            /// <summary>
            /// 隐藏 fake view。
            /// </summary>
            /// <param name="cancellationToken">fixture 取消。</param>
            /// <returns>已隐藏任务。</returns>
            public Task HideAsync(CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                Interactive = false;
                return Task.CompletedTask;
            }

            /// <summary>
            /// 解除 fake binding。
            /// </summary>
            /// <param name="cancellationToken">fixture 取消。</param>
            /// <returns>已解除任务。</returns>
            public Task UnbindAsync(CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                _bound = false;
                LastBinding = null;
                return Task.CompletedTask;
            }

            /// <summary>
            /// 幂等释放 fake generation。
            /// </summary>
            /// <param name="cancellationToken">fixture 取消。</param>
            /// <returns>已释放任务。</returns>
            public Task DisposeAsync(CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                if (_initialized)
                {
                    DisposeCount++;
                }

                _initialized = false;
                _bound = false;
                Interactive = false;
                LastBinding = null;
                return Task.CompletedTask;
            }

            /// <summary>
            /// 以不依赖新 runtime API 的方式让 fixture Task 响应 cancellation。
            /// </summary>
            /// <param name="task">待等待任务。</param>
            /// <param name="cancellationToken">等待取消。</param>
            /// <returns>原任务完成或取消时完成的任务。</returns>
            private static async Task AwaitWithCancellationAsync(
                Task task,
                CancellationToken cancellationToken)
            {
                var cancellation = new TaskCompletionSource<bool>();
                using (cancellationToken.Register(() => cancellation.TrySetCanceled()))
                {
                    var completed = await Task.WhenAny(task, cancellation.Task);
                    await completed;
                }
            }
        }

        /// <summary>
        /// 提供记录原子 input state 的纯 C# fixture。
        /// </summary>
        private sealed class FakeInputCoordinator : IClientUiInputCoordinator
        {
            /// <summary>表示 fixture 是否已停止。</summary>
            private bool _stopped;

            /// <summary>获取最近一次提交的不可变 input state。</summary>
            public ClientUiInputState CurrentState { get; private set; } = ClientUiInputState.Gameplay;

            /// <summary>
            /// 初始化 fixture 并恢复 gameplay baseline。
            /// </summary>
            /// <param name="cancellationToken">fixture 取消。</param>
            /// <returns>已初始化任务。</returns>
            public Task InitializeAsync(CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                _stopped = false;
                CurrentState = ClientUiInputState.Gameplay;
                return Task.CompletedTask;
            }

            /// <summary>
            /// 提交不可变 input state。
            /// </summary>
            /// <param name="state">目标 state。</param>
            /// <param name="cancellationToken">fixture 取消。</param>
            /// <returns>已提交任务。</returns>
            public Task ApplyAsync(ClientUiInputState state, CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                if (_stopped)
                {
                    throw new InvalidOperationException("fixture input 已停止。");
                }

                CurrentState = state;
                return Task.CompletedTask;
            }

            /// <summary>
            /// 停止 fixture 并恢复 gameplay baseline。
            /// </summary>
            /// <param name="cancellationToken">fixture 取消。</param>
            /// <returns>已停止任务。</returns>
            public Task StopAsync(CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                CurrentState = ClientUiInputState.Gameplay;
                _stopped = true;
                return Task.CompletedTask;
            }
        }
    }
}
