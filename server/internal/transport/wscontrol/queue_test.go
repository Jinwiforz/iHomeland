package wscontrol

import (
	"errors"
	"testing"
)

// TestSendQueuePreservesOrderAndBudgets 验证FIFO、item/byte边界与取出后预算归还。
func TestSendQueuePreservesOrderAndBudgets(t *testing.T) {
	queue := newSendQueue(2, 5)
	first := EncodedMessage{messageID: 500, bytes: []byte{1, 2}}
	second := EncodedMessage{messageID: 501, bytes: []byte{3, 4, 5}}
	if err := queue.tryPush(first, nil); err != nil {
		t.Fatal(err)
	}
	if err := queue.tryPush(second, nil); err != nil {
		t.Fatal(err)
	}
	if err := queue.tryPush(first, nil); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("expected full queue, got %v", err)
	}
	for _, expected := range []uint32{500, 501} {
		message := <-queue.items
		queue.take(message)
		if message.encoded.messageID != expected {
			t.Fatalf("FIFO order changed: got %d want %d", message.encoded.messageID, expected)
		}
	}
	items, bytes := queue.snapshot()
	if items != 0 || bytes != 0 {
		t.Fatalf("queue budget leaked: items=%d bytes=%d", items, bytes)
	}
}

// TestSendQueueCloseIsIdempotentAndReleasesMemory 验证并发安全关闭与frame引用释放。
func TestSendQueueCloseIsIdempotentAndReleasesMemory(t *testing.T) {
	queue := newSendQueue(2, 8)
	completion := make(chan error, 1)
	if err := queue.tryPush(EncodedMessage{messageID: 500, bytes: []byte{1, 2, 3}}, completion); err != nil {
		t.Fatal(err)
	}
	queue.close()
	queue.close()
	if err := queue.tryPush(EncodedMessage{messageID: 500, bytes: []byte{1}}, nil); !errors.Is(err, ErrQueueClosed) {
		t.Fatalf("closed queue accepted item: %v", err)
	}
	queue.release()
	select {
	case err := <-completion:
		if !errors.Is(err, ErrQueueClosed) {
			t.Fatalf("release reported an incorrect completion result: %v", err)
		}
	default:
		t.Fatal("release did not report the final PUSH as unwritten")
	}
	items, bytes := queue.snapshot()
	if items != 0 || bytes != 0 {
		t.Fatal("release retained queue memory")
	}
}
