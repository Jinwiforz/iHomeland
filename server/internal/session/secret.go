package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

const (
	// tokenSecretBytes 固定 256-bit entropy，抵抗长期 bearer credential 离线猜测。
	tokenSecretBytes = 32
	// secretPlaceholder 是所有默认 credential 输出共享的固定脱敏值。
	secretPlaceholder = "[REDACTED]"
)

// SecretKind 区分不能互换使用的 opaque credential。
type SecretKind uint8

const (
	// SecretKindUnspecified 是拒绝解析和生成的零值。
	SecretKindUnspecified SecretKind = iota
	// SecretKindAccess 用于短期 HTTPS bearer authentication。
	SecretKindAccess
	// SecretKindRefresh 用于原子 token pair rotation。
	SecretKindRefresh
)

// prefix 返回凭据类型的稳定公开前缀，避免错类型凭据进入 store lookup。
func (kind SecretKind) prefix() (string, error) {
	switch kind {
	case SecretKindAccess:
		return "ih_at_", nil
	case SecretKindRefresh:
		return "ih_rt_", nil
	default:
		return "", errors.New("secret kind is unsupported")
	}
}

// SecretGenerator 填充高熵字节；实现必须保证全部 buffer 被覆盖或返回错误。
type SecretGenerator interface {
	// Fill 使用不可预测字节覆盖 buffer，不得复用先前输出。
	Fill(buffer []byte) error
}

// CryptoSecretGenerator 使用操作系统 CSPRNG 生成生产凭据材料。
type CryptoSecretGenerator struct{}

// Fill 调用 crypto/rand，并在熵源失败时不返回部分 credential。
func (CryptoSecretGenerator) Fill(buffer []byte) error {
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Errorf("generate credential entropy: %w", err)
	}
	return nil
}

// Secret 持有只在签发和入站认证边界短暂存在的 opaque 明文。
//
// String、GoString 与 slog 始终脱敏；只有 Reveal 显式返回传输值。
type Secret struct {
	// kind 防止 access、refresh 和 ticket 被交叉使用。
	kind SecretKind
	// raw 是带类型前缀的完整 bearer credential，不得持久化或记录。
	raw string
}

// NewSecret 生成固定 256-bit entropy 并编码为带类型前缀的 base64url credential。
func NewSecret(kind SecretKind, generator SecretGenerator) (Secret, error) {
	prefix, err := kind.prefix()
	if err != nil {
		return Secret{}, err
	}
	if generator == nil {
		return Secret{}, errors.New("secret generator is required")
	}
	material := make([]byte, tokenSecretBytes)
	if err := generator.Fill(material); err != nil {
		return Secret{}, err
	}
	return Secret{kind: kind, raw: prefix + base64.RawURLEncoding.EncodeToString(material)}, nil
}

// ParseSecret 严格校验前缀、base64url canonical form 和 entropy 长度，不在错误中回显输入。
func ParseSecret(kind SecretKind, raw string) (Secret, error) {
	prefix, err := kind.prefix()
	if err != nil {
		return Secret{}, err
	}
	if !strings.HasPrefix(raw, prefix) {
		return Secret{}, errors.New("credential type is invalid")
	}
	encoded := strings.TrimPrefix(raw, prefix)
	material, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(material) != tokenSecretBytes || base64.RawURLEncoding.EncodeToString(material) != encoded {
		return Secret{}, errors.New("credential encoding is invalid")
	}
	return Secret{kind: kind, raw: raw}, nil
}

// Kind 返回 credential 类型，不暴露明文。
func (secret Secret) Kind() SecretKind { return secret.kind }

// Reveal 显式返回传输所需明文；调用方不得持久化、记录或用作 metrics label。
//
// 返回值是无法主动清零的 Go string，调用方应只在响应编码或认证解析的最短作用域
// 持有它，不能缓存到 application state、context value 或异步日志字段。
func (secret Secret) Reveal() string { return secret.raw }

// Digest 计算用于 store lookup 的固定长度不可逆摘要。
func (secret Secret) Digest() Digest { return Digest{value: sha256.Sum256([]byte(secret.raw))} }

// String 实现安全默认格式化，避免 `%v` 或错误拼接泄漏 credential。
func (Secret) String() string { return secretPlaceholder }

// GoString 实现安全 Go-syntax 格式化，避免 `%#v` 绕过 String。
func (Secret) GoString() string { return secretPlaceholder }

// LogValue 让 slog 只记录固定占位符，不展开私有字段。
func (Secret) LogValue() slog.Value { return slog.StringValue(secretPlaceholder) }

// Digest 是可比较的 SHA-256 credential lookup key。
//
// 默认格式化只显示短安全摘要；Bytes 是 storage adapter 唯一取得全值的显式边界。
type Digest struct {
	// value 保存固定 32-byte SHA-256 输出。
	value [sha256.Size]byte
}

// Bytes 返回摘要值副本，调用方不得把完整结果写入日志或 metrics。
func (digest Digest) Bytes() [sha256.Size]byte { return digest.value }

// Summary 返回用于受控关联的前 64-bit 十六进制摘要，不可用于 store lookup。
//
// 截断值存在碰撞可能，只能辅助单次诊断，不能参与认证、去重、撤销或审计判定。
func (digest Digest) Summary() string { return hex.EncodeToString(digest.value[:8]) }

// String 只返回安全摘要，避免默认格式化暴露完整 store key。
func (digest Digest) String() string { return digest.Summary() }

// GoString 与 String 保持相同脱敏边界。
func (digest Digest) GoString() string { return digest.Summary() }

// LogValue 让 slog 记录安全摘要而不是完整 digest。
func (digest Digest) LogValue() slog.Value { return slog.StringValue(digest.Summary()) }

// Valid 报告 digest 是否不是未初始化零值。
func (digest Digest) Valid() bool { return digest.value != [sha256.Size]byte{} }
