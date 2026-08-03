using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.Net;
using System.Net.Http;

namespace IHomeland.Client.Infrastructure.Http
{
    /// <summary>
    /// 标识一个冻结 HTTP operation 的请求 body 规则。
    /// </summary>
    internal enum ClientHttpBodyPolicy
    {
        /// <summary>
        /// 请求不得携带 body。
        /// </summary>
        None = 0,

        /// <summary>
        /// 请求必须携带单一 JSON object。
        /// </summary>
        Json = 1,
    }

    /// <summary>
    /// 标识 operation 是否需要 Session owner 提供 Bearer credential。
    /// </summary>
    internal enum ClientHttpAuthentication
    {
        /// <summary>
        /// Operation 在认证前公开，不发送 Authorization header。
        /// </summary>
        Anonymous = 0,

        /// <summary>
        /// Operation 必须使用当前 session snapshot 的 access token。
        /// </summary>
        Bearer = 1,
    }

    /// <summary>
    /// 描述客户端允许发送的单个冻结 HTTP operation 及其资源预算。
    /// </summary>
    internal sealed class ClientHttpOperation
    {
        /// <summary>
        /// 创建不可变 operation descriptor。
        /// </summary>
        /// <param name="operationID">与 OpenAPI operationId 完全一致的稳定名称。</param>
        /// <param name="method">固定 HTTP method。</param>
        /// <param name="relativePath">不含 query 的根相对 path。</param>
        /// <param name="authentication">是否要求当前 Bearer credential。</param>
        /// <param name="requestBodyPolicy">请求 body 的封闭规则。</param>
        /// <param name="requestBodyLimitBytes">OpenAPI 登记的请求体上限，单位为字节。</param>
        /// <param name="successStatus">唯一成功 HTTP status。</param>
        /// <param name="successHasBody">成功响应是否必须包含 JSON body。</param>
        /// <param name="timeout">OpenAPI 登记的单次 operation deadline。</param>
        /// <param name="responseBodyLimitBytes">客户端防御性响应体硬上限，单位为字节。</param>
        /// <exception cref="ArgumentException">字符串、预算或 timeout 不符合 descriptor 约束时抛出。</exception>
        /// <exception cref="ArgumentNullException">method 为空时抛出。</exception>
        internal ClientHttpOperation(
            string operationID,
            HttpMethod method,
            string relativePath,
            ClientHttpAuthentication authentication,
            ClientHttpBodyPolicy requestBodyPolicy,
            int requestBodyLimitBytes,
            HttpStatusCode successStatus,
            bool successHasBody,
            TimeSpan timeout,
            int responseBodyLimitBytes)
        {
            if (string.IsNullOrWhiteSpace(operationID))
            {
                throw new ArgumentException("Operation ID 不能为空。", nameof(operationID));
            }

            Method = method ?? throw new ArgumentNullException(nameof(method));
            if (string.IsNullOrWhiteSpace(relativePath) || !relativePath.StartsWith("/", StringComparison.Ordinal))
            {
                throw new ArgumentException("Operation path 必须是根相对路径。", nameof(relativePath));
            }

            if (requestBodyLimitBytes < 0)
            {
                throw new ArgumentException("请求体上限不能为负数。", nameof(requestBodyLimitBytes));
            }

            if (requestBodyPolicy == ClientHttpBodyPolicy.None && requestBodyLimitBytes != 0)
            {
                throw new ArgumentException("无 body operation 的请求体上限必须为零。", nameof(requestBodyLimitBytes));
            }

            if (timeout <= TimeSpan.Zero)
            {
                throw new ArgumentException("Operation timeout 必须为正数。", nameof(timeout));
            }

            if (responseBodyLimitBytes <= 0)
            {
                throw new ArgumentException("响应体上限必须为正数。", nameof(responseBodyLimitBytes));
            }

            OperationID = operationID;
            RelativePath = relativePath;
            Authentication = authentication;
            RequestBodyPolicy = requestBodyPolicy;
            RequestBodyLimitBytes = requestBodyLimitBytes;
            SuccessStatus = successStatus;
            SuccessHasBody = successHasBody;
            Timeout = timeout;
            ResponseBodyLimitBytes = responseBodyLimitBytes;
        }

        /// <summary>
        /// 获取用于 contract correlation 的 OpenAPI operationId。
        /// </summary>
        internal string OperationID { get; }

        /// <summary>
        /// 获取发送请求时唯一允许的 HTTP method。
        /// </summary>
        internal HttpMethod Method { get; }

        /// <summary>
        /// 获取相对于已验证 base URI 的固定业务 path。
        /// </summary>
        internal string RelativePath { get; }

