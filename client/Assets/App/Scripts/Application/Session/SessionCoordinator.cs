using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Core.Configuration;
using IHomeland.Client.Core.Lifetime;
using IHomeland.Client.Infrastructure.Http;

namespace IHomeland.Client.Application.Session
{
    /// <summary>
    /// 作为 App Scope 唯一 Session owner 协调账号认证、token lineage、logout 与短期 ticket。
    /// </summary>
    /// <remarks>
    /// 所有 mutable state 由 <see cref="_sync"/> 保护，锁内不执行外部 I/O。Register/login 使用 intent
    /// 防止旧响应覆盖后发请求；refresh 使用 single-flight 与 generation 双重 guard。Stop/forget 递增
    /// generation，使已经无法取消的迟到 completion 也不能恢复旧 credential。
    /// </remarks>
    internal sealed class SessionCoordinator : IAppLifetimeParticipant
    {
        /// <summary>
        /// 保护 owner state、snapshot、generation、auth intent 与 refresh task。
        /// </summary>
        private readonly object _sync = new object();

        /// <summary>
        /// 保存认证前配置门；没有兼容 config 时拒绝账号与 session operation。
        /// </summary>
        private readonly ClientConfigurationStore _configurationStore;

        /// <summary>
        /// 保存不暴露任意 path 的强类型 HTTP API。
        /// </summary>
        private readonly IClientHttpApi _httpApi;

        /// <summary>
        /// 保存 ticket expiry 判断使用的可测试 UTC 时钟。
        /// </summary>
        private readonly IClientClock _clock;

        /// <summary>
        /// 保存当前 lifecycle/session 状态，只能在锁内访问。
        /// </summary>
        private ClientSessionOwnerState _state = ClientSessionOwnerState.Unauthenticated;

        /// <summary>
        /// 保存 current immutable session snapshot；非 Authenticated 时为空。
        /// </summary>
        private ClientSessionSnapshot _snapshot;

        /// <summary>
        /// 每次 session 替换、清理或 unresolved 迁移时递增，阻断迟到结果。
        /// </summary>
        private long _generation;

        /// <summary>
        /// 每次 register/login 开始时递增，保证后发 intent 优先于旧 completion。
        /// </summary>
        private long _latestAuthenticationIntent;

        /// <summary>
        /// 保存当前共享 refresh 结果；没有进行中 refresh 时为空。
        /// </summary>
        private Task<ClientHttpResult<ClientSessionSnapshot>> _refreshTask;

        /// <summary>
        /// 表示 owner 已由 AppLifetime 初始化，停止后不得重启。
        /// </summary>
        private bool _initialized;

        /// <summary>
        /// 创建唯一 Session owner。
        /// </summary>
        /// <param name="configurationStore">App Scope 唯一 Configuration owner。</param>
        /// <param name="httpApi">强类型 HTTP operation 边界。</param>
        /// <param name="clock">Credential expiry 使用的 UTC 时钟。</param>
        /// <exception cref="ArgumentNullException">任一依赖为空时抛出。</exception>
        internal SessionCoordinator(
            ClientConfigurationStore configurationStore,
            IClientHttpApi httpApi,
            IClientClock clock)
        {
            _configurationStore = configurationStore ?? throw new ArgumentNullException(nameof(configurationStore));
            _httpApi = httpApi ?? throw new ArgumentNullException(nameof(httpApi));
            _clock = clock ?? throw new ArgumentNullException(nameof(clock));
        }

        /// <summary>
        /// 获取当前 session owner 状态快照。
        /// </summary>
        internal ClientSessionOwnerState State
        {
            get
            {
                lock (_sync)
                {
                    return _state;
                }
            }
        }

