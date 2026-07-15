package visitsession

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
)

// Service 编排受信 actor、PersonalWorld、current assignment 与 VisitSessionStore。
//
// Service 不持有 socket、timer、backend client 或 mutable cache。所有运行态并发与 command
// replay 由 store 在线性化点决议；Service 负责重新读取权威 owner/assignment，并把任何
// 矛盾 adapter outcome 视为 dependency defect。所有失败都返回零值 application result；
// ctx 取消或 CommitUnknown 不能证明 mutation 未提交，调用方只能复用相同 CommandID 解析，
// 不得换新 identity 自动补写。依赖满足并发契约时实例可并发调用。
type Service struct {
	// store 持有 world active index、revision、command replay 与完整 mutation result。
	store VisitSessionStore
	// worlds 解析认证 Owner 的 active PersonalWorld 持久事实。
	worlds OwnedWorldReader
	// assignments 解析 placement owner 的 current active assignment 与 lease。
	assignments CurrentAssignmentReader
	// clock 为每个 application 调用提供一次 UTC 微秒 observedAt。
	clock Clock
	// ids 为 VisitSession 与 Invite 创建不可预测随机材料。
	ids IDGenerator
	// policy 固定容量、session 存续时间与各类 lifecycle deadline 配置上限。
	policy Policy
}

// NewService 校验并保留 VisitSession application 的全部必需依赖与 policy。
//
// 构造不会执行 I/O、启动 timer/goroutine 或把 service 注入正式 Composition Root。
func NewService(store VisitSessionStore, worlds OwnedWorldReader, assignments CurrentAssignmentReader, clock Clock, ids IDGenerator, policy Policy) (*Service, error) {
	if store == nil || worlds == nil || assignments == nil || clock == nil || ids == nil || !policy.Valid() {
		return nil, &Error{operation: OperationOpen, code: ErrorCodeInvalidArgument, cause: errors.New("visit session service dependencies are incomplete")}
	}
	return &Service{store: store, worlds: worlds, assignments: assignments, clock: clock, ids: ids, policy: policy}, nil
}

// OpenResult 是 world active index create/resolve 的安全 application 结果。
type OpenResult struct {
	// snapshot 是本次创建或已经存在的 active VisitSession。
	snapshot Snapshot
	// created 只在 store 明确确认本次提交新 candidate 时为 true。
	created bool
}

// Snapshot 返回 active VisitSession 完整值副本。
func (result OpenResult) Snapshot() Snapshot { return result.snapshot }

// Created 报告本次调用是否明确完成了新 create transaction。
func (result OpenResult) Created() bool { return result.created }

// Valid 报告 result 是否包含非 closed 的规范 snapshot。
func (result OpenResult) Valid() bool {
	return result.snapshot.Valid() && result.snapshot.Lifecycle() != LifecycleClosed
}

// String 防止默认格式化展开 active VisitSession identity 与 assignment。
func (OpenResult) String() string { return "[REDACTED_VISIT_OPEN_RESULT]" }

// GoString 防止 `%#v` 展开 application result 私有字段。
func (OpenResult) GoString() string { return "[REDACTED_VISIT_OPEN_RESULT]" }

// LogValue 让结构化日志只记录稳定占位文本。
func (OpenResult) LogValue() slog.Value { return slog.StringValue("[REDACTED_VISIT_OPEN_RESULT]") }

// Open 为受信 Owner 自己的 active PersonalWorld 创建或解析唯一 active VisitSession。
//
// auth、binding 与 commandID 必须来自认证、connection registry 与幂等调用边界，不能由
// payload identity 替代。ctx 取消不能证明 create 未提交；CommitUnknown 时调用方只能复用
// 相同 command identity，不能换新 CommandID 盲目补写。
func (service *Service) Open(ctx context.Context, auth session.AuthContext, bindingID ConnectionBindingID, commandID CommandID) (OpenResult, error) {
	if ctx == nil || !bindingID.Valid() || !commandID.Valid() {
		return OpenResult{}, domainError(OperationOpen, ErrorCodeInvalidArgument)
	}
	actor, err := ActorFromAuth(auth)
	if err != nil {
		return OpenResult{}, domainError(OperationOpen, ErrorCodeInvalidArgument)
	}
	observedAt, err := service.now(OperationOpen)
	if err != nil {
		return OpenResult{}, err
	}
	world, err := service.ownedWorld(ctx, actor, OperationOpen)
	if err != nil {
		return OpenResult{}, err
	}
	assignment, err := service.currentAssignment(ctx, world.ID(), observedAt, OperationOpen)
	if err != nil {
		return OpenResult{}, err
	}
	ownerBinding, _ := NewAuthBinding(actor, bindingID)
	visitID, err := service.newVisitSessionID()
	if err != nil {
		return OpenResult{}, &Error{operation: OperationOpen, code: ErrorCodeDependency, cause: err}
	}
	visit, err := NewVisitSession(visitID, ownerBinding, world.ID(), assignment, service.policy, observedAt)
	if err != nil {
		return OpenResult{}, err
	}
	fingerprint := fingerprintCommand(OperationOpen, actorFields(actor)...)
	fingerprint = extendFingerprint(fingerprint, bindingID.value, world.ID().String(), assignmentFields(assignment.Stamp()), strconv.Itoa(int(service.policy.capacity)), strconv.FormatInt(int64(service.policy.sessionLifetime), 10))
	record, err := NewCreateRecord(commandID, fingerprint, visit.Snapshot())
	if err != nil {
		return OpenResult{}, &Error{operation: OperationOpen, code: ErrorCodeDependencyDefect, cause: err}
	}
	result, outcome, storeErr := service.store.Create(ctx, record)
	return service.validateOpen(record, result, outcome, storeErr, assignment.Stamp())
}

// ResolveActive 返回指定 PersonalWorld 当前 active VisitSession，不创建任何运行态。
func (service *Service) ResolveActive(ctx context.Context, worldID personalworld.PersonalWorldID) (Snapshot, bool, error) {
	if ctx == nil || !worldID.Valid() {
		return Snapshot{}, false, domainError(OperationResolve, ErrorCodeInvalidArgument)
	}
	snapshot, outcome, err := service.store.ResolveActive(ctx, worldID)
	if err != nil {
		return Snapshot{}, false, &Error{operation: OperationResolve, code: ErrorCodeDependency, cause: err}
	}
	switch outcome {
	case ResolveOutcomeFound:
		if !snapshot.Valid() || snapshot.WorldID() != worldID || snapshot.Lifecycle() == LifecycleClosed {
			return Snapshot{}, false, &Error{operation: OperationResolve, code: ErrorCodeDependencyDefect}
		}
		return snapshot, true, nil
	case ResolveOutcomeNotFound:
		if !snapshot.empty() {
			return Snapshot{}, false, &Error{operation: OperationResolve, code: ErrorCodeDependencyDefect}
		}
		return Snapshot{}, false, nil
	default:
		return Snapshot{}, false, &Error{operation: OperationResolve, code: ErrorCodeDependencyDefect}
	}
}

