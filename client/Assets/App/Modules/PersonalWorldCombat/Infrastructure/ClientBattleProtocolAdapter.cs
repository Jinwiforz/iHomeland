using System;
using System.Collections.Generic;
using Google.Protobuf;
using IHomeland.Client.PersonalWorldCombat.Application;
using IHomeland.Protocol.Battle.V1;

namespace IHomeland.Client.PersonalWorldCombat.Infrastructure
{
    /// <summary>
    /// 唯一负责 generated battle Protobuf 与纯 Application model 双向映射的 adapter。
    /// </summary>
    internal sealed class ClientBattleProtocolAdapter
    {
        /// <summary>保存最近一次 snapshot 解码失败的低敏稳定阶段。</summary>
        internal string LastSnapshotDecodeFailure { get; private set; } = "none";

        /// <summary>保存 QuantizedTransform closed schema。</summary>
        private static readonly ClientBattleProtobufSchema TransformSchema =
            Schema(
                Scalar(1),
                Scalar(2),
                Scalar(3),
                Scalar(4),
                Scalar(5),
                Scalar(6),
                Scalar(7));

        /// <summary>保存 BattleEntityState closed schema。</summary>
        private static readonly ClientBattleProtobufSchema EntitySchema =
            Schema(
                Scalar(1),
                Scalar(2),
                Nested(3, TransformSchema),
                Scalar(4),
                Scalar(5),
                Scalar(6),
                Scalar(7),
                Scalar(8));

        /// <summary>保存 BattleEntityDelta closed schema。</summary>
        private static readonly ClientBattleProtobufSchema DeltaSchema =
            Schema(
                Scalar(1),
                Scalar(2),
                Scalar(3),
                Nested(4, TransformSchema),
                Scalar(5),
                Scalar(6),
                Scalar(7));

        /// <summary>保存 BattleFullSnapshot closed schema。</summary>
        private static readonly ClientBattleProtobufSchema FullSchema =
            Schema(
                Scalar(1),
                Scalar(2),
                Scalar(3),
                Scalar(4),
                Scalar(5),
                RepeatedNested(6, EntitySchema),
                Scalar(7));

        /// <summary>保存 BattleDeltaSnapshot closed schema。</summary>
        private static readonly ClientBattleProtobufSchema SnapshotDeltaSchema =
            Schema(
                Scalar(1),
                Scalar(2),
                Scalar(3),
                Scalar(4),
                Scalar(5),
                RepeatedNested(6, DeltaSchema),
                Scalar(7));

        /// <summary>保存 BattleAbilityReliableEvent closed schema。</summary>
        private static readonly ClientBattleProtobufSchema AbilitySchema =
            Schema(
                Scalar(1),
                Scalar(2),
                Scalar(3),
                Scalar(4),
                Scalar(5),
                Scalar(6),
                Packed(7));

        /// <summary>保存 BattleEntityLifecycle closed schema。</summary>
        private static readonly ClientBattleProtobufSchema LifecycleSchema =
            Schema(
                Scalar(1),
                Scalar(2),
                Scalar(3),
                Scalar(4),
                Scalar(5),
                Scalar(6),
                Nested(7, EntitySchema));

        /// <summary>保存 BattleResyncResponse closed schema。</summary>
        private static readonly ClientBattleProtobufSchema
            ResyncResponseSchema =
                Schema(
                    Scalar(1),
                    Scalar(2),
                    Scalar(3),
                    Scalar(4),
                    Scalar(5));

