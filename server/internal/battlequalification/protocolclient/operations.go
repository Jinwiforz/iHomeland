package protocolclient

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/netip"
	"time"
)

const (
	// workloadCommandPayloadBytes 是 workload-command-v1 固定字段表宽度。
	workloadCommandPayloadBytes = 32
	// workloadEventPayloadBytes 是 workload-event-v1 固定字段表宽度。
	workloadEventPayloadBytes = 32
	// networkTransitionPayloadBytes 是 network-transition-v1 固定字段表宽度。
	networkTransitionPayloadBytes = 24
	// networkTransitionEventPayloadBytes 是 network-transition-event-v1 固定字段表宽度。
	networkTransitionEventPayloadBytes = 8
	// pollPayloadBytes 是 poll-v1 固定字段表宽度。
	pollPayloadBytes = 8
	// pollReceiptPayloadBytes 是 poll-receipt-v1 固定字段表宽度。
	pollReceiptPayloadBytes = 144
	// MaximumWorkloadRepeatCount 限制一次 supervisor command 产生的 UDP datagram 数。
	// 编排器必须按该 contract 分片，避免复制 stdio frame 的协议上限。
	MaximumWorkloadRepeatCount = 32
	// maximumPollEvents 限制一次 receipt 可消费的 authenticated datagram 数。
	maximumPollEvents = 32
	// maximumQualifiedActors 与冻结的单 instance actor capacity 一致。
	maximumQualifiedActors = 8
	// maximumBattleInputKind 与 BattleInputKind registry 的最后一个有效编号一致。
	maximumBattleInputKind = 7
	// committedTransitionFlag 是 transition receipt 唯一允许的成功标记。
	committedTransitionFlag = 1
	// maximumPollWait 限制 child 阻塞收取真实 UDP output 的时长。
	maximumPollWait = time.Second
)

// WorkloadOperation 是独立客户端允许发往真实 UDP socket 的闭合 workload。
type WorkloadOperation uint8

const (
	// WorkloadInputBundle 编码一个或多个相邻 input command。
	WorkloadInputBundle WorkloadOperation = 1
	// WorkloadProbe 编码不修改业务状态的网络观测。
	WorkloadProbe WorkloadOperation = 2
	// WorkloadResyncRequest 经 KCP 请求新的完整 baseline。
	WorkloadResyncRequest WorkloadOperation = 3
)

// DeliveryMutation 是资格客户端在真实 socket 上执行的闭合安全负例。
type DeliveryMutation uint8

const (
	// DeliveryUnmodified 发送一个正常 authenticated datagram。
	DeliveryUnmodified DeliveryMutation = 0
	// DeliveryExactReplay 对同一 sealed datagram 执行 byte-identical replay。
	DeliveryExactReplay DeliveryMutation = 1
	// DeliveryTamperedTag 先发送 tag 被修改的副本，再发送原始 datagram。
	DeliveryTamperedTag DeliveryMutation = 2
	// DeliveryOversizePrefix 先发送超过 MTU ceiling 的 datagram，再发送合法 datagram。
	DeliveryOversizePrefix DeliveryMutation = 3
	// DeliveryAADTampered 先发送结构有效但 AAD 被修改的 datagram，再发送合法 datagram。
	DeliveryAADTampered DeliveryMutation = 4
	// DeliveryCiphertextTampered 先发送 ciphertext 被修改的 datagram，再发送合法 datagram。
	DeliveryCiphertextTampered DeliveryMutation = 5
	// DeliveryFutureSequence 发送使用 current key 认证但越过 replay window 的 datagram。
	DeliveryFutureSequence DeliveryMutation = 6
	// DeliveryTooOldSequence 真实推进 replay window 后发送刚离开窗口的旧 datagram。
	DeliveryTooOldSequence DeliveryMutation = 7
	// DeliveryWrongDirection 使用 S2C key 伪装 C2S datagram。
	DeliveryWrongDirection DeliveryMutation = 8
	// DeliveryWrongLane 使用 authenticated KCP kind 承载 raw plaintext。
	DeliveryWrongLane DeliveryMutation = 9
	// DeliveryRebindHijack 从未认证 successor socket 发送 current endpoint datagram。
	DeliveryRebindHijack DeliveryMutation = 10
	// DeliveryKCPExpired 延迟已生成 KCP segment 直到超过 message expiry。
	DeliveryKCPExpired DeliveryMutation = 11
	// DeliveryMalformedPrefix 先发送固定短 malformed datagram，再发送合法 datagram。
	DeliveryMalformedPrefix DeliveryMutation = 12
)

