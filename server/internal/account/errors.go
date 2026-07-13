package account

import (
	"errors"
	"fmt"
)

// ErrorKind 是 transport adapter 可以稳定映射的账号失败类别。
type ErrorKind uint8

const (
	// ErrorKindUnspecified 表示调用方不应依赖的未分类错误。
	ErrorKindUnspecified ErrorKind = iota
	// ErrorKindValidation 表示输入不满足账号领域约束。
	ErrorKindValidation
	// ErrorKindUsernameConflict 表示 canonical username 已被占用。
	ErrorKindUsernameConflict
	// ErrorKindInvalidCredentials 合并未知账号、密码错误和不可认证状态。
	ErrorKindInvalidCredentials
	// ErrorKindDependencyUnavailable 表示 hasher、repository、ID 或 session 依赖失败。
	ErrorKindDependencyUnavailable
)

// String 返回适合指标和 adapter 映射的稳定低基数名称。
func (kind ErrorKind) String() string {
	switch kind {
	case ErrorKindValidation:
		return "validation"
	case ErrorKindUsernameConflict:
		return "username_conflict"
	case ErrorKindInvalidCredentials:
		return "invalid_credentials"
	case ErrorKindDependencyUnavailable:
		return "dependency_unavailable"
	default:
		return "unspecified"
	}
}

// Operation 标识不会携带用户输入的账号应用操作。
type Operation uint8

const (
	// OperationUnspecified 表示错误没有可依赖的操作语义。
	OperationUnspecified Operation = iota
	// OperationConstruct 表示 service 安全依赖不完整。
	OperationConstruct
	// OperationRegister 表示注册流程失败。
	OperationRegister
	// OperationLogin 表示登录流程失败。
	OperationLogin
)

// String 返回稳定低基数操作名。
func (operation Operation) String() string {
	switch operation {
	case OperationConstruct:
		return "construct"
	case OperationRegister:
		return "register"
	case OperationLogin:
		return "login"
	default:
		return "unspecified"
	}
}

// CommitPhase 表达 dependency failure 发生时已知的账号提交状态。
type CommitPhase uint8

const (
	// CommitPhaseNone 表示没有证据表明账号已提交。
	CommitPhaseNone CommitPhase = iota
	// CommitPhaseUnknown 表示 repository 可能已经原子提交，但响应结果不明确。
	CommitPhaseUnknown
	// CommitPhaseAccountCreated 表示账号已提交，仅后续 session 创建失败。
	CommitPhaseAccountCreated
)

// String 返回客户端恢复策略可使用的稳定阶段名称。
func (phase CommitPhase) String() string {
	switch phase {
	case CommitPhaseUnknown:
		return "account_commit_unknown"
	case CommitPhaseAccountCreated:
		return "account_created"
	default:
		return "none"
	}
}

// Error 保存稳定 kind、operation、commit phase 与受控诊断 cause。
//
// Error 文本不拼接 cause，避免 repository key、username 或 credential 被外层回显。
type Error struct {
	// kind 决定 adapter 的协议错误映射。
	kind ErrorKind
	// operation 标识失败用例，不包含用户输入。
	operation Operation
	// phase 只表达注册持久事实的已知提交状态。
	phase CommitPhase
	// cause 保留 errors.Is/As 所需诊断链，默认消息不可见。
	cause error
}

// newError 构造不会把不受信输入写入消息的账号错误。
func newError(kind ErrorKind, operation Operation, phase CommitPhase, cause error) error {
	return &Error{kind: kind, operation: operation, phase: phase, cause: cause}
}

// Error 返回适合边界响应的稳定文本。
func (failure *Error) Error() string {
	return fmt.Sprintf("account %s failed (%s, phase=%s)", failure.operation, failure.kind, failure.phase)
}

// Unwrap 允许受控诊断检查依赖 cause，但响应与普通日志不得直接展开它。
func (failure *Error) Unwrap() error { return failure.cause }

// Kind 返回 adapter 可以稳定映射的失败类别。
func (failure *Error) Kind() ErrorKind { return failure.kind }

// Operation 返回失败所属的低基数用例。
func (failure *Error) Operation() Operation { return failure.operation }

// Phase 返回注册提交状态；login 与 validation 固定为 none。
func (failure *Error) Phase() CommitPhase { return failure.phase }

// ErrorKindOf 提取账号错误类别；未知 error 返回 unspecified。
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
