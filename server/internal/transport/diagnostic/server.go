// Package diagnostic 实现与公开业务面隔离的内部 HTTP 探针 listener。
package diagnostic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"

	"github.com/jinwiforz/ihomeland/server/internal/buildinfo"
	"github.com/jinwiforz/ihomeland/server/internal/config"
	"github.com/jinwiforz/ihomeland/server/internal/observability"
)

// StateProvider 只暴露诊断所需状态，防止 HTTP handler 修改 lifecycle。
type StateProvider interface {
	// DiagnosticState 返回稳定状态名和当前是否 ready。
	DiagnosticState() (string, bool)
}

// TaskOwner 让 listener 的 Serve goroutine 进入全局监督，同时保留 component 级取消顺序。
type TaskOwner interface {
	// Go 登记一个稳定命名的长生命周期任务。
	Go(string, func(context.Context) error) error
	// Stop 取消并等待该 component 拥有的任务。
	Stop(context.Context, error) error
}

// Server 持有诊断 listener、HTTP server 与 component task owner。
type Server struct {
	// settings 是启动前已验证的 listener 资源边界。
	settings config.Diagnostic
	// state 只读查询 lifecycle readiness。
	state StateProvider
	// info 是启动后不可变的安全构建身份。
	info buildinfo.Info
	// metrics 拥有 handler 和规范化请求计数。
	metrics *observability.Metrics
	// tasks 使 Serve loop 遵循 component Stop 顺序。
	tasks TaskOwner

	// mu 保护实际 listener 地址的并发诊断读取。
	mu sync.Mutex
	// listener 只在 Start 成功绑定后存在。
	listener net.Listener
	// server 持有固定路由和 timeout，不在运行时热替换。
	server *http.Server
}

// New 创建尚未绑定端口的诊断 component。
func New(settings config.Diagnostic, state StateProvider, info buildinfo.Info, metrics *observability.Metrics, tasks TaskOwner) *Server {
	component := &Server{settings: settings, state: state, info: info, metrics: metrics, tasks: tasks}
	component.server = &http.Server{
		Addr:              settings.Address,
		Handler:           component.routes(),
		ReadHeaderTimeout: settings.ReadHeaderTimeout,
		ReadTimeout:       settings.ReadTimeout,
		WriteTimeout:      settings.WriteTimeout,
		IdleTimeout:       settings.IdleTimeout,
		MaxHeaderBytes:    settings.MaxHeaderBytes,
	}
	return component
}

// Name 返回 lifecycle 与 metrics 使用的稳定 component 名。
func (*Server) Name() string { return "diagnostic" }

// Start 绑定已验证地址，并在 task owner 中运行 HTTP Serve loop。
func (server *Server) Start(_ context.Context) error {
	listener, err := net.Listen("tcp", server.settings.Address)
	if err != nil {
		return fmt.Errorf("listen on diagnostic address: %w", err)
	}
	server.mu.Lock()
	server.listener = listener
	server.mu.Unlock()
	if err := server.tasks.Go("serve", func(context.Context) error {
		err := server.server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}); err != nil {
		closeErr := listener.Close()
		server.mu.Lock()
		server.listener = nil
		server.mu.Unlock()
		return errors.Join(fmt.Errorf("start diagnostic task: %w", err), closeErr)
	}
	return nil
}

// Stop 先让 HTTP server graceful shutdown，再取消并等待其 Serve task。
func (server *Server) Stop(ctx context.Context) error {
	shutdownErr := server.server.Shutdown(ctx)
	var forceCloseErr error
	if shutdownErr != nil {
		forceCloseErr = server.server.Close()
	}
	taskErr := server.tasks.Stop(ctx, errors.New("diagnostic component stopped"))
	return errors.Join(shutdownErr, forceCloseErr, taskErr)
}

// Address 返回 Start 成功后的实际绑定地址，仅用于诊断与测试。
func (server *Server) Address() string {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.listener == nil {
		return ""
	}
	return server.listener.Addr().String()
}

// routes 构建封闭诊断路由表，未知路径由 ServeMux 直接拒绝。
func (server *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", server.health)
	mux.HandleFunc("/readyz", server.ready)
	mux.HandleFunc("/version", server.version)
	mux.Handle("/metrics", server.methodGuard(server.metrics.Handler()))
	return server.measure(mux)
}

// health 报告进程事件循环存活，不把 readiness 或外部依赖混入 liveness。
func (server *Server) health(writer http.ResponseWriter, request *http.Request) {
	if !allowReadMethod(writer, request) {
		return
	}
	state, _ := server.state.DiagnosticState()
	writeJSON(writer, request, http.StatusOK, map[string]string{"status": "alive", "state": state})
}

// ready 只在显式 ready 状态返回成功，starting/draining/stopped 均为 503。
func (server *Server) ready(writer http.ResponseWriter, request *http.Request) {
	if !allowReadMethod(writer, request) {
		return
	}
	state, ready := server.state.DiagnosticState()
	status := http.StatusServiceUnavailable
	if ready {
		status = http.StatusOK
	}
	writeJSON(writer, request, status, map[string]string{"status": state})
}

// version 返回启动时冻结的有界 build info，不读取文件或环境。
func (server *Server) version(writer http.ResponseWriter, request *http.Request) {
	if !allowReadMethod(writer, request) {
		return
	}
	writeJSON(writer, request, http.StatusOK, server.info)
}

// methodGuard 将所有诊断读取限制为 GET/HEAD 且拒绝 request body。
func (server *Server) methodGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !allowReadMethod(writer, request) {
			return
		}
		next.ServeHTTP(writer, request)
	})
}

// measure 只记录规范化 path 和 status，避免攻击者制造无界 label。
func (server *Server) measure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		recorder := &statusWriter{ResponseWriter: writer, status: http.StatusOK}
		next.ServeHTTP(recorder, request)
		server.metrics.RecordDiagnosticRequest(normalizePath(request.URL.Path), strconv.Itoa(recorder.status))
	})
}

// statusWriter 在不读取响应正文的情况下捕获最终状态码，使 metrics 不接触潜在响应数据。
type statusWriter struct {
	// ResponseWriter 委托真实 header/body 写入。
	http.ResponseWriter
	// status 保存 handler 最终观测的 HTTP status。
	status int
	// wroteHeader 防止后续重复 WriteHeader 改写已发送状态的 metrics 观测。
	wroteHeader bool
}

// WriteHeader 在委托写入前捕获状态码供 metrics 使用。
func (writer *statusWriter) WriteHeader(status int) {
	if writer.wroteHeader {
		return
	}
	writer.wroteHeader = true
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

// allowReadMethod 集中执行 method 与空 body 边界。
func allowReadMethod(writer http.ResponseWriter, request *http.Request) bool {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if request.Body != nil && request.Body != http.NoBody {
		http.Error(writer, "request body is not allowed", http.StatusBadRequest)
		return false
	}
	return true
}

// writeJSON 先在内存完成有界值编码，失败时不会写出部分 JSON。
func writeJSON(writer http.ResponseWriter, request *http.Request, status int, value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		http.Error(writer, "diagnostic encoding failed", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	if request.Method == http.MethodHead {
		return
	}
	// Header 已经提交，客户端断开导致的写失败无法再改变响应；HTTP server 负责关闭该连接。
	_, _ = writer.Write(append(encoded, '\n'))
}

// normalizePath 把所有未知输入折叠为单一 label，阻止 metrics 基数攻击。
func normalizePath(path string) string {
	switch path {
	case "/healthz", "/readyz", "/version", "/metrics":
		return path
	default:
		return "unknown"
	}
}
