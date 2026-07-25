package battleticket

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"hash"
	"io"

	"golang.org/x/crypto/hkdf"
)

const (
	// authorityDomain 隔离幂等候选事实的 canonical fingerprint。
	authorityDomain = "ihomeland/battle-ticket/authority/v1"
	// ticketIDDomain 隔离公开 lookup identity 的 HMAC。
	ticketIDDomain = "ihomeland/battle-ticket/id/v1"
	// bindingDomain 隔离包含 ticket ID 的完整 install binding。
	bindingDomain = "ihomeland/battle-ticket/binding/v1"
	// ticketSecretDomain 隔离 HTTPS credential 的 HMAC。
	ticketSecretDomain = "ihomeland/battle-ticket/secret/v1"
	// proofKeyDomain 是 HKDF-SHA-256 expand 的固定 info。
	proofKeyDomain = "ihomeland/battle-ticket/proof-key/v1"
)

// Deriver 使用 server-owned 256-bit key 确定性生成 response-loss 可重放的 ticket material。
type Deriver struct {
	// key 是只存在于进程 secret owner 内的固定长度根密钥。
	key [secretMaterialBytes]byte
}

// NewDeriver 复制 exact 256-bit 根密钥，拒绝零值与可变长度输入。
func NewDeriver(key []byte) (*Deriver, error) {
	if len(key) != secretMaterialBytes {
		return nil, operationError("derive", ErrorCodeInvalidArgument, nil)
	}
	var fixed [secretMaterialBytes]byte
	copy(fixed[:], key)
	if fixed == [secretMaterialBytes]byte{} {
		return nil, operationError("derive", ErrorCodeInvalidArgument, nil)
	}
	return &Deriver{key: fixed}, nil
}

// Derive 对 exact canonical Facts 确定性生成 ticket ID、secret、proof key 与 binding。
//
// 相同根密钥和相同 Facts 必须产生完全相同结果；任一 session、target、endpoint、slot、
// source identity 或 deadline 漂移都会改变 material。根密钥与派生 secret 均不进入 digest source。
func (deriver *Deriver) Derive(facts Facts) (Material, error) {
	if deriver == nil {
		return Material{}, operationError("derive", ErrorCodeInvalidArgument, nil)
	}
	facts.IssuedAt = canonicalTime(facts.IssuedAt)
	facts.ExpiresAt = canonicalTime(facts.ExpiresAt)
	if !facts.Valid() {
		return Material{}, operationError("derive", ErrorCodeInvalidArgument, nil)
	}
	authorityFingerprint := fingerprintFacts(authorityDomain, facts, false)
	ticketIDMAC := hmac.New(sha256.New, deriver.key[:])
	writeString(ticketIDMAC, ticketIDDomain)
	authorityBytes := authorityFingerprint.Bytes()
	writeBytes(ticketIDMAC, authorityBytes[:])
	ticketIDValue := ticketIDPrefix + base64.RawURLEncoding.EncodeToString(
		ticketIDMAC.Sum(nil)[:ticketIDMaterialBytes])
	ticketID, err := ParseTicketID(ticketIDValue)
	if err != nil {
		return Material{}, operationError("derive", ErrorCodeDependencyDefect, err)
	}
	binding, err := newBinding(ticketID, facts)
	if err != nil {
		return Material{}, operationError("derive", ErrorCodeDependencyDefect, err)
	}
	fingerprint := fingerprintBinding(binding)
	secretMAC := hmac.New(sha256.New, deriver.key[:])
	writeString(secretMAC, ticketSecretDomain)
	writeString(secretMAC, facts.IssueID.Value())
	fingerprintBytes := fingerprint.Bytes()
	writeBytes(secretMAC, fingerprintBytes[:])
	secret, err := ParseTicketSecret(ticketSecretPrefix + base64.RawURLEncoding.EncodeToString(secretMAC.Sum(nil)))
	if err != nil {
		return Material{}, operationError("derive", ErrorCodeDependencyDefect, err)
	}
	secretBytes := secret.Bytes()
	reader := hkdf.New(sha256.New, secretBytes[:], fingerprintBytes[:], []byte(proofKeyDomain))
	var proofBytes [secretMaterialBytes]byte
	if _, err := io.ReadFull(reader, proofBytes[:]); err != nil {
		return Material{}, operationError("derive", ErrorCodeDependencyDefect, err)
	}
	proofKey, err := NewProofKey(proofBytes)
	if err != nil {
		return Material{}, operationError("derive", ErrorCodeDependencyDefect, err)
	}
	return Material{binding: binding, fingerprint: fingerprint, secret: secret, proofKey: proofKey}, nil
}

// String 防止默认格式化泄漏根密钥。
func (Deriver) String() string { return redactedValue }

// GoString 防止 `%#v` 展开根密钥。
func (Deriver) GoString() string { return redactedValue }

// fingerprintBinding 绑定 ticket ID 与全部 authority facts。
func fingerprintBinding(binding Binding) Digest {
	return fingerprintFacts(bindingDomain, binding.facts, true, binding.ticketID.Value())
}

