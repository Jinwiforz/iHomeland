package visitsession

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestReferenceStoreConcurrentOpen 保证同一 PersonalWorld 至多创建一个 active VisitSession。
func TestReferenceStoreConcurrentOpen(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 1)
	store := newReferenceStore()
	const workers = 8
	var wait sync.WaitGroup
	outcomes := make(chan CreateOutcome, workers)
	for index := 0; index < workers; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			visitID, _ := NewVisitSessionID("vses_concurrent" + strconvFormat(uint64(index+1)))
			visit, err := NewVisitSession(visitID, fixture.ownerBinding, fixture.worldID, fixture.assignment, fixture.policy, fixture.createdAt)
			if err != nil {
				t.Errorf("create aggregate: %v", err)
				return
			}
			commandID, _ := NewCommandID("vcmd_concurrent" + strconvFormat(uint64(index+1)))
			fingerprint := fingerprintCommand(OperationOpen, strconvFormat(uint64(index+1)))
			record, _ := NewCreateRecord(commandID, fingerprint, visit.Snapshot())
			_, outcome, err := store.Create(context.Background(), record)
			if err != nil {
				t.Errorf("store create: %v", err)
				return
			}
			outcomes <- outcome
		}()
	}
	wait.Wait()
	close(outcomes)
	created := 0
	for outcome := range outcomes {
		if outcome == CreateOutcomeCreated {
			created++
		} else if outcome != CreateOutcomeExisting {
			t.Fatalf("unexpected outcome %v", outcome)
		}
	}
	if created != 1 {
		t.Fatalf("created %d active sessions, want 1", created)
	}
}

// TestReferenceStoreConcurrentInvite 证明相同 expected revision 的邀请只提交一个且 revision 单调。
func TestReferenceStoreConcurrentInvite(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 2)
	visit := openFixture(t, fixture)
	base := visit.Snapshot()
	store := seedReferenceStore(t, visit)
	visitors := []Actor{fixture.visitorA, fixture.visitorB}
	records := make([]TransitionRecord, 0, len(visitors))
	for index, visitor := range visitors {
		inviteID := mustInviteID(t, "vinv_concurrent"+strconvFormat(uint64(index+1)))
		target, invite, err := visit.CreateInvite(fixture.owner, inviteID, visitor.playerID, fixture.createdAt.Add(time.Minute), fixture.createdAt)
		if err != nil {
			t.Fatal(err)
		}
		fingerprint := fingerprintCommand(OperationCreateInvite, visitor.playerID.String())
		records = append(records, mustTransitionRecord(t, OperationCreateInvite, base, target.Snapshot(), "vcmd_inviteRace"+strconvFormat(uint64(index+1)), fingerprint, invite, AdmissionIntent{}, MembershipSnapshot{}, nil))
	}
	outcomes := commitRace(t, store, records)
	if outcomes[MutationOutcomeApplied] != 1 || outcomes[MutationOutcomeRevisionConflict] != 1 {
		t.Fatalf("unexpected outcomes: %#v", outcomes)
	}
	stored, _, _ := store.FindByID(context.Background(), base.ID())
	if len(stored.Invites()) != 1 || stored.Revision() != base.Revision()+1 {
		t.Fatalf("invalid invite race result: %#v", stored)
	}
}

