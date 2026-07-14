package placement

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
)

// storeRequestPlaceholder 防止条件请求的默认格式化展开 node、instance 或 fencing material。
const storeRequestPlaceholder = "[REDACTED_PLACEMENT_STORE_REQUEST]"

// StoreOutcome 表达 placement 原子操作的业务决议与提交确定性。
type StoreOutcome uint8

const (
	// StoreOutcomeUnspecified 表示 adapter 没有遵守 store contract。
	StoreOutcomeUnspecified StoreOutcome = iota
	// StoreOutcomeApplied 表示请求在原子线性化点成功：transition 已提交；QualifyWrite 则只
	// 表示资格成立且不修改 assignment。
	StoreOutcomeApplied
	// StoreOutcomeExisting 表示 acquire 解析到 current active assignment，未创建新 instance。
	StoreOutcomeExisting
	// StoreOutcomeReplay 表示相同完整 identity 的 transition 已提交并返回原结果。
	StoreOutcomeReplay
	// StoreOutcomeInProgress 表示 current starting assignment 正由另一个调用推进。
	StoreOutcomeInProgress
	// StoreOutcomeNotFound 表示没有可匹配的 current assignment 或 replay tombstone。
	StoreOutcomeNotFound
	// StoreOutcomeConflict 表示 expected stamp 与 current assignment 不一致。
	StoreOutcomeConflict
	// StoreOutcomeExpired 表示匹配 assignment 的 lease 已到期。
	StoreOutcomeExpired
	// StoreOutcomeNotCommitted 表示依赖失败且可以确认没有提交。
	StoreOutcomeNotCommitted
	// StoreOutcomeCommitUnknown 表示调用方不能确认 transition 是否提交。
	StoreOutcomeCommitUnknown
)

// String 返回 adapter 验证、指标与测试使用的稳定低基数名称。
func (outcome StoreOutcome) String() string {
	switch outcome {
	case StoreOutcomeApplied:
		return "applied"
	case StoreOutcomeExisting:
		return "existing"
	case StoreOutcomeReplay:
		return "replay"
	case StoreOutcomeInProgress:
		return "in_progress"
	case StoreOutcomeNotFound:
		return "not_found"
	case StoreOutcomeConflict:
		return "conflict"
	case StoreOutcomeExpired:
		return "expired"
	case StoreOutcomeNotCommitted:
		return "not_committed"
	case StoreOutcomeCommitUnknown:
		return "commit_unknown"
	default:
		return "unspecified"
	}
}

// ResolveOutcome 表达 current assignment 读取的稳定结果。
type ResolveOutcome uint8

const (
	// ResolveOutcomeUnspecified 表示 adapter 未返回有效读取决议。
	ResolveOutcomeUnspecified ResolveOutcome = iota
	// ResolveOutcomeFound 表示返回目标 PersonalWorld 的完整 current snapshot。
	ResolveOutcomeFound
	// ResolveOutcomeNotFound 表示世界尚未启动或已经休眠。
	ResolveOutcomeNotFound
)

// AssignmentCandidate 是 store 尚未分配 generation/fence 的受信 runtime 候选。
//
// Candidate 的 WorldInstanceID 由 application 预先生成并作为不确定重试 identity；store
// 在原子 acquire/replace 内分配单调 generation 与 fencing token。
type AssignmentCandidate struct {
	// worldID 是候选承载的持久 PersonalWorld identity。
	worldID personalworld.PersonalWorldID
	// instanceID 是本次候选不可复活的 WorldInstance identity。
	instanceID WorldInstanceID
	// nodeID 是 runtime controller 的受信目标节点。
	nodeID RuntimeNodeID
	// createdAt 是 service 单次读取并转换为 UTC 的绝对创建时间。
	createdAt time.Time
	// leaseExpiresAt 是 store 提交后候选 lease 的初始绝对 UTC expiry。
	leaseExpiresAt time.Time
}

// NewAssignmentCandidate 校验 application 交给 acquire/replace 的全部候选事实。
//
// 时间会规范为 UTC 微秒，使首次 Redis/MySQL 结果与后续 hydration 保持精确相等；亚微秒
// 差异不属于可用于区分 WorldInstance candidate 的稳定 identity。
func NewAssignmentCandidate(worldID personalworld.PersonalWorldID, instanceID WorldInstanceID, nodeID RuntimeNodeID, createdAt time.Time, leaseExpiresAt time.Time) (AssignmentCandidate, error) {
	if !worldID.Valid() || !instanceID.Valid() || !nodeID.Valid() || createdAt.IsZero() || leaseExpiresAt.IsZero() {
		return AssignmentCandidate{}, errors.New("assignment candidate is incomplete")
	}
	createdAt = canonicalPlacementTime(createdAt)
	leaseExpiresAt = canonicalPlacementTime(leaseExpiresAt)
	if !leaseExpiresAt.After(createdAt) {
		return AssignmentCandidate{}, errors.New("assignment candidate lease is expired")
	}
	return AssignmentCandidate{worldID: worldID, instanceID: instanceID, nodeID: nodeID, createdAt: createdAt, leaseExpiresAt: leaseExpiresAt}, nil
}

