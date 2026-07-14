## Context

W0 已定义 `PersonalWorldRepository` 的 owner 唯一、revision、idempotency 与 commit-unknown 契约，W1 已定义 `PlacementStore` 的 current assignment、两阶段发布、lease/fencing、replace/revoke 和写资格契约。D0 提供唯一 MySQL pool/migrator、standalone Redis client、Keyspace/registry、transaction outcome 和 Docker harness，但刻意没有创建业务表、key 或 adapter。

本 change 需要同时满足两种不同的数据性质：PersonalWorld 是必须跨进程恢复的 MySQL 持久事实；current WorldInstance 是可以丢失的 Redis 运行态，但 generation/fencing token 的历史高水位不能随 Redis flush 回退。实现还必须保留消费侧接口，不让 infrastructure 反向定义第二套 domain outcome，也不能用跨 MySQL/Redis 的顺序双写伪装原子提交。

## Goals / Non-Goals

**Goals:**

- 实现 `personalworld.PersonalWorldRepository` 的 production MySQL adapter，并严格映射已有 outcome 组合。
- 实现 `placement.PlacementStore` 的 production adapter，以 MySQL allocation ledger 保证 generation/fence 永不复用，以 Redis Lua 线性化 current runtime transition。
- 在响应丢失、Redis flush、MySQL/Redis restart 与并发请求下保持 fail-closed、可解析和可重复验收。
- 为所有新 table、key、value schema、migration、指标与 cleanup 指定 owner 和有界策略。

**Non-Goals:**

- 不新增地图、任务、探索、资产、奖励、PlayerState 或通用 world blob/table。
- 不实现 RuntimeController、VisitSession、Account/Session adapter、公开协议、listener、handler、无 consumer 的 outbox table/publisher 或 Unity。
- 不把 repository/store 注册为 lifecycle component，不创建第二个 MySQL pool/Redis client，也不把尚无业务 consumer 的 adapter 接入正式 service graph。
- 不声称 MySQL/Redis 能组成分布式 transaction；跨存储不确定结果必须显式暴露并由稳定 identity 解析。

## Decisions

### 1. Owner-specific adapter 使用独立 storage package

新增 `internal/storage/personalworld` 和 `internal/storage/placement`。前者只依赖共享 `*sql.DB` 与 `personalworld` 消费接口；后者组合共享 `*sql.DB`、`*redis.Client`、Redis `Keyspace` 与 `placement` 消费接口。基础 `storage/mysql`、`storage/redis` 继续只拥有资源 runtime 与通用 policy。

相比把业务 SQL/Lua 直接堆入基础 runtime package，独立 package 能让 table/key/codec/错误映射由业务 adapter owner 管理；相比按 backend 拆成两个半成品，placement package 能明确拥有跨 MySQL allocation 与 Redis current state 的完整失败语义。

### 2. PersonalWorld 使用规范化表与独立幂等结果表

`personal_worlds` 以 `personal_world_id` 为主键、`owner_player_id` 为唯一键，保存封闭 lifecycle、unsigned revision 与 UTC `DATETIME(6)` created time。没有 Account/Player production table 时不创建伪外键；`owner_player_id` 仍由 account identity constructor 和 repository hydration 双重校验。

`personal_world_idempotency` 以 `(actor_player_id, idempotency_key_digest)` 唯一，保存 operation、command fingerprint 与完整 replay result 投影。原始 idempotency key 不进入普通日志或表；adapter 使用 SHA-256 digest 建索引，并继续比较 domain `CommandFingerprint` 防止同 key 异义重用。

为避免 storage adapter 复制领域 fingerprint canonicalization，`personalworld` 为固定 32-byte
摘要提供只面向持久化的值副本导入/导出入口。该入口不改变 repository interface 或领域行为，
摘要仍不得进入普通日志、metrics label 或外部协议；非法长度和全零持久行继续 fail closed。

`EnsurePrimary` 依靠 owner unique constraint 线性化并发创建；duplicate key 后必须解析 owner 的已提交 world，不能先查后插。`CommitArchive` 在同一 MySQL transaction 内先只读决议已提交 replay；miss 后统一锁定 world，再二次锁定并决议幂等 key，随后比较 owner/revision/lifecycle、更新 world 并写 replay result。该固定锁序避免不同 idempotency key 各持 gap lock 后争同一 world 的死锁，同时使同 key race 在二次读取处收敛。Commit error 一律映射为现有 commit-unknown，callback 不自动重放。

当前 mutation 没有跨 owner 事件、外部 side effect 或已定义 consumer，因此不创建空 outbox。未来真实事件投递需求必须由其 owner change 同时定义 event schema、publisher、consumer、幂等和保留策略，不能先提交无人消费的表。

