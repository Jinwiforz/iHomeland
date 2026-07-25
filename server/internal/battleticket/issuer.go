package battleticket

import (
	"context"
	"errors"
	"time"
)

// IssueResult 是 Redis 已确认创建或精确重放后的 deterministic ticket material。
//
// Material 同时包含 HTTPS secret 与后续 private control install 所需 proof key；调用方必须
// 先完成 exact child install，才能把 Secret 返回公开边界。
type IssueResult struct {
	// material 保存首次冻结语义重新确定性派生的完整材料。
	material Material
	// replayed 表示本次调用返回了既有 issuance，而非创建新记录。
	replayed bool
}

// Material 返回只供 application 分发到 control/HTTPS 边界的值副本。
func (result IssueResult) Material() Material { return result.material }

// Replayed 报告结果是否来自首次冻结 issuance 的幂等重放。
func (result IssueResult) Replayed() bool { return result.replayed }

// Valid 报告结果是否包含可验证的完整 material。
func (result IssueResult) Valid() bool { return result.material.Valid() }

// String 防止默认格式化泄漏 credential、proof key 或 binding。
func (IssueResult) String() string { return redactedValue }

// GoString 防止 `%#v` 展开签发结果。
func (IssueResult) GoString() string { return redactedValue }

// Issuer 编排 deterministic derivation 与 digest/handle-only Redis issuance。
//
// 它不拥有 role、target、capacity、endpoint 或 child install；这些事实必须由 application
// 在调用前收集，且公开成功仍取决于后续 exact child install。
type Issuer struct {
	// store 是唯一 issuance/replay owner。
	store Store
	// deriver 持有不进入 Redis 的 server-owned root key。
	deriver *Deriver
	// policy 固定 ticket lifetime 与 replay retention。
	policy Policy
}

// NewIssuer 校验不可缺失的 issuance 依赖。
func NewIssuer(store Store, deriver *Deriver, policy Policy) (*Issuer, error) {
	if store == nil || deriver == nil || !policy.Valid() {
		return nil, operationError("construct", ErrorCodeInvalidArgument, nil)
	}
	return &Issuer{store: store, deriver: deriver, policy: policy}, nil
}

// Issue 创建或精确重放首次 BattleTicket material。
//
// Store commit-unknown 永远不返回 material；调用方只能以同一 IssueID 重试并重新解析，
// 不能把本次候选 secret 当作已签发结果。
func (issuer *Issuer) Issue(ctx context.Context, facts Facts, observedAt time.Time) (IssueResult, error) {
	if issuer == nil || ctx == nil {
		return IssueResult{}, operationError("issue", ErrorCodeInvalidArgument, nil)
	}
	observedAt = canonicalTime(observedAt)
	if err := issuer.policy.ValidateFacts(facts, observedAt); err != nil {
		return IssueResult{}, err
	}
	snapshot, outcome, err := issuer.store.Resolve(ctx, facts.IssueID)
	if err != nil {
		return IssueResult{}, operationError("resolve", ErrorCodeDependency, err)
	}
	switch outcome {
	case ResolveOutcomeFound:
		return issuer.replay(snapshot, facts, observedAt)
	case ResolveOutcomeNotFound:
		if snapshot.Valid() {
			return IssueResult{}, operationError("resolve", ErrorCodeDependencyDefect, nil)
		}
	default:
		return IssueResult{}, operationError("resolve", ErrorCodeDependencyDefect, nil)
	}

	material, err := issuer.deriver.Derive(facts)
	if err != nil {
		return IssueResult{}, err
	}
	physicalExpiresAt, err := issuer.policy.PhysicalExpiresAt(material.Binding())
	if err != nil {
		return IssueResult{}, err
	}
	record := IssueRecord{
		Binding:           material.Binding(),
		Fingerprint:       material.Fingerprint(),
		SecretDigest:      material.Secret().Digest(),
		ProofDigest:       material.ProofKey().Digest(),
		PhysicalExpiresAt: physicalExpiresAt,
	}
	issueOutcome, storeErr := issuer.store.Issue(ctx, record, observedAt)
	if validationErr := ValidateStoreResult(issueOutcome, storeErr); validationErr != nil {
		return IssueResult{}, operationError("issue", ErrorCodeDependencyDefect, validationErr)
	}
	switch issueOutcome {
	case IssueOutcomeCreated:
		return IssueResult{material: material}, nil
	case IssueOutcomeReplay:
		// Owner Lua 只有 exact record 比较相等时才返回 replay，因此候选 material 即首次材料。
		return IssueResult{material: material, replayed: true}, nil
	case IssueOutcomeIdempotencyConflict:
		return IssueResult{}, operationError("issue", ErrorCodeIdempotencyConflict, nil)
	case IssueOutcomeNotCommitted:
		return IssueResult{}, operationError("issue", ErrorCodeDependency, storeErr)
	case IssueOutcomeCommitUnknown:
		return IssueResult{}, operationError("issue", ErrorCodeCommitUnknown, storeErr)
	default:
		return IssueResult{}, operationError("issue", ErrorCodeDependencyDefect, storeErr)
	}
}

// replay 重新派生首次冻结 material 并逐项核对 Redis 中只保存的摘要。
func (issuer *Issuer) replay(snapshot IssueSnapshot, candidate Facts, observedAt time.Time) (IssueResult, error) {
	if !snapshot.Valid() {
		return IssueResult{}, operationError("replay", ErrorCodeDependencyDefect, nil)
	}
	if _, err := issuer.policy.ResolveReplay(snapshot.Binding, candidate, observedAt); err != nil {
		return IssueResult{}, err
	}
	material, err := issuer.deriver.Derive(snapshot.Binding.Facts())
	if err != nil {
		return IssueResult{}, err
	}
	if !material.Binding().Equal(snapshot.Binding) ||
		!material.Fingerprint().Equal(snapshot.Fingerprint) ||
		!material.Secret().Digest().Equal(snapshot.SecretDigest) ||
		!material.ProofKey().Digest().Equal(snapshot.ProofDigest) {
		return IssueResult{}, operationError("replay", ErrorCodeDependencyDefect, errors.New("stored battle ticket digest mismatch"))
	}
	return IssueResult{material: material, replayed: true}, nil
}
