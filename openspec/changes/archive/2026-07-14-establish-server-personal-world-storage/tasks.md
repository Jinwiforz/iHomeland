## 1. Schema 与 adapter 边界

- [x] 1.1 在 `internal/storage/mysql/migrations` 追加四个连续 business table migration，固定 table/column 中文注释与 owner、唯一/外键约束、UTC `DATETIME(6)`、unsigned revision/generation/fence、LF 和不可变 checksum
- [x] 1.2 创建 `internal/storage/personalworld` 与 `internal/storage/placement` package，构造函数只借用 D0 的 `*sql.DB`/`*redis.Client`/Keyspace，不关闭共享资源、不注册 lifecycle component，并添加消费接口 compile-time assertion
- [x] 1.3 增加 migration catalog/schema integration tests，覆盖空库、已有 storage-runtime history、重复/并发启动、constraint 存在、dirty/checksum 拒绝与基线 migration checksum

## 2. PersonalWorld MySQL repository

- [x] 2.1 实现 PersonalWorld row/result codec，严格使用 domain constructors 恢复 ID、owner、lifecycle、revision 与 UTC time，拒绝 unknown/malformed/partial row，并保证默认错误与日志不含 SQL、参数或 identity
- [x] 2.2 实现 `EnsurePrimary` 的 owner unique 线性化、duplicate 解析、candidate collision 检测和 created/existing/not-committed/commit-unknown 映射，禁止先查后插或自动换 candidate
- [x] 2.3 实现 `FindByID` 的 found/not-found/dependency defect 边界，依赖故障和 malformed row 不得伪装成 not-found
- [x] 2.4 实现 `CommitArchive` transaction：先按 actor + idempotency digest 决议 replay/conflict，再锁定并比较 owner/revision/lifecycle，原子提交 archived snapshot 与完整 replay result，禁止自动重放 callback
- [x] 2.5 添加 driver-independent codec/outcome/error tests 与 Docker MySQL tests，覆盖跨 adapter 并发 ensure、revision 竞争、同 key replay/异义 conflict、commit response loss、进程/MySQL restart 和 hydration corruption

## 3. 持久 placement allocation

- [x] 3.1 实现 MySQL sequence/allocation codec 与 transaction allocator，按 PersonalWorld 锁定 high-watermark、同时递增 generation/fence、追加 candidate allocation，并对溢出、历史矛盾和 instance 跨 world 复用 fail closed
- [x] 3.2 实现基于 world/instance/node 稳定 identity 的 allocation 解析和 burned allocation 语义：重试时钟推进只恢复首次结果时间；Redis 后续失败不得删除、递减、复用或重新发布既有不确定 allocation
- [x] 3.3 添加 allocator unit/integration tests，覆盖首次分配、同 candidate replay、跨进程并发、sequence/fence 溢出、duplicate instance、transaction commit unknown、MySQL restart 和 Redis flush 后继续严格增长

## 4. Redis placement state 与原子 scripts

- [x] 4.1 登记 `placement_assignment` 与 `placement_transition` definitions，固定 owner/kind、schema v1、encoded size、TTL、recovery/cleanup/failure/metrics metadata，并让 Keyspace 真实输出 `ih:<env>:placement:<kind>:...`
- [x] 4.2 实现 assignment/replay typed codec、canonical decimal helpers 与稳定 command fingerprint，严格验证完整 stamp、phase、UTC microseconds、outcome、TTL 和字段集合；unknown version、oversize、非法 ID/时间/uint64 全部 fail closed
- [x] 4.3 实现 Acquire/Activate/Renew/Revoke/Replace/QualifyWrite 固定 Lua scripts，原子比较完整 current stamp/phase/expiry，禁用 mutation implicit retry、namespace scan 与 Lua double uint64 arithmetic
- [x] 4.4 实现 assignment `PEXPIREAT` 向上取整和精确领域 expiry 双重检查，以及覆盖有界 retry window 的 transition replay TTL；物理 key 额外存活不得延长 activate/renew/write qualification
- [x] 4.5 实现 script result 到现有 `ResolveOutcome`/`StoreOutcome`/snapshot/fence 的严格映射，任何矛盾 result、stale successor overwrite 或敏感 client error 默认格式化均 fail closed
- [x] 4.6 添加 codec/table/fuzz 与真实 Redis tests，覆盖 starting/active、stale stamp、expiry 边界、revoke replay 不误伤 successor、replace/renew、uint64 精度、corruption、response loss 和 Redis restart

## 5. 跨存储恢复与接口验收

- [x] 5.1 组合 allocator 与 Redis state 实现完整 `placement.PlacementStore`，固定“先 MySQL allocation、后 Redis transition”，只有本次新 allocation 可以首次发布，所有方法允许并发调用且不创建第二 client/pool
- [x] 5.2 实现 response-loss resolve：精确 current/replay 收敛为 replay/in-progress，明确不同 current 返回 conflict，其余保持 commit-unknown；禁止后台静默重放或把顺序操作报告为分布式 transaction
- [x] 5.3 增加 Redis flush/restart recovery tests，证明旧 allocation/lease 不被重新发布，新 candidate 获得更高 generation/fence，旧 runtime 的 renew/revoke/qualify 全部失败且 PersonalWorld MySQL 事实不受影响
- [x] 5.4 增加跨 adapter duplicate instance、acquire competition、Replace 时钟推进 replay、Redis 执行前/响应后失败、commit-unknown 后 flush、dependency restart 与 race 验收；复用 D0 lifecycle tests 证明共享资源受控关闭，adapter 本身没有 memory fallback 或 goroutine
- [x] 5.5 明确验收 archive 是可在无 assignment 时执行的 Owner 控制面 lifecycle transaction；不新增通用 world blob/fenced-write API 或无 consumer outbox，并以测试/结构扫描证明正式 Composition Root 未接线 unused service、RuntimeController、业务 listener 或 fake adapter

## 6. Observability、文档与质量门

- [x] 6.1 扩展固定 adapter/operation/outcome observability，记录 repository、allocation、script、replay、burned/unknown 结果且保持低基数；禁止 SQL、完整 key/value、idempotency key、fence 和 player/world/instance identity 进入 label 或普通日志
- [x] 6.2 通过现有 `tools/storage/storage.ps1 -Action verify` 的 `./internal/storage/...` package glob 纳入新增故障恢复测试，继续复用 run ownership、单次 Docker CLI deadline、独立 cleanup budget 与 secret hygiene
- [x] 6.3 更新 `docs/file-structure.md`、`docs/redis-keys.md`、`docs/storage-schema-comment-convention.md`、`docs/roadmap.md` 与 `server/README.md` 的 adapter/table/key owner、实际 namespace、恢复/清理和“仍无公开 world API”边界
- [x] 6.4 复核全部手写 Go/PowerShell/SQL 注释与错误格式化，确保导出/业务声明、struct 字段、interface 方法、Lua/transaction 不变量、单位、所有权、失败与 cleanup 符合项目注释规范且无无上下文 TODO
- [x] 6.5 使用项目入口执行 format、unit/integration/contract/fuzz/race/vet/mod verify、MySQL restart、Redis flush/restart、`git diff --check`、secret/generated/cache hygiene 和全量 OpenSpec strict
- [x] 6.6 将 `server-personal-world-storage` delta 同步到主 specs，复核 tasks 与实际实现一致并再次 strict，满足归档条件但不提前提出 VisitSession、协议、transport 或 Unity 实现
