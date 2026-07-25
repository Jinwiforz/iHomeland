package battleticketcontrol

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/jinwiforz/ihomeland/server/internal/battleentry"
	"github.com/jinwiforz/ihomeland/server/internal/battleticket"
	"github.com/jinwiforz/ihomeland/server/internal/simulationcontrol"
)

// Caller 是 registry 唯一需要的低优先级 control request 端口。
type Caller interface {
	// Call 发送 closed request 并等待 exact receipt。
	Call(context.Context, simulationcontrol.RequestID, string, any, string) (json.RawMessage, error)
}

// Registry 把一个不可复活 SimulationNodeID 绑定到它的 control session。
type Registry struct {
	// caller 是 child 唯一继承 pipe owner。
	caller Caller
	// nodeID 防止旧 adapter 把 ticket 发给 successor child。
	nodeID simulationcontrol.SimulationNodeID
}

// NewRegistry 校验 exact child session binding。
func NewRegistry(caller Caller, nodeID simulationcontrol.SimulationNodeID) (*Registry, error) {
	if caller == nil || !nodeID.Valid() {
		return nil, errors.New("battle ticket child registry dependencies are invalid")
	}
	return &Registry{caller: caller, nodeID: nodeID}, nil
}

// Install 将 proof key 与完整 immutable binding 幂等安装到 exact child。
func (registry *Registry) Install(ctx context.Context, target simulationcontrol.SimulationTarget, material battleticket.Material) (battleentry.ChildTicketReceipt, error) {
	if registry == nil || ctx == nil || !material.Valid() ||
		registry.validateTarget(target, material.Binding()) != nil {
		return battleentry.ChildTicketReceipt{}, errors.New("battle ticket install input is invalid")
	}
	requestID, err := registry.requestID("install", material.Binding())
	if err != nil {
		return battleentry.ChildTicketReceipt{}, err
	}
	facts := material.Binding().Facts()
	proof := material.ProofKey().Bytes()
	defer clear(proof[:])
	payload := installPayload{
		InstallRequestID:   requestID.String(),
		TicketID:           material.Binding().TicketID().Value(),
		BindingFingerprint: material.Fingerprint().Hex(),
		Binding:            bindingPayloadFrom(facts),
		ProofKey:           hex.EncodeToString(proof[:]),
	}
	raw, err := registry.caller.Call(
		ctx,
		requestID,
		"battle.ticket.install",
		payload,
		"battle.ticket.installed",
	)
	payload.ProofKey = ""
	if err != nil {
		return battleentry.ChildTicketReceipt{}, err
	}
	return registry.decodeReceipt(raw, target, material.Binding(), true)
}

// Status 查询 exact ticket/binding，用于 install receipt 丢失后的有界恢复。
func (registry *Registry) Status(ctx context.Context, target simulationcontrol.SimulationTarget, binding battleticket.Binding) (battleentry.ChildTicketReceipt, error) {
	if registry == nil || ctx == nil || registry.validateTarget(target, binding) != nil {
		return battleentry.ChildTicketReceipt{}, errors.New("battle ticket status input is invalid")
	}
	requestID, err := registry.requestID("status", binding)
	if err != nil {
		return battleentry.ChildTicketReceipt{}, err
	}
	raw, err := registry.caller.Call(
		ctx,
		requestID,
		"battle.ticket.status.query",
		lookupPayloadFrom(binding),
		"battle.ticket.status.receipt",
	)
	if err != nil {
		return battleentry.ChildTicketReceipt{}, err
	}
	return registry.decodeReceipt(raw, target, binding, false)
}

