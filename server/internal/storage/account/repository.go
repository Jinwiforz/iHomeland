package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	domain "github.com/jinwiforz/ihomeland/server/internal/account"
	storagemysql "github.com/jinwiforz/ihomeland/server/internal/storage/mysql"
)

// Observer 接收 repository 的固定 operation/outcome，不接收 SQL、username、identity 或 credential。
type Observer interface {
	// RecordStorageOperation 记录 adapter、operation 与 outcome 三个低基数字段。
	RecordStorageOperation(string, string, string)
}

// Repository 借用共享 MySQL pool 实现 AccountRepository。
//
// Repository 不拥有 pool、listener 或后台任务，可并发调用；共享 pool 必须晚于所有调用关闭。
type Repository struct {
	// db 由 storage/mysql Component 持有，本 adapter 只在调用期间借用。
	db *sql.DB
	// observer 只接收固定低基数结果。
	observer Observer
	// withinTx 固定 production transaction runner；测试可注入 commit boundary，不自动重放。
	withinTx func(context.Context, *sql.DB, *sql.TxOptions, func(*sql.Tx) error) error
}

var _ domain.AccountRepository = (*Repository)(nil)
var _ domain.InvitablePlayerReader = (*Repository)(nil)

// errUsernameConflict 只在 transaction 内传递已知 username 唯一约束分类。
var errUsernameConflict = errors.New("account username unique constraint conflict")

// New 创建不获取资源的 Account MySQL adapter。
func New(db *sql.DB, observer Observer) (*Repository, error) {
	if db == nil || observer == nil {
		return nil, errors.New("account repository requires database and observer")
	}
	return &Repository{db: db, observer: observer, withinTx: storagemysql.WithinTx}, nil
}

