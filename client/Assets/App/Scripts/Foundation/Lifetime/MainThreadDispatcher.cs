using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Foundation.Threading;

namespace IHomeland.Client.Foundation.Lifetime
{
    /// <summary>
    /// 在有界队列中接收跨线程 callback，并只允许捕获的 Unity 主线程执行它们。
    /// </summary>
    /// <remarks>
    /// 队列锁只保护接受状态和 callback 所有权，不在锁内执行外部代码。停止会拒绝后续投递并
    /// 清空尚未执行的 callback，使旧 App Scope 不能在销毁后回写 Unity 对象。
    /// </remarks>
    internal sealed class MainThreadDispatcher :
        IAppLifetimeParticipant,
        IClientMainThreadDispatcher
    {
        /// <summary>
        /// 保护队列、接受状态和容量不变量。
        /// </summary>
        private readonly object _sync = new object();

        /// <summary>
        /// 保存等待 Unity 主线程执行的 callback；队列长度永不超过 <see cref="_capacity"/>。
        /// </summary>
        private readonly Queue<Action> _callbacks = new Queue<Action>();

        /// <summary>为 terminal lifecycle 收敛保留的单一槽位，不与普通业务 callback 争用容量。</summary>
        private Action _criticalCallback;

        /// <summary>
        /// 保存允许执行 drain 的 Unity 主线程托管线程 ID。
        /// </summary>
        private readonly int _mainThreadId;

        /// <summary>
        /// 保存 callback 队列硬上限，用于阻止后台生产者无界占用内存。
        /// </summary>
        private readonly int _capacity;

        /// <summary>
        /// 表示该对象已经执行过一次初始化，停止后不得再次启动旧队列。
        /// </summary>
        private bool _initialized;

        /// <summary>
        /// 表示生命周期已初始化且尚未停止，只能在 <see cref="_sync"/> 内访问。
        /// </summary>
        private bool _accepting;

        /// <summary>
        /// 创建绑定指定主线程与固定容量的 Dispatcher。
        /// </summary>
        /// <param name="mainThreadId">Unity 主线程的托管线程 ID。</param>
        /// <param name="capacity">允许等待执行的最大 callback 数量。</param>
        /// <exception cref="ArgumentException">线程 ID 或容量非正数时抛出。</exception>
        internal MainThreadDispatcher(int mainThreadId, int capacity)
        {
            if (mainThreadId <= 0)
            {
                throw new ArgumentException("主线程 ID 必须为正数。", nameof(mainThreadId));
            }

            if (capacity <= 0)
            {
                throw new ArgumentException("Dispatcher 容量必须为正数。", nameof(capacity));
            }

            _mainThreadId = mainThreadId;
            _capacity = capacity;
        }

        /// <summary>
        /// 获取当前等待执行的 callback 数量快照。
        /// </summary>
        internal int PendingCount
        {
            get
            {
                lock (_sync)
                {
                    return _callbacks.Count + (_criticalCallback == null ? 0 : 1);
                }
            }
        }

        /// <summary>
        /// 在捕获的 Unity 主线程启用 callback 接收。
        /// </summary>
        /// <param name="cancellationToken">启动取消信号；进入接受状态前会检查一次。</param>
        /// <returns>接受状态已启用时完成的任务。</returns>
        /// <exception cref="InvalidOperationException">从非主线程初始化或重复初始化时抛出。</exception>
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            EnsureMainThread();
            lock (_sync)
            {
                if (_initialized)
                {
                    throw new InvalidOperationException("MainThreadDispatcher 已经初始化或停止，不能再次启动。");
                }

                _initialized = true;
                _accepting = true;
            }

            return Task.CompletedTask;
        }

        /// <summary>
        /// 停止接收并放弃尚未执行的 callback，避免旧 App Scope 在关闭后回写。
        /// </summary>
        /// <param name="cancellationToken">共享停止 deadline；同步清理不等待该信号，确保已排队工作始终被释放。</param>
        /// <returns>队列清空且接受状态关闭时完成的任务。</returns>
        public Task StopAsync(CancellationToken cancellationToken)
        {
            lock (_sync)
            {
                _accepting = false;
                _callbacks.Clear();
                _criticalCallback = null;
            }

            return Task.CompletedTask;
        }

