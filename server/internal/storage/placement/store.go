package placement

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	domain "github.com/jinwiforz/ihomeland/server/internal/placement"
	storagemysql "github.com/jinwiforz/ihomeland/server/internal/storage/mysql"
	storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"
	redisclient "github.com/redis/go-redis/v9"
)

const (
	// minimumReplayTTL 拒绝不能覆盖一次正常 response-loss 重试的退化策略。
	minimumReplayTTL = time.Second
	// maximumReplayTTL 保持 transition evidence 有界，避免 Redis 变成永久审计库。
	maximumReplayTTL = 24 * time.Hour
)

// Observer 接收固定 adapter/operation/outcome，不接收 key、identity、fence 或 client error。
type Observer interface {
	// RecordStorageOperation 记录三个低基数分类字段。
	RecordStorageOperation(string, string, string)
}

// Store 组合 MySQL allocation ledger 与 Redis current state，实现 PlacementStore。
//
// Store 没有后台任务或资源所有权，允许并发调用。新 candidate 必须先提交 MySQL allocation；
// 只有本次新建 allocation 可以首次进入 Redis，既有 allocation 只能解析证据。
type Store struct {
	// allocator 拥有持久 generation/fence 分配语义，但只借用 db。
	allocator allocator
	// client 由 storage/redis Component 持有，本 adapter 不关闭。
	client *redisclient.Client
	// keyspace 由 Composition Root 合并 registry 后创建并保持不可变。
	keyspace *storageredis.Keyspace
	// replayTTL 覆盖调用方有界 retry window，且不永久保存 transition。
	replayTTL time.Duration
	// observer 只接收固定低基数结果。
	observer Observer
}

var _ domain.PlacementStore = (*Store)(nil)

// New 创建不获取网络或数据库资源的 placement adapter。
//
// db、client、keyspace 与 observer 的生命周期仍由 Composition Root 管理；replayTTL 必须
// 由调用方根据 retry window 显式提供，并限制在 1 秒到 24 小时之间。
func New(db *sql.DB, client *redisclient.Client, keyspace *storageredis.Keyspace, replayTTL time.Duration, observer Observer) (*Store, error) {
	if db == nil || client == nil || keyspace == nil || observer == nil {
		return nil, errors.New("placement store requires database, redis client, keyspace, and observer")
	}
	if replayTTL < minimumReplayTTL || replayTTL > maximumReplayTTL {
		return nil, errors.New("placement replay TTL is outside bounded policy")
	}
	for _, definitionName := range []string{assignmentDefinitionName, transitionDefinitionName} {
		if _, err := keyspace.Build(definitionName, "validation"); err != nil {
			return nil, errors.New("placement keyspace is missing required definitions")
		}
	}
	return &Store{allocator: allocator{db: db, withinTx: storagemysql.WithinTx}, client: client, keyspace: keyspace, replayTTL: replayTTL, observer: observer}, nil
}

// Resolve 返回 Redis 中单一完整 current snapshot；过期值若尚物理存在仍可返回给 application。
// 该结果只用于观察与状态收敛，不授予写资格；写入方仍须通过 QualifyWrite 的精确 expiry/stamp 校验。
func (store *Store) Resolve(ctx context.Context, worldID personalworld.PersonalWorldID, observedAt time.Time) (domain.AssignmentSnapshot, domain.ResolveOutcome, error) {
	if !worldID.Valid() || observedAt.IsZero() {
		return domain.AssignmentSnapshot{}, domain.ResolveOutcomeUnspecified, store.failure("resolve", "invalid", errors.New("resolve input is invalid"))
	}
	snapshot, found, err := store.readCurrent(ctx, worldID, observedAt)
	if err != nil {
		return domain.AssignmentSnapshot{}, domain.ResolveOutcomeUnspecified, store.failure("resolve", "failed", err)
	}
	if !found {
		store.observe("resolve", "not_found")
		return domain.AssignmentSnapshot{}, domain.ResolveOutcomeNotFound, nil
	}
	store.observe("resolve", "found")
	return snapshot, domain.ResolveOutcomeFound, nil
}

