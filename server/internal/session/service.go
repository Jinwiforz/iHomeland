package session

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"
)

// TokenPair 是唯一允许 raw access/refresh secret 离开 session core 的签发结果。
type TokenPair struct {
	// Access 只应返回给认证响应边界，不得记录或持久化。
	Access Secret
	// AccessExpiresAt 是绝对时间，告知客户端何时停止使用 access token。
	AccessExpiresAt time.Time
	// Refresh 只应返回给受保护的 refresh 持有方。
	Refresh Secret
	// RefreshExpiresAt 是绝对时间，告知客户端 refresh lineage 的轮换期限。
	RefreshExpiresAt time.Time
}

// SessionResult 汇总新 session 的公开 identifier、expiry 与首对 token。
type SessionResult struct {
	// SessionID 用于安全关联，不具备 bearer 权限。
	SessionID SessionID
	// Epoch 固定为 InitialEpoch，客户端不能选择。
	Epoch Epoch
	// ExpiresAt 是整个 session lineage 的绝对上限，refresh 不得延长它。
	ExpiresAt time.Time
	// Tokens 仅在创建成功后包含 raw credentials。
	Tokens TokenPair
}

// ConnectionTicket 是协议 adapter 编码前的完整短期连接资格。
//
// 它与跨端 ConnectionTicket 字段一一对应，但保持纯 Go 且不依赖 generated type。
// 客户端可看到绑定元数据，真正授权仍依赖 SessionStore 中 nonce digest 对应的 record。
type ConnectionTicket struct {
	// SessionID 标识签发资格所属的 session，不能由客户端覆盖。
	SessionID SessionID
	// Epoch 是签发时读取的 session 撤销屏障。
	Epoch Epoch
	// Channel 是该 ticket 唯一允许连接的 realtime listener 类型。
	Channel Channel
	// Endpoint 来自受信 provider，而不是客户端请求。
	Endpoint Endpoint
	// Scopes 是 channel policy 固定授予的只读 capability 集合。
	Scopes ScopeSet
	// Nonce 是协议要求的 16-byte 一次性认证材料。
	Nonce TicketNonce
	// IssuedAt 是签发操作读取一次得到的绝对时间快照。
	IssuedAt time.Time
	// ExpiresAt 是 ticket 的绝对消费期限，达到该时刻即不能连接。
	ExpiresAt time.Time
}

// Valid 报告 ticket projection 是否完整匹配既有跨端字段与 channel policy。
func (ticket ConnectionTicket) Valid() bool {
	return ticket.SessionID.Valid() && ticket.Epoch.Valid() && ticket.Channel != ChannelHTTPS &&
		ticket.Endpoint.Valid() && ticket.Endpoint.Channel() == ticket.Channel &&
		ticket.Scopes.validForChannel(ticket.Channel) && ticket.Nonce.Valid() &&
		!ticket.IssuedAt.IsZero() && ticket.ExpiresAt.After(ticket.IssuedAt)
}

// String 返回不含 nonce 的连接资格摘要，避免默认格式化泄漏一次性认证材料。
func (ticket ConnectionTicket) String() string {
	return "ConnectionTicket{session=" + ticket.SessionID.String() + ", channel=" + strconv.FormatUint(uint64(ticket.Channel), 10) + "}"
}

// GoString 与 String 保持相同安全格式化边界。
func (ticket ConnectionTicket) GoString() string { return ticket.String() }

// LogValue 只记录可撤销身份、channel 与 expiry，不记录 nonce 或完整 scopes。
func (ticket ConnectionTicket) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("session_id", ticket.SessionID.String()),
		slog.Uint64("epoch", uint64(ticket.Epoch)),
		slog.Uint64("channel", uint64(ticket.Channel)),
		slog.Time("expires_at", ticket.ExpiresAt),
	)
}

// Service 编排 transport-independent session、token、ticket 与失效流程。
//
// Service 不拥有 listener、数据库连接或 generated protocol type。
type Service struct {
	// store 执行不能在 application 层拆分的原子状态迁移。
	store SessionStore
	// endpoints 决定 ticket 可以连接的受信地址。
	endpoints EndpointProvider
	// invalidator 在权威 epoch 提交后关闭旧连接。
	invalidator ConnectionInvalidator
	// clock 固定一次调用中所有 expiry 判断的时间点。
	clock Clock
	// ids 创建服务端选择的 session identity。
	ids IDGenerator
	// secrets 创建不可预测 bearer credentials。
	secrets SecretGenerator
	// policy 保存启动后只读的 TTL 安全预算。
	policy Policy
}

