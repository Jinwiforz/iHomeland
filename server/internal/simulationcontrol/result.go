package simulationcontrol

import (
	"context"
	"errors"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
)

const (
	// LifecycleSummaryKind 是 B0.4 首个且唯一已登记结果类型。
	LifecycleSummaryKind = "simulation.lifecycle.summary.v1"
)

// ResultDisposition 是不可变 receipt 的 terminal decision。
type ResultDisposition string

const (
	// ResultDispositionCommitted 表示 owner mutation 与 receipt 同 transaction 提交。
	ResultDispositionCommitted ResultDisposition = "committed"
	// ResultDispositionRejected 表示提案被持久记录为终态拒绝。
	ResultDispositionRejected ResultDisposition = "rejected"
)

// ResultDecision 是 coordinator 交给 receipt store 的稳定裁决。
type ResultDecision struct {
	// Disposition 是 committed 或 rejected。
	Disposition ResultDisposition
	// Reason 是低基数、无 identity 的稳定原因。
	Reason string
	// DecidedAt 是 UTC 微秒时间。
	DecidedAt time.Time
}

// Validate 拒绝未知 disposition、空 reason 或非 canonical 时间。
func (decision ResultDecision) Validate() error {
	if decision.Disposition != ResultDispositionCommitted &&
		decision.Disposition != ResultDispositionRejected ||
		decision.Reason == "" || len(decision.Reason) > 64 ||
		decision.DecidedAt.IsZero() ||
		decision.DecidedAt != decision.DecidedAt.UTC().Truncate(time.Microsecond) {
		return errors.New("simulation result decision is invalid")
	}
	return nil
}

// ResultReceipt 是 MySQL 保存的 immutable proposal+decision 投影。
type ResultReceipt struct {
	// Proposal 是首次见到的完整 immutable proposal。
	Proposal ResultProposal
	// Decision 是 terminal decision。
	Decision ResultDecision
}

// Validate 报告 receipt 是否完整。
func (receipt ResultReceipt) Validate() error {
	if receipt.Proposal.Validate() != nil || receipt.Decision.Validate() != nil {
		return errors.New("simulation result receipt is invalid")
	}
	return nil
}

// ReceiptLookupOutcome 是 result ID 查询的封闭结果。
type ReceiptLookupOutcome uint8

const (
	// ReceiptLookupUnspecified 是非法零值。
	ReceiptLookupUnspecified ReceiptLookupOutcome = iota
	// ReceiptLookupNotFound 表示尚无 terminal receipt。
	ReceiptLookupNotFound
	// ReceiptLookupFound 表示返回 immutable receipt。
	ReceiptLookupFound
)

// ReceiptCommitOutcome 是原子裁决的封闭结果。
type ReceiptCommitOutcome uint8

const (
	// ReceiptCommitUnspecified 是非法零值。
	ReceiptCommitUnspecified ReceiptCommitOutcome = iota
	// ReceiptCommitApplied 表示本次首次提交。
	ReceiptCommitApplied
	// ReceiptCommitReplayed 表示 matching immutable receipt 已存在。
	ReceiptCommitReplayed
	// ReceiptCommitConflict 表示 ResultID 已绑定不同 proposal。
	ReceiptCommitConflict
	// ReceiptCommitUnknown 表示 transaction 结果无法证明。
	ReceiptCommitUnknown
)

// ReceiptStore 原子保存 owner mutation 与 immutable receipt。
type ReceiptStore interface {
	// Lookup 按 ResultID 查询 terminal receipt。
	Lookup(context.Context, string) (ResultReceipt, ReceiptLookupOutcome, error)
	// Decide 首次提交或返回 matching replay/conflict/unknown。
	Decide(context.Context, ResultProposal, ResultDecision) (ResultReceipt, ReceiptCommitOutcome, error)
}

// ProposalBindingResolver 将 C++ result 解析到 exact runtime stamp。
type ProposalBindingResolver interface {
	// ResolveProposalBinding 只接受 matching fingerprint 与 instance。
	ResolveProposalBinding(ResultProposal) (placement.AssignmentStamp, bool)
}

// ResultAcknowledger 向 child 发送 terminal ack。
type ResultAcknowledger interface {
	// AcknowledgeResult 在 receipt 已持久后发送 disposition。
	AcknowledgeResult(context.Context, ResultProposal, string) error
}

// CurrentAssignmentReader 读取 result commit 时的 current placement 事实。
type CurrentAssignmentReader interface {
	// Resolve 返回指定 world 在 observedAt 的 current snapshot。
	Resolve(context.Context, personalworld.PersonalWorldID, time.Time) (placement.AssignmentSnapshot, placement.ResolveOutcome, error)
}

// ResultClock 为 receipt 提供受信 UTC 时间。
type ResultClock interface {
	// Now 返回当前绝对时间。
	Now() time.Time
}

// ResultProcessOutcome 是持久裁决并完成 ack 后的稳定结果。
type ResultProcessOutcome string

const (
	// ResultProcessCommitted 表示首次持久接受并 ack。
	ResultProcessCommitted ResultProcessOutcome = "committed"
	// ResultProcessRejected 表示首次持久拒绝并 ack。
	ResultProcessRejected ResultProcessOutcome = "rejected"
	// ResultProcessReplayed 表示 matching receipt 已存在并重放 ack。
	ResultProcessReplayed ResultProcessOutcome = "replayed"
)

