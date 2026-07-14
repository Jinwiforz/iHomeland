package personalworld

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	domain "github.com/jinwiforz/ihomeland/server/internal/personalworld"
)

// rowScanner 是 *sql.Row 与测试 scanner 共享的最窄 hydration 边界。
type rowScanner interface {
	// Scan 按 query 声明顺序复制完整 row；任何缺失或类型错误必须返回失败。
	Scan(...any) error
}

// worldQueryer 是 *sql.DB 与 *sql.Tx 共享的单行读取边界。
type worldQueryer interface {
	// QueryRowContext 执行固定 repository query；调用者必须立即 Scan，不能保存返回 row。
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// scanWorld 用领域 constructors 恢复完整 PersonalWorld snapshot。
func scanWorld(scanner rowScanner) (domain.Snapshot, error) {
	var worldID string
	var ownerID string
	var lifecycle string
	var revision uint64
	var createdAt time.Time
	if err := scanner.Scan(&worldID, &ownerID, &lifecycle, &revision, &createdAt); err != nil {
		return domain.Snapshot{}, err
	}
	return hydrateWorld(worldID, ownerID, lifecycle, revision, createdAt)
}

// hydrateWorld 拒绝 unknown enum、非法 identity、零 revision 与非 UTC/零时间。
func hydrateWorld(worldValue string, ownerValue string, lifecycleValue string, revisionValue uint64, createdAt time.Time) (domain.Snapshot, error) {
	if createdAt.IsZero() {
		return domain.Snapshot{}, errors.New("persisted world creation time is invalid")
	}
	_, offset := createdAt.Zone()
	if offset != 0 {
		return domain.Snapshot{}, errors.New("persisted world creation time is not UTC")
	}
	worldID, err := domain.NewPersonalWorldID(worldValue)
	if err != nil {
		return domain.Snapshot{}, err
	}
	ownerID, err := account.NewPlayerID(ownerValue)
	if err != nil {
		return domain.Snapshot{}, err
	}
	lifecycle, err := parseLifecycle(lifecycleValue)
	if err != nil {
		return domain.Snapshot{}, err
	}
	revision, err := domain.NewRevision(revisionValue)
	if err != nil {
		return domain.Snapshot{}, err
	}
	return domain.NewSnapshot(worldID, ownerID, lifecycle, revision, createdAt)
}

// parseLifecycle 把持久 enum 映射到封闭领域值，unknown 值不提供 fallback。
func parseLifecycle(value string) (domain.Lifecycle, error) {
	switch value {
	case "active":
		return domain.LifecycleActive, nil
	case "archived":
		return domain.LifecycleArchived, nil
	default:
		return domain.LifecycleUnspecified, errors.New("persisted world lifecycle is unknown")
	}
}

// selectWorldByID 从 queryer 读取目标世界的完整持久投影。
func selectWorldByID(ctx context.Context, queryer worldQueryer, id domain.PersonalWorldID) (domain.Snapshot, error) {
	return scanWorld(queryer.QueryRowContext(ctx, `SELECT personal_world_id, owner_player_id, lifecycle, revision, created_at
		FROM personal_worlds WHERE personal_world_id = ?`, id.String()))
}

// selectWorldByIDForUpdate 锁定 archive compare-and-commit 使用的唯一世界 row。
func selectWorldByIDForUpdate(ctx context.Context, tx *sql.Tx, id domain.PersonalWorldID) (domain.Snapshot, error) {
	return scanWorld(tx.QueryRowContext(ctx, `SELECT personal_world_id, owner_player_id, lifecycle, revision, created_at
		FROM personal_worlds WHERE personal_world_id = ? FOR UPDATE`, id.String()))
}

// selectWorldByOwner 只用于 unique insert 冲突后的已提交事实解析。
func selectWorldByOwner(ctx context.Context, tx *sql.Tx, owner account.PlayerID) (domain.Snapshot, error) {
	return scanWorld(tx.QueryRowContext(ctx, `SELECT personal_world_id, owner_player_id, lifecycle, revision, created_at
		FROM personal_worlds WHERE owner_player_id = ? FOR UPDATE`, owner.String()))
}

// selectReplay 恢复 actor-scoped 首次结果；lock 只用于 world row 已锁定后的 race recheck。
func selectReplay(ctx context.Context, tx *sql.Tx, actor account.PlayerID, keyDigest [sha256.Size]byte, lock bool) (domain.Snapshot, domain.CommandFingerprint, error) {
	var operation string
	var fingerprintBytes []byte
	var worldID string
	var ownerID string
	var lifecycle string
	var revision uint64
	var createdAt time.Time
	query := `SELECT operation, command_fingerprint, result_world_id,
		result_owner_player_id, result_lifecycle, result_revision, result_created_at
		FROM personal_world_idempotency
		WHERE actor_player_id = ? AND idempotency_key_digest = ?`
	if lock {
		query += ` FOR UPDATE`
	}
	err := tx.QueryRowContext(ctx, query, actor.String(), keyDigest[:]).Scan(
		&operation, &fingerprintBytes, &worldID, &ownerID, &lifecycle, &revision, &createdAt,
	)
	if err != nil {
		return domain.Snapshot{}, domain.CommandFingerprint{}, err
	}
	if operation != archiveOperation || len(fingerprintBytes) != sha256.Size {
		return domain.Snapshot{}, domain.CommandFingerprint{}, errors.New("persisted idempotency row is malformed")
	}
	var digest [sha256.Size]byte
	copy(digest[:], fingerprintBytes)
	fingerprint, err := domain.NewCommandFingerprint(digest)
	if err != nil {
		return domain.Snapshot{}, domain.CommandFingerprint{}, err
	}
	snapshot, err := hydrateWorld(worldID, ownerID, lifecycle, revision, createdAt)
	if err != nil {
		return domain.Snapshot{}, domain.CommandFingerprint{}, err
	}
	return snapshot, fingerprint, nil
}
