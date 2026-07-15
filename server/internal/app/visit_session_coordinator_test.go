package app

import (
	"context"
	"crypto/sha256"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	visitv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/visit/v1"
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
	target, _ := account.NewPlayerID("ply_targetFixture")
	directive, _ := visitsession.NewSafeReturnDirective(closed.ID(), target, visitsession.SafeReturnReasonAssignmentChanged)
	coordinator.MutationCommitted(context.Background(), coordinatorSimpleResult(t, visitsession.OperationInvalidateAssignment, closed, "vcmd_fixtureInvalidation", []visitsession.SafeReturnDirective{directive}))
	if wss.count(2003) != 2 || wss.count(2102) != 2 || tcp.visitorCount(2122) != 1 {
		t.Fatalf("assignment effects changed=%d closed=%d return=%d", wss.count(2003), wss.count(2102), tcp.visitorCount(2122))
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

// recordingVisitWSS 记录 message ID，不保存业务 payload。
type recordingVisitWSS struct {
	// messages 只保存低敏 message ID。
	messages []uint32
}

// newRecordingVisitWSS 创建不保存 payload 的 WSS recorder。
func newRecordingVisitWSS() *recordingVisitWSS { return new(recordingVisitWSS) }

// PublishPlayer 记录一次玩家定向 message ID。
func (publisher *recordingVisitWSS) PublishPlayer(_ string, messageID uint32, _ proto.Message) (wscontrol.DeliveryResult, error) {
	publisher.messages = append(publisher.messages, messageID)
	return wscontrol.DeliveryResult{Matched: 1, Enqueued: 1}, nil
}

// count 返回指定 WSS message ID 的投递次数。
func (publisher *recordingVisitWSS) count(messageID uint32) int {
	count := 0
	for _, candidate := range publisher.messages {
		if candidate == messageID {
			count++
		}
	}
	return count
}

// recordingVisitTCP 分别记录精确 connection、visit 与 visitor 投递。
type recordingVisitTCP struct {
	// connections 保存精确 connection 投递的 message ID。
	connections []uint32
	// visits 保存 VisitSession 范围投递的 message ID。
	visits []uint32
	// visitors 保存 Visitor 交集投递的 message ID。
	visitors []uint32
}

// newRecordingVisitTCP 创建按目标类别分组的 TCP recorder。
func newRecordingVisitTCP() *recordingVisitTCP { return new(recordingVisitTCP) }

// HasConnection 固定返回 false，测试不模拟连接抢占。
func (*recordingVisitTCP) HasConnection(string) bool { return false }

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
