package visitsession

import (
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
)

// VisitSession 是个人世界临时访客资格的窄 aggregate。
//
// Aggregate 使用值语义，不持有 socket、timer、store transaction 或 mutable global。
// 调用方必须把 transition 返回的新 snapshot 交给 VisitSessionStore 以 expected revision
// 原子提交；失败时 receiver 与输入集合均不会被修改。
type VisitSession struct {
	// snapshot 保存完整 immutable binding 与当前 invite/membership 运行态投影。
	snapshot Snapshot
}

// NewVisitSession 创建绑定 current active assignment、revision 1 且没有 Visitor 的 aggregate。
func NewVisitSession(id VisitSessionID, ownerBinding AuthBinding, worldID personalworld.PersonalWorldID, assignment placement.AssignmentSnapshot, policy Policy, createdAt time.Time) (VisitSession, error) {
	createdAt = canonicalOptionalTime(createdAt)
	if !id.Valid() || !ownerBinding.Valid() || !worldID.Valid() || !assignment.ValidAt(createdAt) || assignment.Phase() != placement.PhaseActive || assignment.WorldID() != worldID || !policy.Valid() {
		return VisitSession{}, domainError(OperationOpen, ErrorCodeInvalidArgument)
	}
	expiresAt, err := deadlineAt(createdAt, policy.SessionLifetime())
	if err != nil {
		return VisitSession{}, domainError(OperationOpen, ErrorCodeInvalidArgument)
	}
	snapshot, err := NewSnapshot(id, ownerBinding.actor.playerID, worldID, assignment.Stamp(), LifecycleOpen, InitialRevision, policy.Capacity(), createdAt, expiresAt, ownerBinding, 0, time.Time{}, nil, nil)
	if err != nil {
		return VisitSession{}, domainError(OperationOpen, ErrorCodeInvalidArgument)
	}
	return VisitSession{snapshot: snapshot}, nil
}

// HydrateVisitSession 从受校验 snapshot 恢复等价 aggregate，不执行 cleanup 或修复。
func HydrateVisitSession(snapshot Snapshot) (VisitSession, error) {
	if !snapshot.Valid() {
		return VisitSession{}, errors.New("visit session snapshot is invalid")
	}
	return VisitSession{snapshot: snapshot}, nil
}

// Snapshot 返回不共享 slice backing array 的完整规范投影。
func (visit VisitSession) Snapshot() Snapshot {
	snapshot, _ := NewSnapshot(visit.snapshot.id, visit.snapshot.ownerID, visit.snapshot.worldID, visit.snapshot.assignment, visit.snapshot.lifecycle, visit.snapshot.revision, visit.snapshot.capacity, visit.snapshot.createdAt, visit.snapshot.expiresAt, visit.snapshot.ownerBinding, visit.snapshot.ownerGraceGeneration, visit.snapshot.ownerGraceExpiresAt, visit.snapshot.invites, visit.snapshot.memberships)
	return snapshot
}

// ID 返回 aggregate identity。
func (visit VisitSession) ID() VisitSessionID { return visit.snapshot.id }

// OwnerID 返回 immutable Owner identity。
func (visit VisitSession) OwnerID() account.PlayerID { return visit.snapshot.ownerID }

// WorldID 返回 immutable PersonalWorld identity。
func (visit VisitSession) WorldID() personalworld.PersonalWorldID { return visit.snapshot.worldID }

// Assignment 返回 immutable current assignment stamp。
func (visit VisitSession) Assignment() placement.AssignmentStamp { return visit.snapshot.assignment }

// Lifecycle 返回当前控制面状态。
func (visit VisitSession) Lifecycle() Lifecycle { return visit.snapshot.lifecycle }

// Revision 返回当前待提交或已提交版本。
func (visit VisitSession) Revision() Revision { return visit.snapshot.revision }

// RoleOf 返回 actor 在当前 aggregate 中已经成立的控制面角色。
//
// Owner 必须匹配当前 auth lineage；Visitor 必须已经 join 或正在 reconnect，reserved
// membership 不授予角色。Terminal session、缺失/过期 lineage 或未绑定 actor 都默认返回
// Unspecified。
func (visit VisitSession) RoleOf(actor Actor) Role {
	if !visit.Valid() || visit.snapshot.lifecycle == LifecycleClosed || !actor.Valid() {
		return RoleUnspecified
	}
	if visit.ownerMatches(actor) {
		return RoleOwner
	}
	index := visit.memberIndex(actor.playerID)
	if index < 0 {
		return RoleUnspecified
	}
	membership := visit.snapshot.memberships[index]
	if membership.sessionID != actor.sessionID || membership.epoch != actor.epoch {
		return RoleUnspecified
	}
	return membership.Role()
}

