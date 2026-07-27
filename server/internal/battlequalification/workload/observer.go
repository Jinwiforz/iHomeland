package workload

import (
	"errors"

	battlev1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/battle/v1"
	"google.golang.org/protobuf/proto"
)

const (
	// maximumSnapshotPartitions 与独立 C++ client 的 bounded bitmap 一致。
	maximumSnapshotPartitions = 32
)

var (
	// ErrResyncRequired 表示 delta 无法基于 current baseline 安全应用。
	ErrResyncRequired = errors.New("battle snapshot resync is required")
)

// ObserverState 是 workload 与 probe 可读取的低敏 replica 进度。
type ObserverState struct {
	// MappingGeneration 是当前 BattleSession 不可跨越的 InputTick epoch。
	MappingGeneration uint64
	// LatestServerTick 是已完整应用 snapshot/reliable event 的最大 server tick。
	LatestServerTick uint64
	// LatestSnapshotSequence 是已完整应用 snapshot 的最大 sequence。
	LatestSnapshotSequence uint64
	// BaselineID 是 current full snapshot baseline。
	BaselineID uint64
	// LatestReliableEventID 是已接受 KCP event 的最大 identity。
	LatestReliableEventID uint64
	// PendingResyncSequence 是等待 3007 response 的 request；零表示无 pending。
	PendingResyncSequence uint64
	// LastProcessedInputTick 是完整 snapshot 发布的当前 actor 连续确认。
	LastProcessedInputTick uint64
}

// Observer 维护 bounded full/delta partition、baseline/resync 与 reliable event 状态。
type Observer struct {
	// state 是只在完整 partition set 后推进的已提交进度。
	state ObserverState
	// nextResyncSequence 是下一个 KCP resync request identity。
	nextResyncSequence uint64
	// pending 是当前最多一个 snapshot partition set。
	pending pendingSnapshot
	// latestSnapshotServerTick 只比较 raw snapshot lane 内的权威 Tick。
	latestSnapshotServerTick uint64
	// latestReliableServerTick 只比较 KCP reliable event lane 内的权威 Tick。
	latestReliableServerTick uint64
}

// pendingSnapshot 使用固定 32-bit bitmap，不按远端 partition_count 分配内存。
type pendingSnapshot struct {
	// full 区分建立 baseline 与引用 baseline。
	full bool
	// serverTick、sequence、baselineID 与 partitionCount 绑定全部分片。
	serverTick     uint64
	sequence       uint64
	baselineID     uint64
	partitionCount uint32
	// lastProcessedInputTick 冻结同一 partition set 的一致确认。
	lastProcessedInputTick uint64
	// partitions 标记已接收的零基分片。
	partitions uint32
}

// NewObserver 创建绑定非零 mapping generation 的空 replica observer。
func NewObserver(mappingGeneration uint64) *Observer {
	if mappingGeneration == 0 {
		return nil
	}
	return &Observer{
		state:              ObserverState{MappingGeneration: mappingGeneration},
		nextResyncSequence: 1,
	}
}

// State 返回当前已提交进度的值副本。
func (observer *Observer) State() ObserverState {
	if observer == nil {
		return ObserverState{}
	}
	return observer.state
}

// ObserveFull 接收一个 full snapshot 分片，并仅在完整集合后建立 baseline。
func (observer *Observer) ObserveFull(snapshot *battlev1.BattleFullSnapshot) error {
	if observer == nil || snapshot == nil {
		return errors.New("full snapshot is nil")
	}
	return observer.observeSnapshot(
		true, snapshot.GetServerTick(), snapshot.GetSnapshotSequence(),
		snapshot.GetBaselineId(), snapshot.GetPartitionIndex(),
		snapshot.GetPartitionCount(), snapshot.GetLastProcessedInputTick(),
		snapshot.HasLastProcessedInputTick(),
	)
}