// Acquire 先解析有效 current，再持久分配 candidate 并由 Lua 原子发布 starting assignment。
func (store *Store) Acquire(ctx context.Context, request domain.AcquireRequest) (domain.AssignmentSnapshot, domain.StoreOutcome, error) {
	if !request.Valid() {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("acquire", "invalid", errors.New("acquire request is invalid"))
	}
	current, found, err := store.readCurrent(ctx, request.Candidate().WorldID(), request.ObservedAt())
	if err != nil {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("acquire", "read_failed", err)
	}
	if found && current.ValidAt(request.ObservedAt()) {
		if current.Phase() == domain.PhaseActive {
			store.observe("acquire", "existing")
			return current, domain.StoreOutcomeExisting, nil
		}
		store.observe("acquire", "in_progress")
		return current, domain.StoreOutcomeInProgress, nil
	}
	observedUS, createdUS, expiresUS, expiresMS, replayMS, err := store.acquireArguments(request)
	if err != nil {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("acquire", "codec_failed", err)
	}
	reserved, status, reserveErr := store.allocator.reserve(ctx, request.Candidate(), request.ObservedAt())
	if reserveErr != nil {
		if status == allocationCommitUnknown {
			return domain.AssignmentSnapshot{}, domain.StoreOutcomeCommitUnknown, store.failure("acquire", "allocation_commit_unknown", reserveErr)
		}
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("acquire", "allocation_not_committed", reserveErr)
	}
	fingerprint := acquireFingerprint(reserved.snapshot)
	if status == allocationExisting {
		return store.resolveExistingAllocation(ctx, "acquire", reserved.snapshot, fingerprint, request.ObservedAt())
	}
	store.observe("allocation", "created")
	assignmentKey, replayKey, err := store.keys(request.Candidate().WorldID(), fingerprint)
	if err != nil {
		store.observe("allocation", "burned")
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("acquire", "key_failed", err)
	}
	stamp := reserved.snapshot.Stamp()
	value, evalErr := store.client.Eval(ctx, acquireScript, []string{assignmentKey.Value(), replayKey.Value()},
		observedUS, stamp.WorldID().String(), stamp.InstanceID().String(), stamp.NodeID().String(),
		canonicalUint(stamp.Generation().Uint64()), canonicalUint(stamp.FencingToken().Uint64()), "starting",
		createdUS, expiresUS, expiresMS, replayMS, fingerprint).Result()
	if evalErr != nil {
		return store.resolveMutationFailure(ctx, "acquire", reserved.snapshot.WorldID(), reserved.snapshot, fingerprint, request.ObservedAt(), evalErr)
	}
	snapshot, outcome, parseErr := store.parseMutation("acquire", value, request.ObservedAt())
	if parseErr != nil {
		store.observe("allocation", "burned")
		return snapshot, outcome, parseErr
	}
	if outcome != domain.StoreOutcomeApplied && outcome != domain.StoreOutcomeReplay && outcome != domain.StoreOutcomeInProgress {
		store.observe("allocation", "burned")
	}
	return snapshot, outcome, nil
}

// Activate 原子发布匹配且未过期的 starting assignment。
func (store *Store) Activate(ctx context.Context, request domain.StampRequest) (domain.AssignmentSnapshot, domain.StoreOutcome, error) {
	if !request.Valid() {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("activate", "invalid", errors.New("activate request is invalid"))
	}
	fingerprint := transitionFingerprint("activate", stampFields(request.Stamp())...)
	return store.runStampMutation(ctx, "activate", activateScript, request, fingerprint)
}

