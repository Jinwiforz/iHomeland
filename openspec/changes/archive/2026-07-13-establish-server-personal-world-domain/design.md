## Context

当前服务端已经拥有 `account.PlayerID`、统一 session principal、Composition Root clock/ID 边界和纯 Go application service 模式，但还没有个人持久世界的代码 owner。后续 WorldInstance placement、MySQL/Redis adapter、VisitSession、公开协议和 Unity 都依赖稳定的 PersonalWorld identity 与 mutation 语义；若先实现运行实例或存储 schema，容易把 endpoint、lease、连接 presence 或数据库结构误写进持久世界模型。

本 change 只建立 `server/internal/personalworld`。它把 PersonalWorld 视为持久身份与粗粒度生命周期 aggregate，不承载地图、任务、探索、家园、奖励、Visitor、连接或活动运行状态。所有测试使用 deterministic fake 和并发 reference repository，不启动 listener、MySQL、Redis 或 Docker。

## Goals / Non-Goals

**Goals:**

- 建立稳定 `PersonalWorldID`，并直接使用 `account.PlayerID` 表达不可变 `WorldOwnerID`。
- 保证每个 Player 最多一个 primary PersonalWorld，并让重复创建请求幂等收敛到同一事实。
- 建立严格 snapshot hydration、封闭 lifecycle、从 1 开始且单调递增的持久 revision。
- 建立 Owner-only lifecycle mutation、expected revision、idempotency key、原子提交和 commit-unknown 契约。
- 固定 repository、clock、ID generator 的消费侧接口和稳定错误语义，为 W1/D0/W2 提供边界。
- 通过纯 Go table、fuzz、并发与 race tests 证明不变量。

**Non-Goals:**

- 不实现 WorldInstance、assignment、lease/fencing、placement、休眠或迁移。
- 不实现 PlayerState、地图、任务、探索、家园、资产、奖励或 ActivityInstance 子领域。
- 不实现 VisitSession、Party、Room、capacity、admission 或连接 presence。
- 不实现 MySQL/Redis schema、migration、repository adapter、outbox 或 Composition Root wiring。
- 不新增 Protobuf、OpenAPI、registry、handler、listener、配置、端口或 Unity 文件。

## Decisions

### 1. PersonalWorld 直接持有 `account.PlayerID`，不复制玩家身份类型

`PersonalWorld` 使用 `account.PlayerID` 作为 immutable owner，并通过 `OwnerID()` 暴露该值；文档中的 `WorldOwnerID` 是字段语义，不创建第二个可互转 wrapper。PersonalWorldID 使用独立的受校验值类型和 `pworld_` 前缀，避免 PlayerID 与 world identity 混用。

选择该方案是因为 account package 已经是 Player identity owner。把 PlayerID 移到共享 `identity` package 或新增 WorldOwnerID wrapper 会制造尚无第二消费者证明的抽象，并增加不安全转换入口。

### 2. PersonalWorld lifecycle 只表达持久存在性

初始 lifecycle 为 `active`，允许 Owner-only transition 到 terminal `archived`；`archived` 不可恢复，也不能继续 mutation。世界进程是否启动、休眠、迁移或重建属于 W1 的 WorldInstance/placement，不进入 PersonalWorld lifecycle。

本阶段不增加 `dormant`、`loading`、`online` 等状态，因为它们描述运行承载而不是持久世界事实。未来若出现冻结、删除恢复或合规保留需求，必须通过独立 change 扩展状态机和 migration。

### 3. Aggregate 使用严格构造与 snapshot hydration

新建 PersonalWorld 必须同时获得有效 PersonalWorldID、有效 owner、初始 revision `1`、`active` lifecycle 和非零 UTC `createdAt`。Repository hydration 必须通过显式 snapshot constructor，拒绝零值 ID、错误前缀、无效 owner、revision `0`、未知 lifecycle 和零时间；禁止 adapter 通过 struct literal 绕过不变量。

PersonalWorld 不导出可变字段，不返回内部 map、slice 或 pointer。Owner 没有 transfer API，revision 只能由成功领域 transition 递增。

### 4. Primary world 创建由 repository 原子线性化

Application service 提供 `EnsurePrimaryWorld(owner)`。它为候选世界生成 ID 与创建时间，然后调用 `PersonalWorldRepository.EnsurePrimary`；repository 必须用 owner 唯一约束原子返回 `created` 或既有 `existing` world。并发请求最多创建一个 primary world，loser 返回同一个已提交 snapshot。

Repository 还必须区分 `not_committed` 与 `commit_unknown`。Context deadline 只限制调用方等待，不能证明事务未提交；application 在 unknown 情况下返回稳定 dependency/commit phase，不提交第二个 primary world，也不尝试跨存储补偿。重试时可以构造新的候选 value，但 owner 唯一约束必须使它收敛到既有事实。ID 碰撞或 malformed repository result 作为 dependency defect fail closed。

相比先查询再插入，原子 ensure 契约能够避免 TOCTOU 竞争；相比在 account register 事务中创建 world，它保持 account 与 PersonalWorld repository 的所有权独立，并允许后续使用明确 orchestration/outbox 处理跨 aggregate 创建。