// WorldID 返回候选承载的 PersonalWorld identity。
func (candidate AssignmentCandidate) WorldID() personalworld.PersonalWorldID {
	return candidate.worldID
}

// InstanceID 返回预生成且可用于不确定重试收敛的 WorldInstance identity。
func (candidate AssignmentCandidate) InstanceID() WorldInstanceID { return candidate.instanceID }

// NodeID 返回受信目标 RuntimeNode identity。
func (candidate AssignmentCandidate) NodeID() RuntimeNodeID { return candidate.nodeID }

// CreatedAt 返回移除单调分量后的 UTC 候选创建时间。
func (candidate AssignmentCandidate) CreatedAt() time.Time { return candidate.createdAt }

// LeaseExpiresAt 返回初始 lease 的 UTC 绝对 deadline。
func (candidate AssignmentCandidate) LeaseExpiresAt() time.Time { return candidate.leaseExpiresAt }

// Valid 报告 candidate 是否经过完整构造。
func (candidate AssignmentCandidate) Valid() bool {
	return candidate.worldID.Valid() && candidate.instanceID.Valid() && candidate.nodeID.Valid() && !candidate.createdAt.IsZero() && candidate.leaseExpiresAt.After(candidate.createdAt)
}

// Snapshot 使用 store 原子分配的 generation/fence 构造 starting current snapshot。
func (candidate AssignmentCandidate) Snapshot(generation AssignmentGeneration, token FencingToken, observedAt time.Time) (AssignmentSnapshot, error) {
	stamp, err := NewAssignmentStamp(candidate.WorldID(), candidate.InstanceID(), candidate.NodeID(), generation, token)
	if err != nil {
		return AssignmentSnapshot{}, err
	}
	return NewAssignmentSnapshot(stamp, PhaseStarting, candidate.CreatedAt(), candidate.LeaseExpiresAt(), observedAt)
}

// String 防止默认格式化输出 candidate 内部 identity 与时间。
func (AssignmentCandidate) String() string { return storeRequestPlaceholder }

// GoString 防止 `%#v` 展开 candidate 私有字段。
func (AssignmentCandidate) GoString() string { return storeRequestPlaceholder }

// LogValue 让结构化日志只记录稳定占位文本。
func (AssignmentCandidate) LogValue() slog.Value { return slog.StringValue(storeRequestPlaceholder) }

// AcquireRequest 请求原子解析 existing/in-progress 或提交新 starting assignment。
type AcquireRequest struct {
	// candidate 是 application 生成、由 store 分配 generation/fence 的候选事实。
	candidate AssignmentCandidate
	// observedAt 是 store 必须用于 expiry 比较的受信 UTC 时间。
	observedAt time.Time
}

// NewAcquireRequest 校验按需启动的候选和单次时间 snapshot。
func NewAcquireRequest(candidate AssignmentCandidate, observedAt time.Time) (AcquireRequest, error) {
	if !candidate.Valid() || observedAt.IsZero() || !candidate.LeaseExpiresAt().After(observedAt.UTC()) {
		return AcquireRequest{}, errors.New("acquire request is incomplete")
	}
	return AcquireRequest{candidate: candidate, observedAt: canonicalPlacementTime(observedAt)}, nil
}

// Candidate 返回 store 必须原子决议的候选值副本。
func (request AcquireRequest) Candidate() AssignmentCandidate { return request.candidate }

// ObservedAt 返回 expiry 比较使用的 UTC 时间。
func (request AcquireRequest) ObservedAt() time.Time { return request.observedAt }

// Valid 报告 acquire 请求是否经过完整构造。
func (request AcquireRequest) Valid() bool {
	return request.candidate.Valid() && !request.observedAt.IsZero() && request.candidate.LeaseExpiresAt().After(request.observedAt)
}

// String 防止默认格式化展开 acquire 请求。
func (AcquireRequest) String() string { return storeRequestPlaceholder }

