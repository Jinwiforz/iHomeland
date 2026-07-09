package room

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServiceCreateJoinAndDuplicateJoin(t *testing.T) {
	service := NewService(NewMemoryRepository(), Config{})
	service.now = func() time.Time { return time.Unix(100, 0) }

	created, err := service.CreateRoom(context.Background(), CreateRoomRequest{
		PlayerID: "player-1",
		RoomName: "测试房间",
		Capacity: 2,
	})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}

	joined, err := service.JoinRoom(context.Background(), JoinRoomRequest{
		PlayerID: "player-2",
		RoomID:   created.RoomID,
	})
	if err != nil {
		t.Fatalf("JoinRoom() error = %v", err)
	}
	if len(joined.Members) != 2 {
		t.Fatalf("members = %d, want 2", len(joined.Members))
	}

	joinedAgain, err := service.JoinRoom(context.Background(), JoinRoomRequest{
		PlayerID: "player-2",
		RoomID:   created.RoomID,
	})
	if err != nil {
		t.Fatalf("JoinRoom(duplicate) error = %v", err)
	}
	if len(joinedAgain.Members) != 2 {
		t.Fatalf("members after duplicate join = %d, want 2", len(joinedAgain.Members))
	}
}

func TestServiceStartRoom(t *testing.T) {
	now := time.Unix(100, 0)
	service := NewService(NewMemoryRepository(), Config{})
	service.now = func() time.Time { return now }

	created, err := service.CreateRoom(context.Background(), CreateRoomRequest{
		PlayerID: "player-1",
		RoomName: "测试房间",
		Capacity: 2,
	})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}
	if _, err := service.JoinRoom(context.Background(), JoinRoomRequest{
		PlayerID: "player-2",
		RoomID:   created.RoomID,
	}); err != nil {
		t.Fatalf("JoinRoom() error = %v", err)
	}
	if _, err := service.SetReady(context.Background(), SetReadyRequest{
		PlayerID: "player-2",
		RoomID:   created.RoomID,
		Ready:    true,
	}); err != nil {
		t.Fatalf("SetReady() error = %v", err)
	}

	now = now.Add(time.Second)
	started, err := service.StartRoom(context.Background(), StartRoomRequest{
		PlayerID: "player-1",
		RoomID:   created.RoomID,
	})
	if err != nil {
		t.Fatalf("StartRoom() error = %v", err)
	}
	if started.State != RoomStateStarted {
		t.Fatalf("State = %s, want started", started.State)
	}
}

func TestServiceConnectionDisconnectAndReconnect(t *testing.T) {
	now := time.Unix(100, 0)
	service := NewService(NewMemoryRepository(), Config{ReconnectTTL: 10 * time.Second})
	service.now = func() time.Time { return now }

	created, err := service.CreateRoom(context.Background(), CreateRoomRequest{
		PlayerID: "player-1",
		RoomName: "测试房间",
		Capacity: 2,
	})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}
	service.BindConnection("conn-1", "player-1", created.RoomID)

	disconnected, err := service.DisconnectConnection(context.Background(), "conn-1")
	if err != nil {
		t.Fatalf("DisconnectConnection() error = %v", err)
	}
	if disconnected.Members[0].ConnectionState != MemberConnectionStateDisconnected {
		t.Fatalf("ConnectionState = %s, want disconnected", disconnected.Members[0].ConnectionState)
	}

	now = now.Add(time.Second)
	reconnected, err := service.ReconnectMember(context.Background(), ReconnectMemberRequest{
		PlayerID: "player-1",
		RoomID:   created.RoomID,
	})
	if err != nil {
		t.Fatalf("ReconnectMember() error = %v", err)
	}
	if reconnected.Members[0].ConnectionState != MemberConnectionStateOnline {
		t.Fatalf("ConnectionState = %s, want online", reconnected.Members[0].ConnectionState)
	}
}

func TestServiceRejectsExpiredReconnect(t *testing.T) {
	now := time.Unix(100, 0)
	service := NewService(NewMemoryRepository(), Config{ReconnectTTL: time.Second})
	service.now = func() time.Time { return now }

	created, err := service.CreateRoom(context.Background(), CreateRoomRequest{
		PlayerID: "player-1",
		RoomName: "测试房间",
		Capacity: 2,
	})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}
	service.BindConnection("conn-1", "player-1", created.RoomID)
	if _, err := service.DisconnectConnection(context.Background(), "conn-1"); err != nil {
		t.Fatalf("DisconnectConnection() error = %v", err)
	}

	now = now.Add(2 * time.Second)
	_, err = service.ReconnectMember(context.Background(), ReconnectMemberRequest{
		PlayerID: "player-1",
		RoomID:   created.RoomID,
	})
	if !errors.Is(err, ErrReconnectExpired) {
		t.Fatalf("ReconnectMember(expired) error = %v, want ErrReconnectExpired", err)
	}
}
