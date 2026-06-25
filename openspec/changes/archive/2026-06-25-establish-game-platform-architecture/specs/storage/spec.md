## ADDED Requirements

### Requirement: MySQL 是持久事实来源
需要跨进程、跨 Redis 丢失后仍然存在的数据必须存储在 MySQL 或后续明确的持久化系统中。

#### Scenario: Redis 数据丢失
- **WHEN** Redis 中的运行态数据被清空
- **THEN** 玩家进度、房间摘要和对局摘要等持久数据仍可从 MySQL 恢复

### Requirement: Redis 只保存短期运行态数据
Redis 必须用于 session、presence、room index、reconnect token、queue、lock、rate limit 等短期运行态数据。

#### Scenario: 新增 Redis key
- **WHEN** 新增 Redis key
- **THEN** 文档必须记录 owner、用途、TTL 或重建路径

### Requirement: 可重试写入必须幂等
可能被重试的持久化写入必须具备幂等保护。

#### Scenario: 对局摘要重复提交
- **WHEN** 对局摘要提交因瞬时错误被重试
- **THEN** 存储层只产生一份有效记录
