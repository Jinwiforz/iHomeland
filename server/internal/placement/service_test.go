package placement

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestEnsureActiveAndWriteQualification 验证首次启动、既有解析和完整 fence qualification。
func TestEnsureActiveAndWriteQualification(t *testing.T) {
	t.Parallel()
	service, store, runtime, clock, ids := newTestService(t)
	worldID := mustPersonalWorldID(t, "pworld_ensure")
	nodeID := mustRuntimeNodeID(t, "rnode_alpha")
	result, err := service.EnsureActive(context.Background(), worldID, nodeID)
	if err != nil || !result.Valid() || result.Disposition() != ResultDispositionStarted {
		t.Fatalf("first EnsureActive result=%#v err=%v", result, err)
	}
	assignment := result.Assignment()
	if assignment.Phase() != PhaseActive || runtime.runningCount() != 1 || ids.generatedCount() != 1 || store.currentCount() != 1 {
		t.Fatal("first ensure did not produce exactly one active runtime")
	}
	existing, err := service.EnsureActive(context.Background(), worldID, mustRuntimeNodeID(t, "rnode_ignored"))
	if err != nil || existing.Disposition() != ResultDispositionExisting || !existing.Assignment().Equal(assignment) || ids.generatedCount() != 1 {
		t.Fatalf("existing EnsureActive result=%#v err=%v", existing, err)
	}
	fence, err := service.QualifyWrite(context.Background(), assignment.Stamp())
	if err != nil || !fence.Valid() || !fence.Stamp().Equal(assignment.Stamp()) {
		t.Fatalf("QualifyWrite fence=%#v err=%v", fence, err)
	}
	clock.advance(time.Millisecond)
	renewed, err := service.Renew(context.Background(), assignment.Stamp())
	if err != nil || !renewed.Stamp().Equal(assignment.Stamp()) || !renewed.Lease().ExpiresAt().After(assignment.Lease().ExpiresAt()) {
		t.Fatalf("Renew snapshot=%#v err=%v", renewed, err)
	}
}

// TestEnsureActiveConcurrentStarting 验证 starting 可见期间其他调用只收到 in-progress。
func TestEnsureActiveConcurrentStarting(t *testing.T) {
	t.Parallel()
	service, _, runtime, _, ids := newTestService(t)
	runtime.startEntered = make(chan struct{}, 1)
	runtime.startRelease = make(chan struct{})
	worldID := mustPersonalWorldID(t, "pworld_concurrent")
	nodeID := mustRuntimeNodeID(t, "rnode_alpha")
	type ensureResult struct {
		// result 保存 first caller 的 application 结果。
		result AssignmentResult
		// err 保存 first caller 的失败。
		err error
	}
	firstDone := make(chan ensureResult, 1)
	go func() {
		result, err := service.EnsureActive(context.Background(), worldID, nodeID)
		firstDone <- ensureResult{result: result, err: err}
	}()
	select {
	case <-runtime.startEntered:
	// fake runtime 只做内存 channel 同步；一秒仅为 CI 调度留余量并防止永久挂起，不参与业务时序。
	case <-time.After(time.Second):
		t.Fatal("first EnsureActive did not reach runtime Start")
	}
	second, secondErr := service.EnsureActive(context.Background(), worldID, nodeID)
	if second.Valid() || ErrorKindOf(secondErr) != ErrorKindInProgress {
		t.Fatalf("second EnsureActive result=%#v err=%v", second, secondErr)
	}
	close(runtime.startRelease)
	first := <-firstDone
	if first.err != nil || !first.result.Valid() || ids.generatedCount() != 1 || runtime.startCount(first.result.Assignment().InstanceID()) != 1 {
		t.Fatalf("first EnsureActive result=%#v err=%v", first.result, first.err)
	}
}

