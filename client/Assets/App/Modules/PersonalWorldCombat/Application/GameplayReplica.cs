using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;

namespace IHomeland.Client.PersonalWorldCombat.Application
{
    /// <summary>
    /// 标识 snapshot partition 提交给 replica 的封闭结果。
    /// </summary>
    internal enum ClientBattleSnapshotCommit
    {
        /// <summary>集合尚未完整，未发布任何 state。</summary>
        Pending = 1,

        /// <summary>完整集合已原子发布。</summary>
        Published = 2,

        /// <summary>该 partition 或 snapshot 已幂等处理。</summary>
        Duplicate = 3,

        /// <summary>集合不能继续应用且已触发 single-flight resync。</summary>
        ResyncRequested = 4,
    }

    /// <summary>
    /// 标识 route 3007 对 resync 请求的封闭决议。
    /// </summary>
    internal enum ClientBattleResyncDisposition
    {
        /// <summary>下一轮 raw full baseline 已安排。</summary>
        Scheduled = 1,

        /// <summary>请求有效但必须等待 retry_after。</summary>
        RateLimited = 2,

        /// <summary>请求已被更高恢复 intent 取代。</summary>
        StaleRequest = 3,
    }

    /// <summary>
    /// 保存 route 3007 的已验证 resync response。
    /// </summary>
    internal sealed class ClientBattleResyncResponse
    {
        /// <summary>
        /// 创建与 current request 绑定的封闭响应。
        /// </summary>
        internal ClientBattleResyncResponse(
            long battleGeneration,
            ulong requestSequence,
            ulong serverTick,
            ClientBattleResyncDisposition disposition,
            ulong scheduledBaselineID,
            uint retryAfterMilliseconds)
        {
            var scheduled = disposition == ClientBattleResyncDisposition.Scheduled;
            var limited = disposition == ClientBattleResyncDisposition.RateLimited;
            if (battleGeneration <= 0 ||
                requestSequence == 0 ||
                serverTick == 0 ||
                !Enum.IsDefined(typeof(ClientBattleResyncDisposition), disposition) ||
                (scheduled != (scheduledBaselineID != 0)) ||
                (limited != (retryAfterMilliseconds != 0)) ||
                retryAfterMilliseconds > 60000)
            {
                throw new ArgumentException("Client battle resync response is invalid.");
            }

            BattleGeneration = battleGeneration;
            RequestSequence = requestSequence;
            ServerTick = serverTick;
            Disposition = disposition;
            ScheduledBaselineID = scheduledBaselineID;
            RetryAfterMilliseconds = retryAfterMilliseconds;
        }

        /// <summary>获取来源 battle generation。</summary>
        internal long BattleGeneration { get; }

        /// <summary>获取对应 request sequence。</summary>
        internal ulong RequestSequence { get; }

        /// <summary>获取 authority decision Tick。</summary>
        internal ulong ServerTick { get; }

        /// <summary>获取封闭决议。</summary>
        internal ClientBattleResyncDisposition Disposition { get; }

        /// <summary>获取 Scheduled 时即将发送的 baseline identity。</summary>
        internal ulong ScheduledBaselineID { get; }

        /// <summary>获取 RateLimited 时的最短等待，单位毫秒。</summary>
        internal uint RetryAfterMilliseconds { get; }
    }

    /// <summary>
    /// 保存 GameplayReplica 当前原子发布的低敏 state。
    /// </summary>
    internal sealed class ClientGameplayReplicaSnapshot
    {
        /// <summary>
        /// 创建 immutable replica snapshot。
        /// </summary>
        internal ClientGameplayReplicaSnapshot(
            long battleGeneration,
            ulong localEntityID,
            ulong serverTick,
            ulong snapshotSequence,
            ulong baselineID,
            ulong lastProcessedInputTick,
            bool resyncPending,
            IReadOnlyList<ClientBattleEntityState> entities,
            IReadOnlyList<ClientBattleAbilityEvent> abilityEvents,
            ulong observedAbilityEventCount = 0,
            ulong observedLocalAbilityEventCount = 0,
            uint lastObservedLocalAbilityID = 0)
        {
            if (battleGeneration < 0 ||
                (battleGeneration == 0 && localEntityID != 0) ||
                entities == null ||
                abilityEvents == null ||
                entities.Count > ClientBattlePolicy.Current.MaximumEntities ||
                abilityEvents.Count > ClientBattlePolicy.Current.MaximumGameplayCues ||
                observedLocalAbilityEventCount > observedAbilityEventCount ||
                (observedLocalAbilityEventCount == 0) !=
                    (lastObservedLocalAbilityID == 0) ||
                (lastObservedLocalAbilityID != 0 &&
                 !ClientBattleContentIdentity.IsKnownAbility(
                     lastObservedLocalAbilityID)))
            {
                throw new ArgumentException("Client gameplay replica snapshot is invalid.");
            }

            BattleGeneration = battleGeneration;
            LocalEntityID = localEntityID;
            ServerTick = serverTick;
            SnapshotSequence = snapshotSequence;
            BaselineID = baselineID;
            LastProcessedInputTick = lastProcessedInputTick;
            ResyncPending = resyncPending;
            Entities = entities;
            AbilityEvents = abilityEvents;
            ObservedAbilityEventCount = observedAbilityEventCount;
            ObservedLocalAbilityEventCount = observedLocalAbilityEventCount;
            LastObservedLocalAbilityID = lastObservedLocalAbilityID;
        }

