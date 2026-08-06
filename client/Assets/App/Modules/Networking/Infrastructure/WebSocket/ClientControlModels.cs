using System;
using System.Net.WebSockets;
using Google.Protobuf;

namespace IHomeland.Client.Networking.Infrastructure.WebSocket
{
    /// <summary>
    /// 标识 control envelope 被拒绝时的稳定协议原因。
    /// </summary>
    internal enum ClientControlProtocolFailureKind
    {
        /// <summary>
        /// 完整 frame 超过公开或 route 字节预算。
        /// </summary>
        OversizedFrame = 0,

        /// <summary>
        /// Envelope 或 generated payload 不是合法 Protobuf。
        /// </summary>
        MalformedPayload = 1,

        /// <summary>
        /// Envelope 的版本、kind、correlation 或 timestamp 无效。
        /// </summary>
        InvalidEnvelope = 2,

        /// <summary>
        /// Message ID 未登记为 WSS control PUSH。
        /// </summary>
        UnknownRoute = 3,

        /// <summary>
        /// 当前连接 sequence 不是从 1 开始严格连续递增。
        /// </summary>
        InvalidSequence = 4,
    }

    /// <summary>
    /// 表示不携带原始 frame、payload 或底层异常文本的 control 协议失败。
    /// </summary>
    internal sealed class ClientControlProtocolException : Exception
    {
        /// <summary>
        /// 创建只公开稳定 failure kind 的协议异常。
        /// </summary>
        /// <param name="failureKind">安全且低基数的失败分类。</param>
        internal ClientControlProtocolException(ClientControlProtocolFailureKind failureKind)
            : base($"WSS control 协议校验失败：{failureKind}。")
        {
            FailureKind = failureKind;
        }

        /// <summary>
        /// 获取安全且不包含 payload 的失败分类。
        /// </summary>
        internal ClientControlProtocolFailureKind FailureKind { get; }
    }

    /// <summary>
    /// 保存单个 WSS control route 的冻结 message/type/size 映射。
    /// </summary>
    internal sealed class ClientControlRoute
    {
        /// <summary>
        /// 保存精确 generated payload parser，禁止 unknown type fallback。
        /// </summary>
        private readonly Func<ByteString, IMessage> _parsePayload;

        /// <summary>
        /// 创建单个封闭 control route。
        /// </summary>
        /// <param name="messageID">共享 registry 中的稳定 message ID。</param>
        /// <param name="name">共享 registry 中的稳定 route 名称。</param>
        /// <param name="protobufName">Generated descriptor 的完整 Protobuf 名称。</param>
        /// <param name="maximumFrameBytes">完整 encoded envelope 的 route 上限，单位为字节。</param>
        /// <param name="parsePayload">精确 generated payload parser。</param>
        /// <exception cref="ArgumentException">标量或名称不满足冻结边界时抛出。</exception>
        /// <exception cref="ArgumentNullException">Parser 为空时抛出。</exception>
        internal ClientControlRoute(
            uint messageID,
            string name,
            string protobufName,
            int maximumFrameBytes,
            Func<ByteString, IMessage> parsePayload)
        {
            if (messageID == 0)
            {
                throw new ArgumentException("Control message ID 必须为正数。", nameof(messageID));
            }

            if (string.IsNullOrWhiteSpace(name))
            {
                throw new ArgumentException("Control route 名称不能为空。", nameof(name));
            }

            if (string.IsNullOrWhiteSpace(protobufName))
            {
                throw new ArgumentException("Control Protobuf 名称不能为空。", nameof(protobufName));
            }

            if (maximumFrameBytes <= 0)
            {
                throw new ArgumentException("Control route frame 上限必须为正数。", nameof(maximumFrameBytes));
            }

            MessageID = messageID;
            Name = name;
            ProtobufName = protobufName;
            MaximumFrameBytes = maximumFrameBytes;
            _parsePayload = parsePayload ?? throw new ArgumentNullException(nameof(parsePayload));
        }

        /// <summary>
        /// 获取稳定 message ID。
        /// </summary>
        internal uint MessageID { get; }

        /// <summary>
        /// 获取稳定 registry 名称。
        /// </summary>
        internal string Name { get; }

