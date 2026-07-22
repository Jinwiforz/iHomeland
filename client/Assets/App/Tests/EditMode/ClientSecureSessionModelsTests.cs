using System;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Core.Configuration;
using IHomeland.Client.Infrastructure.Http;
using NUnit.Framework;

namespace IHomeland.Client.Tests.EditMode
{
    /// <summary>
    /// 验证 secure session application model 的 schema、字段上限和默认脱敏边界。
    /// </summary>
    public sealed class ClientSecureSessionModelsTests
    {
        /// <summary>确认有效 record 只保存 refresh lineage，且格式化不会输出 identity 或 token。</summary>
        [Test]
        public void Record_ValidSnapshot_IsRedacted()
        {
            var record = CreateRecord("refresh-secret");

            Assert.That(record.SchemaVersion, Is.EqualTo(ClientSecureSessionRecord.CurrentSchemaVersion));
            Assert.That(record.RefreshToken, Is.EqualTo("refresh-secret"));
            Assert.That(record.ToString(), Does.Not.Contain("refresh-secret"));
            Assert.That(record.ToString(), Does.Not.Contain("account-fixture"));
            Assert.That(record.ToString(), Does.Not.Contain("session-fixture"));
        }

        /// <summary>确认无效 schema、binding、credential和expiry组合在进入adapter前被拒绝。</summary>
        [TestCase(2, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "refresh", 4000)]
        [TestCase(1, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "refresh", 4000)]
        [TestCase(1, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "refresh secret", 4000)]
        [TestCase(1, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "refresh", 6000)]
        public void Record_InvalidContract_IsRejected(
            int schema,
            string binding,
            string refresh,
            long refreshExpiry)
        {
            Assert.Throws<ArgumentException>(() => new ClientSecureSessionRecord(
                schema,
                binding,
                new ClientAccountSummary("account-fixture", "Fixture", 1),
                new ClientSessionSummary("session-fixture", 1, 5000),
                refresh,
                refreshExpiry));
        }

        /// <summary>确认 store 结果只允许成功携带值，且固定摘要不泄漏 record。</summary>
        [Test]
        public void StoreResult_EnforcesClosedOutcomeContract()
        {
            var record = CreateRecord("refresh-secret");
            var success = ClientSecureSessionStoreResult<ClientSecureSessionRecord>.Success(record);
            var missing = ClientSecureSessionStoreResult<ClientSecureSessionRecord>.Failed(
                ClientSecureSessionStoreOutcome.NotFound);

            Assert.That(success.IsSuccess, Is.True);
            Assert.That(success.Value, Is.SameAs(record));
            Assert.That(success.ToString(), Does.Not.Contain("refresh-secret"));
            Assert.That(missing.Value, Is.Null);
            Assert.Throws<ArgumentException>(() =>
                ClientSecureSessionStoreResult<ClientSecureSessionRecord>.Failed(
                    ClientSecureSessionStoreOutcome.Succeeded));
        }

        /// <summary>确认refresh credential的硬上限在任何codec或OS调用前生效。</summary>
        [Test]
        public void Record_OversizedRefreshToken_IsRejected()
        {
            var oversized = new string('a', ClientSecureSessionRecord.MaximumRefreshTokenLength + 1);

            Assert.Throws<ArgumentException>(() => CreateRecord(oversized));
        }

        /// <summary>确认environment binding稳定且会隔离环境类别、endpoint与protocol。</summary>
        [Test]
        public void EnvironmentBinding_IsStableAndEnvironmentSpecific()
        {
            var first = ClientEnvironment.Create(
                ClientEnvironmentKind.Test,
                "http://127.0.0.1:18080",
                "1.0.0",
                1);
            var equivalent = ClientEnvironment.Create(
                ClientEnvironmentKind.Test,
                "http://127.0.0.1:18080/",
                "2.0.0",
                1);
            var otherProtocol = ClientEnvironment.Create(
                ClientEnvironmentKind.Test,
                "http://127.0.0.1:18080",
                "1.0.0",
                2);

            var firstBinding = ClientSecureSessionEnvironmentBinding.Create(first);

            Assert.That(firstBinding, Has.Length.EqualTo(64));
            Assert.That(firstBinding, Is.EqualTo(ClientSecureSessionEnvironmentBinding.Create(equivalent)));
            Assert.That(
                firstBinding,
                Is.Not.EqualTo(ClientSecureSessionEnvironmentBinding.Create(otherProtocol)));
        }

        /// <summary>创建字段完整且不依赖平台adapter的测试record。</summary>
        /// <param name="refreshToken">测试 refresh token。</param>
        /// <returns>有效 schema v1 record。</returns>
        private static ClientSecureSessionRecord CreateRecord(string refreshToken)
        {
            return new ClientSecureSessionRecord(
                ClientSecureSessionRecord.CurrentSchemaVersion,
                "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                new ClientAccountSummary("account-fixture", "Fixture", 1),
                new ClientSessionSummary("session-fixture", 1, 5000),
                refreshToken,
                4000);
        }
    }
}
