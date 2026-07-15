// Package observability 维护服务端运行基础的低基数 Prometheus 指标。
//
// 该包不依赖 domain 或 transport owner，也不接受 endpoint、identity、SQL/key/value 或原始
// driver error。调用方必须先把动态结果归一为固定枚举；Metrics 使用进程私有 registry，
// 不修改 Prometheus package-global collector。
package observability

import (
	"fmt"
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
	// publicRequestsTotal 按operation、status class与稳定outcome统计公开HTTP请求。
	publicRequestsTotal *prometheus.CounterVec
	// publicRequestSeconds 观察公开HTTP端到端处理时间。
	publicRequestSeconds *prometheus.HistogramVec
	// publicResponseBytes 观察编码后响应大小，不读取响应正文。
	publicResponseBytes *prometheus.HistogramVec
	// websocketHandshakes 按稳定结果统计control upgrade。
	websocketHandshakes *prometheus.CounterVec
	// websocketConnections 观察当前活动control连接数。
	websocketConnections prometheus.Gauge
	// websocketPushes 按message ID与稳定结果统计PUSH。
	websocketPushes *prometheus.CounterVec
	// websocketPushBytes 观察成功或失败PUSH的完整编码大小。
	websocketPushBytes *prometheus.HistogramVec
	// websocketQueues 统计有界队列接受或拒绝结果。
	websocketQueues *prometheus.CounterVec
	// websocketQueueItems 和 websocketQueueBytes 观察瞬时队列占用。
	websocketQueueItems prometheus.Histogram
	websocketQueueBytes prometheus.Histogram
	// websocketHeartbeats 统计ping/pong稳定结果。
	websocketHeartbeats *prometheus.CounterVec
	// websocketCloses 统计稳定关闭原因。
	websocketCloses *prometheus.CounterVec
	// websocketInvalidations 统计Session失效联动结果。
	websocketInvalidations *prometheus.CounterVec
}

// NewMetrics 注册运行时固定指标集合；私有 registry 使重复构造不会污染 package global 状态。
func NewMetrics() *Metrics {
	metrics := &Metrics{
		registry:               prometheus.NewRegistry(),
		startupTotal:           prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_startup_total", Help: "Server startup results."}, []string{"result"}),
		shutdownTotal:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_shutdown_total", Help: "Server shutdown results."}, []string{"reason", "result"}),
		taskFailuresTotal:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_task_failures_total", Help: "Supervised task failures."}, []string{"task", "kind"}),
		diagnosticRequests:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_diagnostic_requests_total", Help: "Diagnostic HTTP requests."}, []string{"path", "status"}),
		lifecycleSeconds:       prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ihomeland_server_lifecycle_seconds", Help: "Lifecycle component duration.", Buckets: prometheus.DefBuckets}, []string{"phase", "component"}),
		storagePool:            prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "ihomeland_server_storage_pool_connections", Help: "Storage pool connection states."}, []string{"dependency", "state"}),
		storageProbeTotal:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_storage_probe_total", Help: "Required storage probe results."}, []string{"dependency", "outcome"}),
		storageProbeSeconds:    prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ihomeland_server_storage_probe_seconds", Help: "Required storage probe duration.", Buckets: prometheus.DefBuckets}, []string{"dependency", "outcome"}),
		storageMigrationTotal:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_storage_migration_total", Help: "MySQL migration results."}, []string{"outcome"}),
		storageOperationTotal:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_storage_operation_total", Help: "Storage transaction and command results."}, []string{"dependency", "operation", "outcome"}),
		publicRequestsTotal:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_public_http_requests_total", Help: "Public HTTP requests by bounded operation and outcome."}, []string{"operation", "status_class", "outcome"}),
		publicRequestSeconds:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ihomeland_server_public_http_request_seconds", Help: "Public HTTP request duration.", Buckets: prometheus.DefBuckets}, []string{"operation", "status_class", "outcome"}),
		publicResponseBytes:    prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ihomeland_server_public_http_response_bytes", Help: "Public HTTP response bytes.", Buckets: prometheus.ExponentialBuckets(128, 2, 10)}, []string{"operation", "status_class"}),
		websocketHandshakes:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_websocket_control_handshakes_total", Help: "WebSocket control handshake results."}, []string{"outcome"}),
		websocketConnections:   prometheus.NewGauge(prometheus.GaugeOpts{Name: "ihomeland_server_websocket_control_connections", Help: "Active WebSocket control connections."}),
		websocketPushes:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_websocket_control_pushes_total", Help: "WebSocket control push results."}, []string{"message_id", "outcome"}),
		websocketPushBytes:     prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ihomeland_server_websocket_control_push_bytes", Help: "Encoded WebSocket control push bytes.", Buckets: prometheus.ExponentialBuckets(64, 2, 12)}, []string{"message_id", "outcome"}),
		websocketQueues:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_websocket_control_queue_total", Help: "WebSocket control queue results."}, []string{"outcome"}),
		websocketQueueItems:    prometheus.NewHistogram(prometheus.HistogramOpts{Name: "ihomeland_server_websocket_control_queue_items", Help: "WebSocket control queued items.", Buckets: prometheus.ExponentialBuckets(1, 2, 8)}),
		websocketQueueBytes:    prometheus.NewHistogram(prometheus.HistogramOpts{Name: "ihomeland_server_websocket_control_queue_bytes", Help: "WebSocket control queued bytes.", Buckets: prometheus.ExponentialBuckets(64, 2, 16)}),
		websocketHeartbeats:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_websocket_control_heartbeats_total", Help: "WebSocket control heartbeat results."}, []string{"outcome"}),
		websocketCloses:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_websocket_control_closes_total", Help: "WebSocket control close reasons."}, []string{"reason"}),
		websocketInvalidations: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_websocket_control_invalidations_total", Help: "WebSocket control invalidation results."}, []string{"outcome"}),
	}
	metrics.registry.MustRegister(metrics.startupTotal, metrics.shutdownTotal, metrics.taskFailuresTotal, metrics.diagnosticRequests, metrics.lifecycleSeconds, metrics.storagePool, metrics.storageProbeTotal, metrics.storageProbeSeconds, metrics.storageMigrationTotal, metrics.storageOperationTotal, metrics.publicRequestsTotal, metrics.publicRequestSeconds, metrics.publicResponseBytes, metrics.websocketHandshakes, metrics.websocketConnections, metrics.websocketPushes, metrics.websocketPushBytes, metrics.websocketQueues, metrics.websocketQueueItems, metrics.websocketQueueBytes, metrics.websocketHeartbeats, metrics.websocketCloses, metrics.websocketInvalidations)
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
	requireMetricLabel(dependency, "mysql", "redis")
	requireMetricLabel(state, "open", "idle", "in_use")
	metrics.storagePool.WithLabelValues(dependency, state).Set(float64(value))
}

