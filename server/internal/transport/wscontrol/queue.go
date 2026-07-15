package wscontrol

import "sync"

// queuedMessage 在失效通知需要尽力等待写出时携带一次性完成结果。
type queuedMessage struct {
	// encoded 是不可变完整frame。
	encoded EncodedMessage
	// completion 仅由writer或队列释放路径写入一次；普通PUSH不分配该通道。
	completion chan<- error
}

// sendQueue 同时限制待发送item与encoded bytes，且所有操作均为非阻塞。
type sendQueue struct {
	// mu 线性化入队、取出与关闭。
	mu sync.Mutex
	// items 是固定容量FIFO。
	items chan queuedMessage
	// byteLimit 是所有待发送frame的总预算。
	byteLimit int
	// bytes 是当前队列拥有的encoded bytes。
	bytes int
	// closed 阻止关闭后向channel发送。
	closed bool
}

// newSendQueue 构造固定预算队列。
func newSendQueue(itemLimit int, byteLimit int) *sendQueue {
	return &sendQueue{items: make(chan queuedMessage, itemLimit), byteLimit: byteLimit}
}

// tryPush 复制frame并在任一预算不足时fail closed。
func (queue *sendQueue) tryPush(message EncodedMessage, completion chan<- error) error {
	copyMessage := EncodedMessage{messageID: message.messageID, bytes: append([]byte(nil), message.bytes...)}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if queue.closed {
		return ErrQueueClosed
	}
	if len(queue.items) == cap(queue.items) || queue.bytes+copyMessage.Size() > queue.byteLimit {
		return ErrQueueFull
	}
	queue.items <- queuedMessage{encoded: copyMessage, completion: completion}
	queue.bytes += copyMessage.Size()
	return nil
}

// take 在writer收到item后归还队列byte预算。
func (queue *sendQueue) take(message queuedMessage) {
	queue.mu.Lock()
	queue.bytes -= message.encoded.Size()
	if queue.bytes < 0 {
		queue.bytes = 0
	}
	queue.mu.Unlock()
}

// snapshot 返回不含内容的预算快照。
func (queue *sendQueue) snapshot() (int, int) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return len(queue.items), queue.bytes
}

// close 幂等停止新入队；channel不关闭，避免关闭与并发发送竞态。
func (queue *sendQueue) close() {
	queue.mu.Lock()
	queue.closed = true
	queue.mu.Unlock()
}

// release 清除尚未发送的frame引用，并把未写出结果通知给等待中的最终PUSH。
func (queue *sendQueue) release() {
	for {
		select {
		case message := <-queue.items:
			queue.take(message)
			if message.completion != nil {
				message.completion <- ErrQueueClosed
			}
		default:
			return
		}
	}
}
