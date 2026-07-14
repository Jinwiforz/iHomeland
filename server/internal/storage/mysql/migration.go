package mysql

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"embed"
	"errors"
	"fmt"
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// createHistoryTableSQL 由 storage/mysql owner 创建唯一 migration metadata table，不包含业务 schema。
const createHistoryTableSQL = `CREATE TABLE IF NOT EXISTS ih_schema_migrations (
  version BIGINT UNSIGNED NOT NULL PRIMARY KEY COMMENT '迁移版本号',
  name VARCHAR(128) NOT NULL COMMENT '迁移名称',
  checksum BINARY(32) NOT NULL COMMENT '迁移校验值(SHA-256,32字节)',
  state ENUM('in_progress', 'applied') NOT NULL COMMENT '迁移状态',
  started_at TIMESTAMP(6) NOT NULL COMMENT '开始时间(UTC,微秒)',
  applied_at TIMESTAMP(6) NULL COMMENT '应用时间(UTC,微秒)',
  CONSTRAINT uq_ih_schema_migrations_name UNIQUE (name)
) ENGINE=InnoDB COMMENT='迁移历史(owner=storage/mysql)'`

// migrationNamePattern 固定六位连续版本和稳定小写名称。
var migrationNamePattern = regexp.MustCompile(`^(\d{6})_([a-z][a-z0-9_]*)\.sql$`)

// migrationFiles 是编译期固定 catalog，runtime 不从工作目录读取任意 SQL。
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migration 描述一个已嵌入、单 statement 且 checksum 固定的 forward migration。
type Migration struct {
	// Version 是从 1 开始连续递增的版本号。
	Version uint64
	// Name 是不含版本和后缀的稳定名称。
	Name string
	// Checksum 是 SQL 原始 bytes 的 SHA-256。
	Checksum [sha256.Size]byte
	// Statement 是经过 catalog 验证的唯一 SQL statement。
	Statement string
}

// MigrationResult 描述本次启动看到和新应用的 migration 数量。
type MigrationResult struct {
	// Applied 是本次启动新应用的 migration 数量。
	Applied int
	// CurrentVersion 是 catalog 应用完成后的最高版本，空 catalog 为 0。
	CurrentVersion uint64
}

// Catalog 加载并严格验证编译进二进制的 migration 文件。
func Catalog() ([]Migration, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, errors.New("read embedded migration catalog failed")
	}
	migrations := make([]Migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			return nil, errors.New("migration catalog must not contain directories")
		}
		matches := migrationNamePattern.FindStringSubmatch(entry.Name())
		if matches == nil {
			return nil, errors.New("migration catalog contains invalid filename")
		}
		version, _ := strconv.ParseUint(matches[1], 10, 64)
		content, readErr := migrationFiles.ReadFile(path.Join("migrations", entry.Name()))
		if readErr != nil {
			return nil, errors.New("read embedded migration failed")
		}
		statement := strings.TrimSpace(string(content))
		if strings.Count(statement, ";") != 1 || !strings.HasSuffix(statement, ";") {
			return nil, fmt.Errorf("migration %06d must contain exactly one terminated statement", version)
		}
		migrations = append(migrations, Migration{Version: version, Name: matches[2], Checksum: sha256.Sum256(content), Statement: statement})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	if err := validateCatalog(migrations); err != nil {
		return nil, err
	}
	return migrations, nil
}

// validateCatalog 保证排序后的版本严格从 1 连续递增，拒绝 gap 和 duplicate。
func validateCatalog(migrations []Migration) error {
	names := make(map[string]struct{}, len(migrations))
	for index, migration := range migrations {
		if migration.Version != uint64(index+1) {
			return errors.New("migration catalog contains a version gap or duplicate")
		}
		if _, exists := names[migration.Name]; exists {
			return errors.New("migration catalog contains a duplicate name")
		}
		names[migration.Name] = struct{}{}
	}
	return nil
}

