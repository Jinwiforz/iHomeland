package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/simulationcontrol"
)

// TestWorldAssignmentCoordinatorReturnsLocalCurrent 验证已由本进程承载的active assignment只登记任务而不替换。
func TestWorldAssignmentCoordinatorReturnsLocalCurrent(t *testing.T) {
	t.Parallel()
	fixture := newAssignmentCoordinatorFixture(t)
	local := coordinatorAssignment(t, "local", fixture.node, placement.PhaseActive, fixture.clock.now)
	fixture.startRuntime(t, local)
	fixture.store.snapshot, fixture.store.outcome = local, placement.ResolveOutcomeFound
	result, err := fixture.coordinator.EnsureActive(context.Background(), local.WorldID())
	if err != nil || !result.Stamp().Equal(local.Stamp()) {
		t.Fatalf("EnsureActive() result=%v err=%v", result, err)
	}
	if fixture.commands.replaceCalls != 0 || fixture.deadlines.Len() != 2 {
		t.Fatalf("replaceCalls=%d deadlineEntries=%d", fixture.commands.replaceCalls, fixture.deadlines.Len())
	}
}

// TestWorldAssignmentCoordinatorConcurrentLocalReplay 验证并发bootstrap复用同一runtime与两个语义任务。
func TestWorldAssignmentCoordinatorConcurrentLocalReplay(t *testing.T) {
	t.Parallel()
	fixture := newAssignmentCoordinatorFixture(t)
	local := coordinatorAssignment(t, "concurrent", fixture.node, placement.PhaseActive, fixture.clock.now)
	fixture.startRuntime(t, local)
	fixture.store.snapshot, fixture.store.outcome = local, placement.ResolveOutcomeFound
	var wait sync.WaitGroup
	errorsChannel := make(chan error, 16)
	for index := 0; index < 16; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := fixture.coordinator.EnsureActive(context.Background(), local.WorldID())
			if err != nil || !result.Stamp().Equal(local.Stamp()) {
				errorsChannel <- err
			}
		}()
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Fatalf("concurrent EnsureActive error=%v", err)
	}
	if fixture.runtime.count() != 1 || fixture.deadlines.Len() != 2 {
		t.Fatalf("runtime=%d deadlines=%d", fixture.runtime.count(), fixture.deadlines.Len())
	}
}

// TestWorldAssignmentCoordinatorSerializesMissingWorldConvergence 验证 stale not-found 不能清理同 world 刚启动的 runtime。
func TestWorldAssignmentCoordinatorSerializesMissingWorldConvergence(t *testing.T) {
	t.Parallel()
	fixture := newAssignmentCoordinatorFixture(t)
	active := coordinatorAssignment(t, "missingrace", fixture.node, placement.PhaseActive, fixture.clock.now)
	fixture.commands.ensureResult, _ = placement.NewAssignmentResult(active, placement.ResultDispositionStarted, false)
	fixture.commands.ensureEntered = make(chan struct{}, 2)
	fixture.commands.ensureRelease = make(chan struct{})
	fixture.commands.onEnsure = func() {
		fixture.startRuntime(t, active)
		fixture.store.set(active, placement.ResolveOutcomeFound)
	}

	results := make(chan placement.AssignmentSnapshot, 2)
	errorsChannel := make(chan error, 2)
	run := func() {
		result, err := fixture.coordinator.EnsureActive(context.Background(), active.WorldID())
		results <- result
		errorsChannel <- err
	}
	go run()
	<-fixture.commands.ensureEntered
	go run()

	deadline := time.Now().Add(time.Second)
	for {
		fixture.coordinator.convergenceMutex.Lock()
		references := fixture.coordinator.convergenceLanes[active.WorldID().String()].references
		fixture.coordinator.convergenceMutex.Unlock()
		if references == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second activation did not enter the same convergence lane")
		}
		time.Sleep(time.Millisecond)
	}
	if calls := fixture.store.calls(); calls != 1 {
		t.Fatalf("waiting activation performed stale Resolve calls=%d", calls)
	}
	close(fixture.commands.ensureRelease)
	for index := 0; index < 2; index++ {
		if err := <-errorsChannel; err != nil {
			t.Fatalf("EnsureActive[%d]: %v", index, err)
		}
		if result := <-results; !result.Stamp().Equal(active.Stamp()) {
			t.Fatalf("EnsureActive[%d] returned another assignment", index)
		}
	}
	if fixture.commands.ensureCalls != 1 || fixture.runtime.count() != 1 {
		t.Fatalf("ensure calls=%d runtime=%d", fixture.commands.ensureCalls, fixture.runtime.count())
	}
	fixture.coordinator.convergenceMutex.Lock()
	laneCount := len(fixture.coordinator.convergenceLanes)
	fixture.coordinator.convergenceMutex.Unlock()
	if laneCount != 0 {
		t.Fatalf("completed convergence retained %d lanes", laneCount)
	}
}

