// Package simulationresult 持有 Simulation ResultProposal 的 MySQL 不可变裁决。
package simulationresult

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	domain "github.com/jinwiforz/ihomeland/server/internal/simulationcontrol"
)

const receiptColumns = `result_id, proposal_fingerprint, assignment_fingerprint,
simulation_instance_id, result_kind, tick_start, tick_end, payload_digest,
evidence_digest, disposition, reason, decided_at`

// Observer 接收固定 adapter/operation/outcome，不接收 ResultID 或 digest。
type Observer interface {
	// RecordStorageOperation 记录低基数结果。
	RecordStorageOperation(string, string, string)
}

// OwnerCommitter 在 receipt 同 transaction 内提交已登记 result kind 的业务 mutation。
type OwnerCommitter interface {
	// Commit 只能消费强类型低敏 proposal，不得提交未登记 opaque payload。
	Commit(context.Context, *sql.Tx, domain.ResultProposal) error
}

// LifecycleSummaryCommitter 明确登记 lifecycle summary 当前没有额外持久 mutation。
type LifecycleSummaryCommitter struct{}

// Commit 接受已验证摘要；receipt 本身就是当前 kind 的唯一持久事实。
func (LifecycleSummaryCommitter) Commit(_ context.Context, _ *sql.Tx, proposal domain.ResultProposal) error {
	if proposal.Validate() != nil || proposal.Kind != domain.LifecycleSummaryKind {
		return errors.New("lifecycle summary proposal is invalid")
	}
	return nil
}

// Store 原子写入 owner mutation 与 immutable result receipt。
type Store struct {
	// db 由 MySQL component 持有。
	db *sql.DB
	// committers 是 closed result catalog。
	committers map[string]OwnerCommitter
	// observer 只接收固定低基数值。
	observer Observer
}

// New 创建不获取 connection 的 receipt adapter。
func New(db *sql.DB, observer Observer) (*Store, error) {
	if db == nil || observer == nil {
		return nil, errors.New("simulation result store dependencies are invalid")
	}
	return &Store{
		db: db,
		committers: map[string]OwnerCommitter{
			domain.LifecycleSummaryKind: LifecycleSummaryCommitter{},
		},
		observer: observer,
	}, nil
}

// Lookup 按 ResultID 查询 immutable receipt。
func (store *Store) Lookup(ctx context.Context, resultID string) (domain.ResultReceipt, domain.ReceiptLookupOutcome, error) {
	if store == nil || ctx == nil || resultID == "" || len(resultID) > 96 {
		return domain.ResultReceipt{}, domain.ReceiptLookupUnspecified, errors.New("simulation result lookup input is invalid")
	}
	receipt, found, err := scanReceipt(store.db.QueryRowContext(
		ctx,
		"SELECT "+receiptColumns+" FROM simulation_result_receipts WHERE result_id = ?",
		resultID,
	))
	if err != nil {
		store.observe("lookup", "failed")
		return domain.ResultReceipt{}, domain.ReceiptLookupUnspecified, &storeError{operation: "lookup", cause: err}
	}
	if !found {
		store.observe("lookup", "not_found")
		return domain.ResultReceipt{}, domain.ReceiptLookupNotFound, nil
	}
	store.observe("lookup", "found")
	return receipt, domain.ReceiptLookupFound, nil
}

