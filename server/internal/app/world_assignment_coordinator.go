package app

import (
	"context"
	"errors"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
)

const (
	// worldAssignmentConvergenceAttempts 覆盖一次 resolve、一次竞争写与一次最终复核。
	worldAssignmentConvergenceAttempts = 3
	// worldAssignmentRenewRetryDelay 限制短暂依赖失败时的重试频率。
	worldAssignmentRenewRetryDelay = time.Second
)

// placementCommands 是PersonalWorld slice消费的Placement application窄端口。
type placementCommands interface {
	// EnsureActive 创建或解析current active assignment。
	EnsureActive(context.Context, personalworld.PersonalWorldID, placement.RuntimeNodeID) (placement.AssignmentResult, error)
	// Renew 延长完整current stamp的lease。
	Renew(context.Context, placement.AssignmentStamp) (placement.AssignmentSnapshot, error)
	// Replace 以完整predecessor stamp切换到预生成successor。
	Replace(context.Context, placement.ReplaceCommand) (placement.AssignmentResult, error)
}

// placementCurrentStore 是coordinator恢复与并发决议需要的只读current端口。
type placementCurrentStore interface {
	// Resolve 返回observedAt时刻的current snapshot与封闭outcome。
	Resolve(context.Context, personalworld.PersonalWorldID, time.Time) (placement.AssignmentSnapshot, placement.ResolveOutcome, error)
}

// assignmentLossHandler 只处理已有权威missing/expired/replacement证据的predecessor。
type assignmentLossHandler func(context.Context, placement.AssignmentStamp) error

// worldAssignmentCoordinator 把单进程runtime、Placement command与lease task收敛为world-entry窄端口。
type worldAssignmentCoordinator struct {
	// commands 是assignment transition唯一application owner。
	commands placementCommands
	// current 提供每次决议所需的current事实。
	current placementCurrentStore
	// runtime 证明assignment是否实际由本进程逻辑承载。
	runtime *processWorldRuntime
	// deadlines 拥有renew/expiry任务。
	deadlines *semanticDeadlineOwner
	// clock 与Placement service共享受信绝对时间。
	clock Clock
	// ids 预生成commit-unknown重试所需successor identity。
	ids IDGenerator
	// nodeID 是本进程唯一RuntimeNodeID。
	nodeID placement.RuntimeNodeID
	// onLoss 在权威assignment change后关闭绑定predecessor的VisitSession。
	onLoss assignmentLossHandler
	// observer 只记录lease低基数结果，在首次activation前由Composition Root绑定。
	observer personalWorldSliceObserver
}

// newWorldAssignmentCoordinator 校验全部production owner；onLoss可在Visit coordinator构造前为空。
func newWorldAssignmentCoordinator(commands placementCommands, current placementCurrentStore, runtime *processWorldRuntime, deadlines *semanticDeadlineOwner, clock Clock, ids IDGenerator, nodeID placement.RuntimeNodeID, onLoss assignmentLossHandler) (*worldAssignmentCoordinator, error) {
	if commands == nil || current == nil || runtime == nil || deadlines == nil || clock == nil || ids == nil || !nodeID.Valid() {
		return nil, errors.New("world assignment coordinator dependencies are incomplete")
	}
	return &worldAssignmentCoordinator{commands: commands, current: current, runtime: runtime, deadlines: deadlines, clock: clock, ids: ids, nodeID: nodeID, onLoss: onLoss}, nil
}

// bindObserver 在 coordinator 对外可见前绑定低敏 lease 观测器。
func (coordinator *worldAssignmentCoordinator) bindObserver(observer personalWorldSliceObserver) error {
	if coordinator == nil || observer == nil || coordinator.observer != nil {
		return errors.New("world assignment observer cannot be bound")
	}
	coordinator.observer = observer
	return nil
}

