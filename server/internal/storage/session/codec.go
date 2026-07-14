package session

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	domain "github.com/jinwiforz/ihomeland/server/internal/session"
)

const maximumLuaExactInteger int64 = 1<<53 - 1

// canonicalDigest 把固定SHA-256摘要编码为key-safe lowercase hex。
func canonicalDigest(digest domain.Digest) (string, error) {
	if !digest.Valid() {
		return "", errors.New("session credential digest is invalid")
	}
	value := digest.Bytes()
	encoded := hex.EncodeToString(value[:])
	if len(encoded) != sha256.Size*2 {
		return "", errors.New("session credential digest encoding is invalid")
	}
	return encoded, nil
}

// canonicalMicroTime 返回Lua double可无损表示范围内的正UTC Unix微秒字符串。
func canonicalMicroTime(value time.Time) (string, error) {
	microseconds := value.UTC().UnixMicro()
	if value.IsZero() || microseconds <= 0 || microseconds > maximumLuaExactInteger {
		return "", errors.New("session timestamp is outside redis range")
	}
	return strconv.FormatInt(microseconds, 10), nil
}

// expiryMilliseconds 把逻辑微秒向上取整为Redis PEXPIREAT毫秒，避免提前物理删除。
func expiryMilliseconds(value time.Time) (string, error) {
	microseconds := value.UTC().UnixMicro()
	if value.IsZero() || microseconds <= 0 || microseconds > maximumLuaExactInteger {
		return "", errors.New("session expiry is outside redis range")
	}
	return strconv.FormatInt((microseconds+999)/1000, 10), nil
}

// canonicalEpoch 返回不经Lua number转换的正十进制epoch。
func canonicalEpoch(epoch domain.Epoch) (string, error) {
	if !epoch.Valid() {
		return "", errors.New("session epoch is invalid")
	}
	return strconv.FormatUint(uint64(epoch), 10), nil
}

// encodeChannel 映射ticket唯一实时通道。
func encodeChannel(channel domain.Channel) (string, error) {
	switch channel {
	case domain.ChannelWSS:
		return "wss", nil
	case domain.ChannelTLSTCP:
		return "tls_tcp", nil
	default:
		return "", errors.New("session ticket channel is unknown")
	}
}

// encodeScopes 把固定channel capability编码为canonical逗号列表。
func encodeScopes(scopes domain.ScopeSet) (string, error) {
	values := scopes.Values()
	parts := make([]string, 0, len(values))
	for _, scope := range values {
		switch scope {
		case domain.ScopeControl:
			parts = append(parts, "control")
		case domain.ScopeGameplay:
			parts = append(parts, "gameplay")
		default:
			return "", errors.New("session scope is unknown")
		}
	}
	if len(parts) == 0 {
		return "", errors.New("session ticket scopes are empty")
	}
	return strings.Join(parts, ","), nil
}

// decodeScopes 恢复去重排序的受信ScopeSet。
func decodeScopes(value string) (domain.ScopeSet, error) {
	if value == "" {
		return domain.ScopeSet{}, errors.New("session ticket scopes are empty")
	}
	parts := strings.Split(value, ",")
	values := make([]domain.Scope, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "control":
			values = append(values, domain.ScopeControl)
		case "gameplay":
			values = append(values, domain.ScopeGameplay)
		default:
			return domain.ScopeSet{}, errors.New("session ticket scope is unknown")
		}
	}
	set, err := domain.NewScopeSet(values...)
	if err != nil || len(set.Values()) != len(parts) || encodeScopesMust(set) != value {
		return domain.ScopeSet{}, errors.New("session ticket scopes are not canonical")
	}
	return set, nil
}

// encodeScopesMust 只对已验证ScopeSet生成stable文本。
func encodeScopesMust(scopes domain.ScopeSet) string {
	encoded, _ := encodeScopes(scopes)
	return encoded
}

// encodeReason 映射可幂等持久的invalidation reason。
func encodeReason(reason domain.InvalidationReason) (string, error) {
	switch reason {
	case domain.InvalidationReasonLogout:
		return "logout", nil
	case domain.InvalidationReasonForcedLogout:
		return "forced_logout", nil
	case domain.InvalidationReasonPrincipalBan:
		return "principal_ban", nil
	case domain.InvalidationReasonRefreshReplay:
		return "refresh_replay", nil
	default:
		return "", errors.New("session invalidation reason is unknown")
	}
}

// decodeReason 恢复固定低基数invalidation reason。
func decodeReason(value string) (domain.InvalidationReason, error) {
	switch value {
	case "logout":
		return domain.InvalidationReasonLogout, nil
	case "forced_logout":
		return domain.InvalidationReasonForcedLogout, nil
	case "principal_ban":
		return domain.InvalidationReasonPrincipalBan, nil
	case "refresh_replay":
		return domain.InvalidationReasonRefreshReplay, nil
	default:
		return domain.InvalidationReasonUnspecified, errors.New("session invalidation reason is unknown")
	}
}

