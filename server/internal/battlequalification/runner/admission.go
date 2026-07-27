package runner

import (
	"context"
	"errors"

	"github.com/jinwiforz/ihomeland/server/internal/testclient"
)

const (
	// compatibilityMembershipCount 是 VisitSession 独立于 battle capacity 的冻结上限。
	compatibilityMembershipCount = 33
	// overflowBattleActorCount 是首个必须稳定拒绝的 battle actor 数。
	overflowBattleActorCount = 9
	// qualifiedBattleActorCount 是拒绝前必须保持有效的既有 actor 数。
	qualifiedBattleActorCount = 8
)

// AdmissionEvidence 是第九 actor/33 membership compatibility 的低敏结果。
type AdmissionEvidence struct {
	// SchemaVersion 是 admission evidence 的 closed generation。
	SchemaVersion int `json:"schemaVersion"`
	// QualificationVersion 绑定 B0.6 corpus。
	QualificationVersion string `json:"qualificationVersion"`
	// EvidenceKind 是 admission capacity 的 discriminator。
	EvidenceKind string `json:"evidenceKind"`
	// MembershipCount 是拒绝后再次读取的 VisitSession member 数。
	MembershipCount int `json:"membershipCount"`
	// AdmittedBattleActors 是拒绝前保持成功的 BattleTicket 数。
	AdmittedBattleActors int `json:"admittedBattleActors"`
	// CapacityRejected 表示第九 ticket 命中冻结公开错误码。
	CapacityRejected bool `json:"capacityRejected"`
	// VisitRevisionPreserved 表示拒绝前后 revision 未改变。
	VisitRevisionPreserved bool `json:"visitRevisionPreserved"`
	// Disposition 是 fail-closed 最终裁决。
	Disposition string `json:"disposition"`
}

// RunAdmissionCapacity 只经公开 admission 验证 battle 与 VisitSession capacity 解耦。
func RunAdmissionCapacity(
	ctx context.Context,
	runtime *testclient.ScenarioRuntime,
) (_ AdmissionEvidence, resultErr error) {
	if ctx == nil || runtime == nil || runtime.HTTP == nil {
		return AdmissionEvidence{}, errors.New("battle admission capacity runtime is incomplete")
	}
	admission, err := testclient.PrepareBattleAdmission(
		ctx,
		runtime,
		testclient.BattleAdmissionPlan{
			MembershipCount:  compatibilityMembershipCount,
			BattleActorCount: overflowBattleActorCount,
		},
	)
	if err != nil {
		return AdmissionEvidence{}, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, admission.Close())
	}()
	if admission.MembershipCount != compatibilityMembershipCount ||
		len(admission.Participants) != qualifiedBattleActorCount ||
		!admission.CapacityRejected ||
		admission.VisitRevision == 0 {
		return AdmissionEvidence{}, errors.New("battle admission capacity evidence is incomplete")
	}
	return AdmissionEvidence{
		SchemaVersion:          1,
		QualificationVersion:   "battle-network-qualification-v1",
		EvidenceKind:           "admission-capacity-evidence",
		MembershipCount:        admission.MembershipCount,
		AdmittedBattleActors:   len(admission.Participants),
		CapacityRejected:       true,
		VisitRevisionPreserved: true,
		Disposition:            "passed",
	}, nil
}