        /// <summary>
        /// 把有限冗余 semantic input bundle 编码为 route 3000 Protobuf。
        /// </summary>
        /// <param name="bundle">Current generation immutable bundle。</param>
        /// <returns>不含 route header 的 bounded Protobuf bytes。</returns>
        internal byte[] EncodeInputBundle(ClientBattleInputBundle bundle)
        {
            if (bundle == null)
            {
                throw new ArgumentNullException(nameof(bundle));
            }

            var generated = new BattleInputBundle
            {
                NewestInputTick = bundle.NewestInputTick,
                LatestObservedServerTick =
                    bundle.LatestObservedServerTick,
            };
            ulong previousSequence = 0;
            foreach (var frame in bundle.Frames)
            {
                foreach (var command in frame.Commands)
                {
                    if (command.CommandSequence <= previousSequence)
                    {
                        throw new ArgumentException(
                            "client battle command order is invalid",
                            nameof(bundle));
                    }

                    previousSequence = command.CommandSequence;
                    var generatedCommand = new BattleInputCommand
                    {
                        CommandSequence = command.CommandSequence,
                        Kind = (BattleInputKind)(int)command.Kind,
                    };
                    switch (command.Kind)
                    {
                        case ClientBattleInputKind.Move:
                            generatedCommand.MoveXMilli =
                                command.MoveXMilli;
                            generatedCommand.MoveYMilli =
                                command.MoveYMilli;
                            break;
                        case ClientBattleInputKind.Aim:
                            generatedCommand.AimYawMillidegrees =
                                command.AimYawMillidegrees;
                            generatedCommand.AimPitchMillidegrees =
                                command.AimPitchMillidegrees;
                            break;
                        case ClientBattleInputKind.Interact:
                            generatedCommand.InteractionSlot =
                                command.InteractionSlot;
                            break;
                        case ClientBattleInputKind.SwitchWeapon:
                            break;
                    }

                    generated.Commands.Add(generatedCommand);
                }
            }

            return SerializeBounded(
                generated,
                ClientBattleRouteCatalog.Input.MaximumPayloadBytes);
        }

        /// <summary>
        /// 编码不携带业务 mutation 的 route 3001 probe。
        /// </summary>
        /// <param name="probeSequence">Generation 内 nonzero sequence。</param>
        /// <param name="latestSnapshotSequence">已应用 snapshot frontier。</param>
        /// <param name="clientMonotonicTimeMicroseconds">本地 monotonic sample。</param>
        /// <returns>Bounded BattleProbe Protobuf bytes。</returns>
        internal byte[] EncodeProbe(
            ulong probeSequence,
            ulong latestSnapshotSequence,
            ulong clientMonotonicTimeMicroseconds)
        {
            if (probeSequence == 0 ||
                clientMonotonicTimeMicroseconds == 0)
            {
                throw new ArgumentException(
                    "client battle probe is invalid");
            }

            var generated = new BattleProbe
            {
                ProbeSequence = probeSequence,
                LatestSnapshotSequence = latestSnapshotSequence,
                ClientMonotonicTimeUs =
                    clientMonotonicTimeMicroseconds,
            };
            return SerializeBounded(
                generated,
                ClientBattleRouteCatalog.Probe.MaximumPayloadBytes);
        }

        /// <summary>
        /// 编码 single-flight route 3006 resync request。
        /// </summary>
        /// <param name="request">纯 Application recovery intent。</param>
        /// <returns>Bounded BattleResyncRequest Protobuf bytes。</returns>
        internal byte[] EncodeResyncRequest(
            ClientBattleResyncRequest request)
        {
            if (request == null)
            {
                throw new ArgumentNullException(nameof(request));
            }

            var generated = new BattleResyncRequest
            {
                RequestSequence = request.RequestSequence,
                LatestServerTick = request.LatestServerTick,
                MissingBaselineId = request.MissingBaselineID,
                LatestSnapshotSequence =
                    request.LatestSnapshotSequence,
                Reason = (BattleResyncReason)(int)request.Reason,
            };
            return SerializeBounded(
                generated,
                ClientBattleRouteCatalog.ResyncRequest.
                    MaximumPayloadBytes);
        }

