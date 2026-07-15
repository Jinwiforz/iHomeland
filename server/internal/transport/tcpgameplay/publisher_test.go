package tcpgameplay

import (
	"net"
	"testing"
	"time"

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

// TestPublisherSafeReturnTransitionsBeforeClose 验证safe-return入队后立即封闭旧target mutation。
func TestPublisherSafeReturnTransitionsBeforeClose(t *testing.T) {
	t.Parallel()
	config, codec, registry := testRuntime(t)
	config.Policy.CloseTimeout = 100 * time.Millisecond
	registry.config = config
	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()
	entry := testPublisherEntry(codec, serverSide)
	registry.connections[entry.id] = entry
	registry.byVisit[entry.visitID] = map[string]struct{}{entry.id: {}}
	publisher, _ := NewPublisher(registry, new(testObserver))
	directive := visitv1.SafeReturnDirective_builder{VisitSessionId: proto.String(entry.visitID), VisitorId: proto.String(entry.playerID)}.Build()
	push := visitv1.VisitSafeReturnPush_builder{Directive: directive}.Build()
	result, err := publisher.PublishVisit(entry.visitID, 2122, push)
	if err != nil || result.Enqueued != 1 || result.Closed != 1 || entry.State() != ConnectionStateReturning {
		t.Fatalf("safe return result=%+v state=%v err=%v", result, entry.State(), err)
	}
	queued := <-entry.queue.items
	entry.queue.take(queued)
	_ = clientSide.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := clientSide.Read(make([]byte, 1)); err == nil {
		t.Fatal("safe-return completion did not close connection")
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
