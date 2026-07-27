package runner

import (
	"context"
	"errors"
	"math"
	"net/netip"
	"slices"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/correlation"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/gateevidence"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/gateway"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/manifest"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/measurement"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/protocolclient"
	"github.com/jinwiforz/ihomeland/server/internal/testclient"
)

const (
	// securityAttackerCount 为合法五人 workload 增加唯一隔离攻击 session/source。
	securityAttackerCount = 1
	// securityAttackSlot 紧随五个合法 actor，且仍位于 B0.5 八人 hard cap 内。
	securityAttackSlot = 6
	// pairedNegativeDeliveryCount 表示每个固定负例与一个合法对照共用同次 command。
	pairedNegativeDeliveryCount = 2
)

type securityAttackResult struct {
	attackDatagrams   uint64
	responseDatagrams uint64
	sessionTerminated bool
}

// RunSecurity 在独立第六 source 发起单个 tracked attack，并持续驱动合法五人 workload。
func RunSecurity(
	ctx context.Context,
	config Config,
	policy manifest.SecurityPolicy,
	caseID string,
) (_ gateevidence.Evidence, resultErr error) {
	stage := FailureStageValidation
	if err := config.validate(); err != nil ||
		policy.LegitimateActorCount != config.Definition.ActorCount ||
		!slices.Contains(policy.Cases, caseID) ||
		policy.MaximumDurationMilliseconds <= 0 ||
		policy.MaximumPacketsPerSecondSender <= 0 ||
		policy.MaximumSenders < securityAttackerCount ||
		policy.MaximumTotalBytes <= 0 {
		return gateevidence.Evidence{}, newStageError(
			stage,
			errors.New("battle security runner config is invalid"),
		)
	}
	if err := validateSecurityInventory(policy.Cases); err != nil {
		return gateevidence.Evidence{}, newStageError(stage, err)
	}
	securityClientCount :=
		policy.LegitimateActorCount + securityAttackerCount
	config.GatewayConfig.ClientCount = securityClientCount
	config.GatewayConfig.Scheduler.MaximumClients =
		securityClientCount
	owner := &lifecycle{
		metadataDone: make(chan struct{}),
		cleanupBudget: time.Duration(
			config.Definition.ExecutionPolicy.CleanupMilliseconds,
		) * time.Millisecond,
	}
	defer func() {
		if resultErr != nil {
			resultErr = newStageError(stage, errors.Join(resultErr, owner.Close()))
		}
	}()
	stage = FailureStageGateway
	networkGateway, err := gateway.New(config.GatewayConfig)
	if err != nil {
		return gateevidence.Evidence{}, err
	}
	owner.gateway = networkGateway
	if err := networkGateway.Start(ctx); err != nil {
		return gateevidence.Evidence{}, err
	}
	go owner.collectMetadata()

	stage = FailureStageAdmission
	admission, err := testclient.PrepareBattleAdmission(
		ctx,
		config.Runtime,
		testclient.BattleAdmissionPlan{
			MembershipCount:  securityAttackSlot,
			BattleActorCount: securityAttackSlot,
		},
	)
	if err != nil {
		return gateevidence.Evidence{}, err
	}
	owner.admission = admission
	if len(admission.Participants) != securityAttackSlot {
		return gateevidence.Evidence{}, errors.New("battle security admission count drifted")
	}
	endpoint, err := networkGateway.FrontendEndpoint(1)
	if err != nil {
		return gateevidence.Evidence{}, err
	}
	stage = FailureStageActorStart
	for _, participant := range admission.Participants[:policy.LegitimateActorCount] {
		actor, startErr := owner.startParticipant(ctx, config, endpoint, participant)
		if startErr != nil {
			return gateevidence.Evidence{}, startErr
		}
		owner.actors = append(owner.actors, actor)
	}
	legitimate := slices.Clone(owner.actors)
	attackParticipant := admission.Participants[securityAttackSlot-1]
	attacker, handshakeAttack, err := owner.startSecurityAttacker(
		ctx,
		config,
		endpoint,
		attackParticipant,
		caseID,
	)
	if err != nil {
		return gateevidence.Evidence{}, err
	}
	owner.actors = append(owner.actors, attacker)

	stage = FailureStageFaultActivation
	if err := networkGateway.ActivateImpairments(ctx); err != nil {
		return gateevidence.Evidence{}, err
	}
	stage = FailureStageStartMetrics
	attackStartMetrics, err := scrapeAfter(ctx, config, nil)
	if err != nil {
		return gateevidence.Evidence{}, err
	}
	measurementContext, cancelMeasurement :=
		context.WithCancel(ctx)
	defer cancelMeasurement()
	attackResults := make(chan struct {
		result securityAttackResult
		err    error
	}, 1)
	go func() {
		result, attackErr := executeSecurityAttack(
			measurementContext,
			attacker,
			handshakeAttack,
			caseID,
			policy,
		)
		attackResults <- struct {
			result securityAttackResult
			err    error
		}{result: result, err: attackErr}
	}()
	// BattleTicket 只有短寿命。攻击必须在合法 actor 已建立后立即发生，不能等
	// warmup 结束再消费同批签发的攻击凭据；warmup 本身已提供并发合法流量。
	stage = FailureStageWarmup
	phaseResults := make(chan error, 1)
	go func() {
		phaseResults <- runActorPhase(
			measurementContext,
			legitimate,
			correlation.PhaseClean,
			time.Duration(
				config.Definition.ExecutionPolicy.WarmupMilliseconds,
			)*time.Millisecond,
			phaseRecoveryPolicy{},
		)
	}()
	var attackResult struct {
		result securityAttackResult
		err    error
	}
	var warmupErr error
	warmupCompleted := false
	attackCompleted := false
	for !attackCompleted {
		select {
		case result := <-attackResults:
			attackResult = result
			attackCompleted = true
			if result.err != nil {
				cancelMeasurement()
			}
		case phaseErr := <-phaseResults:
			warmupErr = phaseErr
			warmupCompleted = true
			if phaseErr != nil &&
				!errors.Is(phaseErr, context.Canceled) {
				cancelMeasurement()
			}
		}
	}
	if attackResult.err != nil {
		if !warmupCompleted {
			<-phaseResults
		}
		return gateevidence.Evidence{}, newStageError(
			FailureStageSecurityAttack,
			attackResult.err,
		)
	}
	if warmupCompleted && warmupErr != nil &&
		!errors.Is(warmupErr, context.Canceled) {
		return gateevidence.Evidence{}, warmupErr
	}
	stage = FailureStageSecurityEvidence
	attackEndMetrics, err := scrapeAfter(
		ctx,
		config,
		&attackStartMetrics,
	)
	if err != nil {
		return gateevidence.Evidence{}, err
	}
	attackWindow := measurement.Window{
		Start: attackStartMetrics,
		End:   attackEndMetrics,
	}
	if err := attackWindow.Validate(); err != nil {
		return gateevidence.Evidence{}, err
	}
	rejected := attackWindow.End.Control["rejected-packets"] -
		attackWindow.Start.Control["rejected-packets"]
	if rejected == 0 ||
		rejected > attackResult.result.attackDatagrams ||
		attackResult.result.responseDatagrams >
			attackResult.result.attackDatagrams {
		return gateevidence.Evidence{}, errors.New("battle security rejection evidence drifted")
	}
	if !warmupCompleted {
		warmupErr = <-phaseResults
	}
	if warmupErr != nil &&
		!errors.Is(warmupErr, context.Canceled) {
		return gateevidence.Evidence{}, warmupErr
	}
	startClients := snapshotActors(legitimate)
	resetActorMeasurements(legitimate)
	stage = FailureStageStartMetrics
	startMetrics, err := scrapeAfter(ctx, config, &attackEndMetrics)
	if err != nil {
		return gateevidence.Evidence{}, err
	}
	stage = FailureStageMeasurement
	duration := min(
		time.Duration(config.Definition.ExecutionPolicy.MeasurementMilliseconds)*time.Millisecond,
		time.Duration(policy.MaximumDurationMilliseconds)*time.Millisecond,
	)
	if err := runActorPhase(
		ctx,
		legitimate,
		correlation.PhaseClean,
		duration,
		phaseRecoveryPolicy{},
	); err != nil {
		return gateevidence.Evidence{}, err
	}
	if attackResult.result.sessionTerminated {
		attacker.sessionActive = false
	}
	stage = FailureStageFaultQuiesce
	if err := networkGateway.QuiesceImpairments(ctx); err != nil {
		return gateevidence.Evidence{}, err
	}
	if err := awaitGatewayQuiescence(
		ctx,
		networkGateway,
		legitimate,
	); err != nil {
		return gateevidence.Evidence{}, err
	}
	stage = FailureStageEndMetrics
	endMetrics, err := scrapeAfter(ctx, config, &startMetrics)
	if err != nil {
		return gateevidence.Evidence{}, err
	}
	stage = FailureStageSecurityEvidence
	window := measurement.Window{Start: startMetrics, End: endMetrics}
	if err := window.Validate(); err != nil {
		return gateevidence.Evidence{}, err
	}
	stage = FailureStageClientEvidence
	if _, err := clientDeltas(startClients, snapshotActors(legitimate)); err != nil {
		return gateevidence.Evidence{}, err
	}
	stage = FailureStageCleanup
	if err := owner.Close(); err != nil {
		return gateevidence.Evidence{}, err
	}
	attackerBytes, err := clientWireBytes(owner.metadata, securityAttackSlot)
	if err != nil || attackerBytes > uint64(policy.MaximumTotalBytes) {
		return gateevidence.Evidence{}, errors.Join(
			err,
			errors.New("battle security byte budget exceeded"),
		)
	}
	return gateevidence.NewSecurity(gateevidence.SecurityInput{
		ScenarioID: "security-" + caseID,
		WorkloadID: config.Definition.WorkloadID,
		Assertions: []string{
			"attack-rejected",
			"bounded-amplification",
			"legitimate-flow-available",
		},
		AttackDatagrams:      attackResult.result.attackDatagrams,
		RejectedDatagrams:    rejected,
		ResponseDatagrams:    attackResult.result.responseDatagrams,
		LegitimateActorCount: policy.LegitimateActorCount,
	})
}