        /// <summary>
        /// 获取 Bearer credential 要求。
        /// </summary>
        internal ClientHttpAuthentication Authentication { get; }

        /// <summary>
        /// 获取请求 body 的封闭规则。
        /// </summary>
        internal ClientHttpBodyPolicy RequestBodyPolicy { get; }

        /// <summary>
        /// 获取请求 body 最大字节数；零表示禁止 body。
        /// </summary>
        internal int RequestBodyLimitBytes { get; }

        /// <summary>
        /// 获取当前版本唯一接受的成功 HTTP status。
        /// </summary>
        internal HttpStatusCode SuccessStatus { get; }

        /// <summary>
        /// 获取成功响应是否必须携带单一 JSON object。
        /// </summary>
        internal bool SuccessHasBody { get; }

        /// <summary>
        /// 获取从请求开始到完整响应读取结束的总 deadline。
        /// </summary>
        internal TimeSpan Timeout { get; }

        /// <summary>
        /// 获取该 operation 允许读取的最大响应字节数。
        /// </summary>
        internal int ResponseBodyLimitBytes { get; }
    }

    /// <summary>
    /// 集中拥有当前客户端网络阶段允许调用的十一个 HTTP operation descriptor。
    /// </summary>
    internal static class ClientHttpOperationCatalog
    {
        /// <summary>
        /// 允许 version/config 等小型响应使用的客户端上限，单位为字节。
        /// </summary>
        private const int SmallResponseLimitBytes = 16 * 1024;

        /// <summary>
        /// 允许 auth/config/world 复合投影使用的客户端上限，单位为字节。
        /// </summary>
        private const int CompositeResponseLimitBytes = 64 * 1024;

        /// <summary>
        /// 保存按冻结顺序暴露的 operation 只读集合。
        /// </summary>
        private static readonly ReadOnlyCollection<ClientHttpOperation> Operations;

        /// <summary>
        /// 初始化全部 descriptor，并保证运行时不存在任意 path escape hatch。
        /// </summary>
        static ClientHttpOperationCatalog()
        {
            GetVersion = Create("getVersion", HttpMethod.Get, "/v1/version", ClientHttpAuthentication.Anonymous, ClientHttpBodyPolicy.None, 0, HttpStatusCode.OK, true, 3000, SmallResponseLimitBytes);
            GetBootstrapConfig = Create("getBootstrapConfig", HttpMethod.Get, "/v1/config", ClientHttpAuthentication.Anonymous, ClientHttpBodyPolicy.None, 0, HttpStatusCode.OK, true, 3000, CompositeResponseLimitBytes);
            RegisterAccount = Create("registerAccount", HttpMethod.Post, "/v1/auth/register", ClientHttpAuthentication.Anonymous, ClientHttpBodyPolicy.Json, 4096, HttpStatusCode.Created, true, 10000, CompositeResponseLimitBytes);
            LoginAccount = Create("loginAccount", HttpMethod.Post, "/v1/auth/login", ClientHttpAuthentication.Anonymous, ClientHttpBodyPolicy.Json, 4096, HttpStatusCode.OK, true, 10000, CompositeResponseLimitBytes);
            RefreshSession = Create("refreshSession", HttpMethod.Post, "/v1/auth/refresh", ClientHttpAuthentication.Anonymous, ClientHttpBodyPolicy.Json, 4096, HttpStatusCode.OK, true, 5000, SmallResponseLimitBytes);
            LogoutSession = Create("logoutSession", HttpMethod.Post, "/v1/auth/logout", ClientHttpAuthentication.Bearer, ClientHttpBodyPolicy.None, 0, HttpStatusCode.NoContent, false, 5000, SmallResponseLimitBytes);
            IssueConnectionTicket = Create("issueConnectionTicket", HttpMethod.Post, "/v1/session/tickets", ClientHttpAuthentication.Bearer, ClientHttpBodyPolicy.Json, 2048, HttpStatusCode.Created, true, 5000, SmallResponseLimitBytes);
            GetWorldBootstrap = Create("getWorldBootstrap", HttpMethod.Get, "/v1/world/bootstrap", ClientHttpAuthentication.Bearer, ClientHttpBodyPolicy.None, 0, HttpStatusCode.OK, true, 5000, CompositeResponseLimitBytes);
            AcceptVisitInvite = Create("acceptVisitInvite", HttpMethod.Post, "/v1/visits/{visitSessionId}/invites/{inviteId}/accept", ClientHttpAuthentication.Bearer, ClientHttpBodyPolicy.Json, 4096, HttpStatusCode.OK, true, 5000, SmallResponseLimitBytes);
            IssueWorldAdmission = Create("issueWorldAdmission", HttpMethod.Post, "/v1/world/admissions", ClientHttpAuthentication.Bearer, ClientHttpBodyPolicy.Json, 4096, HttpStatusCode.Created, true, 5000, SmallResponseLimitBytes);
            IssueBattleTicket = Create("issueBattleTicket", HttpMethod.Post, "/v1/battle/tickets", ClientHttpAuthentication.Bearer, ClientHttpBodyPolicy.Json, 2048, HttpStatusCode.Created, true, 5000, SmallResponseLimitBytes);
            Operations = new ReadOnlyCollection<ClientHttpOperation>(new[]
            {
                GetVersion,
                GetBootstrapConfig,
                RegisterAccount,
                LoginAccount,
                RefreshSession,
                LogoutSession,
                IssueConnectionTicket,
                GetWorldBootstrap,
                AcceptVisitInvite,
                IssueWorldAdmission,
                IssueBattleTicket,
            });
        }

