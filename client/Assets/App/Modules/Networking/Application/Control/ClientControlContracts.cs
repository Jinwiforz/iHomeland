using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using IHomeland.Client.Networking.Application.Contracts;
using IHomeland.Client.PersonalWorld.Application;

namespace IHomeland.Client.Networking.Application.Control
{
    /// <summary>标识无 generated payload 的九类登记 control PUSH。</summary>
    internal enum ClientControlPushKind
    {
        /// <summary>维护窗口通知。</summary>
        Maintenance = 0,
        /// <summary>强制结束当前 Session。</summary>
        ForcedLogout = 1,
        /// <summary>准入队列进度。</summary>
        QueueStatus = 2,
        /// <summary>Endpoint manifest 完整替换。</summary>
        EndpointUpdate = 3,
        /// <summary>Session epoch 权威失效。</summary>
        SessionInvalidated = 4,
        /// <summary>PersonalWorld assignment hint。</summary>
        WorldAssignmentChanged = 5,
        /// <summary>收到一张访问邀请。</summary>
        VisitInvite = 6,
        /// <summary>Owner availability 变化。</summary>
        VisitOwnerAvailability = 7,
        /// <summary>VisitSession 控制面关闭通知。</summary>
        VisitClosed = 8,
    }

    /// <summary>保存所有 typed control PUSH 共享的低敏 envelope 字段。</summary>
    internal abstract class ClientControlNotification
    {
        /// <summary>创建不可变 control push 基类。</summary>
        /// <param name="kind">固定 typed route。</param>
        /// <param name="sequence">Connection 内严格连续 sequence。</param>
        /// <param name="timestampMilliseconds">服务端发送 Unix 毫秒时间。</param>
        protected ClientControlNotification(
            ClientControlPushKind kind,
            ulong sequence,
            long timestampMilliseconds)
        {
            Kind = kind;
            Sequence = sequence;
            TimestampMilliseconds = timestampMilliseconds;
        }

        /// <summary>获取固定 typed route。</summary>
        internal ClientControlPushKind Kind { get; }
        /// <summary>获取 connection 内 sequence。</summary>
        internal ulong Sequence { get; }
        /// <summary>获取服务端发送 Unix 毫秒时间。</summary>
        internal long TimestampMilliseconds { get; }
    }

    /// <summary>保存维护窗口通知。</summary>
    internal sealed class ClientMaintenancePush : ClientControlNotification
    {
        /// <summary>创建维护窗口通知。</summary>
        internal ClientMaintenancePush(ulong sequence, long timestampMilliseconds, long startsAtMilliseconds, long expectedEndAtMilliseconds, string messageKey)
            : base(ClientControlPushKind.Maintenance, sequence, timestampMilliseconds)
        {
            StartsAtMilliseconds = startsAtMilliseconds;
            ExpectedEndAtMilliseconds = expectedEndAtMilliseconds;
            MessageKey = messageKey ?? throw new ArgumentNullException(nameof(messageKey));
        }

        /// <summary>获取维护开始时间。</summary>
        internal long StartsAtMilliseconds { get; }
        /// <summary>获取预计结束时间。</summary>
        internal long ExpectedEndAtMilliseconds { get; }
        /// <summary>获取稳定本地化 key。</summary>
        internal string MessageKey { get; }
    }

    /// <summary>保存权威 Session 失效通知。</summary>
    internal sealed class ClientSessionInvalidationPush : ClientControlNotification
    {
        /// <summary>创建强制退出或 Session invalidated 通知。</summary>
        internal ClientSessionInvalidationPush(ClientControlPushKind kind, ulong sequence, long timestampMilliseconds, ulong sessionEpoch, string reasonKey)
            : base(kind, sequence, timestampMilliseconds)
        {
            if (kind != ClientControlPushKind.ForcedLogout &&
                kind != ClientControlPushKind.SessionInvalidated)
            {
                throw new ArgumentException("Session invalidation kind 无效。", nameof(kind));
            }

            SessionEpoch = sessionEpoch;
            ReasonKey = reasonKey ?? throw new ArgumentNullException(nameof(reasonKey));
        }

