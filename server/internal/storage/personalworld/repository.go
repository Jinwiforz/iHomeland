package personalworld

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"

	mysqldriver "github.com/go-sql-driver/mysql"
	domain "github.com/jinwiforz/ihomeland/server/internal/personalworld"
	storagemysql "github.com/jinwiforz/ihomeland/server/internal/storage/mysql"
)

// archiveOperation 是持久化幂等记录的稳定操作标识，修改它会破坏历史 replay 解析。
const archiveOperation = "archive"

// Observer 接收 repository 的固定 operation/outcome，不接收 SQL、参数或 identity。
type Observer interface {
	// RecordStorageOperation 记录 adapter、operation 与 outcome 三个低基数字段。
	RecordStorageOperation(string, string, string)
}

// Repository 借用共享 MySQL pool 实现 PersonalWorldRepository。
//
// Repository 没有后台任务和可关闭资源，允许并发调用；共享 pool 的停止必须晚于所有调用结束。
type Repository struct {
	// db 由 storage/mysql Component 持有，本 adapter 只在调用期间借用。
	db *sql.DB
	// observer 只接收固定低基数结果。
	observer Observer
	// withinTx 固定 production transaction runner；测试可注入 commit boundary failure，不重放 callback。
	withinTx func(context.Context, *sql.DB, *sql.TxOptions, func(*sql.Tx) error) error
}

var _ domain.PersonalWorldRepository = (*Repository)(nil)

// New 创建不获取资源的 PersonalWorld MySQL adapter。
//
// db 和 observer 必须来自 Composition Root；Repository 不接管它们的关闭责任。
func New(db *sql.DB, observer Observer) (*Repository, error) {
	if db == nil || observer == nil {
		return nil, errors.New("personal world repository requires database and observer")
	}
	return &Repository{db: db, observer: observer, withinTx: storagemysql.WithinTx}, nil
}

// EnsurePrimary 以 owner unique constraint 线性化 primary world 创建。
//
// Duplicate 只在 insert 被 MySQL 明确拒绝后解析；candidate ID 若属于其他 owner 会作为
// dependency defect fail closed。Commit error 始终保持 commit-unknown，不自动生成新 ID。
func (repository *Repository) EnsurePrimary(ctx context.Context, candidate domain.Snapshot) (domain.Snapshot, domain.EnsureOutcome, error) {
	if !candidate.Valid() {
		return domain.Snapshot{}, domain.EnsureOutcomeNotCommitted, repository.failure("ensure_primary", "invalid", errors.New("candidate is invalid"))
	}
	resolved := domain.Snapshot{}
	outcome := domain.EnsureOutcomeUnspecified
	err := repository.withinTx(ctx, repository.db, nil, func(tx *sql.Tx) error {
		_, insertErr := tx.ExecContext(ctx, `INSERT INTO personal_worlds
			(personal_world_id, owner_player_id, lifecycle, revision, created_at)
			VALUES (?, ?, ?, ?, ?)`, candidate.ID().String(), candidate.OwnerID().String(), candidate.Lifecycle().String(), candidate.Revision().Uint64(), candidate.CreatedAt())
		if insertErr == nil {
			resolved = candidate
			outcome = domain.EnsureOutcomeCreated
			return nil
		}
		if !isDuplicate(insertErr) {
			return insertErr
		}
		existing, findErr := selectWorldByOwner(ctx, tx, candidate.OwnerID())
		if findErr != nil {
			if errors.Is(findErr, sql.ErrNoRows) {
				return errors.New("candidate identity collides with another owner")
			}
			return findErr
		}
		if existing.ID() != candidate.ID() {
			collision, collisionErr := selectWorldByID(ctx, tx, candidate.ID())
			if collisionErr == nil && collision.OwnerID() != candidate.OwnerID() {
				return errors.New("candidate identity collides with another owner")
			}
			if collisionErr != nil && !errors.Is(collisionErr, sql.ErrNoRows) {
				return collisionErr
			}
		}
		resolved = existing
		outcome = domain.EnsureOutcomeExisting
		return nil
	})
	if err != nil {
		if transactionOutcome(err) == storagemysql.TransactionCommitUnknown {
			return domain.Snapshot{}, domain.EnsureOutcomeCommitUnknown, repository.failure("ensure_primary", "commit_unknown", err)
		}
		return domain.Snapshot{}, domain.EnsureOutcomeNotCommitted, repository.failure("ensure_primary", "not_committed", err)
	}
	if !resolved.Valid() || outcome != domain.EnsureOutcomeCreated && outcome != domain.EnsureOutcomeExisting {
		return domain.Snapshot{}, domain.EnsureOutcomeNotCommitted, repository.failure("ensure_primary", "defect", errors.New("repository result is inconsistent"))
	}
	repository.observe("ensure_primary", outcomeName(outcome))
	return resolved, outcome, nil
}

