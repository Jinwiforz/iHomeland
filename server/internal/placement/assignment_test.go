package placement

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
)

// TestPlacementIdentifiers 验证 namespace、长度和 ASCII 约束不会接受 endpoint 或空 identity。
func TestPlacementIdentifiers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		value     string
		worldIDOK bool
		nodeIDOK  bool
	}{
		{name: "world instance", value: "winst_0123AbCd", worldIDOK: true},
		{name: "runtime node", value: "rnode_0123AbCd", nodeIDOK: true},
		{name: "empty suffix", value: "winst_"},
		{name: "wrong namespace", value: "pworld_0123"},
		{name: "endpoint characters", value: "rnode_https://host:443"},
		{name: "unicode", value: "winst_世界"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			worldID, worldErr := NewWorldInstanceID(test.value)
			if (worldErr == nil) != test.worldIDOK || worldID.Valid() != test.worldIDOK {
				t.Fatalf("NewWorldInstanceID(%q) valid=%v err=%v", test.value, worldID.Valid(), worldErr)
			}
			nodeID, nodeErr := NewRuntimeNodeID(test.value)
			if (nodeErr == nil) != test.nodeIDOK || nodeID.Valid() != test.nodeIDOK {
				t.Fatalf("NewRuntimeNodeID(%q) valid=%v err=%v", test.value, nodeID.Valid(), nodeErr)
			}
		})
	}
}

// TestGenerationAndFencingToken 验证 generation/token 零值拒绝和精确 adapter 投影彼此独立。
func TestGenerationAndFencingToken(t *testing.T) {
	t.Parallel()
	if _, err := NewAssignmentGeneration(0); err == nil {
		t.Fatal("zero generation was accepted")
	}
	if _, err := NewFencingToken(0); err == nil {
		t.Fatal("zero fencing token was accepted")
	}
	generation, err := NewAssignmentGeneration(7)
	if err != nil || generation.Uint64() != 7 || generation.String() != "7" {
		t.Fatalf("generation mismatch: value=%v err=%v", generation, err)
	}
	token, err := NewFencingToken(11)
	if err != nil || token.Uint64() != 11 || !token.Valid() {
		t.Fatalf("token mismatch: value=%v err=%v", token, err)
	}
}

// TestAssignmentSnapshotRoundTrip 保护 UTC normalization、值语义和 starting->active transition。
func TestAssignmentSnapshotRoundTrip(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 7, 13, 12, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	createdAt := observedAt.Add(-time.Second)
	expiresAt := observedAt.Add(30 * time.Second)
	stamp := mustAssignmentStamp(t, 1, 3, "winst_roundtrip", "rnode_alpha")
	snapshot, err := NewAssignmentSnapshot(stamp, PhaseStarting, createdAt, expiresAt, observedAt)
	if err != nil {
		t.Fatalf("NewAssignmentSnapshot: %v", err)
	}
	if !snapshot.ValidAt(observedAt) || snapshot.ValidAt(createdAt.Add(-time.Nanosecond)) || snapshot.CreatedAt().Location() != time.UTC || snapshot.Lease().ExpiresAt().Location() != time.UTC {
		t.Fatalf("snapshot did not normalize UTC or validate: %#v", snapshot)
	}
	active, err := snapshot.Activate(observedAt)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if active.Phase() != PhaseActive || !active.Stamp().Equal(snapshot.Stamp()) || active.Lease().ExpiresAt() != snapshot.Lease().ExpiresAt() {
		t.Fatal("activate changed immutable assignment facts")
	}
	renewed, err := active.Renew(expiresAt.Add(30*time.Second), observedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if renewed.FencingToken() != active.FencingToken() || !renewed.Lease().ExpiresAt().After(active.Lease().ExpiresAt()) {
		t.Fatal("renew changed fence or did not advance expiry")
	}
}

// TestExpiredAssignmentHydration 验证 production adapter 可恢复旧 stamp，但过期值不能继续推进。
func TestExpiredAssignmentHydration(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 13, 4, 0, 0, 0, time.UTC)
	createdAt := now.Add(-2 * time.Minute)
	expiresAt := now.Add(-time.Minute)
	stamp := mustAssignmentStamp(t, 2, 5, "winst_expired", "rnode_alpha")
	snapshot, err := NewAssignmentSnapshot(stamp, PhaseStarting, createdAt, expiresAt, now)
	if err != nil || !snapshot.Valid() {
		t.Fatalf("expired snapshot hydration failed: snapshot=%#v err=%v", snapshot, err)
	}
	if snapshot.ValidAt(now) {
		t.Fatal("expired hydrated snapshot remained valid at observation time")
	}
	if _, err := snapshot.Activate(now); err == nil {
		t.Fatal("expired hydrated snapshot activated")
	}
	if _, err := snapshot.Renew(now.Add(time.Minute), now); err == nil {
		t.Fatal("expired hydrated snapshot renewed")
	}
}

