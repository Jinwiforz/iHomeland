package testclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	commonv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/common/v1"
	"google.golang.org/protobuf/proto"
)

// pendingResponse 绑定 correlation 的唯一合法 response message ID 和等待者。
type pendingResponse struct {
	// responseMessageID 是公开 registry 中与 request/command 配对的 S2C 编号。
	responseMessageID uint32
	// result 只接收一次 response 或 error。
	result chan realtimeResult
}

// realtimeResult 是 receive pump 向单个等待者交付的封闭结果。
type realtimeResult struct {
	// message 是已通过 wire 校验的 response。
	message DecodedRealtimeMessage
	// err 是 connection 或协议终止原因。
	err error
}

// RealtimePublicError 是 ERROR envelope 中可安全暴露的稳定错误投影。
type RealtimePublicError struct {
	// Code 是 errors registry 登记的全局错误码。
	Code uint32
	// MessageKey 是客户端本地化使用的稳定键。
	MessageKey string
	// Retryable 表示不修改输入、稍后重试是否可能成功。
	Retryable bool
	// RetryAfterMS 是可选最短重试等待，单位为毫秒。
	RetryAfterMS uint32
}

// Error 返回低敏摘要，不包含 correlation、payload、endpoint 或 backend 文本。
func (failure RealtimePublicError) Error() string {
	return fmt.Sprintf("realtime public error code=%d messageKey=%s retryable=%t", failure.Code, failure.MessageKey, failure.Retryable)
}

// TCPClient 是 TLS 1.3 gameplay connection 的单 writer、单 receive pump 消费者。
type TCPClient struct {
	// connection 是完成双 credential preface 的 TLS socket。
	connection net.Conn
	// admission 暂存 JOIN/RECONNECT command 必须再次提交的同一 credential。
	admission *Secret
	// cancel 停止 receive pump 和场景等待。
	cancel context.CancelFunc
	// writerMutex 串行化 sequence 分配与完整 frame 写出。
	writerMutex sync.Mutex
	// nextWriteSequence 是下一个 C2S envelope sequence，从 1 开始。
	nextWriteSequence uint64
	// pendingMutex 保护有限 pending correlation 集合。
	pendingMutex sync.Mutex
	// pending 只包含尚未响应且调用方 deadline 未到的 operation。
	pending map[[16]byte]pendingResponse
	// pushes 传递无需 correlation 的完整服务端 push。
	pushes chan DecodedRealtimeMessage
	// terminal 传递唯一 receive pump 终止结果后关闭。
	terminal chan error
	// terminalMutex 保护 receive pump 保存的唯一终止原因，供 cleanup 判断 remote socket 是否已终止。
	terminalMutex sync.Mutex
	// terminalErr 是已发送到 terminal channel 的同一终止原因。
	terminalErr error
	// closeOnce 串行化显式关闭与资源清除。
	closeOnce sync.Once
	// wait 等待唯一 receive pump 完成。
	wait sync.WaitGroup
	// frameBytes 是公开 config 公布的 envelope 上限，单位为字节。
	frameBytes int
}

// DialTCP 使用 TLS 1.3、IHTP preface、一次性 ticket 与 admission 建立 gameplay connection。
func DialTCP(ctx context.Context, endpoint Endpoint, ticket, admission *Secret, purpose string, tlsConfig *tls.Config, frameBytes int) (*TCPClient, error) {
	if ctx == nil || endpoint.Channel != "TLS_TCP" || endpoint.Host == "" || endpoint.Port == 0 || ticket == nil || admission == nil || tlsConfig == nil || frameBytes < 1024 || frameBytes > maximumRealtimeFrameBytes {
		return nil, errors.New("qualification TLS_TCP dial input is invalid")
	}
	keepAdmission := false
	defer func() {
		if !keepAdmission {
			admission.Clear()
		}
	}()
	ticketValue, err := ticket.Take()
	if err != nil {
		return nil, err
	}
	admissionValue, err := admission.Reveal()
	if err != nil {
		return nil, err
	}
	preface, err := encodeGameplayPreface(ticketValue, admissionValue, purpose)
	if err != nil {
		return nil, err
	}
	dialer := new(net.Dialer)
	raw, err := dialer.DialContext(ctx, "tcp", endpointURL(endpoint))
	if err != nil {
		return nil, fmt.Errorf("dial qualification TLS_TCP: %w", err)
	}
	clonedTLS := tlsConfig.Clone()
	clonedTLS.MinVersion = tls.VersionTLS13
	clonedTLS.MaxVersion = tls.VersionTLS13
	if clonedTLS.ServerName == "" {
		clonedTLS.ServerName = endpoint.Host
	}
	connection := tls.Client(raw, clonedTLS)
	if err := connection.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("handshake qualification TLS_TCP: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetWriteDeadline(deadline)
	} else {
		_ = connection.SetWriteDeadline(time.Now().Add(defaultHTTPTimeout))
	}
	if err := writeAll(connection, preface); err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("write qualification IHTP preface: %w", err)
	}
	_ = connection.SetWriteDeadline(time.Time{})
	if purpose == "OWN_WORLD" {
		admission.Clear()
	} else {
		// JOIN/RECONNECT command 仍需单次取得同一 admission；ownership 转移给成功 client。
		keepAdmission = true
	}
	pumpContext, cancel := context.WithCancel(context.Background())
	client := &TCPClient{
		connection: connection, admission: admission, cancel: cancel, nextWriteSequence: 1,
		pending: make(map[[16]byte]pendingResponse), pushes: make(chan DecodedRealtimeMessage, 32), terminal: make(chan error, 1), frameBytes: frameBytes,
	}
	client.wait.Add(1)
	go client.receivePump(pumpContext)
	return client, nil
}

