package session

import (
	"errors"
	"fmt"
)

// ErrorKind 是 transport adapter 可以稳定映射的 session 失败类别。
//
// Kind 不携带凭据或身份细节，HTTP status 与 realtime error code 由外层决定。
type ErrorKind uint8

const (
	// ErrorKindUnspecified 表示调用方不应依赖的未分类错误。
	ErrorKindUnspecified ErrorKind = iota
	// ErrorKindInvalidArgument 表示可信调用边界提交了无效值。
	ErrorKindInvalidArgument
	// ErrorKindUnauthenticated 表示凭据未知、无效或已被撤销。
	ErrorKindUnauthenticated
	// ErrorKindExpired 表示凭据明确到达绝对 expiry。
	ErrorKindExpired
	// ErrorKindForbidden 表示有效身份没有请求目标所需的授权。
	ErrorKindForbidden
	// ErrorKindReplayed 表示一次性凭据已被消费。
	ErrorKindReplayed
	// ErrorKindConflict 表示原子状态迁移与当前事实冲突。
	ErrorKindConflict
	// ErrorKindDependencyUnavailable 表示 store、endpoint 或通知边界失败。
	ErrorKindDependencyUnavailable
)

// String 返回稳定低基数名称，供安全诊断使用；协议错误码仍由 adapter 显式映射。
func (kind ErrorKind) String() string {
	switch kind {
	case ErrorKindInvalidArgument:
		return "invalid_argument"
	case ErrorKindUnauthenticated:
		return "unauthenticated"
	case ErrorKindExpired:
		return "expired"
	case ErrorKindForbidden:
		return "forbidden"
	case ErrorKindReplayed:
		return "replayed"
	case ErrorKindConflict:
		return "conflict"
	case ErrorKindDependencyUnavailable:
		return "dependency_unavailable"
	default:
		return "unspecified"
	}
}

// Error 保存稳定 kind、低基数 operation 与仅供诊断链使用的 cause。
//
// Error 文本故意不拼接 cause，避免依赖错误意外回显 secret 或存储 key。
type Error struct {
	// kind 决定 adapter 的稳定失败映射。
	kind ErrorKind
	// operation 是不含用户输入的低基数操作名。
	operation string
	// cause 保留 errors.Is/As 所需诊断链，但默认输出不可见。
	cause error
}

// newError 构造不会把不受信输入写入消息的领域错误。
func newError(kind ErrorKind, operation string, cause error) error {
	return &Error{kind: kind, operation: operation, cause: cause}
}

// Error 返回适合边界响应的稳定文本，不包含内部 cause。
func (failure *Error) Error() string {
	return fmt.Sprintf("session %s failed (%s)", failure.operation, failure.kind)
}

// Unwrap 允许受控诊断代码检查依赖 cause；日志仍不得直接输出凭据输入。
func (failure *Error) Unwrap() error { return failure.cause }

// Kind 返回可供 adapter 映射的稳定错误类别。
func (failure *Error) Kind() ErrorKind { return failure.kind }

// ErrorKindOf 提取 session 错误类别；未知 error 返回 ErrorKindUnspecified。
func ErrorKindOf(err error) ErrorKind {
	var failure *Error
	if errors.As(err, &failure) {
		return failure.kind
	}
	return ErrorKindUnspecified
}
