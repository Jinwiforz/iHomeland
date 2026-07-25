package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/battleentry"
	"github.com/jinwiforz/ihomeland/server/internal/config"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
	"github.com/jinwiforz/ihomeland/server/internal/worldentry"
)

// authenticatedSessionKey 隔离Gin单请求上下文中的受信认证快照，不承载raw bearer。
const authenticatedSessionKey = "ihomeland.authenticated-session"

// operationContextKey 隔离当前请求匹配的不可变operation策略。
const operationContextKey = "ihomeland.operation"

// AccountApplication 是公开账号handler所需的最小端口。
type AccountApplication interface {
	// Register 创建账号与首个session。
	Register(ctx context.Context, command account.RegisterCommand) (account.AuthResult, error)
	// Login 验证凭据并创建新session。
	Login(ctx context.Context, command account.LoginCommand) (account.AuthResult, error)
}

// SessionApplication 是公开认证、轮换、退出与ticket handler所需端口。
type SessionApplication interface {
	// AuthenticateHTTPS 返回受信身份与共同deadline。
	AuthenticateHTTPS(ctx context.Context, raw string) (session.AuthenticatedSession, error)
	// Refresh 原子轮换token pair。
	Refresh(ctx context.Context, raw string) (session.TokenPair, error)
	// Logout 使当前session lineage失效。
	Logout(ctx context.Context, auth session.AuthContext) (session.Invalidation, error)
	// IssueTicket 为受支持channel签发一次性资格。
	IssueTicket(ctx context.Context, auth session.AuthContext, channel session.Channel) (session.ConnectionTicket, error)
}

// WorldApplication 是公开world handler所需的三个application用例。
type WorldApplication interface {
	// BootstrapOwnWorld 返回持久world与可选assignment投影。
	BootstrapOwnWorld(ctx context.Context, authenticated session.AuthenticatedSession) (worldentry.BootstrapResult, error)
	// AcceptVisitInvite 接受定向邀请并返回reservation。
	AcceptVisitInvite(ctx context.Context, authenticated session.AuthenticatedSession, visitID visitsession.VisitSessionID, inviteID visitsession.InviteID, expected visitsession.Revision, idempotencyKey string) (worldentry.ReservationResult, error)
	// IssueWorldAdmission 从权威事实签发opaque credential。
	IssueWorldAdmission(ctx context.Context, authenticated session.AuthenticatedSession, target worldentry.AdmissionTarget, idempotencyKey string) (worldentry.AdmissionResult, error)
}

// BattleApplication 是公开 BattleTicket handler 唯一允许调用的 application 端口。
type BattleApplication interface {
	// Issue 从权威 owner 事实签发 exact SimulationTarget 的短期 credential。
	Issue(ctx context.Context, authenticated session.AuthenticatedSession, target battleentry.Target, idempotencyKey string) (battleentry.Result, error)
}

// HTTPObserver 只接收低基数operation、status class、outcome与数值测量。
type HTTPObserver interface {
	// ObservePublicHTTP 记录一次完成的公开请求。
	ObservePublicHTTP(operation string, statusClass string, outcome string, seconds float64, responseBytes int)
}

// RouterConfig 保存公开协议投影与无状态执行依赖。
type RouterConfig struct {
	// ServerVersion 是构建时注入且长度受控的公开版本。
	ServerVersion string
	// ProtocolVersion 是冻结HTTP契约主版本。
	ProtocolVersion uint32
	// MinimumClientVersion 是允许接入的最低客户端版本。
	MinimumClientVersion string
	// Endpoints 是配置验证后的WSS与TLS/TCP advertised manifest；NewRouter会复制slice。
	Endpoints []session.Endpoint
	// HTTPBodyBytes 是bootstrap config公开的HTTP请求体上限。
	HTTPBodyBytes int
	// RealtimeFrameBytes 是bootstrap config公开的realtime frame上限。
	RealtimeFrameBytes int
	// Ready 在storage与public component完整接线后返回true。
	Ready func() bool
	// Rates 为每个operation提供完整单进程预算。
	Rates map[string]config.RatePolicy
	// RateMaxEntries 是pre/post-auth bucket总容量上限。
	RateMaxEntries int
	// RateIdleTTL 是bucket惰性回收时间。
	RateIdleTTL time.Duration
	// Observer 接收不含URL、IP、identity与错误文本的低基数测量。
	Observer HTTPObserver
	// Logger 只记录request ID、operation、status、duration、bytes与稳定outcome。
	Logger *slog.Logger
}

