using System;
using System.Collections.Generic;
using System.IO;
using Google.Protobuf;
using IHomeland.Protocol.Battle.V1;
using NUnit.Framework;

namespace IHomeland.Client.Tests.EditMode.Protocol
{
    /// <summary>
    /// 验证 Unity C# 对 secure、raw、KCP 与 battle Protobuf canonical wire 的 byte-exact parity。
    /// </summary>
    public sealed class BattleWireGoldenTests
    {
        /// <summary>
        /// 从共享 corpus 读取全部向量并由 C# 独立重编码，不允许维护客户端专属 golden。
        /// </summary>
        [Test]
        public void CanonicalVectorsMatchCSharpEncoding()
        {
            var vectors = LoadVectors();

            AssertBytes(vectors, "secure-raw-aad-v1", EncodeSecureHeader(1, 0x0102030405060708UL, 22));
            AssertBytes(vectors, "raw-probe-route-v1", EncodeRawRouteHeader());
            AssertBytes(vectors, "secure-kcp-aad-v1", EncodeSecureHeader(2, 0x0102030405060709UL, 52));
            AssertBytes(vectors, "kcp-push-segment-v1", EncodeKcpSegmentHeader());
            AssertBytes(vectors, "kcp-ability-route-v1", EncodeKcpRouteEnvelope());

            var probe = new BattleProbe
            {
                ProbeSequence = 1,
                LatestSnapshotSequence = 2,
                ClientMonotonicTimeUs = 3,
            };
            var probeBytes = probe.ToByteArray();
            AssertBytes(vectors, "battle-probe-protobuf-v1", probeBytes);
            Assert.That(BattleProbe.Parser.ParseFrom(probeBytes).ProbeSequence, Is.EqualTo(1UL));

            var ability = new BattleAbilityReliableEvent
            {
                EventId = 1,
                ServerTick = 2,
                SourceEntityId = 3,
                SourceEntityGeneration = 1,
                AbilityId = 7,
                Phase = BattleAbilityPhase.Started,
            };
            var abilityBytes = ability.ToByteArray();
            AssertBytes(vectors, "battle-ability-event-protobuf-v1", abilityBytes);
            Assert.That(
                BattleAbilityReliableEvent.Parser.ParseFrom(abilityBytes).Phase,
                Is.EqualTo(BattleAbilityPhase.Started));
        }

        /// <summary>
        /// 编码固定 48-byte secure header；该 header 作为 AEAD AAD，不含 tag 或 ciphertext。
        /// </summary>
        /// <param name="packetKind">raw、KCP 或 control 的受治理 packet kind。</param>
        /// <param name="packetSequence">当前 key epoch 内严格单调的 packet sequence。</param>
        /// <param name="protectedPayloadLength">ciphertext 对应明文的精确 byte count。</param>
        /// <returns>network-order secure header。</returns>
        private static byte[] EncodeSecureHeader(byte packetKind, ulong packetSequence, ushort protectedPayloadLength)
        {
            var bytes = new byte[48];
            bytes[0] = (byte)'I';
            bytes[1] = (byte)'H';
            bytes[2] = (byte)'B';
            bytes[3] = (byte)'T';
            bytes[4] = 1;
            bytes[5] = packetKind;
            for (var index = 0; index < 8; index++)
            {
                bytes[8 + index] = (byte)index;
                bytes[38 + index] = (byte)(0x10 + index);
            }

            PutBig32(bytes, 16, 9);
            PutBig32(bytes, 20, 0x01020304);
            PutBig64(bytes, 24, packetSequence);
            PutBig16(bytes, 32, protectedPayloadLength);
            PutBig32(bytes, 34, 2);
            return bytes;
        }

        /// <summary>
        /// 编码 raw probe 的固定 route header，长度只覆盖随后的 Protobuf payload。
        /// </summary>
        /// <returns>16-byte network-order raw route header。</returns>
        private static byte[] EncodeRawRouteHeader()
        {
            var bytes = new byte[16];
            PutBig32(bytes, 0, 3001);
            PutBig16(bytes, 4, 6);
            bytes[6] = 0;
            bytes[7] = 1;
            PutBig64(bytes, 8, 0x1112131415161718UL);
            return bytes;
        }

