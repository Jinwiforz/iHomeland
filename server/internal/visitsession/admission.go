package visitsession

import (
	"errors"
	"log/slog"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
)

// SafeReturnReason 是 Visitor 离开当前访客世界的封闭原因。
type SafeReturnReason uint8

const (
	// SafeReturnReasonUnspecified 是禁止进入提交结果的零值。
	SafeReturnReasonUnspecified SafeReturnReason = iota
	// SafeReturnReasonVoluntaryLeave 表示 Visitor 主动离开。
	SafeReturnReasonVoluntaryLeave
	// SafeReturnReasonKicked 表示 immutable Owner 移除 Visitor。
	SafeReturnReasonKicked
	// SafeReturnReasonOwnerClosed 表示 Owner 显式关闭访问。
	SafeReturnReasonOwnerClosed
	// SafeReturnReasonOwnerUnavailable 表示 Owner grace 到期仍未恢复。
	SafeReturnReasonOwnerUnavailable
	// SafeReturnReasonSessionExpired 表示 VisitSession 到达绝对 expiry。
	SafeReturnReasonSessionExpired
	// SafeReturnReasonAssignmentChanged 表示 current assignment 已丢失或替换。
	SafeReturnReasonAssignmentChanged
	// SafeReturnReasonVisitorReconnectExpired 表示 Visitor 恢复窗口到期。
	SafeReturnReasonVisitorReconnectExpired
	// SafeReturnReasonDependencyLost 表示可失效运行态无法再被安全证明。
	SafeReturnReasonDependencyLost
)

// String 返回 storage enum、指标与诊断使用的稳定低基数名称。
func (reason SafeReturnReason) String() string {
	switch reason {
	case SafeReturnReasonVoluntaryLeave:
		return "voluntary_leave"
	case SafeReturnReasonKicked:
		return "kicked"
	case SafeReturnReasonOwnerClosed:
		return "owner_closed"
	case SafeReturnReasonOwnerUnavailable:
		return "owner_unavailable"
	case SafeReturnReasonSessionExpired:
		return "session_expired"
	case SafeReturnReasonAssignmentChanged:
		return "assignment_changed"
	case SafeReturnReasonVisitorReconnectExpired:
		return "visitor_reconnect_expired"
	case SafeReturnReasonDependencyLost:
		return "dependency_lost"
	default:
		return "unspecified"
	}
}

// Valid 报告 reason 是否属于可提交的封闭集合。
func (reason SafeReturnReason) Valid() bool {
	return reason >= SafeReturnReasonVoluntaryLeave && reason <= SafeReturnReasonDependencyLost
}

// SafeReturnDestination 表达后续 owner 应尝试的目标类别，不包含客户端可选 world identity。
type SafeReturnDestination uint8

const (
	// SafeReturnDestinationUnspecified 是禁止进入结果的零值。
	SafeReturnDestinationUnspecified SafeReturnDestination = iota
	// SafeReturnDestinationOwnPersonalWorld 表示优先解析 Visitor 自己的 PersonalWorld。
	SafeReturnDestinationOwnPersonalWorld
	// SafeReturnDestinationSafeEntry 表示 own world 不可用时进入受信安全入口。
	SafeReturnDestinationSafeEntry
)

// String 返回后续迁移 owner 使用的稳定低基数目标类别。
func (destination SafeReturnDestination) String() string {
	switch destination {
	case SafeReturnDestinationOwnPersonalWorld:
		return "own_personal_world"
	case SafeReturnDestinationSafeEntry:
		return "safe_entry"
	default:
		return "unspecified"
	}
}

// Valid 报告 destination 是否属于封闭目标类别。
func (destination SafeReturnDestination) Valid() bool {
	return destination == SafeReturnDestinationOwnPersonalWorld || destination == SafeReturnDestinationSafeEntry
}

