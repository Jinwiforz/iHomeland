package visitsession

import (
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/session"
)

const (
	// maximumIdentifierBytes 限制运行态 key、关联值与 adapter 输入规模。
	maximumIdentifierBytes = 128
	// maximumPendingInvites 防止尚未占用 capacity 的邀请无限增长。
	maximumPendingInvites = 64
	// identityPlaceholder 是 VisitSession 相关 identity 默认格式化唯一允许输出的文本。
	identityPlaceholder = "[REDACTED_VISIT_SESSION_IDENTITY]"
	// visitSessionPlaceholder 防止完整 aggregate 或 snapshot 被普通日志展开。
	visitSessionPlaceholder = "[REDACTED_VISIT_SESSION]"
	// admissionPlaceholder 防止未来 admission material 通过格式化路径扩散。
	admissionPlaceholder = "[REDACTED_VISIT_ADMISSION]"
)

const (
	// minimumSessionLifetime 是 VisitSession 可配置存续时间的安全下限。
	minimumSessionLifetime = time.Minute
	// maximumSessionLifetime 是 VisitSession 可配置存续时间的安全上限。
	maximumSessionLifetime = 24 * time.Hour
	// minimumInviteLifetime 是邀请绝对 deadline 相对首次 observedAt 的安全下限。
	minimumInviteLifetime = time.Second
	// maximumInviteLifetime 是邀请可配置存续时间的安全上限。
	maximumInviteLifetime = time.Hour
	// minimumReservationLifetime 是 join reservation 绝对 deadline 的安全下限。
	minimumReservationLifetime = time.Second
	// maximumReservationLifetime 是 join reservation 可配置存续时间的安全上限。
	maximumReservationLifetime = 2 * time.Minute
	// minimumOwnerGrace 是 Owner grace 绝对 deadline 的安全下限。
	minimumOwnerGrace = time.Second
	// maximumOwnerGrace 是 Owner grace 可配置恢复窗口的安全上限。
	maximumOwnerGrace = 5 * time.Minute
	// minimumVisitorReconnectGrace 是 Visitor reconnect deadline 的安全下限。
	minimumVisitorReconnectGrace = time.Second
	// maximumVisitorReconnectGrace 是 Visitor reconnect 可配置恢复窗口的安全上限。
	maximumVisitorReconnectGrace = 2 * time.Minute
)

const (
	// visitSessionIDPrefix 把通用随机材料固定到 VisitSession namespace。
	visitSessionIDPrefix = "vses_"
	// inviteIDPrefix 区分定向邀请与 aggregate、command 或 connection identity。
	inviteIDPrefix = "vinv_"
	// commandIDPrefix 固定 store 幂等索引使用的 command namespace。
	commandIDPrefix = "vcmd_"
	// connectionBindingIDPrefix 区分 connection registry identity 与 session identity。
	connectionBindingIDPrefix = "vbind_"
)

// VisitSessionID 标识一次不可迁移或复活的个人世界访问 aggregate。
type VisitSessionID struct {
	// value 保存带 VisitSession namespace 前缀的安全 ASCII identity。
	value string
}

// NewVisitSessionID 校验服务端生成或 store hydration 返回的独立 namespace identity。
func NewVisitSessionID(value string) (VisitSessionID, error) {
	if err := validateNamespacedIdentifier(value, visitSessionIDPrefix); err != nil {
		return VisitSessionID{}, err
	}
	return VisitSessionID{value: value}, nil
}

// Value 返回 adapter 索引使用的精确 identity；不得写入普通日志或 metrics label。
func (id VisitSessionID) Value() string { return id.value }

// Valid 报告 identity 是否经过完整构造。
func (id VisitSessionID) Valid() bool { return id.value != "" }

// String 防止默认格式化泄漏访问关联 identity。
func (VisitSessionID) String() string { return identityPlaceholder }

// GoString 防止 `%#v` 展开 identity 私有字段。
func (VisitSessionID) GoString() string { return identityPlaceholder }

// LogValue 让结构化日志只记录稳定占位文本。
func (VisitSessionID) LogValue() slog.Value { return slog.StringValue(identityPlaceholder) }

// InviteID 标识 VisitSession 内的一次定向邀请，不具备 admission 权限。
type InviteID struct {
	// value 保存带 invite namespace 前缀的安全 ASCII identity。
	value string
}

// NewInviteID 校验服务端生成或 store hydration 返回的邀请 identity。
func NewInviteID(value string) (InviteID, error) {
	if err := validateNamespacedIdentifier(value, inviteIDPrefix); err != nil {
		return InviteID{}, err
	}
	return InviteID{value: value}, nil
}

