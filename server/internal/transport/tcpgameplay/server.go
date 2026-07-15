package tcpgameplay

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"
)

// Server 持有独立gameplay listener、accept任务和连接registry生命周期。
type Server struct {
	// config 是启动前冻结的listener与资源策略。
	config Config
	// tlsConfig 在production必须是TLS 1.3；local/test loopback明文时为空。
	tlsConfig *tls.Config
	// registry 在线性化点预留、注册并回收连接。
	registry *Registry
	// handshake 按ticket后admission完成认证。
	handshake *Handshake
	// dispatcher 只在完整认证后接收ReliableEnvelope。
	dispatcher *Dispatcher
	// tasks 监督系统性accept loop失败。
	tasks TaskOwner
	// observer 记录稳定握手结果。
	observer Observer

	// mu 保护listener与stopped状态。
	mu sync.Mutex
	// listener 只在Start成功后存在，并由Stop首先关闭。
	listener net.Listener
	// stopped 阻止重新启动；stopDone使重复Stop等待同一次关闭结果。
	stopped bool
	// stopDone 在唯一关闭流程写入stopErr后关闭。
	stopDone chan struct{}
	// stopErr 保存唯一关闭流程的最终结果。
	stopErr error
	// handlers 等待已经取得reservation的有界握手任务退出。
	handlers sync.WaitGroup
}

// NewServer 创建尚未bind且不启动goroutine的gameplay lifecycle component。
func NewServer(config Config, tlsConfig *tls.Config, registry *Registry, handshake *Handshake, dispatcher *Dispatcher, tasks TaskOwner, observer Observer) (*Server, error) {
	if config.Logger == nil || registry == nil || handshake == nil || dispatcher == nil || tasks == nil || observer == nil {
		return nil, errors.New("tcp gameplay server dependencies are incomplete")
	}
	if config.AllowPlaintext {
		if tlsConfig != nil {
			return nil, errors.New("plaintext tcp gameplay must not contain TLS config")
		}
	} else if tlsConfig == nil || tlsConfig.MinVersion != tls.VersionTLS13 {
		return nil, errors.New("tcp gameplay requires TLS 1.3 config")
	}
	return &Server{config: config, tlsConfig: tlsConfig, registry: registry, handshake: handshake, dispatcher: dispatcher, tasks: tasks, observer: observer}, nil
}

// Name 返回lifecycle与metrics使用的稳定component名称。
func (*Server) Name() string { return "tcp_gameplay" }

// Start 最后执行bind并登记唯一accept loop；部分失败会同步关闭listener。
func (server *Server) Start(_ context.Context) error {
	server.mu.Lock()
	if server.listener != nil || server.stopped {
		server.mu.Unlock()
		return errors.New("tcp gameplay server cannot be started in current state")
	}
	server.mu.Unlock()
	listener, err := net.Listen("tcp", server.config.Policy.Address)
	if err != nil {
		return fmt.Errorf("listen on tcp gameplay address: %w", err)
	}
	server.mu.Lock()
	if server.stopped {
		server.mu.Unlock()
		return errors.Join(errors.New("tcp gameplay server stopped while starting"), listener.Close())
	}
	server.listener = listener
	err = server.tasks.Go("accept", func(ctx context.Context) error { return server.acceptLoop(ctx, listener) })
	if err != nil {
		server.listener = nil
	}
	server.mu.Unlock()
	if err != nil {
		return errors.Join(fmt.Errorf("start tcp gameplay accept task: %w", err), listener.Close())
	}
	return nil
}

// acceptLoop 在创建goroutine前完成remote/global reservation，阻止连接storm放大任务数量。
func (server *Server) acceptLoop(ctx context.Context, listener net.Listener) error {
	for {
		socket, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("tcp gameplay accept failed")
		}
		remoteKey, loopback, ok := normalizedRemote(socket.RemoteAddr())
		if !ok || (server.config.AllowPlaintext && !loopback) {
			server.observer.ObserveTCPHandshake("accept", "remote_rejected")
			_ = socket.Close()
			continue
		}
		reserved, err := server.registry.Reserve(remoteKey)
		if err != nil {
			outcome := "capacity_rejected"
			if errors.Is(err, ErrRateLimited) {
				outcome = "rate_limited"
			}
			server.observer.ObserveTCPHandshake("accept", outcome)
			_ = socket.Close()
			continue
		}
		server.mu.Lock()
		if server.stopped {
			server.mu.Unlock()
			reserved.Release()
			_ = socket.Close()
			continue
		}
		server.handlers.Add(1)
		server.mu.Unlock()
		go func() {
			defer server.handlers.Done()
			server.handleConnection(ctx, socket, reserved)
		}()
	}
}

