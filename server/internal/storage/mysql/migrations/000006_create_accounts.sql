CREATE TABLE accounts (
  account_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '账号ID',
  player_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '玩家ID',
  username VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '登录名',
  display_name VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL COMMENT '展示名称',
  credential_hash VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '凭据哈希(PHC)',
  status ENUM('active', 'inactive') NOT NULL COMMENT '账号状态',
  created_at DATETIME(6) NOT NULL COMMENT '创建时间(UTC,微秒)',
  PRIMARY KEY (account_id),
  CONSTRAINT uq_accounts_player UNIQUE (player_id),
  CONSTRAINT uq_accounts_username UNIQUE (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs COMMENT='账号(owner=storage/account)';
