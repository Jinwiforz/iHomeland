using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Control;
using IHomeland.Client.Application.Gameplay;
using IHomeland.Client.Core.Lifetime;
using IHomeland.Client.Infrastructure.Http;
using IHomeland.Client.Infrastructure.WebSocket;
using IHomeland.Protocol.World.V1;

namespace IHomeland.Client.Application.World
{
    /// <summary>
    /// 唯一拥有 primary PersonalWorld、current world target 与 assignment 收敛提示。
    /// </summary>
    /// <remarks>
    /// Generated message 仅在 callback 栈内存在；提交前始终复制为不可变投影。
    /// Mutable state 由 <see cref="_sync"/> 保护，subscriber 在锁外逐个通知。
    /// </remarks>
    internal sealed class PersonalWorldService : IAppLifetimeParticipant
    {
        /// <summary>保护 snapshot、订阅状态与停止门。</summary>
        private readonly object _sync = new object();

        /// <summary>提供 assignment control hint；测试可不连接 channel。</summary>
        private readonly ClientControlChannel _controlChannel;

        /// <summary>提供完整 world replacement；测试可不连接 channel。</summary>
        private readonly ClientGameplayChannel _gameplayChannel;

        /// <summary>为 primary、current 与最近 retired world 保留已见最高 assignment generation。</summary>
        private readonly Dictionary<string, ClientWorldAssignmentProjection> _highestAssignments =
            new Dictionary<string, ClientWorldAssignmentProjection>(StringComparer.Ordinal);

        /// <summary>记录保留 world 中已由完整快照撤销 assignment 的 generation tombstone。</summary>
        private readonly HashSet<string> _clearedAssignmentWorlds =
            new HashSet<string>(StringComparer.Ordinal);

        /// <summary>保留最近清除 target 的 world identity，使 assignment 历史始终至多覆盖三个 world。</summary>
        private string _retiredPersonalWorldID;

        /// <summary>记录是否已登记 channel subscriber。</summary>
        private bool _subscribed;

        /// <summary>停止后永久拒绝迟到 response/PUSH。</summary>
        private bool _stopped;

        /// <summary>保存最新不可变投影快照。</summary>
        private ClientPersonalWorldServiceSnapshot _snapshot =
            new ClientPersonalWorldServiceSnapshot(null, null, null, false);

        /// <summary>创建可独立测试且无网络副作用的 Service。</summary>
        internal PersonalWorldService()
            : this(null, null)
        {
        }

        /// <summary>创建由既有强类型 channel 驱动的 Service。</summary>
        /// <param name="controlChannel">WSS control owner。</param>
        /// <param name="gameplayChannel">TLS/TCP gameplay owner。</param>
        internal PersonalWorldService(
            ClientControlChannel controlChannel,
            ClientGameplayChannel gameplayChannel)
        {
            _controlChannel = controlChannel;
            _gameplayChannel = gameplayChannel;
        }

        /// <summary>在不可变 snapshot 已提交后通知消费者。</summary>
        internal event Action<ClientPersonalWorldServiceSnapshot> Changed;

        /// <summary>在同 revision/generation 内容冲突时通知 flow 关闭 mutation gate。</summary>
        internal event Action ProjectionConflict;

        /// <summary>获取不含 credential 或 generated message 的当前快照。</summary>
        internal ClientPersonalWorldServiceSnapshot Snapshot
        {
            get
            {
                lock (_sync)
                {
                    return _snapshot;
                }
            }
        }

        /// <summary>只登记现有 channel subscriber，不发起任何网络操作。</summary>
        /// <param name="cancellationToken">AppLifetime 初始化信号。</param>
        /// <returns>订阅完成的任务。</returns>
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            lock (_sync)
            {
                if (_stopped || _subscribed)
                {
                    throw new InvalidOperationException("PersonalWorldService 不能重复初始化或停止后重启。");
                }

                if (_controlChannel != null)
                {
                    _controlChannel.PushReceived += OnControlPush;
                }

                if (_gameplayChannel != null)
                {
                    _gameplayChannel.WorldSnapshotReceived += OnWorldSnapshotPush;
                }

                _subscribed = true;
            }

            return Task.CompletedTask;
        }

