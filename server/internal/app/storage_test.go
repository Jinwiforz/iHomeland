package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jinwiforz/ihomeland/server/internal/buildinfo"
)

// TestRunRejectsMissingStorageSecretBeforeLoggingOrNetwork 保护 secret 缺失在所有可观测副作用前失败。
func TestRunRejectsMissingStorageSecretBeforeLoggingOrNetwork(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	configPath := filepath.Join(directory, "server.yaml")
	admissionKeyPath := filepath.Join(directory, "admission-key")
	if err := os.WriteFile(admissionKeyPath, []byte("test-world-admission-key-material-32-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	configBody := "environment: local\npublicApi:\n  worldAdmission:\n    derivationKeySecret: 'file:" +
		strings.ReplaceAll(admissionKeyPath, "'", "''") + "'\n" + testSimulationControlYAML() +
		"storage:\n  mysql:\n    passwordSecret: env:DEFINITELY_MISSING_STORAGE_SECRET\n"
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	result := Run(context.Background(), Options{
		ConfigPath: configPath,
		LookupEnv:  func(string) (string, bool) { return "", false },
		Output:     &output,
		BuildInfo:  buildinfo.Current(),
	})
	if result.Kind != ResultConfigError {
		t.Fatalf("Run() result = %+v", result)
	}
	if result.Err == nil || !strings.Contains(result.Err.Error(), "storage.mysql.passwordSecret") {
		t.Fatalf("Run() 未定位 MySQL secret boundary：%v", result.Err)
	}
	if output.Len() != 0 {
		t.Fatalf("secret failure 前不应创建 logger 或写日志：%q", output.String())
	}
	if strings.Contains(ResultMessage(result), "DEFINITELY_MISSING_STORAGE_SECRET_VALUE") {
		t.Fatalf("result message 泄露 secret material：%s", ResultMessage(result))
	}
}

// TestRunRejectsDisabledSimulationBeforeSecretsAndListeners 保护可执行 graph 不回退到进程内 runtime。
func TestRunRejectsDisabledSimulationBeforeSecretsAndListeners(t *testing.T) {
	t.Parallel()
	configPath := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(configPath, []byte("environment: local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	result := Run(context.Background(), Options{
		ConfigPath: configPath,
		LookupEnv:  func(string) (string, bool) { return "", false },
		Output:     &output,
		BuildInfo:  buildinfo.Current(),
	})
	if result.Kind != ResultConfigError || !strings.Contains(result.Err.Error(), "simulationControl.enabled") {
		t.Fatalf("Run() result = %+v", result)
	}
	if output.Len() != 0 {
		t.Fatalf("disabled simulation failure 前不应创建 logger 或 listener：%q", output.String())
	}
}

// testSimulationControlYAML 返回只参与启动前配置校验的完整 fake artifact binding。
func testSimulationControlYAML() string {
	return `simulationControl:
  enabled: true
  binaryPath: 'C:\test\ihomeland-sim-server.exe'
  binarySha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  qualificationReceiptPath: 'C:\test\qualification-gate-receipt.json'
  qualificationReceiptSha256: bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
  buildIdentity: cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
  modelManifest: dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
  profileManifest: eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee
  configIdentity: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
  navigationIdentity: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  physicsIdentity: bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
  instanceCapacity: 2
  actorCapacity: 8
  frameBytes: 65536
  pendingRequests: 256
  requestTimeout: 3s
  healthInterval: 5s
  healthTimeout: 1s
  drainTimeout: 3s
  shutdownTimeout: 5s
  stderrLineBytes: 1024
`
}
