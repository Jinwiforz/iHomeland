package visitsession

import "errors"

// Operation 是错误、store fingerprint 与指标使用的稳定低基数操作名。
type Operation uint8

const (
	// OperationUnspecified 表示调用方或 adapter 没有提供可识别操作。
	OperationUnspecified Operation = iota
	// OperationOpen 创建或解析一个 world 的 active VisitSession。
	OperationOpen
	// OperationResolve 读取一个 active VisitSession snapshot。
	OperationResolve
	// OperationCreateInvite 创建定向邀请。
	OperationCreateInvite
	// OperationRevokeInvite 撤销尚未 accept 的邀请。
	OperationRevokeInvite
	// OperationExpireInvite 清理到期邀请。
	OperationExpireInvite
	// OperationAcceptInvite 消费邀请并保留 capacity。
	OperationAcceptInvite
	// OperationJoin 使用受信 qualification 建立 Visitor binding。
	OperationJoin
	// OperationExpireReservation 清理未完成 join 的 reservation。
	OperationExpireReservation
	// OperationLeave 移除发起者自己的 membership。
	OperationLeave
	// OperationKick 由 Owner 移除指定 Visitor。
	OperationKick
	// OperationVisitorDisconnect 进入 Visitor reconnect grace。
	OperationVisitorDisconnect
	// OperationVisitorReconnect 更新 Visitor connection binding。
	OperationVisitorReconnect
	// OperationExpireVisitorReconnect 清理到期 Visitor grace。
	OperationExpireVisitorReconnect
	// OperationOwnerDisconnect 进入 Owner grace。
	OperationOwnerDisconnect
	// OperationOwnerReconnect 恢复 immutable Owner binding。
	OperationOwnerReconnect
	// OperationClose 终止 VisitSession。
	OperationClose
	// OperationExpireOwnerGrace 在 matching deadline 终止 session。
	OperationExpireOwnerGrace
	// OperationExpireSession 在 absolute expiry 终止 session。
	OperationExpireSession
	// OperationInvalidateAssignment 因 current assignment 变化终止 session。
	OperationInvalidateAssignment
	// OperationDependencyLost 因运行态依赖无法证明而终止 session。
	OperationDependencyLost
)

// String 返回指标与 adapter 诊断使用的稳定低基数名称。
func (operation Operation) String() string {
	switch operation {
	case OperationOpen:
		return "open"
	case OperationResolve:
		return "resolve"
	case OperationCreateInvite:
		return "create_invite"
	case OperationRevokeInvite:
		return "revoke_invite"
	case OperationExpireInvite:
		return "expire_invite"
	case OperationAcceptInvite:
		return "accept_invite"
	case OperationJoin:
		return "join"
	case OperationExpireReservation:
		return "expire_reservation"
	case OperationLeave:
		return "leave"
	case OperationKick:
		return "kick"
	case OperationVisitorDisconnect:
		return "visitor_disconnect"
	case OperationVisitorReconnect:
		return "visitor_reconnect"
	case OperationExpireVisitorReconnect:
		return "expire_visitor_reconnect"
	case OperationOwnerDisconnect:
		return "owner_disconnect"
	case OperationOwnerReconnect:
		return "owner_reconnect"
	case OperationClose:
		return "close"
	case OperationExpireOwnerGrace:
		return "expire_owner_grace"
	case OperationExpireSession:
		return "expire_session"
	case OperationInvalidateAssignment:
		return "invalidate_assignment"
	case OperationDependencyLost:
		return "dependency_lost"
	default:
		return "unspecified"
	}
}

// ErrorCode 是 application 可以安全分支且不会泄漏 identity 的稳定分类。
type ErrorCode uint8

const (
	// ErrorCodeUnspecified 表示未知或未分类失败。
	ErrorCodeUnspecified ErrorCode = iota
	// ErrorCodeInvalidArgument 表示受信调用边界仍提供了不完整值。
	ErrorCodeInvalidArgument
	// ErrorCodeForbidden 表示 actor 没有当前控制面操作权限。
	ErrorCodeForbidden
	// ErrorCodeNotFound 表示目标 session、invite 或 membership 不存在。
	ErrorCodeNotFound
	// ErrorCodeInvalidState 表示当前 lifecycle/state 不允许 transition。
	ErrorCodeInvalidState
	// ErrorCodeExpired 表示 deadline 已到达。
	ErrorCodeExpired
	// ErrorCodeCapacity 表示没有剩余 Visitor slot 或邀请集合已满。
	ErrorCodeCapacity
	// ErrorCodeStale 表示 assignment、epoch、binding、generation 或 revision 已过期。
	ErrorCodeStale
	// ErrorCodeIdempotencyConflict 表示相同 CommandID 绑定了不同 fingerprint。
	ErrorCodeIdempotencyConflict
	// ErrorCodeRevisionConflict 表示 expected revision 已过期。
	ErrorCodeRevisionConflict
	// ErrorCodeDependency 表示依赖调用失败且可以确认没有成功结果。
	ErrorCodeDependency
	// ErrorCodeDependencyDefect 表示 adapter 返回矛盾或 malformed 结果。
	ErrorCodeDependencyDefect
	// ErrorCodeCommitUnknown 表示无法确认 mutation 是否已提交。
	ErrorCodeCommitUnknown
)