// TestWorldAssignmentCoordinatorShutdownRevokesAllAssignments 验证关闭先按稳定顺序调用 placement Sleep。
func TestWorldAssignmentCoordinatorShutdownRevokesAllAssignments(t *testing.T) {
	t.Parallel()
	fixture := newAssignmentCoordinatorFixture(t)
	second := coordinatorAssignment(t, "shutdownb", fixture.node, placement.PhaseActive, fixture.clock.now)
	first := coordinatorAssignment(t, "shutdowna", fixture.node, placement.PhaseActive, fixture.clock.now)
	fixture.startRuntime(t, second)
	fixture.startRuntime(t, first)
	if err := fixture.coordinator.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fixture.commands.sleepStamps) != 2 ||
		fixture.commands.sleepStamps[0].InstanceID().String() != first.InstanceID().String() ||
		fixture.commands.sleepStamps[1].InstanceID().String() != second.InstanceID().String() {
		t.Fatalf("sleep stamps = %#v", fixture.commands.sleepStamps)
	}
}

// TestWorldAssignmentCoordinatorSharesExactSimulationTarget 验证 Owner/Visitor 只能消费同一 assignment 派生 target。
func TestWorldAssignmentCoordinatorSharesExactSimulationTarget(t *testing.T) {
	t.Parallel()
	fixture := newAssignmentCoordinatorFixture(t)
	active := coordinatorAssignment(t, "target", fixture.node, placement.PhaseActive, fixture.clock.now)
	nodeID, _ := simulationcontrol.NewSimulationNodeID("snode_coordinatorfixture")
	instanceID, _ := simulationcontrol.NewSimulationInstanceID("sinst_00112233445566778899aabbccddeeff")
	fingerprint, _ := simulationcontrol.NewDigest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	modelManifest, _ := simulationcontrol.NewDigest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	profileManifest, _ := simulationcontrol.NewDigest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
	configIdentity, _ := simulationcontrol.NewDigest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")
	target := simulationcontrol.SimulationTarget{
		RuntimeNodeID:         active.NodeID(),
		NodeID:                nodeID,
		InstanceID:            instanceID,
		AssignmentFingerprint: fingerprint,
		MappingGeneration:     active.Generation().Uint64(),
		ModelManifest:         modelManifest,
		ProfileManifest:       profileManifest,
		ConfigIdentity:        configIdentity,
		ActorCapacity:         8,
		Revision:              1,
	}
	if err := target.Validate(); err != nil {
		t.Fatal(err)
	}
	resolver := &fixedSimulationTargetResolver{target: target}
	if err := fixture.coordinator.bindSimulationTargets(resolver); err != nil {
		t.Fatal(err)
	}
	ownerTarget, ownerFound, ownerErr := fixture.coordinator.ResolveSimulationTarget(context.Background(), active.Stamp())
	visitorTarget, visitorFound, visitorErr := fixture.coordinator.ResolveSimulationTarget(context.Background(), active.Stamp())
	if ownerErr != nil || visitorErr != nil || !ownerFound || !visitorFound ||
		ownerTarget != visitorTarget || ownerTarget != target ||
		!resolver.stamp.Equal(active.Stamp()) {
		t.Fatalf("owner=%#v/%v/%v visitor=%#v/%v/%v", ownerTarget, ownerFound, ownerErr, visitorTarget, visitorFound, visitorErr)
	}
}

