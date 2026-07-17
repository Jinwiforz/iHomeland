using System;
using System.Collections.Generic;
using Google.Protobuf;
using IHomeland.Protocol.Common.V1;
using IHomeland.Protocol.Visit.V1;
using IHomeland.Protocol.World.V1;

namespace IHomeland.Client.Infrastructure.Tcp
{
    /// <summary>
    /// 描述一个冻结 C2S request/command 与唯一 S2C response 的强类型配对。
    /// </summary>
    /// <typeparam name="TRequest">生成的 request/command payload 类型。</typeparam>
    /// <typeparam name="TResponse">生成的 response payload 类型。</typeparam>
    internal sealed class ClientGameplayOperation<TRequest, TResponse>
        where TRequest : class, IMessage<TRequest>
        where TResponse : class, IMessage<TResponse>
    {
        /// <summary>创建 route registry 的不可变强类型投影。</summary>
        /// <param name="requestMessageID">C2S message ID。</param>
        /// <param name="responseMessageID">S2C response/error message ID。</param>
        /// <param name="requestKind">Request 或 Command。</param>
        /// <param name="requestMaximumBytes">C2S 完整 envelope 上限。</param>
        /// <param name="responseMaximumBytes">S2C 完整 envelope 上限。</param>
        /// <param name="timeout">Pending operation deadline。</param>
        /// <param name="responseParser">生成 response parser。</param>
        internal ClientGameplayOperation(
            uint requestMessageID,
            uint responseMessageID,
            MessageKind requestKind,
            int requestMaximumBytes,
            int responseMaximumBytes,
            TimeSpan timeout,
            MessageParser<TResponse> responseParser)
        {
            if (requestMessageID == 0 || responseMessageID == 0 ||
                (requestKind != MessageKind.Request && requestKind != MessageKind.Command) ||
                requestMaximumBytes <= 0 || responseMaximumBytes <= 0 || timeout <= TimeSpan.Zero)
            {
                throw new ArgumentException("Gameplay operation descriptor 无效。");
            }

            RequestMessageID = requestMessageID;
            ResponseMessageID = responseMessageID;
            RequestKind = requestKind;
            RequestMaximumBytes = requestMaximumBytes;
            ResponseMaximumBytes = responseMaximumBytes;
            Timeout = timeout;
            ResponseParser = responseParser ?? throw new ArgumentNullException(nameof(responseParser));
        }

        /// <summary>获取冻结 C2S message ID。</summary>
        internal uint RequestMessageID { get; }

        /// <summary>获取唯一匹配的 S2C response/error message ID。</summary>
        internal uint ResponseMessageID { get; }

        /// <summary>获取 Request 或 Command correlation 语义。</summary>
        internal MessageKind RequestKind { get; }

        /// <summary>获取 C2S 完整 envelope 上限，单位为字节。</summary>
        internal int RequestMaximumBytes { get; }

        /// <summary>获取 S2C 完整 envelope 上限，单位为字节。</summary>
        internal int ResponseMaximumBytes { get; }

        /// <summary>获取 pending deadline。</summary>
        internal TimeSpan Timeout { get; }

        /// <summary>获取只接受固定 generated type 的 parser。</summary>
        internal MessageParser<TResponse> ResponseParser { get; }
    }

    /// <summary>
    /// 描述一个服务端可信 S2C PUSH route。
    /// </summary>
    /// <typeparam name="TPush">生成的 push payload 类型。</typeparam>
    internal sealed class ClientGameplayPush<TPush>
        where TPush : class, IMessage<TPush>
    {
        /// <summary>创建冻结 push descriptor。</summary>
        /// <param name="messageID">S2C PUSH message ID。</param>
        /// <param name="maximumBytes">完整 envelope 上限。</param>
        /// <param name="parser">生成 payload parser。</param>
        internal ClientGameplayPush(uint messageID, int maximumBytes, MessageParser<TPush> parser)
        {
            if (messageID == 0 || maximumBytes <= 0)
            {
                throw new ArgumentException("Gameplay push descriptor 无效。");
            }

            MessageID = messageID;
            MaximumBytes = maximumBytes;
            Parser = parser ?? throw new ArgumentNullException(nameof(parser));
        }

        /// <summary>获取冻结 S2C message ID。</summary>
        internal uint MessageID { get; }

        /// <summary>获取完整 envelope 上限，单位为字节。</summary>
        internal int MaximumBytes { get; }

        /// <summary>获取固定 generated payload parser。</summary>
        internal MessageParser<TPush> Parser { get; }
    }

    /// <summary>
    /// 集中拥有服务端 v1 已登记的全部 gameplay route descriptor。
    /// </summary>
    internal static class ClientGameplayCatalog
    {
        /// <summary>查询当前 world snapshot。</summary>
        internal static readonly ClientGameplayOperation<WorldSnapshotRequest, WorldSnapshotResponse> WorldSnapshot =
            Request<WorldSnapshotRequest, WorldSnapshotResponse>(2000, 2001, 4096, 65536, 10000, WorldSnapshotResponse.Parser);

