package wscontrol

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"
)

var (
	// 以下错误只用于把任意底层文本收敛为稳定关闭类别，不会写入日志、metrics或close reason。
	errClientApplicationFrame = errors.New("websocket control client application frame")
	errHeartbeatTimeout       = errors.New("websocket control heartbeat timeout")
	errIdleTimeout            = errors.New("websocket control idle timeout")
	errWriteFailed            = errors.New("websocket control write failed")
	errConnectionLoopPanic    = errors.New("websocket control connection loop panic")
)

// webSocket 是coder/websocket连接在control owner内使用的最窄端口。
type webSocket interface {
	// Read 读取下一条application message，同时由库处理control frame。
	Read(ctx context.Context) (websocket.MessageType, []byte, error)
	// Write 写出一条完整binary message。
	Write(ctx context.Context, messageType websocket.MessageType, payload []byte) error
	// Ping 发送ping并等待reader观察到匹配pong。
	Ping(ctx context.Context) error
	// Close 执行有界graceful close handshake。
	Close(code websocket.StatusCode, reason string) error
	// CloseNow 立即释放底层连接。
	CloseNow() error
	// SetReadLimit 限制意外客户端application frame的分配。
	SetReadLimit(value int64)
}

// connection 持有一条已认证连接的只读身份摘要和独占I/O生命周期。
type connection struct {
	// id 是CSPRNG连接身份，不来自客户端。
	id string
	// sessionID、playerID和epoch只用于反向索引与失效屏障。
	sessionID string
	playerID  string
	epoch     uint64
	// remoteKey 只用于进程内预算回收，绝不进入日志或metrics。
	remoteKey string
	// socket 是唯一网络句柄。
	socket webSocket
	// codec 在sequence临界区内构造不可变frame。
	codec *Codec
	// queue 隔离publisher与socket writer。
	queue *sendQueue
	// observer 只接收稳定低基数结果。
	observer Observer
	// policy 保存连接deadline。
	policy connectionPolicy

	// sequenceMu 线性化sequence分配与FIFO入队。
	sequenceMu sync.Mutex
	// sequence 是下一条成功入队PUSH使用的序号。
	sequence uint64
	// cancel 终止reader和writer。
	cancelMu sync.Mutex
	cancel   context.CancelCauseFunc
	// stopCause 使run尚未安装cancel时发起的关闭不会丢失取消原因。
	stopCause error
	// done 在两条I/O loop退出且socket完成有界关闭或已触发强制释放后关闭。
	done chan struct{}
	// stopDone 在graceful close完成或达到强制释放边界后关闭。
	stopDone chan struct{}
	// stopOnce 保证并发失效、peer close与shutdown只关闭一次。
	stopOnce sync.Once
}

// connectionPolicy 保存单连接I/O预算。
type connectionPolicy struct {
	// writeTimeout 是每次binary write的独立deadline。
	writeTimeout time.Duration
	// pingInterval 是主动存活探测周期。
	pingInterval time.Duration
	// pongTimeout 限制ping响应等待。
	pongTimeout time.Duration
	// idleTimeout 限制没有任何成功write或ping的最长连续时间。
	idleTimeout time.Duration
	// closeTimeout 限制graceful close。
	closeTimeout time.Duration
}

// newConnection 构造尚未启动goroutine的连接owner。
func newConnection(id string, sessionID string, playerID string, epoch uint64, remoteKey string, socket webSocket, codec *Codec, config Config, observer Observer) *connection {
	return &connection{
		id: id, sessionID: sessionID, playerID: playerID, epoch: epoch, remoteKey: remoteKey,
		socket: socket, codec: codec, queue: newSendQueue(config.Policy.QueueItems, config.Policy.QueueBytes), observer: observer,
		policy: connectionPolicy{
			writeTimeout: config.Policy.WriteTimeout, pingInterval: config.Policy.PingInterval,
			pongTimeout: config.Policy.PongTimeout, idleTimeout: config.Policy.IdleTimeout,
			closeTimeout: config.Policy.CloseTimeout,
		},
		sequence: 1, done: make(chan struct{}), stopDone: make(chan struct{}),
	}
}

