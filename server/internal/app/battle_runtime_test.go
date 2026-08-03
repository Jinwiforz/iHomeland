package app

import (
	"context"
	"strings"
	"testing"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/battleentry"
	"github.com/jinwiforz/ihomeland/server/internal/battleticket"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/simulationcontrol"
)

// TestBattleActorCapacityReusesSuccessorSlot 验证同 actor 重连不增加人数且第九个 actor 仍被拒绝。
func TestBattleActorCapacityReusesSuccessorSlot(t *testing.T) {
	t.Parallel()
	owner := newBattleActorCapacity()
	target := battleCapacityTarget(t)
	player, err := account.NewPlayerID("ply_battlecapacityowner")
	if err != nil {
		t.Fatal(err)
	}
	first := reserveBattleActor(t, owner, target, player, "biss_capacity_owner_first")
	successor := reserveBattleActor(t, owner, target, player, "biss_capacity_owner_successor")
	if first.Slot() != successor.Slot() {
		t.Fatalf("successor slot=%d want=%d", successor.Slot().Index(), first.Slot().Index())
	}
	replayed := reserveBattleActor(t, owner, target, player, "biss_capacity_owner_successor")
	if replayed != successor {
		t.Fatal("successor reservation replay drifted")
	}

	for index := 1; index < simulationcontrol.QualifiedActorCapacity; index++ {
		candidate, candidateErr := account.NewPlayerID("ply_battlecapacity" + string(rune('a'+index)))
		if candidateErr != nil {
			t.Fatal(candidateErr)
		}
		reserved := reserveBattleActor(
			t,
			owner,
			target,
			candidate,
			"biss_capacity_actor_"+string(rune('a'+index)),
		)
		if reserved.Slot().Index() != uint8(index) {
			t.Fatalf("actor=%d slot=%d", index, reserved.Slot().Index())
		}
	}
	ninth, err := account.NewPlayerID("ply_battlecapacityninth")
	if err != nil {
		t.Fatal(err)
	}
	ninthIssue, err := battleticket.NewIssueID("biss_capacity_actor_ninth")
	if err != nil {
		t.Fatal(err)
	}
	_, err = owner.Reserve(context.Background(), battleentry.ReservationRequest{
		IssueID:  ninthIssue,
		PlayerID: ninth,
		Role:     battleticket.RoleOwner,
		Target:   target,
	})
	if battleticket.ErrorCodeOf(err) != battleticket.ErrorCodeCapacityExceeded {
		t.Fatalf("ninth actor error=%v code=%s", err, battleticket.ErrorCodeOf(err))
	}
}

// reserveBattleActor 创建测试 reservation 并要求成功。
func reserveBattleActor(
	t *testing.T,
	owner *battleActorCapacity,
	target simulationcontrol.SimulationTarget,
	player account.PlayerID,
	issueValue string,
) battleentry.Reservation {
	t.Helper()
	issue, err := battleticket.NewIssueID(issueValue)
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := owner.Reserve(context.Background(), battleentry.ReservationRequest{
		IssueID:  issue,
		PlayerID: player,
		Role:     battleticket.RoleOwner,
		Target:   target,
	})
	if err != nil {
		t.Fatal(err)
	}
	return reservation
}

// battleCapacityTarget 构造容量 owner 使用的完整 current target。
func battleCapacityTarget(t *testing.T) simulationcontrol.SimulationTarget {
	t.Helper()
	runtimeNode, err := placement.NewRuntimeNodeID("rnode_battlecapacity")
	if err != nil {
		t.Fatal(err)
	}
	node, err := simulationcontrol.NewSimulationNodeID("snode_battlecapacity")
	if err != nil {
		t.Fatal(err)
	}
	instance, err := simulationcontrol.NewSimulationInstanceID("sinst_00112233445566778899aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	digest := func(value byte) simulationcontrol.Digest {
		result, resultErr := simulationcontrol.NewDigest(
			strings.Repeat(string(value), 64),
		)
		if resultErr != nil {
			t.Fatal(resultErr)
		}
		return result
	}
	target := simulationcontrol.SimulationTarget{
		RuntimeNodeID:         runtimeNode,
		NodeID:                node,
		InstanceID:            instance,
		AssignmentFingerprint: digest('a'),
		MappingGeneration:     1,
		ModelManifest:         digest('b'),
		ProfileManifest:       digest('c'),
		ConfigIdentity:        digest('d'),
		ActorCapacity:         simulationcontrol.QualifiedActorCapacity,
		Revision:              1,
	}
	if err := target.Validate(); err != nil {
		t.Fatal(err)
	}
	return target
}