        /// <summary>获取 current battle generation。</summary>
        internal long BattleGeneration { get; }

        /// <summary>获取 wire v1 actor_slot + 1 锁定的 local entity。</summary>
        internal ulong LocalEntityID { get; }

        /// <summary>获取已发布 authority server Tick。</summary>
        internal ulong ServerTick { get; }

        /// <summary>获取已发布 snapshot sequence。</summary>
        internal ulong SnapshotSequence { get; }

        /// <summary>获取 current full baseline identity。</summary>
        internal ulong BaselineID { get; }

        /// <summary>获取完整 partition set 的 input acknowledgement。</summary>
        internal ulong LastProcessedInputTick { get; }

        /// <summary>获取是否存在 current single-flight resync。</summary>
        internal bool ResyncPending { get; }

        /// <summary>获取按 entity identity 排序的 authority states。</summary>
        internal IReadOnlyList<ClientBattleEntityState> Entities { get; }

        /// <summary>获取有界且未由 projector 消费的 reliable ability events。</summary>
        internal IReadOnlyList<ClientBattleAbilityEvent> AbilityEvents { get; }

        /// <summary>获取current generation已接受的累计ability event数。</summary>
        internal ulong ObservedAbilityEventCount { get; }

        /// <summary>获取current local actor已接受的累计ability event数。</summary>
        internal ulong ObservedLocalAbilityEventCount { get; }

        /// <summary>获取current local actor最近一次权威ability identity。</summary>
        internal uint LastObservedLocalAbilityID { get; }

        /// <summary>
        /// 查找 current local actor state。
        /// </summary>
        internal bool TryGetLocalEntity(out ClientBattleEntityState entity)
        {
            for (var index = 0; index < Entities.Count; index++)
            {
                if (Entities[index].EntityID == LocalEntityID)
                {
                    entity = Entities[index];
                    return true;
                }
            }

            entity = null;
            return false;
        }
    }

    /// <summary>
    /// 唯一拥有 full/delta baseline、entity lifecycle 与 resync intent 的纯 C# owner。
    /// </summary>
    internal sealed class GameplayReplica
    {
        /// <summary>限制并行未完成 snapshot set，防止 partition flood。</summary>
        private const int MaximumPendingSets = 4;

        /// <summary>保护 generation、assembler、replica 与 resync state。</summary>
        private readonly object _sync = new object();

        /// <summary>保存冻结 profile policy。</summary>
        private readonly ClientBattlePolicy _policy;

        /// <summary>保存唯一 KCP resync command port。</summary>
        private readonly IClientBattleGameplayPort _network;

        /// <summary>保存 current entity states。</summary>
        private readonly SortedDictionary<ulong, ClientBattleEntityState> _entities =
            new SortedDictionary<ulong, ClientBattleEntityState>();

        /// <summary>保存已见 entity 的最大 lifecycle generation，包括已 despawn identity。</summary>
        private readonly Dictionary<ulong, uint> _lifecycleGenerations =
            new Dictionary<ulong, uint>();

        /// <summary>保存最多四个尚未完整的 partition set。</summary>
        private readonly Dictionary<SnapshotSetKey, PendingSnapshotSet> _pendingSets =
            new Dictionary<SnapshotSetKey, PendingSnapshotSet>();

        /// <summary>保存有界 reliable ability events，按 event identity 单调追加。</summary>
        private readonly Queue<ClientBattleAbilityEvent> _abilityEvents =
            new Queue<ClientBattleAbilityEvent>();

        /// <summary>保存current generation已接受的累计ability event数。</summary>
        private ulong _observedAbilityEventCount;

        /// <summary>保存current local actor已接受的累计ability event数。</summary>
        private ulong _observedLocalAbilityEventCount;

        /// <summary>保存current local actor最近一次权威ability identity。</summary>
        private uint _lastObservedLocalAbilityID;

        /// <summary>保存 current battle generation。</summary>
        private long _battleGeneration;

        /// <summary>保存 wire v1 actor_slot + 1 local identity。</summary>
        private ulong _localEntityID;

        /// <summary>保存已发布最新 server Tick。</summary>
        private ulong _serverTick;

        /// <summary>保存已发布最新 snapshot sequence。</summary>
        private ulong _snapshotSequence;

        /// <summary>保存 current full baseline identity。</summary>
        private ulong _baselineID;

