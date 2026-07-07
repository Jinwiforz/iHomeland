-- owner: storage
-- purpose: 第一阶段房间大厅最小持久事实。Redis 丢失后，房间摘要和后续对局摘要从 MySQL 恢复。
-- compatibility: 新增表，不修改既有字段；后续字段变更必须采用先兼容读写、再收敛语义的迁移策略。

CREATE TABLE IF NOT EXISTS player_profile_stub (
    player_id VARCHAR(64) PRIMARY KEY COMMENT '玩家ID',
    display_name VARCHAR(64) NOT NULL DEFAULT '' COMMENT '展示名',
    created_at_ms BIGINT NOT NULL COMMENT '创建时间(ms)',
    updated_at_ms BIGINT NOT NULL COMMENT '更新时间(ms)'
) COMMENT='玩家资料占位表';

CREATE TABLE IF NOT EXISTS room_summary (
    room_id VARCHAR(64) PRIMARY KEY COMMENT '房间ID',
    room_name VARCHAR(128) NOT NULL COMMENT '房间名称',
    host_player_id VARCHAR(64) NOT NULL COMMENT '房主玩家ID',
    state VARCHAR(32) NOT NULL COMMENT '房间状态',
    capacity INT NOT NULL COMMENT '容量',
    member_count INT NOT NULL COMMENT '成员数',
    idempotency_key VARCHAR(128) NOT NULL COMMENT '幂等键',
    created_at_ms BIGINT NOT NULL COMMENT '创建时间(ms)',
    updated_at_ms BIGINT NOT NULL COMMENT '更新时间(ms)',
    closed_at_ms BIGINT NULL COMMENT '关闭时间(ms)',
    UNIQUE KEY uk_room_summary_idempotency_key (idempotency_key),
    KEY idx_room_summary_state_updated (state, updated_at_ms)
) COMMENT='房间摘要表';

CREATE TABLE IF NOT EXISTS match_summary_stub (
    match_id VARCHAR(64) PRIMARY KEY COMMENT '对局ID',
    room_id VARCHAR(64) NOT NULL COMMENT '房间ID',
    state VARCHAR(32) NOT NULL COMMENT '对局状态',
    idempotency_key VARCHAR(128) NOT NULL COMMENT '幂等键',
    created_at_ms BIGINT NOT NULL COMMENT '创建时间(ms)',
    updated_at_ms BIGINT NOT NULL COMMENT '更新时间(ms)',
    UNIQUE KEY uk_match_summary_idempotency_key (idempotency_key),
    KEY idx_match_summary_room_id (room_id)
) COMMENT='对局摘要占位表';
