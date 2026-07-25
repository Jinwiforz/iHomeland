package battleticket

import "time"

const (
	// maximumTicketLifetime 防止 BattleTicket 被配置成长寿命 bearer。
	maximumTicketLifetime = 2 * time.Minute
	// maximumReplayRetention 限制 response-loss 记录占用 store 的最长时间。
	maximumReplayRetention = 24 * time.Hour
)

// ReplayOutcome 表达相同 issuance identity 的幂等解析结果。
type ReplayOutcome uint8

const (
	// ReplayOutcomeUnspecified 是禁止返回的零值。
	ReplayOutcomeUnspecified ReplayOutcome = iota
	// ReplayOutcomeReturnExisting 表示必须返回首次冻结的 credential/binding/expiry。
	ReplayOutcomeReturnExisting
)

// Policy 固定 BattleTicket lifetime 与 response-loss 证据保留上限。
type Policy struct {
	// maximumLifetime 是 issue 到 expiry 的 hard upper bound。
	maximumLifetime time.Duration
	// replayRetention 是 expiry 后保留 digest/handle-only 幂等记录的时间。
	replayRetention time.Duration
}

// NewPolicy 校验短期 ticket 与有界 replay retention。
func NewPolicy(maximumLifetime time.Duration, replayRetention time.Duration) (Policy, error) {
	if maximumLifetime <= 0 || maximumLifetime > maximumTicketLifetime ||
		replayRetention < maximumLifetime || replayRetention > maximumReplayRetention {
		return Policy{}, operationError("policy", ErrorCodeInvalidArgument, nil)
	}
	return Policy{maximumLifetime: maximumLifetime, replayRetention: replayRetention}, nil
}

// MaximumLifetime 返回签发 policy 的 hard upper bound。
func (policy Policy) MaximumLifetime() time.Duration { return policy.maximumLifetime }

// ReplayRetention 返回 expiry 后幂等证据保留时间。
func (policy Policy) ReplayRetention() time.Duration { return policy.replayRetention }

// Valid 报告 policy 是否经过完整构造。
func (policy Policy) Valid() bool {
	return policy.maximumLifetime > 0 && policy.maximumLifetime <= maximumTicketLifetime &&
		policy.replayRetention >= policy.maximumLifetime &&
		policy.replayRetention <= maximumReplayRetention
}

// ValidateFacts 验证签发时钟、绝对 expiry 与最大 lifetime，不延长上游 session/target deadline。
func (policy Policy) ValidateFacts(facts Facts, observedAt time.Time) error {
	observedAt = canonicalTime(observedAt)
	facts.IssuedAt = canonicalTime(facts.IssuedAt)
	facts.ExpiresAt = canonicalTime(facts.ExpiresAt)
	if !policy.Valid() || !facts.Valid() || observedAt.IsZero() ||
		facts.IssuedAt.After(observedAt) || !observedAt.Before(facts.ExpiresAt) ||
		facts.ExpiresAt.Sub(facts.IssuedAt) > policy.maximumLifetime {
		return operationError("issue", ErrorCodeInvalidArgument, nil)
	}
	return nil
}

// PhysicalExpiresAt 返回 store digest/handle-only record 的有界清理 deadline。
func (policy Policy) PhysicalExpiresAt(binding Binding) (time.Time, error) {
	if !policy.Valid() || !binding.Valid() {
		return time.Time{}, operationError("policy", ErrorCodeInvalidArgument, nil)
	}
	return canonicalTime(binding.facts.ExpiresAt.Add(policy.replayRetention)), nil
}

// ResolveReplay 对比不随重试时钟变化的权威事实并要求重放首次结果。
//
// Candidate expiry 早于首次值表示上游 session/assignment deadline 已收紧，必须冲突；普通
// 稍后重试得到的更晚 deadline 不会延长首次 ticket。返回成功时调用方必须取 existing Material，
// 不能重新派生 candidate 或分配第二个 actor slot。
func (policy Policy) ResolveReplay(existing Binding, candidate Facts, observedAt time.Time) (ReplayOutcome, error) {
	observedAt = canonicalTime(observedAt)
	candidate.IssuedAt = canonicalTime(candidate.IssuedAt)
	candidate.ExpiresAt = canonicalTime(candidate.ExpiresAt)
	if !policy.Valid() || !existing.Valid() || !candidate.Valid() || observedAt.IsZero() {
		return ReplayOutcomeUnspecified, operationError("replay", ErrorCodeInvalidArgument, nil)
	}
	if !factsEqual(existing.facts, candidate, false) ||
		candidate.ExpiresAt.Before(existing.facts.ExpiresAt) {
		return ReplayOutcomeUnspecified, operationError("replay", ErrorCodeIdempotencyConflict, nil)
	}
	if !observedAt.Before(existing.facts.ExpiresAt) {
		return ReplayOutcomeUnspecified, operationError("replay", ErrorCodeExpired, nil)
	}
	return ReplayOutcomeReturnExisting, nil
}
