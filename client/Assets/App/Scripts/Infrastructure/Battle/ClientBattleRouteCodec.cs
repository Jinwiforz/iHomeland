using System;
using System.Collections.Generic;
using IHomeland.Client.Application.Battle;

namespace IHomeland.Client.Infrastructure.Battle
{
    /// <summary>
    /// 标识 battle message 唯一允许的 secure lane。
    /// </summary>
    internal enum ClientBattleRouteLane
    {
        /// <summary>Unreliable sequenced raw lane。</summary>
        Raw = 1,

        /// <summary>Reliable ordered expiring KCP lane。</summary>
        Kcp = 2,
    }

    /// <summary>
    /// 标识 battle message 唯一允许的 wire direction。
    /// </summary>
    internal enum ClientBattleRouteDirection
    {
        /// <summary>客户端到服务端。</summary>
        ClientToServer = 1,

        /// <summary>服务端到客户端。</summary>
        ServerToClient = 2,
    }

    /// <summary>
    /// 保存 route registry 3000..3007 的 immutable closed projection。
    /// </summary>
    internal sealed class ClientBattleRoute
    {
        /// <summary>
        /// 创建单一登记 route。
        /// </summary>
        /// <param name="messageID">全局唯一 message ID。</param>
        /// <param name="lane">唯一允许 lane。</param>
        /// <param name="direction">唯一允许 direction。</param>
        /// <param name="maximumPayloadBytes">Protobuf payload ceiling。</param>
        /// <param name="maximumRatePerSecond">Session 内 rate ceiling。</param>
        /// <param name="expiryMilliseconds">Application value expiry。</param>
        internal ClientBattleRoute(
            uint messageID,
            ClientBattleRouteLane lane,
            ClientBattleRouteDirection direction,
            int maximumPayloadBytes,
            int maximumRatePerSecond,
            int expiryMilliseconds)
        {
            if (messageID < 3000 ||
                messageID > 3007 ||
                maximumPayloadBytes <= 0 ||
                maximumRatePerSecond <= 0 ||
                expiryMilliseconds <= 0)
            {
                throw new ArgumentException(
                    "client battle route is invalid");
            }

            MessageID = messageID;
            Lane = lane;
            Direction = direction;
            MaximumPayloadBytes = maximumPayloadBytes;
            MaximumRatePerSecond = maximumRatePerSecond;
            ExpiryMilliseconds = expiryMilliseconds;
        }

        /// <summary>获取 global message ID。</summary>
        internal uint MessageID { get; }

        /// <summary>获取唯一 secure lane。</summary>
        internal ClientBattleRouteLane Lane { get; }

        /// <summary>获取唯一 wire direction。</summary>
        internal ClientBattleRouteDirection Direction { get; }

        /// <summary>获取 Protobuf payload ceiling。</summary>
        internal int MaximumPayloadBytes { get; }

        /// <summary>获取 session 内每秒 rate ceiling。</summary>
        internal int MaximumRatePerSecond { get; }

        /// <summary>获取 application value expiry。</summary>
        internal int ExpiryMilliseconds { get; }
    }

    /// <summary>
    /// 提供与 routes.json 精确一致的 3000..3007 closed catalog。
    /// </summary>
    internal static class ClientBattleRouteCatalog
    {
        /// <summary>保存 raw input route。</summary>
        internal static readonly ClientBattleRoute Input =
            new ClientBattleRoute(
                3000,
                ClientBattleRouteLane.Raw,
                ClientBattleRouteDirection.ClientToServer,
                384,
                40,
                300);

        /// <summary>保存 raw probe route。</summary>
        internal static readonly ClientBattleRoute Probe =
            new ClientBattleRoute(
                3001,
                ClientBattleRouteLane.Raw,
                ClientBattleRouteDirection.ClientToServer,
                96,
                4,
                250);

        /// <summary>保存 raw full snapshot route。</summary>
        internal static readonly ClientBattleRoute FullSnapshot =
            new ClientBattleRoute(
                3002,
                ClientBattleRouteLane.Raw,
                ClientBattleRouteDirection.ServerToClient,
                1040,
                2,
                500);