        /// <summary>
        /// 获取认证前版本兼容查询 descriptor。
        /// </summary>
        internal static ClientHttpOperation GetVersion { get; }

        /// <summary>
        /// 获取认证前公开配置查询 descriptor。
        /// </summary>
        internal static ClientHttpOperation GetBootstrapConfig { get; }

        /// <summary>
        /// 获取创建账号与 session 的 descriptor。
        /// </summary>
        internal static ClientHttpOperation RegisterAccount { get; }

        /// <summary>
        /// 获取凭据登录并创建 session 的 descriptor。
        /// </summary>
        internal static ClientHttpOperation LoginAccount { get; }

        /// <summary>
        /// 获取轮换当前 token pair 的 descriptor。
        /// </summary>
        internal static ClientHttpOperation RefreshSession { get; }

        /// <summary>
        /// 获取使当前 session epoch 失效的 descriptor。
        /// </summary>
        internal static ClientHttpOperation LogoutSession { get; }

        /// <summary>
        /// 获取签发单一 channel ticket 的 descriptor。
        /// </summary>
        internal static ClientHttpOperation IssueConnectionTicket { get; }

        /// <summary>
        /// 获取查询 own-world 安全启动投影的 descriptor。
        /// </summary>
        internal static ClientHttpOperation GetWorldBootstrap { get; }

        /// <summary>
        /// 获取目标 Visitor 接受定向 invite 的 descriptor。
        /// </summary>
        internal static ClientHttpOperation AcceptVisitInvite { get; }

        /// <summary>
        /// 获取签发一次性 gameplay world admission 的 descriptor。
        /// </summary>
        internal static ClientHttpOperation IssueWorldAdmission { get; }

        /// <summary>
        /// 获取为 current own/visit target 签发一次性 BattleTicket 的 descriptor。
        /// </summary>
        internal static ClientHttpOperation IssueBattleTicket { get; }

        /// <summary>
        /// 获取只包含当前 HTTP bootstrap capability 允许 operation 的稳定只读快照。
        /// </summary>
        internal static IReadOnlyList<ClientHttpOperation> All => Operations;

        /// <summary>
        /// 以毫秒 contract 值创建 descriptor，避免各声明重复单位换算。
        /// </summary>
        /// <param name="operationID">OpenAPI operationId。</param>
        /// <param name="method">HTTP method。</param>
        /// <param name="path">根相对 path。</param>
        /// <param name="authentication">Bearer 要求。</param>
        /// <param name="bodyPolicy">请求 body 规则。</param>
        /// <param name="requestLimitBytes">请求上限，单位为字节。</param>
        /// <param name="successStatus">唯一成功 status。</param>
        /// <param name="successHasBody">成功时是否要求 JSON body。</param>
        /// <param name="timeoutMilliseconds">总 deadline，单位为毫秒。</param>
        /// <param name="responseLimitBytes">客户端响应上限，单位为字节。</param>
        /// <returns>完成单位转换的不可变 descriptor。</returns>
        private static ClientHttpOperation Create(
            string operationID,
            HttpMethod method,
            string path,
            ClientHttpAuthentication authentication,
            ClientHttpBodyPolicy bodyPolicy,
            int requestLimitBytes,
            HttpStatusCode successStatus,
            bool successHasBody,
            int timeoutMilliseconds,
            int responseLimitBytes)
        {
            return new ClientHttpOperation(
                operationID,
                method,
                path,
                authentication,
                bodyPolicy,
                requestLimitBytes,
                successStatus,
                successHasBody,
                TimeSpan.FromMilliseconds(timeoutMilliseconds),
                responseLimitBytes);
        }
    }
}
