// Package redis 拥有服务端唯一 standalone Redis client、key registry 和 TTL policy。
//
// 该包不依赖 transport 或业务 domain，不拥有 typed value、Lua 或持久事实。Component
// 持有 client 与 probe；业务 adapter 只能借用 client，并负责自身 codec、idempotency、
// script outcome 与 Redis 丢失后的 fail-closed 恢复。
package redis

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/config"
	"github.com/jinwiforz/ihomeland/server/internal/secret"
	redisclient "github.com/redis/go-redis/v9"
)

// TaskOwner 是 Redis component 所需的最窄 supervised task 生命周期边界。
type TaskOwner interface {
	// Go 登记并启动一个具名 task。
	Go(string, func(context.Context) error) error
	// Stop 取消 owner 下所有 task 并等待退出。
	Stop(context.Context, error) error
}

// Observer 记录低基数 Redis runtime 结果，不接收完整 key、value 或原始错误。
type Observer interface {
	// ObserveStorageProbe 记录固定 dependency 和 outcome 的 probe 结果。
	ObserveStorageProbe(string, string, float64)
	// SetStoragePool 记录固定 pool state 的当前连接数。
	SetStoragePool(string, string, int)
}

// Component 管理唯一 standalone Redis client 及其 required probe。
type Component struct {
	// settings 是配置层已完成交叉校验的 standalone client 快照。
	settings config.Redis
	// password 在 client options 构造后立即清零原始 bytes。
	password secret.Value
	// tls 为 nil 表示 local/test plaintext；production 配置保证非 nil 且验证身份。
	tls *tls.Config
	// tasks 拥有 required probe 的取消与等待边界。
	tasks TaskOwner
	// observer 只接收固定 dependency/outcome 和 pool 状态。
	observer Observer
	// logger 只记录稳定 operation 与连续失败次数，不接收 client 原始错误。
	logger *slog.Logger

	// mu 保护 client 所有权与一次性启动/关闭状态。
	mu sync.RWMutex
	// client 只在认证、database 与 standalone 检查全部成功后公开。
	client *redisclient.Client
	// started 防止重复 Start 创建第二个 client。
	started bool
	// stopped 使 Stop 幂等并阻止 Start-after-Stop。
	stopped bool
	// stopDone 让并发 Stop 等待同一次资源回收，而不是提前报告成功。
	stopDone chan struct{}
	// stopErr 保存首次 Stop 的稳定结果，供后续调用复用。
	stopErr error
}

// New 创建不获取网络资源的 standalone Redis component。
//
// 成功后 password 的使用所有权转移给 component，调用方不得在 Start 返回前 Expose 或
// Destroy 其别名；调用方可保留退出时的幂等 Destroy，覆盖 component 尚未启动的路径。
func New(settings config.Redis, password secret.Value, tlsConfig *tls.Config, tasks TaskOwner, observer Observer, logger *slog.Logger) (*Component, error) {
	if tasks == nil || observer == nil || logger == nil {
		return nil, errors.New("redis component requires task owner, observer, and logger")
	}
	return &Component{
		settings: settings,
		password: password,
		tls:      tlsConfig,
		tasks:    tasks,
		observer: observer,
		logger:   logger,
		stopDone: make(chan struct{}),
	}, nil
}

// Name 返回 lifecycle 使用的稳定低基数名称。
func (*Component) Name() string { return "redis" }

// Start 创建 client，禁用隐式 command retry，并验证认证、database 和 standalone mode。
//
// Start 会替换 go-redis 的 process-global logger，影响进程内全部 go-redis client；该行为
// 依赖 Composition Root 的唯一 client owner 约束，且不会在 Stop 时恢复。password 在
// client options 构造后立即销毁，成功后的 client 只由 Stop 关闭。
func (component *Component) Start(ctx context.Context) error {
	component.mu.Lock()
	if component.started || component.stopped {
		component.mu.Unlock()
		return errors.New("redis component may start only once")
	}
	component.started = true
	component.mu.Unlock()
	// go-redis 的 package logger 会把原始 network error 写入 stderr；唯一 client owner 禁用该旁路，失败由 probe 和 metrics 分类。
	redisclient.SetLogger(safeClientLogger{})

	options := &redisclient.Options{
		Addr: component.settings.Address, Username: component.settings.Username, DB: component.settings.Database,
		DialTimeout: component.settings.DialTimeout, ReadTimeout: component.settings.ReadTimeout, WriteTimeout: component.settings.WriteTimeout,
		PoolSize: component.settings.Pool.Size, MinIdleConns: component.settings.Pool.MinIdleConns,
		ConnMaxLifetime: component.settings.Pool.ConnMaxLifetime, ConnMaxIdleTime: component.settings.Pool.ConnMaxIdleTime,
		MaxRetries: -1, MinRetryBackoff: -1, MaxRetryBackoff: -1, TLSConfig: component.tls,
	}
	if err := component.password.Expose(func(value []byte) error {
		options.Password = string(value)
		return nil
	}); err != nil {
		return errors.New("construct redis credentials failed")
	}
	component.password.Destroy()
	client := redisclient.NewClient(options)
	owned := false
	defer func() {
		if !owned {
			// 启动尚未公开 client，关闭失败不会覆盖更有诊断价值的启动错误。
			_ = client.Close()
		}
	}()
	if err := client.Ping(ctx).Err(); err != nil {
		return wrapDriverError("redis startup probe failed", err)
	}
	clientInfo, err := client.ClientInfo(ctx).Result()
	if err != nil {
		return wrapDriverError("redis selected database validation failed", err)
	}
	if clientInfo.DB != component.settings.Database {
		return errors.New("redis selected database validation failed")
	}
	info, err := client.Info(ctx, "cluster").Result()
	if err != nil {
		return wrapDriverError("redis standalone validation failed", err)
	}
	if !strings.Contains(info, "cluster_enabled:0") {
		return errors.New("redis endpoint must use standalone mode")
	}
	recordPool(component.observer, client)
	component.mu.Lock()
	component.client = client
	component.mu.Unlock()
	if err := component.tasks.Go("probe", component.probe); err != nil {
		component.mu.Lock()
		component.client = nil
		component.mu.Unlock()
		return errors.New("start redis probe task failed")
	}
	owned = true
	return nil
}