// NewService 校验全部安全依赖，防止运行时静默跳过存储或连接失效通知。
func NewService(store SessionStore, endpoints EndpointProvider, invalidator ConnectionInvalidator, clock Clock, ids IDGenerator, secrets SecretGenerator, policy Policy) (*Service, error) {
	if store == nil || endpoints == nil || invalidator == nil || clock == nil || ids == nil || secrets == nil || !policy.Valid() {
		return nil, newError(ErrorKindInvalidArgument, "construct", errors.New("session service dependencies are incomplete"))
	}
	return &Service{store: store, endpoints: endpoints, invalidator: invalidator, clock: clock, ids: ids, secrets: secrets, policy: policy}, nil
}

// CreateSession 为上游已验证 principal 创建唯一 session 与首对 opaque token。
//
// principal 必须来自账号 application service，不能由注册或登录 payload 直接构造。
// Store 失败时不会返回 raw token；远程提交结果不明确时，上层应重新确认登录状态，
// 不能把本次生成但未返回的 credential 暴露给客户端。
func (service *Service) CreateSession(ctx context.Context, principal Principal) (SessionResult, error) {
	if !principal.Valid() {
		return SessionResult{}, newError(ErrorKindInvalidArgument, "create", nil)
	}
	now := service.clock.Now()
	randomID, err := service.ids.NewID()
	if err != nil {
		return SessionResult{}, newError(ErrorKindDependencyUnavailable, "create", err)
	}
	id, err := NewSessionID(sessionIDPrefix + randomID)
	if err != nil {
		return SessionResult{}, newError(ErrorKindDependencyUnavailable, "create", errors.New("ID generator returned invalid material"))
	}
	pair, accessRecord, refreshRecord, err := service.newTokenPair(id, InitialEpoch, now)
	if err != nil {
		return SessionResult{}, err
	}
	expiresAt := now.Add(service.policy.SessionTTL())
	bundle := SessionBundle{
		Session: SessionRecord{ID: id, Principal: principal, Epoch: InitialEpoch, Status: StatusActive, ExpiresAt: expiresAt},
		Access:  accessRecord,
		Refresh: refreshRecord,
	}
	outcome, err := service.store.Create(ctx, bundle)
	if err != nil {
		return SessionResult{}, newError(ErrorKindDependencyUnavailable, "create", err)
	}
	if outcome != StoreOutcomeApplied {
		return SessionResult{}, outcomeError("create", outcome)
	}
	return SessionResult{SessionID: id, Epoch: InitialEpoch, ExpiresAt: expiresAt, Tokens: pair}, nil
}

// AuthenticateAccess 验证 access secret 与当前 session 状态并构造 HTTPS AuthContext。
//
// raw 只允许在入站认证边界短暂存在，方法不会把它放入错误、日志或 AuthContext。
// Store dependency failure 必须 fail closed，不能退化为仅校验 token 格式。
func (service *Service) AuthenticateAccess(ctx context.Context, raw string) (AuthContext, error) {
	snapshot, _, err := service.authenticateSnapshot(ctx, raw)
	if err != nil {
		return AuthContext{}, err
	}
	emptyScopes, _ := NewScopeSet()
	auth, err := newAuthContext(snapshot.Principal, snapshot.SessionID, snapshot.Epoch, ChannelHTTPS, emptyScopes)
	if err != nil {
		return AuthContext{}, newError(ErrorKindDependencyUnavailable, "authenticate", err)
	}
	return auth, nil
}

// AuthenticateHTTPS 原子验证 access 与 session，并返回身份和共同有效截止时间。
//
// HTTP transport 应使用该方法把截止时间传入可能提交状态的 application service；
// AuthenticateAccess 仅作为不跨认证时刻提交状态的兼容入口。
func (service *Service) AuthenticateHTTPS(ctx context.Context, raw string) (AuthenticatedSession, error) {
	snapshot, now, err := service.authenticateSnapshot(ctx, raw)
	if err != nil {
		return AuthenticatedSession{}, err
	}
	emptyScopes, _ := NewScopeSet()
	auth, err := newAuthContext(snapshot.Principal, snapshot.SessionID, snapshot.Epoch, ChannelHTTPS, emptyScopes)
	if err != nil {
		return AuthenticatedSession{}, newError(ErrorKindDependencyUnavailable, "authenticate", err)
	}
	deadline := snapshot.AccessExpiresAt
	if snapshot.SessionExpiresAt.Before(deadline) {
		deadline = snapshot.SessionExpiresAt
	}
	authenticated, err := newAuthenticatedSession(auth, deadline, now)
	if err != nil {
		return AuthenticatedSession{}, newError(ErrorKindDependencyUnavailable, "authenticate", err)
	}
	return authenticated, nil
}