// Router 拥有固定operation table、middleware顺序与三个application端口。
type Router struct {
	// engine 是只在transport包内可见的Gin路由器。
	engine *gin.Engine
	// accounts 持有账号用例。
	accounts AccountApplication
	// sessions 持有认证与session用例。
	sessions SessionApplication
	// worlds 持有world-entry编排。
	worlds WorldApplication
	// battles 持有 battle-entry target admission。
	battles BattleApplication
	// config 是启动后不可变公开投影。
	config RouterConfig
	// limiter 同时执行匿名IP和认证session两阶段预算。
	limiter *limiter
}

// NewRouter 使用gin.New和固定middleware构造全部11个冻结operation。
func NewRouter(accounts AccountApplication, sessions SessionApplication, worlds WorldApplication, battles BattleApplication, config RouterConfig) (*Router, error) {
	if accounts == nil || sessions == nil || worlds == nil || battles == nil || config.ServerVersion == "" || config.ProtocolVersion == 0 || config.MinimumClientVersion == "" || len(config.Endpoints) != 2 || config.HTTPBodyBytes < 4096 || config.RealtimeFrameBytes < 1024 || config.Ready == nil || len(config.Rates) != len(operations) || config.RateMaxEntries < 1 || config.RateIdleTTL <= 0 || config.Observer == nil || config.Logger == nil {
		return nil, errors.New("public HTTP router dependencies are incomplete")
	}
	for _, endpoint := range config.Endpoints {
		if !endpoint.Valid() || endpoint.Channel() == session.ChannelHTTPS {
			return nil, errors.New("public HTTP endpoint manifest is invalid")
		}
	}
	config.Endpoints = append([]session.Endpoint(nil), config.Endpoints...)
	gin.SetMode(gin.ReleaseMode)
	router := &Router{engine: gin.New(), accounts: accounts, sessions: sessions, worlds: worlds, battles: battles, config: config, limiter: newLimiter(config.Rates, config.RateMaxEntries, config.RateIdleTTL)}
	router.engine.Use(router.safetyMiddleware())
	handlers := map[string]gin.HandlerFunc{
		"getVersion": router.getVersion, "getBootstrapConfig": router.getBootstrapConfig,
		"registerAccount": router.registerAccount, "loginAccount": router.loginAccount, "refreshSession": router.refreshSession,
		"logoutSession": router.logoutSession, "issueConnectionTicket": router.issueConnectionTicket,
		"getWorldBootstrap": router.getWorldBootstrap, "acceptVisitInvite": router.acceptVisitInvite,
		"issueWorldAdmission": router.issueWorldAdmission, "issueBattleTicket": router.issueBattleTicket,
	}
	for _, operation := range operations {
		handler, exists := handlers[operation.ID]
		if !exists {
			return nil, errors.New("public HTTP operation handler is missing")
		}
		router.engine.Handle(operation.Method, operation.Path, router.operationMiddleware(operation), handler)
	}
	router.engine.NoRoute(func(c *gin.Context) { router.writeError(c, validationError) })
	router.engine.NoMethod(func(c *gin.Context) { router.writeError(c, validationError) })
	return router, nil
}

// Handler 返回标准库handler，listener lifecycle无需依赖Gin类型。
func (router *Router) Handler() http.Handler { return router.engine }

