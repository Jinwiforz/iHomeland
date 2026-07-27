package manifest

import (
	"maps"
	"path/filepath"
	"slices"
	"time"
)

var (
	expectedLifecycleOwners = map[string]string{
		"assignment-replacement":     LifecycleOwnerProcessSupervisor,
		"child-crash-restart":        LifecycleOwnerProcessSupervisor,
		"go-restart":                 LifecycleOwnerProcessSupervisor,
		"network-pause-resume":       LifecycleOwnerFaultGateway,
		"owner-grace-expiry":         LifecycleOwnerPublicProtocol,
		"session-epoch-invalidation": LifecycleOwnerPublicProtocol,
		"shutdown-drain-deadline":    LifecycleOwnerProcessSupervisor,
		"valid-endpoint-rebind":      LifecycleOwnerFaultGateway,
		"visitor-leave":              LifecycleOwnerPublicProtocol,
		"visitor-reconnect":          LifecycleOwnerPublicProtocol,
	}
	expectedLifecycleModes = map[string]string{
		"assignment-replacement":     "replaced",
		"child-crash-restart":        "replaced",
		"go-restart":                 "replaced",
		"network-pause-resume":       "preserved",
		"owner-grace-expiry":         "terminated",
		"session-epoch-invalidation": "terminated",
		"shutdown-drain-deadline":    "terminated",
		"valid-endpoint-rebind":      "preserved",
		"visitor-leave":              "terminated",
		"visitor-reconnect":          "replaced",
	}
)

const (
	// lifecycleSecurityRelativePath 是 B0.6 security/lifecycle/soak policy 的固定位置。
	lifecycleSecurityRelativePath = "shared/contracts/fixtures/battle/qualification/lifecycle-security.json"
	// lifecycleSecurityKind 是该 tracked document 的 closed kind。
	lifecycleSecurityKind = "lifecycle-security"
	// LifecycleOwnerPublicProtocol 由公开 HTTPS/WSS/TLS-TCP testclient 执行。
	LifecycleOwnerPublicProtocol = "public-protocol"
	// LifecycleOwnerFaultGateway 由 opaque loopback gateway 与协议客户端执行。
	LifecycleOwnerFaultGateway = "fault-gateway"
	// LifecycleOwnerProcessSupervisor 由唯一 PowerShell environment owner 执行。
	LifecycleOwnerProcessSupervisor = "process-supervisor"
)

// SoakPolicy 是 30 分钟运行的全部时间、actor 与 bounded-growth gate。
type SoakPolicy struct {
	// WorkloadID 绑定 default-coop admission/workload。
	WorkloadID string `json:"workloadId"`
	// DurationMilliseconds 是 warmup 后 mandatory measurement window。
	DurationMilliseconds int `json:"durationMilliseconds"`
	// SampleIntervalMilliseconds 是 server periodic sampler cadence。
	SampleIntervalMilliseconds int `json:"sampleIntervalMilliseconds"`
	// WarmupMilliseconds 是不参与 memory growth 基线的预热。
	WarmupMilliseconds int `json:"warmupMilliseconds"`
	// RekeyIntervalMilliseconds 是显式 authenticated rekey cadence。
	RekeyIntervalMilliseconds int `json:"rekeyIntervalMilliseconds"`
	// MinimumObservedRekeys 是 client/control 两源都必须达到的数量。
	MinimumObservedRekeys uint64 `json:"minimumObservedRekeys"`
	// CleanupMilliseconds 是独立 cleanup deadline。
	CleanupMilliseconds int `json:"cleanupMilliseconds"`
	// RequiredInvariants 是 soak report 必须保留的完整 coverage。
	RequiredInvariants []string `json:"requiredInvariants"`
}