        /// <summary>
        /// 解码 raw route 3002/3003，并强制 Protobuf metadata 与 route header 一致。
        /// </summary>
        /// <param name="battleGeneration">Current client battle generation。</param>
        /// <param name="frame">已验证 raw server route frame。</param>
        /// <param name="partition">成功时返回 immutable Application partition。</param>
        /// <returns>Closed wire、presence、排序与 mask 全部合法时为 true。</returns>
        internal bool TryDecodeSnapshot(
            long battleGeneration,
            ClientBattleRouteFrame frame,
            out ClientBattleSnapshotPartition partition)
        {
            partition = null;
            LastSnapshotDecodeFailure = "precondition";
            if (battleGeneration <= 0 ||
                frame == null ||
                frame.Route.Lane != ClientBattleRouteLane.Raw ||
                frame.Route.Direction !=
                ClientBattleRouteDirection.ServerToClient)
            {
                return false;
            }

            try
            {
                if (frame.Route.MessageID ==
                    ClientBattleRouteCatalog.FullSnapshot.MessageID)
                {
                    var decoded = TryDecodeFull(
                        battleGeneration,
                        frame,
                        out partition);
                    if (decoded)
                    {
                        LastSnapshotDecodeFailure = "none";
                    }

                    return decoded;
                }

                if (frame.Route.MessageID ==
                    ClientBattleRouteCatalog.DeltaSnapshot.MessageID)
                {
                    var decoded = TryDecodeDelta(
                        battleGeneration,
                        frame,
                        out partition);
                    if (decoded)
                    {
                        LastSnapshotDecodeFailure = "none";
                    }

                    return decoded;
                }
            }
            catch (InvalidProtocolBufferException)
            {
                LastSnapshotDecodeFailure = "protobuf";
                return false;
            }
            catch (ArgumentException)
            {
                LastSnapshotDecodeFailure = "application-contract";
                return false;
            }
            catch (OverflowException)
            {
                LastSnapshotDecodeFailure = "numeric-overflow";
                return false;
            }

            LastSnapshotDecodeFailure = "message-route";
            return false;
        }

        /// <summary>
        /// 解码 route 3004 reliable ability event。
        /// </summary>
        /// <param name="battleGeneration">Current client battle generation。</param>
        /// <param name="frame">已验证 KCP server frame。</param>
        /// <param name="abilityEvent">成功时返回 immutable Application event。</param>
        /// <returns>Closed wire 与业务边界合法时为 true。</returns>
        internal bool TryDecodeAbilityEvent(
            long battleGeneration,
            ClientBattleRouteFrame frame,
            out ClientBattleAbilityEvent abilityEvent)
        {
            abilityEvent = null;
            if (!MatchesReliableRoute(
                    battleGeneration,
                    frame,
                    ClientBattleRouteCatalog.AbilityEvent) ||
                !ClientBattleClosedProtobuf.Validate(
                    frame.Payload,
                    AbilitySchema))
            {
                return false;
            }

            try
            {
                var message =
                    BattleAbilityReliableEvent.Parser.ParseFrom(
                        frame.Payload);
                if (!message.HasEventId ||
                    !message.HasServerTick ||
                    !message.HasSourceEntityId ||
                    !message.HasSourceEntityGeneration ||
                    !message.HasAbilityId ||
                    !message.HasPhase ||
                    message.EventId == 0 ||
                    message.ServerTick == 0 ||
                    message.SourceEntityId == 0 ||
                    message.SourceEntityGeneration == 0 ||
                    !ClientBattleContentIdentity.IsKnownAbility(
                        message.AbilityId) ||
                    (int)message.Phase < 1 ||
                    (int)message.Phase > 4 ||
                    message.TargetEntityIds.Count > 16)
                {
                    return false;
                }

                var targets = new ulong[message.TargetEntityIds.Count];
                for (var index = 0; index < targets.Length; index++)
                {
                    targets[index] = message.TargetEntityIds[index];
                }

                abilityEvent = new ClientBattleAbilityEvent(
                    battleGeneration,
                    message.EventId,
                    message.ServerTick,
                    message.SourceEntityId,
                    message.SourceEntityGeneration,
                    message.AbilityId,
                    (ClientBattleAbilityPhase)(int)message.Phase,
                    targets);
                return true;
            }
            catch (InvalidProtocolBufferException)
            {
                return false;
            }
            catch (ArgumentException)
            {
                return false;
            }
        }

