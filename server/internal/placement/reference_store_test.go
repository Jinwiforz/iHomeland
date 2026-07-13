package placement

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
)

// referenceOperation 标识测试需要覆盖的单次 store 方法。
type referenceOperation uint8

const (
	// referenceOperationAcquire 覆盖下一次 acquire 结果。
	referenceOperationAcquire referenceOperation = iota + 1
	// referenceOperationActivate 覆盖下一次 activate 结果。
	referenceOperationActivate
	// referenceOperationRenew 覆盖下一次 renew 结果。
	referenceOperationRenew
	// referenceOperationRevoke 覆盖下一次 revoke 结果。
	referenceOperationRevoke
	// referenceOperationReplace 覆盖下一次 replace 结果。
	referenceOperationReplace
)

// storeOverride 允许 application tests 注入 adapter 违约或依赖故障组合。
type storeOverride struct {
	// snapshot 是被覆盖方法返回的 assignment 值。
	snapshot AssignmentSnapshot
	// outcome 是被覆盖方法返回的 store 决议。
	outcome StoreOutcome
	// err 是被覆盖方法返回的依赖故障。
	err error
}

// replacementKey 绑定 predecessor 与预生成 successor，模拟 production store 的 replay identity。
type replacementKey struct {
	// expected 是第一次 replace 使用的 predecessor stamp。
	expected AssignmentStamp
	// successorID 是不确定重试复用的 instance identity。
	successorID WorldInstanceID
	// targetNodeID 是 successor 的目标节点。
	targetNodeID RuntimeNodeID
}

// referenceStore 使用单 mutex 模拟 current assignment、high-watermark 与 replay 原子边界。
//
// 所有 map、counter 和 failure injection 都由 mutex 保护；方法持锁期间不调用外部代码，
// 使并发测试可以由 race detector 验证。该类型只存在于 `_test.go`，不模拟 Redis 持久恢复。
type referenceStore struct {
	// mutex 保护全部 current、high-watermark、replay 和 override 状态。
	mutex sync.Mutex
	// current 按 PersonalWorldID 保存最多一个 current assignment。
	current map[string]AssignmentSnapshot
	// generationHigh 保存已发出的最大 generation，即使 current 被撤销也不回退。
	generationHigh map[string]uint64
	// fenceHigh 保存已发出的最大 fencing token，即使 current 被撤销也不回退。
	fenceHigh map[string]uint64
	// revoked 在单个测试生命周期保存 sleep/replacement replay 证据；production adapter 必须另行限界。
	revoked map[AssignmentStamp]AssignmentSnapshot
	// replacements 保存原 predecessor/successor transition 的 replay 关系。
	replacements map[replacementKey]AssignmentSnapshot
	// highWatermarkAvailable 模拟 allocator 恢复是否可以证明不回退。
	highWatermarkAvailable bool
	// overrides 为下一次指定 mutation 返回任意 adapter result，读取后立即删除。
	overrides map[referenceOperation]storeOverride
	// resolveOverrideEnabled 允许下一次 resolve 返回 malformed 组合。
	resolveOverrideEnabled bool
	// resolveSnapshot 是覆盖 resolve 返回的 snapshot。
	resolveSnapshot AssignmentSnapshot
	// resolveOutcome 是覆盖 resolve 返回的读取决议。
	resolveOutcome ResolveOutcome
	// resolveErr 是覆盖 resolve 返回的依赖故障。
	resolveErr error
	// qualifyOverrideEnabled 允许下一次 qualification 返回任意组合。
	qualifyOverrideEnabled bool
	// qualifyFence 是覆盖 qualification 返回的 fence。
	qualifyFence WriteFence
	// qualifyOutcome 是覆盖 qualification 返回的 store 决议。
	qualifyOutcome StoreOutcome
	// qualifyErr 是覆盖 qualification 返回的依赖故障。
	qualifyErr error
	// acquireCommitUnknown 让下一次 acquire 提交后模拟响应丢失。
	acquireCommitUnknown bool
	// activateCommitUnknown 让下一次 activate 提交后模拟响应丢失。
	activateCommitUnknown bool
	// revokeCommitUnknown 让下一次 revoke 提交后模拟响应丢失。
	revokeCommitUnknown bool
	// replaceCommitUnknown 让下一次 replace 提交后模拟响应丢失。
	replaceCommitUnknown bool
}

