package session

import (
	"errors"
	"log/slog"
	"net/netip"
	"sort"
	"strings"
	"time"
)

// maximumIdentifierBytes 限制身份索引、日志关联值和未来存储 key 的大小。
const maximumIdentifierBytes = 128

// SessionID 标识单个可独立失效的认证 session。
//
// 值由服务端 IDGenerator 创建，不接受客户端选择；零值无效。
type SessionID struct {
	// value 保存经过边界校验的 opaque identifier。
	value string
}

// NewSessionID 校验服务端生成的 identifier 并构造 SessionID。
func NewSessionID(value string) (SessionID, error) {
	if err := validateIdentifier("session id", value); err != nil {
		return SessionID{}, err
	}
	return SessionID{value: value}, nil
}

// String 返回可用于存储索引和安全关联的非凭据 identifier。
func (id SessionID) String() string { return id.value }

// Valid 报告 SessionID 是否经过非零构造。
func (id SessionID) Valid() bool { return id.value != "" }

// Principal 保存上游账号域已经确认的账号与玩家身份。
//
// Session core 只绑定该身份，不判断凭据、封禁状态或账号是否存在。
type Principal struct {
	// accountID 是账号域的不可变身份。
	accountID string
	// playerID 是业务授权和房间 membership 使用的玩家身份。
	playerID string
}

// principalPlaceholder 防止账号与玩家身份通过默认格式化泄漏。
const principalPlaceholder = "[REDACTED_PRINCIPAL]"

// sessionIDPrefix 将通用随机 ID 材料归入 session namespace，避免不同实体混用。
const sessionIDPrefix = "ses_"

// NewPrincipal 校验上游提供的稳定账号与玩家 identifier。
func NewPrincipal(accountID string, playerID string) (Principal, error) {
	if err := validateIdentifier("account id", accountID); err != nil {
		return Principal{}, err
	}
	if err := validateIdentifier("player id", playerID); err != nil {
		return Principal{}, err
	}
	return Principal{accountID: accountID, playerID: playerID}, nil
}

// AccountID 返回账号域身份；调用方不得用 payload 值覆盖它。
func (principal Principal) AccountID() string { return principal.accountID }

// PlayerID 返回业务玩家身份；该值来自认证 session 而非客户端声明。
func (principal Principal) PlayerID() string { return principal.playerID }

// Valid 报告 Principal 是否包含完整账号和玩家身份。
func (principal Principal) Valid() bool { return principal.accountID != "" && principal.playerID != "" }

// String 避免身份字段被默认格式化写入日志或错误。
func (Principal) String() string { return principalPlaceholder }

// GoString 防止 `%#v` 展开 Principal 私有字段。
func (Principal) GoString() string { return principalPlaceholder }

// LogValue 让 slog 只记录固定身份占位符。
func (Principal) LogValue() slog.Value { return slog.StringValue(principalPlaceholder) }

// Epoch 是使旧 token、ticket 与 connection context 全部失效的单调屏障。
type Epoch uint64

// InitialEpoch 是每个新 session 唯一允许的起始 epoch。
const InitialEpoch Epoch = 1

// Next 返回下一 epoch，并在 uint64 上界拒绝回绕。
func (epoch Epoch) Next() (Epoch, error) {
	if epoch == 0 || epoch == ^Epoch(0) {
		return 0, errors.New("session epoch cannot advance")
	}
	return epoch + 1, nil
}

// Valid 报告 epoch 是否满足从 1 开始的不变量。
func (epoch Epoch) Valid() bool { return epoch >= InitialEpoch }

// Status 表达 session 是否仍可用于认证。
type Status uint8

const (
	// StatusUnspecified 是禁止进入 store 或 AuthContext 的零值。
	StatusUnspecified Status = iota
	// StatusActive 允许未过期且 epoch 匹配的凭据认证。
	StatusActive
	// StatusInvalidated 表示 session 已永久撤销，不能通过 refresh 恢复。
	StatusInvalidated
)

// Channel 标识认证上下文所在的唯一入口边界。
type Channel uint8

const (
	// ChannelUnspecified 防止凭据成为与通道无关的万能资格。
	ChannelUnspecified Channel = iota
	// ChannelHTTPS 表示 access token 认证的启动与账号面。
	ChannelHTTPS
	// ChannelWSS 表示 WebSocket 带外控制面。
	ChannelWSS
	// ChannelTLSTCP 表示 TLS/TCP 权威可靠业务面。
	ChannelTLSTCP
)

// Valid 报告 channel 是否属于当前服务端支持的封闭集合。
func (channel Channel) Valid() bool {
	return channel == ChannelHTTPS || channel == ChannelWSS || channel == ChannelTLSTCP
}

// Scope 是服务端授予 AuthContext 的最小 capability。
type Scope uint8