// ObserveDelta 接收一个 delta snapshot 分片，要求 current baseline 精确匹配。
func (observer *Observer) ObserveDelta(snapshot *battlev1.BattleDeltaSnapshot) error {
	if observer == nil || snapshot == nil {
		return errors.New("delta snapshot is nil")
	}
	if observer.state.BaselineID == 0 ||
		snapshot.GetBaselineId() != observer.state.BaselineID {
		observer.pending = pendingSnapshot{}
		return ErrResyncRequired
	}
	return observer.observeSnapshot(
		false, snapshot.GetServerTick(), snapshot.GetSnapshotSequence(),
		snapshot.GetBaselineId(), snapshot.GetPartitionIndex(),
		snapshot.GetPartitionCount(), snapshot.GetLastProcessedInputTick(),
		snapshot.HasLastProcessedInputTick(),
	)
}

// NewResyncRequest 建立最多一个 bounded pending KCP recovery request。
func (observer *Observer) NewResyncRequest(reason battlev1.BattleResyncReason) (*battlev1.BattleResyncRequest, error) {
	if observer == nil ||
		reason == battlev1.BattleResyncReason_BATTLE_RESYNC_REASON_UNSPECIFIED ||
		observer.state.PendingResyncSequence != 0 {
		return nil, errors.New("resync request state is invalid")
	}
	sequence := observer.nextResyncSequence
	observer.nextResyncSequence++
	observer.state.PendingResyncSequence = sequence
	return battlev1.BattleResyncRequest_builder{
		RequestSequence:        proto.Uint64(sequence),
		LatestServerTick:       proto.Uint64(observer.state.LatestServerTick),
		MissingBaselineId:      proto.Uint64(observer.state.BaselineID),
		LatestSnapshotSequence: proto.Uint64(observer.state.LatestSnapshotSequence),
		Reason:                 &reason,
	}.Build(), nil
}

// ObserveResync 验证 response correlation 与 closed disposition。
func (observer *Observer) ObserveResync(response *battlev1.BattleResyncResponse) error {
	if observer == nil || response == nil ||
		observer.state.PendingResyncSequence == 0 ||
		response.GetRequestSequence() != observer.state.PendingResyncSequence ||
		response.GetServerTick() == 0 {
		return errors.New("resync response state is invalid")
	}
	switch response.GetDisposition() {
	case battlev1.BattleResyncDisposition_BATTLE_RESYNC_DISPOSITION_SCHEDULED:
		if response.GetScheduledBaselineId() == 0 || response.GetRetryAfterMs() != 0 {
			return errors.New("scheduled resync response is invalid")
		}
	case battlev1.BattleResyncDisposition_BATTLE_RESYNC_DISPOSITION_RATE_LIMITED:
		if response.GetScheduledBaselineId() != 0 || response.GetRetryAfterMs() == 0 {
			return errors.New("rate-limited resync response is invalid")
		}
	case battlev1.BattleResyncDisposition_BATTLE_RESYNC_DISPOSITION_STALE_REQUEST:
		if response.GetScheduledBaselineId() != 0 || response.GetRetryAfterMs() != 0 {
			return errors.New("stale resync response is invalid")
		}
	default:
		return errors.New("unknown resync disposition")
	}
	observer.state.PendingResyncSequence = 0
	if response.GetServerTick() > observer.state.LatestServerTick {
		observer.state.LatestServerTick = response.GetServerTick()
	}
	return nil
}

// ObserveAbility 验证 reliable ability event identity、tick 与 closed phase。
func (observer *Observer) ObserveAbility(event *battlev1.BattleAbilityReliableEvent) error {
	if event == nil ||
		event.GetPhase() == battlev1.BattleAbilityPhase_BATTLE_ABILITY_PHASE_UNSPECIFIED {
		return errors.New("ability event is invalid")
	}
	return observer.observeReliable(event.GetEventId(), event.GetServerTick())
}

