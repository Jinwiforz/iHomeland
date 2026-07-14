package session

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	domain "github.com/jinwiforz/ihomeland/server/internal/session"
	storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"
	redisclient "github.com/redis/go-redis/v9"
)

// Observer 接收固定 adapter/operation/outcome，不接收 key、digest、identity 或 Redis error。
type Observer interface {
	// RecordStorageOperation 记录 adapter、operation 与 outcome 三个低基数字段。
	RecordStorageOperation(string, string, string)
}

// Store 借用共享 standalone Redis client 实现 SessionStore。
//
// Store 没有资源所有权或本地运行态，可并发调用；全部安全迁移由单个owner Lua script
// 线性化。Redis flush 后旧credential只会fail closed，不从其他存储恢复。
type Store struct {
	// client 由storage/redis Component持有，本adapter不关闭。
	client *redisclient.Client
	// keyspace 只允许使用已登记Session owner definitions。
	keyspace *storageredis.Keyspace
	// recordPrefix 供Lua从token/ticket binding构造同namespace session record key。
	recordPrefix string
	// accessPrefix 供refresh rotation原子删除上一枚access digest key。
	accessPrefix string
	// observer 只接收固定低基数结果。
	observer Observer
}

var _ domain.SessionStore = (*Store)(nil)

// New 创建不获取资源的production Session Redis adapter。
func New(client *redisclient.Client, keyspace *storageredis.Keyspace, observer Observer) (*Store, error) {
	if client == nil || keyspace == nil || observer == nil {
		return nil, errors.New("session store requires redis client, keyspace, and observer")
	}
	for _, name := range []string{sessionRecordDefinitionName, sessionAccessDefinitionName, sessionRefreshDefinitionName, sessionTicketDefinitionName, sessionPrincipalDefinitionName} {
		if _, err := keyspace.Build(name, "validation"); err != nil {
			return nil, errors.New("session keyspace is missing required definitions")
		}
	}
	recordPrefix, err := keyPrefix(keyspace, sessionRecordDefinitionName)
	if err != nil {
		return nil, err
	}
	accessPrefix, err := keyPrefix(keyspace, sessionAccessDefinitionName)
	if err != nil {
		return nil, err
	}
	return &Store{client: client, keyspace: keyspace, recordPrefix: recordPrefix, accessPrefix: accessPrefix, observer: observer}, nil
}

// Create 原子创建session、首对token与principal失效索引。
func (store *Store) Create(ctx context.Context, bundle domain.SessionBundle) (domain.StoreOutcome, error) {
	if err := validateBundle(bundle); err != nil {
		return domain.StoreOutcomeUnspecified, store.failure("create", "invalid", err)
	}
	recordKey, err := store.recordKey(bundle.Session.ID)
	if err != nil {
		return domain.StoreOutcomeUnspecified, store.failure("create", "key_failed", err)
	}
	accessKey, accessDigest, err := store.digestKey(sessionAccessDefinitionName, bundle.Access.Digest)
	if err != nil {
		return domain.StoreOutcomeUnspecified, store.failure("create", "key_failed", err)
	}
	refreshKey, _, err := store.digestKey(sessionRefreshDefinitionName, bundle.Refresh.Digest)
	if err != nil {
		return domain.StoreOutcomeUnspecified, store.failure("create", "key_failed", err)
	}
	principalKey, err := store.principalKey(bundle.Session.Principal)
	if err != nil {
		return domain.StoreOutcomeUnspecified, store.failure("create", "key_failed", err)
	}
	epoch, epochErr := canonicalEpoch(bundle.Session.Epoch)
	sessionUS, sessionErr := canonicalMicroTime(bundle.Session.ExpiresAt)
	sessionMS, sessionTTLErr := expiryMilliseconds(bundle.Session.ExpiresAt)
	accessUS, accessErr := canonicalMicroTime(bundle.Access.ExpiresAt)
	accessMS, accessTTLErr := expiryMilliseconds(bundle.Access.ExpiresAt)
	refreshUS, refreshErr := canonicalMicroTime(bundle.Refresh.ExpiresAt)
	refreshMS, refreshTTLErr := expiryMilliseconds(bundle.Refresh.ExpiresAt)
	if errors.Join(epochErr, sessionErr, sessionTTLErr, accessErr, accessTTLErr, refreshErr, refreshTTLErr) != nil {
		return domain.StoreOutcomeUnspecified, store.failure("create", "invalid", errors.New("session bundle time or epoch is outside redis range"))
	}
	value, evalErr := createScript.Run(ctx, store.client,
		[]string{recordKey.Value(), accessKey.Value(), refreshKey.Value(), principalKey.Value()},
		bundle.Session.ID.String(), bundle.Session.Principal.AccountID(), bundle.Session.Principal.PlayerID(), epoch,
		sessionUS, sessionMS, accessUS, accessMS, sessionUS, sessionMS, accessDigest, refreshUS, refreshMS, store.recordPrefix).Result()
	return store.parseOutcome("create", value, evalErr)
}

