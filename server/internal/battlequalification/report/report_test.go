package report

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/gateevidence"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/measurement"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/runner"
)

// TestReduceEvidenceMetricUsesExactSources 验证 bandwidth、KCP、Tick 与 memory 公式。
func TestReduceEvidenceMetricUsesExactSources(t *testing.T) {
	evidence := runner.Evidence{
		ActorCount: 2,
		Gateway: runner.GatewayEvidence{
			MaximumDeliveryAgeUS: 75,
			Packets: []runner.PacketEvidence{
				{Direction: "uplink", Disposition: "delivered", Bytes: 2_000},
				{Direction: "downlink", Disposition: "delivered", Bytes: 8_000},
				{Direction: "downlink", Disposition: "loss", Bytes: 100_000},
			},
		},
		Clients: []runner.ClientEvidence{
			{MaximumSnapshotGapUS: 70, MaximumBaselineRecoveryUS: 90},
			{MaximumSnapshotGapUS: 80, MaximumBaselineRecoveryUS: 60},
		},
		Metrics: measurement.Window{
			Start: measurement.Snapshot{
				Control: map[string]uint64{
					"kcp-egress-packets": 10,
					"kcp-retransmits":    2,
				},
				Processes: map[string]map[string]uint64{
					"go-parent": {"monotonic-time-us": 1, "working-set-bytes": 100},
					"cpp-child": {"monotonic-time-us": 1, "working-set-bytes": 200},
				},
			},
			End: measurement.Snapshot{
				Control: map[string]uint64{
					"kcp-egress-packets":           22,
					"kcp-retransmits":              4,
					"maximum-tick-duration-ns":     2_501,
					"instance-memory-bytes":        300,
					"history-memory-bytes":         40,
					"ingress-queue-high-watermark": 5,
					"egress-queue-high-watermark":  6,
					"kcp-queue-high-watermark":     7,
					"tick-debt-high-watermark":     3,
				},
				Processes: map[string]map[string]uint64{
					"go-parent": {"monotonic-time-us": 1_000_001, "working-set-bytes": 160},
					"cpp-child": {"monotonic-time-us": 1_000_001, "working-set-bytes": 250},
				},
			},
		},
	}
	tests := map[string]uint64{
		"delivered-uplink-gateway-bytes-divided-by-window-and-actors":   1_000,
		"delivered-downlink-gateway-bytes-divided-by-window-and-actors": 4_000,
		"delivered-gateway-bytes-divided-by-window":                     8_000,
		"maximum-gateway-delivery-age":                                  75,
		"maximum-resync-to-successor-baseline":                          90,
		"kcp-egress-xmit-divided-by-first-transmissions":                12_000,
		"maximum-control-ingress-queue-high-watermark":                  5,
		"maximum-control-egress-queue-high-watermark":                   6,
		"maximum-control-kcp-queue-high-watermark":                      7,
		"maximum-tick-duration-ns-to-us-ceiling":                        3,
		"maximum-control-tick-debt-high-watermark":                      3,
		"maximum-control-accounted-instance-bytes":                      300,
		"maximum-control-accounted-history-bytes":                       40,
		"maximum-end-minus-start-positive":                              60,
	}
	for method, want := range tests {
		got, err := reduceEvidenceMetric(evidence, method)
		if err != nil || got != want {
			t.Fatalf("%s=%d want=%d err=%v", method, got, want, err)
		}
	}
}

// TestWithinToleranceUsesWorstCaseDenominator 验证复现阈值 inclusive 且不受整数截断影响。
func TestWithinToleranceUsesWorstCaseDenominator(t *testing.T) {
	if !withinTolerance(900, 1_000, 1_000) {
		t.Fatal("inclusive ten percent tolerance was rejected")
	}
	if withinTolerance(899, 1_000, 1_000) {
		t.Fatal("out-of-tolerance values were accepted")
	}
	if !withinTolerance(0, 0, 0) {
		t.Fatal("two exact zero values drifted")
	}
}