// EnsureActive 返回本进程实际承载且store已确认active的current assignment。
func (coordinator *worldAssignmentCoordinator) EnsureActive(ctx context.Context, worldID personalworld.PersonalWorldID) (placement.AssignmentSnapshot, error) {
	if coordinator == nil || ctx == nil || !worldID.Valid() {
		return placement.AssignmentSnapshot{}, errors.New("world assignment activation input is invalid")
	}
	for attempt := 0; attempt < worldAssignmentConvergenceAttempts; attempt++ {
		now := coordinator.clock.Now().UTC().Truncate(time.Microsecond)
		current, outcome, err := coordinator.current.Resolve(ctx, worldID, now)
		if err != nil {
			return placement.AssignmentSnapshot{}, err
		}
		switch outcome {
		case placement.ResolveOutcomeNotFound:
			if current.Valid() {
				return placement.AssignmentSnapshot{}, errors.New("placement returned snapshot for missing current")
			}
			// Redis flush/TTL 可以删除current；先停止同world全部本地旧runtime并失效仍存在的VisitSession。
			for _, orphan := range coordinator.runtime.stampsForWorld(worldID) {
				if cleanupErr := coordinator.stopLocalAssignment(ctx, orphan, true); cleanupErr != nil {
					return placement.AssignmentSnapshot{}, cleanupErr
				}
			}
			result, ensureErr := coordinator.commands.EnsureActive(ctx, worldID, coordinator.nodeID)
			if ensureErr != nil {
				return placement.AssignmentSnapshot{}, ensureErr
			}
			assignment, accepted, acceptErr := coordinator.acceptResult(result)
			if acceptErr != nil {
				return placement.AssignmentSnapshot{}, acceptErr
			}
			if accepted {
				return assignment, nil
			}
		case placement.ResolveOutcomeFound:
			if !current.Valid() || current.WorldID() != worldID {
				return placement.AssignmentSnapshot{}, errors.New("placement returned malformed current assignment")
			}
			if current.Phase() == placement.PhaseActive && current.ValidAt(now) && current.NodeID() == coordinator.nodeID && coordinator.runtime.contains(current.Stamp()) {
				if err := coordinator.scheduleAssignment(current); err != nil {
					return placement.AssignmentSnapshot{}, err
				}
				return current, nil
			}
			result, replaceErr := coordinator.replace(ctx, current.Stamp())
			assignment, accepted, acceptErr := coordinator.acceptResult(result)
			if acceptErr != nil {
				return placement.AssignmentSnapshot{}, acceptErr
			}
			if accepted && (replaceErr == nil || placement.ErrorKindOf(replaceErr) == placement.ErrorKindCleanupFailed) {
				return assignment, nil
			}
			if replaceErr != nil && placement.ErrorKindOf(replaceErr) != placement.ErrorKindConflict && placement.ErrorKindOf(replaceErr) != placement.ErrorKindInProgress {
				return placement.AssignmentSnapshot{}, replaceErr
			}
		default:
			return placement.AssignmentSnapshot{}, errors.New("placement returned unknown resolve outcome")
		}
	}
	return placement.AssignmentSnapshot{}, errors.New("world assignment did not converge")
}

// Resolve 委托production store，供world admission签发执行独立point-in-time读取。
func (coordinator *worldAssignmentCoordinator) Resolve(ctx context.Context, worldID personalworld.PersonalWorldID, observedAt time.Time) (placement.AssignmentSnapshot, placement.ResolveOutcome, error) {
	if coordinator == nil {
		return placement.AssignmentSnapshot{}, placement.ResolveOutcomeUnspecified, errors.New("world assignment coordinator is unavailable")
	}
	return coordinator.current.Resolve(ctx, worldID, observedAt)
}