// CreateInvite 由 Owner 为目标 Player 创建不预占 capacity 的定向邀请。
func (service *Service) CreateInvite(ctx context.Context, auth session.AuthContext, targetID account.PlayerID, expiresAt time.Time, expected Revision, commandID CommandID) (MutationResult, error) {
	actor, observedAt, err := service.actorAndTime(ctx, auth, OperationCreateInvite)
	if err != nil {
		return MutationResult{}, err
	}
	world, err := service.ownedWorld(ctx, actor, OperationCreateInvite)
	if err != nil {
		return MutationResult{}, err
	}
	snapshot, found, err := service.ResolveActive(ctx, world.ID())
	if err != nil {
		return MutationResult{}, err
	}
	if !found {
		return MutationResult{}, domainError(OperationCreateInvite, ErrorCodeNotFound)
	}
	fingerprint := commandFingerprint(OperationCreateInvite, snapshot.ID(), expected, actorFields(actor), targetID.String(), timeField(expiresAt))
	return service.commit(ctx, snapshot, expected, commandID, fingerprint, OperationCreateInvite, func(visit VisitSession) (mutationProposal, error) {
		if policyErr := validatePolicyDeadline(OperationCreateInvite, observedAt, expiresAt, minimumInviteLifetime, service.policy.inviteLifetime); policyErr != nil {
			return mutationProposal{}, policyErr
		}
		inviteID, idErr := service.newInviteID()
		if idErr != nil {
			return mutationProposal{}, &Error{operation: OperationCreateInvite, code: ErrorCodeDependency, cause: idErr}
		}
		target, invite, applyErr := visit.CreateInvite(actor, inviteID, targetID, expiresAt, observedAt)
		return mutationProposal{visit: target, invite: invite}, applyErr
	})
}

// RevokeInvite 由 Owner 撤销 matching pending invite。
func (service *Service) RevokeInvite(ctx context.Context, auth session.AuthContext, visitID VisitSessionID, inviteID InviteID, expected Revision, commandID CommandID) (MutationResult, error) {
	actor, observedAt, err := service.actorAndTime(ctx, auth, OperationRevokeInvite)
	if err != nil {
		return MutationResult{}, err
	}
	return service.commitByID(ctx, visitID, expected, commandID, commandFingerprint(OperationRevokeInvite, visitID, expected, actorFields(actor), inviteID.value), OperationRevokeInvite, func(visit VisitSession) (mutationProposal, error) {
		target, applyErr := visit.RevokeInvite(actor, inviteID, observedAt)
		return mutationProposal{visit: target}, applyErr
	})
}

// ExpireInvite 执行不伪造 Owner 的 matching system cleanup command。
func (service *Service) ExpireInvite(ctx context.Context, visitID VisitSessionID, inviteID InviteID, deadline time.Time, expected Revision, commandID CommandID) (MutationResult, error) {
	observedAt, err := service.contextTime(ctx, OperationExpireInvite)
	if err != nil {
		return MutationResult{}, err
	}
	return service.commitByID(ctx, visitID, expected, commandID, commandFingerprint(OperationExpireInvite, visitID, expected, nil, inviteID.value, timeField(deadline)), OperationExpireInvite, func(visit VisitSession) (mutationProposal, error) {
		target, applyErr := visit.ExpireInvite(inviteID, deadline, observedAt)
		return mutationProposal{visit: target}, applyErr
	})
}

// AcceptInvite 由邀请目标的 AuthContext 原子创建 reservation 与非凭据 AdmissionIntent。
func (service *Service) AcceptInvite(ctx context.Context, auth session.AuthContext, visitID VisitSessionID, inviteID InviteID, reservationExpiresAt time.Time, expected Revision, commandID CommandID) (MutationResult, error) {
	actor, observedAt, err := service.actorAndTime(ctx, auth, OperationAcceptInvite)
	if err != nil {
		return MutationResult{}, err
	}
	snapshot, err := service.find(ctx, visitID, OperationAcceptInvite)
	if err != nil {
		return MutationResult{}, err
	}
	assignment, err := service.currentAssignment(ctx, snapshot.WorldID(), observedAt, OperationAcceptInvite)
	if err != nil {
		return MutationResult{}, err
	}
	return service.acceptInvite(ctx, actor, observedAt, snapshot, assignment, inviteID, reservationExpiresAt, expected, commandID)
}

// acceptInvite 在一次权威读取后进入统一原子提交路径。
func (service *Service) acceptInvite(ctx context.Context, actor Actor, observedAt time.Time, snapshot Snapshot, assignment placement.AssignmentSnapshot, inviteID InviteID, reservationExpiresAt time.Time, expected Revision, commandID CommandID) (MutationResult, error) {
	fingerprint := commandFingerprint(OperationAcceptInvite, snapshot.ID(), expected, actorFields(actor), inviteID.value, timeField(reservationExpiresAt), assignmentFields(assignment.Stamp()))
	return service.commit(ctx, snapshot, expected, commandID, fingerprint, OperationAcceptInvite, func(visit VisitSession) (mutationProposal, error) {
		if policyErr := validatePolicyDeadline(OperationAcceptInvite, observedAt, reservationExpiresAt, minimumReservationLifetime, service.policy.reservationLifetime); policyErr != nil {
			return mutationProposal{}, policyErr
		}
		target, admission, applyErr := visit.AcceptInvite(actor, inviteID, reservationExpiresAt, assignment, observedAt)
		return mutationProposal{visit: target, admission: admission}, applyErr
	})
}