// GoString 防止 `%#v` 展开 acquire 请求。
func (AcquireRequest) GoString() string { return storeRequestPlaceholder }

// LogValue 让结构化日志只记录稳定占位文本。
func (AcquireRequest) LogValue() slog.Value { return slog.StringValue(storeRequestPlaceholder) }

// StampRequest 是 activate、revoke 与 qualify 使用的完整条件 identity 和受信时间。
type StampRequest struct {
	// stamp 是 store 必须与 current 完整比较的 assignment identity。
	stamp AssignmentStamp
	// observedAt 是 lease expiry 比较使用的 UTC 时间。
	observedAt time.Time
}

// NewStampRequest 校验完整 current stamp 和服务端时间 snapshot。
func NewStampRequest(stamp AssignmentStamp, observedAt time.Time) (StampRequest, error) {
	if !stamp.Valid() || observedAt.IsZero() {
		return StampRequest{}, errors.New("stamp request is incomplete")
	}
	return StampRequest{stamp: stamp, observedAt: canonicalPlacementTime(observedAt)}, nil
}

// Stamp 返回 store 必须完整比较的 assignment identity。
func (request StampRequest) Stamp() AssignmentStamp { return request.stamp }

// ObservedAt 返回 expiry 比较使用的 UTC 时间。
func (request StampRequest) ObservedAt() time.Time { return request.observedAt }

// Valid 报告请求是否来自完整构造路径。
func (request StampRequest) Valid() bool {
	return request.stamp.Valid() && !request.observedAt.IsZero()
}

// String 防止默认格式化展开条件请求。
func (StampRequest) String() string { return storeRequestPlaceholder }

// GoString 防止 `%#v` 展开条件请求。
func (StampRequest) GoString() string { return storeRequestPlaceholder }

// LogValue 让结构化日志只记录稳定占位文本。
func (StampRequest) LogValue() slog.Value { return slog.StringValue(storeRequestPlaceholder) }

// RenewRequest 请求在旧 lease 到期前原子推进 expiry，stamp 与 token 保持不变。
type RenewRequest struct {
	// condition 保存完整 current stamp 与受信观测时间。
	condition StampRequest
	// leaseExpiresAt 是严格晚于旧 expiry 的新 UTC deadline。
	leaseExpiresAt time.Time
}

// NewRenewRequest 校验完整 stamp、观测时间与新的未来 expiry。
//
// 新 expiry 是否严格晚于 store current expiry 只能在原子操作内确认；constructor 先保证它
// 晚于 observedAt，避免明显过期值进入 adapter。
func NewRenewRequest(stamp AssignmentStamp, observedAt time.Time, leaseExpiresAt time.Time) (RenewRequest, error) {
	condition, err := NewStampRequest(stamp, observedAt)
	if err != nil || leaseExpiresAt.IsZero() || !leaseExpiresAt.UTC().After(condition.ObservedAt()) {
		return RenewRequest{}, errors.New("renew request is incomplete")
	}
	return RenewRequest{condition: condition, leaseExpiresAt: canonicalPlacementTime(leaseExpiresAt)}, nil
}

// Condition 返回 renew 必须完整比较的 stamp 与时间。
func (request RenewRequest) Condition() StampRequest { return request.condition }

// LeaseExpiresAt 返回目标 UTC deadline。
func (request RenewRequest) LeaseExpiresAt() time.Time { return request.leaseExpiresAt }

// Valid 报告 renew 请求是否经过完整构造。
func (request RenewRequest) Valid() bool {
	return request.condition.Valid() && request.leaseExpiresAt.After(request.condition.ObservedAt())
}

// String 防止默认格式化展开 renew 请求。
func (RenewRequest) String() string { return storeRequestPlaceholder }

// GoString 防止 `%#v` 展开 renew 请求。
func (RenewRequest) GoString() string { return storeRequestPlaceholder }

// LogValue 让结构化日志只记录稳定占位文本。
func (RenewRequest) LogValue() slog.Value { return slog.StringValue(storeRequestPlaceholder) }

// ReplaceRequest 请求原子撤销 predecessor 并创建 generation/fence 更大的 successor。
type ReplaceRequest struct {
	// expected 是必须在 cutover 线性化点仍为 current 的 predecessor stamp。
	expected AssignmentStamp
	// successor 是预生成 identity 的 starting assignment 候选。
	successor AssignmentCandidate
	// observedAt 是 predecessor expiry 与 successor lease 使用的 UTC 时间。
	observedAt time.Time
}

