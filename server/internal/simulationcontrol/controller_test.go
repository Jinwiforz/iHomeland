package simulationcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
)

// fakeControlSession 用 closed receipts 验证 controller lifecycle binding。
type fakeControlSession struct {
	// mutex 保护 calls 和 closed。
	mutex sync.Mutex
	// calls 保存 request kind 顺序。
	calls []string
	// instanceID 是 ready receipt 固定 identity。
	instanceID string
	// closed 模拟 terminal session。
	closed chan struct{}
	// startEntered 在需要验证并发 probe 时观察 start 已占用 control turn。
	startEntered chan struct{}
	// releaseStart 允许测试在 probe 完成后释放阻塞的 start receipt。
	releaseStart chan struct{}
	// qualificationMutation 选择 snapshot receipt 的单一负例变体。
	qualificationMutation string
}

// Call 返回与 request 完整关联的 canonical receipt。
func (session *fakeControlSession) Call(_ context.Context, requestID RequestID, kind string, payload any, expected string) (json.RawMessage, error) {
	session.mutex.Lock()
	session.calls = append(session.calls, kind)
	session.mutex.Unlock()
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	switch kind {
	case "node.hello.challenge":
		return json.Marshal(struct {
			ActorCapacity         int    `json:"actorCapacity"`
			BuildIdentity         string `json:"buildIdentity"`
			InstanceCapacity      int    `json:"instanceCapacity"`
			ModelManifest         string `json:"modelManifest"`
			PlatformQualification string `json:"platformQualification"`
			ProfileManifest       string `json:"profileManifest"`
			RuntimeNodeID         string `json:"runtimeNodeId"`
			SimulationNodeID      string `json:"simulationNodeId"`
		}{
			ActorCapacity:         8,
			BuildIdentity:         strings.Repeat("a", 64),
			InstanceCapacity:      2,
			ModelManifest:         strings.Repeat("b", 64),
			PlatformQualification: "implementation-qualified-windows-x64",
			ProfileManifest:       strings.Repeat("c", 64),
			RuntimeNodeID:         "rnode_controltest",
			SimulationNodeID:      "snode_controltest",
		})
	case "instance.start":
		if session.startEntered != nil {
			session.startEntered <- struct{}{}
			<-session.releaseStart
		}
		assignment := values["assignment"].(map[string]any)
		return json.Marshal(struct {
			AssignmentFingerprint string `json:"assignmentFingerprint"`
			MappingGeneration     string `json:"mappingGeneration"`
			Replayed              bool   `json:"replayed"`
			Seed                  string `json:"seed"`
			SimulationInstanceID  string `json:"simulationInstanceId"`
			StartRequestID        string `json:"startRequestId"`
		}{
			AssignmentFingerprint: assignment["assignmentFingerprint"].(string),
			MappingGeneration:     values["mappingGeneration"].(string),
			Replayed:              false,
			Seed:                  values["seed"].(string),
			SimulationInstanceID:  session.instanceID,
			StartRequestID:        requestID.String(),
		})
	case "instance.drain":
		return json.Marshal(struct {
			AssignmentFingerprint string `json:"assignmentFingerprint"`
			CommittedTick         string `json:"committedTick"`
			SimulationInstanceID  string `json:"simulationInstanceId"`
			State                 string `json:"state"`
		}{
			AssignmentFingerprint: values["assignmentFingerprint"].(string),
			CommittedTick:         "1",
			SimulationInstanceID:  session.instanceID,
			State:                 "drained",
		})
	case "instance.stop":
		return json.Marshal(struct {
			AssignmentFingerprint string `json:"assignmentFingerprint"`
			State                 string `json:"state"`
			WorldInstanceID       string `json:"worldInstanceId"`
		}{
			AssignmentFingerprint: values["assignmentFingerprint"].(string),
			State:                 "stopped",
			WorldInstanceID:       values["worldInstanceId"].(string),
		})
	case "battle_qualification_snapshot_request":
		receipt := qualificationSnapshotWire{
			ActiveSessionCount:    "0",
			AssignmentFingerprint: values["assignmentFingerprint"].(string),
			CommittedTick:         "7",
			InstalledTicketCount:  "0",
			Metrics:               zeroQualificationMetricsWire(),
			NodeCount:             "1",
			QualificationRunID:    values["qualificationRunId"].(string),
			RunningInstanceCount:  "1",
			SampleSequence:        values["sampleSequence"].(string),
			SimulationInstanceID:  values["simulationInstanceId"].(string),
			SimulationNodeID:      values["simulationNodeId"].(string),
		}
		if session.qualificationMutation == "old-node" {
			receipt.SimulationNodeID = "snode_stalequalification"
		}
		if session.qualificationMutation == "old-sequence" {
			receipt.SampleSequence = "2"
		}
		rawReceipt, marshalErr := json.Marshal(receipt)
		if marshalErr != nil {
			return nil, marshalErr
		}
		if session.qualificationMutation == "unknown-field" {
			var object map[string]any
			if unmarshalErr := json.Unmarshal(rawReceipt, &object); unmarshalErr != nil {
				return nil, unmarshalErr
			}
			object["proofKey"] = strings.Repeat("9", 64)
			return json.Marshal(object)
		}
		return rawReceipt, nil
	default:
		return nil, errors.New("unexpected fake control kind: " + kind + "/" + expected)
	}
}

