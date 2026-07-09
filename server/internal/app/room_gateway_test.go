package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	"ihomeland/server/internal/config"
	"ihomeland/server/internal/protocol"
	pb "ihomeland/server/internal/protocol/pb/realtime/v1"
	"ihomeland/server/internal/storage"
)

func TestRoomLobbyWebSocketFlow(t *testing.T) {
	server, err := newTestHTTPServer(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewHTTPServer() error = %v", err)
	}
	testServer := httptest.NewServer(server.Handler)
	defer testServer.Close()

	hostConn := dialAppGateway(t, testServer)
	defer hostConn.Close(websocket.StatusNormalClosure, "test done")
	guestConn := dialAppGateway(t, testServer)
	defer guestConn.Close(websocket.StatusNormalClosure, "test done")
	hostSession := registerTestPlayer(t, hostConn, "host")
	guestSession := registerTestPlayer(t, guestConn, "guest")
	hostPlayerID := hostSession.GetPlayer().GetPlayerId()
	guestPlayerID := guestSession.GetPlayer().GetPlayerId()

	createResponse := sendRoomEnvelope[*pb.CreateRoomResponse](t, hostConn, protocol.MessageIDCreateRoomRequest, "req-create", &pb.CreateRoomRequest{
		PlayerId: hostPlayerID,
		RoomName: "测试房间",
		Capacity: 2,
	})
	roomID := createResponse.GetRoom().GetRoomId()
	if roomID == "" {
		t.Fatal("room id is empty")
	}
	if createResponse.GetRoom().GetHostPlayerId() != hostPlayerID {
		t.Fatalf("host = %q, want %q", createResponse.GetRoom().GetHostPlayerId(), hostPlayerID)
	}

	joinResponse := sendRoomEnvelope[*pb.JoinRoomResponse](t, guestConn, protocol.MessageIDJoinRoomRequest, "req-join", &pb.JoinRoomRequest{
		PlayerId: guestPlayerID,
		RoomId:   roomID,
	})
	if len(joinResponse.GetRoom().GetMembers()) != 2 {
		t.Fatalf("members = %d, want 2", len(joinResponse.GetRoom().GetMembers()))
	}

	readyResponse := sendRoomEnvelope[*pb.SetReadyResponse](t, guestConn, protocol.MessageIDSetReadyRequest, "req-ready", &pb.SetReadyRequest{
		PlayerId: guestPlayerID,
		RoomId:   roomID,
		Ready:    true,
	})
	if !memberReady(readyResponse.GetRoom(), guestPlayerID) {
		t.Fatalf("%s ready = false, want true", guestPlayerID)
	}

	transferResponse := sendRoomEnvelope[*pb.TransferHostResponse](t, hostConn, protocol.MessageIDTransferHostRequest, "req-transfer", &pb.TransferHostRequest{
		PlayerId:       hostPlayerID,
		RoomId:         roomID,
		TargetPlayerId: guestPlayerID,
	})
	if transferResponse.GetRoom().GetHostPlayerId() != guestPlayerID {
		t.Fatalf("host = %q, want %q", transferResponse.GetRoom().GetHostPlayerId(), guestPlayerID)
	}

	leaveResponse := sendRoomEnvelope[*pb.LeaveRoomResponse](t, hostConn, protocol.MessageIDLeaveRoomRequest, "req-leave", &pb.LeaveRoomRequest{
		PlayerId: hostPlayerID,
		RoomId:   roomID,
	})
	if len(leaveResponse.GetRoom().GetMembers()) != 1 {
		t.Fatalf("members after leave = %d, want 1", len(leaveResponse.GetRoom().GetMembers()))
	}
}

