package visitsession

import (
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/placement"
)

// TestVisitSessionOpenAndInvite 验证初始 binding、capacity 计数与 Owner-only 定向邀请。
func TestVisitSessionOpenAndInvite(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 1)
	visit := openFixture(t, fixture)
	if visit.Revision() != InitialRevision || visit.Lifecycle() != LifecycleOpen || visit.Snapshot().ActiveMemberCount() != 0 {
		t.Fatalf("unexpected initial snapshot: %#v", visit.Snapshot())
	}
	invited, invite, err := visit.CreateInvite(fixture.owner, mustInviteID(t, "vinv_open"), fixture.visitorA.playerID, fixture.createdAt.Add(time.Minute), fixture.createdAt)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if invited.Snapshot().ActiveMemberCount() != 0 || len(invited.Snapshot().Invites()) != 1 || invite.State() != InviteStatePending {
		t.Fatalf("invite changed capacity: %#v", invited.Snapshot())
	}
	if _, _, err := visit.CreateInvite(fixture.visitorA, mustInviteID(t, "vinv_forbidden"), fixture.visitorB.playerID, fixture.createdAt.Add(time.Minute), fixture.createdAt); !IsErrorCode(err, ErrorCodeForbidden) {
		t.Fatalf("visitor invite should be forbidden: %v", err)
	}
	if _, _, err := visit.CreateInvite(fixture.owner, mustInviteID(t, "vinv_owner"), fixture.owner.playerID, fixture.createdAt.Add(time.Minute), fixture.createdAt); !IsErrorCode(err, ErrorCodeInvalidArgument) {
		t.Fatalf("owner self invite should fail: %v", err)
	}
}

// TestInviteRevokeAndExpiry 验证 Owner revoke 与 system expiry 只作用于 matching pending invite。
func TestInviteRevokeAndExpiry(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 1)
	visit, inviteID := invitedFixture(t, fixture, fixture.visitorA, "vinv_cleanup")
	deadline := visit.Snapshot().Invites()[0].ExpiresAt()
	if _, err := visit.ExpireInvite(inviteID, deadline, deadline.Add(-time.Microsecond)); !IsErrorCode(err, ErrorCodeInvalidState) {
		t.Fatalf("early invite expiry should fail: %v", err)
	}
	expired, err := visit.ExpireInvite(inviteID, deadline, deadline)
	if err != nil || len(expired.Snapshot().Invites()) != 0 {
		t.Fatalf("expire invite: snapshot=%#v err=%v", expired.Snapshot(), err)
	}
	visit, inviteID = invitedFixture(t, fixture, fixture.visitorA, "vinv_revoke")
	revoked, err := visit.RevokeInvite(fixture.owner, inviteID, fixture.createdAt.Add(time.Second))
	if err != nil || len(revoked.Snapshot().Invites()) != 0 {
		t.Fatalf("revoke invite: snapshot=%#v err=%v", revoked.Snapshot(), err)
	}
}

