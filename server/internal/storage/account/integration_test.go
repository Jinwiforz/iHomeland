//go:build storage_integration

package account

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	domain "github.com/jinwiforz/ihomeland/server/internal/account"
	storagemysql "github.com/jinwiforz/ihomeland/server/internal/storage/mysql"
)

// integrationObserver 丢弃固定低基数观测；测试直接断言 repository outcome。
type integrationObserver struct{}

// RecordStorageOperation 满足 adapter observer contract。
func (integrationObserver) RecordStorageOperation(string, string, string) {}

// TestRepositoryIntegrationConcurrencyCorruptionAndRestart 验证唯一性、strict hydration和持久恢复。
func TestRepositoryIntegrationConcurrencyCorruptionAndRestart(t *testing.T) {
	requireIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	db := openIntegrationDB(t)
	defer func() { _ = db.Close() }()
	if _, err := storagemysql.Migrate(ctx, db, "ihomeland", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	repository, err := New(db, integrationObserver{})
	if err != nil {
		t.Fatal(err)
	}
	hasher, _, err := NewHasher(2)
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	username := "race." + suffix
	records := []domain.CreateRecord{
		integrationRecord(t, hasher, "acc_a"+suffix, "ply_a"+suffix, username),
		integrationRecord(t, hasher, "acc_b"+suffix, "ply_b"+suffix, strings.ToUpper(username)),
	}
	type result struct {
		// outcome 是单次并发Create分类。
		outcome domain.CreateOutcome
		// err 是默认脱敏adapter错误。
		err error
	}
	results := make(chan result, 2)
	var wait sync.WaitGroup
	for _, record := range records {
		wait.Add(1)
		go func(candidate domain.CreateRecord) {
			defer wait.Done()
			outcome, createErr := repository.Create(ctx, candidate)
			results <- result{outcome: outcome, err: createErr}
		}(record)
	}
	wait.Wait()
	close(results)
	created := 0
	conflicts := 0
	for current := range results {
		if current.err != nil {
			t.Fatalf("Create() error = %v", current.err)
		}
		switch current.outcome {
		case domain.CreateOutcomeCreated:
			created++
		case domain.CreateOutcomeUsernameConflict:
			conflicts++
		}
	}
	if created != 1 || conflicts != 1 {
		t.Fatalf("concurrent outcomes created=%d conflicts=%d", created, conflicts)
	}
	canonical, _ := domain.NewUsername(username)
	found, outcome, err := repository.FindForAuthentication(ctx, canonical)
	if err != nil || outcome != domain.FindOutcomeFound || !found.Account.Valid() || !found.Credential.Valid() {
		t.Fatalf("FindForAuthentication() = %v, %v", outcome, err)
	}

	collision := integrationRecord(t, hasher, found.Account.ID().String(), "ply_c"+suffix, "other."+suffix)
	if collisionOutcome, collisionErr := repository.Create(ctx, collision); collisionErr == nil || collisionOutcome != domain.CreateOutcomeNotCommitted {
		t.Fatalf("AccountID collision = %v, %v", collisionOutcome, collisionErr)
	}
	playerCollision := integrationRecord(t, hasher, "acc_c"+suffix, found.Account.PlayerID().String(), "player."+suffix)
	if collisionOutcome, collisionErr := repository.Create(ctx, playerCollision); collisionErr == nil || collisionOutcome != domain.CreateOutcomeNotCommitted {
		t.Fatalf("PlayerID collision = %v, %v", collisionOutcome, collisionErr)
	}
	corruptUsername := "corrupt." + suffix
	if _, err := db.ExecContext(ctx, `INSERT INTO accounts
		(account_id, player_id, username, display_name, credential_hash, status, created_at)
		VALUES (?, ?, ?, ?, ?, 'active', UTC_TIMESTAMP(6))`, "acc_d"+suffix, "ply_d"+suffix,
		corruptUsername, "损坏账号", "$argon2id$bad"); err != nil {
		t.Fatal(err)
	}
	corruptKey, _ := domain.NewUsername(corruptUsername)
	if _, corruptOutcome, corruptErr := repository.FindForAuthentication(ctx, corruptKey); corruptErr == nil || corruptOutcome != domain.FindOutcomeUnspecified {
		t.Fatalf("corrupt hydration = %v, %v", corruptOutcome, corruptErr)
	}
	restartIntegrationMySQL(t, db)
	if recovered, recoveredOutcome, recoveredErr := repository.FindForAuthentication(ctx, canonical); recoveredErr != nil || recoveredOutcome != domain.FindOutcomeFound || recovered.Account.ID() != found.Account.ID() {
		t.Fatalf("restart recovery = %v, %v", recoveredOutcome, recoveredErr)
	}
}

// integrationRecord 创建不同 identity 但可竞争同一 canonical username 的完整记录。
func integrationRecord(t *testing.T, hasher *Hasher, accountValue string, playerValue string, usernameValue string) domain.CreateRecord {
	t.Helper()
	accountID, _ := domain.NewAccountID(accountValue)
	playerID, _ := domain.NewPlayerID(playerValue)
	username, _ := domain.NewUsername(usernameValue)
	displayName, _ := domain.NewDisplayName("集成测试玩家")
	entity, err := domain.NewAccount(accountID, playerID, username, displayName, domain.StatusActive, time.Now().UTC().Truncate(time.Microsecond))
	if err != nil {
		t.Fatal(err)
	}
	password, _ := domain.NewRegisterPassword("StrongPassword1")
	hash, err := hasher.Hash(context.Background(), password)
	if err != nil {
		t.Fatal(err)
	}
	return domain.CreateRecord{Account: entity, Credential: hash}
}

// openIntegrationDB 使用harness file secret创建测试pool，不记录DSN或password。
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

// restartIntegrationMySQL 重启harness container并等待现有pool恢复。
func restartIntegrationMySQL(t *testing.T, db *sql.DB) {
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

// requireIntegration 防止绕过统一harness误连本机MySQL。
func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("IHOMELAND_STORAGE_INTEGRATION") != "1" {
		t.Skip("storage integration harness is required")
	}
}
