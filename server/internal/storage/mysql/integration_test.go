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
	"unicode"

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
	resetIntegrationSchema(t, ctx, db)
	result, err := Migrate(ctx, db, "ihomeland", 10*time.Second)
	if err != nil || result.Applied != 5 {
		t.Fatalf("empty migration = %+v, %v", result, err)
	}
	assertBusinessSchema(t, ctx, db)
	result, err = Migrate(ctx, db, "ihomeland", 10*time.Second)
	if err != nil || result.Applied != 0 {
		t.Fatalf("repeat migration = %+v, %v", result, err)
	}
	resetIntegrationSchema(t, ctx, db)
	seedD0MigrationHistory(t, ctx, db)
	result, err = Migrate(ctx, db, "ihomeland", 10*time.Second)
	if err != nil || result.Applied != 4 || result.CurrentVersion != 5 {
		t.Fatalf("D0 history upgrade = %+v, %v", result, err)
	}
	assertBusinessSchema(t, ctx, db)
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

	resetIntegrationSchema(t, ctx, db)
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
	if totalApplied != 5 {
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
	resetIntegrationSchema(t, ctx, db)

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

// assertBusinessSchema 验证受管 table/column 的中文短注释、owner、单位、约束与 unsigned 类型。
func assertBusinessSchema(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for table, expectedComment := range map[string]string{
		"ih_schema_migrations":       "迁移历史(owner=storage/mysql)",
		"personal_worlds":            "个人世界(owner=storage/personalworld)",
		"personal_world_idempotency": "个人世界幂等结果(owner=storage/personalworld)",
		"placement_sequences":        "实例放置序列(owner=storage/placement)",
		"placement_allocations":      "实例放置分配(owner=storage/placement)",
	} {
		var comment string
		if err := db.QueryRowContext(ctx, `SELECT table_comment FROM information_schema.tables
			WHERE table_schema = DATABASE() AND table_name = ?`, table).Scan(&comment); err != nil || comment != expectedComment || !containsHan(comment) {
			t.Fatalf("table %s comment = %q, want %q, %v", table, comment, expectedComment, err)
		}
	}
	columnComments := map[string]string{
		"ih_schema_migrations.version":                      "迁移版本号",
		"ih_schema_migrations.name":                         "迁移名称",
		"ih_schema_migrations.checksum":                     "迁移校验值(SHA-256,32字节)",
		"ih_schema_migrations.state":                        "迁移状态",
		"ih_schema_migrations.started_at":                   "开始时间(UTC,微秒)",
		"ih_schema_migrations.applied_at":                   "应用时间(UTC,微秒)",
		"personal_worlds.personal_world_id":                 "个人世界ID",
		"personal_worlds.owner_player_id":                   "所属玩家ID",
		"personal_worlds.lifecycle":                         "生命周期",
		"personal_worlds.revision":                          "修订号",
		"personal_worlds.created_at":                        "创建时间(UTC,微秒)",
		"personal_world_idempotency.actor_player_id":        "操作玩家ID",
		"personal_world_idempotency.idempotency_key_digest": "幂等键摘要(SHA-256,32字节)",
		"personal_world_idempotency.operation":              "操作类型",
		"personal_world_idempotency.command_fingerprint":    "命令指纹(SHA-256,32字节)",
		"personal_world_idempotency.result_world_id":        "结果个人世界ID",
		"personal_world_idempotency.result_owner_player_id": "结果所属玩家ID",
		"personal_world_idempotency.result_lifecycle":       "结果生命周期",
		"personal_world_idempotency.result_revision":        "结果修订号",
		"personal_world_idempotency.result_created_at":      "结果创建时间(UTC,微秒)",
		"personal_world_idempotency.committed_at":           "提交时间(UTC,微秒)",
		"placement_sequences.personal_world_id":             "个人世界ID",
		"placement_sequences.generation_high":               "实例代际高水位",
		"placement_sequences.fencing_high":                  "围栏令牌高水位",
		"placement_sequences.updated_at":                    "更新时间(UTC,微秒)",
		"placement_allocations.world_instance_id":           "世界实例ID",
		"placement_allocations.personal_world_id":           "个人世界ID",
		"placement_allocations.runtime_node_id":             "运行节点ID",
		"placement_allocations.generation":                  "实例代际",
		"placement_allocations.fencing_token":               "围栏令牌",
		"placement_allocations.candidate_created_at":        "候选创建时间(UTC,微秒)",
		"placement_allocations.candidate_lease_expires_at":  "候选租约到期时间(UTC,微秒)",
		"placement_allocations.candidate_checksum":          "候选身份校验值(SHA-256,32字节)",
		"placement_allocations.allocated_at":                "分配时间(UTC,微秒)",
	}
	rows, err := db.QueryContext(ctx, `SELECT table_name, column_name, data_type, column_comment
		FROM information_schema.columns WHERE table_schema = DATABASE()`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seenColumns := make(map[string]struct{}, len(columnComments))
	for rows.Next() {
		var table string
		var column string
		var dataType string
		var comment string
		if err := rows.Scan(&table, &column, &dataType, &comment); err != nil {
			t.Fatal(err)
		}
		key := table + "." + column
		expectedComment, managed := columnComments[key]
		if !managed {
			t.Fatalf("managed schema contains undocumented column %s", key)
		}
		if comment != expectedComment || !containsHan(comment) {
			t.Fatalf("column %s comment = %q, want %q", key, comment, expectedComment)
		}
		if (dataType == "datetime" || dataType == "timestamp") && (!strings.Contains(comment, "UTC") || !strings.Contains(comment, "微秒")) {
			t.Fatalf("temporal column %s comment lacks timezone/precision: %q", key, comment)
		}
		seenColumns[key] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seenColumns) != len(columnComments) {
		t.Fatalf("documented columns = %d, observed = %d", len(columnComments), len(seenColumns))
	}
	for _, constraint := range []string{
		"uq_personal_worlds_owner", "chk_personal_worlds_revision", "chk_personal_world_idempotency_revision",
		"fk_placement_sequences_world", "chk_placement_sequences_generation", "chk_placement_sequences_fence",
		"uq_placement_allocations_generation", "uq_placement_allocations_fence", "fk_placement_allocations_world",
		"chk_placement_allocations_generation", "chk_placement_allocations_fence", "chk_placement_allocations_lease",
	} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.table_constraints
			WHERE constraint_schema = DATABASE() AND constraint_name = ?`, constraint).Scan(&count); err != nil || count != 1 {
			t.Fatalf("constraint %s count = %d, %v", constraint, count, err)
		}
	}
	for table, column := range map[string]string{
		"personal_worlds": "revision", "placement_sequences": "generation_high", "placement_allocations": "fencing_token",
	} {
		var columnType string
		if err := db.QueryRowContext(ctx, `SELECT column_type FROM information_schema.columns
			WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?`, table, column).Scan(&columnType); err != nil || !strings.Contains(columnType, "unsigned") {
			t.Fatalf("column %s.%s type = %q, %v", table, column, columnType, err)
		}
	}
}

// containsHan 报告 schema comment 是否至少包含一个中文汉字。
func containsHan(value string) bool {
	for _, character := range value {
		if unicode.Is(unicode.Han, character) {
			return true
		}
	}
	return false
}

// resetIntegrationSchema 按外键反序删除隔离 harness 中的全部 migration-owned table。
//
// 该 helper 只供 migration recovery 测试模拟空库；生产 migrator 永不执行 down 或删除数据。
func resetIntegrationSchema(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"placement_allocations", "placement_sequences", "personal_world_idempotency", "personal_worlds", "ih_schema_migrations"} {
		if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatalf("reset integration table failed: %v", err)
		}
	}
}

// seedD0MigrationHistory 模拟共享基线仅应用 000001 的真实 forward upgrade 起点。
func seedD0MigrationHistory(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	catalog, err := Catalog()
	if err != nil || len(catalog) < 1 {
		t.Fatalf("load D0 migration: %v", err)
	}
	if _, err := db.ExecContext(ctx, createHistoryTableSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, catalog[0].Statement); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO ih_schema_migrations
		(version, name, checksum, state, started_at, applied_at)
		VALUES (?, ?, ?, 'applied', UTC_TIMESTAMP(6), UTC_TIMESTAMP(6))`,
		catalog[0].Version, catalog[0].Name, catalog[0].Checksum[:]); err != nil {
		t.Fatal(err)
	}
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
