package personalworld

import (
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
)

// TestPersonalWorldIDValidation 保护实体 namespace、长度和安全 ASCII 边界。
func TestPersonalWorldIDValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "valid", value: "pworld_0123456789abcdef", valid: true},
		{name: "empty", value: "", valid: false},
		{name: "prefix only", value: "pworld_", valid: false},
		{name: "player prefix", value: "ply_0123456789abcdef", valid: false},
		{name: "punctuation", value: "pworld_bad-value", valid: false},
		{name: "unicode", value: "pworld_世界", valid: false},
		{name: "too long", value: "pworld_" + strings.Repeat("a", maximumIdentifierBytes), valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			id, err := NewPersonalWorldID(test.value)
			if (err == nil) != test.valid || id.Valid() != test.valid {
				t.Fatalf("NewPersonalWorldID() valid=%v err=%v", id.Valid(), err)
			}
		})
	}
}

// TestRevisionAndLifecycleValidation 固定正 revision、溢出和封闭状态集合。
func TestRevisionAndLifecycleValidation(t *testing.T) {
	t.Parallel()
	if _, err := NewRevision(0); err == nil {
		t.Fatal("zero revision accepted")
	}
	revision, err := NewRevision(1)
	if err != nil || revision != InitialRevision || revision.Uint64() != 1 {
		t.Fatalf("revision=%v err=%v", revision, err)
	}
	if next, err := revision.next(); err != nil || next != 2 {
		t.Fatalf("next=%v err=%v", next, err)
	}
	maximum := Revision(^uint64(0))
	if _, err := maximum.next(); err == nil {
		t.Fatal("maximum revision advanced")
	}
	if LifecycleUnspecified.Valid() || !LifecycleActive.Valid() || !LifecycleArchived.Valid() || Lifecycle(99).Valid() {
		t.Fatal("lifecycle closed set is invalid")
	}
	if LifecycleUnspecified != 0 || LifecycleActive != 1 || LifecycleArchived != 2 {
		t.Fatal("lifecycle persisted values changed")
	}
	if LifecycleActive.String() != "active" || LifecycleArchived.String() != "archived" || Lifecycle(99).String() != "unspecified" {
		t.Fatal("lifecycle stable names changed")
	}
}

// TestIdempotencyKeyValidationAndRedaction 验证 command identity 有界且所有默认日志路径脱敏。
func TestIdempotencyKeyValidationAndRedaction(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value string
		valid bool
	}{
		{value: "archive:0001", valid: true},
		{value: "abc_DEF-123.456", valid: true},
		{value: "short", valid: false},
		{value: "archive key", valid: false},
		{value: "归档命令0001", valid: false},
		{value: strings.Repeat("a", maximumIdempotencyKeyBytes+1), valid: false},
	}
	for _, test := range tests {
		key, err := NewIdempotencyKey(test.value)
		if (err == nil) != test.valid || key.Valid() != test.valid {
			t.Fatalf("value=%q valid=%v err=%v", test.value, key.Valid(), err)
		}
		if !test.valid {
			continue
		}
		if key.Value() != test.value || key.String() != redactedIdempotencyKey || fmt.Sprintf("%v", key) != redactedIdempotencyKey || fmt.Sprintf("%#v", key) != redactedIdempotencyKey {
			t.Fatal("idempotency key formatting leaked or changed value")
		}
		logValue := key.LogValue()
		if logValue.Kind() != slog.KindString || logValue.String() != redactedIdempotencyKey {
			t.Fatal("structured logging did not redact idempotency key")
		}
	}
}

// TestSnapshotHydrationAndArchiveTransition 验证 immutable owner、UTC 时间和唯一 lifecycle transition。
func TestSnapshotHydrationAndArchiveTransition(t *testing.T) {
	t.Parallel()
	id := mustWorldID(t, "snapshot")
	owner := mustPlayerID(t, "snapshot")
	createdAt := time.Date(2026, 7, 13, 8, 0, 0, 123, time.FixedZone("test", 8*60*60))
	world, err := NewPersonalWorld(id, owner, createdAt)
	if err != nil {
		t.Fatalf("NewPersonalWorld() error = %v", err)
	}
	if !world.Valid() || world.ID() != id || world.OwnerID() != owner || world.Lifecycle() != LifecycleActive || world.Revision() != InitialRevision || world.CreatedAt().Location() != time.UTC || world.CreatedAt().Nanosecond()%int(time.Microsecond) != 0 {
		t.Fatalf("new world = %#v", world.Snapshot())
	}
	hydrated, err := HydratePersonalWorld(world.Snapshot())
	if err != nil || !hydrated.Snapshot().Equal(world.Snapshot()) {
		t.Fatalf("HydratePersonalWorld() world=%v err=%v", hydrated.Valid(), err)
	}
	archived, err := hydrated.archive()
	if err != nil {
		t.Fatalf("archive() error = %v", err)
	}
	if archived.OwnerID() != owner || archived.ID() != id || archived.Lifecycle() != LifecycleArchived || archived.Revision() != InitialRevision+1 || !archived.CreatedAt().Equal(world.CreatedAt()) {
		t.Fatalf("archived snapshot = %#v", archived.Snapshot())
	}
	if _, err := archived.archive(); err == nil {
		t.Fatal("archived world accepted another transition")
	}
}