        /// <summary>
        /// 编码标准 KCP segment header；KCP 是 wire layout 中唯一使用 little-endian 的层。
        /// </summary>
        /// <returns>24-byte KCP push segment header。</returns>
        private static byte[] EncodeKcpSegmentHeader()
        {
            var bytes = new byte[24];
            PutLittle32(bytes, 0, 0x01020304);
            bytes[4] = 81;
            bytes[5] = 0;
            PutLittle16(bytes, 6, 64);
            PutLittle32(bytes, 8, 0x0a0b0c0d);
            PutLittle32(bytes, 12, 1);
            PutLittle32(bytes, 16, 0);
            PutLittle32(bytes, 20, 28);
            return bytes;
        }

        /// <summary>
        /// 编码 KCP 重组完成后的 logical route envelope，KCP 分片外不泄漏 transport 字段。
        /// </summary>
        /// <returns>16-byte network-order KCP route envelope。</returns>
        private static byte[] EncodeKcpRouteEnvelope()
        {
            var bytes = new byte[16];
            PutBig32(bytes, 0, 3004);
            PutBig16(bytes, 4, 12);
            PutBig16(bytes, 6, 0);
            PutBig64(bytes, 8, 0x2122232425262728UL);
            return bytes;
        }

        /// <summary>
        /// 写入 network-order uint16；调用方保证目标至少还有两个 bytes。
        /// </summary>
        /// <param name="destination">拥有结果的目标 byte array。</param>
        /// <param name="offset">首个写入位置。</param>
        /// <param name="value">待编码无符号值。</param>
        private static void PutBig16(byte[] destination, int offset, ushort value)
        {
            destination[offset] = (byte)(value >> 8);
            destination[offset + 1] = (byte)value;
        }

        /// <summary>
        /// 写入 network-order uint32；调用方保证目标至少还有四个 bytes。
        /// </summary>
        /// <param name="destination">拥有结果的目标 byte array。</param>
        /// <param name="offset">首个写入位置。</param>
        /// <param name="value">待编码无符号值。</param>
        private static void PutBig32(byte[] destination, int offset, uint value)
        {
            destination[offset] = (byte)(value >> 24);
            destination[offset + 1] = (byte)(value >> 16);
            destination[offset + 2] = (byte)(value >> 8);
            destination[offset + 3] = (byte)value;
        }

        /// <summary>
        /// 写入 network-order uint64；调用方保证目标至少还有八个 bytes。
        /// </summary>
        /// <param name="destination">拥有结果的目标 byte array。</param>
        /// <param name="offset">首个写入位置。</param>
        /// <param name="value">待编码无符号值。</param>
        private static void PutBig64(byte[] destination, int offset, ulong value)
        {
            for (var index = 0; index < 8; index++)
            {
                destination[offset + index] = (byte)(value >> ((7 - index) * 8));
            }
        }

        /// <summary>
        /// 写入 KCP little-endian uint16；不得复用于 secure 或 route header。
        /// </summary>
        /// <param name="destination">拥有结果的目标 byte array。</param>
        /// <param name="offset">首个写入位置。</param>
        /// <param name="value">待编码无符号值。</param>
        private static void PutLittle16(byte[] destination, int offset, ushort value)
        {
            destination[offset] = (byte)value;
            destination[offset + 1] = (byte)(value >> 8);
        }

        /// <summary>
        /// 写入 KCP little-endian uint32；不得复用于 secure 或 route header。
        /// </summary>
        /// <param name="destination">拥有结果的目标 byte array。</param>
        /// <param name="offset">首个写入位置。</param>
        /// <param name="value">待编码无符号值。</param>
        private static void PutLittle32(byte[] destination, int offset, uint value)
        {
            destination[offset] = (byte)value;
            destination[offset + 1] = (byte)(value >> 8);
            destination[offset + 2] = (byte)(value >> 16);
            destination[offset + 3] = (byte)(value >> 24);
        }

        /// <summary>
        /// 对指定共享 vector 执行 byte-exact 断言，并把 vector id 保留在失败消息中。
        /// </summary>
        /// <param name="vectors">当前 canonical corpus 的唯一向量索引。</param>
        /// <param name="vectorId">受治理的稳定 vector identity。</param>
        /// <param name="actual">C# 独立编码得到的 bytes。</param>
        private static void AssertBytes(
            IReadOnlyDictionary<string, byte[]> vectors,
            string vectorId,
            byte[] actual)
        {
            Assert.That(vectors.ContainsKey(vectorId), Is.True, $"缺少 canonical vector {vectorId}。");
            Assert.That(actual, Is.EqualTo(vectors[vectorId]), vectorId);
        }

