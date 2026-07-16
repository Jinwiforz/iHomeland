package testclient

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestQualificationReportIsLowSensitivity 验证报告只含稳定字段且拒绝非法 outcome。
func TestQualificationReportIsLowSensitivity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	report := NewQualificationReport("run-qualification", 1, strings.Repeat("a", 64), time.Unix(1, 0))
	report.Gates = []GateReport{{ID: "contract", Outcome: "pass", DurationMS: 1}}
	report.Scenarios = []ScenarioReport{{ID: "contract-freeze", Phase: "contract", Evidence: "contract", Execution: "contract", Mandatory: true, Outcome: "pass", DurationMS: 1}}
	if err := WriteQualificationReport(path, report); err != nil {
		t.Fatalf("write report: %v", err)
	}
	report.Qualified = true
	if err := WriteQualificationReport(path, report); err == nil {
		t.Fatal("incomplete qualified report was accepted")
	}
	report.Qualified = false
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	for _, forbidden := range []string{"password", "credential", "127.0.0.1", repositoryRoot(t)} {
		if strings.Contains(string(content), forbidden) {
			t.Fatalf("report leaked forbidden text %q", forbidden)
		}
	}
	report.Scenarios[0].Outcome = "maybe"
	if err := WriteQualificationReport(path, report); err == nil {
		t.Fatal("invalid report outcome was accepted")
	}
	report.Scenarios[0].Outcome = "pass"
	report.Scenarios[0].Execution = "black_box"
	if err := WriteQualificationReport(path, report); err == nil {
		t.Fatal("invalid report execution was accepted")
	}
	report.Scenarios[0].Execution = "contract"
	report.Scenarios = append(report.Scenarios, report.Scenarios[0])
	if err := WriteQualificationReport(path, report); err == nil {
		t.Fatal("duplicate report scenario was accepted")
	}
	report.Scenarios = report.Scenarios[:1]
	report.Gates[0].ID = "../escape"
	if err := WriteQualificationReport(path, report); err == nil {
		t.Fatal("invalid gate identity was accepted")
	}
}
