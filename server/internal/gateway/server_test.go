package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"ihomeland/server/internal/protocol"
	pb "ihomeland/server/internal/protocol/pb/realtime/v1"
)

func TestWebSocketConnectionCreatesAndCleansSession(t *testing.T) {
	gatewayServer, testServer := newTestGateway(t, 200*time.Millisecond)
	defer testServer.Close()

	conn := dialGateway(t, testServer)
	waitForSessionCount(t, gatewayServer, 1)

	_ = conn.Close(websocket.StatusNormalClosure, "test done")
	waitForSessionCount(t, gatewayServer, 0)
}

func TestHeartbeatReturnsHeartbeatResponse(t *testing.T) {
	_, testServer := newTestGateway(t, time.Second)
	defer testServer.Close()

	conn := dialGateway(t, testServer)
	defer conn.Close(websocket.StatusNormalClosure, "test done")

	request := buildTestEnvelope(t, protocol.MaxSupportedVersion, protocol.MessageIDHeartbeatRequest, "req-heartbeat", &pb.HeartbeatRequest{ClientTimeMs: 123})
	writeEnvelope(t, conn, request)

	response := readEnvelope(t, conn)
	if got := response.GetMessageId(); got != pb.MessageID_MESSAGE_ID_HEARTBEAT_RESPONSE {
		t.Fatalf("MessageId = %v, want heartbeat response", got)
	}
	if got := response.GetRequestId(); got != "req-heartbeat" {
		t.Fatalf("RequestId = %q, want req-heartbeat", got)
	}

	message, err := protocol.DecodeEnvelope(response, false)
	if err != nil {
		t.Fatalf("DecodeEnvelope(response) error = %v", err)
	}
	heartbeat, ok := message.(*pb.HeartbeatResponse)
	if !ok {
		t.Fatalf("response payload = %T, want *pb.HeartbeatResponse", message)
	}
	if heartbeat.GetClientTimeMs() != 123 {
		t.Fatalf("ClientTimeMs = %d, want 123", heartbeat.GetClientTimeMs())
	}
	if heartbeat.GetServerTimeMs() == 0 {
		t.Fatal("ServerTimeMs = 0, want non-zero")
	}
}

func TestProtocolVersionUnsupportedReturnsSupportedRange(t *testing.T) {
	_, testServer := newTestGateway(t, time.Second)
	defer testServer.Close()

	conn := dialGateway(t, testServer)
	defer conn.Close(websocket.StatusNormalClosure, "test done")

	request := buildTestEnvelope(t, protocol.MaxSupportedVersion+1, protocol.MessageIDHeartbeatRequest, "req-version", &pb.HeartbeatRequest{ClientTimeMs: 123})
	writeEnvelope(t, conn, request)

	response := readEnvelope(t, conn)
	if got := response.GetMessageId(); got != pb.MessageID_MESSAGE_ID_PROTOCOL_VERSION_UNSUPPORTED {
		t.Fatalf("MessageId = %v, want protocol version unsupported", got)
	}

	message, err := protocol.DecodeEnvelope(response, false)
	if err != nil {
		t.Fatalf("DecodeEnvelope(response) error = %v", err)
	}
	version, ok := message.(*pb.ProtocolVersionUnsupported)
	if !ok {
		t.Fatalf("response payload = %T, want *pb.ProtocolVersionUnsupported", message)
	}
	if version.GetClientVersion() != protocol.MaxSupportedVersion+1 {
		t.Fatalf("ClientVersion = %d", version.GetClientVersion())
	}
	if version.GetMinSupportedVersion() != protocol.MinSupportedVersion || version.GetMaxSupportedVersion() != protocol.MaxSupportedVersion {
		t.Fatalf("supported range = %d-%d", version.GetMinSupportedVersion(), version.GetMaxSupportedVersion())
	}
}

func TestInvalidPayloadReturnsErrorResponse(t *testing.T) {
	_, testServer := newTestGateway(t, time.Second)
	defer testServer.Close()

	conn := dialGateway(t, testServer)
	defer conn.Close(websocket.StatusNormalClosure, "test done")

	payload, err := anypb.New(&pb.ErrorResponse{Code: pb.ErrorCode_ERROR_CODE_PAYLOAD_INVALID})
	if err != nil {
		t.Fatalf("anypb.New() error = %v", err)
	}
	request := &pb.Envelope{
		ProtocolVersion: protocol.MaxSupportedVersion,
		MessageId:       pb.MessageID_MESSAGE_ID_HEARTBEAT_REQUEST,
		RequestId:       "req-invalid",
		Sequence:        1,
		TimestampMs:     time.Now().UnixMilli(),
		Payload:         payload,
	}
	writeEnvelope(t, conn, request)

	response := readEnvelope(t, conn)
	assertErrorCode(t, response, pb.ErrorCode_ERROR_CODE_PAYLOAD_INVALID)
}

