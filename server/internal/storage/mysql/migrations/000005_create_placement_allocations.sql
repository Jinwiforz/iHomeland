CREATE TABLE placement_allocations (
  world_instance_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '世界实例ID',
  personal_world_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '个人世界ID',
  runtime_node_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '运行节点ID',
  generation BIGINT UNSIGNED NOT NULL COMMENT '实例代际',
  fencing_token BIGINT UNSIGNED NOT NULL COMMENT '围栏令牌',
  candidate_created_at DATETIME(6) NOT NULL COMMENT '候选创建时间(UTC,微秒)',
  candidate_lease_expires_at DATETIME(6) NOT NULL COMMENT '候选租约到期时间(UTC,微秒)',
  candidate_checksum BINARY(32) NOT NULL COMMENT '候选身份校验值(SHA-256,32字节)',
  allocated_at DATETIME(6) NOT NULL COMMENT '分配时间(UTC,微秒)',
  PRIMARY KEY (world_instance_id),
  CONSTRAINT uq_placement_allocations_generation UNIQUE (personal_world_id, generation),
  CONSTRAINT uq_placement_allocations_fence UNIQUE (personal_world_id, fencing_token),
  CONSTRAINT fk_placement_allocations_world FOREIGN KEY (personal_world_id) REFERENCES personal_worlds (personal_world_id),
  CONSTRAINT chk_placement_allocations_generation CHECK (generation > 0),
  CONSTRAINT chk_placement_allocations_fence CHECK (fencing_token > 0),
  CONSTRAINT chk_placement_allocations_lease CHECK (candidate_lease_expires_at > candidate_created_at)
) ENGINE=InnoDB COMMENT='实例放置分配(owner=storage/placement)';
