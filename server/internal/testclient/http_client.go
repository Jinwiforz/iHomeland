package testclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

const (
	// defaultHTTPBodyLimit 是公开配置返回前客户端采用的保守响应上限，单位为字节。
	defaultHTTPBodyLimit = 1 << 20
	// defaultHTTPTimeout 是调用方未提供更短 deadline 时的兜底请求预算。
	defaultHTTPTimeout = 15 * time.Second
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
