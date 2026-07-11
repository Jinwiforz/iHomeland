package app

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"

	"github.com/jinwiforz/ihomeland/server/internal/observability"
)

// TaskFailure 描述一个已登记长生命周期任务的致命退出。
type TaskFailure struct {
	// Task 是由 owner 与局部名称组成的稳定低基数标识。
	Task string
	// Kind 区分 error 与 panic，供恢复和指标使用。
	Kind string
	// Err 保留内部原因，不直接编码到客户端响应。
	Err error
	// Stack 只在 panic 时保存内部诊断 stack。
	Stack []byte
}

// Error 返回不包含 stack 的稳定任务诊断。
func (failure *TaskFailure) Error() string {
	return fmt.Sprintf("task %s failed with %s: %v", failure.Task, failure.Kind, failure.Err)
}

// Unwrap 暴露底层原因供 errors.Is/As 使用。
func (failure *TaskFailure) Unwrap() error { return failure.Err }

// TaskGroup 登记全部长生命周期任务，并向 Composition Root 传播首个致命失败。
type TaskGroup struct {
	// metrics 记录所有失败，即使 failures 通道已保存首个原因。
	metrics *observability.Metrics
	// failures 向 Composition Root 非阻塞传播首个致命失败。
	failures chan *TaskFailure

	// mu 保护全局任务名集合。
	mu sync.Mutex
	// names 防止日志和指标中的任务身份发生碰撞。
	names map[string]struct{}
	// wg 允许最终确认全部 owner task 已退出。
	wg sync.WaitGroup
}

// TaskOwner 持有一个 component 或 root task 集合的取消与等待边界。
type TaskOwner struct {
	// group 是全局登记和失败传播 owner。
	group *TaskGroup
	// name 是 component/root 的稳定前缀。
	name string
	// ctx 只由该 owner 取消，保证逆序关闭语义。
	ctx context.Context
	// cancel 记录关闭 cause 并广播给 owner tasks。
	cancel context.CancelCauseFunc

	// mu 防止 Stop 与新增 task 竞态。
	mu sync.Mutex
	// stopped 阻止关闭后继续创建 goroutine。
	stopped bool
	// wg 只等待当前 owner 的任务。
	wg sync.WaitGroup
}

// NewTaskGroup 创建不启动 goroutine 的任务登记表。
func NewTaskGroup(metrics *observability.Metrics) *TaskGroup {
	return &TaskGroup{metrics: metrics, failures: make(chan *TaskFailure, 1), names: make(map[string]struct{})}
}

// NewOwner 为明确 owner 创建 child context；owner 名称必须符合 component 命名规则。
func (group *TaskGroup) NewOwner(parent context.Context, name string) (*TaskOwner, error) {
	if !componentNamePattern.MatchString(name) {
		return nil, errors.New("task owner has invalid name")
	}
	ctx, cancel := context.WithCancelCause(parent)
	return &TaskOwner{group: group, name: name, ctx: ctx, cancel: cancel}, nil
}

// Failures 返回只读首个致命失败通道；后续失败仍计入 metrics 但不会阻塞任务退出。
func (group *TaskGroup) Failures() <-chan *TaskFailure { return group.failures }

// Wait 等待全部已登记任务退出或调用方 deadline 到期。
func (group *TaskGroup) Wait(ctx context.Context) error {
	done := make(chan struct{})
	// WaitGroup 无 context API；单次 waiter 只在全部任务结束时退出，deadline 只限制调用方等待时间。
	go func() {
		group.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for supervised tasks: %w", context.Cause(ctx))
	}
}

// Go 登记并启动一个 owner task；同一进程中的完整任务名必须唯一。
func (owner *TaskOwner) Go(name string, task func(context.Context) error) error {
	if !componentNamePattern.MatchString(name) || task == nil {
		return errors.New("task requires a valid name and function")
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.stopped {
		return errors.New("task owner is stopped")
	}
	fullName := owner.name + "." + name
	owner.group.mu.Lock()
	if _, exists := owner.group.names[fullName]; exists {
		owner.group.mu.Unlock()
		return fmt.Errorf("duplicate task %s", fullName)
	}
	owner.group.names[fullName] = struct{}{}
	owner.group.mu.Unlock()

	owner.wg.Add(1)
	owner.group.wg.Add(1)
	go owner.run(fullName, task)
	return nil
}

// Stop 取消 owner context 并等待其全部任务，不影响其他 component owner。
func (owner *TaskOwner) Stop(ctx context.Context, cause error) error {
	owner.mu.Lock()
	if !owner.stopped {
		owner.stopped = true
		owner.cancel(cause)
	}
	owner.mu.Unlock()
	done := make(chan struct{})
	// owner 取消后使用独立 waiter 桥接 WaitGroup 与共享 shutdown deadline，不轮询任务状态。
	go func() {
		owner.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for task owner %s: %w", owner.name, context.Cause(ctx))
	}
}

// run 包装任务完成计数、error 过滤和 panic 恢复，不改变 task 的业务返回值。
func (owner *TaskOwner) run(fullName string, task func(context.Context) error) {
	defer owner.wg.Done()
	defer owner.group.wg.Done()
	defer func() {
		if recovered := recover(); recovered != nil {
			owner.report(&TaskFailure{Task: fullName, Kind: "panic", Err: fmt.Errorf("%v", recovered), Stack: debug.Stack()})
		}
	}()
	if err := task(owner.ctx); err != nil && owner.ctx.Err() == nil && !errors.Is(err, context.Canceled) {
		owner.report(&TaskFailure{Task: fullName, Kind: "error", Err: err})
	}
}

// report 保证失败不会阻塞任务退出，同时让每次失败都进入 metrics。
func (owner *TaskOwner) report(failure *TaskFailure) {
	owner.group.metrics.RecordTaskFailure(failure.Task, failure.Kind)
	select {
	case owner.group.failures <- failure:
	default:
	}
}
