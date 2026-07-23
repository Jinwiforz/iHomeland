using System;
using IHomeland.Client.Application.World;
using IHomeland.Client.Presentation.PersonalWorld;

namespace IHomeland.Client.Core.Composition
{
    /// <summary>创建 PersonalWorld、VisitSession、target 与 recovery owners。</summary>
    internal static class WorldComposition
    {
        /// <summary>创建只依赖 typed channel ports 的 World 模块对象图。</summary>
        internal static WorldCompositionBundle Create(
            FoundationCompositionBundle foundation,
            SessionCompositionBundle session,
            ChannelCompositionBundle channels)
        {
            if (foundation == null)
            {
                throw new ArgumentNullException(nameof(foundation));
            }

            if (session == null)
            {
                throw new ArgumentNullException(nameof(session));
            }

            if (channels == null)
            {
                throw new ArgumentNullException(nameof(channels));
            }

            var personalWorld = new PersonalWorldService(
                channels.ControlPort,
                channels.GameplayPort);
            var visitSession = new VisitSessionService(
                channels.ControlPort,
                channels.GameplayPort,
                foundation.Clock);
            var admission = new WorldAdmissionCoordinator(
                session.SessionCoordinator,
                foundation.Clock,
                channels.GameplayPort,
                personalWorld,
                visitSession);
            var recovery = new ClientConnectionRecoveryCoordinator(
                session.SessionCoordinator,
                channels.ControlPort,
                channels.GameplayPort,
                admission,
                ClientPersonalWorldExperience.DefaultConnectionRecoveryTimeout);
            return new WorldCompositionBundle(
                personalWorld,
                visitSession,
                admission,
                recovery);
        }
    }

    /// <summary>封闭 World 模块的四个唯一 owner。</summary>
    internal sealed class WorldCompositionBundle
    {
        /// <summary>创建不可变 World bundle。</summary>
        internal WorldCompositionBundle(
            PersonalWorldService personalWorld,
            VisitSessionService visitSession,
            WorldAdmissionCoordinator admission,
            ClientConnectionRecoveryCoordinator recovery)
        {
            PersonalWorld = personalWorld ??
                throw new ArgumentNullException(nameof(personalWorld));
            VisitSession = visitSession ??
                throw new ArgumentNullException(nameof(visitSession));
            Admission = admission ?? throw new ArgumentNullException(nameof(admission));
            Recovery = recovery ?? throw new ArgumentNullException(nameof(recovery));
        }

        /// <summary>获取唯一 PersonalWorld projection owner。</summary>
        internal PersonalWorldService PersonalWorld { get; }

        /// <summary>获取唯一 VisitSession/inbox owner。</summary>
        internal VisitSessionService VisitSession { get; }

        /// <summary>获取唯一 current target owner。</summary>
        internal WorldAdmissionCoordinator Admission { get; }

        /// <summary>获取唯一 recovery intent owner。</summary>
        internal ClientConnectionRecoveryCoordinator Recovery { get; }
    }
}
