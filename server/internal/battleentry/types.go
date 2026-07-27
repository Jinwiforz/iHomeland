package battleentry

import (
	"errors"
	"log/slog"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/battleticket"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/simulationcontrol"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
)

const redactedValue = "[REDACTED_BATTLE_ENTRY]"

// TargetKind 是公开 BattleTicket request 允许选择的封闭目标类别。
type TargetKind uint8

const (
	// TargetKindUnspecified 禁止进入 application。
	TargetKindUnspecified TargetKind = iota
	// TargetKindOwnWorld 表示认证玩家自己的 primary PersonalWorld。
	TargetKindOwnWorld
	// TargetKindVisitWorld 表示认证玩家已有资格的 VisitSession。
	TargetKindVisitWorld
)

// String 返回 HTTP codec 使用的稳定目标名称。
func (kind TargetKind) String() string {
	if kind == TargetKindOwnWorld {
		return "OWN_WORLD"
	}
	if kind == TargetKindVisitWorld {
		return "VISIT_WORLD"
	}
	return "UNSPECIFIED"
}

// Target 保存 closed request 解析结果；身份、role、world 与 runtime target 不来自 payload。
type Target struct {
	// Kind 选择 own-world 或 visit-world policy。
	Kind TargetKind
	// VisitSessionID 只在 visit-world selector 中存在。
	VisitSessionID visitsession.VisitSessionID
}

// Valid 报告 selector 字段组合是否完整且无多余字段。
func (target Target) Valid() bool {
	return target.Kind == TargetKindOwnWorld && !target.VisitSessionID.Valid() ||
		target.Kind == TargetKindVisitWorld && target.VisitSessionID.Valid()
}

// String 防止默认格式化展开 VisitSession identity。
func (Target) String() string { return redactedValue }

// GoString 防止 `%#v` 展开 selector。
func (Target) GoString() string { return redactedValue }

// ReservationRequest 是 installed+active capacity owner 的原子 slot 请求。
//
// IssueID 使 response-loss retry 返回同一 slot；Target 必须是刚解析的 exact current target。
type ReservationRequest struct {
	// IssueID 是不含原始 HTTP key 的签发 identity。
	IssueID battleticket.IssueID
	// PlayerID 来自 AuthContext。
	PlayerID account.PlayerID
	// Role 来自 world/visit owner。
	Role battleticket.Role
	// Target 是 exact child/instance/revision。
	Target simulationcontrol.SimulationTarget
}

// Valid 报告容量请求是否绑定完整 actor 与 qualified target。
func (request ReservationRequest) Valid() bool {
	return request.IssueID.Valid() && request.PlayerID.Valid() && request.Role.Valid() &&
		request.Target.Validate() == nil &&
		request.Target.ActorCapacity == simulationcontrol.QualifiedActorCapacity
}

// String 防止 actor 与 target identity 进入普通日志。
func (ReservationRequest) String() string { return redactedValue }

// GoString 防止 `%#v` 展开 capacity 请求。
func (ReservationRequest) GoString() string { return redactedValue }

// VisitAuthority 是 VisitSession owner 投影给 battle admission 的只读 actor 资格。
//
// 字段绑定 actor/session/assignment/deadline；revision 只用于证明读取了完整权威版本，
// 不进入 BattleTicket，因为 target revision 与完整 assignment 已承担 runtime freshness。
type VisitAuthority struct {
	visitorID  account.PlayerID
	sessionID  session.SessionID
	epoch      session.Epoch
	assignment placement.AssignmentStamp
	expiresAt  time.Time
	revision   visitsession.Revision
}

// NewVisitAuthority 从 VisitSession owner 的只读结果构造 battle application 投影。
func NewVisitAuthority(
	visitorID account.PlayerID,
	sessionID session.SessionID,
	epoch session.Epoch,
	assignment placement.AssignmentStamp,
	expiresAt time.Time,
	revision visitsession.Revision,
) (VisitAuthority, error) {
	authority := VisitAuthority{
		visitorID: visitorID, sessionID: sessionID, epoch: epoch,
		assignment: assignment, expiresAt: expiresAt.UTC().Truncate(time.Microsecond),
		revision: revision,
	}
	if !authority.Valid() {
		return VisitAuthority{}, errors.New("visit battle authority is incomplete")
	}
	return authority, nil
}

// Valid 报告 Visitor authority 是否来自完整 owner 结果。
func (authority VisitAuthority) Valid() bool {
	return authority.visitorID.Valid() && authority.sessionID.Valid() &&
		authority.epoch.Valid() && authority.assignment.Valid() &&
		!authority.expiresAt.IsZero() && authority.revision.Valid()
}

// String 防止 actor、lineage 与 assignment 进入普通日志。
func (VisitAuthority) String() string { return redactedValue }

// GoString 防止 `%#v` 展开 VisitSession authority。
func (VisitAuthority) GoString() string { return redactedValue }

// Reservation 是 capacity owner 已原子预留的 exact actor slot。
type Reservation struct {
	// issueID 绑定幂等请求。
	issueID battleticket.IssueID
	// target 绑定不可复活 child/instance 与 revision。
	target simulationcontrol.SimulationTarget
	// slot 是 installed+active hard cap 内的零基位置。
	slot battleticket.ActorSlot
}

