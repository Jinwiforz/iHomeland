// Package mysql 拥有服务端唯一 MySQL pool、migration 和 transaction runtime。
//
// 该包不依赖 transport 或业务 domain，也不决定 repository conflict/retry。Component
// 持有 pool 与 probe；业务 adapter 只能在 lifecycle 有效期内借用 DB 或使用 WithinTx，
// 不得关闭共享资源或自动重放未知 transaction。
package mysql

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jinwiforz/ihomeland/server/internal/config"
	"github.com/jinwiforz/ihomeland/server/internal/secret"
)

// TaskOwner 是 MySQL component 所需的最窄 supervised task 生命周期边界。
type TaskOwner interface {
	// Go 登记并启动一个具名 task。
	Go(string, func(context.Context) error) error
	// Stop 取消 owner 下所有 task 并等待退出。
	Stop(context.Context, error) error
}

// Observer 记录低基数 MySQL runtime 结果，不接收 SQL、DSN 或原始错误。
type Observer interface {
	// ObserveStorageProbe 记录固定 dependency 和 outcome 的 probe 结果。
	ObserveStorageProbe(string, string, float64)
	// ObserveMigration 记录固定 outcome 与 migration 数量。
	ObserveMigration(string, int)
	// SetStoragePool 记录固定 pool state 的当前连接数。
	SetStoragePool(string, string, int)
	// RecordStorageOperation 记录固定 transaction outcome。
	RecordStorageOperation(string, string, string)
}

// Component 管理唯一 database/sql pool 及其 required probe。
type Component struct {
	// settings 是配置层已完成交叉校验的只读快照。
	settings config.MySQL
	// password 在 driver config 构造后立即清零原始 bytes。
	password secret.Value
	// tls 为 nil 表示 local/test plaintext；production 配置保证非 nil 且验证身份。
	tls *tls.Config
	// tasks 拥有 required probe 的取消与等待边界。
	tasks TaskOwner
	// observer 只接收固定 dependency/outcome 和 pool 状态。
	observer Observer
	// logger 只记录稳定 operation 与连续失败次数，不接收 driver 原始错误。
	logger *slog.Logger

	// mu 保护 pool 所有权与一次性启动/关闭状态。
	mu sync.RWMutex
	// db 只在 startup probe、session 与 migration 全部成功后公开。
	db *sql.DB
	// started 防止重复 Start 获取第二个 pool。
	started bool
	// stopped 使 Stop 幂等并阻止 Start-after-Stop。
	stopped bool
	// stopDone 让并发 Stop 等待同一次资源回收，而不是提前报告成功。
	stopDone chan struct{}
	// stopErr 保存首次 Stop 的稳定结果，供后续调用复用。
	stopErr error
}