// TestEnsureActiveRecoversCommitUnknown 验证 acquire/activate 响应丢失只按原 candidate 收敛。
func TestEnsureActiveRecoversCommitUnknown(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		inject func(*referenceStore)
	}{
		{name: "acquire", inject: func(store *referenceStore) { store.setAcquireCommitUnknown() }},
		{name: "activate", inject: func(store *referenceStore) { store.setActivateCommitUnknown() }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service, store, runtime, _, ids := newTestService(t)
			test.inject(store)
			result, err := service.EnsureActive(context.Background(), mustPersonalWorldID(t, "pworld_unknown"+test.name), mustRuntimeNodeID(t, "rnode_alpha"))
			if err != nil || !result.Valid() || result.Disposition() != ResultDispositionReplayed {
				t.Fatalf("EnsureActive result=%#v err=%v", result, err)
			}
			if ids.generatedCount() != 1 || runtime.startCount(result.Assignment().InstanceID()) != 1 || store.currentCount() != 1 {
				t.Fatal("commit-unknown recovery duplicated candidate or runtime")
			}
		})
	}
}

// TestEnsureActiveStartFailureRevokesCandidate 验证 runtime failure 不留下 writable starting 资格。
func TestEnsureActiveStartFailureRevokesCandidate(t *testing.T) {
	t.Parallel()
	service, store, runtime, _, _ := newTestService(t)
	runtime.setStartError(errors.New("runtime unavailable"))
	result, err := service.EnsureActive(context.Background(), mustPersonalWorldID(t, "pworld_startfail"), mustRuntimeNodeID(t, "rnode_alpha"))
	if result.Valid() || ErrorKindOf(err) != ErrorKindRuntimeUnavailable || CommitPhaseOf(err) != CommitPhaseCommitted {
		t.Fatalf("EnsureActive result=%#v err=%v", result, err)
	}
	if store.currentCount() != 0 || runtime.runningCount() != 0 {
		t.Fatal("start failure left current assignment or runtime")
	}
}

// TestReadyAfterReplacementIsRejected 构造迟到 ready，验证旧 activate 不能覆盖 successor。
func TestReadyAfterReplacementIsRejected(t *testing.T) {
	t.Parallel()
	service, store, runtime, clock, _ := newTestService(t)
	runtime.startEntered = make(chan struct{}, 1)
	runtime.startRelease = make(chan struct{})
	worldID := mustPersonalWorldID(t, "pworld_readyrace")
	nodeID := mustRuntimeNodeID(t, "rnode_alpha")
	errDone := make(chan error, 1)
	go func() {
		_, err := service.EnsureActive(context.Background(), worldID, nodeID)
		errDone <- err
	}()
	select {
	case <-runtime.startEntered:
	// fake runtime 只做内存 channel 同步；一秒仅为 CI 调度留余量并防止永久挂起，不参与业务时序。
	case <-time.After(time.Second):
		t.Fatal("EnsureActive did not reach runtime Start")
	}
	predecessor, found := store.currentSnapshot(worldID)
	if !found || predecessor.Phase() != PhaseStarting {
		t.Fatal("starting predecessor not found")
	}
	now := clock.Now()
	successorCandidate := mustCandidate(t, worldID, "winst_successor", "rnode_beta", now, time.Minute)
	replaceRequest, err := NewReplaceRequest(predecessor.Stamp(), successorCandidate, now)
	if err != nil {
		t.Fatalf("NewReplaceRequest: %v", err)
	}
	successor, outcome, err := store.Replace(context.Background(), replaceRequest)
	if err != nil || outcome != StoreOutcomeApplied || successor.Phase() != PhaseStarting {
		t.Fatalf("Replace outcome=%s err=%v", outcome, err)
	}
	close(runtime.startRelease)
	ensureErr := <-errDone
	if ErrorKindOf(ensureErr) != ErrorKindConflict {
		t.Fatalf("late ready error=%v", ensureErr)
	}
	current, found := store.currentSnapshot(worldID)
	if !found || !current.Stamp().Equal(successor.Stamp()) {
		t.Fatal("late ready overwrote successor")
	}
	if _, err := service.QualifyWrite(context.Background(), predecessor.Stamp()); ErrorKindOf(err) != ErrorKindConflict {
		t.Fatalf("predecessor qualification error=%v", err)
	}
	if runtime.runningCount() != 0 || runtime.stopCount(predecessor.InstanceID()) != 1 {
		t.Fatal("late ready runtime was not stopped by its full predecessor stamp")
	}
}

