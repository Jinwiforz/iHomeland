package simulationcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"

	"github.com/jinwiforz/ihomeland/server/internal/placement"
)

// BattleQualificationMetrics 是 C++ node 返回的低敏累计与峰值。
type BattleQualificationMetrics struct {
	// RawIngressBytes 是已接受 raw 入站 bytes。
	RawIngressBytes uint64
	// RawIngressPackets 是已接受 raw 入站 packets。
	RawIngressPackets uint64
	// RawEgressBytes 是已生成 raw 出站 bytes。
	RawEgressBytes uint64
	// RawEgressPackets 是已生成 raw 出站 packets。
	RawEgressPackets uint64
	// KCPIngressBytes 是已接受 KCP 入站 bytes。
	KCPIngressBytes uint64
	// KCPIngressPackets 是已接受 KCP 入站 packets。
	KCPIngressPackets uint64
	// KCPEgressBytes 是已生成 KCP 出站 bytes。
	KCPEgressBytes uint64
	// KCPEgressPackets 是已生成 KCP 出站 packets。
	KCPEgressPackets uint64
	// DroppedPackets 是 runtime 主动丢弃累计。
	DroppedPackets uint64
	// RejectedPackets 是 protocol、authority 与资源拒绝累计。
	RejectedPackets uint64
	// ExpiredMessages 是 deadline 终结累计。
	ExpiredMessages uint64
	// KCPRetransmits 是 KCP xmit 单调增量累计。
	KCPRetransmits uint64
	// IngressQueueHighWatermark 是入站 items 峰值。
	IngressQueueHighWatermark uint64
	// EgressQueueHighWatermark 是出站 items 峰值。
	EgressQueueHighWatermark uint64
	// KCPQueueHighWatermark 是 KCP application/inflight/waiting 峰值。
	KCPQueueHighWatermark uint64
	// MaximumTickDurationNS 是完整 observer Tick 最大耗时。
	MaximumTickDurationNS uint64
	// TickDebtHighWatermark 是最大待处理 clock credits。
	TickDebtHighWatermark uint64
	// InstanceMemoryBytes 是 live instance owners 的实际预留 accounting 峰值。
	InstanceMemoryBytes uint64
	// HistoryMemoryBytes 是 live HistoryRing 的实际预留 accounting 峰值。
	HistoryMemoryBytes uint64
	// Rebinds 是成功 endpoint generation 切换累计。
	Rebinds uint64
	// Rekeys 是成功 key epoch 切换累计。
	Rekeys uint64
	// CloseNormal 是 normal 终结累计。
	CloseNormal uint64
	// CloseAuthentication 是 authentication 终结累计。
	CloseAuthentication uint64
	// CloseTimeout 是 timeout 终结累计。
	CloseTimeout uint64
	// CloseResource 是 resource 终结累计。
	CloseResource uint64
	// CloseLifecycle 是 lifecycle 终结累计。
	CloseLifecycle uint64
	// CloseTransport 是 transport 终结累计。
	CloseTransport uint64
	// CloseInternal 是 internal 终结累计。
	CloseInternal uint64
}

// BattleQualificationSnapshot 绑定一次 run/sample 与 exact node/instance。
type BattleQualificationSnapshot struct {
	// RunID 是显式启用 controller 时冻结的资格 run。
	RunID QualificationRunID
	// SampleSequence 从一开始且每次成功读取精确递增一。
	SampleSequence uint64
	// NodeID 是当前 child incarnation。
	NodeID SimulationNodeID
	// InstanceID 是当前不可复活 simulation worker。
	InstanceID SimulationInstanceID
	// AssignmentFingerprint 绑定 current placement stamp。
	AssignmentFingerprint Digest
	// CommittedTick 是读取时已完整提交的最后 Tick。
	CommittedTick uint64
	// NodeCount 对单 child snapshot 固定为一。
	NodeCount uint64
	// RunningInstanceCount 是当前 node 占用 slots。
	RunningInstanceCount uint64
	// ActiveSessionCount 是已消费且未终结 ticket 数。
	ActiveSessionCount uint64
	// InstalledTicketCount 是尚未消费的 ticket 数。
	InstalledTicketCount uint64
	// Metrics 是不清零的低敏 runtime 累计与峰值。
	Metrics BattleQualificationMetrics
}

