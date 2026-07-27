package battleticket

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/simulationcontrol"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
)

const (
	// ticketIDPrefix 防止 BattleTicket identity 与其他 credential namespace 互换。
	ticketIDPrefix = "btk1_"
	// ticketSecretPrefix 防止 BattleTicket secret 与其他 bearer credential 互换。
	ticketSecretPrefix = "bts1_"
	// secretMaterialBytes 固定 256-bit ticket secret 与 proof key。
	secretMaterialBytes = sha256.Size
	// ticketIDMaterialBytes 与 battle wire ClientHello 的 opaque identifier 宽度一致。
	ticketIDMaterialBytes = 16
	// maximumIssueIDBytes 限制幂等 identity 对 store key 与 fingerprint 的影响。
	maximumIssueIDBytes = 128
	// qualifiedActorCapacity 与已资格 SimulationInstance hard cap 一致。
	qualifiedActorCapacity = 8
	// redactedValue 是本包 secret、binding 和内部 identity 的唯一默认格式化结果。
	redactedValue = "[REDACTED_BATTLE_TICKET]"
)

// dnsHostPattern 限制 advertised host 为无 scheme、path 与空 label 的 ASCII DNS name。
var dnsHostPattern = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)(?:\.(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?))*$`)

// Role 是 BattleTicket 从权威 world/visit policy 派生的封闭角色。
type Role uint8

const (
	// RoleUnspecified 是禁止进入 binding 的零值。
	RoleUnspecified Role = iota
	// RoleOwner 表示认证玩家进入自己的 current PersonalWorld battle target。
	RoleOwner
	// RoleVisitor 表示认证玩家以 current VisitSession membership 进入 target。
	RoleVisitor
)

// String 返回 storage enum 与低基数诊断使用的稳定名称。
func (role Role) String() string {
	if role == RoleOwner {
		return "owner"
	}
	if role == RoleVisitor {
		return "visitor"
	}
	return "unspecified"
}

// Valid 报告 role 是否属于可签发集合。
func (role Role) Valid() bool { return role == RoleOwner || role == RoleVisitor }

// ParseRole 从严格 storage enum 恢复角色。
func ParseRole(value string) (Role, error) {
	switch value {
	case "owner":
		return RoleOwner, nil
	case "visitor":
		return RoleVisitor, nil
	default:
		return RoleUnspecified, errors.New("battle ticket role is invalid")
	}
}

// IssueID 是 application 从 operation、AuthContext lineage 与 Idempotency-Key 派生的稳定 identity。
type IssueID struct {
	// value 保存已验证的内部 identity，不保存原始 Idempotency-Key。
	value string
}

// NewIssueID 校验有界安全 ASCII；原始 HTTP key 不得直接传入。
func NewIssueID(value string) (IssueID, error) {
	if len(value) < 16 || len(value) > maximumIssueIDBytes || !safeIdentity(value) {
		return IssueID{}, errors.New("battle ticket issue identity is invalid")
	}
	return IssueID{value: value}, nil
}

// Value 返回 store key/fingerprint 使用的稳定 identity。
func (id IssueID) Value() string { return id.value }

// Valid 报告 identity 是否经过完整构造。
func (id IssueID) Valid() bool { return id.value != "" }

// String 防止幂等 identity 进入普通日志。
func (IssueID) String() string { return redactedValue }

// GoString 防止 `%#v` 展开幂等 identity。
func (IssueID) GoString() string { return redactedValue }

// TicketID 是 UDP ClientHello 可以发送的非秘密 opaque lookup identity。
type TicketID struct {
	// value 保存 versioned base64url identity。
	value string
}

// ParseTicketID 从 HTTP/store/control 边界严格恢复 identity。
func ParseTicketID(value string) (TicketID, error) {
	if !strings.HasPrefix(value, ticketIDPrefix) {
		return TicketID{}, errors.New("battle ticket identity version is invalid")
	}
	material, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, ticketIDPrefix))
	if err != nil || len(material) != ticketIDMaterialBytes ||
		ticketIDPrefix+base64.RawURLEncoding.EncodeToString(material) != value {
		return TicketID{}, errors.New("battle ticket identity encoding is invalid")
	}
	return TicketID{value: value}, nil
}

// Value 返回 UDP/control/store 边界使用的 raw identity。
func (id TicketID) Value() string { return id.value }

