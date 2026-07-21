using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Control;
using IHomeland.Client.Application.Gameplay;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Core.Lifetime;
using IHomeland.Client.Infrastructure.Tcp;
using IHomeland.Client.Infrastructure.WebSocket;
using IHomeland.Protocol.Visit.V1;

namespace IHomeland.Client.Application.World
{
    /// <summary>
    /// 唯一拥有 current VisitSession、定向 invite inbox、Owner 发出邀请与控制面收敛提示。
    /// </summary>
    internal sealed class VisitSessionService : IAppLifetimeParticipant
    {
        /// <summary>分别限制未过期且不同 identity 的 inbox 与 Owner 发出邀请数量。</summary>
        internal const int InviteCapacity = 128;

        /// <summary>保护投影、inbox、Owner 发出邀请、target role 与生命周期。</summary>
        private readonly object _sync = new object();

        /// <summary>提供 WSS invite/availability/closed typed PUSH。</summary>
        private readonly ClientControlChannel _controlChannel;

        /// <summary>提供 Visit command、完整 snapshot 与 safe-return。</summary>
        private readonly ClientGameplayChannel _gameplayChannel;

        /// <summary>提供 invite 等于即失效的确定性 UTC 时间。</summary>
        private readonly IClientClock _clock;

        /// <summary>按 VisitSessionID + InviteID 保存有界定向 inbox。</summary>
        private readonly Dictionary<string, ClientVisitInviteProjection> _invites =
            new Dictionary<string, ClientVisitInviteProjection>(StringComparer.Ordinal);

        /// <summary>按 VisitSessionID + InviteID 保存 Owner mutation response 证明的有界发出邀请。</summary>
        private readonly Dictionary<string, ClientVisitInviteProjection> _outgoingInvites =
            new Dictionary<string, ClientVisitInviteProjection>(StringComparer.Ordinal);

        /// <summary>由 coordinator 建立、caller 不能自报的 current role。</summary>
        private ClientVisitRole _targetRole;

        /// <summary>Visitor target identity；Owner own-world target 时为空。</summary>
        private string _targetVisitSessionID;

        /// <summary>保存 current 完整 replacement。</summary>
        private ClientVisitSessionProjection _current;

        /// <summary>保存最近清除 target 的最高 revision，拒绝同一 target 的迟到或倒退重绑。</summary>
        private ClientVisitSessionProjection _retiredSession;

        /// <summary>保存最新匹配 control hint。</summary>
        private ClientVisitControlHint _controlHint;

        /// <summary>标记 control hint 或冲突要求只读刷新。</summary>
        private bool _needsRefresh;

        /// <summary>记录是否已连接现有 channel event。</summary>
        private bool _subscribed;

        /// <summary>停止后永久拒绝 command 与迟到 callback。</summary>
        private bool _stopped;

        /// <summary>创建无 channel 的纯投影测试实例。</summary>
        /// <param name="clock">可控 UTC 时钟。</param>
        internal VisitSessionService(IClientClock clock)
            : this(null, null, clock)
        {
        }

        /// <summary>创建由现有 channel 驱动的 App Scope Service。</summary>
        /// <param name="controlChannel">WSS control owner。</param>
        /// <param name="gameplayChannel">TLS/TCP gameplay owner。</param>
        /// <param name="clock">Invite expiry 时钟。</param>
        internal VisitSessionService(
            ClientControlChannel controlChannel,
            ClientGameplayChannel gameplayChannel,
            IClientClock clock)
        {
            _controlChannel = controlChannel;
            _gameplayChannel = gameplayChannel;
            _clock = clock ?? throw new ArgumentNullException(nameof(clock));
        }

        /// <summary>在不可变 snapshot 已提交后通知消费者。</summary>
        internal event Action<ClientVisitSessionServiceSnapshot> Changed;

        /// <summary>在同 revision 内容冲突时通知 flow fail closed。</summary>
        internal event Action ProjectionConflict;

        /// <summary>向 coordinator 交付合法、匹配 current target 的权威 safe-return。</summary>
        internal event Action<ClientSafeReturnProjection> SafeReturnReceived;

        /// <summary>获取已按当前时钟清理过期 invite 的不可变快照。</summary>
        internal ClientVisitSessionServiceSnapshot Snapshot
        {
            get
            {
                lock (_sync)
                {
                    PruneExpiredLocked();
                    return BuildSnapshotLocked();
                }
            }
        }

        /// <summary>只登记 typed channel subscriber，不发起网络操作。</summary>
        /// <param name="cancellationToken">AppLifetime 初始化信号。</param>
        /// <returns>订阅完成的任务。</returns>
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            lock (_sync)
            {
                if (_stopped || _subscribed)
                {
                    throw new InvalidOperationException("VisitSessionService 不能重复初始化或停止后重启。");
                }

                if (_controlChannel != null)
                {
                    _controlChannel.PushReceived += OnControlPush;
                }

                if (_gameplayChannel != null)
                {
                    _gameplayChannel.VisitSnapshotReceived += OnVisitSnapshotPush;
                    _gameplayChannel.SafeReturnReceived += OnSafeReturnPush;
                }

                _subscribed = true;
            }

            return Task.CompletedTask;
        }

