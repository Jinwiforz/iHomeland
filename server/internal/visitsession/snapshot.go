package visitsession

import (
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
)

// InviteSnapshot 是 store 与 domain 之间严格构造的定向邀请投影。
type InviteSnapshot struct {
	// id 是独立 invite namespace identity。
	id InviteID
	// targetID 是唯一允许 accept 的认证 PlayerID。
	targetID account.PlayerID
	// state 区分仍可 accept 与已消费的邀请。
	state InviteState
	// createdRevision 是创建邀请后 aggregate 的已提交 revision。
	createdRevision Revision
	// expiresAt 是等于或晚于该 UTC 微秒时刻即失效的绝对 deadline。
	expiresAt time.Time
}

// NewInviteSnapshot 校验 store hydration 返回的完整邀请事实。
func NewInviteSnapshot(id InviteID, targetID account.PlayerID, state InviteState, createdRevision Revision, expiresAt time.Time) (InviteSnapshot, error) {
	if !id.Valid() || !targetID.Valid() || !state.Valid() || !createdRevision.Valid() || expiresAt.IsZero() {
		return InviteSnapshot{}, errors.New("visit invite snapshot is incomplete")
	}
	return InviteSnapshot{id: id, targetID: targetID, state: state, createdRevision: createdRevision, expiresAt: canonicalTime(expiresAt)}, nil
}

// ID 返回邀请 identity 值副本。
func (invite InviteSnapshot) ID() InviteID { return invite.id }

// TargetID 返回唯一允许 accept 的 PlayerID。
func (invite InviteSnapshot) TargetID() account.PlayerID { return invite.targetID }

// State 返回邀请封闭状态。
func (invite InviteSnapshot) State() InviteState { return invite.state }

// CreatedRevision 返回创建邀请后的 aggregate revision。
func (invite InviteSnapshot) CreatedRevision() Revision { return invite.createdRevision }

// ExpiresAt 返回 UTC 微秒绝对 deadline。
func (invite InviteSnapshot) ExpiresAt() time.Time { return invite.expiresAt }

// Valid 报告邀请是否可以进入 aggregate hydration。
func (invite InviteSnapshot) Valid() bool {
	return invite.id.Valid() && invite.targetID.Valid() && invite.state.Valid() && invite.createdRevision.Valid() && !invite.expiresAt.IsZero()
}

// String 防止默认格式化展开邀请与目标 PlayerID。
func (InviteSnapshot) String() string { return "[REDACTED_VISIT_INVITE]" }

// GoString 防止 `%#v` 展开邀请私有字段。
func (InviteSnapshot) GoString() string { return "[REDACTED_VISIT_INVITE]" }

// LogValue 让结构化日志只记录稳定占位文本。
func (InviteSnapshot) LogValue() slog.Value { return slog.StringValue("[REDACTED_VISIT_INVITE]") }

// MembershipSnapshot 是一个 Visitor 在 VisitSession 内唯一 active membership 的投影。
type MembershipSnapshot struct {
	// visitorID 是 membership 的 aggregate-local 唯一 key。
	visitorID account.PlayerID
	// inviteID 保留 accept 所消费邀请的关联，不授予 join 权限。
	inviteID InviteID
	// state 表达 reservation、已连接或断线恢复窗口。
	state MembershipState
	// sessionID 与 epoch 共同固定 accept/join/reconnect 的认证 lineage。
	sessionID session.SessionID
	// epoch 使旧 token、ticket 与 callback 无法恢复新 lineage。
	epoch session.Epoch
	// bindingID 在 joined/reconnecting 状态保存当前或刚断开的精确连接 identity。
	bindingID ConnectionBindingID
	// reservationExpiresAt 只在 reserved 状态保存等于即失效的 UTC 微秒 deadline。
	reservationExpiresAt time.Time
	// reconnectGeneration 只在 reconnecting 状态区分延迟 expiry callback。
	reconnectGeneration uint64
	// reconnectExpiresAt 只在 reconnecting 状态保存等于即失效的 UTC 微秒 deadline。
	reconnectExpiresAt time.Time
}