// authenticateSnapshot 统一解析 secret并取得当前原子store快照，避免两个认证入口语义漂移。
func (service *Service) authenticateSnapshot(ctx context.Context, raw string) (AuthSnapshot, time.Time, error) {
	secret, err := ParseSecret(SecretKindAccess, raw)
	if err != nil {
		return AuthSnapshot{}, time.Time{}, newError(ErrorKindUnauthenticated, "authenticate", nil)
	}
	now := service.clock.Now()
	snapshot, outcome, err := service.store.ResolveAccess(ctx, secret.Digest(), now)
	if err != nil {
		return AuthSnapshot{}, time.Time{}, newError(ErrorKindDependencyUnavailable, "authenticate", err)
	}
	if outcome != StoreOutcomeApplied {
		return AuthSnapshot{}, time.Time{}, outcomeError("authenticate", outcome)
	}
	return snapshot, now, nil
}

// Refresh 原子替换 token pair；重放会先提交 epoch 失效再通知连接边界。
//
// 成功结果中的 expiry 采用 store 截断后的事实，保证轮换不能延长 session。依赖错误
// 不返回预生成 secret；调用方不能据此假定旧 refresh 仍未消费，也不能自动重复提交。
func (service *Service) Refresh(ctx context.Context, raw string) (TokenPair, error) {
	secret, err := ParseSecret(SecretKindRefresh, raw)
	if err != nil {
		return TokenPair{}, newError(ErrorKindUnauthenticated, "refresh", nil)
	}
	now := service.clock.Now()
	access, err := NewSecret(SecretKindAccess, service.secrets)
	if err != nil {
		return TokenPair{}, newError(ErrorKindDependencyUnavailable, "refresh", err)
	}
	refresh, err := NewSecret(SecretKindRefresh, service.secrets)
	if err != nil {
		return TokenPair{}, newError(ErrorKindDependencyUnavailable, "refresh", err)
	}
	rotation := Rotation{
		PresentedRefresh: secret.Digest(),
		Access:           TokenRecord{Digest: access.Digest(), ExpiresAt: now.Add(service.policy.AccessTTL())},
		Refresh:          TokenRecord{Digest: refresh.Digest(), ExpiresAt: now.Add(service.policy.RefreshTTL())},
		Now:              now,
	}
	snapshot, invalidation, outcome, err := service.store.RotateRefresh(ctx, rotation)
	if err != nil {
		return TokenPair{}, newError(ErrorKindDependencyUnavailable, "refresh", err)
	}
	if outcome == StoreOutcomeReplayed {
		if invalidation.SessionID.Valid() {
			if err := service.invalidator.Invalidate(ctx, invalidation); err != nil {
				return TokenPair{}, newError(ErrorKindDependencyUnavailable, "notify-invalidation", err)
			}
		}
		return TokenPair{}, newError(ErrorKindReplayed, "refresh", nil)
	}
	if outcome != StoreOutcomeApplied {
		return TokenPair{}, outcomeError("refresh", outcome)
	}
	if !snapshot.SessionID.Valid() || !snapshot.Epoch.Valid() {
		return TokenPair{}, newError(ErrorKindDependencyUnavailable, "refresh", errors.New("store returned invalid snapshot"))
	}
	if !now.Before(snapshot.AccessExpiresAt) || !now.Before(snapshot.RefreshExpiresAt) || snapshot.AccessExpiresAt.After(snapshot.RefreshExpiresAt) {
		return TokenPair{}, newError(ErrorKindDependencyUnavailable, "refresh", errors.New("store returned invalid token expiry"))
	}
	return TokenPair{Access: access, AccessExpiresAt: snapshot.AccessExpiresAt, Refresh: refresh, RefreshExpiresAt: snapshot.RefreshExpiresAt}, nil
}

