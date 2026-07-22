//go:build storage_integration

package visitsession

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	domain "github.com/jinwiforz/ihomeland/server/internal/visitsession"
	redisclient "github.com/redis/go-redis/v9"
)

// integrationObserver 满足production观测契约且不在集成测试输出identity。
type integrationObserver struct{}

// RecordStorageOperation 丢弃不包含identity的固定结果。
func (integrationObserver) RecordStorageOperation(string, string, string) {}

// TestStoreIntegrationLifecycleReplayAndTerminalRetention 覆盖真实Redis主路径与完整terminal replay。
func TestStoreIntegrationLifecycleReplayAndTerminalRetention(t *testing.T) {
	store, client := openIntegrationStore(t)
	fixture := newTestFixture(t, "lifecycle")
	record := testCreateRecord(t, fixture.snapshot, "lifecycle")
	result, outcome, err := store.Create(context.Background(), record)
	if err != nil || outcome != domain.CreateOutcomeCreated || !result.Snapshot().Equal(fixture.snapshot) {
		t.Fatalf("create outcome=%v err=%v", outcome, err)
	}
	if replay, replayOutcome, replayErr := store.Create(context.Background(), record); replayErr != nil || replayOutcome != domain.CreateOutcomeReplay || !replay.Snapshot().Equal(fixture.snapshot) {
		t.Fatalf("create replay outcome=%v err=%v", replayOutcome, replayErr)
	}
	conflict, _ := domain.NewCreateRecord(record.CommandID(), testFingerprint(t, "different"), fixture.snapshot)
	if value, conflictOutcome, conflictErr := store.Create(context.Background(), conflict); conflictErr != nil || conflictOutcome != domain.CreateOutcomeIdempotencyConflict || value.Snapshot().Valid() {
		t.Fatalf("create conflict outcome=%v err=%v", conflictOutcome, conflictErr)
	}
	if active, resolveOutcome, resolveErr := store.ResolveActive(context.Background(), fixture.snapshot.WorldID()); resolveErr != nil || resolveOutcome != domain.ResolveOutcomeFound || !active.Equal(fixture.snapshot) {
		t.Fatalf("resolve outcome=%v err=%v", resolveOutcome, resolveErr)
	}
	if found, findOutcome, findErr := store.FindByID(context.Background(), fixture.snapshot.ID()); findErr != nil || findOutcome != domain.ResolveOutcomeFound || !found.Equal(fixture.snapshot) {
		t.Fatalf("find outcome=%v err=%v", findOutcome, findErr)
	}
	changedCapacity, _ := domain.NewCapacity(3)
	changedRevision, _ := domain.NewRevision(fixture.snapshot.Revision().Uint64() + 1)
	changedTarget, err := domain.NewSnapshot(fixture.snapshot.ID(), fixture.snapshot.OwnerID(), fixture.snapshot.WorldID(), fixture.snapshot.Assignment(), domain.LifecycleClosed, changedRevision, changedCapacity, fixture.snapshot.CreatedAt(), fixture.snapshot.ExpiresAt(), fixture.snapshot.OwnerBinding(), 0, time.Time{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	changedCommand, _ := domain.NewCommandID("vcmd_changeImmutable")
	changedFingerprint := testFingerprint(t, "change-immutable-capacity")
	changedResult, err := domain.NewMutationResult(domain.OperationClose, changedTarget, changedCommand, changedFingerprint, domain.InviteSnapshot{}, domain.AdmissionIntent{}, domain.MembershipSnapshot{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	changedRecord, err := domain.NewTransitionRecord(domain.OperationClose, fixture.snapshot.ID(), fixture.snapshot.Revision(), changedCommand, changedFingerprint, changedResult)
	if err != nil {
		t.Fatal(err)
	}
	if result, immutableOutcome, immutableErr := store.Commit(context.Background(), changedRecord); immutableErr == nil || immutableOutcome != domain.MutationOutcomeNotCommitted || result.Snapshot().Valid() {
		t.Fatalf("immutable capacity change accepted outcome=%v err=%v", immutableOutcome, immutableErr)
	}
	if unchanged, findOutcome, findErr := store.FindByID(context.Background(), fixture.snapshot.ID()); findErr != nil || findOutcome != domain.ResolveOutcomeFound || !unchanged.Equal(fixture.snapshot) {
		t.Fatalf("immutable rejection changed snapshot outcome=%v err=%v", findOutcome, findErr)
	}

	closeRecord := testCloseRecord(t, fixture.snapshot, "lifecycle")
	closed, closeOutcome, closeErr := store.Commit(context.Background(), closeRecord)
	if closeErr != nil || closeOutcome != domain.MutationOutcomeApplied || closed.Snapshot().Lifecycle() != domain.LifecycleClosed {
		t.Fatalf("close outcome=%v err=%v", closeOutcome, closeErr)
	}
	if replay, replayOutcome, replayErr := store.Commit(context.Background(), closeRecord); replayErr != nil || replayOutcome != domain.MutationOutcomeReplay || !replay.Snapshot().Equal(closed.Snapshot()) {
		t.Fatalf("close replay outcome=%v err=%v", replayOutcome, replayErr)
	}
	if active, resolveOutcome, resolveErr := store.ResolveActive(context.Background(), fixture.snapshot.WorldID()); resolveErr != nil || resolveOutcome != domain.ResolveOutcomeNotFound || active.Valid() {
		t.Fatalf("terminal active outcome=%v err=%v", resolveOutcome, resolveErr)
	}
	if terminal, findOutcome, findErr := store.FindByID(context.Background(), fixture.snapshot.ID()); findErr != nil || findOutcome != domain.ResolveOutcomeFound || !terminal.Equal(closed.Snapshot()) {
		t.Fatalf("terminal find outcome=%v err=%v", findOutcome, findErr)
	}
	key, _ := store.sessionKey(fixture.snapshot.ID())
	if ttl := client.PTTL(context.Background(), key.Value()).Val(); ttl <= 0 || ttl < 59*time.Minute {
		t.Fatalf("terminal physical ttl=%s", ttl)
	}
}

// TestStoreIntegrationProbeAndDirectiveReplay 覆盖无target决议与多条安全返回首次结果。
func TestStoreIntegrationProbeAndDirectiveReplay(t *testing.T) {
	store, _ := openIntegrationStore(t)
	fixture := newTestFixture(t, "directive")
	record := testCreateRecord(t, fixture.snapshot, "directive")
	if _, outcome, err := store.Create(context.Background(), record); err != nil || outcome != domain.CreateOutcomeCreated {
		t.Fatalf("create outcome=%v err=%v", outcome, err)
	}

	missing := newTestFixture(t, "missingprobe")
	missingCommand, _ := domain.NewCommandID("vcmd_missingProbe")
	missingProbe, err := domain.NewConflictProbe(domain.OperationLeave, missing.snapshot.ID(), domain.InitialRevision, missingCommand, testFingerprint(t, "missing-probe"))
	if err != nil {
		t.Fatal(err)
	}
	if result, outcome, commitErr := store.Commit(context.Background(), missingProbe); commitErr != nil || outcome != domain.MutationOutcomeNotFound || result.Snapshot().Valid() {
		t.Fatalf("missing probe outcome=%v err=%v", outcome, commitErr)
	}

	invalidCommand, _ := domain.NewCommandID("vcmd_invalidProbe")
	invalidProbe, err := domain.NewConflictProbe(domain.OperationLeave, fixture.snapshot.ID(), fixture.snapshot.Revision(), invalidCommand, testFingerprint(t, "invalid-probe"))
	if err != nil {
		t.Fatal(err)
	}
	if result, outcome, commitErr := store.Commit(context.Background(), invalidProbe); commitErr != nil || outcome != domain.MutationOutcomeInvalidState || result.Snapshot().Valid() {
		t.Fatalf("invalid probe outcome=%v err=%v", outcome, commitErr)
	}

	targetRevision, _ := domain.NewRevision(fixture.snapshot.Revision().Uint64() + 1)
	target, err := domain.NewSnapshot(fixture.snapshot.ID(), fixture.snapshot.OwnerID(), fixture.snapshot.WorldID(), fixture.snapshot.Assignment(), domain.LifecycleClosed, targetRevision, fixture.snapshot.Capacity(), fixture.snapshot.CreatedAt(), fixture.snapshot.ExpiresAt(), fixture.snapshot.OwnerBinding(), 0, time.Time{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	visitorA, _ := account.NewPlayerID("ply_directivea")
	visitorB, _ := account.NewPlayerID("ply_directiveb")
	directiveA, _ := domain.NewSafeReturnDirective(fixture.snapshot.ID(), visitorA, domain.SafeReturnReasonOwnerClosed, target.Revision())
	directiveB, _ := domain.NewSafeReturnDirective(fixture.snapshot.ID(), visitorB, domain.SafeReturnReasonOwnerClosed, target.Revision())
	closeCommand, _ := domain.NewCommandID("vcmd_directiveClose")
	closeFingerprint := testFingerprint(t, "directive-close")
	closeResult, err := domain.NewMutationResult(domain.OperationClose, target, closeCommand, closeFingerprint, domain.InviteSnapshot{}, domain.AdmissionIntent{}, domain.MembershipSnapshot{}, []domain.SafeReturnDirective{directiveA, directiveB})
	if err != nil {
		t.Fatal(err)
	}
	closeRecord, err := domain.NewTransitionRecord(domain.OperationClose, fixture.snapshot.ID(), fixture.snapshot.Revision(), closeCommand, closeFingerprint, closeResult)
	if err != nil {
		t.Fatal(err)
	}
	first, outcome, err := store.Commit(context.Background(), closeRecord)
	if err != nil || outcome != domain.MutationOutcomeApplied || len(first.Directives()) != 2 {
		t.Fatalf("directive close outcome=%v err=%v", outcome, err)
	}
	replay, outcome, err := store.Commit(context.Background(), closeRecord)
	if err != nil || outcome != domain.MutationOutcomeReplay || len(replay.Directives()) != 2 || replay.Directives()[0].VisitorID() != visitorA || replay.Directives()[1].VisitorID() != visitorB {
		t.Fatalf("directive replay outcome=%v err=%v", outcome, err)
	}
}

// TestStoreIntegrationConcurrentCreateAndCommit 证明多adapter共享唯一线性化点。
func TestStoreIntegrationConcurrentCreateAndCommit(t *testing.T) {
	store, client := openIntegrationStore(t)
	second, err := New(client, testKeyspace(t), fixedClock{now: time.Now().UTC()}, time.Hour, integrationObserver{})
	if err != nil {
		t.Fatal(err)
	}
	fixtureA := newTestFixture(t, "racea")
	fixtureB := newTestFixture(t, "raceb")
	// 两个candidate必须竞争同一world，但保持不同VisitSessionID。
	fixtureB.snapshot, err = domain.NewSnapshot(fixtureB.snapshot.ID(), fixtureA.snapshot.OwnerID(), fixtureA.snapshot.WorldID(), fixtureA.snapshot.Assignment(), domain.LifecycleOpen, domain.InitialRevision, fixtureA.snapshot.Capacity(), fixtureA.snapshot.CreatedAt(), fixtureA.snapshot.ExpiresAt(), fixtureA.snapshot.OwnerBinding(), 0, time.Time{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	records := []domain.CreateRecord{testCreateRecord(t, fixtureA.snapshot, "racea"), testCreateRecord(t, fixtureB.snapshot, "raceb")}
	stores := []*Store{store, second}
	type createAnswer struct {
		result  domain.CreateResult
		outcome domain.CreateOutcome
		err     error
	}
	answers := make(chan createAnswer, 2)
	var wait sync.WaitGroup
	for index := range records {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			result, outcome, createErr := stores[index].Create(context.Background(), records[index])
			answers <- createAnswer{result: result, outcome: outcome, err: createErr}
		}(index)
	}
	wait.Wait()
	close(answers)
	created, existing := 0, 0
	var active domain.Snapshot
	for answer := range answers {
		if answer.err != nil {
			t.Fatal(answer.err)
		}
		switch answer.outcome {
		case domain.CreateOutcomeCreated:
			created++
			active = answer.result.Snapshot()
		case domain.CreateOutcomeExisting:
			existing++
		default:
			t.Fatalf("unexpected create outcome=%v", answer.outcome)
		}
	}
	if created != 1 || existing != 1 {
		t.Fatalf("created=%d existing=%d", created, existing)
	}
	closeA := testCloseRecord(t, active, "raceclosea")
	closeB := testCloseRecord(t, active, "racecloseb")
	type commitAnswer struct {
		outcome domain.MutationOutcome
		err     error
	}
	commits := make(chan commitAnswer, 2)
	for index, record := range []domain.TransitionRecord{closeA, closeB} {
		wait.Add(1)
		go func(index int, record domain.TransitionRecord) {
			defer wait.Done()
			_, outcome, commitErr := stores[index].Commit(context.Background(), record)
			commits <- commitAnswer{outcome: outcome, err: commitErr}
		}(index, record)
	}
	wait.Wait()
	close(commits)
	applied, conflicts := 0, 0
	for answer := range commits {
		if answer.err != nil {
			t.Fatal(answer.err)
		}
		if answer.outcome == domain.MutationOutcomeApplied {
			applied++
		}
		if answer.outcome == domain.MutationOutcomeRevisionConflict {
			conflicts++
		}
	}
	if applied != 1 || conflicts != 1 {
		t.Fatalf("applied=%d revision_conflicts=%d", applied, conflicts)
	}

	capacityFixture := newTestFixture(t, "capacity")
	one, _ := domain.NewCapacity(1)
	capacityFixture.snapshot, err = domain.NewSnapshot(capacityFixture.snapshot.ID(), capacityFixture.snapshot.OwnerID(), capacityFixture.snapshot.WorldID(), capacityFixture.snapshot.Assignment(), domain.LifecycleOpen, domain.InitialRevision, one, capacityFixture.snapshot.CreatedAt(), capacityFixture.snapshot.ExpiresAt(), capacityFixture.snapshot.OwnerBinding(), 0, time.Time{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, outcome, createErr := store.Create(context.Background(), testCreateRecord(t, capacityFixture.snapshot, "capacity")); createErr != nil || outcome != domain.CreateOutcomeCreated {
		t.Fatalf("capacity create outcome=%v err=%v", outcome, createErr)
	}
	capacityCommits := make(chan commitAnswer, 2)
	for index, record := range []domain.TransitionRecord{testAcceptRecord(t, capacityFixture.snapshot, "slota"), testAcceptRecord(t, capacityFixture.snapshot, "slotb")} {
		wait.Add(1)
		go func(index int, record domain.TransitionRecord) {
			defer wait.Done()
			_, outcome, commitErr := stores[index].Commit(context.Background(), record)
			capacityCommits <- commitAnswer{outcome: outcome, err: commitErr}
		}(index, record)
	}
	wait.Wait()
	close(capacityCommits)
	applied, conflicts = 0, 0
	for answer := range capacityCommits {
		if answer.err != nil {
			t.Fatal(answer.err)
		}
		if answer.outcome == domain.MutationOutcomeApplied {
			applied++
		}
		if answer.outcome == domain.MutationOutcomeRevisionConflict {
			conflicts++
		}
	}
	if applied != 1 || conflicts != 1 {
		t.Fatalf("capacity applied=%d conflicts=%d", applied, conflicts)
	}
	stored, outcome, findErr := store.FindByID(context.Background(), capacityFixture.snapshot.ID())
	if findErr != nil || outcome != domain.ResolveOutcomeFound || stored.ActiveMemberCount() != 1 {
		t.Fatalf("capacity snapshot members=%d outcome=%v err=%v", stored.ActiveMemberCount(), outcome, findErr)
	}
}

// TestStoreIntegrationFlushRestartAndCorruption 固定可失效恢复与malformed状态拒绝。
func TestStoreIntegrationFlushRestartAndCorruption(t *testing.T) {
	store, client := openIntegrationStore(t)
	fixture := newTestFixture(t, "recovery")
	record := testCreateRecord(t, fixture.snapshot, "recovery")
	if _, outcome, err := store.Create(context.Background(), record); err != nil || outcome != domain.CreateOutcomeCreated {
		t.Fatalf("create outcome=%v err=%v", outcome, err)
	}
	restartIntegrationRedis(t, client)
	rebuilt, err := New(client, testKeyspace(t), fixedClock{now: time.Now().UTC()}, time.Hour, integrationObserver{})
	if err != nil {
		t.Fatal(err)
	}
	store = rebuilt
	if found, outcome, err := store.FindByID(context.Background(), fixture.snapshot.ID()); err != nil || outcome != domain.ResolveOutcomeFound || !found.Equal(fixture.snapshot) {
		t.Fatalf("restart find outcome=%v err=%v", outcome, err)
	}
	key, _ := store.sessionKey(fixture.snapshot.ID())
	if err := client.Persist(context.Background(), key.Value()).Err(); err != nil {
		t.Fatal(err)
	}
	if found, outcome, err := store.FindByID(context.Background(), fixture.snapshot.ID()); err == nil || outcome != domain.ResolveOutcomeUnspecified || found.Valid() {
		t.Fatalf("missing ttl accepted outcome=%v err=%v", outcome, err)
	}
	if err := client.FlushDB(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	if found, outcome, err := store.FindByID(context.Background(), fixture.snapshot.ID()); err != nil || outcome != domain.ResolveOutcomeNotFound || found.Valid() {
		t.Fatalf("flush find outcome=%v err=%v", outcome, err)
	}
	if replay, outcome, err := store.Create(context.Background(), record); err != nil || outcome != domain.CreateOutcomeCreated || !replay.Snapshot().Valid() {
		t.Fatalf("fresh create after flush outcome=%v err=%v", outcome, err)
	}
	commandKey, _ := store.commandKey(record.CommandID())
	if err := client.HSet(context.Background(), commandKey.Value(), "world", "pworld_corrupt").Err(); err != nil {
		t.Fatal(err)
	}
	if replay, outcome, err := store.Create(context.Background(), record); err == nil || outcome != domain.CreateOutcomeCommitUnknown || replay.Snapshot().Valid() {
		t.Fatalf("command metadata corruption accepted outcome=%v err=%v", outcome, err)
	}
	if err := client.HSet(context.Background(), commandKey.Value(), "world", fixture.snapshot.WorldID().String()).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(context.Background(), key.Value(), "v", "2").Err(); err != nil {
		t.Fatal(err)
	}
	if found, outcome, err := store.FindByID(context.Background(), fixture.snapshot.ID()); err == nil || outcome != domain.ResolveOutcomeUnspecified || found.Valid() {
		t.Fatalf("unknown schema accepted outcome=%v err=%v", outcome, err)
	}
	if err := client.HSet(context.Background(), key.Value(), "v", "1").Err(); err != nil {
		t.Fatal(err)
	}
	other := newTestFixture(t, "payloadmismatch")
	mismatched, err := domain.NewSnapshot(other.snapshot.ID(), fixture.snapshot.OwnerID(), fixture.snapshot.WorldID(), fixture.snapshot.Assignment(), fixture.snapshot.Lifecycle(), fixture.snapshot.Revision(), fixture.snapshot.Capacity(), fixture.snapshot.CreatedAt(), fixture.snapshot.ExpiresAt(), fixture.snapshot.OwnerBinding(), 0, time.Time{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	mismatchedPayload, err := encodeSnapshot(mismatched)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(context.Background(), key.Value(), "payload", mismatchedPayload).Err(); err != nil {
		t.Fatal(err)
	}
	if found, outcome, err := store.ResolveActive(context.Background(), fixture.snapshot.WorldID()); err == nil || outcome != domain.ResolveOutcomeUnspecified || found.Valid() {
		t.Fatalf("index payload contradiction accepted outcome=%v err=%v", outcome, err)
	}
	originalPayload, err := encodeSnapshot(fixture.snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(context.Background(), key.Value(), "payload", originalPayload).Err(); err != nil {
		t.Fatal(err)
	}
	originalFacts, err := snapshotFacts(fixture.snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(context.Background(), key.Value(), "facts", strings.Repeat("a", maximumSessionBytes)).Err(); err != nil {
		t.Fatal(err)
	}
	if found, outcome, err := store.FindByID(context.Background(), fixture.snapshot.ID()); err == nil || outcome != domain.ResolveOutcomeUnspecified || found.Valid() {
		t.Fatalf("oversized hash metadata accepted outcome=%v err=%v", outcome, err)
	}
	if err := client.HSet(context.Background(), key.Value(), "facts", originalFacts).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(context.Background(), key.Value(), "payload", strings.Repeat("x", maximumSessionBytes+1)).Err(); err != nil {
		t.Fatal(err)
	}
	if found, outcome, err := store.FindByID(context.Background(), fixture.snapshot.ID()); err == nil || outcome != domain.ResolveOutcomeUnspecified || found.Valid() {
		t.Fatalf("oversized corruption accepted outcome=%v err=%v", outcome, err)
	}
}

// TestStoreIntegrationResponseLossResolvesByCommandReplay 确定性证明Lua提交后丢响应只能复用原command解析。
func TestStoreIntegrationResponseLossResolvesByCommandReplay(t *testing.T) {
	store, _ := openIntegrationStore(t)
	fixture := newTestFixture(t, "loss")
	record := testCreateRecord(t, fixture.snapshot, "loss")
	if _, outcome, err := store.Create(context.Background(), record); err != nil || outcome != domain.CreateOutcomeCreated {
		t.Fatalf("create outcome=%v err=%v", outcome, err)
	}
	faultClient, fault := openResponseLossClient(t)
	defer func() { fault.release(); _ = faultClient.Close() }()
	if _, err := commitScript.Load(context.Background(), faultClient).Result(); err != nil {
		t.Fatal(err)
	}
	faultStore, err := New(faultClient, testKeyspace(t), fixedClock{now: time.Now().UTC()}, time.Hour, integrationObserver{})
	if err != nil {
		t.Fatal(err)
	}
	closeRecord := testCloseRecord(t, fixture.snapshot, "loss")
	type answer struct {
		result  domain.MutationResult
		outcome domain.MutationOutcome
		err     error
	}
	done := make(chan answer, 1)
	go func() {
		result, outcome, commitErr := faultStore.Commit(context.Background(), closeRecord)
		done <- answer{result: result, outcome: outcome, err: commitErr}
	}()
	select {
	case <-fault.commandWritten:
	case <-time.After(5 * time.Second):
		t.Fatal("Lua command was not written")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		found, outcome, findErr := store.FindByID(context.Background(), fixture.snapshot.ID())
		if findErr == nil && outcome == domain.ResolveOutcomeFound && found.Lifecycle() == domain.LifecycleClosed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("committed state not observed: outcome=%v err=%v", outcome, findErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	fault.release()
	first := <-done
	if first.err == nil || first.outcome != domain.MutationOutcomeCommitUnknown || first.result.Snapshot().Valid() {
		t.Fatalf("response loss outcome=%v err=%v", first.outcome, first.err)
	}
	replayed, outcome, err := store.Commit(context.Background(), closeRecord)
	if err != nil || outcome != domain.MutationOutcomeReplay || replayed.Snapshot().Lifecycle() != domain.LifecycleClosed {
		t.Fatalf("replay outcome=%v err=%v", outcome, err)
	}
	differentFingerprint := testFingerprint(t, "different-after-response-loss")
	differentResult, err := domain.NewMutationResult(domain.OperationClose, replayed.Snapshot(), closeRecord.CommandID(), differentFingerprint, domain.InviteSnapshot{}, domain.AdmissionIntent{}, domain.MembershipSnapshot{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	differentRecord, err := domain.NewTransitionRecord(domain.OperationClose, closeRecord.VisitSessionID(), closeRecord.ExpectedRevision(), closeRecord.CommandID(), differentFingerprint, differentResult)
	if err != nil {
		t.Fatal(err)
	}
	if result, conflictOutcome, conflictErr := store.Commit(context.Background(), differentRecord); conflictErr != nil || conflictOutcome != domain.MutationOutcomeIdempotencyConflict || result.Snapshot().Valid() {
		t.Fatalf("response-loss fingerprint conflict outcome=%v err=%v", conflictOutcome, conflictErr)
	}
}

// openIntegrationStore 清空脚本分配的隔离Redis并构造production adapter。
func openIntegrationStore(t testing.TB) (*Store, *redisclient.Client) {
	t.Helper()
	requireIntegration(t)
	client := openIntegrationRedis(t)
	t.Cleanup(func() { _ = client.Close() })
	if err := client.FlushDB(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	store, err := New(client, testKeyspace(t), fixedClock{now: time.Now().UTC()}, time.Hour, integrationObserver{})
	if err != nil {
		t.Fatal(err)
	}
	return store, client
}

// openIntegrationRedis 只从storage harness注入的文件与地址读取连接配置。
func openIntegrationRedis(t testing.TB) *redisclient.Client {
	t.Helper()
	password, err := os.ReadFile(os.Getenv("IHOMELAND_TEST_REDIS_PASSWORD_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	client := redisclient.NewClient(&redisclient.Options{Addr: os.Getenv("IHOMELAND_TEST_REDIS_ADDRESS"), Password: string(password), DialTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, MaxRetries: -1, MinRetryBackoff: -1, MaxRetryBackoff: -1})
	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		t.Fatal(err)
	}
	return client
}

// restartIntegrationRedis 重启本次harness专属容器并等待原client恢复可用。
//
// 30秒是Docker Desktop重启恢复的测试预算；200毫秒轮询避免固定长sleep，同时限制Ping频率。
func restartIntegrationRedis(t testing.TB, client *redisclient.Client) {
	t.Helper()
	command := exec.Command("docker", "restart", os.Getenv("IHOMELAND_TEST_REDIS_CONTAINER"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("restart redis: %v (%s)", err, output)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := client.Ping(ctx).Err()
		cancel()
		if err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("Redis did not recover before deadline")
}

// requireIntegration 防止普通单元测试误连开发者本地Redis。
func requireIntegration(t testing.TB) {
	t.Helper()
	if os.Getenv("IHOMELAND_STORAGE_INTEGRATION") != "1" {
		t.Skip("storage integration harness is required")
	}
}

// responseLossFault 在请求完整写入后、读取响应前建立确定性故障同步点。
type responseLossFault struct {
	// commandWritten 通知测试目标EVALSHA已经交给socket。
	commandWritten chan struct{}
	// releaseRead 控制何时向client返回注入错误。
	releaseRead chan struct{}
	// writeOnce保证只关闭一次写入通知。
	writeOnce sync.Once
	// releaseOnce保证只解除一次读取阻塞。
	releaseOnce sync.Once
	// dropping从目标EVALSHA写入后持续拒绝读取；该故障client随后只允许关闭，不再复用。
	dropping atomic.Bool
}

// release 解除响应读取阻塞，使调用方观察commit-unknown。
func (fault *responseLossFault) release() { fault.releaseOnce.Do(func() { close(fault.releaseRead) }) }

// responseLossConn 装饰单一Redis连接并只注入目标Lua响应丢失。
type responseLossConn struct {
	// Conn保留真实socket写入，使服务端仍可能提交。
	net.Conn
	// fault协调测试线程与socket读写。
	fault *responseLossFault
}

// Write 在完整EVALSHA请求交给socket后建立提交未知同步点。
func (connection *responseLossConn) Write(payload []byte) (int, error) {
	written, err := connection.Conn.Write(payload)
	if err == nil && bytes.Contains(bytes.ToUpper(payload[:written]), []byte("\r\nEVALSHA\r\n")) {
		connection.fault.dropping.Store(true)
		connection.fault.writeOnce.Do(func() { close(connection.fault.commandWritten) })
	}
	return written, err
}

// Read 在目标script可能提交后持续返回注入错误，不读取或输出任何key、response或payload。
func (connection *responseLossConn) Read(payload []byte) (int, error) {
	if connection.fault.dropping.Load() {
		<-connection.fault.releaseRead
		return 0, errors.New("injected redis response loss")
	}
	return connection.Conn.Read(payload)
}

// openResponseLossClient 创建禁用自动重试的单连接故障注入client。
func openResponseLossClient(t testing.TB) (*redisclient.Client, *responseLossFault) {
	t.Helper()
	password, err := os.ReadFile(os.Getenv("IHOMELAND_TEST_REDIS_PASSWORD_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	fault := &responseLossFault{commandWritten: make(chan struct{}), releaseRead: make(chan struct{})}
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	client := redisclient.NewClient(&redisclient.Options{
		Addr: os.Getenv("IHOMELAND_TEST_REDIS_ADDRESS"), Password: string(password), PoolSize: 1, MaxRetries: -1,
		DialTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
		Dialer: func(ctx context.Context, network, address string) (net.Conn, error) {
			connection, dialErr := dialer.DialContext(ctx, network, address)
			if dialErr != nil {
				return nil, dialErr
			}
			return &responseLossConn{Conn: connection, fault: fault}, nil
		},
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		t.Fatal(err)
	}
	return client, fault
}
