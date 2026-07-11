package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/config"
	"github.com/jinwiforz/ihomeland/server/internal/observability"
)

// panicOnCancelComponent 模拟只在正常关闭取消后 panic 的后台任务。
type panicOnCancelComponent struct {
	// owner 保证 panic 发生在 component Stop 的等待窗口内。
	owner *TaskOwner
}

// Name 返回测试使用的稳定 component 名。
func (*panicOnCancelComponent) Name() string { return "worker" }

// Start 登记等待取消的任务，证明启动阶段本身可以成功。
func (component *panicOnCancelComponent) Start(context.Context) error {
	return component.owner.Go("run", func(ctx context.Context) error {
		<-ctx.Done()
		panic("panic during shutdown")
	})
}

// Stop 触发任务取消并等待 panic 被任务边界转换为失败。
func (component *panicOnCancelComponent) Stop(ctx context.Context) error {
	return component.owner.Stop(ctx, errors.New("component stopped"))
}

// TestTaskGroupPropagatesErrorAndPanic 验证异常任务无法静默退出或留下 ready 进程。
func TestTaskGroupPropagatesErrorAndPanic(t *testing.T) {
	for name, task := range map[string]func(context.Context) error{
		"error": func(context.Context) error { return errors.New("failed") },
		"panic": func(context.Context) error { panic("failed") },
	} {
		t.Run(name, func(t *testing.T) {
			group := NewTaskGroup(observability.NewMetrics())
			owner, err := group.NewOwner(context.Background(), "worker")
			if err != nil {
				t.Fatal(err)
			}
			if err := owner.Go("run", task); err != nil {
				t.Fatal(err)
			}
			select {
			case failure := <-group.Failures():
				if failure.Kind != name || (name == "panic" && len(failure.Stack) == 0) {
					t.Fatalf("unexpected task failure: %+v", failure)
				}
			case <-time.After(time.Second):
				t.Fatal("task failure was not propagated")
			}
			if err := owner.Stop(context.Background(), errors.New("test complete")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestTaskOwnerCancellationIsNotFailure 验证 component 正常 Stop 不产生运行时致命事件。
func TestTaskOwnerCancellationIsNotFailure(t *testing.T) {
	group := NewTaskGroup(observability.NewMetrics())
	owner, err := group.NewOwner(context.Background(), "worker")
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Go("run", func(ctx context.Context) error {
		<-ctx.Done()
		return context.Cause(ctx)
	}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Stop(context.Background(), errors.New("normal stop")); err != nil {
		t.Fatal(err)
	}
	select {
	case failure := <-group.Failures():
		t.Fatalf("normal cancellation reported failure: %v", failure)
	default:
	}
}

// TestTaskOwnerHonorsWaitDeadline 暴露忽略取消的任务名并避免 Stop 无限等待。
func TestTaskOwnerHonorsWaitDeadline(t *testing.T) {
	group := NewTaskGroup(observability.NewMetrics())
	owner, err := group.NewOwner(context.Background(), "worker")
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	if err := owner.Go("blocked", func(context.Context) error { <-release; return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := owner.Stop(ctx, errors.New("stop")); err == nil {
		t.Fatal("uncooperative task should exceed deadline")
	}
	close(release)
}

// TestFinishPreservesTaskPanicDuringShutdown 验证关闭窗口中的 panic 仍产生 runtime 非零结果。
func TestFinishPreservesTaskPanicDuringShutdown(t *testing.T) {
	metrics := observability.NewMetrics()
	tasks := NewTaskGroup(metrics)
	componentContext, cancelComponents := context.WithCancelCause(context.Background())
	defer cancelComponents(errors.New("test complete"))
	owner, err := tasks.NewOwner(componentContext, "worker")
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := newTestLifecycle(t, []Component{&panicOnCancelComponent{owner: owner}})
	if err := lifecycle.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	readiness := NewReadiness()
	if err := readiness.MarkReady(); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	result := finish(config.Default(), lifecycle, tasks, readiness, cancelComponents, logger, metrics, "signal", nil)
	if result.Kind != ResultRuntimeError {
		t.Fatalf("shutdown panic returned %s: %v", result.Kind, result.Err)
	}
	var failure *TaskFailure
	if !errors.As(result.Err, &failure) || failure.Kind != "panic" {
		t.Fatalf("shutdown panic cause was not preserved: %v", result.Err)
	}
}