// replace 预生成successor identity并保留完整expected stamp。
func (coordinator *worldAssignmentCoordinator) replace(ctx context.Context, predecessor placement.AssignmentStamp) (placement.AssignmentResult, error) {
	material, err := coordinator.ids.NewID()
	if err != nil {
		return placement.AssignmentResult{}, err
	}
	successor, err := placement.NewWorldInstanceID("winst_" + material)
	if err != nil {
		return placement.AssignmentResult{}, err
	}
	command, err := placement.NewReplaceCommand(predecessor, successor, coordinator.nodeID)
	if err != nil {
		return placement.AssignmentResult{}, err
	}
	return coordinator.commands.Replace(ctx, command)
}

// acceptResult 验证 Placement result 同时具有本地 runtime 事实，并原样传播 lease 调度失败。
func (coordinator *worldAssignmentCoordinator) acceptResult(result placement.AssignmentResult) (placement.AssignmentSnapshot, bool, error) {
	if !result.Valid() {
		return placement.AssignmentSnapshot{}, false, nil
	}
	assignment := result.Assignment()
	if assignment.NodeID() != coordinator.nodeID || !coordinator.runtime.contains(assignment.Stamp()) {
		return placement.AssignmentSnapshot{}, false, nil
	}
	if err := coordinator.scheduleAssignment(assignment); err != nil {
		return placement.AssignmentSnapshot{}, false, err
	}
	return assignment, true, nil
}

// scheduleAssignment 原位登记renew与absolute expiry任务；续约时点固定为剩余lease一半。
func (coordinator *worldAssignmentCoordinator) scheduleAssignment(snapshot placement.AssignmentSnapshot) error {
	now := coordinator.clock.Now().UTC().Truncate(time.Microsecond)
	if !snapshot.ValidAt(now) || snapshot.Phase() != placement.PhaseActive || snapshot.NodeID() != coordinator.nodeID || !coordinator.runtime.contains(snapshot.Stamp()) {
		return errors.New("cannot schedule non-local active assignment")
	}
	remaining := snapshot.Lease().ExpiresAt().Sub(now)
	renewAt := now.Add(remaining / 2).UTC().Truncate(time.Microsecond)
	renewKey := assignmentDeadlineKey(semanticDeadlineAssignmentRenew, snapshot.Stamp())
	expiryKey := assignmentDeadlineKey(semanticDeadlineAssignmentExpiry, snapshot.Stamp())
	if err := coordinator.deadlines.Schedule(semanticDeadlineTask{key: renewKey, deadline: renewAt, execute: func(ctx context.Context, _ semanticDeadlineTask) error {
		return coordinator.renew(ctx, snapshot)
	}}); err != nil {
		return err
	}
	if err := coordinator.deadlines.Schedule(semanticDeadlineTask{key: expiryKey, deadline: snapshot.Lease().ExpiresAt(), execute: func(ctx context.Context, _ semanticDeadlineTask) error {
		return coordinator.expire(ctx, snapshot)
	}}); err != nil {
		coordinator.deadlines.Cancel(renewKey)
		return err
	}
	return nil
}

// renew 延长matching current lease；短暂依赖失败只在原expiry前按固定小步重试。
func (coordinator *worldAssignmentCoordinator) renew(ctx context.Context, previous placement.AssignmentSnapshot) error {
	if !coordinator.runtime.contains(previous.Stamp()) {
		return nil
	}
	renewed, err := coordinator.commands.Renew(ctx, previous.Stamp())
	if err == nil {
		coordinator.observeLease("renewed")
		return coordinator.scheduleAssignment(renewed)
	}
	now := coordinator.clock.Now().UTC().Truncate(time.Microsecond)
	switch placement.ErrorKindOf(err) {
	case placement.ErrorKindDependencyUnavailable:
		if now.Before(previous.Lease().ExpiresAt()) {
			coordinator.observeLease("retry")
			retryAt := now.Add(worldAssignmentRenewRetryDelay)
			if retryAt.After(previous.Lease().ExpiresAt()) {
				retryAt = previous.Lease().ExpiresAt()
			}
			return coordinator.deadlines.Schedule(semanticDeadlineTask{key: assignmentDeadlineKey(semanticDeadlineAssignmentRenew, previous.Stamp()), deadline: retryAt, execute: func(ctx context.Context, _ semanticDeadlineTask) error {
				return coordinator.renew(ctx, previous)
			}})
		}
		coordinator.observeLease("expired")
		return coordinator.stopLocalAssignment(ctx, previous.Stamp(), false)
	case placement.ErrorKindNotFound, placement.ErrorKindExpired, placement.ErrorKindConflict:
		coordinator.observeLease("lost")
		return coordinator.stopLocalAssignment(ctx, previous.Stamp(), true)
	default:
		coordinator.observeLease("failed")
		return err
	}
}

