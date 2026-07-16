using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using Google.Protobuf;
using IHomeland.Client.Infrastructure.WebSocket;
using IHomeland.Protocol.Common.V1;
using IHomeland.Protocol.Control.V1;
using IHomeland.Protocol.Visit.V1;
using IHomeland.Protocol.World.V1;
using NUnit.Framework;
using UnityEngine;

namespace IHomeland.Client.Tests.EditMode
{
    /// <summary>
    /// 验证 WSS control catalog 与 `ReliableEnvelope` 的封闭跨端契约。
    /// </summary>
    [TestFixture]
    internal sealed class ClientControlCodecTests
    {
        /// <summary>
        /// 确认手写 catalog 精确覆盖 tracked message/route registry 的 9 个 WSS PUSH。
        /// </summary>
        [Test]
        public void Catalog_MatchesTrackedWssRegistries()
        {
            var catalog = new ClientControlCatalog();
            var routesPath = RegistryPath("routes.json");
            var messagesPath = RegistryPath("messages.json");
            using (var routesDocument = JsonDocument.Parse(File.ReadAllText(routesPath)))
            using (var messagesDocument = JsonDocument.Parse(File.ReadAllText(messagesPath)))
            {
                var routeSizes = new Dictionary<uint, int>();
                foreach (var route in routesDocument.RootElement.GetProperty("routes").EnumerateArray())
                {
                    if (route.GetProperty("channel").GetString() == "WSS")
                    {
                        routeSizes.Add(
                            route.GetProperty("messageId").GetUInt32(),
                            route.GetProperty("maxSize").GetInt32());
                    }
                }

                var messageContracts = new Dictionary<uint, Tuple<string, string>>();
                foreach (var message in messagesDocument.RootElement.GetProperty("messages").EnumerateArray())
                {
                    var id = message.GetProperty("id").GetUInt32();
                    if (routeSizes.ContainsKey(id))
                    {
                        Assert.That(message.GetProperty("kind").GetString(), Is.EqualTo("PUSH"));
                        Assert.That(message.GetProperty("direction").GetString(), Is.EqualTo("SERVER_TO_CLIENT"));
                        messageContracts.Add(
                            id,
                            Tuple.Create(
                                message.GetProperty("name").GetString(),
                                message.GetProperty("protobuf").GetString()));
                    }
                }

                Assert.That(catalog.Routes.Count, Is.EqualTo(9));
                Assert.That(routeSizes.Count, Is.EqualTo(9));
                foreach (var pair in catalog.Routes)
                {
                    Assert.That(routeSizes[pair.Key], Is.EqualTo(pair.Value.MaximumFrameBytes));
                    Assert.That(messageContracts[pair.Key].Item1, Is.EqualTo(pair.Value.Name));
                    Assert.That(messageContracts[pair.Key].Item2, Is.EqualTo(pair.Value.ProtobufName));
                }
            }
        }

        /// <summary>
        /// 确认 tracked realtime golden 中的 WSS packets 可由同一 runtime codec 精确解码。
        /// </summary>
        [Test]
        public void Decode_TrackedControlGoldenPackets_Succeeds()
        {
            var catalog = new ClientControlCatalog();
            var codec = new ClientControlCodec(catalog);
            var goldenPath = Path.GetFullPath(Path.Combine(
                UnityEngine.Application.dataPath,
                "..",
                "..",
                "shared",
                "contracts",
                "fixtures",
                "realtime",
                "golden.json"));
            var decoded = 0;
            using (var document = JsonDocument.Parse(File.ReadAllText(goldenPath)))
            {
                foreach (var packet in document.RootElement.GetProperty("packets").EnumerateArray())
                {
                    var messageID = packet.GetProperty("messageId").GetUInt32();
                    if (!catalog.TryGet(messageID, out _) ||
                        !packet.TryGetProperty("envelopeBase64", out var envelopeBase64))
                    {
                        continue;
                    }

                    var frame = Convert.FromBase64String(envelopeBase64.GetString());
                    var envelope = ReliableEnvelope.Parser.ParseFrom(frame);
                    var push = codec.Decode(frame, frame.Length, 65536, envelope.Sequence);
                    Assert.That(push.Route.MessageID, Is.EqualTo(messageID));
                    Assert.That(push.Payload.Descriptor.FullName, Is.EqualTo(
                        packet.GetProperty("protobuf").GetString()));
                    decoded++;
                }
            }

            Assert.That(decoded, Is.GreaterThanOrEqualTo(6));
        }

