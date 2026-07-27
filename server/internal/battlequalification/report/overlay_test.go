package report

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/manifest"
)

// TestNewProfileImplementationOverlayCoversFrozenProfile 验证五项实现缺口和全部预算均被独立绑定。
func TestNewProfileImplementationOverlayCoversFrozenProfile(t *testing.T) {
	repositoryRoot, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := manifest.LoadMetricCatalog(repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	metrics := make([]MetricResult, 0, len(catalog.Metrics))
	for _, spec := range catalog.Metrics {
		metrics = append(metrics, MetricResult{
			MetricID: spec.MetricID, Unit: spec.Unit, Budget: spec.Maximum,
			RunAMeasured: 1, RunBMeasured: 1, SoakMeasured: 1,
			WorstCase: 1, Disposition: "passed",
		})
	}
	identity := testIdentity("a")
	report := FinalReport{
		Qualification: qualifiedConclusion,
		Identity:      &identity,
		MetricResults: metrics,
		ScenarioResults: []ScenarioResult{
			{
				ScenarioID: "real-kcp-retransmit", WorkloadID: "default-coop",
				Disposition: "passed", EvidenceSHA256: testDigest("b"),
			},
			{
				ScenarioID: "real-mtu-boundary", WorkloadID: "default-coop",
				Disposition: "passed", EvidenceSHA256: testDigest("c"),
			},
		},
		Scope: Scope{
			EnvironmentClass: "controlled-local-fault-gateway",
			Unlocks:          []string{"implement-unity-gameplay-runtime"},
		},
	}
	overlay, err := NewProfileImplementationOverlay(
		repositoryRoot,
		report,
		testDigest("d"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(overlay.ImplementationRequired) !=
		len(profileImplementationRequirements) ||
		len(overlay.Targets) != len(catalog.Metrics) ||
		overlay.PublicInternetQualified ||
		overlay.Capacity.MaximumQualifiedActors != maximumQualifiedActors ||
		overlay.Capacity.VisitCompatibilityActors != visitCompatibilityActors {
		t.Fatalf("profile overlay coverage drifted: %+v", overlay)
	}
	for index, requirement := range profileImplementationRequirements {
		if overlay.ImplementationRequired[index].RequirementID != requirement ||
			overlay.ImplementationRequired[index].Disposition != "passed" {
			t.Fatalf(
				"profile implementation item %d drifted: %+v",
				index,
				overlay.ImplementationRequired[index],
			)
		}
	}
}

// TestNewProfileImplementationOverlayRejectsUnqualifiedReport 验证部分运行不能留下 B0.2 pass overlay。
func TestNewProfileImplementationOverlayRejectsUnqualifiedReport(t *testing.T) {
	repositoryRoot, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewProfileImplementationOverlay(
		repositoryRoot,
		newNotQualifiedReport(),
		testDigest("a"),
	); err == nil {
		t.Fatal("not-qualified report produced a profile implementation overlay")
	}
}

// testDigest 生成测试使用的 canonical lowercase SHA-256 形状。
func testDigest(character string) string {
	return strings.Repeat(character, 64)
}