// String 返回 adapter 错误映射与指标使用的稳定低基数名称。
func (code ErrorCode) String() string {
	switch code {
	case ErrorCodeInvalidArgument:
		return "invalid_argument"
	case ErrorCodeForbidden:
		return "forbidden"
	case ErrorCodeNotFound:
		return "not_found"
	case ErrorCodeInvalidState:
		return "invalid_state"
	case ErrorCodeExpired:
		return "expired"
	case ErrorCodeCapacity:
		return "capacity"
	case ErrorCodeStale:
		return "stale"
	case ErrorCodeIdempotencyConflict:
		return "idempotency_conflict"
	case ErrorCodeRevisionConflict:
		return "revision_conflict"
	case ErrorCodeDependency:
		return "dependency"
	case ErrorCodeDependencyDefect:
		return "dependency_defect"
	case ErrorCodeCommitUnknown:
		return "commit_unknown"
	default:
		return "unspecified"
	}
}

// CommitPhase 描述失败时 store mutation 的提交确定性。
type CommitPhase uint8

const (
	// CommitPhaseUnspecified 适用于提交前 validation 或不完整 adapter 结果。
	CommitPhaseUnspecified CommitPhase = iota
	// CommitPhaseNotCommitted 表示依赖可确认没有提交 mutation。
	CommitPhaseNotCommitted
	// CommitPhaseCommitted 表示 store 已确认首次提交或 replay。
	CommitPhaseCommitted
	// CommitPhaseUnknown 表示调用方无法确认是否提交，只能复用相同 command identity 解析。
	CommitPhaseUnknown
)

// String 返回恢复策略、指标与受控诊断使用的稳定低基数名称。
func (phase CommitPhase) String() string {
	switch phase {
	case CommitPhaseNotCommitted:
		return "not_committed"
	case CommitPhaseCommitted:
		return "committed"
	case CommitPhaseUnknown:
		return "unknown"
	default:
		return "unspecified"
	}
}

// Error 是不泄漏 VisitSession identity、assignment、command 或 binding 的稳定边界错误。
type Error struct {
	// operation 标识失败的低基数用例。
	operation Operation
	// code 是调用方可以安全分支的稳定分类。
	code ErrorCode
	// phase 表达 mutation 提交确定性。
	phase CommitPhase
	// cause 只保留内部错误链；Error 文本不会展开其可能敏感内容。
	cause error
}

// Error 返回不含任何业务 identity 的稳定诊断文本。
func (err *Error) Error() string {
	if err == nil {
		return "visit session error"
	}
	return "visit session " + err.operation.String() + " failed"
}

// Unwrap 返回内部 cause 供受控诊断使用；adapter 不得直接把 cause 返回客户端。
func (err *Error) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

// Operation 返回失败用例分类。
func (err *Error) Operation() Operation {
	if err == nil {
		return OperationUnspecified
	}
	return err.operation
}

// Code 返回稳定错误分类。
func (err *Error) Code() ErrorCode {
	if err == nil {
		return ErrorCodeUnspecified
	}
	return err.code
}

// CommitPhase 返回调用方选择重试恢复策略所需的提交确定性。
func (err *Error) CommitPhase() CommitPhase {
	if err == nil {
		return CommitPhaseUnspecified
	}
	return err.phase
}

// IsErrorCode 报告错误链是否包含指定 VisitSession 稳定分类。
func IsErrorCode(err error, code ErrorCode) bool {
	var target *Error
	return errors.As(err, &target) && target.code == code
}

// domainError 创建不会包含输入值的 transition 错误。
func domainError(operation Operation, code ErrorCode) error {
	return &Error{operation: operation, code: code, phase: CommitPhaseUnspecified}
}
