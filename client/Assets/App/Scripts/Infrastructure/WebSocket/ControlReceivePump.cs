using System;
using System.Net.WebSockets;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Control;

namespace IHomeland.Client.Infrastructure.WebSocket
{
    /// <summary>标识单次 Control connection 的封闭终态。</summary>
    internal enum ControlAttemptOutcomeKind
    {
        /// <summary>当前 operation 被显式取消。</summary>
        Requested = 0,
        /// <summary>Socket connect/receive 出现可恢复 transport failure。</summary>
        TransportFailure = 1,
        /// <summary>Peer 在无失效语义时关闭连接。</summary>
        PeerClosed = 2,
        /// <summary>Frame、envelope、route 或 subprotocol 违反冻结合同。</summary>
        ProtocolFailure = 3,
        /// <summary>主线程有界队列拒绝 PUSH。</summary>
        MainThreadBackpressure = 4,
        /// <summary>Session owner 已提交失效。</summary>
        SessionInvalidated = 5,
        /// <summary>旧连接试图失效已替换的 Session generation。</summary>
        SupersededConnection = 6,
        /// <summary>配置、ticket 或握手 policy 拒绝连接。</summary>
        PolicyRejected = 7,
    }

    /// <summary>
    /// 拥有单个 Control connection 的 fragment buffer、frame cap、sequence 与 peer-close 分类。
    /// </summary>
    internal sealed class ControlReceivePump
    {
        /// <summary>严格解码冻结 Control envelope。</summary>
        private readonly ClientControlCodec _codec;

        /// <summary>把 wire payload 映射为 typed Application notification。</summary>
        private readonly ClientControlProtocolAdapter _protocolAdapter;

        /// <summary>创建无连接所有权的 receive pump。</summary>
        internal ControlReceivePump(
            ClientControlCodec codec,
            ClientControlProtocolAdapter protocolAdapter)
        {
            _codec = codec ?? throw new ArgumentNullException(nameof(codec));
            _protocolAdapter = protocolAdapter ??
                throw new ArgumentNullException(nameof(protocolAdapter));
        }

        /// <summary>运行到取消、peer close 或一个稳定 terminal outcome。</summary>
        internal async Task<ControlAttemptOutcomeKind> RunAsync(
            IClientWebSocket socket,
            long connectionGeneration,
            long sourceSessionGeneration,
            int maximumFrameBytes,
            Func<long, ulong, Task<bool>> invalidateSession,
            Action<long> invalidateGameplay,
            Func<long, ClientControlNotification, bool> postPush,
            CancellationToken cancellationToken)
        {
            if (socket == null)
            {
                throw new ArgumentNullException(nameof(socket));
            }

            if (maximumFrameBytes <= 0)
            {
                return ControlAttemptOutcomeKind.PolicyRejected;
            }

            if (invalidateSession == null)
            {
                throw new ArgumentNullException(nameof(invalidateSession));
            }

            if (postPush == null)
            {
                throw new ArgumentNullException(nameof(postPush));
            }

            var frame = new byte[maximumFrameBytes];
            ulong expectedSequence = 1;
            while (true)
            {
                var length = 0;
                while (true)
                {
                    if (length >= frame.Length)
                    {
                        return ControlAttemptOutcomeKind.ProtocolFailure;
                    }

                    var result = await socket.ReceiveAsync(
                        new ArraySegment<byte>(frame, length, frame.Length - length),
                        cancellationToken);
                    if (result.MessageType == WebSocketMessageType.Close)
                    {
                        return IsProtocolClose(result.CloseStatus)
                            ? ControlAttemptOutcomeKind.ProtocolFailure
                            : ControlAttemptOutcomeKind.PeerClosed;
                    }

                    if (result.MessageType != WebSocketMessageType.Binary ||
                        result.Count < 0 ||
                        result.Count > frame.Length - length ||
                        (result.Count == 0 && !result.EndOfMessage))
                    {
                        return ControlAttemptOutcomeKind.ProtocolFailure;
                    }

                    length += result.Count;
                    if (!result.EndOfMessage)
                    {
                        continue;
                    }

                    break;
                }

                ClientControlNotification push;
                try
                {
                    push = _protocolAdapter.Map(
                        _codec.Decode(
                            frame,
                            length,
                            maximumFrameBytes,
                            expectedSequence));
                }
                catch (ClientControlProtocolException)
                {
                    return ControlAttemptOutcomeKind.ProtocolFailure;
                }

                expectedSequence++;
                if (push is ClientSessionInvalidationPush invalidation)
                {
                    var invalidated = await invalidateSession(
                        sourceSessionGeneration,
                        invalidation.SessionEpoch);
                    if (invalidated)
                    {
                        invalidateGameplay?.Invoke(sourceSessionGeneration);
                    }

                    _ = postPush(connectionGeneration, push);
                    return invalidated
                        ? ControlAttemptOutcomeKind.SessionInvalidated
                        : ControlAttemptOutcomeKind.SupersededConnection;
                }

                if (!postPush(connectionGeneration, push))
                {
                    return ControlAttemptOutcomeKind.MainThreadBackpressure;
                }
            }
        }

        /// <summary>判断 close code 是否为不可恢复的协议或 policy failure。</summary>
        private static bool IsProtocolClose(WebSocketCloseStatus? closeStatus)
        {
            return closeStatus == WebSocketCloseStatus.ProtocolError ||
                   closeStatus == WebSocketCloseStatus.InvalidMessageType ||
                   closeStatus == WebSocketCloseStatus.InvalidPayloadData ||
                   closeStatus == WebSocketCloseStatus.PolicyViolation ||
                   closeStatus == WebSocketCloseStatus.MessageTooBig ||
                   closeStatus == WebSocketCloseStatus.MandatoryExtension;
        }
    }
}
