using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.AppShell.Presentation.Navigation;
using IHomeland.Client.PersonalWorld.Presentation;
using NUnit.Framework;

namespace IHomeland.Client.AppShell.Tests.EditMode
{
    /// <summary>验证 Router 与 Presentation 拆分组件的纯状态和容量契约。</summary>
    internal sealed class UiArchitectureModuleTests
    {
        /// <summary>验证 route generation 单调递增且 SceneBound 必须绑定有效 Scene。</summary>
        [Test]
        public void RouteStateAndPlannerPreserveGenerationAndScenePolicy()
        {
            var state = new ClientUiRouteState();
            var planner = new ClientUiTransitionPlanner();
            var sceneRoute = new ClientUiRouteDefinition(
                ClientUiRouteId.WorldHud,
                ClientUiFrameworkOwner.Ugui,
                ClientUiLayer.Hud,
                ClientUiInputMode.Gameplay,
                ClientUiLifecycle.SceneBound);
            var appRoute = new ClientUiRouteDefinition(
                ClientUiRouteId.Login,
                ClientUiFrameworkOwner.UiToolkit,
                ClientUiLayer.Screen,
                ClientUiInputMode.Text,
                ClientUiLifecycle.Cached);

            Assert.That(state.NextGeneration(), Is.EqualTo(1));
            Assert.That(state.NextGeneration(), Is.EqualTo(2));
            Assert.That(planner.IsSceneBindingValid(sceneRoute, 1), Is.True);
            Assert.That(planner.IsSceneBindingValid(sceneRoute, 0), Is.False);
            Assert.That(planner.IsSceneBindingValid(appRoute, 0), Is.True);
            Assert.That(planner.IsSceneBindingValid(appRoute, 1), Is.False);
        }

        /// <summary>验证 bounded queue 在已有 owner 和 waiter 时拒绝额外 transition。</summary>
        [Test]
        public async Task TransitionQueueRejectsOverCapacityWithoutCancellingOwner()
        {
            using (var queue = new ClientUiTransitionQueue(1))
            {
                var owner = await queue.EnterAsync(
                    CancellationToken.None,
                    CancellationToken.None);
                var waiting = queue.EnterAsync(
                    CancellationToken.None,
                    CancellationToken.None);
                var overloaded = await queue.EnterAsync(
                    CancellationToken.None,
                    CancellationToken.None);

                Assert.That(owner.Entered, Is.True);
                Assert.That(overloaded.Entered, Is.False);
                Assert.That(
                    overloaded.Result.Code,
                    Is.EqualTo(ClientUiTransitionCode.Overloaded));

                queue.Exit();
                var next = await waiting;
                Assert.That(next.Entered, Is.True);
                queue.Exit();
            }
        }

        /// <summary>验证 presentation intent 只在 running 且无并发 intent 时取得 lease。</summary>
        [Test]
        public void PresentationStateOwnsSingleIntentPerGeneration()
        {
            var state = new PersonalWorldPresentationState
            {
                Running = true,
                PresentationGeneration = 7,
            };

            Assert.That(
                state.TryBeginIntent(ClientPersonalWorldIntent.Login, out var generation),
                Is.True);
            Assert.That(generation, Is.EqualTo(7));
            Assert.That(state.ActiveIntent, Is.EqualTo(ClientPersonalWorldIntent.Login));
            Assert.That(
                state.TryBeginIntent(ClientPersonalWorldIntent.Logout, out _),
                Is.False);

            state.ActiveIntent = ClientPersonalWorldIntent.None;
            state.PresentationGeneration = 8;
            Assert.That(state.IsCurrent(7), Is.False);
            Assert.That(state.IsCurrent(8), Is.True);
        }
    }
}
