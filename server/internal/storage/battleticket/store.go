package battleticket

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	domain "github.com/jinwiforz/ihomeland/server/internal/battleticket"
	storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"
	redisclient "github.com/redis/go-redis/v9"
)

// Observer 接收固定 adapter/operation/outcome，不接收 key、identity、digest 或 Redis error。
type Observer interface {
	// RecordStorageOperation 记录固定低基数 storage 结果。
	RecordStorageOperation(adapter, operation, outcome string)
}

// Store 借用共享 Redis client 实现 battleticket.Store。
type Store struct {
	// client 由 storage/redis Component 持有，本 adapter 不关闭。
	client *redisclient.Client
	// keyspace 只允许构造已登记 battleticket key。
	keyspace *storageredis.Keyspace
	// observer 只接收固定低基数结果。
	observer Observer
}

var _ domain.Store = (*Store)(nil)

// New 创建不获取资源、不启动后台任务的 production adapter。
func New(client *redisclient.Client, keyspace *storageredis.Keyspace, observer Observer) (*Store, error) {
	if client == nil || keyspace == nil || observer == nil {
		return nil, errors.New("battle ticket store requires redis client, keyspace, and observer")
	}
	if _, err := keyspace.Build(issueDefinitionName, "validation"); err != nil {
		return nil, errors.New("battle ticket keyspace is missing required definition")
	}
	return &Store{client: client, keyspace: keyspace, observer: observer}, nil
}

// Resolve 读取首次完整 record；missing 不从 memory、MySQL 或其他 key 恢复。
func (store *Store) Resolve(ctx context.Context, issueID domain.IssueID) (domain.IssueSnapshot, domain.ResolveOutcome, error) {
	if !issueID.Valid() {
		return domain.IssueSnapshot{}, domain.ResolveOutcomeUnspecified, store.failure("resolve", "invalid", errors.New("battle ticket issue identity is invalid"))
	}
	key, err := store.issueKey(issueID)
	if err != nil {
		return domain.IssueSnapshot{}, domain.ResolveOutcomeUnspecified, store.failure("resolve", "key_failed", err)
	}
	value, evalErr := resolveScript.Run(ctx, store.client, []string{key.Value()}).Result()
	if evalErr != nil {
		return domain.IssueSnapshot{}, domain.ResolveOutcomeUnspecified, store.failure("resolve", "read_failed", evalErr)
	}
	items, err := resultStrings(value)
	if err != nil || len(items) == 0 {
		return domain.IssueSnapshot{}, domain.ResolveOutcomeUnspecified, store.failure("resolve", "defect", errors.New("battle ticket resolve result is invalid"))
	}
	switch items[0] {
	case "not_found":
		store.observer.RecordStorageOperation("battleticket", "resolve", "not_found")
		return domain.IssueSnapshot{}, domain.ResolveOutcomeNotFound, nil
	case "found":
		snapshot, decodeErr := decodeSnapshot(issueID, items[1:])
		if decodeErr != nil {
			return domain.IssueSnapshot{}, domain.ResolveOutcomeUnspecified, store.failure("resolve", "codec_failed", decodeErr)
		}
		store.observer.RecordStorageOperation("battleticket", "resolve", "found")
		return snapshot, domain.ResolveOutcomeFound, nil
	case "defect":
		return domain.IssueSnapshot{}, domain.ResolveOutcomeUnspecified, store.failure("resolve", "defect", errors.New("battle ticket issue state is corrupt"))
	default:
		return domain.IssueSnapshot{}, domain.ResolveOutcomeUnspecified, store.failure("resolve", "defect", errors.New("battle ticket resolve outcome is unknown"))
	}
}

// Issue 使用 owner Lua 原子创建或精确重放 digest/handle-only record。
// Redis/Lua error 保守返回 commit-unknown；调用方必须复用同一 IssueID 先 Resolve。
func (store *Store) Issue(ctx context.Context, record domain.IssueRecord, observedAt time.Time) (domain.IssueOutcome, error) {
	facts := record.Binding.Facts()
	if !record.Valid() || observedAt.IsZero() || !facts.ExpiresAt.After(observedAt.UTC()) {
		return domain.IssueOutcomeUnspecified, store.failure("issue", "invalid", errors.New("battle ticket issue record is invalid"))
	}
	key, err := store.issueKey(facts.IssueID)
	if err != nil {
		return domain.IssueOutcomeUnspecified, store.failure("issue", "key_failed", err)
	}
	physicalMS, err := expiryMilliseconds(record.PhysicalExpiresAt)
	if err != nil {
		return domain.IssueOutcomeUnspecified, store.failure("issue", "invalid", err)
	}
	values := encodeRecord(record)
	args := make([]any, 0, len(values)+1)
	for _, value := range values {
		args = append(args, value)
	}
	args = append(args, physicalMS)
	value, evalErr := issueScript.Run(ctx, store.client, []string{key.Value()}, args...).Result()
	if evalErr != nil {
		return domain.IssueOutcomeCommitUnknown, store.failure("issue", "commit_unknown", evalErr)
	}
	items, err := resultStrings(value)
	if err != nil || len(items) != 1 {
		return domain.IssueOutcomeUnspecified, store.failure("issue", "defect", errors.New("battle ticket issue result is invalid"))
	}
	switch items[0] {
	case "created":
		return store.result(domain.IssueOutcomeCreated, "created")
	case "replay":
		return store.result(domain.IssueOutcomeReplay, "replay")
	case "conflict":
		return store.result(domain.IssueOutcomeIdempotencyConflict, "conflict")
	case "defect":
		return domain.IssueOutcomeUnspecified, store.failure("issue", "defect", errors.New("battle ticket issue state is corrupt"))
	default:
		return domain.IssueOutcomeUnspecified, store.failure("issue", "defect", errors.New("battle ticket issue outcome is unknown"))
	}
}

// issueKey 使用 IssueID SHA-256 前 128 bits，避免 raw idempotency material 进入 Redis key。
func (store *Store) issueKey(issueID domain.IssueID) (storageredis.Key, error) {
	return store.keyspace.Build(issueDefinitionName, storageredis.DigestIdentity([]byte(issueID.Value())))
}

// result 记录 deterministic success outcome。
func (store *Store) result(outcome domain.IssueOutcome, label string) (domain.IssueOutcome, error) {
	store.observer.RecordStorageOperation("battleticket", "issue", label)
	return outcome, nil
}

// failure 记录固定结果并保留仅供进程内诊断的 cause chain。
func (store *Store) failure(operation string, outcome string, cause error) error {
	store.observer.RecordStorageOperation("battleticket", operation, outcome)
	return fmt.Errorf("battle ticket redis %s failed: %w", operation, cause)
}

// expiryMilliseconds 向上取整 UTC microseconds，防止 physical TTL 早于 replay deadline。
func expiryMilliseconds(value time.Time) (string, error) {
	if value.IsZero() || value.UnixMicro() <= 0 {
		return "", errors.New("battle ticket Redis expiry is invalid")
	}
	return strconv.FormatInt((value.UnixMicro()+999)/1000, 10), nil
}

// resultStrings 严格解析 go-redis Lua array，不接受数字或嵌套值。
func resultStrings(value any) ([]string, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, errors.New("battle ticket Redis script result is not an array")
	}
	result := make([]string, len(items))
	for index, item := range items {
		switch typed := item.(type) {
		case string:
			result[index] = typed
		case []byte:
			result[index] = string(typed)
		default:
			return nil, errors.New("battle ticket Redis script result contains non-string")
		}
	}
	return result, nil
}
