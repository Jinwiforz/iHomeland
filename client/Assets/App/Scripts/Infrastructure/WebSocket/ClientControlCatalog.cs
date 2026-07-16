using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using IHomeland.Protocol.Control.V1;
using IHomeland.Protocol.Visit.V1;
using IHomeland.Protocol.World.V1;

namespace IHomeland.Client.Infrastructure.WebSocket
{
    /// <summary>
    /// 集中保存冻结为 WSS/CONTROL/SERVER_TO_CLIENT/PUSH 的 9 条 route。
    /// </summary>
    internal sealed class ClientControlCatalog
    {
        /// <summary>
        /// 保存按 message ID 索引的封闭 route 表。
        /// </summary>
        private readonly IReadOnlyDictionary<uint, ClientControlRoute> _routes;

        /// <summary>
        /// 创建并验证固定 route 表不存在重复 ID。
        /// </summary>
        internal ClientControlCatalog()
        {
            var routes = new Dictionary<uint, ClientControlRoute>();
            Add(routes, Route(500, "CONTROL_MAINTENANCE_PUSH", MaintenancePush.Descriptor.FullName, 4096, MaintenancePush.Parser.ParseFrom));
            Add(routes, Route(501, "CONTROL_FORCED_LOGOUT_PUSH", ForcedLogoutPush.Descriptor.FullName, 2048, ForcedLogoutPush.Parser.ParseFrom));
            Add(routes, Route(502, "CONTROL_QUEUE_STATUS_PUSH", QueueStatusPush.Descriptor.FullName, 1024, QueueStatusPush.Parser.ParseFrom));
            Add(routes, Route(503, "CONTROL_ENDPOINT_UPDATE_PUSH", EndpointUpdatePush.Descriptor.FullName, 8192, EndpointUpdatePush.Parser.ParseFrom));
            Add(routes, Route(504, "CONTROL_SESSION_INVALIDATED_PUSH", SessionInvalidatedPush.Descriptor.FullName, 2048, SessionInvalidatedPush.Parser.ParseFrom));
            Add(routes, Route(2003, "WORLD_ASSIGNMENT_CHANGED_PUSH", WorldAssignmentChangedPush.Descriptor.FullName, 16384, WorldAssignmentChangedPush.Parser.ParseFrom));
            Add(routes, Route(2100, "VISIT_INVITE_PUSH", VisitInvitePush.Descriptor.FullName, 16384, VisitInvitePush.Parser.ParseFrom));
            Add(routes, Route(2101, "VISIT_OWNER_AVAILABILITY_PUSH", VisitOwnerAvailabilityPush.Descriptor.FullName, 16384, VisitOwnerAvailabilityPush.Parser.ParseFrom));
            Add(routes, Route(2102, "VISIT_CLOSED_NOTICE_PUSH", VisitClosedNoticePush.Descriptor.FullName, 16384, VisitClosedNoticePush.Parser.ParseFrom));
            _routes = new ReadOnlyDictionary<uint, ClientControlRoute>(routes);
        }

        /// <summary>
        /// 获取只读 route 字典，供 codec 与 contract parity 测试使用。
        /// </summary>
        internal IReadOnlyDictionary<uint, ClientControlRoute> Routes => _routes;

        /// <summary>
        /// 尝试按 message ID 取得唯一 WSS control route。
        /// </summary>
        /// <param name="messageID">Envelope 声明的 message ID。</param>
        /// <param name="route">成功时返回冻结 route。</param>
        /// <returns>Message ID 已登记时返回 true。</returns>
        internal bool TryGet(uint messageID, out ClientControlRoute route)
        {
            return _routes.TryGetValue(messageID, out route);
        }

        /// <summary>
        /// 创建 route 并适配 generated parser delegate。
        /// </summary>
        /// <typeparam name="TPayload">精确 generated payload 类型。</typeparam>
        /// <param name="messageID">稳定 message ID。</param>
        /// <param name="name">稳定 registry 名称。</param>
        /// <param name="protobufName">Generated descriptor 全名。</param>
        /// <param name="maximumFrameBytes">完整 envelope route 上限，单位为字节。</param>
        /// <param name="parser">精确 generated parser。</param>
        /// <returns>封闭 route。</returns>
        private static ClientControlRoute Route<TPayload>(
            uint messageID,
            string name,
            string protobufName,
            int maximumFrameBytes,
            Func<Google.Protobuf.ByteString, TPayload> parser)
            where TPayload : Google.Protobuf.IMessage
        {
            return new ClientControlRoute(
                messageID,
                name,
                protobufName,
                maximumFrameBytes,
                payload => parser(payload));
        }

        /// <summary>
        /// 将 route 加入字典并对重复 ID fail fast。
        /// </summary>
        /// <param name="routes">正在构造的私有字典。</param>
        /// <param name="route">待加入的非空 route。</param>
        private static void Add(IDictionary<uint, ClientControlRoute> routes, ClientControlRoute route)
        {
            if (routes.ContainsKey(route.MessageID))
            {
                throw new InvalidOperationException($"Control route {route.MessageID} 重复。");
            }

            routes.Add(route.MessageID, route);
        }
    }
}