func TestRoomStartWebSocketFlow(t *testing.T) {
	server, err := newTestHTTPServer(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewHTTPServer() error = %v", err)
	}
	testServer := httptest.NewServer(server.Handler)
	defer testServer.Close()

	hostConn := dialAppGateway(t, testServer)
	defer hostConn.Close(websocket.StatusNormalClosure, "test done")
	guestConn := dialAppGateway(t, testServer)
	defer guestConn.Close(websocket.StatusNormalClosure, "test done")
	hostSession := registerTestPlayer(t, hostConn, "start_host")
	guestSession := registerTestPlayer(t, guestConn, "start_guest")
	hostPlayerID := hostSession.GetPlayer().GetPlayerId()
	guestPlayerID := guestSession.GetPlayer().GetPlayerId()

	createResponse := sendRoomEnvelope[*pb.CreateRoomResponse](t, hostConn, protocol.MessageIDCreateRoomRequest, "req-start-create", &pb.CreateRoomRequest{
		PlayerId: hostPlayerID,
		RoomName: "start room",
		Capacity: 2,
	})
	roomID := createResponse.GetRoom().GetRoomId()
	_ = sendRoomEnvelope[*pb.JoinRoomResponse](t, guestConn, protocol.MessageIDJoinRoomRequest, "req-start-join", &pb.JoinRoomRequest{
		PlayerId: guestPlayerID,
		RoomId:   roomID,
	})
	_ = sendRoomEnvelope[*pb.SetReadyResponse](t, guestConn, protocol.MessageIDSetReadyRequest, "req-start-ready", &pb.SetReadyRequest{
		PlayerId: guestPlayerID,
		RoomId:   roomID,
		Ready:    true,
	})

	startResponse := sendRoomEnvelope[*pb.StartRoomResponse](t, hostConn, protocol.MessageIDStartRoomRequest, "req-start", &pb.StartRoomRequest{
		PlayerId: hostPlayerID,
		RoomId:   roomID,
	})
	if startResponse.GetRoom().GetState() != pb.RoomState_ROOM_STATE_STARTED {
		t.Fatalf("room state = %v, want started", startResponse.GetRoom().GetState())
	}
}

func TestRoomStartWebSocketRejectsUnreadyMember(t *testing.T) {
	server, err := newTestHTTPServer(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewHTTPServer() error = %v", err)
	}
	testServer := httptest.NewServer(server.Handler)
	defer testServer.Close()

	hostConn := dialAppGateway(t, testServer)
	defer hostConn.Close(websocket.StatusNormalClosure, "test done")
	guestConn := dialAppGateway(t, testServer)
	defer guestConn.Close(websocket.StatusNormalClosure, "test done")
	hostSession := registerTestPlayer(t, hostConn, "unready_host")
	guestSession := registerTestPlayer(t, guestConn, "unready_guest")
	hostPlayerID := hostSession.GetPlayer().GetPlayerId()
	guestPlayerID := guestSession.GetPlayer().GetPlayerId()

	createResponse := sendRoomEnvelope[*pb.CreateRoomResponse](t, hostConn, protocol.MessageIDCreateRoomRequest, "req-unready-create", &pb.CreateRoomRequest{
		PlayerId: hostPlayerID,
		RoomName: "unready room",
		Capacity: 2,
	})
	roomID := createResponse.GetRoom().GetRoomId()
	_ = sendRoomEnvelope[*pb.JoinRoomResponse](t, guestConn, protocol.MessageIDJoinRoomRequest, "req-unready-join", &pb.JoinRoomRequest{
		PlayerId: guestPlayerID,
		RoomId:   roomID,
	})

	errorResponse := sendErrorEnvelope(t, hostConn, protocol.MessageIDStartRoomRequest, "req-unready-start", &pb.StartRoomRequest{
		PlayerId: hostPlayerID,
		RoomId:   roomID,
	})
	if errorResponse.GetCode() != pb.ErrorCode_ERROR_CODE_PAYLOAD_INVALID {
		t.Fatalf("error code = %v, want payload invalid", errorResponse.GetCode())
	}
	if !strings.Contains(errorResponse.GetDetail(), "room not ready") {
		t.Fatalf("error detail = %q, want room not ready", errorResponse.GetDetail())
	}
}

func TestRoomLobbyReconnectWebSocketFlow(t *testing.T) {
	server, err := newTestHTTPServer(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewHTTPServer() error = %v", err)
	}
	testServer := httptest.NewServer(server.Handler)
	defer testServer.Close()

	conn := dialAppGateway(t, testServer)
	session := registerTestPlayer(t, conn, "reconnect")
	playerID := session.GetPlayer().GetPlayerId()
	createResponse := sendRoomEnvelope[*pb.CreateRoomResponse](t, conn, protocol.MessageIDCreateRoomRequest, "req-create", &pb.CreateRoomRequest{
		PlayerId: playerID,
		RoomName: "测试房间",
		Capacity: 2,
	})
	roomID := createResponse.GetRoom().GetRoomId()
	_ = conn.Close(websocket.StatusNormalClosure, "simulate disconnect")

	reconnectConn := dialAppGateway(t, testServer)
	defer reconnectConn.Close(websocket.StatusNormalClosure, "test done")
	_ = sendRoomEnvelope[*pb.ResumeSessionResponse](t, reconnectConn, protocol.MessageIDResumeSessionRequest, "req-resume", &pb.ResumeSessionRequest{
		SessionToken: session.GetSessionToken(),
	})
	reconnectResponse := sendRoomEnvelope[*pb.ReconnectRoomResponse](t, reconnectConn, protocol.MessageIDReconnectRoomRequest, "req-reconnect", &pb.ReconnectRoomRequest{
		PlayerId: playerID,
		RoomId:   roomID,
	})
	if memberConnectionState(reconnectResponse.GetRoom(), playerID) != pb.RoomMemberConnectionState_ROOM_MEMBER_CONNECTION_STATE_ONLINE {
		t.Fatalf("%s connection state is not online after reconnect", playerID)
	}
}