// ObserveStorageProbe 记录 mysql|redis 与 ok|failed 的次数和耗时。
func (metrics *Metrics) ObserveStorageProbe(dependency string, outcome string, seconds float64) {
	requireMetricLabel(dependency, "mysql", "redis")
	requireMetricLabel(outcome, "ok", "failed")
	metrics.storageProbeTotal.WithLabelValues(dependency, outcome).Inc()
	metrics.storageProbeSeconds.WithLabelValues(dependency, outcome).Observe(seconds)
}

// ObserveMigration 记录 applied|failed outcome；count 不作为 label，避免基数增长。
func (metrics *Metrics) ObserveMigration(outcome string, count int) {
	requireMetricLabel(outcome, "applied", "failed")
	metrics.storageMigrationTotal.WithLabelValues(outcome).Add(float64(count))
}

// RecordStorageOperation 记录固定 runtime/adapter、operation 与 outcome。
//
// PersonalWorld/placement adapter 只上报此处枚举的流程结果；identity、SQL、key、fence、
// idempotency material 与原始错误永远不能成为 label。
func (metrics *Metrics) RecordStorageOperation(adapter string, operation string, outcome string) {
	requireMetricLabel(adapter, "mysql", "redis", "account", "session", "personalworld", "placement", "visitsession", "worldadmission")
	requireMetricLabel(operation,
		"transaction", "command", "script", "ensure_primary", "find_by_id", "archive", "allocation",
		"resolve", "acquire", "activate", "renew", "revoke", "replace", "qualify_write", "create",
		"find_for_authentication", "resolve_access", "rotate_refresh", "issue_ticket", "consume_ticket",
		"invalidate_session", "invalidate_principal", "resolve_active", "commit", "issue", "consume")
	requireMetricLabel(outcome,
		"ok", "failed", "invalid", "defect", "dependency_defect", "codec_failed", "key_failed", "read_failed",
		"current_read_failed", "replay_read_failed", "allocation_read_failed", "not_committed", "pre_commit_transient",
		"commit_unknown", "allocation_not_committed", "allocation_commit_unknown", "not_applied", "created", "existing",
		"applied", "replay", "in_progress", "not_found", "conflict", "expired", "revision_conflict",
		"idempotency_conflict", "invalid_state", "found", "burned", "username_conflict", "replayed",
		"invalidated", "epoch_mismatch", "consumed", "binding_mismatch", "corrupt", "stale")
	metrics.storageOperationTotal.WithLabelValues(adapter, operation, outcome).Inc()
}

