package runner

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/gateway"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/manifest"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/measurement"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/protocolclient"
)

// TestStageErrorPublishesOnlyClosedCode 验证诊断阶段可见但内部 cause 不会进入错误文本。
func TestStageErrorPublishesOnlyClosedCode(t *testing.T) {
	cause := errors.New("sensitive-endpoint-and-credential")
	failure := newStageError(FailureStageRebind, cause)
	var stageFailure *StageError
	if !errors.As(failure, &stageFailure) ||
		!errors.Is(failure, cause) ||
		stageFailure.QualificationFailureCode() != "rebind" ||
		stageFailure.Error() != "battle qualification stage failed" {
		t.Fatalf("stage failure contract drifted: %v", failure)
	}
	if wrappedAgain := newStageError(FailureStageCleanup, failure); wrappedAgain != failure {
		t.Fatal("原始失败阶段被 cleanup 覆盖")
	}
}

// TestStageErrorPreservesSecurityAttackCode 验证 measurement/cleanup 不覆盖攻击失败定位。
func TestStageErrorPreservesSecurityAttackCode(t *testing.T) {
	attackFailure := newStageError(
		FailureStageSecurityAttack,
		errors.New("private attack detail"),
	)
	wrapped := newStageError(FailureStageMeasurement, attackFailure)
	var failure *StageError
	if wrapped != attackFailure ||
		!errors.As(wrapped, &failure) ||
		failure.QualificationFailureCode() != "security-attack" {
		t.Fatal("安全攻击失败阶段被 measurement 覆盖")
	}
}

// TestSecurityEvidenceStageCode 验证安全计数失败不会继续伪装为 metrics 采样失败。
func TestSecurityEvidenceStageCode(t *testing.T) {
	failure := newStageError(
		FailureStageSecurityEvidence,
		errors.New("security evidence drifted"),
	)
	var stageFailure *StageError
	if !errors.As(failure, &stageFailure) ||
		stageFailure.QualificationFailureCode() !=
			string(FailureStageSecurityEvidence) {
		t.Fatal("安全证据失败阶段未稳定发布")
	}
}

// TestScenarioFailureEvidencePreservesClientKCPLayers 验证失败文件能区分 secure、ACK 与 reconciliation。
func TestScenarioFailureEvidencePreservesClientKCPLayers(t *testing.T) {
	cause := protocolclient.PollFailure{
		Code: protocolclient.PollFailureKCPInflightExpiry,
		Receipt: protocolclient.PollReceipt{
			ClientSlot:       2,
			SnapshotCount:    11,
			ReliableCount:    3,
			LatestServerTick: 20,
			KCP: protocolclient.ClientKCPState{
				CloseReason:        protocolclient.ClientKCPInflightExpired,
				SecureDatagrams:    9,
				InputDatagrams:     9,
				InputACKCommands:   7,
				InputPushCommands:  2,
				OutputDatagrams:    10,
				ReconciledMessages: 4,
				InflightMessages:   1,
				WaitingSegments:    1,
			},
		},
	}
	evidence, ok := NewScenarioFailureEvidence(
		manifest.Definition{
			ScenarioID:       "real-kcp-retransmit",
			SourceScenarioID: "kcp-retransmit",
			WorkloadID:       "default-coop",
			ActorCount:       5,
		},
		captureScenarioFailureState(
			errors.Join(errors.New("cleanup"), cause),
			[]gateway.Metadata{
				{
					Direction: gateway.DirectionUplink, ClientSlot: 2,
					PacketKind:  gateway.PublicPacketKCP,
					Disposition: gateway.DispositionDelivered,
				},
				{
					Direction: gateway.DirectionDownlink, ClientSlot: 2,
					PacketKind:  gateway.PublicPacketKCP,
					Disposition: gateway.DispositionBurst,
				},
				{
					Direction: gateway.DirectionDownlink, ClientSlot: 2,
					PacketKind:  gateway.PublicPacketKCP,
					Disposition: gateway.DispositionDelivered,
				},
			},
		),
	)
	if !ok ||
		evidence.Disposition != "failed" ||
		evidence.Failure.Stage != "client-poll-kcp-inflight-expiry" ||
		evidence.Failure.ClientSlot != 2 ||
		evidence.Failure.KCP.SecureDatagrams != 9 ||
		evidence.Failure.KCP.InputACKCommands != 7 ||
		evidence.Failure.KCP.ReconciledMessages != 4 ||
		evidence.Failure.KCP.InflightMessages != 1 ||
		evidence.Failure.GatewayKCP.UplinkDelivered != 1 ||
		evidence.Failure.GatewayKCP.DownlinkTerminal != 2 ||
		evidence.Failure.GatewayKCP.DownlinkDelivered != 1 ||
		len(evidence.Failure.GatewayKCP.Packets) != 3 {
		t.Fatalf("failure evidence=%+v available=%t", evidence, ok)
	}
}

