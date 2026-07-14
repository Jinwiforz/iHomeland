package observability

import (
	"fmt"
	"strings"
	"testing"
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
