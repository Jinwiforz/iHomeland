package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/manifest"
)

const (
	// profileVersion 是 B0.6 overlay 唯一允许绑定的冻结 B0.2 代际。
	profileVersion = "battle-network-profile-v2"
	// profileOverlayKind 是独立 implementation overlay 的 closed discriminator。
	profileOverlayKind = "battle-network-profile-implementation-overlay"
	// profileQualificationReportRelativePath 是冻结 B0.2 逻辑资格报告。
	profileQualificationReportRelativePath = "shared/contracts/fixtures/battle/network-profile/reports/qualification.json"
	// defaultQualifiedActors 是 B0.2 default-coop 的冻结 actor 数。
	defaultQualifiedActors = 5
	// maximumQualifiedActors 是 B0.2/B0.6 已资格的 battle hard cap。
	maximumQualifiedActors = 8
	// rejectedOverflowActor 是必须由 admission gate 拒绝的首个 actor。
	rejectedOverflowActor = maximumQualifiedActors + 1
	// visitCompatibilityActors 是仅用于 VisitSession 兼容验证的人数。
	visitCompatibilityActors = 33
)

var profileImplementationRequirements = []string{
	"cpu-per-tick-measurement",
	"history-memory-measurement",
	"kcp-adapter-parity",
	"memory-per-instance-measurement",
	"wire-encoded-size-parity",
}

// ProfileImplementationItem 把一个冻结 implementation_required 映射到真实证据。
type ProfileImplementationItem struct {
	// RequirementID 是 B0.2 qualification report 中的 exact identity。
	RequirementID string `json:"requirementId"`
	// SourceEvidenceSHA256 绑定对应 metric/scenario 的不可变证据。
	SourceEvidenceSHA256 string `json:"sourceEvidenceSha256"`
	// MeasuredValue 是按登记 method 归约后的真实最坏值。
	MeasuredValue uint64 `json:"measuredValue"`
	// Unit 禁止消费者猜测换算。
	Unit string `json:"unit"`
	// WorkloadID 是产生证据的 workload 或跨场景归约 identity。
	WorkloadID string `json:"workloadId"`
	// Method 是实际执行的稳定归约/门禁标识。
	Method string `json:"method"`
	// EnvironmentClass 限制结论不能扩张到公网。
	EnvironmentClass string `json:"environmentClass"`
	// Disposition 只允许由 qualified report 派生 passed。
	Disposition string `json:"disposition"`
}

// ProfileTargetResult 是 B0.2 target budget 的独立实现裁决。
type ProfileTargetResult struct {
	// MetricID 是 B0.6 catalog 与报告共用的稳定 identity。
	MetricID string `json:"metricId"`
	// SourceEvidenceSHA256 绑定规范化 MetricResult。
	SourceEvidenceSHA256 string `json:"sourceEvidenceSha256"`
	// MeasuredValue 是连续 verify 与 soak 的 worst-case。
	MeasuredValue uint64 `json:"measuredValue"`
	// Unit 是 metric catalog 的登记单位。
	Unit string `json:"unit"`
	// WorkloadID 表示该值跨全部 mandatory scenario 取最坏值。
	WorkloadID string `json:"workloadId"`
	// Method 是 metric catalog 的唯一归约方法。
	Method string `json:"method"`
	// Budget 是 inclusive target upper bound。
	Budget uint64 `json:"budget"`
	// EnvironmentClass 限制结论范围。
	EnvironmentClass string `json:"environmentClass"`
	// Disposition 是预算与复现共同裁决。
	Disposition string `json:"disposition"`
}

// ProfileCapacityResult 保留 battle capacity 与 VisitSession compatibility 的边界。
type ProfileCapacityResult struct {
	// DefaultQualifiedActors 是 mandatory default-coop 数量。
	DefaultQualifiedActors uint64 `json:"defaultQualifiedActors"`
	// MaximumQualifiedActors 是已验证 hard cap。
	MaximumQualifiedActors uint64 `json:"maximumQualifiedActors"`
	// RejectedOverflowActor 是首个稳定拒绝的 actor slot。
	RejectedOverflowActor uint64 `json:"rejectedOverflowActor"`
	// VisitCompatibilityActors 不代表 battle capacity。
	VisitCompatibilityActors uint64 `json:"visitCompatibilityActors"`
	// CapacityGateRequired 保持 B0.2 admission gate。
	CapacityGateRequired bool `json:"capacityGateRequired"`
	// CapacityGateDisposition 是真实第九 actor/33 人兼容场景结论。
	CapacityGateDisposition string `json:"capacityGateDisposition"`
}

