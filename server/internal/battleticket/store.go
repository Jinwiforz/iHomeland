package battleticket

import (
	"context"
	"errors"
	"time"
)

// IssueRecord 是 Redis 原子创建 digest/handle-only issuance 所需的完整值。
type IssueRecord struct {
	// Binding 保存首次冻结的 target/session facts 与 ticket ID。
	Binding Binding
	// Fingerprint 绑定全部签发语义。
	Fingerprint Digest
	// SecretDigest 校验 deterministic response-loss secret，不保存 raw secret。
	SecretDigest Digest
	// ProofDigest 校验 exact child install material，不保存 raw proof key。
	ProofDigest Digest
	// PhysicalExpiresAt 是业务 expiry 加有界 replay retention 的 UTC 微秒 deadline。
	PhysicalExpiresAt time.Time
}

// Valid 报告 record 完整且 digest 与 binding 一致。
func (record IssueRecord) Valid() bool {
	if !record.Binding.Valid() || !record.Fingerprint.Valid() ||
		!record.SecretDigest.Valid() || !record.ProofDigest.Valid() ||
		record.PhysicalExpiresAt.IsZero() ||
		record.PhysicalExpiresAt.Before(record.Binding.facts.ExpiresAt) {
		return false
	}
	return record.Fingerprint.Equal(record.Binding.Fingerprint())
}

// IssueSnapshot 是按 IssueID 解析到的首次 digest/handle-only 签发事实。
type IssueSnapshot struct {
	// Binding 保存首次冻结且后续不可延长的 facts 与 ticket ID。
	Binding Binding
	// Fingerprint 是完整 binding digest。
	Fingerprint Digest
	// SecretDigest 校验重新确定性派生的 HTTPS credential。
	SecretDigest Digest
	// ProofDigest 校验重新确定性派生的 private control material。
	ProofDigest Digest
}

// Valid 报告 snapshot 完整且 binding/fingerprint 一致。
func (snapshot IssueSnapshot) Valid() bool {
	return snapshot.Binding.Valid() && snapshot.Fingerprint.Valid() &&
		snapshot.SecretDigest.Valid() && snapshot.ProofDigest.Valid() &&
		snapshot.Fingerprint.Equal(snapshot.Binding.Fingerprint())
}

// ResolveOutcome 表达按 IssueID 读取首次签发事实的封闭结果。
type ResolveOutcome uint8

const (
	// ResolveOutcomeUnspecified 表示 adapter 没有返回有效决议。
	ResolveOutcomeUnspecified ResolveOutcome = iota
	// ResolveOutcomeFound 表示首次事实仍在有界 replay window。
	ResolveOutcomeFound
	// ResolveOutcomeNotFound 表示 Redis 不再保留该 issuance。
	ResolveOutcomeNotFound
)

// IssueOutcome 表达 issuance Lua mutation 的确定性。
type IssueOutcome uint8

const (
	// IssueOutcomeUnspecified 表示 adapter 未返回有效决议。
	IssueOutcomeUnspecified IssueOutcome = iota
	// IssueOutcomeCreated 表示 digest/handle-only record 已原子创建。
	IssueOutcomeCreated
	// IssueOutcomeReplay 表示相同 identity/semantics 重放首次结果。
	IssueOutcomeReplay
	// IssueOutcomeIdempotencyConflict 表示同一 identity 改变了 binding 或 digest。
	IssueOutcomeIdempotencyConflict
	// IssueOutcomeNotCommitted 表示依赖明确证明 mutation 未提交。
	IssueOutcomeNotCommitted
	// IssueOutcomeCommitUnknown 表示无法证明 mutation 最终状态。
	IssueOutcomeCommitUnknown
)

// Store 由 battleticket 消费侧定义 Redis issuance/replay 边界。
type Store interface {
	// Resolve 按 IssueID 读取首次事实；key miss 不恢复旧资格。
	Resolve(ctx context.Context, issueID IssueID) (IssueSnapshot, ResolveOutcome, error)
	// Issue 原子创建或比较 digest/handle-only record。
	Issue(ctx context.Context, record IssueRecord, observedAt time.Time) (IssueOutcome, error)
}

// ValidateStoreResult 拒绝 adapter 的矛盾 success/error 组合。
func ValidateStoreResult(outcome IssueOutcome, err error) error {
	if err == nil && (outcome == IssueOutcomeCreated || outcome == IssueOutcomeReplay ||
		outcome == IssueOutcomeIdempotencyConflict) {
		return nil
	}
	if err != nil && (outcome == IssueOutcomeNotCommitted || outcome == IssueOutcomeCommitUnknown) {
		return nil
	}
	return errors.New("battle ticket issue store result is inconsistent")
}