const (
	// ScopeUnspecified 是不能被授予的零值。
	ScopeUnspecified Scope = iota
	// ScopeControl 允许建立 control-plane connection。
	ScopeControl
	// ScopeGameplay 只允许建立可靠业务连接，具体 mutation 仍需 admission 与领域授权。
	ScopeGameplay
)

// Valid 报告 scope 是否属于当前契约支持的封闭集合。
func (scope Scope) Valid() bool {
	return scope == ScopeControl || scope == ScopeGameplay
}

// ScopeSet 持有去重且稳定排序的只读 capability 集合。
//
// 内部 slice 永不直接返回，防止 adapter 或业务 payload 在认证后扩权。
type ScopeSet struct {
	// values 按 Scope 数值升序保存唯一成员。
	values []Scope
}

// NewScopeSet 校验、去重并规范化 scopes；空集合只适用于 HTTPS AuthContext。
func NewScopeSet(values ...Scope) (ScopeSet, error) {
	unique := make(map[Scope]struct{}, len(values))
	for _, value := range values {
		if !value.Valid() {
			return ScopeSet{}, errors.New("scope set contains unsupported scope")
		}
		unique[value] = struct{}{}
	}
	normalized := make([]Scope, 0, len(unique))
	for value := range unique {
		normalized = append(normalized, value)
	}
	sort.Slice(normalized, func(left int, right int) bool { return normalized[left] < normalized[right] })
	return ScopeSet{values: normalized}, nil
}

// Has 报告集合是否包含指定 capability。
func (set ScopeSet) Has(scope Scope) bool {
	index := sort.Search(len(set.values), func(index int) bool { return set.values[index] >= scope })
	return index < len(set.values) && set.values[index] == scope
}

// Values 返回规范化副本，调用方修改结果不会改变 AuthContext。
func (set ScopeSet) Values() []Scope { return append([]Scope(nil), set.values...) }

// Len 返回 capability 数量，用于强制非 HTTPS context 至少拥有一个 scope。
func (set ScopeSet) Len() int { return len(set.values) }

// Endpoint 是 ticket 绑定的受信网络目标。
type Endpoint struct {
	// channel 决定 listener handshake 和允许的 scope policy。
	channel Channel
	// host 是规范化小写 IP 或 DNS name，不包含 scheme 和路径。
	host string
	// port 是范围 1-65535 的 listener 端口。
	port uint16
}

// NewEndpoint 校验受信 endpoint provider 返回的 channel、host 与 port。
func NewEndpoint(channel Channel, host string, port uint16) (Endpoint, error) {
	if channel != ChannelWSS && channel != ChannelTLSTCP {
		return Endpoint{}, errors.New("endpoint requires a realtime channel")
	}
	normalizedHost := strings.ToLower(strings.TrimSuffix(host, "."))
	if !validHost(normalizedHost) {
		return Endpoint{}, errors.New("endpoint host is invalid")
	}
	if port == 0 {
		return Endpoint{}, errors.New("endpoint port must be between 1 and 65535")
	}
	return Endpoint{channel: channel, host: normalizedHost, port: port}, nil
}

// Channel 返回 endpoint 唯一允许的 realtime channel。
func (endpoint Endpoint) Channel() Channel { return endpoint.channel }

// Host 返回不含凭据、scheme 或路径的规范化 host。
func (endpoint Endpoint) Host() string { return endpoint.host }

// Port 返回 listener 端口。
func (endpoint Endpoint) Port() uint16 { return endpoint.port }

// Equal 使用全部绑定字段比较两个 endpoint identity。
func (endpoint Endpoint) Equal(other Endpoint) bool { return endpoint == other }

// Valid 报告 endpoint 是否经过完整构造。
func (endpoint Endpoint) Valid() bool {
	return endpoint.channel.Valid() && endpoint.channel != ChannelHTTPS && endpoint.host != "" && endpoint.port != 0
}

// AuthContext 是 application service 唯一可以信任的操作者身份。
//
// Context 不保存 raw credential、socket、generated type 或可变 map。
type AuthContext struct {
	// principal 是上游账号域确认并绑定到 session 的身份。
	principal Principal
	// sessionID 标识当前认证 lineage。
	sessionID SessionID
	// epoch 必须与 store 当前 session epoch 一致。
	epoch Epoch
	// channel 标识完成认证的入口，业务不能从 payload 改写。
	channel Channel
	// scopes 是构造时冻结的 capability 集合。
	scopes ScopeSet
}

// AuthenticatedSession 是 HTTPS 认证边界交给 application service 的可信会话快照。
//
// deadline 来自与身份相同的原子 store 读取，业务层可据此限制会跨越认证时刻的
// 状态变更；该值不包含 raw credential，也不会延长 session 生命周期。
type AuthenticatedSession struct {
	// auth 是经过验证且固定为 HTTPS channel 的身份上下文。
	auth AuthContext
	// deadline 是 access 与 session 两个绝对失效时间中的较早者。
	deadline time.Time
}