// TestSnapshotRejectsIncompleteFacts 覆盖 adapter 不能绕过的 hydration 不变量。
func TestSnapshotRejectsIncompleteFacts(t *testing.T) {
	t.Parallel()
	id := mustWorldID(t, "invalid")
	owner := mustPlayerID(t, "invalid")
	now := time.Unix(1_750_000_000, 0)
	tests := []struct {
		name      string
		id        PersonalWorldID
		owner     account.PlayerID
		lifecycle Lifecycle
		revision  Revision
		createdAt time.Time
	}{
		{name: "zero id", owner: owner, lifecycle: LifecycleActive, revision: 1, createdAt: now},
		{name: "zero owner", id: id, lifecycle: LifecycleActive, revision: 1, createdAt: now},
		{name: "unknown lifecycle", id: id, owner: owner, lifecycle: Lifecycle(99), revision: 1, createdAt: now},
		{name: "zero revision", id: id, owner: owner, lifecycle: LifecycleActive, createdAt: now},
		{name: "zero time", id: id, owner: owner, lifecycle: LifecycleActive, revision: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if snapshot, err := NewSnapshot(test.id, test.owner, test.lifecycle, test.revision, test.createdAt); err == nil || snapshot.Valid() {
				t.Fatalf("malformed snapshot accepted: %#v", snapshot)
			}
		})
	}
	if _, err := HydratePersonalWorld(Snapshot{}); err == nil {
		t.Fatal("zero snapshot hydrated")
	}
}

// TestArchiveCommandValidationAndRedaction 保护可信输入边界与复合值的默认日志脱敏。
func TestArchiveCommandValidationAndRedaction(t *testing.T) {
	t.Parallel()
	owner := mustPlayerID(t, "command")
	worldID := mustWorldID(t, "command")
	key := mustIdempotencyKey(t, "archive:command:redaction")
	command, err := NewArchiveCommand(owner, worldID, InitialRevision, key)
	if err != nil || !command.Valid() {
		t.Fatalf("NewArchiveCommand() valid=%v err=%v", command.Valid(), err)
	}
	if _, err := NewArchiveCommand(account.PlayerID{}, worldID, InitialRevision, key); err == nil {
		t.Fatal("zero actor accepted")
	}
	formatted := fmt.Sprintf("%v|%+v|%#v", command, command, command)
	if strings.Contains(formatted, key.Value()) || formatted != strings.Join([]string{archiveMutationPlaceholder, archiveMutationPlaceholder, archiveMutationPlaceholder}, "|") {
		t.Fatalf("archive command formatting leaked fields: %s", formatted)
	}
	logValue := slog.AnyValue(command).Resolve()
	if logValue.Kind() != slog.KindString || logValue.String() != archiveMutationPlaceholder {
		t.Fatalf("archive command log value = %v", logValue)
	}
}