// SafeReturnDirective 是 core 原子结果中的确定性迁移意图。
//
// Directive 不表示 transport 通知、own-world 启动或连接迁移已经发生；后续 owner 必须按
// command replay 返回的同一结果幂等执行 side effect。
type SafeReturnDirective struct {
	// visitSessionID 绑定产生结果的 terminal 或单成员 mutation。
	visitSessionID VisitSessionID
	// visitorID 是必须离开旧 assignment 的认证玩家身份。
	visitorID account.PlayerID
	// reason 是不可由客户端改写的封闭原因。
	reason SafeReturnReason
	// preferred 表示优先 own PersonalWorld。
	preferred SafeReturnDestination
	// fallback 表示 preferred 不可用时的安全入口。
	fallback SafeReturnDestination
}

// NewSafeReturnDirective 构造不含 endpoint、credential 或客户端 world identity 的结果。
func NewSafeReturnDirective(visitSessionID VisitSessionID, visitorID account.PlayerID, reason SafeReturnReason) (SafeReturnDirective, error) {
	if !visitSessionID.Valid() || !visitorID.Valid() || !reason.Valid() {
		return SafeReturnDirective{}, errors.New("safe return directive is incomplete")
	}
	return SafeReturnDirective{visitSessionID: visitSessionID, visitorID: visitorID, reason: reason, preferred: SafeReturnDestinationOwnPersonalWorld, fallback: SafeReturnDestinationSafeEntry}, nil
}

// VisitSessionID 返回产生结果的 aggregate identity。
func (directive SafeReturnDirective) VisitSessionID() VisitSessionID { return directive.visitSessionID }

// VisitorID 返回后续迁移 owner 必须处理的玩家身份。
func (directive SafeReturnDirective) VisitorID() account.PlayerID { return directive.visitorID }

// Reason 返回不可更改的离开原因。
func (directive SafeReturnDirective) Reason() SafeReturnReason { return directive.reason }

// PreferredDestination 返回优先目标类别。
func (directive SafeReturnDirective) PreferredDestination() SafeReturnDestination {
	return directive.preferred
}

// FallbackDestination 返回 preferred 不可用时的目标类别。
func (directive SafeReturnDirective) FallbackDestination() SafeReturnDestination {
	return directive.fallback
}

// Valid 报告 directive 是否可以进入 store result。
func (directive SafeReturnDirective) Valid() bool {
	return directive.visitSessionID.Valid() && directive.visitorID.Valid() && directive.reason.Valid() && directive.preferred == SafeReturnDestinationOwnPersonalWorld && directive.fallback == SafeReturnDestinationSafeEntry
}

// String 防止默认格式化扩散 aggregate 与 Visitor identity。
func (SafeReturnDirective) String() string { return "[REDACTED_SAFE_RETURN_DIRECTIVE]" }

// GoString 防止 `%#v` 展开结果身份。
func (SafeReturnDirective) GoString() string { return "[REDACTED_SAFE_RETURN_DIRECTIVE]" }

// LogValue 让结构化日志只记录稳定占位文本。
func (SafeReturnDirective) LogValue() slog.Value {
	return slog.StringValue("[REDACTED_SAFE_RETURN_DIRECTIVE]")
}

// AdmissionIntent 是 accept 成功后交给未来 admission issuer 的非凭据投影。
//
// Intent 没有签名、nonce、endpoint 或连接权限，不能直接调用 Join。调用方只能把它交给
// 后续 admission owner；expiry 等于即失效且不晚于邀请、session 与 assignment lease。
type AdmissionIntent struct {
	// visitSessionID 绑定 reservation 所属 aggregate。
	visitSessionID VisitSessionID
	// visitorID 绑定 accept 时的认证 PlayerID。
	visitorID account.PlayerID
	// sessionID 与 epoch 固定 accept 时认证 lineage。
	sessionID session.SessionID
	// epoch 防止旧 intent 恢复已经失效的 lineage。
	epoch session.Epoch
	// assignment 绑定 accept 时重新确认的完整 current stamp。
	assignment placement.AssignmentStamp
	// expiresAt 是 reservation 与 intent 共同使用的 UTC 微秒 deadline。
	expiresAt time.Time
}

