package observability

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestStorageMetricLabelsRejectUnboundedValues 防止 endpoint、key、identity 或原始错误成为 label。
func TestStorageMetricLabelsRejectUnboundedValues(t *testing.T) {
	t.Parallel()

	const sensitive = "ih:production:session:lease:sensitive-identity"
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("unbounded storage metric label 应被拒绝")
		}
		if strings.Contains(fmt.Sprint(recovered), sensitive) {
			t.Fatalf("metric label panic 泄露原值：%v", recovered)
		}
	}()
	NewMetrics().ObserveStorageProbe("redis", sensitive, 0)
}

// TestStorageMetricLabelsAcceptFixedVocabulary 保护 runtime 使用的 pool、probe、migration 与 operation 枚举。
func TestStorageMetricLabelsAcceptFixedVocabulary(t *testing.T) {
	t.Parallel()

	metrics := NewMetrics()
	metrics.SetStoragePool("mysql", "open", 1)
	metrics.ObserveStorageProbe("redis", "ok", 0.01)
	metrics.ObserveMigration("applied", 1)
	metrics.RecordStorageOperation("mysql", "transaction", "commit_unknown")
}

// TestWebSocketMetricLabelsAcceptFixedVocabulary 保护握手、心跳和关闭路径实际使用的稳定枚举。
func TestWebSocketMetricLabelsAcceptFixedVocabulary(t *testing.T) {
	t.Parallel()

	metrics := NewMetrics()
	metrics.ObserveWSSHandshake("tls_version_rejected")
	metrics.ObserveWSSHandshake("panic")
	metrics.ObserveWSSHandshake("panic_after_upgrade")
	metrics.ObserveWSSHeartbeat("idle_timeout")
	for _, reason := range []string{"server_draining", "session_invalidated", "slow_consumer", "protocol_violation", "heartbeat_timeout", "idle_timeout", "peer_closed", "io_failed", "panic", "local_close"} {
		metrics.ObserveWSSClose(reason)
	}
}

// TestTCPGameplayMetricLabelsAcceptFixedVocabulary 保护TCP握手、dispatch、队列与关闭实际枚举。
func TestTCPGameplayMetricLabelsAcceptFixedVocabulary(t *testing.T) {
	t.Parallel()

	metrics := NewMetrics()
	metrics.ObserveTCPHandshake("preface", "accepted")
	metrics.SetTCPConnections("pending", 1)
	metrics.ObserveTCPFrame("c2s", "accepted", 128)
	metrics.ObserveTCPDispatch(2000, "ok", time.Millisecond)
	metrics.AddTCPInFlight(1)
	metrics.AddTCPInFlight(-1)
	metrics.ObserveTCPQueue("accepted", 1, 128)
	metrics.ObserveTCPPush(2122, "enqueued")
	metrics.ObserveTCPClose("slow_consumer")
	metrics.ObserveTCPClose("application_return")
	metrics.ObserveTCPClose("fail_closed")
	metrics.ObserveTCPInvalidation("closed")
}

// TestPersonalWorldSliceMetricLabelsAcceptFixedVocabulary 保护竖切资源与结果的封闭标签集合。
func TestPersonalWorldSliceMetricLabelsAcceptFixedVocabulary(t *testing.T) {
	t.Parallel()
	metrics := NewMetrics()
	metrics.SetWorldRuntimes(1)
	metrics.SetSemanticDeadlines(2)
	metrics.ObserveWorldLease("renewed")
	metrics.ObserveSemanticDeadline("owner_grace", "executed")
	metrics.ObserveVisitLifecycle("disconnect", "stale")
	metrics.ObserveVisitDelivery("safe_return", "offline")
}

// TestTCPGameplayMetricLabelsRejectCredential 防止credential或动态target进入label。
func TestTCPGameplayMetricLabelsRejectCredential(t *testing.T) {
	t.Parallel()

	const sensitive = "wad1_sensitive-admission-material"
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("credential metric label 应被拒绝")
		}
		if strings.Contains(fmt.Sprint(recovered), sensitive) {
			t.Fatalf("metric label panic 泄露原值：%v", recovered)
		}
	}()
	NewMetrics().ObserveTCPHandshake("preface", sensitive)
}
