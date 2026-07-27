package testclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// defaultHTTPBodyLimit 是公开配置返回前客户端采用的保守响应上限，单位为字节。
	defaultHTTPBodyLimit = 1 << 20
	// defaultHTTPTimeout 是调用方未提供更短 deadline 时的兜底请求预算。
	defaultHTTPTimeout = 15 * time.Second
	// battleTicketIDBytes 是 ClientHello opaque lookup identity 的固定宽度。
	battleTicketIDBytes = 16
	// battleTicketSecretBytes 是 BattleTicket bearer 的固定 256-bit 宽度。
	battleTicketSecretBytes = 32
	// battleWireVersion 是当前唯一可接受的 battle binary wire 版本。
	battleWireVersion = 1
)

// HTTPClient 严格消费服务端公开 HTTPS JSON API。
type HTTPClient struct {
	// baseURL 是仅含 scheme/authority 的资格服务端地址。
	baseURL *url.URL
	// client 负责 TLS 验证和有界网络 I/O。
	client *http.Client
	// responseLimit 是单个公开 JSON response 上限，单位为字节。
	responseLimit int64
}

// PublicError 是 OpenAPI 定义的稳定低敏错误响应。
type PublicError struct {
	// Code 是 registry 中的公开数字错误码。
	Code uint32 `json:"code"`
	// MessageKey 是客户端本地化使用的稳定键。
	MessageKey string `json:"messageKey"`
	// RequestID 是服务端分配的低敏请求关联标识。
	RequestID string `json:"requestId"`
	// Retryable 表示相同语义是否允许在策略控制下重试。
	Retryable bool `json:"retryable"`
	// RetryAfterMS 是可选最短重试等待，单位为毫秒。
	RetryAfterMS *uint32 `json:"retryAfterMs,omitempty"`
	// Details 是封闭字段级错误列表。
	Details []ErrorDetail `json:"details,omitempty"`
}

// ErrorDetail 是公开输入错误的字段与稳定原因。
type ErrorDetail struct {
	// Field 是公开 request field 名称。
	Field string `json:"field"`
	// Reason 是不含 backend 文本的稳定原因。
	Reason string `json:"reason"`
}

// Error 返回低敏稳定错误摘要，不包含 response body。
func (failure PublicError) Error() string {
	return fmt.Sprintf("public error code=%d messageKey=%s requestId=%s retryable=%t", failure.Code, failure.MessageKey, failure.RequestID, failure.Retryable)
}

// AuthResponse 是 register/login 返回的公开账号、session、token 与 endpoint 投影。
type AuthResponse struct {
	// Account 是当前账号公开摘要。
	Account AccountSummary `json:"account"`
	// Session 是当前认证 session 摘要。
	Session SessionSummary `json:"session"`
	// Tokens 是当前 bearer 与 refresh credential pair。
	Tokens TokenPair `json:"tokens"`
	// Endpoints 是部署公布的 realtime endpoint 集合。
	Endpoints []Endpoint `json:"endpoints"`
}

// AccountSummary 是账号创建后公开的稳定投影。
type AccountSummary struct {
	// AccountID 是服务端账号 identity。
	AccountID string `json:"accountId"`
	// DisplayName 是规范化后的玩家显示名称。
	DisplayName string `json:"displayName"`
	// CreatedAtMS 是创建时刻，单位为 Unix epoch millisecond。
	CreatedAtMS int64 `json:"createdAtMs"`
}

// SessionSummary 是当前认证 session 的公开投影。
type SessionSummary struct {
	// SessionID 是当前 session identity。
	SessionID string `json:"sessionId"`
	// SessionEpoch 是从 1 开始的认证代际。
	SessionEpoch uint64 `json:"sessionEpoch"`
	// ExpiresAtMS 是 session 到期时刻，单位为 Unix epoch millisecond。
	ExpiresAtMS int64 `json:"expiresAtMs"`
}

// TokenPair 是不可记录的短期 access/refresh credential 原始响应。
type TokenPair struct {
	// AccessToken 是 HTTP bearer credential。
	AccessToken string `json:"accessToken"`
	// RefreshToken 是轮换当前 session 的 credential。
	RefreshToken string `json:"refreshToken"`
	// AccessExpiresAtMS 是 access token 到期时刻，单位为 Unix epoch millisecond。
	AccessExpiresAtMS int64 `json:"accessExpiresAtMs"`
	// RefreshExpiresAtMS 是 refresh token 到期时刻，单位为 Unix epoch millisecond。
	RefreshExpiresAtMS int64 `json:"refreshExpiresAtMs"`
}

