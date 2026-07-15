package tcpgameplay

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	worldv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/world/v1"
	"github.com/jinwiforz/ihomeland/server/internal/protocol"
)

// testNow 是TCP测试共享的合法确定性绝对时间。
var testNow = time.UnixMilli(1_700_000_000_000)

// TestSendQueueEnforcesItemByteAndCloseBounds 验证双预算、完成结果与幂等关闭。
func TestSendQueueEnforcesItemByteAndCloseBounds(t *testing.T) {
	t.Parallel()
	queue := newSendQueue(2, 8)
	message := EncodedMessage{messageID: 2002, frame: []byte{1, 2, 3, 4}}
	completion := make(chan error, 1)
	if err := queue.tryPush(message, completion); err != nil {
		t.Fatal(err)
	}
	if err := queue.tryPush(message, nil); err != nil {
		t.Fatal(err)
	}
	if err := queue.tryPush(message, nil); err != ErrQueueFull {
		t.Fatalf("third push error = %v", err)
	}
	queue.close()
	queue.close()
	if err := queue.tryPush(message, nil); err != ErrQueueClosed {
		t.Fatalf("closed push error = %v", err)
	}
	queue.release()
	if err := <-completion; err != ErrQueueClosed {
		t.Fatalf("completion = %v", err)
	}
	if items, encodedBytes := queue.snapshot(); items != 0 || encodedBytes != 0 {
		t.Fatalf("queue retained resources: items=%d bytes=%d", items, encodedBytes)
	}
}

// TestRegistryReservationsStayWithinGlobalAndRemoteLimits 验证storm并发下reservation线性化且无泄漏。
func TestRegistryReservationsStayWithinGlobalAndRemoteLimits(t *testing.T) {
	t.Parallel()
	runtimeConfig, codec, _ := testRuntime(t)
	runtimeConfig.Policy.MaxConnections = 8
	runtimeConfig.Policy.MaxPerRemote = 8
	runtimeConfig.Policy.MaxPerTarget = 8
	runtimeConfig.Policy.PreAuthRate.Burst = 64
	runtimeConfig.Policy.PreAuthRate.Requests = 64
	registry, err := NewRegistry(runtimeConfig, codec, fixedClock{value: testNow}, new(testIDs), new(testObserver))
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	accepted := make(chan *reservation, 64)
	for range 64 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if reserved, err := registry.Reserve("127.0.0.1"); err == nil {
				accepted <- reserved
			}
		}()
	}
	wait.Wait()
	close(accepted)
	values := make([]*reservation, 0, 8)
	for reserved := range accepted {
		values = append(values, reserved)
	}
	if len(values) != 8 {
		t.Fatalf("accepted reservations = %d, want 8", len(values))
	}
	for _, reserved := range values {
		reserved.Release()
		reserved.Release()
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.reserved != 0 || registry.remotes["127.0.0.1"].reserved != 0 {
		t.Fatalf("reservation leaked: global=%d remote=%d", registry.reserved, registry.remotes["127.0.0.1"].reserved)
	}
}

// TestSerializedWriterPreservesConcurrentSequenceAndFrames 验证response/push共享唯一writer与单调sequence。
func TestSerializedWriterPreservesConcurrentSequenceAndFrames(t *testing.T) {
	t.Parallel()
	runtimeConfig, codec, registry := testRuntime(t)
	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()
	entry := &connection{
		id: "tcp_00000000000000000000000000000001", socket: serverSide, codec: codec, observer: new(testObserver),
		queue: newSendQueue(16, runtimeConfig.Policy.QueueBytes), state: ConnectionStateActive,
		nextServerSequence: 1, nextClientSequence: 1, routeRates: make(map[string]*routeRateState), done: make(chan struct{}),
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	writerDone := make(chan error, 1)
	go func() { writerDone <- registry.writeLoop(ctx, entry) }()
	const count = 8
	var wait sync.WaitGroup
	for range count {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := entry.enqueue(2002, worldv1.WorldSnapshotPush_builder{}.Build(), Correlation{}, nil); err != nil {
				t.Errorf("enqueue: %v", err)
			}
		}()
	}
	wait.Wait()
	for sequence := uint64(1); sequence <= count; sequence++ {
		payload := readPipeFrame(t, clientSide)
		envelope, err := protocol.UnmarshalEnvelope(payload)
		if err != nil || envelope.GetSequence() != sequence || envelope.GetMessageId() != 2002 {
			t.Fatalf("frame %d envelope=%v err=%v", sequence, envelope, err)
		}
	}
	cancel(context.Canceled)
	_ = serverSide.Close()
	select {
	case <-writerDone:
	case <-time.After(time.Second):
		t.Fatal("writer did not exit")
	}
}