// TestWorldAssignmentCoordinatorAuthoritativeExpiryInvalidatesVisit 验证missing current停止精确runtime并通知Visit owner。
func TestWorldAssignmentCoordinatorAuthoritativeExpiryInvalidatesVisit(t *testing.T) {
	t.Parallel()
	fixture := newAssignmentCoordinatorFixture(t)
	local := coordinatorAssignment(t, "expired", fixture.node, placement.PhaseActive, fixture.clock.now)
	fixture.startRuntime(t, local)
	fixture.store.snapshot, fixture.store.outcome = placement.AssignmentSnapshot{}, placement.ResolveOutcomeNotFound
	called := 0
	fixture.coordinator.onLoss = func(_ context.Context, stamp placement.AssignmentStamp) error {
		if !stamp.Equal(local.Stamp()) {
			t.Fatal("assignment loss handler received wrong stamp")
		}
		called++
		return nil
	}
	if err := fixture.coordinator.expire(context.Background(), local); err != nil {
		t.Fatal(err)
	}
	if fixture.runtime.contains(local.Stamp()) || called != 1 {
		t.Fatalf("runtime retained=%v invalidations=%d", fixture.runtime.contains(local.Stamp()), called)
	}
}

// TestWorldAssignmentCoordinatorMissingCurrentCleansLocalOrphan 验证Redis key丢失不会保留旧runtime。
func TestWorldAssignmentCoordinatorMissingCurrentCleansLocalOrphan(t *testing.T) {
	t.Parallel()
	fixture := newAssignmentCoordinatorFixture(t)
	old := coordinatorAssignment(t, "flush", fixture.node, placement.PhaseActive, fixture.clock.now)
	fixture.startRuntime(t, old)
	newStamp, _ := placement.NewAssignmentStamp(old.WorldID(), mustInstanceID(t, "winst_flushsuccessor"), fixture.node, placement.AssignmentGeneration(2), placement.FencingToken(2))
	successor := coordinatorAssignmentWithStamp(t, newStamp, placement.PhaseActive, fixture.clock.now, fixture.clock.now.Add(time.Minute))
	fixture.commands.ensureResult, _ = placement.NewAssignmentResult(successor, placement.ResultDispositionStarted, false)
	fixture.commands.onEnsure = func() { fixture.startRuntime(t, successor) }
	invalidated := 0
	fixture.coordinator.onLoss = func(context.Context, placement.AssignmentStamp) error { invalidated++; return nil }
	result, err := fixture.coordinator.EnsureActive(context.Background(), old.WorldID())
	if err != nil || !result.Stamp().Equal(successor.Stamp()) {
		t.Fatalf("EnsureActive result=%v err=%v", result, err)
	}
	if fixture.runtime.contains(old.Stamp()) || !fixture.runtime.contains(successor.Stamp()) || invalidated != 1 || fixture.runtime.count() != 1 {
		t.Fatalf("old=%v new=%v invalidated=%d count=%d", fixture.runtime.contains(old.Stamp()), fixture.runtime.contains(successor.Stamp()), invalidated, fixture.runtime.count())
	}
}