        /// <summary>
        /// 解码 route 3005 reliable entity lifecycle。
        /// </summary>
        /// <param name="battleGeneration">Current client battle generation。</param>
        /// <param name="frame">已验证 KCP server frame。</param>
        /// <param name="lifecycle">成功时返回 immutable Application event。</param>
        /// <returns>Closed wire 与 spawn/despawn presence 合法时为 true。</returns>
        internal bool TryDecodeEntityLifecycle(
            long battleGeneration,
            ClientBattleRouteFrame frame,
            out ClientBattleEntityLifecycle lifecycle)
        {
            lifecycle = null;
            if (!MatchesReliableRoute(
                    battleGeneration,
                    frame,
                    ClientBattleRouteCatalog.EntityLifecycle) ||
                !ClientBattleClosedProtobuf.Validate(
                    frame.Payload,
                    LifecycleSchema))
            {
                return false;
            }

            try
            {
                var message =
                    BattleEntityLifecycle.Parser.ParseFrom(
                        frame.Payload);
                if (!message.HasEventId ||
                    !message.HasServerTick ||
                    !message.HasEntityId ||
                    !message.HasEntityGeneration ||
                    !message.HasKind ||
                    !message.HasArchetypeId ||
                    message.EventId == 0 ||
                    message.ServerTick == 0 ||
                    message.EntityId == 0 ||
                    message.EntityGeneration == 0 ||
                    (int)message.Kind < 1 ||
                    (int)message.Kind > 2)
                {
                    return false;
                }

                var kind =
                    (ClientBattleEntityLifecycleKind)(int)message.Kind;
                ClientBattleEntityState initial = null;
                if (kind == ClientBattleEntityLifecycleKind.Spawn)
                {
                    if (message.InitialState == null ||
                        !TryMapEntity(
                            message.InitialState,
                            out initial) ||
                        message.ArchetypeId == 0 ||
                        message.ArchetypeId != initial.ArchetypeID)
                    {
                        return false;
                    }
                }
                else if (message.InitialState != null ||
                         message.ArchetypeId != 0)
                {
                    return false;
                }

                lifecycle = new ClientBattleEntityLifecycle(
                    battleGeneration,
                    message.EventId,
                    message.ServerTick,
                    message.EntityId,
                    message.EntityGeneration,
                    kind,
                    message.ArchetypeId,
                    initial);
                return true;
            }
            catch (InvalidProtocolBufferException)
            {
                return false;
            }
            catch (ArgumentException)
            {
                return false;
            }
        }

        /// <summary>
        /// 解码 route 3007 resync response。
        /// </summary>
        /// <param name="battleGeneration">Current client battle generation。</param>
        /// <param name="frame">已验证 KCP server frame。</param>
        /// <param name="response">成功时返回 immutable Application response。</param>
        /// <returns>Disposition-specific fields 与上限合法时为 true。</returns>
        internal bool TryDecodeResyncResponse(
            long battleGeneration,
            ClientBattleRouteFrame frame,
            out ClientBattleResyncResponse response)
        {
            response = null;
            if (!MatchesReliableRoute(
                    battleGeneration,
                    frame,
                    ClientBattleRouteCatalog.ResyncResponse) ||
                !ClientBattleClosedProtobuf.Validate(
                    frame.Payload,
                    ResyncResponseSchema))
            {
                return false;
            }

            try
            {
                var message =
                    BattleResyncResponse.Parser.ParseFrom(
                        frame.Payload);
                if (!message.HasRequestSequence ||
                    !message.HasServerTick ||
                    !message.HasDisposition ||
                    !message.HasScheduledBaselineId ||
                    !message.HasRetryAfterMs ||
                    message.RequestSequence == 0 ||
                    message.ServerTick == 0 ||
                    (int)message.Disposition < 1 ||
                    (int)message.Disposition > 3)
                {
                    return false;
                }

                response = new ClientBattleResyncResponse(
                    battleGeneration,
                    message.RequestSequence,
                    message.ServerTick,
                    (ClientBattleResyncDisposition)
                    (int)message.Disposition,
                    message.ScheduledBaselineId,
                    message.RetryAfterMs);
                return true;
            }
            catch (InvalidProtocolBufferException)
            {
                return false;
            }
            catch (ArgumentException)
            {
                return false;
            }
        }

