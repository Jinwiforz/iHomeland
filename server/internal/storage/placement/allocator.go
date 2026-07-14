package placement

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"hash"
	"math"
	"time"

	domain "github.com/jinwiforz/ihomeland/server/internal/placement"
	storagemysql "github.com/jinwiforz/ihomeland/server/internal/storage/mysql"
)

// allocationChecksumDomain 隔离 allocation checksum 与其他 SHA-256 投影，版本变化必须显式迁移历史记录。
const allocationChecksumDomain = "placement.allocation.v1"

// allocation 保存一次不可删除、不可复用的 MySQL reservation 投影。
type allocation struct {
	// snapshot 是 allocation 分配 generation/fence 后形成的 starting assignment。
	snapshot domain.AssignmentSnapshot
	// checksum 绑定 world、instance、node 与 candidate 时间，防止相同 ID 异义重用。
	checksum [sha256.Size]byte
}

// allocator 借用共享 MySQL pool 分配 world-scoped 单调 generation/fence。
type allocator struct {
	// db 由 storage/mysql Component 持有。
	db *sql.DB
	// withinTx 固定 production runner；测试只用它注入 commit-unknown，不重放 callback。
	withinTx func(context.Context, *sql.DB, *sql.TxOptions, func(*sql.Tx) error) error
}