// RegisterRequest 是公开账号注册输入。
type RegisterRequest struct {
	// Username 是公开 grammar 允许的账号名。
	Username string `json:"username"`
	// Password 是原样 UTF-8 密码，不得记录。
	Password string `json:"password"`
	// DisplayName 是期望的玩家显示名称。
	DisplayName string `json:"displayName"`
}

// LoginRequest 是公开账号登录输入。
type LoginRequest struct {
	// Username 是已注册账号名。
	Username string `json:"username"`
	// Password 是原样 UTF-8 密码，不得记录。
	Password string `json:"password"`
}

// TicketRequest 是单一 realtime channel ticket 请求。
type TicketRequest struct {
	// Channel 是 WSS 或 TLS_TCP。
	Channel string `json:"channel"`
}

// TicketResponse 是一次性 realtime ticket 与绑定 endpoint。
type TicketResponse struct {
	// Ticket 是仅供紧邻握手使用的一次性 credential。
	Ticket string `json:"ticket"`
	// Endpoint 是 ticket 精确绑定的连接目标。
	Endpoint Endpoint `json:"endpoint"`
	// Scopes 是 ticket 授予的封闭 realtime scope。
	Scopes []string `json:"scopes"`
	// ExpiresAtMS 是等于即失效的 Unix epoch millisecond deadline。
	ExpiresAtMS int64 `json:"expiresAtMs"`
}

// WorldBootstrapResponse 是当前 actor own-world 与 assignment 投影。
type WorldBootstrapResponse struct {
	// World 是持久 PersonalWorld 事实。
	World PersonalWorldSummary `json:"world"`
	// Assignment 是可选当前运行实例投影。
	Assignment *WorldAssignment `json:"assignment,omitempty"`
}

// PersonalWorldSummary 是 HTTP own-world 的客户端安全投影。
type PersonalWorldSummary struct {
	// PersonalWorldID 是持久世界 identity。
	PersonalWorldID string `json:"personalWorldId"`
	// OwnerPlayerID 是不可变 Owner 玩家 identity。
	OwnerPlayerID string `json:"ownerPlayerId"`
	// Lifecycle 是 ACTIVE 或 ARCHIVED。
	Lifecycle string `json:"lifecycle"`
	// Revision 是从 1 开始的持久事实版本。
	Revision uint64 `json:"revision"`
	// CreatedAtMS 是创建时刻，单位为 Unix epoch millisecond。
	CreatedAtMS int64 `json:"createdAtMs"`
}

// WorldAssignment 是公开 current instance 连接投影。
type WorldAssignment struct {
	// PersonalWorldID 是 assignment 绑定的持久世界 identity。
	PersonalWorldID string `json:"personalWorldId"`
	// WorldInstanceID 是当前不可复活运行实例 identity。
	WorldInstanceID string `json:"worldInstanceId"`
	// Endpoint 是当前 TLS_TCP gameplay 连接目标。
	Endpoint Endpoint `json:"endpoint"`
	// Generation 是从 1 开始的 current assignment 代际。
	Generation uint64 `json:"generation"`
	// LeaseExpiresAtMS 是租约到期时刻，单位为 Unix epoch millisecond。
	LeaseExpiresAtMS int64 `json:"leaseExpiresAtMs"`
}

// AcceptVisitInviteRequest 是目标 Visitor 的乐观并发 accept 输入。
type AcceptVisitInviteRequest struct {
	// ExpectedRevision 是从 invite push 读取的 VisitSession revision。
	ExpectedRevision uint64 `json:"expectedRevision"`
}

// AcceptVisitInviteResponse 是 accept 后创建的 reservation 投影。
type AcceptVisitInviteResponse struct {
	// Reservation 是只用于后续 admission/join 的公开 reservation。
	Reservation VisitReservation `json:"reservation"`
}

// VisitReservation 是已接受邀请的有限期 membership reservation。
type VisitReservation struct {
	// VisitSessionID 是目标访问会话 identity。
	VisitSessionID string `json:"visitSessionId"`
	// Revision 是 accept 提交后的 aggregate revision。
	Revision uint64 `json:"revision"`
	// ReservationExpiresAtMS 是 reservation 到期时刻，单位为 Unix epoch millisecond。
	ReservationExpiresAtMS int64 `json:"reservationExpiresAtMs"`
}

// WorldAdmissionRequest 是 own-world 或 visit-world credential 签发目标。
type WorldAdmissionRequest struct {
	// Kind 是 OWN_WORLD 或 VISIT_WORLD。
	Kind string `json:"kind"`
	// VisitSessionID 仅在 VISIT_WORLD 时设置。
	VisitSessionID string `json:"visitSessionId,omitempty"`
}

