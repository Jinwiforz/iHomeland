package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	visitv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/visit/v1"
	"github.com/jinwiforz/ihomeland/server/internal/observability"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/transport/tcpgameplay"
	"github.com/jinwiforz/ihomeland/server/internal/transport/wscontrol"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
	"google.golang.org/protobuf/proto"
)

// TestVisitCoordinatorReconcilesAndDeduplicatesEffects 验证 deadline 替换、旧 revision 拒绝与 replay 副作用去重。
func TestVisitCoordinatorReconcilesAndDeduplicatesEffects(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	clock := newManualSemanticClock(now)
	deadlines, _ := newSemanticDeadlineOwner(16, clock)
	wss := newRecordingVisitWSS()
	tcp := newRecordingVisitTCP()
	policy, _ := visitsession.NewPolicy(2, time.Hour, time.Minute, 30*time.Second, 20*time.Second, 15*time.Second)
	coordinator := &personalWorldVisitCoordinator{
		deadlines: deadlines, clock: clock, policy: policy, wss: wss, tcp: tcp, connections: tcp,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)), maximumEffects: 16, failClosed: func() {},
		schedules: make(map[string]visitScheduleState), effects: make(map[string]struct{}),
		projector: func(_ context.Context, snapshot visitsession.Snapshot) (*visitv1.VisitSessionSnapshot, error) {
			return visitv1.VisitSessionSnapshot_builder{VisitSessionId: proto.String(snapshot.ID().Value()), OwnerPlayerId: proto.String(snapshot.OwnerID().String())}.Build(), nil
		},
	}

	open := coordinatorSnapshotFixture(t, now, visitsession.LifecycleOpen, 2, true)
	result := coordinatorCreateInviteResult(t, open)
	coordinator.MutationCommitted(context.Background(), result)
	coordinator.MutationCommitted(context.Background(), result)
	if deadlines.Len() != 2 {
		t.Fatalf("deadline count = %d, want session + invite", deadlines.Len())
	}
	if wss.count(2100) != 1 || tcp.connectionCount(2121) != 1 || tcp.visitCount(2121) != 1 {
		t.Fatalf("effects wss=%d owner=%d visitors=%d", wss.count(2100), tcp.connectionCount(2121), tcp.visitCount(2121))
	}

	older := coordinatorSnapshotFixture(t, now, visitsession.LifecycleOpen, 1, false)
	coordinator.SnapshotResolved(context.Background(), older)
	if deadlines.Len() != 2 {
		t.Fatalf("older revision changed deadline count to %d", deadlines.Len())
	}

	closed := coordinatorSnapshotFixture(t, now, visitsession.LifecycleClosed, 3, false)
	closeResult := coordinatorCloseResult(t, closed)
	coordinator.MutationCommitted(context.Background(), closeResult)
	if deadlines.Len() != 0 {
		t.Fatalf("closed snapshot retained %d deadlines", deadlines.Len())
	}
	if wss.count(2102) != 1 {
		t.Fatalf("closed notice count = %d", wss.count(2102))
	}
}

// TestVisitCoordinatorPublishesRetiredInviteToExactTarget 验证退役事实复用2100且只投递给原目标玩家。
func TestVisitCoordinatorPublishesRetiredInviteToExactTarget(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 10, 30, 0, 0, time.UTC)
	wss := newRecordingVisitWSS()
	coordinator := &personalWorldVisitCoordinator{
		wss:            wss,
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		maximumEffects: 16,
		failClosed:     func() {},
		schedules:      make(map[string]visitScheduleState),
		effects:        make(map[string]struct{}),
	}
	source := coordinatorSnapshotFixture(t, now, visitsession.LifecycleOpen, 2, true)
	target := coordinatorSnapshotFixture(t, now, visitsession.LifecycleOpen, 3, false)
	result := coordinatorRetiredInviteResult(t, target, source.Invites()[0])
	coordinator.MutationCommitted(context.Background(), result)
	coordinator.MutationCommitted(context.Background(), result)

	records := wss.recordsFor(2100)
	if len(records) != 1 {
		t.Fatalf("retired invite deliveries = %d", len(records))
	}
	push, ok := records[0].payload.(*visitv1.VisitInvitePush)
	if !ok || records[0].playerID != source.Invites()[0].TargetID().String() || push.GetInvite().GetInviteId() != source.Invites()[0].ID().Value() || push.GetInvite().GetState() != visitv1.VisitInviteState_VISIT_INVITE_STATE_RETIRED {
		t.Fatalf("retired invite delivery = %#v payload=%T", records[0], records[0].payload)
	}
}

