package worldadmission

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
)

const (
	// minimumDerivationKeyBytes 要求至少256-bit secret material。
	minimumDerivationKeyBytes = 32
	// maximumAdmissionLifetime 防止配置错误签发长期 bearer资格。
	maximumAdmissionLifetime = 5 * time.Minute
	// maximumReplayRetention 限制credential失效后的Redis证据保留窗口。
	maximumReplayRetention = 10 * time.Minute
	// credentialDomainSeparator 防止同一key在其他HMAC用途产生可替换输出。
	credentialDomainSeparator = "ihomeland/world-admission/credential/v1"
)

// Policy 是签发有效期与response-loss证据窗口的严格配置。
type Policy struct {
	// maximumLifetime 限制credential业务有效期。
	maximumLifetime time.Duration
	// replayRetention 限制业务失效后的response-loss证据窗口。
	replayRetention time.Duration
}

// NewPolicy 校验短期业务 lifetime 与有界 replay retention。
func NewPolicy(maximumLifetime time.Duration, replayRetention time.Duration) (Policy, error) {
	if maximumLifetime <= 0 || maximumLifetime > maximumAdmissionLifetime || replayRetention < 0 || replayRetention > maximumReplayRetention {
		return Policy{}, errors.New("world admission policy is invalid")
	}
	return Policy{maximumLifetime: maximumLifetime, replayRetention: replayRetention}, nil
}

// Service 编排 deterministic issuer、原子 verifier 与 current assignment复核。
type Service struct {
	// store 原子维护issuance与credential可失效运行态。
	store Store
	// placements 读取current full assignment，不修改runtime。
	placements PlacementReader
	// clock 提供单次编排使用的受信绝对时间。
	clock Clock
	// derivationKey 是构造时复制的HMAC secret，不进入store或日志。
	derivationKey []byte
	// policy 是启动后只读的短期安全预算。
	policy Policy
}

// NewService 复制 derivation key 并要求显式 production 依赖；不创建 client 或 goroutine。
//
// 调用方仍拥有传入依赖的生命周期。构造后的 Service 与 key 副本保持只读，可由多个请求
// 并发使用；key 不会传给 Store、placement reader 或日志。
func NewService(store Store, placements PlacementReader, clock Clock, derivationKey []byte, policy Policy) (*Service, error) {
	if store == nil || placements == nil || clock == nil || len(derivationKey) < minimumDerivationKeyBytes || policy.maximumLifetime <= 0 {
		return nil, errors.New("world admission service dependencies are incomplete")
	}
	return &Service{store: store, placements: placements, clock: clock, derivationKey: append([]byte(nil), derivationKey...), policy: policy}, nil
}

// IssueResult 只在确定性签发成功时拥有raw credential与binding。
type IssueResult struct {
	// credential 只允许协议 adapter 在最短作用域编码。
	credential Credential
	// binding 是后续内部校验使用的受信结果，不得直接编码给客户端。
	binding Binding
	// replayed 标识结果是否来自首次提交重放。
	replayed bool
}

// Credential 返回协议 adapter 唯一可编码的 raw bearer；调用方不得记录或持久化。
func (result IssueResult) Credential() Credential { return result.credential }

// Binding 返回包含内部 assignment 与授权事实的值副本；调用方只能生成显式安全投影。
func (result IssueResult) Binding() Binding { return result.binding }

// Replayed 报告结果是否来自相同issuance identity的首次结果重放。
func (result IssueResult) Replayed() bool { return result.replayed }

// Valid 只报告结果字段结构完整；调用方仍只能从 Issue 的无错误返回取得该结果。
func (result IssueResult) Valid() bool { return result.credential.Valid() && result.binding.Valid() }

// String 防止默认格式化递归泄漏credential。
func (IssueResult) String() string { return admissionPlaceholder }

// GoString 防止 `%#v` 展开raw credential。
func (IssueResult) GoString() string { return admissionPlaceholder }

