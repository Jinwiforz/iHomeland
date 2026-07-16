using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.Net;

namespace IHomeland.Client.Infrastructure.Http
{
    /// <summary>
    /// 标识客户端在 HTTP 边界可以安全交给上层的失败类别。
    /// </summary>
    internal enum ClientHttpFailureKind
    {
        /// <summary>
        /// 调用方主动取消等待。
        /// </summary>
        CallerCancelled = 0,

        /// <summary>
        /// Operation deadline 在完整响应前到期。
        /// </summary>
        Timeout = 1,

        /// <summary>
        /// DNS、connect、TLS 或 HTTP 传输失败。
        /// </summary>
        Transport = 2,

        /// <summary>
        /// App Scope 已停止并撤销请求所有权。
        /// </summary>
        Stopped = 3,

        /// <summary>
        /// 服务端响应超出该 operation 的客户端字节上限。
        /// </summary>
        ResponseTooLarge = 4,

        /// <summary>
        /// Status、media type 或 JSON 投影不符合冻结契约。
        /// </summary>
        MalformedResponse = 5,

        /// <summary>
        /// 本地环境、版本或生命周期前置条件不成立。
        /// </summary>
        LocalPolicy = 6,
    }

    /// <summary>
    /// 保存不会泄漏底层 exception、body 或 credential 的本地 HTTP 失败。
    /// </summary>
    internal sealed class ClientHttpFailure
    {
        /// <summary>
        /// 创建只包含稳定 failure kind 与 operation correlation 的失败结果。
        /// </summary>
        /// <param name="kind">调用方可据此选择恢复路径的封闭失败类别。</param>
        /// <param name="operationID">发生失败的冻结 operationId。</param>
        /// <param name="statusCode">已收到响应时的 HTTP status；传输前失败时为空。</param>
        internal ClientHttpFailure(
            ClientHttpFailureKind kind,
            string operationID,
            HttpStatusCode? statusCode = null)
        {
            Kind = kind;
            OperationID = operationID ?? throw new ArgumentNullException(nameof(operationID));
            StatusCode = statusCode;
        }

        /// <summary>
        /// 获取稳定失败类别。
        /// </summary>
        internal ClientHttpFailureKind Kind { get; }

        /// <summary>
        /// 获取不含 path 参数或 credential 的 operation correlation。
        /// </summary>
        internal string OperationID { get; }

        /// <summary>
        /// 获取已经安全接收的 HTTP status；未收到响应时为空。
        /// </summary>
        internal HttpStatusCode? StatusCode { get; }

        /// <summary>
        /// 返回不含内部异常和远端 body 的安全诊断文本。
        /// </summary>
        /// <returns>只包含 kind、operationId 与可选 status class 的字符串。</returns>
        public override string ToString()
        {
            return StatusCode.HasValue
                ? $"{Kind} operation={OperationID} status={(int)StatusCode.Value}"
                : $"{Kind} operation={OperationID}";
        }
    }

    /// <summary>
    /// 标识冻结 error registry 中供客户端恢复策略使用的类别。
    /// </summary>
    internal enum ClientServerErrorCategory
    {
        /// <summary>
        /// 当前客户端尚不认识但结构有效的服务端错误。
        /// </summary>
        Unknown = 0,

        /// <summary>
        /// 协议版本或 envelope 不兼容。
        /// </summary>
        Protocol = 1,

        /// <summary>
        /// 认证或 credential 失效。
        /// </summary>
        Authentication = 2,

        /// <summary>
        /// 请求字段未通过验证。
        /// </summary>
        Validation = 3,

        /// <summary>
        /// 当前状态与请求冲突。
        /// </summary>
        Conflict = 4,

        /// <summary>
        /// 目标事实不存在。
        /// </summary>
        NotFound = 5,

        /// <summary>
        /// 服务端限流预算耗尽。
        /// </summary>
        RateLimit = 6,

        /// <summary>
        /// 服务端依赖暂不可用。
        /// </summary>
        Dependency = 7,

        /// <summary>
        /// 服务端返回安全内部失败。
        /// </summary>
        Internal = 8,
    }

    /// <summary>
    /// 保存服务端允许公开的单个字段错误，不包含原始输入值。
    /// </summary>
    internal sealed class ClientErrorDetail
    {
        /// <summary>
        /// 创建有界字段错误投影。
        /// </summary>
        /// <param name="field">长度不超过 64 的字段路径。</param>
        /// <param name="reason">长度不超过 64 的稳定原因。</param>
        internal ClientErrorDetail(string field, string reason)
        {
            Field = field ?? throw new ArgumentNullException(nameof(field));
            Reason = reason ?? throw new ArgumentNullException(nameof(reason));
        }

        /// <summary>
        /// 获取不包含字段值的安全字段路径。
        /// </summary>
        internal string Field { get; }

        /// <summary>
        /// 获取稳定且不含内部异常的拒绝原因。
        /// </summary>
        internal string Reason { get; }
    }