// TestAcceptJoinAndReservationExpiry 验证 accept 原子占位、qualification join 与 deadline 等号边界。
func TestAcceptJoinAndReservationExpiry(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 1)
	visit, inviteID := invitedFixture(t, fixture, fixture.visitorA, "vinv_accept")
	reservationDeadline := fixture.createdAt.Add(20 * time.Second)
	reserved, intent, err := visit.AcceptInvite(fixture.visitorA, inviteID, reservationDeadline, fixture.assignment, fixture.createdAt.Add(time.Second))
	if err != nil {
		t.Fatalf("accept invite: %v", err)
	}
	if !intent.Valid() || reserved.Snapshot().ActiveMemberCount() != 1 || reserved.Snapshot().Memberships()[0].State() != MembershipStateReserved {
		t.Fatalf("invalid accept result: %#v", reserved.Snapshot())
	}
	if reserved.Snapshot().Memberships()[0].Role() != RoleUnspecified || reserved.RoleOf(fixture.visitorA) != RoleUnspecified || reserved.RoleOf(fixture.owner) != RoleOwner {
		t.Fatal("reserved membership granted a Visitor role or Owner role projection was lost")
	}
	if _, _, err := visit.AcceptInvite(fixture.visitorB, inviteID, reservationDeadline, fixture.assignment, fixture.createdAt.Add(time.Second)); !IsErrorCode(err, ErrorCodeForbidden) {
		t.Fatalf("non-target accept should fail: %v", err)
	}
	binding := mustBindingID(t, "vbind_visitorA")
	shortIntent, _ := HydrateAdmissionIntent(reserved.ID(), fixture.visitorA.playerID, fixture.visitorA.sessionID, fixture.visitorA.epoch, reserved.Assignment(), reservationDeadline.Add(-5*time.Second))
	if _, _, err := reserved.Join(fixture.visitorA, JoinQualification{intent: shortIntent, purpose: qualificationPurposeJoin}, binding, fixture.assignment, reservationDeadline.Add(-10*time.Second)); err != nil {
		t.Fatalf("short-lived credential within reservation was rejected: %v", err)
	}
	if _, _, err := reserved.Join(fixture.visitorA, JoinQualification{intent: intent, purpose: qualificationPurposeJoin}, binding, fixture.assignment, reservationDeadline); !IsErrorCode(err, ErrorCodeStale) {
		t.Fatalf("join at deadline should fail: %v", err)
	}
	joined, member, err := reserved.Join(fixture.visitorA, JoinQualification{intent: intent, purpose: qualificationPurposeJoin}, binding, fixture.assignment, reservationDeadline.Add(-time.Microsecond))
	if err != nil || member.State() != MembershipStateJoined {
		t.Fatalf("join reservation: member=%#v err=%v", member, err)
	}
	if member.Role() != RoleVisitor || joined.RoleOf(fixture.visitorA) != RoleVisitor {
		t.Fatal("joined membership did not receive the Visitor role")
	}
	if _, _, err := reserved.Join(fixture.visitorA, JoinQualification{intent: intent, purpose: qualificationPurposeReconnect}, binding, fixture.assignment, fixture.createdAt.Add(2*time.Second)); !IsErrorCode(err, ErrorCodeStale) {
		t.Fatalf("reconnect qualification was accepted by join: %v", err)
	}
	newEpoch := testActor(t, fixture.visitorA.playerID.String(), "ses_roleNew", fixture.visitorA.epoch+1)
	if joined.RoleOf(newEpoch) != RoleUnspecified {
		t.Fatal("stale membership granted a changed session lineage the Visitor role")
	}
	if _, _, err := joined.Join(fixture.visitorA, JoinQualification{intent: intent, purpose: qualificationPurposeJoin}, binding, fixture.assignment, fixture.createdAt.Add(2*time.Second)); !IsErrorCode(err, ErrorCodeInvalidState) {
		t.Fatalf("join twice should fail: %v", err)
	}

	visit, inviteID = invitedFixture(t, fixture, fixture.visitorA, "vinv_expire")
	reserved, _, err = visit.AcceptInvite(fixture.visitorA, inviteID, reservationDeadline, fixture.assignment, fixture.createdAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reserved.ExpireReservation(fixture.visitorA.playerID, inviteID, reservationDeadline, reservationDeadline.Add(-time.Microsecond)); !IsErrorCode(err, ErrorCodeInvalidState) {
		t.Fatalf("early reservation expiry should fail: %v", err)
	}
	expired, err := reserved.ExpireReservation(fixture.visitorA.playerID, inviteID, reservationDeadline, reservationDeadline)
	if err != nil || expired.Snapshot().ActiveMemberCount() != 0 || len(expired.Snapshot().Invites()) != 0 {
		t.Fatalf("expire reservation: snapshot=%#v err=%v", expired.Snapshot(), err)
	}
}