// Revoke 只撤销 exact node/instance/ticket/binding，request identity 可安全重放。
func (registry *Registry) Revoke(ctx context.Context, target simulationcontrol.SimulationTarget, binding battleticket.Binding) (battleentry.ChildTicketReceipt, error) {
	if registry == nil || ctx == nil || registry.validateTarget(target, binding) != nil {
		return battleentry.ChildTicketReceipt{}, errors.New("battle ticket revoke input is invalid")
	}
	requestID, err := registry.requestID("revoke", binding)
	if err != nil {
		return battleentry.ChildTicketReceipt{}, err
	}
	raw, err := registry.caller.Call(
		ctx,
		requestID,
		"battle.ticket.revoke",
		struct {
			RevokeRequestID      string `json:"revokeRequestId"`
			TicketID             string `json:"ticketId"`
			BindingFingerprint   string `json:"bindingFingerprint"`
			SimulationInstanceID string `json:"simulationInstanceId"`
			ActorSlot            string `json:"actorSlot"`
		}{
			RevokeRequestID:      requestID.String(),
			TicketID:             binding.TicketID().Value(),
			BindingFingerprint:   binding.Fingerprint().Hex(),
			SimulationInstanceID: target.InstanceID.String(),
			ActorSlot:            strconv.FormatUint(uint64(binding.Facts().ActorSlot.Index()), 10),
		},
		"battle.ticket.revoked",
	)
	if err != nil {
		return battleentry.ChildTicketReceipt{}, err
	}
	var receipt revokeReceipt
	if err := decodeClosed(raw, &receipt); err != nil {
		return battleentry.ChildTicketReceipt{}, err
	}
	if receipt.RevokeRequestID != requestID.String() ||
		receipt.TicketID != binding.TicketID().Value() ||
		receipt.BindingFingerprint != binding.Fingerprint().Hex() ||
		receipt.SimulationInstanceID != target.InstanceID.String() ||
		receipt.State != "revoked" {
		return battleentry.ChildTicketReceipt{}, errors.New("battle ticket revoke receipt drifted")
	}
	return battleentry.ChildTicketReceipt{
		TicketID: binding.TicketID(), BindingFingerprint: binding.Fingerprint(),
		Target: target, Slot: binding.Facts().ActorSlot,
		State: battleentry.ChildTicketStateRevoked,
	}, nil
}

type installPayload struct {
	InstallRequestID   string         `json:"installRequestId"`
	TicketID           string         `json:"ticketId"`
	BindingFingerprint string         `json:"bindingFingerprint"`
	Binding            bindingPayload `json:"binding"`
	ProofKey           string         `json:"proofKey"`
}

type bindingPayload struct {
	PlayerID              string `json:"playerId"`
	SessionID             string `json:"sessionId"`
	SessionEpoch          string `json:"sessionEpoch"`
	Role                  string `json:"role"`
	PersonalWorldID       string `json:"personalWorldId"`
	VisitSessionID        string `json:"visitSessionId"`
	WorldInstanceID       string `json:"worldInstanceId"`
	RuntimeNodeID         string `json:"runtimeNodeId"`
	AssignmentGeneration  string `json:"assignmentGeneration"`
	FencingToken          string `json:"fencingToken"`
	AssignmentFingerprint string `json:"assignmentFingerprint"`
	SimulationNodeID      string `json:"simulationNodeId"`
	SimulationInstanceID  string `json:"simulationInstanceId"`
	MappingGeneration     string `json:"mappingGeneration"`
	TargetRevision        string `json:"targetRevision"`
	ModelIdentity         string `json:"modelIdentity"`
	ProfileIdentity       string `json:"profileIdentity"`
	ConfigIdentity        string `json:"configIdentity"`
	WireIdentity          string `json:"wireIdentity"`
	ActorSlot             string `json:"actorSlot"`
	AdvertisedHost        string `json:"advertisedHost"`
	AdvertisedPort        string `json:"advertisedPort"`
	IssueID               string `json:"issueId"`
	IssuedAtUnixMS        string `json:"issuedAtUnixMs"`
	ExpiresAtUnixMS       string `json:"expiresAtUnixMs"`
}

type lookupPayload struct {
	TicketID             string `json:"ticketId"`
	BindingFingerprint   string `json:"bindingFingerprint"`
	SimulationInstanceID string `json:"simulationInstanceId"`
}

