package infra

import (
	"context"
	"net"
	"testing"
	"time"

	"ihomeland/server/internal/config"
)

func TestNewTCPChecker(t *testing.T) {
	cfg := config.Default()
	cfg.MySQL.Addr = "127.0.0.1:33306"
	cfg.Redis.Addr = "127.0.0.1:36379"

	checker := NewTCPChecker(cfg)

	if checker.MySQLAddr != "127.0.0.1:33306" {
		t.Fatalf("MySQLAddr = %q", checker.MySQLAddr)
	}
	if checker.RedisAddr != "127.0.0.1:36379" {
		t.Fatalf("RedisAddr = %q", checker.RedisAddr)
	}
}

func TestTCPChecker(t *testing.T) {
	listener := listenLocalTCP(t)
	defer listener.Close()
	go acceptUntilClosed(listener)

	checker := TCPChecker{
		MySQLAddr: listener.Addr().String(),
		RedisAddr: "127.0.0.1:1",
		Timeout:   50 * time.Millisecond,
	}

	statuses := checker.Check(context.Background())

	if len(statuses) != 2 {
		t.Fatalf("len(statuses) = %d, want 2", len(statuses))
	}
	if !statuses[0].Ready {
		t.Fatalf("mysql status = %+v", statuses[0])
	}
	if statuses[1].Ready {
		t.Fatalf("redis status = %+v, want not ready", statuses[1])
	}
	if statuses[1].Error == "" {
		t.Fatalf("redis error is empty")
	}
}

func listenLocalTCP(t *testing.T) net.Listener {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	return listener
}

func acceptUntilClosed(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		_ = conn.Close()
	}
}