// Renew 原子比较完整 stamp，在旧 lease 有效时严格推进绝对 expiry。
func (store *Store) Renew(ctx context.Context, request domain.RenewRequest) (domain.AssignmentSnapshot, domain.StoreOutcome, error) {
	if !request.Valid() {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("renew", "invalid", errors.New("renew request is invalid"))
	}
	condition := request.Condition()
	observedUS, err := canonicalMicroTime(condition.ObservedAt())
	if err != nil {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("renew", "codec_failed", err)
	}
	expiresUS, err := canonicalMicroTime(request.LeaseExpiresAt())
	if err != nil {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("renew", "codec_failed", err)
	}
	expiresMS, err := expiryMilliseconds(request.LeaseExpiresAt())
	if err != nil {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("renew", "codec_failed", err)
	}
	replayMS, err := replayMilliseconds(store.replayTTL)
	if err != nil {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("renew", "codec_failed", err)
	}
	fingerprint := transitionFingerprint("renew", append(stampFields(condition.Stamp()), expiresUS)...)
	assignmentKey, replayKey, err := store.keys(condition.Stamp().WorldID(), fingerprint)
	if err != nil {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("renew", "key_failed", err)
	}
	stamp := stampFields(condition.Stamp())
	value, evalErr := store.client.Eval(ctx, renewScript, []string{assignmentKey.Value(), replayKey.Value()},
		observedUS, stamp[0], stamp[1], stamp[2], stamp[3], stamp[4], expiresUS, expiresMS, replayMS, fingerprint).Result()
	if evalErr != nil {
		expected, resolveErr := store.expectedRenewSnapshot(ctx, request)
		if resolveErr == nil && expected.Valid() {
			return store.resolveMutationFailure(ctx, "renew", condition.Stamp().WorldID(), expected, fingerprint, condition.ObservedAt(), evalErr)
		}
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeCommitUnknown, store.failure("renew", "commit_unknown", evalErr)
	}
	return store.parseMutation("renew", value, condition.ObservedAt())
}

// Revoke 原子删除完整匹配 current，并保存不误伤 successor 的有界 replay snapshot。
func (store *Store) Revoke(ctx context.Context, request domain.StampRequest) (domain.AssignmentSnapshot, domain.StoreOutcome, error) {
	if !request.Valid() {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("revoke", "invalid", errors.New("revoke request is invalid"))
	}
	fingerprint := transitionFingerprint("revoke", stampFields(request.Stamp())...)
	return store.runStampMutation(ctx, "revoke", revokeScript, request, fingerprint)
}

// Replace 先为 successor 持久分配更高 generation/fence，再由 Lua 原子 cutover current。
func (store *Store) Replace(ctx context.Context, request domain.ReplaceRequest) (domain.AssignmentSnapshot, domain.StoreOutcome, error) {
	if !request.Valid() {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("replace", "invalid", errors.New("replace request is invalid"))
	}
	current, found, err := store.readCurrent(ctx, request.Expected().WorldID(), request.ObservedAt())
	if err != nil {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("replace", "read_failed", err)
	}
	if !found {
		store.observe("replace", "not_found")
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotFound, nil
	}
	if !current.Stamp().Equal(request.Expected()) {
		if snapshotMatchesCandidateIdentity(current, request.Successor()) {
			reserved, allocationFound, lookupErr := store.allocator.lookup(ctx, request.Successor(), request.ObservedAt())
			if lookupErr != nil {
				return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("replace", "allocation_read_failed", lookupErr)
			}
			if !allocationFound || !sameAllocation(current, reserved.snapshot) {
				return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("replace", "dependency_defect", errors.New("current successor has no matching allocation"))
			}
			return store.resolveExistingAllocation(ctx, "replace", reserved.snapshot, replaceFingerprint(request.Expected(), reserved.snapshot), request.ObservedAt())
		}
		store.observe("replace", "conflict")
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeConflict, nil
	}
	observedUS, err := canonicalMicroTime(request.ObservedAt())
	if err != nil {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("replace", "codec_failed", err)
	}
	createdUS, err := canonicalMicroTime(request.Successor().CreatedAt())
	if err != nil {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("replace", "codec_failed", err)
	}
	expiresUS, err := canonicalMicroTime(request.Successor().LeaseExpiresAt())
	if err != nil {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("replace", "codec_failed", err)
	}
	expiresMS, err := expiryMilliseconds(request.Successor().LeaseExpiresAt())
	if err != nil {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("replace", "codec_failed", err)
	}
	replayMS, err := replayMilliseconds(store.replayTTL)
	if err != nil {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("replace", "codec_failed", err)
	}
	reserved, status, reserveErr := store.allocator.reserve(ctx, request.Successor(), request.ObservedAt())
	if reserveErr != nil {
		if status == allocationCommitUnknown {
			return domain.AssignmentSnapshot{}, domain.StoreOutcomeCommitUnknown, store.failure("replace", "allocation_commit_unknown", reserveErr)
		}
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("replace", "allocation_not_committed", reserveErr)
	}
	fingerprint := replaceFingerprint(request.Expected(), reserved.snapshot)
	if status == allocationExisting {
		return store.resolveExistingAllocation(ctx, "replace", reserved.snapshot, fingerprint, request.ObservedAt())
	}
	store.observe("allocation", "created")
	assignmentKey, replayKey, err := store.keys(request.Expected().WorldID(), fingerprint)
	if err != nil {
		store.observe("allocation", "burned")
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure("replace", "key_failed", err)
	}
	predecessor := stampFields(request.Expected())
	successor := stampFields(reserved.snapshot.Stamp())
	value, evalErr := store.client.Eval(ctx, replaceScript, []string{assignmentKey.Value(), replayKey.Value()},
		observedUS, predecessor[0], predecessor[1], predecessor[2], predecessor[3], predecessor[4],
		successor[0], successor[1], successor[2], successor[3], successor[4], createdUS, expiresUS, expiresMS, replayMS, fingerprint).Result()
	if evalErr != nil {
		return store.resolveMutationFailure(ctx, "replace", reserved.snapshot.WorldID(), reserved.snapshot, fingerprint, request.ObservedAt(), evalErr)
	}
	snapshot, outcome, parseErr := store.parseMutation("replace", value, request.ObservedAt())
	if parseErr != nil {
		store.observe("allocation", "burned")
		return snapshot, outcome, parseErr
	}
	if outcome != domain.StoreOutcomeApplied && outcome != domain.StoreOutcomeReplay {
		store.observe("allocation", "burned")
	}
	return snapshot, outcome, nil
}

