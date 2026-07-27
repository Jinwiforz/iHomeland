package measurement

import (
	"errors"
	"strings"
	"testing"
)

// TestParsePrometheusAndWindow 验证 closed catalog、label 顺序和跨窗口单调性。
func TestParsePrometheusAndWindow(t *testing.T) {
	start := completeExposition(1, 10, 1)
	end := completeExposition(2, 11, 2)
	startSnapshot, err := ParsePrometheus(strings.NewReader(start))
	if err != nil {
		t.Fatalf("parse start: %v", err)
	}
	endSnapshot, err := ParsePrometheus(strings.NewReader(end))
	if err != nil {
		t.Fatalf("parse end: %v", err)
	}
	if err := (Window{Start: startSnapshot, End: endSnapshot}).Validate(); err != nil {
		t.Fatalf("validate window: %v", err)
	}
}

// TestParsePrometheusRejectsIncompleteDuplicateAndRegression 验证缺失、重复与回退 fail closed。
func TestParsePrometheusRejectsIncompleteDuplicateAndRegression(t *testing.T) {
	if _, err := ParsePrometheus(strings.NewReader(
		`ihomeland_server_battle_qualification_control{metric="sample-sequence"} 1`,
	)); !errors.Is(err, ErrInvalidMetrics) {
		t.Fatalf("incomplete exposition err=%v", err)
	}
	duplicate := completeExposition(1, 10, 1) +
		"ihomeland_server_battle_qualification_control{metric=\"sample-sequence\"} 1\n"
	if _, err := ParsePrometheus(strings.NewReader(duplicate)); !errors.Is(err, ErrInvalidMetrics) {
		t.Fatalf("duplicate exposition err=%v", err)
	}
	start, _ := ParsePrometheus(strings.NewReader(completeExposition(2, 10, 2)))
	end, _ := ParsePrometheus(strings.NewReader(completeExposition(1, 11, 1)))
	if err := (Window{Start: start, End: end}).Validate(); !errors.Is(err, ErrInvalidMetrics) {
		t.Fatalf("regressed window err=%v", err)
	}
}

// completeExposition 生成包含全部 mandatory fields 的最小测试 exposition。
func completeExposition(sampleSequence, committedTick, processSequence uint64) string {
	var builder strings.Builder
	for metric := range controlMetricNames {
		value := uint64(0)
		switch metric {
		case "sample-sequence":
			value = sampleSequence
		case "committed-tick":
			value = committedTick
		case "node-count", "running-instance-count", "active-session-count":
			value = 1
		}
		builder.WriteString("ihomeland_server_battle_qualification_control{metric=\"")
		builder.WriteString(metric)
		builder.WriteString("\"} ")
		builder.WriteString(formatUint(value))
		builder.WriteByte('\n')
	}
	for role := range processRoles {
		for metric := range processMetricNames {
			value := uint64(1)
			switch metric {
			case "sequence":
				value = processSequence
			case "monotonic-time-us":
				value = processSequence * 1000
			case "cpu-time-ns":
				value = processSequence * 100
			}
			builder.WriteString("ihomeland_server_battle_qualification_process{metric=\"")
			builder.WriteString(metric)
			builder.WriteString("\",role=\"")
			builder.WriteString(role)
			builder.WriteString("\"} ")
			builder.WriteString(formatUint(value))
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

// TestSoakSummaryObservesEveryAdjacentWindow 验证周期归约保留峰值且拒绝 counter 回退。
func TestSoakSummaryObservesEveryAdjacentWindow(t *testing.T) {
	start, err := ParsePrometheus(strings.NewReader(completeExposition(1, 10, 1)))
	if err != nil {
		t.Fatalf("parse start: %v", err)
	}
	end, err := ParsePrometheus(strings.NewReader(completeExposition(2, 11, 2)))
	if err != nil {
		t.Fatalf("parse end: %v", err)
	}
	end.Control["active-session-count"] = 5
	end.Control["installed-ticket-count"] = 5
	end.Control["ingress-queue-high-watermark"] = 7
	end.Processes["go-parent"]["working-set-bytes"] = 1024
	end.Processes["cpp-child"]["working-set-bytes"] = 2048
	var summary SoakSummary
	if err := summary.ObserveSoakSample(start, end); err != nil {
		t.Fatalf("observe sample: %v", err)
	}
	if summary.SampleCount != 1 ||
		summary.MaximumActiveSessions != 5 ||
		summary.MaximumIngressQueueItems != 7 ||
		summary.MaximumGoWorkingSetBytes != 1024 ||
		summary.MaximumCppWorkingSetBytes != 2048 ||
		!summary.MonotonicCountersPassed {
		t.Fatalf("summary = %+v", summary)
	}
	if err := summary.ObserveSoakSample(end, start); err == nil {
		t.Fatal("counter regression was accepted")
	}
}

// formatUint 避免测试依赖 Prometheus formatter。
func formatUint(value uint64) string {
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	for value != 0 {
		index--
		buffer[index] = byte('0' + value%10)
		value /= 10
	}
	return string(buffer[index:])
}
