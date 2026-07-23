using System;
using System.Threading;
using System.Threading.Tasks;

namespace IHomeland.Client.Presentation.PersonalWorld
{
    /// <summary>串行化最高 authority 的 Session invalidation 到 Login 收敛。</summary>
    internal sealed class SessionInvalidationPresentationTransaction
    {
        /// <summary>唯一 Login 收敛 gate。</summary>
        private readonly SemaphoreSlim _gate = new SemaphoreSlim(1, 1);

        /// <summary>运行一笔 Scene/routes/session presentation 收敛。</summary>
        internal async Task ExecuteAsync(
            Func<Task> converge,
            CancellationToken cancellationToken)
        {
            if (converge == null)
            {
                throw new ArgumentNullException(nameof(converge));
            }

            await _gate.WaitAsync(cancellationToken);
            try
            {
                await converge();
            }
            finally
            {
                _gate.Release();
            }
        }
    }

    /// <summary>串行化 recovery snapshot 到 Scene/HUD/ConnectionLost 的表现提交。</summary>
    internal sealed class RecoveryPresentationTransaction
    {
        /// <summary>唯一 recovery presentation gate。</summary>
        private readonly SemaphoreSlim _gate = new SemaphoreSlim(1, 1);

        /// <summary>运行一笔 generation-scoped recovery presentation commit。</summary>
        internal async Task ExecuteAsync(
            Func<Task> converge,
            CancellationToken cancellationToken)
        {
            if (converge == null)
            {
                throw new ArgumentNullException(nameof(converge));
            }

            await _gate.WaitAsync(cancellationToken);
            try
            {
                await converge();
            }
            finally
            {
                _gate.Release();
            }
        }
    }
}
