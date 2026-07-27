package gateway

import (
	"errors"
	"fmt"
	"time"
)

const (
	// maximumQualificationClients 来自 B0.6 冻结的 8 actor hard cap。
	maximumQualificationClients = 8
	// maximumBattleDatagramBytes 来自 B0.5 IPv6/UDP 无分片上限。
	maximumBattleDatagramBytes = 1_200
	// percentageCeiling 是 manifest 百分比字段的闭区间上限。
	percentageCeiling = 100
)

var (
	// ErrInvalidConfig 表示 gateway 参数未完整绑定冻结 manifest。
	ErrInvalidConfig = errors.New("battle qualification gateway config is invalid")
	// ErrInvalidPacket 表示 caller 提供的 packet identity、时间或大小不合法。
	ErrInvalidPacket = errors.New("battle qualification gateway packet is invalid")
	// ErrClosed 表示 scheduler 或 gateway 已进入不可逆终态。
	ErrClosed = errors.New("battle qualification gateway is closed")
)

// Direction 是 fault scheduler 的 closed 网络方向。
type Direction uint8

const (
	// DirectionUplink 表示 qualification client 到 C++ backend。
	DirectionUplink Direction = 1
	// DirectionDownlink 表示 C++ backend 到 qualification client。
	DirectionDownlink Direction = 2
)

// Valid 报告 direction 是否属于 closed contract。
func (direction Direction) Valid() bool {
	return direction == DirectionUplink || direction == DirectionDownlink
}

// String 返回用于低敏 evidence 的稳定方向。
func (direction Direction) String() string {
	switch direction {
	case DirectionUplink:
		return "uplink"
	case DirectionDownlink:
		return "downlink"
	default:
		return "unknown"
	}
}

// Disposition 是每个 packet copy 的唯一终局裁决。
type Disposition uint8

const (
	// DispositionDelivered 表示 copy 已由 gateway 写入目标 socket。
	DispositionDelivered Disposition = 1
	// DispositionLoss 表示 manifest 的随机 loss 丢弃了 copy。
	DispositionLoss Disposition = 2
	// DispositionBurst 表示 manifest 的 deterministic burst 丢弃了 copy。
	DispositionBurst Disposition = 3
	// DispositionMTU 表示 datagram 超出 manifest MTU。
	DispositionMTU Disposition = 4
	// DispositionQueue 表示 global/per-client hard queue 无法接受全部 copy。
	DispositionQueue Disposition = 5
	// DispositionDeadline 表示 copy 在调度或实际投递前已到 deadline。
	DispositionDeadline Disposition = 6
	// DispositionPaused 表示对应方向处于显式 network pause。
	DispositionPaused Disposition = 7
	// DispositionMappingExpired 表示旧 NAT mapping 已越过迟到投递窗口。
	DispositionMappingExpired Disposition = 8
	// DispositionWriteFailed 表示目标 socket 写入失败。
	DispositionWriteFailed Disposition = 9
	// DispositionClosed 表示 gateway cleanup 终结了仍在队列中的 copy。
	DispositionClosed Disposition = 10
	// DispositionSourceRejected 表示 frontend 收到非 current client remote。
	DispositionSourceRejected Disposition = 11
)

// String 返回 report schema 可登记的稳定 disposition。
func (disposition Disposition) String() string {
	switch disposition {
	case DispositionDelivered:
		return "delivered"
	case DispositionLoss:
		return "loss"
	case DispositionBurst:
		return "burst"
	case DispositionMTU:
		return "mtu"
	case DispositionQueue:
		return "queue"
	case DispositionDeadline:
		return "deadline"
	case DispositionPaused:
		return "paused"
	case DispositionMappingExpired:
		return "mapping-expired"
	case DispositionWriteFailed:
		return "write-failed"
	case DispositionClosed:
		return "closed"
	case DispositionSourceRejected:
		return "source-rejected"
	default:
		return "unknown"
	}
}

// PublicPacketKind 是 gateway 可从未加密 header 读取的最小分类。
type PublicPacketKind uint8

const (
	// PublicPacketUnknown 表示 datagram 不具备已登记公开 header。
	PublicPacketUnknown PublicPacketKind = 0
	// PublicPacketHandshake 表示四类 fixed handshake datagram。
	PublicPacketHandshake PublicPacketKind = 1
	// PublicPacketRaw 表示 secure raw lane。
	PublicPacketRaw PublicPacketKind = 2
	// PublicPacketKCP 表示 secure KCP lane。
	PublicPacketKCP PublicPacketKind = 3
	// PublicPacketControl 表示 secure transport-control lane。
	PublicPacketControl PublicPacketKind = 4
)