// WorldAdmissionResponse 是一次性 gameplay admission 与精确 binding 投影。
type WorldAdmissionResponse struct {
	// Credential 是仅供 TLS_TCP handshake/command 使用的 opaque credential。
	Credential string `json:"credential"`
	// Endpoint 是 credential 绑定的 gameplay endpoint。
	Endpoint Endpoint `json:"endpoint"`
	// Role 是 OWNER 或 VISITOR。
	Role string `json:"role"`
	// Purpose 是 OWN_WORLD、JOIN 或 RECONNECT。
	Purpose string `json:"purpose"`
	// VisitRevision 是 Visitor 首帧使用的权威 VisitSession CAS 版本；Owner 为零。
	VisitRevision uint64 `json:"visitRevision"`
	// ExpiresAtMS 是 credential 到期时刻，单位为 Unix epoch millisecond。
	ExpiresAtMS int64 `json:"expiresAtMs"`
}

// BattleTicketRequest 是公开 BattleTicket 的 closed target selector。
type BattleTicketRequest struct {
	// Kind 是 OWN_WORLD 或 VISIT_WORLD。
	Kind string `json:"kind"`
	// VisitSessionID 仅在 VISIT_WORLD 时设置。
	VisitSessionID string `json:"visitSessionId,omitempty"`
}

// BattleEndpoint 是 BattleTicket 唯一允许公布的 UDP endpoint。
type BattleEndpoint struct {
	// Transport 固定为 UDP。
	Transport string `json:"transport"`
	// Host 是 trusted provider 公布的 host。
	Host string `json:"host"`
	// Port 是非零 UDP port。
	Port uint16 `json:"port"`
}

// BattleWireSuite 是 BattleTicket 冻结的 wire 与密码套件。
type BattleWireSuite struct {
	// WireVersion 是 binary envelope 代际。
	WireVersion uint8 `json:"wireVersion"`
	// KeyAgreement 固定为 X25519。
	KeyAgreement string `json:"keyAgreement"`
	// KDF 固定为 HKDF-SHA-256。
	KDF string `json:"kdf"`
	// AEAD 固定为 ChaCha20-Poly1305。
	AEAD string `json:"aead"`
}

// battleTicketResponse 是仅存在于 HTTPS decode 栈上的 raw credential DTO。
type battleTicketResponse struct {
	// TicketID 是 UDP ClientHello 使用的 opaque lookup identity。
	TicketID string `json:"ticketId"`
	// TicketSecret 是必须立即转移到 BattleTicket 的一次性 bearer。
	TicketSecret string `json:"ticketSecret"`
	// Endpoint 是受信 advertised UDP endpoint。
	Endpoint BattleEndpoint `json:"endpoint"`
	// WireSuite 是客户端必须精确支持的算法集合。
	WireSuite BattleWireSuite `json:"wireSuite"`
	// Role 是 OWNER 或 VISITOR。
	Role string `json:"role"`
	// TargetKind 是 OWN_WORLD 或 VISIT_WORLD。
	TargetKind string `json:"targetKind"`
	// TargetRevision 是签发时冻结的权威 target revision。
	TargetRevision uint64 `json:"targetRevision"`
	// ExpiresAtMS 是等于即失效的 Unix milliseconds。
	ExpiresAtMS int64 `json:"expiresAtMs"`
}

// BattleTicket 拥有一次 BattleTicket HTTPS response 的 credential 与低敏投影。
//
// TicketSecret 不作为公开字段暴露；调用方只能把 ownership 一次性转移给紧邻的
// battle protocol client。Close 与默认格式化均不会泄漏 credential。
type BattleTicket struct {
	// TicketID 是 ClientHello 使用的非秘密 lookup identity。
	TicketID string
	// Endpoint 是受信 advertised UDP endpoint。
	Endpoint BattleEndpoint
	// WireSuite 是精确 wire 与密码套件。
	WireSuite BattleWireSuite
	// Role 是 OWNER 或 VISITOR。
	Role string
	// TargetKind 是 OWN_WORLD 或 VISIT_WORLD。
	TargetKind string
	// TargetRevision 是签发时冻结的权威 target revision。
	TargetRevision uint64
	// ExpiresAtMS 是等于即失效的 Unix milliseconds。
	ExpiresAtMS int64
	// secret 是一次性 bearer 的唯一 owner。
	secret *Secret
}

// BattleCredential 是向独立协议客户端一次性转移的 fixed binary credential。
type BattleCredential struct {
	// TicketID 是 ClientHello 使用的公开 lookup identity。
	TicketID [battleTicketIDBytes]byte
	// TicketSecret 是只允许进入 child stdin 一次的 bearer。
	TicketSecret [battleTicketSecretBytes]byte
}

