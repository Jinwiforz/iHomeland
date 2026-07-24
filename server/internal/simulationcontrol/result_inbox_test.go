package simulationcontrol

import "testing"

func TestProposalInboxRetainsHeadUntilExplicitDiscard(t *testing.T) {
	inbox := NewProposalInbox()
	proposal := testResultProposal(t, LifecycleSummaryKind, "sresult_inbox_1")
	if err := inbox.OfferResult(proposal); err != nil {
		t.Fatalf("OfferResult: %v", err)
	}

	first, found := inbox.Peek()
	if !found || first != proposal || inbox.Len() != 1 {
		t.Fatal("Peek transferred ownership of an uncommitted proposal")
	}
	retried, found := inbox.Peek()
	if !found || retried != proposal || inbox.Len() != 1 {
		t.Fatal("proposal was not retained for a bounded retry")
	}
	if err := inbox.Discard(proposal); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if _, found := inbox.Peek(); found || inbox.Len() != 0 {
		t.Fatal("terminal proposal remained queued")
	}
}

func TestProposalInboxRejectsOutOfOrderDiscard(t *testing.T) {
	inbox := NewProposalInbox()
	first := testResultProposal(t, LifecycleSummaryKind, "sresult_inbox_first")
	second := testResultProposal(t, LifecycleSummaryKind, "sresult_inbox_second")
	if err := inbox.OfferResult(first); err != nil {
		t.Fatalf("OfferResult(first): %v", err)
	}
	if err := inbox.OfferResult(second); err != nil {
		t.Fatalf("OfferResult(second): %v", err)
	}

	if err := inbox.Discard(second); err == nil {
		t.Fatal("out-of-order discard was accepted")
	}
	head, found := inbox.Peek()
	if !found || head != first || inbox.Len() != 2 {
		t.Fatal("failed discard mutated the queue")
	}
}

func TestProposalInboxDeduplicatesExactReplayAndRejectsConflict(t *testing.T) {
	inbox := NewProposalInbox()
	proposal := testResultProposal(t, LifecycleSummaryKind, "sresult_inbox_replay")
	if err := inbox.OfferResult(proposal); err != nil {
		t.Fatalf("OfferResult: %v", err)
	}
	if err := inbox.OfferResult(proposal); err != nil {
		t.Fatalf("OfferResult(replay): %v", err)
	}
	if inbox.Len() != 1 {
		t.Fatal("exact replay consumed additional inbox capacity")
	}

	conflict := proposal
	conflict.TickEnd++
	conflict.ProposalFingerprint = conflict.CanonicalFingerprint()
	if err := inbox.OfferResult(conflict); err == nil {
		t.Fatal("conflicting ResultID replay was accepted")
	}
	if inbox.Len() != 1 {
		t.Fatal("conflicting replay mutated the inbox")
	}
}