        /// <summary>保存 current baseline 建立时的 server Tick。</summary>
        private ulong _baselineServerTick;

        /// <summary>保存 current baseline 已消费的 delta 数量。</summary>
        private int _baselineFanout;

        /// <summary>保存 current 完整 snapshot set 的 input acknowledgement。</summary>
        private ulong _lastProcessedInputTick;

        /// <summary>保存 ability/lifecycle 共用可靠序列的最后 identity。</summary>
        private ulong _lastReliableEventID;

        /// <summary>保存下一 resync request sequence。</summary>
        private ulong _nextResyncSequence = 1;

        /// <summary>保存 current single-flight resync；为空表示没有恢复请求。</summary>
        private ClientBattleResyncRequest _resyncRequest;

        /// <summary>保存 Scheduled response 指定、只能由 raw full 满足的 baseline。</summary>
        private ulong _scheduledBaselineID;

        /// <summary>保存 rate limited request 可再次发送的 Unix 毫秒。</summary>
        private long _resyncRetryAtMilliseconds;

        /// <summary>
        /// 创建不依赖 generated Protobuf 或 Unity object 的 replica owner。
        /// </summary>
        internal GameplayReplica(
            ClientBattlePolicy policy,
            IClientBattleGameplayPort network)
        {
            _policy = policy ?? throw new ArgumentNullException(nameof(policy));
            _network = network ?? throw new ArgumentNullException(nameof(network));
        }

        /// <summary>
        /// 以 authenticated actor slot 激活 successor generation。
        /// </summary>
        internal void Activate(long battleGeneration, int actorSlot)
        {
            if (battleGeneration <= 0 || actorSlot < 0 || actorSlot >= 8)
            {
                throw new ArgumentOutOfRangeException(nameof(battleGeneration));
            }

            lock (_sync)
            {
                if (battleGeneration <= _battleGeneration)
                {
                    throw new InvalidOperationException(
                        "GameplayReplica requires a successor battle generation.");
                }

                ResetLocked();
                _battleGeneration = battleGeneration;
                _localEntityID = checked((ulong)actorSlot + 1);
            }
        }

        /// <summary>
        /// 接受一个已验证 partition，并且仅在集合完整后原子发布。
        /// </summary>
        internal ClientBattleSnapshotCommit AcceptSnapshot(
            ClientBattleSnapshotPartition partition)
        {
            if (partition == null)
            {
                throw new ArgumentNullException(nameof(partition));
            }

            lock (_sync)
            {
                if (partition.BattleGeneration != _battleGeneration ||
                    _battleGeneration == 0)
                {
                    return ClientBattleSnapshotCommit.Duplicate;
                }

                if (partition.SnapshotSequence < _snapshotSequence ||
                    (partition.SnapshotSequence == _snapshotSequence &&
                     _snapshotSequence != 0))
                {
                    return ClientBattleSnapshotCommit.Duplicate;
                }

                var key = new SnapshotSetKey(partition);
                if (!_pendingSets.TryGetValue(key, out var pending))
                {
                    if (_pendingSets.Count >= MaximumPendingSets)
                    {
                        throw new ClientBattleReplicaProtocolException(
                            "Snapshot assembler exceeded its pending-set limit.");
                    }

                    pending = new PendingSnapshotSet(partition);
                    _pendingSets.Add(key, pending);
                }

                var accepted = pending.TryAdd(partition);
                if (accepted == PartitionAddResult.Conflict)
                {
                    throw new ClientBattleReplicaProtocolException(
                        "Snapshot partition conflicts with an existing partition.");
                }

                if (accepted == PartitionAddResult.Duplicate)
                {
                    return ClientBattleSnapshotCommit.Duplicate;
                }

                if (!pending.Complete)
                {
                    return ClientBattleSnapshotCommit.Pending;
                }

                _pendingSets.Remove(key);
                var ordered = pending.Ordered();
                if (partition.Kind == ClientBattleSnapshotKind.Full)
                {
                    ApplyFullLocked(ordered);
                    return ClientBattleSnapshotCommit.Published;
                }

                var reason = CanApplyDeltaLocked(ordered[0]);
                if (reason.HasValue)
                {
                    RequestResyncLocked(reason.Value, ordered[0].BaselineID);
                    return ClientBattleSnapshotCommit.ResyncRequested;
                }

                return ApplyDeltaLocked(ordered)
                    ? ClientBattleSnapshotCommit.Published
                    : ClientBattleSnapshotCommit.ResyncRequested;
            }
        }

