using System;
using IHomeland.Client.AppShell.Application.Configuration;
using IHomeland.Client.Networking.Application.Ports;
using IHomeland.Client.Session.Application;
using IHomeland.Client.Networking.Infrastructure.Http;
using IHomeland.Client.Session.Infrastructure.Security;
using IHomeland.Client.Networking.Infrastructure.Tcp;
using IHomeland.Client.Networking.Infrastructure.WebSocket;
using UnityEngine;

namespace IHomeland.Client.Networking.Runtime.Composition
{
    /// <summary>创建 HTTP、secure storage 与 socket factory concrete adapters。</summary>
    internal static class InfrastructureComposition
    {
        /// <summary>按已验证环境创建全部 concrete transport/security adapters。</summary>
        internal static InfrastructureCompositionBundle Create(
            ClientEnvironment environment)
        {
            if (environment == null)
            {
                throw new ArgumentNullException(nameof(environment));
            }

            var transport = new ClientHttpTransport(environment);
            var httpApi = new ClientHttpApi(
                transport,
                new ClientHttpContractMapper());
            var environmentBinding =
                ClientSecureSessionEnvironmentBinding.Create(environment);
            var processArguments = Environment.GetCommandLineArgs();
            var isDebugBuild =
                UnityEngine.Application.isEditor || UnityEngine.Debug.isDebugBuild;
            var secureSessionProfile = ClientSecureSessionProfile.Resolve(
                environment.EnvironmentKind,
                processArguments,
                isDebugBuild);
            var secureSessionRoot = ClientSecureSessionProfile.ResolveStorageRoot(
                environment.EnvironmentKind,
                processArguments,
                isDebugBuild,
                UnityEngine.Application.persistentDataPath);
            var windowsSecureStorage =
                UnityEngine.Application.platform == RuntimePlatform.WindowsEditor ||
                UnityEngine.Application.platform == RuntimePlatform.WindowsPlayer;
            IClientSecureSessionStore secureSessionStore = windowsSecureStorage
                ? (IClientSecureSessionStore)new ClientSecureSessionFileStore(
                    secureSessionRoot,
                    secureSessionProfile,
                    environmentBinding,
                    new WindowsDpapiDataProtector(),
                    platformSupported: true)
                : new UnsupportedClientSecureSessionStore();

            return new InfrastructureCompositionBundle(
                transport,
                httpApi,
                secureSessionStore,
                environmentBinding,
                new SystemClientGameplayConnectionFactory(environment),
                new SystemClientWebSocketFactory());
        }
    }

    /// <summary>封闭 Infrastructure 模块的 ports 与 concrete lifecycle owners。</summary>
    internal sealed class InfrastructureCompositionBundle
    {
        /// <summary>创建不可变 Infrastructure bundle。</summary>
        internal InfrastructureCompositionBundle(
            ClientHttpTransport transport,
            IClientBootstrapGateway bootstrapGateway,
            IClientSecureSessionStore secureSessionStore,
            string environmentBinding,
            IClientGameplayConnectionFactory gameplayConnectionFactory,
            IClientWebSocketFactory webSocketFactory)
        {
            Transport = transport ?? throw new ArgumentNullException(nameof(transport));
            BootstrapGateway = bootstrapGateway ??
                throw new ArgumentNullException(nameof(bootstrapGateway));
            SessionGateway = bootstrapGateway as IClientSessionGateway ??
                throw new ArgumentException(
                    "HTTP adapter 必须同时实现 Session gateway。",
                    nameof(bootstrapGateway));
            SecureSessionStore = secureSessionStore ??
                throw new ArgumentNullException(nameof(secureSessionStore));
            EnvironmentBinding = environmentBinding ??
                throw new ArgumentNullException(nameof(environmentBinding));
            GameplayConnectionFactory = gameplayConnectionFactory ??
                throw new ArgumentNullException(nameof(gameplayConnectionFactory));
            WebSocketFactory = webSocketFactory ??
                throw new ArgumentNullException(nameof(webSocketFactory));
        }

        /// <summary>获取 HTTP transport lifecycle owner。</summary>
        internal ClientHttpTransport Transport { get; }

        /// <summary>获取 bootstrap gateway port。</summary>
        internal IClientBootstrapGateway BootstrapGateway { get; }

        /// <summary>获取 Session gateway port。</summary>
        internal IClientSessionGateway SessionGateway { get; }

        /// <summary>获取 secure storage port。</summary>
        internal IClientSecureSessionStore SecureSessionStore { get; }

        /// <summary>获取 secure record 环境 binding。</summary>
        internal string EnvironmentBinding { get; }

        /// <summary>获取 gameplay connection factory。</summary>
        internal IClientGameplayConnectionFactory GameplayConnectionFactory { get; }

        /// <summary>获取 WSS factory。</summary>
        internal IClientWebSocketFactory WebSocketFactory { get; }
    }
}