// Bytes 返回 proof key public salt 使用的固定长度 ticket identity 副本。
//
// 非法零值返回全零数组；安全派生入口仍会单独验证 identity，调用方不得以全零结果
// 代替构造校验。
func (id TicketID) Bytes() [ticketIDMaterialBytes]byte {
	var fixed [ticketIDMaterialBytes]byte
	if !strings.HasPrefix(id.value, ticketIDPrefix) {
		return fixed
	}
	material, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(id.value, ticketIDPrefix))
	if err != nil || len(material) != ticketIDMaterialBytes {
		return fixed
	}
	copy(fixed[:], material)
	return fixed
}

// Valid 报告 identity 是否符合 canonical encoding。
func (id TicketID) Valid() bool {
	_, err := ParseTicketID(id.value)
	return err == nil
}

// String 防止普通日志把 ticket lookup identity 当作公开 correlation。
func (TicketID) String() string { return redactedValue }

// GoString 防止 `%#v` 展开 ticket identity。
func (TicketID) GoString() string { return redactedValue }

// TicketSecret 是只经 HTTPS 单次交付给 handshake owner 的 256-bit credential。
type TicketSecret struct {
	// material 保存固定长度 secret，不保存可解析 claims。
	material [secretMaterialBytes]byte
}

// ParseTicketSecret 从 versioned base64url 文本严格恢复 secret。
func ParseTicketSecret(value string) (TicketSecret, error) {
	if !strings.HasPrefix(value, ticketSecretPrefix) {
		return TicketSecret{}, errors.New("battle ticket secret version is invalid")
	}
	material, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, ticketSecretPrefix))
	if err != nil || len(material) != secretMaterialBytes ||
		ticketSecretPrefix+base64.RawURLEncoding.EncodeToString(material) != value {
		return TicketSecret{}, errors.New("battle ticket secret encoding is invalid")
	}
	var fixed [secretMaterialBytes]byte
	copy(fixed[:], material)
	return TicketSecret{material: fixed}, nil
}

// Value 返回 HTTPS/handshake 边界使用的 versioned base64url credential。
func (secret TicketSecret) Value() string {
	if !secret.Valid() {
		return ""
	}
	return ticketSecretPrefix + base64.RawURLEncoding.EncodeToString(secret.material[:])
}

// Bytes 返回派生 proof key 所需的固定长度副本；调用方必须限制生命周期且不得记录。
func (secret TicketSecret) Bytes() [secretMaterialBytes]byte { return secret.material }

// Valid 报告 secret 是否非零且可 canonical 编码。
func (secret TicketSecret) Valid() bool { return secret.material != [secretMaterialBytes]byte{} }

// Digest 返回 store 只允许保存的 SHA-256 lookup digest。
func (secret TicketSecret) Digest() Digest {
	return Digest{value: sha256.Sum256(secret.material[:])}
}

// String 返回固定占位符，避免 `%v` 泄漏 credential。
func (TicketSecret) String() string { return redactedValue }

// GoString 防止 `%#v` 展开 secret bytes。
func (TicketSecret) GoString() string { return redactedValue }

// LogValue 让 slog 只记录固定占位符。
func (TicketSecret) LogValue() slog.Value { return slog.StringValue(redactedValue) }

// ProofKey 是只经 private control 安装到 exact child 的 256-bit transcript authentication key。
type ProofKey struct {
	// material 保存固定长度派生结果。
	material [secretMaterialBytes]byte
}

// NewProofKey 从 private control hydration 的完整 bytes 构造 proof key。
func NewProofKey(material [secretMaterialBytes]byte) (ProofKey, error) {
	if material == [secretMaterialBytes]byte{} {
		return ProofKey{}, errors.New("battle ticket proof key is empty")
	}
	return ProofKey{material: material}, nil
}

// Bytes 返回 private control adapter 所需的固定长度副本。
func (key ProofKey) Bytes() [secretMaterialBytes]byte { return key.material }

// Valid 报告 proof key 是否非零。
func (key ProofKey) Valid() bool { return key.material != [secretMaterialBytes]byte{} }

// Digest 返回 Redis 只允许保存的 proof key SHA-256 校验摘要。
func (key ProofKey) Digest() Digest {
	return Digest{value: sha256.Sum256(key.material[:])}
}

// String 返回固定占位符，避免 `%v` 泄漏 proof key。
func (ProofKey) String() string { return redactedValue }

// GoString 防止 `%#v` 展开 proof key。
func (ProofKey) GoString() string { return redactedValue }

