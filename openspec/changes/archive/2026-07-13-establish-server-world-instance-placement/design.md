## Context

`server/internal/personalworld` 已经拥有 PersonalWorld 持久 identity、immutable owner、revision 与粗粒度 lifecycle，但明确排除了运行承载。当前服务端没有 owner 回答“某个 PersonalWorld 现在由哪个进程实例承载”“该实例是否仍可写”以及“休眠、重建或迁移后旧实例为何不能恢复资格”。后续 Redis placement adapter、VisitSession、world admission 和 TCP gameplay 都会消费这些答案，因此必须在基础设施和协议之前固定 transport-independent 模型。

本 change 新增 `server/internal/placement`，只建模 WorldInstance placement 与应用编排。它消费 `personalworld.PersonalWorldID`，但不读取或修改 PersonalWorld owner、lifecycle、revision 与持久内容。测试使用 deterministic fake、并发 reference store 和 fake runtime controller，不启动 listener、MySQL、Redis 或 Docker。

## Goals / Non-Goals

**Goals:**

- 建立 `WorldInstanceID`、`RuntimeNodeID`、assignment generation、fencing token、lease deadline 与 placement phase 的严格值对象和 snapshot。
- 保证一个 PersonalWorld 最多存在一个 current assignment，且最多一个 active writable WorldInstance。
- 定义按需启动、续租、休眠、重建和迁移的条件状态迁移与失败语义。
- 让每次写入资格都由完整 assignment stamp 和当前未过期 lease 决定，拒绝旧 instance、generation、node 或 fencing token。
- 定义 placement store、runtime controller、clock 与 ID generator 的消费侧契约，为 D0/W2 提供可实现边界。
- 通过纯 Go table、fuzz、并发与 race tests 证明线性化、失效和 stale-instance 不变量。

**Non-Goals:**

- 不实现 Redis key/value、Lua、MySQL fencing high-watermark、migration 或 production adapter。
- 不实现 scheduler、负载均衡、跨地域路由、endpoint discovery、进程间复制或 snapshot transfer。
- 不实现 PersonalWorld 持久内容加载与 mutation repository，也不修改其 revision/lifecycle。
- 不实现 VisitSession、invite、admission、connection presence、Protobuf、OpenAPI、handler、listener 或 Unity。
- 不承诺无中断迁移；服务端 v1 优先保证旧写者失效和状态可诊断。

## Decisions

### 1. Placement 独立拥有运行身份，不扩展 PersonalWorld aggregate

`placement` 直接消费 `personalworld.PersonalWorldID`，并定义独立的 `WorldInstanceID` 与 `RuntimeNodeID`。WorldInstanceID 使用受校验的 `winst_` 前缀值；RuntimeNodeID 表示受信服务端运行节点 identity，不包含 URL、端口或客户端可提交 endpoint。Assignment snapshot 关联 PersonalWorldID，但不得复制或改变 WorldOwnerID、PlayerID、持久 revision。

选择独立 package 而不是给 `personalworld.World` 增加 online/instance/lease 字段，是因为运行承载可以休眠、重建和迁移，而持久世界 identity 必须保持不变。也不创建通用 infrastructure placement interface；接口由消费语义 owner `placement` 定义，Redis adapter 在 W2 实现。

### 2. Current assignment 只使用 `starting` 与 `active` 两个 phase

Current assignment snapshot 包含 PersonalWorldID、WorldInstanceID、RuntimeNodeID、非零 generation、phase、创建时间，以及 lease 的 fencing token 和绝对 UTC expiry。`starting` 表示已获得独占候选资格但尚未对 gameplay 开放；`active` 表示 runtime 已报告 ready，且仍需通过当前 lease 校验才可写。没有 current assignment 表示世界处于休眠或尚未启动。

Snapshot 的结构有效性与调用时 lease 有效性分开判断。Hydration 要求 expiry 晚于 assignment created time，但允许读取在当前观测时间已经过期的 current snapshot；否则 production adapter 无法恢复原 stamp 供 acquire/revoke 原子替换。过期 snapshot 只能用于条件清理或 replacement，不能 renew、activate 或取得 `WriteFence`。新 candidate 仍必须保证 initial expiry 严格晚于创建时的受信时间。

不增加 `sleeping`、`stopped`、`failed` 或 `migrating` 作为 current phase：这些是无 assignment、操作结果或编排过程，不应成为长期可写状态。已撤销 assignment 只作为结果/审计摘要返回，不能再次 hydrate 为 current。未来需要无中断迁移或副本拓扑时必须通过独立 change 扩展模型。

