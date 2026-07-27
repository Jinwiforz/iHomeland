package workload

import (
	"errors"
	"testing"

	battlev1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/battle/v1"
	"google.golang.org/protobuf/proto"
)

// TestObserverCommitsOnlyCompleteBaselineAndMatchingDelta 验证分片完整性与 baseline identity。
func TestObserverCommitsOnlyCompleteBaselineAndMatchingDelta(t *testing.T) {
	observer := NewObserver(11)
	for _, partition := range []uint32{1, 0} {
		full := battlev1.BattleFullSnapshot_builder{
			ServerTick:             proto.Uint64(10),
			SnapshotSequence:       proto.Uint64(1),
			BaselineId:             proto.Uint64(7),
			PartitionIndex:         proto.Uint32(partition),
			PartitionCount:         proto.Uint32(2),
			LastProcessedInputTick: proto.Uint64(4),
		}.Build()
		if err := observer.ObserveFull(full); err != nil {
			t.Fatalf("observe full partition %d: %v", partition, err)
		}
	}
	if state := observer.State(); state.BaselineID != 7 ||
		state.LatestSnapshotSequence != 1 || state.LatestServerTick != 10 {
		t.Fatalf("full baseline state=%+v", state)
	}
	delta := battlev1.BattleDeltaSnapshot_builder{
		ServerTick:             proto.Uint64(11),
		SnapshotSequence:       proto.Uint64(2),
		BaselineId:             proto.Uint64(7),
		PartitionIndex:         proto.Uint32(0),
		PartitionCount:         proto.Uint32(1),
		LastProcessedInputTick: proto.Uint64(6),
	}.Build()
	if err := observer.ObserveDelta(delta); err != nil {
		t.Fatalf("observe delta: %v", err)
	}
	if observer.State().LatestSnapshotSequence != 2 {
		t.Fatal("delta snapshot did not advance sequence")
	}
}

// TestObserverRequiresResyncAndCorrelatesResponse 验证 missing baseline 只走唯一 KCP resync。
func TestObserverRequiresResyncAndCorrelatesResponse(t *testing.T) {
	observer := NewObserver(11)
	delta := battlev1.BattleDeltaSnapshot_builder{
		ServerTick:             proto.Uint64(2),
		SnapshotSequence:       proto.Uint64(1),
		BaselineId:             proto.Uint64(9),
		PartitionIndex:         proto.Uint32(0),
		PartitionCount:         proto.Uint32(1),
		LastProcessedInputTick: proto.Uint64(0),
	}.Build()
	if err := observer.ObserveDelta(delta); !errors.Is(err, ErrResyncRequired) {
		t.Fatalf("missing baseline error=%v", err)
	}
	request, err := observer.NewResyncRequest(
		battlev1.BattleResyncReason_BATTLE_RESYNC_REASON_MISSING_BASELINE,
	)
	if err != nil {
		t.Fatalf("new resync: %v", err)
	}
	if _, err := observer.NewResyncRequest(
		battlev1.BattleResyncReason_BATTLE_RESYNC_REASON_MISSING_BASELINE,
	); err == nil {
		t.Fatal("second pending resync was accepted")
	}
	disposition := battlev1.BattleResyncDisposition_BATTLE_RESYNC_DISPOSITION_SCHEDULED
	response := battlev1.BattleResyncResponse_builder{
		RequestSequence:     proto.Uint64(request.GetRequestSequence()),
		ServerTick:          proto.Uint64(3),
		Disposition:         &disposition,
		ScheduledBaselineId: proto.Uint64(10),
		RetryAfterMs:        proto.Uint32(0),
	}.Build()
	if err := observer.ObserveResync(response); err != nil {
		t.Fatalf("observe resync: %v", err)
	}
	if observer.State().PendingResyncSequence != 0 {
		t.Fatal("resync response did not clear pending request")
	}
}