// Value 返回 adapter 索引使用的精确 identity；该值本身不是 bearer secret。
func (id InviteID) Value() string { return id.value }

// Valid 报告 identity 是否经过完整构造。
func (id InviteID) Valid() bool { return id.value != "" }

// String 防止默认格式化扩散邀请关联值。
func (InviteID) String() string { return identityPlaceholder }

// GoString 防止 `%#v` 展开 identity 私有字段。
func (InviteID) GoString() string { return identityPlaceholder }

// LogValue 让结构化日志只记录稳定占位文本。
func (InviteID) LogValue() slog.Value { return slog.StringValue(identityPlaceholder) }

// CommandID 标识一次必须由 store 幂等决议的 VisitSession mutation。
type CommandID struct {
	// value 保存带 command namespace 前缀的安全 ASCII identity。
	value string
}

// NewCommandID 校验可信调用边界生成的稳定 command identity。
func NewCommandID(value string) (CommandID, error) {
	if err := validateNamespacedIdentifier(value, commandIDPrefix); err != nil {
		return CommandID{}, err
	}
	return CommandID{value: value}, nil
}

// Value 返回 store 幂等索引使用的精确 identity。
func (id CommandID) Value() string { return id.value }

// Valid 报告 identity 是否经过完整构造。
func (id CommandID) Valid() bool { return id.value != "" }

// String 防止默认格式化泄漏重试关联值。
func (CommandID) String() string { return identityPlaceholder }

// GoString 防止 `%#v` 展开 identity 私有字段。
func (CommandID) GoString() string { return identityPlaceholder }

// LogValue 让结构化日志只记录稳定占位文本。
func (CommandID) LogValue() slog.Value { return slog.StringValue(identityPlaceholder) }

// ConnectionBindingID 标识未来 connection registry 已确认的一次连接绑定。
//
// 该值只用于拒绝延迟 callback，不保存 socket、endpoint 或任何连接对象。
type ConnectionBindingID struct {
	// value 保存带 connection-binding namespace 前缀的安全 ASCII identity。
	value string
}

// NewConnectionBindingID 校验受信 connection registry 生成的 opaque identity。
func NewConnectionBindingID(value string) (ConnectionBindingID, error) {
	if err := validateNamespacedIdentifier(value, connectionBindingIDPrefix); err != nil {
		return ConnectionBindingID{}, err
	}
	return ConnectionBindingID{value: value}, nil
}

// Value 返回 adapter 条件比较使用的精确 identity。
func (id ConnectionBindingID) Value() string { return id.value }

// Valid 报告 identity 是否经过完整构造。
func (id ConnectionBindingID) Valid() bool { return id.value != "" }

// String 防止默认格式化泄漏连接关联值。
func (ConnectionBindingID) String() string { return identityPlaceholder }

// GoString 防止 `%#v` 展开 identity 私有字段。
func (ConnectionBindingID) GoString() string { return identityPlaceholder }

// LogValue 让结构化日志只记录稳定占位文本。
func (ConnectionBindingID) LogValue() slog.Value { return slog.StringValue(identityPlaceholder) }

// Capacity 是一个 VisitSession 同时保留的 Visitor membership 上限。
type Capacity uint8

const (
	// MinimumCapacity 是允许开启访问的最小 Visitor 数量。
	MinimumCapacity Capacity = 1
	// MaximumCapacity 是单 aggregate 有界并发与 snapshot 大小上限。
	MaximumCapacity Capacity = 32
)

// NewCapacity 校验受信 policy 配置的 1-32 Visitor 上限。
func NewCapacity(value uint8) (Capacity, error) {
	capacity := Capacity(value)
	if capacity < MinimumCapacity || capacity > MaximumCapacity {
		return 0, errors.New("visit session capacity is out of range")
	}
	return capacity, nil
}

// Uint8 返回 adapter projection 使用的容量数值。
func (capacity Capacity) Uint8() uint8 { return uint8(capacity) }

// Valid 报告 capacity 是否位于固定安全范围。
func (capacity Capacity) Valid() bool {
	return capacity >= MinimumCapacity && capacity <= MaximumCapacity
}

// Revision 是 VisitSession 乐观并发版本；每次首次 mutation 精确推进一次。
type Revision uint64

// InitialRevision 是新建 VisitSession 的第一个版本。
const InitialRevision Revision = 1

// NewRevision 校验 store hydration 返回的正 revision。
func NewRevision(value uint64) (Revision, error) {
	if value == 0 {
		return 0, errors.New("visit session revision must be positive")
	}
	return Revision(value), nil
}

// Uint64 返回 store 条件比较使用的版本值。
func (revision Revision) Uint64() uint64 { return uint64(revision) }