// newAuthenticatedSession 只接受完整 HTTPS 身份和未来的绝对截止时间。
func newAuthenticatedSession(auth AuthContext, deadline time.Time, now time.Time) (AuthenticatedSession, error) {
	if !auth.Valid() || auth.Channel() != ChannelHTTPS || deadline.IsZero() || now.IsZero() || !now.Before(deadline) {
		return AuthenticatedSession{}, errors.New("authenticated session requires a valid HTTPS identity and future deadline")
	}
	return AuthenticatedSession{auth: auth, deadline: deadline}, nil
}

// Valid 报告快照是否包含完整 HTTPS 身份与绝对截止时间。
func (session AuthenticatedSession) Valid() bool {
	return session.auth.Valid() && session.auth.Channel() == ChannelHTTPS && !session.deadline.IsZero()
}

// AuthContext 返回不可变身份值副本。
func (session AuthenticatedSession) AuthContext() AuthContext { return session.auth }

// Deadline 返回本次认证可用于业务变更的最晚绝对时间。
func (session AuthenticatedSession) Deadline() time.Time { return session.deadline }

// String 返回不包含 principal、credential 与截止时间的安全摘要。
func (session AuthenticatedSession) String() string {
	return "AuthenticatedSession{session=" + session.auth.SessionID().String() + "}"
}

// GoString 与 String 保持相同安全格式化边界。
func (session AuthenticatedSession) GoString() string { return session.String() }

// LogValue 复用 AuthContext 的脱敏日志边界，不记录认证截止时间。
func (session AuthenticatedSession) LogValue() slog.Value { return session.auth.LogValue() }

// newAuthContext 只允许 session 认证流程构造 AuthContext，防止其他 package 伪造可信身份。
func newAuthContext(principal Principal, sessionID SessionID, epoch Epoch, channel Channel, scopes ScopeSet) (AuthContext, error) {
	if !principal.Valid() || !sessionID.Valid() || !epoch.Valid() || !channel.Valid() {
		return AuthContext{}, errors.New("auth context requires valid identity, epoch, and channel")
	}
	if !scopes.validForChannel(channel) {
		return AuthContext{}, errors.New("auth context scopes do not match channel policy")
	}
	return AuthContext{principal: principal, sessionID: sessionID, epoch: epoch, channel: channel, scopes: scopes}, nil
}

// validForChannel 验证不可扩权的固定 capability matrix。
func (set ScopeSet) validForChannel(channel Channel) bool {
	switch channel {
	case ChannelHTTPS:
		return set.Len() == 0
	case ChannelWSS:
		return set.Len() == 1 && set.Has(ScopeControl)
	case ChannelTLSTCP:
		return set.Len() == 1 && set.Has(ScopeGameplay)
	default:
		return false
	}
}

// Valid 报告 AuthContext 是否由完整身份和符合 channel policy 的 scopes 构成。
func (auth AuthContext) Valid() bool {
	return auth.principal.Valid() && auth.sessionID.Valid() && auth.epoch.Valid() && auth.scopes.validForChannel(auth.channel)
}

// Principal 返回经过认证的账号与玩家身份值副本。
func (auth AuthContext) Principal() Principal { return auth.principal }

// SessionID 返回身份所属 session。
func (auth AuthContext) SessionID() SessionID { return auth.sessionID }

// Epoch 返回凭据验证时确认的 session epoch。
func (auth AuthContext) Epoch() Epoch { return auth.epoch }

// Channel 返回完成认证的入口 channel。
func (auth AuthContext) Channel() Channel { return auth.channel }

// HasScope 报告服务端是否授予指定 capability。
func (auth AuthContext) HasScope(scope Scope) bool { return auth.scopes.Has(scope) }

// Scopes 返回规范化副本，调用方无法修改内部授权集合。
func (auth AuthContext) Scopes() []Scope { return auth.scopes.Values() }

// String 返回不包含 principal 与 credential 的认证上下文摘要。
func (auth AuthContext) String() string {
	return "AuthContext{session=" + auth.sessionID.String() + "}"
}

// GoString 与 String 保持相同安全格式化边界。
func (auth AuthContext) GoString() string { return auth.String() }

// LogValue 仅记录可撤销 session identity、epoch 与 channel，不记录 principal。
func (auth AuthContext) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("session_id", auth.sessionID.String()),
		slog.Uint64("epoch", uint64(auth.epoch)),
		slog.Uint64("channel", uint64(auth.channel)),
	)
}

// validateIdentifier 只接受有界安全 ASCII，避免日志、路径与存储 key 注入。
func validateIdentifier(name string, value string) error {
	if len(value) < 1 || len(value) > maximumIdentifierBytes {
		return errors.New(name + " must contain 1-128 bytes")
	}
	for _, character := range []byte(value) {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' && character != '.' {
			return errors.New(name + " contains unsupported identifier characters")
		}
	}
	return nil
}

// validHost 接受 IP 或符合 DNS label 长度与字符边界的 ASCII name。
func validHost(host string) bool {
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	if len(host) < 1 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}
