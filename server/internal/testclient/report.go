package testclient

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ReportSchemaVersion 是资格机器报告的当前格式代际。
const ReportSchemaVersion = 1

// QualificationReport 是一次完整运行的低敏机器证据。
type QualificationReport struct {
	// SchemaVersion 选择报告 JSON 结构代际。
	SchemaVersion int `json:"schemaVersion"`
	// QualificationVersion 是 manifest 声明的发布资格代际。
	QualificationVersion string `json:"qualificationVersion"`
	// RunID 是本地资源 owner 使用的随机低敏 identity。
	RunID string `json:"runId"`
	// ProtocolVersion 是 GET /v1/version 返回的公开协议代际。
	ProtocolVersion uint32 `json:"protocolVersion"`
	// ContractDigest 是冻结输入集合的 aggregate SHA-256。
	ContractDigest string `json:"contractDigest"`
	// StartedAt 是运行开始时刻，使用 UTC RFC3339 且不含本机路径。
	StartedAt string `json:"startedAt"`
	// DurationMS 是统一入口从开始到最终报告写入前的墙钟耗时，单位为毫秒。
	DurationMS int64 `json:"durationMs"`
	// Qualified 只有全部 mandatory 场景通过且 cleanup 通过后才可由入口置 true。
	Qualified bool `json:"qualified"`
	// Gates 按入口执行顺序保存分层门禁结果；不包含命令行或本机路径。
	Gates []GateReport `json:"gates"`
	// Scenarios 按 manifest 顺序保存低敏稳定结果。
	Scenarios []ScenarioReport `json:"scenarios"`
	// Failure 是首个失败门禁的稳定类别；成功时为空。
	Failure string `json:"failure,omitempty"`
	// Cleanup 是 PowerShell owner 最终写入的 pass 或 cleanup_failure。
	Cleanup string `json:"cleanup"`
}

// GateReport 是入口级 contract、quality、black-box 或 cleanup 门禁结果。
type GateReport struct {
	// ID 是资格入口定义的稳定门禁 identity。
	ID string `json:"id"`
	// Outcome 是 pass、fail 或 skipped。
	Outcome string `json:"outcome"`
	// DurationMS 是该门禁墙钟耗时，单位为毫秒。
	DurationMS int64 `json:"durationMs"`
}

// ScenarioReport 是单个 manifest 场景的稳定机器结果。
type ScenarioReport struct {
	// ID 是 manifest 中的稳定场景 identity。
	ID string `json:"id"`
	// Phase 是 manifest 中的隔离执行阶段。
	Phase string `json:"phase"`
	// Evidence 是实际要求的最低证据层级。
	Evidence string `json:"evidence"`
	// Execution 区分当前 wire 场景与本轮已执行的分层证明。
	Execution string `json:"execution"`
	// Mandatory 决定 skipped 是否使完整资格失败。
	Mandatory bool `json:"mandatory"`
	// Outcome 是 pass、fail 或 skipped。
	Outcome string `json:"outcome"`
	// DurationMS 是该场景耗时，单位为毫秒。
	DurationMS int64 `json:"durationMs"`
}

// WriteQualificationReport 原子替换目标 JSON，不写入 credential、路径、端口或 backend 文本。
func WriteQualificationReport(path string, report QualificationReport) error {
	if !filepath.IsAbs(path) || report.SchemaVersion != ReportSchemaVersion || !validRunID(report.RunID) || report.QualificationVersion != QualificationVersion || report.ProtocolVersion == 0 || report.ContractDigest == "" || report.StartedAt == "" {
		return errors.New("qualification report input is invalid")
	}
	digest, digestErr := hex.DecodeString(report.ContractDigest)
	if digestErr != nil || len(digest) != 32 || report.ContractDigest != strings.ToLower(report.ContractDigest) {
		return errors.New("qualification report contract digest is invalid")
	}
	if _, err := time.Parse(time.RFC3339, report.StartedAt); err != nil {
		return errors.New("qualification report start time is invalid")
	}
	if report.Cleanup != "pending" && report.Cleanup != "pass" && report.Cleanup != "cleanup_failure" {
		return errors.New("qualification report cleanup outcome is invalid")
	}
	seenScenarios := make(map[string]bool, len(report.Scenarios))
	for _, scenario := range report.Scenarios {
		if !validStableID(scenario.ID) || !knownPhases[scenario.Phase] || !knownEvidence[scenario.Evidence] || !knownExecutions[scenario.Execution] || !validPhaseExecution(scenario.Phase, scenario.Execution) || scenario.Outcome != "pass" && scenario.Outcome != "fail" && scenario.Outcome != "skipped" || scenario.DurationMS < 0 {
			return fmt.Errorf("qualification scenario report %q is invalid", scenario.ID)
		}
		if seenScenarios[scenario.ID] {
			return fmt.Errorf("qualification scenario report %q is duplicated", scenario.ID)
		}
		seenScenarios[scenario.ID] = true
	}
	seenGates := make(map[string]bool, len(report.Gates))
	for _, gate := range report.Gates {
		if !validStableID(gate.ID) || gate.Outcome != "pass" && gate.Outcome != "fail" && gate.Outcome != "skipped" || gate.DurationMS < 0 {
			return fmt.Errorf("qualification gate report %q is invalid", gate.ID)
		}
		if seenGates[gate.ID] {
			return fmt.Errorf("qualification gate report %q is duplicated", gate.ID)
		}
		seenGates[gate.ID] = true
	}
	if report.Failure != "" && !validStableID(report.Failure) {
		return errors.New("qualification report failure category is invalid")
	}
	if report.Qualified {
		if report.Cleanup != "pass" || report.Failure != "" || len(report.Scenarios) == 0 || len(report.Gates) == 0 {
			return errors.New("qualified report is incomplete")
		}
		for _, scenario := range report.Scenarios {
			if scenario.Mandatory && scenario.Outcome != "pass" {
				return errors.New("qualified report contains an unsuccessful mandatory scenario")
			}
		}
		for _, gate := range report.Gates {
			if gate.Outcome != "pass" {
				return errors.New("qualified report contains an unsuccessful gate")
			}
		}
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode qualification report: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create qualification report directory: %w", err)
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, encoded, 0o600); err != nil {
		return fmt.Errorf("write qualification report: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("replace qualification report: %w", err)
	}
	return nil
}

// validRunID 接受工具生成的有界小写 ASCII 字母、数字和连字符。
func validRunID(value string) bool {
	if len(value) < 8 || len(value) > 64 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' {
			continue
		}
		return false
	}
	return true
}

// NewQualificationReport 创建尚未由 cleanup owner 确认的报告骨架。
func NewQualificationReport(runID string, protocolVersion uint32, contractDigest string, startedAt time.Time) QualificationReport {
	return QualificationReport{
		SchemaVersion: ReportSchemaVersion, QualificationVersion: QualificationVersion, RunID: runID,
		ProtocolVersion: protocolVersion, ContractDigest: contractDigest, StartedAt: startedAt.UTC().Format(time.RFC3339), Cleanup: "pending",
	}
}