// ResolveAccess 原子校验access、session status、epoch与逻辑expiry。
func (store *Store) ResolveAccess(ctx context.Context, digest domain.Digest, now time.Time) (domain.AuthSnapshot, domain.StoreOutcome, error) {
	accessKey, _, err := store.digestKey(sessionAccessDefinitionName, digest)
	if err != nil || now.IsZero() {
		return domain.AuthSnapshot{}, domain.StoreOutcomeUnspecified, store.failure("resolve_access", "invalid", errors.New("access lookup input is invalid"))
	}
	nowUS, err := canonicalMicroTime(now)
	if err != nil {
		return domain.AuthSnapshot{}, domain.StoreOutcomeUnspecified, store.failure("resolve_access", "invalid", err)
	}
	value, evalErr := resolveAccessScript.Run(ctx, store.client, []string{accessKey.Value()}, nowUS, store.recordPrefix).Result()
	if evalErr != nil {
		return domain.AuthSnapshot{}, domain.StoreOutcomeUnspecified, store.failure("resolve_access", "read_failed", evalErr)
	}
	items, err := scriptItems(value)
	if err != nil {
		return domain.AuthSnapshot{}, domain.StoreOutcomeUnspecified, store.failure("resolve_access", "defect", err)
	}
	if items[0] != "applied" {
		outcome, mapErr := mapOutcome(items[0])
		if mapErr != nil {
			return domain.AuthSnapshot{}, domain.StoreOutcomeUnspecified, store.failure("resolve_access", "defect", mapErr)
		}
		store.observe("resolve_access", items[0])
		return domain.AuthSnapshot{}, outcome, nil
	}
	if len(items) != 5 {
		return domain.AuthSnapshot{}, domain.StoreOutcomeUnspecified, store.failure("resolve_access", "defect", errors.New("access result has invalid shape"))
	}
	snapshot, err := parseAuthIdentity(items, 1)
	if err != nil {
		return domain.AuthSnapshot{}, domain.StoreOutcomeUnspecified, store.failure("resolve_access", "codec_failed", err)
	}
	store.observe("resolve_access", "applied")
	return snapshot, domain.StoreOutcomeApplied, nil
}