        /// <summary>
        /// 按 reliable route 顺序提交 entity lifecycle。
        /// </summary>
        internal void AcceptLifecycle(ClientBattleEntityLifecycle lifecycle)
        {
            if (lifecycle == null)
            {
                throw new ArgumentNullException(nameof(lifecycle));
            }

            lock (_sync)
            {
                if (lifecycle.BattleGeneration != _battleGeneration ||
                    _battleGeneration == 0 ||
                    lifecycle.EventID <= _lastReliableEventID)
                {
                    return;
                }

                if (_lastReliableEventID != 0 &&
                    lifecycle.EventID != _lastReliableEventID + 1)
                {
                    RequestResyncLocked(
                        ClientBattleResyncReason.EntityGenerationGap,
                        _baselineID);
                    return;
                }

                _lifecycleGenerations.TryGetValue(
                    lifecycle.EntityID,
                    out var knownGeneration);
                if (lifecycle.Kind == ClientBattleEntityLifecycleKind.Spawn)
                {
                    if (_entities.TryGetValue(
                            lifecycle.EntityID,
                            out var existing))
                    {
                        if (existing.Generation !=
                                lifecycle.EntityGeneration ||
                            !EntityIdentityEquivalent(
                                existing,
                                lifecycle.InitialState))
                        {
                            RequestResyncLocked(
                                ClientBattleResyncReason.EntityGenerationGap,
                                _baselineID);
                            return;
                        }
                    }
                    else
                    {
                        var expected = knownGeneration == 0
                            ? 1U
                            : checked(knownGeneration + 1U);
                        if (lifecycle.EntityGeneration != expected ||
                            _entities.Count >= _policy.MaximumEntities)
                        {
                            RequestResyncLocked(
                                ClientBattleResyncReason.EntityGenerationGap,
                                _baselineID);
                            return;
                        }

                        _entities.Add(
                            lifecycle.EntityID,
                            lifecycle.InitialState);
                    }
                }
                else
                {
                    if (knownGeneration != lifecycle.EntityGeneration ||
                        (_entities.TryGetValue(
                             lifecycle.EntityID,
                             out var current) &&
                         current.Generation != lifecycle.EntityGeneration))
                    {
                        RequestResyncLocked(
                            ClientBattleResyncReason.EntityGenerationGap,
                            _baselineID);
                        return;
                    }

                    _entities.Remove(lifecycle.EntityID);
                }

                _lifecycleGenerations[lifecycle.EntityID] =
                    lifecycle.EntityGeneration;
                _lastReliableEventID = lifecycle.EventID;
                _serverTick = Math.Max(_serverTick, lifecycle.ServerTick);
            }
        }

        /// <summary>
        /// 按 reliable route 顺序提交有界 ability event。
        /// </summary>
        internal void AcceptAbilityEvent(ClientBattleAbilityEvent abilityEvent)
        {
            if (abilityEvent == null)
            {
                throw new ArgumentNullException(nameof(abilityEvent));
            }

            lock (_sync)
            {
                if (abilityEvent.BattleGeneration != _battleGeneration ||
                    _battleGeneration == 0 ||
                    abilityEvent.EventID <= _lastReliableEventID)
                {
                    return;
                }

                if (_lastReliableEventID != 0 &&
                    abilityEvent.EventID != _lastReliableEventID + 1)
                {
                    RequestResyncLocked(
                        ClientBattleResyncReason.DeltaGap,
                        _baselineID);
                    return;
                }

                if (!_entities.TryGetValue(
                        abilityEvent.SourceEntityID,
                        out var source) ||
                    source.Generation !=
                        abilityEvent.SourceEntityGeneration)
                {
                    RequestResyncLocked(
                        ClientBattleResyncReason.EntityGenerationGap,
                        _baselineID);
                    return;
                }

                for (var index = 0;
                     index < abilityEvent.TargetEntityIDs.Count;
                     index++)
                {
                    if (!_entities.ContainsKey(
                            abilityEvent.TargetEntityIDs[index]))
                    {
                        RequestResyncLocked(
                            ClientBattleResyncReason.EntityGenerationGap,
                            _baselineID);
                        return;
                    }
                }

                if (_abilityEvents.Count >= _policy.MaximumGameplayCues)
                {
                    throw new ClientBattleReplicaProtocolException(
                        "Reliable ability event queue reached its hard limit.");
                }

                _abilityEvents.Enqueue(abilityEvent);
                _observedAbilityEventCount = checked(
                    _observedAbilityEventCount + 1);
                if (abilityEvent.SourceEntityID == _localEntityID)
                {
                    _observedLocalAbilityEventCount = checked(
                        _observedLocalAbilityEventCount + 1);
                    _lastObservedLocalAbilityID = abilityEvent.AbilityID;
                }
                _lastReliableEventID = abilityEvent.EventID;
                _serverTick = Math.Max(_serverTick, abilityEvent.ServerTick);
            }
        }

