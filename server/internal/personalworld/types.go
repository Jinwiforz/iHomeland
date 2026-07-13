package personalworld

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"log/slog"
	"strings"
)

const (
	// personalWorldIDPrefix 把通用随机材料固定到 PersonalWorld namespace。
	personalWorldIDPrefix = "pworld_"
	// maximumIdentifierBytes 防止异常 generator 放大持久索引和日志关联值。
	maximumIdentifierBytes = 128
	// minimumIdempotencyKeyBytes 拒绝空值和过短占位 key；真正熵值仍由可信调用方负责。
	minimumIdempotencyKeyBytes = 8
	// maximumIdempotencyKeyBytes 限制索引、fingerprint 和失败请求的处理成本。
	maximumIdempotencyKeyBytes = 128
	// redactedIdempotencyKey 是所有默认格式化路径唯一允许输出的占位文本。
	redactedIdempotencyKey = "[REDACTED_IDEMPOTENCY_KEY]"
	// archiveFingerprintDomain 固定已持久化摘要的 operation 与 canonicalization 版本。
	archiveFingerprintDomain = "personalworld.archive.v1"
)

// PersonalWorldID 标识不可由客户端选择的个人持久世界事实。
type PersonalWorldID struct {
	// value 保存带 PersonalWorld namespace 前缀的安全 ASCII identifier。
	value string
}

// NewPersonalWorldID 校验 repository hydration 或服务端生成的世界 identifier。
//
// 该 constructor 只验证 namespace 与安全字符，不把客户端提供的字符串提升为可信 identity；
// 新建 ID 的随机性与不可预测性仍由受信 IDGenerator 保证。
func NewPersonalWorldID(value string) (PersonalWorldID, error) {
	if !strings.HasPrefix(value, personalWorldIDPrefix) || len(value) <= len(personalWorldIDPrefix) || len(value) > maximumIdentifierBytes {
		return PersonalWorldID{}, errors.New("personal world identifier prefix or length is invalid")
	}
	for index := len(personalWorldIDPrefix); index < len(value); index++ {
		if !asciiAlphaNumeric(value[index]) {
			return PersonalWorldID{}, errors.New("personal world identifier contains unsupported characters")
		}
	}
	return PersonalWorldID{value: value}, nil
}

// String 返回可用于持久索引和受控关联日志的世界 identifier。
func (id PersonalWorldID) String() string { return id.value }

// Valid 报告 PersonalWorldID 是否经过完整构造。
func (id PersonalWorldID) Valid() bool { return id.value != "" }

// Revision 是 PersonalWorld 持久 mutation 的乐观并发版本。
type Revision uint64

const (
	// InitialRevision 是新建 PersonalWorld 的第一个持久版本。
	InitialRevision Revision = 1
)

// NewRevision 校验 repository hydration 返回的正 revision。
func NewRevision(value uint64) (Revision, error) {
	if value == 0 {
		return 0, errors.New("personal world revision must be positive")
	}
	return Revision(value), nil
}

// Uint64 返回 storage 与未来协议投影使用的无符号版本值。
func (revision Revision) Uint64() uint64 { return uint64(revision) }

// Valid 报告 revision 是否可以参与 expected revision 比较。
func (revision Revision) Valid() bool { return revision > 0 }

// next 返回成功 mutation 的唯一后继 revision，并拒绝无效值与溢出。
func (revision Revision) next() (Revision, error) {
	if !revision.Valid() || revision == Revision(^uint64(0)) {
		return 0, errors.New("personal world revision cannot advance")
	}
	return revision + 1, nil
}

// IdempotencyKey 标识一次可安全重试的持久 world command。
//
// Key 由可信调用边界生成并持久化，但不具备授权能力。Repository 必须把它与已授权
// actor scope 组成索引，不能让相同文本 key 在不同 Owner 间冲突。默认格式化始终脱敏，
// repository 只能通过 Value 取得精确值；错误与普通日志不得输出该值。
type IdempotencyKey struct {
	// value 保存有界安全 ASCII command identity。
	value string
}

// NewIdempotencyKey 校验调用方生成的稳定 command identity。
//
// Key 仅用于重试收敛，不携带 Owner authorization；允许字符集保持跨日志、数据库和语言一致，
// 原始值只能在 repository 索引边界通过 Value 使用。
func NewIdempotencyKey(value string) (IdempotencyKey, error) {
	if len(value) < minimumIdempotencyKeyBytes || len(value) > maximumIdempotencyKeyBytes {
		return IdempotencyKey{}, errors.New("idempotency key length is invalid")
	}
	for index := 0; index < len(value); index++ {
		if !idempotencyCharacterAllowed(value[index]) {
			return IdempotencyKey{}, errors.New("idempotency key contains unsupported characters")
		}
	}
	return IdempotencyKey{value: value}, nil
}