// Valid 报告 revision 是否可以参与 expected revision 比较。
func (revision Revision) Valid() bool { return revision > 0 }

// next 返回成功 mutation 的唯一后继版本，并拒绝无效值或溢出。
func (revision Revision) next() (Revision, error) {
	if !revision.Valid() || revision == Revision(^uint64(0)) {
		return 0, errors.New("visit session revision cannot advance")
	}
	return revision + 1, nil
}

// Policy 固定 VisitSession 的容量、存续时间与各类绝对 deadline 配置上限。
type Policy struct {
	// capacity 是 reserved、joined 与 reconnecting membership 的总上限。
	capacity Capacity
	// sessionLifetime 限制 aggregate 从创建到绝对失效的存续时间。
	sessionLifetime time.Duration
	// inviteLifetime 限制定向邀请相对首次 observedAt 的最长有效时间。
	inviteLifetime time.Duration
	// reservationLifetime 限制 accept 后等待受信 join 的最长时间。
	reservationLifetime time.Duration
	// ownerGrace 限制 Owner 断线后恢复同一 aggregate 的最长时间。
	ownerGrace time.Duration
	// visitorReconnectGrace 限制 joined Visitor 恢复新 binding 的最长时间。
	visitorReconnectGrace time.Duration
}

// NewPolicy 校验当前领域策略的固定闭区间：session 为 1 分钟至 24 小时，invite 为
// 1 秒至 1 小时，reservation 为 1 秒至 2 分钟，Owner grace 为 1 秒至 5 分钟，
// Visitor reconnect grace 为 1 秒至 2 分钟。
func NewPolicy(capacity Capacity, sessionLifetime, inviteLifetime, reservationLifetime, ownerGrace, visitorReconnectGrace time.Duration) (Policy, error) {
	if !capacity.Valid() || sessionLifetime < minimumSessionLifetime || sessionLifetime > maximumSessionLifetime ||
		inviteLifetime < minimumInviteLifetime || inviteLifetime > maximumInviteLifetime ||
		reservationLifetime < minimumReservationLifetime || reservationLifetime > maximumReservationLifetime ||
		ownerGrace < minimumOwnerGrace || ownerGrace > maximumOwnerGrace ||
		visitorReconnectGrace < minimumVisitorReconnectGrace || visitorReconnectGrace > maximumVisitorReconnectGrace {
		return Policy{}, errors.New("visit session policy is out of range")
	}
	return Policy{capacity: capacity, sessionLifetime: sessionLifetime, inviteLifetime: inviteLifetime, reservationLifetime: reservationLifetime, ownerGrace: ownerGrace, visitorReconnectGrace: visitorReconnectGrace}, nil
}

// Capacity 返回 active Visitor membership 的固定上限。
func (policy Policy) Capacity() Capacity { return policy.capacity }

// SessionLifetime 返回 aggregate 存续时间。
func (policy Policy) SessionLifetime() time.Duration { return policy.sessionLifetime }

// InviteLifetime 返回邀请相对首次 observedAt 的配置上限。
func (policy Policy) InviteLifetime() time.Duration { return policy.inviteLifetime }

// ReservationLifetime 返回 join reservation 的配置上限。
func (policy Policy) ReservationLifetime() time.Duration { return policy.reservationLifetime }

// OwnerGrace 返回 Owner 断线恢复窗口的配置上限。
func (policy Policy) OwnerGrace() time.Duration { return policy.ownerGrace }

// VisitorReconnectGrace 返回 Visitor 断线恢复窗口的配置上限。
func (policy Policy) VisitorReconnectGrace() time.Duration { return policy.visitorReconnectGrace }

// Valid 报告 policy 是否经过完整范围校验。
func (policy Policy) Valid() bool {
	_, err := NewPolicy(policy.capacity, policy.sessionLifetime, policy.inviteLifetime, policy.reservationLifetime, policy.ownerGrace, policy.visitorReconnectGrace)
	return err == nil
}

// Lifecycle 表达 VisitSession 控制面的封闭状态。
type Lifecycle uint8

const (
	// LifecycleUnspecified 是禁止进入 snapshot 的零值。
	LifecycleUnspecified Lifecycle = iota
	// LifecycleOpen 允许 Owner 邀请以及目标 Visitor accept/join。
	LifecycleOpen
	// LifecycleOwnerGrace 暂停新资格并等待 immutable Owner 恢复。
	LifecycleOwnerGrace
	// LifecycleClosed 是不可恢复或迁移的 terminal 状态。
	LifecycleClosed
)