// WorkloadCommand 是不含权威状态或玩家身份的 typed client intent。
type WorkloadCommand struct {
	// ClientSlot 必须命中已建立的 run-local session。
	ClientSlot uint8
	// Operation 决定 raw input、raw probe 或 KCP resync route。
	Operation WorkloadOperation
	// CommandKind 只在 input-bundle 中使用，对应冻结的 BattleInputKind。
	CommandKind uint8
	// RepeatCount 允许资格场景产生有界 burst、duplicate 与 backpressure。
	RepeatCount uint8
	// ApplicationSequence 是 session generation 内非零应用序列。
	ApplicationSequence uint64
	// ApplicationTick 是 input tick、已观测 server tick 或 resync latest tick。
	ApplicationTick uint64
	// ValueA 是 input 第一量化参数或 resync reason。
	ValueA int32
	// ValueB 是 input 第二量化参数；其他 operation 必须为零。
	ValueB int32
	// Delivery 只改变 UDP delivery，不改变应用 payload 或权威字段。
	Delivery DeliveryMutation
}

// WorkloadEventKind 是 child 从真实 UDP 路径观测到的闭合结果。
type WorkloadEventKind uint8

const (
	// WorkloadDatagramSent 表示 workload 已进入 connected UDP socket。
	WorkloadDatagramSent WorkloadEventKind = 1
	// WorkloadSnapshotReceived 表示通过 AEAD 与 raw baseline 校验的 snapshot。
	WorkloadSnapshotReceived WorkloadEventKind = 2
	// WorkloadReliableReceived 表示通过 AEAD、KCP 与 application route 校验的消息。
	WorkloadReliableReceived WorkloadEventKind = 3
	// WorkloadReliableQueued 表示 KCP application 已排队但本轮尚未产生 segment。
	WorkloadReliableQueued WorkloadEventKind = 4
)

// WorkloadEvent 是不暴露 wire payload 的真实网络低敏投影。
type WorkloadEvent struct {
	// ClientSlot 是 run-local session correlation。
	ClientSlot uint8
	// Kind 区分发送、snapshot 与 reliable receive。
	Kind WorkloadEventKind
	// Operation 回显触发发送的 workload operation；异步 receive 为零。
	Operation WorkloadOperation
	// Count 是本事件代表的 datagram、partition 或 message 数。
	Count uint8
	// PacketSequence 是已认证 packet sequence；仅发送事件可为零。
	PacketSequence uint64
	// ApplicationSequence 是 raw/KCP application sequence。
	ApplicationSequence uint64
	// ApplicationTick 是 input/server tick。
	ApplicationTick uint64
}

// NetworkTransitionOperation 是 active session 的闭合网络状态迁移。
type NetworkTransitionOperation uint8

const (
	// NetworkRebind 在同一 server endpoint 上切换 client UDP socket。
	NetworkRebind NetworkTransitionOperation = 1
	// NetworkRekey 原子推进双方 traffic key epoch。
	NetworkRekey NetworkTransitionOperation = 2
	// NetworkClose 完成 authenticated close 后终结 session。
	NetworkClose NetworkTransitionOperation = 3
	// NetworkOldEpochProbe 完成 rekey，越过 overlap 后发送 predecessor epoch control。
	NetworkOldEpochProbe NetworkTransitionOperation = 4
)

// NetworkTransition 是一次真实 control lane 状态迁移。
type NetworkTransition struct {
	// ClientSlot 必须命中已建立的 run-local session。
	ClientSlot uint8
	// Operation 是 rebind、rekey 或 close。
	Operation NetworkTransitionOperation
	// AdvertisedEndpoint 只在 rebind 时携带 server-facing NAT successor。
	AdvertisedEndpoint netip.AddrPort
}

// NetworkTransitionEvent 是不含 endpoint、nonce 或 key 的迁移结果。
type NetworkTransitionEvent struct {
	// ClientSlot 是 run-local session correlation。
	ClientSlot uint8
	// Operation 回显已完成的迁移。
	Operation NetworkTransitionOperation
	// Committed 表示双方 acknowledgement 与本地 commit 均成功。
	Committed bool
	// Generation 是 rebind endpoint generation 或 rekey key epoch；close 为零。
	Generation uint32
}

// NetworkTransitionFailureCode 是独立客户端可安全回传的迁移失败分类。
type NetworkTransitionFailureCode uint8

