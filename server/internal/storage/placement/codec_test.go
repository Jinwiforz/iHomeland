package placement

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	domain "github.com/jinwiforz/ihomeland/server/internal/placement"
	storagemysql "github.com/jinwiforz/ihomeland/server/internal/storage/mysql"
	storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"
)

// TestDefinitionsBuildPlacementNamespace 固定 owner、kind、TTL 与真实 namespace。
func TestDefinitionsBuildPlacementNamespace(t *testing.T) {
	t.Parallel()
	definitions := Definitions()
	registry, err := storageredis.NewRegistry(definitions)
	if err != nil {
		t.Fatal(err)
	}
	keyspace, err := storageredis.NewKeyspace("test", registry)
	if err != nil {
		t.Fatal(err)
	}
	assignmentKey, err := keyspace.Build(assignmentDefinitionName, "pworld_codec1")
	if err != nil || assignmentKey.Value() != "ih:test:placement:assignment:pworld_codec1" {
		t.Fatalf("assignment key = %q, %v", assignmentKey.Value(), err)
	}
	replayKey, err := keyspace.Build(transitionDefinitionName, strings.Repeat("a", 64))
	if err != nil || !strings.HasPrefix(replayKey.Value(), "ih:test:placement:transition:") {
		t.Fatalf("replay key = %q, %v", replayKey.Value(), err)
	}
	expectedPurposes := map[string]string{
		assignmentDefinitionName: "个人世界当前实例放置",
		transitionDefinitionName: "实例放置变更重放证据",
	}
	for _, definition := range definitions {
		if definition.Owner != "placement" || definition.TTLPolicy != storageredis.TTLRequired || definition.SchemaVersion != redisSchemaVersion || definition.Purpose != expectedPurposes[definition.Name] {
			t.Fatalf("definition = %+v", definition)
		}
	}
}

// TestAllocatorMapsCommitUnknownWithoutPublishingSnapshot 固定 MySQL commit-unknown 的零结果边界。
func TestAllocatorMapsCommitUnknownWithoutPublishingSnapshot(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Microsecond)
	worldID, _ := personalworld.NewPersonalWorldID("pworld_unknown1")
	instanceID, _ := domain.NewWorldInstanceID("winst_unknown1")
	nodeID, _ := domain.NewRuntimeNodeID("rnode_unknown1")
	candidate, _ := domain.NewAssignmentCandidate(worldID, instanceID, nodeID, now, now.Add(time.Minute))
	value := allocator{
		db: &sql.DB{},
		withinTx: func(context.Context, *sql.DB, *sql.TxOptions, func(*sql.Tx) error) error {
			return &storagemysql.TransactionError{Outcome: storagemysql.TransactionCommitUnknown, Err: errors.New("injected")}
		},
	}
	result, status, err := value.reserve(context.Background(), candidate, now)
	if err == nil || status != allocationCommitUnknown || result.snapshot.Valid() {
		t.Fatalf("allocation commit unknown = %v, %v", status, err)
	}
}

// TestAssignmentCodecPreservesUInt64AndMicroseconds 保护超过 IEEE-754 精度边界的 stamp 与精确 expiry。
func TestAssignmentCodecPreservesUInt64AndMicroseconds(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 7, 14, 0, 0, 0, 123456000, time.UTC)
	createdAt := observedAt.Add(-time.Second)
	expiresAt := observedAt.Add(time.Second + 789*time.Microsecond)
	fields := map[string]string{
		"v": "1", "world": "pworld_codec1", "instance": "winst_codec1", "node": "rnode_codec1",
		"generation": canonicalUint(math.MaxUint64 - 1), "fence": canonicalUint(math.MaxUint64),
		"phase": "active", "created_us": canonicalUint(uint64(createdAt.UnixMicro())), "expires_us": canonicalUint(uint64(expiresAt.UnixMicro())),
	}
	snapshot, err := decodeAssignment(fields, observedAt, false)
	if err != nil || snapshot.Generation().Uint64() != math.MaxUint64-1 || snapshot.FencingToken().Uint64() != math.MaxUint64 || !snapshot.Lease().ExpiresAt().Equal(expiresAt) {
		t.Fatalf("decode boundary snapshot = %v, %v", snapshot.Valid(), err)
	}
	for name, mutate := range map[string]func(map[string]string){
		"leading zero":    func(values map[string]string) { values["generation"] = "01" },
		"unknown phase":   func(values map[string]string) { values["phase"] = "sleeping" },
		"unknown version": func(values map[string]string) { values["v"] = "2" },
		"extra field":     func(values map[string]string) { values["extra"] = "x" },
	} {
		t.Run(name, func(t *testing.T) {
			copyFields := make(map[string]string, len(fields)+1)
			for key, value := range fields {
				copyFields[key] = value
			}
			mutate(copyFields)
			if value, decodeErr := decodeAssignment(copyFields, observedAt, false); decodeErr == nil || value.Valid() {
				t.Fatalf("malformed decode = %v, %v", value.Valid(), decodeErr)
			}
		})
	}
}

// TestExpiryMillisecondsRoundsUpWithoutExtendingDomainValidity 固定 Redis 物理 expiry 的向上取整边界。
func TestExpiryMillisecondsRoundsUpWithoutExtendingDomainValidity(t *testing.T) {
	t.Parallel()
	expiresAt := time.UnixMicro(1_700_000_000_000_001).UTC()
	milliseconds, err := expiryMilliseconds(expiresAt)
	if err != nil || milliseconds != "1700000000001" {
		t.Fatalf("expiry milliseconds = %q, %v", milliseconds, err)
	}
	if _, err := canonicalMicroTime(time.UnixMicro(0)); err == nil {
		t.Fatal("epoch boundary must be rejected")
	}
	if _, err := expiryMilliseconds(time.UnixMicro(maximumLuaExactInteger*1000 + 1)); err == nil {
		t.Fatal("Lua double precision overflow must be rejected")
	}
}

// TestLuaScriptsAvoidUnsafeMutationPatterns 固定无 namespace scan、无 uint64 tonumber 与每操作独立 script。
func TestLuaScriptsAvoidUnsafeMutationPatterns(t *testing.T) {
	t.Parallel()
	for name, script := range map[string]string{
		"acquire": acquireScript, "activate": activateScript, "renew": renewScript,
		"revoke": revokeScript, "replace": replaceScript, "qualify": qualifyWriteScript,
	} {
		if strings.Contains(script, "redis.call('KEYS'") || strings.Contains(script, "tonumber(generation)") || strings.Contains(script, "tonumber(fence)") {
			t.Fatalf("%s script contains forbidden operation", name)
		}
		if !strings.Contains(script, "current_complete") || !strings.Contains(script, "same_stamp") {
			t.Fatalf("%s script misses strict validation", name)
		}
	}
}

// FuzzCanonicalDecimalAndAssignmentHydration 保证任意 decimal/Hash 输入不会绕过 constructor 或 panic。
func FuzzCanonicalDecimalAndAssignmentHydration(f *testing.F) {
	for _, seed := range []string{"0", "1", "01", "9007199254740993", "18446744073709551615", "18446744073709551616", "-1", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		parsed, err := parseCanonicalUint(value)
		if err == nil && canonicalUint(parsed) != value {
			t.Fatalf("accepted non-canonical value %q", value)
		}
	})
}