// Clear 清零全部 credential bytes；调用方必须在所有路径执行。
func (credential *BattleCredential) Clear() {
	if credential == nil {
		return
	}
	clear(credential.TicketID[:])
	clear(credential.TicketSecret[:])
}

// TakeCredential 原子消费 BattleTicket bearer 并解码为 fixed binary owner。
func (ticket *BattleTicket) TakeCredential() (BattleCredential, error) {
	const (
		ticketIDPrefix     = "btk1_"
		ticketSecretPrefix = "bts1_"
	)
	var credential BattleCredential
	if ticket == nil || ticket.secret == nil {
		return credential, errors.New("battle ticket credential is unavailable")
	}
	secret := ticket.secret
	ticket.secret = nil
	secretText, err := secret.Take()
	_ = secret.Close()
	if err != nil {
		return credential, err
	}
	defer clearString(&secretText)
	if !strings.HasPrefix(ticket.TicketID, ticketIDPrefix) ||
		!strings.HasPrefix(secretText, ticketSecretPrefix) {
		return credential, errors.New("battle ticket credential prefix drifted")
	}
	ticketID, err := base64.RawURLEncoding.DecodeString(
		strings.TrimPrefix(ticket.TicketID, ticketIDPrefix),
	)
	if err != nil || len(ticketID) != len(credential.TicketID) {
		clear(ticketID)
		return credential, errors.New("battle ticket identity decode failed")
	}
	copy(credential.TicketID[:], ticketID)
	clear(ticketID)
	secretBytes, err := base64.RawURLEncoding.DecodeString(
		strings.TrimPrefix(secretText, ticketSecretPrefix),
	)
	if err != nil || len(secretBytes) != len(credential.TicketSecret) {
		clear(secretBytes)
		credential.Clear()
		return BattleCredential{}, errors.New("battle ticket secret decode failed")
	}
	copy(credential.TicketSecret[:], secretBytes)
	clear(secretBytes)
	return credential, nil
}

// TakeSecret 把 BattleTicket secret ownership 一次性转移给协议客户端。
func (ticket *BattleTicket) TakeSecret() (*Secret, error) {
	if ticket == nil || ticket.secret == nil {
		return nil, errors.New("battle ticket secret is unavailable")
	}
	secret := ticket.secret
	ticket.secret = nil
	return secret, nil
}

// Close 清除尚未转移的 BattleTicket secret；重复调用安全。
func (ticket *BattleTicket) Close() error {
	if ticket == nil {
		return nil
	}
	ticket.secret.Clear()
	ticket.secret = nil
	return nil
}

// String 返回固定脱敏值，禁止默认格式化泄漏完整 ticket projection。
func (BattleTicket) String() string { return redactedValue }

// GoString 与 String 保持相同脱敏语义。
func (BattleTicket) GoString() string { return redactedValue }

// NewHTTPClient 创建使用调用方 TLS transport 的严格公开 API client。
func NewHTTPClient(baseURL string, client *http.Client) (*HTTPClient, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("qualification HTTPS base URL is invalid")
	}
	if client == nil {
		return nil, errors.New("qualification HTTP transport is nil")
	}
	clone := *client
	if clone.Timeout <= 0 || clone.Timeout > defaultHTTPTimeout {
		clone.Timeout = defaultHTTPTimeout
	}
	return &HTTPClient{baseURL: parsed, client: &clone, responseLimit: defaultHTTPBodyLimit}, nil
}

// Version 调用冻结的 getVersion operation。
func (client *HTTPClient) Version(ctx context.Context) (VersionProjection, error) {
	var response VersionProjection
	err := client.doJSON(ctx, http.MethodGet, "/v1/version", nil, nil, "", http.StatusOK, &response)
	if err == nil {
		err = response.validate()
	}
	return response, err
}

// BootstrapConfig 调用冻结的 getBootstrapConfig operation。
func (client *HTTPClient) BootstrapConfig(ctx context.Context) (BootstrapProjection, error) {
	var response BootstrapProjection
	err := client.doJSON(ctx, http.MethodGet, "/v1/config", nil, nil, "", http.StatusOK, &response)
	if err == nil {
		err = response.validate()
	}
	return response, err
}

// Register 调用冻结的 registerAccount operation。
func (client *HTTPClient) Register(ctx context.Context, request RegisterRequest) (AuthResponse, error) {
	var response AuthResponse
	err := client.doJSON(ctx, http.MethodPost, "/v1/auth/register", request, nil, "", http.StatusCreated, &response)
	if err == nil {
		err = response.validate()
	}
	return response, err
}

