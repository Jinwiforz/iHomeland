using System;

namespace IHomeland.Client.Tests.EditMode.Protocol
{
    /// <summary>
    /// 映射由服务端 fixture generator 拥有的实时 golden manifest。
    /// </summary>
    /// <remarks>
    /// Unity JsonUtility 只绑定字段且按名称匹配；这些 public lowerCamel 字段刻意镜像受治理 JSON，
    /// 仅存在于 Editor 测试程序集，不构成客户端运行时 API。
    /// </remarks>
    [Serializable]
    internal sealed class GoldenManifest
    {
        /// <summary>
        /// 选择 manifest JSON 的结构代际，不等同于网络协议版本。
        /// </summary>
        public int schemaVersion = 0;

        /// <summary>
        /// 保存需要由 C# runtime 逐条验证的冻结 packet。
        /// </summary>
        public GoldenPacket[] packets = Array.Empty<GoldenPacket>();
    }

    /// <summary>
    /// 映射单条 Go 生成的 payload、JSON、envelope 与摘要事实。
    /// </summary>
    /// <remarks>字段名称保持 fixture JSON 原样，避免额外 DTO 映射成为第二份契约。</remarks>
    [Serializable]
    internal sealed class GoldenPacket
    {
        /// <summary>
        /// 提供失败诊断使用的稳定 fixture 名称。
        /// </summary>
        public string name = string.Empty;

        /// <summary>
        /// 保存可靠路由编号；零表示不使用 ReliableEnvelope 的共享消息。
        /// </summary>
        public int messageId = 0;

        /// <summary>
        /// 保存 payload 对应的 Protobuf fully-qualified name。
        /// </summary>
        public string protobuf = string.Empty;

        /// <summary>
        /// 保存由 Go deterministic marshal 产生的 payload Base64。
        /// </summary>
        public string payloadBase64 = string.Empty;

        /// <summary>
        /// 保存使用 proto field name 的紧凑规范 JSON。
        /// </summary>
        public string payloadJson = string.Empty;

        /// <summary>
        /// 保存可靠消息的完整 envelope Base64；共享消息保持空字符串。
        /// </summary>
        public string envelopeBase64 = string.Empty;

        /// <summary>
        /// 保存 envelope 或共享 payload 的小写 SHA-256。
        /// </summary>
        public string sha256 = string.Empty;
    }

    /// <summary>
    /// 映射受治理的实时消息 registry 文档。
    /// </summary>
    /// <remarks>
    /// 这里只投影 parity 所需字段；JsonUtility 会忽略 registry 的其他受治理字段，完整结构仍由 Go validator 验证。
    /// </remarks>
    [Serializable]
    internal sealed class MessageRegistryDocument
    {
        /// <summary>
        /// 选择 registry JSON 的结构代际。
        /// </summary>
        public int schemaVersion = 0;

        /// <summary>
        /// 保存已经登记且允许进入可靠 envelope 的消息事实。
        /// </summary>
        public MessageRegistryEntry[] messages = Array.Empty<MessageRegistryEntry>();
    }

    /// <summary>
    /// 映射 parity 所需的最小消息身份、owner 与 kind。
    /// </summary>
    /// <remarks>字段名称保持 registry JSON 原样，类型仅用于 Editor 验收。</remarks>
    [Serializable]
    internal sealed class MessageRegistryEntry
    {
        /// <summary>
        /// 保存可靠 envelope 使用的唯一消息编号。
        /// </summary>
        public int id = 0;

        /// <summary>
        /// 保存消息所属协议 owner。
        /// </summary>
        public string owner = string.Empty;

        /// <summary>
        /// 保存消息 Protobuf fully-qualified name。
        /// </summary>
        public string protobuf = string.Empty;

        /// <summary>
        /// 保存 REQUEST、RESPONSE、COMMAND 或 PUSH 路由语义。
        /// </summary>
        public string kind = string.Empty;
    }
}
