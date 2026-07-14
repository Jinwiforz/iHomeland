package session

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	domain "github.com/jinwiforz/ihomeland/server/internal/session"
	storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"
	redisclient "github.com/redis/go-redis/v9"
)

// fixedGenerator 为测试生成稳定且非零的凭据材料。
type fixedGenerator byte

// Fill 使用固定字节覆盖完整目标，满足 SecretGenerator 契约。
func (generator fixedGenerator) Fill(target []byte) error {
	copy(target, bytes.Repeat([]byte{byte(generator)}, len(target)))
	return nil
}

// discardObserver 丢弃固定低基数存储观测。
type discardObserver struct{}

// RecordStorageOperation 满足 Store observer 契约。
func (discardObserver) RecordStorageOperation(string, string, string) {}

// TestDefinitions 验证Session definitions可以进入共享registry且治理字段完整。
func TestDefinitions(t *testing.T) {
	definitions := Definitions()
	if len(definitions) != 5 {
		t.Fatalf("definition count = %d, want 5", len(definitions))
	}
	registry, err := storageredis.NewRegistry(definitions)
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		if definition.Owner != "session" || definition.TTLPolicy != storageredis.TTLRequired || definition.SchemaVersion != redisSchemaVersion {
			t.Fatalf("definition %q has invalid governance metadata", definition.Name)
		}
		if _, exists := registry.Lookup(definition.Name); !exists {
			t.Fatalf("definition %q was not registered", definition.Name)
		}
	}
}

// TestCodecBoundaries 验证微秒expiry、enum和Lua reply的canonical边界。
func TestCodecBoundaries(t *testing.T) {
	timestamp := time.UnixMicro(1_750_000_000_000_001).UTC()
	microseconds, err := canonicalMicroTime(timestamp)
	if err != nil || microseconds != "1750000000000001" {
		t.Fatalf("canonical microseconds = %q, %v", microseconds, err)
	}
	milliseconds, err := expiryMilliseconds(timestamp)
	if err != nil || milliseconds != "1750000000001" {
		t.Fatalf("rounded milliseconds = %q, %v", milliseconds, err)
	}
	if _, err := canonicalMicroTime(time.Time{}); err == nil {
		t.Fatal("zero timestamp was accepted")
	}
	if _, err := parseMicroTime("01"); err == nil {
		t.Fatal("non-canonical timestamp was accepted")
	}
	control, _ := domain.NewScopeSet(domain.ScopeControl)
	encoded, err := encodeScopes(control)
	if err != nil || encoded != "control" {
		t.Fatalf("encoded scopes = %q, %v", encoded, err)
	}
	decoded, err := decodeScopes(encoded)
	if err != nil || !decoded.Has(domain.ScopeControl) || decoded.Len() != 1 {
		t.Fatalf("decoded scopes are invalid: %v", err)
	}
	for _, corrupt := range []string{"", "control,control", "gameplay,control", "admin"} {
		if _, err := decodeScopes(corrupt); err == nil {
			t.Fatalf("corrupt scopes %q were accepted", corrupt)
		}
	}
	if _, err := scriptItems([]any{"applied", int64(1)}); err == nil {
		t.Fatal("non-string script field was accepted")
	}
}

// TestStrictKeyBuilders 验证真实key只通过显式Value暴露且默认格式化隐藏identity。
func TestStrictKeyBuilders(t *testing.T) {
	registry, err := storageredis.NewRegistry(Definitions())
	if err != nil {
		t.Fatal(err)
	}
	keyspace, err := storageredis.NewKeyspace("test", registry)
	if err != nil {
		t.Fatal(err)
	}
	client := redisclient.NewClient(&redisclient.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = client.Close() })
	store, err := New(client, keyspace, discardObserver{})
	if err != nil {
		t.Fatal(err)
	}
	secret, err := domain.NewSecret(domain.SecretKindAccess, fixedGenerator(0x2a))
	if err != nil {
		t.Fatal(err)
	}
	key, digest, err := store.digestKey(sessionAccessDefinitionName, secret.Digest())
	if err != nil {
		t.Fatal(err)
	}
	if len(digest) != 64 || digest != strings.ToLower(digest) {
		t.Fatalf("digest encoding is not lowercase SHA-256 hex: %q", digest)
	}
	if strings.Contains(key.String(), digest) || !strings.Contains(key.Value(), digest) {
		t.Fatal("redis key formatting leaked or omitted digest identity")
	}
	principal, _ := domain.NewPrincipal("acc_codec1", "ply_codec1")
	principalKey, err := store.principalKey(principal)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(principalKey.Value(), principal.AccountID()) || strings.Contains(principalKey.Value(), principal.PlayerID()) {
		t.Fatal("principal key contains raw identity")
	}
}

