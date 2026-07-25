package battleticket

import "errors"

// ErrorCode 是 application 与 transport adapter 可以稳定映射的封闭失败决议。
type ErrorCode uint8

const (
	// ErrorCodeUnspecified 是禁止返回的零值。
	ErrorCodeUnspecified ErrorCode = iota
	// ErrorCodeInvalidArgument 表示调用方没有提供完整受信输入。
	ErrorCodeInvalidArgument
	// ErrorCodeTargetNotReady 表示 current target 或 listener readiness 尚不可证明。
	ErrorCodeTargetNotReady
	// ErrorCodeCapacityExceeded 表示 exact instance 的 installed+active actor 已达到 8。
	ErrorCodeCapacityExceeded
	// ErrorCodeTargetStale 表示 assignment、target revision、node 或 instance 已被替换。
	ErrorCodeTargetStale
	// ErrorCodeIdempotencyConflict 表示同一 issuance identity 改变了权威 target 语义。
	ErrorCodeIdempotencyConflict
	// ErrorCodeExpired 表示首次冻结的绝对 expiry 已到达。
	ErrorCodeExpired
	// ErrorCodeDependency 表示依赖暂时失败但没有损坏证据。
	ErrorCodeDependency
	// ErrorCodeDependencyDefect 表示 adapter 返回矛盾或损坏状态。
	ErrorCodeDependencyDefect
	// ErrorCodeCommitUnknown 表示 mutation 的提交结果无法证明。
	ErrorCodeCommitUnknown
)

// String 返回低基数日志与 metrics 使用的稳定名称。
func (code ErrorCode) String() string {
	switch code {
	case ErrorCodeInvalidArgument:
		return "invalid_argument"
	case ErrorCodeTargetNotReady:
		return "target_not_ready"
	case ErrorCodeCapacityExceeded:
		return "capacity_exceeded"
	case ErrorCodeTargetStale:
		return "target_stale"
	case ErrorCodeIdempotencyConflict:
		return "idempotency_conflict"
	case ErrorCodeExpired:
		return "expired"
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

// Error 是不包含 credential、binding 或 backend detail 的稳定安全错误。
type Error struct {
	// operation 是固定低基数阶段名。
	operation string
	// code 是公开恢复逻辑可分支的稳定决议。
	code ErrorCode
	// cause 只供进程内 errors.Is/As 使用，不进入 Error 文本。
	cause error
}

// Error 返回固定 operation/code，不拼接 secret、identity 或底层错误。
func (failure *Error) Error() string {
	if failure == nil {
		return "battle ticket failure"
	}
	return "battle ticket " + failure.operation + " failed: " + failure.code.String()
}

// Unwrap 保留进程内取消和依赖诊断链。
func (failure *Error) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.cause
}

// Code 返回 transport adapter 映射 errors.json 使用的稳定决议。
func (failure *Error) Code() ErrorCode {
	if failure == nil {
		return ErrorCodeUnspecified
	}
	return failure.code
}

// Operation 返回固定低基数阶段名。
func (failure *Error) Operation() string {
	if failure == nil {
		return ""
	}
	return failure.operation
}

// ErrorCodeOf 从 error chain 读取稳定决议；非本包错误返回零值。
func ErrorCodeOf(err error) ErrorCode {
	var failure *Error
	if errors.As(err, &failure) {
		return failure.Code()
	}
	return ErrorCodeUnspecified
}

// NewAdmissionError 为跨 owner application orchestration 构造稳定 BattleTicket 失败。
//
// operation 必须由调用方使用固定低基数字面量；cause 只保留在进程内错误链，不进入文本。
func NewAdmissionError(operation string, code ErrorCode, cause error) error {
	if operation == "" || code == ErrorCodeUnspecified {
		return operationError("admission", ErrorCodeDependencyDefect, cause)
	}
	return operationError(operation, code, cause)
}

// operationError 构造不会回显 cause 的包内失败。
func operationError(operation string, code ErrorCode, cause error) error {
	return &Error{operation: operation, code: code, cause: cause}
}