const (
	// TransitionFailureInvalidRequest 表示迁移请求违反 closed contract。
	TransitionFailureInvalidRequest NetworkTransitionFailureCode = 1
	// TransitionFailureRebindRequest 表示 rebind request 未进入真实 UDP socket。
	TransitionFailureRebindRequest NetworkTransitionFailureCode = 2
	// TransitionFailureRebindChallengeReceive 表示未收到可认证的 rebind challenge。
	TransitionFailureRebindChallengeReceive NetworkTransitionFailureCode = 3
	// TransitionFailureRebindChallengeValidate 表示 challenge kind、长度或 generation 漂移。
	TransitionFailureRebindChallengeValidate NetworkTransitionFailureCode = 4
	// TransitionFailureRebindConfirm 表示 rebind confirm 未进入 successor mapping。
	TransitionFailureRebindConfirm NetworkTransitionFailureCode = 5
	// TransitionFailureRebindCommitReceive 表示未收到可认证的 rebind commit。
	TransitionFailureRebindCommitReceive NetworkTransitionFailureCode = 6
	// TransitionFailureRebindCommitValidate 表示 commit 内容或本地 generation 提交失败。
	TransitionFailureRebindCommitValidate NetworkTransitionFailureCode = 7
	// TransitionFailureOtherOperation 表示非 rebind 迁移的未分类失败。
	TransitionFailureOtherOperation NetworkTransitionFailureCode = 8
	// TransitionFailureRebindApplicationPacket 表示 rebind 间交错 application 包验证失败。
	TransitionFailureRebindApplicationPacket NetworkTransitionFailureCode = 9
	// TransitionFailureRebindRuntime 表示 rebind 未归类的固定 runtime contract 失败。
	TransitionFailureRebindRuntime NetworkTransitionFailureCode = 10
	// TransitionFailureRebindStandard 表示 rebind 的标准库边界失败。
	TransitionFailureRebindStandard NetworkTransitionFailureCode = 11
	// TransitionFailureRebindUnknown 表示 rebind 的非标准异常边界失败。
	TransitionFailureRebindUnknown NetworkTransitionFailureCode = 12
	// TransitionFailureRebindChallengeAuthentication 表示 challenge 未通过 AEAD 或 replay 验证。
	TransitionFailureRebindChallengeAuthentication NetworkTransitionFailureCode = 13
	// TransitionFailureRebindChallengeApplication 表示等待 challenge 时交错 application 包验证失败。
	TransitionFailureRebindChallengeApplication NetworkTransitionFailureCode = 14
	// TransitionFailureRebindChallengeInterleave 表示 challenge 前交错包超过闭合上限。
	TransitionFailureRebindChallengeInterleave NetworkTransitionFailureCode = 15
	// TransitionFailureRebindCommitAuthentication 表示 commit 未通过 AEAD 或 replay 验证。
	TransitionFailureRebindCommitAuthentication NetworkTransitionFailureCode = 16
	// TransitionFailureRebindCommitApplication 表示等待 commit 时交错 application 包验证失败。
	TransitionFailureRebindCommitApplication NetworkTransitionFailureCode = 17
	// TransitionFailureRebindCommitInterleave 表示 commit 前交错包超过闭合上限。
	TransitionFailureRebindCommitInterleave NetworkTransitionFailureCode = 18
	// TransitionFailureRekeyPrepare 表示 rekey 前置 epoch 或 old-epoch fixture 无法建立。
	TransitionFailureRekeyPrepare NetworkTransitionFailureCode = 19
	// TransitionFailureRekeyProposal 表示 rekey proposal 未进入真实 UDP socket。
	TransitionFailureRekeyProposal NetworkTransitionFailureCode = 20
	// TransitionFailureRekeyCommitReceive 表示未收到可认证的 rekey commit。
	TransitionFailureRekeyCommitReceive NetworkTransitionFailureCode = 21
	// TransitionFailureRekeyCommitAuthentication 表示 rekey commit 未通过 AEAD 或 replay 验证。
	TransitionFailureRekeyCommitAuthentication NetworkTransitionFailureCode = 22
	// TransitionFailureRekeyCommitApplication 表示等待 rekey commit 时交错 application 包验证失败。
	TransitionFailureRekeyCommitApplication NetworkTransitionFailureCode = 23
	// TransitionFailureRekeyCommitInterleave 表示 rekey commit 前交错包超过闭合上限。
	TransitionFailureRekeyCommitInterleave NetworkTransitionFailureCode = 24
	// TransitionFailureRekeyCommitValidate 表示 rekey commit 内容或本地 epoch 提交失败。
	TransitionFailureRekeyCommitValidate NetworkTransitionFailureCode = 25
	// TransitionFailureOldEpochSend 表示 overlap 到期后的 predecessor probe 未发出。
	TransitionFailureOldEpochSend NetworkTransitionFailureCode = 26
	// TransitionFailureCloseRequest 表示 authenticated close request 未发出。
	TransitionFailureCloseRequest NetworkTransitionFailureCode = 27
	// TransitionFailureCloseAckReceive 表示未收到可认证的 close acknowledgement。
	TransitionFailureCloseAckReceive NetworkTransitionFailureCode = 28
	// TransitionFailureCloseAckAuthentication 表示 close acknowledgement 未通过 AEAD 或 replay 验证。
	TransitionFailureCloseAckAuthentication NetworkTransitionFailureCode = 29
	// TransitionFailureCloseAckApplication 表示等待 close acknowledgement 时交错 application 包验证失败。
	TransitionFailureCloseAckApplication NetworkTransitionFailureCode = 30
	// TransitionFailureCloseAckInterleave 表示 close acknowledgement 前交错包超过闭合上限。
	TransitionFailureCloseAckInterleave NetworkTransitionFailureCode = 31
	// TransitionFailureCloseAckValidate 表示 close acknowledgement 内容或本地关闭失败。
	TransitionFailureCloseAckValidate NetworkTransitionFailureCode = 32
	// TransitionFailureRekeyUnhandled 表示 rekey 未进入任一已登记阶段。
	TransitionFailureRekeyUnhandled NetworkTransitionFailureCode = 33
	// TransitionFailureCloseUnhandled 表示 close 未进入任一已登记阶段。
	TransitionFailureCloseUnhandled NetworkTransitionFailureCode = 34
	// TransitionFailureOldEpochUnhandled 表示 old-epoch probe 未进入任一已登记阶段。
	TransitionFailureOldEpochUnhandled NetworkTransitionFailureCode = 35
)