// QualifyWrite 原子读取 current active/lease/stamp，并返回一次 point-in-time WriteFence。
// WriteFence 不构成长效授权；未来 runtime-originated 持久写仍须由实际 mutation owner 在最终
// MySQL commit boundary 重新验证，不能只依赖本方法曾经成功。
func (store *Store) QualifyWrite(ctx context.Context, request domain.StampRequest) (domain.WriteFence, domain.StoreOutcome, error) {
	if !request.Valid() {
		return domain.WriteFence{}, domain.StoreOutcomeNotCommitted, store.failure("qualify_write", "invalid", errors.New("qualification request is invalid"))
	}
	assignmentKey, err := store.assignmentKey(request.Stamp().WorldID())
	if err != nil {
		return domain.WriteFence{}, domain.StoreOutcomeNotCommitted, store.failure("qualify_write", "key_failed", err)
	}
	observedUS, err := canonicalMicroTime(request.ObservedAt())
	if err != nil {
		return domain.WriteFence{}, domain.StoreOutcomeNotCommitted, store.failure("qualify_write", "codec_failed", err)
	}
	stamp := stampFields(request.Stamp())
	value, evalErr := store.client.Eval(ctx, qualifyWriteScript, []string{assignmentKey.Value()}, observedUS, stamp[0], stamp[1], stamp[2], stamp[3], stamp[4]).Result()
	if evalErr != nil {
		return domain.WriteFence{}, domain.StoreOutcomeNotCommitted, store.failure("qualify_write", "read_failed", evalErr)
	}
	code, snapshot, parseErr := parseScriptReply(value, request.ObservedAt())
	if parseErr != nil {
		return domain.WriteFence{}, domain.StoreOutcomeNotCommitted, store.failure("qualify_write", "defect", parseErr)
	}
	outcome, mapErr := mapStoreOutcome(code)
	if mapErr != nil {
		return domain.WriteFence{}, domain.StoreOutcomeNotCommitted, store.failure("qualify_write", "defect", mapErr)
	}
	if outcome != domain.StoreOutcomeApplied {
		store.observe("qualify_write", outcome.String())
		return domain.WriteFence{}, outcome, nil
	}
	fence, err := domain.NewWriteFence(snapshot.Stamp())
	if err != nil || !snapshot.Stamp().Equal(request.Stamp()) {
		return domain.WriteFence{}, domain.StoreOutcomeNotCommitted, store.failure("qualify_write", "defect", errors.New("qualification result contradicts request"))
	}
	store.observe("qualify_write", "applied")
	return fence, domain.StoreOutcomeApplied, nil
}

