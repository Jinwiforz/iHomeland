using System;
using System.Collections.Generic;
using System.IO;
using System.Security.Cryptography;
using System.Text;
using Google.Protobuf;
using IHomeland.Protocol.Battle.V1;
using NUnit.Framework;

namespace IHomeland.Client.PersonalWorldCombat.Tests.Protocol
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
            AssertBytes(vectors, "secure-kcp-aad-v1", EncodeSecureHeader(2, 0x0102030405060709UL, 53));
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
                AbilityId = 202,
                Phase = BattleAbilityPhase.Started,
            };
            var abilityBytes = ability.ToByteArray();
            AssertBytes(vectors, "battle-ability-event-protobuf-v1", abilityBytes);
            Assert.That(
                BattleAbilityReliableEvent.Parser.ParseFrom(abilityBytes).Phase,
                Is.EqualTo(BattleAbilityPhase.Started));

            var playerState = new BattleEntityState
            {
                EntityId = 1,
                EntityGeneration = 1,
                Transform = CreateZeroTransform(),
                HealthMilli = 100000,
                StateFlags = 16,
                ArchetypeId = 1,
                EquippedWeaponId = 101,
                MaxHealthMilli = 100000,
            };
            AssertBytes(
                vectors,
                "battle-player-entity-state-protobuf-v1",
                playerState.ToByteArray());

            var bossState = new BattleEntityState
            {
                EntityId = 9,
                EntityGeneration = 1,
                Transform = CreateZeroTransform(),
                HealthMilli = 300000,
                StateFlags = 1,
                ArchetypeId = 3,
                EquippedWeaponId = 0,
                MaxHealthMilli = 300000,
            };
            AssertBytes(
                vectors,
                "battle-boss-entity-state-protobuf-v1",
                bossState.ToByteArray());
            var bossLifecycle = new BattleEntityLifecycle
            {
                EventId = 1,
                ServerTick = 10,
                EntityId = 9,
                EntityGeneration = 1,
                Kind = BattleEntityLifecycleKind.Spawn,
                ArchetypeId = 3,
                InitialState = bossState.Clone(),
            };
            AssertBytes(
                vectors,
                "battle-boss-lifecycle-protobuf-v1",
                bossLifecycle.ToByteArray());
            Assert.That(bossLifecycle.ArchetypeId, Is.EqualTo(bossLifecycle.InitialState.ArchetypeId));

            AssertFullSnapshot(
                vectors,
                "battle-full-snapshot-ack-zero-v1",
                1,
                1,
                1,
                0,
                1,
                0);
            AssertDeltaSnapshot(
                vectors,
                "battle-delta-snapshot-ack-zero-v1",
                1,
                1,
                1,
                0,
                1,
                0);
            AssertDeltaSnapshot(
                vectors,
                "battle-delta-snapshot-ack-normal-v1",
                2,
                2,
                1,
                0,
                1,
                42);
            AssertDeltaSnapshot(
                vectors,
                "battle-delta-snapshot-ack-large-v1",
                3,
                3,
                1,
                0,
                1,
                ulong.MaxValue);
            AssertFullSnapshot(
                vectors,
                "battle-full-snapshot-ack-multipart-0-v1",
                4,
                4,
                2,
                0,
                2,
                128);
            AssertFullSnapshot(
                vectors,
                "battle-full-snapshot-ack-multipart-1-v1",
                4,
                4,
                2,
                1,
                2,
                128);

            var missingAcknowledgement = BattleFullSnapshot.Parser.ParseFrom(
                LoadMalformedBytes(
                    "snapshot-ack-presence-missing",
                    "BATTLE_SNAPSHOT_ACK_MISSING"));
            Assert.That(missingAcknowledgement.HasLastProcessedInputTick, Is.False);

            var ticketId = new byte[16];
            var ticketSecret = new byte[32];
            for (var index = 0; index < ticketId.Length; index++)
            {
                ticketId[index] = (byte)index;
            }

            for (var index = 0; index < ticketSecret.Length; index++)
            {
                ticketSecret[index] = (byte)(0x20 + index);
            }

            var expectedProof = DeriveProofKeyV2(ticketId, ticketSecret);
            AssertBytes(
                vectors,
                "battle-ticket-proof-key-v2",
                expectedProof);
            var wrongTicketId = (byte[])ticketId.Clone();
            wrongTicketId[0] ^= byte.MaxValue;
            var wrongSecret = (byte[])ticketSecret.Clone();
            wrongSecret[0] ^= byte.MaxValue;
            Assert.That(
                DeriveProofKeyV2(wrongTicketId, ticketSecret),
                Is.Not.EqualTo(expectedProof),
                "错误 ticket ID 不得派生相同 proof。");
            Assert.That(
                DeriveProofKeyV2(ticketId, wrongSecret),
                Is.Not.EqualTo(expectedProof),
                "错误 ticket secret 不得派生相同 proof。");
            Assert.That(
                DeriveProofKey(
                    ticketId,
                    ticketSecret,
                    "ihomeland/battle-ticket/proof-key/v1"),
                Is.Not.EqualTo(expectedProof),
                "旧 derivation domain 不得兼容 proof-key/v2。");
            Array.Clear(expectedProof, 0, expectedProof.Length);
            Array.Clear(wrongTicketId, 0, wrongTicketId.Length);
            Array.Clear(wrongSecret, 0, wrongSecret.Length);
        }

        /// <summary>
        /// 重编码 full snapshot fixture，并验证显式零值不会被当作字段缺失。
        /// </summary>
        /// <param name="vectors">共享 canonical vector 索引。</param>
        /// <param name="vectorId">当前 full snapshot vector identity。</param>
        /// <param name="serverTick">权威 simulation tick。</param>
        /// <param name="snapshotSequence">session 内 snapshot sequence。</param>
        /// <param name="baselineId">完整 baseline identity。</param>
        /// <param name="partitionIndex">当前零基分区索引。</param>
        /// <param name="partitionCount">逻辑 snapshot 分区总数。</param>
        /// <param name="lastProcessedInputTick">当前 mapping generation 的连续输入确认。</param>
        private static void AssertFullSnapshot(
            IReadOnlyDictionary<string, byte[]> vectors,
            string vectorId,
            ulong serverTick,
            ulong snapshotSequence,
            ulong baselineId,
            uint partitionIndex,
            uint partitionCount,
            ulong lastProcessedInputTick)
        {
            var snapshot = new BattleFullSnapshot
            {
                ServerTick = serverTick,
                SnapshotSequence = snapshotSequence,
                BaselineId = baselineId,
                PartitionIndex = partitionIndex,
                PartitionCount = partitionCount,
                LastProcessedInputTick = lastProcessedInputTick,
            };
            var encoded = snapshot.ToByteArray();
            AssertBytes(vectors, vectorId, encoded);
            var parsed = BattleFullSnapshot.Parser.ParseFrom(encoded);
            Assert.That(parsed.HasLastProcessedInputTick, Is.True, vectorId);
            Assert.That(parsed.LastProcessedInputTick, Is.EqualTo(lastProcessedInputTick), vectorId);
        }

        /// <summary>
        /// 重编码 delta snapshot fixture，并验证最大 uint64 使用规范十字节 varint。
        /// </summary>
        /// <param name="vectors">共享 canonical vector 索引。</param>
        /// <param name="vectorId">当前 delta snapshot vector identity。</param>
        /// <param name="serverTick">权威 simulation tick。</param>
        /// <param name="snapshotSequence">session 内 snapshot sequence。</param>
        /// <param name="baselineId">引用的 full baseline identity。</param>
        /// <param name="partitionIndex">当前零基分区索引。</param>
        /// <param name="partitionCount">逻辑 snapshot 分区总数。</param>
        /// <param name="lastProcessedInputTick">当前 mapping generation 的连续输入确认。</param>
        private static void AssertDeltaSnapshot(
            IReadOnlyDictionary<string, byte[]> vectors,
            string vectorId,
            ulong serverTick,
            ulong snapshotSequence,
            ulong baselineId,
            uint partitionIndex,
            uint partitionCount,
            ulong lastProcessedInputTick)
        {
            var snapshot = new BattleDeltaSnapshot
            {
                ServerTick = serverTick,
                SnapshotSequence = snapshotSequence,
                BaselineId = baselineId,
                PartitionIndex = partitionIndex,
                PartitionCount = partitionCount,
                LastProcessedInputTick = lastProcessedInputTick,
            };
            var encoded = snapshot.ToByteArray();
            AssertBytes(vectors, vectorId, encoded);
            var parsed = BattleDeltaSnapshot.Parser.ParseFrom(encoded);
            Assert.That(parsed.HasLastProcessedInputTick, Is.True, vectorId);
            Assert.That(parsed.LastProcessedInputTick, Is.EqualTo(lastProcessedInputTick), vectorId);
        }

        /// <summary>
        /// 从 HTTPS 可交付的 raw ticket ID/secret 独立执行 proof-key/v2 HKDF。
        /// </summary>
        /// <param name="ticketId">固定 16-byte public HKDF salt。</param>
        /// <param name="ticketSecret">固定 32-byte test-only IKM。</param>
        /// <returns>32-byte ClientAuth transcript proof key。</returns>
        private static byte[] DeriveProofKeyV2(byte[] ticketId, byte[] ticketSecret)
        {
            return DeriveProofKey(
                ticketId,
                ticketSecret,
                "ihomeland/battle-ticket/proof-key/v2");
        }

        /// <summary>
        /// 使用指定 domain 派生测试 proof，生产协议只允许 v2 domain。
        /// </summary>
        /// <param name="ticketId">固定 16-byte public HKDF salt。</param>
        /// <param name="ticketSecret">固定 32-byte test-only IKM。</param>
        /// <param name="domain">用于验证版本隔离的 HKDF info。</param>
        /// <returns>32-byte transcript proof key。</returns>
        private static byte[] DeriveProofKey(
            byte[] ticketId,
            byte[] ticketSecret,
            string domain)
        {
            if (ticketId == null || ticketId.Length != 16)
            {
                throw new ArgumentException("ticket ID 必须为 16 bytes。", nameof(ticketId));
            }

            if (ticketSecret == null || ticketSecret.Length != 32)
            {
                throw new ArgumentException("ticket secret 必须为 32 bytes。", nameof(ticketSecret));
            }

            if (string.IsNullOrEmpty(domain))
            {
                throw new ArgumentException("proof domain 不得为空。", nameof(domain));
            }

            byte[] pseudoRandomKey;
            using (var extract = new HMACSHA256(ticketId))
            {
                pseudoRandomKey = extract.ComputeHash(ticketSecret);
            }

            try
            {
                var info = Encoding.UTF8.GetBytes(domain);
                var expansionInput = new byte[info.Length + 1];
                Buffer.BlockCopy(info, 0, expansionInput, 0, info.Length);
                expansionInput[expansionInput.Length - 1] = 1;
                using (var expand = new HMACSHA256(pseudoRandomKey))
                {
                    return expand.ComputeHash(expansionInput);
                }
            }
            finally
            {
                Array.Clear(pseudoRandomKey, 0, pseudoRandomKey.Length);
            }
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
            PutLittle32(bytes, 20, 29);
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
            PutBig16(bytes, 4, 13);
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
        /// 创建七个 scalar 都显式存在的规范零 transform。
        /// </summary>
        /// <returns>用于 full state 与 lifecycle parity 的独立 transform。</returns>
        private static QuantizedTransform CreateZeroTransform()
        {
            return new QuantizedTransform
            {
                PositionXMm = 0,
                PositionYMm = 0,
                PositionZMm = 0,
                YawMillidegrees = 0,
                VelocityXMmPerSecond = 0,
                VelocityYMmPerSecond = 0,
                VelocityZMmPerSecond = 0,
            };
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
        /// 从共享 malformed corpus 取得具有固定 bytes 与 reason 的负例。
        /// </summary>
        /// <param name="caseId">稳定负例 identity。</param>
        /// <param name="expectedReason">当前 consumer 必须匹配的拒绝原因。</param>
        /// <returns>由调用方独立拥有的 payload bytes。</returns>
        /// <exception cref="InvalidDataException">负例缺失、reason 漂移或 bytes 未冻结时抛出。</exception>
        private static byte[] LoadMalformedBytes(string caseId, string expectedReason)
        {
            var path = Path.Combine(
                RepositoryRoot,
                "shared",
                "contracts",
                "fixtures",
                "battle",
                "wire",
                "malformed-corpus.json");
            var document = UnityEngine.JsonUtility.FromJson<BattleWireMalformedDocument>(
                File.ReadAllText(path));
            if (document == null || document.cases == null)
            {
                throw new InvalidDataException("battle malformed corpus 为空或结构无效。");
            }

            foreach (var testCase in document.cases)
            {
                if (testCase != null &&
                    string.Equals(testCase.case_id, caseId, StringComparison.Ordinal))
                {
                    if (!string.Equals(
                            testCase.expected_reason,
                            expectedReason,
                            StringComparison.Ordinal))
                    {
                        throw new InvalidDataException($"malformed case {caseId} 的 reason 已漂移。");
                    }

                    var bytes = DecodeHex(testCase.bytes_hex);
                    if (bytes.Length != testCase.byte_count)
                    {
                        throw new InvalidDataException($"malformed case {caseId} 的 byte count 已漂移。");
                    }

                    return bytes;
                }
            }

            throw new InvalidDataException($"malformed case {caseId} 不存在。");
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
    /// 映射共享 malformed corpus 的最小 JSON 投影。
    /// </summary>
    [Serializable]
    internal sealed class BattleWireMalformedDocument
    {
        /// <summary>
        /// 保存稳定 negative cases。
        /// </summary>
        public BattleWireMalformedCase[] cases = Array.Empty<BattleWireMalformedCase>();
    }

    /// <summary>
    /// 映射一个可直接解码的 malformed payload。
    /// </summary>
    [Serializable]
    internal sealed class BattleWireMalformedCase
    {
        /// <summary>
        /// 稳定 case identity。
        /// </summary>
        public string case_id = string.Empty;

        /// <summary>
        /// canonical lowercase payload bytes。
        /// </summary>
        public string bytes_hex = string.Empty;

        /// <summary>
        /// payload 冻结长度。
        /// </summary>
        public int byte_count;

        /// <summary>
        /// receiver 必须产生的稳定拒绝原因。
        /// </summary>
        public string expected_reason = string.Empty;
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
