package worldadmission

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
)

const (
	// credentialPrefix 区分未来不可静默复用的 credential 编码版本。
	credentialPrefix = "wad1_"
	// credentialMaterialBytes 是 HMAC-SHA-256 输出的固定长度。
	credentialMaterialBytes = sha256.Size
	// maximumOperationIDBytes 限制幂等 identity 对 Redis key 与 fingerprint 的影响。
	maximumOperationIDBytes = 128
	// admissionPlaceholder 是 credential、binding 与 qualification 的统一安全格式化结果。
	admissionPlaceholder = "[REDACTED_WORLD_ADMISSION]"
)

// Role 是 world admission 授予的封闭角色，而不是客户端声明。
type Role uint8

const (
	// RoleUnspecified 是禁止进入 binding 的零值。
	RoleUnspecified Role = iota
	// RoleOwner 表示认证玩家进入自己的 PersonalWorld。
	RoleOwner
	// RoleVisitor 表示认证玩家进入已有 VisitSession。
	RoleVisitor
)

// String 返回 Redis enum 与低基数诊断使用的稳定名称。
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

// ParseRole 从严格 Redis enum 恢复角色。
func ParseRole(value string) (Role, error) {
	switch value {
	case "owner":
		return RoleOwner, nil
	case "visitor":
		return RoleVisitor, nil
	default:
		return RoleUnspecified, errors.New("world admission role is invalid")
	}
}

// Purpose 是 credential 唯一允许的业务用途。
type Purpose uint8

const (
	// PurposeUnspecified 是禁止进入 binding 的零值。
	PurposeUnspecified Purpose = iota
	// PurposeOwnWorld 表示 Owner 进入自己的 current PersonalWorld。
	PurposeOwnWorld
	// PurposeJoin 表示 Visitor 把 reserved membership 转为 joined。
	PurposeJoin
	// PurposeReconnect 表示 Visitor 恢复 reconnecting membership。
	PurposeReconnect
)

// String 返回 Redis enum 与协议 adapter 使用的稳定名称。
func (purpose Purpose) String() string {
	switch purpose {
	case PurposeOwnWorld:
		return "own_world"
	case PurposeJoin:
		return "join"
	case PurposeReconnect:
		return "reconnect"
	default:
		return "unspecified"
	}
}

// Valid 报告 purpose 是否属于可签发集合。
func (purpose Purpose) Valid() bool {
	return purpose == PurposeOwnWorld || purpose == PurposeJoin || purpose == PurposeReconnect
}

// ParsePurpose 从严格 Redis enum 恢复用途。
func ParsePurpose(value string) (Purpose, error) {
	switch value {
	case "own_world":
		return PurposeOwnWorld, nil
	case "join":
		return PurposeJoin, nil
	case "reconnect":
		return PurposeReconnect, nil
	default:
		return PurposeUnspecified, errors.New("world admission purpose is invalid")
	}
}

// IssueID 是 HTTP application 提供的稳定 issuance 幂等 identity。
type IssueID struct {
	// value 保存已验证的客户端幂等identity，不进入普通日志。
	value string
}

// NewIssueID 校验安全 ASCII identity；调用方必须从已验证 Idempotency-Key 派生。
func NewIssueID(value string) (IssueID, error) {
	if !validOperationID(value) {
		return IssueID{}, errors.New("world admission issue id is invalid")
	}
	return IssueID{value: value}, nil
}

// Value 返回 store fingerprint 使用的稳定 identity；不得写入低基数 label。
func (id IssueID) Value() string { return id.value }

// Valid 报告 identity 是否经过完整构造。
func (id IssueID) Valid() bool { return id.value != "" }

// String 防止客户端幂等 identity 进入普通日志。
func (IssueID) String() string { return admissionPlaceholder }

// GoString 防止 `%#v` 展开私有 identity。
func (IssueID) GoString() string { return admissionPlaceholder }

// ConsumeID 是 TLS/TCP adapter 为一次 verify 尝试分配的稳定 identity。
type ConsumeID struct {
	// value 保存已验证的连接尝试identity，不进入普通日志。
	value string
}

// NewConsumeID 校验安全 ASCII identity；response-loss重试必须复用同一值。
func NewConsumeID(value string) (ConsumeID, error) {
	if !validOperationID(value) {
		return ConsumeID{}, errors.New("world admission consume id is invalid")
	}
	return ConsumeID{value: value}, nil
}