// safetyMiddleware 设置request correlation、安全header并把panic转换为稳定内部错误。
//
// 该middleware必须位于最外层，才能在所有后续阶段结束后记录低基数字段；日志和metrics
// 不读取URL、remote identity、header、body、credential或backend错误文本。
func (router *Router) safetyMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		startedAt := time.Now()
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Cache-Control", "no-store")
		c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		requestID := c.GetHeader("X-Request-ID")
		if !safeRequestID(requestID) {
			var material [16]byte
			if _, err := rand.Read(material[:]); err != nil {
				requestID = "request-id-unavailable"
				c.Set("request-id", requestID)
				c.Header("X-Request-ID", requestID)
				router.writeError(c, internalError)
				return
			}
			requestID = hex.EncodeToString(material[:])
		}
		c.Set("request-id", requestID)
		c.Header("X-Request-ID", requestID)
		defer func() {
			if recover() != nil {
				router.writeError(c, internalError)
			}
			operationID, _ := c.Get("operation-id")
			operation, _ := operationID.(string)
			if operation == "" {
				return
			}
			status := c.Writer.Status()
			statusClass, outcome := "5xx", "server_error"
			if status >= 200 && status < 300 {
				statusClass, outcome = "2xx", "success"
			} else if status >= 400 && status < 500 {
				statusClass, outcome = "4xx", "client_error"
			}
			duration := time.Since(startedAt)
			router.config.Observer.ObservePublicHTTP(operation, statusClass, outcome, duration.Seconds(), c.Writer.Size())
			router.config.Logger.Info("public HTTP request completed", "request_id", requestID, "operation", operation, "status", status, "duration_ms", duration.Milliseconds(), "response_bytes", c.Writer.Size(), "outcome", outcome)
		}()
		c.Next()
	}
}

// operationMiddleware 按固定顺序执行readiness、资源门、pre-auth限流、deadline、Bearer认证与post-auth限流。
//
// 认证身份只来自Session owner并保存在当前Gin context；任一门拒绝都会Abort且不会调用application。
// request context取消会向下传播，但不能证明可能提交的mutation尚未提交。
func (router *Router) operationMiddleware(operation Operation) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("operation-id", operation.ID)
		c.Set(operationContextKey, operation)
		if !router.config.Ready() {
			router.writeError(c, dependencyError)
			return
		}
		if c.Request.URL.RawQuery != "" {
			router.writeError(c, validationError)
			return
		}
		if operation.BodyLimit == 0 {
			if err := requireNoBody(c.Request); err != nil {
				router.writeError(c, validationError)
				return
			}
		} else if c.Request.ContentLength > operation.BodyLimit {
			router.writeError(c, validationError)
			return
		}
		if operation.Idempotency == IdempotencyKeyRequired && !safeIdempotencyKey(c.GetHeader("Idempotency-Key")) {
			router.writeError(c, validationError)
			return
		}
		remoteIP, ok := normalizedRemoteIP(c.Request.RemoteAddr)
		if !ok {
			router.writeError(c, validationError)
			return
		}
		if allowed, retry := router.limiter.allow(operation.ID, "pre_auth", remoteIP, time.Now().UTC()); !allowed {
			c.Header("Retry-After", strconv.Itoa(int((retry+time.Second-1)/time.Second)))
			router.writeError(c, rateLimited)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), operation.Timeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		if operation.Authenticated {
			raw, ok := bearerCredential(c.GetHeader("Authorization"))
			if !ok {
				router.writeError(c, unauthenticated)
				return
			}
			authenticated, err := router.sessions.AuthenticateHTTPS(ctx, raw)
			if err != nil {
				router.writeError(c, mapApplicationError(err))
				return
			}
			c.Set(authenticatedSessionKey, authenticated)
			subject := authenticated.AuthContext().SessionID().String() + ":" + strconv.FormatUint(uint64(authenticated.AuthContext().Epoch()), 10)
			if allowed, retry := router.limiter.allow(operation.ID, "post_auth", subject, time.Now().UTC()); !allowed {
				c.Header("Retry-After", strconv.Itoa(int((retry+time.Second-1)/time.Second)))
				router.writeError(c, rateLimited)
				return
			}
		}
		c.Next()
	}
}

