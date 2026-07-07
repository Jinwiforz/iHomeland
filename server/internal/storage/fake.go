package storage

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// FakeStore 是无网络依赖的 storage adapter，供业务和 storage 单元测试使用。
type FakeStore struct {
	mu              sync.Mutex
	playerProfiles  map[string]PlayerProfile
	playersByID     map[string]string
	accountSessions map[string]AccountSession
	roomSummaries   map[string]RoomSummary
	presences       map[string]Presence
	roomIndexes     map[string]RoomIndexEntry
	reconnectTokens map[string]ReconnectToken
}

// NewFakeStore 创建内存 storage adapter。
func NewFakeStore() *FakeStore {
	return &FakeStore{
		roomSummaries:   make(map[string]RoomSummary),
		playerProfiles:  make(map[string]PlayerProfile),
		playersByID:     make(map[string]string),
		accountSessions: make(map[string]AccountSession),
		presences:       make(map[string]Presence),
		roomIndexes:     make(map[string]RoomIndexEntry),
		reconnectTokens: make(map[string]ReconnectToken),
	}
}

// CreatePlayerProfile 创建玩家账号基础资料，账号名和玩家 ID 必须唯一。
func (s *FakeStore) CreatePlayerProfile(ctx context.Context, profile PlayerProfile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validatePlayerProfile(profile); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	accountName := normalizeAccountName(profile.AccountName)
	if _, ok := s.playerProfiles[accountName]; ok {
		return fmt.Errorf("%w: account already exists", ErrConflict)
	}
	if _, ok := s.playersByID[profile.PlayerID]; ok {
		return fmt.Errorf("%w: player id already exists", ErrConflict)
	}
	profile.AccountName = accountName
	s.playerProfiles[accountName] = profile
	s.playersByID[profile.PlayerID] = accountName
	return nil
}

// GetPlayerProfileByAccount 按账号名读取玩家资料。
func (s *FakeStore) GetPlayerProfileByAccount(ctx context.Context, accountName string) (PlayerProfile, error) {
	if err := ctx.Err(); err != nil {
		return PlayerProfile{}, err
	}
	accountName = normalizeAccountName(accountName)
	if accountName == "" {
		return PlayerProfile{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.playerProfiles[accountName]
	if !ok {
		return PlayerProfile{}, ErrNotFound
	}
	return profile, nil
}

// GetPlayerProfileByID 按玩家 ID 读取玩家资料。
func (s *FakeStore) GetPlayerProfileByID(ctx context.Context, playerID string) (PlayerProfile, error) {
	if err := ctx.Err(); err != nil {
		return PlayerProfile{}, err
	}
	if err := validateID(playerID); err != nil {
		return PlayerProfile{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	accountName, ok := s.playersByID[playerID]
	if !ok {
		return PlayerProfile{}, ErrNotFound
	}
	return s.playerProfiles[accountName], nil
}

// SetAccountSession 写入账号 session，同一 token 重试写入会覆盖同一 key。
func (s *FakeStore) SetAccountSession(ctx context.Context, session AccountSession, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateAccountSession(session); err != nil {
		return err
	}
	if err := validateTTL(ttl); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accountSessions[session.SessionToken] = session
	return nil
}

// GetAccountSession 读取账号 session。
func (s *FakeStore) GetAccountSession(ctx context.Context, sessionToken string) (AccountSession, error) {
	if err := ctx.Err(); err != nil {
		return AccountSession{}, err
	}
	if err := validateID(sessionToken); err != nil {
		return AccountSession{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.accountSessions[strings.TrimSpace(sessionToken)]
	if !ok {
		return AccountSession{}, ErrNotFound
	}
	return session, nil
}

// DeleteAccountSession 删除账号 session，重复删除保持幂等。
func (s *FakeStore) DeleteAccountSession(ctx context.Context, sessionToken string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateID(sessionToken); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.accountSessions, strings.TrimSpace(sessionToken))
	return nil
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

func normalizeAccountName(accountName string) string {
	return strings.ToLower(strings.TrimSpace(accountName))
}