        /// <summary>
        /// 获取精确 generated descriptor 全名。
        /// </summary>
        internal string ProtobufName { get; }

        /// <summary>
        /// 获取完整 encoded envelope 的 route 上限，单位为字节。
        /// </summary>
        internal int MaximumFrameBytes { get; }

        /// <summary>
        /// 使用 route 固定 parser 解码 generated payload。
        /// </summary>
        /// <param name="payload">Envelope 中的不可变 payload bytes。</param>
        /// <returns>精确 generated message。</returns>
        internal IMessage ParsePayload(ByteString payload)
        {
            return _parsePayload(payload);
        }
    }

    /// <summary>
    /// 保存已通过完整 WSS control 契约校验的强类型 PUSH。
    /// </summary>
    internal sealed class ClientControlWirePush
    {
        /// <summary>
        /// 创建不可变 control push。
        /// </summary>
        /// <param name="route">已匹配的封闭 route。</param>
        /// <param name="sequence">当前连接内从 1 开始严格连续的 sequence。</param>
        /// <param name="timestampMilliseconds">服务端 Unix 发送时间，单位为毫秒。</param>
        /// <param name="payload">按 route 精确解析的 generated payload。</param>
        internal ClientControlWirePush(
            ClientControlRoute route,
            ulong sequence,
            long timestampMilliseconds,
            IMessage payload)
        {
            Route = route ?? throw new ArgumentNullException(nameof(route));
            Payload = payload ?? throw new ArgumentNullException(nameof(payload));
            Sequence = sequence;
            TimestampMilliseconds = timestampMilliseconds;
        }

        /// <summary>
        /// 获取匹配的稳定 route。
        /// </summary>
        internal ClientControlRoute Route { get; }

        /// <summary>
        /// 获取当前连接内的严格连续 sequence。
        /// </summary>
        internal ulong Sequence { get; }

        /// <summary>
        /// 获取服务端 Unix 发送时间，单位为毫秒。
        /// </summary>
        internal long TimestampMilliseconds { get; }

        /// <summary>
        /// 获取强类型 generated payload；调用方必须按 Route 订阅而非猜测类型。
        /// </summary>
        internal IMessage Payload { get; }

        /// <summary>
        /// 返回不包含 payload 的安全诊断摘要。
        /// </summary>
        /// <returns>Message ID、sequence 与 timestamp。</returns>
        public override string ToString()
        {
            return $"ClientControlWirePush messageId={Route.MessageID} sequence={Sequence} timestampMs={TimestampMilliseconds}";
        }
    }

    /// <summary>
    /// 标识 control channel 的可观察生命周期状态。
    /// </summary>
    internal enum ClientControlChannelState
    {
        /// <summary>
        /// 尚未由 AppLifetime 初始化。
        /// </summary>
        Created = 0,

        /// <summary>
        /// 已初始化但没有显式运行。
        /// </summary>
        Idle = 1,

        /// <summary>
        /// 正在签发 ticket 或建立 WebSocket。
        /// </summary>
        Connecting = 2,

        /// <summary>
        /// 已协商 control subprotocol 并运行 receive pump。
        /// </summary>
        Connected = 3,

        /// <summary>
        /// 瞬时故障后正在等待有限重连。
        /// </summary>
        Recovering = 4,

        /// <summary>
        /// 当前显式运行已结束但仍允许再次启动。
        /// </summary>
        Disconnected = 5,

        /// <summary>
        /// 当前 session 已由 control PUSH 权威失效。
        /// </summary>
        SessionInvalidated = 6,

        /// <summary>
        /// App Scope 已停止且不得重新启动。
        /// </summary>
        Stopped = 7,
    }

    /// <summary>
    /// 标识最近一次 control connection 结束的稳定低敏原因。
    /// </summary>
    internal enum ClientControlCloseReason
    {
        /// <summary>
        /// 尚无关闭结果。
        /// </summary>
        None = 0,

        /// <summary>
        /// 显式运行 cancellation 或 App Scope 停止。
        /// </summary>
        Requested = 1,

        /// <summary>
        /// 可恢复的连接或 receive transport failure。
        /// </summary>
        TransportFailure = 2,

