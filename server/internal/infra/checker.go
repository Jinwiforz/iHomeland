// Package infra 负责本地基础设施依赖的轻量健康探测。
package infra

import (
	"context"
	"errors"
	"net"
	"time"

	"ihomeland/server/internal/config"
)

const defaultCheckTimeout = 500 * time.Millisecond

// Status 描述单个基础设施依赖的探测结果。
type Status struct {
	Name  string `json:"name"`
	Ready bool   `json:"ready"`
	Error string `json:"error,omitempty"`
}

// Checker 探测服务端运行所需的基础设施依赖。
type Checker interface {
	Check(ctx context.Context) []Status
}

// TCPChecker 通过 TCP 连接探测本地 MySQL 和 Redis 是否可达。
type TCPChecker struct {
	MySQLAddr string
	RedisAddr string
	Timeout   time.Duration
}

// NewTCPChecker 根据服务端配置创建基础设施依赖探测器。
func NewTCPChecker(cfg config.Config) TCPChecker {
	return TCPChecker{
		MySQLAddr: cfg.MySQL.Addr,
		RedisAddr: cfg.Redis.Addr,
		Timeout:   defaultCheckTimeout,
	}
}

// Check 返回 MySQL 和 Redis 的当前连接状态。
func (c TCPChecker) Check(ctx context.Context) []Status {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultCheckTimeout
	}

	return []Status{
		checkTCP(ctx, "mysql", c.MySQLAddr, timeout),
		checkTCP(ctx, "redis", c.RedisAddr, timeout),
	}
}

func checkTCP(ctx context.Context, name string, addr string, timeout time.Duration) Status {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return Status{Name: name, Ready: false, Error: err.Error()}
	}
	if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return Status{Name: name, Ready: false, Error: err.Error()}
	}
	return Status{Name: name, Ready: true}
}