// IssueTicket 为受支持 realtime channel 签发短期、固定 scope、固定 endpoint 的一次性资格。
//
// auth 必须来自当前认证边界；客户端只能选择受支持 channel，不能提交 endpoint 或
// scopes。Store 在写入时再次校验 session/epoch，避免旧 AuthContext 签发新资格。
func (service *Service) IssueTicket(ctx context.Context, auth AuthContext, channel Channel) (ConnectionTicket, error) {
	if !auth.Valid() {
		return ConnectionTicket{}, newError(ErrorKindInvalidArgument, "issue-ticket", nil)
	}
	if auth.Channel() != ChannelHTTPS {
		return ConnectionTicket{}, newError(ErrorKindForbidden, "issue-ticket", nil)
	}
	scopes, err := scopesForChannel(channel)
	if err != nil {
		return ConnectionTicket{}, err
	}
	endpoint, err := service.endpoints.EndpointFor(ctx, channel)
	if err != nil {
		return ConnectionTicket{}, newError(ErrorKindDependencyUnavailable, "resolve-endpoint", err)
	}
	if !endpoint.Valid() || endpoint.Channel() != channel {
		return ConnectionTicket{}, newError(ErrorKindDependencyUnavailable, "resolve-endpoint", errors.New("provider returned mismatched endpoint"))
	}
	nonce, err := NewTicketNonce(service.secrets)
	if err != nil {
		return ConnectionTicket{}, newError(ErrorKindDependencyUnavailable, "issue-ticket", err)
	}
	now := service.clock.Now()
	expiresAt := now.Add(service.policy.TicketTTL())
	record := TicketRecord{Digest: nonce.Digest(), SessionID: auth.SessionID(), Epoch: auth.Epoch(), Channel: channel, Endpoint: endpoint, Scopes: scopes, ExpiresAt: expiresAt}
	outcome, err := service.store.IssueTicket(ctx, record, now)
	if err != nil {
		return ConnectionTicket{}, newError(ErrorKindDependencyUnavailable, "issue-ticket", err)
	}
	if outcome != StoreOutcomeApplied {
		return ConnectionTicket{}, outcomeError("issue-ticket", outcome)
	}
	result := ConnectionTicket{
		SessionID: auth.SessionID(),
		Epoch:     auth.Epoch(),
		Channel:   channel,
		Endpoint:  endpoint,
		Scopes:    scopes,
		Nonce:     nonce,
		IssuedAt:  now,
		ExpiresAt: expiresAt,
	}
	if !result.Valid() {
		return ConnectionTicket{}, newError(ErrorKindDependencyUnavailable, "issue-ticket", errors.New("ticket projection is invalid"))
	}
	return result, nil
}

// ConsumeTicket 原子校验 ticket 的 channel、endpoint、expiry 与当前 epoch。
//
// channel 与 endpoint 必须描述正在接收握手的真实 listener。绑定失败不会切换为
// access-token 认证，也不会授予 AuthContext；是否消费由 SessionStore 原子契约决定。
func (service *Service) ConsumeTicket(ctx context.Context, nonce TicketNonce, channel Channel, endpoint Endpoint) (AuthContext, error) {
	if !nonce.Valid() {
		return AuthContext{}, newError(ErrorKindUnauthenticated, "consume-ticket", nil)
	}
	if !endpoint.Valid() || endpoint.Channel() != channel {
		return AuthContext{}, newError(ErrorKindInvalidArgument, "consume-ticket", nil)
	}
	snapshot, outcome, err := service.store.ConsumeTicket(ctx, nonce.Digest(), channel, endpoint, service.clock.Now())
	if err != nil {
		return AuthContext{}, newError(ErrorKindDependencyUnavailable, "consume-ticket", err)
	}
	if outcome != StoreOutcomeApplied {
		return AuthContext{}, outcomeError("consume-ticket", outcome)
	}
	auth, err := newAuthContext(snapshot.Principal, snapshot.SessionID, snapshot.Epoch, channel, snapshot.Scopes)
	if err != nil {
		return AuthContext{}, newError(ErrorKindDependencyUnavailable, "consume-ticket", err)
	}
	return auth, nil
}

// Logout 撤销 AuthContext 所属 session，再幂等通知连接边界。
//
// 通知失败时返回已经提交的 Invalidation 与 dependency error，调用方应重试通知，
// 不能重新启用旧 token、ticket 或连接；重复失效不得继续递增 epoch。
func (service *Service) Logout(ctx context.Context, auth AuthContext) (Invalidation, error) {
	if !auth.Valid() {
		return Invalidation{}, newError(ErrorKindInvalidArgument, "logout", nil)
	}
	return service.invalidateSession(ctx, auth.SessionID(), InvalidationReasonLogout)
}

// ForceLogout 撤销受信管理边界指定的单一 session，不接受客户端选择 reason。
func (service *Service) ForceLogout(ctx context.Context, id SessionID) (Invalidation, error) {
	if !id.Valid() {
		return Invalidation{}, newError(ErrorKindInvalidArgument, "force-logout", nil)
	}
	return service.invalidateSession(ctx, id, InvalidationReasonForcedLogout)
}

