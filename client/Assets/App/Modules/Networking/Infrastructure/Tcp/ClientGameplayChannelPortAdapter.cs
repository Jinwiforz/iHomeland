using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Networking.Application.Contracts;
using IHomeland.Client.Networking.Application.Gameplay;
using IHomeland.Client.Networking.Application.Ports;
using IHomeland.Client.Session.Application;
using IHomeland.Client.PersonalWorld.Application;
using IHomeland.Protocol.Visit.V1;
using IHomeland.Protocol.World.V1;

namespace IHomeland.Client.Networking.Infrastructure.Tcp
{
    /// <summary>
    /// 把 concrete gameplay channel 的 generated API 收窄为登记的 Application typed port。
    /// </summary>
    internal sealed class ClientGameplayChannelPortAdapter :
        IClientGameplayChannelPort
    {
        /// <summary>保护订阅、停止与 current connection role。</summary>
        private readonly object _sync = new object();

        /// <summary>保存拥有 socket generation 的 concrete channel。</summary>
        private readonly ClientGameplayChannel _channel;

        /// <summary>保存 generated/Application 唯一映射器。</summary>
        private readonly ClientGameplayProtocolAdapter _protocol;

        /// <summary>保存 current connection 绑定角色，只用于 PUSH 映射。</summary>
        private ClientVisitRole _role;

        /// <summary>标记是否已绑定 concrete channel events。</summary>
        private bool _subscribed;

        /// <summary>标记 port adapter 已停止。</summary>
        private bool _stopped;

        /// <summary>创建 gameplay typed port adapter。</summary>
        internal ClientGameplayChannelPortAdapter(
            ClientGameplayChannel channel,
            ClientGameplayProtocolAdapter protocol)
        {
            _channel = channel ?? throw new ArgumentNullException(nameof(channel));
            _protocol = protocol ?? throw new ArgumentNullException(nameof(protocol));
        }

        /// <inheritdoc />
        public event Action<ClientPersonalWorldProjection> WorldSnapshotReceived;

        /// <inheritdoc />
        public event Action<ClientVisitSessionProjection> VisitSnapshotReceived;

        /// <inheritdoc />
        public event Action<ClientSafeReturnProjection> SafeReturnReceived;

        /// <inheritdoc />
        public event Action<ClientGameplayHealthSnapshot> UnexpectedDisconnect;

        /// <inheritdoc />
        public ClientGameplayHealthSnapshot Snapshot => MapHealth(_channel.Snapshot);

        /// <inheritdoc />
        public async Task InitializeAsync(CancellationToken cancellationToken)
        {
            lock (_sync)
            {
                if (_stopped)
                {
                    throw new InvalidOperationException(
                        "Gameplay channel port adapter 已停止。");
                }

                if (!_subscribed)
                {
                    _channel.WorldSnapshotReceived += OnWorldSnapshot;
                    _channel.VisitSnapshotReceived += OnVisitSnapshot;
                    _channel.SafeReturnReceived += OnSafeReturn;
                    _channel.UnexpectedDisconnect += OnUnexpectedDisconnect;
                    _subscribed = true;
                }
            }

            await _channel.InitializeAsync(cancellationToken);
        }

        /// <inheritdoc />
        public async Task<bool> ConnectAsync(
            ClientWorldAdmissionLease admission,
            CancellationToken cancellationToken)
        {
            if (admission == null)
            {
                return false;
            }

            var connected = await _channel.ConnectAsync(admission, cancellationToken);
            if (connected)
            {
                lock (_sync)
                {
                    _role = admission.Role == ClientWorldRole.Visitor
                        ? ClientVisitRole.Visitor
                        : ClientVisitRole.Owner;
                }
            }

            return connected;
        }

        /// <inheritdoc />
        public async Task<ClientGameplayResult<ClientVisitSessionProjection>> JoinVisitAsync(
            ulong expectedRevision,
            CancellationToken cancellationToken)
        {
            var source = await _channel.JoinPendingVisitAsync(
                expectedRevision,
                cancellationToken);
            return Map(
                source,
                value => _protocol.MapVisit(
                    value?.Result?.Snapshot,
                    ClientVisitRole.Visitor));
        }

        /// <inheritdoc />
        public async Task<ClientGameplayResult<ClientVisitSessionProjection>> ReconnectVisitAsync(
            ulong expectedRevision,
            CancellationToken cancellationToken)
        {
            var source = await _channel.ReconnectPendingVisitAsync(
                expectedRevision,
                cancellationToken);
            return Map(
                source,
                value => _protocol.MapVisit(
                    value?.Result?.Snapshot,
                    ClientVisitRole.Visitor));
        }

        /// <inheritdoc />
        public async Task<ClientGameplayResult<ClientPersonalWorldProjection>> GetWorldSnapshotAsync(
            CancellationToken cancellationToken)
        {
            var source = await _channel.SendAsync(
                ClientGameplayCatalog.WorldSnapshot,
                new WorldSnapshotRequest(),
                cancellationToken);
            return Map(source, value => _protocol.MapWorld(value?.Snapshot));
        }

        /// <inheritdoc />
        public async Task<ClientGameplayResult<ClientVisitSessionProjection>> GetVisitSnapshotAsync(
            ClientVisitRole role,
            CancellationToken cancellationToken)
        {
            var source = await _channel.SendAsync(
                ClientGameplayCatalog.VisitSnapshot,
                new VisitSnapshotRequest(),
                cancellationToken);
            return Map(
                source,
                value => _protocol.MapVisit(value?.Snapshot, role));
        }

        /// <inheritdoc />
        public async Task<ClientGameplayResult<ClientVisitMutationCandidate>> OpenVisitAsync(
            CancellationToken cancellationToken)
        {
            var source = await _channel.SendAsync(
                ClientGameplayCatalog.VisitOpen,
                new VisitOpenCommand(),
                cancellationToken);
            return Map(
                source,
                value => new ClientVisitMutationCandidate(
                    _protocol.MapVisit(value?.Snapshot, ClientVisitRole.Owner),
                    null,
                    null));
        }

        /// <inheritdoc />
        public async Task<ClientGameplayResult<ClientVisitMutationCandidate>> CreateVisitInviteAsync(
            ClientCreateVisitInviteRequest request,
            CancellationToken cancellationToken)
        {
            if (request == null)
            {
                return ClientGameplayResult<ClientVisitMutationCandidate>.Failed(
                    ClientGameplayFailureKind.Policy);
            }

            var source = await _channel.SendAsync(
                ClientGameplayCatalog.VisitCreateInvite,
                new VisitCreateInviteCommand
                {
                    TargetVisitorId = request.TargetVisitorID,
                    InviteLifetimeMs = request.LifetimeMilliseconds,
                    ExpectedRevision = request.ExpectedRevision,
                },
                cancellationToken);
            return Map(
                source,
                value => _protocol.MapMutation(
                    value?.Result,
                    ClientVisitRole.Owner,
                    value?.Invite));
        }

        /// <inheritdoc />
        public Task<ClientGameplayResult<ClientVisitMutationCandidate>> RevokeVisitInviteAsync(
            ClientRevisionedIdentityRequest request,
            CancellationToken cancellationToken)
        {
            if (request == null)
            {
                return Policy();
            }

            return SendMutationAsync(
                ClientGameplayCatalog.VisitRevokeInvite,
                new VisitRevokeInviteCommand
                {
                    InviteId = request.Identity,
                    ExpectedRevision = request.ExpectedRevision,
                },
                value => value.Result,
                ClientVisitRole.Owner,
                cancellationToken);
        }

        /// <inheritdoc />
        public Task<ClientGameplayResult<ClientVisitMutationCandidate>> KickVisitMemberAsync(
            ClientRevisionedIdentityRequest request,
            CancellationToken cancellationToken)
        {
            if (request == null)
            {
                return Policy();
            }

            return SendMutationAsync(
                ClientGameplayCatalog.VisitKick,
                new VisitKickCommand
                {
                    TargetVisitorId = request.Identity,
                    ExpectedRevision = request.ExpectedRevision,
                },
                value => value.Result,
                ClientVisitRole.Owner,
                cancellationToken);
        }

        /// <inheritdoc />
        public Task<ClientGameplayResult<ClientVisitMutationCandidate>> CloseVisitAsync(
            ulong expectedRevision,
            CancellationToken cancellationToken)
        {
            return SendMutationAsync(
                ClientGameplayCatalog.VisitClose,
                new VisitCloseCommand { ExpectedRevision = expectedRevision },
                value => value.Result,
                ClientVisitRole.Owner,
                cancellationToken);
        }

        /// <inheritdoc />
        public Task<ClientGameplayResult<ClientVisitMutationCandidate>> LeaveVisitAsync(
            ulong expectedRevision,
            CancellationToken cancellationToken)
        {
            return SendMutationAsync(
                ClientGameplayCatalog.VisitLeave,
                new VisitLeaveCommand { ExpectedRevision = expectedRevision },
                value => value.Result,
                ClientVisitRole.Visitor,
                cancellationToken);
        }

        /// <inheritdoc />
        public void InvalidateSession(long sourceSessionGeneration)
        {
            _channel.InvalidateSession(sourceSessionGeneration);
        }

        /// <inheritdoc />
        public async Task CloseAsync(CancellationToken cancellationToken)
        {
            await _channel.CloseAsync(cancellationToken);
            lock (_sync)
            {
                _role = ClientVisitRole.None;
            }
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
                    _channel.WorldSnapshotReceived -= OnWorldSnapshot;
                    _channel.VisitSnapshotReceived -= OnVisitSnapshot;
                    _channel.SafeReturnReceived -= OnSafeReturn;
                    _channel.UnexpectedDisconnect -= OnUnexpectedDisconnect;
                    _subscribed = false;
                }
            }

