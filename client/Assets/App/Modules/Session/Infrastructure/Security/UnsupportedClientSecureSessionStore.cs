using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Session.Application;

namespace IHomeland.Client.Session.Infrastructure.Security
{
    /// <summary>
    /// 为未登记平台提供显式Unsupported结果，禁止降级到明文或自制加密。
    /// </summary>
    internal sealed class UnsupportedClientSecureSessionStore : IClientSecureSessionStore
    {
        /// <summary>记录生命周期状态，防止停止后被重新启用。</summary>
        private StoreState _state = StoreState.Created;

        /// <inheritdoc />
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (_state != StoreState.Created)
            {
                throw new System.InvalidOperationException(
                    "Unsupported secure session store 不能重复初始化。");
            }

            _state = StoreState.Ready;
            return Task.CompletedTask;
        }

        /// <inheritdoc />
        public Task<ClientSecureSessionStoreResult<ClientSecureSessionRecord>> ReadAsync(
            CancellationToken cancellationToken)
        {
            return Task.FromResult(ClientSecureSessionStoreResult<ClientSecureSessionRecord>.Failed(
                Outcome(cancellationToken)));
        }

        /// <inheritdoc />
        public Task<ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>> ReplaceAsync(
            ClientSecureSessionRecord record,
            CancellationToken cancellationToken)
        {
            return Task.FromResult(ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>.Failed(
                Outcome(cancellationToken)));
        }

        /// <inheritdoc />
        public Task<ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>> DeleteAsync(
            CancellationToken cancellationToken)
        {
            return Task.FromResult(ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>.Failed(
                Outcome(cancellationToken)));
        }

        /// <inheritdoc />
        public Task StopAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            _state = StoreState.Stopped;
            return Task.CompletedTask;
        }

        /// <summary>将当前生命周期与取消信号映射为封闭结果。</summary>
        /// <param name="cancellationToken">调用方取消信号。</param>
        /// <returns>Cancelled、Stopped或Unsupported。</returns>
        private ClientSecureSessionStoreOutcome Outcome(CancellationToken cancellationToken)
        {
            if (cancellationToken.IsCancellationRequested)
            {
                return ClientSecureSessionStoreOutcome.Cancelled;
            }

            return _state == StoreState.Stopped
                ? ClientSecureSessionStoreOutcome.Stopped
                : ClientSecureSessionStoreOutcome.Unsupported;
        }

        /// <summary>限制 Unsupported adapter 生命周期。</summary>
        private enum StoreState
        {
            /// <summary>尚未初始化。</summary>
            Created = 0,

            /// <summary>已经初始化并稳定返回 Unsupported。</summary>
            Ready = 1,

            /// <summary>已经停止。</summary>
            Stopped = 2,
        }
    }
}
