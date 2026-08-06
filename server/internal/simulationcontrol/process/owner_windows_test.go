package process

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/simulationcontrol"
)

// TestQualificationConfigRequiresExplicitModeAndSingleCLI 验证生产默认关闭且资格参数唯一。
func TestQualificationConfigRequiresExplicitModeAndSingleCLI(t *testing.T) {
	t.Parallel()
	digest, _ := simulationcontrol.NewDigest(strings.Repeat("a", 64))
	runID, _ := simulationcontrol.NewQualificationRunID(
		"bqrun_0123456789abcdef0123456789abcdef",
	)
	base := Config{
		BinaryPath:                 filepath.Join(t.TempDir(), "sim.exe"),
		BinarySHA256:               digest,
		QualificationReceiptPath:   filepath.Join(t.TempDir(), "receipt.json"),
		QualificationReceiptSHA256: digest,
		RequestTimeout:             time.Second,
		ShutdownTimeout:            time.Second,
		StderrLineLimit:            256,
		GameplayPackageRoot:        filepath.Join(t.TempDir(), "personal-world-combat-v1"),
		GameplayArenaRoot:          filepath.Join(t.TempDir(), "personal-world-combat-arena-v1"),
		GameplayPackageID:          "personal-world-combat-v1",
		ConfigIdentity:             digest,
		NavigationIdentity:         digest,
		PhysicsIdentity:            digest,
		WireIdentity:               digest,
	}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	if arguments := processArguments(base); len(arguments) != 15 ||
		arguments[0] != "--control-stdio" {
		t.Fatalf("production arguments = %v", arguments)
	}
	runOnly := base
	runOnly.QualificationRunID = runID
	if err := runOnly.Validate(); err == nil {
		t.Fatal("qualification run without explicit mode was accepted")
	}
	modeOnly := base
	modeOnly.QualificationMode = true
	if err := modeOnly.Validate(); err == nil {
		t.Fatal("qualification mode without run identity was accepted")
	}
	qualified := base
	qualified.QualificationMode = true
	qualified.QualificationRunID = runID
	if err := qualified.Validate(); err != nil {
		t.Fatal(err)
	}
	arguments := processArguments(qualified)
	if len(arguments) != 17 ||
		arguments[15] != "--battle-qualification-run-id" ||
		arguments[16] != runID.String() {
		t.Fatalf("qualification arguments = %v", arguments)
	}
}

// TestPumpStderrBoundsPressure 验证大量长行不会阻塞且输出始终截断、去控制字符。
func TestPumpStderrBoundsPressure(t *testing.T) {
	t.Parallel()
	var input strings.Builder
	for index := 0; index < 512; index++ {
		input.WriteString(strings.Repeat("x", 256))
		input.WriteByte(0x01)
		input.WriteString("\n")
	}
	sink := &lockedDiagnosticSink{}
	pumpStderr(io.NopCloser(strings.NewReader(input.String())), 64, sink)
	lines := sink.snapshot()
	if len(lines) != 512 {
		t.Fatalf("stderr lines=%d", len(lines))
	}
	for _, line := range lines {
		if len(line) > 64 || strings.ContainsRune(line, 0x01) {
			t.Fatalf("stderr line violated bound: %q", line)
		}
	}
}

// TestVerifyFileRejectsDigestDrift 验证 artifact mismatch 在启动 child 前 fail closed。
func TestVerifyFileRejectsDigestDrift(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "artifact.bin")
	if err := os.WriteFile(path, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	wrong, _ := simulationcontrol.NewDigest(strings.Repeat("a", 64))
	if err := verifyFile(path, wrong); err == nil {
		t.Fatal("digest drift was accepted")
	}
}

// TestPipeCloserIsIdempotent 验证两个继承 handle 各关闭一次并稳定重放首次结果。
func TestPipeCloserIsIdempotent(t *testing.T) {
	t.Parallel()
	expected := errors.New("close failed")
	first := &countingCloser{}
	second := &countingCloser{err: expected}
	closer := &pipeCloser{stdin: first, stdout: second}
	if err := closer.Close(); !errors.Is(err, expected) {
		t.Fatalf("first Close=%v", err)
	}
	if err := closer.Close(); !errors.Is(err, expected) {
		t.Fatalf("replayed Close=%v", err)
	}
	if first.count != 1 || second.count != 1 {
		t.Fatalf("close counts=%d/%d", first.count, second.count)
	}
}

// lockedDiagnosticSink 保存并发安全的低敏测试输出。
type lockedDiagnosticSink struct {
	// mutex 保护 lines。
	mutex sync.Mutex
	// lines 是已清洗输出。
	lines []string
}

// ObserveSimulationDiagnostic 保存一行。
func (sink *lockedDiagnosticSink) ObserveSimulationDiagnostic(value string) {
	sink.mutex.Lock()
	defer sink.mutex.Unlock()
	sink.lines = append(sink.lines, value)
}

// snapshot 返回输出副本。
func (sink *lockedDiagnosticSink) snapshot() []string {
	sink.mutex.Lock()
	defer sink.mutex.Unlock()
	return append([]string(nil), sink.lines...)
}

// countingCloser 记录 handle close 次数。
type countingCloser struct {
	// count 是调用次数。
	count int
	// err 是稳定返回错误。
	err error
}

// Close 记录调用并返回预置错误。
func (closer *countingCloser) Close() error {
	closer.count++
	return closer.err
}
