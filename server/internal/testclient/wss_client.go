package testclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	// wssControlPath 是公开 control WebSocket 的冻结 path。
	wssControlPath = "/v1/control"
	// wssControlSubprotocol 是公开 control WebSocket 的冻结 subprotocol。
	wssControlSubprotocol = "ihomeland.control.v1"
	// realtimeCloseTimeout 是客户端主动关闭 handshake 的最大预算。
	realtimeCloseTimeout = 3 * time.Second
)

// WSSClient 是只有一个 receive pump 的 control push 消费者。
type WSSClient struct {
	// connection 是 coder websocket 拥有的 TLS WebSocket。
	connection *websocket.Conn
	// cancel 停止唯一 receive pump。
	cancel context.CancelFunc
	// messages 传递已完成 envelope/payload 校验的 push。
	messages chan DecodedRealtimeMessage
	// terminal 传递唯一终止结果后关闭。
	terminal chan error
	// terminalMutex 保护 receive pump 保存的唯一终止原因，供 cleanup 判断 peer 是否已关闭。
	terminalMutex sync.Mutex
	// terminalErr 是已发送到 terminal channel 的同一终止原因。
	terminalErr error
	// closeOnce 串行化显式关闭和 receive failure。
	closeOnce sync.Once
	// wait 等待唯一 receive pump 释放连接引用。
	wait sync.WaitGroup
	// frameBytes 是公开 config 公布的完整 envelope 上限，单位为字节。
	frameBytes int
}

// DialWSS 使用 TLS 1.3、冻结 subprotocol 和一次性 ticket 建立 control connection。
func DialWSS(ctx context.Context, endpoint Endpoint, ticket *Secret, tlsConfig *tls.Config, frameBytes int) (*WSSClient, error) {
	if ctx == nil || endpoint.Channel != "WSS" || endpoint.Host == "" || endpoint.Port == 0 || ticket == nil || tlsConfig == nil || frameBytes < 1024 || frameBytes > maximumRealtimeFrameBytes {
		return nil, errors.New("qualification WSS dial input is invalid")
	}
	ticketValue, err := ticket.Take()
	if err != nil {
		return nil, err
	}
	if !validTicketCredential(ticketValue) {
		return nil, errors.New("qualification WSS ticket grammar is invalid")
	}
	clonedTLS := tlsConfig.Clone()
	clonedTLS.MinVersion = tls.VersionTLS13
	clonedTLS.MaxVersion = tls.VersionTLS13
	transport := &http.Transport{TLSClientConfig: clonedTLS, ForceAttemptHTTP2: false}
	httpClient := &http.Client{Transport: transport, Timeout: defaultHTTPTimeout}
	target := url.URL{Scheme: "wss", Host: endpointURL(endpoint), Path: wssControlPath}
	header := make(http.Header)
	header.Set("Authorization", "Ticket "+ticketValue)
	connection, response, err := websocket.Dial(ctx, target.String(), &websocket.DialOptions{
		HTTPClient: httpClient, HTTPHeader: header, Subprotocols: []string{wssControlSubprotocol}, CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, fmt.Errorf("dial qualification WSS: %w", err)
	}
	if connection.Subprotocol() != wssControlSubprotocol {
		_ = connection.CloseNow()
		return nil, errors.New("qualification WSS subprotocol was not selected")
	}
	connection.SetReadLimit(int64(frameBytes))
	pumpContext, cancel := context.WithCancel(context.Background())
	client := &WSSClient{
		connection: connection, cancel: cancel, messages: make(chan DecodedRealtimeMessage, 32), terminal: make(chan error, 1), frameBytes: frameBytes,
	}
	client.wait.Add(1)
	go client.receivePump(pumpContext)
	return client, nil
}

// Messages 返回只读 push stream；连接终止后 channel 关闭。
func (client *WSSClient) Messages() <-chan DecodedRealtimeMessage {
	if client == nil {
		return nil
	}
	return client.messages
}

// Terminal 返回唯一 receive pump 终止结果；正常主动关闭返回 nil。
func (client *WSSClient) Terminal() <-chan error {
	if client == nil {
		return nil
	}
	return client.terminal
}

// Close 在独立预算内发送 normal close，并等待 receive pump 退出。
func (client *WSSClient) Close() error {
	if client == nil {
		return nil
	}
	var closeErr error
	client.closeOnce.Do(func() {
		client.cancel()
		closeDone := make(chan error, 1)
		go func() { closeDone <- client.connection.Close(websocket.StatusNormalClosure, "qualification complete") }()
		select {
		case closeErr = <-closeDone:
		case <-time.After(realtimeCloseTimeout):
			closeErr = client.connection.CloseNow()
		}
	})
	client.wait.Wait()
	client.terminalMutex.Lock()
	terminalErr := client.terminalErr
	client.terminalMutex.Unlock()
	return normalizeWSSCleanupError(closeErr, terminalErr)
}

// normalizeWSSCleanupError 把任一路径已收到的 peer close frame 视为资源已释放；业务场景另行断言状态。
func normalizeWSSCleanupError(closeErr, terminalErr error) error {
	if websocket.CloseStatus(closeErr) != -1 || websocket.CloseStatus(terminalErr) != -1 {
		return nil
	}
	return closeErr
}

// finishTerminal 原子保存并发布 receive pump 的唯一终止原因。
func (client *WSSClient) finishTerminal(err error) {
	client.terminalMutex.Lock()
	client.terminalErr = err
	client.terminalMutex.Unlock()
	client.terminal <- err
}

// receivePump 是唯一 application read owner，按单连接 sequence 解码 binary push。
func (client *WSSClient) receivePump(ctx context.Context) {
	defer client.wait.Done()
	defer close(client.messages)
	defer close(client.terminal)
	expectedSequence := uint64(1)
	for {
		messageType, encoded, err := client.connection.Read(ctx)
		if err != nil {
			if ctx.Err() != nil || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				client.finishTerminal(nil)
			} else {
				client.finishTerminal(fmt.Errorf("qualification WSS receive failed: %w", err))
			}
			return
		}
		if messageType != websocket.MessageBinary || len(encoded) > client.frameBytes {
			client.finishTerminal(errWireProtocol)
			_ = client.connection.CloseNow()
			return
		}
		message, err := decodeServerEnvelope(encoded, expectedSequence, true)
		if err != nil {
			client.finishTerminal(err)
			_ = client.connection.CloseNow()
			return
		}
		expectedSequence++
		select {
		case client.messages <- message:
		case <-ctx.Done():
			client.finishTerminal(nil)
			return
		}
	}
}