// invalidateSession 统一执行先提交 epoch、再通知连接的单 session 流程。
func (service *Service) invalidateSession(ctx context.Context, id SessionID, reason InvalidationReason) (Invalidation, error) {
	invalidation, outcome, err := service.store.InvalidateSession(ctx, id, reason)
	if err != nil {
		return Invalidation{}, newError(ErrorKindDependencyUnavailable, "invalidate-session", err)
	}
	if outcome != StoreOutcomeApplied && outcome != StoreOutcomeInvalidated {
		return Invalidation{}, outcomeError("invalidate-session", outcome)
	}
	if err := service.invalidator.Invalidate(ctx, invalidation); err != nil {
		return invalidation, newError(ErrorKindDependencyUnavailable, "notify-invalidation", err)
	}
	return invalidation, nil
}

// InvalidatePrincipal 撤销 principal 当前全部 sessions，不决定未来登录许可。
//
// Store 必须先原子提交完整结果，随后本方法尝试通知每个 session，即使前一通知失败
// 也不会提前停止。返回 dependency error 时 invalidations 仍是已经生效的权威事实。
func (service *Service) InvalidatePrincipal(ctx context.Context, principal Principal) ([]Invalidation, error) {
	if !principal.Valid() {
		return nil, newError(ErrorKindInvalidArgument, "invalidate-principal", nil)
	}
	invalidations, err := service.store.InvalidatePrincipal(ctx, principal, InvalidationReasonPrincipalBan)
	if err != nil {
		return nil, newError(ErrorKindDependencyUnavailable, "invalidate-principal", err)
	}
	var notificationFailure error
	for _, invalidation := range invalidations {
		if err := service.invalidator.Invalidate(ctx, invalidation); err != nil {
			if notificationFailure == nil {
				notificationFailure = err
			}
		}
	}
	if notificationFailure != nil {
		return invalidations, newError(ErrorKindDependencyUnavailable, "notify-invalidation", notificationFailure)
	}
	return invalidations, nil
}

// newTokenPair 生成创建 session 所需的 raw 返回值与 digest records。
func (service *Service) newTokenPair(id SessionID, epoch Epoch, now time.Time) (TokenPair, TokenRecord, TokenRecord, error) {
	access, err := NewSecret(SecretKindAccess, service.secrets)
	if err != nil {
		return TokenPair{}, TokenRecord{}, TokenRecord{}, newError(ErrorKindDependencyUnavailable, "generate-token", err)
	}
	refresh, err := NewSecret(SecretKindRefresh, service.secrets)
	if err != nil {
		return TokenPair{}, TokenRecord{}, TokenRecord{}, newError(ErrorKindDependencyUnavailable, "generate-token", err)
	}
	accessExpiry := now.Add(service.policy.AccessTTL())
	refreshExpiry := now.Add(service.policy.RefreshTTL())
	pair := TokenPair{Access: access, AccessExpiresAt: accessExpiry, Refresh: refresh, RefreshExpiresAt: refreshExpiry}
	return pair,
		TokenRecord{Digest: access.Digest(), SessionID: id, Epoch: epoch, ExpiresAt: accessExpiry},
		TokenRecord{Digest: refresh.Digest(), SessionID: id, Epoch: epoch, ExpiresAt: refreshExpiry}, nil
}

// scopesForChannel 固定 channel capability matrix，调用方不能提交自定义 scopes。
func scopesForChannel(channel Channel) (ScopeSet, error) {
	switch channel {
	case ChannelWSS:
		return NewScopeSet(ScopeControl)
	case ChannelTLSTCP:
		return NewScopeSet(ScopeGameplay)
	default:
		return ScopeSet{}, newError(ErrorKindInvalidArgument, "issue-ticket", nil)
	}
}

// outcomeError 将存储结果收敛为 transport-independent 稳定错误类别。
func outcomeError(operation string, outcome StoreOutcome) error {
	switch outcome {
	case StoreOutcomeExpired:
		return newError(ErrorKindExpired, operation, nil)
	case StoreOutcomeReplayed:
		return newError(ErrorKindReplayed, operation, nil)
	case StoreOutcomeConflict:
		return newError(ErrorKindConflict, operation, nil)
	case StoreOutcomeEpochMismatch, StoreOutcomeInvalidated, StoreOutcomeNotFound:
		return newError(ErrorKindUnauthenticated, operation, nil)
	default:
		return newError(ErrorKindDependencyUnavailable, operation, errors.New("store returned unspecified outcome"))
	}
}
