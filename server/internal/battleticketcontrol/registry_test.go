package battleticketcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/battleentry"
	"github.com/jinwiforz/ihomeland/server/internal/battleticket"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/simulationcontrol"
)

func TestRegistryMapsExactInstallStatusAndRevoke(t *testing.T) {
	target, material := testTargetAndMaterial(t)
	caller := &recordingCaller{t: t, target: target, material: material}
	registry, err := NewRegistry(caller, target.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := registry.Install(context.Background(), target, material)
	if err != nil || !installed.MatchesInstalled(target, material) {
		t.Fatalf("Install() receipt=%v err=%v", installed, err)
	}
	replayed, err := registry.Install(context.Background(), target, material)
	if err != nil || !replayed.MatchesInstalled(target, material) ||
		caller.requestIDs[0] != caller.requestIDs[1] {
		t.Fatalf("Install() replay identity drifted: receipt=%v err=%v", replayed, err)
	}
	status, err := registry.Status(context.Background(), target, material.Binding())
	if err != nil || !status.MatchesInstalled(target, material) {
		t.Fatalf("Status() receipt=%v err=%v", status, err)
	}
	revoked, err := registry.Revoke(context.Background(), target, material.Binding())
	if err != nil || revoked.State != battleentry.ChildTicketStateRevoked ||
		!sameReceiptBinding(revoked, target, material) {
		t.Fatalf("Revoke() receipt=%v err=%v", revoked, err)
	}
	if caller.installPayloadContainedSecret {
		t.Fatal("private control install contained HTTPS ticket secret")
	}
}

func TestRegistryRejectsSuccessorTargetBeforeControlCall(t *testing.T) {
	target, material := testTargetAndMaterial(t)
	caller := &recordingCaller{t: t, target: target, material: material}
	registry, _ := NewRegistry(caller, target.NodeID)
	successor := target
	successor.InstanceID, _ = simulationcontrol.NewSimulationInstanceID(
		"sinst_" + strings.Repeat("f", 32))
	successor.Revision++
	if _, err := registry.Install(context.Background(), successor, material); err == nil {
		t.Fatal("successor target accepted old ticket binding")
	}
	if len(caller.requestIDs) != 0 {
		t.Fatal("invalid successor reached control session")
	}
}

type recordingCaller struct {
	t                             *testing.T
	target                        simulationcontrol.SimulationTarget
	material                      battleticket.Material
	requestIDs                    []simulationcontrol.RequestID
	installPayloadContainedSecret bool
}

func (caller *recordingCaller) Call(_ context.Context, requestID simulationcontrol.RequestID, kind string, payload any, expected string) (json.RawMessage, error) {
	caller.t.Helper()
	caller.requestIDs = append(caller.requestIDs, requestID)
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	facts := caller.material.Binding().Facts()
	base := map[string]any{
		"ticketId":             caller.material.Binding().TicketID().Value(),
		"bindingFingerprint":   caller.material.Fingerprint().Hex(),
		"simulationInstanceId": caller.target.InstanceID.String(),
		"actorSlot":            strconv.FormatUint(uint64(facts.ActorSlot.Index()), 10),
	}
	switch kind {
	case "battle.ticket.install":
		if expected != "battle.ticket.installed" ||
			!strings.Contains(string(raw), `"proofKey":"`) {
			return nil, errors.New("install mapping drifted")
		}
		caller.installPayloadContainedSecret =
			strings.Contains(string(raw), caller.material.Secret().Value())
		base["installRequestId"] = requestID.String()
		base["state"] = "installed"
	case "battle.ticket.status.query":
		if expected != "battle.ticket.status.receipt" {
			return nil, errors.New("status mapping drifted")
		}
		base["state"] = "installed"
	case "battle.ticket.revoke":
		if expected != "battle.ticket.revoked" {
			return nil, errors.New("revoke mapping drifted")
		}
		delete(base, "actorSlot")
		base["revokeRequestId"] = requestID.String()
		base["state"] = "revoked"
	default:
		return nil, errors.New("unexpected control kind")
	}
	return json.Marshal(base)
}

func testTargetAndMaterial(t *testing.T) (simulationcontrol.SimulationTarget, battleticket.Material) {
	t.Helper()
	playerID, _ := account.NewPlayerID("ply_controlregistry")
	sessionID, _ := session.NewSessionID("ses_controlregistry")
	worldID, _ := personalworld.NewPersonalWorldID("pworld_controlregistry")
	instanceID, _ := placement.NewWorldInstanceID("winst_controlregistry")
	runtimeNodeID, _ := placement.NewRuntimeNodeID("rnode_controlregistry")
	stamp, _ := placement.NewAssignmentStamp(
		worldID,
		instanceID,
		runtimeNodeID,
		placement.AssignmentGeneration(2),
		placement.FencingToken(3),
	)
	nodeID, _ := simulationcontrol.NewSimulationNodeID("snode_control_registry")
	simulationInstanceID, _ := simulationcontrol.NewSimulationInstanceID(
		"sinst_" + strings.Repeat("1", 32))
	assignmentDigest, _ := simulationcontrol.NewDigest(strings.Repeat("a", 64))
	modelDigest, _ := simulationcontrol.NewDigest(strings.Repeat("b", 64))
	profileDigest, _ := simulationcontrol.NewDigest(strings.Repeat("c", 64))
	configDigest, _ := simulationcontrol.NewDigest(strings.Repeat("d", 64))
	wireDigest, _ := battleticket.ParseDigestHex(strings.Repeat("e", 64))
	slot, _ := battleticket.NewActorSlot(2)
	endpoint, _ := battleticket.NewEndpoint("battle.example.invalid", 58445)
	issueID, _ := battleticket.NewIssueID("biss_control_registry_0001")
	now := time.Date(2026, 7, 24, 1, 2, 3, 0, time.UTC)
	facts := battleticket.Facts{
		PlayerID: playerID, SessionID: sessionID, SessionEpoch: session.InitialEpoch,
		Role: battleticket.RoleOwner, WorldID: worldID, Assignment: stamp,
		AssignmentFingerprint: assignmentDigest, RuntimeNodeID: runtimeNodeID,
		SimulationNodeID: nodeID, SimulationInstanceID: simulationInstanceID,
		MappingGeneration: 4, TargetRevision: 5,
		ModelIdentity: modelDigest, ProfileIdentity: profileDigest,
		ConfigIdentity: configDigest, WireIdentity: wireDigest,
		ActorSlot: slot, Endpoint: endpoint, IssueID: issueID,
		IssuedAt: now, ExpiresAt: now.Add(30 * time.Second),
	}
	deriver, _ := battleticket.NewDeriver([]byte(strings.Repeat("k", 32)))
	material, err := deriver.Derive(facts)
	if err != nil {
		t.Fatal(err)
	}
	target := simulationcontrol.SimulationTarget{
		RuntimeNodeID: runtimeNodeID, NodeID: nodeID, InstanceID: simulationInstanceID,
		AssignmentFingerprint: assignmentDigest, MappingGeneration: 4,
		ModelManifest: modelDigest, ProfileManifest: profileDigest,
		ConfigIdentity: configDigest, ActorCapacity: simulationcontrol.QualifiedActorCapacity,
		Revision: 5,
	}
	return target, material
}

func sameReceiptBinding(receipt battleentry.ChildTicketReceipt, target simulationcontrol.SimulationTarget, material battleticket.Material) bool {
	return receipt.TicketID == material.Binding().TicketID() &&
		receipt.BindingFingerprint.Equal(material.Fingerprint()) &&
		receipt.Target == target &&
		receipt.Slot == material.Binding().Facts().ActorSlot
}
