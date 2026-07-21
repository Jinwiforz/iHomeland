using System;
using System.Collections;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Presentation.Hosts.UGUI;
using IHomeland.Client.Presentation.Hosts.UIToolkit;
using IHomeland.Client.Presentation.Navigation;
using NUnit.Framework;
using UnityEngine;
using UnityEngine.EventSystems;
using UnityEngine.TestTools;
using UnityEngine.UIElements;

namespace IHomeland.Client.Tests.PlayMode
{
    /// <summary>验证产品 binding 在通用 uGUI Host 失败回滚与销毁路径中的对称生命周期。</summary>
    public sealed class ClientProductHostPlayModeTests
    {
        /// <summary>验证首次 bind 失败不会污染 Host，后续成功 bind 在 dispose 时只解绑一次。</summary>
        [Test]
        public async Task FailedProductBindCanRetryAndDisposeUnbindsExactlyOnce()
        {
            var root = new GameObject("ProductHostFixture");
            root.SetActive(false);
            try
            {
                var canvas = root.AddComponent<Canvas>();
                var eventSystem = root.AddComponent<EventSystem>();
                var binding = root.AddComponent<ScriptedProductBinding>();
                binding.FailFirstBind = true;
                var host = root.AddComponent<ClientUguiHost>();
                var content = new GameObject("CanvasContent");
                content.transform.SetParent(root.transform, worldPositionStays: false);
                var canvasGroup = content.AddComponent<CanvasGroup>();
                host.ConfigureBeforeActivation(
                    ClientUiRouteId.WorldHud,
                    canvas,
                    canvasGroup,
                    eventSystem,
                    defaultFocus: null);
                host.ConfigureProductBindingBeforeActivation(binding);
                host.ConfigureProductContext(new FixtureProductContext());
                root.SetActive(true);

                var viewHost = (IClientUiViewHost)host;
                await viewHost.InitializeAsync(CancellationToken.None);
                var firstBinding = new ClientUiRouteBinding(
                    ClientUiRouteId.WorldHud,
                    navigationGeneration: 1,
                    sceneGeneration: 1,
                    CancellationToken.None);
                Assert.ThrowsAsync<InvalidOperationException>(async () =>
                    await viewHost.BindAsync(firstBinding, CancellationToken.None));

                var secondBinding = new ClientUiRouteBinding(
                    ClientUiRouteId.WorldHud,
                    navigationGeneration: 2,
                    sceneGeneration: 1,
                    CancellationToken.None);
                await viewHost.BindAsync(secondBinding, CancellationToken.None);
                await viewHost.DisposeAsync(CancellationToken.None);

                Assert.That(binding.BindCalls, Is.EqualTo(2));
                Assert.That(binding.UnbindCalls, Is.EqualTo(1));
            }
            finally
            {
                UnityEngine.Object.DestroyImmediate(root);
            }
        }

