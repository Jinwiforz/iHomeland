package process

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/protocolclient"
	"github.com/jinwiforz/ihomeland/server/internal/battleticket"
	"github.com/jinwiforz/ihomeland/server/internal/battleticketcontrol"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/simulationcontrol"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
)

const (
	realGameplayConfigIdentity     = "d6e3f016e4c29f4ad3916759e5ea059443fe37145dbe51621704180e50bc555b"
	realGameplayNavigationIdentity = "14165efe5b47caf6c7c8e2f9a4794c4d238aa4badeedc3d5badb41a9c2d5d19f"
	realGameplayPhysicsIdentity    = "64d53e1803d7cb14f4953ba1978175b162577eeb064716b2630cad0acf4de02e"
	realGameplayWireIdentity       = "9a40facbb23aafc556d38b403c8f8b1e264e0f2d9414e32da434b11d07554432"
	realGameplayMappingIdentity    = "9e78652d1d05524c2a67668b208f703120a214bfb45f053c94f6c3dc79595e3b"
)

// TestRealChildBattleUDPReadyBeforeHello 验证真实 Go parent 只有在 child 已绑定唯一 UDP socket 后才收到 hello。
func TestRealChildBattleUDPReadyBeforeHello(t *testing.T) {
	if os.Getenv("IHOMELAND_SIMULATION_REAL_CHILD") != "1" {
		t.Skip("real simulation child harness is required")
	}
	repositoryRoot, _ := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	binaryPath := filepath.Join(repositoryRoot, "simulation", "out", "build", "windows-msvc-ci", "ihomeland-sim-server.exe")
	receiptPath := filepath.Join(repositoryRoot, "simulation", "out", "build", "windows-msvc-ci", "qualification-gate-receipt.json")
	probe, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.LocalAddr().(*net.UDPAddr).Port
	_ = probe.Close()
	nonce, _ := simulationcontrol.NewSessionNonce()
	diagnostics := &diagnosticCollector{}
	owner, err := Start(realProcessConfig(t, Config{
		BinaryPath: binaryPath, BinarySHA256: fileDigest(t, binaryPath),
		QualificationReceiptPath: receiptPath, QualificationReceiptSHA256: fileDigest(t, receiptPath),
		RequestTimeout: 3 * time.Second, ShutdownTimeout: 3 * time.Second, StderrLineLimit: 1024,
		BattleUDPEnabled: true, BattleUDPBindHost: "127.0.0.1", BattleUDPBindPort: uint16(port),
		BattleUDPAdvertisedHost: "127.0.0.1", BattleUDPAdvertisedPort: uint16(port),
		BattleListenerIdentity: "0123456789abcdef0123456789abcdef",
	}), nonce, simulationcontrol.NewProposalInbox(), diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Terminate(context.Background()) })
	controller, err := simulationcontrol.BootstrapController(context.Background(), owner.Session(), realControllerConfig(t))
	if err != nil {
		t.Fatalf("bootstrap: %v diagnostics=%#v child=%v", err, diagnostics.snapshot(), owner.Err())
	}
	conflict, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err == nil {
		_ = conflict.Close()
		t.Fatal("hello receipt arrived before battle UDP listener owned the configured port")
	}
	if err := controller.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// TestRealChildProtocolClientSessionStart 验证两个真实 child 通过唯一 UDP listener 完成握手。