        /// <summary>
        /// 解码 full snapshot 并建立 immutable entity partition。
        /// </summary>
        /// <param name="battleGeneration">Current battle generation。</param>
        /// <param name="frame">Raw route frame。</param>
        /// <param name="partition">成功时返回 full partition。</param>
        /// <returns>Closed contract 完整合法时为 true。</returns>
        private bool TryDecodeFull(
            long battleGeneration,
            ClientBattleRouteFrame frame,
            out ClientBattleSnapshotPartition partition)
        {
            partition = null;
            if (!ClientBattleClosedProtobuf.Validate(
                    frame.Payload,
                    FullSchema))
            {
                LastSnapshotDecodeFailure = "full-closed-schema";
                return false;
            }

            var message = BattleFullSnapshot.Parser.ParseFrom(
                frame.Payload);
            if (!message.HasServerTick ||
                !message.HasSnapshotSequence ||
                !message.HasBaselineId ||
                !message.HasPartitionIndex ||
                !message.HasPartitionCount ||
                !message.HasLastProcessedInputTick ||
                message.ServerTick == 0 ||
                message.SnapshotSequence == 0 ||
                message.BaselineId == 0 ||
                message.PartitionCount == 0 ||
                message.PartitionCount >
                ClientBattlePolicy.Current.MaximumSnapshotPartitions ||
                message.PartitionIndex >= message.PartitionCount ||
                message.PartitionIndex != frame.PartitionIndex ||
                message.PartitionCount != frame.PartitionCount ||
                message.Entities.Count == 0 ||
                message.Entities.Count >
                ClientBattlePolicy.Current.MaximumEntities)
            {
                LastSnapshotDecodeFailure = "full-metadata";
                return false;
            }

            var entities =
                new ClientBattleEntityState[message.Entities.Count];
            for (var index = 0; index < entities.Length; index++)
            {
                if (!TryMapEntity(message.Entities[index], out entities[index]))
                {
                    LastSnapshotDecodeFailure = ClassifyEntityFailure(
                        message.Entities[index]);
                    return false;
                }
            }

            partition = new ClientBattleSnapshotPartition(
                battleGeneration,
                ClientBattleSnapshotKind.Full,
                message.ServerTick,
                message.SnapshotSequence,
                message.BaselineId,
                checked((int)message.PartitionIndex),
                checked((int)message.PartitionCount),
                message.LastProcessedInputTick,
                entities,
                Array.Empty<ClientBattleEntityDelta>());
            return true;
        }

        /// <summary>
        /// 解码 delta snapshot 并验证 state-mask/presence。
        /// </summary>
        /// <param name="battleGeneration">Current battle generation。</param>
        /// <param name="frame">Raw route frame。</param>
        /// <param name="partition">成功时返回 delta partition。</param>
        /// <returns>Closed contract 完整合法时为 true。</returns>
        private bool TryDecodeDelta(
            long battleGeneration,
            ClientBattleRouteFrame frame,
            out ClientBattleSnapshotPartition partition)
        {
            partition = null;
            if (!ClientBattleClosedProtobuf.Validate(
                    frame.Payload,
                    SnapshotDeltaSchema))
            {
                LastSnapshotDecodeFailure = "delta-closed-schema";
                return false;
            }

            var message = BattleDeltaSnapshot.Parser.ParseFrom(
                frame.Payload);
            if (!message.HasServerTick ||
                !message.HasSnapshotSequence ||
                !message.HasBaselineId ||
                !message.HasPartitionIndex ||
                !message.HasPartitionCount ||
                !message.HasLastProcessedInputTick ||
                message.ServerTick == 0 ||
                message.SnapshotSequence == 0 ||
                message.BaselineId == 0 ||
                message.PartitionCount == 0 ||
                message.PartitionCount >
                ClientBattlePolicy.Current.MaximumSnapshotPartitions ||
                message.PartitionIndex >= message.PartitionCount ||
                message.PartitionIndex != frame.PartitionIndex ||
                message.PartitionCount != frame.PartitionCount ||
                message.Deltas.Count == 0 ||
                message.Deltas.Count >
                ClientBattlePolicy.Current.MaximumEntities)
            {
                LastSnapshotDecodeFailure = "delta-metadata";
                return false;
            }

            var deltas =
                new ClientBattleEntityDelta[message.Deltas.Count];
            for (var index = 0; index < deltas.Length; index++)
            {
                if (!TryMapDelta(message.Deltas[index], out deltas[index]))
                {
                    LastSnapshotDecodeFailure = "delta-entity";
                    return false;
                }
            }

            partition = new ClientBattleSnapshotPartition(
                battleGeneration,
                ClientBattleSnapshotKind.Delta,
                message.ServerTick,
                message.SnapshotSequence,
                message.BaselineId,
                checked((int)message.PartitionIndex),
                checked((int)message.PartitionCount),
                message.LastProcessedInputTick,
                Array.Empty<ClientBattleEntityState>(),
                deltas);
            return true;
        }

