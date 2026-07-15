package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// minimumPublicBodyBytes 与 OpenAPI PublicLimits 下界一致。
	minimumPublicBodyBytes = 1024
	// maximumPublicBodyBytes 限制公开配置造成的单请求内存放大。
	maximumPublicBodyBytes = 1024 * 1024
	// maximumRateRequests 限制单个 operation 的配置速率。
	maximumRateRequests = 10000
	// maximumRateEntries 限制进程内 limiter 的身份基数。
	maximumRateEntries = 100000
)

// publicHostPattern 限制客户端可见DNS name为无scheme、path、空label或首尾连字符的安全ASCII形式。
var publicHostPattern = regexp.MustCompile(`^(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)(?:\.(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?))*$`)

// publicOperationIDs 是 runtime 与 OpenAPI 必须精确共享的封闭 operation 集合。
var publicOperationIDs = [...]string{
	"getVersion", "getBootstrapConfig", "registerAccount", "loginAccount", "refreshSession",
	"logoutSession", "issueConnectionTicket", "getWorldBootstrap", "acceptVisitInvite", "issueWorldAdmission",
}

// PublicAPI 保存公开 listener、协议投影和业务 policy 的非敏感配置。
type PublicAPI struct {
	// Address 是显式IP与端口组成的公开listener bind地址。
	Address string `yaml:"address"`
	// ReadHeaderTimeout 限制慢速header占用连接的时间。
	ReadHeaderTimeout time.Duration `yaml:"readHeaderTimeout"`
	// ReadTimeout 限制完整请求读取时间。
	ReadTimeout time.Duration `yaml:"readTimeout"`
	// WriteTimeout 限制公开响应写出时间。
	WriteTimeout time.Duration `yaml:"writeTimeout"`
	// IdleTimeout 限制keep-alive空闲连接寿命。
	IdleTimeout time.Duration `yaml:"idleTimeout"`
	// MaxHeaderBytes 限制请求header内存占用。
	MaxHeaderBytes int `yaml:"maxHeaderBytes"`
	// TLS 保存证书文件与private-key secret reference。
	TLS PublicTLS `yaml:"tls"`
	// Endpoints 是只读公开的realtime advertised manifest。
	Endpoints RealtimeEndpoints `yaml:"endpoints"`
	// Limits 是允许返回客户端的协议资源预算。
	Limits PublicLimits `yaml:"limits"`
	// Rates 必须为每个公开operation登记唯一预算。
	Rates map[string]RatePolicy `yaml:"rates"`
	// RateMaxEntries 限制进程内pre/post-auth bucket总数。
	RateMaxEntries int `yaml:"rateMaxEntries"`
	// RateIdleTTL 控制限流bucket惰性回收时间。
	RateIdleTTL time.Duration `yaml:"rateIdleTtl"`
	// Account 定义账号凭据计算资源策略。
	Account AccountPolicy `yaml:"account"`
	// Session 定义session与credential严格TTL顺序。
	Session SessionPolicy `yaml:"session"`
	// PlacementReplayTTL 限制placement幂等证据保留时间。
	PlacementReplayTTL time.Duration `yaml:"placementReplayTtl"`
	// VisitSession 定义访问aggregate与store策略。
	VisitSession VisitSessionPolicy `yaml:"visitSession"`
	// WorldAdmission 定义签发与响应丢失恢复策略。
	WorldAdmission WorldAdmissionPolicy `yaml:"worldAdmission"`
}

// PublicTLS 定义公开 HTTP server TLS material reference。
type PublicTLS struct {
	// Enabled 决定listener是否强制TLS；production必须为true。
	Enabled bool `yaml:"enabled"`
	// CertificateFile 是有界读取的公开certificate chain绝对路径。
	CertificateFile string `yaml:"certificateFile"`
	// PrivateKeySecret 是private key的env:/file: reference。
	PrivateKeySecret string `yaml:"privateKeySecret"`
}