        /// <summary>
        /// 应用 current route 3007 response，不从 KCP response 读取 full snapshot。
        /// </summary>
        internal void AcceptResyncResponse(
            ClientBattleResyncResponse response,
            long utcNowMilliseconds)
        {
            if (response == null)
            {
                throw new ArgumentNullException(nameof(response));
            }

            lock (_sync)
            {
                if (_resyncRequest == null ||
                    response.BattleGeneration != _battleGeneration ||
                    response.RequestSequence != _resyncRequest.RequestSequence)
                {
                    return;
                }

                _serverTick = Math.Max(_serverTick, response.ServerTick);
                switch (response.Disposition)
                {
                    case ClientBattleResyncDisposition.Scheduled:
                        _scheduledBaselineID = response.ScheduledBaselineID;
                        _resyncRetryAtMilliseconds = 0;
                        break;
                    case ClientBattleResyncDisposition.RateLimited:
                        _scheduledBaselineID = 0;
                        _resyncRetryAtMilliseconds = checked(
                            utcNowMilliseconds +
                            response.RetryAfterMilliseconds);
                        break;
                    case ClientBattleResyncDisposition.StaleRequest:
                        _resyncRequest = null;
                        _scheduledBaselineID = 0;
                        _resyncRetryAtMilliseconds = 0;
                        break;
                    default:
                        throw new ClientBattleReplicaProtocolException(
                            "Resync response disposition is unknown.");
                }
            }
        }

        /// <summary>
        /// 在 rate-limit deadline 后以新 sequence 重发 current single-flight intent。
        /// </summary>
        internal bool TickResync(long utcNowMilliseconds)
        {
            lock (_sync)
            {
                if (_resyncRequest == null ||
                    _resyncRetryAtMilliseconds == 0 ||
                    utcNowMilliseconds < _resyncRetryAtMilliseconds)
                {
                    return false;
                }

                var retry = CreateResyncLocked(
                    _resyncRequest.Reason,
                    _resyncRequest.MissingBaselineID);
                _resyncRequest = retry;
                _resyncRetryAtMilliseconds = 0;
                return _network.TryRequestResync(retry);
            }
        }

        /// <summary>
        /// 读取并清除 current ability event queue，供纯 projector 去重投影。
        /// </summary>
        internal IReadOnlyList<ClientBattleAbilityEvent> DrainAbilityEvents(
            long battleGeneration)
        {
            lock (_sync)
            {
                if (battleGeneration != _battleGeneration ||
                    _abilityEvents.Count == 0)
                {
                    return Array.Empty<ClientBattleAbilityEvent>();
                }

                var events = _abilityEvents.ToArray();
                _abilityEvents.Clear();
                return new ReadOnlyCollection<ClientBattleAbilityEvent>(events);
            }
        }

        /// <summary>
        /// 读取按 identity 排序的 immutable replica snapshot。
        /// </summary>
        internal ClientGameplayReplicaSnapshot Snapshot()
        {
            lock (_sync)
            {
                var entities = new ClientBattleEntityState[_entities.Count];
                _entities.Values.CopyTo(entities, 0);
                var events = _abilityEvents.ToArray();
                return new ClientGameplayReplicaSnapshot(
                    _battleGeneration,
                    _localEntityID,
                    _serverTick,
                    _snapshotSequence,
                    _baselineID,
                    _lastProcessedInputTick,
                    _resyncRequest != null,
                    new ReadOnlyCollection<ClientBattleEntityState>(entities),
                    new ReadOnlyCollection<ClientBattleAbilityEvent>(events),
                    _observedAbilityEventCount,
                    _observedLocalAbilityEventCount,
                    _lastObservedLocalAbilityID);
            }
        }

        /// <summary>
        /// 结束 current generation并清除 assembler、baseline 与 reliable event。
        /// </summary>
        internal void Deactivate(long battleGeneration)
        {
            lock (_sync)
            {
                if (battleGeneration != _battleGeneration)
                {
                    return;
                }

                ResetLocked();
                _battleGeneration = 0;
                _localEntityID = 0;
            }
        }

