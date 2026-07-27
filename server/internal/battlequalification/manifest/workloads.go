package manifest

import (
	"path/filepath"
	"slices"

	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/correlation"
)

const (
	// workloadsRelativePath 是 B0.6 actor/cadence workload 的固定位置。
	workloadsRelativePath = "shared/contracts/fixtures/battle/qualification/workloads.json"
	// workloadsKind 是 workload document 的 closed kind。
	workloadsKind = "workloads"
)

// workloadEntry 是 admission shape 与 phase coverage 的 closed projection。
type workloadEntry struct {
	WorkloadID          string   `json:"workloadId"`
	Classification      string   `json:"classification"`
	OwnerCount          uint8    `json:"ownerCount"`
	VisitorCount        uint8    `json:"visitorCount"`
	BattleActorCount    uint8    `json:"battleActorCount"`
	ExpectedDisposition string   `json:"expectedDisposition"`
	Phases              []string `json:"phases"`
}

// messageCadenceEntry 保留 workload document 的完整 closed field set。
type messageCadenceEntry struct {
	LogicalKind          string `json:"logicalKind"`
	Lane                 string `json:"lane"`
	Direction            string `json:"direction"`
	MaximumRatePerSecond int    `json:"maximumRatePerSecond"`
	MaximumLogicalBytes  int    `json:"maximumLogicalBytes"`
}

// workloadsDocument 是 workloads.json 的 closed projection。
type workloadsDocument struct {
	FormatVersion             int                   `json:"formatVersion"`
	QualificationVersion      string                `json:"qualificationVersion"`
	DocumentKind              string                `json:"documentKind"`
	SimulationTickNanoseconds int64                 `json:"simulationTickNanoseconds"`
	InputTickNanoseconds      int64                 `json:"inputTickNanoseconds"`
	SnapshotIntervalTicks     int                   `json:"snapshotIntervalTicks"`
	Workloads                 []workloadEntry       `json:"workloads"`
	MessageCadence            []messageCadenceEntry `json:"messageCadence"`
}

// LoadCapacityDefinition 把 tracked 1/5/8 workload 投影为 clean gateway scenario。
func LoadCapacityDefinition(
	repositoryRoot string,
	workloadID string,
) (Definition, error) {
	definition, err := Load(repositoryRoot, "real-clean-default")
	if err != nil {
		return Definition{}, err
	}
	var document workloadsDocument
	if err := readClosedJSON(
		filepath.Join(repositoryRoot, filepath.FromSlash(workloadsRelativePath)),
		&document,
	); err != nil {
		return Definition{}, err
	}
	if document.FormatVersion != 1 ||
		document.QualificationVersion != qualificationVersion ||
		document.DocumentKind != workloadsKind ||
		document.SimulationTickNanoseconds != 50_000_000 ||
		document.InputTickNanoseconds != 25_000_000 ||
		document.SnapshotIntervalTicks != 2 ||
		len(document.Workloads) != 6 ||
		len(document.MessageCadence) != 8 {
		return Definition{}, ErrInvalidManifest
	}
	var selected *workloadEntry
	for index := range document.Workloads {
		if document.Workloads[index].WorkloadID == workloadID {
			if selected != nil {
				return Definition{}, ErrInvalidManifest
			}
			selected = &document.Workloads[index]
		}
	}
	if selected == nil ||
		!slices.Contains(
			[]string{"solo-owner", "default-capacity", "qualified-capacity"},
			selected.WorkloadID,
		) ||
		selected.Classification != "network-capacity" ||
		selected.OwnerCount != 1 ||
		selected.BattleActorCount != selected.OwnerCount+selected.VisitorCount ||
		selected.ExpectedDisposition != "qualified" {
		return Definition{}, ErrInvalidManifest
	}
	phases := make([]correlation.WorkloadPhase, len(selected.Phases))
	for index, phase := range selected.Phases {
		phases[index] = correlation.WorkloadPhase(phase)
		if !phases[index].Valid() || phases[index] == correlation.PhaseAdmission {
			return Definition{}, ErrInvalidManifest
		}
	}
	definition.ScenarioID = "capacity-" + selected.WorkloadID
	definition.WorkloadID = selected.WorkloadID
	definition.ActorCount = selected.BattleActorCount
	definition.Phases = phases
	definition.Impairments = []string{"clean"}
	return definition, nil
}
