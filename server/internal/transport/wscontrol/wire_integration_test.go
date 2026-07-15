package wscontrol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	controlv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/control/v1"
	visitv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/visit/v1"
	worldv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/world/v1"
	"github.com/jinwiforz/ihomeland/server/internal/protocol"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"google.golang.org/protobuf/proto"
)

// TestRegistryDeliversAllPushesOverRealWebSocket 验证9类typed publisher穿过真实binary WebSocket writer。
func TestRegistryDeliversAllPushesOverRealWebSocket(t *testing.T) {
	serverConnections := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(response, request, &websocket.AcceptOptions{Subprotocols: []string{Subprotocol}, CompressionMode: websocket.CompressionDisabled})
		if err == nil {
			serverConnections <- connection
		}
	}))
	defer server.Close()
	client, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http")+Path, &websocket.DialOptions{Subprotocols: []string{Subprotocol}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()
	serverConnection := <-serverConnections

	registry := newTestRegistry(t, testConfig())
	reserved, err := registry.reserve("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	connectionID, err := registry.register(reserved, "ses_wire", "ply_wire", 1, serverConnection)
	if err != nil {
		t.Fatal(err)
	}
	pushes := []struct {
		id      uint32
		payload proto.Message
	}{
		{500, controlv1.MaintenancePush_builder{}.Build()},
		{501, controlv1.ForcedLogoutPush_builder{}.Build()},
		{502, controlv1.QueueStatusPush_builder{}.Build()},
		{503, controlv1.EndpointUpdatePush_builder{}.Build()},
		{504, controlv1.SessionInvalidatedPush_builder{}.Build()},
		{2003, worldv1.WorldAssignmentChangedPush_builder{}.Build()},
		{2100, visitv1.VisitInvitePush_builder{}.Build()},
		{2101, visitv1.VisitOwnerAvailabilityPush_builder{}.Build()},
		{2102, visitv1.VisitClosedNoticePush_builder{}.Build()},
	}
	for index, push := range pushes {
		result, err := registry.PublishConnection(connectionID, push.id, push.payload)
		if err != nil || result.Enqueued != 1 {
			t.Fatalf("publish %d: result=%+v err=%v", push.id, result, err)
		}
		readContext, cancel := context.WithTimeout(context.Background(), time.Second)
		messageType, encoded, readErr := client.Read(readContext)
		cancel()
		if readErr != nil || messageType != websocket.MessageBinary {
			t.Fatalf("read %d: type=%v err=%v", push.id, messageType, readErr)
		}
		envelope, decodeErr := protocol.UnmarshalEnvelope(encoded)
		if decodeErr != nil || envelope.GetMessageId() != push.id || envelope.GetSequence() != uint64(index+1) {
			t.Fatalf("envelope %d drifted: envelope=%v err=%v", push.id, envelope, decodeErr)
		}
	}
}

// TestRegistryInvalidatesRealWebSocketWithinDeadline 验证最终PUSH与close不会阻塞Session调用方。
func TestRegistryInvalidatesRealWebSocketWithinDeadline(t *testing.T) {
	serverConnections := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(response, request, &websocket.AcceptOptions{Subprotocols: []string{Subprotocol}})
		if err == nil {
			serverConnections <- connection
		}
	}))
	defer server.Close()
	client, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http")+Path, &websocket.DialOptions{Subprotocols: []string{Subprotocol}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()
	registry := newTestRegistry(t, testConfig())
	reserved, _ := registry.reserve("127.0.0.1")
	if _, err := registry.register(reserved, "ses_invalidate", "ply_invalidate", 1, <-serverConnections); err != nil {
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() {
		readContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _, readErr := client.Read(readContext)
		readDone <- readErr
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	err = registry.Invalidate(ctx, session.Invalidation{SessionID: mustSessionID(t, "ses_invalidate"), Epoch: 2, Reason: session.InvalidationReasonLogout})
	if err != nil || time.Since(started) >= time.Second {
		t.Fatalf("real websocket invalidation duration=%s err=%v", time.Since(started), err)
	}
	if err := <-readDone; err != nil {
		t.Fatalf("client did not receive final invalidation push: %v", err)
	}
}
