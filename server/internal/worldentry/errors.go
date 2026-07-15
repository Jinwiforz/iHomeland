package worldentry

import "errors"

// ErrorCode 是HTTP adapter可稳定映射且不包含identity的world-entry失败分类。
type ErrorCode uint8

const (
	// ErrorCodeUnspecified 禁止形成公开响应。
	ErrorCodeUnspecified ErrorCode = iota
	// ErrorCodeValidation 表示调用边界输入不完整。
	ErrorCodeValidation
	// ErrorCodeWorldNotReady 表示尚无可签发的current active assignment。
	ErrorCodeWorldNotReady
	// ErrorCodeAssignmentStale 表示读取到的current assignment事实已失效或矛盾。
	ErrorCodeAssignmentStale
	// ErrorCodeDependency 表示owner或backend暂时不可用。
	ErrorCodeDependency
	// ErrorCodeDependencyDefect 表示owner返回矛盾或malformed成功结果。
	ErrorCodeDependencyDefect
)

// Error 保存稳定分类与只供受控诊断链检查的cause。
type Error struct {
	// code 决定集中HTTP错误映射。
	code ErrorCode
	// cause 不进入普通错误文本或公开响应。
	cause error
}

// Error 返回不含backend文本与identity的稳定描述。
func (*Error) Error() string { return "world entry operation failed" }

// Unwrap 允许受控errors.Is/As，不授权外层回显cause。
func (failure *Error) Unwrap() error { return failure.cause }

// Code 返回transport可分支的稳定分类。
func (failure *Error) Code() ErrorCode { return failure.code }

// IsErrorCode 报告错误链是否包含指定world-entry分类。
func IsErrorCode(err error, code ErrorCode) bool {
	var failure *Error
	return errors.As(err, &failure) && failure.code == code
}

// operationError 构造默认脱敏的world-entry错误。
func operationError(code ErrorCode, cause error) error { return &Error{code: code, cause: cause} }