        /// <summary>
        /// 在不访问网络的情况下启用 session operation。
        /// </summary>
        /// <param name="cancellationToken">初始化前检查的 AppLifetime 取消信号。</param>
        /// <returns>Owner 已允许显式认证调用时完成的任务。</returns>
        /// <exception cref="InvalidOperationException">重复初始化或停止后重启时抛出。</exception>
        public Task InitializeAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            lock (_sync)
            {
                if (_initialized || _state == ClientSessionOwnerState.Stopped)
                {
                    throw new InvalidOperationException("SessionCoordinator 不能重复初始化或停止后重启。");
                }

                _initialized = true;
                _state = ClientSessionOwnerState.Unauthenticated;
            }

            return Task.CompletedTask;
        }

        /// <summary>
        /// 创建账号并在完整 AuthResponse 验证后原子替换 session snapshot。
        /// </summary>
        /// <param name="username">符合公开 grammar 的 username。</param>
        /// <param name="password">只存活于当前调用的原始 password。</param>
        /// <param name="displayName">待服务端规范化的显示名。</param>
        /// <param name="cancellationToken">取消等待；取消不证明服务端未提交。</param>
        /// <returns>已提交 current snapshot、服务端错误或稳定本地失败。</returns>
        internal Task<ClientHttpResult<ClientSessionSnapshot>> RegisterAsync(
            string username,
            string password,
            string displayName,
            CancellationToken cancellationToken)
        {
            return AuthenticateAsync(
                token => _httpApi.RegisterAsync(username, password, displayName, token),
                ClientHttpOperationCatalog.RegisterAccount.OperationID,
                cancellationToken);
        }

        /// <summary>
        /// 登录账号并在完整 AuthResponse 验证后原子替换 session snapshot。
        /// </summary>
        /// <param name="username">符合公开 grammar 的 username。</param>
        /// <param name="password">只存活于当前调用的原始 password。</param>
        /// <param name="cancellationToken">取消等待；取消不证明服务端未提交。</param>
        /// <returns>已提交 current snapshot、服务端错误或稳定本地失败。</returns>
        internal Task<ClientHttpResult<ClientSessionSnapshot>> LoginAsync(
            string username,
            string password,
            CancellationToken cancellationToken)
        {
            return AuthenticateAsync(
                token => _httpApi.LoginAsync(username, password, token),
                ClientHttpOperationCatalog.LoginAccount.OperationID,
                cancellationToken);
        }

        /// <summary>
        /// Single-flight 轮换当前 token pair，并以 source generation 拒绝迟到结果。
        /// </summary>
        /// <param name="cancellationToken">第一个调用方拥有底层请求；后续调用方可独立取消等待。</param>
        /// <returns>更新后的 current snapshot、服务端错误或稳定本地失败。</returns>
        internal Task<ClientHttpResult<ClientSessionSnapshot>> RefreshAsync(
            CancellationToken cancellationToken)
        {
            Task<ClientHttpResult<ClientSessionSnapshot>> sharedTask;
            TaskCompletionSource<ClientHttpResult<ClientSessionSnapshot>> completion = null;
            ClientSessionSnapshot source = null;
            lock (_sync)
            {
                if (_refreshTask != null)
                {
                    return cancellationToken.CanBeCanceled
                        ? AwaitSharedRefreshAsync(_refreshTask, cancellationToken)
                        : _refreshTask;
                }

                if (!CanUseAuthenticatedSnapshot(out source))
                {
                    return Task.FromResult(LocalPolicy<ClientSessionSnapshot>(
                        ClientHttpOperationCatalog.RefreshSession.OperationID));
                }

                completion = new TaskCompletionSource<ClientHttpResult<ClientSessionSnapshot>>(
                    TaskCreationOptions.RunContinuationsAsynchronously);
                _refreshTask = completion.Task;
                sharedTask = _refreshTask;
            }

            _ = CompleteRefreshAsync(source, cancellationToken, completion, sharedTask);
            return sharedTask;
        }

