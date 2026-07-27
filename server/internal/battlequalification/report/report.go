package report

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"math/bits"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"

	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/gateevidence"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/manifest"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/runner"
)

const (
	// qualificationVersion 是 finalizer 唯一接受的 B0.6 代际。
	qualificationVersion = "battle-network-qualification-v1"
	// maximumEvidenceBytes 限制单个 run/scenario JSON 的读取量。
	maximumEvidenceBytes = 16 << 20
	// basisPointsUnit 是相对复现误差的固定分母。
	basisPointsUnit = 10_000
	// microsecondsPerSecond 是 gateway byte rate 的 checked 单位换算。
	microsecondsPerSecond = 1_000_000
	// nanosecondsPerMicrosecond 是 Tick duration 的向上取整单位换算。
	nanosecondsPerMicrosecond = 1_000
	// finalReportKind 是 finalizer 唯一允许写出的报告类型。
	finalReportKind = "final"
	// qualifiedConclusion 是完整证据唯一允许解锁的结论。
	qualifiedConclusion = "battle-network-qualified-windows-x64-controlled"
	// notQualifiedConclusion 是任一完整性 gate 失败的稳定结论。
	notQualifiedConclusion = "not-qualified"
)

var (
	// ErrIncomplete 表示 run、coverage、identity、digest 或 cleanup 不足以资格。
	ErrIncomplete = errors.New("battle qualification evidence is incomplete")
	// stableIDPattern 限制 evidence file name 与 completeness code。
	stableIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

// Identity 是两次 verify 与 soak 必须逐字段相等的冻结输入集合。
type Identity struct {
	SourceSHA256      string `json:"sourceSha256"`
	BinarySHA256      string `json:"binarySha256"`
	ToolchainSHA256   string `json:"toolchainSha256"`
	DependencySHA256  string `json:"dependencySha256"`
	ModelSHA256       string `json:"modelSha256"`
	ProfileSHA256     string `json:"profileSha256"`
	ControlSHA256     string `json:"controlSha256"`
	WireSHA256        string `json:"wireSha256"`
	ConfigSHA256      string `json:"configSha256"`
	EnvironmentSHA256 string `json:"environmentSha256"`
	FaultSHA256       string `json:"faultSha256"`
	WorkloadSHA256    string `json:"workloadSha256"`
}

// CleanupEvidence 是 run-local resource audit 的闭合结果。
type CleanupEvidence struct {
	Disposition         string `json:"disposition"`
	RemainingProcesses  uint64 `json:"remainingProcesses"`
	RemainingListeners  uint64 `json:"remainingListeners"`
	RemainingContainers uint64 `json:"remainingContainers"`
	ReusableCredentials uint64 `json:"reusableCredentials"`
}

// ScenarioReference 绑定 run index 与一个 immutable evidence file。
type ScenarioReference struct {
	ScenarioID   string `json:"ScenarioId"`
	WorkloadID   string `json:"WorkloadId"`
	EvidenceKind string `json:"EvidenceKind"`
	SHA256       string `json:"Sha256"`
	Disposition  string `json:"Disposition"`
}

// RunIndex 是 PowerShell environment owner 在 cleanup 后提交的索引。
type RunIndex struct {
	SchemaVersion        int                 `json:"schemaVersion"`
	QualificationVersion string              `json:"qualificationVersion"`
	RunKind              string              `json:"runKind"`
	RunID                string              `json:"runId"`
	Identity             *Identity           `json:"identity"`
	Scenarios            []ScenarioReference `json:"scenarios"`
	Cleanup              CleanupEvidence     `json:"cleanup"`
}

// RunResult 是最终报告保留的完整 run digest。
type RunResult struct {
	Role           string `json:"role"`
	EvidenceSHA256 string `json:"evidenceSha256"`
	Disposition    string `json:"disposition"`
}

// ScenarioResult 是两次 verify 对同一 coverage 的 combined digest。
type ScenarioResult struct {
	ScenarioID     string `json:"scenarioId"`
	WorkloadID     string `json:"workloadId"`
	Disposition    string `json:"disposition"`
	EvidenceSHA256 string `json:"evidenceSha256"`
}

// MetricResult 是两次 verify、soak、worst-case 与预算裁决。
type MetricResult struct {
	MetricID     string `json:"metricId"`
	Unit         string `json:"unit"`
	Budget       uint64 `json:"budget"`
	RunAMeasured uint64 `json:"runAMeasured"`
	RunBMeasured uint64 `json:"runBMeasured"`
	SoakMeasured uint64 `json:"soakMeasured"`
	WorstCase    uint64 `json:"worstCase"`
	Disposition  string `json:"disposition"`
}

// Completeness 是所有 fail-closed 类别的稳定集合。
type Completeness struct {
	Missing      []string `json:"missing"`
	Failed       []string `json:"failed"`
	Skipped      []string `json:"skipped"`
	Stale        []string `json:"stale"`
	Unsupported  []string `json:"unsupported"`
	Unclassified []string `json:"unclassified"`
}

// Scope 固定 B0.6 结论边界。
type Scope struct {
	Platform                string   `json:"platform"`
	EnvironmentClass        string   `json:"environmentClass"`
	PublicInternetQualified bool     `json:"publicInternetQualified"`
	UnityRuntimeQualified   bool     `json:"unityRuntimeQualified"`
	Unlocks                 []string `json:"unlocks"`
}

// FinalReport 是 report.schema.json 的完整 runtime projection。
type FinalReport struct {
	SchemaVersion        int              `json:"schemaVersion"`
	QualificationVersion string           `json:"qualificationVersion"`
	ReportKind           string           `json:"reportKind"`
	Qualification        string           `json:"qualification"`
	Identity             *Identity        `json:"identity,omitempty"`
	Runs                 []RunResult      `json:"runs"`
	ScenarioResults      []ScenarioResult `json:"scenarioResults"`
	MetricResults        []MetricResult   `json:"metricResults"`
	Completeness         Completeness     `json:"completeness"`
	Cleanup              CleanupEvidence  `json:"cleanup"`
	Scope                Scope            `json:"scope"`
}

// Input 是 finalizer 的显式三 run owner；禁止自动选择最新目录。
type Input struct {
	RepositoryRoot string
	RunADirectory  string
	RunBDirectory  string
	SoakDirectory  string
}

type loadedRun struct {
	index     RunIndex
	directory string
	digest    string
	evidence  map[string]runner.Evidence
}

// Finalize 验证 identity、coverage、digest、度量复现与 cleanup，并始终生成可解释结论。
//
// 证据不完整属于资格结论而不是工具故障，因此返回 schema-valid not-qualified；
// error 仅保留给调用边界无法编码的程序错误。
func Finalize(input Input) (FinalReport, error) {
	report := newNotQualifiedReport()
	if !filepath.IsAbs(input.RepositoryRoot) {
		report.Completeness.Missing = append(report.Completeness.Missing, "repository-root")
		return report, nil
	}
	if !filepath.IsAbs(input.RunADirectory) {
		report.Completeness.Missing = append(report.Completeness.Missing, "run-a")
	}
	if !filepath.IsAbs(input.RunBDirectory) {
		report.Completeness.Missing = append(report.Completeness.Missing, "run-b")
	}
	if !filepath.IsAbs(input.SoakDirectory) {
		report.Completeness.Missing = append(report.Completeness.Missing, "soak")
	}
	if len(report.Completeness.Missing) != 0 {
		return report, nil
	}

	catalog, catalogErr := manifest.LoadMetricCatalog(input.RepositoryRoot)
	if catalogErr != nil {
		report.Completeness.Missing = append(report.Completeness.Missing, "metric-catalog")
		return report, nil
	}
	runA, runAErr := loadRun(input.RunADirectory, "verify")
	runB, runBErr := loadRun(input.RunBDirectory, "verify")
	soak, soakErr := loadRun(input.SoakDirectory, "soak")
	loaded := []struct {
		role string
		run  loadedRun
		err  error
	}{
		{role: "run-a", run: runA, err: runAErr},
		{role: "run-b", run: runB, err: runBErr},
		{role: "soak", run: soak, err: soakErr},
	}
	for _, candidate := range loaded {
		if candidate.err != nil {
			report.Completeness.Missing = append(
				report.Completeness.Missing,
				candidate.role,
			)
			continue
		}
		disposition := "passed"
		if !cleanupPassed(candidate.run.index.Cleanup) {
			disposition = "failed"
		}
		report.Runs = append(report.Runs, RunResult{
			Role: candidate.role, EvidenceSHA256: candidate.run.digest,
			Disposition: disposition,
		})
	}
	if len(report.Completeness.Missing) != 0 {
		return normalizeNotQualified(report), nil
	}

	if !runsShareFrozenIdentity(runA, runB, soak) {
		report.Completeness.Stale = append(report.Completeness.Stale, "run-identity")
		return normalizeNotQualified(report), nil
	}
	identity := *runA.index.Identity
	report.Identity = &identity

	expectedVerify, expectedSoak, coverageErr := expectedCoverage(input.RepositoryRoot)
	if coverageErr != nil {
		report.Completeness.Missing = append(report.Completeness.Missing, "coverage-contract")
		return normalizeNotQualified(report), nil
	}
	for _, check := range []struct {
		id       string
		actual   []ScenarioReference
		expected []string
	}{
		{id: "coverage-run-a", actual: runA.index.Scenarios, expected: expectedVerify},
		{id: "coverage-run-b", actual: runB.index.Scenarios, expected: expectedVerify},
		{id: "coverage-soak", actual: soak.index.Scenarios, expected: expectedSoak},
	} {
		if requireCoverage(check.actual, check.expected) != nil {
			report.Completeness.Missing = append(report.Completeness.Missing, check.id)
		}
	}
	if len(report.Completeness.Missing) != 0 {
		return normalizeNotQualified(report), nil
	}

	scenarios, scenarioErr := combineScenarioResults(runA, runB)
	if scenarioErr != nil {
		report.Completeness.Unclassified = append(
			report.Completeness.Unclassified,
			"scenario-reproducibility",
		)
	} else {
		report.ScenarioResults = scenarios
	}
	metrics, metricErr := aggregateMetrics(catalog, runA, runB, soak)
	if metricErr != nil {
		report.Completeness.Failed = append(
			report.Completeness.Failed,
			"metric-budget-or-reproducibility",
		)
	} else {
		report.MetricResults = metrics
	}
	if validateSoak(input.RepositoryRoot, soak) != nil {
		report.Completeness.Failed = append(report.Completeness.Failed, "soak-invariants")
	}
	report.Cleanup = combineCleanup(
		runA.index.Cleanup,
		runB.index.Cleanup,
		soak.index.Cleanup,
	)
	if !cleanupPassed(report.Cleanup) {
		report.Completeness.Failed = append(report.Completeness.Failed, "cleanup")
	}
	if completenessEmpty(report.Completeness) {
		report.Qualification = qualifiedConclusion
	}
	return normalizeNotQualified(report), nil
}

// runsShareFrozenIdentity 拒绝旧 run 复用、空 identity 与任一输入摘要漂移。
func runsShareFrozenIdentity(runA, runB, soak loadedRun) bool {
	return runA.index.RunID != runB.index.RunID &&
		runA.index.RunID != soak.index.RunID &&
		runB.index.RunID != soak.index.RunID &&
		runA.index.Identity != nil &&
		runB.index.Identity != nil &&
		soak.index.Identity != nil &&
		reflect.DeepEqual(*runA.index.Identity, *runB.index.Identity) &&
		reflect.DeepEqual(*runA.index.Identity, *soak.index.Identity)
}

// newNotQualifiedReport 建立不伪造 identity、digest 或 measurement 的最小报告。
func newNotQualifiedReport() FinalReport {
	return FinalReport{
		SchemaVersion:        1,
		QualificationVersion: qualificationVersion,
		ReportKind:           finalReportKind,
		Qualification:        notQualifiedConclusion,
		Runs:                 []RunResult{},
		ScenarioResults:      []ScenarioResult{},
		MetricResults:        []MetricResult{},
		Completeness: Completeness{
			Missing: []string{}, Failed: []string{}, Skipped: []string{},
			Stale: []string{}, Unsupported: []string{}, Unclassified: []string{},
		},
		Cleanup: CleanupEvidence{Disposition: "failed"},
		Scope: Scope{
			Platform: "windows-x64", EnvironmentClass: "controlled-local-fault-gateway",
			PublicInternetQualified: false, UnityRuntimeQualified: false,
			Unlocks: []string{"implement-unity-gameplay-runtime"},
		},
	}
}

// normalizeNotQualified 稳定排序、去重 completeness，且只在全部 gate 为空时保留通过结论。
func normalizeNotQualified(report FinalReport) FinalReport {
	for _, values := range []*[]string{
		&report.Completeness.Missing,
		&report.Completeness.Failed,
		&report.Completeness.Skipped,
		&report.Completeness.Stale,
		&report.Completeness.Unsupported,
		&report.Completeness.Unclassified,
	} {
		slices.Sort(*values)
		*values = slices.Compact(*values)
	}
	if !completenessEmpty(report.Completeness) {
		report.Qualification = notQualifiedConclusion
	}
	return report
}

// completenessEmpty 判断所有 fail-closed 类别是否均为空。
func completenessEmpty(value Completeness) bool {
	return len(value.Missing) == 0 &&
		len(value.Failed) == 0 &&
		len(value.Skipped) == 0 &&
		len(value.Stale) == 0 &&
		len(value.Unsupported) == 0 &&
		len(value.Unclassified) == 0
}

// validateSoak 验证长时窗口、两次 rekey 与 tracked invariant 列表均真实出现。
func validateSoak(repositoryRoot string, run loadedRun) error {
	policy, err := manifest.LoadSoakPolicy(repositoryRoot)
	if err != nil || len(run.evidence) != 1 {
		return ErrIncomplete
	}
	evidence, ok := run.evidence["real-clean-default"]
	requiredRekeys, multiplyErr := checkedMultiply(
		policy.MinimumObservedRekeys,
		uint64(evidence.ActorCount),
	)
	minimumSamples := uint64(
		policy.DurationMilliseconds/policy.SampleIntervalMilliseconds,
	) - 1
	if !ok || evidence.EvidenceKind != "soak-run-evidence" ||
		multiplyErr != nil ||
		evidence.Transitions.ClientRekeys < requiredRekeys ||
		evidence.Transitions.ControlRekeys < requiredRekeys ||
		evidence.SoakSummary == nil ||
		evidence.SoakSummary.SampleCount < minimumSamples ||
		!evidence.SoakSummary.MonotonicCountersPassed ||
		evidence.SoakSummary.MaximumActiveSessions > uint64(evidence.ActorCount) ||
		evidence.SoakSummary.MaximumInstalledTickets > uint64(evidence.ActorCount) ||
		evidence.Closure.ClientClosedSessions != uint64(evidence.ActorCount) ||
		evidence.Closure.ControlNormalCloseReasons < uint64(evidence.ActorCount) ||
		evidence.Closure.ControlUnexpectedCloseReasons != 0 ||
		evidence.Closure.RemainingActiveSessions != 0 ||
		evidence.Closure.RemainingInstalledTickets != 0 ||
		!slices.Equal(evidence.SoakInvariants, policy.RequiredInvariants) {
		return ErrIncomplete
	}
	start := evidence.Metrics.Start.Processes["cpp-child"]["monotonic-time-us"]
	end := evidence.Metrics.End.Processes["cpp-child"]["monotonic-time-us"]
	if end < start ||
		end-start < uint64(policy.DurationMilliseconds)*1_000 {
		return ErrIncomplete
	}
	return nil
}

// loadRun 进行 closed decode、index digest、cleanup 与每个 scenario file digest 复核。
func loadRun(directory, expectedKind string) (loadedRun, error) {
	indexPath := filepath.Join(directory, "run.json")
	var index RunIndex
	raw, err := readClosedJSON(indexPath, &index)
	if err != nil {
		return loadedRun{}, err
	}
	if index.SchemaVersion != 1 ||
		index.QualificationVersion != qualificationVersion ||
		index.RunKind != expectedKind ||
		index.RunID != filepath.Base(directory) ||
		(index.Identity != nil && !index.Identity.valid()) ||
		!cleanupValid(index.Cleanup) {
		return loadedRun{}, ErrIncomplete
	}
	result := loadedRun{
		index: index, directory: directory,
		digest:   sha256Hex(raw),
		evidence: make(map[string]runner.Evidence),
	}
	seen := make(map[string]struct{}, len(index.Scenarios))
	for _, reference := range index.Scenarios {
		if _, duplicate := seen[reference.ScenarioID]; duplicate ||
			!stableIDPattern.MatchString(reference.ScenarioID) ||
			!stableIDPattern.MatchString(reference.WorkloadID) ||
			reference.Disposition != "passed" ||
			!validSHA256(reference.SHA256) {
			return loadedRun{}, ErrIncomplete
		}
		seen[reference.ScenarioID] = struct{}{}
		evidence, readErr := loadReference(directory, reference)
		if readErr != nil {
			return loadedRun{}, ErrIncomplete
		}
		if evidence != nil {
			result.evidence[reference.ScenarioID] = *evidence
		}
	}
	return result, nil
}

// loadReference 按 evidence kind closed decode，并对所有索引条目复核 file digest。
func loadReference(
	directory string,
	reference ScenarioReference,
) (*runner.Evidence, error) {
	path := filepath.Join(directory, reference.ScenarioID+".json")
	switch reference.EvidenceKind {
	case "scenario-run-evidence", "soak-run-evidence":
		var evidence runner.Evidence
		raw, err := readClosedJSON(path, &evidence)
		if err != nil || sha256Hex(raw) != reference.SHA256 ||
			evidence.SchemaVersion != 1 ||
			evidence.QualificationVersion != qualificationVersion ||
			evidence.ScenarioID != reference.ScenarioID ||
			evidence.WorkloadID != reference.WorkloadID ||
			evidence.EvidenceKind != reference.EvidenceKind ||
			evidence.Disposition != "passed" ||
			evidence.ActorCount == 0 || evidence.ActorCount > 8 ||
			len(evidence.Clients) != int(evidence.ActorCount) ||
			evidence.Metrics.Validate() != nil {
			return nil, ErrIncomplete
		}
		return &evidence, nil
	case "admission-capacity-evidence":
		var evidence runner.AdmissionEvidence
		raw, err := readClosedJSON(path, &evidence)
		if err != nil || sha256Hex(raw) != reference.SHA256 ||
			reference.ScenarioID != "admission-capacity" ||
			evidence.SchemaVersion != 1 ||
			evidence.QualificationVersion != qualificationVersion ||
			evidence.EvidenceKind != reference.EvidenceKind ||
			evidence.MembershipCount != 33 ||
			evidence.AdmittedBattleActors != 8 ||
			!evidence.CapacityRejected ||
			!evidence.VisitRevisionPreserved ||
			evidence.Disposition != "passed" {
			return nil, ErrIncomplete
		}
		return nil, nil
	case "security-availability-evidence", "lifecycle-evidence", "regression-evidence":
		var evidence gateevidence.Evidence
		raw, err := readClosedJSON(path, &evidence)
		if err != nil || sha256Hex(raw) != reference.SHA256 ||
			evidence.Validate() != nil ||
			evidence.EvidenceKind != reference.EvidenceKind ||
			evidence.ScenarioID != reference.ScenarioID ||
			evidence.WorkloadID != reference.WorkloadID {
			return nil, ErrIncomplete
		}
		return nil, nil
	default:
		return nil, ErrIncomplete
	}
}

// expectedCoverage 由 tracked fault、capacity、security 与 lifecycle manifests 构造。
func expectedCoverage(repositoryRoot string) ([]string, []string, error) {
	type executionScenario struct {
		ScenarioID          string   `json:"scenarioId"`
		SourceScenarioID    string   `json:"sourceScenarioId"`
		SourceDurationTicks int      `json:"sourceDurationTicks"`
		SourceWorkloads     []string `json:"sourceWorkloads"`
		SourcePhases        []string `json:"sourcePhases"`
		Impairments         []string `json:"impairments"`
		ActorCount          uint8    `json:"actorCount"`
		Mandatory           bool     `json:"mandatory"`
	}
	var execution struct {
		FormatVersion        int                 `json:"formatVersion"`
		QualificationVersion string              `json:"qualificationVersion"`
		DocumentKind         string              `json:"documentKind"`
		ProfileVersion       string              `json:"profileVersion"`
		Seed                 uint32              `json:"seed"`
		PRNG                 string              `json:"prng"`
		RequiredDirections   []string            `json:"requiredDirections"`
		SchedulerOrder       []string            `json:"schedulerOrder"`
		GatewayPolicy        json.RawMessage     `json:"gatewayPolicy"`
		ExecutionPolicy      json.RawMessage     `json:"executionPolicy"`
		Scenarios            []executionScenario `json:"scenarios"`
	}
	if _, err := readClosedJSON(
		filepath.Join(
			repositoryRoot,
			"shared", "contracts", "fixtures", "battle", "qualification",
			"fault-execution.json",
		),
		&execution,
	); err != nil {
		return nil, nil, err
	}
	if execution.FormatVersion != 1 ||
		execution.QualificationVersion != qualificationVersion ||
		execution.DocumentKind != "fault-execution" ||
		len(execution.Scenarios) != 12 {
		return nil, nil, ErrIncomplete
	}
	var lifecycle struct {
		FormatVersion        int             `json:"formatVersion"`
		QualificationVersion string          `json:"qualificationVersion"`
		DocumentKind         string          `json:"documentKind"`
		AttackBudget         json.RawMessage `json:"attackBudget"`
		SecurityCases        []string        `json:"securityCases"`
		LifecycleCases       []string        `json:"lifecycleCases"`
		Soak                 json.RawMessage `json:"soak"`
	}
	if _, err := readClosedJSON(
		filepath.Join(
			repositoryRoot,
			"shared", "contracts", "fixtures", "battle", "qualification",
			"lifecycle-security.json",
		),
		&lifecycle,
	); err != nil {
		return nil, nil, err
	}
	if lifecycle.FormatVersion != 1 ||
		lifecycle.QualificationVersion != qualificationVersion ||
		lifecycle.DocumentKind != "lifecycle-security" ||
		len(lifecycle.SecurityCases) != 17 ||
		len(lifecycle.LifecycleCases) != 10 {
		return nil, nil, ErrIncomplete
	}
	expected := make([]string, 0, len(execution.Scenarios)+31)
	for _, scenario := range execution.Scenarios {
		expected = append(expected, scenario.ScenarioID)
	}
	expected = append(
		expected,
		"capacity-solo-owner",
		"capacity-default-capacity",
		"capacity-qualified-capacity",
		"admission-capacity",
	)
	for _, securityCase := range lifecycle.SecurityCases {
		expected = append(expected, "security-"+securityCase)
	}
	for _, lifecycleCase := range lifecycle.LifecycleCases {
		expected = append(expected, "lifecycle-"+lifecycleCase)
	}
	slices.Sort(expected)
	if len(expected) == 0 || hasDuplicate(expected) {
		return nil, nil, ErrIncomplete
	}
	return expected, []string{"real-clean-default"}, nil
}

func requireCoverage(references []ScenarioReference, expected []string) error {
	actual := make([]string, len(references))
	for index, reference := range references {
		actual[index] = reference.ScenarioID
	}
	slices.Sort(actual)
	if !slices.Equal(actual, expected) {
		return ErrIncomplete
	}
	return nil
}

func combineScenarioResults(first, second loadedRun) ([]ScenarioResult, error) {
	firstByID := make(map[string]ScenarioReference, len(first.index.Scenarios))
	for _, reference := range first.index.Scenarios {
		firstByID[reference.ScenarioID] = reference
	}
	results := make([]ScenarioResult, 0, len(second.index.Scenarios))
	for _, right := range second.index.Scenarios {
		left, ok := firstByID[right.ScenarioID]
		if !ok || left.WorkloadID != right.WorkloadID ||
			left.EvidenceKind != right.EvidenceKind ||
			left.Disposition != right.Disposition {
			return nil, ErrIncomplete
		}
		results = append(results, ScenarioResult{
			ScenarioID:     right.ScenarioID,
			WorkloadID:     right.WorkloadID,
			Disposition:    "passed",
			EvidenceSHA256: sha256Hex([]byte(left.SHA256 + "\n" + right.SHA256 + "\n")),
		})
	}
	slices.SortFunc(results, func(left, right ScenarioResult) int {
		if left.ScenarioID < right.ScenarioID {
			return -1
		}
		if left.ScenarioID > right.ScenarioID {
			return 1
		}
		return 0
	})
	return results, nil
}

func aggregateMetrics(
	catalog manifest.MetricCatalog,
	runA, runB, soak loadedRun,
) ([]MetricResult, error) {
	results := make([]MetricResult, 0, len(catalog.Metrics))
	for _, metric := range catalog.Metrics {
		var first, second, soakValue uint64
		var err error
		if metric.Window == "post-warmup-soak" {
			soakValue, err = reduceRunMetric(soak, metric)
			first, second = soakValue, soakValue
		} else {
			first, err = reduceRunMetric(runA, metric)
			if err == nil {
				second, err = reduceRunMetric(runB, metric)
			}
			if err == nil {
				soakValue, err = reduceRunMetric(soak, metric)
			}
		}
		if err != nil ||
			first > metric.Maximum || second > metric.Maximum ||
			soakValue > metric.Maximum ||
			!withinTolerance(
				first,
				second,
				uint64(metric.ReproducibilityToleranceBasisPoints),
			) {
			return nil, ErrIncomplete
		}
		results = append(results, MetricResult{
			MetricID: metric.MetricID, Unit: metric.Unit, Budget: metric.Maximum,
			RunAMeasured: first, RunBMeasured: second, SoakMeasured: soakValue,
			WorstCase: max(first, second, soakValue), Disposition: "passed",
		})
	}
	return results, nil
}

func reduceRunMetric(run loadedRun, metric manifest.MetricSpec) (uint64, error) {
	var maximum uint64
	if len(run.evidence) == 0 {
		return 0, ErrIncomplete
	}
	for _, evidence := range run.evidence {
		value, err := reduceEvidenceMetric(evidence, metric.Method)
		if err != nil {
			return 0, err
		}
		maximum = max(maximum, value)
	}
	return maximum, nil
}

func reduceEvidenceMetric(evidence runner.Evidence, method string) (uint64, error) {
	control := evidence.Metrics.End.Control
	startControl := evidence.Metrics.Start.Control
	durationUS, err := checkedDelta(
		evidence.Metrics.Start.Processes["cpp-child"]["monotonic-time-us"],
		evidence.Metrics.End.Processes["cpp-child"]["monotonic-time-us"],
	)
	if err != nil {
		return 0, err
	}
	if durationUS == 0 || evidence.ActorCount == 0 {
		return 0, ErrIncomplete
	}
	var uplinkBytes, downlinkBytes uint64
	for _, packet := range evidence.Gateway.Packets {
		if packet.Disposition != "delivered" {
			continue
		}
		if packet.Direction == "uplink" {
			uplinkBytes, err = checkedAdd(uplinkBytes, packet.Bytes)
		} else if packet.Direction == "downlink" {
			downlinkBytes, err = checkedAdd(downlinkBytes, packet.Bytes)
		} else {
			return 0, ErrIncomplete
		}
		if err != nil {
			return 0, err
		}
	}
	perActorDuration, err := checkedMultiply(
		durationUS,
		uint64(evidence.ActorCount),
	)
	if err != nil {
		return 0, err
	}
	switch method {
	case "delivered-uplink-gateway-bytes-divided-by-window-and-actors":
		return ceilProductQuotient(
			uplinkBytes,
			microsecondsPerSecond,
			perActorDuration,
		)
	case "delivered-downlink-gateway-bytes-divided-by-window-and-actors":
		return ceilProductQuotient(
			downlinkBytes,
			microsecondsPerSecond,
			perActorDuration,
		)
	case "delivered-gateway-bytes-divided-by-window":
		return ceilProductQuotient(
			downlinkBytes,
			microsecondsPerSecond,
			durationUS,
		)
	case "maximum-gateway-delivery-age":
		return evidence.Gateway.MaximumDeliveryAgeUS, nil
	case "maximum-resync-to-successor-baseline":
		var value uint64
		for _, client := range evidence.Clients {
			value = max(value, client.MaximumBaselineRecoveryUS)
		}
		return value, nil
	case "kcp-egress-xmit-divided-by-first-transmissions":
		xmits, deltaErr := checkedDelta(
			startControl["kcp-egress-packets"],
			control["kcp-egress-packets"],
		)
		if deltaErr != nil {
			return 0, deltaErr
		}
		retransmits, deltaErr := checkedDelta(
			startControl["kcp-retransmits"],
			control["kcp-retransmits"],
		)
		if deltaErr != nil {
			return 0, deltaErr
		}
		if xmits == 0 {
			return 0, nil
		}
		if retransmits >= xmits {
			return 0, ErrIncomplete
		}
		return ceilProductQuotient(
			xmits,
			basisPointsUnit,
			xmits-retransmits,
		)
	case "maximum-control-ingress-queue-high-watermark":
		return control["ingress-queue-high-watermark"], nil
	case "maximum-control-egress-queue-high-watermark":
		return control["egress-queue-high-watermark"], nil
	case "maximum-control-kcp-queue-high-watermark":
		return control["kcp-queue-high-watermark"], nil
	case "maximum-tick-duration-ns-to-us-ceiling":
		return ceilProductQuotient(
			control["maximum-tick-duration-ns"],
			1,
			nanosecondsPerMicrosecond,
		)
	case "maximum-control-tick-debt-high-watermark":
		return control["tick-debt-high-watermark"], nil
	case "maximum-control-accounted-instance-bytes":
		return control["instance-memory-bytes"], nil
	case "maximum-control-accounted-history-bytes":
		return control["history-memory-bytes"], nil
	case "maximum-end-minus-start-positive":
		var value uint64
		for _, role := range []string{"go-parent", "cpp-child"} {
			start := evidence.Metrics.Start.Processes[role]["working-set-bytes"]
			end := evidence.Metrics.End.Processes[role]["working-set-bytes"]
			if end > start {
				value = max(value, end-start)
			}
		}
		return value, nil
	default:
		return 0, ErrIncomplete
	}
}

// checkedAdd 拒绝 evidence counter 求和溢出。
func checkedAdd(left, right uint64) (uint64, error) {
	sum, carry := bits.Add64(left, right, 0)
	if carry != 0 {
		return 0, ErrIncomplete
	}
	return sum, nil
}

// checkedMultiply 拒绝 measurement 分母乘法溢出。
func checkedMultiply(left, right uint64) (uint64, error) {
	high, low := bits.Mul64(left, right)
	if high != 0 {
		return 0, ErrIncomplete
	}
	return low, nil
}

// checkedDelta 拒绝 cumulative counter 或 monotonic time 回退。
func checkedDelta(start, end uint64) (uint64, error) {
	if end < start {
		return 0, ErrIncomplete
	}
	return end - start, nil
}

func ceilProductQuotient(left, right, divisor uint64) (uint64, error) {
	if divisor == 0 {
		return 0, ErrIncomplete
	}
	numerator := new(big.Int).Mul(
		new(big.Int).SetUint64(left),
		new(big.Int).SetUint64(right),
	)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, new(big.Int).SetUint64(divisor), remainder)
	if remainder.Sign() != 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsUint64() {
		return 0, ErrIncomplete
	}
	return quotient.Uint64(), nil
}

