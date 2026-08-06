using System;
using System.Collections;
using System.Linq;
using System.Reflection;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.PersonalWorldCombat.Application;
using IHomeland.Client.PersonalWorld.Presentation;
using IHomeland.Client.Core.Runtime.Scenes;
using IHomeland.Client.PersonalWorld.Runtime.Scenes;
using IHomeland.Client.PersonalWorldCombat.Runtime.Scenes;
using NUnit.Framework;
using Unity.Cinemachine;
using UnityEngine;
using UnityEngine.SceneManagement;
using UnityEngine.TestTools;

namespace IHomeland.Client.PersonalWorldCombat.Tests.PlayMode
{
    /// <summary>验证 PersonalWorldSceneContext 只接受同 Scene 直接引用和 current generation。</summary>
    public sealed class PersonalWorldSceneContextPlayModeTests
    {
        /// <summary>验证运行中render telemetry只更新固定计数器且不写Unity Console。</summary>
        [Test]
        public void RenderTelemetryAggregatesWithoutRuntimeConsoleWrites()
        {
            var root = new GameObject("RenderTelemetryFixture");
            try
            {
                var host = root.AddComponent<ClientBattleSceneHost>();
                var capture = typeof(ClientBattleSceneHost).GetMethod(
                    "CaptureRenderDiagnostics",
                    BindingFlags.Instance | BindingFlags.NonPublic);
                Assert.That(capture, Is.Not.Null);
                foreach (var seconds in new[] { 0.016f, 0.04f, 0.06f, 0.12f })
                {
                    capture.Invoke(host, new object[] { seconds });
                }

                LogAssert.NoUnexpectedReceived();
                Assert.That(
                    GetPrivateField<int>(host, "_diagnosticRenderFrames"),
                    Is.EqualTo(4));
                Assert.That(
                    GetPrivateField<int>(
                        host,
                        "_diagnosticFramesOverThirtyThreeMilliseconds"),
                    Is.EqualTo(3));
                Assert.That(
                    GetPrivateField<int>(
                        host,
                        "_diagnosticFramesOverFiftyMilliseconds"),
                    Is.EqualTo(2));
                Assert.That(
                    GetPrivateField<int>(
                        host,
                        "_diagnosticFramesOverOneHundredMilliseconds"),
                    Is.EqualTo(1));
            }
            finally
            {
                UnityEngine.Object.DestroyImmediate(root);
            }
        }

        /// <summary>验证世界参照只创建有界合并Renderer且不引入客户端Collider事实。</summary>
        [UnityTest]
        public IEnumerator ReferenceEnvironmentBuildsRendererOnlySpatialCues()
        {
            var root = new GameObject("ReferenceEnvironmentFixture");
            Material material = null;
            try
            {
                root.SetActive(false);
                var environment =
                    root.AddComponent<PersonalWorldReferenceEnvironment>();
                var shader =
                    Shader.Find("Universal Render Pipeline/Unlit");
                Assert.That(shader, Is.Not.Null);
                material = new Material(shader);
                SetPrivateField(
                    environment,
                    "_materialTemplate",
                    material);
                root.SetActive(true);
                yield return null;

                Assert.That(
                    root.GetComponentsInChildren<MeshRenderer>(),
                    Has.Length.EqualTo(3));
                Assert.That(
                    root.GetComponentsInChildren<MeshFilter>(),
                    Has.Length.EqualTo(3));
                Assert.That(
                    root.GetComponentsInChildren<Collider>(),
                    Is.Empty);
                Assert.That(
                    root.transform.Find("ReferenceGround"),
                    Is.Not.Null);
                Assert.That(
                    root.transform.Find("ReferenceGrid"),
                    Is.Not.Null);
                Assert.That(
                    root.transform.Find("ReferenceLandmarks"),
                    Is.Not.Null);
            }
            finally
            {
                UnityEngine.Object.DestroyImmediate(root);
                UnityEngine.Object.DestroyImmediate(material);
            }
        }

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

