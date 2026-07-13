package personalworld

import (
	"errors"
	"fmt"
)

// ErrorKind 是 transport adapter 可以稳定映射的 PersonalWorld 失败类别。
type ErrorKind uint8

const (
	// ErrorKindUnspecified 表示调用方不应依赖的未分类错误。
	ErrorKindUnspecified ErrorKind = iota
	// ErrorKindValidation 表示输入或构造依赖不满足领域约束。
	ErrorKindValidation
	// ErrorKindNotFound 表示指定 PersonalWorld 不存在。
	ErrorKindNotFound
	// ErrorKindForbidden 表示可信 actor 不是 immutable WorldOwnerID。
	ErrorKindForbidden
	// ErrorKindRevisionConflict 表示 expected revision 已经过期。
	ErrorKindRevisionConflict
	// ErrorKindIdempotencyConflict 表示同一 key 被用于不同 command。
	ErrorKindIdempotencyConflict
	// ErrorKindInvalidState 表示 lifecycle 不接受目标 mutation。
	ErrorKindInvalidState
	// ErrorKindDependencyUnavailable 表示 repository、clock 或 ID 边界失败或违约。
	ErrorKindDependencyUnavailable
)

// String 返回适合指标和 adapter 映射的稳定低基数名称。
func (kind ErrorKind) String() string {
	switch kind {
	case ErrorKindValidation:
		return "validation"
	case ErrorKindNotFound:
		return "not_found"
	case ErrorKindForbidden:
		return "forbidden"
	case ErrorKindRevisionConflict:
		return "revision_conflict"
	case ErrorKindIdempotencyConflict:
		return "idempotency_conflict"
	case ErrorKindInvalidState:
		return "invalid_state"
	case ErrorKindDependencyUnavailable:
		return "dependency_unavailable"
	default:
		return "unspecified"
	}
}

// Operation 标识不会携带用户输入的 PersonalWorld application 用例。
type Operation uint8

const (
	// OperationUnspecified 表示错误没有可依赖的操作语义。
	OperationUnspecified Operation = iota
	// OperationConstruct 表示 service 依赖不完整。
	OperationConstruct
	// OperationEnsurePrimary 表示 primary world 创建或解析失败。
	OperationEnsurePrimary
	// OperationArchive 表示 Owner archive mutation 失败。
	OperationArchive
)

// String 返回稳定低基数操作名。
func (operation Operation) String() string {
	switch operation {
	case OperationConstruct:
		return "construct"
	case OperationEnsurePrimary:
		return "ensure_primary"
	case OperationArchive:
		return "archive"
	default:
		return "unspecified"
	}
}

// CommitPhase 表达失败发生时已知的 PersonalWorld 持久提交状态。
type CommitPhase uint8

const (
	// CommitPhaseNone 表示没有证据表明本次操作已提交。
	CommitPhaseNone CommitPhase = iota
	// CommitPhaseUnknown 表示 transaction 可能提交，但调用方无法确认。
	CommitPhaseUnknown
	// CommitPhaseCommitted 表示 repository 已明确提交，但返回结果违反契约或后续处理失败。
	CommitPhaseCommitted
)

// String 返回恢复策略和安全诊断可使用的稳定阶段名称。
func (phase CommitPhase) String() string {
	switch phase {
	case CommitPhaseUnknown:
		return "world_commit_unknown"
	case CommitPhaseCommitted:
		return "world_committed"
	default:
		return "none"
	}
}

// Error 保存稳定 kind、operation、commit phase 与受控诊断 cause。
//
// Error 文本不拼接 cause、idempotency key 或 repository snapshot，避免不受信输入和
// 持久记录通过 HTTP/realtime adapter 或普通日志回显。
type Error struct {
	// kind 决定 adapter 的稳定失败映射。
	kind ErrorKind
	// operation 标识失败用例，不包含世界或玩家 identity。
	operation Operation
	// phase 表达 transaction 当前可证明的提交状态。
	phase CommitPhase
	// cause 保留 errors.Is/As 所需诊断链，默认文本不可见。
	cause error
}

// newError 构造不会把不受信输入写入消息的 PersonalWorld 错误。
func newError(kind ErrorKind, operation Operation, phase CommitPhase, cause error) error {
	return &Error{kind: kind, operation: operation, phase: phase, cause: cause}
}

// Error 返回适合边界响应的稳定文本。
func (failure *Error) Error() string {
	return fmt.Sprintf("personal world %s failed (%s, phase=%s)", failure.operation, failure.kind, failure.phase)
}

// Unwrap 允许受控诊断检查依赖 cause；响应与普通日志不得直接展开它。
func (failure *Error) Unwrap() error { return failure.cause }

// Kind 返回 adapter 可以稳定映射的失败类别。
func (failure *Error) Kind() ErrorKind { return failure.kind }

// Operation 返回失败所属的低基数 use case。
func (failure *Error) Operation() Operation { return failure.operation }

// Phase 返回持久 transaction 的已知提交状态。
func (failure *Error) Phase() CommitPhase { return failure.phase }

// ErrorKindOf 提取 PersonalWorld 错误类别；未知 error 返回 unspecified。
func ErrorKindOf(err error) ErrorKind {
	var failure *Error
	if errors.As(err, &failure) {
		return failure.kind
	}
	return ErrorKindUnspecified
}

// CommitPhaseOf 提取安全提交阶段；未知 error 返回 none。
func CommitPhaseOf(err error) CommitPhase {
	var failure *Error
	if errors.As(err, &failure) {
		return failure.phase
	}
	return CommitPhaseNone
}
