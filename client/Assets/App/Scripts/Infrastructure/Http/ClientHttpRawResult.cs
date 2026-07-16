using System;
using System.Net;

namespace IHomeland.Client.Infrastructure.Http
{
    /// <summary>
    /// 保存有界读取完成但尚未进行 operation schema 解码的 HTTP 响应。
    /// </summary>
    internal sealed class ClientHttpRawResponse
    {
        /// <summary>
        /// 创建完整拥有 body buffer 的原始响应。
        /// </summary>
        /// <param name="statusCode">实际 HTTP status。</param>
        /// <param name="mediaType">Content-Type 的 media type；header 缺失时为空。</param>
        /// <param name="body">已按 operation 上限读取完成的新字节数组。</param>
        /// <param name="retryAfter">有效 Retry-After delta；header 缺失时为空。</param>
        internal ClientHttpRawResponse(
            HttpStatusCode statusCode,
            string mediaType,
            byte[] body,
            TimeSpan? retryAfter)
        {
            StatusCode = statusCode;
            MediaType = mediaType;
            Body = body ?? throw new ArgumentNullException(nameof(body));
            RetryAfter = retryAfter;
        }

        /// <summary>
        /// 获取实际 HTTP status。
        /// </summary>
        internal HttpStatusCode StatusCode { get; }

        /// <summary>
        /// 获取不含 charset 参数的 media type；header 缺失时为空。
        /// </summary>
        internal string MediaType { get; }

        /// <summary>
        /// 获取由当前响应独占且已受字节上限约束的 body buffer。
        /// </summary>
        internal byte[] Body { get; }

        /// <summary>
        /// 获取 Retry-After header 的 delta 投影；缺失或不是 delta 形式时为空。
        /// </summary>
        internal TimeSpan? RetryAfter { get; }
    }

    /// <summary>
    /// 表示 transport 已完整读取响应，或在进入 codec 前产生稳定本地失败。
    /// </summary>
    internal sealed class ClientHttpRawResult
    {
        /// <summary>
        /// 创建响应或失败二选一结果。
        /// </summary>
        /// <param name="response">成功读取的原始响应。</param>
        /// <param name="failure">不含内部 cause 的本地失败。</param>
        private ClientHttpRawResult(ClientHttpRawResponse response, ClientHttpFailure failure)
        {
            Response = response;
            Failure = failure;
        }

        /// <summary>
        /// 获取 transport 是否完整读取了一个有界响应。
        /// </summary>
        internal bool HasResponse => Response != null;

        /// <summary>
        /// 获取完整响应；本地失败时为空。
        /// </summary>
        internal ClientHttpRawResponse Response { get; }

        /// <summary>
        /// 获取稳定本地失败；完整响应时为空。
        /// </summary>
        internal ClientHttpFailure Failure { get; }

        /// <summary>
        /// 创建完整响应结果。
        /// </summary>
        /// <param name="response">所有权转移给 codec 的有界响应。</param>
        /// <returns>只包含 response 的 raw result。</returns>
        internal static ClientHttpRawResult Received(ClientHttpRawResponse response)
        {
            return new ClientHttpRawResult(
                response ?? throw new ArgumentNullException(nameof(response)),
                null);
        }

        /// <summary>
        /// 创建 transport 本地失败结果。
        /// </summary>
        /// <param name="failure">不含内部 exception 或 credential 的失败。</param>
        /// <returns>只包含 failure 的 raw result。</returns>
        internal static ClientHttpRawResult Failed(ClientHttpFailure failure)
        {
            return new ClientHttpRawResult(
                null,
                failure ?? throw new ArgumentNullException(nameof(failure)));
        }
    }
}
