package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// processStartAttempts 只吸收临时端口释放到子进程重绑之间的抢占，不掩盖其他启动失败。
const processStartAttempts = 3

// testProcess 保存已就绪子进程及其分离输出，确保测试只在进程退出后读取 buffer。
type testProcess struct {
	// command 是可发送平台关闭请求的真实服务端进程。
	command *exec.Cmd
	// address 是本次配置选择的诊断探针地址。
	address string
	// output 收集正常结构化日志。
	output *bytes.Buffer
	// errorOutput 收集进程级失败诊断。
	errorOutput *bytes.Buffer
	// wait 只接收一次真实进程退出结果。
	wait <-chan error
}

// TestRunRejectsInvalidConfig 验证薄入口把启动期配置错误稳定映射为退出码 2。
func TestRunRejectsInvalidConfig(t *testing.T) {
	var output bytes.Buffer
	signals := make(chan os.Signal)
	code := run([]string{"--config", filepath.Join(t.TempDir(), "missing.yaml")}, io.Discard, &output, signals)
	if code != 2 || !strings.Contains(output.String(), "config_error") {
		t.Fatalf("unexpected config failure: code=%d output=%q", code, output.String())
	}
}

// TestRunSecondSignalForcesExit 验证第二次终止请求不受失控 shutdown deadline 阻塞。
func TestRunSecondSignalForcesExit(t *testing.T) {
	configPath, _ := writeProcessConfig(t)
	signals := make(chan os.Signal, 2)
	signals <- syscall.SIGTERM
	signals <- syscall.SIGTERM
	var output bytes.Buffer
	if code := run([]string{"--config", configPath}, &output, &output, signals); code != forcedExitCode {
		t.Fatalf("second signal returned %d, output=%q", code, output.String())
	}
}