func withinTolerance(left, right, tolerance uint64) bool {
	difference := left
	if right > left {
		difference = right - left
	} else {
		difference = left - right
	}
	maximum := max(left, right)
	if maximum == 0 {
		return true
	}
	comparison := new(big.Int).Mul(
		new(big.Int).SetUint64(difference),
		new(big.Int).SetUint64(basisPointsUnit),
	)
	limit := new(big.Int).Mul(
		new(big.Int).SetUint64(maximum),
		new(big.Int).SetUint64(tolerance),
	)
	return comparison.Cmp(limit) <= 0
}

func combineCleanup(values ...CleanupEvidence) CleanupEvidence {
	result := CleanupEvidence{Disposition: "passed"}
	for _, value := range values {
		var err error
		result.RemainingProcesses, err = checkedAdd(
			result.RemainingProcesses,
			value.RemainingProcesses,
		)
		if err == nil {
			result.RemainingListeners, err = checkedAdd(
				result.RemainingListeners,
				value.RemainingListeners,
			)
		}
		if err == nil {
			result.RemainingContainers, err = checkedAdd(
				result.RemainingContainers,
				value.RemainingContainers,
			)
		}
		if err == nil {
			result.ReusableCredentials, err = checkedAdd(
				result.ReusableCredentials,
				value.ReusableCredentials,
			)
		}
		if err != nil {
			result.RemainingProcesses = ^uint64(0)
			result.Disposition = "failed"
			continue
		}
		if !cleanupPassed(value) {
			result.Disposition = "failed"
		}
	}
	return result
}