// NewReplaceRequest 校验 predecessor、successor 和 break-before-make 时间边界。
func NewReplaceRequest(expected AssignmentStamp, successor AssignmentCandidate, observedAt time.Time) (ReplaceRequest, error) {
	if !expected.Valid() || !successor.Valid() || observedAt.IsZero() || successor.WorldID() != expected.WorldID() || successor.InstanceID() == expected.InstanceID() || !successor.LeaseExpiresAt().After(observedAt.UTC()) {
		return ReplaceRequest{}, errors.New("replace request is incomplete or reuses predecessor")
	}
	return ReplaceRequest{expected: expected, successor: successor, observedAt: canonicalPlacementTime(observedAt)}, nil
}

// Expected 返回 cutover 必须完整比较的 predecessor stamp。
func (request ReplaceRequest) Expected() AssignmentStamp { return request.expected }

// Successor 返回 store 必须分配新 generation/fence 的候选值。
func (request ReplaceRequest) Successor() AssignmentCandidate { return request.successor }

// ObservedAt 返回 cutover 使用的 UTC 时间。
func (request ReplaceRequest) ObservedAt() time.Time { return request.observedAt }

// Valid 报告 replace 请求是否满足 world 一致、instance 不复用和时间约束。
func (request ReplaceRequest) Valid() bool {
	return request.expected.Valid() && request.successor.Valid() && !request.observedAt.IsZero() && request.successor.WorldID() == request.expected.WorldID() && request.successor.InstanceID() != request.expected.InstanceID() && request.successor.LeaseExpiresAt().After(request.observedAt)
}

// String 防止默认格式化展开 replace 请求。
func (ReplaceRequest) String() string { return storeRequestPlaceholder }

// GoString 防止 `%#v` 展开 replace 请求。
func (ReplaceRequest) GoString() string { return storeRequestPlaceholder }

// LogValue 让结构化日志只记录稳定占位文本。
func (ReplaceRequest) LogValue() slog.Value { return slog.StringValue(storeRequestPlaceholder) }

// PlacementStore 线性化 PersonalWorld 的 current assignment、lease 与 fencing high-watermark。
//
// 实现必须允许并发调用，并在同一 PersonalWorld scope 内原子比较完整 stamp。ctx deadline
// 只限制调用方等待，不能证明 transition 未提交；无法确认时必须返回 CommitUnknown。
// Success outcome 返回完整结果且 error 为 nil；NotCommitted/CommitUnknown 必须返回零结果
// 和非 nil error，调用方不得从错误字符串猜测提交状态。
type PlacementStore interface {
	// Resolve 返回目标 PersonalWorld 的单一一致性 current snapshot；Found 可以返回已过期值供
	// application 触发 replacement，但 snapshot 必须结构完整且绑定目标 world。
	Resolve(ctx context.Context, worldID personalworld.PersonalWorldID, observedAt time.Time) (AssignmentSnapshot, ResolveOutcome, error)
	// Acquire 原子返回 existing active、starting in-progress，或在无有效 current 时分配严格
	// 增长的 generation/fence 并提交 candidate starting snapshot。
	Acquire(ctx context.Context, request AcquireRequest) (AssignmentSnapshot, StoreOutcome, error)
	// Activate 只允许完整 current starting stamp 在 lease 到期前转为 active；重复的相同 active
	// stamp 返回 Replay，任何 successor 已存在时返回 Conflict。
	Activate(ctx context.Context, request StampRequest) (AssignmentSnapshot, StoreOutcome, error)
	// Renew 在 lease 到期前原子比较完整 current stamp，并在 token 不变时推进 expiry。
	Renew(ctx context.Context, request RenewRequest) (AssignmentSnapshot, StoreOutcome, error)
	// Revoke 原子移除完整匹配的 current assignment；实现必须保留有界 replay 证据，使相同
	// stamp 的响应丢失重试不会误伤 successor。
	Revoke(ctx context.Context, request StampRequest) (AssignmentSnapshot, StoreOutcome, error)
	// Replace 在一个线性化点撤销 expected predecessor，并以更大的 generation/fence 提交
	// successor starting snapshot；相同 successor identity 重试必须返回 Replay。
	Replace(ctx context.Context, request ReplaceRequest) (AssignmentSnapshot, StoreOutcome, error)
	// QualifyWrite 只在完整 stamp 为 current active 且 lease 未过期时返回 point-in-time fence；
	// Applied 仅表示原子资格检查成立，不提交状态变更，其他 outcome 必须返回零值 WriteFence。
	QualifyWrite(ctx context.Context, request StampRequest) (WriteFence, StoreOutcome, error)
}
