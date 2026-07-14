package redis

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// testRedisError 模拟 go-redis 明确 server rejection，不依赖 client 网络行为。
type testRedisError string

// Error 返回测试 reply 文本。
func (failure testRedisError) Error() string { return string(failure) }

// RedisError 标记该错误来自 Redis reply 而不是连接中断。
func (testRedisError) RedisError() {}

// testDefinition 返回字段完整且可由各边界定向修改的 registry fixture。
func testDefinition() Definition {
	return Definition{
		Name: "session_lease", Owner: "session", Kind: "lease", Purpose: "测试运行态租约",
		TTLPolicy: TTLRequired, SchemaVersion: 1, MaxEncodedBytes: 256,
		Recovery: "Redis flush 后重新认证", Cleanup: "TTL expiry", Failure: "拒绝未知或损坏值", MetricsName: "session_lease",
	}
}

// testKeyspace 构造只登记 session_lease 的不可变 namespace fixture。
func testKeyspace(t testing.TB) *Keyspace {
	t.Helper()
	registry, err := NewRegistry([]Definition{testDefinition()})
	if err != nil {
		t.Fatal(err)
	}
	keyspace, err := NewKeyspace("local", registry)
	if err != nil {
		t.Fatal(err)
	}
	return keyspace
}

// TestBuildKey 固定真实冒号 namespace，并验证默认格式不暴露 identity/hash tag。
func TestBuildKey(t *testing.T) {
	t.Parallel()

	key, err := testKeyspace(t).Build("session_lease", "account_01", "device_02")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := key.Value(), "ih:local:session:lease:account_01:device_02"; got != want {
		t.Fatalf("key = %q，want %q", got, want)
	}
	if strings.Contains(key.String(), "account_01") || strings.Contains(key.String(), "{") {
		t.Fatalf("key default format 泄露 identity 或引入 hash tag：%q", key)
	}
	for _, output := range []string{fmt.Sprint(key), fmt.Sprintf("%#v", key), key.LogValue().String()} {
		if strings.Contains(output, "account_01") || strings.Contains(output, "device_02") {
			t.Fatalf("key 格式化泄露 identity：%q", output)
		}
	}
	if _, err := testKeyspace(t).Build("unknown", "account_01"); err == nil {
		t.Fatal("未登记 definition 应被拒绝")
	}
}

// TestBuildKeyRejectsInjection 覆盖空值、分隔符、花括号、控制字符和超长 segment。
func TestBuildKeyRejectsInjection(t *testing.T) {
	t.Parallel()

	tests := []string{"", "a:b", "{account}", "line\nbreak", "tab\tvalue", strings.Repeat("x", maximumSegmentBytes+1)}
	for _, value := range tests {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			if _, err := testKeyspace(t).Build("session_lease", value); err == nil {
				t.Fatalf("identity %q 应被拒绝", value)
			}
		})
	}
}

// FuzzBuildKey 证明所有成功输出都保持 namespace 前缀、长度和注入不变量。
func FuzzBuildKey(f *testing.F) {
	registry, err := NewRegistry([]Definition{testDefinition()})
	if err != nil {
		f.Fatal(err)
	}
	keyspace, err := NewKeyspace("test", registry)
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range []string{"account_01", "a:b", "{tag}", "世界", "\x00", strings.Repeat("x", maximumSegmentBytes)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, identity string) {
		key, err := keyspace.Build("session_lease", identity)
		if err != nil {
			return
		}
		if strings.ContainsAny(identity, ":{}\r\n\t") {
			t.Fatalf("forbidden identity accepted: %q", identity)
		}
		if len(key.Value()) > maximumKeyBytes || !strings.HasPrefix(key.Value(), "ih:test:session:lease:") {
			t.Fatalf("invalid key output: %q", key.Value())
		}
	})
}

// TestRegistryValidationAndImmutability 防止 duplicate/malformed metadata 或调用方后改写 registry。
func TestRegistryValidationAndImmutability(t *testing.T) {
	t.Parallel()

	definition := testDefinition()
	registry, err := NewRegistry([]Definition{definition})
	if err != nil {
		t.Fatal(err)
	}
	definition.Owner = "changed"
	stored, exists := registry.Lookup("session_lease")
	if !exists || stored.Owner != "session" {
		t.Fatalf("registry 未保存不可变值副本：%+v", stored)
	}
	if _, err := NewRegistry([]Definition{testDefinition(), testDefinition()}); err == nil {
		t.Fatal("duplicate definition 应被拒绝")
	}
	malformed := testDefinition()
	malformed.Cleanup = ""
	if _, err := NewRegistry([]Definition{malformed}); err == nil {
		t.Fatal("缺少 cleanup metadata 应被拒绝")
	}
}

// TestTTLAndEncodedValueBoundaries 保护非正 expiry、unknown version 与 oversize 的 fail-closed 行为。
func TestTTLAndEncodedValueBoundaries(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0)
	if ttl, err := TTLUntil(now.Add(time.Second), now); err != nil || ttl != time.Second {
		t.Fatalf("TTLUntil() = %v, %v", ttl, err)
	}
	for _, expiry := range []time.Time{now, now.Add(-time.Nanosecond)} {
		if _, err := TTLUntil(expiry, now); err == nil {
			t.Fatal("非正 TTL 应被拒绝")
		}
	}
	definition := testDefinition()
	if err := ValidateEncodedValue(definition, 1, []byte("ok")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEncodedValue(definition, 2, []byte("ok")); err == nil {
		t.Fatal("unknown schema version 应被拒绝")
	}
	if err := ValidateEncodedValue(definition, 1, make([]byte, definition.MaxEncodedBytes+1)); err == nil {
		t.Fatal("oversize value 应被拒绝")
	}
	if err := ValidateEncodedSize(definition, 1, definition.MaxEncodedBytes); err != nil {
		t.Fatalf("最大 encoded size 被拒绝: %v", err)
	}
	if err := ValidateEncodedSize(definition, 1, 0); err == nil {
		t.Fatal("空 encoded size 应被拒绝")
	}
}

// TestDigestAndCommandClassification 验证敏感 identity 摘要与 command/script 的保守失败分类。
func TestDigestAndCommandClassification(t *testing.T) {
	t.Parallel()

	const sensitive = "raw-bearer-token"
	digest := DigestIdentity([]byte(sensitive))
	if strings.Contains(digest, sensitive) || len(digest) != 32 {
		t.Fatalf("DigestIdentity() = %q", digest)
	}
	if got := ClassifyCommandError(OperationReadOnly, errors.New("network")); got != CommandReadFailed {
		t.Fatalf("read outcome = %s", got)
	}
	if got := ClassifyCommandError(OperationAtomicCommand, testRedisError("ERR rejected")); got != CommandNotApplied {
		t.Fatalf("server rejection outcome = %s", got)
	}
	if got := ClassifyCommandError(OperationAtomicCommand, errors.New("connection reset")); got != CommandCommitUnknown {
		t.Fatalf("mutation network outcome = %s", got)
	}
	if got := ClassifyCommandError(OperationAtomicMutation, testRedisError("ERR script failed after write")); got != CommandCommitUnknown {
		t.Fatalf("script server error outcome = %s", got)
	}
}
