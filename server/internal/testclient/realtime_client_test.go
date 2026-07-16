package testclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	commonv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/common/v1"
	controlv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/control/v1"
	worldv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/world/v1"
	"google.golang.org/protobuf/proto"
)

// TestWSSClientTLSSubprotocolTicketAndReceivePump 验证真实 TLS WebSocket 握手与单 pump push 解码。
func TestWSSClientTLSSubprotocolTicketAndReceivePump(t *testing.T) {
	serverErrors := make(chan error, 1)
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Ticket "+strings.Repeat("a", 32) {
			serverErrors <- errors.New("WSS ticket header mismatch")
			return
		}
		connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{Subprotocols: []string{wssControlSubprotocol}, CompressionMode: websocket.CompressionDisabled})
		if err != nil {
			serverErrors <- err
			return
		}
		defer connection.CloseNow()
		payload, _ := proto.Marshal(new(controlv1.MaintenancePush))
		kind := commonv1.MessageKind_MESSAGE_KIND_PUSH
		envelope := commonv1.ReliableEnvelope_builder{
			ProtocolVersion: proto.Uint32(1), MessageId: proto.Uint32(500), Kind: &kind,
			Sequence: proto.Uint64(1), TimestampMs: proto.Int64(1), Payload: payload,
		}.Build()
		encoded, _ := proto.Marshal(envelope)
		if err := connection.Write(request.Context(), websocket.MessageBinary, encoded); err != nil {
			serverErrors <- err
			return
		}
		serverErrors <- nil
		<-request.Context().Done()
	})
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	host, port := splitTestAddress(t, strings.TrimPrefix(server.URL, "https://"))
	ticket, _ := NewSecret(strings.Repeat("a", 32))
	transport := server.Client().Transport.(*http.Transport)
	client, err := DialWSS(t.Context(), Endpoint{Channel: "WSS", Host: host, Port: port}, ticket, transport.TLSClientConfig, 65536)
	if err != nil {
		t.Fatalf("dial WSS: %v", err)
	}
	select {
	case message := <-client.Messages():
		if message.MessageID != 500 || message.Sequence != 1 {
			t.Fatalf("WSS message=%+v", message)
		}
		if _, ok := message.Payload.(*controlv1.MaintenancePush); !ok {
			t.Fatalf("WSS payload type=%T", message.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WSS push timed out")
	}
	if err := client.Close(); err != nil && websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("close WSS: %v", err)
	}
	if err := <-serverErrors; err != nil {
		t.Fatalf("WSS fixture: %v", err)
	}
}

// TestWSSCleanupAcceptsPeerClose 验证服务端先关闭连接时 cleanup 不把已释放资源误报为关闭失败。
func TestWSSCleanupAcceptsPeerClose(t *testing.T) {
	peerClose := websocket.CloseError{Code: websocket.StatusPolicyViolation, Reason: "session invalidated"}
	if err := normalizeWSSCleanupError(errors.New("close handshake already completed"), peerClose); err != nil {
		t.Fatalf("normalize peer close: %v", err)
	}
	sentinel := errors.New("local close failed")
	if err := normalizeWSSCleanupError(sentinel, nil); !errors.Is(err, sentinel) {
		t.Fatalf("local close failure was hidden: %v", err)
	}
}

// TestTCPCleanupAcceptsClosedNetwork 验证重复关闭不误报，同时保留其他 socket cleanup 错误。
func TestTCPCleanupAcceptsClosedNetwork(t *testing.T) {
	if err := normalizeTCPCleanupError(net.ErrClosed, nil); err != nil {
		t.Fatalf("normalize closed network: %v", err)
	}
	if err := normalizeTCPCleanupError(errors.New("TLS close notify failed"), io.EOF); err != nil {
		t.Fatalf("normalize terminated peer: %v", err)
	}
	sentinel := errors.New("socket cleanup failed")
	if err := normalizeTCPCleanupError(sentinel, nil); !errors.Is(err, sentinel) {
		t.Fatalf("socket cleanup failure was hidden: %v", err)
	}
}