### 3. Generation/fence 由 MySQL append-only allocation ledger 分配

Redis 不能成为单调 fencing 历史的唯一来源。`placement_sequences` 为每个 PersonalWorld 保存最后发出的 generation/fencing high-watermark；`placement_allocations` 追加保存 WorldInstanceID、node、generation、fence 和候选时间，并对 WorldInstanceID、`(world,generation)`、`(world,fence)` 建唯一约束。

Acquire/Replace 先在 MySQL transaction 中锁定 sequence、检查溢出、同时递增 generation/fence 并插入 allocation。已分配数值永不回收；即使后续 Redis 明确未写入也只会烧掉序号。相同 world、WorldInstanceID 与 RuntimeNodeID 再次请求时只能解析既有 allocation，不会获得第二组数字；首次 candidate 的 created time 与 initial expiry 是已提交结果，不是重试 identity，重试时钟推进不得制造冲突。旧 WorldInstanceID 永远不能跨 world、跨 node 或在后续 generation 重用。

相比从 Redis `INCR` 或 current value 推导高水位，该设计能跨 flush/restart 保持单调；相比只保存每个 world 的最后值，append-only allocation 能拒绝历史 instance identity 复用并为不确定请求提供稳定解析证据。

### 4. Redis Hash + Lua 拥有 current assignment 的线性化点

实际 key 使用：

- `ih:<env>:placement:assignment:<personalWorldID>`：TTL-required Hash，schema v1，保存完整 stamp、phase、created/expires UTC microseconds。
- `ih:<env>:placement:transition:<transitionDigest>`：TTL-required replay record，schema v1，保存 operation、request fingerprint、outcome 与必要 result snapshot。

Acquire、Activate、Renew、Revoke、Replace 与 QualifyWrite 分别由固定 owner Lua script 原子比较完整 stamp/phase/expiry 并写入结果。Script 不接收客户端 identity，不扫描 namespace，也不依赖 go-redis mutation retry。Generation、fence 和 microsecond timestamp 以规范十进制字符串编码；Lua 使用长度后字典序比较和精确字符串相等，禁止用 double `tonumber` 破坏 uint64 精度。

Transition fingerprint 只绑定可跨重试复用的 command identity：Acquire 绑定已分配 stamp，Activate/Revoke 绑定 expected stamp，Renew 额外绑定目标 expiry，Replace 绑定 predecessor 与 successor stamp。每次调用重新读取的 `observedAt`、以及 Replace 重试临时候选的 created time/expiry 不进入 fingerprint；它们只参与当次 expiry 校验，首次提交的结果时间由 allocation/replay 恢复。

Assignment key 的 Redis expiry 向上取整到毫秒，value 仍保存精确 microsecond deadline；script 每次使用 request 中的受信 `observedAt` 做精确到期判断，因此 key 最多额外存活不足一毫秒但不能在领域 expiry 后续租、activate 或取得写资格。Transition replay TTL 由构造时的有界 policy 提供，必须覆盖调用方 retry window，不能永久保存。

### 5. 跨存储流程只承诺安全，不承诺伪原子成功

新 candidate 的流程固定为“先持久分配，后尝试 Redis transition”。只有本次刚创建的 allocation 可以首次发布；若相同 candidate 已有 allocation而 Redis 中既无精确 current/replay 证据，也不能证明明确未应用，adapter 返回 commit-unknown，禁止把旧 allocation 重新发布为新 lease。

Redis script 返回后连接中断时，adapter先用相同 current/replay identity 解析：精确匹配可返回 replay，明确不同 current 返回 conflict，其余保持 commit-unknown。Redis flush 后旧 assignment/replay 消失，旧 allocation 不会被复活；后续全新 candidate 从 MySQL 获得更大的 generation/fence，旧 runtime 即使仍存活也不能再次通过 current qualification。

该策略会在跨存储故障时烧掉 allocation 或返回暂时不确定，这是用可用性换取 fencing 不回退。不得通过删除 MySQL allocation、从 Redis current 猜测高水位或后台静默重放来“修复”表面成功率。

### 6. Redis codec、registry 与错误边界保持封闭

Assignment/replay codec 只接受固定 schema v1、完整字段、大小上限、封闭 phase/outcome 和严格 domain constructor；unknown version、缺字段、溢出、非法 ID 或时间矛盾全部视为 dependency defect。完整 Redis key/value、fencing token、idempotency key、SQL、参数和 driver/client 原文不得进入普通错误、日志或 metrics。

Adapter 复用 D0 安全 error chain：默认文本只记录固定 component/operation/outcome，底层 cause 仅供受控 `errors.Is/As`。Metrics 使用固定 adapter/operation/outcome label，world/player/instance identity、migration version 和原始错误不作为 label。

