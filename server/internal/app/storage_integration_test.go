//go:build storage_integration

package app

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/buildinfo"
)

// TestStorageRequiredReadinessDrainingAndProcessRecovery 验证 storage 启动/运行失败、逆序关闭和新进程恢复。
func TestStorageRequiredReadinessDrainingAndProcessRecovery(t *testing.T) {
	if os.Getenv("IHOMELAND_STORAGE_INTEGRATION") != "1" {
		t.Skip("storage integration harness is required")
	}
	diagnosticAddress := reserveAddress(t)
	configPath := writeIntegrationConfig(t, diagnosticAddress)
	redisContainer := os.Getenv("IHOMELAND_TEST_REDIS_CONTAINER")

	runDocker(t, "stop", "--time", "1", redisContainer)
	failed := Run(context.Background(), integrationOptions(configPath))
	if failed.Kind != ResultStartupError {
		t.Fatalf("missing Redis result = %+v", failed)
	}
	assertAddressReusable(t, diagnosticAddress)
	runDocker(t, "start", redisContainer)
	waitForContainerHealthy(t, redisContainer)

	ctx, cancel := context.WithCancelCause(context.Background())
	resultChannel := make(chan Result, 1)
	go func() { resultChannel <- Run(ctx, integrationOptions(configPath)) }()
	waitForReady(t, diagnosticAddress)
	runDocker(t, "stop", "--time", "1", redisContainer)
	select {
	case result := <-resultChannel:
		if result.Kind != ResultRuntimeError {
			t.Fatalf("probe failure result = %+v", result)
		}
	case <-time.After(10 * time.Second):
		cancel(context.DeadlineExceeded)
		t.Fatal("required Redis probe 未触发单向 draining")
	}
	cancel(ErrSignalShutdown)
	assertAddressReusable(t, diagnosticAddress)
	runDocker(t, "start", redisContainer)
	waitForContainerHealthy(t, redisContainer)

	recoveryContext, stopRecovery := context.WithCancelCause(context.Background())
	recoveryResult := make(chan Result, 1)
	go func() { recoveryResult <- Run(recoveryContext, integrationOptions(configPath)) }()
	waitForReady(t, diagnosticAddress)
	stopRecovery(ErrSignalShutdown)
	if result := <-recoveryResult; result.Kind != ResultClean {
		t.Fatalf("recovered process result = %+v", result)
	}
	assertAddressReusable(t, diagnosticAddress)
}

// integrationOptions 使用真实 production graph，只将日志丢弃以保持测试输出稳定。
func integrationOptions(configPath string) Options {
	return Options{ConfigPath: configPath, Output: io.Discard, BuildInfo: buildinfo.Current()}
}

// writeIntegrationConfig 将 harness endpoint 与 file secret reference 写入测试私有临时目录。
func writeIntegrationConfig(t *testing.T, diagnosticAddress string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server.yaml")
	body := fmt.Sprintf(`environment: local
runtime:
  startupTimeout: 3s
  shutdownTimeout: 3s
diagnostic:
  address: %s
storage:
  mysql:
    address: %s
    passwordSecret: 'file:%s'
    probe:
      interval: 100ms
      timeout: 50ms
      failureThreshold: 2
  redis:
    address: %s
    passwordSecret: 'file:%s'
    probe:
      interval: 100ms
      timeout: 50ms
      failureThreshold: 2
`, diagnosticAddress, os.Getenv("IHOMELAND_TEST_MYSQL_ADDRESS"), os.Getenv("IHOMELAND_TEST_MYSQL_PASSWORD_FILE"), os.Getenv("IHOMELAND_TEST_REDIS_ADDRESS"), os.Getenv("IHOMELAND_TEST_REDIS_PASSWORD_FILE"))
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// reserveAddress 让 OS 选择 loopback 端口后立即释放，供单进程 listener rollback 验收使用。
func reserveAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

// waitForReady 有界轮询 diagnostic readyz，不把 listener 启动等同于 graph ready。
func waitForReady(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + address + "/readyz")
		if err == nil {
			// 测试只消费状态码；关闭只读响应失败不改变 readiness 断言。
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("process did not become ready")
}

// assertAddressReusable 证明 diagnostic 在逆序关闭的最后阶段已释放端口。
func assertAddressReusable(t *testing.T, address string) {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("diagnostic listener 未最后关闭：%v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("关闭复用性探针 listener 失败：%v", err)
	}
}

// runDocker 只执行 harness 已登记 container 的 stop/start fault injection。
func runDocker(t *testing.T, arguments ...string) {
	t.Helper()
	command := exec.Command("docker", arguments...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("docker operation failed: %v (%s)", err, output)
	}
}

// waitForContainerHealthy 在新进程恢复前确认 Docker dependency 已完成自身初始化。
func waitForContainerHealthy(t *testing.T, name string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		command := exec.Command("docker", "inspect", "--format", "{{.State.Health.Status}}", name)
		output, err := command.Output()
		if err == nil && string(output) == "healthy\n" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("container did not become healthy")
}