// Valid 报告 aggregate 是否来自合法创建或 hydration 路径。
func (visit VisitSession) Valid() bool { return visit.snapshot.Valid() }

// String 防止默认格式化展开完整 aggregate 运行态。
func (VisitSession) String() string { return visitSessionPlaceholder }

// GoString 防止 `%#v` 展开 aggregate 私有字段。
func (VisitSession) GoString() string { return visitSessionPlaceholder }

// LogValue 让结构化日志只记录稳定占位文本。
func (VisitSession) LogValue() slog.Value { return slog.StringValue(visitSessionPlaceholder) }

// CreateInvite 由当前 Owner 创建不预占 capacity 的定向 pending invite。
func (visit VisitSession) CreateInvite(owner Actor, inviteID InviteID, targetID account.PlayerID, expiresAt, observedAt time.Time) (VisitSession, InviteSnapshot, error) {
	const operation = OperationCreateInvite
	if err := visit.requireOpen(owner, observedAt, operation); err != nil {
		return VisitSession{}, InviteSnapshot{}, err
	}
	expiresAt, observedAt = canonicalOptionalTime(expiresAt), canonicalOptionalTime(observedAt)
	if !inviteID.Valid() || !targetID.Valid() || targetID == visit.OwnerID() || expiresAt.IsZero() || !expiresAt.After(observedAt) || expiresAt.After(visit.snapshot.expiresAt) || expiresAt.Sub(observedAt) < minimumInviteLifetime || expiresAt.Sub(observedAt) > maximumInviteLifetime {
		return VisitSession{}, InviteSnapshot{}, domainError(operation, ErrorCodeInvalidArgument)
	}
	if visit.pendingInviteCount() >= maximumPendingInvites {
		return VisitSession{}, InviteSnapshot{}, domainError(operation, ErrorCodeCapacity)
	}
	for _, invite := range visit.snapshot.invites {
		if invite.id == inviteID || invite.targetID == targetID && invite.state == InviteStatePending {
			return VisitSession{}, InviteSnapshot{}, domainError(operation, ErrorCodeInvalidState)
		}
	}
	next, err := visit.nextRevision(operation)
	if err != nil {
		return VisitSession{}, InviteSnapshot{}, err
	}
	invite, err := NewInviteSnapshot(inviteID, targetID, InviteStatePending, next, expiresAt)
	if err != nil {
		return VisitSession{}, InviteSnapshot{}, domainError(operation, ErrorCodeInvalidArgument)
	}
	target, err := visit.rebuild(next, visit.snapshot.lifecycle, visit.snapshot.ownerBinding, visit.snapshot.ownerGraceGeneration, visit.snapshot.ownerGraceExpiresAt, append(visit.snapshot.Invites(), invite), visit.snapshot.Memberships())
	return target, invite, err
}

// RevokeInvite 由当前 Owner 移除尚未 accept 的精确邀请。
func (visit VisitSession) RevokeInvite(owner Actor, inviteID InviteID, observedAt time.Time) (VisitSession, error) {
	const operation = OperationRevokeInvite
	if err := visit.requireOpen(owner, observedAt, operation); err != nil {
		return VisitSession{}, err
	}
	index := visit.inviteIndex(inviteID)
	if index < 0 {
		return VisitSession{}, domainError(operation, ErrorCodeNotFound)
	}
	if visit.snapshot.invites[index].state != InviteStatePending {
		return VisitSession{}, domainError(operation, ErrorCodeInvalidState)
	}
	invites := visit.snapshot.Invites()
	invites = append(invites[:index], invites[index+1:]...)
	next, err := visit.nextRevision(operation)
	if err != nil {
		return VisitSession{}, err
	}
	return visit.rebuild(next, visit.snapshot.lifecycle, visit.snapshot.ownerBinding, 0, time.Time{}, invites, visit.snapshot.Memberships())
}