// Value 返回 repository 唯一索引使用的原始 command identity。
func (key IdempotencyKey) Value() string { return key.value }

// Valid 报告 key 是否经过完整构造。
func (key IdempotencyKey) Valid() bool { return key.value != "" }

// String 防止默认格式化把调用方 command identity 写入日志。
func (IdempotencyKey) String() string { return redactedIdempotencyKey }

// GoString 防止 `%#v` 绕过普通 String 脱敏边界。
func (IdempotencyKey) GoString() string { return redactedIdempotencyKey }

// LogValue 让结构化日志只记录稳定占位值。
func (IdempotencyKey) LogValue() slog.Value { return slog.StringValue(redactedIdempotencyKey) }

// Lifecycle 表达 PersonalWorld 的持久存在性，不表示运行实例状态。
type Lifecycle uint8

const (
	// LifecycleUnspecified 是禁止进入 aggregate 与 repository 的零值。
	LifecycleUnspecified Lifecycle = 0
	// LifecycleActive 表示世界可以接受其领域允许的持久 mutation；数值进入 fingerprint，禁止重排。
	LifecycleActive Lifecycle = 1
	// LifecycleArchived 表示世界已终止且不能恢复或继续 mutation；数值进入 fingerprint，禁止重排。
	LifecycleArchived Lifecycle = 2
)

// String 返回适合指标、storage enum 和诊断的稳定低基数名称。
func (lifecycle Lifecycle) String() string {
	switch lifecycle {
	case LifecycleActive:
		return "active"
	case LifecycleArchived:
		return "archived"
	default:
		return "unspecified"
	}
}

// Valid 报告 lifecycle 是否属于当前封闭持久状态集合。
func (lifecycle Lifecycle) Valid() bool {
	return lifecycle == LifecycleActive || lifecycle == LifecycleArchived
}

// CommandFingerprint 是 repository 用于识别同 key 同命令的固定摘要。
//
// Fingerprint 不承担认证或防篡改；它只在 idempotency transaction 内比较规范字段，
// 避免依赖易变序列化格式或拼接歧义。
type CommandFingerprint struct {
	// digest 保存 archive command 规范字段的 SHA-256 摘要。
	digest [sha256.Size]byte
}

// Valid 报告 fingerprint 是否来自受校验 command，而不是零值结果。
func (fingerprint CommandFingerprint) Valid() bool {
	return fingerprint.digest != [sha256.Size]byte{}
}

// Equal 使用固定大小值比较两个 command fingerprint。
func (fingerprint CommandFingerprint) Equal(other CommandFingerprint) bool {
	return fingerprint.digest == other.digest
}

// String 防止默认格式化把 command 关联摘要扩散到日志。
func (CommandFingerprint) String() string { return "[REDACTED_COMMAND_FINGERPRINT]" }

// GoString 防止 `%#v` 输出 fingerprint 内部 bytes。
func (CommandFingerprint) GoString() string { return "[REDACTED_COMMAND_FINGERPRINT]" }

// LogValue 防止结构化日志 handler 把摘要 bytes 展开为数组。
func (CommandFingerprint) LogValue() slog.Value {
	return slog.StringValue("[REDACTED_COMMAND_FINGERPRINT]")
}

// fingerprintArchiveCommand 对规范字段做长度前缀编码，避免字符串拼接碰撞。
//
// SHA-256 的 hash.Hash Write 按标准库契约不会返回错误，因此这里安全忽略返回值；摘要只用于
// 相等性判定，不替代签名、认证或授权。
func fingerprintArchiveCommand(worldID PersonalWorldID, actorID string, expected Revision, target Lifecycle) CommandFingerprint {
	hasher := sha256.New()
	writeFingerprintField(hasher, archiveFingerprintDomain)
	writeFingerprintField(hasher, worldID.String())
	writeFingerprintField(hasher, actorID)
	var revision [8]byte
	binary.BigEndian.PutUint64(revision[:], expected.Uint64())
	_, _ = hasher.Write(revision[:])
	_, _ = hasher.Write([]byte{byte(target)})
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return CommandFingerprint{digest: digest}
}

// writeFingerprintField 使用无歧义长度前缀写入单个 ASCII command 字段。
func writeFingerprintField(hasher hash.Hash, value string) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	_, _ = hasher.Write(length[:])
	_, _ = hasher.Write([]byte(value))
}

// idempotencyCharacterAllowed 固定跨语言与数据库一致的 command key 字符集合。
func idempotencyCharacterAllowed(value byte) bool {
	return asciiAlphaNumeric(value) || value == '-' || value == '_' || value == '.' || value == ':'
}

// asciiAlphaNumeric 避免 locale 或 Unicode rule 影响持久 identity。
func asciiAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}