        /// <summary>
        /// 确认合法 binary PUSH 返回精确 generated payload 与 envelope 元数据。
        /// </summary>
        [Test]
        public void Decode_ValidMaintenancePush_ReturnsTypedPayload()
        {
            var payload = new MaintenancePush
            {
                StartsAtMs = 1000,
                ExpectedEndAtMs = 2000,
                MessageKey = "maintenance.fixture",
            };
            var frame = EnvelopeBytes(500, 1, payload);

            var push = CreateCodec().Decode(frame, frame.Length, 65536, 1);

            Assert.That(push.Route.MessageID, Is.EqualTo(500));
            Assert.That(push.Sequence, Is.EqualTo(1));
            Assert.That(push.TimestampMilliseconds, Is.EqualTo(1700000000000));
            Assert.That(push.Payload, Is.TypeOf<MaintenancePush>());
            Assert.That(((MaintenancePush)push.Payload).MessageKey, Is.EqualTo("maintenance.fixture"));
        }

        /// <summary>
        /// 确认 9 条 catalog route 都能由各自固定 generated parser 实际解码。
        /// </summary>
        [Test]
        public void Decode_AllCatalogPayloads_UseExactGeneratedParsers()
        {
            var payloads = new Dictionary<uint, IMessage>
            {
                { 500, new MaintenancePush() },
                { 501, new ForcedLogoutPush() },
                { 502, new QueueStatusPush() },
                { 503, new EndpointUpdatePush() },
                { 504, new SessionInvalidatedPush() },
                { 2003, new WorldAssignmentChangedPush() },
                { 2100, new VisitInvitePush() },
                { 2101, new VisitOwnerAvailabilityPush() },
                { 2102, new VisitClosedNoticePush() },
            };
            var catalog = new ClientControlCatalog();
            var codec = new ClientControlCodec(catalog);

            Assert.That(payloads.Count, Is.EqualTo(catalog.Routes.Count));
            foreach (var pair in payloads)
            {
                var frame = EnvelopeBytes(pair.Key, 1, pair.Value);
                var push = codec.Decode(frame, frame.Length, 65536, 1);

                Assert.That(push.Payload.Descriptor.FullName, Is.EqualTo(catalog.Routes[pair.Key].ProtobufName));
            }
        }

        /// <summary>
        /// 确认连接内 sequence 缺口在 payload 分发前 fail closed。
        /// </summary>
        [Test]
        public void Decode_SequenceGap_IsRejected()
        {
            var frame = EnvelopeBytes(500, 3, new MaintenancePush());

            var exception = Assert.Throws<ClientControlProtocolException>(
                () => CreateCodec().Decode(frame, frame.Length, 65536, 2));

            Assert.That(exception.FailureKind, Is.EqualTo(ClientControlProtocolFailureKind.InvalidSequence));
        }

        /// <summary>
        /// 确认 TLS/TCP 或未知 message ID 不能降级为 unknown control event。
        /// </summary>
        [Test]
        public void Decode_UnknownRoute_IsRejected()
        {
            var frame = EnvelopeBytes(2002, 1, new MaintenancePush());

            var exception = Assert.Throws<ClientControlProtocolException>(
                () => CreateCodec().Decode(frame, frame.Length, 65536, 1));

            Assert.That(exception.FailureKind, Is.EqualTo(ClientControlProtocolFailureKind.UnknownRoute));
        }