        /// <summary>把 entity 解码拒绝归类为不包含 identity 或 payload 的稳定阶段。</summary>
        /// <param name="message">已由 Protobuf parser 创建的 entity。</param>
        /// <returns>供 Development 诊断使用的封闭失败阶段。</returns>
        private static string ClassifyEntityFailure(BattleEntityState message)
        {
            if (message == null ||
                !message.HasEntityId ||
                !message.HasEntityGeneration ||
                !message.HasHealthMilli ||
                !message.HasStateFlags ||
                !message.HasArchetypeId ||
                !message.HasEquippedWeaponId ||
                !message.HasMaxHealthMilli ||
                message.Transform == null)
            {
                return "full-entity-presence";
            }

            if (message.EntityId == 0 || message.EntityGeneration == 0)
            {
                return "full-entity-identity";
            }

            if (!ClientBattleContentIdentity.IsKnownArchetype(
                    message.ArchetypeId) ||
                !ClientBattleContentIdentity.WeaponMatchesArchetype(
                    message.ArchetypeId,
                    message.EquippedWeaponId))
            {
                return "full-entity-content";
            }

            if (message.MaxHealthMilli == 0 ||
                message.HealthMilli > message.MaxHealthMilli)
            {
                return "full-entity-health";
            }

            if ((message.StateFlags &
                 ~ClientBattleEntityState.KnownStateFlags) != 0)
            {
                return "full-entity-state-flags";
            }

            return "full-entity-transform";
        }

        /// <summary>
        /// 映射 full/lifecycle 使用的完整 entity state。
        /// </summary>
        /// <param name="message">Generated entity。</param>
        /// <param name="entity">成功时返回纯 Application entity。</param>
        /// <returns>Presence、identity、transform 与 flags 合法时为 true。</returns>
        private static bool TryMapEntity(
            BattleEntityState message,
            out ClientBattleEntityState entity)
        {
            entity = null;
            if (message == null ||
                !message.HasEntityId ||
                !message.HasEntityGeneration ||
                !message.HasHealthMilli ||
                !message.HasStateFlags ||
                !message.HasArchetypeId ||
                !message.HasEquippedWeaponId ||
                !message.HasMaxHealthMilli ||
                message.EntityId == 0 ||
                message.EntityGeneration == 0 ||
                !ClientBattleContentIdentity.IsKnownArchetype(
                    message.ArchetypeId) ||
                !ClientBattleContentIdentity.WeaponMatchesArchetype(
                    message.ArchetypeId,
                    message.EquippedWeaponId) ||
                message.MaxHealthMilli == 0 ||
                message.HealthMilli > message.MaxHealthMilli ||
                message.Transform == null ||
                (message.StateFlags &
                 ~ClientBattleEntityState.KnownStateFlags) != 0 ||
                !TryMapTransform(
                    message.Transform,
                    out var transform))
            {
                return false;
            }

            entity = new ClientBattleEntityState(
                message.EntityId,
                message.EntityGeneration,
                transform,
                message.HealthMilli,
                message.StateFlags,
                message.ArchetypeId,
                message.EquippedWeaponId,
                message.MaxHealthMilli);
            return true;
        }