// RotateRefresh 原子消费旧refresh、撤销旧access并写入新pair。
func (store *Store) RotateRefresh(ctx context.Context, rotation domain.Rotation) (domain.AuthSnapshot, domain.Invalidation, domain.StoreOutcome, error) {
	if !rotation.PresentedRefresh.Valid() || !rotation.Access.Digest.Valid() || !rotation.Refresh.Digest.Valid() ||
		rotation.Access.Digest == rotation.Refresh.Digest || rotation.PresentedRefresh == rotation.Access.Digest ||
		rotation.PresentedRefresh == rotation.Refresh.Digest || rotation.Now.IsZero() || rotation.Access.ExpiresAt.IsZero() || rotation.Refresh.ExpiresAt.IsZero() {
		return domain.AuthSnapshot{}, domain.Invalidation{}, domain.StoreOutcomeUnspecified, store.failure("rotate_refresh", "invalid", errors.New("refresh rotation is invalid"))
	}
	if !rotation.Now.Before(rotation.Access.ExpiresAt) || !rotation.Now.Before(rotation.Refresh.ExpiresAt) {
		return domain.AuthSnapshot{}, domain.Invalidation{}, domain.StoreOutcomeUnspecified, store.failure("rotate_refresh", "invalid", errors.New("refresh rotation expiry is not in the future"))
	}
	presentedKey, _, err := store.digestKey(sessionRefreshDefinitionName, rotation.PresentedRefresh)
	if err != nil {
		return domain.AuthSnapshot{}, domain.Invalidation{}, domain.StoreOutcomeUnspecified, store.failure("rotate_refresh", "key_failed", err)
	}
	accessKey, accessDigest, err := store.digestKey(sessionAccessDefinitionName, rotation.Access.Digest)
	if err != nil {
		return domain.AuthSnapshot{}, domain.Invalidation{}, domain.StoreOutcomeUnspecified, store.failure("rotate_refresh", "key_failed", err)
	}
	refreshKey, _, err := store.digestKey(sessionRefreshDefinitionName, rotation.Refresh.Digest)
	if err != nil {
		return domain.AuthSnapshot{}, domain.Invalidation{}, domain.StoreOutcomeUnspecified, store.failure("rotate_refresh", "key_failed", err)
	}
	nowUS, nowErr := canonicalMicroTime(rotation.Now)
	accessUS, accessErr := canonicalMicroTime(rotation.Access.ExpiresAt)
	refreshUS, refreshErr := canonicalMicroTime(rotation.Refresh.ExpiresAt)
	if nowErr != nil || accessErr != nil || refreshErr != nil {
		return domain.AuthSnapshot{}, domain.Invalidation{}, domain.StoreOutcomeUnspecified, store.failure("rotate_refresh", "invalid", errors.New("refresh expiry is invalid"))
	}
	value, evalErr := rotateRefreshScript.Run(ctx, store.client,
		[]string{presentedKey.Value(), accessKey.Value(), refreshKey.Value()}, nowUS, store.recordPrefix, store.accessPrefix,
		accessUS, accessDigest, refreshUS).Result()
	if evalErr != nil {
		return domain.AuthSnapshot{}, domain.Invalidation{}, domain.StoreOutcomeUnspecified, store.failure("rotate_refresh", "commit_unknown", evalErr)
	}
	items, err := scriptItems(value)
	if err != nil {
		return domain.AuthSnapshot{}, domain.Invalidation{}, domain.StoreOutcomeUnspecified, store.failure("rotate_refresh", "defect", err)
	}
	if items[0] == "replayed" {
		if len(items) != 4 {
			return domain.AuthSnapshot{}, domain.Invalidation{}, domain.StoreOutcomeUnspecified, store.failure("rotate_refresh", "defect", errors.New("refresh replay result has invalid shape"))
		}
		invalidation, parseErr := parseInvalidation(items[1], items[2], items[3])
		if parseErr != nil {
			return domain.AuthSnapshot{}, domain.Invalidation{}, domain.StoreOutcomeUnspecified, store.failure("rotate_refresh", "codec_failed", parseErr)
		}
		store.observe("rotate_refresh", "replayed")
		return domain.AuthSnapshot{}, invalidation, domain.StoreOutcomeReplayed, nil
	}
	if items[0] != "applied" {
		outcome, mapErr := mapOutcome(items[0])
		if mapErr != nil {
			return domain.AuthSnapshot{}, domain.Invalidation{}, domain.StoreOutcomeUnspecified, store.failure("rotate_refresh", "defect", mapErr)
		}
		store.observe("rotate_refresh", items[0])
		return domain.AuthSnapshot{}, domain.Invalidation{}, outcome, nil
	}
	if len(items) != 7 {
		return domain.AuthSnapshot{}, domain.Invalidation{}, domain.StoreOutcomeUnspecified, store.failure("rotate_refresh", "defect", errors.New("refresh result has invalid shape"))
	}
	snapshot, err := parseAuthIdentity(items, 1)
	if err != nil {
		return domain.AuthSnapshot{}, domain.Invalidation{}, domain.StoreOutcomeUnspecified, store.failure("rotate_refresh", "codec_failed", err)
	}
	snapshot.AccessExpiresAt, err = parseMicroTime(items[5])
	if err == nil {
		snapshot.RefreshExpiresAt, err = parseMicroTime(items[6])
	}
	if err != nil {
		return domain.AuthSnapshot{}, domain.Invalidation{}, domain.StoreOutcomeUnspecified, store.failure("rotate_refresh", "codec_failed", err)
	}
	store.observe("rotate_refresh", "applied")
	return snapshot, domain.Invalidation{}, domain.StoreOutcomeApplied, nil
}

