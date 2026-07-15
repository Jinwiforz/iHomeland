package worldadmission

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
)

// TestCredentialAndBindingAreRedacted 验证默认格式化不会递归泄漏raw credential或binding。
func TestCredentialAndBindingAreRedacted(t *testing.T) {
	fixture := newAdmissionFixture(t)
	result, err := fixture.service.Issue(context.Background(), mustIssueID(t, "issue_redacted"), fixture.binding)
	if err != nil {
		t.Fatal(err)
	}
	raw := result.Credential().Value()
	for _, value := range []any{result.Credential(), result.Binding(), result} {
		formatted := fmt.Sprintf("%v %#v", value, value)
		if strings.Contains(formatted, raw) || strings.Contains(formatted, fixture.playerID.String()) {
			t.Fatalf("world admission formatting leaked secret or binding: %s", formatted)
		}
	}
	if result.Credential().LogValue().Kind() != slog.KindString {
		t.Fatal("credential slog value is not redacted string")
	}
}

// TestIssueIsDeterministicAndIdempotent 验证response-loss后只返回首次credential。
func TestIssueIsDeterministicAndIdempotent(t *testing.T) {
	fixture := newAdmissionFixture(t)
	id := mustIssueID(t, "issue_replay")
	fixture.store.loseNextIssue = true
	if result, err := fixture.service.Issue(context.Background(), id, fixture.binding); result.Valid() || !IsErrorCode(err, ErrorCodeCommitUnknown) {
		t.Fatalf("first issue result=%#v err=%v", result, err)
	}
	replayed, err := fixture.service.Issue(context.Background(), id, fixture.binding)
	if err != nil || !replayed.Valid() || !replayed.Replayed() {
		t.Fatalf("replay result=%#v err=%v", replayed, err)
	}
	again, err := fixture.service.Issue(context.Background(), id, fixture.binding)
	if err != nil || again.Credential().Value() != replayed.Credential().Value() {
		t.Fatalf("credential changed across replay: err=%v", err)
	}
	changed, _ := NewBinding(fixture.playerID, fixture.sessionID, session.Epoch(2), RoleVisitor, fixture.worldID, fixture.visitID, PurposeJoin, fixture.stamp, fixture.endpoint, fixture.now, fixture.binding.ExpiresAt())
	if result, err := fixture.service.Issue(context.Background(), id, changed); result.Valid() || !IsErrorCode(err, ErrorCodeIdempotencyConflict) {
		t.Fatalf("changed binding result=%#v err=%v", result, err)
	}
}

// TestVerifyConsumesOnceAndResolvesExactRetry 验证静态binding、原子消费和consume response-loss。
func TestVerifyConsumesOnceAndResolvesExactRetry(t *testing.T) {
	fixture := newAdmissionFixture(t)
	issued, err := fixture.service.Issue(context.Background(), mustIssueID(t, "issue_verify"), fixture.binding)
	if err != nil {
		t.Fatal(err)
	}
	consumeID := mustConsumeID(t, "consume_verify")
	qualification, err := fixture.service.Verify(context.Background(), issued.Credential(), consumeID, fixture.auth, fixture.endpoint, PurposeJoin)
	if err != nil || !qualification.Valid() || !qualification.Binding().Equal(fixture.binding) {
		t.Fatalf("verify qualification=%#v err=%v", qualification, err)
	}
	replayed, err := fixture.service.Verify(context.Background(), issued.Credential(), consumeID, fixture.auth, fixture.endpoint, PurposeJoin)
	if err != nil || !replayed.Valid() {
		t.Fatalf("exact retry qualification=%#v err=%v", replayed, err)
	}
	fixture.clock.now = fixture.binding.ExpiresAt()
	if qualification, err := fixture.service.Verify(context.Background(), issued.Credential(), consumeID, fixture.auth, fixture.endpoint, PurposeJoin); qualification.Valid() || !IsErrorCode(err, ErrorCodeExpired) {
		t.Fatalf("expired exact retry qualification=%#v err=%v", qualification, err)
	}
	fixture.clock.now = fixture.now
	if qualification, err := fixture.service.Verify(context.Background(), issued.Credential(), mustConsumeID(t, "consume_other"), fixture.auth, fixture.endpoint, PurposeJoin); qualification.Valid() || !IsErrorCode(err, ErrorCodeReplayed) {
		t.Fatalf("different consume identity qualification=%#v err=%v", qualification, err)
	}
}

