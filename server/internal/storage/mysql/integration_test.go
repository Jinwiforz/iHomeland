//go:build storage_integration

package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jinwiforz/ihomeland/server/internal/config"
	"github.com/jinwiforz/ihomeland/server/internal/secret"
)

// integrationTaskOwner 让 component 验收真实资源，同时避免测试创建独立长期 probe goroutine。
type integrationTaskOwner struct{}

// Go 接受已验证 task 注册；周期 probe 由 process integration 单独覆盖。
func (*integrationTaskOwner) Go(string, func(context.Context) error) error { return nil }

// Stop 保持 fake owner 幂等，不拥有外部资源。
func (*integrationTaskOwner) Stop(context.Context, error) error { return nil }

// integrationObserver 丢弃低基数观测，测试只断言 storage 行为。
type integrationObserver struct{}

// ObserveStorageProbe 满足 component observer contract。
func (integrationObserver) ObserveStorageProbe(string, string, float64) {}

// ObserveMigration 满足 component observer contract。
func (integrationObserver) ObserveMigration(string, int) {}

// SetStoragePool 满足 component observer contract。
func (integrationObserver) SetStoragePool(string, string, int) {}

// RecordStorageOperation 满足 component observer contract。
func (integrationObserver) RecordStorageOperation(string, string, string) {}

// TestMySQLIntegrationRuntimeMigrationAndRecovery 覆盖空库、并发、dirty/checksum/lock、commit 中断和 restart。
func TestMySQLIntegrationRuntimeMigrationAndRecovery(t *testing.T) {
	requireIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	db := openIntegrationDB(t, "ihomeland", "IHOMELAND_TEST_MYSQL_PASSWORD_FILE")
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("关闭 integration database 失败：%v", err)
		}
	}()
	if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS ih_schema_migrations"); err != nil {
		t.Fatal(err)
	}
	result, err := Migrate(ctx, db, "ihomeland", 10*time.Second)
	if err != nil || result.Applied != 1 {
		t.Fatalf("empty migration = %+v, %v", result, err)
	}
	result, err = Migrate(ctx, db, "ihomeland", 10*time.Second)
	if err != nil || result.Applied != 0 {
		t.Fatalf("repeat migration = %+v, %v", result, err)
	}
	lockConnection, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var acquired int
	if err := lockConnection.QueryRowContext(ctx, "SELECT GET_LOCK('ihomeland:migration:ihomeland', 0)").Scan(&acquired); err != nil || acquired != 1 {
		t.Fatalf("hold migration lock = %d, %v", acquired, err)
	}
	if _, err := Migrate(ctx, db, "ihomeland", time.Second); err == nil || !strings.Contains(err.Error(), "lock failed") {
		t.Fatalf("migration lock timeout error = %v", err)
	}
	var released int
	if err := lockConnection.QueryRowContext(ctx, "SELECT RELEASE_LOCK('ihomeland:migration:ihomeland')").Scan(&released); err != nil || released != 1 {
		t.Fatalf("release migration lock = %d, %v", released, err)
	}
	if err := lockConnection.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, "DROP TABLE ih_schema_migrations"); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	results := make(chan MigrationResult, 4)
	failures := make(chan error, 4)
	// 四个并发启动者足以同时竞争同一 advisory lock，且保持本地/CI 连接预算有界。
	for index := 0; index < 4; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			value, migrateErr := Migrate(ctx, db, "ihomeland", 10*time.Second)
			if migrateErr != nil {
				failures <- migrateErr
				return
			}
			results <- value
		}()
	}
	wait.Wait()
	close(results)
	close(failures)
	for failure := range failures {
		t.Fatalf("concurrent migration error = %v", failure)
	}
	totalApplied := 0
	for value := range results {
		totalApplied += value.Applied
	}
	if totalApplied != 1 {
		t.Fatalf("concurrent applied total = %d", totalApplied)
	}

	if _, err := db.ExecContext(ctx, "UPDATE ih_schema_migrations SET state='in_progress' WHERE version=1"); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(ctx, db, "ihomeland", time.Second); err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("dirty migration error = %v", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE ih_schema_migrations SET state='applied', checksum=REPEAT(0x00, 32) WHERE version=1"); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(ctx, db, "ihomeland", time.Second); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("checksum migration error = %v", err)
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE ih_schema_migrations"); err != nil {
		t.Fatal(err)
	}

	settings := config.DefaultStorage().MySQL
	settings.Address = os.Getenv("IHOMELAND_TEST_MYSQL_ADDRESS")
	password := resolveIntegrationSecret(t, os.Getenv("IHOMELAND_TEST_MYSQL_PASSWORD_FILE"))
	component, err := New(settings, password, nil, &integrationTaskOwner{}, integrationObserver{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := component.Start(ctx); err != nil {
		t.Fatalf("component.Start() = %v", err)
	}
	if component.DB() == nil {
		t.Fatal("started component must expose pool")
	}
	if err := component.Stop(ctx); err != nil {
		t.Fatalf("component.Stop() = %v", err)
	}
	if err := component.Stop(ctx); err != nil {
		t.Fatalf("idempotent Stop() = %v", err)
	}

	assertCommitUnknown(t, ctx)
	callbackCalls := 0
	callbackFailure := errors.New("owner conflict")
	if err := WithinTx(ctx, db, nil, func(*sql.Tx) error { callbackCalls++; return callbackFailure }); !errors.Is(err, callbackFailure) {
		t.Fatalf("callback error = %v", err)
	}
	if callbackCalls != 1 {
		t.Fatalf("callback calls = %d", callbackCalls)
	}
	restartContainer(t, os.Getenv("IHOMELAND_TEST_MYSQL_CONTAINER"))
	waitForPing(t, db)
}

// assertCommitUnknown 由 root connection 杀死 transaction connection，验证 callback 不会重放。
func assertCommitUnknown(t *testing.T, ctx context.Context) {
	appDB := openIntegrationDB(t, "ihomeland", "IHOMELAND_TEST_MYSQL_PASSWORD_FILE")
	defer func() {
		if err := appDB.Close(); err != nil {
			t.Errorf("关闭 application database 失败：%v", err)
		}
	}()
	rootDB := openIntegrationDB(t, "root", "IHOMELAND_TEST_MYSQL_ROOT_PASSWORD_FILE")
	defer func() {
		if err := rootDB.Close(); err != nil {
			t.Errorf("关闭 root database 失败：%v", err)
		}
	}()
	err := WithinTx(ctx, appDB, nil, func(tx *sql.Tx) error {
		var connectionID uint64
		if err := tx.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&connectionID); err != nil {
			return err
		}
		_, killErr := rootDB.ExecContext(ctx, fmt.Sprintf("KILL CONNECTION %d", connectionID))
		return killErr
	})
	var transactionError *TransactionError
	if !errors.As(err, &transactionError) || transactionError.Outcome != TransactionCommitUnknown {
		t.Fatalf("interrupted commit error = %T %v", err, err)
	}
}