// TakeAdmission 原子取得 JOIN/RECONNECT command 所需的 preface credential。
func (client *TCPClient) TakeAdmission() (string, error) {
	if client == nil {
		return "", errors.New("qualification TLS_TCP client is nil")
	}
	return client.admission.Take()
}

// Request 发送冻结 REQUEST 并等待精确关联的 response 或公开 error。
func (client *TCPClient) Request(ctx context.Context, messageID uint32, payload proto.Message) (DecodedRealtimeMessage, error) {
	return client.roundTrip(ctx, messageID, messageID+1, payload, commonv1.MessageKind_MESSAGE_KIND_REQUEST)
}

// Command 发送冻结 COMMAND 并等待精确关联的 response 或公开 error。
func (client *TCPClient) Command(ctx context.Context, messageID uint32, payload proto.Message) (DecodedRealtimeMessage, error) {
	return client.roundTrip(ctx, messageID, messageID+1, payload, commonv1.MessageKind_MESSAGE_KIND_COMMAND)
}

// Pushes 返回只读 gameplay push stream；连接终止后 channel 关闭。
func (client *TCPClient) Pushes() <-chan DecodedRealtimeMessage {
	if client == nil {
		return nil
	}
	return client.pushes
}

// Terminal 返回唯一 receive pump 终止结果。
func (client *TCPClient) Terminal() <-chan error {
	if client == nil {
		return nil
	}
	return client.terminal
}

// PendingCount 返回当前等待 response 的有界 operation 数量。
func (client *TCPClient) PendingCount() int {
	if client == nil {
		return 0
	}
	client.pendingMutex.Lock()
	defer client.pendingMutex.Unlock()
	return len(client.pending)
}

// Close 清除 credential、取消 pending、关闭 socket 并等待 receive pump。
func (client *TCPClient) Close() error {
	if client == nil {
		return nil
	}
	var closeErr error
	client.closeOnce.Do(func() {
		client.admission.Clear()
		if client.cancel != nil {
			client.cancel()
		}
		if client.connection != nil {
			closeErr = client.connection.Close()
		}
		client.failPending(errors.New("qualification TLS_TCP client closed"))
	})
	client.wait.Wait()
	client.terminalMutex.Lock()
	terminalErr := client.terminalErr
	client.terminalMutex.Unlock()
	return normalizeTCPCleanupError(closeErr, terminalErr)
}

// normalizeTCPCleanupError 把重复关闭或 receive pump 已确认的 remote 终止视为资源已释放。
func normalizeTCPCleanupError(closeErr, terminalErr error) error {
	if errors.Is(closeErr, net.ErrClosed) || terminalErr != nil {
		return nil
	}
	return closeErr
}

// finishTerminal 原子保存并发布 receive pump 的唯一终止原因。
func (client *TCPClient) finishTerminal(err error) {
	client.terminalMutex.Lock()
	client.terminalErr = err
	client.terminalMutex.Unlock()
	client.terminal <- err
}

// roundTrip 登记 correlation 后通过唯一 writer 发送，并按调用方 deadline 回收 pending。
func (client *TCPClient) roundTrip(ctx context.Context, messageID, responseMessageID uint32, payload proto.Message, expectedKind commonv1.MessageKind) (DecodedRealtimeMessage, error) {
	if client == nil || ctx == nil {
		return DecodedRealtimeMessage{}, errors.New("qualification realtime operation input is invalid")
	}
	profile, exists := clientMessageTypes[messageID]
	if !exists || profile.kind != expectedKind {
		return DecodedRealtimeMessage{}, errWireProtocol
	}
	correlation, err := newCorrelationID()
	if err != nil {
		return DecodedRealtimeMessage{}, err
	}
	var key [16]byte
	copy(key[:], correlation)
	waiter := pendingResponse{responseMessageID: responseMessageID, result: make(chan realtimeResult, 1)}
	client.pendingMutex.Lock()
	if len(client.pending) >= 128 {
		client.pendingMutex.Unlock()
		return DecodedRealtimeMessage{}, errors.New("qualification realtime pending limit reached")
	}
	client.pending[key] = waiter
	client.pendingMutex.Unlock()
	if err := client.writeEnvelope(ctx, messageID, payload, correlation); err != nil {
		client.removePending(key)
		return DecodedRealtimeMessage{}, err
	}
	select {
	case result := <-waiter.result:
		return result.message, result.err
	case <-ctx.Done():
		client.removePending(key)
		return DecodedRealtimeMessage{}, context.Cause(ctx)
	}
}

