package storage

import (
	"fmt"
	"strings"
	"time"
)

const (
	keyNamespace = "ih"

	// SessionConnectionTTL 是连接会话 key 的默认 TTL。
	SessionConnectionTTL = 30 * time.Minute
	// AccountSessionTTL 是账号 session token key 的默认 TTL。
	AccountSessionTTL = 24 * time.Hour
	// PresencePlayerTTL 是玩家在线状态 key 的默认 TTL。
	PresencePlayerTTL = 2 * time.Minute
	// RoomIndexTTL 是房间索引 key 的默认 TTL。
	RoomIndexTTL = 5 * time.Minute
	// ReconnectTokenTTL 是重连资格 key 的默认 TTL。
	ReconnectTokenTTL = 30 * time.Second
	// RoomLockTTL 是房间短锁 key 的默认 TTL。
	RoomLockTTL = 10 * time.Second
	// GatewayRateTTL 是网关限流 key 的默认 TTL。
	GatewayRateTTL = time.Minute
)

// RedisKeys 构造第一阶段 Redis key。
type RedisKeys struct {
	env string
}

// NewRedisKeys 创建 Redis key builder。
func NewRedisKeys(env string) (RedisKeys, error) {
	env = strings.TrimSpace(env)
	if env == "" {
		return RedisKeys{}, ErrInvalidArgument
	}
	return RedisKeys{env: env}, nil
}

// Env 返回 key 所属环境名。
func (k RedisKeys) Env() string {
	return k.env
}

// SessionConnection 返回连接会话 key。
func (k RedisKeys) SessionConnection(connectionID string) (string, error) {
	return k.build("session", "connection", connectionID)
}

// AccountSession 返回账号 session token key。
func (k RedisKeys) AccountSession(sessionToken string) (string, error) {
	return k.build("account", "session", sessionToken)
}

// PresencePlayer 返回玩家在线状态 key。
func (k RedisKeys) PresencePlayer(playerID string) (string, error) {
	return k.build("presence", "player", playerID)
}

// RoomIndex 返回房间索引 key。
func (k RedisKeys) RoomIndex(roomID string) (string, error) {
	return k.build("room", "index", roomID)
}

// RoomReconnect 返回玩家重连资格 key。
func (k RedisKeys) RoomReconnect(roomID string, playerID string) (string, error) {
	if err := validateID(roomID); err != nil {
		return "", err
	}
	if err := validateID(playerID); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%s:room:reconnect:%s:%s", keyNamespace, k.env, strings.TrimSpace(roomID), strings.TrimSpace(playerID)), nil
}

// RoomLock 返回房间短锁 key。
func (k RedisKeys) RoomLock(roomID string) (string, error) {
	return k.build("lock", "room", roomID)
}

// GatewayRate 返回网关限流 key。
func (k RedisKeys) GatewayRate(identity string) (string, error) {
	return k.build("rate", "gateway", identity)
}

func (k RedisKeys) build(parts ...string) (string, error) {
	if strings.TrimSpace(k.env) == "" {
		return "", ErrInvalidArgument
	}
	all := []string{keyNamespace, k.env}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return "", ErrInvalidArgument
		}
		all = append(all, part)
	}
	return strings.Join(all, ":"), nil
}