type ticketReceipt struct {
	InstallRequestID     string  `json:"installRequestId,omitempty"`
	TicketID             string  `json:"ticketId"`
	BindingFingerprint   string  `json:"bindingFingerprint"`
	SimulationInstanceID string  `json:"simulationInstanceId"`
	ActorSlot            *string `json:"actorSlot,omitempty"`
	State                string  `json:"state"`
}

type revokeReceipt struct {
	RevokeRequestID      string `json:"revokeRequestId"`
	TicketID             string `json:"ticketId"`
	BindingFingerprint   string `json:"bindingFingerprint"`
	SimulationInstanceID string `json:"simulationInstanceId"`
	State                string `json:"state"`
}

func bindingPayloadFrom(facts battleticket.Facts) bindingPayload {
	visitID := ""
	if facts.VisitSessionID.Valid() {
		visitID = facts.VisitSessionID.Value()
	}
	return bindingPayload{
		PlayerID: facts.PlayerID.String(), SessionID: facts.SessionID.String(),
		SessionEpoch: strconv.FormatUint(uint64(facts.SessionEpoch), 10),
		Role:         facts.Role.String(), PersonalWorldID: facts.WorldID.String(),
		VisitSessionID: visitID, WorldInstanceID: facts.Assignment.InstanceID().String(),
		RuntimeNodeID:         facts.RuntimeNodeID.String(),
		AssignmentGeneration:  strconv.FormatUint(facts.Assignment.Generation().Uint64(), 10),
		FencingToken:          strconv.FormatUint(facts.Assignment.FencingToken().Uint64(), 10),
		AssignmentFingerprint: facts.AssignmentFingerprint.String(),
		SimulationNodeID:      facts.SimulationNodeID.String(),
		SimulationInstanceID:  facts.SimulationInstanceID.String(),
		MappingGeneration:     strconv.FormatUint(facts.MappingGeneration, 10),
		TargetRevision:        strconv.FormatUint(facts.TargetRevision, 10),
		ModelIdentity:         facts.ModelIdentity.String(), ProfileIdentity: facts.ProfileIdentity.String(),
		ConfigIdentity: facts.ConfigIdentity.String(), WireIdentity: facts.WireIdentity.Hex(),
		ActorSlot:       strconv.FormatUint(uint64(facts.ActorSlot.Index()), 10),
		AdvertisedHost:  facts.Endpoint.Host(),
		AdvertisedPort:  strconv.FormatUint(uint64(facts.Endpoint.Port()), 10),
		IssueID:         facts.IssueID.Value(),
		IssuedAtUnixMS:  strconv.FormatInt(facts.IssuedAt.UnixMilli(), 10),
		ExpiresAtUnixMS: strconv.FormatInt(facts.ExpiresAt.UnixMilli(), 10),
	}
}

func lookupPayloadFrom(binding battleticket.Binding) lookupPayload {
	return lookupPayload{
		TicketID:             binding.TicketID().Value(),
		BindingFingerprint:   binding.Fingerprint().Hex(),
		SimulationInstanceID: binding.Facts().SimulationInstanceID.String(),
	}
}

func (registry *Registry) validateTarget(target simulationcontrol.SimulationTarget, binding battleticket.Binding) error {
	if registry.caller == nil || !registry.nodeID.Valid() || target.Validate() != nil ||
		!binding.Valid() || target.NodeID != registry.nodeID {
		return errors.New("battle ticket child target is invalid")
	}
	facts := binding.Facts()
	if facts.SimulationNodeID != target.NodeID ||
		facts.SimulationInstanceID != target.InstanceID ||
		facts.RuntimeNodeID != target.RuntimeNodeID ||
		facts.AssignmentFingerprint != target.AssignmentFingerprint ||
		facts.MappingGeneration != target.MappingGeneration ||
		facts.TargetRevision != target.Revision ||
		facts.ModelIdentity != target.ModelManifest ||
		facts.ProfileIdentity != target.ProfileManifest ||
		facts.ConfigIdentity != target.ConfigIdentity {
		return errors.New("battle ticket binding does not match exact child target")
	}
	return nil
}