// TestActivateExpiryCleansStarting 验证 runtime ready 前 lease 到期会撤销 candidate 并停止 runtime。
func TestActivateExpiryCleansStarting(t *testing.T) {
	t.Parallel()
	service, store, runtime, clock, _ := newTestService(t)
	runtime.startEntered = make(chan struct{}, 1)
	runtime.startRelease = make(chan struct{})
	worldID := mustPersonalWorldID(t, "pworld_activateexpiry")
	nodeID := mustRuntimeNodeID(t, "rnode_alpha")
	errDone := make(chan error, 1)
	go func() {
		_, err := service.EnsureActive(context.Background(), worldID, nodeID)
		errDone <- err
	}()
	select {
	case <-runtime.startEntered:
	// fake runtime 只做内存 channel 同步；一秒仅为 CI 调度留余量并防止永久挂起，不参与业务时序。
	case <-time.After(time.Second):
		t.Fatal("EnsureActive did not reach runtime Start")
	}
	starting, found := store.currentSnapshot(worldID)
	if !found || starting.Phase() != PhaseStarting {
		t.Fatal("starting assignment was not committed before runtime ready")
	}
	clock.advance(testLeaseTTL)
	close(runtime.startRelease)
	if err := <-errDone; ErrorKindOf(err) != ErrorKindExpired {
		t.Fatalf("activate expiry error=%v", err)
	}
	if store.currentCount() != 0 || runtime.runningCount() != 0 || runtime.stopCount(starting.InstanceID()) != 1 {
		t.Fatal("activate expiry left starting assignment or runtime behind")
	}
}

// TestLeaseExpiryReplacementRejectsStaleWriter 验证 expiry 后的新 instance/fence 使旧 writer 永久失效。
func TestLeaseExpiryReplacementRejectsStaleWriter(t *testing.T) {
	t.Parallel()
	service, _, _, clock, _ := newTestService(t)
	worldID := mustPersonalWorldID(t, "pworld_expiry")
	nodeID := mustRuntimeNodeID(t, "rnode_alpha")
	first, err := service.EnsureActive(context.Background(), worldID, nodeID)
	if err != nil {
		t.Fatalf("first EnsureActive: %v", err)
	}
	clock.advance(testLeaseTTL)
	if _, err := service.Renew(context.Background(), first.Assignment().Stamp()); ErrorKindOf(err) != ErrorKindExpired {
		t.Fatalf("boundary renew error=%v", err)
	}
	second, err := service.EnsureActive(context.Background(), worldID, nodeID)
	if err != nil {
		t.Fatalf("second EnsureActive: %v", err)
	}
	if second.Assignment().InstanceID() == first.Assignment().InstanceID() || second.Assignment().Generation().Uint64() <= first.Assignment().Generation().Uint64() || second.Assignment().FencingToken().Uint64() <= first.Assignment().FencingToken().Uint64() {
		t.Fatal("expiry replacement reused instance, generation, or fence")
	}
	if _, err := service.QualifyWrite(context.Background(), first.Assignment().Stamp()); ErrorKindOf(err) != ErrorKindConflict {
		t.Fatalf("stale qualification error=%v", err)
	}
	if _, err := service.QualifyWrite(context.Background(), second.Assignment().Stamp()); err != nil {
		t.Fatalf("successor qualification: %v", err)
	}
}

// TestSleepRevokeBeforeStopAndReplay 验证 sleep 先失效 fence、重复请求 replay 且不创建 current。
func TestSleepRevokeBeforeStopAndReplay(t *testing.T) {
	t.Parallel()
	service, store, runtime, _, _ := newTestService(t)
	active, err := service.EnsureActive(context.Background(), mustPersonalWorldID(t, "pworld_sleep"), mustRuntimeNodeID(t, "rnode_alpha"))
	if err != nil {
		t.Fatalf("EnsureActive: %v", err)
	}
	result, err := service.Sleep(context.Background(), active.Assignment().Stamp())
	if err != nil || !result.Valid() || result.CleanupFailed() || store.currentCount() != 0 || runtime.runningCount() != 0 {
		t.Fatalf("Sleep result=%#v err=%v", result, err)
	}
	replay, err := service.Sleep(context.Background(), active.Assignment().Stamp())
	if err != nil || !replay.Valid() || !replay.Predecessor().Equal(result.Predecessor()) || store.currentCount() != 0 {
		t.Fatalf("Sleep replay=%#v err=%v", replay, err)
	}
}

