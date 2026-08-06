using System;
using Google.Protobuf;
using IHomeland.Client.Networking.Application.Gameplay;
using IHomeland.Client.Core.Foundation.Lifetime;
using IHomeland.Client.Core.Foundation.Threading;
using IHomeland.Protocol.Visit.V1;
using IHomeland.Protocol.World.V1;

namespace IHomeland.Client.Networking.Infrastructure.Tcp
{
    /// <summary>标识三个登记 Gameplay PUSH route。</summary>
    internal enum GameplayPushRouteKind
    {
        /// <summary>PersonalWorld 完整替换。</summary>
        WorldSnapshot = 0,
        /// <summary>VisitSession 完整替换。</summary>
        VisitSnapshot = 1,
        /// <summary>权威 safe-return。</summary>
        SafeReturn = 2,
    }

    /// <summary>保存解码后的单个 typed Gameplay PUSH。</summary>
    internal sealed class GameplayPushRoute
    {
        /// <summary>创建 typed route。</summary>
        internal GameplayPushRoute(GameplayPushRouteKind kind, object value)
        {
            Kind = kind;
            Value = value ?? throw new ArgumentNullException(nameof(value));
        }

        /// <summary>获取固定 route。</summary>
        internal GameplayPushRouteKind Kind { get; }

        /// <summary>获取 route 对应的 generated value；不会暴露给 Application。</summary>
        internal object Value { get; }
    }

    /// <summary>
    /// 拥有 Gameplay response/error 与三个 PUSH route 的封闭解码和主线程有界投递。
    /// </summary>
    internal sealed class GameplayRouteDispatcher
    {
        /// <summary>解码 Error envelope。</summary>
        private readonly ClientGameplayCodec _codec;

        /// <summary>有界转移 typed PUSH callback。</summary>
        private readonly IClientMainThreadDispatcher _dispatcher;

        /// <summary>创建固定 route dispatcher。</summary>
        internal GameplayRouteDispatcher(
            ClientGameplayCodec codec,
            IClientMainThreadDispatcher dispatcher)
        {
            _codec = codec ?? throw new ArgumentNullException(nameof(codec));
            _dispatcher = dispatcher ?? throw new ArgumentNullException(nameof(dispatcher));
        }

        /// <summary>按 pending parser 解码 response 或结构化 server error。</summary>
        internal GameplayPendingCompletion DecodeCompletion(
            ClientGameplayEnvelope envelope,
            GameplayPendingOperation pending)
        {
            if (envelope == null || pending == null)
            {
                throw new ArgumentNullException(
                    envelope == null ? nameof(envelope) : nameof(pending));
            }

            return envelope.Kind == IHomeland.Protocol.Common.V1.MessageKind.Error
                ? GameplayPendingCompletion.Rejected(_codec.DecodeError(envelope))
                : GameplayPendingCompletion.Succeeded(
                    pending.ParseResponse(envelope));
        }

        /// <summary>只接受三个登记 PUSH message ID 并解码其 payload。</summary>
        internal GameplayPushRoute DecodePush(ClientGameplayEnvelope envelope)
        {
            if (envelope == null)
            {
                throw new ArgumentNullException(nameof(envelope));
            }

            try
            {
                if (envelope.MessageID ==
                    ClientGameplayCatalog.WorldSnapshotPushRoute.MessageID)
                {
                    return new GameplayPushRoute(
                        GameplayPushRouteKind.WorldSnapshot,
                        ClientGameplayCatalog.WorldSnapshotPushRoute.Parser.ParseFrom(
                            envelope.Payload));
                }

                if (envelope.MessageID ==
                    ClientGameplayCatalog.VisitSnapshotPushRoute.MessageID)
                {
                    return new GameplayPushRoute(
                        GameplayPushRouteKind.VisitSnapshot,
                        ClientGameplayCatalog.VisitSnapshotPushRoute.Parser.ParseFrom(
                            envelope.Payload));
                }

                if (envelope.MessageID ==
                    ClientGameplayCatalog.VisitSafeReturnPushRoute.MessageID)
                {
                    return new GameplayPushRoute(
                        GameplayPushRouteKind.SafeReturn,
                        ClientGameplayCatalog.VisitSafeReturnPushRoute.Parser.ParseFrom(
                            envelope.Payload));
                }
            }
            catch (InvalidProtocolBufferException exception)
            {
                throw new ClientGameplayProtocolException(
                    "Gameplay PUSH payload 无效。",
                    exception);
            }

            throw new ClientGameplayProtocolException("Gameplay PUSH route 未登记。");
        }

        /// <summary>有界投递已解码 callback。</summary>
        internal bool TryPost(Action callback)
        {
            return _dispatcher.TryPost(
                callback ?? throw new ArgumentNullException(nameof(callback))) ==
                   DispatchPostResult.Accepted;
        }
    }
}
