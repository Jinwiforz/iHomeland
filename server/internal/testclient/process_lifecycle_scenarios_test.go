package testclient

import (
	"bytes"
	"testing"
)

// TestRecordAssignmentProjectionBindsIncarnation 验证投影同时绑定 world、instance 与 generation。
func TestRecordAssignmentProjectionBindsIncarnation(t *testing.T) {
	before := &LifecycleRecorder{}
	after := &LifecycleRecorder{}
	first := WorldBootstrapResponse{
		World: PersonalWorldSummary{PersonalWorldID: "world"},
		Assignment: &WorldAssignment{
			WorldInstanceID: "instance-before",
			Generation:      1,
		},
	}
	second := WorldBootstrapResponse{
		World: PersonalWorldSummary{PersonalWorldID: "world"},
		Assignment: &WorldAssignment{
			WorldInstanceID: "instance-after",
			Generation:      2,
		},
	}
	if err := recordAssignmentProjection(before.recordPredecessor, first); err != nil {
		t.Fatal(err)
	}
	if err := recordAssignmentProjection(after.recordPredecessor, second); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before.predecessor, after.predecessor) {
		t.Fatal("replacement assignment produced identical lifecycle projection")
	}
}

// TestRecordAssignmentProjectionRejectsMissingAssignment 验证缺失 successor fail closed。
func TestRecordAssignmentProjectionRejectsMissingAssignment(t *testing.T) {
	recorder := &LifecycleRecorder{}
	if err := recordAssignmentProjection(
		recorder.recordPredecessor,
		WorldBootstrapResponse{},
	); err == nil {
		t.Fatal("missing assignment lifecycle projection was accepted")
	}
}
