using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Session.Application;

namespace IHomeland.Client.PersonalWorld.Application
{
    /// <summary>标识冻结恢复目标类别。</summary>
    internal enum ClientRecoveryTargetKind
    {
        /// <summary>当前玩家自己的PersonalWorld。</summary>
        OwnWorld = 0,

        /// <summary>当前Visitor仍有权重连的VisitSession。</summary>
        Visiting = 1,
    }

    /// <summary>标识唯一恢复owner的产品阶段。</summary>
    internal enum ClientConnectionRecoveryPhase
    {
        /// <summary>没有恢复intent。</summary>
        Idle = 0,

        /// <summary>Control adapter正在恢复且control-only能力被冻结。</summary>
        RecoveringControl = 1,

        /// <summary>正在重建权威gameplay target。</summary>
        RecoveringWorld = 2,

        /// <summary>Target已提交，等待Scene/HUD generation确认。</summary>
        AwaitingSceneCommit = 3,

        /// <summary>自动恢复已得出稳定失败，允许一次manual intent。</summary>
        ConnectionLost = 4,

        /// <summary>App Scope已停止。</summary>
        Stopped = 5,
    }

    /// <summary>标识恢复attempt的稳定低敏结果。</summary>
    internal enum ClientConnectionRecoveryResultKind
    {
        /// <summary>尚无terminal结果。</summary>
        None = 0,

        /// <summary>权威target或control snapshot已收敛。</summary>
        Succeeded = 1,

        /// <summary>瞬时transport预算耗尽。</summary>
        Transport = 2,

        /// <summary>协议、identity或revision验证失败。</summary>
        Protocol = 3,

        /// <summary>Session已失效或refresh commit unknown。</summary>
        Authentication = 4,

        /// <summary>当前状态不允许恢复旧目标。</summary>
        Policy = 5,

        /// <summary>总deadline到期。</summary>
        Deadline = 6,

        /// <summary>权威结果要求退役Visitor并返回自己的世界。</summary>
        ReturningOwnWorld = 7,

        /// <summary>App Scope停止。</summary>
        Stopped = 8,

        /// <summary>未登记异常被封闭为内部失败。</summary>
        Internal = 9,
    }

    /// <summary>保存不含credential或endpoint的权威恢复目标。</summary>
    internal sealed class ClientRecoveryTargetDescriptor
    {
        /// <summary>创建经过组合验证的冻结恢复目标。</summary>
        /// <param name="kind">OwnWorld或Visiting。</param>
        /// <param name="sessionGeneration">建立目标时的Session generation。</param>
        /// <param name="sessionID">建立目标时的服务端Session identity。</param>
        /// <param name="sessionEpoch">建立目标时的服务端Session epoch。</param>
        /// <param name="targetGeneration">World flow target generation。</param>
        /// <param name="personalWorldID">目标PersonalWorld identity。</param>
        /// <param name="worldInstanceID">目标WorldInstance identity。</param>
        /// <param name="visitSessionID">Visitor target或OwnWorld上已打开的VisitSession identity。</param>
        /// <param name="worldRevision">最高PersonalWorld revision。</param>
        /// <param name="visitRevision">存在VisitSession时的最高aggregate revision；否则为0。</param>
        /// <param name="assignmentGeneration">最高runtime assignment generation。</param>
        /// <param name="reconnectExpiresAtMilliseconds">Visitor公开重连上界；OwnWorld为0。</param>
        internal ClientRecoveryTargetDescriptor(
            ClientRecoveryTargetKind kind,
            long sessionGeneration,
            string sessionID,
            long sessionEpoch,
            long targetGeneration,
            string personalWorldID,
            string worldInstanceID,
            string visitSessionID,
            ulong worldRevision,
            ulong visitRevision,
            ulong assignmentGeneration,
            long reconnectExpiresAtMilliseconds)
        {
            if (sessionGeneration <= 0 || string.IsNullOrWhiteSpace(sessionID) ||
                sessionEpoch <= 0 || targetGeneration <= 0 ||
                string.IsNullOrWhiteSpace(personalWorldID) ||
                string.IsNullOrWhiteSpace(worldInstanceID) || worldRevision == 0 ||
                assignmentGeneration == 0)
            {
                throw new ArgumentException("Recovery target基础字段无效。");
            }

            if (kind == ClientRecoveryTargetKind.Visiting)
            {
                if (string.IsNullOrWhiteSpace(visitSessionID) ||
                    visitRevision == 0 ||
                    reconnectExpiresAtMilliseconds <= 0)
                {
                    throw new ArgumentException("Visiting recovery target字段无效。");
                }
            }
            else if (kind != ClientRecoveryTargetKind.OwnWorld ||
                     string.IsNullOrEmpty(visitSessionID) != (visitRevision == 0) ||
                     reconnectExpiresAtMilliseconds != 0)
            {
                throw new ArgumentException("OwnWorld recovery target字段无效。");
            }

            Kind = kind;
            SessionGeneration = sessionGeneration;
            SessionID = sessionID;
            SessionEpoch = sessionEpoch;
            TargetGeneration = targetGeneration;
            PersonalWorldID = personalWorldID;
            WorldInstanceID = worldInstanceID;
            VisitSessionID = visitSessionID;
            WorldRevision = worldRevision;
            VisitRevision = visitRevision;
            AssignmentGeneration = assignmentGeneration;
            ReconnectExpiresAtMilliseconds = reconnectExpiresAtMilliseconds;
        }