// ResultCoordinator 执行 lookup、fence/current 校验、持久裁决和 terminal ack。
type ResultCoordinator struct {
	// receipts 是唯一 immutable decision owner。
	receipts ReceiptStore
	// bindings 解析 C++ instance 到 placement stamp。
	bindings ProposalBindingResolver
	// current 是 placement current 线性化事实读端口。
	current CurrentAssignmentReader
	// acknowledger 只在 receipt 确定后通知 child。
	acknowledger ResultAcknowledger
	// clock 为新 decision 提供 UTC 微秒时间。
	clock ResultClock
}

// NewResultCoordinator 校验全部消费侧依赖。
func NewResultCoordinator(receipts ReceiptStore, bindings ProposalBindingResolver, current CurrentAssignmentReader, acknowledger ResultAcknowledger, clock ResultClock) (*ResultCoordinator, error) {
	if receipts == nil || bindings == nil || current == nil || acknowledger == nil || clock == nil {
		return nil, errors.New("simulation result coordinator dependencies are invalid")
	}
	return &ResultCoordinator{
		receipts:     receipts,
		bindings:     bindings,
		current:      current,
		acknowledger: acknowledger,
		clock:        clock,
	}, nil
}

// Process 先解析既有 receipt，再验证 current binding 并持久裁决。
func (coordinator *ResultCoordinator) Process(ctx context.Context, proposal ResultProposal) error {
	_, err := coordinator.ProcessWithOutcome(ctx, proposal)
	return err
}

// ProcessWithOutcome 执行 receipt-first 裁决并返回低基数 terminal outcome。
func (coordinator *ResultCoordinator) ProcessWithOutcome(ctx context.Context, proposal ResultProposal) (ResultProcessOutcome, error) {
	if coordinator == nil || ctx == nil || proposal.Validate() != nil {
		return "", errors.New("simulation result process input is invalid")
	}
	existing, lookup, err := coordinator.receipts.Lookup(ctx, proposal.ResultID)
	if err != nil {
		return "", err
	}
	if lookup == ReceiptLookupFound {
		if existing.Validate() != nil {
			return "", errors.New("simulation result receipt is invalid")
		}
		if existing.Proposal != proposal {
			return "", errors.New("simulation result ID fingerprint conflicts")
		}
		if err := coordinator.acknowledger.AcknowledgeResult(ctx, proposal, "replayed"); err != nil {
			return "", err
		}
		return ResultProcessReplayed, nil
	}
	if lookup != ReceiptLookupNotFound {
		return "", errors.New("simulation receipt store returned invalid lookup outcome")
	}

	now := coordinator.clock.Now().UTC().Truncate(time.Microsecond)
	decision := ResultDecision{
		Disposition: ResultDispositionCommitted,
		Reason:      "accepted",
		DecidedAt:   now,
	}
	stamp, bound := coordinator.bindings.ResolveProposalBinding(proposal)
	if proposal.Kind != LifecycleSummaryKind {
		decision.Disposition = ResultDispositionRejected
		decision.Reason = "unknown_kind"
	} else if !validLifecycleTickRange(proposal.TickStart, proposal.TickEnd) {
		decision.Disposition = ResultDispositionRejected
		decision.Reason = "invalid_tick_range"
	} else if !bound {
		decision.Disposition = ResultDispositionRejected
		decision.Reason = "stale_binding"
	} else {
		current, outcome, resolveErr := coordinator.current.Resolve(ctx, stamp.WorldID(), now)
		if resolveErr != nil {
			return "", resolveErr
		}
		if outcome != placement.ResolveOutcomeFound || !current.ValidAt(now) ||
			!current.Stamp().Equal(stamp) {
			decision.Disposition = ResultDispositionRejected
			decision.Reason = "stale_assignment"
		}
	}
	if decision.Validate() != nil {
		return "", errors.New("simulation result decision could not be constructed")
	}
	receipt, outcome, err := coordinator.receipts.Decide(ctx, proposal, decision)
	if err != nil {
		return "", err
	}
	switch outcome {
	case ReceiptCommitApplied:
		disposition := string(receipt.Decision.Disposition)
		if err := coordinator.acknowledger.AcknowledgeResult(ctx, proposal, disposition); err != nil {
			return "", err
		}
		if receipt.Decision.Disposition == ResultDispositionRejected {
			return ResultProcessRejected, nil
		}
		return ResultProcessCommitted, nil
	case ReceiptCommitReplayed:
		if err := coordinator.acknowledger.AcknowledgeResult(ctx, proposal, "replayed"); err != nil {
			return "", err
		}
		return ResultProcessReplayed, nil
	case ReceiptCommitConflict:
		return "", errors.New("simulation result receipt conflicts")
	case ReceiptCommitUnknown:
		return "", errors.New("simulation result commit outcome is unknown")
	default:
		return "", errors.New("simulation receipt store returned invalid commit outcome")
	}
}

// validLifecycleTickRange 只接受空运行 0..0 或从首 Tick 开始的 1..N 摘要。
func validLifecycleTickRange(start uint64, end uint64) bool {
	return start == 0 && end == 0 || start == 1 && end >= 1
}