        /// <summary>
        /// 读取共享 canonical-golden.json，并拒绝重复 identity 或非法 hex。
        /// </summary>
        /// <returns>vector identity 到独立 byte array 的只读索引。</returns>
        /// <exception cref="InvalidDataException">corpus 为空、结构无效或存在重复 identity 时抛出。</exception>
        private static IReadOnlyDictionary<string, byte[]> LoadVectors()
        {
            var path = Path.Combine(
                RepositoryRoot,
                "shared",
                "contracts",
                "fixtures",
                "battle",
                "wire",
                "canonical-golden.json");
            var document = UnityEngine.JsonUtility.FromJson<BattleWireGoldenDocument>(File.ReadAllText(path));
            if (document == null || document.vectors == null || document.vectors.Length == 0)
            {
                throw new InvalidDataException("battle canonical golden 为空或结构无效。");
            }

            var result = new Dictionary<string, byte[]>(StringComparer.Ordinal);
            foreach (var vector in document.vectors)
            {
                if (vector == null || string.IsNullOrWhiteSpace(vector.vector_id) ||
                    !result.TryAdd(vector.vector_id, DecodeHex(vector.bytes_hex)))
                {
                    throw new InvalidDataException("battle canonical golden 包含无效或重复 vector identity。");
                }
            }

            return result;
        }

        /// <summary>
        /// 严格解码 lowercase even-length hex，不接受平台相关分隔符。
        /// </summary>
        /// <param name="value">corpus 中的 bytes_hex。</param>
        /// <returns>解码后的独立 byte array。</returns>
        /// <exception cref="InvalidDataException">hex 为空、长度为奇数或含非 lowercase hex 字符时抛出。</exception>
        private static byte[] DecodeHex(string value)
        {
            if (string.IsNullOrEmpty(value) || value.Length % 2 != 0)
            {
                throw new InvalidDataException("canonical vector 的 bytes_hex 必须为非空偶数长度。");
            }

            var result = new byte[value.Length / 2];
            for (var index = 0; index < result.Length; index++)
            {
                var high = DecodeNibble(value[index * 2]);
                var low = DecodeNibble(value[index * 2 + 1]);
                result[index] = (byte)((high << 4) | low);
            }

            return result;
        }

        /// <summary>
        /// 解码单个 lowercase hex nibble，拒绝 uppercase 以冻结 corpus 规范。
        /// </summary>
        /// <param name="value">待解码字符。</param>
        /// <returns>0 到 15 的数值。</returns>
        /// <exception cref="InvalidDataException">字符不属于 lowercase hex alphabet 时抛出。</exception>
        private static int DecodeNibble(char value)
        {
            if (value >= '0' && value <= '9')
            {
                return value - '0';
            }

            if (value >= 'a' && value <= 'f')
            {
                return value - 'a' + 10;
            }

            throw new InvalidDataException("canonical vector 包含非 lowercase hex 字符。");
        }

        /// <summary>
        /// 获取包含 versions.yaml 与 shared/contracts 的仓库根目录。
        /// </summary>
        /// <value>从 Unity Application.dataPath 向上两级解析的绝对路径。</value>
        /// <exception cref="DirectoryNotFoundException">Unity 工程不在预期仓库布局时抛出。</exception>
        private static string RepositoryRoot
        {
            get
            {
                var clientRoot = Directory.GetParent(UnityEngine.Application.dataPath)?.FullName ??
                    throw new DirectoryNotFoundException("无法从 Application.dataPath 解析 Unity 工程根目录。");
                return Directory.GetParent(clientRoot)?.FullName ??
                    throw new DirectoryNotFoundException("无法从 Unity 工程根目录解析仓库根目录。");
            }
        }
    }

    /// <summary>
    /// 映射 canonical golden 的最小 JSON 投影；完整 schema 仍由 corpus validator 负责。
    /// </summary>
    [Serializable]
    internal sealed class BattleWireGoldenDocument
    {
        /// <summary>
        /// 保存本次 corpus 的全部 canonical vectors。
        /// </summary>
        public BattleWireGoldenVector[] vectors = Array.Empty<BattleWireGoldenVector>();
    }

    /// <summary>
    /// 映射 C# parity 所需的稳定 vector identity 与 lowercase bytes。
    /// </summary>
    [Serializable]
    internal sealed class BattleWireGoldenVector
    {
        /// <summary>
        /// 保存跨语言失败诊断使用的稳定 identity。
        /// </summary>
        public string vector_id = string.Empty;

        /// <summary>
        /// 保存 source corpus 拥有的 lowercase wire bytes。
        /// </summary>
        public string bytes_hex = string.Empty;
    }
}