        /// <summary>由 coordinator 建立 current role 与 Visitor target identity。</summary>
        /// <param name="role">Admission/flow 证明的角色。</param>
        /// <param name="visitSessionID">Visitor target；Owner 时必须为空。</param>
        /// <returns>Role binding 合法且已提交时返回 true。</returns>
        internal bool SetTargetRole(ClientVisitRole role, string visitSessionID)
        {
            if (role != ClientVisitRole.Owner && role != ClientVisitRole.Visitor)
            {
                return false;
            }

            if (role == ClientVisitRole.Owner && visitSessionID != null ||
                role == ClientVisitRole.Visitor &&
                !ClientWorldProjectionMapper.IsValidIdentity(visitSessionID))
            {
                return false;
            }

            ClientVisitSessionServiceSnapshot committed = null;
            lock (_sync)
            {
                if (_stopped)
                {
                    return false;
                }

                _targetRole = role;
                _targetVisitSessionID = visitSessionID;
                if (_current != null &&
                    (role != _current.Role ||
                     role == ClientVisitRole.Visitor &&
                     !string.Equals(_current.VisitSessionID, visitSessionID, StringComparison.Ordinal)))
                {
                    _retiredSession = _current;
                    _current = null;
                    _controlHint = null;
                    _needsRefresh = false;
                    committed = BuildSnapshotLocked();
                }
            }

            Notify(committed, false);
            return true;
        }

        /// <summary>清除 current target role、完整投影与 hint，并拒绝旧 target command。</summary>
        internal void ClearTargetRole()
        {
            ClientVisitSessionServiceSnapshot committed;
            lock (_sync)
            {
                if (_targetRole == ClientVisitRole.None && _targetVisitSessionID == null &&
                    _current == null && _controlHint == null && !_needsRefresh)
                {
                    return;
                }

                _targetRole = ClientVisitRole.None;
                _targetVisitSessionID = null;
                if (_current != null)
                {
                    _retiredSession = _current;
                }

                _current = null;
                _controlHint = null;
                _needsRefresh = false;
                committed = BuildSnapshotLocked();
            }

            Notify(committed, false);
        }

        /// <summary>提交完整 VisitSession replacement，并施加 identity/revision gate。</summary>
        /// <param name="snapshot">Generated 完整快照。</param>
        /// <param name="role">由 current flow 建立的角色。</param>
        /// <returns>稳定 apply 结果。</returns>
        internal ClientProjectionApplyResult ApplySnapshot(
            VisitSessionSnapshot snapshot,
            ClientVisitRole role)
        {
            ClientVisitSessionProjection incoming;
            try
            {
                incoming = ClientWorldProjectionMapper.FromVisitSnapshot(snapshot, role);
            }
            catch (ClientWorldProjectionException)
            {
                return ClientProjectionApplyResult.Rejected;
            }

            ClientVisitSessionServiceSnapshot committed = null;
            ClientProjectionApplyResult result;
            lock (_sync)
            {
                if (_stopped || role != _targetRole ||
                    role == ClientVisitRole.Visitor &&
                    !string.Equals(incoming.VisitSessionID, _targetVisitSessionID, StringComparison.Ordinal))
                {
                    return ClientProjectionApplyResult.Rejected;
                }

                var baseline = _current;
                if (baseline == null && _retiredSession != null &&
                    string.Equals(
                        _retiredSession.VisitSessionID,
                        incoming.VisitSessionID,
                        StringComparison.Ordinal))
                {
                    baseline = _retiredSession;
                }

                result = Compare(baseline, incoming);
                if (result == ClientProjectionApplyResult.Applied ||
                    (result == ClientProjectionApplyResult.Duplicate && _current == null))
                {
                    if (incoming.Role == ClientVisitRole.Owner &&
                        incoming.Lifecycle == ClientVisitLifecycle.Closed)
                    {
                        _retiredSession = incoming;
                        _current = null;
                        RemoveOutgoingSessionInvitesLocked(incoming.VisitSessionID);
                    }
                    else
                    {
                        _current = incoming;
                        if (incoming.Role == ClientVisitRole.Owner)
                        {
                            RetireJoinedVisitorInvitesLocked(incoming);
                        }
                    }

                    _controlHint = null;
                    _needsRefresh = false;
                    committed = BuildSnapshotLocked();
                }
                else if (result == ClientProjectionApplyResult.Conflict)
                {
#if UNITY_EDITOR || DEVELOPMENT_BUILD
                    UnityEngine.Debug.LogWarning(
                        "[IHOMELAND_PROJECTION] component=VisitSessionService " +
                        "operation=apply_snapshot outcome=conflict differences=" +
                        DescribeProjectionDifferences(baseline, incoming));
#endif
                    _needsRefresh = true;
                    committed = BuildSnapshotLocked();
                }
            }

            Notify(committed, result == ClientProjectionApplyResult.Conflict);
            return result;
        }

        /// <summary>应用按 identity 去重且按 expiry 清理的定向 invite。</summary>
        /// <param name="push">WSS generated invite PUSH。</param>
        /// <returns>稳定 inbox apply 结果。</returns>
        internal ClientProjectionApplyResult ApplyInvite(VisitInvitePush push)
        {
            ClientVisitInviteProjection incoming;
            try
            {
                incoming = ClientWorldProjectionMapper.FromInvitePush(push);
            }
            catch (ClientWorldProjectionException)
            {
                return ClientProjectionApplyResult.Rejected;
            }

            return ApplyInviteProjection(incoming, outgoing: false);
        }

        /// <summary>在 HTTP accept 已明确提交后从 inbox 退役对应 pending invite。</summary>
        /// <param name="visitSessionID">已接受邀请所属 VisitSessionID。</param>
        /// <param name="inviteID">已消费 InviteID。</param>
        /// <returns>当前 inbox 包含并移除该 identity 时返回 true。</returns>
        /// <remarks>
        /// 此入口只接收 coordinator 从成功 accept response 得到的权威结论；timeout、transport 与
        /// commit-unknown 不得调用，避免客户端猜测服务端提交结果。
        /// </remarks>
        internal bool RetireAcceptedInvite(string visitSessionID, string inviteID)
        {
            return RetireInvite(visitSessionID, inviteID);
        }

