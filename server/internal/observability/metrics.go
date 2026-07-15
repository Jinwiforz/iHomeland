// Package observability 维护服务端运行基础的低基数 Prometheus 指标。
//
// 该包不依赖 domain 或 transport owner，也不接受 endpoint、identity、SQL/key/value 或原始
// driver error。调用方必须先把动态结果归一为固定枚举；Metrics 使用进程私有 registry，
// 不修改 Prometheus package-global collector。
package observability

import (
	"fmt"
	"net/http"
	"time"

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
	// websocketQueueItems 观察瞬时队列条目数。
	websocketQueueItems prometheus.Histogram
	// websocketQueueBytes 观察瞬时队列字节数。
	websocketQueueBytes prometheus.Histogram
	// websocketHeartbeats 统计ping/pong稳定结果。
	websocketHeartbeats *prometheus.CounterVec
	// websocketCloses 统计稳定关闭原因。
	websocketCloses *prometheus.CounterVec
	// websocketInvalidations 统计Session失效联动结果。
	websocketInvalidations *prometheus.CounterVec
	// tcpHandshakes 按固定阶段和结果统计gameplay认证。
	tcpHandshakes *prometheus.CounterVec
	// tcpConnections 按transport状态观察连接数。
	tcpConnections *prometheus.GaugeVec
	// tcpFrames 观察固定方向与稳定结果。
	tcpFrames *prometheus.CounterVec
	// tcpFrameBytes 观察完整 frame 大小。
	tcpFrameBytes *prometheus.HistogramVec
	// tcpDispatches 按登记 message ID 和稳定结果观察 application dispatch。
	tcpDispatches *prometheus.CounterVec
	// tcpDispatchSeconds 观察 application dispatch 耗时。
	tcpDispatchSeconds *prometheus.HistogramVec
	// tcpInFlight 观察当前进程尚未完成的gameplay operation数量。
	tcpInFlight prometheus.Gauge
	// tcpQueues 统计双预算队列接受与拒绝结果。
	tcpQueues *prometheus.CounterVec
	// tcpQueueItems 观察队列条目数。
	tcpQueueItems prometheus.Histogram
	// tcpQueueBytes 观察队列字节数。
	tcpQueueBytes prometheus.Histogram
	// tcpPushes 记录 PUSH 投递稳定结果。
	tcpPushes *prometheus.CounterVec
	// tcpCloses 记录连接关闭类别。
	tcpCloses *prometheus.CounterVec
	// tcpInvalidations 记录失效联动结果。
	tcpInvalidations *prometheus.CounterVec
	// worldRuntimes 观察进程内逻辑 WorldInstance 数量。
	worldRuntimes prometheus.Gauge
	// semanticDeadlines 观察进程内语义任务数量。
	semanticDeadlines prometheus.Gauge
	// worldLeases 记录 lease 封闭结果。
	worldLeases *prometheus.CounterVec
	// deadlineRuns 记录语义任务封闭结果。
	deadlineRuns *prometheus.CounterVec
	// visitLifecycles 记录连接生命周期协调结果。
	visitLifecycles *prometheus.CounterVec
	// visitDeliveries 记录跨通道副作用投递结果。
	visitDeliveries *prometheus.CounterVec
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
		tcpHandshakes:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_tcp_gameplay_handshakes_total", Help: "TLS/TCP gameplay handshake results."}, []string{"stage", "outcome"}),
		tcpConnections:         prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "ihomeland_server_tcp_gameplay_connections", Help: "TLS/TCP gameplay connections by transport state."}, []string{"state"}),
		tcpFrames:              prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_tcp_gameplay_frames_total", Help: "TLS/TCP gameplay frame results."}, []string{"direction", "outcome"}),
		tcpFrameBytes:          prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ihomeland_server_tcp_gameplay_frame_bytes", Help: "TLS/TCP gameplay complete frame bytes.", Buckets: prometheus.ExponentialBuckets(64, 2, 15)}, []string{"direction", "outcome"}),
		tcpDispatches:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_tcp_gameplay_dispatches_total", Help: "TLS/TCP gameplay dispatch results."}, []string{"message_id", "outcome"}),
		tcpDispatchSeconds:     prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ihomeland_server_tcp_gameplay_dispatch_seconds", Help: "TLS/TCP gameplay dispatch duration.", Buckets: prometheus.DefBuckets}, []string{"message_id", "outcome"}),
		tcpInFlight:            prometheus.NewGauge(prometheus.GaugeOpts{Name: "ihomeland_server_tcp_gameplay_in_flight", Help: "TLS/TCP gameplay operations currently in flight."}),
		tcpQueues:              prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_tcp_gameplay_queue_total", Help: "TLS/TCP gameplay queue results."}, []string{"outcome"}),
		tcpQueueItems:          prometheus.NewHistogram(prometheus.HistogramOpts{Name: "ihomeland_server_tcp_gameplay_queue_items", Help: "TLS/TCP gameplay queued items.", Buckets: prometheus.ExponentialBuckets(1, 2, 8)}),
		tcpQueueBytes:          prometheus.NewHistogram(prometheus.HistogramOpts{Name: "ihomeland_server_tcp_gameplay_queue_bytes", Help: "TLS/TCP gameplay queued bytes.", Buckets: prometheus.ExponentialBuckets(64, 2, 16)}),
		tcpPushes:              prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_tcp_gameplay_pushes_total", Help: "TLS/TCP gameplay push results."}, []string{"message_id", "outcome"}),
		tcpCloses:              prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_tcp_gameplay_closes_total", Help: "TLS/TCP gameplay close reasons."}, []string{"reason"}),
		tcpInvalidations:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_tcp_gameplay_invalidations_total", Help: "TLS/TCP gameplay invalidation results."}, []string{"outcome"}),
		worldRuntimes:          prometheus.NewGauge(prometheus.GaugeOpts{Name: "ihomeland_server_world_runtimes", Help: "Process-local logical WorldInstance runtimes."}),
		semanticDeadlines:      prometheus.NewGauge(prometheus.GaugeOpts{Name: "ihomeland_server_semantic_deadlines", Help: "Scheduled semantic deadline entries."}),
		worldLeases:            prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_world_lease_total", Help: "World assignment lease outcomes."}, []string{"outcome"}),
		deadlineRuns:           prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_semantic_deadline_total", Help: "Semantic deadline execution outcomes."}, []string{"kind", "outcome"}),
		visitLifecycles:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_visit_lifecycle_total", Help: "Visit connection lifecycle outcomes."}, []string{"operation", "outcome"}),
		visitDeliveries:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ihomeland_server_visit_delivery_total", Help: "Visit cross-channel delivery outcomes."}, []string{"kind", "outcome"}),
	}
	metrics.registry.MustRegister(metrics.startupTotal, metrics.shutdownTotal, metrics.taskFailuresTotal, metrics.diagnosticRequests, metrics.lifecycleSeconds, metrics.storagePool, metrics.storageProbeTotal, metrics.storageProbeSeconds, metrics.storageMigrationTotal, metrics.storageOperationTotal, metrics.publicRequestsTotal, metrics.publicRequestSeconds, metrics.publicResponseBytes, metrics.websocketHandshakes, metrics.websocketConnections, metrics.websocketPushes, metrics.websocketPushBytes, metrics.websocketQueues, metrics.websocketQueueItems, metrics.websocketQueueBytes, metrics.websocketHeartbeats, metrics.websocketCloses, metrics.websocketInvalidations, metrics.tcpHandshakes, metrics.tcpConnections, metrics.tcpFrames, metrics.tcpFrameBytes, metrics.tcpDispatches, metrics.tcpDispatchSeconds, metrics.tcpInFlight, metrics.tcpQueues, metrics.tcpQueueItems, metrics.tcpQueueBytes, metrics.tcpPushes, metrics.tcpCloses, metrics.tcpInvalidations, metrics.worldRuntimes, metrics.semanticDeadlines, metrics.worldLeases, metrics.deadlineRuns, metrics.visitLifecycles, metrics.visitDeliveries)
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

