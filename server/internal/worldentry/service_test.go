package worldentry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
	"github.com/jinwiforz/ihomeland/server/internal/worldadmission"
)

// TestWorldEntryValuesRedactDefaultFormatting 验证公开投影不会意外进入普通日志。
func TestWorldEntryValuesRedactDefaultFormatting(t *testing.T) {
	fixture := newWorldEntryFixture(t)
	values := []any{
		AssignmentProjection{WorldID: fixture.assignment.WorldID(), InstanceID: fixture.assignment.InstanceID().String(), Generation: uint64(fixture.assignment.Generation())},
		BootstrapResult{World: fixture.world},
		ReservationResult{VisitSessionID: mustVisitSessionID(t, "vses_sensitive"), Revision: visitsession.Revision(2), ExpiresAt: fixture.clock.now},
		AdmissionTarget{Kind: AdmissionTargetVisitWorld, VisitSessionID: mustVisitSessionID(t, "vses_sensitive")},
	}
	for _, value := range values {
		formatted := fmt.Sprintf("%v %#v", value, value)
		if strings.Contains(formatted, "sensitive") || strings.Contains(formatted, fixture.assignment.InstanceID().String()) || formatted != redactedValue+" "+redactedValue {
			t.Fatalf("world-entry value leaked default formatting: %s", formatted)
		}
	}
}

