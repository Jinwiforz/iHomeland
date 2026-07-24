package simulationcontrol

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
)

// memoryReceiptStore 模拟 ResultID immutable first-writer decision。
type memoryReceiptStore struct {
	// mutex 保护 receipts。
	mutex sync.Mutex
	// receipts 按 ResultID 保存。
	receipts map[string]ResultReceipt
}

// Lookup 返回内存 receipt。
func (store *memoryReceiptStore) Lookup(_ context.Context, resultID string) (ResultReceipt, ReceiptLookupOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	receipt, ok := store.receipts[resultID]
	if !ok {
		return ResultReceipt{}, ReceiptLookupNotFound, nil
	}
	return receipt, ReceiptLookupFound, nil
}

// Decide 原子区分 applied、replay 与 conflict。
func (store *memoryReceiptStore) Decide(_ context.Context, proposal ResultProposal, decision ResultDecision) (ResultReceipt, ReceiptCommitOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if existing, ok := store.receipts[proposal.ResultID]; ok {
		if existing.Proposal == proposal &&
			existing.Decision.Disposition == decision.Disposition &&
			existing.Decision.Reason == decision.Reason {
			return existing, ReceiptCommitReplayed, nil
		}
		return existing, ReceiptCommitConflict, nil
	}
	receipt := ResultReceipt{Proposal: proposal, Decision: decision}
	store.receipts[proposal.ResultID] = receipt
	return receipt, ReceiptCommitApplied, nil
}

// resultFixture 同时提供 binding、current、ack 和 clock。
type resultFixture struct {
	// stamp 是 proposal 的 exact runtime binding。
	stamp placement.AssignmentStamp
	// current 是 commit 时的 placement snapshot。
	current placement.AssignmentSnapshot
	// bound 控制 C++ binding 是否仍存在。
	bound bool
	// acknowledgements 保存 terminal disposition。
	acknowledgements []string
	// acknowledgeErr 模拟 receipt 已提交后的 response loss。
	acknowledgeErr error
	// now 是固定 UTC 微秒时间。
	now time.Time
}

// ResolveProposalBinding 返回配置的 exact stamp。
func (fixture *resultFixture) ResolveProposalBinding(ResultProposal) (placement.AssignmentStamp, bool) {
	return fixture.stamp, fixture.bound
}

// AcknowledgeResult 记录 terminal disposition。
func (fixture *resultFixture) AcknowledgeResult(_ context.Context, _ ResultProposal, disposition string) error {
	if fixture.acknowledgeErr != nil {
		return fixture.acknowledgeErr
	}
	fixture.acknowledgements = append(fixture.acknowledgements, disposition)
	return nil
}

// Resolve 返回 fixture current。
func (fixture *resultFixture) Resolve(_ context.Context, worldID personalworld.PersonalWorldID, _ time.Time) (placement.AssignmentSnapshot, placement.ResolveOutcome, error) {
	if fixture.current.WorldID() != worldID {
		return placement.AssignmentSnapshot{}, placement.ResolveOutcomeNotFound, nil
	}
	return fixture.current, placement.ResolveOutcomeFound, nil
}

// Now 返回固定时间。
func (fixture *resultFixture) Now() time.Time { return fixture.now }

// TestResultCoordinatorCommitAndReplay 验证先 receipt、current fence、commit、ack 与 restart replay。
func TestResultCoordinatorCommitAndReplay(t *testing.T) {
	t.Parallel()
	fixture := newResultFixture(t)
	store := &memoryReceiptStore{receipts: make(map[string]ResultReceipt)}
	coordinator, err := NewResultCoordinator(store, fixture, fixture, fixture, fixture)
	if err != nil {
		t.Fatal(err)
	}
	proposal := testResultProposal(t, LifecycleSummaryKind, "sresult_resulttest1")
	if err := coordinator.Process(context.Background(), proposal); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(fixture.acknowledgements) != 1 || fixture.acknowledgements[0] != "committed" {
		t.Fatalf("acknowledgements = %#v", fixture.acknowledgements)
	}
	if err := coordinator.Process(context.Background(), proposal); err != nil {
		t.Fatalf("replay Process: %v", err)
	}
	if fixture.acknowledgements[1] != "replayed" {
		t.Fatalf("replay ack = %q", fixture.acknowledgements[1])
	}
}

// TestResultCoordinatorRejectsUnknownAndStale 验证 unknown kind 与失效 binding 持久拒绝。
func TestResultCoordinatorRejectsUnknownAndStale(t *testing.T) {
	t.Parallel()
	for name, configure := range map[string]func(*resultFixture, *ResultProposal){
		"unknown-kind": func(_ *resultFixture, proposal *ResultProposal) {
			proposal.Kind = "simulation.unknown.v1"
			proposal.ProposalFingerprint = proposal.CanonicalFingerprint()
		},
		"stale-binding": func(fixture *resultFixture, _ *ResultProposal) {
			fixture.bound = false
		},
		"invalid-tick-range": func(_ *resultFixture, proposal *ResultProposal) {
			proposal.TickStart = 2
			proposal.ProposalFingerprint = proposal.CanonicalFingerprint()
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newResultFixture(t)
			proposal := testResultProposal(t, LifecycleSummaryKind, "sresult_"+strings.ReplaceAll(name, "-", ""))
			configure(fixture, &proposal)
			store := &memoryReceiptStore{receipts: make(map[string]ResultReceipt)}
			coordinator, _ := NewResultCoordinator(store, fixture, fixture, fixture, fixture)
			if err := coordinator.Process(context.Background(), proposal); err != nil {
				t.Fatalf("Process: %v", err)
			}
			receipt := store.receipts[proposal.ResultID]
			if receipt.Decision.Disposition != ResultDispositionRejected ||
				fixture.acknowledgements[0] != "rejected" {
				t.Fatalf("receipt/ack = %#v/%#v", receipt, fixture.acknowledgements)
			}
		})
	}
}

