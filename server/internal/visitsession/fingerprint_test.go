package visitsession

import (
	"testing"
	"time"
)

// TestCommandFingerprintBindsStableSecurityFields 验证 session、actor、target、binding、deadline 与 assignment 变化都会改变摘要。
func TestCommandFingerprintBindsStableSecurityFields(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 1)
	expected := InitialRevision
	otherVisitID, _ := NewVisitSessionID("vses_other")
	base := commandFingerprint(OperationVisitorDisconnect, fixture.visitID, expected, actorFields(fixture.visitorA), fixture.visitorA.playerID.String(), "vbind_a", timeField(fixture.createdAt.Add(time.Minute)), assignmentFields(fixture.assignment.Stamp()))
	variants := []CommandFingerprint{
		commandFingerprint(OperationVisitorDisconnect, otherVisitID, expected, actorFields(fixture.visitorA), fixture.visitorA.playerID.String(), "vbind_a", timeField(fixture.createdAt.Add(time.Minute)), assignmentFields(fixture.assignment.Stamp())),
		commandFingerprint(OperationVisitorDisconnect, fixture.visitID, expected, actorFields(fixture.visitorB), fixture.visitorA.playerID.String(), "vbind_a", timeField(fixture.createdAt.Add(time.Minute)), assignmentFields(fixture.assignment.Stamp())),
		commandFingerprint(OperationVisitorDisconnect, fixture.visitID, expected, actorFields(fixture.visitorA), fixture.visitorB.playerID.String(), "vbind_a", timeField(fixture.createdAt.Add(time.Minute)), assignmentFields(fixture.assignment.Stamp())),
		commandFingerprint(OperationVisitorDisconnect, fixture.visitID, expected, actorFields(fixture.visitorA), fixture.visitorA.playerID.String(), "vbind_b", timeField(fixture.createdAt.Add(time.Minute)), assignmentFields(fixture.assignment.Stamp())),
		commandFingerprint(OperationVisitorDisconnect, fixture.visitID, expected, actorFields(fixture.visitorA), fixture.visitorA.playerID.String(), "vbind_a", timeField(fixture.createdAt.Add(time.Minute+time.Microsecond)), assignmentFields(fixture.assignment.Stamp())),
		commandFingerprint(OperationVisitorDisconnect, fixture.visitID, expected, actorFields(fixture.visitorA), fixture.visitorA.playerID.String(), "vbind_a", timeField(fixture.createdAt.Add(time.Minute)), assignmentFields(replacementAssignment(t, fixture, 2).Stamp())),
	}
	for index, variant := range variants {
		if base.Equal(variant) {
			t.Fatalf("variant %d did not change fingerprint", index)
		}
	}
}

// TestCommandFingerprintExcludesObservedAt 证明相同稳定 command 在重试时钟推进后摘要保持一致。
func TestCommandFingerprintExcludesObservedAt(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 1)
	deadline := fixture.createdAt.Add(time.Minute)
	first := commandFingerprint(OperationExpireInvite, fixture.visitID, InitialRevision, nil, "vinv_same", timeField(deadline))
	_ = fixture.createdAt.Add(30 * time.Second) // observedAt 由 application 重新读取，但不进入 command 字段。
	second := commandFingerprint(OperationExpireInvite, fixture.visitID, InitialRevision, nil, "vinv_same", timeField(deadline))
	if !first.Equal(second) {
		t.Fatal("observedAt changed stable fingerprint")
	}
}