// AcceptInviteFromHTTP 由 VisitSession owner计算公开 accept 的最早 reservation deadline。
//
// deadline 同时受 policy、invite、aggregate、current assignment lease 与认证 session约束；
// transport 不能提交或延长该值。HTTP fingerprint只包含调用语义与权威assignment，不包含
// 每次重试都会变化的observedAt派生deadline；首次提交的完整结果仍由store原子保存并重放。
func (service *Service) AcceptInviteFromHTTP(ctx context.Context, authenticated session.AuthenticatedSession, visitID VisitSessionID, inviteID InviteID, expected Revision, commandID CommandID) (MutationResult, error) {
	if !authenticated.Valid() {
		return MutationResult{}, domainError(OperationAcceptInvite, ErrorCodeInvalidArgument)
	}
	auth := authenticated.AuthContext()
	actor, observedAt, err := service.actorAndTime(ctx, auth, OperationAcceptInvite)
	if err != nil {
		return MutationResult{}, err
	}
	snapshot, err := service.find(ctx, visitID, OperationAcceptInvite)
	if err != nil {
		return MutationResult{}, err
	}
	assignment, err := service.currentAssignment(ctx, snapshot.WorldID(), observedAt, OperationAcceptInvite)
	if err != nil {
		return MutationResult{}, err
	}
	var invite InviteSnapshot
	for _, candidate := range snapshot.Invites() {
		if candidate.ID() == inviteID {
			invite = candidate
			break
		}
	}
	if !invite.Valid() {
		return MutationResult{}, domainError(OperationAcceptInvite, ErrorCodeNotFound)
	}
	if invite.TargetID() != actor.playerID {
		return MutationResult{}, domainError(OperationAcceptInvite, ErrorCodeForbidden)
	}
	deadline := earliestDeadline(
		observedAt.Add(service.policy.reservationLifetime),
		invite.ExpiresAt(),
		snapshot.ExpiresAt(),
		assignment.Lease().ExpiresAt(),
		authenticated.Deadline(),
	)
	fingerprint := commandFingerprint(OperationAcceptInvite, snapshot.ID(), expected, actorFields(actor), inviteID.value, assignmentFields(assignment.Stamp()))
	if replay, resolved, replayErr := service.resolveCommandBeforeApply(ctx, snapshot, expected, commandID, fingerprint, OperationAcceptInvite); resolved {
		return replay, replayErr
	}
	if !assignment.Stamp().Equal(snapshot.Assignment()) {
		return MutationResult{}, domainError(OperationAcceptInvite, ErrorCodeStale)
	}
	return service.commit(ctx, snapshot, expected, commandID, fingerprint, OperationAcceptInvite, func(visit VisitSession) (mutationProposal, error) {
		if policyErr := validatePolicyDeadline(OperationAcceptInvite, observedAt, deadline, minimumReservationLifetime, service.policy.reservationLifetime); policyErr != nil {
			return mutationProposal{}, policyErr
		}
		target, admission, applyErr := visit.AcceptInvite(actor, inviteID, deadline, assignment, observedAt)
		return mutationProposal{visit: target, admission: admission}, applyErr
	})
}

// resolveCommandBeforeApply 让HTTP幂等identity在领域precondition之前由store原子决议。
//
// 无result probe在command不存在且revision匹配时返回InvalidState，表示可以继续构造首次
// target；其他outcome均为已提交replay/conflict或真实失败。并发首次请求仍由后续完整
// Commit在线性化点收敛，不能依据本次只读式probe声称尚未提交。
func (service *Service) resolveCommandBeforeApply(ctx context.Context, snapshot Snapshot, expected Revision, commandID CommandID, fingerprint CommandFingerprint, operation Operation) (MutationResult, bool, error) {
	probe, err := NewConflictProbe(operation, snapshot.ID(), expected, commandID, fingerprint)
	if err != nil {
		return MutationResult{}, true, &Error{operation: operation, code: ErrorCodeDependencyDefect, cause: err}
	}
	result, outcome, storeErr := service.store.Commit(ctx, probe)
	if outcome == MutationOutcomeInvalidState && storeErr == nil && mutationResultEmpty(result) && snapshot.Revision() == expected {
		return MutationResult{}, false, nil
	}
	returnResult, validationErr := service.validateMutation(snapshot, probe, MutationResult{}, result, outcome, storeErr)
	return returnResult, true, validationErr
}

// ResolveAdmissionEligibility 只读解析当前 actor 的 JOIN 或 RECONNECT 签发资格。
//
// 方法不创建 credential、membership、command或后台状态；assignment变更、lineage不匹配、
// deadline达到或非 reserved/reconnecting state 均 fail closed。
func (service *Service) ResolveAdmissionEligibility(ctx context.Context, authenticated session.AuthenticatedSession, visitID VisitSessionID) (AdmissionEligibility, error) {
	if !authenticated.Valid() {
		return AdmissionEligibility{}, domainError(OperationResolve, ErrorCodeInvalidArgument)
	}
	actor, observedAt, err := service.actorAndTime(ctx, authenticated.AuthContext(), OperationResolve)
	if err != nil {
		return AdmissionEligibility{}, err
	}
	snapshot, err := service.find(ctx, visitID, OperationResolve)
	if err != nil {
		return AdmissionEligibility{}, err
	}
	assignment, err := service.currentAssignment(ctx, snapshot.WorldID(), observedAt, OperationResolve)
	if err != nil {
		return AdmissionEligibility{}, err
	}
	if !assignment.Stamp().Equal(snapshot.Assignment()) {
		return AdmissionEligibility{}, domainError(OperationResolve, ErrorCodeStale)
	}
	var member MembershipSnapshot
	for _, candidate := range snapshot.Memberships() {
		if candidate.VisitorID() == actor.playerID {
			member = candidate
			break
		}
	}
	if !member.Valid() {
		return AdmissionEligibility{}, domainError(OperationResolve, ErrorCodeNotFound)
	}
	if member.SessionID() != authSessionID(actor) || member.Epoch() != authEpoch(actor) {
		return AdmissionEligibility{}, domainError(OperationResolve, ErrorCodeStale)
	}
	var purpose AdmissionPurpose
	var memberDeadline time.Time
	switch member.State() {
	case MembershipStateReserved:
		purpose, memberDeadline = AdmissionPurposeJoin, member.ReservationExpiresAt()
	case MembershipStateReconnecting:
		purpose, memberDeadline = AdmissionPurposeReconnect, member.ReconnectExpiresAt()
	default:
		return AdmissionEligibility{}, domainError(OperationResolve, ErrorCodeInvalidState)
	}
	deadline := earliestDeadline(memberDeadline, snapshot.ExpiresAt(), assignment.Lease().ExpiresAt(), authenticated.Deadline())
	if !observedAt.Before(deadline) {
		return AdmissionEligibility{}, domainError(OperationResolve, ErrorCodeExpired)
	}
	intent, err := HydrateAdmissionIntent(snapshot.ID(), actor.playerID, authSessionID(actor), authEpoch(actor), assignment.Stamp(), deadline)
	if err != nil {
		return AdmissionEligibility{}, &Error{operation: OperationResolve, code: ErrorCodeDependencyDefect, cause: err}
	}
	return AdmissionEligibility{intent: intent, purpose: purpose}, nil
}

// earliestDeadline 返回非空绝对时间中的最早值。
func earliestDeadline(deadlines ...time.Time) time.Time {
	var earliest time.Time
	for _, deadline := range deadlines {
		if !deadline.IsZero() && (earliest.IsZero() || deadline.Before(earliest)) {
			earliest = deadline
		}
	}
	return earliest
}

// authSessionID 从包内actor读取可信session lineage，集中避免桥接层重复访问私有字段。
func authSessionID(actor Actor) session.SessionID { return actor.sessionID }