        /// <summary>
        /// 验证 Camera Host 对缺失 target 使用 Exploration fallback，并且每个 impulse identity
        /// 只消费一次，解绑后迟到 intent 不会重新触发。
        /// </summary>
        [Test]
        public void CameraHostMapsFallbackAndConsumesImpulseOnce()
        {
            var root = new GameObject("CameraHostFixture");
            root.SetActive(false);
            var host = root.AddComponent<CinemachineCameraHost>();
            var actors = root.AddComponent<ClientActorViewRegistry>();
            var impulseSource = root.AddComponent<CinemachineImpulseSource>();
            impulseSource.ImpulseDefinition.ImpulseChannel = 1;
            impulseSource.ImpulseDefinition.ImpulseShape =
                CinemachineImpulseDefinition.ImpulseShapes.Bump;
            impulseSource.ImpulseDefinition.ImpulseDuration = 0.2f;
            impulseSource.ImpulseDefinition.ImpulseType =
                CinemachineImpulseDefinition.ImpulseTypes.Uniform;
            impulseSource.ImpulseDefinition.DissipationDistance = 100f;
            impulseSource.DefaultVelocity = Vector3.down;
            var exploration = CreateChild(root, "ExplorationRig");
            var melee = CreateChild(root, "MeleeRig");
            var rangedAim = CreateChild(root, "RangedAimRig");
            var cinematic = CreateChild(root, "CinematicRig");
            var followProxy = CreateChild(root, "FollowProxy").transform;
            SetPrivateField(host, "_explorationRig", exploration);
            SetPrivateField(host, "_meleeRig", melee);
            SetPrivateField(host, "_rangedAimRig", rangedAim);
            SetPrivateField(host, "_cinematicRig", cinematic);
            SetPrivateField(host, "_followProxy", followProxy);
            SetPrivateField(host, "_impulseSource", impulseSource);

            var manager = CinemachineImpulseManager.Instance;
            var previousTimeOverride = CinemachineCore.CurrentTimeOverride;
            try
            {
                manager.Clear();
                CinemachineCore.CurrentTimeOverride = 10f;
                root.SetActive(true);
                host.Bind(sceneGeneration: 1);
                host.Apply(
                    new ClientBattleCameraIntent(
                        battleGeneration: 1,
                        mode: ClientBattleCameraMode.RangedAim,
                        followEntityID: 99,
                        impulseEventID: 7),
                    actors);

                Assert.That(exploration.activeSelf, Is.True);
                Assert.That(melee.activeSelf, Is.False);
                Assert.That(rangedAim.activeSelf, Is.False);
                Assert.That(cinematic.activeSelf, Is.False);
                CinemachineCore.CurrentTimeOverride = 10.05f;
                Assert.That(
                    manager.GetImpulseAt(
                        Vector3.zero,
                        distance2D: false,
                        channelMask: 1,
                        out _,
                        out _),
                    Is.True);

                manager.Clear();
                CinemachineCore.CurrentTimeOverride = 11f;
                host.Apply(
                    new ClientBattleCameraIntent(
                        battleGeneration: 1,
                        mode: ClientBattleCameraMode.Exploration,
                        followEntityID: 99,
                        impulseEventID: 7),
                    actors);
                CinemachineCore.CurrentTimeOverride = 11.05f;
                Assert.That(
                    manager.GetImpulseAt(
                        Vector3.zero,
                        distance2D: false,
                        channelMask: 1,
                        out _,
                        out _),
                    Is.False);

                host.Unbind();
                CinemachineCore.CurrentTimeOverride = 12f;
                host.Apply(
                    new ClientBattleCameraIntent(
                        battleGeneration: 1,
                        mode: ClientBattleCameraMode.Cinematic,
                        followEntityID: 99,
                        impulseEventID: 8),
                    actors);
                CinemachineCore.CurrentTimeOverride = 12.05f;
                Assert.That(
                    manager.GetImpulseAt(
                        Vector3.zero,
                        distance2D: false,
                        channelMask: 1,
                        out _,
                        out _),
                    Is.False);
            }
            finally
            {
                CinemachineCore.CurrentTimeOverride = previousTimeOverride;
                manager.Clear();
                UnityEngine.Object.DestroyImmediate(root);
            }
        }