// validateSecurityInventory 保证 tracked case inventory 全部映射到 closed client fixture。
func validateSecurityInventory(cases []string) error {
	for _, caseID := range cases {
		if _, ok := handshakeAttackKind(caseID); ok || caseID == "old-epoch" {
			continue
		}
		if _, _, _, err := securityDelivery(caseID); err != nil {
			return err
		}
	}
	return nil
}

// clientWireBytes 汇总指定 run-local slot 的双向 datagram bytes，并拒绝算术溢出。
func clientWireBytes(metadata []gateway.Metadata, clientSlot uint8) (uint64, error) {
	var total uint64
	for _, packet := range metadata {
		if packet.ClientSlot != clientSlot {
			continue
		}
		if packet.LengthBytes < 0 {
			return 0, errors.New("battle security byte count is invalid")
		}
		length := uint64(packet.LengthBytes)
		if total > math.MaxUint64-length {
			return 0, errors.New("battle security byte count overflow")
		}
		total += length
	}
	return total, nil
}

// startSecurityAttacker 建立 active attacker，或把一次性 credential 交给 pre-session probe。
func (owner *lifecycle) startSecurityAttacker(
	ctx context.Context,
	config Config,
	endpoint netip.AddrPort,
	participant testclient.BattleParticipant,
	caseID string,
) (*actorOwner, *protocolclient.HandshakeAttack, error) {
	handshakeKind, isHandshake := handshakeAttackKind(caseID)
	if !isHandshake {
		actor, err := owner.startParticipant(ctx, config, endpoint, participant)
		return actor, nil, err
	}
	if participant.Slot != securityAttackSlot || participant.Ticket == nil ||
		participant.Ticket.ExpiresAtMS <= 0 {
		return nil, nil, errors.New("battle security handshake participant is invalid")
	}
	ticketAddress, err := netip.ParseAddr(participant.Ticket.Endpoint.Host)
	if err != nil {
		return nil, nil, errors.New("BattleTicket endpoint is not numeric")
	}
	if netip.AddrPortFrom(ticketAddress, participant.Ticket.Endpoint.Port) != endpoint {
		return nil, nil, errors.New("BattleTicket advertised endpoint drifted from gateway")
	}
	credential, err := participant.Ticket.TakeCredential()
	if err != nil {
		return nil, nil, err
	}
	if err := owner.gateway.RegisterTicket(
		ctx,
		securityAttackSlot,
		credential.TicketID,
	); err != nil {
		credential.Clear()
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		credential.Clear()
		return nil, nil, err
	}
	// Attack child 仍由 Supervisor 独占；此处 ctx 只约束启动 identity 校验。
	child, err := protocolclient.Start(ctx, config.ProtocolClientConfig)
	if err != nil {
		credential.Clear()
		return nil, nil, err
	}
	attack := &protocolclient.HandshakeAttack{
		Kind: handshakeKind,
		Start: protocolclient.SessionStart{
			ClientSlot: securityAttackSlot,
			Endpoint:   endpoint,
			Credential: &protocolclient.SessionCredential{
				TicketID:     credential.TicketID,
				TicketSecret: credential.TicketSecret,
			},
			TicketExpiresAtUnixMS: uint64(participant.Ticket.ExpiresAtMS),
		},
	}
	credential.Clear()
	return &actorOwner{
		slot: securityAttackSlot, supervisor: child, sessionActive: false,
	}, attack, nil
}

