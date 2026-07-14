package visitsession

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
)

// referenceStore 仅在测试中提供 VisitSessionStore 的并发线性化参考实现。
//
// mutex 同时保护 world active index、snapshot 与 command replay，锁内不调用外部代码。
// failure injection 用于证明 not-committed、commit-unknown 和响应丢失恢复边界；该类型
// 不得移动到 production package 或作为 Redis 不可用时的 memory fallback。
type referenceStore struct {
	// mu 保护以下全部 map 与一次性故障注入字段。
	mu sync.Mutex
	// activeByWorld 把 PersonalWorld 精确映射到至多一个未关闭 VisitSessionID。
	activeByWorld map[string]VisitSessionID
	// snapshots 保存 active 与 terminal snapshot，便于按 ID 诊断与 replay。
	snapshots map[string]Snapshot
	// commands 按全局 CommandID 保存 create 或 mutation 首次完整结果。
	commands map[string]referenceCommand
	// nextCreateOutcome 在下一次 create 线性化前返回指定故障结果。
	nextCreateOutcome CreateOutcome
	// nextMutationOutcome 在下一次 commit 线性化前返回指定故障结果。
	nextMutationOutcome MutationOutcome
	// nextError 是下一次故障结果配套的 dependency error。
	nextError error
	// loseMutationResponse 使下一次 mutation 先提交、再模拟调用方未收到响应。
	loseMutationResponse bool
}

// referenceCommand 保存同一 CommandID 的 fingerprint 与唯一结果类型。
type referenceCommand struct {
	// fingerprint 决议 replay 与 idempotency conflict。
	fingerprint CommandFingerprint
	// create 只对 Open command 非零。
	create CreateResult
	// mutation 只对 transition command 非零。
	mutation MutationResult
}

// newReferenceStore 创建所有索引均为空的测试 store。
func newReferenceStore() *referenceStore {
	return &referenceStore{activeByWorld: make(map[string]VisitSessionID), snapshots: make(map[string]Snapshot), commands: make(map[string]referenceCommand)}
}

// Create 在线性化点先决议 command，再维护 world active unique index。
func (store *referenceStore) Create(_ context.Context, record CreateRecord) (CreateResult, CreateOutcome, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if command, exists := store.commands[record.commandID.value]; exists {
		if !command.fingerprint.Equal(record.fingerprint) || !command.create.snapshot.Valid() {
			return CreateResult{}, CreateOutcomeIdempotencyConflict, nil
		}
		return command.create, CreateOutcomeReplay, nil
	}
	if store.nextCreateOutcome != CreateOutcomeUnspecified {
		outcome, err := store.nextCreateOutcome, store.nextError
		store.nextCreateOutcome, store.nextError = CreateOutcomeUnspecified, nil
		return CreateResult{}, outcome, err
	}
	worldKey := record.candidate.WorldID().String()
	if activeID, exists := store.activeByWorld[worldKey]; exists {
		existing, ok := store.snapshots[activeID.value]
		if !ok || !existing.Valid() || existing.Lifecycle() == LifecycleClosed {
			return CreateResult{}, CreateOutcomeUnspecified, errors.New("reference active index is inconsistent")
		}
		result, _ := NewExistingResult(existing)
		return result, CreateOutcomeExisting, nil
	}
	result, _ := NewCreateResult(record.candidate, record.commandID, record.fingerprint)
	store.snapshots[record.candidate.ID().value] = record.candidate
	store.activeByWorld[worldKey] = record.candidate.ID()
	store.commands[record.commandID.value] = referenceCommand{fingerprint: record.fingerprint, create: result}
	return result, CreateOutcomeCreated, nil
}

// ResolveActive 在线性化读取 world index 与对应完整 snapshot。
func (store *referenceStore) ResolveActive(_ context.Context, worldID personalworld.PersonalWorldID) (Snapshot, ResolveOutcome, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	id, exists := store.activeByWorld[worldID.String()]
	if !exists {
		return Snapshot{}, ResolveOutcomeNotFound, nil
	}
	snapshot, exists := store.snapshots[id.value]
	if !exists {
		return Snapshot{}, ResolveOutcomeUnspecified, errors.New("reference active snapshot is missing")
	}
	return snapshot, ResolveOutcomeFound, nil
}

