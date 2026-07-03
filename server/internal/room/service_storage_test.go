package room

import (
	"context"
	"testing"
	"time"

	"ihomeland/server/internal/storage"
)

func TestServiceSavesRoomSummaryThroughStorageBoundary(t *testing.T) {
	ctx := context.Background()
	store := storage.NewFakeStore()
	service := NewService(NewMemoryRepository(), Config{
		RoomSummaryRepository: store,
		ReconnectTokenCache:   store,
	})

	snapshot, err := service.CreateRoom(ctx, CreateRoomRequest{
		PlayerID: "player-1",
		RoomName: "Alpha",
		Capacity: 4,
	})
	if err != nil {
		t.Fatalf("CreateRoom failed: %v", err)
	}
	if _, err := service.JoinRoom(ctx, JoinRoomRequest{RoomID: snapshot.RoomID, PlayerID: "player-2"}); err != nil {
		t.Fatalf("JoinRoom failed: %v", err)
	}
	summary, err := store.GetRoomSummary(ctx, snapshot.RoomID)
	if err != nil {
		t.Fatalf("GetRoomSummary failed: %v", err)
	}
	if summary.MemberCount != 2 || summary.State != string(RoomStateOpen) {
		t.Fatalf("summary = %+v, want member count 2 and open state", summary)
	}
}

func TestServiceManagesReconnectTokenThroughStorageBoundary(t *testing.T) {
	ctx := context.Background()
	store := storage.NewFakeStore()
	service := NewService(NewMemoryRepository(), Config{
		ReconnectTTL:          time.Minute,
		RoomSummaryRepository: store,
		ReconnectTokenCache:   store,
	})

	snapshot, err := service.CreateRoom(ctx, CreateRoomRequest{
		PlayerID: "player-1",
		RoomName: "Alpha",
		Capacity: 4,
	})
	if err != nil {
		t.Fatalf("CreateRoom failed: %v", err)
	}
	if _, err := service.DisconnectMember(ctx, snapshot.RoomID, "player-1"); err != nil {
		t.Fatalf("DisconnectMember failed: %v", err)
	}
	token, err := store.GetReconnectToken(ctx, snapshot.RoomID, "player-1")
	if err != nil {
		t.Fatalf("GetReconnectToken failed: %v", err)
	}
	if token.Deadline.IsZero() {
		t.Fatalf("ReconnectToken deadline is zero")
	}
	if _, err := service.ReconnectMember(ctx, ReconnectMemberRequest{RoomID: snapshot.RoomID, PlayerID: "player-1"}); err != nil {
		t.Fatalf("ReconnectMember failed: %v", err)
	}
	if _, err := store.GetReconnectToken(ctx, snapshot.RoomID, "player-1"); err != storage.ErrNotFound {
		t.Fatalf("GetReconnectToken after reconnect error = %v, want ErrNotFound", err)
	}
}
