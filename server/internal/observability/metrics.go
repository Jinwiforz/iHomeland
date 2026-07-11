// Package observability 维护服务端运行基础的低基数 Prometheus 指标。
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
}

// NewMetrics 注册运行时固定指标集合；私有 registry 使重复构造不会污染 package global 状态。
func NewMetrics() *Metrics {
	metrics := &Metrics{
		registry:           prometheus.NewRegistry(),
		startupTotal:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_startup_total", Help: "Server startup results."}, []string{"result"}),
		shutdownTotal:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_shutdown_total", Help: "Server shutdown results."}, []string{"reason", "result"}),
		taskFailuresTotal:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_task_failures_total", Help: "Supervised task failures."}, []string{"task", "kind"}),
		diagnosticRequests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_diagnostic_requests_total", Help: "Diagnostic HTTP requests."}, []string{"path", "status"}),
		lifecycleSeconds:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ihomeland_server_lifecycle_seconds", Help: "Lifecycle component duration.", Buckets: prometheus.DefBuckets}, []string{"phase", "component"}),
	}
	metrics.registry.MustRegister(metrics.startupTotal, metrics.shutdownTotal, metrics.taskFailuresTotal, metrics.diagnosticRequests, metrics.lifecycleSeconds)
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
