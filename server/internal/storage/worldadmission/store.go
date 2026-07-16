package worldadmission

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"
	domain "github.com/jinwiforz/ihomeland/server/internal/worldadmission"
	redisclient "github.com/redis/go-redis/v9"
)

// Observer 接收固定adapter/operation/outcome，不接收key、identity、digest或Redis错误。
type Observer interface {
	// RecordStorageOperation 记录固定 adapter、operation 与 outcome，不接收业务 identity。
	RecordStorageOperation(adapter, operation, outcome string)
}

// Store 借用共享 standalone Redis client 实现 worldadmission Store。
//
// Adapter 不拥有 client 生命周期，不保存 raw credential，也不启动 timer 或 goroutine。全部
// mutation 由 owner Lua script 在线性化点完成，进程内不存在 memory fallback。
type Store struct {
	// client 由storage/redis Component持有，本adapter不关闭。
	client *redisclient.Client
	// keyspace 只允许构造已登记worldadmission key。
	keyspace *storageredis.Keyspace
	// observer 只接收固定低基数结果。
	observer Observer
	// credentialPrefix 只用于owner Lua按issue digest定位对应credential Hash。
	credentialPrefix string
}

var _ domain.Store = (*Store)(nil)

// New 创建不获取资源、不启动后台任务的 production adapter。
//
// client、keyspace 与 observer 的生命周期仍由 Composition Root 持有；构造会验证两个 owner
// definition 已登记，避免运行到首次请求才发现 key schema 缺失。
func New(client *redisclient.Client, keyspace *storageredis.Keyspace, observer Observer) (*Store, error) {
	if client == nil || keyspace == nil || observer == nil {
		return nil, errors.New("world admission store requires redis client, keyspace, and observer")
	}
	for _, name := range []string{issueDefinitionName, credentialDefinitionName} {
		if _, err := keyspace.Build(name, "validation"); err != nil {
			return nil, errors.New("world admission keyspace is missing required definitions")
		}
	}
	credentialPrefix, err := admissionKeyPrefix(keyspace, credentialDefinitionName)
	if err != nil {
		return nil, err
	}
	return &Store{client: client, keyspace: keyspace, observer: observer, credentialPrefix: credentialPrefix}, nil
}

// ResolveIssue 原子读取issue与其digest指向的credential，任一半缺失或损坏均fail closed。
func (store *Store) ResolveIssue(ctx context.Context, issueID domain.IssueID) (domain.IssueSnapshot, domain.IssueResolveOutcome, error) {
	if !issueID.Valid() {
		return domain.IssueSnapshot{}, domain.IssueResolveOutcomeUnspecified, store.failure("issue", "invalid", errors.New("world admission issue ID is invalid"))
	}
	issueKey, err := store.keyspace.Build(issueDefinitionName, storageredis.DigestIdentity([]byte(issueID.Value())))
	if err != nil {
		return domain.IssueSnapshot{}, domain.IssueResolveOutcomeUnspecified, store.failure("issue", "key_failed", err)
	}
	value, evalErr := resolveIssueScript.Run(ctx, store.client, []string{issueKey.Value()}, store.credentialPrefix).Result()
	if evalErr != nil {
		return domain.IssueSnapshot{}, domain.IssueResolveOutcomeUnspecified, store.failure("issue", "read_failed", evalErr)
	}
	items, err := resultStrings(value)
	if err != nil || len(items) == 0 {
		return domain.IssueSnapshot{}, domain.IssueResolveOutcomeUnspecified, store.failure("issue", "defect", errors.New("world admission issue resolve result is invalid"))
	}
	switch items[0] {
	case "not_found":
		store.observer.RecordStorageOperation("worldadmission", "issue", "not_found")
		return domain.IssueSnapshot{}, domain.IssueResolveOutcomeNotFound, nil
	case "found":
		if len(items) != 20 {
			return domain.IssueSnapshot{}, domain.IssueResolveOutcomeUnspecified, store.failure("issue", "defect", errors.New("world admission issue resolve shape is invalid"))
		}
		fingerprint, fingerprintErr := domain.ParseDigestHex(items[1])
		digest, digestErr := domain.ParseDigestHex(items[2])
		binding, bindingErr := decodeBindingReply(items[4:])
		snapshot := domain.IssueSnapshot{Fingerprint: fingerprint, CredentialDigest: digest, Binding: binding, Consumed: items[3] == "consumed"}
		if fingerprintErr != nil || digestErr != nil || bindingErr != nil || (items[3] != "issued" && items[3] != "consumed") || !snapshot.Valid() {
			return domain.IssueSnapshot{}, domain.IssueResolveOutcomeUnspecified, store.failure("issue", "codec_failed", errors.New("world admission issue resolve payload is invalid"))
		}
		store.observer.RecordStorageOperation("worldadmission", "issue", "found")
		return snapshot, domain.IssueResolveOutcomeFound, nil
	case "defect":
		return domain.IssueSnapshot{}, domain.IssueResolveOutcomeUnspecified, store.failure("issue", "defect", errors.New("world admission issue state is corrupt"))
	default:
		return domain.IssueSnapshot{}, domain.IssueResolveOutcomeUnspecified, store.failure("issue", "defect", errors.New("world admission issue resolve outcome is unknown"))
	}
}