// IssueTicket 只在current session/epoch仍有效时写入一次性ticket。
func (store *Store) IssueTicket(ctx context.Context, record domain.TicketRecord, now time.Time) (domain.StoreOutcome, error) {
	channel, channelErr := encodeChannel(record.Channel)
	scopes, scopesErr := encodeScopes(record.Scopes)
	if !record.Digest.Valid() || !record.SessionID.Valid() || !record.Epoch.Valid() || !record.Endpoint.Valid() ||
		record.Endpoint.Channel() != record.Channel || record.ExpiresAt.IsZero() || now.IsZero() || channelErr != nil || scopesErr != nil ||
		!now.Before(record.ExpiresAt) ||
		(channel == "wss" && scopes != "control") || (channel == "tls_tcp" && scopes != "gameplay") {
		return domain.StoreOutcomeUnspecified, store.failure("issue_ticket", "invalid", errors.New("ticket record is invalid"))
	}
	ticketKey, _, err := store.digestKey(sessionTicketDefinitionName, record.Digest)
	if err != nil {
		return domain.StoreOutcomeUnspecified, store.failure("issue_ticket", "key_failed", err)
	}
	recordKey, err := store.recordKey(record.SessionID)
	if err != nil {
		return domain.StoreOutcomeUnspecified, store.failure("issue_ticket", "key_failed", err)
	}
	nowUS, nowErr := canonicalMicroTime(now)
	epoch, epochErr := canonicalEpoch(record.Epoch)
	expiresUS, expiresErr := canonicalMicroTime(record.ExpiresAt)
	expiresMS, millisecondsErr := expiryMilliseconds(record.ExpiresAt)
	if nowErr != nil || epochErr != nil || expiresErr != nil || millisecondsErr != nil {
		return domain.StoreOutcomeUnspecified, store.failure("issue_ticket", "invalid", errors.New("ticket expiry is invalid"))
	}
	value, evalErr := issueTicketScript.Run(ctx, store.client, []string{ticketKey.Value(), recordKey.Value()},
		nowUS, epoch, record.SessionID.String(), channel, record.Endpoint.Host(), strconv.FormatUint(uint64(record.Endpoint.Port()), 10), expiresUS, scopes, expiresMS).Result()
	return store.parseOutcome("issue_ticket", value, evalErr)
}

// ConsumeTicket 仅在完整listener binding匹配后至多消费一次。
func (store *Store) ConsumeTicket(ctx context.Context, digest domain.Digest, channel domain.Channel, endpoint domain.Endpoint, now time.Time) (domain.AuthSnapshot, domain.StoreOutcome, error) {
	channelValue, channelErr := encodeChannel(channel)
	if !digest.Valid() || !endpoint.Valid() || endpoint.Channel() != channel || now.IsZero() || channelErr != nil {
		return domain.AuthSnapshot{}, domain.StoreOutcomeUnspecified, store.failure("consume_ticket", "invalid", errors.New("ticket consume input is invalid"))
	}
	ticketKey, _, err := store.digestKey(sessionTicketDefinitionName, digest)
	if err != nil {
		return domain.AuthSnapshot{}, domain.StoreOutcomeUnspecified, store.failure("consume_ticket", "key_failed", err)
	}
	nowUS, err := canonicalMicroTime(now)
	if err != nil {
		return domain.AuthSnapshot{}, domain.StoreOutcomeUnspecified, store.failure("consume_ticket", "invalid", err)
	}
	value, evalErr := consumeTicketScript.Run(ctx, store.client, []string{ticketKey.Value()}, nowUS, channelValue,
		endpoint.Host(), strconv.FormatUint(uint64(endpoint.Port()), 10), store.recordPrefix).Result()
	if evalErr != nil {
		return domain.AuthSnapshot{}, domain.StoreOutcomeUnspecified, store.failure("consume_ticket", "commit_unknown", evalErr)
	}
	items, err := scriptItems(value)
	if err != nil {
		return domain.AuthSnapshot{}, domain.StoreOutcomeUnspecified, store.failure("consume_ticket", "defect", err)
	}
	if items[0] != "applied" {
		outcome, mapErr := mapOutcome(items[0])
		if mapErr != nil {
			return domain.AuthSnapshot{}, domain.StoreOutcomeUnspecified, store.failure("consume_ticket", "defect", mapErr)
		}
		store.observe("consume_ticket", items[0])
		return domain.AuthSnapshot{}, outcome, nil
	}
	if len(items) != 6 {
		return domain.AuthSnapshot{}, domain.StoreOutcomeUnspecified, store.failure("consume_ticket", "defect", errors.New("ticket result has invalid shape"))
	}
	snapshot, err := parseAuthIdentity(items, 1)
	if err == nil {
		snapshot.Scopes, err = decodeScopes(items[5])
	}
	if err != nil {
		return domain.AuthSnapshot{}, domain.StoreOutcomeUnspecified, store.failure("consume_ticket", "codec_failed", err)
	}
	store.observe("consume_ticket", "applied")
	return snapshot, domain.StoreOutcomeApplied, nil
}

