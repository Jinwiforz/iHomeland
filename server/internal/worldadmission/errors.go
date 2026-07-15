package worldadmission

import "errors"

// ErrorCode 是 application/transport adapter 映射 stable error registry 的封闭决议。
type ErrorCode uint8

const (
	// ErrorCodeUnspecified 表示禁止返回的零值。
	ErrorCodeUnspecified ErrorCode = iota
	// ErrorCodeInvalidArgument 表示调用方没有提供完整受信输入。
	ErrorCodeInvalidArgument
	// ErrorCodeInvalid 表示 credential 不存在或静态 binding 不匹配。
	ErrorCodeInvalid
	// ErrorCodeExpired 表示业务 expiry 已到达。
	ErrorCodeExpired
	// ErrorCodeReplayed 表示 credential 已被不同 consume identity 使用。
	ErrorCodeReplayed
	// ErrorCodeStaleAssignment 表示 current full assignment 已改变或失效。
	ErrorCodeStaleAssignment
	// ErrorCodeIdempotencyConflict 表示相同 issuance identity 改变语义。
	ErrorCodeIdempotencyConflict
	// ErrorCodeDependency 表示依赖失败但未发现损坏状态。
	ErrorCodeDependency
	// ErrorCodeDependencyDefect 表示 adapter 返回矛盾、损坏或未知 schema。
	ErrorCodeDependencyDefect
	// ErrorCodeCommitUnknown 表示 Redis mutation 的提交状态无法证明。
	ErrorCodeCommitUnknown
)

// Error 是不包含 credential/binding/backend detail 的稳定安全错误。
type Error struct {
	// operation 是固定低基数阶段名。
	operation string
	// code 是 transport adapter 映射 stable registry 的决议。
	code ErrorCode
	// cause 只供进程内诊断链使用，不编码给客户端。
	cause error
}

// Error 返回适合诊断聚合的稳定文本，不拼接底层错误或 identity。
func (err *Error) Error() string {
	if err == nil {
		return "world admission error"
	}
	return "world admission " + err.operation + " failed"
}

// Unwrap 只供进程内 errors.Is/As 与测试读取，不得直接编码到客户端。
func (err *Error) Unwrap() error { return err.cause }

// Code 返回 transport adapter 应映射的稳定决议。
func (err *Error) Code() ErrorCode { return err.code }

// IsErrorCode 报告错误链是否包含指定 world admission 决议。
func IsErrorCode(err error, code ErrorCode) bool {
	var typed *Error
	return errors.As(err, &typed) && typed.code == code
}

// operationError 构造统一脱敏错误。
func operationError(operation string, code ErrorCode, cause error) error {
	return &Error{operation: operation, code: code, cause: cause}
}