// TestReadFrameRejectsOversizeBeforePayloadAllocation 验证声明长度先于payload读取被预算拒绝。
func TestReadFrameRejectsOversizeBeforePayloadAllocation(t *testing.T) {
	t.Parallel()
	_, _, registry := testRuntime(t)
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	go func() {
		var prefix [4]byte
		binary.BigEndian.PutUint32(prefix[:], uint32(registry.config.FrameBytes+1))
		_, _ = clientSide.Write(prefix[:])
		_ = clientSide.Close()
	}()
	if _, err := registry.readFrame(context.Background(), serverSide); err != errTCPProtocolViolation {
		t.Fatalf("readFrame error = %v", err)
	}
}

// TestStableCloseReasonClassifiesRateAbuse 验证持续route滥用不会退化为泛化I/O原因。
func TestStableCloseReasonClassifiesRateAbuse(t *testing.T) {
	t.Parallel()
	if reason := stableCloseReason(ErrRateLimited); reason != "rate_limited" {
		t.Fatalf("stableCloseReason(ErrRateLimited)=%q", reason)
	}
}

// TestConnectionPeerCloseCleansEveryIndex 验证peer close会终止I/O owner并释放全部反向索引与remote预算。
func TestConnectionPeerCloseCleansEveryIndex(t *testing.T) {
	t.Parallel()
	_, codec, registry := testRuntime(t)
	serverSide, clientSide := net.Pipe()
	entry := attachTestConnection(registry, codec, serverSide, true)
	dispatcher, err := NewDispatcher(new(dispatcherApplication), new(Handshake), registry, new(testObserver))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.StartConnection(entry, dispatcher); err != nil {
		t.Fatal(err)
	}
	_ = clientSide.Close()
	select {
	case <-entry.done:
	case <-time.After(time.Second):
		t.Fatal("peer close did not stop connection owner")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if len(registry.connections) != 0 || len(registry.bySession) != 0 || len(registry.byPlayer) != 0 || len(registry.byWorld) != 0 || len(registry.byVisit) != 0 || registry.remotes[entry.remoteKey].active != 0 {
		t.Fatalf("peer close leaked registry indexes: connections=%d session=%d player=%d world=%d visit=%d remote=%d", len(registry.connections), len(registry.bySession), len(registry.byPlayer), len(registry.byWorld), len(registry.byVisit), registry.remotes[entry.remoteKey].active)
	}
}

// TestRegistryStopRemovesPendingUnstartedConnection 验证commit与I/O owner启动之间的pending连接也会被同步关闭。
func TestRegistryStopRemovesPendingUnstartedConnection(t *testing.T) {
	t.Parallel()
	_, codec, registry := testRuntime(t)
	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()
	entry := attachTestConnection(registry, codec, serverSide, false)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := registry.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := registry.Stop(ctx); err != nil {
		t.Fatalf("repeated Stop failed: %v", err)
	}
	select {
	case <-entry.done:
	default:
		t.Fatal("unstarted pending connection did not publish completion")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if len(registry.connections) != 0 || registry.remotes[entry.remoteKey].active != 0 {
		t.Fatalf("shutdown leaked pending connection: connections=%d remote=%d", len(registry.connections), registry.remotes[entry.remoteKey].active)
	}
}

// attachTestConnection 建立只含registry/connection生命周期所需字段的受信测试entry。
func attachTestConnection(registry *Registry, codec *Codec, socket net.Conn, active bool) *connection {
	state := ConnectionStatePending
	if active {
		state = ConnectionStateActive
	}
	entry := &connection{
		id: "tcp_00000000000000000000000000000001", remoteKey: "127.0.0.1", sessionID: "ses_cleanup", playerID: "ply_cleanup", worldID: "pworld_cleanup", visitID: "visit_cleanup",
		socket: socket, queue: newSendQueue(8, registry.config.Policy.QueueBytes), codec: codec, observer: new(testObserver), state: state,
		nextServerSequence: 1, nextClientSequence: 1, routeRates: make(map[string]*routeRateState), done: make(chan struct{}),
	}
	registry.mu.Lock()
	registry.connections[entry.id] = entry
	addConnectionIndex(registry.bySession, entry.sessionID, entry.id)
	addConnectionIndex(registry.byPlayer, entry.playerID, entry.id)
	addConnectionIndex(registry.byWorld, entry.worldID, entry.id)
	addConnectionIndex(registry.byVisit, entry.visitID, entry.id)
	registry.remotes[entry.remoteKey] = &remoteState{active: 1, lastSeen: testNow}
	registry.mu.Unlock()
	return entry
}

// readPipeFrame 读取测试net.Pipe上的单个完整frame。
func readPipeFrame(t testing.TB, reader io.Reader) []byte {
	t.Helper()
	var prefix [4]byte
	if _, err := io.ReadFull(reader, prefix[:]); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, binary.BigEndian.Uint32(prefix[:]))
	if _, err := io.ReadFull(reader, payload); err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), payload...)
}
