using System;
using System.Buffers;
using System.Collections.Generic;
using System.IO;
using System.Net;
using System.Text;
using System.Text.Json;
using System.Text.RegularExpressions;

namespace IHomeland.Client.Infrastructure.Http
{
    /// <summary>
    /// 显式编码本阶段请求并把不可信 JSON 响应转换为不可变安全投影。
    /// </summary>
    /// <remarks>
    /// Codec 不使用 reflection DTO，避免 IL2CPP metadata 依赖。未知 response property 被忽略以容纳
    /// additive 演进，但当前版本的 required field、enum、范围和集合上限必须全部通过验证。
    /// </remarks>
    internal sealed class ClientHttpCodec
    {
        /// <summary>
        /// 校验 username 的冻结 ASCII grammar。
        /// </summary>
        private static readonly Regex UsernamePattern = new Regex(
            "^[A-Za-z0-9](?:[A-Za-z0-9._-]{1,62}[A-Za-z0-9])?$",
            RegexOptions.CultureInvariant);

        /// <summary>
        /// 校验 world/session/invite 等公开 identity 的安全 ASCII grammar。
        /// </summary>
        private static readonly Regex IdentityPattern = new Regex(
            "^[A-Za-z0-9._:-]+$",
            RegexOptions.CultureInvariant);

        /// <summary>
        /// 校验允许为空的字段名、reason、message key 与 request correlation，阻止控制字符进入日志或 UI。
        /// </summary>
        private static readonly Regex SafeAsciiTokenPattern = new Regex(
            "^[A-Za-z0-9._:-]*$",
            RegexOptions.CultureInvariant);

        /// <summary>
        /// 编码 register request，确保只产生 username、password 与 displayName 三个字段。
        /// </summary>
        /// <param name="username">3 到 64 字符的冻结 ASCII username。</param>
        /// <param name="password">12 到 128 字符且 UTF-8 不超过 128 字节的原始 password。</param>
        /// <param name="displayName">1 到 32 字符的显示名；最终 Unicode 规范化仍由服务端负责。</param>
        /// <returns>所有权交给单次 request content 的 UTF-8 JSON 字节。</returns>
        /// <exception cref="ArgumentException">任一输入不符合公开客户端约束时抛出；消息不回显输入。</exception>
        internal byte[] EncodeRegister(string username, string password, string displayName)
        {
            ValidateUsername(username);
            ValidatePassword(password, minimumCharacters: 12);
            ValidateText(displayName, 1, 32, nameof(displayName));
            return WriteObject(writer =>
            {
                writer.WriteString("username", username);
                writer.WriteString("password", password);
                writer.WriteString("displayName", displayName);
            });
        }

        /// <summary>
        /// 编码 login request，保持 password 原始内容且不增加 actor 字段。
        /// </summary>
        /// <param name="username">3 到 64 字符的冻结 ASCII username。</param>
        /// <param name="password">1 到 128 字符且 UTF-8 不超过 128 字节的原始 password。</param>
        /// <returns>所有权交给单次 request content 的 UTF-8 JSON 字节。</returns>
        /// <exception cref="ArgumentException">任一输入不符合公开客户端约束时抛出。</exception>
        internal byte[] EncodeLogin(string username, string password)
        {
            ValidateUsername(username);
            ValidatePassword(password, minimumCharacters: 1);
            return WriteObject(writer =>
            {
                writer.WriteString("username", username);
                writer.WriteString("password", password);
            });
        }

        /// <summary>
        /// 编码 refresh request，不解析或记录 opaque refresh token。
        /// </summary>
        /// <param name="refreshToken">长度 32 到 2048 的当前 opaque refresh token。</param>
        /// <returns>所有权交给单次 request content 的 UTF-8 JSON 字节。</returns>
        /// <exception cref="ArgumentException">Token 长度不符合 contract 时抛出。</exception>
        internal byte[] EncodeRefresh(string refreshToken)
        {
            ValidateText(refreshToken, 32, 2048, nameof(refreshToken));
            return WriteObject(writer => writer.WriteString("refreshToken", refreshToken));
        }

        /// <summary>
        /// 编码只允许选择 WSS 或 TLS_TCP 的 connection ticket request。
        /// </summary>
        /// <param name="channel">客户端请求的单一 realtime channel。</param>
        /// <returns>只包含 channel 字段的 UTF-8 JSON 字节。</returns>
        /// <exception cref="ArgumentOutOfRangeException">Channel enum 无效时抛出。</exception>
        internal byte[] EncodeTicket(ClientEndpointChannel channel)
        {
            var wireValue = channel switch
            {
                ClientEndpointChannel.Wss => "WSS",
                ClientEndpointChannel.TlsTcp => "TLS_TCP",
                _ => throw new ArgumentOutOfRangeException(nameof(channel), "Connection ticket channel 无效。"),
            };
            return WriteObject(writer => writer.WriteString("channel", wireValue));
        }

