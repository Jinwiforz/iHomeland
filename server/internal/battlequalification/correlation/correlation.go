package correlation

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"sync"
)

const (
	// correlationKeyBytes 是 run-local HMAC-SHA-256 key 宽度。
	correlationKeyBytes = sha256.Size
	// digestBytes 是 evidence 允许保存的低敏摘要宽度。
	digestBytes = 8
	// maximumClientSlot 是已资格 battle actor hard cap。
	maximumClientSlot = 8
	// maximumSimulationIdentityBytes 限制传入摘要器的不可变 instance identity。
	maximumSimulationIdentityBytes = 128
)

const (
	// sessionDomain 隔离 BattleSession routing identity。
	sessionDomain = "ihomeland/battle-qualification/correlation/session/v1"
	// assignmentDomain 隔离 AssignmentStamp fingerprint。
	assignmentDomain = "ihomeland/battle-qualification/correlation/assignment/v1"
	// instanceDomain 隔离 SimulationInstance identity。
	instanceDomain = "ihomeland/battle-qualification/correlation/instance/v1"
)

// WorkloadPhase 是 qualification corpus 登记的 closed workload phase。
type WorkloadPhase string

const (
	// PhaseAdmission 验证 actor capacity 与 membership compatibility。
	PhaseAdmission WorkloadPhase = "admission"
	// PhaseClean 是无 impairment 的基线阶段。
	PhaseClean WorkloadPhase = "clean"
	// PhaseIdle 是只维持 probe/snapshot 的低输入阶段。
	PhaseIdle WorkloadPhase = "idle"
	// PhaseMovementHeavy 是 40 Hz movement 输入阶段。
	PhaseMovementHeavy WorkloadPhase = "movement-heavy"
	// PhaseCombatHeavy 是高频 combat 输入阶段。
	PhaseCombatHeavy WorkloadPhase = "combat-heavy"
	// PhaseBossBurst 是可靠事件突发阶段。
	PhaseBossBurst WorkloadPhase = "boss-burst"
	// PhaseKCPRetransmit 是 KCP 丢包重传阶段。
	PhaseKCPRetransmit WorkloadPhase = "kcp-retransmit"
	// PhaseQueuePressure 是有界 queue 压力阶段。
	PhaseQueuePressure WorkloadPhase = "queue-pressure"
	// PhaseDisconnectDrain 是断线停止输入并排空队列阶段。
	PhaseDisconnectDrain WorkloadPhase = "disconnect-drain"
)

// Valid 报告 phase 是否来自冻结 workload corpus。
func (phase WorkloadPhase) Valid() bool {
	switch phase {
	case PhaseAdmission, PhaseClean, PhaseIdle, PhaseMovementHeavy, PhaseCombatHeavy,
		PhaseBossBurst, PhaseKCPRetransmit, PhaseQueuePressure, PhaseDisconnectDrain:
		return true
	default:
		return false
	}
}

// Identity 是四源 evidence 共用的低敏 run-local correlation。
type Identity struct {
	// ClientSlot 是 1..8 的 run-local actor slot。
	ClientSlot uint8 `json:"clientSlot"`
	// SessionDigest 是 BattleSession routing identity 的 keyed 64-bit 摘要。
	SessionDigest string `json:"sessionDigest"`
	// BattleSessionGeneration 是不可复活 session incarnation。
	BattleSessionGeneration uint32 `json:"battleSessionGeneration"`
	// EndpointGeneration 是当前 authenticated endpoint generation。
	EndpointGeneration uint32 `json:"endpointGeneration"`
	// MappingGeneration 是 InputTick 到 simulation timeline 的 current generation。
	MappingGeneration uint64 `json:"mappingGeneration"`
	// AssignmentDigest 是完整 AssignmentStamp fingerprint 的 keyed 64-bit 摘要。
	AssignmentDigest string `json:"assignmentDigest"`
	// InstanceDigest 是 SimulationInstance identity 的 keyed 64-bit 摘要。
	InstanceDigest string `json:"instanceDigest"`
	// Phase 是采样所属的 closed workload phase。
	Phase WorkloadPhase `json:"phase"`
}