// handleConnection 有界完成TLS、preface、ticket、admission与registry commit。
func (server *Server) handleConnection(parent context.Context, raw net.Conn, reserved *reservation) {
	defer reserved.Release()
	connection := raw
	handshakeContext, cancel := context.WithTimeout(parent, server.config.Policy.HandshakeTimeout)
	defer cancel()
	if server.tlsConfig != nil {
		tlsConnection := tls.Server(raw, server.tlsConfig.Clone())
		if err := tlsConnection.HandshakeContext(handshakeContext); err != nil {
			server.observer.ObserveTCPHandshake("tls", "rejected")
			_ = raw.Close()
			return
		}
		connection = tlsConnection
		server.observer.ObserveTCPHandshake("tls", "accepted")
	}
	_ = connection.SetReadDeadline(time.Now().Add(server.config.Policy.HandshakeTimeout))
	preface, err := ReadPreface(connection, server.config.Policy.HandshakeBytes)
	if err != nil {
		server.observer.ObserveTCPHandshake("preface", "rejected")
		_ = connection.Close()
		return
	}
	server.observer.ObserveTCPHandshake("preface", "accepted")
	auth, qualification, err := server.handshake.Authenticate(handshakeContext, preface, reserved)
	if err != nil {
		_ = connection.Close()
		return
	}
	entry, err := server.registry.Commit(reserved, auth, qualification, connection)
	if err != nil {
		server.observer.ObserveTCPHandshake("register", "rejected")
		_ = connection.Close()
		return
	}
	_ = connection.SetDeadline(time.Time{})
	if err := server.registry.StartConnection(entry, server.dispatcher); err != nil {
		server.observer.ObserveTCPHandshake("register", "rejected")
		server.registry.Remove(entry.id)
		return
	}
	server.observer.ObserveTCPHandshake("register", "accepted")
}

// Stop 先停止accept，再在共享deadline内关闭连接，最后等待受监督任务。
func (server *Server) Stop(ctx context.Context) error {
	if ctx == nil {
		return errors.New("tcp gameplay stop context is nil")
	}
	server.mu.Lock()
	if server.stopDone != nil {
		done := server.stopDone
		server.mu.Unlock()
		select {
		case <-done:
			server.mu.Lock()
			err := server.stopErr
			server.mu.Unlock()
			return err
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	server.stopped = true
	server.stopDone = make(chan struct{})
	listener := server.listener
	server.mu.Unlock()
	var listenerErr error
	if listener != nil {
		listenerErr = listener.Close()
	}
	taskErr := server.tasks.Stop(ctx, errors.New("tcp gameplay component stopped"))
	registryErr := server.registry.Stop(ctx)
	var handlerErr error
	select {
	case <-waitGroupChannel(&server.handlers):
	case <-ctx.Done():
		handlerErr = context.Cause(ctx)
	}
	stopErr := errors.Join(listenerErr, taskErr, registryErr, handlerErr)
	server.mu.Lock()
	server.stopErr = stopErr
	close(server.stopDone)
	server.mu.Unlock()
	return stopErr
}

// Address 返回Start后的实际bind地址；未启动时为空。
func (server *Server) Address() string {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.listener == nil {
		return ""
	}
	return server.listener.Addr().String()
}

// normalizedRemote 只接受TCP IP remote并返回进程内规范identity与loopback属性。
func normalizedRemote(address net.Addr) (string, bool, bool) {
	if address == nil {
		return "", false, false
	}
	host, _, err := net.SplitHostPort(address.String())
	if err != nil {
		return "", false, false
	}
	parsed, err := netip.ParseAddr(host)
	if err != nil {
		return "", false, false
	}
	parsed = parsed.Unmap()
	return parsed.String(), parsed.IsLoopback(), true
}