// TestProbeSkipsBusyLifecycleTurn 验证健康任务不会排队在正常 start 后误报 timeout。
func TestProbeSkipsBusyLifecycleTurn(t *testing.T) {
	t.Parallel()
	session := &fakeControlSession{
		instanceID:   "sinst_abcdefabcdefabcdefabcdefabcdefab",
		closed:       make(chan struct{}),
		startEntered: make(chan struct{}, 1),
		releaseStart: make(chan struct{}),
	}
	controller, err := BootstrapController(context.Background(), session, testControllerConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := testStartingSnapshot(t, "winst_busyprobe", 1, 1)
	startDone := make(chan error, 1)
	go func() {
		startDone <- controller.Start(context.Background(), snapshot)
	}()
	<-session.startEntered
	if err := controller.Probe(context.Background()); !errors.Is(err, ErrProbeBusy) {
		t.Fatalf("Probe() error = %v, want ErrProbeBusy", err)
	}
	close(session.releaseStart)
	if err := <-startDone; err != nil {
		t.Fatal(err)
	}
}

// Send 记录 one-way result ack。
func (session *fakeControlSession) Send(_ context.Context, _ RequestID, kind string, _ any) error {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	session.calls = append(session.calls, kind)
	return nil
}

// Close 进入 terminal 状态。
func (session *fakeControlSession) Close() error {
	select {
	case <-session.closed:
	default:
		close(session.closed)
	}
	return nil
}

// Done 返回 terminal channel。
func (session *fakeControlSession) Done() <-chan struct{} { return session.closed }

// Err 返回 fake terminal failure。
func (session *fakeControlSession) Err() error {
	select {
	case <-session.closed:
		return errors.New("fake session closed")
	default:
		return nil
	}
}

// TestControllerLifecycle 验证 hello、ready target、drain invalidation 与 exact stop。
func TestControllerLifecycle(t *testing.T) {
	t.Parallel()
	session := &fakeControlSession{
		instanceID: "sinst_00112233445566778899aabbccddeeff",
		closed:     make(chan struct{}),
	}
	controller, err := BootstrapController(context.Background(), session, testControllerConfig(t))
	if err != nil {
		t.Fatalf("BootstrapController: %v", err)
	}
	snapshot := testStartingSnapshot(t, "winst_controltest1", 1, 1)
	if err := controller.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("Start: %v", err)
	}
	target, found := controller.ResolveTarget(snapshot.Stamp())
	config := testControllerConfig(t)
	if !found || target.InstanceID.String() != session.instanceID ||
		target.RuntimeNodeID != config.RuntimeNodeID ||
		target.MappingGeneration != snapshot.Generation().Uint64() ||
		target.ModelManifest != config.Build.ModelManifest ||
		target.ProfileManifest != config.Build.ProfileManifest ||
		target.ConfigIdentity != config.ConfigIdentity ||
		target.ActorCapacity != config.Capacity.Actors {
		t.Fatalf("ResolveTarget = %#v, %v", target, found)
	}
	if err := controller.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("replayed Start: %v", err)
	}
	controller.mutex.Lock()
	binding := controller.bindings[snapshot.InstanceID().String()]
	controller.mutex.Unlock()
	proposal := ResultProposal{
		ResultID:              "sresult_controller_binding",
		Kind:                  LifecycleSummaryKind,
		AssignmentFingerprint: binding.fingerprint,
		InstanceID:            binding.instanceID,
		TickStart:             1,
		TickEnd:               2,
		PayloadDigest:         lifecyclePayloadDigest(binding, 2),
		EvidenceDigest:        controller.lifecycleEvidenceDigest(binding, 2),
	}
	proposal.ProposalFingerprint = proposal.CanonicalFingerprint()
	if resolved, found := controller.ResolveProposalBinding(proposal); !found ||
		!resolved.Equal(snapshot.Stamp()) {
		t.Fatal("matching lifecycle evidence did not resolve exact assignment")
	}
	drifted := proposal
	drifted.EvidenceDigest, _ = NewDigest(strings.Repeat("9", 64))
	drifted.ProposalFingerprint = drifted.CanonicalFingerprint()
	if _, found := controller.ResolveProposalBinding(drifted); found {
		t.Fatal("self-consistent but drifted lifecycle evidence was accepted")
	}
	if err := controller.Drain(context.Background(), snapshot.Stamp()); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if _, found := controller.ResolveTarget(snapshot.Stamp()); found {
		t.Fatal("drained target remained available")
	}
	if err := controller.Stop(context.Background(), snapshot.Stamp()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if controller.Contains(snapshot.Stamp()) {
		t.Fatal("stopped binding remained registered")
	}
	if err := controller.Stop(context.Background(), snapshot.Stamp()); err != nil {
		t.Fatalf("replayed Stop: %v", err)
	}
	unknown := testStartingSnapshot(t, "winst_controlunknown", 1, 1)
	if err := controller.Stop(context.Background(), unknown.Stamp()); err == nil {
		t.Fatal("unknown stop was accepted as replay")
	}
	if err := controller.Start(context.Background(), snapshot); err == nil {
		t.Fatal("stopped WorldInstanceID was restarted in the same node incarnation")
	}
}

