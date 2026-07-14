## Why

Account、Session、PersonalWorld 与 Placement 已经冻结消费侧存储契约，但正式服务端仍只有诊断 listener，没有可重复初始化、可诊断失败和可受控关闭的 MySQL/Redis 生产资源。现在需要先建立统一数据运行时，避免后续 repository/cache adapter 各自创建连接、migration、key、重试和恢复语义。

## What Changes

- 新增 MySQL 生产 pool 与 migration runtime，固定连接验证、UTC/严格模式、transaction helper、migration lock/history/checksum、中文 schema 注释、空库初始化和失败回滚边界。
- 新增 Redis 生产 client runtime，固定 namespace/key builder、TTL/value schema registry、字段字典责任、超时与安全重试分类、连接验证、flush/损坏值故障语义和有界关闭。
- 扩展严格配置与 secret provider 边界，使 storage endpoint、pool、timeout 和 TLS policy 在副作用前校验，凭据不进入普通配置快照、日志、metrics 或错误文本。
- 将 MySQL 与 Redis 作为正式 Composition Root 的必需 lifecycle/readiness 前置，并补齐启动失败、逆序关闭、依赖中断和恢复可观测性。
- 提供使用 `versions.yaml` 锁定 MySQL/Redis 镜像的 Docker integration harness，覆盖空库 migration、重复启动、并发 migrator、Redis flush、进程重启和 shutdown；不注入 memory fallback。
- 更新 storage 目录、Redis key registry 机制、服务端运行说明和本地验证入口；本 change 不登记尚无 adapter owner 的业务 key definition。
- 本 change 不实现 Account repository、SessionStore、PersonalWorld repository、PlacementStore、业务 transaction/outbox、公开 handler/listener、协议或 Unity 接入。

## Capabilities

### New Capabilities

- `server-storage-runtime`: 定义 MySQL/Redis 生产资源、migration、transaction/retry policy、key/value/TTL registry、恢复、生命周期与独立 integration 验收要求。

### Modified Capabilities

- `server-architecture`: 将 MySQL/Redis 资源纳入唯一 Composition Root 的必需启动、readiness、失败回滚和逆序关闭行为。

## Impact

- 新增 `server/internal/storage/mysql`（含嵌入式 `migrations/`）、`server/internal/storage/redis` 与统一 storage integration 工具入口。
- 修改 `server/internal/config`、`server/internal/app`、observability、示例配置与测试，使 storage 配置先验证、资源按依赖顺序接线且失败不进入 ready。
- 增加锁定的 Go MySQL/Redis driver 依赖及必要版本治理，复用 `versions.yaml` 中的 MySQL `9.7.1` 与 Redis `8.8.0` 镜像版本。
- 更新 `docs/redis-keys.md`、`docs/storage-schema-comment-convention.md`、`docs/file-structure.md`、`docs/technology-versions.md` 与 `server/README.md`；不修改 Protobuf/OpenAPI/registry、网络端口分配、数据库业务 schema、Unity 或生成代码。
- 为后续 `establish-server-personal-world-storage` 以及 Account/Session production adapter changes 提供受控基础设施，不提前宣称任何业务 API 可用。