// TestSleepStopFailureDoesNotRestoreFence 验证 cleanup failure 返回部分成功且旧 writer 仍失效。
func TestSleepStopFailureDoesNotRestoreFence(t *testing.T) {
	t.Parallel()
	service, store, runtime, _, _ := newTestService(t)
	active, err := service.EnsureActive(context.Background(), mustPersonalWorldID(t, "pworld_stopfail"), mustRuntimeNodeID(t, "rnode_alpha"))
	if err != nil {
		t.Fatalf("EnsureActive: %v", err)
	}
	runtime.setStopError(errors.New("stop failed"))
	result, err := service.Sleep(context.Background(), active.Assignment().Stamp())
	if !result.Valid() || !result.PlacementCommitted() || !result.CleanupFailed() || ErrorKindOf(err) != ErrorKindCleanupFailed || CommitPhaseOf(err) != CommitPhaseCommitted {
		t.Fatalf("Sleep result=%#v err=%v", result, err)
	}
	if store.currentCount() != 0 {
		t.Fatal("stop failure restored current assignment")
	}
	if _, err := service.QualifyWrite(context.Background(), active.Assignment().Stamp()); ErrorKindOf(err) != ErrorKindNotFound {
		t.Fatalf("revoked qualification error=%v", err)
	}
}

// TestSleepDrainFailureStillRevokesAndStops 验证 bounded drain 失败不延长旧 fence。
func TestSleepDrainFailureStillRevokesAndStops(t *testing.T) {
	t.Parallel()
	service, store, runtime, _, _ := newTestService(t)
	active, err := service.EnsureActive(context.Background(), mustPersonalWorldID(t, "pworld_drainfail"), mustRuntimeNodeID(t, "rnode_alpha"))
	if err != nil {
		t.Fatalf("EnsureActive: %v", err)
	}
	runtime.setDrainError(context.DeadlineExceeded)
	result, err := service.Sleep(context.Background(), active.Assignment().Stamp())
	if !result.Valid() || !result.DrainFailed() || result.CleanupFailed() ||
		ErrorKindOf(err) != ErrorKindCleanupFailed || store.currentCount() != 0 ||
		runtime.runningCount() != 0 {
		t.Fatalf("Sleep result=%#v err=%v", result, err)
	}
}

// TestSleepCommitUnknownCanReplay 验证 revoke 响应丢失后相同 stamp 重试会清理原 runtime。
func TestSleepCommitUnknownCanReplay(t *testing.T) {
	t.Parallel()
	service, store, runtime, _, _ := newTestService(t)
	active, err := service.EnsureActive(context.Background(), mustPersonalWorldID(t, "pworld_sleepunknown"), mustRuntimeNodeID(t, "rnode_alpha"))
	if err != nil {
		t.Fatalf("EnsureActive: %v", err)
	}
	store.setRevokeCommitUnknown()
	if result, err := service.Sleep(context.Background(), active.Assignment().Stamp()); result.Valid() || ErrorKindOf(err) != ErrorKindDependencyUnavailable || CommitPhaseOf(err) != CommitPhaseUnknown {
		t.Fatalf("first Sleep result=%#v err=%v", result, err)
	}
	if store.currentCount() != 0 || runtime.runningCount() != 1 {
		t.Fatal("commit-unknown revoke changed unexpected placement or runtime state")
	}
	replayed, err := service.Sleep(context.Background(), active.Assignment().Stamp())
	if err != nil || !replayed.Valid() || runtime.runningCount() != 0 {
		t.Fatalf("replayed Sleep result=%#v err=%v", replayed, err)
	}
}