// runStampMutation 执行 activate/revoke 共享的完整 stamp script 边界。
func (store *Store) runStampMutation(ctx context.Context, operation string, script string, request domain.StampRequest, fingerprint string) (domain.AssignmentSnapshot, domain.StoreOutcome, error) {
	assignmentKey, replayKey, err := store.keys(request.Stamp().WorldID(), fingerprint)
	if err != nil {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure(operation, "key_failed", err)
	}
	observedUS, err := canonicalMicroTime(request.ObservedAt())
	if err != nil {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure(operation, "codec_failed", err)
	}
	replayMS, err := replayMilliseconds(store.replayTTL)
	if err != nil {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure(operation, "codec_failed", err)
	}
	stamp := stampFields(request.Stamp())
	expected := domain.AssignmentSnapshot{}
	if current, found, readErr := store.readCurrent(ctx, request.Stamp().WorldID(), request.ObservedAt()); readErr == nil && found && current.Stamp().Equal(request.Stamp()) {
		expected = current
		if operation == "activate" && current.Phase() == domain.PhaseStarting {
			if active, activateErr := current.Activate(request.ObservedAt()); activateErr == nil {
				expected = active
			}
		}
	}
	value, evalErr := store.client.Eval(ctx, script, []string{assignmentKey.Value(), replayKey.Value()},
		observedUS, stamp[0], stamp[1], stamp[2], stamp[3], stamp[4], replayMS, fingerprint).Result()
	if evalErr != nil {
		return store.resolveMutationFailure(ctx, operation, request.Stamp().WorldID(), expected, fingerprint, request.ObservedAt(), evalErr)
	}
	return store.parseMutation(operation, value, request.ObservedAt())
}

// parseMutation 把固定 Lua code 映射到现有 StoreOutcome，并验证 snapshot 形状。
func (store *Store) parseMutation(operation string, value any, observedAt time.Time) (domain.AssignmentSnapshot, domain.StoreOutcome, error) {
	code, snapshot, err := parseScriptReply(value, observedAt)
	if err != nil {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeCommitUnknown, store.failure(operation, "defect", err)
	}
	if code == "defect" {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure(operation, "dependency_defect", errors.New("redis state is corrupt"))
	}
	outcome, err := mapStoreOutcome(code)
	if err != nil {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeCommitUnknown, store.failure(operation, "defect", err)
	}
	if successOutcome(outcome) != snapshot.Valid() {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeCommitUnknown, store.failure(operation, "defect", errors.New("script result snapshot contradicts outcome"))
	}
	store.observe(operation, outcome.String())
	return snapshot, outcome, nil
}

// resolveExistingAllocation 只接受 Redis current/replay 精确证据，绝不重新发布旧 allocation。
func (store *Store) resolveExistingAllocation(ctx context.Context, operation string, expected domain.AssignmentSnapshot, fingerprint string, observedAt time.Time) (domain.AssignmentSnapshot, domain.StoreOutcome, error) {
	replay, replayFingerprint, replayFound, replayErr := store.readReplay(ctx, operation, expected.WorldID(), fingerprint, observedAt)
	if replayErr != nil {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeCommitUnknown, store.failure(operation, "replay_read_failed", replayErr)
	}
	if replayFound {
		if replayFingerprint != fingerprint || !sameAllocation(replay, expected) {
			store.observe(operation, "conflict")
			return domain.AssignmentSnapshot{}, domain.StoreOutcomeConflict, nil
		}
		store.observe(operation, "replay")
		return replay, domain.StoreOutcomeReplay, nil
	}
	current, found, currentErr := store.readCurrent(ctx, expected.WorldID(), observedAt)
	if currentErr != nil {
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeCommitUnknown, store.failure(operation, "current_read_failed", currentErr)
	}
	if found {
		if !sameAllocation(current, expected) {
			store.observe(operation, "conflict")
			return domain.AssignmentSnapshot{}, domain.StoreOutcomeConflict, nil
		}
		outcome := domain.StoreOutcomeReplay
		if current.Phase() == domain.PhaseStarting {
			outcome = domain.StoreOutcomeInProgress
		}
		store.observe(operation, outcome.String())
		return current, outcome, nil
	}
	return domain.AssignmentSnapshot{}, domain.StoreOutcomeCommitUnknown, store.failure(operation, "commit_unknown", errors.New("existing allocation has no redis evidence"))
}

