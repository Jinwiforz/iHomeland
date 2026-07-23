using System.Collections;
using System.Collections.Generic;
using System.Reflection;
using System.Text.RegularExpressions;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Core.Bootstrap;
using IHomeland.Client.Core.Composition;
using IHomeland.Client.Application.Configuration;
using IHomeland.Client.Core.Configuration;
using IHomeland.Client.Foundation.Lifetime;
using IHomeland.Client.Scenes.Contexts;
using IHomeland.Client.Scenes.PersonalWorld;
using IHomeland.Client.Presentation.Hosts;
using IHomeland.Client.Presentation.Hosts.UGUI;
using IHomeland.Client.Presentation.Hosts.UIToolkit;
using NUnit.Framework;
using UnityEngine;
using UnityEngine.SceneManagement;
using UnityEngine.TestTools;
using UnityEngine.InputSystem;

namespace IHomeland.Client.Tests.PlayMode
{
    /// <summary>
    /// 通过实际 GameObject、Update 和 SceneManager 验证 AppRoot Unity Host 生命周期。
    /// </summary>
    public sealed class AppRootPlayModeTests
    {
        /// <summary>保存测试创建且必须显式销毁的 InputActionAsset 模板。</summary>
        private readonly List<InputActionAsset> _inputAssets = new List<InputActionAsset>();

        /// <summary>
        /// 每个测试后销毁所有 AppRoot，验证 OnDestroy 清理并隔离静态唯一性状态。
        /// </summary>
        /// <returns>等待 Unity 完成延迟 Destroy 和生命周期 callback 的枚举器。</returns>
        [UnityTearDown]
        public IEnumerator TearDownRoots()
        {
            var roots = Object.FindObjectsByType<AppRoot>(FindObjectsInactive.Include);
            var stops = new List<Task>();
            foreach (var root in roots)
            {
                if (root != null)
                {
                    stops.Add(root.StopAsync());
                }
            }

            while (stops.Exists(stop => !stop.IsCompleted))
            {
                yield return null;
            }

            foreach (var stop in stops)
            {
                Assert.That(stop.IsCanceled, Is.False, "测试清理不应取消 App Scope 停止事务。");
                Assert.That(stop.IsFaulted, Is.False, stop.Exception?.ToString());
            }

            Assert.That(AppRoot.QualificationClaimedOwnerCount, Is.Zero);

            foreach (var root in roots)
            {
                if (root != null)
                {
                    Object.DestroyImmediate(root.gameObject);
                }
            }

            foreach (var inputAsset in _inputAssets)
            {
                if (inputAsset != null)
                {
                    Object.DestroyImmediate(inputAsset);
                }
            }

            _inputAssets.Clear();
        }

        /// <summary>
        /// 保护重复 bootstrap 在创建第二套 Composition 前销毁重复入口。
        /// </summary>
        /// <returns>等待两个 Awake 和延迟 Destroy 完成的枚举器。</returns>
        [UnityTest]
        public IEnumerator DuplicateBootstrapKeepsOneRunningRoot()
        {
            var first = CreateBootstrapRoot("PrimaryAppRoot", out var firstBootstrap);
            yield return WaitForSuccessfulTask(firstBootstrap.StartupCompletion);
            _ = CreateBootstrapRoot("DuplicateAppRoot", out var duplicateBootstrap);
            yield return WaitForSuccessfulTask(duplicateBootstrap.StartupCompletion);

            Assert.That(AppRoot.QualificationClaimedOwnerCount, Is.EqualTo(1));
            Assert.That(first.State, Is.EqualTo(AppLifetimeState.Running));
        }