// TestReplaceRebuildAndMigration 验证同节点/跨节点 successor 都严格推进 generation/fence。
func TestReplaceRebuildAndMigration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		targetNode string
	}{
		{name: "rebuild", targetNode: "rnode_alpha"},
		{name: "migration", targetNode: "rnode_beta"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service, store, runtime, _, _ := newTestService(t)
			worldID := mustPersonalWorldID(t, "pworld_"+test.name)
			predecessor, err := service.EnsureActive(context.Background(), worldID, mustRuntimeNodeID(t, "rnode_alpha"))
			if err != nil {
				t.Fatalf("EnsureActive: %v", err)
			}
			command := mustReplaceCommand(t, predecessor.Assignment().Stamp(), "winst_"+test.name, test.targetNode)
			successor, err := service.Replace(context.Background(), command)
			if err != nil || !successor.Valid() || successor.Assignment().NodeID() != command.TargetNodeID() {
				t.Fatalf("Replace result=%#v err=%v", successor, err)
			}
			if successor.Assignment().Generation().Uint64() <= predecessor.Assignment().Generation().Uint64() || successor.Assignment().FencingToken().Uint64() <= predecessor.Assignment().FencingToken().Uint64() {
				t.Fatal("replace did not advance generation/fence")
			}
			if _, err := service.QualifyWrite(context.Background(), predecessor.Assignment().Stamp()); ErrorKindOf(err) != ErrorKindConflict {
				t.Fatalf("predecessor qualification error=%v", err)
			}
			if _, err := service.QualifyWrite(context.Background(), successor.Assignment().Stamp()); err != nil {
				t.Fatalf("successor qualification: %v", err)
			}
			if store.currentCount() != 1 || runtime.runningCount() != 1 {
				t.Fatal("replace left more than one current or running runtime")
			}
		})
	}
}

// TestReplaceCommitUnknownReusesSuccessor 验证 replace response loss 只解析预生成 successor。
func TestReplaceCommitUnknownReusesSuccessor(t *testing.T) {
	t.Parallel()
	service, store, runtime, _, _ := newTestService(t)
	predecessor, err := service.EnsureActive(context.Background(), mustPersonalWorldID(t, "pworld_replaceunknown"), mustRuntimeNodeID(t, "rnode_alpha"))
	if err != nil {
		t.Fatalf("EnsureActive: %v", err)
	}
	store.setReplaceCommitUnknown()
	command := mustReplaceCommand(t, predecessor.Assignment().Stamp(), "winst_unknown", "rnode_beta")
	result, err := service.Replace(context.Background(), command)
	if err != nil || !result.Valid() || result.Disposition() != ResultDispositionReplayed || result.Assignment().InstanceID() != command.SuccessorID() {
		t.Fatalf("Replace result=%#v err=%v", result, err)
	}
	if runtime.runningCount() != 1 || runtime.startCount(command.SuccessorID()) != 1 {
		t.Fatal("replace commit-unknown duplicated successor runtime")
	}
}

// TestReplaceExternalReplayReusesCommittedSuccessor 验证时间推进后的相同 command 返回首次 assignment。
func TestReplaceExternalReplayReusesCommittedSuccessor(t *testing.T) {
	t.Parallel()
	service, _, runtime, clock, _ := newTestService(t)
	predecessor, err := service.EnsureActive(context.Background(), mustPersonalWorldID(t, "pworld_replacereplay"), mustRuntimeNodeID(t, "rnode_alpha"))
	if err != nil {
		t.Fatalf("EnsureActive: %v", err)
	}
	command := mustReplaceCommand(t, predecessor.Assignment().Stamp(), "winst_replay", "rnode_beta")
	first, err := service.Replace(context.Background(), command)
	if err != nil || !first.Valid() {
		t.Fatalf("first Replace result=%#v err=%v", first, err)
	}
	clock.advance(time.Millisecond)
	replayed, err := service.Replace(context.Background(), command)
	if err != nil || !replayed.Valid() || replayed.Disposition() != ResultDispositionReplayed || !replayed.Assignment().Equal(first.Assignment()) {
		t.Fatalf("replayed Replace result=%#v err=%v", replayed, err)
	}
	if runtime.startCount(command.SuccessorID()) != 1 || runtime.runningCount() != 1 {
		t.Fatal("external replace replay duplicated successor runtime")
	}
}

