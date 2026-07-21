using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Presentation.PersonalWorld;
using IHomeland.Client.Scenes.Contexts;
using IHomeland.Client.Scenes.PersonalWorld;
using NUnit.Framework;
using UnityEngine;
using UnityEngine.SceneManagement;

namespace IHomeland.Client.Tests.PlayMode
{
    /// <summary>验证 PersonalWorldSceneContext 只接受同 Scene 直接引用和 current generation。</summary>
    public sealed class PersonalWorldSceneContextPlayModeTests
    {
        /// <summary>验证 SceneLifetime 释放后旧 Context 立即拒绝迟到 HUD 回写。</summary>
        [Test]
        public async Task ReleasedSceneLifetimeRejectsLateHudApply()
        {
            var fixture = CreateFixture(includeLight: true);
            var lifetimeOwner = new SceneLifetimeOwner();
            try
            {
                await lifetimeOwner.InitializeAsync(CancellationToken.None);
                var lifetime = lifetimeOwner.BeginScene();
                var initial = Hud("world-1");
                fixture.Context.Bind(lifetime, initial);

                Assert.That(fixture.Context.TryApply(Hud("world-2")), Is.True);
                Assert.That(fixture.Context.ViewState.WorldInstanceID, Is.EqualTo("world-2"));

                lifetimeOwner.Release(lifetime);
                Assert.That(fixture.Context.TryApply(Hud("late-world")), Is.False);
                Assert.That(fixture.Context.ViewState.WorldInstanceID, Is.EqualTo("world-2"));
            }
            finally
            {
                await lifetimeOwner.StopAsync(CancellationToken.None);
                UnityEngine.Object.DestroyImmediate(fixture.Root);
            }
        }

        /// <summary>验证缺少任一 Camera、Light 或 SceneRoot 直接引用时 fail closed。</summary>
        [Test]
        public void MissingDirectReferenceIsRejectedBeforeBinding()
        {
            var fixture = CreateFixture(includeLight: false);
            try
            {
                Assert.Throws<InvalidOperationException>(() => fixture.Context.ValidateConfiguration());
            }
            finally
            {
                UnityEngine.Object.DestroyImmediate(fixture.Root);
            }
        }

        /// <summary>验证 Context 发现只接受候选 Scene 内恰好一个实例。</summary>
        [Test]
        public async Task ContextDiscoveryRejectsMissingAndDuplicateInstances()
        {
            var scene = SceneManager.CreateScene($"ContextDiscovery-{Guid.NewGuid():N}");
            GameObject first = null;
            GameObject second = null;
            try
            {
                Assert.Throws<InvalidOperationException>(() =>
                    ClientWorldSceneTransitionHost.FindSingleContext(scene));

                first = new GameObject("FirstContext");
                SceneManager.MoveGameObjectToScene(first, scene);
                var firstContext = first.AddComponent<PersonalWorldSceneContext>();
                Assert.That(
                    ClientWorldSceneTransitionHost.FindSingleContext(scene),
                    Is.SameAs(firstContext));

                second = new GameObject("SecondContext");
                SceneManager.MoveGameObjectToScene(second, scene);
                second.AddComponent<PersonalWorldSceneContext>();
                Assert.Throws<InvalidOperationException>(() =>
                    ClientWorldSceneTransitionHost.FindSingleContext(scene));
            }
            finally
            {
                UnityEngine.Object.DestroyImmediate(second);
                UnityEngine.Object.DestroyImmediate(first);
                await AwaitOperationAsync(SceneManager.UnloadSceneAsync(scene));
            }
        }

        /// <summary>验证登记场景原子提交后只有 current Scene/target generation 可以继续写入。</summary>
        [Test]
        public async Task RegisteredSceneCommitsCurrentGenerationAndRejectsLateApply()
        {
            var fixture = await TransitionFixture.CreateAsync();
            try
            {
                var result = await fixture.Host.LoadAsync(
                    ClientWorldSceneId.PersonalWorld,
                    targetGeneration: 1,
                    ViewState(targetGeneration: 1, sceneGeneration: 0, "world-current"),
                    CancellationToken.None);

                Assert.That(result, Is.EqualTo(ClientWorldSceneTransitionCode.Succeeded));
                var committed = fixture.Host.Snapshot;
                Assert.That(committed.SceneGeneration, Is.GreaterThan(0));
                Assert.That(fixture.Host.TryApply(ViewState(
                    targetGeneration: 1,
                    committed.SceneGeneration,
                    "world-updated")), Is.True);

                Assert.That(
                    await fixture.Host.UnloadAsync(CancellationToken.None),
                    Is.EqualTo(ClientWorldSceneTransitionCode.Succeeded));
                Assert.That(fixture.Host.TryApply(ViewState(
                    targetGeneration: 1,
                    committed.SceneGeneration,
                    "world-late")), Is.False);
                Assert.That(fixture.Host.Snapshot.SceneId, Is.EqualTo(ClientWorldSceneId.None));
            }
            finally
            {
                await fixture.DisposeAsync();
            }
        }