// parseCanonicalUint 拒绝空值、leading zero、符号和溢出。
func parseCanonicalUint(value string) (uint64, error) {
	if value == "" || len(value) > 1 && value[0] == '0' {
		return 0, errors.New("session decimal is not canonical")
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || strconv.FormatUint(parsed, 10) != value {
		return 0, errors.New("session decimal is invalid")
	}
	return parsed, nil
}

// parseMicroTime 恢复正UTC Unix微秒。
func parseMicroTime(value string) (time.Time, error) {
	parsed, err := parseCanonicalUint(value)
	if err != nil || parsed > uint64(maximumLuaExactInteger) {
		return time.Time{}, errors.New("session timestamp is invalid")
	}
	return time.UnixMicro(int64(parsed)).UTC(), nil
}

// scriptItems 把go-redis reply规范为string列表，拒绝nil和非bulk string。
func scriptItems(value any) ([]string, error) {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return nil, errors.New("session script result has invalid shape")
	}
	result := make([]string, len(items))
	for index, item := range items {
		text, valid := item.(string)
		if !valid {
			return nil, errors.New("session script result contains invalid type")
		}
		result[index] = text
	}
	return result, nil
}

// parseAuthIdentity 构造store已原子确认的AuthSnapshot identity部分。
func parseAuthIdentity(items []string, offset int) (domain.AuthSnapshot, error) {
	if len(items) < offset+4 {
		return domain.AuthSnapshot{}, errors.New("session auth result is incomplete")
	}
	principal, err := domain.NewPrincipal(items[offset], items[offset+1])
	if err != nil {
		return domain.AuthSnapshot{}, err
	}
	if !validOwnedIdentifier(items[offset], "acc_", false) || !validOwnedIdentifier(items[offset+1], "ply_", false) {
		return domain.AuthSnapshot{}, errors.New("session auth principal namespace is invalid")
	}
	id, err := domain.NewSessionID(items[offset+2])
	if err != nil {
		return domain.AuthSnapshot{}, err
	}
	if !validOwnedIdentifier(items[offset+2], "ses_", true) {
		return domain.AuthSnapshot{}, errors.New("session auth identity namespace is invalid")
	}
	epochValue, err := parseCanonicalUint(items[offset+3])
	if err != nil {
		return domain.AuthSnapshot{}, err
	}
	epoch := domain.Epoch(epochValue)
	if !epoch.Valid() {
		return domain.AuthSnapshot{}, errors.New("session auth epoch is invalid")
	}
	return domain.AuthSnapshot{Principal: principal, SessionID: id, Epoch: epoch}, nil
}

// parseInvalidation 构造单个幂等失效事实。
func parseInvalidation(sessionValue string, epochValue string, reasonValue string) (domain.Invalidation, error) {
	id, err := domain.NewSessionID(sessionValue)
	if err != nil {
		return domain.Invalidation{}, err
	}
	if !validOwnedIdentifier(sessionValue, "ses_", true) {
		return domain.Invalidation{}, errors.New("session invalidation identity namespace is invalid")
	}
	parsedEpoch, err := parseCanonicalUint(epochValue)
	if err != nil || !domain.Epoch(parsedEpoch).Valid() {
		return domain.Invalidation{}, errors.New("session invalidation epoch is invalid")
	}
	reason, err := decodeReason(reasonValue)
	if err != nil {
		return domain.Invalidation{}, err
	}
	return domain.Invalidation{SessionID: id, Epoch: domain.Epoch(parsedEpoch), Reason: reason}, nil
}

// validateBundle 固定Create跨record不变量并提前拒绝无意义Lua调用。
func validateBundle(bundle domain.SessionBundle) error {
	if !bundle.Session.ID.Valid() || !validOwnedIdentifier(bundle.Session.ID.String(), "ses_", true) ||
		!bundle.Session.Principal.Valid() || !validOwnedIdentifier(bundle.Session.Principal.AccountID(), "acc_", false) ||
		!validOwnedIdentifier(bundle.Session.Principal.PlayerID(), "ply_", false) || bundle.Session.Epoch != domain.InitialEpoch ||
		bundle.Session.Status != domain.StatusActive || bundle.Session.ExpiresAt.IsZero() ||
		!bundle.Access.Digest.Valid() || !bundle.Refresh.Digest.Valid() || bundle.Access.Digest == bundle.Refresh.Digest ||
		bundle.Access.SessionID != bundle.Session.ID || bundle.Refresh.SessionID != bundle.Session.ID ||
		bundle.Access.Epoch != bundle.Session.Epoch || bundle.Refresh.Epoch != bundle.Session.Epoch ||
		bundle.Access.ExpiresAt.IsZero() || bundle.Refresh.ExpiresAt.IsZero() ||
		!bundle.Access.ExpiresAt.Before(bundle.Refresh.ExpiresAt) ||
		bundle.Access.ExpiresAt.After(bundle.Session.ExpiresAt) || bundle.Refresh.ExpiresAt.After(bundle.Session.ExpiresAt) {
		return errors.New("session bundle is invalid")
	}
	return nil
}

// validOwnedIdentifier 把通用领域identity收紧到Session Redis schema的owner namespace。
func validOwnedIdentifier(value string, prefix string, extended bool) bool {
	if !strings.HasPrefix(value, prefix) || len(value) <= len(prefix) || len(value) > 128 {
		return false
	}
	for index := len(prefix); index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' {
			continue
		}
		if !extended || character != '-' && character != '_' && character != '.' {
			return false
		}
	}
	return true
}