// authEpoch 从包内actor读取可信撤销屏障。
func authEpoch(actor Actor) session.Epoch { return actor.epoch }

// Join 只消费包内受信 admission qualification，并重新确认 current assignment 与 lease。
func (service *Service) Join(ctx context.Context, auth session.AuthContext, visitID VisitSessionID, qualification JoinQualification, bindingID ConnectionBindingID, expected Revision, commandID CommandID) (MutationResult, error) {
	actor, observedAt, err := service.actorAndTime(ctx, auth, OperationJoin)
	if err != nil {
		return MutationResult{}, err
	}
	snapshot, err := service.find(ctx, visitID, OperationJoin)
	if err != nil {
		return MutationResult{}, err
	}
	assignment, err := service.currentAssignment(ctx, snapshot.WorldID(), observedAt, OperationJoin)
	if err != nil {
		return MutationResult{}, err
	}
	intent := qualification.intent
	fingerprint := commandFingerprint(OperationJoin, visitID, expected, actorFields(actor), bindingID.value, timeField(intent.expiresAt), assignmentFields(assignment.Stamp()))
	return service.commit(ctx, snapshot, expected, commandID, fingerprint, OperationJoin, func(visit VisitSession) (mutationProposal, error) {
		target, member, applyErr := visit.Join(actor, qualification, bindingID, assignment, observedAt)
		return mutationProposal{visit: target, membership: member}, applyErr
	})
}

// ExpireReservation 清理 matching 到期 reservation，不返回 safe-return。
func (service *Service) ExpireReservation(ctx context.Context, visitID VisitSessionID, visitorID account.PlayerID, inviteID InviteID, deadline time.Time, expected Revision, commandID CommandID) (MutationResult, error) {
	observedAt, err := service.contextTime(ctx, OperationExpireReservation)
	if err != nil {
		return MutationResult{}, err
	}
	return service.commitByID(ctx, visitID, expected, commandID, commandFingerprint(OperationExpireReservation, visitID, expected, nil, visitorID.String(), inviteID.value, timeField(deadline)), OperationExpireReservation, func(visit VisitSession) (mutationProposal, error) {
		target, applyErr := visit.ExpireReservation(visitorID, inviteID, deadline, observedAt)
		return mutationProposal{visit: target}, applyErr
	})
}

// Leave 只允许 Visitor 以自身当前 AuthContext 与 matching binding 移除 membership。
func (service *Service) Leave(ctx context.Context, auth session.AuthContext, visitID VisitSessionID, bindingID ConnectionBindingID, expected Revision, commandID CommandID) (MutationResult, error) {
	actor, _, err := service.actorAndTime(ctx, auth, OperationLeave)
	if err != nil {
		return MutationResult{}, err
	}
	return service.commitByID(ctx, visitID, expected, commandID, commandFingerprint(OperationLeave, visitID, expected, actorFields(actor), bindingID.value), OperationLeave, func(visit VisitSession) (mutationProposal, error) {
		target, directive, applyErr := visit.Leave(actor, bindingID)
		return mutationProposal{visit: target, directives: []SafeReturnDirective{directive}}, applyErr
	})
}

// Kick 只允许 immutable Owner 移除指定 joined/reconnecting Visitor。
func (service *Service) Kick(ctx context.Context, auth session.AuthContext, visitID VisitSessionID, visitorID account.PlayerID, expected Revision, commandID CommandID) (MutationResult, error) {
	actor, _, err := service.actorAndTime(ctx, auth, OperationKick)
	if err != nil {
		return MutationResult{}, err
	}
	return service.commitByID(ctx, visitID, expected, commandID, commandFingerprint(OperationKick, visitID, expected, actorFields(actor), visitorID.String()), OperationKick, func(visit VisitSession) (mutationProposal, error) {
		target, directive, applyErr := visit.Kick(actor, visitorID)
		return mutationProposal{visit: target, directives: []SafeReturnDirective{directive}}, applyErr
	})
}

// VisitorDisconnect 只对 matching lineage/binding 创建绝对 reconnect deadline。
func (service *Service) VisitorDisconnect(ctx context.Context, auth session.AuthContext, visitID VisitSessionID, bindingID ConnectionBindingID, deadline time.Time, expected Revision, commandID CommandID) (MutationResult, error) {
	actor, observedAt, err := service.actorAndTime(ctx, auth, OperationVisitorDisconnect)
	if err != nil {
		return MutationResult{}, err
	}
	return service.commitByID(ctx, visitID, expected, commandID, commandFingerprint(OperationVisitorDisconnect, visitID, expected, actorFields(actor), bindingID.value, timeField(deadline)), OperationVisitorDisconnect, func(visit VisitSession) (mutationProposal, error) {
		if policyErr := validatePolicyDeadline(OperationVisitorDisconnect, observedAt, deadline, minimumVisitorReconnectGrace, service.policy.visitorReconnectGrace); policyErr != nil {
			return mutationProposal{}, policyErr
		}
		target, applyErr := visit.VisitorDisconnect(actor, bindingID, deadline, observedAt)
		return mutationProposal{visit: target}, applyErr
	})
}

// VisitorReconnect 只消费 RECONNECT qualification，重新确认 current assignment 后以新 binding恢复。
func (service *Service) VisitorReconnect(ctx context.Context, auth session.AuthContext, visitID VisitSessionID, qualification JoinQualification, bindingID ConnectionBindingID, expected Revision, commandID CommandID) (MutationResult, error) {
	actor, observedAt, err := service.actorAndTime(ctx, auth, OperationVisitorReconnect)
	if err != nil {
		return MutationResult{}, err
	}
	snapshot, err := service.find(ctx, visitID, OperationVisitorReconnect)
	if err != nil {
		return MutationResult{}, err
	}
	assignment, err := service.currentAssignment(ctx, snapshot.WorldID(), observedAt, OperationVisitorReconnect)
	if err != nil {
		return MutationResult{}, err
	}
	intent := qualification.intent
	fingerprint := commandFingerprint(OperationVisitorReconnect, visitID, expected, actorFields(actor), bindingID.value, timeField(intent.expiresAt), assignmentFields(assignment.Stamp()))
	return service.commit(ctx, snapshot, expected, commandID, fingerprint, OperationVisitorReconnect, func(visit VisitSession) (mutationProposal, error) {
		target, member, applyErr := visit.VisitorReconnect(actor, qualification, bindingID, assignment, observedAt)
		return mutationProposal{visit: target, membership: member}, applyErr
	})
}