        /// <summary>
        /// 尝试把 callback 的执行所有权转移到有界主线程队列。
        /// </summary>
        /// <param name="callback">将在捕获主线程执行的非空工作项；投递调用本身不会执行该 delegate。</param>
        /// <returns>明确说明已接受、容量已满或生命周期已停止的结果。</returns>
        /// <exception cref="ArgumentNullException">callback 为空时抛出。</exception>
        public DispatchPostResult TryPost(Action callback)
        {
            if (callback == null)
            {
                throw new ArgumentNullException(nameof(callback));
            }

            lock (_sync)
            {
                if (!_accepting)
                {
                    return DispatchPostResult.Stopped;
                }

                if (_callbacks.Count >= _capacity)
                {
                    return DispatchPostResult.QueueFull;
                }

                _callbacks.Enqueue(callback);
                return DispatchPostResult.Accepted;
            }
        }

        /// <summary>把 terminal lifecycle callback 投递到独立单槽，确保普通队列满时仍可 fail closed。</summary>
        /// <param name="callback">将在主线程执行的非空终态收敛工作。</param>
        /// <returns>已接受、终态槽已占用或生命周期已停止。</returns>
        /// <remarks>
        /// 此入口只用于连接终止等必须使产品状态立即不可交互的事件；不允许承载普通 PUSH、response
        /// 或可重试业务工作。单槽保持整体内存有界，也避免通过丢弃普通队列项伪造处理顺序。
        /// </remarks>
        public DispatchPostResult TryPostCritical(Action callback)
        {
            if (callback == null)
            {
                throw new ArgumentNullException(nameof(callback));
            }

            lock (_sync)
            {
                if (!_accepting)
                {
                    return DispatchPostResult.Stopped;
                }

                if (_criticalCallback != null)
                {
                    return DispatchPostResult.QueueFull;
                }

                _criticalCallback = callback;
                return DispatchPostResult.Accepted;
            }
        }

        /// <summary>
        /// 在 Unity 主线程有界执行当前批次 callback，并隔离每个 callback 的异常。
        /// </summary>
        /// <param name="maximumCallbacks">本次 Update 最多取出的 callback 数量，必须为正数。</param>
        /// <returns>本批执行数量和错误的不可变快照。</returns>
        /// <exception cref="ArgumentException">批次上限非正数时抛出。</exception>
        /// <exception cref="InvalidOperationException">从非捕获主线程调用时抛出。</exception>
        internal MainThreadDrainResult Drain(int maximumCallbacks)
        {
            if (maximumCallbacks <= 0)
            {
                throw new ArgumentException("单帧 drain 上限必须为正数。", nameof(maximumCallbacks));
            }

            EnsureMainThread();
            List<Exception> errors = null;
            var executedCount = 0;

            while (executedCount < maximumCallbacks)
            {
                Action callback;
                lock (_sync)
                {
                    if (!_accepting || _criticalCallback == null && _callbacks.Count == 0)
                    {
                        break;
                    }

                    if (_criticalCallback != null)
                    {
                        callback = _criticalCallback;
                        _criticalCallback = null;
                    }
                    else
                    {
                        callback = _callbacks.Dequeue();
                    }
                }

                try
                {
                    callback();
                }
                catch (Exception callbackError)
                {
                    errors ??= new List<Exception>();
                    errors.Add(callbackError);
                }

                executedCount++;
            }

            IReadOnlyList<Exception> capturedErrors = errors != null
                ? errors
                : Array.Empty<Exception>();
            return new MainThreadDrainResult(executedCount, capturedErrors);
        }

        /// <summary>
        /// 验证当前调用线程是创建 Dispatcher 时捕获的 Unity 主线程。
        /// </summary>
        /// <exception cref="InvalidOperationException">当前线程不是捕获主线程时抛出。</exception>
        private void EnsureMainThread()
        {
            if (Environment.CurrentManagedThreadId != _mainThreadId)
            {
                throw new InvalidOperationException("MainThreadDispatcher 只能在捕获的 Unity 主线程执行此操作。");
            }
        }
    }
}
