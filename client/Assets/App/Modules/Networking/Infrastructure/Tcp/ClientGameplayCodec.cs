using System;
using System.Collections.Generic;
using Google.Protobuf;
using IHomeland.Client.Networking.Application.Contracts;
using IHomeland.Client.Networking.Infrastructure.Http;
using IHomeland.Protocol.Common.V1;

namespace IHomeland.Client.Networking.Infrastructure.Tcp
{
    /// <summary>
    /// 保存已经通过公共 envelope 校验的 S2C 消息。
    /// </summary>
    internal sealed class ClientGameplayEnvelope
    {
        /// <summary>创建不暴露可变生成消息的安全接收投影。</summary>
        /// <param name="messageID">S2C route ID。</param>
        /// <param name="kind">Response、Error 或 Push。</param>
        /// <param name="correlationKind">Correlation 来源是 request 还是 command；push 为 Unspecified。</param>
        /// <param name="correlationID">Response/error 的 16-byte correlation；push 为空。</param>
        /// <param name="payload">有界 payload bytes 副本。</param>
        internal ClientGameplayEnvelope(
            uint messageID,
            MessageKind kind,
            MessageKind correlationKind,
            byte[] correlationID,
            byte[] payload)
        {
            MessageID = messageID;
            Kind = kind;
            CorrelationKind = correlationKind;
            CorrelationID = correlationID ?? Array.Empty<byte>();
            Payload = payload ?? throw new ArgumentNullException(nameof(payload));
        }

        /// <summary>获取 S2C route ID。</summary>
        internal uint MessageID { get; }

        /// <summary>获取 Response、Error 或 Push kind。</summary>
        internal MessageKind Kind { get; }

        /// <summary>获取 correlation 原始字段的 Request 或 Command 语义。</summary>
        internal MessageKind CorrelationKind { get; }

        /// <summary>获取 correlation bytes 副本。</summary>
        internal byte[] CorrelationID { get; }

        /// <summary>获取待 descriptor parser 解码的 payload bytes。</summary>
        internal byte[] Payload { get; }
    }

    /// <summary>
    /// 确定性编码 C2S envelope，并在 payload 解析前验证 S2C 公共不变量。
    /// </summary>
    internal sealed class ClientGameplayCodec
    {
        /// <summary>当前唯一支持的 ReliableEnvelope protocol version。</summary>
        internal const uint ProtocolVersion = 1;

        /// <summary>编码一个强类型 request/command envelope body。</summary>
        /// <typeparam name="TRequest">生成 request/command payload。</typeparam>
        /// <typeparam name="TResponse">Descriptor 配对的 response 类型。</typeparam>
        /// <param name="operation">冻结 route descriptor。</param>
        /// <param name="payload">非空生成 payload。</param>
        /// <param name="correlationID">CSPRNG 16-byte ID。</param>
        /// <param name="sequence">从 1 开始严格递增的 C2S sequence。</param>
        /// <param name="timestampMilliseconds">正数 Unix 时间，单位为毫秒。</param>
        /// <returns>不含 4-byte prefix 的确定性 envelope bytes。</returns>
        internal byte[] Encode<TRequest, TResponse>(
            ClientGameplayOperation<TRequest, TResponse> operation,
            TRequest payload,
            byte[] correlationID,
            ulong sequence,
            long timestampMilliseconds)
            where TRequest : class, IMessage<TRequest>
            where TResponse : class, IMessage<TResponse>
        {
            if (operation == null || payload == null || correlationID == null ||
                correlationID.Length != 16 || sequence == 0 || timestampMilliseconds <= 0)
            {
                throw new ClientGameplayProtocolException("Gameplay outbound envelope 输入无效。");
            }

            var envelope = new ReliableEnvelope
            {
                ProtocolVersion = ProtocolVersion,
                MessageId = operation.RequestMessageID,
                Kind = operation.RequestKind,
                Sequence = sequence,
                TimestampMs = timestampMilliseconds,
                Payload = payload.ToByteString(),
            };
            if (operation.RequestKind == MessageKind.Request)
            {
                envelope.RequestId = ByteString.CopyFrom(correlationID);
            }
            else
            {
                envelope.CommandId = ByteString.CopyFrom(correlationID);
            }

            var encoded = envelope.ToByteArray();
            if (encoded.Length > operation.RequestMaximumBytes || encoded.Length > ClientGameplayFramer.MaximumFrameBytes)
            {
                throw new ClientGameplayProtocolException("Gameplay outbound envelope 超出 route 预算。");
            }

            return encoded;
        }

