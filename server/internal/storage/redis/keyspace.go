package redis

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// maximumSegmentBytes 限制单个 identity 对 key 内存与网络预算的影响。
	maximumSegmentBytes = 128
	// maximumKeyBytes 保持低于 Redis protocol key 上限并给 namespace 留出预算。
	maximumKeyBytes = 512
)

// namespaceSegmentPattern 只允许稳定小写 environment/owner/kind/metrics metadata。
var namespaceSegmentPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// identitySegmentPattern 只接受 owner 已规范化的 ASCII ID/digest，不允许 raw username 或 Unicode 同形异义。
var identitySegmentPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

// TTLPolicy 描述 key 是否必须过期或由 owner 主动清理。
type TTLPolicy string

const (
	// TTLRequired 要求每次写入都携带正 expiry。
	TTLRequired TTLPolicy = "required"
	// TTLOwnerCleanup 只允许定义了明确 owner cleanup 的 key 不设置 TTL。
	TTLOwnerCleanup TTLPolicy = "owner_cleanup"
)

// Definition 是一个 owner-specific Redis key 的不可变治理 metadata。
type Definition struct {
	// Name 是日志和 metrics 使用的稳定低基数名称。
	Name string
	// Owner 是负责 value、mutation、恢复和 cleanup 的业务模块。
	Owner string
	// Kind 是真实 key namespace 中的资源类型段。
	Kind string
	// Purpose 使用中文短名词概括运行态用途；完整字段字典由 docs/redis-keys.md 管理。
	Purpose string
	// TTLPolicy 声明 expiry 或主动 cleanup 契约。
	TTLPolicy TTLPolicy
	// SchemaVersion 是 owner codec 必须验证的正整数版本。
	SchemaVersion uint16
	// MaxEncodedBytes 是 value 编码后的硬上限。
	MaxEncodedBytes int
	// Recovery 描述 Redis flush/restart 后由 owner 执行的恢复策略。
	Recovery string
	// Cleanup 描述 expiry 或 owner 主动清理责任。
	Cleanup string
	// Failure 描述 corruption/unknown version 时的 fail-closed 行为。
	Failure string
	// MetricsName 是不含 identity 的低基数指标名。
	MetricsName string
}

// Registry 保存按 name 排序的不可变 definition 快照。
type Registry struct {
	// definitions 保存按 name 索引的值副本，构造后不再修改。
	definitions map[string]Definition
	// names 保存稳定排序快照，返回时再次复制避免调用方篡改。
	names []string
}

// NewRegistry 验证 owner、TTL、schema、大小和重复项后复制 definitions。
func NewRegistry(definitions []Definition) (*Registry, error) {
	registry := &Registry{definitions: make(map[string]Definition, len(definitions))}
	for _, definition := range definitions {
		if err := validateDefinition(definition); err != nil {
			return nil, fmt.Errorf("redis definition %q: %w", definition.Name, err)
		}
		if _, exists := registry.definitions[definition.Name]; exists {
			return nil, fmt.Errorf("duplicate redis definition %q", definition.Name)
		}
		registry.definitions[definition.Name] = definition
		registry.names = append(registry.names, definition.Name)
	}
	sort.Strings(registry.names)
	return registry, nil
}

// Lookup 返回 definition 的值副本，调用方不能修改 registry。
func (registry *Registry) Lookup(name string) (Definition, bool) {
	definition, exists := registry.definitions[name]
	return definition, exists
}

// Names 返回稳定排序的 definition name 副本。
func (registry *Registry) Names() []string { return append([]string(nil), registry.names...) }

// Key 保存真实 Redis key，但默认格式化只返回不含 identity 的 pattern。
type Key struct {
	// raw 只允许显式 Value 调用发送给 Redis，不参与默认格式化。
	raw string
	// pattern 省略全部 identity，用于安全日志与诊断。
	pattern string
}

// Keyspace 将一个已验证 environment 绑定到不可变 Registry，禁止调用方绕过登记构造 key。
type Keyspace struct {
	// environment 是真实 key 的第二段，用于隔离部署环境。
	environment string
	// registry 是 name -> owner/kind metadata 的唯一可信来源。
	registry *Registry
}

// NewKeyspace 验证 environment 并要求显式 registry，不创建 Redis client 或其他资源。
func NewKeyspace(environment string, registry *Registry) (*Keyspace, error) {
	if !namespaceSegmentPattern.MatchString(environment) {
		return nil, errors.New("redis environment segment has invalid format")
	}
	if registry == nil {
		return nil, errors.New("redis keyspace requires a registry")
	}
	return &Keyspace{environment: environment, registry: registry}, nil
}

