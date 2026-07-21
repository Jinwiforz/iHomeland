package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
)

// TestGameplayMutationIDsTranslateNamespaces 验证transport前缀不会进入VisitSession identifier正文。
func TestGameplayMutationIDsTranslateNamespaces(t *testing.T) {
	t.Parallel()
	binding, command, err := gameplayMutationIDs("tcp_0123456789abcdef0123456789abcdef", strings.Repeat("ab", 16))
	if err != nil || !binding.Valid() || !command.Valid() {
		t.Fatalf("gameplayMutationIDs binding=%v command=%v err=%v", binding, command, err)
	}
	if _, _, err := gameplayMutationIDs("invalid", strings.Repeat("ab", 16)); err == nil {
		t.Fatal("connection identity without tcp_ namespace was accepted")
	}
}

// TestReconcileVisitOpenUsesOneAuthoritativeRecovery 验证stale之外不恢复且恢复只重新解析一次。
func TestReconcileVisitOpenUsesOneAuthoritativeRecovery(t *testing.T) {
	t.Parallel()
	fixture := newStaleOpenCoordinatorFixture(t, false)
	staleErr := staleOpenTestError(t, fixture)
	reconciler := newRecordingStaleOpenReconciler(nil)
	openCalls := 0
	_, err := reconcileVisitOpen(context.Background(), fixture.worldID, func() (visitsession.OpenResult, error) {
		openCalls++
		if openCalls == 1 {
			return visitsession.OpenResult{}, staleErr
		}
		return visitsession.OpenResult{}, nil
	}, reconciler)
	if err != nil || openCalls != 2 || reconciler.calls != 1 {
		t.Fatalf("reconcile result calls=%d reconciles=%d err=%v", openCalls, reconciler.calls, err)
	}

	openCalls = 0
	reconciler = newRecordingStaleOpenReconciler(nil)
	ordinary := errors.New("ordinary dependency failure")
	_, err = reconcileVisitOpen(context.Background(), fixture.worldID, func() (visitsession.OpenResult, error) {
		openCalls++
		return visitsession.OpenResult{}, ordinary
	}, reconciler)
	if !errors.Is(err, ordinary) || openCalls != 1 || reconciler.calls != 0 {
		t.Fatalf("ordinary error calls=%d reconciles=%d err=%v", openCalls, reconciler.calls, err)
	}
}

// TestReconcileVisitOpenFailsClosedOnUnknownTerminal 验证恢复失败不会执行第二次Open。
func TestReconcileVisitOpenFailsClosedOnUnknownTerminal(t *testing.T) {
	t.Parallel()
	fixture := newStaleOpenCoordinatorFixture(t, false)
	staleErr := staleOpenTestError(t, fixture)
	fixture.store.failNext(visitsession.MutationOutcomeCommitUnknown, errors.New("injected response loss"))
	commitUnknown := fixture.coordinator.ReconcileStaleOpen(context.Background(), fixture.worldID)
	if !visitsession.IsErrorCode(commitUnknown, visitsession.ErrorCodeCommitUnknown) {
		t.Fatalf("fixture did not produce commit unknown: %v", commitUnknown)
	}
	reconciler := newRecordingStaleOpenReconciler(commitUnknown)
	openCalls := 0
	result, err := reconcileVisitOpen(context.Background(), fixture.worldID, func() (visitsession.OpenResult, error) {
		openCalls++
		return visitsession.OpenResult{}, staleErr
	}, reconciler)
	if result.Valid() || !visitsession.IsErrorCode(err, visitsession.ErrorCodeCommitUnknown) || openCalls != 1 || reconciler.calls != 1 {
		t.Fatalf("unknown result=%#v calls=%d reconciles=%d err=%v", result, openCalls, reconciler.calls, err)
	}
}

// TestReconcileVisitOpenDoesNotLoopOnPersistentStale 验证第二次权威结果原样返回而非循环追赶。
func TestReconcileVisitOpenDoesNotLoopOnPersistentStale(t *testing.T) {
	t.Parallel()
	fixture := newStaleOpenCoordinatorFixture(t, false)
	staleErr := staleOpenTestError(t, fixture)
	reconciler := newRecordingStaleOpenReconciler(nil)
	openCalls := 0
	_, err := reconcileVisitOpen(context.Background(), fixture.worldID, func() (visitsession.OpenResult, error) {
		openCalls++
		return visitsession.OpenResult{}, staleErr
	}, reconciler)
	if !visitsession.IsErrorCode(err, visitsession.ErrorCodeStale) || openCalls != 2 || reconciler.calls != 1 {
		t.Fatalf("persistent stale calls=%d reconciles=%d err=%v", openCalls, reconciler.calls, err)
	}
}

// staleOpenTestError 从真实领域transition取得稳定stale分类，不伪造Error内部字段。
func staleOpenTestError(t *testing.T, fixture staleOpenCoordinatorFixture) error {
	t.Helper()
	commandID, _ := visitsession.NewCommandID("vcmd_staleOpenErrorFixture")
	_, err := fixture.service.ExpireSession(context.Background(), fixture.visitID, fixture.now.Add(2*time.Hour), visitsession.InitialRevision, commandID)
	if !visitsession.IsErrorCode(err, visitsession.ErrorCodeStale) {
		t.Fatalf("stale fixture error=%v", err)
	}
	return err
}

// recordingStaleOpenReconciler 记录application是否越界重复执行恢复。
type recordingStaleOpenReconciler struct {
	// calls 是恢复端口调用次数。
	calls int
	// err 是恢复提交的稳定结果。
	err error
}

// newRecordingStaleOpenReconciler 创建固定结果的恢复记录器。
func newRecordingStaleOpenReconciler(err error) *recordingStaleOpenReconciler {
	return &recordingStaleOpenReconciler{err: err}
}

// ReconcileStaleOpen 记录一次调用并返回预设结果。
func (reconciler *recordingStaleOpenReconciler) ReconcileStaleOpen(_ context.Context, _ personalworld.PersonalWorldID) error {
	reconciler.calls++
	return reconciler.err
}