        /// <summary>解码并验证一个 S2C envelope 的公共字段与 route budget。</summary>
        /// <param name="encoded">不含 frame prefix 的完整 envelope。</param>
        /// <param name="expectedSequence">当前 connection 下一个 S2C sequence。</param>
        /// <returns>待 pending 或 push descriptor 进一步解析的安全投影。</returns>
        internal ClientGameplayEnvelope Decode(byte[] encoded, ulong expectedSequence)
        {
            if (encoded == null || encoded.Length == 0 ||
                encoded.Length > ClientGameplayFramer.MaximumFrameBytes || expectedSequence == 0)
            {
                throw new ClientGameplayProtocolException("Gameplay inbound envelope 长度或 sequence 无效。");
            }

            ReliableEnvelope envelope;
            try
            {
                envelope = ReliableEnvelope.Parser.ParseFrom(encoded);
            }
            catch (InvalidProtocolBufferException error)
            {
                throw new ClientGameplayProtocolException("Gameplay envelope Protobuf 无效。", error);
            }

            if (envelope.ProtocolVersion != ProtocolVersion ||
                envelope.Sequence != expectedSequence ||
                envelope.TimestampMs <= 0)
            {
                throw new ClientGameplayProtocolException("Gameplay envelope version、sequence 或 timestamp 无效。");
            }

            if (envelope.Kind == MessageKind.Push)
            {
                ValidatePush(envelope, encoded.Length);
                return new ClientGameplayEnvelope(
                    envelope.MessageId,
                    envelope.Kind,
                    MessageKind.Unspecified,
                    Array.Empty<byte>(),
                    envelope.Payload.ToByteArray());
            }

            if (envelope.Kind != MessageKind.Response && envelope.Kind != MessageKind.Error)
            {
                throw new ClientGameplayProtocolException("Gameplay S2C kind 无效。");
            }

            if (!ClientGameplayCatalog.TryGetResponseMaximum(envelope.MessageId, out var maximumBytes) ||
                encoded.Length > maximumBytes)
            {
                throw new ClientGameplayProtocolException("Gameplay response route 或预算无效。");
            }

            var hasRequest = envelope.RequestId.Length == 16 && envelope.CommandId.Length == 0;
            var hasCommand = envelope.CommandId.Length == 16 && envelope.RequestId.Length == 0;
            if (!hasRequest && !hasCommand)
            {
                throw new ClientGameplayProtocolException("Gameplay response correlation 无效。");
            }

            return new ClientGameplayEnvelope(
                envelope.MessageId,
                envelope.Kind,
                hasRequest ? MessageKind.Request : MessageKind.Command,
                (hasRequest ? envelope.RequestId : envelope.CommandId).ToByteArray(),
                envelope.Payload.ToByteArray());
        }

        /// <summary>使用 operation 的固定 parser 解码 response payload。</summary>
        /// <typeparam name="TRequest">Descriptor 的生成 request/command 类型。</typeparam>
        /// <typeparam name="TResponse">Descriptor 的生成 response 类型。</typeparam>
        /// <param name="operation">提供唯一 response ID 与 parser 的冻结 descriptor。</param>
        /// <param name="envelope">已通过公共字段、budget 与 correlation 校验的 response。</param>
        /// <returns>只按 descriptor 声明类型解析的 generated response。</returns>
        internal TResponse DecodeResponse<TRequest, TResponse>(
            ClientGameplayOperation<TRequest, TResponse> operation,
            ClientGameplayEnvelope envelope)
            where TRequest : class, IMessage<TRequest>
            where TResponse : class, IMessage<TResponse>
        {
            if (operation == null || envelope == null ||
                envelope.MessageID != operation.ResponseMessageID ||
                envelope.Kind != MessageKind.Response)
            {
                throw new ClientGameplayProtocolException("Gameplay response descriptor 不匹配。");
            }

            try
            {
                return operation.ResponseParser.ParseFrom(envelope.Payload);
            }
            catch (InvalidProtocolBufferException error)
            {
                throw new ClientGameplayProtocolException("Gameplay response payload 无效。", error);
            }
        }

