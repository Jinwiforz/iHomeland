package app

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	"ihomeland/server/internal/config"
	"ihomeland/server/internal/protocol"
	pb "ihomeland/server/internal/protocol/pb/realtime/v1"
)

func TestRoomLobbyWebSocketFlow(t *testing.T) {
	server, err := NewHTTPServer(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewHTTPServer() error = %v", err)
	}
	testServer := httptest.NewServer(server.Handler)
	defer testServer.Close()

	hostConn := dialAppGateway(t, testServer)
	defer hostConn.Close(websocket.StatusNormalClosure, "test done")
	guestConn := dialAppGateway(t, testServer)
	defer guestConn.Close(websocket.StatusNormalClosure, "test done")

	createResponse := sendRoomEnvelope[*pb.CreateRoomResponse](t, hostConn, protocol.MessageIDCreateRoomRequest, "req-create", &pb.CreateRoomRequest{
		PlayerId: "player-1",
		RoomName: "测试房间",
		Capacity: 2,
	})
	roomID := createResponse.GetRoom().GetRoomId()
	if roomID == "" {
		t.Fatal("room id is empty")
	}
	if createResponse.GetRoom().GetHostPlayerId() != "player-1" {
		t.Fatalf("host = %q, want player-1", createResponse.GetRoom().GetHostPlayerId())
	}

	joinResponse := sendRoomEnvelope[*pb.JoinRoomResponse](t, guestConn, protocol.MessageIDJoinRoomRequest, "req-join", &pb.JoinRoomRequest{
		PlayerId: "player-2",
		RoomId:   roomID,
	})
	if len(joinResponse.GetRoom().GetMembers()) != 2 {
		t.Fatalf("members = %d, want 2", len(joinResponse.GetRoom().GetMembers()))
	}

	readyResponse := sendRoomEnvelope[*pb.SetReadyResponse](t, guestConn, protocol.MessageIDSetReadyRequest, "req-ready", &pb.SetReadyRequest{
		PlayerId: "player-2",
		RoomId:   roomID,
		Ready:    true,
	})
	if !memberReady(readyResponse.GetRoom(), "player-2") {
		t.Fatal("player-2 ready = false, want true")
	}

	transferResponse := sendRoomEnvelope[*pb.TransferHostResponse](t, hostConn, protocol.MessageIDTransferHostRequest, "req-transfer", &pb.TransferHostRequest{
		PlayerId:       "player-1",
		RoomId:         roomID,
		TargetPlayerId: "player-2",
	})
	if transferResponse.GetRoom().GetHostPlayerId() != "player-2" {
		t.Fatalf("host = %q, want player-2", transferResponse.GetRoom().GetHostPlayerId())
	}

	leaveResponse := sendRoomEnvelope[*pb.LeaveRoomResponse](t, hostConn, protocol.MessageIDLeaveRoomRequest, "req-leave", &pb.LeaveRoomRequest{
		PlayerId: "player-1",
		RoomId:   roomID,
	})
	if len(leaveResponse.GetRoom().GetMembers()) != 1 {
		t.Fatalf("members after leave = %d, want 1", len(leaveResponse.GetRoom().GetMembers()))
	}
}

func TestRoomLobbyReconnectWebSocketFlow(t *testing.T) {
	server, err := NewHTTPServer(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewHTTPServer() error = %v", err)
	}
	testServer := httptest.NewServer(server.Handler)
	defer testServer.Close()

	conn := dialAppGateway(t, testServer)
	createResponse := sendRoomEnvelope[*pb.CreateRoomResponse](t, conn, protocol.MessageIDCreateRoomRequest, "req-create", &pb.CreateRoomRequest{
		PlayerId: "player-1",
		RoomName: "测试房间",
		Capacity: 2,
	})
	roomID := createResponse.GetRoom().GetRoomId()
	_ = conn.Close(websocket.StatusNormalClosure, "simulate disconnect")

	reconnectConn := dialAppGateway(t, testServer)
	defer reconnectConn.Close(websocket.StatusNormalClosure, "test done")
	reconnectResponse := sendRoomEnvelope[*pb.ReconnectRoomResponse](t, reconnectConn, protocol.MessageIDReconnectRoomRequest, "req-reconnect", &pb.ReconnectRoomRequest{
		PlayerId: "player-1",
		RoomId:   roomID,
	})
	if memberConnectionState(reconnectResponse.GetRoom(), "player-1") != pb.RoomMemberConnectionState_ROOM_MEMBER_CONNECTION_STATE_ONLINE {
		t.Fatal("player-1 connection state is not online after reconnect")
	}
}

func dialAppGateway(t *testing.T, testServer *httptest.Server) *websocket.Conn {
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

func sendRoomEnvelope[T proto.Message](t *testing.T, conn *websocket.Conn, messageID protocol.MessageID, requestID string, message proto.Message) T {
	t.Helper()
	envelope, err := protocol.BuildEnvelope(protocol.BuildOptions{
		ProtocolVersion: protocol.MaxSupportedVersion,
		MessageID:       messageID,
		RequestID:       requestID,
		Sequence:        1,
	}, message)
	if err != nil {
		t.Fatalf("BuildEnvelope() error = %v", err)
	}
	data, err := proto.Marshal(envelope)
	if err != nil {
		t.Fatalf("proto.Marshal() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageBinary, data); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	messageType, responseData, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if messageType != websocket.MessageBinary {
		t.Fatalf("message type = %v, want binary", messageType)
	}
	responseEnvelope := &pb.Envelope{}
	if err := proto.Unmarshal(responseData, responseEnvelope); err != nil {
		t.Fatalf("proto.Unmarshal() error = %v", err)
	}
	decoded, err := protocol.DecodeEnvelope(responseEnvelope, true)
	if err != nil {
		t.Fatalf("DecodeEnvelope() error = %v", err)
	}
	typed, ok := decoded.(T)
	if !ok {
		t.Fatalf("decoded response = %T", decoded)
	}
	return typed
}

func memberReady(snapshot *pb.RoomSnapshot, playerID string) bool {
	for _, member := range snapshot.GetMembers() {
		if member.GetPlayerId() == playerID {
			return member.GetReady()
		}
	}
	return false
}

func memberConnectionState(snapshot *pb.RoomSnapshot, playerID string) pb.RoomMemberConnectionState {
	for _, member := range snapshot.GetMembers() {
		if member.GetPlayerId() == playerID {
			return member.GetConnectionState()
		}
	}
	return pb.RoomMemberConnectionState_ROOM_MEMBER_CONNECTION_STATE_UNSPECIFIED
}