        /// <summary>开启或解析 active VisitSession。</summary>
        internal static readonly ClientGameplayOperation<VisitOpenCommand, VisitOpenResponse> VisitOpen =
            Command<VisitOpenCommand, VisitOpenResponse>(2103, 2104, VisitOpenResponse.Parser);

        /// <summary>创建定向 visit invite。</summary>
        internal static readonly ClientGameplayOperation<VisitCreateInviteCommand, VisitCreateInviteResponse> VisitCreateInvite =
            Command<VisitCreateInviteCommand, VisitCreateInviteResponse>(2105, 2106, VisitCreateInviteResponse.Parser);

        /// <summary>撤销 pending invite。</summary>
        internal static readonly ClientGameplayOperation<VisitRevokeInviteCommand, VisitRevokeInviteResponse> VisitRevokeInvite =
            Command<VisitRevokeInviteCommand, VisitRevokeInviteResponse>(2107, 2108, VisitRevokeInviteResponse.Parser);

        /// <summary>使用 JOIN admission 完成 membership。</summary>
        internal static readonly ClientGameplayOperation<VisitJoinCommand, VisitJoinResponse> VisitJoin =
            Command<VisitJoinCommand, VisitJoinResponse>(2109, 2110, VisitJoinResponse.Parser);

        /// <summary>当前 Visitor 主动离开。</summary>
        internal static readonly ClientGameplayOperation<VisitLeaveCommand, VisitLeaveResponse> VisitLeave =
            Command<VisitLeaveCommand, VisitLeaveResponse>(2111, 2112, VisitLeaveResponse.Parser);

        /// <summary>Owner 移除指定 Visitor。</summary>
        internal static readonly ClientGameplayOperation<VisitKickCommand, VisitKickResponse> VisitKick =
            Command<VisitKickCommand, VisitKickResponse>(2113, 2114, VisitKickResponse.Parser);

        /// <summary>使用 RECONNECT admission 恢复 membership。</summary>
        internal static readonly ClientGameplayOperation<VisitReconnectCommand, VisitReconnectResponse> VisitReconnect =
            Command<VisitReconnectCommand, VisitReconnectResponse>(2115, 2116, VisitReconnectResponse.Parser);

        /// <summary>Owner 关闭 VisitSession。</summary>
        internal static readonly ClientGameplayOperation<VisitCloseCommand, VisitCloseResponse> VisitClose =
            Command<VisitCloseCommand, VisitCloseResponse>(2117, 2118, VisitCloseResponse.Parser);

        /// <summary>查询当前 VisitSession snapshot。</summary>
        internal static readonly ClientGameplayOperation<VisitSnapshotRequest, VisitSnapshotResponse> VisitSnapshot =
            Request<VisitSnapshotRequest, VisitSnapshotResponse>(2119, 2120, 4096, 65536, 10000, VisitSnapshotResponse.Parser);

        /// <summary>PersonalWorld 完整替换 push。</summary>
        internal static readonly ClientGameplayPush<WorldSnapshotPush> WorldSnapshotPushRoute =
            new ClientGameplayPush<WorldSnapshotPush>(2002, 65536, WorldSnapshotPush.Parser);

        /// <summary>VisitSession 完整替换 push。</summary>
        internal static readonly ClientGameplayPush<VisitSnapshotPush> VisitSnapshotPushRoute =
            new ClientGameplayPush<VisitSnapshotPush>(2121, 65536, VisitSnapshotPush.Parser);

        /// <summary>Visitor 权威安全返回 push。</summary>
        internal static readonly ClientGameplayPush<VisitSafeReturnPush> VisitSafeReturnPushRoute =
            new ClientGameplayPush<VisitSafeReturnPush>(2122, 16384, VisitSafeReturnPush.Parser);

        /// <summary>保存 response ID 到完整 envelope 上限的只读索引。</summary>
        private static readonly IReadOnlyDictionary<uint, int> ResponseMaximums =
            BuildResponseMaximums();

        /// <summary>尝试查询 response route 的 envelope 上限。</summary>
        /// <param name="messageID">S2C response ID。</param>
        /// <param name="maximumBytes">成功时返回 route 上限。</param>
        /// <returns>Message ID 已登记为 response 时返回 true。</returns>
        internal static bool TryGetResponseMaximum(uint messageID, out int maximumBytes)
        {
            return ResponseMaximums.TryGetValue(messageID, out maximumBytes);
        }