        /// <summary>
        /// 编码封闭 world admission target，不允许客户端自报 actor 或 assignment。
        /// </summary>
        /// <param name="target">Own-world 或 visit-world target。</param>
        /// <returns>只包含 schema 声明字段的 UTF-8 JSON。</returns>
        /// <exception cref="ArgumentNullException">Target 为空时抛出。</exception>
        /// <exception cref="ArgumentException">VisitSessionID 不符合公开 identity grammar 时抛出。</exception>
        internal byte[] EncodeWorldAdmission(ClientWorldAdmissionTarget target)
        {
            if (target == null)
            {
                throw new ArgumentNullException(nameof(target));
            }

            return WriteObject(writer =>
            {
                switch (target.Kind)
                {
                    case ClientWorldAdmissionTargetKind.OwnWorld:
                        writer.WriteString("kind", "OWN_WORLD");
                        break;
                    case ClientWorldAdmissionTargetKind.VisitWorld:
                        ValidateIdentity(target.VisitSessionID, nameof(target.VisitSessionID));
                        writer.WriteString("kind", "VISIT_WORLD");
                        writer.WriteString("visitSessionId", target.VisitSessionID);
                        break;
                    default:
                        throw new ArgumentOutOfRangeException(nameof(target), "World admission target kind 无效。");
                }
            });
        }

        /// <summary>
        /// 构造冻结 accept operation 的 path，并在创建 HTTP request 前验证两个 identity。
        /// </summary>
        /// <param name="request">目标 VisitSession、invite 与 expected revision。</param>
        /// <returns>只替换两个已验证 path segment 的根相对路径。</returns>
        /// <exception cref="ArgumentNullException">Request 为空时抛出。</exception>
        /// <exception cref="ArgumentException">Identity 或 expected revision 无效时抛出。</exception>
        internal string BuildVisitInviteAcceptPath(ClientVisitInviteAcceptRequest request)
        {
            ValidateVisitInviteAcceptRequest(request);
            return "/v1/visits/" + Uri.EscapeDataString(request.VisitSessionID) +
                   "/invites/" + Uri.EscapeDataString(request.InviteID) +
                   "/accept";
        }

        /// <summary>
        /// 编码 invite accept request，确保 body 只包含 expectedRevision。
        /// </summary>
        /// <param name="request">目标 VisitSession、invite 与 expected revision。</param>
        /// <returns>只包含 expectedRevision 的 UTF-8 JSON。</returns>
        /// <exception cref="ArgumentNullException">Request 为空时抛出。</exception>
        /// <exception cref="ArgumentException">Identity 或 expected revision 无效时抛出。</exception>
        internal byte[] EncodeVisitInviteAccept(ClientVisitInviteAcceptRequest request)
        {
            ValidateVisitInviteAcceptRequest(request);
            return WriteObject(writer => writer.WriteNumber("expectedRevision", request.ExpectedRevision));
        }

        /// <summary>
        /// 解码 version response 并验证全部 required field。
        /// </summary>
        /// <param name="body">已通过字节上限检查的 UTF-8 JSON body。</param>
        /// <returns>不可变版本投影。</returns>
        /// <exception cref="ClientHttpContractException">JSON 或字段不符合冻结 schema 时抛出。</exception>
        internal ClientVersionInfo DecodeVersion(ReadOnlyMemory<byte> body)
        {
            return ReadObject(body, root => new ClientVersionInfo(
                ReadInt32(root, "protocolVersion", 1, int.MaxValue),
                ReadString(root, "minimumClientVersion", 1, 32),
                ReadString(root, "serverVersion", 1, 32)));
        }

        /// <summary>
        /// 解码公开启动配置，不从响应中改写 HTTP base URI。
        /// </summary>
        /// <param name="body">已通过字节上限检查的 UTF-8 JSON body。</param>
        /// <returns>Endpoint 与 limits 的不可变快照。</returns>
        /// <exception cref="ClientHttpContractException">JSON 或任一投影不符合 schema 时抛出。</exception>
        internal ClientBootstrapConfiguration DecodeBootstrapConfiguration(ReadOnlyMemory<byte> body)
        {
            return ReadObject(body, root =>
            {
                var endpoints = ReadEndpoints(root, "endpoints", maximumItems: 8);
                var limitsElement = ReadObjectProperty(root, "limits");
                var limits = new ClientPublicLimits(
                    ReadInt32(limitsElement, "httpBodyBytes", 1024, 1048576),
                    ReadInt32(limitsElement, "realtimeFrameBytes", 1024, 1048576));
                return new ClientBootstrapConfiguration(endpoints, limits);
            });
        }

