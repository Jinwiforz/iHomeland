package battleentry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/battleticket"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/simulationcontrol"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
)

func TestIssueDerivesOwnerAndVisitorRoleFromAuthority(t *testing.T) {
	fixture := newBattleEntryFixture(t)
	ownerResult, err := fixture.service.Issue(context.Background(), fixture.ownerSession,
		Target{Kind: TargetKindOwnWorld}, "owner-request-key-0001")
	if err != nil {
		t.Fatal(err)
	}
	if !ownerResult.Valid() || ownerResult.Role != battleticket.RoleOwner ||
		ownerResult.TargetKind != TargetKindOwnWorld ||
		ownerResult.Endpoint != fixture.endpoint {
		t.Fatal("own-world result did not preserve authority projection")
	}

	visitor := newAuthenticatedSession(t, "acc_visitor", "ply_visitor", fixture.clock.now)
	fixture.visits.assignment = fixture.assignment.Stamp()
	visitorResult, err := fixture.service.Issue(context.Background(), visitor,
		Target{Kind: TargetKindVisitWorld, VisitSessionID: fixture.visitID},
		"visitor-request-key-0001")
	if err != nil {
		t.Fatal(err)
	}
	if !visitorResult.Valid() || visitorResult.Role != battleticket.RoleVisitor ||
		visitorResult.TargetKind != TargetKindVisitWorld ||
		visitorResult.TargetRevision != fixture.target.Revision {
		t.Fatal("visit-world result did not preserve authority projection")
	}
	if fixture.visits.mutationCount.Load() != 0 {
		t.Fatal("battle admission must not mutate VisitSession")
	}
}

func TestConcurrentNinthActorFailsClosedWithoutVisitMutation(t *testing.T) {
	fixture := newBattleEntryFixture(t)
	fixture.visits.assignment = fixture.assignment.Stamp()
	const actorCount = simulationcontrol.QualifiedActorCapacity + 1
	start := make(chan struct{})
	results := make(chan error, actorCount)
	authenticated := make([]session.AuthenticatedSession, actorCount)
	for index := 0; index < actorCount; index++ {
		authenticated[index] = newAuthenticatedSession(t,
			fmt.Sprintf("acc_visitor_%d", index),
			fmt.Sprintf("ply_visitor%d", index), fixture.clock.now)
	}
	for index := 0; index < actorCount; index++ {
		index := index
		go func() {
			<-start
			_, err := fixture.service.Issue(context.Background(), authenticated[index],
				Target{Kind: TargetKindVisitWorld, VisitSessionID: fixture.visitID},
				fmt.Sprintf("capacity-request-%04d", index))
			results <- err
		}()
	}
	close(start)
	successes, capacityFailures := 0, 0
	for range actorCount {
		err := <-results
		switch battleticket.ErrorCodeOf(err) {
		case battleticket.ErrorCodeUnspecified:
			if err != nil {
				t.Fatalf("unexpected admission failure: %v", err)
			}
			successes++
		case battleticket.ErrorCodeCapacityExceeded:
			capacityFailures++
		default:
			t.Fatalf("unexpected stable failure: %v (cause: %v)", err, errors.Unwrap(err))
		}
	}
	if successes != simulationcontrol.QualifiedActorCapacity || capacityFailures != 1 {
		t.Fatalf("expected 8 admitted and one capacity rejection, got success=%d capacity=%d", successes, capacityFailures)
	}
	if fixture.capacity.count() != simulationcontrol.QualifiedActorCapacity {
		t.Fatalf("expected exactly 8 retained reservations, got %d", fixture.capacity.count())
	}
	if fixture.visits.mutationCount.Load() != 0 || fixture.visits.revision != visitsession.Revision(7) {
		t.Fatal("capacity rejection changed VisitSession state")
	}
}

func TestAssignmentTargetReplacementReleasesReservationAndReturnsNoCredential(t *testing.T) {
	fixture := newBattleEntryFixture(t)
	replacement := fixture.target
	replacement.Revision++
	replacement.InstanceID, _ = simulationcontrol.NewSimulationInstanceID("sinst_" + strings.Repeat("b", 32))
	fixture.targets.replacement = replacement

	result, err := fixture.service.Issue(context.Background(), fixture.ownerSession,
		Target{Kind: TargetKindOwnWorld}, "replacement-key-0001")
	if battleticket.ErrorCodeOf(err) != battleticket.ErrorCodeTargetStale {
		t.Fatalf("expected target stale, got %v", err)
	}
	if result.Valid() || result.TicketSecret.Valid() {
		t.Fatal("stale target returned a usable credential")
	}
	if fixture.capacity.count() != 0 || fixture.capacity.releaseCount.Load() != 1 {
		t.Fatal("stale target did not release exact capacity reservation")
	}
}

