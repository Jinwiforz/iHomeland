package manifest

import (
	"errors"
	"path/filepath"
)

const (
	// metricsRelativePath 是 B0.6 metric catalog 的固定位置。
	metricsRelativePath = "shared/contracts/fixtures/battle/qualification/metrics.json"
	// metricsKind 是 metric catalog 的 closed document kind。
	metricsKind = "metrics"
	// ProcessWorkingSetGrowthMetricID 是 soak process growth 的唯一预算项。
	ProcessWorkingSetGrowthMetricID = "process-working-set-growth"
)

// MetricSpec 是一个冻结测量项的 source、单位、预算与聚合契约。
type MetricSpec struct {
	// MetricID 是跨 evidence/report 稳定的低敏标识。
	MetricID string `json:"metricId"`
	// Source 是四源 evidence 中的唯一事实 owner。
	Source string `json:"source"`
	// Unit 禁止聚合器猜测单位或隐式换算。
	Unit string `json:"unit"`
	// BudgetSource 是该上限的长期规格来源标识。
	BudgetSource string `json:"budgetSource"`
	// Method 是唯一允许的 evidence 归约公式。
	Method string `json:"method"`
	// Window 是 measurement 或 post-warmup-soak。
	Window string `json:"window"`
	// Maximum 是允许的 inclusive upper bound。
	Maximum uint64 `json:"maximum"`
	// ReproducibilityToleranceBasisPoints 是两次 verify 的相对漂移上限。
	ReproducibilityToleranceBasisPoints uint32 `json:"reproducibilityToleranceBasisPoints"`
	// Aggregation 是 scenario 内唯一允许的归约方法。
	Aggregation string `json:"aggregation"`
}

// MetricCatalog 是资格入口与 finalizer 共享的冻结预算集合。
type MetricCatalog struct {
	// Metrics 按 tracked document 顺序保留全部 metric。
	Metrics []MetricSpec
	byID    map[string]MetricSpec
}

// metricComparisonPolicy 是跨 run 复现性的 closed policy。
type metricComparisonPolicy struct {
	BothRunsMustPassBudget       bool   `json:"bothRunsMustPassBudget"`
	Aggregation                  string `json:"aggregation"`
	ReproducibilityFailurePolicy string `json:"reproducibilityFailurePolicy"`
	MissingSamplePolicy          string `json:"missingSamplePolicy"`
}

// metricsDocument 是 metrics.json 的 closed runtime projection。
type metricsDocument struct {
	FormatVersion        int                    `json:"formatVersion"`
	QualificationVersion string                 `json:"qualificationVersion"`
	DocumentKind         string                 `json:"documentKind"`
	Sources              []string               `json:"sources"`
	ComparisonPolicy     metricComparisonPolicy `json:"comparisonPolicy"`
	Metrics              []MetricSpec           `json:"metrics"`
}

// expectedMetricIdentity 把稳定 metric ID 绑定到唯一 evidence source 与归约公式。
func expectedMetricIdentity(metricID string) (string, string, bool) {
	switch metricID {
	case "uplink-bytes-per-player-second":
		return "fault-gateway", "delivered-uplink-gateway-bytes-divided-by-window-and-actors", true
	case "downlink-bytes-per-player-second":
		return "fault-gateway", "delivered-downlink-gateway-bytes-divided-by-window-and-actors", true
	case "downlink-bytes-per-instance-second":
		return "fault-gateway", "delivered-gateway-bytes-divided-by-window", true
	case "delivery-age":
		return "fault-gateway", "maximum-gateway-delivery-age", true
	case "baseline-recovery":
		return "client", "maximum-resync-to-successor-baseline", true
	case "kcp-amplification":
		return "control-snapshot", "kcp-egress-xmit-divided-by-first-transmissions", true
	case "ingress-queue-items":
		return "control-snapshot", "maximum-control-ingress-queue-high-watermark", true
	case "egress-queue-items":
		return "control-snapshot", "maximum-control-egress-queue-high-watermark", true
	case "kcp-queue-messages":
		return "control-snapshot", "maximum-control-kcp-queue-high-watermark", true
	case "cpu-per-simulation-tick":
		return "control-snapshot", "maximum-tick-duration-ns-to-us-ceiling", true
	case "hard-tick-debt":
		return "control-snapshot", "maximum-control-tick-debt-high-watermark", true
	case "instance-memory":
		return "control-snapshot", "maximum-control-accounted-instance-bytes", true
	case "history-memory":
		return "control-snapshot", "maximum-control-accounted-history-bytes", true
	case ProcessWorkingSetGrowthMetricID:
		return "process-sampler", "maximum-end-minus-start-positive", true
	default:
		return "", "", false
	}
}

// LoadMetricCatalog 加载所有预算；runtime 与 finalizer 不得复制数值常量。
func LoadMetricCatalog(repositoryRoot string) (MetricCatalog, error) {
	var document metricsDocument
	if err := readClosedJSON(
		filepath.Join(repositoryRoot, filepath.FromSlash(metricsRelativePath)),
		&document,
	); err != nil {
		return MetricCatalog{}, err
	}
	if document.FormatVersion != 1 ||
		document.QualificationVersion != qualificationVersion ||
		document.DocumentKind != metricsKind ||
		len(document.Sources) != 4 ||
		document.ComparisonPolicy.Aggregation != "worst-case" ||
		!document.ComparisonPolicy.BothRunsMustPassBudget ||
		document.ComparisonPolicy.ReproducibilityFailurePolicy != "not-qualified" ||
		document.ComparisonPolicy.MissingSamplePolicy != "not-qualified" ||
		len(document.Metrics) == 0 ||
		hasDuplicate(document.Sources) {
		return MetricCatalog{}, ErrInvalidManifest
	}
	catalog := MetricCatalog{
		Metrics: append([]MetricSpec(nil), document.Metrics...),
		byID:    make(map[string]MetricSpec, len(document.Metrics)),
	}
	for _, metric := range document.Metrics {
		expectedSource, expectedMethod, registered :=
			expectedMetricIdentity(metric.MetricID)
		if metric.MetricID == "" || metric.Source == "" || metric.Unit == "" ||
			metric.BudgetSource == "" || metric.Method == "" ||
			!registered ||
			metric.Source != expectedSource ||
			metric.Method != expectedMethod ||
			(metric.Window != "measurement" &&
				metric.Window != "post-warmup-soak") ||
			metric.Maximum == 0 ||
			metric.Aggregation == "" {
			return MetricCatalog{}, ErrInvalidManifest
		}
		if _, exists := catalog.byID[metric.MetricID]; exists {
			return MetricCatalog{}, ErrInvalidManifest
		}
		catalog.byID[metric.MetricID] = metric
	}
	if _, ok := catalog.byID[ProcessWorkingSetGrowthMetricID]; !ok {
		return MetricCatalog{}, ErrInvalidManifest
	}
	return catalog, nil
}

// Metric 返回已登记预算；调用方必须处理缺项而不能补默认值。
func (catalog MetricCatalog) Metric(metricID string) (MetricSpec, error) {
	metric, ok := catalog.byID[metricID]
	if !ok {
		return MetricSpec{}, errors.New("battle qualification metric is not registered")
	}
	return metric, nil
}