        /// <summary>
        /// 确认完整 encoded envelope 执行 route 上限，而不是只限制内部 payload。
        /// </summary>
        [Test]
        public void Decode_EnvelopeAboveRouteBudget_IsRejected()
        {
            var frame = EnvelopeBytes(
                500,
                1,
                new MaintenancePush { MessageKey = new string('x', 5000) });
            Assert.That(frame.Length, Is.GreaterThan(4096));

            var exception = Assert.Throws<ClientControlProtocolException>(
                () => CreateCodec().Decode(frame, frame.Length, 8192, 1));

            Assert.That(exception.FailureKind, Is.EqualTo(ClientControlProtocolFailureKind.OversizedFrame));
        }

        /// <summary>
        /// 确认 PUSH 不允许携带 request/command correlation。
        /// </summary>
        [Test]
        public void Decode_PushWithCorrelation_IsRejected()
        {
            var envelope = Envelope(500, 1, new MaintenancePush());
            envelope.RequestId = ByteString.CopyFrom(new byte[16]);
            var frame = envelope.ToByteArray();

            var exception = Assert.Throws<ClientControlProtocolException>(
                () => CreateCodec().Decode(frame, frame.Length, 65536, 1));

            Assert.That(exception.FailureKind, Is.EqualTo(ClientControlProtocolFailureKind.InvalidEnvelope));
        }

        /// <summary>
        /// 确认精确 route parser 拒绝 malformed generated payload。
        /// </summary>
        [Test]
        public void Decode_MalformedGeneratedPayload_IsRejected()
        {
            var envelope = Envelope(500, 1, new MaintenancePush());
            envelope.Payload = ByteString.CopyFrom(0x80);
            var frame = envelope.ToByteArray();

            var exception = Assert.Throws<ClientControlProtocolException>(
                () => CreateCodec().Decode(frame, frame.Length, 65536, 1));

            Assert.That(exception.FailureKind, Is.EqualTo(ClientControlProtocolFailureKind.MalformedPayload));
        }

        /// <summary>
        /// 创建使用 production catalog 的 codec。
        /// </summary>
        /// <returns>待测试的封闭 control codec。</returns>
        private static ClientControlCodec CreateCodec()
        {
            return new ClientControlCodec(new ClientControlCatalog());
        }

        /// <summary>
        /// 创建测试 PUSH envelope。
        /// </summary>
        /// <param name="messageID">待声明的 message ID。</param>
        /// <param name="sequence">当前连接 sequence。</param>
        /// <param name="payload">待确定性序列化的 generated payload。</param>
        /// <returns>可继续构造负向字段的 envelope。</returns>
        private static ReliableEnvelope Envelope(uint messageID, ulong sequence, IMessage payload)
        {
            return new ReliableEnvelope
            {
                ProtocolVersion = 1,
                MessageId = messageID,
                Kind = MessageKind.Push,
                Sequence = sequence,
                TimestampMs = 1700000000000,
                Payload = payload.ToByteString(),
            };
        }

        /// <summary>
        /// 创建完整 binary envelope bytes。
        /// </summary>
        /// <param name="messageID">待声明的 message ID。</param>
        /// <param name="sequence">当前连接 sequence。</param>
        /// <param name="payload">待序列化 generated payload。</param>
        /// <returns>单条 WebSocket binary message 内容。</returns>
        private static byte[] EnvelopeBytes(uint messageID, ulong sequence, IMessage payload)
        {
            return Envelope(messageID, sequence, payload).ToByteArray();
        }

        /// <summary>
        /// 解析仓库 tracked registry 的绝对路径。
        /// </summary>
        /// <param name="fileName">Registry 文件名。</param>
        /// <returns>当前 Unity project 对应仓库中的绝对路径。</returns>
        private static string RegistryPath(string fileName)
        {
            return Path.GetFullPath(Path.Combine(
                UnityEngine.Application.dataPath,
                "..",
                "..",
                "shared",
                "contracts",
                "registry",
                fileName));
        }
    }
}