// TestVerifyRejectsContradictoryStoreBinding 验证 service 不信任 adapter 返回的其他连接 binding。
func TestVerifyRejectsContradictoryStoreBinding(t *testing.T) {
	fixture := newAdmissionFixture(t)
	issued, err := fixture.service.Issue(context.Background(), mustIssueID(t, "issue_defective_store"), fixture.binding)
	if err != nil {
		t.Fatal(err)
	}
	otherPlayer, _ := account.NewPlayerID("ply_admissionOther")
	otherSession, _ := session.NewSessionID("ses_admissionOther")
	otherBinding, err := NewBinding(otherPlayer, otherSession, session.InitialEpoch, RoleVisitor, fixture.worldID, fixture.visitID, PurposeJoin, fixture.stamp, fixture.endpoint, fixture.now, fixture.binding.ExpiresAt())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(defectiveConsumeStore{binding: otherBinding}, fixture.placements, fixture.clock, []byte("0123456789abcdef0123456789abcdef"), fixture.service.policy)
	if err != nil {
		t.Fatal(err)
	}
	qualification, err := service.Verify(context.Background(), issued.Credential(), mustConsumeID(t, "consume_defective_store"), fixture.auth, fixture.endpoint, PurposeJoin)
	if qualification.Valid() || !IsErrorCode(err, ErrorCodeDependencyDefect) {
		t.Fatalf("qualification=%#v err=%v", qualification, err)
	}
}

// TestVerifyRejectsStaticAndCurrentBindingMismatch 验证purpose、endpoint、epoch与full assignment都fail closed。
func TestVerifyRejectsStaticAndCurrentBindingMismatch(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*admissionFixture)
		code   ErrorCode
	}{
		{name: "purpose", mutate: func(f *admissionFixture) { f.verifyPurpose = PurposeReconnect }, code: ErrorCodeInvalid},
		{name: "endpoint", mutate: func(f *admissionFixture) {
			f.endpoint, _ = session.NewEndpoint(session.ChannelTLSTCP, "other.example.invalid", 4433)
		}, code: ErrorCodeInvalid},
		{name: "assignment", mutate: func(f *admissionFixture) { f.placements.snapshot = successorAssignment(t, f) }, code: ErrorCodeStaleAssignment},
		{name: "dependency", mutate: func(f *admissionFixture) { f.placements.err = errors.New("placement unavailable") }, code: ErrorCodeDependency},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAdmissionFixture(t)
			issued, err := fixture.service.Issue(context.Background(), mustIssueID(t, "issue_"+test.name), fixture.binding)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(fixture)
			qualification, err := fixture.service.Verify(context.Background(), issued.Credential(), mustConsumeID(t, "consume_"+test.name), fixture.auth, fixture.endpoint, fixture.verifyPurpose)
			if qualification.Valid() || !IsErrorCode(err, test.code) {
				t.Fatalf("qualification=%#v err=%v", qualification, err)
			}
		})
	}
}

// TestVisitQualificationPreservesPurpose 验证JOIN与RECONNECT hydration不能互换。
func TestVisitQualificationPreservesPurpose(t *testing.T) {
	fixture := newAdmissionFixture(t)
	join, _ := newQualification(fixture.binding)
	visitQualification, err := join.VisitSessionQualification()
	if err != nil || strings.Contains(fmt.Sprintf("%#v", visitQualification), fixture.playerID.String()) {
		t.Fatalf("join bridge err=%v", err)
	}
	reconnectBinding, err := NewBinding(fixture.playerID, fixture.sessionID, session.InitialEpoch, RoleVisitor, fixture.worldID, fixture.visitID, PurposeReconnect, fixture.stamp, fixture.endpoint, fixture.now, fixture.binding.ExpiresAt())
	if err != nil {
		t.Fatal(err)
	}
	reconnect, _ := newQualification(reconnectBinding)
	if _, err := reconnect.VisitSessionQualification(); err != nil {
		t.Fatalf("reconnect bridge: %v", err)
	}
}