// TestVisitorLeaveKickAndReconnect 验证精确 binding、防 ABA callback 与单成员 safe-return。
func TestVisitorLeaveKickAndReconnect(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 2)
	joined, originalBinding := joinedFixture(t, fixture, fixture.visitorA, "vinv_reconnect", "vbind_old")
	reconnectDeadline := fixture.createdAt.Add(40 * time.Second)
	reconnecting, err := joined.VisitorDisconnect(fixture.visitorA, originalBinding, reconnectDeadline, fixture.createdAt.Add(10*time.Second))
	if err != nil {
		t.Fatalf("disconnect visitor: %v", err)
	}
	newBinding := mustBindingID(t, "vbind_new")
	reconnectIntent, _ := HydrateAdmissionIntent(reconnecting.ID(), fixture.visitorA.playerID, fixture.visitorA.sessionID, fixture.visitorA.epoch, reconnecting.Assignment(), reconnectDeadline)
	shortReconnectIntent, _ := HydrateAdmissionIntent(reconnecting.ID(), fixture.visitorA.playerID, fixture.visitorA.sessionID, fixture.visitorA.epoch, reconnecting.Assignment(), reconnectDeadline.Add(-5*time.Second))
	if _, _, err := reconnecting.VisitorReconnect(fixture.visitorA, JoinQualification{intent: shortReconnectIntent, purpose: qualificationPurposeReconnect}, newBinding, fixture.assignment, fixture.createdAt.Add(20*time.Second)); err != nil {
		t.Fatalf("short-lived reconnect credential within grace was rejected: %v", err)
	}
	if _, _, err := reconnecting.VisitorReconnect(fixture.visitorA, JoinQualification{intent: reconnectIntent, purpose: qualificationPurposeJoin}, newBinding, fixture.assignment, fixture.createdAt.Add(20*time.Second)); !IsErrorCode(err, ErrorCodeStale) {
		t.Fatalf("join qualification was accepted by reconnect: %v", err)
	}
	recovered, member, err := reconnecting.VisitorReconnect(fixture.visitorA, JoinQualification{intent: reconnectIntent, purpose: qualificationPurposeReconnect}, newBinding, fixture.assignment, fixture.createdAt.Add(20*time.Second))
	if err != nil || member.BindingID() != newBinding {
		t.Fatalf("reconnect visitor: member=%#v err=%v", member, err)
	}
	if _, err := recovered.VisitorDisconnect(fixture.visitorA, originalBinding, fixture.createdAt.Add(time.Minute), fixture.createdAt.Add(30*time.Second)); !IsErrorCode(err, ErrorCodeStale) {
		t.Fatalf("old disconnect should be stale: %v", err)
	}
	if _, _, err := recovered.Leave(fixture.visitorA, originalBinding); !IsErrorCode(err, ErrorCodeStale) {
		t.Fatalf("old leave should be stale: %v", err)
	}
	left, directive, err := recovered.Leave(fixture.visitorA, newBinding)
	if err != nil || directive.Reason() != SafeReturnReasonVoluntaryLeave || directive.Revision() != left.Revision() || left.Snapshot().ActiveMemberCount() != 0 {
		t.Fatalf("leave visitor: directive=%#v err=%v", directive, err)
	}

	joined, _ = joinedFixture(t, fixture, fixture.visitorA, "vinv_kick", "vbind_kick")
	kicked, directive, err := joined.Kick(fixture.owner, fixture.visitorA.playerID)
	if err != nil || directive.Reason() != SafeReturnReasonKicked || directive.Revision() != kicked.Revision() || kicked.Snapshot().ActiveMemberCount() != 0 {
		t.Fatalf("kick visitor: directive=%#v err=%v", directive, err)
	}
	if _, _, err := joined.Kick(fixture.visitorB, fixture.visitorA.playerID); !IsErrorCode(err, ErrorCodeForbidden) {
		t.Fatalf("visitor kick should fail: %v", err)
	}
}

