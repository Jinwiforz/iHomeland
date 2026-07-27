package runner

import (
	"context"
	"encoding/binary"
	"errors"
	"slices"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/correlation"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/gateevidence"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/gateway"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/measurement"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/protocolclient"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/workload"
	"github.com/jinwiforz/ihomeland/server/internal/testclient"
)

// RunLifecycle 通过公开协议和封闭 process checkpoint 生成生命周期证据。
func RunLifecycle(
	ctx context.Context,
	runtime *testclient.ScenarioRuntime,
	caseID string,
) (gateevidence.Evidence, error) {
	if ctx == nil || runtime == nil || runtime.HTTP == nil {
		return gateevidence.Evidence{}, errors.New("battle lifecycle runtime is incomplete")
	}
	recorder := &testclient.LifecycleRecorder{}
	scenarioRuntime := *runtime
	scenarioRuntime.Lifecycle = recorder
	if err := testclient.RunLifecycleScenario(
		ctx,
		caseID,
		&scenarioRuntime,
	); err != nil {
		return gateevidence.Evidence{}, err
	}
	observation, err := recorder.Observation()
	if err != nil {
		return gateevidence.Evidence{}, err
	}
	defer observation.Clear()
	mode := gateevidence.TransitionReplaced
	assertions := []string{
		"predecessor-rejected",
		"successor-current",
	}
	if observation.Terminated {
		mode = gateevidence.TransitionTerminated
		assertions = []string{
			"predecessor-rejected",
			"terminal-state-stable",
		}
	}
	return gateevidence.NewLifecycle(gateevidence.LifecycleInput{
		ScenarioID:            "lifecycle-" + caseID,
		WorkloadID:            "default-coop",
		Assertions:            assertions,
		Mode:                  mode,
		PredecessorProjection: observation.PredecessorProjection,
		SuccessorProjection:   observation.SuccessorProjection,
		PredecessorRejected:   observation.PredecessorRejected,
	})
}

// RunGatewayLifecycle 经 opaque loopback gateway 执行 pause/resume 或 endpoint rebind。
func RunGatewayLifecycle(
	ctx context.Context,
	config Config,
	caseID string,
) (_ gateevidence.Evidence, resultErr error) {
	if err := config.validate(); err != nil ||
		(caseID != "network-pause-resume" &&
			caseID != "valid-endpoint-rebind") {
		return gateevidence.Evidence{}, errors.New("battle gateway lifecycle config is invalid")
	}
	owner, err := startLifecycleActors(ctx, config)
	if err != nil {
		return gateevidence.Evidence{}, err
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, owner.Close())
		}
	}()
	warmup := time.Duration(
		config.Definition.ExecutionPolicy.WarmupMilliseconds,
	) * time.Millisecond
	if err := runActorPhase(
		ctx,
		owner.actors,
		correlation.PhaseClean,
		warmup,
		phaseRecoveryPolicy{},
	); err != nil {
		return gateevidence.Evidence{}, err
	}
	if err := owner.gateway.ActivateImpairments(ctx); err != nil {
		return gateevidence.Evidence{}, err
	}
	startMetrics, err := scrapeAfter(ctx, config, nil)
	if err != nil {
		return gateevidence.Evidence{}, err
	}
	target := owner.actors[0]
	switch caseID {
	case "network-pause-resume":
		if err := owner.gateway.SetPaused(ctx, gateway.DirectionUplink, true); err != nil {
			return gateevidence.Evidence{}, err
		}
		if err := sendLifecycleProbe(ctx, target); err != nil {
			return gateevidence.Evidence{}, err
		}
		if err := owner.gateway.SetPaused(ctx, gateway.DirectionUplink, false); err != nil {
			return gateevidence.Evidence{}, err
		}
	case "valid-endpoint-rebind":
		if err := sendLifecycleProbe(ctx, target); err != nil {
			return gateevidence.Evidence{}, err
		}
		rotation, rotateErr := owner.gateway.RotateMappingEndpoint(ctx, target.slot)
		if rotateErr != nil {
			return gateevidence.Evidence{}, rotateErr
		}
		event, transitionErr := target.supervisor.Transition(
			ctx,
			protocolclient.NetworkTransition{
				ClientSlot:         target.slot,
				Operation:          protocolclient.NetworkRebind,
				AdvertisedEndpoint: rotation.Endpoint,
			},
		)
		if transitionErr != nil || !event.Committed ||
			event.Generation != rotation.Generation {
			return gateevidence.Evidence{}, errors.Join(
				transitionErr,
				errors.New("battle lifecycle rebind did not commit successor"),
			)
		}
	}
	duration := time.Duration(
		config.Definition.ExecutionPolicy.MeasurementMilliseconds,
	) * time.Millisecond
	if err := runActorPhase(
		ctx,
		owner.actors,
		correlation.PhaseClean,
		duration,
		phaseRecoveryPolicy{},
	); err != nil {
		return gateevidence.Evidence{}, err
	}
	if err := owner.gateway.QuiesceImpairments(ctx); err != nil {
		return gateevidence.Evidence{}, err
	}
	if err := awaitGatewayQuiescence(
		ctx,
		owner.gateway,
		owner.actors,
	); err != nil {
		return gateevidence.Evidence{}, err
	}
	endMetrics, err := scrapeAfter(ctx, config, &startMetrics)
	if err != nil {
		return gateevidence.Evidence{}, err
	}
	if err := (measurement.Window{Start: startMetrics, End: endMetrics}).Validate(); err != nil {
		return gateevidence.Evidence{}, err
	}
	if err := owner.Close(); err != nil {
		return gateevidence.Evidence{}, err
	}
	predecessor, successor, rejected := gatewayTransitionEvidence(
		owner.metadata,
		target.slot,
		caseID,
		startMetrics,
		endMetrics,
	)
	if !rejected {
		return gateevidence.Evidence{}, errors.New("battle lifecycle predecessor was not rejected")
	}
	return gateevidence.NewLifecycle(gateevidence.LifecycleInput{
		ScenarioID:            "lifecycle-" + caseID,
		WorkloadID:            config.Definition.WorkloadID,
		Assertions:            []string{"legitimate-flow-available", "predecessor-rejected"},
		Mode:                  gateevidence.TransitionPreserved,
		PredecessorProjection: predecessor,
		SuccessorProjection:   successor,
		PredecessorRejected:   true,
	})
}