// RealtimeEndpoints 固定 WSS 与 TLS/TCP 两个受信 advertised endpoint。
type RealtimeEndpoints struct {
	// WSS 是control channel受信公开地址。
	WSS Endpoint `yaml:"wss"`
	// TLSTCP 是gameplay channel受信公开地址。
	TLSTCP Endpoint `yaml:"tlsTcp"`
}

// Endpoint 是不含 scheme/path 的公开 host/port。
type Endpoint struct {
	// Host 是IP或规范DNS name，不包含scheme/path。
	Host string `yaml:"host"`
	// Port 是1至65535的公开监听端口。
	Port int `yaml:"port"`
}

// PublicLimits 是 `/v1/config` 可公开的有界资源预算。
type PublicLimits struct {
	// HTTPBodyBytes 是公开HTTP operation允许的全局最大请求体(bytes)。
	HTTPBodyBytes int `yaml:"httpBodyBytes"`
	// RealtimeFrameBytes 是后续realtime listener公开的最大frame(bytes)。
	RealtimeFrameBytes int `yaml:"realtimeFrameBytes"`
}

// RatePolicy 定义单进程token bucket的补充速率与burst下限保护。
type RatePolicy struct {
	// Requests 是Window内持续补充的token数量。
	Requests int `yaml:"requests"`
	// Window 是补充Requests个token的时间窗口。
	Window time.Duration `yaml:"window"`
	// Burst 是单subject可积累的最大token数量。
	Burst int `yaml:"burst"`
}

// AccountPolicy 定义 Argon2id 并发内存门。
type AccountPolicy struct {
	// MaxConcurrentHashes 限制同进程Argon2id内存放大。
	MaxConcurrentHashes int `yaml:"maxConcurrentHashes"`
}

// SessionPolicy 定义 session core 的严格 TTL 顺序。
type SessionPolicy struct {
	// TicketTTL 是一次性realtime资格寿命。
	TicketTTL time.Duration `yaml:"ticketTtl"`
	// AccessTTL 是HTTPS bearer在线验证寿命。
	AccessTTL time.Duration `yaml:"accessTtl"`
	// RefreshTTL 是单枚refresh轮换期限。
	RefreshTTL time.Duration `yaml:"refreshTtl"`
	// SessionTTL 是整个认证lineage绝对寿命。
	SessionTTL time.Duration `yaml:"sessionTtl"`
}

// VisitSessionPolicy 定义 VisitSession domain 与 store 的有界策略。
type VisitSessionPolicy struct {
	// Capacity 是reserved/joined/reconnecting Visitor总上限。
	Capacity int `yaml:"capacity"`
	// SessionLifetime 是aggregate绝对寿命。
	SessionLifetime time.Duration `yaml:"sessionLifetime"`
	// InviteLifetime 是定向邀请最大寿命。
	InviteLifetime time.Duration `yaml:"inviteLifetime"`
	// ReservationLifetime 是accept后等待JOIN的最大时间。
	ReservationLifetime time.Duration `yaml:"reservationLifetime"`
	// OwnerGrace 是Owner断线恢复窗口。
	OwnerGrace time.Duration `yaml:"ownerGrace"`
	// VisitorReconnectGrace 是Visitor断线恢复窗口。
	VisitorReconnectGrace time.Duration `yaml:"visitorReconnectGrace"`
	// ReplayRetention 是aggregate失效后command证据保留时间。
	ReplayRetention time.Duration `yaml:"replayRetention"`
}

// WorldAdmissionPolicy 定义 issuer lifetime、replay 与 secret reference。
type WorldAdmissionPolicy struct {
	// MaximumLifetime 是opaque credential业务寿命上限。
	MaximumLifetime time.Duration `yaml:"maximumLifetime"`
	// ReplayRetention 是业务失效后的response-loss证据窗口。
	ReplayRetention time.Duration `yaml:"replayRetention"`
	// DerivationKeySecret 是HMAC key的env:/file: reference。
	DerivationKeySecret string `yaml:"derivationKeySecret"`
}