// TestReferenceStoreConcurrentAcceptLastSlot 证明相同 expected revision 下只有一个 Visitor 取得最后 capacity。
func TestReferenceStoreConcurrentAcceptLastSlot(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 1)
	visit := openFixture(t, fixture)
	visit = addPending(t, visit, fixture, fixture.visitorA, "vinv_slotA", fixture.createdAt)
	visit = addPending(t, visit, fixture, fixture.visitorB, "vinv_slotB", fixture.createdAt.Add(time.Second))
	store := seedReferenceStore(t, visit)
	base := visit.Snapshot()
	visitors := []Actor{fixture.visitorA, fixture.visitorB}
	inviteIDs := []InviteID{mustInviteID(t, "vinv_slotA"), mustInviteID(t, "vinv_slotB")}
	var wait sync.WaitGroup
	outcomes := make(chan MutationOutcome, 2)
	for index := range visitors {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			domain, err := HydrateVisitSession(base)
			if err != nil {
				t.Errorf("hydrate: %v", err)
				return
			}
			deadline := fixture.createdAt.Add(30 * time.Second)
			target, admission, err := domain.AcceptInvite(visitors[index], inviteIDs[index], deadline, fixture.assignment, fixture.createdAt.Add(3*time.Second))
			if err != nil {
				t.Errorf("accept proposal: %v", err)
				return
			}
			commandID, _ := NewCommandID("vcmd_slot" + strconvFormat(uint64(index+1)))
			fingerprint := fingerprintCommand(OperationAcceptInvite, visitors[index].playerID.String())
			result, _ := NewMutationResult(OperationAcceptInvite, target.Snapshot(), commandID, fingerprint, InviteSnapshot{}, admission, MembershipSnapshot{}, nil)
			record, _ := NewTransitionRecord(OperationAcceptInvite, base.ID(), base.Revision(), commandID, fingerprint, result)
			_, outcome, err := store.Commit(context.Background(), record)
			if err != nil {
				t.Errorf("commit: %v", err)
				return
			}
			outcomes <- outcome
		}()
	}
	wait.Wait()
	close(outcomes)
	applied, conflicts := 0, 0
	for outcome := range outcomes {
		if outcome == MutationOutcomeApplied {
			applied++
		} else if outcome == MutationOutcomeRevisionConflict {
			conflicts++
		} else {
			t.Fatalf("unexpected outcome %v", outcome)
		}
	}
	if applied != 1 || conflicts != 1 {
		t.Fatalf("applied=%d conflicts=%d", applied, conflicts)
	}
	stored, _, _ := store.FindByID(context.Background(), base.ID())
	if stored.ActiveMemberCount() != 1 || stored.Revision() != base.Revision()+1 {
		t.Fatalf("invalid concurrent result: %#v", stored)
	}
}

// TestReferenceStoreResponseLossAndReplay 验证 observedAt 推进后相同 command 返回首次完整 directives。
func TestReferenceStoreResponseLossAndReplay(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 1)
	joined, binding := joinedFixture(t, fixture, fixture.visitorA, "vinv_loss", "vbind_loss")
	store := seedReferenceStore(t, joined)
	target, directive, err := joined.Leave(fixture.visitorA, binding)
	if err != nil {
		t.Fatal(err)
	}
	commandID, _ := NewCommandID("vcmd_responseLoss")
	fingerprint := fingerprintCommand(OperationLeave, fixture.visitorA.playerID.String(), binding.value)
	result, _ := NewMutationResult(OperationLeave, target.Snapshot(), commandID, fingerprint, InviteSnapshot{}, AdmissionIntent{}, MembershipSnapshot{}, []SafeReturnDirective{directive})
	record, _ := NewTransitionRecord(OperationLeave, joined.ID(), joined.Revision(), commandID, fingerprint, result)
	store.loseNextMutationResponse()
	if returned, outcome, err := store.Commit(context.Background(), record); outcome != MutationOutcomeCommitUnknown || err == nil || !mutationResultEmpty(returned) {
		t.Fatalf("first response: outcome=%v err=%v result=%#v", outcome, err, returned)
	}
	probe, _ := NewConflictProbe(OperationLeave, joined.ID(), joined.Revision(), commandID, fingerprint)
	replayed, outcome, err := store.Commit(context.Background(), probe)
	if err != nil || outcome != MutationOutcomeReplay || len(replayed.Directives()) != 1 || replayed.Directives()[0] != directive {
		t.Fatalf("replay: outcome=%v err=%v result=%#v", outcome, err, replayed)
	}
	conflictFingerprint := fingerprintCommand(OperationLeave, fixture.visitorA.playerID.String(), "different-binding")
	conflictProbe, _ := NewConflictProbe(OperationLeave, joined.ID(), joined.Revision(), commandID, conflictFingerprint)
	if _, outcome, err := store.Commit(context.Background(), conflictProbe); err != nil || outcome != MutationOutcomeIdempotencyConflict {
		t.Fatalf("idempotency conflict: outcome=%v err=%v", outcome, err)
	}
}