// Create 以单 row transaction 原子提交 account、player 与 credential。
//
// Username unique 是唯一可公开业务冲突；AccountID/PlayerID 碰撞表示 server identity
// 或数据缺陷。Commit acknowledgement 不明确时禁止重放或猜测 rollback。
func (repository *Repository) Create(ctx context.Context, record domain.CreateRecord) (domain.CreateOutcome, error) {
	if !record.Account.Valid() || !record.Credential.Valid() || validatePHC(record.Credential.Encoded()) != nil {
		return domain.CreateOutcomeNotCommitted, repository.failure("create", "invalid", errors.New("account create record is invalid"))
	}
	status, err := encodeAccountStatus(record.Account.Status())
	if err != nil {
		return domain.CreateOutcomeNotCommitted, repository.failure("create", "invalid", err)
	}
	err = repository.withinTx(ctx, repository.db, nil, func(tx *sql.Tx) error {
		_, insertErr := tx.ExecContext(ctx, `INSERT INTO accounts
			(account_id, player_id, username, display_name, credential_hash, status, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, record.Account.ID().String(), record.Account.PlayerID().String(),
			record.Account.Username().String(), record.Account.DisplayName().String(), record.Credential.Encoded(), status,
			record.Account.CreatedAt().UTC().Truncate(time.Microsecond))
		if insertErr == nil {
			return nil
		}
		constraint := duplicateConstraint(insertErr)
		if constraint == "username" {
			return errUsernameConflict
		}
		if constraint == "account" || constraint == "player" {
			return errors.New("server-generated account identity collided")
		}
		return insertErr
	})
	if errors.Is(err, errUsernameConflict) {
		repository.observe("create", "username_conflict")
		return domain.CreateOutcomeUsernameConflict, nil
	}
	if err != nil {
		if transactionOutcome(err) == storagemysql.TransactionCommitUnknown {
			return domain.CreateOutcomeCommitUnknown, repository.failure("create", "commit_unknown", err)
		}
		return domain.CreateOutcomeNotCommitted, repository.failure("create", "not_committed", err)
	}
	repository.observe("create", "created")
	return domain.CreateOutcomeCreated, nil
}

// FindForAuthentication 读取 exact canonical username 的完整认证快照。
func (repository *Repository) FindForAuthentication(ctx context.Context, username domain.Username) (domain.AuthenticationRecord, domain.FindOutcome, error) {
	if !username.Valid() {
		return domain.AuthenticationRecord{}, domain.FindOutcomeUnspecified, repository.failure("find_for_authentication", "invalid", errors.New("canonical username is invalid"))
	}
	record, err := scanAuthentication(repository.db.QueryRowContext(ctx, `SELECT account_id, player_id, username,
		display_name, credential_hash, status, created_at FROM accounts WHERE username = ?`, username.String()))
	if errors.Is(err, sql.ErrNoRows) {
		repository.observe("find_for_authentication", "not_found")
		return domain.AuthenticationRecord{}, domain.FindOutcomeNotFound, nil
	}
	if err != nil {
		return domain.AuthenticationRecord{}, domain.FindOutcomeUnspecified, repository.failure("find_for_authentication", "failed", err)
	}
	if record.Account.Username() != username {
		return domain.AuthenticationRecord{}, domain.FindOutcomeUnspecified, repository.failure("find_for_authentication", "corrupt", errors.New("account query returned different username"))
	}
	repository.observe("find_for_authentication", "found")
	return record, domain.FindOutcomeFound, nil
}

// ResolveInvitablePlayer 按唯一 PlayerID 返回 active 可邀请性，不暴露账号其他事实。
func (repository *Repository) ResolveInvitablePlayer(ctx context.Context, playerID domain.PlayerID) (domain.InvitablePlayerOutcome, error) {
	if ctx == nil || !playerID.Valid() {
		return domain.InvitablePlayerOutcomeUnspecified, repository.failure("resolve_invitable_player", "invalid", errors.New("player identity is invalid"))
	}
	var statusValue string
	err := repository.db.QueryRowContext(ctx, `SELECT status FROM accounts WHERE player_id = ?`, playerID.String()).Scan(&statusValue)
	if errors.Is(err, sql.ErrNoRows) {
		repository.observe("resolve_invitable_player", "unavailable")
		return domain.InvitablePlayerOutcomeUnavailable, nil
	}
	if err != nil {
		return domain.InvitablePlayerOutcomeUnspecified, repository.failure("resolve_invitable_player", "failed", err)
	}
	status, err := decodeAccountStatus(statusValue)
	if err != nil {
		return domain.InvitablePlayerOutcomeUnspecified, repository.failure("resolve_invitable_player", "corrupt", err)
	}
	if status != domain.StatusActive {
		repository.observe("resolve_invitable_player", "unavailable")
		return domain.InvitablePlayerOutcomeUnavailable, nil
	}
	repository.observe("resolve_invitable_player", "available")
	return domain.InvitablePlayerOutcomeAvailable, nil
}

// adapterError 保留受控 cause，默认文本只包含固定 operation/outcome。
type adapterError struct {
	// operation 是代码定义的 repository 操作。
	operation string
	// outcome 是固定低基数失败分类。
	outcome string
	// cause 只供受控 errors.Is/As 使用，不参与 Error 文本。
	cause error
}

// Error 返回不包含 SQL、driver文本、username、identity或credential的稳定错误。
func (failure *adapterError) Error() string {
	return fmt.Sprintf("account storage %s failed (%s)", failure.operation, failure.outcome)
}

// Unwrap 返回受控内部诊断使用的底层 cause。
func (failure *adapterError) Unwrap() error { return failure.cause }

// failure 构造安全错误并记录固定失败观测。
func (repository *Repository) failure(operation string, outcome string, cause error) error {
	repository.observe(operation, outcome)
	return &adapterError{operation: operation, outcome: outcome, cause: cause}
}

// observe 只转发固定 adapter/operation/outcome。
func (repository *Repository) observe(operation string, outcome string) {
	repository.observer.RecordStorageOperation("account", operation, outcome)
}

// duplicateConstraint 只在 MySQL 1062 内部文本中识别代码拥有的 constraint name。
//
// Driver message 可能包含 username/identity，因此返回值只允许固定分类，调用方禁止记录原 error。
func duplicateConstraint(err error) string {
	var failure *mysqldriver.MySQLError
	if !errors.As(err, &failure) || failure.Number != 1062 {
		return ""
	}
	switch {
	case strings.Contains(failure.Message, "uq_accounts_username"):
		return "username"
	case strings.Contains(failure.Message, "uq_accounts_player"):
		return "player"
	case strings.Contains(failure.Message, "PRIMARY"):
		return "account"
	default:
		return ""
	}
}

// transactionOutcome 从错误链提取共享 MySQL commit boundary。
func transactionOutcome(err error) storagemysql.TransactionOutcome {
	var failure *storagemysql.TransactionError
	if errors.As(err, &failure) {
		return failure.Outcome
	}
	return storagemysql.TransactionNotCommitted
}
