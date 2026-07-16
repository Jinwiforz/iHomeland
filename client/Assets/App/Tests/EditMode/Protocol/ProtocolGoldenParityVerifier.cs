using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Reflection;
using System.Security.Cryptography;
using System.Text;
using Google.Protobuf;
using Google.Protobuf.Reflection;
using IHomeland.Protocol.Common.V1;
using UnityEngine;

namespace IHomeland.Client.Tests.EditMode.Protocol
{
    /// <summary>
    /// 使用生成 descriptor 验证 C# 与 Go 共享 fixture 的字节、JSON 和路由身份。
    /// </summary>
    internal static class ProtocolGoldenParityVerifier
    {
        /// <summary>
        /// 定义当前工具唯一理解的 fixture 与 registry 结构代际。
        /// </summary>
        private const int SupportedSchemaVersion = 1;

        /// <summary>
        /// 使用 proto field name 且不输出未设置默认值，保持与 Go fixture formatter 一致。
        /// </summary>
        private static readonly JsonFormatter FixtureJsonFormatter = new JsonFormatter(
            JsonFormatter.Settings.Default.WithPreserveProtoFieldNames(true));

        /// <summary>
        /// 从只读 JSON 文件加载并严格检查 golden manifest 的必需结构。
        /// </summary>
        /// <param name="path">仓库内 golden.json 的绝对路径。</param>
        /// <returns>可供单次 parity 验证消费的 manifest 快照。</returns>
        /// <exception cref="FileNotFoundException">输入文件不存在时抛出。</exception>
        /// <exception cref="InvalidDataException">文件为空或结构代际、packet 集合无效时抛出。</exception>
        internal static GoldenManifest LoadGoldenManifest(string path)
        {
            var manifest = JsonUtility.FromJson<GoldenManifest>(ReadRequiredText(path));
            if (manifest == null || manifest.schemaVersion != SupportedSchemaVersion ||
                manifest.packets == null || manifest.packets.Length == 0)
            {
                throw new InvalidDataException("实时 golden manifest 缺少受支持的 schemaVersion 或 packet。");
            }

            return manifest;
        }

        /// <summary>
        /// 从只读 JSON 文件加载并严格检查 message registry 的必需结构。
        /// </summary>
        /// <param name="path">仓库内 messages.json 的绝对路径。</param>
        /// <returns>可供单次 parity 验证消费的 registry 快照。</returns>
        /// <exception cref="FileNotFoundException">输入文件不存在时抛出。</exception>
        /// <exception cref="InvalidDataException">文件为空或结构代际、message 集合无效时抛出。</exception>
        internal static MessageRegistryDocument LoadMessageRegistry(string path)
        {
            var registry = JsonUtility.FromJson<MessageRegistryDocument>(ReadRequiredText(path));
            if (registry == null || registry.schemaVersion != SupportedSchemaVersion ||
                registry.messages == null || registry.messages.Length == 0)
            {
                throw new InvalidDataException("实时 message registry 缺少受支持的 schemaVersion 或 message。");
            }

            return registry;
        }

        /// <summary>
        /// 从生成程序集公开的 FileDescriptor 建立消息全名索引，不维护第二份消息 switch。
        /// </summary>
        /// <param name="assembly">固定名称的生成协议程序集。</param>
        /// <returns>覆盖顶层和嵌套消息的只读 descriptor 索引。</returns>
        /// <exception cref="ArgumentNullException">程序集引用为空时抛出。</exception>
        /// <exception cref="InvalidDataException">程序集没有 descriptor 或包含重复消息全名时抛出。</exception>
        internal static IReadOnlyDictionary<string, MessageDescriptor> BuildDescriptorIndex(Assembly assembly)
        {
            if (assembly == null)
            {
                throw new ArgumentNullException(nameof(assembly));
            }

            var files = assembly.GetTypes()
                .Select(type => type.GetProperty("Descriptor", BindingFlags.Public | BindingFlags.Static))
                .Where(property => property != null && property.PropertyType == typeof(FileDescriptor))
                .Select(property => (FileDescriptor)property.GetValue(null))
                .Where(descriptor => descriptor != null)
                .ToArray();
            return BuildDescriptorIndex(files);
        }

