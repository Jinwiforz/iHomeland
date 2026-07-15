package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	visitv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/visit/v1"
	worldv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/world/v1"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/transport/tcpgameplay"
	"github.com/jinwiforz/ihomeland/server/internal/transport/wscontrol"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
	"github.com/jinwiforz/ihomeland/server/internal/worldentry"
	"google.golang.org/protobuf/proto"
)

const visitDeadlineRetryDelay = time.Second

// visitWSSPublisher 是 VisitSession 控制面通知所需的最窄 WSS 端口。
type visitWSSPublisher interface {
	// PublishPlayer 只向认证 PlayerID 的当前控制连接投递。
	PublishPlayer(string, uint32, proto.Message) (wscontrol.DeliveryResult, error)
}

// visitTCPPublisher 是 gameplay snapshot 与 safe-return 所需的精确投递端口。
type visitTCPPublisher interface {
	// PublishConnection 向精确 Owner connection 投递。
	PublishConnection(string, uint32, proto.Message) (tcpgameplay.DeliveryResult, error)
	// PublishVisit 向当前 VisitSession Visitor target 投递。
	PublishVisit(string, uint32, proto.Message) (tcpgameplay.DeliveryResult, error)
	// PublishVisitor 向 VisitSessionID 与 VisitorID 的索引交集投递。
	PublishVisitor(string, string, uint32, proto.Message) (tcpgameplay.DeliveryResult, error)
}

// visitConnectionLookup 只回答精确 gameplay connection 是否仍可被当前进程解析。
type visitConnectionLookup interface {
	// HasConnection 不授予 mutation 权限，只用于避免旧连接仍存活时被新连接抢占。
	HasConnection(string) bool
}

// visitSnapshotProjector 把领域事实转换为已登记的 client-safe 完整替换投影。
type visitSnapshotProjector func(context.Context, visitsession.Snapshot) (*visitv1.VisitSessionSnapshot, error)

// visitScheduleState 防止旧 revision replay 覆盖较新 deadline 集合。
type visitScheduleState struct {
	// revision 是最近完成 reconciliation 的已提交版本。
	revision uint64
	// keys 是该版本仍应存在的全部语义任务。
	keys map[string]semanticDeadlineKey
}

// personalWorldVisitCoordinator 统一拥有 VisitSession deadline、连接生命周期和跨通道副作用。
//
// 领域 store 始终先提交事实；本类型只消费完整 result/snapshot 并幂等收敛可推导状态。
// 网络离线、队列已满或重复 replay 不会回滚事实，也不会被改报为 command 失败。
type personalWorldVisitCoordinator struct {
	// visits 是所有 VisitSession command 的唯一领域 owner。
	visits *visitsession.Service
	// deadlines 是进程内唯一有界语义时间 owner。
	deadlines *semanticDeadlineOwner
	// clock 决定 grace、retry 和 lifecycle command 的时间边界。
	clock Clock
	// policy 固定 Owner/Visitor grace，不从 payload 接受 deadline。
	policy visitsession.Policy
	// wss 只执行玩家控制连接定向投递。
	wss visitWSSPublisher
	// tcp 只执行 gameplay connection、VisitSession 或 Visitor 定向投递。
	tcp visitTCPPublisher
	// connections 只回答精确 gameplay connection 是否仍存活。
	connections visitConnectionLookup
	// logger 只记录低基数 operation/outcome，不记录 identity、binding 或 payload。
	logger *slog.Logger
	// observer 只记录封闭 lifecycle、deadline 与 delivery 结果。
	observer personalWorldSliceObserver
	// maximumEffects 限制进程内 replay 去重证据数量。
	maximumEffects int
	// failClosed 在可推导状态无法登记时撤销全局 readiness。
	failClosed func()

	// mutex 保护 projector 与 effect replay cache。
	mutex sync.Mutex
	// reconcileMutex 串行化 snapshot revision 与 deadline 集合的整体替换。
	reconcileMutex sync.Mutex
	// projector 在 TCP application 构造后、listener 启动前恰好绑定一次。
	projector visitSnapshotProjector
	// schedules 由 reconcileMutex 独占，按 VisitSessionID 保存最新 revision 任务集合。
	schedules map[string]visitScheduleState
	// effects 保存已经执行过副作用的 CommandID。
	effects map[string]struct{}
	// effectOrder 以固定容量环形切片提供 O(1) FIFO 淘汰。
	effectOrder []string
	// effectCursor 指向环形顺序切片下一次要淘汰的位置。
	effectCursor int
}