func TestRoomRequestRequiresAuthenticatedConnection(t *testing.T) {
	server, err := newTestHTTPServer(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewHTTPServer() error = %v", err)
	}
	testServer := httptest.NewServer(server.Handler)
	defer testServer.Close()

	conn := dialAppGateway(t, testServer)
	defer conn.Close(websocket.StatusNormalClosure, "test done")

	errorResponse := sendErrorEnvelope(t, conn, protocol.MessageIDCreateRoomRequest, "req-create-unauthenticated", &pb.CreateRoomRequest{
		PlayerId: "player-forged",
		RoomName: "测试房间",
		Capacity: 2,
	})
	if errorResponse.GetCode() != pb.ErrorCode_ERROR_CODE_UNAUTHENTICATED {
		t.Fatalf("error code = %v, want unauthenticated", errorResponse.GetCode())
	}

	session := registerTestPlayer(t, conn, "after_unauthenticated")
	createResponse := sendRoomEnvelope[*pb.CreateRoomResponse](t, conn, protocol.MessageIDCreateRoomRequest, "req-create-after-auth", &pb.CreateRoomRequest{
		PlayerId: session.GetPlayer().GetPlayerId(),
		RoomName: "测试房间",
		Capacity: 2,
	})
	if createResponse.GetRoom().GetRoomId() != "room-1" {
		t.Fatalf("room id = %q, want room-1", createResponse.GetRoom().GetRoomId())
	}
}

func TestRoomRequestRejectsMismatchedPlayerID(t *testing.T) {
	server, err := newTestHTTPServer(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewHTTPServer() error = %v", err)
	}
	testServer := httptest.NewServer(server.Handler)
	defer testServer.Close()

	conn := dialAppGateway(t, testServer)
	defer conn.Close(websocket.StatusNormalClosure, "test done")
	session := registerTestPlayer(t, conn, "identity_mismatch")

	errorResponse := sendErrorEnvelope(t, conn, protocol.MessageIDCreateRoomRequest, "req-create-mismatch", &pb.CreateRoomRequest{
		PlayerId: "player-forged",
		RoomName: "测试房间",
		Capacity: 2,
	})
	if errorResponse.GetCode() != pb.ErrorCode_ERROR_CODE_UNAUTHENTICATED {
		t.Fatalf("error code = %v, want unauthenticated", errorResponse.GetCode())
	}

	createResponse := sendRoomEnvelope[*pb.CreateRoomResponse](t, conn, protocol.MessageIDCreateRoomRequest, "req-create-real", &pb.CreateRoomRequest{
		PlayerId: session.GetPlayer().GetPlayerId(),
		RoomName: "测试房间",
		Capacity: 2,
	})
	if createResponse.GetRoom().GetRoomId() != "room-1" {
		t.Fatalf("room id = %q, want room-1", createResponse.GetRoom().GetRoomId())
	}
}

func TestAccountSessionWebSocketFlow(t *testing.T) {
	server, err := newTestHTTPServer(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewHTTPServer() error = %v", err)
	}
	testServer := httptest.NewServer(server.Handler)
	defer testServer.Close()

	conn := dialAppGateway(t, testServer)
	defer conn.Close(websocket.StatusNormalClosure, "test done")

	registerResponse := sendRoomEnvelope[*pb.RegisterResponse](t, conn, protocol.MessageIDRegisterRequest, "req-register", &pb.RegisterRequest{
		Account:     "tester",
		Password:    "secret",
		DisplayName: "测试玩家",
	})
	if registerResponse.GetPlayer().GetPlayerId() == "" {
		t.Fatal("registered player id is empty")
	}
	if registerResponse.GetSessionToken() == "" {
		t.Fatal("session token is empty")
	}

	currentResponse := sendRoomEnvelope[*pb.GetCurrentPlayerResponse](t, conn, protocol.MessageIDGetCurrentPlayerRequest, "req-current", &pb.GetCurrentPlayerRequest{})
	if !currentResponse.GetAuthenticated() {
		t.Fatal("current player authenticated = false, want true")
	}
	if currentResponse.GetPlayer().GetPlayerId() != registerResponse.GetPlayer().GetPlayerId() {
		t.Fatalf("current player id = %q, want %q", currentResponse.GetPlayer().GetPlayerId(), registerResponse.GetPlayer().GetPlayerId())
	}

	logoutResponse := sendRoomEnvelope[*pb.LogoutResponse](t, conn, protocol.MessageIDLogoutRequest, "req-logout", &pb.LogoutRequest{
		SessionToken: registerResponse.GetSessionToken(),
	})
	if !logoutResponse.GetSuccess() {
		t.Fatal("logout success = false, want true")
	}

	currentResponse = sendRoomEnvelope[*pb.GetCurrentPlayerResponse](t, conn, protocol.MessageIDGetCurrentPlayerRequest, "req-current-after-logout", &pb.GetCurrentPlayerRequest{})
	if currentResponse.GetAuthenticated() {
		t.Fatal("current player authenticated = true after logout, want false")
	}
}

