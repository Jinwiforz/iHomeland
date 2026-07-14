package visitsession

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"
	domain "github.com/jinwiforz/ihomeland/server/internal/visitsession"
	redisclient "github.com/redis/go-redis/v9"
)

// replay retention边界同时限制故障决议窗口与Redis资源占用。
const (
	minimumReplayRetention = time.Minute
	maximumReplayRetention = 24 * time.Hour
)

// Clock 提供计算物理TTL所需的可替换受信绝对时间。
type Clock interface {
	// Now 必须允许并发调用；Store只用它拒绝已越过physical retention的写入。
	Now() time.Time
}

// Observer 接收固定adapter/operation/outcome，不接收任何identity、key、value或driver文本。
type Observer interface {
	// RecordStorageOperation 记录三个低基数分类字段。
	RecordStorageOperation(string, string, string)
}

// Store 借用共享standalone Redis client实现VisitSessionStore。
//
// Store没有client或后台任务所有权，可并发调用；所有create/transition由owner Lua script
// 线性化。Redis丢失后旧访问资格只会fail closed，不从MySQL或memory恢复。
type Store struct {
	// client由storage/redis Component持有，本adapter不得关闭。
	client *redisclient.Client
	// keyspace只允许构造已登记VisitSession owner key。
	keyspace *storageredis.Keyspace
	// clock用于验证session expiry加retention仍在未来。
	clock Clock
	// replayRetention使全部首次结果保留到session到期后的同一有界窗口。
	replayRetention time.Duration
	// sessionPrefix供Lua从active index解析完整session key。
	sessionPrefix string
	// activePrefix供Commit在先决议command后从current world派生active key。
	activePrefix string
	// observer只接收固定低基数结果。
	observer Observer
}

// 编译期固定production adapter必须完整实现领域端口。
var _ domain.VisitSessionStore = (*Store)(nil)

// New 创建不获取资源的production VisitSession Redis adapter。
//
// client、keyspace、clock与observer均由调用方持有并保证生命周期覆盖Store；Store会保留引用，
// 但不关闭client或启动goroutine。构造只验证依赖、三个owner definitions及1分钟至24小时的
// replayRetention，不连接Redis；成功返回值允许并发调用。
func New(client *redisclient.Client, keyspace *storageredis.Keyspace, clock Clock, replayRetention time.Duration, observer Observer) (*Store, error) {
	if client == nil || keyspace == nil || clock == nil || observer == nil {
		return nil, errors.New("visit session store requires redis client, keyspace, clock, and observer")
	}
	if replayRetention < minimumReplayRetention || replayRetention > maximumReplayRetention {
		return nil, errors.New("visit session replay retention must be between one minute and twenty-four hours")
	}
	for _, name := range []string{activeDefinitionName, sessionDefinitionName, commandDefinitionName} {
		if _, err := keyspace.Build(name, "validation"); err != nil {
			return nil, errors.New("visit session keyspace is missing required definitions")
		}
	}
	prefix, err := keyPrefix(keyspace, sessionDefinitionName)
	if err != nil {
		return nil, err
	}
	activePrefix, err := keyPrefix(keyspace, activeDefinitionName)
	if err != nil {
		return nil, err
	}
	return &Store{client: client, keyspace: keyspace, clock: clock, replayRetention: replayRetention, sessionPrefix: prefix, activePrefix: activePrefix, observer: observer}, nil
}