        /// <summary>保存 raw delta snapshot route。</summary>
        internal static readonly ClientBattleRoute DeltaSnapshot =
            new ClientBattleRoute(
                3003,
                ClientBattleRouteLane.Raw,
                ClientBattleRouteDirection.ServerToClient,
                900,
                10,
                300);

        /// <summary>保存 reliable ability event route。</summary>
        internal static readonly ClientBattleRoute AbilityEvent =
            new ClientBattleRoute(
                3004,
                ClientBattleRouteLane.Kcp,
                ClientBattleRouteDirection.ServerToClient,
                512,
                20,
                500);

        /// <summary>保存 reliable entity lifecycle route。</summary>
        internal static readonly ClientBattleRoute EntityLifecycle =
            new ClientBattleRoute(
                3005,
                ClientBattleRouteLane.Kcp,
                ClientBattleRouteDirection.ServerToClient,
                512,
                20,
                500);

        /// <summary>保存 reliable resync request route。</summary>
        internal static readonly ClientBattleRoute ResyncRequest =
            new ClientBattleRoute(
                3006,
                ClientBattleRouteLane.Kcp,
                ClientBattleRouteDirection.ClientToServer,
                128,
                2,
                2250);

        /// <summary>保存 reliable resync response route。</summary>
        internal static readonly ClientBattleRoute ResyncResponse =
            new ClientBattleRoute(
                3007,
                ClientBattleRouteLane.Kcp,
                ClientBattleRouteDirection.ServerToClient,
                768,
                2,
                2250);

        /// <summary>保存按 message ID 索引的 closed table。</summary>
        private static readonly IReadOnlyDictionary<uint, ClientBattleRoute>
            Routes =
                new Dictionary<uint, ClientBattleRoute>
                {
                    { Input.MessageID, Input },
                    { Probe.MessageID, Probe },
                    { FullSnapshot.MessageID, FullSnapshot },
                    { DeltaSnapshot.MessageID, DeltaSnapshot },
                    { AbilityEvent.MessageID, AbilityEvent },
                    { EntityLifecycle.MessageID, EntityLifecycle },
                    { ResyncRequest.MessageID, ResyncRequest },
                    { ResyncResponse.MessageID, ResyncResponse },
                };

        /// <summary>
        /// 查找 closed route，不允许 caller 构造任意 message ID。
        /// </summary>
        /// <param name="messageID">待查找 message ID。</param>
        /// <param name="route">成功时返回唯一登记项。</param>
        /// <returns>Message ID 已登记时为 true。</returns>
        internal static bool TryGet(
            uint messageID,
            out ClientBattleRoute route)
        {
            return Routes.TryGetValue(messageID, out route);
        }
    }

    /// <summary>
    /// 保存完成 header 校验但尚未解析 Protobuf 的 caller-owned route payload。
    /// </summary>
    internal sealed class ClientBattleRouteFrame
    {
        /// <summary>
        /// 创建已通过 closed envelope 校验的 frame。
        /// </summary>
        /// <param name="route">Closed catalog route。</param>
        /// <param name="applicationSequence">Session 内 nonzero sequence。</param>
        /// <param name="partitionIndex">Raw snapshot partition index。</param>
        /// <param name="partitionCount">Raw snapshot partition count。</param>
        /// <param name="payload">Caller-owned Protobuf bytes。</param>
        internal ClientBattleRouteFrame(
            ClientBattleRoute route,
            ulong applicationSequence,
            byte partitionIndex,
            byte partitionCount,
            byte[] payload)
        {
            Route = route ?? throw new ArgumentNullException(nameof(route));
            ApplicationSequence = applicationSequence;
            PartitionIndex = partitionIndex;
            PartitionCount = partitionCount;
            Payload = payload ?? throw new ArgumentNullException(nameof(payload));
        }

        /// <summary>获取 closed route。</summary>
        internal ClientBattleRoute Route { get; }

        /// <summary>获取 session 内 application sequence。</summary>
        internal ulong ApplicationSequence { get; }

        /// <summary>获取 raw partition index；KCP 固定为零。</summary>
        internal byte PartitionIndex { get; }

