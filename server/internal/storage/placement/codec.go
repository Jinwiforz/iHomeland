package placement

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"strconv"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	domain "github.com/jinwiforz/ihomeland/server/internal/placement"
	storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"
)

// transitionFingerprintDomain 隔离 transition fingerprint，避免不同协议字段集合产生跨域碰撞。
const transitionFingerprintDomain = "placement.transition.v1"

// maximumLuaExactInteger 是 Redis Lua double 可以无损表示的最大正整数。
const maximumLuaExactInteger int64 = 1<<53 - 1

// assignmentFieldNames 固定 current Hash 的完整封闭字段集合。
var assignmentFieldNames = map[string]struct{}{
	"v": {}, "world": {}, "instance": {}, "node": {}, "generation": {}, "fence": {},
	"phase": {}, "created_us": {}, "expires_us": {},
}

// replayFieldNames 在完整 assignment 上增加 mutation identity 与首次 outcome。
var replayFieldNames = map[string]struct{}{
	"v": {}, "operation": {}, "fingerprint": {}, "outcome": {}, "world": {}, "instance": {},
	"node": {}, "generation": {}, "fence": {}, "phase": {}, "created_us": {}, "expires_us": {},
}

// newPersonalWorldID 让 MySQL 与 Redis hydration 共享领域 constructor。
func newPersonalWorldID(value string) (personalworld.PersonalWorldID, error) {
	return personalworld.NewPersonalWorldID(value)
}

// decodeAssignment 从 owner Hash 恢复完整 snapshot，缺失、额外或非法字段全部拒绝。
func decodeAssignment(fields map[string]string, observedAt time.Time, replay bool) (domain.AssignmentSnapshot, error) {
	expected := assignmentFieldNames
	definition := assignmentDefinition()
	if replay {
		expected = replayFieldNames
		definition = transitionDefinition()
	}
	if len(fields) != len(expected) {
		return domain.AssignmentSnapshot{}, errors.New("placement redis value has unexpected field count")
	}
	encodedBytes := 0
	for name, value := range fields {
		if _, found := expected[name]; !found {
			return domain.AssignmentSnapshot{}, errors.New("placement redis value contains unknown field")
		}
		encodedBytes += len(name) + len(value)
	}
	if encodedBytes == 0 || encodedBytes > definition.MaxEncodedBytes {
		return domain.AssignmentSnapshot{}, errors.New("placement redis value exceeds encoded budget")
	}
	version, err := parseCanonicalUint(fields["v"])
	if err != nil || version > uint64(^uint16(0)) {
		return domain.AssignmentSnapshot{}, errors.New("placement redis schema version is invalid")
	}
	if err := storageredis.ValidateEncodedValue(definition, uint16(version), make([]byte, encodedBytes)); err != nil {
		return domain.AssignmentSnapshot{}, err
	}
	if replay {
		if !validTransitionOperation(fields["operation"]) || fields["outcome"] != "applied" {
			return domain.AssignmentSnapshot{}, errors.New("placement replay metadata is invalid")
		}
		fingerprint, decodeErr := hex.DecodeString(fields["fingerprint"])
		if decodeErr != nil || len(fingerprint) != sha256.Size {
			return domain.AssignmentSnapshot{}, errors.New("placement replay fingerprint is invalid")
		}
	}
	worldID, err := personalworld.NewPersonalWorldID(fields["world"])
	if err != nil {
		return domain.AssignmentSnapshot{}, err
	}
	instanceID, err := domain.NewWorldInstanceID(fields["instance"])
	if err != nil {
		return domain.AssignmentSnapshot{}, err
	}
	nodeID, err := domain.NewRuntimeNodeID(fields["node"])
	if err != nil {
		return domain.AssignmentSnapshot{}, err
	}
	generationValue, err := parseCanonicalUint(fields["generation"])
	if err != nil {
		return domain.AssignmentSnapshot{}, err
	}
	generation, err := domain.NewAssignmentGeneration(generationValue)
	if err != nil {
		return domain.AssignmentSnapshot{}, err
	}
	fenceValue, err := parseCanonicalUint(fields["fence"])
	if err != nil {
		return domain.AssignmentSnapshot{}, err
	}
	fence, err := domain.NewFencingToken(fenceValue)
	if err != nil {
		return domain.AssignmentSnapshot{}, err
	}
	phase, err := parsePhase(fields["phase"])
	if err != nil {
		return domain.AssignmentSnapshot{}, err
	}
	createdAt, err := parseMicroTime(fields["created_us"])
	if err != nil {
		return domain.AssignmentSnapshot{}, err
	}
	expiresAt, err := parseMicroTime(fields["expires_us"])
	if err != nil {
		return domain.AssignmentSnapshot{}, err
	}
	stamp, err := domain.NewAssignmentStamp(worldID, instanceID, nodeID, generation, fence)
	if err != nil {
		return domain.AssignmentSnapshot{}, err
	}
	return domain.NewAssignmentSnapshot(stamp, phase, createdAt, expiresAt, observedAt)
}

