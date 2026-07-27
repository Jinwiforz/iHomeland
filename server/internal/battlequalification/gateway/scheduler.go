package gateway

import (
	"container/heap"
	"fmt"
	"math"
	"math/bits"
	"sync"
	"time"
)

const (
	// lcgMultiplier 与 B0.2 `lcg32-numerical-recipes` 完全一致。
	lcgMultiplier uint32 = 1_664_525
	// lcgIncrement 与 B0.2 `lcg32-numerical-recipes` 完全一致。
	lcgIncrement uint32 = 1_013_904_223
	// duplicateCopies 固定 original 加一份 duplicate。
	duplicateCopies = 2
)

// lcg32 是与 B0.2 simulator byte-for-byte 对齐的 uint32 wrap PRNG。
type lcg32 struct {
	// state 是下一次 transition 的当前 32-bit 状态。
	state uint32
}

// next 依赖 Go unsigned overflow 获得模 2^32 的规范化结果。
func (generator *lcg32) next() uint32 {
	generator.state = lcgMultiplier*generator.state + lcgIncrement
	return generator.state
}

// tokenBucket 是 direction-owned 的整数带宽时间轴。
type tokenBucket struct {
	// policy 冻结 refill 与 capacity。
	policy BandwidthPolicy
	// tokens 是 lastAt 时刻可消费的整数 bytes。
	tokens uint64
	// lastAt 是最近 refill/reservation 的单调时间。
	lastAt time.Duration
	// initialized 区分合法的零时间与尚未建立时间轴。
	initialized bool
}

// reserve 返回满足 token budget 的最早 due time。
func (bucket *tokenBucket) reserve(dueAt time.Duration, bytes uint64) time.Duration {
	if !bucket.policy.Enabled {
		return dueAt
	}
	if !bucket.initialized {
		bucket.tokens = bucket.policy.BurstBytes
		bucket.lastAt = dueAt
		bucket.initialized = true
	}
	if dueAt > bucket.lastAt {
		elapsed := dueAt - bucket.lastAt
		refill := scaledDuration(elapsed, bucket.policy.BytesPerSecond)
		if refill >= bucket.policy.BurstBytes-bucket.tokens {
			bucket.tokens = bucket.policy.BurstBytes
		} else {
			bucket.tokens += refill
		}
		bucket.lastAt = dueAt
	}
	if bucket.tokens >= bytes {
		bucket.tokens -= bytes
		return dueAt
	}
	deficit := bytes - bucket.tokens
	wait := durationForBytes(deficit, bucket.policy.BytesPerSecond)
	bucket.tokens = 0
	bucket.lastAt += wait
	return bucket.lastAt
}

// scaledDuration 计算 floor(duration*rate/second)，避免乘法溢出。
func scaledDuration(duration time.Duration, rate uint64) uint64 {
	seconds := uint64(duration / time.Second)
	remainder := uint64(duration % time.Second)
	if seconds > math.MaxUint64/rate {
		return math.MaxUint64
	}
	whole := seconds * rate
	rateSeconds := rate / uint64(time.Second)
	rateRemainder := rate % uint64(time.Second)
	if rateSeconds != 0 && remainder > math.MaxUint64/rateSeconds {
		return math.MaxUint64
	}
	fractionWhole := remainder * rateSeconds
	fractionRemainder := remainder * rateRemainder / uint64(time.Second)
	if fractionWhole > math.MaxUint64-fractionRemainder {
		return math.MaxUint64
	}
	fraction := fractionWhole + fractionRemainder
	if whole > math.MaxUint64-fraction {
		return math.MaxUint64
	}
	return whole + fraction
}