        /// <summary>获取 raw partition count；KCP 固定为一。</summary>
        internal byte PartitionCount { get; }

        /// <summary>获取 caller-owned Protobuf payload。</summary>
        internal byte[] Payload { get; }
    }

    /// <summary>
    /// 编解码 canonical 16-byte raw/KCP route envelope，并在 Protobuf 前拒绝跨 lane/direction。
    /// </summary>
    internal static class ClientBattleRouteCodec
    {
        /// <summary>保存 raw 与 KCP route envelope 宽度。</summary>
        internal const int RouteHeaderBytes = 16;

        /// <summary>保存完整 secure plaintext ceiling。</summary>
        internal const int MaximumPlaintextBytes = 1120;

        /// <summary>
        /// 编码 client-to-server raw route 3000 或 3001。
        /// </summary>
        /// <param name="route">Closed C2S raw route。</param>
        /// <param name="applicationSequence">Nonzero session sequence。</param>
        /// <param name="payload">已通过 generated contract 校验的 Protobuf。</param>
        /// <returns>Canonical raw plaintext。</returns>
        internal static byte[] EncodeRawClient(
            ClientBattleRoute route,
            ulong applicationSequence,
            byte[] payload)
        {
            ValidateEncode(
                route,
                ClientBattleRouteLane.Raw,
                ClientBattleRouteDirection.ClientToServer,
                applicationSequence,
                payload);
            if (route.MessageID != ClientBattleRouteCatalog.Input.MessageID &&
                route.MessageID != ClientBattleRouteCatalog.Probe.MessageID)
            {
                throw new ArgumentException(
                    "client battle raw route is not sendable");
            }

            var output = CreateEnvelope(
                route,
                applicationSequence,
                payload);
            output[6] = 0;
            output[7] = 1;
            return output;
        }

        /// <summary>
        /// 解码 server-to-client raw snapshot envelope。
        /// </summary>
        /// <param name="plaintext">Authenticated raw plaintext。</param>
        /// <param name="frame">成功时返回 caller-owned payload。</param>
        /// <returns>Header、lane、direction、partition 与 size 全部合法时为 true。</returns>
        internal static bool TryDecodeRawServer(
            byte[] plaintext,
            out ClientBattleRouteFrame frame)
        {
            frame = null;
            if (!TryReadEnvelope(
                    plaintext,
                    ClientBattleRouteLane.Raw,
                    ClientBattleRouteDirection.ServerToClient,
                    out var route,
                    out var sequence,
                    out var payloadLength))
            {
                return false;
            }

            if ((route.MessageID !=
                 ClientBattleRouteCatalog.FullSnapshot.MessageID &&
                 route.MessageID !=
                 ClientBattleRouteCatalog.DeltaSnapshot.MessageID) ||
                plaintext[7] == 0 ||
                plaintext[7] >
                ClientBattlePolicy.Current.MaximumSnapshotPartitions ||
                plaintext[6] >= plaintext[7])
            {
                return false;
            }

            frame = new ClientBattleRouteFrame(
                route,
                sequence,
                plaintext[6],
                plaintext[7],
                CopyPayload(plaintext, payloadLength));
            return true;
        }

        /// <summary>
        /// 编码 client-to-server KCP route 3006。
        /// </summary>
        /// <param name="applicationSequence">Nonzero reliable sequence。</param>
        /// <param name="payload">已校验 BattleResyncRequest Protobuf。</param>
        /// <returns>Canonical KCP application frame。</returns>
        internal static byte[] EncodeResyncRequest(
            ulong applicationSequence,
            byte[] payload)
        {
            var route = ClientBattleRouteCatalog.ResyncRequest;
            ValidateEncode(
                route,
                ClientBattleRouteLane.Kcp,
                ClientBattleRouteDirection.ClientToServer,
                applicationSequence,
                payload);
            return CreateEnvelope(route, applicationSequence, payload);
        }

