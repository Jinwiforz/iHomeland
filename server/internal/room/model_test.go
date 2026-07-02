package room

import (
	"errors"
	"testing"
	"time"
)

func TestRoomLifecycle(t *testing.T) {
	now := time.Unix(100, 0)
	room, err := NewRoom("room-1", "测试房间", "player-1", 3, now)
	if err != nil {
		t.Fatalf("NewRoom() error = %v", err)
	}
	if room.HostPlayerID != "player-1" {
		t.Fatalf("HostPlayerID = %q, want player-1", room.HostPlayerID)
	}

	snapshot, err := room.Join("player-2", now.Add(time.Second))
	if err != nil {
		t.Fatalf("Join() error = %v", err)
	}
	if len(snapshot.Members) != 2 {
		t.Fatalf("members = %d, want 2", len(snapshot.Members))
	}

	snapshot, err = room.SetReady("player-2", true, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("SetReady() error = %v", err)
	}
	if !snapshot.Members[1].Ready {
		t.Fatal("player-2 ready = false, want true")
	}

	snapshot, err = room.TransferHost("player-1", "player-2", now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("TransferHost() error = %v", err)
	}
	if snapshot.HostPlayerID != "player-2" {
		t.Fatalf("HostPlayerID = %q, want player-2", snapshot.HostPlayerID)
	}

	snapshot, err = room.Leave("player-2", now.Add(4*time.Second))
	if err != nil {
		t.Fatalf("Leave() error = %v", err)
	}
	if snapshot.HostPlayerID != "player-1" {
		t.Fatalf("HostPlayerID after host leave = %q, want player-1", snapshot.HostPlayerID)
	}
}

func TestRoomRejectsInvalidTransitions(t *testing.T) {
	now := time.Unix(100, 0)
	room, err := NewRoom("room-1", "测试房间", "player-1", 2, now)
	if err != nil {
		t.Fatalf("NewRoom() error = %v", err)
	}
	if _, err := room.Join("player-2", now); err != nil {
		t.Fatalf("Join(player-2) error = %v", err)
	}
	if _, err := room.Join("player-3", now); !errors.Is(err, ErrRoomFull) {
		t.Fatalf("Join(full) error = %v, want ErrRoomFull", err)
	}
	if _, err := room.TransferHost("player-2", "player-1", now); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("TransferHost(non-host) error = %v, want ErrPermissionDenied", err)
	}
	if _, err := room.SetReady("missing", true, now); !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("SetReady(missing) error = %v, want ErrMemberNotFound", err)
	}
}

func TestRoomDisconnectAndReconnect(t *testing.T) {
	now := time.Unix(100, 0)
	room, err := NewRoom("room-1", "测试房间", "player-1", 2, now)
	if err != nil {
		t.Fatalf("NewRoom() error = %v", err)
	}
	deadline := now.Add(30 * time.Second)
	snapshot, err := room.Disconnect("player-1", deadline, now)
	if err != nil {
		t.Fatalf("Disconnect() error = %v", err)
	}
	if snapshot.Members[0].ConnectionState != MemberConnectionStateDisconnected {
		t.Fatalf("ConnectionState = %s, want disconnected", snapshot.Members[0].ConnectionState)
	}

	snapshot, err = room.Reconnect("player-1", now.Add(time.Second))
	if err != nil {
		t.Fatalf("Reconnect() error = %v", err)
	}
	if snapshot.Members[0].ConnectionState != MemberConnectionStateOnline {
		t.Fatalf("ConnectionState = %s, want online", snapshot.Members[0].ConnectionState)
	}
}

func TestRoomRejectsExpiredReconnect(t *testing.T) {
	now := time.Unix(100, 0)
	room, err := NewRoom("room-1", "测试房间", "player-1", 2, now)
	if err != nil {
		t.Fatalf("NewRoom() error = %v", err)
	}
	if _, err := room.Disconnect("player-1", now.Add(time.Second), now); err != nil {
		t.Fatalf("Disconnect() error = %v", err)
	}
	if _, err := room.Reconnect("player-1", now.Add(2*time.Second)); !errors.Is(err, ErrReconnectExpired) {
		t.Fatalf("Reconnect(expired) error = %v, want ErrReconnectExpired", err)
	}
}