// getVersion 返回固定协议与构建兼容信息。
func (router *Router) getVersion(c *gin.Context) {
	c.JSON(http.StatusOK, versionProjection(router.config.ServerVersion, router.config.ProtocolVersion, router.config.MinimumClientVersion))
}

// getBootstrapConfig 只公开advertised endpoints与协议资源上限。
func (router *Router) getBootstrapConfig(c *gin.Context) {
	c.JSON(http.StatusOK, bootstrapConfigProjection(router.config.Endpoints, router.config.HTTPBodyBytes, router.config.RealtimeFrameBytes))
}

// registerAccount 解码公开字段并委托Account owner。
func (router *Router) registerAccount(c *gin.Context) {
	var request registerRequest
	if router.decodeOperationJSON(c, &request) != nil || !validRegisterRequest(request) {
		router.writeError(c, validationError)
		return
	}
	result, err := router.accounts.Register(c.Request.Context(), account.NewRegisterCommand(request.Username, request.Password, request.DisplayName))
	if err != nil {
		router.writeError(c, mapApplicationError(err))
		return
	}
	c.JSON(http.StatusCreated, authProjection(result, router.config.Endpoints))
}

// loginAccount 解码凭据并委托Account owner。
func (router *Router) loginAccount(c *gin.Context) {
	var request loginRequest
	if router.decodeOperationJSON(c, &request) != nil || !validLoginRequest(request) {
		router.writeError(c, validationError)
		return
	}
	result, err := router.accounts.Login(c.Request.Context(), account.NewLoginCommand(request.Username, request.Password))
	if err != nil {
		router.writeError(c, mapApplicationError(err))
		return
	}
	c.JSON(http.StatusOK, authProjection(result, router.config.Endpoints))
}

// refreshSession 解码refresh bearer并委托Session owner原子轮换。
func (router *Router) refreshSession(c *gin.Context) {
	var request refreshRequest
	if router.decodeOperationJSON(c, &request) != nil || len(request.RefreshToken) < 32 || len(request.RefreshToken) > 2048 {
		router.writeError(c, validationError)
		return
	}
	pair, err := router.sessions.Refresh(c.Request.Context(), request.RefreshToken)
	if err != nil {
		router.writeError(c, mapApplicationError(err))
		return
	}
	c.JSON(http.StatusOK, tokenProjection(pair))
}

// logoutSession 只使用middleware认证身份，不接受payload identity。
func (router *Router) logoutSession(c *gin.Context) {
	authenticated, ok := router.authenticated(c)
	if !ok {
		router.writeError(c, internalError)
		return
	}
	if _, err := router.sessions.Logout(c.Request.Context(), authenticated.AuthContext()); err != nil {
		router.writeError(c, mapApplicationError(err))
		return
	}
	c.Status(http.StatusNoContent)
}

// issueConnectionTicket 只接受channel枚举，endpoint与scope由Session owner决定。
func (router *Router) issueConnectionTicket(c *gin.Context) {
	var request ticketRequest
	if router.decodeOperationJSON(c, &request) != nil {
		router.writeError(c, validationError)
		return
	}
	channel := session.ChannelWSS
	if request.Channel == "TLS_TCP" {
		channel = session.ChannelTLSTCP
	} else if request.Channel != "WSS" {
		router.writeError(c, validationError)
		return
	}
	authenticated, ok := router.authenticated(c)
	if !ok {
		router.writeError(c, internalError)
		return
	}
	ticket, err := router.sessions.IssueTicket(c.Request.Context(), authenticated.AuthContext(), channel)
	if err != nil {
		router.writeError(c, mapApplicationError(err))
		return
	}
	c.JSON(http.StatusCreated, ticketProjection(ticket))
}