// String 返回 evidence 使用的稳定公开 packet kind。
func (kind PublicPacketKind) String() string {
	switch kind {
	case PublicPacketHandshake:
		return "handshake"
	case PublicPacketRaw:
		return "raw"
	case PublicPacketKCP:
		return "kcp"
	case PublicPacketControl:
		return "control"
	default:
		return "unknown"
	}
}

// BandwidthPolicy 冻结一个方向的 token bucket；disabled 必须显式携带零值。
type BandwidthPolicy struct {
	// Enabled 指示当前 scenario 是否注入 bandwidth impairment。
	Enabled bool
	// BytesPerSecond 是整数 refill rate。
	BytesPerSecond uint64
	// BurstBytes 是 bucket 的 hard capacity 和初始 token 数。
	BurstBytes uint64
}

// Validate 拒绝隐式启用、零速率或超过单 datagram 的不可用 burst。
func (policy BandwidthPolicy) Validate(maximumDatagramBytes int) error {
	if !policy.Enabled {
		if policy.BytesPerSecond != 0 || policy.BurstBytes != 0 {
			return fmt.Errorf("%w: disabled bandwidth has values", ErrInvalidConfig)
		}
		return nil
	}
	if policy.BytesPerSecond == 0 || policy.BurstBytes < uint64(maximumDatagramBytes) {
		return fmt.Errorf("%w: bandwidth bounds", ErrInvalidConfig)
	}
	return nil
}

// DirectionPolicy 是单方向完整 fault 参数，不提供运行时默认值。
type DirectionPolicy struct {
	// BaseLatency 是每个 copy 的固定单向延迟。
	BaseLatency time.Duration
	// Jitter 是对称闭区间 [-Jitter,+Jitter] 的整数随机偏移。
	Jitter time.Duration
	// LossPercent 是 LCG roll 的丢包百分比。
	LossPercent uint8
	// DuplicatePercent 是生成第二份 copy 的百分比。
	DuplicatePercent uint8
	// ReorderPercent 是提前 ReorderAdvance 的百分比。
	ReorderPercent uint8
	// BurstInterval 是按 receive sequence 取模的周期；零表示关闭。
	BurstInterval uint64
	// BurstLength 是每个周期从 offset 0 开始的丢弃长度。
	BurstLength uint64
	// QueueItems 是该方向每客户端 queued copy hard cap。
	QueueItems int
	// Bandwidth 是该方向独立的 token bucket。
	Bandwidth BandwidthPolicy
}

// Validate 拒绝超界百分比、不完整 burst 和无界队列。
func (policy DirectionPolicy) Validate(maximumDatagramBytes int) error {
	if policy.BaseLatency < 0 || policy.Jitter < 0 ||
		policy.Jitter > time.Duration((int64(^uint64(0)>>1)-1)/2) ||
		policy.BaseLatency > time.Duration(int64(^uint64(0)>>1))-policy.Jitter ||
		policy.BaseLatency%time.Microsecond != 0 ||
		policy.Jitter%time.Microsecond != 0 ||
		policy.LossPercent > percentageCeiling ||
		policy.DuplicatePercent > percentageCeiling ||
		policy.ReorderPercent > percentageCeiling ||
		policy.QueueItems <= 0 ||
		policy.BurstLength > policy.BurstInterval ||
		(policy.BurstInterval == 0 && policy.BurstLength != 0) {
		return ErrInvalidConfig
	}
	return policy.Bandwidth.Validate(maximumDatagramBytes)
}

// Config 冻结一个 scenario 的 scheduler hard limits 与双向 policy。
type Config struct {
	// Seed 是 `lcg32-numerical-recipes` 的非零初始状态。
	Seed uint32
	// PatternDuration 是 B0.2 source scenario 的单次有限重放时长。
	PatternDuration time.Duration
	// BaselineGap 在每个 client 首个已送达 uplink KCP 后丢弃下一枚 downlink Raw。
	BaselineGap bool
	// MaximumClients 是本 run 允许的 client slot 上界。
	MaximumClients uint8
	// MaximumDatagramBytes 是 gateway 接受的 UDP payload ceiling。
	MaximumDatagramBytes int
	// GlobalQueueItems 是全部方向、客户端和 duplicate copy 的 hard cap。
	GlobalQueueItems int
	// PacketLifetime 是从 receive time 起计算的投递硬 deadline。
	PacketLifetime time.Duration
	// ReorderAdvance 是命中 reorder 时从单向延迟扣除的 manifest 参数。
	ReorderAdvance time.Duration
	// Uplink 是 client-to-backend 的独立 fault policy。
	Uplink DirectionPolicy
	// Downlink 是 backend-to-client 的独立 fault policy。
	Downlink DirectionPolicy
}

