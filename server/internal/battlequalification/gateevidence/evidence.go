// Package gateevidence 定义 B0.6 security、lifecycle 与 regression 的闭合低敏证据。
package gateevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"slices"
)

const (
	// QualificationVersion 是当前证据唯一接受的资格代际。
	QualificationVersion = "battle-network-qualification-v1"
	// SecurityAvailabilityKind 表示真实攻击与合法五人流量并发的裁决。
	SecurityAvailabilityKind = "security-availability-evidence"
	// LifecycleKind 表示受控生命周期迁移的 predecessor/successor 裁决。
	LifecycleKind = "lifecycle-evidence"
	// RegressionKind 表示不依赖运行态 identity 的 failure regression 裁决。
	RegressionKind = "regression-evidence"
	// PassedDisposition 是全部断言成功后的唯一通过值。
	PassedDisposition = "passed"
	// TransitionPreserved 表示迁移前后仍是同一受控 binding。
	TransitionPreserved = "preserved"
	// TransitionReplaced 表示 successor binding 已替换 predecessor。
	TransitionReplaced = "replaced"
	// TransitionTerminated 表示 predecessor 已终止且不存在 successor。
	TransitionTerminated = "terminated"
)

var (
	stableIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	sha256Pattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// SecurityResult 保存攻击规模、真实拒绝量和合法 workload 可用性。
type SecurityResult struct {
	// AttackDatagrams 是本场景真实进入 socket 的攻击 datagram 数。
	AttackDatagrams uint64 `json:"attackDatagrams"`
	// RejectedDatagrams 是 control snapshot 观测到的对应拒绝增量。
	RejectedDatagrams uint64 `json:"rejectedDatagrams"`
	// ResponseDatagrams 是未认证攻击收到的响应数，用于裁决抗放大。
	ResponseDatagrams uint64 `json:"responseDatagrams"`
	// LegitimateActorCount 固定为与攻击并发运行的五个合法 actor。
	LegitimateActorCount uint8 `json:"legitimateActorCount"`
	// AvailabilityPassed 表示合法 workload 的完整预算裁决仍通过。
	AvailabilityPassed bool `json:"availabilityPassed"`
}

// SecurityInput 是 security availability 构造器的完整 typed 输入。
type SecurityInput struct {
	// ScenarioID 是带 `security-` 前缀的 tracked case identity。
	ScenarioID string
	// WorkloadID 是与拒绝用例并发运行的合法 workload。
	WorkloadID string
	// Assertions 是已经由 runner 实际验证的低敏断言。
	Assertions []string
	// AttackDatagrams 是本场景进入本机 socket 的拒绝测试 datagram 数。
	AttackDatagrams uint64
	// RejectedDatagrams 是 control snapshot 的拒绝增量。
	RejectedDatagrams uint64
	// ResponseDatagrams 是拒绝测试收到的 response 数。
	ResponseDatagrams uint64
	// LegitimateActorCount 是并发保持可用的合法 actor 数。
	LegitimateActorCount uint8
}

// LifecycleInput 是 lifecycle transition 构造器的完整 typed 输入。
type LifecycleInput struct {
	// ScenarioID 是带 `lifecycle-` 前缀的 tracked case identity。
	ScenarioID string
	// WorkloadID 是迁移影响的冻结 workload。
	WorkloadID string
	// Assertions 是已经由 runner 实际验证的低敏断言。
	Assertions []string
	// Mode 只能是 preserved、replaced 或 terminated。
	Mode string
	// PredecessorProjection 是只在内存中存在的旧 binding 投影。
	PredecessorProjection []byte
	// SuccessorProjection 是 preserved/replaced 时只在内存中存在的新投影。
	SuccessorProjection []byte
	// PredecessorRejected 表示旧流量未推进迁移后的 timeline。
	PredecessorRejected bool
}

// BindingTransition 保存不含业务 identity 原文的生命周期代际关系。
type BindingTransition struct {
	// Mode 区分 identity 保持、替换或终止。
	Mode string `json:"mode"`
	// PredecessorSHA256 是迁移前公开 identity 投影的摘要。
	PredecessorSHA256 string `json:"predecessorSha256"`
	// SuccessorSHA256 是迁移后公开 identity 投影摘要；终止时为空。
	SuccessorSHA256 string `json:"successorSha256,omitempty"`
	// PredecessorRejected 表示旧流量未推进 successor timeline。
	PredecessorRejected bool `json:"predecessorRejected"`
}

// Evidence 是 security、lifecycle 与 regression 共用的闭合 envelope。
type Evidence struct {
	// SchemaVersion 是 gate evidence schema 版本。
	SchemaVersion int `json:"schemaVersion"`
	// QualificationVersion 绑定完整 B0.6 资格代际。
	QualificationVersion string `json:"qualificationVersion"`
	// EvidenceKind 选择 security、lifecycle 或 regression projection。
	EvidenceKind string `json:"evidenceKind"`
	// ScenarioID 是 run index 使用的完整稳定标识。
	ScenarioID string `json:"scenarioId"`
	// WorkloadID 是本 gate 并发或影响的冻结 workload。
	WorkloadID string `json:"workloadId"`
	// Assertions 是排序、去重后的已验证低敏断言。
	Assertions []string `json:"assertions"`
	// Security 只在 security-availability evidence 中存在。
	Security *SecurityResult `json:"security,omitempty"`
	// Transition 只在 lifecycle evidence 中存在。
	Transition *BindingTransition `json:"transition,omitempty"`
	// Disposition 只有全部 typed gate 通过时才允许为 passed。
	Disposition string `json:"disposition"`
}

// NewSecurity 构造、规范化并验证 security availability evidence。
func NewSecurity(input SecurityInput) (Evidence, error) {
	evidence := Evidence{
		SchemaVersion:        1,
		QualificationVersion: QualificationVersion,
		EvidenceKind:         SecurityAvailabilityKind,
		ScenarioID:           input.ScenarioID,
		WorkloadID:           input.WorkloadID,
		Assertions:           sortedAssertions(input.Assertions),
		Security: &SecurityResult{
			AttackDatagrams:      input.AttackDatagrams,
			RejectedDatagrams:    input.RejectedDatagrams,
			ResponseDatagrams:    input.ResponseDatagrams,
			LegitimateActorCount: input.LegitimateActorCount,
			AvailabilityPassed:   true,
		},
		Disposition: PassedDisposition,
	}
	if err := evidence.Validate(); err != nil {
		return Evidence{}, err
	}
	return evidence, nil
}

// NewLifecycle 构造只保留 projection digest 的 lifecycle evidence。
func NewLifecycle(input LifecycleInput) (Evidence, error) {
	if len(input.PredecessorProjection) == 0 ||
		(input.Mode == TransitionTerminated && len(input.SuccessorProjection) != 0) ||
		(input.Mode != TransitionTerminated && len(input.SuccessorProjection) == 0) {
		return Evidence{}, errors.New("battle qualification lifecycle projection is invalid")
	}
	transition := &BindingTransition{
		Mode:                input.Mode,
		PredecessorSHA256:   projectionDigest(input.PredecessorProjection),
		PredecessorRejected: input.PredecessorRejected,
	}
	if input.Mode != TransitionTerminated {
		transition.SuccessorSHA256 = projectionDigest(input.SuccessorProjection)
	}
	evidence := Evidence{
		SchemaVersion:        1,
		QualificationVersion: QualificationVersion,
		EvidenceKind:         LifecycleKind,
		ScenarioID:           input.ScenarioID,
		WorkloadID:           input.WorkloadID,
		Assertions:           sortedAssertions(input.Assertions),
		Transition:           transition,
		Disposition:          PassedDisposition,
	}
	if err := evidence.Validate(); err != nil {
		return Evidence{}, err
	}
	return evidence, nil
}

// Validate 拒绝未知 kind、伪造通过、未排序断言和不完整 typed projection。
func (evidence Evidence) Validate() error {
	if evidence.SchemaVersion != 1 ||
		evidence.QualificationVersion != QualificationVersion ||
		!stableIDPattern.MatchString(evidence.ScenarioID) ||
		!stableIDPattern.MatchString(evidence.WorkloadID) ||
		evidence.Disposition != PassedDisposition ||
		len(evidence.Assertions) == 0 ||
		!slices.IsSorted(evidence.Assertions) ||
		hasDuplicate(evidence.Assertions) {
		return errors.New("battle qualification gate evidence is invalid")
	}
	for _, assertion := range evidence.Assertions {
		if !stableIDPattern.MatchString(assertion) {
			return errors.New("battle qualification gate assertion is invalid")
		}
	}
	switch evidence.EvidenceKind {
	case SecurityAvailabilityKind:
		if evidence.Security == nil || evidence.Transition != nil ||
			evidence.Security.AttackDatagrams == 0 ||
			evidence.Security.RejectedDatagrams == 0 ||
			evidence.Security.RejectedDatagrams > evidence.Security.AttackDatagrams ||
			evidence.Security.ResponseDatagrams > evidence.Security.AttackDatagrams ||
			evidence.Security.LegitimateActorCount != 5 ||
			!evidence.Security.AvailabilityPassed {
			return errors.New("battle qualification security evidence is invalid")
		}
	case LifecycleKind:
		if evidence.Security != nil || evidence.Transition == nil ||
			!evidence.Transition.valid() {
			return errors.New("battle qualification lifecycle evidence is invalid")
		}
	case RegressionKind:
		if evidence.Security != nil || evidence.Transition != nil {
			return errors.New("battle qualification regression evidence is invalid")
		}
	default:
		return errors.New("battle qualification gate evidence kind is invalid")
	}
	return nil
}

// valid 验证 successor 语义与摘要，不允许 terminated 伪造 successor。
func (transition BindingTransition) valid() bool {
	if !sha256Pattern.MatchString(transition.PredecessorSHA256) ||
		!transition.PredecessorRejected {
		return false
	}
	switch transition.Mode {
	case TransitionPreserved:
		return sha256Pattern.MatchString(transition.SuccessorSHA256) &&
			transition.SuccessorSHA256 == transition.PredecessorSHA256
	case TransitionReplaced:
		return sha256Pattern.MatchString(transition.SuccessorSHA256) &&
			transition.SuccessorSHA256 != transition.PredecessorSHA256
	case TransitionTerminated:
		return transition.SuccessorSHA256 == ""
	default:
		return false
	}
}

// sortedAssertions 隔离 caller slice并生成稳定顺序；重复项仍由 Validate 拒绝。
func sortedAssertions(assertions []string) []string {
	normalized := slices.Clone(assertions)
	slices.Sort(normalized)
	return normalized
}

// projectionDigest 对运行态投影做单向摘要，证据不保留 identity 原文。
func projectionDigest(projection []byte) string {
	digest := sha256.Sum256(projection)
	return hex.EncodeToString(digest[:])
}

// hasDuplicate 要求 caller 先排序，以 O(n) 拒绝重复断言。
func hasDuplicate(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index] == values[index-1] {
			return true
		}
	}
	return false
}
