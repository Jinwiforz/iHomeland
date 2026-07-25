package battleticket

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestIssuerReplaysFirstDeterministicMaterial(t *testing.T) {
	facts := validFacts(t)
	store := &issuerMemoryStore{}
	issuer := newTestIssuer(t, store)
	first, err := issuer.Issue(context.Background(), facts, facts.IssuedAt)
	if err != nil {
		t.Fatal(err)
	}
	retry := facts
	retry.IssuedAt = retry.IssuedAt.Add(time.Second)
	retry.ExpiresAt = retry.ExpiresAt.Add(time.Second)
	replayed, err := issuer.Issue(context.Background(), retry, retry.IssuedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Valid() || !replayed.Valid() || !replayed.Replayed() ||
		!first.Material().Binding().Equal(replayed.Material().Binding()) ||
		first.Material().Secret().Value() != replayed.Material().Secret().Value() {
		t.Fatal("response-loss retry did not return the first frozen material")
	}
}

func TestIssuerCommitUnknownReturnsNoCandidateMaterial(t *testing.T) {
	facts := validFacts(t)
	issuer := newTestIssuer(t, commitUnknownStore{})
	result, err := issuer.Issue(context.Background(), facts, facts.IssuedAt)
	if ErrorCodeOf(err) != ErrorCodeCommitUnknown {
		t.Fatalf("expected commit unknown, got %v", err)
	}
	if result.Valid() || result.Material().Valid() {
		t.Fatal("commit-unknown exposed candidate material")
	}
}

func newTestIssuer(t *testing.T, store Store) *Issuer {
	t.Helper()
	deriver, err := NewDeriver([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewPolicy(30*time.Second, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := NewIssuer(store, deriver, policy)
	if err != nil {
		t.Fatal(err)
	}
	return issuer
}

type issuerMemoryStore struct {
	mutex  sync.Mutex
	record IssueRecord
}

func (store *issuerMemoryStore) Resolve(context.Context, IssueID) (IssueSnapshot, ResolveOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if !store.record.Valid() {
		return IssueSnapshot{}, ResolveOutcomeNotFound, nil
	}
	return IssueSnapshot{
		Binding: store.record.Binding, Fingerprint: store.record.Fingerprint,
		SecretDigest: store.record.SecretDigest, ProofDigest: store.record.ProofDigest,
	}, ResolveOutcomeFound, nil
}

func (store *issuerMemoryStore) Issue(_ context.Context, record IssueRecord, _ time.Time) (IssueOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.record.Valid() {
		return IssueOutcomeReplay, nil
	}
	store.record = record
	return IssueOutcomeCreated, nil
}

type commitUnknownStore struct{}

func (commitUnknownStore) Resolve(context.Context, IssueID) (IssueSnapshot, ResolveOutcome, error) {
	return IssueSnapshot{}, ResolveOutcomeNotFound, nil
}

func (commitUnknownStore) Issue(context.Context, IssueRecord, time.Time) (IssueOutcome, error) {
	return IssueOutcomeCommitUnknown, errors.New("sentinel commit state unknown")
}