// Validate 在创建 PRNG、queue 或 socket 前完成全部边界校验。
func (config Config) Validate() error {
	if config.Seed == 0 ||
		config.PatternDuration <= 0 ||
		config.MaximumClients == 0 ||
		config.MaximumClients > maximumQualificationClients ||
		config.MaximumDatagramBytes <= 0 ||
		config.MaximumDatagramBytes > maximumBattleDatagramBytes ||
		config.GlobalQueueItems <= 0 ||
		config.PacketLifetime <= 0 ||
		config.ReorderAdvance < 0 {
		return ErrInvalidConfig
	}
	if config.ReorderAdvance%time.Microsecond != 0 {
		return ErrInvalidConfig
	}
	if err := config.Uplink.Validate(config.MaximumDatagramBytes); err != nil {
		return err
	}
	return config.Downlink.Validate(config.MaximumDatagramBytes)
}

// Policy 返回 closed direction 对应的 immutable 配置副本。
func (config Config) Policy(direction Direction) (DirectionPolicy, bool) {
	switch direction {
	case DirectionUplink:
		return config.Uplink, true
	case DirectionDownlink:
		return config.Downlink, true
	default:
		return DirectionPolicy{}, false
	}
}

// Packet 是进入 deterministic scheduler 的 owned opaque datagram。
type Packet struct {
	// Direction 是本次 socket traversal 的方向。
	Direction Direction
	// ClientSlot 是 run-local 1..MaximumClients correlation。
	ClientSlot uint8
	// MappingGeneration 是 gateway-owned NAT mapping generation。
	MappingGeneration uint32
	// ReceiveSequence 是单一 ingress owner 分配的全局正整数。
	ReceiveSequence uint64
	// ReceivedAt 是 run-local monotonic duration。
	ReceivedAt time.Duration
	// Payload 是 caller 转移给 scheduler 的 opaque bytes。
	Payload []byte
}

// Metadata 是不包含 payload、secret、IP 或绝对路径的 packet evidence。
type Metadata struct {
	// Direction 是本 copy 的网络方向。
	Direction Direction
	// ClientSlot 是 run-local client correlation。
	ClientSlot uint8
	// MappingGeneration 区分 NAT predecessor/successor。
	MappingGeneration uint32
	// ReceiveSequence 绑定原始 socket receive。
	ReceiveSequence uint64
	// CopyIndex 为零表示 original，为一表示 manifest duplicate。
	CopyIndex uint8
	// LengthBytes 是 opaque UDP payload 长度。
	LengthBytes int
	// PacketKind 只来自公开 magic/header。
	PacketKind PublicPacketKind
	// Correlation 是公开 session digest 的不可逆二次摘要；handshake/unknown 为空。
	Correlation string
	// ReceivedAt 是 run-local monotonic receive time。
	ReceivedAt time.Duration
	// DueAt 是 scheduler 的最终投递时间。
	DueAt time.Duration
	// DeliveredAt 只在 socket write 成功时记录实际单调投递时刻。
	DeliveredAt time.Duration
	// Disposition 是该 copy 的唯一终局裁决。
	Disposition Disposition
}

// Delivery 是 scheduler 到 socket owner 的 owned copy。
type Delivery struct {
	// Packet 保存方向、mapping 与 opaque payload。
	Packet Packet
	// CopyIndex 区分 original 与 duplicate。
	CopyIndex uint8
	// DueAt 是稳定排序后的投递时刻。
	DueAt time.Duration
}

// ScheduleResult 返回立即终结 evidence 与成功入队的 copy 数。
type ScheduleResult struct {
	// Finalized 是没有进入 queue 的 copy 终局 evidence。
	Finalized []Metadata
	// QueuedCopies 是原子进入 scheduler 的 copy 数。
	QueuedCopies int
}
