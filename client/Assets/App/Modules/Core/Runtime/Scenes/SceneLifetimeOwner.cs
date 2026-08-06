using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Core.Foundation.Lifetime;

namespace IHomeland.Client.Core.Runtime.Scenes
{
    /// <summary>
    /// 在 App Scope 内唯一拥有活动 Scene Scope generation 与取消顺序。
    /// </summary>
    /// <remarks>
    /// owner 在锁内先替换 current generation，再在锁外触发旧 token 取消，避免取消 callback
    /// 重入状态锁。停止后永久拒绝新 Scene Scope，旧对象图不能再次激活场景。
    /// </remarks>
    internal sealed class SceneLifetimeOwner : IAppLifetimeParticipant
    {
        /// <summary>
        /// 保护运行状态、generation 计数器与 current Scene Scope。
        /// </summary>
        private readonly object _sync = new object();

        /// <summary>
        /// 保存最后一次分配的 generation，只能在 <see cref="_sync"/> 内递增。
        /// </summary>
        private long _lastGeneration;

        /// <summary>
        /// 保存当前活动 Scene Scope；切换时先从 owner 中移除再取消。
        /// </summary>
        private SceneLifetime _current;

        /// <summary>
        /// 表示该 owner 已经初始化过，停止后不得复用旧 generation 空间。
        /// </summary>
        private bool _initialized;

        /// <summary>
        /// 表示生命周期已经初始化且允许创建 Scene Scope。
        /// </summary>
        private bool _accepting;

        /// <summary>
        /// 在 App Scope 初始化阶段启用 Scene Scope 创建。
        /// </summary>
        /// <param name="cancellationToken">启动取消信号；启用前检查一次。</param>
        /// <returns>owner 可以创建 Scene Scope 时完成的任务。</returns>
        /// <exception cref="InvalidOperationException">重复初始化旧 owner 时抛出。</exception>
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            lock (_sync)
            {
                if (_initialized)
                {
                    throw new InvalidOperationException("SceneLifetimeOwner 已经初始化或停止，不能再次启动。");
                }

                _initialized = true;
                _accepting = true;
            }

            return Task.CompletedTask;
        }

        /// <summary>
        /// 永久停止新 Scene Scope 创建，并取消当前 Scene Scope。
        /// </summary>
        /// <param name="cancellationToken">共享停止 deadline；同步失效不等待该信号。</param>
        /// <returns>当前 Scene Scope 已失效并释放时完成的任务。</returns>
        public Task StopAsync(CancellationToken cancellationToken)
        {
            SceneLifetime current;
            lock (_sync)
            {
                _accepting = false;
                current = _current;
                _current = null;
            }

            try
            {
                current?.CancelAndDispose();
                return Task.CompletedTask;
            }
            catch (Exception cancellationError)
            {
                return Task.FromException(cancellationError);
            }
        }

        /// <summary>
        /// 激活新的 Scene Scope，并在锁外使上一代 token 失效。
        /// </summary>
        /// <returns>拥有新 generation 和取消 token 的 Scene lifetime。</returns>
        /// <exception cref="InvalidOperationException">App Scope 尚未运行、已经停止或 generation 溢出时抛出。</exception>
        internal SceneLifetime BeginScene()
        {
            SceneLifetime previous;
            SceneLifetime current;
            lock (_sync)
            {
                if (!_accepting)
                {
                    throw new InvalidOperationException("App Scope 未运行或已停止，不能创建 Scene Scope。");
                }

                try
                {
                    _lastGeneration = checked(_lastGeneration + 1);
                }
                catch (OverflowException overflowError)
                {
                    throw new InvalidOperationException("Scene generation 已耗尽，旧 App Scope 必须停止。", overflowError);
                }

                previous = _current;
                current = new SceneLifetime(this, _lastGeneration);
                _current = current;
            }

            try
            {
                previous?.CancelAndDispose();
            }
            catch (Exception previousCancellationError)
            {
                // 新一代尚未交给调用方，旧代取消失败时必须同步撤销，避免遗留不可达 current。
                try
                {
                    Release(current);
                }
                catch (Exception currentCancellationError)
                {
                    throw new AggregateException(
                        "切换 Scene Scope 时旧代和未交付新代均取消失败。",
                        previousCancellationError,
                        currentCancellationError);
                }

                throw;
            }

            return current;
        }

        /// <summary>
        /// 判断指定 generation 是否仍是当前可写 Scene Scope。
        /// </summary>
        /// <param name="generation">callback 捕获的 Scene Scope generation。</param>
        /// <returns>owner 正在运行且 generation 与 current Scene Scope 相同时返回 true。</returns>
        internal bool IsCurrent(long generation)
        {
            lock (_sync)
            {
                return _accepting && _current != null && _current.Generation == generation;
            }
        }

        /// <summary>
        /// 从 owner 移除指定 Scene Scope，并在锁外执行幂等取消和释放。
        /// </summary>
        /// <param name="lifetime">需要释放的非空 Scene lifetime；可以已经被新一代替换。</param>
        /// <exception cref="ArgumentNullException">lifetime 为空时抛出。</exception>
        internal void Release(SceneLifetime lifetime)
        {
            if (lifetime == null)
            {
                throw new ArgumentNullException(nameof(lifetime));
            }

            lock (_sync)
            {
                if (ReferenceEquals(_current, lifetime))
                {
                    _current = null;
                }
            }

            lifetime.CancelAndDispose();
        }
    }
}