// QualificationSnapshot 读取 exact current instance 的不清零低敏指标。
func (controller *Controller) QualificationSnapshot(
	ctx context.Context,
	runID QualificationRunID,
	stamp placement.AssignmentStamp,
) (BattleQualificationSnapshot, error) {
	if controller == nil || ctx == nil || !runID.Valid() || !stamp.Valid() {
		return BattleQualificationSnapshot{}, errors.New(
			"battle qualification snapshot input is invalid",
		)
	}
	controller.operationMutex.Lock()
	defer controller.operationMutex.Unlock()
	if !controller.config.QualificationMode ||
		controller.config.QualificationRunID != runID {
		return BattleQualificationSnapshot{}, errors.New(
			"battle qualification snapshot is disabled or stale",
		)
	}
	binding, err := controller.exactBinding(stamp)
	if err != nil {
		return BattleQualificationSnapshot{}, err
	}
	if controller.qualificationSampleSequence == math.MaxUint64 {
		return BattleQualificationSnapshot{}, errors.New(
			"battle qualification sample sequence is exhausted",
		)
	}
	sampleSequence := controller.qualificationSampleSequence + 1
	requestID, err := newRequestID("qsnapshot")
	if err != nil {
		return BattleQualificationSnapshot{}, err
	}
	payload, err := controller.session.Call(
		ctx,
		requestID,
		"battle_qualification_snapshot_request",
		struct {
			AssignmentFingerprint string `json:"assignmentFingerprint"`
			QualificationRunID    string `json:"qualificationRunId"`
			SampleSequence        string `json:"sampleSequence"`
			SimulationInstanceID  string `json:"simulationInstanceId"`
			SimulationNodeID      string `json:"simulationNodeId"`
		}{
			AssignmentFingerprint: binding.fingerprint.String(),
			QualificationRunID:    runID.String(),
			SampleSequence: strconv.FormatUint(
				sampleSequence,
				10,
			),
			SimulationInstanceID: binding.instanceID.String(),
			SimulationNodeID:     controller.config.NodeID.String(),
		},
		"battle_qualification_snapshot_receipt",
	)
	if err != nil {
		return BattleQualificationSnapshot{}, err
	}
	snapshot, err := decodeQualificationSnapshot(
		payload,
		controller.config,
		binding,
		runID,
		sampleSequence,
	)
	if err != nil {
		controller.mutex.Lock()
		controller.healthy = false
		controller.mutex.Unlock()
		_ = controller.session.Close()
		return BattleQualificationSnapshot{}, err
	}
	controller.qualificationSampleSequence = sampleSequence
	return snapshot, nil
}

