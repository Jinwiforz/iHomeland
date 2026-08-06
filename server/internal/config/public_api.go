package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
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
	// fixedDeadlineEntriesPerRuntime 覆盖 2 个 assignment、1 个 session、1 个 Owner grace 与最多 64 个 pending invite deadline。
	fixedDeadlineEntriesPerRuntime = 68
	// maximumWorldRuntimeInstances 限制单进程逻辑WorldInstance registry规模。
	maximumWorldRuntimeInstances = 10000
	// maximumSemanticDeadlineEntries 限制单worker持有的语义任务总数。
	maximumSemanticDeadlineEntries = 1000000
	// maximumGameplayTCPIdleTimeout 为heartbeat失效或异常客户端保留服务端最终回收上限。
	// 其他I/O与生命周期timeout仍使用maximumTimeout，避免把单连接空闲策略扩散为通用长等待。
	maximumGameplayTCPIdleTimeout = 30 * time.Minute
)

// publicHostPattern 限制客户端可见DNS name为无scheme、path、空label或首尾连字符的安全ASCII形式。
var publicHostPattern = regexp.MustCompile(`^(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)(?:\.(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?))*$`)

// publicOperationIDs 是 runtime 与 OpenAPI 必须精确共享的封闭 operation 集合。
var publicOperationIDs = [...]string{
	"getVersion", "getBootstrapConfig", "registerAccount", "loginAccount", "refreshSession",
	"logoutSession", "issueConnectionTicket", "getWorldBootstrap", "acceptVisitInvite", "issueWorldAdmission",
	"issueBattleTicket",
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
	// WorldRuntime 定义进程内WorldInstance与语义deadline资源策略。
	WorldRuntime WorldRuntimePolicy `yaml:"worldRuntime"`
	// VisitSession 定义访问aggregate与store策略。
	VisitSession VisitSessionPolicy `yaml:"visitSession"`
	// WorldAdmission 定义签发与响应丢失恢复策略。
	WorldAdmission WorldAdmissionPolicy `yaml:"worldAdmission"`
	// BattleUDP 定义唯一 C++ listener、ticket 与安全会话的冻结策略。
	BattleUDP BattleUDPPolicy `yaml:"battleUdp"`
	// WebSocketControl 定义共享公开listener上的WSS控制面资源与握手策略。
	WebSocketControl WebSocketControlPolicy `yaml:"websocketControl"`
	// GameplayTCP 定义独立TLS/TCP gameplay listener的认证、并发和生命周期预算。
	GameplayTCP GameplayTCPPolicy `yaml:"gameplayTcp"`
}