// ProfileImplementationOverlay 是独立于 B0.2 source corpus 的低敏真实实现投影。
type ProfileImplementationOverlay struct {
	// SchemaVersion 是 overlay closed schema 代际。
	SchemaVersion int `json:"schemaVersion"`
	// QualificationVersion 绑定 B0.6。
	QualificationVersion string `json:"qualificationVersion"`
	// DocumentKind 区分 overlay 与 B0.2 source report。
	DocumentKind string `json:"documentKind"`
	// ProfileVersion 是只读绑定的 B0.2 代际。
	ProfileVersion string `json:"profileVersion"`
	// ProfileSHA256 是 final report 已冻结的 B0.2 identity。
	ProfileSHA256 string `json:"profileSha256"`
	// QualificationReportSHA256 绑定唯一 B0.6 final report。
	QualificationReportSHA256 string `json:"qualificationReportSha256"`
	// Qualification 只接受完整 B0.6 结论。
	Qualification string `json:"qualification"`
	// EnvironmentClass 固定本机受控 fault gateway。
	EnvironmentClass string `json:"environmentClass"`
	// PublicInternetQualified 必须保持 false。
	PublicInternetQualified bool `json:"publicInternetQualified"`
	// Unlocks 只允许 B0.7。
	Unlocks []string `json:"unlocks"`
	// ImplementationRequired 逐项补齐冻结列表。
	ImplementationRequired []ProfileImplementationItem `json:"implementationRequired"`
	// Targets 逐项保留全部 metric target 裁决。
	Targets []ProfileTargetResult `json:"targets"`
	// Capacity 保持 battle 与 VisitSession capacity 分离。
	Capacity ProfileCapacityResult `json:"capacity"`
}

// NewProfileImplementationOverlay 从已 qualified final report 派生只读 B0.2 overlay。
func NewProfileImplementationOverlay(
	repositoryRoot string,
	report FinalReport,
	reportSHA256 string,
) (ProfileImplementationOverlay, error) {
	if !filepath.IsAbs(repositoryRoot) || report.Identity == nil ||
		report.Qualification != qualifiedConclusion ||
		report.Scope.EnvironmentClass != "controlled-local-fault-gateway" ||
		report.Scope.PublicInternetQualified ||
		!validSHA256(reportSHA256) {
		return ProfileImplementationOverlay{}, errors.New("profile overlay source report is not qualified")
	}
	if err := validateProfileImplementationRequirements(repositoryRoot); err != nil {
		return ProfileImplementationOverlay{}, err
	}
	catalog, err := manifest.LoadMetricCatalog(repositoryRoot)
	if err != nil {
		return ProfileImplementationOverlay{}, err
	}
	metrics := make(map[string]MetricResult, len(report.MetricResults))
	for _, metric := range report.MetricResults {
		if metric.Disposition != "passed" {
			return ProfileImplementationOverlay{}, ErrIncomplete
		}
		metrics[metric.MetricID] = metric
	}
	scenarios := make(map[string]ScenarioResult, len(report.ScenarioResults))
	for _, scenario := range report.ScenarioResults {
		if scenario.Disposition != "passed" {
			return ProfileImplementationOverlay{}, ErrIncomplete
		}
		scenarios[scenario.ScenarioID] = scenario
	}
	items, err := buildProfileImplementationItems(
		repositoryRoot,
		report.Scope.EnvironmentClass,
		catalog,
		metrics,
		scenarios,
	)
	if err != nil {
		return ProfileImplementationOverlay{}, err
	}
	targets, err := buildProfileTargets(
		report.Scope.EnvironmentClass,
		catalog,
		metrics,
	)
	if err != nil {
		return ProfileImplementationOverlay{}, err
	}
	return ProfileImplementationOverlay{
		SchemaVersion:             1,
		QualificationVersion:      qualificationVersion,
		DocumentKind:              profileOverlayKind,
		ProfileVersion:            profileVersion,
		ProfileSHA256:             report.Identity.ProfileSHA256,
		QualificationReportSHA256: reportSHA256,
		Qualification:             qualifiedConclusion,
		EnvironmentClass:          report.Scope.EnvironmentClass,
		PublicInternetQualified:   false,
		Unlocks:                   []string{"implement-unity-gameplay-runtime"},
		ImplementationRequired:    items,
		Targets:                   targets,
		Capacity: ProfileCapacityResult{
			DefaultQualifiedActors:   defaultQualifiedActors,
			MaximumQualifiedActors:   maximumQualifiedActors,
			RejectedOverflowActor:    rejectedOverflowActor,
			VisitCompatibilityActors: visitCompatibilityActors,
			CapacityGateRequired:     true,
			CapacityGateDisposition:  "passed",
		},
	}, nil
}

