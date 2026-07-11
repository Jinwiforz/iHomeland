// Package app 组装服务端唯一 Composition Root 并治理进程生命周期。
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sync"

	"github.com/jinwiforz/ihomeland/server/internal/observability"
)

// componentNamePattern 同时限制日志、metrics 与 task owner 使用低基数稳定名称。
var componentNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Component 只由持有外部资源或长生命周期任务的 concrete component 实现。
//
// Start 成功后组件才进入关闭栈；Stop 必须幂等处理自身资源，但 Runner 对每个实例最多调用一次。
type Component interface {
	// Name 返回用于日志、指标和失败诊断的稳定低基数名称。
	Name() string
	// Start 获取资源并在可服务后返回；失败时组件负责释放尚未完成所有权转移的局部资源。
	Start(context.Context) error
	// Stop 停止接收新工作、取消自身任务并在共享 deadline 内释放资源。
	Stop(context.Context) error
}

// Lifecycle 按依赖顺序启动组件，并以唯一 started stack 决定逆序关闭。
type Lifecycle struct {
	// components 保留显式依赖顺序，调用方后续修改输入 slice 不会改变运行图。
	components []Component
	// clock 提供可测试的耗时观测。
	clock Clock
	// logger 记录稳定 component 生命周期结果。
	logger *slog.Logger
	// metrics 记录低基数启动和停止耗时。
	metrics *observability.Metrics

	// mu 保护 started stack 与并发 Stop 共享结果。
	mu sync.Mutex
	// started 是唯一关闭顺序源，只包含 Start 成功的组件。
	started []Component
	// startCalled 防止重复获取资源。
	startCalled bool
	// stopStarted 选出唯一执行关闭的调用者。
	stopStarted bool
	// stopDone 广播首个关闭调用完成。
	stopDone chan struct{}
	// stopErr 保存所有并发调用共享的聚合结果。
	stopErr error
}

// NewLifecycle 验证组件名称和唯一性后创建 runner，不启动任何资源。
func NewLifecycle(components []Component, clock Clock, logger *slog.Logger, metrics *observability.Metrics) (*Lifecycle, error) {
	seen := make(map[string]struct{}, len(components))
	owned := append([]Component(nil), components...)
	for index, component := range owned {
		if component == nil {
			return nil, fmt.Errorf("lifecycle component %d is nil", index)
		}
		name := component.Name()
		if !componentNamePattern.MatchString(name) {
			return nil, fmt.Errorf("lifecycle component %d has invalid name", index)
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("duplicate lifecycle component %s", name)
		}
		seen[name] = struct{}{}
	}
	return &Lifecycle{components: owned, clock: clock, logger: logger, metrics: metrics, stopDone: make(chan struct{})}, nil
}

// Start 顺序启动全部组件，并在每次成功后立即登记关闭所有权。
//
// 发生错误时已成功组件仍保留在 started stack，调用方必须使用独立 shutdown context 调用 Stop 回滚。
func (lifecycle *Lifecycle) Start(ctx context.Context) error {
	lifecycle.mu.Lock()
	if lifecycle.startCalled {
		lifecycle.mu.Unlock()
		return errors.New("lifecycle start may be called only once")
	}
	lifecycle.startCalled = true
	lifecycle.mu.Unlock()

	for _, component := range lifecycle.components {
		startedAt := lifecycle.clock.Now()
		if err := component.Start(ctx); err != nil {
			lifecycle.metrics.ObserveLifecycle("start", component.Name(), lifecycle.clock.Now().Sub(startedAt).Seconds())
			return fmt.Errorf("start component %s: %w", component.Name(), err)
		}
		lifecycle.metrics.ObserveLifecycle("start", component.Name(), lifecycle.clock.Now().Sub(startedAt).Seconds())
		lifecycle.mu.Lock()
		lifecycle.started = append(lifecycle.started, component)
		lifecycle.mu.Unlock()
		lifecycle.logger.Info("lifecycle component started", "component", component.Name(), "operation", "start")
	}
	return nil
}

// Stop 让第一个调用者执行逆序关闭，其他并发调用者只等待同一结果。
//
// ctx 是所有组件共享的总预算；即使一个 Stop 失败，runner 仍继续尝试后续资源并使用 errors.Join 聚合原因。
func (lifecycle *Lifecycle) Stop(ctx context.Context) error {
	lifecycle.mu.Lock()
	if lifecycle.stopStarted {
		done := lifecycle.stopDone
		lifecycle.mu.Unlock()
		select {
		case <-done:
			lifecycle.mu.Lock()
			err := lifecycle.stopErr
			lifecycle.mu.Unlock()
			return err
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	lifecycle.stopStarted = true
	started := append([]Component(nil), lifecycle.started...)
	lifecycle.mu.Unlock()

	var failures []error
	for index := len(started) - 1; index >= 0; index-- {
		component := started[index]
		stoppedAt := lifecycle.clock.Now()
		err := component.Stop(ctx)
		lifecycle.metrics.ObserveLifecycle("stop", component.Name(), lifecycle.clock.Now().Sub(stoppedAt).Seconds())
		if err != nil {
			wrapped := fmt.Errorf("stop component %s: %w", component.Name(), err)
			failures = append(failures, wrapped)
			lifecycle.logger.Error("lifecycle component stop failed", "component", component.Name(), "operation", "stop", "error", err)
		} else {
			lifecycle.logger.Info("lifecycle component stopped", "component", component.Name(), "operation", "stop")
		}
	}
	result := errors.Join(failures...)
	lifecycle.mu.Lock()
	lifecycle.stopErr = result
	close(lifecycle.stopDone)
	lifecycle.mu.Unlock()
	return result
}
