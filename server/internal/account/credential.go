package account

import (
	"errors"
	"log/slog"
)

const (
	// minimumRegisterPasswordBytes 是新凭据的最低 byte 强度门槛；不按 rune 计数，避免编码差异改变接受集合。
	minimumRegisterPasswordBytes = 12
	// minimumLoginPasswordBytes 只拒绝空输入，使未来旧策略账号仍可验证而不会被新注册门槛锁死。
	minimumLoginPasswordBytes = 1
	// maximumPasswordBytes 限制 memory-hard hasher 的单请求输入成本和敏感数据驻留大小。
	maximumPasswordBytes = 128
	// maximumCredentialHashBytes 为自描述算法参数、salt 与未来升级信息保留空间，同时阻止异常存储放大。
	maximumCredentialHashBytes = 4096
)

// RegisterPassword 保存未经 trim、case-fold 或 Unicode normalization 的注册密码。
//
// 该值只用于一次 Hash 调用，不提供序列化能力。Go string 无法主动清零，因此调用方必须
// 缩短其生命周期，不能把实例放入日志、缓存、全局状态或异步队列。
type RegisterPassword struct {
	// value 保留调用方提交的原始 bytes，只允许 credential hasher 在当前调用期间读取。
	value string
}

// NewRegisterPassword 仅执行注册安全长度边界，不改变原始 bytes。
//
// 长度错误使用固定文本，不回显输入、实际内容或可用于日志关联的摘要。
func NewRegisterPassword(value string) (RegisterPassword, error) {
	if len(value) < minimumRegisterPasswordBytes || len(value) > maximumPasswordBytes {
		return RegisterPassword{}, errors.New("register password length is invalid")
	}
	return RegisterPassword{value: value}, nil
}

// Value 返回 hasher 必需的原始密码；返回 string 与值共享敏感内容，调用方不得持久化、记录或保留结果。
func (password RegisterPassword) Value() string { return password.value }

// Valid 报告 password 是否经过长度边界构造；零值必须在进入 hasher 前被拒绝。
func (password RegisterPassword) Valid() bool { return password.value != "" }

// String 返回固定占位符，避免默认格式化泄漏 plaintext。
func (RegisterPassword) String() string { return credentialPlaceholder }

// GoString 防止 `%#v` 展开私有 plaintext 字段。
func (RegisterPassword) GoString() string { return credentialPlaceholder }

// LogValue 让 slog 只记录固定凭据占位符。
func (RegisterPassword) LogValue() slog.Value { return slog.StringValue(credentialPlaceholder) }

// LoginPassword 保存未经变换的登录密码，并允许未来验证旧密码策略。
//
// 该类型与 RegisterPassword 分离，避免提高新密码门槛时意外拒绝历史账号登录。实例具有
// 与注册密码相同的短生命周期和禁止记录约束。
type LoginPassword struct {
	// value 保留调用方提交的原始 bytes，只允许 credential verifier 在当前调用期间读取。
	value string
}

// NewLoginPassword 允许 1-128 bytes，避免未来旧密码被新注册策略锁死。
func NewLoginPassword(value string) (LoginPassword, error) {
	if len(value) < minimumLoginPasswordBytes || len(value) > maximumPasswordBytes {
		return LoginPassword{}, errors.New("login password length is invalid")
	}
	return LoginPassword{value: value}, nil
}

// Value 返回 verifier 必需的原始密码；返回 string 与值共享敏感内容，调用方不得持久化、记录或保留结果。
func (password LoginPassword) Value() string { return password.value }

// Valid 报告 password 是否经过长度边界构造；零值不能作为 dummy verification 的替代输入。
func (password LoginPassword) Valid() bool { return password.value != "" }

// String 返回固定占位符，避免默认格式化泄漏 plaintext。
func (LoginPassword) String() string { return credentialPlaceholder }

// GoString 防止 `%#v` 展开私有 plaintext 字段。
func (LoginPassword) GoString() string { return credentialPlaceholder }

// LogValue 让 slog 只记录固定凭据占位符。
func (LoginPassword) LogValue() slog.Value { return slog.StringValue(credentialPlaceholder) }

// CredentialHash 保存具体 hasher 生成的自描述不可逆凭据表示。
//
// Hash 不是 bearer credential，但仍包含算法参数和 salt，默认按敏感值处理。只有 repository
// 与 CredentialHasher.Verify 可以读取 encoded 内容，公开响应和普通诊断不得持有该值。
type CredentialHash struct {
	// encoded 只允许 repository 和 verifier 在受控边界读取。
	encoded string
}

// NewCredentialHash 拒绝空值、非可打印 ASCII 和异常大小，具体算法标识、参数与版本由 hasher adapter 验证。
//
// 这里不解析某一种算法格式，避免 account domain 在 production algorithm 锁定前反向拥有
// infrastructure 规则；可打印 ASCII 限制保证数据库、配置和诊断边界不会出现隐藏分隔符。
func NewCredentialHash(encoded string) (CredentialHash, error) {
	if len(encoded) == 0 || len(encoded) > maximumCredentialHashBytes {
		return CredentialHash{}, errors.New("credential hash length is invalid")
	}
	for index := 0; index < len(encoded); index++ {
		if encoded[index] < 0x21 || encoded[index] > 0x7e {
			return CredentialHash{}, errors.New("credential hash encoding is invalid")
		}
	}
	return CredentialHash{encoded: encoded}, nil
}

// Encoded 返回 repository/verifier 必需的自描述 hash；返回值与原实例共享敏感内容，调用方不得记录或公开。
func (hash CredentialHash) Encoded() string { return hash.encoded }

// Valid 报告 hash 是否经过非零安全边界构造；它不代表具体算法或成本参数已经通过 adapter 验证。
func (hash CredentialHash) Valid() bool { return hash.encoded != "" }

// String 返回固定占位符，避免默认格式化泄漏完整 hash、salt 或参数。
func (CredentialHash) String() string { return credentialPlaceholder }

// GoString 防止 `%#v` 展开私有 encoded 字段。
func (CredentialHash) GoString() string { return credentialPlaceholder }

// LogValue 让 slog 只记录固定凭据占位符。
func (CredentialHash) LogValue() slog.Value { return slog.StringValue(credentialPlaceholder) }
