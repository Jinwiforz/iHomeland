using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Application.Bootstrap;
using IHomeland.Client.Foundation.Lifetime;
using IHomeland.Client.Application.Contracts;

namespace IHomeland.Client.Application.Session
{
    /// <summary>
    /// 在App启动时唯一执行Read、bootstrap、refresh、replace与session commit。
    /// </summary>
    /// <remarks>
    /// Coordinator不保存第二份credential或Session事实；完成后只保留低敏终态。全部网络与存储
    /// 操作共享单一总deadline，且同一实例不能启动第二次restore flight。
    /// </remarks>
    internal sealed class ClientSessionRestoreCoordinator : IAppLifetimeParticipant
    {
        /// <summary>限制完整启动恢复总预算。</summary>
        internal static readonly TimeSpan DefaultRestoreDeadline = TimeSpan.FromSeconds(35);

        /// <summary>保护生命周期、结果与run cancellation owner。</summary>
        private readonly object _sync = new object();

        /// <summary>唯一secure record owner。</summary>
        private readonly IClientSecureSessionStore _secureSessionStore;

        /// <summary>认证前version/config owner。</summary>
        private readonly ClientBootstrapService _bootstrapService;

        /// <summary>唯一Session owner。</summary>
        private readonly SessionCoordinator _sessionCoordinator;

        /// <summary>完整恢复总deadline。</summary>
        private readonly TimeSpan _restoreDeadline;

        /// <summary>当前restore run cancellation owner。</summary>
        private CancellationTokenSource _runCancellation;

        /// <summary>Initialize是否已经被调用。</summary>
        private bool _initialized;

        /// <summary>App Scope是否已停止。</summary>
        private bool _stopped;

        /// <summary>已经得出的唯一低敏终态。</summary>
        private ClientSessionRestoreResult _result;

        /// <summary>创建启动恢复owner。</summary>
        /// <param name="secureSessionStore">唯一secure record store。</param>
        /// <param name="bootstrapService">唯一bootstrap用例。</param>
        /// <param name="sessionCoordinator">唯一Session owner。</param>
        /// <param name="restoreDeadline">Read到commit的正总预算。</param>
        internal ClientSessionRestoreCoordinator(
            IClientSecureSessionStore secureSessionStore,
            ClientBootstrapService bootstrapService,
            SessionCoordinator sessionCoordinator,
            TimeSpan restoreDeadline)
        {
            _secureSessionStore = secureSessionStore ??
                throw new ArgumentNullException(nameof(secureSessionStore));
            _bootstrapService = bootstrapService ?? throw new ArgumentNullException(nameof(bootstrapService));
            _sessionCoordinator = sessionCoordinator ??
                throw new ArgumentNullException(nameof(sessionCoordinator));
            if (restoreDeadline <= TimeSpan.Zero)
            {
                throw new ArgumentException("Session restore deadline必须为正数。", nameof(restoreDeadline));
            }

            _restoreDeadline = restoreDeadline;
        }

        /// <summary>获取已经完成的低敏恢复终态；执行中或未启动时为空。</summary>
        internal ClientSessionRestoreResult Result
        {
            get
            {
                lock (_sync)
                {
                    return _result;
                }
            }
        }

        /// <summary>执行唯一有界启动恢复，并把非异常业务结果封闭为Result。</summary>
        /// <param name="cancellationToken">AppLifetime启动取消信号。</param>
        /// <returns>唯一restore flight完成时结束。</returns>
        public async Task InitializeAsync(CancellationToken cancellationToken)
        {
            CancellationToken token;
            lock (_sync)
            {
                if (_initialized || _stopped)
                {
                    throw new InvalidOperationException(
                        "ClientSessionRestoreCoordinator不能重复初始化或停止后重启。");
                }

                _initialized = true;
                _runCancellation = CancellationTokenSource.CreateLinkedTokenSource(
                    cancellationToken);
                _runCancellation.CancelAfter(_restoreDeadline);
                token = _runCancellation.Token;
            }

            var result = await ExecuteAsync(token);
            lock (_sync)
            {
                _result = result;
            }
        }

        /// <summary>停止restore owner并拒绝任何迟到终态覆盖。</summary>
        /// <param name="cancellationToken">同步停止前检查的生命周期信号。</param>
        /// <returns>Run cancellation已发布时完成。</returns>
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
                _runCancellation?.Cancel();
                _runCancellation?.Dispose();
                _runCancellation = null;
            }

            return Task.CompletedTask;
        }

        /// <summary>按固定顺序执行一次恢复，不自动重试或使用延时修正状态。</summary>
        /// <param name="cancellationToken">完整恢复总deadline与生命周期组合信号。</param>
        /// <returns>稳定低敏恢复终态。</returns>
        private async Task<ClientSessionRestoreResult> ExecuteAsync(
            CancellationToken cancellationToken)
        {
            var read = await _secureSessionStore.ReadAsync(cancellationToken);
            if (!read.IsSuccess)
            {
                switch (read.Outcome)
                {
                    case ClientSecureSessionStoreOutcome.NotFound:
                    case ClientSecureSessionStoreOutcome.Unsupported:
                        return ClientSessionRestoreResult.Failed(
                            ClientSessionRestoreOutcome.NotAvailable);
                    case ClientSecureSessionStoreOutcome.Stopped:
                        return ClientSessionRestoreResult.Failed(
                            ClientSessionRestoreOutcome.Stopped);
                    case ClientSecureSessionStoreOutcome.ProfileInUse:
                        return ClientSessionRestoreResult.Failed(
                            ClientSessionRestoreOutcome.ProfileInUse);
                    case ClientSecureSessionStoreOutcome.Cancelled:
                        return ClientSessionRestoreResult.Failed(IsStopped()
                            ? ClientSessionRestoreOutcome.Stopped
                            : ClientSessionRestoreOutcome.Unresolved);
                    default:
                        return ClientSessionRestoreResult.Failed(
                            ClientSessionRestoreOutcome.StorageFailure);
                }
            }

            var bootstrap = await _bootstrapService.BootstrapAsync(cancellationToken);
            if (!bootstrap.IsSuccess)
            {
                return ClientSessionRestoreResult.Failed(
                    IsStopped() ||
                    bootstrap.Failure != null &&
                    bootstrap.Failure.Kind == ClientGatewayFailureKind.Stopped
                        ? ClientSessionRestoreOutcome.Stopped
                        : ClientSessionRestoreOutcome.Unresolved);
            }

            return await _sessionCoordinator.RestoreAsync(read.Value, cancellationToken);
        }

        /// <summary>线程安全读取stop优先级。</summary>
        /// <returns>Stop已发布时返回true。</returns>
        private bool IsStopped()
        {
            lock (_sync)
            {
                return _stopped;
            }
        }
    }
}
