using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.PersonalWorldCombat.Application;
using IHomeland.Client.Core.Foundation.Lifetime;
using UnityEngine;

namespace IHomeland.Client.PersonalWorldCombat.Infrastructure
{
    /// <summary>
    /// 把 network pump 已验证的 immutable battle projection 有界投递到唯一 Unity 主线程。
    /// </summary>
    /// <remarks>
    /// Binding 在 AppLifetime 初始化前完成。队列只保存解码后的 Application models，不保存
    /// raw datagram、ticket、key 或 Unity object；terminal 使用 MainThreadDispatcher 独立单槽。
    /// </remarks>
    internal sealed class ClientBattleInboundRouter :
        IClientBattleInboundSink,
        IAppLifetimeParticipant
    {
        /// <summary>限制普通 snapshot/event/resync 投递总项数。</summary>
        private const int QueueCapacity = 256;

        /// <summary>限制一次主线程 callback 内执行的 battle work items。</summary>
        private const int MaximumItemsPerDrain = 64;

        /// <summary>保护 sink binding、queue 与生命周期。</summary>
        private readonly object _sync = new object();

        /// <summary>复用既有 App Scope 唯一主线程 dispatcher。</summary>
        private readonly MainThreadDispatcher _dispatcher;

        /// <summary>保存可替换 latest raw snapshot 的固定容量队列。</summary>
        private readonly LinkedList<InboundWork> _queue =
            new LinkedList<InboundWork>();

        /// <summary>App Scope 唯一 runtime sink。</summary>
        private IClientBattleInboundSink _sink;

        /// <summary>表示队列已有一个 dispatcher drain callback owner。</summary>
        private bool _drainScheduled;

        /// <summary>表示 AppLifetime 已允许接收普通 work。</summary>
        private bool _initialized;

        /// <summary>表示 Router 已不可逆停止。</summary>
        private bool _stopped;

        /// <summary>
        /// 创建只依赖既有主线程队列的 battle inbound adapter。
        /// </summary>
        /// <param name="dispatcher">App Scope 唯一 MainThreadDispatcher。</param>
        internal ClientBattleInboundRouter(MainThreadDispatcher dispatcher)
        {
            _dispatcher = dispatcher ??
                throw new ArgumentNullException(nameof(dispatcher));
        }

        /// <summary>
        /// Snapshot 在主线程提交后的只读通知，供 connection owner 推进 baseline 状态。
        /// </summary>
        internal event Action<
            ClientBattleSnapshotPartition,
            ClientBattleSnapshotCommit> SnapshotCommitted;

        /// <summary>
        /// Main-thread sink 拒绝或抛出后通知 connection owner执行唯一 terminal。
        /// </summary>
        internal event Action<long, ClientBattleFailure> DispatchFailed;

        /// <summary>获取不泄露 payload 的 current receive queue item 数。</summary>
        internal int PendingItems
        {
            get
            {
                lock (_sync)
                {
                    return _queue.Count;
                }
            }
        }

        /// <summary>
        /// 在任何 connection activation 前绑定唯一 runtime sink。
        /// </summary>
        /// <param name="sink">Composition 创建的 App Scope runtime owner。</param>
        internal void Bind(IClientBattleInboundSink sink)
        {
            if (sink == null)
            {
                throw new ArgumentNullException(nameof(sink));
            }

            lock (_sync)
            {
                if (_sink != null)
                {
                    throw new InvalidOperationException(
                        "ClientBattleInboundRouter cannot rebind");
                }

                _sink = sink;
            }
        }

        /// <summary>允许 network pump 投递，但不产生 callback或网络副作用。</summary>
        /// <param name="cancellationToken">AppLifetime 初始化取消信号。</param>
        /// <returns>本地状态提交完成。</returns>
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            lock (_sync)
            {
                if (_initialized || _stopped || _sink == null)
                {
                    throw new InvalidOperationException(
                        "ClientBattleInboundRouter initialization is invalid");
                }

                _initialized = true;
            }

            return Task.CompletedTask;
        }

        /// <inheritdoc />
        public ClientBattleSnapshotCommit AcceptSnapshot(
            ClientBattleSnapshotPartition partition)
        {
            if (partition == null)
            {
                throw new ArgumentNullException(nameof(partition));
            }

            Enqueue(InboundWork.Snapshot(partition));
            return ClientBattleSnapshotCommit.Pending;
        }

