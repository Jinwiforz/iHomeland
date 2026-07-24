// Package simulationcontrol 拥有 Go 与 C++ simulation child 之间的私有控制契约。
package simulationcontrol

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/jinwiforz/ihomeland/server/internal/placement"
)

const (
	// SchemaVersion 是冻结 control frame schema。
	SchemaVersion = "simulation-control-v1"
	// MaximumFrameBytes 是 length prefix 允许的 JSON hard limit。
	MaximumFrameBytes = 65_536
	// PendingRequestLimit 是单 session 等待关联结果的 hard limit。
	PendingRequestLimit = 256
	// QualifiedActorCapacity 是 B0.3 已资格的单 instance actor 上限。
	QualifiedActorCapacity = 8
)

var (
	simulationNodeIDPattern     = regexp.MustCompile(`^snode_[A-Za-z0-9_-]{4,80}$`)
	simulationInstanceIDPattern = regexp.MustCompile(`^sinst_[0-9a-f]{32}$`)
	requestIDPattern            = regexp.MustCompile(`^sctl_[A-Za-z0-9_-]{16,80}$`)
	resultIDPattern             = regexp.MustCompile(`^sresult_[A-Za-z0-9_-]{4,88}$`)
	resultKindPattern           = regexp.MustCompile(`^[a-z][a-z0-9.-]{2,95}$`)
	digestPattern               = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// SimulationNodeID 标识一次不可复活 C++ child incarnation。
type SimulationNodeID string

// NewSimulationNodeID 校验受信 Composition Root 生成的 node identity。
func NewSimulationNodeID(value string) (SimulationNodeID, error) {
	if !simulationNodeIDPattern.MatchString(value) {
		return "", errors.New("simulation node identity is invalid")
	}
	return SimulationNodeID(value), nil
}

// String 返回仅供内部协议投影的 node identity。
func (id SimulationNodeID) String() string { return string(id) }

// Valid 报告 node identity 是否符合封闭格式。
func (id SimulationNodeID) Valid() bool {
	return simulationNodeIDPattern.MatchString(string(id))
}

// SimulationInstanceID 标识 C++ node 内一次不可复活 worker timeline。
type SimulationInstanceID string

// NewSimulationInstanceID 校验 C++ ready receipt 返回的 instance identity。
func NewSimulationInstanceID(value string) (SimulationInstanceID, error) {
	if !simulationInstanceIDPattern.MatchString(value) {
		return "", errors.New("simulation instance identity is invalid")
	}
	return SimulationInstanceID(value), nil
}

// String 返回仅供内部 target/receipt 关联的 instance identity。
func (id SimulationInstanceID) String() string { return string(id) }

// Valid 报告 instance identity 是否符合封闭格式。
func (id SimulationInstanceID) Valid() bool {
	return simulationInstanceIDPattern.MatchString(string(id))
}

// RequestID 标识一次稳定 control request 或 result handshake。
type RequestID string

// NewRequestID 校验受信生成的 control correlation identity。
func NewRequestID(value string) (RequestID, error) {
	if !requestIDPattern.MatchString(value) {
		return "", errors.New("control request identity is invalid")
	}
	return RequestID(value), nil
}

// String 返回内部 frame 使用的 request identity。
func (id RequestID) String() string { return string(id) }

// Valid 报告 request identity 是否符合封闭格式。
func (id RequestID) Valid() bool { return requestIDPattern.MatchString(string(id)) }

// Digest 是 control contract 中规范 lowercase SHA-256。
type Digest string

// NewDigest 校验 qualification、asset 或 proposal digest。
func NewDigest(value string) (Digest, error) {
	if !digestPattern.MatchString(value) {
		return "", errors.New("control digest is invalid")
	}
	return Digest(value), nil
}

// String 返回用于精确协议比较的 digest。
func (digest Digest) String() string { return string(digest) }

// Valid 报告 digest 是否为规范 SHA-256。
func (digest Digest) Valid() bool { return digestPattern.MatchString(string(digest)) }

// NewSessionNonce 使用 CSPRNG 创建不写入日志的 256-bit bootstrap nonce。
func NewSessionNonce() (Digest, error) {
	var material [32]byte
	if _, err := rand.Read(material[:]); err != nil {
		return "", fmt.Errorf("generate simulation control nonce: %w", err)
	}
	return NewDigest(hex.EncodeToString(material[:]))
}

// NodeCapacity 是 child 已验证的 instance 与 actor hard limits。
type NodeCapacity struct {
	// Instances 是同一 child 同时运行的 instance slots。
	Instances int
	// Actors 是每个 instance 的 actor 上限。
	Actors int
}

// Validate 拒绝零值、超出 pending 上限或超过 B0.3 actor 资格的容量。
func (capacity NodeCapacity) Validate() error {
	if capacity.Instances < 1 || capacity.Instances > PendingRequestLimit ||
		capacity.Actors < 1 || capacity.Actors > QualifiedActorCapacity {
		return errors.New("simulation node capacity is invalid")
	}
	return nil
}

// BuildBinding 固定 child binary 与 B0.3 model/profile 资格输入。
type BuildBinding struct {
	// BuildIdentity 是预期 Release build identity。
	BuildIdentity Digest
	// ModelManifest 是冻结 battle model manifest digest。
	ModelManifest Digest
	// ProfileManifest 是冻结 network profile manifest digest。
	ProfileManifest Digest
	// PlatformQualification 必须是 Windows x64 implementation 结论。
	PlatformQualification string
}

// Validate 拒绝缺失或非 implementation-qualified Windows x64 binding。
func (binding BuildBinding) Validate() error {
	if !binding.BuildIdentity.Valid() || !binding.ModelManifest.Valid() ||
		!binding.ProfileManifest.Valid() ||
		binding.PlatformQualification != "implementation-qualified-windows-x64" {
		return errors.New("simulation build binding is invalid")
	}
	return nil
}

// SimulationTarget 是 application 内部投递所需的 current runtime binding。
//
// Target 不携带 endpoint、credential、process handle 或 transport 类型。
type SimulationTarget struct {
	// RuntimeNodeID 绑定 placement 已选择的受信 runtime node。
	RuntimeNodeID placement.RuntimeNodeID
	// NodeID 绑定不可复活 child incarnation。
	NodeID SimulationNodeID
	// InstanceID 绑定不可复活 simulation worker。
	InstanceID SimulationInstanceID
	// AssignmentFingerprint 绑定完整 placement stamp。
	AssignmentFingerprint Digest
	// MappingGeneration 绑定输入映射时间线，禁止跨代复用 input。
	MappingGeneration uint64
	// ModelManifest 绑定已通过 B0.3 资格验证的 simulation model。
	ModelManifest Digest
	// ProfileManifest 绑定已通过 B0.3 资格验证的 network profile。
	ProfileManifest Digest
	// ConfigIdentity 绑定已检查的 runtime config。
	ConfigIdentity Digest
	// ActorCapacity 是当前 instance 的 Go-owned hard upper bound。
	ActorCapacity int
	// Revision 在 replacement 后单调变化，使旧 target cache 失效。
	Revision uint64
}

// Validate 拒绝 incomplete 或零 revision target。
func (target SimulationTarget) Validate() error {
	if !target.RuntimeNodeID.Valid() || !target.NodeID.Valid() || !target.InstanceID.Valid() ||
		!target.AssignmentFingerprint.Valid() || target.MappingGeneration == 0 ||
		!target.ModelManifest.Valid() || !target.ProfileManifest.Valid() ||
		!target.ConfigIdentity.Valid() ||
		target.ActorCapacity < 1 || target.Revision == 0 {
		return errors.New("simulation target is invalid")
	}
	return nil
}

// ResultProposal 是 child 等待持久裁决的 immutable 低敏摘要。
type ResultProposal struct {
	// ResultID 是 node timeline 内不可复用的结果 identity。
	ResultID string
	// Kind 必须存在于 Go result catalog。
	Kind string
	// AssignmentFingerprint 绑定完整 placement stamp。
	AssignmentFingerprint Digest
	// InstanceID 绑定产生结果的 C++ worker。
	InstanceID SimulationInstanceID
	// TickStart 是摘要覆盖首 Tick。
	TickStart uint64
	// TickEnd 是摘要覆盖末 Tick。
	TickEnd uint64
	// PayloadDigest 绑定低敏 canonical payload。
	PayloadDigest Digest
	// EvidenceDigest 绑定 replay evidence。
	EvidenceDigest Digest
	// ProposalFingerprint 绑定全部 immutable 字段。
	ProposalFingerprint Digest
}

// CanonicalFingerprint 重算除 ProposalFingerprint 外全部 immutable 字段的规范摘要。
func (proposal ResultProposal) CanonicalFingerprint() Digest {
	material := proposal.ResultID + "|" + proposal.Kind + "|" +
		proposal.AssignmentFingerprint.String() + "|" +
		proposal.InstanceID.String() + "|" +
		strconv.FormatUint(proposal.TickStart, 10) + "|" +
		strconv.FormatUint(proposal.TickEnd, 10) + "|" +
		proposal.PayloadDigest.String() + "|" +
		proposal.EvidenceDigest.String()
	sum := sha256.Sum256([]byte(material))
	return Digest(hex.EncodeToString(sum[:]))
}

// Validate 执行通用结构和 canonical fingerprint 检查；业务 kind 与 fence 由 coordinator 再判定。
func (proposal ResultProposal) Validate() error {
	if !resultIDPattern.MatchString(proposal.ResultID) ||
		!resultKindPattern.MatchString(proposal.Kind) ||
		!proposal.AssignmentFingerprint.Valid() || !proposal.InstanceID.Valid() ||
		!proposal.PayloadDigest.Valid() || !proposal.EvidenceDigest.Valid() ||
		!proposal.ProposalFingerprint.Valid() || proposal.TickStart > proposal.TickEnd ||
		proposal.ProposalFingerprint != proposal.CanonicalFingerprint() {
		return fmt.Errorf("simulation result proposal is invalid")
	}
	return nil
}
