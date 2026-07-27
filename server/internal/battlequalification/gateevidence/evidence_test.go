package gateevidence

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEvidenceValidateClosedKinds 验证三类 evidence 的 typed projection 不能互相冒充。
func TestEvidenceValidateClosedKinds(t *testing.T) {
	digestA := strings.Repeat("a", 64)
	digestB := strings.Repeat("b", 64)
	tests := []Evidence{
		{
			SchemaVersion: 1, QualificationVersion: QualificationVersion,
			EvidenceKind: SecurityAvailabilityKind, ScenarioID: "security-aad-tamper",
			WorkloadID: "default-coop", Assertions: []string{"attack-rejected"},
			Security: &SecurityResult{
				AttackDatagrams: 1, RejectedDatagrams: 1,
				LegitimateActorCount: 5, AvailabilityPassed: true,
			},
			Disposition: PassedDisposition,
		},
		{
			SchemaVersion: 1, QualificationVersion: QualificationVersion,
			EvidenceKind: LifecycleKind, ScenarioID: "lifecycle-assignment-replacement",
			WorkloadID: "default-coop", Assertions: []string{"predecessor-rejected"},
			Transition: &BindingTransition{
				Mode: "replaced", PredecessorSHA256: digestA,
				SuccessorSHA256: digestB, PredecessorRejected: true,
			},
			Disposition: PassedDisposition,
		},
		{
			SchemaVersion: 1, QualificationVersion: QualificationVersion,
			EvidenceKind: RegressionKind, ScenarioID: "regression-source-drift",
			WorkloadID: "qualification-corpus", Assertions: []string{"drift-rejected"},
			Disposition: PassedDisposition,
		},
	}
	for _, evidence := range tests {
		if err := evidence.Validate(); err != nil {
			t.Fatalf("%s validate: %v", evidence.EvidenceKind, err)
		}
	}
}

// TestNewLifecycleHashesProjectionAndSortsAssertions 验证原始 identity 不会进入证据。
func TestNewLifecycleHashesProjectionAndSortsAssertions(t *testing.T) {
	predecessor := []byte("run-local-predecessor")
	successor := []byte("run-local-successor")
	evidence, err := NewLifecycle(LifecycleInput{
		ScenarioID: "lifecycle-go-restart",
		WorkloadID: "default-coop",
		Assertions: []string{
			"successor-current",
			"predecessor-rejected",
		},
		Mode:                  TransitionReplaced,
		PredecessorProjection: predecessor,
		SuccessorProjection:   successor,
		PredecessorRejected:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), string(predecessor)) ||
		strings.Contains(string(encoded), string(successor)) ||
		evidence.Assertions[0] != "predecessor-rejected" {
		t.Fatalf("lifecycle evidence leaked or drifted: %s", encoded)
	}
}

// TestNewLifecycleRejectsIncompleteProjection 验证构造器不接受空 predecessor 或伪造终止 successor。
func TestNewLifecycleRejectsIncompleteProjection(t *testing.T) {
	if _, err := NewLifecycle(LifecycleInput{
		ScenarioID:          "lifecycle-go-restart",
		WorkloadID:          "default-coop",
		Assertions:          []string{"predecessor-rejected"},
		Mode:                TransitionReplaced,
		PredecessorRejected: true,
	}); err == nil {
		t.Fatal("empty lifecycle projections were accepted")
	}
	if _, err := NewLifecycle(LifecycleInput{
		ScenarioID:            "lifecycle-shutdown-drain-deadline",
		WorkloadID:            "default-coop",
		Assertions:            []string{"predecessor-rejected"},
		Mode:                  TransitionTerminated,
		PredecessorProjection: []byte("predecessor"),
		SuccessorProjection:   []byte("forged-successor"),
		PredecessorRejected:   true,
	}); err == nil {
		t.Fatal("terminated lifecycle accepted successor projection")
	}
}

// TestEvidenceValidateRejectsForgedLifecycle 验证 predecessor 未拒绝或同代替换不能通过。
func TestEvidenceValidateRejectsForgedLifecycle(t *testing.T) {
	digest := strings.Repeat("a", 64)
	evidence := Evidence{
		SchemaVersion: 1, QualificationVersion: QualificationVersion,
		EvidenceKind: LifecycleKind, ScenarioID: "lifecycle-go-restart",
		WorkloadID: "default-coop", Assertions: []string{"predecessor-rejected"},
		Transition: &BindingTransition{
			Mode: "replaced", PredecessorSHA256: digest,
			SuccessorSHA256: digest, PredecessorRejected: true,
		},
		Disposition: PassedDisposition,
	}
	if err := evidence.Validate(); err == nil {
		t.Fatal("same-generation replacement was accepted")
	}
}