// TestVisitCoordinatorMapsAvailabilityAndAssignmentLoss 验证既有WSS通知与精确Visitor safe-return映射。
func TestVisitCoordinatorMapsAvailabilityAndAssignmentLoss(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 11, 0, 0, 0, time.UTC)
	clock := newManualSemanticClock(now)
	deadlines, _ := newSemanticDeadlineOwner(16, clock)
	wss := newRecordingVisitWSS()
	tcp := newRecordingVisitTCP()
	policy, _ := visitsession.NewPolicy(2, time.Hour, time.Minute, 30*time.Second, 20*time.Second, 15*time.Second)
	coordinator := &personalWorldVisitCoordinator{
		deadlines: deadlines, clock: clock, policy: policy, wss: wss, tcp: tcp, connections: tcp,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)), maximumEffects: 16, failClosed: func() {},
		schedules: make(map[string]visitScheduleState), effects: make(map[string]struct{}),
	}
	grace := coordinatorSnapshotFixture(t, now, visitsession.LifecycleOwnerGrace, 3, true)
	coordinator.MutationCommitted(context.Background(), coordinatorSimpleResult(t, visitsession.OperationOwnerDisconnect, grace, "vcmd_fixtureGrace", nil))
	if wss.count(2101) != 1 {
		t.Fatalf("owner availability count = %d", wss.count(2101))
	}
	closed := coordinatorSnapshotFixture(t, now, visitsession.LifecycleClosed, 4, false)
	ownerConnection, ok := tcpConnectionFromVisitBinding(closed.OwnerBinding().ConnectionID())
	if !ok {
		t.Fatal("owner binding did not map to gameplay connection")
	}
	tcp.addConnection(ownerConnection)
	target, _ := account.NewPlayerID("ply_targetFixture")
	directive, _ := visitsession.NewSafeReturnDirective(closed.ID(), target, visitsession.SafeReturnReasonAssignmentChanged, closed.Revision())
	coordinator.MutationCommitted(context.Background(), coordinatorSimpleResult(t, visitsession.OperationInvalidateAssignment, closed, "vcmd_fixtureInvalidation", []visitsession.SafeReturnDirective{directive}))
	if wss.count(2003) != 2 || wss.count(2102) != 2 || tcp.visitorCount(2122) != 1 {
		t.Fatalf("assignment effects changed=%d closed=%d return=%d", wss.count(2003), wss.count(2102), tcp.visitorCount(2122))
	}
}

// TestVisitCoordinatorReconcilesStaleOpen 验证显式Open恢复会关闭旧访问并只投递一次完整副作用。
func TestVisitCoordinatorReconcilesStaleOpen(t *testing.T) {
	t.Parallel()
	fixture := newStaleOpenCoordinatorFixture(t, false)
	if err := fixture.coordinator.ReconcileStaleOpen(context.Background(), fixture.worldID); err != nil {
		t.Fatal(err)
	}
	if active, outcome, err := fixture.store.ResolveActive(context.Background(), fixture.worldID); err != nil || outcome != visitsession.ResolveOutcomeNotFound || active.Valid() {
		t.Fatalf("stale active index survived: snapshot=%#v outcome=%v err=%v", active, outcome, err)
	}
	closed, outcome, err := fixture.store.FindByID(context.Background(), fixture.visitID)
	if err != nil || outcome != visitsession.ResolveOutcomeFound || closed.Lifecycle() != visitsession.LifecycleClosed || !closed.Assignment().Equal(fixture.oldAssignment) {
		t.Fatalf("terminal snapshot drifted: snapshot=%#v outcome=%v err=%v", closed, outcome, err)
	}
	if fixture.wss.count(2003) != 1 || fixture.wss.count(2102) != 2 || fixture.tcp.visitorCount(2122) != 1 {
		t.Fatalf("terminal effects assignment=%d closed=%d safe_return=%d", fixture.wss.count(2003), fixture.wss.count(2102), fixture.tcp.visitorCount(2122))
	}
	if err := fixture.coordinator.ReconcileStaleOpen(context.Background(), fixture.worldID); err != nil {
		t.Fatal(err)
	}
	if fixture.wss.count(2003) != 1 || fixture.tcp.visitorCount(2122) != 1 {
		t.Fatal("resolved stale open repeated terminal effects")
	}
}