### 3. PlacementStore 原子线性化 assignment，应用层不使用先查后写

`PlacementStore` 提供 resolve、acquire、activate、renew、revoke/replace 与 qualify fence 的条件操作。Store 必须在单个线性化边界内比较完整 current stamp 并提交结果；并发 `EnsureActive` 只能有一个调用获得新的 `starting` assignment，其他调用返回同一 active assignment 或明确的 in-progress 结果。

完整 stamp 至少包含 PersonalWorldID、WorldInstanceID、RuntimeNodeID、assignment generation 和 fencing token。任何条件写只比较 world ID 或 instance ID 都不足以防止 ABA。Store outcome 必须区分 applied、existing/replay、conflict、not found、not committed 与 commit unknown；application 验证 snapshot/outcome/error 组合，矛盾结果作为 dependency defect fail closed。

相比在 application 内加进程锁，store 线性化契约可被多进程 Redis adapter 正确实现，也能由并发 reference store 验收。Package-level mutex 或单机 registry 不能成为生产正确性的来源。

### 4. Assignment generation 与 fencing token 是不同语义的单调值

Assignment generation 表达客户端、admission 和控制面可观察的 current assignment 版本；每次启动、重建或迁移到新的 WorldInstance 都严格增加。Fencing token 表达持久写入授权序列；每次授予新的写候选资格都必须大于该 PersonalWorld 已发出的任何 token，renew 不改变 token。两者使用不同 Go 类型，禁止互换、从 wall clock 推导或由客户端提供。

当前服务端 v1 中新 assignment 会同时推进 generation 与 fencing token，但仍保持类型和验证分离，避免后续把公开 generation 错当成存储 fence。Production store 在 Redis 丢失、进程重启或 allocator 恢复后仍必须证明值不回退、不复用；若无法取得可信 high-watermark，就必须拒绝新 assignment。具体 durable allocator/epoch 方案由 W2 根据 MySQL/Redis transaction 边界决定。

### 5. Lease 是有界资格，过期后不能续租或复活原 instance

Lease TTL 由受信配置进入 service，并在明确最小/最大范围内校验；deadline 只由注入的服务端 clock 计算。Renew 必须携带完整 current stamp，在 expiry 之前原子延长 deadline，且新 deadline 必须晚于旧 deadline。过期 lease 立即使 `starting` 或 `active` assignment 不可写；旧 token 的延迟 renew 必须失败。

过期 assignment 不允许以相同 WorldInstanceID 或 token 原地复活。后续 resolve/start 必须创建新 instance、generation 与 fence，旧 runtime 即使仍存活也只能被停止。该策略牺牲短暂可用性，但消除了 lease expiry 后的双写窗口和 ABA 恢复。

### 6. `active` 不是可缓存的永久写许可

Runtime 只有在 assignment phase 为 `active`、完整 stamp 等于 store current 且 lease 在 store 使用的当前时间仍有效时才具备候选写资格。Application 可以生成不可变 `WriteFence` 传给后续 world repository，但不得返回或缓存一个脱离 stamp 的布尔“已授权”。

W1 的 `QualifyWrite` 用于验证模型和拒绝 stale caller；W2 的持久 mutation adapter 必须在实际提交边界再次比较当前 fencing token 或等价权威 fence，不能以先检查 Redis、后无条件写 MySQL 伪装原子授权。资格在检查后可能立即失效，因此 transport、handler 或 connection context 都不能成为 write authority。

### 7. Runtime side effect 发生在 store 资格提交之后，active 发布发生在 ready 之后

`Service.EnsureActive` 先原子 acquire `starting` assignment，再调用 `RuntimeController.Start`，runtime ready 后才以完整 stamp activate。Start 失败、调用取消、lease 在 ready 前到期、activate conflict 或明确未提交时，service 使用完整 stamp 尝试条件 revoke 并停止对应 runtime；caller context 已取消或依赖不可用时，清理允许失败并退回 lease expiry/reconciliation。并发观察者对 `starting` 返回 in-progress，不重复启动同一 instance。