// Value 返回原子 consume fingerprint 使用的稳定 identity。
func (id ConsumeID) Value() string { return id.value }

// Valid 报告 identity 是否经过完整构造。
func (id ConsumeID) Valid() bool { return id.value != "" }

// String 防止连接尝试 identity 进入普通日志。
func (ConsumeID) String() string { return admissionPlaceholder }

// GoString 防止 `%#v` 展开私有 identity。
func (ConsumeID) GoString() string { return admissionPlaceholder }

// Credential 是只由 issuer 创建或协议 adapter 严格解析的 bearer secret。
type Credential struct {
	// value 保存带版本前缀的raw bearer secret。
	value string
}

// ParseCredential 验证版本、字符集与固定 entropy 长度，不解释任何 claims。
func ParseCredential(value string) (Credential, error) {
	if !strings.HasPrefix(value, credentialPrefix) {
		return Credential{}, errors.New("world admission credential version is invalid")
	}
	encoded := strings.TrimPrefix(value, credentialPrefix)
	material, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(material) != credentialMaterialBytes || base64.RawURLEncoding.EncodeToString(material) != encoded {
		return Credential{}, errors.New("world admission credential encoding is invalid")
	}
	return Credential{value: value}, nil
}

// Value 返回协议响应/请求边界所需 raw secret；调用方不得记录、缓存或持久化。
func (credential Credential) Value() string { return credential.value }

// Valid 报告 credential 是否符合固定 opaque 编码。
func (credential Credential) Valid() bool {
	_, err := ParseCredential(credential.value)
	return err == nil
}

// Digest 返回 Redis 索引使用的 SHA-256 摘要。
//
// 摘要不授予访问权限，但仍可关联同一 credential，因此默认格式化继续脱敏，调用方不得把它
// 当作可公开 correlation ID。
func (credential Credential) Digest() Digest {
	return Digest{value: sha256.Sum256([]byte(credential.value))}
}

// String 返回固定占位符，避免 `%v` 泄漏 bearer secret。
func (Credential) String() string { return admissionPlaceholder }

// GoString 防止 `%#v` 展开 raw secret。
func (Credential) GoString() string { return admissionPlaceholder }

// LogValue 让 slog 只记录固定占位符。
func (Credential) LogValue() slog.Value { return slog.StringValue(admissionPlaceholder) }

// Digest 是 credential 或 fingerprint 的固定 SHA-256 值。
type Digest struct {
	// value 保存固定长度SHA-256结果。
	value [sha256.Size]byte
}

// NewDigest 从 storage hydration 的完整 bytes 恢复摘要。
func NewDigest(value [sha256.Size]byte) (Digest, error) {
	if value == [sha256.Size]byte{} {
		return Digest{}, errors.New("world admission digest is empty")
	}
	return Digest{value: value}, nil
}

// ParseDigestHex 从严格小写十六进制恢复摘要。
func ParseDigestHex(value string) (Digest, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size || value != strings.ToLower(value) {
		return Digest{}, errors.New("world admission digest encoding is invalid")
	}
	var bytes [sha256.Size]byte
	copy(bytes[:], decoded)
	return NewDigest(bytes)
}

// Bytes 返回固定大小副本。
func (digest Digest) Bytes() [sha256.Size]byte { return digest.value }

// Hex 返回 Redis Hash/key 使用的小写十六进制。
func (digest Digest) Hex() string { return hex.EncodeToString(digest.value[:]) }

// Valid 报告摘要是否非零。
func (digest Digest) Valid() bool { return digest.value != [sha256.Size]byte{} }

// Equal 比较完整固定长度摘要。
func (digest Digest) Equal(other Digest) bool { return digest.value == other.value }

// String 防止摘要被误当作可公开 credential correlation。
func (Digest) String() string { return admissionPlaceholder }

// GoString 防止 `%#v` 展开摘要 bytes。
func (Digest) GoString() string { return admissionPlaceholder }