// New 创建不获取网络资源的 MySQL component。
//
// 成功后 password 的使用所有权转移给 component，调用方不得在 Start 返回前 Expose 或
// Destroy 其别名；调用方可保留退出时的幂等 Destroy，覆盖 component 尚未启动的路径。
func New(settings config.MySQL, password secret.Value, tlsConfig *tls.Config, tasks TaskOwner, observer Observer, logger *slog.Logger) (*Component, error) {
	if tasks == nil || observer == nil || logger == nil {
		return nil, errors.New("mysql component requires task owner, observer, and logger")
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
func (*Component) Name() string { return "mysql" }

// Start 创建并验证 pool、session 与 migration，全部成功后才公开共享资源。
//
// Start 在 driver config 构造后立即销毁 password，并只允许调用一次。失败时关闭局部
// pool；成功时还会注册 required probe，后续资源释放只由 Stop 负责。
func (component *Component) Start(ctx context.Context) error {
	component.mu.Lock()
	if component.started || component.stopped {
		component.mu.Unlock()
		return errors.New("mysql component may start only once")
	}
	component.started = true
	component.mu.Unlock()

	driverConfig := mysqldriver.NewConfig()
	driverConfig.User = component.settings.Username
	driverConfig.Net = "tcp"
	driverConfig.Addr = component.settings.Address
	driverConfig.DBName = component.settings.Database
	driverConfig.Timeout = component.settings.ConnectTimeout
	driverConfig.ReadTimeout = component.settings.ReadTimeout
	driverConfig.WriteTimeout = component.settings.WriteTimeout
	driverConfig.ParseTime = true
	driverConfig.Loc = time.UTC
	driverConfig.RejectReadOnly = true
	driverConfig.InterpolateParams = false
	driverConfig.MultiStatements = false
	driverConfig.TLS = component.tls
	driverConfig.Logger = safeDriverLogger{}
	driverConfig.Params = map[string]string{
		"time_zone": "'+00:00'",
		"sql_mode":  "'STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION'",
	}
	if err := component.password.Expose(func(value []byte) error {
		driverConfig.Passwd = string(value)
		return nil
	}); err != nil {
		return errors.New("construct mysql credentials failed")
	}
	component.password.Destroy()
	connector, err := mysqldriver.NewConnector(driverConfig)
	if err != nil {
		return errors.New("construct mysql connector failed")
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(component.settings.Pool.MaxOpenConns)
	db.SetMaxIdleConns(component.settings.Pool.MaxIdleConns)
	db.SetConnMaxLifetime(component.settings.Pool.ConnMaxLifetime)
	db.SetConnMaxIdleTime(component.settings.Pool.ConnMaxIdleTime)
	owned := false
	defer func() {
		if !owned {
			// 启动尚未公开 pool，关闭失败不会覆盖更有诊断价值的启动错误。
			_ = db.Close()
		}
	}()

	if err := db.PingContext(ctx); err != nil {
		return wrapDriverError("mysql startup probe failed", err)
	}
	recordPool(component.observer, db)
	if err := validateSession(ctx, db, component.settings.Database); err != nil {
		return err
	}
	result, err := Migrate(ctx, db, component.settings.Database, component.settings.MigrationLockTimeout)
	if err != nil {
		component.observer.ObserveMigration("failed", 1)
		return err
	}
	component.observer.ObserveMigration("applied", result.Applied)

	component.mu.Lock()
	component.db = db
	component.mu.Unlock()
	if err := component.tasks.Go("probe", component.probe); err != nil {
		component.mu.Lock()
		component.db = nil
		component.mu.Unlock()
		return fmt.Errorf("start mysql probe task: %w", err)
	}
	owned = true
	return nil
}

// safeDriverLogger 阻止 driver 绕过结构化日志边界直接输出原始 network error。
type safeDriverLogger struct{}

// Print 丢弃 driver 原始文本；稳定失败通过 component error、probe 与 metrics 传播。
func (safeDriverLogger) Print(...any) {}

// driverError 保留受控底层 cause，但默认文本只暴露稳定 operation 分类。
type driverError struct {
	// message 是不含 endpoint、query、参数或 driver 原文的稳定文本。
	message string
	// cause 只供内部 errors.Is/As 诊断，不参与 Error 格式化。
	cause error
}

// Error 返回可安全写入普通日志的稳定文本。
func (failure *driverError) Error() string { return failure.message }

// Unwrap 返回只允许受控内部诊断读取的底层 driver cause。
func (failure *driverError) Unwrap() error { return failure.cause }

// wrapDriverError 构造默认格式脱敏且保留 cause 的 storage driver 错误。
func wrapDriverError(message string, cause error) error {
	return &driverError{message: message, cause: cause}
}

// Stop 先停止 probe owner，再撤销并关闭 pool；并发或重复调用共享首次停止结果。
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

	// probe 在退出前仍可能读取 pool；必须先等待 task owner，再撤销共享指针。
	taskErr := component.tasks.Stop(ctx, errors.New("mysql component stopping"))
	component.mu.Lock()
	db := component.db
	component.db = nil
	component.mu.Unlock()
	var closeErr error
	if db != nil {
		closeErr = db.Close()
	}
	stopErr := errors.Join(taskErr, closeErr)
	component.mu.Lock()
	component.stopErr = stopErr
	close(component.stopDone)
	component.mu.Unlock()
	return stopErr
}

// DB 返回已成功启动且由 component 持有的借用 pool；启动前或关闭后返回 nil。
//
// 调用方不得 Close 或跨 Stop 保留返回值，并须在 lifecycle 开始关闭前结束所有操作；
// DB 返回后若与 Stop 并发，后续调用仍可能观察到 pool 已关闭。
func (component *Component) DB() *sql.DB {
	component.mu.RLock()
	defer component.mu.RUnlock()
	return component.db
}

// WithinTx 在当前 shared pool 上恰好执行一次 callback，并记录稳定 commit outcome。
//
// 业务 adapter 应在 component lifecycle 有效期内优先使用该入口，且仍由消费 owner 决定
// conflict/commit-unknown 的恢复动作；该方法不得与 Stop 并发。
func (component *Component) WithinTx(ctx context.Context, options *sql.TxOptions, callback func(*sql.Tx) error) error {
	err := WithinTx(ctx, component.DB(), options, callback)
	outcome := "ok"
	if err != nil {
		var transactionError *TransactionError
		if errors.As(err, &transactionError) {
			outcome = string(transactionError.Outcome)
		} else {
			// callback error 已确认执行 rollback，具体业务 conflict 仍保留在原 error chain。
			outcome = string(TransactionNotCommitted)
		}
	}
	component.observer.RecordStorageOperation("mysql", "transaction", outcome)
	return err
}

// probe 周期验证 required pool，连续失败达到阈值时把 fatal error 交给 TaskGroup。
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
			db := component.db
			component.mu.RUnlock()
			if db == nil {
				cancel()
				if ctx.Err() != nil {
					return nil
				}
				return errors.New("mysql required probe resource unavailable")
			}
			err := db.PingContext(probeContext)
			cancel()
			outcome := "ok"
			if err != nil {
				outcome = "failed"
				failures++
				component.logger.Warn("storage required probe failed", "operation", "probe", "dependency", "mysql", "consecutive_failures", failures)
			} else {
				failures = 0
			}
			component.observer.ObserveStorageProbe("mysql", outcome, time.Since(startedAt).Seconds())
			recordPool(component.observer, db)
			if failures >= component.settings.Probe.FailureThreshold {
				return wrapDriverError("mysql required probe failure threshold reached", err)
			}
		}
	}
}

