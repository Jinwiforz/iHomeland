package simulationcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

// zeroQualificationMetricsWire 返回全部字段显式登记的规范零值。
func zeroQualificationMetricsWire() qualificationMetricsWire {
	return qualificationMetricsWire{
		CloseAuthentication:       "0",
		CloseInternal:             "0",
		CloseLifecycle:            "0",
		CloseNormal:               "0",
		CloseResource:             "0",
		CloseTimeout:              "0",
		CloseTransport:            "0",
		DroppedPackets:            "0",
		EgressQueueHighWatermark:  "0",
		ExpiredMessages:           "0",
		IngressQueueHighWatermark: "0",
		InstanceMemoryBytes:       "0",
		HistoryMemoryBytes:        "0",
		KCPEgressBytes:            "0",
		KCPEgressPackets:          "0",
		KCPIngressBytes:           "0",
		KCPIngressPackets:         "0",
		KCPQueueHighWatermark:     "0",
		KCPRetransmits:            "0",
		MaximumTickDurationNS:     "0",
		RawEgressBytes:            "0",
		RawEgressPackets:          "0",
		RawIngressBytes:           "0",
		RawIngressPackets:         "0",
		Rebinds:                   "0",
		RejectedPackets:           "0",
		Rekeys:                    "0",
		TickDebtHighWatermark:     "0",
	}
}

// TestQualificationSnapshotWireDecodesCrossLanguageGolden 验证 typed decoder 的
// 字段顺序与 C++ canonical JSON 一致，避免新增 metric 只在真实 stdio 时失败。
func TestQualificationSnapshotWireDecodesCrossLanguageGolden(t *testing.T) {
	t.Parallel()
	path := filepath.Join(
		"..", "..", "..", "shared", "contracts", "fixtures",
		"simulation-control", "canonical-golden.json",
	)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Frames []struct {
			GoldenID      string `json:"goldenId"`
			CanonicalJSON string `json:"canonicalJson"`
		} `json:"frames"`
	}
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatal(err)
	}
	for _, golden := range document.Frames {
		if golden.GoldenID != "qualification-snapshot-receipt" {
			continue
		}
		var frame struct {
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal([]byte(golden.CanonicalJSON), &frame); err != nil {
			t.Fatal(err)
		}
		var wire qualificationSnapshotWire
		if err := decodeClosedPayload(frame.Payload, &wire); err != nil {
			t.Fatalf("decode qualification golden: %v", err)
		}
		return
	}
	t.Fatal("qualification snapshot receipt golden is missing")
}

// TestQualificationSnapshotSerializesConcurrentSamples 验证并发读取只推进连续 sample。
func TestQualificationSnapshotSerializesConcurrentSamples(t *testing.T) {
	t.Parallel()
	runID, err := NewQualificationRunID(
		"bqrun_0123456789abcdef0123456789abcdef",
	)
	if err != nil {
		t.Fatal(err)
	}
	session := &fakeControlSession{
		instanceID: "sinst_0123456789abcdef0123456789abcdef",
		closed:     make(chan struct{}),
	}
	config := testControllerConfig(t)
	config.QualificationMode = true
	config.QualificationRunID = runID
	controller, err := BootstrapController(
		context.Background(),
		session,
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	starting := testStartingSnapshot(
		t,
		"winst_qualificationsnapshot",
		1,
		1,
	)
	if err := controller.Start(context.Background(), starting); err != nil {
		t.Fatal(err)
	}

	const concurrentSamples = 8
	sequences := make(chan uint64, concurrentSamples)
	failures := make(chan error, concurrentSamples)
	var group sync.WaitGroup
	for index := 0; index < concurrentSamples; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			snapshot, snapshotErr := controller.QualificationSnapshot(
				context.Background(),
				runID,
				starting.Stamp(),
			)
			if snapshotErr != nil {
				failures <- snapshotErr
				return
			}
			if snapshot.NodeID != config.NodeID ||
				snapshot.InstanceID.String() != session.instanceID ||
				snapshot.CommittedTick != 7 ||
				snapshot.NodeCount != 1 ||
				snapshot.RunningInstanceCount != 1 {
				failures <- errors.New(
					"qualification snapshot binding drifted",
				)
				return
			}
			sequences <- snapshot.SampleSequence
		}()
	}
	group.Wait()
	close(failures)
	for failure := range failures {
		t.Fatal(failure)
	}
	close(sequences)
	actual := make([]int, 0, concurrentSamples)
	for sequence := range sequences {
		actual = append(actual, int(sequence))
	}
	sort.Ints(actual)
	for index, sequence := range actual {
		if sequence != index+1 {
			t.Fatalf("sample sequences = %v", actual)
		}
	}
}