        /// <summary>
        /// 从显式文件集合建立索引，并把重复 fully-qualified name 视为生成边界损坏。
        /// </summary>
        /// <param name="files">需要合并的 Protobuf 文件 descriptor。</param>
        /// <returns>唯一消息全名到 descriptor 的映射。</returns>
        /// <exception cref="ArgumentNullException">文件集合为空引用时抛出。</exception>
        /// <exception cref="InvalidDataException">文件为空项、没有消息或包含重复消息全名时抛出。</exception>
        internal static IReadOnlyDictionary<string, MessageDescriptor> BuildDescriptorIndex(
            IEnumerable<FileDescriptor> files)
        {
            if (files == null)
            {
                throw new ArgumentNullException(nameof(files));
            }

            var descriptors = new Dictionary<string, MessageDescriptor>(StringComparer.Ordinal);
            foreach (var file in files)
            {
                if (file == null)
                {
                    throw new InvalidDataException("生成程序集公开了空 FileDescriptor。");
                }

                AddMessageDescriptors(file.MessageTypes, descriptors);
            }

            if (descriptors.Count == 0)
            {
                throw new InvalidDataException("生成协议程序集没有公开任何 MessageDescriptor。");
            }

            return descriptors;
        }

        /// <summary>
        /// 对完整 manifest 执行 payload、JSON、envelope、摘要、registry 与覆盖一致性验证。
        /// </summary>
        /// <param name="manifest">Go fixture generator 拥有的 golden 快照。</param>
        /// <param name="registry">实时消息唯一登记快照。</param>
        /// <param name="descriptors">从本次生成程序集发现的 descriptor 索引。</param>
        /// <exception cref="ArgumentNullException">任一输入为空引用时抛出。</exception>
        /// <exception cref="InvalidDataException">packet、registry、覆盖或跨语言编码事实不一致时抛出。</exception>
        internal static void Verify(
            GoldenManifest manifest,
            MessageRegistryDocument registry,
            IReadOnlyDictionary<string, MessageDescriptor> descriptors)
        {
            if (manifest == null)
            {
                throw new ArgumentNullException(nameof(manifest));
            }

            if (registry == null)
            {
                throw new ArgumentNullException(nameof(registry));
            }

            if (descriptors == null)
            {
                throw new ArgumentNullException(nameof(descriptors));
            }

            if (manifest.packets == null || manifest.packets.Length == 0 ||
                registry.messages == null || registry.messages.Length == 0)
            {
                throw new InvalidDataException("parity 输入缺少 packet 或 message registry 条目。");
            }

            var registryById = BuildRegistryIndex(registry.messages);
            var seenMessageIds = new HashSet<int>();
            var seenKinds = new HashSet<string>(StringComparer.Ordinal);
            var sawStandalonePayload = false;
            var sawControlPush = false;
            foreach (var packet in manifest.packets)
            {
                VerifyPacket(
                    packet,
                    registryById,
                    descriptors,
                    seenMessageIds,
                    seenKinds,
                    ref sawStandalonePayload,
                    ref sawControlPush);
            }

            foreach (var entry in registry.messages.Where(entry => entry.owner == "world" || entry.owner == "visit"))
            {
                if (!seenMessageIds.Contains(entry.id))
                {
                    throw new InvalidDataException($"golden manifest 未覆盖 {entry.owner} message id {entry.id}。");
                }
            }

            foreach (var requiredKind in new[] { "REQUEST", "RESPONSE", "COMMAND", "PUSH" })
            {
                if (!seenKinds.Contains(requiredKind))
                {
                    throw new InvalidDataException($"golden manifest 未覆盖 message kind {requiredKind}。");
                }
            }

            if (!sawStandalonePayload || !sawControlPush)
            {
                throw new InvalidDataException("golden manifest 必须覆盖独立 payload 与代表性 control push。");
            }
        }

