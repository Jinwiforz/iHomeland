using System;
using System.IO;
using System.Linq;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Application.Configuration;
using IHomeland.Client.Application.Contracts;
using IHomeland.Client.Infrastructure.Http;
using IHomeland.Client.Infrastructure.Security;
using NUnit.Framework;

namespace IHomeland.Client.Tests.EditMode
{
    /// <summary>
    /// 验证 secure session file owner、DPAPI adapter、profile隔离与关闭失败语义。
    /// </summary>
    public sealed class ClientSecureSessionStoreTests
    {
        /// <summary>每个测试独占的绝对持久数据根。</summary>
        private string _root;

        /// <summary>创建不复用历史状态的测试目录。</summary>
        [SetUp]
        public void SetUp()
        {
            _root = Path.Combine(
                Path.GetTempPath(),
                "ihomeland-secure-session-tests",
                Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(_root);
        }

        /// <summary>只清理当前测试创建的精确根。</summary>
        [TearDown]
        public void TearDown()
        {
            if (!Directory.Exists(_root))
            {
                return;
            }

            foreach (var path in Directory.GetFiles(_root, "*", SearchOption.AllDirectories))
            {
                File.SetAttributes(path, FileAttributes.Normal);
            }

            Directory.Delete(_root, recursive: true);
        }

        /// <summary>确认完整 record 经过保护、原子落盘并可严格恢复。</summary>
        [Test]
        public async Task FileStore_RoundTripsProtectedRecord_WithoutPlaintextLeak()
        {
            var protector = new XorDataProtector();
            var store = CreateStore(protector);
            await store.InitializeAsync(CancellationToken.None);
            try
            {
                var record = CreateRecord("refresh-secret");
                var replaced = await store.ReplaceAsync(record, CancellationToken.None);
                var read = await store.ReadAsync(CancellationToken.None);
                var recordPath = GetRecordPath();
                var diskText = Encoding.UTF8.GetString(File.ReadAllBytes(recordPath));

                Assert.That(replaced.Outcome, Is.EqualTo(ClientSecureSessionStoreOutcome.Succeeded));
                Assert.That(read.Outcome, Is.EqualTo(ClientSecureSessionStoreOutcome.Succeeded));
                Assert.That(read.Value.RefreshToken, Is.EqualTo("refresh-secret"));
                Assert.That(diskText, Does.Not.Contain("refresh-secret"));
                Assert.That(diskText, Does.Not.Contain("account-fixture"));
                Assert.That(File.GetAttributes(recordPath).HasFlag(FileAttributes.Hidden), Is.True);
                Assert.That(File.Exists(GetTemporaryPath()), Is.False);
            }
            finally
            {
                await store.StopAsync(CancellationToken.None);
            }
        }

        /// <summary>确认取消发生在commit前时旧record保持current且temp被精确清除。</summary>
        [Test]
        public async Task FileStore_CancelledReplace_PreservesPreviousRecord()
        {
            var store = CreateStore(new XorDataProtector());
            await store.InitializeAsync(CancellationToken.None);
            try
            {
                await store.ReplaceAsync(CreateRecord("refresh-old"), CancellationToken.None);
                using (var cancellation = new CancellationTokenSource())
                {
                    cancellation.Cancel();
                    var cancelled = await store.ReplaceAsync(
                        CreateRecord("refresh-new"),
                        cancellation.Token);
                    var read = await store.ReadAsync(CancellationToken.None);

                    Assert.That(
                        cancelled.Outcome,
                        Is.EqualTo(ClientSecureSessionStoreOutcome.Cancelled));
                    Assert.That(read.Value.RefreshToken, Is.EqualTo("refresh-old"));
                    Assert.That(File.Exists(GetTemporaryPath()), Is.False);
                }
            }
            finally
            {
                await store.StopAsync(CancellationToken.None);
            }
        }

        /// <summary>确认损坏envelope失败关闭并删除精确record，避免反复尝试同一坏状态。</summary>
        [Test]
        public async Task FileStore_CorruptEnvelope_IsRejectedAndRetired()
        {
            var store = CreateStore(new XorDataProtector());
            await store.InitializeAsync(CancellationToken.None);
            try
            {
                Directory.CreateDirectory(Path.GetDirectoryName(GetRecordPath()));
                File.WriteAllBytes(GetRecordPath(), new byte[] { 1, 2, 3, 4 });

                var read = await store.ReadAsync(CancellationToken.None);

                Assert.That(read.Outcome, Is.EqualTo(ClientSecureSessionStoreOutcome.InvalidRecord));
                Assert.That(File.Exists(GetRecordPath()), Is.False);
            }
            finally
            {
                await store.StopAsync(CancellationToken.None);
            }
        }

        /// <summary>确认protector失败或输出过大时不会提交明文、temp或部分record。</summary>
        [TestCase(ProtectorMode.Failure)]
        [TestCase(ProtectorMode.Oversized)]
        public async Task FileStore_ProtectionFailure_DoesNotCommit(ProtectorMode mode)
        {
            var store = CreateStore(new XorDataProtector(mode));
            await store.InitializeAsync(CancellationToken.None);
            try
            {
                var result = await store.ReplaceAsync(
                    CreateRecord("refresh-secret"),
                    CancellationToken.None);

                Assert.That(
                    result.Outcome,
                    Is.EqualTo(ClientSecureSessionStoreOutcome.ProtectionFailure));
                Assert.That(File.Exists(GetRecordPath()), Is.False);
                Assert.That(File.Exists(GetTemporaryPath()), Is.False);
            }
            finally
            {
                await store.StopAsync(CancellationToken.None);
            }
        }

        /// <summary>确认同一profile的第二个进程owner不能读取、删除或轮换lineage。</summary>
        [Test]
        public async Task FileStore_SecondWriter_IsProfileInUse()
        {
            var first = CreateStore(new XorDataProtector());
            var second = CreateStore(new XorDataProtector());
            await first.InitializeAsync(CancellationToken.None);
            await second.InitializeAsync(CancellationToken.None);
            try
            {
                var read = await second.ReadAsync(CancellationToken.None);
                var replace = await second.ReplaceAsync(
                    CreateRecord("refresh-second"),
                    CancellationToken.None);
                var delete = await second.DeleteAsync(CancellationToken.None);

                Assert.That(read.Outcome, Is.EqualTo(ClientSecureSessionStoreOutcome.ProfileInUse));
                Assert.That(replace.Outcome, Is.EqualTo(ClientSecureSessionStoreOutcome.ProfileInUse));
                Assert.That(delete.Outcome, Is.EqualTo(ClientSecureSessionStoreOutcome.ProfileInUse));
                Assert.That(File.Exists(GetRecordPath()), Is.False);
            }
            finally
            {
                await second.StopAsync(CancellationToken.None);
                await first.StopAsync(CancellationToken.None);
            }
        }

        /// <summary>确认stop后的store不会被重新初始化或继续操作。</summary>
        [Test]
        public async Task FileStore_Stop_IsTerminal()
        {
            var store = CreateStore(new XorDataProtector());
            await store.InitializeAsync(CancellationToken.None);
            await store.StopAsync(CancellationToken.None);

            var result = await store.ReadAsync(CancellationToken.None);

            Assert.That(result.Outcome, Is.EqualTo(ClientSecureSessionStoreOutcome.Stopped));
            Assert.ThrowsAsync<InvalidOperationException>(async () =>
                await store.InitializeAsync(CancellationToken.None));
        }

        /// <summary>确认Release与Production不解析隔离profile，Debug Local只接受封闭grammar。</summary>
        [Test]
        public void ProfileResolver_RestrictsQualificationIsolation()
        {
            Assert.That(
                ClientSecureSessionProfile.Resolve(
                    ClientEnvironmentKind.Local,
                    new[] { "player.exe", "-ihomelandDataProfile", "visitor-a" },
                    isDebugBuild: true),
                Is.EqualTo("visitor-a"));
            Assert.That(
                ClientSecureSessionProfile.Resolve(
                    ClientEnvironmentKind.Local,
                    new[] { "player.exe", "-ihomelandDataProfile", "../escape" },
                    isDebugBuild: false),
                Is.EqualTo(ClientSecureSessionProfile.DefaultProfile));
            Assert.That(
                ClientSecureSessionProfile.Resolve(
                    ClientEnvironmentKind.Production,
                    new[] { "player.exe", "-ihomelandDataProfile", "visitor-a" },
                    isDebugBuild: true),
                Is.EqualTo(ClientSecureSessionProfile.DefaultProfile));
            Assert.Throws<ArgumentException>(() => ClientSecureSessionProfile.Resolve(
                ClientEnvironmentKind.Test,
                new[] { "player.exe", "-ihomelandDataProfile", "../escape" },
                isDebugBuild: true));
        }

        /// <summary>确认资格存储根只在Debug Local/Test中接受绝对路径。</summary>
        [Test]
        public void ProfileResolver_RestrictsQualificationStorageRoot()
        {
            var absolute = Path.GetFullPath(Path.Combine(Path.GetTempPath(), "ihomeland-qualification"));
            Assert.That(
                ClientSecureSessionProfile.ResolveStorageRoot(
                    ClientEnvironmentKind.Test,
                    new[] { "player.exe", "-ihomelandQualificationStorageRoot", absolute },
                    isDebugBuild: true,
                    defaultRoot: "default-root"),
                Is.EqualTo(absolute));
            Assert.Throws<ArgumentException>(() =>
                ClientSecureSessionProfile.ResolveStorageRoot(
                    ClientEnvironmentKind.Local,
                    new[] { "player.exe", "-ihomelandQualificationStorageRoot", "relative" },
                    isDebugBuild: true,
                    defaultRoot: "default-root"));
            Assert.That(
                ClientSecureSessionProfile.ResolveStorageRoot(
                    ClientEnvironmentKind.Production,
                    new[] { "player.exe", "-ihomelandQualificationStorageRoot", absolute },
                    isDebugBuild: true,
                    defaultRoot: "default-root"),
                Is.EqualTo("default-root"));
            Assert.That(
                ClientSecureSessionProfile.ResolveStorageRoot(
                    ClientEnvironmentKind.Test,
                    new[] { "player.exe", "-ihomelandQualificationStorageRoot", absolute },
                    isDebugBuild: false,
                    defaultRoot: "default-root"),
                Is.EqualTo("default-root"));
        }

        /// <summary>确认非Windows adapter只返回Unsupported且stop后保持terminal。</summary>
        [Test]
        public async Task UnsupportedStore_NeverFallsBackToPlaintext()
        {
            var store = new UnsupportedClientSecureSessionStore();
            await store.InitializeAsync(CancellationToken.None);

            var beforeStop = await store.ReplaceAsync(
                CreateRecord("refresh-secret"),
                CancellationToken.None);
            await store.StopAsync(CancellationToken.None);
            var afterStop = await store.ReadAsync(CancellationToken.None);

            Assert.That(beforeStop.Outcome, Is.EqualTo(ClientSecureSessionStoreOutcome.Unsupported));
            Assert.That(afterStop.Outcome, Is.EqualTo(ClientSecureSessionStoreOutcome.Stopped));
            Assert.ThrowsAsync<InvalidOperationException>(async () =>
                await store.InitializeAsync(CancellationToken.None));
        }

        /// <summary>确认Windows DPAPI CurrentUser可往返且错误entropy触发native失败而不抛细节。</summary>
        [Test]
        public void WindowsDpapi_RoundTripAndWrongEntropy_AreClosed()
        {
            if (Environment.OSVersion.Platform != PlatformID.Win32NT)
            {
                Assert.Ignore("DPAPI contract仅在Windows Editor运行。");
            }

            var protector = new WindowsDpapiDataProtector();
            var plaintext = Encoding.UTF8.GetBytes("refresh-secret");
            var entropy = Encoding.UTF8.GetBytes("environment-a");
            var wrongEntropy = Encoding.UTF8.GetBytes("environment-b");
            try
            {
                var protectedResult = protector.Protect(plaintext, entropy);
                Assert.That(protectedResult.Succeeded, Is.True);
                Assert.That(protectedResult.Data.SequenceEqual(plaintext), Is.False);

                var restored = protector.Unprotect(protectedResult.Data, entropy);
                var rejected = protector.Unprotect(protectedResult.Data, wrongEntropy);
                try
                {
                    Assert.That(restored.Succeeded, Is.True);
                    Assert.That(restored.Data, Is.EqualTo(plaintext));
                    Assert.That(rejected.Succeeded, Is.False);
                }
                finally
                {
                    Clear(protectedResult.Data);
                    Clear(restored.Data);
                    Clear(rejected.Data);
                }
            }
            finally
            {
                Clear(plaintext);
                Clear(entropy);
                Clear(wrongEntropy);
            }
        }

        /// <summary>创建使用当前测试根和default profile的file store。</summary>
        /// <param name="protector">可控保护adapter。</param>
        /// <returns>尚未初始化的store。</returns>
        private ClientSecureSessionFileStore CreateStore(IClientDataProtector protector)
        {
            return new ClientSecureSessionFileStore(
                _root,
                ClientSecureSessionProfile.DefaultProfile,
                Binding,
                protector,
                platformSupported: true);
        }

        /// <summary>创建字段完整的测试record。</summary>
        /// <param name="refreshToken">测试lineage。</param>
        /// <returns>有效schema v1 record。</returns>
        private static ClientSecureSessionRecord CreateRecord(string refreshToken)
        {
            return new ClientSecureSessionRecord(
                ClientSecureSessionRecord.CurrentSchemaVersion,
                Binding,
                new ClientAccountSummary("account-fixture", "Fixture", 1),
                new ClientSessionSummary("session-fixture", 2, 5000),
                refreshToken,
                4000);
        }

        /// <summary>返回store contract的精确record路径。</summary>
        /// <returns>当前测试根内的record路径。</returns>
        private string GetRecordPath()
        {
            return Path.Combine(_root, "secure-session", "default", "session.v1.bin");
        }

        /// <summary>返回store contract的精确temporary路径。</summary>
        /// <returns>当前测试根内的temp路径。</returns>
        private string GetTemporaryPath()
        {
            return Path.Combine(_root, "secure-session", "default", "session.v1.tmp");
        }

        /// <summary>尽力归零测试拥有的managed bytes。</summary>
        /// <param name="buffer">可为空的测试buffer。</param>
        private static void Clear(byte[] buffer)
        {
            if (buffer != null)
            {
                Array.Clear(buffer, 0, buffer.Length);
            }
        }

        /// <summary>测试固定environment binding。</summary>
        private const string Binding =
            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";

        /// <summary>控制fake protector故障模式。</summary>
        public enum ProtectorMode
        {
            /// <summary>可逆保护。</summary>
            Success = 0,

            /// <summary>稳定native失败。</summary>
            Failure = 1,

            /// <summary>返回超出envelope上限的异常结果。</summary>
            Oversized = 2,
        }

        /// <summary>以固定XOR模拟不会把plaintext直接落盘的数据保护adapter。</summary>
        private sealed class XorDataProtector : IClientDataProtector
        {
            /// <summary>当前故障模式。</summary>
            private readonly ProtectorMode _mode;

            /// <summary>创建成功或故障fake。</summary>
            /// <param name="mode">每次protect使用的故障模式。</param>
            internal XorDataProtector(ProtectorMode mode = ProtectorMode.Success)
            {
                _mode = mode;
            }

            /// <inheritdoc />
            public ClientDataProtectionResult Protect(byte[] plaintext, byte[] entropy)
            {
                if (_mode == ProtectorMode.Failure)
                {
                    return ClientDataProtectionResult.Failure();
                }

                if (_mode == ProtectorMode.Oversized)
                {
                    return ClientDataProtectionResult.Success(
                        new byte[ClientSecureSessionFileStore.MaximumEnvelopeBytes]);
                }

                return ClientDataProtectionResult.Success(Transform(plaintext));
            }

            /// <inheritdoc />
            public ClientDataProtectionResult Unprotect(byte[] ciphertext, byte[] entropy)
            {
                return ClientDataProtectionResult.Success(Transform(ciphertext));
            }

            /// <summary>对每个byte执行自反变换。</summary>
            /// <param name="input">调用方拥有的源bytes。</param>
            /// <returns>新分配的变换结果。</returns>
            private static byte[] Transform(byte[] input)
            {
                var output = new byte[input.Length];
                for (var index = 0; index < input.Length; index++)
                {
                    output[index] = (byte)(input[index] ^ 0xA5);
                }

                return output;
            }
        }
    }
}