// qualificationMetricsWire 保留 JSON decimal 文本直到全部 closed 校验完成。
type qualificationMetricsWire struct {
	// CloseAuthentication 是 authentication 终结累计文本。
	CloseAuthentication string `json:"closeAuthentication"`
	// CloseInternal 是 internal 终结累计文本。
	CloseInternal string `json:"closeInternal"`
	// CloseLifecycle 是 lifecycle 终结累计文本。
	CloseLifecycle string `json:"closeLifecycle"`
	// CloseNormal 是 normal 终结累计文本。
	CloseNormal string `json:"closeNormal"`
	// CloseResource 是 resource 终结累计文本。
	CloseResource string `json:"closeResource"`
	// CloseTimeout 是 timeout 终结累计文本。
	CloseTimeout string `json:"closeTimeout"`
	// CloseTransport 是 transport 终结累计文本。
	CloseTransport string `json:"closeTransport"`
	// DroppedPackets 是 runtime 丢弃累计文本。
	DroppedPackets string `json:"droppedPackets"`
	// EgressQueueHighWatermark 是出站 queue 峰值文本。
	EgressQueueHighWatermark string `json:"egressQueueHighWatermark"`
	// ExpiredMessages 是 deadline 终结累计文本。
	ExpiredMessages string `json:"expiredMessages"`
	// HistoryMemoryBytes 是 live history 预留 accounting 文本。
	HistoryMemoryBytes string `json:"historyMemoryBytes"`
	// IngressQueueHighWatermark 是入站 queue 峰值文本。
	IngressQueueHighWatermark string `json:"ingressQueueHighWatermark"`
	// InstanceMemoryBytes 是 live instance 预留 accounting 文本。
	InstanceMemoryBytes string `json:"instanceMemoryBytes"`
	// KCPEgressBytes 是 KCP 出站 bytes 文本。
	KCPEgressBytes string `json:"kcpEgressBytes"`
	// KCPEgressPackets 是 KCP 出站 packets 文本。
	KCPEgressPackets string `json:"kcpEgressPackets"`
	// KCPIngressBytes 是 KCP 入站 bytes 文本。
	KCPIngressBytes string `json:"kcpIngressBytes"`
	// KCPIngressPackets 是 KCP 入站 packets 文本。
	KCPIngressPackets string `json:"kcpIngressPackets"`
	// KCPQueueHighWatermark 是 KCP queue 峰值文本。
	KCPQueueHighWatermark string `json:"kcpQueueHighWatermark"`
	// KCPRetransmits 是 KCP xmit 增量累计文本。
	KCPRetransmits string `json:"kcpRetransmits"`
	// MaximumTickDurationNS 是最大 Tick 耗时文本。
	MaximumTickDurationNS string `json:"maximumTickDurationNs"`
	// RawEgressBytes 是 raw 出站 bytes 文本。
	RawEgressBytes string `json:"rawEgressBytes"`
	// RawEgressPackets 是 raw 出站 packets 文本。
	RawEgressPackets string `json:"rawEgressPackets"`
	// RawIngressBytes 是 raw 入站 bytes 文本。
	RawIngressBytes string `json:"rawIngressBytes"`
	// RawIngressPackets 是 raw 入站 packets 文本。
	RawIngressPackets string `json:"rawIngressPackets"`
	// Rebinds 是成功 endpoint generation 切换累计文本。
	Rebinds string `json:"rebinds"`
	// RejectedPackets 是 closed gate 拒绝累计文本。
	RejectedPackets string `json:"rejectedPackets"`
	// Rekeys 是成功 key epoch 切换累计文本。
	Rekeys string `json:"rekeys"`
	// TickDebtHighWatermark 是最大待处理 clock credits 文本。
	TickDebtHighWatermark string `json:"tickDebtHighWatermark"`
}

// qualificationSnapshotWire 是 receipt 的 closed wire representation。
type qualificationSnapshotWire struct {
	// ActiveSessionCount 是已消费且未终结 ticket 数文本。
	ActiveSessionCount string `json:"activeSessionCount"`
	// AssignmentFingerprint 绑定 current placement stamp。
	AssignmentFingerprint string `json:"assignmentFingerprint"`
	// CommittedTick 是最后完整 Tick 文本。
	CommittedTick string `json:"committedTick"`
	// InstalledTicketCount 是尚未消费 ticket 数文本。
	InstalledTicketCount string `json:"installedTicketCount"`
	// Metrics 是 closed 低敏计数集合。
	Metrics qualificationMetricsWire `json:"metrics"`
	// NodeCount 对单 child snapshot 固定为一。
	NodeCount string `json:"nodeCount"`
	// QualificationRunID 绑定显式资格 run。
	QualificationRunID string `json:"qualificationRunId"`
	// RunningInstanceCount 是 node 占用 slots 文本。
	RunningInstanceCount string `json:"runningInstanceCount"`
	// SampleSequence 是连续 sample 文本。
	SampleSequence string `json:"sampleSequence"`
	// SimulationInstanceID 绑定不可复活 worker。
	SimulationInstanceID string `json:"simulationInstanceId"`
	// SimulationNodeID 绑定 child incarnation。
	SimulationNodeID string `json:"simulationNodeId"`
}

// qualificationMetricIndex 为固定 wire 顺序提供具名索引，禁止位置数字散落。
type qualificationMetricIndex uint8