// Create 原子维护每个PersonalWorld至多一个active VisitSession及完整Open replay。
//
// 方法在同一Lua执行点先决议CommandID replay/conflict，再检查world active唯一性。发送前
// 校验或已取消 context 返回 NotCommitted；Lua 明确报告写入前状态损坏时也能证明未提交。
// Driver error 或未知/矛盾 reply 返回 CommitUnknown 与零值结果，调用方只能复用相同
// CommandID/fingerprint 解析首次结果。
func (store *Store) Create(ctx context.Context, record domain.CreateRecord) (domain.CreateResult, domain.CreateOutcome, error) {
	if err := contextPreflight(ctx); err != nil {
		return domain.CreateResult{}, domain.CreateOutcomeNotCommitted, store.failure("create", "not_committed", err)
	}
	candidate := record.Candidate()
	if !record.CommandID().Valid() || !record.Fingerprint().Valid() || !candidate.Valid() || candidate.Lifecycle() != domain.LifecycleOpen || candidate.Revision() != domain.InitialRevision {
		return domain.CreateResult{}, domain.CreateOutcomeNotCommitted, store.failure("create", "invalid", errors.New("visit create record is invalid"))
	}
	sessionPayload, err := encodeSnapshot(candidate)
	if err != nil {
		return domain.CreateResult{}, domain.CreateOutcomeNotCommitted, store.failure("create", "codec_failed", err)
	}
	createResult, err := domain.NewCreateResult(candidate, record.CommandID(), record.Fingerprint())
	if err != nil {
		return domain.CreateResult{}, domain.CreateOutcomeNotCommitted, store.failure("create", "invalid", err)
	}
	resultPayload, err := encodeCreateResult(createResult)
	if err != nil {
		return domain.CreateResult{}, domain.CreateOutcomeNotCommitted, store.failure("create", "codec_failed", err)
	}
	commandKey, activeKey, sessionKey, err := store.keys(record.CommandID(), candidate.WorldID(), candidate.ID())
	if err != nil {
		return domain.CreateResult{}, domain.CreateOutcomeNotCommitted, store.failure("create", "key_failed", err)
	}
	expiresUS, expiresMS, err := store.physicalExpiry(candidate.ExpiresAt())
	if err != nil {
		return domain.CreateResult{}, domain.CreateOutcomeNotCommitted, store.failure("create", "invalid", err)
	}
	facts, err := snapshotFacts(candidate)
	if err != nil {
		return domain.CreateResult{}, domain.CreateOutcomeNotCommitted, store.failure("create", "codec_failed", err)
	}
	version := strconv.FormatUint(uint64(redisSchemaVersion), 10)
	fingerprint := encodeFingerprint(record.Fingerprint())
	revision := canonicalUint(candidate.Revision().Uint64())
	if err := validateHashEncoding(activeDefinition(),
		"v", version,
		"visit_id", candidate.ID().Value(),
		"world", candidate.WorldID().String(),
		"expires_us", expiresUS,
	); err != nil {
		return domain.CreateResult{}, domain.CreateOutcomeNotCommitted, store.failure("create", "codec_failed", err)
	}
	if err := validateHashEncoding(sessionDefinition(),
		"v", version,
		"visit_id", candidate.ID().Value(),
		"world", candidate.WorldID().String(),
		"revision", revision,
		"lifecycle", candidate.Lifecycle().String(),
		"expires_us", expiresUS,
		"facts", facts,
		"payload", sessionPayload,
	); err != nil {
		return domain.CreateResult{}, domain.CreateOutcomeNotCommitted, store.failure("create", "codec_failed", err)
	}
	if err := validateHashEncoding(commandDefinition(),
		"v", version,
		"kind", "create",
		"fingerprint", fingerprint,
		"visit_id", candidate.ID().Value(),
		"world", candidate.WorldID().String(),
		"revision", revision,
		"expires_us", expiresUS,
		"payload", resultPayload,
	); err != nil {
		return domain.CreateResult{}, domain.CreateOutcomeNotCommitted, store.failure("create", "codec_failed", err)
	}
	value, evalErr := createScript.Run(ctx, store.client, []string{commandKey.Value(), activeKey.Value(), sessionKey.Value()},
		version, fingerprint, candidate.ID().Value(), candidate.WorldID().String(), revision, candidate.Lifecycle().String(), expiresUS, facts, sessionPayload,
		expiresMS, store.sessionPrefix, resultPayload, strconv.Itoa(maximumCommandBytes), strconv.Itoa(maximumActiveBytes), strconv.Itoa(maximumSessionBytes)).Result()
	if evalErr != nil {
		outcome := storageredis.ClassifyCommandError(storageredis.OperationAtomicMutation, evalErr)
		return domain.CreateResult{}, domain.CreateOutcomeCommitUnknown, store.failure("create", string(outcome), evalErr)
	}
	items, err := scriptItems(value)
	if err != nil {
		return domain.CreateResult{}, domain.CreateOutcomeCommitUnknown, store.failure("create", "defect", err)
	}
	switch items[0] {
	case "created", "replay":
		expectedLength := 2
		if items[0] == "replay" {
			expectedLength = 6
		}
		if len(items) != expectedLength {
			return domain.CreateResult{}, domain.CreateOutcomeCommitUnknown, store.failure("create", "defect", errors.New("visit create reply has invalid shape"))
		}
		result, parseErr := decodeCreateResult(items[1])
		if parseErr != nil || result.CommandID() != record.CommandID() || !result.Fingerprint().Equal(record.Fingerprint()) {
			return domain.CreateResult{}, domain.CreateOutcomeCommitUnknown, store.failure("create", "defect", errors.New("visit create reply contradicts command"))
		}
		if items[0] == "created" && items[1] != resultPayload {
			return domain.CreateResult{}, domain.CreateOutcomeCommitUnknown, store.failure("create", "defect", errors.New("visit create reply contradicts command"))
		}
		if items[0] == "replay" {
			if metadataErr := validateCommandMetadata("create", fingerprint, items[2], items[3], items[4], items[5], items[1]); metadataErr != nil {
				return domain.CreateResult{}, domain.CreateOutcomeCommitUnknown, store.failure("create", "defect", metadataErr)
			}
		}
		outcome := domain.CreateOutcomeCreated
		if items[0] == "replay" {
			outcome = domain.CreateOutcomeReplay
		}
		store.observe("create", items[0])
		return result, outcome, nil
	case "existing":
		if len(items) != 8 {
			return domain.CreateResult{}, domain.CreateOutcomeCommitUnknown, store.failure("create", "defect", errors.New("visit existing reply has invalid shape"))
		}
		snapshot, parseErr := decodeSnapshot(items[1])
		if parseErr != nil || snapshot.WorldID() != candidate.WorldID() || snapshot.Lifecycle() == domain.LifecycleClosed || validateSessionMetadata(snapshot, items[2], items[3], items[4], items[5], items[6], items[7]) != nil {
			return domain.CreateResult{}, domain.CreateOutcomeCommitUnknown, store.failure("create", "defect", errors.New("visit existing reply is invalid"))
		}
		result, parseErr := domain.NewExistingResult(snapshot)
		if parseErr != nil {
			return domain.CreateResult{}, domain.CreateOutcomeCommitUnknown, store.failure("create", "defect", parseErr)
		}
		store.observe("create", "existing")
		return result, domain.CreateOutcomeExisting, nil
	case "idempotency_conflict":
		if len(items) != 1 {
			return domain.CreateResult{}, domain.CreateOutcomeCommitUnknown, store.failure("create", "defect", errors.New("visit conflict reply has invalid shape"))
		}
		store.observe("create", items[0])
		return domain.CreateResult{}, domain.CreateOutcomeIdempotencyConflict, nil
	case "defect":
		if len(items) != 1 {
			return domain.CreateResult{}, domain.CreateOutcomeCommitUnknown, store.failure("create", "defect", errors.New("visit defect reply has invalid shape"))
		}
		return domain.CreateResult{}, domain.CreateOutcomeNotCommitted, store.failure("create", "dependency_defect", errors.New("visit redis state is corrupt"))
	default:
		return domain.CreateResult{}, domain.CreateOutcomeCommitUnknown, store.failure("create", "defect", errors.New("visit create reply is unknown"))
	}
}

