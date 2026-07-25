package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/battleentry"
	"github.com/jinwiforz/ihomeland/server/internal/battleticket"
	"github.com/jinwiforz/ihomeland/server/internal/config"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
	"github.com/jinwiforz/ihomeland/server/internal/worldentry"
)

// TestRouterVersionAndReadiness 验证公开version成功且draining gate在调用application前拒绝。
func TestRouterVersionAndReadiness(t *testing.T) {
	ready := true
	router := newTestRouter(t, &ready, 10)
	response := performRequest(router.Handler(), http.MethodGet, "/v1/version", "", nil)
	if response.Code != http.StatusOK || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("version response=%d headers=%v", response.Code, response.Header())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["protocolVersion"] != float64(1) || body["serverVersion"] != "0.1.0" {
		t.Fatalf("version body=%s err=%v", response.Body.String(), err)
	}
	response = performRequest(router.Handler(), http.MethodGet, "/v1/version", "", map[string]string{"X-Request-ID": strings.Repeat("x", 65)})
	if response.Header().Get("X-Request-ID") == strings.Repeat("x", 65) || len(response.Header().Get("X-Request-ID")) > 64 {
		t.Fatalf("unsafe request ID was echoed: %q", response.Header().Get("X-Request-ID"))
	}
	response = performRequest(router.Handler(), http.MethodGet, "/v1/version?debug=true", "", nil)
	assertErrorResponse(t, response, http.StatusBadRequest, 200)
	ready = false
	response = performRequest(router.Handler(), http.MethodGet, "/v1/version", "", nil)
	assertErrorResponse(t, response, http.StatusServiceUnavailable, 500)
}

// TestRouterRejectsClosedOrDuplicateJSONBeforeApplication 验证closed object与重复字段不调用Account owner。
func TestRouterRejectsClosedOrDuplicateJSONBeforeApplication(t *testing.T) {
	ready := true
	accounts := &fakeAccountApplication{}
	router := newTestRouterWithAccounts(t, &ready, 10, accounts)
	for _, body := range []string{
		`{"username":"valid-user","password":"password-value","displayName":"User","actor":"attacker"}`,
		`{"username":"valid-user","username":"other-user","password":"password-value","displayName":"User"}`,
	} {
		response := performRequest(router.Handler(), http.MethodPost, "/v1/auth/register", body, map[string]string{"Content-Type": "application/json"})
		assertErrorResponse(t, response, http.StatusBadRequest, 200)
	}
	if accounts.calls != 0 {
		t.Fatalf("account application called %d times for invalid input", accounts.calls)
	}
}

// TestRouterRejectsSchemaInvalidAndOversizedBodyBeforeApplication 验证operation策略与账号值对象在调用owner前生效。
func TestRouterRejectsSchemaInvalidAndOversizedBodyBeforeApplication(t *testing.T) {
	ready := true
	accounts := &fakeAccountApplication{}
	router := newTestRouterWithAccounts(t, &ready, 10, accounts)
	tests := []string{
		`{"username":"-invalid","password":"password-value","displayName":"User"}`,
		`{"username":"valid-user","password":"short","displayName":"User"}`,
		`{"username":"valid-user","password":"password-\ud800x","displayName":"User"}`,
		string([]byte{'{', '"', 'u', 's', 'e', 'r', 'n', 'a', 'm', 'e', '"', ':', '"', 0xff, '"', '}'}),
		strings.Repeat("x", 4097),
	}
	for _, body := range tests {
		response := performRequest(router.Handler(), http.MethodPost, "/v1/auth/register", body, map[string]string{"Content-Type": "application/json"})
		assertErrorResponse(t, response, http.StatusBadRequest, 200)
	}
	if accounts.calls != 0 {
		t.Fatalf("account application called %d times for invalid input", accounts.calls)
	}
}

// TestUnicodeEscapeValidationAcceptsOnlyCompleteSurrogatePairs 验证严格JSON边界不破坏合法补充平面字符。
func TestUnicodeEscapeValidationAcceptsOnlyCompleteSurrogatePairs(t *testing.T) {
	if err := rejectInvalidUnicodeEscapes([]byte(`{"value":"\ud83d\ude00"}`)); err != nil {
		t.Fatalf("valid surrogate pair rejected: %v", err)
	}
	for _, raw := range [][]byte{[]byte(`{"value":"\ud83d"}`), []byte(`{"value":"\ude00"}`)} {
		if err := rejectInvalidUnicodeEscapes(raw); err == nil {
			t.Fatalf("isolated surrogate accepted: %s", raw)
		}
	}
}