// Issue 使用 owner Lua script 原子创建或重放 issuance/credential Hash。
//
// 两个 Hash 使用同一绝对 physical expiry，脚本会交叉验证 schema、TTL、fingerprint、digest
// 与完整 binding。Redis/Lua 返回错误时 mutation 可能已经提交，本方法保守返回
// IssueOutcomeCommitUnknown；调用方必须复用相同 IssueID 与 record 重试，不能生成新语义。
func (store *Store) Issue(ctx context.Context, record domain.IssueRecord, observedAt time.Time) (domain.IssueOutcome, error) {
	if !record.Valid() || observedAt.IsZero() || !record.Binding.ExpiresAt().After(observedAt) {
		return domain.IssueOutcomeUnspecified, store.failure("issue", "invalid", errors.New("world admission issue record is invalid"))
	}
	issueKey, err := store.keyspace.Build(issueDefinitionName, storageredis.DigestIdentity([]byte(record.IssueID.Value())))
	if err != nil {
		return domain.IssueOutcomeUnspecified, store.failure("issue", "key_failed", err)
	}
	credentialKey, err := store.keyspace.Build(credentialDefinitionName, record.CredentialDigest.Hex())
	if err != nil {
		return domain.IssueOutcomeUnspecified, store.failure("issue", "key_failed", err)
	}
	physicalMS, err := expiryMilliseconds(record.PhysicalExpiresAt)
	if err != nil {
		return domain.IssueOutcomeUnspecified, store.failure("issue", "invalid", err)
	}
	args := []any{record.Fingerprint.Hex(), record.CredentialDigest.Hex()}
	for _, value := range encodeBinding(record.Binding) {
		args = append(args, value)
	}
	args = append(args, strconv.FormatInt(record.Binding.ExpiresAt().UnixMicro(), 10), physicalMS, strconv.FormatInt(record.Binding.IssuedAt().UnixMicro(), 10))
	value, evalErr := issueScript.Run(ctx, store.client, []string{issueKey.Value(), credentialKey.Value()}, args...).Result()
	if evalErr != nil {
		return domain.IssueOutcomeCommitUnknown, store.failure("issue", "commit_unknown", evalErr)
	}
	items, err := resultStrings(value)
	if err != nil || len(items) != 1 {
		return domain.IssueOutcomeUnspecified, store.failure("issue", "defect", errors.New("world admission issue result is invalid"))
	}
	switch items[0] {
	case "created":
		return store.issueResult("issue", domain.IssueOutcomeCreated, "created")
	case "replay":
		return store.issueResult("issue", domain.IssueOutcomeReplay, "replay")
	case "consumed":
		return store.issueResult("issue", domain.IssueOutcomeConsumed, "consumed")
	case "conflict":
		return store.issueResult("issue", domain.IssueOutcomeIdempotencyConflict, "conflict")
	case "defect":
		return domain.IssueOutcomeUnspecified, store.failure("issue", "defect", errors.New("world admission issue state is corrupt"))
	default:
		return domain.IssueOutcomeUnspecified, store.failure("issue", "defect", errors.New("world admission issue outcome is unknown"))
	}
}