        /// <summary>
        /// 保护 AppRoot 在活动场景切换后仍由 DontDestroyOnLoad 场景持有。
        /// </summary>
        /// <returns>等待场景切换和卸载完成的枚举器。</returns>
        [UnityTest]
        public IEnumerator RunningRootSurvivesActiveSceneReplacement()
        {
            var originalScene = SceneManager.GetActiveScene();
            var root = CreateBootstrapRoot("PersistentAppRoot", out var bootstrap);
            yield return WaitForSuccessfulTask(bootstrap.StartupCompletion);
            var replacementScene = SceneManager.CreateScene("RuntimeReplacementScene");

            Assert.That(SceneManager.SetActiveScene(replacementScene), Is.True);
            yield return null;

            Assert.That(root, Is.Not.Null);
            Assert.That(root.State, Is.EqualTo(AppLifetimeState.Running));
            Assert.That(root.gameObject.scene.name, Is.EqualTo("DontDestroyOnLoad"));

            Assert.That(SceneManager.SetActiveScene(originalScene), Is.True);
            yield return SceneManager.UnloadSceneAsync(replacementScene);
        }

        /// <summary>
        /// 保护 AppRoot Update 同时有界 drain callback 并仅驱动 Composition 登记的 tickable。
        /// </summary>
        /// <returns>等待一个实际 Unity Update 执行的枚举器。</returns>
        [UnityTest]
        public IEnumerator UpdateDrivesDispatcherAndRegisteredTickable()
        {
            var gameObject = new GameObject("DrivenAppRoot");
            var root = gameObject.AddComponent<AppRoot>();
            Assert.That(root.TryClaim(), Is.True);

            var dispatcher = new MainThreadDispatcher(Thread.CurrentThread.ManagedThreadId, capacity: 4);
            var sceneLifetimeOwner = new SceneLifetimeOwner();
            var tickable = new CountingTickable();
            IAppLifetimeParticipant[] participants = { dispatcher, sceneLifetimeOwner };
            var lifetime = new AppLifetime(
                participants,
                System.TimeSpan.FromSeconds(2),
                System.TimeSpan.FromSeconds(2));
            var composition = new AppCompositionResult(
                lifetime,
                dispatcher,
                new IAppTickable[] { tickable },
                maximumDispatchesPerFrame: 2);
            root.Attach(composition);
            var startup = root.StartAsync();
            Assert.That(startup.IsCompletedSuccessfully, Is.True);

            var callbackExecuted = false;
            Assert.That(
                dispatcher.TryPost(() => callbackExecuted = true),
                Is.EqualTo(DispatchPostResult.Accepted));
            yield return null;

            Assert.That(callbackExecuted, Is.True);
            Assert.That(tickable.TickCount, Is.GreaterThanOrEqualTo(1));
        }

        /// <summary>
        /// 保护销毁旧 root 后可在同一进程创建下一套对象图，验证静态 claim 的对称释放。
        /// </summary>
        /// <returns>等待两轮 Destroy 和 Awake callback 完成的枚举器。</returns>
        [UnityTest]
        public IEnumerator DestroyedRootReleasesClaimForNextRun()
        {
            var first = CreateBootstrapRoot("FirstRunRoot", out var firstBootstrap);
            yield return WaitForSuccessfulTask(firstBootstrap.StartupCompletion);
            var stop = first.StopAsync();
            yield return WaitForSuccessfulTask(stop);

            Assert.That(AppRoot.QualificationClaimedOwnerCount, Is.Zero);
            Object.DestroyImmediate(first.gameObject);

            var second = CreateBootstrapRoot("SecondRunRoot", out var secondBootstrap);
            yield return WaitForSuccessfulTask(secondBootstrap.StartupCompletion);

            Assert.That(second, Is.Not.Null);
            Assert.That(second.State, Is.EqualTo(AppLifetimeState.Running));
            var roots = Object.FindObjectsByType<AppRoot>(FindObjectsInactive.Include);
            Assert.That(roots, Has.Length.EqualTo(1));
            Assert.That(roots[0], Is.SameAs(second));
        }