// NewMembershipSnapshot 校验 store hydration 返回的完整 Visitor membership。
func NewMembershipSnapshot(visitorID account.PlayerID, inviteID InviteID, state MembershipState, sessionID session.SessionID, epoch session.Epoch, bindingID ConnectionBindingID, reservationExpiresAt time.Time, reconnectGeneration uint64, reconnectExpiresAt time.Time) (MembershipSnapshot, error) {
	membership := MembershipSnapshot{visitorID: visitorID, inviteID: inviteID, state: state, sessionID: sessionID, epoch: epoch, bindingID: bindingID, reservationExpiresAt: canonicalOptionalTime(reservationExpiresAt), reconnectGeneration: reconnectGeneration, reconnectExpiresAt: canonicalOptionalTime(reconnectExpiresAt)}
	if !membership.Valid() {
		return MembershipSnapshot{}, errors.New("visit membership snapshot is inconsistent")
	}
	return membership, nil
}

// VisitorID 返回 membership 的认证 PlayerID。
func (membership MembershipSnapshot) VisitorID() account.PlayerID { return membership.visitorID }

// InviteID 返回创建 reservation 的邀请关联值；该值不是 credential。
func (membership MembershipSnapshot) InviteID() InviteID { return membership.inviteID }

// State 返回 membership 封闭状态。
func (membership MembershipSnapshot) State() MembershipState { return membership.state }

// SessionID 返回 accept 时绑定且 reconnect 必须匹配的 session lineage。
func (membership MembershipSnapshot) SessionID() session.SessionID { return membership.sessionID }

// Epoch 返回旧认证 lineage 的失效屏障。
func (membership MembershipSnapshot) Epoch() session.Epoch { return membership.epoch }

// BindingID 返回 joined/reconnecting 的精确连接 identity；reserved 状态返回零值。
func (membership MembershipSnapshot) BindingID() ConnectionBindingID { return membership.bindingID }

// ReservationExpiresAt 返回 reserved membership 的 UTC 微秒 deadline。
func (membership MembershipSnapshot) ReservationExpiresAt() time.Time {
	return membership.reservationExpiresAt
}

// ReconnectGeneration 返回 reconnecting callback 必须精确匹配的 generation。
func (membership MembershipSnapshot) ReconnectGeneration() uint64 {
	return membership.reconnectGeneration
}

// ReconnectExpiresAt 返回 reconnecting membership 的 UTC 微秒 deadline。
func (membership MembershipSnapshot) ReconnectExpiresAt() time.Time {
	return membership.reconnectExpiresAt
}

// Role 只为已经 join 或正在 reconnect 的 membership 返回 Visitor 角色。
//
// Reserved 仅表示 capacity 与 admission reservation，尚未取得 connection membership 角色。
func (membership MembershipSnapshot) Role() Role {
	if membership.Valid() && (membership.state == MembershipStateJoined || membership.state == MembershipStateReconnecting) {
		return RoleVisitor
	}
	return RoleUnspecified
}

// Valid 报告 membership 是否满足其当前 state 的字段组合。
func (membership MembershipSnapshot) Valid() bool {
	if !membership.visitorID.Valid() || !membership.inviteID.Valid() || !membership.state.Valid() || !membership.sessionID.Valid() || !membership.epoch.Valid() {
		return false
	}
	switch membership.state {
	case MembershipStateReserved:
		return !membership.reservationExpiresAt.IsZero() && !membership.bindingID.Valid() && membership.reconnectGeneration == 0 && membership.reconnectExpiresAt.IsZero()
	case MembershipStateJoined:
		return membership.reservationExpiresAt.IsZero() && membership.bindingID.Valid() && membership.reconnectGeneration == 0 && membership.reconnectExpiresAt.IsZero()
	case MembershipStateReconnecting:
		return membership.reservationExpiresAt.IsZero() && membership.bindingID.Valid() && membership.reconnectGeneration > 0 && !membership.reconnectExpiresAt.IsZero()
	default:
		return false
	}
}

// String 防止默认格式化展开 Visitor、session lineage 与 connection binding。
func (MembershipSnapshot) String() string { return "[REDACTED_VISIT_MEMBERSHIP]" }

// GoString 防止 `%#v` 展开 membership 私有字段。
func (MembershipSnapshot) GoString() string { return "[REDACTED_VISIT_MEMBERSHIP]" }

// LogValue 让结构化日志只记录稳定占位文本。
func (MembershipSnapshot) LogValue() slog.Value {
	return slog.StringValue("[REDACTED_VISIT_MEMBERSHIP]")
}

