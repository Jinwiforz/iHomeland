package wscontrol

import (
	"errors"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jinwiforz/ihomeland/server/internal/contract"
	controlv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/control/v1"
	"github.com/jinwiforz/ihomeland/server/internal/protocol"
)

// TestConnectionSerializesPushes 验证每连接writer按sequence顺序发送binary envelope。
func TestConnectionSerializesPushes(t *testing.T) {
	config := testConfig()
	codec, err := NewCodec(contract.WSSPushCatalog(), config.FrameBytes, testClock{now: time.UnixMilli(10)})
	if err != nil {
		t.Fatal(err)
	}
	socket := newTestSocket()
	socket.writeSignal = make(chan struct{}, 2)
	entry := newConnection("con_test", "ses_test", "ply_test", 1, "127.0.0.1", socket, codec, config, testObserver{})
	if err := entry.enqueue(500, controlv1.MaintenancePush_builder{}.Build(), nil); err != nil {
		t.Fatal(err)
	}
	if err := entry.enqueue(502, controlv1.QueueStatusPush_builder{}.Build(), nil); err != nil {
		t.Fatal(err)
	}
	go entry.run(t.Context())
	for range 2 {
		select {
		case <-socket.writeSignal:
		case <-time.After(time.Second):
			t.Fatal("writer did not drain queue")
		}
	}
	entry.stop(websocket.StatusNormalClosure, "test complete")
	select {
	case <-entry.done:
	case <-time.After(time.Second):
		t.Fatal("connection did not stop")
	}
	socket.mu.Lock()
	writes := append([][]byte(nil), socket.writes...)
	socket.mu.Unlock()
	if len(writes) != 2 {
		t.Fatalf("got %d writes", len(writes))
	}
	for index, encoded := range writes {
		envelope, err := protocol.UnmarshalEnvelope(encoded)
		if err != nil || envelope.GetSequence() != uint64(index+1) {
			t.Fatalf("write %d sequence mismatch: %v", index, err)
		}
	}
}

// TestConnectionRejectsApplicationFrame 验证客户端application data立即结束只出不进连接。
func TestConnectionRejectsApplicationFrame(t *testing.T) {
	config := testConfig()
	codec, err := NewCodec(contract.WSSPushCatalog(), config.FrameBytes, testClock{now: time.UnixMilli(10)})
	if err != nil {
		t.Fatal(err)
	}
	socket := newTestSocket()
	socket.readMessage = true
	entry := newConnection("con_test", "ses_test", "ply_test", 1, "127.0.0.1", socket, codec, config, testObserver{})
	go entry.run(t.Context())
	select {
	case <-entry.done:
	case <-time.After(time.Second):
		t.Fatal("application frame did not close connection")
	}
}

// TestConnectionClosesOnHeartbeatFailure 验证pong deadline失败不会保留连接任务。
func TestConnectionClosesOnHeartbeatFailure(t *testing.T) {
	config := testConfig()
	config.Policy.PingInterval = time.Millisecond
	codec, err := NewCodec(contract.WSSPushCatalog(), config.FrameBytes, testClock{now: time.UnixMilli(10)})
	if err != nil {
		t.Fatal(err)
	}
	socket := newTestSocket()
	socket.pingErr = errors.New("pong timeout")
	entry := newConnection("con_test", "ses_test", "ply_test", 1, "127.0.0.1", socket, codec, config, testObserver{})
	go entry.run(t.Context())
	select {
	case <-entry.done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat failure did not close connection")
	}
}

// TestConnectionIdleDeadlineBoundsBlockedHeartbeat 验证单次ping不能把无成功I/O时间拖过idle上限。
func TestConnectionIdleDeadlineBoundsBlockedHeartbeat(t *testing.T) {
	config := testConfig()
	config.Policy.PingInterval = time.Millisecond
	config.Policy.PongTimeout = time.Second
	config.Policy.IdleTimeout = 20 * time.Millisecond
	codec, err := NewCodec(contract.WSSPushCatalog(), config.FrameBytes, testClock{now: time.UnixMilli(10)})
	if err != nil {
		t.Fatal(err)
	}
	socket := newTestSocket()
	socket.pingBlock = true
	entry := newConnection("con_test", "ses_test", "ply_test", 1, "127.0.0.1", socket, codec, config, testObserver{})
	started := time.Now()
	go entry.run(t.Context())
	select {
	case <-entry.done:
		if elapsed := time.Since(started); elapsed >= 250*time.Millisecond {
			t.Fatalf("idle deadline was not enforced promptly: %s", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked heartbeat bypassed idle deadline")
	}
}

// TestConnectionRecoversWriterPanic 验证单连接adapter panic不会终止进程或泄漏原始文本。
func TestConnectionRecoversWriterPanic(t *testing.T) {
	config := testConfig()
	codec, err := NewCodec(contract.WSSPushCatalog(), config.FrameBytes, testClock{now: time.UnixMilli(10)})
	if err != nil {
		t.Fatal(err)
	}
	socket := newTestSocket()
	socket.panicWrite = true
	entry := newConnection("con_test", "ses_test", "ply_test", 1, "127.0.0.1", socket, codec, config, testObserver{})
	if err := entry.enqueue(500, controlv1.MaintenancePush_builder{}.Build(), nil); err != nil {
		t.Fatal(err)
	}
	go entry.run(t.Context())
	select {
	case <-entry.done:
	case <-time.After(time.Second):
		t.Fatal("writer panic did not close connection")
	}
}