// DefaultPublicAPI 返回仅绑定loopback的本地明文配置；production校验会拒绝该TLS策略。
func DefaultPublicAPI() PublicAPI {
	rates := make(map[string]RatePolicy, len(publicOperationIDs))
	for _, operationID := range publicOperationIDs {
		rates[operationID] = RatePolicy{Requests: 120, Window: time.Minute, Burst: 20}
	}
	rates["registerAccount"] = RatePolicy{Requests: 10, Window: time.Minute, Burst: 3}
	rates["loginAccount"] = RatePolicy{Requests: 20, Window: time.Minute, Burst: 5}
	return PublicAPI{
		Address:           "127.0.0.1:8080",
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8192,
		Endpoints: RealtimeEndpoints{
			WSS:    Endpoint{Host: "localhost", Port: 8443},
			TLSTCP: Endpoint{Host: "localhost", Port: 8444},
		},
		Limits:         PublicLimits{HTTPBodyBytes: 4096, RealtimeFrameBytes: 65536},
		Rates:          rates,
		RateMaxEntries: 4096, RateIdleTTL: 10 * time.Minute,
		Account: AccountPolicy{MaxConcurrentHashes: 2},
		Session: SessionPolicy{
			TicketTTL:  30 * time.Second,
			AccessTTL:  15 * time.Minute,
			RefreshTTL: 30 * 24 * time.Hour,
			SessionTTL: 30 * 24 * time.Hour,
		},
		PlacementReplayTTL: 10 * time.Minute,
		VisitSession: VisitSessionPolicy{
			Capacity:              4,
			SessionLifetime:       24 * time.Hour,
			InviteLifetime:        5 * time.Minute,
			ReservationLifetime:   time.Minute,
			OwnerGrace:            2 * time.Minute,
			VisitorReconnectGrace: 2 * time.Minute,
			ReplayRetention:       10 * time.Minute,
		},
		WorldAdmission: WorldAdmissionPolicy{
			MaximumLifetime:     30 * time.Second,
			ReplayRetention:     5 * time.Minute,
			DerivationKeySecret: "env:IHOMELAND_WORLD_ADMISSION_KEY",
		},
	}
}

// validate 在产生listener或storage副作用前交叉校验公开地址、资源上限、完整operation策略与TTL顺序。
func (public PublicAPI) validate(environment string, diagnosticAddress string) error {
	if err := validatePublicAddress(public.Address, environment == "test"); err != nil {
		return err
	}
	if public.Address == diagnosticAddress {
		return errors.New("address must differ from diagnostic.address")
	}
	for name, value := range map[string]time.Duration{"readHeaderTimeout": public.ReadHeaderTimeout, "readTimeout": public.ReadTimeout, "writeTimeout": public.WriteTimeout, "idleTimeout": public.IdleTimeout} {
		if err := validateDuration(name, value); err != nil {
			return err
		}
	}
	if public.MaxHeaderBytes < 1024 || public.MaxHeaderBytes > 65536 {
		return errors.New("maxHeaderBytes must be between 1024 and 65536")
	}
	if err := public.TLS.validate(environment, public.Address); err != nil {
		return err
	}
	if err := public.Endpoints.validate(); err != nil {
		return err
	}
	if public.Limits.HTTPBodyBytes < minimumPublicBodyBytes || public.Limits.HTTPBodyBytes > maximumPublicBodyBytes || public.Limits.RealtimeFrameBytes < minimumPublicBodyBytes || public.Limits.RealtimeFrameBytes > maximumPublicBodyBytes {
		return errors.New("limits must be between 1024 and 1048576 bytes")
	}
	if public.Limits.HTTPBodyBytes < 4096 {
		return errors.New("limits.httpBodyBytes must cover the largest OpenAPI body limit")
	}
	if len(public.Rates) != len(publicOperationIDs) {
		return errors.New("rates must contain every public operation exactly once")
	}
	for _, operationID := range publicOperationIDs {
		policy, exists := public.Rates[operationID]
		if !exists {
			return fmt.Errorf("rates.%s is required", operationID)
		}
		if err := policy.validate(); err != nil {
			return fmt.Errorf("rates.%s: %w", operationID, err)
		}
	}
	if public.RateMaxEntries < 1 || public.RateMaxEntries > maximumRateEntries {
		return fmt.Errorf("rateMaxEntries must be between 1 and %d", maximumRateEntries)
	}
	if public.RateIdleTTL < time.Second || public.RateIdleTTL > 24*time.Hour {
		return errors.New("rateIdleTtl must be between 1s and 24h")
	}
	if public.Account.MaxConcurrentHashes < 1 || public.Account.MaxConcurrentHashes > 16 {
		return errors.New("account.maxConcurrentHashes must be between 1 and 16")
	}
	if !(public.Session.TicketTTL < public.Session.AccessTTL && public.Session.AccessTTL < public.Session.RefreshTTL && public.Session.RefreshTTL <= public.Session.SessionTTL) {
		return errors.New("session TTL order must satisfy ticket < access < refresh <= session")
	}
	if public.PlacementReplayTTL < time.Second || public.PlacementReplayTTL > 24*time.Hour {
		return errors.New("placementReplayTtl must be between 1s and 24h")
	}
	if err := public.VisitSession.validate(); err != nil {
		return err
	}
	return public.WorldAdmission.validate()
}

