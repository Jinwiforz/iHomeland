// Package storage 定义第一阶段房间大厅的持久化与运行态缓存边界。
package storage

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	// ErrInvalidArgument 表示调用 storage 边界时传入了非法参数。
	ErrInvalidArgument = errors.New("storage invalid argument")
	// ErrNotFound 表示目标数据不存在。
	ErrNotFound = errors.New("storage not found")
	// ErrConflict 表示幂等键或状态与现有数据冲突。
	ErrConflict = errors.New("storage conflict")
	// ErrUnavailable 表示底层依赖暂不可用。
	ErrUnavailable = errors.New("storage unavailable")
)

// RoomSummary 是 MySQL 中保存的房间摘要事实。
type RoomSummary struct {
	RoomID         string
	Name           string
	HostPlayerID   string
	State          string
	Capacity       int
	MemberCount    int
	IdempotencyKey string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ClosedAt       time.Time
}

// Presence 描述玩家当前连接运行态。
type Presence struct {
	PlayerID     string
	ConnectionID string
	RoomID       string
	Online       bool
	UpdatedAt    time.Time
}

// RoomIndexEntry 描述 Redis 房间索引缓存条目。
type RoomIndexEntry struct {
	RoomID      string
	Name        string
	State       string
	Capacity    int
	MemberCount int
	UpdatedAt   time.Time
}

// ReconnectToken 描述短期重连资格。
type ReconnectToken struct {
	RoomID       string
	PlayerID     string
	ConnectionID string
	Deadline     time.Time
	IssuedAt     time.Time
}

// RoomSummaryRepository 保存和读取房间摘要。
type RoomSummaryRepository interface {
	SaveRoomSummary(ctx context.Context, summary RoomSummary) error
	GetRoomSummary(ctx context.Context, roomID string) (RoomSummary, error)
}

// PresenceCache 保存玩家在线状态缓存。
type PresenceCache interface {
	SetPresence(ctx context.Context, presence Presence, ttl time.Duration) error
	DeletePresence(ctx context.Context, playerID string) error
}

// RoomIndexCache 保存房间索引缓存。
type RoomIndexCache interface {
	SetRoomIndex(ctx context.Context, entry RoomIndexEntry, ttl time.Duration) error
	DeleteRoomIndex(ctx context.Context, roomID string) error
}

// ReconnectTokenCache 保存短期重连资格缓存。
type ReconnectTokenCache interface {
	SetReconnectToken(ctx context.Context, token ReconnectToken, ttl time.Duration) error
	GetReconnectToken(ctx context.Context, roomID string, playerID string) (ReconnectToken, error)
	DeleteReconnectToken(ctx context.Context, roomID string, playerID string) error
}

func validateID(id string) error {
	if strings.TrimSpace(id) == "" {
		return ErrInvalidArgument
	}
	return nil
}

func validateTTL(ttl time.Duration) error {
	if ttl <= 0 {
		return ErrInvalidArgument
	}
	return nil
}

func validateRoomSummary(summary RoomSummary) error {
	if validateID(summary.RoomID) != nil || strings.TrimSpace(summary.Name) == "" || strings.TrimSpace(summary.State) == "" {
		return ErrInvalidArgument
	}
	if summary.Capacity <= 0 || summary.MemberCount < 0 || summary.MemberCount > summary.Capacity {
		return ErrInvalidArgument
	}
	if strings.TrimSpace(summary.IdempotencyKey) == "" {
		return ErrInvalidArgument
	}
	return nil
}

func validatePresence(presence Presence) error {
	if validateID(presence.PlayerID) != nil || validateID(presence.ConnectionID) != nil {
		return ErrInvalidArgument
	}
	return nil
}

func validateRoomIndex(entry RoomIndexEntry) error {
	if validateID(entry.RoomID) != nil || strings.TrimSpace(entry.Name) == "" || strings.TrimSpace(entry.State) == "" {
		return ErrInvalidArgument
	}
	if entry.Capacity <= 0 || entry.MemberCount < 0 || entry.MemberCount > entry.Capacity {
		return ErrInvalidArgument
	}
	return nil
}

func validateReconnectToken(token ReconnectToken) error {
	if validateID(token.RoomID) != nil || validateID(token.PlayerID) != nil || token.Deadline.IsZero() {
		return ErrInvalidArgument
	}
	return nil
}
