package processmetrics

import "errors"

var (
	// ErrUnsupported 表示当前平台不能提供 mandatory process counters。
	ErrUnsupported = errors.New("battle qualification process metrics are unsupported")
)

// Sample 是一个受监督 process 的累计与瞬时低敏指标。
type Sample struct {
	// Sequence 是当前 Sampler 从一开始的严格单调序列。
	Sequence uint64 `json:"sequence"`
	// MonotonicTimeUS 是相对 sampler 创建时刻的单调微秒。
	MonotonicTimeUS uint64 `json:"monotonicTimeUs"`
	// CPUTimeNS 是 kernel+user 的累计 CPU 纳秒。
	CPUTimeNS uint64 `json:"cpuTimeNs"`
	// WorkingSetBytes 是采样时驻留 working set。
	WorkingSetBytes uint64 `json:"workingSetBytes"`
	// HandleCount 是进程当前 handle 数量。
	HandleCount uint32 `json:"handleCount"`
	// ThreadCount 是进程当前 thread 数量。
	ThreadCount uint32 `json:"threadCount"`
}