        /// <summary>
        /// 映射 delta 并拒绝 mask 与 scalar/message presence 不一致。
        /// </summary>
        /// <param name="message">Generated entity delta。</param>
        /// <param name="delta">成功时返回纯 Application delta。</param>
        /// <returns>Closed mask 与字段 presence 精确匹配时为 true。</returns>
        private static bool TryMapDelta(
            BattleEntityDelta message,
            out ClientBattleEntityDelta delta)
        {
            delta = null;
            if (message == null ||
                !message.HasEntityId ||
                !message.HasEntityGeneration ||
                !message.HasStateMask ||
                message.EntityId == 0 ||
                message.EntityGeneration == 0 ||
                message.StateMask == 0 ||
                (message.StateMask & ~15U) != 0 ||
                ((message.StateMask &
                  ClientBattleEntityDelta.TransformMask) != 0) !=
                (message.Transform != null) ||
                ((message.StateMask &
                  ClientBattleEntityDelta.HealthMask) != 0) !=
                message.HasHealthMilli ||
                ((message.StateMask &
                  ClientBattleEntityDelta.StateFlagsMask) != 0) !=
                message.HasStateFlags ||
                ((message.StateMask &
                  ClientBattleEntityDelta.EquippedWeaponMask) != 0) !=
                message.HasEquippedWeaponId ||
                (message.HasEquippedWeaponId &&
                  message.EquippedWeaponId != 0 &&
                  !ClientBattleContentIdentity.IsKnownWeapon(
                      message.EquippedWeaponId)))
            {
                return false;
            }

            ClientBattleTransform? transform = null;
            if (message.Transform != null)
            {
                if (!TryMapTransform(message.Transform, out var mapped))
                {
                    return false;
                }

                transform = mapped;
            }

            delta = new ClientBattleEntityDelta(
                message.EntityId,
                message.EntityGeneration,
                message.StateMask,
                transform,
                message.HasHealthMilli
                    ? (uint?)message.HealthMilli
                    : null,
                message.HasStateFlags
                    ? (uint?)message.StateFlags
                    : null,
                message.HasEquippedWeaponId
                    ? (uint?)message.EquippedWeaponId
                    : null);
            return true;
        }

        /// <summary>
        /// 映射完整 quantized transform，并要求 edition scalar presence。
        /// </summary>
        /// <param name="message">Generated transform。</param>
        /// <param name="transform">成功时返回纯 value type。</param>
        /// <returns>七个量化字段显式存在且 yaw 合法时为 true。</returns>
        private static bool TryMapTransform(
            QuantizedTransform message,
            out ClientBattleTransform transform)
        {
            transform = default;
            if (message == null ||
                !message.HasPositionXMm ||
                !message.HasPositionYMm ||
                !message.HasPositionZMm ||
                !message.HasYawMillidegrees ||
                !message.HasVelocityXMmPerSecond ||
                !message.HasVelocityYMmPerSecond ||
                !message.HasVelocityZMmPerSecond ||
                message.YawMillidegrees < -180000 ||
                message.YawMillidegrees >= 180000)
            {
                return false;
            }

            transform = new ClientBattleTransform(
                message.PositionXMm,
                message.PositionYMm,
                message.PositionZMm,
                message.YawMillidegrees,
                message.VelocityXMmPerSecond,
                message.VelocityYMmPerSecond,
                message.VelocityZMmPerSecond);
            return true;
        }

        /// <summary>
        /// 验证 KCP server frame 精确匹配 expected registered route。
        /// </summary>
        /// <param name="battleGeneration">Current battle generation。</param>
        /// <param name="frame">Route frame。</param>
        /// <param name="expected">Expected catalog singleton。</param>
        /// <returns>Generation、lane、direction 与 route 全部匹配时为 true。</returns>
        private static bool MatchesReliableRoute(
            long battleGeneration,
            ClientBattleRouteFrame frame,
            ClientBattleRoute expected)
        {
            return battleGeneration > 0 &&
                   frame != null &&
                   ReferenceEquals(frame.Route, expected) &&
                   frame.Route.Lane == ClientBattleRouteLane.Kcp &&
                   frame.Route.Direction ==
                   ClientBattleRouteDirection.ServerToClient;
        }