// Login 调用冻结的 loginAccount operation。
func (client *HTTPClient) Login(ctx context.Context, request LoginRequest) (AuthResponse, error) {
	var response AuthResponse
	err := client.doJSON(ctx, http.MethodPost, "/v1/auth/login", request, nil, "", http.StatusOK, &response)
	if err == nil {
		err = response.validate()
	}
	return response, err
}

// Refresh 调用冻结的 refreshSession operation 并轮换 refresh credential。
func (client *HTTPClient) Refresh(ctx context.Context, refreshToken *Secret) (TokenPair, error) {
	value, err := refreshToken.Reveal()
	if err != nil {
		return TokenPair{}, err
	}
	var response TokenPair
	err = client.doJSON(ctx, http.MethodPost, "/v1/auth/refresh", struct {
		RefreshToken string `json:"refreshToken"`
	}{RefreshToken: value}, nil, "", http.StatusOK, &response)
	if err == nil {
		err = response.validate()
	}
	return response, err
}

// Logout 调用冻结的 logoutSession operation。
func (client *HTTPClient) Logout(ctx context.Context, accessToken *Secret) error {
	return client.doJSON(ctx, http.MethodPost, "/v1/auth/logout", nil, accessToken, "", http.StatusNoContent, nil)
}

// IssueTicket 调用冻结的 issueConnectionTicket operation。
func (client *HTTPClient) IssueTicket(ctx context.Context, accessToken *Secret, request TicketRequest) (TicketResponse, error) {
	var response TicketResponse
	err := client.doJSON(ctx, http.MethodPost, "/v1/session/tickets", request, accessToken, "", http.StatusCreated, &response)
	if err == nil {
		err = response.validate(request.Channel)
	}
	return response, err
}

// WorldBootstrap 调用冻结的 getWorldBootstrap operation。
func (client *HTTPClient) WorldBootstrap(ctx context.Context, accessToken *Secret) (WorldBootstrapResponse, error) {
	var response WorldBootstrapResponse
	err := client.doJSON(ctx, http.MethodGet, "/v1/world/bootstrap", nil, accessToken, "", http.StatusOK, &response)
	if err == nil {
		err = response.validate()
	}
	return response, err
}

// AcceptVisitInvite 调用冻结的 acceptVisitInvite operation。
func (client *HTTPClient) AcceptVisitInvite(ctx context.Context, accessToken *Secret, visitSessionID, inviteID, idempotencyKey string, request AcceptVisitInviteRequest) (AcceptVisitInviteResponse, error) {
	if !validPublicIdentity(visitSessionID) || !validPublicIdentity(inviteID) || !validIdempotencyKey(idempotencyKey) || request.ExpectedRevision == 0 {
		return AcceptVisitInviteResponse{}, errors.New("accept visit invite input is invalid")
	}
	path := "/v1/visits/" + url.PathEscape(visitSessionID) + "/invites/" + url.PathEscape(inviteID) + "/accept"
	var response AcceptVisitInviteResponse
	err := client.doJSON(ctx, http.MethodPost, path, request, accessToken, idempotencyKey, http.StatusOK, &response)
	if err == nil {
		err = response.Reservation.validate(visitSessionID)
	}
	return response, err
}

// IssueWorldAdmission 调用冻结的 issueWorldAdmission operation。
func (client *HTTPClient) IssueWorldAdmission(ctx context.Context, accessToken *Secret, idempotencyKey string, request WorldAdmissionRequest) (WorldAdmissionResponse, error) {
	if !validIdempotencyKey(idempotencyKey) || request.Kind != "OWN_WORLD" && request.Kind != "VISIT_WORLD" || request.Kind == "OWN_WORLD" && request.VisitSessionID != "" || request.Kind == "VISIT_WORLD" && !validPublicIdentity(request.VisitSessionID) {
		return WorldAdmissionResponse{}, errors.New("world admission input is invalid")
	}
	var response WorldAdmissionResponse
	err := client.doJSON(ctx, http.MethodPost, "/v1/world/admissions", request, accessToken, idempotencyKey, http.StatusCreated, &response)
	if err == nil {
		err = response.validate(request.Kind)
	}
	return response, err
}