func TestRealChildProtocolClientSessionStart(t *testing.T) {
	if os.Getenv("IHOMELAND_SIMULATION_REAL_CHILD") != "1" {
		t.Skip("real simulation child harness is required")
	}
	binaryPath, receiptPath := realArtifactPaths(t)
	clientPath := filepath.Join(
		filepath.Dir(binaryPath),
		"ihomeland-battle-protocol-client.exe",
	)
	probe, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(probe.LocalAddr().(*net.UDPAddr).Port)
	_ = probe.Close()
	nonce, err := simulationcontrol.NewSessionNonce()
	if err != nil {
		t.Fatal(err)
	}
	qualificationRunID, err := simulationcontrol.NewQualificationRunID(
		"bqrun_" + nonce.String()[:32],
	)
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := &diagnosticCollector{}
	owner, err := Start(
		realProcessConfig(t, Config{
			BinaryPath:                 binaryPath,
			BinarySHA256:               fileDigest(t, binaryPath),
			QualificationReceiptPath:   receiptPath,
			QualificationReceiptSHA256: fileDigest(t, receiptPath),
			RequestTimeout:             3 * time.Second,
			ShutdownTimeout:            3 * time.Second,
			StderrLineLimit:            1024,
			BattleUDPEnabled:           true,
			BattleUDPBindHost:          "127.0.0.1",
			BattleUDPBindPort:          port,
			BattleUDPAdvertisedHost:    "127.0.0.1",
			BattleUDPAdvertisedPort:    port,
			BattleListenerIdentity:     "0123456789abcdef0123456789abcdef",
			QualificationRunID:         qualificationRunID,
			QualificationMode:          true,
		}),
		nonce,
		simulationcontrol.NewProposalInbox(),
		diagnostics,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Terminate(context.Background()) })
	controllerConfig := realControllerConfig(t)
	controllerConfig.QualificationRunID = qualificationRunID
	controllerConfig.QualificationMode = true
	controller, err := simulationcontrol.BootstrapController(
		context.Background(),
		owner.Session(),
		controllerConfig,
	)
	if err != nil {
		t.Fatalf("bootstrap: %v diagnostics=%#v", err, diagnostics.snapshot())
	}
	snapshot := realStartingSnapshot(t)
	if err := controller.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("start instance: %v", err)
	}
	target, found := controller.ResolveTarget(snapshot.Stamp())
	if !found || target.Validate() != nil {
		t.Fatalf("resolve target: found=%v target=%+v", found, target)
	}

	playerID, err := account.NewPlayerID("ply_realprotocolclient")
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := session.NewSessionID("ses_realprotocolclient")
	if err != nil {
		t.Fatal(err)
	}
	actorSlot, err := battleticket.NewActorSlot(0)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := battleticket.NewEndpoint("127.0.0.1", port)
	if err != nil {
		t.Fatal(err)
	}
	issueID, err := battleticket.NewIssueID("biss_real_protocol_client_0001")
	if err != nil {
		t.Fatal(err)
	}
	wireIdentity, err := battleticket.ParseDigestHex(strings.Repeat("1", 64))
	if err != nil {
		t.Fatal(err)
	}
	issuedAt := time.Now().UTC().Truncate(time.Microsecond)
	facts := battleticket.Facts{
		PlayerID:              playerID,
		SessionID:             sessionID,
		SessionEpoch:          session.InitialEpoch,
		Role:                  battleticket.RoleOwner,
		WorldID:               snapshot.Stamp().WorldID(),
		Assignment:            snapshot.Stamp(),
		AssignmentFingerprint: target.AssignmentFingerprint,
		RuntimeNodeID:         target.RuntimeNodeID,
		SimulationNodeID:      target.NodeID,
		SimulationInstanceID:  target.InstanceID,
		MappingGeneration:     target.MappingGeneration,
		TargetRevision:        target.Revision,
		ModelIdentity:         target.ModelManifest,
		ProfileIdentity:       target.ProfileManifest,
		ConfigIdentity:        target.ConfigIdentity,
		WireIdentity:          wireIdentity,
		ActorSlot:             actorSlot,
		Endpoint:              endpoint,
		IssueID:               issueID,
		IssuedAt:              issuedAt,
		ExpiresAt:             issuedAt.Add(30 * time.Second),
	}
	derivationKey := make([]byte, sha256.Size)
	for index := range derivationKey {
		derivationKey[index] = 0x42
	}
	deriver, err := battleticket.NewDeriver(derivationKey)
	clear(derivationKey)
	if err != nil {
		t.Fatal(err)
	}
	material, err := deriver.Derive(facts)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := battleticketcontrol.NewRegistry(owner.Session(), target.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Install(context.Background(), target, material); err != nil {
		t.Fatalf(
			"install ticket: %v cause=%v diagnostics=%#v child=%v",
			err,
			errors.Unwrap(err),
			diagnostics.snapshot(),
			owner.Err(),
		)
	}

	buildBytes, err := hex.DecodeString(realBuildIdentity(t))
	if err != nil || len(buildBytes) != sha256.Size {
		t.Fatalf("decode protocol client identity: %v", err)
	}
	var buildIdentity [sha256.Size]byte
	copy(buildIdentity[:], buildBytes)
	processContext, cancelProcess := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer cancelProcess()
	clientSupervisor, err := protocolclient.Start(
		processContext,
		protocolclient.Config{
			ExecutablePath:        clientPath,
			ExpectedBuildIdentity: buildIdentity,
		},
	)
	if err != nil {
		t.Fatalf("start protocol client: %v", err)
	}
	credential := &protocolclient.SessionCredential{
		TicketID:     material.Binding().TicketID().Bytes(),
		TicketSecret: material.Secret().Bytes(),
	}
	startContext, cancelStart := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	sessionEvent, err := clientSupervisor.StartSession(
		startContext,
		protocolclient.SessionStart{
			ClientSlot: 1,
			Endpoint: netip.AddrPortFrom(
				netip.MustParseAddr("127.0.0.1"),
				port,
			),
			Credential: credential,
			TicketExpiresAtUnixMS: uint64(
				facts.ExpiresAt.UnixMilli(),
			),
		},
	)
	cancelStart()
	if err != nil || sessionEvent.ClientSlot != 1 {
		t.Fatalf("session start event=%+v err=%v diagnostics=%#v", sessionEvent, err, diagnostics.snapshot())
	}
	var retainedCredential byte
	for _, value := range credential.TicketID {
		retainedCredential |= value
	}
	for _, value := range credential.TicketSecret {
		retainedCredential |= value
	}
	if retainedCredential != 0 {
		t.Fatal("real supervisor retained ticket credential")
	}
	var pollReceipt protocolclient.PollReceipt
	for probeSequence := uint64(1); probeSequence <= 3 &&
		pollReceipt.SnapshotCount == 0; probeSequence++ {
		workloadContext, cancelWorkload := context.WithTimeout(
			context.Background(),
			3*time.Second,
		)
		probeEvent, workloadErr := clientSupervisor.SendWorkload(
			workloadContext,
			protocolclient.WorkloadCommand{
				ClientSlot:          1,
				Operation:           protocolclient.WorkloadProbe,
				RepeatCount:         1,
				ApplicationSequence: probeSequence,
				ApplicationTick:     probeSequence,
			},
		)
		cancelWorkload()
		if workloadErr != nil ||
			probeEvent.Kind != protocolclient.WorkloadDatagramSent ||
			probeEvent.Count != 1 {
			t.Fatalf("send real probe: event=%+v err=%v", probeEvent, workloadErr)
		}
		pollContext, cancelPoll := context.WithTimeout(
			context.Background(),
			3*time.Second,
		)
		pollReceipt, err = clientSupervisor.PollSession(
			pollContext,
			protocolclient.Poll{
				ClientSlot:    1,
				MaximumEvents: 4,
				Wait:          time.Second,
			},
		)
		cancelPoll()
		if err != nil {
			break
		}
	}
	if err != nil || pollReceipt.SnapshotCount == 0 ||
		pollReceipt.LatestServerTick == 0 {
		t.Fatalf("poll real snapshot: receipt=%+v err=%v diagnostics=%#v", pollReceipt, err, diagnostics.snapshot())
	}
	inputTick := uint64(1) + (pollReceipt.LatestServerTick-1)*2
	workloadContext, cancelWorkload := context.WithTimeout(
		context.Background(),
		3*time.Second,
	)
	inputEvent, err := clientSupervisor.SendWorkload(
		workloadContext,
		protocolclient.WorkloadCommand{
			ClientSlot:          1,
			Operation:           protocolclient.WorkloadInputBundle,
			CommandKind:         1,
			RepeatCount:         1,
			ApplicationSequence: inputTick,
			ApplicationTick:     inputTick,
			ValueA:              500,
			ValueB:              -500,
		},
	)
	cancelWorkload()
	if err != nil || inputEvent.Count != 1 {
		t.Fatalf("send real input: event=%+v err=%v", inputEvent, err)
	}
	for attempt := 0; attempt < 4 &&
		pollReceipt.LastProcessedInputTick < inputTick; attempt++ {
		pollContext, cancelPoll := context.WithTimeout(
			context.Background(),
			3*time.Second,
		)
		pollReceipt, err = clientSupervisor.PollSession(
			pollContext,
			protocolclient.Poll{
				ClientSlot:    1,
				MaximumEvents: 8,
				Wait:          500 * time.Millisecond,
			},
		)
		cancelPoll()
		if err != nil {
			break
		}
	}
	if err != nil || pollReceipt.LastProcessedInputTick < inputTick {
		t.Fatalf(
			"real input acknowledgement: inputTick=%d receipt=%+v err=%v",
			inputTick,
			pollReceipt,
			err,
		)
	}
	snapshotCountBeforeSecurity := pollReceipt.SnapshotCount
	for index, delivery := range []protocolclient.DeliveryMutation{
		protocolclient.DeliveryExactReplay,
		protocolclient.DeliveryTamperedTag,
		protocolclient.DeliveryOversizePrefix,
	} {
		securityContext, cancelSecurity := context.WithTimeout(
			context.Background(),
			3*time.Second,
		)
		event, securityErr := clientSupervisor.SendWorkload(
			securityContext,
			protocolclient.WorkloadCommand{
				ClientSlot:          1,
				Operation:           protocolclient.WorkloadProbe,
				RepeatCount:         1,
				ApplicationSequence: uint64(index) + 10,
				ApplicationTick:     pollReceipt.SnapshotCount,
				Delivery:            delivery,
			},
		)
		cancelSecurity()
		if securityErr != nil || event.Count != 2 {
			t.Fatalf("real delivery mutation %d: event=%+v err=%v", delivery, event, securityErr)
		}
	}
	pollContext, cancelPoll := context.WithTimeout(
		context.Background(),
		3*time.Second,
	)
	pollReceipt, err = clientSupervisor.PollSession(
		pollContext,
		protocolclient.Poll{
			ClientSlot:    1,
			MaximumEvents: 8,
			Wait:          time.Second,
		},
	)
	cancelPoll()
	if err != nil ||
		pollReceipt.SnapshotCount < snapshotCountBeforeSecurity+3 {
		t.Fatalf("real replay/tamper/MTU gate: receipt=%+v err=%v", pollReceipt, err)
	}
	metricsBeforeExpiry, err := controller.QualificationSnapshot(
		context.Background(),
		qualificationRunID,
		snapshot.Stamp(),
	)
	if err != nil {
		t.Fatalf("read metrics before expired input: %v", err)
	}
	expiredContext, cancelExpired := context.WithTimeout(
		context.Background(),
		3*time.Second,
	)
	expiredEvent, err := clientSupervisor.SendWorkload(
		expiredContext,
		protocolclient.WorkloadCommand{
			ClientSlot:          1,
			Operation:           protocolclient.WorkloadInputBundle,
			CommandKind:         3,
			RepeatCount:         1,
			ApplicationSequence: inputTick + 20,
			ApplicationTick:     1,
		},
	)
	cancelExpired()
	if err != nil || expiredEvent.Count != 1 {
		t.Fatalf("send expired real input: event=%+v err=%v", expiredEvent, err)
	}
	snapshotCountBeforeExpiry := pollReceipt.SnapshotCount
	reliableCountBeforeExpiry := pollReceipt.ReliableCount
	pollContext, cancelPoll = context.WithTimeout(
		context.Background(),
		3*time.Second,
	)
	pollReceipt, err = clientSupervisor.PollSession(
		pollContext,
		protocolclient.Poll{
			ClientSlot:    1,
			MaximumEvents: 2,
			Wait:          500 * time.Millisecond,
		},
	)
	cancelPoll()
	if err != nil ||
		pollReceipt.SnapshotCount < snapshotCountBeforeExpiry ||
		pollReceipt.ReliableCount != reliableCountBeforeExpiry {
		t.Fatalf("expired real input advanced application state: receipt=%+v err=%v", pollReceipt, err)
	}
	metricsAfterExpiry, err := controller.QualificationSnapshot(
		context.Background(),
		qualificationRunID,
		snapshot.Stamp(),
	)
	if err != nil {
		t.Fatalf("read metrics after expired input: %v", err)
	}
	if metricsAfterExpiry.Metrics.RejectedPackets !=
		metricsBeforeExpiry.Metrics.RejectedPackets+1 {
		t.Fatalf(
			"expired real input rejection count drifted: before=%d after=%d",
			metricsBeforeExpiry.Metrics.RejectedPackets,
			metricsAfterExpiry.Metrics.RejectedPackets,
		)
	}
	backpressureContext, cancelBackpressure := context.WithTimeout(
		context.Background(),
		3*time.Second,
	)
	backpressureEvent, err := clientSupervisor.SendWorkload(
		backpressureContext,
		protocolclient.WorkloadCommand{
			ClientSlot:          1,
			Operation:           protocolclient.WorkloadProbe,
			RepeatCount:         32,
			ApplicationSequence: 100,
			ApplicationTick:     pollReceipt.SnapshotCount,
		},
	)
	cancelBackpressure()
	if err != nil || backpressureEvent.Count != 32 {
		t.Fatalf("send real backpressure burst: event=%+v err=%v", backpressureEvent, err)
	}
	snapshotCountBeforeIngressBurst := pollReceipt.SnapshotCount
	pollContext, cancelPoll = context.WithTimeout(
		context.Background(),
		3*time.Second,
	)
	pollReceipt, err = clientSupervisor.PollSession(
		pollContext,
		protocolclient.Poll{
			ClientSlot:    1,
			MaximumEvents: 8,
			Wait:          time.Second,
		},
	)
	cancelPoll()
	publishedDuringIngress := pollReceipt.SnapshotCount -
		snapshotCountBeforeIngressBurst
	if err != nil || publishedDuringIngress == 0 {
		t.Fatalf(
			"continuous ingress starved periodic publication: receipt=%+v published=%d err=%v",
			pollReceipt,
			publishedDuringIngress,
			err,
		)
	}
	transitionContext, cancelTransition := context.WithTimeout(
		context.Background(),
		3*time.Second,
	)
	rekeyEvent, err := clientSupervisor.Transition(
		transitionContext,
		protocolclient.NetworkTransition{
			ClientSlot: 1,
			Operation:  protocolclient.NetworkRekey,
		},
	)
	cancelTransition()
	if err != nil || !rekeyEvent.Committed ||
		rekeyEvent.Generation != 2 {
		t.Fatalf("real rekey: event=%+v err=%v", rekeyEvent, err)
	}
	workloadContext, cancelWorkload = context.WithTimeout(
		context.Background(),
		3*time.Second,
	)
	resyncEvent, err := clientSupervisor.SendWorkload(
		workloadContext,
		protocolclient.WorkloadCommand{
			ClientSlot:          1,
			Operation:           protocolclient.WorkloadResyncRequest,
			RepeatCount:         1,
			ApplicationSequence: 1,
			ApplicationTick:     pollReceipt.LatestServerTick,
			ValueA:              1,
		},
	)
	cancelWorkload()
	if err != nil || resyncEvent.Count == 0 {
		t.Fatalf("send real resync: event=%+v err=%v", resyncEvent, err)
	}
	pollContext, cancelPoll = context.WithTimeout(
		context.Background(),
		3*time.Second,
	)
	pollReceipt, err = clientSupervisor.PollSession(
		pollContext,
		protocolclient.Poll{
			ClientSlot:    1,
			MaximumEvents: 8,
			Wait:          time.Second,
		},
	)
	cancelPoll()
	if err != nil || pollReceipt.ReliableCount == 0 ||
		pollReceipt.SnapshotCount < 2 {
		t.Fatalf("poll real KCP/resync: receipt=%+v err=%v diagnostics=%#v", pollReceipt, err, diagnostics.snapshot())
	}
	transitionContext, cancelTransition = context.WithTimeout(
		context.Background(),
		3*time.Second,
	)
	closeEvent, err := clientSupervisor.Transition(
		transitionContext,
		protocolclient.NetworkTransition{
			ClientSlot: 1,
			Operation:  protocolclient.NetworkClose,
		},
	)
	cancelTransition()
	if err != nil || !closeEvent.Committed ||
		closeEvent.Generation != 0 {
		t.Fatalf("real close: event=%+v err=%v", closeEvent, err)
	}
	closeContext, cancelClose := context.WithTimeout(
		context.Background(),
		3*time.Second,
	)
	if err := clientSupervisor.Close(closeContext); err != nil {
		cancelClose()
		t.Fatalf("close protocol client: %v", err)
	}
	cancelClose()
	if clientSupervisor.HadStderr() {
		t.Fatal("real protocol client wrote stderr")
	}
	if _, err := registry.Revoke(
		context.Background(),
		target,
		material.Binding(),
	); err != nil {
		t.Fatalf("revoke closed owner ticket: %v", err)
	}
	type additionalActor struct {
		supervisor *protocolclient.Supervisor
		cancel     context.CancelFunc
		slot       uint8
		binding    battleticket.Binding
	}
	additionalActors := make([]additionalActor, 0, 8)
	for slot := uint8(0); slot < 8; slot++ {
		material := deriveRealActorMaterial(
			t,
			deriver,
			facts,
			int(slot)+2,
			slot,
		)
		supervisor, cancelActor := startRealProtocolActor(
			t,
			registry,
			target,
			material,
			clientPath,
			buildIdentity,
			slot+1,
			port,
		)
		t.Cleanup(func() {
			cleanupContext, cancelCleanup :=
				context.WithTimeout(
					context.Background(),
					time.Second,
				)
			_ = supervisor.Close(cleanupContext)
			cancelCleanup()
			cancelActor()
		})
		additionalActors = append(
			additionalActors,
			additionalActor{
				supervisor: supervisor,
				cancel:     cancelActor,
				slot:       slot + 1,
				binding:    material.Binding(),
			},
		)
	}
	if _, err := battleticket.NewActorSlot(8); err == nil {
		t.Fatal("real ninth actor admission was not rejected")
	}
	if _, err := registry.Revoke(
		context.Background(),
		target,
		additionalActors[0].binding,
	); err != nil {
		t.Fatalf("revoke active visitor: %v", err)
	}
	invalidationContext, cancelInvalidation := context.WithTimeout(
		context.Background(),
		2*time.Second,
	)
	if _, err := additionalActors[0].supervisor.Transition(
		invalidationContext,
		protocolclient.NetworkTransition{
			ClientSlot: additionalActors[0].slot,
			Operation:  protocolclient.NetworkClose,
		},
	); err == nil {
		cancelInvalidation()
		t.Fatal("revoked real visitor session remained active")
	}
	cancelInvalidation()
	for _, actor := range additionalActors {
		actorCloseContext, cancelActorClose := context.WithTimeout(
			context.Background(),
			3*time.Second,
		)
		if err := actor.supervisor.Close(actorCloseContext); err != nil {
			cancelActorClose()
			t.Fatalf("shutdown visitor slot %d: %v", actor.slot, err)
		}
		cancelActorClose()
		actor.cancel()
	}
	if err := controller.Stop(context.Background(), snapshot.Stamp()); err != nil {
		t.Fatalf("stop instance: %v", err)
	}
	if err := controller.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// deriveRealActorMaterial 从共同 target 事实构造一个不同账号/Session/VisitSession actor。
func deriveRealActorMaterial(
	t *testing.T,
	deriver *battleticket.Deriver,
	base battleticket.Facts,
	identityIndex int,
	slot uint8,
) battleticket.Material {
	t.Helper()
	playerID, err := account.NewPlayerID(
		fmt.Sprintf("ply_realvisitor%02d", identityIndex),
	)
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := session.NewSessionID(
		fmt.Sprintf("ses_realvisitor%02d", identityIndex),
	)
	if err != nil {
		t.Fatal(err)
	}
	visitSessionID, err := visitsession.NewVisitSessionID(
		fmt.Sprintf("vses_realvisitor%02d", identityIndex),
	)
	if err != nil {
		t.Fatal(err)
	}
	actorSlot, err := battleticket.NewActorSlot(slot)
	if err != nil {
		t.Fatal(err)
	}
	issueID, err := battleticket.NewIssueID(
		fmt.Sprintf("biss_real_visitor_%02d_0001", identityIndex),
	)
	if err != nil {
		t.Fatal(err)
	}
	issuedAt := time.Now().UTC().Truncate(time.Microsecond)
	base.PlayerID = playerID
	base.SessionID = sessionID
	base.Role = battleticket.RoleVisitor
	base.VisitSessionID = visitSessionID
	base.ActorSlot = actorSlot
	base.IssueID = issueID
	base.IssuedAt = issuedAt
	base.ExpiresAt = issuedAt.Add(30 * time.Second)
	material, err := deriver.Derive(base)
	if err != nil {
		t.Fatal(err)
	}
	return material
}

// startRealProtocolActor 安装公开 credential 并启动一个独立真实 client process。
func startRealProtocolActor(
	t *testing.T,
	registry *battleticketcontrol.Registry,
	target simulationcontrol.SimulationTarget,
	material battleticket.Material,
	clientPath string,
	buildIdentity [sha256.Size]byte,
	clientSlot uint8,
	port uint16,
) (*protocolclient.Supervisor, context.CancelFunc) {
	t.Helper()
	if _, err := registry.Install(
		context.Background(),
		target,
		material,
	); err != nil {
		t.Fatalf("install actor slot %d: %v", clientSlot, err)
	}
	processContext, cancelProcess := context.WithTimeout(
		context.Background(),
		30*time.Second,
	)
	supervisor, err := protocolclient.Start(
		processContext,
		protocolclient.Config{
			ExecutablePath:        clientPath,
			ExpectedBuildIdentity: buildIdentity,
		},
	)
	if err != nil {
		cancelProcess()
		t.Fatalf("start actor slot %d: %v", clientSlot, err)
	}
	credential := &protocolclient.SessionCredential{
		TicketID:     material.Binding().TicketID().Bytes(),
		TicketSecret: material.Secret().Bytes(),
	}
	startContext, cancelStart := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	event, err := supervisor.StartSession(
		startContext,
		protocolclient.SessionStart{
			ClientSlot: clientSlot,
			Endpoint: netip.AddrPortFrom(
				netip.MustParseAddr("127.0.0.1"),
				port,
			),
			Credential: credential,
			TicketExpiresAtUnixMS: uint64(
				material.Binding().Facts().ExpiresAt.UnixMilli(),
			),
		},
	)
	cancelStart()
	if err != nil || event.ClientSlot != clientSlot {
		_ = supervisor.Close(context.Background())
		cancelProcess()
		t.Fatalf("start actor slot %d: event=%+v err=%v", clientSlot, event, err)
	}
	return supervisor, cancelProcess
}

// diagnosticCollector 保存低敏 child stderr 供失败断言。
type diagnosticCollector struct {
	// mutex 保护 lines。
	mutex sync.Mutex
	// lines 是截断后的诊断。
	lines []string
}

// ObserveSimulationDiagnostic 记录一行低敏诊断。
func (collector *diagnosticCollector) ObserveSimulationDiagnostic(line string) {
	collector.mutex.Lock()
	defer collector.mutex.Unlock()
	collector.lines = append(collector.lines, line)
}

// snapshot 返回诊断副本。
func (collector *diagnosticCollector) snapshot() []string {
	collector.mutex.Lock()
	defer collector.mutex.Unlock()
	return append([]string(nil), collector.lines...)
}

// TestRealChildLifecycle 验证真实 binary 的 hello/start/drain/result/stop/shutdown。
func TestRealChildLifecycle(t *testing.T) {
	if os.Getenv("IHOMELAND_SIMULATION_REAL_CHILD") != "1" {
		t.Skip("real simulation child harness is required")
	}
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(repositoryRoot, "simulation", "out", "build", "windows-msvc-ci", "ihomeland-sim-server.exe")
	receiptPath := filepath.Join(repositoryRoot, "simulation", "out", "build", "windows-msvc-ci", "qualification-gate-receipt.json")
	binaryDigest := fileDigest(t, binaryPath)
	receiptDigest := fileDigest(t, receiptPath)
	nonce, err := simulationcontrol.NewSessionNonce()
	if err != nil {
		t.Fatal(err)
	}
	inbox := simulationcontrol.NewProposalInbox()
	diagnostics := &diagnosticCollector{}
	owner, err := Start(
		realProcessConfig(t, Config{
			BinaryPath:                 binaryPath,
			BinarySHA256:               binaryDigest,
			QualificationReceiptPath:   receiptPath,
			QualificationReceiptSHA256: receiptDigest,
			RequestTimeout:             3 * time.Second,
			ShutdownTimeout:            3 * time.Second,
			StderrLineLimit:            1024,
		}),
		nonce,
		inbox,
		diagnostics,
	)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		select {
		case <-owner.Done():
		default:
			_ = owner.Terminate(context.Background())
		}
	})
	controller, err := simulationcontrol.BootstrapController(
		context.Background(),
		owner.Session(),
		realControllerConfig(t),
	)
	if err != nil {
		t.Fatalf("BootstrapController: %v", err)
	}
	snapshots := make([]placement.AssignmentSnapshot, 0, 8)
	for index, suffix := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		snapshots = append(snapshots, realStartingSnapshotAt(t, suffix, 1, uint64(index+1)))
	}
	for index, snapshot := range snapshots {
		if err := controller.Start(context.Background(), snapshot); err != nil {
			t.Fatalf("controller.Start[%d]: %v diagnostics=%#v", index, err, diagnostics.snapshot())
		}
	}
	for index, snapshot := range snapshots {
		if target, found := controller.ResolveTarget(snapshot.Stamp()); !found || target.Validate() != nil {
			t.Fatalf("ResolveTarget[%d] = %#v, %v", index, target, found)
		}
		if state, _, err := controller.Status(context.Background(), snapshot.Stamp()); err != nil || state != "running" {
			t.Fatalf("controller.Status[%d] = %q, %v", index, state, err)
		}
	}
	assertNoNetworkEndpoint(t, owner.ProcessID())
	for index, snapshot := range snapshots {
		if err := controller.Drain(context.Background(), snapshot.Stamp()); err != nil {
			t.Fatalf("controller.Drain[%d]: %v", index, err)
		}
		proposal, found := inbox.Peek()
		if !found {
			t.Fatalf("drain[%d] did not flush a ResultProposal", index)
		}
		if err := controller.AcknowledgeResult(context.Background(), proposal, "committed"); err != nil {
			t.Fatalf("AcknowledgeResult[%d]: %v", index, err)
		}
		if err := inbox.Discard(proposal); err != nil {
			t.Fatalf("Discard[%d]: %v", index, err)
		}
		if err := controller.Stop(context.Background(), snapshot.Stamp()); err != nil {
			t.Fatalf("controller.Stop[%d]: %v", index, err)
		}
	}
	if err := controller.Shutdown(context.Background()); err != nil {
		t.Fatalf("controller.Shutdown: %v", err)
	}
	select {
	case <-owner.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("simulation child did not exit")
	}
	if err := owner.Err(); err != nil {
		t.Fatalf("child exit: %v", err)
	}
	if lines := diagnostics.snapshot(); len(lines) != 0 {
		t.Fatalf("child diagnostics = %#v", lines)
	}
}