// TestRouterAuthenticationAndBodylessBoundary 验证protected route要求Bearer且logout拒绝request body。
func TestRouterAuthenticationAndBodylessBoundary(t *testing.T) {
	ready := true
	router := newTestRouter(t, &ready, 10)
	response := performRequest(router.Handler(), http.MethodGet, "/v1/world/bootstrap", "", nil)
	assertErrorResponse(t, response, http.StatusUnauthorized, 100)
	response = performRequest(router.Handler(), http.MethodPost, "/v1/auth/logout", `{}`, map[string]string{"Authorization": "Bearer " + strings.Repeat("a", 32), "Content-Type": "application/json"})
	assertErrorResponse(t, response, http.StatusBadRequest, 200)
	response = performRequest(router.Handler(), http.MethodPost, "/v1/visits/vses_fixture/invites/vinv_fixture/accept", `{"expectedRevision":1}`, map[string]string{"Content-Type": "application/json", "Idempotency-Key": "invalid!key-value"})
	assertErrorResponse(t, response, http.StatusBadRequest, 200)
}

// TestRouterRateLimitIsBoundedByOperation 验证单operation/IP预算映射稳定RATE_LIMITED与Retry-After。
func TestRouterRateLimitIsBoundedByOperation(t *testing.T) {
	ready := true
	router := newTestRouter(t, &ready, 1)
	if first := performRequest(router.Handler(), http.MethodGet, "/v1/version", "", nil); first.Code != http.StatusOK {
		t.Fatalf("first request status=%d", first.Code)
	}
	second := performRequest(router.Handler(), http.MethodGet, "/v1/version", "", nil)
	assertErrorResponse(t, second, http.StatusTooManyRequests, 400)
	if second.Header().Get("Retry-After") == "" {
		t.Fatal("rate-limited response omitted Retry-After")
	}
}

// TestLimiterCapacityDoesNotGrowPastBound 验证新subject在容量满时fail closed且idle后可回收。
func TestLimiterCapacityDoesNotGrowPastBound(t *testing.T) {
	policy := map[string]config.RatePolicy{"operation": {Requests: 1, Window: time.Minute, Burst: 1}}
	limiter := newLimiter(policy, 1, time.Second)
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	if allowed, _ := limiter.allow("operation", "pre", "one", now); !allowed {
		t.Fatal("first subject was rejected")
	}
	if allowed, _ := limiter.allow("operation", "pre", "two", now); allowed {
		t.Fatal("capacity overflow subject was accepted")
	}
	if allowed, _ := limiter.allow("operation", "pre", "two", now.Add(time.Second)); !allowed {
		t.Fatal("idle bucket was not reclaimed")
	}
}

// TestMiddlewareRecoversPanicAndPropagatesDeadline 验证panic与operation timeout均返回稳定安全错误。
func TestMiddlewareRecoversPanicAndPropagatesDeadline(t *testing.T) {
	ready := true
	router := newTestRouter(t, &ready, 10)
	panicEngine := gin.New()
	panicEngine.Use(router.safetyMiddleware())
	panicEngine.GET("/panic", func(c *gin.Context) { c.Set("operation-id", "getVersion"); panic("fixture panic") })
	response := performRequest(panicEngine, http.MethodGet, "/panic", "", nil)
	assertErrorResponse(t, response, http.StatusInternalServerError, 501)

	timeoutEngine := gin.New()
	timeoutEngine.Use(router.safetyMiddleware())
	timeoutEngine.GET("/timeout", router.operationMiddleware(Operation{ID: "getVersion", Method: "GET", Path: "/timeout", Timeout: time.Millisecond, Idempotency: IdempotencySafe}), func(c *gin.Context) {
		<-c.Request.Context().Done()
		router.writeError(c, mapApplicationError(c.Request.Context().Err()))
	})
	response = performRequest(timeoutEngine, http.MethodGet, "/timeout", "", nil)
	assertErrorResponse(t, response, http.StatusServiceUnavailable, 500)
}

// fakeAccountApplication 记录invalid request是否越过transport validation。
type fakeAccountApplication struct {
	// calls 统计Register调用次数。
	calls int
}

// Register 记录调用并返回显式测试错误。
func (application *fakeAccountApplication) Register(context.Context, account.RegisterCommand) (account.AuthResult, error) {
	application.calls++
	return account.AuthResult{}, errors.New("unexpected register call")
}

// Login 返回固定测试错误。
func (*fakeAccountApplication) Login(context.Context, account.LoginCommand) (account.AuthResult, error) {
	return account.AuthResult{}, errors.New("unused login")
}

// fakeSessionApplication 只为认证矩阵返回稳定unauthenticated测试错误。
type fakeSessionApplication struct{}

// AuthenticateHTTPS 拒绝所有测试Bearer。
func (*fakeSessionApplication) AuthenticateHTTPS(context.Context, string) (session.AuthenticatedSession, error) {
	return session.AuthenticatedSession{}, errors.New("test authentication rejected")
}

