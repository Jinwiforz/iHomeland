package wscontrol

import (
	"bytes"
	"context"
	"crypto/tls"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jinwiforz/ihomeland/server/internal/session"
)

// testTicketConsumer 记录handler传给Session owner的受信绑定，不保存raw header。
type testTicketConsumer struct {
	mu       sync.Mutex
	calls    int
	channel  session.Channel
	endpoint session.Endpoint
	err      error
	panic    bool
}

// ConsumeTicket 记录受信binding并返回测试配置结果。
func (consumer *testTicketConsumer) ConsumeTicket(_ context.Context, _ session.TicketNonce, channel session.Channel, endpoint session.Endpoint) (session.AuthContext, error) {
	if consumer.panic {
		panic("sensitive ticket consumer panic")
	}
	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	consumer.calls++
	consumer.channel = channel
	consumer.endpoint = endpoint
	return session.AuthContext{}, consumer.err
}

// TestHandlerRecoversDependencyPanicWithoutLeakingText 验证upgrade前panic收敛为安全503与固定日志。
func TestHandlerRecoversDependencyPanicWithoutLeakingText(t *testing.T) {
	config := testConfig()
	var logs bytes.Buffer
	config.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	registry := newTestRegistry(t, config)
	endpoint, err := session.NewEndpoint(session.ChannelWSS, "127.0.0.1", 8080)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(config, func() bool { return true }, &testTicketConsumer{panic: true}, endpoint, registry, testObserver{})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, validHandshakeRequest())
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "sensitive") || strings.Contains(logs.String(), "sensitive") {
		t.Fatalf("panic boundary drifted: status=%d body=%q logs=%q", response.Code, response.Body.String(), logs.String())
	}
}

// TestHandlerRejectsUnsafeHandshakeInputs 验证upgrade前安全门和稳定ErrorResponse。
func TestHandlerRejectsUnsafeHandshakeInputs(t *testing.T) {
	config := testConfig()
	config.AllowPlaintext = false
	config.Policy.AllowedOrigins = []string{"https://client.example"}
	registry := newTestRegistry(t, config)
	endpoint, err := session.NewEndpoint(session.ChannelWSS, "127.0.0.1", 8080)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(config, func() bool { return true }, &testTicketConsumer{}, endpoint, registry, testObserver{})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*http.Request)
		status int
	}{
		{"method", func(request *http.Request) { request.Method = http.MethodPost }, http.StatusBadRequest},
		{"query", func(request *http.Request) { request.URL.RawQuery = "ticket=secret" }, http.StatusBadRequest},
		{"encoded path", func(request *http.Request) { request.URL.RawPath = "/v1/%63ontrol" }, http.StatusBadRequest},
		{"cookie", func(request *http.Request) { request.Header.Set("Cookie", "ticket=secret") }, http.StatusBadRequest},
		{"plaintext", func(request *http.Request) { request.TLS = nil }, http.StatusBadRequest},
		{"TLS version", func(request *http.Request) { request.TLS.Version = tls.VersionTLS12 }, http.StatusBadRequest},
		{"host", func(request *http.Request) { request.Host = "evil.example" }, http.StatusForbidden},
		{"origin", func(request *http.Request) { request.Header.Set("Origin", "https://evil.example") }, http.StatusForbidden},
		{"duplicate origin", func(request *http.Request) {
			request.Header.Add("Origin", "https://client.example")
			request.Header.Add("Origin", "https://client.example")
		}, http.StatusForbidden},
		{"upgrade", func(request *http.Request) { request.Header.Del("Upgrade") }, http.StatusBadRequest},
		{"subprotocol", func(request *http.Request) { request.Header.Set("Sec-WebSocket-Protocol", "other") }, http.StatusBadRequest},
		{"ticket", func(request *http.Request) { request.Header.Set("Authorization", "Ticket ABCD") }, http.StatusUnauthorized},
		{"duplicate ticket", func(request *http.Request) {
			request.Header.Add("Authorization", "Ticket 0123456789abcdef0123456789abcdef")
		}, http.StatusUnauthorized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validHandshakeRequest()
			test.mutate(request)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("got %d want %d", response.Code, test.status)
			}
			if strings.Contains(response.Body.String(), "0123456789abcdef0123456789abcdef") || !strings.Contains(response.Body.String(), "messageKey") {
				t.Fatal("safe error response leaked credential or lost ErrorResponse")
			}
		})
	}
}