        /// <summary>获取使旧连接失效的新 epoch。</summary>
        internal ulong SessionEpoch { get; }
        /// <summary>获取稳定安全原因 key。</summary>
        internal string ReasonKey { get; }
    }

    /// <summary>保存准入队列瞬时观测。</summary>
    internal sealed class ClientQueueStatusPush : ClientControlNotification
    {
        /// <summary>创建队列状态通知。</summary>
        internal ClientQueueStatusPush(ulong sequence, long timestampMilliseconds, uint position, uint retryAfterMilliseconds)
            : base(ClientControlPushKind.QueueStatus, sequence, timestampMilliseconds)
        {
            Position = position;
            RetryAfterMilliseconds = retryAfterMilliseconds;
        }

        /// <summary>获取一基队列位置。</summary>
        internal uint Position { get; }
        /// <summary>获取下次检查最短等待毫秒数。</summary>
        internal uint RetryAfterMilliseconds { get; }
    }

    /// <summary>保存 endpoint manifest 完整替换。</summary>
    internal sealed class ClientEndpointUpdatePush : ClientControlNotification
    {
        /// <summary>创建 endpoint 更新通知并防御性复制集合。</summary>
        internal ClientEndpointUpdatePush(ulong sequence, long timestampMilliseconds, IEnumerable<ClientEndpoint> endpoints, long effectiveAtMilliseconds)
            : base(ClientControlPushKind.EndpointUpdate, sequence, timestampMilliseconds)
        {
            Endpoints = new ReadOnlyCollection<ClientEndpoint>(
                new List<ClientEndpoint>(endpoints ??
                    throw new ArgumentNullException(nameof(endpoints))));
            EffectiveAtMilliseconds = effectiveAtMilliseconds;
        }

        /// <summary>获取当前完整 endpoint manifest。</summary>
        internal IReadOnlyList<ClientEndpoint> Endpoints { get; }
        /// <summary>获取切换边界 Unix 毫秒时间。</summary>
        internal long EffectiveAtMilliseconds { get; }
    }

    /// <summary>保存 PersonalWorld assignment 控制面提示。</summary>
    internal sealed class ClientWorldAssignmentChangedPush : ClientControlNotification
    {
        /// <summary>创建 assignment 变化提示。</summary>
        internal ClientWorldAssignmentChangedPush(ulong sequence, long timestampMilliseconds, string personalWorldID, ClientWorldAssignmentProjection assignment, string reasonKey)
            : base(ClientControlPushKind.WorldAssignmentChanged, sequence, timestampMilliseconds)
        {
            PersonalWorldID = personalWorldID ??
                throw new ArgumentNullException(nameof(personalWorldID));
            Assignment = assignment;
            ReasonKey = reasonKey ?? string.Empty;
        }

        /// <summary>获取目标 PersonalWorld identity。</summary>
        internal string PersonalWorldID { get; }
        /// <summary>获取可选完整 assignment；缺失表示撤销。</summary>
        internal ClientWorldAssignmentProjection Assignment { get; }
        /// <summary>获取稳定安全原因 key。</summary>
        internal string ReasonKey { get; }
    }

    /// <summary>保存已映射的访问邀请。</summary>
    internal sealed class ClientVisitInvitePush : ClientControlNotification
    {
        /// <summary>创建邀请通知。</summary>
        internal ClientVisitInvitePush(ulong sequence, long timestampMilliseconds, ClientVisitInviteProjection invite)
            : base(ClientControlPushKind.VisitInvite, sequence, timestampMilliseconds)
        {
            Invite = invite ?? throw new ArgumentNullException(nameof(invite));
        }

        /// <summary>获取公开邀请 projection。</summary>
        internal ClientVisitInviteProjection Invite { get; }
    }