        /// <summary>在 HTTP accept 被服务端明确判定为失效后，从 inbox 退役对应 pending invite。</summary>
        /// <param name="visitSessionID">失效邀请所属 VisitSessionID。</param>
        /// <param name="inviteID">服务端拒绝的 InviteID。</param>
        /// <returns>当前 inbox 包含并移除该 identity 时返回 true。</returns>
        /// <remarks>
        /// 此入口只接收 coordinator 白名单中的确定性服务端拒绝；容量、限流、依赖失败、timeout、
        /// transport 与 commit-unknown 不得调用，避免客户端根据瞬时故障猜测邀请事实。
        /// </remarks>
        internal bool RetireRejectedInvite(string visitSessionID, string inviteID)
        {
            return RetireInvite(visitSessionID, inviteID);
        }

        /// <summary>按完整 invite identity 幂等移除 inbox 项。</summary>
        /// <param name="visitSessionID">邀请所属 VisitSessionID。</param>
        /// <param name="inviteID">待退役 InviteID。</param>
        /// <returns>当前 inbox 包含并移除该 identity 时返回 true。</returns>
        private bool RetireInvite(string visitSessionID, string inviteID)
        {
            if (!ClientWorldProjectionMapper.IsValidIdentity(visitSessionID) ||
                !ClientWorldProjectionMapper.IsValidIdentity(inviteID))
            {
                return false;
            }

            ClientVisitSessionServiceSnapshot committed = null;
            bool removed;
            lock (_sync)
            {
                if (_stopped)
                {
                    return false;
                }

                removed = _invites.Remove(InviteKey(visitSessionID, inviteID));
                if (removed)
                {
                    committed = BuildSnapshotLocked();
                }
            }

            Notify(committed, false);
            return removed;
        }

        /// <summary>按 identity、revision 与 expiry 提交已验证的邀请投影。</summary>
        /// <param name="incoming">由 response 或 PUSH 转换的不可变邀请。</param>
        /// <param name="outgoing">是否保存为 Owner 发出邀请；false 表示 Visitor inbox。</param>
        /// <returns>稳定 apply 结果。</returns>
        private ClientProjectionApplyResult ApplyInviteProjection(
            ClientVisitInviteProjection incoming,
            bool outgoing)
        {
            if (incoming == null)
            {
                throw new ArgumentNullException(nameof(incoming));
            }

            ClientVisitSessionServiceSnapshot committed = null;
            ClientProjectionApplyResult result;
            lock (_sync)
            {
                if (_stopped ||
                    (incoming.State == ClientVisitInviteState.Pending &&
                     incoming.ExpiresAtMilliseconds <= _clock.UtcNowMilliseconds))
                {
                    return ClientProjectionApplyResult.Rejected;
                }

                PruneExpiredLocked();
                if (outgoing &&
                    (_targetRole != ClientVisitRole.Owner || _current == null ||
                     !string.Equals(_current.VisitSessionID, incoming.VisitSessionID, StringComparison.Ordinal) ||
                     !string.Equals(_current.OwnerPlayerID, incoming.OwnerPlayerID, StringComparison.Ordinal)))
                {
                    return ClientProjectionApplyResult.Rejected;
                }

                var invites = outgoing ? _outgoingInvites : _invites;
                var key = InviteKey(incoming.VisitSessionID, incoming.InviteID);
                if (incoming.State != ClientVisitInviteState.Pending)
                {
                    var removed = invites.Remove(key);
                    if (removed)
                    {
                        committed = BuildSnapshotLocked();
                    }

                    result = removed
                        ? ClientProjectionApplyResult.Applied
                        : ClientProjectionApplyResult.Duplicate;
                }
                else
                {
                    var removedSuperseded = RemoveSupersededTargetInvitesLocked(invites, incoming);
                    if (invites.TryGetValue(key, out var current))
                    {
                        if (incoming.CreatedRevision < current.CreatedRevision)
                        {
                            return ClientProjectionApplyResult.Stale;
                        }

                        if (incoming.CreatedRevision == current.CreatedRevision)
                        {
                            result = current.IsEquivalent(incoming)
                            ? ClientProjectionApplyResult.Duplicate
                            : ClientProjectionApplyResult.Conflict;
                            if (removedSuperseded)
                            {
                                committed = BuildSnapshotLocked();
                            }
                        }
                        else
                        {
                            invites[key] = incoming;
                            committed = BuildSnapshotLocked();
                            result = ClientProjectionApplyResult.Applied;
                        }

                    }
                    else
                    {
                        if (invites.Count >= InviteCapacity)
                        {
                            return ClientProjectionApplyResult.Overflow;
                        }

                        invites[key] = incoming;
                        committed = BuildSnapshotLocked();
                        result = ClientProjectionApplyResult.Applied;
                    }
                }
            }

            Notify(committed, false);
            return result;
        }

        /// <summary>应用 Owner availability control hint，且不替代完整 snapshot。</summary>
        /// <param name="push">WSS generated availability PUSH。</param>
        /// <returns>稳定 hint apply 结果。</returns>
        internal ClientProjectionApplyResult ApplyOwnerAvailability(VisitOwnerAvailabilityPush push)
        {
            try
            {
                return ApplyControlHint(ClientWorldProjectionMapper.FromOwnerAvailability(push));
            }
            catch (ClientWorldProjectionException)
            {
                return ClientProjectionApplyResult.Rejected;
            }
        }