// TestVisitCoordinatorStaleOpenKeepsCurrentSession 验证并发恢复已创建current session时不会误关新事实。
func TestVisitCoordinatorStaleOpenKeepsCurrentSession(t *testing.T) {
	t.Parallel()
	fixture := newStaleOpenCoordinatorFixture(t, true)
	if err := fixture.coordinator.ReconcileStaleOpen(context.Background(), fixture.worldID); err != nil {
		t.Fatal(err)
	}
	active, outcome, err := fixture.store.ResolveActive(context.Background(), fixture.worldID)
	if err != nil || outcome != visitsession.ResolveOutcomeFound || !active.Assignment().Equal(fixture.currentAssignment) || active.Lifecycle() != visitsession.LifecycleOpen {
		t.Fatalf("current session was changed: snapshot=%#v outcome=%v err=%v", active, outcome, err)
	}
	if fixture.wss.count(2003) != 0 || fixture.tcp.visitorCount(2122) != 0 {
		t.Fatal("current session produced stale recovery effects")
	}
}

// TestVisitCoordinatorStaleOpenCommitUnknownFailsClosed 验证不确定终态不会继续或投递伪成功。
func TestVisitCoordinatorStaleOpenCommitUnknownFailsClosed(t *testing.T) {
	t.Parallel()
	fixture := newStaleOpenCoordinatorFixture(t, false)
	fixture.store.failNext(visitsession.MutationOutcomeCommitUnknown, errors.New("injected response loss"))
	err := fixture.coordinator.ReconcileStaleOpen(context.Background(), fixture.worldID)
	if !visitsession.IsErrorCode(err, visitsession.ErrorCodeCommitUnknown) {
		t.Fatalf("commit unknown was not preserved: %v", err)
	}
	active, outcome, resolveErr := fixture.store.ResolveActive(context.Background(), fixture.worldID)
	if resolveErr != nil || outcome != visitsession.ResolveOutcomeFound || active.ID() != fixture.visitID || active.Lifecycle() != visitsession.LifecycleOpen {
		t.Fatalf("unknown commit fabricated terminal state: snapshot=%#v outcome=%v err=%v", active, outcome, resolveErr)
	}
	if fixture.wss.count(2003) != 0 || fixture.tcp.visitorCount(2122) != 0 {
		t.Fatal("unknown commit published terminal effects")
	}
}

// TestVisitCoordinatorConcurrentStaleOpenDeduplicates 验证并发恢复共享system identity和首次副作用。
func TestVisitCoordinatorConcurrentStaleOpenDeduplicates(t *testing.T) {
	t.Parallel()
	fixture := newStaleOpenCoordinatorFixture(t, false)
	const workers = 12
	errorsByWorker := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsByWorker <- fixture.coordinator.ReconcileStaleOpen(context.Background(), fixture.worldID)
		}()
	}
	wait.Wait()
	close(errorsByWorker)
	for err := range errorsByWorker {
		if err != nil {
			t.Fatal(err)
		}
	}
	if fixture.wss.count(2003) != 1 || fixture.wss.count(2102) != 2 || fixture.tcp.visitorCount(2122) != 1 {
		t.Fatalf("concurrent effects assignment=%d closed=%d safe_return=%d", fixture.wss.count(2003), fixture.wss.count(2102), fixture.tcp.visitorCount(2122))
	}
}