    /// <summary>保存 Owner availability 控制面提示。</summary>
    internal sealed class ClientVisitOwnerAvailabilityPush : ClientControlNotification
    {
        /// <summary>创建 availability 通知。</summary>
        internal ClientVisitOwnerAvailabilityPush(ulong sequence, long timestampMilliseconds, ClientVisitControlHint hint)
            : base(ClientControlPushKind.VisitOwnerAvailability, sequence, timestampMilliseconds)
        {
            Hint = hint ?? throw new ArgumentNullException(nameof(hint));
        }

        /// <summary>获取已降敏的控制面提示。</summary>
        internal ClientVisitControlHint Hint { get; }
    }

    /// <summary>保存 VisitSession 控制面关闭提示。</summary>
    internal sealed class ClientVisitClosedPush : ClientControlNotification
    {
        /// <summary>创建关闭通知。</summary>
        internal ClientVisitClosedPush(ulong sequence, long timestampMilliseconds, ClientVisitControlHint hint)
            : base(ClientControlPushKind.VisitClosed, sequence, timestampMilliseconds)
        {
            Hint = hint ?? throw new ArgumentNullException(nameof(hint));
        }

        /// <summary>获取已降敏的控制面提示。</summary>
        internal ClientVisitControlHint Hint { get; }
    }

    /// <summary>标识 control channel 的平台无关生命周期阶段。</summary>
    internal enum ClientControlHealthPhase
    {
        /// <summary>尚未初始化。</summary>
        Created = 0,
        /// <summary>已初始化但没有 active run。</summary>
        Idle = 1,
        /// <summary>正在建立 current attempt。</summary>
        Connecting = 2,
        /// <summary>Current attempt 已通过握手。</summary>
        Connected = 3,
        /// <summary>瞬时失败后正在等待有限重试。</summary>
        Recovering = 4,
        /// <summary>Current run 已结束但允许再次运行。</summary>
        Disconnected = 5,
        /// <summary>Current Session authority 已失效。</summary>
        SessionInvalidated = 6,
        /// <summary>App Scope 已停止。</summary>
        Stopped = 7,
    }

    /// <summary>标识 control run 终止的稳定、平台无关原因。</summary>
    internal enum ClientControlDisconnectKind
    {
        /// <summary>尚无终止结果。</summary>
        None = 0,
        /// <summary>调用方取消或 App Scope 停止。</summary>
        Requested = 1,
        /// <summary>连接、receive 或有限重试预算失败。</summary>
        Transport = 2,
        /// <summary>Frame、envelope、route 或主线程投递违反合同。</summary>
        Protocol = 3,
        /// <summary>Session authority 已失效。</summary>
        SessionInvalidated = 4,
        /// <summary>旧 connection 已被新 Session generation 取代。</summary>
        Superseded = 5,
        /// <summary>配置、ticket 或 handshake policy 拒绝运行。</summary>
        Policy = 6,
    }

    /// <summary>保存 control channel 的平台无关低敏 health。</summary>
    internal sealed class ClientControlHealthSnapshot
    {
        /// <summary>创建 health snapshot。</summary>
        internal ClientControlHealthSnapshot(
            ClientControlHealthPhase phase,
            ClientControlDisconnectKind disconnectKind,
            long generation,
            int attempt)
        {
            Phase = phase;
            DisconnectKind = disconnectKind;
            Generation = generation;
            Attempt = attempt;
        }

        /// <summary>获取当前是否已连接。</summary>
        internal bool Connected => Phase == ClientControlHealthPhase.Connected;
        /// <summary>获取 current run 是否已经终止。</summary>
        internal bool Terminal =>
            Phase == ClientControlHealthPhase.Disconnected ||
            Phase == ClientControlHealthPhase.SessionInvalidated ||
            Phase == ClientControlHealthPhase.Stopped;
        /// <summary>获取 current run 的生命周期阶段。</summary>
        internal ClientControlHealthPhase Phase { get; }
        /// <summary>获取 recent terminal 的稳定低敏分类。</summary>
        internal ClientControlDisconnectKind DisconnectKind { get; }
        /// <summary>获取 current run generation。</summary>
        internal long Generation { get; }
        /// <summary>获取 current run attempt。</summary>
        internal int Attempt { get; }
    }
}