### 7. Archive 是控制面 lifecycle mutation，不伪装 runtime world-state 写入

当前 PersonalWorld 只有 ensure/archive lifecycle 事实。Archive 由 immutable Owner、expected revision 和 idempotency transaction 授权，必须允许世界没有运行实例时执行，因此不要求 `placement.WriteFence`。本 change 不创造任何来自 WorldInstance 的地图/任务/奖励持久 mutation，也不新增无 consumer 的通用 fenced-write API。

Placement adapter 必须确保只有 current active 且 lease 有效的完整 stamp 能取得 point-in-time `WriteFence`。未来真实 world-state owner 出现时，其独立 change 必须设计最终 MySQL commit gate 来重新验证该 fence；不能把本 change 的 archive transaction 或一次 `QualifyWrite` 误称为永久运行写授权。

### 8. Migration 只前进，adapter 暂不接入正式业务 graph

storage-runtime 基线已经让 `000001` 与 history table definition 提供完整中文 table/column 注释；本 change 只在现有 catalog 后追加四个单 statement business migrations，分别创建 `personal_worlds`、`personal_world_idempotency`、`placement_sequences` 与 `placement_allocations`。`.gitattributes` 固定 SQL 为 LF，避免跨平台 checkout 改变 migration bytes。D0 Composition Root 会照常执行新增 catalog，但 W2 不实例化尚无 consumer 的 repository/service，也不改变 diagnostic-only 对外边界。

Adapter 通过 compile-time interface assertion、纯 codec/result tests 和 Docker integration 验收。Storage harness 继续提供唯一真实 MySQL/Redis 入口，并覆盖两个独立进程共享数据库/Redis 的并发和恢复场景。

## Risks / Trade-offs

- [MySQL allocation 成功但 Redis transition 失败会产生空洞] → allocation 永不复用；指标记录 burned/unknown outcome，安全优先于序号连续。
- [Redis flush 使旧 commit-unknown 无法最终证明] → 不重发旧 allocation，保持 commit-unknown；新业务尝试必须使用新 WorldInstanceID 并获得更高 fence。
- [Transition replay TTL 到期后无法回答很晚的重复请求] → TTL 必须覆盖有界 retry/lease 窗口；过期后返回 not-found/conflict/unknown，绝不猜测 replay 成功。
- [Lua 十进制比较或 codec 漂移导致错误决议] → 单一 codec、canonical encoding、table/fuzz/golden tests，并在真实 Redis 覆盖 uint64 边界。
- [Append-only allocation/idempotency 表持续增长] → 本阶段保留安全审计与永不复用证据；任何归档/压缩必须在有数据规模和保留期证据后独立设计，不在本 change 静默删除。
- [Archive 无 runtime fence 容易被误解为例外漏洞] → 文档明确它是 Owner 控制面 lifecycle mutation；本 change 不允许任何 runtime-originated world-state commit。
- [新增 migration 无法自动 down] → 向后回滚应用时保留新表；旧二进制忽略它们，禁止自动删除可能已经提交的数据。

## Migration Plan

1. 追加并验证四个 business table migration，覆盖中文 schema 注释、约束、UTC/unsigned 映射、空库和仅有 storage-runtime history 的升级 catalog。
2. 实现 PersonalWorld MySQL adapter及 driver-independent outcome/codec tests，再在 Docker MySQL 覆盖并发、idempotency 和 commit interruption。
3. 实现 placement allocation ledger、Redis definitions/codec/scripts 和跨存储 adapter，先验证单操作，再覆盖 response loss、flush/restart 与跨进程并发。
4. 扩展 observability 与 owner 文档；由现有 storage harness package glob 纳入新增测试，运行 unit/integration/fuzz/race、strict 和仓库卫生检查。
5. 本 change 不增加业务 service graph；后续 transport/vertical-slice change 只在对应所有 adapter 与 runtime controller 就绪后接线。

回滚代码时移除 adapter 使用并恢复旧二进制；新 MySQL 表保留且不 down，Redis assignment/replay key 由 TTL 或显式测试 cleanup 回收。若 migration 执行中断留下 dirty history，继续遵守 D0 显式审计修复流程。

## 已确定参数

- ID 列上限为 128 ASCII bytes；摘要使用 SHA-256 32 bytes；持久绝对时间使用 UTC `DATETIME(6)`/`TIMESTAMP(6)`。
- Assignment/transition Hash encoded budget 分别为 2048/3072 bytes，schema version 固定为 1。
- Replay TTL 由调用方按 retry window 提供，constructor 限制为 1 秒至 24 小时；assignment TTL 由 lease absolute expiry 决定。
- Integration 总 timeout 与 Docker cleanup budget 继续由 storage harness 统一管理，不在 adapter 内复制。
