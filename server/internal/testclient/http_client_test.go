package testclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestHTTPClientFrozenOperations 验证 11 个冻结 operation 的 method、path、认证、幂等键与 closed response。
func TestHTTPClientFrozenOperations(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /v1/version":
			_, _ = io.WriteString(writer, `{"protocolVersion":1,"minimumClientVersion":"0.1.0","serverVersion":"0.1.0"}`)
		case "GET /v1/config":
			_, _ = io.WriteString(writer, `{"endpoints":[{"channel":"WSS","host":"127.0.0.1","port":1},{"channel":"TLS_TCP","host":"127.0.0.1","port":2}],"limits":{"httpBodyBytes":65536,"realtimeFrameBytes":65536}}`)
		case "POST /v1/auth/register":
			writer.WriteHeader(http.StatusCreated)
			writeAuthFixture(writer)
		case "POST /v1/auth/login":
			writeAuthFixture(writer)
		case "POST /v1/auth/refresh":
			_, _ = io.WriteString(writer, tokenFixture())
		case "POST /v1/auth/logout":
			requireBearer(t, request)
			writer.WriteHeader(http.StatusNoContent)
		case "POST /v1/session/tickets":
			requireBearer(t, request)
			writer.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(writer, `{"ticket":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","endpoint":{"channel":"WSS","host":"127.0.0.1","port":1},"scopes":["CONTROL"],"expiresAtMs":2}`)
		case "GET /v1/world/bootstrap":
			requireBearer(t, request)
			_, _ = io.WriteString(writer, `{"world":{"personalWorldId":"pw","ownerPlayerId":"player","lifecycle":"ACTIVE","revision":1,"createdAtMs":1},"assignment":{"personalWorldId":"pw","worldInstanceId":"wi","endpoint":{"channel":"TLS_TCP","host":"127.0.0.1","port":2},"generation":1,"leaseExpiresAtMs":2}}`)
		case "POST /v1/visits/visit/invites/invite/accept":
			requireBearerAndIdempotency(t, request)
			_, _ = io.WriteString(writer, `{"reservation":{"visitSessionId":"visit","revision":2,"reservationExpiresAtMs":3}}`)
		case "POST /v1/world/admissions":
			requireBearerAndIdempotency(t, request)
			writer.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(writer, `{"credential":"wad1_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","endpoint":{"channel":"TLS_TCP","host":"127.0.0.1","port":2},"role":"OWNER","purpose":"OWN_WORLD","expiresAtMs":3}`)
		case "POST /v1/battle/tickets":
			requireBearerAndIdempotency(t, request)
			writer.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(writer, battleTicketFixture())
		default:
			http.Error(writer, "unexpected operation", http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := NewHTTPClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new HTTP client: %v", err)
	}
	ctx := context.Background()
	access, _ := NewSecret("access")
	refresh, _ := NewSecret("refresh")
	if _, err := client.Version(ctx); err != nil {
		t.Fatalf("version: %v", err)
	}
	if _, err := client.BootstrapConfig(ctx); err != nil {
		t.Fatalf("config: %v", err)
	}
	if _, err := client.Register(ctx, RegisterRequest{Username: "user", Password: "password-long", DisplayName: "User"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := client.Login(ctx, LoginRequest{Username: "user", Password: "password-long"}); err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := client.Refresh(ctx, refresh); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if err := client.Logout(ctx, access); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := client.IssueTicket(ctx, access, TicketRequest{Channel: "WSS"}); err != nil {
		t.Fatalf("ticket: %v", err)
	}
	if _, err := client.WorldBootstrap(ctx, access); err != nil {
		t.Fatalf("world bootstrap: %v", err)
	}
	if _, err := client.AcceptVisitInvite(ctx, access, "visit", "invite", "idempotency_1234", AcceptVisitInviteRequest{ExpectedRevision: 1}); err != nil {
		t.Fatalf("accept invite: %v", err)
	}
	if _, err := client.IssueWorldAdmission(ctx, access, "idempotency_5678", WorldAdmissionRequest{Kind: "OWN_WORLD"}); err != nil {
		t.Fatalf("world admission: %v", err)
	}
	battleTicket, err := client.IssueBattleTicket(ctx, access, "battle_ticket_1234", BattleTicketRequest{Kind: "OWN_WORLD"})
	if err != nil {
		t.Fatalf("battle ticket: %v", err)
	}
	if battleTicket.Role != "OWNER" || battleTicket.Endpoint.Transport != "UDP" {
		t.Fatal("battle ticket projection drifted")
	}
	if err := battleTicket.Close(); err != nil {
		t.Fatalf("close battle ticket: %v", err)
	}
}

// TestBattleTicketResponseLossReplayAndSecretOwnership 验证同 key 精确重放且 secret 只能转移一次。
func TestBattleTicketResponseLossReplayAndSecretOwnership(t *testing.T) {
	requestCount := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestCount++
		requireBearerAndIdempotency(t, request)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(writer, battleTicketFixture())
	}))
	defer server.Close()
	client, err := NewHTTPClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new HTTP client: %v", err)
	}
	access, _ := NewSecret("access")
	ticket, err := client.IssueBattleTicketAfterResponseLoss(
		context.Background(), access, "battle_replay_1234",
		BattleTicketRequest{Kind: "OWN_WORLD"},
	)
	if err != nil {
		t.Fatalf("response-loss replay: %v", err)
	}
	if requestCount != 2 {
		t.Fatalf("request count=%d want=2", requestCount)
	}
	secret, err := ticket.TakeSecret()
	if err != nil {
		t.Fatalf("take secret: %v", err)
	}
	if _, err := ticket.TakeSecret(); err == nil {
		t.Fatal("battle ticket secret was transferred twice")
	}
	secret.Clear()
}

