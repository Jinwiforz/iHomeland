package tcpgameplay

import "sync"

// queuedMessage 携带不可变frame与可选的一次性写出结果。
type queuedMessage struct {
	// encoded 是已经完成route校验的完整length-prefixed frame。
	encoded EncodedMessage
	// completion 仅由writer或release路径写入一次；普通response/push不必分配。
	completion chan<- error
}

// sendQueue 同时限制待发送item与完整encoded frame bytes，所有操作均非阻塞。
type sendQueue struct {
	// mu 线性化入队、取出与关闭。
	mu sync.Mutex
	// items 是固定容量FIFO；不关闭channel以避免与并发sender竞态。
	items chan queuedMessage
	// byteLimit 是所有待发送完整frame的总预算。
	byteLimit int
	// bytes 是当前队列拥有的encoded bytes。
	bytes int
	// closed 阻止关闭后继续入队。
	closed bool
}

// newSendQueue 构造固定双重预算队列。
func newSendQueue(itemLimit int, byteLimit int) *sendQueue {
	return &sendQueue{items: make(chan queuedMessage, itemLimit), byteLimit: byteLimit}
}

// tryPush 复制frame并在任一预算不足时fail closed。
func (queue *sendQueue) tryPush(message EncodedMessage, completion chan<- error) error {
	copyMessage := EncodedMessage{messageID: message.messageID, frame: append([]byte(nil), message.frame...)}
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

// take 在writer取得item后归还队列byte预算。
func (queue *sendQueue) take(message queuedMessage) {
	queue.mu.Lock()
	queue.bytes -= message.encoded.Size()
	if queue.bytes < 0 {
		queue.bytes = 0
	}
	queue.mu.Unlock()
}

// snapshot 返回不包含消息内容的预算快照。
func (queue *sendQueue) snapshot() (int, int) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return len(queue.items), queue.bytes
}

// close 幂等停止新入队。
func (queue *sendQueue) close() {
	queue.mu.Lock()
	queue.closed = true
	queue.mu.Unlock()
}

// release 清除未发送frame引用并通知等待中的调用方。
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
