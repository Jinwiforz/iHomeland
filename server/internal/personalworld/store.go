package personalworld

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
)

// archiveMutationPlaceholder 是 command/record 所有默认输出唯一允许的稳定文本。
const archiveMutationPlaceholder = "[REDACTED_ARCHIVE_MUTATION]"

// Clock 为 PersonalWorld created time 提供可替换绝对时间源。
//
// 生产实现复用 Composition Root 的 SystemClock；一次 ensure 只能读取一次并把同一 UTC
// snapshot 交给 repository，revision 而不是 wall clock 负责 mutation 顺序。
type Clock interface {
	// Now 返回当前绝对时间；实现必须允许并发调用，调用方负责校验非零并转换为 UTC 持久事实。
	Now() time.Time
}

// IDGenerator 创建不可由客户端预测或选择的 PersonalWorld identity 材料。
type IDGenerator interface {
	// NewID 并发安全地返回不可预测安全 ASCII 材料；personalworld package 负责添加实体前缀和校验。
	NewID() (string, error)
}

// EnsureOutcome 表达 primary world 原子创建的确定性或不确定结果。
type EnsureOutcome uint8

const (
	// EnsureOutcomeUnspecified 表示 adapter 没有遵守 repository contract。
	EnsureOutcomeUnspecified EnsureOutcome = iota
	// EnsureOutcomeCreated 表示候选 primary world 已原子提交。
	EnsureOutcomeCreated
	// EnsureOutcomeExisting 表示 owner 已有 primary world，本次没有新提交。
	EnsureOutcomeExisting
	// EnsureOutcomeNotCommitted 表示依赖失败且可以确认没有提交。
	EnsureOutcomeNotCommitted
	// EnsureOutcomeCommitUnknown 表示调用方不能确认 create transaction 是否提交。
	EnsureOutcomeCommitUnknown
)

// FindOutcome 表达按 PersonalWorldID 读取 snapshot 的稳定结果。
type FindOutcome uint8

const (
	// FindOutcomeUnspecified 表示 adapter 未返回有效结果。
	FindOutcomeUnspecified FindOutcome = iota
	// FindOutcomeFound 表示返回了完整持久 snapshot。
	FindOutcomeFound
	// FindOutcomeNotFound 表示世界 identity 不存在。
	FindOutcomeNotFound
)

// MutationOutcome 表达 archive transaction 的业务决议与提交确定性。
type MutationOutcome uint8

const (
	// MutationOutcomeUnspecified 表示 adapter 没有遵守 repository contract。
	MutationOutcomeUnspecified MutationOutcome = iota
	// MutationOutcomeApplied 表示 archive 与 idempotency result 已原子提交。
	MutationOutcomeApplied
	// MutationOutcomeReplay 表示相同 key/fingerprint 已提交并返回原结果。
	MutationOutcomeReplay
	// MutationOutcomeNotFound 表示 PersonalWorldID 不存在且没有提交。
	MutationOutcomeNotFound
	// MutationOutcomeRevisionConflict 表示 expected revision 已过期且没有提交。
	MutationOutcomeRevisionConflict
	// MutationOutcomeIdempotencyConflict 表示相同 key 已绑定不同 fingerprint。
	MutationOutcomeIdempotencyConflict
	// MutationOutcomeInvalidState 表示当前 lifecycle 不允许 archive。
	MutationOutcomeInvalidState
	// MutationOutcomeNotCommitted 表示依赖失败且可以确认没有提交。
	MutationOutcomeNotCommitted
	// MutationOutcomeCommitUnknown 表示调用方不能确认 mutation transaction 是否提交。
	MutationOutcomeCommitUnknown
)

// ArchiveCommand 是可信 application 边界接受的具体 Owner mutation。
//
// 字段保持私有，调用方必须先用 NewArchiveCommand 验证 actor、world、revision 和 key；
// actor 必须来自 AuthContext 等可信边界，不能直接复制客户端 payload identity。
type ArchiveCommand struct {
	// actorID 是当前可信认证身份对应的 PlayerID。
	actorID account.PlayerID
	// worldID 是 mutation 目标 PersonalWorld identity。
	worldID PersonalWorldID
	// expectedRevision 是调用方最后读取的持久版本。
	expectedRevision Revision
	// idempotencyKey 与 actorID 共同让响应丢失后的相同 command 安全收敛。
	idempotencyKey IdempotencyKey
}