// validate 强制production使用TLS，并把开发明文例外限制在显式loopback bind地址。
func (policy PublicTLS) validate(environment string, address string) error {
	if !policy.Enabled {
		if environment == "production" {
			return errors.New("tls must be enabled in production")
		}
		if policy.CertificateFile != "" || policy.PrivateKeySecret != "" {
			return errors.New("disabled tls must not contain tls material")
		}
		host, _, _ := net.SplitHostPort(address)
		parsed, err := netip.ParseAddr(host)
		if err != nil || !parsed.IsLoopback() {
			return errors.New("plaintext public API must bind loopback")
		}
		return nil
	}
	if !filepath.IsAbs(policy.CertificateFile) {
		return errors.New("tls.certificateFile must be an absolute path")
	}
	if err := validateSecretReference(policy.PrivateKeySecret); err != nil {
		return fmt.Errorf("tls.privateKeySecret: %w", err)
	}
	return nil
}

// validate 要求两个advertised realtime channel都提供客户端可连接的受限host/port。
func (endpoints RealtimeEndpoints) validate() error {
	if err := endpoints.WSS.validate("endpoints.wss"); err != nil {
		return err
	}
	return endpoints.TLSTCP.validate("endpoints.tlsTcp")
}

// validate 拒绝scheme/path、空DNS label与零端口，避免把未受信请求地址发布给客户端。
func (endpoint Endpoint) validate(name string) error {
	if endpoint.Port < 1 || endpoint.Port > 65535 {
		return fmt.Errorf("%s.port must be between 1 and 65535", name)
	}
	host := strings.ToLower(endpoint.Host)
	if net.ParseIP(host) == nil && (len(host) > 253 || !publicHostPattern.MatchString(host) || strings.Contains(host, "..")) {
		return fmt.Errorf("%s.host must be an IP or valid DNS name", name)
	}
	return nil
}

// validate 限制单operation token补充速率、burst与时间窗口，防止配置关闭资源保护。
func (policy RatePolicy) validate() error {
	if policy.Requests < 1 || policy.Requests > maximumRateRequests || policy.Burst < 1 || policy.Burst > policy.Requests {
		return errors.New("requests/burst are outside the safe range")
	}
	if policy.Window < time.Second || policy.Window > time.Hour {
		return errors.New("window must be between 1s and 1h")
	}
	return nil
}