// InvalidateSession 单调推进epoch并幂等返回首次invalidation。
func (store *Store) InvalidateSession(ctx context.Context, id domain.SessionID, reason domain.InvalidationReason) (domain.Invalidation, domain.StoreOutcome, error) {
	reasonValue, reasonErr := encodeReason(reason)
	if !id.Valid() || reasonErr != nil {
		return domain.Invalidation{}, domain.StoreOutcomeUnspecified, store.failure("invalidate_session", "invalid", errors.New("session invalidation input is invalid"))
	}
	key, err := store.recordKey(id)
	if err != nil {
		return domain.Invalidation{}, domain.StoreOutcomeUnspecified, store.failure("invalidate_session", "key_failed", err)
	}
	value, evalErr := invalidateSessionScript.Run(ctx, store.client, []string{key.Value()}, id.String(), reasonValue).Result()
	if evalErr != nil {
		return domain.Invalidation{}, domain.StoreOutcomeUnspecified, store.failure("invalidate_session", "commit_unknown", evalErr)
	}
	items, err := scriptItems(value)
	if err != nil {
		return domain.Invalidation{}, domain.StoreOutcomeUnspecified, store.failure("invalidate_session", "defect", err)
	}
	if items[0] == "not_found" {
		store.observe("invalidate_session", "not_found")
		return domain.Invalidation{}, domain.StoreOutcomeNotFound, nil
	}
	if items[0] == "defect" || len(items) != 4 || items[0] != "applied" && items[0] != "invalidated" {
		return domain.Invalidation{}, domain.StoreOutcomeUnspecified, store.failure("invalidate_session", "defect", errors.New("invalidation result is invalid"))
	}
	invalidation, err := parseInvalidation(items[1], items[2], items[3])
	if err != nil {
		return domain.Invalidation{}, domain.StoreOutcomeUnspecified, store.failure("invalidate_session", "codec_failed", err)
	}
	outcome := domain.StoreOutcomeApplied
	if items[0] == "invalidated" {
		outcome = domain.StoreOutcomeInvalidated
	}
	store.observe("invalidate_session", items[0])
	return invalidation, outcome, nil
}

// InvalidatePrincipal 在单个standalone Lua script内撤销principal全部现有sessions。
func (store *Store) InvalidatePrincipal(ctx context.Context, principal domain.Principal, reason domain.InvalidationReason) ([]domain.Invalidation, error) {
	reasonValue, reasonErr := encodeReason(reason)
	if !principal.Valid() || reasonErr != nil {
		return nil, store.failure("invalidate_principal", "invalid", errors.New("principal invalidation input is invalid"))
	}
	key, err := store.principalKey(principal)
	if err != nil {
		return nil, store.failure("invalidate_principal", "key_failed", err)
	}
	value, evalErr := invalidatePrincipalScript.Run(ctx, store.client, []string{key.Value()}, principal.AccountID(), principal.PlayerID(), reasonValue, store.recordPrefix).Result()
	if evalErr != nil {
		return nil, store.failure("invalidate_principal", "commit_unknown", evalErr)
	}
	items, err := scriptItems(value)
	if err != nil || items[0] != "applied" || (len(items)-1)%3 != 0 {
		return nil, store.failure("invalidate_principal", "defect", errors.New("principal invalidation result is invalid"))
	}
	result := make([]domain.Invalidation, 0, (len(items)-1)/3)
	for index := 1; index < len(items); index += 3 {
		invalidation, parseErr := parseInvalidation(items[index], items[index+1], items[index+2])
		if parseErr != nil {
			return nil, store.failure("invalidate_principal", "codec_failed", parseErr)
		}
		result = append(result, invalidation)
	}
	store.observe("invalidate_principal", "applied")
	return result, nil
}