// Decide 首次执行 registered owner committer 并插入 receipt，或返回 matching replay/conflict。
func (store *Store) Decide(ctx context.Context, proposal domain.ResultProposal, decision domain.ResultDecision) (domain.ResultReceipt, domain.ReceiptCommitOutcome, error) {
	if store == nil || ctx == nil || proposal.Validate() != nil || decision.Validate() != nil {
		return domain.ResultReceipt{}, domain.ReceiptCommitUnspecified, errors.New("simulation result decision input is invalid")
	}
	if decision.Disposition == domain.ResultDispositionCommitted {
		if _, registered := store.committers[proposal.Kind]; !registered {
			return domain.ResultReceipt{}, domain.ReceiptCommitUnspecified, errors.New("simulation result kind is not registered")
		}
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		store.observe("decide", "begin_failed")
		return domain.ResultReceipt{}, domain.ReceiptCommitUnknown, &storeError{operation: "decide", cause: err}
	}
	rollback := func() {
		_ = tx.Rollback()
	}
	existing, found, err := scanReceipt(tx.QueryRowContext(
		ctx,
		"SELECT "+receiptColumns+" FROM simulation_result_receipts WHERE result_id = ? FOR UPDATE",
		proposal.ResultID,
	))
	if err != nil {
		rollback()
		store.observe("decide", "read_failed")
		return domain.ResultReceipt{}, domain.ReceiptCommitUnknown, &storeError{operation: "decide", cause: err}
	}
	if found {
		rollback()
		if receiptMatches(existing, proposal, decision) {
			store.observe("decide", "replayed")
			return existing, domain.ReceiptCommitReplayed, nil
		}
		store.observe("decide", "conflict")
		return existing, domain.ReceiptCommitConflict, nil
	}
	if decision.Disposition == domain.ResultDispositionCommitted {
		if err := store.committers[proposal.Kind].Commit(ctx, tx, proposal); err != nil {
			rollback()
			store.observe("decide", "owner_rejected")
			return domain.ResultReceipt{}, domain.ReceiptCommitUnspecified, &storeError{operation: "decide", cause: err}
		}
	}
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO simulation_result_receipts (
result_id, proposal_fingerprint, assignment_fingerprint, simulation_instance_id,
result_kind, tick_start, tick_end, payload_digest, evidence_digest, disposition,
reason, decided_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		proposal.ResultID,
		mustDigestBytes(proposal.ProposalFingerprint),
		mustDigestBytes(proposal.AssignmentFingerprint),
		proposal.InstanceID.String(),
		proposal.Kind,
		proposal.TickStart,
		proposal.TickEnd,
		mustDigestBytes(proposal.PayloadDigest),
		mustDigestBytes(proposal.EvidenceDigest),
		string(decision.Disposition),
		decision.Reason,
		decision.DecidedAt,
	)
	if err != nil {
		rollback()
		var mysqlErr *mysqldriver.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return store.resolveDuplicate(ctx, proposal, decision)
		}
		store.observe("decide", "insert_failed")
		return domain.ResultReceipt{}, domain.ReceiptCommitUnspecified, &storeError{operation: "decide", cause: err}
	}
	if err := tx.Commit(); err != nil {
		store.observe("decide", "commit_unknown")
		return domain.ResultReceipt{}, domain.ReceiptCommitUnknown, &storeError{operation: "decide", cause: err}
	}
	receipt := domain.ResultReceipt{Proposal: proposal, Decision: decision}
	store.observe("decide", "applied")
	return receipt, domain.ReceiptCommitApplied, nil
}

// resolveDuplicate 在并发 insert 失败后重新读取 terminal receipt。
func (store *Store) resolveDuplicate(ctx context.Context, proposal domain.ResultProposal, decision domain.ResultDecision) (domain.ResultReceipt, domain.ReceiptCommitOutcome, error) {
	receipt, outcome, err := store.Lookup(ctx, proposal.ResultID)
	if err != nil || outcome != domain.ReceiptLookupFound {
		store.observe("decide", "duplicate_unknown")
		return domain.ResultReceipt{}, domain.ReceiptCommitUnknown, &storeError{operation: "decide", cause: err}
	}
	if receiptMatches(receipt, proposal, decision) {
		store.observe("decide", "replayed")
		return receipt, domain.ReceiptCommitReplayed, nil
	}
	store.observe("decide", "conflict")
	return receipt, domain.ReceiptCommitConflict, nil
}

// receiptScanner 抽象 sql.Row 便于 transaction 与 DB 复用 codec。
type receiptScanner interface {
	// Scan 读取固定 receipt columns。
	Scan(...any) error
}

