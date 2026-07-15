package tcpgameplay

import (
	"bytes"
	"net"
	"testing"

	visitv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/visit/v1"
	worldv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/world/v1"
	"google.golang.org/protobuf/proto"
)

// TestPublisherValidatesTargetAndSerializesPush 验证typed target、sequence与队列共享边界。
func TestPublisherValidatesTargetAndSerializesPush(t *testing.T) {
	t.Parallel()
	_, codec, registry := testRuntime(t)
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()
	entry := testPublisherEntry(codec, serverSide)
	registry.connections[entry.id] = entry
	registry.byWorld[entry.worldID] = map[string]struct{}{entry.id: {}}
	publisher, err := NewPublisher(registry, new(testObserver))
	if err != nil {
		t.Fatal(err)
	}
	world := worldv1.PersonalWorldSnapshot_builder{PersonalWorldId: proto.String(entry.worldID)}.Build()
	push := worldv1.WorldSnapshotPush_builder{Snapshot: worldv1.WorldSnapshot_builder{World: world}.Build()}.Build()
	result, err := publisher.PublishWorld(entry.worldID, 2002, push)
	if err != nil || result.Enqueued != 1 {
		t.Fatalf("PublishWorld result=%+v err=%v", result, err)
	}
	queued := <-entry.queue.items
	if queued.encoded.MessageID() != 2002 || entry.nextServerSequence != 2 {
		t.Fatalf("queued message=%d next sequence=%d", queued.encoded.MessageID(), entry.nextServerSequence)
	}
	entry.queue.take(queued)
	wrongWorld := worldv1.PersonalWorldSnapshot_builder{PersonalWorldId: proto.String("pw_wrong")}.Build()
	wrongPush := worldv1.WorldSnapshotPush_builder{Snapshot: worldv1.WorldSnapshot_builder{World: wrongWorld}.Build()}.Build()
	if _, err := publisher.PublishWorld(entry.worldID, 2002, wrongPush); err == nil {
		t.Fatal("payload target override was accepted")
	}
}

// TestSafeReturnFromCurrentCommandFollowsResponse 验证自身 leave 的关闭帧不会抢先终止 command response。
func TestSafeReturnFromCurrentCommandFollowsResponse(t *testing.T) {
	t.Parallel()
	_, codec, registry := testRuntime(t)
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()
	entry := testPublisherEntry(codec, serverSide)
	registry.connections[entry.id] = entry
	registry.byVisit[entry.visitID] = map[string]struct{}{entry.id: {}}
	registry.byPlayer[entry.playerID] = map[string]struct{}{entry.id: {}}
	publisher, _ := NewPublisher(registry, new(testObserver))
	if err := entry.beginDispatch(); err != nil {
		t.Fatal(err)
	}
	directive := visitv1.SafeReturnDirective_builder{VisitSessionId: proto.String(entry.visitID), VisitorId: proto.String(entry.playerID)}.Build()
	push := visitv1.VisitSafeReturnPush_builder{Directive: directive}.Build()
	if _, err := publisher.PublishVisitor(entry.visitID, entry.playerID, 2122, push); err != nil {
		t.Fatal(err)
	}
	if len(entry.queue.items) != 0 {
		t.Fatal("safe-return was queued before current command response")
	}
	correlation := Correlation{CommandID: bytes.Repeat([]byte{1}, 16)}
	if err := entry.enqueue(2112, visitv1.VisitLeaveResponse_builder{}.Build(), correlation, nil); err != nil {
		t.Fatal(err)
	}
	if err := entry.finishDispatch(true); err != nil {
		t.Fatal(err)
	}
	response := <-entry.queue.items
	entry.queue.take(response)
	safeReturn := <-entry.queue.items
	entry.queue.take(safeReturn)
	if response.encoded.MessageID() != 2112 || safeReturn.encoded.MessageID() != 2122 || !safeReturn.closeAfter {
		t.Fatalf("queue order = %d, %d close=%v", response.encoded.MessageID(), safeReturn.encoded.MessageID(), safeReturn.closeAfter)
	}
}

