## ADDED Requirements

### Requirement: PersonalWorld identity 与 owner 必须稳定且不可混用
服务端 MUST 使用独立、受校验且不可由客户端选择的 PersonalWorldID 标识个人持久世界，并 MUST 直接使用 account owner 提供的有效 PlayerID 作为 immutable WorldOwnerID。PersonalWorldID、PlayerID、未来 WorldInstanceID 与 ActivityInstanceID MUST NOT 相互转换或替代；PersonalWorld MUST NOT 提供 owner transfer。

#### Scenario: 创建 primary PersonalWorld
- **WHEN** application 为有效 PlayerID 创建个人世界
- **THEN** 世界获得独立 PersonalWorldID，WorldOwnerID 固定为该 PlayerID，且后续 lifecycle mutation 不改变 owner

#### Scenario: Hydration 混用实体 ID
- **WHEN** repository snapshot 使用 PlayerID、错误前缀或零值充当 PersonalWorldID
- **THEN** hydration 失败且 snapshot 不进入 application 或 domain mutation

### Requirement: 每个 Player 最多拥有一个 primary PersonalWorld
PersonalWorldRepository MUST 使用 PlayerID owner 唯一约束原子线性化 primary world 创建。`EnsurePrimaryWorld` MUST 对重复和并发调用幂等：首次调用创建初始世界，后续调用返回同一已提交 PersonalWorldID 和 snapshot，不得先查询后插入或提交多个 primary world。Application MAY 为单次尝试构造未提交候选 value，但候选值在 `created` outcome 前 MUST NOT 被视为持久事实。

#### Scenario: 并发创建同一 Player 的世界
- **WHEN** 多个 goroutine 同时为同一 PlayerID 调用 `EnsurePrimaryWorld`
- **THEN** 最多一个 primary world 被提交，所有成功调用返回同一 PersonalWorldID、owner 和初始事实

#### Scenario: 创建结果无法确认
- **WHEN** repository 因 deadline 或连接故障无法确认 primary world transaction 是否提交
- **THEN** application 返回 commit-unknown dependency failure，不提交第二个 primary world、不声称未提交，也不执行跨存储补偿删除

### Requirement: PersonalWorld snapshot 必须严格构造和 hydration
新建 PersonalWorld MUST 以 `active` lifecycle、revision `1` 和非零 UTC created time 开始。Hydration MUST 验证 PersonalWorldID、owner、lifecycle、revision 与 created time 的完整性；未知 lifecycle、revision `0`、零时间或不完整 snapshot MUST fail closed。Aggregate 字段 MUST 保持私有，adapter 不得通过 struct literal 或可变集合绕过不变量。

#### Scenario: Hydrate 合法 snapshot
- **WHEN** repository 返回有效 ID、owner、封闭 lifecycle、正 revision 和非零 created time
- **THEN** domain 恢复等价 PersonalWorld value，所有 accessor 返回不可变值副本

#### Scenario: Hydrate malformed snapshot
- **WHEN** snapshot 缺少 owner、使用未知 lifecycle、revision 为零或 created time 为空
- **THEN** constructor/hydration 拒绝该值且不产生部分有效 aggregate；若该值来自 repository，application 将其映射为稳定 dependency failure

### Requirement: PersonalWorld lifecycle 必须只表达持久存在性
PersonalWorld lifecycle MUST 只包含当前确认的 `active` 与 terminal `archived` 状态。世界创建后为 active；只有 immutable Owner 可以请求 `active -> archived`，archived world MUST NOT 恢复或接受后续 mutation。WorldInstance loading、online、sleeping、migration、lease 与 connection presence MUST NOT 进入 PersonalWorld lifecycle。

#### Scenario: Owner 归档 active world
- **WHEN** 可信 actor 等于 WorldOwnerID，world 为 active 且 mutation 通过 revision/idempotency 校验
- **THEN** world 转为 archived、owner 保持不变且 revision 精确增加一

#### Scenario: Visitor 或其他 Player 归档世界
- **WHEN** actor PlayerID 不等于 WorldOwnerID
- **THEN** application 返回 forbidden，lifecycle 与 revision 保持不变

#### Scenario: 已归档世界再次 mutation
- **WHEN** 新 idempotency key 请求修改 archived world
- **THEN** application 返回 invalid-state 且不会把 archived 解释为 WorldInstance 休眠或可恢复状态