// safeClientLogger 阻止 client 绕过结构化日志边界直接输出原始 network error。
type safeClientLogger struct{}

// Printf 丢弃 client 原始文本；稳定失败通过 component error、probe 与 metrics 传播。
func (safeClientLogger) Printf(context.Context, string, ...any) {}

// driverError 保留受控底层 cause，但默认文本只暴露稳定 operation 分类。
type driverError struct {
	// message 是不含 endpoint、key、value 或 client 原文的稳定文本。
	message string
	// cause 只供内部 errors.Is/As 诊断，不参与 Error 格式化。
	cause error
}

// Error 返回可安全写入普通日志的稳定文本。
func (failure *driverError) Error() string { return failure.message }

// Unwrap 返回只允许受控内部诊断读取的底层 client cause。
func (failure *driverError) Unwrap() error { return failure.cause }

// wrapDriverError 构造默认格式脱敏且保留 cause 的 storage client 错误。
func wrapDriverError(message string, cause error) error {
	return &driverError{message: message, cause: cause}
}

// Stop 先停止 probe owner，再撤销并关闭 client；并发或重复调用共享首次停止结果。
func (component *Component) Stop(ctx context.Context) error {
	component.mu.Lock()
	if component.stopped {
		done := component.stopDone
		component.mu.Unlock()
		select {
		case <-done:
			component.mu.RLock()
			defer component.mu.RUnlock()
			return component.stopErr
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	component.stopped = true
	component.mu.Unlock()
	// probe 在退出前仍可能读取 client；必须先等待 task owner，再撤销共享指针。
	taskErr := component.tasks.Stop(ctx, errors.New("redis component stopping"))
	component.mu.Lock()
	client := component.client
	component.client = nil
	component.mu.Unlock()
	var closeErr error
	if client != nil {
		closeErr = client.Close()
	}
	stopErr := errors.Join(taskErr, closeErr)
	component.mu.Lock()
	component.stopErr = stopErr
	close(component.stopDone)
	component.mu.Unlock()
	return stopErr
}

// Client 返回已成功启动且由 component 持有的借用 client；启动前或关闭后返回 nil。
//
// 调用方不得 Close 或跨 Stop 保留返回值，并须在 lifecycle 开始关闭前结束所有 command；
// Client 返回后若与 Stop 并发，后续调用仍可能观察到 client 已关闭。
func (component *Component) Client() *redisclient.Client {
	component.mu.RLock()
	defer component.mu.RUnlock()
	return component.client
}

// probe 周期验证 required client，连续失败达到阈值时把 fatal error 交给 TaskGroup。
func (component *Component) probe(ctx context.Context) error {
	ticker := time.NewTicker(component.settings.Probe.Interval)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			startedAt := time.Now()
			probeContext, cancel := context.WithTimeout(ctx, component.settings.Probe.Timeout)
			component.mu.RLock()
			client := component.client
			component.mu.RUnlock()
			if client == nil {
				cancel()
				if ctx.Err() != nil {
					return nil
				}
				return errors.New("redis required probe resource unavailable")
			}
			err := client.Ping(probeContext).Err()
			cancel()
			outcome := "ok"
			if err != nil {
				outcome = "failed"
				failures++
				component.logger.Warn("storage required probe failed", "operation", "probe", "dependency", "redis", "consecutive_failures", failures)
			} else {
				failures = 0
			}
			component.observer.ObserveStorageProbe("redis", outcome, time.Since(startedAt).Seconds())
			recordPool(component.observer, client)
			if failures >= component.settings.Probe.FailureThreshold {
				return wrapDriverError("redis required probe failure threshold reached", err)
			}
		}
	}
}

// recordPool 只上报 open/idle/in_use 数量，不包含 endpoint、key 或 identity。
func recordPool(observer Observer, client *redisclient.Client) {
	stats := client.PoolStats()
	observer.SetStoragePool("redis", "open", int(stats.TotalConns))
	observer.SetStoragePool("redis", "idle", int(stats.IdleConns))
	observer.SetStoragePool("redis", "in_use", int(stats.TotalConns-stats.IdleConns))
}
