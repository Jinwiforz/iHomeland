package placement

import (
	"errors"
	"fmt"
)

// ErrorKind 是 transport 或上层 application 可以稳定映射的 placement 失败类别。
type ErrorKind uint8

const (
	// ErrorKindUnspecified 表示调用方不应依赖的未分类错误。
	ErrorKindUnspecified ErrorKind = iota
	// ErrorKindValidation 表示输入、TTL 或依赖不满足 placement 约束。
	ErrorKindValidation
	// ErrorKindNotFound 表示目标 PersonalWorld 没有匹配的 current assignment。
	ErrorKindNotFound
	// ErrorKindConflict 表示 expected stamp 已被更高 generation 或 fence 替换。
	ErrorKindConflict
	// ErrorKindInProgress 表示 current starting assignment 正在由另一个调用推进。
	ErrorKindInProgress
	// ErrorKindExpired 表示 lease 已到期且旧 holder 不能续租或复活。
	ErrorKindExpired
	// ErrorKindDependencyUnavailable 表示 store、clock 或 ID generator 失败或违约。
	ErrorKindDependencyUnavailable
	// ErrorKindRuntimeUnavailable 表示 runtime controller 无法启动候选 instance。
	ErrorKindRuntimeUnavailable
	// ErrorKindCleanupFailed 表示 placement 已提交，但 runtime stop 清理失败。
	ErrorKindCleanupFailed
)

// String 返回 adapter 映射和指标使用的稳定低基数名称。
func (kind ErrorKind) String() string {
	switch kind {
	case ErrorKindValidation:
		return "validation"
	case ErrorKindNotFound:
		return "not_found"
	case ErrorKindConflict:
		return "conflict"
	case ErrorKindInProgress:
		return "in_progress"
	case ErrorKindExpired:
		return "expired"
	case ErrorKindDependencyUnavailable:
		return "dependency_unavailable"
	case ErrorKindRuntimeUnavailable:
		return "runtime_unavailable"
	case ErrorKindCleanupFailed:
		return "cleanup_failed"
	default:
		return "unspecified"
	}
}

// Operation 标识不会携带 world、node 或 token 的 placement application 用例。
type Operation uint8

const (
	// OperationUnspecified 表示错误没有可依赖的操作语义。
	OperationUnspecified Operation = iota
	// OperationConstruct 表示 service 依赖或 lease TTL 无效。
	OperationConstruct
	// OperationEnsureActive 表示按需启动或解析 current runtime 失败。
	OperationEnsureActive
	// OperationRenew 表示 current lease 续租失败。
	OperationRenew
	// OperationQualifyWrite 表示 point-in-time write qualification 失败。
	OperationQualifyWrite
	// OperationSleep 表示条件撤销 current assignment 或停止 runtime 失败。
	OperationSleep
	// OperationReplace 表示重建或迁移 successor 失败。
	OperationReplace
)

// String 返回稳定低基数 operation 名称。
func (operation Operation) String() string {
	switch operation {
	case OperationConstruct:
		return "construct"
	case OperationEnsureActive:
		return "ensure_active"
	case OperationRenew:
		return "renew"
	case OperationQualifyWrite:
		return "qualify_write"
	case OperationSleep:
		return "sleep"
	case OperationReplace:
		return "replace"
	default:
		return "unspecified"
	}
}

// CommitPhase 表达错误发生时可证明的 placement 提交状态。
type CommitPhase uint8

const (
	// CommitPhaseNone 表示没有证据表明目标 placement transition 已提交。
	CommitPhaseNone CommitPhase = iota
	// CommitPhaseUnknown 表示 store transition 可能提交，但无法确认 current 事实。
	CommitPhaseUnknown
	// CommitPhaseCommitted 表示已有证据证明 placement transition 已提交；后续失败可能来自
	// runtime side effect、结果校验或 adapter 契约。
	CommitPhaseCommitted
)

// String 返回恢复策略和安全诊断使用的稳定阶段名称。
func (phase CommitPhase) String() string {
	switch phase {
	case CommitPhaseUnknown:
		return "placement_commit_unknown"
	case CommitPhaseCommitted:
		return "placement_committed"
	default:
		return "none"
	}
}

// Error 保存稳定 kind、operation、commit phase 与受控诊断 cause。
//
// Error 文本不拼接 world、node、instance、fencing token 或 store payload；cause 只供受控
// errors.Is/As 诊断，不能直接作为 transport 响应或普通日志字段。
type Error struct {
	// kind 决定上层稳定失败映射。
	kind ErrorKind
	// operation 标识失败用例，不包含 identity。
	operation Operation
	// phase 表达 store transition 当前可证明的提交状态。
	phase CommitPhase
	// cause 保留受控诊断链，默认文本不可见。
	cause error
}

// newError 构造不会回显不受信或高敏 placement 数据的错误。
func newError(kind ErrorKind, operation Operation, phase CommitPhase, cause error) error {
	return &Error{kind: kind, operation: operation, phase: phase, cause: cause}
}

// Error 返回适合 application 边界响应的稳定文本。
func (failure *Error) Error() string {
	return fmt.Sprintf("placement %s failed (%s, phase=%s)", failure.operation, failure.kind, failure.phase)
}

// Unwrap 允许受控诊断检查依赖 cause，稳定错误文本不会展开它。
func (failure *Error) Unwrap() error { return failure.cause }

// Kind 返回上层可以稳定映射的失败类别。
func (failure *Error) Kind() ErrorKind { return failure.kind }

// Operation 返回失败所属的低基数 use case。
func (failure *Error) Operation() Operation { return failure.operation }

// Phase 返回 placement transition 的已知提交阶段。
func (failure *Error) Phase() CommitPhase { return failure.phase }

// ErrorKindOf 提取 placement 错误类别；未知 error 返回 unspecified。
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