// TestAdapterErrorAndObservationRedaction 验证默认错误与观测不携带key、digest、identity或driver文本。
func TestAdapterErrorAndObservationRedaction(t *testing.T) {
	cause := errors.New("redis key ih:test:session:access:full-digest account=acc_sensitive token=ih_at_sensitive")
	failure := (&adapterError{operation: "resolve_access", outcome: "read_failed", cause: cause}).Error()
	for _, forbidden := range []string{"ih:test", "full-digest", "acc_sensitive", "ih_at_sensitive", "redis key"} {
		if strings.Contains(fmt.Sprintf("%+v", failure), forbidden) {
			t.Fatalf("adapter error leaked %q", forbidden)
		}
	}
}

// TestValidateBundleRejectsCrossRecordMismatch 验证Create在调用Lua前拒绝交叉绑定错误。
func TestValidateBundleRejectsCrossRecordMismatch(t *testing.T) {
	bundle := testBundle(t, "codec", time.Now().UTC())
	otherID, _ := domain.NewSessionID("ses_other")
	bundle.Refresh.SessionID = otherID
	if err := validateBundle(bundle); err == nil {
		t.Fatal("cross-record session mismatch was accepted")
	}
	invalidID, _ := domain.NewSessionID("generic_without_owner")
	bundle = testBundle(t, "namespace", time.Now().UTC())
	bundle.Session.ID = invalidID
	bundle.Access.SessionID = invalidID
	bundle.Refresh.SessionID = invalidID
	if err := validateBundle(bundle); err == nil {
		t.Fatal("ownerless session identity was accepted")
	}
}

// FuzzSessionCodecInputs 验证任意文本不会绕过canonical数字与scope解析。
func FuzzSessionCodecInputs(f *testing.F) {
	for _, seed := range []string{"1", "01", "1750000000000000", "control", "gameplay", "control,control", "-1", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if parsed, err := parseCanonicalUint(value); err == nil && value != "0" && (value[0] == '0' || value != strings.TrimSpace(value)) {
			t.Fatalf("non-canonical uint accepted: %q -> %d", value, parsed)
		}
		if scopes, err := decodeScopes(value); err == nil {
			encoded, encodeErr := encodeScopes(scopes)
			if encodeErr != nil || encoded != value {
				t.Fatalf("scope codec is not canonical: %q -> %q", value, encoded)
			}
		}
	})
}

// testBundle 构造符合SessionStore Create契约的稳定测试输入。
func testBundle(t *testing.T, suffix string, now time.Time) domain.SessionBundle {
	t.Helper()
	id, _ := domain.NewSessionID("ses_" + suffix)
	principal, _ := domain.NewPrincipal("acc_"+suffix, "ply_"+suffix)
	access, err := domain.NewSecret(domain.SecretKindAccess, fixedGenerator(0x11))
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := domain.NewSecret(domain.SecretKindRefresh, fixedGenerator(0x22))
	if err != nil {
		t.Fatal(err)
	}
	sessionExpiry := now.Add(time.Hour).UTC()
	return domain.SessionBundle{
		Session: domain.SessionRecord{ID: id, Principal: principal, Epoch: domain.InitialEpoch, Status: domain.StatusActive, ExpiresAt: sessionExpiry},
		Access:  domain.TokenRecord{Digest: access.Digest(), SessionID: id, Epoch: domain.InitialEpoch, ExpiresAt: now.Add(10 * time.Minute).UTC()},
		Refresh: domain.TokenRecord{Digest: refresh.Digest(), SessionID: id, Epoch: domain.InitialEpoch, ExpiresAt: now.Add(30 * time.Minute).UTC()},
	}
}
