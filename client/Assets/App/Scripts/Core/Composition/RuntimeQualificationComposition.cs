using System;
using System.Collections.Generic;
using IHomeland.Client.Foundation.Lifetime;
using IHomeland.Client.Presentation.Hosts;

namespace IHomeland.Client.Core.Composition
{
    /// <summary>冻结 AppLifetime 顺序并导出 AppRoot/qualification 所需最小结果。</summary>
    internal static class RuntimeQualificationComposition
    {
        /// <summary>按声明顺序创建唯一 AppLifetime 与 AppCompositionResult。</summary>
        internal static AppCompositionResult Create(
            ClientUiHostRoot uiHostRoot,
            FoundationCompositionBundle foundation,
            InfrastructureCompositionBundle infrastructure,
            SessionCompositionBundle session,
            ChannelCompositionBundle channels,
            WorldCompositionBundle world,
            PresentationCompositionBundle presentation,
            TimeSpan rollbackTimeout,
            TimeSpan shutdownTimeout,
            int maximumDispatchesPerFrame)
        {
            if (uiHostRoot == null || foundation == null || infrastructure == null ||
                session == null || channels == null || world == null ||
                presentation == null)
            {
                throw new ArgumentNullException(
                    "Runtime module dependencies 不能为空。");
            }

            var participants = new List<IAppLifetimeParticipant>
            {
                foundation.Dispatcher,
                uiHostRoot,
                session.ConfigurationStore,
                infrastructure.Transport,
                infrastructure.SecureSessionStore,
                session.SessionCoordinator,
                session.RestoreCoordinator,
                channels.GameplayPort,
                channels.ControlPort,
                world.PersonalWorld,
                world.VisitSession,
                world.Admission,
                world.Recovery,
                presentation.SceneLifetimeOwner,
            };
            if (presentation.SceneTransitionHost != null)
            {
                participants.Add(presentation.SceneTransitionHost);
            }

            participants.Add(presentation.Router);
            if (presentation.Experience != null)
            {
                participants.Add(presentation.Experience);
            }

            var lifetime = new AppLifetime(
                participants,
                rollbackTimeout,
                shutdownTimeout);
            return new AppCompositionResult(
                lifetime,
                foundation.Dispatcher,
                Array.Empty<IAppTickable>(),
                maximumDispatchesPerFrame,
                session.BootstrapService,
                session.SessionCoordinator,
                channels.ControlChannel,
                channels.GameplayChannel,
                world.PersonalWorld,
                world.VisitSession,
                world.Admission,
                world.Recovery,
                presentation.Router,
                presentation.Experience,
                presentation.SceneTransitionHost);
        }
    }
}