        /// <inheritdoc />
        public bool AcceptEntityLifecycle(
            ClientBattleEntityLifecycle lifecycle)
        {
            if (lifecycle == null)
            {
                throw new ArgumentNullException(nameof(lifecycle));
            }

            Enqueue(InboundWork.Lifecycle(lifecycle));
            return true;
        }

        /// <inheritdoc />
        public bool AcceptAbilityEvent(
            ClientBattleAbilityEvent abilityEvent)
        {
            if (abilityEvent == null)
            {
                throw new ArgumentNullException(nameof(abilityEvent));
            }

            Enqueue(InboundWork.Ability(abilityEvent));
            return true;
        }

        /// <inheritdoc />
        public bool AcceptResyncResponse(
            ClientBattleResyncResponse response,
            long nowMilliseconds)
        {
            if (response == null || nowMilliseconds < 0)
            {
                throw new ArgumentException(
                    "Client battle resync dispatch is invalid");
            }

            Enqueue(InboundWork.Resync(response, nowMilliseconds));
            return true;
        }

        /// <inheritdoc />
        public void PublishTerminal(
            long battleGeneration,
            ClientBattleFailure failure)
        {
            if (battleGeneration <= 0 ||
                failure == ClientBattleFailure.None)
            {
                return;
            }

            IClientBattleInboundSink sink;
            lock (_sync)
            {
                if (!_initialized || _stopped)
                {
                    return;
                }

                sink = RequireSinkLocked();
                _queue.Clear();
                _drainScheduled = false;
            }

            Action publish = () =>
                sink.PublishTerminal(battleGeneration, failure);
            var posted = _dispatcher.TryPostCritical(publish);
            if (posted == DispatchPostResult.QueueFull)
            {
                posted = _dispatcher.TryPost(publish);
            }

            if (posted != DispatchPostResult.Accepted)
            {
                throw new ClientBattleInboundBackpressureException(
                    "Client battle terminal dispatcher slot is unavailable.");
            }
        }