        /// <summary>
        /// 使当前 session epoch 失效，并对 commit-unknown 进入 fail-closed unresolved。
        /// </summary>
        /// <param name="cancellationToken">取消等待；timeout/cancel/transport 均可能表示远端已提交。</param>
        /// <returns>204 成功、服务端错误或稳定本地失败。</returns>
        internal async Task<ClientHttpResult<ClientHttpEmpty>> LogoutAsync(
            CancellationToken cancellationToken)
        {
            if (!TryCaptureAuthenticated(out var source))
            {
                return LocalPolicy<ClientHttpEmpty>(ClientHttpOperationCatalog.LogoutSession.OperationID);
            }

            var result = await _httpApi.LogoutAsync(source.Tokens.AccessToken, cancellationToken);
            lock (_sync)
            {
                if (!IsCurrent(source.Generation))
                {
                    return result;
                }

                if (result.IsSuccess || IsUnauthenticated(result.ServerError))
                {
                    ClearLocked(ClientSessionOwnerState.Unauthenticated);
                }
                else if (IsCommitUnknown(result.Failure))
                {
                    ClearLocked(ClientSessionOwnerState.Unresolved);
                }
            }

            return result;
        }

        /// <summary>
        /// 为当前 session generation 签发单通道 ticket lease。
        /// </summary>
        /// <param name="channel">WSS 或 TLS_TCP channel。</param>
        /// <param name="cancellationToken">取消等待；不会自动重试签发。</param>
        /// <returns>绑定 source generation 的单次 lease、服务端错误或本地失败。</returns>
        internal async Task<ClientHttpResult<ClientConnectionTicketLease>> IssueConnectionTicketAsync(
            ClientEndpointChannel channel,
            CancellationToken cancellationToken)
        {
            if (!TryCaptureAuthenticated(out var source))
            {
                return LocalPolicy<ClientConnectionTicketLease>(
                    ClientHttpOperationCatalog.IssueConnectionTicket.OperationID);
            }

            var result = await _httpApi.IssueConnectionTicketAsync(
                source.Tokens.AccessToken,
                channel,
                cancellationToken);
            if (!result.IsSuccess)
            {
                HandleAuthoritativeUnauthenticated(source.Generation, result.ServerError);
                return ConvertFailure<ClientConnectionTicket, ClientConnectionTicketLease>(result);
            }

            if (!TicketMatchesChannel(result.Value, channel))
            {
                return ClientHttpResult<ClientConnectionTicketLease>.Failed(new ClientHttpFailure(
                    ClientHttpFailureKind.MalformedResponse,
                    ClientHttpOperationCatalog.IssueConnectionTicket.OperationID));
            }

            lock (_sync)
            {
                if (!IsCurrent(source.Generation))
                {
                    return LocalPolicy<ClientConnectionTicketLease>(
                        ClientHttpOperationCatalog.IssueConnectionTicket.OperationID);
                }

                return ClientHttpResult<ClientConnectionTicketLease>.Success(
                    new ClientConnectionTicketLease(result.Value, source.Generation));
            }
        }

        /// <summary>
        /// 查询 own-world bootstrap，并在 session 轮换后丢弃旧响应。
        /// </summary>
        /// <param name="cancellationToken">取消等待；结果不保存为 PersonalWorld 最终事实。</param>
        /// <returns>当前 generation 的查询投影、服务端错误或本地失败。</returns>
        internal async Task<ClientHttpResult<ClientWorldBootstrap>> GetWorldBootstrapAsync(
            CancellationToken cancellationToken)
        {
            if (!TryCaptureAuthenticated(out var source))
            {
                return LocalPolicy<ClientWorldBootstrap>(
                    ClientHttpOperationCatalog.GetWorldBootstrap.OperationID);
            }

            var result = await _httpApi.GetWorldBootstrapAsync(
                source.Tokens.AccessToken,
                cancellationToken);
            if (!result.IsSuccess)
            {
                HandleAuthoritativeUnauthenticated(source.Generation, result.ServerError);
                return result;
            }

            lock (_sync)
            {
                return IsCurrent(source.Generation)
                    ? result
                    : LocalPolicy<ClientWorldBootstrap>(
                        ClientHttpOperationCatalog.GetWorldBootstrap.OperationID);
            }
        }

