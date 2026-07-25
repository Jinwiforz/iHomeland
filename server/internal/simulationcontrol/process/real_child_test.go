package process

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/simulationcontrol"
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
	owner, err := Start(Config{
		BinaryPath: binaryPath, BinarySHA256: fileDigest(t, binaryPath),
		QualificationReceiptPath: receiptPath, QualificationReceiptSHA256: fileDigest(t, receiptPath),
		RequestTimeout: 3 * time.Second, ShutdownTimeout: 3 * time.Second, StderrLineLimit: 1024,
		BattleUDPEnabled: true, BattleUDPBindHost: "127.0.0.1", BattleUDPBindPort: uint16(port),
		BattleUDPAdvertisedHost: "127.0.0.1", BattleUDPAdvertisedPort: uint16(port),
		BattleListenerIdentity: "0123456789abcdef0123456789abcdef",
	}, nonce, simulationcontrol.NewProposalInbox(), diagnostics)
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
		Config{
			BinaryPath:                 binaryPath,
			BinarySHA256:               binaryDigest,
			QualificationReceiptPath:   receiptPath,
			QualificationReceiptSHA256: receiptDigest,
			RequestTimeout:             3 * time.Second,
			ShutdownTimeout:            3 * time.Second,
			StderrLineLimit:            1024,
		},
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
				Config{
					BinaryPath:                 binaryPath,
					BinarySHA256:               testCase.binaryDigest,
					QualificationReceiptPath:   receiptPath,
					QualificationReceiptSHA256: testCase.receiptDigest,
					RequestTimeout:             time.Second,
					ShutdownTimeout:            time.Second,
					StderrLineLimit:            256,
				},
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
		Config{
			BinaryPath:                 binaryPath,
			BinarySHA256:               fileDigest(t, binaryPath),
			QualificationReceiptPath:   receiptPath,
			QualificationReceiptSHA256: fileDigest(t, receiptPath),
			RequestTimeout:             time.Second,
			ShutdownTimeout:            time.Second,
			StderrLineLimit:            256,
		},
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
		Config{
			BinaryPath:                 binaryPath,
			BinarySHA256:               fileDigest(t, binaryPath),
			QualificationReceiptPath:   receiptPath,
			QualificationReceiptSHA256: fileDigest(t, receiptPath),
			RequestTimeout:             3 * time.Second,
			ShutdownTimeout:            3 * time.Second,
			StderrLineLimit:            1024,
		},
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
			"actorCapacity":           8,
			"expectedBuildIdentity":   realBuildIdentity(t),
			"expectedModelManifest":   "65e136d20dfa244db4ce42007cfe1c0411b7f807b635209704b6ef51e93d08b1",
			"expectedProfileManifest": "ca8d0b85e2f1b57d2209e4f376a174c89833ff30b7b3dd694d26c17408be341f",
			"instanceCapacity":        8,
			"runtimeNodeId":           "rnode_realchildtest",
			"simulationNodeId":        "snode_session",
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
			"expectedProfileManifest": "ca8d0b85e2f1b57d2209e4f376a174c89833ff30b7b3dd694d26c17408be341f",
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
			ProfileManifest:       digest("ca8d0b85e2f1b57d2209e4f376a174c89833ff30b7b3dd694d26c17408be341f"),
			PlatformQualification: "implementation-qualified-windows-x64",
		},
		Capacity:           simulationcontrol.NodeCapacity{Instances: 8, Actors: 8},
		ConfigIdentity:     digest(strings.Repeat("d", 64)),
		NavigationIdentity: digest(strings.Repeat("e", 64)),
		PhysicsIdentity:    digest(strings.Repeat("f", 64)),
		DrainDeadline:      time.Second,
		StopDeadline:       time.Second,
	}
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