// getWorldBootstrap 调用transport-independent world-entry用例。
func (router *Router) getWorldBootstrap(c *gin.Context) {
	authenticated, ok := router.authenticated(c)
	if !ok {
		router.writeError(c, internalError)
		return
	}
	result, err := router.worlds.BootstrapOwnWorld(c.Request.Context(), authenticated)
	if err != nil {
		router.writeError(c, mapApplicationError(err))
		return
	}
	c.JSON(http.StatusOK, bootstrapProjection(result))
}

// acceptVisitInvite 严格解析path、revision与幂等key后调用world-entry。
func (router *Router) acceptVisitInvite(c *gin.Context) {
	visitID, visitErr := visitsession.NewVisitSessionID(c.Param("visitSessionId"))
	inviteID, inviteErr := visitsession.NewInviteID(c.Param("inviteId"))
	var request acceptVisitInviteRequest
	if visitErr != nil || inviteErr != nil || router.decodeOperationJSON(c, &request) != nil || !visitsession.Revision(request.ExpectedRevision).Valid() {
		router.writeError(c, validationError)
		return
	}
	authenticated, ok := router.authenticated(c)
	if !ok {
		router.writeError(c, internalError)
		return
	}
	result, err := router.worlds.AcceptVisitInvite(c.Request.Context(), authenticated, visitID, inviteID, visitsession.Revision(request.ExpectedRevision), c.GetHeader("Idempotency-Key"))
	if err != nil {
		router.writeError(c, mapApplicationError(err))
		return
	}
	c.JSON(http.StatusOK, reservationProjection(result))
}

// issueWorldAdmission 只解析target kind；actor、role、purpose与endpoint均来自owner。
func (router *Router) issueWorldAdmission(c *gin.Context) {
	var request worldAdmissionRequest
	if router.decodeOperationJSON(c, &request) != nil {
		router.writeError(c, validationError)
		return
	}
	var target worldentry.AdmissionTarget
	if request.Kind == "OWN_WORLD" && request.VisitSessionID == "" {
		target.Kind = worldentry.AdmissionTargetOwnWorld
	} else if request.Kind == "VISIT_WORLD" {
		visitID, err := visitsession.NewVisitSessionID(request.VisitSessionID)
		if err != nil {
			router.writeError(c, validationError)
			return
		}
		target = worldentry.AdmissionTarget{Kind: worldentry.AdmissionTargetVisitWorld, VisitSessionID: visitID}
	} else {
		router.writeError(c, validationError)
		return
	}
	authenticated, ok := router.authenticated(c)
	if !ok {
		router.writeError(c, internalError)
		return
	}
	result, err := router.worlds.IssueWorldAdmission(c.Request.Context(), authenticated, target, c.GetHeader("Idempotency-Key"))
	if err != nil {
		router.writeError(c, mapApplicationError(err))
		return
	}
	c.JSON(http.StatusCreated, admissionProjection(result))
}

// issueBattleTicket 只解析 closed target selector 并调用 battle-entry。
//
// Handler 不读取 Host、不解析 runtime identity、不访问 capacity/Redis/control，也不接触 proof key。
func (router *Router) issueBattleTicket(c *gin.Context) {
	var request battleTicketRequest
	if router.decodeOperationJSON(c, &request) != nil {
		router.writeError(c, validationError)
		return
	}
	var target battleentry.Target
	if request.Kind == "OWN_WORLD" {
		if request.VisitSessionID != "" {
			router.writeError(c, validationError)
			return
		}
		target.Kind = battleentry.TargetKindOwnWorld
	} else if request.Kind == "VISIT_WORLD" {
		visitID, err := visitsession.NewVisitSessionID(request.VisitSessionID)
		if err != nil {
			router.writeError(c, validationError)
			return
		}
		target = battleentry.Target{Kind: battleentry.TargetKindVisitWorld, VisitSessionID: visitID}
	} else {
		router.writeError(c, validationError)
		return
	}
	authenticated, ok := router.authenticated(c)
	if !ok {
		router.writeError(c, internalError)
		return
	}
	result, err := router.battles.Issue(c.Request.Context(), authenticated, target, c.GetHeader("Idempotency-Key"))
	if err != nil {
		router.writeError(c, mapApplicationError(err))
		return
	}
	if !result.Valid() {
		router.writeError(c, internalError)
		return
	}
	c.JSON(http.StatusCreated, battleTicketProjection(result))
}