        /// <summary>应用 terminal close control hint，且不伪造 safe-return。</summary>
        /// <param name="push">WSS generated close notice PUSH。</param>
        /// <returns>稳定 hint apply 结果。</returns>
        internal ClientProjectionApplyResult ApplyClosedNotice(VisitClosedNoticePush push)
        {
            try
            {
                return ApplyControlHint(ClientWorldProjectionMapper.FromClosedNotice(push));
            }
            catch (ClientWorldProjectionException)
            {
                return ClientProjectionApplyResult.Rejected;
            }
        }

        /// <summary>在 own-world Owner target 上开启或读取 active VisitSession。</summary>
        /// <param name="cancellationToken">只取消 caller 等待。</param>
        /// <returns>强类型 command result。</returns>
        internal async Task<ClientGameplayResult<VisitOpenResponse>> OpenAsync(
            CancellationToken cancellationToken)
        {
            if (!CanOpen())
            {
                return Policy<VisitOpenResponse>();
            }

            var result = await _gameplayChannel.SendAsync(
                ClientGameplayCatalog.VisitOpen,
                new VisitOpenCommand(),
                cancellationToken);
            if (!result.IsSuccess)
            {
                return result;
            }

            var applied = result.Value?.Snapshot == null
                ? ClientProjectionApplyResult.Rejected
                : ApplySnapshot(result.Value.Snapshot, ClientVisitRole.Owner);
            return applied == ClientProjectionApplyResult.Applied ||
                   applied == ClientProjectionApplyResult.Duplicate
                ? result
                : ClientGameplayResult<VisitOpenResponse>.Failed(ClientGameplayFailureKind.Protocol);
        }

        /// <summary>以 current revision 创建定向 invite。</summary>
        /// <param name="targetVisitorID">被操作 Visitor identity。</param>
        /// <param name="lifetimeMilliseconds">请求有效期，单位为毫秒。</param>
        /// <param name="cancellationToken">只取消 caller 等待。</param>
        /// <returns>强类型 mutation result。</returns>
        internal async Task<ClientGameplayResult<VisitCreateInviteResponse>> CreateInviteAsync(
            string targetVisitorID,
            uint lifetimeMilliseconds,
            CancellationToken cancellationToken)
        {
            if (!TryOwnerRevision(out var revision) ||
                !ClientWorldProjectionMapper.IsValidIdentity(targetVisitorID) ||
                lifetimeMilliseconds == 0)
            {
                return Policy<VisitCreateInviteResponse>();
            }

            var result = await _gameplayChannel.SendAsync(
                ClientGameplayCatalog.VisitCreateInvite,
                new VisitCreateInviteCommand
                {
                    TargetVisitorId = targetVisitorID,
                    InviteLifetimeMs = lifetimeMilliseconds,
                    ExpectedRevision = revision,
                },
                cancellationToken);
            var mutationApplied = ApplyMutationResult(result, value => value.Result);
            if (!mutationApplied.IsSuccess)
            {
                return mutationApplied;
            }

            ClientVisitInviteProjection invite;
            try
            {
                string ownerPlayerID;
                lock (_sync)
                {
                    ownerPlayerID = _current?.OwnerPlayerID;
                }

                invite = ClientWorldProjectionMapper.FromInviteSummary(
                    mutationApplied.Value?.Invite,
                    ownerPlayerID);
            }
            catch (ClientWorldProjectionException)
            {
                return ClientGameplayResult<VisitCreateInviteResponse>.Failed(
                    ClientGameplayFailureKind.Protocol);
            }

            var inviteApplied = ApplyInviteProjection(invite, outgoing: true);
            return inviteApplied == ClientProjectionApplyResult.Applied ||
                   inviteApplied == ClientProjectionApplyResult.Duplicate
                ? mutationApplied
                : ClientGameplayResult<VisitCreateInviteResponse>.Failed(
                    ClientGameplayFailureKind.Protocol);
        }

        /// <summary>以 current revision 撤销 pending invite。</summary>
        /// <param name="inviteID">待撤销 invite identity。</param>
        /// <param name="cancellationToken">只取消 caller 等待。</param>
        /// <returns>强类型 mutation result。</returns>
        internal async Task<ClientGameplayResult<VisitRevokeInviteResponse>> RevokeInviteAsync(
            string inviteID,
            CancellationToken cancellationToken)
        {
            if (!TryOwnerRevision(out var revision) ||
                !ClientWorldProjectionMapper.IsValidIdentity(inviteID))
            {
                return Policy<VisitRevokeInviteResponse>();
            }

            var result = await _gameplayChannel.SendAsync(
                ClientGameplayCatalog.VisitRevokeInvite,
                new VisitRevokeInviteCommand { InviteId = inviteID, ExpectedRevision = revision },
                cancellationToken);
            var mutationApplied = ApplyMutationResult(result, value => value.Result);
            if (!mutationApplied.IsSuccess)
            {
                return mutationApplied;
            }

            ClientVisitSessionServiceSnapshot committed = null;
            lock (_sync)
            {
                if (_current != null &&
                    _outgoingInvites.Remove(InviteKey(_current.VisitSessionID, inviteID)))
                {
                    committed = BuildSnapshotLocked();
                }
            }

            Notify(committed, false);
            return mutationApplied;
        }

