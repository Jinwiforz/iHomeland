package worldadmission

import (
	"testing"

	"github.com/jinwiforz/ihomeland/server/internal/observability"
	domain "github.com/jinwiforz/ihomeland/server/internal/worldadmission"
)

// TestConsumeBindingMismatchUsesRegisteredMetricLabel 验证拒绝路径不会用 Lua wire 值击穿 metrics 枚举。
func TestConsumeBindingMismatchUsesRegisteredMetricLabel(t *testing.T) {
	store := &Store{observer: observability.NewMetrics()}
	_, outcome, err := store.consumeResult(domain.ConsumeOutcomeBindingMismatch, "binding_mismatch")
	if err != nil || outcome != domain.ConsumeOutcomeBindingMismatch {
		t.Fatalf("consumeResult() outcome=%v error=%v", outcome, err)
	}
}
