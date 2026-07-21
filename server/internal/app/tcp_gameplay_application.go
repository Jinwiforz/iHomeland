package app

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	sessionv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/session/v1"
	visitv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/visit/v1"
	worldv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/world/v1"
	personalworld "github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/transport/tcpgameplay"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
	"github.com/jinwiforz/ihomeland/server/internal/worldadmission"
	"google.golang.org/protobuf/proto"
)

// gameplayWorldReader 是PersonalWorld snapshot所需的只读领域端口。
type gameplayWorldReader interface {
	// FindByID 返回受信binding指向的持久世界事实。
	FindByID(context.Context, personalworld.PersonalWorldID) (personalworld.Snapshot, personalworld.FindOutcome, error)
}

// gameplayAssignmentReader 是client-safe assignment投影所需的current只读端口。
type gameplayAssignmentReader interface {
	// Resolve 返回observedAt时刻current assignment与lease。
	Resolve(context.Context, personalworld.PersonalWorldID, time.Time) (placement.AssignmentSnapshot, placement.ResolveOutcome, error)
}

// visitResultCoordinator 统一消费已经提交或读取到的 VisitSession 事实。
//
// 回调发生在领域 owner 返回成功之后，因此实现不得把投递失败反向伪装成 command 失败；
// deadline、通知和 safe-return 必须依赖 result 中的权威 snapshot 幂等收敛。
type visitResultCoordinator interface {
	staleOpenReconciler
	// OpenCommitted 消费首次创建或幂等解析的 active snapshot。
	OpenCommitted(context.Context, visitsession.OpenResult)
	// OwnerOpened 防止新连接抢占仍存活的 Owner binding，并恢复已失联旧 binding。
	OwnerOpened(context.Context, session.AuthContext, string, visitsession.OpenResult) (visitsession.Snapshot, error)
	// MutationCommitted 消费首次提交或幂等 replay 的完整结果。
	MutationCommitted(context.Context, visitsession.MutationResult)
	// SnapshotResolved 对只读边界执行 lazy deadline reconciliation。
	SnapshotResolved(context.Context, visitsession.Snapshot)
}

// staleOpenReconciler 是显式Open恢复旧assignment时唯一允许的终态端口。
type staleOpenReconciler interface {
	// ReconcileStaleOpen 提交或解析阻塞current assignment的旧session终态。
	ReconcileStaleOpen(context.Context, personalworld.PersonalWorldID) error
}

// tcpGameplayApplication 把transport逐operation端口适配到既有领域owner。
type tcpGameplayApplication struct {
	// worlds 只读PersonalWorld持久事实，不拥有repository生命周期。
	worlds gameplayWorldReader
	// assignments 只读current placement并验证完整stamp。
	assignments gameplayAssignmentReader
	// visits 是VisitSession mutation与replay唯一owner。
	visits *visitsession.Service
	// results 在事实提交后统一登记deadline并投递跨通道副作用。
	results visitResultCoordinator
	// endpoint 是受信advertised TLS_TCP投影。
	endpoint session.Endpoint
	// clock 与领域service共享绝对时间来源。
	clock Clock
}

// newTCPGameplayApplication 构造无socket、无缓存且不启动任务的application bridge。
func newTCPGameplayApplication(worlds gameplayWorldReader, assignments gameplayAssignmentReader, visits *visitsession.Service, results visitResultCoordinator, endpoint session.Endpoint, clock Clock) (*tcpGameplayApplication, error) {
	if worlds == nil || assignments == nil || visits == nil || results == nil || !endpoint.Valid() || endpoint.Channel() != session.ChannelTLSTCP || clock == nil {
		return nil, errors.New("tcp gameplay application dependencies are incomplete")
	}
	return &tcpGameplayApplication{worlds: worlds, assignments: assignments, visits: visits, results: results, endpoint: endpoint, clock: clock}, nil
}

