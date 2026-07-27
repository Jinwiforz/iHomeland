package gateway

import (
	"reflect"
	"testing"
	"time"
)

// testConfig 返回无 bandwidth impairment 的完整双向 fixture。
func testConfig() Config {
	policy := DirectionPolicy{
		BaseLatency:      50 * time.Millisecond,
		Jitter:           10 * time.Millisecond,
		LossPercent:      0,
		DuplicatePercent: 0,
		ReorderPercent:   0,
		BurstInterval:    0,
		BurstLength:      0,
		QueueItems:       32,
		Bandwidth:        BandwidthPolicy{Enabled: false},
	}
	return Config{
		Seed:                 20_260_723,
		PatternDuration:      2 * time.Second,
		MaximumClients:       8,
		MaximumDatagramBytes: 1_200,
		GlobalQueueItems:     256,
		PacketLifetime:       500 * time.Millisecond,
		ReorderAdvance:       20 * time.Millisecond,
		Uplink:               policy,
		Downlink:             policy,
	}
}

// secureFixture 构造只有公开 header 有意义的 opaque datagram。
func secureFixture(kind byte, length int) []byte {
	payload := make([]byte, length)
	copy(payload, "IHBT")
	payload[4] = 1
	payload[5] = kind
	copy(payload[8:16], []byte{1, 2, 3, 4, 5, 6, 7, 8})
	return payload
}

// TestLCG32CanonicalVector 保护与 B0.2 PowerShell simulator 相同的 PRNG stream。
func TestLCG32CanonicalVector(t *testing.T) {
	random := lcg32{state: 20_260_723}
	expected := []uint32{
		1_410_647_606,
		599_558_173,
		1_980_918_488,
		17_330_263,
		2_669_564_362,
	}
	for index, want := range expected {
		if got := random.next(); got != want {
			t.Fatalf("draw %d=%d want=%d", index, got, want)
		}
	}
}

// TestSchedulerCanonicalFaultArithmetic 验证 jitter 单位与固定 draw 顺序。
func TestSchedulerCanonicalFaultArithmetic(t *testing.T) {
	scheduler, err := NewScheduler(testConfig())
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	result, err := scheduler.Schedule(Packet{
		Direction:         DirectionUplink,
		ClientSlot:        1,
		MappingGeneration: 1,
		ReceiveSequence:   1,
		ReceivedAt:        0,
		Payload:           secureFixture(1, 64),
	})
	if err != nil || result.QueuedCopies != 1 || len(result.Finalized) != 0 {
		t.Fatalf("Schedule result=%+v err=%v", result, err)
	}
	deliveries, finalized, err := scheduler.PopDue(48_197 * time.Microsecond)
	if err != nil || len(finalized) != 0 || len(deliveries) != 1 {
		t.Fatalf("PopDue deliveries=%d finalized=%d err=%v", len(deliveries), len(finalized), err)
	}
	if deliveries[0].DueAt != 48_197*time.Microsecond {
		t.Fatalf("due=%s", deliveries[0].DueAt)
	}
}

// TestSchedulerStableTupleOrder 验证同 due time 的 closed tuple 排序。
func TestSchedulerStableTupleOrder(t *testing.T) {
	config := testConfig()
	config.Uplink.BaseLatency, config.Uplink.Jitter = 0, 0
	config.Downlink.BaseLatency, config.Downlink.Jitter = 0, 0
	scheduler, err := NewScheduler(config)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	inputs := []Packet{
		{Direction: DirectionDownlink, ClientSlot: 2, MappingGeneration: 1, ReceiveSequence: 4, Payload: secureFixture(2, 64)},
		{Direction: DirectionUplink, ClientSlot: 2, MappingGeneration: 1, ReceiveSequence: 3, Payload: secureFixture(1, 64)},
		{Direction: DirectionUplink, ClientSlot: 1, MappingGeneration: 1, ReceiveSequence: 2, Payload: secureFixture(1, 64)},
	}
	for _, packet := range inputs {
		if _, err := scheduler.Schedule(packet); err != nil {
			t.Fatalf("Schedule: %v", err)
		}
	}
	deliveries, _, err := scheduler.PopDue(0)
	if err != nil {
		t.Fatalf("PopDue: %v", err)
	}
	got := make([]uint64, len(deliveries))
	for index := range deliveries {
		got[index] = deliveries[index].Packet.ReceiveSequence
	}
	if want := []uint64{2, 3, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order=%v want=%v", got, want)
	}
}

