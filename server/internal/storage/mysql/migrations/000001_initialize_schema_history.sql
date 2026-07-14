-- storage/mysql 拥有该 metadata table；本 migration 固定其中文 schema 注释。
ALTER TABLE ih_schema_migrations
  MODIFY COLUMN version BIGINT UNSIGNED NOT NULL COMMENT '迁移版本号',
  MODIFY COLUMN name VARCHAR(128) NOT NULL COMMENT '迁移名称',
  MODIFY COLUMN checksum BINARY(32) NOT NULL COMMENT '迁移校验值(SHA-256,32字节)',
  MODIFY COLUMN state ENUM('in_progress', 'applied') NOT NULL COMMENT '迁移状态',
  MODIFY COLUMN started_at TIMESTAMP(6) NOT NULL COMMENT '开始时间(UTC,微秒)',
  MODIFY COLUMN applied_at TIMESTAMP(6) NULL COMMENT '应用时间(UTC,微秒)',
  COMMENT = '迁移历史(owner=storage/mysql)';