        /// <summary>
        /// 解码 register/login 的原子账号、session、token 与 endpoint 响应。
        /// </summary>
        /// <param name="body">已通过字节上限检查的 UTF-8 JSON body。</param>
        /// <returns>完整认证结果；调用方只能在整个对象验证后提交状态。</returns>
        /// <exception cref="ClientHttpContractException">JSON 或任一嵌套投影无效时抛出。</exception>
        internal ClientAuthentication DecodeAuthentication(ReadOnlyMemory<byte> body)
        {
            return ReadObject(body, root =>
            {
                var accountElement = ReadObjectProperty(root, "account");
                var sessionElement = ReadObjectProperty(root, "session");
                var tokenElement = ReadObjectProperty(root, "tokens");
                var account = new ClientAccountSummary(
                    ReadString(accountElement, "accountId", 1, 64),
                    ReadString(accountElement, "displayName", 1, 32),
                    ReadInt64(accountElement, "createdAtMs", 0, long.MaxValue));
                var session = new ClientSessionSummary(
                    ReadString(sessionElement, "sessionId", 1, 128),
                    ReadInt64(sessionElement, "sessionEpoch", 1, long.MaxValue),
                    ReadInt64(sessionElement, "expiresAtMs", 0, long.MaxValue));
                var tokens = ReadTokenPair(tokenElement);
                var endpoints = ReadEndpoints(root, "endpoints", maximumItems: 8);
                return new ClientAuthentication(account, session, tokens, endpoints);
            });
        }

        /// <summary>
        /// 解码 refresh 成功返回的 token pair。
        /// </summary>
        /// <param name="body">已通过字节上限检查的 UTF-8 JSON body。</param>
        /// <returns>完整且不可变的新 token pair。</returns>
        /// <exception cref="ClientHttpContractException">JSON 或 token 字段无效时抛出。</exception>
        internal ClientTokenPair DecodeTokenPair(ReadOnlyMemory<byte> body)
        {
            return ReadObject(body, ReadTokenPair);
        }

        /// <summary>
        /// 解码 connection ticket，验证 endpoint channel、scope 唯一性和绝对 expiry。
        /// </summary>
        /// <param name="body">已通过字节上限检查的 UTF-8 JSON body。</param>
        /// <returns>包含 opaque credential 的短期 ticket 投影。</returns>
        /// <exception cref="ClientHttpContractException">JSON、scope 或 endpoint 无效时抛出。</exception>
        internal ClientConnectionTicket DecodeConnectionTicket(ReadOnlyMemory<byte> body)
        {
            return ReadObject(body, root =>
            {
                var ticket = ReadString(root, "ticket", 32, 2048);
                var endpoint = ReadEndpoint(ReadObjectProperty(root, "endpoint"), gameplayOnly: false);
                var scopesElement = ReadArrayProperty(root, "scopes", 1, 8);
                var scopes = new List<ClientConnectionScope>();
                var seen = new HashSet<ClientConnectionScope>();
                foreach (var item in scopesElement.EnumerateArray())
                {
                    if (item.ValueKind != JsonValueKind.String)
                    {
                        throw Contract("scopes 元素必须是字符串 enum。");
                    }

                    var scope = item.GetString() switch
                    {
                        "CONTROL" => ClientConnectionScope.Control,
                        "GAMEPLAY" => ClientConnectionScope.Gameplay,
                        _ => throw Contract("scopes 包含未知 enum。"),
                    };
                    if (!seen.Add(scope))
                    {
                        throw Contract("scopes 不得包含重复值。");
                    }

                    scopes.Add(scope);
                }

                return new ClientConnectionTicket(
                    ticket,
                    endpoint,
                    scopes,
                    ReadInt64(root, "expiresAtMs", 0, long.MaxValue));
            });
        }

        /// <summary>
        /// 解码 own-world bootstrap，但不保存或提升 world/assignment 为客户端最终事实。
        /// </summary>
        /// <param name="body">已通过字节上限检查的 UTF-8 JSON body。</param>
        /// <returns>本次响应的 world 与可选 assignment 安全投影。</returns>
        /// <exception cref="ClientHttpContractException">JSON 或 world/assignment 无效时抛出。</exception>
        internal ClientWorldBootstrap DecodeWorldBootstrap(ReadOnlyMemory<byte> body)
        {
            return ReadObject(body, root =>
            {
                var worldElement = ReadObjectProperty(root, "world");
                var world = new ClientPersonalWorldSummary(
                    ReadIdentity(worldElement, "personalWorldId"),
                    ReadIdentity(worldElement, "ownerPlayerId"),
                    ReadWorldLifecycle(worldElement),
                    ReadInt64(worldElement, "revision", 1, long.MaxValue),
                    ReadInt64(worldElement, "createdAtMs", 1, long.MaxValue));

                ClientWorldAssignment assignment = null;
                if (root.TryGetProperty("assignment", out var assignmentElement) &&
                    assignmentElement.ValueKind != JsonValueKind.Null)
                {
                    EnsureObject(assignmentElement, "assignment");
                    assignment = new ClientWorldAssignment(
                        ReadIdentity(assignmentElement, "personalWorldId"),
                        ReadIdentity(assignmentElement, "worldInstanceId"),
                        ReadEndpoint(ReadObjectProperty(assignmentElement, "endpoint"), gameplayOnly: true),
                        ReadInt64(assignmentElement, "generation", 1, long.MaxValue),
                        ReadInt64(assignmentElement, "leaseExpiresAtMs", 1, long.MaxValue));
                    if (!string.Equals(
                            assignment.PersonalWorldID,
                            world.PersonalWorldID,
                            StringComparison.Ordinal))
                    {
                        throw new ClientHttpContractException(
                            "World assignment 与 own-world identity 不一致。");
                    }
                }

                return new ClientWorldBootstrap(world, assignment);
            });
        }