// mustVisitSessionID 构造测试VisitSession identity。
func mustVisitSessionID(t *testing.T, value string) visitsession.VisitSessionID {
	t.Helper()
	id, err := visitsession.NewVisitSessionID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestBootstrapOwnWorldReturnsClientSafeAssignment 验证bootstrap必须激活runtime并隐藏node/fence。
func TestBootstrapOwnWorldReturnsClientSafeAssignment(t *testing.T) {
	fixture := newWorldEntryFixture(t)
	service, err := NewService(fixture.worlds, fixture.assignments, fixture.visits, fixture.admissions, fixture.endpoints, fixture.clock, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.BootstrapOwnWorld(context.Background(), fixture.authenticated)
	if err != nil || !result.Valid() || !result.Assignment.Valid() {
		t.Fatalf("bootstrap=%#v err=%v", result, err)
	}
	if result.Assignment.InstanceID != fixture.assignment.InstanceID().String() || result.Assignment.Generation != uint64(fixture.assignment.Generation()) {
		t.Fatalf("assignment projection mismatch: %#v", result.Assignment)
	}
	fixture.assignments.snapshot = placement.AssignmentSnapshot{}
	result, err = service.BootstrapOwnWorld(context.Background(), fixture.authenticated)
	if err == nil || result.Valid() {
		t.Fatalf("missing assignment bootstrap=%#v err=%v", result, err)
	}
}

// TestBootstrapObservesAssignmentAfterActivation 验证本次新建assignment不会被调用前时间误判为not-ready。
func TestBootstrapObservesAssignmentAfterActivation(t *testing.T) {
	fixture := newWorldEntryFixture(t)
	fixture.assignments.onEnsure = func() {
		fixture.clock.now = fixture.clock.now.Add(time.Millisecond)
		stamp := fixture.assignment.Stamp()
		fixture.assignment, _ = placement.NewAssignmentSnapshot(stamp, placement.PhaseActive, fixture.clock.now, fixture.clock.now.Add(time.Minute), fixture.clock.now)
		fixture.assignments.snapshot = fixture.assignment
	}
	service, err := NewService(fixture.worlds, fixture.assignments, fixture.visits, fixture.admissions, fixture.endpoints, fixture.clock, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.BootstrapOwnWorld(context.Background(), fixture.authenticated)
	if err != nil || !result.Valid() {
		t.Fatalf("new assignment bootstrap=%v err=%v", result.Valid(), err)
	}
}

// TestIdempotencyDerivationSeparatesOperationAndActor 验证相同scope稳定、跨operation和lineage分域。
func TestIdempotencyDerivationSeparatesOperationAndActor(t *testing.T) {
	fixture := newWorldEntryFixture(t)
	auth := fixture.authenticated.AuthContext()
	first := deriveIdentity("accept-visit-invite", auth, "fixture-key-0000001")
	if first != deriveIdentity("accept-visit-invite", auth, "fixture-key-0000001") {
		t.Fatal("same idempotency scope was not deterministic")
	}
	if first == deriveIdentity("issue-world-admission", auth, "fixture-key-0000001") {
		t.Fatal("operation domain separator was ignored")
	}
	other := newAuthenticatedSession(t, "acc_other", "ply_other", fixture.clock.now)
	if first == deriveIdentity("accept-visit-invite", other.AuthContext(), "fixture-key-0000001") {
		t.Fatal("actor/session scope was ignored")
	}
	if validateIdempotencyKey("invalid!key-value") == nil {
		t.Fatal("OpenAPI-forbidden idempotency character was accepted")
	}
}

// TestVisitEligibilityErrorRemainsOwnerTyped 验证HTTP映射仍能识别VisitSession稳定错误。
func TestVisitEligibilityErrorRemainsOwnerTyped(t *testing.T) {
	fixture := newWorldEntryFixture(t)
	service, err := NewService(fixture.worlds, fixture.assignments, fixture.visits, fixture.admissions, fixture.endpoints, fixture.clock, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	visitID, _ := visitsession.NewVisitSessionID("vses_fixture")
	_, err = service.IssueWorldAdmission(context.Background(), fixture.authenticated, AdmissionTarget{Kind: AdmissionTargetVisitWorld, VisitSessionID: visitID}, "fixture-admission-key-01")
	if err != errCaptured {
		t.Fatalf("VisitSession owner error was reclassified: %v", err)
	}
}

// TestAcceptAndAdmissionPassOnlyDerivedTrustedFacts 验证raw幂等key不成为owner identity且payload不能选择binding。
func TestAcceptAndAdmissionPassOnlyDerivedTrustedFacts(t *testing.T) {
	fixture := newWorldEntryFixture(t)
	service, err := NewService(fixture.worlds, fixture.assignments, fixture.visits, fixture.admissions, fixture.endpoints, fixture.clock, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	visitID, _ := visitsession.NewVisitSessionID("vses_fixture")
	inviteID, _ := visitsession.NewInviteID("vinv_fixture")
	rawKey := "fixture-accept-key-0001"
	if _, err := service.AcceptVisitInvite(context.Background(), fixture.authenticated, visitID, inviteID, visitsession.Revision(1), rawKey); !errors.Is(err, errCaptured) {
		t.Fatalf("accept error=%v", err)
	}
	if !fixture.visits.command.Valid() || fixture.visits.command.Value() == rawKey {
		t.Fatalf("raw idempotency key reached VisitSession owner: %q", fixture.visits.command.Value())
	}
	if _, err := service.IssueWorldAdmission(context.Background(), fixture.authenticated, AdmissionTarget{Kind: AdmissionTargetOwnWorld}, "fixture-admission-key-01"); !errors.Is(err, errCaptured) {
		t.Fatalf("admission error=%v", err)
	}
	binding := fixture.admissions.binding
	if !binding.Valid() || binding.PlayerID().String() != fixture.world.OwnerID().String() || binding.Role() != worldadmission.RoleOwner || binding.Purpose() != worldadmission.PurposeOwnWorld || !binding.Assignment().Equal(fixture.assignment.Stamp()) {
		t.Fatalf("derived binding=%#v", binding)
	}
}

// errCaptured 让fixture在不伪造owner成功结果的情况下证明调用已到达预期边界。
var errCaptured = errors.New("captured owner call")

// worldEntryFixture 汇总不启动backend的窄owner fakes。
type worldEntryFixture struct {
	// clock 是全部deadline共享时间。
	clock *fixedClock
	// authenticated 是Session owner创建的受信HTTP快照。
	authenticated session.AuthenticatedSession
	// world 是owner持久事实。
	world personalworld.Snapshot
	// assignment 是current active placement。
	assignment placement.AssignmentSnapshot
	// worlds 提供primary world。
	worlds *fakeWorldEnsurer
	// assignments 提供current placement。
	assignments *fakeAssignmentReader
	// visits 捕获派生command。
	visits *capturingVisitOwner
	// admissions 捕获派生issue/binding。
	admissions *capturingAdmissionIssuer
	// endpoints 是受信advertised manifest。
	endpoints *session.StaticEndpointProvider
}

// newWorldEntryFixture 构造完整规范world、assignment与authenticated session。
func newWorldEntryFixture(t *testing.T) worldEntryFixture {
	t.Helper()
	now := time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC)
	worldID, _ := personalworld.NewPersonalWorldID("pworld_fixture")
	playerID, _ := account.NewPlayerID("ply_fixture")
	world, _ := personalworld.NewSnapshot(worldID, playerID, personalworld.LifecycleActive, personalworld.Revision(1), now.Add(-time.Hour))
	instanceID, _ := placement.NewWorldInstanceID("winst_fixture")
	nodeID, _ := placement.NewRuntimeNodeID("rnode_fixture")
	stamp, _ := placement.NewAssignmentStamp(worldID, instanceID, nodeID, placement.AssignmentGeneration(2), placement.FencingToken(3))
	assignment, _ := placement.NewAssignmentSnapshot(stamp, placement.PhaseActive, now.Add(-time.Minute), now.Add(time.Minute), now)
	wss, _ := session.NewEndpoint(session.ChannelWSS, "control.example.invalid", 443)
	tcp, _ := session.NewEndpoint(session.ChannelTLSTCP, "game.example.invalid", 4433)
	endpoints, _ := session.NewStaticEndpointProvider(wss, tcp)
	clock := &fixedClock{now: now}
	return worldEntryFixture{clock: clock, authenticated: newAuthenticatedSession(t, "acc_fixture", playerID.String(), now), world: world, assignment: assignment, worlds: &fakeWorldEnsurer{snapshot: world}, assignments: &fakeAssignmentReader{snapshot: assignment, outcome: placement.ResolveOutcomeFound}, visits: &capturingVisitOwner{}, admissions: &capturingAdmissionIssuer{}, endpoints: endpoints}
}

// fixedClock 返回确定性UTC时间。
type fixedClock struct {
	// now 是fixture固定的UTC时间。
	now time.Time
}

// Now 返回当前fixture时间。
func (clock *fixedClock) Now() time.Time { return clock.now }

// fakeWorldEnsurer 返回预设primary world。
type fakeWorldEnsurer struct {
	// snapshot 是预设primary world持久事实。
	snapshot personalworld.Snapshot
}

// EnsurePrimary 返回与actor匹配的持久事实。
func (ensurer *fakeWorldEnsurer) EnsurePrimary(_ context.Context, ownerID account.PlayerID) (personalworld.Snapshot, error) {
	if ensurer.snapshot.OwnerID() != ownerID {
		return personalworld.Snapshot{}, errors.New("owner mismatch")
	}
	return ensurer.snapshot, nil
}

// fakeAssignmentReader 返回可切换的placement activation与读取结果。
type fakeAssignmentReader struct {
	// snapshot 是预设current assignment。
	snapshot placement.AssignmentSnapshot
	// outcome 是预设封闭读取决议。
	outcome placement.ResolveOutcome
	// onEnsure 模拟activation期间绝对时间和snapshot推进。
	onEnsure func()
}

// EnsureActive 返回预设activation结果；零值用于验证bootstrap fail closed。
func (reader *fakeAssignmentReader) EnsureActive(context.Context, personalworld.PersonalWorldID) (placement.AssignmentSnapshot, error) {
	if reader.onEnsure != nil {
		reader.onEnsure()
	}
	if !reader.snapshot.Valid() {
		return placement.AssignmentSnapshot{}, errors.New("assignment activation failed")
	}
	return reader.snapshot, nil
}

// Resolve 返回预设current事实。
func (reader *fakeAssignmentReader) Resolve(context.Context, personalworld.PersonalWorldID, time.Time) (placement.AssignmentSnapshot, placement.ResolveOutcome, error) {
	return reader.snapshot, reader.outcome, nil
}

// capturingVisitOwner 捕获world-entry派生的command identity。
type capturingVisitOwner struct {
	// command 保存world-entry派生后的VisitSession command identity。
	command visitsession.CommandID
}

// AcceptInviteFromHTTP 捕获输入后返回sentinel，不伪造领域结果。
func (owner *capturingVisitOwner) AcceptInviteFromHTTP(_ context.Context, _ session.AuthenticatedSession, _ visitsession.VisitSessionID, _ visitsession.InviteID, _ visitsession.Revision, command visitsession.CommandID) (visitsession.MutationResult, error) {
	owner.command = command
	return visitsession.MutationResult{}, errCaptured
}

// ResolveAdmissionEligibility 未被own-world fixture调用。
func (*capturingVisitOwner) ResolveAdmissionEligibility(context.Context, session.AuthenticatedSession, visitsession.VisitSessionID) (visitsession.AdmissionEligibility, error) {
	return visitsession.AdmissionEligibility{}, errCaptured
}

// capturingAdmissionIssuer 捕获完整binding但不创建测试credential。
type capturingAdmissionIssuer struct {
	// issue 保存world-entry派生后的WorldAdmission issue identity。
	issue worldadmission.IssueID
	// binding 保存签发前构造的完整受信绑定。
	binding worldadmission.Binding
}

// Issue 捕获签发输入后返回sentinel。
func (issuer *capturingAdmissionIssuer) Issue(_ context.Context, issue worldadmission.IssueID, binding worldadmission.Binding) (worldadmission.IssueResult, error) {
	issuer.issue, issuer.binding = issue, binding
	return worldadmission.IssueResult{}, errCaptured
}

// authStore 只为测试通过Session public API构造AuthenticatedSession。
type authStore struct {
	// mutex 保护并发fixture读写bundle。
	mutex sync.Mutex
	// bundle 保存由Session service创建的认证事实。
	bundle session.SessionBundle
}

// Create 保存session bundle。
func (store *authStore) Create(_ context.Context, bundle session.SessionBundle) (session.StoreOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.bundle = bundle
	return session.StoreOutcomeApplied, nil
}

// ResolveAccess 返回与access digest同一原子快照的两个deadline。
func (store *authStore) ResolveAccess(_ context.Context, digest session.Digest, _ time.Time) (session.AuthSnapshot, session.StoreOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if digest != store.bundle.Access.Digest {
		return session.AuthSnapshot{}, session.StoreOutcomeNotFound, nil
	}
	return session.AuthSnapshot{Principal: store.bundle.Session.Principal, SessionID: store.bundle.Session.ID, Epoch: store.bundle.Session.Epoch, AccessExpiresAt: store.bundle.Access.ExpiresAt, SessionExpiresAt: store.bundle.Session.ExpiresAt}, session.StoreOutcomeApplied, nil
}

// RotateRefresh 未被fixture调用。
func (*authStore) RotateRefresh(context.Context, session.Rotation) (session.AuthSnapshot, session.Invalidation, session.StoreOutcome, error) {
	return session.AuthSnapshot{}, session.Invalidation{}, session.StoreOutcomeNotFound, nil
}

// IssueTicket 未被fixture调用。
func (*authStore) IssueTicket(context.Context, session.TicketRecord, time.Time) (session.StoreOutcome, error) {
	return session.StoreOutcomeNotFound, nil
}

// ConsumeTicket 未被fixture调用。
func (*authStore) ConsumeTicket(context.Context, session.Digest, session.Channel, session.Endpoint, time.Time) (session.AuthSnapshot, session.StoreOutcome, error) {
	return session.AuthSnapshot{}, session.StoreOutcomeNotFound, nil
}

// InvalidateSession 未被fixture调用。
func (*authStore) InvalidateSession(context.Context, session.SessionID, session.InvalidationReason) (session.Invalidation, session.StoreOutcome, error) {
	return session.Invalidation{}, session.StoreOutcomeNotFound, nil
}

// InvalidatePrincipal 未被fixture调用。
func (*authStore) InvalidatePrincipal(context.Context, session.Principal, session.InvalidationReason) ([]session.Invalidation, error) {
	return nil, nil
}

// authIDs 返回规范安全材料。
type authIDs struct{}

// NewID 返回固定fixture材料。
func (authIDs) NewID() (string, error) { return "worldEntryFixture", nil }

// newAuthenticatedSession 通过Session service创建不可伪造的受信认证快照。
func newAuthenticatedSession(t *testing.T, accountID string, playerID string, now time.Time) session.AuthenticatedSession {
	t.Helper()
	principal, _ := session.NewPrincipal(accountID, playerID)
	store := &authStore{}
	wss, _ := session.NewEndpoint(session.ChannelWSS, "control.example.invalid", 443)
	tcp, _ := session.NewEndpoint(session.ChannelTLSTCP, "game.example.invalid", 4433)
	endpoints, _ := session.NewStaticEndpointProvider(wss, tcp)
	policy, _ := session.NewPolicy(time.Second, time.Minute, time.Hour, time.Hour)
	service, err := session.NewService(store, endpoints, session.NoActiveRealtimeConnections{}, &fixedClock{now: now}, authIDs{}, session.CryptoSecretGenerator{}, policy)
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