// WorldSnapshot 读取connection binding唯一世界并验证current assignment完整相等。
func (application *tcpGameplayApplication) WorldSnapshot(ctx context.Context, operation tcpgameplay.OperationContext, _ *worldv1.WorldSnapshotRequest) (*worldv1.WorldSnapshotResponse, error) {
	binding, err := application.binding(operation)
	if err != nil {
		return nil, err
	}
	world, outcome, repositoryErr := application.worlds.FindByID(ctx, binding.WorldID())
	if repositoryErr != nil {
		return nil, dependencyPublicError()
	}
	if outcome != personalworld.FindOutcomeFound || !world.Valid() || world.ID() != binding.WorldID() {
		return nil, tcpgameplay.PublicError{Code: 2000, MessageKey: "error.world.not_found"}
	}
	assignment, err := application.currentAssignment(ctx, binding)
	if err != nil {
		return nil, err
	}
	snapshot := worldv1.WorldSnapshot_builder{World: projectPersonalWorld(world), Assignment: application.projectAssignment(assignment)}.Build()
	return worldv1.WorldSnapshotResponse_builder{Snapshot: snapshot}.Build(), nil
}

// VisitSnapshot 读取当前binding所指或Owner world当前active VisitSession。
func (application *tcpGameplayApplication) VisitSnapshot(ctx context.Context, operation tcpgameplay.OperationContext, _ *visitv1.VisitSnapshotRequest) (*visitv1.VisitSnapshotResponse, error) {
	snapshot, err := application.resolveVisit(ctx, operation)
	if err != nil {
		return nil, err
	}
	application.results.SnapshotResolved(ctx, snapshot)
	projected, err := application.projectVisit(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	return visitv1.VisitSnapshotResponse_builder{Snapshot: projected}.Build(), nil
}

// VisitOpen 委托VisitSession owner并使用envelope command identity建立Owner binding。
func (application *tcpGameplayApplication) VisitOpen(ctx context.Context, operation tcpgameplay.OperationContext, _ *visitv1.VisitOpenCommand, rawCommandID []byte) (*visitv1.VisitOpenResponse, error) {
	if operation.Qualification.Binding().Purpose() != worldadmission.PurposeOwnWorld {
		return nil, forbiddenPublicError()
	}
	binding, err := application.binding(operation)
	if err != nil {
		return nil, err
	}
	bindingID, commandID, err := gameplayMutationIDs(operation.ConnectionID, hex.EncodeToString(rawCommandID))
	if err != nil {
		return nil, dependencyPublicError()
	}
	result, err := reconcileVisitOpen(ctx, binding.WorldID(), func() (visitsession.OpenResult, error) {
		return application.visits.Open(ctx, operation.Auth, bindingID, commandID)
	}, application.results)
	if err != nil {
		return nil, mapVisitError(err)
	}
	snapshot, err := application.results.OwnerOpened(ctx, operation.Auth, operation.ConnectionID, result)
	if err != nil {
		return nil, err
	}
	projected, err := application.projectVisit(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	return visitv1.VisitOpenResponse_builder{Snapshot: projected}.Build(), nil
}

// reconcileVisitOpen 只在首次Open明确返回stale后执行一次终态恢复和一次重新解析。
//
// open闭包固定同一AuthContext、binding和CommandID；本函数不按时间或次数循环，也不在
// reconciliation结果不确定时继续创建，因此两个调用是由权威终态分隔的application步骤。
func reconcileVisitOpen(ctx context.Context, worldID personalworld.PersonalWorldID, open func() (visitsession.OpenResult, error), reconciler staleOpenReconciler) (visitsession.OpenResult, error) {
	result, err := open()
	if !visitsession.IsErrorCode(err, visitsession.ErrorCodeStale) {
		return result, err
	}
	if err := reconciler.ReconcileStaleOpen(ctx, worldID); err != nil {
		return visitsession.OpenResult{}, err
	}
	return open()
}

// VisitCreateInvite 委托Owner mutation并投影完整replay结果和invite。
func (application *tcpGameplayApplication) VisitCreateInvite(ctx context.Context, operation tcpgameplay.OperationContext, payload *visitv1.VisitCreateInviteCommand, rawCommandID []byte) (*visitv1.VisitCreateInviteResponse, error) {
	target, err := account.NewPlayerID(payload.GetTargetVisitorId())
	if err != nil {
		return nil, validationPublicError()
	}
	_, commandID, err := gameplayMutationIDs(operation.ConnectionID, hex.EncodeToString(rawCommandID))
	if err != nil {
		return nil, validationPublicError()
	}
	expiresAt := application.clock.Now().UTC().Add(time.Duration(payload.GetInviteLifetimeMs()) * time.Millisecond)
	expected, err := visitsession.NewRevision(payload.GetExpectedRevision())
	if err != nil {
		return nil, validationPublicError()
	}
	result, err := application.visits.CreateInvite(ctx, operation.Auth, target, expiresAt, expected, commandID)
	if err != nil {
		return nil, mapVisitError(err)
	}
	application.results.MutationCommitted(ctx, result)
	projected, err := application.projectMutation(ctx, result)
	if err != nil {
		return nil, err
	}
	invite := projectInvite(result.Snapshot().ID(), result.Invite())
	return visitv1.VisitCreateInviteResponse_builder{Result: projected, Invite: invite}.Build(), nil
}

// VisitRevokeInvite 撤销当前Owner active VisitSession中的指定邀请。
func (application *tcpGameplayApplication) VisitRevokeInvite(ctx context.Context, operation tcpgameplay.OperationContext, payload *visitv1.VisitRevokeInviteCommand, rawCommandID []byte) (*visitv1.VisitRevokeInviteResponse, error) {
	visit, err := application.resolveVisit(ctx, operation)
	if err != nil {
		return nil, err
	}
	inviteID, err := visitsession.NewInviteID(payload.GetInviteId())
	if err != nil {
		return nil, validationPublicError()
	}
	expected, commandID, err := revisionAndCommand(payload.GetExpectedRevision(), rawCommandID)
	if err != nil {
		return nil, validationPublicError()
	}
	result, err := application.visits.RevokeInvite(ctx, operation.Auth, visit.ID(), inviteID, expected, commandID)
	if err != nil {
		return nil, mapVisitError(err)
	}
	application.results.MutationCommitted(ctx, result)
	projected, err := application.projectMutation(ctx, result)
	if err != nil {
		return nil, err
	}
	return visitv1.VisitRevokeInviteResponse_builder{Result: projected}.Build(), nil
}

// VisitJoin 使用dispatcher恢复并比对的Qualification提交reserved membership。
func (application *tcpGameplayApplication) VisitJoin(ctx context.Context, operation tcpgameplay.OperationContext, qualification worldadmission.Qualification, payload *visitv1.VisitJoinCommand, rawCommandID []byte) (*visitv1.VisitJoinResponse, error) {
	return application.joinOrReconnect(ctx, operation, qualification, payload.GetExpectedRevision(), rawCommandID, false)
}

// VisitLeave 移除当前Visitor自己的binding并返回权威safe-return结果。
func (application *tcpGameplayApplication) VisitLeave(ctx context.Context, operation tcpgameplay.OperationContext, payload *visitv1.VisitLeaveCommand, rawCommandID []byte) (*visitv1.VisitLeaveResponse, error) {
	visit, bindingID, expected, commandID, err := application.visitMutationInput(ctx, operation, payload.GetExpectedRevision(), rawCommandID)
	if err != nil {
		return nil, err
	}
	result, err := application.visits.Leave(ctx, operation.Auth, visit.ID(), bindingID, expected, commandID)
	if err != nil {
		return nil, mapVisitError(err)
	}
	application.results.MutationCommitted(ctx, result)
	projected, err := application.projectMutation(ctx, result)
	if err != nil {
		return nil, err
	}
	return visitv1.VisitLeaveResponse_builder{Result: projected}.Build(), nil
}

// VisitKick 只把payload目标解释为被操作Visitor，不用它覆盖actor。
func (application *tcpGameplayApplication) VisitKick(ctx context.Context, operation tcpgameplay.OperationContext, payload *visitv1.VisitKickCommand, rawCommandID []byte) (*visitv1.VisitKickResponse, error) {
	visit, _, expected, commandID, err := application.visitMutationInput(ctx, operation, payload.GetExpectedRevision(), rawCommandID)
	if err != nil {
		return nil, err
	}
	target, err := account.NewPlayerID(payload.GetTargetVisitorId())
	if err != nil {
		return nil, validationPublicError()
	}
	result, err := application.visits.Kick(ctx, operation.Auth, visit.ID(), target, expected, commandID)
	if err != nil {
		return nil, mapVisitError(err)
	}
	application.results.MutationCommitted(ctx, result)
	projected, err := application.projectMutation(ctx, result)
	if err != nil {
		return nil, err
	}
	return visitv1.VisitKickResponse_builder{Result: projected}.Build(), nil
}

// VisitReconnect 使用dispatcher恢复并比对的Qualification更新Visitor binding。
func (application *tcpGameplayApplication) VisitReconnect(ctx context.Context, operation tcpgameplay.OperationContext, qualification worldadmission.Qualification, payload *visitv1.VisitReconnectCommand, rawCommandID []byte) (*visitv1.VisitReconnectResponse, error) {
	response, err := application.joinOrReconnect(ctx, operation, qualification, payload.GetExpectedRevision(), rawCommandID, true)
	if err != nil {
		return nil, err
	}
	return visitv1.VisitReconnectResponse_builder{Result: response.GetResult()}.Build(), nil
}

// VisitClose 由immutable Owner终止当前active VisitSession。
func (application *tcpGameplayApplication) VisitClose(ctx context.Context, operation tcpgameplay.OperationContext, payload *visitv1.VisitCloseCommand, rawCommandID []byte) (*visitv1.VisitCloseResponse, error) {
	visit, _, expected, commandID, err := application.visitMutationInput(ctx, operation, payload.GetExpectedRevision(), rawCommandID)
	if err != nil {
		return nil, err
	}
	result, err := application.visits.Close(ctx, operation.Auth, visit.ID(), expected, commandID)
	if err != nil {
		return nil, mapVisitError(err)
	}
	application.results.MutationCommitted(ctx, result)
	projected, err := application.projectMutation(ctx, result)
	if err != nil {
		return nil, err
	}
	return visitv1.VisitCloseResponse_builder{Result: projected}.Build(), nil
}

// joinOrReconnect 集中维护两个admission mutation的相同id/revision/投影边界。
func (application *tcpGameplayApplication) joinOrReconnect(ctx context.Context, operation tcpgameplay.OperationContext, qualification worldadmission.Qualification, revision uint64, rawCommandID []byte, reconnect bool) (*visitv1.VisitJoinResponse, error) {
	visit, bindingID, expected, commandID, err := application.visitMutationInput(ctx, operation, revision, rawCommandID)
	if err != nil {
		return nil, err
	}
	joinQualification, err := qualification.VisitSessionQualification()
	if err != nil {
		return nil, forbiddenPublicError()
	}
	var result visitsession.MutationResult
	if reconnect {
		result, err = application.visits.VisitorReconnect(ctx, operation.Auth, visit.ID(), joinQualification, bindingID, expected, commandID)
	} else {
		result, err = application.visits.Join(ctx, operation.Auth, visit.ID(), joinQualification, bindingID, expected, commandID)
	}
	if err != nil {
		return nil, mapVisitError(err)
	}
	application.results.MutationCommitted(ctx, result)
	projected, err := application.projectMutation(ctx, result)
	if err != nil {
		return nil, err
	}
	return visitv1.VisitJoinResponse_builder{Result: projected}.Build(), nil
}

// visitMutationInput 解析受信target、connection binding、revision与envelope command identity。
func (application *tcpGameplayApplication) visitMutationInput(ctx context.Context, operation tcpgameplay.OperationContext, revision uint64, rawCommandID []byte) (visitsession.Snapshot, visitsession.ConnectionBindingID, visitsession.Revision, visitsession.CommandID, error) {
	visit, err := application.resolveVisit(ctx, operation)
	if err != nil {
		return visitsession.Snapshot{}, visitsession.ConnectionBindingID{}, 0, visitsession.CommandID{}, err
	}
	bindingID, commandID, err := gameplayMutationIDs(operation.ConnectionID, hex.EncodeToString(rawCommandID))
	if err != nil {
		return visitsession.Snapshot{}, visitsession.ConnectionBindingID{}, 0, visitsession.CommandID{}, validationPublicError()
	}
	expected, err := visitsession.NewRevision(revision)
	if err != nil {
		return visitsession.Snapshot{}, visitsession.ConnectionBindingID{}, 0, visitsession.CommandID{}, validationPublicError()
	}
	return visit, bindingID, expected, commandID, nil
}

// resolveVisit 只从qualification binding解析target；Owner通过world active index读取。
func (application *tcpGameplayApplication) resolveVisit(ctx context.Context, operation tcpgameplay.OperationContext) (visitsession.Snapshot, error) {
	binding, err := application.binding(operation)
	if err != nil {
		return visitsession.Snapshot{}, err
	}
	snapshot, found, err := application.visits.ResolveActive(ctx, binding.WorldID())
	if err != nil {
		return visitsession.Snapshot{}, mapVisitError(err)
	}
	if !found || (binding.VisitSessionID().Valid() && snapshot.ID() != binding.VisitSessionID()) {
		return visitsession.Snapshot{}, tcpgameplay.PublicError{Code: 2100, MessageKey: "error.visit.not_found", CloseConnection: true}
	}
	application.results.SnapshotResolved(ctx, snapshot)
	return snapshot, nil
}

// binding 验证transport传入的只读上下文内部一致。
func (application *tcpGameplayApplication) binding(operation tcpgameplay.OperationContext) (worldadmission.Binding, error) {
	if operation.ConnectionID == "" || !operation.Auth.Valid() || !operation.Qualification.Valid() {
		return worldadmission.Binding{}, tcpgameplay.PublicError{Code: 101, MessageKey: "error.auth.forbidden", CloseConnection: true}
	}
	binding := operation.Qualification.Binding()
	if binding.PlayerID().String() != operation.Auth.Principal().PlayerID() || binding.SessionID() != operation.Auth.SessionID() || binding.Epoch() != operation.Auth.Epoch() || !binding.Endpoint().Equal(application.endpoint) {
		return worldadmission.Binding{}, tcpgameplay.PublicError{Code: 101, MessageKey: "error.auth.forbidden", CloseConnection: true}
	}
	return binding, nil
}

// currentAssignment 读取并完整比较stamp，禁止仅比较generation或客户端endpoint。
func (application *tcpGameplayApplication) currentAssignment(ctx context.Context, binding worldadmission.Binding) (placement.AssignmentSnapshot, error) {
	current, outcome, err := application.assignments.Resolve(ctx, binding.WorldID(), application.clock.Now().UTC())
	if err != nil {
		return placement.AssignmentSnapshot{}, dependencyPublicError()
	}
	if outcome != placement.ResolveOutcomeFound || !current.Valid() || !current.Stamp().Equal(binding.Assignment()) {
		return placement.AssignmentSnapshot{}, tcpgameplay.PublicError{Code: 2002, MessageKey: "error.world.assignment_stale", CloseConnection: true}
	}
	return current, nil
}

// projectPersonalWorld 只公开协议允许的持久事实。
func projectPersonalWorld(snapshot personalworld.Snapshot) *worldv1.PersonalWorldSnapshot {
	lifecycle := worldv1.PersonalWorldLifecycle(snapshot.Lifecycle())
	return worldv1.PersonalWorldSnapshot_builder{
		PersonalWorldId: proto.String(snapshot.ID().String()), OwnerPlayerId: proto.String(snapshot.OwnerID().String()),
		Lifecycle: &lifecycle, Revision: proto.Uint64(uint64(snapshot.Revision())), CreatedAtMs: proto.Int64(snapshot.CreatedAt().UnixMilli()),
	}.Build()
}

// projectAssignment 隐藏node与fencing token并使用受信advertised endpoint。
func (application *tcpGameplayApplication) projectAssignment(snapshot placement.AssignmentSnapshot) *worldv1.WorldAssignment {
	channel := sessionv1.TransportChannel_TRANSPORT_CHANNEL_TLS_TCP
	endpoint := sessionv1.Endpoint_builder{Channel: &channel, Host: proto.String(application.endpoint.Host()), Port: proto.Uint32(uint32(application.endpoint.Port()))}.Build()
	return worldv1.WorldAssignment_builder{
		PersonalWorldId: proto.String(snapshot.WorldID().String()), WorldInstanceId: proto.String(snapshot.InstanceID().String()), Endpoint: endpoint,
		Generation: proto.Uint64(uint64(snapshot.Generation())), LeaseExpiresAtMs: proto.Int64(snapshot.Lease().ExpiresAt().UnixMilli()),
	}.Build()
}

// projectVisit 生成不含session lineage、binding、invite集合与完整assignment stamp的替换快照。
func (application *tcpGameplayApplication) projectVisit(ctx context.Context, snapshot visitsession.Snapshot) (*visitv1.VisitSessionSnapshot, error) {
	current, outcome, err := application.assignments.Resolve(ctx, snapshot.WorldID(), application.clock.Now().UTC())
	if err != nil {
		return nil, dependencyPublicError()
	}
	if outcome != placement.ResolveOutcomeFound || !current.Valid() || !current.Stamp().Equal(snapshot.Assignment()) {
		return nil, tcpgameplay.PublicError{Code: 2002, MessageKey: "error.world.assignment_stale", CloseConnection: true}
	}
	lifecycle := visitv1.VisitLifecycle(snapshot.Lifecycle())
	visitors := make([]*visitv1.VisitVisitorSummary, 0, len(snapshot.Memberships()))
	for _, membership := range snapshot.Memberships() {
		state := visitv1.VisitMembershipState(membership.State())
		visitors = append(visitors, visitv1.VisitVisitorSummary_builder{PlayerId: proto.String(membership.VisitorID().String()), State: &state}.Build())
	}
	builder := visitv1.VisitSessionSnapshot_builder{
		VisitSessionId: proto.String(snapshot.ID().Value()), OwnerPlayerId: proto.String(snapshot.OwnerID().String()), Assignment: application.projectAssignment(current),
		Lifecycle: &lifecycle, Revision: proto.Uint64(snapshot.Revision().Uint64()), Capacity: proto.Uint32(uint32(snapshot.Capacity().Uint8())), Visitors: visitors,
		CreatedAtMs: proto.Int64(snapshot.CreatedAt().UnixMilli()), ExpiresAtMs: proto.Int64(snapshot.ExpiresAt().UnixMilli()),
	}
	if deadline := snapshot.OwnerGraceExpiresAt(); !deadline.IsZero() {
		builder.OwnerGraceExpiresAtMs = proto.Int64(deadline.UnixMilli())
	}
	return builder.Build(), nil
}

// projectMutation 投影首次提交或幂等replay的完整结果。
func (application *tcpGameplayApplication) projectMutation(ctx context.Context, result visitsession.MutationResult) (*visitv1.VisitMutationResult, error) {
	snapshot, err := application.projectVisit(ctx, result.Snapshot())
	if err != nil {
		return nil, err
	}
	directives := make([]*visitv1.SafeReturnDirective, 0, len(result.Directives()))
	for _, directive := range result.Directives() {
		reason := visitv1.SafeReturnReason(directive.Reason())
		preferred := visitv1.SafeReturnDestination(directive.PreferredDestination())
		fallback := visitv1.SafeReturnDestination(directive.FallbackDestination())
		directives = append(directives, visitv1.SafeReturnDirective_builder{
			VisitSessionId: proto.String(directive.VisitSessionID().Value()), VisitorId: proto.String(directive.VisitorID().String()),
			Reason: &reason, Preferred: &preferred, Fallback: &fallback,
		}.Build())
	}
	return visitv1.VisitMutationResult_builder{Snapshot: snapshot, SafeReturns: directives}.Build(), nil
}

// projectInvite 生成不含credential与membership内部字段的公开邀请。
func projectInvite(visitID visitsession.VisitSessionID, invite visitsession.InviteSnapshot) *visitv1.VisitInviteSummary {
	state := visitv1.VisitInviteState(invite.State())
	return visitv1.VisitInviteSummary_builder{
		InviteId: proto.String(invite.ID().Value()), VisitSessionId: proto.String(visitID.Value()), TargetVisitorId: proto.String(invite.TargetID().String()),
		State: &state, CreatedRevision: proto.Uint64(invite.CreatedRevision().Uint64()), ExpiresAtMs: proto.Int64(invite.ExpiresAt().UnixMilli()),
	}.Build()
}

// gameplayMutationIDs 从服务端ConnectionID与16-byte envelope identity构造领域命名空间ID。
func gameplayMutationIDs(connectionID string, commandIdentity string) (visitsession.ConnectionBindingID, visitsession.CommandID, error) {
	connectionMaterial, ok := strings.CutPrefix(connectionID, "tcp_")
	if !ok || connectionMaterial == "" {
		return visitsession.ConnectionBindingID{}, visitsession.CommandID{}, errors.New("tcp gameplay connection identity is invalid")
	}
	bindingID, err := visitsession.NewConnectionBindingID("vbind_" + connectionMaterial)
	if err != nil {
		return visitsession.ConnectionBindingID{}, visitsession.CommandID{}, err
	}
	commandID, err := visitsession.NewCommandID("vcmd_" + commandIdentity)
	if err != nil {
		return visitsession.ConnectionBindingID{}, visitsession.CommandID{}, err
	}
	return bindingID, commandID, nil
}

// revisionAndCommand 校验乐观版本与envelope command identity。
func revisionAndCommand(revision uint64, rawCommandID []byte) (visitsession.Revision, visitsession.CommandID, error) {
	expected, err := visitsession.NewRevision(revision)
	if err != nil {
		return 0, visitsession.CommandID{}, err
	}
	commandID, err := visitsession.NewCommandID("vcmd_" + hex.EncodeToString(rawCommandID))
	return expected, commandID, err
}

// mapVisitError 将领域封闭错误映射到errors.json登记语义，不编码cause文本。
func mapVisitError(err error) error {
	var domain *visitsession.Error
	if !errors.As(err, &domain) {
		return dependencyPublicError()
	}
	switch domain.Code() {
	case visitsession.ErrorCodeInvalidArgument:
		return validationPublicError()
	case visitsession.ErrorCodeForbidden:
		switch domain.Operation() {
		case visitsession.OperationJoin, visitsession.OperationLeave, visitsession.OperationVisitorReconnect:
			return tcpgameplay.PublicError{Code: 2107, MessageKey: "error.visit.membership_required"}
		}
		return forbiddenPublicError()
	case visitsession.ErrorCodeNotFound:
		if domain.Operation() == visitsession.OperationRevokeInvite || domain.Operation() == visitsession.OperationAcceptInvite {
			return tcpgameplay.PublicError{Code: 2101, MessageKey: "error.visit.invite_not_found"}
		}
		return tcpgameplay.PublicError{Code: 2100, MessageKey: "error.visit.not_found", CloseConnection: true}
	case visitsession.ErrorCodeInvalidState:
		return tcpgameplay.PublicError{Code: 2104, MessageKey: "error.visit.state_conflict"}
	case visitsession.ErrorCodeExpired:
		switch domain.Operation() {
		case visitsession.OperationRevokeInvite, visitsession.OperationAcceptInvite:
			return tcpgameplay.PublicError{Code: 2102, MessageKey: "error.visit.invite_expired"}
		case visitsession.OperationVisitorReconnect:
			return tcpgameplay.PublicError{Code: 2109, MessageKey: "error.visit.reconnect_expired"}
		default:
			return tcpgameplay.PublicError{Code: 2104, MessageKey: "error.visit.state_conflict"}
		}
	case visitsession.ErrorCodeCapacity:
		return tcpgameplay.PublicError{Code: 2103, MessageKey: "error.visit.capacity_exceeded"}
	case visitsession.ErrorCodeStale:
		switch domain.Operation() {
		case visitsession.OperationResolve, visitsession.OperationJoin, visitsession.OperationVisitorReconnect:
			return tcpgameplay.PublicError{Code: 2002, MessageKey: "error.world.assignment_stale", CloseConnection: true}
		}
		return tcpgameplay.PublicError{Code: 2105, MessageKey: "error.visit.revision_conflict"}
	case visitsession.ErrorCodeRevisionConflict:
		return tcpgameplay.PublicError{Code: 2105, MessageKey: "error.visit.revision_conflict"}
	case visitsession.ErrorCodeIdempotencyConflict:
		return tcpgameplay.PublicError{Code: 2106, MessageKey: "error.visit.idempotency_conflict"}
	case visitsession.ErrorCodeDependency, visitsession.ErrorCodeCommitUnknown:
		return dependencyPublicError()
	default:
		return tcpgameplay.PublicError{Code: 501, MessageKey: "error.internal", Retryable: true}
	}
}

// validationPublicError 返回共享输入校验错误。
func validationPublicError() error {
	return tcpgameplay.PublicError{Code: 200, MessageKey: "error.validation.failed"}
}

// forbiddenPublicError 返回共享授权拒绝错误。
func forbiddenPublicError() error {
	return tcpgameplay.PublicError{Code: 101, MessageKey: "error.auth.forbidden"}
}

// dependencyPublicError 返回不含 backend 文本的可重试错误，并关闭无法继续证明资格的旧 connection。
func dependencyPublicError() error {
	return tcpgameplay.PublicError{Code: 500, MessageKey: "error.dependency.unavailable", Retryable: true, CloseConnection: true}
}