// ObservePublicHTTP 记录固定operation、status class与三值outcome，不接受URL、identity或错误文本。
func (metrics *Metrics) ObservePublicHTTP(operation string, statusClass string, outcome string, seconds float64, responseBytes int) {
	requireMetricLabel(operation, "getVersion", "getBootstrapConfig", "registerAccount", "loginAccount", "refreshSession", "logoutSession", "issueConnectionTicket", "getWorldBootstrap", "acceptVisitInvite", "issueWorldAdmission")
	requireMetricLabel(statusClass, "2xx", "4xx", "5xx")
	requireMetricLabel(outcome, "success", "client_error", "server_error")
	metrics.publicRequestsTotal.WithLabelValues(operation, statusClass, outcome).Inc()
	metrics.publicRequestSeconds.WithLabelValues(operation, statusClass, outcome).Observe(seconds)
	if responseBytes < 0 {
		responseBytes = 0
	}
	metrics.publicResponseBytes.WithLabelValues(operation, statusClass).Observe(float64(responseBytes))
}

// ObserveWSSHandshake 记录严格握手门的低基数结果。
func (metrics *Metrics) ObserveWSSHandshake(outcome string) {
	requireMetricLabel(outcome, "invalid_request", "draining", "tls_required", "tls_version_rejected", "host_rejected", "origin_rejected", "upgrade_rejected", "ticket_rejected", "remote_rejected", "rate_limited", "capacity_rejected", "scope_rejected", "dependency_failed", "upgrade_failed_after_consume", "subprotocol_rejected", "registration_failed", "panic", "panic_after_upgrade", "accepted")
	metrics.websocketHandshakes.WithLabelValues(outcome).Inc()
}

// SetWSSConnections 更新当前进程活动连接数。
func (metrics *Metrics) SetWSSConnections(value int) {
	if value < 0 {
		panic("invalid websocket connection count")
	}
	metrics.websocketConnections.Set(float64(value))
}

// ObserveWSSPush 记录已登记message ID、稳定结果与完整编码字节数。
func (metrics *Metrics) ObserveWSSPush(messageID uint32, outcome string, encodedBytes int) {
	message := fmt.Sprintf("%d", messageID)
	requireMetricLabel(message, "500", "501", "502", "503", "504", "2003", "2100", "2101", "2102")
	requireMetricLabel(outcome, "sent", "write_failed", "rejected")
	if encodedBytes < 0 {
		encodedBytes = 0
	}
	metrics.websocketPushes.WithLabelValues(message, outcome).Inc()
	metrics.websocketPushBytes.WithLabelValues(message, outcome).Observe(float64(encodedBytes))
}

// ObserveWSSQueue 记录队列结果和不含payload的占用数值。
func (metrics *Metrics) ObserveWSSQueue(outcome string, items int, bytes int) {
	requireMetricLabel(outcome, "accepted", "rejected")
	if items < 0 || bytes < 0 {
		panic("invalid websocket queue observation")
	}
	metrics.websocketQueues.WithLabelValues(outcome).Inc()
	metrics.websocketQueueItems.Observe(float64(items))
	metrics.websocketQueueBytes.Observe(float64(bytes))
}

// ObserveWSSHeartbeat 记录ping/pong结果。
func (metrics *Metrics) ObserveWSSHeartbeat(outcome string) {
	requireMetricLabel(outcome, "ok", "timeout", "idle_timeout")
	metrics.websocketHeartbeats.WithLabelValues(outcome).Inc()
}

// ObserveWSSClose 记录不含peer文本的稳定关闭原因。
func (metrics *Metrics) ObserveWSSClose(reason string) {
	requireMetricLabel(reason, "server_draining", "session_invalidated", "slow_consumer", "protocol_violation", "heartbeat_timeout", "idle_timeout", "peer_closed", "io_failed", "panic", "local_close")
	metrics.websocketCloses.WithLabelValues(reason).Inc()
}

// ObserveWSSInvalidation 记录提交后失效联动结果。
func (metrics *Metrics) ObserveWSSInvalidation(outcome string) {
	requireMetricLabel(outcome, "no_active_connection", "closed", "notify_failed_closed")
	metrics.websocketInvalidations.WithLabelValues(outcome).Inc()
}

// requireMetricLabel 只接受编译期固定枚举；非法值视为 programmer error 并 panic。
// panic 信息不回显可能敏感的调用方原值。
func requireMetricLabel(value string, allowed ...string) {
	for _, candidate := range allowed {
		if value == candidate {
			return
		}
	}
	panic("invalid metric label")
}