// TestObserverRejectsDuplicatePartitionAndReliableEvent 验证 raw/KCP 重复均不推进状态。
func TestObserverRejectsDuplicatePartitionAndReliableEvent(t *testing.T) {
	observer := NewObserver(11)
	full := battlev1.BattleFullSnapshot_builder{
		ServerTick:             proto.Uint64(1),
		SnapshotSequence:       proto.Uint64(1),
		BaselineId:             proto.Uint64(1),
		PartitionIndex:         proto.Uint32(0),
		PartitionCount:         proto.Uint32(2),
		LastProcessedInputTick: proto.Uint64(0),
	}.Build()
	if err := observer.ObserveFull(full); err != nil {
		t.Fatalf("observe full: %v", err)
	}
	if err := observer.ObserveFull(full); err == nil {
		t.Fatal("duplicate snapshot partition was accepted")
	}
	phase := battlev1.BattleAbilityPhase_BATTLE_ABILITY_PHASE_STARTED
	event := battlev1.BattleAbilityReliableEvent_builder{
		EventId: proto.Uint64(1), ServerTick: proto.Uint64(2), Phase: &phase,
	}.Build()
	if err := observer.ObserveAbility(event); err != nil {
		t.Fatalf("observe ability: %v", err)
	}
	if err := observer.ObserveAbility(event); err == nil {
		t.Fatal("duplicate reliable event was accepted")
	}
}

// TestObserverRequiresPresenceAndStablePartitionAcknowledgement 验证零值 presence 与分片一致性。
func TestObserverRequiresPresenceAndStablePartitionAcknowledgement(t *testing.T) {
	observer := NewObserver(12)
	missing := battlev1.BattleFullSnapshot_builder{
		ServerTick:       proto.Uint64(1),
		SnapshotSequence: proto.Uint64(1),
		BaselineId:       proto.Uint64(1),
		PartitionIndex:   proto.Uint32(0),
		PartitionCount:   proto.Uint32(1),
	}.Build()
	if err := observer.ObserveFull(missing); err == nil {
		t.Fatal("missing input acknowledgement presence was accepted")
	}
	first := battlev1.BattleFullSnapshot_builder{
		ServerTick:             proto.Uint64(1),
		SnapshotSequence:       proto.Uint64(1),
		BaselineId:             proto.Uint64(1),
		PartitionIndex:         proto.Uint32(0),
		PartitionCount:         proto.Uint32(2),
		LastProcessedInputTick: proto.Uint64(0),
	}.Build()
	if err := observer.ObserveFull(first); err != nil {
		t.Fatalf("explicit zero acknowledgement: %v", err)
	}
	drifted := proto.Clone(first).(*battlev1.BattleFullSnapshot)
	drifted.SetPartitionIndex(1)
	drifted.SetLastProcessedInputTick(1)
	if err := observer.ObserveFull(drifted); err == nil {
		t.Fatal("partition acknowledgement drift was accepted")
	}
	if state := observer.State(); state.MappingGeneration != 12 ||
		state.LatestSnapshotSequence != 0 ||
		state.LastProcessedInputTick != 0 {
		t.Fatalf("drifted partition published state=%+v", state)
	}
	correctSecond := proto.Clone(first).(*battlev1.BattleFullSnapshot)
	correctSecond.SetPartitionIndex(1)
	if err := observer.ObserveFull(correctSecond); err != nil {
		t.Fatalf("restart second partition: %v", err)
	}
	if state := observer.State(); state.LatestSnapshotSequence != 0 {
		t.Fatalf("rejected set retained predecessor partition: %+v", state)
	}
	if err := observer.ObserveFull(first); err != nil {
		t.Fatalf("restart first partition: %v", err)
	}
	if state := observer.State(); state.LatestSnapshotSequence != 1 ||
		state.LastProcessedInputTick != 0 {
		t.Fatalf("complete restarted set was not published: %+v", state)
	}
}
