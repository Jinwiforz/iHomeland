package storage

import (
	"context"
	"fmt"
)

// SQLExecutor 是 MySQL adapter 需要的最小执行能力，避免业务层依赖具体 driver。
type SQLExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (SQLResult, error)
	QueryRowContext(ctx context.Context, query string, args ...any) SQLRow
}

// SQLResult 表示 SQL 写入结果的最小抽象。
type SQLResult interface {
	RowsAffected() (int64, error)
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