    /// <summary>
    /// 保存已验证的公开 ErrorResponse 与客户端 registry 分类。
    /// </summary>
    internal sealed class ClientServerError
    {
        /// <summary>
        /// 创建安全服务端错误投影。
        /// </summary>
        /// <param name="code">服务端稳定错误码。</param>
        /// <param name="category">客户端已知类别或 Unknown。</param>
        /// <param name="messageKey">供 UI 本地化的稳定 key。</param>
        /// <param name="requestID">服务端公开的有界 correlation。</param>
        /// <param name="retryable">服务端声明该结果是否允许显式重试。</param>
        /// <param name="retryAfter">可选重试等待时间。</param>
        /// <param name="details">最多 16 个不含输入值的字段错误。</param>
        internal ClientServerError(
            int code,
            ClientServerErrorCategory category,
            string messageKey,
            string requestID,
            bool retryable,
            TimeSpan? retryAfter,
            IReadOnlyList<ClientErrorDetail> details)
        {
            Code = code;
            Category = category;
            MessageKey = messageKey ?? throw new ArgumentNullException(nameof(messageKey));
            RequestID = requestID ?? throw new ArgumentNullException(nameof(requestID));
            Retryable = retryable;
            RetryAfter = retryAfter;
            Details = new ReadOnlyCollection<ClientErrorDetail>(
                new List<ClientErrorDetail>(details ?? throw new ArgumentNullException(nameof(details))));
        }

        /// <summary>
        /// 获取服务端稳定错误码。
        /// </summary>
        internal int Code { get; }

        /// <summary>
        /// 获取当前客户端对已知错误的恢复类别。
        /// </summary>
        internal ClientServerErrorCategory Category { get; }

        /// <summary>
        /// 获取安全本地化 key，而不是服务端内部错误文本。
        /// </summary>
        internal string MessageKey { get; }

        /// <summary>
        /// 获取服务端公开的有界 request correlation。
        /// </summary>
        internal string RequestID { get; }

        /// <summary>
        /// 获取服务端对显式重试的声明；该值不会触发自动重试。
        /// </summary>
        internal bool Retryable { get; }

        /// <summary>
        /// 获取服务端建议的有界等待时间；缺失时为空。
        /// </summary>
        internal TimeSpan? RetryAfter { get; }

        /// <summary>
        /// 获取不可变且不包含原始输入值的字段错误集合。
        /// </summary>
        internal IReadOnlyList<ClientErrorDetail> Details { get; }

        /// <summary>
        /// 返回仅包含公开 correlation 的安全诊断文本。
        /// </summary>
        /// <returns>错误码、类别、messageKey 与 requestId。</returns>
        public override string ToString()
        {
            return $"ServerError code={Code} category={Category} messageKey={MessageKey} requestId={RequestID}";
        }
    }

    /// <summary>
    /// 表示 HTTP 调用的成功值、服务端错误或本地失败三选一结果。
    /// </summary>
    /// <typeparam name="T">成功时返回的不可变投影类型。</typeparam>
    internal sealed class ClientHttpResult<T>
    {
        /// <summary>
        /// 创建封闭结果；调用方只能通过静态工厂取得合法组合。
        /// </summary>
        /// <param name="value">成功值。</param>
        /// <param name="serverError">已验证服务端错误。</param>
        /// <param name="failure">本地失败。</param>
        private ClientHttpResult(T value, ClientServerError serverError, ClientHttpFailure failure)
        {
            Value = value;
            ServerError = serverError;
            Failure = failure;
        }

        /// <summary>
        /// 获取结果是否包含成功值。
        /// </summary>
        internal bool IsSuccess => ServerError == null && Failure == null;

        /// <summary>
        /// 获取成功投影；非成功结果为类型默认值。
        /// </summary>
        internal T Value { get; }

        /// <summary>
        /// 获取结构有效的服务端错误；其他结果为空。
        /// </summary>
        internal ClientServerError ServerError { get; }

        /// <summary>
        /// 获取客户端本地失败；其他结果为空。
        /// </summary>
        internal ClientHttpFailure Failure { get; }

        /// <summary>
        /// 创建成功结果。
        /// </summary>
        /// <param name="value">已经完整验证且所有权转移给调用方的成功投影。</param>
        /// <returns>只包含成功值的结果。</returns>
        /// <exception cref="ArgumentNullException">引用类型成功值为空时抛出。</exception>
        internal static ClientHttpResult<T> Success(T value)
        {
            if (ReferenceEquals(value, null))
            {
                throw new ArgumentNullException(nameof(value));
            }

            return new ClientHttpResult<T>(value, null, null);
        }

        /// <summary>
        /// 创建服务端拒绝结果。
        /// </summary>
        /// <param name="error">已通过 ErrorResponse 和 registry 验证的错误。</param>
        /// <returns>只包含服务端错误的结果。</returns>
        internal static ClientHttpResult<T> Rejected(ClientServerError error)
        {
            return new ClientHttpResult<T>(default, error ?? throw new ArgumentNullException(nameof(error)), null);
        }

        /// <summary>
        /// 创建本地失败结果。
        /// </summary>
        /// <param name="failure">不含内部 cause 或 credential 的稳定失败。</param>
        /// <returns>只包含本地失败的结果。</returns>
        internal static ClientHttpResult<T> Failed(ClientHttpFailure failure)
        {
            return new ClientHttpResult<T>(default, null, failure ?? throw new ArgumentNullException(nameof(failure)));
        }
    }

    /// <summary>
    /// 表示没有业务 payload 的 HTTP operation 成功值。
    /// </summary>
    internal readonly struct ClientHttpEmpty
    {
    }
}