// newPersonalWorldVisitCoordinator 构造不启动 goroutine 的竖切协调器。
func newPersonalWorldVisitCoordinator(visits *visitsession.Service, deadlines *semanticDeadlineOwner, clock Clock, policy visitsession.Policy, wss visitWSSPublisher, tcp visitTCPPublisher, connections visitConnectionLookup, logger *slog.Logger, observer personalWorldSliceObserver, maximumEffects int, failClosed func()) (*personalWorldVisitCoordinator, error) {
	if visits == nil || deadlines == nil || clock == nil || !policy.Valid() || wss == nil || tcp == nil || connections == nil || logger == nil || observer == nil || maximumEffects < 1 || failClosed == nil {
		return nil, errors.New("visit session coordinator dependencies are incomplete")
	}
	return &personalWorldVisitCoordinator{
		visits: visits, deadlines: deadlines, clock: clock, policy: policy, wss: wss, tcp: tcp,
		connections: connections, logger: logger, observer: observer, maximumEffects: maximumEffects, failClosed: failClosed,
		schedules: make(map[string]visitScheduleState), effects: make(map[string]struct{}, maximumEffects), effectOrder: make([]string, 0, maximumEffects),
	}, nil
}

// BindProjector 在公开 listener 启动前一次性补全无环 composition graph。
func (coordinator *personalWorldVisitCoordinator) BindProjector(projector visitSnapshotProjector) error {
	if coordinator == nil || projector == nil {
		return errors.New("visit snapshot projector is invalid")
	}
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	if coordinator.projector != nil {
		return errors.New("visit snapshot projector cannot be rebound")
	}
	coordinator.projector = projector
	return nil
}

// OpenCommitted 登记 create/resolve 结果；Open 本身不产生邀请或 terminal 通知。
func (coordinator *personalWorldVisitCoordinator) OpenCommitted(ctx context.Context, result visitsession.OpenResult) {
	if coordinator == nil || !result.Valid() {
		return
	}
	coordinator.reconcile(ctx, result.Snapshot())
	coordinator.publishSnapshot(ctx, result.Snapshot())
}

// OwnerOpened 只允许当前 binding 的幂等 Open，或在旧 connection 已不存在时执行显式换绑。
func (coordinator *personalWorldVisitCoordinator) OwnerOpened(ctx context.Context, auth session.AuthContext, connectionID string, result visitsession.OpenResult) (visitsession.Snapshot, error) {
	if coordinator == nil || ctx == nil || !auth.Valid() || connectionID == "" || !result.Valid() {
		return visitsession.Snapshot{}, dependencyPublicError()
	}
	snapshot := result.Snapshot()
	desired, err := visitsession.NewConnectionBindingID("vbind_" + strings.TrimPrefix(connectionID, "tcp_"))
	if err != nil {
		return visitsession.Snapshot{}, dependencyPublicError()
	}
	if snapshot.OwnerBinding().ConnectionID() == desired {
		coordinator.OpenCommitted(ctx, result)
		return snapshot, nil
	}
	if oldConnection, ok := tcpConnectionFromVisitBinding(snapshot.OwnerBinding().ConnectionID()); ok && coordinator.connections.HasConnection(oldConnection) {
		return visitsession.Snapshot{}, tcpgameplay.PublicError{Code: 2104, MessageKey: "error.visit.state_conflict"}
	}
	if snapshot.Lifecycle() == visitsession.LifecycleOpen {
		deadline := earliestCoordinatorDeadline(coordinator.clock.Now().UTC().Add(coordinator.policy.OwnerGrace()), snapshot.ExpiresAt())
		commandID, commandErr := lifecycleVisitCommandID("owner_rebind_disconnect", connectionID, snapshot.ID(), snapshot.Revision())
		if commandErr != nil {
			return visitsession.Snapshot{}, dependencyPublicError()
		}
		disconnected, disconnectErr := coordinator.visits.OwnerDisconnect(ctx, auth, snapshot.ID(), snapshot.OwnerBinding().ConnectionID(), deadline, snapshot.Revision(), commandID)
		if disconnectErr != nil {
			return visitsession.Snapshot{}, mapVisitError(disconnectErr)
		}
		coordinator.MutationCommitted(ctx, disconnected)
		snapshot = disconnected.Snapshot()
	}
	if snapshot.Lifecycle() != visitsession.LifecycleOwnerGrace {
		return visitsession.Snapshot{}, tcpgameplay.PublicError{Code: 2104, MessageKey: "error.visit.state_conflict"}
	}
	commandID, err := lifecycleVisitCommandID("owner_rebind_reconnect", connectionID, snapshot.ID(), snapshot.Revision())
	if err != nil {
		return visitsession.Snapshot{}, dependencyPublicError()
	}
	reconnected, err := coordinator.visits.OwnerReconnect(ctx, auth, snapshot.ID(), desired, snapshot.Revision(), commandID)
	if err != nil {
		return visitsession.Snapshot{}, mapVisitError(err)
	}
	coordinator.MutationCommitted(ctx, reconnected)
	return reconnected.Snapshot(), nil
}

// SnapshotResolved 在读取边界惰性重建可推导 deadline，不产生一次性控制通知。
func (coordinator *personalWorldVisitCoordinator) SnapshotResolved(ctx context.Context, snapshot visitsession.Snapshot) {
	if coordinator == nil || !snapshot.Valid() {
		return
	}
	coordinator.reconcile(ctx, snapshot)
}