        /// <summary>
        /// 深度优先登记顶层与嵌套消息，重复全名直接失败而不按遍历顺序覆盖。
        /// </summary>
        /// <param name="messages">当前 descriptor 层级的消息集合。</param>
        /// <param name="destination">本次生成程序集的唯一索引。</param>
        /// <exception cref="InvalidDataException">消息全名重复时抛出。</exception>
        private static void AddMessageDescriptors(
            IEnumerable<MessageDescriptor> messages,
            IDictionary<string, MessageDescriptor> destination)
        {
            foreach (var message in messages)
            {
                if (destination.ContainsKey(message.FullName))
                {
                    throw new InvalidDataException($"重复 MessageDescriptor：{message.FullName}。");
                }

                destination.Add(message.FullName, message);
                AddMessageDescriptors(message.NestedTypes, destination);
            }
        }

        /// <summary>
        /// 构建消息编号索引并拒绝零值、重复编号或缺失协议身份。
        /// </summary>
        /// <param name="entries">message registry 的完整条目集合。</param>
        /// <returns>消息编号到登记事实的只读映射。</returns>
        /// <exception cref="ArgumentNullException">条目集合为空引用时抛出。</exception>
        /// <exception cref="InvalidDataException">条目不完整或消息编号重复时抛出。</exception>
        private static IReadOnlyDictionary<int, MessageRegistryEntry> BuildRegistryIndex(
            IEnumerable<MessageRegistryEntry> entries)
        {
            if (entries == null)
            {
                throw new ArgumentNullException(nameof(entries));
            }

            var result = new Dictionary<int, MessageRegistryEntry>();
            foreach (var entry in entries)
            {
                if (entry == null || entry.id <= 0 || string.IsNullOrWhiteSpace(entry.protobuf) ||
                    string.IsNullOrWhiteSpace(entry.owner) || string.IsNullOrWhiteSpace(entry.kind))
                {
                    throw new InvalidDataException("message registry 包含不完整条目。");
                }

                if (result.ContainsKey(entry.id))
                {
                    throw new InvalidDataException($"message registry 包含重复 id {entry.id}。");
                }

                result.Add(entry.id, entry);
            }

            return result;
        }

