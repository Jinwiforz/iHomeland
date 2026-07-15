package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
)

// TestProcessWorldRuntimeLifecycle 验证runtime按完整stamp幂等登记并拒绝stale stop。
func TestProcessWorldRuntimeLifecycle(t *testing.T) {
	t.Parallel()
	node, _ := placement.NewRuntimeNodeID("rnode_runtimetest")
	runtime, err := newProcessWorldRuntime(node, 2, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	first := runtimeAssignment(t, "alpha", node, 1)
	if err := runtime.Start(context.Background(), first); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := runtime.Start(context.Background(), first); err != nil || runtime.count() != 1 {
		t.Fatalf("idempotent Start() error = %v count = %d", err, runtime.count())
	}
	otherNode, _ := placement.NewRuntimeNodeID("rnode_othertest")
	foreign := runtimeAssignment(t, "foreign", otherNode, 1)
	if err := runtime.Start(context.Background(), foreign); err == nil {
		t.Fatal("foreign assignment unexpectedly started")
	}
	if err := runtime.Stop(context.Background(), first.Stamp()); err != nil || runtime.count() != 0 {
		t.Fatalf("Stop() error = %v count = %d", err, runtime.count())
	}
	if err := runtime.Stop(context.Background(), first.Stamp()); err != nil {
		t.Fatalf("idempotent Stop() error = %v", err)
	}
}

// TestProcessWorldRuntimeCapacityAndClose 验证capacity与terminal close不会泄漏runtime。
func TestProcessWorldRuntimeCapacityAndClose(t *testing.T) {
	t.Parallel()
	node, _ := placement.NewRuntimeNodeID("rnode_capacitytest")
	runtime, _ := newProcessWorldRuntime(node, 1, func() bool { return true })
	first := runtimeAssignment(t, "one", node, 1)
	second := runtimeAssignment(t, "two", node, 1)
	if err := runtime.Start(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background(), second); err == nil {
		t.Fatal("capacity exhaustion unexpectedly succeeded")
	}
	runtime.Close()
	if runtime.count() != 0 || runtime.contains(first.Stamp()) {
		t.Fatal("Close() retained runtime")
	}
	if err := runtime.Start(context.Background(), first); err == nil {
		t.Fatal("closed runtime unexpectedly restarted")
	}
}

// TestProcessWorldRuntimeConcurrentStart 验证并发重复Start只登记一个instance。
func TestProcessWorldRuntimeConcurrentStart(t *testing.T) {
	t.Parallel()
	node, _ := placement.NewRuntimeNodeID("rnode_concurrenttest")
	runtime, _ := newProcessWorldRuntime(node, 2, func() bool { return true })
	snapshot := runtimeAssignment(t, "same", node, 1)
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 16)
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsSeen <- runtime.Start(context.Background(), snapshot)
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("concurrent Start() error = %v", err)
		}
	}
	if runtime.count() != 1 {
		t.Fatalf("runtime count = %d, want 1", runtime.count())
	}
}

// runtimeAssignment 构造RuntimeController测试所需starting snapshot。
func runtimeAssignment(t *testing.T, suffix string, node placement.RuntimeNodeID, generationValue uint64) placement.AssignmentSnapshot {
	t.Helper()
	world, _ := personalworld.NewPersonalWorldID("pworld_runtime" + suffix)
	instance, _ := placement.NewWorldInstanceID("winst_runtime" + suffix)
	generation, _ := placement.NewAssignmentGeneration(generationValue)
	fence, _ := placement.NewFencingToken(generationValue)
	stamp, err := placement.NewAssignmentStamp(world, instance, node, generation, fence)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	snapshot, err := placement.NewAssignmentSnapshot(stamp, placement.PhaseStarting, now, now.Add(time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
