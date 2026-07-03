package storage

import (
	"context"
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
