using System;
using IHomeland.Client.AppShell.Application.Bootstrap;
using IHomeland.Client.AppShell.Application.Configuration;
using IHomeland.Client.Core.Runtime.Composition;
using IHomeland.Client.Networking.Runtime.Composition;
using IHomeland.Client.Session.Application;

namespace IHomeland.Client.Session.Runtime.Composition
{
    /// <summary>创建 Configuration、bootstrap、Session 与 restore owners。</summary>
    internal static class SessionComposition
    {
        /// <summary>创建只依赖 ports 的 Session 模块对象图。</summary>
        internal static SessionCompositionBundle Create(
            ClientEnvironment environment,
            FoundationCompositionBundle foundation,
            InfrastructureCompositionBundle infrastructure)
        {
            if (environment == null)
            {
                throw new ArgumentNullException(nameof(environment));
            }

            if (foundation == null)
            {
                throw new ArgumentNullException(nameof(foundation));
            }

            if (infrastructure == null)
            {
                throw new ArgumentNullException(nameof(infrastructure));
            }

            var configurationStore = new ClientConfigurationStore();
            var bootstrap = new ClientBootstrapService(
                environment,
                infrastructure.BootstrapGateway,
                configurationStore);
            var session = new SessionCoordinator(
                configurationStore,
                infrastructure.SessionGateway,
                foundation.Clock,
                infrastructure.SecureSessionStore,
                infrastructure.EnvironmentBinding);
            var restore = new ClientSessionRestoreCoordinator(
                infrastructure.SecureSessionStore,
                bootstrap,
                session,
                ClientSessionRestoreCoordinator.DefaultRestoreDeadline);
            return new SessionCompositionBundle(
                configurationStore,
                bootstrap,
                session,
                restore);
        }
    }

    /// <summary>封闭 Session 模块的 owner/facade 输出。</summary>
    internal sealed class SessionCompositionBundle
    {
        /// <summary>创建不可变 Session bundle。</summary>
        internal SessionCompositionBundle(
            ClientConfigurationStore configurationStore,
            ClientBootstrapService bootstrapService,
            SessionCoordinator sessionCoordinator,
            ClientSessionRestoreCoordinator restoreCoordinator)
        {
            ConfigurationStore = configurationStore ??
                throw new ArgumentNullException(nameof(configurationStore));
            BootstrapService = bootstrapService ??
                throw new ArgumentNullException(nameof(bootstrapService));
            SessionCoordinator = sessionCoordinator ??
                throw new ArgumentNullException(nameof(sessionCoordinator));
            RestoreCoordinator = restoreCoordinator ??
                throw new ArgumentNullException(nameof(restoreCoordinator));
        }

        /// <summary>获取唯一 Configuration owner。</summary>
        internal ClientConfigurationStore ConfigurationStore { get; }

        /// <summary>获取 bootstrap facade。</summary>
        internal ClientBootstrapService BootstrapService { get; }

        /// <summary>获取唯一 Session owner。</summary>
        internal SessionCoordinator SessionCoordinator { get; }

        /// <summary>获取启动 restore owner。</summary>
        internal ClientSessionRestoreCoordinator RestoreCoordinator { get; }
    }
}