// MutationCommitted 先收敛 deadline，再对首次观察到的 CommandID 执行跨通道副作用。
func (coordinator *personalWorldVisitCoordinator) MutationCommitted(ctx context.Context, result visitsession.MutationResult) {
	if coordinator == nil || !result.Snapshot().Valid() || !result.CommandID().Valid() {
		return
	}
	coordinator.reconcile(ctx, result.Snapshot())
	if !coordinator.claimEffect(result.CommandID().Value()) {
		return
	}
	coordinator.publishControl(result)
	coordinator.publishSnapshot(ctx, result.Snapshot())
	coordinator.publishSafeReturns(result.Directives())
}

// claimEffect 以环形 FIFO 淘汰保持 replay 去重内存硬上限与 O(1) 写入。
func (coordinator *personalWorldVisitCoordinator) claimEffect(commandID string) bool {
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	if _, exists := coordinator.effects[commandID]; exists {
		return false
	}
	if len(coordinator.effectOrder) < coordinator.maximumEffects {
		coordinator.effectOrder = append(coordinator.effectOrder, commandID)
	} else {
		oldest := coordinator.effectOrder[coordinator.effectCursor]
		delete(coordinator.effects, oldest)
		coordinator.effectOrder[coordinator.effectCursor] = commandID
		coordinator.effectCursor = (coordinator.effectCursor + 1) % coordinator.maximumEffects
	}
	coordinator.effects[commandID] = struct{}{}
	return true
}

// reconcile 只允许同一 aggregate 的 revision 单调前进，并精确取消旧任务。
func (coordinator *personalWorldVisitCoordinator) reconcile(ctx context.Context, snapshot visitsession.Snapshot) {
	if ctx == nil || !snapshot.Valid() {
		return
	}
	visitID := snapshot.ID().Value()
	coordinator.reconcileMutex.Lock()
	defer coordinator.reconcileMutex.Unlock()
	previous, exists := coordinator.schedules[visitID]
	if exists && previous.revision > snapshot.Revision().Uint64() {
		return
	}
	if !exists && len(coordinator.schedules) == coordinator.maximumEffects {
		for candidate, state := range coordinator.schedules {
			if len(state.keys) == 0 {
				delete(coordinator.schedules, candidate)
				break
			}
		}
		if len(coordinator.schedules) == coordinator.maximumEffects {
			coordinator.logger.Error("visit reconciliation capacity exhausted", "operation", "snapshot_reconcile", "outcome", "capacity")
			coordinator.failClosed()
			return
		}
	}
	next := make(map[string]semanticDeadlineKey)
	if snapshot.Lifecycle() != visitsession.LifecycleClosed {
		coordinator.scheduleVisitTaskLocked(snapshot, semanticDeadlineVisitSession, "", "", 0, snapshot.ExpiresAt(), next, func(callCtx context.Context, task semanticDeadlineTask, commandID visitsession.CommandID) (visitsession.MutationResult, error) {
			return coordinator.visits.ExpireSession(callCtx, snapshot.ID(), task.conditionDeadline, visitsession.Revision(task.revision), commandID)
		})
		for _, invite := range snapshot.Invites() {
			if invite.State() != visitsession.InviteStatePending {
				continue
			}
			invite := invite
			coordinator.scheduleVisitTaskLocked(snapshot, semanticDeadlineInvite, invite.ID().Value(), "", 0, invite.ExpiresAt(), next, func(callCtx context.Context, task semanticDeadlineTask, commandID visitsession.CommandID) (visitsession.MutationResult, error) {
				return coordinator.visits.ExpireInvite(callCtx, snapshot.ID(), invite.ID(), task.conditionDeadline, visitsession.Revision(task.revision), commandID)
			})
		}
		for _, member := range snapshot.Memberships() {
			member := member
			switch member.State() {
			case visitsession.MembershipStateReserved:
				coordinator.scheduleVisitTaskLocked(snapshot, semanticDeadlineReservation, member.VisitorID().String(), "", 0, member.ReservationExpiresAt(), next, func(callCtx context.Context, task semanticDeadlineTask, commandID visitsession.CommandID) (visitsession.MutationResult, error) {
					return coordinator.visits.ExpireReservation(callCtx, snapshot.ID(), member.VisitorID(), member.InviteID(), task.conditionDeadline, visitsession.Revision(task.revision), commandID)
				})
			case visitsession.MembershipStateReconnecting:
				coordinator.scheduleVisitTaskLocked(snapshot, semanticDeadlineVisitorGrace, member.VisitorID().String(), member.BindingID().Value(), member.ReconnectGeneration(), member.ReconnectExpiresAt(), next, func(callCtx context.Context, task semanticDeadlineTask, commandID visitsession.CommandID) (visitsession.MutationResult, error) {
					return coordinator.visits.ExpireVisitorReconnect(callCtx, snapshot.ID(), member.VisitorID(), member.ReconnectGeneration(), member.BindingID(), task.conditionDeadline, visitsession.Revision(task.revision), commandID)
				})
			}
		}
		if snapshot.Lifecycle() == visitsession.LifecycleOwnerGrace {
			coordinator.scheduleVisitTaskLocked(snapshot, semanticDeadlineOwnerGrace, "owner", snapshot.OwnerBinding().ConnectionID().Value(), snapshot.OwnerGraceGeneration(), snapshot.OwnerGraceExpiresAt(), next, func(callCtx context.Context, task semanticDeadlineTask, commandID visitsession.CommandID) (visitsession.MutationResult, error) {
				return coordinator.visits.ExpireOwnerGrace(callCtx, snapshot.ID(), snapshot.OwnerGraceGeneration(), snapshot.OwnerBinding().ConnectionID(), task.conditionDeadline, visitsession.Revision(task.revision), commandID)
			})
		}
	}
	for stable, key := range previous.keys {
		if _, retained := next[stable]; !retained {
			coordinator.deadlines.Cancel(key)
		}
	}
	coordinator.schedules[visitID] = visitScheduleState{revision: snapshot.Revision().Uint64(), keys: next}
}