// resolveMutationFailure 用相同 current/replay identity 收窄 response loss，其他情况保持 unknown。
func (store *Store) resolveMutationFailure(ctx context.Context, operation string, worldID personalworld.PersonalWorldID, expected domain.AssignmentSnapshot, fingerprint string, observedAt time.Time, cause error) (domain.AssignmentSnapshot, domain.StoreOutcome, error) {
	replay, replayFingerprint, replayFound, replayErr := store.readReplay(ctx, operation, worldID, fingerprint, observedAt)
	if replayErr == nil && replayFound {
		if replayFingerprint == fingerprint && (!expected.Valid() || sameAllocation(replay, expected)) {
			store.observe(operation, "replay")
			return replay, domain.StoreOutcomeReplay, nil
		}
		store.observe(operation, "conflict")
		return domain.AssignmentSnapshot{}, domain.StoreOutcomeConflict, nil
	}
	if expected.Valid() {
		current, found, currentErr := store.readCurrent(ctx, worldID, observedAt)
		if currentErr == nil && found {
			if sameAllocation(current, expected) {
				switch operation {
				case "activate":
					if current.Phase() == domain.PhaseActive {
						store.observe(operation, "replay")
						return current, domain.StoreOutcomeReplay, nil
					}
					return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure(operation, "not_committed", cause)
				case "revoke":
					return domain.AssignmentSnapshot{}, domain.StoreOutcomeNotCommitted, store.failure(operation, "not_committed", cause)
				case "renew":
					if current.Lease().ExpiresAt().Equal(expected.Lease().ExpiresAt()) {
						store.observe(operation, "replay")
						return current, domain.StoreOutcomeReplay, nil
					}
				default:
					outcome := domain.StoreOutcomeReplay
					if operation == "acquire" && current.Phase() == domain.PhaseStarting {
						outcome = domain.StoreOutcomeInProgress
					}
					store.observe(operation, outcome.String())
					return current, outcome, nil
				}
			}
			store.observe(operation, "conflict")
			return domain.AssignmentSnapshot{}, domain.StoreOutcomeConflict, nil
		}
	} else {
		_, found, currentErr := store.readCurrent(ctx, worldID, observedAt)
		if currentErr == nil && found {
			store.observe(operation, "conflict")
			return domain.AssignmentSnapshot{}, domain.StoreOutcomeConflict, nil
		}
	}
	return domain.AssignmentSnapshot{}, domain.StoreOutcomeCommitUnknown, store.failure(operation, "commit_unknown", cause)
}

// expectedRenewSnapshot 读取 response-loss 后可能已推进的新 expiry，供精确证据解析。
func (store *Store) expectedRenewSnapshot(ctx context.Context, request domain.RenewRequest) (domain.AssignmentSnapshot, error) {
	current, found, err := store.readCurrent(ctx, request.Condition().Stamp().WorldID(), request.Condition().ObservedAt())
	if err != nil || !found {
		return domain.AssignmentSnapshot{}, err
	}
	if !current.Stamp().Equal(request.Condition().Stamp()) || !current.Lease().ExpiresAt().Equal(request.LeaseExpiresAt()) {
		return domain.AssignmentSnapshot{}, nil
	}
	return current, nil
}

