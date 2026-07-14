## Why

`VisitSession` 已具备完整的领域状态机和原子 `VisitSessionStore` 契约，但生产代码仍只有测试 reference store；公开 HTTP 若在此时实现 invite accept 或 world admission，会被迫注入 fake、memory adapter，或在 handler change 内顺带发明未经独立验收的 Redis 一致性语义。现在需要先补齐 VisitSession 的可恢复运行态存储，使 invite、membership、revision、replay 与 safe-return 结果能够在真实 Redis、并发和故障条件下 fail closed。

## What Changes

- 新增 VisitSession owner 的 production Redis adapter，实现 active-world 唯一索引、完整 snapshot、command replay/result 与原子 create/CAS transition。
- 为所有已实现 VisitSession key/value 登记 owner、schema version、大小预算、TTL、恢复、清理、故障行为、低基数 metrics，并在 Redis 字段字典中维护中文短注释和时间单位。
- 明确 session absolute expiry、replay retention、terminal snapshot retention 与 active index 清理关系；Redis flush、过期、损坏或未知 schema 时不恢复旧访问资格、不伪造提交结果。
- Adapter 接受调用方注入的 Redis client、共享 keyspace、clock 与观测契约；未来接线时必须复用 Composition Root 持有的共享实例。Definitions 与 adapter 纳入 storage integration harness；本 change 不修改正式 service graph，不构造 cleanup goroutine、admission credential 或公开 listener。
- 通过真实 Docker Redis、并发、race、response-loss、restart/flush/corruption 测试验证 adapter 负责的 `VisitSessionStore` outcome、完整 replay 和边界不变量。
- 调整交付顺序：公开 world/visit transport 之前先独立完成 VisitSession production storage，再独立完成 world admission issuer/verifier；`add-server-http-bootstrap` 不得承载这两类前置语义。

## Capabilities

### New Capabilities

- `server-visit-session-storage`: 定义 VisitSession Redis schema、原子一致性、TTL/恢复/清理、提交不确定性、观测与 production integration 验收行为。

### Modified Capabilities

- `delivery-sequencing`: 在公开 HTTP/WSS/TLS-TCP adapters 之前显式加入 VisitSession production storage 与独立 world admission runtime 两个进入门。

## Impact

- 代码：新增 `server/internal/storage/visitsession/`，扩展共享 Redis definitions 与 storage verification；正式 Composition Root 保持不构造 VisitSession runtime。
- 文档：更新 `docs/roadmap.md`、`docs/file-structure.md`、`docs/redis-keys.md`、`server/README.md`；Redis schema 注释继续由 `docs/redis-keys.md` 唯一管理。
- 存储：只增加可失效 Redis 运行态，不增加 MySQL table 或 migration，不把 VisitSession 当作持久事实。
- 协议与网络：不修改 OpenAPI、Protobuf、registry、fixtures、端口或 listener，不签发/验证 admission credential。
- 依赖：复用现有 Redis runtime 和 Go module，不新增第三方依赖、独立 client、后台任务或 memory fallback。
- 回滚：回滚到本 change 开始前的可运行提交；因为没有公开 consumer，删除 adapter/definitions 后现有诊断与 storage runtime 仍可启动，遗留 VisitSession key 由 TTL 自然清理。
