package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// testClock 提供可并发读取和推进的绝对时间。
type testClock struct {
	// mutex 保护 now，供 race test 并发调用 Service。
	mutex sync.RWMutex
	// now 是测试显式控制的唯一时间事实。
	now time.Time
}

// Now 返回当前测试时间快照。
func (clock *testClock) Now() time.Time {
	clock.mutex.RLock()
	defer clock.mutex.RUnlock()
	return clock.now
}

// Advance 原子推进测试时间，不允许生产代码隐式等待。
func (clock *testClock) Advance(duration time.Duration) {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	clock.now = clock.now.Add(duration)
}

// testIDGenerator 生成确定且互不冲突的测试 session id。
type testIDGenerator struct {
	// mutex 保护 next，允许并发创建 session。
	mutex sync.Mutex
	// next 是测试 identifier 的单调序号。
	next int
}

// invalidIDGenerator 模拟违反安全 ASCII 契约的基础依赖。
type invalidIDGenerator struct{}

// NewID 返回会破坏 Redis key 与日志边界的非法材料。
func (invalidIDGenerator) NewID() (string, error) { return "{invalid}", nil }

// NewID 返回只包含安全 ASCII 的确定 identifier 材料。
func (generator *testIDGenerator) NewID() (string, error) {
	generator.mutex.Lock()
	defer generator.mutex.Unlock()
	generator.next++
	return fmt.Sprintf("fixture-%d", generator.next), nil
}

// testEndpointProvider 模拟经过启动配置验证的 realtime manifest。
type testEndpointProvider struct {
	// endpoints 按封闭 channel 返回固定地址。
	endpoints map[Channel]Endpoint
	// err 模拟依赖不可用而不暴露客户端输入。
	err error
}

// EndpointFor 返回预配置 endpoint，不根据请求 host 或 port 动态构造。
func (provider *testEndpointProvider) EndpointFor(_ context.Context, channel Channel) (Endpoint, error) {
	if provider.err != nil {
		return Endpoint{}, provider.err
	}
	endpoint, exists := provider.endpoints[channel]
	if !exists {
		return Endpoint{}, errors.New("endpoint unavailable")
	}
	return endpoint, nil
}

// testInvalidator 记录连接通知，并可模拟通知依赖失败。
type testInvalidator struct {
	// mutex 保护 calls 与 failure，供并发失效测试使用。
	mutex sync.Mutex
	// calls 保存已接收的幂等安全事实。
	calls []Invalidation
	// failure 使通知在权威状态提交后失败。
	failure error
}

// Invalidate 记录调用；失败时仍证明通知发生在 store commit 之后。
func (invalidator *testInvalidator) Invalidate(_ context.Context, invalidation Invalidation) error {
	invalidator.mutex.Lock()
	defer invalidator.mutex.Unlock()
	invalidator.calls = append(invalidator.calls, invalidation)
	return invalidator.failure
}

// Calls 返回通知副本，防止测试修改 recorder 内部状态。
func (invalidator *testInvalidator) Calls() []Invalidation {
	invalidator.mutex.Lock()
	defer invalidator.mutex.Unlock()
	return append([]Invalidation(nil), invalidator.calls...)
}

// serviceFixture 汇总不依赖 listener、Redis 或 wall clock 的 session 测试环境。
type serviceFixture struct {
	// service 是被测 application boundary。
	service *Service
	// store 用于验证权威状态变化，不进入生产 Composition Root。
	store *testSessionStore
	// clock 控制 expiry 边界。
	clock *testClock
	// invalidator 观察 store commit 后的连接通知。
	invalidator *testInvalidator
	// wss 与 tcp 是 provider 唯一允许签发的 endpoint。
	wss Endpoint
	tcp Endpoint
}