        /// <summary>提交 HTTP primary world bootstrap。</summary>
        /// <param name="bootstrap">Session owner 验证后的 bootstrap。</param>
        /// <returns>稳定 revision gate 结果。</returns>
        internal ClientProjectionApplyResult ApplyBootstrap(ClientWorldBootstrap bootstrap)
        {
            ClientPersonalWorldProjection incoming;
            try
            {
                incoming = ClientWorldProjectionMapper.FromBootstrap(bootstrap);
            }
            catch (ClientWorldProjectionException)
            {
                return ClientProjectionApplyResult.Rejected;
            }

            return ApplyPrimary(incoming);
        }

        /// <summary>提交 TLS/TCP 完整 world replacement。</summary>
        /// <param name="snapshot">Generated world snapshot。</param>
        /// <returns>稳定 revision gate 结果。</returns>
        internal ClientProjectionApplyResult ApplyWorldSnapshot(WorldSnapshot snapshot)
        {
            ClientPersonalWorldProjection incoming;
            try
            {
                incoming = ClientWorldProjectionMapper.FromWorldSnapshot(snapshot);
            }
            catch (ClientWorldProjectionException)
            {
                return ClientProjectionApplyResult.Rejected;
            }

            ClientPersonalWorldServiceSnapshot committed = null;
            ClientProjectionApplyResult result;
            lock (_sync)
            {
                if (_stopped)
                {
                    return ClientProjectionApplyResult.Rejected;
                }

                result = Compare(_snapshot.CurrentWorld, incoming);
                if (result == ClientProjectionApplyResult.Applied)
                {
                    result = ValidateAssignmentGenerationLocked(incoming.Assignment);
                }

                if (result == ClientProjectionApplyResult.Applied)
                {
                    var primary = _snapshot.PrimaryWorld;
                    if (primary != null &&
                        string.Equals(primary.PersonalWorldID, incoming.PersonalWorldID, StringComparison.Ordinal) &&
                        Compare(primary, incoming) == ClientProjectionApplyResult.Applied)
                    {
                        primary = incoming;
                    }

                    committed = new ClientPersonalWorldServiceSnapshot(
                        primary,
                        incoming,
                        null,
                        false);
                    _snapshot = committed;
                    RememberAssignmentLocked(incoming.PersonalWorldID, incoming.Assignment);
                    PruneAssignmentHistoryLocked();
                }
                else if (result == ClientProjectionApplyResult.Duplicate && _snapshot.NeedsRefresh)
                {
                    // 健康 channel 返回的等价完整 replacement 已重新确认 current target；
                    // 不替换 projection 对象，只结束 control-only refresh gate。
                    committed = new ClientPersonalWorldServiceSnapshot(
                        _snapshot.PrimaryWorld,
                        _snapshot.CurrentWorld,
                        null,
                        false);
                    _snapshot = committed;
                }
                else if (result == ClientProjectionApplyResult.Conflict)
                {
                    committed = MarkRefreshLocked();
                }
            }

            Notify(committed, result == ClientProjectionApplyResult.Conflict);
            return result;
        }

        /// <summary>应用 WSS assignment control hint，但不替代完整 gameplay snapshot。</summary>
        /// <param name="push">Generated assignment changed PUSH。</param>
        /// <returns>稳定 generation gate 结果。</returns>
        internal ClientProjectionApplyResult ApplyAssignmentHint(WorldAssignmentChangedPush push)
        {
            ClientWorldAssignmentProjection incoming;
            string worldID;
            try
            {
                incoming = ClientWorldProjectionMapper.FromAssignmentHint(push, out worldID);
            }
            catch (ClientWorldProjectionException)
            {
                return ClientProjectionApplyResult.Rejected;
            }

            ClientPersonalWorldServiceSnapshot committed = null;
            var conflict = false;
            ClientProjectionApplyResult result;
            lock (_sync)
            {
                if (_stopped || !MatchesKnownWorldLocked(worldID))
                {
                    return ClientProjectionApplyResult.Rejected;
                }

                var current = _snapshot.AssignmentHint ?? FindKnownAssignmentLocked(worldID);
                result = CompareAssignment(current, incoming);
                if (result == ClientProjectionApplyResult.Applied)
                {
                    committed = new ClientPersonalWorldServiceSnapshot(
                        _snapshot.PrimaryWorld,
                        _snapshot.CurrentWorld,
                        incoming,
                        true);
                    _snapshot = committed;
                    if (incoming != null)
                    {
                        RememberAssignmentLocked(worldID, incoming);
                    }
                }
                else if (result == ClientProjectionApplyResult.Conflict)
                {
                    committed = MarkRefreshLocked();
                    conflict = true;
                }
            }

            Notify(committed, conflict);
            return result;
        }