### Requirement: 世界 mutation 必须同时使用 expected revision 与幂等 identity
每个持久 PersonalWorld mutation MUST 携带大于零的 expected revision 和有界安全 idempotency key。Repository MUST 以可信 actor PlayerID 与 key 组成幂等索引作用域，并在同一原子提交中比较 revision、记录 command fingerprint、提交目标 snapshot 并保存结果。成功 mutation MUST 将 revision 精确增加一；stale revision 不得写入。

#### Scenario: 两个请求竞争同一 revision
- **WHEN** 两个不同 idempotency key 都以同一 expected revision 修改同一 active world
- **THEN** 最多一个请求提交，另一个收到 revision conflict，最终 revision 只增加一次

#### Scenario: 相同命令重试
- **WHEN** 相同 idempotency key 与相同 command fingerprint 在响应丢失后重试
- **THEN** repository 返回第一次提交的 snapshot 和结果，不重复执行 mutation 且不再次增加 revision

#### Scenario: 重用 key 提交不同命令
- **WHEN** 同一已通过 Owner authorization 的 actor scope 内，相同 idempotency key 被用于不同 world、expected revision 或目标状态
- **THEN** repository 返回 idempotency conflict，任何世界事实均不改变

#### Scenario: 不同 Owner 使用相同文本 key
- **WHEN** 两个已通过各自 Owner authorization 的 Player 在各自世界使用相同文本 idempotency key
- **THEN** repository 按 actor scope 独立决议，两次合法 mutation 不会因跨 Owner key 碰撞而互相阻塞

### Requirement: Repository 结果必须区分业务冲突与提交不确定性
PersonalWorldRepository MUST 明确表达 created/existing、not found、revision conflict、idempotency replay/conflict、not committed 与 commit unknown，不得只以模糊 error 推断事务结果。Application MUST 验证 outcome、snapshot、revision、owner 与 error 的组合，malformed 或矛盾结果 MUST 作为 dependency defect fail closed。

#### Scenario: Repository 返回 stale revision
- **WHEN** repository 确认 expected revision 已过期且没有提交 mutation
- **THEN** application 返回稳定 revision conflict，不将其伪装为 dependency failure 或自动覆盖最新 snapshot

#### Scenario: Mutation commit unknown
- **WHEN** repository 不能确认 mutation transaction 是否提交
- **THEN** application 返回 commit-unknown phase，调用方只能使用相同 idempotency identity 查询或重试，不得生成新 key 后盲目补写

#### Scenario: Repository 返回矛盾结果
- **WHEN** repository 声称成功但返回错误 owner、未递增 revision、无效 lifecycle 或同时返回 dependency error
- **THEN** application 返回 dependency defect，不向上游暴露伪成功 snapshot

### Requirement: PersonalWorld 必须保持窄 aggregate 所有权
PersonalWorld MUST 只拥有 world identity、immutable owner、持久 revision、created time 与粗粒度 lifecycle。PlayerState、地图、任务、探索、家园、资产、奖励、ActivityInstance、VisitSession、WorldInstance、placement、socket 和 connection presence MUST NOT 存入通用 PersonalWorld blob、万能 map 或未定义 extension field。

#### Scenario: 提议把运行实例状态写入 PersonalWorld
- **WHEN** implementation 尝试向 PersonalWorld aggregate 增加 endpoint、lease、online players 或 connection binding
- **THEN** 评审必须拒绝该所有权，把运行承载交给后续 WorldInstance/placement owner

#### Scenario: 提议顺序双写世界与玩家奖励
- **WHEN** PersonalWorld mutation 同时要求修改 Visitor 或 Owner 的 player ledger
- **THEN** 本 change 不提供伪原子双写，后续子领域必须明确 mutation owner、settlement owner、idempotency 和 transaction/outbox 边界

### Requirement: PersonalWorld core 必须在无基础设施环境独立验收
PersonalWorld domain/application MUST 只依赖消费侧定义的 repository、clock 和 ID generator，以及 account owner 的 PlayerID。测试 MUST 使用 deterministic fakes 与并发 reference repository，不启动 listener、MySQL、Redis、Docker，不依赖 generated protocol type。正式 Composition Root MUST NOT 注入测试 repository 或提前声明 PersonalWorld API 可用。

#### Scenario: 独立运行领域测试
- **WHEN** 测试创建、hydration、owner authorization、revision competition、idempotency replay 和 commit unknown
- **THEN** 测试只构造纯 Go 对象并能在 unit、fuzz 与 race detector 下验证所有并发路径

#### Scenario: 正式服务端启动
- **WHEN** production storage、placement 和公开 adapter 尚未完成对应 change 与验收
- **THEN** 当前进程不创建 PersonalWorld service production graph、不开放 world listener 或 handler，也不使用 memory fallback