// enqueue 编码并原子分配sequence；失败不会留下可继续使用的sequence缺口。
// 非nil completion必须具有一个元素的缓冲，确保调用方deadline结束后writer仍可有界报告结果。
func (connection *connection) enqueue(messageID uint32, payload proto.Message, completion chan<- error) error {
	connection.sequenceMu.Lock()
	defer connection.sequenceMu.Unlock()
	encoded, err := connection.codec.Encode(messageID, payload, connection.sequence)
	if err != nil {
		return err
	}
	if err := connection.queue.tryPush(encoded, completion); err != nil {
		items, bytes := connection.queue.snapshot()
		connection.observer.ObserveWSSQueue("rejected", items, bytes)
		return err
	}
	connection.sequence++
	items, bytes := connection.queue.snapshot()
	connection.observer.ObserveWSSQueue("accepted", items, bytes)
	return nil
}

// run 启动恰好一个reader和一个serialized writer，并在任一退出后回收socket。
func (connection *connection) run(parent context.Context) {
	ctx, cancel := context.WithCancelCause(parent)
	connection.cancelMu.Lock()
	connection.cancel = cancel
	stopCause := connection.stopCause
	connection.cancelMu.Unlock()
	if stopCause != nil {
		cancel(stopCause)
	}
	connection.socket.SetReadLimit(1)
	errorsChannel := make(chan error, 2)
	go func() { errorsChannel <- recoverConnectionLoop(func() error { return connection.readLoop(ctx) }) }()
	go func() { errorsChannel <- recoverConnectionLoop(func() error { return connection.writeLoop(ctx) }) }()
	first := <-errorsChannel
	cancel(first)
	code, reason, outcome := connection.closeMapping(first)
	connection.stop(code, reason)
	connection.observer.ObserveWSSClose(outcome)
	<-errorsChannel
	<-connection.stopDone
	connection.queue.close()
	connection.queue.release()
	close(connection.done)
}

// readLoop 只维持control frame；任何application frame都违反当前只出不进契约。
func (connection *connection) readLoop(ctx context.Context) error {
	for {
		_, _, err := connection.socket.Read(ctx)
		if err != nil {
			return err
		}
		return errClientApplicationFrame
	}
}

// writeLoop 串行发送PUSH和ping，避免sequence、binary write与close所有权分散。
func (connection *connection) writeLoop(ctx context.Context) error {
	ticker := time.NewTicker(connection.policy.pingInterval)
	defer ticker.Stop()
	idleTimer := time.NewTimer(connection.policy.idleTimeout)
	defer idleTimer.Stop()
	lastActivity := time.Now()
	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-idleTimer.C:
			connection.observer.ObserveWSSHeartbeat("idle_timeout")
			return errIdleTimeout
		case message := <-connection.queue.items:
			connection.queue.take(message)
			if ctx.Err() != nil {
				if message.completion != nil {
					message.completion <- context.Cause(ctx)
				}
				return context.Cause(ctx)
			}
			writeContext, cancel, idleExpired := connection.ioContext(ctx, lastActivity, connection.policy.writeTimeout)
			if idleExpired {
				if message.completion != nil {
					message.completion <- context.DeadlineExceeded
				}
				connection.observer.ObserveWSSHeartbeat("idle_timeout")
				return errIdleTimeout
			}
			err := connection.socket.Write(writeContext, websocket.MessageBinary, message.encoded.bytes)
			cancel()
			if message.completion != nil {
				message.completion <- err
			}
			if err != nil {
				connection.observer.ObserveWSSPush(message.encoded.messageID, "write_failed", message.encoded.Size())
				return errors.Join(errWriteFailed, err)
			}
			lastActivity = time.Now()
			idleTimer.Reset(connection.policy.idleTimeout)
			connection.observer.ObserveWSSPush(message.encoded.messageID, "sent", message.encoded.Size())
		case <-ticker.C:
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			pingContext, cancel, idleExpired := connection.ioContext(ctx, lastActivity, connection.policy.pongTimeout)
			if idleExpired {
				connection.observer.ObserveWSSHeartbeat("idle_timeout")
				return errIdleTimeout
			}
			err := connection.socket.Ping(pingContext)
			cancel()
			if err != nil {
				if !time.Now().Before(lastActivity.Add(connection.policy.idleTimeout)) {
					connection.observer.ObserveWSSHeartbeat("idle_timeout")
				} else {
					connection.observer.ObserveWSSHeartbeat("timeout")
				}
				return errors.Join(errHeartbeatTimeout, err)
			}
			lastActivity = time.Now()
			idleTimer.Reset(connection.policy.idleTimeout)
			connection.observer.ObserveWSSHeartbeat("ok")
		}
	}
}