// ResolveActive 原子读取world index与未关闭完整snapshot。
//
// 正TTL只证明运行投影仍可读取，不延长session、invite或membership领域资格；调用方必须按
// snapshot deadline重新决议。缺失一半的index/session、metadata矛盾或缺失TTL均fail closed。
func (store *Store) ResolveActive(ctx context.Context, worldID personalworld.PersonalWorldID) (domain.Snapshot, domain.ResolveOutcome, error) {
	if ctx == nil || !worldID.Valid() {
		return domain.Snapshot{}, domain.ResolveOutcomeUnspecified, store.failure("resolve_active", "invalid", errors.New("visit active lookup is invalid"))
	}
	key, err := store.activeKey(worldID)
	if err != nil {
		return domain.Snapshot{}, domain.ResolveOutcomeUnspecified, store.failure("resolve_active", "key_failed", err)
	}
	value, evalErr := resolveActiveScript.Run(ctx, store.client, []string{key.Value()}, strconv.FormatUint(uint64(redisSchemaVersion), 10), worldID.String(), store.sessionPrefix, strconv.Itoa(maximumActiveBytes), strconv.Itoa(maximumSessionBytes)).Result()
	return store.parseRead("resolve_active", value, evalErr, worldID, domain.VisitSessionID{})
}