        /// <summary>以 current revision 移除指定 Visitor。</summary>
        /// <param name="targetVisitorID">被操作 Visitor identity。</param>
        /// <param name="cancellationToken">只取消 caller 等待。</param>
        /// <returns>强类型 mutation result。</returns>
        internal async Task<ClientGameplayResult<VisitKickResponse>> KickAsync(
            string targetVisitorID,
            CancellationToken cancellationToken)
        {
            if (!TryOwnerRevision(out var revision) ||
                !ClientWorldProjectionMapper.IsValidIdentity(targetVisitorID))
            {
                return Policy<VisitKickResponse>();
            }

            var result = await _gameplayChannel.SendAsync(
                ClientGameplayCatalog.VisitKick,
                new VisitKickCommand { TargetVisitorId = targetVisitorID, ExpectedRevision = revision },
                cancellationToken);
            return ApplyMutationResult(result, value => value.Result);
        }

        /// <summary>以 current revision 关闭 Owner 的 VisitSession。</summary>
        /// <param name="cancellationToken">只取消 caller 等待。</param>
        /// <returns>强类型 mutation result。</returns>
        internal async Task<ClientGameplayResult<VisitCloseResponse>> CloseAsync(
            CancellationToken cancellationToken)
        {
            if (!TryOwnerRevision(out var revision))
            {
                return Policy<VisitCloseResponse>();
            }

            var result = await _gameplayChannel.SendAsync(
                ClientGameplayCatalog.VisitClose,
                new VisitCloseCommand { ExpectedRevision = revision },
                cancellationToken);
            return ApplyMutationResult(result, value => value.Result);
        }

        /// <summary>以 current Visitor revision 主动离开 active VisitSession。</summary>
        /// <param name="cancellationToken">只取消 caller 等待。</param>
        /// <returns>强类型 mutation result。</returns>
        internal async Task<ClientGameplayResult<VisitLeaveResponse>> LeaveAsync(
            CancellationToken cancellationToken)
        {
            if (!TryVisitorRevision(out var revision))
            {
                return Policy<VisitLeaveResponse>();
            }

            var result = await _gameplayChannel.SendAsync(
                ClientGameplayCatalog.VisitLeave,
                new VisitLeaveCommand { ExpectedRevision = revision },
                cancellationToken);
            return ApplyMutationResult(result, value => value.Result);
        }