// IssueBattleTicket 调用冻结的 issueBattleTicket operation，并立即封装 raw credential。
func (client *HTTPClient) IssueBattleTicket(ctx context.Context, accessToken *Secret, idempotencyKey string, request BattleTicketRequest) (*BattleTicket, error) {
	if !validIdempotencyKey(idempotencyKey) ||
		request.Kind != "OWN_WORLD" && request.Kind != "VISIT_WORLD" ||
		request.Kind == "OWN_WORLD" && request.VisitSessionID != "" ||
		request.Kind == "VISIT_WORLD" && !validPublicIdentity(request.VisitSessionID) {
		return nil, errors.New("battle ticket input is invalid")
	}
	var response battleTicketResponse
	if err := client.doJSON(ctx, http.MethodPost, "/v1/battle/tickets", request, accessToken, idempotencyKey, http.StatusCreated, &response); err != nil {
		clearString(&response.TicketSecret)
		return nil, err
	}
	ticket, err := takeBattleTicket(&response)
	clearString(&response.TicketSecret)
	return ticket, err
}

// IssueBattleTicketAfterResponseLoss 丢弃首次成功 response，再用同一 key 精确重放。
//
// 该方法建模 HTTPS response 已提交但未交付给下游协议 owner：首次 credential 只在
// 本调用栈中用于常量时间等值检查并立即清除，唯一返回值来自第二次公开请求。
func (client *HTTPClient) IssueBattleTicketAfterResponseLoss(ctx context.Context, accessToken *Secret, idempotencyKey string, request BattleTicketRequest) (*BattleTicket, error) {
	first, err := client.IssueBattleTicket(ctx, accessToken, idempotencyKey, request)
	if err != nil {
		return nil, err
	}
	defer first.Close()
	replayed, err := client.IssueBattleTicket(ctx, accessToken, idempotencyKey, request)
	if err != nil {
		return nil, err
	}
	if !sameBattleTicket(first, replayed) {
		_ = replayed.Close()
		return nil, errors.New("battle ticket response-loss replay drifted")
	}
	return replayed, nil
}