        /// <summary>
        /// 解码 KCP reassembly 后的 server-to-client application frame。
        /// </summary>
        /// <param name="plaintext">Native KCP 返回的完整 message。</param>
        /// <param name="frame">成功时返回 caller-owned Protobuf payload。</param>
        /// <returns>Route、flags、sequence 与 size 全部合法时为 true。</returns>
        internal static bool TryDecodeKcpServer(
            byte[] plaintext,
            out ClientBattleRouteFrame frame)
        {
            frame = null;
            if (!TryReadEnvelope(
                    plaintext,
                    ClientBattleRouteLane.Kcp,
                    ClientBattleRouteDirection.ServerToClient,
                    out var route,
                    out var sequence,
                    out var payloadLength) ||
                plaintext[6] != 0 ||
                plaintext[7] != 0 ||
                (route.MessageID !=
                 ClientBattleRouteCatalog.AbilityEvent.MessageID &&
                 route.MessageID !=
                 ClientBattleRouteCatalog.EntityLifecycle.MessageID &&
                 route.MessageID !=
                 ClientBattleRouteCatalog.ResyncResponse.MessageID))
            {
                return false;
            }

            frame = new ClientBattleRouteFrame(
                route,
                sequence,
                0,
                1,
                CopyPayload(plaintext, payloadLength));
            return true;
        }

        /// <summary>
        /// 验证 encode caller 只能使用 closed route、direction 与 payload ceiling。
        /// </summary>
        /// <param name="route">待编码 route。</param>
        /// <param name="lane">预期 lane。</param>
        /// <param name="direction">预期 direction。</param>
        /// <param name="sequence">Nonzero application sequence。</param>
        /// <param name="payload">Nonempty Protobuf bytes。</param>
        private static void ValidateEncode(
            ClientBattleRoute route,
            ClientBattleRouteLane lane,
            ClientBattleRouteDirection direction,
            ulong sequence,
            byte[] payload)
        {
            if (route == null ||
                !ClientBattleRouteCatalog.TryGet(
                    route.MessageID,
                    out var registered) ||
                !ReferenceEquals(route, registered) ||
                route.Lane != lane ||
                route.Direction != direction ||
                sequence == 0 ||
                payload == null ||
                payload.Length == 0 ||
                payload.Length > route.MaximumPayloadBytes ||
                payload.Length + RouteHeaderBytes >
                MaximumPlaintextBytes)
            {
                throw new ArgumentException(
                    "client battle route input is invalid");
            }
        }

        /// <summary>
        /// 创建 canonical 16-byte envelope 并复制 payload。
        /// </summary>
        /// <param name="route">已验证 route。</param>
        /// <param name="sequence">Nonzero application sequence。</param>
        /// <param name="payload">Validated payload。</param>
        /// <returns>Caller-owned application frame。</returns>
        private static byte[] CreateEnvelope(
            ClientBattleRoute route,
            ulong sequence,
            byte[] payload)
        {
            var output = new byte[RouteHeaderBytes + payload.Length];
            WriteUInt32BigEndian(output, 0, route.MessageID);
            WriteUInt16BigEndian(
                output,
                4,
                checked((ushort)payload.Length));
            WriteUInt64BigEndian(output, 8, sequence);
            Buffer.BlockCopy(
                payload,
                0,
                output,
                RouteHeaderBytes,
                payload.Length);
            return output;
        }

        /// <summary>
        /// 读取公共 envelope 并验证 closed registry、lane、direction 与长度。
        /// </summary>
        /// <param name="plaintext">Authenticated plaintext。</param>
        /// <param name="lane">预期 lane。</param>
        /// <param name="direction">预期 direction。</param>
        /// <param name="route">成功时返回 registered route。</param>
        /// <param name="sequence">成功时返回 application sequence。</param>
        /// <param name="payloadLength">成功时返回 payload width。</param>
        /// <returns>公共 envelope 合法时为 true。</returns>
        private static bool TryReadEnvelope(
            byte[] plaintext,
            ClientBattleRouteLane lane,
            ClientBattleRouteDirection direction,
            out ClientBattleRoute route,
            out ulong sequence,
            out int payloadLength)
        {
            route = null;
            sequence = 0;
            payloadLength = 0;
            if (plaintext == null ||
                plaintext.Length <= RouteHeaderBytes ||
                plaintext.Length > MaximumPlaintextBytes)
            {
                return false;
            }

            var messageID = ReadUInt32BigEndian(plaintext, 0);
            payloadLength = ReadUInt16BigEndian(plaintext, 4);
            sequence = ReadUInt64BigEndian(plaintext, 8);
            return ClientBattleRouteCatalog.TryGet(
                       messageID,
                       out route) &&
                   route.Lane == lane &&
                   route.Direction == direction &&
                   payloadLength > 0 &&
                   payloadLength <= route.MaximumPayloadBytes &&
                   payloadLength + RouteHeaderBytes ==
                   plaintext.Length &&
                   sequence != 0;
        }