// durationForBytes 计算 ceil(bytes/rate seconds)，结果至少为一 nanosecond。
func durationForBytes(bytes uint64, rate uint64) time.Duration {
	whole := bytes / rate
	remainder := bytes % rate
	if whole > uint64(math.MaxInt64/int64(time.Second)) {
		return time.Duration(math.MaxInt64)
	}
	nanoseconds := whole * uint64(time.Second)
	if remainder != 0 {
		high, low := bits.Mul64(remainder, uint64(time.Second))
		quotient, modulus := bits.Div64(high, low, rate)
		fraction := quotient
		if modulus != 0 {
			fraction++
		}
		if nanoseconds > uint64(math.MaxInt64)-fraction {
			return time.Duration(math.MaxInt64)
		}
		nanoseconds += fraction
	}
	if nanoseconds == 0 {
		nanoseconds = 1
	}
	return time.Duration(nanoseconds)
}

// scheduledCopy 是 heap 内部的 owned packet copy。
type scheduledCopy struct {
	// packet 保存不可修改的 ingress identity 与 bytes。
	packet Packet
	// copyIndex 是稳定排序的最后一项。
	copyIndex uint8
	// dueAt 是 impairment 和 bandwidth 共同决定的时间。
	dueAt time.Duration
	// deadlineAt 是 receivedAt 加 manifest lifetime。
	deadlineAt time.Duration
}

// scheduleHeap 按规范 tuple 实现最小堆。
type scheduleHeap []scheduledCopy

// Len 返回 queued copy 数。
func (items scheduleHeap) Len() int {
	return len(items)
}

// Less 实现 dueTime/direction/clientSlot/receiveSequence/copyIndex 全序。
func (items scheduleHeap) Less(left int, right int) bool {
	a, b := items[left], items[right]
	if a.dueAt != b.dueAt {
		return a.dueAt < b.dueAt
	}
	if a.packet.Direction != b.packet.Direction {
		return a.packet.Direction < b.packet.Direction
	}
	if a.packet.ClientSlot != b.packet.ClientSlot {
		return a.packet.ClientSlot < b.packet.ClientSlot
	}
	if a.packet.ReceiveSequence != b.packet.ReceiveSequence {
		return a.packet.ReceiveSequence < b.packet.ReceiveSequence
	}
	return a.copyIndex < b.copyIndex
}

// Swap 交换 heap 元素。
func (items scheduleHeap) Swap(left int, right int) {
	items[left], items[right] = items[right], items[left]
}

// Push 接收 heap package 传入的 scheduled copy。
func (items *scheduleHeap) Push(value any) {
	*items = append(*items, value.(scheduledCopy))
}

// Pop 移除并返回最后一个 heap 元素。
func (items *scheduleHeap) Pop() any {
	previous := *items
	last := len(previous) - 1
	value := previous[last]
	previous[last] = scheduledCopy{}
	*items = previous[:last]
	return value
}

// Scheduler 是 deterministic fault、带宽与有界 queue 的唯一状态 owner。
//
// 方法可并发调用；mutex 同时串行化 PRNG 消耗和 heap 变更，因此相同 receive sequence
// 调用序列产生相同 disposition。调用者必须保证 ReceiveSequence 本身由单一 owner 分配。
type Scheduler struct {
	// config 是构造时完整验证的 manifest 投影。
	config Config
	// random 是双向共享的规范化 PRNG stream。
	random lcg32
	// patternSeed 是当前 workload phase 每轮重复使用的派生 seed。
	patternSeed uint32
	// patternStartedAt 是当前 workload phase 的 gateway 单调起点。
	patternStartedAt time.Duration
	// patternEpoch 是最近一次已调度 packet 所属的重复轮次。
	patternEpoch uint64
	// patternSequence 是当前重复轮次内的 packet ordinal。
	patternSequence uint64
	// queue 是稳定 tuple 排序的全部 copy。
	queue scheduleHeap
	// queuedByClientDirection 保护 per-client per-direction hard cap。
	queuedByClientDirection map[clientDirection]int
	// buckets 按 closed direction 隔离 bandwidth timeline。
	buckets map[Direction]*tokenBucket
	// paused 是 direction owner 显式控制的 network state。
	paused map[Direction]bool
	// closed 禁止关闭后复用 PRNG 或 queue。
	closed bool
	// mutex 保护本类型全部 mutable state。
	mutex sync.Mutex
}

