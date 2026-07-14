CREATE TABLE personal_world_idempotency (
  actor_player_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '操作玩家ID',
  idempotency_key_digest BINARY(32) NOT NULL COMMENT '幂等键摘要(SHA-256,32字节)',
  operation VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '操作类型',
  command_fingerprint BINARY(32) NOT NULL COMMENT '命令指纹(SHA-256,32字节)',
  result_world_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '结果个人世界ID',
  result_owner_player_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '结果所属玩家ID',
  result_lifecycle ENUM('active', 'archived') NOT NULL COMMENT '结果生命周期',
  result_revision BIGINT UNSIGNED NOT NULL COMMENT '结果修订号',
  result_created_at DATETIME(6) NOT NULL COMMENT '结果创建时间(UTC,微秒)',
  committed_at DATETIME(6) NOT NULL COMMENT '提交时间(UTC,微秒)',
  PRIMARY KEY (actor_player_id, idempotency_key_digest),
  CONSTRAINT chk_personal_world_idempotency_revision CHECK (result_revision > 0)
) ENGINE=InnoDB COMMENT='个人世界幂等结果(owner=storage/personalworld)';
