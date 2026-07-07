package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// RedisClient 是 Redis adapter 需要的最小 key/value 能力，避免业务层依赖具体 client。
type RedisClient interface {
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Del(ctx context.Context, key string) error
	Get(ctx context.Context, key string) ([]byte, error)
}

// RedisRuntimeCache 是第一阶段 Redis 运行态 cache adapter。
type RedisRuntimeCache struct {
	client RedisClient
	keys   RedisKeys
}

// RedisAccountSessionCache 是账号 session token 的 Redis cache adapter。
type RedisAccountSessionCache struct {
	client RedisClient
	keys   RedisKeys
}

// NewRedisAccountSessionCache 创建 Redis 账号 session cache。
func NewRedisAccountSessionCache(client RedisClient, keys RedisKeys) (*RedisAccountSessionCache, error) {
	if client == nil || keys.Env() == "" {
		return nil, ErrInvalidArgument
	}
	return &RedisAccountSessionCache{client: client, keys: keys}, nil
}

// SetAccountSession 写入短期账号 session，TTL 必须由 account service 根据过期时间传入。
func (c *RedisAccountSessionCache) SetAccountSession(ctx context.Context, session AccountSession, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateAccountSession(session); err != nil {
		return err
	}
	if err := validateTTL(ttl); err != nil {
		return err
	}
	key, err := c.keys.AccountSession(session.SessionToken)
	if err != nil {
		return err
	}
	value, err := json.Marshal(accountSessionValue{
		SessionToken: session.SessionToken,
		PlayerID:     session.PlayerID,
		AccountName:  session.AccountName,
		IssuedAtMs:   session.IssuedAt.UnixMilli(),
		ExpiresAtMs:  session.ExpiresAt.UnixMilli(),
		ConnectionID: session.ConnectionID,
	})
	if err != nil {
		return fmt.Errorf("%w: encode account session: %v", ErrInvalidArgument, err)
	}
	if err := c.client.Set(ctx, key, value, ttl); err != nil {
		return fmt.Errorf("%w: set account session: %v", ErrUnavailable, err)
	}
	return nil
}

// GetAccountSession 读取并解码账号 session；Redis miss 映射为 ErrNotFound。
func (c *RedisAccountSessionCache) GetAccountSession(ctx context.Context, sessionToken string) (AccountSession, error) {
	if err := ctx.Err(); err != nil {
		return AccountSession{}, err
	}
	key, err := c.keys.AccountSession(sessionToken)
	if err != nil {
		return AccountSession{}, err
	}
	data, err := c.client.Get(ctx, key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return AccountSession{}, ErrNotFound
		}
		return AccountSession{}, fmt.Errorf("%w: get account session: %v", ErrUnavailable, err)
	}
	var value accountSessionValue
	if err := json.Unmarshal(data, &value); err != nil {
		return AccountSession{}, fmt.Errorf("%w: decode account session: %v", ErrInvalidArgument, err)
	}
	session := AccountSession{
		SessionToken: value.SessionToken,
		PlayerID:     value.PlayerID,
		AccountName:  value.AccountName,
		IssuedAt:     time.UnixMilli(value.IssuedAtMs),
		ExpiresAt:    time.UnixMilli(value.ExpiresAtMs),
		ConnectionID: value.ConnectionID,
	}
	if err := validateAccountSession(session); err != nil {
		return AccountSession{}, err
	}
	return session, nil
}

// DeleteAccountSession 删除账号 session；删除不存在的 key 保持幂等。
func (c *RedisAccountSessionCache) DeleteAccountSession(ctx context.Context, sessionToken string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key, err := c.keys.AccountSession(sessionToken)
	if err != nil {
		return err
	}
	if err := c.client.Del(ctx, key); err != nil {
		return fmt.Errorf("%w: delete account session: %v", ErrUnavailable, err)
	}
	return nil
}

type accountSessionValue struct {
	SessionToken string `json:"session_token"`
	PlayerID     string `json:"player_id"`
	AccountName  string `json:"account_name"`
	IssuedAtMs   int64  `json:"issued_at_ms"`
	ExpiresAtMs  int64  `json:"expires_at_ms"`
	ConnectionID string `json:"connection_id"`
}

// NewRedisRuntimeCache 创建 Redis 运行态 cache。
func NewRedisRuntimeCache(client RedisClient, keys RedisKeys) (*RedisRuntimeCache, error) {
	if client == nil || keys.Env() == "" {
		return nil, ErrInvalidArgument
	}
	return &RedisRuntimeCache{client: client, keys: keys}, nil
}

// SetPresence 校验并写入玩家在线状态缓存。
func (c *RedisRuntimeCache) SetPresence(ctx context.Context, presence Presence, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validatePresence(presence); err != nil {
		return err
	}
	if err := validateTTL(ttl); err != nil {
		return err
	}
	return fmt.Errorf("%w: redis presence adapter is not wired to value codec yet", ErrUnavailable)
}

// DeletePresence 删除玩家在线状态缓存。
func (c *RedisRuntimeCache) DeletePresence(ctx context.Context, playerID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key, err := c.keys.PresencePlayer(playerID)
	if err != nil {
		return err
	}
	return c.client.Del(ctx, key)
}

// SetRoomIndex 校验并写入房间索引缓存。
func (c *RedisRuntimeCache) SetRoomIndex(ctx context.Context, entry RoomIndexEntry, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRoomIndex(entry); err != nil {
		return err
	}
	if err := validateTTL(ttl); err != nil {
		return err
	}
	return fmt.Errorf("%w: redis room index adapter is not wired to value codec yet", ErrUnavailable)
}

// DeleteRoomIndex 删除房间索引缓存。
func (c *RedisRuntimeCache) DeleteRoomIndex(ctx context.Context, roomID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key, err := c.keys.RoomIndex(roomID)
	if err != nil {
		return err
	}
	return c.client.Del(ctx, key)
}

// SetReconnectToken 校验并写入重连资格缓存。
func (c *RedisRuntimeCache) SetReconnectToken(ctx context.Context, token ReconnectToken, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateReconnectToken(token); err != nil {
		return err
	}
	if err := validateTTL(ttl); err != nil {
		return err
	}
	return fmt.Errorf("%w: redis reconnect token adapter is not wired to value codec yet", ErrUnavailable)
}

// GetReconnectToken 校验并读取重连资格缓存。
func (c *RedisRuntimeCache) GetReconnectToken(ctx context.Context, roomID string, playerID string) (ReconnectToken, error) {
	if err := ctx.Err(); err != nil {
		return ReconnectToken{}, err
	}
	if _, err := c.keys.RoomReconnect(roomID, playerID); err != nil {
		return ReconnectToken{}, err
	}
	return ReconnectToken{}, fmt.Errorf("%w: redis reconnect token adapter is not wired to value codec yet", ErrUnavailable)
}

// DeleteReconnectToken 删除重连资格缓存。
func (c *RedisRuntimeCache) DeleteReconnectToken(ctx context.Context, roomID string, playerID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key, err := c.keys.RoomReconnect(roomID, playerID)
	if err != nil {
		return err
	}
	return c.client.Del(ctx, key)
}
