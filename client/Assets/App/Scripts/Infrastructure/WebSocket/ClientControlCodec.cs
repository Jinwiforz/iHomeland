using Google.Protobuf;
using IHomeland.Protocol.Common.V1;

namespace IHomeland.Client.Infrastructure.WebSocket
{
    /// <summary>
    /// 严格验证并解码只允许服务端发送的 WSS control `ReliableEnvelope`。
    /// </summary>
    internal sealed class ClientControlCodec
    {
        /// <summary>
        /// 保存唯一封闭 route catalog。
        /// </summary>
        private readonly ClientControlCatalog _catalog;

        /// <summary>
        /// 创建 control codec。
        /// </summary>
        /// <param name="catalog">冻结的 9-route catalog。</param>
        internal ClientControlCodec(ClientControlCatalog catalog)
        {
            _catalog = catalog ?? throw new System.ArgumentNullException(nameof(catalog));
        }

        /// <summary>
        /// 解码一条完整 binary WebSocket message，并执行 envelope、route、size 与 sequence 校验。
        /// </summary>
        /// <param name="frame">由 receive pump 持有的有界 buffer。</param>
        /// <param name="length">完整 message 的有效字节数。</param>
        /// <param name="maximumFrameBytes">Bootstrap configuration 公布的全局 frame 上限。</param>
        /// <param name="expectedSequence">当前连接下一条必须精确匹配的 sequence。</param>
        /// <returns>已通过 route 精确解析的不可变 PUSH。</returns>
        /// <exception cref="ClientControlProtocolException">任一冻结协议边界不满足时抛出。</exception>
        internal ClientControlPush Decode(
            byte[] frame,
            int length,
            int maximumFrameBytes,
            ulong expectedSequence)
        {
            if (frame == null || length <= 0 || length > frame.Length)
            {
                throw new ClientControlProtocolException(ClientControlProtocolFailureKind.MalformedPayload);
            }

            if (maximumFrameBytes <= 0 || length > maximumFrameBytes)
            {
                throw new ClientControlProtocolException(ClientControlProtocolFailureKind.OversizedFrame);
            }

            ReliableEnvelope envelope;
            try
            {
                envelope = ReliableEnvelope.Parser.ParseFrom(frame, 0, length);
            }
            catch (InvalidProtocolBufferException)
            {
                throw new ClientControlProtocolException(ClientControlProtocolFailureKind.MalformedPayload);
            }

            if (envelope.ProtocolVersion != 1 ||
                envelope.Kind != MessageKind.Push ||
                envelope.RequestId.Length != 0 ||
                envelope.CommandId.Length != 0 ||
                envelope.TimestampMs <= 0)
            {
                throw new ClientControlProtocolException(ClientControlProtocolFailureKind.InvalidEnvelope);
            }

            if (expectedSequence == 0 || envelope.Sequence != expectedSequence)
            {
                throw new ClientControlProtocolException(ClientControlProtocolFailureKind.InvalidSequence);
            }

            if (!_catalog.TryGet(envelope.MessageId, out var route))
            {
                throw new ClientControlProtocolException(ClientControlProtocolFailureKind.UnknownRoute);
            }

            if (length > route.MaximumFrameBytes)
            {
                throw new ClientControlProtocolException(ClientControlProtocolFailureKind.OversizedFrame);
            }

            IMessage payload;
            try
            {
                payload = route.ParsePayload(envelope.Payload);
            }
            catch (InvalidProtocolBufferException)
            {
                throw new ClientControlProtocolException(ClientControlProtocolFailureKind.MalformedPayload);
            }

            if (payload == null || payload.Descriptor.FullName != route.ProtobufName)
            {
                throw new ClientControlProtocolException(ClientControlProtocolFailureKind.MalformedPayload);
            }

            return new ClientControlPush(
                route,
                envelope.Sequence,
                envelope.TimestampMs,
                payload);
        }
    }
}