// FindByID 返回物理retention窗口内的current或terminal完整snapshot。
//
// 返回Found不表示领域deadline仍有效，也不表示safe-return side effect已经完成；调用方只能
// 将快照交给application执行显式校验或cleanup command。
func (store *Store) FindByID(ctx context.Context, visitSessionID domain.VisitSessionID) (domain.Snapshot, domain.ResolveOutcome, error) {
	if ctx == nil || !visitSessionID.Valid() {
		return domain.Snapshot{}, domain.ResolveOutcomeUnspecified, store.failure("find_by_id", "invalid", errors.New("visit ID lookup is invalid"))
	}
	key, err := store.sessionKey(visitSessionID)
	if err != nil {
		return domain.Snapshot{}, domain.ResolveOutcomeUnspecified, store.failure("find_by_id", "key_failed", err)
	}
	value, evalErr := findScript.Run(ctx, store.client, []string{key.Value()}, strconv.FormatUint(uint64(redisSchemaVersion), 10), strconv.Itoa(maximumSessionBytes)).Result()
	return store.parseRead("find_by_id", value, evalErr, personalworld.PersonalWorldID{}, visitSessionID)
}

// Commit 原子决议replay、expected revision、immutable facts、target/result与terminal index删除。
//
// 无target probe只产生确定性冲突且不写replay；成功提交会保存首次完整result。发送前失败返回
// NotCommitted；Lua 明确报告写入前 defect 也返回 NotCommitted。可能已送达的 driver
// error 或未知/矛盾 reply 返回 CommitUnknown 与零值结果；Store 不隐式重试 application
// mutation 或替换 CommandID。
func (store *Store) Commit(ctx context.Context, record domain.TransitionRecord) (domain.MutationResult, domain.MutationOutcome, error) {
	if err := contextPreflight(ctx); err != nil {
		return domain.MutationResult{}, domain.MutationOutcomeNotCommitted, store.failure("commit", "not_committed", err)
	}
	if record.Operation() == domain.OperationUnspecified || !record.VisitSessionID().Valid() || !record.ExpectedRevision().Valid() || !record.CommandID().Valid() || !record.Fingerprint().Valid() {
		return domain.MutationResult{}, domain.MutationOutcomeNotCommitted, store.failure("commit", "invalid", errors.New("visit transition record is invalid"))
	}
	currentKey, err := store.sessionKey(record.VisitSessionID())
	if err != nil {
		return domain.MutationResult{}, domain.MutationOutcomeNotCommitted, store.failure("commit", "key_failed", err)
	}
	var target domain.Snapshot
	if record.HasResult() {
		target = record.Result().Snapshot()
	}
	commandKey, err := store.commandKey(record.CommandID())
	if err != nil {
		return domain.MutationResult{}, domain.MutationOutcomeNotCommitted, store.failure("commit", "key_failed", err)
	}
	hasResult := "0"
	targetPayload, resultPayload := "", ""
	targetID, targetWorld, targetRevision, targetLifecycle, expiresUS, expiresMS, facts := "", "", "", "", "", "", ""
	if record.HasResult() {
		hasResult = "1"
		targetID, targetWorld = target.ID().Value(), target.WorldID().String()
		targetPayload, err = encodeSnapshot(target)
		if err == nil {
			resultPayload, err = encodeMutationResult(record.Result())
		}
		targetRevision = canonicalUint(target.Revision().Uint64())
		targetLifecycle = target.Lifecycle().String()
		expiresUS, expiresMS, err = store.physicalExpiry(target.ExpiresAt())
		if err == nil {
			facts, err = snapshotFacts(target)
		}
	}
	if err != nil {
		return domain.MutationResult{}, domain.MutationOutcomeNotCommitted, store.failure("commit", "codec_failed", err)
	}
	version := strconv.FormatUint(uint64(redisSchemaVersion), 10)
	fingerprint := encodeFingerprint(record.Fingerprint())
	if record.HasResult() {
		if err := validateHashEncoding(sessionDefinition(),
			"v", version,
			"visit_id", targetID,
			"world", targetWorld,
			"revision", targetRevision,
			"lifecycle", targetLifecycle,
			"expires_us", expiresUS,
			"facts", facts,
			"payload", targetPayload,
		); err != nil {
			return domain.MutationResult{}, domain.MutationOutcomeNotCommitted, store.failure("commit", "codec_failed", err)
		}
		if err := validateHashEncoding(commandDefinition(),
			"v", version,
			"kind", "mutation",
			"fingerprint", fingerprint,
			"visit_id", targetID,
			"world", targetWorld,
			"revision", targetRevision,
			"expires_us", expiresUS,
			"payload", resultPayload,
		); err != nil {
			return domain.MutationResult{}, domain.MutationOutcomeNotCommitted, store.failure("commit", "codec_failed", err)
		}
	}
	value, evalErr := commitScript.Run(ctx, store.client, []string{commandKey.Value(), currentKey.Value()},
		version, fingerprint, record.VisitSessionID().Value(), canonicalUint(record.ExpectedRevision().Uint64()), hasResult, targetID, targetWorld, targetRevision,
		targetLifecycle, expiresUS, facts, targetPayload, resultPayload, expiresMS, store.activePrefix, strconv.Itoa(maximumCommandBytes), strconv.Itoa(maximumSessionBytes), strconv.Itoa(maximumActiveBytes)).Result()
	if evalErr != nil {
		outcome := storageredis.ClassifyCommandError(storageredis.OperationAtomicMutation, evalErr)
		return domain.MutationResult{}, domain.MutationOutcomeCommitUnknown, store.failure("commit", string(outcome), evalErr)
	}
	items, err := scriptItems(value)
	if err != nil {
		return domain.MutationResult{}, domain.MutationOutcomeCommitUnknown, store.failure("commit", "defect", err)
	}
	if items[0] == "applied" || items[0] == "replay" {
		expectedLength := 2
		if items[0] == "replay" {
			expectedLength = 6
		}
		if len(items) != expectedLength {
			return domain.MutationResult{}, domain.MutationOutcomeCommitUnknown, store.failure("commit", "defect", errors.New("visit mutation reply has invalid shape"))
		}
		result, parseErr := decodeMutationResult(items[1])
		if parseErr != nil || result.CommandID() != record.CommandID() || !result.Fingerprint().Equal(record.Fingerprint()) || result.Operation() != record.Operation() {
			return domain.MutationResult{}, domain.MutationOutcomeCommitUnknown, store.failure("commit", "defect", errors.New("visit mutation reply contradicts command"))
		}
		if items[0] == "applied" && items[1] != resultPayload {
			return domain.MutationResult{}, domain.MutationOutcomeCommitUnknown, store.failure("commit", "defect", errors.New("visit mutation reply contradicts command"))
		}
		if items[0] == "replay" {
			if metadataErr := validateCommandMetadata("mutation", fingerprint, items[2], items[3], items[4], items[5], items[1]); metadataErr != nil {
				return domain.MutationResult{}, domain.MutationOutcomeCommitUnknown, store.failure("commit", "defect", metadataErr)
			}
		}
		outcome := domain.MutationOutcomeApplied
		if items[0] == "replay" {
			outcome = domain.MutationOutcomeReplay
		}
		store.observe("commit", items[0])
		return result, outcome, nil
	}
	if len(items) != 1 {
		return domain.MutationResult{}, domain.MutationOutcomeCommitUnknown, store.failure("commit", "defect", errors.New("visit mutation outcome has invalid shape"))
	}
	if items[0] == "defect" {
		return domain.MutationResult{}, domain.MutationOutcomeNotCommitted, store.failure("commit", "dependency_defect", errors.New("visit redis state is corrupt"))
	}
	outcome, mapErr := mutationOutcome(items[0])
	if mapErr != nil {
		return domain.MutationResult{}, domain.MutationOutcomeCommitUnknown, store.failure("commit", "defect", mapErr)
	}
	store.observe("commit", items[0])
	return domain.MutationResult{}, outcome, nil
}