// TestVisitorReconnectExpiryMatchesGeneration 验证旧 timer 不能移除新 binding 或其他 membership。
func TestVisitorReconnectExpiryMatchesGeneration(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 1)
	joined, binding := joinedFixture(t, fixture, fixture.visitorA, "vinv_timer", "vbind_timer")
	deadline := fixture.createdAt.Add(40 * time.Second)
	reconnecting, err := joined.VisitorDisconnect(fixture.visitorA, binding, deadline, fixture.createdAt.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	member := reconnecting.Snapshot().Memberships()[0]
	if _, _, err := reconnecting.ExpireVisitorReconnect(fixture.visitorA.playerID, member.ReconnectGeneration()+1, binding, deadline, deadline); !IsErrorCode(err, ErrorCodeStale) {
		t.Fatalf("wrong generation should be stale: %v", err)
	}
	if _, _, err := reconnecting.ExpireVisitorReconnect(fixture.visitorA.playerID, member.ReconnectGeneration(), binding, deadline, deadline.Add(-time.Microsecond)); !IsErrorCode(err, ErrorCodeInvalidState) {
		t.Fatalf("early timer should fail: %v", err)
	}
	expired, directive, err := reconnecting.ExpireVisitorReconnect(fixture.visitorA.playerID, member.ReconnectGeneration(), binding, deadline, deadline)
	if err != nil || directive.Reason() != SafeReturnReasonVisitorReconnectExpired || directive.Revision() != expired.Revision() || expired.Snapshot().ActiveMemberCount() != 0 {
		t.Fatalf("expire reconnect: directive=%#v err=%v", directive, err)
	}
}

// TestOwnerGraceReconnectAndClose 验证 grace 期间暂停新资格、Owner 不转移与旧 timer stale。
func TestOwnerGraceReconnectAndClose(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 1)
	joined, _ := joinedFixture(t, fixture, fixture.visitorA, "vinv_ownergrace", "vbind_visitor")
	deadline := fixture.createdAt.Add(50 * time.Second)
	grace, err := joined.OwnerDisconnect(fixture.owner, fixture.ownerBinding.connectionID, deadline, fixture.createdAt.Add(10*time.Second))
	if err != nil {
		t.Fatalf("owner disconnect: %v", err)
	}
	if _, _, err := grace.CreateInvite(fixture.owner, mustInviteID(t, "vinv_paused"), fixture.visitorB.playerID, deadline, fixture.createdAt.Add(20*time.Second)); !IsErrorCode(err, ErrorCodeInvalidState) {
		t.Fatalf("invite during grace should fail: %v", err)
	}
	if _, _, err := grace.AcceptInvite(fixture.visitorB, mustInviteID(t, "vinv_missing"), deadline, fixture.assignment, fixture.createdAt.Add(20*time.Second)); !IsErrorCode(err, ErrorCodeInvalidState) {
		t.Fatalf("accept during grace should fail: %v", err)
	}
	if _, _, err := grace.Close(fixture.visitorA); !IsErrorCode(err, ErrorCodeForbidden) {
		t.Fatalf("visitor must not succeed Owner: %v", err)
	}
	newOwner := testActor(t, fixture.owner.playerID.String(), "ses_ownerNew", 2)
	recovered, err := grace.OwnerReconnect(newOwner, mustBindingID(t, "vbind_ownerNew"), fixture.assignment, deadline.Add(-time.Microsecond))
	if err != nil || recovered.Lifecycle() != LifecycleOpen {
		t.Fatalf("owner reconnect: snapshot=%#v err=%v", recovered.Snapshot(), err)
	}
	graceSnapshot := grace.Snapshot()
	if _, _, err := recovered.ExpireOwnerGrace(graceSnapshot.OwnerGraceGeneration(), graceSnapshot.OwnerBinding().ConnectionID(), graceSnapshot.OwnerGraceExpiresAt(), deadline); !IsErrorCode(err, ErrorCodeStale) {
		t.Fatalf("old owner timer should be stale: %v", err)
	}
}