        /// <summary>
        /// 验证缺失环境 profile 会在争用唯一 root 前安全停用 bootstrap。
        /// </summary>
        /// <returns>等待 Awake 完成并观察无网络对象图启动的枚举器。</returns>
        [UnityTest]
        public IEnumerator MissingEnvironmentProfileFailsBeforeRootClaim()
        {
            var gameObject = new GameObject("MissingEnvironmentRoot");
            gameObject.SetActive(false);
            var root = gameObject.AddComponent<AppRoot>();
            var bootstrap = gameObject.AddComponent<AppBootstrap>();
            SetPrivateField(bootstrap, "_appRoot", root);
            LogAssert.Expect(
                LogType.Error,
                "AppBootstrap 缺少 ClientEnvironmentProfile 直接序列化引用。");

            gameObject.SetActive(true);
            yield return WaitForSuccessfulTask(bootstrap.StartupCompletion);

            Assert.That(bootstrap.enabled, Is.False);
            Assert.That(root.State, Is.EqualTo(AppLifetimeState.Created));
            Assert.That(AppRoot.QualificationClaimedOwnerCount, Is.Zero);
        }

        /// <summary>
        /// 验证非法 Production 明文 profile 在构造对象图前失败并释放唯一 root claim。
        /// </summary>
        /// <returns>等待失败回滚、延迟销毁与临时 ScriptableObject 清理的枚举器。</returns>
        [UnityTest]
        public IEnumerator InvalidEnvironmentProfileRollsBackClaim()
        {
            var profile = ScriptableObject.CreateInstance<ClientEnvironmentProfile>();
            SetPrivateField(profile, "_environmentKind", ClientEnvironmentKind.Production);
            SetPrivateField(profile, "_httpBaseUri", "http://127.0.0.1:8080/");
            var gameObject = new GameObject("InvalidEnvironmentRoot");
            gameObject.SetActive(false);
            var root = gameObject.AddComponent<AppRoot>();
            var uiHostRoot = AddUiHostRoot(gameObject);
            var sceneTransitionHost = gameObject.AddComponent<ClientWorldSceneTransitionHost>();
            var bootstrap = gameObject.AddComponent<AppBootstrap>();
            SetPrivateField(bootstrap, "_appRoot", root);
            SetPrivateField(bootstrap, "_environmentProfile", profile);
            SetPrivateField(bootstrap, "_uiHostRoot", uiHostRoot);
            SetPrivateField(bootstrap, "_sceneTransitionHost", sceneTransitionHost);
            LogAssert.Expect(
                LogType.Exception,
                new Regex("Production HTTP base URI 必须使用 HTTPS", RegexOptions.CultureInvariant));

            gameObject.SetActive(true);
            yield return WaitForSuccessfulTask(bootstrap.StartupCompletion);

            Assert.That(root.State, Is.EqualTo(AppLifetimeState.Created));
            Assert.That(AppRoot.QualificationClaimedOwnerCount, Is.Zero);
            Object.DestroyImmediate(gameObject);
            var replacement = CreateBootstrapRoot(
                "ReplacementAfterInvalidEnvironment",
                out var replacementBootstrap);
            yield return WaitForSuccessfulTask(replacementBootstrap.StartupCompletion);
            Assert.That(replacement.State, Is.EqualTo(AppLifetimeState.Running));
            Object.DestroyImmediate(profile);
        }

        /// <summary>
        /// 创建一个以直接引用接线、激活后立即 bootstrap 的测试 GameObject。
        /// </summary>
        /// <param name="name">用于诊断场景层级的 GameObject 名称。</param>
        /// <param name="bootstrap">返回公开当前真实启动事务的 Host。</param>
        /// <returns>已经激活并开始启动的 AppRoot。</returns>
        private AppRoot CreateBootstrapRoot(string name, out AppBootstrap bootstrap)
        {
            var gameObject = new GameObject(name);
            gameObject.SetActive(false);
            var root = gameObject.AddComponent<AppRoot>();
            var uiHostRoot = AddUiHostRoot(gameObject);
            bootstrap = gameObject.AddComponent<AppBootstrap>();
            bootstrap.ConfigureBeforeActivation(root, uiHostRoot, CreateTestEnvironment());
            gameObject.SetActive(true);
            return root;
        }

