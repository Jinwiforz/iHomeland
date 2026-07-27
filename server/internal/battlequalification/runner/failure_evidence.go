package runner

import (
	"errors"

	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/gateway"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/manifest"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/protocolclient"
)

// ScenarioFailureEvidence 保留一次真实场景失败时可安全落盘的最小分层状态。
type ScenarioFailureEvidence struct {
	// SchemaVersion 是 failure evidence closed schema generation。
	SchemaVersion int `json:"schemaVersion"`
	// QualificationVersion 绑定 B0.6 corpus identity。
	QualificationVersion string `json:"qualificationVersion"`
	// EvidenceKind 区分失败诊断与可进入 finalize 的通过 evidence。
	EvidenceKind string `json:"evidenceKind"`
	// ScenarioID 是本次执行的 B0.6 overlay identity。
	ScenarioID string `json:"scenarioId"`
	// SourceScenarioID 是只读 B0.2 source identity。
	SourceScenarioID string `json:"sourceScenarioId"`
	// WorkloadID 是本场景冻结的 workload。
	WorkloadID string `json:"workloadId"`
	// ActorCount 是场景实际要求的并发协议客户端数量。
	ActorCount uint8 `json:"actorCount"`
	// Failure 是命中失败边界的单 client 原子低敏投影。
	Failure ClientFailureEvidence `json:"failure"`
	// Disposition 固定为 failed，禁止进入资格聚合。
	Disposition string `json:"disposition"`
}

// ClientFailureEvidence 区分 secure delivery、KCP ACK 与 reconciliation 层。
type ClientFailureEvidence struct {
	// Stage 是 supervisor 允许公开的稳定失败码。
	Stage string `json:"stage"`
	// ClientSlot 是 run-local session correlation，不是 PlayerID。
	ClientSlot uint8 `json:"clientSlot"`
	// PollFailureCode 是 poll-receipt-v1 的闭合数值枚举。
	PollFailureCode uint8 `json:"pollFailureCode"`
	// SnapshotCount 是失败前已通过 raw validator 的累计 snapshot 数。
	SnapshotCount uint64 `json:"snapshotCount"`
	// ReliableCount 是失败前已通过 KCP/application validator 的累计消息数。
	ReliableCount uint64 `json:"reliableCount"`
	// LatestServerTick 是失败前已认证 application payload 的权威 Tick 前沿。
	LatestServerTick uint64 `json:"latestServerTick"`
	// KCP 是同一 poll failure 原子投影的低敏 KCP 状态。
	KCP ClientKCPFailureEvidence `json:"kcp"`
	// GatewayKCP 是失败 client 在 gateway 终局 metadata 中的 KCP 分层汇总。
	GatewayKCP GatewayKCPFailureEvidence `json:"gatewayKcp"`
}

// ClientKCPFailureEvidence 只保存固定容量与累计计数，不包含 packet identity 或 payload。
type ClientKCPFailureEvidence struct {
	// CloseReason 是独立客户端 KCP lane 的闭合终态。
	CloseReason uint8 `json:"closeReason"`
	// SecureDatagrams 是通过 AEAD/replay 后进入 KCP lane 的累计 datagram 数。
	SecureDatagrams uint64 `json:"secureDatagrams"`
	// InputDatagrams 是通过 KCP header gate 后交给 primitive 的累计 datagram 数。
	InputDatagrams uint64 `json:"inputDatagrams"`
	// InputACKCommands 是有效入站 datagram 中的累计 ACK command 数。
	InputACKCommands uint64 `json:"inputAckCommands"`
	// InputPushCommands 是有效入站 datagram 中的累计 PUSH command 数。
	InputPushCommands uint64 `json:"inputPushCommands"`
	// OutputDatagrams 是 KCP primitive 交给 secure lane 的累计 datagram 数。
	OutputDatagrams uint64 `json:"outputDatagrams"`
	// ReconciledMessages 是因 peer ACK 从 inflight 集合回收的累计 message 数。
	ReconciledMessages uint64 `json:"reconciledMessages"`
	// QueuedMessages 是尚未提交给 KCP primitive 的当前 message 数。
	QueuedMessages uint64 `json:"queuedMessages"`
	// InflightMessages 是尚未完成 ACK reconciliation 的当前 message 数。
	InflightMessages uint64 `json:"inflightMessages"`
	// WaitingSegments 是 KCP send queue 与 send buffer 的当前 segment 数。
	WaitingSegments uint64 `json:"waitingSegments"`
}

// GatewayKCPFailureEvidence 保存失败 client 的双向 KCP 终局守恒计数。
type GatewayKCPFailureEvidence struct {
	// UplinkTerminal 是 gateway 已终结的 client-to-server KCP copy 数。
	UplinkTerminal uint64 `json:"uplinkTerminal"`
	// UplinkDelivered 是 gateway 已写入 backend socket 的 KCP copy 数。
	UplinkDelivered uint64 `json:"uplinkDelivered"`
	// DownlinkTerminal 是 gateway 已终结的 server-to-client KCP copy 数。
	DownlinkTerminal uint64 `json:"downlinkTerminal"`
	// DownlinkDelivered 是 gateway 已写入 client socket 的 KCP copy 数。
	DownlinkDelivered uint64 `json:"downlinkDelivered"`
	// Packets 按 direction、disposition 固定顺序保存非零终局计数。
	Packets []GatewayKCPPacketFailureEvidence `json:"packets"`
}