// TestBattleTicketCredentialOwnership 验证 public identity 与 bearer 只解码并转移一次。
func TestBattleTicketCredentialOwnership(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requireBearerAndIdempotency(t, request)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(writer, battleTicketFixture())
	}))
	defer server.Close()
	client, err := NewHTTPClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new HTTP client: %v", err)
	}
	access, _ := NewSecret("access")
	ticket, err := client.IssueBattleTicket(
		context.Background(),
		access,
		"battle_credential_1234",
		BattleTicketRequest{Kind: "OWN_WORLD"},
	)
	if err != nil {
		t.Fatalf("issue BattleTicket: %v", err)
	}
	credential, err := ticket.TakeCredential()
	if err != nil {
		t.Fatalf("take credential: %v", err)
	}
	if credential.TicketID != ([battleTicketIDBytes]byte{}) {
		t.Fatal("BattleTicket identity bytes drifted")
	}
	if credential.TicketSecret != [battleTicketSecretBytes]byte{
		1, 1, 1, 1, 1, 1, 1, 1,
		1, 1, 1, 1, 1, 1, 1, 1,
		1, 1, 1, 1, 1, 1, 1, 1,
		1, 1, 1, 1, 1, 1, 1, 1,
	} {
		t.Fatal("BattleTicket secret bytes drifted")
	}
	if _, err := ticket.TakeCredential(); err == nil {
		t.Fatal("BattleTicket credential was transferred twice")
	}
	credential.Clear()
	if credential != (BattleCredential{}) {
		t.Fatal("BattleTicket credential was not cleared")
	}
}

// TestBattleTicketRejectsDriftedReplay 验证同 key 返回不同 credential 时 fail closed。
func TestBattleTicketRejectsDriftedReplay(t *testing.T) {
	requestCount := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestCount++
		requireBearerAndIdempotency(t, request)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		fixture := battleTicketFixture()
		if requestCount == 2 {
			fixture = strings.Replace(fixture, "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE", "AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAg", 1)
		}
		_, _ = io.WriteString(writer, fixture)
	}))
	defer server.Close()
	client, err := NewHTTPClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new HTTP client: %v", err)
	}
	access, _ := NewSecret("access")
	if ticket, err := client.IssueBattleTicketAfterResponseLoss(
		context.Background(), access, "battle_replay_5678",
		BattleTicketRequest{Kind: "OWN_WORLD"},
	); err == nil || ticket != nil {
		t.Fatal("drifted BattleTicket replay was accepted")
	}
}