// NetworkTransitionFailure 保留封闭分类，不携带 endpoint、payload 或底层异常文本。
type NetworkTransitionFailure struct {
	// Operation 是失败 event 回显的 closed transition operation。
	Operation NetworkTransitionOperation
	// Code 是 network-transition-event-v1 登记的非零失败枚举。
	Code NetworkTransitionFailureCode
}

// Error 返回固定低敏文本。
func (failure NetworkTransitionFailure) Error() string {
	return "battle protocol client transition failed"
}

// QualificationFailureCode 返回编排器允许写入日志的稳定分类。
func (failure NetworkTransitionFailure) QualificationFailureCode() string {
	switch failure.Code {
	case TransitionFailureInvalidRequest:
		return "client-transition-invalid-request"
	case TransitionFailureRebindRequest:
		return "client-transition-rebind-request"
	case TransitionFailureRebindChallengeReceive:
		return "client-transition-rebind-challenge-receive"
	case TransitionFailureRebindChallengeValidate:
		return "client-transition-rebind-challenge-validate"
	case TransitionFailureRebindConfirm:
		return "client-transition-rebind-confirm"
	case TransitionFailureRebindCommitReceive:
		return "client-transition-rebind-commit-receive"
	case TransitionFailureRebindCommitValidate:
		return "client-transition-rebind-commit-validate"
	case TransitionFailureOtherOperation:
		return "client-transition-other-operation"
	case TransitionFailureRebindApplicationPacket:
		return "client-transition-rebind-application-packet"
	case TransitionFailureRebindRuntime:
		return "client-transition-rebind-runtime"
	case TransitionFailureRebindStandard:
		return "client-transition-rebind-standard"
	case TransitionFailureRebindUnknown:
		return "client-transition-rebind-unknown"
	case TransitionFailureRebindChallengeAuthentication:
		return "client-transition-rebind-challenge-authentication"
	case TransitionFailureRebindChallengeApplication:
		return "client-transition-rebind-challenge-application"
	case TransitionFailureRebindChallengeInterleave:
		return "client-transition-rebind-challenge-interleave"
	case TransitionFailureRebindCommitAuthentication:
		return "client-transition-rebind-commit-authentication"
	case TransitionFailureRebindCommitApplication:
		return "client-transition-rebind-commit-application"
	case TransitionFailureRebindCommitInterleave:
		return "client-transition-rebind-commit-interleave"
	case TransitionFailureRekeyPrepare:
		return "client-transition-rekey-prepare"
	case TransitionFailureRekeyProposal:
		return "client-transition-rekey-proposal"
	case TransitionFailureRekeyCommitReceive:
		return "client-transition-rekey-commit-receive"
	case TransitionFailureRekeyCommitAuthentication:
		return "client-transition-rekey-commit-authentication"
	case TransitionFailureRekeyCommitApplication:
		return "client-transition-rekey-commit-application"
	case TransitionFailureRekeyCommitInterleave:
		return "client-transition-rekey-commit-interleave"
	case TransitionFailureRekeyCommitValidate:
		return "client-transition-rekey-commit-validate"
	case TransitionFailureOldEpochSend:
		return "client-transition-old-epoch-send"
	case TransitionFailureCloseRequest:
		return "client-transition-close-request"
	case TransitionFailureCloseAckReceive:
		return "client-transition-close-ack-receive"
	case TransitionFailureCloseAckAuthentication:
		return "client-transition-close-ack-authentication"
	case TransitionFailureCloseAckApplication:
		return "client-transition-close-ack-application"
	case TransitionFailureCloseAckInterleave:
		return "client-transition-close-ack-interleave"
	case TransitionFailureCloseAckValidate:
		return "client-transition-close-ack-validate"
	case TransitionFailureRekeyUnhandled:
		return "client-transition-rekey-unhandled"
	case TransitionFailureCloseUnhandled:
		return "client-transition-close-unhandled"
	case TransitionFailureOldEpochUnhandled:
		return "client-transition-old-epoch-unhandled"
	default:
		return "client-transition-unknown"
	}
}

// Poll 是一次有界真实 UDP receive 请求。
type Poll struct {
	// ClientSlot 必须命中已建立的 run-local session。
	ClientSlot uint8
	// MaximumEvents 限制本次处理的 authenticated datagram 数。
	MaximumEvents uint8
	// Wait 是等待首个 datagram 的最长时间。
	Wait time.Duration
}

