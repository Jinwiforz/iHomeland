using System;
using System.Buffers.Binary;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Google.Protobuf;
using IHomeland.Client.Infrastructure.Http;
using IHomeland.Client.Infrastructure.Tcp;
using IHomeland.Protocol.Common.V1;
using IHomeland.Protocol.Visit.V1;
using IHomeland.Protocol.World.V1;
using NUnit.Framework;

namespace IHomeland.Client.Tests.EditMode
{
    /// <summary>
    /// 验证 gameplay preface、framing、typed route 与 envelope 的跨端 wire 契约。
    /// </summary>
    public sealed class ClientGameplayProtocolTests
    {
        /// <summary>
        /// 验证客户端 preface bytes 与服务端共享 fixture 完全一致且异常不泄漏凭据。
        /// </summary>
        [Test]
        public void PrefaceMatchesSharedFixtureAndRejectsCredentialDrift()
        {
            var fixturePath = Path.Combine(
                FindRepositoryRoot(),
                "shared",
                "contracts",
                "fixtures",
                "realtime",
                "tcp-preface.json");
            using (var document = JsonDocument.Parse(File.ReadAllText(fixturePath)))
            {
                var root = document.RootElement;
                var frame = ClientGameplayPreface.Encode(
                    root.GetProperty("ticket").GetString(),
                    root.GetProperty("admission").GetString(),
                    ClientWorldAdmissionPurpose.OwnWorld);
                Assert.That(BitConverter.ToString(frame).Replace("-", string.Empty).ToLowerInvariant(),
                    Is.EqualTo(root.GetProperty("frameHex").GetString()));
            }

            var error = Assert.Throws<ClientGameplayProtocolException>(() =>
                ClientGameplayPreface.Encode(
                    "ABCDEF0123456789ABCDEF0123456789",
                    "wad1_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
                    ClientWorldAdmissionPurpose.OwnWorld));
            Assert.That(error.Message, Does.Not.Contain("ABCDEF"));
        }

        /// <summary>
        /// 验证 arbitrary partial reads 只在完整收齐 frame 后返回，并在分配前拒绝 oversize。
        /// </summary>
        /// <returns>等待可控 partial connection 完成读取。</returns>
        [Test]
        public async Task FramerPerformsExactReadsAndRejectsOversizePrefix()
        {
            var expected = new byte[] { 1, 2, 3, 4, 5 };
            var connection = new PartialConnection(
                ClientGameplayFramer.Frame(expected),
                maximumReadBytes: 1);

            var actual = await ClientGameplayFramer.ReadFrameAsync(
                connection,
                1024,
                CancellationToken.None);

            Assert.That(actual, Is.EqualTo(expected));
            var oversizePrefix = new byte[4];
            BinaryPrimitives.WriteUInt32BigEndian(oversizePrefix, 1025);
            Assert.ThrowsAsync<ClientGameplayProtocolException>(async () =>
                await ClientGameplayFramer.ReadFrameAsync(
                    new PartialConnection(oversizePrefix, 4),
                    1024,
                    CancellationToken.None));
        }

        /// <summary>
        /// 验证 request/command descriptor 产生正确 correlation，response 与 error 使用相同 route budget。
        /// </summary>
        [Test]
        public void CodecFreezesTypedRoutesSequenceAndCorrelation()
        {
            var codec = new ClientGameplayCodec();
            var correlation = Enumerable.Repeat((byte)7, 16).ToArray();
            var requestBytes = codec.Encode(
                ClientGameplayCatalog.WorldSnapshot,
                new WorldSnapshotRequest(),
                correlation,
                1,
                1);
            var request = ReliableEnvelope.Parser.ParseFrom(requestBytes);
            Assert.That(request.MessageId, Is.EqualTo(2000));
            Assert.That(request.Kind, Is.EqualTo(MessageKind.Request));
            Assert.That(request.RequestId.ToByteArray(), Is.EqualTo(correlation));
            Assert.That(request.CommandId, Is.EqualTo(ByteString.Empty));

            var commandBytes = codec.Encode(
                ClientGameplayCatalog.VisitLeave,
                new VisitLeaveCommand { ExpectedRevision = 1 },
                correlation,
                2,
                1);
            var command = ReliableEnvelope.Parser.ParseFrom(commandBytes);
            Assert.That(command.Kind, Is.EqualTo(MessageKind.Command));
            Assert.That(command.CommandId.ToByteArray(), Is.EqualTo(correlation));

            var response = new ReliableEnvelope
            {
                ProtocolVersion = 1,
                MessageId = 2001,
                Kind = MessageKind.Response,
                RequestId = ByteString.CopyFrom(correlation),
                Sequence = 1,
                TimestampMs = 1,
                Payload = new WorldSnapshotResponse().ToByteString(),
            };
            var decoded = codec.Decode(response.ToByteArray(), 1);
            Assert.That(codec.DecodeResponse(ClientGameplayCatalog.WorldSnapshot, decoded), Is.Not.Null);

            response.Sequence = 3;
            Assert.Throws<ClientGameplayProtocolException>(() => codec.Decode(response.ToByteArray(), 2));
            response.Sequence = 2;
            response.MessageId = 9999;
            Assert.Throws<ClientGameplayProtocolException>(() => codec.Decode(response.ToByteArray(), 2));
        }