        /// <summary>
        /// 由 coordinator 在旧 gameplay generation 关闭后清除 current target，不影响 primary world 或 generation gate。
        /// </summary>
        internal void ClearCurrentTarget()
        {
            ClientPersonalWorldServiceSnapshot committed;
            lock (_sync)
            {
                if (_stopped || _snapshot.CurrentWorld == null && _snapshot.AssignmentHint == null)
                {
                    return;
                }

                committed = new ClientPersonalWorldServiceSnapshot(
                    _snapshot.PrimaryWorld,
                    null,
                    null,
                    false);
                _retiredPersonalWorldID = _snapshot.CurrentWorld?.PersonalWorldID;
                _snapshot = committed;
                PruneAssignmentHistoryLocked();
            }

            Changed?.Invoke(committed);
        }

        /// <summary>在control generation断开时退役旧hint并冻结依赖其完整性的能力。</summary>
        /// <remarks>Current gameplay完整投影保持可读；新control连接不能继承旧generation的hint。</remarks>
        internal void InvalidateControlProjection()
        {
            ClientPersonalWorldServiceSnapshot committed;
            lock (_sync)
            {
                if (_stopped)
                {
                    return;
                }

                committed = new ClientPersonalWorldServiceSnapshot(
                    _snapshot.PrimaryWorld,
                    _snapshot.CurrentWorld,
                    null,
                    _snapshot.CurrentWorld != null);
                _snapshot = committed;
            }

            Changed?.Invoke(committed);
        }

        /// <summary>解除 subscriber 并永久拒绝迟到输入。</summary>
        /// <param name="cancellationToken">停止信号；本地解除订阅不阻塞。</param>
        /// <returns>停止完成的任务。</returns>
        public Task StopAsync(CancellationToken cancellationToken)
        {
            lock (_sync)
            {
                if (_stopped)
                {
                    return Task.CompletedTask;
                }

                _stopped = true;
                if (_subscribed)
                {
                    if (_controlChannel != null)
                    {
                        _controlChannel.PushReceived -= OnControlPush;
                    }

                    if (_gameplayChannel != null)
                    {
                        _gameplayChannel.WorldSnapshotReceived -= OnWorldSnapshotPush;
                    }

                    _subscribed = false;
                }
            }

            return Task.CompletedTask;
        }

        /// <summary>应用 primary world revision gate。</summary>
        /// <param name="incoming">已验证不可变投影。</param>
        /// <returns>稳定 gate 结果。</returns>
        private ClientProjectionApplyResult ApplyPrimary(ClientPersonalWorldProjection incoming)
        {
            ClientPersonalWorldServiceSnapshot committed = null;
            ClientProjectionApplyResult result;
            lock (_sync)
            {
                if (_stopped)
                {
                    return ClientProjectionApplyResult.Rejected;
                }

                result = Compare(_snapshot.PrimaryWorld, incoming);
                if (result == ClientProjectionApplyResult.Applied)
                {
                    result = ValidateAssignmentGenerationLocked(incoming.Assignment);
                }

                if (result == ClientProjectionApplyResult.Applied)
                {
                    committed = new ClientPersonalWorldServiceSnapshot(
                        incoming,
                        _snapshot.CurrentWorld,
                        null,
                        false);
                    _snapshot = committed;
                    RememberAssignmentLocked(incoming.PersonalWorldID, incoming.Assignment);
                    PruneAssignmentHistoryLocked();
                }
                else if (result == ClientProjectionApplyResult.Conflict)
                {
                    committed = MarkRefreshLocked();
                }
            }

            Notify(committed, result == ClientProjectionApplyResult.Conflict);
            return result;
        }