        /// <summary>验证 load 中切换 target 时旧候选回滚且新 target 成为唯一 current。</summary>
        [Test]
        public async Task TargetSwitchDuringLoadSupersedesOldCandidate()
        {
            var fixture = await TransitionFixture.CreateAsync();
            try
            {
                var first = fixture.Host.LoadAsync(
                    ClientWorldSceneId.PersonalWorld,
                    targetGeneration: 1,
                    ViewState(targetGeneration: 1, sceneGeneration: 0, "world-old"),
                    CancellationToken.None);
                var second = fixture.Host.LoadAsync(
                    ClientWorldSceneId.PersonalWorld,
                    targetGeneration: 2,
                    ViewState(targetGeneration: 2, sceneGeneration: 0, "world-new"),
                    CancellationToken.None);

                Assert.That(await first, Is.EqualTo(ClientWorldSceneTransitionCode.Superseded));
                Assert.That(await second, Is.EqualTo(ClientWorldSceneTransitionCode.Succeeded));
                Assert.That(fixture.Host.Snapshot.TargetGeneration, Is.EqualTo(2));
                Assert.That(fixture.Host.TryApply(ViewState(
                    targetGeneration: 1,
                    fixture.Host.Snapshot.SceneGeneration,
                    "world-late")), Is.False);
            }
            finally
            {
                await fixture.DisposeAsync();
            }
        }

        /// <summary>验证 unload 与 App stop 会使候选失效，且停止终态不被后续请求污染。</summary>
        [Test]
        public async Task UnloadRaceAndAppStopLeaveNoCurrentScene()
        {
            var fixture = await TransitionFixture.CreateAsync();
            try
            {
                var load = fixture.Host.LoadAsync(
                    ClientWorldSceneId.PersonalWorld,
                    targetGeneration: 1,
                    ViewState(targetGeneration: 1, sceneGeneration: 0, "world-racing"),
                    CancellationToken.None);
                var unload = fixture.Host.UnloadAsync(CancellationToken.None);
                Assert.That(await load, Is.EqualTo(ClientWorldSceneTransitionCode.Superseded));
                Assert.That(await unload, Is.EqualTo(ClientWorldSceneTransitionCode.Succeeded));
                Assert.That(fixture.Host.Snapshot.SceneId, Is.EqualTo(ClientWorldSceneId.None));

                var stoppingLoad = fixture.Host.LoadAsync(
                    ClientWorldSceneId.PersonalWorld,
                    targetGeneration: 2,
                    ViewState(targetGeneration: 2, sceneGeneration: 0, "world-stopping"),
                    CancellationToken.None);
                await fixture.Host.StopAsync(CancellationToken.None);
                Assert.That(
                    await stoppingLoad,
                    Is.EqualTo(ClientWorldSceneTransitionCode.Cancelled)
                        .Or.EqualTo(ClientWorldSceneTransitionCode.Superseded));
                Assert.That(fixture.Host.Snapshot.LastResult, Is.EqualTo(ClientWorldSceneTransitionCode.Stopped));

                Assert.That(
                    await fixture.Host.LoadAsync(
                        ClientWorldSceneId.PersonalWorld,
                        targetGeneration: 3,
                        ViewState(targetGeneration: 3, sceneGeneration: 0, "world-after-stop"),
                        CancellationToken.None),
                    Is.EqualTo(ClientWorldSceneTransitionCode.Stopped));
                Assert.That(fixture.Host.Snapshot.LastResult, Is.EqualTo(ClientWorldSceneTransitionCode.Stopped));
                Assert.That(fixture.Host.Snapshot.Transitioning, Is.False);
            }
            finally
            {
                await fixture.DisposeAsync();
            }
        }

        /// <summary>创建完全位于当前测试 Scene 的直接引用 fixture。</summary>
        /// <param name="includeLight">是否创建必需 Light。</param>
        /// <returns>尚未绑定 SceneLifetime 的 fixture。</returns>
        private static SceneContextFixture CreateFixture(bool includeLight)
        {
            var root = new GameObject("PersonalWorldSceneFixture");
            root.SetActive(false);
            var context = root.AddComponent<PersonalWorldSceneContext>();
            var cameraObject = new GameObject("SceneCamera");
            cameraObject.transform.SetParent(root.transform, worldPositionStays: false);
            var camera = cameraObject.AddComponent<Camera>();
            Light light = null;
            if (includeLight)
            {
                var lightObject = new GameObject("SceneLight");
                lightObject.transform.SetParent(root.transform, worldPositionStays: false);
                light = lightObject.AddComponent<Light>();
            }

            var sceneRootObject = new GameObject("SceneRoot");
            sceneRootObject.transform.SetParent(root.transform, worldPositionStays: false);
            context.ConfigureBeforeActivation(camera, light, sceneRootObject.transform);
            root.SetActive(true);
            return new SceneContextFixture(root, context);
        }