        /// <summary>
        /// Peer 在没有 session 失效语义时关闭连接。
        /// </summary>
        PeerClosed = 3,

        /// <summary>
        /// Frame、envelope、route 或 subprotocol 违反冻结契约。
        /// </summary>
        ProtocolFailure = 4,

        /// <summary>
        /// 主线程有界队列拒绝投递。
        /// </summary>
        MainThreadBackpressure = 5,

        /// <summary>
        /// Forced logout 或 session invalidation 已清除 current lineage。
        /// </summary>
        SessionInvalidated = 6,

        /// <summary>
        /// 旧连接的失效通知因 session generation 已更新而被拒绝。
        /// </summary>
        SupersededConnection = 7,

        /// <summary>
        /// 有限瞬时故障重试预算已经耗尽。
        /// </summary>
        RetryExhausted = 8,

        /// <summary>
        /// 配置、session、ticket 或握手 policy 不允许建立连接。
        /// </summary>
        PolicyRejected = 9,
    }

    /// <summary>
    /// 保存 control channel 状态、原因、run generation 与当前尝试序号的不可变快照。
    /// </summary>
    internal sealed class ClientControlChannelSnapshot
    {
        /// <summary>
        /// 创建安全状态快照。
        /// </summary>
        /// <param name="state">当前生命周期状态。</param>
        /// <param name="closeReason">最近稳定关闭原因。</param>
        /// <param name="generation">每次显式run递增的本地generation。</param>
        /// <param name="attempt">当前显式运行内从 1 开始的尝试序号；未尝试时为 0。</param>
        internal ClientControlChannelSnapshot(
            ClientControlChannelState state,
            ClientControlCloseReason closeReason,
            long generation,
            int attempt)
        {
            State = state;
            CloseReason = closeReason;
            Generation = generation;
            Attempt = attempt;
        }

        /// <summary>
        /// 获取当前生命周期状态。
        /// </summary>
        internal ClientControlChannelState State { get; }

        /// <summary>
        /// 获取最近稳定关闭原因。
        /// </summary>
        internal ClientControlCloseReason CloseReason { get; }

        /// <summary>获取阻止旧run回写的本地generation。</summary>
        internal long Generation { get; }

        /// <summary>
        /// 获取当前运行内的连接尝试序号。
        /// </summary>
        internal int Attempt { get; }

        /// <summary>
        /// 返回不包含 endpoint、ticket、session 或 payload 的安全摘要。
        /// </summary>
        /// <returns>状态、原因与尝试序号。</returns>
        public override string ToString()
        {
            return $"ClientControlChannel state={State} reason={CloseReason} generation={Generation} attempt={Attempt}";
        }
    }

    /// <summary>
    /// 保存一次底层 WebSocket receive 的平台无关结果。
    /// </summary>
    internal readonly struct ClientWebSocketReadResult
    {
        /// <summary>
        /// 创建 receive 结果。
        /// </summary>
        /// <param name="count">本次写入调用方 buffer 的字节数。</param>
        /// <param name="messageType">当前 message 类型。</param>
        /// <param name="endOfMessage">本次读取是否结束完整 WebSocket message。</param>
        /// <param name="closeStatus">Peer close 时的可选标准状态。</param>
        internal ClientWebSocketReadResult(
            int count,
            WebSocketMessageType messageType,
            bool endOfMessage,
            WebSocketCloseStatus? closeStatus)
        {
            Count = count;
            MessageType = messageType;
            EndOfMessage = endOfMessage;
            CloseStatus = closeStatus;
        }

        /// <summary>
        /// 获取本次读取字节数。
        /// </summary>
        internal int Count { get; }

        /// <summary>
        /// 获取 text、binary 或 close 类型。
        /// </summary>
        internal WebSocketMessageType MessageType { get; }

        /// <summary>
        /// 获取是否到达完整 message 末尾。
        /// </summary>
        internal bool EndOfMessage { get; }

        /// <summary>
        /// 获取 peer close 的可选标准状态；不保留不可信 description。
        /// </summary>
        internal WebSocketCloseStatus? CloseStatus { get; }
    }
}