// TestScenarioFailureEvidenceRequiresPostCleanupGatewayState 验证失败文件不会伪造缺失的 gateway 层。
func TestScenarioFailureEvidenceRequiresPostCleanupGatewayState(t *testing.T) {
	_, ok := NewScenarioFailureEvidence(
		manifest.Definition{ActorCount: 1},
		protocolclient.PollFailure{
			Code: protocolclient.PollFailureKCPInflightExpiry,
			Receipt: protocolclient.PollReceipt{
				ClientSlot: 1,
				KCP: protocolclient.ClientKCPState{
					CloseReason:      protocolclient.ClientKCPInflightExpired,
					InflightMessages: 1,
				},
			},
		},
	)
	if ok {
		t.Fatal("missing post-cleanup gateway state was accepted")
	}
}

// TestCleanupContextOwnsOneIndependentBudget 验证回收不继承场景取消，
// 且 close、关闭后采样与资源释放不能各自重置 deadline。
func TestCleanupContextOwnsOneIndependentBudget(t *testing.T) {
	const cleanupBudget = 200 * time.Millisecond
	owner := &lifecycle{cleanupBudget: cleanupBudget}
	first := owner.beginCleanup()
	second := owner.beginCleanup()
	if first != second {
		t.Fatal("cleanup stages received different contexts")
	}
	if err := first.Err(); err != nil {
		t.Fatalf("independent cleanup context started canceled: %v", err)
	}
	deadline, ok := first.Deadline()
	remaining := time.Until(deadline)
	if !ok || remaining <= cleanupBudget/2 || remaining > cleanupBudget {
		t.Fatalf("cleanup deadline=%v remaining=%s", deadline, remaining)
	}
	owner.cleanupCancel()
	if !errors.Is(first.Err(), context.Canceled) {
		t.Fatalf("cleanup cancellation was not propagated: %v", first.Err())
	}
}

