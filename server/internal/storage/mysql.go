package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// SQLExecutor 是 MySQL adapter 需要的最小执行能力，避免业务层依赖具体 driver。
type SQLExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) SQLRow
}

// SQLRow 表示 SQL 单行读取结果的最小抽象。
type SQLRow interface {
	Scan(dest ...any) error
}

// MySQLRoomSummaryRepository 是 room_summary 表的 MySQL repository adapter。
type MySQLRoomSummaryRepository struct {
	exec SQLExecutor
}

// NewMySQLRoomSummaryRepository 创建 MySQL 房间摘要 repository。
func NewMySQLRoomSummaryRepository(exec SQLExecutor) (*MySQLRoomSummaryRepository, error) {
	if exec == nil {
		return nil, ErrInvalidArgument
	}
	return &MySQLRoomSummaryRepository{exec: exec}, nil
}

// SaveRoomSummary 校验并幂等写入房间摘要。
func (r *MySQLRoomSummaryRepository) SaveRoomSummary(ctx context.Context, summary RoomSummary) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRoomSummary(summary); err != nil {
		return err
	}
	return fmt.Errorf("%w: mysql room summary adapter is not wired to concrete SQL statements yet", ErrUnavailable)
}

// GetRoomSummary 校验并读取房间摘要。
func (r *MySQLRoomSummaryRepository) GetRoomSummary(ctx context.Context, roomID string) (RoomSummary, error) {
	if err := ctx.Err(); err != nil {
		return RoomSummary{}, err
	}
	if err := validateID(roomID); err != nil {
		return RoomSummary{}, err
	}
	return RoomSummary{}, fmt.Errorf("%w: mysql room summary adapter is not wired to concrete SQL statements yet", ErrUnavailable)
}

// MySQLPlayerProfileRepository 是 account_player 表的 MySQL repository adapter。
type MySQLPlayerProfileRepository struct {
	exec SQLExecutor
}

// NewMySQLPlayerProfileRepository 创建 MySQL 玩家账号资料 repository。
func NewMySQLPlayerProfileRepository(exec SQLExecutor) (*MySQLPlayerProfileRepository, error) {
	if exec == nil {
		return nil, ErrInvalidArgument
	}
	return &MySQLPlayerProfileRepository{exec: exec}, nil
}

// CreatePlayerProfile 写入第一阶段账号资料；账号名唯一冲突会映射为 ErrConflict。
func (r *MySQLPlayerProfileRepository) CreatePlayerProfile(ctx context.Context, profile PlayerProfile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validatePlayerProfile(profile); err != nil {
		return err
	}
	_, err := r.exec.ExecContext(
		ctx,
		`INSERT INTO account_player
			(player_id, account_name, password_hash, display_name, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(profile.PlayerID),
		strings.TrimSpace(profile.AccountName),
		profile.PasswordHash,
		profile.DisplayName,
		timeToUnixMilli(profile.CreatedAt),
		timeToUnixMilli(profile.UpdatedAt),
	)
	if err != nil {
		if isMySQLDuplicateEntry(err) {
			return ErrConflict
		}
		return fmt.Errorf("%w: create player profile: %v", ErrUnavailable, err)
	}
	return nil
}

// GetPlayerProfileByAccount 根据账号名读取玩家资料。
func (r *MySQLPlayerProfileRepository) GetPlayerProfileByAccount(ctx context.Context, accountName string) (PlayerProfile, error) {
	if err := ctx.Err(); err != nil {
		return PlayerProfile{}, err
	}
	accountName = strings.TrimSpace(accountName)
	if accountName == "" {
		return PlayerProfile{}, ErrInvalidArgument
	}
	return r.scanPlayerProfile(r.exec.QueryRowContext(
		ctx,
		`SELECT player_id, account_name, password_hash, display_name, created_at_ms, updated_at_ms
		FROM account_player
		WHERE account_name = ?`,
		accountName,
	))
}

// GetPlayerProfileByID 根据稳定 player id 读取玩家资料。
func (r *MySQLPlayerProfileRepository) GetPlayerProfileByID(ctx context.Context, playerID string) (PlayerProfile, error) {
	if err := ctx.Err(); err != nil {
		return PlayerProfile{}, err
	}
	if err := validateID(playerID); err != nil {
		return PlayerProfile{}, err
	}
	return r.scanPlayerProfile(r.exec.QueryRowContext(
		ctx,
		`SELECT player_id, account_name, password_hash, display_name, created_at_ms, updated_at_ms
		FROM account_player
		WHERE player_id = ?`,
		strings.TrimSpace(playerID),
	))
}

func (r *MySQLPlayerProfileRepository) scanPlayerProfile(row SQLRow) (PlayerProfile, error) {
	var profile PlayerProfile
	var createdAtMs int64
	var updatedAtMs int64
	if err := row.Scan(
		&profile.PlayerID,
		&profile.AccountName,
		&profile.PasswordHash,
		&profile.DisplayName,
		&createdAtMs,
		&updatedAtMs,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PlayerProfile{}, ErrNotFound
		}
		return PlayerProfile{}, fmt.Errorf("%w: scan player profile: %v", ErrUnavailable, err)
	}
	profile.CreatedAt = time.UnixMilli(createdAtMs)
	profile.UpdatedAt = time.UnixMilli(updatedAtMs)
	return profile, nil
}

func isMySQLDuplicateEntry(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

func timeToUnixMilli(value time.Time) int64 {
	if value.IsZero() {
		return time.Now().UnixMilli()
	}
	return value.UnixMilli()
}
