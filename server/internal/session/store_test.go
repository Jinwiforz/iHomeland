package session

import (
	"context"
	"errors"
	"sync"
	"time"
)

// testRefreshRecord 保存 refresh lineage、上一枚 access 与 replay tombstone。
type testRefreshRecord struct {
	// token 是当前或已消费 refresh 的绑定事实。
	token TokenRecord
	// access 是本次 refresh 成功时必须撤销的上一枚 access。
	access Digest
	// consumed 使并发 compare-and-consume 至多成功一次。
	consumed bool
	// replay 保存第一次重放已经提交的幂等失效事实。
	replay Invalidation
}

// testTicketRecord 保存一次性 ticket 与消费状态。
type testTicketRecord struct {
	// ticket 包含 channel、endpoint、scope 与 epoch 绑定。
	ticket TicketRecord
	// consumed 防止第二个连接获得 AuthContext。
	consumed bool
}

// testSessionStore 是只存在于测试二进制的并发安全参考模型。
//
// 它用于验证 SessionStore 原子契约，不是 Redis 行为或生产持久化实现。
type testSessionStore struct {
	// mutex 让所有安全状态迁移具有单一线性化点。
	mutex sync.Mutex
	// sessions 保存当前 epoch 与永久 invalidated 状态。
	sessions map[SessionID]SessionRecord
	// access 保存尚未被 refresh rotation 主动撤销的 access records。
	access map[Digest]TokenRecord
	// refresh 保留到 session expiry 的 active records 与 tombstones。
	refresh map[Digest]*testRefreshRecord
	// tickets 保存 active 与 consumed 一次性 records。
	tickets map[Digest]*testTicketRecord
	// invalidations 保存重复失效通知所需的最后提交事实。
	invalidations map[SessionID]Invalidation
	// failure 模拟原子 adapter 在提交前不可用。
	failure error
}

// newTestSessionStore 创建空参考模型；调用方不能从非测试代码引用它。
func newTestSessionStore() *testSessionStore {
	return &testSessionStore{
		sessions:      make(map[SessionID]SessionRecord),
		access:        make(map[Digest]TokenRecord),
		refresh:       make(map[Digest]*testRefreshRecord),
		tickets:       make(map[Digest]*testTicketRecord),
		invalidations: make(map[SessionID]Invalidation),
	}
}

// Create 原子写入完整 bundle，并拒绝任何 identifier 或 digest 冲突。
func (store *testSessionStore) Create(_ context.Context, bundle SessionBundle) (StoreOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.failure != nil {
		return StoreOutcomeUnspecified, store.failure
	}
	if _, exists := store.sessions[bundle.Session.ID]; exists {
		return StoreOutcomeConflict, nil
	}
	if _, exists := store.access[bundle.Access.Digest]; exists {
		return StoreOutcomeConflict, nil
	}
	if _, exists := store.refresh[bundle.Refresh.Digest]; exists {
		return StoreOutcomeConflict, nil
	}
	if !validBundle(bundle) {
		return StoreOutcomeUnspecified, errors.New("invalid session bundle")
	}
	store.sessions[bundle.Session.ID] = bundle.Session
	store.access[bundle.Access.Digest] = bundle.Access
	store.refresh[bundle.Refresh.Digest] = &testRefreshRecord{token: bundle.Refresh, access: bundle.Access.Digest}
	return StoreOutcomeApplied, nil
}

// ResolveAccess 在线性化点同时读取 token 与当前 session 事实。
func (store *testSessionStore) ResolveAccess(_ context.Context, digest Digest, now time.Time) (AuthSnapshot, StoreOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.failure != nil {
		return AuthSnapshot{}, StoreOutcomeUnspecified, store.failure
	}
	token, exists := store.access[digest]
	if !exists {
		return AuthSnapshot{}, StoreOutcomeNotFound, nil
	}
	if !now.Before(token.ExpiresAt) {
		return AuthSnapshot{}, StoreOutcomeExpired, nil
	}
	return store.resolveSession(token.SessionID, token.Epoch, now)
}