// Snapshot 是 VisitSessionStore 与 domain 之间完整、规范且有界的运行态投影。
type Snapshot struct {
	// id 是 aggregate 的不可迁移 identity。
	id VisitSessionID
	// ownerID 是永不因连接或 Visitor 状态改变的 PlayerID。
	ownerID account.PlayerID
	// worldID 是 Owner 的 immutable PersonalWorld identity。
	worldID personalworld.PersonalWorldID
	// assignment 是创建时 current assignment 的完整条件 stamp。
	assignment placement.AssignmentStamp
	// lifecycle 表达 open、Owner grace 或 terminal closed。
	lifecycle Lifecycle
	// revision 是 snapshot 的已提交乐观并发版本。
	revision Revision
	// capacity 是 reserved、joined 与 reconnecting membership 的上限。
	capacity Capacity
	// createdAt 是服务端选择的 UTC 微秒创建时间。
	createdAt time.Time
	// expiresAt 是整个 VisitSession 等于即失效的 UTC 微秒 deadline。
	expiresAt time.Time
	// ownerBinding 保存当前或进入 grace 时最后确认的 Owner 连接条件。
	ownerBinding AuthBinding
	// ownerGraceGeneration 区分延迟 Owner grace timer；仅 owner_grace 状态为正。
	ownerGraceGeneration uint64
	// ownerGraceExpiresAt 是 owner_grace 状态等于即失效的 UTC 微秒 deadline。
	ownerGraceExpiresAt time.Time
	// invites 按 InviteID 稳定升序保存，外部只能取得副本。
	invites []InviteSnapshot
	// memberships 按 VisitorID 稳定升序保存，外部只能取得副本。
	memberships []MembershipSnapshot
}

// NewSnapshot 校验 store hydration 的完整 VisitSession 事实并规范集合顺序。
//
// Hydration 允许恢复已经到期的值以执行显式 cleanup，但不修复字段、不推进 revision，
// 也不把 malformed adapter 结果降级为 not-found。
func NewSnapshot(id VisitSessionID, ownerID account.PlayerID, worldID personalworld.PersonalWorldID, assignment placement.AssignmentStamp, lifecycle Lifecycle, revision Revision, capacity Capacity, createdAt, expiresAt time.Time, ownerBinding AuthBinding, ownerGraceGeneration uint64, ownerGraceExpiresAt time.Time, invites []InviteSnapshot, memberships []MembershipSnapshot) (Snapshot, error) {
	snapshot := Snapshot{id: id, ownerID: ownerID, worldID: worldID, assignment: assignment, lifecycle: lifecycle, revision: revision, capacity: capacity, createdAt: canonicalOptionalTime(createdAt), expiresAt: canonicalOptionalTime(expiresAt), ownerBinding: ownerBinding, ownerGraceGeneration: ownerGraceGeneration, ownerGraceExpiresAt: canonicalOptionalTime(ownerGraceExpiresAt), invites: append([]InviteSnapshot(nil), invites...), memberships: append([]MembershipSnapshot(nil), memberships...)}
	if err := snapshot.validate(); err != nil {
		return Snapshot{}, err
	}
	sort.Slice(snapshot.invites, func(left, right int) bool { return snapshot.invites[left].id.value < snapshot.invites[right].id.value })
	sort.Slice(snapshot.memberships, func(left, right int) bool {
		return snapshot.memberships[left].visitorID.String() < snapshot.memberships[right].visitorID.String()
	})
	return snapshot, nil
}

// ID 返回 aggregate identity 值副本。
func (snapshot Snapshot) ID() VisitSessionID { return snapshot.id }

// OwnerID 返回 immutable Owner PlayerID。
func (snapshot Snapshot) OwnerID() account.PlayerID { return snapshot.ownerID }

// WorldID 返回 immutable PersonalWorld identity。
func (snapshot Snapshot) WorldID() personalworld.PersonalWorldID { return snapshot.worldID }

// Assignment 返回创建时 current assignment 的完整条件 stamp。
func (snapshot Snapshot) Assignment() placement.AssignmentStamp { return snapshot.assignment }

// Lifecycle 返回控制面封闭状态。
func (snapshot Snapshot) Lifecycle() Lifecycle { return snapshot.lifecycle }