// LogValue 让 slog 只记录固定占位符。
func (ProofKey) LogValue() slog.Value { return slog.StringValue(redactedValue) }

// Digest 是 credential、binding 或 source identity 的固定 SHA-256。
type Digest struct {
	// value 保存固定长度 digest。
	value [sha256.Size]byte
}

// NewDigest 从 storage hydration 的完整 bytes 恢复 digest。
func NewDigest(value [sha256.Size]byte) (Digest, error) {
	if value == [sha256.Size]byte{} {
		return Digest{}, errors.New("battle ticket digest is empty")
	}
	return Digest{value: value}, nil
}

// ParseDigestHex 从严格 lowercase hex 恢复 digest。
func ParseDigestHex(value string) (Digest, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size || strings.ToLower(value) != value {
		return Digest{}, errors.New("battle ticket digest encoding is invalid")
	}
	var fixed [sha256.Size]byte
	copy(fixed[:], decoded)
	return NewDigest(fixed)
}

// Bytes 返回固定长度副本。
func (digest Digest) Bytes() [sha256.Size]byte { return digest.value }

// Hex 返回 store/control 精确比较使用的 lowercase hex。
func (digest Digest) Hex() string { return hex.EncodeToString(digest.value[:]) }

// Valid 报告 digest 是否非零。
func (digest Digest) Valid() bool { return digest.value != [sha256.Size]byte{} }

// Equal 使用完整固定长度值比较 digest。
func (digest Digest) Equal(other Digest) bool { return digest.value == other.value }

// String 防止 binding/secret digest 进入普通日志。
func (Digest) String() string { return redactedValue }

// GoString 防止 `%#v` 展开 digest bytes。
func (Digest) GoString() string { return redactedValue }

// ActorSlot 是 exact SimulationInstance 的零基 actor index。
type ActorSlot struct {
	// index 是 0 到 7 的固定 slot。
	index uint8
	// initialized 区分合法 slot 0 与未构造零值。
	initialized bool
}

// NewActorSlot 校验 B0.3 qualified 8-actor hard cap。
func NewActorSlot(index uint8) (ActorSlot, error) {
	if index >= qualifiedActorCapacity {
		return ActorSlot{}, errors.New("battle actor slot exceeds qualified capacity")
	}
	return ActorSlot{index: index, initialized: true}, nil
}

// Index 返回 private control 与 C++ instance 使用的零基 slot。
func (slot ActorSlot) Index() uint8 { return slot.index }

// Valid 报告 slot 是否来自构造路径并位于 0 到 7。
func (slot ActorSlot) Valid() bool {
	return slot.initialized && slot.index < qualifiedActorCapacity
}

// Endpoint 是 trusted provider 返回的 client-visible UDP 目标。
type Endpoint struct {
	// host 是规范化 lowercase IP 或 DNS name。
	host string
	// port 是显式 1 到 65535 UDP port。
	port uint16
}

// NewEndpoint 校验 advertised endpoint；它不接受 scheme、path、通配 host 或 port 0。
func NewEndpoint(host string, port uint16) (Endpoint, error) {
	normalized := strings.ToLower(strings.TrimSuffix(host, "."))
	if port == 0 || !validEndpointHost(normalized) {
		return Endpoint{}, errors.New("battle endpoint is invalid")
	}
	return Endpoint{host: normalized, port: port}, nil
}

// Host 返回 client-visible normalized host。
func (endpoint Endpoint) Host() string { return endpoint.host }

// Port 返回 client-visible UDP port。
func (endpoint Endpoint) Port() uint16 { return endpoint.port }

// Valid 报告 endpoint 是否经过完整构造。
func (endpoint Endpoint) Valid() bool {
	return endpoint.port != 0 && validEndpointHost(endpoint.host)
}

// Equal 比较完整 advertised endpoint。
func (endpoint Endpoint) Equal(other Endpoint) bool { return endpoint == other }

// WireSuite 固定 BattleTicket 允许协商的 wire 与算法 identity。
type WireSuite struct {
	// WireVersion 选择 binary layout 代际。
	WireVersion uint8
	// KeyAgreement 固定 X25519。
	KeyAgreement string
	// KDF 固定 HKDF-SHA-256。
	KDF string
	// AEAD 固定 ChaCha20-Poly1305。
	AEAD string
}

// CurrentWireSuite 返回本 change 唯一允许的 wire/crypto suite。
func CurrentWireSuite() WireSuite {
	return WireSuite{WireVersion: 1, KeyAgreement: "X25519", KDF: "HKDF-SHA-256", AEAD: "ChaCha20-Poly1305"}
}

