package workload

import "testing"

// TestInputAcknowledgementTrackerCorrelatesExplicitCursor 验证批量推进只覆盖已发送输入。
func TestInputAcknowledgementTrackerCorrelatesExplicitCursor(t *testing.T) {
	tracker, err := NewInputAcknowledgementTracker(11)
	if err != nil {
		t.Fatalf("new tracker: %v", err)
	}
	for _, inputTick := range []uint64{1, 2, 3, 4} {
		if err := tracker.RecordSent(11, inputTick); err != nil {
			t.Fatalf("record input %d: %v", inputTick, err)
		}
	}
	window, err := tracker.Advance(11, 4)
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if window.MappingGeneration != 11 ||
		window.FirstInputTick != 1 || window.LastInputTick != 4 {
		t.Fatalf("acknowledgement window=%+v", window)
	}
	if _, err := tracker.Advance(11, 5); err == nil {
		t.Fatal("acknowledgement advanced beyond sent input")
	}
}

// TestInputAcknowledgementTrackerRejectsPredecessorAfterReplacement 验证重连不继承确认。
func TestInputAcknowledgementTrackerRejectsPredecessorAfterReplacement(t *testing.T) {
	tracker, err := NewInputAcknowledgementTracker(20)
	if err != nil {
		t.Fatalf("new tracker: %v", err)
	}
	if err := tracker.RecordSent(20, 1); err != nil {
		t.Fatalf("record predecessor: %v", err)
	}
	if err := tracker.ReplaceMapping(21); err != nil {
		t.Fatalf("replace mapping: %v", err)
	}
	if _, err := tracker.Advance(20, 1); err == nil {
		t.Fatal("predecessor acknowledgement reached successor")
	}
	window, err := tracker.Advance(21, 0)
	if err != nil || window.FirstInputTick != 0 || window.LastInputTick != 0 {
		t.Fatalf("successor zero acknowledgement window=%+v err=%v", window, err)
	}
}

// TestInputAcknowledgementTrackerKeepsOwnAndVisitSessionsIndependent 验证角色不同的 actor 不共享确认前沿。
func TestInputAcknowledgementTrackerKeepsOwnAndVisitSessionsIndependent(t *testing.T) {
	const (
		ownMappingGeneration   = 31
		visitMappingGeneration = 41
	)
	tests := []struct {
		name              string
		mappingGeneration uint64
		sentThrough       uint64
		acknowledged      uint64
	}{
		{
			name:              "own-world",
			mappingGeneration: ownMappingGeneration,
			sentThrough:       4,
			acknowledged:      4,
		},
		{
			name:              "visit-world",
			mappingGeneration: visitMappingGeneration,
			sentThrough:       6,
			acknowledged:      3,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			tracker, err := NewInputAcknowledgementTracker(
				test.mappingGeneration,
			)
			if err != nil {
				t.Fatal(err)
			}
			for inputTick := uint64(1); inputTick <= test.sentThrough; inputTick++ {
				if err := tracker.RecordSent(test.mappingGeneration, inputTick); err != nil {
					t.Fatalf("record input %d: %v", inputTick, err)
				}
			}
			window, err := tracker.Advance(
				test.mappingGeneration,
				test.acknowledged,
			)
			if err != nil ||
				window.MappingGeneration != test.mappingGeneration ||
				window.FirstInputTick != 1 ||
				window.LastInputTick != test.acknowledged {
				t.Fatalf("acknowledgement window=%+v err=%v", window, err)
			}
		})
	}
}

// TestInputAcknowledgementTrackerHandlesLossReorderAndDuplicate 验证 snapshot delivery 异常不伪造确认。
func TestInputAcknowledgementTrackerHandlesLossReorderAndDuplicate(t *testing.T) {
	const (
		mappingGeneration       = 51
		sentInputCount          = 6
		firstAcknowledgement    = 4
		successorAcknowledgment = 6
	)
	tracker, err := NewInputAcknowledgementTracker(mappingGeneration)
	if err != nil {
		t.Fatal(err)
	}
	for inputTick := uint64(1); inputTick <= sentInputCount; inputTick++ {
		if err := tracker.RecordSent(mappingGeneration, inputTick); err != nil {
			t.Fatalf("record input %d: %v", inputTick, err)
		}
	}
	window, err := tracker.Advance(mappingGeneration, firstAcknowledgement)
	if err != nil || window.FirstInputTick != 1 ||
		window.LastInputTick != firstAcknowledgement {
		t.Fatalf("reordered snapshot window=%+v err=%v", window, err)
	}
	duplicate, err := tracker.Advance(mappingGeneration, firstAcknowledgement)
	if err != nil || duplicate.FirstInputTick != 0 ||
		duplicate.LastInputTick != 0 {
		t.Fatalf("duplicate acknowledgement window=%+v err=%v", duplicate, err)
	}
	if _, err := tracker.Advance(mappingGeneration, firstAcknowledgement-1); err == nil {
		t.Fatal("late predecessor acknowledgement was accepted")
	}
	window, err = tracker.Advance(mappingGeneration, successorAcknowledgment)
	if err != nil ||
		window.FirstInputTick != firstAcknowledgement+1 ||
		window.LastInputTick != successorAcknowledgment {
		t.Fatalf("post-loss acknowledgement window=%+v err=%v", window, err)
	}
}

// TestInputAcknowledgementTrackerDoesNotCorrelateBackpressuredInput 验证失败发送不会扩大可确认前沿。
func TestInputAcknowledgementTrackerDoesNotCorrelateBackpressuredInput(t *testing.T) {
	const (
		mappingGeneration = 61
		acceptedThrough   = 3
		rejectedInputTick = 4
	)
	tracker, err := NewInputAcknowledgementTracker(mappingGeneration)
	if err != nil {
		t.Fatal(err)
	}
	for inputTick := uint64(1); inputTick <= acceptedThrough; inputTick++ {
		if err := tracker.RecordSent(mappingGeneration, inputTick); err != nil {
			t.Fatalf("record input %d: %v", inputTick, err)
		}
	}
	// rejectedInputTick 模拟 sink 返回 backpressure，因此不调用 RecordSent。
	if _, err := tracker.Advance(mappingGeneration, rejectedInputTick); err == nil {
		t.Fatal("backpressured input was correlated as sent")
	}
	window, err := tracker.Advance(mappingGeneration, acceptedThrough)
	if err != nil || window.FirstInputTick != 1 ||
		window.LastInputTick != acceptedThrough {
		t.Fatalf("accepted frontier window=%+v err=%v", window, err)
	}
}