// TestPendingPushDropsWhenResponseCannotQueue 验证 response 未入队时不会单独发送当前 command 产生的 PUSH。
func TestPendingPushDropsWhenResponseCannotQueue(t *testing.T) {
	t.Parallel()
	_, codec, _ := testRuntime(t)
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()
	entry := testPublisherEntry(codec, serverSide)
	if err := entry.beginDispatch(); err != nil {
		t.Fatal(err)
	}
	world := worldv1.PersonalWorldSnapshot_builder{PersonalWorldId: proto.String(entry.worldID)}.Build()
	push := worldv1.WorldSnapshotPush_builder{Snapshot: worldv1.WorldSnapshot_builder{World: world}.Build()}.Build()
	if err := entry.enqueuePush(2002, push, false); err != nil {
		t.Fatal(err)
	}
	if err := entry.finishDispatch(false); err != nil {
		t.Fatal(err)
	}
	if len(entry.queue.items) != 0 || entry.dispatching || len(entry.pendingPushes) != 0 || entry.pendingPushBytes != 0 {
		t.Fatal("pending push survived failed response boundary")
	}
}

// TestPublisherSafeReturnTransitionsBeforeClose 验证safe-return入队后立即封闭旧target mutation。
func TestPublisherSafeReturnTransitionsBeforeClose(t *testing.T) {
	t.Parallel()
	_, codec, registry := testRuntime(t)
	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()
	entry := testPublisherEntry(codec, serverSide)
	registry.connections[entry.id] = entry
	registry.byVisit[entry.visitID] = map[string]struct{}{entry.id: {}}
	registry.byPlayer[entry.playerID] = map[string]struct{}{entry.id: {}}
	publisher, _ := NewPublisher(registry, new(testObserver))
	directive := visitv1.SafeReturnDirective_builder{VisitSessionId: proto.String(entry.visitID), VisitorId: proto.String(entry.playerID)}.Build()
	push := visitv1.VisitSafeReturnPush_builder{Directive: directive}.Build()
	result, err := publisher.PublishVisitor(entry.visitID, entry.playerID, 2122, push)
	if err != nil || result.Enqueued != 1 || result.Closed != 1 || entry.State() != ConnectionStateReturning {
		t.Fatalf("safe return result=%+v state=%v err=%v", result, entry.State(), err)
	}
	queued := <-entry.queue.items
	entry.queue.take(queued)
	if !queued.closeAfter || entry.closeClass != CloseClassApplicationReturn {
		t.Fatalf("safe-return queued closeAfter=%v closeClass=%s", queued.closeAfter, entry.closeClass)
	}
}

// TestVisitSnapshotPushAcceptsExactOwnerTarget 验证 own-world connection 不需要伪造 VisitSession 索引。
func TestVisitSnapshotPushAcceptsExactOwnerTarget(t *testing.T) {
	t.Parallel()
	_, codec, _ := testRuntime(t)
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()
	entry := testPublisherEntry(codec, serverSide)
	entry.visitID = ""
	assignment := worldv1.WorldAssignment_builder{PersonalWorldId: proto.String(entry.worldID)}.Build()
	snapshot := visitv1.VisitSessionSnapshot_builder{VisitSessionId: proto.String("visit_target"), OwnerPlayerId: proto.String(entry.playerID), Assignment: assignment}.Build()
	push := visitv1.VisitSnapshotPush_builder{Snapshot: snapshot}.Build()
	if err := validatePushTarget(entry, 2121, push); err != nil {
		t.Fatalf("exact owner target rejected: %v", err)
	}
	snapshot.SetOwnerPlayerId("player_other")
	if err := validatePushTarget(entry, 2121, push); err == nil {
		t.Fatal("mismatched owner target accepted")
	}
}

// testPublisherEntry 创建只含publisher所需受信索引投影的连接。
func testPublisherEntry(codec *Codec, socket net.Conn) *connection {
	return &connection{
		id: "tcp_00000000000000000000000000000001", playerID: "player_test", worldID: "pw_test", visitID: "visit_test", socket: socket,
		queue: newSendQueue(4, 65536), codec: codec, observer: new(testObserver), state: ConnectionStateActive,
		nextServerSequence: 1, nextClientSequence: 1, routeRates: make(map[string]*routeRateState), done: make(chan struct{}),
	}
}