// closeMapping 把请求关闭、peer状态和I/O失败映射为不含底层文本的固定协议结果。
func (connection *connection) closeMapping(loopErr error) (websocket.StatusCode, string, string) {
	connection.cancelMu.Lock()
	requested := connection.stopCause
	connection.cancelMu.Unlock()
	if requested != nil {
		switch requested.Error() {
		case "server draining":
			return websocket.StatusGoingAway, "server draining", "server_draining"
		case "slow consumer":
			return websocket.StatusPolicyViolation, "slow consumer", "slow_consumer"
		case "forced logout", "session invalidated":
			return websocket.StatusPolicyViolation, requested.Error(), "session_invalidated"
		default:
			return websocket.StatusNormalClosure, "connection closed", "local_close"
		}
	}
	peerStatus := websocket.CloseStatus(loopErr)
	switch {
	case errors.Is(loopErr, errClientApplicationFrame):
		return websocket.StatusPolicyViolation, "application frame forbidden", "protocol_violation"
	case errors.Is(loopErr, errHeartbeatTimeout):
		return websocket.StatusPolicyViolation, "heartbeat timeout", "heartbeat_timeout"
	case errors.Is(loopErr, errIdleTimeout):
		return websocket.StatusPolicyViolation, "idle timeout", "idle_timeout"
	case errors.Is(loopErr, errConnectionLoopPanic):
		return websocket.StatusInternalError, "connection failed", "panic"
	case errors.Is(loopErr, errWriteFailed):
		return websocket.StatusInternalError, "write failed", "io_failed"
	case peerStatus == websocket.StatusProtocolError || peerStatus == websocket.StatusUnsupportedData || peerStatus == websocket.StatusMessageTooBig:
		return websocket.StatusPolicyViolation, "application frame forbidden", "protocol_violation"
	case peerStatus != -1:
		return websocket.StatusNormalClosure, "peer closed", "peer_closed"
	default:
		return websocket.StatusInternalError, "connection failed", "io_failed"
	}
}

// ioContext 把单次I/O deadline收紧到剩余idle预算，避免阻塞操作绕过连接级上限。
func (connection *connection) ioContext(ctx context.Context, lastActivity time.Time, operationTimeout time.Duration) (context.Context, context.CancelFunc, bool) {
	remaining := time.Until(lastActivity.Add(connection.policy.idleTimeout))
	if remaining <= 0 {
		return nil, func() {}, true
	}
	if remaining < operationTimeout {
		operationTimeout = remaining
	}
	operationContext, cancel := context.WithTimeout(ctx, operationTimeout)
	return operationContext, cancel, false
}

// stop 幂等发起稳定close；每连接独立等待，调用方可在一个总deadline内并行等待全部done。
func (connection *connection) stop(code websocket.StatusCode, reason string) {
	connection.stopOnce.Do(func() {
		connection.queue.close()
		cause := errors.New(reason)
		connection.cancelMu.Lock()
		connection.stopCause = cause
		cancel := connection.cancel
		connection.cancelMu.Unlock()
		if cancel != nil {
			cancel(cause)
		}
		go func() {
			defer close(connection.stopDone)
			closeDone := make(chan struct{})
			go func() {
				_ = connection.socket.Close(code, reason)
				close(closeDone)
			}()
			timer := time.NewTimer(connection.policy.closeTimeout)
			defer timer.Stop()
			select {
			case <-closeDone:
			case <-timer.C:
				// coder/websocket的并发CloseNow可能等待同一个内部owner，因此不能同步阻塞关闭协调者。
				go func() { _ = connection.socket.CloseNow() }()
			}
		}()
	})
}

// recoverConnectionLoop 把第三方socket或测试adapter panic收敛为当前连接失败。
func recoverConnectionLoop(loop func() error) (err error) {
	defer func() {
		if recover() != nil {
			err = errConnectionLoopPanic
		}
	}()
	return loop()
}