        /// <summary>
        /// 复制 envelope 后的 caller-owned Protobuf payload。
        /// </summary>
        /// <param name="plaintext">完整 route frame。</param>
        /// <param name="payloadLength">已验证 payload width。</param>
        /// <returns>新 payload buffer。</returns>
        private static byte[] CopyPayload(
            byte[] plaintext,
            int payloadLength)
        {
            var payload = new byte[payloadLength];
            Buffer.BlockCopy(
                plaintext,
                RouteHeaderBytes,
                payload,
                0,
                payloadLength);
            return payload;
        }

        /// <summary>
        /// 编码 network-order uint16。
        /// </summary>
        /// <param name="output">Destination buffer。</param>
        /// <param name="offset">Value 起点。</param>
        /// <param name="value">Host-order value。</param>
        private static void WriteUInt16BigEndian(
            byte[] output,
            int offset,
            ushort value)
        {
            output[offset] = (byte)(value >> 8);
            output[offset + 1] = (byte)value;
        }

        /// <summary>
        /// 编码 network-order uint32。
        /// </summary>
        /// <param name="output">Destination buffer。</param>
        /// <param name="offset">Value 起点。</param>
        /// <param name="value">Host-order value。</param>
        private static void WriteUInt32BigEndian(
            byte[] output,
            int offset,
            uint value)
        {
            output[offset] = (byte)(value >> 24);
            output[offset + 1] = (byte)(value >> 16);
            output[offset + 2] = (byte)(value >> 8);
            output[offset + 3] = (byte)value;
        }

        /// <summary>
        /// 编码 network-order uint64。
        /// </summary>
        /// <param name="output">Destination buffer。</param>
        /// <param name="offset">Value 起点。</param>
        /// <param name="value">Host-order value。</param>
        private static void WriteUInt64BigEndian(
            byte[] output,
            int offset,
            ulong value)
        {
            for (var index = 0; index < 8; index++)
            {
                output[offset + index] = (byte)(
                    value >> ((7 - index) * 8));
            }
        }

        /// <summary>
        /// 解码 network-order uint16。
        /// </summary>
        /// <param name="input">Source buffer。</param>
        /// <param name="offset">Value 起点。</param>
        /// <returns>Host-order value。</returns>
        private static ushort ReadUInt16BigEndian(
            byte[] input,
            int offset)
        {
            return (ushort)(
                (input[offset] << 8) |
                input[offset + 1]);
        }

        /// <summary>
        /// 解码 network-order uint32。
        /// </summary>
        /// <param name="input">Source buffer。</param>
        /// <param name="offset">Value 起点。</param>
        /// <returns>Host-order value。</returns>
        private static uint ReadUInt32BigEndian(
            byte[] input,
            int offset)
        {
            return ((uint)input[offset] << 24) |
                   ((uint)input[offset + 1] << 16) |
                   ((uint)input[offset + 2] << 8) |
                   input[offset + 3];
        }

        /// <summary>
        /// 解码 network-order uint64。
        /// </summary>
        /// <param name="input">Source buffer。</param>
        /// <param name="offset">Value 起点。</param>
        /// <returns>Host-order value。</returns>
        private static ulong ReadUInt64BigEndian(
            byte[] input,
            int offset)
        {
            ulong output = 0;
            for (var index = 0; index < 8; index++)
            {
                output = (output << 8) | input[offset + index];
            }

            return output;
        }
    }

}
