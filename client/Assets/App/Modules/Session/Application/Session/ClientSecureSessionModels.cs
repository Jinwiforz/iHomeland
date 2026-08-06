using System;
using System.Security.Cryptography;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.AppShell.Application.Configuration;
using IHomeland.Client.Core.Foundation.Lifetime;
using IHomeland.Client.Networking.Application.Contracts;

namespace IHomeland.Client.Session.Application
{
    /// <summary>
    /// 为 secure record 派生不含 secret 的稳定 product/environment binding。
    /// </summary>
    internal static class ClientSecureSessionEnvironmentBinding
    {
        /// <summary>固定 Windows 产品身份，防止其他 Unity 产品误读相同 payload。</summary>
        private const string ProductScope = "com.jinwiforz.ihomeland";

        /// <summary>从已验证环境构造 SHA-256 binding。</summary>
        /// <param name="environment">Composition 使用的不可变环境。</param>
        /// <returns>64 字符小写十六进制摘要。</returns>
        internal static string Create(ClientEnvironment environment)
        {
            if (environment == null)
            {
                throw new ArgumentNullException(nameof(environment));
            }

            var canonical = string.Join(
                "\n",
                "ihomeland-secure-session-v1",
                ProductScope,
                ((int)environment.EnvironmentKind).ToString(),
                environment.HttpBaseUri.AbsoluteUri,
                environment.ProtocolVersion.ToString());
            var bytes = Encoding.UTF8.GetBytes(canonical);
            try
            {
                using (var sha256 = SHA256.Create())
                {
                    var digest = sha256.ComputeHash(bytes);
                    try
                    {
                        var builder = new StringBuilder(digest.Length * 2);
                        foreach (var value in digest)
                        {
                            builder.Append(value.ToString("x2"));
                        }

                        return builder.ToString();
                    }
                    finally
                    {
                        Array.Clear(digest, 0, digest.Length);
                    }
                }
            }
            finally
            {
                Array.Clear(bytes, 0, bytes.Length);
            }
        }
    }

    /// <summary>
    /// 标识 secure session store 的封闭、低敏操作结果。
    /// </summary>
    internal enum ClientSecureSessionStoreOutcome
    {
        /// <summary>操作已完整提交。</summary>
        Succeeded = 0,

        /// <summary>当前 profile 没有持久 session。</summary>
        NotFound = 1,

        /// <summary>当前平台没有登记安全存储 adapter。</summary>
        Unsupported = 2,

        /// <summary>另一个进程已经拥有同一 production profile。</summary>
        ProfileInUse = 3,

        /// <summary>调用方在操作提交前取消等待。</summary>
        Cancelled = 4,

        /// <summary>持久 record 的 schema、长度、binding 或字段无效。</summary>
        InvalidRecord = 5,

        /// <summary>OS 保护或解保护操作失败。</summary>
        ProtectionFailure = 6,

        /// <summary>文件或平台存储无法完整提交。</summary>
        StorageFailure = 7,

        /// <summary>Store 已停止并拒绝后续操作。</summary>
        Stopped = 8,
    }

    /// <summary>
    /// 表示不携带 secret、路径或异常文本的 secure store 结果。
    /// </summary>
    /// <typeparam name="T">成功时返回的 application-owned value 类型。</typeparam>
    internal sealed class ClientSecureSessionStoreResult<T>
        where T : class
    {
        /// <summary>创建封闭 store 结果。</summary>
        /// <param name="outcome">稳定低基数结果。</param>
        /// <param name="value">仅成功时存在的值。</param>
        private ClientSecureSessionStoreResult(
            ClientSecureSessionStoreOutcome outcome,
            T value)
        {
            if (outcome == ClientSecureSessionStoreOutcome.Succeeded && value == null)
            {
                throw new ArgumentNullException(nameof(value));
            }

            if (outcome != ClientSecureSessionStoreOutcome.Succeeded && value != null)
            {
                throw new ArgumentException("非成功 secure store 结果不得携带值。", nameof(value));
            }

            Outcome = outcome;
            Value = value;
        }

        /// <summary>获取稳定操作结果。</summary>
        internal ClientSecureSessionStoreOutcome Outcome { get; }

        /// <summary>获取成功值；非成功结果为空。</summary>
        internal T Value { get; }

        /// <summary>获取操作是否完整成功。</summary>
        internal bool IsSuccess => Outcome == ClientSecureSessionStoreOutcome.Succeeded;

        /// <summary>创建成功结果。</summary>
        /// <param name="value">完整有效的成功值。</param>
        /// <returns>携带值的成功结果。</returns>
        internal static ClientSecureSessionStoreResult<T> Success(T value)
        {
            return new ClientSecureSessionStoreResult<T>(
                ClientSecureSessionStoreOutcome.Succeeded,
                value);
        }

        /// <summary>创建不携带值的稳定结果。</summary>
        /// <param name="outcome">Succeeded 以外的封闭结果。</param>
        /// <returns>不携带敏感上下文的结果。</returns>
        /// <exception cref="ArgumentException">Outcome 为 Succeeded 时抛出。</exception>
        internal static ClientSecureSessionStoreResult<T> Failed(
            ClientSecureSessionStoreOutcome outcome)
        {
            if (outcome == ClientSecureSessionStoreOutcome.Succeeded)
            {
                throw new ArgumentException("成功结果必须携带值。", nameof(outcome));
            }

            return new ClientSecureSessionStoreResult<T>(outcome, null);
        }

        /// <summary>返回固定低敏结果摘要。</summary>
        /// <returns>只包含 outcome，不包含 record 或 secret。</returns>
        public override string ToString()
        {
            return $"ClientSecureSessionStoreResult outcome={Outcome}";
        }
    }