        /// <summary>尝试查询三个登记 PUSH route 的完整 envelope 上限。</summary>
        /// <param name="messageID">S2C PUSH ID。</param>
        /// <param name="maximumBytes">命中时返回 descriptor 中的 route 上限。</param>
        /// <returns>Message ID 已登记为可信 PUSH 时返回 true。</returns>
        internal static bool TryGetPushMaximum(uint messageID, out int maximumBytes)
        {
            if (messageID == WorldSnapshotPushRoute.MessageID)
            {
                maximumBytes = WorldSnapshotPushRoute.MaximumBytes;
                return true;
            }

            if (messageID == VisitSnapshotPushRoute.MessageID)
            {
                maximumBytes = VisitSnapshotPushRoute.MaximumBytes;
                return true;
            }

            if (messageID == VisitSafeReturnPushRoute.MessageID)
            {
                maximumBytes = VisitSafeReturnPushRoute.MaximumBytes;
                return true;
            }

            maximumBytes = 0;
            return false;
        }

        /// <summary>从唯一 operation descriptor 构建 response budget 索引，避免重复维护 route 数值。</summary>
        /// <returns>包含全部登记 response 的只读使用索引。</returns>
        private static IReadOnlyDictionary<uint, int> BuildResponseMaximums()
        {
            var maximums = new Dictionary<uint, int>();
            AddResponse(maximums, WorldSnapshot);
            AddResponse(maximums, VisitOpen);
            AddResponse(maximums, VisitCreateInvite);
            AddResponse(maximums, VisitRevokeInvite);
            AddResponse(maximums, VisitJoin);
            AddResponse(maximums, VisitLeave);
            AddResponse(maximums, VisitKick);
            AddResponse(maximums, VisitReconnect);
            AddResponse(maximums, VisitClose);
            AddResponse(maximums, VisitSnapshot);
            return maximums;
        }

        /// <summary>把一个强类型 operation 的唯一 response budget 加入索引并拒绝重复 ID。</summary>
        /// <typeparam name="TRequest">Descriptor 的生成 request/command 类型。</typeparam>
        /// <typeparam name="TResponse">Descriptor 的生成 response 类型。</typeparam>
        /// <param name="maximums">正在构建的唯一 response ID 索引。</param>
        /// <param name="operation">唯一拥有 route 数值与 budget 的 descriptor。</param>
        private static void AddResponse<TRequest, TResponse>(
            IDictionary<uint, int> maximums,
            ClientGameplayOperation<TRequest, TResponse> operation)
            where TRequest : class, IMessage<TRequest>
            where TResponse : class, IMessage<TResponse>
        {
            maximums.Add(operation.ResponseMessageID, operation.ResponseMaximumBytes);
        }

        /// <summary>创建 Request descriptor 并集中转换毫秒单位。</summary>
        /// <typeparam name="TRequest">生成 request 类型。</typeparam>
        /// <typeparam name="TResponse">生成 response 类型。</typeparam>
        /// <param name="requestID">C2S request ID。</param>
        /// <param name="responseID">唯一 S2C response/error ID。</param>
        /// <param name="requestMaximum">C2S 完整 envelope 上限，单位为字节。</param>
        /// <param name="responseMaximum">S2C 完整 envelope 上限，单位为字节。</param>
        /// <param name="timeoutMilliseconds">Pending deadline，单位为毫秒。</param>
        /// <param name="parser">生成 response parser。</param>
        /// <returns>不可变 Request descriptor。</returns>
        private static ClientGameplayOperation<TRequest, TResponse> Request<TRequest, TResponse>(
            uint requestID,
            uint responseID,
            int requestMaximum,
            int responseMaximum,
            int timeoutMilliseconds,
            MessageParser<TResponse> parser)
            where TRequest : class, IMessage<TRequest>
            where TResponse : class, IMessage<TResponse>
        {
            return new ClientGameplayOperation<TRequest, TResponse>(
                requestID,
                responseID,
                MessageKind.Request,
                requestMaximum,
                responseMaximum,
                TimeSpan.FromMilliseconds(timeoutMilliseconds),
                parser);
        }

        /// <summary>创建统一预算的 Visit command descriptor。</summary>
        /// <typeparam name="TRequest">生成 command 类型。</typeparam>
        /// <typeparam name="TResponse">生成 response 类型。</typeparam>
        /// <param name="requestID">C2S command ID。</param>
        /// <param name="responseID">唯一 S2C response/error ID。</param>
        /// <param name="parser">生成 response parser。</param>
        /// <returns>使用统一预算与 deadline 的不可变 Command descriptor。</returns>
        private static ClientGameplayOperation<TRequest, TResponse> Command<TRequest, TResponse>(
            uint requestID,
            uint responseID,
            MessageParser<TResponse> parser)
            where TRequest : class, IMessage<TRequest>
            where TResponse : class, IMessage<TResponse>
        {
            return new ClientGameplayOperation<TRequest, TResponse>(
                requestID,
                responseID,
                MessageKind.Command,
                4096,
                16384,
                TimeSpan.FromMilliseconds(5000),
                parser);
        }
    }
}
