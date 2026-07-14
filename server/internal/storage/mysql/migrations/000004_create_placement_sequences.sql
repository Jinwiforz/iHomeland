CREATE TABLE placement_sequences (
  personal_world_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '个人世界ID',
  generation_high BIGINT UNSIGNED NOT NULL COMMENT '实例代际高水位',
  fencing_high BIGINT UNSIGNED NOT NULL COMMENT '围栏令牌高水位',
  updated_at DATETIME(6) NOT NULL COMMENT '更新时间(UTC,微秒)',
  PRIMARY KEY (personal_world_id),
  CONSTRAINT fk_placement_sequences_world FOREIGN KEY (personal_world_id) REFERENCES personal_worlds (personal_world_id),
  CONSTRAINT chk_placement_sequences_generation CHECK (generation_high > 0),
  CONSTRAINT chk_placement_sequences_fence CHECK (fencing_high > 0)
) ENGINE=InnoDB COMMENT='实例放置序列(owner=storage/placement)';