// ExpireVisitorReconnect 清理 matching generation/binding/deadline 并 replay 完整 directive。
func (service *Service) ExpireVisitorReconnect(ctx context.Context, visitID VisitSessionID, visitorID account.PlayerID, generation uint64, bindingID ConnectionBindingID, deadline time.Time, expected Revision, commandID CommandID) (MutationResult, error) {
	observedAt, err := service.contextTime(ctx, OperationExpireVisitorReconnect)
	if err != nil {
		return MutationResult{}, err
	}
	fingerprint := commandFingerprint(OperationExpireVisitorReconnect, visitID, expected, nil, visitorID.String(), strconv.FormatUint(generation, 10), bindingID.value, timeField(deadline))
	return service.commitByID(ctx, visitID, expected, commandID, fingerprint, OperationExpireVisitorReconnect, func(visit VisitSession) (mutationProposal, error) {
		target, directive, applyErr := visit.ExpireVisitorReconnect(visitorID, generation, bindingID, deadline, observedAt)
		return mutationProposal{visit: target, directives: []SafeReturnDirective{directive}}, applyErr
	})
}

// OwnerDisconnect 只对当前完整 Owner binding 建立绝对 grace deadline。
func (service *Service) OwnerDisconnect(ctx context.Context, auth session.AuthContext, visitID VisitSessionID, bindingID ConnectionBindingID, deadline time.Time, expected Revision, commandID CommandID) (MutationResult, error) {
	actor, observedAt, err := service.actorAndTime(ctx, auth, OperationOwnerDisconnect)
	if err != nil {
		return MutationResult{}, err
	}
	return service.commitByID(ctx, visitID, expected, commandID, commandFingerprint(OperationOwnerDisconnect, visitID, expected, actorFields(actor), bindingID.value, timeField(deadline)), OperationOwnerDisconnect, func(visit VisitSession) (mutationProposal, error) {
		if policyErr := validatePolicyDeadline(OperationOwnerDisconnect, observedAt, deadline, minimumOwnerGrace, service.policy.ownerGrace); policyErr != nil {
			return mutationProposal{}, policyErr
		}
		target, applyErr := visit.OwnerDisconnect(actor, bindingID, deadline, observedAt)
		return mutationProposal{visit: target}, applyErr
	})
}

// OwnerReconnect 只允许 immutable Owner 的当前 AuthContext 更新 owner binding。
func (service *Service) OwnerReconnect(ctx context.Context, auth session.AuthContext, visitID VisitSessionID, bindingID ConnectionBindingID, expected Revision, commandID CommandID) (MutationResult, error) {
	actor, observedAt, err := service.actorAndTime(ctx, auth, OperationOwnerReconnect)
	if err != nil {
		return MutationResult{}, err
	}
	snapshot, err := service.find(ctx, visitID, OperationOwnerReconnect)
	if err != nil {
		return MutationResult{}, err
	}
	assignment, err := service.currentAssignment(ctx, snapshot.WorldID(), observedAt, OperationOwnerReconnect)
	if err != nil {
		return MutationResult{}, err
	}
	fingerprint := commandFingerprint(OperationOwnerReconnect, visitID, expected, actorFields(actor), bindingID.value, assignmentFields(assignment.Stamp()))
	return service.commit(ctx, snapshot, expected, commandID, fingerprint, OperationOwnerReconnect, func(visit VisitSession) (mutationProposal, error) {
		target, applyErr := visit.OwnerReconnect(actor, bindingID, assignment, observedAt)
		return mutationProposal{visit: target}, applyErr
	})
}

// Close 只允许 immutable Owner terminal close，并把完整 directives 保存为 replay result。
func (service *Service) Close(ctx context.Context, auth session.AuthContext, visitID VisitSessionID, expected Revision, commandID CommandID) (MutationResult, error) {
	actor, _, err := service.actorAndTime(ctx, auth, OperationClose)
	if err != nil {
		return MutationResult{}, err
	}
	return service.commitByID(ctx, visitID, expected, commandID, commandFingerprint(OperationClose, visitID, expected, actorFields(actor)), OperationClose, func(visit VisitSession) (mutationProposal, error) {
		target, directives, applyErr := visit.Close(actor)
		return mutationProposal{visit: target, directives: directives}, applyErr
	})
}

// ExpireOwnerGrace 不伪造 Owner，只比较 generation、旧 binding 与真实 deadline。
func (service *Service) ExpireOwnerGrace(ctx context.Context, visitID VisitSessionID, generation uint64, bindingID ConnectionBindingID, deadline time.Time, expected Revision, commandID CommandID) (MutationResult, error) {
	observedAt, err := service.contextTime(ctx, OperationExpireOwnerGrace)
	if err != nil {
		return MutationResult{}, err
	}
	fingerprint := commandFingerprint(OperationExpireOwnerGrace, visitID, expected, nil, strconv.FormatUint(generation, 10), bindingID.value, timeField(deadline))
	return service.commitByID(ctx, visitID, expected, commandID, fingerprint, OperationExpireOwnerGrace, func(visit VisitSession) (mutationProposal, error) {
		target, directives, applyErr := visit.ExpireOwnerGrace(generation, bindingID, deadline, observedAt)
		return mutationProposal{visit: target, directives: directives}, applyErr
	})
}

// ExpireSession 在 matching absolute deadline 到达时保存完整 terminal result。
func (service *Service) ExpireSession(ctx context.Context, visitID VisitSessionID, deadline time.Time, expected Revision, commandID CommandID) (MutationResult, error) {
	observedAt, err := service.contextTime(ctx, OperationExpireSession)
	if err != nil {
		return MutationResult{}, err
	}
	return service.commitByID(ctx, visitID, expected, commandID, commandFingerprint(OperationExpireSession, visitID, expected, nil, timeField(deadline)), OperationExpireSession, func(visit VisitSession) (mutationProposal, error) {
		target, directives, applyErr := visit.ExpireSession(deadline, observedAt)
		return mutationProposal{visit: target, directives: directives}, applyErr
	})
}

// InvalidateAssignment 仅在 placement 证明旧 assignment 已非 current 时关闭访问。
func (service *Service) InvalidateAssignment(ctx context.Context, visitID VisitSessionID, expected Revision, commandID CommandID) (MutationResult, error) {
	observedAt, err := service.contextTime(ctx, OperationInvalidateAssignment)
	if err != nil {
		return MutationResult{}, err
	}
	snapshot, err := service.find(ctx, visitID, OperationInvalidateAssignment)
	if err != nil {
		return MutationResult{}, err
	}
	if err := service.requireAssignmentInvalidation(ctx, snapshot, observedAt); err != nil {
		return MutationResult{}, err
	}
	return service.commit(ctx, snapshot, expected, commandID, commandFingerprint(OperationInvalidateAssignment, visitID, expected, nil), OperationInvalidateAssignment, func(visit VisitSession) (mutationProposal, error) {
		target, directives, applyErr := visit.InvalidateAssignment()
		return mutationProposal{visit: target, directives: directives}, applyErr
	})
}