// RotateRefresh 在同一锁内处理 expiry、replay invalidation 与新 token pair。
func (store *testSessionStore) RotateRefresh(_ context.Context, rotation Rotation) (AuthSnapshot, Invalidation, StoreOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.failure != nil {
		return AuthSnapshot{}, Invalidation{}, StoreOutcomeUnspecified, store.failure
	}
	entry, exists := store.refresh[rotation.PresentedRefresh]
	if !exists {
		return AuthSnapshot{}, Invalidation{}, StoreOutcomeNotFound, nil
	}
	if !rotation.Now.Before(entry.token.ExpiresAt) {
		return AuthSnapshot{}, Invalidation{}, StoreOutcomeExpired, nil
	}
	if entry.consumed {
		if entry.replay.SessionID.Valid() {
			return AuthSnapshot{}, entry.replay, StoreOutcomeReplayed, nil
		}
		invalidation, outcome, err := store.invalidateSession(entry.token.SessionID, InvalidationReasonRefreshReplay)
		if err != nil {
			return AuthSnapshot{}, Invalidation{}, StoreOutcomeUnspecified, err
		}
		entry.replay = invalidation
		return AuthSnapshot{}, invalidation, replayOutcome(outcome), nil
	}
	snapshot, outcome, err := store.resolveSession(entry.token.SessionID, entry.token.Epoch, rotation.Now)
	if outcome != StoreOutcomeApplied || err != nil {
		return AuthSnapshot{}, Invalidation{}, outcome, err
	}
	if !rotation.Access.Digest.Valid() || !rotation.Refresh.Digest.Valid() || rotation.Access.Digest == rotation.Refresh.Digest {
		return AuthSnapshot{}, Invalidation{}, StoreOutcomeUnspecified, errors.New("invalid rotation digests")
	}
	if _, exists := store.access[rotation.Access.Digest]; exists {
		return AuthSnapshot{}, Invalidation{}, StoreOutcomeConflict, nil
	}
	if _, exists := store.refresh[rotation.Refresh.Digest]; exists {
		return AuthSnapshot{}, Invalidation{}, StoreOutcomeConflict, nil
	}
	entry.consumed = true
	delete(store.access, entry.access)
	sessionExpiry := store.sessions[snapshot.SessionID].ExpiresAt
	rotation.Access.ExpiresAt = earlierTime(rotation.Access.ExpiresAt, sessionExpiry)
	rotation.Refresh.ExpiresAt = earlierTime(rotation.Refresh.ExpiresAt, sessionExpiry)
	rotation.Access.SessionID = snapshot.SessionID
	rotation.Access.Epoch = snapshot.Epoch
	rotation.Refresh.SessionID = snapshot.SessionID
	rotation.Refresh.Epoch = snapshot.Epoch
	store.access[rotation.Access.Digest] = rotation.Access
	store.refresh[rotation.Refresh.Digest] = &testRefreshRecord{token: rotation.Refresh, access: rotation.Access.Digest}
	snapshot.AccessExpiresAt = rotation.Access.ExpiresAt
	snapshot.RefreshExpiresAt = rotation.Refresh.ExpiresAt
	return snapshot, Invalidation{}, StoreOutcomeApplied, nil
}

// IssueTicket 只在 session 当前有效且 epoch 匹配时保存 ticket。
func (store *testSessionStore) IssueTicket(_ context.Context, record TicketRecord, now time.Time) (StoreOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.failure != nil {
		return StoreOutcomeUnspecified, store.failure
	}
	if _, exists := store.tickets[record.Digest]; exists {
		return StoreOutcomeConflict, nil
	}
	_, outcome, err := store.resolveSession(record.SessionID, record.Epoch, now)
	if outcome != StoreOutcomeApplied || err != nil {
		return outcome, err
	}
	if !record.Digest.Valid() || !record.Endpoint.Valid() || record.Scopes.Len() == 0 || !now.Before(record.ExpiresAt) {
		return StoreOutcomeUnspecified, errors.New("invalid ticket record")
	}
	store.tickets[record.Digest] = &testTicketRecord{ticket: record}
	return StoreOutcomeApplied, nil
}

// ConsumeTicket 在全部绑定通过后才标记 consumed，错误 listener 不会烧毁合法 ticket。
func (store *testSessionStore) ConsumeTicket(_ context.Context, digest Digest, channel Channel, endpoint Endpoint, now time.Time) (AuthSnapshot, StoreOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.failure != nil {
		return AuthSnapshot{}, StoreOutcomeUnspecified, store.failure
	}
	entry, exists := store.tickets[digest]
	if !exists {
		return AuthSnapshot{}, StoreOutcomeNotFound, nil
	}
	if entry.consumed {
		return AuthSnapshot{}, StoreOutcomeReplayed, nil
	}
	if !now.Before(entry.ticket.ExpiresAt) {
		return AuthSnapshot{}, StoreOutcomeExpired, nil
	}
	if entry.ticket.Channel != channel || !entry.ticket.Endpoint.Equal(endpoint) {
		return AuthSnapshot{}, StoreOutcomeNotFound, nil
	}
	snapshot, outcome, err := store.resolveSession(entry.ticket.SessionID, entry.ticket.Epoch, now)
	if outcome != StoreOutcomeApplied || err != nil {
		return AuthSnapshot{}, outcome, err
	}
	entry.consumed = true
	snapshot.Scopes = entry.ticket.Scopes
	return snapshot, StoreOutcomeApplied, nil
}