// executeSecurityAttack 只使用 closed local socket mutations，绝不接触 raw socket 或公网。
func executeSecurityAttack(
	ctx context.Context,
	attacker *actorOwner,
	handshakeAttack *protocolclient.HandshakeAttack,
	caseID string,
	policy manifest.SecurityPolicy,
) (securityAttackResult, error) {
	if attacker == nil || attacker.supervisor == nil {
		return securityAttackResult{}, errors.New("battle security attacker is unavailable")
	}
	if handshakeAttack != nil {
		receipt, err := attacker.supervisor.ProbeHandshakeAttack(ctx, *handshakeAttack)
		if err != nil {
			return securityAttackResult{}, err
		}
		return securityAttackResult{
			attackDatagrams:   uint64(receipt.AttackDatagrams),
			responseDatagrams: uint64(receipt.ResponseDatagrams),
			sessionTerminated: true,
		}, nil
	}
	if caseID == "old-epoch" {
		event, err := attacker.supervisor.Transition(
			ctx,
			protocolclient.NetworkTransition{
				ClientSlot: attacker.slot,
				Operation:  protocolclient.NetworkOldEpochProbe,
			},
		)
		if err != nil || !event.Committed || event.Generation == 0 {
			return securityAttackResult{}, errors.Join(
				err,
				errors.New("battle old epoch probe did not commit rekey"),
			)
		}
		return securityAttackResult{attackDatagrams: 1}, nil
	}
	delivery, operation, terminal, err := securityDelivery(caseID)
	if err != nil {
		return securityAttackResult{}, err
	}
	total := 1
	if caseID == "malformed-flood" || caseID == "rate-exhaustion" {
		total = policy.MaximumPacketsPerSecondSender / pairedNegativeDeliveryCount
		if total == 0 {
			return securityAttackResult{}, errors.New("battle security rate budget is too small")
		}
	}
	var sequence uint64 = 1
	var attacks uint64
	var wireDatagrams uint64
	for total > 0 {
		repeat := min(total, protocolclient.MaximumWorkloadRepeatCount)
		command := protocolclient.WorkloadCommand{
			ClientSlot:          attacker.slot,
			Operation:           operation,
			RepeatCount:         uint8(repeat),
			ApplicationSequence: sequence,
			ApplicationTick:     sequence,
			Delivery:            delivery,
		}
		if operation == protocolclient.WorkloadResyncRequest {
			command.ValueA = 1
		}
		event, sendErr := attacker.supervisor.SendWorkload(ctx, command)
		if sendErr != nil {
			return securityAttackResult{}, sendErr
		}
		if event.Count == 0 || sequence > math.MaxUint64-uint64(repeat) {
			return securityAttackResult{}, errors.New("battle security attack count overflow")
		}
		eventDatagrams := uint64(event.Count)
		rateLimit := uint64(policy.MaximumPacketsPerSecondSender)
		if eventDatagrams > rateLimit ||
			wireDatagrams > rateLimit-eventDatagrams {
			return securityAttackResult{}, errors.New("battle security packet rate budget exceeded")
		}
		wireDatagrams += eventDatagrams
		attacks += uint64(repeat)
		sequence += uint64(repeat)
		total -= repeat
	}
	return securityAttackResult{
		attackDatagrams: attacks, sessionTerminated: terminal,
	}, nil
}