// TestRealChildArtifactDriftFailsBeforeSpawn 验证 binary/receipt 任一摘要漂移都不会创建 child。
func TestRealChildArtifactDriftFailsBeforeSpawn(t *testing.T) {
	if os.Getenv("IHOMELAND_SIMULATION_REAL_CHILD") != "1" {
		t.Skip("real simulation child harness is required")
	}
	binaryPath, receiptPath := realArtifactPaths(t)
	nonce, err := simulationcontrol.NewSessionNonce()
	if err != nil {
		t.Fatal(err)
	}
	wrong, _ := simulationcontrol.NewDigest(strings.Repeat("a", 64))
	for _, testCase := range []struct {
		name          string
		binaryDigest  simulationcontrol.Digest
		receiptDigest simulationcontrol.Digest
	}{
		{name: "binary", binaryDigest: wrong, receiptDigest: fileDigest(t, receiptPath)},
		{name: "receipt", binaryDigest: fileDigest(t, binaryPath), receiptDigest: wrong},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			owner, startErr := Start(
				realProcessConfig(t, Config{
					BinaryPath:                 binaryPath,
					BinarySHA256:               testCase.binaryDigest,
					QualificationReceiptPath:   receiptPath,
					QualificationReceiptSHA256: testCase.receiptDigest,
					RequestTimeout:             time.Second,
					ShutdownTimeout:            time.Second,
					StderrLineLimit:            256,
				}),
				nonce,
				simulationcontrol.NewProposalInbox(),
				&diagnosticCollector{},
			)
			if startErr == nil || owner != nil {
				t.Fatalf("Start=%v owner=%#v", startErr, owner)
			}
		})
	}
}