        /// <summary>
        /// 在 Session owner 锁内验证 generation/expiry，并从 lease 原子取得 ticket 一次。
        /// </summary>
        /// <param name="lease">由当前或旧 generation 签发的 lease。</param>
        /// <param name="ticketUse">成功时返回交给匹配 channel 的 credential 所有权。</param>
        /// <returns>当前 session、generation、expiry 与单次交付均有效时返回 true。</returns>
        /// <exception cref="ArgumentNullException">Lease 为空时抛出。</exception>
        internal bool TryTakeConnectionTicket(
            ClientConnectionTicketLease lease,
            out ClientConnectionTicketUse ticketUse)
        {
            if (lease == null)
            {
                throw new ArgumentNullException(nameof(lease));
            }

            lock (_sync)
            {
                if (_state != ClientSessionOwnerState.Authenticated || _snapshot == null)
                {
                    ticketUse = null;
                    return false;
                }

                return lease.TryTake(
                    _snapshot.Generation,
                    _clock.UtcNowMilliseconds,
                    out ticketUse);
            }
        }

        /// <summary>
        /// 尝试取得当前不可变 session snapshot，供只读状态展示与测试观察。
        /// </summary>
        /// <param name="snapshot">Authenticated 时返回 current snapshot。</param>
        /// <returns>当前持有可用 session 时返回 true。</returns>
        internal bool TryGetCurrent(out ClientSessionSnapshot snapshot)
        {
            lock (_sync)
            {
                snapshot = _snapshot;
                return _state == ClientSessionOwnerState.Authenticated && snapshot != null;
            }
        }

        /// <summary>
        /// 接受 current control connection 携带的更高权威 epoch，并原子清除对应旧 session lineage。
        /// </summary>
        /// <param name="sourceGeneration">WSS ticket 单次交付时捕获的本地 session generation。</param>
        /// <param name="invalidatedEpoch">服务端 forced logout/session invalidation 公布的新 epoch。</param>
        /// <returns>来源仍 current 且新 epoch 更高、因而已清除 snapshot 时返回 true。</returns>
        internal bool TryInvalidateFromControl(long sourceGeneration, ulong invalidatedEpoch)
        {
            lock (_sync)
            {
                if (_state != ClientSessionOwnerState.Authenticated ||
                    _snapshot == null ||
                    _snapshot.Generation != sourceGeneration ||
                    invalidatedEpoch <= (ulong)_snapshot.Session.SessionEpoch)
                {
                    return false;
                }

                ClearLocked(ClientSessionOwnerState.Unauthenticated);
                return true;
            }
        }

        /// <summary>
        /// 显式放弃本地 session lineage，不声称远端 logout 已完成。
        /// </summary>
        internal void Forget()
        {
            lock (_sync)
            {
                if (_state != ClientSessionOwnerState.Stopped)
                {
                    ClearLocked(ClientSessionOwnerState.Unauthenticated);
                }
            }
        }

        /// <summary>
        /// 停止 owner、清除 credential 引用并阻止全部迟到状态提交。
        /// </summary>
        /// <param name="cancellationToken">同步清理不等待该信号。</param>
        /// <returns>Owner 已进入 Stopped 时完成的任务。</returns>
        public Task StopAsync(CancellationToken cancellationToken)
        {
            lock (_sync)
            {
                _latestAuthenticationIntent++;
                ClearLocked(ClientSessionOwnerState.Stopped);
            }

            return Task.CompletedTask;
        }