// FindByID 在线性化读取 active 或 terminal snapshot。
func (store *referenceStore) FindByID(_ context.Context, visitSessionID VisitSessionID) (Snapshot, ResolveOutcome, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	snapshot, exists := store.snapshots[visitSessionID.value]
	if !exists {
		return Snapshot{}, ResolveOutcomeNotFound, nil
	}
	return snapshot, ResolveOutcomeFound, nil
}

// Commit 先决议 replay/conflict，再比较 revision 并原子保存 snapshot/result/index。
func (store *referenceStore) Commit(_ context.Context, record TransitionRecord) (MutationResult, MutationOutcome, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if command, exists := store.commands[record.commandID.value]; exists {
		if !command.fingerprint.Equal(record.fingerprint) || mutationResultEmpty(command.mutation) {
			return MutationResult{}, MutationOutcomeIdempotencyConflict, nil
		}
		return command.mutation, MutationOutcomeReplay, nil
	}
	if store.nextMutationOutcome != MutationOutcomeUnspecified {
		outcome, err := store.nextMutationOutcome, store.nextError
		store.nextMutationOutcome, store.nextError = MutationOutcomeUnspecified, nil
		return MutationResult{}, outcome, err
	}
	current, exists := store.snapshots[record.visitSessionID.value]
	if !exists {
		return MutationResult{}, MutationOutcomeNotFound, nil
	}
	if current.Revision() != record.expectedRevision {
		return MutationResult{}, MutationOutcomeRevisionConflict, nil
	}
	if !record.HasResult() {
		return MutationResult{}, MutationOutcomeInvalidState, nil
	}
	result := record.result
	if result.snapshot.ID() != current.ID() || result.snapshot.Revision() != current.Revision()+1 {
		return MutationResult{}, MutationOutcomeInvalidState, nil
	}
	store.snapshots[current.ID().value] = result.snapshot
	store.commands[record.commandID.value] = referenceCommand{fingerprint: record.fingerprint, mutation: result}
	if result.snapshot.Lifecycle() == LifecycleClosed {
		delete(store.activeByWorld, result.snapshot.WorldID().String())
	}
	if store.loseMutationResponse {
		store.loseMutationResponse = false
		return MutationResult{}, MutationOutcomeCommitUnknown, errors.New("simulated response loss")
	}
	return result, MutationOutcomeApplied, nil
}

// failNextMutation 配置下一次 mutation 在提交前返回确定性或未知失败。
func (store *referenceStore) failNextMutation(outcome MutationOutcome, err error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.nextMutationOutcome, store.nextError = outcome, err
}

// loseNextMutationResponse 配置下一次 mutation 已提交但调用方收到 commit-unknown。
func (store *referenceStore) loseNextMutationResponse() {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.loseMutationResponse = true
}

// fakeClock 提供可并发读取且由测试显式推进的 UTC 时间。
type fakeClock struct {
	// mu 保护可由并发测试推进的绝对时间。
	mu sync.Mutex
	// now 是当前测试选择的绝对时间快照。
	now time.Time
}

// Now 返回当前测试绝对时间。
func (clock *fakeClock) Now() time.Time { clock.mu.Lock(); defer clock.mu.Unlock(); return clock.now }

// Set 显式推进或回退测试时间，以验证 fingerprint 排除 observedAt。
func (clock *fakeClock) Set(value time.Time) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = value
}

// sequenceIDs 为并发测试生成唯一且可预测的安全 ASCII 材料。
type sequenceIDs struct {
	// mu 保护递增测试序列，避免并发调用返回重复材料。
	mu sync.Mutex
	// next 是下一次 NewID 递增前的测试计数。
	next uint64
}

// NewID 在线性化点返回递增测试材料；生产不可使用可预测 generator。
func (ids *sequenceIDs) NewID() (string, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.next++
	return "ref" + strconvFormat(ids.next), nil
}

// strconvFormat 避免测试 generator 依赖格式化 identity 的默认脱敏路径。
func strconvFormat(value uint64) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = digits[value%10]
		value /= 10
	}
	return string(buffer[index:])
}