        /// <summary>获取目标类别。</summary>
        internal ClientRecoveryTargetKind Kind { get; }

        /// <summary>获取来源Session generation。</summary>
        internal long SessionGeneration { get; }

        /// <summary>获取不得写入日志的服务端Session identity。</summary>
        internal string SessionID { get; }

        /// <summary>获取服务端Session epoch。</summary>
        internal long SessionEpoch { get; }

        /// <summary>获取来源target generation。</summary>
        internal long TargetGeneration { get; }

        /// <summary>获取PersonalWorld identity。</summary>
        internal string PersonalWorldID { get; }

        /// <summary>获取不可把迟到连接复活为current的WorldInstance identity。</summary>
        internal string WorldInstanceID { get; }

        /// <summary>获取可选VisitSession identity。</summary>
        internal string VisitSessionID { get; }

        /// <summary>获取最高PersonalWorld revision。</summary>
        internal ulong WorldRevision { get; }

        /// <summary>获取最高VisitSession revision；无VisitSession时为0。</summary>
        internal ulong VisitRevision { get; }

        /// <summary>获取最高assignment generation。</summary>
        internal ulong AssignmentGeneration { get; }

        /// <summary>获取Visitor重连绝对deadline，单位Unix毫秒。</summary>
        internal long ReconnectExpiresAtMilliseconds { get; }

        /// <summary>判断current snapshot是否仍属于冻结目标的同一服务端Session血统。</summary>
        /// <param name="session">唯一Session owner的current snapshot。</param>
        /// <returns>Session identity与epoch相同且generation未倒退时返回true。</returns>
        internal bool IsBoundTo(ClientSessionSnapshot session)
        {
            return session != null && session.Generation >= SessionGeneration &&
                   string.Equals(session.Session.SessionID, SessionID, StringComparison.Ordinal) &&
                   session.Session.SessionEpoch == SessionEpoch;
        }

        /// <summary>把同一服务端Session上的token轮换重绑定为新的本地generation。</summary>
        /// <param name="session">唯一Session owner的current snapshot。</param>
        /// <param name="descriptor">成功时返回保留全部世界事实的新descriptor；generation未变化时返回当前实例。</param>
        /// <returns>仅同一Session血统且generation未倒退时返回true。</returns>
        internal bool TryRebindTo(
            ClientSessionSnapshot session,
            out ClientRecoveryTargetDescriptor descriptor)
        {
            descriptor = null;
            if (!IsBoundTo(session))
            {
                return false;
            }

            if (session.Generation == SessionGeneration)
            {
                descriptor = this;
                return true;
            }

            descriptor = new ClientRecoveryTargetDescriptor(
                Kind,
                session.Generation,
                SessionID,
                SessionEpoch,
                TargetGeneration,
                PersonalWorldID,
                WorldInstanceID,
                VisitSessionID,
                WorldRevision,
                VisitRevision,
                AssignmentGeneration,
                ReconnectExpiresAtMilliseconds);
            return true;
        }