// ObserveTCPHandshake 记录TLS/preface/ticket/admission/register固定阶段与结果。
func (metrics *Metrics) ObserveTCPHandshake(stage string, outcome string) {
	requireMetricLabel(stage, "accept", "tls", "preface", "ticket", "admission", "register")
	requireMetricLabel(outcome, "accepted", "rejected", "remote_rejected", "capacity_rejected", "rate_limited", "scope_rejected", "rejected_after_ticket", "dependency_defect", "binding_rejected")
	metrics.tcpHandshakes.WithLabelValues(stage, outcome).Inc()
}

// SetTCPConnections 更新指定transport状态的连接数量。
func (metrics *Metrics) SetTCPConnections(state string, value int) {
	requireMetricLabel(state, "pending", "active", "returning", "closing")
	if value < 0 {
		panic("invalid tcp gameplay connection count")
	}
	metrics.tcpConnections.WithLabelValues(state).Set(float64(value))
}

// ObserveTCPFrame 记录方向、稳定结果与完整frame字节数。
func (metrics *Metrics) ObserveTCPFrame(direction string, outcome string, bytes int) {
	requireMetricLabel(direction, "c2s", "s2c")
	requireMetricLabel(outcome, "accepted", "rejected", "sent", "write_failed")
	if bytes < 0 {
		panic("invalid tcp gameplay frame size")
	}
	metrics.tcpFrames.WithLabelValues(direction, outcome).Inc()
	metrics.tcpFrameBytes.WithLabelValues(direction, outcome).Observe(float64(bytes))
}