// parseRead 验证只读Lua返回形状、完整payload及调用方查询条件的一致性。
func (store *Store) parseRead(operation string, value any, evalErr error, expectedWorld personalworld.PersonalWorldID, expectedID domain.VisitSessionID) (domain.Snapshot, domain.ResolveOutcome, error) {
	if evalErr != nil {
		outcome := storageredis.ClassifyCommandError(storageredis.OperationReadOnly, evalErr)
		return domain.Snapshot{}, domain.ResolveOutcomeUnspecified, store.failure(operation, string(outcome), evalErr)
	}
	items, err := scriptItems(value)
	if err != nil {
		return domain.Snapshot{}, domain.ResolveOutcomeUnspecified, store.failure(operation, "defect", err)
	}
	if items[0] == "not_found" && len(items) == 1 {
		store.observe(operation, "not_found")
		return domain.Snapshot{}, domain.ResolveOutcomeNotFound, nil
	}
	if items[0] != "found" || len(items) != 8 {
		return domain.Snapshot{}, domain.ResolveOutcomeUnspecified, store.failure(operation, "defect", errors.New("visit read reply has invalid shape"))
	}
	snapshot, err := decodeSnapshot(items[1])
	if err != nil || expectedWorld.Valid() && snapshot.WorldID() != expectedWorld || expectedID.Valid() && snapshot.ID() != expectedID {
		return domain.Snapshot{}, domain.ResolveOutcomeUnspecified, store.failure(operation, "defect", errors.New("visit read payload contradicts lookup"))
	}
	if err := validateSessionMetadata(snapshot, items[2], items[3], items[4], items[5], items[6], items[7]); err != nil {
		return domain.Snapshot{}, domain.ResolveOutcomeUnspecified, store.failure(operation, "defect", err)
	}
	store.observe(operation, "found")
	return snapshot, domain.ResolveOutcomeFound, nil
}