// TestVisitBindingRoundTrip 验证 transport 与 domain connection namespace 的可逆边界。
func TestVisitBindingRoundTrip(t *testing.T) {
	t.Parallel()
	binding, err := visitsession.NewConnectionBindingID("vbind_0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	connectionID, ok := tcpConnectionFromVisitBinding(binding)
	if !ok || connectionID != "tcp_0123456789abcdef" {
		t.Fatalf("round trip = %q, %t", connectionID, ok)
	}
}

// TestVisitCoordinatorEffectCacheUsesBoundedFIFO 验证环形淘汰保留最近 CommandID 且不会扩容。
func TestVisitCoordinatorEffectCacheUsesBoundedFIFO(t *testing.T) {
	t.Parallel()
	coordinator := &personalWorldVisitCoordinator{
		maximumEffects: 2,
		effects:        make(map[string]struct{}, 2),
		effectOrder:    make([]string, 0, 2),
	}
	if !coordinator.claimEffect("first") || !coordinator.claimEffect("second") || !coordinator.claimEffect("third") {
		t.Fatal("new command was unexpectedly treated as replay")
	}
	if coordinator.claimEffect("third") {
		t.Fatal("recent command replay was not rejected")
	}
	if !coordinator.claimEffect("first") {
		t.Fatal("oldest command was not evicted")
	}
	if len(coordinator.effects) != 2 || len(coordinator.effectOrder) != 2 {
		t.Fatalf("effect cache grew beyond bound: map=%d order=%d", len(coordinator.effects), len(coordinator.effectOrder))
	}
}

// coordinatorSnapshotFixture 构造只包含 Owner 与可选 pending invite 的规范 snapshot。
func coordinatorSnapshotFixture(t *testing.T, now time.Time, lifecycle visitsession.Lifecycle, revision uint64, withInvite bool) visitsession.Snapshot {
	t.Helper()
	owner, _ := account.NewPlayerID("ply_ownerFixture")
	target, _ := account.NewPlayerID("ply_targetFixture")
	worldID, _ := personalworld.NewPersonalWorldID("pworld_fixture")
	instanceID, _ := placement.NewWorldInstanceID("winst_fixture")
	nodeID, _ := placement.NewRuntimeNodeID("rnode_fixture")
	generation, _ := placement.NewAssignmentGeneration(1)
	fence, _ := placement.NewFencingToken(1)
	stamp, _ := placement.NewAssignmentStamp(worldID, instanceID, nodeID, generation, fence)
	sessionID, _ := session.NewSessionID("ses_ownerFixture")
	bindingID, _ := visitsession.NewConnectionBindingID("vbind_ownerFixture")
	binding, _ := visitsession.HydrateAuthBinding(owner, sessionID, session.InitialEpoch, bindingID)
	visitID, _ := visitsession.NewVisitSessionID("vses_fixture")
	capacity, _ := visitsession.NewCapacity(2)
	var invites []visitsession.InviteSnapshot
	if withInvite {
		inviteID, _ := visitsession.NewInviteID("vinv_fixture")
		createdRevision, _ := visitsession.NewRevision(revision)
		invite, _ := visitsession.NewInviteSnapshot(inviteID, target, visitsession.InviteStatePending, createdRevision, now.Add(time.Minute))
		invites = []visitsession.InviteSnapshot{invite}
	}
	graceGeneration, graceDeadline := uint64(0), time.Time{}
	if lifecycle == visitsession.LifecycleOwnerGrace {
		graceGeneration, graceDeadline = 1, now.Add(20*time.Second)
	}
	snapshot, err := visitsession.NewSnapshot(visitID, owner, worldID, stamp, lifecycle, visitsession.Revision(revision), capacity, now, now.Add(time.Hour), binding, graceGeneration, graceDeadline, invites, nil)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// coordinatorSimpleResult 构造不携带invite/admission/membership projection的规范mutation。
func coordinatorSimpleResult(t *testing.T, operation visitsession.Operation, snapshot visitsession.Snapshot, commandValue string, directives []visitsession.SafeReturnDirective) visitsession.MutationResult {
	t.Helper()
	commandID, _ := visitsession.NewCommandID(commandValue)
	digest := sha256.Sum256([]byte(commandValue))
	fingerprint, _ := visitsession.NewCommandFingerprint(digest)
	result, err := visitsession.NewMutationResult(operation, snapshot, commandID, fingerprint, visitsession.InviteSnapshot{}, visitsession.AdmissionIntent{}, visitsession.MembershipSnapshot{}, directives)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// coordinatorCreateInviteResult 构造可 replay 的 create-invite 原子结果。
func coordinatorCreateInviteResult(t *testing.T, snapshot visitsession.Snapshot) visitsession.MutationResult {
	t.Helper()
	commandID, _ := visitsession.NewCommandID("vcmd_fixtureInvite")
	digest := sha256.Sum256([]byte("fixture invite"))
	fingerprint, _ := visitsession.NewCommandFingerprint(digest)
	result, err := visitsession.NewMutationResult(visitsession.OperationCreateInvite, snapshot, commandID, fingerprint, snapshot.Invites()[0], visitsession.AdmissionIntent{}, visitsession.MembershipSnapshot{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// coordinatorCloseResult 构造没有 Visitor 时的 terminal close 结果。
func coordinatorCloseResult(t *testing.T, snapshot visitsession.Snapshot) visitsession.MutationResult {
	t.Helper()
	commandID, _ := visitsession.NewCommandID("vcmd_fixtureClose")
	digest := sha256.Sum256([]byte("fixture close"))
	fingerprint, _ := visitsession.NewCommandFingerprint(digest)
	result, err := visitsession.NewMutationResult(visitsession.OperationClose, snapshot, commandID, fingerprint, visitsession.InviteSnapshot{}, visitsession.AdmissionIntent{}, visitsession.MembershipSnapshot{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// coordinatorRetiredInviteResult 构造只携带一个精确退役事实的原子结果。
func coordinatorRetiredInviteResult(t *testing.T, snapshot visitsession.Snapshot, retired visitsession.InviteSnapshot) visitsession.MutationResult {
	t.Helper()
	commandID, _ := visitsession.NewCommandID("vcmd_fixtureRetire")
	digest := sha256.Sum256([]byte("fixture retire"))
	fingerprint, _ := visitsession.NewCommandFingerprint(digest)
	result, err := visitsession.NewMutationResultWithRetiredInvites(visitsession.OperationRevokeInvite, snapshot, commandID, fingerprint, visitsession.InviteSnapshot{}, visitsession.AdmissionIntent{}, visitsession.MembershipSnapshot{}, []visitsession.InviteSnapshot{retired}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// staleOpenCoordinatorFixture 汇总旧assignment恢复所需的真实领域Service与可观察副作用。
type staleOpenCoordinatorFixture struct {
	// coordinator 是待测application恢复owner。
	coordinator *personalWorldVisitCoordinator
	// service 提供真实领域错误与终态命令边界。
	service *visitsession.Service
	// store 保存可线性化的active/terminal VisitSession事实。
	store *staleOpenVisitStore
	// wss 记录assignment与close控制通知。
	wss *recordingVisitWSS
	// tcp 记录精确Visitor safe-return。
	tcp *recordingVisitTCP
	// worldID 是active index的稳定目标。
	worldID personalworld.PersonalWorldID
	// visitID 是旧aggregate identity。
	visitID visitsession.VisitSessionID
	// oldAssignment 必须保留在terminal snapshot中。
	oldAssignment placement.AssignmentStamp
	// currentAssignment 是placement恢复后的唯一current stamp。
	currentAssignment placement.AssignmentStamp
	// now 是所有fixture deadline的绝对时间锚点。
	now time.Time
}

// newStaleOpenCoordinatorFixture 构造旧session或已经由并发恢复创建的current session。
func newStaleOpenCoordinatorFixture(t *testing.T, currentActive bool) staleOpenCoordinatorFixture {
	t.Helper()
	now := time.Date(2026, 7, 20, 15, 0, 0, 0, time.UTC)
	worldID, _ := personalworld.NewPersonalWorldID("pworld_staleOpenFixture")
	oldInstance, _ := placement.NewWorldInstanceID("winst_staleOpenOld")
	oldNode, _ := placement.NewRuntimeNodeID("rnode_staleOpenOld")
	oldAssignment, _ := placement.NewAssignmentStamp(worldID, oldInstance, oldNode, placement.AssignmentGeneration(1), placement.FencingToken(1))
	currentInstance, _ := placement.NewWorldInstanceID("winst_staleOpenCurrent")
	currentNode, _ := placement.NewRuntimeNodeID("rnode_staleOpenCurrent")
	currentAssignment, _ := placement.NewAssignmentStamp(worldID, currentInstance, currentNode, placement.AssignmentGeneration(2), placement.FencingToken(2))
	assignment := oldAssignment
	if currentActive {
		assignment = currentAssignment
	}
	owner, _ := account.NewPlayerID("ply_staleOpenOwner")
	ownerSession, _ := session.NewSessionID("ses_staleOpenOwner")
	ownerBindingID, _ := visitsession.NewConnectionBindingID("vbind_staleOpenOwner")
	ownerBinding, _ := visitsession.HydrateAuthBinding(owner, ownerSession, session.InitialEpoch, ownerBindingID)
	visitor, _ := account.NewPlayerID("ply_staleOpenVisitor")
	visitorSession, _ := session.NewSessionID("ses_staleOpenVisitor")
	visitorBinding, _ := visitsession.NewConnectionBindingID("vbind_staleOpenVisitor")
	inviteID, _ := visitsession.NewInviteID("vinv_staleOpenVisitor")
	acceptedInvite, err := visitsession.NewInviteSnapshot(inviteID, visitor, visitsession.InviteStateAccepted, visitsession.InitialRevision, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	membership, err := visitsession.NewMembershipSnapshot(visitor, inviteID, visitsession.MembershipStateJoined, visitorSession, session.InitialEpoch, visitorBinding, time.Time{}, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	visitID, _ := visitsession.NewVisitSessionID("vses_staleOpenFixture")
	capacity, _ := visitsession.NewCapacity(2)
	snapshot, err := visitsession.NewSnapshot(visitID, owner, worldID, assignment, visitsession.LifecycleOpen, visitsession.InitialRevision, capacity, now, now.Add(time.Hour), ownerBinding, 0, time.Time{}, []visitsession.InviteSnapshot{acceptedInvite}, []visitsession.MembershipSnapshot{membership})
	if err != nil {
		t.Fatal(err)
	}
	store := newStaleOpenVisitStore(snapshot)
	currentSnapshot, err := placement.NewAssignmentSnapshot(currentAssignment, placement.PhaseActive, now, now.Add(time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	clock := newManualSemanticClock(now)
	policy, _ := visitsession.NewPolicy(capacity, time.Hour, time.Minute, 30*time.Second, 20*time.Second, 15*time.Second)
	service, err := visitsession.NewService(store, staleOpenWorldReader{}, staleOpenPlayerReader{}, staleOpenAssignmentReader{snapshot: currentSnapshot}, clock, staleOpenIDGenerator{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	deadlines, _ := newSemanticDeadlineOwner(16, clock)
	wss := newRecordingVisitWSS()
	tcp := newRecordingVisitTCP()
	coordinator := &personalWorldVisitCoordinator{
		visits: service, deadlines: deadlines, clock: clock, policy: policy, wss: wss, tcp: tcp, connections: tcp,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)), observer: observability.NewMetrics(), maximumEffects: 16, failClosed: func() {},
		schedules: make(map[string]visitScheduleState), effects: make(map[string]struct{}),
		projector: func(_ context.Context, snapshot visitsession.Snapshot) (*visitv1.VisitSessionSnapshot, error) {
			return visitv1.VisitSessionSnapshot_builder{VisitSessionId: proto.String(snapshot.ID().Value()), OwnerPlayerId: proto.String(snapshot.OwnerID().String())}.Build(), nil
		},
	}
	return staleOpenCoordinatorFixture{coordinator: coordinator, service: service, store: store, wss: wss, tcp: tcp, worldID: worldID, visitID: visitID, oldAssignment: oldAssignment, currentAssignment: currentAssignment, now: now}
}

// staleOpenWorldReader 仅满足Service构造；system invalidation不会读取持久world。
type staleOpenWorldReader struct{}

// ResolveOwnedWorld 对未使用端口返回确定性not-found。
func (staleOpenWorldReader) ResolveOwnedWorld(context.Context, account.PlayerID) (personalworld.Snapshot, visitsession.OwnedWorldOutcome, error) {
	return personalworld.Snapshot{}, visitsession.OwnedWorldOutcomeNotFound, nil
}

// staleOpenPlayerReader 满足未由恢复路径调用的 Account 可邀请性端口。
type staleOpenPlayerReader struct{}

// ResolveInvitablePlayer 对恢复fixture返回available；该路径不会创建邀请。
func (staleOpenPlayerReader) ResolveInvitablePlayer(context.Context, account.PlayerID) (account.InvitablePlayerOutcome, error) {
	return account.InvitablePlayerOutcomeAvailable, nil
}

// staleOpenAssignmentReader 返回重启后current assignment的稳定快照。
type staleOpenAssignmentReader struct {
	// snapshot 是placement权威active值。
	snapshot placement.AssignmentSnapshot
}

// ResolveCurrent 只允许matching world读取current assignment。
func (reader staleOpenAssignmentReader) ResolveCurrent(_ context.Context, worldID personalworld.PersonalWorldID, _ time.Time) (placement.AssignmentSnapshot, visitsession.AssignmentOutcome, error) {
	if !reader.snapshot.Valid() || reader.snapshot.WorldID() != worldID {
		return placement.AssignmentSnapshot{}, visitsession.AssignmentOutcomeNotFound, nil
	}
	return reader.snapshot, visitsession.AssignmentOutcomeFound, nil
}

// staleOpenIDGenerator 仅满足Service构造；恢复不创建entity identity。
type staleOpenIDGenerator struct{}

// NewID 返回合法固定材料；测试若意外进入create仍可被领域校验。
func (staleOpenIDGenerator) NewID() (string, error) { return "staleOpenGenerated", nil }

// staleOpenVisitCommand 保存system command首次完整结果供并发replay。
type staleOpenVisitCommand struct {
	// fingerprint 绑定首次invalidation语义。
	fingerprint visitsession.CommandFingerprint
	// result 是首次terminal mutation完整结果。
	result visitsession.MutationResult
}

// staleOpenVisitStore 是仅覆盖恢复路径的并发线性化测试store。
type staleOpenVisitStore struct {
	// mutex 保护active、snapshots、commands与一次性故障注入。
	mutex sync.Mutex
	// active 是world active index指向的未关闭snapshot。
	active visitsession.Snapshot
	// snapshots 同时保留active与terminal aggregate。
	snapshots map[string]visitsession.Snapshot
	// commands 为相同system identity提供完整replay。
	commands map[string]staleOpenVisitCommand
	// nextOutcome 注入下一次Commit的确定性或不确定结果。
	nextOutcome visitsession.MutationOutcome
	// nextError 是故障结果的依赖cause。
	nextError error
}

// newStaleOpenVisitStore 以一个合法active snapshot初始化测试store。
func newStaleOpenVisitStore(snapshot visitsession.Snapshot) *staleOpenVisitStore {
	return &staleOpenVisitStore{active: snapshot, snapshots: map[string]visitsession.Snapshot{snapshot.ID().Value(): snapshot}, commands: make(map[string]staleOpenVisitCommand)}
}

// failNext 注入一次Commit outcome，不修改任何已存事实。
func (store *staleOpenVisitStore) failNext(outcome visitsession.MutationOutcome, err error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.nextOutcome, store.nextError = outcome, err
}

// Create 不属于该恢复fixture；调用即返回可确认未提交。
func (*staleOpenVisitStore) Create(context.Context, visitsession.CreateRecord) (visitsession.CreateResult, visitsession.CreateOutcome, error) {
	return visitsession.CreateResult{}, visitsession.CreateOutcomeNotCommitted, errors.New("stale open fixture does not implement create")
}

// ResolveActive 在线性化点返回当前未关闭snapshot。
func (store *staleOpenVisitStore) ResolveActive(_ context.Context, worldID personalworld.PersonalWorldID) (visitsession.Snapshot, visitsession.ResolveOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if !store.active.Valid() {
		return visitsession.Snapshot{}, visitsession.ResolveOutcomeNotFound, nil
	}
	if store.active.WorldID() != worldID || store.active.Lifecycle() == visitsession.LifecycleClosed {
		return visitsession.Snapshot{}, visitsession.ResolveOutcomeUnspecified, errors.New("stale open fixture active index is inconsistent")
	}
	return store.active, visitsession.ResolveOutcomeFound, nil
}

// FindByID 返回active或terminal完整snapshot。
func (store *staleOpenVisitStore) FindByID(_ context.Context, visitID visitsession.VisitSessionID) (visitsession.Snapshot, visitsession.ResolveOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	snapshot, found := store.snapshots[visitID.Value()]
	if !found {
		return visitsession.Snapshot{}, visitsession.ResolveOutcomeNotFound, nil
	}
	return snapshot, visitsession.ResolveOutcomeFound, nil
}

// Commit 原子决议system command replay、revision与terminal active-index删除。
func (store *staleOpenVisitStore) Commit(_ context.Context, record visitsession.TransitionRecord) (visitsession.MutationResult, visitsession.MutationOutcome, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if command, found := store.commands[record.CommandID().Value()]; found {
		if !command.fingerprint.Equal(record.Fingerprint()) {
			return visitsession.MutationResult{}, visitsession.MutationOutcomeIdempotencyConflict, nil
		}
		return command.result, visitsession.MutationOutcomeReplay, nil
	}
	if store.nextOutcome != visitsession.MutationOutcomeUnspecified {
		outcome, err := store.nextOutcome, store.nextError
		store.nextOutcome, store.nextError = visitsession.MutationOutcomeUnspecified, nil
		return visitsession.MutationResult{}, outcome, err
	}
	current, found := store.snapshots[record.VisitSessionID().Value()]
	if !found {
		return visitsession.MutationResult{}, visitsession.MutationOutcomeNotFound, nil
	}
	if current.Revision() != record.ExpectedRevision() {
		return visitsession.MutationResult{}, visitsession.MutationOutcomeRevisionConflict, nil
	}
	if !record.HasResult() {
		return visitsession.MutationResult{}, visitsession.MutationOutcomeInvalidState, nil
	}
	result := record.Result()
	store.snapshots[record.VisitSessionID().Value()] = result.Snapshot()
	if result.Snapshot().Lifecycle() == visitsession.LifecycleClosed && store.active.Valid() && store.active.ID() == record.VisitSessionID() {
		store.active = visitsession.Snapshot{}
	} else {
		store.active = result.Snapshot()
	}
	store.commands[record.CommandID().Value()] = staleOpenVisitCommand{fingerprint: record.Fingerprint(), result: result}
	return result, visitsession.MutationOutcomeApplied, nil
}

// recordingVisitWSS 记录玩家、message ID 与业务 payload。
type recordingVisitWSS struct {
	// mutex 保护并发恢复测试产生的记录。
	mutex sync.Mutex
	// records 保存可断言的精确定向投递。
	records []recordingVisitWSSRecord
}

// recordingVisitWSSRecord 保存一次精确玩家控制消息投递。
type recordingVisitWSSRecord struct {
	// playerID 是控制消息的唯一目标玩家。
	playerID string
	// messageID 是冻结 registry message ID。
	messageID uint32
	// payload 是已构造的 protobuf 消息。
	payload proto.Message
}

// newRecordingVisitWSS 创建保存精确定向 payload 的 WSS recorder。
func newRecordingVisitWSS() *recordingVisitWSS { return new(recordingVisitWSS) }

// PublishPlayer 记录一次玩家定向 message ID。
func (publisher *recordingVisitWSS) PublishPlayer(playerID string, messageID uint32, payload proto.Message) (wscontrol.DeliveryResult, error) {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()
	publisher.records = append(publisher.records, recordingVisitWSSRecord{playerID: playerID, messageID: messageID, payload: payload})
	return wscontrol.DeliveryResult{Matched: 1, Enqueued: 1}, nil
}

// count 返回指定 WSS message ID 的投递次数。
func (publisher *recordingVisitWSS) count(messageID uint32) int {
	return len(publisher.recordsFor(messageID))
}

// recordsFor 返回指定 message ID 的快照记录。
func (publisher *recordingVisitWSS) recordsFor(messageID uint32) []recordingVisitWSSRecord {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()
	records := make([]recordingVisitWSSRecord, 0)
	for _, record := range publisher.records {
		if record.messageID == messageID {
			records = append(records, record)
		}
	}
	return records
}

// recordingVisitTCP 分别记录精确 connection、visit 与 visitor 投递。
type recordingVisitTCP struct {
	// activeConnections 保存当前进程仍可解析的精确 gameplay connection。
	activeConnections map[string]struct{}
	// connections 保存精确 connection 投递的 message ID。
	connections []uint32
	// visits 保存 VisitSession 范围投递的 message ID。
	visits []uint32
	// visitors 保存 Visitor 交集投递的 message ID。
	visitors []uint32
}

// newRecordingVisitTCP 创建按目标类别分组的 TCP recorder。
func newRecordingVisitTCP() *recordingVisitTCP {
	return &recordingVisitTCP{activeConnections: make(map[string]struct{})}
}

// HasConnection 返回精确 gameplay connection 是否仍归当前进程所有。
func (publisher *recordingVisitTCP) HasConnection(connectionID string) bool {
	_, found := publisher.activeConnections[connectionID]
	return found
}

// addConnection 登记当前进程仍存活的 gameplay connection。
func (publisher *recordingVisitTCP) addConnection(connectionID string) {
	publisher.activeConnections[connectionID] = struct{}{}
}

// PublishConnection 记录精确 connection 投递。
func (publisher *recordingVisitTCP) PublishConnection(_ string, messageID uint32, _ proto.Message) (tcpgameplay.DeliveryResult, error) {
	publisher.connections = append(publisher.connections, messageID)
	return tcpgameplay.DeliveryResult{Matched: 1, Enqueued: 1}, nil
}

// PublishVisit 记录 VisitSession 范围投递。
func (publisher *recordingVisitTCP) PublishVisit(_ string, messageID uint32, _ proto.Message) (tcpgameplay.DeliveryResult, error) {
	publisher.visits = append(publisher.visits, messageID)
	return tcpgameplay.DeliveryResult{Matched: 1, Enqueued: 1}, nil
}

// PublishVisitor 记录 VisitSession 与 Visitor 交集投递。
func (publisher *recordingVisitTCP) PublishVisitor(_, _ string, messageID uint32, _ proto.Message) (tcpgameplay.DeliveryResult, error) {
	publisher.visitors = append(publisher.visitors, messageID)
	return tcpgameplay.DeliveryResult{Matched: 1, Enqueued: 1}, nil
}

// connectionCount 返回精确 connection message 的投递次数。
func (publisher *recordingVisitTCP) connectionCount(messageID uint32) int {
	return countVisitMessages(publisher.connections, messageID)
}

// visitCount 返回 VisitSession message 的投递次数。
func (publisher *recordingVisitTCP) visitCount(messageID uint32) int {
	return countVisitMessages(publisher.visits, messageID)
}

// visitorCount 返回 Visitor message 的投递次数。
func (publisher *recordingVisitTCP) visitorCount(messageID uint32) int {
	return countVisitMessages(publisher.visitors, messageID)
}

// countVisitMessages 统计低基数 message ID。
func countVisitMessages(messages []uint32, messageID uint32) int {
	count := 0
	for _, candidate := range messages {
		if candidate == messageID {
			count++
		}
	}
	return count
}