// String 返回 storage enum 与低基数诊断使用的稳定名称。
func (lifecycle Lifecycle) String() string {
	switch lifecycle {
	case LifecycleOpen:
		return "open"
	case LifecycleOwnerGrace:
		return "owner_grace"
	case LifecycleClosed:
		return "closed"
	default:
		return "unspecified"
	}
}

// Valid 报告 lifecycle 是否属于封闭集合。
func (lifecycle Lifecycle) Valid() bool {
	return lifecycle == LifecycleOpen || lifecycle == LifecycleOwnerGrace || lifecycle == LifecycleClosed
}

// InviteState 表达 active invite projection 的封闭状态。
type InviteState uint8

const (
	// InviteStateUnspecified 是禁止进入 snapshot 的零值。
	InviteStateUnspecified InviteState = iota
	// InviteStatePending 允许且只允许目标 Visitor 发起 accept。
	InviteStatePending
	// InviteStateAccepted 保留 membership 与原邀请的关联，不再允许重复 accept。
	InviteStateAccepted
)

// String 返回 storage enum 使用的稳定名称。
func (state InviteState) String() string {
	if state == InviteStatePending {
		return "pending"
	}
	if state == InviteStateAccepted {
		return "accepted"
	}
	return "unspecified"
}

// Valid 报告 state 是否属于封闭集合。
func (state InviteState) Valid() bool {
	return state == InviteStatePending || state == InviteStateAccepted
}

// MembershipState 表达 Visitor 访问资格的封闭状态。
type MembershipState uint8

const (
	// MembershipStateUnspecified 是禁止进入 snapshot 的零值。
	MembershipStateUnspecified MembershipState = iota
	// MembershipStateReserved 表示 accept 成功但尚未完成受信 admission。
	MembershipStateReserved
	// MembershipStateJoined 表示 Visitor 已绑定当前连接。
	MembershipStateJoined
	// MembershipStateReconnecting 表示旧连接断开且仍在有界恢复窗口。
	MembershipStateReconnecting
)

// String 返回 storage enum 使用的稳定名称。
func (state MembershipState) String() string {
	switch state {
	case MembershipStateReserved:
		return "reserved"
	case MembershipStateJoined:
		return "joined"
	case MembershipStateReconnecting:
		return "reconnecting"
	default:
		return "unspecified"
	}
}

// Valid 报告 state 是否属于封闭集合。
func (state MembershipState) Valid() bool {
	return state >= MembershipStateReserved && state <= MembershipStateReconnecting
}

// Role 是 VisitSession 控制面可向后续显式 policy 投影的角色。
type Role uint8

const (
	// RoleUnspecified 不授予任何控制面或 gameplay 权限。
	RoleUnspecified Role = iota
	// RoleOwner 只代表 immutable VisitSession Owner。
	RoleOwner
	// RoleVisitor 只代表当前 aggregate 中的 active membership。
	RoleVisitor
)

// String 返回后续显式 authorization policy 使用的稳定角色名。
func (role Role) String() string {
	switch role {
	case RoleOwner:
		return "owner"
	case RoleVisitor:
		return "visitor"
	default:
		return "unspecified"
	}
}

// Valid 报告 role 是否属于明确角色集合。
func (role Role) Valid() bool { return role == RoleOwner || role == RoleVisitor }

// Actor 保存从受信 session.AuthContext 提取的不可覆盖身份。
type Actor struct {
	// playerID 是认证 principal 的业务玩家身份。
	playerID account.PlayerID
	// sessionID 标识当前认证 lineage。
	sessionID session.SessionID
	// epoch 是旧 token、ticket 与 callback 的失效屏障。
	epoch session.Epoch
}

// ActorFromAuth 从不可伪造 AuthContext 构造 VisitSession command actor。
func ActorFromAuth(auth session.AuthContext) (Actor, error) {
	if !auth.Valid() {
		return Actor{}, errors.New("visit session auth context is invalid")
	}
	playerID, err := account.NewPlayerID(auth.Principal().PlayerID())
	if err != nil {
		return Actor{}, errors.New("visit session principal is invalid")
	}
	return Actor{playerID: playerID, sessionID: auth.SessionID(), epoch: auth.Epoch()}, nil
}

// PlayerID 返回认证 owner 提供的玩家身份值副本。
func (actor Actor) PlayerID() account.PlayerID { return actor.playerID }

// SessionID 返回 actor 当前认证 lineage。
func (actor Actor) SessionID() session.SessionID { return actor.sessionID }

// Epoch 返回 actor 当前 session 屏障。
func (actor Actor) Epoch() session.Epoch { return actor.epoch }