        /// <summary>
        /// 执行 register/login 并只允许最新 auth intent 原子提交。
        /// </summary>
        /// <param name="send">只捕获当前调用参数的强类型 HTTP delegate。</param>
        /// <param name="operationID">用于本地竞态拒绝的冻结 operationId。</param>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>Current snapshot、服务端错误或本地失败。</returns>
        private async Task<ClientHttpResult<ClientSessionSnapshot>> AuthenticateAsync(
            Func<CancellationToken, Task<ClientHttpResult<ClientAuthentication>>> send,
            string operationID,
            CancellationToken cancellationToken)
        {
            long intent;
            lock (_sync)
            {
                if (!CanStartAuthentication())
                {
                    return LocalPolicy<ClientSessionSnapshot>(
                        operationID);
                }

                intent = ++_latestAuthenticationIntent;
            }

            var result = await send(cancellationToken);
            if (!result.IsSuccess)
            {
                return ConvertFailure<ClientAuthentication, ClientSessionSnapshot>(result);
            }

            lock (_sync)
            {
                if (_state == ClientSessionOwnerState.Stopped || intent != _latestAuthenticationIntent)
                {
                    return LocalPolicy<ClientSessionSnapshot>(
                        operationID);
                }

                _generation++;
                _snapshot = new ClientSessionSnapshot(
                    result.Value.Account,
                    result.Value.Session,
                    result.Value.Tokens,
                    _generation);
                _state = ClientSessionOwnerState.Authenticated;
                return ClientHttpResult<ClientSessionSnapshot>.Success(_snapshot);
            }
        }

        /// <summary>
        /// 完成共享 refresh，并在任何结果后对称清除 single-flight 引用。
        /// </summary>
        /// <param name="source">Refresh 开始时捕获的 immutable snapshot。</param>
        /// <param name="cancellationToken">第一个调用方拥有的取消信号。</param>
        /// <param name="completion">全部并发调用共享的 completion owner。</param>
        /// <param name="sharedTask">用于防止旧 finally 清除后发 refresh 的任务身份。</param>
        /// <returns>内部 completion task；错误通过共享结果或 exception 观察。</returns>
        private async Task CompleteRefreshAsync(
            ClientSessionSnapshot source,
            CancellationToken cancellationToken,
            TaskCompletionSource<ClientHttpResult<ClientSessionSnapshot>> completion,
            Task<ClientHttpResult<ClientSessionSnapshot>> sharedTask)
        {
            try
            {
                var result = await _httpApi.RefreshAsync(
                    source.Tokens.RefreshToken,
                    cancellationToken);
                ClientHttpResult<ClientSessionSnapshot> mapped;
                if (!result.IsSuccess)
                {
                    HandleAuthoritativeUnauthenticated(source.Generation, result.ServerError);
                    HandleCommitUnknown(source.Generation, result.Failure);
                    mapped = ConvertFailure<ClientTokenPair, ClientSessionSnapshot>(result);
                }
                else
                {
                    lock (_sync)
                    {
                        if (!IsCurrent(source.Generation))
                        {
                            mapped = LocalPolicy<ClientSessionSnapshot>(
                                ClientHttpOperationCatalog.RefreshSession.OperationID);
                        }
                        else
                        {
                            _generation++;
                            _snapshot = new ClientSessionSnapshot(
                                source.Account,
                                source.Session,
                                result.Value,
                                _generation);
                            _state = ClientSessionOwnerState.Authenticated;
                            mapped = ClientHttpResult<ClientSessionSnapshot>.Success(_snapshot);
                        }
                    }
                }

                completion.TrySetResult(mapped);
            }
            catch (Exception error)
            {
                completion.TrySetException(error);
            }
            finally
            {
                lock (_sync)
                {
                    if (ReferenceEquals(_refreshTask, sharedTask))
                    {
                        _refreshTask = null;
                    }
                }
            }
        }

