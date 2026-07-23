using System;
using System.Collections.Generic;
using System.Net;
using IHomeland.Client.Application.Contracts;

namespace IHomeland.Client.Infrastructure.Http
{
    /// <summary>
    /// 保存客户端用于校验 ErrorResponse 的单条冻结 registry 记录。
    /// </summary>
    internal sealed class ClientKnownError
    {
        /// <summary>
        /// 创建与 shared error registry 一致的记录。
        /// </summary>
        /// <param name="code">稳定错误码。</param>
        /// <param name="category">客户端恢复类别。</param>
        /// <param name="messageKey">稳定本地化 key。</param>
        /// <param name="retryable">冻结 retryable 语义。</param>
        /// <param name="statusCode">冻结 HTTP status。</param>
        internal ClientKnownError(
            int code,
            ClientServerErrorCategory category,
            string messageKey,
            bool retryable,
            HttpStatusCode statusCode)
        {
            Code = code;
            Category = category;
            MessageKey = messageKey ?? throw new ArgumentNullException(nameof(messageKey));
            Retryable = retryable;
            StatusCode = statusCode;
        }

        /// <summary>
        /// 获取稳定错误码。
        /// </summary>
        internal int Code { get; }

        /// <summary>
        /// 获取客户端恢复类别。
        /// </summary>
        internal ClientServerErrorCategory Category { get; }

        /// <summary>
        /// 获取冻结 messageKey。
        /// </summary>
        internal string MessageKey { get; }

        /// <summary>
        /// 获取冻结 retryable 语义。
        /// </summary>
        internal bool Retryable { get; }

        /// <summary>
        /// 获取冻结 HTTP status。
        /// </summary>
        internal HttpStatusCode StatusCode { get; }
    }

    /// <summary>
    /// 集中校验客户端已知错误码的 category、messageKey、retryable 与 HTTP status。
    /// </summary>
    internal static class ClientErrorRegistry
    {
        /// <summary>
        /// 保存由 code 唯一索引的冻结错误记录。
        /// </summary>
        private static readonly IReadOnlyDictionary<int, ClientKnownError> Errors = Build();

        /// <summary>
        /// 尝试取得当前客户端认识的错误记录。
        /// </summary>
        /// <param name="code">服务端 ErrorResponse code。</param>
        /// <param name="knownError">命中时返回不可变 registry 记录。</param>
        /// <returns>当前客户端认识该 code 时返回 true。</returns>
        internal static bool TryGet(int code, out ClientKnownError knownError)
        {
            return Errors.TryGetValue(code, out knownError);
        }

