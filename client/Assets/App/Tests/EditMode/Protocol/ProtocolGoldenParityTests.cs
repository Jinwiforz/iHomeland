using System;
using System.IO;
using System.Linq;
using IHomeland.Protocol.Common.V1;
using NUnit.Framework;
using UnityEngine;

namespace IHomeland.Client.Tests.EditMode.Protocol
{
    /// <summary>
    /// 验证 Unity 实际编译的 C# 协议与 Go golden fixture 保持完全互操作。
    /// </summary>
    public sealed class ProtocolGoldenParityTests
    {
        /// <summary>
        /// 对当前完整 fixture 和 registry 执行跨语言 parity。
        /// </summary>
        [Test]
        public void GoldenPacketsMatchGoFixture()
        {
            var manifest = LoadManifest();
            var registry = LoadRegistry();
            var descriptors = ProtocolGoldenParityVerifier.BuildDescriptorIndex(typeof(ReliableEnvelope).Assembly);

            Assert.DoesNotThrow(() => ProtocolGoldenParityVerifier.Verify(manifest, registry, descriptors));
        }

        /// <summary>
        /// 确保 descriptor 发现不会按反射遍历顺序覆盖重复消息全名。
        /// </summary>
        [Test]
        public void DescriptorCatalogRejectsDuplicateFullName()
        {
            var file = EnvelopeReflection.Descriptor;

            Assert.Throws<InvalidDataException>(() =>
                ProtocolGoldenParityVerifier.BuildDescriptorIndex(new[] { file, file }));
        }

        /// <summary>
        /// 确保 fixture 引用未生成消息时报告 packet 和缺失全名。
        /// </summary>
        [Test]
        public void ParityRejectsUnknownDescriptor()
        {
            var manifest = LoadManifest();
            manifest.packets[0].protobuf = "ihomeland.missing.v1.Unknown";

            var exception = Assert.Throws<InvalidDataException>(() => Verify(manifest, LoadRegistry()));
            Assert.That(exception?.Message, Does.Contain("Unknown"));
        }

        /// <summary>
        /// 确保损坏 Base64 不会被当作空 payload 或宽松跳过。
        /// </summary>
        [Test]
        public void ParityRejectsMalformedBase64()
        {
            var manifest = LoadManifest();
            manifest.packets[0].payloadBase64 = "***not-base64***";

            var exception = Assert.Throws<InvalidDataException>(() => Verify(manifest, LoadRegistry()));
            Assert.That(exception?.Message, Does.Contain("payloadBase64"));
        }

        /// <summary>
        /// 确保 fixture 与 message registry 的类型映射不能各自漂移。
        /// </summary>
        [Test]
        public void ParityRejectsRegistryTypeDrift()
        {
            var manifest = LoadManifest();
            var registry = LoadRegistry();
            var messageId = manifest.packets.First(packet => packet.messageId != 0).messageId;
            registry.messages.First(entry => entry.id == messageId).protobuf = "ihomeland.missing.v1.Drift";

            var exception = Assert.Throws<InvalidDataException>(() => Verify(manifest, registry));
            Assert.That(exception?.Message, Does.Contain("registry"));
        }

        /// <summary>
        /// 确保删除任一已登记 world/visit packet 都会破坏冻结覆盖门禁。
        /// </summary>
        [Test]
        public void ParityRejectsWorldVisitCoverageDrift()
        {
            var manifest = LoadManifest();
            manifest.packets = manifest.packets.Where(packet => packet.messageId != 2000).ToArray();

            var exception = Assert.Throws<InvalidDataException>(() => Verify(manifest, LoadRegistry()));
            Assert.That(exception?.Message, Does.Contain("2000"));
        }

        /// <summary>
        /// 使用真实生成程序集执行指定 manifest 的辅助入口。
        /// </summary>
        /// <param name="manifest">可由负向测试局部修改的 manifest 快照。</param>
        /// <param name="registry">可由负向测试局部修改的 registry 快照。</param>
        /// <exception cref="InvalidDataException">fixture、registry 或生成 descriptor 不一致时抛出。</exception>
        private static void Verify(GoldenManifest manifest, MessageRegistryDocument registry)
        {
            var descriptors = ProtocolGoldenParityVerifier.BuildDescriptorIndex(typeof(ReliableEnvelope).Assembly);
            ProtocolGoldenParityVerifier.Verify(manifest, registry, descriptors);
        }

        /// <summary>
        /// 每次测试重新读取 manifest，避免负向 mutation 污染其他用例。
        /// </summary>
        /// <returns>当前仓库中的完整 golden 快照。</returns>
        /// <exception cref="FileNotFoundException">仓库 fixture 不存在时抛出。</exception>
        /// <exception cref="InvalidDataException">fixture 结构不受支持时抛出。</exception>
        private static GoldenManifest LoadManifest()
        {
            return ProtocolGoldenParityVerifier.LoadGoldenManifest(Path.Combine(
                RepositoryRoot,
                "shared",
                "contracts",
                "fixtures",
                "realtime",
                "golden.json"));
        }

        /// <summary>
        /// 每次测试重新读取 registry，避免负向 mutation 污染其他用例。
        /// </summary>
        /// <returns>当前仓库中的 message registry 快照。</returns>
        /// <exception cref="FileNotFoundException">仓库 registry 不存在时抛出。</exception>
        /// <exception cref="InvalidDataException">registry 结构不受支持时抛出。</exception>
        private static MessageRegistryDocument LoadRegistry()
        {
            return ProtocolGoldenParityVerifier.LoadMessageRegistry(Path.Combine(
                RepositoryRoot,
                "shared",
                "contracts",
                "registry",
                "messages.json"));
        }

        /// <summary>
        /// 获取包含 versions.yaml 和 shared/contracts 的仓库根目录。
        /// </summary>
        /// <value>由 Unity 工程目录向上解析得到的仓库根绝对路径。</value>
        /// <exception cref="DirectoryNotFoundException">当前 Unity 工程不位于预期仓库布局时抛出。</exception>
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
}