// Refresh 未被当前router边界测试调用。
func (*fakeSessionApplication) Refresh(context.Context, string) (session.TokenPair, error) {
	return session.TokenPair{}, errors.New("unused refresh")
}

// Logout 未被当前router边界测试调用。
func (*fakeSessionApplication) Logout(context.Context, session.AuthContext) (session.Invalidation, error) {
	return session.Invalidation{}, errors.New("unused logout")
}

// IssueTicket 未被当前router边界测试调用。
func (*fakeSessionApplication) IssueTicket(context.Context, session.AuthContext, session.Channel) (session.ConnectionTicket, error) {
	return session.ConnectionTicket{}, errors.New("unused ticket")
}

// fakeWorldApplication 保证无认证测试不会进入world application。
type fakeWorldApplication struct{}

// fakeBattleApplication 保证无认证测试不会进入 battle application。
type fakeBattleApplication struct{}

// noopHTTPObserver 接受测试中的低基数测量。
type noopHTTPObserver struct{}

// ObservePublicHTTP 不保留测试请求字段。
func (noopHTTPObserver) ObservePublicHTTP(string, string, string, float64, int) {}

// BootstrapOwnWorld 在意外调用时失败。
func (*fakeWorldApplication) BootstrapOwnWorld(context.Context, session.AuthenticatedSession) (worldentry.BootstrapResult, error) {
	return worldentry.BootstrapResult{}, errors.New("unused bootstrap")
}

// AcceptVisitInvite 在意外调用时失败。
func (*fakeWorldApplication) AcceptVisitInvite(context.Context, session.AuthenticatedSession, visitsession.VisitSessionID, visitsession.InviteID, visitsession.Revision, string) (worldentry.ReservationResult, error) {
	return worldentry.ReservationResult{}, errors.New("unused accept")
}

// IssueWorldAdmission 在意外调用时失败。
func (*fakeWorldApplication) IssueWorldAdmission(context.Context, session.AuthenticatedSession, worldentry.AdmissionTarget, string) (worldentry.AdmissionResult, error) {
	return worldentry.AdmissionResult{}, errors.New("unused admission")
}

// Issue 在意外调用时以 target-not-ready fail closed。
func (*fakeBattleApplication) Issue(context.Context, session.AuthenticatedSession, battleentry.Target, string) (battleentry.Result, error) {
	return battleentry.Result{}, battleticket.NewAdmissionError(
		"test", battleticket.ErrorCodeTargetNotReady, nil,
	)
}

// newTestRouter 构造完整operation policy与受信endpoint manifest。
func newTestRouter(t *testing.T, ready *bool, burst int) *Router {
	t.Helper()
	return newTestRouterWithAccounts(t, ready, burst, &fakeAccountApplication{})
}

// newTestRouterWithAccounts 允许测试观察账号application调用。
func newTestRouterWithAccounts(t *testing.T, ready *bool, burst int, accounts AccountApplication) *Router {
	t.Helper()
	wss, _ := session.NewEndpoint(session.ChannelWSS, "control.example.invalid", 443)
	tcp, _ := session.NewEndpoint(session.ChannelTLSTCP, "game.example.invalid", 4433)
	rates := make(map[string]config.RatePolicy, len(operations))
	for _, operation := range operations {
		rates[operation.ID] = config.RatePolicy{Requests: burst, Window: time.Minute, Burst: burst}
	}
	router, err := NewRouter(accounts, &fakeSessionApplication{}, &fakeWorldApplication{}, &fakeBattleApplication{}, RouterConfig{ServerVersion: "0.1.0", ProtocolVersion: 1, MinimumClientVersion: "0.1.0", Endpoints: []session.Endpoint{wss, tcp}, HTTPBodyBytes: 4096, RealtimeFrameBytes: 65536, Ready: func() bool { return *ready }, Rates: rates, RateMaxEntries: 64, RateIdleTTL: time.Minute, Observer: noopHTTPObserver{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return router
}

// performRequest 使用固定remote IP与request ID避免测试依赖随机值。
func performRequest(handler http.Handler, method string, path string, body string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "192.0.2.1:12345"
	request.Header.Set("X-Request-ID", "fixture-request-id")
	for name, value := range headers {
		if strings.EqualFold(name, "Host") {
			request.Host = value
		} else {
			request.Header.Set(name, value)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// assertErrorResponse 验证状态、稳定code与correlation，不读取内部错误文本。
func assertErrorResponse(t *testing.T, response *httptest.ResponseRecorder, status int, code uint32) {
	t.Helper()
	var body errorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != status || body.Code != code || body.RequestID != "fixture-request-id" {
		t.Fatalf("error response status=%d body=%s err=%v", response.Code, response.Body.String(), err)
	}
}