// clientDirection 唯一定位一个 per-client directional queue budget。
type clientDirection struct {
	// clientSlot 是 run-local slot。
	clientSlot uint8
	// direction 是 closed network direction。
	direction Direction
}

// NewScheduler 验证完整配置后创建空 deterministic scheduler。
func NewScheduler(config Config) (*Scheduler, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	scheduler := &Scheduler{
		config:                  config,
		random:                  lcg32{state: config.Seed},
		patternSeed:             config.Seed,
		queuedByClientDirection: make(map[clientDirection]int),
		buckets: map[Direction]*tokenBucket{
			DirectionUplink:   {policy: config.Uplink.Bandwidth},
			DirectionDownlink: {policy: config.Downlink.Bandwidth},
		},
		paused: make(map[Direction]bool),
	}
	heap.Init(&scheduler.queue)
	return scheduler, nil
}

// RestartPattern 为新 workload phase 原子重置派生 seed 与有限模式时钟。
//
// 已排队 copy 保留原 due/deadline；只有后续 ingress 使用新 phase 的重复 fault draw。
func (scheduler *Scheduler) RestartPattern(seed uint32, startedAt time.Duration) error {
	if seed == 0 || startedAt < 0 {
		return ErrInvalidPacket
	}
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()
	if scheduler.closed {
		return ErrClosed
	}
	scheduler.patternSeed = seed
	scheduler.patternStartedAt = startedAt
	scheduler.patternEpoch = 0
	scheduler.patternSequence = 0
	scheduler.random.state = seed
	return nil
}

// SetPaused 原子切换单方向 pause；pause 期间新 packet 以稳定终态丢弃。
func (scheduler *Scheduler) SetPaused(direction Direction, paused bool) error {
	if !direction.Valid() {
		return ErrInvalidPacket
	}
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()
	if scheduler.closed {
		return ErrClosed
	}
	scheduler.paused[direction] = paused
	return nil
}