// scanReceipt 严格解码一行或 not found。
func scanReceipt(row receiptScanner) (domain.ResultReceipt, bool, error) {
	var (
		resultID, instanceID, kind, disposition, reason string
		proposalFingerprint, assignmentFingerprint      []byte
		payloadDigest, evidenceDigest                   []byte
		tickStart, tickEnd                              uint64
		decidedAt                                       time.Time
	)
	err := row.Scan(
		&resultID,
		&proposalFingerprint,
		&assignmentFingerprint,
		&instanceID,
		&kind,
		&tickStart,
		&tickEnd,
		&payloadDigest,
		&evidenceDigest,
		&disposition,
		&reason,
		&decidedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ResultReceipt{}, false, nil
	}
	if err != nil {
		return domain.ResultReceipt{}, false, err
	}
	proposalDigest, err := digestFromBytes(proposalFingerprint)
	if err != nil {
		return domain.ResultReceipt{}, false, err
	}
	assignmentDigest, err := digestFromBytes(assignmentFingerprint)
	if err != nil {
		return domain.ResultReceipt{}, false, err
	}
	payload, err := digestFromBytes(payloadDigest)
	if err != nil {
		return domain.ResultReceipt{}, false, err
	}
	evidence, err := digestFromBytes(evidenceDigest)
	if err != nil {
		return domain.ResultReceipt{}, false, err
	}
	simulationInstanceID, err := domain.NewSimulationInstanceID(instanceID)
	if err != nil {
		return domain.ResultReceipt{}, false, err
	}
	receipt := domain.ResultReceipt{
		Proposal: domain.ResultProposal{
			ResultID:              resultID,
			Kind:                  kind,
			AssignmentFingerprint: assignmentDigest,
			InstanceID:            simulationInstanceID,
			TickStart:             tickStart,
			TickEnd:               tickEnd,
			PayloadDigest:         payload,
			EvidenceDigest:        evidence,
			ProposalFingerprint:   proposalDigest,
		},
		Decision: domain.ResultDecision{
			Disposition: domain.ResultDisposition(disposition),
			Reason:      reason,
			DecidedAt:   decidedAt.UTC().Truncate(time.Microsecond),
		},
	}
	if err := receipt.Validate(); err != nil {
		return domain.ResultReceipt{}, false, err
	}
	return receipt, true, nil
}

// receiptMatches 比较全部 immutable proposal 与 decision 字段。
func receiptMatches(receipt domain.ResultReceipt, proposal domain.ResultProposal, decision domain.ResultDecision) bool {
	return receipt.Proposal == proposal &&
		receipt.Decision.Disposition == decision.Disposition &&
		receipt.Decision.Reason == decision.Reason
}

// mustDigestBytes 把已验证 Digest 转为 32-byte SQL 参数。
func mustDigestBytes(digest domain.Digest) []byte {
	decoded, _ := hex.DecodeString(digest.String())
	return decoded
}

// digestFromBytes 解码 fixed BINARY(32)。
func digestFromBytes(value []byte) (domain.Digest, error) {
	if len(value) != 32 {
		return "", errors.New("simulation result digest column has invalid length")
	}
	return domain.NewDigest(hex.EncodeToString(value))
}

// storeError 不在默认文本中暴露 SQL、result identity 或 driver 内容。
type storeError struct {
	// operation 是固定 adapter 操作。
	operation string
	// cause 只用于受控诊断。
	cause error
}

// Error 返回低敏稳定错误。
func (failure *storeError) Error() string {
	return fmt.Sprintf("simulation result storage %s failed", failure.operation)
}

// Unwrap 返回底层 cause。
func (failure *storeError) Unwrap() error { return failure.cause }

// observe 记录固定低基数 outcome。
func (store *Store) observe(operation string, outcome string) {
	store.observer.RecordStorageOperation("simulationresult", operation, outcome)
}

var (
	_ domain.ReceiptStore = (*Store)(nil)
	_ OwnerCommitter      = LifecycleSummaryCommitter{}
)
