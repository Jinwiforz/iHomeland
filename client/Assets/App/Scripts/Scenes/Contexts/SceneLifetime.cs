using System;
using System.Threading;

namespace IHomeland.Client.Scenes.Contexts
{
    /// <summary>
    /// 表示一个 Scene Scope 的 generation 与取消所有权。
    /// </summary>
    /// <remarks>
    /// token 在构造时缓存，因此释放后仍可安全读取取消状态。迟到 callback 应在 Unity 主线程
    /// 写入场景对象前检查 <see cref="CanCommit"/>，而不是仅依赖异步操作是否自行响应取消。
    /// </remarks>
    internal sealed class SceneLifetime : IDisposable
    {
        /// <summary>
        /// 保护取消源只被取消和释放一次。
        /// </summary>
        private readonly object _sync = new object();

        /// <summary>
        /// 保存创建当前 generation 的唯一 App Scope owner。
        /// </summary>
        private readonly SceneLifetimeOwner _owner;

        /// <summary>
        /// 保存当前 Scene Scope 的取消源，只由本实例释放。
        /// </summary>
        private readonly CancellationTokenSource _cancellationSource = new CancellationTokenSource();

        /// <summary>
        /// 缓存取消 token，使取消源释放后迟到 callback 仍能读取状态。
        /// </summary>
        private readonly CancellationToken _cancellationToken;

        /// <summary>
        /// 表示取消源已经失效并释放，只能在 <see cref="_sync"/> 内访问。
        /// </summary>
        private bool _disposed;

        /// <summary>
        /// 创建由指定 owner 管理的 Scene Scope generation。
        /// </summary>
        /// <param name="owner">负责切换和判定当前 generation 的 App Scope owner。</param>
        /// <param name="generation">由 owner 单调分配的正整数代际。</param>
        /// <exception cref="ArgumentException">generation 非正数时抛出。</exception>
        /// <exception cref="ArgumentNullException">owner 为空时抛出。</exception>
        internal SceneLifetime(SceneLifetimeOwner owner, long generation)
        {
            _owner = owner ?? throw new ArgumentNullException(nameof(owner));
            if (generation <= 0)
            {
                throw new ArgumentException("Scene generation 必须为正数。", nameof(generation));
            }

            Generation = generation;
            _cancellationToken = _cancellationSource.Token;
        }

        /// <summary>
        /// 获取由 App Scope 单调分配的 Scene Scope 代际。
        /// </summary>
        internal long Generation { get; }

        /// <summary>
        /// 获取在场景替换、卸载或 App Scope 停止时取消的 token。
        /// </summary>
        internal CancellationToken CancellationToken => _cancellationToken;

        /// <summary>
        /// 获取当前 callback 是否仍可向该 Scene Scope 提交结果。
        /// </summary>
        /// <remarks>
        /// 这是瞬时快照。场景对象写入仍须在 Unity 主线程完成，并在紧邻写入处检查该值。
        /// </remarks>
        internal bool CanCommit => !_cancellationToken.IsCancellationRequested && _owner.IsCurrent(Generation);

        /// <summary>
        /// 使当前 Scene Scope 失效；重复调用保持幂等。
        /// </summary>
        /// <remarks>取消 callback 的异常会向调用方传播，但取消源仍会在 finally 中释放。</remarks>
        public void Dispose()
        {
            _owner.Release(this);
        }

        /// <summary>
        /// 取消并释放当前 Scene Scope 拥有的取消源，且不再次通知 owner。
        /// </summary>
        /// <remarks>由 owner 在切换或释放 generation 后调用，外部取消 callback 不会在 owner 锁内执行。</remarks>
        internal void CancelAndDispose()
        {
            lock (_sync)
            {
                if (_disposed)
                {
                    return;
                }

                _disposed = true;
                try
                {
                    _cancellationSource.Cancel(throwOnFirstException: false);
                }
                finally
                {
                    _cancellationSource.Dispose();
                }
            }
        }
    }
}