// Issue 验证 current assignment 与全部 deadline 后幂等签发 opaque credential。
//
// issueID 必须来自 application 已验证的幂等键，binding 必须完全由 AuthContext、领域 owner
// 与 placement 事实派生。方法读取 current assignment，并通过 Store 原子创建或重放 Redis
// issuance/credential records；相同 issueID、相同授权事实且未收紧权威deadline时重放首次
// credential与较短binding。改变授权事实或收紧deadline返回幂等冲突，重试时钟推进不能延长资格。
//
// ctx 取消或 Redis 错误不能证明 mutation 未提交，此时返回 ErrorCodeCommitUnknown 且绝不返回
// raw credential。调用方只能使用相同 issueID 与完全相同的 binding 重试解析首次结果。
func (service *Service) Issue(ctx context.Context, issueID IssueID, binding Binding) (IssueResult, error) {
	const operation = "issue"
	if service == nil || !issueID.Valid() || !binding.Valid() {
		return IssueResult{}, operationError(operation, ErrorCodeInvalidArgument, nil)
	}
	now := canonicalTime(service.clock.Now())
	if now.IsZero() || binding.IssuedAt().After(now) || !now.Before(binding.ExpiresAt()) || binding.ExpiresAt().Sub(binding.IssuedAt()) > service.policy.maximumLifetime {
		return IssueResult{}, operationError(operation, ErrorCodeInvalidArgument, nil)
	}
	if err := service.verifyCurrentAssignment(ctx, binding, now); err != nil {
		return IssueResult{}, err
	}
	if existing, outcome, resolveErr := service.store.ResolveIssue(ctx, issueID); resolveErr != nil {
		return IssueResult{}, operationError(operation, ErrorCodeDependency, resolveErr)
	} else if outcome == IssueResolveOutcomeFound {
		return service.replayIssue(issueID, binding, existing, now)
	} else if outcome != IssueResolveOutcomeNotFound {
		return IssueResult{}, operationError(operation, ErrorCodeDependencyDefect, nil)
	}
	fingerprint := fingerprintBinding(binding)
	credential, err := service.deriveCredential(issueID, fingerprint)
	if err != nil {
		return IssueResult{}, operationError(operation, ErrorCodeDependencyDefect, err)
	}
	record := IssueRecord{IssueID: issueID, Fingerprint: fingerprint, CredentialDigest: credential.Digest(), Binding: binding, PhysicalExpiresAt: canonicalTime(binding.ExpiresAt().Add(service.policy.replayRetention))}
	outcome, storeErr := service.store.Issue(ctx, record, now)
	if resultErr := validateIssueResult(outcome, storeErr); resultErr != nil {
		return IssueResult{}, operationError(operation, ErrorCodeDependencyDefect, resultErr)
	}
	switch outcome {
	case IssueOutcomeCreated:
		return IssueResult{credential: credential, binding: binding}, nil
	case IssueOutcomeReplay:
		return IssueResult{credential: credential, binding: binding, replayed: true}, nil
	case IssueOutcomeConsumed:
		return IssueResult{}, operationError(operation, ErrorCodeReplayed, nil)
	case IssueOutcomeIdempotencyConflict:
		existing, resolveOutcome, resolveErr := service.store.ResolveIssue(ctx, issueID)
		if resolveErr != nil {
			return IssueResult{}, operationError(operation, ErrorCodeDependency, resolveErr)
		}
		if resolveOutcome != IssueResolveOutcomeFound {
			return IssueResult{}, operationError(operation, ErrorCodeDependencyDefect, nil)
		}
		return service.replayIssue(issueID, binding, existing, now)
	case IssueOutcomeNotCommitted:
		return IssueResult{}, operationError(operation, ErrorCodeDependency, storeErr)
	case IssueOutcomeCommitUnknown:
		return IssueResult{}, operationError(operation, ErrorCodeCommitUnknown, storeErr)
	default:
		return IssueResult{}, operationError(operation, ErrorCodeDependencyDefect, nil)
	}
}

// replayIssue 对比不随重试时钟变化的授权事实，并返回首次较短binding与credential。
//
// 候选deadline早于首次deadline表示权威上限已经收紧，必须冲突；普通稍后重试得到的
// now+TTL更晚，只重放首次更短结果，不延长已签发资格。
func (service *Service) replayIssue(issueID IssueID, candidate Binding, existing IssueSnapshot, now time.Time) (IssueResult, error) {
	const operation = "issue"
	if !existing.Valid() || !sameBindingAuthority(candidate, existing.Binding) || candidate.ExpiresAt().Before(existing.Binding.ExpiresAt()) {
		return IssueResult{}, operationError(operation, ErrorCodeIdempotencyConflict, nil)
	}
	if existing.Consumed {
		return IssueResult{}, operationError(operation, ErrorCodeReplayed, nil)
	}
	if !now.Before(existing.Binding.ExpiresAt()) {
		return IssueResult{}, operationError(operation, ErrorCodeExpired, nil)
	}
	credential, err := service.deriveCredential(issueID, existing.Fingerprint)
	if err != nil || !credential.Digest().Equal(existing.CredentialDigest) {
		return IssueResult{}, operationError(operation, ErrorCodeDependencyDefect, err)
	}
	return IssueResult{credential: credential, binding: existing.Binding, replayed: true}, nil
}

// sameBindingAuthority 比较除服务端签发窗口外全部不可变授权事实。
func sameBindingAuthority(left Binding, right Binding) bool {
	return left.PlayerID() == right.PlayerID() && left.SessionID() == right.SessionID() && left.Epoch() == right.Epoch() &&
		left.Role() == right.Role() && left.WorldID() == right.WorldID() && left.VisitSessionID() == right.VisitSessionID() &&
		left.Purpose() == right.Purpose() && left.Assignment().Equal(right.Assignment()) && left.Endpoint().Equal(right.Endpoint())
}

