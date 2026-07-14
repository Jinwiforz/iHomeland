package visitsession

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"
	domain "github.com/jinwiforz/ihomeland/server/internal/visitsession"
	redisclient "github.com/redis/go-redis/v9"
)

// fixedClock 为TTL边界测试提供不滑动的受信绝对时间。
type fixedClock struct {
	// now是每次Now调用返回的UTC时间。
	now time.Time
}

// Now 返回测试固定绝对时间。
func (clock fixedClock) Now() time.Time { return clock.now }

// recordingObserver 收集固定低基数字段供脱敏断言。
type recordingObserver struct {
	// values按调用顺序保存adapter:operation:outcome。
	values []string
}

// RecordStorageOperation 只保存低基数字段，供测试确认没有identity。
func (observer *recordingObserver) RecordStorageOperation(adapter, operation, outcome string) {
	observer.values = append(observer.values, adapter+":"+operation+":"+outcome)
}

// TestNewStoreValidatesDependenciesAndRetention 固定构造阶段不获取资源且拒绝退化配置。
func TestNewStoreValidatesDependenciesAndRetention(t *testing.T) {
	client := redisclient.NewClient(&redisclient.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	defer func() { _ = client.Close() }()
	keyspace := testKeyspace(t)
	observer := &recordingObserver{}
	clock := fixedClock{now: time.Now().UTC()}
	for _, retention := range []time.Duration{time.Minute - time.Nanosecond, 24*time.Hour + time.Nanosecond} {
		if _, err := New(client, keyspace, clock, retention, observer); err == nil {
			t.Fatalf("retention %s accepted", retention)
		}
	}
	if _, err := New(client, keyspace, clock, time.Minute, observer); err != nil {
		t.Fatal(err)
	}
	if _, err := New(nil, keyspace, clock, time.Minute, observer); err == nil {
		t.Fatal("nil client accepted")
	}
}

// TestCancelledMutationIsProvenNotCommitted 验证发送前取消不会被误报commit-unknown。
func TestCancelledMutationIsProvenNotCommitted(t *testing.T) {
	client := redisclient.NewClient(&redisclient.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	defer func() { _ = client.Close() }()
	observer := &recordingObserver{}
	store, err := New(client, testKeyspace(t), fixedClock{now: time.Now().UTC()}, time.Minute, observer)
	if err != nil {
		t.Fatal(err)
	}
	fixture := newTestFixture(t, "cancel")
	record := testCreateRecord(t, fixture.snapshot, "cancel")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errors.New("cancel before redis send"))
	result, outcome, createErr := store.Create(ctx, record)
	if createErr == nil || outcome != domain.CreateOutcomeNotCommitted || result.Snapshot().Valid() {
		t.Fatalf("cancel outcome=%v result=%v err=%v", outcome, result, createErr)
	}
	if len(observer.values) == 0 || observer.values[len(observer.values)-1] != "visitsession:create:not_committed" {
		t.Fatalf("observations=%v", observer.values)
	}
}

// TestPhysicalExpiryAnchorsToSessionDeadline 验证retention不滑动领域expiry且毫秒向后取整。
func TestPhysicalExpiryAnchorsToSessionDeadline(t *testing.T) {
	now := time.UnixMicro(1_700_000_000_000_001).UTC()
	client := redisclient.NewClient(&redisclient.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	defer func() { _ = client.Close() }()
	store, err := New(client, testKeyspace(t), fixedClock{now: now}, time.Minute, &recordingObserver{})
	if err != nil {
		t.Fatal(err)
	}
	sessionExpiry := now.Add(time.Hour)
	expiresUS, physicalMS, err := store.physicalExpiry(sessionExpiry)
	if err != nil || expiresUS != "1700003600000001" || physicalMS != "1700003660001" {
		t.Fatalf("physical expiry = %s/%s, %v", expiresUS, physicalMS, err)
	}
}

// TestOwnerParserRejectsMalformedRepliesAndRedactsFailure 固定unknown code、非string与安全错误文本。
func TestOwnerParserRejectsMalformedRepliesAndRedactsFailure(t *testing.T) {
	if _, err := scriptItems([]any{"applied", int64(1)}); err == nil {
		t.Fatal("non-string script field accepted")
	}
	if _, err := mutationOutcome("unknown"); err == nil {
		t.Fatal("unknown mutation outcome accepted")
	}
	failure := (&adapterError{operation: "commit", outcome: "defect", cause: errors.New("secret-key-vses_hidden")}).Error()
	if strings.Contains(failure, "secret") || strings.Contains(failure, "vses_hidden") {
		t.Fatalf("failure leaked cause: %s", failure)
	}
}

// testKeyspace 通过production definitions构造隔离测试命名空间。
func testKeyspace(t testing.TB) *storageredis.Keyspace {
	t.Helper()
	registry, err := storageredis.NewRegistry(Definitions())
	if err != nil {
		t.Fatal(err)
	}
	keyspace, err := storageredis.NewKeyspace("test", registry)
	if err != nil {
		t.Fatal(err)
	}
	return keyspace
}
