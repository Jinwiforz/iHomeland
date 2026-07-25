package battleticket

import (
	"errors"
	"strconv"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	domain "github.com/jinwiforz/ihomeland/server/internal/battleticket"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/simulationcontrol"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
)

// encodeRecord 返回 issue Lua 按固定位置读取的严格字段，不包含 raw secret 或 proof key。
func encodeRecord(record domain.IssueRecord) []string {
	binding := record.Binding
	facts := binding.Facts()
	visitID := "none"
	if facts.VisitSessionID.Valid() {
		visitID = facts.VisitSessionID.Value()
	}
	return []string{
		record.Fingerprint.Hex(),
		record.SecretDigest.Hex(),
		record.ProofDigest.Hex(),
		binding.TicketID().Value(),
		facts.PlayerID.String(),
		facts.SessionID.String(),
		strconv.FormatUint(uint64(facts.SessionEpoch), 10),
		facts.Role.String(),
		facts.WorldID.String(),
		visitID,
		facts.Assignment.InstanceID().String(),
		facts.Assignment.NodeID().String(),
		strconv.FormatUint(facts.Assignment.Generation().Uint64(), 10),
		strconv.FormatUint(facts.Assignment.FencingToken().Uint64(), 10),
		facts.AssignmentFingerprint.String(),
		facts.RuntimeNodeID.String(),
		facts.SimulationNodeID.String(),
		facts.SimulationInstanceID.String(),
		strconv.FormatUint(facts.MappingGeneration, 10),
		strconv.FormatUint(facts.TargetRevision, 10),
		facts.ModelIdentity.String(),
		facts.ProfileIdentity.String(),
		facts.ConfigIdentity.String(),
		facts.WireIdentity.Hex(),
		strconv.FormatUint(uint64(facts.ActorSlot.Index()), 10),
		facts.Endpoint.Host(),
		strconv.FormatUint(uint64(facts.Endpoint.Port()), 10),
		strconv.FormatInt(facts.IssuedAt.UnixMicro(), 10),
		strconv.FormatInt(facts.ExpiresAt.UnixMicro(), 10),
	}
}

// decodeSnapshot 从 Lua 固定 reply 恢复 domain snapshot，任何部分字段都直接失败。
func decodeSnapshot(issueID domain.IssueID, items []string) (domain.IssueSnapshot, error) {
	if len(items) != 29 {
		return domain.IssueSnapshot{}, errors.New("battle ticket issue result has invalid shape")
	}
	fingerprint, err := domain.ParseDigestHex(items[0])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	secretDigest, err := domain.ParseDigestHex(items[1])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	proofDigest, err := domain.ParseDigestHex(items[2])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	ticketID, err := domain.ParseTicketID(items[3])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	playerID, err := account.NewPlayerID(items[4])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	sessionID, err := session.NewSessionID(items[5])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	epoch, err := parseUint64(items[6])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	role, err := domain.ParseRole(items[7])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	worldID, err := personalworld.NewPersonalWorldID(items[8])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	var visitID visitsession.VisitSessionID
	if items[9] != "none" {
		visitID, err = visitsession.NewVisitSessionID(items[9])
		if err != nil {
			return domain.IssueSnapshot{}, err
		}
	}
	instanceID, err := placement.NewWorldInstanceID(items[10])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	assignmentNodeID, err := placement.NewRuntimeNodeID(items[11])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	generationValue, err := parseUint64(items[12])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	generation, err := placement.NewAssignmentGeneration(generationValue)
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	fenceValue, err := parseUint64(items[13])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	fence, err := placement.NewFencingToken(fenceValue)
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	assignment, err := placement.NewAssignmentStamp(worldID, instanceID, assignmentNodeID, generation, fence)
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	assignmentFingerprint, err := simulationcontrol.NewDigest(items[14])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	runtimeNodeID, err := placement.NewRuntimeNodeID(items[15])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	simulationNodeID, err := simulationcontrol.NewSimulationNodeID(items[16])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	simulationInstanceID, err := simulationcontrol.NewSimulationInstanceID(items[17])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	mappingGeneration, err := parseUint64(items[18])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	targetRevision, err := parseUint64(items[19])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	modelIdentity, err := simulationcontrol.NewDigest(items[20])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	profileIdentity, err := simulationcontrol.NewDigest(items[21])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	configIdentity, err := simulationcontrol.NewDigest(items[22])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	wireIdentity, err := domain.ParseDigestHex(items[23])
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	slotValue, err := strconv.ParseUint(items[24], 10, 8)
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	actorSlot, err := domain.NewActorSlot(uint8(slotValue))
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	portValue, err := strconv.ParseUint(items[26], 10, 16)
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	endpoint, err := domain.NewEndpoint(items[25], uint16(portValue))
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	issuedUS, err := strconv.ParseInt(items[27], 10, 64)
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	expiresUS, err := strconv.ParseInt(items[28], 10, 64)
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	facts := domain.Facts{
		PlayerID: playerID, SessionID: sessionID, SessionEpoch: session.Epoch(epoch), Role: role,
		WorldID: worldID, VisitSessionID: visitID, Assignment: assignment,
		AssignmentFingerprint: assignmentFingerprint, RuntimeNodeID: runtimeNodeID,
		SimulationNodeID: simulationNodeID, SimulationInstanceID: simulationInstanceID,
		MappingGeneration: mappingGeneration, TargetRevision: targetRevision,
		ModelIdentity: modelIdentity, ProfileIdentity: profileIdentity, ConfigIdentity: configIdentity,
		WireIdentity: wireIdentity, ActorSlot: actorSlot, Endpoint: endpoint, IssueID: issueID,
		IssuedAt: time.UnixMicro(issuedUS), ExpiresAt: time.UnixMicro(expiresUS),
	}
	binding, err := domain.HydrateBinding(ticketID, facts)
	if err != nil {
		return domain.IssueSnapshot{}, err
	}
	snapshot := domain.IssueSnapshot{
		Binding: binding, Fingerprint: fingerprint, SecretDigest: secretDigest, ProofDigest: proofDigest,
	}
	if !snapshot.Valid() {
		return domain.IssueSnapshot{}, errors.New("battle ticket issue snapshot is inconsistent")
	}
	return snapshot, nil
}

// parseUint64 只接受 canonical decimal uint64。
func parseUint64(value string) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || strconv.FormatUint(parsed, 10) != value {
		return 0, errors.New("battle ticket integer is not canonical")
	}
	return parsed, nil
}
