package measurement

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

const (
	// controlMetricFamily 是 existing diagnostic listener 上的资格 control snapshot。
	controlMetricFamily = "ihomeland_server_battle_qualification_control"
	// processMetricFamily 是 existing diagnostic listener 上的资格 OS process sample。
	processMetricFamily = "ihomeland_server_battle_qualification_process"
)

var (
	// ErrInvalidMetrics 表示 exposition 缺失、重复、未知或不满足单调窗口。
	ErrInvalidMetrics = errors.New("battle qualification metrics are invalid")
)

var controlMetricNames = map[string]struct{}{
	"sample-sequence": {}, "committed-tick": {}, "node-count": {},
	"running-instance-count": {}, "active-session-count": {},
	"installed-ticket-count": {}, "raw-ingress-bytes": {},
	"raw-ingress-packets": {}, "raw-egress-bytes": {},
	"raw-egress-packets": {}, "kcp-ingress-bytes": {},
	"kcp-ingress-packets": {}, "kcp-egress-bytes": {},
	"kcp-egress-packets": {}, "dropped-packets": {},
	"rejected-packets": {}, "expired-messages": {},
	"kcp-retransmits": {}, "ingress-queue-high-watermark": {},
	"egress-queue-high-watermark": {}, "kcp-queue-high-watermark": {},
	"maximum-tick-duration-ns": {}, "tick-debt-high-watermark": {},
	"instance-memory-bytes": {}, "history-memory-bytes": {},
	"rebinds": {}, "rekeys": {}, "close-normal": {},
	"close-authentication": {}, "close-timeout": {}, "close-resource": {},
	"close-lifecycle": {}, "close-transport": {}, "close-internal": {},
}

var processMetricNames = map[string]struct{}{
	"sequence": {}, "monotonic-time-us": {}, "cpu-time-ns": {},
	"working-set-bytes": {}, "handle-count": {}, "thread-count": {},
}

var processRoles = map[string]struct{}{
	"go-parent": {}, "cpp-child": {},
}

// Snapshot 是一次 diagnostic scrape 的 closed 低敏投影。
type Snapshot struct {
	// Control 保存 exact C++ control sample。
	Control map[string]uint64 `json:"control"`
	// Processes 保存 Go parent 与 exact C++ child 的 OS sample。
	Processes map[string]map[string]uint64 `json:"processes"`
}

// Window 绑定 measurement 开始和结束的同源 samples。
type Window struct {
	// Start 是 warmup 结束后的起始 sample。
	Start Snapshot `json:"start"`
	// End 是 measurement 结束后的终止 sample。
	End Snapshot `json:"end"`
}

// SoakSummary 是一秒 cadence 采样的有界归约，不保存 1800 份重复 snapshot。
type SoakSummary struct {
	// SampleCount 是 post-warmup measurement 内成功验证的周期样本数。
	SampleCount uint64 `json:"sampleCount"`
	// MaximumActiveSessions 是窗口内 active session gauge 上界。
	MaximumActiveSessions uint64 `json:"maximumActiveSessions"`
	// MaximumInstalledTickets 是窗口内 installed ticket gauge 上界。
	MaximumInstalledTickets uint64 `json:"maximumInstalledTickets"`
	// MaximumIngressQueueItems 是窗口内 ingress queue high-watermark 上界。
	MaximumIngressQueueItems uint64 `json:"maximumIngressQueueItems"`
	// MaximumEgressQueueItems 是窗口内 egress queue high-watermark 上界。
	MaximumEgressQueueItems uint64 `json:"maximumEgressQueueItems"`
	// MaximumKCPQueueMessages 是窗口内 KCP queue high-watermark 上界。
	MaximumKCPQueueMessages uint64 `json:"maximumKcpQueueMessages"`
	// MaximumTickDebt 是窗口内 Tick debt high-watermark 上界。
	MaximumTickDebt uint64 `json:"maximumTickDebt"`
	// MaximumGoWorkingSetBytes 是周期采样到的 Go parent working set 上界。
	MaximumGoWorkingSetBytes uint64 `json:"maximumGoWorkingSetBytes"`
	// MaximumCppWorkingSetBytes 是周期采样到的 C++ child working set 上界。
	MaximumCppWorkingSetBytes uint64 `json:"maximumCppWorkingSetBytes"`
	// MonotonicCountersPassed 表示每个相邻 sample window 均通过单调验证。
	MonotonicCountersPassed bool `json:"monotonicCountersPassed"`
}

// ObserveSoakSample 验证相邻窗口并把瞬时/高水位值归约进固定大小 summary。
func (summary *SoakSummary) ObserveSoakSample(
	previous Snapshot,
	current Snapshot,
) error {
	if summary == nil {
		return ErrInvalidMetrics
	}
	if err := (Window{Start: previous, End: current}).Validate(); err != nil {
		return err
	}
	summary.SampleCount++
	summary.MaximumActiveSessions = max(
		summary.MaximumActiveSessions,
		current.Control["active-session-count"],
	)
	summary.MaximumInstalledTickets = max(
		summary.MaximumInstalledTickets,
		current.Control["installed-ticket-count"],
	)
	summary.MaximumIngressQueueItems = max(
		summary.MaximumIngressQueueItems,
		current.Control["ingress-queue-high-watermark"],
	)
	summary.MaximumEgressQueueItems = max(
		summary.MaximumEgressQueueItems,
		current.Control["egress-queue-high-watermark"],
	)
	summary.MaximumKCPQueueMessages = max(
		summary.MaximumKCPQueueMessages,
		current.Control["kcp-queue-high-watermark"],
	)
	summary.MaximumTickDebt = max(
		summary.MaximumTickDebt,
		current.Control["tick-debt-high-watermark"],
	)
	summary.MaximumGoWorkingSetBytes = max(
		summary.MaximumGoWorkingSetBytes,
		current.Processes["go-parent"]["working-set-bytes"],
	)
	summary.MaximumCppWorkingSetBytes = max(
		summary.MaximumCppWorkingSetBytes,
		current.Processes["cpp-child"]["working-set-bytes"],
	)
	summary.MonotonicCountersPassed = true
	return nil
}