        /// <summary>
        /// 验证 Actor registry 原子创建、替换和销毁 local/remote view，HUD 与 Camera 只消费
        /// immutable presentation state，并在解绑后拒绝迟到回写。
        /// </summary>
        [UnityTest]
        public IEnumerator ActorHudAndCameraHostsConsumeOnlyCurrentPresentation()
        {
            var root = new GameObject("BattlePresentationFixture");
            root.SetActive(false);

            var actorTemplate = CreateChild(root, "ActorTemplate");
            var actorRoot = CreateChild(root, "ActorRoot");
            var actors = root.AddComponent<ClientActorViewRegistry>();
            SetPrivateField(actors, "_actorPrefab", actorTemplate);
            SetPrivateField(actors, "_actorRoot", actorRoot.transform);
            SetPrivateField(actors, "_sceneGeneration", 1L);
            SetPrivateField(actors, "_battleGeneration", 21L);

            var canvasGroup = CreateChild(root, "BattleHud").AddComponent<CanvasGroup>();
            var statusText = AddTextComponent(CreateChild(root, "StatusText"));
            var healthText = AddTextComponent(CreateChild(root, "HealthText"));
            var weaponText = AddTextComponent(CreateChild(root, "WeaponText"));
            var skillText = AddTextComponent(CreateChild(root, "SkillText"));
            var bossHealthText = AddTextComponent(CreateChild(root, "BossHealthText"));
            var bossStateText = AddTextComponent(CreateChild(root, "BossStateText"));
            var hud = root.AddComponent<ClientBattleHudHost>();
            SetPrivateField(hud, "_root", canvasGroup);
            SetPrivateField(hud, "_statusText", statusText);
            SetPrivateField(hud, "_healthText", healthText);
            SetPrivateField(hud, "_weaponText", weaponText);
            SetPrivateField(hud, "_skillText", skillText);
            SetPrivateField(hud, "_bossHealthText", bossHealthText);
            SetPrivateField(hud, "_bossStateText", bossStateText);

            var camera = root.AddComponent<CinemachineCameraHost>();
            var exploration = CreateChild(root, "ExplorationRig");
            var melee = CreateChild(root, "MeleeRig");
            var rangedAim = CreateChild(root, "RangedAimRig");
            var cinematic = CreateChild(root, "CinematicRig");
            var followProxy = CreateChild(root, "FollowProxy").transform;
            var impulseSource = root.AddComponent<CinemachineImpulseSource>();
            SetPrivateField(camera, "_explorationRig", exploration);
            SetPrivateField(camera, "_meleeRig", melee);
            SetPrivateField(camera, "_rangedAimRig", rangedAim);
            SetPrivateField(camera, "_cinematicRig", cinematic);
            SetPrivateField(camera, "_followProxy", followProxy);
            SetPrivateField(camera, "_impulseSource", impulseSource);

            try
            {
                root.SetActive(true);
                hud.Bind(sceneGeneration: 1);
                camera.Bind(sceneGeneration: 1);
                Assert.That(
                    ReadText(statusText),
                    Is.EqualTo("Entering battle..."));
                Assert.That(ReadText(healthText), Is.EqualTo("HP waiting"));
                Assert.That(ReadText(weaponText), Is.EqualTo("Weapon waiting"));
                Assert.That(ReadText(skillText), Is.EqualTo("Primary waiting"));
                Assert.That(ReadText(bossHealthText), Is.EqualTo("Boss HP waiting"));
                Assert.That(ReadText(bossStateText), Is.EqualTo("Boss state waiting"));
                AssertBasicLatin(ReadText(statusText));
                AssertBasicLatin(ReadText(healthText));
                hud.ApplyRuntime(
                    new ClientBattleRuntimeSnapshot(
                        ClientBattleAvailability.LoadingBaseline,
                        ClientBattleFailure.None,
                        battleGeneration: 21,
                        targetKind: ClientBattleTargetKind.OwnWorld,
                        role: ClientBattleRole.Owner,
                        actorSlot: 1,
                        baselineReady: false,
                        inputEnabled: false));
                Assert.That(
                    ReadText(statusText),
                    Is.EqualTo("Loading character..."));
                AssertBasicLatin(ReadText(statusText));
                var initial = Presentation(
                    local: Actor(
                        entityID: 2,
                        entityGeneration: 1,
                        local: true,
                        x: 1000,
                        y: 2000,
                        z: 3000,
                        yaw: 90000),
                    remote: Actor(
                        entityID: 3,
                        entityGeneration: 1,
                        local: false,
                        x: 4000,
                        y: 5000,
                        z: 6000,
                        yaw: 180000),
                    correctionVisible: true,
                    cameraMode: ClientBattleCameraMode.RangedAim);

                Assert.That(actors.TryApply(initial), Is.True);
                hud.Apply(initial.Hud);
                camera.Apply(initial.Camera, actors);
                Assert.That(actorRoot.transform.childCount, Is.EqualTo(2));
                actors.ReplaceBattleGeneration(
                    battleGeneration: 0,
                    preserveActors: true);
                Assert.That(
                    actorRoot.transform.childCount,
                    Is.EqualTo(2),
                    "Recoverable generation transition cleared the last trusted frame.");
                actors.ReplaceBattleGeneration(
                    battleGeneration: 21,
                    preserveActors: true);
                Assert.That(
                    actors.TryGetActorTransform(2, out var localTransform),
                    Is.True);
                AssertPosition(localTransform, 1f, 2f, 3f);
                Assert.That(
                    Mathf.DeltaAngle(localTransform.eulerAngles.y, 90f),
                    Is.EqualTo(0f).Within(0.01f));
                Assert.That(
                    actors.TryGetActorTransform(3, out var remoteTransform),
                    Is.True);
                AssertPosition(remoteTransform, 4f, 5f, 6f);
                Assert.That(rangedAim.activeSelf, Is.True);
                Assert.That(exploration.activeSelf, Is.False);
                Assert.That(followProxy.position, Is.EqualTo(localTransform.position));
                Assert.That(ReadText(statusText), Is.EqualTo("Battle ready"));
                Assert.That(ReadText(healthText), Is.EqualTo("HP 48.5/48.5"));
                Assert.That(ReadText(weaponText), Is.EqualTo("Weapon Sword"));
                Assert.That(ReadText(skillText), Is.EqualTo("Primary Ready"));
                Assert.That(ReadText(bossHealthText), Is.EqualTo("Boss absent"));
                Assert.That(ReadText(bossStateText), Is.EqualTo("Boss state absent"));
                AssertBasicLatin(ReadText(statusText));
                AssertBasicLatin(ReadText(healthText));

                hud.ApplyRuntime(
                    new ClientBattleRuntimeSnapshot(
                        ClientBattleAvailability.Connecting,
                        ClientBattleFailure.None,
                        battleGeneration: 0,
                        targetKind: ClientBattleTargetKind.OwnWorld,
                        role: ClientBattleRole.None,
                        actorSlot: -1,
                        baselineReady: false,
                        inputEnabled: false));
                Assert.That(
                    ReadText(statusText),
                    Is.EqualTo("Connection lost. Reconnecting..."));
                Assert.That(
                    ReadText(healthText),
                    Is.EqualTo("HP 48.5/48.5 (last known)"));
                hud.DiscardCommittedState();
                hud.ApplyRuntime(
                    new ClientBattleRuntimeSnapshot(
                        ClientBattleAvailability.Unavailable,
                        ClientBattleFailure.Protocol,
                        battleGeneration: 0,
                        targetKind: ClientBattleTargetKind.OwnWorld,
                        role: ClientBattleRole.None,
                        actorSlot: -1,
                        baselineReady: false,
                        inputEnabled: false));
                Assert.That(
                    ReadText(statusText),
                    Is.EqualTo("Battle failed to load. Re-enter the world."));
                Assert.That(
                    ReadText(healthText),
                    Is.EqualTo("HP waiting"));
                Assert.That(ReadText(weaponText), Is.EqualTo("Weapon waiting"));
                Assert.That(ReadText(skillText), Is.EqualTo("Primary waiting"));
                Assert.That(ReadText(bossHealthText), Is.EqualTo("Boss HP waiting"));
                Assert.That(ReadText(bossStateText), Is.EqualTo("Boss state waiting"));
                AssertBasicLatin(ReadText(statusText));

                hud.Apply(
                    new ClientBattleHudViewState(
                        battleGeneration: 21,
                        ClientBattleAvailability.Active,
                        localHealthMilli: 48500,
                        localMaxHealthMilli: 100000,
                        equippedWeaponID: ClientBattleContentIdentity.FanWeapon,
                        bossHealthMilli: 0,
                        bossMaxHealthMilli: 500000,
                        bossPhase: 3,
                        bossDead: true,
                        correctionVisible: false,
                        inputEnabled: false,
                        degraded: false));
                Assert.That(ReadText(healthText), Is.EqualTo("HP 48.5/100"));
                Assert.That(ReadText(weaponText), Is.EqualTo("Weapon Fan"));
                Assert.That(ReadText(skillText), Is.EqualTo("Primary Locked"));
                Assert.That(ReadText(bossHealthText), Is.EqualTo("Boss HP 0/500"));
                Assert.That(ReadText(bossStateText), Is.EqualTo("Boss phase 3 Dead"));
                AssertBasicLatin(ReadText(weaponText));
                AssertBasicLatin(ReadText(bossStateText));

                var advanced = Presentation(
                    local: Actor(
                        entityID: 2,
                        entityGeneration: 1,
                        local: true,
                        x: 3000,
                        y: 2000,
                        z: 3000,
                        yaw: 120000),
                    remote: Actor(
                        entityID: 3,
                        entityGeneration: 1,
                        local: false,
                        x: 4000,
                        y: 5000,
                        z: 6000,
                        yaw: 180000),
                    correctionVisible: false,
                    cameraMode: ClientBattleCameraMode.RangedAim);
                Assert.That(actors.TryApply(advanced), Is.True);
                AssertPosition(localTransform, 3f, 2f, 3f);
                var retargeted = Presentation(
                    local: Actor(
                        entityID: 2,
                        entityGeneration: 1,
                        local: true,
                        x: 5000,
                        y: 2000,
                        z: 3000,
                        yaw: 150000),
                    remote: Actor(
                        entityID: 3,
                        entityGeneration: 1,
                        local: false,
                        x: 4000,
                        y: 5000,
                        z: 6000,
                        yaw: 180000),
                    correctionVisible: false,
                    cameraMode: ClientBattleCameraMode.RangedAim);
                Assert.That(actors.TryApply(retargeted), Is.True);
                camera.Tick(actors);
                AssertPosition(localTransform, 5f, 2f, 3f);
                Assert.That(
                    Mathf.DeltaAngle(
                        localTransform.eulerAngles.y,
                        150f),
                    Is.EqualTo(0f).Within(0.01f));
                Assert.That(
                    followProxy.position,
                    Is.EqualTo(localTransform.position));
                camera.Tick(actors, semanticAimYawMillidegrees: 45000);
                Assert.That(
                    Mathf.DeltaAngle(followProxy.eulerAngles.y, 45f),
                    Is.EqualTo(0f).Within(0.01f),
                    "Interactive camera did not consume render-cadence semantic aim.");

                var movingSample = Presentation(
                    local: Actor(
                        entityID: 2,
                        entityGeneration: 2,
                        local: true,
                        x: 0,
                        y: 0,
                        z: 0,
                        yaw: 0,
                        velocityX: 3000),
                    remote: Actor(
                        entityID: 3,
                        entityGeneration: 1,
                        local: false,
                        x: 4000,
                        y: 5000,
                        z: 6000,
                        yaw: 180000),
                    correctionVisible: false,
                    cameraMode: ClientBattleCameraMode.Exploration);
                Assert.That(actors.TryApply(movingSample), Is.True);
                Assert.That(
                    actors.TryGetActorTransform(2, out var movingTransform),
                    Is.True);
                AssertPosition(movingTransform, 0f, 0f, 0f);
                for (var frame = 0; frame < 12; frame++)
                {
                    Assert.That(actors.TryApply(movingSample), Is.True);
                }

                Assert.That(
                    movingTransform.position.x,
                    Is.EqualTo(0f).Within(0.0001f),
                    "Local view不得根据prediction velocity建立第二条外推移动路径。");

                var predictionMovingSample = Presentation(
                    local: Actor(
                        entityID: 2,
                        entityGeneration: 3,
                        local: true,
                        x: 0,
                        y: 0,
                        z: 0,
                        yaw: 0,
                        velocityX: 3000),
                    remote: Actor(
                        entityID: 3,
                        entityGeneration: 1,
                        local: false,
                        x: 4000,
                        y: 5000,
                        z: 6000,
                        yaw: 180000),
                    correctionVisible: false,
                    cameraMode: ClientBattleCameraMode.Exploration);
                Assert.That(actors.TryApply(predictionMovingSample), Is.True);
                Assert.That(
                    actors.TryGetActorTransform(2, out var predictionTransform),
                    Is.True);
                var renderSamplesMillimeters =
                    new[] { 12, 75, 126, 150, 189, 225, 300 };
                for (var frame = 0;
                     frame < renderSamplesMillimeters.Length;
                     frame++)
                {
                    predictionMovingSample = Presentation(
                        local: Actor(
                            entityID: 2,
                            entityGeneration: 3,
                            local: true,
                            x: renderSamplesMillimeters[frame],
                            y: 0,
                            z: 0,
                            yaw: 0,
                            velocityX: 3000),
                        remote: Actor(
                            entityID: 3,
                            entityGeneration: 1,
                            local: false,
                            x: 4000,
                            y: 5000,
                            z: 6000,
                            yaw: 180000),
                        correctionVisible: false,
                        cameraMode: ClientBattleCameraMode.Exploration);
                    Assert.That(
                        actors.TryApply(predictionMovingSample),
                        Is.True);
                    Assert.That(
                        predictionTransform.position.x,
                        Is.EqualTo(renderSamplesMillimeters[frame] * 0.001f).
                            Within(0.0002f),
                        "Scene registry必须精确提交prediction owner给出的同帧表现pose。");
                    camera.Tick(actors);
                    Assert.That(
                        followProxy.position,
                        Is.EqualTo(predictionTransform.position),
                        "Camera必须跟随同一条prediction表现时间轴。");
                }

                predictionMovingSample = Presentation(
                    local: Actor(
                        entityID: 2,
                        entityGeneration: 3,
                        local: true,
                        x: 600,
                        y: 0,
                        z: 0,
                        yaw: 0,
                        velocityX: 3000),
                    remote: Actor(
                        entityID: 3,
                        entityGeneration: 1,
                        local: false,
                        x: 4000,
                        y: 5000,
                        z: 6000,
                        yaw: 180000),
                    correctionVisible: false,
                    cameraMode: ClientBattleCameraMode.Exploration);
                Assert.That(actors.TryApply(predictionMovingSample), Is.True);
                AssertPosition(predictionTransform, 0.6f, 0f, 0f);

                var stoppedSample = Presentation(
                    local: Actor(
                        entityID: 2,
                        entityGeneration: 3,
                        local: true,
                        x: 600,
                        y: 0,
                        z: 0,
                        yaw: 0),
                    remote: Actor(
                        entityID: 3,
                        entityGeneration: 1,
                        local: false,
                        x: 4000,
                        y: 5000,
                        z: 6000,
                        yaw: 180000),
                    correctionVisible: false,
                    cameraMode: ClientBattleCameraMode.Exploration);
                Assert.That(actors.TryApply(stoppedSample), Is.True);
                for (var frame = 0; frame < 30; frame++)
                {
                    Assert.That(actors.TryApply(stoppedSample), Is.True);
                    Assert.That(
                        predictionTransform.position.x,
                        Is.EqualTo(0.6f).Within(0.0001f),
                        "Neutral Move后Scene不得自行积分、回摆或追赶另一条时间轴。");
                }

                Assert.That(
                    predictionTransform.position.x,
                    Is.EqualTo(0.6f).Within(0.001f),
                    "Local view必须只收敛到最终prediction position并稳定停止。");

                var successor = Presentation(
                    local: null,
                    remote: Actor(
                        entityID: 3,
                        entityGeneration: 2,
                        local: false,
                        x: 7000,
                        y: 8000,
                        z: 9000,
                        yaw: 270000),
                    correctionVisible: false,
                    cameraMode: ClientBattleCameraMode.Exploration);
                Assert.That(actors.TryApply(successor), Is.True);
                yield return null;

                Assert.That(actorRoot.transform.childCount, Is.EqualTo(1));
                Assert.That(actors.TryGetActorTransform(2, out _), Is.False);
                Assert.That(
                    actors.TryGetActorTransform(3, out var successorRemote),
                    Is.True);
                Assert.That(
                    successorRemote.name,
                    Is.EqualTo("ActorView-3-2"));
                AssertPosition(successorRemote, 7f, 8f, 9f);

                hud.Unbind();
                camera.Unbind();
                actors.Unbind();
                yield return null;
                Assert.That(actorRoot.transform.childCount, Is.Zero);
                Assert.That(canvasGroup.alpha, Is.Zero);
                Assert.That(ReadText(statusText), Is.Empty);
                Assert.That(ReadText(healthText), Is.Empty);
                Assert.That(ReadText(weaponText), Is.Empty);
                Assert.That(ReadText(skillText), Is.Empty);
                Assert.That(ReadText(bossHealthText), Is.Empty);
                Assert.That(ReadText(bossStateText), Is.Empty);

                hud.Apply(initial.Hud);
                camera.Apply(initial.Camera, actors);
                Assert.That(actors.TryApply(initial), Is.False);
                Assert.That(canvasGroup.alpha, Is.Zero);
                Assert.That(ReadText(statusText), Is.Empty);
                Assert.That(ReadText(healthText), Is.Empty);
                Assert.That(ReadText(weaponText), Is.Empty);
                Assert.That(ReadText(skillText), Is.Empty);
                Assert.That(ReadText(bossHealthText), Is.Empty);
                Assert.That(ReadText(bossStateText), Is.Empty);
                Assert.That(exploration.activeSelf, Is.True);
            }
            finally
            {
                UnityEngine.Object.DestroyImmediate(root);
            }
        }