func (registry *Registry) requestID(action string, binding battleticket.Binding) (simulationcontrol.RequestID, error) {
	material := "ihomeland/battle-ticket-control/v1|" + action + "|" +
		registry.nodeID.String() + "|" + binding.Facts().SimulationInstanceID.String() + "|" +
		binding.Facts().IssueID.Value() + "|" + binding.TicketID().Value() + "|" +
		binding.Fingerprint().Hex()
	digest := sha256.Sum256([]byte(material))
	return simulationcontrol.NewRequestID(
		"sctl_ticket_" + action + "_" + hex.EncodeToString(digest[:16]))
}

func (registry *Registry) decodeReceipt(raw json.RawMessage, target simulationcontrol.SimulationTarget, binding battleticket.Binding, requireInstalled bool) (battleentry.ChildTicketReceipt, error) {
	var receipt ticketReceipt
	if err := decodeClosed(raw, &receipt); err != nil {
		return battleentry.ChildTicketReceipt{}, err
	}
	if receipt.TicketID != binding.TicketID().Value() ||
		receipt.BindingFingerprint != binding.Fingerprint().Hex() ||
		receipt.SimulationInstanceID != target.InstanceID.String() {
		return battleentry.ChildTicketReceipt{}, errors.New("battle ticket child receipt binding drifted")
	}
	state, err := parseState(receipt.State)
	if err != nil || requireInstalled && state != battleentry.ChildTicketStateInstalled {
		return battleentry.ChildTicketReceipt{}, errors.New("battle ticket child receipt state is invalid")
	}
	if requireInstalled {
		requestID, requestErr := registry.requestID("install", binding)
		if requestErr != nil || receipt.InstallRequestID != requestID.String() {
			return battleentry.ChildTicketReceipt{}, errors.New("battle ticket install receipt request identity drifted")
		}
	} else if receipt.InstallRequestID != "" {
		return battleentry.ChildTicketReceipt{}, errors.New("battle ticket status receipt contains install identity")
	}
	slot := battleticket.ActorSlot{}
	if state == battleentry.ChildTicketStateMissing {
		if receipt.ActorSlot != nil {
			return battleentry.ChildTicketReceipt{}, errors.New("missing battle ticket receipt contains actor slot")
		}
	} else {
		if receipt.ActorSlot == nil {
			return battleentry.ChildTicketReceipt{}, errors.New("battle ticket receipt actor slot is missing")
		}
		value, parseErr := strconv.ParseUint(*receipt.ActorSlot, 10, 8)
		if parseErr != nil {
			return battleentry.ChildTicketReceipt{}, errors.New("battle ticket receipt actor slot is invalid")
		}
		slot, err = battleticket.NewActorSlot(uint8(value))
		if err != nil || slot != binding.Facts().ActorSlot {
			return battleentry.ChildTicketReceipt{}, errors.New("battle ticket receipt actor slot drifted")
		}
	}
	return battleentry.ChildTicketReceipt{
		TicketID: binding.TicketID(), BindingFingerprint: binding.Fingerprint(),
		Target: target, Slot: slot, State: state,
	}, nil
}

func parseState(value string) (battleentry.ChildTicketState, error) {
	switch value {
	case "installed":
		return battleentry.ChildTicketStateInstalled, nil
	case "consumed":
		return battleentry.ChildTicketStateConsumed, nil
	case "revoked":
		return battleentry.ChildTicketStateRevoked, nil
	case "expired":
		return battleentry.ChildTicketStateExpired, nil
	case "missing":
		return battleentry.ChildTicketStateMissing, nil
	default:
		return battleentry.ChildTicketStateUnspecified, fmt.Errorf("battle ticket child state is unknown")
	}
}

func decodeClosed(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode battle ticket child receipt: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("battle ticket child receipt has trailing data")
	}
	return nil
}

var _ battleentry.ChildTicketRegistry = (*Registry)(nil)