// Build 按已登记 definition name 构造 `ih:<env>:<owner>:<kind>:<identity...>`，不使用 Cluster hash tag。
func (keyspace *Keyspace) Build(definitionName string, identities ...string) (Key, error) {
	if keyspace == nil || keyspace.registry == nil {
		return Key{}, errors.New("redis keyspace is not initialized")
	}
	definition, exists := keyspace.registry.Lookup(definitionName)
	if !exists {
		return Key{}, errors.New("redis key definition is not registered")
	}
	if err := validateDefinition(definition); err != nil {
		return Key{}, err
	}
	if len(identities) == 0 {
		return Key{}, errors.New("redis key requires at least one identity segment")
	}
	segments := []string{"ih", keyspace.environment, definition.Owner, definition.Kind}
	for _, identity := range identities {
		if err := validateIdentity(identity); err != nil {
			return Key{}, err
		}
		segments = append(segments, identity)
	}
	raw := strings.Join(segments, ":")
	if len(raw) > maximumKeyBytes {
		return Key{}, errors.New("redis key exceeds size limit")
	}
	return Key{raw: raw, pattern: strings.Join(segments[:4], ":") + ":<identity...>"}, nil
}

// String 返回不包含 identity 的 key pattern，适合默认日志。
func (key Key) String() string { return key.pattern }

// GoString 阻止 %#v 绕过 String 展开完整 key。
func (key Key) GoString() string { return key.pattern }

// LogValue 为 slog 提供不包含 identity 的 key pattern。
func (key Key) LogValue() slog.Value { return slog.StringValue(key.pattern) }

// MarshalText 阻止通用文本编码器序列化完整 key。
func (key Key) MarshalText() ([]byte, error) { return []byte(key.pattern), nil }

// Value 返回发送给 Redis client 的完整 key；调用方不得写入日志或 metrics。
func (key Key) Value() string { return key.raw }

// DigestIdentity 返回 SHA-256 前 128 bit 的确定性小写十六进制 key segment。
//
// 该结果只用于避免 raw identity 直接进入 namespace，不是 secret、MAC 或认证凭据；无盐
// digest 仍允许对低熵输入离线枚举，owner 必须优先传入高熵 ID/token 或独立安全标识。
func DigestIdentity(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:16])
}

// TTLUntil 根据 absolute expiry 和受信 now 返回正 TTL，过期或相等时 fail closed。
func TTLUntil(expiresAt time.Time, now time.Time) (time.Duration, error) {
	ttl := expiresAt.Sub(now)
	if ttl <= 0 {
		return 0, errors.New("redis expiry must be after current time")
	}
	return ttl, nil
}

// ValidateEncodedSize 在 owner decode 前验证 schema version 和累计 encoded 字节数。
//
// Hash owner 应传入全部 field name 与 value 的累计字节数，不能只统计其中的
// JSON payload。该函数不分配与 value 同大小的临时 buffer。
func ValidateEncodedSize(definition Definition, schemaVersion uint16, encodedBytes int) error {
	if schemaVersion != definition.SchemaVersion {
		return errors.New("redis value schema version is unknown")
	}
	if encodedBytes <= 0 {
		return errors.New("redis encoded value is empty or corrupt")
	}
	if encodedBytes > definition.MaxEncodedBytes {
		return errors.New("redis encoded value exceeds definition size")
	}
	return nil
}

// ValidateEncodedValue 验证已物化的单一 encoded value 的 schema version 与字节数。
func ValidateEncodedValue(definition Definition, schemaVersion uint16, encoded []byte) error {
	return ValidateEncodedSize(definition, schemaVersion, len(encoded))
}

// validateDefinition 在 registry 与 builder 两个入口统一验证 owner metadata。
func validateDefinition(definition Definition) error {
	if !namespaceSegmentPattern.MatchString(definition.Name) || !namespaceSegmentPattern.MatchString(definition.Owner) || !namespaceSegmentPattern.MatchString(definition.Kind) || !namespaceSegmentPattern.MatchString(definition.MetricsName) {
		return errors.New("name, owner, kind, and metricsName must be stable lowercase segments")
	}
	if definition.Purpose == "" || definition.Recovery == "" || definition.Cleanup == "" || definition.Failure == "" {
		return errors.New("purpose, recovery, cleanup, and failure are required")
	}
	if definition.TTLPolicy != TTLRequired && definition.TTLPolicy != TTLOwnerCleanup {
		return errors.New("ttlPolicy must be required or owner_cleanup")
	}
	if definition.SchemaVersion == 0 {
		return errors.New("schemaVersion must be positive")
	}
	if definition.MaxEncodedBytes < 1 || definition.MaxEncodedBytes > 1024*1024 {
		return errors.New("maxEncodedBytes must be between 1 and 1048576")
	}
	return nil
}

// validateIdentity 拒绝 namespace 分隔符、Cluster hash tag、控制字符和超长 UTF-8。
func validateIdentity(value string) error {
	if value == "" || len(value) > maximumSegmentBytes {
		return errors.New("redis identity segment is empty or oversized")
	}
	if !identitySegmentPattern.MatchString(value) {
		return errors.New("redis identity segment contains forbidden characters")
	}
	return nil
}