// mustInstanceID 构造测试WorldInstance identity。
func mustInstanceID(t *testing.T, value string) placement.WorldInstanceID {
	t.Helper()
	id, err := placement.NewWorldInstanceID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestWorldAssignmentCoordinatorReplacesOrphan 验证其他进程或starting predecessor被完整replacement。
func TestWorldAssignmentCoordinatorReplacesOrphan(t *testing.T) {
	t.Parallel()
	fixture := newAssignmentCoordinatorFixture(t)
	foreignNode, _ := placement.NewRuntimeNodeID("rnode_foreignfixture")
	predecessor := coordinatorAssignment(t, "orphan", foreignNode, placement.PhaseActive, fixture.clock.now)
	successor := coordinatorAssignment(t, "successor", fixture.node, placement.PhaseActive, fixture.clock.now)
	fixture.store.snapshot, fixture.store.outcome = predecessor, placement.ResolveOutcomeFound
	fixture.commands.replaceResult, _ = placement.NewAssignmentResult(successor, placement.ResultDispositionStarted, false)
	fixture.commands.onReplace = func() { fixture.startRuntime(t, successor) }
	result, err := fixture.coordinator.EnsureActive(context.Background(), predecessor.WorldID())
	if err != nil || !result.Stamp().Equal(successor.Stamp()) {
		t.Fatalf("EnsureActive() result=%v err=%v", result, err)
	}
	if fixture.commands.replaceCalls != 1 || !fixture.runtime.contains(successor.Stamp()) {
		t.Fatalf("replaceCalls=%d runtime=%v", fixture.commands.replaceCalls, fixture.runtime.contains(successor.Stamp()))
	}
}

// TestWorldAssignmentCoordinatorMissingAndRenew 验证missing走Ensure，renew成功原位替换两个deadline。
func TestWorldAssignmentCoordinatorMissingAndRenew(t *testing.T) {
	t.Parallel()
	fixture := newAssignmentCoordinatorFixture(t)
	active := coordinatorAssignment(t, "ensure", fixture.node, placement.PhaseActive, fixture.clock.now)
	fixture.commands.ensureResult, _ = placement.NewAssignmentResult(active, placement.ResultDispositionStarted, false)
	fixture.commands.onEnsure = func() { fixture.startRuntime(t, active) }
	result, err := fixture.coordinator.EnsureActive(context.Background(), active.WorldID())
	if err != nil || !result.Stamp().Equal(active.Stamp()) || fixture.commands.ensureCalls != 1 {
		t.Fatalf("EnsureActive() result=%v calls=%d err=%v", result, fixture.commands.ensureCalls, err)
	}
	renewed := coordinatorAssignmentWithStamp(t, active.Stamp(), placement.PhaseActive, fixture.clock.now, fixture.clock.now.Add(2*time.Minute))
	fixture.commands.renewed = renewed
	if err := fixture.coordinator.renew(context.Background(), active); err != nil {
		t.Fatalf("renew() error = %v", err)
	}
	if fixture.deadlines.Len() != 2 {
		t.Fatalf("deadline entries after renew = %d", fixture.deadlines.Len())
	}
}

// TestWorldAssignmentCoordinatorPropagatesDeadlineCapacity 验证 activation 后的调度失败不会被泛化为未收敛。
func TestWorldAssignmentCoordinatorPropagatesDeadlineCapacity(t *testing.T) {
	t.Parallel()
	fixture := newAssignmentCoordinatorFixture(t)
	active := coordinatorAssignment(t, "capacity", fixture.node, placement.PhaseActive, fixture.clock.now)
	fixture.commands.ensureResult, _ = placement.NewAssignmentResult(active, placement.ResultDispositionStarted, false)
	fixture.commands.onEnsure = func() { fixture.startRuntime(t, active) }
	limited, _ := newSemanticDeadlineOwner(1, applicationSemanticClock{Clock: fixture.clock})
	fixture.coordinator.deadlines = limited
	_, err := fixture.coordinator.EnsureActive(context.Background(), active.WorldID())
	if err == nil || err.Error() != "semantic deadline capacity is exhausted" {
		t.Fatalf("EnsureActive() error = %v", err)
	}
	if limited.Len() != 0 {
		t.Fatalf("partial deadline schedule retained %d entries", limited.Len())
	}
}

// TestWorldAssignmentCoordinatorRejectsMalformedExpiryRead 验证损坏 current 不会被误当作正常 expiry。
func TestWorldAssignmentCoordinatorRejectsMalformedExpiryRead(t *testing.T) {
	t.Parallel()
	fixture := newAssignmentCoordinatorFixture(t)
	active := coordinatorAssignment(t, "malformed", fixture.node, placement.PhaseActive, fixture.clock.now)
	fixture.startRuntime(t, active)
	fixture.store.snapshot, fixture.store.outcome = placement.AssignmentSnapshot{}, placement.ResolveOutcomeFound
	if err := fixture.coordinator.expire(context.Background(), active); err == nil {
		t.Fatal("malformed current assignment was accepted")
	}
	if fixture.runtime.contains(active.Stamp()) {
		t.Fatal("malformed expiry read retained local runtime")
	}
}

// assignmentCoordinatorFixture 聚合不启动backend的placement/runtime fakes。
type assignmentCoordinatorFixture struct {
	// clock 固定所有 lease 计算基点。
	clock fakeClock
	// node 是本地 runtime identity。
	node placement.RuntimeNodeID
	// runtime 保存测试内可推导 assignment。
	runtime *processWorldRuntime
	// deadlines 保存 renew 与 expiry 任务。
	deadlines *semanticDeadlineOwner
	// store 返回预置 current 事实。
	store *fakePlacementCurrentStore
	// commands 捕获 placement mutation。
	commands *fakePlacementCommands
	// coordinator 是被测 application owner。
	coordinator *worldAssignmentCoordinator
}

// newAssignmentCoordinatorFixture 创建ready的单进程coordinator。
func newAssignmentCoordinatorFixture(t *testing.T) assignmentCoordinatorFixture {
	t.Helper()
	clock := fakeClock{now: time.Date(2026, 7, 15, 2, 0, 0, 0, time.UTC)}
	node, _ := placement.NewRuntimeNodeID("rnode_coordinatorfixture")
	runtime, _ := newProcessWorldRuntime(node, 8, func() bool { return true })
	deadlines, _ := newSemanticDeadlineOwner(32, applicationSemanticClock{Clock: clock})
	store := &fakePlacementCurrentStore{outcome: placement.ResolveOutcomeNotFound}
	commands := &fakePlacementCommands{}
	coordinator, err := newWorldAssignmentCoordinator(commands, store, runtime, deadlines, clock, fakeIDGenerator{id: "abcdef0123456789abcdef0123456789"}, node, nil)
	if err != nil {
		t.Fatal(err)
	}
	return assignmentCoordinatorFixture{clock: clock, node: node, runtime: runtime, deadlines: deadlines, store: store, commands: commands, coordinator: coordinator}
}

// startRuntime 从active snapshot重建同stamp starting投影以满足RuntimeController contract。
func (fixture assignmentCoordinatorFixture) startRuntime(t *testing.T, active placement.AssignmentSnapshot) {
	t.Helper()
	starting := coordinatorAssignmentWithStamp(t, active.Stamp(), placement.PhaseStarting, fixture.clock.now, active.Lease().ExpiresAt())
	if err := fixture.runtime.Start(context.Background(), starting); err != nil {
		t.Fatal(err)
	}
}

// fakePlacementCurrentStore 返回可变current决议。
type fakePlacementCurrentStore struct {
	// mutex 保护动态并发测试中的 snapshot、outcome 与 resolveCalls。
	mutex sync.Mutex
	// snapshot 是 Resolve 返回的可选 current。
	snapshot placement.AssignmentSnapshot
	// outcome 是与 snapshot 配套的封闭结果。
	outcome placement.ResolveOutcome
	// err 模拟 dependency failure。
	err error
	// resolveCalls 记录 point-in-time 读取次数。
	resolveCalls int
}

// fixedSimulationTargetResolver 捕获 application 传入的 exact stamp。
type fixedSimulationTargetResolver struct {
	// target 是 Owner/Visitor 共享的内部 target。
	target simulationcontrol.SimulationTarget
	// stamp 是最近一次解析参数。
	stamp placement.AssignmentStamp
}

// Resolve 返回固定 target；接口没有 PlayerID 或客户端可覆盖字段。
func (resolver *fixedSimulationTargetResolver) Resolve(_ context.Context, stamp placement.AssignmentStamp) (simulationcontrol.SimulationTarget, bool, error) {
	resolver.stamp = stamp
	return resolver.target, true, nil
}

// Resolve 返回测试预置的 current 结果。
func (store *fakePlacementCurrentStore) Resolve(context.Context, personalworld.PersonalWorldID, time.Time) (placement.AssignmentSnapshot, placement.ResolveOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.resolveCalls++
	return store.snapshot, store.outcome, store.err
}

// set 原子更新动态并发测试的 current 事实。
func (store *fakePlacementCurrentStore) set(snapshot placement.AssignmentSnapshot, outcome placement.ResolveOutcome) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.snapshot = snapshot
	store.outcome = outcome
}