// scheduleVisitTaskLocked 登记一个条件不变的领域到期 command。
func (coordinator *personalWorldVisitCoordinator) scheduleVisitTaskLocked(snapshot visitsession.Snapshot, kind semanticDeadlineKind, target string, binding string, generation uint64, deadline time.Time, next map[string]semanticDeadlineKey, execute func(context.Context, semanticDeadlineTask, visitsession.CommandID) (visitsession.MutationResult, error)) {
	key := semanticDeadlineKey{kind: kind, scope: snapshot.ID().Value(), target: target, binding: binding, generation: generation}
	task := semanticDeadlineTask{key: key, deadline: deadline, conditionDeadline: deadline, revision: snapshot.Revision().Uint64()}
	task.execute = func(ctx context.Context, current semanticDeadlineTask) error {
		commandID, err := current.visitCommandID()
		if err != nil {
			return err
		}
		result, err := execute(ctx, current, commandID)
		if err == nil {
			coordinator.MutationCommitted(ctx, result)
			return nil
		}
		if retryableVisitDeadlineError(err) {
			if coordinator.observer != nil {
				coordinator.observer.ObserveSemanticDeadline(current.key.kind.String(), "retry")
			}
			current.deadline = coordinator.clock.Now().UTC().Add(visitDeadlineRetryDelay)
			return coordinator.deadlines.Schedule(current)
		}
		if terminalVisitDeadlineError(err) {
			if coordinator.observer != nil {
				coordinator.observer.ObserveSemanticDeadline(current.key.kind.String(), "stale")
			}
			return nil
		}
		return err
	}
	if err := coordinator.deadlines.Schedule(task); err != nil {
		coordinator.logger.Error("visit deadline schedule failed", "operation", kind.String(), "outcome", "capacity_or_closed")
		coordinator.failClosed()
		return
	}
	next[key.stableValue()] = key
}

// retryableVisitDeadlineError 只重试无法确认提交结论的依赖类别，并复用相同 CommandID。
func retryableVisitDeadlineError(err error) bool {
	return visitsession.IsErrorCode(err, visitsession.ErrorCodeDependency) || visitsession.IsErrorCode(err, visitsession.ErrorCodeCommitUnknown)
}

// terminalVisitDeadlineError 把已经由较新事实取代的 callback 视为幂等收敛。
func terminalVisitDeadlineError(err error) bool {
	for _, code := range []visitsession.ErrorCode{visitsession.ErrorCodeNotFound, visitsession.ErrorCodeInvalidState, visitsession.ErrorCodeExpired, visitsession.ErrorCodeStale, visitsession.ErrorCodeRevisionConflict} {
		if visitsession.IsErrorCode(err, code) {
			return true
		}
	}
	return false
}

// publishControl 把 committed operation 映射到既有 WSS push，不向无关玩家广播。
func (coordinator *personalWorldVisitCoordinator) publishControl(result visitsession.MutationResult) {
	snapshot := result.Snapshot()
	if result.Operation() == visitsession.OperationInvalidateAssignment {
		message := worldv1.WorldAssignmentChangedPush_builder{
			PersonalWorldId: proto.String(snapshot.WorldID().String()), ReasonKey: proto.String("world.assignment.changed"),
		}.Build()
		targets := map[string]struct{}{snapshot.OwnerID().String(): {}}
		for _, directive := range result.Directives() {
			targets[directive.VisitorID().String()] = struct{}{}
		}
		players := make([]string, 0, len(targets))
		for playerID := range targets {
			players = append(players, playerID)
		}
		sort.Strings(players)
		for _, playerID := range players {
			coordinator.publishWSS(playerID, 2003, message)
		}
	}
	switch result.Operation() {
	case visitsession.OperationCreateInvite:
		invite := result.Invite()
		if invite.Valid() {
			message := visitv1.VisitInvitePush_builder{Invite: projectInvite(snapshot.ID(), invite), OwnerPlayerId: proto.String(snapshot.OwnerID().String())}.Build()
			coordinator.publishWSS(invite.TargetID().String(), 2100, message)
		}
	case visitsession.OperationOwnerDisconnect, visitsession.OperationOwnerReconnect:
		message := visitv1.VisitOwnerAvailabilityPush_builder{
			VisitSessionId: proto.String(snapshot.ID().Value()), Available: proto.Bool(snapshot.Lifecycle() == visitsession.LifecycleOpen),
			Revision: proto.Uint64(snapshot.Revision().Uint64()),
		}.Build()
		if deadline := snapshot.OwnerGraceExpiresAt(); !deadline.IsZero() {
			message.SetGraceExpiresAtMs(deadline.UnixMilli())
		}
		for _, playerID := range visitControlTargets(snapshot, false) {
			coordinator.publishWSS(playerID, 2101, message)
		}
	default:
		if snapshot.Lifecycle() == visitsession.LifecycleClosed {
			reason := resultCloseReason(result)
			publicReason := visitv1.SafeReturnReason(reason)
			message := visitv1.VisitClosedNoticePush_builder{VisitSessionId: proto.String(snapshot.ID().Value()), Reason: &publicReason, Revision: proto.Uint64(snapshot.Revision().Uint64())}.Build()
			targets := make(map[string]struct{})
			for _, playerID := range visitControlTargets(snapshot, true) {
				targets[playerID] = struct{}{}
			}
			for _, directive := range result.Directives() {
				targets[directive.VisitorID().String()] = struct{}{}
			}
			players := make([]string, 0, len(targets))
			for playerID := range targets {
				players = append(players, playerID)
			}
			sort.Strings(players)
			for _, playerID := range players {
				coordinator.publishWSS(playerID, 2102, message)
			}
		}
	}
}