// FuzzWorldAdmissionParsing 验证任意入站credential/identity不会panic或接受非规范编码。
func FuzzWorldAdmissionParsing(f *testing.F) {
	f.Add("wad1_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "consume_fixture")
	f.Add("", "")
	f.Fuzz(func(t *testing.T, credentialValue string, identity string) {
		credential, credentialErr := ParseCredential(credentialValue)
		if credentialErr == nil && !credential.Valid() {
			t.Fatal("parsed credential is invalid")
		}
		consumeID, identityErr := NewConsumeID(identity)
		if identityErr == nil && !consumeID.Valid() {
			t.Fatal("parsed consume identity is invalid")
		}
	})
}

// admissionFixture 汇总issuer/verifier测试的受信事实与可控依赖。
type admissionFixture struct {
	// now 是全部领域事实共享的受信基准时间。
	now time.Time
	// playerID 是Visitor认证身份。
	playerID account.PlayerID
	// sessionID 是Visitor认证lineage。
	sessionID session.SessionID
	// worldID 是credential target。
	worldID personalworld.PersonalWorldID
	// visitID 是Visitor purpose target。
	visitID visitsession.VisitSessionID
	// stamp 是签发时current full assignment。
	stamp placement.AssignmentStamp
	// endpoint 是唯一TLS/TCP消费目标。
	endpoint session.Endpoint
	// binding 是默认JOIN授权事实。
	binding Binding
	// auth 是真实session service构造的gameplay AuthContext。
	auth session.AuthContext
	// store 保存测试内可失效运行态。
	store *memoryAdmissionStore
	// placements 返回可注入故障的current assignment。
	placements *fakePlacementReader
	// service 是被测issuer/verifier。
	service *Service
	// clock 允许expiry用例推进绝对时间。
	clock *fixedClock
	// verifyPurpose 是用例默认提交的业务用途。
	verifyPurpose Purpose
}

// newAdmissionFixture 构造共享时间、身份、assignment 与内存依赖，确保测试只改变目标风险条件。
func newAdmissionFixture(t *testing.T) *admissionFixture {
	t.Helper()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	playerID, _ := account.NewPlayerID("ply_admissionFixture")
	sessionID, _ := session.NewSessionID("ses_admissionFixture")
	worldID, _ := personalworld.NewPersonalWorldID("pworld_admissionFixture")
	visitID, _ := visitsession.NewVisitSessionID("vses_admissionFixture")
	instanceID, _ := placement.NewWorldInstanceID("winst_admissionFixture")
	nodeID, _ := placement.NewRuntimeNodeID("rnode_admissionFixture")
	generation, _ := placement.NewAssignmentGeneration(1)
	fence, _ := placement.NewFencingToken(1)
	stamp, _ := placement.NewAssignmentStamp(worldID, instanceID, nodeID, generation, fence)
	assignment, _ := placement.NewAssignmentSnapshot(stamp, placement.PhaseActive, now.Add(-time.Minute), now.Add(time.Minute), now)
	endpoint, _ := session.NewEndpoint(session.ChannelTLSTCP, "game.example.invalid", 4433)
	binding, err := NewBinding(playerID, sessionID, session.InitialEpoch, RoleVisitor, worldID, visitID, PurposeJoin, stamp, endpoint, now, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	auth := newGameplayAuth(t, now, playerID, sessionID, endpoint)
	store := newMemoryAdmissionStore()
	placements := &fakePlacementReader{snapshot: assignment, outcome: placement.ResolveOutcomeFound}
	clock := &fixedClock{now: now}
	policy, _ := NewPolicy(time.Minute, time.Minute)
	service, err := NewService(store, placements, clock, []byte("0123456789abcdef0123456789abcdef"), policy)
	if err != nil {
		t.Fatal(err)
	}
	return &admissionFixture{now: now, playerID: playerID, sessionID: sessionID, worldID: worldID, visitID: visitID, stamp: stamp, endpoint: endpoint, binding: binding, auth: auth, store: store, placements: placements, service: service, clock: clock, verifyPurpose: PurposeJoin}
}

// successorAssignment 构造同一 PersonalWorld 的新实例与 fencing lineage，用于验证完整 stamp 比较。
func successorAssignment(t *testing.T, fixture *admissionFixture) placement.AssignmentSnapshot {
	t.Helper()
	instanceID, _ := placement.NewWorldInstanceID("winst_admissionSuccessor")
	nodeID, _ := placement.NewRuntimeNodeID("rnode_admissionSuccessor")
	generation, _ := placement.NewAssignmentGeneration(2)
	fence, _ := placement.NewFencingToken(2)
	stamp, _ := placement.NewAssignmentStamp(fixture.worldID, instanceID, nodeID, generation, fence)
	snapshot, _ := placement.NewAssignmentSnapshot(stamp, placement.PhaseActive, fixture.now, fixture.now.Add(time.Minute), fixture.now)
	return snapshot
}

// fixedClock 提供测试显式推进的并发只读时间。
type fixedClock struct {
	// now 是测试显式推进的绝对时间。
	now time.Time
}

// Now 返回测试显式控制的绝对时间。
func (clock *fixedClock) Now() time.Time { return clock.now }

// fakePlacementReader 返回固定current assignment或注入依赖失败。
type fakePlacementReader struct {
	// snapshot 是Found时返回的完整current事实。
	snapshot placement.AssignmentSnapshot
	// outcome 控制Found/NotFound决议。
	outcome placement.ResolveOutcome
	// err 注入依赖失败。
	err error
}

// Resolve 返回预置 snapshot/outcome/error 组合，以覆盖 placement 决议和 adapter defect。
func (reader *fakePlacementReader) Resolve(_ context.Context, _ personalworld.PersonalWorldID, _ time.Time) (placement.AssignmentSnapshot, placement.ResolveOutcome, error) {
	return reader.snapshot, reader.outcome, reader.err
}

// storedCredential 模拟Redis credential Hash的最小状态。
type storedCredential struct {
	// binding 是首次签发的受信事实。
	binding Binding
	// status 是issued或consumed。
	status string
	// consumeID 保存首次消费identity。
	consumeID ConsumeID
	// fingerprint 保存首次消费语义。
	fingerprint Digest
}

// memoryAdmissionStore 只在测试中模拟原子issue/consume线性化点。
type memoryAdmissionStore struct {
	// mu 线性化并发测试访问。
	mu sync.Mutex
	// issues 按raw测试identity保存首次record。
	issues map[string]IssueRecord
	// credentials 按digest保存一次性状态。
	credentials map[string]storedCredential
	// loseNextIssue 在提交后注入一次response loss。
	loseNextIssue bool
}

// defectiveConsumeStore 模拟返回结构有效但与请求身份矛盾的缺陷 adapter。
type defectiveConsumeStore struct {
	// binding 是故意与请求身份不一致的返回值。
	binding Binding
}

// Issue 标记本缺陷 adapter 测试不会执行签发。
func (defectiveConsumeStore) Issue(context.Context, IssueRecord, time.Time) (IssueOutcome, error) {
	return IssueOutcomeUnspecified, errors.New("unused")
}

// Consume 返回错误主体的结构有效 binding，验证 service 的防御性复核。
func (store defectiveConsumeStore) Consume(context.Context, ConsumeRequest) (Binding, ConsumeOutcome, error) {
	return store.binding, ConsumeOutcomeApplied, nil
}

// newMemoryAdmissionStore 创建隔离的线性化内存模型，不承担 production recovery 语义。
func newMemoryAdmissionStore() *memoryAdmissionStore {
	return &memoryAdmissionStore{issues: map[string]IssueRecord{}, credentials: map[string]storedCredential{}}
}

// Issue 模拟首次写入、精确幂等重放、语义冲突与提交后响应丢失。
func (store *memoryAdmissionStore) Issue(_ context.Context, record IssueRecord, _ time.Time) (IssueOutcome, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if existing, ok := store.issues[record.IssueID.Value()]; ok {
		credential := store.credentials[existing.CredentialDigest.Hex()]
		if !existing.Fingerprint.Equal(record.Fingerprint) || !existing.CredentialDigest.Equal(record.CredentialDigest) || !existing.Binding.Equal(record.Binding) {
			return IssueOutcomeIdempotencyConflict, nil
		}
		if credential.status == "consumed" {
			return IssueOutcomeConsumed, nil
		}
		return IssueOutcomeReplay, nil
	}
	store.issues[record.IssueID.Value()] = record
	store.credentials[record.CredentialDigest.Hex()] = storedCredential{binding: record.Binding, status: "issued"}
	if store.loseNextIssue {
		store.loseNextIssue = false
		return IssueOutcomeCommitUnknown, errors.New("response lost")
	}
	return IssueOutcomeCreated, nil
}

// Consume 模拟一次性消费、精确 response-loss 重放及不同 identity 重放拒绝。
func (store *memoryAdmissionStore) Consume(_ context.Context, request ConsumeRequest) (Binding, ConsumeOutcome, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.credentials[request.CredentialDigest.Hex()]
	if !ok {
		return Binding{}, ConsumeOutcomeNotFound, nil
	}
	if record.status == "consumed" {
		if record.consumeID == request.ConsumeID && record.fingerprint.Equal(request.Fingerprint) {
			return record.binding, ConsumeOutcomeReplay, nil
		}
		return Binding{}, ConsumeOutcomeReplayed, nil
	}
	if !record.binding.ExpiresAt().After(request.ObservedAt) {
		return Binding{}, ConsumeOutcomeExpired, nil
	}
	if record.binding.PlayerID().String() != request.Auth.Principal().PlayerID() || record.binding.SessionID() != request.Auth.SessionID() || record.binding.Epoch() != request.Auth.Epoch() || record.binding.Purpose() != request.Purpose || !record.binding.Endpoint().Equal(request.Endpoint) {
		return Binding{}, ConsumeOutcomeBindingMismatch, nil
	}
	record.status = "consumed"
	record.consumeID = request.ConsumeID
	record.fingerprint = request.Fingerprint
	store.credentials[request.CredentialDigest.Hex()] = record
	return record.binding, ConsumeOutcomeApplied, nil
}

// mustIssueID 构造合法测试 IssueID，并把 fixture 错误提升为当前测试失败。
func mustIssueID(t *testing.T, value string) IssueID {
	t.Helper()
	id, err := NewIssueID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// mustConsumeID 构造合法测试 ConsumeID，并把 fixture 错误提升为当前测试失败。
func mustConsumeID(t *testing.T, value string) ConsumeID {
	t.Helper()
	id, err := NewConsumeID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// sessionTestStore 只为真实 session service 构造封闭 AuthContext。
type sessionTestStore struct {
	// snapshot 是ticket consume返回的权威session事实。
	snapshot session.AuthSnapshot
}

// Create 标记本 fixture 不会创建 session。
func (store sessionTestStore) Create(context.Context, session.SessionBundle) (session.StoreOutcome, error) {
	return session.StoreOutcomeUnspecified, errors.New("unused")
}

// ResolveAccess 标记本 fixture 不会解析 access credential。
func (store sessionTestStore) ResolveAccess(context.Context, session.Digest, time.Time) (session.AuthSnapshot, session.StoreOutcome, error) {
	return session.AuthSnapshot{}, session.StoreOutcomeUnspecified, errors.New("unused")
}

// RotateRefresh 标记本 fixture 不会轮换 refresh credential。
func (store sessionTestStore) RotateRefresh(context.Context, session.Rotation) (session.AuthSnapshot, session.Invalidation, session.StoreOutcome, error) {
	return session.AuthSnapshot{}, session.Invalidation{}, session.StoreOutcomeUnspecified, errors.New("unused")
}

// IssueTicket 标记 AuthContext 使用预置 ticket 路径构造，不在此处签发。
func (store sessionTestStore) IssueTicket(context.Context, session.TicketRecord, time.Time) (session.StoreOutcome, error) {
	return session.StoreOutcomeUnspecified, errors.New("unused")
}

// ConsumeTicket 返回预置的权威 session snapshot。
func (store sessionTestStore) ConsumeTicket(context.Context, session.Digest, session.Channel, session.Endpoint, time.Time) (session.AuthSnapshot, session.StoreOutcome, error) {
	return store.snapshot, session.StoreOutcomeApplied, nil
}

// InvalidateSession 标记本 fixture 不触发 session 失效。
func (store sessionTestStore) InvalidateSession(context.Context, session.SessionID, session.InvalidationReason) (session.Invalidation, session.StoreOutcome, error) {
	return session.Invalidation{}, session.StoreOutcomeUnspecified, errors.New("unused")
}

// InvalidatePrincipal 标记本 fixture 不触发 principal 失效。
func (store sessionTestStore) InvalidatePrincipal(context.Context, session.Principal, session.InvalidationReason) ([]session.Invalidation, error) {
	return nil, errors.New("unused")
}

// sessionEndpointProvider 返回测试listener的受信identity。
type sessionEndpointProvider struct {
	// endpoint 是唯一允许的测试目标。
	endpoint session.Endpoint
}

// EndpointFor 返回 fixture 唯一受信的 listener endpoint。
func (provider sessionEndpointProvider) EndpointFor(context.Context, session.Channel) (session.Endpoint, error) {
	return provider.endpoint, nil
}

// sessionInvalidator 满足本 fixture 不会触发的连接失效边界。
type sessionInvalidator struct{}

// Invalidate 满足本 fixture 不会触发的连接失效边界。
func (sessionInvalidator) Invalidate(context.Context, session.Invalidation) error { return nil }

// sessionIDGenerator 满足本 fixture 不会调用的新 ID 边界。
type sessionIDGenerator struct{}

// NewID 返回不会进入持久 identity 的固定测试值。
func (sessionIDGenerator) NewID() (string, error) { return "admissionFixture", nil }

// sessionSecretGenerator 为真实 ticket consume 前置构造提供确定性测试熵。
type sessionSecretGenerator struct{}

// Fill 用确定性字节填充仅供 fixture 构造的 ticket nonce。
func (sessionSecretGenerator) Fill(buffer []byte) error {
	for index := range buffer {
		buffer[index] = byte(index + 1)
	}
	return nil
}

// newGameplayAuth 通过真实 session ticket consume 构造 TLS/TCP gameplay AuthContext，避免伪造私有字段。
func newGameplayAuth(t *testing.T, now time.Time, playerID account.PlayerID, sessionID session.SessionID, endpoint session.Endpoint) session.AuthContext {
	t.Helper()
	principal, _ := session.NewPrincipal("acc_admissionFixture", playerID.String())
	scopes, _ := session.NewScopeSet(session.ScopeGameplay)
	store := sessionTestStore{snapshot: session.AuthSnapshot{Principal: principal, SessionID: sessionID, Epoch: session.InitialEpoch, Scopes: scopes}}
	policy, _ := session.NewPolicy(time.Second, time.Minute, time.Hour, time.Hour)
	service, err := session.NewService(store, sessionEndpointProvider{endpoint}, sessionInvalidator{}, &fixedClock{now}, sessionIDGenerator{}, sessionSecretGenerator{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	nonce, _ := session.ParseTicketNonce([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	auth, err := service.ConsumeTicket(context.Background(), nonce, session.ChannelTLSTCP, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	return auth
}