// physicalExpiry 计算领域到期时间及其有界replay保留后的Redis绝对毫秒deadline。
func (store *Store) physicalExpiry(sessionExpiry time.Time) (string, string, error) {
	physical := sessionExpiry.UTC().Truncate(time.Microsecond).Add(store.replayRetention)
	if !physical.After(sessionExpiry) {
		return "", "", errors.New("visit physical expiry overflowed")
	}
	if _, err := storageredis.TTLUntil(physical, store.clock.Now()); err != nil {
		return "", "", err
	}
	expiresUS, err := canonicalTime(sessionExpiry)
	if err != nil {
		return "", "", err
	}
	expiresMS, err := expiryMilliseconds(physical)
	return expiresUS, expiresMS, err
}

// keys 一次构造Create脚本所需的command、active与session owner keys。
func (store *Store) keys(commandID domain.CommandID, worldID personalworld.PersonalWorldID, visitID domain.VisitSessionID) (storageredis.Key, storageredis.Key, storageredis.Key, error) {
	command, err := store.commandKey(commandID)
	if err != nil {
		return storageredis.Key{}, storageredis.Key{}, storageredis.Key{}, err
	}
	active, err := store.activeKey(worldID)
	if err != nil {
		return storageredis.Key{}, storageredis.Key{}, storageredis.Key{}, err
	}
	sessionKey, err := store.sessionKey(visitID)
	return command, active, sessionKey, err
}