// visitControlTargets 返回稳定去重的 Owner、Visitor 与可选 pending invite target。
func visitControlTargets(snapshot visitsession.Snapshot, includeOwner bool) []string {
	targets := make(map[string]struct{})
	if includeOwner {
		targets[snapshot.OwnerID().String()] = struct{}{}
	}
	for _, member := range snapshot.Memberships() {
		targets[member.VisitorID().String()] = struct{}{}
	}
	for _, invite := range snapshot.Invites() {
		if invite.State() == visitsession.InviteStatePending {
			targets[invite.TargetID().String()] = struct{}{}
		}
	}
	values := make([]string, 0, len(targets))
	for value := range targets {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

// resultCloseReason 从原子 directive 或封闭 operation 恢复公开 reason。
func resultCloseReason(result visitsession.MutationResult) visitsession.SafeReturnReason {
	if directives := result.Directives(); len(directives) > 0 {
		return directives[0].Reason()
	}
	switch result.Operation() {
	case visitsession.OperationClose:
		return visitsession.SafeReturnReasonOwnerClosed
	case visitsession.OperationExpireOwnerGrace:
		return visitsession.SafeReturnReasonOwnerUnavailable
	case visitsession.OperationExpireSession:
		return visitsession.SafeReturnReasonSessionExpired
	case visitsession.OperationInvalidateAssignment:
		return visitsession.SafeReturnReasonAssignmentChanged
	case visitsession.OperationDependencyLost:
		return visitsession.SafeReturnReasonDependencyLost
	default:
		return visitsession.SafeReturnReasonDependencyLost
	}
}

// publishSnapshot 向 Owner 精确 connection 与全部当前 Visitor binding 投递完整替换投影。
func (coordinator *personalWorldVisitCoordinator) publishSnapshot(ctx context.Context, snapshot visitsession.Snapshot) {
	coordinator.mutex.Lock()
	projector := coordinator.projector
	coordinator.mutex.Unlock()
	if snapshot.Lifecycle() == visitsession.LifecycleClosed {
		return
	}
	if projector == nil {
		coordinator.logger.Error("visit snapshot projector is unavailable", "operation", "snapshot_push", "outcome", "composition_error")
		coordinator.failClosed()
		return
	}
	projected, err := projector(ctx, snapshot)
	if err != nil {
		coordinator.logger.Warn("visit snapshot projection skipped", "operation", "snapshot_push", "outcome", "dependency_or_stale")
		return
	}
	push := visitv1.VisitSnapshotPush_builder{Snapshot: projected}.Build()
	if ownerConnection, ok := tcpConnectionFromVisitBinding(snapshot.OwnerBinding().ConnectionID()); ok {
		coordinator.publishTCPConnection(ownerConnection, 2121, push)
	}
	coordinator.publishTCPVisit(snapshot.ID().Value(), 2121, push)
}

// publishSafeReturns 精确投递每条原子 directive，不按 PlayerID 跨 session 扩散。
func (coordinator *personalWorldVisitCoordinator) publishSafeReturns(directives []visitsession.SafeReturnDirective) {
	for _, directive := range directives {
		reason := visitv1.SafeReturnReason(directive.Reason())
		preferred := visitv1.SafeReturnDestination(directive.PreferredDestination())
		fallback := visitv1.SafeReturnDestination(directive.FallbackDestination())
		projected := visitv1.SafeReturnDirective_builder{
			VisitSessionId: proto.String(directive.VisitSessionID().Value()), VisitorId: proto.String(directive.VisitorID().String()),
			Reason: &reason, Preferred: &preferred, Fallback: &fallback,
		}.Build()
		_, err := coordinator.tcp.PublishVisitor(directive.VisitSessionID().Value(), directive.VisitorID().String(), 2122, visitv1.VisitSafeReturnPush_builder{Directive: projected}.Build())
		coordinator.observeDelivery("safe_return", err, tcpgameplay.ErrConnectionNotFound)
	}
}

// publishWSS 把离线 target 视为正常投递结果。
func (coordinator *personalWorldVisitCoordinator) publishWSS(playerID string, messageID uint32, message proto.Message) {
	_, err := coordinator.wss.PublishPlayer(playerID, messageID, message)
	coordinator.observeDelivery("wss_push", err, wscontrol.ErrConnectionNotFound)
}

// publishTCPConnection 把已关闭 Owner connection 视为正常竞态。
func (coordinator *personalWorldVisitCoordinator) publishTCPConnection(connectionID string, messageID uint32, message proto.Message) {
	_, err := coordinator.tcp.PublishConnection(connectionID, messageID, message)
	coordinator.observeDelivery("tcp_connection_push", err, tcpgameplay.ErrConnectionNotFound)
}

// publishTCPVisit 把尚无 Visitor gameplay connection 视为正常结果。
func (coordinator *personalWorldVisitCoordinator) publishTCPVisit(visitID string, messageID uint32, message proto.Message) {
	_, err := coordinator.tcp.PublishVisit(visitID, messageID, message)
	coordinator.observeDelivery("tcp_visit_push", err, tcpgameplay.ErrConnectionNotFound)
}

// observeDelivery 只记录稳定结果，不展开 backend error 文本。
func (coordinator *personalWorldVisitCoordinator) observeDelivery(operation string, err error, offline error) {
	outcome := "delivered"
	if err == nil {
		if coordinator.observer != nil {
			coordinator.observer.ObserveVisitDelivery(operation, outcome)
		}
		return
	}
	outcome = "failed"
	if errors.Is(err, offline) {
		outcome = "offline"
	}
	coordinator.logger.Debug("visit delivery completed", "operation", operation, "outcome", outcome)
	if coordinator.observer != nil {
		coordinator.observer.ObserveVisitDelivery(operation, outcome)
	}
}

// Connected 对 Owner grace 执行自动恢复，并对所有连接读取边界惰性重建 deadline。
func (coordinator *personalWorldVisitCoordinator) Connected(ctx context.Context, view tcpgameplay.LifecycleView) error {
	if coordinator == nil || ctx == nil || !view.Valid() {
		return errors.New("visit lifecycle connect is invalid")
	}
	binding := view.Binding()
	snapshot, found, err := coordinator.visits.ResolveActive(ctx, binding.WorldID())
	if err != nil {
		coordinator.observeLifecycle("connect", "failed")
		return err
	}
	if !found || (binding.VisitSessionID().Valid() && binding.VisitSessionID() != snapshot.ID()) {
		coordinator.observeLifecycle("connect", "ignored")
		return nil
	}
	coordinator.SnapshotResolved(ctx, snapshot)
	if binding.Role().String() != "owner" || snapshot.Lifecycle() != visitsession.LifecycleOwnerGrace {
		coordinator.observeLifecycle("connect", "ignored")
		return nil
	}
	newBinding, err := visitBindingFromConnection(view)
	if err != nil {
		return err
	}
	commandID, err := lifecycleVisitCommandID("owner_reconnect", view.ConnectionID(), snapshot.ID(), snapshot.Revision())
	if err != nil {
		return err
	}
	result, err := coordinator.visits.OwnerReconnect(ctx, view.Auth(), snapshot.ID(), newBinding, snapshot.Revision(), commandID)
	if err != nil {
		if terminalVisitDeadlineError(err) {
			coordinator.observeLifecycle("connect", "stale")
			return nil
		}
		coordinator.observeLifecycle("connect", "failed")
		return err
	}
	coordinator.MutationCommitted(ctx, result)
	coordinator.observeLifecycle("connect", "applied")
	return nil
}

// Disconnected 把 unexpected/invalidated close 转为精确 Owner 或 Visitor grace command。
func (coordinator *personalWorldVisitCoordinator) Disconnected(ctx context.Context, view tcpgameplay.LifecycleView, class tcpgameplay.CloseClass) error {
	if coordinator == nil || ctx == nil || !view.Valid() {
		return errors.New("visit lifecycle disconnect is invalid")
	}
	if class == tcpgameplay.CloseClassApplicationReturn || class == tcpgameplay.CloseClassDraining {
		coordinator.observeLifecycle("disconnect", "ignored")
		return nil
	}
	binding := view.Binding()
	snapshot, found, err := coordinator.visits.ResolveActive(ctx, binding.WorldID())
	if err != nil || !found {
		if err != nil {
			coordinator.observeLifecycle("disconnect", "failed")
		} else {
			coordinator.observeLifecycle("disconnect", "ignored")
		}
		return err
	}
	if binding.VisitSessionID().Valid() && binding.VisitSessionID() != snapshot.ID() {
		coordinator.observeLifecycle("disconnect", "stale")
		return nil
	}
	connectionBinding, err := visitBindingFromConnection(view)
	if err != nil {
		return err
	}
	now := coordinator.clock.Now().UTC()
	if binding.Role().String() == "owner" {
		if snapshot.OwnerBinding().ConnectionID() != connectionBinding {
			coordinator.observeLifecycle("disconnect", "stale")
			return nil
		}
		deadline := earliestCoordinatorDeadline(now.Add(coordinator.policy.OwnerGrace()), snapshot.ExpiresAt())
		commandID, err := lifecycleVisitCommandID("owner_disconnect", view.ConnectionID(), snapshot.ID(), snapshot.Revision())
		if err != nil {
			return err
		}
		result, err := coordinator.visits.OwnerDisconnect(ctx, view.Auth(), snapshot.ID(), connectionBinding, deadline, snapshot.Revision(), commandID)
		if err == nil {
			coordinator.MutationCommitted(ctx, result)
		}
		return coordinator.finishLifecycle("disconnect", err)
	}
	for _, member := range snapshot.Memberships() {
		if member.VisitorID() != binding.PlayerID() || member.State() != visitsession.MembershipStateJoined || member.BindingID() != connectionBinding {
			continue
		}
		deadline := earliestCoordinatorDeadline(now.Add(coordinator.policy.VisitorReconnectGrace()), snapshot.ExpiresAt())
		commandID, err := lifecycleVisitCommandID("visitor_disconnect", view.ConnectionID(), snapshot.ID(), snapshot.Revision())
		if err != nil {
			return err
		}
		result, err := coordinator.visits.VisitorDisconnect(ctx, view.Auth(), snapshot.ID(), connectionBinding, deadline, snapshot.Revision(), commandID)
		if err == nil {
			coordinator.MutationCommitted(ctx, result)
		}
		return coordinator.finishLifecycle("disconnect", err)
	}
	coordinator.observeLifecycle("disconnect", "ignored")
	return nil
}

// InvalidateAssignment 在 placement 权威丢失时提交 terminal VisitSession 事实。
func (coordinator *personalWorldVisitCoordinator) InvalidateAssignment(ctx context.Context, stamp placement.AssignmentStamp) error {
	if coordinator == nil || ctx == nil || !stamp.Valid() {
		return errors.New("visit assignment invalidation is invalid")
	}
	snapshot, found, err := coordinator.visits.ResolveActive(ctx, stamp.WorldID())
	if err != nil || !found || !snapshot.Assignment().Equal(stamp) {
		if err != nil {
			coordinator.observeLifecycle("assignment_invalidate", "failed")
		} else {
			coordinator.observeLifecycle("assignment_invalidate", "ignored")
		}
		return err
	}
	commandID, err := assignmentVisitCommandID(stamp, snapshot.ID(), snapshot.Revision())
	if err != nil {
		return err
	}
	result, err := coordinator.visits.InvalidateAssignment(ctx, snapshot.ID(), snapshot.Revision(), commandID)
	if err == nil {
		coordinator.MutationCommitted(ctx, result)
	}
	return coordinator.finishLifecycle("assignment_invalidate", err)
}

// finishLifecycle 统一把领域竞态分类为 applied、stale 或 failed。
func (coordinator *personalWorldVisitCoordinator) finishLifecycle(operation string, err error) error {
	if err == nil {
		coordinator.observeLifecycle(operation, "applied")
		return nil
	}
	if terminalVisitDeadlineError(err) {
		coordinator.observeLifecycle(operation, "stale")
		return nil
	}
	coordinator.observeLifecycle(operation, "failed")
	return err
}

// observeLifecycle 只转发固定 operation/outcome。
func (coordinator *personalWorldVisitCoordinator) observeLifecycle(operation string, outcome string) {
	if coordinator.observer != nil {
		coordinator.observer.ObserveVisitLifecycle(operation, outcome)
	}
}

// visitBindingFromConnection 复用 transport ConnectionID 与 VisitSession binding 的固定映射。
func visitBindingFromConnection(view tcpgameplay.LifecycleView) (visitsession.ConnectionBindingID, error) {
	return visitsession.NewConnectionBindingID("vbind_" + strings.TrimPrefix(view.ConnectionID(), "tcp_"))
}

// tcpConnectionFromVisitBinding 只逆转本进程生成的 vbind_ 前缀。
func tcpConnectionFromVisitBinding(binding visitsession.ConnectionBindingID) (string, bool) {
	value := binding.Value()
	if !strings.HasPrefix(value, "vbind_") || len(value) == len("vbind_") {
		return "", false
	}
	return "tcp_" + strings.TrimPrefix(value, "vbind_"), true
}

// lifecycleVisitCommandID 从稳定事件条件派生幂等 identity。
func lifecycleVisitCommandID(kind string, connectionID string, visitID visitsession.VisitSessionID, revision visitsession.Revision) (visitsession.CommandID, error) {
	digest := sha256.Sum256([]byte(strings.Join([]string{kind, connectionID, visitID.Value(), strconv.FormatUint(revision.Uint64(), 10)}, "\x00")))
	return visitsession.NewCommandID("vcmd_" + hex.EncodeToString(digest[:16]))
}

// assignmentVisitCommandID 覆盖完整 AssignmentStamp，避免旧 generation callback 碰撞。
func assignmentVisitCommandID(stamp placement.AssignmentStamp, visitID visitsession.VisitSessionID, revision visitsession.Revision) (visitsession.CommandID, error) {
	material := []string{stamp.WorldID().String(), stamp.InstanceID().String(), stamp.NodeID().String(), strconv.FormatUint(stamp.Generation().Uint64(), 10), strconv.FormatUint(stamp.FencingToken().Uint64(), 10), visitID.Value(), strconv.FormatUint(revision.Uint64(), 10)}
	digest := sha256.Sum256([]byte(strings.Join(material, "\x00")))
	return visitsession.NewCommandID("vcmd_" + hex.EncodeToString(digest[:16]))
}

// earliestCoordinatorDeadline 返回两个非零绝对时间中的较早值。
func earliestCoordinatorDeadline(left time.Time, right time.Time) time.Time {
	if right.IsZero() || (!left.IsZero() && left.Before(right)) {
		return left.UTC().Truncate(time.Microsecond)
	}
	return right.UTC().Truncate(time.Microsecond)
}

// coordinatedWorldEntryVisits 把 HTTP mutation 纳入同一 result 路径。
type coordinatedWorldEntryVisits struct {
	// visits 保留原始领域 owner。
	visits *visitsession.Service
	// coordinator 消费成功 accept 结果与资格读取的 lazy snapshot。
	coordinator *personalWorldVisitCoordinator
}

// coordinatedWorldEntryAssignments 在 own-world bootstrap 激活后惰性恢复该 world 的 VisitSession deadlines。
type coordinatedWorldEntryAssignments struct {
	// assignments 保留 placement activation/current owner。
	assignments *worldAssignmentCoordinator
	// visits 只读取该 world 的 active VisitSession。
	visits *visitsession.Service
	// coordinator 收敛已读取 snapshot 的可推导状态。
	coordinator *personalWorldVisitCoordinator
}

// EnsureActive 先完成 runtime/store activation，再恢复所触及 world 的 VisitSession deadlines。
func (adapter coordinatedWorldEntryAssignments) EnsureActive(ctx context.Context, worldID personalworld.PersonalWorldID) (placement.AssignmentSnapshot, error) {
	assignment, err := adapter.assignments.EnsureActive(ctx, worldID)
	if err != nil {
		return placement.AssignmentSnapshot{}, err
	}
	snapshot, found, resolveErr := adapter.visits.ResolveActive(ctx, worldID)
	if resolveErr != nil {
		return placement.AssignmentSnapshot{}, resolveErr
	}
	if found {
		adapter.coordinator.SnapshotResolved(ctx, snapshot)
	}
	return assignment, nil
}

// Resolve 委托 placement current owner，不复制 assignment 事实。
func (adapter coordinatedWorldEntryAssignments) Resolve(ctx context.Context, worldID personalworld.PersonalWorldID, observedAt time.Time) (placement.AssignmentSnapshot, placement.ResolveOutcome, error) {
	return adapter.assignments.Resolve(ctx, worldID, observedAt)
}

// AcceptInviteFromHTTP 委托领域 owner，并在事实提交后执行统一 result reconciliation。
func (adapter coordinatedWorldEntryVisits) AcceptInviteFromHTTP(ctx context.Context, authenticated session.AuthenticatedSession, visitID visitsession.VisitSessionID, inviteID visitsession.InviteID, expected visitsession.Revision, commandID visitsession.CommandID) (visitsession.MutationResult, error) {
	result, err := adapter.visits.AcceptInviteFromHTTP(ctx, authenticated, visitID, inviteID, expected, commandID)
	if err == nil {
		adapter.coordinator.MutationCommitted(ctx, result)
	}
	return result, err
}

// ResolveAdmissionEligibility 在成功只读资格边界同步执行 lazy deadline reconciliation。
func (adapter coordinatedWorldEntryVisits) ResolveAdmissionEligibility(ctx context.Context, authenticated session.AuthenticatedSession, visitID visitsession.VisitSessionID) (visitsession.AdmissionEligibility, error) {
	eligibility, err := adapter.visits.ResolveAdmissionEligibility(ctx, authenticated, visitID)
	if err != nil {
		return visitsession.AdmissionEligibility{}, err
	}
	snapshot, found, resolveErr := adapter.visits.ResolveActive(ctx, eligibility.Intent().Assignment().WorldID())
	if resolveErr != nil {
		return visitsession.AdmissionEligibility{}, resolveErr
	}
	if !found || snapshot.ID() != visitID {
		return visitsession.AdmissionEligibility{}, errors.New("visit eligibility snapshot changed during reconciliation")
	}
	adapter.coordinator.SnapshotResolved(ctx, snapshot)
	return eligibility, nil
}

var _ visitResultCoordinator = (*personalWorldVisitCoordinator)(nil)
var _ tcpgameplay.LifecycleSink = (*personalWorldVisitCoordinator)(nil)
var _ worldentry.VisitSessionOwner = coordinatedWorldEntryVisits{}
var _ worldentry.AssignmentOwner = coordinatedWorldEntryAssignments{}
