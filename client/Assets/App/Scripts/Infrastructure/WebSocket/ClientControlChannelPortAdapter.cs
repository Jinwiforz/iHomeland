using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Control;
using IHomeland.Client.Application.Ports;

namespace IHomeland.Client.Infrastructure.WebSocket
{
    /// <summary>
    /// 把 concrete WSS lifecycle owner 收窄为 Application control channel port。
    /// </summary>
    internal sealed class ClientControlChannelPortAdapter :
        IClientControlChannelPort
    {
        /// <summary>保护订阅与停止幂等。</summary>
        private readonly object _sync = new object();

        /// <summary>保存 concrete WSS channel owner。</summary>
        private readonly ClientControlChannel _channel;

        /// <summary>记录是否已绑定 concrete events。</summary>
        private bool _subscribed;

        /// <summary>记录 adapter 已停止。</summary>
        private bool _stopped;

        /// <summary>创建 control channel port adapter。</summary>
        internal ClientControlChannelPortAdapter(ClientControlChannel channel)
        {
            _channel = channel ?? throw new ArgumentNullException(nameof(channel));
        }

        /// <inheritdoc />
        public event Action<ClientControlNotification> PushReceived;

        /// <inheritdoc />
        public event Action<ClientControlHealthSnapshot> HealthChanged;

        /// <inheritdoc />
        public ClientControlHealthSnapshot Snapshot => Map(_channel.Snapshot);

        /// <inheritdoc />
        public async Task InitializeAsync(CancellationToken cancellationToken)
        {
            lock (_sync)
            {
                if (_stopped)
                {
                    throw new InvalidOperationException(
                        "Control channel port adapter 已停止。");
                }

                if (!_subscribed)
                {
                    _channel.PushReceived += OnPush;
                    _channel.HealthChanged += OnHealthChanged;
                    _subscribed = true;
                }
            }

            await _channel.InitializeAsync(cancellationToken);
        }

        /// <inheritdoc />
        public Task RunAsync(CancellationToken cancellationToken)
        {
            return _channel.RunAsync(cancellationToken);
        }

        /// <inheritdoc />
        public Task<bool> WaitUntilConnectedAsync(CancellationToken cancellationToken)
        {
            return _channel.WaitUntilConnectedAsync(cancellationToken);
        }

        /// <inheritdoc />
        public async Task StopAsync(CancellationToken cancellationToken)
        {
            lock (_sync)
            {
                if (_stopped)
                {
                    return;
                }

                _stopped = true;
                if (_subscribed)
                {
                    _channel.PushReceived -= OnPush;
                    _channel.HealthChanged -= OnHealthChanged;
                    _subscribed = false;
                }
            }

            await _channel.StopAsync(cancellationToken);
        }

        /// <summary>转发无 generated payload 的 typed notification。</summary>
        private void OnPush(ClientControlNotification notification)
        {
            PushReceived?.Invoke(notification);
        }

        /// <summary>映射并转发低敏 health snapshot。</summary>
        private void OnHealthChanged(ClientControlChannelSnapshot snapshot)
        {
            HealthChanged?.Invoke(Map(snapshot));
        }

        /// <summary>把 concrete state 收敛为 Application health contract。</summary>
        private static ClientControlHealthSnapshot Map(
            ClientControlChannelSnapshot snapshot)
        {
            return new ClientControlHealthSnapshot(
                MapPhase(snapshot.State),
                MapDisconnectKind(snapshot.CloseReason),
                snapshot.Generation,
                snapshot.Attempt);
        }

        /// <summary>把 concrete state 映射为稳定 Application phase。</summary>
        private static ClientControlHealthPhase MapPhase(ClientControlChannelState state)
        {
            switch (state)
            {
                case ClientControlChannelState.Created:
                    return ClientControlHealthPhase.Created;
                case ClientControlChannelState.Idle:
                    return ClientControlHealthPhase.Idle;
                case ClientControlChannelState.Connecting:
                    return ClientControlHealthPhase.Connecting;
                case ClientControlChannelState.Connected:
                    return ClientControlHealthPhase.Connected;
                case ClientControlChannelState.Recovering:
                    return ClientControlHealthPhase.Recovering;
                case ClientControlChannelState.Disconnected:
                    return ClientControlHealthPhase.Disconnected;
                case ClientControlChannelState.SessionInvalidated:
                    return ClientControlHealthPhase.SessionInvalidated;
                case ClientControlChannelState.Stopped:
                    return ClientControlHealthPhase.Stopped;
                default:
                    throw new ArgumentOutOfRangeException(nameof(state), state, null);
            }
        }

        /// <summary>把 concrete close reason 映射为稳定 Application 分类。</summary>
        private static ClientControlDisconnectKind MapDisconnectKind(
            ClientControlCloseReason reason)
        {
            switch (reason)
            {
                case ClientControlCloseReason.None:
                    return ClientControlDisconnectKind.None;
                case ClientControlCloseReason.Requested:
                    return ClientControlDisconnectKind.Requested;
                case ClientControlCloseReason.TransportFailure:
                case ClientControlCloseReason.PeerClosed:
                case ClientControlCloseReason.RetryExhausted:
                    return ClientControlDisconnectKind.Transport;
                case ClientControlCloseReason.ProtocolFailure:
                case ClientControlCloseReason.MainThreadBackpressure:
                    return ClientControlDisconnectKind.Protocol;
                case ClientControlCloseReason.SessionInvalidated:
                    return ClientControlDisconnectKind.SessionInvalidated;
                case ClientControlCloseReason.SupersededConnection:
                    return ClientControlDisconnectKind.Superseded;
                case ClientControlCloseReason.PolicyRejected:
                    return ClientControlDisconnectKind.Policy;
                default:
                    throw new ArgumentOutOfRangeException(nameof(reason), reason, null);
            }
        }
    }
}