// Revision 返回 snapshot 已提交版本。
func (snapshot Snapshot) Revision() Revision { return snapshot.revision }

// Capacity 返回 Visitor membership 固定上限。
func (snapshot Snapshot) Capacity() Capacity { return snapshot.capacity }

// CreatedAt 返回 UTC 微秒创建时间。
func (snapshot Snapshot) CreatedAt() time.Time { return snapshot.createdAt }

// ExpiresAt 返回 aggregate 等于即失效的 UTC 微秒 deadline。
func (snapshot Snapshot) ExpiresAt() time.Time { return snapshot.expiresAt }

// OwnerBinding 返回当前或 grace 前最后确认的 Owner 连接条件值副本。
func (snapshot Snapshot) OwnerBinding() AuthBinding { return snapshot.ownerBinding }

// OwnerGraceGeneration 返回 owner_grace callback 必须匹配的 generation。
func (snapshot Snapshot) OwnerGraceGeneration() uint64 { return snapshot.ownerGraceGeneration }

// OwnerGraceExpiresAt 返回 owner_grace 等于即失效的 UTC 微秒 deadline。
func (snapshot Snapshot) OwnerGraceExpiresAt() time.Time { return snapshot.ownerGraceExpiresAt }

// Invites 返回稳定排序副本，调用方修改结果不会改变 aggregate。
func (snapshot Snapshot) Invites() []InviteSnapshot {
	return append([]InviteSnapshot(nil), snapshot.invites...)
}

// Memberships 返回稳定排序副本，调用方修改结果不会改变 aggregate。
func (snapshot Snapshot) Memberships() []MembershipSnapshot {
	return append([]MembershipSnapshot(nil), snapshot.memberships...)
}

// ActiveMemberCount 返回 capacity 计数；Owner 与 pending invite 永远不参与。
func (snapshot Snapshot) ActiveMemberCount() int { return len(snapshot.memberships) }

// Valid 报告 snapshot 是否可以安全进入 domain/application。
func (snapshot Snapshot) Valid() bool { return snapshot.validate() == nil }

// String 防止默认格式化展开身份、assignment 与 membership。
func (Snapshot) String() string { return visitSessionPlaceholder }

// GoString 防止 `%#v` 展开私有运行态字段。
func (Snapshot) GoString() string { return visitSessionPlaceholder }

// LogValue 让结构化日志只记录稳定占位文本。
func (Snapshot) LogValue() slog.Value { return slog.StringValue(visitSessionPlaceholder) }

// Equal 比较完整规范事实，供 store replay 与 application dependency 验证使用。
func (snapshot Snapshot) Equal(other Snapshot) bool {
	if snapshot.id != other.id || snapshot.ownerID != other.ownerID || snapshot.worldID != other.worldID || !snapshot.assignment.Equal(other.assignment) || snapshot.lifecycle != other.lifecycle || snapshot.revision != other.revision || snapshot.capacity != other.capacity || !snapshot.createdAt.Equal(other.createdAt) || !snapshot.expiresAt.Equal(other.expiresAt) || !snapshot.ownerBinding.Equal(other.ownerBinding) || snapshot.ownerGraceGeneration != other.ownerGraceGeneration || !snapshot.ownerGraceExpiresAt.Equal(other.ownerGraceExpiresAt) || len(snapshot.invites) != len(other.invites) || len(snapshot.memberships) != len(other.memberships) {
		return false
	}
	leftInvites, rightInvites := snapshot.Invites(), other.Invites()
	sort.Slice(leftInvites, func(i, j int) bool { return leftInvites[i].id.value < leftInvites[j].id.value })
	sort.Slice(rightInvites, func(i, j int) bool { return rightInvites[i].id.value < rightInvites[j].id.value })
	for index := range leftInvites {
		if leftInvites[index] != rightInvites[index] {
			return false
		}
	}
	leftMembers, rightMembers := snapshot.Memberships(), other.Memberships()
	sort.Slice(leftMembers, func(i, j int) bool { return leftMembers[i].visitorID.String() < leftMembers[j].visitorID.String() })
	sort.Slice(rightMembers, func(i, j int) bool { return rightMembers[i].visitorID.String() < rightMembers[j].visitorID.String() })
	for index := range leftMembers {
		if leftMembers[index] != rightMembers[index] {
			return false
		}
	}
	return true
}