// Schedule 消耗固定 LCG draw 顺序并原子接受 original/duplicate copies。
func (scheduler *Scheduler) Schedule(packet Packet) (ScheduleResult, error) {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()
	if scheduler.closed {
		return ScheduleResult{}, ErrClosed
	}
	policy, ok := scheduler.config.Policy(packet.Direction)
	if !ok || packet.ClientSlot == 0 ||
		packet.ClientSlot > scheduler.config.MaximumClients ||
		packet.MappingGeneration == 0 ||
		packet.ReceiveSequence == 0 ||
		packet.ReceivedAt < 0 ||
		packet.ReceivedAt >
			time.Duration(math.MaxInt64)-scheduler.config.PacketLifetime ||
		len(packet.Payload) == 0 {
		return ScheduleResult{}, ErrInvalidPacket
	}
	if packet.ReceivedAt < scheduler.patternStartedAt {
		return ScheduleResult{}, ErrInvalidPacket
	}
	epoch := uint64(
		(packet.ReceivedAt - scheduler.patternStartedAt) /
			scheduler.config.PatternDuration,
	)
	if epoch != scheduler.patternEpoch {
		scheduler.patternEpoch = epoch
		scheduler.patternSequence = 0
		scheduler.random.state = scheduler.patternSeed
	}
	scheduler.patternSequence++
	faultSequence := scheduler.patternSequence
	packet.Payload = append([]byte(nil), packet.Payload...)
	defer clear(packet.Payload)
	kind, correlation := classifyPacket(packet.Payload)
	makeMetadata := func(copyIndex uint8, dueAt time.Duration, disposition Disposition) Metadata {
		return Metadata{
			Direction:         packet.Direction,
			ClientSlot:        packet.ClientSlot,
			MappingGeneration: packet.MappingGeneration,
			ReceiveSequence:   packet.ReceiveSequence,
			CopyIndex:         copyIndex,
			LengthBytes:       len(packet.Payload),
			PacketKind:        kind,
			Correlation:       correlation,
			ReceivedAt:        packet.ReceivedAt,
			DueAt:             dueAt,
			Disposition:       disposition,
		}
	}
	if len(packet.Payload) > scheduler.config.MaximumDatagramBytes {
		return ScheduleResult{Finalized: []Metadata{
			makeMetadata(0, packet.ReceivedAt, DispositionMTU),
		}}, nil
	}
	if scheduler.paused[packet.Direction] {
		return ScheduleResult{Finalized: []Metadata{
			makeMetadata(0, packet.ReceivedAt, DispositionPaused),
		}}, nil
	}

	loss := scheduler.random.next()%percentageCeiling < uint32(policy.LossPercent)
	jitter := time.Duration(0)
	if policy.Jitter > 0 {
		jitterMicroseconds := uint64(policy.Jitter / time.Microsecond)
		width := jitterMicroseconds*2 + 1
		offset := time.Duration(uint64(scheduler.random.next())%width) * time.Microsecond
		jitter = offset - policy.Jitter
	}
	reordered := scheduler.random.next()%percentageCeiling < uint32(policy.ReorderPercent)
	burst := policy.BurstInterval > 0 &&
		faultSequence%policy.BurstInterval < policy.BurstLength
	duplicate := scheduler.random.next()%percentageCeiling < uint32(policy.DuplicatePercent)
	if loss || burst {
		disposition := DispositionLoss
		if burst {
			disposition = DispositionBurst
		}
		return ScheduleResult{Finalized: []Metadata{
			makeMetadata(0, packet.ReceivedAt, disposition),
		}}, nil
	}

	delay := policy.BaseLatency + jitter
	if reordered {
		delay -= scheduler.config.ReorderAdvance
		if delay < 0 {
			delay = 0
		}
	}
	dueAt := packet.ReceivedAt + delay
	deadlineAt := packet.ReceivedAt + scheduler.config.PacketLifetime
	copies := 1
	if duplicate {
		copies = duplicateCopies
	}
	key := clientDirection{clientSlot: packet.ClientSlot, direction: packet.Direction}
	if len(scheduler.queue)+copies > scheduler.config.GlobalQueueItems ||
		scheduler.queuedByClientDirection[key]+copies > policy.QueueItems {
		finalized := make([]Metadata, copies)
		for copyIndex := range copies {
			finalized[copyIndex] = makeMetadata(
				uint8(copyIndex), dueAt, DispositionQueue)
		}
		return ScheduleResult{Finalized: finalized}, nil
	}

	finalized := make([]Metadata, 0, copies)
	queuedCopies := 0
	bucket := scheduler.buckets[packet.Direction]
	for copyIndex := range copies {
		beforeReservation := *bucket
		copyDueAt := bucket.reserve(dueAt, uint64(len(packet.Payload)))
		if copyDueAt >= deadlineAt {
			*bucket = beforeReservation
			finalized = append(
				finalized,
				makeMetadata(
					uint8(copyIndex),
					copyDueAt,
					DispositionDeadline))
			continue
		}
		copyPacket := packet
		copyPacket.Payload = append([]byte(nil), packet.Payload...)
		heap.Push(&scheduler.queue, scheduledCopy{
			packet:     copyPacket,
			copyIndex:  uint8(copyIndex),
			dueAt:      copyDueAt,
			deadlineAt: deadlineAt,
		})
		scheduler.queuedByClientDirection[key]++
		queuedCopies++
	}
	return ScheduleResult{
		Finalized:    finalized,
		QueuedCopies: queuedCopies,
	}, nil
}