// expire 在absolute boundary重新读取current，避免晚到旧expiry停止已成功续约的相同stamp。
func (coordinator *worldAssignmentCoordinator) expire(ctx context.Context, previous placement.AssignmentSnapshot) error {
	if !coordinator.runtime.contains(previous.Stamp()) {
		return nil
	}
	now := coordinator.clock.Now().UTC().Truncate(time.Microsecond)
	current, outcome, err := coordinator.current.Resolve(ctx, previous.WorldID(), now)
	if err != nil {
		coordinator.observeLease("failed")
		return coordinator.stopLocalAssignment(ctx, previous.Stamp(), false)
	}
	switch outcome {
	case placement.ResolveOutcomeNotFound:
		coordinator.observeLease("lost")
		return coordinator.stopLocalAssignment(ctx, previous.Stamp(), true)
	case placement.ResolveOutcomeFound:
		if !current.Valid() || current.WorldID() != previous.WorldID() {
			coordinator.observeLease("failed")
			return errors.Join(coordinator.stopLocalAssignment(ctx, previous.Stamp(), false), errors.New("placement returned malformed current assignment at expiry"))
		}
		if current.Stamp().Equal(previous.Stamp()) && current.Phase() == placement.PhaseActive && current.ValidAt(now) {
			return coordinator.scheduleAssignment(current)
		}
		coordinator.observeLease("lost")
		return coordinator.stopLocalAssignment(ctx, previous.Stamp(), true)
	default:
		coordinator.observeLease("failed")
		return errors.Join(coordinator.stopLocalAssignment(ctx, previous.Stamp(), false), errors.New("placement returned unknown resolve outcome at expiry"))
	}
}

// observeLease 忽略未绑定测试实例，并只转发封闭结果。
func (coordinator *worldAssignmentCoordinator) observeLease(outcome string) {
	if coordinator.observer != nil {
		coordinator.observer.ObserveWorldLease(outcome)
	}
}

// stopLocalAssignment 清除本地 runtime 与两个任务；只有权威证据才通知 VisitSession owner。
func (coordinator *worldAssignmentCoordinator) stopLocalAssignment(ctx context.Context, stamp placement.AssignmentStamp, authoritativeLoss bool) error {
	coordinator.deadlines.Cancel(assignmentDeadlineKey(semanticDeadlineAssignmentRenew, stamp))
	coordinator.deadlines.Cancel(assignmentDeadlineKey(semanticDeadlineAssignmentExpiry, stamp))
	stopErr := coordinator.runtime.Stop(ctx, stamp)
	if authoritativeLoss && coordinator.onLoss != nil {
		return errors.Join(stopErr, coordinator.onLoss(ctx, stamp))
	}
	return stopErr
}

// assignmentDeadlineKey 绑定不可复活instance与generation，旧任务不能覆盖successor。
func assignmentDeadlineKey(kind semanticDeadlineKind, stamp placement.AssignmentStamp) semanticDeadlineKey {
	return semanticDeadlineKey{kind: kind, scope: stamp.InstanceID().String(), target: stamp.WorldID().String(), generation: stamp.Generation().Uint64()}
}