// Binding 保存 issuer 与 verifier 之间不可由客户端覆盖的全部授权事实。
type Binding struct {
	// playerID 是AuthContext确认的操作者。
	playerID account.PlayerID
	// sessionID 是credential绑定的认证lineage。
	sessionID session.SessionID
	// epoch 是撤销旧lineage的单调屏障。
	epoch session.Epoch
	// role 是Owner或Visitor的封闭授权角色。
	role Role
	// worldID 是唯一允许进入的PersonalWorld。
	worldID personalworld.PersonalWorldID
	// visitSessionID 只在Visitor purpose存在。
	visitSessionID visitsession.VisitSessionID
	// purpose 防止OWN_WORLD、JOIN与RECONNECT互换。
	purpose Purpose
	// visitRevision 冻结 Visitor admission 签发时的权威 VisitSession CAS 版本；Owner 为零值。
	visitRevision visitsession.Revision
	// assignment 保存不可公开但必须完整比较的current stamp。
	assignment placement.AssignmentStamp
	// endpoint 是唯一允许消费的TLS/TCP目标。
	endpoint session.Endpoint
	// issuedAt 是credential签发时间(UTC,微秒)。
	issuedAt time.Time
	// expiresAt 是等于即失效的业务deadline(UTC,微秒)。
	expiresAt time.Time
}

// NewBinding 校验 application 从 AuthContext、领域与 placement 派生的完整事实。
func NewBinding(playerID account.PlayerID, sessionID session.SessionID, epoch session.Epoch, role Role, worldID personalworld.PersonalWorldID, visitSessionID visitsession.VisitSessionID, purpose Purpose, visitRevision visitsession.Revision, assignment placement.AssignmentStamp, endpoint session.Endpoint, issuedAt time.Time, expiresAt time.Time) (Binding, error) {
	binding := Binding{playerID: playerID, sessionID: sessionID, epoch: epoch, role: role, worldID: worldID, visitSessionID: visitSessionID, purpose: purpose, visitRevision: visitRevision, assignment: assignment, endpoint: endpoint, issuedAt: canonicalTime(issuedAt), expiresAt: canonicalTime(expiresAt)}
	if !binding.Valid() {
		return Binding{}, errors.New("world admission binding is incomplete")
	}
	if role == RoleOwner && (purpose != PurposeOwnWorld || visitSessionID.Valid() || visitRevision.Valid()) {
		return Binding{}, errors.New("owner admission binding is inconsistent")
	}
	if role == RoleVisitor && ((purpose != PurposeJoin && purpose != PurposeReconnect) || !visitSessionID.Valid() || !visitRevision.Valid()) {
		return Binding{}, errors.New("visitor admission binding is inconsistent")
	}
	return binding, nil
}

// PlayerID 返回认证玩家身份。
func (binding Binding) PlayerID() account.PlayerID { return binding.playerID }

// SessionID 返回 credential 固定的 session lineage。
func (binding Binding) SessionID() session.SessionID { return binding.sessionID }

// Epoch 返回 credential 固定的失效屏障。
func (binding Binding) Epoch() session.Epoch { return binding.epoch }

// Role 返回由权威事实派生的角色。
func (binding Binding) Role() Role { return binding.role }

// WorldID 返回 credential 唯一允许进入的 PersonalWorld。
func (binding Binding) WorldID() personalworld.PersonalWorldID { return binding.worldID }

// VisitSessionID 返回 Visitor target；Owner admission 返回零值。
func (binding Binding) VisitSessionID() visitsession.VisitSessionID { return binding.visitSessionID }

// Purpose 返回不可跨状态复用的用途。
func (binding Binding) Purpose() Purpose { return binding.purpose }

// VisitRevision 返回 Visitor admission 冻结的 VisitSession revision；Owner 返回零值。
func (binding Binding) VisitRevision() visitsession.Revision { return binding.visitRevision }

// Assignment 返回必须与 current placement 完整相等的 stamp。
func (binding Binding) Assignment() placement.AssignmentStamp { return binding.assignment }

// Endpoint 返回唯一允许消费 credential 的 TLS/TCP endpoint。
func (binding Binding) Endpoint() session.Endpoint { return binding.endpoint }

// IssuedAt 返回 UTC 微秒签发时刻。
func (binding Binding) IssuedAt() time.Time { return binding.issuedAt }

// ExpiresAt 返回等于即失效的 UTC 微秒 deadline。
func (binding Binding) ExpiresAt() time.Time { return binding.expiresAt }

