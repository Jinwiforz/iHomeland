CREATE TABLE simulation_result_receipts (
  result_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '模拟结果ID',
  proposal_fingerprint BINARY(32) NOT NULL COMMENT '提案指纹(SHA-256,32字节)',
  assignment_fingerprint BINARY(32) NOT NULL COMMENT '放置指纹(SHA-256,32字节)',
  simulation_instance_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '模拟实例ID',
  result_kind VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '模拟结果类型',
  tick_start BIGINT UNSIGNED NOT NULL COMMENT '摘要起始Tick',
  tick_end BIGINT UNSIGNED NOT NULL COMMENT '摘要结束Tick',
  payload_digest BINARY(32) NOT NULL COMMENT '低敏载荷摘要(SHA-256,32字节)',
  evidence_digest BINARY(32) NOT NULL COMMENT '回放证据摘要(SHA-256,32字节)',
  disposition ENUM('committed', 'rejected') NOT NULL COMMENT '持久裁决结果',
  reason VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '稳定裁决原因',
  decided_at DATETIME(6) NOT NULL COMMENT '裁决时间(UTC,微秒)',
  PRIMARY KEY (result_id),
  KEY idx_simulation_result_assignment (assignment_fingerprint, simulation_instance_id),
  CONSTRAINT chk_simulation_result_tick_range CHECK (tick_start <= tick_end)
) ENGINE=InnoDB COMMENT='模拟结果不可变回执(owner=storage/simulationresult)';