func TestIssuerCommitUnknownReleasesReservationAndReturnsNoCredential(t *testing.T) {
	fixture := newBattleEntryFixture(t)
	fixture.service.issuer = commitUnknownIssuer{}
	result, err := fixture.service.Issue(context.Background(), fixture.ownerSession,
		Target{Kind: TargetKindOwnWorld}, "commit-unknown-key-0001")
	if battleticket.ErrorCodeOf(err) != battleticket.ErrorCodeCommitUnknown {
		t.Fatalf("expected commit unknown, got %v", err)
	}
	if result.Valid() || result.TicketID.Valid() || result.TicketSecret.Valid() {
		t.Fatal("commit-unknown returned candidate credential material")
	}
	if fixture.capacity.count() != 0 || fixture.capacity.releaseCount.Load() != 1 {
		t.Fatal("commit-unknown did not revoke unreturned actor reservation")
	}
}

func TestChildInstallResponseLossQueriesExactStatusAndReplaysCredential(t *testing.T) {
	fixture := newBattleEntryFixture(t)
	fixture.child.responseLossOnce = true
	first, err := fixture.service.Issue(context.Background(), fixture.ownerSession,
		Target{Kind: TargetKindOwnWorld}, "child-response-loss-key-0001")
	if err != nil || !first.Valid() {
		t.Fatalf("response-loss recovery failed: result=%v err=%v", first, err)
	}
	replayed, err := fixture.service.Issue(context.Background(), fixture.ownerSession,
		Target{Kind: TargetKindOwnWorld}, "child-response-loss-key-0001")
	if err != nil || !replayed.Valid() ||
		replayed.TicketID != first.TicketID ||
		replayed.TicketSecret != first.TicketSecret ||
		!replayed.ExpiresAt.Equal(first.ExpiresAt) {
		t.Fatalf("response-loss replay drifted: result=%v err=%v", replayed, err)
	}
	if fixture.capacity.count() != 1 || fixture.child.revokeCount.Load() != 0 {
		t.Fatal("successful response-loss replay changed capacity or revoked ticket")
	}
}

func TestPostInstallTargetReplacementRevokesOnlyOldExactTicket(t *testing.T) {
	fixture := newBattleEntryFixture(t)
	replacement := fixture.target
	replacement.Revision++
	replacement.InstanceID, _ = simulationcontrol.NewSimulationInstanceID(
		"sinst_" + strings.Repeat("c", 32))
	fixture.targets.replacement = replacement
	fixture.targets.replaceAt = 3

	result, err := fixture.service.Issue(context.Background(), fixture.ownerSession,
		Target{Kind: TargetKindOwnWorld}, "post-install-replacement-0001")
	if battleticket.ErrorCodeOf(err) != battleticket.ErrorCodeTargetStale {
		t.Fatalf("expected target stale after install, got %v", err)
	}
	if result.Valid() || fixture.capacity.count() != 0 ||
		fixture.child.revokeCount.Load() != 1 {
		t.Fatal("post-install replacement returned credential or leaked capacity")
	}
	if !sameTarget(fixture.child.lastRevokeTarget, fixture.target) ||
		sameTarget(fixture.child.lastRevokeTarget, replacement) {
		t.Fatal("cleanup revoke escaped old exact target")
	}
}

func TestCancellationAfterInstallRevokesBeforeReturning(t *testing.T) {
	fixture := newBattleEntryFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	fixture.child.afterInstall = cancel
	result, err := fixture.service.Issue(ctx, fixture.ownerSession,
		Target{Kind: TargetKindOwnWorld}, "cancel-after-install-key-0001")
	if battleticket.ErrorCodeOf(err) != battleticket.ErrorCodeDependency {
		t.Fatalf("expected bounded cancellation failure, got %v", err)
	}
	if result.Valid() || fixture.capacity.count() != 0 ||
		fixture.child.revokeCount.Load() != 1 {
		t.Fatal("cancelled install returned credential or leaked exact reservation")
	}
}

