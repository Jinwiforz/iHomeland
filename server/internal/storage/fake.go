package storage

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// FakeStore 是无网络依赖的 storage adapter，供业务和 storage 单元测试使用。
type FakeStore struct {
	mu              sync.Mutex
	roomSummaries   map[string]RoomSummary
	presences       map[string]Presence
	roomIndexes     map[string]RoomIndexEntry
	reconnectTokens map[string]ReconnectToken
}

// NewFakeStore 创建内存 storage adapter。
func NewFakeStore() *FakeStore {
	return &FakeStore{
		roomSummaries:   make(map[string]RoomSummary),
		presences:       make(map[string]Presence),
		roomIndexes:     make(map[string]RoomIndexEntry),
		reconnectTokens: make(map[string]ReconnectToken),
	}
}

// SaveRoomSummary 幂等保存房间摘要，同一 room id 只保留一份最新摘要。
func (s *FakeStore) SaveRoomSummary(ctx context.Context, summary RoomSummary) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRoomSummary(summary); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.roomSummaries[summary.RoomID]; ok && existing.IdempotencyKey != summary.IdempotencyKey {
		return fmt.Errorf("%w: room summary idempotency key changed", ErrConflict)
	}
	s.roomSummaries[summary.RoomID] = summary
	return nil
}

// GetRoomSummary 读取房间摘要。
func (s *FakeStore) GetRoomSummary(ctx context.Context, roomID string) (RoomSummary, error) {
	if err := ctx.Err(); err != nil {
		return RoomSummary{}, err
	}
	if err := validateID(roomID); err != nil {
		return RoomSummary{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	summary, ok := s.roomSummaries[roomID]
	if !ok {
		return RoomSummary{}, ErrNotFound
	}
	return summary, nil
}

// SetPresence 写入玩家在线状态缓存。
func (s *FakeStore) SetPresence(ctx context.Context, presence Presence, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validatePresence(presence); err != nil {
		return err
	}
	if err := validateTTL(ttl); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.presences[presence.PlayerID] = presence
	return nil
}

// DeletePresence 删除玩家在线状态缓存，重复删除保持幂等。
func (s *FakeStore) DeletePresence(ctx context.Context, playerID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateID(playerID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.presences, playerID)
	return nil
}

// SetRoomIndex 写入房间索引缓存。
func (s *FakeStore) SetRoomIndex(ctx context.Context, entry RoomIndexEntry, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRoomIndex(entry); err != nil {
		return err
	}
	if err := validateTTL(ttl); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roomIndexes[entry.RoomID] = entry
	return nil
}

// DeleteRoomIndex 删除房间索引缓存，重复删除保持幂等。
func (s *FakeStore) DeleteRoomIndex(ctx context.Context, roomID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateID(roomID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.roomIndexes, roomID)
	return nil
}

// SetReconnectToken 写入重连资格，同一成员重复断线会覆盖同一 key。
func (s *FakeStore) SetReconnectToken(ctx context.Context, token ReconnectToken, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateReconnectToken(token); err != nil {
		return err
	}
	if err := validateTTL(ttl); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconnectTokens[reconnectTokenMapKey(token.RoomID, token.PlayerID)] = token
	return nil
}

// GetReconnectToken 读取重连资格。
func (s *FakeStore) GetReconnectToken(ctx context.Context, roomID string, playerID string) (ReconnectToken, error) {
	if err := ctx.Err(); err != nil {
		return ReconnectToken{}, err
	}
	if validateID(roomID) != nil || validateID(playerID) != nil {
		return ReconnectToken{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.reconnectTokens[reconnectTokenMapKey(roomID, playerID)]
	if !ok {
		return ReconnectToken{}, ErrNotFound
	}
	return token, nil
}

// DeleteReconnectToken 删除重连资格，重复删除保持幂等。
func (s *FakeStore) DeleteReconnectToken(ctx context.Context, roomID string, playerID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if validateID(roomID) != nil || validateID(playerID) != nil {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.reconnectTokens, reconnectTokenMapKey(roomID, playerID))
	return nil
}

func reconnectTokenMapKey(roomID string, playerID string) string {
	return roomID + "\x00" + playerID
}