// readCurrent 读取并严格解码 assignment Hash；空 Hash 是唯一 not-found 表达。
func (store *Store) readCurrent(ctx context.Context, worldID personalworld.PersonalWorldID, observedAt time.Time) (domain.AssignmentSnapshot, bool, error) {
	key, err := store.assignmentKey(worldID)
	if err != nil {
		return domain.AssignmentSnapshot{}, false, err
	}
	fields, found, err := store.readTTLHash(ctx, key)
	if err != nil || !found {
		return domain.AssignmentSnapshot{}, false, err
	}
	if len(fields) == 0 {
		return domain.AssignmentSnapshot{}, false, nil
	}
	snapshot, err := decodeAssignment(fields, observedAt, false)
	if err != nil {
		return domain.AssignmentSnapshot{}, false, formatCodecError("assignment", err)
	}
	if snapshot.WorldID() != worldID {
		return domain.AssignmentSnapshot{}, false, errors.New("placement assignment key and value world differ")
	}
	return snapshot, true, nil
}

// readReplay 读取 transition evidence，并同时返回必须匹配请求的 fingerprint。
func (store *Store) readReplay(ctx context.Context, operation string, worldID personalworld.PersonalWorldID, fingerprint string, observedAt time.Time) (domain.AssignmentSnapshot, string, bool, error) {
	if !worldID.Valid() {
		return domain.AssignmentSnapshot{}, "", false, nil
	}
	_, key, err := store.keys(worldID, fingerprint)
	if err != nil {
		return domain.AssignmentSnapshot{}, "", false, err
	}
	fields, found, err := store.readTTLHash(ctx, key)
	if err != nil || !found {
		return domain.AssignmentSnapshot{}, "", false, err
	}
	if len(fields) == 0 {
		return domain.AssignmentSnapshot{}, "", false, nil
	}
	snapshot, err := decodeAssignment(fields, observedAt, true)
	if err != nil {
		return domain.AssignmentSnapshot{}, "", false, formatCodecError("replay", err)
	}
	if snapshot.WorldID() != worldID {
		return domain.AssignmentSnapshot{}, "", false, errors.New("placement replay key and value world differ")
	}
	if fields["operation"] != operation {
		return domain.AssignmentSnapshot{}, "", false, errors.New("placement replay operation differs from request")
	}
	return snapshot, fields["fingerprint"], true, nil
}

// readTTLHash 读取 TTL-required Hash，并拒绝缺少 expiry 的永久 key。
//
// HGETALL 后 key 自然到期会被解释为 not-found；非空 Hash 若仍存在却没有 TTL，则属于
// dependency defect。该检查只治理物理生命周期，领域 expiry 仍由 value 与 Lua 精确判断。
func (store *Store) readTTLHash(ctx context.Context, key storageredis.Key) (map[string]string, bool, error) {
	fields, err := store.client.HGetAll(ctx, key.Value()).Result()
	if err != nil {
		return nil, false, err
	}
	if len(fields) == 0 {
		return nil, false, nil
	}
	ttl, err := store.client.PTTL(ctx, key.Value()).Result()
	if err != nil {
		return nil, false, err
	}
	if ttl == -2 {
		return nil, false, nil
	}
	if ttl == -1 {
		return nil, false, errors.New("placement redis hash is missing required TTL")
	}
	if ttl < 0 {
		return nil, false, errors.New("placement redis hash returned invalid TTL")
	}
	return fields, true, nil
}

// assignmentKey 构造不在默认格式化中暴露 world identity 的 current key。
func (store *Store) assignmentKey(worldID personalworld.PersonalWorldID) (storageredis.Key, error) {
	return store.keyspace.Build(assignmentDefinitionName, worldID.String())
}

// keys 构造同一 transition 使用的 current 与 digest replay key。
func (store *Store) keys(worldID personalworld.PersonalWorldID, fingerprint string) (storageredis.Key, storageredis.Key, error) {
	assignmentKey, err := store.assignmentKey(worldID)
	if err != nil {
		return storageredis.Key{}, storageredis.Key{}, err
	}
	replayKey, err := store.keyspace.Build(transitionDefinitionName, fingerprint)
	if err != nil {
		return storageredis.Key{}, storageredis.Key{}, err
	}
	return assignmentKey, replayKey, nil
}