// TestRealChildTerminalExitClosesSession 验证精确 child 被终止后 session 不可复活。
func TestRealChildTerminalExitClosesSession(t *testing.T) {
	if os.Getenv("IHOMELAND_SIMULATION_REAL_CHILD") != "1" {
		t.Skip("real simulation child harness is required")
	}
	binaryPath, receiptPath := realArtifactPaths(t)
	nonce, err := simulationcontrol.NewSessionNonce()
	if err != nil {
		t.Fatal(err)
	}
	owner, err := Start(
		realProcessConfig(t, Config{
			BinaryPath:                 binaryPath,
			BinarySHA256:               fileDigest(t, binaryPath),
			QualificationReceiptPath:   receiptPath,
			QualificationReceiptSHA256: fileDigest(t, receiptPath),
			RequestTimeout:             time.Second,
			ShutdownTimeout:            time.Second,
			StderrLineLimit:            256,
		}),
		nonce,
		simulationcontrol.NewProposalInbox(),
		&diagnosticCollector{},
	)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := simulationcontrol.BootstrapController(context.Background(), owner.Session(), realControllerConfig(t))
	if err != nil {
		_ = owner.Terminate(context.Background())
		t.Fatal(err)
	}
	_ = owner.Terminate(context.Background())
	select {
	case <-owner.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("terminated child handle was not reaped")
	}
	if err := controller.Probe(context.Background()); err == nil {
		t.Fatal("terminal child session accepted a later request")
	}
}

