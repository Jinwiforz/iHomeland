using System;
using IHomeland.Client.Core.Runtime.Composition;
using IHomeland.Client.Networking.Runtime.Composition;
using IHomeland.Client.AppShell.Runtime.Presentation.Hosts;
using IHomeland.Client.AppShell.Presentation.Navigation;
using IHomeland.Client.PersonalWorld.Runtime.Composition;
using IHomeland.Client.PersonalWorld.Presentation;
using IHomeland.Client.PersonalWorldCombat.Runtime.Composition;
using IHomeland.Client.Core.Runtime.Scenes;
using IHomeland.Client.PersonalWorld.Runtime.Scenes;
using IHomeland.Client.Session.Runtime.Composition;

namespace IHomeland.Client.AppShell.Runtime.Composition
{
    /// <summary>创建 route、Scene 与产品 Experience 表现对象图。</summary>
    internal static class PresentationComposition
    {
        /// <summary>创建 isolated 或 production Presentation 模块。</summary>
        internal static PresentationCompositionBundle Create(
            ClientUiHostRoot uiHostRoot,
            ClientWorldSceneTransitionHost sceneTransitionHost,
            bool productExperience,
            int transitionQueueCapacity,
            TimeSpan cleanupTimeout,
            FoundationCompositionBundle foundation,
            SessionCompositionBundle session,
            ChannelCompositionBundle channels,
            WorldCompositionBundle world,
            BattleCompositionBundle battle)
        {
            if (uiHostRoot == null)
            {
                throw new ArgumentNullException(nameof(uiHostRoot));
            }

            if (foundation == null ||
                session == null ||
                channels == null ||
                world == null ||
                battle == null)
            {
                throw new ArgumentNullException(
                    "Presentation module dependencies 不能为空。");
            }

            var sceneLifetimeOwner = new SceneLifetimeOwner();
            if (productExperience)
            {
                if (sceneTransitionHost == null)
                {
                    throw new ArgumentNullException(nameof(sceneTransitionHost));
                }

                uiHostRoot.ValidateBattleInputConfiguration();
                sceneTransitionHost.Configure(
                    sceneLifetimeOwner,
                    battle.Runtime,
                    uiHostRoot);
            }

            var routeDefinitions = productExperience
                ? ClientPersonalWorldUiRoutes.Definitions
                : Array.Empty<ClientUiRouteDefinition>();
            var router = new ClientUiRouter(
                new ClientUiRegistry(routeDefinitions, uiHostRoot.GetHosts()),
                uiHostRoot,
                transitionQueueCapacity,
                cleanupTimeout);
            ClientPersonalWorldExperience experience = null;
            if (productExperience)
            {
                experience = new ClientPersonalWorldExperience(
                    session.BootstrapService,
                    session.SessionCoordinator,
                    session.RestoreCoordinator,
                    world.Recovery,
                    foundation.Dispatcher,
                    channels.ControlPort,
                    world.PersonalWorld,
                    world.VisitSession,
                    world.Admission,
                    router,
                    sceneTransitionHost,
                    ClientPersonalWorldExperience.DefaultConnectionRecoveryTimeout);
                uiHostRoot.GameplayMenuRequested +=
                    experience.RequestWorldVisitFromGameplayMenu;
                uiHostRoot.UiCancelRequested += experience.RequestUiCancel;
                uiHostRoot.ConfigureProductBindings(experience);
            }

            return new PresentationCompositionBundle(
                sceneLifetimeOwner,
                router,
                experience,
                sceneTransitionHost);
        }
    }

    /// <summary>封闭 Presentation 模块的 Runtime 接线输出。</summary>
    internal sealed class PresentationCompositionBundle
    {
        /// <summary>创建不可变 Presentation bundle。</summary>
        internal PresentationCompositionBundle(
            SceneLifetimeOwner sceneLifetimeOwner,
            ClientUiRouter router,
            ClientPersonalWorldExperience experience,
            ClientWorldSceneTransitionHost sceneTransitionHost)
        {
            SceneLifetimeOwner = sceneLifetimeOwner ??
                throw new ArgumentNullException(nameof(sceneLifetimeOwner));
            Router = router ?? throw new ArgumentNullException(nameof(router));
            Experience = experience;
            SceneTransitionHost = sceneTransitionHost;
        }

        /// <summary>获取 Scene Scope lifetime owner。</summary>
        internal SceneLifetimeOwner SceneLifetimeOwner { get; }

        /// <summary>获取唯一 route owner。</summary>
        internal ClientUiRouter Router { get; }

        /// <summary>获取产品 action facade；isolated graph 为空。</summary>
        internal ClientPersonalWorldExperience Experience { get; }

        /// <summary>获取 Runtime Scene host；isolated graph 为空。</summary>
        internal ClientWorldSceneTransitionHost SceneTransitionHost { get; }
    }
}
