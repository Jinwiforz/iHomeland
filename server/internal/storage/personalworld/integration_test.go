//go:build storage_integration

package personalworld

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jinwiforz/ihomeland/server/internal/account"
	domain "github.com/jinwiforz/ihomeland/server/internal/personalworld"
	storagemysql "github.com/jinwiforz/ihomeland/server/internal/storage/mysql"
)

// integrationObserver 丢弃固定低基数观测；测试直接断言 repository 结果。
type integrationObserver struct{}

// RecordStorageOperation 满足 adapter observer contract。
func (integrationObserver) RecordStorageOperation(string, string, string) {}

// TestRepositoryIntegrationConcurrencyReplayAndHydration 覆盖 owner 唯一、revision、replay 与损坏行拒绝。
func TestRepositoryIntegrationConcurrencyReplayAndHydration(t *testing.T) {
	requireIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	db := openIntegrationDB(t)
	defer func() { _ = db.Close() }()
	if _, err := storagemysql.Migrate(ctx, db, "ihomeland", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	repositoryA, err := New(db, integrationObserver{})
	if err != nil {
		t.Fatal(err)
	}
	repositoryB, err := New(db, integrationObserver{})
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	ownerID, _ := account.NewPlayerID("ply_" + suffix)
	worldA, _ := domain.NewPersonalWorldID("pworld_a" + suffix)
	worldB, _ := domain.NewPersonalWorldID("pworld_b" + suffix)
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	candidateA, _ := domain.NewSnapshot(worldA, ownerID, domain.LifecycleActive, domain.InitialRevision, createdAt)
	candidateB, _ := domain.NewSnapshot(worldB, ownerID, domain.LifecycleActive, domain.InitialRevision, createdAt)

	type ensureResult struct {
		// snapshot 是并发调用返回的确定 primary world。
		snapshot domain.Snapshot
		// outcome 区分唯一 created 与 existing follower。
		outcome domain.EnsureOutcome
		// err 保存 adapter 安全错误，不包含 SQL 或 identity。
		err error
	}
	results := make(chan ensureResult, 2)
	var wait sync.WaitGroup
	for _, pair := range []struct {
		// repository 是两个独立 adapter instance 之一。
		repository *Repository
		// candidate 使用不同 WorldID 竞争同一 owner unique key。
		candidate domain.Snapshot
	}{{repositoryA, candidateA}, {repositoryB, candidateB}} {
		wait.Add(1)
		go func(repository *Repository, candidate domain.Snapshot) {
			defer wait.Done()
			snapshot, outcome, ensureErr := repository.EnsurePrimary(ctx, candidate)
			results <- ensureResult{snapshot: snapshot, outcome: outcome, err: ensureErr}
		}(pair.repository, pair.candidate)
	}
	wait.Wait()
	close(results)
	var primary domain.Snapshot
	createdCount := 0
	for result := range results {
		if result.err != nil || !result.snapshot.Valid() {
			t.Fatalf("concurrent ensure = %v, %v", result.outcome, result.err)
		}
		if result.outcome == domain.EnsureOutcomeCreated {
			createdCount++
		}
		if !primary.Valid() {
			primary = result.snapshot
		} else if !primary.Equal(result.snapshot) {
			t.Fatalf("ensure returned different primary worlds")
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count = %d", createdCount)
	}
	found, findOutcome, err := repositoryA.FindByID(ctx, primary.ID())
	if err != nil || findOutcome != domain.FindOutcomeFound || !found.Equal(primary) {
		t.Fatalf("find primary = %v, %v", findOutcome, err)
	}

	key, _ := domain.NewIdempotencyKey("archive:" + suffix)
	command, _ := domain.NewArchiveCommand(ownerID, primary.ID(), primary.Revision(), key)
	record, _ := domain.NewArchiveRecord(command, primary)
	applied, mutationOutcome, err := repositoryA.CommitArchive(ctx, record)
	if err != nil || mutationOutcome != domain.MutationOutcomeApplied || !applied.Valid() {
		t.Fatalf("archive applied = %v, %v", mutationOutcome, err)
	}
	replayed, mutationOutcome, err := repositoryB.CommitArchive(ctx, record)
	if err != nil || mutationOutcome != domain.MutationOutcomeReplay || !replayed.World().Equal(applied.World()) {
		t.Fatalf("archive replay = %v, %v", mutationOutcome, err)
	}
	conflictCommand, _ := domain.NewArchiveCommand(ownerID, primary.ID(), applied.World().Revision(), key)
	conflictRecord, _ := domain.NewArchiveRecord(conflictCommand, applied.World())
	if result, conflictOutcome, conflictErr := repositoryA.CommitArchive(ctx, conflictRecord); conflictErr != nil || conflictOutcome != domain.MutationOutcomeIdempotencyConflict || result.Valid() {
		t.Fatalf("idempotency conflict = %v, %v", conflictOutcome, conflictErr)
	}

	competitionOwner, _ := account.NewPlayerID("ply_c" + suffix)
	competitionWorldID, _ := domain.NewPersonalWorldID("pworld_c" + suffix)
	competitionSnapshot, _ := domain.NewSnapshot(competitionWorldID, competitionOwner, domain.LifecycleActive, domain.InitialRevision, createdAt)
	if _, ensureOutcome, ensureErr := repositoryA.EnsurePrimary(ctx, competitionSnapshot); ensureErr != nil || ensureOutcome != domain.EnsureOutcomeCreated {
		t.Fatalf("competition ensure = %v, %v", ensureOutcome, ensureErr)
	}
	competitionRecords := make([]domain.ArchiveRecord, 2)
	for index, keyValue := range []string{"archive-c1:" + suffix, "archive-c2:" + suffix} {
		competitionKey, _ := domain.NewIdempotencyKey(keyValue)
		competitionCommand, _ := domain.NewArchiveCommand(competitionOwner, competitionWorldID, domain.InitialRevision, competitionKey)
		competitionRecords[index], _ = domain.NewArchiveRecord(competitionCommand, competitionSnapshot)
	}
	mutationResults := make(chan domain.MutationOutcome, 2)
	for index, repository := range []*Repository{repositoryA, repositoryB} {
		go func(currentRepository *Repository, record domain.ArchiveRecord) {
			_, currentOutcome, _ := currentRepository.CommitArchive(ctx, record)
			mutationResults <- currentOutcome
		}(repository, competitionRecords[index])
	}
	appliedCount := 0
	terminalCount := 0
	for range 2 {
		switch <-mutationResults {
		case domain.MutationOutcomeApplied:
			appliedCount++
		case domain.MutationOutcomeRevisionConflict, domain.MutationOutcomeInvalidState:
			terminalCount++
		}
	}
	if appliedCount != 1 || terminalCount != 1 {
		t.Fatalf("revision competition = applied %d terminal %d", appliedCount, terminalCount)
	}

	corruptWorldID := "pworld_corrupt" + suffix
	if _, err := db.ExecContext(ctx, `INSERT INTO personal_worlds
		(personal_world_id, owner_player_id, lifecycle, revision, created_at)
		VALUES (?, ?, 'active', 1, UTC_TIMESTAMP(6))`, corruptWorldID, "invalid_owner"); err != nil {
		t.Fatal(err)
	}
	corruptID, _ := domain.NewPersonalWorldID(corruptWorldID)
	if snapshot, outcome, findErr := repositoryA.FindByID(ctx, corruptID); findErr == nil || outcome != domain.FindOutcomeUnspecified || snapshot.Valid() {
		t.Fatalf("corrupt hydration = %v, %v", outcome, findErr)
	}
	restartMySQL(t, db)
	if recovered, recoveredOutcome, recoveredErr := repositoryB.FindByID(ctx, primary.ID()); recoveredErr != nil || recoveredOutcome != domain.FindOutcomeFound || !recovered.Equal(applied.World()) {
		t.Fatalf("restart recovery = %v, %v", recoveredOutcome, recoveredErr)
	}
}

// restartMySQL 重启 harness 已登记 container，并有界等待现有 pool 重连后读取持久事实。
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

// openIntegrationDB 使用 harness file secret 创建只属于测试的 pool，不记录 DSN 或 password。
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

// requireIntegration 防止开发者绕过统一 harness 误连本机 MySQL。
func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("IHOMELAND_STORAGE_INTEGRATION") != "1" {
		t.Skip("storage integration harness is required")
	}
}