type battleEntryFixture struct {
	clock        *fixedClock
	ownerSession session.AuthenticatedSession
	world        personalworld.Snapshot
	assignment   placement.AssignmentSnapshot
	target       simulationcontrol.SimulationTarget
	endpoint     battleticket.Endpoint
	visitID      visitsession.VisitSessionID
	visits       *fakeVisitRoles
	targets      *fakeTargets
	capacity     *memoryCapacity
	child        *memoryChildTickets
	service      *Service
}

func newBattleEntryFixture(t *testing.T) battleEntryFixture {
	t.Helper()
	now := time.Date(2026, 7, 24, 1, 2, 3, 0, time.UTC)
	ownerID, _ := account.NewPlayerID("ply_owner")
	worldID, _ := personalworld.NewPersonalWorldID("pworld_battle")
	world, _ := personalworld.NewSnapshot(worldID, ownerID, personalworld.LifecycleActive,
		personalworld.Revision(1), now.Add(-time.Hour))
	instanceID, _ := placement.NewWorldInstanceID("winst_battle")
	runtimeNodeID, _ := placement.NewRuntimeNodeID("rnode_battle")
	stamp, _ := placement.NewAssignmentStamp(worldID, instanceID, runtimeNodeID,
		placement.AssignmentGeneration(2), placement.FencingToken(3))
	assignment, _ := placement.NewAssignmentSnapshot(stamp, placement.PhaseActive,
		now.Add(-time.Minute), now.Add(time.Minute), now)
	simulationNodeID, _ := simulationcontrol.NewSimulationNodeID("snode_battle")
	simulationInstanceID, _ := simulationcontrol.NewSimulationInstanceID("sinst_" + strings.Repeat("a", 32))
	digest := mustSimulationDigest(t, "a")
	target := simulationcontrol.SimulationTarget{
		RuntimeNodeID: runtimeNodeID, NodeID: simulationNodeID, InstanceID: simulationInstanceID,
		AssignmentFingerprint: digest, MappingGeneration: 4, ModelManifest: digest,
		ProfileManifest: digest, ConfigIdentity: digest,
		ActorCapacity: simulationcontrol.QualifiedActorCapacity, Revision: 5,
	}
	endpoint, _ := battleticket.NewEndpoint("battle.example.invalid", 58445)
	wireIdentity, _ := battleticket.ParseDigestHex(strings.Repeat("b", 64))
	visitID, _ := visitsession.NewVisitSessionID("vses_battle")
	clock := &fixedClock{now: now}
	store := newMemoryTicketStore()
	deriver, _ := battleticket.NewDeriver([]byte(strings.Repeat("k", 32)))
	policy, _ := battleticket.NewPolicy(30*time.Second, time.Hour)
	issuer, _ := battleticket.NewIssuer(store, deriver, policy)
	visits := &fakeVisitRoles{visitID: visitID, revision: visitsession.Revision(7),
		deadline: now.Add(45 * time.Second)}
	targets := &fakeTargets{target: target}
	capacity := newMemoryCapacity()
	child := newMemoryChildTickets()
	service, err := NewService(
		&fakeWorlds{snapshot: world}, &fakeAssignments{snapshot: assignment},
		visits, targets, fakeBattleEndpoints{endpoint: endpoint}, capacity,
		issuer, child, clock, 30*time.Second, wireIdentity,
	)
	if err != nil {
		t.Fatal(err)
	}
	return battleEntryFixture{
		clock: clock, ownerSession: newAuthenticatedSession(t, "acc_owner", ownerID.String(), now),
		world: world, assignment: assignment, target: target, endpoint: endpoint,
		visitID: visitID, visits: visits, targets: targets, capacity: capacity,
		child: child, service: service,
	}
}

type fixedClock struct{ now time.Time }

func (clock *fixedClock) Now() time.Time { return clock.now }

type fakeWorlds struct{ snapshot personalworld.Snapshot }

func (worlds *fakeWorlds) EnsurePrimary(_ context.Context, ownerID account.PlayerID) (personalworld.Snapshot, error) {
	if worlds.snapshot.OwnerID() != ownerID {
		return personalworld.Snapshot{}, errors.New("owner mismatch")
	}
	return worlds.snapshot, nil
}

type fakeAssignments struct{ snapshot placement.AssignmentSnapshot }