// NewReservation 构造 capacity owner 返回的 exact target slot。
func NewReservation(issueID battleticket.IssueID, target simulationcontrol.SimulationTarget, slot battleticket.ActorSlot) (Reservation, error) {
	reservation := Reservation{issueID: issueID, target: target, slot: slot}
	if !reservation.Valid() {
		return Reservation{}, errors.New("battle actor reservation is incomplete")
	}
	return reservation, nil
}

// IssueID 返回本 reservation 的幂等 owner identity。
func (reservation Reservation) IssueID() battleticket.IssueID { return reservation.issueID }

// Target 返回 reservation 绑定的 exact target 值副本。
func (reservation Reservation) Target() simulationcontrol.SimulationTarget { return reservation.target }

// Slot 返回 qualified hard cap 内的 actor slot。
func (reservation Reservation) Slot() battleticket.ActorSlot { return reservation.slot }

// Valid 报告 reservation 是否完整绑定 exact qualified target。
func (reservation Reservation) Valid() bool {
	return reservation.issueID.Valid() && reservation.target.Validate() == nil &&
		reservation.target.ActorCapacity == simulationcontrol.QualifiedActorCapacity &&
		reservation.slot.Valid()
}

// String 防止 slot 与 target binding 进入普通日志。
func (Reservation) String() string { return redactedValue }

// GoString 防止 `%#v` 展开 reservation。
func (Reservation) GoString() string { return redactedValue }

// ChildTicketState 是 exact child 对 ticket lifecycle 的封闭低敏投影。
type ChildTicketState uint8

const (
	// ChildTicketStateUnspecified 表示 child 没有返回可接受状态。
	ChildTicketStateUnspecified ChildTicketState = iota
	// ChildTicketStateInstalled 表示 proof key 已安装且 actor slot 已预留。
	ChildTicketStateInstalled
	// ChildTicketStateConsumed 表示 credential 已被一次握手消费。
	ChildTicketStateConsumed
	// ChildTicketStateRevoked 表示 exact ticket 已不可逆撤销。
	ChildTicketStateRevoked
	// ChildTicketStateExpired 表示 exact ticket 已到绝对 deadline。
	ChildTicketStateExpired
	// ChildTicketStateMissing 表示 exact child 没有该 binding。
	ChildTicketStateMissing
)

// ChildTicketReceipt 是 install/status/revoke 的 exact child 低敏结果。
type ChildTicketReceipt struct {
	// TicketID 回显 exact lookup identity。
	TicketID battleticket.TicketID
	// BindingFingerprint 回显完整 binding digest。
	BindingFingerprint battleticket.Digest
	// Target 回显不可复活 child/instance/revision。
	Target simulationcontrol.SimulationTarget
	// Slot 是首次安装冻结的 actor slot。
	Slot battleticket.ActorSlot
	// State 是不可逆 child lifecycle。
	State ChildTicketState
}

// MatchesInstalled 报告 receipt 是否确认了 material 的 exact installed 状态。
func (receipt ChildTicketReceipt) MatchesInstalled(target simulationcontrol.SimulationTarget, material battleticket.Material) bool {
	if !material.Valid() || receipt.State != ChildTicketStateInstalled ||
		receipt.TicketID != material.Binding().TicketID() ||
		!receipt.BindingFingerprint.Equal(material.Fingerprint()) ||
		!sameTarget(receipt.Target, target) {
		return false
	}
	return receipt.Slot == material.Binding().Facts().ActorSlot
}

// String 防止完整 child target 被默认日志展开。
func (ChildTicketReceipt) String() string { return redactedValue }

// GoString 防止 `%#v` 展开 receipt binding。
func (ChildTicketReceipt) GoString() string { return redactedValue }

// Result 是 HTTP codec 唯一允许编码的 BattleTicket client-safe projection。
type Result struct {
	// TicketID 是 UDP ClientHello 使用的非秘密 opaque identity。
	TicketID battleticket.TicketID
	// TicketSecret 是只经 HTTPS 单次交付的 bearer credential。
	TicketSecret battleticket.TicketSecret
	// Endpoint 是 trusted provider 发布的 UDP 地址。
	Endpoint battleticket.Endpoint
	// WireSuite 是固定版本与算法集合。
	WireSuite battleticket.WireSuite
	// Role 来自 PersonalWorld 或 VisitSession owner。
	Role battleticket.Role
	// TargetKind 是 request selector 对应的低敏类别。
	TargetKind TargetKind
	// TargetRevision 是响应漂移检测使用的 current SimulationTarget revision。
	TargetRevision uint64
	// ExpiresAt 是等于即失效的绝对 UTC deadline。
	ExpiresAt time.Time
}

// Valid 报告公开结果是否包含完整 credential 与 client-safe binding。
func (result Result) Valid() bool {
	if !result.TicketID.Valid() || !result.TicketSecret.Valid() || !result.Endpoint.Valid() ||
		!result.WireSuite.Valid() || !result.Role.Valid() || result.TargetRevision == 0 ||
		result.ExpiresAt.IsZero() {
		return false
	}
	return result.TargetKind == TargetKindOwnWorld && result.Role == battleticket.RoleOwner ||
		result.TargetKind == TargetKindVisitWorld && result.Role == battleticket.RoleVisitor
}

// String 防止 ticket credential 被默认格式化。
func (Result) String() string { return redactedValue }

// GoString 防止 `%#v` 展开公开签发结果。
func (Result) GoString() string { return redactedValue }

// LogValue 只允许结构化日志记录固定低敏占位符。
func (Result) LogValue() slog.Value { return slog.StringValue(redactedValue) }
