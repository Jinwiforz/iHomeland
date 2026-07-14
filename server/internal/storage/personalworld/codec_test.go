package personalworld

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	domain "github.com/jinwiforz/ihomeland/server/internal/personalworld"
	storagemysql "github.com/jinwiforz/ihomeland/server/internal/storage/mysql"
)

// discardObserver 满足 unit repository 的低基数观测边界。
type discardObserver struct{}

// RecordStorageOperation 丢弃固定测试观测。
func (discardObserver) RecordStorageOperation(string, string, string) {}

// TestHydrateWorldStrictlyValidatesPersistentRow 保护 ID、owner、enum、revision 与 UTC 的封闭 hydration。
func TestHydrateWorldStrictlyValidatesPersistentRow(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 7, 14, 1, 2, 3, 456000000, time.UTC)
	snapshot, err := hydrateWorld("pworld_codec1", "ply_codec1", "active", 1, createdAt)
	if err != nil || !snapshot.Valid() || snapshot.Lifecycle() != domain.LifecycleActive || !snapshot.CreatedAt().Equal(createdAt) {
		t.Fatalf("hydrate valid row = %v, %v", snapshot.Valid(), err)
	}
	for name, testCase := range map[string]struct {
		// world 是待 hydration 的 PersonalWorldID 原始列值。
		world string
		// owner 是待 hydration 的 owner PlayerID 原始列值。
		owner string
		// lifecycle 是待 hydration 的持久化生命周期枚举值。
		lifecycle string
		// revision 是待 hydration 的无符号乐观并发版本。
		revision uint64
		// created 是待 hydration 的 DATETIME(6) UTC 时间值。
		created time.Time
	}{
		"world namespace":   {"wrong", "ply_codec1", "active", 1, createdAt},
		"owner namespace":   {"pworld_codec1", "wrong", "active", 1, createdAt},
		"unknown lifecycle": {"pworld_codec1", "ply_codec1", "sleeping", 1, createdAt},
		"zero revision":     {"pworld_codec1", "ply_codec1", "active", 0, createdAt},
		"non utc":           {"pworld_codec1", "ply_codec1", "active", 1, createdAt.In(time.FixedZone("offset", 3600))},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if value, hydrateErr := hydrateWorld(testCase.world, testCase.owner, testCase.lifecycle, testCase.revision, testCase.created); hydrateErr == nil || value.Valid() {
				t.Fatalf("malformed row = %v, %v", value.Valid(), hydrateErr)
			}
		})
	}
}

// TestAdapterErrorDoesNotExposeCause 防止 SQL、参数与 identity 通过默认错误格式化泄漏。
func TestAdapterErrorDoesNotExposeCause(t *testing.T) {
	t.Parallel()
	cause := errors.New("SELECT secret FROM personal_worlds WHERE owner='ply_sensitive'")
	failure := (&adapterError{operation: "find_by_id", outcome: "failed", cause: cause}).Error()
	for _, forbidden := range []string{"SELECT", "secret", "ply_sensitive"} {
		if strings.Contains(failure, forbidden) {
			t.Fatalf("adapter error leaked %q: %s", forbidden, failure)
		}
	}
}

// TestEnsurePrimaryMapsCommitUnknown 验证 MySQL commit 不确定时不会返回 candidate 或伪装未提交。
func TestEnsurePrimaryMapsCommitUnknown(t *testing.T) {
	t.Parallel()
	repository, err := New(&sql.DB{}, discardObserver{})
	if err != nil {
		t.Fatal(err)
	}
	repository.withinTx = func(context.Context, *sql.DB, *sql.TxOptions, func(*sql.Tx) error) error {
		return &storagemysql.TransactionError{Outcome: storagemysql.TransactionCommitUnknown, Err: errors.New("injected")}
	}
	worldID, _ := domain.NewPersonalWorldID("pworld_unknown1")
	ownerID, _ := account.NewPlayerID("ply_unknown1")
	candidate, _ := domain.NewSnapshot(worldID, ownerID, domain.LifecycleActive, domain.InitialRevision, time.Now().UTC())
	result, outcome, commitErr := repository.EnsurePrimary(context.Background(), candidate)
	if commitErr == nil || outcome != domain.EnsureOutcomeCommitUnknown || result.Valid() {
		t.Fatalf("commit unknown = %v, %v", outcome, commitErr)
	}
}