// TestReferenceStoreNotCommittedAndCommitUnknownDoNotWrite 证明失败注入不会伪造 snapshot 或自动补写。
func TestReferenceStoreNotCommittedAndCommitUnknownDoNotWrite(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 1)
	visit, _ := invitedFixture(t, fixture, fixture.visitorA, "vinv_failure")
	store := seedReferenceStore(t, visit)
	target, err := visit.RevokeInvite(fixture.owner, mustInviteID(t, "vinv_failure"), fixture.createdAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	commandID, _ := NewCommandID("vcmd_failure")
	fingerprint := fingerprintCommand(OperationRevokeInvite, "failure")
	result, _ := NewMutationResult(OperationRevokeInvite, target.Snapshot(), commandID, fingerprint, InviteSnapshot{}, AdmissionIntent{}, MembershipSnapshot{}, nil)
	record, _ := NewTransitionRecord(OperationRevokeInvite, visit.ID(), visit.Revision(), commandID, fingerprint, result)
	for _, outcome := range []MutationOutcome{MutationOutcomeNotCommitted, MutationOutcomeCommitUnknown} {
		store.failNextMutation(outcome, errors.New("injected"))
		if returned, got, err := store.Commit(context.Background(), record); got != outcome || err == nil || !mutationResultEmpty(returned) {
			t.Fatalf("outcome=%v got=%v err=%v", outcome, got, err)
		}
		stored, _, _ := store.FindByID(context.Background(), visit.ID())
		if !stored.Equal(visit.Snapshot()) {
			t.Fatalf("failure %v wrote state", outcome)
		}
	}
}