// TestTCPClientTLS13PrefaceRoundTrip 验证真实 TLS socket、IHTP preface、framing、correlation 与 response pump。
func TestTCPClientTLS13PrefaceRoundTrip(t *testing.T) {
	listener, clientTLS := newTLSListener(t)
	serverErrors := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			serverErrors <- err
			return
		}
		defer connection.Close()
		preface, err := readFrame(connection, 1024)
		if err != nil || len(preface) != 91 || string(preface[:4]) != "IHTP" {
			serverErrors <- fmt.Errorf("preface length=%d err=%v", len(preface), err)
			return
		}
		requestBytes, err := readFrame(connection, 65536)
		if err != nil {
			serverErrors <- err
			return
		}
		request := new(commonv1.ReliableEnvelope)
		if err := proto.Unmarshal(requestBytes, request); err != nil || request.GetMessageId() != 2000 || len(request.GetRequestId()) != 16 {
			serverErrors <- fmt.Errorf("request envelope is invalid: %v", err)
			return
		}
		payload, _ := proto.Marshal(new(worldv1.WorldSnapshotResponse))
		kind := commonv1.MessageKind_MESSAGE_KIND_RESPONSE
		response := commonv1.ReliableEnvelope_builder{
			ProtocolVersion: proto.Uint32(1), MessageId: proto.Uint32(2001), Kind: &kind,
			RequestId: request.GetRequestId(), Sequence: proto.Uint64(1), TimestampMs: proto.Int64(1), Payload: payload,
		}.Build()
		encoded, _ := proto.Marshal(response)
		frame, _ := encodeFrame(encoded, 65536)
		serverErrors <- writeAll(connection, frame)
	}()
	ticket, _ := NewSecret(strings.Repeat("a", 32))
	admission, _ := NewSecret("wad1_" + strings.Repeat("A", 43))
	host, port := splitTestAddress(t, listener.Addr().String())
	client, err := DialTCP(t.Context(), Endpoint{Channel: "TLS_TCP", Host: host, Port: port}, ticket, admission, "OWN_WORLD", clientTLS, 65536)
	if err != nil {
		t.Fatalf("dial TCP: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	message, err := client.Request(ctx, 2000, new(worldv1.WorldSnapshotRequest))
	if err != nil {
		t.Fatalf("TCP request: %v", err)
	}
	if message.MessageID != 2001 || message.Kind != commonv1.MessageKind_MESSAGE_KIND_RESPONSE || message.Sequence != 1 {
		t.Fatalf("TCP response=%+v", message)
	}
	if client.PendingCount() != 0 {
		t.Fatalf("pending=%d after response", client.PendingCount())
	}
	_ = client.Close()
	if err := <-serverErrors; err != nil {
		t.Fatalf("TCP fixture: %v", err)
	}
}

// TestTCPClientTimeoutReclaimsPending 验证无响应 operation 到期后立即释放 correlation waiter。
func TestTCPClientTimeoutReclaimsPending(t *testing.T) {
	listener, clientTLS := newTLSListener(t)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		_, _ = readFrame(connection, 1024)
		_, _ = readFrame(connection, 65536)
		_, _ = io.Copy(io.Discard, connection)
	}()
	host, port := splitTestAddress(t, listener.Addr().String())
	ticket, _ := NewSecret(strings.Repeat("b", 32))
	admission, _ := NewSecret("wad1_" + strings.Repeat("B", 43))
	client, err := DialTCP(t.Context(), Endpoint{Channel: "TLS_TCP", Host: host, Port: port}, ticket, admission, "OWN_WORLD", clientTLS, 65536)
	if err != nil {
		t.Fatalf("dial TCP: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := client.Request(ctx, 2000, new(worldv1.WorldSnapshotRequest)); err == nil {
		t.Fatal("missing response did not time out")
	}
	if client.PendingCount() != 0 {
		t.Fatalf("pending=%d after timeout", client.PendingCount())
	}
	_ = client.Close()
	<-serverDone
}

// newTLSListener 创建只接受 TLS 1.3 的本地 raw TCP fixture 和可信 client 配置。
func newTLSListener(t *testing.T) (net.Listener, *tls.Config) {
	t.Helper()
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	serverTLS := fixture.TLS.Clone()
	clientTLS := fixture.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	fixture.Close()
	serverTLS.MinVersion = tls.VersionTLS13
	serverTLS.MaxVersion = tls.VersionTLS13
	listener, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("listen TLS: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener, clientTLS
}

// splitTestAddress 解析本地 fixture address 为公开 endpoint 字段。
func splitTestAddress(t *testing.T, address string) (string, uint16) {
	t.Helper()
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("split test address: %v", err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		t.Fatalf("parse test port: %v", err)
	}
	return host, uint16(port)
}