// LoseDependency 在运行态事实无法安全恢复时 fail closed，不伪造持久 world 事实。
func (service *Service) LoseDependency(ctx context.Context, visitID VisitSessionID, expected Revision, commandID CommandID) (MutationResult, error) {
	if _, err := service.contextTime(ctx, OperationDependencyLost); err != nil {
		return MutationResult{}, err
	}
	return service.commitByID(ctx, visitID, expected, commandID, commandFingerprint(OperationDependencyLost, visitID, expected, nil), OperationDependencyLost, func(visit VisitSession) (mutationProposal, error) {
		target, directives, applyErr := visit.LoseDependency()
		return mutationProposal{visit: target, directives: directives}, applyErr
	})
}

// mutationProposal 保存 domain transition 的 target 与 operation-specific result payload。
type mutationProposal struct {
	// visit 是 domain transition 返回的完整 target aggregate。
	visit VisitSession
	// invite 只由 create-invite transition 填充。
	invite InviteSnapshot
	// admission 只由 accept transition 填充且仍不具备 credential 权限。
	admission AdmissionIntent
	// membership 只由 join/reconnect transition 填充。
	membership MembershipSnapshot
	// directives 是必须与 snapshot 原子提交并完整 replay 的稳定结果。
	directives []SafeReturnDirective
}

// commitByID 严格读取 snapshot 后进入统一 CAS/replay 路径。
func (service *Service) commitByID(ctx context.Context, visitID VisitSessionID, expected Revision, commandID CommandID, fingerprint CommandFingerprint, operation Operation, apply func(VisitSession) (mutationProposal, error)) (MutationResult, error) {
	snapshot, err := service.find(ctx, visitID, operation)
	if err != nil {
		return MutationResult{}, err
	}
	return service.commit(ctx, snapshot, expected, commandID, fingerprint, operation, apply)
}

// commit 对 revision 匹配生成完整 target；不匹配时只发 replay/conflict probe。
func (service *Service) commit(ctx context.Context, snapshot Snapshot, expected Revision, commandID CommandID, fingerprint CommandFingerprint, operation Operation, apply func(VisitSession) (mutationProposal, error)) (MutationResult, error) {
	if ctx == nil || !snapshot.Valid() || !expected.Valid() || !commandID.Valid() || !fingerprint.Valid() {
		return MutationResult{}, domainError(operation, ErrorCodeInvalidArgument)
	}
	var record TransitionRecord
	var proposalResult MutationResult
	var err error
	if snapshot.Revision() == expected {
		visit, hydrateErr := HydrateVisitSession(snapshot)
		if hydrateErr != nil {
			return MutationResult{}, &Error{operation: operation, code: ErrorCodeDependencyDefect, cause: hydrateErr}
		}
		proposal, applyErr := apply(visit)
		if applyErr != nil {
			return MutationResult{}, applyErr
		}
		proposalResult, err = NewMutationResult(operation, proposal.visit.Snapshot(), commandID, fingerprint, proposal.invite, proposal.admission, proposal.membership, proposal.directives)
		if err != nil {
			return MutationResult{}, &Error{operation: operation, code: ErrorCodeDependencyDefect, cause: err}
		}
		record, err = NewTransitionRecord(operation, snapshot.ID(), expected, commandID, fingerprint, proposalResult)
	} else {
		record, err = NewConflictProbe(operation, snapshot.ID(), expected, commandID, fingerprint)
	}
	if err != nil {
		return MutationResult{}, &Error{operation: operation, code: ErrorCodeDependencyDefect, cause: err}
	}
	result, outcome, storeErr := service.store.Commit(ctx, record)
	return service.validateMutation(snapshot, record, proposalResult, result, outcome, storeErr)
}

// validateMutation 将矛盾 outcome/result 统一映射为 dependency defect。
func (service *Service) validateMutation(observed Snapshot, record TransitionRecord, proposal, result MutationResult, outcome MutationOutcome, storeErr error) (MutationResult, error) {
	operation := record.operation
	if outcome == MutationOutcomeApplied || outcome == MutationOutcomeReplay {
		if storeErr != nil || result.validate() != nil || result.operation != operation || result.commandID != record.commandID || !result.fingerprint.Equal(record.fingerprint) || result.snapshot.ID() != record.visitSessionID || result.snapshot.Revision() != record.expectedRevision+1 || !immutableSnapshotFactsEqual(result.snapshot, observed) {
			return MutationResult{}, &Error{operation: operation, code: ErrorCodeDependencyDefect, phase: CommitPhaseCommitted, cause: storeErr}
		}
		if outcome == MutationOutcomeApplied && (!record.HasResult() || !result.snapshot.Equal(proposal.snapshot) || !mutationPayloadEqual(result, proposal)) {
			return MutationResult{}, &Error{operation: operation, code: ErrorCodeDependencyDefect, phase: CommitPhaseCommitted}
		}
		return result, nil
	}
	if !mutationResultEmpty(result) {
		return MutationResult{}, &Error{operation: operation, code: ErrorCodeDependencyDefect, cause: storeErr}
	}
	if storeErr != nil && outcome != MutationOutcomeNotCommitted && outcome != MutationOutcomeCommitUnknown {
		return MutationResult{}, &Error{operation: operation, code: ErrorCodeDependencyDefect, cause: storeErr}
	}
	switch outcome {
	case MutationOutcomeNotFound:
		return MutationResult{}, &Error{operation: operation, code: ErrorCodeNotFound}
	case MutationOutcomeRevisionConflict:
		return MutationResult{}, &Error{operation: operation, code: ErrorCodeRevisionConflict}
	case MutationOutcomeIdempotencyConflict:
		return MutationResult{}, &Error{operation: operation, code: ErrorCodeIdempotencyConflict}
	case MutationOutcomeCapacityConflict:
		return MutationResult{}, &Error{operation: operation, code: ErrorCodeCapacity}
	case MutationOutcomeStale:
		return MutationResult{}, &Error{operation: operation, code: ErrorCodeStale}
	case MutationOutcomeInvalidState:
		return MutationResult{}, &Error{operation: operation, code: ErrorCodeInvalidState}
	case MutationOutcomeNotCommitted:
		return MutationResult{}, &Error{operation: operation, code: ErrorCodeDependency, phase: CommitPhaseNotCommitted, cause: storeErr}
	case MutationOutcomeCommitUnknown:
		return MutationResult{}, &Error{operation: operation, code: ErrorCodeCommitUnknown, phase: CommitPhaseUnknown, cause: storeErr}
	default:
		return MutationResult{}, &Error{operation: operation, code: ErrorCodeDependencyDefect, cause: storeErr}
	}
}