        /// <summary>等待被测生命周期事务的真实完成边界，并把取消或异常作为测试失败报告。</summary>
        /// <param name="operation">由产品生命周期对象返回的唯一事务。</param>
        /// <returns>事务成功完成时结束的 Unity 协程。</returns>
        private static IEnumerator WaitForSuccessfulTask(Task operation)
        {
            Assert.That(operation, Is.Not.Null);
            while (!operation.IsCompleted)
            {
                yield return null;
            }

            Assert.That(operation.IsCanceled, Is.False, "生命周期事务不应被意外取消。");
            Assert.That(operation.IsFaulted, Is.False, operation.Exception?.ToString());
        }

        /// <summary>
        /// 在测试 GameObject 上创建带最小 Player/UI action map 的唯一 UI/Input Host root。
        /// </summary>
        /// <param name="gameObject">尚未激活的测试 App Scope GameObject。</param>
        /// <returns>已完成激活前配置的 Host root。</returns>
        private ClientUiHostRoot AddUiHostRoot(GameObject gameObject)
        {
            var uiHostRoot = gameObject.AddComponent<ClientUiHostRoot>();
            var inputActions = ScriptableObject.CreateInstance<InputActionAsset>();
            var player = inputActions.AddActionMap("Player");
            player.AddAction("Move");
            player.AddAction("Menu", InputActionType.Button);
            var ui = inputActions.AddActionMap("UI");
            ui.AddAction("Navigate");
            ui.AddAction("Cancel", InputActionType.Button);
            _inputAssets.Add(inputActions);
            uiHostRoot.ConfigureBeforeActivation(
                inputActions,
                System.Array.Empty<ClientUiToolkitHost>(),
                System.Array.Empty<ClientUguiHost>());
            return uiHostRoot;
        }

        /// <summary>
        /// 创建不访问真实服务端的 loopback PlayMode 环境。
        /// </summary>
        /// <returns>协议版本为 1 的不可变测试环境。</returns>
        private static ClientEnvironment CreateTestEnvironment()
        {
            return ClientEnvironment.Create(
                ClientEnvironmentKind.Test,
                "http://127.0.0.1:8080/",
                "0.1.0",
                1);
        }

        /// <summary>
        /// 为序列化边界负向测试设置私有字段，不为 production 增加测试专用 API。
        /// </summary>
        /// <typeparam name="TTarget">声明字段的运行时类型。</typeparam>
        /// <typeparam name="TValue">待写入字段的值类型。</typeparam>
        /// <param name="target">待配置的测试对象。</param>
        /// <param name="fieldName">与序列化字段一致的私有名称。</param>
        /// <param name="value">测试需要模拟的序列化值。</param>
        private static void SetPrivateField<TTarget, TValue>(
            TTarget target,
            string fieldName,
            TValue value)
        {
            var field = typeof(TTarget).GetField(
                fieldName,
                BindingFlags.Instance | BindingFlags.NonPublic);
            Assert.That(field, Is.Not.Null, $"找不到测试字段 {fieldName}。");
            field.SetValue(target, value);
        }

        /// <summary>
        /// 记录由 AppRoot 实际 Update 驱动的 tick 次数。
        /// </summary>
        private sealed class CountingTickable : IAppTickable
        {
            /// <summary>
            /// 获取当前测试运行中收到的 tick 次数。
            /// </summary>
            internal int TickCount { get; private set; }

            /// <summary>
            /// 记录一次由唯一 AppRoot 发出的主线程 tick。
            /// </summary>
            /// <param name="unscaledDeltaTimeSeconds">Unity 提供的不缩放帧间隔，单位为秒。</param>
            public void Tick(float unscaledDeltaTimeSeconds)
            {
                Assert.That(unscaledDeltaTimeSeconds, Is.GreaterThanOrEqualTo(0f));
                TickCount++;
            }
        }
    }
}