        /// <summary>比较同一 PersonalWorld 的 revision 与完整语义。</summary>
        /// <param name="current">当前投影。</param>
        /// <param name="incoming">新投影。</param>
        /// <returns>稳定 gate 结果。</returns>
        private static ClientProjectionApplyResult Compare(
            ClientPersonalWorldProjection current,
            ClientPersonalWorldProjection incoming)
        {
            if (current == null)
            {
                return ClientProjectionApplyResult.Applied;
            }

            if (!string.Equals(current.PersonalWorldID, incoming.PersonalWorldID, StringComparison.Ordinal))
            {
                return ClientProjectionApplyResult.Conflict;
            }

            if (incoming.Revision < current.Revision)
            {
                return ClientProjectionApplyResult.Stale;
            }

            if (incoming.Revision == current.Revision)
            {
                if (!current.HasSameWorldFacts(incoming))
                {
                    return ClientProjectionApplyResult.Conflict;
                }

                // PersonalWorld revision 与 runtime assignment generation 是两个独立单调门；服务端
                // 重启可以在 world revision 不变时发布更高 generation 的新 WorldInstance。
                return CompareAssignment(current.Assignment, incoming.Assignment);
            }

            if (!string.Equals(current.OwnerPlayerID, incoming.OwnerPlayerID, StringComparison.Ordinal) ||
                current.CreatedAtMilliseconds != incoming.CreatedAtMilliseconds)
            {
                return ClientProjectionApplyResult.Conflict;
            }

            return ClientProjectionApplyResult.Applied;
        }

        /// <summary>比较 assignment generation，并正确处理完整撤销。</summary>
        /// <param name="current">当前已知 assignment。</param>
        /// <param name="incoming">可选新 assignment；为空表示撤销。</param>
        /// <returns>稳定 generation gate 结果。</returns>
        private static ClientProjectionApplyResult CompareAssignment(
            ClientWorldAssignmentProjection current,
            ClientWorldAssignmentProjection incoming)
        {
            if (incoming == null)
            {
                return current == null
                    ? ClientProjectionApplyResult.Duplicate
                    : ClientProjectionApplyResult.Applied;
            }

            if (current == null)
            {
                return ClientProjectionApplyResult.Applied;
            }

            if (incoming.Generation < current.Generation)
            {
                return ClientProjectionApplyResult.Stale;
            }

            if (incoming.Generation == current.Generation)
            {
                if (!current.HasSameIdentity(incoming))
                {
                    return ClientProjectionApplyResult.Conflict;
                }

                if (incoming.LeaseExpiresAtMilliseconds < current.LeaseExpiresAtMilliseconds)
                {
                    return ClientProjectionApplyResult.Stale;
                }

                return incoming.LeaseExpiresAtMilliseconds == current.LeaseExpiresAtMilliseconds
                    ? ClientProjectionApplyResult.Duplicate
                    : ClientProjectionApplyResult.Applied;
            }

            return ClientProjectionApplyResult.Applied;
        }

        /// <summary>判断 control hint 是否属于任一已知 world。</summary>
        /// <param name="worldID">Hint PersonalWorldID。</param>
        /// <returns>Identity 匹配时返回 true。</returns>
        private bool MatchesKnownWorldLocked(string worldID)
        {
            return _snapshot.PrimaryWorld != null &&
                   string.Equals(_snapshot.PrimaryWorld.PersonalWorldID, worldID, StringComparison.Ordinal) ||
                   _snapshot.CurrentWorld != null &&
                   string.Equals(_snapshot.CurrentWorld.PersonalWorldID, worldID, StringComparison.Ordinal);
        }

        /// <summary>查找对应完整 snapshot 中的 assignment。</summary>
        /// <param name="worldID">PersonalWorldID。</param>
        /// <returns>匹配 assignment；不存在时为空。</returns>
        private ClientWorldAssignmentProjection FindKnownAssignmentLocked(string worldID)
        {
            if (_highestAssignments.TryGetValue(worldID, out var highest))
            {
                return highest;
            }

            if (_snapshot.CurrentWorld != null &&
                string.Equals(_snapshot.CurrentWorld.PersonalWorldID, worldID, StringComparison.Ordinal))
            {
                return _snapshot.CurrentWorld.Assignment;
            }

            return _snapshot.PrimaryWorld != null &&
                   string.Equals(_snapshot.PrimaryWorld.PersonalWorldID, worldID, StringComparison.Ordinal)
                ? _snapshot.PrimaryWorld.Assignment
                : null;
        }

        /// <summary>拒绝完整清除后重新出现的同代/低代 assignment，以及同代内容冲突。</summary>
        /// <param name="incoming">可选新 assignment；为空是完整清除。</param>
        /// <returns>Generation 可提交时返回 Applied。</returns>
        private ClientProjectionApplyResult ValidateAssignmentGenerationLocked(
            ClientWorldAssignmentProjection incoming)
        {
            if (incoming == null ||
                !_highestAssignments.TryGetValue(incoming.PersonalWorldID, out var highest))
            {
                return ClientProjectionApplyResult.Applied;
            }

            if (incoming.Generation < highest.Generation)
            {
                return ClientProjectionApplyResult.Conflict;
            }

            if (incoming.Generation == highest.Generation &&
                (_clearedAssignmentWorlds.Contains(incoming.PersonalWorldID) ||
                 !incoming.HasSameIdentity(highest) ||
                 incoming.LeaseExpiresAtMilliseconds < highest.LeaseExpiresAtMilliseconds))
            {
                return ClientProjectionApplyResult.Conflict;
            }

            return ClientProjectionApplyResult.Applied;
        }