// fingerprintFacts 以长度前缀和固定大端整数编码全部权威事实。
func fingerprintFacts(domain string, facts Facts, includeTicketID bool, ticketID ...string) Digest {
	hasher := sha256.New()
	writeString(hasher, domain)
	if includeTicketID {
		writeString(hasher, ticketID[0])
	}
	writeString(hasher, facts.PlayerID.String())
	writeString(hasher, facts.SessionID.String())
	writeUint64(hasher, uint64(facts.SessionEpoch))
	writeUint64(hasher, uint64(facts.Role))
	writeString(hasher, facts.WorldID.String())
	writeString(hasher, facts.VisitSessionID.Value())
	writeAssignment(hasher, facts)
	writeString(hasher, facts.SimulationNodeID.String())
	writeString(hasher, facts.SimulationInstanceID.String())
	writeUint64(hasher, facts.MappingGeneration)
	writeUint64(hasher, facts.TargetRevision)
	writeString(hasher, facts.ModelIdentity.String())
	writeString(hasher, facts.ProfileIdentity.String())
	writeString(hasher, facts.ConfigIdentity.String())
	wireIdentity := facts.WireIdentity.Bytes()
	writeBytes(hasher, wireIdentity[:])
	writeUint64(hasher, uint64(facts.ActorSlot.Index()))
	writeString(hasher, facts.Endpoint.Host())
	writeUint64(hasher, uint64(facts.Endpoint.Port()))
	writeString(hasher, facts.IssueID.Value())
	writeInt64(hasher, facts.IssuedAt.UnixMicro())
	writeInt64(hasher, facts.ExpiresAt.UnixMicro())
	return digestFromHasher(hasher)
}

// writeAssignment 编码完整 placement stamp 与 SimulationTarget 提供的交叉检查 digest。
func writeAssignment(hasher hash.Hash, facts Facts) {
	stamp := facts.Assignment
	writeString(hasher, stamp.WorldID().String())
	writeString(hasher, stamp.InstanceID().String())
	writeString(hasher, stamp.NodeID().String())
	writeUint64(hasher, stamp.Generation().Uint64())
	writeUint64(hasher, stamp.FencingToken().Uint64())
	writeString(hasher, facts.AssignmentFingerprint.String())
	writeString(hasher, facts.RuntimeNodeID.String())
}

// writeString 使用 byte length 前缀消除拼接歧义。
func writeString(hasher hash.Hash, value string) { writeBytes(hasher, []byte(value)) }

// writeBytes 使用 uint64 big-endian length 后写入原始 bytes。
func writeBytes(hasher hash.Hash, value []byte) {
	writeUint64(hasher, uint64(len(value)))
	// hash.Hash 的 Write 契约接收全部输入并返回 nil error。
	_, _ = hasher.Write(value)
}

// writeUint64 使用固定 big-endian 编码整数。
func writeUint64(hasher hash.Hash, value uint64) {
	var buffer [8]byte
	binary.BigEndian.PutUint64(buffer[:], value)
	// hash.Hash 的 Write 契约接收全部输入并返回 nil error。
	_, _ = hasher.Write(buffer[:])
}

// writeInt64 保留有符号 Unix microseconds 的 bit pattern。
func writeInt64(hasher hash.Hash, value int64) { writeUint64(hasher, uint64(value)) }

// digestFromHasher 复制固定 SHA-256 输出。
func digestFromHasher(hasher hash.Hash) Digest {
	var value [sha256.Size]byte
	copy(value[:], hasher.Sum(nil))
	return Digest{value: value}
}

// factsEqual 比较幂等或完整 binding 所需的全部权威事实。
func factsEqual(left Facts, right Facts, includeTimes bool) bool {
	equal := left.PlayerID == right.PlayerID &&
		left.SessionID == right.SessionID &&
		left.SessionEpoch == right.SessionEpoch &&
		left.Role == right.Role &&
		left.WorldID == right.WorldID &&
		left.VisitSessionID == right.VisitSessionID &&
		left.Assignment.Equal(right.Assignment) &&
		left.AssignmentFingerprint == right.AssignmentFingerprint &&
		left.RuntimeNodeID == right.RuntimeNodeID &&
		left.SimulationNodeID == right.SimulationNodeID &&
		left.SimulationInstanceID == right.SimulationInstanceID &&
		left.MappingGeneration == right.MappingGeneration &&
		left.TargetRevision == right.TargetRevision &&
		left.ModelIdentity == right.ModelIdentity &&
		left.ProfileIdentity == right.ProfileIdentity &&
		left.ConfigIdentity == right.ConfigIdentity &&
		left.WireIdentity.Equal(right.WireIdentity) &&
		left.ActorSlot == right.ActorSlot &&
		left.Endpoint.Equal(right.Endpoint) &&
		left.IssueID == right.IssueID
	if !includeTimes {
		return equal
	}
	return equal && left.IssuedAt.Equal(right.IssuedAt) && left.ExpiresAt.Equal(right.ExpiresAt)
}