        /// <summary>停止接收并清除尚未交给 Application owner 的 immutable work。</summary>
        /// <param name="cancellationToken">共享 AppLifetime stop deadline。</param>
        /// <returns>Queue ownership 已释放时完成。</returns>
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
                _initialized = false;
                _drainScheduled = false;
                _queue.Clear();
            }

            return Task.CompletedTask;
        }

        /// <summary>在hard cap内加入普通work，raw snapshot可替换同类latest项。</summary>
        /// <param name="work">已验证且不含raw secret的immutable work。</param>
        private void Enqueue(InboundWork work)
        {
            var mustSchedule = false;
            lock (_sync)
            {
                if (!_initialized || _stopped)
                {
                    throw new ClientBattleInboundBackpressureException(
                        "Client battle inbound router is not accepting work.");
                }

                if (_queue.Count >= QueueCapacity)
                {
                    if (!TryReplaceLatestSnapshotLocked(work))
                    {
                        throw new ClientBattleInboundBackpressureException(
                            "Client battle inbound queue reached its hard limit.");
                    }

                    return;
                }

                _queue.AddLast(work);
                if (!_drainScheduled)
                {
                    _drainScheduled = true;
                    mustSchedule = true;
                }
            }

            if (mustSchedule &&
                _dispatcher.TryPost(DrainOnMainThread) !=
                    DispatchPostResult.Accepted)
            {
                lock (_sync)
                {
                    _drainScheduled = false;
                    _queue.Clear();
                }

                throw new ClientBattleInboundBackpressureException(
                    "Client battle main-thread dispatcher rejected its drain.");
            }
        }

        /// <summary>
        /// Queue满时仅替换同generation同kind的latest raw snapshot，禁止delta覆盖首个full。
        /// </summary>
        /// <param name="replacement">待保留的newest work。</param>
        /// <returns>找到安全可替换snapshot时为true。</returns>
        private bool TryReplaceLatestSnapshotLocked(InboundWork replacement)
        {
            if (replacement.Kind != InboundWorkKind.Snapshot)
            {
                return false;
            }

            var node = _queue.Last;
            while (node != null)
            {
                var candidate = node.Value;
                if (candidate.Kind == InboundWorkKind.Snapshot &&
                    candidate.BattleGeneration ==
                        replacement.BattleGeneration &&
                    candidate.SnapshotPartition.Kind ==
                        replacement.SnapshotPartition.Kind)
                {
                    node.Value = replacement;
                    return true;
                }

                node = node.Previous;
            }

            return false;
        }

        /// <summary>在捕获的Unity主线程有界执行work，并按需续投下一批。</summary>
        private void DrainOnMainThread()
        {
            for (var index = 0; index < MaximumItemsPerDrain; index++)
            {
                InboundWork work;
                IClientBattleInboundSink sink;
                lock (_sync)
                {
                    if (_stopped || _queue.Count == 0)
                    {
                        _drainScheduled = false;
                        return;
                    }

                    sink = RequireSinkLocked();
                    work = _queue.First.Value;
                    _queue.RemoveFirst();
                }

                DispatchOne(sink, work);
            }

            var scheduleNext = false;
            lock (_sync)
            {
                if (!_stopped && _queue.Count > 0)
                {
                    scheduleNext = true;
                }
                else
                {
                    _drainScheduled = false;
                }
            }

            if (scheduleNext &&
                _dispatcher.TryPost(DrainOnMainThread) !=
                    DispatchPostResult.Accepted)
            {
                long generation;
                lock (_sync)
                {
                    generation = _queue.First?.Value.BattleGeneration ?? 0;
                    _queue.Clear();
                    _drainScheduled = false;
                }

                if (generation > 0)
                {
                    DispatchFailed?.Invoke(
                        generation,
                        ClientBattleFailure.Backpressure);
                }
            }
        }

        /// <summary>执行一个typed work并把稳定结果通知connection owner。</summary>
        /// <param name="sink">已绑定Application owner。</param>
        /// <param name="work">从queue移交的work。</param>
        private void DispatchOne(
            IClientBattleInboundSink sink,
            InboundWork work)
        {
            try
            {
                switch (work.Kind)
                {
                    case InboundWorkKind.Snapshot:
                        var commit = sink.AcceptSnapshot(
                            work.SnapshotPartition);
                        SnapshotCommitted?.Invoke(
                            work.SnapshotPartition,
                            commit);
                        break;
                    case InboundWorkKind.Lifecycle:
                        if (!sink.AcceptEntityLifecycle(work.LifecycleEvent))
                        {
                            throw new InvalidOperationException(
                                "Client battle lifecycle dispatch was rejected.");
                        }

                        break;
                    case InboundWorkKind.Ability:
                        if (!sink.AcceptAbilityEvent(work.AbilityEvent))
                        {
                            throw new InvalidOperationException(
                                "Client battle ability dispatch was rejected.");
                        }

                        break;
                    case InboundWorkKind.Resync:
                        if (!sink.AcceptResyncResponse(
                                work.ResyncResponse,
                                work.ObservedAtMilliseconds))
                        {
                            throw new InvalidOperationException(
                                "Client battle resync dispatch was rejected.");
                        }

                        break;
                    default:
                        throw new InvalidOperationException(
                            "Client battle inbound work kind is unknown.");
                }
            }
#if DEVELOPMENT_BUILD || UNITY_EDITOR
            catch (Exception exception)
#else
            catch (Exception)
#endif
            {
                lock (_sync)
                {
                    _queue.Clear();
                    _drainScheduled = false;
                }

#if DEVELOPMENT_BUILD || UNITY_EDITOR
                Debug.LogError(
                    "[IHOMELAND_BATTLE_TERMINAL] " +
                    $"generation={work.BattleGeneration} " +
                    $"stage=main-thread-{work.Kind} " +
                    $"exception={exception.GetType().Name} " +
                    $"reason={exception.Message}");
#endif
                DispatchFailed?.Invoke(
                    work.BattleGeneration,
                    ClientBattleFailure.Protocol);
            }
        }

        /// <summary>在持锁状态取得已完成Composition binding的sink。</summary>
        /// <returns>唯一Application runtime owner。</returns>
        private IClientBattleInboundSink RequireSinkLocked()
        {
            return _sink ?? throw new InvalidOperationException(
                "Client battle inbound sink is not bound");
        }

        /// <summary>标识queue中解码后的封闭work类别。</summary>
        private enum InboundWorkKind
        {
            /// <summary>Raw full/delta snapshot partition。</summary>
            Snapshot = 1,

            /// <summary>KCP entity lifecycle event。</summary>
            Lifecycle = 2,

            /// <summary>KCP reliable ability event。</summary>
            Ability = 3,

            /// <summary>KCP resync response。</summary>
            Resync = 4,
        }

        /// <summary>保存不含raw bytes或secret的单个inbound queue item。</summary>
        private sealed class InboundWork
        {
            /// <summary>创建字段互斥的typed work。</summary>
            private InboundWork(
                InboundWorkKind kind,
                long battleGeneration,
                ClientBattleSnapshotPartition snapshotPartition,
                ClientBattleEntityLifecycle lifecycleEvent,
                ClientBattleAbilityEvent abilityEvent,
                ClientBattleResyncResponse resyncResponse,
                long observedAtMilliseconds)
            {
                Kind = kind;
                BattleGeneration = battleGeneration;
                SnapshotPartition = snapshotPartition;
                LifecycleEvent = lifecycleEvent;
                AbilityEvent = abilityEvent;
                ResyncResponse = resyncResponse;
                ObservedAtMilliseconds = observedAtMilliseconds;
            }

            /// <summary>获取closed work kind。</summary>
            internal InboundWorkKind Kind { get; }

            /// <summary>获取generation fence。</summary>
            internal long BattleGeneration { get; }

            /// <summary>获取snapshot work；其他kind为空。</summary>
            internal ClientBattleSnapshotPartition SnapshotPartition { get; }

            /// <summary>获取lifecycle work；其他kind为空。</summary>
            internal ClientBattleEntityLifecycle LifecycleEvent { get; }

            /// <summary>获取ability work；其他kind为空。</summary>
            internal ClientBattleAbilityEvent AbilityEvent { get; }

            /// <summary>获取resync work；其他kind为空。</summary>
            internal ClientBattleResyncResponse ResyncResponse { get; }

            /// <summary>获取resync response已认证时的Unix毫秒。</summary>
            internal long ObservedAtMilliseconds { get; }

            /// <summary>创建raw snapshot work。</summary>
            /// <param name="partition">已验证partition。</param>
            /// <returns>Typed queue item。</returns>
            internal static InboundWork Snapshot(
                ClientBattleSnapshotPartition partition)
            {
                return new InboundWork(
                    InboundWorkKind.Snapshot,
                    partition.BattleGeneration,
                    partition,
                    null,
                    null,
                    null,
                    0);
            }

            /// <summary>创建entity lifecycle work。</summary>
            /// <param name="lifecycle">已验证event。</param>
            /// <returns>Typed queue item。</returns>
            internal static InboundWork Lifecycle(
                ClientBattleEntityLifecycle lifecycle)
            {
                return new InboundWork(
                    InboundWorkKind.Lifecycle,
                    lifecycle.BattleGeneration,
                    null,
                    lifecycle,
                    null,
                    null,
                    0);
            }

            /// <summary>创建ability event work。</summary>
            /// <param name="ability">已验证event。</param>
            /// <returns>Typed queue item。</returns>
            internal static InboundWork Ability(
                ClientBattleAbilityEvent ability)
            {
                return new InboundWork(
                    InboundWorkKind.Ability,
                    ability.BattleGeneration,
                    null,
                    null,
                    ability,
                    null,
                    0);
            }

            /// <summary>创建resync response work。</summary>
            /// <param name="response">已验证response。</param>
            /// <param name="observedAtMilliseconds">认证完成时间。</param>
            /// <returns>Typed queue item。</returns>
            internal static InboundWork Resync(
                ClientBattleResyncResponse response,
                long observedAtMilliseconds)
            {
                return new InboundWork(
                    InboundWorkKind.Resync,
                    response.BattleGeneration,
                    null,
                    null,
                    null,
                    response,
                    observedAtMilliseconds);
            }
        }
    }

    /// <summary>表示battle inbound或main-thread queue达到硬上限或已停止。</summary>
    internal sealed class ClientBattleInboundBackpressureException : Exception
    {
        /// <summary>创建不包含payload或identity的稳定queue failure。</summary>
        /// <param name="message">低敏固定错误文本。</param>
        internal ClientBattleInboundBackpressureException(string message)
            : base(message)
        {
        }
    }
}