        /// <summary>解码共享 ErrorPayload 并拒绝不可信 message key/correlation 漂移。</summary>
        /// <param name="envelope">已验证为 Error 的 envelope。</param>
        /// <returns>结构有效的公开错误 payload。</returns>
        internal ClientServerError DecodeError(ClientGameplayEnvelope envelope)
        {
            if (envelope == null || envelope.Kind != MessageKind.Error)
            {
                throw new ClientGameplayProtocolException("Gameplay error kind 无效。");
            }

            try
            {
                var error = ErrorPayload.Parser.ParseFrom(envelope.Payload);
                if (error.Code == 0 || error.Code > int.MaxValue ||
                    !ClientErrorRegistry.TryGet((int)error.Code, out var knownError) ||
                    !string.Equals(error.MessageKey, knownError.MessageKey, StringComparison.Ordinal) ||
                    error.Retryable != knownError.Retryable ||
                    error.RetryAfterMs > 60000 ||
                    (!knownError.Retryable && error.RetryAfterMs != 0))
                {
                    throw new ClientGameplayProtocolException("Gameplay ErrorPayload 与冻结 error registry 不一致。");
                }

                if (error.Details.Count > 16)
                {
                    throw new ClientGameplayProtocolException("Gameplay ErrorPayload details 超出上限。");
                }

                foreach (var detail in error.Details)
                {
                    if (!IsSafeErrorToken(detail.Field) || !IsSafeErrorToken(detail.Reason))
                    {
                        throw new ClientGameplayProtocolException("Gameplay ErrorPayload details 字段无效。");
                    }
                }

                var requestCorrelation = envelope.CorrelationKind == MessageKind.Request;
                if ((requestCorrelation &&
                     (error.RequestId.Length != 16 ||
                      !error.RequestId.Span.SequenceEqual(envelope.CorrelationID))) ||
                    (!requestCorrelation && error.RequestId.Length != 0))
                {
                    throw new ClientGameplayProtocolException("Gameplay ErrorPayload correlation 不一致。");
                }

                var details = new List<ClientErrorDetail>(error.Details.Count);
                foreach (var detail in error.Details)
                {
                    details.Add(new ClientErrorDetail(detail.Field, detail.Reason));
                }

                return new ClientServerError(
                    checked((int)error.Code),
                    knownError.Category,
                    error.MessageKey,
                    error.RequestId.Length == 0
                        ? string.Empty
                        : Convert.ToBase64String(error.RequestId.ToByteArray()),
                    error.Retryable,
                    error.RetryAfterMs == 0
                        ? (TimeSpan?)null
                        : TimeSpan.FromMilliseconds(error.RetryAfterMs),
                    details);
            }
            catch (InvalidProtocolBufferException parseError)
            {
                throw new ClientGameplayProtocolException("Gameplay ErrorPayload Protobuf 无效。", parseError);
            }
        }

        /// <summary>验证允许为空且不超过 64 字符的公开 error detail token。</summary>
        /// <param name="value">公开字段名或稳定 reason key。</param>
        /// <returns>只包含 ASCII 字母、数字、点、下划线、冒号或连字符时返回 true。</returns>
        private static bool IsSafeErrorToken(string value)
        {
            if (value == null || value.Length > 64)
            {
                return false;
            }

            foreach (var character in value)
            {
                var isAsciiLetterOrDigit =
                    (character >= 'A' && character <= 'Z') ||
                    (character >= 'a' && character <= 'z') ||
                    (character >= '0' && character <= '9');
                if (!isAsciiLetterOrDigit &&
                    character != '.' && character != '_' && character != ':' && character != '-')
                {
                    return false;
                }
            }

            return true;
        }

        /// <summary>验证 PUSH route、correlation 空值与完整 envelope 上限。</summary>
        /// <param name="envelope">生成 envelope。</param>
        /// <param name="encodedBytes">完整 envelope 字节数。</param>
        private static void ValidatePush(ReliableEnvelope envelope, int encodedBytes)
        {
            if (!ClientGameplayCatalog.TryGetPushMaximum(envelope.MessageId, out var maximum) ||
                encodedBytes > maximum ||
                envelope.RequestId.Length != 0 || envelope.CommandId.Length != 0)
            {
                throw new ClientGameplayProtocolException("Gameplay PUSH route、budget 或 correlation 无效。");
            }
        }
    }
}