// PollReceipt 汇总 child 已独立认证并解析的低敏状态。
type PollReceipt struct {
	// ClientSlot 是 run-local session correlation。
	ClientSlot uint8
	// EventCount 是本轮实际处理的 datagram 数。
	EventCount uint8
	// SnapshotCount 是累计接受的完整 snapshot 数。
	SnapshotCount uint64
	// ReliableCount 是累计接受的完整 KCP application message 数。
	ReliableCount uint64
	// LatestServerTick 是 raw/KCP payload 已验证的最新权威 tick。
	LatestServerTick uint64
	// LatestApplicationSequence 是最近接受的 raw/KCP application sequence。
	LatestApplicationSequence uint64
	// LatestSnapshotSequence 是 raw baseline validator 最近接受的 snapshot sequence。
	LatestSnapshotSequence uint64
	// BaselineID 是最近完整建立且仍可被 delta 引用的 baseline。
	BaselineID uint64
	// LastProcessedInputTick 是完整 snapshot 显式发布的连续输入确认。
	LastProcessedInputTick uint64
	// BaselineGapCount 是 raw decoder 观测到未知 baseline 的累计次数。
	BaselineGapCount uint64
	// KCP 是 client session 的低敏 ACK、secure delivery 与 reconciliation 状态。
	KCP ClientKCPState
}

// ClientKCPCloseReason 是独立客户端 KCP lane 的不可逆终态分类。
type ClientKCPCloseReason uint8

const (
	// ClientKCPActive 表示 lane 尚未进入终态。
	ClientKCPActive ClientKCPCloseReason = 0
	// ClientKCPClockClosed 表示 absolute/relative clock contract 失败。
	ClientKCPClockClosed ClientKCPCloseReason = 1
	// ClientKCPOutputClosed 表示 primitive output 越过边界或复制失败。
	ClientKCPOutputClosed ClientKCPCloseReason = 2
	// ClientKCPPrimitiveClosed 表示 KCP send/input/recv 返回错误。
	ClientKCPPrimitiveClosed ClientKCPCloseReason = 3
	// ClientKCPInflightExpired 表示未完成 ACK reconciliation 的 message 到期。
	ClientKCPInflightExpired ClientKCPCloseReason = 4
	// ClientKCPProtocolClosed 表示 KCP segment 或 application route 违反 closed contract。
	ClientKCPProtocolClosed ClientKCPCloseReason = 5
)

// ClientKCPState 是 poll receipt 中不含 payload、endpoint 或 sequence identity 的状态。
type ClientKCPState struct {
	// CloseReason 是 client KCP lane 当前稳定终态。
	CloseReason ClientKCPCloseReason
	// SecureDatagrams 是通过 AEAD/replay 后进入 KCP lane 的累计 datagram 数。
	SecureDatagrams uint64
	// InputDatagrams 是通过 KCP header gate 后交给 primitive 的累计 datagram 数。
	InputDatagrams uint64
	// InputACKCommands 是有效入站 datagram 中的累计 ACK command 数。
	InputACKCommands uint64
	// InputPushCommands 是有效入站 datagram 中的累计 PUSH command 数。
	InputPushCommands uint64
	// OutputDatagrams 是 primitive 交给 secure KCP lane 的累计 datagram 数。
	OutputDatagrams uint64
	// ReconciledMessages 是因 peer ACK 从 inflight 集合回收的累计 message 数。
	ReconciledMessages uint64
	// QueuedMessages 是尚未提交给 KCP primitive 的当前 message 数。
	QueuedMessages uint64
	// InflightMessages 是已提交但尚未完成 ACK reconciliation 的当前 message 数。
	InflightMessages uint64
	// WaitingSegments 是 KCP send queue 与 send buffer 的当前 segment 数。
	WaitingSegments uint64
}

// PollFailureCode 是独立客户端可安全回传的 poll 失败分类。
type PollFailureCode uint8

const (
	// PollFailureInvalidRequest 表示 stdio poll request 违反 closed contract。
	PollFailureInvalidRequest PollFailureCode = 1
	// PollFailureAuthentication 表示 UDP datagram 未通过 secure channel 验证。
	PollFailureAuthentication PollFailureCode = 2
	// PollFailureSnapshot 表示 raw snapshot/baseline 校验失败。
	PollFailureSnapshot PollFailureCode = 3
	// PollFailureKCP 表示 KCP segment 未通过独立 adapter 校验。
	PollFailureKCP PollFailureCode = 4
	// PollFailureUnexpectedLane 表示 poll 收到不属于 raw/KCP 的 control datagram。
	PollFailureUnexpectedLane PollFailureCode = 5
	// PollFailureTransport 表示 socket 或未分类的 client receive 边界失败。
	PollFailureTransport PollFailureCode = 6
	// PollFailureKCPInflightExpiry 表示尚未被 peer ACK 的发送消息到达 route deadline。
	PollFailureKCPInflightExpiry PollFailureCode = 7
)

// PollFailure 保留封闭分类与同一 client session 的低敏 KCP 状态。
type PollFailure struct {
	// Code 是 poll-receipt-v1 登记的非零失败枚举。
	Code PollFailureCode
	// Receipt 是失败边界原子投影的累计 application 与 KCP evidence。
	Receipt PollReceipt
}

// Error 返回固定低敏文本。
func (failure PollFailure) Error() string {
	return "battle protocol client poll failed"
}