        /// <summary>
        /// 验证只有三个登记 PUSH 可通过公共 envelope 校验。
        /// </summary>
        [Test]
        public void CodecRejectsUnknownOrCorrelatedPush()
        {
            var codec = new ClientGameplayCodec();
            var push = new ReliableEnvelope
            {
                ProtocolVersion = 1,
                MessageId = 2002,
                Kind = MessageKind.Push,
                Sequence = 1,
                TimestampMs = 1,
                Payload = new WorldSnapshotPush().ToByteString(),
            };
            Assert.That(codec.Decode(push.ToByteArray(), 1).MessageID, Is.EqualTo(2002));

            push.MessageId = 2999;
            Assert.Throws<ClientGameplayProtocolException>(() => codec.Decode(push.ToByteArray(), 1));
            push.MessageId = 2002;
            push.RequestId = ByteString.CopyFrom(new byte[16]);
            Assert.Throws<ClientGameplayProtocolException>(() => codec.Decode(push.ToByteArray(), 1));
        }

        /// <summary>
        /// 验证 realtime error 必须匹配冻结 registry 与 envelope request correlation。
        /// </summary>
        [Test]
        public void CodecRejectsErrorRegistryOrCorrelationDrift()
        {
            var codec = new ClientGameplayCodec();
            var correlation = Enumerable.Repeat((byte)9, 16).ToArray();
            var payload = new ErrorPayload
            {
                Code = 2000,
                MessageKey = "error.world.not_found",
                RequestId = ByteString.CopyFrom(correlation),
                Retryable = false,
            };
            var envelope = new ReliableEnvelope
            {
                ProtocolVersion = 1,
                MessageId = 2001,
                Kind = MessageKind.Error,
                RequestId = ByteString.CopyFrom(correlation),
                Sequence = 1,
                TimestampMs = 1,
                Payload = payload.ToByteString(),
            };

            Assert.That(codec.DecodeError(codec.Decode(envelope.ToByteArray(), 1)).Code, Is.EqualTo(2000));

            payload.MessageKey = "error.internal";
            envelope.Sequence = 2;
            envelope.Payload = payload.ToByteString();
            Assert.Throws<ClientGameplayProtocolException>(() =>
                codec.DecodeError(codec.Decode(envelope.ToByteArray(), 2)));

            payload.MessageKey = "error.world.not_found";
            payload.RequestId = ByteString.Empty;
            envelope.Sequence = 3;
            envelope.Payload = payload.ToByteString();
            Assert.Throws<ClientGameplayProtocolException>(() =>
                codec.DecodeError(codec.Decode(envelope.ToByteArray(), 3)));

            payload.RequestId = ByteString.CopyFrom(correlation);
            payload.Details.Add(new ErrorDetail { Field = "worldId", Reason = "unsafe\ntext" });
            envelope.Sequence = 4;
            envelope.Payload = payload.ToByteString();
            Assert.Throws<ClientGameplayProtocolException>(() =>
                codec.DecodeError(codec.Decode(envelope.ToByteArray(), 4)));
        }

        /// <summary>
        /// 从测试工作目录向上定位共享 realtime fixtures。
        /// </summary>
        /// <returns>仓库根目录绝对路径。</returns>
        private static string FindRepositoryRoot()
        {
            var directory = new DirectoryInfo(Environment.CurrentDirectory);
            while (directory != null)
            {
                if (File.Exists(Path.Combine(
                        directory.FullName,
                        "shared",
                        "contracts",
                        "fixtures",
                        "realtime",
                        "tcp-preface.json")))
                {
                    return directory.FullName;
                }

                directory = directory.Parent;
            }

            throw new DirectoryNotFoundException("无法定位 shared realtime fixtures。");
        }

        /// <summary>
        /// 以固定最大 chunk 模拟 socket partial read，不提供网络副作用。
        /// </summary>
        private sealed class PartialConnection : IClientGameplayConnection
        {
            /// <summary>保存待读取 bytes。</summary>
            private readonly byte[] _bytes;

            /// <summary>限制每次 read 返回的字节数。</summary>
            private readonly int _maximumReadBytes;

            /// <summary>保存下一读取位置。</summary>
            private int _offset;

            /// <summary>创建受测试控制的 partial connection。</summary>
            /// <param name="bytes">完整输入 bytes。</param>
            /// <param name="maximumReadBytes">每次最多返回字节数。</param>
            internal PartialConnection(byte[] bytes, int maximumReadBytes)
            {
                _bytes = bytes ?? throw new ArgumentNullException(nameof(bytes));
                _maximumReadBytes = maximumReadBytes;
            }

            /// <inheritdoc />
            public Task<int> ReadAsync(
                byte[] buffer,
                int offset,
                int count,
                CancellationToken cancellationToken)
            {
                cancellationToken.ThrowIfCancellationRequested();
                var available = _bytes.Length - _offset;
                var length = Math.Min(Math.Min(count, _maximumReadBytes), available);
                if (length <= 0)
                {
                    return Task.FromResult(0);
                }

                Buffer.BlockCopy(_bytes, _offset, buffer, offset, length);
                _offset += length;
                return Task.FromResult(length);
            }

            /// <inheritdoc />
            public Task WriteAsync(byte[] buffer, CancellationToken cancellationToken)
            {
                throw new NotSupportedException();
            }

            /// <summary>Fake 不拥有外部资源。</summary>
            public void Dispose()
            {
            }
        }
    }
}
