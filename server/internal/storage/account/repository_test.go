package account

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	domain "github.com/jinwiforz/ihomeland/server/internal/account"
	storagemysql "github.com/jinwiforz/ihomeland/server/internal/storage/mysql"
)

// observerStub 丢弃低基数观测。
type observerStub struct{}

// RecordStorageOperation 满足 repository observer contract。
func (observerStub) RecordStorageOperation(string, string, string) {}

// scannerStub 为 hydration tests 注入完整 row。
type scannerStub struct {
	// values 按 SELECT 列顺序保存。
	values []any
}

// Scan 把稳定测试值复制到目标指针。
func (scanner scannerStub) Scan(destinations ...any) error {
	if len(destinations) != len(scanner.values) {
		return errors.New("scan count mismatch")
	}
	for index, value := range scanner.values {
		switch target := destinations[index].(type) {
		case *string:
			*target = value.(string)
		case *time.Time:
			*target = value.(time.Time)
		default:
			return errors.New("unsupported scan destination")
		}
	}
	return nil
}

// TestHydrateAuthenticationStrictBoundaries 验证 canonical文本、PHC和UTC时间严格恢复。
func TestHydrateAuthenticationStrictBoundaries(t *testing.T) {
	hasher, _, err := NewHasher(1)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := domain.NewRegisterPassword("StrongPassword1")
	hash, _ := hasher.Hash(context.Background(), password)
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	valid := []any{"acc_abc123", "ply_abc123", "player.one", "玩家 One", hash.Encoded(), "active", createdAt}
	if record, scanErr := scanAuthentication(scannerStub{values: valid}); scanErr != nil || !record.Account.Valid() || !record.Credential.Valid() {
		t.Fatalf("scan valid = %+v, %v", record, scanErr)
	}
	for name, mutate := range map[string]func([]any){
		"username":   func(values []any) { values[2] = "Player.One" },
		"display":    func(values []any) { values[3] = "  Player  " },
		"credential": func(values []any) { values[4] = "$argon2id$bad" },
		"status":     func(values []any) { values[5] = "unknown" },
		"timezone":   func(values []any) { values[6] = createdAt.In(time.FixedZone("bad", 3600)) },
	} {
		values := append([]any(nil), valid...)
		mutate(values)
		if _, scanErr := scanAuthentication(scannerStub{values: values}); scanErr == nil {
			t.Fatalf("%s corruption was accepted", name)
		}
	}
}

// TestRepositoryCommitUnknownAndSafeError 验证 commit boundary不会泄漏底层敏感文本。
func TestRepositoryCommitUnknownAndSafeError(t *testing.T) {
	repository := &Repository{db: &sql.DB{}, observer: observerStub{}}
	repository.withinTx = func(context.Context, *sql.DB, *sql.TxOptions, func(*sql.Tx) error) error {
		return &storagemysql.TransactionError{Outcome: storagemysql.TransactionCommitUnknown, Err: errors.New("secret sql username")}
	}
	record := validCreateRecord(t)
	outcome, err := repository.Create(context.Background(), record)
	if outcome != domain.CreateOutcomeCommitUnknown || err == nil {
		t.Fatalf("Create() = %v, %v", outcome, err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), record.Account.Username().String()) {
		t.Fatalf("safe error leaked cause: %v", err)
	}
}

// validCreateRecord 构造 repository unit tests 使用的完整记录。
func validCreateRecord(t *testing.T) domain.CreateRecord {
	t.Helper()
	accountID, _ := domain.NewAccountID("acc_test123")
	playerID, _ := domain.NewPlayerID("ply_test123")
	username, _ := domain.NewUsername("test.user")
	displayName, _ := domain.NewDisplayName("测试玩家")
	entity, _ := domain.NewAccount(accountID, playerID, username, displayName, domain.StatusActive, time.Now().UTC().Truncate(time.Microsecond))
	hasher, _, err := NewHasher(1)
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