        /// <summary>解除 subscriber、关闭 role gate 并拒绝迟到输入。</summary>
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
                _targetRole = ClientVisitRole.None;
                _targetVisitSessionID = null;
                if (_subscribed)
                {
                    if (_controlChannel != null)
                    {
                        _controlChannel.PushReceived -= OnControlPush;
                    }

                    if (_gameplayChannel != null)
                    {
                        _gameplayChannel.VisitSnapshotReceived -= OnVisitSnapshotPush;
                        _gameplayChannel.SafeReturnReceived -= OnSafeReturnPush;
                    }

                    _subscribed = false;
                }
            }

            return Task.CompletedTask;
        }

        /// <summary>提交 control hint 并保持完整 snapshot 不变。</summary>
        /// <param name="incoming">已验证 hint。</param>
        /// <returns>稳定 revision gate 结果。</returns>
        private ClientProjectionApplyResult ApplyControlHint(ClientVisitControlHint incoming)
        {
            ClientVisitSessionServiceSnapshot committed;
            var conflict = false;
            lock (_sync)
            {
                if (_stopped || _current == null ||
                    !string.Equals(_current.VisitSessionID, incoming.VisitSessionID, StringComparison.Ordinal))
                {
                    return ClientProjectionApplyResult.Rejected;
                }

                if (incoming.Revision < _current.Revision ||
                    _controlHint != null && incoming.Revision < _controlHint.Revision)
                {
                    return ClientProjectionApplyResult.Stale;
                }

                if (_controlHint != null && incoming.Revision == _controlHint.Revision)
                {
                    if (HintsEquivalent(_controlHint, incoming))
                    {
                        return ClientProjectionApplyResult.Duplicate;
                    }

                    _needsRefresh = true;
                    committed = BuildSnapshotLocked();
                    conflict = true;
                }
                else
                {
                    _controlHint = incoming;
                    _needsRefresh = true;
                    committed = BuildSnapshotLocked();
                }
            }

            Notify(committed, conflict);
            return conflict
                ? ClientProjectionApplyResult.Conflict
                : ClientProjectionApplyResult.Applied;
        }

        /// <summary>应用成功 mutation response 中的完整首次结果。</summary>
        /// <typeparam name="TResponse">强类型 response。</typeparam>
        /// <param name="result">Gameplay operation result。</param>
        /// <param name="selector">取得共享 mutation result。</param>
        /// <returns>原成功/拒绝结果或 projection protocol failure。</returns>
        private ClientGameplayResult<TResponse> ApplyMutationResult<TResponse>(
            ClientGameplayResult<TResponse> result,
            Func<TResponse, VisitMutationResult> selector)
            where TResponse : class
        {
            if (!result.IsSuccess)
            {
                return result;
            }

            var mutation = selector(result.Value);
            ClientVisitRole role;
            lock (_sync)
            {
                role = _targetRole;
            }

            var applied = mutation?.Snapshot == null
                ? ClientProjectionApplyResult.Rejected
                : ApplySnapshot(mutation.Snapshot, role);
            return applied == ClientProjectionApplyResult.Applied ||
                   applied == ClientProjectionApplyResult.Duplicate
                ? result
                : ClientGameplayResult<TResponse>.Failed(ClientGameplayFailureKind.Protocol);
        }

        /// <summary>判断 Owner open 是否可写入 gameplay channel。</summary>
        /// <returns>Current target 为 own-world Owner 且 Service active 时返回 true。</returns>
        private bool CanOpen()
        {
            lock (_sync)
            {
                return !_stopped && _gameplayChannel != null &&
                       _targetRole == ClientVisitRole.Owner &&
                       _targetVisitSessionID == null;
            }
        }

        /// <summary>取得 Owner command current revision。</summary>
        /// <param name="revision">成功时返回正 revision。</param>
        /// <returns>Role、target 与完整 snapshot 均匹配时返回 true。</returns>
        private bool TryOwnerRevision(out ulong revision)
        {
            lock (_sync)
            {
                if (!_stopped && _gameplayChannel != null &&
                    _targetRole == ClientVisitRole.Owner && _current != null &&
                    _current.Role == ClientVisitRole.Owner &&
                    _current.Lifecycle == ClientVisitLifecycle.Open)
                {
                    revision = _current.Revision;
                    return true;
                }

                revision = 0;
                return false;
            }
        }

        /// <summary>取得 Visitor leave current revision。</summary>
        /// <param name="revision">成功时返回正 revision。</param>
        /// <returns>Role、target 与完整 snapshot 均匹配时返回 true。</returns>
        private bool TryVisitorRevision(out ulong revision)
        {
            lock (_sync)
            {
                if (!_stopped && _gameplayChannel != null &&
                    _targetRole == ClientVisitRole.Visitor && _current != null &&
                    _current.Role == ClientVisitRole.Visitor &&
                    string.Equals(_current.VisitSessionID, _targetVisitSessionID, StringComparison.Ordinal) &&
                    _current.Lifecycle != ClientVisitLifecycle.Closed)
                {
                    revision = _current.Revision;
                    return true;
                }

                revision = 0;
                return false;
            }
        }

        /// <summary>比较同一 VisitSession 的 revision 与 immutable fields。</summary>
        /// <param name="current">Current replacement。</param>
        /// <param name="incoming">Incoming replacement。</param>
        /// <returns>稳定 apply 结果。</returns>
        private static ClientProjectionApplyResult Compare(
            ClientVisitSessionProjection current,
            ClientVisitSessionProjection incoming)
        {
            if (current == null)
            {
                return ClientProjectionApplyResult.Applied;
            }

            if (!string.Equals(current.VisitSessionID, incoming.VisitSessionID, StringComparison.Ordinal))
            {
                return ClientProjectionApplyResult.Conflict;
            }

            if (incoming.Revision < current.Revision)
            {
                return ClientProjectionApplyResult.Stale;
            }

            if (incoming.Revision == current.Revision)
            {
                if (!current.IsEquivalentIgnoringAssignmentLease(incoming))
                {
                    return ClientProjectionApplyResult.Conflict;
                }

                if (incoming.Assignment.LeaseExpiresAtMilliseconds <
                    current.Assignment.LeaseExpiresAtMilliseconds)
                {
                    return ClientProjectionApplyResult.Stale;
                }

                return incoming.Assignment.LeaseExpiresAtMilliseconds ==
                       current.Assignment.LeaseExpiresAtMilliseconds
                    ? ClientProjectionApplyResult.Duplicate
                    : ClientProjectionApplyResult.Applied;
            }

            if (!string.Equals(current.OwnerPlayerID, incoming.OwnerPlayerID, StringComparison.Ordinal) ||
                current.CreatedAtMilliseconds != incoming.CreatedAtMilliseconds ||
                current.ExpiresAtMilliseconds != incoming.ExpiresAtMilliseconds ||
                !current.Assignment.HasSameIdentity(incoming.Assignment) ||
                incoming.Assignment.LeaseExpiresAtMilliseconds <
                current.Assignment.LeaseExpiresAtMilliseconds ||
                current.Role != incoming.Role)
            {
                return ClientProjectionApplyResult.Conflict;
            }

            return ClientProjectionApplyResult.Applied;
        }

        /// <summary>生成不包含玩家、世界或连接 identity 的投影差异字段列表。</summary>
        /// <param name="current">当前已提交投影。</param>
        /// <param name="incoming">待提交投影。</param>
        /// <returns>用于 Development 诊断的稳定字段名列表。</returns>
        private static string DescribeProjectionDifferences(
            ClientVisitSessionProjection current,
            ClientVisitSessionProjection incoming)
        {
            if (current == null || incoming == null)
            {
                return "projection_presence";
            }

            var differences = new List<string>();
            AddDifference(differences, "visit_session_id", !string.Equals(
                current.VisitSessionID,
                incoming.VisitSessionID,
                StringComparison.Ordinal));
            AddDifference(differences, "owner_player_id", !string.Equals(
                current.OwnerPlayerID,
                incoming.OwnerPlayerID,
                StringComparison.Ordinal));
            AddDifference(differences, "assignment", !current.Assignment.IsEquivalent(incoming.Assignment));
            AddDifference(differences, "lifecycle", current.Lifecycle != incoming.Lifecycle);
            AddDifference(differences, "revision", current.Revision != incoming.Revision);
            AddDifference(differences, "capacity", current.Capacity != incoming.Capacity);
            AddDifference(differences, "created_at_ms", current.CreatedAtMilliseconds != incoming.CreatedAtMilliseconds);
            AddDifference(differences, "expires_at_ms", current.ExpiresAtMilliseconds != incoming.ExpiresAtMilliseconds);
            AddDifference(
                differences,
                "owner_grace_expires_at_ms",
                current.OwnerGraceExpiresAtMilliseconds != incoming.OwnerGraceExpiresAtMilliseconds);
            AddDifference(differences, "role", current.Role != incoming.Role);
            AddDifference(differences, "visitors", !VisitorsEquivalent(current.Visitors, incoming.Visitors));
            return differences.Count == 0 ? "unknown" : string.Join(",", differences);
        }

        /// <summary>把发生变化的字段名追加到诊断列表。</summary>
        /// <param name="differences">待追加的差异列表。</param>
        /// <param name="field">稳定字段名。</param>
        /// <param name="different">字段是否发生变化。</param>
        private static void AddDifference(List<string> differences, string field, bool different)
        {
            if (different)
            {
                differences.Add(field);
            }
        }

        /// <summary>比较两个稳定排序的 Visitor 投影集合。</summary>
        /// <param name="current">当前 Visitor 集合。</param>
        /// <param name="incoming">待提交 Visitor 集合。</param>
        /// <returns>数量、顺序和全部字段一致时返回 true。</returns>
        private static bool VisitorsEquivalent(
            IReadOnlyList<ClientVisitVisitorProjection> current,
            IReadOnlyList<ClientVisitVisitorProjection> incoming)
        {
            if (current.Count != incoming.Count)
            {
                return false;
            }

            for (var index = 0; index < current.Count; index++)
            {
                if (!current[index].IsEquivalent(incoming[index]))
                {
                    return false;
                }
            }

            return true;
        }

        /// <summary>按当前 UTC 时间惰性清理等于即失效 invite。</summary>
        /// <returns>集合是否发生变化。</returns>
        private bool PruneExpiredLocked()
        {
            return PruneExpiredLocked(_invites) | PruneExpiredLocked(_outgoingInvites);
        }

        /// <summary>按当前 UTC 时间惰性清理指定邀请索引。</summary>
        /// <param name="invites">待清理的 inbox 或 Owner 发出邀请索引。</param>
        /// <returns>索引是否发生变化。</returns>
        private bool PruneExpiredLocked(IDictionary<string, ClientVisitInviteProjection> invites)
        {
            List<string> expired = null;
            foreach (var pair in invites)
            {
                if (pair.Value.ExpiresAtMilliseconds <= _clock.UtcNowMilliseconds)
                {
                    if (expired == null)
                    {
                        expired = new List<string>();
                    }

                    expired.Add(pair.Key);
                }
            }

            if (expired == null)
            {
                return false;
            }

            foreach (var key in expired)
            {
                invites.Remove(key);
            }

            return true;
        }

        /// <summary>从当前字段构造稳定排序的不可变 Service snapshot。</summary>
        /// <returns>新 snapshot。</returns>
        private ClientVisitSessionServiceSnapshot BuildSnapshotLocked()
        {
            var invites = SortedInvites(_invites);
            var outgoingInvites = SortedInvites(_outgoingInvites);
            return new ClientVisitSessionServiceSnapshot(
                _current,
                invites,
                outgoingInvites,
                _controlHint,
                _needsRefresh);
        }

        /// <summary>按 VisitSessionID 与 InviteID 构造稳定排序的邀请快照。</summary>
        /// <param name="invites">待快照的 inbox 或 Owner 发出邀请索引。</param>
        /// <returns>不与内部 dictionary 共享的新列表。</returns>
        private static List<ClientVisitInviteProjection> SortedInvites(
            IDictionary<string, ClientVisitInviteProjection> invites)
        {
            var sorted = new List<ClientVisitInviteProjection>(invites.Values);
            sorted.Sort((left, right) =>
            {
                var session = string.CompareOrdinal(left.VisitSessionID, right.VisitSessionID);
                return session != 0 ? session : string.CompareOrdinal(left.InviteID, right.InviteID);
            });
            return sorted;
        }

        /// <summary>用同一 target 的更高 created revision 替换本地旧 pending identity。</summary>
        /// <param name="invites">待收敛的 inbox 或 Owner 发出邀请索引。</param>
        /// <param name="incoming">服务端证明的新 pending invite。</param>
        /// <remarks>
        /// 服务端 aggregate 对同一 target 最多允许一个 pending invite；因此更高 created revision
        /// 证明旧 identity 已被撤销、接受或到期。反向或同 revision 不在此处删除，仍交给 identity gate。
        /// </remarks>
        /// <returns>至少移除一个旧 identity 时返回 true。</returns>
        private static bool RemoveSupersededTargetInvitesLocked(
            IDictionary<string, ClientVisitInviteProjection> invites,
            ClientVisitInviteProjection incoming)
        {
            List<string> superseded = null;
            foreach (var pair in invites)
            {
                var current = pair.Value;
                if (current.State != ClientVisitInviteState.Pending ||
                    current.CreatedRevision >= incoming.CreatedRevision ||
                    !string.Equals(current.VisitSessionID, incoming.VisitSessionID, StringComparison.Ordinal) ||
                    !string.Equals(current.OwnerPlayerID, incoming.OwnerPlayerID, StringComparison.Ordinal) ||
                    !string.Equals(current.TargetVisitorID, incoming.TargetVisitorID, StringComparison.Ordinal))
                {
                    continue;
                }

                if (superseded == null)
                {
                    superseded = new List<string>();
                }

                superseded.Add(pair.Key);
            }

            if (superseded == null)
            {
                return false;
            }

            foreach (var key in superseded)
            {
                invites.Remove(key);
            }

            return true;
        }

        /// <summary>清除指定 VisitSession 的全部 Owner 发出邀请。</summary>
        /// <param name="visitSessionID">已关闭或退出 Owner target 的 VisitSessionID。</param>
        private void RemoveOutgoingSessionInvitesLocked(string visitSessionID)
        {
            List<string> removed = null;
            foreach (var pair in _outgoingInvites)
            {
                if (!string.Equals(
                        pair.Value.VisitSessionID,
                        visitSessionID,
                        StringComparison.Ordinal))
                {
                    continue;
                }

                if (removed == null)
                {
                    removed = new List<string>();
                }

                removed.Add(pair.Key);
            }

            if (removed == null)
            {
                return;
            }

            foreach (var key in removed)
            {
                _outgoingInvites.Remove(key);
            }
        }

        /// <summary>根据 Owner 完整 member snapshot 退役已经被接受的 outgoing invite。</summary>
        /// <param name="snapshot">刚通过 revision gate 的 Owner VisitSession snapshot。</param>
        /// <remarks>
        /// Visitor 出现在权威 member 集合中即证明该 target 的 pending invite 已被消费。Owner 不需要
        /// 等待额外 invite 状态 PUSH，也不能继续向 View 暴露会被服务端拒绝的撤销操作。
        /// </remarks>
        private void RetireJoinedVisitorInvitesLocked(ClientVisitSessionProjection snapshot)
        {
            if (snapshot.Visitors.Count == 0 || _outgoingInvites.Count == 0)
            {
                return;
            }

            var visitorIDs = new HashSet<string>(StringComparer.Ordinal);
            foreach (var visitor in snapshot.Visitors)
            {
                visitorIDs.Add(visitor.PlayerID);
            }

            var retiredKeys = new List<string>();
            foreach (var pair in _outgoingInvites)
            {
                if (string.Equals(
                        pair.Value.VisitSessionID,
                        snapshot.VisitSessionID,
                        StringComparison.Ordinal) &&
                    visitorIDs.Contains(pair.Value.TargetVisitorID))
                {
                    retiredKeys.Add(pair.Key);
                }
            }

            foreach (var key in retiredKeys)
            {
                _outgoingInvites.Remove(key);
            }
        }

        /// <summary>构造不允许 identity 拼接碰撞的内部 invite key。</summary>
        /// <param name="visitSessionID">VisitSessionID。</param>
        /// <param name="inviteID">InviteID。</param>
        /// <returns>只在内存索引使用的 stable key。</returns>
        private static string InviteKey(string visitSessionID, string inviteID)
        {
            return visitSessionID + "\0" + inviteID;
        }

        /// <summary>比较两个 control hint 的全部语义。</summary>
        /// <param name="left">Current hint。</param>
        /// <param name="right">Incoming hint。</param>
        /// <returns>全部字段一致时返回 true。</returns>
        private static bool HintsEquivalent(ClientVisitControlHint left, ClientVisitControlHint right)
        {
            return string.Equals(left.VisitSessionID, right.VisitSessionID, StringComparison.Ordinal) &&
                   left.Revision == right.Revision &&
                   left.OwnerAvailable == right.OwnerAvailable &&
                   left.GraceExpiresAtMilliseconds == right.GraceExpiresAtMilliseconds &&
                   left.ClosedReason == right.ClosedReason;
        }

        /// <summary>创建不触达 writer 的稳定 policy failure。</summary>
        /// <typeparam name="TResponse">预期 response 类型。</typeparam>
        /// <returns>Policy failure。</returns>
        private static ClientGameplayResult<TResponse> Policy<TResponse>()
            where TResponse : class
        {
            return ClientGameplayResult<TResponse>.Failed(ClientGameplayFailureKind.Policy);
        }

        /// <summary>分派 control channel 的三个 Visit typed PUSH。</summary>
        /// <param name="push">已验证 control PUSH。</param>
        private void OnControlPush(ClientControlPush push)
        {
            if (push?.Payload is VisitInvitePush invite)
            {
                ApplyInvite(invite);
            }
            else if (push?.Payload is VisitOwnerAvailabilityPush availability)
            {
                ApplyOwnerAvailability(availability);
            }
            else if (push?.Payload is VisitClosedNoticePush closed)
            {
                ApplyClosedNotice(closed);
            }
        }

        /// <summary>提交 gameplay 完整 VisitSession PUSH。</summary>
        /// <param name="push">已验证 gameplay PUSH。</param>
        private void OnVisitSnapshotPush(VisitSnapshotPush push)
        {
            ClientVisitRole role;
            lock (_sync)
            {
                role = _targetRole;
            }

            if (push != null && role != ClientVisitRole.None)
            {
                ApplySnapshot(push.Snapshot, role);
            }
        }

        /// <summary>校验 safe-return identity 后在锁外通知 coordinator。</summary>
        /// <param name="push">Gameplay channel 已先关闭 mutation gate 的 PUSH。</param>
        private void OnSafeReturnPush(VisitSafeReturnPush push)
        {
            ClientSafeReturnProjection projection;
            try
            {
                projection = ClientWorldProjectionMapper.FromSafeReturn(push?.Directive);
            }
            catch (ClientWorldProjectionException)
            {
                return;
            }

            lock (_sync)
            {
                if (_stopped || _targetRole != ClientVisitRole.Visitor ||
                    !string.Equals(_targetVisitSessionID, projection.VisitSessionID, StringComparison.Ordinal))
                {
                    return;
                }
            }

            SafeReturnReceived?.Invoke(projection);
        }

        /// <summary>在锁外通知 snapshot 与可选 conflict。</summary>
        /// <param name="snapshot">已提交 snapshot。</param>
        /// <param name="conflict">是否报告协议冲突。</param>
        private void Notify(ClientVisitSessionServiceSnapshot snapshot, bool conflict)
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