// TestHTTPClientRejectsUnknownOversizeAndMalformedErrors 验证 closed JSON、body 上限和低敏公开错误。
func TestHTTPClientRejectsUnknownOversizeAndMalformedErrors(t *testing.T) {
	tests := map[string]http.HandlerFunc{
		"unknown": func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(writer, `{"protocolVersion":1,"minimumClientVersion":"0.1.0","serverVersion":"0.1.0","internal":"leak"}`)
		},
		"oversize": func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(writer, `{"protocolVersion":1,"minimumClientVersion":"`+strings.Repeat("a", 1024)+`","serverVersion":"0.1.0"}`)
		},
		"malformed error": func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(writer, `backend exploded with password`)
		},
	}
	for name, handler := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewTLSServer(handler)
			defer server.Close()
			client, err := NewHTTPClient(server.URL, server.Client())
			if err != nil {
				t.Fatalf("new HTTP client: %v", err)
			}
			if name == "oversize" {
				client.responseLimit = 128
			}
			_, err = client.Version(context.Background())
			if err == nil {
				t.Fatal("invalid response was accepted")
			}
			if strings.Contains(err.Error(), "password") {
				t.Fatalf("backend body leaked through error: %v", err)
			}
		})
	}
}

// TestHTTPClientPublicErrorAndDeadline 验证结构化公开错误可匹配且 request deadline 生效。
func TestHTTPClientPublicErrorAndDeadline(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("slow") == "true" {
			time.Sleep(100 * time.Millisecond)
		}
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(writer, `{"code":1001,"messageKey":"auth.invalid","requestId":"request-1","retryable":false}`)
	}))
	defer server.Close()
	client, err := NewHTTPClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new HTTP client: %v", err)
	}
	_, err = client.Version(context.Background())
	var publicError PublicError
	if !errors.As(err, &publicError) || publicError.Code != 1001 {
		t.Fatalf("public error=%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	client.baseURL.RawQuery = "slow=true"
	_, err = client.Version(ctx)
	if err == nil {
		t.Fatal("expired deadline was ignored")
	}
}

// writeAuthFixture 写入 register/login 共用的封闭成功响应。
func writeAuthFixture(writer io.Writer) {
	_, _ = io.WriteString(writer, `{"account":{"accountId":"account","displayName":"User","createdAtMs":1},"session":{"sessionId":"session","sessionEpoch":1,"expiresAtMs":2},"tokens":`+tokenFixture()+`,"endpoints":[{"channel":"WSS","host":"127.0.0.1","port":1},{"channel":"TLS_TCP","host":"127.0.0.1","port":2}]}`)
}

// tokenFixture 返回只在本地 unit server 使用的 token JSON。
func tokenFixture() string {
	return `{"accessToken":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","refreshToken":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","accessExpiresAtMs":2,"refreshExpiresAtMs":3}`
}

// battleTicketFixture 返回 production fixed-width identity 与 credential 的 closed response。
func battleTicketFixture() string {
	return `{"ticketId":"btk1_AAAAAAAAAAAAAAAAAAAAAA","ticketSecret":"bts1_AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE","endpoint":{"transport":"UDP","host":"127.0.0.1","port":30000},"wireSuite":{"wireVersion":1,"keyAgreement":"X25519","kdf":"HKDF-SHA-256","aead":"ChaCha20-Poly1305"},"role":"OWNER","targetKind":"OWN_WORLD","targetRevision":1,"expiresAtMs":2}`
}

// requireBearer 验证冻结 operation 使用 bearer header。
func requireBearer(t *testing.T, request *http.Request) {
	t.Helper()
	if request.Header.Get("Authorization") != "Bearer access" {
		t.Errorf("authorization=%q", request.Header.Get("Authorization"))
	}
}

// requireBearerAndIdempotency 验证 mutation 同时携带身份与幂等 key。
func requireBearerAndIdempotency(t *testing.T, request *http.Request) {
	t.Helper()
	requireBearer(t, request)
	if request.Header.Get("Idempotency-Key") == "" {
		t.Error("idempotency key is missing")
	}
}