// TestLeaseExpiryBoundary 确认等于 expiry 的请求已经失效且不能续租。
func TestLeaseExpiryBoundary(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 13, 4, 0, 0, 0, time.UTC)
	token := mustFencingToken(t, 1)
	lease, err := NewLease(token, now.Add(time.Second), now)
	if err != nil {
		t.Fatalf("NewLease: %v", err)
	}
	if !lease.ValidAt(now.Add(time.Second - time.Nanosecond)) {
		t.Fatal("lease expired before boundary")
	}
	if lease.ValidAt(now.Add(time.Second)) || lease.ValidAt(now.Add(2*time.Second)) {
		t.Fatal("lease remained valid at or after boundary")
	}
	if _, err := NewLease(token, now, now); err == nil {
		t.Fatal("constructor accepted boundary-expired lease")
	}
}

// TestMalformedAssignmentHydration 覆盖 snapshot 缺失字段、未知 phase 与非法时间关系。
func TestMalformedAssignmentHydration(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 13, 4, 0, 0, 0, time.UTC)
	validStamp := mustAssignmentStamp(t, 1, 1, "winst_valid", "rnode_valid")
	tests := []struct {
		name      string
		stamp     AssignmentStamp
		phase     Phase
		createdAt time.Time
		expiresAt time.Time
		observed  time.Time
	}{
		{name: "zero stamp", phase: PhaseStarting, createdAt: now, expiresAt: now.Add(time.Second), observed: now},
		{name: "unknown phase", stamp: validStamp, phase: Phase(99), createdAt: now, expiresAt: now.Add(time.Second), observed: now},
		{name: "future created time", stamp: validStamp, phase: PhaseStarting, createdAt: now.Add(time.Second), expiresAt: now.Add(2 * time.Second), observed: now},
		{name: "expiry not after created time", stamp: validStamp, phase: PhaseStarting, createdAt: now, expiresAt: now, observed: now},
		{name: "zero observed time", stamp: validStamp, phase: PhaseStarting, createdAt: now, expiresAt: now.Add(time.Second)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if snapshot, err := NewAssignmentSnapshot(test.stamp, test.phase, test.createdAt, test.expiresAt, test.observed); err == nil || snapshot.Valid() {
				t.Fatalf("malformed snapshot accepted: %#v", snapshot)
			}
		})
	}
}

// TestPlacementSensitiveFormatting 防止 `%v`、`%#v` 和 slog 展开 node 或 fencing token。
func TestPlacementSensitiveFormatting(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 13, 4, 0, 0, 0, time.UTC)
	stamp := mustAssignmentStamp(t, 42, 987654321, "winst_sensitive", "rnode_sensitive")
	snapshot, err := NewAssignmentSnapshot(stamp, PhaseActive, now, now.Add(time.Minute), now)
	if err != nil {
		t.Fatalf("NewAssignmentSnapshot: %v", err)
	}
	fence, err := NewWriteFence(stamp)
	if err != nil {
		t.Fatalf("NewWriteFence: %v", err)
	}
	candidate := mustCandidate(t, stamp.WorldID(), "winst_candidate", "rnode_candidate", now, time.Minute)
	acquireRequest, err := NewAcquireRequest(candidate, now)
	if err != nil {
		t.Fatalf("NewAcquireRequest: %v", err)
	}
	stampRequest, err := NewStampRequest(stamp, now)
	if err != nil {
		t.Fatalf("NewStampRequest: %v", err)
	}
	renewRequest, err := NewRenewRequest(stamp, now, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("NewRenewRequest: %v", err)
	}
	replaceCommand := mustReplaceCommand(t, stamp, "winst_replacement", "rnode_replacement")
	replacementCandidate := mustCandidate(t, stamp.WorldID(), replaceCommand.SuccessorID().String(), replaceCommand.TargetNodeID().String(), now, time.Minute)
	replaceRequest, err := NewReplaceRequest(stamp, replacementCandidate, now)
	if err != nil {
		t.Fatalf("NewReplaceRequest: %v", err)
	}
	values := []any{stamp.FencingToken(), stamp, snapshot, fence, candidate, acquireRequest, stampRequest, renewRequest, replaceCommand, replaceRequest}
	for _, value := range values {
		logValue := slog.AnyValue(value).Resolve()
		if logValue.Kind() != slog.KindString {
			t.Fatalf("placement LogValue kind=%s for %T", logValue.Kind(), value)
		}
		formatted := fmt.Sprintf("%v %#v %s", value, value, logValue.String())
		for _, forbidden := range []string{"987654321", "rnode_sensitive", "rnode_candidate", "rnode_replacement"} {
			if strings.Contains(formatted, forbidden) {
				t.Fatalf("sensitive placement material %q leaked: %q", forbidden, formatted)
			}
		}
	}
}