// NewArchiveCommand 校验具体 archive use case 的全部调用边界值。
//
// 构造只确认值完整，不查询 owner 或授予权限；actor 必须由调用方从可信认证上下文取得，
// 最终 Owner authorization 由 ArchiveWorld 使用 repository snapshot 完成。
func NewArchiveCommand(actorID account.PlayerID, worldID PersonalWorldID, expectedRevision Revision, idempotencyKey IdempotencyKey) (ArchiveCommand, error) {
	if !actorID.Valid() || !worldID.Valid() || !expectedRevision.Valid() || !idempotencyKey.Valid() {
		return ArchiveCommand{}, errors.New("archive command is incomplete")
	}
	return ArchiveCommand{actorID: actorID, worldID: worldID, expectedRevision: expectedRevision, idempotencyKey: idempotencyKey}, nil
}

// String 防止默认格式化绕过私有字段并展开原始 idempotency key。
func (ArchiveCommand) String() string { return archiveMutationPlaceholder }

// GoString 防止 `%#v` 输出 ArchiveCommand 的内部字段。
func (ArchiveCommand) GoString() string { return archiveMutationPlaceholder }

// LogValue 让结构化日志只记录稳定占位文本，不展开 actor、world 或 key。
func (ArchiveCommand) LogValue() slog.Value { return slog.StringValue(archiveMutationPlaceholder) }

// ActorID 返回可信调用边界提供的 PlayerID。
func (command ArchiveCommand) ActorID() account.PlayerID { return command.actorID }

// WorldID 返回 mutation 目标 PersonalWorldID。
func (command ArchiveCommand) WorldID() PersonalWorldID { return command.worldID }

// ExpectedRevision 返回本次 compare-and-commit 前置版本。
func (command ArchiveCommand) ExpectedRevision() Revision { return command.expectedRevision }

// IdempotencyKey 返回 repository 原子记录的 command identity。
func (command ArchiveCommand) IdempotencyKey() IdempotencyKey { return command.idempotencyKey }

// Valid 报告 command 是否经过完整构造。
func (command ArchiveCommand) Valid() bool {
	return command.actorID.Valid() && command.worldID.Valid() && command.expectedRevision.Valid() && command.idempotencyKey.Valid()
}

// ArchiveRecord 是 repository 必须原子决议并提交的完整 archive mutation。
type ArchiveRecord struct {
	// command 保存可信 actor、目标、expected revision 与 idempotency identity。
	command ArchiveCommand
	// target 是 mutation 成功后必须持久化的 archived snapshot。
	target Snapshot
	// fingerprint 绑定除 idempotency key 外的全部 command 语义。
	fingerprint CommandFingerprint
}

// NewArchiveRecord 根据已加载世界的 immutable facts 与 command 构造原子提交记录。
//
// 当前 snapshot 可能已经是同 command 的 replay 结果，因此 target revision 必须从
// command.expectedRevision 推导，而不是盲目使用当前 revision 再加一。Repository 先检查
// idempotency record，再以当前持久事实决议 revision 与 lifecycle。
func NewArchiveRecord(command ArchiveCommand, current Snapshot) (ArchiveRecord, error) {
	if !command.Valid() || !current.Valid() || current.ID() != command.WorldID() || current.OwnerID() != command.ActorID() {
		return ArchiveRecord{}, errors.New("archive record facts are inconsistent")
	}
	targetRevision, err := command.ExpectedRevision().next()
	if err != nil {
		return ArchiveRecord{}, err
	}
	target, err := NewSnapshot(current.ID(), current.OwnerID(), LifecycleArchived, targetRevision, current.CreatedAt())
	if err != nil {
		return ArchiveRecord{}, err
	}
	fingerprint := fingerprintArchiveCommand(command.WorldID(), command.ActorID().String(), command.ExpectedRevision(), LifecycleArchived)
	return ArchiveRecord{command: command, target: target, fingerprint: fingerprint}, nil
}

// String 防止 repository record 的默认格式化间接展开 ArchiveCommand。
func (ArchiveRecord) String() string { return archiveMutationPlaceholder }

// GoString 防止 `%#v` 绕过 ArchiveRecord 的普通 String 脱敏边界。
func (ArchiveRecord) GoString() string { return archiveMutationPlaceholder }