// Valid 报告 actor 是否来自完整 AuthContext 投影。
func (actor Actor) Valid() bool {
	return actor.playerID.Valid() && actor.sessionID.Valid() && actor.epoch.Valid()
}

// String 防止默认格式化展开认证身份。
func (Actor) String() string { return "[REDACTED_VISIT_ACTOR]" }

// GoString 防止 `%#v` 展开认证身份。
func (Actor) GoString() string { return "[REDACTED_VISIT_ACTOR]" }

// LogValue 让结构化日志只记录稳定占位文本。
func (Actor) LogValue() slog.Value { return slog.StringValue("[REDACTED_VISIT_ACTOR]") }

// AuthBinding 把受信 actor lineage 与 connection registry identity 精确绑定。
type AuthBinding struct {
	// actor 保存认证 PlayerID、SessionID 与 epoch。
	actor Actor
	// connectionID 是当前或刚断开的精确连接 identity。
	connectionID ConnectionBindingID
}

// NewAuthBinding 校验认证 actor 与受信连接 identity 的完整组合。
func NewAuthBinding(actor Actor, connectionID ConnectionBindingID) (AuthBinding, error) {
	if !actor.Valid() || !connectionID.Valid() {
		return AuthBinding{}, errors.New("visit session auth binding is incomplete")
	}
	return AuthBinding{actor: actor, connectionID: connectionID}, nil
}

// HydrateAuthBinding 从已验证的 storage projection 恢复认证与连接绑定。
//
// 该入口只重建 VisitSession snapshot 中已经提交的条件事实，不创建 AuthContext，也不授予
// 新 command 权限。调用方仍须通过 NewSnapshot 的 owner 与集合交叉校验。
func HydrateAuthBinding(playerID account.PlayerID, sessionID session.SessionID, epoch session.Epoch, connectionID ConnectionBindingID) (AuthBinding, error) {
	actor := Actor{playerID: playerID, sessionID: sessionID, epoch: epoch}
	return NewAuthBinding(actor, connectionID)
}

// Actor 返回不可变认证投影值副本。
func (binding AuthBinding) Actor() Actor { return binding.actor }

// ConnectionID 返回精确 callback 条件 identity。
func (binding AuthBinding) ConnectionID() ConnectionBindingID { return binding.connectionID }

// Valid 报告 binding 是否经过完整构造。
func (binding AuthBinding) Valid() bool { return binding.actor.Valid() && binding.connectionID.Valid() }

// Equal 比较 PlayerID、SessionID、epoch 与 connection identity 全部字段。
func (binding AuthBinding) Equal(other AuthBinding) bool { return binding == other }

// String 防止默认格式化展开认证 lineage 与 connection identity。
func (AuthBinding) String() string { return "[REDACTED_VISIT_AUTH_BINDING]" }

// GoString 防止 `%#v` 展开 binding 私有字段。
func (AuthBinding) GoString() string { return "[REDACTED_VISIT_AUTH_BINDING]" }

// LogValue 让结构化日志只记录稳定占位文本。
func (AuthBinding) LogValue() slog.Value { return slog.StringValue("[REDACTED_VISIT_AUTH_BINDING]") }

// canonicalTime 移除单调分量并固定为跨 store 一致的 UTC 微秒精度。
func canonicalTime(value time.Time) time.Time { return value.UTC().Truncate(time.Microsecond) }

// deadlineAt 以规范 created time 生成绝对 deadline，并拒绝 duration 溢出或零值。
func deadlineAt(createdAt time.Time, lifetime time.Duration) (time.Time, error) {
	if createdAt.IsZero() || lifetime <= 0 {
		return time.Time{}, errors.New("visit session deadline input is invalid")
	}
	createdAt = canonicalTime(createdAt)
	deadline := canonicalTime(createdAt.Add(lifetime))
	if !deadline.After(createdAt) {
		return time.Time{}, errors.New("visit session deadline cannot advance")
	}
	return deadline, nil
}

// expiredAt 使用“等于即失效”规则判断受信绝对时间是否到达 deadline。
func expiredAt(observedAt, deadline time.Time) bool {
	return !canonicalTime(observedAt).Before(deadline)
}

// validateNamespacedIdentifier 固定 entity namespace、最大长度与跨存储安全 ASCII。
func validateNamespacedIdentifier(value, prefix string) error {
	if !strings.HasPrefix(value, prefix) || len(value) <= len(prefix) || len(value) > maximumIdentifierBytes {
		return errors.New("visit session identifier prefix or length is invalid")
	}
	for index := len(prefix); index < len(value); index++ {
		character := value[index]
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') {
			return errors.New("visit session identifier contains unsupported characters")
		}
	}
	return nil
}
