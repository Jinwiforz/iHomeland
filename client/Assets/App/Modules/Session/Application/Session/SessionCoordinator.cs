using System;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Networking.Application.Contracts;
using IHomeland.Client.Networking.Application.Ports;
using IHomeland.Client.AppShell.Application.Configuration;
using IHomeland.Client.Core.Foundation.Lifetime;
using IHomeland.Client.Core.Foundation.Time;
using IHomeland.Client.PersonalWorldCombat.Application;

namespace IHomeland.Client.Session.Application
{
    /// <summary>
    /// 作为 App Scope 唯一 Session owner 协调账号认证、token lineage、logout 与短期 ticket。
    /// </summary>
    /// <remarks>
    /// 所有 mutable state 由 <see cref="_sync"/> 保护，锁内不执行外部 I/O。Register/login 使用 intent
    /// 防止旧响应覆盖后发请求；refresh 使用 single-flight 与 generation 双重 guard。Stop/forget 递增
    /// generation，使已经无法取消的迟到 completion 也不能恢复旧 credential。
    /// </remarks>
    internal sealed class SessionCoordinator :
        IAppLifetimeParticipant,
        IClientBattleAuthorizationSource
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
        private readonly IClientSessionGateway _gateway;

        /// <summary>执行无状态 register/login gateway 候选调用。</summary>
        private readonly SessionAuthenticationFlow _authenticationFlow;

        /// <summary>执行无状态 refresh gateway 候选调用。</summary>
        private readonly SessionRefreshFlow _refreshFlow;

        /// <summary>执行启动 secure lineage 轮换候选。</summary>
        private readonly SessionRestoreFlow _restoreFlow;

        /// <summary>执行 logout gateway 候选，不决定 owner 终态。</summary>
        private readonly SessionTerminationFlow _terminationFlow;

        /// <summary>集中 ticket/admission lease 创建与单次交付规则。</summary>
        private readonly SessionCredentialRegistry _credentialRegistry =
            new SessionCredentialRegistry();

        /// <summary>纯计算 generation、epoch 与迟到提交决议。</summary>
        private readonly SessionStateMachine _stateMachine =
            new SessionStateMachine();

        /// <summary>
        /// 保存 ticket expiry 判断使用的可测试 UTC 时钟。
        /// </summary>
        private readonly IClientClock _clock;

        /// <summary>
        /// 原子持久化唯一refresh lineage的安全存储port。
        /// </summary>
        private readonly IClientSecureSessionStore _secureSessionStore;

        /// <summary>
        /// 绑定当前product/environment/protocol的稳定低敏摘要。
        /// </summary>
        private readonly string _environmentBinding;

        /// <summary>
        /// 串行化secure record mutation与对应内存commit，锁内不执行HTTP。
        /// </summary>
        private readonly SemaphoreSlim _credentialMutation = new SemaphoreSlim(1, 1);

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
        private Task<ClientGatewayResult<ClientSessionSnapshot>> _refreshTask;

        /// <summary>启动restore正在独占unauthenticated入口。</summary>
        private bool _restoreInProgress;

        /// <summary>
        /// 表示 owner 已由 AppLifetime 初始化，停止后不得重启。
        /// </summary>
        private bool _initialized;