// QualificationFailureCode 返回编排器允许写入日志的稳定分类。
func (failure PollFailure) QualificationFailureCode() string {
	switch failure.Code {
	case PollFailureInvalidRequest:
		return "client-poll-invalid-request"
	case PollFailureAuthentication:
		return "client-poll-authentication"
	case PollFailureSnapshot:
		return "client-poll-snapshot"
	case PollFailureKCP:
		return "client-poll-kcp"
	case PollFailureUnexpectedLane:
		return "client-poll-unexpected-lane"
	case PollFailureTransport:
		return "client-poll-transport"
	case PollFailureKCPInflightExpiry:
		return "client-poll-kcp-inflight-expiry"
	default:
		return "client-poll-unknown"
	}
}

// SendWorkload 编码 typed command 并要求 child 返回 exact workload event。
func (supervisor *Supervisor) SendWorkload(
	ctx context.Context,
	command WorkloadCommand,
) (WorkloadEvent, error) {
	payload, err := encodeWorkloadCommand(command)
	if err != nil {
		return WorkloadEvent{}, err
	}
	receipt, err := supervisor.Exchange(ctx, KindWorkloadCommandRequest, payload)
	if err != nil {
		return WorkloadEvent{}, err
	}
	defer clear(receipt.Payload)
	if receipt.Kind != KindWorkloadEvent {
		supervisor.abort()
		return WorkloadEvent{}, ErrUnexpectedReceipt
	}
	event, err := decodeWorkloadEvent(receipt.Payload)
	if err != nil {
		supervisor.abort()
	}
	return event, err
}

// Transition 编码 typed control 请求并要求 child 返回 exact committed event。
func (supervisor *Supervisor) Transition(
	ctx context.Context,
	transition NetworkTransition,
) (NetworkTransitionEvent, error) {
	payload, err := encodeNetworkTransition(transition)
	if err != nil {
		return NetworkTransitionEvent{}, err
	}
	receipt, err := supervisor.Exchange(ctx, KindNetworkTransitionRequest, payload)
	if err != nil {
		return NetworkTransitionEvent{}, err
	}
	defer clear(receipt.Payload)
	if receipt.Kind != KindNetworkTransitionEvent {
		supervisor.abort()
		return NetworkTransitionEvent{}, ErrUnexpectedReceipt
	}
	event, err := decodeNetworkTransitionEvent(receipt.Payload)
	if err != nil {
		supervisor.abort()
	}
	return event, err
}

// PollSession 等待并汇总真实 UDP output，不返回 endpoint 或 wire payload。
func (supervisor *Supervisor) PollSession(
	ctx context.Context,
	request Poll,
) (PollReceipt, error) {
	payload, err := encodePoll(request)
	if err != nil {
		return PollReceipt{}, err
	}
	receipt, err := supervisor.Exchange(ctx, KindPollRequest, payload)
	if err != nil {
		return PollReceipt{}, err
	}
	defer clear(receipt.Payload)
	if receipt.Kind != KindPollReceipt {
		supervisor.abort()
		return PollReceipt{}, ErrUnexpectedReceipt
	}
	decoded, err := decodePollReceipt(receipt.Payload)
	if err != nil {
		supervisor.abort()
	}
	return decoded, err
}

// encodeWorkloadCommand 编码 closed 32-byte workload field table。
func encodeWorkloadCommand(command WorkloadCommand) ([]byte, error) {
	if command.ClientSlot == 0 || command.ClientSlot > maximumQualifiedActors ||
		command.Operation < WorkloadInputBundle ||
		command.Operation > WorkloadResyncRequest ||
		command.RepeatCount == 0 ||
		command.RepeatCount > MaximumWorkloadRepeatCount ||
		command.ApplicationSequence == 0 ||
		command.ApplicationTick == 0 ||
		command.Delivery > DeliveryMalformedPrefix ||
		(command.Operation == WorkloadResyncRequest &&
			command.Delivery != DeliveryUnmodified &&
			command.Delivery != DeliveryKCPExpired) ||
		(command.Operation != WorkloadResyncRequest &&
			command.Delivery == DeliveryKCPExpired) {
		return nil, ErrInvalidFrame
	}
	if command.Operation == WorkloadInputBundle {
		if command.CommandKind == 0 || command.CommandKind > maximumBattleInputKind {
			return nil, ErrInvalidFrame
		}
	} else if command.CommandKind != 0 || command.ValueB != 0 {
		return nil, ErrInvalidFrame
	}
	payload := make([]byte, workloadCommandPayloadBytes)
	payload[0] = command.ClientSlot
	payload[1] = byte(command.Operation)
	payload[2] = command.CommandKind
	payload[3] = command.RepeatCount
	binary.BigEndian.PutUint64(payload[4:12], command.ApplicationSequence)
	binary.BigEndian.PutUint64(payload[12:20], command.ApplicationTick)
	binary.BigEndian.PutUint32(payload[20:24], uint32(command.ValueA))
	binary.BigEndian.PutUint32(payload[24:28], uint32(command.ValueB))
	payload[28] = byte(command.Delivery)
	return payload, nil
}

