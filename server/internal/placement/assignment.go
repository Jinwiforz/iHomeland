package placement

import (
	"errors"
	"log/slog"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
)

const (
	// assignmentPlaceholder 是 snapshot 与 stamp 默认格式化唯一允许输出的稳定文本。
	assignmentPlaceholder = "[REDACTED_PLACEMENT_ASSIGNMENT]"
	// writeFencePlaceholder 防止候选持久写入资格进入普通日志。
	writeFencePlaceholder = "[REDACTED_WRITE_FENCE]"
)

// AssignmentStamp 是所有条件 placement 操作必须完整比较的不可变 identity。
//
// Stamp 不包含 phase 或 expiry，因为这两者可以在同一 assignment 内推进；world、instance、
// node、generation 和 fence 任一不匹配都必须拒绝，避免只比较 ID 产生 ABA。
type AssignmentStamp struct {
	// worldID 是被承载的持久 PersonalWorld identity。
	worldID personalworld.PersonalWorldID
	// instanceID 是本次不可复活运行承载的 identity。
	instanceID WorldInstanceID
	// nodeID 是受信 runtime controller 路由使用的节点 identity。
	nodeID RuntimeNodeID
	// generation 是 current assignment 的公开单调版本。
	generation AssignmentGeneration
	// fencingToken 是后续持久提交必须重新比较的写者序列。
	fencingToken FencingToken
}

// NewAssignmentStamp 校验 store hydration 或条件操作使用的完整 identity。
func NewAssignmentStamp(worldID personalworld.PersonalWorldID, instanceID WorldInstanceID, nodeID RuntimeNodeID, generation AssignmentGeneration, fencingToken FencingToken) (AssignmentStamp, error) {
	if !worldID.Valid() || !instanceID.Valid() || !nodeID.Valid() || !generation.Valid() || !fencingToken.Valid() {
		return AssignmentStamp{}, errors.New("assignment stamp is incomplete")
	}
	return AssignmentStamp{worldID: worldID, instanceID: instanceID, nodeID: nodeID, generation: generation, fencingToken: fencingToken}, nil
}

// WorldID 返回 stamp 绑定的持久 PersonalWorld identity。
func (stamp AssignmentStamp) WorldID() personalworld.PersonalWorldID { return stamp.worldID }

// InstanceID 返回本次运行承载的 immutable identity。
func (stamp AssignmentStamp) InstanceID() WorldInstanceID { return stamp.instanceID }

// NodeID 返回受信 runtime 节点 identity。
func (stamp AssignmentStamp) NodeID() RuntimeNodeID { return stamp.nodeID }

// Generation 返回 assignment 的单调版本。
func (stamp AssignmentStamp) Generation() AssignmentGeneration { return stamp.generation }

// FencingToken 返回后续持久提交必须重新验证的 token。
func (stamp AssignmentStamp) FencingToken() FencingToken { return stamp.fencingToken }

// Valid 报告 stamp 是否来自完整构造路径。
func (stamp AssignmentStamp) Valid() bool {
	return stamp.worldID.Valid() && stamp.instanceID.Valid() && stamp.nodeID.Valid() && stamp.generation.Valid() && stamp.fencingToken.Valid()
}

// Equal 比较防止 stale operation 的全部 identity 字段。
func (stamp AssignmentStamp) Equal(other AssignmentStamp) bool { return stamp == other }

// String 防止默认格式化输出 node 与 fencing material。
func (AssignmentStamp) String() string { return assignmentPlaceholder }

// GoString 防止 `%#v` 展开 stamp 私有字段。
func (AssignmentStamp) GoString() string { return assignmentPlaceholder }

// LogValue 让结构化日志只记录稳定占位文本。
func (AssignmentStamp) LogValue() slog.Value { return slog.StringValue(assignmentPlaceholder) }

// Lease 保存 current assignment 的 fencing token 与绝对 UTC expiry。
type Lease struct {
	// fencingToken 标识当前写者序列，renew 不能修改该值。
	fencingToken FencingToken
	// expiresAt 是等于或晚于该 UTC 时刻即失效的绝对 deadline。
	expiresAt time.Time
}

// NewLease 校验 store hydration 返回的 token 与相对受信观测时间仍有效的 deadline。
//
// observedAt 和 expiresAt 都会移除单调分量并规范为 UTC 微秒；expiry 必须严格晚于观测
// 时间，因此到达边界的 lease 不会被构造成可续租或可写值。
func NewLease(fencingToken FencingToken, expiresAt time.Time, observedAt time.Time) (Lease, error) {
	if !fencingToken.Valid() || observedAt.IsZero() || expiresAt.IsZero() {
		return Lease{}, errors.New("assignment lease is incomplete")
	}
	observedAt = canonicalPlacementTime(observedAt)
	expiresAt = canonicalPlacementTime(expiresAt)
	if !expiresAt.After(observedAt) {
		return Lease{}, errors.New("assignment lease is expired")
	}
	return Lease{fencingToken: fencingToken, expiresAt: expiresAt}, nil
}