// doJSON 执行带 deadline、closed response 和稳定公开错误的单次 HTTPS operation。
func (client *HTTPClient) doJSON(ctx context.Context, method, path string, requestBody any, accessToken *Secret, idempotencyKey string, expectedStatus int, responseBody any) error {
	if client == nil || client.baseURL == nil || client.client == nil || ctx == nil {
		return errors.New("qualification HTTP request context is invalid")
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultHTTPTimeout)
		defer cancel()
	}
	var body io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("encode qualification request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	requestURL := *client.baseURL
	requestURL.Path = path
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), body)
	if err != nil {
		return fmt.Errorf("create qualification request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if accessToken != nil {
		value, revealErr := accessToken.Reveal()
		if revealErr != nil {
			return revealErr
		}
		request.Header.Set("Authorization", "Bearer "+value)
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := client.client.Do(request)
	if err != nil {
		return fmt.Errorf("execute qualification HTTP operation: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		var publicError PublicError
		if decodeErr := decodeClosedJSON(response.Body, client.responseLimit, &publicError); decodeErr != nil {
			return fmt.Errorf("qualification HTTP status=%d with invalid public error", response.StatusCode)
		}
		if validateErr := publicError.validate(); validateErr != nil {
			return fmt.Errorf("qualification HTTP status=%d with incomplete public error", response.StatusCode)
		}
		return publicError
	}
	if responseBody == nil {
		if response.ContentLength > 0 {
			return errors.New("qualification HTTP success unexpectedly returned a body")
		}
		return nil
	}
	if err := decodeClosedJSON(response.Body, client.responseLimit, responseBody); err != nil {
		return fmt.Errorf("decode qualification HTTP success: %w", err)
	}
	return nil
}

// takeBattleTicket 验证 closed DTO 并把 raw secret 转移到默认脱敏 owner。
func takeBattleTicket(response *battleTicketResponse) (*BattleTicket, error) {
	if response == nil || !validBattleTicketID(response.TicketID) ||
		!validBattleEndpoint(response.Endpoint) ||
		response.WireSuite != (BattleWireSuite{
			WireVersion: battleWireVersion, KeyAgreement: "X25519",
			KDF: "HKDF-SHA-256", AEAD: "ChaCha20-Poly1305",
		}) ||
		response.TargetRevision == 0 || response.ExpiresAtMS <= 0 ||
		response.Role == "OWNER" && response.TargetKind != "OWN_WORLD" ||
		response.Role == "VISITOR" && response.TargetKind != "VISIT_WORLD" ||
		response.Role != "OWNER" && response.Role != "VISITOR" {
		return nil, errors.New("battle ticket response is invalid")
	}
	if !validBattleTicketSecret(response.TicketSecret) {
		return nil, errors.New("battle ticket credential is invalid")
	}
	secret, err := NewSecret(response.TicketSecret)
	if err != nil {
		return nil, err
	}
	return &BattleTicket{
		TicketID: response.TicketID, Endpoint: response.Endpoint,
		WireSuite: response.WireSuite, Role: response.Role,
		TargetKind: response.TargetKind, TargetRevision: response.TargetRevision,
		ExpiresAtMS: response.ExpiresAtMS, secret: secret,
	}, nil
}

// sameBattleTicket 对完整幂等 response 做常量时间 credential 比较。
func sameBattleTicket(left, right *BattleTicket) bool {
	if left == nil || right == nil || left.secret == nil || right.secret == nil ||
		left.TicketID != right.TicketID || left.Endpoint != right.Endpoint ||
		left.WireSuite != right.WireSuite || left.Role != right.Role ||
		left.TargetKind != right.TargetKind ||
		left.TargetRevision != right.TargetRevision ||
		left.ExpiresAtMS != right.ExpiresAtMS {
		return false
	}
	return left.secret.equal(right.secret)
}

// validBattleTicketID 校验生产 wire 使用的 fixed 128-bit opaque identity。
func validBattleTicketID(value string) bool {
	const prefix = "btk1_"
	if len(value) <= len(prefix) || value[:len(prefix)] != prefix {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value[len(prefix):])
	return err == nil && len(decoded) == battleTicketIDBytes &&
		prefix+base64.RawURLEncoding.EncodeToString(decoded) == value
}

// validBattleTicketSecret 校验 versioned 256-bit bearer 的 canonical base64url。
func validBattleTicketSecret(value string) bool {
	const prefix = "bts1_"
	if len(value) <= len(prefix) || value[:len(prefix)] != prefix {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value[len(prefix):])
	defer clear(decoded)
	return err == nil && len(decoded) == battleTicketSecretBytes &&
		prefix+base64.RawURLEncoding.EncodeToString(decoded) == value
}

// validBattleEndpoint 拒绝非 UDP、空 host 与零 port。
func validBattleEndpoint(endpoint BattleEndpoint) bool {
	return endpoint.Transport == "UDP" && endpoint.Host != "" &&
		endpoint.Port != 0 && !strings.ContainsAny(endpoint.Host, "/\\?#")
}

// clearString 尽早移除可控 DTO 对 raw credential 的引用。
//
// Go string backing storage 不能可靠覆零；真实 bytes owner 由 Secret 负责。
func clearString(value *string) {
	if value != nil {
		*value = ""
	}
}

// validate 检查公开错误必填字段与有界 retryAfter。
func (failure PublicError) validate() error {
	if failure.Code == 0 || failure.MessageKey == "" || len(failure.MessageKey) > 128 || failure.RequestID == "" || len(failure.RequestID) > 64 {
		return errors.New("public error projection is incomplete")
	}
	if failure.RetryAfterMS != nil && *failure.RetryAfterMS > 60_000 {
		return errors.New("public error retryAfterMs is outside contract")
	}
	if len(failure.Details) > 16 {
		return errors.New("public error details exceed contract")
	}
	return nil
}

// validate 检查公开版本投影的必填边界。
func (projection VersionProjection) validate() error {
	if projection.ProtocolVersion == 0 || projection.MinimumClientVersion == "" || len(projection.MinimumClientVersion) > 32 || projection.ServerVersion == "" || len(projection.ServerVersion) > 32 {
		return errors.New("version projection is incomplete")
	}
	return nil
}

// validate 检查 endpoint 集合和资源上限，不接受未知 channel 或重复 channel。
func (projection BootstrapProjection) validate() error {
	example := EndpointManifestExample{SchemaVersion: 1, Version: VersionProjection{ProtocolVersion: 1, MinimumClientVersion: "validation", ServerVersion: "validation"}, Config: projection}
	return example.Validate()
}

// validate 检查账号、session、token 和 endpoint 必填语义。
func (response AuthResponse) validate() error {
	if response.Account.AccountID == "" || response.Account.DisplayName == "" || response.Account.CreatedAtMS <= 0 || response.Session.SessionID == "" || response.Session.SessionEpoch == 0 || response.Session.ExpiresAtMS <= 0 {
		return errors.New("auth response identity projection is incomplete")
	}
	if err := response.Tokens.validate(); err != nil {
		return err
	}
	return (BootstrapProjection{Endpoints: response.Endpoints, Limits: PublicLimits{HTTPBodyBytes: 1024, RealtimeFrameBytes: 1024}}).validate()
}

// validate 检查 token pair 的 credential 与 deadline 均存在。
func (pair TokenPair) validate() error {
	if len(pair.AccessToken) < 32 || len(pair.AccessToken) > 2048 || len(pair.RefreshToken) < 32 || len(pair.RefreshToken) > 2048 || pair.AccessExpiresAtMS <= 0 || pair.RefreshExpiresAtMS <= 0 {
		return errors.New("token pair projection is incomplete")
	}
	return nil
}

// validate 检查 ticket 绑定 requested channel、scope、endpoint 与到期时刻。
func (response TicketResponse) validate(channel string) error {
	if !validTicketCredential(response.Ticket) || response.Endpoint.Channel != channel || response.ExpiresAtMS <= 0 || len(response.Scopes) == 0 || len(response.Scopes) > 8 {
		return errors.New("ticket response projection is incomplete")
	}
	if channel != "WSS" && channel != "TLS_TCP" || !validEndpoint(response.Endpoint) {
		return errors.New("ticket response endpoint is invalid")
	}
	return nil
}

// validate 检查 own-world 与 assignment 的 identity、版本和 TLS_TCP endpoint。
func (response WorldBootstrapResponse) validate() error {
	if !validPublicIdentity(response.World.PersonalWorldID) || !validPublicIdentity(response.World.OwnerPlayerID) || response.World.Lifecycle != "ACTIVE" && response.World.Lifecycle != "ARCHIVED" || response.World.Revision == 0 || response.World.CreatedAtMS <= 0 {
		return errors.New("world bootstrap projection is incomplete")
	}
	if response.Assignment != nil {
		assignment := response.Assignment
		if assignment.PersonalWorldID != response.World.PersonalWorldID || !validPublicIdentity(assignment.WorldInstanceID) || assignment.Endpoint.Channel != "TLS_TCP" || !validEndpoint(assignment.Endpoint) || assignment.Generation == 0 || assignment.LeaseExpiresAtMS <= 0 {
			return errors.New("world assignment projection is incomplete")
		}
	}
	return nil
}

// validate 检查 reservation 精确绑定 path 中的 VisitSession。
func (reservation VisitReservation) validate(visitSessionID string) error {
	if reservation.VisitSessionID != visitSessionID || reservation.Revision == 0 || reservation.ReservationExpiresAtMS <= 0 {
		return errors.New("visit reservation projection is incomplete")
	}
	return nil
}

// validate 检查 admission credential、endpoint、role、purpose 与请求目标一致。
func (response WorldAdmissionResponse) validate(kind string) error {
	if !validAdmissionCredential(response.Credential) || response.Endpoint.Channel != "TLS_TCP" || !validEndpoint(response.Endpoint) || response.ExpiresAtMS <= 0 {
		return errors.New("world admission projection is incomplete")
	}
	if kind == "OWN_WORLD" && (response.Role != "OWNER" || response.Purpose != "OWN_WORLD" || response.VisitRevision != 0) {
		return errors.New("own-world admission binding is inconsistent")
	}
	if kind == "VISIT_WORLD" && (response.Role != "VISITOR" || response.Purpose != "JOIN" && response.Purpose != "RECONNECT" || response.VisitRevision == 0) {
		return errors.New("visit-world admission binding is inconsistent")
	}
	return nil
}

// validEndpoint 检查公开 endpoint 的 channel、host 与非零端口。
func validEndpoint(endpoint Endpoint) bool {
	return (endpoint.Channel == "WSS" || endpoint.Channel == "TLS_TCP") && endpoint.Host != "" && len(endpoint.Host) <= 253 && endpoint.Port != 0
}

// validPublicIdentity 检查公开 path/target identity 的有界安全 ASCII grammar。
func validPublicIdentity(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, character := range []byte(value) {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '.' || character == '_' || character == ':' || character == '-' {
			continue
		}
		return false
	}
	return true
}

// validIdempotencyKey 检查 HTTP mutation key 的公开长度与字符集。
func validIdempotencyKey(value string) bool {
	return len(value) >= 16 && validPublicIdentity(value)
}

// decodeClosedJSON 拒绝空 body、未知字段、额外 value 和超过限制的响应。
func decodeClosedJSON(reader io.Reader, limit int64, target any) error {
	if reader == nil || target == nil || limit <= 0 {
		return errors.New("closed JSON input is invalid")
	}
	limited := &io.LimitedReader{R: reader, N: limit + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if limited.N == 0 {
		return errors.New("JSON response exceeds byte limit")
	}
	return requireJSONEOF(decoder)
}

// endpointURL 返回公开 endpoint 的 host:port，仅供精确 ticket/admission binding 使用。
func endpointURL(endpoint Endpoint) string {
	return net.JoinHostPort(endpoint.Host, fmt.Sprint(endpoint.Port))
}
