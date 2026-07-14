## Why

PersonalWorld 与 WorldInstance placement 已冻结消费侧接口，D0 也已提供受控 MySQL/Redis runtime，但正式服务端仍没有实现这些接口的 production adapter。现在需要把持久世界事实、幂等结果和可失效 assignment 落到明确的数据模型中，并在 Redis 丢失、响应丢失和进程重启后继续保证 primary world 唯一、revision 不回退以及 fencing token 不复用。

## What Changes

- 增加 PersonalWorld MySQL schema 与 production repository，实现 owner 唯一 primary world、严格 hydration、revision compare-and-commit、archive idempotency replay/conflict 和 commit-unknown 分类。
- 增加 PlacementStore production adapter：MySQL 持久保存不可回退的 generation/fencing allocation，高水位分配成功后再由 Redis Lua 原子维护 current assignment、phase、lease、replace/revoke 与 bounded replay evidence。
- 为 `placement:assignment` 与 placement transition replay 登记明确 Redis key/value schema、TTL、owner、恢复与故障策略；Redis flush 后不恢复旧 lease，也不从零重新发号。
- 扩展 storage Docker integration 验收，覆盖并发 primary world、revision/idempotency 竞争、duplicate instance、stale fence、commit-unknown、MySQL restart、Redis flush/restart 与跨进程恢复。
- 为全部受管 MySQL table/column 建立中文 schema 注释与信息架构验收，为已实现 Redis Hash 建立长期字段字典。
- 更新 storage/Redis key/目录和本地验收文档。
- 本 change 不创建地图、任务、奖励、通用 world blob 或无 consumer 的 outbox，不实现 Account/Session/VisitSession adapter、RuntimeController、公开协议/handler/listener 或 Unity 接入，也不把 test fake 接入正式 Composition Root。

## Capabilities

### New Capabilities

- `server-personal-world-storage`: 定义 PersonalWorld MySQL repository、Placement MySQL fencing allocation + Redis current state adapter、跨存储失败语义、恢复边界与 integration 验收。

### Modified Capabilities

无。现有 `server-personal-world`、`server-world-instance-placement` 与 `server-storage-runtime` 已冻结消费接口和基础运行时要求；本 change 通过新的 adapter capability 落实这些既有契约，并按上游要求保留只由稳定 WorldInstance identity 决议的 response-loss replay，不改变其领域行为。

## 进入、验收与回滚

- 上游基线：`d4e1962` 的 PersonalWorld core、`65c807f` 的 placement core 与 `5269271` 的 storage runtime，以及对应主 specs。
- 状态 owner：`storage/personalworld` 拥有 MySQL PersonalWorld/replay 投影；`storage/placement` 拥有 MySQL allocation 与 Redis assignment/replay。Owner lifecycle mutation 仍属于 PersonalWorld；本 change 不拥有 gameplay mutation、settlement 或 safe-return。
- 协议与安全：不新增协议、listener 或客户端入口；actor/instance/node 必须来自受信上游，默认错误、日志和 metrics 不暴露 identity、SQL、key/value、fence、幂等材料或 secret。
- 验收：项目 Go 入口的 unit/integration/fuzz/race/vet/mod verify、Docker storage verify、schema/Redis contract、`git diff --check` 与全量 OpenSpec strict 全部通过，主 spec 同步且正式 Composition Root 仍未接线 world API。
- 回滚：代码回滚到可运行提交 `5269271`；已执行的 forward MySQL migration 不 down、不删数据，旧二进制忽略新增表，Redis key 由 TTL 或测试 ownership cleanup 回收。

## Impact

- 新增 `server/internal/storage/personalworld` 与 `server/internal/storage/placement` production adapter package，并向 `server/internal/storage/mysql/migrations` 追加不可变 forward migrations。
- 复用既有 `personalworld.PersonalWorldRepository`、`placement.PlacementStore`、MySQL transaction/runtime、Redis Keyspace/registry 与受控错误分类，不引入第二套领域接口或数据库 client。
- 增加 PersonalWorld、idempotency、placement allocation/high-watermark 表，以及 assignment/replay Redis definitions；每个 table/key 由对应 adapter owner 管理。
- 复用 `tools/storage/storage.ps1` 的 `./internal/storage/...` 入口自动纳入新增 integration/recovery tests，并更新 `docs/file-structure.md`、`docs/redis-keys.md`、`docs/storage-schema-comment-convention.md` 与 `server/README.md`。
- 正式进程继续只开放 diagnostic listener；production adapters 完成后仍等待后续 service/transport change 决定 Composition Root 业务接线。