// Valid 报告 suite 是否精确匹配受评审算法集合。
func (suite WireSuite) Valid() bool { return suite == CurrentWireSuite() }

// Facts 是 application 从全部权威 owner 收集的不可变 BattleTicket 候选事实。
// Public fields 只用于按值交给 NewBindingMaterial；构造后的 Binding 不暴露可变引用。
type Facts struct {
	// PlayerID 来自 HTTPS AuthContext。
	PlayerID account.PlayerID
	// SessionID 绑定认证 lineage。
	SessionID session.SessionID
	// SessionEpoch 是 logout/refresh/ban 使用的失效屏障。
	SessionEpoch session.Epoch
	// Role 来自 PersonalWorld owner 或 VisitSession membership policy。
	Role Role
	// WorldID 是 exact PersonalWorld target。
	WorldID personalworld.PersonalWorldID
	// VisitSessionID 只在 Visitor role 存在。
	VisitSessionID visitsession.VisitSessionID
	// Assignment 是 current placement 的完整 stamp。
	Assignment placement.AssignmentStamp
	// AssignmentFingerprint 是 SimulationTarget 对同一 stamp 的 canonical digest。
	AssignmentFingerprint simulationcontrol.Digest
	// RuntimeNodeID 是 placement 选择的受信 runtime node。
	RuntimeNodeID placement.RuntimeNodeID
	// SimulationNodeID 绑定不可复活 C++ child incarnation。
	SimulationNodeID simulationcontrol.SimulationNodeID
	// SimulationInstanceID 绑定不可复活 worker timeline。
	SimulationInstanceID simulationcontrol.SimulationInstanceID
	// MappingGeneration 绑定 InputTick mapping timeline。
	MappingGeneration uint64
	// TargetRevision 在 replacement 后单调变化。
	TargetRevision uint64
	// ModelIdentity 绑定冻结 battle model manifest。
	ModelIdentity simulationcontrol.Digest
	// ProfileIdentity 绑定冻结 network profile manifest。
	ProfileIdentity simulationcontrol.Digest
	// ConfigIdentity 绑定 exact runtime config。
	ConfigIdentity simulationcontrol.Digest
	// WireIdentity 绑定 battle wire corpus manifest。
	WireIdentity Digest
	// ActorSlot 是 exact instance 预留的零基 slot。
	ActorSlot ActorSlot
	// Endpoint 来自 trusted BattleEndpointProvider。
	Endpoint Endpoint
	// IssueID 是不含原始 HTTP key 的 deterministic issuance identity。
	IssueID IssueID
	// IssuedAt 是 UTC 微秒签发时刻。
	IssuedAt time.Time
	// ExpiresAt 是等于即失效的 UTC 微秒 deadline。
	ExpiresAt time.Time
}

// Binding 是 ticket ID 与全部权威 target/session 事实的不可变绑定。
type Binding struct {
	// ticketID 是本 binding 唯一允许消费的 lookup identity。
	ticketID TicketID
	// facts 保存不含引用字段的权威事实副本。
	facts Facts
}

// Valid 报告候选事实的 role、target、identity、endpoint 与时间组合是否完整。
func (facts Facts) Valid() bool {
	if !facts.PlayerID.Valid() || !facts.SessionID.Valid() ||
		!facts.SessionEpoch.Valid() || !facts.Role.Valid() || !facts.WorldID.Valid() ||
		!facts.Assignment.Valid() || !facts.AssignmentFingerprint.Valid() ||
		!facts.RuntimeNodeID.Valid() || facts.Assignment.NodeID() != facts.RuntimeNodeID ||
		facts.Assignment.WorldID() != facts.WorldID || !facts.SimulationNodeID.Valid() ||
		!facts.SimulationInstanceID.Valid() || facts.MappingGeneration == 0 ||
		facts.TargetRevision == 0 || !facts.ModelIdentity.Valid() ||
		!facts.ProfileIdentity.Valid() || !facts.ConfigIdentity.Valid() ||
		!facts.WireIdentity.Valid() || !facts.ActorSlot.Valid() || !facts.Endpoint.Valid() ||
		!facts.IssueID.Valid() || facts.IssuedAt.IsZero() || !facts.ExpiresAt.After(facts.IssuedAt) {
		return false
	}
	if facts.Role == RoleOwner {
		return !facts.VisitSessionID.Valid()
	}
	return facts.VisitSessionID.Valid()
}

