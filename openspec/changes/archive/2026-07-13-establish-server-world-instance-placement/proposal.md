## Why

PersonalWorld 已经固定持久 identity、owner 与 lifecycle，但服务端还没有独立 owner 决定世界由哪个可替换运行实例承载，也无法证明旧实例失去写资格。现在需要先冻结 WorldInstance placement、assignment generation 与 lease/fencing 语义，避免后续 Redis adapter、访客 admission 和网络 endpoint 各自发明不一致的运行资格。

## What Changes

- 新增 transport-independent `placement` domain/application package，建立 `WorldInstanceID`、节点 identity、assignment generation、实例状态与 placement snapshot 不变量。
- 定义 PersonalWorld 的按需 `EnsureActive`、休眠、重建与迁移编排；同一 PersonalWorld 最多只有一个 current active writable WorldInstance。
- 定义带 TTL 的 lease、单调 fencing token、renew/revoke 和写入资格校验契约，旧 assignment、旧 generation、过期 lease 或旧 fencing token 一律 fail closed。
- 定义消费侧 `PlacementStore`、`RuntimeController`、`Clock` 与 `IDGenerator` 契约，并以 deterministic fakes 和并发 reference store 完成纯 Go 验收。
- 明确 assignment/lease 是可恢复运行态，不能改变 PersonalWorldID、WorldOwnerID 或持久 revision；本 change 不实现 Redis、MySQL、协议、endpoint、admission、listener 或 VisitSession。

## Capabilities

### New Capabilities

- `server-world-instance-placement`: 定义 WorldInstance 运行身份、assignment generation、placement 状态机、lease/fencing、迁移与 stale-instance 拒绝要求。

### Modified Capabilities

无。

## Impact

- 新增 `server/internal/placement` 手写 Go 代码与测试。
- 复用 `server/internal/personalworld.PersonalWorldID`、Composition Root 的 clock/ID 生产边界和既有错误、并发、日志安全规范，但不接入正式 Composition Root。
- 不新增或修改 Protobuf、OpenAPI、registry、listener、配置、端口、数据库 schema、Redis key、Unity 文件或生成代码。
- 为后续 `establish-server-storage-runtime`、`establish-server-personal-world-storage`、VisitSession、world protocol 与 admission 提供稳定消费侧契约。