// authenticated 读取middleware私有request scope中的受信值。
func (router *Router) authenticated(c *gin.Context) (session.AuthenticatedSession, bool) {
	value, exists := c.Get(authenticatedSessionKey)
	authenticated, ok := value.(session.AuthenticatedSession)
	return authenticated, exists && ok && authenticated.Valid()
}

// decodeOperationJSON 使用集中operation策略中的body上限解码当前请求。
func (router *Router) decodeOperationJSON(c *gin.Context, target any) error {
	value, exists := c.Get(operationContextKey)
	operation, ok := value.(Operation)
	if !exists || !ok || operation.BodyLimit <= 0 {
		return errors.New("public HTTP operation body policy is missing")
	}
	return decodeJSON(c.Request, operation.BodyLimit, target)
}

// validRegisterRequest 在调用Account owner前执行公开schema与既有领域值对象边界。
func validRegisterRequest(request registerRequest) bool {
	_, usernameErr := account.NewUsername(request.Username)
	_, passwordErr := account.NewRegisterPassword(request.Password)
	_, displayNameErr := account.NewDisplayName(request.DisplayName)
	return usernameErr == nil && passwordErr == nil && displayNameErr == nil
}

// validLoginRequest 在调用Account owner前拒绝不可能通过既有领域边界的凭据形状。
func validLoginRequest(request loginRequest) bool {
	_, usernameErr := account.NewUsername(request.Username)
	_, passwordErr := account.NewLoginPassword(request.Password)
	return usernameErr == nil && passwordErr == nil
}

// writeError 中止chain并编码稳定catalog响应，不包含backend cause。
func (router *Router) writeError(c *gin.Context, item catalogError) {
	requestID, _ := c.Get("request-id")
	value, _ := requestID.(string)
	c.AbortWithStatusJSON(item.Status, errorResponse{Code: item.Code, MessageKey: item.MessageKey, RequestID: value, Retryable: item.Retryable})
}

// bearerCredential 只接受单一Bearer scheme与非空有界opaque value。
func bearerCredential(value string) (string, bool) {
	if !strings.HasPrefix(value, "Bearer ") {
		return "", false
	}
	raw := strings.TrimPrefix(value, "Bearer ")
	return raw, len(raw) >= 32 && len(raw) <= 2048 && !strings.ContainsAny(raw, " \t\r\n")
}

// safeRequestID 限制客户端correlation为安全ASCII且不参与授权。
func safeRequestID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, character := range []byte(value) {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

// safeIdempotencyKey 与OpenAPI/application共同限制原始key且从不记录。
func safeIdempotencyKey(value string) bool {
	if len(value) < 16 || len(value) > 128 {
		return false
	}
	for _, character := range []byte(value) {
		if !idempotencyKeyCharacterAllowed(character) {
			return false
		}
	}
	return true
}

// idempotencyKeyCharacterAllowed 与OpenAPI安全ASCII pattern保持精确一致。
func idempotencyKeyCharacterAllowed(character byte) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '.' || character == '_' || character == ':' || character == '-'
}

// normalizedRemoteIP 提取标准库RemoteAddr中的规范IP，不信任转发header。
func normalizedRemoteIP(value string) (string, bool) {
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
