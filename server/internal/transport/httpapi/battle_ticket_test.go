package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/battleentry"
	"github.com/jinwiforz/ihomeland/server/internal/battleticket"
	"github.com/jinwiforz/ihomeland/server/internal/config"
	"github.com/jinwiforz/ihomeland/server/internal/session"
)

func TestBattleTicketHandlerUsesAuthenticatedApplicationAndTrustedEndpoint(t *testing.T) {
	fixture := newBattleRouterFixture(t)
	headers := map[string]string{
		"Authorization":   "Bearer " + strings.Repeat("a", 32),
		"Content-Type":    "application/json",
		"Idempotency-Key": "battle-handler-key-0001",
		"Host":            "attacker.example.invalid",
	}
	response := performRequest(fixture.router.Handler(), http.MethodPost, "/v1/battle/tickets",
		`{"kind":"OWN_WORLD"}`, headers)
	if response.Code != http.StatusCreated {
		t.Fatalf("battle ticket status=%d body=%s", response.Code, response.Body.String())
	}
	var projected battleTicketResponse
	if err := json.Unmarshal(response.Body.Bytes(), &projected); err != nil {
		t.Fatal(err)
	}
	if projected.Endpoint.Host != fixture.result.Endpoint.Host() ||
		projected.Endpoint.Host == "attacker.example.invalid" ||
		projected.Endpoint.Transport != "UDP" ||
		projected.WireSuite.KeyAgreement != "X25519" ||
		projected.WireSuite.KDF != "HKDF-SHA-256" ||
		projected.WireSuite.AEAD != "ChaCha20-Poly1305" {
		t.Fatalf("battle response escaped trusted projection: %+v", projected)
	}
	if fixture.battles.calls != 1 || fixture.battles.target.Kind != battleentry.TargetKindOwnWorld ||
		fixture.battles.idempotencyKey != "battle-handler-key-0001" ||
		!fixture.battles.receivedDeadline {
		t.Fatal("handler did not pass the authenticated bounded application call")
	}
}

func TestBattleTicketMiddlewareRejectsClosedBodyAndDrainingBeforeApplication(t *testing.T) {
	fixture := newBattleRouterFixture(t)
	headers := map[string]string{
		"Authorization":   "Bearer " + strings.Repeat("a", 32),
		"Content-Type":    "application/json",
		"Idempotency-Key": "battle-handler-key-0002",
	}
	response := performRequest(fixture.router.Handler(), http.MethodPost, "/v1/battle/tickets",
		`{"kind":"OWN_WORLD","host":"attacker.invalid"}`, headers)
	assertErrorResponse(t, response, http.StatusBadRequest, 200)
	if fixture.battles.calls != 0 {
		t.Fatal("closed decode failure reached battle application")
	}
	fixture.ready = false
	response = performRequest(fixture.router.Handler(), http.MethodPost, "/v1/battle/tickets",
		`{"kind":"OWN_WORLD"}`, headers)
	assertErrorResponse(t, response, http.StatusServiceUnavailable, 500)
	if fixture.battles.calls != 0 {
		t.Fatal("draining gate reached battle application")
	}
}

func TestBattleTicketMiddlewareEnforcesAuthBodyIdempotencyAndRate(t *testing.T) {
	fixture := newBattleRouterFixture(t)
	validHeaders := map[string]string{
		"Authorization":   "Bearer " + strings.Repeat("a", 32),
		"Content-Type":    "application/json",
		"Idempotency-Key": "battle-handler-key-0003",
	}
	response := performRequest(fixture.router.Handler(), http.MethodPost, "/v1/battle/tickets",
		`{"kind":"OWN_WORLD"}`, map[string]string{
			"Content-Type": "application/json", "Idempotency-Key": "battle-handler-key-0003",
		})
	assertErrorResponse(t, response, http.StatusUnauthorized, 100)
	response = performRequest(fixture.router.Handler(), http.MethodPost, "/v1/battle/tickets",
		`{"kind":"OWN_WORLD"}`, map[string]string{
			"Authorization": "Bearer " + strings.Repeat("a", 32),
			"Content-Type":  "application/json", "Idempotency-Key": "unsafe!key",
		})
	assertErrorResponse(t, response, http.StatusBadRequest, 200)
	response = performRequest(fixture.router.Handler(), http.MethodPost, "/v1/battle/tickets",
		strings.Repeat("x", 2049), validHeaders)
	assertErrorResponse(t, response, http.StatusBadRequest, 200)
	if fixture.battles.calls != 0 {
		t.Fatal("auth/body/idempotency rejection reached battle application")
	}

	rateFixture := newBattleRouterFixtureWithBurst(t, 1)
	first := performRequest(rateFixture.router.Handler(), http.MethodPost, "/v1/battle/tickets",
		`{"kind":"OWN_WORLD"}`, validHeaders)
	if first.Code != http.StatusCreated {
		t.Fatalf("first rate-budget request failed: %d", first.Code)
	}
	second := performRequest(rateFixture.router.Handler(), http.MethodPost, "/v1/battle/tickets",
		`{"kind":"OWN_WORLD"}`, validHeaders)
	assertErrorResponse(t, second, http.StatusTooManyRequests, 400)
	if second.Header().Get("Retry-After") == "" || rateFixture.battles.calls != 1 {
		t.Fatal("battle rate gate did not stop the second application call")
	}
}