// ExpireInvite 仅在 identity、真实 deadline 与等于即失效条件全部匹配时清理 pending invite。
func (visit VisitSession) ExpireInvite(inviteID InviteID, expectedDeadline, observedAt time.Time) (VisitSession, error) {
	const operation = OperationExpireInvite
	index := visit.inviteIndex(inviteID)
	if index < 0 {
		return VisitSession{}, domainError(operation, ErrorCodeNotFound)
	}
	invite := visit.snapshot.invites[index]
	if invite.state != InviteStatePending || !invite.expiresAt.Equal(canonicalOptionalTime(expectedDeadline)) {
		return VisitSession{}, domainError(operation, ErrorCodeStale)
	}
	if observedAt.IsZero() || !expiredAt(observedAt, invite.expiresAt) {
		return VisitSession{}, domainError(operation, ErrorCodeInvalidState)
	}
	invites := visit.snapshot.Invites()
	invites = append(invites[:index], invites[index+1:]...)
	next, err := visit.nextRevision(operation)
	if err != nil {
		return VisitSession{}, err
	}
	return visit.rebuild(next, visit.snapshot.lifecycle, visit.snapshot.ownerBinding, visit.snapshot.ownerGraceGeneration, visit.snapshot.ownerGraceExpiresAt, invites, visit.snapshot.Memberships())
}

// AcceptInvite 由目标 Visitor 原子消费 pending invite、占用 capacity 并返回非凭据 intent。
func (visit VisitSession) AcceptInvite(visitor Actor, inviteID InviteID, reservationExpiresAt time.Time, current placement.AssignmentSnapshot, observedAt time.Time) (VisitSession, AdmissionIntent, error) {
	const operation = OperationAcceptInvite
	if err := visit.requireAvailable(observedAt, operation); err != nil {
		return VisitSession{}, AdmissionIntent{}, err
	}
	if !visitor.Valid() || visitor.playerID == visit.OwnerID() || !visit.currentAssignment(current, observedAt) {
		return VisitSession{}, AdmissionIntent{}, domainError(operation, ErrorCodeStale)
	}
	index := visit.inviteIndex(inviteID)
	if index < 0 {
		return VisitSession{}, AdmissionIntent{}, domainError(operation, ErrorCodeNotFound)
	}
	invite := visit.snapshot.invites[index]
	if invite.state != InviteStatePending || invite.targetID != visitor.playerID {
		return VisitSession{}, AdmissionIntent{}, domainError(operation, ErrorCodeForbidden)
	}
	if expiredAt(observedAt, invite.expiresAt) {
		return VisitSession{}, AdmissionIntent{}, domainError(operation, ErrorCodeExpired)
	}
	if visit.memberIndex(visitor.playerID) >= 0 {
		return VisitSession{}, AdmissionIntent{}, domainError(operation, ErrorCodeInvalidState)
	}
	if len(visit.snapshot.memberships) >= int(visit.snapshot.capacity) {
		return VisitSession{}, AdmissionIntent{}, domainError(operation, ErrorCodeCapacity)
	}
	deadline := canonicalOptionalTime(reservationExpiresAt)
	observedAt = canonicalOptionalTime(observedAt)
	if deadline.IsZero() || !deadline.After(observedAt) || deadline.Sub(observedAt) < minimumReservationLifetime || deadline.Sub(observedAt) > maximumReservationLifetime || deadline.After(invite.expiresAt) || deadline.After(visit.snapshot.expiresAt) || deadline.After(current.Lease().ExpiresAt()) {
		return VisitSession{}, AdmissionIntent{}, domainError(operation, ErrorCodeInvalidArgument)
	}
	next, err := visit.nextRevision(operation)
	if err != nil {
		return VisitSession{}, AdmissionIntent{}, err
	}
	invites := visit.snapshot.Invites()
	invites[index].state = InviteStateAccepted
	membership, err := NewMembershipSnapshot(visitor.playerID, inviteID, MembershipStateReserved, visitor.sessionID, visitor.epoch, ConnectionBindingID{}, deadline, 0, time.Time{})
	if err != nil {
		return VisitSession{}, AdmissionIntent{}, domainError(operation, ErrorCodeInvalidArgument)
	}
	target, err := visit.rebuild(next, visit.snapshot.lifecycle, visit.snapshot.ownerBinding, 0, time.Time{}, invites, append(visit.snapshot.Memberships(), membership))
	if err != nil {
		return VisitSession{}, AdmissionIntent{}, err
	}
	intent, err := newAdmissionIntent(visit.ID(), visitor, visit.Assignment(), deadline)
	if err != nil {
		return VisitSession{}, AdmissionIntent{}, domainError(operation, ErrorCodeInvalidArgument)
	}
	return target, intent, nil
}