// FindByID 读取单一完整 snapshot，并严格区分不存在与依赖/数据缺陷。
func (repository *Repository) FindByID(ctx context.Context, id domain.PersonalWorldID) (domain.Snapshot, domain.FindOutcome, error) {
	if !id.Valid() {
		return domain.Snapshot{}, domain.FindOutcomeUnspecified, repository.failure("find_by_id", "invalid", errors.New("world ID is invalid"))
	}
	snapshot, err := selectWorldByID(ctx, repository.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		repository.observe("find_by_id", "not_found")
		return domain.Snapshot{}, domain.FindOutcomeNotFound, nil
	}
	if err != nil {
		return domain.Snapshot{}, domain.FindOutcomeUnspecified, repository.failure("find_by_id", "failed", err)
	}
	repository.observe("find_by_id", "found")
	return snapshot, domain.FindOutcomeFound, nil
}

// CommitArchive 在单一 transaction 中决议 actor-scoped replay、锁定 world 并提交 revision。
//
// Transaction 先只读解析已提交 replay；miss 后统一先锁 world，再二次锁定幂等 key，避免
// 不同 key 各持 gap lock 后争同一 world 的死锁。同 key race 在二次读取处收敛。Callback
// 只执行一次；Commit error 不触发补偿删除，调用方必须使用相同 identity 解析。
func (repository *Repository) CommitArchive(ctx context.Context, record domain.ArchiveRecord) (domain.MutationResult, domain.MutationOutcome, error) {
	if !record.Valid() {
		return domain.MutationResult{}, domain.MutationOutcomeNotCommitted, repository.failure("archive", "invalid", errors.New("archive record is invalid"))
	}
	result := domain.MutationResult{}
	outcome := domain.MutationOutcomeUnspecified
	err := repository.withinTx(ctx, repository.db, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(tx *sql.Tx) error {
		keyDigest := digestKey(record.Command().IdempotencyKey())
		replay, replayFingerprint, replayErr := selectReplay(ctx, tx, record.Command().ActorID(), keyDigest, false)
		if replayErr == nil {
			var decisionErr error
			result, outcome, decisionErr = decideReplay(record, replay, replayFingerprint)
			return decisionErr
		}
		if !errors.Is(replayErr, sql.ErrNoRows) {
			return replayErr
		}

		current, findErr := selectWorldByIDForUpdate(ctx, tx, record.Command().WorldID())
		if errors.Is(findErr, sql.ErrNoRows) {
			outcome = domain.MutationOutcomeNotFound
			return nil
		}
		if findErr != nil {
			return findErr
		}
		// 所有 mutation 已先取得 world row lock；二次 key lock 因此不会与另一 world contender 形成反向等待。
		replay, replayFingerprint, replayErr = selectReplay(ctx, tx, record.Command().ActorID(), keyDigest, true)
		if replayErr == nil {
			var decisionErr error
			result, outcome, decisionErr = decideReplay(record, replay, replayFingerprint)
			return decisionErr
		}
		if !errors.Is(replayErr, sql.ErrNoRows) {
			return replayErr
		}
		if current.OwnerID() != record.Command().ActorID() {
			return errors.New("persisted owner contradicts authorized command")
		}
		if current.Revision() != record.Command().ExpectedRevision() {
			outcome = domain.MutationOutcomeRevisionConflict
			return nil
		}
		if current.Lifecycle() != domain.LifecycleActive {
			outcome = domain.MutationOutcomeInvalidState
			return nil
		}
		update, updateErr := tx.ExecContext(ctx, `UPDATE personal_worlds
			SET lifecycle = ?, revision = ?
			WHERE personal_world_id = ? AND owner_player_id = ? AND lifecycle = ? AND revision = ?`,
			record.Target().Lifecycle().String(), record.Target().Revision().Uint64(), record.Target().ID().String(),
			record.Target().OwnerID().String(), current.Lifecycle().String(), current.Revision().Uint64())
		if updateErr != nil {
			return updateErr
		}
		affected, affectedErr := update.RowsAffected()
		if affectedErr != nil || affected != 1 {
			return errors.New("archive update affected unexpected rows")
		}
		fingerprint := record.Fingerprint().Digest()
		_, insertErr := tx.ExecContext(ctx, `INSERT INTO personal_world_idempotency
			(actor_player_id, idempotency_key_digest, operation, command_fingerprint,
			 result_world_id, result_owner_player_id, result_lifecycle, result_revision,
			 result_created_at, committed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(6))`,
			record.Command().ActorID().String(), keyDigest[:], archiveOperation, fingerprint[:],
			record.Target().ID().String(), record.Target().OwnerID().String(), record.Target().Lifecycle().String(),
			record.Target().Revision().Uint64(), record.Target().CreatedAt())
		if insertErr != nil {
			return insertErr
		}
		var resultErr error
		result, resultErr = domain.NewMutationResult(record.Target(), record.Fingerprint())
		if resultErr != nil {
			return resultErr
		}
		outcome = domain.MutationOutcomeApplied
		return nil
	})
	if err != nil {
		if transactionOutcome(err) == storagemysql.TransactionCommitUnknown {
			return domain.MutationResult{}, domain.MutationOutcomeCommitUnknown, repository.failure("archive", "commit_unknown", err)
		}
		return domain.MutationResult{}, domain.MutationOutcomeNotCommitted, repository.failure("archive", "not_committed", err)
	}
	if outcome == domain.MutationOutcomeApplied || outcome == domain.MutationOutcomeReplay {
		if !result.Valid() {
			return domain.MutationResult{}, domain.MutationOutcomeNotCommitted, repository.failure("archive", "defect", errors.New("mutation result is incomplete"))
		}
	} else if result.Valid() {
		return domain.MutationResult{}, domain.MutationOutcomeNotCommitted, repository.failure("archive", "defect", errors.New("non-success result is not empty"))
	}
	repository.observe("archive", mutationOutcomeName(outcome))
	return result, outcome, nil
}