// TestReplaceStopFailureReturnsActiveSuccessor 验证 predecessor cleanup 失败不掩盖已 active successor。
func TestReplaceStopFailureReturnsActiveSuccessor(t *testing.T) {
	t.Parallel()
	service, store, runtime, _, _ := newTestService(t)
	predecessor, err := service.EnsureActive(context.Background(), mustPersonalWorldID(t, "pworld_replacecleanup"), mustRuntimeNodeID(t, "rnode_alpha"))
	if err != nil {
		t.Fatalf("EnsureActive: %v", err)
	}
	runtime.setStopError(errors.New("stop failed"))
	command := mustReplaceCommand(t, predecessor.Assignment().Stamp(), "winst_cleanup", "rnode_beta")
	successor, err := service.Replace(context.Background(), command)
	if !successor.Valid() || !successor.CleanupFailed() || ErrorKindOf(err) != ErrorKindCleanupFailed || CommitPhaseOf(err) != CommitPhaseCommitted {
		t.Fatalf("Replace result=%#v err=%v", successor, err)
	}
	current, found := store.currentSnapshot(predecessor.Assignment().WorldID())
	if !found || !current.Stamp().Equal(successor.Assignment().Stamp()) {
		t.Fatal("predecessor stop failure changed current successor")
	}
	if _, err := service.QualifyWrite(context.Background(), predecessor.Assignment().Stamp()); ErrorKindOf(err) != ErrorKindConflict {
		t.Fatalf("predecessor qualification error=%v", err)
	}
}

// TestReplaceDrainFailureStillCutsOver 验证 predecessor drain 失败后仍先推进 fence 再启动 successor。
func TestReplaceDrainFailureStillCutsOver(t *testing.T) {
	t.Parallel()
	service, store, runtime, _, _ := newTestService(t)
	predecessor, err := service.EnsureActive(context.Background(), mustPersonalWorldID(t, "pworld_replacedrain"), mustRuntimeNodeID(t, "rnode_alpha"))
	if err != nil {
		t.Fatalf("EnsureActive: %v", err)
	}
	runtime.setDrainError(context.DeadlineExceeded)
	command := mustReplaceCommand(t, predecessor.Assignment().Stamp(), "winst_replacedrain", "rnode_beta")
	successor, err := service.Replace(context.Background(), command)
	if !successor.Valid() || !successor.CleanupFailed() ||
		ErrorKindOf(err) != ErrorKindCleanupFailed ||
		runtime.drainCount(predecessor.Assignment().InstanceID()) != 1 {
		t.Fatalf("Replace result=%#v err=%v", successor, err)
	}
	current, found := store.currentSnapshot(predecessor.Assignment().WorldID())
	if !found || !current.Stamp().Equal(successor.Assignment().Stamp()) {
		t.Fatal("drain failure prevented successor cutover")
	}
}

// TestStaleSleepCannotRevokeSuccessor 验证 delayed predecessor sleep 与新 current 冲突。
func TestStaleSleepCannotRevokeSuccessor(t *testing.T) {
	t.Parallel()
	service, store, _, _, _ := newTestService(t)
	predecessor, err := service.EnsureActive(context.Background(), mustPersonalWorldID(t, "pworld_stalesleep"), mustRuntimeNodeID(t, "rnode_alpha"))
	if err != nil {
		t.Fatalf("EnsureActive: %v", err)
	}
	command := mustReplaceCommand(t, predecessor.Assignment().Stamp(), "winst_successor", "rnode_beta")
	successor, err := service.Replace(context.Background(), command)
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if _, err := service.Sleep(context.Background(), predecessor.Assignment().Stamp()); ErrorKindOf(err) != ErrorKindConflict {
		t.Fatalf("stale Sleep error=%v", err)
	}
	current, found := store.currentSnapshot(successor.Assignment().WorldID())
	if !found || !current.Stamp().Equal(successor.Assignment().Stamp()) {
		t.Fatal("stale sleep removed successor")
	}
}

// TestConcurrentEnsureMaintainsSingleWriter 以多 goroutine 验证 store 只发布一个 active fence。
func TestConcurrentEnsureMaintainsSingleWriter(t *testing.T) {
	t.Parallel()
	service, store, _, _, _ := newTestService(t)
	worldID := mustPersonalWorldID(t, "pworld_race")
	nodeID := mustRuntimeNodeID(t, "rnode_alpha")
	// 32 个 caller 足以在 race 下形成竞争，同时保持测试资源有界；正确性不依赖调度顺序。
	const callers = 32
	var waitGroup sync.WaitGroup
	results := make(chan AssignmentResult, callers)
	errorsFound := make(chan error, callers)
	for index := 0; index < callers; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			result, err := service.EnsureActive(context.Background(), worldID, nodeID)
			if err != nil {
				errorsFound <- err
				return
			}
			results <- result
		}()
	}
	waitGroup.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		if ErrorKindOf(err) != ErrorKindInProgress {
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	current, found := store.currentSnapshot(worldID)
	if !found || current.Phase() != PhaseActive || store.currentCount() != 1 {
		t.Fatal("concurrent ensure did not converge to one active assignment")
	}
	for result := range results {
		if !result.Assignment().Stamp().Equal(current.Stamp()) {
			t.Fatal("successful concurrent caller observed a different writer")
		}
	}
}

