## Why

房间大厅已经具备进程内内存实现，但断线重连、房间索引和房间摘要仍会随进程重启丢失。现在需要为第一阶段定义清晰的 Redis/MySQL 持久化与运行态边界，让后续实现可以在不扩大到完整账号、经济或战绩系统的前提下接入可恢复的数据路径。

## What Changes

- 定义第一阶段房间大厅需要的 storage adapter 接口，隔离 room service 与具体 Redis/MySQL 客户端。
- 定义 MySQL 作为持久事实来源的最小数据边界，例如玩家基础资料占位、房间摘要和后续对局摘要预留。
- 定义 Redis 作为短期运行态数据的边界，例如 session、presence、room index、reconnect token 和必要 lock/rate limit 命名空间。
- 明确 Redis key 的 owner、用途、TTL、value 形态、重建来源和清理触发条件。
- 明确 MySQL schema 变更需要迁移文件、回滚策略和兼容读写策略。
- 补充本地验证与测试边界：可用 fake/in-memory adapter 测业务逻辑，用集成测试或脚本验证本地 Redis/MySQL 连接。
- 本 change 不实现完整账号系统、背包、经济、战绩、匹配队列、battle server，也不把 Redis 作为持久事实来源。

## Capabilities

### New Capabilities

- 无。

### Modified Capabilities

- `storage`: 细化第一阶段 Redis/MySQL 边界、schema 迁移、Redis key 规范、repository/cache adapter、幂等和恢复策略。

## Impact

- 影响 `server/internal/storage`，需要新增接口、adapter 和测试。
- 可能影响 `server/internal/room` 的 repository/cache 边界，但 room 状态机不应直接依赖 Redis/MySQL 客户端。
- 影响 `docs/redis-keys.md`、`docs/architecture.md`、`docs/file-structure.md` 或服务端 README 中与持久化边界相关的说明。
- 可能新增 MySQL migration 目录和最小 schema 文件；必须记录兼容策略。