// commandKey 构造全局幂等命令结果key。
func (store *Store) commandKey(id domain.CommandID) (storageredis.Key, error) {
	if !id.Valid() {
		return storageredis.Key{}, errors.New("visit command ID is invalid")
	}
	return store.keyspace.Build(commandDefinitionName, id.Value())
}

// activeKey 构造PersonalWorld唯一活动会话索引key。
func (store *Store) activeKey(id personalworld.PersonalWorldID) (storageredis.Key, error) {
	if !id.Valid() {
		return storageredis.Key{}, errors.New("visit world ID is invalid")
	}
	return store.keyspace.Build(activeDefinitionName, id.String())
}

// sessionKey 构造完整VisitSession snapshot key。
func (store *Store) sessionKey(id domain.VisitSessionID) (storageredis.Key, error) {
	if !id.Valid() {
		return storageredis.Key{}, errors.New("visit session ID is invalid")
	}
	return store.keyspace.Build(sessionDefinitionName, id.Value())
}

// keyPrefix 从受注册表约束的示例key派生Lua动态寻址所需前缀。
func keyPrefix(keyspace *storageredis.Keyspace, definitionName string) (string, error) {
	const marker = "validation"
	key, err := keyspace.Build(definitionName, marker)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(key.Value(), marker) {
		return "", errors.New("visit key prefix derivation failed")
	}
	return strings.TrimSuffix(key.Value(), marker), nil
}

// contextPreflight 仅在Redis发送前证明nil或已取消请求尚未提交。
func contextPreflight(ctx context.Context) error {
	if ctx == nil {
		return errors.New("visit store context is nil")
	}
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	default:
		return nil
	}
}

// scriptItems 将driver返回收敛为不允许隐式类型转换的字符串列表。
func scriptItems(value any) ([]string, error) {
	values, ok := value.([]any)
	if !ok || len(values) == 0 {
		return nil, errors.New("visit script result has invalid shape")
	}
	items := make([]string, len(values))
	for index, value := range values {
		item, ok := value.(string)
		if !ok {
			return nil, errors.New("visit script result contains invalid type")
		}
		items[index] = item
	}
	return items, nil
}

// mutationOutcome 只映射当前owner script能够确定产生的非成功mutation结果。
//
// capacity与stale等policy结果由application在生成record前决议；若Lua意外返回这些文本，
// parser必须按unknown reply fail closed，不能在adapter重复实现领域状态机。
func mutationOutcome(code string) (domain.MutationOutcome, error) {
	switch code {
	case "not_found":
		return domain.MutationOutcomeNotFound, nil
	case "revision_conflict":
		return domain.MutationOutcomeRevisionConflict, nil
	case "idempotency_conflict":
		return domain.MutationOutcomeIdempotencyConflict, nil
	case "invalid_state":
		return domain.MutationOutcomeInvalidState, nil
	default:
		return domain.MutationOutcomeUnspecified, errors.New("visit mutation outcome is unknown")
	}
}

// adapterError 对外只暴露低基数故障分类，并通过Unwrap保留受控内部诊断能力。
type adapterError struct {
	// operation是固定adapter操作名。
	operation string
	// outcome是固定故障结果名。
	outcome string
	// cause不得进入Error、日志或metrics标签。
	cause error
}

// Error 返回不含Redis key/value、identity、fingerprint或driver文本的稳定错误。
func (failure *adapterError) Error() string {
	return fmt.Sprintf("visit session storage %s failed (%s)", failure.operation, failure.outcome)
}

// Unwrap 仅供受控内部诊断检查cause。
func (failure *adapterError) Unwrap() error { return failure.cause }

// failure 在生成脱敏错误的同时记录一次低基数失败观测。
func (store *Store) failure(operation string, outcome string, cause error) error {
	store.observe(operation, outcome)
	return &adapterError{operation: operation, outcome: outcome, cause: cause}
}

// observe 将观测维度固定为adapter、operation与outcome。
func (store *Store) observe(operation string, outcome string) {
	store.observer.RecordStorageOperation("visitsession", operation, outcome)
}