        /// <summary>
        /// 验证单条 packet，并把 envelope 路由覆盖写入本次 manifest 的局部集合。
        /// </summary>
        /// <param name="packet">待验证的冻结 packet。</param>
        /// <param name="registryById">消息编号唯一索引。</param>
        /// <param name="descriptors">生成消息 descriptor 索引。</param>
        /// <param name="seenMessageIds">已通过验证的可靠消息编号。</param>
        /// <param name="seenKinds">已通过验证的 message kind。</param>
        /// <param name="sawStandalonePayload">是否已经验证 message id 为零的共享 payload。</param>
        /// <param name="sawControlPush">是否已经验证 control owner 的代表性 push。</param>
        /// <exception cref="InvalidDataException">packet 结构、编码、摘要、路由或覆盖事实不一致时抛出。</exception>
        private static void VerifyPacket(
            GoldenPacket packet,
            IReadOnlyDictionary<int, MessageRegistryEntry> registryById,
            IReadOnlyDictionary<string, MessageDescriptor> descriptors,
            ISet<int> seenMessageIds,
            ISet<string> seenKinds,
            ref bool sawStandalonePayload,
            ref bool sawControlPush)
        {
            if (packet == null || string.IsNullOrWhiteSpace(packet.name) ||
                string.IsNullOrWhiteSpace(packet.protobuf) || string.IsNullOrWhiteSpace(packet.sha256))
            {
                throw new InvalidDataException("golden manifest 包含不完整 packet。");
            }

            if (!descriptors.TryGetValue(packet.protobuf, out var descriptor))
            {
                throw new InvalidDataException($"packet {packet.name} 缺少 C# descriptor {packet.protobuf}。");
            }

            var payload = DecodeBase64(packet.payloadBase64, packet.name, "payloadBase64");
            IMessage message;
            try
            {
                message = descriptor.Parser.ParseFrom(payload);
            }
            catch (Exception exception)
            {
                throw new InvalidDataException($"packet {packet.name} payload 无法由 {packet.protobuf} 解析。", exception);
            }

            if (!payload.SequenceEqual(message.ToByteArray()))
            {
                throw new InvalidDataException($"packet {packet.name} payload 重编码字节发生变化。");
            }

            IMessage jsonMessage;
            try
            {
                jsonMessage = JsonParser.Default.Parse(packet.payloadJson, descriptor);
            }
            catch (Exception exception)
            {
                throw new InvalidDataException($"packet {packet.name} payloadJson 无法由 {packet.protobuf} 严格解析。", exception);
            }

            if (!payload.SequenceEqual(jsonMessage.ToByteArray()))
            {
                throw new InvalidDataException($"packet {packet.name} payloadJson 与 payload 字节语义不一致。");
            }

            var formattedJson = CompactJson(FixtureJsonFormatter.Format(message));
            if (!string.Equals(formattedJson, packet.payloadJson, StringComparison.Ordinal))
            {
                throw new InvalidDataException($"packet {packet.name} proto-name JSON 与 Go fixture 不一致。");
            }

            byte[] digestSource;
            if (packet.messageId == 0)
            {
                if (!string.IsNullOrEmpty(packet.envelopeBase64))
                {
                    throw new InvalidDataException($"standalone packet {packet.name} 不得携带 envelope。");
                }

                sawStandalonePayload = true;
                digestSource = payload;
            }
            else
            {
                if (!registryById.TryGetValue(packet.messageId, out var registryEntry) ||
                    !string.Equals(registryEntry.protobuf, packet.protobuf, StringComparison.Ordinal))
                {
                    throw new InvalidDataException($"packet {packet.name} 与 message registry 类型映射不一致。");
                }

                var envelopeBytes = DecodeBase64(packet.envelopeBase64, packet.name, "envelopeBase64");
                ReliableEnvelope envelope;
                try
                {
                    envelope = ReliableEnvelope.Parser.ParseFrom(envelopeBytes);
                }
                catch (Exception exception)
                {
                    throw new InvalidDataException($"packet {packet.name} envelope 无法解析。", exception);
                }
                if (envelope.MessageId != packet.messageId || !envelope.Payload.Span.SequenceEqual(payload))
                {
                    throw new InvalidDataException($"packet {packet.name} envelope 的 message id 或 payload 不一致。");
                }

                if (!envelopeBytes.SequenceEqual(envelope.ToByteArray()))
                {
                    throw new InvalidDataException($"packet {packet.name} envelope 重编码字节发生变化。");
                }

                var envelopeKind = envelope.Kind.ToString().ToUpperInvariant();
                if (!string.Equals(envelopeKind, registryEntry.kind, StringComparison.Ordinal))
                {
                    throw new InvalidDataException($"packet {packet.name} envelope kind 与 registry 不一致。");
                }

                seenMessageIds.Add(packet.messageId);
                seenKinds.Add(envelopeKind);
                sawControlPush |= registryEntry.owner == "control" && registryEntry.kind == "PUSH";
                digestSource = envelopeBytes;
            }

            if (!string.Equals(ComputeSHA256(digestSource), packet.sha256, StringComparison.Ordinal))
            {
                throw new InvalidDataException($"packet {packet.name} SHA-256 不一致。");
            }
        }