// TestBatchCloseReasonsAndStableOrder 验证 terminal transition 只推进一次 revision并稳定返回全部 joined Visitor。
func TestBatchCloseReasonsAndStableOrder(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 2)
	visit := openFixture(t, fixture)
	visit = addJoined(t, visit, fixture, fixture.visitorB, "vinv_closeB", "vbind_closeB", fixture.createdAt)
	visit = addJoined(t, visit, fixture, fixture.visitorA, "vinv_closeA", "vbind_closeA", fixture.createdAt.Add(3*time.Second))
	before := visit.Revision()
	closed, directives, err := visit.InvalidateAssignment()
	if err != nil {
		t.Fatalf("invalidate assignment: %v", err)
	}
	if closed.Lifecycle() != LifecycleClosed || closed.Revision() != before+1 || len(closed.Snapshot().Invites()) != 0 || len(closed.Snapshot().Memberships()) != 0 {
		t.Fatalf("invalid closed snapshot: %#v", closed.Snapshot())
	}
	if closed.RoleOf(fixture.owner) != RoleUnspecified || closed.RoleOf(fixture.visitorA) != RoleUnspecified {
		t.Fatal("terminal session retained an active role projection")
	}
	if len(directives) != 2 || directives[0].VisitorID().String() != fixture.visitorA.playerID.String() || directives[0].Reason() != SafeReturnReasonAssignmentChanged || directives[0].Revision() != closed.Revision() || directives[1].Reason() != SafeReturnReasonAssignmentChanged || directives[1].Revision() != closed.Revision() {
		t.Fatalf("unstable directives: %#v", directives)
	}
}

// TestOwnerAndSessionExpiryAtEquality 验证 Owner grace 与 session absolute deadline 等号即关闭。
func TestOwnerAndSessionExpiryAtEquality(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 1)
	joined, _ := joinedFixture(t, fixture, fixture.visitorA, "vinv_expiry", "vbind_expiry")
	graceDeadline := fixture.createdAt.Add(40 * time.Second)
	grace, err := joined.OwnerDisconnect(fixture.owner, fixture.ownerBinding.connectionID, graceDeadline, fixture.createdAt.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := grace.Snapshot()
	closed, directives, err := grace.ExpireOwnerGrace(snapshot.OwnerGraceGeneration(), snapshot.OwnerBinding().ConnectionID(), snapshot.OwnerGraceExpiresAt(), graceDeadline)
	if err != nil || closed.Lifecycle() != LifecycleClosed || len(directives) != 1 || directives[0].Reason() != SafeReturnReasonOwnerUnavailable {
		t.Fatalf("expire owner grace: directives=%#v err=%v", directives, err)
	}

	joined, _ = joinedFixture(t, fixture, fixture.visitorA, "vinv_sessionexpiry", "vbind_sessionexpiry")
	sessionDeadline := joined.Snapshot().ExpiresAt()
	closed, directives, err = joined.ExpireSession(sessionDeadline, sessionDeadline)
	if err != nil || closed.Lifecycle() != LifecycleClosed || len(directives) != 1 || directives[0].Reason() != SafeReturnReasonSessionExpired {
		t.Fatalf("expire session: directives=%#v err=%v", directives, err)
	}
}

// TestDependencyLossClose 验证不可安全恢复运行态时返回 dependency-lost 而不迁移旧资格。
func TestDependencyLossClose(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 1)
	joined, _ := joinedFixture(t, fixture, fixture.visitorA, "vinv_dependency", "vbind_dependency")
	closed, directives, err := joined.LoseDependency()
	if err != nil || closed.Lifecycle() != LifecycleClosed || len(directives) != 1 || directives[0].Reason() != SafeReturnReasonDependencyLost {
		t.Fatalf("dependency loss: directives=%#v err=%v", directives, err)
	}
}

// TestAssignmentAndEpochChangesFailClosed 验证 replacement assignment 与旧 session epoch 不能恢复 membership。
func TestAssignmentAndEpochChangesFailClosed(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 1)
	visit, inviteID := invitedFixture(t, fixture, fixture.visitorA, "vinv_stale")
	deadline := fixture.createdAt.Add(20 * time.Second)
	otherAssignment := replacementAssignment(t, fixture, 2)
	if _, _, err := visit.AcceptInvite(fixture.visitorA, inviteID, deadline, otherAssignment, fixture.createdAt.Add(time.Second)); !IsErrorCode(err, ErrorCodeStale) {
		t.Fatalf("replacement assignment should fail: %v", err)
	}
	reserved, intent, err := visit.AcceptInvite(fixture.visitorA, inviteID, deadline, fixture.assignment, fixture.createdAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	newEpoch := fixture.visitorA
	newEpoch.epoch++
	if _, _, err := reserved.Join(newEpoch, JoinQualification{intent: intent, purpose: qualificationPurposeJoin}, mustBindingID(t, "vbind_epoch"), fixture.assignment, fixture.createdAt.Add(2*time.Second)); !IsErrorCode(err, ErrorCodeStale) {
		t.Fatalf("new epoch must reject old qualification: %v", err)
	}
}

