package worldadmission

import (
	"errors"
	"strconv"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
	domain "github.com/jinwiforz/ihomeland/server/internal/worldadmission"
)

// encodeBinding 返回 issue Lua 按固定位置读取的严格字段。
func encodeBinding(binding domain.Binding) []string {
	visitID := "none"
	visitRevision := "0"
	if binding.VisitSessionID().Valid() {
		visitID = binding.VisitSessionID().Value()
		visitRevision = strconv.FormatUint(uint64(binding.VisitRevision()), 10)
	}
	return []string{
		binding.PlayerID().String(), binding.SessionID().String(), strconv.FormatUint(uint64(binding.Epoch()), 10),
		binding.Role().String(), binding.WorldID().String(), visitID, binding.Purpose().String(), visitRevision,
		binding.Assignment().InstanceID().String(), binding.Assignment().NodeID().String(),
		strconv.FormatUint(binding.Assignment().Generation().Uint64(), 10), strconv.FormatUint(binding.Assignment().FencingToken().Uint64(), 10),
		"tls_tcp", binding.Endpoint().Host(), strconv.FormatUint(uint64(binding.Endpoint().Port()), 10),
	}
}

// decodeBindingReply 从 code 后的 17 个 binding 字段恢复 domain 值。
func decodeBindingReply(items []string) (domain.Binding, error) {
	if len(items) != 17 {
		return domain.Binding{}, errors.New("world admission binding result has invalid shape")
	}
	playerID, err := account.NewPlayerID(items[0])
	if err != nil {
		return domain.Binding{}, err
	}
	sessionID, err := session.NewSessionID(items[1])
	if err != nil {
		return domain.Binding{}, err
	}
	epochValue, err := strconv.ParseUint(items[2], 10, 64)
	if err != nil {
		return domain.Binding{}, err
	}
	role, err := domain.ParseRole(items[3])
	if err != nil {
		return domain.Binding{}, err
	}
	worldID, err := personalworld.NewPersonalWorldID(items[4])
	if err != nil {
		return domain.Binding{}, err
	}
	var visitID visitsession.VisitSessionID
	if items[5] != "none" {
		visitID, err = visitsession.NewVisitSessionID(items[5])
		if err != nil {
			return domain.Binding{}, err
		}
	}
	purpose, err := domain.ParsePurpose(items[6])
	if err != nil {
		return domain.Binding{}, err
	}
	visitRevisionValue, err := strconv.ParseUint(items[7], 10, 64)
	if err != nil {
		return domain.Binding{}, err
	}
	visitRevision := visitsession.Revision(visitRevisionValue)
	instanceID, err := placement.NewWorldInstanceID(items[8])
	if err != nil {
		return domain.Binding{}, err
	}
	nodeID, err := placement.NewRuntimeNodeID(items[9])
	if err != nil {
		return domain.Binding{}, err
	}
	generationValue, err := strconv.ParseUint(items[10], 10, 64)
	if err != nil {
		return domain.Binding{}, err
	}
	generation, err := placement.NewAssignmentGeneration(generationValue)
	if err != nil {
		return domain.Binding{}, err
	}
	fenceValue, err := strconv.ParseUint(items[11], 10, 64)
	if err != nil {
		return domain.Binding{}, err
	}
	fence, err := placement.NewFencingToken(fenceValue)
	if err != nil {
		return domain.Binding{}, err
	}
	stamp, err := placement.NewAssignmentStamp(worldID, instanceID, nodeID, generation, fence)
	if err != nil {
		return domain.Binding{}, err
	}
	if items[12] != "tls_tcp" {
		return domain.Binding{}, errors.New("world admission channel is invalid")
	}
	port, err := strconv.ParseUint(items[14], 10, 16)
	if err != nil {
		return domain.Binding{}, err
	}
	endpoint, err := session.NewEndpoint(session.ChannelTLSTCP, items[13], uint16(port))
	if err != nil {
		return domain.Binding{}, err
	}
	issuedUS, err := strconv.ParseInt(items[15], 10, 64)
	if err != nil {
		return domain.Binding{}, err
	}
	expiresUS, err := strconv.ParseInt(items[16], 10, 64)
	if err != nil {
		return domain.Binding{}, err
	}
	return domain.NewBinding(playerID, sessionID, session.Epoch(epochValue), role, worldID, visitID, purpose, visitRevision, stamp, endpoint, time.UnixMicro(issuedUS), time.UnixMicro(expiresUS))
}