func cleanupPassed(value CleanupEvidence) bool {
	return value.Disposition == "passed" &&
		value.RemainingProcesses == 0 &&
		value.RemainingListeners == 0 &&
		value.RemainingContainers == 0 &&
		value.ReusableCredentials == 0
}

// cleanupValid 接受真实 passed 或 failed 裁决，但拒绝未知 disposition 与伪通过。
func cleanupValid(value CleanupEvidence) bool {
	return value.Disposition == "failed" || cleanupPassed(value)
}

func readClosedJSON(path string, target any) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrIncomplete
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() ||
		info.Size() <= 0 || info.Size() > maximumEvidenceBytes {
		return nil, ErrIncomplete
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumEvidenceBytes+1))
	if err != nil || len(raw) > maximumEvidenceBytes {
		return nil, ErrIncomplete
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return nil, ErrIncomplete
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, ErrIncomplete
	}
	return raw, nil
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size &&
		hex.EncodeToString(decoded) == value
}

func (identity Identity) valid() bool {
	return validSHA256(identity.SourceSHA256) &&
		validSHA256(identity.BinarySHA256) &&
		validSHA256(identity.ToolchainSHA256) &&
		validSHA256(identity.DependencySHA256) &&
		validSHA256(identity.ModelSHA256) &&
		validSHA256(identity.ProfileSHA256) &&
		validSHA256(identity.ControlSHA256) &&
		validSHA256(identity.WireSHA256) &&
		validSHA256(identity.ConfigSHA256) &&
		validSHA256(identity.EnvironmentSHA256) &&
		validSHA256(identity.FaultSHA256) &&
		validSHA256(identity.WorkloadSHA256)
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func hasDuplicate(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index] == values[index-1] {
			return true
		}
	}
	return false
}

// MarshalCanonical 生成 schema 使用的稳定缩进 JSON 与 LF。
func MarshalCanonical(report FinalReport) ([]byte, error) {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal battle qualification report: %w", err)
	}
	return append(raw, '\n'), nil
}