        /// <summary>
        /// 构建与 `shared/contracts/registry/errors.json` 对齐的唯一 code map。
        /// </summary>
        /// <returns>初始化后只读使用的完整错误表。</returns>
        private static IReadOnlyDictionary<int, ClientKnownError> Build()
        {
            var errors = new Dictionary<int, ClientKnownError>();
            Add(errors, 1, ClientServerErrorCategory.Protocol, "error.protocol.invalid_envelope", false, HttpStatusCode.BadRequest);
            Add(errors, 2, ClientServerErrorCategory.Protocol, "error.protocol.unsupported_version", false, HttpStatusCode.UpgradeRequired);
            Add(errors, 100, ClientServerErrorCategory.Authentication, "error.auth.unauthenticated", false, HttpStatusCode.Unauthorized);
            Add(errors, 101, ClientServerErrorCategory.Authentication, "error.auth.forbidden", false, HttpStatusCode.Forbidden);
            Add(errors, 102, ClientServerErrorCategory.Authentication, "error.auth.invalid_credentials", false, HttpStatusCode.Unauthorized);
            Add(errors, 103, ClientServerErrorCategory.Authentication, "error.auth.ticket_expired", false, HttpStatusCode.Unauthorized);
            Add(errors, 104, ClientServerErrorCategory.Conflict, "error.account.username_taken", false, HttpStatusCode.Conflict);
            Add(errors, 200, ClientServerErrorCategory.Validation, "error.validation.failed", false, HttpStatusCode.BadRequest);
            Add(errors, 400, ClientServerErrorCategory.RateLimit, "error.rate_limited", true, HttpStatusCode.TooManyRequests);
            Add(errors, 500, ClientServerErrorCategory.Dependency, "error.dependency.unavailable", true, HttpStatusCode.ServiceUnavailable);
            Add(errors, 501, ClientServerErrorCategory.Internal, "error.internal", true, HttpStatusCode.InternalServerError);
            Add(errors, 2000, ClientServerErrorCategory.NotFound, "error.world.not_found", false, HttpStatusCode.NotFound);
            Add(errors, 2001, ClientServerErrorCategory.Conflict, "error.world.not_ready", true, HttpStatusCode.Conflict);
            Add(errors, 2002, ClientServerErrorCategory.Conflict, "error.world.assignment_stale", false, HttpStatusCode.Conflict);
            Add(errors, 2003, ClientServerErrorCategory.Authentication, "error.world.admission_invalid", false, HttpStatusCode.Unauthorized);
            Add(errors, 2004, ClientServerErrorCategory.Authentication, "error.world.admission_expired", false, HttpStatusCode.Unauthorized);
            Add(errors, 2005, ClientServerErrorCategory.Conflict, "error.world.admission_replayed", false, HttpStatusCode.Conflict);
            Add(errors, 2006, ClientServerErrorCategory.Conflict, "error.world.idempotency_conflict", false, HttpStatusCode.Conflict);
            Add(errors, 2100, ClientServerErrorCategory.NotFound, "error.visit.not_found", false, HttpStatusCode.NotFound);
            Add(errors, 2101, ClientServerErrorCategory.NotFound, "error.visit.invite_not_found", false, HttpStatusCode.NotFound);
            Add(errors, 2102, ClientServerErrorCategory.Conflict, "error.visit.invite_expired", false, HttpStatusCode.Gone);
            Add(errors, 2103, ClientServerErrorCategory.Conflict, "error.visit.capacity_exceeded", false, HttpStatusCode.Conflict);
            Add(errors, 2104, ClientServerErrorCategory.Conflict, "error.visit.state_conflict", false, HttpStatusCode.Conflict);
            Add(errors, 2105, ClientServerErrorCategory.Conflict, "error.visit.revision_conflict", false, HttpStatusCode.Conflict);
            Add(errors, 2106, ClientServerErrorCategory.Conflict, "error.visit.idempotency_conflict", false, HttpStatusCode.Conflict);
            Add(errors, 2107, ClientServerErrorCategory.Authentication, "error.visit.membership_required", false, HttpStatusCode.Forbidden);
            Add(errors, 2108, ClientServerErrorCategory.Conflict, "error.visit.owner_unavailable", false, HttpStatusCode.Conflict);
            Add(errors, 2109, ClientServerErrorCategory.Conflict, "error.visit.reconnect_expired", false, HttpStatusCode.Gone);
            return errors;
        }

        /// <summary>
        /// 添加单条 code 并拒绝重复初始化，避免静默覆盖 contract 漂移。
        /// </summary>
        /// <param name="errors">正在构建的唯一 code map。</param>
        /// <param name="code">稳定错误码。</param>
        /// <param name="category">客户端恢复类别。</param>
        /// <param name="messageKey">冻结本地化 key。</param>
        /// <param name="retryable">冻结 retryable 语义。</param>
        /// <param name="statusCode">冻结 HTTP status。</param>
        private static void Add(
            IDictionary<int, ClientKnownError> errors,
            int code,
            ClientServerErrorCategory category,
            string messageKey,
            bool retryable,
            HttpStatusCode statusCode)
        {
            errors.Add(code, new ClientKnownError(code, category, messageKey, retryable, statusCode));
        }
    }
}