// TestRealSessionMultipleStarts 验证 Session 自身可连续消费多个真实 instance.ready。
func TestRealSessionMultipleStarts(t *testing.T) {
	if os.Getenv("IHOMELAND_SIMULATION_REAL_CHILD") != "1" {
		t.Skip("real simulation child harness is required")
	}
	binaryPath, receiptPath := realArtifactPaths(t)
	nonce, err := simulationcontrol.NewSessionNonce()
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := &diagnosticCollector{}
	owner, err := Start(
		realProcessConfig(t, Config{
			BinaryPath:                 binaryPath,
			BinarySHA256:               fileDigest(t, binaryPath),
			QualificationReceiptPath:   receiptPath,
			QualificationReceiptSHA256: fileDigest(t, receiptPath),
			RequestTimeout:             3 * time.Second,
			ShutdownTimeout:            3 * time.Second,
			StderrLineLimit:            1024,
		}),
		nonce,
		simulationcontrol.NewProposalInbox(),
		diagnostics,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Terminate(context.Background()) })
	helloID, _ := simulationcontrol.NewRequestID("sctl_sessionhello000001")
	if _, err := owner.Session().Call(
		context.Background(),
		helloID,
		"node.hello.challenge",
		map[string]any{
			"actorCapacity":              8,
			"expectedBuildIdentity":      realBuildIdentity(t),
			"expectedConfigIdentity":     realGameplayConfigIdentity,
			"expectedModelManifest":      "65e136d20dfa244db4ce42007cfe1c0411b7f807b635209704b6ef51e93d08b1",
			"expectedNavigationIdentity": realGameplayNavigationIdentity,
			"expectedPhysicsIdentity":    realGameplayPhysicsIdentity,
			"expectedProfileManifest":    "c7ff3d1f582625d18028c4ce20fb808b2e56e10084c4ccf61790b0c5f486c424",
			"expectedWireIdentity":       realGameplayWireIdentity,
			"instanceCapacity":           8,
			"runtimeNodeId":              "rnode_realchildtest",
			"simulationNodeId":           "snode_session",
		},
		"node.hello.receipt",
	); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 8; index++ {
		requestID, _ := simulationcontrol.NewRequestID("sctl_start_" + strings.Repeat("a", 31) + strconv.Itoa(index))
		suffix := string(rune('a' + index - 1))
		worldID := "pworld_realchildtest" + suffix
		instanceID := "winst_realchildtest" + suffix
		fingerprint := sha256.Sum256([]byte(worldID + "\x00" + instanceID + "\x00rnode_realchildtest\x001\x00" + strconv.Itoa(index)))
		seedDigest := sha256.Sum256([]byte("simulation-seed-v1\x00" + hex.EncodeToString(fingerprint[:]) + "\x00" + strings.Repeat("d", 64)))
		seed := binary.BigEndian.Uint64(seedDigest[:8])
		if seed == 0 {
			seed = 1
		}
		_, callErr := owner.Session().Call(
			context.Background(),
			requestID,
			"instance.start",
			map[string]any{
				"actorCapacity": 8,
				"assignment": map[string]any{
					"assignmentFingerprint": hex.EncodeToString(fingerprint[:]),
					"fencingToken":          strconv.Itoa(index),
					"generation":            "1",
					"personalWorldId":       worldID,
					"runtimeNodeId":         "rnode_realchildtest",
					"worldInstanceId":       instanceID,
				},
				"configIdentity":     strings.Repeat("d", 64),
				"mappingGeneration":  "1",
				"navigationIdentity": strings.Repeat("e", 64),
				"physicsIdentity":    strings.Repeat("f", 64),
				"seed":               strconv.FormatUint(seed, 10),
				"startRequestId":     requestID.String(),
			},
			"instance.ready",
		)
		if callErr != nil {
			t.Fatalf("start[%d]: %v diagnostics=%#v", index, callErr, diagnostics.snapshot())
		}
	}
}

