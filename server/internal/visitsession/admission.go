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
	// revision 绑定产生该结果的已提交 aggregate 版本，防止旧成员关系的迟到指令影响新成员关系。
	revision Revision
}

// NewSafeReturnDirective 构造不含 endpoint、credential 或客户端 world identity 的结果。
func NewSafeReturnDirective(visitSessionID VisitSessionID, visitorID account.PlayerID, reason SafeReturnReason, revision Revision) (SafeReturnDirective, error) {
	if !visitSessionID.Valid() || !visitorID.Valid() || !reason.Valid() || !revision.Valid() {
		return SafeReturnDirective{}, errors.New("safe return directive is incomplete")
	}
	return SafeReturnDirective{visitSessionID: visitSessionID, visitorID: visitorID, reason: reason, preferred: SafeReturnDestinationOwnPersonalWorld, fallback: SafeReturnDestinationSafeEntry, revision: revision}, nil
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

// Revision 返回产生该指令的已提交 aggregate 版本。
func (directive SafeReturnDirective) Revision() Revision { return directive.revision }

// Valid 报告 directive 是否可以进入 store result。
func (directive SafeReturnDirective) Valid() bool {
	return directive.visitSessionID.Valid() && directive.visitorID.Valid() && directive.reason.Valid() && directive.preferred == SafeReturnDestinationOwnPersonalWorld && directive.fallback == SafeReturnDestinationSafeEntry && directive.revision.Valid()
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
	// expiresAt 对accept intent等于membership deadline；verifier hydration可按credential上限收紧但不可延长。
	expiresAt time.Time
}

// AdmissionPurpose 区分公开签发入口允许的首次加入与断线恢复资格。
type AdmissionPurpose uint8

const (
	// AdmissionPurposeUnspecified 禁止进入 WorldAdmission binding。
	AdmissionPurposeUnspecified AdmissionPurpose = iota
	// AdmissionPurposeJoin 只适用于当前 reserved membership。
	AdmissionPurposeJoin
	// AdmissionPurposeReconnect 只适用于当前 reconnecting membership。
	AdmissionPurposeReconnect
)

// Valid 报告 purpose 是否属于公开签发入口的封闭集合。
func (purpose AdmissionPurpose) Valid() bool {
	return purpose == AdmissionPurposeJoin || purpose == AdmissionPurposeReconnect
}

// String 返回稳定低基数名称。
func (purpose AdmissionPurpose) String() string {
	if purpose == AdmissionPurposeJoin {
		return "join"
	}
	if purpose == AdmissionPurposeReconnect {
		return "reconnect"
	}
	return "unspecified"
}

// AdmissionEligibility 是 VisitSession owner 对当前 actor 的只读签发资格结论。
//
// 结果没有 credential、nonce 或 endpoint；WorldAdmission owner仍须独立验证 placement并签发。
type AdmissionEligibility struct {
	// intent 绑定当前 actor、session lineage、assignment 与最早 deadline。
	intent AdmissionIntent
	// purpose 由 membership state 唯一决定，payload 不能选择。
	purpose AdmissionPurpose
	// revision 是签发判断读取的权威 VisitSession CAS 版本。
	revision Revision
}

// Intent 返回非凭据 admission intent 值副本。
func (eligibility AdmissionEligibility) Intent() AdmissionIntent { return eligibility.intent }

// Purpose 返回领域 owner 根据 membership state 决定的用途。
func (eligibility AdmissionEligibility) Purpose() AdmissionPurpose { return eligibility.purpose }

// Revision 返回签发判断所依据的权威 VisitSession revision。
func (eligibility AdmissionEligibility) Revision() Revision { return eligibility.revision }

// Valid 报告结果是否包含完整 intent 与封闭 purpose。
func (eligibility AdmissionEligibility) Valid() bool {
	return eligibility.intent.Valid() && eligibility.purpose.Valid() && eligibility.revision.Valid()
}

// String 防止默认格式化展开 actor、lineage 与 assignment。
func (AdmissionEligibility) String() string { return admissionPlaceholder }

// GoString 与 String 保持相同脱敏边界。
func (AdmissionEligibility) GoString() string { return admissionPlaceholder }

// LogValue 仅输出稳定占位文本。
func (AdmissionEligibility) LogValue() slog.Value { return slog.StringValue(admissionPlaceholder) }

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

// qualificationPurpose 区分首次加入与断线恢复，禁止同一资格跨状态复用。
type qualificationPurpose uint8

const (
	qualificationPurposeUnspecified qualificationPurpose = iota
	qualificationPurposeJoin
	qualificationPurposeReconnect
)

// JoinQualification 表示 world admission verifier 已确认的单次 VisitSession 输入。
//
// 名称为兼容既有 Join API 保留；qualification 同时以封闭 purpose 支持 VisitorReconnect。
// 导出的 hydration 入口只供 worldadmission owner 在原子消费后调用，application 仍会重新
// 验证 AuthContext、membership、deadline 与 current full assignment。
type JoinQualification struct {
	// intent 保存 verifier 已恢复的完整 Visitor binding。
	intent AdmissionIntent
	// purpose 防止 JOIN 与 RECONNECT credential互换。
	purpose qualificationPurpose
}

// HydrateJoinQualification 从已验证的 JOIN binding 构造单次受信输入。
//
// 该导出桥接只允许 worldadmission owner 在 credential 已原子消费且复核 placement 后调用；
// transport、payload、invite 或 AdmissionIntent 不得直接提升为 qualification。字段必须来自同一
// verifier binding，expiresAt 是等于即失效的 UTC 微秒 deadline。
func HydrateJoinQualification(visitSessionID VisitSessionID, visitorID account.PlayerID, sessionID session.SessionID, epoch session.Epoch, assignment placement.AssignmentStamp, expiresAt time.Time) (JoinQualification, error) {
	return hydrateQualification(qualificationPurposeJoin, visitSessionID, visitorID, sessionID, epoch, assignment, expiresAt)
}

// HydrateReconnectQualification 从已验证的 RECONNECT binding 构造单次受信输入。
//
// 该导出桥接只允许 worldadmission owner 在 credential 已原子消费且复核 placement 后调用；
// transport、payload、invite 或 AdmissionIntent 不得直接提升为 qualification。VisitSession 仍会
// 重新比较 AuthContext、reconnecting membership、完整 assignment 与恢复 deadline。
func HydrateReconnectQualification(visitSessionID VisitSessionID, visitorID account.PlayerID, sessionID session.SessionID, epoch session.Epoch, assignment placement.AssignmentStamp, expiresAt time.Time) (JoinQualification, error) {
	return hydrateQualification(qualificationPurposeReconnect, visitSessionID, visitorID, sessionID, epoch, assignment, expiresAt)
}

// hydrateQualification 统一验证 verifier hydration 的完整字段。
func hydrateQualification(purpose qualificationPurpose, visitSessionID VisitSessionID, visitorID account.PlayerID, sessionID session.SessionID, epoch session.Epoch, assignment placement.AssignmentStamp, expiresAt time.Time) (JoinQualification, error) {
	intent, err := HydrateAdmissionIntent(visitSessionID, visitorID, sessionID, epoch, assignment, expiresAt)
	if err != nil || (purpose != qualificationPurposeJoin && purpose != qualificationPurposeReconnect) {
		return JoinQualification{}, errors.New("visit admission qualification is incomplete")
	}
	return JoinQualification{intent: intent, purpose: purpose}, nil
}

// validFor 报告 qualification 是否由完整 verifier binding 构造且 purpose精确匹配。
func (qualification JoinQualification) validFor(purpose qualificationPurpose) bool {
	return qualification.intent.Valid() && qualification.purpose == purpose
}

// String 防止默认格式化扩散潜在 admission material。
func (JoinQualification) String() string { return admissionPlaceholder }

// GoString 防止 `%#v` 展开 qualification 私有字段。
func (JoinQualification) GoString() string { return admissionPlaceholder }

// LogValue 让结构化日志只记录稳定占位文本。
func (JoinQualification) LogValue() slog.Value { return slog.StringValue(admissionPlaceholder) }