// openFixture 创建共享 policy 下的初始 aggregate。
func openFixture(t *testing.T, fixture aggregateFixture) VisitSession {
	t.Helper()
	visit, err := NewVisitSession(fixture.visitID, fixture.ownerBinding, fixture.worldID, fixture.assignment, fixture.policy, fixture.createdAt)
	if err != nil {
		t.Fatal(err)
	}
	return visit
}

// invitedFixture 创建一个 pending invite，并返回其 aggregate 与 identity。
func invitedFixture(t *testing.T, fixture aggregateFixture, visitor Actor, inviteValue string) (VisitSession, InviteID) {
	t.Helper()
	visit := openFixture(t, fixture)
	inviteID := mustInviteID(t, inviteValue)
	visit, _, err := visit.CreateInvite(fixture.owner, inviteID, visitor.playerID, fixture.createdAt.Add(time.Minute), fixture.createdAt)
	if err != nil {
		t.Fatal(err)
	}
	return visit, inviteID
}

// joinedFixture 完成 invite、accept 与 trusted join 的正常路径。
func joinedFixture(t *testing.T, fixture aggregateFixture, visitor Actor, inviteValue, bindingValue string) (VisitSession, ConnectionBindingID) {
	t.Helper()
	visit, inviteID := invitedFixture(t, fixture, visitor, inviteValue)
	deadline := fixture.createdAt.Add(20 * time.Second)
	reserved, intent, err := visit.AcceptInvite(visitor, inviteID, deadline, fixture.assignment, fixture.createdAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	binding := mustBindingID(t, bindingValue)
	joined, _, err := reserved.Join(visitor, JoinQualification{intent: intent, purpose: qualificationPurposeJoin}, binding, fixture.assignment, fixture.createdAt.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return joined, binding
}

// addJoined 在既有 aggregate 中增加一个 joined Visitor，便于验证批量 close。
func addJoined(t *testing.T, visit VisitSession, fixture aggregateFixture, visitor Actor, inviteValue, bindingValue string, observedAt time.Time) VisitSession {
	t.Helper()
	inviteID := mustInviteID(t, inviteValue)
	invited, _, err := visit.CreateInvite(fixture.owner, inviteID, visitor.playerID, observedAt.Add(time.Minute), observedAt)
	if err != nil {
		t.Fatal(err)
	}
	deadline := observedAt.Add(20 * time.Second)
	reserved, intent, err := invited.AcceptInvite(visitor, inviteID, deadline, fixture.assignment, observedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	joined, _, err := reserved.Join(visitor, JoinQualification{intent: intent, purpose: qualificationPurposeJoin}, mustBindingID(t, bindingValue), fixture.assignment, observedAt.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return joined
}

// replacementAssignment 创建 stamp 已变化但仍 current active 的可信 placement snapshot。
func replacementAssignment(t *testing.T, fixture aggregateFixture, generationValue uint64) placement.AssignmentSnapshot {
	t.Helper()
	instance, _ := placement.NewWorldInstanceID("winst_replacement")
	node, _ := placement.NewRuntimeNodeID("rnode_replacement")
	generation, _ := placement.NewAssignmentGeneration(generationValue)
	fence, _ := placement.NewFencingToken(generationValue)
	stamp, _ := placement.NewAssignmentStamp(fixture.worldID, instance, node, generation, fence)
	snapshot, err := placement.NewAssignmentSnapshot(stamp, placement.PhaseActive, fixture.createdAt, fixture.createdAt.Add(time.Hour), fixture.createdAt)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