// TestQualificationSnapshotRejectsDisabledAndStaleRun 验证生产与旧 run 不进入 pipe。
func TestQualificationSnapshotRejectsDisabledAndStaleRun(t *testing.T) {
	t.Parallel()
	runID, _ := NewQualificationRunID(
		"bqrun_0123456789abcdef0123456789abcdef",
	)
	staleRunID, _ := NewQualificationRunID(
		"bqrun_fedcba9876543210fedcba9876543210",
	)
	session := &fakeControlSession{
		instanceID: "sinst_abcdefabcdefabcdefabcdefabcdefab",
		closed:     make(chan struct{}),
	}
	config := testControllerConfig(t)
	controller, err := BootstrapController(
		context.Background(),
		session,
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	starting := testStartingSnapshot(
		t,
		"winst_qualificationdisabled",
		1,
		1,
	)
	if err := controller.Start(context.Background(), starting); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.QualificationSnapshot(
		context.Background(),
		runID,
		starting.Stamp(),
	); err == nil {
		t.Fatal("production controller accepted qualification snapshot")
	}

	sessionEnabled := &fakeControlSession{
		instanceID: "sinst_fedcbafedcbafedcbafedcbafedcbafe",
		closed:     make(chan struct{}),
	}
	config.QualificationMode = true
	config.QualificationRunID = runID
	controllerEnabled, err := BootstrapController(
		context.Background(),
		sessionEnabled,
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	startingEnabled := testStartingSnapshot(
		t,
		"winst_qualificationstalerun",
		1,
		1,
	)
	if err := controllerEnabled.Start(
		context.Background(),
		startingEnabled,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := controllerEnabled.QualificationSnapshot(
		context.Background(),
		staleRunID,
		startingEnabled.Stamp(),
	); err == nil {
		t.Fatal("stale qualification run was accepted")
	}
}

// TestQualificationSnapshotRejectsMalformedReceipts 验证 unknown/旧身份/乱序均终结 controller。
func TestQualificationSnapshotRejectsMalformedReceipts(t *testing.T) {
	t.Parallel()
	runID, _ := NewQualificationRunID(
		"bqrun_0123456789abcdef0123456789abcdef",
	)
	for _, testCase := range []struct {
		// name 选择 fake receipt mutation。
		name string
		// instanceID 保证并行 subtest 使用独立 placement identity。
		instanceID string
	}{
		{name: "unknown-field", instanceID: "winst_qualificationnegativea"},
		{name: "old-node", instanceID: "winst_qualificationnegativeb"},
		{name: "old-sequence", instanceID: "winst_qualificationnegativec"},
	} {
		testCase := testCase
		mutation := testCase.name
		t.Run(mutation, func(t *testing.T) {
			session := &fakeControlSession{
				instanceID:            "sinst_00112233445566778899aabbccddeeff",
				closed:                make(chan struct{}),
				qualificationMutation: mutation,
			}
			config := testControllerConfig(t)
			config.QualificationMode = true
			config.QualificationRunID = runID
			controller, err := BootstrapController(
				context.Background(),
				session,
				config,
			)
			if err != nil {
				t.Fatal(err)
			}
			starting := testStartingSnapshot(
				t,
				testCase.instanceID,
				1,
				1,
			)
			if err := controller.Start(
				context.Background(),
				starting,
			); err != nil {
				t.Fatal(err)
			}
			if _, err := controller.QualificationSnapshot(
				context.Background(),
				runID,
				starting.Stamp(),
			); err == nil {
				t.Fatal("malformed qualification receipt was accepted")
			}
			if controller.Healthy() {
				t.Fatal("malformed qualification receipt kept controller healthy")
			}
		})
	}
}
