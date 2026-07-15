package worldadmission

import (
	"context"
	"errors"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
)

// Clock 提供可替换且并发安全的受信绝对时间。
type Clock interface {
	// Now 返回当前绝对时间；service 会规范为 UTC 微秒。
	Now() time.Time
}

// PlacementReader 读取 placement owner 的 current assignment，不启动或迁移 runtime。
type PlacementReader interface {
	// Resolve 返回 current assignment；Found必须携带目标world的完整snapshot。
	Resolve(ctx context.Context, worldID personalworld.PersonalWorldID, observedAt time.Time) (placement.AssignmentSnapshot, placement.ResolveOutcome, error)
}

// IssueRecord 是原子创建 issuance/credential Hash 所需的完整值。
type IssueRecord struct {
	// IssueID 是HTTP application提供的稳定幂等identity。
	IssueID IssueID
	// Fingerprint 绑定全部签发语义。
	Fingerprint Digest
	// CredentialDigest 是raw credential的SHA-256索引。
	CredentialDigest Digest
	// Binding 是credential封装的全部受信授权事实。
	Binding Binding
	// PhysicalExpiresAt 是业务expiry加有界replay retention的UTC微秒时刻。
	PhysicalExpiresAt time.Time
}

// IssueSnapshot 是按IssueID解析到的首次签发事实与credential状态。
type IssueSnapshot struct {
	// Fingerprint 是首次签发语义摘要。
	Fingerprint Digest
	// CredentialDigest 是可重新确定性派生credential的校验摘要。
	CredentialDigest Digest
	// Binding 是首次提交且后续不可延长的完整授权事实。
	Binding Binding
	// Consumed 表示credential已被首次连接烧毁。
	Consumed bool
}

// Valid 报告解析结果是否包含完整首次签发事实。
func (snapshot IssueSnapshot) Valid() bool {
	return snapshot.Fingerprint.Valid() && snapshot.CredentialDigest.Valid() && snapshot.Binding.Valid()
}

// IssueResolveOutcome 表达按IssueID读取首次签发事实的封闭结果。
type IssueResolveOutcome uint8

const (
	// IssueResolveOutcomeUnspecified 表示adapter没有返回有效决议。
	IssueResolveOutcomeUnspecified IssueResolveOutcome = iota
	// IssueResolveOutcomeFound 表示首次签发事实仍在有界重放窗口内。
	IssueResolveOutcomeFound
	// IssueResolveOutcomeNotFound 表示该IssueID没有保留的签发事实。
	IssueResolveOutcomeNotFound
)

// Valid 报告 record 是否完整且physical retention不短于业务expiry。
func (record IssueRecord) Valid() bool {
	return record.IssueID.Valid() && record.Fingerprint.Valid() && record.CredentialDigest.Valid() && record.Binding.Valid() && !record.PhysicalExpiresAt.IsZero() && !record.PhysicalExpiresAt.Before(record.Binding.ExpiresAt())
}

// ConsumeRequest 是原子比较静态 binding 与写入首次消费 identity 的输入。
type ConsumeRequest struct {
	// CredentialDigest 是入站raw credential的SHA-256索引。
	CredentialDigest Digest
	// ConsumeID 标识一次可精确重试的验证尝试。
	ConsumeID ConsumeID
	// Fingerprint 绑定consume identity与全部连接事实。
	Fingerprint Digest
	// Auth 是ConnectionTicket已认证的只读连接身份。
	Auth session.AuthContext
	// Endpoint 是实际接收连接的受信TLS/TCP目标。
	Endpoint session.Endpoint
	// Purpose 是当前业务命令要求的唯一用途。
	Purpose Purpose
	// ObservedAt 是本次消费的UTC微秒绝对时间。
	ObservedAt time.Time
}

// Valid 报告 request 是否来自已认证 TLS/TCP gameplay connection。
func (request ConsumeRequest) Valid() bool {
	return request.CredentialDigest.Valid() && request.ConsumeID.Valid() && request.Fingerprint.Valid() && request.Auth.Valid() && request.Auth.Channel() == session.ChannelTLSTCP && request.Auth.HasScope(session.ScopeGameplay) && request.Endpoint.Valid() && request.Endpoint.Channel() == session.ChannelTLSTCP && request.Purpose.Valid() && !request.ObservedAt.IsZero()
}

// IssueOutcome 表达 issuance mutation 的确定性。
type IssueOutcome uint8

