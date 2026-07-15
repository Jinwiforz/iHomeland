package httpapi

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/jinwiforz/ihomeland/server/internal/config"
)

// TaskOwner 把Serve goroutine纳入全局监督与component关闭顺序。
type TaskOwner interface {
	// Go 登记稳定命名的长生命周期任务。
	Go(name string, task func(context.Context) error) error
	// Stop 取消并等待该component拥有的任务。
	Stop(ctx context.Context, cause error) error
}

// Component 持有公开listener、TLS策略、http.Server与in-flight request生命周期。
//
// 唯一Composition Root按Start/Stop顺序串行调用生命周期方法；Serve goroutine由TaskOwner
// 监督。Address可与Serve并发读取，Stop依赖http.Server保证in-flight shutdown安全。
type Component struct {
	// settings 是启动前完成交叉验证的公开server资源边界。
	settings config.PublicAPI
	// tlsConfig 是secret准备阶段构造的TLS 1.3策略；明文本地模式为空。
	tlsConfig *tls.Config
	// tasks 监督Serve loop。
	tasks TaskOwner
	// server 拥有timeout、header上限和graceful shutdown。
	server *http.Server

	// mutex 保护listener与stopped状态。
	mutex sync.Mutex
	// listener 只在Start成功后存在。
	listener net.Listener
	// stopped 使Stop幂等且不重复关闭task owner。
	stopped bool
}

// NewComponent 创建尚未产生网络副作用的公开HTTP lifecycle component。
func NewComponent(settings config.PublicAPI, handler http.Handler, tlsConfig *tls.Config, tasks TaskOwner) (*Component, error) {
	if handler == nil || tasks == nil || (settings.TLS.Enabled && (tlsConfig == nil || tlsConfig.MinVersion != tls.VersionTLS13)) || (!settings.TLS.Enabled && tlsConfig != nil) {
		return nil, errors.New("public HTTP component dependencies are incomplete")
	}
	component := &Component{settings: settings, tlsConfig: tlsConfig, tasks: tasks}
	component.server = &http.Server{Addr: settings.Address, Handler: handler, ReadHeaderTimeout: settings.ReadHeaderTimeout, ReadTimeout: settings.ReadTimeout, WriteTimeout: settings.WriteTimeout, IdleTimeout: settings.IdleTimeout, MaxHeaderBytes: settings.MaxHeaderBytes}
	return component, nil
}

// Name 返回lifecycle与metrics使用的稳定component名。
func (*Component) Name() string { return "public_http" }

// Start 先绑定地址，再按配置包裹TLS listener并登记受监督Serve loop。
//
// 调用方不得与Start或Stop并发调用；任务登记失败时方法同步关闭已绑定listener，不遗留
// 可接受连接的局部资源。
func (component *Component) Start(_ context.Context) error {
	component.mutex.Lock()
	if component.listener != nil || component.stopped {
		component.mutex.Unlock()
		return errors.New("public HTTP component cannot be started in current state")
	}
	component.mutex.Unlock()
	listener, err := net.Listen("tcp", component.settings.Address)
	if err != nil {
		return fmt.Errorf("listen on public HTTP address: %w", err)
	}
	serveListener := listener
	if component.tlsConfig != nil {
		serveListener = tls.NewListener(listener, component.tlsConfig.Clone())
	}
	component.mutex.Lock()
	component.listener = listener
	component.mutex.Unlock()
	if err := component.tasks.Go("serve", func(context.Context) error {
		serveErr := component.server.Serve(serveListener)
		if errors.Is(serveErr, http.ErrServerClosed) || errors.Is(serveErr, net.ErrClosed) {
			return nil
		}
		return serveErr
	}); err != nil {
		closeErr := listener.Close()
		component.mutex.Lock()
		component.listener = nil
		component.mutex.Unlock()
		return errors.Join(fmt.Errorf("start public HTTP task: %w", err), closeErr)
	}
	return nil
}

// Stop 先停止接收并等待in-flight request，再取消和回收Serve任务；重复调用安全返回。
//
// ctx deadline到达时会强制关闭连接，随后仍等待TaskOwner确认Serve退出；调用方必须先于
// Redis和MySQL执行本方法，避免请求观察到已释放storage。
func (component *Component) Stop(ctx context.Context) error {
	component.mutex.Lock()
	if component.stopped {
		component.mutex.Unlock()
		return nil
	}
	component.stopped = true
	component.mutex.Unlock()
	shutdownErr := component.server.Shutdown(ctx)
	var closeErr error
	if shutdownErr != nil {
		closeErr = component.server.Close()
	}
	taskErr := component.tasks.Stop(ctx, errors.New("public HTTP component stopped"))
	return errors.Join(shutdownErr, closeErr, taskErr)
}

// Address 返回Start后实际绑定地址，仅供启动日志与测试。
func (component *Component) Address() string {
	component.mutex.Lock()
	defer component.mutex.Unlock()
	if component.listener == nil {
		return ""
	}
	return component.listener.Addr().String()
}