// Join 把 matching reservation 转为 joined，并保存受信 connection binding。
func (visit VisitSession) Join(visitor Actor, qualification JoinQualification, bindingID ConnectionBindingID, current placement.AssignmentSnapshot, observedAt time.Time) (VisitSession, MembershipSnapshot, error) {
	const operation = OperationJoin
	if err := visit.requireAvailable(observedAt, operation); err != nil {
		return VisitSession{}, MembershipSnapshot{}, err
	}
	if !visitor.Valid() || !qualification.validFor(qualificationPurposeJoin) || !bindingID.Valid() || !visit.currentAssignment(current, observedAt) {
		return VisitSession{}, MembershipSnapshot{}, domainError(operation, ErrorCodeStale)
	}
	intent := qualification.intent
	if intent.visitSessionID != visit.ID() || intent.visitorID != visitor.playerID || intent.sessionID != visitor.sessionID || intent.epoch != visitor.epoch || !intent.assignment.Equal(visit.Assignment()) || expiredAt(observedAt, intent.expiresAt) {
		return VisitSession{}, MembershipSnapshot{}, domainError(operation, ErrorCodeStale)
	}
	index := visit.memberIndex(visitor.playerID)
	if index < 0 {
		return VisitSession{}, MembershipSnapshot{}, domainError(operation, ErrorCodeNotFound)
	}
	membership := visit.snapshot.memberships[index]
	if membership.state != MembershipStateReserved || membership.sessionID != visitor.sessionID || membership.epoch != visitor.epoch || !membership.reservationExpiresAt.Equal(intent.expiresAt) {
		return VisitSession{}, MembershipSnapshot{}, domainError(operation, ErrorCodeInvalidState)
	}
	joined, _ := NewMembershipSnapshot(visitor.playerID, membership.inviteID, MembershipStateJoined, visitor.sessionID, visitor.epoch, bindingID, time.Time{}, 0, time.Time{})
	memberships := visit.snapshot.Memberships()
	memberships[index] = joined
	next, err := visit.nextRevision(operation)
	if err != nil {
		return VisitSession{}, MembershipSnapshot{}, err
	}
	target, err := visit.rebuild(next, visit.snapshot.lifecycle, visit.snapshot.ownerBinding, 0, time.Time{}, visit.snapshot.Invites(), memberships)
	return target, joined, err
}

// ExpireReservation 清理 matching 到期 reservation，不生成 safe-return directive。
func (visit VisitSession) ExpireReservation(visitorID account.PlayerID, inviteID InviteID, expectedDeadline, observedAt time.Time) (VisitSession, error) {
	const operation = OperationExpireReservation
	index := visit.memberIndex(visitorID)
	if index < 0 {
		return VisitSession{}, domainError(operation, ErrorCodeNotFound)
	}
	membership := visit.snapshot.memberships[index]
	if membership.state != MembershipStateReserved || membership.inviteID != inviteID || !membership.reservationExpiresAt.Equal(canonicalOptionalTime(expectedDeadline)) {
		return VisitSession{}, domainError(operation, ErrorCodeStale)
	}
	if observedAt.IsZero() || !expiredAt(observedAt, membership.reservationExpiresAt) {
		return VisitSession{}, domainError(operation, ErrorCodeInvalidState)
	}
	invites := visit.removeInvite(inviteID)
	memberships := visit.removeMember(index)
	next, err := visit.nextRevision(operation)
	if err != nil {
		return VisitSession{}, err
	}
	return visit.rebuild(next, visit.snapshot.lifecycle, visit.snapshot.ownerBinding, visit.snapshot.ownerGraceGeneration, visit.snapshot.ownerGraceExpiresAt, invites, memberships)
}

// Leave 只允许 joined/reconnecting Visitor 以自身 matching lineage 与 binding 离开。
func (visit VisitSession) Leave(visitor Actor, bindingID ConnectionBindingID) (VisitSession, SafeReturnDirective, error) {
	const operation = OperationLeave
	index := visit.memberIndex(visitor.playerID)
	if index < 0 {
		return VisitSession{}, SafeReturnDirective{}, domainError(operation, ErrorCodeNotFound)
	}
	membership := visit.snapshot.memberships[index]
	if !visitor.Valid() || (membership.state != MembershipStateJoined && membership.state != MembershipStateReconnecting) || membership.sessionID != visitor.sessionID || membership.epoch != visitor.epoch || membership.bindingID != bindingID {
		return VisitSession{}, SafeReturnDirective{}, domainError(operation, ErrorCodeStale)
	}
	directive, _ := NewSafeReturnDirective(visit.ID(), visitor.playerID, SafeReturnReasonVoluntaryLeave)
	next, err := visit.nextRevision(operation)
	if err != nil {
		return VisitSession{}, SafeReturnDirective{}, err
	}
	target, err := visit.rebuild(next, visit.snapshot.lifecycle, visit.snapshot.ownerBinding, visit.snapshot.ownerGraceGeneration, visit.snapshot.ownerGraceExpiresAt, visit.removeInvite(membership.inviteID), visit.removeMember(index))
	return target, directive, err
}