// parseScriptReply 验证 Lua code 与可选 Hash pairs，并拒绝矛盾形状。
func parseScriptReply(value any, observedAt time.Time) (string, domain.AssignmentSnapshot, error) {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return "", domain.AssignmentSnapshot{}, errors.New("placement script result has invalid shape")
	}
	code, ok := items[0].(string)
	if !ok || !validScriptCode(code) {
		return "", domain.AssignmentSnapshot{}, errors.New("placement script result has unknown code")
	}
	if len(items) == 1 {
		return code, domain.AssignmentSnapshot{}, nil
	}
	if (len(items)-1)%2 != 0 {
		return "", domain.AssignmentSnapshot{}, errors.New("placement script result has incomplete fields")
	}
	fields := make(map[string]string, (len(items)-1)/2)
	for index := 1; index < len(items); index += 2 {
		name, nameOK := items[index].(string)
		fieldValue, valueOK := items[index+1].(string)
		if !nameOK || !valueOK {
			return "", domain.AssignmentSnapshot{}, errors.New("placement script result field type is invalid")
		}
		if _, duplicate := fields[name]; duplicate {
			return "", domain.AssignmentSnapshot{}, errors.New("placement script result contains duplicate field")
		}
		fields[name] = fieldValue
	}
	_, replay := fields["operation"]
	snapshot, err := decodeAssignment(fields, observedAt, replay)
	if err != nil {
		return "", domain.AssignmentSnapshot{}, err
	}
	if code == "not_found" || code == "conflict" || code == "expired" {
		return "", domain.AssignmentSnapshot{}, errors.New("placement script returned snapshot for empty outcome")
	}
	return code, snapshot, nil
}

// transitionFingerprint 构造 replay key 与 record 共享的固定 SHA-256 lowercase hex。
func transitionFingerprint(operation string, fields ...string) string {
	hasher := sha256.New()
	writeTransitionField(hasher, transitionFingerprintDomain)
	writeTransitionField(hasher, operation)
	for _, field := range fields {
		writeTransitionField(hasher, field)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// stampFields 返回 Lua 与 fingerprint 使用的完整 assignment identity 字符串。
func stampFields(stamp domain.AssignmentStamp) []string {
	return []string{stamp.WorldID().String(), stamp.InstanceID().String(), stamp.NodeID().String(),
		canonicalUint(stamp.Generation().Uint64()), canonicalUint(stamp.FencingToken().Uint64())}
}

// writeTransitionField 使用 uint32 长度前缀避免 fingerprint 拼接歧义。
func writeTransitionField(hasher hash.Hash, value string) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	_, _ = hasher.Write(length[:])
	_, _ = hasher.Write([]byte(value))
}

// canonicalUint 返回 Lua 不经 double 解释的规范十进制字符串。
func canonicalUint(value uint64) string { return strconv.FormatUint(value, 10) }

// parseCanonicalUint 拒绝空值、正号、空白和 leading zero。
func parseCanonicalUint(value string) (uint64, error) {
	if value == "" || len(value) > 1 && value[0] == '0' {
		return 0, errors.New("decimal value is not canonical")
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || canonicalUint(parsed) != value {
		return 0, errors.New("decimal value is invalid")
	}
	return parsed, nil
}

// canonicalMicroTime 返回正 UTC Unix microseconds；Redis schema 不接受 epoch 前时间。
func canonicalMicroTime(value time.Time) (string, error) {
	if value.IsZero() || value.UnixMicro() <= 0 {
		return "", errors.New("placement timestamp is invalid")
	}
	return strconv.FormatInt(value.UTC().UnixMicro(), 10), nil
}

// parseMicroTime 恢复正 canonical Unix microseconds，不接受 uint64 overflow 或负值。
func parseMicroTime(value string) (time.Time, error) {
	parsed, err := parseCanonicalUint(value)
	if err != nil || parsed > uint64(^uint64(0)>>1) {
		return time.Time{}, errors.New("placement timestamp is invalid")
	}
	return time.UnixMicro(int64(parsed)).UTC(), nil
}

// expiryMilliseconds 向上取整 absolute microseconds，物理 key 最多额外存活不足一毫秒。
func expiryMilliseconds(value time.Time) (string, error) {
	micros := value.UTC().UnixMicro()
	if value.IsZero() || micros <= 0 || micros > maximumLuaExactInteger*1000 {
		return "", errors.New("placement expiry is outside redis range")
	}
	return strconv.FormatInt((micros+999)/1000, 10), nil
}

// replayMilliseconds 把正有界 duration 向上取整为 Redis PEXPIRE milliseconds。
func replayMilliseconds(value time.Duration) (string, error) {
	if value <= 0 {
		return "", errors.New("placement replay TTL must be positive")
	}
	milliseconds := (value + time.Millisecond - 1) / time.Millisecond
	if milliseconds <= 0 {
		return "", errors.New("placement replay TTL is outside redis range")
	}
	return strconv.FormatInt(int64(milliseconds), 10), nil
}

// parsePhase 把持久字符串映射到封闭 starting/active 状态。
func parsePhase(value string) (domain.Phase, error) {
	switch value {
	case "starting":
		return domain.PhaseStarting, nil
	case "active":
		return domain.PhaseActive, nil
	default:
		return domain.PhaseUnspecified, errors.New("placement phase is unknown")
	}
}

// validTransitionOperation 限制 replay record 只能来自已实现 mutation。
func validTransitionOperation(value string) bool {
	switch value {
	case "acquire", "activate", "renew", "revoke", "replace":
		return true
	default:
		return false
	}
}

// validScriptCode 限制 Lua 与 Go outcome parser 的协议集合。
func validScriptCode(value string) bool {
	switch value {
	case "applied", "existing", "replay", "in_progress", "not_found", "conflict", "expired", "defect":
		return true
	default:
		return false
	}
}

// formatCodecError 产生不包含 Hash、key 或 identity 的稳定 codec failure。
// cause 会通过 %w 保留在 error chain 与默认文本中，因此调用方只能传入不含原始持久值、
// Redis key、identity 或 client/driver 文本的固定 codec/domain validation error。
func formatCodecError(operation string, cause error) error {
	return fmt.Errorf("placement storage %s codec failed: %w", operation, cause)
}