// Valid 报告 binding 是否结构完整且时间、channel、target组合一致。
func (binding Binding) Valid() bool {
	if !binding.playerID.Valid() || !binding.sessionID.Valid() || !binding.epoch.Valid() || !binding.role.Valid() || !binding.worldID.Valid() || !binding.purpose.Valid() || !binding.assignment.Valid() || !binding.endpoint.Valid() || binding.endpoint.Channel() != session.ChannelTLSTCP || binding.issuedAt.IsZero() || !binding.expiresAt.After(binding.issuedAt) || binding.assignment.WorldID() != binding.worldID {
		return false
	}
	return (binding.role == RoleOwner && binding.purpose == PurposeOwnWorld && !binding.visitSessionID.Valid() && !binding.visitRevision.Valid()) || (binding.role == RoleVisitor && binding.visitSessionID.Valid() && binding.visitRevision.Valid() && (binding.purpose == PurposeJoin || binding.purpose == PurposeReconnect))
}

// Equal 比较完整授权事实。
func (binding Binding) Equal(other Binding) bool {
	return binding.playerID == other.playerID && binding.sessionID == other.sessionID && binding.epoch == other.epoch && binding.role == other.role && binding.worldID == other.worldID && binding.visitSessionID == other.visitSessionID && binding.purpose == other.purpose && binding.visitRevision == other.visitRevision && binding.assignment.Equal(other.assignment) && binding.endpoint.Equal(other.endpoint) && binding.issuedAt.Equal(other.issuedAt) && binding.expiresAt.Equal(other.expiresAt)
}

// String 防止默认格式化泄漏内部授权事实。
func (Binding) String() string { return admissionPlaceholder }

// GoString 防止 `%#v` 展开 private fields。
func (Binding) GoString() string { return admissionPlaceholder }

// LogValue 让结构化日志只记录固定占位符。
func (Binding) LogValue() slog.Value { return slog.StringValue(admissionPlaceholder) }

// Qualification 是 verifier 在原子消费并复核 placement 后返回的只读事实。
type Qualification struct {
	// binding 只由成功verifier路径写入。
	binding Binding
}

// newQualification 只允许本 package 的成功 verify 路径构造资格。
func newQualification(binding Binding) (Qualification, error) {
	if !binding.Valid() {
		return Qualification{}, errors.New("world admission qualification is invalid")
	}
	return Qualification{binding: binding}, nil
}

// Binding 返回不可变 binding 值副本。
func (qualification Qualification) Binding() Binding { return qualification.binding }

// Valid 报告 qualification 是否来自完整 binding。
func (qualification Qualification) Valid() bool { return qualification.binding.Valid() }

// VisitSessionQualification 为 Visitor 构造 VisitSession 的 purpose-scoped受信输入。
func (qualification Qualification) VisitSessionQualification() (visitsession.JoinQualification, error) {
	binding := qualification.binding
	if !qualification.Valid() || binding.role != RoleVisitor {
		return visitsession.JoinQualification{}, errors.New("world admission is not a visitor qualification")
	}
	if binding.purpose == PurposeJoin {
		return visitsession.HydrateJoinQualification(binding.visitSessionID, binding.playerID, binding.sessionID, binding.epoch, binding.assignment, binding.expiresAt)
	}
	if binding.purpose == PurposeReconnect {
		return visitsession.HydrateReconnectQualification(binding.visitSessionID, binding.playerID, binding.sessionID, binding.epoch, binding.assignment, binding.expiresAt)
	}
	return visitsession.JoinQualification{}, errors.New("world admission purpose is not supported by VisitSession")
}

// String 防止默认格式化扩散授权事实。
func (Qualification) String() string { return admissionPlaceholder }

// GoString 防止 `%#v` 展开 private binding。
func (Qualification) GoString() string { return admissionPlaceholder }

// LogValue 让结构化日志只记录固定占位符。
func (Qualification) LogValue() slog.Value { return slog.StringValue(admissionPlaceholder) }

// validOperationID 接受有界安全 ASCII，禁止 namespace/control 注入。
func validOperationID(value string) bool {
	if len(value) < 1 || len(value) > maximumOperationIDBytes {
		return false
	}
	for _, character := range []byte(value) {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '-' && character != '_' && character != '.' {
			return false
		}
	}
	return true
}

// canonicalTime 统一移除单调分量并使用 Redis schema 的 UTC 微秒精度。
func canonicalTime(value time.Time) time.Time { return value.UTC().Truncate(time.Microsecond) }