// Kick 只允许 immutable Owner 移除一个 joined/reconnecting Visitor。
func (visit VisitSession) Kick(owner Actor, visitorID account.PlayerID) (VisitSession, SafeReturnDirective, error) {
	const operation = OperationKick
	if !visit.ownerMatches(owner) {
		return VisitSession{}, SafeReturnDirective{}, domainError(operation, ErrorCodeForbidden)
	}
	index := visit.memberIndex(visitorID)
	if index < 0 {
		return VisitSession{}, SafeReturnDirective{}, domainError(operation, ErrorCodeNotFound)
	}
	membership := visit.snapshot.memberships[index]
	if membership.state != MembershipStateJoined && membership.state != MembershipStateReconnecting {
		return VisitSession{}, SafeReturnDirective{}, domainError(operation, ErrorCodeInvalidState)
	}
	directive, _ := NewSafeReturnDirective(visit.ID(), visitorID, SafeReturnReasonKicked)
	next, err := visit.nextRevision(operation)
	if err != nil {
		return VisitSession{}, SafeReturnDirective{}, err
	}
	target, err := visit.rebuild(next, visit.snapshot.lifecycle, visit.snapshot.ownerBinding, visit.snapshot.ownerGraceGeneration, visit.snapshot.ownerGraceExpiresAt, visit.removeInvite(membership.inviteID), visit.removeMember(index))
	return target, directive, err
}

// VisitorDisconnect 仅把 matching joined binding 转为有界 reconnecting 状态。
func (visit VisitSession) VisitorDisconnect(visitor Actor, bindingID ConnectionBindingID, reconnectExpiresAt, observedAt time.Time) (VisitSession, error) {
	const operation = OperationVisitorDisconnect
	index := visit.memberIndex(visitor.playerID)
	if index < 0 {
		return VisitSession{}, domainError(operation, ErrorCodeNotFound)
	}
	membership := visit.snapshot.memberships[index]
	deadline, observedAt := canonicalOptionalTime(reconnectExpiresAt), canonicalOptionalTime(observedAt)
	if !visitor.Valid() || membership.state != MembershipStateJoined || membership.sessionID != visitor.sessionID || membership.epoch != visitor.epoch || membership.bindingID != bindingID {
		return VisitSession{}, domainError(operation, ErrorCodeStale)
	}
	if deadline.IsZero() || !deadline.After(observedAt) || deadline.Sub(observedAt) < minimumVisitorReconnectGrace || deadline.Sub(observedAt) > maximumVisitorReconnectGrace || deadline.After(visit.snapshot.expiresAt) {
		return VisitSession{}, domainError(operation, ErrorCodeInvalidArgument)
	}
	reconnecting, _ := NewMembershipSnapshot(visitor.playerID, membership.inviteID, MembershipStateReconnecting, visitor.sessionID, visitor.epoch, bindingID, time.Time{}, membership.reconnectGeneration+1, deadline)
	memberships := visit.snapshot.Memberships()
	memberships[index] = reconnecting
	next, err := visit.nextRevision(operation)
	if err != nil {
		return VisitSession{}, err
	}
	return visit.rebuild(next, visit.snapshot.lifecycle, visit.snapshot.ownerBinding, visit.snapshot.ownerGraceGeneration, visit.snapshot.ownerGraceExpiresAt, visit.snapshot.Invites(), memberships)
}

// VisitorReconnect 由同一 Visitor 在 deadline 前以新 binding 恢复 joined 状态。
func (visit VisitSession) VisitorReconnect(visitor Actor, qualification JoinQualification, newBindingID ConnectionBindingID, current placement.AssignmentSnapshot, observedAt time.Time) (VisitSession, MembershipSnapshot, error) {
	const operation = OperationVisitorReconnect
	if err := visit.requireAvailable(observedAt, operation); err != nil {
		return VisitSession{}, MembershipSnapshot{}, err
	}
	index := visit.memberIndex(visitor.playerID)
	if index < 0 {
		return VisitSession{}, MembershipSnapshot{}, domainError(operation, ErrorCodeNotFound)
	}
	membership := visit.snapshot.memberships[index]
	if !visitor.Valid() || !qualification.validFor(qualificationPurposeReconnect) || !newBindingID.Valid() || membership.state != MembershipStateReconnecting || membership.sessionID != visitor.sessionID || membership.epoch != visitor.epoch || expiredAt(observedAt, membership.reconnectExpiresAt) || !visit.currentAssignment(current, observedAt) {
		return VisitSession{}, MembershipSnapshot{}, domainError(operation, ErrorCodeStale)
	}
	intent := qualification.intent
	if intent.visitSessionID != visit.ID() || intent.visitorID != visitor.playerID || intent.sessionID != visitor.sessionID || intent.epoch != visitor.epoch || !intent.assignment.Equal(visit.Assignment()) || !intent.expiresAt.Equal(membership.reconnectExpiresAt) || expiredAt(observedAt, intent.expiresAt) {
		return VisitSession{}, MembershipSnapshot{}, domainError(operation, ErrorCodeStale)
	}
	joined, _ := NewMembershipSnapshot(visitor.playerID, membership.inviteID, MembershipStateJoined, visitor.sessionID, visitor.epoch, newBindingID, time.Time{}, 0, time.Time{})
	memberships := visit.snapshot.Memberships()
	memberships[index] = joined
	next, err := visit.nextRevision(operation)
	if err != nil {
		return VisitSession{}, MembershipSnapshot{}, err
	}
	target, err := visit.rebuild(next, visit.snapshot.lifecycle, visit.snapshot.ownerBinding, 0, time.Time{}, visit.snapshot.Invites(), memberships)
	return target, joined, err
}