        /// <summary>
        /// 原子建立 full baseline并验证 local actor identity。
        /// </summary>
        private void ApplyFullLocked(
            IReadOnlyList<ClientBattleSnapshotPartition> partitions)
        {
            var replacement =
                new SortedDictionary<ulong, ClientBattleEntityState>();
            foreach (var partition in partitions)
            {
                foreach (var entity in partition.Entities)
                {
                    if (replacement.Count >= _policy.MaximumEntities ||
                        replacement.ContainsKey(entity.EntityID))
                    {
                        throw new ClientBattleReplicaProtocolException(
                            "Full snapshot contains duplicate or excessive entities.");
                    }

                    if (_lifecycleGenerations.TryGetValue(
                            entity.EntityID,
                            out var knownGeneration) &&
                        entity.Generation < knownGeneration)
                    {
                        throw new ClientBattleReplicaProtocolException(
                            "Full snapshot regressed an entity generation.");
                    }

                    if (_entities.TryGetValue(
                            entity.EntityID,
                            out var current) &&
                        current.Generation == entity.Generation &&
                        (current.ArchetypeID != entity.ArchetypeID ||
                         current.MaxHealthMilli != entity.MaxHealthMilli))
                    {
                        throw new ClientBattleReplicaProtocolException(
                            "Full snapshot changed immutable entity content: " +
                            $"entity_id={entity.EntityID}, " +
                            $"generation={entity.Generation}, " +
                            $"current_archetype_id={current.ArchetypeID}, " +
                            $"incoming_archetype_id={entity.ArchetypeID}, " +
                            $"current_max_health_milli={current.MaxHealthMilli}, " +
                            $"incoming_max_health_milli={entity.MaxHealthMilli}.");
                    }

                    replacement.Add(entity.EntityID, entity);
                }
            }

            if (!replacement.TryGetValue(
                    _localEntityID,
                    out var local) ||
                local.ArchetypeID !=
                    ClientBattleContentIdentity.PlayerArchetype)
            {
                throw new ClientBattleReplicaProtocolException(
                    "Full snapshot does not contain the mapped local player.");
            }

            var bossCount = 0;
            foreach (var entity in replacement.Values)
            {
                if (entity.ArchetypeID ==
                    ClientBattleContentIdentity.BossArchetype)
                {
                    bossCount++;
                }
            }

            if (bossCount > 1)
            {
                throw new ClientBattleReplicaProtocolException(
                    "Full snapshot contains conflicting Boss entities.");
            }

            var first = partitions[0];
            if (_scheduledBaselineID != 0 &&
                first.BaselineID != _scheduledBaselineID)
            {
                throw new ClientBattleReplicaProtocolException(
                    "Scheduled resync baseline identity drifted.");
            }

            _entities.Clear();
            foreach (var pair in replacement)
            {
                _entities.Add(pair.Key, pair.Value);
                _lifecycleGenerations[pair.Key] = pair.Value.Generation;
            }

            _serverTick = first.ServerTick;
            _snapshotSequence = first.SnapshotSequence;
            _baselineID = first.BaselineID;
            _baselineServerTick = first.ServerTick;
            _baselineFanout = 0;
            _lastProcessedInputTick = first.LastProcessedInputTick;
            _pendingSets.Clear();
            _resyncRequest = null;
            _scheduledBaselineID = 0;
            _resyncRetryAtMilliseconds = 0;
        }

        /// <summary>
        /// 判断 delta 是否仍可安全引用 current baseline。
        /// </summary>
        private ClientBattleResyncReason? CanApplyDeltaLocked(
            ClientBattleSnapshotPartition first)
        {
            if (_baselineID == 0 || first.BaselineID != _baselineID)
            {
                return ClientBattleResyncReason.MissingBaseline;
            }

            if (first.ServerTick < _baselineServerTick ||
                first.ServerTick - _baselineServerTick >
                    (ulong)_policy.MaximumBaselineAgeTicks ||
                _baselineFanout >= _policy.MaximumBaselineFanout)
            {
                return ClientBattleResyncReason.BaselineExpired;
            }

            if (_snapshotSequence != 0 &&
                first.SnapshotSequence > _snapshotSequence + 1)
            {
                return ClientBattleResyncReason.DeltaGap;
            }

            return null;
        }

        /// <summary>
        /// 在临时副本完整校验全部 delta 后一次替换 current state。
        /// </summary>
        private bool ApplyDeltaLocked(
            IReadOnlyList<ClientBattleSnapshotPartition> partitions)
        {
            var replacement =
                new SortedDictionary<ulong, ClientBattleEntityState>(_entities);
            var seen = new HashSet<ulong>();
            foreach (var partition in partitions)
            {
                foreach (var delta in partition.Deltas)
                {
                    if (!seen.Add(delta.EntityID))
                    {
                        throw new ClientBattleReplicaProtocolException(
                            "Delta snapshot contains a duplicate entity.");
                    }

                    if (!replacement.TryGetValue(delta.EntityID, out var current) ||
                        current.Generation != delta.Generation)
                    {
                        RequestResyncLocked(
                            ClientBattleResyncReason.EntityGenerationGap,
                            _baselineID);
                        return false;
                    }

                    replacement[delta.EntityID] = current.With(
                        delta.Transform,
                        delta.HealthMilli,
                        delta.StateFlags,
                        delta.EquippedWeaponID);
                }
            }

            var first = partitions[0];
            _entities.Clear();
            foreach (var pair in replacement)
            {
                _entities.Add(pair.Key, pair.Value);
            }

            _serverTick = first.ServerTick;
            _snapshotSequence = first.SnapshotSequence;
            _lastProcessedInputTick = first.LastProcessedInputTick;
            _baselineFanout++;
            return true;
        }

        /// <summary>
        /// 建立或保留唯一 current resync intent。
        /// </summary>
        private void RequestResyncLocked(
            ClientBattleResyncReason reason,
            ulong missingBaselineID)
        {
            if (_resyncRequest != null)
            {
                return;
            }

            var request = CreateResyncLocked(reason, missingBaselineID);
            _resyncRequest = request;
            if (!_network.TryRequestResync(request))
            {
                throw new ClientBattleReplicaProtocolException(
                    "Resync request could not enter the bounded KCP queue.");
            }
        }

