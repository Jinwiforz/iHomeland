## Context

服务端当前已经完成 WebSocket 网关和自定义房间大厅，但 room service 仍使用单进程内存 repository。第一阶段已经有本地 MySQL/Redis 基础设施配置与 `/readyz` 探测能力；现在需要把“哪些数据进入 MySQL、哪些运行态进入 Redis、业务代码如何依赖这些边界”设计清楚，并实现可测试的最小 storage adapter。

这个 change 的目标不是把所有游戏数据持久化，而是为房间大厅建立可恢复边界：MySQL 保存持久事实或摘要，Redis 保存短期运行态，room 业务只依赖接口。

## Goals / Non-Goals

**Goals:**

- 在 `server/internal/storage` 中定义第一阶段 repository/cache 接口和错误语义。
- 定义 MySQL 最小 schema 与迁移入口，覆盖玩家基础资料占位、房间摘要和后续对局摘要预留。
- 定义 Redis key、TTL、owner、value 结构、重建来源和清理策略，覆盖 session、presence、room index、reconnect token、lock/rate limit 命名空间。
- 为 room service 引入 persistence/cache 边界，但保持状态机可在无网络环境下用 fake adapter 测试。
- 补充本地验证和测试：单元测试使用 fake/in-memory adapter，集成检查只验证本地 MySQL/Redis 连接与基础读写能力。

**Non-Goals:**

- 不实现完整账号、登录、鉴权、背包、经济、战绩或排行榜系统。
- 不把 Redis 作为玩家进度、房间摘要或对局摘要的唯一事实来源。
- 不引入 gRPC 服务拆分，不实现跨服或 battle server 持久化。
- 不要求房间大厅在本 change 内具备生产级多进程一致性；只定义第一阶段可恢复路径和边界。

## Decisions

### 1. MySQL 保存持久事实，Redis 保存短期运行态

MySQL schema 负责跨进程、跨 Redis 丢失后仍需存在的数据。第一阶段只建立最小表：玩家基础资料占位、房间摘要、后续对局摘要预留。Redis 只保存 session、presence、room index、reconnect token、lock 和 rate limit 等短期状态。

替代方案是继续只用内存 repository。该方案无法验证重启恢复、Redis 丢失恢复和 schema 迁移边界。

### 2. Room 只依赖接口，不直接依赖 Redis/MySQL 客户端

`room` 包继续保持状态机纯净。持久化通过 `storage` 中的 repository/cache interface 接入，例如 room summary repository、presence cache、reconnect token cache 和 room index cache。测试中可以用 fake adapter。

替代方案是在 room service 中直接调用具体 Redis/MySQL client。该方案会让业务规则难以在无网络环境下测试，也会扩大状态机的失败面。

### 3. Redis key 必须先文档化再实现

每个 Redis key 必须记录 owner、用途、TTL、value、重建来源和清理触发条件。建议第一阶段 key：

- `ih:{env}:session:connection:{connectionID}`
- `ih:{env}:presence:player:{playerID}`
- `ih:{env}:room:index:{roomID}`
- `ih:{env}:room:reconnect:{roomID}:{playerID}`
- `ih:{env}:lock:room:{roomID}`
- `ih:{env}:rate:gateway:{identity}`

### 4. 迁移文件归属 `server/internal/storage/migrations`

MySQL schema 变更必须以迁移文件表达，并包含 up/down 或等价回滚说明。迁移命名使用递增序号，例如 `0001_room_lobby_summary.up.sql`。服务端业务代码不得手工创建表。

### 5. 当前实现优先 adapter 和验证，不追求完整生产 HA

这个 change 可以实现本地 Redis/MySQL adapter、接口、fake adapter 和验证脚本/测试，但不需要做分布式锁完整公平性、多进程房间迁移或跨服路由。

## Risks / Trade-offs

- [Risk] 过早设计完整账号或经济 schema。→ 只保留第一阶段必要字段和预留摘要，不引入完整账号/经济业务。
- [Risk] Redis key 过期导致运行态丢失。→ 每个 key 必须有 TTL 和重建来源；持久事实从 MySQL 恢复。
- [Risk] MySQL 迁移一旦发布难以回滚。→ 每个迁移必须有兼容策略和回滚说明，避免破坏性字段语义变更。
- [Risk] room service 接入 storage 后测试变慢。→ 状态机测试继续使用 fake/in-memory adapter，真实依赖只用于明确的集成验证。

## Migration Plan

1. 新增 storage 接口、错误类型、fake adapter 和基础测试。
2. 新增 MySQL migration 目录和第一阶段最小 schema。
3. 新增 Redis key 文档条目并实现 key builder/TTL 常量。
4. 将 room service 的运行态保存点接到 storage 边界，但保留内存实现作为默认本地路径或测试路径。
5. 更新验证脚本或测试说明，确认本地 MySQL/Redis 连接可用并能执行基础读写检查。

## Open Questions

- 第一阶段是否默认启动真实 Redis/MySQL adapter，还是继续默认 in-memory、通过配置打开真实 adapter，需要实现阶段结合本地开发体验决定。
- 房间摘要的保留时间和最终字段是否足够支撑后续客户端房间列表，需要在 `document-client-integration` 或后续 room list change 中再细化。