// TestHandlerLogsExcludeHandshakeSecrets 验证结构化日志不读取header、remote、Origin或payload。
func TestHandlerLogsExcludeHandshakeSecrets(t *testing.T) {
	config := testConfig()
	var logs bytes.Buffer
	config.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	registry := newTestRegistry(t, config)
	endpoint, err := session.NewEndpoint(session.ChannelWSS, "127.0.0.1", 8080)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(config, func() bool { return true }, &testTicketConsumer{}, endpoint, registry, testObserver{})
	if err != nil {
		t.Fatal(err)
	}
	request := validHandshakeRequest()
	request.Host = "secret-host.example"
	request.RemoteAddr = "203.0.113.77:4444"
	request.Header.Set("Origin", "https://secret-origin.example")
	request.Header.Set("Authorization", "Ticket 0123456789abcdef0123456789abcdef")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	output := logs.String()
	for _, forbidden := range []string{"secret-host", "203.0.113.77", "secret-origin", "0123456789abcdef"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("handshake log leaked %q", forbidden)
		}
	}
}

// TestHandlerConsumesTrustedWSSBindingBeforeUpgrade 验证通过安全门后固定消费WSS endpoint，再校验AuthContext预算。
func TestHandlerConsumesTrustedWSSBindingBeforeUpgrade(t *testing.T) {
	config := testConfig()
	registry := newTestRegistry(t, config)
	endpoint, err := session.NewEndpoint(session.ChannelWSS, "control.example", 443)
	if err != nil {
		t.Fatal(err)
	}
	consumer := &testTicketConsumer{}
	handler, err := NewHandler(config, func() bool { return true }, consumer, endpoint, registry, testObserver{})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, validHandshakeRequest())
	// testTicketConsumer不能伪造Session owner的AuthContext，因此身份预算校验应在upgrade前安全拒绝。
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("invalid AuthContext reached upgrade: status=%d", response.Code)
	}
	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	if consumer.calls != 1 || consumer.channel != session.ChannelWSS || !consumer.endpoint.Equal(endpoint) {
		t.Fatal("handler did not consume ticket with trusted WSS binding")
	}
}

// TestMuxKeepsHTTPAndWSSPathsIsolated 验证顶层mux不扩张既有HTTP operation table。
func TestMuxKeepsHTTPAndWSSPathsIsolated(t *testing.T) {
	httpCalls, websocketCalls := 0, 0
	mux, err := Mux(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { httpCalls++ }), http.HandlerFunc(func(http.ResponseWriter, *http.Request) { websocketCalls++ }))
	if err != nil {
		t.Fatal(err)
	}
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://example.test/v1/version", nil))
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "http://example.test"+Path, nil))
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://example.test"+Path, nil))
	if httpCalls != 2 || websocketCalls != 1 {
		t.Fatalf("mux routing drifted: http=%d websocket=%d", httpCalls, websocketCalls)
	}
}

// FuzzTicketCredential 严格保持32位小写hex header语法。
func FuzzTicketCredential(f *testing.F) {
	for _, seed := range []string{"", "Ticket 0123456789abcdef0123456789abcdef", "Ticket ABCDEF0123456789abcdef0123456789", "Bearer 0123456789abcdef0123456789abcdef"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		raw, ok := ticketCredential(value)
		if !ok {
			return
		}
		if value != "Ticket "+raw || len(raw) != 32 || strings.ToLower(raw) != raw {
			t.Fatal("accepted non-canonical ticket credential")
		}
	})
}

// validHandshakeRequest 构造除Authorization外完整的TLS握手请求。
func validHandshakeRequest() *http.Request {
	request := httptest.NewRequest(http.MethodGet, "https://127.0.0.1:8080"+Path, nil)
	request.TLS.Version = tls.VersionTLS13
	request.Host = "127.0.0.1:8080"
	request.RemoteAddr = "127.0.0.1:10000"
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Protocol", Subprotocol)
	request.Header.Set("Authorization", "Ticket 0123456789abcdef0123456789abcdef")
	return request
}