// TestSchedulerDuplicateQueueAndBurst 验证 duplicate 原子 queue 预算与 burst 终局。
func TestSchedulerDuplicateQueueAndBurst(t *testing.T) {
	config := testConfig()
	config.Uplink.DuplicatePercent = 100
	config.Uplink.QueueItems = 1
	scheduler, err := NewScheduler(config)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	result, err := scheduler.Schedule(Packet{
		Direction: DirectionUplink, ClientSlot: 1, MappingGeneration: 1,
		ReceiveSequence: 1, Payload: secureFixture(1, 64),
	})
	if err != nil || result.QueuedCopies != 0 || len(result.Finalized) != 2 {
		t.Fatalf("duplicate queue result=%+v err=%v", result, err)
	}
	for _, metadata := range result.Finalized {
		if metadata.Disposition != DispositionQueue {
			t.Fatalf("disposition=%s", metadata.Disposition.String())
		}
	}

	config = testConfig()
	config.Uplink.BurstInterval = 2
	config.Uplink.BurstLength = 1
	scheduler, err = NewScheduler(config)
	if err != nil {
		t.Fatalf("NewScheduler burst: %v", err)
	}
	if _, err := scheduler.Schedule(Packet{
		Direction: DirectionUplink, ClientSlot: 1, MappingGeneration: 1,
		ReceiveSequence: 1, Payload: secureFixture(1, 64),
	}); err != nil {
		t.Fatalf("Schedule first burst ordinal: %v", err)
	}
	result, err = scheduler.Schedule(Packet{
		Direction: DirectionUplink, ClientSlot: 1, MappingGeneration: 1,
		ReceiveSequence: 2, Payload: secureFixture(1, 64),
	})
	if err != nil || len(result.Finalized) != 1 ||
		result.Finalized[0].Disposition != DispositionBurst {
		t.Fatalf("burst result=%+v err=%v", result, err)
	}
}

// TestSchedulerRepeatsFiniteFaultPattern 验证每个 source duration 重放同一 draw stream。
func TestSchedulerRepeatsFiniteFaultPattern(t *testing.T) {
	config := testConfig()
	scheduler, err := NewScheduler(config)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	result, err := scheduler.Schedule(Packet{
		Direction:         DirectionUplink,
		ClientSlot:        1,
		MappingGeneration: 1,
		ReceiveSequence:   1,
		ReceivedAt:        0,
		Payload:           secureFixture(1, 64),
	})
	if err != nil || result.QueuedCopies != 1 {
		t.Fatalf("Schedule first epoch result=%+v err=%v", result, err)
	}
	first, finalized, err := scheduler.PopDue(
		config.Uplink.BaseLatency + config.Uplink.Jitter,
	)
	if err != nil || len(finalized) != 0 || len(first) != 1 {
		t.Fatalf(
			"PopDue first deliveries=%d finalized=%d err=%v",
			len(first),
			len(finalized),
			err,
		)
	}
	result, err = scheduler.Schedule(Packet{
		Direction:         DirectionUplink,
		ClientSlot:        1,
		MappingGeneration: 1,
		ReceiveSequence:   2,
		ReceivedAt:        config.PatternDuration,
		Payload:           secureFixture(1, 64),
	})
	if err != nil || result.QueuedCopies != 1 {
		t.Fatalf("Schedule second epoch result=%+v err=%v", result, err)
	}
	second, finalized, err := scheduler.PopDue(
		config.PatternDuration + config.Uplink.BaseLatency + config.Uplink.Jitter,
	)
	if err != nil || len(finalized) != 0 || len(second) != 1 {
		t.Fatalf(
			"PopDue deliveries=%d finalized=%d err=%v",
			len(second),
			len(finalized),
			err,
		)
	}
	if second[0].DueAt-first[0].DueAt != config.PatternDuration {
		t.Fatalf("fault pattern due times diverged: first=%+v second=%+v", first, second)
	}
}

