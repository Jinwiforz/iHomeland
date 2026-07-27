package workload

import (
	"testing"

	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/correlation"
	battlev1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/battle/v1"
)

// TestDriverMaintainsFortyHertzTickMappingAndBoundedRedundancy 验证 InputTick 以 40 Hz 对齐权威 Tick。
func TestDriverMaintainsFortyHertzTickMappingAndBoundedRedundancy(t *testing.T) {
	driver, err := New(correlation.PhaseMovementHeavy)
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	for step := 1; step <= 8; step++ {
		bundle, nextErr := driver.NextInput(7)
		if nextErr != nil {
			t.Fatalf("next input: %v", nextErr)
		}
		wantTick := uint64(12 + step)
		if bundle.GetNewestInputTick() != wantTick ||
			bundle.GetLatestObservedServerTick() != 7 {
			t.Fatalf("step=%d tick=%d", step, bundle.GetNewestInputTick())
		}
		wantDepth := min(step, inputBundleDepth)
		if len(bundle.GetCommands()) != wantDepth {
			t.Fatalf("step=%d depth=%d want=%d", step, len(bundle.GetCommands()), wantDepth)
		}
		for index := 1; index < len(bundle.GetCommands()); index++ {
			if bundle.GetCommands()[index].GetCommandSequence() <=
				bundle.GetCommands()[index-1].GetCommandSequence() {
				t.Fatal("bundle command sequence is not increasing")
			}
		}
	}
}

// TestDriverProducesClosedPhasePatterns 验证 movement/combat/boss/idle 使用唯一允许的 typed intent。
func TestDriverProducesClosedPhasePatterns(t *testing.T) {
	tests := []struct {
		phase correlation.WorkloadPhase
		kind  battlev1.BattleInputKind
	}{
		{correlation.PhaseClean, battlev1.BattleInputKind_BATTLE_INPUT_KIND_MOVE},
		{correlation.PhaseIdle, battlev1.BattleInputKind_BATTLE_INPUT_KIND_MOVE},
		{correlation.PhaseMovementHeavy, battlev1.BattleInputKind_BATTLE_INPUT_KIND_MOVE},
		{correlation.PhaseCombatHeavy, battlev1.BattleInputKind_BATTLE_INPUT_KIND_PRIMARY_ABILITY},
		{correlation.PhaseBossBurst, battlev1.BattleInputKind_BATTLE_INPUT_KIND_PRIMARY_ABILITY},
	}
	for _, test := range tests {
		driver, err := New(test.phase)
		if err != nil {
			t.Fatalf("new %s driver: %v", test.phase, err)
		}
		bundle, nextErr := driver.NextInput(1)
		if nextErr != nil {
			t.Fatalf("phase=%s next input: %v", test.phase, nextErr)
		}
		if len(bundle.GetCommands()) != 1 ||
			bundle.GetCommands()[0].GetKind() != test.kind {
			t.Fatalf("phase=%s produced wrong input", test.phase)
		}
	}
	idle, err := New(correlation.PhaseDisconnectDrain)
	if err != nil {
		t.Fatalf("new idle driver: %v", err)
	}
	if bundle, nextErr := idle.NextInput(1); nextErr != nil || bundle != nil {
		t.Fatal("disconnect-drain emitted an input")
	}
}

// TestDriverProbeSequenceIsMonotonic 验证 probe 只包含低敏观测进度。
func TestDriverProbeSequenceIsMonotonic(t *testing.T) {
	driver, err := New(correlation.PhaseClean)
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	first := driver.NextProbe(Snapshot{LatestSnapshotSequence: 9}, 100)
	second := driver.NextProbe(Snapshot{LatestSnapshotSequence: 10}, 200)
	if first.GetProbeSequence() != 1 || second.GetProbeSequence() != 2 ||
		second.GetLatestSnapshotSequence() != 10 ||
		second.GetClientMonotonicTimeUs() != 200 {
		t.Fatal("probe sequence or observation drifted")
	}
}

// TestDriverTransitionPreservesSessionSequence 验证 phase 切换不重置 session 内 command identity。
func TestDriverTransitionPreservesSessionSequence(t *testing.T) {
	driver, err := New(correlation.PhaseClean)
	if err != nil {
		t.Fatal(err)
	}
	first, err := driver.NextInput(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.Transition(correlation.PhaseBossBurst); err != nil {
		t.Fatal(err)
	}
	second, err := driver.NextInput(1)
	if err != nil {
		t.Fatal(err)
	}
	firstSequence := first.GetCommands()[len(first.GetCommands())-1].GetCommandSequence()
	secondCommand := second.GetCommands()[len(second.GetCommands())-1]
	if secondCommand.GetCommandSequence() != firstSequence+1 ||
		secondCommand.GetKind() !=
			battlev1.BattleInputKind_BATTLE_INPUT_KIND_SECONDARY_ABILITY {
		t.Fatal("workload transition reset sequence or missed phase")
	}
}

// TestDriverAdvancesInputClockAcrossGap 验证 disconnect-drain 不发送但不会复用旧 InputTick。
func TestDriverAdvancesInputClockAcrossGap(t *testing.T) {
	driver, err := New(correlation.PhaseClean)
	if err != nil {
		t.Fatal(err)
	}
	first, err := driver.NextInput(10)
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.Transition(correlation.PhaseDisconnectDrain); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if bundle, nextErr := driver.NextInput(10); nextErr != nil || bundle != nil {
			t.Fatalf("gap bundle=%v err=%v", bundle, nextErr)
		}
	}
	if err := driver.Transition(correlation.PhaseClean); err != nil {
		t.Fatal(err)
	}
	successor, err := driver.NextInput(11)
	if err != nil {
		t.Fatal(err)
	}
	if first.GetNewestInputTick() != 19 ||
		successor.GetNewestInputTick() != 22 {
		t.Fatalf(
			"input clock first=%d successor=%d",
			first.GetNewestInputTick(),
			successor.GetNewestInputTick(),
		)
	}
}