// TestResultCoordinatorConflictingResultID 验证同 ResultID 不同 fingerprint 不会被 ack。
func TestResultCoordinatorConflictingResultID(t *testing.T) {
	t.Parallel()
	fixture := newResultFixture(t)
	first := testResultProposal(t, LifecycleSummaryKind, "sresult_conflict1")
	store := &memoryReceiptStore{receipts: map[string]ResultReceipt{
		first.ResultID: {
			Proposal: first,
			Decision: ResultDecision{
				Disposition: ResultDispositionCommitted,
				Reason:      "accepted",
				DecidedAt:   fixture.now,
			},
		},
	}}
	changed := first
	changed.PayloadDigest, _ = NewDigest(strings.Repeat("9", 64))
	changed.ProposalFingerprint = changed.CanonicalFingerprint()
	coordinator, _ := NewResultCoordinator(store, fixture, fixture, fixture, fixture)
	if err := coordinator.Process(context.Background(), changed); err == nil {
		t.Fatal("conflicting ResultID was accepted")
	}
	if len(fixture.acknowledgements) != 0 {
		t.Fatal("conflicting result was acknowledged")
	}
}

// TestResultCoordinatorRecoversAckLoss 验证 receipt 已提交但 ack 丢失后只重放既有裁决。
func TestResultCoordinatorRecoversAckLoss(t *testing.T) {
	t.Parallel()
	fixture := newResultFixture(t)
	fixture.acknowledgeErr = errors.New("ack response lost")
	store := &memoryReceiptStore{receipts: make(map[string]ResultReceipt)}
	coordinator, _ := NewResultCoordinator(store, fixture, fixture, fixture, fixture)
	proposal := testResultProposal(t, LifecycleSummaryKind, "sresult_ackloss")
	if err := coordinator.Process(context.Background(), proposal); err == nil {
		t.Fatal("ack loss was reported as success")
	}
	if receipt, ok := store.receipts[proposal.ResultID]; !ok ||
		receipt.Decision.Disposition != ResultDispositionCommitted {
		t.Fatal("ack loss discarded committed receipt")
	}
	fixture.acknowledgeErr = nil
	if err := coordinator.Process(context.Background(), proposal); err != nil {
		t.Fatalf("replay after ack loss: %v", err)
	}
	if len(fixture.acknowledgements) != 1 || fixture.acknowledgements[0] != "replayed" {
		t.Fatalf("acknowledgements = %#v", fixture.acknowledgements)
	}
}

// TestResultCoordinatorRejectsMalformedProposal 验证结构不完整的 proposal 不进入 receipt 或 ack。
func TestResultCoordinatorRejectsMalformedProposal(t *testing.T) {
	t.Parallel()
	fixture := newResultFixture(t)
	store := &memoryReceiptStore{receipts: make(map[string]ResultReceipt)}
	coordinator, _ := NewResultCoordinator(store, fixture, fixture, fixture, fixture)
	proposal := testResultProposal(t, LifecycleSummaryKind, "sresult_malformed")
	proposal.PayloadDigest = ""
	if err := coordinator.Process(context.Background(), proposal); err == nil {
		t.Fatal("malformed proposal was accepted")
	}
	if len(store.receipts) != 0 || len(fixture.acknowledgements) != 0 {
		t.Fatal("malformed proposal reached receipt or ack")
	}
}

// TestResultCoordinatorRejectsForgedFingerprint 验证 child 不能修改 immutable 字段并复用旧 fingerprint。
func TestResultCoordinatorRejectsForgedFingerprint(t *testing.T) {
	t.Parallel()
	fixture := newResultFixture(t)
	store := &memoryReceiptStore{receipts: make(map[string]ResultReceipt)}
	coordinator, _ := NewResultCoordinator(store, fixture, fixture, fixture, fixture)
	proposal := testResultProposal(t, LifecycleSummaryKind, "sresult_forged")
	proposal.TickEnd++
	if err := coordinator.Process(context.Background(), proposal); err == nil {
		t.Fatal("forged proposal fingerprint was accepted")
	}
	if len(store.receipts) != 0 || len(fixture.acknowledgements) != 0 {
		t.Fatal("forged proposal reached receipt or ack")
	}
}

// newResultFixture 创建 active、lease-valid exact placement。
func newResultFixture(t *testing.T) *resultFixture {
	t.Helper()
	starting := testStartingSnapshot(t, "winst_resulttest1", 7, 9)
	now := starting.CreatedAt().Add(time.Second)
	active, err := starting.Activate(now)
	if err != nil {
		t.Fatal(err)
	}
	return &resultFixture{
		stamp:   active.Stamp(),
		current: active,
		bound:   true,
		now:     now,
	}
}

// testResultProposal 返回完整 immutable proposal。
func testResultProposal(t *testing.T, kind string, resultID string) ResultProposal {
	t.Helper()
	digest := func(value string) Digest {
		result, err := NewDigest(strings.Repeat(value, 64))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	instanceID, _ := NewSimulationInstanceID("sinst_00112233445566778899aabbccddeeff")
	proposal := ResultProposal{
		ResultID:              resultID,
		Kind:                  kind,
		AssignmentFingerprint: digest("1"),
		InstanceID:            instanceID,
		TickStart:             1,
		TickEnd:               2,
		PayloadDigest:         digest("2"),
		EvidenceDigest:        digest("3"),
	}
	proposal.ProposalFingerprint = proposal.CanonicalFingerprint()
	if err := proposal.Validate(); err != nil {
		t.Fatal(err)
	}
	return proposal
}