// LogValue 让结构化日志只记录稳定占位文本，不输出 target snapshot 或 fingerprint。
func (ArchiveRecord) LogValue() slog.Value { return slog.StringValue(archiveMutationPlaceholder) }

// Command 返回受校验的 archive use case 输入值副本。
func (record ArchiveRecord) Command() ArchiveCommand { return record.command }

// Target 返回成功 transaction 必须提交的 snapshot。
func (record ArchiveRecord) Target() Snapshot { return record.target }

// Fingerprint 返回 repository 与 idempotency result 原子保存的 command 摘要。
func (record ArchiveRecord) Fingerprint() CommandFingerprint { return record.fingerprint }

// Valid 报告 record 的 command、target 和 fingerprint 是否相互一致。
func (record ArchiveRecord) Valid() bool {
	if !record.command.Valid() || !record.target.Valid() || !record.fingerprint.Valid() {
		return false
	}
	if record.target.ID() != record.command.WorldID() || record.target.OwnerID() != record.command.ActorID() || record.target.Lifecycle() != LifecycleArchived {
		return false
	}
	next, err := record.command.ExpectedRevision().next()
	return err == nil && record.target.Revision() == next && record.fingerprint.Equal(fingerprintArchiveCommand(record.command.WorldID(), record.command.ActorID().String(), record.command.ExpectedRevision(), LifecycleArchived))
}

// MutationResult 是 applied/replay outcome 必须返回的已提交事实。
type MutationResult struct {
	// world 是 archive transaction 已提交的完整 snapshot。
	world Snapshot
	// fingerprint 必须与调用 record 完全相同。
	fingerprint CommandFingerprint
}

// NewMutationResult 构造 repository 可返回的 applied/replay 结果。
func NewMutationResult(world Snapshot, fingerprint CommandFingerprint) (MutationResult, error) {
	if !world.Valid() || !fingerprint.Valid() {
		return MutationResult{}, errors.New("mutation result is incomplete")
	}
	return MutationResult{world: world, fingerprint: fingerprint}, nil
}

// World 返回 transaction 已提交的 PersonalWorld snapshot。
func (result MutationResult) World() Snapshot { return result.world }

// Fingerprint 返回 transaction 绑定的 command 摘要。
func (result MutationResult) Fingerprint() CommandFingerprint { return result.fingerprint }

// Valid 报告 result 是否包含完整的已提交事实。
func (result MutationResult) Valid() bool { return result.world.Valid() && result.fingerprint.Valid() }

// empty 报告非成功 outcome 是否返回了严格零 mutation result。
func (result MutationResult) empty() bool { return result == (MutationResult{}) }

// PersonalWorldRepository 只暴露 W0 用例真正需要的原子操作。
//
// 实现必须允许并发调用，并分别用 owner unique key、expected revision 与 idempotency key
// 线性化创建和 mutation。ctx deadline 只限制调用方等待，不能被解释为 transaction 必定
// 回滚；无法确认时必须返回对应 commit-unknown outcome。成功或业务决议正常返回时 error
// 必须为 nil；依赖故障返回非 nil error 时，outcome 仍必须如实表达 none、unknown 或
// committed 证据，不能依靠错误字符串推断 transaction 结果。
type PersonalWorldRepository interface {
	// EnsurePrimary 原子创建 owner 的唯一 primary world，或返回已存在的同一事实。
	// Created 必须返回与 candidate 完全相同的 snapshot；Existing 必须返回相同 owner 的完整
	// snapshot；NotCommitted 与 CommitUnknown 必须返回零 snapshot。
	EnsurePrimary(ctx context.Context, candidate Snapshot) (Snapshot, EnsureOutcome, error)
	// FindByID 返回单一一致性 snapshot；Found 必须返回目标 ID 的完整值，NotFound 必须返回
	// 零 snapshot，依赖错误不得伪装为 not found。
	FindByID(ctx context.Context, id PersonalWorldID) (Snapshot, FindOutcome, error)
	// CommitArchive 在一个 transaction 内先按 `(actorID, idempotencyKey)` 决议 replay/conflict，
	// 再比较当前 owner、revision 与 lifecycle，并原子保存 target、fingerprint 和 replay result。
	// Applied/Replay 必须返回与 record 一致的完整 result；其他 outcome 必须返回零 result。
	CommitArchive(ctx context.Context, record ArchiveRecord) (MutationResult, MutationOutcome, error)
}