// ParsePrometheus 只提取两类资格 metric，拒绝重复、未知 label 与非整数数值。
func ParsePrometheus(reader io.Reader) (Snapshot, error) {
	if reader == nil {
		return Snapshot{}, ErrInvalidMetrics
	}
	snapshot := Snapshot{
		Control:   make(map[string]uint64, len(controlMetricNames)),
		Processes: make(map[string]map[string]uint64, len(processRoles)),
	}
	scanner := bufio.NewScanner(io.LimitReader(reader, 1<<20))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(line, controlMetricFamily+"{"):
			labels, value, err := parseSample(line, controlMetricFamily)
			if err != nil || len(labels) != 1 {
				return Snapshot{}, ErrInvalidMetrics
			}
			name := labels["metric"]
			if _, ok := controlMetricNames[name]; !ok {
				return Snapshot{}, ErrInvalidMetrics
			}
			if _, duplicate := snapshot.Control[name]; duplicate {
				return Snapshot{}, ErrInvalidMetrics
			}
			snapshot.Control[name] = value
		case strings.HasPrefix(line, processMetricFamily+"{"):
			labels, value, err := parseSample(line, processMetricFamily)
			if err != nil || len(labels) != 2 {
				return Snapshot{}, ErrInvalidMetrics
			}
			role, metric := labels["role"], labels["metric"]
			if _, ok := processRoles[role]; !ok {
				return Snapshot{}, ErrInvalidMetrics
			}
			if _, ok := processMetricNames[metric]; !ok {
				return Snapshot{}, ErrInvalidMetrics
			}
			if snapshot.Processes[role] == nil {
				snapshot.Processes[role] = make(map[string]uint64, len(processMetricNames))
			}
			if _, duplicate := snapshot.Processes[role][metric]; duplicate {
				return Snapshot{}, ErrInvalidMetrics
			}
			snapshot.Processes[role][metric] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("%w: read exposition", ErrInvalidMetrics)
	}
	if err := snapshot.Validate(); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

// Validate 要求两类 metric catalog 都完整出现。
func (snapshot Snapshot) Validate() error {
	if len(snapshot.Control) != len(controlMetricNames) ||
		len(snapshot.Processes) != len(processRoles) {
		return ErrInvalidMetrics
	}
	for role := range processRoles {
		if len(snapshot.Processes[role]) != len(processMetricNames) {
			return ErrInvalidMetrics
		}
	}
	return nil
}

// Validate 验证 sample sequence、累计 counters、monotonic time 与 process CPU 不回退。
func (window Window) Validate() error {
	if err := window.Start.Validate(); err != nil {
		return err
	}
	if err := window.End.Validate(); err != nil {
		return err
	}
	if window.End.Control["sample-sequence"] <= window.Start.Control["sample-sequence"] ||
		window.End.Control["committed-tick"] <= window.Start.Control["committed-tick"] {
		return ErrInvalidMetrics
	}
	for metric := range controlMetricNames {
		if metric == "running-instance-count" ||
			metric == "active-session-count" ||
			metric == "installed-ticket-count" {
			continue
		}
		if window.End.Control[metric] < window.Start.Control[metric] {
			return ErrInvalidMetrics
		}
	}
	for role := range processRoles {
		start, end := window.Start.Processes[role], window.End.Processes[role]
		if end["sequence"] <= start["sequence"] ||
			end["monotonic-time-us"] <= start["monotonic-time-us"] ||
			end["cpu-time-ns"] < start["cpu-time-ns"] {
			return ErrInvalidMetrics
		}
	}
	return nil
}

// parseSample 解码 Prometheus text format 中无转义 stable labels 与非负整数 gauge。
func parseSample(line, family string) (map[string]string, uint64, error) {
	if !strings.HasPrefix(line, family+"{") {
		return nil, 0, ErrInvalidMetrics
	}
	closing := strings.Index(line, "} ")
	if closing < 0 {
		return nil, 0, ErrInvalidMetrics
	}
	labelText := line[len(family)+1 : closing]
	valueText := strings.TrimSpace(line[closing+2:])
	if strings.ContainsAny(labelText, "\\\n\r") || strings.Contains(valueText, " ") {
		return nil, 0, ErrInvalidMetrics
	}
	labels := make(map[string]string)
	for _, pair := range strings.Split(labelText, ",") {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 || len(parts[1]) < 2 ||
			parts[1][0] != '"' || parts[1][len(parts[1])-1] != '"' {
			return nil, 0, ErrInvalidMetrics
		}
		name, value := parts[0], parts[1][1:len(parts[1])-1]
		if name == "" || value == "" {
			return nil, 0, ErrInvalidMetrics
		}
		if _, duplicate := labels[name]; duplicate {
			return nil, 0, ErrInvalidMetrics
		}
		labels[name] = value
	}
	parsed, err := strconv.ParseFloat(valueText, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) ||
		parsed < 0 || parsed != math.Trunc(parsed) ||
		parsed > float64(^uint64(0)) {
		return nil, 0, ErrInvalidMetrics
	}
	return labels, uint64(parsed), nil
}