func TestBattleTicketErrorsMapToFrozenCatalog(t *testing.T) {
	tests := []struct {
		code   battleticket.ErrorCode
		status int
		public uint32
	}{
		{battleticket.ErrorCodeTargetNotReady, http.StatusServiceUnavailable, 3000},
		{battleticket.ErrorCodeCapacityExceeded, http.StatusConflict, 3001},
		{battleticket.ErrorCodeTargetStale, http.StatusConflict, 3002},
		{battleticket.ErrorCodeIdempotencyConflict, http.StatusConflict, 3003},
		{battleticket.ErrorCodeCommitUnknown, http.StatusServiceUnavailable, 500},
	}
	for _, test := range tests {
		mapped := mapApplicationError(battleticket.NewAdmissionError("test", test.code, errors.New("sentinel")))
		if mapped.Status != test.status || mapped.Code != test.public {
			t.Fatalf("battle code %s mapped to status=%d code=%d", test.code, mapped.Status, mapped.Code)
		}
	}
}

type battleRouterFixture struct {
	ready   bool
	result  battleentry.Result
	battles *capturingBattleApplication
	router  *Router
}

func newBattleRouterFixture(t *testing.T) *battleRouterFixture {
	return newBattleRouterFixtureWithBurst(t, 20)
}

func newBattleRouterFixtureWithBurst(t *testing.T, burst int) *battleRouterFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	authenticated := newHTTPAuthenticatedSession(t, now)
	ticketID, _ := battleticket.ParseTicketID("btk1_" + base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("i", 16))))
	ticketSecret, _ := battleticket.ParseTicketSecret("bts1_" + base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("s", 32))))
	endpoint, _ := battleticket.NewEndpoint("trusted-battle.example.invalid", 58445)
	result := battleentry.Result{
		TicketID: ticketID, TicketSecret: ticketSecret, Endpoint: endpoint,
		WireSuite: battleticket.CurrentWireSuite(), Role: battleticket.RoleOwner,
		TargetKind: battleentry.TargetKindOwnWorld, TargetRevision: 9,
		ExpiresAt: now.Add(30 * time.Second),
	}
	battles := &capturingBattleApplication{result: result}
	ready := true
	wss, _ := session.NewEndpoint(session.ChannelWSS, "control.example.invalid", 443)
	tcp, _ := session.NewEndpoint(session.ChannelTLSTCP, "game.example.invalid", 4433)
	rates := make(map[string]config.RatePolicy, len(operations))
	for _, operation := range operations {
		rates[operation.ID] = config.RatePolicy{Requests: burst, Window: time.Minute, Burst: burst}
	}
	fixture := &battleRouterFixture{ready: ready, result: result, battles: battles}
	router, err := NewRouter(
		&fakeAccountApplication{}, &acceptedSessionApplication{authenticated: authenticated},
		&fakeWorldApplication{}, battles,
		RouterConfig{
			ServerVersion: "0.1.0", ProtocolVersion: 1, MinimumClientVersion: "0.1.0",
			Endpoints: []session.Endpoint{wss, tcp}, HTTPBodyBytes: 4096,
			RealtimeFrameBytes: 65536, Ready: func() bool { return fixture.ready },
			Rates: rates, RateMaxEntries: 64, RateIdleTTL: time.Minute,
			Observer: noopHTTPObserver{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.router = router
	return fixture
}

type capturingBattleApplication struct {
	calls            int
	target           battleentry.Target
	idempotencyKey   string
	receivedDeadline bool
	result           battleentry.Result
}

func (application *capturingBattleApplication) Issue(ctx context.Context, _ session.AuthenticatedSession, target battleentry.Target, idempotencyKey string) (battleentry.Result, error) {
	application.calls++
	application.target = target
	application.idempotencyKey = idempotencyKey
	deadline, ok := ctx.Deadline()
	application.receivedDeadline = ok && deadline.After(time.Now()) &&
		time.Until(deadline) <= 5*time.Second
	return application.result, nil
}

type acceptedSessionApplication struct {
	authenticated session.AuthenticatedSession
}

func (application *acceptedSessionApplication) AuthenticateHTTPS(context.Context, string) (session.AuthenticatedSession, error) {
	return application.authenticated, nil
}

func (*acceptedSessionApplication) Refresh(context.Context, string) (session.TokenPair, error) {
	return session.TokenPair{}, errors.New("unused refresh")
}

func (*acceptedSessionApplication) Logout(context.Context, session.AuthContext) (session.Invalidation, error) {
	return session.Invalidation{}, errors.New("unused logout")
}

func (*acceptedSessionApplication) IssueTicket(context.Context, session.AuthContext, session.Channel) (session.ConnectionTicket, error) {
	return session.ConnectionTicket{}, errors.New("unused ticket")
}

type httpAuthStore struct {
	mutex  sync.Mutex
	bundle session.SessionBundle
}

func (store *httpAuthStore) Create(_ context.Context, bundle session.SessionBundle) (session.StoreOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.bundle = bundle
	return session.StoreOutcomeApplied, nil
}

func (store *httpAuthStore) ResolveAccess(_ context.Context, digest session.Digest, _ time.Time) (session.AuthSnapshot, session.StoreOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if digest != store.bundle.Access.Digest {
		return session.AuthSnapshot{}, session.StoreOutcomeNotFound, nil
	}
	return session.AuthSnapshot{
		Principal: store.bundle.Session.Principal, SessionID: store.bundle.Session.ID,
		Epoch: store.bundle.Session.Epoch, AccessExpiresAt: store.bundle.Access.ExpiresAt,
		SessionExpiresAt: store.bundle.Session.ExpiresAt,
	}, session.StoreOutcomeApplied, nil
}

func (*httpAuthStore) RotateRefresh(context.Context, session.Rotation) (session.AuthSnapshot, session.Invalidation, session.StoreOutcome, error) {
	return session.AuthSnapshot{}, session.Invalidation{}, session.StoreOutcomeNotFound, nil
}

func (*httpAuthStore) IssueTicket(context.Context, session.TicketRecord, time.Time) (session.StoreOutcome, error) {
	return session.StoreOutcomeNotFound, nil
}

func (*httpAuthStore) ConsumeTicket(context.Context, session.Digest, session.Channel, session.Endpoint, time.Time) (session.AuthSnapshot, session.StoreOutcome, error) {
	return session.AuthSnapshot{}, session.StoreOutcomeNotFound, nil
}

func (*httpAuthStore) InvalidateSession(context.Context, session.SessionID, session.InvalidationReason) (session.Invalidation, session.StoreOutcome, error) {
	return session.Invalidation{}, session.StoreOutcomeNotFound, nil
}

func (*httpAuthStore) InvalidatePrincipal(context.Context, session.Principal, session.InvalidationReason) ([]session.Invalidation, error) {
	return nil, nil
}

type httpAuthIDs struct{}

func (httpAuthIDs) NewID() (string, error) { return "battleHTTPFixture", nil }

type httpAuthClock struct{ now time.Time }

func (clock httpAuthClock) Now() time.Time { return clock.now }

func newHTTPAuthenticatedSession(t *testing.T, now time.Time) session.AuthenticatedSession {
	t.Helper()
	principal, _ := session.NewPrincipal("acc_battleHTTP", "ply_battleHTTP")
	store := &httpAuthStore{}
	wss, _ := session.NewEndpoint(session.ChannelWSS, "control.example.invalid", 443)
	tcp, _ := session.NewEndpoint(session.ChannelTLSTCP, "game.example.invalid", 4433)
	endpoints, _ := session.NewStaticEndpointProvider(wss, tcp)
	policy, _ := session.NewPolicy(time.Second, time.Minute, time.Hour, time.Hour)
	service, err := session.NewService(store, endpoints, session.NoActiveRealtimeConnections{},
		httpAuthClock{now: now}, httpAuthIDs{}, session.CryptoSecretGenerator{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateSession(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	authenticated, err := service.AuthenticateHTTPS(context.Background(), created.Tokens.Access.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	return authenticated
}