// TestServerProcessLifecycle 构建真实 binary 并验证 ready、signal、零退出、日志和端口释放。
func TestServerProcessLifecycle(t *testing.T) {
	if os.Getenv("IHOMELAND_STORAGE_INTEGRATION") != "1" {
		// cmd/server owner：ready 现依赖真实 storage；统一 harness 提供资源并执行本测试，普通单元门不隐式连接本机服务。
		t.Skip("cmd/server owner: run tools/storage/storage.ps1 verify to satisfy required storage readiness")
	}
	executable := buildServer(t)
	process := startReadyProcess(t, executable, func() (string, string) { return writeProcessConfig(t) })
	if err := requestProcessShutdown(process.command.Process); err != nil {
		if killErr := process.command.Process.Kill(); killErr != nil {
			t.Logf("kill process after shutdown request failure: %v", killErr)
		}
		<-process.wait
		t.Fatalf("request graceful shutdown: %v", err)
	}
	select {
	case err := <-process.wait:
		if err != nil {
			t.Fatalf("server process did not exit cleanly: %v\nstdout:\n%s\nstderr:\n%s", err, process.output.String(), process.errorOutput.String())
		}
	case <-time.After(10 * time.Second):
		if err := process.command.Process.Kill(); err != nil {
			t.Logf("kill process after shutdown deadline: %v", err)
		}
		t.Fatal("server process did not stop before deadline")
	}
	if !strings.Contains(process.output.String(), "server runtime ready") || !strings.Contains(process.output.String(), "server runtime stopped") {
		t.Fatalf("process logs lack lifecycle evidence:\n%s", process.output.String())
	}
	for _, line := range strings.Split(strings.TrimSpace(process.output.String()), "\n") {
		if count := strings.Count(line, "component="); count != 1 {
			t.Fatalf("runtime log must contain exactly one component field, got %d:\n%s", count, line)
		}
	}
	if process.errorOutput.Len() != 0 {
		t.Fatalf("clean lifecycle wrote unexpected stderr:\n%s", process.errorOutput.String())
	}
	listener, err := net.Listen("tcp", process.address)
	if err != nil {
		t.Fatalf("diagnostic port was not released: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestServerProcessInvalidConfig 验证真实 binary 在创建 listener 前以配置错误退出。
func TestServerProcessInvalidConfig(t *testing.T) {
	executable := buildServer(t)
	path := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(path, []byte("unknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "--config", path)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("invalid process configuration should fail")
	}
	exitError, ok := err.(*exec.ExitError)
	if !ok || exitError.ExitCode() != 2 || !strings.Contains(string(output), "config_error") {
		t.Fatalf("unexpected invalid config result: %v %q", err, output)
	}
}

// TestServerProcessShutdownTimeout 使用半截 header 保持活跃连接，验证总关闭预算会强制失败退出。
func TestServerProcessShutdownTimeout(t *testing.T) {
	if os.Getenv("IHOMELAND_STORAGE_INTEGRATION") != "1" {
		// cmd/server owner：只有真实 graph ready 后才能验证 shutdown timeout；由 storage verify 负责恢复该前置条件。
		t.Skip("cmd/server owner: run tools/storage/storage.ps1 verify to test process shutdown with required storage")
	}
	executable := buildServer(t)
	process := startReadyProcess(t, executable, func() (string, string) {
		return writeProcessConfigWithTimeouts(t, 20*time.Millisecond, 5*time.Second)
	})
	connection, err := net.Dial("tcp", process.address)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := io.WriteString(connection, "GET /healthz HTTP/1.1\r\nHost:"); err != nil {
		t.Fatal(err)
	}
	if err := requestProcessShutdown(process.command.Process); err != nil {
		if killErr := process.command.Process.Kill(); killErr != nil {
			t.Logf("kill process after shutdown request failure: %v", killErr)
		}
		<-process.wait
		t.Fatal(err)
	}
	select {
	case err := <-process.wait:
		exitError, ok := err.(*exec.ExitError)
		combined := process.output.String() + process.errorOutput.String()
		if !ok || exitError.ExitCode() != 5 || !strings.Contains(combined, "shutdown_error") {
			t.Fatalf("unexpected shutdown timeout: %v\n%s", err, combined)
		}
	case <-time.After(10 * time.Second):
		if err := process.command.Process.Kill(); err != nil {
			t.Logf("kill process after shutdown timeout: %v", err)
		}
		t.Fatal("shutdown timeout did not terminate process")
	}
}

// buildServer 使用当前受管 Go SDK 构建真实测试 binary。
func buildServer(t *testing.T) string {
	t.Helper()
	name := "ihomeland-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	executable := filepath.Join(t.TempDir(), name)
	command := exec.Command("go", "build", "-o", executable, "./cmd/server")
	command.Dir = filepath.Clean(filepath.Join("..", ".."))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build server: %v\n%s", err, output)
	}
	return executable
}

// writeProcessConfig 创建使用动态空闲端口的标准进程测试配置。
func writeProcessConfig(t *testing.T) (string, string) {
	return writeProcessConfigWithTimeouts(t, 5*time.Second, time.Second)
}

// writeProcessConfigWithTimeouts 允许 shutdown 测试独立控制总预算和慢 header 时间。
func writeProcessConfigWithTimeouts(t *testing.T, shutdownTimeout time.Duration, readHeaderTimeout time.Duration) (string, string) {
	t.Helper()
	address := reserveProcessAddress(t)
	publicAddress := reserveProcessAddress(t)
	contents := fmt.Sprintf("environment: test\nruntime:\n  startupTimeout: 5s\n  shutdownTimeout: %s\nlogging:\n  level: info\n  format: text\ndiagnostic:\n  address: %s\n  readHeaderTimeout: %s\n  readTimeout: 10s\n  writeTimeout: 2s\n  idleTimeout: 2s\n  maxHeaderBytes: 4096\npublicApi:\n  address: %s\n", shutdownTimeout, address, readHeaderTimeout, publicAddress)
	if os.Getenv("IHOMELAND_STORAGE_INTEGRATION") == "1" {
		contents += fmt.Sprintf("storage:\n  mysql:\n    address: %s\n    passwordSecret: 'file:%s'\n  redis:\n    address: %s\n    passwordSecret: 'file:%s'\n", os.Getenv("IHOMELAND_TEST_MYSQL_ADDRESS"), os.Getenv("IHOMELAND_TEST_MYSQL_PASSWORD_FILE"), os.Getenv("IHOMELAND_TEST_REDIS_ADDRESS"), os.Getenv("IHOMELAND_TEST_REDIS_PASSWORD_FILE"))
	}
	path := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, address
}

// reserveProcessAddress 让OS选择并释放独立loopback端口，避免诊断与公开listener冲突。
func reserveProcessAddress(t *testing.T) string {
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

// startReadyProcess 启动真实 binary，并只对临时地址被抢占执行有界重试。
func startReadyProcess(t *testing.T, executable string, configFactory func() (string, string)) *testProcess {
	t.Helper()
	for attempt := 1; attempt <= processStartAttempts; attempt++ {
		configPath, address := configFactory()
		output := new(bytes.Buffer)
		errorOutput := new(bytes.Buffer)
		command := exec.Command(executable, "--config", configPath)
		command.Env = append(os.Environ(), "IHOMELAND_WORLD_ADMISSION_KEY=cmd-server-integration-admission-key")
		prepareProcess(command)
		command.Stdout = output
		command.Stderr = errorOutput
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		wait := make(chan error, 1)
		go func() { wait <- command.Wait() }()
		if err := waitUntilReady(address, wait); err == nil {
			return &testProcess{command: command, address: address, output: output, errorOutput: errorOutput, wait: wait}
		} else if attempt == processStartAttempts || !strings.Contains(errorOutput.String(), "listen on diagnostic address") {
			t.Fatalf("server did not become ready: %v\nstdout:\n%s\nstderr:\n%s", err, output.String(), errorOutput.String())
		}
	}
	t.Fatal("server start attempts exhausted")
	return nil
}

// waitUntilReady 轮询权威探针，并在进程提前退出时返回原因而非依赖固定 sleep。
func waitUntilReady(address string, processExit <-chan error) error {
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	url := "http://" + address + "/readyz"
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for {
		select {
		case err := <-processExit:
			return fmt.Errorf("server exited before ready: %w", err)
		case <-deadline.C:
			return errors.New("server did not become ready before deadline")
		case <-ticker.C:
			response, err := client.Get(url)
			if err != nil {
				continue
			}
			_, copyErr := io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			closeErr := response.Body.Close()
			if copyErr != nil || closeErr != nil {
				return fmt.Errorf("read readiness response: copy=%v close=%v", copyErr, closeErr)
			}
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
	}
}
