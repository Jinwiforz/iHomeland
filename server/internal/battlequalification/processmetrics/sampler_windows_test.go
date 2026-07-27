//go:build windows

package processmetrics

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestSamplerReadsCurrentProcessWithoutIdentityLeak 验证 Windows 四项 mandatory counter 与 cleanup。
func TestSamplerReadsCurrentProcessWithoutIdentityLeak(t *testing.T) {
	sampler, err := New(os.Getpid())
	if err != nil {
		t.Fatalf("new sampler: %v", err)
	}
	first, err := sampler.Sample()
	if err != nil {
		t.Fatalf("first sample: %v", err)
	}
	second, err := sampler.Sample()
	if err != nil {
		t.Fatalf("second sample: %v", err)
	}
	if first.Sequence != 1 || second.Sequence != 2 ||
		second.CPUTimeNS < first.CPUTimeNS ||
		first.WorkingSetBytes == 0 || first.HandleCount == 0 ||
		first.ThreadCount == 0 {
		t.Fatalf("invalid process samples: first=%+v second=%+v", first, second)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal process sample: %v", err)
	}
	if strings.Contains(strings.ToLower(string(encoded)), "pid") {
		t.Fatal("process sample exposed PID field")
	}
	if err := sampler.Close(); err != nil {
		t.Fatalf("close sampler: %v", err)
	}
	if _, err := sampler.Sample(); err == nil {
		t.Fatal("closed sampler returned a sample")
	}
}