// TestMalformedStoreResultsFailClosed 验证 success outcome 不能携带零或不一致结果。
func TestMalformedStoreResultsFailClosed(t *testing.T) {
	t.Parallel()
	t.Run("resolve", func(t *testing.T) {
		service, store, _, _, _ := newTestService(t)
		store.setResolveOverride(AssignmentSnapshot{}, ResolveOutcomeFound, nil)
		_, err := service.EnsureActive(context.Background(), mustPersonalWorldID(t, "pworld_badresolve"), mustRuntimeNodeID(t, "rnode_alpha"))
		if ErrorKindOf(err) != ErrorKindDependencyUnavailable {
			t.Fatalf("malformed resolve error=%v", err)
		}
	})
	t.Run("acquire", func(t *testing.T) {
		service, store, _, _, _ := newTestService(t)
		store.setOverride(referenceOperationAcquire, AssignmentSnapshot{}, StoreOutcomeApplied, nil)
		_, err := service.EnsureActive(context.Background(), mustPersonalWorldID(t, "pworld_badacquire"), mustRuntimeNodeID(t, "rnode_alpha"))
		if ErrorKindOf(err) != ErrorKindDependencyUnavailable || CommitPhaseOf(err) != CommitPhaseCommitted {
			t.Fatalf("malformed acquire error=%v", err)
		}
	})
	t.Run("activate not committed", func(t *testing.T) {
		service, store, runtime, _, _ := newTestService(t)
		cause := errors.New("activate dependency unavailable")
		store.setOverride(referenceOperationActivate, AssignmentSnapshot{}, StoreOutcomeNotCommitted, cause)
		_, err := service.EnsureActive(context.Background(), mustPersonalWorldID(t, "pworld_activatefailure"), mustRuntimeNodeID(t, "rnode_alpha"))
		if ErrorKindOf(err) != ErrorKindDependencyUnavailable || CommitPhaseOf(err) != CommitPhaseCommitted || !errors.Is(err, cause) {
			t.Fatalf("activate failure error=%v", err)
		}
		if store.currentCount() != 0 || runtime.runningCount() != 0 {
			t.Fatal("confirmed uncommitted activate did not clean starting runtime")
		}
	})
	t.Run("qualification", func(t *testing.T) {
		service, store, _, _, _ := newTestService(t)
		active, err := service.EnsureActive(context.Background(), mustPersonalWorldID(t, "pworld_badqualify"), mustRuntimeNodeID(t, "rnode_alpha"))
		if err != nil {
			t.Fatalf("EnsureActive: %v", err)
		}
		store.setQualifyOverride(WriteFence{}, StoreOutcomeApplied, nil)
		_, err = service.QualifyWrite(context.Background(), active.Assignment().Stamp())
		if ErrorKindOf(err) != ErrorKindDependencyUnavailable {
			t.Fatalf("malformed qualify error=%v", err)
		}
	})
	t.Run("qualification success with error", func(t *testing.T) {
		service, store, _, _, _ := newTestService(t)
		active, err := service.EnsureActive(context.Background(), mustPersonalWorldID(t, "pworld_qualifyerror"), mustRuntimeNodeID(t, "rnode_alpha"))
		if err != nil {
			t.Fatalf("EnsureActive: %v", err)
		}
		cause := errors.New("qualification adapter contradiction")
		store.setQualifyOverride(WriteFence{}, StoreOutcomeApplied, cause)
		_, err = service.QualifyWrite(context.Background(), active.Assignment().Stamp())
		if ErrorKindOf(err) != ErrorKindDependencyUnavailable || CommitPhaseOf(err) != CommitPhaseNone || !errors.Is(err, cause) {
			t.Fatalf("qualification contradictory success error=%v", err)
		}
	})
}