// TestFinalizeIncompleteInputProducesNotQualified 验证缺失证据是可编码结论而非工具错误。
func TestFinalizeIncompleteInputProducesNotQualified(t *testing.T) {
	report, err := Finalize(Input{})
	if err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if report.Qualification != notQualifiedConclusion ||
		report.Identity != nil ||
		len(report.Completeness.Missing) != 1 ||
		report.Completeness.Missing[0] != "repository-root" {
		t.Fatalf("unexpected incomplete report: %+v", report)
	}
	raw, err := MarshalCanonical(report)
	if err != nil {
		t.Fatalf("MarshalCanonical() error = %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if _, exists := document["identity"]; exists {
		t.Fatal("not-qualified report forged an empty identity")
	}
}

// TestCheckedArithmeticRejectsEvidenceOverflow 验证 rate、counter 与 cleanup 不发生静默回绕。
func TestCheckedArithmeticRejectsEvidenceOverflow(t *testing.T) {
	if _, err := checkedAdd(math.MaxUint64, 1); err == nil {
		t.Fatal("counter addition overflow was accepted")
	}
	if _, err := checkedMultiply(math.MaxUint64, 2); err == nil {
		t.Fatal("measurement denominator overflow was accepted")
	}
	if _, err := checkedDelta(2, 1); err == nil {
		t.Fatal("monotonic counter regression was accepted")
	}
	cleanup := combineCleanup(
		CleanupEvidence{Disposition: "passed", RemainingProcesses: math.MaxUint64},
		CleanupEvidence{Disposition: "passed", RemainingProcesses: 1},
	)
	if cleanup.Disposition != "failed" {
		t.Fatal("cleanup overflow did not fail closed")
	}
}

// TestLoadReferenceVerifiesGateEvidenceDigest 验证非 scenario evidence 也必须 closed decode 与复核摘要。
func TestLoadReferenceVerifiesGateEvidenceDigest(t *testing.T) {
	directory := t.TempDir()
	evidence := gateevidence.Evidence{
		SchemaVersion:        1,
		QualificationVersion: qualificationVersion,
		EvidenceKind:         gateevidence.SecurityAvailabilityKind,
		ScenarioID:           "security-aad-tamper",
		WorkloadID:           "default-coop",
		Assertions:           []string{"attack-rejected", "legitimate-flow-available"},
		Security: &gateevidence.SecurityResult{
			AttackDatagrams: 1, RejectedDatagrams: 1,
			LegitimateActorCount: 5, AvailabilityPassed: true,
		},
		Disposition: "passed",
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatalf("marshal evidence: %v", err)
	}
	path := filepath.Join(directory, evidence.ScenarioID+".json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write evidence: %v", err)
	}
	reference := ScenarioReference{
		ScenarioID: evidence.ScenarioID, WorkloadID: evidence.WorkloadID,
		EvidenceKind: evidence.EvidenceKind, SHA256: sha256Hex(raw),
		Disposition: "passed",
	}
	if _, err := loadReference(directory, reference); err != nil {
		t.Fatalf("loadReference() error = %v", err)
	}
	reference.SHA256 = sha256Hex([]byte("different"))
	if _, err := loadReference(directory, reference); err == nil {
		t.Fatal("gate evidence digest mismatch was accepted")
	}
}

