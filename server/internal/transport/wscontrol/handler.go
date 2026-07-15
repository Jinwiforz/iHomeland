package wscontrol

import (
	"crypto/tls"
	"encoding/hex"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/transport/httpapi"
)

// Handler 完成upgrade前安全门、原子ticket消费与registry注册。
type Handler struct {
	// config 是冻结握手与连接策略。
	config Config
	// ready 在draining时立即拒绝新upgrade。
	ready func() bool
	// tickets 是现有production Session service。
	tickets TicketConsumer
	// endpoint 是ticket签发时使用的受信advertised WSS endpoint。
	endpoint session.Endpoint
	// registry 拥有upgrade后的连接。
	registry *Registry
	// observer 只记录稳定握手结果。
	observer Observer
}

// NewHandler 构造无listener的唯一control upgrade handler。
func NewHandler(config Config, ready func() bool, tickets TicketConsumer, endpoint session.Endpoint, registry *Registry, observer Observer) (*Handler, error) {
	if err := config.validate(); err != nil || ready == nil || tickets == nil || !endpoint.Valid() || endpoint.Channel() != session.ChannelWSS || registry == nil || observer == nil {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("websocket control handler dependencies are incomplete")
	}
	return &Handler{config: config, ready: ready, tickets: tickets, endpoint: endpoint, registry: registry, observer: observer}, nil
}

// ServeHTTP 只接受冻结GET握手；成功upgrade后连接生命周期完全转移给registry。
func (handler *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	fail := func(kind httpapi.SafeErrorKind, outcome string) {
		handler.observer.ObserveWSSHandshake(outcome)
		handler.config.Logger.Info("websocket control handshake rejected", "operation", "handshake", "outcome", outcome)
		httpapi.WriteSafeError(response, request, kind)
	}
	var acceptedConnection *websocket.Conn
	defer func() {
		if recover() == nil {
			return
		}
		if acceptedConnection == nil {
			fail(httpapi.SafeErrorDependencyUnavailable, "panic")
			return
		}
		handler.observer.ObserveWSSHandshake("panic_after_upgrade")
		handler.config.Logger.Error("websocket control handshake failed", "operation", "handshake", "outcome", "panic_after_upgrade")
		// 已upgrade连接不能再写HTTP ErrorResponse；关闭在独立goroutine中执行，避免第三方close等待peer阻塞handler。
		handler.closeUnregistered(acceptedConnection, websocket.StatusInternalError, "connection failed")
	}()
	if request.Method != http.MethodGet || request.URL.Path != Path || request.URL.EscapedPath() != Path || request.URL.RawQuery != "" || request.Header.Get("Cookie") != "" {
		fail(httpapi.SafeErrorValidation, "invalid_request")
		return
	}
	if !handler.ready() {
		fail(httpapi.SafeErrorDependencyUnavailable, "draining")
		return
	}
	if request.TLS == nil && !handler.config.AllowPlaintext {
		fail(httpapi.SafeErrorValidation, "tls_required")
		return
	}
	if request.TLS != nil && request.TLS.Version != tls.VersionTLS13 {
		fail(httpapi.SafeErrorValidation, "tls_version_rejected")
		return
	}
	if !contains(handler.config.Policy.AllowedHosts, strings.ToLower(request.Host)) {
		fail(httpapi.SafeErrorForbidden, "host_rejected")
		return
	}
	originValues := request.Header.Values("Origin")
	if len(originValues) > 1 {
		fail(httpapi.SafeErrorForbidden, "origin_rejected")
		return
	}
	origin := request.Header.Get("Origin")
	if origin != "" && !contains(handler.config.Policy.AllowedOrigins, strings.ToLower(origin)) {
		fail(httpapi.SafeErrorForbidden, "origin_rejected")
		return
	}
	if !headerHasToken(strings.Join(request.Header.Values("Connection"), ","), "upgrade") || !strings.EqualFold(request.Header.Get("Upgrade"), "websocket") || !headerHasExactToken(strings.Join(request.Header.Values("Sec-WebSocket-Protocol"), ","), Subprotocol) {
		fail(httpapi.SafeErrorValidation, "upgrade_rejected")
		return
	}
	if len(request.Header.Values("Authorization")) != 1 {
		fail(httpapi.SafeErrorUnauthenticated, "ticket_rejected")
		return
	}
	rawTicket, ok := ticketCredential(request.Header.Get("Authorization"))
	if !ok {
		fail(httpapi.SafeErrorUnauthenticated, "ticket_rejected")
		return
	}
	remoteKey, ok := normalizedRemote(request.RemoteAddr)
	if !ok {
		fail(httpapi.SafeErrorValidation, "remote_rejected")
		return
	}
	reserved, err := handler.registry.reserve(remoteKey)
	if err != nil {
		if errors.Is(err, ErrRateLimited) {
			fail(httpapi.SafeErrorRateLimited, "rate_limited")
		} else {
			fail(httpapi.SafeErrorDependencyUnavailable, "capacity_rejected")
		}
		return
	}
	defer reserved.Release()
	decoded := make([]byte, hex.DecodedLen(len(rawTicket)))
	if _, err := hex.Decode(decoded, []byte(rawTicket)); err != nil {
		fail(httpapi.SafeErrorUnauthenticated, "ticket_rejected")
		return
	}
	nonce, err := session.ParseTicketNonce(decoded)
	for index := range decoded {
		decoded[index] = 0
	}
	if err != nil {
		fail(httpapi.SafeErrorUnauthenticated, "ticket_rejected")
		return
	}
	auth, err := handler.tickets.ConsumeTicket(request.Context(), nonce, session.ChannelWSS, handler.endpoint)
	if err != nil {
		kind := httpapi.SafeErrorUnauthenticated
		outcome := "ticket_rejected"
		if session.ErrorKindOf(err) == session.ErrorKindForbidden {
			kind, outcome = httpapi.SafeErrorForbidden, "scope_rejected"
		} else if session.ErrorKindOf(err) == session.ErrorKindDependencyUnavailable {
			kind, outcome = httpapi.SafeErrorDependencyUnavailable, "dependency_failed"
		}
		fail(kind, outcome)
		return
	}
	if err := handler.registry.bindAuth(reserved, auth); err != nil {
		fail(httpapi.SafeErrorDependencyUnavailable, "capacity_rejected")
		return
	}
	connection, err := websocket.Accept(response, request, &websocket.AcceptOptions{
		Subprotocols:       []string{Subprotocol},
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		handler.observer.ObserveWSSHandshake("upgrade_failed_after_consume")
		handler.config.Logger.Info("websocket control handshake ended", "operation", "handshake", "outcome", "upgrade_failed_after_consume")
		return
	}
	acceptedConnection = connection
	if connection.Subprotocol() != Subprotocol {
		handler.closeUnregistered(connection, websocket.StatusPolicyViolation, "subprotocol required")
		handler.observer.ObserveWSSHandshake("subprotocol_rejected")
		handler.config.Logger.Info("websocket control handshake ended", "operation", "handshake", "outcome", "subprotocol_rejected")
		return
	}
	connectionID, err := handler.registry.registerAuth(reserved, auth, connection)
	if err != nil {
		code, reason := websocket.StatusTryAgainLater, "connection unavailable"
		if errors.Is(err, ErrRegistryStopped) {
			code, reason = websocket.StatusGoingAway, "server draining"
		}
		handler.closeUnregistered(connection, code, reason)
		handler.observer.ObserveWSSHandshake("registration_failed")
		handler.config.Logger.Info("websocket control handshake ended", "operation", "handshake", "outcome", "registration_failed")
		return
	}
	handler.observer.ObserveWSSHandshake("accepted")
	handler.config.Logger.Info("websocket control handshake accepted", "operation", "handshake", "connection_id", connectionID, "outcome", "accepted")
}

// Mux 只分流精确GET control path，其余请求原样委托既有HTTP router。
func Mux(httpHandler http.Handler, websocketHandler http.Handler) (http.Handler, error) {
	if httpHandler == nil || websocketHandler == nil {
		return nil, errors.New("public mux handlers are required")
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && request.URL.Path == Path {
			websocketHandler.ServeHTTP(response, request)
			return
		}
		httpHandler.ServeHTTP(response, request)
	}), nil
}