// decodeWorkloadEvent 解码 closed 32-byte low-sensitive receipt。
func decodeWorkloadEvent(payload []byte) (WorkloadEvent, error) {
	if len(payload) != workloadEventPayloadBytes {
		return WorkloadEvent{}, ErrUnexpectedReceipt
	}
	kind := WorkloadEventKind(payload[1])
	operation := WorkloadOperation(payload[2])
	if payload[0] == 0 || payload[0] > maximumQualifiedActors ||
		kind < WorkloadDatagramSent ||
		kind > WorkloadReliableQueued ||
		(kind == WorkloadReliableQueued &&
			(operation != WorkloadResyncRequest || payload[3] != 0)) ||
		(kind != WorkloadReliableQueued && payload[3] == 0) ||
		binary.BigEndian.Uint32(payload[28:32]) != 0 {
		return WorkloadEvent{}, ErrUnexpectedReceipt
	}
	return WorkloadEvent{
		ClientSlot:          payload[0],
		Kind:                kind,
		Operation:           operation,
		Count:               payload[3],
		PacketSequence:      binary.BigEndian.Uint64(payload[4:12]),
		ApplicationSequence: binary.BigEndian.Uint64(payload[12:20]),
		ApplicationTick:     binary.BigEndian.Uint64(payload[20:28]),
	}, nil
}

// encodeNetworkTransition 编码 closed 8-byte control request。
func encodeNetworkTransition(transition NetworkTransition) ([]byte, error) {
	if transition.ClientSlot == 0 ||
		transition.ClientSlot > maximumQualifiedActors ||
		transition.Operation < NetworkRebind ||
		transition.Operation > NetworkOldEpochProbe {
		return nil, ErrInvalidFrame
	}
	if transition.Operation == NetworkRebind {
		if !transition.AdvertisedEndpoint.IsValid() ||
			transition.AdvertisedEndpoint.Addr().IsUnspecified() ||
			transition.AdvertisedEndpoint.Port() == 0 {
			return nil, ErrInvalidFrame
		}
	} else if transition.AdvertisedEndpoint.IsValid() {
		return nil, ErrInvalidFrame
	}
	payload := make([]byte, networkTransitionPayloadBytes)
	payload[0] = transition.ClientSlot
	payload[1] = byte(transition.Operation)
	if transition.Operation == NetworkRebind {
		if transition.AdvertisedEndpoint.Addr().Is4() {
			payload[2] = addressFamilyIPv4
		} else {
			payload[2] = addressFamilyIPv6
		}
		binary.BigEndian.PutUint16(payload[4:6], transition.AdvertisedEndpoint.Port())
		address := transition.AdvertisedEndpoint.Addr().As16()
		copy(payload[8:24], address[:])
	}
	return payload, nil
}

// transitionFailureMatchesOperation 拒绝把一个阶段码伪装成另一类 operation。
func transitionFailureMatchesOperation(
	operation NetworkTransitionOperation,
	code NetworkTransitionFailureCode,
) bool {
	if code == TransitionFailureInvalidRequest {
		return true
	}
	switch operation {
	case NetworkRebind:
		return code >= TransitionFailureRebindRequest &&
			code <= TransitionFailureRebindCommitValidate ||
			code >= TransitionFailureRebindApplicationPacket &&
				code <= TransitionFailureRebindCommitInterleave
	case NetworkRekey:
		return code >= TransitionFailureRekeyPrepare &&
			code <= TransitionFailureRekeyCommitValidate ||
			code == TransitionFailureRekeyUnhandled ||
			code == TransitionFailureOtherOperation
	case NetworkClose:
		return code >= TransitionFailureCloseRequest &&
			code <= TransitionFailureCloseAckValidate ||
			code == TransitionFailureCloseUnhandled ||
			code == TransitionFailureOtherOperation
	case NetworkOldEpochProbe:
		return code >= TransitionFailureRekeyPrepare &&
			code <= TransitionFailureOldEpochSend ||
			code == TransitionFailureOldEpochUnhandled ||
			code == TransitionFailureOtherOperation
	default:
		return false
	}
}

// decodeNetworkTransitionEvent 解码 closed 8-byte committed result。
func decodeNetworkTransitionEvent(payload []byte) (NetworkTransitionEvent, error) {
	if len(payload) != networkTransitionEventPayloadBytes ||
		payload[0] == 0 || payload[0] > maximumQualifiedActors ||
		payload[1] < byte(NetworkRebind) ||
		payload[1] > byte(NetworkOldEpochProbe) {
		return NetworkTransitionEvent{}, ErrUnexpectedReceipt
	}
	generation := binary.BigEndian.Uint32(payload[4:8])
	if payload[2] == 0 {
		code := NetworkTransitionFailureCode(payload[3])
		operation := NetworkTransitionOperation(payload[1])
		if code < TransitionFailureInvalidRequest ||
			code > TransitionFailureOldEpochUnhandled ||
			!transitionFailureMatchesOperation(operation, code) ||
			generation != 0 {
			return NetworkTransitionEvent{}, ErrUnexpectedReceipt
		}
		return NetworkTransitionEvent{}, NetworkTransitionFailure{
			Operation: operation,
			Code:      code,
		}
	}
	if payload[2] != committedTransitionFlag || payload[3] != 0 {
		return NetworkTransitionEvent{}, ErrUnexpectedReceipt
	}
	if (NetworkTransitionOperation(payload[1]) == NetworkClose && generation != 0) ||
		(NetworkTransitionOperation(payload[1]) != NetworkClose && generation == 0) {
		return NetworkTransitionEvent{}, ErrUnexpectedReceipt
	}
	return NetworkTransitionEvent{
		ClientSlot: payload[0],
		Operation:  NetworkTransitionOperation(payload[1]),
		Committed:  true,
		Generation: generation,
	}, nil
}