// immutableSnapshotFactsEqual 防止 replay 结果替换 Owner、world、assignment、capacity 或 lifecycle 时间轴。
func immutableSnapshotFactsEqual(left, right Snapshot) bool {
	return left.ID() == right.ID() && left.OwnerID() == right.OwnerID() && left.WorldID() == right.WorldID() &&
		left.Assignment().Equal(right.Assignment()) && left.Capacity() == right.Capacity() &&
		left.CreatedAt().Equal(right.CreatedAt()) && left.ExpiresAt().Equal(right.ExpiresAt())
}

// validateOpen 严格校验 created/existing/replay 与 command/result 组合。
func (service *Service) validateOpen(record CreateRecord, result CreateResult, outcome CreateOutcome, storeErr error, current placement.AssignmentStamp) (OpenResult, error) {
	validSnapshot := result.snapshot.Valid() && result.snapshot.WorldID() == record.candidate.WorldID() && result.snapshot.OwnerID() == record.candidate.OwnerID() && result.snapshot.Lifecycle() != LifecycleClosed
	switch outcome {
	case CreateOutcomeCreated:
		if storeErr != nil || !validSnapshot || !result.snapshot.Equal(record.candidate) || result.commandID != record.commandID || !result.fingerprint.Equal(record.fingerprint) {
			return OpenResult{}, &Error{operation: OperationOpen, code: ErrorCodeDependencyDefect, phase: CommitPhaseCommitted, cause: storeErr}
		}
		return OpenResult{snapshot: result.snapshot, created: true}, nil
	case CreateOutcomeReplay:
		if storeErr != nil || !validSnapshot || result.commandID != record.commandID || !result.fingerprint.Equal(record.fingerprint) {
			return OpenResult{}, &Error{operation: OperationOpen, code: ErrorCodeDependencyDefect, phase: CommitPhaseCommitted, cause: storeErr}
		}
		return OpenResult{snapshot: result.snapshot}, nil
	case CreateOutcomeExisting:
		if storeErr != nil || !validSnapshot || result.commandID.Valid() || result.fingerprint.Valid() {
			return OpenResult{}, &Error{operation: OperationOpen, code: ErrorCodeDependencyDefect, cause: storeErr}
		}
		if !result.snapshot.Assignment().Equal(current) {
			return OpenResult{}, &Error{operation: OperationOpen, code: ErrorCodeStale}
		}
		return OpenResult{snapshot: result.snapshot}, nil
	case CreateOutcomeIdempotencyConflict:
		if storeErr != nil || validSnapshot {
			return OpenResult{}, &Error{operation: OperationOpen, code: ErrorCodeDependencyDefect, cause: storeErr}
		}
		return OpenResult{}, &Error{operation: OperationOpen, code: ErrorCodeIdempotencyConflict}
	case CreateOutcomeNotCommitted:
		if validSnapshot {
			return OpenResult{}, &Error{operation: OperationOpen, code: ErrorCodeDependencyDefect, cause: storeErr}
		}
		return OpenResult{}, &Error{operation: OperationOpen, code: ErrorCodeDependency, phase: CommitPhaseNotCommitted, cause: storeErr}
	case CreateOutcomeCommitUnknown:
		if validSnapshot {
			return OpenResult{}, &Error{operation: OperationOpen, code: ErrorCodeDependencyDefect, cause: storeErr}
		}
		return OpenResult{}, &Error{operation: OperationOpen, code: ErrorCodeCommitUnknown, phase: CommitPhaseUnknown, cause: storeErr}
	default:
		return OpenResult{}, &Error{operation: OperationOpen, code: ErrorCodeDependencyDefect, cause: storeErr}
	}
}

// find 严格校验按 ID 读取的 outcome 与 snapshot identity。
func (service *Service) find(ctx context.Context, visitID VisitSessionID, operation Operation) (Snapshot, error) {
	if ctx == nil || !visitID.Valid() {
		return Snapshot{}, domainError(operation, ErrorCodeInvalidArgument)
	}
	snapshot, outcome, err := service.store.FindByID(ctx, visitID)
	if err != nil {
		return Snapshot{}, &Error{operation: operation, code: ErrorCodeDependency, cause: err}
	}
	if outcome == ResolveOutcomeNotFound && snapshot.empty() {
		return Snapshot{}, domainError(operation, ErrorCodeNotFound)
	}
	if outcome != ResolveOutcomeFound || !snapshot.Valid() || snapshot.ID() != visitID {
		return Snapshot{}, &Error{operation: operation, code: ErrorCodeDependencyDefect}
	}
	return snapshot, nil
}

// ownedWorld 验证 reader 返回 active、Owner 精确匹配的 PersonalWorld snapshot。
func (service *Service) ownedWorld(ctx context.Context, actor Actor, operation Operation) (personalworld.Snapshot, error) {
	snapshot, outcome, err := service.worlds.ResolveOwnedWorld(ctx, actor.playerID)
	if err != nil {
		return personalworld.Snapshot{}, &Error{operation: operation, code: ErrorCodeDependency, cause: err}
	}
	if outcome == OwnedWorldOutcomeNotFound && snapshot == (personalworld.Snapshot{}) {
		return personalworld.Snapshot{}, domainError(operation, ErrorCodeNotFound)
	}
	if outcome != OwnedWorldOutcomeFound || !snapshot.Valid() || snapshot.OwnerID() != actor.playerID || snapshot.Lifecycle() != personalworld.LifecycleActive {
		return personalworld.Snapshot{}, &Error{operation: operation, code: ErrorCodeDependencyDefect}
	}
	return snapshot, nil
}

// currentAssignment 验证 reader 返回目标 world 的 active、lease 当前有效完整 snapshot。
func (service *Service) currentAssignment(ctx context.Context, worldID personalworld.PersonalWorldID, observedAt time.Time, operation Operation) (placement.AssignmentSnapshot, error) {
	snapshot, outcome, err := service.assignments.ResolveCurrent(ctx, worldID, observedAt)
	if err != nil {
		return placement.AssignmentSnapshot{}, &Error{operation: operation, code: ErrorCodeDependency, cause: err}
	}
	if outcome == AssignmentOutcomeNotFound && snapshot == (placement.AssignmentSnapshot{}) {
		return placement.AssignmentSnapshot{}, domainError(operation, ErrorCodeStale)
	}
	if outcome != AssignmentOutcomeFound || !snapshot.Valid() || snapshot.WorldID() != worldID {
		return placement.AssignmentSnapshot{}, &Error{operation: operation, code: ErrorCodeDependencyDefect}
	}
	if snapshot.Phase() != placement.PhaseActive || !snapshot.ValidAt(observedAt) {
		return placement.AssignmentSnapshot{}, domainError(operation, ErrorCodeStale)
	}
	return snapshot, nil
}