// startLifecycleActors 建立 gateway、公开 admission 与全部独立 client owner。
func startLifecycleActors(ctx context.Context, config Config) (_ *lifecycle, resultErr error) {
	owner := &lifecycle{
		metadataDone: make(chan struct{}),
		cleanupBudget: time.Duration(
			config.Definition.ExecutionPolicy.CleanupMilliseconds,
		) * time.Millisecond,
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, owner.Close())
		}
	}()
	networkGateway, err := gateway.New(config.GatewayConfig)
	if err != nil {
		return nil, err
	}
	owner.gateway = networkGateway
	if err := networkGateway.Start(ctx); err != nil {
		return nil, err
	}
	go owner.collectMetadata()
	admission, err := testclient.PrepareBattleAdmission(
		ctx,
		config.Runtime,
		testclient.BattleAdmissionPlan{
			MembershipCount:  int(config.Definition.ActorCount),
			BattleActorCount: int(config.Definition.ActorCount),
		},
	)
	if err != nil {
		return nil, err
	}
	owner.admission = admission
	if err := owner.startActors(ctx, config); err != nil {
		return nil, err
	}
	return owner, nil
}

// sendLifecycleProbe 发送由既有 driver 分配 sequence 的单个固定 probe。
func sendLifecycleProbe(ctx context.Context, actor *actorOwner) error {
	if actor == nil || actor.driver == nil ||
		actor.state.latestSnapshotSequence == 0 {
		return errors.New("battle lifecycle actor has no current snapshot")
	}
	actor.state.step++
	probe := actor.driver.NextProbe(workload.Snapshot{
		MappingGeneration:      1,
		LatestServerTick:       actor.state.latestServerTick,
		LatestSnapshotSequence: actor.state.latestSnapshotSequence,
		LastProcessedInputTick: actor.state.lastProcessedInputTick,
	}, uint64(actor.state.step)*uint64(workload.InputCadence/time.Microsecond))
	if probe == nil {
		return errors.New("battle lifecycle probe is unavailable")
	}
	event, err := actor.supervisor.SendWorkload(
		ctx,
		protocolclient.WorkloadCommand{
			ClientSlot:          actor.slot,
			Operation:           protocolclient.WorkloadProbe,
			RepeatCount:         1,
			ApplicationSequence: probe.GetProbeSequence(),
			ApplicationTick:     probe.GetLatestSnapshotSequence(),
		},
	)
	if err != nil || event.ClientSlot != actor.slot ||
		event.Kind != protocolclient.WorkloadDatagramSent ||
		event.Count != 1 {
		return errors.Join(err, errors.New("battle lifecycle probe receipt drifted"))
	}
	actor.state.sentDatagrams++
	return nil
}

// gatewayTransitionEvidence 解析 mapping metadata，并用 control reject 证明旧流量未推进。
func gatewayTransitionEvidence(
	metadata []gateway.Metadata,
	clientSlot uint8,
	caseID string,
	startMetrics measurement.Snapshot,
	endMetrics measurement.Snapshot,
) ([]byte, []byte, bool) {
	var predecessor string
	var successor string
	paused := false
	for _, packet := range metadata {
		if packet.ClientSlot != clientSlot {
			continue
		}
		if packet.Correlation != "" {
			if packet.MappingGeneration == 1 {
				predecessor = packet.Correlation
			}
			if packet.MappingGeneration > 1 {
				successor = packet.Correlation
			}
		}
		paused = paused || packet.Disposition == gateway.DispositionPaused
	}
	if caseID == "network-pause-resume" {
		successor = predecessor
	}
	startRejected := startMetrics.Control["rejected-packets"]
	endRejected := endMetrics.Control["rejected-packets"]
	rejected := paused
	if caseID == "valid-endpoint-rebind" {
		rejected = endRejected > startRejected
	}
	if predecessor == "" || successor == "" {
		return nil, nil, false
	}
	predecessorProjection := lifecycleCorrelationProjection(clientSlot, predecessor)
	successorProjection := lifecycleCorrelationProjection(clientSlot, successor)
	if !slices.Equal(predecessorProjection, successorProjection) {
		return nil, nil, false
	}
	return predecessorProjection, successorProjection, rejected
}

// lifecycleCorrelationProjection 将 run-local slot 与公开 session digest 编为无歧义 bytes。
func lifecycleCorrelationProjection(clientSlot uint8, correlationDigest string) []byte {
	projection := make([]byte, 5+len(correlationDigest))
	projection[0] = clientSlot
	binary.BigEndian.PutUint32(projection[1:5], uint32(len(correlationDigest)))
	copy(projection[5:], correlationDigest)
	return projection
}