// acquireArguments 构造 Lua 使用的 canonical time 与有界 TTL 参数。
func (store *Store) acquireArguments(request domain.AcquireRequest) (string, string, string, string, string, error) {
	observedUS, err := canonicalMicroTime(request.ObservedAt())
	if err != nil {
		return "", "", "", "", "", err
	}
	createdUS, err := canonicalMicroTime(request.Candidate().CreatedAt())
	if err != nil {
		return "", "", "", "", "", err
	}
	expiresUS, err := canonicalMicroTime(request.Candidate().LeaseExpiresAt())
	if err != nil {
		return "", "", "", "", "", err
	}
	expiresMS, err := expiryMilliseconds(request.Candidate().LeaseExpiresAt())
	if err != nil {
		return "", "", "", "", "", err
	}
	replayMS, err := replayMilliseconds(store.replayTTL)
	return observedUS, createdUS, expiresUS, expiresMS, replayMS, err
}

// acquireFingerprint 绑定首次 allocation 的稳定 stamp，不绑定重试时钟。
func acquireFingerprint(snapshot domain.AssignmentSnapshot) string {
	return transitionFingerprint("acquire", stampFields(snapshot.Stamp())...)
}

// replaceFingerprint 绑定 predecessor 与已分配 successor stamp，不绑定重试时钟或临时候选时间。
func replaceFingerprint(predecessor domain.AssignmentStamp, successor domain.AssignmentSnapshot) string {
	fields := append(stampFields(predecessor), stampFields(successor.Stamp())...)
	return transitionFingerprint("replace", fields...)
}

// sameAllocation 比较 reservation 不可变字段，允许 phase 和 renew 后 expiry 合法推进。
func sameAllocation(current domain.AssignmentSnapshot, expected domain.AssignmentSnapshot) bool {
	return current.Valid() && expected.Valid() && current.Stamp().Equal(expected.Stamp()) && current.CreatedAt().Equal(expected.CreatedAt()) &&
		!current.Lease().ExpiresAt().Before(expected.Lease().ExpiresAt())
}

// successOutcome 报告 StoreOutcome 是否必须携带完整 snapshot。
func successOutcome(outcome domain.StoreOutcome) bool {
	switch outcome {
	case domain.StoreOutcomeApplied, domain.StoreOutcomeExisting, domain.StoreOutcomeReplay, domain.StoreOutcomeInProgress:
		return true
	default:
		return false
	}
}

// mapStoreOutcome 把固定 Lua code 映射到领域已有 outcome。
func mapStoreOutcome(code string) (domain.StoreOutcome, error) {
	switch code {
	case "applied":
		return domain.StoreOutcomeApplied, nil
	case "existing":
		return domain.StoreOutcomeExisting, nil
	case "replay":
		return domain.StoreOutcomeReplay, nil
	case "in_progress":
		return domain.StoreOutcomeInProgress, nil
	case "not_found":
		return domain.StoreOutcomeNotFound, nil
	case "conflict":
		return domain.StoreOutcomeConflict, nil
	case "expired":
		return domain.StoreOutcomeExpired, nil
	default:
		return domain.StoreOutcomeUnspecified, errors.New("placement script outcome is unknown")
	}
}

// adapterError 保留受控 cause，默认文本只包含固定 operation/outcome。
type adapterError struct {
	// operation 是代码定义的固定 store 操作。
	operation string
	// outcome 是固定低基数失败分类。
	outcome string
	// cause 只供受控 errors.Is/As 使用，不参与 Error 文本。
	cause error
}

// Error 返回不包含 SQL、Redis key/value、identity、fence 或 client 原文的稳定错误。
func (failure *adapterError) Error() string {
	return fmt.Sprintf("placement storage %s failed (%s)", failure.operation, failure.outcome)
}

// Unwrap 返回内部诊断使用的底层 cause。
func (failure *adapterError) Unwrap() error { return failure.cause }

// failure 记录固定失败结果并构造默认脱敏错误。
func (store *Store) failure(operation string, outcome string, cause error) error {
	store.observe(operation, outcome)
	return &adapterError{operation: operation, outcome: outcome, cause: cause}
}

// observe 只转发固定 adapter、operation 与 outcome。
func (store *Store) observe(operation string, outcome string) {
	store.observer.RecordStorageOperation("placement", operation, outcome)
}
