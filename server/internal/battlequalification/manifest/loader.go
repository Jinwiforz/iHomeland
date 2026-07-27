package manifest

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/correlation"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/gateway"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/workload"
)

const (
	// maximumManifestBytes 限制每个 tracked JSON document 的读取量。
	maximumManifestBytes = 1 << 20
	// faultExecutionRelativePath 是 B0.6 runtime overlay 的固定位置。
	faultExecutionRelativePath = "shared/contracts/fixtures/battle/qualification/fault-execution.json"
	// faultMatrixRelativePath 是只读 B0.2 source matrix 的固定位置。
	faultMatrixRelativePath = "shared/contracts/fixtures/battle/network-profile/fault-matrix.json"
	// qualificationVersion 是当前 loader 唯一接受的 overlay 代际。
	qualificationVersion = "battle-network-qualification-v1"
	// profileVersion 是当前 loader 唯一接受的 B0.2 代际。
	profileVersion = "battle-network-profile-v2"
	// faultExecutionKind 是 overlay 的 closed document kind。
	faultExecutionKind = "fault-execution"
	// faultMatrixKind 是 B0.2 source 的 closed document kind。
	faultMatrixKind = "fault-matrix"
	// qualificationPRNG 是 scheduler 唯一允许的伪随机算法。
	qualificationPRNG = "lcg32-numerical-recipes"
	// measurementBandwidthMode 禁止 fault gateway 用 shaping 掩盖实际带宽超限。
	measurementBandwidthMode = "measure-only-no-shaping"
)

var (
	// ErrInvalidManifest 表示 tracked corpus 不完整、漂移或含未知字段。
	ErrInvalidManifest = errors.New("battle qualification manifest is invalid")
	// ErrScenarioNotFound 表示请求的 scenario 未在两份 corpus 中唯一登记。
	ErrScenarioNotFound = errors.New("battle qualification scenario is not registered")
)

// GatewayPolicy 是不允许由 CLI 补默认值的 socket/scheduler owner 配置。
type GatewayPolicy struct {
	// MaximumDatagramBytes 是 IPv6/UDP 无分片 payload ceiling。
	MaximumDatagramBytes int `json:"maximumDatagramBytes"`
	// GlobalQueueItems 是全部 packet copy 的 hard cap。
	GlobalQueueItems int `json:"globalQueueItems"`
	// IngressQueueItems 是 socket reader 到 event loop 的 hard cap。
	IngressQueueItems int `json:"ingressQueueItems"`
	// EvidenceQueueItems 是终局 metadata channel 的 hard cap。
	EvidenceQueueItems int `json:"evidenceQueueItems"`
	// PacketLifetimeMilliseconds 是 scheduler copy 的 absolute lifetime。
	PacketLifetimeMilliseconds int `json:"packetLifetimeMilliseconds"`
	// ReorderAdvanceMicroseconds 是命中 reorder 时的 due-time advance。
	ReorderAdvanceMicroseconds int `json:"reorderAdvanceMicroseconds"`
	// SchedulerPollMicroseconds 是 due queue 最大唤醒粒度。
	SchedulerPollMicroseconds int `json:"schedulerPollMicroseconds"`
	// MappingLateDeliveryMilliseconds 是 predecessor upstream 保留窗口。
	MappingLateDeliveryMilliseconds int `json:"mappingLateDeliveryMilliseconds"`
	// BandwidthMode 规定 profile matrix 只测量、不通过 shaping 掩盖超限。
	BandwidthMode string `json:"bandwidthMode"`
}

// ExecutionPolicy 冻结 scenario 的阶段预算。
type ExecutionPolicy struct {
	// WarmupMilliseconds 是不计入裁决的显式预热。
	WarmupMilliseconds int `json:"warmupMilliseconds"`
	// MeasurementMilliseconds 是 mandatory sample window。
	MeasurementMilliseconds int `json:"measurementMilliseconds"`
	// ScenarioDeadlineMilliseconds 是 warmup/measurement/收敛总 deadline。
	ScenarioDeadlineMilliseconds int `json:"scenarioDeadlineMilliseconds"`
	// CleanupMilliseconds 是独立回收预算。
	CleanupMilliseconds int `json:"cleanupMilliseconds"`
	// FaultShapePolicy 固定重复 source pattern。
	FaultShapePolicy string `json:"faultShapePolicy"`
	// PayloadPolicy 固定 opaque bytes。
	PayloadPolicy string `json:"payloadPolicy"`
	// UnsupportedPolicy 固定 fail closed。
	UnsupportedPolicy string `json:"unsupportedPolicy"`
}