// calls 返回已完成的 current 读取次数。
func (store *fakePlacementCurrentStore) calls() int {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	return store.resolveCalls
}

// fakePlacementCommands 捕获Ensure/Replace/Renew并允许在result返回前登记runtime。
type fakePlacementCommands struct {
	// ensureResult 是 EnsureActive 预置结果。
	ensureResult placement.AssignmentResult
	// ensureErr 模拟 EnsureActive 失败。
	ensureErr error
	// replaceResult 是 Replace 预置结果。
	replaceResult placement.AssignmentResult
	// replaceErr 模拟 Replace 失败。
	replaceErr error
	// renewed 是 Renew 预置结果。
	renewed placement.AssignmentSnapshot
	// renewErr 模拟 Renew 失败。
	renewErr error
	// sleepErr 模拟 shutdown revoke/stop 失败。
	sleepErr error
	// ensureCalls 记录 EnsureActive 调用次数。
	ensureCalls int
	// replaceCalls 记录 Replace 调用次数。
	replaceCalls int
	// sleepStamps 记录 shutdown 撤销的 exact stamps。
	sleepStamps []placement.AssignmentStamp
	// onEnsure 在返回 EnsureActive 结果前登记 runtime。
	onEnsure func()
	// ensureEntered 通知测试 EnsureActive 已越过 current Resolve。
	ensureEntered chan struct{}
	// ensureRelease 允许测试冻结首次 activation 并观察同 world 等待者。
	ensureRelease chan struct{}
	// onReplace 在返回 Replace 结果前登记 runtime。
	onReplace func()
}

