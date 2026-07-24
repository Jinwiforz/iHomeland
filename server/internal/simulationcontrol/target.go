package simulationcontrol

import (
	"context"
	"errors"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/placement"
)

// TargetClock 为 target point-in-time qualification 提供受信时间。
type TargetClock interface {
	// Now 返回当前绝对时间。
	Now() time.Time
}

// TargetResolver 每次同时读取 placement current 与 node registry，不缓存永久资格。
type TargetResolver struct {
	// current 是 placement current owner 的只读端口。
	current CurrentAssignmentReader
	// controller 提供 exact ready instance binding。
	controller *Controller
	// clock 为 lease 判断提供单次 UTC snapshot。
	clock TargetClock
}

// NewTargetResolver 校验 current、node registry 与 clock。
func NewTargetResolver(current CurrentAssignmentReader, controller *Controller, clock TargetClock) (*TargetResolver, error) {
	if current == nil || controller == nil || clock == nil {
		return nil, errors.New("simulation target resolver dependencies are invalid")
	}
	return &TargetResolver{current: current, controller: controller, clock: clock}, nil
}

// Resolve 只返回 current active、lease-valid、healthy、ready 的 exact target。
func (resolver *TargetResolver) Resolve(ctx context.Context, stamp placement.AssignmentStamp) (SimulationTarget, bool, error) {
	if resolver == nil || ctx == nil || !stamp.Valid() {
		return SimulationTarget{}, false, errors.New("simulation target resolve input is invalid")
	}
	now := resolver.clock.Now().UTC().Truncate(time.Microsecond)
	if now.IsZero() {
		return SimulationTarget{}, false, errors.New("simulation target clock returned zero")
	}
	if !resolver.controller.Healthy() {
		return SimulationTarget{}, false, nil
	}
	current, outcome, err := resolver.current.Resolve(ctx, stamp.WorldID(), now)
	if err != nil {
		return SimulationTarget{}, false, err
	}
	if outcome == placement.ResolveOutcomeNotFound {
		return SimulationTarget{}, false, nil
	}
	if outcome != placement.ResolveOutcomeFound {
		return SimulationTarget{}, false, errors.New("simulation target current outcome is invalid")
	}
	if !current.ValidAt(now) || current.Phase() != placement.PhaseActive ||
		!current.Stamp().Equal(stamp) {
		return SimulationTarget{}, false, nil
	}
	target, found := resolver.controller.ResolveTarget(stamp)
	return target, found, nil
}

// NodeSnapshot 是 selector/metrics 使用的低敏 node 状态。
type NodeSnapshot struct {
	// Healthy 表示 node 接受新 instance。
	Healthy bool
	// Draining 表示 node 已进入不可恢复关闭。
	Draining bool
	// InstanceCapacity 是 hello 后实际 hard slots。
	InstanceCapacity int
	// RunningInstances 是当前已登记 bindings。
	RunningInstances int
	// ActorCapacity 是每 instance qualified cap。
	ActorCapacity int
}

// Snapshot 返回不含 identity、stamp、nonce 或 path 的 node 状态。
func (controller *Controller) Snapshot() NodeSnapshot {
	if controller == nil {
		return NodeSnapshot{Draining: true}
	}
	healthy := controller.Healthy()
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	return NodeSnapshot{
		Healthy:          healthy,
		Draining:         !healthy,
		InstanceCapacity: controller.config.Capacity.Instances,
		RunningInstances: len(controller.bindings) + len(controller.pendingStarts),
		ActorCapacity:    controller.config.Capacity.Actors,
	}
}

// SelectCapacity 报告单 node 是否可承载指定 actor 数；C++ 自报值只能缩小配置。
func (controller *Controller) SelectCapacity(actorCount int) bool {
	snapshot := controller.Snapshot()
	return snapshot.Healthy && !snapshot.Draining &&
		actorCount >= 1 && actorCount <= snapshot.ActorCapacity &&
		snapshot.RunningInstances < snapshot.InstanceCapacity
}
