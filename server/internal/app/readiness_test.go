package app

import (
	"sync"
	"testing"
)

// TestReadinessStateMachine 验证 readiness 只能按启动到关闭方向迁移。
func TestReadinessStateMachine(t *testing.T) {
	readiness := NewReadiness()
	if state, ready := readiness.DiagnosticState(); state != "starting" || ready {
		t.Fatalf("unexpected initial state: %s %v", state, ready)
	}
	if err := readiness.MarkReady(); err != nil {
		t.Fatal(err)
	}
	if err := readiness.MarkReady(); err == nil {
		t.Fatal("ready transition must not repeat")
	}
	if err := readiness.BeginDraining(); err != nil {
		t.Fatal(err)
	}
	if err := readiness.BeginDraining(); err != nil {
		t.Fatal("draining transition should be idempotent")
	}
	if err := readiness.MarkStopped(); err != nil {
		t.Fatal(err)
	}
	if err := readiness.MarkReady(); err == nil {
		t.Fatal("stopped readiness must not become ready")
	}
}

// TestReadinessConcurrentDrain 确保多个关闭触发只产生一个 draining 状态且无数据竞态。
func TestReadinessConcurrentDrain(t *testing.T) {
	readiness := NewReadiness()
	if err := readiness.MarkReady(); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for range 32 {
		wait.Go(func() {
			if err := readiness.BeginDraining(); err != nil {
				t.Errorf("begin draining: %v", err)
			}
		})
	}
	wait.Wait()
	if readiness.State() != ReadinessDraining {
		t.Fatalf("unexpected final state: %s", readiness.State())
	}
}