        /// <summary>
        /// 解码 invite accept response，并绑定请求的 VisitSession 与 expected revision。
        /// </summary>
        /// <param name="body">已通过字节上限检查的 UTF-8 JSON body。</param>
        /// <param name="request">产生该响应的封闭 accept 输入。</param>
        /// <returns>只供后续 admission/join 使用的 reservation。</returns>
        /// <exception cref="ArgumentNullException">Request 为空时抛出。</exception>
        /// <exception cref="ArgumentException">Request identity 或 revision 无效时抛出。</exception>
        /// <exception cref="ClientHttpContractException">响应 identity、revision 或 expiry 无效时抛出。</exception>
        internal ClientVisitReservation DecodeVisitInviteAccept(
            ReadOnlyMemory<byte> body,
            ClientVisitInviteAcceptRequest request)
        {
            ValidateVisitInviteAcceptRequest(request);
            return ReadObject(body, root =>
            {
                var reservationElement = ReadObjectProperty(root, "reservation");
                var visitSessionID = ReadIdentity(reservationElement, "visitSessionId");
                if (!string.Equals(visitSessionID, request.VisitSessionID, StringComparison.Ordinal))
                {
                    throw Contract("Reservation 与 accept VisitSession identity 不一致。");
                }

                var revision = ReadInt64(reservationElement, "revision", 1, long.MaxValue);
                if (revision <= request.ExpectedRevision)
                {
                    throw Contract("Reservation revision 未推进 expected revision。");
                }

                return new ClientVisitReservation(
                    visitSessionID,
                    revision,
                    ReadInt64(reservationElement, "reservationExpiresAtMs", 1, long.MaxValue));
            });
        }

        /// <summary>
        /// 解码 world admission 并验证 opaque grammar、TLS/TCP endpoint 与封闭 role/purpose。
        /// </summary>
        /// <param name="body">已通过字节上限检查的 UTF-8 JSON body。</param>
        /// <returns>包含 opaque credential 的短期 admission 投影。</returns>
        /// <exception cref="ClientHttpContractException">响应不符合冻结 schema 时抛出。</exception>
        internal ClientWorldAdmission DecodeWorldAdmission(ReadOnlyMemory<byte> body)
        {
            return ReadObject(body, root =>
            {
                var credential = ReadString(root, "credential", 32, 4096);
                if (!Regex.IsMatch(credential, "^[A-Za-z0-9._~-]+$", RegexOptions.CultureInvariant))
                {
                    throw Contract("credential 不符合 opaque ASCII grammar。");
                }

                var role = ReadString(root, "role", 1, 16) switch
                {
                    "OWNER" => ClientWorldRole.Owner,
                    "VISITOR" => ClientWorldRole.Visitor,
                    _ => throw Contract("role 包含未知 enum。"),
                };
                var purpose = ReadString(root, "purpose", 1, 16) switch
                {
                    "OWN_WORLD" => ClientWorldAdmissionPurpose.OwnWorld,
                    "JOIN" => ClientWorldAdmissionPurpose.Join,
                    "RECONNECT" => ClientWorldAdmissionPurpose.Reconnect,
                    _ => throw Contract("purpose 包含未知 enum。"),
                };
                if ((role == ClientWorldRole.Owner) != (purpose == ClientWorldAdmissionPurpose.OwnWorld))
                {
                    throw Contract("World admission role 与 purpose 不一致。");
                }

                var visitRevision = checked((ulong)ReadInt64(root, "visitRevision", 0, long.MaxValue));
                if ((role == ClientWorldRole.Owner) != (visitRevision == 0))
                {
                    throw Contract("World admission role 与 visitRevision 不一致。");
                }

                return new ClientWorldAdmission(
                    credential,
                    ReadEndpoint(ReadObjectProperty(root, "endpoint"), gameplayOnly: true),
                    role,
                    purpose,
                    visitRevision,
                    ReadInt64(root, "expiresAtMs", 1, long.MaxValue));
            });
        }