// TestArchiveRecordFingerprintBindsCommand 验证 fingerprint 稳定且会区分 revision、world 和 actor。
func TestArchiveRecordFingerprintBindsCommand(t *testing.T) {
	t.Parallel()
	world := mustWorld(t, "fingerprint", mustPlayerID(t, "fingerprint"))
	key := mustIdempotencyKey(t, "archive:fingerprint")
	command, err := NewArchiveCommand(world.OwnerID(), world.ID(), world.Revision(), key)
	if err != nil {
		t.Fatalf("NewArchiveCommand() error = %v", err)
	}
	record, err := NewArchiveRecord(command, world.Snapshot())
	if err != nil || !record.Valid() {
		t.Fatalf("NewArchiveRecord() valid=%v err=%v", record.Valid(), err)
	}
	const expectedFingerprint = "680888fd016325a800d14af55b0963e3691ed541ab7f2b0191501d06f60ab851"
	if actual := fmt.Sprintf("%x", record.Fingerprint().digest); actual != expectedFingerprint {
		t.Fatalf("archive fingerprint compatibility changed: %s", actual)
	}
	recordFormatted := fmt.Sprintf("%v|%+v|%#v", record, record, record)
	if strings.Contains(recordFormatted, key.Value()) || recordFormatted != strings.Join([]string{archiveMutationPlaceholder, archiveMutationPlaceholder, archiveMutationPlaceholder}, "|") {
		t.Fatalf("archive record formatting leaked fields: %s", recordFormatted)
	}
	recordLogValue := slog.AnyValue(record).Resolve()
	if recordLogValue.Kind() != slog.KindString || recordLogValue.String() != archiveMutationPlaceholder {
		t.Fatalf("archive record log value = %v", recordLogValue)
	}
	repeat, err := NewArchiveRecord(command, world.Snapshot())
	if err != nil || !record.Fingerprint().Equal(repeat.Fingerprint()) {
		t.Fatal("same command produced different fingerprint")
	}
	otherRevision := mustRevision(t, 2)
	otherCommand := mustArchiveCommand(t, world.OwnerID(), world.ID(), otherRevision, key)
	other, err := NewArchiveRecord(otherCommand, world.Snapshot())
	if err != nil || record.Fingerprint().Equal(other.Fingerprint()) {
		t.Fatal("different revision reused fingerprint")
	}
	variants := []CommandFingerprint{
		fingerprintArchiveCommand(mustWorldID(t, "otherworld"), world.OwnerID().String(), world.Revision(), LifecycleArchived),
		fingerprintArchiveCommand(world.ID(), mustPlayerID(t, "otheractor").String(), world.Revision(), LifecycleArchived),
		fingerprintArchiveCommand(world.ID(), world.OwnerID().String(), world.Revision(), LifecycleActive),
	}
	for _, variant := range variants {
		if record.Fingerprint().Equal(variant) {
			t.Fatal("different command semantics reused fingerprint")
		}
	}
	if strings.Contains(fmt.Sprintf("%v %#v", record.Fingerprint(), record.Fingerprint()), fmt.Sprintf("%x", record.Fingerprint().digest)) {
		t.Fatal("fingerprint formatting leaked digest")
	}
	fingerprintLogValue := slog.AnyValue(record.Fingerprint()).Resolve()
	if fingerprintLogValue.Kind() != slog.KindString || fingerprintLogValue.String() != "[REDACTED_COMMAND_FINGERPRINT]" {
		t.Fatalf("fingerprint log value = %v", fingerprintLogValue)
	}
}

// FuzzPersonalWorldIDValidation 确认任意 bytes 不会绕过 namespace 或触发 panic。
func FuzzPersonalWorldIDValidation(f *testing.F) {
	f.Add("pworld_0123456789abcdef")
	f.Add("ply_0123456789abcdef")
	f.Add(string([]byte{0xff, 0xfe}))
	f.Fuzz(func(t *testing.T, input string) {
		id, err := NewPersonalWorldID(input)
		if err == nil && (!id.Valid() || id.String() != input || !strings.HasPrefix(input, personalWorldIDPrefix)) {
			t.Fatalf("accepted inconsistent ID: %q", input)
		}
	})
}

// FuzzIdempotencyKeyValidation 确认任意输入只能得到安全 key 或稳定错误。
func FuzzIdempotencyKeyValidation(f *testing.F) {
	f.Add("archive:0001")
	f.Add("short")
	f.Add("archive key")
	f.Fuzz(func(t *testing.T, input string) {
		key, err := NewIdempotencyKey(input)
		if err == nil {
			if !key.Valid() || key.Value() != input || len(input) < minimumIdempotencyKeyBytes || len(input) > maximumIdempotencyKeyBytes {
				t.Fatalf("accepted inconsistent key: %q", input)
			}
			if strings.Contains(fmt.Sprintf("%v %#v", key, key), input) {
				t.Fatal("formatted key leaked input")
			}
		}
	})
}

// FuzzSnapshotHydration 确认随机持久字段不能构造部分有效 aggregate。
func FuzzSnapshotHydration(f *testing.F) {
	f.Add("pworld_0123456789abcdef", "ply_0123456789abcdef", uint8(LifecycleActive), uint64(1), int64(1_750_000_000))
	f.Add("", "", uint8(99), uint64(0), int64(0))
	f.Fuzz(func(t *testing.T, worldValue string, ownerValue string, lifecycleValue uint8, revisionValue uint64, unixSeconds int64) {
		id, idErr := NewPersonalWorldID(worldValue)
		owner, ownerErr := account.NewPlayerID(ownerValue)
		revision, revisionErr := NewRevision(revisionValue)
		createdAt := time.Unix(unixSeconds, 0)
		snapshot, snapshotErr := NewSnapshot(id, owner, Lifecycle(lifecycleValue), revision, createdAt)
		if snapshotErr == nil {
			if idErr != nil || ownerErr != nil || revisionErr != nil || !snapshot.Valid() {
				t.Fatal("snapshot accepted invalid source values")
			}
			world, err := HydratePersonalWorld(snapshot)
			if err != nil || !world.Valid() || !world.Snapshot().Equal(snapshot) {
				t.Fatalf("valid snapshot did not round-trip: %v", err)
			}
		}
	})
}