// parseOutcome 处理不携带projection的script结果。
func (store *Store) parseOutcome(operation string, value any, evalErr error) (domain.StoreOutcome, error) {
	if evalErr != nil {
		return domain.StoreOutcomeUnspecified, store.failure(operation, "commit_unknown", evalErr)
	}
	items, err := scriptItems(value)
	if err != nil || len(items) != 1 {
		return domain.StoreOutcomeUnspecified, store.failure(operation, "defect", errors.New("session script outcome has invalid shape"))
	}
	outcome, err := mapOutcome(items[0])
	if err != nil {
		return domain.StoreOutcomeUnspecified, store.failure(operation, "defect", err)
	}
	store.observe(operation, items[0])
	return outcome, nil
}

// mapOutcome 把固定Lua code映射到既有SessionStore outcome。
func mapOutcome(code string) (domain.StoreOutcome, error) {
	switch code {
	case "applied":
		return domain.StoreOutcomeApplied, nil
	case "not_found":
		return domain.StoreOutcomeNotFound, nil
	case "expired":
		return domain.StoreOutcomeExpired, nil
	case "replayed":
		return domain.StoreOutcomeReplayed, nil
	case "epoch_mismatch":
		return domain.StoreOutcomeEpochMismatch, nil
	case "invalidated":
		return domain.StoreOutcomeInvalidated, nil
	case "conflict":
		return domain.StoreOutcomeConflict, nil
	case "defect":
		return domain.StoreOutcomeUnspecified, errors.New("session redis state is corrupt")
	default:
		return domain.StoreOutcomeUnspecified, errors.New("session script outcome is unknown")
	}
}

// recordKey 构造默认格式不暴露SessionID的record key。
func (store *Store) recordKey(id domain.SessionID) (storageredis.Key, error) {
	if !id.Valid() || !validOwnedIdentifier(id.String(), "ses_", true) {
		return storageredis.Key{}, errors.New("session ID is invalid")
	}
	return store.keyspace.Build(sessionRecordDefinitionName, id.String())
}

// digestKey 构造credential digest key并返回canonical hex segment。
func (store *Store) digestKey(definitionName string, digest domain.Digest) (storageredis.Key, string, error) {
	encoded, err := canonicalDigest(digest)
	if err != nil {
		return storageredis.Key{}, "", err
	}
	key, err := store.keyspace.Build(definitionName, encoded)
	return key, encoded, err
}

// principalKey 使用account/player长度前缀摘要避免拼接歧义和raw identity namespace暴露。
func (store *Store) principalKey(principal domain.Principal) (storageredis.Key, error) {
	if !principal.Valid() || !validOwnedIdentifier(principal.AccountID(), "acc_", false) || !validOwnedIdentifier(principal.PlayerID(), "ply_", false) {
		return storageredis.Key{}, errors.New("session principal is invalid")
	}
	material := fmt.Sprintf("%d:%s:%d:%s", len(principal.AccountID()), principal.AccountID(), len(principal.PlayerID()), principal.PlayerID())
	return store.keyspace.Build(sessionPrincipalDefinitionName, storageredis.DigestIdentity([]byte(material)))
}

// keyPrefix 从已验证validation key派生同definition prefix，仅作为Lua内部key构造参数。
func keyPrefix(keyspace *storageredis.Keyspace, definitionName string) (string, error) {
	const marker = "validation"
	key, err := keyspace.Build(definitionName, marker)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(key.Value(), marker) {
		return "", errors.New("session key prefix derivation failed")
	}
	return strings.TrimSuffix(key.Value(), marker), nil
}

// adapterError 默认只暴露固定operation/outcome，底层cause仅供受控errors.Is/As。
type adapterError struct {
	// operation 是代码定义的 SessionStore 操作。
	operation string
	// outcome 是固定低基数失败分类。
	outcome string
	// cause 只供受控 errors.Is/As 使用，不参与 Error 文本。
	cause error
}

// Error 返回不含Redis key/value、identity、digest或driver文本的稳定错误。
func (failure *adapterError) Error() string {
	return fmt.Sprintf("session storage %s failed (%s)", failure.operation, failure.outcome)
}

// Unwrap 返回受控内部诊断使用的cause。
func (failure *adapterError) Unwrap() error { return failure.cause }

// failure 记录固定失败分类并构造安全错误。
func (store *Store) failure(operation string, outcome string, cause error) error {
	store.observe(operation, outcome)
	return &adapterError{operation: operation, outcome: outcome, cause: cause}
}

// observe 只转发固定adapter/operation/outcome。
func (store *Store) observe(operation string, outcome string) {
	store.observer.RecordStorageOperation("session", operation, outcome)
}
