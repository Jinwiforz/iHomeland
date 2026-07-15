package tcpgameplay

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// serverTaskOwner 为listener测试监督单个accept loop。
type serverTaskOwner struct {
	// mu 保护cancel与done的一次性登记。
	mu sync.Mutex
	// cancel 向accept loop发布稳定停止原因。
	cancel context.CancelCauseFunc
	// done 接收accept loop最终结果。
	done chan error
}

// Go 登记并启动accept loop。
func (owner *serverTaskOwner) Go(_ string, task func(context.Context) error) error {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.done != nil {
		return errors.New("tcp gameplay test task already started")
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	owner.cancel = cancel
	owner.done = make(chan error, 1)
	go func() { owner.done <- task(ctx) }()
	return nil
}

// Stop 取消并等待accept loop。
func (owner *serverTaskOwner) Stop(ctx context.Context, cause error) error {
	owner.mu.Lock()
	cancel, done := owner.cancel, owner.done
	owner.mu.Unlock()
	if cancel == nil || done == nil {
		return nil
	}
	cancel(cause)
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// TestServerRejectsMalformedPrefaceAndStopsIdempotently 验证真实listener的有界握手与关闭边界。
func TestServerRejectsMalformedPrefaceAndStopsIdempotently(t *testing.T) {
	config, _, registry := testRuntime(t)
	config.Policy.Address = "127.0.0.1:0"
	handshake := new(Handshake)
	dispatcher, err := NewDispatcher(new(dispatcherApplication), handshake, registry, new(testObserver))
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(config, nil, registry, handshake, dispatcher, new(serverTaskOwner), new(testObserver))
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout("tcp", server.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.Write([]byte{0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := connection.Read(make([]byte, 1)); err == nil {
		t.Fatal("malformed preface connection remained open")
	}
	stopContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
	if err := server.Stop(stopContext); err != nil {
		t.Fatalf("second Stop failed: %v", err)
	}
}

// TestServerRequiresTLS13 验证production listener不能被TLS 1.2配置降级。
func TestServerRequiresTLS13(t *testing.T) {
	t.Parallel()
	config, _, registry := testRuntime(t)
	handshake := new(Handshake)
	dispatcher, err := NewDispatcher(new(dispatcherApplication), handshake, registry, new(testObserver))
	if err != nil {
		t.Fatal(err)
	}
	config.AllowPlaintext = false
	_, err = NewServer(config, &tls.Config{MinVersion: tls.VersionTLS12}, registry, handshake, dispatcher, new(serverTaskOwner), new(testObserver))
	if err == nil {
		t.Fatal("TLS 1.2 gameplay listener was accepted")
	}
}