// writeEnvelope 在单临界区内分配 sequence、编码并完整写出一个 frame。
func (client *TCPClient) writeEnvelope(ctx context.Context, messageID uint32, payload proto.Message, correlation []byte) error {
	client.writerMutex.Lock()
	defer client.writerMutex.Unlock()
	encoded, err := encodeClientEnvelope(messageID, payload, correlation, client.nextWriteSequence)
	if err != nil {
		return err
	}
	frame, err := encodeFrame(encoded, client.frameBytes)
	if err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = client.connection.SetWriteDeadline(deadline)
	} else {
		_ = client.connection.SetWriteDeadline(time.Now().Add(defaultHTTPTimeout))
	}
	if err := writeAll(client.connection, frame); err != nil {
		return fmt.Errorf("write qualification TLS_TCP frame: %w", err)
	}
	_ = client.connection.SetWriteDeadline(time.Time{})
	client.nextWriteSequence++
	return nil
}

// receivePump 是唯一 socket read owner，按 sequence 分流 response/error/push。
func (client *TCPClient) receivePump(ctx context.Context) {
	defer client.wait.Done()
	defer close(client.pushes)
	defer close(client.terminal)
	expectedSequence := uint64(1)
	for {
		encoded, err := readFrame(client.connection, client.frameBytes)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				client.finishTerminal(nil)
			} else {
				client.finishTerminal(fmt.Errorf("qualification TLS_TCP receive failed: %w", err))
			}
			client.failPending(err)
			return
		}
		message, err := decodeServerEnvelope(encoded, expectedSequence, false)
		if err != nil {
			client.finishTerminal(err)
			client.failPending(err)
			_ = client.connection.Close()
			return
		}
		expectedSequence++
		if message.Kind == commonv1.MessageKind_MESSAGE_KIND_PUSH {
			select {
			case client.pushes <- message:
			case <-ctx.Done():
				client.finishTerminal(nil)
				client.failPending(ctx.Err())
				return
			}
			continue
		}
		if !client.completePending(message) {
			client.finishTerminal(errWireProtocol)
			client.failPending(errWireProtocol)
			_ = client.connection.Close()
			return
		}
	}
}

// completePending 精确匹配 correlation 与 response message ID，并删除等待项。
func (client *TCPClient) completePending(message DecodedRealtimeMessage) bool {
	correlation := message.RequestID
	if len(correlation) == 0 {
		correlation = message.CommandID
	}
	if len(correlation) != 16 {
		return false
	}
	var key [16]byte
	copy(key[:], correlation)
	client.pendingMutex.Lock()
	waiter, exists := client.pending[key]
	if exists && waiter.responseMessageID == message.MessageID {
		delete(client.pending, key)
	}
	client.pendingMutex.Unlock()
	if !exists || waiter.responseMessageID != message.MessageID {
		return false
	}
	if message.Kind == commonv1.MessageKind_MESSAGE_KIND_ERROR {
		payload, ok := message.Payload.(*commonv1.ErrorPayload)
		if !ok || payload.GetCode() == 0 || payload.GetMessageKey() == "" || len(payload.GetDetails()) > 16 || payload.GetRetryAfterMs() > 60_000 {
			waiter.result <- realtimeResult{err: errWireProtocol}
			return true
		}
		waiter.result <- realtimeResult{err: RealtimePublicError{
			Code: payload.GetCode(), MessageKey: payload.GetMessageKey(), Retryable: payload.GetRetryable(), RetryAfterMS: payload.GetRetryAfterMs(),
		}}
		return true
	}
	waiter.result <- realtimeResult{message: message}
	return true
}

// removePending 删除 timeout 或 write failure 对应的等待项。
func (client *TCPClient) removePending(key [16]byte) {
	client.pendingMutex.Lock()
	delete(client.pending, key)
	client.pendingMutex.Unlock()
}

// failPending 清空全部等待项并向每个 waiter 发送相同低敏终止原因。
func (client *TCPClient) failPending(cause error) {
	client.pendingMutex.Lock()
	pending := client.pending
	client.pending = make(map[[16]byte]pendingResponse)
	client.pendingMutex.Unlock()
	for _, waiter := range pending {
		waiter.result <- realtimeResult{err: cause}
	}
}

// writeAll 处理 TLS/TCP partial write，直到完整 frame 写出或出现错误。
func writeAll(writer net.Conn, value []byte) error {
	for len(value) > 0 {
		count, err := writer.Write(value)
		if err != nil {
			return err
		}
		if count <= 0 || count > len(value) {
			return io.ErrUnexpectedEOF
		}
		value = value[count:]
	}
	return nil
}
