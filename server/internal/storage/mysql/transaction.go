package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// TransactionOutcome 描述失败发生时可证明的 commit 边界。
type TransactionOutcome string

const (
	// TransactionNotCommitted 表示 transaction 尚未开始或已确认 rollback。
	TransactionNotCommitted TransactionOutcome = "not_committed"
	// TransactionPreCommitTransient 表示 commit 前遇到可由业务 owner 评估重试的瞬时冲突。
	TransactionPreCommitTransient TransactionOutcome = "pre_commit_transient"
	// TransactionCommitUnknown 表示 commit 请求后无法证明是否已提交，禁止盲目重放。
	TransactionCommitUnknown TransactionOutcome = "commit_unknown"
)

// TransactionError 保留 transaction outcome 和内部原因，不决定业务重试。
type TransactionError struct {
	// Outcome 是稳定 commit 边界分类。
	Outcome TransactionOutcome
	// Err 是供内部 errors.Is/As 使用的底层错误。
	Err error
}

// Error 只返回稳定分类；底层原因仅通过 errors.Is/As 在受控内部链路读取。
func (failure *TransactionError) Error() string {
	return "mysql transaction " + string(failure.Outcome)
}

// Unwrap 返回底层 transaction 原因。
func (failure *TransactionError) Unwrap() error { return failure.Err }

// WithinTx 创建 transaction、恰好执行一次 callback，并按结果 commit 或 rollback。
//
// callback 不得自行 Commit/Rollback、不得保存 tx 到返回后使用；其 error 原样作为 owner
// outcome 保留。panic 会触发 best-effort rollback 后继续传播。任何 Commit error（包括
// context cancellation 或连接中断）都按 commit-unknown 返回，函数从不自动重放 callback。
func WithinTx(ctx context.Context, db *sql.DB, options *sql.TxOptions, callback func(*sql.Tx) error) error {
	if db == nil || callback == nil {
		return &TransactionError{Outcome: TransactionNotCommitted, Err: errors.New("database and callback are required")}
	}
	tx, err := db.BeginTx(ctx, options)
	if err != nil {
		return &TransactionError{Outcome: classifyPreCommit(err), Err: err}
	}
	// 该 defer 只覆盖 callback panic 等非正常退出；正常 error 路径仍显式保留 rollback failure。
	defer func() { _ = tx.Rollback() }()
	callbackErr := callback(tx)
	if callbackErr != nil {
		rollbackErr := tx.Rollback()
		return errors.Join(callbackErr, wrapRollbackFailure(rollbackErr))
	}
	if err := tx.Commit(); err != nil {
		return &TransactionError{Outcome: TransactionCommitUnknown, Err: err}
	}
	return nil
}

// classifyPreCommit 只把明确 MySQL lock timeout/deadlock 标记为 owner 可评估的瞬时失败。
func classifyPreCommit(err error) TransactionOutcome {
	var mysqlError *mysqldriver.MySQLError
	if errors.As(err, &mysqlError) && (mysqlError.Number == 1205 || mysqlError.Number == 1213) {
		return TransactionPreCommitTransient
	}
	return TransactionNotCommitted
}

// wrapRollbackFailure 忽略已完成 transaction，并保留其他 cleanup failure 的 not-committed 分类。
func wrapRollbackFailure(err error) error {
	if err == nil || errors.Is(err, sql.ErrTxDone) {
		return nil
	}
	return &TransactionError{Outcome: TransactionNotCommitted, Err: fmt.Errorf("rollback failed: %w", err)}
}