const (
	// IssueOutcomeUnspecified 表示adapter未返回有效决议。
	IssueOutcomeUnspecified IssueOutcome = iota
	// IssueOutcomeCreated 表示两类Hash已原子创建。
	IssueOutcomeCreated
	// IssueOutcomeReplay 表示相同identity/语义重放首次结果。
	IssueOutcomeReplay
	// IssueOutcomeConsumed 表示首次credential已经被消费。
	IssueOutcomeConsumed
	// IssueOutcomeIdempotencyConflict 表示相同identity改变语义。
	IssueOutcomeIdempotencyConflict
	// IssueOutcomeNotCommitted 表示依赖明确证明mutation未提交。
	IssueOutcomeNotCommitted
	// IssueOutcomeCommitUnknown 表示无法证明mutation最终状态。
	IssueOutcomeCommitUnknown
)

// ConsumeOutcome 表达 credential consume 的原子决议。
type ConsumeOutcome uint8

const (
	// ConsumeOutcomeUnspecified 表示adapter未返回有效决议。
	ConsumeOutcomeUnspecified ConsumeOutcome = iota
	// ConsumeOutcomeApplied 表示首次静态binding验证与消费已提交。
	ConsumeOutcomeApplied
	// ConsumeOutcomeReplay 表示相同consume identity重放首次binding。
	ConsumeOutcomeReplay
	// ConsumeOutcomeNotFound 表示digest对应credential不存在。
	ConsumeOutcomeNotFound
	// ConsumeOutcomeExpired 表示业务expiry已经到达。
	ConsumeOutcomeExpired
	// ConsumeOutcomeBindingMismatch 表示任一静态连接事实不匹配。
	ConsumeOutcomeBindingMismatch
	// ConsumeOutcomeReplayed 表示不同identity重放已消费credential。
	ConsumeOutcomeReplayed
	// ConsumeOutcomeNotCommitted 表示依赖明确证明mutation未提交。
	ConsumeOutcomeNotCommitted
	// ConsumeOutcomeCommitUnknown 表示无法证明consume最终状态。
	ConsumeOutcomeCommitUnknown
)

// Store 由 worldadmission 消费侧定义原子签发与消费边界。
type Store interface {
	// ResolveIssue 按IssueID读取首次签发事实，使服务端派生时间变化不破坏HTTP精确重放。
	ResolveIssue(ctx context.Context, issueID IssueID) (IssueSnapshot, IssueResolveOutcome, error)
	// Issue 原子创建或重放 issuance/credential records；返回error时outcome必须说明提交确定性。
	Issue(ctx context.Context, record IssueRecord, observedAt time.Time) (IssueOutcome, error)
	// Consume 原子比较静态binding并写入首次consume identity；Applied/Replay必须返回完整Binding。
	Consume(ctx context.Context, request ConsumeRequest) (Binding, ConsumeOutcome, error)
}

// validateIssueResult 拒绝 adapter 的矛盾成功/失败组合。
func validateIssueResult(outcome IssueOutcome, err error) error {
	if err == nil && (outcome == IssueOutcomeCreated || outcome == IssueOutcomeReplay || outcome == IssueOutcomeConsumed || outcome == IssueOutcomeIdempotencyConflict) {
		return nil
	}
	if err != nil && (outcome == IssueOutcomeNotCommitted || outcome == IssueOutcomeCommitUnknown) {
		return nil
	}
	return errors.New("world admission issue store result is inconsistent")
}

// validateConsumeResult 拒绝 adapter 返回部分binding或矛盾outcome。
func validateConsumeResult(binding Binding, outcome ConsumeOutcome, err error) error {
	if err == nil {
		if outcome == ConsumeOutcomeApplied || outcome == ConsumeOutcomeReplay {
			if binding.Valid() {
				return nil
			}
			return errors.New("world admission consume result lacks binding")
		}
		if (outcome == ConsumeOutcomeNotFound || outcome == ConsumeOutcomeExpired || outcome == ConsumeOutcomeBindingMismatch || outcome == ConsumeOutcomeReplayed) && !binding.Valid() {
			return nil
		}
	}
	if err != nil && (outcome == ConsumeOutcomeNotCommitted || outcome == ConsumeOutcomeCommitUnknown) && !binding.Valid() {
		return nil
	}
	return errors.New("world admission consume store result is inconsistent")
}