        /// <summary>保存各 PersonalWorld 已见最高 generation，并记录完整撤销 tombstone。</summary>
        /// <param name="personalWorldID">完整 world snapshot 绑定的 PersonalWorldID。</param>
        /// <param name="assignment">可选 assignment；为空表示完整快照已撤销当前实例。</param>
        private void RememberAssignmentLocked(
            string personalWorldID,
            ClientWorldAssignmentProjection assignment)
        {
            if (assignment == null)
            {
                if (_highestAssignments.ContainsKey(personalWorldID))
                {
                    _clearedAssignmentWorlds.Add(personalWorldID);
                }

                return;
            }

            if (!_highestAssignments.TryGetValue(assignment.PersonalWorldID, out var current) ||
                assignment.Generation > current.Generation ||
                assignment.Generation == current.Generation &&
                assignment.HasSameIdentity(current) &&
                assignment.LeaseExpiresAtMilliseconds > current.LeaseExpiresAtMilliseconds)
            {
                _highestAssignments[assignment.PersonalWorldID] = assignment;
                _clearedAssignmentWorlds.Remove(assignment.PersonalWorldID);
            }
        }

        /// <summary>移除 primary、current 与最近 retired target 之外的 assignment 历史。</summary>
        private void PruneAssignmentHistoryLocked()
        {
            List<string> obsolete = null;
            foreach (var worldID in _highestAssignments.Keys)
            {
                var retained = (_snapshot.PrimaryWorld != null &&
                                string.Equals(
                                    _snapshot.PrimaryWorld.PersonalWorldID,
                                    worldID,
                                    StringComparison.Ordinal)) ||
                               (_snapshot.CurrentWorld != null &&
                                string.Equals(
                                    _snapshot.CurrentWorld.PersonalWorldID,
                                    worldID,
                                    StringComparison.Ordinal)) ||
                               string.Equals(_retiredPersonalWorldID, worldID, StringComparison.Ordinal);
                if (!retained)
                {
                    if (obsolete == null)
                    {
                        obsolete = new List<string>();
                    }

                    obsolete.Add(worldID);
                }
            }

            if (obsolete == null)
            {
                return;
            }

            foreach (var worldID in obsolete)
            {
                _highestAssignments.Remove(worldID);
                _clearedAssignmentWorlds.Remove(worldID);
            }
        }

        /// <summary>在锁内保留事实并设置 needs-refresh。</summary>
        /// <returns>新不可变 Service snapshot。</returns>
        private ClientPersonalWorldServiceSnapshot MarkRefreshLocked()
        {
            _snapshot = new ClientPersonalWorldServiceSnapshot(
                _snapshot.PrimaryWorld,
                _snapshot.CurrentWorld,
                _snapshot.AssignmentHint,
                true);
            return _snapshot;
        }

        /// <summary>处理 WSS typed PUSH。</summary>
        /// <param name="push">Control channel 已验证 payload。</param>
        private void OnControlPush(ClientControlPush push)
        {
            if (push?.Payload is WorldAssignmentChangedPush assignment)
            {
                ApplyAssignmentHint(assignment);
            }
        }

        /// <summary>处理 TLS/TCP 完整 world replacement PUSH。</summary>
        /// <param name="push">Gameplay channel 已验证 PUSH。</param>
        private void OnWorldSnapshotPush(WorldSnapshotPush push)
        {
            if (push != null)
            {
                ApplyWorldSnapshot(push.Snapshot);
            }
        }

        /// <summary>在锁外通知全部 subscriber；异常不会回滚已经提交的事实。</summary>
        /// <param name="snapshot">已提交 snapshot；未提交时为空。</param>
        /// <param name="conflict">是否同时报告协议冲突。</param>
        private void Notify(ClientPersonalWorldServiceSnapshot snapshot, bool conflict)
        {
            if (snapshot != null)
            {
                Changed?.Invoke(snapshot);
            }

            if (conflict)
            {
                ProjectionConflict?.Invoke();
            }
        }
    }
}