// executionScenario 是 overlay 对 B0.2 source 的逐项映射。
type executionScenario struct {
	ScenarioID          string   `json:"scenarioId"`
	SourceScenarioID    string   `json:"sourceScenarioId"`
	SourceDurationTicks int      `json:"sourceDurationTicks"`
	SourceWorkloads     []string `json:"sourceWorkloads"`
	SourcePhases        []string `json:"sourcePhases"`
	Impairments         []string `json:"impairments"`
	ActorCount          uint8    `json:"actorCount"`
	Mandatory           bool     `json:"mandatory"`
}

// executionDocument 是 fault-execution.json 的 closed 投影。
type executionDocument struct {
	FormatVersion        int                 `json:"formatVersion"`
	QualificationVersion string              `json:"qualificationVersion"`
	DocumentKind         string              `json:"documentKind"`
	ProfileVersion       string              `json:"profileVersion"`
	Seed                 uint32              `json:"seed"`
	PRNG                 string              `json:"prng"`
	RequiredDirections   []string            `json:"requiredDirections"`
	SchedulerOrder       []string            `json:"schedulerOrder"`
	GatewayPolicy        GatewayPolicy       `json:"gatewayPolicy"`
	ExecutionPolicy      ExecutionPolicy     `json:"executionPolicy"`
	Scenarios            []executionScenario `json:"scenarios"`
}

// sourceScenario 是 B0.2 fault-matrix scenario 的完整数值输入。
type sourceScenario struct {
	ScenarioID       string   `json:"scenario_id"`
	Workloads        []string `json:"workloads"`
	Phases           []string `json:"phases"`
	Impairments      []string `json:"impairments"`
	DurationTicks    int      `json:"duration_ticks"`
	BaseLatencyUS    int      `json:"base_latency_us"`
	JitterUS         int      `json:"jitter_us"`
	LossPercent      uint8    `json:"loss_percent"`
	DuplicatePercent uint8    `json:"duplicate_percent"`
	ReorderPercent   uint8    `json:"reorder_percent"`
	BurstInterval    uint64   `json:"burst_interval"`
	BurstLength      uint64   `json:"burst_length"`
	QueueLimit       int      `json:"queue_limit"`
	ExpectedOutcome  string   `json:"expected_outcome"`
}

// matrixDocument 是只读 B0.2 fault-matrix 的 closed 投影。
type matrixDocument struct {
	FormatVersion       string           `json:"format_version"`
	ProfileVersion      string           `json:"profile_version"`
	DocumentKind        string           `json:"document_kind"`
	PRNG                string           `json:"prng"`
	Seed                uint32           `json:"seed"`
	RequiredImpairments []string         `json:"required_impairments"`
	RequiredWorkloads   []string         `json:"required_workloads"`
	RequiredPhases      []string         `json:"required_phases"`
	Scenarios           []sourceScenario `json:"scenarios"`
}

// Definition 是 exact overlay/source pair 的不可变运行时投影。
type Definition struct {
	// ScenarioID 是 report 使用的 B0.6 ID。
	ScenarioID string
	// SourceScenarioID 是 B0.2 source ID。
	SourceScenarioID string
	// WorkloadID 是 source matrix 中唯一登记的 workload。
	WorkloadID string
	// ActorCount 固定为 overlay 登记的五人矩阵数量。
	ActorCount uint8
	// Phases 是 source 的 closed workload phase 集合。
	Phases []correlation.WorkloadPhase
	// Impairments 是 source 的完整 fault coverage。
	Impairments []string
	// GatewayPolicy 冻结所有 socket/scheduler hard limits。
	GatewayPolicy GatewayPolicy
	// ExecutionPolicy 冻结 warmup、measurement、deadline 与 cleanup。
	ExecutionPolicy ExecutionPolicy
	// seed 是 exact B0.2 PRNG 初始状态。
	seed uint32
	// source 保存 latency/loss/reorder/queue 等 source 数值。
	source sourceScenario
}

