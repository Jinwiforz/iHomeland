package testclient

import (
	"bytes"
	"testing"
)

// TestLifecycleRecorderReturnsOwnedProjection 验证规范化投影、单次提交和 cleanup。
func TestLifecycleRecorderReturnsOwnedProjection(t *testing.T) {
	recorder := &LifecycleRecorder{}
	if err := recorder.recordPredecessor("session", "old", "1"); err != nil {
		t.Fatal(err)
	}
	if err := recorder.recordSuccessor("session", "new", "2"); err != nil {
		t.Fatal(err)
	}
	observation, err := recorder.Observation()
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.PredecessorProjection) == 0 ||
		len(observation.SuccessorProjection) == 0 ||
		bytes.Equal(
			observation.PredecessorProjection,
			observation.SuccessorProjection,
		) ||
		!observation.PredecessorRejected || observation.Terminated {
		t.Fatalf("lifecycle observation drifted: %+v", observation)
	}
	observation.Clear()
	if observation.PredecessorProjection != nil ||
		observation.SuccessorProjection != nil {
		t.Fatal("lifecycle observation was not cleared")
	}
	if err := recorder.recordTermination(); err == nil {
		t.Fatal("successor observation accepted conflicting termination")
	}
}

// TestLifecycleRecorderRejectsIncompleteObservation 验证缺失 predecessor/rejection fail closed。
func TestLifecycleRecorderRejectsIncompleteObservation(t *testing.T) {
	recorder := &LifecycleRecorder{}
	if _, err := recorder.Observation(); err == nil {
		t.Fatal("empty lifecycle observation was accepted")
	}
	if err := recorder.recordPredecessor("session", "only"); err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Observation(); err == nil {
		t.Fatal("predecessor-only lifecycle observation was accepted")
	}
}