// recordPool 只上报 open/idle/in_use 数量，不包含 DSN、query 或 identity。
func recordPool(observer Observer, db *sql.DB) {
	stats := db.Stats()
	observer.SetStoragePool("mysql", "open", stats.OpenConnections)
	observer.SetStoragePool("mysql", "idle", stats.Idle)
	observer.SetStoragePool("mysql", "in_use", stats.InUse)
}

// validateSession 确认 database、version、UTC 与 strict SQL mode，避免连接默认值漂移。
func validateSession(ctx context.Context, db *sql.DB, expectedDatabase string) error {
	var database, version, timezone, sqlMode string
	err := db.QueryRowContext(ctx, "SELECT DATABASE(), VERSION(), @@session.time_zone, @@session.sql_mode").Scan(&database, &version, &timezone, &sqlMode)
	if err != nil {
		return wrapDriverError("mysql session validation query failed", err)
	}
	if database != expectedDatabase || version == "" {
		return errors.New("mysql session database or version mismatch")
	}
	if timezone != "+00:00" && !strings.EqualFold(timezone, "UTC") {
		return errors.New("mysql session time zone must be UTC")
	}
	if !strings.Contains(sqlMode, "STRICT_TRANS_TABLES") && !strings.Contains(sqlMode, "STRICT_ALL_TABLES") {
		return errors.New("mysql session sql mode must be strict")
	}
	return nil
}
