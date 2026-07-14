CREATE TABLE personal_worlds (
  personal_world_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '个人世界ID',
  owner_player_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '所属玩家ID',
  lifecycle ENUM('active', 'archived') NOT NULL COMMENT '生命周期',
  revision BIGINT UNSIGNED NOT NULL COMMENT '修订号',
  created_at DATETIME(6) NOT NULL COMMENT '创建时间(UTC,微秒)',
  PRIMARY KEY (personal_world_id),
  CONSTRAINT uq_personal_worlds_owner UNIQUE (owner_player_id),
  CONSTRAINT chk_personal_worlds_revision CHECK (revision > 0)
) ENGINE=InnoDB COMMENT='个人世界(owner=storage/personalworld)';