Activate 返回 commit unknown 时，service 必须 resolve 并只在精确 stamp 已 active 时报告成功；仍无法确认则返回 phase-aware dependency failure，不以盲目 revoke 破坏可能已经提交的 active assignment。Runtime 即使已经启动也不能自行宣布 active。Runtime controller 的 Start 必须按 WorldInstanceID 幂等，Stop 必须比较完整 assignment stamp，避免响应丢失创建第二个 runtime 或让 predecessor 停止 successor。

### 8. 休眠、重建和迁移采用 revoke-before-stop 与 break-before-make

Sleep 必须以 expected current stamp 原子撤销 assignment，再停止 runtime；Stop 失败不能恢复已撤销 fence，调用结果必须表明 placement 已提交但 cleanup 失败。重复 sleep 若确认目标 stamp 已不存在可返回 replay，携带 stale stamp 试图撤销较新的 assignment 则返回 conflict。

Rebuild 与 migration 共用 replace 原语：调用方提供 expected current stamp、预生成的新 WorldInstanceID 和目标 RuntimeNodeID；store 原子撤销 predecessor 并创建 generation/fence 更大的 `starting` successor。旧实例从该线性化点起失去写资格；随后停止旧 runtime、启动并 activate successor。相同 expected stamp、successor ID 与 target node 构成稳定重试 identity；store replay 返回首次提交的完整 successor，application 验证稳定 identity 与 generation/fence 单调性，不把重试时重新计算的 created time/expiry 当作首次提交值。由此响应丢失不会生成多个 successor。

该 break-before-make 设计允许短暂不可用，不尝试在 W1 解决 snapshot catch-up 或双活 cutover。相比先启动可写 successor 再撤销 predecessor，它能在没有跨存储事务和复制协议时明确证明最多一个 writer。

### 9. Reference store 与 fake controller 只存在于测试

生产 package 只包含值对象、snapshot/state transition、service 和消费侧 interfaces。并发 reference store、fake clock、fake ID generator、fake runtime controller 与 fault injection 放在 `_test.go`，并覆盖 store 线性化和 impossible outcome 检测。正式 Composition Root 不接线 placement service，也不提供 memory fallback。

手写 Go package、导出声明、业务类型、方法、struct 字段与 interface 方法按项目约定使用中文 Go doc；错误、snapshot 与日志值的默认格式不得泄露内部 node address、完整 fencing material 或 store payload。

## Risks / Trade-offs

- [break-before-make 会造成迁移窗口不可用] → 服务端 v1 优先单写安全；无中断迁移必须等持久 snapshot transfer 与复制模型成立后另行设计。
- [单调 generation/fence 的生产恢复需要持久 high-watermark] → W1 将不回退定义为 store contract；W2 必须选择可证明的 durable allocator/epoch，无法恢复时 fail closed。
- [lease 过短会抖动，过长会延迟故障接管] → 使用受控 TTL/renew cadence 配置和 fake-clock boundary tests；具体默认值留给 D0 配置 change。
- [runtime 已启动但 activate 结果未知会留下孤儿进程] → service 先精确 resolve；明确未 active 时按 stamp 清理，仍无法确认时不盲目撤销可能已提交的 active assignment，最终由 lease expiry 和 reconciliation 回收。
- [store check 与 MySQL mutation 之间仍可能竞态] → W1 不宣称前置检查足够；W2 必须把 fence enforcement 接入持久提交边界并提供 duplicate-instance tests。
- [当前模型不保留 terminal assignment history] → 审计由结构化事件或未来持久审计 owner 承担，current store 不被历史记录拖成事实数据库。

## Migration Plan

1. 新增纯 Go `internal/placement` package 与测试，不修改 production Composition Root，因此部署和公开协议行为保持不变。
2. 通过 format、unit/fuzz/race、vet、注释与日志安全、OpenSpec strict 和仓库卫生验证后归档本 change。
3. D0 先提供 MySQL/Redis runtime；W2 再实现 generation/fence 持久 high-watermark、assignment/lease Redis adapter、reconciliation 与持久 mutation fence enforcement。
4. VisitSession、world protocol 和 admission 只能消费已冻结的 assignment stamp，不得重新解释或绕过 placement write qualification。

回滚只需移除尚未接线的 package；本 change 不产生数据库、Redis、网络或客户端迁移。

## Open Questions

本 change 没有阻塞实现或归档的待决问题。生产 lease TTL、renew cadence、durable fence allocator、跨节点 runtime RPC、snapshot transfer 与无中断迁移均由后续 D0/W2 或独立 change 依据真实 adapter 和可用性目标决定。
