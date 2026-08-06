package app

import (
	"bytes"
	"context"
	"fmt"
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
	if err := os.WriteFile(admissionKeyPath, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	configBody := "environment: local\npublicApi:\n  worldAdmission:\n    derivationKeySecret: 'file:" +
		strings.ReplaceAll(admissionKeyPath, "'", "''") + "'\n  battleUdp:\n    derivationKeySecret: 'file:" +
		strings.ReplaceAll(admissionKeyPath, "'", "''") + "'\n" + testSimulationControlYAML(t) +
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

// TestRunRejectsGameplaySourceDriftBeforeSecretsAndListeners 验证 selector failure 不产生部分启动副作用。
func TestRunRejectsGameplaySourceDriftBeforeSecretsAndListeners(t *testing.T) {
	t.Parallel()
	configuration := "environment: local\n" + testSimulationControlYAML(t)
	configuration = strings.ReplaceAll(
		configuration,
		"d6e3f016e4c29f4ad3916759e5ea059443fe37145dbe51621704180e50bc555b",
		strings.Repeat("0", 64),
	)
	configPath := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(configPath, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	result := Run(context.Background(), Options{
		ConfigPath: configPath,
		LookupEnv:  func(string) (string, bool) { return "", false },
		Output:     &output,
		BuildInfo:  buildinfo.Current(),
	})
	if result.Kind != ResultConfigError ||
		!strings.Contains(result.Err.Error(), "source identity differs") {
		t.Fatalf("Run() result = %+v", result)
	}
	if output.Len() != 0 || strings.Contains(result.Err.Error(), "personal-world-combat-v1\\") {
		t.Fatalf("selector failure leaked side effect or path: output=%q error=%v", output.String(), result.Err)
	}
}

// testSimulationControlYAML 返回只参与启动前配置校验的完整 fake artifact binding。
func testSimulationControlYAML(t *testing.T) string {
	t.Helper()
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	packageRoot := filepath.Join(repositoryRoot, "shared", "contracts", "gameplay", "battle", "packages", "personal-world-combat-v1")
	arenaRoot := filepath.Join(repositoryRoot, "simulation", "content", "personal-world-combat-v1")
	return fmt.Sprintf(`simulationControl:
  enabled: true
  binaryPath: 'C:\test\ihomeland-sim-server.exe'
  binarySha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  qualificationReceiptPath: 'C:\test\qualification-gate-receipt.json'
  qualificationReceiptSha256: bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
  buildIdentity: cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
  modelManifest: 65e136d20dfa244db4ce42007cfe1c0411b7f807b635209704b6ef51e93d08b1
  profileManifest: c7ff3d1f582625d18028c4ce20fb808b2e56e10084c4ccf61790b0c5f486c424
  configIdentity: d6e3f016e4c29f4ad3916759e5ea059443fe37145dbe51621704180e50bc555b
  navigationIdentity: 14165efe5b47caf6c7c8e2f9a4794c4d238aa4badeedc3d5badb41a9c2d5d19f
  physicsIdentity: 64d53e1803d7cb14f4953ba1978175b162577eeb064716b2630cad0acf4de02e
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
gameplayPackage:
  rootPath: '%s'
  arenaRootPath: '%s'
  packageId: personal-world-combat-v1
  configIdentity: d6e3f016e4c29f4ad3916759e5ea059443fe37145dbe51621704180e50bc555b
  navigationIdentity: 14165efe5b47caf6c7c8e2f9a4794c4d238aa4badeedc3d5badb41a9c2d5d19f
  physicsIdentity: 64d53e1803d7cb14f4953ba1978175b162577eeb064716b2630cad0acf4de02e
  wireIdentity: 9a40facbb23aafc556d38b403c8f8b1e264e0f2d9414e32da434b11d07554432
`, strings.ReplaceAll(packageRoot, "'", "''"), strings.ReplaceAll(arenaRoot, "'", "''"))
}
