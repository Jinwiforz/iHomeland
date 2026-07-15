package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestSemanticDeadlineOwnerOrdersAndReplaces 验证单worker排序、同key替换与稳定command identity。
func TestSemanticDeadlineOwnerOrdersAndReplaces(t *testing.T) {
	t.Parallel()
	clock := newManualSemanticClock(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC))
	owner, _ := newSemanticDeadlineOwner(4, clock)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- owner.Run(ctx) }()
	results := make(chan string, 3)
	keyA := semanticDeadlineKey{kind: semanticDeadlineInvite, scope: "visit_alpha", target: "invite_alpha"}
	keyB := semanticDeadlineKey{kind: semanticDeadlineVisitSession, scope: "visit_beta"}
	callback := func(name string) func(context.Context, semanticDeadlineTask) error {
		return func(_ context.Context, task semanticDeadlineTask) error {
			if _, err := task.visitCommandID(); err != nil {
				return err
			}
			results <- name
			return nil
		}
	}
	if err := owner.Schedule(semanticDeadlineTask{key: keyA, deadline: clock.Now().Add(3 * time.Second), conditionDeadline: clock.Now().Add(3 * time.Second), revision: 1, execute: callback("old")}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Schedule(semanticDeadlineTask{key: keyB, deadline: clock.Now().Add(2 * time.Second), conditionDeadline: clock.Now().Add(2 * time.Second), revision: 1, execute: callback("first")}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Schedule(semanticDeadlineTask{key: keyA, deadline: clock.Now().Add(time.Second), conditionDeadline: clock.Now().Add(time.Second), revision: 2, execute: callback("replacement")}); err != nil {
		t.Fatal(err)
	}
	if owner.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", owner.Len())
	}
	clock.Advance(time.Second)
	if got := receiveDeadlineResult(t, results); got != "replacement" {
		t.Fatalf("first result = %q", got)
	}
	clock.Advance(time.Second)
	if got := receiveDeadlineResult(t, results); got != "first" {
		t.Fatalf("second result = %q", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

// TestSemanticDeadlineOwnerCapacityCancelAndClose 验证queue边界、cancel与terminal close。
func TestSemanticDeadlineOwnerCapacityCancelAndClose(t *testing.T) {
	t.Parallel()
	clock := newManualSemanticClock(time.Now().UTC())
	owner, _ := newSemanticDeadlineOwner(1, clock)
	firstKey := semanticDeadlineKey{kind: semanticDeadlineAssignmentRenew, scope: "winst_alpha"}
	secondKey := semanticDeadlineKey{kind: semanticDeadlineAssignmentExpiry, scope: "winst_beta"}
	callback := func(context.Context, semanticDeadlineTask) error { return nil }
	if err := owner.Schedule(semanticDeadlineTask{key: firstKey, deadline: clock.Now().Add(time.Hour), execute: callback}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Schedule(semanticDeadlineTask{key: secondKey, deadline: clock.Now().Add(time.Hour), execute: callback}); err == nil {
		t.Fatal("capacity exhaustion unexpectedly succeeded")
	}
	owner.Cancel(firstKey)
	if owner.Len() != 0 {
		t.Fatalf("Len() after cancel = %d", owner.Len())
	}
	done := make(chan error, 1)
	go func() { done <- owner.Run(context.Background()) }()
	startDeadline := time.Now().Add(time.Second)
	for {
		owner.mutex.Lock()
		running := owner.running
		owner.mutex.Unlock()
		if running {
			break
		}
		if time.Now().After(startDeadline) {
			t.Fatal("deadline worker did not start")
		}
		time.Sleep(time.Millisecond)
	}
	owner.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() after Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() did not stop deadline worker")
	}
	if err := owner.Schedule(semanticDeadlineTask{key: firstKey, deadline: clock.Now().Add(time.Hour), execute: callback}); err == nil {
		t.Fatal("closed owner unexpectedly scheduled task")
	}
}

// TestSemanticDeadlineOwnerPropagatesCallbackFailure 验证callback错误交给TaskOwner监督而非静默吞掉。
func TestSemanticDeadlineOwnerPropagatesCallbackFailure(t *testing.T) {
	t.Parallel()
	clock := newManualSemanticClock(time.Now().UTC())
	owner, _ := newSemanticDeadlineOwner(1, clock)
	want := errors.New("deadline callback failed")
	key := semanticDeadlineKey{kind: semanticDeadlineAssignmentRenew, scope: "winst_failure"}
	if err := owner.Schedule(semanticDeadlineTask{key: key, deadline: clock.Now(), execute: func(context.Context, semanticDeadlineTask) error { return want }}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Run(context.Background()); !errors.Is(err, want) {
		t.Fatalf("Run() error = %v", err)
	}
}

// TestSemanticDeadlineOwnerTreatsCancellationAsCleanStop 验证 draining 取消正在执行的 callback 不会伪报 worker failure。
func TestSemanticDeadlineOwnerTreatsCancellationAsCleanStop(t *testing.T) {
	t.Parallel()
	clock := newManualSemanticClock(time.Now().UTC())
	owner, _ := newSemanticDeadlineOwner(1, clock)
	started := make(chan struct{})
	key := semanticDeadlineKey{kind: semanticDeadlineAssignmentRenew, scope: "winst_cancel"}
	if err := owner.Schedule(semanticDeadlineTask{key: key, deadline: clock.Now(), execute: func(ctx context.Context, _ semanticDeadlineTask) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- owner.Run(ctx) }()
	<-started
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() cancellation error = %v", err)
	}
}

// receiveDeadlineResult 有界等待manual clock触发的worker结果。
func receiveDeadlineResult(t *testing.T, results <-chan string) string {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(time.Second):
		t.Fatal("deadline worker did not execute")
		return ""
	}
}

// manualSemanticClock 同步推进所有当前timer，测试不会依赖wall clock排序。
type manualSemanticClock struct {
	// mutex 保护测试时间与 timer 集合。
	mutex sync.Mutex
	// now 是由测试显式推进的当前时间。
	now time.Time
	// timers 保存尚未触发的一次性 timer。
	timers map[*manualSemanticTimer]struct{}
}

// newManualSemanticClock 创建无后台goroutine的fake clock。
func newManualSemanticClock(now time.Time) *manualSemanticClock {
	return &manualSemanticClock{now: now, timers: make(map[*manualSemanticTimer]struct{})}
}

// Now 返回由测试推进的绝对时间。
func (clock *manualSemanticClock) Now() time.Time {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	return clock.now
}

// NewTimer 登记一个由 Advance 同步触发的一次性 timer。
func (clock *manualSemanticClock) NewTimer(duration time.Duration) semanticTimer {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	timer := &manualSemanticTimer{clock: clock, deadline: clock.now.Add(duration), channel: make(chan time.Time, 1)}
	clock.timers[timer] = struct{}{}
	return timer
}

// Advance 推进绝对时间并一次性触发所有达到deadline的active timer。
func (clock *manualSemanticClock) Advance(duration time.Duration) {
	clock.mutex.Lock()
	clock.now = clock.now.Add(duration)
	for timer := range clock.timers {
		if !timer.stopped && !timer.deadline.After(clock.now) {
			timer.stopped = true
			delete(clock.timers, timer)
			timer.channel <- clock.now
		}
	}
	clock.mutex.Unlock()
}

// manualSemanticTimer 是fake clock拥有的一次性timer。
type manualSemanticTimer struct {
	// clock 拥有本 timer 的生命周期。
	clock *manualSemanticClock
	// deadline 是 fake clock 的绝对触发时刻。
	deadline time.Time
	// channel 最多缓冲一次触发结果。
	channel chan time.Time
	// stopped 防止重复触发或删除。
	stopped bool
}

// C 返回测试 timer 的只读触发通道。
func (timer *manualSemanticTimer) C() <-chan time.Time { return timer.channel }

// Stop 从 fake clock 移除尚未触发的 timer。
func (timer *manualSemanticTimer) Stop() bool {
	timer.clock.mutex.Lock()
	defer timer.clock.mutex.Unlock()
	if timer.stopped {
		return false
	}
	timer.stopped = true
	delete(timer.clock.timers, timer)
	return true
}