        /// <summary>
        /// 验证projectile Prefab与damage cue按catalog创建，并在generation replacement后零迟到回写。
        /// </summary>
        [UnityTest]
        public IEnumerator ProjectileAndDamageCueAreRetiredWithBattleGeneration()
        {
            var root = new GameObject("CombatCatalogFixture");
            root.SetActive(false);
            var actorRoot = CreateChild(root, "ActorRoot");
            var projectileTemplate = CreateChild(root, "ProjectileTemplate");
            var effectTemplate = CreateChild(root, "DamageEffectTemplate");
            var particle = effectTemplate.AddComponent<ParticleSystem>();
            var main = particle.main;
            main.playOnAwake = false;
            var catalog = ScriptableObject.CreateInstance<ClientCombatResourceCatalog>();
            SetPrivateField(catalog, "_fanProjectileDisplay", projectileTemplate);
            SetPrivateField(catalog, "_damageVfx", effectTemplate);
            var registry = root.AddComponent<ClientActorViewRegistry>();
            SetPrivateField(registry, "_resourceCatalog", catalog);
            SetPrivateField(registry, "_actorRoot", actorRoot.transform);
            SetPrivateField(registry, "_sceneGeneration", 1L);
            SetPrivateField(registry, "_battleGeneration", 21L);

            var first = new ClientActorViewState(
                battleGeneration: 21,
                entityID: 10,
                entityGeneration: 1,
                local: false,
                new ClientBattleTransform(0, 0, 0, 0, 0, 0, 0),
                healthMilli: 1,
                maxHealthMilli: 1,
                stateFlags: 0,
                archetypeID: ClientBattleContentIdentity.FanProjectileArchetype,
                equippedWeaponID: 0,
                degraded: false);
            var target = new ClientActorViewState(
                battleGeneration: 21,
                entityID: 11,
                entityGeneration: 1,
                local: false,
                new ClientBattleTransform(1000, 0, 0, 0, 0, 0, 0),
                healthMilli: 1,
                maxHealthMilli: 1,
                stateFlags: 0,
                archetypeID: ClientBattleContentIdentity.FanProjectileArchetype,
                equippedWeaponID: 0,
                degraded: false);
            var hud = new ClientBattleHudViewState(
                battleGeneration: 21,
                ClientBattleAvailability.Active,
                localHealthMilli: 1,
                localMaxHealthMilli: 1,
                equippedWeaponID: ClientBattleContentIdentity.SwordWeapon,
                bossHealthMilli: 0,
                bossMaxHealthMilli: 0,
                bossPhase: 0,
                bossDead: false,
                correctionVisible: false,
                inputEnabled: true,
                degraded: false);
            var cue = new ClientGameplayCue(
                battleGeneration: 21,
                eventID: 7,
                sourceEntityID: 10,
                sourceEntityGeneration: 1,
                abilityID: ClientBattleContentIdentity.FanAbility,
                ClientBattleAbilityPhase.Committed,
                new ulong[] { 11 });
            var presentation = new ClientGameplayPresentationState(
                new[] { first, target },
                hud,
                new[] { cue },
                new ClientBattleCameraIntent(
                    battleGeneration: 21,
                    ClientBattleCameraMode.RangedAim,
                    followEntityID: 10,
                    impulseEventID: 0));

            try
            {
                root.SetActive(true);
                Assert.That(registry.TryApply(presentation), Is.True);
                Assert.That(actorRoot.transform.childCount, Is.EqualTo(3));
                var effect = actorRoot.transform.Find("DamageVfx-11");
                Assert.That(effect, Is.Not.Null);
                Assert.That(
                    effect.GetComponent<ParticleSystem>().isPlaying,
                    Is.True);

                Assert.That(registry.TryApply(presentation), Is.True);
                Assert.That(
                    actorRoot.transform.Cast<Transform>()
                        .Count(child => child.name == "DamageVfx-11"),
                    Is.EqualTo(1),
                    "同一 reliable cue 被重复播放。");

                registry.ReplaceBattleGeneration(
                    battleGeneration: 22,
                    preserveActors: false);
                yield return null;
                Assert.That(actorRoot.transform.childCount, Is.Zero);
                Assert.That(registry.TryApply(presentation), Is.False);
                Assert.That(actorRoot.transform.childCount, Is.Zero);
            }
            finally
            {
                registry.Unbind();
                UnityEngine.Object.DestroyImmediate(catalog);
                UnityEngine.Object.DestroyImmediate(root);
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

        /// <summary>创建测试 root 下的 Scene-owned 子对象。</summary>
        /// <param name="root">测试 fixture root。</param>
        /// <param name="name">子对象名称。</param>
        /// <returns>保持 activeSelf 的测试对象。</returns>
        private static GameObject CreateChild(GameObject root, string name)
        {
            var child = new GameObject(name);
            child.transform.SetParent(root.transform, worldPositionStays: false);
            return child;
        }

        /// <summary>为程序化 Scene fixture 设置生产代码的 serialized direct reference。</summary>
        /// <typeparam name="T">Serialized field value 类型。</typeparam>
        /// <param name="target">包含 serialized field 的组件。</param>
        /// <param name="fieldName">Private serialized field 名称。</param>
        /// <param name="value">测试 direct reference。</param>
        private static void SetPrivateField<T>(
            object target,
            string fieldName,
            T value)
        {
            var field = target.GetType().GetField(
                fieldName,
                BindingFlags.Instance | BindingFlags.NonPublic);
            Assert.That(field, Is.Not.Null, $"缺少 serialized field：{fieldName}");
            field.SetValue(target, value);
        }

        /// <summary>读取测试对象的private field以验证有界状态。</summary>
        /// <typeparam name="T">Private field value类型。</typeparam>
        /// <param name="target">包含被测field的对象。</param>
        /// <param name="fieldName">Private field名称。</param>
        /// <returns>被测field的current value。</returns>
        private static T GetPrivateField<T>(object target, string fieldName)
        {
            var field = target.GetType().GetField(
                fieldName,
                BindingFlags.Instance | BindingFlags.NonPublic);
            Assert.That(field, Is.Not.Null, $"缺少private field：{fieldName}");
            return (T)field.GetValue(target);
        }

        /// <summary>以毫米到米换算允许的浮点容差验证 Actor Transform。</summary>
        /// <param name="target">被测 Actor Transform。</param>
        /// <param name="x">期望米位置 X。</param>
        /// <param name="y">期望米位置 Y。</param>
        /// <param name="z">期望米位置 Z。</param>
        private static void AssertPosition(
            Transform target,
            float x,
            float y,
            float z)
        {
            Assert.That(target.position.x, Is.EqualTo(x).Within(0.0001f));
            Assert.That(target.position.y, Is.EqualTo(y).Within(0.0001f));
            Assert.That(target.position.z, Is.EqualTo(z).Within(0.0001f));
        }

        /// <summary>通过已加载的 TextMeshPro runtime 类型创建测试文本，不向测试 asmdef 增加 UI owner。</summary>
        /// <param name="target">承载 TMP component 的 Scene object。</param>
        /// <returns>可直接赋给 `TMP_Text` serialized field 的组件。</returns>
        private static Component AddTextComponent(GameObject target)
        {
            var textType = Type.GetType(
                "TMPro.TextMeshProUGUI, Unity.TextMeshPro",
                throwOnError: false);
            Assert.That(textType, Is.Not.Null, "Unity.TextMeshPro runtime assembly 未加载。");
            return target.AddComponent(textType);
        }

        /// <summary>读取测试 TMP component 的当前文本。</summary>
        /// <param name="component">TextMeshProUGUI component。</param>
        /// <returns>HUD Host 已提交的字符串。</returns>
        private static string ReadText(Component component)
        {
            var property = component.GetType().GetProperty(
                "text",
                BindingFlags.Instance | BindingFlags.Public);
            Assert.That(property, Is.Not.Null, "TMP_Text.text property 不存在。");
            return property.GetValue(component) as string;
        }

        /// <summary>确认 current Battle HUD 文案完全由 Basic Latin printable glyph组成。</summary>
        /// <param name="value">待验证的 TMP 文本。</param>
        private static void AssertBasicLatin(string value)
        {
            Assert.That(value, Is.Not.Null);
            foreach (var character in value)
            {
                var codePoint = (int)character;
                Assert.That(
                    codePoint,
                    Is.InRange((int)' ', (int)'~'),
                    $"HUD text contains a non-Basic-Latin glyph: U+{codePoint:X4}");
            }
        }

        /// <summary>创建一个明确标识 local prediction 或 remote interpolation 来源的 actor state。</summary>
        /// <param name="entityID">Entity identity。</param>
        /// <param name="entityGeneration">Lifecycle generation。</param>
        /// <param name="local">是否来自 local prediction。</param>
        /// <param name="x">毫米位置 X。</param>
        /// <param name="y">毫米位置 Y。</param>
        /// <param name="z">毫米位置 Z。</param>
        /// <param name="yaw">毫度 yaw。</param>
        /// <param name="velocityX">毫米/秒 world X速度。</param>
        /// <param name="velocityY">毫米/秒 world Y速度。</param>
        /// <param name="velocityZ">毫米/秒 world Z速度。</param>
        /// <returns>Battle generation 21 的 immutable actor state。</returns>
        private static ClientActorViewState Actor(
            ulong entityID,
            uint entityGeneration,
            bool local,
            int x,
            int y,
            int z,
            int yaw,
            int velocityX = 0,
            int velocityY = 0,
            int velocityZ = 0)
        {
            return new ClientActorViewState(
                battleGeneration: 21,
                entityID,
                entityGeneration,
                local,
                new ClientBattleTransform(
                    x,
                    y,
                    z,
                    yaw,
                    velocityXMillimetersPerSecond: velocityX,
                    velocityYMillimetersPerSecond: velocityY,
                    velocityZMillimetersPerSecond: velocityZ),
                healthMilli: local ? 48500u : 100000u,
                stateFlags: 0,
                degraded: false);
        }

        /// <summary>创建 Actor、HUD 与 Camera 使用的同 generation immutable presentation。</summary>
        /// <param name="local">可选 local prediction actor。</param>
        /// <param name="remote">Remote interpolation actor。</param>
        /// <param name="correctionVisible">HUD 是否显示位置校正。</param>
        /// <param name="cameraMode">Camera intent mode。</param>
        /// <returns>Battle generation 21 的一致 presentation state。</returns>
        private static ClientGameplayPresentationState Presentation(
            ClientActorViewState local,
            ClientActorViewState remote,
            bool correctionVisible,
            ClientBattleCameraMode cameraMode)
        {
            var actorStates = local == null
                ? new[] { remote }
                : new[] { local, remote };
            var followEntityID = local?.EntityID ?? remote.EntityID;
            return new ClientGameplayPresentationState(
                actorStates,
                new ClientBattleHudViewState(
                    battleGeneration: 21,
                    ClientBattleAvailability.Active,
                    localHealthMilli: local?.HealthMilli ?? 0,
                    correctionVisible,
                    inputEnabled: true,
                    degraded: false),
                Array.Empty<ClientGameplayCue>(),
                new ClientBattleCameraIntent(
                    battleGeneration: 21,
                    cameraMode,
                    followEntityID,
                    impulseEventID: 0));
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