        /// <summary>判断另一个descriptor是否来自同一服务端Session血统。</summary>
        /// <param name="other">待比较的恢复目标。</param>
        /// <returns>Session identity与epoch均一致时返回true。</returns>
        internal bool HasSameSessionLineage(ClientRecoveryTargetDescriptor other)
        {
            return other != null &&
                   string.Equals(other.SessionID, SessionID, StringComparison.Ordinal) &&
                   other.SessionEpoch == SessionEpoch;
        }

        /// <summary>返回不包含业务identity的低敏摘要。</summary>
        /// <returns>只包含kind与generation。</returns>
        public override string ToString()
        {
            return $"ClientRecoveryTarget[REDACTED] kind={Kind} targetGeneration={TargetGeneration}";
        }
    }

    /// <summary>保存恢复owner唯一不可变状态。</summary>
    internal sealed class ClientConnectionRecoverySnapshot
    {
        /// <summary>创建低敏恢复状态。</summary>
        /// <param name="phase">当前产品阶段。</param>
        /// <param name="intentGeneration">每笔automatic/manual intent递增。</param>
        /// <param name="sourceChannelGeneration">触发恢复的channel generation。</param>
        /// <param name="targetGeneration">冻结target generation；control-only恢复为0。</param>
        /// <param name="result">当前稳定结果。</param>
        /// <param name="manual">当前intent是否由玩家显式发起。</param>
        internal ClientConnectionRecoverySnapshot(
            ClientConnectionRecoveryPhase phase,
            long intentGeneration,
            long sourceChannelGeneration,
            long targetGeneration,
            ClientConnectionRecoveryResultKind result,
            bool manual)
        {
            Phase = phase;
            IntentGeneration = intentGeneration;
            SourceChannelGeneration = sourceChannelGeneration;
            TargetGeneration = targetGeneration;
            Result = result;
            Manual = manual;
        }

        /// <summary>获取当前阶段。</summary>
        internal ClientConnectionRecoveryPhase Phase { get; }

        /// <summary>获取恢复intent generation。</summary>
        internal long IntentGeneration { get; }

        /// <summary>获取来源channel generation。</summary>
        internal long SourceChannelGeneration { get; }

        /// <summary>获取冻结target generation。</summary>
        internal long TargetGeneration { get; }

        /// <summary>获取稳定结果。</summary>
        internal ClientConnectionRecoveryResultKind Result { get; }

        /// <summary>获取是否为manual intent。</summary>
        internal bool Manual { get; }
    }

    /// <summary>定义恢复owner消费的窄权威操作边界。</summary>
    internal interface IClientConnectionRecoveryOperations
    {
        /// <summary>捕获最近稳定target，不创建第二份业务事实。</summary>
        /// <param name="descriptor">成功时返回冻结descriptor。</param>
        /// <returns>存在可恢复稳定target时返回true。</returns>
        bool TryCaptureTarget(out ClientRecoveryTargetDescriptor descriptor);

        /// <summary>使旧control-only inbox/hint立即失效。</summary>
        void InvalidateControlOnlyState();

        /// <summary>在新control generation后通过健康gameplay收敛完整snapshot。</summary>
        /// <param name="descriptor">断线前冻结target。</param>
        /// <param name="cancellationToken">唯一恢复总deadline。</param>
        /// <returns>稳定低敏结果。</returns>
        Task<ClientConnectionRecoveryResultKind> ReconcileControlAsync(
            ClientRecoveryTargetDescriptor descriptor,
            CancellationToken cancellationToken);

        /// <summary>按OwnWorld或Visitor RECONNECT计划恢复target。</summary>
        /// <param name="descriptor">断线前冻结target。</param>
        /// <param name="cancellationToken">唯一恢复总deadline。</param>
        /// <returns>稳定低敏结果。</returns>
        Task<ClientConnectionRecoveryResultKind> RecoverWorldAsync(
            ClientRecoveryTargetDescriptor descriptor,
            CancellationToken cancellationToken);

        /// <summary>验证恢复后target仍对应同一意图并返回current generation。</summary>
        /// <param name="descriptor">来源descriptor。</param>
        /// <param name="currentTargetGeneration">成功时current target generation。</param>
        /// <returns>Identity、revision与assignment均合法收敛时返回true。</returns>
        bool TryValidateRecoveredTarget(
            ClientRecoveryTargetDescriptor descriptor,
            out long currentTargetGeneration);
    }
}