        /// <summary>创建最小无 credential HUD 投影。</summary>
        /// <param name="worldInstanceID">测试 WorldInstance 标识。</param>
        /// <returns>Owner HUD 投影。</returns>
        private static ClientWorldHudViewState Hud(string worldInstanceID)
        {
            return new ClientWorldHudViewState(
                visible: true,
                isOwner: true,
                isVisitor: false,
                personalWorldID: "personal-world",
                worldInstanceID,
                visitSessionID: string.Empty);
        }

        /// <summary>创建指定 target/scene generation 的最小完整页面状态。</summary>
        /// <param name="targetGeneration">权威目标代际。</param>
        /// <param name="sceneGeneration">已提交 Scene 代际；候选加载前为 0。</param>
        /// <param name="worldInstanceID">测试 WorldInstance 标识。</param>
        /// <returns>不含 credential 的 Owner 页面投影。</returns>
        private static ClientPersonalWorldViewState ViewState(
            long targetGeneration,
            long sceneGeneration,
            string worldInstanceID)
        {
            var baseline = ClientPersonalWorldViewState.Inactive;
            return new ClientPersonalWorldViewState(
                presentationGeneration: 1,
                targetGeneration,
                sceneGeneration,
                ClientPersonalWorldPhase.OwnWorld,
                ClientPersonalWorldIntent.None,
                baseline.Login,
                baseline.Shell,
                baseline.WorldVisit,
                Hud(worldInstanceID));
        }

        /// <summary>等待 Unity SceneManager operation 完成。</summary>
        /// <param name="operation">可为空的卸载 operation。</param>
        /// <returns>Operation 完成时结束。</returns>
        private static async Task AwaitOperationAsync(AsyncOperation operation)
        {
            while (operation != null && !operation.isDone)
            {
                await Task.Yield();
            }
        }

        /// <summary>保存测试创建且需要统一销毁的 Context 对象。</summary>
        private sealed class SceneContextFixture
        {
            /// <summary>创建 fixture。</summary>
            /// <param name="root">测试根对象。</param>
            /// <param name="context">被测 Context。</param>
            internal SceneContextFixture(GameObject root, PersonalWorldSceneContext context)
            {
                Root = root ?? throw new ArgumentNullException(nameof(root));
                Context = context ?? throw new ArgumentNullException(nameof(context));
            }

            /// <summary>获取测试根对象。</summary>
            internal GameObject Root { get; }

            /// <summary>获取被测 Context。</summary>
            internal PersonalWorldSceneContext Context { get; }
        }

        /// <summary>保存已初始化且按生产逆序清理的场景转换 fixture。</summary>
        private sealed class TransitionFixture
        {
            /// <summary>保存程序化 Host GameObject。</summary>
            private readonly GameObject _root;

            /// <summary>保存 Scene generation owner。</summary>
            private readonly SceneLifetimeOwner _lifetimeOwner;

            /// <summary>创建已初始化 fixture。</summary>
            /// <param name="root">承载 Host 的程序化对象。</param>
            /// <param name="lifetimeOwner">已初始化 owner。</param>
            /// <param name="host">已初始化 Host。</param>
            private TransitionFixture(
                GameObject root,
                SceneLifetimeOwner lifetimeOwner,
                ClientWorldSceneTransitionHost host)
            {
                _root = root ?? throw new ArgumentNullException(nameof(root));
                _lifetimeOwner = lifetimeOwner ?? throw new ArgumentNullException(nameof(lifetimeOwner));
                Host = host ?? throw new ArgumentNullException(nameof(host));
            }

            /// <summary>获取被测场景转换 Host。</summary>
            internal ClientWorldSceneTransitionHost Host { get; }

            /// <summary>按生产顺序初始化 owner 与 Host。</summary>
            /// <returns>可加载登记场景的 fixture。</returns>
            internal static async Task<TransitionFixture> CreateAsync()
            {
                var root = new GameObject("WorldSceneTransitionFixture");
                var owner = new SceneLifetimeOwner();
                var host = root.AddComponent<ClientWorldSceneTransitionHost>();
                await owner.InitializeAsync(CancellationToken.None);
                host.Configure(owner);
                await host.InitializeAsync(CancellationToken.None);
                return new TransitionFixture(root, owner, host);
            }

            /// <summary>按 Host -> owner 逆序幂等停止并销毁程序化对象。</summary>
            /// <returns>场景与 generation 已释放时完成。</returns>
            internal async Task DisposeAsync()
            {
                try
                {
                    await Host.StopAsync(CancellationToken.None);
                }
                finally
                {
                    await _lifetimeOwner.StopAsync(CancellationToken.None);
                    UnityEngine.Object.DestroyImmediate(_root);
                }
            }
        }
    }
}