// FencingToken 返回 lease 持有的精确写者序列。
func (lease Lease) FencingToken() FencingToken { return lease.fencingToken }

// ExpiresAt 返回移除单调分量后的 UTC 绝对 deadline。
func (lease Lease) ExpiresAt() time.Time { return lease.expiresAt }

// Valid 报告 lease 是否包含结构完整的 token 与 deadline；当前有效性必须用 ValidAt 判断。
func (lease Lease) Valid() bool { return lease.fencingToken.Valid() && !lease.expiresAt.IsZero() }

// ValidAt 使用受信绝对时间判断 lease 是否仍在严格有效区间内。
func (lease Lease) ValidAt(observedAt time.Time) bool {
	return lease.Valid() && !observedAt.IsZero() && lease.expiresAt.After(observedAt.UTC())
}

// AssignmentSnapshot 是 PlacementStore 与 application 之间严格构造的 current 投影。
//
// Snapshot 按值返回且不含引用字段。没有 current assignment 使用零值与 not-found outcome
// 表达，不能用 `sleeping`、`failed` 或 terminal phase 伪造 current 事实。
type AssignmentSnapshot struct {
	// stamp 保存所有条件操作必须比较的 immutable identity。
	stamp AssignmentStamp
	// phase 只允许 starting 或 active。
	phase Phase
	// createdAt 是 assignment 首次提交的 UTC 绝对时间。
	createdAt time.Time
	// lease 保存当前 token 与有界 expiry。
	lease Lease
}

// NewAssignmentSnapshot 校验 store hydration 返回的完整 current assignment。
//
// createdAt 必须不晚于 observedAt，lease expiry 必须严格晚于 createdAt。三个时间都规范为
// Redis/MySQL schema 使用的 UTC 微秒，避免首次结果与持久 hydration 因亚微秒差异冲突。
// Hydration 允许恢复在 observedAt 已经过期的 snapshot，使 application 能按原 stamp 条件
// 替换或撤销；调用期有效性必须用 ValidAt 判断。该 constructor 不修复 generation 或 phase。
func NewAssignmentSnapshot(stamp AssignmentStamp, phase Phase, createdAt time.Time, leaseExpiresAt time.Time, observedAt time.Time) (AssignmentSnapshot, error) {
	if !stamp.Valid() || !phase.Valid() || createdAt.IsZero() || leaseExpiresAt.IsZero() || observedAt.IsZero() {
		return AssignmentSnapshot{}, errors.New("assignment snapshot is incomplete")
	}
	createdAt = canonicalPlacementTime(createdAt)
	leaseExpiresAt = canonicalPlacementTime(leaseExpiresAt)
	observedAt = canonicalPlacementTime(observedAt)
	if createdAt.After(observedAt) {
		return AssignmentSnapshot{}, errors.New("assignment created time is in the future")
	}
	if !leaseExpiresAt.After(createdAt) {
		return AssignmentSnapshot{}, errors.New("assignment lease does not follow created time")
	}
	lease := Lease{fencingToken: stamp.FencingToken(), expiresAt: leaseExpiresAt}
	return AssignmentSnapshot{stamp: stamp, phase: phase, createdAt: createdAt, lease: lease}, nil
}

// Stamp 返回不包含引用字段的完整条件 identity 副本。
func (snapshot AssignmentSnapshot) Stamp() AssignmentStamp { return snapshot.stamp }

// WorldID 返回 assignment 绑定的 PersonalWorld identity。
func (snapshot AssignmentSnapshot) WorldID() personalworld.PersonalWorldID {
	return snapshot.stamp.WorldID()
}

// InstanceID 返回 assignment 的 WorldInstance identity。
func (snapshot AssignmentSnapshot) InstanceID() WorldInstanceID { return snapshot.stamp.InstanceID() }

// NodeID 返回 assignment 的受信 RuntimeNode identity。
func (snapshot AssignmentSnapshot) NodeID() RuntimeNodeID { return snapshot.stamp.NodeID() }

// Generation 返回 current assignment 的单调版本。
func (snapshot AssignmentSnapshot) Generation() AssignmentGeneration {
	return snapshot.stamp.Generation()
}

// FencingToken 返回 lease 与 stamp 共同绑定的写者序列。
func (snapshot AssignmentSnapshot) FencingToken() FencingToken { return snapshot.stamp.FencingToken() }

// Phase 返回 starting 或 active current 状态。
func (snapshot AssignmentSnapshot) Phase() Phase { return snapshot.phase }

// CreatedAt 返回 assignment 首次提交的 UTC 绝对时间。
func (snapshot AssignmentSnapshot) CreatedAt() time.Time { return snapshot.createdAt }

// Lease 返回当前 lease 值副本。
func (snapshot AssignmentSnapshot) Lease() Lease { return snapshot.lease }

// Valid 报告 snapshot 是否结构完整；调用期 lease 有效性必须使用 ValidAt 再判断。
func (snapshot AssignmentSnapshot) Valid() bool {
	return snapshot.stamp.Valid() && snapshot.phase.Valid() && !snapshot.createdAt.IsZero() && snapshot.lease.Valid() && snapshot.lease.FencingToken() == snapshot.stamp.FencingToken() && snapshot.lease.ExpiresAt().After(snapshot.createdAt)
}

