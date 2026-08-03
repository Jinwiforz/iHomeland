using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Battle;
using IHomeland.Client.Application.Session;
using IHomeland.Client.Foundation.Lifetime;

namespace IHomeland.Client.Application.World
{
    /// <summary>
    /// 从既有 Session、world flow 与 projection owners 原子派生 battle target intent。
    /// </summary>
    /// <remarks>
    /// 本 adapter 不保存第二份 world、VisitSession 或 assignment 事实；每次通知后消费者必须重新捕获。
    /// </remarks>
    internal sealed class ClientBattleWorldTargetSource :
        IClientBattleTargetSource,
        IAppLifetimeParticipant
    {
        /// <summary>保护 subscription 与不可逆停止状态。</summary>
        private readonly object _sync = new object();

        /// <summary>读取 current authenticated Session generation。</summary>
        private readonly SessionCoordinator _session;

        /// <summary>读取 current stable world flow generation。</summary>
        private readonly WorldAdmissionCoordinator _admission;

        /// <summary>读取 current PersonalWorld 与 assignment projection。</summary>
        private readonly PersonalWorldService _personalWorld;

        /// <summary>读取 current VisitSession role/revision binding。</summary>
        private readonly VisitSessionService _visitSession;

        /// <summary>表示 callbacks 已登记。</summary>
        private bool _subscribed;

        /// <summary>表示 App Scope 已永久停止。</summary>
        private bool _stopped;

        /// <summary>
        /// 创建只读 target adapter。
        /// </summary>
        internal ClientBattleWorldTargetSource(
            SessionCoordinator session,
            WorldAdmissionCoordinator admission,
            PersonalWorldService personalWorld,
            VisitSessionService visitSession)
        {
            _session = session ?? throw new ArgumentNullException(nameof(session));
            _admission = admission ?? throw new ArgumentNullException(nameof(admission));
            _personalWorld = personalWorld ??
                throw new ArgumentNullException(nameof(personalWorld));
            _visitSession = visitSession ??
                throw new ArgumentNullException(nameof(visitSession));
        }

        /// <inheritdoc />
        public event Action Changed;

        /// <inheritdoc />
        public bool TryCapture(out ClientBattleTargetIntent intent)
        {
            intent = null;
            lock (_sync)
            {
                if (_stopped)
                {
                    return false;
                }
            }

            if (!_session.TryGetCurrent(out var session))
            {
                return false;
            }

            var flow = _admission.Snapshot;
            if (flow.State != ClientWorldFlowState.OwnWorld &&
                flow.State != ClientWorldFlowState.Visiting)
            {
                return false;
            }

            var world = _personalWorld.Snapshot.CurrentWorld;
            var assignment = world?.Assignment;
            if (world == null ||
                assignment == null ||
                flow.TargetGeneration <= 0 ||
                assignment.Generation == 0 ||
                !string.Equals(
                    world.PersonalWorldID,
                    assignment.PersonalWorldID,
                    StringComparison.Ordinal))
            {
                return false;
            }

            if (flow.State == ClientWorldFlowState.OwnWorld)
            {
                intent = ClientBattleTargetIntent.OwnWorld(
                    session.Generation,
                    world.PersonalWorldID,
                    assignment.WorldInstanceID,
                    assignment.Generation);
                return true;
            }

            var visit = _visitSession.Snapshot.Current;
            if (visit == null ||
                visit.Role != ClientVisitRole.Visitor ||
                visit.Revision == 0 ||
                !string.Equals(
                    visit.VisitSessionID,
                    flow.VisitSessionID,
                    StringComparison.Ordinal) ||
                !visit.Assignment.HasSameIdentity(assignment))
            {
                return false;
            }

            intent = ClientBattleTargetIntent.VisitWorld(
                session.Generation,
                world.PersonalWorldID,
                assignment.WorldInstanceID,
                assignment.Generation,
                visit.VisitSessionID,
                visit.Revision);
            return true;
        }

        /// <inheritdoc />
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            lock (_sync)
            {
                if (_subscribed || _stopped)
                {
                    throw new InvalidOperationException(
                        "ClientBattleWorldTargetSource cannot initialize twice");
                }

                _admission.Changed += OnWorldChanged;
                _session.Invalidated += OnSessionInvalidated;
                _subscribed = true;
            }

            return Task.CompletedTask;
        }

        /// <inheritdoc />
        public Task StopAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            lock (_sync)
            {
                if (_stopped)
                {
                    return Task.CompletedTask;
                }

                _stopped = true;
                if (_subscribed)
                {
                    _admission.Changed -= OnWorldChanged;
                    _session.Invalidated -= OnSessionInvalidated;
                    _subscribed = false;
                }
            }

            return Task.CompletedTask;
        }

        /// <summary>把 world flow replacement 转换为无参数重新捕获通知。</summary>
        private void OnWorldChanged(ClientWorldFlowSnapshot snapshot)
        {
            NotifyChanged();
        }

        /// <summary>使旧 Session generation 的 battle target 立即不可捕获。</summary>
        private void OnSessionInvalidated(long generation)
        {
            NotifyChanged();
        }

        /// <summary>在 owner 锁外通知消费者，避免 callback 重入 source 临界区。</summary>
        private void NotifyChanged()
        {
            Action changed;
            lock (_sync)
            {
                if (_stopped || !_subscribed)
                {
                    return;
                }

                changed = Changed;
            }

            changed?.Invoke();
        }
    }
}
