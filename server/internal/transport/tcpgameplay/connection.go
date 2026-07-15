package tcpgameplay

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"time"
)

var (
	// 以下错误只用于把底层失败收敛为稳定关闭类别，不进入响应或结构化日志。
	errTCPPeerClosed        = errors.New("tcp gameplay peer closed")
	errTCPProtocolViolation = errors.New("tcp gameplay protocol violation")
	errTCPIdleTimeout       = errors.New("tcp gameplay idle timeout")
	errTCPReadFailed        = errors.New("tcp gameplay read failed")
	errTCPWriteFailed       = errors.New("tcp gameplay write failed")
	errTCPLoopPanic         = errors.New("tcp gameplay loop panic")
)

// StartConnection 为已注册entry启动恰好一个reader与一个serialized writer owner。
func (registry *Registry) StartConnection(entry *connection, dispatcher *Dispatcher) error {
	if entry == nil || dispatcher == nil || entry.socket == nil {
		return errors.New("tcp gameplay connection dependencies are incomplete")
	}
	registry.mu.Lock()
	if registry.stopped || registry.connections[entry.id] != entry || entry.startRejected || entry.started {
		registry.mu.Unlock()
		return ErrConnectionNotFound
	}
	entry.started = true
	registry.wg.Add(1)
	registry.mu.Unlock()
	go registry.runConnection(entry, dispatcher)
	return nil
}

// runConnection 确保任一I/O退出都关闭socket、释放queue并解除全部索引。
func (registry *Registry) runConnection(entry *connection, dispatcher *Dispatcher) {
	defer registry.wg.Done()
	defer func() {
		registry.Remove(entry.id)
		entry.signalDone()
	}()
	configureTCPKeepAlive(entry.socket, registry.config.Policy.KeepAlive)
	ctx, cancel := context.WithCancelCause(context.Background())
	errorsChannel := make(chan error, 2)
	go func() {
		errorsChannel <- recoverTCPLoop(func() error { return registry.readLoop(ctx, entry, dispatcher) })
	}()
	go func() { errorsChannel <- recoverTCPLoop(func() error { return registry.writeLoop(ctx, entry) }) }()
	first := <-errorsChannel
	cancel(first)
	entry.queue.close()
	entry.closeOnce.Do(func() { _ = entry.socket.Close() })
	<-errorsChannel
	entry.queue.release()
	entry.mu.Lock()
	entry.state = ConnectionStateClosing
	entry.mu.Unlock()
	registry.observer.ObserveTCPClose(stableCloseReason(first))
}

// readLoop 按frame prefix/payload分别设置idle与partial-frame deadline并同步dispatch。
func (registry *Registry) readLoop(ctx context.Context, entry *connection, dispatcher *Dispatcher) error {
	batch := 0
	for {
		payload, err := registry.readFrame(ctx, entry.socket)
		if err != nil {
			return err
		}
		entry.mu.Lock()
		expected := entry.nextClientSequence
		entry.mu.Unlock()
		message, err := registry.codec.Decode(payload, expected)
		if err != nil {
			registry.observer.ObserveTCPFrame("c2s", "rejected", len(payload)+4)
			return errTCPProtocolViolation
		}
		entry.mu.Lock()
		entry.nextClientSequence++
		entry.mu.Unlock()
		registry.observer.ObserveTCPFrame("c2s", "accepted", len(payload)+4)
		if err := dispatcher.Dispatch(ctx, entry, message); err != nil {
			if errors.Is(err, ErrQueueFull) {
				return ErrQueueFull
			}
			return err
		}
		batch++
		if batch >= registry.config.Policy.ReadBatchFrames {
			batch = 0
			runtime.Gosched()
		}
	}
}

// readFrame 在看到声明长度后先检查全局预算，再分配精确payload。
func (registry *Registry) readFrame(ctx context.Context, socket net.Conn) ([]byte, error) {
	if err := socket.SetReadDeadline(time.Now().Add(registry.config.Policy.IdleTimeout)); err != nil {
		return nil, errTCPReadFailed
	}
	var prefix [4]byte
	if _, err := io.ReadFull(socket, prefix[:]); err != nil {
		return nil, classifyReadError(ctx, err)
	}
	length := binary.BigEndian.Uint32(prefix[:])
	if length == 0 || length > uint32(registry.config.FrameBytes) {
		return nil, errTCPProtocolViolation
	}
	if err := socket.SetReadDeadline(time.Now().Add(registry.config.Policy.ReadTimeout)); err != nil {
		return nil, errTCPReadFailed
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(socket, payload); err != nil {
		return nil, classifyReadError(ctx, err)
	}
	return payload, nil
}

// writeLoop 是所有response/error/push唯一的socket writer。
func (registry *Registry) writeLoop(ctx context.Context, entry *connection) error {
	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case message := <-entry.queue.items:
			entry.queue.take(message)
			if ctx.Err() != nil {
				completeWrite(message, context.Cause(ctx))
				return context.Cause(ctx)
			}
			if err := entry.socket.SetWriteDeadline(time.Now().Add(registry.config.Policy.WriteTimeout)); err != nil {
				completeWrite(message, errTCPWriteFailed)
				return errTCPWriteFailed
			}
			_, err := entry.socket.Write(message.encoded.frame)
			completeWrite(message, err)
			if err != nil {
				registry.observer.ObserveTCPFrame("s2c", "write_failed", message.encoded.Size())
				return errTCPWriteFailed
			}
			registry.observer.ObserveTCPFrame("s2c", "sent", message.encoded.Size())
		}
	}
}

// completeWrite 以非阻塞约定报告一次最终写出结果。
func completeWrite(message queuedMessage, err error) {
	if message.completion != nil {
		message.completion <- err
	}
}

// classifyReadError 把EOF、deadline和其他I/O错误映射为固定本地类别。
func classifyReadError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return errTCPPeerClosed
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return errTCPIdleTimeout
	}
	return errTCPReadFailed
}

// stableCloseReason 返回固定低基数关闭类别。
func stableCloseReason(err error) string {
	switch {
	case errors.Is(err, errTCPPeerClosed):
		return "peer_closed"
	case errors.Is(err, errTCPProtocolViolation), errors.Is(err, ErrProtocol):
		return "protocol_violation"
	case errors.Is(err, errTCPIdleTimeout):
		return "idle_timeout"
	case errors.Is(err, ErrQueueFull):
		return "slow_consumer"
	case errors.Is(err, ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, errTCPLoopPanic):
		return "panic"
	case errors.Is(err, context.Canceled):
		return "server_draining"
	default:
		return "io_failed"
	}
}

// configureTCPKeepAlive 对TLS和明文连接的底层TCP socket启用OS探测，不创建application heartbeat。
func configureTCPKeepAlive(connection net.Conn, period time.Duration) {
	for {
		switch typed := connection.(type) {
		case *net.TCPConn:
			_ = typed.SetKeepAlive(true)
			_ = typed.SetKeepAlivePeriod(period)
			return
		case *tls.Conn:
			connection = typed.NetConn()
		default:
			return
		}
	}
}

// recoverTCPLoop 把socket adapter或application panic收敛为当前连接失败。
func recoverTCPLoop(loop func() error) (err error) {
	defer func() {
		if recover() != nil {
			err = errTCPLoopPanic
		}
	}()
	if loop == nil {
		return fmt.Errorf("%w: nil loop", errTCPLoopPanic)
	}
	return loop()
}
