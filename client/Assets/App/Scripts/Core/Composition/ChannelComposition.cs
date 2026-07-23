using System;
using IHomeland.Client.Application.Configuration;
using IHomeland.Client.Application.Ports;
using IHomeland.Client.Infrastructure.Tcp;
using IHomeland.Client.Infrastructure.WebSocket;

namespace IHomeland.Client.Core.Composition
{
    /// <summary>创建 concrete Control/Gameplay owners 与窄 Application port adapters。</summary>
    internal static class ChannelComposition
    {
        /// <summary>创建两个 channel generation owners 及其 protocol adapters。</summary>
        internal static ChannelCompositionBundle Create(
            ClientEnvironment environment,
            FoundationCompositionBundle foundation,
            InfrastructureCompositionBundle infrastructure,
            SessionCompositionBundle session,
            TimeSpan[] controlRetryDelays)
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

            if (session == null)
            {
                throw new ArgumentNullException(nameof(session));
            }

            if (controlRetryDelays == null)
            {
                throw new ArgumentNullException(nameof(controlRetryDelays));
            }

            var gameplayChannel = new ClientGameplayChannel(
                session.ConfigurationStore,
                session.SessionCoordinator,
                infrastructure.GameplayConnectionFactory,
                new ClientGameplayCodec(),
                foundation.Dispatcher,
                foundation.Delay);
            var gameplayPort = new ClientGameplayChannelPortAdapter(
                gameplayChannel,
                new ClientGameplayProtocolAdapter());
            var controlChannel = new ClientControlChannel(
                environment,
                session.ConfigurationStore,
                session.SessionCoordinator,
                infrastructure.WebSocketFactory,
                new ClientControlCodec(new ClientControlCatalog()),
                new ClientControlProtocolAdapter(),
                foundation.Dispatcher,
                foundation.Delay,
                controlRetryDelays,
                gameplayPort.InvalidateSession);
            var controlPort = new ClientControlChannelPortAdapter(controlChannel);
            return new ChannelCompositionBundle(
                controlChannel,
                controlPort,
                gameplayChannel,
                gameplayPort);
        }
    }

    /// <summary>封闭 Channel 模块的 concrete diagnostics owner 与业务 ports。</summary>
    internal sealed class ChannelCompositionBundle
    {
        /// <summary>创建不可变 Channel bundle。</summary>
        internal ChannelCompositionBundle(
            ClientControlChannel controlChannel,
            IClientControlChannelPort controlPort,
            ClientGameplayChannel gameplayChannel,
            IClientGameplayChannelPort gameplayPort)
        {
            ControlChannel = controlChannel ??
                throw new ArgumentNullException(nameof(controlChannel));
            ControlPort = controlPort ??
                throw new ArgumentNullException(nameof(controlPort));
            GameplayChannel = gameplayChannel ??
                throw new ArgumentNullException(nameof(gameplayChannel));
            GameplayPort = gameplayPort ??
                throw new ArgumentNullException(nameof(gameplayPort));
        }

        /// <summary>获取 concrete WSS owner，仅供 Runtime diagnostics。</summary>
        internal ClientControlChannel ControlChannel { get; }

        /// <summary>获取 Application control port。</summary>
        internal IClientControlChannelPort ControlPort { get; }

        /// <summary>获取 concrete TLS/TCP owner，仅供 Runtime diagnostics。</summary>
        internal ClientGameplayChannel GameplayChannel { get; }

        /// <summary>获取 Application gameplay port。</summary>
        internal IClientGameplayChannelPort GameplayPort { get; }
    }
}