// attackBudgetDocument 保留 security runtime 的全部 hard limits。
type attackBudgetDocument struct {
	MaximumSenders                int    `json:"maximumSenders"`
	MaximumPacketsPerSecondSender int    `json:"maximumPacketsPerSecondPerSender"`
	MaximumDurationMilliseconds   int    `json:"maximumDurationMilliseconds"`
	MaximumTotalBytes             int    `json:"maximumTotalBytes"`
	LegitimateActorCount          uint8  `json:"legitimateActorCount"`
	AvailabilityPolicy            string `json:"availabilityPolicy"`
}

// SecurityPolicy 是真实 attack runner 的 closed case inventory 与资源上限。
type SecurityPolicy struct {
	// MaximumSenders 是同时存在的非合法 source hard cap。
	MaximumSenders int
	// MaximumPacketsPerSecondSender 是每个 source 的发送速率 hard cap。
	MaximumPacketsPerSecondSender int
	// MaximumDurationMilliseconds 是单个攻击窗口 hard deadline。
	MaximumDurationMilliseconds int
	// MaximumTotalBytes 是单个 security case 的累计 wire byte hard cap。
	MaximumTotalBytes int
	// LegitimateActorCount 是必须与攻击并发的合法 workload 数量。
	LegitimateActorCount uint8
	// Cases 是排序后的 mandatory security inventory。
	Cases []string
}

// LifecyclePolicy 是受控业务、网络与进程迁移的 closed case inventory。
type LifecyclePolicy struct {
	// Cases 是排序后的 mandatory lifecycle identity。
	Cases []string
	// Owners 为每个 case 指定唯一受控执行边界。
	Owners map[string]string
	// Modes 为每个 case 指定 preserved、replaced 或 terminated 语义。
	Modes map[string]string
}

// lifecycleSecurityDocument 是 lifecycle-security.json 的 closed projection。
type lifecycleSecurityDocument struct {
	FormatVersion        int                  `json:"formatVersion"`
	QualificationVersion string               `json:"qualificationVersion"`
	DocumentKind         string               `json:"documentKind"`
	AttackBudget         attackBudgetDocument `json:"attackBudget"`
	SecurityCases        []string             `json:"securityCases"`
	LifecycleCases       []string             `json:"lifecycleCases"`
	LifecycleOwners      map[string]string    `json:"lifecycleOwners"`
	LifecycleModes       map[string]string    `json:"lifecycleModes"`
	Soak                 SoakPolicy           `json:"soak"`
}

// LoadSoakPolicy 只读加载并校验 mandatory soak policy，不接受 CLI duration override。
func LoadSoakPolicy(repositoryRoot string) (SoakPolicy, error) {
	document, err := loadLifecycleSecurity(repositoryRoot)
	if err != nil {
		return SoakPolicy{}, err
	}
	return document.Soak, nil
}

// LoadSecurityPolicy 加载真实攻击矩阵与 hard limits，不允许 CLI 自行补默认值。
func LoadSecurityPolicy(repositoryRoot string) (SecurityPolicy, error) {
	document, err := loadLifecycleSecurity(repositoryRoot)
	if err != nil {
		return SecurityPolicy{}, err
	}
	return SecurityPolicy{
		MaximumSenders:                document.AttackBudget.MaximumSenders,
		MaximumPacketsPerSecondSender: document.AttackBudget.MaximumPacketsPerSecondSender,
		MaximumDurationMilliseconds:   document.AttackBudget.MaximumDurationMilliseconds,
		MaximumTotalBytes:             document.AttackBudget.MaximumTotalBytes,
		LegitimateActorCount:          document.AttackBudget.LegitimateActorCount,
		Cases:                         slices.Clone(document.SecurityCases),
	}, nil
}

// LoadLifecyclePolicy 加载 mandatory lifecycle inventory，不允许 CLI 扩展 case。
func LoadLifecyclePolicy(repositoryRoot string) (LifecyclePolicy, error) {
	document, err := loadLifecycleSecurity(repositoryRoot)
	if err != nil {
		return LifecyclePolicy{}, err
	}
	return LifecyclePolicy{
		Cases:  slices.Clone(document.LifecycleCases),
		Owners: cloneStringMap(document.LifecycleOwners),
		Modes:  cloneStringMap(document.LifecycleModes),
	}, nil
}

