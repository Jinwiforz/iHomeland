// Package observability 维护服务端运行基础的低基数 Prometheus 指标。
//
// 该包不依赖 domain 或 transport owner，也不接受 endpoint、identity、SQL/key/value 或原始
// driver error。调用方必须先把动态结果归一为固定枚举；Metrics 使用进程私有 registry，
// 不修改 Prometheus package-global collector。
package observability

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics 拥有进程私有 registry，避免 package global collector 在测试或多实例构造时冲突。
type Metrics struct {
	// registry 隔离当前进程 collector，测试可重复构造且不会碰撞 global registry。
	registry *prometheus.Registry
	// startupTotal 按固定结果统计启动尝试。
	startupTotal *prometheus.CounterVec
	// shutdownTotal 按固定原因和结果统计关闭。
	shutdownTotal *prometheus.CounterVec
	// taskFailuresTotal 只接受已登记任务名和 error/panic 分类。
	taskFailuresTotal *prometheus.CounterVec
	// diagnosticRequests 使用规范化路径与状态，不保存原始 URL。
	diagnosticRequests *prometheus.CounterVec
	// lifecycleSeconds 观察固定 component 的启动和停止耗时。
	lifecycleSeconds *prometheus.HistogramVec
	// storagePool 只按 dependency 和固定状态观察 pool 数量。
	storagePool *prometheus.GaugeVec
	// storageProbeTotal 按 dependency 与固定 outcome 统计 probe。
	storageProbeTotal *prometheus.CounterVec
	// storageProbeSeconds 观察 storage probe 耗时。
	storageProbeSeconds *prometheus.HistogramVec
	// storageMigrationTotal 记录 MySQL migration 的固定 outcome 与数量。
	storageMigrationTotal *prometheus.CounterVec
	// storageOperationTotal 记录 transaction/command 的固定 operation 与 outcome。
	storageOperationTotal *prometheus.CounterVec
}

// NewMetrics 注册运行时固定指标集合；私有 registry 使重复构造不会污染 package global 状态。
func NewMetrics() *Metrics {
	metrics := &Metrics{
		registry:              prometheus.NewRegistry(),
		startupTotal:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_startup_total", Help: "Server startup results."}, []string{"result"}),
		shutdownTotal:         prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_shutdown_total", Help: "Server shutdown results."}, []string{"reason", "result"}),
		taskFailuresTotal:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_task_failures_total", Help: "Supervised task failures."}, []string{"task", "kind"}),
		diagnosticRequests:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_diagnostic_requests_total", Help: "Diagnostic HTTP requests."}, []string{"path", "status"}),
		lifecycleSeconds:      prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ihomeland_server_lifecycle_seconds", Help: "Lifecycle component duration.", Buckets: prometheus.DefBuckets}, []string{"phase", "component"}),
		storagePool:           prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "ihomeland_server_storage_pool_connections", Help: "Storage pool connection states."}, []string{"dependency", "state"}),
		storageProbeTotal:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_storage_probe_total", Help: "Required storage probe results."}, []string{"dependency", "outcome"}),
		storageProbeSeconds:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ihomeland_server_storage_probe_seconds", Help: "Required storage probe duration.", Buckets: prometheus.DefBuckets}, []string{"dependency", "outcome"}),
		storageMigrationTotal: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_storage_migration_total", Help: "MySQL migration results."}, []string{"outcome"}),
		storageOperationTotal: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_storage_operation_total", Help: "Storage transaction and command results."}, []string{"dependency", "operation", "outcome"}),
	}
	metrics.registry.MustRegister(metrics.startupTotal, metrics.shutdownTotal, metrics.taskFailuresTotal, metrics.diagnosticRequests, metrics.lifecycleSeconds, metrics.storagePool, metrics.storageProbeTotal, metrics.storageProbeSeconds, metrics.storageMigrationTotal, metrics.storageOperationTotal)
	return metrics
}

// Handler 返回只暴露当前私有 registry 的 OpenMetrics 兼容 handler。
func (metrics *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(metrics.registry, promhttp.HandlerOpts{EnableOpenMetrics: true})
}

// RecordStartup 记录启动结果；调用方只能传入约定的有限状态，避免产生无界 label。
func (metrics *Metrics) RecordStartup(result string) {
	metrics.startupTotal.WithLabelValues(result).Inc()
}

// RecordShutdown 记录关闭原因与结果；两个参数都必须来自运行时的有限状态集合。
func (metrics *Metrics) RecordShutdown(reason string, result string) {
	metrics.shutdownTotal.WithLabelValues(reason, result).Inc()
}

// RecordTaskFailure 记录由注册任务名构成的有界 task 标签。
func (metrics *Metrics) RecordTaskFailure(task string, kind string) {
	metrics.taskFailuresTotal.WithLabelValues(task, kind).Inc()
}

// RecordDiagnosticRequest 记录规范化路径和状态码，不使用原始 URL。
func (metrics *Metrics) RecordDiagnosticRequest(path string, status string) {
	metrics.diagnosticRequests.WithLabelValues(path, status).Inc()
}

// ObserveLifecycle 记录稳定 component 与 phase 的秒数；耗时使用秒以符合 Prometheus 单位约定。
func (metrics *Metrics) ObserveLifecycle(phase string, component string, seconds float64) {
	metrics.lifecycleSeconds.WithLabelValues(phase, component).Observe(seconds)
}

// SetStoragePool 记录 mysql|redis 的 open|idle|in_use 连接数，不接收 endpoint 或 identity。
func (metrics *Metrics) SetStoragePool(dependency string, state string, value int) {
	requireStorageLabel(dependency, "mysql", "redis")
	requireStorageLabel(state, "open", "idle", "in_use")
	metrics.storagePool.WithLabelValues(dependency, state).Set(float64(value))
}

// ObserveStorageProbe 记录 mysql|redis 与 ok|failed 的次数和耗时。
func (metrics *Metrics) ObserveStorageProbe(dependency string, outcome string, seconds float64) {
	requireStorageLabel(dependency, "mysql", "redis")
	requireStorageLabel(outcome, "ok", "failed")
	metrics.storageProbeTotal.WithLabelValues(dependency, outcome).Inc()
	metrics.storageProbeSeconds.WithLabelValues(dependency, outcome).Observe(seconds)
}

// ObserveMigration 记录 applied|failed outcome；count 不作为 label，避免基数增长。
func (metrics *Metrics) ObserveMigration(outcome string, count int) {
	requireStorageLabel(outcome, "applied", "failed")
	metrics.storageMigrationTotal.WithLabelValues(outcome).Add(float64(count))
}

// RecordStorageOperation 记录固定 transaction/command operation 与 outcome。
func (metrics *Metrics) RecordStorageOperation(dependency string, operation string, outcome string) {
	requireStorageLabel(dependency, "mysql", "redis")
	requireStorageLabel(operation, "transaction", "command", "script")
	requireStorageLabel(outcome, "ok", "failed", "not_committed", "pre_commit_transient", "commit_unknown", "not_applied", "read_failed")
	metrics.storageOperationTotal.WithLabelValues(dependency, operation, outcome).Inc()
}

// requireStorageLabel 只接受编译期固定枚举；非法值视为 programmer error 并 panic。
// panic 信息不回显可能敏感的调用方原值。
func requireStorageLabel(value string, allowed ...string) {
	for _, candidate := range allowed {
		if value == candidate {
			return
		}
	}
	panic("invalid storage metric label")
}