// TestAwaitQuiescenceKeepsDraining 验证 barrier 期间持续 drain，完成后再执行最终一轮。
func TestAwaitQuiescenceKeepsDraining(t *testing.T) {
	release := make(chan struct{})
	var drains atomic.Uint32
	err := awaitQuiescenceWhileDraining(
		context.Background(),
		func(ctx context.Context) error {
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		func(context.Context) error {
			if drains.Add(1) == 2 {
				close(release)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("await quiescence: %v", err)
	}
	if got := drains.Load(); got < 3 {
		t.Fatalf("drain rounds=%d want at least 3", got)
	}
}

// TestAwaitQuiescenceCancelsOnDrainFailure 验证 client failure 终止 barrier goroutine。
func TestAwaitQuiescenceCancelsOnDrainFailure(t *testing.T) {
	drainFailure := errors.New("drain failed")
	awaitStopped := make(chan struct{})
	err := awaitQuiescenceWhileDraining(
		context.Background(),
		func(ctx context.Context) error {
			<-ctx.Done()
			close(awaitStopped)
			return ctx.Err()
		},
		func(context.Context) error {
			return drainFailure
		},
	)
	if !errors.Is(err, drainFailure) {
		t.Fatalf("drain failure=%v", err)
	}
	select {
	case <-awaitStopped:
	default:
		t.Fatal("await goroutine remained active")
	}
}

// TestSummarizeGatewayFiltersWindowAndSorts 验证 warmup 被排除且聚合顺序稳定。
func TestSummarizeGatewayFiltersWindowAndSorts(t *testing.T) {
	metadata := []gateway.Metadata{
		{
			Direction: gateway.DirectionUplink, ClientSlot: 2,
			LengthBytes: 20, PacketKind: gateway.PublicPacketRaw,
			ReceivedAt: 3 * time.Second, DueAt: 3*time.Second + time.Millisecond,
			DeliveredAt: 3*time.Second + 1500*time.Microsecond,
			Disposition: gateway.DispositionDelivered,
		},
		{
			Direction: gateway.DirectionDownlink, ClientSlot: 1,
			LengthBytes: 30, PacketKind: gateway.PublicPacketKCP,
			ReceivedAt: 2 * time.Second, DueAt: 2*time.Second + 2*time.Millisecond,
			Disposition: gateway.DispositionLoss,
		},
		{
			Direction: gateway.DirectionUplink, ClientSlot: 1,
			LengthBytes: 99, PacketKind: gateway.PublicPacketRaw,
			ReceivedAt: time.Second, DueAt: time.Second,
			Disposition: gateway.DispositionDelivered,
		},
	}
	summary, err := summarizeGateway(metadata, 2*time.Second, 4*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Packets) != 2 || summary.MaximumScheduledAgeUS != 2000 ||
		summary.MaximumDeliveryAgeUS != 1500 ||
		summary.Packets[0].Direction != "downlink" ||
		summary.Packets[1].ClientSlot != 2 {
		t.Fatalf("gateway summary=%+v", summary)
	}
}

// TestClientDeltasRequiresBaselineProgress 验证 snapshot、Tick、baseline 与 input ack 缺一不可。
func TestClientDeltasRequiresBaselineProgress(t *testing.T) {
	start := []actorState{{snapshotCount: 1, latestServerTick: 2}}
	end := []actorState{{
		sentDatagrams: 3, snapshotCount: 2, latestServerTick: 3,
		latestSnapshotSequence: 4, baselineID: 5, lastProcessedInputTick: 1,
		maximumSnapshotGapUS: 100,
	}}
	evidence, err := clientDeltas(start, end)
	if err != nil || len(evidence) != 1 || evidence[0].SnapshotCount != 1 {
		t.Fatalf("client evidence=%+v err=%v", evidence, err)
	}
	end[0].baselineID = 0
	if _, err := clientDeltas(start, end); err == nil {
		t.Fatal("missing baseline was accepted")
	}
}

// TestNextResyncTriggerUsesProfileRateAndExactRecoveryOwner 验证 seed 不冒充恢复起点，
// decoder gap 在 2/s route rate 到期后优先于周期 KCP workload。
func TestNextResyncTriggerUsesProfileRateAndExactRecoveryOwner(t *testing.T) {
	if resyncCadence != 500*time.Millisecond {
		t.Fatalf("resync cadence=%s want 500ms", resyncCadence)
	}
	policy := phaseRecoveryPolicy{
		baselineGapDriven: true,
		periodicKCP:       true,
	}
	actor := actorOwner{state: actorState{
		latestServerTick: 1,
		step:             1,
	}}
	sentAt := time.Unix(1, 0)
	if trigger := actor.nextResyncTrigger(
		policy,
		sentAt,
	); trigger != resyncTriggerGapSeed {
		t.Fatalf("initial trigger=%d want gap seed", trigger)
	}
	actor.state.baselineID = 7
	actor.commitResyncTrigger(resyncTriggerGapSeed, sentAt)
	if !actor.state.recoveryStartedAt.IsZero() ||
		actor.state.recoveryBaselineID != 0 {
		t.Fatal("fault seed started baseline recovery measurement")
	}

	actor.state.resyncSequence = 1
	actor.state.baselineGapCount = 1
	if trigger := actor.nextResyncTrigger(
		policy,
		sentAt.Add(resyncCadence-time.Nanosecond),
	); trigger != resyncTriggerNone {
		t.Fatalf("early trigger=%d want none", trigger)
	}

	eligibleAt := sentAt.Add(resyncCadence)
	if trigger := actor.nextResyncTrigger(
		policy,
		eligibleAt,
	); trigger != resyncTriggerGapRecovery {
		t.Fatalf("eligible trigger=%d want gap recovery", trigger)
	}
	actor.commitResyncTrigger(resyncTriggerGapRecovery, eligibleAt)
	if actor.state.recoveryStartedAt != eligibleAt ||
		actor.state.recoveryBaselineID != 7 {
		t.Fatalf(
			"recovery measurement start=%v baseline=%d",
			actor.state.recoveryStartedAt,
			actor.state.recoveryBaselineID,
		)
	}

	actor.state.handledBaselineGapCount = 1
	if trigger := actor.nextResyncTrigger(
		policy,
		eligibleAt.Add(resyncCadence),
	); trigger != resyncTriggerGapRecovery {
		t.Fatalf("active recovery trigger=%d want gap recovery", trigger)
	}

	actor.state.recoveryStartedAt = time.Time{}
	actor.state.recoveryBaselineID = 0
	if trigger := actor.nextResyncTrigger(
		policy,
		eligibleAt.Add(resyncCadence),
	); trigger != resyncTriggerPeriodic {
		t.Fatalf("periodic trigger=%d want periodic", trigger)
	}
}

// TestObservePollReceiptDoesNotStartStaleRecovery 验证同一 poll 已建立 successor
// baseline 时，累计 gap 不会在 poll 返回后重新触发过期 resync。
func TestObservePollReceiptDoesNotStartStaleRecovery(t *testing.T) {
	policy := phaseRecoveryPolicy{baselineGapDriven: true}
	actor := actorOwner{
		slot: 3,
		state: actorState{
			snapshotCount:           4,
			latestServerTick:        10,
			latestSnapshotSequence:  4,
			baselineID:              7,
			baselineGapCount:        2,
			handledBaselineGapCount: 2,
			step:                    1,
		},
	}
	err := actor.observePollReceipt(protocolclient.PollReceipt{
		ClientSlot:             3,
		SnapshotCount:          5,
		LatestServerTick:       11,
		LatestSnapshotSequence: 5,
		BaselineID:             8,
		BaselineGapCount:       3,
		LastProcessedInputTick: 1,
	})
	if err != nil {
		t.Fatalf("observe poll receipt: %v", err)
	}
	if actor.state.handledBaselineGapCount != 3 {
		t.Fatalf(
			"handled baseline gap count=%d want 3",
			actor.state.handledBaselineGapCount,
		)
	}
	observedAt := time.Unix(1, 0)
	if trigger := actor.nextResyncTrigger(
		policy,
		observedAt,
	); trigger != resyncTriggerGapSeed {
		t.Fatalf("trigger=%d want only initial fault seed", trigger)
	}

	actor.state.resyncSequence = 1
	if trigger := actor.nextResyncTrigger(
		policy,
		observedAt,
	); trigger != resyncTriggerNone {
		t.Fatalf("trigger=%d want none after terminal poll recovery", trigger)
	}

	err = actor.observePollReceipt(protocolclient.PollReceipt{
		ClientSlot:             3,
		SnapshotCount:          5,
		LatestServerTick:       11,
		LatestSnapshotSequence: 5,
		BaselineID:             8,
		BaselineGapCount:       4,
		LastProcessedInputTick: 1,
	})
	if err != nil {
		t.Fatalf("observe unresolved poll receipt: %v", err)
	}
	if trigger := actor.nextResyncTrigger(
		policy,
		observedAt,
	); trigger != resyncTriggerGapRecovery {
		t.Fatalf("trigger=%d want recovery for unresolved gap", trigger)
	}
}

// TestNewerMetricsSnapshotAndCrossCheck 验证四个 source 必须同时推进并满足 packet 守恒。
func TestNewerMetricsSnapshotAndCrossCheck(t *testing.T) {
	start := measurement.Snapshot{
		Control: map[string]uint64{
			"sample-sequence": 1, "raw-ingress-packets": 10,
			"kcp-ingress-packets": 2, "raw-egress-packets": 20,
			"kcp-egress-packets": 3,
		},
		Processes: map[string]map[string]uint64{
			"go-parent": {"sequence": 1},
			"cpp-child": {"sequence": 1},
		},
	}
	end := measurement.Snapshot{
		Control: map[string]uint64{
			"sample-sequence": 2, "raw-ingress-packets": 12,
			"kcp-ingress-packets": 3, "raw-egress-packets": 22,
			"kcp-egress-packets": 4,
		},
		Processes: map[string]map[string]uint64{
			"go-parent": {"sequence": 2},
			"cpp-child": {"sequence": 2},
		},
	}
	if !newerMetricsSnapshot(end, &start) {
		t.Fatal("newer four-source snapshot was rejected")
	}
	evidence := GatewayEvidence{Packets: []PacketEvidence{
		{Direction: "uplink", Disposition: "delivered", Count: 3},
		{Direction: "downlink", Disposition: "delivered", Count: 2},
		{Direction: "downlink", Disposition: "dropped", Count: 1},
	}}
	if err := crossCheck(
		evidence,
		[]ClientEvidence{{ClientSlot: 1, SnapshotCount: 1}},
		measurement.Window{Start: start, End: end},
	); err != nil {
		t.Fatal(err)
	}
	evidence.Packets[0].Count = 2
	err := crossCheck(
		evidence,
		[]ClientEvidence{{ClientSlot: 1, SnapshotCount: 1}},
		measurement.Window{Start: start, End: end},
	)
	var failure *StageError
	if !errors.As(err, &failure) ||
		failure.QualificationFailureCode() != "cross-check-uplink" {
		t.Fatalf("uplink conservation failure=%v", err)
	}
	evidence.Packets[0].Count = 3
	evidence.Packets[2].Count = 0
	err = crossCheck(
		evidence,
		[]ClientEvidence{{ClientSlot: 1, SnapshotCount: 1}},
		measurement.Window{Start: start, End: end},
	)
	failure = nil
	if !errors.As(err, &failure) ||
		failure.QualificationFailureCode() != "cross-check-downlink" {
		t.Fatalf("downlink conservation failure=%v", err)
	}
}

// TestNewClosureEvidenceRequiresNormalCloseAndZeroRuntime 验证双源关闭与运行态归零缺一不可。
func TestNewClosureEvidenceRequiresNormalCloseAndZeroRuntime(t *testing.T) {
	start := measurement.Snapshot{Control: map[string]uint64{
		"close-normal": 4, "close-authentication": 1,
		"close-timeout": 2, "close-resource": 3,
		"close-lifecycle": 4, "close-transport": 5, "close-internal": 6,
	}}
	end := measurement.Snapshot{Control: map[string]uint64{
		"close-normal": 9, "close-authentication": 1,
		"close-timeout": 2, "close-resource": 3,
		"close-lifecycle": 4, "close-transport": 5, "close-internal": 6,
		"active-session-count": 0, "installed-ticket-count": 0,
	}}
	evidence, err := newClosureEvidence(start, end, 5, 5)
	if err != nil ||
		evidence.ClientClosedSessions != 5 ||
		evidence.ControlNormalCloseReasons != 5 ||
		evidence.ControlUnexpectedCloseReasons != 0 {
		t.Fatalf("closure evidence=%+v err=%v", evidence, err)
	}
	end.Control["close-timeout"]++
	if _, err := newClosureEvidence(start, end, 5, 5); err == nil {
		t.Fatal("unexpected close reason was accepted")
	}
	end.Control["close-timeout"]--
	end.Control["active-session-count"] = 1
	if _, err := newClosureEvidence(start, end, 5, 5); err == nil {
		t.Fatal("remaining active session was accepted")
	}
}

// TestValidateTransitionEvidenceScopesMandatoryRekey 验证普通场景不误判自动 rekey。
func TestValidateTransitionEvidenceScopesMandatoryRekey(t *testing.T) {
	if err := validateRebindEvidence(5, 5, 5); err != nil {
		t.Fatal(err)
	}
	if err := validateRebindEvidence(0, 0, 0); err != nil {
		t.Fatalf("non-rebind scenario was rejected: %v", err)
	}
	if err := validateRebindEvidence(0, 1, 0); err == nil {
		t.Fatal("unpaired rebind evidence was accepted")
	}
	if stage := classifyRebindEvidence(5, 4, 5); stage != FailureStageRebindControlEvidence {
		t.Fatalf("rebind stage=%s", stage)
	}
	if err := validateRekeyEvidence(0, 3, 0); err != nil {
		t.Fatalf("non-mandatory automatic rekey was rejected: %v", err)
	}
	if err := validateRekeyEvidence(9, 10, 10); err == nil {
		t.Fatal("incomplete mandatory client rekey was accepted")
	}
	if err := validateRekeyEvidence(10, 10, 10); err != nil {
		t.Fatal(err)
	}
}

// TestClientWireBytesScopesSlotAndRejectsInvalidLength 验证 byte hard cap 只统计指定 source。
func TestClientWireBytesScopesSlotAndRejectsInvalidLength(t *testing.T) {
	metadata := []gateway.Metadata{
		{ClientSlot: securityAttackSlot, LengthBytes: 1200},
		{ClientSlot: 1, LengthBytes: 900},
		{ClientSlot: securityAttackSlot, LengthBytes: 48},
	}
	total, err := clientWireBytes(metadata, securityAttackSlot)
	if err != nil || total != 1248 {
		t.Fatalf("clientWireBytes total=%d err=%v", total, err)
	}
	metadata[2].LengthBytes = -1
	if _, err := clientWireBytes(metadata, securityAttackSlot); err == nil {
		t.Fatal("negative metadata length was accepted")
	}
}

// TestValidateSecurityInventoryRequiresClosedMapping 验证 17 个 tracked case 均有固定实现。
func TestValidateSecurityInventoryRequiresClosedMapping(t *testing.T) {
	cases := []string{
		"aad-tamper",
		"ciphertext-tamper",
		"cookie-less-amplification",
		"future-sequence",
		"kcp-expiry",
		"malformed-flood",
		"old-epoch",
		"oversize-datagram",
		"proof-forgery",
		"rate-exhaustion",
		"rebind-hijack",
		"replay-duplicate",
		"spoofed-source",
		"ticket-replay",
		"too-old-sequence",
		"wrong-direction",
		"wrong-lane",
	}
	if err := validateSecurityInventory(cases); err != nil {
		t.Fatal(err)
	}
	cases[len(cases)-1] = "unregistered-case"
	if err := validateSecurityInventory(cases); err == nil {
		t.Fatal("unmapped security case was accepted")
	}
}

// TestGatewayTransitionEvidenceRequiresSameSessionAndPredecessorRejection 验证 rebind 不替换 session。
func TestGatewayTransitionEvidenceRequiresSameSessionAndPredecessorRejection(t *testing.T) {
	metadata := []gateway.Metadata{
		{ClientSlot: 1, MappingGeneration: 1, Correlation: "session-digest"},
		{ClientSlot: 1, MappingGeneration: 2, Correlation: "session-digest"},
	}
	start := measurement.Snapshot{
		Control: map[string]uint64{"rejected-packets": 3},
	}
	end := measurement.Snapshot{
		Control: map[string]uint64{"rejected-packets": 4},
	}
	predecessor, successor, rejected := gatewayTransitionEvidence(
		metadata,
		1,
		"valid-endpoint-rebind",
		start,
		end,
	)
	if !rejected || len(predecessor) == 0 ||
		!slices.Equal(predecessor, successor) {
		t.Fatalf("gateway transition evidence was incomplete")
	}
	metadata[1].Correlation = "replacement-session"
	if _, _, rejected := gatewayTransitionEvidence(
		metadata,
		1,
		"valid-endpoint-rebind",
		start,
		end,
	); rejected {
		t.Fatal("rebind accepted a replacement session")
	}
}