    /// <summary>
    /// 表示 secure store mutation 的无数据成功值。
    /// </summary>
    internal sealed class ClientSecureSessionStoreEmpty
    {
        /// <summary>提供唯一无状态实例。</summary>
        internal static readonly ClientSecureSessionStoreEmpty Value =
            new ClientSecureSessionStoreEmpty();

        /// <summary>阻止外部创建无意义实例。</summary>
        private ClientSecureSessionStoreEmpty()
        {
        }
    }

    /// <summary>
    /// 保存可由 OS 安全存储持久化的唯一 refresh lineage 与恢复所需最小摘要。
    /// </summary>
    /// <remarks>
    /// Record 不包含 access token、password、ticket、admission、endpoint 或 world/visit 事实。
    /// Infrastructure 必须把完整 record 放入 OS 保护 payload，不能只保护 RefreshToken 字段。
    /// </remarks>
    internal sealed class ClientSecureSessionRecord
    {
        /// <summary>当前持久 record schema。</summary>
        internal const int CurrentSchemaVersion = 1;

        /// <summary>限制 opaque refresh token 的 encoded 字符数。</summary>
        internal const int MaximumRefreshTokenLength = 4096;

        /// <summary>创建经过 application contract 验证的持久 record。</summary>
        /// <param name="schemaVersion">持久 record schema。</param>
        /// <param name="environmentBinding">64 字符小写十六进制环境绑定。</param>
        /// <param name="account">恢复后重建 Session snapshot 所需账号摘要。</param>
        /// <param name="session">Refresh 所属 session identity、epoch 与 expiry。</param>
        /// <param name="refreshToken">只允许交给 refresh operation 的 opaque token。</param>
        /// <param name="refreshExpiresAtMilliseconds">Refresh token 绝对 Unix expiry，单位为毫秒。</param>
        /// <exception cref="ArgumentException">任一字段不满足持久契约时抛出低敏异常。</exception>
        /// <exception cref="ArgumentNullException">任一引用为空时抛出。</exception>
        internal ClientSecureSessionRecord(
            int schemaVersion,
            string environmentBinding,
            ClientAccountSummary account,
            ClientSessionSummary session,
            string refreshToken,
            long refreshExpiresAtMilliseconds)
        {
            Account = account ?? throw new ArgumentNullException(nameof(account));
            Session = session ?? throw new ArgumentNullException(nameof(session));
            if (schemaVersion != CurrentSchemaVersion)
            {
                throw new ArgumentException("Secure session record schema 不受支持。", nameof(schemaVersion));
            }

            if (!IsLowerHex(environmentBinding, 64))
            {
                throw new ArgumentException("Secure session environment binding 无效。", nameof(environmentBinding));
            }

            if (!IsBoundedText(account.AccountID, 1, 128) ||
                !IsBoundedText(account.DisplayName, 1, 128) ||
                account.CreatedAtMilliseconds < 0 ||
                !IsBoundedText(session.SessionID, 1, 128) ||
                session.SessionEpoch <= 0 ||
                session.ExpiresAtMilliseconds <= 0 ||
                !IsOpaqueCredential(refreshToken) ||
                refreshExpiresAtMilliseconds <= 0 ||
                refreshExpiresAtMilliseconds > session.ExpiresAtMilliseconds)
            {
                throw new ArgumentException("Secure session record 字段组合无效。", nameof(refreshToken));
            }

            SchemaVersion = schemaVersion;
            EnvironmentBinding = environmentBinding;
            RefreshToken = refreshToken;
            RefreshExpiresAtMilliseconds = refreshExpiresAtMilliseconds;
        }

        /// <summary>获取 record schema。</summary>
        internal int SchemaVersion { get; }

        /// <summary>获取防止跨环境恢复的确定性绑定。</summary>
        internal string EnvironmentBinding { get; }

        /// <summary>获取持久账号摘要。</summary>
        internal ClientAccountSummary Account { get; }

        /// <summary>获取持久 session 摘要。</summary>
        internal ClientSessionSummary Session { get; }