// Source 是只在 Build 调用栈中存在的完整关联输入。
//
// 调用方不得序列化或保存 Source；Build 不在 Correlator 内保留任何字段。
type Source struct {
	// ClientSlot 是 1..8 的 run-local actor slot。
	ClientSlot uint8
	// SessionIdentity 是 secure wire 的 16-byte session routing identity。
	SessionIdentity [16]byte
	// BattleSessionGeneration 是不可复活 session incarnation。
	BattleSessionGeneration uint32
	// EndpointGeneration 是当前 authenticated endpoint generation。
	EndpointGeneration uint32
	// MappingGeneration 是 InputTick timeline generation。
	MappingGeneration uint64
	// AssignmentFingerprint 是完整 AssignmentStamp 的 SHA-256。
	AssignmentFingerprint [sha256.Size]byte
	// SimulationIdentity 是 exact SimulationInstance 的 opaque identity。
	SimulationIdentity string
	// Phase 是当前 workload phase。
	Phase WorkloadPhase
}

// Correlator 拥有单次 run 的随机 HMAC key。
type Correlator struct {
	// mutex 串行化 Build 与 Close，防止清零期间读取 key。
	mutex sync.RWMutex
	// key 在 run cleanup 时清零，不进入 evidence。
	key [correlationKeyBytes]byte
	// closed 禁止 cleanup 后继续生成不可关联的新摘要。
	closed bool
}

// New 使用系统 CSPRNG 创建 run-local Correlator。
func New() (*Correlator, error) {
	return newFromReader(rand.Reader)
}

// newFromReader 允许 unit test 注入确定性 entropy，不作为 production API 暴露。
func newFromReader(reader io.Reader) (*Correlator, error) {
	if reader == nil {
		return nil, errors.New("correlation entropy source is nil")
	}
	var key [correlationKeyBytes]byte
	if _, err := io.ReadFull(reader, key[:]); err != nil {
		clear(key[:])
		return nil, errors.New("read correlation entropy")
	}
	if key == [correlationKeyBytes]byte{} {
		return nil, errors.New("correlation key is empty")
	}
	return &Correlator{key: key}, nil
}

// Build 校验完整 source，并生成不能跨 run 复用的低敏 correlation。
func (correlator *Correlator) Build(source Source) (Identity, error) {
	if correlator == nil {
		return Identity{}, errors.New("correlator is nil")
	}
	if source.ClientSlot == 0 || source.ClientSlot > maximumClientSlot ||
		source.SessionIdentity == [16]byte{} ||
		source.BattleSessionGeneration == 0 ||
		source.EndpointGeneration == 0 ||
		source.MappingGeneration == 0 ||
		source.AssignmentFingerprint == [sha256.Size]byte{} ||
		len(source.SimulationIdentity) == 0 ||
		len(source.SimulationIdentity) > maximumSimulationIdentityBytes ||
		!source.Phase.Valid() {
		return Identity{}, errors.New("correlation source is invalid")
	}
	correlator.mutex.RLock()
	defer correlator.mutex.RUnlock()
	if correlator.closed {
		return Identity{}, errors.New("correlator is closed")
	}
	return Identity{
		ClientSlot: source.ClientSlot,
		SessionDigest: correlator.digest(
			sessionDomain, source.SessionIdentity[:],
		),
		BattleSessionGeneration: source.BattleSessionGeneration,
		EndpointGeneration:      source.EndpointGeneration,
		MappingGeneration:       source.MappingGeneration,
		AssignmentDigest: correlator.digest(
			assignmentDomain, source.AssignmentFingerprint[:],
		),
		InstanceDigest: correlator.digest(
			instanceDomain, []byte(source.SimulationIdentity),
		),
		Phase: source.Phase,
	}, nil
}

// Close 清零 run-local HMAC key；重复调用安全。
func (correlator *Correlator) Close() error {
	if correlator == nil {
		return nil
	}
	correlator.mutex.Lock()
	clear(correlator.key[:])
	correlator.closed = true
	correlator.mutex.Unlock()
	return nil
}

// digest 使用 domain 与长度前缀消除跨字段、跨类型关联。
func (correlator *Correlator) digest(domain string, material []byte) string {
	mac := hmac.New(sha256.New, correlator.key[:])
	writeBytes(mac, []byte(domain))
	writeBytes(mac, material)
	sum := mac.Sum(nil)
	result := hex.EncodeToString(sum[:digestBytes])
	clear(sum)
	return result
}

// writeBytes 使用固定大端长度前缀，避免拼接歧义。
func writeBytes(target hash.Hash, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = target.Write(length[:])
	_, _ = target.Write(value)
}