// closeUnregistered 有界关闭尚未转移给registry的socket，使HTTP shutdown等待close frame写出。
func (handler *Handler) closeUnregistered(connection *websocket.Conn, code websocket.StatusCode, reason string) {
	closeDone := make(chan struct{})
	go func() {
		_ = connection.Close(code, reason)
		close(closeDone)
	}()
	timer := time.NewTimer(handler.config.Policy.CloseTimeout)
	defer timer.Stop()
	select {
	case <-closeDone:
	case <-timer.C:
		// CloseNow可能与Close共享内部owner，不能把有界handler再次阻塞在强制关闭上。
		go func() { _ = connection.CloseNow() }()
	}
}

// ticketCredential 只接受固定scheme和32位小写hex，不复制到日志或错误。
func ticketCredential(value string) (string, bool) {
	if len(value) != len("Ticket ")+32 || !strings.HasPrefix(value, "Ticket ") {
		return "", false
	}
	raw := strings.TrimPrefix(value, "Ticket ")
	for _, character := range []byte(raw) {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return "", false
		}
	}
	return raw, true
}

// normalizedRemote 将socket address规范成不含port的IP预算key。
func normalizedRemote(value string) (string, bool) {
	host := value
	if address, err := netip.ParseAddrPort(value); err == nil {
		host = address.Addr().String()
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return "", false
	}
	return address.Unmap().String(), true
}

// headerHasToken 按HTTP token列表执行不区分大小写匹配。
func headerHasToken(value string, expected string) bool {
	for _, token := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(token), expected) {
			return true
		}
	}
	return false
}

// headerHasExactToken 要求subprotocol列表包含大小写完全一致的冻结值。
func headerHasExactToken(value string, expected string) bool {
	for _, token := range strings.Split(value, ",") {
		if strings.TrimSpace(token) == expected {
			return true
		}
	}
	return false
}

// contains 在启动时规范化的短allowlist中执行精确匹配。
func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
