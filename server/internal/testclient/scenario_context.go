package testclient

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Resource 是资格场景拥有且必须显式关闭的网络或临时资源。
type Resource interface {
	// Close 释放资源；实现必须允许场景 cleanup 重复调用。
	Close() error
}

// ScenarioContext 拥有一个场景的取消、actor 与逆序资源清理生命周期。
type ScenarioContext struct {
	// Context 传播全局/场景 deadline，不携带 credential。
	context.Context
	// cancel 在 cleanup 开始时停止所有仍在等待的 operation。
	cancel context.CancelCauseFunc
	// mutex 保护 actor、资源栈与 closed 状态。
	mutex sync.Mutex
	// actors 按低敏 label 保持场景内唯一 actor context。
	actors map[string]*Actor
	// resources 按创建顺序登记，关闭时严格逆序执行。
	resources []Resource
	// closed 阻止 cleanup 后重新登记资源。
	closed bool
}

// NewScenarioContext 创建继承调用方 deadline 的多 actor 场景 owner。
func NewScenarioContext(parent context.Context) (*ScenarioContext, error) {
	if parent == nil {
		return nil, errors.New("qualification scenario parent context is nil")
	}
	ctx, cancel := context.WithCancelCause(parent)
	return &ScenarioContext{Context: ctx, cancel: cancel, actors: make(map[string]*Actor)}, nil
}

// AddActor 创建并登记一个 credential 隔离的 actor。
func (scenario *ScenarioContext) AddActor() (*Actor, error) {
	if scenario == nil {
		return nil, errors.New("qualification scenario context is nil")
	}
	actor, err := NewActor()
	if err != nil {
		return nil, err
	}
	scenario.mutex.Lock()
	defer scenario.mutex.Unlock()
	if scenario.closed {
		return nil, errors.New("qualification scenario is closed")
	}
	if _, exists := scenario.actors[actor.Label]; exists {
		return nil, errors.New("qualification actor label collision")
	}
	scenario.actors[actor.Label] = actor
	return actor, nil
}

// Track 把已创建资源转移给场景；失败时调用方仍拥有并必须关闭资源。
func (scenario *ScenarioContext) Track(resource Resource) error {
	if scenario == nil || resource == nil {
		return errors.New("qualification scenario resource is invalid")
	}
	scenario.mutex.Lock()
	defer scenario.mutex.Unlock()
	if scenario.closed {
		return errors.New("qualification scenario is closed")
	}
	scenario.resources = append(scenario.resources, resource)
	return nil
}

// Close 先取消 operation，再逆序关闭资源，最后清除全部 actor credential。
func (scenario *ScenarioContext) Close() error {
	if scenario == nil {
		return nil
	}
	scenario.mutex.Lock()
	if scenario.closed {
		scenario.mutex.Unlock()
		return nil
	}
	scenario.closed = true
	resources := append([]Resource(nil), scenario.resources...)
	actors := make([]*Actor, 0, len(scenario.actors))
	for _, actor := range scenario.actors {
		actors = append(actors, actor)
	}
	scenario.resources = nil
	scenario.actors = nil
	scenario.mutex.Unlock()
	scenario.cancel(errors.New("qualification scenario cleanup"))
	var cleanupErr error
	for index := len(resources) - 1; index >= 0; index-- {
		if err := resources[index].Close(); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("close qualification resource %d: %w", index, err))
		}
	}
	for _, actor := range actors {
		actor.ClearCredentials()
	}
	return cleanupErr
}

// closeScenario 把延迟 cleanup 失败并入场景结果，避免通过 defer 静默丢失资源关闭错误。
func closeScenario(scenario *ScenarioContext, result *error) {
	if result == nil {
		return
	}
	*result = errors.Join(*result, scenario.Close())
}

// TCPHandle 标记一个 actor gameplay target 的本地代际。
// 异步回调写入状态前必须用 ActorConnections.IsCurrentTCP 复核。
type TCPHandle struct {
	// generation 在每次 replacement 时严格增加。
	generation uint64
	// client 是该代际唯一 gameplay connection。
	client *TCPClient
}

// Client 返回 handle 对应的 gameplay client。
func (handle TCPHandle) Client() *TCPClient { return handle.client }

// ActorConnections 用本地代际屏障防止旧 connection callback 覆盖新 target。
type ActorConnections struct {
	// mutex 保护 current target 与 generation。
	mutex sync.Mutex
	// tcpGeneration 是最近一次 replacement 分配的代际。
	tcpGeneration uint64
	// tcp 是当前 gameplay target；nil 表示未连接。
	tcp *TCPClient
}

// ReplaceTCP 先发布新代际并移除旧 target，再关闭旧 connection。
func (connections *ActorConnections) ReplaceTCP(client *TCPClient) TCPHandle {
	if connections == nil {
		return TCPHandle{}
	}
	connections.mutex.Lock()
	previous := connections.tcp
	connections.tcpGeneration++
	connections.tcp = client
	handle := TCPHandle{generation: connections.tcpGeneration, client: client}
	connections.mutex.Unlock()
	if previous != nil && previous != client {
		_ = previous.Close()
	}
	return handle
}

// IsCurrentTCP 报告异步结果是否仍属于当前 target 的同一代际。
func (connections *ActorConnections) IsCurrentTCP(handle TCPHandle) bool {
	if connections == nil || handle.client == nil {
		return false
	}
	connections.mutex.Lock()
	defer connections.mutex.Unlock()
	return connections.tcp == handle.client && connections.tcpGeneration == handle.generation
}

// Close 移除 current target、递增代际并关闭最后一条 gameplay connection。
func (connections *ActorConnections) Close() error {
	if connections == nil {
		return nil
	}
	connections.mutex.Lock()
	current := connections.tcp
	connections.tcp = nil
	connections.tcpGeneration++
	connections.mutex.Unlock()
	if current == nil {
		return nil
	}
	return current.Close()
}