// ExpireVisitorReconnect 只移除 matching generation、binding 与 deadline 的 Visitor。
func (visit VisitSession) ExpireVisitorReconnect(visitorID account.PlayerID, generation uint64, bindingID ConnectionBindingID, expectedDeadline, observedAt time.Time) (VisitSession, SafeReturnDirective, error) {
	const operation = OperationExpireVisitorReconnect
	index := visit.memberIndex(visitorID)
	if index < 0 {
		return VisitSession{}, SafeReturnDirective{}, domainError(operation, ErrorCodeNotFound)
	}
	membership := visit.snapshot.memberships[index]
	if membership.state != MembershipStateReconnecting || membership.reconnectGeneration != generation || membership.bindingID != bindingID || !membership.reconnectExpiresAt.Equal(canonicalOptionalTime(expectedDeadline)) {
		return VisitSession{}, SafeReturnDirective{}, domainError(operation, ErrorCodeStale)
	}
	if observedAt.IsZero() || !expiredAt(observedAt, membership.reconnectExpiresAt) {
		return VisitSession{}, SafeReturnDirective{}, domainError(operation, ErrorCodeInvalidState)
	}
	directive, _ := NewSafeReturnDirective(visit.ID(), visitorID, SafeReturnReasonVisitorReconnectExpired)
	next, err := visit.nextRevision(operation)
	if err != nil {
		return VisitSession{}, SafeReturnDirective{}, err
	}
	target, err := visit.rebuild(next, visit.snapshot.lifecycle, visit.snapshot.ownerBinding, visit.snapshot.ownerGraceGeneration, visit.snapshot.ownerGraceExpiresAt, visit.removeInvite(membership.inviteID), visit.removeMember(index))
	return target, directive, err
}

// OwnerDisconnect 仅在当前完整 Owner binding 匹配时进入 owner_grace。
func (visit VisitSession) OwnerDisconnect(owner Actor, bindingID ConnectionBindingID, graceExpiresAt, observedAt time.Time) (VisitSession, error) {
	const operation = OperationOwnerDisconnect
	if visit.snapshot.lifecycle != LifecycleOpen || !visit.ownerMatches(owner) || visit.snapshot.ownerBinding.connectionID != bindingID {
		return VisitSession{}, domainError(operation, ErrorCodeStale)
	}
	deadline, observedAt := canonicalOptionalTime(graceExpiresAt), canonicalOptionalTime(observedAt)
	if deadline.IsZero() || !deadline.After(observedAt) || deadline.Sub(observedAt) < minimumOwnerGrace || deadline.Sub(observedAt) > maximumOwnerGrace || deadline.After(visit.snapshot.expiresAt) {
		return VisitSession{}, domainError(operation, ErrorCodeInvalidArgument)
	}
	next, err := visit.nextRevision(operation)
	if err != nil {
		return VisitSession{}, err
	}
	return visit.rebuild(next, LifecycleOwnerGrace, visit.snapshot.ownerBinding, visit.snapshot.ownerGraceGeneration+1, deadline, visit.snapshot.Invites(), visit.snapshot.Memberships())
}