// InvalidateSession 原子提交新 epoch；重复调用返回同一事实而不继续递增。
func (store *testSessionStore) InvalidateSession(_ context.Context, id SessionID, reason InvalidationReason) (Invalidation, StoreOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.failure != nil {
		return Invalidation{}, StoreOutcomeUnspecified, store.failure
	}
	return store.invalidateSession(id, reason)
}

// InvalidatePrincipal 在单一临界区撤销 principal 当前全部 active sessions。
func (store *testSessionStore) InvalidatePrincipal(_ context.Context, principal Principal, reason InvalidationReason) ([]Invalidation, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.failure != nil {
		return nil, store.failure
	}
	result := make([]Invalidation, 0)
	for id, record := range store.sessions {
		if record.Principal == principal {
			invalidation, outcome, err := store.invalidateSession(id, reason)
			if err != nil {
				return nil, err
			}
			if outcome == StoreOutcomeApplied || outcome == StoreOutcomeInvalidated {
				result = append(result, invalidation)
			}
		}
	}
	return result, nil
}

// resolveSession 在调用方持锁时统一校验 status、expiry 与 epoch。
func (store *testSessionStore) resolveSession(id SessionID, epoch Epoch, now time.Time) (AuthSnapshot, StoreOutcome, error) {
	record, exists := store.sessions[id]
	if !exists {
		return AuthSnapshot{}, StoreOutcomeNotFound, nil
	}
	if !now.Before(record.ExpiresAt) {
		return AuthSnapshot{}, StoreOutcomeExpired, nil
	}
	if record.Status != StatusActive {
		return AuthSnapshot{}, StoreOutcomeInvalidated, nil
	}
	if record.Epoch != epoch {
		return AuthSnapshot{}, StoreOutcomeEpochMismatch, nil
	}
	return AuthSnapshot{Principal: record.Principal, SessionID: id, Epoch: record.Epoch}, StoreOutcomeApplied, nil
}

// invalidateSession 在调用方持锁时递增 epoch 并保留幂等通知事实。
func (store *testSessionStore) invalidateSession(id SessionID, reason InvalidationReason) (Invalidation, StoreOutcome, error) {
	record, exists := store.sessions[id]
	if !exists {
		return Invalidation{}, StoreOutcomeNotFound, nil
	}
	if record.Status == StatusInvalidated {
		return store.invalidations[id], StoreOutcomeInvalidated, nil
	}
	next, err := record.Epoch.Next()
	if err != nil {
		return Invalidation{}, StoreOutcomeUnspecified, err
	}
	record.Epoch = next
	record.Status = StatusInvalidated
	store.sessions[id] = record
	invalidation := Invalidation{SessionID: id, Epoch: next, Reason: reason}
	store.invalidations[id] = invalidation
	return invalidation, StoreOutcomeApplied, nil
}

// validBundle 验证参考 store 不会掩盖 application service 构造错误。
func validBundle(bundle SessionBundle) bool {
	return bundle.Session.ID.Valid() && bundle.Session.Principal.Valid() && bundle.Session.Epoch == InitialEpoch &&
		bundle.Session.Status == StatusActive && bundle.Session.ID == bundle.Access.SessionID &&
		bundle.Session.ID == bundle.Refresh.SessionID && bundle.Access.Epoch == bundle.Session.Epoch &&
		bundle.Refresh.Epoch == bundle.Session.Epoch && bundle.Access.Digest.Valid() && bundle.Refresh.Digest.Valid() &&
		bundle.Access.Digest != bundle.Refresh.Digest && bundle.Access.ExpiresAt.Before(bundle.Refresh.ExpiresAt) &&
		!bundle.Refresh.ExpiresAt.After(bundle.Session.ExpiresAt)
}

// replayOutcome 将首次或重复 replay 都收敛为相同外部结果。
func replayOutcome(outcome StoreOutcome) StoreOutcome {
	if outcome == StoreOutcomeApplied || outcome == StoreOutcomeInvalidated {
		return StoreOutcomeReplayed
	}
	return outcome
}

// earlierTime 把轮换 token expiry 限制在原 session 总寿命内。
func earlierTime(left time.Time, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}