func TestUnknownMessageIDReturnsErrorResponse(t *testing.T) {
	_, testServer := newTestGateway(t, time.Second)
	defer testServer.Close()

	conn := dialGateway(t, testServer)
	defer conn.Close(websocket.StatusNormalClosure, "test done")

	request := buildTestEnvelope(t, protocol.MaxSupportedVersion, protocol.MessageID(2000), "req-unknown", &pb.HeartbeatRequest{ClientTimeMs: 123})
	writeEnvelope(t, conn, request)

	response := readEnvelope(t, conn)
	assertErrorCode(t, response, pb.ErrorCode_ERROR_CODE_MESSAGE_ID_UNSUPPORTED)
}

func TestMissingRequestIDReturnsErrorResponse(t *testing.T) {
	_, testServer := newTestGateway(t, time.Second)
	defer testServer.Close()

	conn := dialGateway(t, testServer)
	defer conn.Close(websocket.StatusNormalClosure, "test done")

	request := buildTestEnvelope(t, protocol.MaxSupportedVersion, protocol.MessageIDHeartbeatRequest, "", &pb.HeartbeatRequest{ClientTimeMs: 123})
	writeEnvelope(t, conn, request)

	response := readEnvelope(t, conn)
	assertErrorCode(t, response, pb.ErrorCode_ERROR_CODE_REQUEST_ID_REQUIRED)
}

func TestNonBinaryMessageClosesConnection(t *testing.T) {
	_, testServer := newTestGateway(t, time.Second)
	defer testServer.Close()

	conn := dialGateway(t, testServer)
	defer conn.Close(websocket.StatusNormalClosure, "test done")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte("not protobuf")); err != nil {
		t.Fatalf("Write(text) error = %v", err)
	}

	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Fatal("Read() error = nil, want closed connection")
	}
}

func TestIdleTimeoutClosesConnectionAndCleansSession(t *testing.T) {
	gatewayServer, testServer := newTestGateway(t, 20*time.Millisecond)
	defer testServer.Close()

	conn := dialGateway(t, testServer)
	defer conn.Close(websocket.StatusNormalClosure, "test done")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Fatal("Read() error = nil, want idle timeout close")
	}
	waitForSessionCount(t, gatewayServer, 0)
}

func newTestGateway(t *testing.T, idleTimeout time.Duration) (*Server, *httptest.Server) {
	t.Helper()

	gin.SetMode(gin.ReleaseMode)
	gatewayServer, err := NewServer(Config{IdleTimeout: idleTimeout}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	router := gin.New()
	RegisterRoutes(router, gatewayServer)
	return gatewayServer, httptest.NewServer(router)
}

func dialGateway(t *testing.T, testServer *httptest.Server) *websocket.Conn {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	url := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/ws"
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("websocket.Dial() error = %v", err)
	}
	return conn
}

func buildTestEnvelope(t *testing.T, version uint32, messageID protocol.MessageID, requestID string, message proto.Message) *pb.Envelope {
	t.Helper()

	envelope, err := protocol.BuildEnvelope(protocol.BuildOptions{
		ProtocolVersion: version,
		MessageID:       messageID,
		RequestID:       requestID,
		Sequence:        1,
		Timestamp:       time.Now(),
	}, message)
	if err == nil {
		return envelope
	}

	payload, payloadErr := anypb.New(message)
	if payloadErr != nil {
		t.Fatalf("anypb.New() error = %v", payloadErr)
	}
	return &pb.Envelope{
		ProtocolVersion: version,
		MessageId:       pb.MessageID(messageID),
		RequestId:       requestID,
		Sequence:        1,
		TimestampMs:     time.Now().UnixMilli(),
		Payload:         payload,
	}
}

func writeEnvelope(t *testing.T, conn *websocket.Conn, envelope *pb.Envelope) {
	t.Helper()

	data, err := proto.Marshal(envelope)
	if err != nil {
		t.Fatalf("proto.Marshal() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageBinary, data); err != nil {
		t.Fatalf("Write(binary) error = %v", err)
	}
}

func readEnvelope(t *testing.T, conn *websocket.Conn) *pb.Envelope {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	messageType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if messageType != websocket.MessageBinary {
		t.Fatalf("message type = %v, want binary", messageType)
	}

	envelope := &pb.Envelope{}
	if err := proto.Unmarshal(data, envelope); err != nil {
		t.Fatalf("proto.Unmarshal() error = %v", err)
	}
	return envelope
}

func assertErrorCode(t *testing.T, envelope *pb.Envelope, want pb.ErrorCode) {
	t.Helper()

	if got := envelope.GetMessageId(); got != pb.MessageID_MESSAGE_ID_ERROR_RESPONSE {
		t.Fatalf("MessageId = %v, want error response", got)
	}
	message, err := protocol.DecodeEnvelope(envelope, false)
	if err != nil {
		t.Fatalf("DecodeEnvelope(error) error = %v", err)
	}
	response, ok := message.(*pb.ErrorResponse)
	if !ok {
		t.Fatalf("response payload = %T, want *pb.ErrorResponse", message)
	}
	if response.GetCode() != want {
		t.Fatalf("ErrorCode = %v, want %v", response.GetCode(), want)
	}
}

func waitForSessionCount(t *testing.T, server *Server, want int) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := server.SessionCount(); got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("SessionCount() = %d, want %d", server.SessionCount(), want)
}