// newAdmissionIntent 只允许 aggregate 在成功 accept transition 中创建 intent。
func newAdmissionIntent(visitSessionID VisitSessionID, actor Actor, assignment placement.AssignmentStamp, expiresAt time.Time) (AdmissionIntent, error) {
	intent := AdmissionIntent{visitSessionID: visitSessionID, visitorID: actor.playerID, sessionID: actor.sessionID, epoch: actor.epoch, assignment: assignment, expiresAt: canonicalOptionalTime(expiresAt)}
	if !intent.Valid() {
		return AdmissionIntent{}, errors.New("admission intent is incomplete")
	}
	return intent, nil
}

// HydrateAdmissionIntent 从已提交的 accept result 恢复非凭据 reservation 投影。
//
// 该入口不创建签名、nonce、endpoint、JoinQualification 或任何连接权限；NewMutationResult
// 仍会把 intent 与 reserved membership、assignment 和 deadline 做完整交叉校验。
func HydrateAdmissionIntent(visitSessionID VisitSessionID, visitorID account.PlayerID, sessionID session.SessionID, epoch session.Epoch, assignment placement.AssignmentStamp, expiresAt time.Time) (AdmissionIntent, error) {
	intent := AdmissionIntent{visitSessionID: visitSessionID, visitorID: visitorID, sessionID: sessionID, epoch: epoch, assignment: assignment, expiresAt: canonicalOptionalTime(expiresAt)}
	if !intent.Valid() {
		return AdmissionIntent{}, errors.New("admission intent hydration is incomplete")
	}
	return intent, nil
}

// VisitSessionID 返回 reservation 所属 aggregate identity。
func (intent AdmissionIntent) VisitSessionID() VisitSessionID { return intent.visitSessionID }

// VisitorID 返回 accept 时的认证 PlayerID。
func (intent AdmissionIntent) VisitorID() account.PlayerID { return intent.visitorID }

// SessionID 返回 accept 时的认证 lineage。
func (intent AdmissionIntent) SessionID() session.SessionID { return intent.sessionID }

// Epoch 返回 intent 固定的 session 屏障。
func (intent AdmissionIntent) Epoch() session.Epoch { return intent.epoch }

// Assignment 返回 accept 时重新确认的完整 current stamp。
func (intent AdmissionIntent) Assignment() placement.AssignmentStamp { return intent.assignment }

// ExpiresAt 返回等于即失效的 UTC 微秒 deadline。
func (intent AdmissionIntent) ExpiresAt() time.Time { return intent.expiresAt }

// Valid 报告 intent 是否来自完整 accept 结果；有效不表示具备连接权限。
func (intent AdmissionIntent) Valid() bool {
	return intent.visitSessionID.Valid() && intent.visitorID.Valid() && intent.sessionID.Valid() && intent.epoch.Valid() && intent.assignment.Valid() && !intent.expiresAt.IsZero()
}

// String 防止默认格式化把 intent 误传播为 credential。
func (AdmissionIntent) String() string { return admissionPlaceholder }

// GoString 防止 `%#v` 展开 intent identity 与 assignment。
func (AdmissionIntent) GoString() string { return admissionPlaceholder }

// LogValue 让结构化日志只记录稳定占位文本。
func (AdmissionIntent) LogValue() slog.Value { return slog.StringValue(admissionPlaceholder) }

// JoinQualification 表示未来 admission verifier 已确认的单次 join 输入。
//
// 当前 production package 故意不导出 constructor，因此 invite 或 AdmissionIntent 不能被
// 调用方自行提升为资格。后续 admission change 必须在本 owner 边界增加受信构造路径。
type JoinQualification struct {
	// intent 只由未来包内受信 verifier 路径封装，外部调用方不能写入。
	intent AdmissionIntent
}

// valid 报告 qualification 是否由包内受信 verifier 路径构造。
func (qualification JoinQualification) valid() bool { return qualification.intent.Valid() }

// String 防止默认格式化扩散潜在 admission material。
func (JoinQualification) String() string { return admissionPlaceholder }

// GoString 防止 `%#v` 展开 qualification 私有字段。
func (JoinQualification) GoString() string { return admissionPlaceholder }

// LogValue 让结构化日志只记录稳定占位文本。
func (JoinQualification) LogValue() slog.Value { return slog.StringValue(admissionPlaceholder) }