// ObserveLifecycle 验证 reliable entity lifecycle identity、tick 与 closed kind。
func (observer *Observer) ObserveLifecycle(event *battlev1.BattleEntityLifecycle) error {
	if event == nil ||
		event.GetKind() == battlev1.BattleEntityLifecycleKind_BATTLE_ENTITY_LIFECYCLE_KIND_UNSPECIFIED {
		return errors.New("entity lifecycle event is invalid")
	}
	return observer.observeReliable(event.GetEventId(), event.GetServerTick())
}

// observeReliable 要求 KCP application event identity 严格递增。
func (observer *Observer) observeReliable(eventID, serverTick uint64) error {
	if observer == nil || eventID == 0 || serverTick == 0 ||
		eventID <= observer.state.LatestReliableEventID ||
		serverTick < observer.latestReliableServerTick {
		return errors.New("reliable event sequence is invalid")
	}
	observer.state.LatestReliableEventID = eventID
	observer.latestReliableServerTick = serverTick
	if serverTick > observer.state.LatestServerTick {
		observer.state.LatestServerTick = serverTick
	}
	return nil
}

// observeSnapshot 用固定 bitmap 聚合同一 snapshot 的有界分片。
func (observer *Observer) observeSnapshot(
	full bool,
	serverTick uint64,
	sequence uint64,
	baselineID uint64,
	partitionIndex uint32,
	partitionCount uint32,
	lastProcessedInputTick uint64,
	hasLastProcessedInputTick bool,
) error {
	if serverTick == 0 || sequence == 0 || baselineID == 0 ||
		!hasLastProcessedInputTick ||
		partitionCount == 0 || partitionCount > maximumSnapshotPartitions ||
		partitionIndex >= partitionCount ||
		sequence <= observer.state.LatestSnapshotSequence ||
		serverTick < observer.latestSnapshotServerTick {
		if observer.pending.sequence != 0 &&
			sequence == observer.pending.sequence {
			observer.pending = pendingSnapshot{}
		}
		return errors.New("snapshot identity is invalid")
	}
	if observer.pending.sequence == 0 || sequence > observer.pending.sequence {
		observer.pending = pendingSnapshot{
			full: full, serverTick: serverTick, sequence: sequence,
			baselineID: baselineID, partitionCount: partitionCount,
			lastProcessedInputTick: lastProcessedInputTick,
		}
	}
	if sequence != observer.pending.sequence ||
		full != observer.pending.full ||
		serverTick != observer.pending.serverTick ||
		baselineID != observer.pending.baselineID ||
		partitionCount != observer.pending.partitionCount ||
		lastProcessedInputTick != observer.pending.lastProcessedInputTick {
		observer.pending = pendingSnapshot{}
		return errors.New("snapshot partition binding drifted")
	}
	bit := uint32(1) << partitionIndex
	if observer.pending.partitions&bit != 0 {
		observer.pending = pendingSnapshot{}
		return errors.New("snapshot partition is duplicated")
	}
	observer.pending.partitions |= bit
	if observer.pending.partitions != completePartitionMask(partitionCount) {
		return nil
	}
	if lastProcessedInputTick < observer.state.LastProcessedInputTick {
		observer.pending = pendingSnapshot{}
		return errors.New("snapshot input acknowledgement regressed")
	}
	observer.state.LatestServerTick = serverTick
	observer.latestSnapshotServerTick = serverTick
	if observer.latestReliableServerTick > observer.state.LatestServerTick {
		observer.state.LatestServerTick = observer.latestReliableServerTick
	}
	observer.state.LatestSnapshotSequence = sequence
	observer.state.LastProcessedInputTick = lastProcessedInputTick
	if full {
		observer.state.BaselineID = baselineID
	}
	observer.pending = pendingSnapshot{}
	return nil
}

// completePartitionMask 构造 1..32 分片的固定 bitmap。
func completePartitionMask(partitionCount uint32) uint32 {
	if partitionCount == maximumSnapshotPartitions {
		return ^uint32(0)
	}
	return (uint32(1) << partitionCount) - 1
}
