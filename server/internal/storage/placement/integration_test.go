//go:build storage_integration

package placement

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	domain "github.com/jinwiforz/ihomeland/server/internal/placement"
	storagemysql "github.com/jinwiforz/ihomeland/server/internal/storage/mysql"
	storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"
	redisclient "github.com/redis/go-redis/v9"
)

// integrationObserver 丢弃固定低基数观测；测试直接断言 allocation 与 transition 行为。
type integrationObserver struct{}

// RecordStorageOperation 满足 adapter observer contract。
func (integrationObserver) RecordStorageOperation(string, string, string) {}

// TestStoreIntegrationFencingReplayFlushAndSuccessorSafety 覆盖完整状态机、Redis flush 与 stale 拒绝。
func TestStoreIntegrationFencingReplayFlushAndSuccessorSafety(t *testing.T) {
	requireIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	db := openIntegrationDB(t)
	defer func() { _ = db.Close() }()
	if _, err := storagemysql.Migrate(ctx, db, "ihomeland", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	client := openIntegrationRedis(t)
	defer func() { _ = client.Close() }()
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	registry, err := storageredis.NewRegistry(Definitions())
	if err != nil {
		t.Fatal(err)
	}
	keyspace, err := storageredis.NewKeyspace("test", registry)
	if err != nil {
		t.Fatal(err)
	}
	store, err := New(db, client, keyspace, time.Minute, integrationObserver{})
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	worldID, _ := personalworld.NewPersonalWorldID("pworld_" + suffix)
	if _, err := db.ExecContext(ctx, `INSERT INTO personal_worlds
		(personal_world_id, owner_player_id, lifecycle, revision, created_at)
		VALUES (?, ?, 'active', 1, UTC_TIMESTAMP(6))`, worldID.String(), "ply_"+suffix); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	firstRequest := newAcquireRequest(t, worldID, "winst_a"+suffix, now, now.Add(5*time.Minute))
	first, outcome, err := store.Acquire(ctx, firstRequest)
	if err != nil || outcome != domain.StoreOutcomeApplied || first.Generation().Uint64() != 1 || first.FencingToken().Uint64() != 1 {
		t.Fatalf("first acquire = %v, %v", outcome, err)
	}
	activateRequest, _ := domain.NewStampRequest(first.Stamp(), now)
	active, outcome, err := store.Activate(ctx, activateRequest)
	if err != nil || outcome != domain.StoreOutcomeApplied || active.Phase() != domain.PhaseActive {
		t.Fatalf("activate = %v, %v", outcome, err)
	}
	fence, outcome, err := store.QualifyWrite(ctx, activateRequest)
	if err != nil || outcome != domain.StoreOutcomeApplied || !fence.Valid() {
		t.Fatalf("qualify = %v, %v", outcome, err)
	}
	renewRequest, _ := domain.NewRenewRequest(active.Stamp(), now, now.Add(10*time.Minute))
	renewed, outcome, err := store.Renew(ctx, renewRequest)
	if err != nil || outcome != domain.StoreOutcomeApplied || !renewed.Lease().ExpiresAt().Equal(now.Add(10*time.Minute)) {
		t.Fatalf("renew = %v, %v", outcome, err)
	}
	revokeRequest, _ := domain.NewStampRequest(renewed.Stamp(), now)
	revoked, outcome, err := store.Revoke(ctx, revokeRequest)
	if err != nil || outcome != domain.StoreOutcomeApplied || !revoked.Stamp().Equal(renewed.Stamp()) {
		t.Fatalf("revoke = %v, %v", outcome, err)
	}
	if replay, replayOutcome, replayErr := store.Revoke(ctx, revokeRequest); replayErr != nil || replayOutcome != domain.StoreOutcomeReplay || !replay.Stamp().Equal(revoked.Stamp()) {
		t.Fatalf("revoke replay = %v, %v", replayOutcome, replayErr)
	}

	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	if snapshot, retryOutcome, retryErr := store.Acquire(ctx, firstRequest); retryErr == nil || retryOutcome != domain.StoreOutcomeCommitUnknown || snapshot.Valid() {
		t.Fatalf("old allocation after flush = %v, %v", retryOutcome, retryErr)
	}
	secondRequest := newAcquireRequest(t, worldID, "winst_b"+suffix, now.Add(time.Second), now.Add(6*time.Minute))
	second, outcome, err := store.Acquire(ctx, secondRequest)
	if err != nil || outcome != domain.StoreOutcomeApplied || second.Generation().Uint64() <= first.Generation().Uint64() || second.FencingToken().Uint64() <= first.FencingToken().Uint64() {
		t.Fatalf("second acquire = %v, %v", outcome, err)
	}
	if staleFence, staleOutcome, staleErr := store.QualifyWrite(ctx, activateRequest); staleErr != nil || staleOutcome != domain.StoreOutcomeConflict || staleFence.Valid() {
		t.Fatalf("stale qualify = %v, %v", staleOutcome, staleErr)
	}
	staleRenewRequest, _ := domain.NewRenewRequest(first.Stamp(), now.Add(time.Second), now.Add(11*time.Minute))
	if stale, staleOutcome, staleErr := store.Renew(ctx, staleRenewRequest); staleErr != nil || staleOutcome != domain.StoreOutcomeConflict || stale.Valid() {
		t.Fatalf("stale renew = %v, %v", staleOutcome, staleErr)
	}
	secondStampRequest, _ := domain.NewStampRequest(second.Stamp(), now.Add(time.Second))
	secondActive, outcome, err := store.Activate(ctx, secondStampRequest)
	if err != nil || outcome != domain.StoreOutcomeApplied {
		t.Fatalf("second activate = %v, %v", outcome, err)
	}
	successorCandidate := newCandidate(t, worldID, "winst_c"+suffix, now.Add(2*time.Second), now.Add(7*time.Minute))
	replaceRequest, _ := domain.NewReplaceRequest(secondActive.Stamp(), successorCandidate, now.Add(2*time.Second))
	successor, outcome, err := store.Replace(ctx, replaceRequest)
	if err != nil || outcome != domain.StoreOutcomeApplied || successor.Generation().Uint64() <= second.Generation().Uint64() {
		t.Fatalf("replace = %v, %v", outcome, err)
	}
	retryCandidate := newCandidate(t, worldID, "winst_c"+suffix, now.Add(3*time.Second), now.Add(8*time.Minute))
	retryReplace, _ := domain.NewReplaceRequest(secondActive.Stamp(), retryCandidate, now.Add(3*time.Second))
	replayedSuccessor, replayOutcome, replayErr := store.Replace(ctx, retryReplace)
	if replayErr != nil || replayOutcome != domain.StoreOutcomeReplay || !replayedSuccessor.Equal(successor) {
		t.Fatalf("replace replay with advanced clock = %v, %v", replayOutcome, replayErr)
	}
	preFailureClient := openIntegrationRedis(t)
	preFailureClient.AddHook(&preExecutionFailureHook{})
	defer func() { _ = preFailureClient.Close() }()
	preFailureStore, newErr := New(db, preFailureClient, keyspace, time.Minute, integrationObserver{})
	if newErr != nil {
		t.Fatal(newErr)
	}
	preFailureRevoke, _ := domain.NewStampRequest(successor.Stamp(), now.Add(3*time.Second))
	if snapshot, failureOutcome, failureErr := preFailureStore.Revoke(ctx, preFailureRevoke); failureErr == nil || failureOutcome != domain.StoreOutcomeNotCommitted || snapshot.Valid() {
		t.Fatalf("revoke pre-execution failure = %v, %v", failureOutcome, failureErr)
	}
	if unchanged, unchangedOutcome, unchangedErr := store.Resolve(ctx, worldID, now.Add(3*time.Second)); unchangedErr != nil || unchangedOutcome != domain.ResolveOutcomeFound || !unchanged.Equal(successor) {
		t.Fatalf("revoke pre-execution state = %v, %v", unchangedOutcome, unchangedErr)
	}
	if stale, staleOutcome, staleErr := store.Revoke(ctx, secondStampRequest); staleErr != nil || staleOutcome != domain.StoreOutcomeConflict || stale.Valid() {
		t.Fatalf("stale revoke = %v, %v", staleOutcome, staleErr)
	}
	resolved, resolveOutcome, err := store.Resolve(ctx, worldID, now.Add(2*time.Second))
	if err != nil || resolveOutcome != domain.ResolveOutcomeFound || !resolved.Stamp().Equal(successor.Stamp()) {
		t.Fatalf("resolve successor = %v, %v", resolveOutcome, err)
	}
	expiredRequest, _ := domain.NewStampRequest(successor.Stamp(), successor.Lease().ExpiresAt())
	if expiredFence, expiredOutcome, expiredErr := store.QualifyWrite(ctx, expiredRequest); expiredErr != nil || expiredOutcome != domain.StoreOutcomeExpired || expiredFence.Valid() {
		t.Fatalf("exact expiry qualification = %v, %v", expiredOutcome, expiredErr)
	}

	secondWorldID, _ := personalworld.NewPersonalWorldID("pworld_d" + suffix)
	if _, err := db.ExecContext(ctx, `INSERT INTO personal_worlds
		(personal_world_id, owner_player_id, lifecycle, revision, created_at)
		VALUES (?, ?, 'active', 1, UTC_TIMESTAMP(6))`, secondWorldID.String(), "ply_d"+suffix); err != nil {
		t.Fatal(err)
	}
	duplicateCandidate := newCandidate(t, secondWorldID, successor.InstanceID().String(), now.Add(3*time.Second), now.Add(8*time.Minute))
	duplicateRequest, _ := domain.NewAcquireRequest(duplicateCandidate, now.Add(3*time.Second))
	if duplicate, duplicateOutcome, duplicateErr := store.Acquire(ctx, duplicateRequest); duplicateErr == nil || duplicateOutcome != domain.StoreOutcomeNotCommitted || duplicate.Valid() {
		t.Fatalf("cross-world duplicate instance = %v, %v", duplicateOutcome, duplicateErr)
	}

	overflowWorldID, _ := personalworld.NewPersonalWorldID("pworld_e" + suffix)
	if _, err := db.ExecContext(ctx, `INSERT INTO personal_worlds
		(personal_world_id, owner_player_id, lifecycle, revision, created_at)
		VALUES (?, ?, 'active', 1, UTC_TIMESTAMP(6))`, overflowWorldID.String(), "ply_e"+suffix); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO placement_sequences
		(personal_world_id, generation_high, fencing_high, updated_at)
		VALUES (?, ?, ?, UTC_TIMESTAMP(6))`, overflowWorldID.String(), ^uint64(0), ^uint64(0)); err != nil {
		t.Fatal(err)
	}
	overflowRequest := newAcquireRequest(t, overflowWorldID, "winst_e"+suffix, now.Add(4*time.Second), now.Add(9*time.Minute))
	if overflow, overflowOutcome, overflowErr := store.Acquire(ctx, overflowRequest); overflowErr == nil || overflowOutcome != domain.StoreOutcomeNotCommitted || overflow.Valid() {
		t.Fatalf("sequence overflow = %v, %v", overflowOutcome, overflowErr)
	}

	responseWorldID, _ := personalworld.NewPersonalWorldID("pworld_f" + suffix)
	if _, err := db.ExecContext(ctx, `INSERT INTO personal_worlds
		(personal_world_id, owner_player_id, lifecycle, revision, created_at)
		VALUES (?, ?, 'active', 1, UTC_TIMESTAMP(6))`, responseWorldID.String(), "ply_f"+suffix); err != nil {
		t.Fatal(err)
	}
	faultClient := openIntegrationRedis(t)
	faultClient.AddHook(&postResultLossHook{})
	defer func() { _ = faultClient.Close() }()
	faultStore, err := New(db, faultClient, keyspace, time.Minute, integrationObserver{})
	if err != nil {
		t.Fatal(err)
	}
	responseRequest := newAcquireRequest(t, responseWorldID, "winst_f"+suffix, now.Add(5*time.Second), now.Add(10*time.Minute))
	responseSnapshot, responseOutcome, responseErr := faultStore.Acquire(ctx, responseRequest)
	if responseErr != nil || responseOutcome != domain.StoreOutcomeReplay || !responseSnapshot.Valid() {
		t.Fatalf("response-loss resolve = %v, %v", responseOutcome, responseErr)
	}

	concurrentWorldID, _ := personalworld.NewPersonalWorldID("pworld_g" + suffix)
	if _, err := db.ExecContext(ctx, `INSERT INTO personal_worlds
		(personal_world_id, owner_player_id, lifecycle, revision, created_at)
		VALUES (?, ?, 'active', 1, UTC_TIMESTAMP(6))`, concurrentWorldID.String(), "ply_g"+suffix); err != nil {
		t.Fatal(err)
	}
	storeB, err := New(db, client, keyspace, time.Minute, integrationObserver{})
	if err != nil {
		t.Fatal(err)
	}
	concurrentRequests := []domain.AcquireRequest{
		newAcquireRequest(t, concurrentWorldID, "winst_g1"+suffix, now.Add(6*time.Second), now.Add(11*time.Minute)),
		newAcquireRequest(t, concurrentWorldID, "winst_g2"+suffix, now.Add(6*time.Second), now.Add(11*time.Minute)),
	}
	type acquireResult struct {
		// snapshot 是两个 adapter 最终观察到的同一 current assignment。
		snapshot domain.AssignmentSnapshot
		// outcome 允许 linearization winner 或观察到 winner 的 follower 结果。
		outcome domain.StoreOutcome
		// err 保存安全 adapter failure；成功竞争必须为 nil。
		err error
	}
	concurrentResults := make(chan acquireResult, 2)
	start := make(chan struct{})
	for index, adapter := range []*Store{store, storeB} {
		go func(currentStore *Store, request domain.AcquireRequest) {
			<-start
			snapshot, currentOutcome, currentErr := currentStore.Acquire(ctx, request)
			concurrentResults <- acquireResult{snapshot: snapshot, outcome: currentOutcome, err: currentErr}
		}(adapter, concurrentRequests[index])
	}
	close(start)
	var concurrentCurrent domain.AssignmentSnapshot
	for range 2 {
		result := <-concurrentResults
		if result.err != nil || !result.snapshot.Valid() || result.outcome != domain.StoreOutcomeApplied && result.outcome != domain.StoreOutcomeInProgress && result.outcome != domain.StoreOutcomeExisting {
			t.Fatalf("concurrent acquire = %v, %v", result.outcome, result.err)
		}
		if !concurrentCurrent.Valid() {
			concurrentCurrent = result.snapshot
		} else if !concurrentCurrent.Stamp().Equal(result.snapshot.Stamp()) {
			t.Fatal("concurrent adapters returned different current assignments")
		}
	}
	var generationHigh uint64
	var fenceHigh uint64
	if err := db.QueryRowContext(ctx, `SELECT generation_high, fencing_high FROM placement_sequences
		WHERE personal_world_id = ?`, concurrentWorldID.String()).Scan(&generationHigh, &fenceHigh); err != nil || generationHigh == 0 || generationHigh != fenceHigh {
		t.Fatalf("concurrent high-watermark = %d/%d, %v", generationHigh, fenceHigh, err)
	}
	corruptWorldID, _ := personalworld.NewPersonalWorldID("pworld_h" + suffix)
	corruptKey, _ := keyspace.Build(assignmentDefinitionName, corruptWorldID.String())
	if err := client.HSet(ctx, corruptKey.Value(), map[string]any{
		"v": "1", "world": corruptWorldID.String(), "instance": "winst_h" + suffix, "node": "rnode_integration",
		"generation": "1", "fence": "1", "phase": "active", "created_us": fmt.Sprint(now.UnixMicro()),
		"expires_us": fmt.Sprint(now.Add(time.Minute).UnixMicro()), "unexpected": "field",
	}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Expire(ctx, corruptKey.Value(), time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if corrupt, corruptOutcome, corruptErr := store.Resolve(ctx, corruptWorldID, now); corruptErr == nil || corruptOutcome != domain.ResolveOutcomeUnspecified || corrupt.Valid() {
		t.Fatalf("corrupt redis hydration = %v, %v", corruptOutcome, corruptErr)
	}
	noTTLWorldID, _ := personalworld.NewPersonalWorldID("pworld_j" + suffix)
	noTTLKey, _ := keyspace.Build(assignmentDefinitionName, noTTLWorldID.String())
	if err := client.HSet(ctx, noTTLKey.Value(), map[string]any{
		"v": "1", "world": noTTLWorldID.String(), "instance": "winst_j" + suffix, "node": "rnode_integration",
		"generation": "1", "fence": "1", "phase": "active", "created_us": fmt.Sprint(now.UnixMicro()),
		"expires_us": fmt.Sprint(now.Add(time.Minute).UnixMicro()),
	}).Err(); err != nil {
		t.Fatal(err)
	}
	if permanent, permanentOutcome, permanentErr := store.Resolve(ctx, noTTLWorldID, now); permanentErr == nil || permanentOutcome != domain.ResolveOutcomeUnspecified || permanent.Valid() {
		t.Fatalf("missing TTL hydration = %v, %v", permanentOutcome, permanentErr)
	}

	restartRedis(t, client)
	if recovered, recoveredOutcome, recoveredErr := store.Resolve(ctx, worldID, now.Add(2*time.Second)); recoveredErr != nil || recoveredOutcome != domain.ResolveOutcomeFound || !recovered.Stamp().Equal(successor.Stamp()) {
		t.Fatalf("redis restart recovery = %v, %v", recoveredOutcome, recoveredErr)
	}
	restartWorldID, _ := personalworld.NewPersonalWorldID("pworld_i" + suffix)
	if _, err := db.ExecContext(ctx, `INSERT INTO personal_worlds
		(personal_world_id, owner_player_id, lifecycle, revision, created_at)
		VALUES (?, ?, 'active', 1, UTC_TIMESTAMP(6))`, restartWorldID.String(), "ply_i"+suffix); err != nil {
		t.Fatal(err)
	}
	beforeRestartRequest := newAcquireRequest(t, restartWorldID, "winst_i1"+suffix, now.Add(7*time.Second), now.Add(12*time.Minute))
	beforeRestart, beforeOutcome, beforeErr := store.Acquire(ctx, beforeRestartRequest)
	if beforeErr != nil || beforeOutcome != domain.StoreOutcomeApplied {
		t.Fatalf("before mysql restart = %v, %v", beforeOutcome, beforeErr)
	}
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	restartMySQL(t, db)
	afterRestartRequest := newAcquireRequest(t, restartWorldID, "winst_i2"+suffix, now.Add(8*time.Second), now.Add(13*time.Minute))
	afterRestart, afterOutcome, afterErr := store.Acquire(ctx, afterRestartRequest)
	if afterErr != nil || afterOutcome != domain.StoreOutcomeApplied || afterRestart.Generation().Uint64() <= beforeRestart.Generation().Uint64() || afterRestart.FencingToken().Uint64() <= beforeRestart.FencingToken().Uint64() {
		t.Fatalf("after mysql restart = %v, %v", afterOutcome, afterErr)
	}
}

// postResultLossHook 在首个 EVAL 已完成后隐藏结果，确定性模拟 mutation response loss。
type postResultLossHook struct {
	// injected 保证只破坏一次 mutation，后续 evidence read 正常执行。
	injected atomic.Bool
}

// preExecutionFailureHook 在首个 EVAL 发送前失败，证明 resolver 不会把未执行 mutation 误报为 replay。
type preExecutionFailureHook struct {
	// injected 保证只阻止一次 mutation，后续 evidence read 可以观察 authoritative state。
	injected atomic.Bool
}

// DialHook 不修改 connection 获取；故障只位于 mutation command 之前。
func (*preExecutionFailureHook) DialHook(next redisclient.DialHook) redisclient.DialHook { return next }

// ProcessHook 在首个 EVAL 调用 next 前返回错误，确定性证明 Redis 未执行 script。
func (hook *preExecutionFailureHook) ProcessHook(next redisclient.ProcessHook) redisclient.ProcessHook {
	return func(ctx context.Context, command redisclient.Cmder) error {
		if command.Name() == "eval" && hook.injected.CompareAndSwap(false, true) {
			return errors.New("injected redis pre-execution failure")
		}
		return next(ctx, command)
	}
}

// ProcessPipelineHook 不修改 pipeline；placement mutation 不通过 pipeline 发送。
func (*preExecutionFailureHook) ProcessPipelineHook(next redisclient.ProcessPipelineHook) redisclient.ProcessPipelineHook {
	return next
}

// DialHook 不修改 connection 获取；故障只位于 command result 边界。
func (*postResultLossHook) DialHook(next redisclient.DialHook) redisclient.DialHook { return next }

// ProcessHook 先执行 authoritative command，再把首个成功 EVAL 改写为稳定 injected error。
func (hook *postResultLossHook) ProcessHook(next redisclient.ProcessHook) redisclient.ProcessHook {
	return func(ctx context.Context, command redisclient.Cmder) error {
		err := next(ctx, command)
		if err == nil && command.Name() == "eval" && hook.injected.CompareAndSwap(false, true) {
			return errors.New("injected redis response loss")
		}
		return err
	}
}

// ProcessPipelineHook 不修改 pipeline；placement mutation 不通过 pipeline 发送。
func (*postResultLossHook) ProcessPipelineHook(next redisclient.ProcessPipelineHook) redisclient.ProcessPipelineHook {
	return next
}

// newAcquireRequest 构造使用固定 observedAt 的合法 integration request。
func newAcquireRequest(t *testing.T, worldID personalworld.PersonalWorldID, instanceValue string, observedAt time.Time, expiresAt time.Time) domain.AcquireRequest {
	t.Helper()
	candidate := newCandidate(t, worldID, instanceValue, observedAt, expiresAt)
	request, err := domain.NewAcquireRequest(candidate, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

// newCandidate 构造单次不可复活 WorldInstance candidate。
func newCandidate(t *testing.T, worldID personalworld.PersonalWorldID, instanceValue string, createdAt time.Time, expiresAt time.Time) domain.AssignmentCandidate {
	t.Helper()
	instanceID, err := domain.NewWorldInstanceID(instanceValue)
	if err != nil {
		t.Fatal(err)
	}
	nodeID, _ := domain.NewRuntimeNodeID("rnode_integration")
	candidate, err := domain.NewAssignmentCandidate(worldID, instanceID, nodeID, createdAt, expiresAt)
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

// openIntegrationDB 使用 harness file secret 创建测试 pool，不记录 DSN 或 password。
func openIntegrationDB(t *testing.T) *sql.DB {
	t.Helper()
	password, err := os.ReadFile(os.Getenv("IHOMELAND_TEST_MYSQL_PASSWORD_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	config := mysqldriver.NewConfig()
	config.User = "ihomeland"
	config.Passwd = string(password)
	config.Net = "tcp"
	config.Addr = os.Getenv("IHOMELAND_TEST_MYSQL_ADDRESS")
	config.DBName = "ihomeland"
	config.Timeout = 3 * time.Second
	config.ParseTime = true
	config.Loc = time.UTC
	config.Params = map[string]string{"time_zone": "'+00:00'", "sql_mode": "'STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION'"}
	connector, err := mysqldriver.NewConnector(config)
	if err != nil {
		t.Fatal(err)
	}
	return sql.OpenDB(connector)
}

// openIntegrationRedis 使用 harness password 创建禁用 retry 的测试 client。
func openIntegrationRedis(t *testing.T) *redisclient.Client {
	t.Helper()
	password, err := os.ReadFile(os.Getenv("IHOMELAND_TEST_REDIS_PASSWORD_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	client := redisclient.NewClient(&redisclient.Options{
		Addr: os.Getenv("IHOMELAND_TEST_REDIS_ADDRESS"), Password: string(password),
		DialTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
		MaxRetries: -1, MinRetryBackoff: -1, MaxRetryBackoff: -1,
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		t.Fatal(err)
	}
	return client
}

// restartRedis 重启 harness container，并有界等待现有 client pool 重连。
func restartRedis(t *testing.T, client *redisclient.Client) {
	t.Helper()
	command := exec.Command("docker", "restart", os.Getenv("IHOMELAND_TEST_REDIS_CONTAINER"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("restart redis container: %v (%s)", err, output)
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

// restartMySQL 重启 harness container，并有界等待 allocation pool 重新建连。
func restartMySQL(t *testing.T, db *sql.DB) {
	t.Helper()
	command := exec.Command("docker", "restart", os.Getenv("IHOMELAND_TEST_MYSQL_CONTAINER"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("restart mysql container: %v (%s)", err, output)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := db.PingContext(ctx)
		cancel()
		if err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("MySQL did not recover before deadline")
}

// requireIntegration 防止开发者绕过统一 harness 误连本机 storage。
func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("IHOMELAND_STORAGE_INTEGRATION") != "1" {
		t.Skip("storage integration harness is required")
	}
}
