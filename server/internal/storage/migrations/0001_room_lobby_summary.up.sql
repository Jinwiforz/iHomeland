-- owner: storage
-- purpose: 第一阶段房间大厅最小持久事实。Redis 丢失后，房间摘要和后续对局摘要从 MySQL 恢复。
-- compatibility: 新增表，不修改既有字段；后续字段变更必须采用先兼容读写、再收敛语义的迁移策略。

CREATE TABLE IF NOT EXISTS player_profile_stub (
    player_id VARCHAR(64) PRIMARY KEY,
    display_name VARCHAR(64) NOT NULL DEFAULT '',
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS room_summary (
    room_id VARCHAR(64) PRIMARY KEY,
    room_name VARCHAR(128) NOT NULL,
    host_player_id VARCHAR(64) NOT NULL,
    state VARCHAR(32) NOT NULL,
    capacity INT NOT NULL,
    member_count INT NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    closed_at_ms BIGINT NULL,
    UNIQUE KEY uk_room_summary_idempotency_key (idempotency_key),
    KEY idx_room_summary_state_updated (state, updated_at_ms)
);

CREATE TABLE IF NOT EXISTS match_summary_stub (
    match_id VARCHAR(64) PRIMARY KEY,
    room_id VARCHAR(64) NOT NULL,
    state VARCHAR(32) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    UNIQUE KEY uk_match_summary_idempotency_key (idempotency_key),
    KEY idx_match_summary_room_id (room_id)
);