// newServiceFixture 创建固定 policy 和依赖的可重复测试环境。
func newServiceFixture(t *testing.T) serviceFixture {
	t.Helper()
	wss, err := NewEndpoint(ChannelWSS, "control.example.test", 443)
	if err != nil {
		t.Fatal(err)
	}
	tcp, err := NewEndpoint(ChannelTLSTCP, "game.example.test", 8443)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewPolicy(30*time.Second, 15*time.Minute, 7*24*time.Hour, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	store := newTestSessionStore()
	clock := &testClock{now: time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)}
	invalidator := new(testInvalidator)
	service, err := NewService(
		store,
		&testEndpointProvider{endpoints: map[Channel]Endpoint{ChannelWSS: wss, ChannelTLSTCP: tcp}},
		invalidator,
		clock,
		new(testIDGenerator),
		new(deterministicSecretGenerator),
		policy,
	)
	if err != nil {
		t.Fatal(err)
	}
	return serviceFixture{service: service, store: store, clock: clock, invalidator: invalidator, wss: wss, tcp: tcp}
}

// mustPrincipal 创建测试身份，并让失败定位到调用处。
func mustPrincipal(t *testing.T, suffix string) Principal {
	t.Helper()
	principal, err := NewPrincipal("account-"+suffix, "player-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	return principal
}

// TestSessionCreateAuthenticateAndRotate 验证首发 token、access 撤销与新 token 生效。
func TestSessionCreateAuthenticateAndRotate(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateSession(context.Background(), mustPrincipal(t, "one"))
	if err != nil {
		t.Fatal(err)
	}
	if created.Epoch != InitialEpoch || created.Tokens.Access.Kind() != SecretKindAccess || created.Tokens.Refresh.Kind() != SecretKindRefresh {
		t.Fatal("session creation returned an invalid identity or token pair")
	}
	auth, err := fixture.service.AuthenticateAccess(context.Background(), created.Tokens.Access.Reveal())
	if err != nil || auth.SessionID() != created.SessionID || auth.Principal() != mustPrincipal(t, "one") {
		t.Fatalf("initial access authentication failed: %v", err)
	}
	rotated, err := fixture.service.Refresh(context.Background(), created.Tokens.Refresh.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.AuthenticateAccess(context.Background(), created.Tokens.Access.Reveal()); ErrorKindOf(err) != ErrorKindUnauthenticated {
		t.Fatalf("rotated access remained valid: %v", err)
	}
	if _, err := fixture.service.AuthenticateAccess(context.Background(), rotated.Access.Reveal()); err != nil {
		t.Fatalf("new access token was rejected: %v", err)
	}
}

// TestAccessExpiresAtBoundary 验证达到绝对 expiry 时不再创建 AuthContext。
func TestAccessExpiresAtBoundary(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateSession(context.Background(), mustPrincipal(t, "access-expiry"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.Advance(15 * time.Minute)
	if _, err := fixture.service.AuthenticateAccess(context.Background(), created.Tokens.Access.Reveal()); ErrorKindOf(err) != ErrorKindExpired {
		t.Fatalf("access token accepted at expiry boundary: %v", err)
	}
}

// TestRefreshReplayInvalidatesLineage 验证 tombstone 重放递增 epoch 并撤销新旧资格。
func TestRefreshReplayInvalidatesLineage(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateSession(context.Background(), mustPrincipal(t, "replay"))
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := fixture.service.Refresh(context.Background(), created.Tokens.Refresh.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Refresh(context.Background(), created.Tokens.Refresh.Reveal()); ErrorKindOf(err) != ErrorKindReplayed {
		t.Fatalf("consumed refresh was not classified as replay: %v", err)
	}
	calls := fixture.invalidator.Calls()
	if len(calls) != 1 || calls[0].Epoch != InitialEpoch+1 || calls[0].Reason != InvalidationReasonRefreshReplay {
		t.Fatalf("unexpected replay invalidation: %+v", calls)
	}
	if _, err := fixture.service.AuthenticateAccess(context.Background(), rotated.Access.Reveal()); ErrorKindOf(err) != ErrorKindUnauthenticated {
		t.Fatalf("replay did not revoke current access: %v", err)
	}
}

// TestConcurrentRefreshHasSingleWinnerAndInvalidatesReplay 并发提交同一 refresh 时至多一个返回 token pair。
func TestConcurrentRefreshHasSingleWinnerAndInvalidatesReplay(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateSession(context.Background(), mustPrincipal(t, "race"))
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errorsByCall := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, refreshErr := fixture.service.Refresh(context.Background(), created.Tokens.Refresh.Reveal())
			errorsByCall <- refreshErr
		}()
	}
	close(start)
	successes := 0
	replays := 0
	for range 2 {
		err := <-errorsByCall
		if err == nil {
			successes++
		} else if ErrorKindOf(err) == ErrorKindReplayed {
			replays++
		}
	}
	if successes != 1 || replays != 1 {
		t.Fatalf("concurrent refresh results: success=%d replay=%d", successes, replays)
	}
}

// TestTicketBindingConsumptionAndExpiry 覆盖 scope matrix、错误 listener、单次消费与 expiry。
func TestTicketBindingConsumptionAndExpiry(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateSession(context.Background(), mustPrincipal(t, "ticket"))
	if err != nil {
		t.Fatal(err)
	}
	auth, err := fixture.service.AuthenticateAccess(context.Background(), created.Tokens.Access.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := fixture.service.IssueTicket(context.Background(), auth, ChannelWSS)
	if err != nil {
		t.Fatal(err)
	}
	if !ticket.Valid() || ticket.Endpoint != fixture.wss || ticket.Channel != ChannelWSS || ticket.SessionID != created.SessionID ||
		ticket.Epoch != created.Epoch || !ticket.Scopes.Has(ScopeControl) || !ticket.Nonce.Valid() ||
		!ticket.IssuedAt.Equal(fixture.clock.Now()) {
		t.Fatal("ticket endpoint did not come from trusted provider")
	}
	if _, err := fixture.service.ConsumeTicket(context.Background(), ticket.Nonce, ChannelTLSTCP, fixture.tcp); ErrorKindOf(err) != ErrorKindUnauthenticated {
		t.Fatalf("wrong channel was not rejected: %v", err)
	}
	wrongEndpoint, _ := NewEndpoint(ChannelWSS, "other.example.test", 443)
	if _, err := fixture.service.ConsumeTicket(context.Background(), ticket.Nonce, ChannelWSS, wrongEndpoint); ErrorKindOf(err) != ErrorKindUnauthenticated {
		t.Fatalf("wrong endpoint was not rejected: %v", err)
	}
	realtimeAuth, err := fixture.service.ConsumeTicket(context.Background(), ticket.Nonce, ChannelWSS, fixture.wss)
	if err != nil || !realtimeAuth.HasScope(ScopeControl) || realtimeAuth.HasScope(ScopeGameplay) {
		t.Fatalf("WSS ticket scopes are invalid: auth=%+v err=%v", realtimeAuth.Scopes(), err)
	}
	if _, err := fixture.service.IssueTicket(context.Background(), realtimeAuth, ChannelWSS); ErrorKindOf(err) != ErrorKindForbidden {
		t.Fatalf("realtime context issued another ticket: %v", err)
	}
	if _, err := fixture.service.ConsumeTicket(context.Background(), ticket.Nonce, ChannelWSS, fixture.wss); ErrorKindOf(err) != ErrorKindReplayed {
		t.Fatalf("ticket replay was not rejected: %v", err)
	}
	expiring, err := fixture.service.IssueTicket(context.Background(), auth, ChannelTLSTCP)
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.Advance(30 * time.Second)
	if _, err := fixture.service.ConsumeTicket(context.Background(), expiring.Nonce, ChannelTLSTCP, fixture.tcp); ErrorKindOf(err) != ErrorKindExpired {
		t.Fatalf("ticket accepted at expiry boundary: %v", err)
	}
}

// TestConcurrentTicketConsumptionHasSingleWinner 证明 compare-and-consume 不会授权两个连接。
func TestConcurrentTicketConsumptionHasSingleWinner(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateSession(context.Background(), mustPrincipal(t, "ticket-race"))
	if err != nil {
		t.Fatal(err)
	}
	auth, err := fixture.service.AuthenticateAccess(context.Background(), created.Tokens.Access.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := fixture.service.IssueTicket(context.Background(), auth, ChannelWSS)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, consumeErr := fixture.service.ConsumeTicket(context.Background(), ticket.Nonce, ChannelWSS, fixture.wss)
			results <- consumeErr
		}()
	}
	close(start)
	successes := 0
	replays := 0
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else if ErrorKindOf(err) == ErrorKindReplayed {
			replays++
		}
	}
	if successes != 1 || replays != 1 {
		t.Fatalf("concurrent consume results: success=%d replay=%d", successes, replays)
	}
}

// TestRefreshExpiryIsClampedToSessionLifetime 防止轮换把 session 变相续期。
func TestRefreshExpiryIsClampedToSessionLifetime(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateSession(context.Background(), mustPrincipal(t, "lifetime"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.Advance(6*24*time.Hour + 23*time.Hour)
	rotated, err := fixture.service.Refresh(context.Background(), created.Tokens.Refresh.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	if !rotated.RefreshExpiresAt.Equal(created.ExpiresAt) || rotated.AccessExpiresAt.After(created.ExpiresAt) {
		t.Fatalf("rotation extended session lifetime: session=%v access=%v refresh=%v", created.ExpiresAt, rotated.AccessExpiresAt, rotated.RefreshExpiresAt)
	}
}

// TestOldEpochTicketAndAccessFailAfterInvalidation 验证通知之前提交的 epoch 是权威屏障。
func TestOldEpochTicketAndAccessFailAfterInvalidation(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateSession(context.Background(), mustPrincipal(t, "logout"))
	if err != nil {
		t.Fatal(err)
	}
	auth, err := fixture.service.AuthenticateAccess(context.Background(), created.Tokens.Access.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := fixture.service.IssueTicket(context.Background(), auth, ChannelWSS)
	if err != nil {
		t.Fatal(err)
	}
	invalidation, err := fixture.service.Logout(context.Background(), auth)
	if err != nil || invalidation.Epoch != InitialEpoch+1 {
		t.Fatalf("logout invalidation failed: %+v %v", invalidation, err)
	}
	if _, err := fixture.service.AuthenticateAccess(context.Background(), created.Tokens.Access.Reveal()); ErrorKindOf(err) != ErrorKindUnauthenticated {
		t.Fatalf("old access survived logout: %v", err)
	}
	if _, err := fixture.service.ConsumeTicket(context.Background(), ticket.Nonce, ChannelWSS, fixture.wss); ErrorKindOf(err) != ErrorKindUnauthenticated {
		t.Fatalf("old epoch ticket survived logout: %v", err)
	}
}

// TestNotificationFailureDoesNotRollbackAuthority 验证连接通知失败只影响清理速度，不恢复身份。
func TestNotificationFailureDoesNotRollbackAuthority(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateSession(context.Background(), mustPrincipal(t, "notify"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.invalidator.failure = errors.New("registry unavailable")
	invalidation, err := fixture.service.ForceLogout(context.Background(), created.SessionID)
	if ErrorKindOf(err) != ErrorKindDependencyUnavailable || invalidation.Epoch != InitialEpoch+1 {
		t.Fatalf("notification failure lost committed fact: %+v %v", invalidation, err)
	}
	if _, err := fixture.service.AuthenticateAccess(context.Background(), created.Tokens.Access.Reveal()); ErrorKindOf(err) != ErrorKindUnauthenticated {
		t.Fatalf("notification failure rolled back session: %v", err)
	}
}

// TestConcurrentInvalidationPublishesStableEpoch 验证重复撤销不会持续递增 epoch。
func TestConcurrentInvalidationPublishesStableEpoch(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateSession(context.Background(), mustPrincipal(t, "invalidate-race"))
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan Invalidation, 2)
	errorsByCall := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			invalidation, invalidateErr := fixture.service.ForceLogout(context.Background(), created.SessionID)
			results <- invalidation
			errorsByCall <- invalidateErr
		}()
	}
	close(start)
	for range 2 {
		invalidation := <-results
		if err := <-errorsByCall; err != nil || invalidation.Epoch != InitialEpoch+1 {
			t.Fatalf("concurrent invalidation was not idempotent: %+v %v", invalidation, err)
		}
	}
}

// TestPrincipalInvalidationRevokesAllExistingSessions 验证 ban 输入只撤销当前 sessions。
func TestPrincipalInvalidationRevokesAllExistingSessions(t *testing.T) {
	fixture := newServiceFixture(t)
	principal := mustPrincipal(t, "ban")
	first, err := fixture.service.CreateSession(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.CreateSession(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	invalidations, err := fixture.service.InvalidatePrincipal(context.Background(), principal)
	if err != nil || len(invalidations) != 2 {
		t.Fatalf("principal invalidation failed: count=%d err=%v", len(invalidations), err)
	}
	for _, raw := range []string{first.Tokens.Access.Reveal(), second.Tokens.Access.Reveal()} {
		if _, err := fixture.service.AuthenticateAccess(context.Background(), raw); ErrorKindOf(err) != ErrorKindUnauthenticated {
			t.Fatalf("banned principal access survived: %v", err)
		}
	}
}

// TestPrincipalNotificationAttemptsAllSessions 验证单次通知故障不会阻止其余 session 清理。
func TestPrincipalNotificationAttemptsAllSessions(t *testing.T) {
	fixture := newServiceFixture(t)
	principal := mustPrincipal(t, "ban-notify")
	for range 2 {
		if _, err := fixture.service.CreateSession(context.Background(), principal); err != nil {
			t.Fatal(err)
		}
	}
	fixture.invalidator.failure = errors.New("registry unavailable")
	invalidations, err := fixture.service.InvalidatePrincipal(context.Background(), principal)
	if ErrorKindOf(err) != ErrorKindDependencyUnavailable || len(invalidations) != 2 || len(fixture.invalidator.Calls()) != 2 {
		t.Fatalf("principal notifications stopped early: invalidations=%d calls=%d err=%v", len(invalidations), len(fixture.invalidator.Calls()), err)
	}
}

// TestErrorsAndFormattingDoNotExposeCredential 验证失败分类和默认输出都不包含 raw secret。
func TestErrorsAndFormattingDoNotExposeCredential(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateSession(context.Background(), mustPrincipal(t, "redact"))
	if err != nil {
		t.Fatal(err)
	}
	raw := created.Tokens.Refresh.Reveal()
	_, err = fixture.service.Refresh(context.Background(), strings.Replace(raw, "ih_rt_", "ih_at_", 1))
	if ErrorKindOf(err) != ErrorKindUnauthenticated || strings.Contains(fmt.Sprintf("%v", err), raw) {
		t.Fatalf("unsafe or unstable credential error: %v", err)
	}
	if strings.Contains(fmt.Sprintf("%+v", created), raw) {
		t.Fatal("default result formatting exposed raw credential")
	}
	principal := mustPrincipal(t, "sensitive")
	auth, authErr := newAuthContext(principal, created.SessionID, created.Epoch, ChannelHTTPS, ScopeSet{})
	if authErr != nil {
		t.Fatal(authErr)
	}
	formatted := fmt.Sprintf("%v %#v %v", principal, principal, auth)
	if strings.Contains(formatted, principal.AccountID()) || strings.Contains(formatted, principal.PlayerID()) {
		t.Fatalf("identity formatting exposed principal: %s", formatted)
	}
}

// TestStructuredLoggingRedactsIdentityAndCredentials 验证所有可误传给 slog 的领域值保持脱敏。
func TestStructuredLoggingRedactsIdentityAndCredentials(t *testing.T) {
	fixture := newServiceFixture(t)
	principal := mustPrincipal(t, "structured-log")
	created, err := fixture.service.CreateSession(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := fixture.service.AuthenticateAccess(context.Background(), created.Tokens.Access.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := fixture.service.IssueTicket(context.Background(), auth, ChannelWSS)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	logger.Info("session values", "principal", principal, "auth", auth, "ticket", ticket, "nonce", ticket.Nonce, "digest", ticket.Nonce.Digest())
	nonceBytes := ticket.Nonce.Bytes()
	for _, forbidden := range []string{
		principal.AccountID(),
		principal.PlayerID(),
		created.Tokens.Access.Reveal(),
		created.Tokens.Refresh.Reveal(),
		fmt.Sprintf("%x", nonceBytes),
		fmt.Sprintf("%x", ticket.Nonce.Digest().Bytes()),
	} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatalf("structured log exposed sensitive value: %s", output.String())
		}
	}
}

// TestServiceRejectsInvalidTrustBoundaries 验证公开入口在调用 store 前拒绝零值与错误 channel。
func TestServiceRejectsInvalidTrustBoundaries(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateSession(context.Background(), mustPrincipal(t, "guard"))
	if err != nil {
		t.Fatal(err)
	}
	auth, err := fixture.service.AuthenticateAccess(context.Background(), created.Tokens.Access.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]struct {
		run  func() error
		kind ErrorKind
	}{
		"zero auth ticket": {run: func() error {
			_, err := fixture.service.IssueTicket(context.Background(), AuthContext{}, ChannelWSS)
			return err
		}, kind: ErrorKindInvalidArgument},
		"HTTPS target": {run: func() error {
			_, err := fixture.service.IssueTicket(context.Background(), auth, ChannelHTTPS)
			return err
		}, kind: ErrorKindInvalidArgument},
		"zero nonce": {run: func() error {
			_, err := fixture.service.ConsumeTicket(context.Background(), TicketNonce{}, ChannelWSS, fixture.wss)
			return err
		}, kind: ErrorKindUnauthenticated},
		"zero auth logout": {run: func() error { _, err := fixture.service.Logout(context.Background(), AuthContext{}); return err }, kind: ErrorKindInvalidArgument},
		"zero forced id":   {run: func() error { _, err := fixture.service.ForceLogout(context.Background(), SessionID{}); return err }, kind: ErrorKindInvalidArgument},
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if err := check.run(); ErrorKindOf(err) != check.kind {
				t.Fatalf("trust boundary returned %v, want %v: %v", ErrorKindOf(err), check.kind, err)
			}
		})
	}
}

// TestSessionErrorKeepsStableKindAndHiddenCause 验证诊断链可检查但默认错误文本不泄漏依赖细节。
func TestSessionErrorKeepsStableKindAndHiddenCause(t *testing.T) {
	cause := errors.New("sensitive storage key")
	err := newError(ErrorKindDependencyUnavailable, "lookup", cause)
	var failure *Error
	if !errors.As(err, &failure) || !errors.Is(err, cause) || failure.Kind() != ErrorKindDependencyUnavailable {
		t.Fatal("session error lost stable kind or diagnostic cause")
	}
	if strings.Contains(err.Error(), cause.Error()) || !strings.Contains(err.Error(), ErrorKindDependencyUnavailable.String()) {
		t.Fatalf("session error text is unsafe or unreadable: %v", err)
	}
	if ErrorKind(255).String() != "unspecified" {
		t.Fatal("unknown error kind did not fail closed to unspecified")
	}
}

// TestStoreFailureIsDependencyUnavailable 验证 adapter 故障不会降级为未知凭据或被吞掉。
func TestStoreFailureIsDependencyUnavailable(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateSession(context.Background(), mustPrincipal(t, "dependency"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.store.mutex.Lock()
	fixture.store.failure = errors.New("storage unavailable")
	fixture.store.mutex.Unlock()
	if _, err := fixture.service.AuthenticateAccess(context.Background(), created.Tokens.Access.Reveal()); ErrorKindOf(err) != ErrorKindDependencyUnavailable {
		t.Fatalf("store failure was misclassified: %v", err)
	}
}

// TestInvalidGeneratedSessionIDFailsClosed 验证通用 ID generator 不能绕过 session identifier 校验。
func TestInvalidGeneratedSessionIDFailsClosed(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.ids = invalidIDGenerator{}
	if _, err := fixture.service.CreateSession(context.Background(), mustPrincipal(t, "invalid-id")); ErrorKindOf(err) != ErrorKindDependencyUnavailable {
		t.Fatalf("invalid generated ID was not rejected as a dependency defect: %v", err)
	}
}

// TestErrorKindOfUnknownError 防止 adapter 把任意依赖错误误分类为认证结果。
func TestErrorKindOfUnknownError(t *testing.T) {
	if ErrorKindOf(errors.New("unknown")) != ErrorKindUnspecified {
		t.Fatal("unknown errors must not receive a session classification")
	}
}