// Load 从 repository root 只读加载并交叉校验一个 scenario。
func Load(repositoryRoot string, scenarioID string) (Definition, error) {
	if !filepath.IsAbs(repositoryRoot) || strings.TrimSpace(scenarioID) == "" {
		return Definition{}, ErrInvalidManifest
	}
	var execution executionDocument
	if err := readClosedJSON(
		filepath.Join(repositoryRoot, filepath.FromSlash(faultExecutionRelativePath)),
		&execution,
	); err != nil {
		return Definition{}, err
	}
	var matrix matrixDocument
	if err := readClosedJSON(
		filepath.Join(repositoryRoot, filepath.FromSlash(faultMatrixRelativePath)),
		&matrix,
	); err != nil {
		return Definition{}, err
	}
	if err := validateDocuments(execution, matrix); err != nil {
		return Definition{}, err
	}
	var overlay *executionScenario
	for index := range execution.Scenarios {
		if execution.Scenarios[index].ScenarioID == scenarioID {
			if overlay != nil {
				return Definition{}, ErrInvalidManifest
			}
			overlay = &execution.Scenarios[index]
		}
	}
	if overlay == nil {
		return Definition{}, ErrScenarioNotFound
	}
	var source *sourceScenario
	for index := range matrix.Scenarios {
		if matrix.Scenarios[index].ScenarioID == overlay.SourceScenarioID {
			if source != nil {
				return Definition{}, ErrInvalidManifest
			}
			source = &matrix.Scenarios[index]
		}
	}
	if source == nil || !overlayMatchesSource(*overlay, *source) ||
		len(source.Workloads) != 1 {
		return Definition{}, ErrInvalidManifest
	}
	phases := make([]correlation.WorkloadPhase, len(source.Phases))
	for index, phase := range source.Phases {
		phases[index] = correlation.WorkloadPhase(phase)
		if !phases[index].Valid() || phases[index] == correlation.PhaseAdmission {
			return Definition{}, ErrInvalidManifest
		}
	}
	return Definition{
		ScenarioID:       overlay.ScenarioID,
		SourceScenarioID: overlay.SourceScenarioID,
		WorkloadID:       source.Workloads[0],
		ActorCount:       overlay.ActorCount,
		Phases:           phases,
		Impairments:      slices.Clone(source.Impairments),
		GatewayPolicy:    execution.GatewayPolicy,
		ExecutionPolicy:  execution.ExecutionPolicy,
		seed:             execution.Seed,
		source:           *source,
	}, nil
}

// SocketConfig 生成不允许 CLI 改写的真实 gateway 配置。
func (definition Definition) SocketConfig(
	backend netip.AddrPort,
	frontendBind netip.AddrPort,
) (gateway.SocketConfig, error) {
	if !backend.IsValid() || !frontendBind.IsValid() {
		return gateway.SocketConfig{}, ErrInvalidManifest
	}
	initialSeed, err := definition.FaultPatternSeed(definition.Phases[0])
	if err != nil {
		return gateway.SocketConfig{}, err
	}
	base := gateway.DirectionPolicy{
		BaseLatency:      time.Duration(definition.source.BaseLatencyUS) * time.Microsecond,
		Jitter:           time.Duration(definition.source.JitterUS) * time.Microsecond,
		LossPercent:      definition.source.LossPercent,
		DuplicatePercent: definition.source.DuplicatePercent,
		ReorderPercent:   definition.source.ReorderPercent,
		BurstInterval:    definition.source.BurstInterval,
		BurstLength:      definition.source.BurstLength,
		QueueItems:       definition.source.QueueLimit,
		Bandwidth:        gateway.BandwidthPolicy{Enabled: false},
	}
	clean := base
	clean.BaseLatency = 0
	clean.Jitter = 0
	clean.LossPercent = 0
	clean.DuplicatePercent = 0
	clean.ReorderPercent = 0
	clean.BurstInterval = 0
	clean.BurstLength = 0
	uplink, downlink := base, base
	switch definition.SourceScenarioID {
	case "uplink-latency-jitter":
		downlink = clean
	case "downlink-latency-jitter":
		uplink = clean
	}
	config := gateway.SocketConfig{
		Scheduler: gateway.Config{
			Seed: initialSeed,
			PatternDuration: time.Duration(
				definition.source.DurationTicks,
			) * workload.SimulationCadence,
			BaselineGap: slices.Contains(
				definition.Impairments,
				"baseline-gap",
			),
			MaximumClients:       definition.ActorCount,
			MaximumDatagramBytes: definition.GatewayPolicy.MaximumDatagramBytes,
			GlobalQueueItems:     definition.GatewayPolicy.GlobalQueueItems,
			PacketLifetime: time.Duration(
				definition.GatewayPolicy.PacketLifetimeMilliseconds,
			) * time.Millisecond,
			ReorderAdvance: time.Duration(
				definition.GatewayPolicy.ReorderAdvanceMicroseconds,
			) * time.Microsecond,
			Uplink:   uplink,
			Downlink: downlink,
		},
		Backend:            backend,
		FrontendBind:       frontendBind,
		ClientCount:        definition.ActorCount,
		IngressQueueItems:  definition.GatewayPolicy.IngressQueueItems,
		EvidenceQueueItems: definition.GatewayPolicy.EvidenceQueueItems,
		SchedulerPollInterval: time.Duration(
			definition.GatewayPolicy.SchedulerPollMicroseconds,
		) * time.Microsecond,
		MappingLateDelivery: time.Duration(
			definition.GatewayPolicy.MappingLateDeliveryMilliseconds,
		) * time.Millisecond,
	}
	if err := config.Validate(); err != nil {
		return gateway.SocketConfig{}, fmt.Errorf("%w: gateway projection", ErrInvalidManifest)
	}
	return config, nil
}

