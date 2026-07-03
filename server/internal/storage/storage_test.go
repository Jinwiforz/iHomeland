package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFakeStoreRoomSummaryIsIdempotent(t *testing.T) {
	store := NewFakeStore()
	ctx := context.Background()
	summary := RoomSummary{
		RoomID:         "room-1",
		Name:           "Alpha",
		HostPlayerID:   "player-1",
		State:          "open",
		Capacity:       4,
		MemberCount:    1,
		IdempotencyKey: "room-1",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := store.SaveRoomSummary(ctx, summary); err != nil {
		t.Fatalf("SaveRoomSummary failed: %v", err)
	}
	summary.MemberCount = 2
	if err := store.SaveRoomSummary(ctx, summary); err != nil {
		t.Fatalf("retry SaveRoomSummary failed: %v", err)
	}
	got, err := store.GetRoomSummary(ctx, "room-1")
	if err != nil {
		t.Fatalf("GetRoomSummary failed: %v", err)
	}
	if got.MemberCount != 2 {
		t.Fatalf("MemberCount = %d, want 2", got.MemberCount)
	}
	summary.IdempotencyKey = "other"
	if err := store.SaveRoomSummary(ctx, summary); !errors.Is(err, ErrConflict) {
		t.Fatalf("SaveRoomSummary conflict error = %v, want ErrConflict", err)
	}
}

func TestFakeStoreReconnectTokenOverwritesSameKey(t *testing.T) {
	store := NewFakeStore()
	ctx := context.Background()
	first := ReconnectToken{
		RoomID:   "room-1",
		PlayerID: "player-1",
		Deadline: time.Now().Add(10 * time.Second),
		IssuedAt: time.Now(),
	}
	second := first
	second.ConnectionID = "conn-2"
	second.Deadline = time.Now().Add(20 * time.Second)
	if err := store.SetReconnectToken(ctx, first, ReconnectTokenTTL); err != nil {
		t.Fatalf("SetReconnectToken failed: %v", err)
	}
	if err := store.SetReconnectToken(ctx, second, ReconnectTokenTTL); err != nil {
		t.Fatalf("overwrite SetReconnectToken failed: %v", err)
	}
	got, err := store.GetReconnectToken(ctx, "room-1", "player-1")
	if err != nil {
		t.Fatalf("GetReconnectToken failed: %v", err)
	}
	if got.ConnectionID != "conn-2" {
		t.Fatalf("ConnectionID = %q, want conn-2", got.ConnectionID)
	}
}

func TestRedisKeysAndTTLs(t *testing.T) {
	keys, err := NewRedisKeys("dev")
	if err != nil {
		t.Fatalf("NewRedisKeys failed: %v", err)
	}
	cases := []struct {
		name string
		got  func() (string, error)
		want string
	}{
		{name: "session", got: func() (string, error) { return keys.SessionConnection("conn-1") }, want: "ih:dev:session:connection:conn-1"},
		{name: "presence", got: func() (string, error) { return keys.PresencePlayer("player-1") }, want: "ih:dev:presence:player:player-1"},
		{name: "room index", got: func() (string, error) { return keys.RoomIndex("room-1") }, want: "ih:dev:room:index:room-1"},
		{name: "reconnect", got: func() (string, error) { return keys.RoomReconnect("room-1", "player-1") }, want: "ih:dev:room:reconnect:room-1:player-1"},
		{name: "lock", got: func() (string, error) { return keys.RoomLock("room-1") }, want: "ih:dev:lock:room:room-1"},
		{name: "rate", got: func() (string, error) { return keys.GatewayRate("ip-127") }, want: "ih:dev:rate:gateway:ip-127"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.got()
			if err != nil {
				t.Fatalf("key build failed: %v", err)
			}
			if got != tc.want {
				t.Fatalf("key = %q, want %q", got, tc.want)
			}
		})
	}
	if SessionConnectionTTL <= PresencePlayerTTL || ReconnectTokenTTL <= 0 || RoomLockTTL <= 0 || GatewayRateTTL <= 0 || RoomIndexTTL <= 0 {
		t.Fatalf("unexpected ttl constants")
	}
	if _, err := NewRedisKeys(""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("NewRedisKeys empty env error = %v, want ErrInvalidArgument", err)
	}
}

func TestAdapterConstructorsValidateInputs(t *testing.T) {
	if _, err := NewMySQLRoomSummaryRepository(nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("NewMySQLRoomSummaryRepository error = %v, want ErrInvalidArgument", err)
	}
	keys, err := NewRedisKeys("dev")
	if err != nil {
		t.Fatalf("NewRedisKeys failed: %v", err)
	}
	if _, err := NewRedisRuntimeCache(nil, keys); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("NewRedisRuntimeCache error = %v, want ErrInvalidArgument", err)
	}
}

func TestMigrationFilesExistAndFollowNaming(t *testing.T) {
	root := filepath.Join("migrations")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir migrations failed: %v", err)
	}
	foundUp := false
	foundDown := false
	for _, entry := range entries {
		switch entry.Name() {
		case "0001_room_lobby_summary.up.sql":
			foundUp = true
		case "0001_room_lobby_summary.down.sql":
			foundDown = true
		}
	}
	if !foundUp || !foundDown {
		t.Fatalf("migration files found up=%v down=%v, want both", foundUp, foundDown)
	}
}