// loadLifecycleSecurity 是 security 与 soak loader 共用的唯一 closed validator。
func loadLifecycleSecurity(repositoryRoot string) (lifecycleSecurityDocument, error) {
	var document lifecycleSecurityDocument
	if err := readClosedJSON(
		filepath.Join(repositoryRoot, filepath.FromSlash(lifecycleSecurityRelativePath)),
		&document,
	); err != nil {
		return lifecycleSecurityDocument{}, err
	}
	if document.FormatVersion != 1 ||
		document.QualificationVersion != qualificationVersion ||
		document.DocumentKind != lifecycleSecurityKind ||
		document.AttackBudget.MaximumSenders <= 0 ||
		document.AttackBudget.MaximumPacketsPerSecondSender <= 0 ||
		document.AttackBudget.MaximumDurationMilliseconds <= 0 ||
		document.AttackBudget.MaximumTotalBytes <= 0 ||
		document.AttackBudget.LegitimateActorCount != 5 ||
		document.AttackBudget.AvailabilityPolicy !=
			"legitimate-workload-remains-within-budget" ||
		len(document.SecurityCases) != 17 ||
		len(document.LifecycleCases) != 10 ||
		!validLifecycleExecution(document) ||
		document.Soak.WorkloadID != "default-coop" ||
		document.Soak.DurationMilliseconds != int((30*time.Minute)/time.Millisecond) ||
		document.Soak.SampleIntervalMilliseconds != int(time.Second/time.Millisecond) ||
		document.Soak.WarmupMilliseconds <= 0 ||
		document.Soak.RekeyIntervalMilliseconds <= 0 ||
		document.Soak.MinimumObservedRekeys < 2 ||
		document.Soak.CleanupMilliseconds <= 0 ||
		len(document.Soak.RequiredInvariants) == 0 {
		return lifecycleSecurityDocument{}, ErrInvalidManifest
	}
	if hasDuplicate(document.SecurityCases) ||
		hasDuplicate(document.LifecycleCases) ||
		hasDuplicate(document.Soak.RequiredInvariants) ||
		!slices.IsSorted(document.SecurityCases) ||
		!slices.IsSorted(document.LifecycleCases) ||
		!slices.IsSorted(document.Soak.RequiredInvariants) {
		return lifecycleSecurityDocument{}, ErrInvalidManifest
	}
	return document, nil
}

// validLifecycleExecution 验证 case、owner 与 transition mode 三份 keyset 完全一致。
func validLifecycleExecution(document lifecycleSecurityDocument) bool {
	if len(document.LifecycleOwners) != len(document.LifecycleCases) ||
		len(document.LifecycleModes) != len(document.LifecycleCases) ||
		!maps.Equal(document.LifecycleOwners, expectedLifecycleOwners) ||
		!maps.Equal(document.LifecycleModes, expectedLifecycleModes) {
		return false
	}
	for _, caseID := range document.LifecycleCases {
		owner := document.LifecycleOwners[caseID]
		mode := document.LifecycleModes[caseID]
		if (owner != LifecycleOwnerPublicProtocol &&
			owner != LifecycleOwnerFaultGateway &&
			owner != LifecycleOwnerProcessSupervisor) ||
			(mode != "preserved" && mode != "replaced" && mode != "terminated") {
			return false
		}
	}
	return true
}

// cloneStringMap 隔离 manifest runtime projection，禁止调用方修改 loader 状态。
func cloneStringMap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

// hasDuplicate 检测已由 source schema 声明 unique 的 runtime slice drift。
func hasDuplicate(values []string) bool {
	for index := 1; index < len(values); index++ {
		if slices.Contains(values[:index], values[index]) {
			return true
		}
	}
	return false
}