        /// <summary>
        /// 解码公开 ErrorResponse 并交叉验证当前客户端认识的 registry 记录。
        /// </summary>
        /// <param name="body">已通过字节上限检查的 UTF-8 JSON error body。</param>
        /// <param name="statusCode">实际 HTTP status。</param>
        /// <returns>Known 或 unknown code 的安全服务端错误投影。</returns>
        /// <exception cref="ClientHttpContractException">ErrorResponse 结构或已知 registry 语义漂移时抛出。</exception>
        internal ClientServerError DecodeServerError(
            ReadOnlyMemory<byte> body,
            HttpStatusCode statusCode)
        {
            return ReadObject(body, root =>
            {
                var code = ReadInt32(root, "code", 1, int.MaxValue);
                var messageKey = ReadSafeAsciiToken(root, "messageKey", 1, 128);
                var requestID = ReadSafeAsciiToken(root, "requestId", 1, 64);
                var retryable = ReadBoolean(root, "retryable");
                TimeSpan? retryAfter = null;
                if (root.TryGetProperty("retryAfterMs", out var retryAfterElement))
                {
                    var milliseconds = ReadInt32Value(retryAfterElement, "retryAfterMs", 0, 60000);
                    retryAfter = TimeSpan.FromMilliseconds(milliseconds);
                }

                var details = new List<ClientErrorDetail>();
                if (root.TryGetProperty("details", out var detailsElement))
                {
                    EnsureArray(detailsElement, "details", 0, 16);
                    foreach (var item in detailsElement.EnumerateArray())
                    {
                        EnsureObject(item, "details item");
                        details.Add(new ClientErrorDetail(
                            ReadSafeAsciiToken(item, "field", 0, 64),
                            ReadSafeAsciiToken(item, "reason", 0, 64)));
                    }
                }

                var category = ClientServerErrorCategory.Unknown;
                if (ClientErrorRegistry.TryGet(code, out var knownError))
                {
                    if (knownError.StatusCode != statusCode ||
                        knownError.Retryable != retryable ||
                        !string.Equals(knownError.MessageKey, messageKey, StringComparison.Ordinal))
                    {
                        throw Contract("已知 ErrorResponse 与冻结 error registry 不一致。");
                    }

                    category = knownError.Category;
                }

                return new ClientServerError(
                    code,
                    category,
                    messageKey,
                    requestID,
                    retryable,
                    retryAfter,
                    details);
            });
        }

        /// <summary>
        /// 写入单一 JSON object 并返回精确 written bytes。
        /// </summary>
        /// <param name="writeProperties">只负责写属性、不得写第二个根对象的 callback。</param>
        /// <returns>单一 UTF-8 JSON object。</returns>
        private static byte[] WriteObject(Action<Utf8JsonWriter> writeProperties)
        {
            var buffer = new ArrayBufferWriter<byte>();
            using (var writer = new Utf8JsonWriter(buffer))
            {
                writer.WriteStartObject();
                writeProperties(writer);
                writer.WriteEndObject();
                writer.Flush();
            }

            return buffer.WrittenSpan.ToArray();
        }

        /// <summary>
        /// 解析单一 JSON object，并把 JsonException 转换为不含 body 的安全 contract exception。
        /// </summary>
        /// <typeparam name="T">不可变成功或错误投影。</typeparam>
        /// <param name="body">有界 UTF-8 JSON body。</param>
        /// <param name="project">只在 JsonDocument 有效期内读取 root 的投影函数。</param>
        /// <returns>不持有 JsonDocument buffer 的不可变投影。</returns>
        private static T ReadObject<T>(ReadOnlyMemory<byte> body, Func<JsonElement, T> project)
        {
            if (body.IsEmpty)
            {
                throw Contract("JSON response body 不能为空。");
            }

            try
            {
                using (var document = JsonDocument.Parse(body))
                {
                    var root = document.RootElement;
                    EnsureObject(root, "response root");
                    return project(root);
                }
            }
            catch (ClientHttpContractException)
            {
                throw;
            }
            catch (JsonException error)
            {
                throw new ClientHttpContractException("Response body 不是有效的单一 JSON object。", error);
            }
            catch (InvalidOperationException error)
            {
                throw new ClientHttpContractException("Response JSON field 类型不符合 contract。", error);
            }
            catch (OverflowException error)
            {
                throw new ClientHttpContractException("Response JSON number 超出 contract 范围。", error);
            }
        }

        /// <summary>
        /// 读取 Auth/refresh 共用 token pair，并验证长度与 Unix 毫秒范围。
        /// </summary>
        /// <param name="element">TokenPair JSON object。</param>
        /// <returns>不输出 token 的不可变投影。</returns>
        private static ClientTokenPair ReadTokenPair(JsonElement element)
        {
            EnsureObject(element, "tokens");
            return new ClientTokenPair(
                ReadString(element, "accessToken", 32, 2048),
                ReadString(element, "refreshToken", 32, 2048),
                ReadInt64(element, "accessExpiresAtMs", 0, long.MaxValue),
                ReadInt64(element, "refreshExpiresAtMs", 0, long.MaxValue));
        }

