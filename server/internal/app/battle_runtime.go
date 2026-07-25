package app

import (
	"context"
	"errors"
	"sync"

	"github.com/jinwiforz/ihomeland/server/internal/battleentry"
	"github.com/jinwiforz/ihomeland/server/internal/battleticket"
	"github.com/jinwiforz/ihomeland/server/internal/config"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/simulationcontrol"
)

// battleEndpointProvider 只发布已完成 child hello 的唯一配置 endpoint。
type battleEndpointProvider struct {
	// component 证明 exact child/control 已 ready。
	component *simulationNodeComponent
	// endpoint 是配置验证后的 client-visible UDP 地址。
	endpoint battleticket.Endpoint
}

// EndpointFor 拒绝旧 child target、未启动 child 与隐式 Host 派生。
func (provider battleEndpointProvider) EndpointFor(_ context.Context, target simulationcontrol.SimulationTarget) (battleticket.Endpoint, error) {
	if provider.component == nil || provider.component.ControlSession() == nil ||
		target.Validate() != nil || target.NodeID != provider.component.SimulationNodeID() ||
		!provider.endpoint.Valid() {
		return battleticket.Endpoint{}, errors.New("battle UDP listener is not ready")
	}
	return provider.endpoint, nil
}

// battleActorCapacity 是 Go admission 的幂等预留镜像；C++ install registry 仍执行最终 hard cap。
type battleActorCapacity struct {
	// mutex 串行化同一 instance 的 8-slot 决策。
	mutex sync.Mutex
	// reservations 按 IssueID 保存 response-loss 幂等结果。
	reservations map[string]battleentry.Reservation
	// occupied 保存 exact target 的 slot ownership。
	occupied map[simulationcontrol.SimulationTarget][simulationcontrol.QualifiedActorCapacity]string
}

// newBattleActorCapacity 创建无后台任务的固定容量 owner。
func newBattleActorCapacity() *battleActorCapacity {
	return &battleActorCapacity{
		reservations: make(map[string]battleentry.Reservation),
		occupied:     make(map[simulationcontrol.SimulationTarget][simulationcontrol.QualifiedActorCapacity]string),
	}
}

// Reserve 为相同 issue/target 返回同一 slot，并原子拒绝第 9 个 actor。
func (owner *battleActorCapacity) Reserve(_ context.Context, request battleentry.ReservationRequest) (battleentry.Reservation, error) {
	if owner == nil || !request.Valid() {
		return battleentry.Reservation{}, battleticket.NewAdmissionError("capacity", battleticket.ErrorCodeInvalidArgument, nil)
	}
	owner.mutex.Lock()
	defer owner.mutex.Unlock()
	key := request.IssueID.Value()
	if existing, found := owner.reservations[key]; found {
		if existing.Target() != request.Target {
			return battleentry.Reservation{}, battleticket.NewAdmissionError("capacity", battleticket.ErrorCodeIdempotencyConflict, nil)
		}
		return existing, nil
	}
	slots := owner.occupied[request.Target]
	for index := uint8(0); index < simulationcontrol.QualifiedActorCapacity; index++ {
		if slots[index] != "" {
			continue
		}
		slot, _ := battleticket.NewActorSlot(index)
		reservation, err := battleentry.NewReservation(request.IssueID, request.Target, slot)
		if err != nil {
			return battleentry.Reservation{}, err
		}
		slots[index] = key
		owner.occupied[request.Target] = slots
		owner.reservations[key] = reservation
		return reservation, nil
	}
	return battleentry.Reservation{}, battleticket.NewAdmissionError("capacity", battleticket.ErrorCodeCapacityExceeded, nil)
}

// battleTargetResolver 适配既有 control resolver 的窄方法名，不复制 target 状态。
type battleTargetResolver struct {
	// resolver 是 placement 与 child registry 共同持有的唯一 target owner。
	resolver *simulationcontrol.TargetResolver
}

// ResolveSimulationTarget 委托 exact assignment 查询。
func (adapter battleTargetResolver) ResolveSimulationTarget(ctx context.Context, stamp placement.AssignmentStamp) (simulationcontrol.SimulationTarget, bool, error) {
	if adapter.resolver == nil {
		return simulationcontrol.SimulationTarget{}, false, errors.New("battle target resolver is unavailable")
	}
	return adapter.resolver.Resolve(ctx, stamp)
}

// Release 只释放相同 issue、target 与 slot，避免旧 cleanup 命中 successor。
func (owner *battleActorCapacity) Release(_ context.Context, reservation battleentry.Reservation) error {
	if owner == nil || !reservation.Valid() {
		return errors.New("battle reservation is invalid")
	}
	owner.mutex.Lock()
	defer owner.mutex.Unlock()
	key := reservation.IssueID().Value()
	current, found := owner.reservations[key]
	if !found {
		return nil
	}
	if current.Target() != reservation.Target() || current.Slot() != reservation.Slot() {
		return errors.New("battle reservation binding drifted")
	}
	slots := owner.occupied[reservation.Target()]
	if slots[reservation.Slot().Index()] == key {
		slots[reservation.Slot().Index()] = ""
	}
	delete(owner.reservations, key)
	if slots == [simulationcontrol.QualifiedActorCapacity]string{} {
		delete(owner.occupied, reservation.Target())
	} else {
		owner.occupied[reservation.Target()] = slots
	}
	return nil
}

// configuredBattleEndpoint 解析严格配置，不读取 HTTP Host 或请求字段。
func configuredBattleEndpoint(policy config.BattleUDPPolicy) (battleticket.Endpoint, error) {
	return battleticket.NewEndpoint(policy.Advertised.Host, uint16(policy.Advertised.Port))
}

var (
	_ battleentry.BattleEndpointProvider   = battleEndpointProvider{}
	_ battleentry.ActorCapacityOwner       = (*battleActorCapacity)(nil)
	_ battleentry.SimulationTargetResolver = battleTargetResolver{}
)