const (
	qualificationMetricRawIngressBytes qualificationMetricIndex = iota
	qualificationMetricRawIngressPackets
	qualificationMetricRawEgressBytes
	qualificationMetricRawEgressPackets
	qualificationMetricKCPIngressBytes
	qualificationMetricKCPIngressPackets
	qualificationMetricKCPEgressBytes
	qualificationMetricKCPEgressPackets
	qualificationMetricDroppedPackets
	qualificationMetricRejectedPackets
	qualificationMetricExpiredMessages
	qualificationMetricKCPRetransmits
	qualificationMetricIngressQueueHighWatermark
	qualificationMetricEgressQueueHighWatermark
	qualificationMetricKCPQueueHighWatermark
	qualificationMetricMaximumTickDurationNS
	qualificationMetricTickDebtHighWatermark
	qualificationMetricInstanceMemoryBytes
	qualificationMetricHistoryMemoryBytes
	qualificationMetricRebinds
	qualificationMetricRekeys
	qualificationMetricCloseNormal
	qualificationMetricCloseAuthentication
	qualificationMetricCloseTimeout
	qualificationMetricCloseResource
	qualificationMetricCloseLifecycle
	qualificationMetricCloseTransport
	qualificationMetricCloseInternal
	qualificationMetricCount
)

// decodeQualificationSnapshot 校验 closed receipt、全部 decimal 与 exact binding。
func decodeQualificationSnapshot(
	payload json.RawMessage,
	config ControllerConfig,
	binding instanceBinding,
	runID QualificationRunID,
	sampleSequence uint64,
) (BattleQualificationSnapshot, error) {
	var wire qualificationSnapshotWire
	if err := decodeClosedPayload(payload, &wire); err != nil {
		return BattleQualificationSnapshot{}, err
	}
	sequence, err := parseCanonicalUint64(wire.SampleSequence)
	if err != nil {
		return BattleQualificationSnapshot{}, err
	}
	nodeID, err := NewSimulationNodeID(wire.SimulationNodeID)
	if err != nil {
		return BattleQualificationSnapshot{}, err
	}
	instanceID, err := NewSimulationInstanceID(wire.SimulationInstanceID)
	if err != nil {
		return BattleQualificationSnapshot{}, err
	}
	fingerprint, err := NewDigest(wire.AssignmentFingerprint)
	if err != nil {
		return BattleQualificationSnapshot{}, err
	}
	if wire.QualificationRunID != runID.String() ||
		sequence != sampleSequence ||
		nodeID != config.NodeID ||
		instanceID != binding.instanceID ||
		fingerprint != binding.fingerprint {
		return BattleQualificationSnapshot{}, errors.New(
			"battle qualification snapshot binding drifted",
		)
	}
	committedTick, err := parseCanonicalUint64AllowZero(wire.CommittedTick)
	if err != nil {
		return BattleQualificationSnapshot{}, err
	}
	nodeCount, err := parseCanonicalUint64(wire.NodeCount)
	if err != nil || nodeCount != 1 {
		return BattleQualificationSnapshot{}, errors.New(
			"battle qualification node count is invalid",
		)
	}
	runningInstances, err := parseCanonicalUint64(wire.RunningInstanceCount)
	if err != nil || runningInstances > uint64(config.Capacity.Instances) {
		return BattleQualificationSnapshot{}, errors.New(
			"battle qualification instance count is invalid",
		)
	}
	activeSessions, err := parseCanonicalUint64AllowZero(wire.ActiveSessionCount)
	if err != nil || activeSessions > uint64(config.Capacity.Actors) {
		return BattleQualificationSnapshot{}, errors.New(
			"battle qualification active session count is invalid",
		)
	}
	installedTickets, err := parseCanonicalUint64AllowZero(wire.InstalledTicketCount)
	if err != nil ||
		installedTickets > uint64(config.Capacity.Actors) ||
		activeSessions+installedTickets > uint64(config.Capacity.Actors) {
		return BattleQualificationSnapshot{}, errors.New(
			"battle qualification ticket count is invalid",
		)
	}
	metrics, err := decodeQualificationMetrics(wire.Metrics)
	if err != nil {
		return BattleQualificationSnapshot{}, err
	}
	return BattleQualificationSnapshot{
		RunID:                 runID,
		SampleSequence:        sampleSequence,
		NodeID:                nodeID,
		InstanceID:            instanceID,
		AssignmentFingerprint: fingerprint,
		CommittedTick:         committedTick,
		NodeCount:             nodeCount,
		RunningInstanceCount:  runningInstances,
		ActiveSessionCount:    activeSessions,
		InstalledTicketCount:  installedTickets,
		Metrics:               metrics,
	}, nil
}