// TestControllerRejectsStaleStamp 验证旧 generation 不能 drain successor。
func TestControllerRejectsStaleStamp(t *testing.T) {
	t.Parallel()
	session := &fakeControlSession{
		instanceID: "sinst_ffeeddccbbaa99887766554433221100",
		closed:     make(chan struct{}),
	}
	controller, err := BootstrapController(context.Background(), session, testControllerConfig(t))
	if err != nil {
		t.Fatalf("BootstrapController: %v", err)
	}
	snapshot := testStartingSnapshot(t, "winst_controltest2", 2, 2)
	if err := controller.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("Start: %v", err)
	}
	stale := testStartingSnapshot(t, "winst_controltest2", 1, 1)
	if err := controller.Drain(context.Background(), stale.Stamp()); err == nil {
		t.Fatal("stale drain was accepted")
	}
	if _, found := controller.ResolveTarget(snapshot.Stamp()); !found {
		t.Fatal("stale drain invalidated successor")
	}
}

// TestTargetResolverRequiresCurrentActiveLease 验证 ready binding 不能绕过 placement current。
func TestTargetResolverRequiresCurrentActiveLease(t *testing.T) {
	t.Parallel()
	session := &fakeControlSession{
		instanceID: "sinst_1234567890abcdef1234567890abcdef",
		closed:     make(chan struct{}),
	}
	controller, err := BootstrapController(context.Background(), session, testControllerConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	starting := testStartingSnapshot(t, "winst_targetresolver", 3, 4)
	if err := controller.Start(context.Background(), starting); err != nil {
		t.Fatal(err)
	}
	now := starting.CreatedAt().Add(time.Second)
	active, err := starting.Activate(now)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &resultFixture{current: active, now: now}
	resolver, err := NewTargetResolver(fixture, controller, fixture)
	if err != nil {
		t.Fatal(err)
	}
	target, found, err := resolver.Resolve(context.Background(), active.Stamp())
	if err != nil || !found || target.Validate() != nil {
		t.Fatalf("Resolve = %#v, %v, %v", target, found, err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := resolver.Resolve(context.Background(), active.Stamp()); err != nil || found {
		t.Fatalf("terminal child target found=%v err=%v", found, err)
	}
	fixture.current = testStartingSnapshot(t, "winst_targetresolver", 3, 4)
	if _, found, err := resolver.Resolve(context.Background(), active.Stamp()); err != nil || found {
		t.Fatalf("starting current target found=%v err=%v", found, err)
	}
}

// TestSelectCapacityEnforcesEightActorGate 验证 8/9 actor 与 slot 使用。
func TestSelectCapacityEnforcesEightActorGate(t *testing.T) {
	t.Parallel()
	session := &fakeControlSession{
		instanceID: "sinst_abcdefabcdefabcdefabcdefabcdefab",
		closed:     make(chan struct{}),
	}
	controller, err := BootstrapController(context.Background(), session, testControllerConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if !controller.SelectCapacity(8) || controller.SelectCapacity(9) {
		t.Fatal("actor capacity selector drifted")
	}
}

// testControllerConfig 返回完整 B0.3 control registration。
func testControllerConfig(t *testing.T) ControllerConfig {
	t.Helper()
	nodeID, _ := NewSimulationNodeID("snode_controltest")
	runtimeNodeID, _ := placement.NewRuntimeNodeID("rnode_controltest")
	digest := func(value string) Digest {
		result, err := NewDigest(strings.Repeat(value, 64))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	return ControllerConfig{
		NodeID:        nodeID,
		RuntimeNodeID: runtimeNodeID,
		Build: BuildBinding{
			BuildIdentity:         digest("a"),
			ModelManifest:         digest("b"),
			ProfileManifest:       digest("c"),
			PlatformQualification: "implementation-qualified-windows-x64",
		},
		Capacity:           NodeCapacity{Instances: 2, Actors: 8},
		ConfigIdentity:     digest("d"),
		NavigationIdentity: digest("e"),
		PhysicsIdentity:    digest("f"),
		DrainDeadline:      time.Second,
		StopDeadline:       time.Second,
	}
}

// testStartingSnapshot 构造 placement starting assignment。
func testStartingSnapshot(t *testing.T, instanceValue string, generationValue uint64, fenceValue uint64) placement.AssignmentSnapshot {
	t.Helper()
	worldID, _ := personalworld.NewPersonalWorldID("pworld_controltest")
	instanceID, _ := placement.NewWorldInstanceID(instanceValue)
	nodeID, _ := placement.NewRuntimeNodeID("rnode_controltest")
	generation, _ := placement.NewAssignmentGeneration(generationValue)
	fence, _ := placement.NewFencingToken(fenceValue)
	stamp, _ := placement.NewAssignmentStamp(worldID, instanceID, nodeID, generation, fence)
	now := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	snapshot, err := placement.NewAssignmentSnapshot(stamp, placement.PhaseStarting, now, now.Add(time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