// ObserveTCPDispatch 记录已登记C2S message、固定处理结果与端到端耗时。
func (metrics *Metrics) ObserveTCPDispatch(messageID uint32, outcome string, duration time.Duration) {
	message := fmt.Sprintf("%d", messageID)
	requireMetricLabel(message, "2000", "2103", "2105", "2107", "2109", "2111", "2113", "2115", "2117", "2119")
	requireMetricLabel(outcome, "ok", "state_rejected", "rate_limited", "in_flight_rejected", "application_error", "queue_rejected", "panic")
	if duration < 0 {
		panic("invalid tcp gameplay dispatch duration")
	}
	metrics.tcpDispatches.WithLabelValues(message, outcome).Inc()
	metrics.tcpDispatchSeconds.WithLabelValues(message, outcome).Observe(duration.Seconds())
}

// AddTCPInFlight 调整执行中operation gauge；调用方必须严格成对增加与归还。
func (metrics *Metrics) AddTCPInFlight(delta int) {
	if delta != 1 && delta != -1 {
		panic("invalid tcp gameplay in-flight delta")
	}
	metrics.tcpInFlight.Add(float64(delta))
}

// ObserveTCPQueue 记录双预算队列结果和瞬时占用。
func (metrics *Metrics) ObserveTCPQueue(outcome string, items int, bytes int) {
	requireMetricLabel(outcome, "accepted", "rejected")
	if items < 0 || bytes < 0 {
		panic("invalid tcp gameplay queue observation")
	}
	metrics.tcpQueues.WithLabelValues(outcome).Inc()
	metrics.tcpQueueItems.Observe(float64(items))
	metrics.tcpQueueBytes.Observe(float64(bytes))
}

// ObserveTCPPush 记录三个登记gameplay PUSH的稳定结果。
func (metrics *Metrics) ObserveTCPPush(messageID uint32, outcome string) {
	message := fmt.Sprintf("%d", messageID)
	requireMetricLabel(message, "2002", "2121", "2122")
	requireMetricLabel(outcome, "enqueued", "target_rejected", "queue_rejected")
	metrics.tcpPushes.WithLabelValues(message, outcome).Inc()
}

// ObserveTCPClose 记录不含peer文本的固定关闭原因。
func (metrics *Metrics) ObserveTCPClose(reason string) {
	requireMetricLabel(reason, "server_draining", "slow_consumer", "rate_limited", "protocol_violation", "idle_timeout", "peer_closed", "io_failed", "panic", "application_return", "fail_closed")
	metrics.tcpCloses.WithLabelValues(reason).Inc()
}

// ObserveTCPInvalidation 记录Session旧epoch连接清理结果。
func (metrics *Metrics) ObserveTCPInvalidation(outcome string) {
	requireMetricLabel(outcome, "closed", "deadline")
	metrics.tcpInvalidations.WithLabelValues(outcome).Inc()
}

// SetWorldRuntimes 更新当前进程逻辑 WorldInstance 数量。
func (metrics *Metrics) SetWorldRuntimes(value int) {
	if value < 0 {
		panic("invalid world runtime count")
	}
	metrics.worldRuntimes.Set(float64(value))
}

// SetSemanticDeadlines 更新单 worker 当前持有的语义 deadline 数量。
func (metrics *Metrics) SetSemanticDeadlines(value int) {
	if value < 0 {
		panic("invalid semantic deadline count")
	}
	metrics.semanticDeadlines.Set(float64(value))
}

// ObserveWorldLease 记录 lease 续约与失效的封闭结果。
func (metrics *Metrics) ObserveWorldLease(outcome string) {
	requireMetricLabel(outcome, "renewed", "retry", "lost", "expired", "failed")
	metrics.worldLeases.WithLabelValues(outcome).Inc()
}

// ObserveSemanticDeadline 记录封闭 kind 的执行结果。
func (metrics *Metrics) ObserveSemanticDeadline(kind string, outcome string) {
	requireMetricLabel(kind, "assignment_renew", "assignment_expiry", "visit_session", "invite", "reservation", "owner_grace", "visitor_grace")
	requireMetricLabel(outcome, "executed", "retry", "stale", "failed")
	metrics.deadlineRuns.WithLabelValues(kind, outcome).Inc()
}

// ObserveVisitLifecycle 记录受信 TCP lifecycle callback 的低基数结果。
func (metrics *Metrics) ObserveVisitLifecycle(operation string, outcome string) {
	requireMetricLabel(operation, "connect", "disconnect", "assignment_invalidate")
	requireMetricLabel(outcome, "applied", "ignored", "stale", "failed")
	metrics.visitLifecycles.WithLabelValues(operation, outcome).Inc()
}

// ObserveVisitDelivery 记录跨通道投递类型与稳定结果。
func (metrics *Metrics) ObserveVisitDelivery(kind string, outcome string) {
	requireMetricLabel(kind, "wss_push", "tcp_connection_push", "tcp_visit_push", "safe_return")
	requireMetricLabel(outcome, "delivered", "offline", "failed")
	metrics.visitDeliveries.WithLabelValues(kind, outcome).Inc()
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