// ValidAt 报告 snapshot 结构完整且 lease 在受信观测时间仍有效。
func (snapshot AssignmentSnapshot) ValidAt(observedAt time.Time) bool {
	return snapshot.Valid() && !observedAt.IsZero() && !snapshot.createdAt.After(observedAt.UTC()) && snapshot.lease.ValidAt(observedAt)
}

// Equal 比较全部 current assignment 事实，供 adapter result 与 replay 验证使用。
func (snapshot AssignmentSnapshot) Equal(other AssignmentSnapshot) bool {
	return snapshot.stamp.Equal(other.stamp) && snapshot.phase == other.phase && snapshot.createdAt.Equal(other.createdAt) && snapshot.lease.expiresAt.Equal(other.lease.expiresAt)
}

// Activate 构造相同 stamp 与 lease 的 active snapshot。
//
// observedAt 必须早于 lease expiry；该值转换不修改 receiver，真正的 current 条件比较和
// 提交仍由 PlacementStore.Activate 原子完成。
func (snapshot AssignmentSnapshot) Activate(observedAt time.Time) (AssignmentSnapshot, error) {
	if snapshot.phase != PhaseStarting || !snapshot.ValidAt(observedAt) {
		return AssignmentSnapshot{}, errors.New("assignment cannot activate")
	}
	return AssignmentSnapshot{stamp: snapshot.stamp, phase: PhaseActive, createdAt: snapshot.createdAt, lease: snapshot.lease}, nil
}

// Renew 构造 fencing token 不变且 deadline 严格推进的 snapshot。
//
// observedAt 必须早于旧 expiry，新 expiry 必须晚于旧 expiry；真正的 current 条件比较和
// 提交仍由 PlacementStore.Renew 原子完成。
func (snapshot AssignmentSnapshot) Renew(newExpiresAt time.Time, observedAt time.Time) (AssignmentSnapshot, error) {
	if !snapshot.ValidAt(observedAt) || newExpiresAt.IsZero() || !newExpiresAt.UTC().After(snapshot.lease.ExpiresAt()) {
		return AssignmentSnapshot{}, errors.New("assignment lease cannot renew")
	}
	lease, err := NewLease(snapshot.FencingToken(), newExpiresAt, observedAt)
	if err != nil {
		return AssignmentSnapshot{}, err
	}
	return AssignmentSnapshot{stamp: snapshot.stamp, phase: snapshot.phase, createdAt: snapshot.createdAt, lease: lease}, nil
}

// String 防止默认格式化输出 node、lease deadline 与 fencing material。
func (AssignmentSnapshot) String() string { return assignmentPlaceholder }

// GoString 防止 `%#v` 展开 snapshot 私有字段。
func (AssignmentSnapshot) GoString() string { return assignmentPlaceholder }

// LogValue 让结构化日志只记录稳定占位文本。
func (AssignmentSnapshot) LogValue() slog.Value { return slog.StringValue(assignmentPlaceholder) }

// empty 报告 store 是否返回严格零 snapshot，而不是部分填充的非法值。
func (snapshot AssignmentSnapshot) empty() bool { return snapshot == (AssignmentSnapshot{}) }

// WriteFence 是一次 point-in-time qualification 返回的不可变完整 stamp。
//
// Fence 不代表永久授权；调用方必须把它交给实际持久 mutation 边界重新比较，不能缓存为
// 脱离 assignment 的布尔写权限。
type WriteFence struct {
	// stamp 绑定 qualification 时的全部 current identity。
	stamp AssignmentStamp
}

// NewWriteFence 从完整 current stamp 构造候选持久写入资格。
func NewWriteFence(stamp AssignmentStamp) (WriteFence, error) {
	if !stamp.Valid() {
		return WriteFence{}, errors.New("write fence is incomplete")
	}
	return WriteFence{stamp: stamp}, nil
}

// Stamp 返回后续持久提交必须重新验证的完整 identity。
func (fence WriteFence) Stamp() AssignmentStamp { return fence.stamp }

// Valid 报告 fence 是否来自完整 qualification 结果。
func (fence WriteFence) Valid() bool { return fence.stamp.Valid() }

// String 防止默认格式化扩散 write authority material。
func (WriteFence) String() string { return writeFencePlaceholder }

// GoString 防止 `%#v` 展开 WriteFence 私有字段。
func (WriteFence) GoString() string { return writeFencePlaceholder }

// LogValue 让结构化日志只记录稳定占位文本。
func (WriteFence) LogValue() slog.Value { return slog.StringValue(writeFencePlaceholder) }

// empty 报告 store 是否返回严格零 fence。
func (fence WriteFence) empty() bool { return fence == (WriteFence{}) }

// canonicalPlacementTime 把 adapter-facing absolute time 规范为持久 schema 的 UTC 微秒精度。
func canonicalPlacementTime(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}