// TestVisitorReconnectCASRace 证明旧 reconnect timer 与新 binding 竞争时不能在新 revision 上继续删除。
func TestVisitorReconnectCASRace(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 1)
	joined, oldBinding := joinedFixture(t, fixture, fixture.visitorA, "vinv_raceVisitor", "vbind_raceOld")
	deadline := fixture.createdAt.Add(40 * time.Second)
	reconnecting, err := joined.VisitorDisconnect(fixture.visitorA, oldBinding, deadline, fixture.createdAt.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	base := reconnecting.Snapshot()
	member := base.Memberships()[0]
	newBinding := mustBindingID(t, "vbind_raceNew")
	recovered, recoveredMember, err := reconnecting.VisitorReconnect(fixture.visitorA, newBinding, fixture.assignment, fixture.createdAt.Add(20*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	expired, directive, err := reconnecting.ExpireVisitorReconnect(fixture.visitorA.playerID, member.ReconnectGeneration(), oldBinding, deadline, deadline)
	if err != nil {
		t.Fatal(err)
	}
	store := seedReferenceStore(t, reconnecting)
	records := []TransitionRecord{
		mustTransitionRecord(t, OperationVisitorReconnect, base, recovered.Snapshot(), "vcmd_raceReconnect", fingerprintCommand(OperationVisitorReconnect, "reconnect"), InviteSnapshot{}, AdmissionIntent{}, recoveredMember, nil),
		mustTransitionRecord(t, OperationExpireVisitorReconnect, base, expired.Snapshot(), "vcmd_raceExpire", fingerprintCommand(OperationExpireVisitorReconnect, "expire"), InviteSnapshot{}, AdmissionIntent{}, MembershipSnapshot{}, []SafeReturnDirective{directive}),
	}
	outcomes := commitRace(t, store, records)
	if outcomes[MutationOutcomeApplied] != 1 || outcomes[MutationOutcomeRevisionConflict] != 1 {
		t.Fatalf("unexpected outcomes: %#v", outcomes)
	}
	stored, _, _ := store.FindByID(context.Background(), base.ID())
	if len(stored.Memberships()) == 1 && stored.Memberships()[0].BindingID() != newBinding {
		t.Fatalf("old timer retained unexpected binding: %#v", stored.Memberships())
	}
}

// TestOwnerReconnectCASRace 证明 old grace timer 不能关闭已经恢复到新 revision 的 Owner binding。
func TestOwnerReconnectCASRace(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 1)
	joined, _ := joinedFixture(t, fixture, fixture.visitorA, "vinv_raceOwner", "vbind_raceVisitor")
	deadline := fixture.createdAt.Add(40 * time.Second)
	grace, err := joined.OwnerDisconnect(fixture.owner, fixture.ownerBinding.connectionID, deadline, fixture.createdAt.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	base := grace.Snapshot()
	newOwner := testActor(t, fixture.owner.playerID.String(), "ses_raceOwnerNew", 2)
	recovered, err := grace.OwnerReconnect(newOwner, mustBindingID(t, "vbind_raceOwnerNew"), fixture.assignment, fixture.createdAt.Add(20*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	closed, directives, err := grace.ExpireOwnerGrace(base.OwnerGraceGeneration(), base.OwnerBinding().ConnectionID(), deadline, deadline)
	if err != nil {
		t.Fatal(err)
	}
	store := seedReferenceStore(t, grace)
	records := []TransitionRecord{
		mustTransitionRecord(t, OperationOwnerReconnect, base, recovered.Snapshot(), "vcmd_ownerReconnect", fingerprintCommand(OperationOwnerReconnect, "reconnect"), InviteSnapshot{}, AdmissionIntent{}, MembershipSnapshot{}, nil),
		mustTransitionRecord(t, OperationExpireOwnerGrace, base, closed.Snapshot(), "vcmd_ownerExpire", fingerprintCommand(OperationExpireOwnerGrace, "expire"), InviteSnapshot{}, AdmissionIntent{}, MembershipSnapshot{}, directives),
	}
	outcomes := commitRace(t, store, records)
	if outcomes[MutationOutcomeApplied] != 1 || outcomes[MutationOutcomeRevisionConflict] != 1 {
		t.Fatalf("unexpected outcomes: %#v", outcomes)
	}
	stored, _, _ := store.FindByID(context.Background(), base.ID())
	if stored.OwnerID() != fixture.owner.playerID {
		t.Fatalf("owner succeeded to visitor: %#v", stored)
	}
	if stored.Lifecycle() == LifecycleOpen && stored.OwnerBinding().ConnectionID() != mustBindingID(t, "vbind_raceOwnerNew") {
		t.Fatalf("old timer changed recovered owner binding: %#v", stored)
	}
}

// mustTransitionRecord 构造 race 测试使用的完整 target result 与 CAS record。
func mustTransitionRecord(t *testing.T, operation Operation, base Snapshot, target Snapshot, commandValue string, fingerprint CommandFingerprint, invite InviteSnapshot, admission AdmissionIntent, membership MembershipSnapshot, directives []SafeReturnDirective) TransitionRecord {
	t.Helper()
	commandID := mustCommandID(t, commandValue)
	result, err := NewMutationResult(operation, target, commandID, fingerprint, invite, admission, membership, directives)
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewTransitionRecord(operation, base.ID(), base.Revision(), commandID, fingerprint, result)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

// commitRace 同时提交多个相同 expected revision 的 record，并统计线性化结果。
func commitRace(t *testing.T, store *referenceStore, records []TransitionRecord) map[MutationOutcome]int {
	t.Helper()
	var wait sync.WaitGroup
	channel := make(chan MutationOutcome, len(records))
	for _, record := range records {
		record := record
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, outcome, err := store.Commit(context.Background(), record)
			if err != nil {
				t.Errorf("race commit: %v", err)
				return
			}
			channel <- outcome
		}()
	}
	wait.Wait()
	close(channel)
	outcomes := make(map[MutationOutcome]int)
	for outcome := range channel {
		outcomes[outcome]++
	}
	return outcomes
}

// seedReferenceStore 安装既有 snapshot 与 active index，不创建 production memory fallback。
func seedReferenceStore(t *testing.T, visit VisitSession) *referenceStore {
	t.Helper()
	store := newReferenceStore()
	snapshot := visit.Snapshot()
	store.snapshots[snapshot.ID().value] = snapshot
	if snapshot.Lifecycle() != LifecycleClosed {
		store.activeByWorld[snapshot.WorldID().String()] = snapshot.ID()
	}
	return store
}

// addPending 在既有 aggregate 中添加 pending invite，保持 capacity 未占用。
func addPending(t *testing.T, visit VisitSession, fixture aggregateFixture, visitor Actor, inviteValue string, observedAt time.Time) VisitSession {
	t.Helper()
	target, _, err := visit.CreateInvite(fixture.owner, mustInviteID(t, inviteValue), visitor.playerID, observedAt.Add(time.Minute), observedAt)
	if err != nil {
		t.Fatal(err)
	}
	return target
}