func (assignments *fakeAssignments) Resolve(context.Context, personalworld.PersonalWorldID, time.Time) (placement.AssignmentSnapshot, placement.ResolveOutcome, error) {
	return assignments.snapshot, placement.ResolveOutcomeFound, nil
}

type fakeVisitRoles struct {
	visitID       visitsession.VisitSessionID
	assignment    placement.AssignmentStamp
	revision      visitsession.Revision
	deadline      time.Time
	mutationCount atomic.Int32
}

func (roles *fakeVisitRoles) ResolveBattleAuthority(_ context.Context, authenticated session.AuthenticatedSession, visitID visitsession.VisitSessionID) (VisitAuthority, error) {
	if visitID != roles.visitID {
		return VisitAuthority{}, errors.New("visit mismatch")
	}
	auth := authenticated.AuthContext()
	playerID, _ := account.NewPlayerID(auth.Principal().PlayerID())
	intent, err := visitsession.HydrateAdmissionIntent(visitID, playerID, auth.SessionID(),
		auth.Epoch(), roles.assignment, roles.deadline)
	if err != nil {
		return VisitAuthority{}, err
	}
	return VisitAuthority{Intent: intent, Revision: roles.revision}, nil
}

type fakeTargets struct {
	target      simulationcontrol.SimulationTarget
	replacement simulationcontrol.SimulationTarget
	replaceAt   int32
	calls       atomic.Int32
}

func (targets *fakeTargets) ResolveSimulationTarget(context.Context, placement.AssignmentStamp) (simulationcontrol.SimulationTarget, bool, error) {
	call := targets.calls.Add(1)
	replaceAt := targets.replaceAt
	if replaceAt == 0 {
		replaceAt = 2
	}
	if call >= replaceAt && targets.replacement.Validate() == nil {
		return targets.replacement, true, nil
	}
	return targets.target, true, nil
}

type fakeBattleEndpoints struct{ endpoint battleticket.Endpoint }

func (provider fakeBattleEndpoints) EndpointFor(context.Context, simulationcontrol.SimulationTarget) (battleticket.Endpoint, error) {
	return provider.endpoint, nil
}

type memoryCapacity struct {
	mutex        sync.Mutex
	requests     map[string]ReservationRequest
	reservations map[string]Reservation
	used         [simulationcontrol.QualifiedActorCapacity]bool
	releaseCount atomic.Int32
}

func newMemoryCapacity() *memoryCapacity {
	return &memoryCapacity{
		requests: make(map[string]ReservationRequest), reservations: make(map[string]Reservation),
	}
}

func (capacity *memoryCapacity) Reserve(_ context.Context, request ReservationRequest) (Reservation, error) {
	if !request.Valid() {
		return Reservation{}, battleticket.NewAdmissionError("capacity",
			battleticket.ErrorCodeDependencyDefect, nil)
	}
	capacity.mutex.Lock()
	defer capacity.mutex.Unlock()
	key := request.IssueID.Value()
	if existing, ok := capacity.reservations[key]; ok {
		original := capacity.requests[key]
		if original.PlayerID != request.PlayerID || original.Role != request.Role ||
			!sameTarget(original.Target, request.Target) {
			return Reservation{}, battleticket.NewAdmissionError("capacity",
				battleticket.ErrorCodeIdempotencyConflict, nil)
		}
		return existing, nil
	}
	for index := range capacity.used {
		if capacity.used[index] {
			continue
		}
		slot, _ := battleticket.NewActorSlot(uint8(index))
		reservation, _ := NewReservation(request.IssueID, request.Target, slot)
		capacity.used[index] = true
		capacity.requests[key] = request
		capacity.reservations[key] = reservation
		return reservation, nil
	}
	return Reservation{}, battleticket.NewAdmissionError("capacity",
		battleticket.ErrorCodeCapacityExceeded, nil)
}

func (capacity *memoryCapacity) Release(_ context.Context, reservation Reservation) error {
	capacity.mutex.Lock()
	defer capacity.mutex.Unlock()
	key := reservation.IssueID().Value()
	current, ok := capacity.reservations[key]
	if !ok || !sameTarget(current.Target(), reservation.Target()) ||
		current.Slot() != reservation.Slot() {
		return errors.New("reservation mismatch")
	}
	delete(capacity.requests, key)
	delete(capacity.reservations, key)
	capacity.used[current.Slot().Index()] = false
	capacity.releaseCount.Add(1)
	return nil
}