        /// <summary>
        /// 分配下一 resync sequence。
        /// </summary>
        private ClientBattleResyncRequest CreateResyncLocked(
            ClientBattleResyncReason reason,
            ulong missingBaselineID)
        {
            if (_nextResyncSequence == 0)
            {
                throw new ClientBattleReplicaProtocolException(
                    "Resync sequence is exhausted.");
            }

            var sequence = _nextResyncSequence;
            _nextResyncSequence =
                sequence == ulong.MaxValue ? 0 : sequence + 1;
            return new ClientBattleResyncRequest(
                _battleGeneration,
                sequence,
                _serverTick,
                missingBaselineID,
                _snapshotSequence,
                reason);
        }

        /// <summary>
        /// 清除 current generation 的全部 mutable state。
        /// </summary>
        private void ResetLocked()
        {
            _entities.Clear();
            _lifecycleGenerations.Clear();
            _pendingSets.Clear();
            _abilityEvents.Clear();
            _observedAbilityEventCount = 0;
            _observedLocalAbilityEventCount = 0;
            _lastObservedLocalAbilityID = 0;
            _serverTick = 0;
            _snapshotSequence = 0;
            _baselineID = 0;
            _baselineServerTick = 0;
            _baselineFanout = 0;
            _lastProcessedInputTick = 0;
            _lastReliableEventID = 0;
            _nextResyncSequence = 1;
            _resyncRequest = null;
            _scheduledBaselineID = 0;
            _resyncRetryAtMilliseconds = 0;
        }

        /// <summary>
        /// 比较 full baseline 与迟到 spawn initial state 是否表达同一 entity identity。
        /// full 可能已推进 transform、health 与 state，不能用 mutable state 否定可靠 spawn。
        /// </summary>
        private static bool EntityIdentityEquivalent(
            ClientBattleEntityState left,
            ClientBattleEntityState right)
        {
            return left != null &&
                   right != null &&
                   left.EntityID == right.EntityID &&
                   left.Generation == right.Generation &&
                   left.ArchetypeID == right.ArchetypeID &&
                   left.MaxHealthMilli == right.MaxHealthMilli;
        }

        /// <summary>
        /// 标识一个 snapshot partition set 的全部一致性字段。
        /// </summary>
        private readonly struct SnapshotSetKey : IEquatable<SnapshotSetKey>
        {
            /// <summary>
            /// 从单个 partition 冻结集合 identity。
            /// </summary>
            internal SnapshotSetKey(ClientBattleSnapshotPartition partition)
            {
                BattleGeneration = partition.BattleGeneration;
                Kind = partition.Kind;
                ServerTick = partition.ServerTick;
                SnapshotSequence = partition.SnapshotSequence;
                BaselineID = partition.BaselineID;
                PartitionCount = partition.PartitionCount;
                LastProcessedInputTick = partition.LastProcessedInputTick;
            }

            /// <summary>获取 battle generation。</summary>
            internal long BattleGeneration { get; }

            /// <summary>获取 full/delta kind。</summary>
            internal ClientBattleSnapshotKind Kind { get; }

            /// <summary>获取共享 server Tick。</summary>
            internal ulong ServerTick { get; }

            /// <summary>获取共享 snapshot sequence。</summary>
            internal ulong SnapshotSequence { get; }

            /// <summary>获取共享 baseline identity。</summary>
            internal ulong BaselineID { get; }

            /// <summary>获取共享 partition count。</summary>
            internal int PartitionCount { get; }

            /// <summary>获取共享 acknowledgement。</summary>
            internal ulong LastProcessedInputTick { get; }

            /// <inheritdoc />
            public bool Equals(SnapshotSetKey other)
            {
                return BattleGeneration == other.BattleGeneration &&
                       Kind == other.Kind &&
                       ServerTick == other.ServerTick &&
                       SnapshotSequence == other.SnapshotSequence &&
                       BaselineID == other.BaselineID &&
                       PartitionCount == other.PartitionCount &&
                       LastProcessedInputTick == other.LastProcessedInputTick;
            }

            /// <inheritdoc />
            public override bool Equals(object obj)
            {
                return obj is SnapshotSetKey other && Equals(other);
            }

            /// <inheritdoc />
            public override int GetHashCode()
            {
                unchecked
                {
                    var hash = BattleGeneration.GetHashCode();
                    hash = (hash * 397) ^ (int)Kind;
                    hash = (hash * 397) ^ ServerTick.GetHashCode();
                    hash = (hash * 397) ^ SnapshotSequence.GetHashCode();
                    hash = (hash * 397) ^ BaselineID.GetHashCode();
                    hash = (hash * 397) ^ PartitionCount;
                    hash = (hash * 397) ^ LastProcessedInputTick.GetHashCode();
                    return hash;
                }
            }
        }

