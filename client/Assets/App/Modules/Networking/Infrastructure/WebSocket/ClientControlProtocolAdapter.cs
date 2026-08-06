using System;
using System.Collections.Generic;
using System.Text;
using IHomeland.Client.Networking.Application.Contracts;
using IHomeland.Client.Networking.Application.Control;
using IHomeland.Client.PersonalWorld.Application;
using IHomeland.Client.Networking.Infrastructure.Tcp;
using IHomeland.Protocol.Control.V1;
using IHomeland.Protocol.Session.V1;
using IHomeland.Protocol.Visit.V1;
using IHomeland.Protocol.World.V1;

namespace IHomeland.Client.Networking.Infrastructure.WebSocket
{
    /// <summary>
    /// 把已校验 WSS generated payload 映射为无 Protocol 依赖的 Application notification。
    /// </summary>
    internal sealed class ClientControlProtocolAdapter
    {
        /// <summary>限制 endpoint manifest，避免小字段重复放大 Application allocation。</summary>
        private const int MaximumEndpointCount = 16;

        /// <summary>限制公开 message/reason key 的 UTF-8 字节数。</summary>
        private const int MaximumKeyBytes = 128;

        /// <summary>映射一条已经由 codec 完成 envelope/route/sequence 校验的 wire PUSH。</summary>
        /// <param name="wire">携带精确 generated payload 的 Infrastructure-owned wire value。</param>
        /// <returns>不含 generated message 或原始 payload 的 Application notification。</returns>
        /// <exception cref="ClientControlProtocolException">Payload 字段或 route/type binding 无效时抛出。</exception>
        internal ClientControlNotification Map(ClientControlWirePush wire)
        {
            if (wire == null)
            {
                throw Invalid();
            }

            try
            {
                switch (wire.Payload)
                {
                    case MaintenancePush maintenance:
                        RequireTimestamp(maintenance.StartsAtMs);
                        RequireTimestamp(maintenance.ExpectedEndAtMs);
                        RequireKey(maintenance.MessageKey);
                        return new ClientMaintenancePush(
                            wire.Sequence,
                            wire.TimestampMilliseconds,
                            maintenance.StartsAtMs,
                            maintenance.ExpectedEndAtMs,
                            maintenance.MessageKey);

                    case ForcedLogoutPush forcedLogout:
                        RequireEpoch(forcedLogout.SessionEpoch);
                        RequireKey(forcedLogout.ReasonKey);
                        return new ClientSessionInvalidationPush(
                            ClientControlPushKind.ForcedLogout,
                            wire.Sequence,
                            wire.TimestampMilliseconds,
                            forcedLogout.SessionEpoch,
                            forcedLogout.ReasonKey);

                    case QueueStatusPush queue:
                        if (queue.Position == 0 || queue.RetryAfterMs == 0)
                        {
                            throw Invalid();
                        }

                        return new ClientQueueStatusPush(
                            wire.Sequence,
                            wire.TimestampMilliseconds,
                            queue.Position,
                            queue.RetryAfterMs);

                    case EndpointUpdatePush endpoints:
                        RequireTimestamp(endpoints.EffectiveAtMs);
                        return new ClientEndpointUpdatePush(
                            wire.Sequence,
                            wire.TimestampMilliseconds,
                            MapEndpoints(endpoints.Endpoints),
                            endpoints.EffectiveAtMs);

                    case SessionInvalidatedPush invalidated:
                        RequireEpoch(invalidated.SessionEpoch);
                        RequireKey(invalidated.ReasonKey);
                        return new ClientSessionInvalidationPush(
                            ClientControlPushKind.SessionInvalidated,
                            wire.Sequence,
                            wire.TimestampMilliseconds,
                            invalidated.SessionEpoch,
                            invalidated.ReasonKey);

                    case WorldAssignmentChangedPush assignment:
                        var projection =
                            ClientWorldProjectionMapper.FromAssignmentHint(
                                assignment,
                                out var personalWorldID);
                        RequireOptionalKey(assignment.ReasonKey);
                        return new ClientWorldAssignmentChangedPush(
                            wire.Sequence,
                            wire.TimestampMilliseconds,
                            personalWorldID,
                            projection,
                            assignment.ReasonKey);

                    case VisitInvitePush invite:
                        return new ClientVisitInvitePush(
                            wire.Sequence,
                            wire.TimestampMilliseconds,
                            ClientWorldProjectionMapper.FromInvitePush(invite));

                    case VisitOwnerAvailabilityPush availability:
                        return new ClientVisitOwnerAvailabilityPush(
                            wire.Sequence,
                            wire.TimestampMilliseconds,
                            ClientWorldProjectionMapper.FromOwnerAvailability(availability));

                    case VisitClosedNoticePush closed:
                        return new ClientVisitClosedPush(
                            wire.Sequence,
                            wire.TimestampMilliseconds,
                            ClientWorldProjectionMapper.FromClosedNotice(closed));

                    default:
                        throw new ClientControlProtocolException(
                            ClientControlProtocolFailureKind.UnknownRoute);
                }
            }
            catch (ClientWorldProjectionException)
            {
                throw Invalid();
            }
            catch (ArgumentException)
            {
                throw Invalid();
            }
        }

        /// <summary>映射并验证 endpoint manifest。</summary>
        private static IReadOnlyList<ClientEndpoint> MapEndpoints(
            IEnumerable<Endpoint> endpoints)
        {
            var mapped = new List<ClientEndpoint>();
            foreach (var endpoint in endpoints)
            {
                if (endpoint == null ||
                    string.IsNullOrWhiteSpace(endpoint.Host) ||
                    Encoding.UTF8.GetByteCount(endpoint.Host) > 253 ||
                    endpoint.Port == 0 ||
                    endpoint.Port > 65535 ||
                    mapped.Count == MaximumEndpointCount)
                {
                    throw Invalid();
                }

                ClientEndpointChannel channel;
                switch (endpoint.Channel)
                {
                    case TransportChannel.Wss:
                        channel = ClientEndpointChannel.Wss;
                        break;
                    case TransportChannel.TlsTcp:
                        channel = ClientEndpointChannel.TlsTcp;
                        break;
                    default:
                        throw Invalid();
                }

                mapped.Add(new ClientEndpoint(channel, endpoint.Host, (int)endpoint.Port));
            }

            if (mapped.Count == 0)
            {
                throw Invalid();
            }

            return mapped;
        }

        /// <summary>验证必须为正数的 Unix 毫秒时间。</summary>
        private static void RequireTimestamp(long value)
        {
            if (value <= 0)
            {
                throw Invalid();
            }
        }

        /// <summary>验证必须前进的 Session epoch。</summary>
        private static void RequireEpoch(ulong value)
        {
            if (value == 0)
            {
                throw Invalid();
            }
        }

        /// <summary>验证必需的低敏本地化或原因 key。</summary>
        private static void RequireKey(string value)
        {
            if (string.IsNullOrWhiteSpace(value) ||
                Encoding.UTF8.GetByteCount(value) > MaximumKeyBytes)
            {
                throw Invalid();
            }
        }

        /// <summary>验证允许为空的低敏原因 key。</summary>
        private static void RequireOptionalKey(string value)
        {
            if (!string.IsNullOrEmpty(value))
            {
                RequireKey(value);
            }
        }

        /// <summary>创建不携带 generated 字段或 payload 的稳定协议失败。</summary>
        private static ClientControlProtocolException Invalid()
        {
            return new ClientControlProtocolException(
                ClientControlProtocolFailureKind.MalformedPayload);
        }
    }
}