        /// <summary>
        /// 解码 fixture Base64，并把格式错误关联到 packet 与字段名。
        /// </summary>
        /// <param name="value">允许空字符串表示空 payload 的 Base64。</param>
        /// <param name="packetName">稳定 fixture 名称。</param>
        /// <param name="fieldName">发生错误的 JSON 字段名。</param>
        /// <returns>解码后的不可共享字节数组。</returns>
        /// <exception cref="InvalidDataException">值为空引用或不是合法 Base64 时抛出。</exception>
        private static byte[] DecodeBase64(string value, string packetName, string fieldName)
        {
            if (value == null)
            {
                throw new InvalidDataException($"packet {packetName} 缺少 {fieldName}。");
            }

            try
            {
                return Convert.FromBase64String(value);
            }
            catch (FormatException exception)
            {
                throw new InvalidDataException($"packet {packetName} 的 {fieldName} 不是合法 Base64。", exception);
            }
        }

        /// <summary>
        /// 计算与 Go fixture 相同的小写十六进制 SHA-256。
        /// </summary>
        /// <param name="value">envelope 或独立 payload 的原始字节。</param>
        /// <returns>固定 64 字符的小写摘要。</returns>
        /// <exception cref="ArgumentNullException">摘要输入为空引用时抛出。</exception>
        private static string ComputeSHA256(byte[] value)
        {
            if (value == null)
            {
                throw new ArgumentNullException(nameof(value));
            }

            using (var sha256 = SHA256.Create())
            {
                return BitConverter.ToString(sha256.ComputeHash(value))
                    .Replace("-", string.Empty)
                    .ToLowerInvariant();
            }
        }

        /// <summary>
        /// 只移除 JSON 字符串外的格式空白，使不同 formatter 排版不掩盖字段名或值漂移。
        /// </summary>
        /// <param name="value">由受信任 Protobuf formatter 产生的 JSON。</param>
        /// <returns>保留字符串内容和转义的紧凑 JSON。</returns>
        /// <exception cref="ArgumentNullException">formatter 输出为空引用时抛出。</exception>
        /// <exception cref="InvalidDataException">formatter 输出包含未闭合字符串时抛出。</exception>
        private static string CompactJson(string value)
        {
            if (value == null)
            {
                throw new ArgumentNullException(nameof(value));
            }

            var builder = new StringBuilder(value.Length);
            var insideString = false;
            var escaped = false;
            foreach (var character in value)
            {
                if (insideString)
                {
                    builder.Append(character);
                    if (escaped)
                    {
                        escaped = false;
                    }
                    else if (character == '\\')
                    {
                        escaped = true;
                    }
                    else if (character == '"')
                    {
                        insideString = false;
                    }

                    continue;
                }

                if (character == '"')
                {
                    insideString = true;
                    builder.Append(character);
                }
                else if (!char.IsWhiteSpace(character))
                {
                    builder.Append(character);
                }
            }

            if (insideString || escaped)
            {
                throw new InvalidDataException("formatter 产生了未闭合的 JSON string。");
            }

            return builder.ToString();
        }

        /// <summary>
        /// 读取必需 fixture 文件，不允许缺失、目录替代或空内容被当作合法快照。
        /// </summary>
        /// <param name="path">仓库内合同文件的绝对路径。</param>
        /// <returns>原始 UTF-8 JSON 文本。</returns>
        /// <exception cref="FileNotFoundException">路径为空或文件不存在时抛出。</exception>
        /// <exception cref="InvalidDataException">文件不包含有效文本时抛出。</exception>
        private static string ReadRequiredText(string path)
        {
            if (string.IsNullOrWhiteSpace(path) || !File.Exists(path))
            {
                throw new FileNotFoundException("协议 parity 输入文件不存在。", path);
            }

            var contents = File.ReadAllText(path);
            if (string.IsNullOrWhiteSpace(contents))
            {
                throw new InvalidDataException($"协议 parity 输入文件为空：{path}。");
            }

            return contents;
        }
    }
}