// newReferenceStore 创建 high-watermark 可用且没有 current assignment 的测试 store。
func newReferenceStore() *referenceStore {
	return &referenceStore{
		current:                make(map[string]AssignmentSnapshot),
		generationHigh:         make(map[string]uint64),
		fenceHigh:              make(map[string]uint64),
		revoked:                make(map[AssignmentStamp]AssignmentSnapshot),
		replacements:           make(map[replacementKey]AssignmentSnapshot),
		highWatermarkAvailable: true,
		overrides:              make(map[referenceOperation]storeOverride),
	}
}

// Resolve 返回 mutex 内读取的单一一致性 current snapshot，包括结构有效但已过期的值。
func (store *referenceStore) Resolve(ctx context.Context, worldID personalworld.PersonalWorldID, observedAt time.Time) (AssignmentSnapshot, ResolveOutcome, error) {
	if err := ctx.Err(); err != nil {
		return AssignmentSnapshot{}, ResolveOutcomeUnspecified, err
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.resolveOverrideEnabled {
		store.resolveOverrideEnabled = false
		return store.resolveSnapshot, store.resolveOutcome, store.resolveErr
	}
	if !worldID.Valid() || observedAt.IsZero() {
		return AssignmentSnapshot{}, ResolveOutcomeUnspecified, errors.New("resolve input is invalid")
	}
	snapshot, found := store.current[worldID.String()]
	if !found {
		return AssignmentSnapshot{}, ResolveOutcomeNotFound, nil
	}
	return snapshot, ResolveOutcomeFound, nil
}

// Acquire 原子解析有效 current，或替换过期值并推进两个 high-watermark。
func (store *referenceStore) Acquire(ctx context.Context, request AcquireRequest) (AssignmentSnapshot, StoreOutcome, error) {
	if err := ctx.Err(); err != nil {
		return AssignmentSnapshot{}, StoreOutcomeNotCommitted, err
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if override, found := store.takeOverride(referenceOperationAcquire); found {
		return override.snapshot, override.outcome, override.err
	}
	if !request.Valid() {
		return AssignmentSnapshot{}, StoreOutcomeNotCommitted, errors.New("acquire request is invalid")
	}
	worldKey := request.Candidate().WorldID().String()
	if current, found := store.current[worldKey]; found && current.ValidAt(request.ObservedAt()) {
		if current.Phase() == PhaseActive {
			return current, StoreOutcomeExisting, nil
		}
		return current, StoreOutcomeInProgress, nil
	} else if found {
		store.revoked[current.Stamp()] = current
	}
	if !store.highWatermarkAvailable {
		return AssignmentSnapshot{}, StoreOutcomeNotCommitted, errors.New("placement high-watermark is unavailable")
	}
	snapshot, err := store.allocateStarting(request.Candidate(), request.ObservedAt())
	if err != nil {
		return AssignmentSnapshot{}, StoreOutcomeNotCommitted, err
	}
	store.current[worldKey] = snapshot
	if store.acquireCommitUnknown {
		store.acquireCommitUnknown = false
		return AssignmentSnapshot{}, StoreOutcomeCommitUnknown, errors.New("acquire response lost after commit")
	}
	return snapshot, StoreOutcomeApplied, nil
}

// Activate 原子发布匹配且未过期的 starting assignment。
func (store *referenceStore) Activate(ctx context.Context, request StampRequest) (AssignmentSnapshot, StoreOutcome, error) {
	if err := ctx.Err(); err != nil {
		return AssignmentSnapshot{}, StoreOutcomeNotCommitted, err
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if override, found := store.takeOverride(referenceOperationActivate); found {
		return override.snapshot, override.outcome, override.err
	}
	if !request.Valid() {
		return AssignmentSnapshot{}, StoreOutcomeNotCommitted, errors.New("activate request is invalid")
	}
	current, found := store.current[request.Stamp().WorldID().String()]
	if !found {
		return AssignmentSnapshot{}, StoreOutcomeNotFound, nil
	}
	if !current.Stamp().Equal(request.Stamp()) {
		return AssignmentSnapshot{}, StoreOutcomeConflict, nil
	}
	if !current.ValidAt(request.ObservedAt()) {
		return AssignmentSnapshot{}, StoreOutcomeExpired, nil
	}
	if current.Phase() == PhaseActive {
		return current, StoreOutcomeReplay, nil
	}
	active, err := current.Activate(request.ObservedAt())
	if err != nil {
		return AssignmentSnapshot{}, StoreOutcomeNotCommitted, err
	}
	store.current[current.WorldID().String()] = active
	if store.activateCommitUnknown {
		store.activateCommitUnknown = false
		return AssignmentSnapshot{}, StoreOutcomeCommitUnknown, errors.New("activate response lost after commit")
	}
	return active, StoreOutcomeApplied, nil
}

// Renew 原子比较完整 stamp，并只在 lease 有效时推进 deadline。
func (store *referenceStore) Renew(ctx context.Context, request RenewRequest) (AssignmentSnapshot, StoreOutcome, error) {
	if err := ctx.Err(); err != nil {
		return AssignmentSnapshot{}, StoreOutcomeNotCommitted, err
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if override, found := store.takeOverride(referenceOperationRenew); found {
		return override.snapshot, override.outcome, override.err
	}
	if !request.Valid() {
		return AssignmentSnapshot{}, StoreOutcomeNotCommitted, errors.New("renew request is invalid")
	}
	condition := request.Condition()
	current, found := store.current[condition.Stamp().WorldID().String()]
	if !found {
		return AssignmentSnapshot{}, StoreOutcomeNotFound, nil
	}
	if !current.Stamp().Equal(condition.Stamp()) {
		return AssignmentSnapshot{}, StoreOutcomeConflict, nil
	}
	if !current.ValidAt(condition.ObservedAt()) {
		return AssignmentSnapshot{}, StoreOutcomeExpired, nil
	}
	if request.LeaseExpiresAt().Equal(current.Lease().ExpiresAt()) {
		return current, StoreOutcomeReplay, nil
	}
	renewed, err := current.Renew(request.LeaseExpiresAt(), condition.ObservedAt())
	if err != nil {
		return AssignmentSnapshot{}, StoreOutcomeConflict, nil
	}
	store.current[current.WorldID().String()] = renewed
	return renewed, StoreOutcomeApplied, nil
}

// Revoke 原子移除匹配 current，并以完整 stamp 保留 replay 证据。
func (store *referenceStore) Revoke(ctx context.Context, request StampRequest) (AssignmentSnapshot, StoreOutcome, error) {
	if err := ctx.Err(); err != nil {
		return AssignmentSnapshot{}, StoreOutcomeNotCommitted, err
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if override, found := store.takeOverride(referenceOperationRevoke); found {
		return override.snapshot, override.outcome, override.err
	}
	if !request.Valid() {
		return AssignmentSnapshot{}, StoreOutcomeNotCommitted, errors.New("revoke request is invalid")
	}
	worldKey := request.Stamp().WorldID().String()
	if current, found := store.current[worldKey]; found {
		if !current.Stamp().Equal(request.Stamp()) {
			return AssignmentSnapshot{}, StoreOutcomeConflict, nil
		}
		delete(store.current, worldKey)
		store.revoked[current.Stamp()] = current
		if store.revokeCommitUnknown {
			store.revokeCommitUnknown = false
			return AssignmentSnapshot{}, StoreOutcomeCommitUnknown, errors.New("revoke response lost after commit")
		}
		return current, StoreOutcomeApplied, nil
	}
	if revoked, found := store.revoked[request.Stamp()]; found {
		return revoked, StoreOutcomeReplay, nil
	}
	return AssignmentSnapshot{}, StoreOutcomeNotFound, nil
}

// Replace 原子撤销 predecessor、推进 high-watermark 并提交 starting successor。
func (store *referenceStore) Replace(ctx context.Context, request ReplaceRequest) (AssignmentSnapshot, StoreOutcome, error) {
	if err := ctx.Err(); err != nil {
		return AssignmentSnapshot{}, StoreOutcomeNotCommitted, err
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if override, found := store.takeOverride(referenceOperationReplace); found {
		return override.snapshot, override.outcome, override.err
	}
	if !request.Valid() {
		return AssignmentSnapshot{}, StoreOutcomeNotCommitted, errors.New("replace request is invalid")
	}
	worldKey := request.Expected().WorldID().String()
	current, found := store.current[worldKey]
	key := replacementKey{expected: request.Expected(), successorID: request.Successor().InstanceID(), targetNodeID: request.Successor().NodeID()}
	if found && !current.Stamp().Equal(request.Expected()) {
		if replay, replayFound := store.replacements[key]; replayFound && current.Stamp().Equal(replay.Stamp()) {
			return current, StoreOutcomeReplay, nil
		}
		return AssignmentSnapshot{}, StoreOutcomeConflict, nil
	}
	if !found {
		return AssignmentSnapshot{}, StoreOutcomeNotFound, nil
	}
	if !store.highWatermarkAvailable {
		return AssignmentSnapshot{}, StoreOutcomeNotCommitted, errors.New("placement high-watermark is unavailable")
	}
	successor, err := store.allocateStarting(request.Successor(), request.ObservedAt())
	if err != nil {
		return AssignmentSnapshot{}, StoreOutcomeNotCommitted, err
	}
	store.revoked[current.Stamp()] = current
	store.replacements[key] = successor
	store.current[worldKey] = successor
	if store.replaceCommitUnknown {
		store.replaceCommitUnknown = false
		return AssignmentSnapshot{}, StoreOutcomeCommitUnknown, errors.New("replace response lost after commit")
	}
	return successor, StoreOutcomeApplied, nil
}

// QualifyWrite 原子确认完整 stamp、active phase 与未过期 lease。
func (store *referenceStore) QualifyWrite(ctx context.Context, request StampRequest) (WriteFence, StoreOutcome, error) {
	if err := ctx.Err(); err != nil {
		return WriteFence{}, StoreOutcomeNotCommitted, err
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.qualifyOverrideEnabled {
		store.qualifyOverrideEnabled = false
		return store.qualifyFence, store.qualifyOutcome, store.qualifyErr
	}
	if !request.Valid() {
		return WriteFence{}, StoreOutcomeNotCommitted, errors.New("qualification request is invalid")
	}
	current, found := store.current[request.Stamp().WorldID().String()]
	if !found {
		return WriteFence{}, StoreOutcomeNotFound, nil
	}
	if !current.Stamp().Equal(request.Stamp()) {
		return WriteFence{}, StoreOutcomeConflict, nil
	}
	if !current.ValidAt(request.ObservedAt()) {
		return WriteFence{}, StoreOutcomeExpired, nil
	}
	if current.Phase() != PhaseActive {
		return WriteFence{}, StoreOutcomeInProgress, nil
	}
	fence, err := NewWriteFence(current.Stamp())
	if err != nil {
		return WriteFence{}, StoreOutcomeNotCommitted, err
	}
	return fence, StoreOutcomeApplied, nil
}

// allocateStarting 在持锁状态推进 world-scoped high-watermark 并构造 starting snapshot。
func (store *referenceStore) allocateStarting(candidate AssignmentCandidate, observedAt time.Time) (AssignmentSnapshot, error) {
	worldKey := candidate.WorldID().String()
	if store.generationHigh[worldKey] == ^uint64(0) || store.fenceHigh[worldKey] == ^uint64(0) {
		return AssignmentSnapshot{}, errors.New("placement high-watermark cannot advance")
	}
	store.generationHigh[worldKey]++
	store.fenceHigh[worldKey]++
	generation, err := NewAssignmentGeneration(store.generationHigh[worldKey])
	if err != nil {
		return AssignmentSnapshot{}, err
	}
	token, err := NewFencingToken(store.fenceHigh[worldKey])
	if err != nil {
		return AssignmentSnapshot{}, err
	}
	return candidate.Snapshot(generation, token, observedAt)
}

// takeOverride 在调用方持锁时读取并删除单次 mutation override。
func (store *referenceStore) takeOverride(operation referenceOperation) (storeOverride, bool) {
	override, found := store.overrides[operation]
	if found {
		delete(store.overrides, operation)
	}
	return override, found
}

// setOverride 配置下一次指定 store mutation 返回任意结果组合。
func (store *referenceStore) setOverride(operation referenceOperation, snapshot AssignmentSnapshot, outcome StoreOutcome, err error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.overrides[operation] = storeOverride{snapshot: snapshot, outcome: outcome, err: err}
}

// setResolveOverride 配置下一次 resolve 返回任意结果组合。
func (store *referenceStore) setResolveOverride(snapshot AssignmentSnapshot, outcome ResolveOutcome, err error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.resolveOverrideEnabled = true
	store.resolveSnapshot = snapshot
	store.resolveOutcome = outcome
	store.resolveErr = err
}

// setQualifyOverride 配置下一次 qualification 返回任意结果组合。
func (store *referenceStore) setQualifyOverride(fence WriteFence, outcome StoreOutcome, err error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.qualifyOverrideEnabled = true
	store.qualifyFence = fence
	store.qualifyOutcome = outcome
	store.qualifyErr = err
}

// setHighWatermarkAvailable 切换 reference allocator 是否可以证明 generation/fence 不回退。
func (store *referenceStore) setHighWatermarkAvailable(available bool) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.highWatermarkAvailable = available
}

// setAcquireCommitUnknown 配置下一次 acquire 提交后丢失响应。
func (store *referenceStore) setAcquireCommitUnknown() {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.acquireCommitUnknown = true
}

// setActivateCommitUnknown 配置下一次 activate 提交后丢失响应。
func (store *referenceStore) setActivateCommitUnknown() {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.activateCommitUnknown = true
}

// setRevokeCommitUnknown 配置下一次 revoke 提交后丢失响应。
func (store *referenceStore) setRevokeCommitUnknown() {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.revokeCommitUnknown = true
}

// setReplaceCommitUnknown 配置下一次 replace 提交后丢失响应。
func (store *referenceStore) setReplaceCommitUnknown() {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.replaceCommitUnknown = true
}

// currentSnapshot 返回测试断言使用的 mutex-protected current 值。
func (store *referenceStore) currentSnapshot(worldID personalworld.PersonalWorldID) (AssignmentSnapshot, bool) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	snapshot, found := store.current[worldID.String()]
	return snapshot, found
}

// currentCount 返回测试断言使用的 current PersonalWorld 数量。
func (store *referenceStore) currentCount() int {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	return len(store.current)
}

// fakeClock 提供可并发推进且不依赖 wall clock 的 deterministic 时间源。
type fakeClock struct {
	// mutex 保护 now，使 race tests 可以共享同一个 clock。
	mutex sync.Mutex
	// now 是测试明确选择的绝对时间。
	now time.Time
}

// Now 返回 mutex 保护的当前测试时间。
func (clock *fakeClock) Now() time.Time {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	return clock.now
}

// advance 原子推进测试时间，避免使用不稳定 sleep。
func (clock *fakeClock) advance(duration time.Duration) {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	clock.now = clock.now.Add(duration)
}

// sequenceIDGenerator 生成确定、并发安全且可注入失败的 identity 材料。
type sequenceIDGenerator struct {
	// mutex 保护序列与 failure injection。
	mutex sync.Mutex
	// next 是下一个 identity suffix 的数值部分。
	next uint64
	// err 非 nil 时让 NewID 显式失败。
	err error
}

// NewID 返回只含 ASCII 字母数字的确定性 suffix。
func (generator *sequenceIDGenerator) NewID() (string, error) {
	generator.mutex.Lock()
	defer generator.mutex.Unlock()
	if generator.err != nil {
		return "", generator.err
	}
	generator.next++
	return fmt.Sprintf("%032x", generator.next), nil
}

// generatedCount 返回已经成功生成的 suffix 数量。
func (generator *sequenceIDGenerator) generatedCount() uint64 {
	generator.mutex.Lock()
	defer generator.mutex.Unlock()
	return generator.next
}

// fakeRuntimeController 用 map 证明 Start/Stop 对 WorldInstanceID 的幂等和隔离。
type fakeRuntimeController struct {
	// mutex 保护 runtime map、计数、failure injection 与 hooks。
	mutex sync.Mutex
	// running 保存已经 ready 且尚未 Stop 的 runtime stamp。
	running map[WorldInstanceID]AssignmentStamp
	// startCalls 记录每个 instance 的 Start 调用次数，便于并发断言。
	startCalls map[WorldInstanceID]int
	// stopCalls 记录每个 instance 的 Stop 调用次数。
	stopCalls map[WorldInstanceID]int
	// startErr 让 Start 在不改变 running map 时失败。
	startErr error
	// stopErr 让 Stop 在不删除 runtime 时失败。
	stopErr error
	// startEntered 在 Start 取得输入后通知并发测试。
	startEntered chan struct{}
	// startRelease 让并发测试控制 ready 返回顺序。
	startRelease chan struct{}
}

// newFakeRuntimeController 创建没有运行实例或 failure injection 的测试 controller。
func newFakeRuntimeController() *fakeRuntimeController {
	return &fakeRuntimeController{
		running:    make(map[WorldInstanceID]AssignmentStamp),
		startCalls: make(map[WorldInstanceID]int),
		stopCalls:  make(map[WorldInstanceID]int),
	}
}

// setStartError 并发安全地配置后续 Start 故障。
func (controller *fakeRuntimeController) setStartError(err error) {
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	controller.startErr = err
}

// setStopError 并发安全地配置后续 Stop 故障。
func (controller *fakeRuntimeController) setStopError(err error) {
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	controller.stopErr = err
}

// Start 幂等记录相同 WorldInstanceID，并允许测试暂停 ready 回调。
func (controller *fakeRuntimeController) Start(ctx context.Context, snapshot AssignmentSnapshot) error {
	if !snapshot.Valid() || snapshot.Phase() != PhaseStarting {
		return errors.New("runtime start snapshot is invalid")
	}
	controller.mutex.Lock()
	controller.startCalls[snapshot.InstanceID()]++
	startErr := controller.startErr
	entered := controller.startEntered
	release := controller.startRelease
	if existing, found := controller.running[snapshot.InstanceID()]; found && !existing.Equal(snapshot.Stamp()) {
		controller.mutex.Unlock()
		return errors.New("world instance identity was reused with different stamp")
	}
	controller.mutex.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if release != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
		}
	}
	if startErr != nil {
		return startErr
	}
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	controller.running[snapshot.InstanceID()] = snapshot.Stamp()
	return nil
}

// Stop 只删除完整 stamp 匹配的 runtime，stale instance 不能停止 successor。
func (controller *fakeRuntimeController) Stop(ctx context.Context, stamp AssignmentStamp) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !stamp.Valid() {
		return errors.New("runtime stop stamp is invalid")
	}
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	controller.stopCalls[stamp.InstanceID()]++
	if controller.stopErr != nil {
		return controller.stopErr
	}
	if existing, found := controller.running[stamp.InstanceID()]; found {
		if !existing.Equal(stamp) {
			return errors.New("runtime stop stamp conflicts with running instance")
		}
		delete(controller.running, stamp.InstanceID())
	}
	return nil
}

// startCount 返回指定 instance 的 Start 调用次数。
func (controller *fakeRuntimeController) startCount(instanceID WorldInstanceID) int {
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	return controller.startCalls[instanceID]
}

// runningCount 返回当前 fake runtime 数量。
func (controller *fakeRuntimeController) runningCount() int {
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	return len(controller.running)
}

// stopCount 返回指定 instance 的 Stop 调用次数。
func (controller *fakeRuntimeController) stopCount(instanceID WorldInstanceID) int {
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	return controller.stopCalls[instanceID]
}

// TestReferenceStoreHighWatermarkAndReplay 验证撤销后发号不回退、revoke replay 不误伤 successor。
func TestReferenceStoreHighWatermarkAndReplay(t *testing.T) {
	t.Parallel()
	store := newReferenceStore()
	now := time.Date(2026, 7, 13, 5, 0, 0, 0, time.UTC)
	worldID := mustPersonalWorldID(t, "pworld_store")
	first := acquireReferenceAssignment(t, store, worldID, "winst_first", "rnode_alpha", now)
	revokeRequest, requestErr := NewStampRequest(first.Stamp(), now.Add(time.Second))
	if requestErr != nil {
		t.Fatalf("NewStampRequest: %v", requestErr)
	}
	revoked, outcome, err := store.Revoke(context.Background(), revokeRequest)
	if err != nil || outcome != StoreOutcomeApplied || !revoked.Equal(first) {
		t.Fatalf("first revoke outcome=%s err=%v", outcome, err)
	}
	replayed, outcome, err := store.Revoke(context.Background(), revokeRequest)
	if err != nil || outcome != StoreOutcomeReplay || !replayed.Equal(first) {
		t.Fatalf("revoke replay outcome=%s err=%v", outcome, err)
	}
	second := acquireReferenceAssignment(t, store, worldID, "winst_second", "rnode_alpha", now.Add(2*time.Second))
	if second.Generation().Uint64() <= first.Generation().Uint64() || second.FencingToken().Uint64() <= first.FencingToken().Uint64() {
		t.Fatal("reference store reused generation or fence")
	}
	if _, outcome, err := store.Revoke(context.Background(), revokeRequest); err != nil || outcome != StoreOutcomeConflict {
		t.Fatalf("stale revoke outcome=%s err=%v", outcome, err)
	}
}

// TestReferenceStoreRejectsUnavailableHighWatermark 验证 allocator 不可信时 fail closed。
func TestReferenceStoreRejectsUnavailableHighWatermark(t *testing.T) {
	t.Parallel()
	store := newReferenceStore()
	store.setHighWatermarkAvailable(false)
	now := time.Date(2026, 7, 13, 5, 0, 0, 0, time.UTC)
	candidate := mustCandidate(t, mustPersonalWorldID(t, "pworld_highwater"), "winst_highwater", "rnode_alpha", now, time.Minute)
	request, requestErr := NewAcquireRequest(candidate, now)
	if requestErr != nil {
		t.Fatalf("NewAcquireRequest: %v", requestErr)
	}
	snapshot, outcome, err := store.Acquire(context.Background(), request)
	if err == nil || outcome != StoreOutcomeNotCommitted || !snapshot.empty() || store.currentCount() != 0 {
		t.Fatalf("unavailable high-watermark result=%#v outcome=%s err=%v", snapshot, outcome, err)
	}
}

// acquireReferenceAssignment 提交测试 candidate 并要求 reference store 返回 applied starting snapshot。
func acquireReferenceAssignment(t *testing.T, store *referenceStore, worldID personalworld.PersonalWorldID, instanceValue string, nodeValue string, now time.Time) AssignmentSnapshot {
	t.Helper()
	candidate := mustCandidate(t, worldID, instanceValue, nodeValue, now, time.Minute)
	request, err := NewAcquireRequest(candidate, now)
	if err != nil {
		t.Fatalf("NewAcquireRequest: %v", err)
	}
	snapshot, outcome, err := store.Acquire(context.Background(), request)
	if err != nil || outcome != StoreOutcomeApplied || !snapshot.ValidAt(now) {
		t.Fatalf("Acquire outcome=%s err=%v snapshot=%#v", outcome, err, snapshot)
	}
	return snapshot
}

// mustCandidate 构造测试用 assignment candidate，失败表示 fixture 本身无效。
func mustCandidate(t *testing.T, worldID personalworld.PersonalWorldID, instanceValue string, nodeValue string, now time.Time, ttl time.Duration) AssignmentCandidate {
	t.Helper()
	instanceID, err := NewWorldInstanceID(instanceValue)
	if err != nil {
		t.Fatalf("NewWorldInstanceID: %v", err)
	}
	nodeID, err := NewRuntimeNodeID(nodeValue)
	if err != nil {
		t.Fatalf("NewRuntimeNodeID: %v", err)
	}
	candidate, err := NewAssignmentCandidate(worldID, instanceID, nodeID, now, now.Add(ttl))
	if err != nil {
		t.Fatalf("NewAssignmentCandidate: %v", err)
	}
	return candidate
}

// mustPersonalWorldID 构造测试用 PersonalWorldID，失败表示 fixture 本身无效。
func mustPersonalWorldID(t *testing.T, value string) personalworld.PersonalWorldID {
	t.Helper()
	worldID, err := personalworld.NewPersonalWorldID(value)
	if err != nil {
		t.Fatalf("NewPersonalWorldID: %v", err)
	}
	return worldID
}