// decodeQualificationMetrics 逐字段拒绝非规范或溢出的累计文本。
func decodeQualificationMetrics(
	wire qualificationMetricsWire,
) (BattleQualificationMetrics, error) {
	fields := []string{
		wire.RawIngressBytes,
		wire.RawIngressPackets,
		wire.RawEgressBytes,
		wire.RawEgressPackets,
		wire.KCPIngressBytes,
		wire.KCPIngressPackets,
		wire.KCPEgressBytes,
		wire.KCPEgressPackets,
		wire.DroppedPackets,
		wire.RejectedPackets,
		wire.ExpiredMessages,
		wire.KCPRetransmits,
		wire.IngressQueueHighWatermark,
		wire.EgressQueueHighWatermark,
		wire.KCPQueueHighWatermark,
		wire.MaximumTickDurationNS,
		wire.TickDebtHighWatermark,
		wire.InstanceMemoryBytes,
		wire.HistoryMemoryBytes,
		wire.Rebinds,
		wire.Rekeys,
		wire.CloseNormal,
		wire.CloseAuthentication,
		wire.CloseTimeout,
		wire.CloseResource,
		wire.CloseLifecycle,
		wire.CloseTransport,
		wire.CloseInternal,
	}
	if len(fields) != int(qualificationMetricCount) {
		return BattleQualificationMetrics{}, errors.New(
			"battle qualification metric catalog is incomplete",
		)
	}
	values := make([]uint64, qualificationMetricCount)
	for index, field := range fields {
		value, err := parseCanonicalUint64AllowZero(field)
		if err != nil {
			return BattleQualificationMetrics{}, err
		}
		values[index] = value
	}
	return BattleQualificationMetrics{
		RawIngressBytes:           values[qualificationMetricRawIngressBytes],
		RawIngressPackets:         values[qualificationMetricRawIngressPackets],
		RawEgressBytes:            values[qualificationMetricRawEgressBytes],
		RawEgressPackets:          values[qualificationMetricRawEgressPackets],
		KCPIngressBytes:           values[qualificationMetricKCPIngressBytes],
		KCPIngressPackets:         values[qualificationMetricKCPIngressPackets],
		KCPEgressBytes:            values[qualificationMetricKCPEgressBytes],
		KCPEgressPackets:          values[qualificationMetricKCPEgressPackets],
		DroppedPackets:            values[qualificationMetricDroppedPackets],
		RejectedPackets:           values[qualificationMetricRejectedPackets],
		ExpiredMessages:           values[qualificationMetricExpiredMessages],
		KCPRetransmits:            values[qualificationMetricKCPRetransmits],
		IngressQueueHighWatermark: values[qualificationMetricIngressQueueHighWatermark],
		EgressQueueHighWatermark:  values[qualificationMetricEgressQueueHighWatermark],
		KCPQueueHighWatermark:     values[qualificationMetricKCPQueueHighWatermark],
		MaximumTickDurationNS:     values[qualificationMetricMaximumTickDurationNS],
		TickDebtHighWatermark:     values[qualificationMetricTickDebtHighWatermark],
		InstanceMemoryBytes:       values[qualificationMetricInstanceMemoryBytes],
		HistoryMemoryBytes:        values[qualificationMetricHistoryMemoryBytes],
		Rebinds:                   values[qualificationMetricRebinds],
		Rekeys:                    values[qualificationMetricRekeys],
		CloseNormal:               values[qualificationMetricCloseNormal],
		CloseAuthentication:       values[qualificationMetricCloseAuthentication],
		CloseTimeout:              values[qualificationMetricCloseTimeout],
		CloseResource:             values[qualificationMetricCloseResource],
		CloseLifecycle:            values[qualificationMetricCloseLifecycle],
		CloseTransport:            values[qualificationMetricCloseTransport],
		CloseInternal:             values[qualificationMetricCloseInternal],
	}, nil
}