        /// <summary>
        /// 创建唯一 Session owner。
        /// </summary>
        /// <param name="configurationStore">App Scope 唯一 Configuration owner。</param>
        /// <param name="gateway">Session 固定 operation 边界。</param>
        /// <param name="clock">Credential expiry 使用的 UTC 时钟。</param>
        /// <param name="secureSessionStore">唯一refresh lineage安全存储owner。</param>
        /// <param name="environmentBinding">当前环境的稳定secure record binding。</param>
        /// <exception cref="ArgumentNullException">任一依赖为空时抛出。</exception>
        internal SessionCoordinator(
            ClientConfigurationStore configurationStore,
            IClientSessionGateway gateway,
            IClientClock clock,
            IClientSecureSessionStore secureSessionStore,
            string environmentBinding)
        {
            _configurationStore = configurationStore ?? throw new ArgumentNullException(nameof(configurationStore));
            _gateway = gateway ?? throw new ArgumentNullException(nameof(gateway));
            _authenticationFlow = new SessionAuthenticationFlow(_gateway);
            _refreshFlow = new SessionRefreshFlow(_gateway);
            _restoreFlow = new SessionRestoreFlow(_refreshFlow);
            _terminationFlow = new SessionTerminationFlow(_gateway);
            _clock = clock ?? throw new ArgumentNullException(nameof(clock));
            _secureSessionStore = secureSessionStore ??
                throw new ArgumentNullException(nameof(secureSessionStore));
            _environmentBinding = environmentBinding ??
                throw new ArgumentNullException(nameof(environmentBinding));
            if (_environmentBinding.Length != 64)
            {
                throw new ArgumentException("Environment binding长度无效。", nameof(environmentBinding));
            }
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
        /// 当前已认证 lineage 被权威清除后通知 App Scope 消费者；参数为清除后 generation。
        /// </summary>
        /// <remarks>
        /// 事件只表示 Authenticated 到非认证终态的单向边界，不用于广播 token refresh。
        /// Subscriber 必须在执行时重读 current Session，避免迟到通知覆盖后续显式登录。
        /// </remarks>
        internal event Action<long> Invalidated;

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
        internal Task<ClientGatewayResult<ClientSessionSnapshot>> RegisterAsync(
            string username,
            string password,
            string displayName,
            CancellationToken cancellationToken)
        {
            return AuthenticateAsync(
                token => _authenticationFlow.RegisterAsync(
                    username,
                    password,
                    displayName,
                    token),
                ClientOperationIDs.RegisterAccount,
                cancellationToken);
        }

        /// <summary>
        /// 登录账号并在完整 AuthResponse 验证后原子替换 session snapshot。
        /// </summary>
        /// <param name="username">符合公开 grammar 的 username。</param>
        /// <param name="password">只存活于当前调用的原始 password。</param>
        /// <param name="cancellationToken">取消等待；取消不证明服务端未提交。</param>
        /// <returns>已提交 current snapshot、服务端错误或稳定本地失败。</returns>
        internal Task<ClientGatewayResult<ClientSessionSnapshot>> LoginAsync(
            string username,
            string password,
            CancellationToken cancellationToken)
        {
            return AuthenticateAsync(
                token => _authenticationFlow.LoginAsync(
                    username,
                    password,
                    token),
                ClientOperationIDs.LoginAccount,
                cancellationToken);
        }

        /// <summary>
        /// 使用启动时读取的current refresh lineage执行唯一轮换，并在安全替换后发布snapshot。
        /// </summary>
        /// <param name="record">已经由secure store解封并通过schema验证的record。</param>
        /// <param name="cancellationToken">有界启动恢复取消信号；取消refresh视为commit unknown。</param>
        /// <returns>封闭恢复终态与仅成功时存在的current snapshot。</returns>
        internal async Task<ClientSessionRestoreResult> RestoreAsync(
            ClientSecureSessionRecord record,
            CancellationToken cancellationToken)
        {
            if (record == null)
            {
                throw new ArgumentNullException(nameof(record));
            }

            long intent;
            long invalidatedGeneration = 0;
            lock (_sync)
            {
                if (!_initialized ||
                    _state == ClientSessionOwnerState.Stopped ||
                    _state != ClientSessionOwnerState.Unauthenticated ||
                    _restoreInProgress ||
                    !_configurationStore.TryGetCurrent(out _))
                {
                    return ClientSessionRestoreResult.Failed(
                        _state == ClientSessionOwnerState.Stopped
                            ? ClientSessionRestoreOutcome.Stopped
                            : ClientSessionRestoreOutcome.Unresolved);
                }

                _restoreInProgress = true;
                intent = ++_latestAuthenticationIntent;
            }

            try
            {
                if (!string.Equals(
                        record.EnvironmentBinding,
                        _environmentBinding,
                        StringComparison.Ordinal) ||
                    record.RefreshExpiresAtMilliseconds <= _clock.UtcNowMilliseconds ||
                    record.Session.ExpiresAtMilliseconds <= _clock.UtcNowMilliseconds)
                {
                    var retired = await RetireRestoreRecordAsync(
                        intent,
                        ClientSessionOwnerState.Unauthenticated);
                    return ClientSessionRestoreResult.Failed(retired
                        ? ClientSessionRestoreOutcome.Rejected
                        : ClientSessionRestoreOutcome.StorageFailure);
                }

                var refresh = await _restoreFlow.ExecuteAsync(
                    record,
                    cancellationToken);
                if (!refresh.IsSuccess)
                {
                    if (IsUnauthenticated(refresh.ServerError))
                    {
                        var rejected = await RetireRestoreRecordAsync(
                            intent,
                            ClientSessionOwnerState.Unauthenticated);
                        return ClientSessionRestoreResult.Failed(rejected
                            ? ClientSessionRestoreOutcome.Rejected
                            : ClientSessionRestoreOutcome.StorageFailure);
                    }

                    if (IsCommitUnknown(refresh.Failure))
                    {
                        var outcome = refresh.Failure.Kind == ClientGatewayFailureKind.Stopped
                            ? ClientSessionRestoreOutcome.Stopped
                            : ClientSessionRestoreOutcome.Unresolved;
                        var retired = await RetireRestoreRecordAsync(
                            intent,
                            ClientSessionOwnerState.Unresolved);
                        return ClientSessionRestoreResult.Failed(retired
                            ? outcome
                            : ClientSessionRestoreOutcome.StorageFailure);
                    }

                    // 已收到非认证类服务端响应，或请求在发送前被本地策略拒绝时，
                    // refresh lineage 尚未被消费；本次启动不重试，但保留record供下次进程恢复。
                    return ClientSessionRestoreResult.Failed(
                        ClientSessionRestoreOutcome.Unresolved);
                }

                await _credentialMutation.WaitAsync(CancellationToken.None);
                try
                {
                    bool canPersist;
                    lock (_sync)
                    {
                        canPersist = _state == ClientSessionOwnerState.Unauthenticated &&
                                     _restoreInProgress &&
                                     intent == _latestAuthenticationIntent;
                    }

                    if (!canPersist)
                    {
                        await _secureSessionStore.DeleteAsync(CancellationToken.None);
                        return ClientSessionRestoreResult.Failed(
                            ClientSessionRestoreOutcome.Stopped);
                    }

                    var candidate = new ClientSessionSnapshot(
                        record.Account,
                        record.Session,
                        refresh.Value,
                        generation: 0);
                    var stored = await _secureSessionStore.ReplaceAsync(
                        ClientSecureSessionRecord.FromSnapshot(
                            _environmentBinding,
                            candidate),
                        CancellationToken.None);
                    if (!stored.IsSuccess)
                    {
                        lock (_sync)
                        {
                            if (_state != ClientSessionOwnerState.Stopped &&
                                intent == _latestAuthenticationIntent)
                            {
                                invalidatedGeneration = ClearLocked(
                                    ClientSessionOwnerState.Unresolved);
                            }
                        }

                        PublishInvalidated(invalidatedGeneration);

                        await _secureSessionStore.DeleteAsync(CancellationToken.None);
                        return ClientSessionRestoreResult.Failed(
                            ClientSessionRestoreOutcome.StorageFailure);
                    }

                    ClientSessionSnapshot committed;
                    lock (_sync)
                    {
                        if (_state != ClientSessionOwnerState.Unauthenticated ||
                            !_restoreInProgress ||
                            intent != _latestAuthenticationIntent)
                        {
                            committed = null;
                        }
                        else
                        {
                            _generation++;
                            _snapshot = new ClientSessionSnapshot(
                                record.Account,
                                record.Session,
                                refresh.Value,
                                _generation);
                            _state = ClientSessionOwnerState.Authenticated;
                            committed = _snapshot;
                        }
                    }

                    if (committed == null)
                    {
                        await _secureSessionStore.DeleteAsync(CancellationToken.None);
                        return ClientSessionRestoreResult.Failed(
                            ClientSessionRestoreOutcome.Stopped);
                    }

                    return ClientSessionRestoreResult.Restored(committed);
                }
                finally
                {
                    _credentialMutation.Release();
                }
            }
            finally
            {
                lock (_sync)
                {
                    if (intent == _latestAuthenticationIntent)
                    {
                        _restoreInProgress = false;
                    }
                }
            }
        }

        /// <summary>
        /// Single-flight 轮换当前 token pair，并以 source generation 拒绝迟到结果。
        /// </summary>
        /// <param name="cancellationToken">第一个调用方拥有底层请求；后续调用方可独立取消等待。</param>
        /// <returns>更新后的 current snapshot、服务端错误或稳定本地失败。</returns>
        internal Task<ClientGatewayResult<ClientSessionSnapshot>> RefreshAsync(
            CancellationToken cancellationToken)
        {
            Task<ClientGatewayResult<ClientSessionSnapshot>> sharedTask;
            TaskCompletionSource<ClientGatewayResult<ClientSessionSnapshot>> completion = null;
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
                        ClientOperationIDs.RefreshSession));
                }

                completion = new TaskCompletionSource<ClientGatewayResult<ClientSessionSnapshot>>(
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
        internal async Task<ClientGatewayResult<ClientGatewayEmpty>> LogoutAsync(
            CancellationToken cancellationToken)
        {
            var access = await CaptureFreshAuthenticatedAsync(
                ClientOperationIDs.LogoutSession,
                cancellationToken);
            if (!access.IsSuccess)
            {
                return ConvertFailure<ClientSessionSnapshot, ClientGatewayEmpty>(access);
            }

            var source = access.Value;

            var result = await _terminationFlow.LogoutAsync(
                source.Tokens.AccessToken,
                cancellationToken);
            ClientSessionOwnerState? terminalState = null;
            if (result.IsSuccess || IsUnauthenticated(result.ServerError))
            {
                terminalState = ClientSessionOwnerState.Unauthenticated;
            }
            else if (IsCommitUnknown(result.Failure))
            {
                terminalState = ClientSessionOwnerState.Unresolved;
            }

            if (terminalState.HasValue &&
                !await RetireCurrentLineageAsync(source.Generation, terminalState.Value))
            {
                return SecureStorageFailure<ClientGatewayEmpty>(
                    ClientOperationIDs.LogoutSession);
            }

            return result;
        }

        /// <summary>
        /// 为当前 session generation 签发单通道 ticket lease。
        /// </summary>
        /// <param name="channel">WSS 或 TLS_TCP channel。</param>
        /// <param name="cancellationToken">取消等待；不会自动重试签发。</param>
        /// <returns>绑定 source generation 的单次 lease、服务端错误或本地失败。</returns>
        internal async Task<ClientGatewayResult<ClientConnectionTicketLease>> IssueConnectionTicketAsync(
            ClientEndpointChannel channel,
            CancellationToken cancellationToken)
        {
            var access = await CaptureFreshAuthenticatedAsync(
                ClientOperationIDs.IssueConnectionTicket,
                cancellationToken);
            if (!access.IsSuccess)
            {
                return ConvertFailure<ClientSessionSnapshot, ClientConnectionTicketLease>(access);
            }

            var source = access.Value;

            var result = await _gateway.IssueConnectionTicketAsync(
                new ClientConnectionTicketGatewayRequest(
                    CreateAuthorizationLease(source.Tokens.AccessToken),
                    channel),
                cancellationToken);
            if (!result.IsSuccess)
            {
                await HandleAuthoritativeUnauthenticatedAsync(
                    source.Generation,
                    result.ServerError);
                return ConvertFailure<ClientConnectionTicket, ClientConnectionTicketLease>(result);
            }

            if (!TicketMatchesChannel(result.Value, channel))
            {
                return ClientGatewayResult<ClientConnectionTicketLease>.Failed(new ClientGatewayFailure(
                    ClientGatewayFailureKind.MalformedResponse,
                    ClientOperationIDs.IssueConnectionTicket));
            }

            lock (_sync)
            {
                if (!IsCurrent(source.Generation))
                {
                    return LocalPolicy<ClientConnectionTicketLease>(
                        ClientOperationIDs.IssueConnectionTicket);
                }

                return ClientGatewayResult<ClientConnectionTicketLease>.Success(
                    _credentialRegistry.CreateConnectionTicket(
                        result.Value,
                        source.Generation));
            }
        }

        /// <summary>
        /// 查询 own-world bootstrap，并在 session 轮换后丢弃旧响应。
        /// </summary>
        /// <param name="cancellationToken">取消等待；结果不保存为 PersonalWorld 最终事实。</param>
        /// <returns>当前 generation 的查询投影、服务端错误或本地失败。</returns>
        internal async Task<ClientGatewayResult<ClientWorldBootstrap>> GetWorldBootstrapAsync(
            CancellationToken cancellationToken)
        {
            var access = await CaptureFreshAuthenticatedAsync(
                ClientOperationIDs.GetWorldBootstrap,
                cancellationToken);
            if (!access.IsSuccess)
            {
                return ConvertFailure<ClientSessionSnapshot, ClientWorldBootstrap>(access);
            }

            var source = access.Value;

            var result = await _gateway.GetWorldBootstrapAsync(
                CreateAuthorizationRequest(source.Tokens.AccessToken),
                cancellationToken);
            if (!result.IsSuccess)
            {
                await HandleAuthoritativeUnauthenticatedAsync(
                    source.Generation,
                    result.ServerError);
                return result;
            }

            lock (_sync)
            {
                return IsCurrent(source.Generation)
                    ? result
                    : LocalPolicy<ClientWorldBootstrap>(
                        ClientOperationIDs.GetWorldBootstrap);
            }
        }

        /// <summary>
        /// 使用 current session generation 接受定向 invite，并拒绝迟到或已过期 reservation。
        /// </summary>
        /// <param name="request">VisitSession、invite 与 expected revision 的封闭输入。</param>
        /// <param name="idempotencyKey">同一 accept intent 稳定复用的 key。</param>
        /// <param name="cancellationToken">取消等待；不会自动重试或声称服务端未提交。</param>
        /// <returns>Current generation 的有效 reservation、服务端错误或本地失败。</returns>
        internal async Task<ClientGatewayResult<ClientVisitReservation>> AcceptVisitInviteAsync(
            ClientVisitInviteAcceptRequest request,
            string idempotencyKey,
            CancellationToken cancellationToken)
        {
            var access = await CaptureFreshAuthenticatedAsync(
                ClientOperationIDs.AcceptVisitInvite,
                cancellationToken);
            if (!access.IsSuccess)
            {
                return ConvertFailure<ClientSessionSnapshot, ClientVisitReservation>(access);
            }

            var source = access.Value;

            var result = await _gateway.AcceptVisitInviteAsync(
                new ClientAcceptVisitInviteGatewayRequest(
                    CreateAuthorizationLease(source.Tokens.AccessToken),
                    request,
                    idempotencyKey),
                cancellationToken);
            if (!result.IsSuccess)
            {
                await HandleAuthoritativeUnauthenticatedAsync(
                    source.Generation,
                    result.ServerError);
                return result;
            }

            if (result.Value.ExpiresAtMilliseconds <= _clock.UtcNowMilliseconds)
            {
                return ClientGatewayResult<ClientVisitReservation>.Failed(new ClientGatewayFailure(
                    ClientGatewayFailureKind.MalformedResponse,
                    ClientOperationIDs.AcceptVisitInvite));
            }

            lock (_sync)
            {
                return IsCurrent(source.Generation)
                    ? result
                    : LocalPolicy<ClientVisitReservation>(
                        ClientOperationIDs.AcceptVisitInvite);
            }
        }

        /// <summary>
        /// 为当前 session generation 签发单次 world admission lease。
        /// </summary>
        /// <param name="target">Own-world 或 visit-world target。</param>
        /// <param name="idempotencyKey">稳定签发意图 key；transport 不自动重试。</param>
        /// <param name="cancellationToken">取消等待的信号。</param>
        /// <returns>绑定 source generation 的单次 lease、服务端错误或本地失败。</returns>
        internal async Task<ClientGatewayResult<ClientWorldAdmissionLease>> IssueWorldAdmissionAsync(
            ClientWorldAdmissionTarget target,
            string idempotencyKey,
            CancellationToken cancellationToken)
        {
            var access = await CaptureFreshAuthenticatedAsync(
                ClientOperationIDs.IssueWorldAdmission,
                cancellationToken);
            if (!access.IsSuccess)
            {
                return ConvertFailure<ClientSessionSnapshot, ClientWorldAdmissionLease>(access);
            }

            var source = access.Value;

            var result = await _gateway.IssueWorldAdmissionAsync(
                new ClientWorldAdmissionGatewayRequest(
                    CreateAuthorizationLease(source.Tokens.AccessToken),
                    target,
                    idempotencyKey),
                cancellationToken);
            if (!result.IsSuccess)
            {
                await HandleAuthoritativeUnauthenticatedAsync(
                    source.Generation,
                    result.ServerError);
                return ConvertFailure<ClientWorldAdmission, ClientWorldAdmissionLease>(result);
            }

            lock (_sync)
            {
                if (!IsCurrent(source.Generation))
                {
                    return LocalPolicy<ClientWorldAdmissionLease>(
                        ClientOperationIDs.IssueWorldAdmission);
                }

                return ClientGatewayResult<ClientWorldAdmissionLease>.Success(
                    _credentialRegistry.CreateWorldAdmission(
                        result.Value,
                        source.Generation));
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

                return _credentialRegistry.TryTakeConnectionTicket(
                    lease,
                    _snapshot.Generation,
                    _clock.UtcNowMilliseconds,
                    out ticketUse);
            }
        }

        /// <summary>
        /// 在 Session owner 锁内验证 generation、expiry 与 binding，并从 lease 原子取得 admission 一次。
        /// </summary>
        /// <param name="lease">由当前或旧 generation 签发的 lease。</param>
        /// <param name="admissionUse">成功时返回交给 gameplay channel 的 credential 所有权。</param>
        /// <returns>当前 session、generation、expiry、binding 与单次交付均有效时返回 true。</returns>
        /// <exception cref="ArgumentNullException">Lease 为空时抛出。</exception>
        internal bool TryTakeWorldAdmission(
            ClientWorldAdmissionLease lease,
            out ClientWorldAdmissionUse admissionUse)
        {
            if (lease == null)
            {
                throw new ArgumentNullException(nameof(lease));
            }

            lock (_sync)
            {
                if (_state != ClientSessionOwnerState.Authenticated || _snapshot == null)
                {
                    admissionUse = null;
                    return false;
                }

                return _credentialRegistry.TryTakeWorldAdmission(
                    lease,
                    _snapshot.Generation,
                    _clock.UtcNowMilliseconds,
                    out admissionUse);
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
        internal async Task<bool> TryInvalidateFromControlAsync(
            long sourceGeneration,
            ulong invalidatedEpoch)
        {
            long invalidatedGeneration = 0;
            await _credentialMutation.WaitAsync(CancellationToken.None);
            try
            {
                lock (_sync)
                {
                    if (!_stateMachine.CanAcceptInvalidation(
                            _state,
                            _snapshot,
                            sourceGeneration,
                            invalidatedEpoch))
                    {
                        return false;
                    }

                    invalidatedGeneration = ClearLocked(
                        ClientSessionOwnerState.Unauthenticated);
                }

                PublishInvalidated(invalidatedGeneration);

                var deleted = await _secureSessionStore.DeleteAsync(CancellationToken.None);
                if (!IsRetired(deleted.Outcome))
                {
                    lock (_sync)
                    {
                        if (_state == ClientSessionOwnerState.Unauthenticated && _snapshot == null)
                        {
                            _state = ClientSessionOwnerState.Unresolved;
                        }
                    }
                }

                return true;
            }
            finally
            {
                _credentialMutation.Release();
            }
        }

        /// <summary>
        /// 显式放弃本地 session lineage，不声称远端 logout 已完成。
        /// </summary>
        internal async Task<ClientSecureSessionStoreOutcome> ForgetAsync(
            CancellationToken cancellationToken)
        {
            long invalidatedGeneration = 0;
            await _credentialMutation.WaitAsync(cancellationToken);
            try
            {
                lock (_sync)
                {
                    if (_state == ClientSessionOwnerState.Stopped)
                    {
                        return ClientSecureSessionStoreOutcome.Stopped;
                    }

                    _latestAuthenticationIntent++;
                    invalidatedGeneration = ClearLocked(
                        ClientSessionOwnerState.Unauthenticated);
                }

                PublishInvalidated(invalidatedGeneration);

                var deleted = await _secureSessionStore.DeleteAsync(cancellationToken);
                return deleted.Outcome;
            }
            finally
            {
                _credentialMutation.Release();
            }
        }

        /// <summary>
        /// 停止 owner、清除 credential 引用并阻止全部迟到状态提交。
        /// </summary>
        /// <param name="cancellationToken">同步清理不等待该信号。</param>
        /// <returns>Owner 已进入 Stopped 时完成的任务。</returns>
        public async Task StopAsync(CancellationToken cancellationToken)
        {
            long invalidatedGeneration;
            lock (_sync)
            {
                _latestAuthenticationIntent++;
                invalidatedGeneration = ClearLocked(ClientSessionOwnerState.Stopped);
            }

            PublishInvalidated(invalidatedGeneration);

            await _credentialMutation.WaitAsync(cancellationToken);
            _credentialMutation.Release();
        }

        /// <summary>
        /// 执行 register/login 并只允许最新 auth intent 原子提交。
        /// </summary>
        /// <param name="send">只捕获当前调用参数的强类型 HTTP delegate。</param>
        /// <param name="operationID">用于本地竞态拒绝的冻结 operationId。</param>
        /// <param name="cancellationToken">调用方取消等待的信号。</param>
        /// <returns>Current snapshot、服务端错误或本地失败。</returns>
        private async Task<ClientGatewayResult<ClientSessionSnapshot>> AuthenticateAsync(
            Func<CancellationToken, Task<ClientGatewayResult<ClientAuthentication>>> send,
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

            ClientGatewayResult<ClientSessionSnapshot> commitResult;
            var retireCandidate = false;
            await _credentialMutation.WaitAsync(CancellationToken.None);
            try
            {
                bool canPersist;
                lock (_sync)
                {
                    canPersist = _state != ClientSessionOwnerState.Stopped &&
                                 intent == _latestAuthenticationIntent;
                }

                if (!canPersist)
                {
                    retireCandidate = true;
                    commitResult = LocalPolicy<ClientSessionSnapshot>(operationID);
                }
                else
                {
                    var persistedCandidate = new ClientSessionSnapshot(
                        result.Value.Account,
                        result.Value.Session,
                        result.Value.Tokens,
                        generation: 0);
                    var stored = await _secureSessionStore.ReplaceAsync(
                        ClientSecureSessionRecord.FromSnapshot(
                            _environmentBinding,
                            persistedCandidate),
                        CancellationToken.None);
                    if (!stored.IsSuccess)
                    {
                        retireCandidate = true;
                        commitResult = SecureStorageFailure<ClientSessionSnapshot>(
                            operationID,
                            stored.Outcome);
                    }
                    else
                    {
                        ClientSessionSnapshot committed;
                        lock (_sync)
                        {
                            if (_state == ClientSessionOwnerState.Stopped ||
                                intent != _latestAuthenticationIntent)
                            {
                                committed = null;
                            }
                            else
                            {
                                _generation++;
                                _snapshot = new ClientSessionSnapshot(
                                    result.Value.Account,
                                    result.Value.Session,
                                    result.Value.Tokens,
                                    _generation);
                                _state = ClientSessionOwnerState.Authenticated;
                                committed = _snapshot;
                            }
                        }

                        if (committed == null)
                        {
                            await _secureSessionStore.DeleteAsync(CancellationToken.None);
                            retireCandidate = true;
                            commitResult = LocalPolicy<ClientSessionSnapshot>(operationID);
                        }
                        else
                        {
                            commitResult = ClientGatewayResult<ClientSessionSnapshot>.Success(committed);
                        }
                    }
                }
            }
            finally
            {
                _credentialMutation.Release();
            }

            if (retireCandidate)
            {
                await RetireUnpublishedServerSessionAsync(result.Value.Tokens.AccessToken);
            }

            return commitResult;
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
            TaskCompletionSource<ClientGatewayResult<ClientSessionSnapshot>> completion,
            Task<ClientGatewayResult<ClientSessionSnapshot>> sharedTask)
        {
            long invalidatedGeneration = 0;
            try
            {
                var result = await _refreshFlow.ExecuteAsync(
                    source.Tokens.RefreshToken,
                    cancellationToken);
                ClientGatewayResult<ClientSessionSnapshot> mapped;
                if (!result.IsSuccess)
                {
                    ClientSessionOwnerState? terminalState = null;
                    if (IsUnauthenticated(result.ServerError))
                    {
                        terminalState = ClientSessionOwnerState.Unauthenticated;
                    }
                    else if (IsCommitUnknown(result.Failure))
                    {
                        terminalState = ClientSessionOwnerState.Unresolved;
                    }

                    var retired = !terminalState.HasValue ||
                                  await RetireCurrentLineageAsync(
                                      source.Generation,
                                      terminalState.Value);
                    mapped = retired
                        ? ConvertFailure<ClientTokenPair, ClientSessionSnapshot>(result)
                        : SecureStorageFailure<ClientSessionSnapshot>(
                            ClientOperationIDs.RefreshSession);
                }
                else
                {
                    await _credentialMutation.WaitAsync(CancellationToken.None);
                    try
                    {
                        lock (_sync)
                        {
                            if (!IsCurrent(source.Generation))
                            {
                                mapped = LocalPolicy<ClientSessionSnapshot>(
                                    ClientOperationIDs.RefreshSession);
                            }
                            else
                            {
                                mapped = null;
                            }
                        }

                        if (mapped == null)
                        {
                            var candidate = new ClientSessionSnapshot(
                                source.Account,
                                source.Session,
                                result.Value,
                                generation: 0);
                            var stored = await _secureSessionStore.ReplaceAsync(
                                ClientSecureSessionRecord.FromSnapshot(
                                    _environmentBinding,
                                    candidate),
                                CancellationToken.None);
                            if (!stored.IsSuccess)
                            {
                                lock (_sync)
                                {
                                    if (IsCurrent(source.Generation))
                                    {
                                        invalidatedGeneration = ClearLocked(
                                            ClientSessionOwnerState.Unresolved);
                                    }
                                }

                                PublishInvalidated(invalidatedGeneration);

                                await _secureSessionStore.DeleteAsync(CancellationToken.None);
                                mapped = SecureStorageFailure<ClientSessionSnapshot>(
                                    ClientOperationIDs.RefreshSession);
                            }
                            else
                            {
                                ClientSessionSnapshot committed;
                                lock (_sync)
                                {
                                    if (!IsCurrent(source.Generation))
                                    {
                                        committed = null;
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
                                        committed = _snapshot;
                                    }
                                }

                                if (committed == null)
                                {
                                    await _secureSessionStore.DeleteAsync(CancellationToken.None);
                                    mapped = LocalPolicy<ClientSessionSnapshot>(
                                        ClientOperationIDs.RefreshSession);
                                }
                                else
                                {
                                    mapped = ClientGatewayResult<ClientSessionSnapshot>.Success(committed);
                                }
                            }
                        }
                    }
                    finally
                    {
                        _credentialMutation.Release();
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
        /// 为同一 battle connect attempt 取得 fresh 且只能消费一次的 authorization lease。
        /// </summary>
        /// <param name="expectedSessionGeneration">Activation 冻结的 Session generation。</param>
        /// <param name="cancellationToken">Current attempt cancellation。</param>
        /// <returns>同 generation lease、服务端拒绝或稳定本地失败。</returns>
        public async Task<ClientGatewayResult<ClientCredentialLease>>
            AcquireBattleAuthorizationAsync(
                long expectedSessionGeneration,
                CancellationToken cancellationToken)
        {
            if (expectedSessionGeneration <= 0)
            {
                return LocalPolicy<ClientCredentialLease>(
                    ClientOperationIDs.IssueBattleTicket);
            }

            if (!TryCaptureAuthenticated(out var source) ||
                source.Generation != expectedSessionGeneration)
            {
                return LocalPolicy<ClientCredentialLease>(
                    ClientOperationIDs.IssueBattleTicket);
            }

            var current = await CaptureFreshAuthenticatedAsync(
                ClientOperationIDs.IssueBattleTicket,
                cancellationToken);
            if (!current.IsSuccess)
            {
                return current.ServerError != null
                    ? ClientGatewayResult<ClientCredentialLease>.Rejected(
                        current.ServerError)
                    : ClientGatewayResult<ClientCredentialLease>.Failed(
                        current.Failure);
            }

            lock (_sync)
            {
                if (current.Value.Generation < expectedSessionGeneration ||
                    !IsCurrent(current.Value.Generation) ||
                    !string.Equals(
                        current.Value.Session.SessionID,
                        source.Session.SessionID,
                        StringComparison.Ordinal) ||
                    current.Value.Session.SessionEpoch !=
                        source.Session.SessionEpoch)
                {
                    return LocalPolicy<ClientCredentialLease>(
                        ClientOperationIDs.IssueBattleTicket);
                }

                return ClientGatewayResult<ClientCredentialLease>.Success(
                    CreateAuthorizationLease(
                        current.Value.Tokens.AccessToken));
            }
        }

        /// <summary>
        /// 为 Bearer operation 捕获 current access；绝对到期时共享一次既有 refresh flight。
        /// </summary>
        /// <param name="operationID">原始强类型 HTTP operation，用于无 Session 时的本地拒绝。</param>
        /// <param name="cancellationToken">首个 refresh owner 或当前等待方的取消信号。</param>
        /// <returns>仍有效或已成功刷新的 current Session snapshot；失败时不执行原始 operation。</returns>
        /// <remarks>
        /// 本方法只比较服务端签发的绝对 expiry，不启动 timer、tick 或延时任务。Refresh 的
        /// commit-unknown 继续由 <see cref="RefreshAsync"/> 撤销旧 lineage，调用方不得使用旧 access。
        /// </remarks>
        private async Task<ClientGatewayResult<ClientSessionSnapshot>> CaptureFreshAuthenticatedAsync(
            string operationID,
            CancellationToken cancellationToken)
        {
            if (!TryCaptureAuthenticated(out var source))
            {
                return LocalPolicy<ClientSessionSnapshot>(operationID);
            }

            if (_clock.UtcNowMilliseconds < source.Tokens.AccessExpiresAtMilliseconds)
            {
                return ClientGatewayResult<ClientSessionSnapshot>.Success(source);
            }

            return await RefreshAsync(cancellationToken);
        }

        /// <summary>
        /// 等待既有 refresh flight，同时让后续调用方只取消自己的等待而不撤销共享请求。
        /// </summary>
        /// <param name="sharedTask">第一个调用方创建并拥有的共享 refresh 任务。</param>
        /// <param name="cancellationToken">当前后续调用方的独立取消信号。</param>
        /// <returns>共享 refresh 结果，或当前等待方的 CallerCancelled 结果。</returns>
        private static async Task<ClientGatewayResult<ClientSessionSnapshot>> AwaitSharedRefreshAsync(
            Task<ClientGatewayResult<ClientSessionSnapshot>> sharedTask,
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
                    : ClientGatewayResult<ClientSessionSnapshot>.Failed(new ClientGatewayFailure(
                        ClientGatewayFailureKind.CallerCancelled,
                        ClientOperationIDs.RefreshSession));
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
                   !_restoreInProgress &&
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
            return _stateMachine.IsCurrent(
                _state,
                _snapshot,
                sourceGeneration);
        }

        /// <summary>
        /// 对 AUTH_UNAUTHENTICATED 执行 fail-closed 清理，但不让旧响应清理新 session。
        /// </summary>
        /// <param name="sourceGeneration">Operation 发起时的 session generation。</param>
        /// <param name="serverError">可选服务端错误。</param>
        private async Task HandleAuthoritativeUnauthenticatedAsync(
            long sourceGeneration,
            ClientServerError serverError)
        {
            if (!IsUnauthenticated(serverError))
            {
                return;
            }

            await RetireCurrentLineageAsync(
                sourceGeneration,
                ClientSessionOwnerState.Unauthenticated);
        }

        /// <summary>
        /// 在credential mutation owner内清除current lineage并精确退休secure record。
        /// </summary>
        /// <param name="sourceGeneration">触发终态的session generation。</param>
        /// <param name="nextState">Unauthenticated或Unresolved终态。</param>
        /// <returns>来源已经非current或record已安全退休时返回true。</returns>
        private async Task<bool> RetireCurrentLineageAsync(
            long sourceGeneration,
            ClientSessionOwnerState nextState)
        {
            long invalidatedGeneration = 0;
            await _credentialMutation.WaitAsync(CancellationToken.None);
            try
            {
                lock (_sync)
                {
                    if (!IsCurrent(sourceGeneration))
                    {
                        return true;
                    }

                    invalidatedGeneration = ClearLocked(nextState);
                }

                PublishInvalidated(invalidatedGeneration);

                var deleted = await _secureSessionStore.DeleteAsync(CancellationToken.None);
                var retired = IsRetired(deleted.Outcome);
                if (!retired)
                {
                    lock (_sync)
                    {
                        if (_snapshot == null && _state == nextState)
                        {
                            _state = ClientSessionOwnerState.Unresolved;
                        }
                    }
                }

                return retired;
            }
            finally
            {
                _credentialMutation.Release();
            }
        }

        /// <summary>退休尚未发布为current的启动record并设置对应Session终态。</summary>
        /// <param name="restoreIntent">唯一启动restore intent。</param>
        /// <param name="nextState">Rejected或commit-unknown对应终态。</param>
        /// <returns>Record已删除或原本不存在时返回true。</returns>
        private async Task<bool> RetireRestoreRecordAsync(
            long restoreIntent,
            ClientSessionOwnerState nextState)
        {
            long invalidatedGeneration = 0;
            await _credentialMutation.WaitAsync(CancellationToken.None);
            try
            {
                lock (_sync)
                {
                    if (_state != ClientSessionOwnerState.Stopped &&
                        restoreIntent == _latestAuthenticationIntent)
                    {
                        invalidatedGeneration = ClearLocked(nextState);
                    }
                }

                PublishInvalidated(invalidatedGeneration);

                var deleted = await _secureSessionStore.DeleteAsync(CancellationToken.None);
                return IsRetired(deleted.Outcome);
            }
            finally
            {
                _credentialMutation.Release();
            }
        }

        /// <summary>尽力使未发布的服务端session失效，且不改变任何current本地事实。</summary>
        /// <param name="accessToken">仅当前候选持有的opaque access token。</param>
        /// <returns>服务端调用得出封闭结果时完成。</returns>
        private async Task RetireUnpublishedServerSessionAsync(string accessToken)
        {
            await _gateway.LogoutAsync(
                CreateAuthorizationRequest(accessToken),
                CancellationToken.None);
        }

        /// <summary>为单个带认证 gateway operation 创建一次性 authorization 请求。</summary>
        /// <param name="accessToken">Session owner 当前 opaque access token。</param>
        /// <returns>只允许 HTTP adapter 单次取得 credential 的请求。</returns>
        private static ClientCredentialGatewayRequest CreateAuthorizationRequest(
            string accessToken)
        {
            return new ClientCredentialGatewayRequest(
                CreateAuthorizationLease(accessToken));
        }

        /// <summary>为复合 gateway request 创建一次性 authorization lease。</summary>
        /// <param name="accessToken">Session owner 当前 opaque access token。</param>
        /// <returns>绑定 HTTP authorization purpose 的 lease。</returns>
        private static ClientCredentialLease CreateAuthorizationLease(string accessToken)
        {
            return new ClientCredentialLease(
                accessToken,
                ClientCredentialPurpose.HttpAuthorization);
        }

        /// <summary>判断store delete是否已经保证旧lineage不可恢复。</summary>
        /// <param name="outcome">封闭store outcome。</param>
        /// <returns>成功删除或原本不存在时返回true。</returns>
        private static bool IsRetired(ClientSecureSessionStoreOutcome outcome)
        {
            return outcome == ClientSecureSessionStoreOutcome.Succeeded ||
                   outcome == ClientSecureSessionStoreOutcome.NotFound;
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
        private static bool IsCommitUnknown(ClientGatewayFailure failure)
        {
            return failure != null &&
                   failure.Kind != ClientGatewayFailureKind.LocalPolicy;
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
        private long ClearLocked(ClientSessionOwnerState nextState)
        {
            var invalidated = _stateMachine.ShouldPublishInvalidation(
                _state,
                _snapshot);
            _generation = _stateMachine.NextGeneration(_generation);
            _snapshot = null;
            _state = nextState;
            return invalidated ? _generation : 0;
        }

        /// <summary>在 owner 锁外隔离发布一次已提交的 Session 失效边界。</summary>
        /// <param name="generation">清除后的 generation；零表示本次没有清除认证 lineage。</param>
        private void PublishInvalidated(long generation)
        {
            if (generation <= 0)
            {
                return;
            }

            var subscribers = Invalidated;
            if (subscribers == null)
            {
                return;
            }

            foreach (Action<long> subscriber in subscribers.GetInvocationList())
            {
                try
                {
                    subscriber(generation);
                }
                catch (Exception)
                {
                    // Session 已提交为失效终态；单个观察者故障不得回滚 credential owner。
                }
            }
        }

        /// <summary>
        /// 创建当前 operation 的本地前置条件失败。
        /// </summary>
        /// <typeparam name="T">原 operation 成功投影类型。</typeparam>
        /// <param name="operationID">冻结 operationId。</param>
        /// <returns>LocalPolicy 失败。</returns>
        private static ClientGatewayResult<T> LocalPolicy<T>(string operationID)
        {
            return ClientGatewayResult<T>.Failed(new ClientGatewayFailure(
                ClientGatewayFailureKind.LocalPolicy,
                operationID));
        }

        /// <summary>创建不携带路径、exception或credential的安全存储失败。</summary>
        /// <typeparam name="T">原operation成功投影类型。</typeparam>
        /// <param name="operationID">触发原子commit的冻结operationId。</param>
        /// <param name="storeOutcome">触发失败的封闭store结果；只单独保留profile ownership。</param>
        /// <returns>SecureStorage或profile-in-use本地失败。</returns>
        private static ClientGatewayResult<T> SecureStorageFailure<T>(
            string operationID,
            ClientSecureSessionStoreOutcome storeOutcome =
                ClientSecureSessionStoreOutcome.StorageFailure)
        {
            return ClientGatewayResult<T>.Failed(new ClientGatewayFailure(
                storeOutcome == ClientSecureSessionStoreOutcome.ProfileInUse
                    ? ClientGatewayFailureKind.SecureStorageProfileInUse
                    : ClientGatewayFailureKind.SecureStorage,
                operationID));
        }

        /// <summary>
        /// 保留服务端错误或本地失败，同时改变成功泛型类型。
        /// </summary>
        /// <typeparam name="TSource">上游成功类型。</typeparam>
        /// <typeparam name="TTarget">当前 application 结果类型。</typeparam>
        /// <param name="source">已确认非成功的结果。</param>
        /// <returns>不丢失安全错误语义的新结果。</returns>
        private static ClientGatewayResult<TTarget> ConvertFailure<TSource, TTarget>(
            ClientGatewayResult<TSource> source)
        {
            return source.ServerError != null
                ? ClientGatewayResult<TTarget>.Rejected(source.ServerError)
                : ClientGatewayResult<TTarget>.Failed(source.Failure);
        }
    }
}