        /// <summary>
        /// 标识 partition 插入 pending set 的结果。
        /// </summary>
        private enum PartitionAddResult
        {
            /// <summary>新 partition 已接受。</summary>
            Added = 1,

            /// <summary>Exact partition 已幂等存在。</summary>
            Duplicate = 2,

            /// <summary>同 index 内容冲突。</summary>
            Conflict = 3,
        }

        /// <summary>
        /// 拥有一个尚未完整 snapshot set 的固定 slot。
        /// </summary>
        private sealed class PendingSnapshotSet
        {
            /// <summary>保存按 partition index 定位的固定 slots。</summary>
            private readonly ClientBattleSnapshotPartition[] _partitions;

            /// <summary>保存已填充 slot 数。</summary>
            private int _count;

            /// <summary>
            /// 创建与首个 partition count 相同的固定集合。
            /// </summary>
            internal PendingSnapshotSet(ClientBattleSnapshotPartition first)
            {
                _partitions =
                    new ClientBattleSnapshotPartition[first.PartitionCount];
            }

            /// <summary>获取集合是否全部到齐。</summary>
            internal bool Complete => _count == _partitions.Length;

            /// <summary>
            /// 接受新 partition 或验证 exact duplicate。
            /// </summary>
            internal PartitionAddResult TryAdd(
                ClientBattleSnapshotPartition partition)
            {
                var existing = _partitions[partition.PartitionIndex];
                if (existing != null)
                {
                    return Equivalent(existing, partition)
                        ? PartitionAddResult.Duplicate
                        : PartitionAddResult.Conflict;
                }

                _partitions[partition.PartitionIndex] = partition;
                _count++;
                return PartitionAddResult.Added;
            }

            /// <summary>
            /// 返回已完整且按 index 排序的 slots。
            /// </summary>
            internal IReadOnlyList<ClientBattleSnapshotPartition> Ordered()
            {
                if (!Complete)
                {
                    throw new InvalidOperationException(
                        "Pending snapshot set is incomplete.");
                }

                return _partitions;
            }

            /// <summary>
            /// 比较 duplicate partition 的全部 payload values。
            /// </summary>
            private static bool Equivalent(
                ClientBattleSnapshotPartition first,
                ClientBattleSnapshotPartition second)
            {
                if (first.Entities.Count != second.Entities.Count ||
                    first.Deltas.Count != second.Deltas.Count)
                {
                    return false;
                }

                for (var index = 0; index < first.Entities.Count; index++)
                {
                    var left = first.Entities[index];
                    var right = second.Entities[index];
                    if (left.EntityID != right.EntityID ||
                        left.Generation != right.Generation ||
                        left.HealthMilli != right.HealthMilli ||
                        left.StateFlags != right.StateFlags ||
                        !TransformEqual(left.Transform, right.Transform))
                    {
                        return false;
                    }
                }

                for (var index = 0; index < first.Deltas.Count; index++)
                {
                    var left = first.Deltas[index];
                    var right = second.Deltas[index];
                    if (left.EntityID != right.EntityID ||
                        left.Generation != right.Generation ||
                        left.StateMask != right.StateMask ||
                        left.HealthMilli != right.HealthMilli ||
                        left.StateFlags != right.StateFlags ||
                        left.Transform.HasValue != right.Transform.HasValue ||
                        (left.Transform.HasValue &&
                         !TransformEqual(
                             left.Transform.Value,
                             right.Transform.Value)))
                    {
                        return false;
                    }
                }

                return true;
            }

            /// <summary>
            /// 比较量化 transform 的全部字段。
            /// </summary>
            private static bool TransformEqual(
                ClientBattleTransform first,
                ClientBattleTransform second)
            {
                return first.PositionXMillimeters == second.PositionXMillimeters &&
                       first.PositionYMillimeters == second.PositionYMillimeters &&
                       first.PositionZMillimeters == second.PositionZMillimeters &&
                       first.YawMillidegrees == second.YawMillidegrees &&
                       first.VelocityXMillimetersPerSecond ==
                           second.VelocityXMillimetersPerSecond &&
                       first.VelocityYMillimetersPerSecond ==
                           second.VelocityYMillimetersPerSecond &&
                       first.VelocityZMillimetersPerSecond ==
                           second.VelocityZMillimetersPerSecond;
            }
        }
    }

    /// <summary>
    /// 表示 replica 的 partition/baseline/lifecycle 契约失败。
    /// </summary>
    internal sealed class ClientBattleReplicaProtocolException : Exception
    {
        /// <summary>
        /// 创建不包含 payload 或 remote identity 的稳定协议错误。
        /// </summary>
        internal ClientBattleReplicaProtocolException(string message)
            : base(message)
        {
        }
    }
}