// TestRealChildResponseLossProxyReplaysStart 使用受控 raw frame proxy 丢弃首次 ready 并重试同一 request。
func TestRealChildResponseLossProxyReplaysStart(t *testing.T) {
	if os.Getenv("IHOMELAND_SIMULATION_REAL_CHILD") != "1" {
		t.Skip("real simulation child harness is required")
	}
	binaryPath, _ := realArtifactPaths(t)
	command := exec.Command(binaryPath, "--control-stdio")
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNewProcessGroup}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waitDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, stderr)
	}()
	go func() {
		_ = command.Wait()
		close(waitDone)
	}()
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		<-waitDone
	})
	reader := bufio.NewReader(stdout)
	nonce, _ := simulationcontrol.NewDigest(strings.Repeat("1", 64))
	helloID, _ := simulationcontrol.NewRequestID("sctl_proxyhello000001")
	hello, err := simulationcontrol.NewFrame(
		"node.hello.challenge",
		map[string]any{
			"actorCapacity":           8,
			"expectedBuildIdentity":   realBuildIdentity(t),
			"expectedModelManifest":   "65e136d20dfa244db4ce42007cfe1c0411b7f807b635209704b6ef51e93d08b1",
			"expectedProfileManifest": "c7ff3d1f582625d18028c4ce20fb808b2e56e10084c4ccf61790b0c5f486c424",
			"instanceCapacity":        8,
			"runtimeNodeId":           "rnode_proxy",
			"simulationNodeId":        "snode_proxy",
		},
		helloID,
		1,
		nonce,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := simulationcontrol.WriteFrame(stdin, hello); err != nil {
		t.Fatal(err)
	}
	helloReceipt, err := simulationcontrol.DecodeFrame(reader)
	if err != nil || helloReceipt.Kind != "node.hello.receipt" || helloReceipt.Sequence != 2 {
		t.Fatalf("hello receipt=%#v err=%v", helloReceipt, err)
	}

	startID, _ := simulationcontrol.NewRequestID("sctl_proxystart000001")
	assignmentFingerprint := sha256.Sum256([]byte("pworld_proxy\x00winst_proxy\x00rnode_proxy\x001\x001"))
	startPayload := map[string]any{
		"actorCapacity": 8,
		"assignment": map[string]any{
			"assignmentFingerprint": hex.EncodeToString(assignmentFingerprint[:]),
			"fencingToken":          "1",
			"generation":            "1",
			"personalWorldId":       "pworld_proxy",
			"runtimeNodeId":         "rnode_proxy",
			"worldInstanceId":       "winst_proxy",
		},
		"configIdentity":     strings.Repeat("d", 64),
		"mappingGeneration":  "1",
		"navigationIdentity": strings.Repeat("e", 64),
		"physicsIdentity":    strings.Repeat("f", 64),
		"seed":               "13",
		"startRequestId":     startID.String(),
	}
	firstStart, err := simulationcontrol.NewFrame("instance.start", startPayload, startID, 3, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if err := simulationcontrol.WriteFrame(stdin, firstStart); err != nil {
		t.Fatal(err)
	}
	droppedReady, err := simulationcontrol.DecodeFrame(reader)
	if err != nil || droppedReady.Kind != "instance.ready" || droppedReady.Sequence != 4 {
		t.Fatalf("first ready=%#v err=%v", droppedReady, err)
	}

	replayedStart, err := simulationcontrol.NewFrame("instance.start", startPayload, startID, 5, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if err := simulationcontrol.WriteFrame(stdin, replayedStart); err != nil {
		t.Fatal(err)
	}
	replayedReady, err := simulationcontrol.DecodeFrame(reader)
	if err != nil || replayedReady.Kind != "instance.ready" || replayedReady.Sequence != 6 {
		t.Fatalf("replayed ready=%#v err=%v", replayedReady, err)
	}
	var payload struct {
		Replayed bool `json:"replayed"`
	}
	if err := json.Unmarshal(replayedReady.Payload, &payload); err != nil || !payload.Replayed {
		t.Fatalf("replayed payload=%s err=%v", replayedReady.Payload, err)
	}
	for index := 2; index <= 8; index++ {
		requestID, requestErr := simulationcontrol.NewRequestID(fmt.Sprintf("sctl_proxystart00000%d%s", index, strings.Repeat("x", 22)))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		worldID := fmt.Sprintf("pworld_proxyworld00000%d", index)
		instanceID := fmt.Sprintf("winst_proxyinstance00000%d", index)
		fingerprint := sha256.Sum256([]byte(worldID + "\x00" + instanceID + "\x00rnode_proxy\x001\x00" + strconv.Itoa(index)))
		payload := map[string]any{
			"actorCapacity": 8,
			"assignment": map[string]any{
				"assignmentFingerprint": hex.EncodeToString(fingerprint[:]),
				"fencingToken":          strconv.Itoa(index),
				"generation":            "1",
				"personalWorldId":       worldID,
				"runtimeNodeId":         "rnode_proxy",
				"worldInstanceId":       instanceID,
			},
			"configIdentity":     strings.Repeat("d", 64),
			"mappingGeneration":  "1",
			"navigationIdentity": strings.Repeat("e", 64),
			"physicsIdentity":    strings.Repeat("f", 64),
			"seed":               strconv.Itoa(index + 20),
			"startRequestId":     requestID.String(),
		}
		sequence := uint64(2*index + 3)
		frame, frameErr := simulationcontrol.NewFrame("instance.start", payload, requestID, sequence, nonce)
		if frameErr != nil {
			t.Fatal(frameErr)
		}
		if err := simulationcontrol.WriteFrame(stdin, frame); err != nil {
			t.Fatal(err)
		}
		ready, readyErr := simulationcontrol.DecodeFrame(reader)
		if readyErr != nil || ready.Kind != "instance.ready" || ready.Sequence != sequence+1 {
			t.Fatalf("ready[%d]=%#v err=%v", index, ready, readyErr)
		}
	}
}

// assertNoNetworkEndpoint 验证 child PID 没有 TCP/UDP endpoint。
func assertNoNetworkEndpoint(t *testing.T, processID int) {
	t.Helper()
	if processID <= 0 {
		t.Fatal("simulation child process ID is invalid")
	}
	output, err := exec.Command("netstat", "-ano").Output()
	if err != nil {
		t.Fatalf("netstat: %v", err)
	}
	expected := strconv.Itoa(processID)
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[len(fields)-1] == expected {
			t.Fatalf("simulation child unexpectedly owns a network endpoint: %s", strings.TrimSpace(line))
		}
	}
}

// fileDigest 读取测试 artifact 的 SHA-256。
func fileDigest(t *testing.T, path string) simulationcontrol.Digest {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	digest, err := simulationcontrol.NewDigest(hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

// realArtifactPaths 返回资格脚本刚构建的真实 child 与 receipt。
func realArtifactPaths(t *testing.T) (string, string) {
	t.Helper()
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(repositoryRoot, "simulation", "out", "build", "windows-msvc-ci", "ihomeland-sim-server.exe"),
		filepath.Join(repositoryRoot, "simulation", "out", "build", "windows-msvc-ci", "qualification-gate-receipt.json")
}

// realControllerConfig 返回与 sim-server compile binding 一致的 registration。
func realControllerConfig(t *testing.T) simulationcontrol.ControllerConfig {
	t.Helper()
	nodeID, _ := simulationcontrol.NewSimulationNodeID("snode_realchildtest")
	runtimeNodeID, _ := placement.NewRuntimeNodeID("rnode_realchildtest")
	digest := func(value string) simulationcontrol.Digest {
		result, err := simulationcontrol.NewDigest(value)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	return simulationcontrol.ControllerConfig{
		NodeID:        nodeID,
		RuntimeNodeID: runtimeNodeID,
		Build: simulationcontrol.BuildBinding{
			BuildIdentity:         digest(realBuildIdentity(t)),
			ModelManifest:         digest("65e136d20dfa244db4ce42007cfe1c0411b7f807b635209704b6ef51e93d08b1"),
			ProfileManifest:       digest("c7ff3d1f582625d18028c4ce20fb808b2e56e10084c4ccf61790b0c5f486c424"),
			PlatformQualification: "implementation-qualified-windows-x64",
		},
		Capacity:           simulationcontrol.NodeCapacity{Instances: 8, Actors: 8},
		ConfigIdentity:     digest(realGameplayConfigIdentity),
		GameplayPackageID:  "personal-world-combat-v1",
		NavigationIdentity: digest(realGameplayNavigationIdentity),
		PhysicsIdentity:    digest(realGameplayPhysicsIdentity),
		WireIdentity:       digest(realGameplayWireIdentity),
		MappingIdentity:    digest(realGameplayMappingIdentity),
		DrainDeadline:      time.Second,
		StopDeadline:       time.Second,
	}
}

// realProcessConfig 为真实 child 注入同一 production package 本机选择。
func realProcessConfig(t *testing.T, config Config) Config {
	t.Helper()
	config.GameplayPackageRoot = realGameplayPackageRoot(t)
	config.GameplayArenaRoot = realGameplayArenaRoot(t)
	config.GameplayPackageID = "personal-world-combat-v1"
	digest := func(value string) simulationcontrol.Digest {
		result, err := simulationcontrol.NewDigest(value)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	config.ConfigIdentity = digest(realGameplayConfigIdentity)
	config.NavigationIdentity = digest(realGameplayNavigationIdentity)
	config.PhysicsIdentity = digest(realGameplayPhysicsIdentity)
	config.WireIdentity = digest(realGameplayWireIdentity)
	return config
}

// realGameplayPackageRoot 返回仓库内 production package 的绝对目录。
func realGameplayPackageRoot(t *testing.T) string {
	t.Helper()
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(repositoryRoot, "shared", "contracts", "gameplay", "battle", "packages", "personal-world-combat-v1")
}

// realGameplayArenaRoot 返回仓库内 production arena source 的绝对目录。
func realGameplayArenaRoot(t *testing.T) string {
	t.Helper()
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(repositoryRoot, "simulation", "content", "personal-world-combat-v1")
}

// realBuildIdentity 从当前 CI build identity 读取已嵌入 qualified child 的 exact target identity。
func realBuildIdentity(t *testing.T) string {
	t.Helper()
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(
		repositoryRoot,
		"simulation",
		"out",
		"build",
		"windows-msvc-ci",
		"ihomeland-build-identity.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var identity struct {
		TargetIdentity string `json:"target_identity"`
	}
	if err := json.Unmarshal(content, &identity); err != nil {
		t.Fatal(err)
	}
	if _, err := simulationcontrol.NewDigest(identity.TargetIdentity); err != nil {
		t.Fatal(err)
	}
	return identity.TargetIdentity
}

// realStartingSnapshot 构造真实 child 使用的 placement starting assignment。
func realStartingSnapshot(t *testing.T) placement.AssignmentSnapshot {
	t.Helper()
	return realStartingSnapshotAt(t, "default", 1, 1)
}

// realStartingSnapshotAt 构造可在同一真实 child 中并存的 placement starting assignment。
func realStartingSnapshotAt(t *testing.T, suffix string, generationValue uint64, fenceValue uint64) placement.AssignmentSnapshot {
	t.Helper()
	worldID, err := personalworld.NewPersonalWorldID("pworld_realchildtest" + suffix)
	if err != nil {
		t.Fatal(err)
	}
	instanceID, err := placement.NewWorldInstanceID("winst_realchildtest" + suffix)
	if err != nil {
		t.Fatal(err)
	}
	nodeID, err := placement.NewRuntimeNodeID("rnode_realchildtest")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := placement.NewAssignmentGeneration(generationValue)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := placement.NewFencingToken(fenceValue)
	if err != nil {
		t.Fatal(err)
	}
	stamp, err := placement.NewAssignmentStamp(worldID, instanceID, nodeID, generation, fence)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	snapshot, err := placement.NewAssignmentSnapshot(
		stamp,
		placement.PhaseStarting,
		now,
		now.Add(time.Minute),
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