func (capacity *memoryCapacity) count() int {
	capacity.mutex.Lock()
	defer capacity.mutex.Unlock()
	return len(capacity.reservations)
}

type commitUnknownIssuer struct{}

func (commitUnknownIssuer) Issue(context.Context, battleticket.Facts, time.Time) (battleticket.IssueResult, error) {
	return battleticket.IssueResult{}, battleticket.NewAdmissionError("issue",
		battleticket.ErrorCodeCommitUnknown, errors.New("sentinel"))
}

type memoryChildTickets struct {
	mutex            sync.Mutex
	receipts         map[string]ChildTicketReceipt
	responseLossOnce bool
	afterInstall     func()
	lastRevokeTarget simulationcontrol.SimulationTarget
	revokeCount      atomic.Int32
}

func newMemoryChildTickets() *memoryChildTickets {
	return &memoryChildTickets{receipts: make(map[string]ChildTicketReceipt)}
}

func (child *memoryChildTickets) Install(_ context.Context, target simulationcontrol.SimulationTarget, material battleticket.Material) (ChildTicketReceipt, error) {
	child.mutex.Lock()
	defer child.mutex.Unlock()
	key := material.Binding().TicketID().Value()
	if existing, ok := child.receipts[key]; ok {
		if !existing.BindingFingerprint.Equal(material.Fingerprint()) ||
			!sameTarget(existing.Target, target) ||
			existing.Slot != material.Binding().Facts().ActorSlot {
			return ChildTicketReceipt{}, errors.New("child install replay conflicts")
		}
		if child.responseLossOnce {
			child.responseLossOnce = false
			return ChildTicketReceipt{}, context.DeadlineExceeded
		}
		return existing, nil
	}
	receipt := ChildTicketReceipt{
		TicketID: material.Binding().TicketID(), BindingFingerprint: material.Fingerprint(),
		Target: target, Slot: material.Binding().Facts().ActorSlot, State: ChildTicketStateInstalled,
	}
	child.receipts[key] = receipt
	if child.afterInstall != nil {
		action := child.afterInstall
		child.afterInstall = nil
		action()
	}
	if child.responseLossOnce {
		child.responseLossOnce = false
		return ChildTicketReceipt{}, context.DeadlineExceeded
	}
	return receipt, nil
}

func (child *memoryChildTickets) Status(_ context.Context, target simulationcontrol.SimulationTarget, binding battleticket.Binding) (ChildTicketReceipt, error) {
	child.mutex.Lock()
	defer child.mutex.Unlock()
	receipt, ok := child.receipts[binding.TicketID().Value()]
	if !ok || !receipt.BindingFingerprint.Equal(binding.Fingerprint()) ||
		!sameTarget(receipt.Target, target) {
		return ChildTicketReceipt{
			TicketID: binding.TicketID(), BindingFingerprint: binding.Fingerprint(),
			Target: target, State: ChildTicketStateMissing,
		}, nil
	}
	return receipt, nil
}

func (child *memoryChildTickets) Revoke(_ context.Context, target simulationcontrol.SimulationTarget, binding battleticket.Binding) (ChildTicketReceipt, error) {
	child.mutex.Lock()
	defer child.mutex.Unlock()
	receipt, ok := child.receipts[binding.TicketID().Value()]
	if !ok || !receipt.BindingFingerprint.Equal(binding.Fingerprint()) ||
		!sameTarget(receipt.Target, target) {
		return ChildTicketReceipt{}, errors.New("child revoke target is missing")
	}
	if receipt.State == ChildTicketStateInstalled || receipt.State == ChildTicketStateConsumed {
		receipt.State = ChildTicketStateRevoked
		child.receipts[binding.TicketID().Value()] = receipt
	}
	child.lastRevokeTarget = target
	child.revokeCount.Add(1)
	return receipt, nil
}

type memoryTicketStore struct {
	mutex   sync.Mutex
	records map[string]battleticket.IssueRecord
}

func newMemoryTicketStore() *memoryTicketStore {
	return &memoryTicketStore{records: make(map[string]battleticket.IssueRecord)}
}