// FuzzArchiveCommandFingerprint 确认任意字段只能生成确定摘要，且 owner/revision 变化不能复用结果。
func FuzzArchiveCommandFingerprint(f *testing.F) {
	f.Add("pworld_0123456789abcdef", "ply_0123456789abcdef", uint64(1))
	f.Add("", "", uint64(0))
	f.Fuzz(func(t *testing.T, worldValue string, ownerValue string, revisionValue uint64) {
		worldID, worldErr := NewPersonalWorldID(worldValue)
		ownerID, ownerErr := account.NewPlayerID(ownerValue)
		revision, revisionErr := NewRevision(revisionValue)
		if worldErr != nil || ownerErr != nil || revisionErr != nil {
			return
		}
		first := fingerprintArchiveCommand(worldID, ownerID.String(), revision, LifecycleArchived)
		second := fingerprintArchiveCommand(worldID, ownerID.String(), revision, LifecycleArchived)
		if !first.Valid() || !first.Equal(second) {
			t.Fatal("valid command did not produce a deterministic fingerprint")
		}
		if revisionValue < ^uint64(0) {
			next := mustRevision(t, revisionValue+1)
			if first.Equal(fingerprintArchiveCommand(worldID, ownerID.String(), next, LifecycleArchived)) {
				t.Fatal("different revision reused fingerprint")
			}
		}
	})
}

// mustWorldID 构造测试专用且 namespace 正确的 PersonalWorldID。
func mustWorldID(t testing.TB, suffix string) PersonalWorldID {
	t.Helper()
	id, err := NewPersonalWorldID("pworld_" + suffix + "0123456789")
	if err != nil {
		t.Fatalf("NewPersonalWorldID() error = %v", err)
	}
	return id
}

// mustPlayerID 构造 account owner 提供的有效 PlayerID。
func mustPlayerID(t testing.TB, suffix string) account.PlayerID {
	t.Helper()
	material := sha256.Sum256([]byte(suffix))
	id, err := account.NewPlayerID(fmt.Sprintf("ply_%x", material[:8]))
	if err != nil {
		t.Fatalf("account.NewPlayerID() error = %v", err)
	}
	return id
}

// mustIdempotencyKey 构造测试专用有效 command identity。
func mustIdempotencyKey(t testing.TB, value string) IdempotencyKey {
	t.Helper()
	key, err := NewIdempotencyKey(value)
	if err != nil {
		t.Fatalf("NewIdempotencyKey() error = %v", err)
	}
	return key
}

// mustRevision 构造测试预期有效的正 revision，并在约束变化时立即终止用例。
func mustRevision(t testing.TB, value uint64) Revision {
	t.Helper()
	revision, err := NewRevision(value)
	if err != nil {
		t.Fatalf("NewRevision() error = %v", err)
	}
	return revision
}

// mustArchiveCommand 构造完整测试 command，不允许测试静默忽略 constructor failure。
func mustArchiveCommand(t testing.TB, actorID account.PlayerID, worldID PersonalWorldID, expectedRevision Revision, key IdempotencyKey) ArchiveCommand {
	t.Helper()
	command, err := NewArchiveCommand(actorID, worldID, expectedRevision, key)
	if err != nil {
		t.Fatalf("NewArchiveCommand() error = %v", err)
	}
	return command
}

// mustSnapshot 构造测试 repository 返回的完整持久值。
func mustSnapshot(t testing.TB, id PersonalWorldID, ownerID account.PlayerID, lifecycle Lifecycle, revision Revision, createdAt time.Time) Snapshot {
	t.Helper()
	snapshot, err := NewSnapshot(id, ownerID, lifecycle, revision, createdAt)
	if err != nil {
		t.Fatalf("NewSnapshot() error = %v", err)
	}
	return snapshot
}

// mustMutationResult 构造测试 repository 的成功 mutation result。
func mustMutationResult(t testing.TB, snapshot Snapshot, fingerprint CommandFingerprint) MutationResult {
	t.Helper()
	result, err := NewMutationResult(snapshot, fingerprint)
	if err != nil {
		t.Fatalf("NewMutationResult() error = %v", err)
	}
	return result
}

// mustWorld 构造 active revision 1 的确定性 PersonalWorld。
func mustWorld(t testing.TB, suffix string, owner account.PlayerID) PersonalWorld {
	t.Helper()
	world, err := NewPersonalWorld(mustWorldID(t, suffix), owner, time.Unix(1_750_000_000, 0))
	if err != nil {
		t.Fatalf("NewPersonalWorld() error = %v", err)
	}
	return world
}