func TestAccountDuplicateRegisterReturnsError(t *testing.T) {
	server, err := newTestHTTPServer(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewHTTPServer() error = %v", err)
	}
	testServer := httptest.NewServer(server.Handler)
	defer testServer.Close()

	conn := dialAppGateway(t, testServer)
	defer conn.Close(websocket.StatusNormalClosure, "test done")

	_ = sendRoomEnvelope[*pb.RegisterResponse](t, conn, protocol.MessageIDRegisterRequest, "req-register", &pb.RegisterRequest{
		Account:  "tester",
		Password: "secret",
	})

	response := sendEnvelope(t, conn, protocol.MessageIDRegisterRequest, "req-register-duplicate", &pb.RegisterRequest{
		Account:  "tester",
		Password: "secret",
	})
	if response.GetMessageId() != pb.MessageID_MESSAGE_ID_ERROR_RESPONSE {
		t.Fatalf("message id = %v, want error response", response.GetMessageId())
	}
	decoded, err := protocol.DecodeEnvelope(response, true)
	if err != nil {
		t.Fatalf("DecodeEnvelope() error = %v", err)
	}
	errorResponse, ok := decoded.(*pb.ErrorResponse)
	if !ok {
		t.Fatalf("decoded response = %T, want *pb.ErrorResponse", decoded)
	}
	if errorResponse.GetCode() != pb.ErrorCode_ERROR_CODE_ACCOUNT_ALREADY_EXISTS {
		t.Fatalf("error code = %v, want account already exists", errorResponse.GetCode())
	}
}

func newTestHTTPServer(cfg config.Config, log *slog.Logger) (*http.Server, error) {
	store := storage.NewFakeStore()
	return newHTTPServer(cfg, log, serverDependencies{
		playerProfiles:  store,
		accountSessions: store,
	})
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

func registerTestPlayer(t *testing.T, conn *websocket.Conn, account string) *pb.RegisterResponse {
	t.Helper()
	response := sendRoomEnvelope[*pb.RegisterResponse](t, conn, protocol.MessageIDRegisterRequest, "req-register-"+account, &pb.RegisterRequest{
		Account:     account,
		Password:    "secret",
		DisplayName: account,
	})
	if response.GetPlayer().GetPlayerId() == "" {
		t.Fatal("registered player id is empty")
	}
	if response.GetSessionToken() == "" {
		t.Fatal("session token is empty")
	}
	return response
}

func sendRoomEnvelope[T proto.Message](t *testing.T, conn *websocket.Conn, messageID protocol.MessageID, requestID string, message proto.Message) T {
	t.Helper()
	responseEnvelope := sendEnvelope(t, conn, messageID, requestID, message)
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

func sendErrorEnvelope(t *testing.T, conn *websocket.Conn, messageID protocol.MessageID, requestID string, message proto.Message) *pb.ErrorResponse {
	t.Helper()
	responseEnvelope := sendEnvelope(t, conn, messageID, requestID, message)
	if responseEnvelope.GetMessageId() != pb.MessageID_MESSAGE_ID_ERROR_RESPONSE {
		t.Fatalf("message id = %v, want error response", responseEnvelope.GetMessageId())
	}
	decoded, err := protocol.DecodeEnvelope(responseEnvelope, true)
	if err != nil {
		t.Fatalf("DecodeEnvelope() error = %v", err)
	}
	errorResponse, ok := decoded.(*pb.ErrorResponse)
	if !ok {
		t.Fatalf("decoded response = %T, want *pb.ErrorResponse", decoded)
	}
	return errorResponse
}

func sendEnvelope(t *testing.T, conn *websocket.Conn, messageID protocol.MessageID, requestID string, message proto.Message) *pb.Envelope {
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
	return responseEnvelope
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