// Migrate 在专用 connection 和 advisory lock 下校验 history 并只执行 forward migration。
//
// db、database 与 lockTimeout 必须来自已验证 component 配置。每个 migration 会先独立
// 提交 in_progress，再执行 DDL 并标记 applied；DDL、最终标记或 context 中断可能留下
// dirty history。Migrate 不自动重试、down 或猜测修复，后续启动会 fail closed，直到
// operator 审计实际 schema 并执行独立修复流程。
func Migrate(ctx context.Context, db *sql.DB, database string, lockTimeout time.Duration) (MigrationResult, error) {
	catalog, err := Catalog()
	if err != nil {
		return MigrationResult{}, err
	}
	connection, err := db.Conn(ctx)
	if err != nil {
		return MigrationResult{}, wrapDriverError("acquire mysql migration connection failed", err)
	}
	defer func() {
		// Advisory lock release 先执行；随后丢弃专用 connection，关闭错误不改变 migration outcome。
		_ = connection.Close()
	}()
	lockName := "ihomeland:migration:" + database
	var acquired sql.NullInt64
	// MySQL GET_LOCK 只接受整数秒；向上取整避免正的 sub-second 配置被静默变为 no-wait。
	lockSeconds := int(math.Ceil(lockTimeout.Seconds()))
	if err := connection.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", lockName, lockSeconds).Scan(&acquired); err != nil {
		return MigrationResult{}, wrapDriverError("acquire mysql migration lock failed", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		return MigrationResult{}, errors.New("acquire mysql migration lock failed")
	}
	defer func() {
		releaseContext, cancelRelease := context.WithTimeout(context.Background(), time.Second)
		defer cancelRelease()
		var released sql.NullInt64
		releaseErr := connection.QueryRowContext(releaseContext, "SELECT RELEASE_LOCK(?)", lockName).Scan(&released)
		if releaseErr != nil || !released.Valid || released.Int64 != 1 {
			// Advisory lock release 无法确认时必须丢弃物理连接，不能把带锁 session 放回共享 pool。
			_ = connection.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	if _, err := connection.ExecContext(ctx, createHistoryTableSQL); err != nil {
		return MigrationResult{}, wrapDriverError("create mysql migration history failed", err)
	}
	history, err := readHistory(ctx, connection)
	if err != nil {
		return MigrationResult{}, err
	}
	if err := validateHistory(catalog, history); err != nil {
		return MigrationResult{}, err
	}
	result := MigrationResult{}
	for _, migration := range catalog {
		result.CurrentVersion = migration.Version
		if _, exists := history[migration.Version]; exists {
			continue
		}
		if err := applyMigration(ctx, connection, migration); err != nil {
			return MigrationResult{}, err
		}
		result.Applied++
	}
	return result, nil
}

// historyEntry 是从 MySQL history table 读取且尚未与 embedded catalog 对照的行快照。
type historyEntry struct {
	// version 对应 catalog 中的唯一递增版本。
	version uint64
	// name 必须与同版本嵌入文件名保持不变。
	name string
	// checksum 保存 SQL 原始 bytes 的 SHA-256。
	checksum []byte
	// state 只允许 in_progress 或 applied；前者始终视为 dirty。
	state string
}

// readHistory 在 advisory lock connection 上读取完整有序快照，并拒绝重复版本。
func readHistory(ctx context.Context, connection *sql.Conn) (map[uint64]historyEntry, error) {
	rows, err := connection.QueryContext(ctx, "SELECT version, name, checksum, state FROM ih_schema_migrations ORDER BY version")
	if err != nil {
		return nil, wrapDriverError("read mysql migration history failed", err)
	}
	defer func() {
		// 只读结果集的迭代错误由 rows.Err 返回；额外关闭错误没有安全恢复动作。
		_ = rows.Close()
	}()
	result := make(map[uint64]historyEntry)
	for rows.Next() {
		var entry historyEntry
		if err := rows.Scan(&entry.version, &entry.name, &entry.checksum, &entry.state); err != nil {
			return nil, wrapDriverError("scan mysql migration history failed", err)
		}
		if _, exists := result[entry.version]; exists {
			return nil, errors.New("mysql migration history contains duplicate version")
		}
		result[entry.version] = entry
	}
	if err := rows.Err(); err != nil {
		return nil, wrapDriverError("iterate mysql migration history failed", err)
	}
	return result, nil
}

// validateHistory 对 dirty、unknown、checksum/name mismatch 与 version gap 全部 fail closed。
func validateHistory(catalog []Migration, history map[uint64]historyEntry) error {
	for version, entry := range history {
		if entry.state != "applied" {
			return fmt.Errorf("mysql migration %06d is dirty", version)
		}
		if version == 0 || version > uint64(len(catalog)) {
			return fmt.Errorf("mysql migration history version %06d is not in catalog", version)
		}
		migration := catalog[version-1]
		if entry.name != migration.Name || !equalChecksum(entry.checksum, migration.Checksum[:]) {
			return fmt.Errorf("mysql migration %06d name or checksum mismatch", version)
		}
	}
	for version := uint64(1); version <= uint64(len(history)); version++ {
		if _, exists := history[version]; !exists {
			return errors.New("mysql migration history contains a version gap")
		}
	}
	return nil
}

// applyMigration 先提交 in_progress，再执行单 statement，最后确认 applied；中断不会被猜测修复。
func applyMigration(ctx context.Context, connection *sql.Conn, migration Migration) error {
	tx, err := connection.BeginTx(ctx, nil)
	if err != nil {
		return wrapDriverError(fmt.Sprintf("prepare mysql migration %06d failed", migration.Version), err)
	}
	_, insertErr := tx.ExecContext(ctx, "INSERT INTO ih_schema_migrations (version, name, checksum, state, started_at) VALUES (?, ?, ?, 'in_progress', UTC_TIMESTAMP(6))", migration.Version, migration.Name, migration.Checksum[:])
	if insertErr != nil {
		rollbackErr := tx.Rollback()
		var rollbackFailure error
		if rollbackErr != nil {
			rollbackFailure = wrapDriverError(fmt.Sprintf("rollback mysql migration %06d marker failed", migration.Version), rollbackErr)
		}
		return errors.Join(wrapDriverError(fmt.Sprintf("record mysql migration %06d in progress failed", migration.Version), insertErr), rollbackFailure)
	}
	if err := tx.Commit(); err != nil {
		return wrapDriverError(fmt.Sprintf("commit mysql migration %06d in-progress marker is unknown", migration.Version), err)
	}
	if _, err := connection.ExecContext(ctx, migration.Statement); err != nil {
		return wrapDriverError(fmt.Sprintf("execute mysql migration %06d failed and left dirty history", migration.Version), err)
	}
	result, err := connection.ExecContext(ctx, "UPDATE ih_schema_migrations SET state = 'applied', applied_at = UTC_TIMESTAMP(6) WHERE version = ? AND state = 'in_progress'", migration.Version)
	if err != nil {
		return wrapDriverError(fmt.Sprintf("mark mysql migration %06d applied failed and left dirty history", migration.Version), err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return wrapDriverError(fmt.Sprintf("read mysql migration %06d affected rows failed", migration.Version), err)
	}
	if affected != 1 {
		return fmt.Errorf("mark mysql migration %06d applied affected unexpected rows", migration.Version)
	}
	return nil
}

// equalChecksum 以固定循环比较两个 SHA-256，避免错误路径提前暴露匹配前缀。
func equalChecksum(left []byte, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}