// encodePoll 编码 closed 8-byte bounded receive request。
func encodePoll(request Poll) ([]byte, error) {
	if request.ClientSlot == 0 ||
		request.ClientSlot > maximumQualifiedActors ||
		request.MaximumEvents == 0 ||
		request.MaximumEvents > maximumPollEvents ||
		request.Wait <= 0 || request.Wait > maximumPollWait ||
		request.Wait%time.Millisecond != 0 {
		return nil, ErrInvalidFrame
	}
	payload := make([]byte, pollPayloadBytes)
	payload[0] = request.ClientSlot
	payload[1] = request.MaximumEvents
	binary.BigEndian.PutUint32(payload[4:8], uint32(request.Wait/time.Millisecond))
	return payload, nil
}

// decodePollReceipt 解码 closed 144-byte cumulative observation。
func decodePollReceipt(payload []byte) (PollReceipt, error) {
	if len(payload) != pollReceiptPayloadBytes ||
		payload[0] == 0 || payload[0] > maximumQualifiedActors ||
		payload[3] > byte(ClientKCPProtocolClosed) ||
		!zeroBytes(payload[4:8]) {
		return PollReceipt{}, fmt.Errorf("%w: poll receipt", ErrUnexpectedReceipt)
	}
	decoded := PollReceipt{
		ClientSlot:                payload[0],
		EventCount:                payload[1],
		SnapshotCount:             binary.BigEndian.Uint64(payload[8:16]),
		ReliableCount:             binary.BigEndian.Uint64(payload[16:24]),
		LatestServerTick:          binary.BigEndian.Uint64(payload[24:32]),
		LatestApplicationSequence: binary.BigEndian.Uint64(payload[32:40]),
		LatestSnapshotSequence:    binary.BigEndian.Uint64(payload[40:48]),
		BaselineID:                binary.BigEndian.Uint64(payload[48:56]),
		LastProcessedInputTick:    binary.BigEndian.Uint64(payload[56:64]),
		BaselineGapCount:          binary.BigEndian.Uint64(payload[64:72]),
		KCP: ClientKCPState{
			CloseReason:       ClientKCPCloseReason(payload[3]),
			SecureDatagrams:   binary.BigEndian.Uint64(payload[72:80]),
			InputDatagrams:    binary.BigEndian.Uint64(payload[80:88]),
			InputACKCommands:  binary.BigEndian.Uint64(payload[88:96]),
			InputPushCommands: binary.BigEndian.Uint64(payload[96:104]),
			OutputDatagrams:   binary.BigEndian.Uint64(payload[104:112]),
			ReconciledMessages: binary.BigEndian.Uint64(
				payload[112:120],
			),
			QueuedMessages:   binary.BigEndian.Uint64(payload[120:128]),
			InflightMessages: binary.BigEndian.Uint64(payload[128:136]),
			WaitingSegments:  binary.BigEndian.Uint64(payload[136:144]),
		},
	}
	if decoded.KCP.QueuedMessages > 64 ||
		decoded.KCP.InflightMessages > 64 ||
		decoded.KCP.WaitingSegments > 64 ||
		decoded.KCP.InputDatagrams > decoded.KCP.SecureDatagrams {
		return PollReceipt{}, fmt.Errorf("%w: poll KCP state", ErrUnexpectedReceipt)
	}
	failureCode := PollFailureCode(payload[2])
	if failureCode != 0 {
		if failureCode > PollFailureKCPInflightExpiry ||
			payload[1] != 0 ||
			(failureCode == PollFailureKCPInflightExpiry &&
				decoded.KCP.CloseReason != ClientKCPInflightExpired) {
			return PollReceipt{}, fmt.Errorf("%w: poll failure receipt", ErrUnexpectedReceipt)
		}
		return PollReceipt{}, PollFailure{
			Code:    failureCode,
			Receipt: decoded,
		}
	}
	if decoded.KCP.CloseReason != ClientKCPActive {
		return PollReceipt{}, fmt.Errorf("%w: successful poll KCP state", ErrUnexpectedReceipt)
	}
	return decoded, nil
}

// zeroBytes 验证 failure receipt 未夹带未登记诊断或运行态字段。
func zeroBytes(value []byte) bool {
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}