        /// <summary>
        /// 读取 endpoint 数组并保证集合与元素均受 OpenAPI 上限约束。
        /// </summary>
        /// <param name="parent">包含数组字段的 JSON object。</param>
        /// <param name="propertyName">Endpoint 数组字段名。</param>
        /// <param name="maximumItems">允许的最大元素数量。</param>
        /// <returns>顺序与 wire 一致的新集合。</returns>
        private static IReadOnlyList<ClientEndpoint> ReadEndpoints(
            JsonElement parent,
            string propertyName,
            int maximumItems)
        {
            var array = ReadArrayProperty(parent, propertyName, 0, maximumItems);
            var endpoints = new List<ClientEndpoint>();
            foreach (var item in array.EnumerateArray())
            {
                endpoints.Add(ReadEndpoint(item, gameplayOnly: false));
            }

            return endpoints;
        }

        /// <summary>
        /// 读取单个 endpoint 并按用途限制 channel。
        /// </summary>
        /// <param name="element">Endpoint JSON object。</param>
        /// <param name="gameplayOnly">是否只接受 TLS_TCP。</param>
        /// <returns>已验证 host、port 与 channel 的 endpoint。</returns>
        private static ClientEndpoint ReadEndpoint(JsonElement element, bool gameplayOnly)
        {
            EnsureObject(element, "endpoint");
            var channelText = ReadString(element, "channel", 1, 16);
            var channel = channelText switch
            {
                "WSS" when !gameplayOnly => ClientEndpointChannel.Wss,
                "TLS_TCP" => ClientEndpointChannel.TlsTcp,
                _ => throw Contract("endpoint.channel 包含不允许的 enum。"),
            };
            var host = ReadString(element, "host", 1, 253);
            if (Uri.CheckHostName(host) == UriHostNameType.Unknown)
            {
                throw Contract("endpoint.host 不是有效 DNS/IP host。");
            }

            return new ClientEndpoint(
                channel,
                host,
                ReadInt32(element, "port", 1, 65535));
        }

        /// <summary>
        /// 读取冻结 PersonalWorld lifecycle enum。
        /// </summary>
        /// <param name="element">包含 lifecycle 的 world object。</param>
        /// <returns>已知 lifecycle。</returns>
        private static ClientPersonalWorldLifecycle ReadWorldLifecycle(JsonElement element)
        {
            return ReadString(element, "lifecycle", 1, 16) switch
            {
                "ACTIVE" => ClientPersonalWorldLifecycle.Active,
                "ARCHIVED" => ClientPersonalWorldLifecycle.Archived,
                _ => throw Contract("world.lifecycle 包含未知 enum。"),
            };
        }

        /// <summary>
        /// 读取并验证公开 ASCII identity。
        /// </summary>
        /// <param name="element">包含 identity 的 JSON object。</param>
        /// <param name="propertyName">Identity 字段名。</param>
        /// <returns>长度与 grammar 均有效的 identity。</returns>
        private static string ReadIdentity(JsonElement element, string propertyName)
        {
            var value = ReadString(element, propertyName, 1, 128);
            if (!IdentityPattern.IsMatch(value))
            {
                throw Contract($"{propertyName} 不符合安全 ASCII identity grammar。");
            }

            return value;
        }

        /// <summary>
        /// 读取有界 ASCII token，拒绝空白、控制字符和日志分隔符。
        /// </summary>
        /// <param name="element">包含 token 的 JSON object。</param>
        /// <param name="propertyName">Token 字段名。</param>
        /// <param name="minimumLength">允许的最小长度。</param>
        /// <param name="maximumLength">允许的最大长度。</param>
        /// <returns>可安全用于 correlation 或稳定 key 的文本。</returns>
        private static string ReadSafeAsciiToken(
            JsonElement element,
            string propertyName,
            int minimumLength,
            int maximumLength)
        {
            var value = ReadString(element, propertyName, minimumLength, maximumLength);
            if (!SafeAsciiTokenPattern.IsMatch(value))
            {
                throw Contract($"{propertyName} 不符合安全 ASCII token grammar。");
            }

            return value;
        }

        /// <summary>
        /// 从 object 读取 required object property。
        /// </summary>
        /// <param name="parent">父 JSON object。</param>
        /// <param name="propertyName">Required property 名。</param>
        /// <returns>已验证为 object 的 property。</returns>
        private static JsonElement ReadObjectProperty(JsonElement parent, string propertyName)
        {
            var value = ReadRequiredProperty(parent, propertyName);
            EnsureObject(value, propertyName);
            return value;
        }

        /// <summary>
        /// 从 object 读取有界 required array property。
        /// </summary>
        /// <param name="parent">父 JSON object。</param>
        /// <param name="propertyName">Required property 名。</param>
        /// <param name="minimumItems">允许的最少元素数量。</param>
        /// <param name="maximumItems">允许的最多元素数量。</param>
        /// <returns>已验证长度的 array element。</returns>
        private static JsonElement ReadArrayProperty(
            JsonElement parent,
            string propertyName,
            int minimumItems,
            int maximumItems)
        {
            var value = ReadRequiredProperty(parent, propertyName);
            EnsureArray(value, propertyName, minimumItems, maximumItems);
            return value;
        }