// newBinding 只允许 deterministic derivation 路径加入 ticket ID。
func newBinding(ticketID TicketID, facts Facts) (Binding, error) {
	facts.IssuedAt = canonicalTime(facts.IssuedAt)
	facts.ExpiresAt = canonicalTime(facts.ExpiresAt)
	binding := Binding{ticketID: ticketID, facts: facts}
	if !binding.Valid() {
		return Binding{}, errors.New("battle ticket binding is incomplete")
	}
	return binding, nil
}

// HydrateBinding 从严格 storage/control fields 恢复完整 binding。
// Adapter 必须在调用前验证 schema/TTL；该 constructor 不修复或补全缺失事实。
func HydrateBinding(ticketID TicketID, facts Facts) (Binding, error) {
	return newBinding(ticketID, facts)
}

// TicketID 返回 UDP lookup 使用的 opaque identity。
func (binding Binding) TicketID() TicketID { return binding.ticketID }

// Facts 返回不可变值副本。
func (binding Binding) Facts() Facts { return binding.facts }

// Fingerprint 返回全部 binding fields 的 canonical SHA-256。
func (binding Binding) Fingerprint() Digest {
	if !binding.Valid() {
		return Digest{}
	}
	return fingerprintBinding(binding)
}

// Valid 报告 binding 的 role、target、identity、endpoint 与时间组合是否完整。
func (binding Binding) Valid() bool {
	return binding.ticketID.Valid() && binding.facts.Valid()
}

// Equal 比较 ticket ID 与全部权威事实。
func (binding Binding) Equal(other Binding) bool {
	return binding.ticketID == other.ticketID && factsEqual(binding.facts, other.facts, true)
}

// String 防止默认格式化泄漏内部 binding。
func (Binding) String() string { return redactedValue }

// GoString 防止 `%#v` 展开 private binding。
func (Binding) GoString() string { return redactedValue }

// LogValue 让 slog 只记录固定占位符。
func (Binding) LogValue() slog.Value { return slog.StringValue(redactedValue) }

// Material 是 issuer 向 store、private control 与 HTTPS 边界分发的独立值集合。
type Material struct {
	// binding 保存完整不可变授权事实。
	binding Binding
	// fingerprint 是 store/install 精确比较使用的完整 binding digest。
	fingerprint Digest
	// secret 只允许 HTTPS handshake owner 单次取得。
	secret TicketSecret
	// proofKey 只允许 private control adapter 安装到 exact child。
	proofKey ProofKey
}

// Binding 返回完整授权事实副本。
func (material Material) Binding() Binding { return material.binding }

// Fingerprint 返回 store/control 精确比较使用的 digest。
func (material Material) Fingerprint() Digest { return material.fingerprint }

// Secret 返回 HTTPS response owner 使用的 secret 值副本。
func (material Material) Secret() TicketSecret { return material.secret }

// ProofKey 返回 private control adapter 使用的 key 值副本。
func (material Material) ProofKey() ProofKey { return material.proofKey }

// Valid 报告 material 是否由完整 deterministic derivation 产生。
func (material Material) Valid() bool {
	return material.binding.Valid() && material.fingerprint.Valid() &&
		material.secret.Valid() && material.proofKey.Valid()
}

// String 防止默认格式化泄漏 material。
func (Material) String() string { return redactedValue }

// GoString 防止 `%#v` 展开 secret 与 proof key。
func (Material) GoString() string { return redactedValue }

// LogValue 让 slog 只记录固定占位符。
func (Material) LogValue() slog.Value { return slog.StringValue(redactedValue) }

// safeIdentity 限制内部 identity 使用跨 Redis/control 稳定的 ASCII 子集。
func safeIdentity(value string) bool {
	for index := range len(value) {
		character := value[index]
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '-' && character != '_' && character != '.' && character != ':' {
			return false
		}
	}
	return true
}

// validEndpointHost 接受规范 IP 或 lowercase DNS name，拒绝 unspecified/multicast host。
func validEndpointHost(host string) bool {
	if address, err := netip.ParseAddr(host); err == nil {
		return !address.IsUnspecified() && !address.IsMulticast()
	}
	return len(host) <= 253 && dnsHostPattern.MatchString(host)
}

// canonicalTime 移除单调分量并统一为 storage/control 使用的 UTC 微秒。
func canonicalTime(value time.Time) time.Time { return value.UTC().Truncate(time.Microsecond) }