        /// <summary>
        /// 序列化 trusted generated message 并执行 route payload ceiling。
        /// </summary>
        /// <param name="message">由 adapter 构造的 typed message。</param>
        /// <param name="maximumBytes">Registry payload ceiling。</param>
        /// <returns>Nonempty caller-owned bytes。</returns>
        private static byte[] SerializeBounded(
            IMessage message,
            int maximumBytes)
        {
            var payload = message.ToByteArray();
            if (payload.Length == 0 ||
                payload.Length > maximumBytes)
            {
                Array.Clear(payload, 0, payload.Length);
                throw new ArgumentException(
                    "client battle protobuf payload is out of bounds");
            }

            return payload;
        }

        /// <summary>
        /// 创建 field-number 唯一的 closed schema。
        /// </summary>
        /// <param name="fields">一个或多个 field rule pair。</param>
        /// <returns>Immutable-use schema。</returns>
        private static ClientBattleProtobufSchema Schema(
            params KeyValuePair<int, ClientBattleProtobufFieldRule>[] fields)
        {
            var map =
                new Dictionary<int, ClientBattleProtobufFieldRule>();
            foreach (var field in fields)
            {
                if (!map.TryAdd(field.Key, field.Value))
                {
                    throw new InvalidOperationException(
                        "client battle protobuf schema duplicates a field");
                }
            }

            return new ClientBattleProtobufSchema(map);
        }

        /// <summary>
        /// 创建 non-repeated varint field rule。
        /// </summary>
        /// <param name="fieldNumber">Positive field number。</param>
        /// <returns>Schema pair。</returns>
        private static KeyValuePair<int, ClientBattleProtobufFieldRule>
            Scalar(int fieldNumber)
        {
            return new KeyValuePair<int, ClientBattleProtobufFieldRule>(
                fieldNumber,
                new ClientBattleProtobufFieldRule(0, false));
        }

        /// <summary>
        /// 创建 non-repeated nested message field rule。
        /// </summary>
        /// <param name="fieldNumber">Positive field number。</param>
        /// <param name="schema">Nested closed schema。</param>
        /// <returns>Schema pair。</returns>
        private static KeyValuePair<int, ClientBattleProtobufFieldRule>
            Nested(int fieldNumber, ClientBattleProtobufSchema schema)
        {
            return new KeyValuePair<int, ClientBattleProtobufFieldRule>(
                fieldNumber,
                new ClientBattleProtobufFieldRule(
                    2,
                    false,
                    schema));
        }

        /// <summary>
        /// 创建 repeated nested message field rule。
        /// </summary>
        /// <param name="fieldNumber">Positive field number。</param>
        /// <param name="schema">Nested closed schema。</param>
        /// <returns>Schema pair。</returns>
        private static KeyValuePair<int, ClientBattleProtobufFieldRule>
            RepeatedNested(
                int fieldNumber,
                ClientBattleProtobufSchema schema)
        {
            return new KeyValuePair<int, ClientBattleProtobufFieldRule>(
                fieldNumber,
                new ClientBattleProtobufFieldRule(
                    2,
                    true,
                    schema));
        }

        /// <summary>
        /// 创建 packed repeated uint64 field rule。
        /// </summary>
        /// <param name="fieldNumber">Positive field number。</param>
        /// <returns>Schema pair。</returns>
        private static KeyValuePair<int, ClientBattleProtobufFieldRule>
            Packed(int fieldNumber)
        {
            return new KeyValuePair<int, ClientBattleProtobufFieldRule>(
                fieldNumber,
                new ClientBattleProtobufFieldRule(
                    2,
                    false,
                    packedVarints: true));
        }
    }
}