// TestSchedulerBandwidthDeadline 验证 token bucket 使用整数 ceil 且不越过 expiry。
func TestSchedulerBandwidthDeadline(t *testing.T) {
	config := testConfig()
	config.Uplink.BaseLatency, config.Uplink.Jitter = 0, 0
	config.Uplink.Bandwidth = BandwidthPolicy{
		Enabled:        true,
		BytesPerSecond: 1_000,
		BurstBytes:     1_200,
	}
	scheduler, err := NewScheduler(config)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	first, err := scheduler.Schedule(Packet{
		Direction: DirectionUplink, ClientSlot: 1, MappingGeneration: 1,
		ReceiveSequence: 1, Payload: secureFixture(1, 1_200),
	})
	if err != nil || first.QueuedCopies != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := scheduler.Schedule(Packet{
		Direction: DirectionUplink, ClientSlot: 1, MappingGeneration: 1,
		ReceiveSequence: 2, Payload: secureFixture(1, 1_200),
	})
	if err != nil || second.QueuedCopies != 0 || len(second.Finalized) != 1 ||
		second.Finalized[0].Disposition != DispositionDeadline ||
		second.Finalized[0].DueAt != 1_200*time.Millisecond {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	third, err := scheduler.Schedule(Packet{
		Direction: DirectionUplink, ClientSlot: 1, MappingGeneration: 1,
		ReceiveSequence: 3, Payload: secureFixture(1, 1_200),
	})
	if err != nil || len(third.Finalized) != 1 ||
		third.Finalized[0].DueAt != 1_200*time.Millisecond {
		t.Fatalf("expired reservation consumed tokens: third=%+v err=%v", third, err)
	}
}

// TestSchedulerCopyConservation 验证每个 original/duplicate 恰有一个终局。
func TestSchedulerCopyConservation(t *testing.T) {
	config := testConfig()
	config.Uplink.BaseLatency, config.Uplink.Jitter = 0, 0
	config.Uplink.DuplicatePercent = 100
	scheduler, err := NewScheduler(config)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	const packetCount = 10
	queued := 0
	finalized := 0
	for sequence := uint64(1); sequence <= packetCount; sequence++ {
		result, scheduleErr := scheduler.Schedule(Packet{
			Direction: DirectionUplink, ClientSlot: 1, MappingGeneration: 1,
			ReceiveSequence: sequence, Payload: secureFixture(1, 64),
		})
		if scheduleErr != nil {
			t.Fatalf("Schedule %d: %v", sequence, scheduleErr)
		}
		queued += result.QueuedCopies
		finalized += len(result.Finalized)
	}
	deliveries, expired, err := scheduler.PopDue(0)
	if err != nil {
		t.Fatalf("PopDue: %v", err)
	}
	finalized += len(deliveries) + len(expired)
	if queued != packetCount*duplicateCopies ||
		finalized != packetCount*duplicateCopies {
		t.Fatalf("queued=%d finalized=%d", queued, finalized)
	}
}

// TestSchedulerMetadataPauseMTUAndCleanup 验证 evidence 低敏分类与全部终局守恒。
func TestSchedulerMetadataPauseMTUAndCleanup(t *testing.T) {
	scheduler, err := NewScheduler(testConfig())
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	if err := scheduler.SetPaused(DirectionDownlink, true); err != nil {
		t.Fatalf("SetPaused: %v", err)
	}
	paused, err := scheduler.Schedule(Packet{
		Direction: DirectionDownlink, ClientSlot: 1, MappingGeneration: 2,
		ReceiveSequence: 1, Payload: secureFixture(3, 64),
	})
	if err != nil || len(paused.Finalized) != 1 ||
		paused.Finalized[0].Disposition != DispositionPaused ||
		paused.Finalized[0].PacketKind != PublicPacketControl ||
		len(paused.Finalized[0].Correlation) != 16 {
		t.Fatalf("paused=%+v err=%v", paused, err)
	}
	if err := scheduler.SetPaused(DirectionDownlink, false); err != nil {
		t.Fatalf("resume: %v", err)
	}
	mtu, err := scheduler.Schedule(Packet{
		Direction: DirectionDownlink, ClientSlot: 1, MappingGeneration: 2,
		ReceiveSequence: 2, Payload: secureFixture(2, 1_201),
	})
	if err != nil || len(mtu.Finalized) != 1 ||
		mtu.Finalized[0].Disposition != DispositionMTU {
		t.Fatalf("mtu=%+v err=%v", mtu, err)
	}
	queued, err := scheduler.Schedule(Packet{
		Direction: DirectionDownlink, ClientSlot: 1, MappingGeneration: 2,
		ReceiveSequence: 3, Payload: secureFixture(2, 64),
	})
	if err != nil || queued.QueuedCopies != 1 {
		t.Fatalf("queued=%+v err=%v", queued, err)
	}
	closed := scheduler.Close()
	if len(closed) != 1 || closed[0].Disposition != DispositionClosed ||
		closed[0].PacketKind != PublicPacketKCP {
		t.Fatalf("closed=%+v", closed)
	}
	if _, err := scheduler.Schedule(Packet{}); err != ErrClosed {
		t.Fatalf("post-close err=%v", err)
	}
}

// TestConfigRejectsHiddenOrUnsafeDefaults 验证缺失限额与亚微秒参数 fail closed。
func TestConfigRejectsHiddenOrUnsafeDefaults(t *testing.T) {
	config := testConfig()
	config.Seed = 0
	if _, err := NewScheduler(config); err != ErrInvalidConfig {
		t.Fatalf("zero seed err=%v", err)
	}
	config = testConfig()
	config.Uplink.Jitter = time.Nanosecond
	if _, err := NewScheduler(config); err != ErrInvalidConfig {
		t.Fatalf("nanosecond jitter err=%v", err)
	}
	config = testConfig()
	config.Uplink.Bandwidth = BandwidthPolicy{
		Enabled:        false,
		BytesPerSecond: 1,
	}
	if _, err := NewScheduler(config); err == nil {
		t.Fatal("disabled bandwidth accepted hidden values")
	}
}