// buildProfileImplementationItems 逐项绑定 B0.2 implementation_required。
func buildProfileImplementationItems(
	repositoryRoot string,
	environmentClass string,
	catalog manifest.MetricCatalog,
	metrics map[string]MetricResult,
	scenarios map[string]ScenarioResult,
) ([]ProfileImplementationItem, error) {
	type metricBinding struct {
		requirementID string
		metricID      string
		scenarioID    string
	}
	bindings := []metricBinding{
		{requirementID: "cpu-per-tick-measurement", metricID: "cpu-per-simulation-tick"},
		{requirementID: "history-memory-measurement", metricID: "history-memory"},
		{requirementID: "kcp-adapter-parity", metricID: "kcp-amplification", scenarioID: "real-kcp-retransmit"},
		{requirementID: "memory-per-instance-measurement", metricID: "instance-memory"},
	}
	items := make([]ProfileImplementationItem, 0, len(profileImplementationRequirements))
	for _, binding := range bindings {
		metric, ok := metrics[binding.metricID]
		spec, specErr := catalog.Metric(binding.metricID)
		if !ok || specErr != nil {
			return nil, ErrIncomplete
		}
		evidenceDigest, err := digestJSON(metric)
		if err != nil {
			return nil, err
		}
		if binding.scenarioID != "" {
			scenario, exists := scenarios[binding.scenarioID]
			if !exists {
				return nil, ErrIncomplete
			}
			evidenceDigest = sha256Hex([]byte(
				evidenceDigest + "\n" + scenario.EvidenceSHA256 + "\n",
			))
		}
		items = append(items, ProfileImplementationItem{
			RequirementID:        binding.requirementID,
			SourceEvidenceSHA256: evidenceDigest,
			MeasuredValue:        metric.WorstCase,
			Unit:                 metric.Unit,
			WorkloadID:           "cross-scenario-worst-case",
			Method:               spec.Method,
			EnvironmentClass:     environmentClass,
			Disposition:          "passed",
		})
	}
	mtuScenario, ok := scenarios["real-mtu-boundary"]
	if !ok {
		return nil, ErrIncomplete
	}
	definition, err := manifest.Load(repositoryRoot, "real-mtu-boundary")
	if err != nil || definition.GatewayPolicy.MaximumDatagramBytes <= 0 {
		return nil, ErrIncomplete
	}
	items = append(items, ProfileImplementationItem{
		RequirementID:        "wire-encoded-size-parity",
		SourceEvidenceSHA256: mtuScenario.EvidenceSHA256,
		MeasuredValue:        uint64(definition.GatewayPolicy.MaximumDatagramBytes),
		Unit:                 "bytes",
		WorkloadID:           mtuScenario.WorkloadID,
		Method:               "real-mtu-boundary-scenario",
		EnvironmentClass:     environmentClass,
		Disposition:          "passed",
	})
	slices.SortFunc(items, func(left, right ProfileImplementationItem) int {
		return indexOf(profileImplementationRequirements, left.RequirementID) -
			indexOf(profileImplementationRequirements, right.RequirementID)
	})
	if len(items) != len(profileImplementationRequirements) {
		return nil, ErrIncomplete
	}
	return items, nil
}

// buildProfileTargets 把 catalog 顺序、方法和 final worst-case 无损投影到 overlay。
func buildProfileTargets(
	environmentClass string,
	catalog manifest.MetricCatalog,
	metrics map[string]MetricResult,
) ([]ProfileTargetResult, error) {
	targets := make([]ProfileTargetResult, 0, len(catalog.Metrics))
	for _, spec := range catalog.Metrics {
		metric, ok := metrics[spec.MetricID]
		if !ok || metric.Budget != spec.Maximum ||
			metric.Unit != spec.Unit || metric.Disposition != "passed" {
			return nil, ErrIncomplete
		}
		digest, err := digestJSON(metric)
		if err != nil {
			return nil, err
		}
		targets = append(targets, ProfileTargetResult{
			MetricID:             metric.MetricID,
			SourceEvidenceSHA256: digest,
			MeasuredValue:        metric.WorstCase,
			Unit:                 metric.Unit,
			WorkloadID:           "cross-scenario-worst-case",
			Method:               spec.Method,
			Budget:               metric.Budget,
			EnvironmentClass:     environmentClass,
			Disposition:          "passed",
		})
	}
	return targets, nil
}

// validateProfileImplementationRequirements 拒绝 B0.2 冻结列表与映射漂移。
func validateProfileImplementationRequirements(repositoryRoot string) error {
	path := filepath.Join(
		repositoryRoot,
		filepath.FromSlash(profileQualificationReportRelativePath),
	)
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	var projection struct {
		ImplementationRequired []string `json:"implementation_required"`
	}
	decoder := json.NewDecoder(io.LimitReader(file, maximumEvidenceBytes))
	if err := decoder.Decode(&projection); err != nil ||
		!slices.Equal(
			projection.ImplementationRequired,
			profileImplementationRequirements,
		) {
		return ErrIncomplete
	}
	return nil
}

// digestJSON 对 typed 低敏投影生成 canonical SHA-256。
func digestJSON(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

// indexOf 返回冻结小集合中的唯一位置。
func indexOf(values []string, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return len(values)
}