// OwnerReconnect 只允许 immutable Owner 在 matching grace deadline 前更新当前 auth binding。
func (visit VisitSession) OwnerReconnect(owner Actor, newBindingID ConnectionBindingID, current placement.AssignmentSnapshot, observedAt time.Time) (VisitSession, error) {
	const operation = OperationOwnerReconnect
	if visit.snapshot.lifecycle != LifecycleOwnerGrace || !owner.Valid() || owner.playerID != visit.OwnerID() || !newBindingID.Valid() || expiredAt(observedAt, visit.snapshot.ownerGraceExpiresAt) || !visit.currentAssignment(current, observedAt) {
		return VisitSession{}, domainError(operation, ErrorCodeStale)
	}
	binding, _ := NewAuthBinding(owner, newBindingID)
	next, err := visit.nextRevision(operation)
	if err != nil {
		return VisitSession{}, err
	}
	return visit.rebuild(next, LifecycleOpen, binding, 0, time.Time{}, visit.snapshot.Invites(), visit.snapshot.Memberships())
}

// Close 只允许当前 immutable Owner 显式终止 session，并返回完整确定性 directives。
func (visit VisitSession) Close(owner Actor) (VisitSession, []SafeReturnDirective, error) {
	if !visit.ownerMatches(owner) {
		return VisitSession{}, nil, domainError(OperationClose, ErrorCodeForbidden)
	}
	return visit.closeWith(OperationClose, SafeReturnReasonOwnerClosed)
}

// ExpireOwnerGrace 只在 generation、旧 binding、真实 deadline 全部匹配且已到期时关闭。
func (visit VisitSession) ExpireOwnerGrace(generation uint64, bindingID ConnectionBindingID, expectedDeadline, observedAt time.Time) (VisitSession, []SafeReturnDirective, error) {
	if visit.snapshot.lifecycle != LifecycleOwnerGrace || visit.snapshot.ownerGraceGeneration != generation || visit.snapshot.ownerBinding.connectionID != bindingID || !visit.snapshot.ownerGraceExpiresAt.Equal(canonicalOptionalTime(expectedDeadline)) {
		return VisitSession{}, nil, domainError(OperationExpireOwnerGrace, ErrorCodeStale)
	}
	if observedAt.IsZero() || !expiredAt(observedAt, visit.snapshot.ownerGraceExpiresAt) {
		return VisitSession{}, nil, domainError(OperationExpireOwnerGrace, ErrorCodeInvalidState)
	}
	return visit.closeWith(OperationExpireOwnerGrace, SafeReturnReasonOwnerUnavailable)
}

// ExpireSession 在 matching absolute deadline 到达时 terminal close aggregate。
func (visit VisitSession) ExpireSession(expectedDeadline, observedAt time.Time) (VisitSession, []SafeReturnDirective, error) {
	if !visit.snapshot.expiresAt.Equal(canonicalOptionalTime(expectedDeadline)) {
		return VisitSession{}, nil, domainError(OperationExpireSession, ErrorCodeStale)
	}
	if observedAt.IsZero() || !expiredAt(observedAt, visit.snapshot.expiresAt) {
		return VisitSession{}, nil, domainError(OperationExpireSession, ErrorCodeInvalidState)
	}
	return visit.closeWith(OperationExpireSession, SafeReturnReasonSessionExpired)
}

// InvalidateAssignment 因 current assignment missing、expired 或 stamp 变化而关闭旧访问。
func (visit VisitSession) InvalidateAssignment() (VisitSession, []SafeReturnDirective, error) {
	return visit.closeWith(OperationInvalidateAssignment, SafeReturnReasonAssignmentChanged)
}

// LoseDependency 因未来 Redis 等运行态无法安全恢复而关闭旧访问。
func (visit VisitSession) LoseDependency() (VisitSession, []SafeReturnDirective, error) {
	return visit.closeWith(OperationDependencyLost, SafeReturnReasonDependencyLost)
}

// closeWith 一次 revision 关闭 aggregate，清空全部 active state并稳定生成 joined/reconnecting directives。
func (visit VisitSession) closeWith(operation Operation, reason SafeReturnReason) (VisitSession, []SafeReturnDirective, error) {
	if visit.snapshot.lifecycle == LifecycleClosed {
		return VisitSession{}, nil, domainError(operation, ErrorCodeInvalidState)
	}
	directives := make([]SafeReturnDirective, 0, len(visit.snapshot.memberships))
	for _, membership := range visit.snapshot.memberships {
		if membership.state == MembershipStateJoined || membership.state == MembershipStateReconnecting {
			directive, _ := NewSafeReturnDirective(visit.ID(), membership.visitorID, reason)
			directives = append(directives, directive)
		}
	}
	sort.Slice(directives, func(left, right int) bool {
		return directives[left].visitorID.String() < directives[right].visitorID.String()
	})
	next, err := visit.nextRevision(operation)
	if err != nil {
		return VisitSession{}, nil, err
	}
	target, err := visit.rebuild(next, LifecycleClosed, visit.snapshot.ownerBinding, 0, time.Time{}, nil, nil)
	return target, directives, err
}