// Verify 原子消费静态 binding，随后复核 current full assignment 并返回只读资格。
//
// auth 必须来自已消费 ConnectionTicket，endpoint 必须是实际接收连接的 TLS/TCP listener
// identity。Store 先在线性化点比较静态授权事实并写入首次 consume identity，然后本方法读取
// placement owner 的 current assignment；placement stale 时 credential 已被烧毁，不会恢复可用。
//
// Redis mutation 或 ctx 取消导致提交状态未知时返回 ErrorCodeCommitUnknown。Redis 已消费但
// placement 暂时失败时，调用方只能复用同一 consumeID、AuthContext、endpoint 与 purpose 重试；
// 不同 consumeID 会稳定返回 replayed，不能绕过一次性限制。
func (service *Service) Verify(ctx context.Context, credential Credential, consumeID ConsumeID, auth session.AuthContext, endpoint session.Endpoint, purpose Purpose) (Qualification, error) {
	const operation = "verify"
	if service == nil || !credential.Valid() || !consumeID.Valid() || !auth.Valid() || !endpoint.Valid() || !purpose.Valid() {
		return Qualification{}, operationError(operation, ErrorCodeInvalidArgument, nil)
	}
	if auth.Channel() != session.ChannelTLSTCP || !auth.HasScope(session.ScopeGameplay) || endpoint.Channel() != session.ChannelTLSTCP {
		return Qualification{}, operationError(operation, ErrorCodeInvalid, nil)
	}
	now := canonicalTime(service.clock.Now())
	fingerprint, err := fingerprintConsume(consumeID, auth, endpoint, purpose)
	if err != nil {
		return Qualification{}, operationError(operation, ErrorCodeInvalidArgument, err)
	}
	request := ConsumeRequest{CredentialDigest: credential.Digest(), ConsumeID: consumeID, Fingerprint: fingerprint, Auth: auth, Endpoint: endpoint, Purpose: purpose, ObservedAt: now}
	binding, outcome, storeErr := service.store.Consume(ctx, request)
	if resultErr := validateConsumeResult(binding, outcome, storeErr); resultErr != nil {
		return Qualification{}, operationError(operation, ErrorCodeDependencyDefect, resultErr)
	}
	switch outcome {
	case ConsumeOutcomeApplied, ConsumeOutcomeReplay:
		// 继续复核placement；Replay用于同一consume identity解析response-loss。
	case ConsumeOutcomeNotFound, ConsumeOutcomeBindingMismatch:
		return Qualification{}, operationError(operation, ErrorCodeInvalid, nil)
	case ConsumeOutcomeExpired:
		return Qualification{}, operationError(operation, ErrorCodeExpired, nil)
	case ConsumeOutcomeReplayed:
		return Qualification{}, operationError(operation, ErrorCodeReplayed, nil)
	case ConsumeOutcomeNotCommitted:
		return Qualification{}, operationError(operation, ErrorCodeDependency, storeErr)
	case ConsumeOutcomeCommitUnknown:
		return Qualification{}, operationError(operation, ErrorCodeCommitUnknown, storeErr)
	default:
		return Qualification{}, operationError(operation, ErrorCodeDependencyDefect, nil)
	}
	if !binding.ExpiresAt().After(now) {
		return Qualification{}, operationError(operation, ErrorCodeExpired, nil)
	}
	if !bindingMatchesConsumeRequest(binding, request) {
		return Qualification{}, operationError(operation, ErrorCodeDependencyDefect, nil)
	}
	if err := service.verifyCurrentAssignment(ctx, binding, now); err != nil {
		return Qualification{}, err
	}
	qualification, err := newQualification(binding)
	if err != nil {
		return Qualification{}, operationError(operation, ErrorCodeDependencyDefect, err)
	}
	return qualification, nil
}

// bindingMatchesConsumeRequest 防止缺陷 adapter 把其他连接的有效 binding 提升为资格。
func bindingMatchesConsumeRequest(binding Binding, request ConsumeRequest) bool {
	return binding.PlayerID().String() == request.Auth.Principal().PlayerID() &&
		binding.SessionID() == request.Auth.SessionID() &&
		binding.Epoch() == request.Auth.Epoch() &&
		binding.Purpose() == request.Purpose &&
		binding.Endpoint().Equal(request.Endpoint)
}