// FuzzPlacementDrainReplaceTransitions 验证 drain、commit-unknown 与 cleanup 组合不产生双 current 或恢复旧 fence。
func FuzzPlacementDrainReplaceTransitions(f *testing.F) {
	for mode := byte(0); mode < 8; mode++ {
		f.Add(mode)
	}
	f.Fuzz(func(t *testing.T, mode byte) {
		service, store, runtime, _, _ := newTestService(t)
		worldID := mustPersonalWorldID(t, "pworld_fuzzlifecycle")
		predecessor, err := service.EnsureActive(context.Background(), worldID, mustRuntimeNodeID(t, "rnode_alpha"))
		if err != nil {
			t.Fatalf("EnsureActive: %v", err)
		}
		mode %= 8
		switch mode {
		case 0:
			_, _ = service.Sleep(context.Background(), predecessor.Assignment().Stamp())
		case 1:
			runtime.setDrainError(context.DeadlineExceeded)
			_, _ = service.Sleep(context.Background(), predecessor.Assignment().Stamp())
		case 2:
			runtime.setStopError(errors.New("stop failed"))
			_, _ = service.Sleep(context.Background(), predecessor.Assignment().Stamp())
		case 3:
			store.setRevokeCommitUnknown()
			_, _ = service.Sleep(context.Background(), predecessor.Assignment().Stamp())
			_, _ = service.Sleep(context.Background(), predecessor.Assignment().Stamp())
		default:
			if mode == 6 {
				store.setReplaceCommitUnknown()
			}
			if mode == 7 {
				runtime.setDrainError(context.DeadlineExceeded)
			}
			targetNode := "rnode_alpha"
			if mode == 5 || mode == 6 || mode == 7 {
				targetNode = "rnode_beta"
			}
			command := mustReplaceCommand(t, predecessor.Assignment().Stamp(), "winst_fuzzsuccessor", targetNode)
			_, _ = service.Replace(context.Background(), command)
		}
		if store.currentCount() > 1 {
			t.Fatal("lifecycle transition published multiple current assignments")
		}
		if runtime.runningCount() > 2 {
			t.Fatal("lifecycle transition leaked unbounded runtimes")
		}
		current, found := store.currentSnapshot(worldID)
		if found {
			if _, err := service.QualifyWrite(context.Background(), current.Stamp()); err != nil {
				t.Fatalf("current assignment lost write qualification: %v", err)
			}
			if current.Stamp().Equal(predecessor.Assignment().Stamp()) {
				return
			}
		}
		if _, err := service.QualifyWrite(context.Background(), predecessor.Assignment().Stamp()); err == nil {
			t.Fatal("completed transition restored predecessor write qualification")
		}
	})
}

const (
	// testLeaseTTL 足够覆盖无 sleep 的确定性测试，又能精确推进到 expiry 边界。
	testLeaseTTL = 30 * time.Second
)

// newTestService 构造不接触 listener、MySQL、Redis 或 wall clock 的 placement application graph。
func newTestService(t *testing.T) (*Service, *referenceStore, *fakeRuntimeController, *fakeClock, *sequenceIDGenerator) {
	t.Helper()
	store := newReferenceStore()
	runtime := newFakeRuntimeController()
	clock := &fakeClock{now: time.Date(2026, 7, 13, 6, 0, 0, 0, time.UTC)}
	ids := new(sequenceIDGenerator)
	service, err := NewService(store, runtime, clock, ids, testLeaseTTL)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service, store, runtime, clock, ids
}

// mustRuntimeNodeID 构造测试用 node identity，失败表示 fixture 本身无效。
func mustRuntimeNodeID(t *testing.T, value string) RuntimeNodeID {
	t.Helper()
	nodeID, err := NewRuntimeNodeID(value)
	if err != nil {
		t.Fatalf("NewRuntimeNodeID: %v", err)
	}
	return nodeID
}

// mustReplaceCommand 构造测试用 replace command，失败表示 fixture 本身无效。
func mustReplaceCommand(t *testing.T, expected AssignmentStamp, successorValue string, nodeValue string) ReplaceCommand {
	t.Helper()
	successorID, err := NewWorldInstanceID(successorValue)
	if err != nil {
		t.Fatalf("NewWorldInstanceID: %v", err)
	}
	command, err := NewReplaceCommand(expected, successorID, mustRuntimeNodeID(t, nodeValue))
	if err != nil {
		t.Fatalf("NewReplaceCommand: %v", err)
	}
	return command
}