// Consume 使用 owner Lua script 原子验证静态 binding 并保存首次 consume identity。
//
// 脚本比较 AuthContext、endpoint、purpose 与业务 expiry，首次成功后把 issued 改为 consumed；
// physical TTL 仅保留有界 tombstone 和 response-loss 证据，不延长资格。相同 consume identity
// 与 fingerprint 可重放首次 binding，不同 identity 稳定返回 replayed。Redis/Lua 错误一律视为
// ConsumeOutcomeCommitUnknown，损坏、未知或缺 TTL 的 Hash fail closed 且不会被覆盖或修复。
func (store *Store) Consume(ctx context.Context, request domain.ConsumeRequest) (domain.Binding, domain.ConsumeOutcome, error) {
	if !request.Valid() {
		return domain.Binding{}, domain.ConsumeOutcomeUnspecified, store.failure("consume", "invalid", errors.New("world admission consume request is invalid"))
	}
	key, err := store.keyspace.Build(credentialDefinitionName, request.CredentialDigest.Hex())
	if err != nil {
		return domain.Binding{}, domain.ConsumeOutcomeUnspecified, store.failure("consume", "key_failed", err)
	}
	value, evalErr := consumeScript.Run(ctx, store.client, []string{key.Value()},
		request.ConsumeID.Value(), request.Fingerprint.Hex(), request.Auth.Principal().PlayerID(), request.Auth.SessionID().String(), strconv.FormatUint(uint64(request.Auth.Epoch()), 10), request.Purpose.String(), "tls_tcp", request.Endpoint.Host(), strconv.FormatInt(request.ObservedAt.UnixMicro(), 10), strconv.FormatUint(uint64(request.Endpoint.Port()), 10)).Result()
	if evalErr != nil {
		return domain.Binding{}, domain.ConsumeOutcomeCommitUnknown, store.failure("consume", "commit_unknown", evalErr)
	}
	items, err := resultStrings(value)
	if err != nil || len(items) == 0 {
		return domain.Binding{}, domain.ConsumeOutcomeUnspecified, store.failure("consume", "defect", errors.New("world admission consume result is invalid"))
	}
	switch items[0] {
	case "applied", "replay":
		binding, decodeErr := decodeBindingReply(items[1:])
		if decodeErr != nil {
			return domain.Binding{}, domain.ConsumeOutcomeUnspecified, store.failure("consume", "codec_failed", decodeErr)
		}
		outcome := domain.ConsumeOutcomeApplied
		if items[0] == "replay" {
			outcome = domain.ConsumeOutcomeReplay
		}
		store.observer.RecordStorageOperation("worldadmission", "consume", items[0])
		return binding, outcome, nil
	case "not_found":
		return store.consumeResult(domain.ConsumeOutcomeNotFound, "not_found")
	case "expired":
		return store.consumeResult(domain.ConsumeOutcomeExpired, "expired")
	case "mismatch":
		// Lua 使用紧凑 wire outcome；metrics 使用跨 adapter 的规范低基数枚举。
		return store.consumeResult(domain.ConsumeOutcomeBindingMismatch, "binding_mismatch")
	case "replayed":
		return store.consumeResult(domain.ConsumeOutcomeReplayed, "replayed")
	case "defect":
		return domain.Binding{}, domain.ConsumeOutcomeUnspecified, store.failure("consume", "defect", errors.New("world admission credential state is corrupt"))
	default:
		return domain.Binding{}, domain.ConsumeOutcomeUnspecified, store.failure("consume", "defect", errors.New("world admission consume outcome is unknown"))
	}
}

// issueResult 记录低基数成功决议并返回无error组合。
func (store *Store) issueResult(operation string, outcome domain.IssueOutcome, label string) (domain.IssueOutcome, error) {
	store.observer.RecordStorageOperation("worldadmission", operation, label)
	return outcome, nil
}

// admissionKeyPrefix 从已登记示例key安全派生同owner动态寻址前缀。
func admissionKeyPrefix(keyspace *storageredis.Keyspace, definitionName string) (string, error) {
	const marker = "validation"
	key, err := keyspace.Build(definitionName, marker)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(key.Value(), marker) {
		return "", errors.New("world admission key prefix derivation failed")
	}
	return strings.TrimSuffix(key.Value(), marker), nil
}

// consumeResult 记录不携带binding的确定性拒绝决议。
func (store *Store) consumeResult(outcome domain.ConsumeOutcome, label string) (domain.Binding, domain.ConsumeOutcome, error) {
	store.observer.RecordStorageOperation("worldadmission", "consume", label)
	return domain.Binding{}, outcome, nil
}

// failure 记录固定结果并保留仅供进程内诊断的cause链。
func (store *Store) failure(operation string, outcome string, cause error) error {
	store.observer.RecordStorageOperation("worldadmission", operation, outcome)
	return fmt.Errorf("world admission redis %s failed: %w", operation, cause)
}

// expiryMilliseconds 向上取整UTC微秒，防止physical TTL早于业务/重放deadline。
func expiryMilliseconds(value time.Time) (string, error) {
	if value.IsZero() || value.UnixMicro() <= 0 {
		return "", errors.New("world admission redis expiry is invalid")
	}
	return strconv.FormatInt((value.UnixMicro()+999)/1000, 10), nil
}

// resultStrings 严格解析go-redis Lua array，不接受数字或嵌套返回。
func resultStrings(value any) ([]string, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, errors.New("redis script result is not an array")
	}
	result := make([]string, len(items))
	for index, item := range items {
		switch typed := item.(type) {
		case string:
			result[index] = typed
		case []byte:
			result[index] = string(typed)
		default:
			return nil, errors.New("redis script result contains non-string")
		}
	}
	return result, nil
}