        /// <summary>获取只能交给 refresh operation 的 opaque token。</summary>
        internal string RefreshToken { get; }

        /// <summary>获取 refresh token 绝对 Unix expiry，单位为毫秒。</summary>
        internal long RefreshExpiresAtMilliseconds { get; }

        /// <summary>创建 current session snapshot 对应的持久 record。</summary>
        /// <param name="environmentBinding">当前客户端环境绑定。</param>
        /// <param name="snapshot">准备提交或已经 current 的完整 session snapshot。</param>
        /// <returns>只包含 refresh lineage 的有效 record。</returns>
        internal static ClientSecureSessionRecord FromSnapshot(
            string environmentBinding,
            ClientSessionSnapshot snapshot)
        {
            if (snapshot == null)
            {
                throw new ArgumentNullException(nameof(snapshot));
            }

            return new ClientSecureSessionRecord(
                CurrentSchemaVersion,
                environmentBinding,
                snapshot.Account,
                snapshot.Session,
                snapshot.Tokens.RefreshToken,
                snapshot.Tokens.RefreshExpiresAtMilliseconds);
        }

        /// <summary>返回固定脱敏摘要。</summary>
        /// <returns>只包含 schema 与 session epoch。</returns>
        public override string ToString()
        {
            return $"ClientSecureSessionRecord[REDACTED] schema={SchemaVersion} sessionEpoch={Session.SessionEpoch}";
        }

        /// <summary>验证固定长度小写十六进制文本。</summary>
        /// <param name="value">待验证文本。</param>
        /// <param name="length">要求的字符数。</param>
        /// <returns>长度和字符集均匹配时返回 true。</returns>
        private static bool IsLowerHex(string value, int length)
        {
            if (value == null || value.Length != length)
            {
                return false;
            }

            foreach (var character in value)
            {
                if (!((character >= '0' && character <= '9') ||
                      (character >= 'a' && character <= 'f')))
                {
                    return false;
                }
            }

            return true;
        }

        /// <summary>验证持久摘要只含有限可打印文本。</summary>
        /// <param name="value">待验证文本。</param>
        /// <param name="minimumLength">最小字符数。</param>
        /// <param name="maximumLength">最大字符数。</param>
        /// <returns>长度和字符范围均有效时返回 true。</returns>
        private static bool IsBoundedText(
            string value,
            int minimumLength,
            int maximumLength)
        {
            if (value == null || value.Length < minimumLength || value.Length > maximumLength)
            {
                return false;
            }

            foreach (var character in value)
            {
                if (char.IsControl(character))
                {
                    return false;
                }
            }

            return true;
        }

        /// <summary>验证 opaque credential 的有界 ASCII grammar。</summary>
        /// <param name="value">不得记录的 credential。</param>
        /// <returns>长度与字符集均有效时返回 true。</returns>
        private static bool IsOpaqueCredential(string value)
        {
            if (string.IsNullOrEmpty(value) || value.Length > MaximumRefreshTokenLength)
            {
                return false;
            }

            foreach (var character in value)
            {
                var isLetterOrDigit =
                    (character >= 'A' && character <= 'Z') ||
                    (character >= 'a' && character <= 'z') ||
                    (character >= '0' && character <= '9');
                if (!isLetterOrDigit &&
                    character != '.' && character != '_' &&
                    character != '~' && character != '-')
                {
                    return false;
                }
            }

            return true;
        }
    }

    /// <summary>
    /// 定义 Session owner 消费的唯一安全持久化边界。
    /// </summary>
    /// <remarks>
    /// 实现必须把完整 record 视为 secret。该接口不提供任意 key、路径、枚举或部分字段更新，
    /// 使 refresh lineage 只能完整读取、原子替换或精确删除。
    /// </remarks>
    internal interface IClientSecureSessionStore : IAppLifetimeParticipant
    {
        /// <summary>读取当前 profile 的完整 record。</summary>
        /// <param name="cancellationToken">操作提交前的取消信号。</param>
        /// <returns>成功 record、NotFound 或稳定低敏失败。</returns>
        Task<ClientSecureSessionStoreResult<ClientSecureSessionRecord>> ReadAsync(
            CancellationToken cancellationToken);

        /// <summary>原子替换当前 profile 的完整 record。</summary>
        /// <param name="record">已经通过 application contract 验证的候选 record。</param>
        /// <param name="cancellationToken">原子 replace 提交前的取消信号。</param>
        /// <returns>完整提交或稳定低敏失败。</returns>
        Task<ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>> ReplaceAsync(
            ClientSecureSessionRecord record,
            CancellationToken cancellationToken);

        /// <summary>删除当前 profile 的精确 record。</summary>
        /// <param name="cancellationToken">删除提交前的取消信号。</param>
        /// <returns>删除成功、原本不存在或稳定低敏失败。</returns>
        Task<ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>> DeleteAsync(
            CancellationToken cancellationToken);
    }
}