        /// <summary>
        /// 从 object 读取 required string 并验证长度。
        /// </summary>
        /// <param name="parent">父 JSON object。</param>
        /// <param name="propertyName">Required property 名。</param>
        /// <param name="minimumLength">允许的最小 UTF-16 字符数。</param>
        /// <param name="maximumLength">允许的最大 UTF-16 字符数。</param>
        /// <returns>已验证的字符串。</returns>
        private static string ReadString(
            JsonElement parent,
            string propertyName,
            int minimumLength,
            int maximumLength)
        {
            var value = ReadRequiredProperty(parent, propertyName);
            if (value.ValueKind != JsonValueKind.String)
            {
                throw Contract($"{propertyName} 必须是字符串。");
            }

            var text = value.GetString();
            if (text == null || text.Length < minimumLength || text.Length > maximumLength)
            {
                throw Contract($"{propertyName} 长度不符合 contract。");
            }

            return text;
        }

        /// <summary>
        /// 从 object 读取 required Int32 并验证闭区间。
        /// </summary>
        /// <param name="parent">父 JSON object。</param>
        /// <param name="propertyName">Required property 名。</param>
        /// <param name="minimum">允许的最小值。</param>
        /// <param name="maximum">允许的最大值。</param>
        /// <returns>已验证整数。</returns>
        private static int ReadInt32(
            JsonElement parent,
            string propertyName,
            int minimum,
            int maximum)
        {
            return ReadInt32Value(ReadRequiredProperty(parent, propertyName), propertyName, minimum, maximum);
        }

        /// <summary>
        /// 读取独立 JSON number 为 Int32 并验证闭区间。
        /// </summary>
        /// <param name="value">待验证 JSON value。</param>
        /// <param name="propertyName">仅用于安全错误定位的字段名。</param>
        /// <param name="minimum">允许的最小值。</param>
        /// <param name="maximum">允许的最大值。</param>
        /// <returns>已验证整数。</returns>
        private static int ReadInt32Value(
            JsonElement value,
            string propertyName,
            int minimum,
            int maximum)
        {
            if (value.ValueKind != JsonValueKind.Number || !value.TryGetInt32(out var number))
            {
                throw Contract($"{propertyName} 必须是 Int32 整数。");
            }

            if (number < minimum || number > maximum)
            {
                throw Contract($"{propertyName} 超出 contract 范围。");
            }

            return number;
        }

        /// <summary>
        /// 从 object 读取 required Int64 并验证闭区间。
        /// </summary>
        /// <param name="parent">父 JSON object。</param>
        /// <param name="propertyName">Required property 名。</param>
        /// <param name="minimum">允许的最小值。</param>
        /// <param name="maximum">允许的最大值。</param>
        /// <returns>已验证整数。</returns>
        private static long ReadInt64(
            JsonElement parent,
            string propertyName,
            long minimum,
            long maximum)
        {
            var value = ReadRequiredProperty(parent, propertyName);
            if (value.ValueKind != JsonValueKind.Number || !value.TryGetInt64(out var number))
            {
                throw Contract($"{propertyName} 必须是 Int64 整数。");
            }

            if (number < minimum || number > maximum)
            {
                throw Contract($"{propertyName} 超出 contract 范围。");
            }

            return number;
        }

        /// <summary>
        /// 从 object 读取 required Boolean。
        /// </summary>
        /// <param name="parent">父 JSON object。</param>
        /// <param name="propertyName">Required property 名。</param>
        /// <returns>Wire boolean 值。</returns>
        private static bool ReadBoolean(JsonElement parent, string propertyName)
        {
            var value = ReadRequiredProperty(parent, propertyName);
            if (value.ValueKind != JsonValueKind.True && value.ValueKind != JsonValueKind.False)
            {
                throw Contract($"{propertyName} 必须是 boolean。");
            }

            return value.GetBoolean();
        }

        /// <summary>
        /// 读取 required property，并拒绝缺失或显式 null。
        /// </summary>
        /// <param name="parent">已验证的父 object。</param>
        /// <param name="propertyName">Required property 名。</param>
        /// <returns>非 null JSON property。</returns>
        private static JsonElement ReadRequiredProperty(JsonElement parent, string propertyName)
        {
            EnsureObject(parent, "parent");
            if (!parent.TryGetProperty(propertyName, out var value) || value.ValueKind == JsonValueKind.Null)
            {
                throw Contract($"缺少 required field {propertyName}。");
            }

            return value;
        }