// openIntegrationDB 使用 harness file secret 构造真实 pool，不把 DSN 或 password 写入测试日志。
func openIntegrationDB(t *testing.T, username string, passwordFileEnvironment string) *sql.DB {
	t.Helper()
	password, err := os.ReadFile(os.Getenv(passwordFileEnvironment))
	if err != nil {
		t.Fatal(err)
	}
	driverConfig := mysqldriver.NewConfig()
	driverConfig.User = username
	driverConfig.Passwd = string(password)
	driverConfig.Net = "tcp"
	driverConfig.Addr = os.Getenv("IHOMELAND_TEST_MYSQL_ADDRESS")
	driverConfig.DBName = "ihomeland"
	driverConfig.Timeout = 3 * time.Second
	driverConfig.ParseTime = true
	driverConfig.Params = map[string]string{"time_zone": "'+00:00'", "sql_mode": "'STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION'"}
	connector, err := mysqldriver.NewConnector(driverConfig)
	if err != nil {
		t.Fatal(err)
	}
	return sql.OpenDB(connector)
}

// resolveIntegrationSecret 复用 production file provider 读取 harness 私有 secret。
func resolveIntegrationSecret(t *testing.T, path string) secret.Value {
	t.Helper()
	reference, err := secret.ParseReference("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	value, err := secret.NewEnvironmentFileProvider().Resolve(context.Background(), reference)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// restartContainer 注入 dependency restart，并保留固定 loopback host port。
func restartContainer(t *testing.T, name string) {
	t.Helper()
	command := exec.Command("docker", "restart", name)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("restart container: %v (%s)", err, output)
	}
}

// waitForPing 有界等待 driver pool 在 container restart 后重新建连。
func waitForPing(t *testing.T, db *sql.DB) {
	t.Helper()
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
	t.Fatal("MySQL did not recover after restart")
}

// requireIntegration 防止开发者绕过统一 harness 误连本机数据库。
func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("IHOMELAND_STORAGE_INTEGRATION") != "1" {
		t.Skip("storage integration harness is required")
	}
}