        /// <summary>
        /// 等待既有 refresh flight，同时让后续调用方只取消自己的等待而不撤销共享请求。
        /// </summary>
        /// <param name="sharedTask">第一个调用方创建并拥有的共享 refresh 任务。</param>
        /// <param name="cancellationToken">当前后续调用方的独立取消信号。</param>
        /// <returns>共享 refresh 结果，或当前等待方的 CallerCancelled 结果。</returns>
        private static async Task<ClientHttpResult<ClientSessionSnapshot>> AwaitSharedRefreshAsync(
            Task<ClientHttpResult<ClientSessionSnapshot>> sharedTask,
            CancellationToken cancellationToken)
        {
            if (sharedTask.IsCompleted)
            {
                return await sharedTask;
            }

            var cancellationSignal = new TaskCompletionSource<bool>(
                TaskCreationOptions.RunContinuationsAsynchronously);
            using (cancellationToken.Register(() => cancellationSignal.TrySetResult(true)))
            {
                var completed = await Task.WhenAny(sharedTask, cancellationSignal.Task);
                return ReferenceEquals(completed, sharedTask)
                    ? await sharedTask
                    : ClientHttpResult<ClientSessionSnapshot>.Failed(new ClientHttpFailure(
                        ClientHttpFailureKind.CallerCancelled,
                        ClientHttpOperationCatalog.RefreshSession.OperationID));
            }
        }

        /// <summary>
        /// 检查 Configuration 与 lifecycle 是否允许发起新的 auth intent。
        /// </summary>
        /// <returns>Owner 已初始化、未停止且配置 Ready 时返回 true。</returns>
        private bool CanStartAuthentication()
        {
            return _initialized &&
                   _state != ClientSessionOwnerState.Stopped &&
                   _configurationStore.TryGetCurrent(out _);
        }

        /// <summary>
        /// 在锁内取得当前 authenticated snapshot。
        /// </summary>
        /// <param name="snapshot">成功时返回 current immutable snapshot。</param>
        /// <returns>生命周期、配置和 session 均允许认证调用时返回 true。</returns>
        private bool CanUseAuthenticatedSnapshot(out ClientSessionSnapshot snapshot)
        {
            snapshot = _snapshot;
            return _initialized &&
                   _state == ClientSessionOwnerState.Authenticated &&
                   snapshot != null &&
                   _configurationStore.TryGetCurrent(out _);
        }

        /// <summary>
        /// 在线程安全边界捕获 current authenticated snapshot。
        /// </summary>
        /// <param name="snapshot">成功时返回 current immutable snapshot。</param>
        /// <returns>当前允许 authenticated operation 时返回 true。</returns>
        private bool TryCaptureAuthenticated(out ClientSessionSnapshot snapshot)
        {
            lock (_sync)
            {
                return CanUseAuthenticatedSnapshot(out snapshot);
            }
        }

        /// <summary>
        /// 判断 source generation 是否仍是 current authenticated lineage。
        /// </summary>
        /// <param name="sourceGeneration">Operation 开始时捕获的 generation。</param>
        /// <returns>Owner 仍为同一 authenticated snapshot 时返回 true。</returns>
        private bool IsCurrent(long sourceGeneration)
        {
            return _state == ClientSessionOwnerState.Authenticated &&
                   _snapshot != null &&
                   _snapshot.Generation == sourceGeneration;
        }

        /// <summary>
        /// 对 AUTH_UNAUTHENTICATED 执行 fail-closed 清理，但不让旧响应清理新 session。
        /// </summary>
        /// <param name="sourceGeneration">Operation 发起时的 session generation。</param>
        /// <param name="serverError">可选服务端错误。</param>
        private void HandleAuthoritativeUnauthenticated(
            long sourceGeneration,
            ClientServerError serverError)
        {
            if (!IsUnauthenticated(serverError))
            {
                return;
            }

            lock (_sync)
            {
                if (IsCurrent(sourceGeneration))
                {
                    ClearLocked(ClientSessionOwnerState.Unauthenticated);
                }
            }
        }