// GatewayKCPPacketFailureEvidence 是一个 closed direction/disposition 计数桶。
type GatewayKCPPacketFailureEvidence struct {
	// Direction 是 uplink 或 downlink。
	Direction string `json:"direction"`
	// Disposition 是 gateway 已登记的唯一终局裁决。
	Disposition string `json:"disposition"`
	// Count 是该计数桶内的 KCP copy 数。
	Count uint64 `json:"count"`
}

// scenarioFailureStateError 在 cleanup 后把完整 gateway 终局绑定到原始失败链。
type scenarioFailureStateError struct {
	cause      error
	gatewayKCP GatewayKCPFailureEvidence
}

// Error 不向进程边界泄露内部 metadata。
func (failure *scenarioFailureStateError) Error() string {
	return "battle qualification scenario failed"
}

// Unwrap 保留原始 poll failure 与稳定阶段码。
func (failure *scenarioFailureStateError) Unwrap() error {
	return failure.cause
}

// captureScenarioFailureState 只为可归属到单 client 的 poll failure 建立 gateway 投影。
func captureScenarioFailureState(cause error, metadata []gateway.Metadata) error {
	var failure protocolclient.PollFailure
	if !errors.As(cause, &failure) || failure.Receipt.ClientSlot == 0 {
		return cause
	}
	return &scenarioFailureStateError{
		cause:      cause,
		gatewayKCP: summarizeFailureGatewayKCP(metadata, failure.Receipt.ClientSlot),
	}
}

// summarizeFailureGatewayKCP 从唯一终局 metadata 构造固定顺序、无 payload 的计数。
func summarizeFailureGatewayKCP(
	metadata []gateway.Metadata,
	clientSlot uint8,
) GatewayKCPFailureEvidence {
	counts := make(map[[2]uint8]uint64)
	for _, packet := range metadata {
		if packet.ClientSlot != clientSlot ||
			packet.PacketKind != gateway.PublicPacketKCP {
			continue
		}
		counts[[2]uint8{uint8(packet.Direction), uint8(packet.Disposition)}]++
	}
	result := GatewayKCPFailureEvidence{
		Packets: make([]GatewayKCPPacketFailureEvidence, 0),
	}
	directions := []gateway.Direction{
		gateway.DirectionUplink,
		gateway.DirectionDownlink,
	}
	dispositions := []gateway.Disposition{
		gateway.DispositionDelivered,
		gateway.DispositionLoss,
		gateway.DispositionBurst,
		gateway.DispositionMTU,
		gateway.DispositionQueue,
		gateway.DispositionDeadline,
		gateway.DispositionPaused,
		gateway.DispositionMappingExpired,
		gateway.DispositionWriteFailed,
		gateway.DispositionClosed,
		gateway.DispositionSourceRejected,
	}
	for _, direction := range directions {
		for _, disposition := range dispositions {
			count := counts[[2]uint8{uint8(direction), uint8(disposition)}]
			if count == 0 {
				continue
			}
			if direction == gateway.DirectionUplink {
				result.UplinkTerminal += count
				if disposition == gateway.DispositionDelivered {
					result.UplinkDelivered += count
				}
			} else {
				result.DownlinkTerminal += count
				if disposition == gateway.DispositionDelivered {
					result.DownlinkDelivered += count
				}
			}
			result.Packets = append(result.Packets, GatewayKCPPacketFailureEvidence{
				Direction:   direction.String(),
				Disposition: disposition.String(),
				Count:       count,
			})
		}
	}
	return result
}

// NewScenarioFailureEvidence 从完整 error chain 提取 poll failure，不伪造其他失败层。
func NewScenarioFailureEvidence(
	definition manifest.Definition,
	cause error,
) (ScenarioFailureEvidence, bool) {
	var failure protocolclient.PollFailure
	if !errors.As(cause, &failure) ||
		failure.Code == 0 ||
		failure.Receipt.ClientSlot == 0 ||
		failure.Receipt.ClientSlot > definition.ActorCount {
		return ScenarioFailureEvidence{}, false
	}
	var state *scenarioFailureStateError
	if !errors.As(cause, &state) {
		return ScenarioFailureEvidence{}, false
	}
	kcp := failure.Receipt.KCP
	return ScenarioFailureEvidence{
		SchemaVersion:        1,
		QualificationVersion: "battle-network-qualification-v1",
		EvidenceKind:         "scenario-failure-evidence",
		ScenarioID:           definition.ScenarioID,
		SourceScenarioID:     definition.SourceScenarioID,
		WorkloadID:           definition.WorkloadID,
		ActorCount:           definition.ActorCount,
		Failure: ClientFailureEvidence{
			Stage:            failure.QualificationFailureCode(),
			ClientSlot:       failure.Receipt.ClientSlot,
			PollFailureCode:  uint8(failure.Code),
			SnapshotCount:    failure.Receipt.SnapshotCount,
			ReliableCount:    failure.Receipt.ReliableCount,
			LatestServerTick: failure.Receipt.LatestServerTick,
			KCP: ClientKCPFailureEvidence{
				CloseReason:        uint8(kcp.CloseReason),
				SecureDatagrams:    kcp.SecureDatagrams,
				InputDatagrams:     kcp.InputDatagrams,
				InputACKCommands:   kcp.InputACKCommands,
				InputPushCommands:  kcp.InputPushCommands,
				OutputDatagrams:    kcp.OutputDatagrams,
				ReconciledMessages: kcp.ReconciledMessages,
				QueuedMessages:     kcp.QueuedMessages,
				InflightMessages:   kcp.InflightMessages,
				WaitingSegments:    kcp.WaitingSegments,
			},
			GatewayKCP: state.gatewayKCP,
		},
		Disposition: "failed",
	}, true
}
