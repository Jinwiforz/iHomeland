package mysql

import (
	"encoding/hex"
	"errors"
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// TestCatalogIsContinuousAndSingleStatement 固定 embedded catalog 的版本、名称与 gap/duplicate 拒绝。
func TestCatalogIsContinuousAndSingleStatement(t *testing.T) {
	t.Parallel()

	catalog, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	expectedNames := []string{"initialize_schema_history", "create_personal_worlds", "create_personal_world_idempotency", "create_placement_sequences", "create_placement_allocations"}
	if len(catalog) != len(expectedNames) {
		t.Fatalf("catalog = %+v", catalog)
	}
	for index, expectedName := range expectedNames {
		if catalog[index].Version != uint64(index+1) || catalog[index].Name != expectedName {
			t.Fatalf("catalog[%d] = %+v", index, catalog[index])
		}
	}
	// 第一条已归档 migration 的固定 checksum 防止业务 schema change 顺手改写历史。
	if got := hex.EncodeToString(catalog[0].Checksum[:]); got != "07cbb33e3dc84249acd0be9b34372bfa61c44576960a18f72ee095b5602a2165" {
		t.Fatalf("archived migration checksum = %s", got)
	}
	if err := validateCatalog([]Migration{{Version: 1}, {Version: 3}}); err == nil {
		t.Fatal("version gap 应被拒绝")
	}
	if err := validateCatalog([]Migration{{Version: 1}, {Version: 1}}); err == nil {
		t.Fatal("duplicate version 应被拒绝")
	}
	if err := validateCatalog([]Migration{{Version: 1, Name: "same"}, {Version: 2, Name: "same"}}); err == nil {
		t.Fatal("duplicate name 应被拒绝")
	}
}

// TestValidateHistoryFailsClosed 覆盖 dirty、unknown 与 gap history 的纯内存拒绝路径。
func TestValidateHistoryFailsClosed(t *testing.T) {
	t.Parallel()

	catalog, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	migration := catalog[0]
	valid := historyEntry{version: 1, name: migration.Name, checksum: migration.Checksum[:], state: "applied"}
	if err := validateHistory(catalog, map[uint64]historyEntry{1: valid}); err != nil {
		t.Fatal(err)
	}
	for name, history := range map[string]map[uint64]historyEntry{
		"dirty":   {1: {version: 1, name: migration.Name, checksum: migration.Checksum[:], state: "in_progress"}},
		"unknown": {2: {version: 2, name: "unknown", checksum: migration.Checksum[:], state: "applied"}},
		"gap":     {1: valid, 3: {version: 3, name: "gap", checksum: migration.Checksum[:], state: "applied"}},
	} {
		if err := validateHistory(catalog, history); err == nil {
			t.Fatalf("%s history 应被拒绝", name)
		}
	}
}

// TestClassifyPreCommit 防止普通 network error 被错误提升为可重试事务。
func TestClassifyPreCommit(t *testing.T) {
	t.Parallel()

	if got := classifyPreCommit(&mysqldriver.MySQLError{Number: 1213, Message: "deadlock"}); got != TransactionPreCommitTransient {
		t.Fatalf("deadlock outcome = %s", got)
	}
	if got := classifyPreCommit(errors.New("network")); got != TransactionNotCommitted {
		t.Fatalf("network outcome = %s", got)
	}
}