        /// <summary>
        /// 对可能已经在远端提交的 refresh/logout 本地失败撤销旧 credential lineage。
        /// </summary>
        /// <param name="sourceGeneration">Operation 发起时的 session generation。</param>
        /// <param name="failure">可选本地失败。</param>
        private void HandleCommitUnknown(long sourceGeneration, ClientHttpFailure failure)
        {
            if (!IsCommitUnknown(failure))
            {
                return;
            }

            lock (_sync)
            {
                if (IsCurrent(sourceGeneration))
                {
                    ClearLocked(ClientSessionOwnerState.Unresolved);
                }
            }
        }

        /// <summary>
        /// 判断服务端错误是否权威声明当前 HTTPS session 未认证。
        /// </summary>
        /// <param name="serverError">可选服务端错误。</param>
        /// <returns>Code 为 AUTH_UNAUTHENTICATED(100) 时返回 true。</returns>
        private static bool IsUnauthenticated(ClientServerError serverError)
        {
            return serverError != null && serverError.Code == 100;
        }

        /// <summary>
        /// 判断 refresh/logout 本地失败是否无法确定远端提交状态。
        /// </summary>
        /// <param name="failure">可选本地失败。</param>
        /// <returns>除发送前 LocalPolicy 外，无法证明远端未提交的本地失败返回 true。</returns>
        private static bool IsCommitUnknown(ClientHttpFailure failure)
        {
            return failure != null &&
                   failure.Kind != ClientHttpFailureKind.LocalPolicy;
        }

        /// <summary>
        /// 验证 ticket endpoint 与固定 scope 精确匹配请求 channel。
        /// </summary>
        /// <param name="ticket">已通过 schema codec 的 ticket。</param>
        /// <param name="channel">本次请求选择的 channel。</param>
        /// <returns>Endpoint channel 与唯一 scope 都匹配时返回 true。</returns>
        private static bool TicketMatchesChannel(
            ClientConnectionTicket ticket,
            ClientEndpointChannel channel)
        {
            if (ticket.Endpoint.Channel != channel || ticket.Scopes.Count != 1)
            {
                return false;
            }

            return channel == ClientEndpointChannel.Wss
                ? ticket.Scopes[0] == ClientConnectionScope.Control
                : ticket.Scopes[0] == ClientConnectionScope.Gameplay;
        }

        /// <summary>
        /// 清除 credential 引用、递增 generation 并迁移到指定非 Authenticated 状态。
        /// </summary>
        /// <param name="nextState">Unauthenticated、Unresolved 或 Stopped。</param>
        private void ClearLocked(ClientSessionOwnerState nextState)
        {
            _generation++;
            _snapshot = null;
            _state = nextState;
        }

        /// <summary>
        /// 创建当前 operation 的本地前置条件失败。
        /// </summary>
        /// <typeparam name="T">原 operation 成功投影类型。</typeparam>
        /// <param name="operationID">冻结 operationId。</param>
        /// <returns>LocalPolicy 失败。</returns>
        private static ClientHttpResult<T> LocalPolicy<T>(string operationID)
        {
            return ClientHttpResult<T>.Failed(new ClientHttpFailure(
                ClientHttpFailureKind.LocalPolicy,
                operationID));
        }

        /// <summary>
        /// 保留服务端错误或本地失败，同时改变成功泛型类型。
        /// </summary>
        /// <typeparam name="TSource">上游成功类型。</typeparam>
        /// <typeparam name="TTarget">当前 application 结果类型。</typeparam>
        /// <param name="source">已确认非成功的结果。</param>
        /// <returns>不丢失安全错误语义的新结果。</returns>
        private static ClientHttpResult<TTarget> ConvertFailure<TSource, TTarget>(
            ClientHttpResult<TSource> source)
        {
            return source.ServerError != null
                ? ClientHttpResult<TTarget>.Rejected(source.ServerError)
                : ClientHttpResult<TTarget>.Failed(source.Failure);
        }
    }
}