        /// <summary>
        /// 验证 UI Toolkit Host 重复 bind/unbind 后只保留 current generation 的一个产品 command callback。
        /// </summary>
        /// <returns>等待 PanelRenderer 建立运行时 panel 的枚举器。</returns>
        [UnityTest]
        public IEnumerator ToolkitRepeatedBindHideAndTeardownDoNotDuplicateProductCommand()
        {
            var panelSettings = ScriptableObject.CreateInstance<PanelSettings>();
            var themeStyleSheet = ScriptableObject.CreateInstance<ThemeStyleSheet>();
            var visualTreeAsset = ScriptableObject.CreateInstance<VisualTreeAsset>();
            var root = new GameObject("ToolkitProductHostFixture");
            root.SetActive(false);
            try
            {
                panelSettings.themeStyleSheet = themeStyleSheet;
                var panelRenderer = root.AddComponent<PanelRenderer>();
                panelRenderer.panelSettings = panelSettings;
                panelRenderer.visualTreeAsset = visualTreeAsset;
                VisualElement panelRoot = null;
                panelRenderer.RegisterUIReloadCallback((_, value, __) => panelRoot = value);

                var binding = root.AddComponent<ScriptedProductBinding>();
                var host = root.AddComponent<ClientUiToolkitHost>();
                host.ConfigureBeforeActivation(ClientUiRouteId.Login, panelRenderer, string.Empty);
                host.ConfigureProductBindingBeforeActivation(binding);
                host.ConfigureProductContext(new FixtureProductContext());
                root.SetActive(true);

                // 复现 Player 启动顺序：AppBootstrap 会在 PanelRenderer 首次 reload 前立即打开 Login。
                var viewHost = (IClientUiViewHost)host;
                var initialization = viewHost.InitializeAsync(CancellationToken.None);
                const int panelReadyFrameBudget = 10;
                for (var frame = 0; frame < panelReadyFrameBudget && !initialization.IsCompleted; frame++)
                {
                    yield return null;
                }

                Assert.That(panelRoot, Is.Not.Null);
                Assert.That(
                    initialization.IsCompletedSuccessfully,
                    Is.True,
                    initialization.Exception?.ToString());

                var firstBinding = new ClientUiRouteBinding(
                    ClientUiRouteId.Login,
                    navigationGeneration: 1,
                    sceneGeneration: 0,
                    CancellationToken.None);
                Assert.That(viewHost.BindAsync(firstBinding, CancellationToken.None).IsCompletedSuccessfully, Is.True);
                Assert.That(viewHost.ShowAsync(ClientUiLayer.Screen, CancellationToken.None).IsCompletedSuccessfully, Is.True);
                Assert.That(viewHost.SetInteractiveAsync(true, CancellationToken.None).IsCompletedSuccessfully, Is.True);
                binding.DispatchCommand();
                Assert.That(binding.CommandCalls, Is.EqualTo(1));

                Assert.That(viewHost.HideAsync(CancellationToken.None).IsCompletedSuccessfully, Is.True);
                Assert.That(panelRoot.enabledSelf, Is.False);
                Assert.That(viewHost.UnbindAsync(CancellationToken.None).IsCompletedSuccessfully, Is.True);
                binding.DispatchCommand();
                Assert.That(binding.CommandCalls, Is.EqualTo(1));

                var secondBinding = new ClientUiRouteBinding(
                    ClientUiRouteId.Login,
                    navigationGeneration: 2,
                    sceneGeneration: 0,
                    CancellationToken.None);
                Assert.That(viewHost.BindAsync(secondBinding, CancellationToken.None).IsCompletedSuccessfully, Is.True);
                binding.DispatchCommand();
                Assert.That(binding.CommandCalls, Is.EqualTo(2));

                Assert.That(viewHost.DisposeAsync(CancellationToken.None).IsCompletedSuccessfully, Is.True);
                binding.DispatchCommand();
                Assert.That(binding.CommandCalls, Is.EqualTo(2));
                Assert.That(binding.BindCalls, Is.EqualTo(2));
                Assert.That(binding.UnbindCalls, Is.EqualTo(2));
            }
            finally
            {
                UnityEngine.Object.DestroyImmediate(root);
                UnityEngine.Object.DestroyImmediate(visualTreeAsset);
                UnityEngine.Object.DestroyImmediate(themeStyleSheet);
                UnityEngine.Object.DestroyImmediate(panelSettings);
            }
        }

        /// <summary>提供不暴露任何业务状态的产品上下文 fixture。</summary>
        private sealed class FixtureProductContext : IClientUiProductContext
        {
        }

        /// <summary>首次 bind 失败、后续成功的产品 binding fixture。</summary>
        private sealed class ScriptedProductBinding : ClientUiProductBindingBehaviour, IClientUiProductBinding
        {
            /// <summary>保存只在当前成功 binding 存活期间登记的 command callback。</summary>
            private event Action CommandRequested;

            /// <summary>指示首次 bind 是否注入失败。</summary>
            internal bool FailFirstBind { get; set; }

            /// <summary>获取 bind 调用次数。</summary>
            internal int BindCalls { get; private set; }

            /// <summary>获取 unbind 调用次数。</summary>
            internal int UnbindCalls { get; private set; }

            /// <summary>获取实际交付给产品 command 的调用次数。</summary>
            internal int CommandCalls { get; private set; }

            /// <summary>验证 fixture context 并保存一次配置事实。</summary>
            /// <param name="context">测试产品上下文。</param>
            public void Configure(IClientUiProductContext context)
            {
                if (!(context is FixtureProductContext))
                {
                    throw new ArgumentException("Fixture 只接受测试产品上下文。", nameof(context));
                }
            }

            /// <summary>首次返回失败，第二次及以后成功。</summary>
            /// <param name="binding">当前 route binding。</param>
            /// <param name="cancellationToken">测试取消信号。</param>
            /// <returns>确定性完成任务。</returns>
            public Task BindAsync(
                ClientUiRouteBinding binding,
                CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                BindCalls++;
                if (FailFirstBind && BindCalls == 1)
                {
                    return Task.FromException(new InvalidOperationException("scripted bind failure"));
                }

                CommandRequested += OnCommandRequested;
                return Task.CompletedTask;
            }

            /// <summary>记录一次对称解绑。</summary>
            /// <param name="cancellationToken">测试取消信号。</param>
            /// <returns>已记录时完成。</returns>
            public Task UnbindAsync(CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                UnbindCalls++;
                CommandRequested -= OnCommandRequested;
                return Task.CompletedTask;
            }

            /// <summary>模拟一次由产品控件发起的 command。</summary>
            internal void DispatchCommand()
            {
                CommandRequested?.Invoke();
            }

            /// <summary>记录 current binding 实际收到的一次 command。</summary>
            private void OnCommandRequested()
            {
                CommandCalls++;
            }
        }
    }
}