### 5. 第一个具体 mutation 是归档，不提供万能 callback

Application service 提供 `ArchiveWorld`，输入可信 actor、PersonalWorldID、expected revision 和 idempotency key。Service 加载 snapshot 并先验证 actor 等于 immutable owner，再构造规范目标 snapshot；当本地 snapshot 为 active 且 revision 匹配时，还必须用领域 transition 交叉验证目标。Repository 在一个 transaction 内先决议 replay/conflict，再检查当前 revision 与 lifecycle，因此已归档世界的同命令重试仍能 replay，新 key 才返回 invalid state。

不提供 `Mutate(func(*World))`、任意 patch map 或通用 command bus。未来地图、任务、奖励等子领域必须定义自己的 command、owner、revision/transaction 与 settlement 语义，不能借 PersonalWorld aggregate 形成万能状态袋。

### 6. Revision 与 idempotency 在同一 repository transaction 决议

每个成功状态 transition 将 revision 精确加一。`ArchiveWorld` 必须携带大于零的 expected revision；不匹配时返回 revision conflict 且不修改状态。Idempotency key 是有界、安全 ASCII、调用方生成的稳定 command identity，repository 必须把 key、command fingerprint 与结果 snapshot 和 mutation 一起原子提交。

Service 必须先确认 actor 等于 immutable owner，避免未授权身份探测 idempotency record。Repository 以 `(actor PlayerID, idempotency key)` 作为索引作用域：同一 Owner scope 内，同 key、同 fingerprint 的重试返回第一次结果且不再次增加 revision；同 key、不同 world/expected revision/target 的请求返回 idempotency conflict。不同 Owner 使用相同文本 key 不得互相冲突。Fingerprint 使用固定 `personalworld.archive.v1` domain、长度前缀字段、big-endian revision 和 target lifecycle byte，仍包含 actor 作为纵深校验；canonicalization 变更必须视为持久兼容性变化。Revision 解决并发顺序，idempotency 解决响应丢失后的重复提交，两者不能互相替代。

### 7. 错误与提交阶段保持 transport-independent

Package 使用稳定 ErrorKind 区分 validation、not found、forbidden、revision conflict、idempotency conflict、invalid state 和 dependency unavailable，并使用 CommitPhase 表达 `none`、`unknown` 与已提交结果。IdempotencyKey、ArchiveCommand、ArchiveRecord 与 fingerprint 的默认 string、Go-syntax 和 slog 路径必须脱敏；错误文本不得包含完整 key、repository record 或内部 cause。HTTP status 和实时 error code 由后续 adapter/protocol change 映射。

Repository 返回的 world、outcome、revision 或 commit phase 组合必须经过 application 校验。任何 impossible combination 都按 dependency defect 失败，不能伪造成 not found、conflict 或成功。

### 8. 测试替身不得进入正式 Composition Root

生产 package 只包含领域值、aggregate、service 和消费侧 interfaces。并发 reference repository、fake clock、fake ID generator 与 failure injection 只放在 `_test.go`。本 change 不修改 `internal/app` 的生产 dependency graph，也不创建 memory fallback。

测试覆盖并发 ensure、snapshot fuzz、owner spoof、archive replay、same-key/different-command、stale revision、terminal state、commit unknown、malformed repository result 和 secret-safe errors，并在 race detector 下运行共享 reference repository。

## Risks / Trade-offs

- [只有 `active -> archived` 的 lifecycle 较窄] → 该状态机只表达当前已确认的持久存在性；运行态留给 WorldInstance，新增持久状态必须由真实需求驱动。
- [PersonalWorld import account 形成单向 package 依赖] → PlayerID 的所有权保持唯一，account 不反向依赖 personalworld；若未来出现多个身份消费者再评估独立 identity package。
- [跨 account 与 PersonalWorld 的 primary 创建尚非原子] → W0 只定义幂等 `EnsurePrimaryWorld`；W2 根据真实 MySQL transaction/outbox 边界决定 orchestration，不在领域层伪造跨 repository 原子性。
- [commit-unknown 增加调用方复杂度] → 显式表达不确定性并依靠 owner unique key/idempotency key 收敛，避免错误补偿或重复世界。
- [单 aggregate revision 未来可能成为热点] → 当前只保护低频世界元数据；高频地图/任务状态必须拥有独立 owner 和 concurrency boundary，不共享一个全局 world blob。

## Migration Plan

1. 新增纯 Go package 与测试，不修改 production Composition Root，因此部署行为保持不变。
2. 通过 format、unit/fuzz/race、vet、依赖/日志安全、OpenSpec strict 和仓库卫生验证后归档本 change。
3. W1 只消费 PersonalWorldID/owner/revision，不修改本 change 的持久身份语义。
4. D0/W2 实现 repository adapter 与 migration 后，才允许正式 Composition Root 注入 PersonalWorld service。

回滚只需移除尚未接线的 package；本 change 不产生数据库、Redis、网络或客户端迁移。

## Open Questions

本 change 没有阻塞实现或归档的待决问题。世界删除保留期、恢复、地图子领域、跨世界奖励和跨 aggregate orchestration 均属于明确排除的后续设计，必须由独立 change 依据真实需求决定。