// requireOpen 验证新 Owner-only operation 的 lifecycle、deadline 与当前 owner lineage。
func (visit VisitSession) requireOpen(owner Actor, observedAt time.Time, operation Operation) error {
	if !visit.ownerMatches(owner) {
		return domainError(operation, ErrorCodeForbidden)
	}
	return visit.requireAvailable(observedAt, operation)
}

// requireAvailable 拒绝 owner_grace、closed 与等于 session deadline 的新资格操作。
func (visit VisitSession) requireAvailable(observedAt time.Time, operation Operation) error {
	if !visit.Valid() || observedAt.IsZero() {
		return domainError(operation, ErrorCodeInvalidArgument)
	}
	if visit.snapshot.lifecycle != LifecycleOpen {
		return domainError(operation, ErrorCodeInvalidState)
	}
	if expiredAt(observedAt, visit.snapshot.expiresAt) {
		return domainError(operation, ErrorCodeExpired)
	}
	return nil
}

// currentAssignment 只接受 active、lease 当前有效且完整 stamp 与 immutable binding 相等的 snapshot。
func (visit VisitSession) currentAssignment(current placement.AssignmentSnapshot, observedAt time.Time) bool {
	return current.Phase() == placement.PhaseActive && current.ValidAt(canonicalOptionalTime(observedAt)) && current.Stamp().Equal(visit.snapshot.assignment)
}

// ownerMatches 比较 immutable PlayerID 与当前 Owner SessionID/epoch，Visitor 永远不能继承。
func (visit VisitSession) ownerMatches(actor Actor) bool {
	return actor.Valid() && actor.playerID == visit.OwnerID() && actor.sessionID == visit.snapshot.ownerBinding.actor.sessionID && actor.epoch == visit.snapshot.ownerBinding.actor.epoch
}

// nextRevision 统一拒绝无效 aggregate 与 revision overflow。
func (visit VisitSession) nextRevision(operation Operation) (Revision, error) {
	if !visit.Valid() {
		return 0, domainError(operation, ErrorCodeInvalidState)
	}
	next, err := visit.snapshot.revision.next()
	if err != nil {
		return 0, domainError(operation, ErrorCodeInvalidState)
	}
	return next, nil
}

// rebuild 通过唯一 snapshot constructor 重新验证 transition 结果并保持输入 slice 不共享。
func (visit VisitSession) rebuild(revision Revision, lifecycle Lifecycle, ownerBinding AuthBinding, graceGeneration uint64, graceExpiresAt time.Time, invites []InviteSnapshot, memberships []MembershipSnapshot) (VisitSession, error) {
	snapshot, err := NewSnapshot(visit.ID(), visit.OwnerID(), visit.WorldID(), visit.Assignment(), lifecycle, revision, visit.snapshot.capacity, visit.snapshot.createdAt, visit.snapshot.expiresAt, ownerBinding, graceGeneration, graceExpiresAt, invites, memberships)
	if err != nil {
		return VisitSession{}, &Error{operation: OperationResolve, code: ErrorCodeInvalidState, cause: err}
	}
	return VisitSession{snapshot: snapshot}, nil
}

// pendingInviteCount 返回尚可 accept 的有界邀请数量。
func (visit VisitSession) pendingInviteCount() int {
	count := 0
	for _, invite := range visit.snapshot.invites {
		if invite.state == InviteStatePending {
			count++
		}
	}
	return count
}

// inviteIndex 查找 exact InviteID，未找到返回 -1。
func (visit VisitSession) inviteIndex(id InviteID) int {
	for index, invite := range visit.snapshot.invites {
		if invite.id == id {
			return index
		}
	}
	return -1
}

// memberIndex 查找 aggregate-local 唯一 Visitor membership，未找到返回 -1。
func (visit VisitSession) memberIndex(visitorID account.PlayerID) int {
	for index, member := range visit.snapshot.memberships {
		if member.visitorID == visitorID {
			return index
		}
	}
	return -1
}

// removeInvite 返回不含指定邀请的新副本。
func (visit VisitSession) removeInvite(id InviteID) []InviteSnapshot {
	invites := visit.snapshot.Invites()
	index := visit.inviteIndex(id)
	if index >= 0 {
		invites = append(invites[:index], invites[index+1:]...)
	}
	return invites
}

// removeMember 返回不含指定 index 的新副本。
func (visit VisitSession) removeMember(index int) []MembershipSnapshot {
	members := visit.snapshot.Memberships()
	return append(members[:index], members[index+1:]...)
}
