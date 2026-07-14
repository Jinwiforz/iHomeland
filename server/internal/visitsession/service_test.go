package visitsession

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
)

// TestServiceOpenAndCreateInvite 验证 AuthContext、owned world、current placement 与 store 编排。
func TestServiceOpenAndCreateInvite(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	open, err := fixture.service.Open(context.Background(), fixture.ownerAuth, fixture.ownerBinding, mustCommandID(t, "vcmd_open"))
	if err != nil || !open.Created() || !open.Valid() {
		t.Fatalf("open: result=%#v err=%v", open, err)
	}
	fixture.clock.Set(fixture.createdAt.Add(time.Second))
	result, err := fixture.service.CreateInvite(context.Background(), fixture.ownerAuth, fixture.visitorID, fixture.createdAt.Add(time.Minute), open.Snapshot().Revision(), mustCommandID(t, "vcmd_invite"))
	if err != nil || !result.Invite().Valid() || result.Snapshot().Revision() != open.Snapshot().Revision()+1 {
		t.Fatalf("create invite: result=%#v err=%v", result, err)
	}
}

// TestServiceAppliesConfiguredInviteLifetime 验证 application 使用 Policy 上限，而不是只接受领域全局上限。
func TestServiceAppliesConfiguredInviteLifetime(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	open, err := fixture.service.Open(context.Background(), fixture.ownerAuth, fixture.ownerBinding, mustCommandID(t, "vcmd_policyOpen"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.Set(fixture.createdAt.Add(time.Second))
	result, err := fixture.service.CreateInvite(context.Background(), fixture.ownerAuth, fixture.visitorID, fixture.createdAt.Add(2*time.Minute), open.Snapshot().Revision(), mustCommandID(t, "vcmd_policyInvite"))
	if !mutationResultEmpty(result) || !IsErrorCode(err, ErrorCodeInvalidArgument) {
		t.Fatalf("configured invite lifetime was ignored: result=%#v err=%v", result, err)
	}
	current, found, err := fixture.service.ResolveActive(context.Background(), fixture.worldID)
	if err != nil || !found || current.Revision() != open.Snapshot().Revision() || len(current.Invites()) != 0 {
		t.Fatalf("rejected policy deadline changed state: snapshot=%#v found=%v err=%v", current, found, err)
	}
}

// TestServiceMutationReplayAfterResponseLoss 验证 observedAt 推进后相同 command 通过 probe 返回首次结果。
func TestServiceMutationReplayAfterResponseLoss(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	open, err := fixture.service.Open(context.Background(), fixture.ownerAuth, fixture.ownerBinding, mustCommandID(t, "vcmd_replayOpen"))
	if err != nil {
		t.Fatal(err)
	}
	expected := open.Snapshot().Revision()
	commandID := mustCommandID(t, "vcmd_replayInvite")
	expiresAt := fixture.createdAt.Add(time.Minute)
	fixture.store.loseNextMutationResponse()
	if _, err := fixture.service.CreateInvite(context.Background(), fixture.ownerAuth, fixture.visitorID, expiresAt, expected, commandID); !IsErrorCode(err, ErrorCodeCommitUnknown) {
		t.Fatalf("first call should be commit unknown: %v", err)
	}
	fixture.clock.Set(fixture.createdAt.Add(10 * time.Second))
	replayed, err := fixture.service.CreateInvite(context.Background(), fixture.ownerAuth, fixture.visitorID, expiresAt, expected, commandID)
	if err != nil || replayed.Operation() != OperationCreateInvite || !replayed.Invite().Valid() {
		t.Fatalf("replay: result=%#v err=%v", replayed, err)
	}
	if replayed.Snapshot().Revision() != expected+1 {
		t.Fatalf("replay advanced revision twice: %d", replayed.Snapshot().Revision())
	}
}

// TestServiceDependencyAndAuthorizationFailures 验证 invalid AuthContext、reader failure 与 owner mismatch fail closed。
func TestServiceDependencyAndAuthorizationFailures(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	if _, err := fixture.service.Open(context.Background(), session.AuthContext{}, fixture.ownerBinding, mustCommandID(t, "vcmd_invalidAuth")); !IsErrorCode(err, ErrorCodeInvalidArgument) {
		t.Fatalf("invalid auth: %v", err)
	}
	fixture.worlds.err = errors.New("world unavailable")
	if _, err := fixture.service.Open(context.Background(), fixture.ownerAuth, fixture.ownerBinding, mustCommandID(t, "vcmd_worldError")); !IsErrorCode(err, ErrorCodeDependency) {
		t.Fatalf("world failure: %v", err)
	}
	fixture.worlds.err = nil
	fixture.worlds.snapshot = mustWorldSnapshot(t, fixture.worldID, fixture.visitorID, fixture.createdAt)
	if _, err := fixture.service.Open(context.Background(), fixture.ownerAuth, fixture.ownerBinding, mustCommandID(t, "vcmd_ownerMismatch")); !IsErrorCode(err, ErrorCodeDependencyDefect) {
		t.Fatalf("owner mismatch: %v", err)
	}
}

// TestServicePlacementMissingAndMalformedStoreResult 验证 placement missing 与矛盾 adapter outcome 不会伪成功。
func TestServicePlacementMissingAndMalformedStoreResult(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	fixture.assignments.outcome = AssignmentOutcomeNotFound
	fixture.assignments.snapshot = placement.AssignmentSnapshot{}
	if _, err := fixture.service.Open(context.Background(), fixture.ownerAuth, fixture.ownerBinding, mustCommandID(t, "vcmd_noPlacement")); !IsErrorCode(err, ErrorCodeStale) {
		t.Fatalf("placement missing: %v", err)
	}
	fixture = newServiceFixture(t)
	fixture.clock.Set(fixture.createdAt.Add(3 * time.Hour))
	if _, err := fixture.service.Open(context.Background(), fixture.ownerAuth, fixture.ownerBinding, mustCommandID(t, "vcmd_expiredPlacement")); !IsErrorCode(err, ErrorCodeStale) {
		t.Fatalf("placement expired: %v", err)
	}

	fixture = newServiceFixture(t)
	malformed := &malformedVisitStore{delegate: fixture.store, createOutcome: CreateOutcomeCreated}
	service, err := NewService(malformed, fixture.worlds, fixture.assignments, fixture.clock, fixture.ids, fixture.policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Open(context.Background(), fixture.ownerAuth, fixture.ownerBinding, mustCommandID(t, "vcmd_malformed")); !IsErrorCode(err, ErrorCodeDependencyDefect) {
		t.Fatalf("malformed create result: %v", err)
	}
}

// TestServiceInvalidateAssignmentRequiresPlacementEvidence 验证正常 current assignment 不能被内部 system command 误关。
func TestServiceInvalidateAssignmentRequiresPlacementEvidence(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	open, err := fixture.service.Open(context.Background(), fixture.ownerAuth, fixture.ownerBinding, mustCommandID(t, "vcmd_invalidateOpen"))
	if err != nil {
		t.Fatal(err)
	}
	expected := open.Snapshot().Revision()
	if _, err := fixture.service.InvalidateAssignment(context.Background(), open.Snapshot().ID(), expected, mustCommandID(t, "vcmd_invalidateCurrent")); !IsErrorCode(err, ErrorCodeInvalidState) {
		t.Fatalf("current assignment was not protected: %v", err)
	}
	fixture.assignments.err = errors.New("placement unavailable")
	if _, err := fixture.service.InvalidateAssignment(context.Background(), open.Snapshot().ID(), expected, mustCommandID(t, "vcmd_invalidateDependency")); !IsErrorCode(err, ErrorCodeDependency) {
		t.Fatalf("dependency failure was treated as invalidation evidence: %v", err)
	}
	fixture.assignments.err = nil
	fixture.assignments.snapshot = placement.AssignmentSnapshot{}
	fixture.assignments.outcome = AssignmentOutcomeNotFound
	result, err := fixture.service.InvalidateAssignment(context.Background(), open.Snapshot().ID(), expected, mustCommandID(t, "vcmd_invalidateMissing"))
	if err != nil || result.Snapshot().Lifecycle() != LifecycleClosed || result.Operation() != OperationInvalidateAssignment {
		t.Fatalf("missing assignment did not close session: result=%#v err=%v", result, err)
	}
	if active, found, err := fixture.service.ResolveActive(context.Background(), fixture.worldID); err != nil || found || !active.empty() {
		t.Fatalf("closed session retained active index: snapshot=%#v found=%v err=%v", active, found, err)
	}
}

// TestServiceNotCommittedAndCommitUnknown 保证 store 提交确定性映射稳定且不返回部分成功。
func TestServiceNotCommittedAndCommitUnknown(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		outcome MutationOutcome
		code    ErrorCode
		phase   CommitPhase
	}{
		{name: "not committed", outcome: MutationOutcomeNotCommitted, code: ErrorCodeDependency, phase: CommitPhaseNotCommitted},
		{name: "commit unknown", outcome: MutationOutcomeCommitUnknown, code: ErrorCodeCommitUnknown, phase: CommitPhaseUnknown},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newServiceFixture(t)
			open, err := fixture.service.Open(context.Background(), fixture.ownerAuth, fixture.ownerBinding, mustCommandID(t, "vcmd_failureOpen"))
			if err != nil {
				t.Fatal(err)
			}
			fixture.store.failNextMutation(test.outcome, errors.New("injected"))
			result, err := fixture.service.CreateInvite(context.Background(), fixture.ownerAuth, fixture.visitorID, fixture.createdAt.Add(time.Minute), open.Snapshot().Revision(), mustCommandID(t, "vcmd_failureInvite"))
			if !mutationResultEmpty(result) || !IsErrorCode(err, test.code) {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			var typed *Error
			if !errors.As(err, &typed) || typed.CommitPhase() != test.phase {
				t.Fatalf("commit phase=%v err=%v", typed, err)
			}
		})
	}
}

// serviceFixture 汇总 application 编排测试的纯 Go 依赖与受信 AuthContext。
type serviceFixture struct {
	// createdAt 是 clock、world 与 assignment 共用的绝对时间基准。
	createdAt time.Time
	// worldID 是 owned world reader 返回的持久 identity。
	worldID personalworld.PersonalWorldID
	// visitorID 是 Owner 创建定向邀请时使用的目标。
	visitorID account.PlayerID
	// ownerAuth 通过 session public API 创建，不能由本 package 伪造。
	ownerAuth session.AuthContext
	// ownerBinding 是 Open 使用的受信 connection registry identity。
	ownerBinding ConnectionBindingID
	// store 是仅测试 reference store。
	store *referenceStore
	// worlds 提供可故障注入的 owned world 查询。
	worlds *fakeWorldReader
	// assignments 提供可故障注入的 current placement 查询。
	assignments *fakeAssignmentReader
	// clock 可在 response-loss 重试前显式推进。
	clock *fakeClock
	// ids 为 Open 与 CreateInvite 提供独立测试材料。
	ids *sequenceIDs
	// policy 是 Service 启动后只读配置。
	policy Policy
	// service 是当前 fixture 被测 application 实例。
	service *Service
}

// newServiceFixture 创建不启动 listener、backend 或 production Composition Root 的完整依赖图。
func newServiceFixture(t *testing.T) serviceFixture {
	t.Helper()
	aggregate := newAggregateFixture(t, 2)
	ownerAuth := newTestAuth(t, "acc_owner", aggregate.owner.playerID.String(), aggregate.createdAt)
	worldSnapshot := mustWorldSnapshot(t, aggregate.worldID, aggregate.owner.playerID, aggregate.createdAt)
	store := newReferenceStore()
	worlds := &fakeWorldReader{snapshot: worldSnapshot, outcome: OwnedWorldOutcomeFound}
	assignments := &fakeAssignmentReader{snapshot: aggregate.assignment, outcome: AssignmentOutcomeFound}
	clock := &fakeClock{now: aggregate.createdAt}
	ids := &sequenceIDs{}
	service, err := NewService(store, worlds, assignments, clock, ids, aggregate.policy)
	if err != nil {
		t.Fatal(err)
	}
	return serviceFixture{createdAt: aggregate.createdAt, worldID: aggregate.worldID, visitorID: aggregate.visitorA.playerID, ownerAuth: ownerAuth, ownerBinding: aggregate.ownerBinding.connectionID, store: store, worlds: worlds, assignments: assignments, clock: clock, ids: ids, policy: aggregate.policy, service: service}
}

// fakeWorldReader 返回测试选择的持久 world outcome，并允许注入依赖失败。
type fakeWorldReader struct {
	// snapshot 是 reader 返回的完整或故意 malformed 结果。
	snapshot personalworld.Snapshot
	// outcome 是与 snapshot 配套的稳定读取决议。
	outcome OwnedWorldOutcome
	// err 用于模拟依赖不可用。
	err error
}

// ResolveOwnedWorld 返回预设结果；测试通过 snapshot owner mismatch 验证 Service 严格校验。
func (reader *fakeWorldReader) ResolveOwnedWorld(_ context.Context, _ account.PlayerID) (personalworld.Snapshot, OwnedWorldOutcome, error) {
	return reader.snapshot, reader.outcome, reader.err
}

// fakeAssignmentReader 返回测试选择的 current placement outcome。
type fakeAssignmentReader struct {
	// snapshot 是 reader 返回的 current placement 候选。
	snapshot placement.AssignmentSnapshot
	// outcome 是与 snapshot 配套的稳定读取决议。
	outcome AssignmentOutcome
	// err 用于模拟 placement dependency failure。
	err error
}

// ResolveCurrent 返回预设结果；Service 仍必须验证 world、phase、lease 与 observedAt。
func (reader *fakeAssignmentReader) ResolveCurrent(_ context.Context, _ personalworld.PersonalWorldID, _ time.Time) (placement.AssignmentSnapshot, AssignmentOutcome, error) {
	return reader.snapshot, reader.outcome, reader.err
}

// malformedVisitStore 仅覆盖指定 create outcome，用于验证矛盾空 result 被拒绝。
type malformedVisitStore struct {
	// delegate 为非 create 方法保留正常 store 行为。
	delegate VisitSessionStore
	// createOutcome 与故意空 result 组成矛盾返回。
	createOutcome CreateOutcome
}

// Create 返回故意矛盾的空 result 与成功 outcome。
func (store *malformedVisitStore) Create(context.Context, CreateRecord) (CreateResult, CreateOutcome, error) {
	return CreateResult{}, store.createOutcome, nil
}

// ResolveActive 委托正常 reference store。
func (store *malformedVisitStore) ResolveActive(ctx context.Context, worldID personalworld.PersonalWorldID) (Snapshot, ResolveOutcome, error) {
	return store.delegate.ResolveActive(ctx, worldID)
}

// FindByID 委托正常 reference store。
func (store *malformedVisitStore) FindByID(ctx context.Context, id VisitSessionID) (Snapshot, ResolveOutcome, error) {
	return store.delegate.FindByID(ctx, id)
}

// Commit 委托正常 reference store。
func (store *malformedVisitStore) Commit(ctx context.Context, record TransitionRecord) (MutationResult, MutationOutcome, error) {
	return store.delegate.Commit(ctx, record)
}

// mustWorldSnapshot 创建 active PersonalWorld 持久投影。
func mustWorldSnapshot(t *testing.T, id personalworld.PersonalWorldID, owner account.PlayerID, createdAt time.Time) personalworld.Snapshot {
	t.Helper()
	snapshot, err := personalworld.NewSnapshot(id, owner, personalworld.LifecycleActive, personalworld.Revision(1), createdAt)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// mustCommandID 构造测试 command identity。
func mustCommandID(t *testing.T, value string) CommandID {
	t.Helper()
	id, err := NewCommandID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// newTestAuth 通过 session public API 创建不可伪造 AuthContext，而不是绕过其 owner 边界。
func newTestAuth(t *testing.T, accountValue, playerValue string, now time.Time) session.AuthContext {
	t.Helper()
	principal, err := session.NewPrincipal(accountValue, playerValue)
	if err != nil {
		t.Fatal(err)
	}
	store := &authSessionStore{}
	policy, _ := session.NewPolicy(time.Second, time.Minute, time.Hour, time.Hour)
	service, err := session.NewService(store, unusedEndpointProvider{}, unusedInvalidator{}, fixedSessionClock{now: now}, &sessionIDs{}, session.CryptoSecretGenerator{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateSession(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := service.AuthenticateAccess(context.Background(), created.Tokens.Access.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

// authSessionStore 只实现创建与 access 解析，以公共 session API 生成测试 AuthContext。
type authSessionStore struct {
	// mu 保护 Create 与 ResolveAccess 共享的 session bundle。
	mu sync.Mutex
	// bundle 保存 session owner 正式 public API 创建的测试认证事实。
	bundle session.SessionBundle
}

// Create 保存首个 session bundle。
func (store *authSessionStore) Create(_ context.Context, bundle session.SessionBundle) (session.StoreOutcome, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.bundle = bundle
	return session.StoreOutcomeApplied, nil
}

// ResolveAccess 校验 digest 并返回当前权威身份。
func (store *authSessionStore) ResolveAccess(_ context.Context, digest session.Digest, _ time.Time) (session.AuthSnapshot, session.StoreOutcome, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if digest != store.bundle.Access.Digest {
		return session.AuthSnapshot{}, session.StoreOutcomeNotFound, nil
	}
	return session.AuthSnapshot{Principal: store.bundle.Session.Principal, SessionID: store.bundle.Session.ID, Epoch: store.bundle.Session.Epoch}, session.StoreOutcomeApplied, nil
}

// RotateRefresh 未被 AuthContext fixture 使用。
func (*authSessionStore) RotateRefresh(context.Context, session.Rotation) (session.AuthSnapshot, session.Invalidation, session.StoreOutcome, error) {
	return session.AuthSnapshot{}, session.Invalidation{}, session.StoreOutcomeNotFound, nil
}

// IssueTicket 未被 AuthContext fixture 使用。
func (*authSessionStore) IssueTicket(context.Context, session.TicketRecord, time.Time) (session.StoreOutcome, error) {
	return session.StoreOutcomeNotFound, nil
}

// ConsumeTicket 未被 AuthContext fixture 使用。
func (*authSessionStore) ConsumeTicket(context.Context, session.Digest, session.Channel, session.Endpoint, time.Time) (session.AuthSnapshot, session.StoreOutcome, error) {
	return session.AuthSnapshot{}, session.StoreOutcomeNotFound, nil
}

// InvalidateSession 未被 AuthContext fixture 使用。
func (*authSessionStore) InvalidateSession(context.Context, session.SessionID, session.InvalidationReason) (session.Invalidation, session.StoreOutcome, error) {
	return session.Invalidation{}, session.StoreOutcomeNotFound, nil
}

// InvalidatePrincipal 未被 AuthContext fixture 使用。
func (*authSessionStore) InvalidatePrincipal(context.Context, session.Principal, session.InvalidationReason) ([]session.Invalidation, error) {
	return nil, nil
}

// fixedSessionClock 为 session fixture 返回固定绝对时间。
type fixedSessionClock struct {
	// now 是 session fixture 的固定绝对时间。
	now time.Time
}

// Now 返回固定测试时间。
func (clock fixedSessionClock) Now() time.Time { return clock.now }

// sessionIDs 为 session fixture 返回安全 ASCII 随机材料替身。
type sessionIDs struct{}

// NewID 返回 fixture 内唯一材料。
func (*sessionIDs) NewID() (string, error) { return "visitAuthFixture", nil }

// unusedEndpointProvider 满足未被 fixture 调用的 realtime endpoint 依赖。
type unusedEndpointProvider struct{}

// EndpointFor 在意外调用时明确失败。
func (unusedEndpointProvider) EndpointFor(context.Context, session.Channel) (session.Endpoint, error) {
	return session.Endpoint{}, errors.New("unused endpoint provider")
}

// unusedInvalidator 满足未被 fixture 调用的连接失效依赖。
type unusedInvalidator struct{}

// Invalidate 在 fixture 中无 side effect。
func (unusedInvalidator) Invalidate(context.Context, session.Invalidation) error { return nil }
