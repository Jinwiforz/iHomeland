using System.Collections;
using System.Collections.Generic;
using System.Reflection;
using System.Text.RegularExpressions;
using System.Threading;
using IHomeland.Client.Core.Bootstrap;
using IHomeland.Client.Core.Composition;
using IHomeland.Client.Core.Configuration;
using IHomeland.Client.Core.Lifetime;
using IHomeland.Client.Scenes.Contexts;
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
            foreach (var root in roots)
            {
                if (root != null)
                {
                    Object.Destroy(root.gameObject);
                }
            }

            foreach (var inputAsset in _inputAssets)
            {
                if (inputAsset != null)
                {
                    Object.Destroy(inputAsset);
                }
            }

            _inputAssets.Clear();

            yield return null;
        }

        /// <summary>
        /// 保护重复 bootstrap 在创建第二套 Composition 前销毁重复入口。
        /// </summary>
        /// <returns>等待两个 Awake 和延迟 Destroy 完成的枚举器。</returns>
        [UnityTest]
        public IEnumerator DuplicateBootstrapKeepsOneRunningRoot()
        {
            var first = CreateBootstrapRoot("PrimaryAppRoot");
            yield return null;
            var duplicate = CreateBootstrapRoot("DuplicateAppRoot");
            yield return null;

            var roots = Object.FindObjectsByType<AppRoot>(FindObjectsInactive.Include);
            Assert.That(roots, Has.Length.EqualTo(1));
            Assert.That(roots[0], Is.SameAs(first));
            Assert.That(first.State, Is.EqualTo(AppLifetimeState.Running));
            Assert.That(duplicate == null, Is.True);
        }

        /// <summary>
        /// 保护 AppRoot 在活动场景切换后仍由 DontDestroyOnLoad 场景持有。
        /// </summary>
        /// <returns>等待场景切换和卸载完成的枚举器。</returns>
        [UnityTest]
        public IEnumerator RunningRootSurvivesActiveSceneReplacement()
        {
            var originalScene = SceneManager.GetActiveScene();
            var root = CreateBootstrapRoot("PersistentAppRoot");
            yield return null;
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
            var first = CreateBootstrapRoot("FirstRunRoot");
            yield return null;
            Object.Destroy(first.gameObject);
            yield return null;
            yield return null;

            var second = CreateBootstrapRoot("SecondRunRoot");
            yield return null;

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
            yield return null;

            Assert.That(bootstrap.enabled, Is.False);
            Assert.That(root.State, Is.EqualTo(AppLifetimeState.Created));
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
            var bootstrap = gameObject.AddComponent<AppBootstrap>();
            SetPrivateField(bootstrap, "_appRoot", root);
            SetPrivateField(bootstrap, "_environmentProfile", profile);
            SetPrivateField(bootstrap, "_uiHostRoot", uiHostRoot);
            LogAssert.Expect(
                LogType.Exception,
                new Regex("Production HTTP base URI 必须使用 HTTPS", RegexOptions.CultureInvariant));

            gameObject.SetActive(true);
            yield return null;
            yield return null;

            Assert.That(root == null, Is.True);
            var replacement = CreateBootstrapRoot("ReplacementAfterInvalidEnvironment");
            yield return null;
            Assert.That(replacement.State, Is.EqualTo(AppLifetimeState.Running));
            Object.Destroy(profile);
        }

        /// <summary>
        /// 创建一个以直接引用接线、激活后立即 bootstrap 的测试 GameObject。
        /// </summary>
        /// <param name="name">用于诊断场景层级的 GameObject 名称。</param>
        /// <returns>已经激活并开始启动的 AppRoot。</returns>
        private AppRoot CreateBootstrapRoot(string name)
        {
            var gameObject = new GameObject(name);
            gameObject.SetActive(false);
            var root = gameObject.AddComponent<AppRoot>();
            var uiHostRoot = AddUiHostRoot(gameObject);
            var bootstrap = gameObject.AddComponent<AppBootstrap>();
            bootstrap.ConfigureBeforeActivation(root, uiHostRoot, CreateTestEnvironment());
            gameObject.SetActive(true);
            return root;
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
            inputActions.AddActionMap("Player").AddAction("Move");
            inputActions.AddActionMap("UI").AddAction("Navigate");
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
