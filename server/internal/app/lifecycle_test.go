package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/observability"
)

// fakeComponent 记录 Start/Stop 顺序并可注入阶段错误。
type fakeComponent struct {
	// name 是满足低基数规则的组件名。
	name string
	// startErr 在 Start 阶段模拟初始化失败。
	startErr error
	// stopErr 在 Stop 阶段模拟释放失败。
	stopErr error
	// events 保存跨组件共享的调用顺序。
	events *[]string
	// mu 保护并发 Stop 测试写入 events。
	mu *sync.Mutex
}

// blockingComponent 一直等待共享关闭 context，用于验证总 deadline。
type blockingComponent struct{}

// Name 返回测试组件名。
func (*blockingComponent) Name() string { return "blocked" }

// Start 模拟成功获取资源。
func (*blockingComponent) Start(context.Context) error { return nil }

// Stop 只在总 deadline 结束时返回。
func (*blockingComponent) Stop(ctx context.Context) error {
	<-ctx.Done()
	return context.Cause(ctx)
}

// Name 返回 fake 的稳定名称。
func (component *fakeComponent) Name() string { return component.name }

// Start 记录调用后返回注入错误。
func (component *fakeComponent) Start(context.Context) error {
	component.record("start:" + component.name)
	return component.startErr
}

// Stop 记录调用后返回注入错误。
func (component *fakeComponent) Stop(context.Context) error {
	component.record("stop:" + component.name)
	return component.stopErr
}

// record 并发安全地追加生命周期事件。
func (component *fakeComponent) record(event string) {
	component.mu.Lock()
	*component.events = append(*component.events, event)
	component.mu.Unlock()
}

// TestLifecycleRollsBackOnlyStartedComponents 验证失败组件不会 Stop，成功组件严格逆序释放。
func TestLifecycleRollsBackOnlyStartedComponents(t *testing.T) {
	var events []string
	var mu sync.Mutex
	components := []Component{
		&fakeComponent{name: "first", events: &events, mu: &mu},
		&fakeComponent{name: "second", events: &events, mu: &mu, startErr: errors.New("start failed")},
		&fakeComponent{name: "third", events: &events, mu: &mu},
	}
	lifecycle := newTestLifecycle(t, components)
	if err := lifecycle.Start(context.Background()); err == nil {
		t.Fatal("startup failure should propagate")
	}
	if err := lifecycle.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	expected := []string{"start:first", "start:second", "stop:first"}
	if !equalStrings(events, expected) {
		t.Fatalf("unexpected lifecycle order: %v", events)
	}
}

// TestLifecycleAggregatesStopFailures 验证关闭失败不阻止剩余组件释放，并供并发调用共享结果。
func TestLifecycleAggregatesStopFailures(t *testing.T) {
	var events []string
	var mu sync.Mutex
	failure := errors.New("stop failed")
	lifecycle := newTestLifecycle(t, []Component{
		&fakeComponent{name: "first", events: &events, mu: &mu},
		&fakeComponent{name: "second", events: &events, mu: &mu, stopErr: failure},
	})
	if err := lifecycle.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() { results <- lifecycle.Stop(context.Background()) }()
	go func() { results <- lifecycle.Stop(context.Background()) }()
	for range 2 {
		if err := <-results; !errors.Is(err, failure) {
			t.Fatalf("concurrent stop lost error: %v", err)
		}
	}
	expected := []string{"start:first", "start:second", "stop:second", "stop:first"}
	if !equalStrings(events, expected) {
		t.Fatalf("unexpected lifecycle order: %v", events)
	}
}

// TestLifecycleSharesShutdownDeadline 确保失控组件不能让进程无限等待。
func TestLifecycleSharesShutdownDeadline(t *testing.T) {
	lifecycle := newTestLifecycle(t, []Component{new(blockingComponent)})
	if err := lifecycle.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := lifecycle.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown deadline was not propagated: %v", err)
	}
}

// newTestLifecycle 使用丢弃日志和进程私有 metrics 构建测试 runner。
func newTestLifecycle(t *testing.T, components []Component) *Lifecycle {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	lifecycle, err := NewLifecycle(components, SystemClock{}, logger, observability.NewMetrics())
	if err != nil {
		t.Fatal(err)
	}
	return lifecycle
}

// equalStrings 比较调用顺序且不隐藏重复或缺失事件。
func equalStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