// FaultPatternSeed 复现 B0.2 对 scenario、workload 与 phase 的 SHA-256 seed 派生。
func (definition Definition) FaultPatternSeed(
	phase correlation.WorkloadPhase,
) (uint32, error) {
	if !slices.Contains(definition.Phases, phase) {
		return 0, ErrInvalidManifest
	}
	key := fmt.Sprintf(
		"%s|%s|%s",
		definition.SourceScenarioID,
		definition.WorkloadID,
		phase,
	)
	digest := sha256.Sum256([]byte(key))
	seed := definition.seed + binary.BigEndian.Uint32(digest[:4])
	if seed == 0 {
		return 0, ErrInvalidManifest
	}
	return seed, nil
}

// readClosedJSON 限制 regular file、字节数、未知字段与 trailing document。
func readClosedJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("%w: open document", ErrInvalidManifest)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() ||
		info.Size() <= 0 || info.Size() > maximumManifestBytes {
		return fmt.Errorf("%w: document bounds", ErrInvalidManifest)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maximumManifestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: decode document", ErrInvalidManifest)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing document", ErrInvalidManifest)
	}
	return nil
}

// validateDocuments 拒绝 identity、coverage、方向、顺序与 gateway policy 漂移。
func validateDocuments(execution executionDocument, matrix matrixDocument) error {
	if execution.FormatVersion != 1 ||
		execution.QualificationVersion != qualificationVersion ||
		execution.DocumentKind != faultExecutionKind ||
		execution.ProfileVersion != profileVersion ||
		execution.PRNG != qualificationPRNG ||
		execution.Seed == 0 ||
		matrix.FormatVersion != "2" ||
		matrix.ProfileVersion != profileVersion ||
		matrix.DocumentKind != faultMatrixKind ||
		matrix.PRNG != qualificationPRNG ||
		matrix.Seed != execution.Seed ||
		!slices.Equal(execution.RequiredDirections, []string{"downlink", "uplink"}) ||
		!slices.Equal(execution.SchedulerOrder, []string{
			"due-time", "direction", "client-slot", "receive-sequence", "copy-index",
		}) ||
		execution.GatewayPolicy.BandwidthMode != measurementBandwidthMode ||
		len(execution.Scenarios) != len(matrix.Scenarios) {
		return ErrInvalidManifest
	}
	if execution.GatewayPolicy.MaximumDatagramBytes <= 0 ||
		execution.GatewayPolicy.GlobalQueueItems <= 0 ||
		execution.GatewayPolicy.IngressQueueItems <= 0 ||
		execution.GatewayPolicy.EvidenceQueueItems <= 0 ||
		execution.GatewayPolicy.PacketLifetimeMilliseconds <= 0 ||
		execution.GatewayPolicy.ReorderAdvanceMicroseconds < 0 ||
		execution.GatewayPolicy.SchedulerPollMicroseconds <= 0 ||
		execution.GatewayPolicy.MappingLateDeliveryMilliseconds <= 0 ||
		execution.ExecutionPolicy.WarmupMilliseconds <= 0 ||
		execution.ExecutionPolicy.MeasurementMilliseconds <= 0 ||
		execution.ExecutionPolicy.ScenarioDeadlineMilliseconds <=
			execution.ExecutionPolicy.WarmupMilliseconds+
				execution.ExecutionPolicy.MeasurementMilliseconds ||
		execution.ExecutionPolicy.CleanupMilliseconds <= 0 ||
		execution.ExecutionPolicy.FaultShapePolicy != "repeat-source-pattern" ||
		execution.ExecutionPolicy.PayloadPolicy != "opaque" ||
		execution.ExecutionPolicy.UnsupportedPolicy != "fail-closed" {
		return ErrInvalidManifest
	}
	return nil
}

// overlayMatchesSource 要求 runtime overlay 未删减或重写任何 source shape。
func overlayMatchesSource(overlay executionScenario, source sourceScenario) bool {
	return overlay.Mandatory &&
		overlay.ActorCount == 5 &&
		overlay.SourceDurationTicks == source.DurationTicks &&
		slices.Equal(overlay.SourceWorkloads, source.Workloads) &&
		slices.Equal(overlay.SourcePhases, source.Phases) &&
		slices.Equal(overlay.Impairments, source.Impairments)
}