func (store *memoryTicketStore) Resolve(_ context.Context, issueID battleticket.IssueID) (battleticket.IssueSnapshot, battleticket.ResolveOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	record, ok := store.records[issueID.Value()]
	if !ok {
		return battleticket.IssueSnapshot{}, battleticket.ResolveOutcomeNotFound, nil
	}
	return battleticket.IssueSnapshot{
		Binding: record.Binding, Fingerprint: record.Fingerprint,
		SecretDigest: record.SecretDigest, ProofDigest: record.ProofDigest,
	}, battleticket.ResolveOutcomeFound, nil
}

func (store *memoryTicketStore) Issue(_ context.Context, record battleticket.IssueRecord, _ time.Time) (battleticket.IssueOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	key := record.Binding.Facts().IssueID.Value()
	if existing, ok := store.records[key]; ok {
		if existing.Binding.Equal(record.Binding) &&
			existing.Fingerprint.Equal(record.Fingerprint) &&
			existing.SecretDigest.Equal(record.SecretDigest) &&
			existing.ProofDigest.Equal(record.ProofDigest) {
			return battleticket.IssueOutcomeReplay, nil
		}
		return battleticket.IssueOutcomeIdempotencyConflict, nil
	}
	store.records[key] = record
	return battleticket.IssueOutcomeCreated, nil
}

type authStore struct {
	mutex  sync.Mutex
	bundle session.SessionBundle
}

func (store *authStore) Create(_ context.Context, bundle session.SessionBundle) (session.StoreOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.bundle = bundle
	return session.StoreOutcomeApplied, nil
}

func (store *authStore) ResolveAccess(_ context.Context, digest session.Digest, _ time.Time) (session.AuthSnapshot, session.StoreOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if digest != store.bundle.Access.Digest {
		return session.AuthSnapshot{}, session.StoreOutcomeNotFound, nil
	}
	return session.AuthSnapshot{
		Principal: store.bundle.Session.Principal, SessionID: store.bundle.Session.ID,
		Epoch: store.bundle.Session.Epoch, AccessExpiresAt: store.bundle.Access.ExpiresAt,
		SessionExpiresAt: store.bundle.Session.ExpiresAt,
	}, session.StoreOutcomeApplied, nil
}

func (*authStore) RotateRefresh(context.Context, session.Rotation) (session.AuthSnapshot, session.Invalidation, session.StoreOutcome, error) {
	return session.AuthSnapshot{}, session.Invalidation{}, session.StoreOutcomeNotFound, nil
}

func (*authStore) IssueTicket(context.Context, session.TicketRecord, time.Time) (session.StoreOutcome, error) {
	return session.StoreOutcomeNotFound, nil
}

func (*authStore) ConsumeTicket(context.Context, session.Digest, session.Channel, session.Endpoint, time.Time) (session.AuthSnapshot, session.StoreOutcome, error) {
	return session.AuthSnapshot{}, session.StoreOutcomeNotFound, nil
}

func (*authStore) InvalidateSession(context.Context, session.SessionID, session.InvalidationReason) (session.Invalidation, session.StoreOutcome, error) {
	return session.Invalidation{}, session.StoreOutcomeNotFound, nil
}

func (*authStore) InvalidatePrincipal(context.Context, session.Principal, session.InvalidationReason) ([]session.Invalidation, error) {
	return nil, nil
}

type authIDs struct{}

func (authIDs) NewID() (string, error) { return "battleEntryFixture", nil }

func newAuthenticatedSession(t *testing.T, accountID string, playerID string, now time.Time) session.AuthenticatedSession {
	t.Helper()
	principal, _ := session.NewPrincipal(accountID, playerID)
	store := &authStore{}
	wss, _ := session.NewEndpoint(session.ChannelWSS, "control.example.invalid", 443)
	tcp, _ := session.NewEndpoint(session.ChannelTLSTCP, "game.example.invalid", 4433)
	endpoints, _ := session.NewStaticEndpointProvider(wss, tcp)
	policy, _ := session.NewPolicy(time.Second, time.Minute, time.Hour, time.Hour)
	service, err := session.NewService(store, endpoints, session.NoActiveRealtimeConnections{},
		&fixedClock{now: now}, authIDs{}, session.CryptoSecretGenerator{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateSession(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	authenticated, err := service.AuthenticateHTTPS(context.Background(), created.Tokens.Access.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	return authenticated
}

func mustSimulationDigest(t *testing.T, character string) simulationcontrol.Digest {
	t.Helper()
	digest, err := simulationcontrol.NewDigest(strings.Repeat(character, 64))
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