func handshakeAttackKind(
	caseID string,
) (protocolclient.HandshakeAttackKind, bool) {
	switch caseID {
	case "cookie-less-amplification":
		return protocolclient.HandshakeCookieLess, true
	case "proof-forgery":
		return protocolclient.HandshakeProofForgery, true
	case "ticket-replay":
		return protocolclient.HandshakeTicketReplay, true
	case "spoofed-source":
		return protocolclient.HandshakeSpoofedSource, true
	default:
		return 0, false
	}
}

func securityDelivery(
	caseID string,
) (
	protocolclient.DeliveryMutation,
	protocolclient.WorkloadOperation,
	bool,
	error,
) {
	switch caseID {
	case "aad-tamper":
		return protocolclient.DeliveryAADTampered, protocolclient.WorkloadProbe, false, nil
	case "ciphertext-tamper":
		return protocolclient.DeliveryCiphertextTampered, protocolclient.WorkloadProbe, false, nil
	case "future-sequence":
		return protocolclient.DeliveryFutureSequence, protocolclient.WorkloadProbe, true, nil
	case "kcp-expiry":
		return protocolclient.DeliveryKCPExpired, protocolclient.WorkloadResyncRequest, true, nil
	case "malformed-flood":
		return protocolclient.DeliveryMalformedPrefix, protocolclient.WorkloadProbe, false, nil
	case "oversize-datagram":
		return protocolclient.DeliveryOversizePrefix, protocolclient.WorkloadProbe, false, nil
	case "rate-exhaustion":
		return protocolclient.DeliveryExactReplay, protocolclient.WorkloadProbe, true, nil
	case "rebind-hijack":
		return protocolclient.DeliveryRebindHijack, protocolclient.WorkloadProbe, false, nil
	case "replay-duplicate":
		return protocolclient.DeliveryExactReplay, protocolclient.WorkloadProbe, false, nil
	case "too-old-sequence":
		return protocolclient.DeliveryTooOldSequence, protocolclient.WorkloadProbe, false, nil
	case "wrong-direction":
		return protocolclient.DeliveryWrongDirection, protocolclient.WorkloadProbe, false, nil
	case "wrong-lane":
		return protocolclient.DeliveryWrongLane, protocolclient.WorkloadProbe, true, nil
	default:
		return 0, 0, false, errors.New("battle security case has no real mutation")
	}
}