// validate 拒绝重复 identity、越界集合、Owner-as-Visitor 与矛盾 deadline。
func (snapshot Snapshot) validate() error {
	if !snapshot.id.Valid() || !snapshot.ownerID.Valid() || !snapshot.worldID.Valid() || !snapshot.assignment.Valid() || snapshot.assignment.WorldID() != snapshot.worldID || !snapshot.lifecycle.Valid() || !snapshot.revision.Valid() || !snapshot.capacity.Valid() || snapshot.createdAt.IsZero() || snapshot.expiresAt.IsZero() || !snapshot.expiresAt.After(snapshot.createdAt) || !snapshot.ownerBinding.Valid() || snapshot.ownerBinding.actor.playerID != snapshot.ownerID {
		return errors.New("visit session snapshot is incomplete")
	}
	if snapshot.lifecycle == LifecycleOwnerGrace {
		if snapshot.ownerGraceGeneration == 0 || snapshot.ownerGraceExpiresAt.IsZero() || !snapshot.ownerGraceExpiresAt.After(snapshot.createdAt) || snapshot.ownerGraceExpiresAt.After(snapshot.expiresAt) {
			return errors.New("visit session owner grace is inconsistent")
		}
	} else if snapshot.ownerGraceGeneration != 0 || !snapshot.ownerGraceExpiresAt.IsZero() {
		return errors.New("visit session contains unexpected owner grace")
	}
	inviteIDs := make(map[string]struct{}, len(snapshot.invites))
	pending, acceptedByTarget := 0, make(map[string]InviteSnapshot)
	if len(snapshot.invites) > maximumPendingInvites+int(snapshot.capacity) {
		return errors.New("visit session invite collection is out of range")
	}
	for _, invite := range snapshot.invites {
		if !invite.Valid() || invite.targetID == snapshot.ownerID || invite.createdRevision > snapshot.revision || !invite.expiresAt.After(snapshot.createdAt) || invite.expiresAt.After(snapshot.expiresAt) {
			return errors.New("visit session invite is inconsistent")
		}
		if _, exists := inviteIDs[invite.id.value]; exists {
			return errors.New("visit session contains duplicate invite")
		}
		inviteIDs[invite.id.value] = struct{}{}
		if invite.state == InviteStatePending {
			pending++
		} else {
			if _, exists := acceptedByTarget[invite.targetID.String()]; exists {
				return errors.New("visit session contains duplicate accepted target")
			}
			acceptedByTarget[invite.targetID.String()] = invite
		}
	}
	if pending > maximumPendingInvites || len(snapshot.memberships) > int(snapshot.capacity) {
		return errors.New("visit session bounded collection is out of range")
	}
	visitors := make(map[string]struct{}, len(snapshot.memberships))
	for _, membership := range snapshot.memberships {
		if !membership.Valid() || membership.visitorID == snapshot.ownerID {
			return errors.New("visit session membership is inconsistent")
		}
		key := membership.visitorID.String()
		if _, exists := visitors[key]; exists {
			return errors.New("visit session contains duplicate visitor")
		}
		visitors[key] = struct{}{}
		acceptedInvite, exists := acceptedByTarget[key]
		if !exists || acceptedInvite.id != membership.inviteID {
			return errors.New("visit session membership has no accepted invite")
		}
		if membership.state == MembershipStateReserved && (!membership.reservationExpiresAt.After(snapshot.createdAt) || membership.reservationExpiresAt.After(acceptedInvite.expiresAt) || membership.reservationExpiresAt.After(snapshot.expiresAt)) {
			return errors.New("visit session reservation deadline is inconsistent")
		}
		if membership.state == MembershipStateReconnecting && (!membership.reconnectExpiresAt.After(snapshot.createdAt) || membership.reconnectExpiresAt.After(snapshot.expiresAt)) {
			return errors.New("visit session reconnect deadline is inconsistent")
		}
	}
	if snapshot.lifecycle == LifecycleClosed && (len(snapshot.invites) != 0 || len(snapshot.memberships) != 0) {
		return errors.New("closed visit session retains active state")
	}
	return nil
}

// canonicalOptionalTime 规范非零 adapter 时间，同时保留状态字段使用的严格零值。
func canonicalOptionalTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return canonicalTime(value)
}