// allocationQueryer 是 *sql.DB 与 *sql.Tx 共享的单行 allocation 读取边界。
type allocationQueryer interface {
	// QueryRowContext 执行固定查询；调用者必须立即 Scan，不能保存返回 row。
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// allocationStatus 表达 reservation 的确定性与提交边界。
type allocationStatus uint8

const (
	// allocationNotCommitted 表示没有 reservation 可安全发布。
	allocationNotCommitted allocationStatus = iota
	// allocationCreated 表示本次 transaction 新建 reservation，允许首次发布。
	allocationCreated
	// allocationExisting 表示相同 candidate 已有 reservation，只允许解析，禁止再次发布。
	allocationExisting
	// allocationCommitUnknown 表示 MySQL commit 可能成功，禁止尝试 Redis transition。
	allocationCommitUnknown
)

// reserve 为 candidate 追加 allocation；相同 WorldInstanceID 只能恢复完全一致的旧值。
//
// 新 reservation 与 high-watermark 在同一 transaction 提交。Redis 后续结果永远不会删除
// allocation；commit-unknown 时返回零值，调用方只能用相同 candidate 重新解析 MySQL。
func (value *allocator) reserve(ctx context.Context, candidate domain.AssignmentCandidate, observedAt time.Time) (allocation, allocationStatus, error) {
	if value == nil || value.db == nil || !candidate.Valid() || observedAt.IsZero() {
		return allocation{}, allocationNotCommitted, errors.New("placement allocation input is invalid")
	}
	checksum := checksumCandidate(candidate)
	reserved := allocation{}
	status := allocationNotCommitted
	runner := value.withinTx
	if runner == nil {
		runner = storagemysql.WithinTx
	}
	err := runner(ctx, value.db, &sql.TxOptions{Isolation: sql.LevelSerializable}, func(tx *sql.Tx) error {
		existing, findErr := selectAllocation(ctx, tx, candidate.InstanceID(), observedAt, true)
		if findErr == nil {
			if existing.checksum != checksum || !allocationMatchesCandidate(existing, candidate) {
				return errors.New("world instance allocation conflicts with persisted candidate")
			}
			reserved = existing
			status = allocationExisting
			return nil
		}
		if !errors.Is(findErr, sql.ErrNoRows) {
			return findErr
		}

		generationValue, fenceValue, nextErr := nextSequence(ctx, tx, candidate)
		if nextErr != nil {
			return nextErr
		}
		generation, generationErr := domain.NewAssignmentGeneration(generationValue)
		if generationErr != nil {
			return generationErr
		}
		fence, fenceErr := domain.NewFencingToken(fenceValue)
		if fenceErr != nil {
			return fenceErr
		}
		snapshot, snapshotErr := candidate.Snapshot(generation, fence, observedAt)
		if snapshotErr != nil {
			return snapshotErr
		}
		_, insertErr := tx.ExecContext(ctx, `INSERT INTO placement_allocations
			(world_instance_id, personal_world_id, runtime_node_id, generation, fencing_token,
			 candidate_created_at, candidate_lease_expires_at, candidate_checksum, allocated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(6))`,
			candidate.InstanceID().String(), candidate.WorldID().String(), candidate.NodeID().String(),
			generation.Uint64(), fence.Uint64(), candidate.CreatedAt(), candidate.LeaseExpiresAt(), checksum[:])
		if insertErr != nil {
			return insertErr
		}
		reserved = allocation{snapshot: snapshot, checksum: checksum}
		status = allocationCreated
		return nil
	})
	if err != nil {
		var transactionError *storagemysql.TransactionError
		if errors.As(err, &transactionError) && transactionError.Outcome == storagemysql.TransactionCommitUnknown {
			return allocation{}, allocationCommitUnknown, err
		}
		return allocation{}, allocationNotCommitted, err
	}
	if !reserved.snapshot.Valid() || status != allocationCreated && status != allocationExisting {
		return allocation{}, allocationNotCommitted, errors.New("placement allocation result is inconsistent")
	}
	return reserved, status, nil
}

// lookup 只解析已提交 allocation，不创建 sequence 或 reservation。
//
// Replace 在 current 已是同一 successor 时使用该路径确认 MySQL 证据，避免把响应丢失重试
// 当作 stale conflict，也避免在 Redis/MySQL 矛盾时写入新的修复性 allocation。
func (value *allocator) lookup(ctx context.Context, candidate domain.AssignmentCandidate, observedAt time.Time) (allocation, bool, error) {
	if value == nil || value.db == nil || !candidate.Valid() || observedAt.IsZero() {
		return allocation{}, false, errors.New("placement allocation lookup input is invalid")
	}
	existing, err := selectAllocation(ctx, value.db, candidate.InstanceID(), observedAt, false)
	if errors.Is(err, sql.ErrNoRows) {
		return allocation{}, false, nil
	}
	if err != nil {
		return allocation{}, false, err
	}
	if existing.checksum != checksumCandidate(candidate) || !allocationMatchesCandidate(existing, candidate) {
		return allocation{}, false, errors.New("world instance allocation conflicts with persisted candidate identity")
	}
	return existing, true, nil
}

// nextSequence 插入首个 high-watermark 或锁定并同时推进 generation/fence。
func nextSequence(ctx context.Context, tx *sql.Tx, candidate domain.AssignmentCandidate) (uint64, uint64, error) {
	insert, err := tx.ExecContext(ctx, `INSERT IGNORE INTO placement_sequences
		(personal_world_id, generation_high, fencing_high, updated_at)
		VALUES (?, 1, 1, UTC_TIMESTAMP(6))`, candidate.WorldID().String())
	if err != nil {
		return 0, 0, err
	}
	affected, err := insert.RowsAffected()
	if err != nil {
		return 0, 0, err
	}
	if affected == 1 {
		return 1, 1, nil
	}
	if affected != 0 {
		return 0, 0, errors.New("placement sequence insert affected unexpected rows")
	}
	var generationHigh uint64
	var fencingHigh uint64
	if err := tx.QueryRowContext(ctx, `SELECT generation_high, fencing_high
		FROM placement_sequences WHERE personal_world_id = ? FOR UPDATE`, candidate.WorldID().String()).Scan(&generationHigh, &fencingHigh); err != nil {
		return 0, 0, err
	}
	if generationHigh == 0 || fencingHigh == 0 || generationHigh == math.MaxUint64 || fencingHigh == math.MaxUint64 {
		return 0, 0, errors.New("placement sequence is invalid or exhausted")
	}
	nextGeneration := generationHigh + 1
	nextFence := fencingHigh + 1
	update, err := tx.ExecContext(ctx, `UPDATE placement_sequences
		SET generation_high = ?, fencing_high = ?, updated_at = UTC_TIMESTAMP(6)
		WHERE personal_world_id = ? AND generation_high = ? AND fencing_high = ?`,
		nextGeneration, nextFence, candidate.WorldID().String(), generationHigh, fencingHigh)
	if err != nil {
		return 0, 0, err
	}
	updated, err := update.RowsAffected()
	if err != nil || updated != 1 {
		return 0, 0, errors.New("placement sequence update affected unexpected rows")
	}
	return nextGeneration, nextFence, nil
}

// selectAllocation 读取 WorldInstanceID 的不可变 reservation；reserve 路径可要求 row lock。
func selectAllocation(ctx context.Context, queryer allocationQueryer, instanceID domain.WorldInstanceID, observedAt time.Time, lock bool) (allocation, error) {
	var worldValue string
	var instanceValue string
	var nodeValue string
	var generationValue uint64
	var fenceValue uint64
	var createdAt time.Time
	var expiresAt time.Time
	var checksumBytes []byte
	query := `SELECT personal_world_id, world_instance_id, runtime_node_id,
		generation, fencing_token, candidate_created_at, candidate_lease_expires_at, candidate_checksum
		FROM placement_allocations WHERE world_instance_id = ?`
	if lock {
		query += ` FOR UPDATE`
	}
	err := queryer.QueryRowContext(ctx, query, instanceID.String()).Scan(
		&worldValue, &instanceValue, &nodeValue, &generationValue, &fenceValue, &createdAt, &expiresAt, &checksumBytes,
	)
	if err != nil {
		return allocation{}, err
	}
	if len(checksumBytes) != sha256.Size {
		return allocation{}, errors.New("persisted allocation checksum is malformed")
	}
	snapshot, err := hydrateAllocation(worldValue, instanceValue, nodeValue, generationValue, fenceValue, createdAt, expiresAt, observedAt)
	if err != nil {
		return allocation{}, err
	}
	var checksum [sha256.Size]byte
	copy(checksum[:], checksumBytes)
	return allocation{snapshot: snapshot, checksum: checksum}, nil
}

// hydrateAllocation 通过 domain constructors 恢复持久 allocation，不修复非法值。
func hydrateAllocation(worldValue string, instanceValue string, nodeValue string, generationValue uint64, fenceValue uint64, createdAt time.Time, expiresAt time.Time, observedAt time.Time) (domain.AssignmentSnapshot, error) {
	worldID, err := newPersonalWorldID(worldValue)
	if err != nil {
		return domain.AssignmentSnapshot{}, err
	}
	instanceID, err := domain.NewWorldInstanceID(instanceValue)
	if err != nil {
		return domain.AssignmentSnapshot{}, err
	}
	nodeID, err := domain.NewRuntimeNodeID(nodeValue)
	if err != nil {
		return domain.AssignmentSnapshot{}, err
	}
	generation, err := domain.NewAssignmentGeneration(generationValue)
	if err != nil {
		return domain.AssignmentSnapshot{}, err
	}
	fence, err := domain.NewFencingToken(fenceValue)
	if err != nil {
		return domain.AssignmentSnapshot{}, err
	}
	stamp, err := domain.NewAssignmentStamp(worldID, instanceID, nodeID, generation, fence)
	if err != nil {
		return domain.AssignmentSnapshot{}, err
	}
	return domain.NewAssignmentSnapshot(stamp, domain.PhaseStarting, createdAt, expiresAt, observedAt)
}

// allocationMatchesCandidate 只比较跨重试稳定的 world/instance/node identity。
//
// createdAt、initial expiry 与调用时钟由首次 allocation 固化并作为结果恢复；重试时重新读取
// 当前时间不得把同一 successor 误判为新的 candidate。
func allocationMatchesCandidate(value allocation, candidate domain.AssignmentCandidate) bool {
	return snapshotMatchesCandidateIdentity(value.snapshot, candidate)
}

// snapshotMatchesCandidateIdentity 比较 WorldInstance 重试唯一允许复用的稳定 identity。
func snapshotMatchesCandidateIdentity(snapshot domain.AssignmentSnapshot, candidate domain.AssignmentCandidate) bool {
	return snapshot.Valid() && candidate.Valid() && snapshot.WorldID() == candidate.WorldID() &&
		snapshot.InstanceID() == candidate.InstanceID() && snapshot.NodeID() == candidate.NodeID()
}

// checksumCandidate 对跨重试稳定的 candidate identity 做长度前缀 canonical encoding。
func checksumCandidate(candidate domain.AssignmentCandidate) [sha256.Size]byte {
	hasher := sha256.New()
	writeChecksumString(hasher, allocationChecksumDomain)
	writeChecksumString(hasher, candidate.WorldID().String())
	writeChecksumString(hasher, candidate.InstanceID().String())
	writeChecksumString(hasher, candidate.NodeID().String())
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return digest
}

// writeChecksumString 使用长度前缀避免 allocation 字段拼接歧义。
func writeChecksumString(hasher hash.Hash, value string) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	_, _ = hasher.Write(length[:])
	_, _ = hasher.Write([]byte(value))
}