// WorldRuntimePolicy 定义单进程PersonalWorld承载与semantic deadline硬预算。
type WorldRuntimePolicy struct {
	// PlacementLeaseTTL 是current assignment单次lease寿命；续约时点由实现确定性派生。
	PlacementLeaseTTL time.Duration `yaml:"placementLeaseTtl"`
	// MaxInstances 限制本进程同时登记的逻辑WorldInstance数量。
	MaxInstances int `yaml:"maxInstances"`
	// DeadlineEntries 限制单一semantic deadline owner持有的任务数量。
	DeadlineEntries int `yaml:"deadlineEntries"`
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

// BattleUDPPolicy 定义安全战斗 UDP listener 与客户端可见 endpoint 的严格配置。
type BattleUDPPolicy struct {
	// BindAddress 是 C++ child 唯一 UDP socket 的显式数字 IP 与端口。
	BindAddress string `yaml:"bindAddress"`
	// Advertised 是只经 HTTPS BattleTicket 下发的受信 endpoint。
	Advertised Endpoint `yaml:"advertised"`
	// DerivationKeySecret 是 BattleTicket deterministic secret 根密钥引用。
	DerivationKeySecret string `yaml:"derivationKeySecret"`
	// WireIdentity 绑定 wire suite source corpus。
	WireIdentity string `yaml:"wireIdentity"`
	// MaximumTicketLifetime 是单枚 BattleTicket 的绝对寿命上限。
	MaximumTicketLifetime time.Duration `yaml:"maximumTicketLifetime"`
	// ReplayRetention 保留 response-loss 幂等证据。
	ReplayRetention time.Duration `yaml:"replayRetention"`
	// CookieRotation 是 stateless cookie key 轮换周期。
	CookieRotation time.Duration `yaml:"cookieRotation"`
	// RekeyInterval 是 authenticated traffic key 时间上限。
	RekeyInterval time.Duration `yaml:"rekeyInterval"`
	// PreviousEpochOverlap 是旧 epoch 唯一允许的接收重叠窗口。
	PreviousEpochOverlap time.Duration `yaml:"previousEpochOverlap"`
	// PreAuthRate 限制单 remote identity 的无状态握手速率。
	PreAuthRate RatePolicy `yaml:"preAuthRate"`
	// NodeQueueItems 是 node ingress/egress hard budget。
	NodeQueueItems int `yaml:"nodeQueueItems"`
	// SessionQueueItems 是每会话 ingress/egress hard budget。
	SessionQueueItems int `yaml:"sessionQueueItems"`
	// KCPQueueItems 是 reliable lane 的消息 hard budget。
	KCPQueueItems int `yaml:"kcpQueueItems"`
	// MaximumMessageExpiry 是 KCP route 允许的最大排队寿命，不覆盖registry中的精确值。
	MaximumMessageExpiry time.Duration `yaml:"maximumMessageExpiry"`
	// DrainTimeout 限制停止公开输入后的 KCP/egress drain。
	DrainTimeout time.Duration `yaml:"drainTimeout"`
}

// WebSocketControlPolicy 定义WSS控制面的固定握手与每进程资源预算。
type WebSocketControlPolicy struct {
	// Path 是公开listener顶层mux唯一允许的WSS upgrade路径。
	Path string `yaml:"path"`
	// Subprotocol 是客户端必须精确协商的协议代际。
	Subprotocol string `yaml:"subprotocol"`
	// AllowedHosts 是upgrade请求允许的规范Host header集合。
	AllowedHosts []string `yaml:"allowedHosts"`
	// AllowedOrigins 是可选Origin header允许的规范origin集合；空集合仍允许native client省略Origin。
	AllowedOrigins []string `yaml:"allowedOrigins"`
	// PreAuthRate 限制单remote identity在认证前的握手速率。
	PreAuthRate RatePolicy `yaml:"preAuthRate"`
	// MaxConnections 限制当前进程active与reserved连接总数。
	MaxConnections int `yaml:"maxConnections"`
	// MaxPerRemote 限制单remote identity的active与reserved连接数。
	MaxPerRemote int `yaml:"maxPerRemote"`
	// MaxPerSession 限制单SessionID的active连接数。
	MaxPerSession int `yaml:"maxPerSession"`
	// MaxPerPlayer 限制单PlayerID跨session的active连接数。
	MaxPerPlayer int `yaml:"maxPerPlayer"`
	// MaxRemoteEntries 限制pre-auth limiter持有的remote identity数量。
	MaxRemoteEntries int `yaml:"maxRemoteEntries"`
	// RemoteIdleTTL 是remote limiter entry惰性回收期限。
	RemoteIdleTTL time.Duration `yaml:"remoteIdleTtl"`
	// QueueItems 限制每连接待发送envelope数量。
	QueueItems int `yaml:"queueItems"`
	// QueueBytes 限制每连接待发送encoded bytes总量。
	QueueBytes int `yaml:"queueBytes"`
	// WriteTimeout 限制单次binary message写出。
	WriteTimeout time.Duration `yaml:"writeTimeout"`
	// PingInterval 控制server主动存活探测频率。
	PingInterval time.Duration `yaml:"pingInterval"`
	// PongTimeout 限制单次ping等待peer响应的时间。
	PongTimeout time.Duration `yaml:"pongTimeout"`
	// IdleTimeout 限制连接长期没有成功I/O的寿命。
	IdleTimeout time.Duration `yaml:"idleTimeout"`
	// CloseTimeout 限制close handshake与连接任务回收。
	CloseTimeout time.Duration `yaml:"closeTimeout"`
}

// GameplayTCPPolicy 定义TLS/TCP gameplay通道的固定协议与每进程资源预算。
type GameplayTCPPolicy struct {
	// Address 是显式IP与端口组成的独立gameplay listener bind地址。
	Address string `yaml:"address"`
	// PreAuthRate 限制单remote identity在认证前提交preface的速率。
	PreAuthRate RatePolicy `yaml:"preAuthRate"`
	// MaxConnections 限制当前进程reserved与active gameplay连接总数。
	MaxConnections int `yaml:"maxConnections"`
	// MaxPerRemote 限制单remote identity的reserved与active连接数。
	MaxPerRemote int `yaml:"maxPerRemote"`
	// MaxPerSession 限制单SessionID的active连接数。
	MaxPerSession int `yaml:"maxPerSession"`
	// MaxPerPlayer 限制单PlayerID跨session的active连接数。
	MaxPerPlayer int `yaml:"maxPerPlayer"`
	// MaxPerTarget 限制单PersonalWorldID或VisitSessionID的active连接数。
	MaxPerTarget int `yaml:"maxPerTarget"`
	// MaxRemoteEntries 限制pre-auth limiter持有的remote identity数量。
	MaxRemoteEntries int `yaml:"maxRemoteEntries"`
	// RemoteIdleTTL 是remote limiter entry的惰性回收期限。
	RemoteIdleTTL time.Duration `yaml:"remoteIdleTtl"`
	// HandshakeBytes 限制preface length-prefix声明和认证缓冲区(bytes)。
	HandshakeBytes int `yaml:"handshakeBytes"`
	// ReadBatchFrames 限制单次reader循环连续处理的frame数量。
	ReadBatchFrames int `yaml:"readBatchFrames"`
	// QueueItems 限制每连接待发送response或push的条目数。
	QueueItems int `yaml:"queueItems"`
	// QueueBytes 限制每连接待发送完整encoded frame总量(bytes)。
	QueueBytes int `yaml:"queueBytes"`
	// HandshakeTimeout 限制TLS握手与authentication preface完成时间。
	HandshakeTimeout time.Duration `yaml:"handshakeTimeout"`
	// ReadTimeout 限制单个完整frame的读取时间。
	ReadTimeout time.Duration `yaml:"readTimeout"`
	// WriteTimeout 限制单个完整frame的写出时间。
	WriteTimeout time.Duration `yaml:"writeTimeout"`
	// KeepAlive 是操作系统TCP keepalive探测周期。
	KeepAlive time.Duration `yaml:"keepAlive"`
	// IdleTimeout 限制连接没有成功合法C2S I/O（包括heartbeat）的寿命。
	IdleTimeout time.Duration `yaml:"idleTimeout"`
	// CloseTimeout 限制连接任务与发送队列的关闭等待时间。
	CloseTimeout time.Duration `yaml:"closeTimeout"`
	// ShutdownTimeout 限制listener停止接受后全部连接的共享关闭时间。
	ShutdownTimeout time.Duration `yaml:"shutdownTimeout"`
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
			WSS:    Endpoint{Host: "127.0.0.1", Port: 8080},
			TLSTCP: Endpoint{Host: "127.0.0.1", Port: 8444},
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
		WorldRuntime: WorldRuntimePolicy{
			PlacementLeaseTTL: 30 * time.Second,
			MaxInstances:      512,
			DeadlineEntries:   65536,
		},
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
		BattleUDP: BattleUDPPolicy{
			BindAddress: "127.0.0.1:58445", Advertised: Endpoint{Host: "127.0.0.1", Port: 58445},
			DerivationKeySecret: "env:IHOMELAND_BATTLE_DERIVATION_KEY",
			WireIdentity:        "9a40facbb23aafc556d38b403c8f8b1e264e0f2d9414e32da434b11d07554432", MaximumTicketLifetime: 30 * time.Second,
			ReplayRetention: 5 * time.Minute, CookieRotation: 30 * time.Second,
			RekeyInterval: 10 * time.Minute, PreviousEpochOverlap: 3 * time.Second,
			PreAuthRate:    RatePolicy{Requests: 60, Window: time.Minute, Burst: 10},
			NodeQueueItems: 256, SessionQueueItems: 256, KCPQueueItems: 64,
			MaximumMessageExpiry: 2250 * time.Millisecond, DrainTimeout: 3 * time.Second,
		},
		WebSocketControl: WebSocketControlPolicy{
			Path: "/v1/control", Subprotocol: "ihomeland.control.v1",
			AllowedHosts:   []string{"127.0.0.1:8080", "localhost:8080"},
			PreAuthRate:    RatePolicy{Requests: 60, Window: time.Minute, Burst: 10},
			MaxConnections: 4096, MaxPerRemote: 32, MaxPerSession: 4, MaxPerPlayer: 8,
			MaxRemoteEntries: 4096, RemoteIdleTTL: 10 * time.Minute,
			QueueItems: 64, QueueBytes: 1024 * 1024,
			WriteTimeout: 5 * time.Second, PingInterval: 15 * time.Second, PongTimeout: 10 * time.Second,
			IdleTimeout: 45 * time.Second, CloseTimeout: 3 * time.Second,
		},
		GameplayTCP: GameplayTCPPolicy{
			Address:        "127.0.0.1:8444",
			PreAuthRate:    RatePolicy{Requests: 60, Window: time.Minute, Burst: 10},
			MaxConnections: 4096, MaxPerRemote: 32, MaxPerSession: 4, MaxPerPlayer: 8, MaxPerTarget: 64,
			MaxRemoteEntries: 4096, RemoteIdleTTL: 10 * time.Minute,
			HandshakeBytes: 8192, ReadBatchFrames: 16,
			QueueItems: 64, QueueBytes: 1024 * 1024,
			HandshakeTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 5 * time.Second,
			KeepAlive: 15 * time.Second, IdleTimeout: 45 * time.Second, CloseTimeout: 3 * time.Second, ShutdownTimeout: 8 * time.Second,
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
	if err := public.WorldRuntime.validate(public.VisitSession.Capacity); err != nil {
		return err
	}
	if err := public.WorldAdmission.validate(); err != nil {
		return err
	}
	if err := public.BattleUDP.validate(environment, public.Address, diagnosticAddress, public.GameplayTCP.Address); err != nil {
		return err
	}
	if err := public.WebSocketControl.Validate(public.Limits.RealtimeFrameBytes); err != nil {
		return err
	}
	return public.GameplayTCP.Validate(public.Limits.RealtimeFrameBytes, public.Address, diagnosticAddress, public.TLS.Enabled, environment == "test")
}

// validate 冻结 B0.5 profile，并阻止 UDP 与 HTTP、diagnostic 或 TLS/TCP owner 共享端口。
func (policy BattleUDPPolicy) validate(environment string, publicAddress string, diagnosticAddress string, gameplayAddress string) error {
	host, portText, err := net.SplitHostPort(policy.BindAddress)
	if err != nil {
		return errors.New("battleUdp.bindAddress must contain an explicit IP and port")
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return errors.New("battleUdp.bindAddress host must be an explicit numeric IP")
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 && environment != "test" {
		return errors.New("battleUdp.bindAddress port 0 is allowed only in test")
	}
	if port == 0 && (!address.IsLoopback() || policy.Advertised.Host != "127.0.0.1" || policy.Advertised.Port != 0) {
		return errors.New("battleUdp test port 0 requires 127.0.0.1 bind and advertised endpoint")
	}
	if port != 0 {
		for name, other := range map[string]string{"publicApi.address": publicAddress, "diagnostic.address": diagnosticAddress, "gameplayTcp.address": gameplayAddress} {
			_, otherPort, splitErr := net.SplitHostPort(other)
			if splitErr == nil && otherPort == portText {
				return fmt.Errorf("battleUdp.bindAddress port must differ from %s", name)
			}
		}
	}
	if policy.Advertised.Port < 0 || policy.Advertised.Port > 65535 ||
		(policy.Advertised.Port == 0 && environment != "test") ||
		net.ParseIP(policy.Advertised.Host) == nil {
		return errors.New("battleUdp.advertised must contain an explicit trusted host and port")
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(policy.WireIdentity) {
		return errors.New("battleUdp.wireIdentity must be lowercase SHA-256")
	}
	if err := validateSecretReference(policy.DerivationKeySecret); err != nil {
		return fmt.Errorf("battleUdp.derivationKeySecret: %w", err)
	}
	if policy.MaximumTicketLifetime < time.Second || policy.MaximumTicketLifetime > 2*time.Minute ||
		policy.ReplayRetention < policy.MaximumTicketLifetime || policy.ReplayRetention > time.Hour {
		return errors.New("battleUdp ticket and replay deadlines are invalid")
	}
	if policy.CookieRotation != 30*time.Second || policy.RekeyInterval != 10*time.Minute ||
		policy.PreviousEpochOverlap != 3*time.Second || policy.MaximumMessageExpiry != 2250*time.Millisecond ||
		policy.NodeQueueItems != 256 || policy.SessionQueueItems != 256 || policy.KCPQueueItems != 64 {
		return errors.New("battleUdp must use the qualified B0.5 crypto and queue profile")
	}
	if err := policy.PreAuthRate.validate(); err != nil {
		return fmt.Errorf("battleUdp.preAuthRate: %w", err)
	}
	if policy.DrainTimeout < policy.MaximumMessageExpiry || policy.DrainTimeout > 10*time.Second {
		return errors.New("battleUdp.drainTimeout must be between message expiry and 10s")
	}
	return nil
}

// validate 约束runtime、lease与deadline容量，并校验queue可覆盖所有可达业务deadline。
func (policy WorldRuntimePolicy) validate(visitCapacity int) error {
	if policy.PlacementLeaseTTL < time.Second || policy.PlacementLeaseTTL > 5*time.Minute {
		return errors.New("worldRuntime.placementLeaseTtl must be between 1s and 5m")
	}
	if policy.MaxInstances < 1 || policy.MaxInstances > maximumWorldRuntimeInstances {
		return fmt.Errorf("worldRuntime.maxInstances must be between 1 and %d", maximumWorldRuntimeInstances)
	}
	minimumEntries := policy.MaxInstances * (fixedDeadlineEntriesPerRuntime + visitCapacity)
	if policy.DeadlineEntries < minimumEntries || policy.DeadlineEntries > maximumSemanticDeadlineEntries {
		return fmt.Errorf("worldRuntime.deadlineEntries must be between %d and %d", minimumEntries, maximumSemanticDeadlineEntries)
	}
	return nil
}

// Validate 约束gameplay listener地址、连接、frame、队列和deadline，且不产生任何外部副作用。
func (policy GameplayTCPPolicy) Validate(frameBytes int, publicAddress string, diagnosticAddress string, tlsEnabled bool, allowZero bool) error {
	if err := validatePublicAddress(policy.Address, allowZero); err != nil {
		return fmt.Errorf("gameplayTcp.%w", err)
	}
	gameplayHost, gameplayPort, _ := net.SplitHostPort(policy.Address)
	for name, address := range map[string]string{"publicApi.address": publicAddress, "diagnostic.address": diagnosticAddress} {
		_, port, err := net.SplitHostPort(address)
		if err == nil && gameplayPort != "0" && gameplayPort == port {
			return fmt.Errorf("gameplayTcp.address port must differ from %s", name)
		}
	}
	if !tlsEnabled {
		parsed, err := netip.ParseAddr(gameplayHost)
		if err != nil || !parsed.IsLoopback() {
			return errors.New("plaintext gameplayTcp must bind loopback")
		}
	}
	if frameBytes < minimumPublicBodyBytes || frameBytes > maximumPublicBodyBytes {
		return errors.New("gameplayTcp frame budget is invalid")
	}
	if err := policy.PreAuthRate.validate(); err != nil {
		return fmt.Errorf("gameplayTcp.preAuthRate: %w", err)
	}
	if policy.MaxConnections < 1 || policy.MaxConnections > 100000 || policy.MaxPerRemote < 1 || policy.MaxPerRemote > policy.MaxConnections ||
		policy.MaxPerSession < 1 || policy.MaxPerSession > policy.MaxConnections || policy.MaxPerPlayer < 1 || policy.MaxPerPlayer > policy.MaxConnections ||
		policy.MaxPerTarget < 1 || policy.MaxPerTarget > policy.MaxConnections {
		return errors.New("gameplayTcp connection limits are invalid")
	}
	if policy.MaxRemoteEntries < 1 || policy.MaxRemoteEntries > maximumRateEntries || policy.RemoteIdleTTL < time.Second || policy.RemoteIdleTTL > 24*time.Hour {
		return errors.New("gameplayTcp remote limiter bounds are invalid")
	}
	if policy.HandshakeBytes < 1024 || policy.HandshakeBytes > 64*1024 || policy.HandshakeBytes > frameBytes {
		return errors.New("gameplayTcp handshake budget is invalid")
	}
	if policy.ReadBatchFrames < 1 || policy.ReadBatchFrames > 256 {
		return errors.New("gameplayTcp read batch budget is invalid")
	}
	if policy.QueueItems < 1 || policy.QueueItems > 1024 || policy.QueueBytes < frameBytes+4 || policy.QueueBytes > 16*1024*1024 {
		return errors.New("gameplayTcp queue budget is invalid")
	}
	for name, value := range map[string]time.Duration{
		"handshakeTimeout": policy.HandshakeTimeout, "readTimeout": policy.ReadTimeout, "writeTimeout": policy.WriteTimeout,
		"keepAlive": policy.KeepAlive, "closeTimeout": policy.CloseTimeout, "shutdownTimeout": policy.ShutdownTimeout,
	} {
		if err := validateDuration("gameplayTcp."+name, value); err != nil {
			return err
		}
	}
	if policy.IdleTimeout < minimumTimeout || policy.IdleTimeout > maximumGameplayTCPIdleTimeout {
		return fmt.Errorf("gameplayTcp.idleTimeout must be between %s and %s", minimumTimeout, maximumGameplayTCPIdleTimeout)
	}
	if policy.WriteTimeout > policy.IdleTimeout || policy.ReadTimeout > policy.IdleTimeout || policy.KeepAlive >= policy.IdleTimeout ||
		policy.CloseTimeout > policy.ShutdownTimeout || policy.ShutdownTimeout > policy.IdleTimeout {
		return errors.New("gameplayTcp deadlines are inconsistent")
	}
	return nil
}

// Validate 冻结WSS握手契约并限制连接、队列、限流与deadline资源。
//
// Transport在构图时也调用本入口，确保直接构造不会维护第二套较宽松校验。
func (policy WebSocketControlPolicy) Validate(frameBytes int) error {
	if frameBytes < minimumPublicBodyBytes || frameBytes > maximumPublicBodyBytes {
		return errors.New("websocketControl frame budget is invalid")
	}
	if policy.Path != "/v1/control" || policy.Subprotocol != "ihomeland.control.v1" {
		return errors.New("websocketControl path and subprotocol must use the frozen contract")
	}
	if len(policy.AllowedHosts) < 1 || len(policy.AllowedHosts) > 32 {
		return errors.New("websocketControl.allowedHosts must contain 1-32 entries")
	}
	if err := validateUniqueStrings("websocketControl.allowedHosts", policy.AllowedHosts, validWebSocketHost); err != nil {
		return err
	}
	if len(policy.AllowedOrigins) > 32 {
		return errors.New("websocketControl.allowedOrigins must contain at most 32 entries")
	}
	if err := validateUniqueStrings("websocketControl.allowedOrigins", policy.AllowedOrigins, validWebSocketOrigin); err != nil {
		return err
	}
	if err := policy.PreAuthRate.validate(); err != nil {
		return fmt.Errorf("websocketControl.preAuthRate: %w", err)
	}
	if policy.MaxConnections < 1 || policy.MaxConnections > 100000 || policy.MaxPerRemote < 1 || policy.MaxPerRemote > policy.MaxConnections ||
		policy.MaxPerSession < 1 || policy.MaxPerSession > policy.MaxConnections || policy.MaxPerPlayer < 1 || policy.MaxPerPlayer > policy.MaxConnections {
		return errors.New("websocketControl connection limits are invalid")
	}
	if policy.MaxRemoteEntries < 1 || policy.MaxRemoteEntries > maximumRateEntries || policy.RemoteIdleTTL < time.Second || policy.RemoteIdleTTL > 24*time.Hour {
		return errors.New("websocketControl remote limiter bounds are invalid")
	}
	if policy.QueueItems < 1 || policy.QueueItems > 1024 || policy.QueueBytes < frameBytes || policy.QueueBytes > 16*1024*1024 {
		return errors.New("websocketControl queue budget is invalid")
	}
	for name, value := range map[string]time.Duration{"writeTimeout": policy.WriteTimeout, "pingInterval": policy.PingInterval, "pongTimeout": policy.PongTimeout, "idleTimeout": policy.IdleTimeout, "closeTimeout": policy.CloseTimeout} {
		if err := validateDuration("websocketControl."+name, value); err != nil {
			return err
		}
	}
	if policy.WriteTimeout > policy.PongTimeout || policy.PongTimeout >= policy.IdleTimeout || policy.PingInterval >= policy.IdleTimeout || policy.CloseTimeout > policy.IdleTimeout {
		return errors.New("websocketControl deadlines are inconsistent")
	}
	return nil
}

// validateUniqueStrings 拒绝空白、非规范值和重复allowlist entry。
func validateUniqueStrings(name string, values []string, valid func(string) bool) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != strings.TrimSpace(value) || value != strings.ToLower(value) || !valid(value) {
			return fmt.Errorf("%s contains an invalid entry", name)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("%s contains a duplicate entry", name)
		}
		seen[value] = struct{}{}
	}
	return nil
}

// validWebSocketHost 校验规范host[:port]且禁止scheme、path与userinfo。
func validWebSocketHost(value string) bool {
	host := value
	if strings.Contains(value, ":") {
		parsedHost, port, err := net.SplitHostPort(value)
		if err != nil {
			return false
		}
		parsedPort, err := strconv.ParseUint(port, 10, 16)
		if err != nil || parsedPort == 0 {
			return false
		}
		host = parsedHost
	}
	return net.ParseIP(host) != nil || (len(host) <= 253 && publicHostPattern.MatchString(host) && !strings.Contains(host, ".."))
}

// validWebSocketOrigin 只接受无path/query/fragment/userinfo的http(s) origin。
func validWebSocketOrigin(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return validWebSocketHost(parsed.Host)
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
		{key: "IHOMELAND_BATTLE_UDP_BIND_ADDRESS", apply: stringSetter(&config.PublicAPI.BattleUDP.BindAddress)},
		{key: "IHOMELAND_BATTLE_UDP_ADVERTISED_HOST", apply: stringSetter(&config.PublicAPI.BattleUDP.Advertised.Host)},
		{key: "IHOMELAND_BATTLE_UDP_ADVERTISED_PORT", apply: intSetter(&config.PublicAPI.BattleUDP.Advertised.Port)},
		{key: "IHOMELAND_BATTLE_DERIVATION_KEY_SECRET", apply: stringSetter(&config.PublicAPI.BattleUDP.DerivationKeySecret)},
		{key: "IHOMELAND_PASSWORD_HASH_CONCURRENCY", apply: intSetter(&config.PublicAPI.Account.MaxConcurrentHashes)},
		{key: "IHOMELAND_PLACEMENT_LEASE_TTL", apply: durationSetter(&config.PublicAPI.WorldRuntime.PlacementLeaseTTL)},
		{key: "IHOMELAND_WORLD_RUNTIME_MAX_INSTANCES", apply: intSetter(&config.PublicAPI.WorldRuntime.MaxInstances)},
		{key: "IHOMELAND_SEMANTIC_DEADLINE_ENTRIES", apply: intSetter(&config.PublicAPI.WorldRuntime.DeadlineEntries)},
		{key: "IHOMELAND_WSS_ALLOWED_HOSTS", apply: stringListSetter(&config.PublicAPI.WebSocketControl.AllowedHosts)},
		{key: "IHOMELAND_WSS_ALLOWED_ORIGINS", apply: stringListSetter(&config.PublicAPI.WebSocketControl.AllowedOrigins)},
		{key: "IHOMELAND_WSS_MAX_CONNECTIONS", apply: intSetter(&config.PublicAPI.WebSocketControl.MaxConnections)},
		{key: "IHOMELAND_WSS_MAX_PER_REMOTE", apply: intSetter(&config.PublicAPI.WebSocketControl.MaxPerRemote)},
		{key: "IHOMELAND_WSS_MAX_PER_SESSION", apply: intSetter(&config.PublicAPI.WebSocketControl.MaxPerSession)},
		{key: "IHOMELAND_WSS_MAX_PER_PLAYER", apply: intSetter(&config.PublicAPI.WebSocketControl.MaxPerPlayer)},
		{key: "IHOMELAND_WSS_QUEUE_ITEMS", apply: intSetter(&config.PublicAPI.WebSocketControl.QueueItems)},
		{key: "IHOMELAND_WSS_QUEUE_BYTES", apply: intSetter(&config.PublicAPI.WebSocketControl.QueueBytes)},
		{key: "IHOMELAND_WSS_WRITE_TIMEOUT", apply: durationSetter(&config.PublicAPI.WebSocketControl.WriteTimeout)},
		{key: "IHOMELAND_WSS_PING_INTERVAL", apply: durationSetter(&config.PublicAPI.WebSocketControl.PingInterval)},
		{key: "IHOMELAND_WSS_PONG_TIMEOUT", apply: durationSetter(&config.PublicAPI.WebSocketControl.PongTimeout)},
		{key: "IHOMELAND_WSS_IDLE_TIMEOUT", apply: durationSetter(&config.PublicAPI.WebSocketControl.IdleTimeout)},
		{key: "IHOMELAND_WSS_CLOSE_TIMEOUT", apply: durationSetter(&config.PublicAPI.WebSocketControl.CloseTimeout)},
		{key: "IHOMELAND_GAMEPLAY_TCP_ADDRESS", apply: stringSetter(&config.PublicAPI.GameplayTCP.Address)},
		{key: "IHOMELAND_GAMEPLAY_TCP_MAX_CONNECTIONS", apply: intSetter(&config.PublicAPI.GameplayTCP.MaxConnections)},
		{key: "IHOMELAND_GAMEPLAY_TCP_MAX_PER_REMOTE", apply: intSetter(&config.PublicAPI.GameplayTCP.MaxPerRemote)},
		{key: "IHOMELAND_GAMEPLAY_TCP_MAX_PER_SESSION", apply: intSetter(&config.PublicAPI.GameplayTCP.MaxPerSession)},
		{key: "IHOMELAND_GAMEPLAY_TCP_MAX_PER_PLAYER", apply: intSetter(&config.PublicAPI.GameplayTCP.MaxPerPlayer)},
		{key: "IHOMELAND_GAMEPLAY_TCP_MAX_PER_TARGET", apply: intSetter(&config.PublicAPI.GameplayTCP.MaxPerTarget)},
		{key: "IHOMELAND_GAMEPLAY_TCP_QUEUE_ITEMS", apply: intSetter(&config.PublicAPI.GameplayTCP.QueueItems)},
		{key: "IHOMELAND_GAMEPLAY_TCP_QUEUE_BYTES", apply: intSetter(&config.PublicAPI.GameplayTCP.QueueBytes)},
		{key: "IHOMELAND_GAMEPLAY_TCP_HANDSHAKE_TIMEOUT", apply: durationSetter(&config.PublicAPI.GameplayTCP.HandshakeTimeout)},
		{key: "IHOMELAND_GAMEPLAY_TCP_READ_TIMEOUT", apply: durationSetter(&config.PublicAPI.GameplayTCP.ReadTimeout)},
		{key: "IHOMELAND_GAMEPLAY_TCP_WRITE_TIMEOUT", apply: durationSetter(&config.PublicAPI.GameplayTCP.WriteTimeout)},
		{key: "IHOMELAND_GAMEPLAY_TCP_KEEPALIVE", apply: durationSetter(&config.PublicAPI.GameplayTCP.KeepAlive)},
		{key: "IHOMELAND_GAMEPLAY_TCP_IDLE_TIMEOUT", apply: durationSetter(&config.PublicAPI.GameplayTCP.IdleTimeout)},
		{key: "IHOMELAND_GAMEPLAY_TCP_CLOSE_TIMEOUT", apply: durationSetter(&config.PublicAPI.GameplayTCP.CloseTimeout)},
		{key: "IHOMELAND_GAMEPLAY_TCP_SHUTDOWN_TIMEOUT", apply: durationSetter(&config.PublicAPI.GameplayTCP.ShutdownTimeout)},
	}
}

// stringListSetter 按逗号解析显式allowlist覆盖；空值表示空集合而不是单个空entry。
func stringListSetter(target *[]string) func(string) error {
	return func(value string) error {
		if value == "" {
			*target = nil
			return nil
		}
		*target = strings.Split(value, ",")
		return nil
	}
}