// PopDue 返回 now 前的全部 copy；过期 copy 只生成终局 evidence。
func (scheduler *Scheduler) PopDue(now time.Duration) ([]Delivery, []Metadata, error) {
	if now < 0 {
		return nil, nil, ErrInvalidPacket
	}
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()
	if scheduler.closed {
		return nil, nil, ErrClosed
	}
	var deliveries []Delivery
	var finalized []Metadata
	for len(scheduler.queue) > 0 && scheduler.queue[0].dueAt <= now {
		copy := heap.Pop(&scheduler.queue).(scheduledCopy)
		key := clientDirection{
			clientSlot: copy.packet.ClientSlot,
			direction:  copy.packet.Direction,
		}
		scheduler.queuedByClientDirection[key]--
		if now >= copy.deadlineAt {
			kind, correlation := classifyPacket(copy.packet.Payload)
			finalized = append(finalized, Metadata{
				Direction:         copy.packet.Direction,
				ClientSlot:        copy.packet.ClientSlot,
				MappingGeneration: copy.packet.MappingGeneration,
				ReceiveSequence:   copy.packet.ReceiveSequence,
				CopyIndex:         copy.copyIndex,
				LengthBytes:       len(copy.packet.Payload),
				PacketKind:        kind,
				Correlation:       correlation,
				ReceivedAt:        copy.packet.ReceivedAt,
				DueAt:             copy.dueAt,
				Disposition:       DispositionDeadline,
			})
			clear(copy.packet.Payload)
			continue
		}
		deliveries = append(deliveries, Delivery{
			Packet:    copy.packet,
			CopyIndex: copy.copyIndex,
			DueAt:     copy.dueAt,
		})
	}
	return deliveries, finalized, nil
}

// PendingCopies 返回仍未产生终局的 fault copy 数，用于 quiescence barrier。
func (scheduler *Scheduler) PendingCopies() int {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()
	return len(scheduler.queue)
}

// FinalizeDelivery 生成 socket owner 写入后的唯一终局 evidence。
func FinalizeDelivery(
	delivery Delivery,
	disposition Disposition,
	finalizedAt time.Duration,
) (Metadata, error) {
	defer clear(delivery.Packet.Payload)
	if disposition != DispositionDelivered &&
		disposition != DispositionMappingExpired &&
		disposition != DispositionWriteFailed {
		return Metadata{}, fmt.Errorf("%w: delivery disposition", ErrInvalidPacket)
	}
	if finalizedAt < delivery.Packet.ReceivedAt {
		return Metadata{}, fmt.Errorf("%w: delivery time", ErrInvalidPacket)
	}
	kind, correlation := classifyPacket(delivery.Packet.Payload)
	metadata := Metadata{
		Direction:         delivery.Packet.Direction,
		ClientSlot:        delivery.Packet.ClientSlot,
		MappingGeneration: delivery.Packet.MappingGeneration,
		ReceiveSequence:   delivery.Packet.ReceiveSequence,
		CopyIndex:         delivery.CopyIndex,
		LengthBytes:       len(delivery.Packet.Payload),
		PacketKind:        kind,
		Correlation:       correlation,
		ReceivedAt:        delivery.Packet.ReceivedAt,
		DueAt:             delivery.DueAt,
		Disposition:       disposition,
	}
	if disposition == DispositionDelivered {
		metadata.DeliveredAt = finalizedAt
	}
	return metadata, nil
}

// Close 终结 queued copy、清空 opaque bytes 并禁止继续消耗 PRNG stream。
func (scheduler *Scheduler) Close() []Metadata {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()
	if scheduler.closed {
		return nil
	}
	scheduler.closed = true
	finalized := make([]Metadata, 0, len(scheduler.queue))
	for len(scheduler.queue) > 0 {
		copy := heap.Pop(&scheduler.queue).(scheduledCopy)
		kind, correlation := classifyPacket(copy.packet.Payload)
		finalized = append(finalized, Metadata{
			Direction:         copy.packet.Direction,
			ClientSlot:        copy.packet.ClientSlot,
			MappingGeneration: copy.packet.MappingGeneration,
			ReceiveSequence:   copy.packet.ReceiveSequence,
			CopyIndex:         copy.copyIndex,
			LengthBytes:       len(copy.packet.Payload),
			PacketKind:        kind,
			Correlation:       correlation,
			ReceivedAt:        copy.packet.ReceivedAt,
			DueAt:             copy.dueAt,
			Disposition:       DispositionClosed,
		})
		clear(copy.packet.Payload)
	}
	scheduler.queue = nil
	clear(scheduler.queuedByClientDirection)
	return finalized
}
