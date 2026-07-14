## 1. 依赖、配置与 secret 边界

- [x] 1.1 核验并锁定 `go-sql-driver/mysql`、`go-redis/v9` module 版本以及 MySQL/Redis 目标平台 image digest，同步 `versions.yaml`、`go.mod` 与 `go.sum`，拒绝浮动 tag/version
- [x] 1.2 扩展 `internal/config` 的 MySQL/Redis endpoint、database、pool、timeout、probe 与 TLS 类型和白名单环境覆盖，补齐 unknown field、范围、交叉约束与 production TLS fail-closed 测试
- [x] 1.3 实现独立 SecretProvider、白名单 environment/file source 与默认脱敏 secret value，确保配置/读取错误只暴露稳定 reference，并覆盖缺失、空值、读取失败和格式化/日志安全
- [x] 1.4 更新 `server/config/local.yaml` 的非敏感 storage 示例，确认仓库不提交 password、private key、`.env`、本机 endpoint 状态或自动生成配置

## 2. 可重复 Docker integration harness

- [x] 2.1 创建 `tools/storage/storage.ps1` 非交互入口，读取并验证 `versions.yaml` 的 tag/digest，生成唯一 run-id、loopback 随机端口、network/volume/container label 与高熵临时 secret
- [x] 2.2 实现 container health 等待、全局 timeout、阶段日志和 `finally` cleanup，所有删除操作先验证绝对 `.local/storage/<run-id>` 与 Docker resource label，禁止清理非本次资源
- [x] 2.3 提供 CI 可调用的 `verify` 以及必要的本地 `up/down/status` 契约，状态只写已忽略目录，并增加失败、取消、并发运行和重复 cleanup 的 PowerShell/等价 contract tests
- [x] 2.4 为每次 Docker CLI 调用传播剩余 deadline，超时或取消时终止子进程；为 `finally` cleanup 分配独立有界预算，覆盖挂起进程终止与残留 state 恢复契约

## 3. MySQL runtime、migration 与 transaction policy

- [x] 3.1 创建 `internal/storage/mysql` package 和有界 lifecycle component，配置唯一 `database/sql` pool、UTC/strict SQL mode、TLS、timeout 与 pool limits，并实现 startup probe、局部失败清理和幂等 Stop
- [x] 3.2 实现嵌入式 LF/single-statement migration catalog、严格 version/name/checksum 校验、专用 connection advisory lock，以及带中文 table/column 注释的 `ih_schema_migrations` in-progress/applied history
- [x] 3.3 实现 dirty、checksum mismatch、version gap/duplicate、lock timeout 和中断后 fail-closed 诊断，以及不自动 down/猜测修复的显式 migration result/error contract
- [x] 3.4 实现一次性 callback 的 `WithinTx`、rollback/commit error joining 与 pre-commit/not-committed/commit-unknown classifier，禁止自动重放业务 callback 或吞并 owner conflict
- [x] 3.5 使用 driver-independent fakes 覆盖 pool/migration/transaction 状态与安全格式化，并在 Docker MySQL 中覆盖空库、重复/并发 migration、dirty/checksum 拒绝、commit interruption、restart 和 shutdown

## 4. Redis runtime、key registry 与 TTL policy

- [x] 4.1 创建 `internal/storage/redis` standalone lifecycle component，配置 TLS/auth/database/pool/command timeout、startup probe、mutation 隐式 retry 禁用、局部失败清理和幂等 Stop
- [x] 4.2 实现集中 Keyspace 与不可变 registry definition，校验 environment/owner/kind/identity segment、schema version、最大 encoded size、TTL/cleanup/recovery/failure/metrics metadata，为已实现 value schema 规定中文字段字典，并让默认日志只暴露 pattern/owner
- [x] 4.3 实现 absolute-expiry TTL helper、非正 TTL 拒绝、unknown version/oversize/corruption fail-closed 和 read-only/atomic mutation outcome 分类，不提供 generic map cache 或 owner-specific value/script
- [x] 4.4 添加 key builder table/fuzz、namespace/colon/brace/control-character 注入、敏感 digest、TTL boundary、registry duplicate/malformed 和错误/日志安全测试
- [x] 4.5 在 Docker Redis 中覆盖 startup/shutdown、standalone 校验、expiry、flush、restart、dependency interruption、commit-unknown fault injection 和不回退 memory state

## 5. Composition Root、readiness 与可观测性

- [x] 5.1 扩展 observability 的 MySQL/Redis pool、probe、migration、transaction/command 与 lifecycle 低基数 metrics，禁止 SQL、DSN、完整 key/value、identity 和原始 error text 成为 label
- [x] 5.2 在 `internal/app` 中解析 secret 并按 diagnostic -> MySQL/migration -> Redis 顺序构造唯一 production graph，为 storage probe 创建明确 TaskGroup owner，只有全部成功才进入 ready
- [x] 5.3 覆盖 MySQL/Redis 各阶段 startup failure 的精确逆序回滚、probe 连续失败触发 draining/non-zero、并发 shutdown、总 deadline、关闭错误聚合和 diagnostic 最后关闭
- [x] 5.4 增加 process/integration tests 证明 storage 缺失或损坏时不 ready、依赖恢复后新进程可启动，且正式 graph 未注入 Account/Session/PersonalWorld/Placement fake 或开放业务 listener

## 6. 文档、恢复验收与质量门

- [x] 6.1 更新 `docs/redis-keys.md` 的 standalone/真实 key 输出、registry/TTL/value metadata 与 Cluster 进入条件，并同步 `docs/file-structure.md`、`docs/technology-versions.md`、`server/README.md` 的 owner、命令、配置、启动/清理和 ready 边界
- [x] 6.2 从干净临时资源运行 storage verify，验收空库 migration、重复/并发启动、MySQL restart、Redis flush/restart、依赖持续中断、成功/失败 cleanup、进程恢复和无持久事实误删
- [x] 6.3 使用项目 Go 入口执行 format、unit/contract/integration/fuzz/race、vet 与 dependency/secret scan，执行 `git diff --check`、generated/cache/本机配置卫生检查和全量 OpenSpec strict
- [x] 6.4 复核所有手写 Go/PowerShell/SQL 注释、MySQL table/column 中文注释、Redis 字段字典、migration/key/table/interface owner、错误/日志脱敏、配置单位/范围和 rollback 说明符合项目文档，确认没有 TODO、silent retry、memory fallback、业务 adapter、协议或 Unity 越界
- [x] 6.5 将 `server-storage-runtime` 与 `server-architecture` delta 同步到主 specs，更新实际任务勾选与验证记录后再次 strict，满足归档条件但不提前归档后续 W2/Account/Session changes