// validate 约束VisitSession容量及全部运行态deadline，使Redis TTL与领域上限保持有界。
func (policy VisitSessionPolicy) validate() error {
	if policy.Capacity < 1 || policy.Capacity > 32 {
		return errors.New("visitSession.capacity must be between 1 and 32")
	}
	if policy.SessionLifetime < time.Minute || policy.SessionLifetime > 24*time.Hour {
		return errors.New("visitSession.sessionLifetime must be between 1m and 24h")
	}
	if policy.InviteLifetime < time.Second || policy.InviteLifetime > time.Hour {
		return errors.New("visitSession.inviteLifetime must be between 1s and 1h")
	}
	if policy.ReservationLifetime < time.Second || policy.ReservationLifetime > 2*time.Minute {
		return errors.New("visitSession.reservationLifetime must be between 1s and 2m")
	}
	if policy.OwnerGrace < time.Second || policy.OwnerGrace > 5*time.Minute {
		return errors.New("visitSession.ownerGrace must be between 1s and 5m")
	}
	if policy.VisitorReconnectGrace < time.Second || policy.VisitorReconnectGrace > 2*time.Minute {
		return errors.New("visitSession.visitorReconnectGrace must be between 1s and 2m")
	}
	if policy.ReplayRetention < time.Second || policy.ReplayRetention > 24*time.Hour {
		return errors.New("visitSession.replayRetention must be between 1s and 24h")
	}
	return nil
}

// validate 限制短期credential与重放证据寿命，并要求derivation key只通过secret reference提供。
func (policy WorldAdmissionPolicy) validate() error {
	if policy.MaximumLifetime <= 0 || policy.MaximumLifetime > 5*time.Minute || policy.ReplayRetention < 0 || policy.ReplayRetention > 10*time.Minute {
		return errors.New("worldAdmission lifetime policy is invalid")
	}
	if err := validateSecretReference(policy.DerivationKeySecret); err != nil {
		return fmt.Errorf("worldAdmission.derivationKeySecret: %w", err)
	}
	return nil
}

// validatePublicAddress 只接受显式IP bind；端口0仅供test在内核分配动态端口。
func validatePublicAddress(value string, allowZero bool) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return errors.New("address must contain an IP and port")
	}
	if _, err := netip.ParseAddr(host); err != nil {
		return errors.New("address host must be an explicit IP")
	}
	parsed, err := strconv.ParseUint(port, 10, 16)
	if err != nil || (parsed == 0 && !allowZero) {
		return errors.New("address port must be between 1 and 65535; test may use 0")
	}
	return nil
}

// publicAPIEnvironmentOverrides 返回公开listener与secret reference的封闭环境覆盖白名单。
func publicAPIEnvironmentOverrides(config *Config) []environmentOverride {
	return []environmentOverride{
		{key: "IHOMELAND_PUBLIC_ADDRESS", apply: stringSetter(&config.PublicAPI.Address)},
		{key: "IHOMELAND_PUBLIC_TLS_ENABLED", apply: boolSetter(&config.PublicAPI.TLS.Enabled)},
		{key: "IHOMELAND_PUBLIC_TLS_CERTIFICATE_FILE", apply: stringSetter(&config.PublicAPI.TLS.CertificateFile)},
		{key: "IHOMELAND_PUBLIC_TLS_PRIVATE_KEY_SECRET", apply: stringSetter(&config.PublicAPI.TLS.PrivateKeySecret)},
		{key: "IHOMELAND_PUBLIC_WSS_HOST", apply: stringSetter(&config.PublicAPI.Endpoints.WSS.Host)},
		{key: "IHOMELAND_PUBLIC_WSS_PORT", apply: intSetter(&config.PublicAPI.Endpoints.WSS.Port)},
		{key: "IHOMELAND_PUBLIC_TLS_TCP_HOST", apply: stringSetter(&config.PublicAPI.Endpoints.TLSTCP.Host)},
		{key: "IHOMELAND_PUBLIC_TLS_TCP_PORT", apply: intSetter(&config.PublicAPI.Endpoints.TLSTCP.Port)},
		{key: "IHOMELAND_WORLD_ADMISSION_KEY_SECRET", apply: stringSetter(&config.PublicAPI.WorldAdmission.DerivationKeySecret)},
		{key: "IHOMELAND_PASSWORD_HASH_CONCURRENCY", apply: intSetter(&config.PublicAPI.Account.MaxConcurrentHashes)},
	}
}