// decideReplay 验证首次结果与当前 record 的 fingerprint/target 完全一致。
func decideReplay(record domain.ArchiveRecord, replay domain.Snapshot, fingerprint domain.CommandFingerprint) (domain.MutationResult, domain.MutationOutcome, error) {
	if !fingerprint.Equal(record.Fingerprint()) {
		return domain.MutationResult{}, domain.MutationOutcomeIdempotencyConflict, nil
	}
	if !replay.Equal(record.Target()) {
		return domain.MutationResult{}, domain.MutationOutcomeNotCommitted, errors.New("idempotency replay result contradicts command")
	}
	result, err := domain.NewMutationResult(replay, fingerprint)
	if err != nil {
		return domain.MutationResult{}, domain.MutationOutcomeNotCommitted, err
	}
	return result, domain.MutationOutcomeReplay, nil
}

// adapterError 保留受控 cause，但默认文本只包含固定 operation/outcome。
type adapterError struct {
	// operation 是固定 repository 操作名。
	operation string
	// outcome 是固定低基数失败类别。
	outcome string
	// cause 只供受控 errors.Is/As 使用。
	cause error
}

// Error 返回不会展开 SQL、参数、driver 文本或 identity 的稳定错误。
func (failure *adapterError) Error() string {
	return fmt.Sprintf("personal world storage %s failed (%s)", failure.operation, failure.outcome)
}

// Unwrap 返回受控内部诊断使用的底层 cause。
func (failure *adapterError) Unwrap() error { return failure.cause }

// failure 构造安全错误并记录固定失败观测。
func (repository *Repository) failure(operation string, outcome string, cause error) error {
	repository.observe(operation, outcome)
	return &adapterError{operation: operation, outcome: outcome, cause: cause}
}

// observe 只转发代码定义的低基数 adapter/operation/outcome。
func (repository *Repository) observe(operation string, outcome string) {
	repository.observer.RecordStorageOperation("personalworld", operation, outcome)
}

// digestKey 把原始幂等 key 固定映射为 actor-scoped BINARY(32) 索引。
func digestKey(key domain.IdempotencyKey) [sha256.Size]byte {
	return sha256.Sum256([]byte(key.Value()))
}

// isDuplicate 只识别 MySQL 唯一约束明确拒绝，不解析易变错误文本。
func isDuplicate(err error) bool {
	var failure *mysqldriver.MySQLError
	return errors.As(err, &failure) && failure.Number == 1062
}

// transactionOutcome 从错误链提取共享 MySQL commit 边界。
func transactionOutcome(err error) storagemysql.TransactionOutcome {
	var failure *storagemysql.TransactionError
	if errors.As(err, &failure) {
		return failure.Outcome
	}
	return storagemysql.TransactionNotCommitted
}

// outcomeName 返回 EnsurePrimary 的稳定观测名称。
func outcomeName(outcome domain.EnsureOutcome) string {
	switch outcome {
	case domain.EnsureOutcomeCreated:
		return "created"
	case domain.EnsureOutcomeExisting:
		return "existing"
	case domain.EnsureOutcomeCommitUnknown:
		return "commit_unknown"
	default:
		return "not_committed"
	}
}

// mutationOutcomeName 返回 archive 的稳定观测名称。
func mutationOutcomeName(outcome domain.MutationOutcome) string {
	switch outcome {
	case domain.MutationOutcomeApplied:
		return "applied"
	case domain.MutationOutcomeReplay:
		return "replay"
	case domain.MutationOutcomeNotFound:
		return "not_found"
	case domain.MutationOutcomeRevisionConflict:
		return "revision_conflict"
	case domain.MutationOutcomeIdempotencyConflict:
		return "idempotency_conflict"
	case domain.MutationOutcomeInvalidState:
		return "invalid_state"
	case domain.MutationOutcomeCommitUnknown:
		return "commit_unknown"
	default:
		return "not_committed"
	}
}