            await _channel.StopAsync(cancellationToken);
        }

        /// <summary>发送具有同一 mutation result shape 的登记 command。</summary>
        private async Task<ClientGameplayResult<ClientVisitMutationCandidate>>
            SendMutationAsync<TRequest, TResponse>(
                ClientGameplayOperation<TRequest, TResponse> operation,
                TRequest request,
                Func<TResponse, VisitMutationResult> selector,
                ClientVisitRole role,
                CancellationToken cancellationToken)
            where TRequest : class, Google.Protobuf.IMessage<TRequest>
            where TResponse : class, Google.Protobuf.IMessage<TResponse>
        {
            var source = await _channel.SendAsync(
                operation,
                request,
                cancellationToken);
            return Map(
                source,
                value => _protocol.MapMutation(selector(value), role));
        }

        /// <summary>映射 concrete channel 的成功、服务端拒绝或本地失败。</summary>
        private static ClientGameplayResult<TTarget> Map<TSource, TTarget>(
            ClientGameplayResult<TSource> source,
            Func<TSource, TTarget> map)
            where TSource : class
            where TTarget : class
        {
            if (source == null)
            {
                return ClientGameplayResult<TTarget>.Failed(
                    ClientGameplayFailureKind.Protocol);
            }

            if (source.ServerError != null)
            {
                return ClientGameplayResult<TTarget>.Rejected(source.ServerError);
            }

            if (source.Failure.HasValue)
            {
                return ClientGameplayResult<TTarget>.Failed(source.Failure.Value);
            }

            try
            {
                return ClientGameplayResult<TTarget>.Success(map(source.Value));
            }
            catch (ClientWorldProjectionException)
            {
                return ClientGameplayResult<TTarget>.Failed(
                    ClientGameplayFailureKind.Protocol);
            }
            catch (ArgumentException)
            {
                return ClientGameplayResult<TTarget>.Failed(
                    ClientGameplayFailureKind.Protocol);
            }
        }

        /// <summary>创建空业务请求的稳定 policy failure task。</summary>
        private static Task<ClientGameplayResult<ClientVisitMutationCandidate>> Policy()
        {
            return Task.FromResult(
                ClientGameplayResult<ClientVisitMutationCandidate>.Failed(
                    ClientGameplayFailureKind.Policy));
        }

        /// <summary>映射 concrete channel health。</summary>
        private static ClientGameplayHealthSnapshot MapHealth(
            ClientGameplayChannelSnapshot snapshot)
        {
            return new ClientGameplayHealthSnapshot(
                snapshot.State != ClientGameplayChannelState.Created &&
                snapshot.State != ClientGameplayChannelState.Ready &&
                snapshot.State != ClientGameplayChannelState.Stopped,
                snapshot.State == ClientGameplayChannelState.Active,
                snapshot.Generation,
                MapDisconnect(snapshot.CloseReason));
        }

        /// <summary>映射 concrete close reason 为 Application terminal 分类。</summary>
        private static ClientGameplayDisconnectKind MapDisconnect(
            ClientGameplayCloseReason reason)
        {
            switch (reason)
            {
                case ClientGameplayCloseReason.None:
                    return ClientGameplayDisconnectKind.None;
                case ClientGameplayCloseReason.Protocol:
                    return ClientGameplayDisconnectKind.Protocol;
                case ClientGameplayCloseReason.SessionInvalidated:
                    return ClientGameplayDisconnectKind.SessionInvalidated;
                case ClientGameplayCloseReason.Caller:
                case ClientGameplayCloseReason.ApplicationReturn:
                case ClientGameplayCloseReason.Shutdown:
                    return ClientGameplayDisconnectKind.Requested;
                default:
                    return ClientGameplayDisconnectKind.Transport;
            }
        }

        /// <summary>映射 world PUSH 并在协议字段无效时关闭该通知路径。</summary>
        private void OnWorldSnapshot(WorldSnapshotPush push)
        {
            try
            {
                WorldSnapshotReceived?.Invoke(_protocol.MapWorld(push?.Snapshot));
            }
            catch (ClientWorldProjectionException)
            {
                _channel.InvalidateProtocolInput();
            }
        }

        /// <summary>按 current connection role 映射 VisitSession PUSH。</summary>
        private void OnVisitSnapshot(VisitSnapshotPush push)
        {
            ClientVisitRole role;
            lock (_sync)
            {
                role = _role;
            }

            try
            {
                VisitSnapshotReceived?.Invoke(_protocol.MapVisit(push?.Snapshot, role));
            }
            catch (ClientWorldProjectionException)
            {
                _channel.InvalidateProtocolInput();
            }
        }

        /// <summary>映射 safe-return PUSH。</summary>
        private void OnSafeReturn(VisitSafeReturnPush push)
        {
            try
            {
                SafeReturnReceived?.Invoke(
                    _protocol.MapSafeReturn(push?.Directive));
            }
            catch (ClientWorldProjectionException)
            {
                _channel.InvalidateProtocolInput();
            }
        }

        /// <summary>映射意外断开 health 并清除 connection role。</summary>
        private void OnUnexpectedDisconnect(ClientGameplayChannelSnapshot snapshot)
        {
            lock (_sync)
            {
                _role = ClientVisitRole.None;
            }

            UnexpectedDisconnect?.Invoke(MapHealth(snapshot));
        }
    }
}
