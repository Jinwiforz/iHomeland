package diagnostic

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/buildinfo"
	"github.com/jinwiforz/ihomeland/server/internal/config"
	"github.com/jinwiforz/ihomeland/server/internal/observability"
)

// fakeState 允许测试原子切换诊断可见状态。
type fakeState struct {
	// mu 保护 handler 并发读取。
	mu sync.RWMutex
	// state 是响应中的稳定状态名。
	state string
	// ready 决定 readyz HTTP status。
	ready bool
}

// DiagnosticState 返回一次加锁状态快照。
func (state *fakeState) DiagnosticState() (string, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.state, state.ready
}

// set 原子替换诊断状态，使单个 listener 可以覆盖全部 readiness 可见阶段。
func (state *fakeState) set(value string, ready bool) {
	state.mu.Lock()
	state.state, state.ready = value, ready
	state.mu.Unlock()
}

// fakeTasks 为诊断 component 模拟 owner 级取消和等待。
type fakeTasks struct {
	// ctx 传递给 Serve task。
	ctx context.Context
	// cancel 由 Stop 调用。
	cancel context.CancelFunc
	// wait 确认 Serve task 已退出。
	wait sync.WaitGroup
}

// newFakeTasks 创建独立测试 owner context。
func newFakeTasks() *fakeTasks {
	ctx, cancel := context.WithCancel(context.Background())
	return &fakeTasks{ctx: ctx, cancel: cancel}
}

// Go 启动并登记单个测试任务。
func (tasks *fakeTasks) Go(_ string, task func(context.Context) error) error {
	tasks.wait.Add(1)
	// 该 fake 只验证 HTTP lifecycle；Serve 的失败传播由真实 TaskGroup 测试覆盖。
	go func() { defer tasks.wait.Done(); _ = task(tasks.ctx) }()
	return nil
}

// Stop 取消 owner 并等待任务退出。
func (tasks *fakeTasks) Stop(context.Context, error) error {
	tasks.cancel()
	tasks.wait.Wait()
	return nil
}

// TestDiagnosticLifecycleAndRoutes 验证真实端口、探针状态、method 边界、metrics 与端口释放。
func TestDiagnosticLifecycleAndRoutes(t *testing.T) {
	state := &fakeState{state: "starting"}
	settings := config.Default().Diagnostic
	settings.Address = "127.0.0.1:0"
	server := New(settings, state, buildinfo.Info{Version: "test", Commit: "fixture"}, observability.NewMetrics(), newFakeTasks())
	if server.server.ReadHeaderTimeout != settings.ReadHeaderTimeout || server.server.MaxHeaderBytes != settings.MaxHeaderBytes {
		t.Fatal("diagnostic resource limits were not applied")
	}
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	baseURL := "http://" + server.Address()
	assertStatus(t, http.MethodGet, baseURL+"/healthz", http.StatusOK, "alive")
	assertStatus(t, http.MethodGet, baseURL+"/readyz", http.StatusServiceUnavailable, "starting")
	state.set("ready", true)
	assertStatus(t, http.MethodGet, baseURL+"/readyz", http.StatusOK, "ready")
	assertHeadWithoutBody(t, baseURL+"/readyz", http.StatusOK)
	state.set("draining", false)
	assertStatus(t, http.MethodGet, baseURL+"/readyz", http.StatusServiceUnavailable, "draining")
	state.set("stopped", false)
	assertStatus(t, http.MethodGet, baseURL+"/readyz", http.StatusServiceUnavailable, "stopped")
	assertStatus(t, http.MethodGet, baseURL+"/version", http.StatusOK, "fixture")
	assertStatus(t, http.MethodPost, baseURL+"/healthz", http.StatusMethodNotAllowed, "method not allowed")
	assertChunkedBodyRejected(t, baseURL+"/healthz")
	assertStatus(t, http.MethodGet, baseURL+"/business", http.StatusNotFound, "404")
	assertStatus(t, http.MethodGet, baseURL+"/metrics", http.StatusOK, "ihomeland_server_diagnostic_requests_total")
	response, err := http.Get(baseURL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	metricsBody, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	closeErr := response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if len(metricsBody) > 1<<20 {
		t.Fatal("diagnostic metrics response exceeded one MiB")
	}
	if strings.Contains(string(metricsBody), "/business") {
		t.Fatal("raw unknown path leaked into metrics label")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodGet, baseURL+"/healthz", nil)
	if _, err := http.DefaultClient.Do(request); err == nil {
		t.Fatal("diagnostic port remained open after Stop")
	}
}

// assertHeadWithoutBody 验证 HEAD 保留 GET 状态语义但不发送响应正文。
func assertHeadWithoutBody(t *testing.T, url string, status int) {
	t.Helper()
	request, err := http.NewRequest(http.MethodHead, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read HEAD response: read=%v close=%v", readErr, closeErr)
	}
	if response.StatusCode != status || len(body) != 0 {
		t.Fatalf("HEAD returned status=%d body=%q", response.StatusCode, body)
	}
}

// assertChunkedBodyRejected 防止未知 Content-Length 的请求体绕过诊断只读边界。
func assertChunkedBodyRejected(t *testing.T, url string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, url, strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	request.ContentLength = -1
	request.TransferEncoding = []string{"chunked"}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close chunked response: %v", err)
		}
	}()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("chunked diagnostic body returned %d", response.StatusCode)
	}
}

// TestDiagnosticStartRejectsOccupiedAddress 验证固定端口冲突会显式失败而不会静默改绑。
func TestDiagnosticStartRejectsOccupiedAddress(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	settings := config.Default().Diagnostic
	settings.Address = occupied.Addr().String()
	server := New(settings, &fakeState{state: "starting"}, buildinfo.Info{Version: "test"}, observability.NewMetrics(), newFakeTasks())
	err = server.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "listen on diagnostic address") {
		t.Fatalf("occupied diagnostic address returned %v", err)
	}
	if server.Address() != "" {
		t.Fatalf("failed server exposed unexpected address %q", server.Address())
	}
}

// assertStatus 读取有界响应并同时验证状态与代表内容。
func assertStatus(t *testing.T, method string, url string, status int, contains string) {
	t.Helper()
	request, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status || !strings.Contains(string(body), contains) {
		t.Fatalf("%s %s returned %d %q", method, url, response.StatusCode, body)
	}
}