// verifyCurrentAssignment 要求active、未过期且完整stamp相等。
func (service *Service) verifyCurrentAssignment(ctx context.Context, binding Binding, observedAt time.Time) error {
	snapshot, outcome, err := service.placements.Resolve(ctx, binding.WorldID(), observedAt)
	if err != nil {
		return operationError("placement", ErrorCodeDependency, err)
	}
	if outcome == placement.ResolveOutcomeNotFound && !snapshot.Valid() {
		return operationError("placement", ErrorCodeStaleAssignment, nil)
	}
	if outcome != placement.ResolveOutcomeFound || !snapshot.Valid() || snapshot.WorldID() != binding.WorldID() {
		return operationError("placement", ErrorCodeDependencyDefect, nil)
	}
	if snapshot.Phase() != placement.PhaseActive || !snapshot.ValidAt(observedAt) || !snapshot.Stamp().Equal(binding.Assignment()) || binding.ExpiresAt().After(snapshot.Lease().ExpiresAt()) {
		return operationError("placement", ErrorCodeStaleAssignment, nil)
	}
	return nil
}

// deriveCredential 使用domain-separated HMAC稳定推导response-loss可重放的opaque值。
func (service *Service) deriveCredential(issueID IssueID, fingerprint Digest) (Credential, error) {
	mac := hmac.New(sha256.New, service.derivationKey)
	writeString(mac, credentialDomainSeparator)
	writeString(mac, issueID.Value())
	bytes := fingerprint.Bytes()
	writeBytes(mac, bytes[:])
	return ParseCredential(credentialPrefix + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
}

// fingerprintBinding 对全部授权事实做无歧义长度前缀编码。
func fingerprintBinding(binding Binding) Digest {
	hasher := sha256.New()
	writeString(hasher, binding.PlayerID().String())
	writeString(hasher, binding.SessionID().String())
	writeUint64(hasher, uint64(binding.Epoch()))
	writeUint64(hasher, uint64(binding.Role()))
	writeString(hasher, binding.WorldID().String())
	writeString(hasher, binding.VisitSessionID().Value())
	writeUint64(hasher, uint64(binding.Purpose()))
	writeAssignment(hasher, binding.Assignment())
	writeEndpoint(hasher, binding.Endpoint())
	writeInt64(hasher, binding.IssuedAt().UnixMicro())
	writeInt64(hasher, binding.ExpiresAt().UnixMicro())
	return digestFromHasher(hasher)
}

// fingerprintConsume 固定首次consume identity与全部静态连接事实。
func fingerprintConsume(consumeID ConsumeID, auth session.AuthContext, endpoint session.Endpoint, purpose Purpose) (Digest, error) {
	playerID, err := account.NewPlayerID(auth.Principal().PlayerID())
	if err != nil {
		return Digest{}, err
	}
	hasher := sha256.New()
	writeString(hasher, consumeID.Value())
	writeString(hasher, playerID.String())
	writeString(hasher, auth.SessionID().String())
	writeUint64(hasher, uint64(auth.Epoch()))
	writeEndpoint(hasher, endpoint)
	writeUint64(hasher, uint64(purpose))
	return digestFromHasher(hasher), nil
}

// writeAssignment 编码full stamp，包含不可公开node/fence字段。
func writeAssignment(hasher hash.Hash, stamp placement.AssignmentStamp) {
	writeString(hasher, stamp.WorldID().String())
	writeString(hasher, stamp.InstanceID().String())
	writeString(hasher, stamp.NodeID().String())
	writeUint64(hasher, uint64(stamp.Generation()))
	writeUint64(hasher, uint64(stamp.FencingToken()))
}

// writeEndpoint 编码唯一TLS/TCP目标。
func writeEndpoint(hasher hash.Hash, endpoint session.Endpoint) {
	writeUint64(hasher, uint64(endpoint.Channel()))
	writeString(hasher, endpoint.Host())
	writeUint64(hasher, uint64(endpoint.Port()))
}

// writeString 使用bytes长度前缀消除拼接歧义。
func writeString(hasher hash.Hash, value string) { writeBytes(hasher, []byte(value)) }

// writeBytes 使用uint64大端长度后写入原始bytes。
func writeBytes(hasher hash.Hash, value []byte) {
	writeUint64(hasher, uint64(len(value)))
	// hash.Hash 的 Write 契约始终接收全部输入并返回 nil error。
	_, _ = hasher.Write(value)
}

// writeUint64 使用固定大端编码整数。
func writeUint64(hasher hash.Hash, value uint64) {
	var buffer [8]byte
	binary.BigEndian.PutUint64(buffer[:], value)
	// hash.Hash 的 Write 契约始终接收全部输入并返回 nil error。
	_, _ = hasher.Write(buffer[:])
}

// writeInt64 保留有符号Unix微秒bit pattern。
func writeInt64(hasher hash.Hash, value int64) { writeUint64(hasher, uint64(value)) }

// digestFromHasher 复制固定SHA-256输出。
func digestFromHasher(hasher hash.Hash) Digest {
	var value [sha256.Size]byte
	copy(value[:], hasher.Sum(nil))
	return Digest{value: value}
}