        /// <summary>
        /// 验证 JSON value 是 object。
        /// </summary>
        /// <param name="value">待验证 JSON value。</param>
        /// <param name="fieldName">用于安全错误定位的字段名。</param>
        private static void EnsureObject(JsonElement value, string fieldName)
        {
            if (value.ValueKind != JsonValueKind.Object)
            {
                throw Contract($"{fieldName} 必须是 JSON object。");
            }

            var propertyNames = new HashSet<string>(StringComparer.Ordinal);
            foreach (var property in value.EnumerateObject())
            {
                if (!propertyNames.Add(property.Name))
                {
                    throw Contract($"{fieldName} 不得包含重复 property。");
                }
            }
        }

        /// <summary>
        /// 验证 JSON value 是长度有界的 array。
        /// </summary>
        /// <param name="value">待验证 JSON value。</param>
        /// <param name="fieldName">用于安全错误定位的字段名。</param>
        /// <param name="minimumItems">最少元素数量。</param>
        /// <param name="maximumItems">最多元素数量。</param>
        private static void EnsureArray(
            JsonElement value,
            string fieldName,
            int minimumItems,
            int maximumItems)
        {
            if (value.ValueKind != JsonValueKind.Array)
            {
                throw Contract($"{fieldName} 必须是 JSON array。");
            }

            var length = value.GetArrayLength();
            if (length < minimumItems || length > maximumItems)
            {
                throw Contract($"{fieldName} 元素数量不符合 contract。");
            }
        }

        /// <summary>
        /// 验证客户端 username 与冻结 ASCII grammar 一致。
        /// </summary>
        /// <param name="username">待发送 username。</param>
        /// <exception cref="ArgumentException">Username 长度或 grammar 无效时抛出。</exception>
        private static void ValidateUsername(string username)
        {
            if (username == null || username.Length < 3 || username.Length > 64 || !UsernamePattern.IsMatch(username))
            {
                throw new ArgumentException("Username 不符合公开 contract。", nameof(username));
            }
        }

        /// <summary>
        /// 验证待发送公开 identity 的长度与安全 ASCII grammar。
        /// </summary>
        /// <param name="value">待验证 identity。</param>
        /// <param name="parameterName">ArgumentException 使用的参数名。</param>
        /// <exception cref="ArgumentException">Identity 无效时抛出。</exception>
        private static void ValidateIdentity(string value, string parameterName)
        {
            if (value == null || value.Length < 1 || value.Length > 128 || !IdentityPattern.IsMatch(value))
            {
                throw new ArgumentException("Identity 不符合公开 contract。", parameterName);
            }
        }

        /// <summary>
        /// 验证 accept path identity 与正 expected revision，阻止创建部分请求。
        /// </summary>
        /// <param name="request">待编码的 accept 输入。</param>
        /// <exception cref="ArgumentNullException">Request 为空时抛出。</exception>
        /// <exception cref="ArgumentException">任一输入无效时抛出。</exception>
        private static void ValidateVisitInviteAcceptRequest(ClientVisitInviteAcceptRequest request)
        {
            if (request == null)
            {
                throw new ArgumentNullException(nameof(request));
            }

            ValidateIdentity(request.VisitSessionID, nameof(request.VisitSessionID));
            ValidateIdentity(request.InviteID, nameof(request.InviteID));
            if (request.ExpectedRevision <= 0)
            {
                throw new ArgumentException("Expected revision 必须为正数。", nameof(request.ExpectedRevision));
            }
        }

        /// <summary>
        /// 验证 password 字符与 UTF-8 字节上限，但不 trim、normalize 或记录内容。
        /// </summary>
        /// <param name="password">待发送原始 password。</param>
        /// <param name="minimumCharacters">当前 operation 允许的最少字符数。</param>
        /// <exception cref="ArgumentException">Password 长度或字节数无效时抛出。</exception>
        private static void ValidatePassword(string password, int minimumCharacters)
        {
            if (password == null ||
                password.Length < minimumCharacters ||
                password.Length > 128 ||
                Encoding.UTF8.GetByteCount(password) > 128)
            {
                throw new ArgumentException("Password 不符合公开 contract。", nameof(password));
            }
        }

        /// <summary>
        /// 验证普通客户端文本的非空与长度边界。
        /// </summary>
        /// <param name="value">待验证文本。</param>
        /// <param name="minimumLength">最少 UTF-16 字符数。</param>
        /// <param name="maximumLength">最多 UTF-16 字符数。</param>
        /// <param name="parameterName">用于 ArgumentException 的安全参数名。</param>
        /// <exception cref="ArgumentException">文本为空或长度越界时抛出。</exception>
        private static void ValidateText(
            string value,
            int minimumLength,
            int maximumLength,
            string parameterName)
        {
            if (value == null || value.Length < minimumLength || value.Length > maximumLength)
            {
                throw new ArgumentException("文本长度不符合公开 contract。", parameterName);
            }
        }

        /// <summary>
        /// 创建不包含远端原始值的 contract exception。
        /// </summary>
        /// <param name="message">字段与约束说明。</param>
        /// <returns>可在 HTTP 边界统一捕获的异常。</returns>
        private static ClientHttpContractException Contract(string message)
        {
            return new ClientHttpContractException(message);
        }
    }
}