// requireAssignmentInvalidation 区分权威 missing/expired/replaced 证据、正常 current assignment 与依赖故障。
func (service *Service) requireAssignmentInvalidation(ctx context.Context, visit Snapshot, observedAt time.Time) error {
	current, outcome, err := service.assignments.ResolveCurrent(ctx, visit.WorldID(), observedAt)
	if err != nil {
		return &Error{operation: OperationInvalidateAssignment, code: ErrorCodeDependency, cause: err}
	}
	switch outcome {
	case AssignmentOutcomeNotFound:
		if current != (placement.AssignmentSnapshot{}) {
			return &Error{operation: OperationInvalidateAssignment, code: ErrorCodeDependencyDefect}
		}
		return nil
	case AssignmentOutcomeFound:
		if !current.Valid() || current.WorldID() != visit.WorldID() {
			return &Error{operation: OperationInvalidateAssignment, code: ErrorCodeDependencyDefect}
		}
		if current.Phase() == placement.PhaseActive && current.ValidAt(observedAt) && current.Stamp().Equal(visit.Assignment()) {
			return domainError(OperationInvalidateAssignment, ErrorCodeInvalidState)
		}
		return nil
	default:
		return &Error{operation: OperationInvalidateAssignment, code: ErrorCodeDependencyDefect}
	}
}

// actorAndTime 统一验证 context/AuthContext 并为一次调用读取单个 observedAt。
func (service *Service) actorAndTime(ctx context.Context, auth session.AuthContext, operation Operation) (Actor, time.Time, error) {
	if ctx == nil {
		return Actor{}, time.Time{}, domainError(operation, ErrorCodeInvalidArgument)
	}
	actor, err := ActorFromAuth(auth)
	if err != nil {
		return Actor{}, time.Time{}, domainError(operation, ErrorCodeInvalidArgument)
	}
	observedAt, err := service.now(operation)
	return actor, observedAt, err
}

// contextTime 验证 context 并读取单个 observedAt。
func (service *Service) contextTime(ctx context.Context, operation Operation) (time.Time, error) {
	if ctx == nil {
		return time.Time{}, domainError(operation, ErrorCodeInvalidArgument)
	}
	return service.now(operation)
}

// now 规范化 clock 输出并拒绝零时间。
func (service *Service) now(operation Operation) (time.Time, error) {
	now := canonicalOptionalTime(service.clock.Now())
	if now.IsZero() {
		return time.Time{}, &Error{operation: operation, code: ErrorCodeDependencyDefect}
	}
	return now, nil
}

// newVisitSessionID 为通用随机材料添加 VisitSession namespace。
func (service *Service) newVisitSessionID() (VisitSessionID, error) {
	raw, err := service.ids.NewID()
	if err != nil {
		return VisitSessionID{}, err
	}
	return NewVisitSessionID(visitSessionIDPrefix + raw)
}

// newInviteID 为通用随机材料添加 Invite namespace。
func (service *Service) newInviteID() (InviteID, error) {
	raw, err := service.ids.NewID()
	if err != nil {
		return InviteID{}, err
	}
	return NewInviteID(inviteIDPrefix + raw)
}

// commandFingerprint 编码 session、expected revision、actor 与 operation-specific 稳定字段。
func commandFingerprint(operation Operation, visitID VisitSessionID, expected Revision, actor []string, fields ...string) CommandFingerprint {
	values := []string{visitID.value, strconv.FormatUint(expected.Uint64(), 10)}
	values = append(values, actor...)
	values = append(values, fields...)
	return fingerprintCommand(operation, values...)
}

// validatePolicyDeadline 在首次生成 target 时执行 configured policy 上限；replay probe 不重新解释推进后的 observedAt。
func validatePolicyDeadline(operation Operation, observedAt, deadline time.Time, minimum, maximum time.Duration) error {
	observedAt, deadline = canonicalOptionalTime(observedAt), canonicalOptionalTime(deadline)
	lifetime := deadline.Sub(observedAt)
	if observedAt.IsZero() || deadline.IsZero() || lifetime < minimum || lifetime > maximum {
		return domainError(operation, ErrorCodeInvalidArgument)
	}
	return nil
}

// actorFields 返回 fingerprint 必须绑定的认证 PlayerID、SessionID 与 epoch。
func actorFields(actor Actor) []string {
	return []string{actor.playerID.String(), actor.sessionID.String(), strconv.FormatUint(uint64(actor.epoch), 10)}
}

// assignmentFields 返回完整 AssignmentStamp 的规范 fingerprint 字段。
func assignmentFields(stamp placement.AssignmentStamp) string {
	if !stamp.Valid() {
		return ""
	}
	return stamp.WorldID().String() + "/" + stamp.InstanceID().String() + "/" + stamp.NodeID().String() + "/" + strconv.FormatUint(stamp.Generation().Uint64(), 10) + "/" + strconv.FormatUint(stamp.FencingToken().Uint64(), 10)
}

// timeField 返回 UTC 微秒绝对时间的无时区歧义整数表达。
func timeField(value time.Time) string {
	if value.IsZero() {
		return "0"
	}
	return strconv.FormatInt(canonicalTime(value).UnixMicro(), 10)
}

// extendFingerprint 把已规范字段继续绑定到新的 domain-separated SHA-256 输入。
func extendFingerprint(base CommandFingerprint, fields ...string) CommandFingerprint {
	values := []string{string(base.digest[:])}
	values = append(values, fields...)
	return fingerprintCommand(OperationOpen, values...)
}

// mutationPayloadEqual 比较 store applied result 与 application proposal 的完整 replay payload。
func mutationPayloadEqual(left, right MutationResult) bool {
	if left.invite != right.invite || left.admission != right.admission || left.membership != right.membership || len(left.directives) != len(right.directives) {
		return false
	}
	for index := range left.directives {
		if left.directives[index] != right.directives[index] {
			return false
		}
	}
	return true
}

// mutationResultEmpty 报告非成功 outcome 是否错误携带部分 result。
func mutationResultEmpty(result MutationResult) bool {
	return result.operation == OperationUnspecified && !result.snapshot.Valid() && !result.commandID.Valid() && !result.fingerprint.Valid() && !result.invite.Valid() && !result.admission.Valid() && !result.membership.Valid() && len(result.directives) == 0
}

// empty 报告 snapshot 是否严格没有任何部分填充字段。
func (snapshot Snapshot) empty() bool {
	return !snapshot.id.Valid() && !snapshot.ownerID.Valid() && !snapshot.worldID.Valid() && !snapshot.assignment.Valid() && snapshot.lifecycle == LifecycleUnspecified && !snapshot.revision.Valid() && !snapshot.capacity.Valid() && snapshot.createdAt.IsZero() && snapshot.expiresAt.IsZero() && !snapshot.ownerBinding.Valid() && snapshot.ownerGraceGeneration == 0 && snapshot.ownerGraceExpiresAt.IsZero() && len(snapshot.invites) == 0 && len(snapshot.memberships) == 0
}
