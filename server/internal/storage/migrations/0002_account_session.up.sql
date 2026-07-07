-- owner: account
-- purpose: 第一阶段账号注册、登录和会话恢复所需的最小玩家资料与密码哈希。
-- compatibility: 新增表，不修改 0001 占位表；后续账号安全能力必须通过独立迁移演进。

CREATE TABLE IF NOT EXISTS account_player (
    player_id VARCHAR(64) PRIMARY KEY COMMENT '玩家ID',
    account_name VARCHAR(64) NOT NULL COMMENT '账号名',
    password_hash VARCHAR(128) NOT NULL COMMENT '密码哈希',
    display_name VARCHAR(64) NOT NULL DEFAULT '' COMMENT '展示名',
    created_at_ms BIGINT NOT NULL COMMENT '创建时间(ms)',
    updated_at_ms BIGINT NOT NULL COMMENT '更新时间(ms)',
    UNIQUE KEY uk_account_player_account_name (account_name)
) COMMENT='玩家账号表';