// TestLoadRunRejectsFinalizeEvidenceForgery 验证 diagnose 混入、cleanup 伪造与 secret 字段均 fail closed。
func TestLoadRunRejectsFinalizeEvidenceForgery(t *testing.T) {
	validIdentity := testIdentity("a")
	tests := []struct {
		name        string
		runKind     string
		cleanup     CleanupEvidence
		disposition string
		mutateGate  func(map[string]any)
	}{
		{
			name: "diagnostic-mixed-as-verify", runKind: "diagnostic",
			cleanup: CleanupEvidence{Disposition: "passed"},
		},
		{
			name: "cleanup-forged-pass", runKind: "verify",
			cleanup: CleanupEvidence{Disposition: "passed", RemainingListeners: 1},
		},
		{
			name: "secret-injected", runKind: "verify",
			cleanup: CleanupEvidence{Disposition: "passed"},
			mutateGate: func(gate map[string]any) {
				gate["ticketSecret"] = "must-not-be-accepted"
			},
		},
		{
			name: "unsupported-disguised-as-pass", runKind: "verify",
			cleanup:     CleanupEvidence{Disposition: "passed"},
			disposition: "unsupported",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			scenarioID := "security-aad-tamper"
			gate := map[string]any{
				"schemaVersion": 1, "qualificationVersion": qualificationVersion,
				"evidenceKind": "security-availability-evidence",
				"scenarioId":   scenarioID, "workloadId": "default-coop",
				"assertions": []string{"attack-rejected"}, "disposition": "passed",
				"security": map[string]any{
					"attackDatagrams": 1, "rejectedDatagrams": 1,
					"responseDatagrams": 0, "legitimateActorCount": 5,
					"availabilityPassed": true,
				},
			}
			if test.mutateGate != nil {
				test.mutateGate(gate)
			}
			raw, err := json.Marshal(gate)
			if err != nil {
				t.Fatalf("marshal gate: %v", err)
			}
			if err := os.WriteFile(
				filepath.Join(directory, scenarioID+".json"),
				raw,
				0o600,
			); err != nil {
				t.Fatalf("write gate: %v", err)
			}
			disposition := test.disposition
			if disposition == "" {
				disposition = "passed"
			}
			index := RunIndex{
				SchemaVersion: 1, QualificationVersion: qualificationVersion,
				RunKind: test.runKind, RunID: filepath.Base(directory),
				Identity: &validIdentity,
				Scenarios: []ScenarioReference{{
					ScenarioID: scenarioID, WorkloadID: "default-coop",
					EvidenceKind: "security-availability-evidence",
					SHA256:       sha256Hex(raw), Disposition: disposition,
				}},
				Cleanup: test.cleanup,
			}
			indexRaw, err := json.Marshal(index)
			if err != nil {
				t.Fatalf("marshal index: %v", err)
			}
			if err := os.WriteFile(
				filepath.Join(directory, "run.json"),
				indexRaw,
				0o600,
			); err != nil {
				t.Fatalf("write index: %v", err)
			}
			if _, err := loadRun(directory, "verify"); err == nil {
				t.Fatal("forged finalizer input was accepted")
			}
		})
	}
}

// TestRunsShareFrozenIdentityRejectsReuseAndDrift 验证连续 run 不能复用目录或漂移 source。
func TestRunsShareFrozenIdentityRejectsReuseAndDrift(t *testing.T) {
	identity := testIdentity("a")
	runA := loadedRun{index: RunIndex{RunID: "run-a", Identity: &identity}}
	runB := loadedRun{index: RunIndex{RunID: "run-b", Identity: &identity}}
	soak := loadedRun{index: RunIndex{RunID: "soak", Identity: &identity}}
	if !runsShareFrozenIdentity(runA, runB, soak) {
		t.Fatal("three independent frozen identities were rejected")
	}
	runB.index.RunID = runA.index.RunID
	if runsShareFrozenIdentity(runA, runB, soak) {
		t.Fatal("old run reuse was accepted")
	}
	runB.index.RunID = "run-b"
	drifted := testIdentity("b")
	runB.index.Identity = &drifted
	if runsShareFrozenIdentity(runA, runB, soak) {
		t.Fatal("source or binary identity drift was accepted")
	}
}

// testIdentity 返回所有字段均为合法且可按填充字符制造漂移的测试 identity。
func testIdentity(fill string) Identity {
	digest := strings.Repeat(fill, 64)
	return Identity{
		SourceSHA256: digest, BinarySHA256: digest, ToolchainSHA256: digest,
		DependencySHA256: digest, ModelSHA256: digest, ProfileSHA256: digest,
		ControlSHA256: digest, WireSHA256: digest, ConfigSHA256: digest,
		EnvironmentSHA256: digest, FaultSHA256: digest, WorkloadSHA256: digest,
	}
}