// FuzzPlacementIdentifiers 验证任意输入只能生成满足 namespace 与 ASCII 约束的 identity。
func FuzzPlacementIdentifiers(f *testing.F) {
	f.Add("winst_seed")
	f.Add("rnode_seed")
	f.Add("https://127.0.0.1:443")
	f.Fuzz(func(t *testing.T, value string) {
		if id, err := NewWorldInstanceID(value); err == nil {
			if !id.Valid() || !strings.HasPrefix(id.String(), worldInstanceIDPrefix) {
				t.Fatalf("invalid world instance accepted: %q", value)
			}
		}
		if id, err := NewRuntimeNodeID(value); err == nil {
			if !id.Valid() || !strings.HasPrefix(id.String(), runtimeNodeIDPrefix) {
				t.Fatalf("invalid runtime node accepted: %q", value)
			}
		}
	})
}

// FuzzAssignmentSnapshotHydration 验证任意 generation/token/phase/time 偏移不会绕过 snapshot 不变量。
func FuzzAssignmentSnapshotHydration(f *testing.F) {
	f.Add(uint64(1), uint64(1), uint8(PhaseStarting), int64(-1), int64(30))
	f.Add(uint64(1), uint64(1), uint8(PhaseStarting), int64(-40), int64(-9))
	f.Add(uint64(0), uint64(1), uint8(PhaseActive), int64(0), int64(30))
	f.Add(uint64(1), uint64(0), uint8(99), int64(1), int64(0))
	f.Fuzz(func(t *testing.T, generationValue uint64, tokenValue uint64, phaseValue uint8, createdOffset int64, expiryOffset int64) {
		generation, generationErr := NewAssignmentGeneration(generationValue)
		token, tokenErr := NewFencingToken(tokenValue)
		if generationErr != nil || tokenErr != nil {
			return
		}
		worldID, worldErr := personalworld.NewPersonalWorldID("pworld_fuzz")
		instanceID, instanceErr := NewWorldInstanceID("winst_fuzz")
		nodeID, nodeErr := NewRuntimeNodeID("rnode_fuzz")
		if worldErr != nil || instanceErr != nil || nodeErr != nil {
			t.Fatalf("constant fuzz identity is invalid: world=%v instance=%v node=%v", worldErr, instanceErr, nodeErr)
		}
		stamp, err := NewAssignmentStamp(worldID, instanceID, nodeID, generation, token)
		if err != nil {
			t.Fatalf("valid stamp rejected: %v", err)
		}
		now := time.Date(2026, 7, 13, 4, 0, 0, 0, time.UTC)
		createdAt := now.Add(time.Duration(createdOffset) * time.Second)
		expiresAt := now.Add(time.Duration(expiryOffset) * time.Second)
		snapshot, err := NewAssignmentSnapshot(stamp, Phase(phaseValue), createdAt, expiresAt, now)
		if err == nil {
			if !snapshot.Valid() || !snapshot.Phase().Valid() || snapshot.CreatedAt().After(now) || !snapshot.Lease().ExpiresAt().After(snapshot.CreatedAt()) {
				t.Fatalf("constructor returned structurally inconsistent snapshot: %#v", snapshot)
			}
			if got, want := snapshot.ValidAt(now), snapshot.Lease().ExpiresAt().After(now); got != want {
				t.Fatalf("ValidAt() = %v, want %v", got, want)
			}
		}
	})
}

// mustAssignmentStamp 构造测试使用的完整 stamp，失败表示 fixture 本身无效。
func mustAssignmentStamp(t *testing.T, generationValue uint64, tokenValue uint64, instanceValue string, nodeValue string) AssignmentStamp {
	t.Helper()
	worldID, err := personalworld.NewPersonalWorldID("pworld_fixture")
	if err != nil {
		t.Fatalf("NewPersonalWorldID: %v", err)
	}
	instanceID, err := NewWorldInstanceID(instanceValue)
	if err != nil {
		t.Fatalf("NewWorldInstanceID: %v", err)
	}
	nodeID, err := NewRuntimeNodeID(nodeValue)
	if err != nil {
		t.Fatalf("NewRuntimeNodeID: %v", err)
	}
	generation, err := NewAssignmentGeneration(generationValue)
	if err != nil {
		t.Fatalf("NewAssignmentGeneration: %v", err)
	}
	token := mustFencingToken(t, tokenValue)
	stamp, err := NewAssignmentStamp(worldID, instanceID, nodeID, generation, token)
	if err != nil {
		t.Fatalf("NewAssignmentStamp: %v", err)
	}
	return stamp
}

// mustFencingToken 构造测试 token，失败表示 fixture 本身无效。
func mustFencingToken(t *testing.T, value uint64) FencingToken {
	t.Helper()
	token, err := NewFencingToken(value)
	if err != nil {
		t.Fatalf("NewFencingToken: %v", err)
	}
	return token
}