// EnsureActive 记录调用并在返回前执行可选 runtime hook。
func (commands *fakePlacementCommands) EnsureActive(context.Context, personalworld.PersonalWorldID, placement.RuntimeNodeID) (placement.AssignmentResult, error) {
	commands.ensureCalls++
	if commands.ensureEntered != nil {
		commands.ensureEntered <- struct{}{}
	}
	if commands.ensureRelease != nil {
		<-commands.ensureRelease
	}
	if commands.onEnsure != nil {
		commands.onEnsure()
	}
	return commands.ensureResult, commands.ensureErr
}

// Renew 返回测试预置的续约结果。
func (commands *fakePlacementCommands) Renew(context.Context, placement.AssignmentStamp) (placement.AssignmentSnapshot, error) {
	return commands.renewed, commands.renewErr
}

// Replace 记录调用并在返回前执行可选 runtime hook。
func (commands *fakePlacementCommands) Replace(context.Context, placement.ReplaceCommand) (placement.AssignmentResult, error) {
	commands.replaceCalls++
	if commands.onReplace != nil {
		commands.onReplace()
	}
	return commands.replaceResult, commands.replaceErr
}

// Sleep 记录 shutdown 撤销的 exact stamp。
func (commands *fakePlacementCommands) Sleep(_ context.Context, stamp placement.AssignmentStamp) (placement.LifecycleResult, error) {
	commands.sleepStamps = append(commands.sleepStamps, stamp)
	return placement.LifecycleResult{}, commands.sleepErr
}

// coordinatorAssignment 构造active/starting assignment。
func coordinatorAssignment(t *testing.T, suffix string, node placement.RuntimeNodeID, phase placement.Phase, now time.Time) placement.AssignmentSnapshot {
	t.Helper()
	world, _ := personalworld.NewPersonalWorldID("pworld_coordinator" + suffix)
	instance, _ := placement.NewWorldInstanceID("winst_coordinator" + suffix)
	generation, _ := placement.NewAssignmentGeneration(1)
	fence, _ := placement.NewFencingToken(1)
	stamp, err := placement.NewAssignmentStamp(world, instance, node, generation, fence)
	if err != nil {
		t.Fatal(err)
	}
	return coordinatorAssignmentWithStamp(t, stamp, phase, now, now.Add(time.Minute))
}

// coordinatorAssignmentWithStamp 构造给定stamp的严格snapshot。
func coordinatorAssignmentWithStamp(t *testing.T, stamp placement.AssignmentStamp, phase placement.Phase, now, expiry time.Time) placement.AssignmentSnapshot {
	t.Helper()
	snapshot, err := placement.NewAssignmentSnapshot(stamp, phase, now.Add(-time.Minute), expiry, now)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
