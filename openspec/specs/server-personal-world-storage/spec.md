# Server PersonalWorld Storage 规格

## Purpose

定义 PersonalWorld 持久事实、幂等 lifecycle mutation、placement allocation high-watermark、Redis assignment/replay 状态机及跨存储不确定结果解析的长期行为边界。

## Requirements

### Requirement: PersonalWorld 持久 schema 必须由 MySQL adapter 明确拥有
系统 MUST 使用规范化 MySQL table 保存 PersonalWorld identity、immutable owner、封闭 lifecycle、正 revision 与 UTC 微秒 created time，并以 owner 唯一约束原子保证每个 Player 最多一个 primary world。Adapter-facing absolute time MUST 在首次提交前规范为 schema 可表达的 UTC 微秒，确保首次结果与后续 hydration/replay 精确相等。Idempotency replay MUST 使用独立 table 保存 actor-scoped key digest、command fingerprint 和完整结果投影；原始 idempotency key、运行 assignment、Visitor、地图、任务、奖励或通用 blob MUST NOT 进入这些表。

#### Scenario: 空库应用 PersonalWorld migrations
- **WHEN** storage migrator 对空数据库执行当前 catalog
- **THEN** 它按不可变顺序创建带 owner/唯一约束的 PersonalWorld 与 idempotency table，重复启动不修改已应用 schema 或提前创建其他业务表

#### Scenario: Hydration 读取非法持久行
- **WHEN** 数据行包含未知 lifecycle、零 revision、错误 ID namespace、空 owner 或非法 UTC time
- **THEN** adapter 通过领域 constructor fail closed，返回稳定 dependency defect 且不构造部分有效 PersonalWorld

### Requirement: PersonalWorld repository 必须线性化 primary world 创建与读取
MySQL adapter MUST 实现现有 `PersonalWorldRepository`，使用 owner unique constraint 作为 `EnsurePrimary` 的线性化边界。并发 duplicate key MUST 解析已提交的同 owner world；candidate ID 与其他 owner 冲突、结果 owner/identity 不一致或 malformed row MUST 作为 dependency defect。Transaction commit、deadline 或连接中断无法证明是否提交时 MUST 返回 `commit-unknown`，不得自动生成第二个 candidate 或删除可能已提交的 row。

#### Scenario: 两个进程并发创建同一 Owner 的 primary world
- **WHEN** 两个 adapter instance 使用不同 candidate 同时调用 `EnsurePrimary`
- **THEN** MySQL 最多提交一个 owner row，所有确定成功调用返回同一 snapshot，失败调用不会提交第二个 world

#### Scenario: Ensure commit 响应丢失
- **WHEN** insert transaction 的 commit 结果因连接中断无法确认
- **THEN** adapter 返回零 snapshot、`commit-unknown` 与安全 dependency error，调用方只能用原 candidate/owner 解析而不能换 ID 盲目补写

#### Scenario: 按 ID 查询不存在的世界
- **WHEN** `FindByID` 对有效但未持久化的 PersonalWorldID 查询
- **THEN** adapter 返回严格零 snapshot 与 `not-found`，不会把依赖故障或 malformed row 伪装成不存在

### Requirement: Archive mutation 必须原子保存 revision 与幂等 replay
`CommitArchive` MUST 在单一 MySQL transaction 内按 `(actor, idempotency key digest)` 决议 replay/conflict，锁定目标 world，比较 immutable owner、expected revision 和 active lifecycle，并原子更新 archived snapshot 与 replay result。相同 fingerprint MUST 返回首次结果且不再次增加 revision；相同 key 的不同 fingerprint、stale revision、not-found 与 invalid-state MUST 返回现有领域 outcome 且不写入世界事实。Callback 或 commit MUST NOT 自动重放。

#### Scenario: 相同 archive command 响应丢失后重试
- **WHEN** 相同 actor、key 和 fingerprint 在首次 transaction 已提交后再次执行
- **THEN** adapter 返回保存的 replay snapshot/result，revision 只增加一次且不重新运行 mutation

#### Scenario: 两个 key 竞争同一 revision
- **WHEN** 两个合法 archive record 使用不同 key 但相同 expected revision 并发提交
- **THEN** 最多一个 transaction applied，另一个返回 revision conflict 或观察 terminal invalid-state，最终 world revision 只推进一次

#### Scenario: Archive commit 无法确认
- **WHEN** world update 与 replay result 已发送 commit 但 driver 无法确认结果
- **THEN** adapter 返回 `commit-unknown`，保留原 actor/key/fingerprint 作为唯一解析 identity，不执行跨存储补偿删除

### Requirement: Placement generation 与 fencing allocation 必须持久且永不复用
Placement adapter MUST 在 MySQL 中按 PersonalWorld 原子维护 generation/fencing high-watermark，并为每个候选 WorldInstance 追加不可变 allocation。每次新 Acquire/Replace reservation MUST 同时获得严格更大的正 generation 与 fencing token；已分配数字和 WorldInstanceID MUST NOT 因 Redis failure、flush、expiry、revoke、application rollback 或 cleanup 被删除后复用。溢出、历史冲突、sequence/allocation 不一致或 MySQL 不可用 MUST fail closed。

#### Scenario: Redis flush 后分配新 assignment
- **WHEN** Redis current/replay key 全部丢失但 MySQL allocation history 仍存在，service 使用新 WorldInstanceID acquire
- **THEN** 新 reservation 的 generation 与 fence 严格大于该 world 历史所有值，旧 runtime 不能因 Redis 为空而从 1 重新取得资格

#### Scenario: 相同 candidate 重复请求 allocation
- **WHEN** 调用方因不确定结果以相同 world、instance 和 node 重试，但重新读取的当前时间与临时候选 created time/expiry 已推进
- **THEN** adapter 恢复首次 allocation 及其结果时间或返回冲突/不确定，不为同一 WorldInstanceID 分配第二组 generation/fence

#### Scenario: Allocation 后 Redis 明确未应用
- **WHEN** MySQL reservation 已提交而 Redis transition 明确拒绝
- **THEN** adapter 保留已烧掉的 allocation 并返回未提交/冲突证据，不回收或递减 high-watermark

### Requirement: Redis current assignment 必须通过有界 schema 与原子 script 管理
Placement adapter MUST 登记 owner 为 `placement` 的 assignment 与 transition replay definitions，并使用确定性 `ih:<env>:placement:<kind>:<identity...>` key。Current assignment MUST 保存 schema version、完整 stamp、starting/active phase、UTC created time 与 lease expiry，并设置 TTL；每个 Acquire、Activate、Renew、Revoke、Replace 与 QualifyWrite MUST 由 owner script 原子比较所需完整字段。Unknown schema、malformed/oversized value、缺失 required TTL、stale stamp、到期 lease 或非法 phase MUST fail closed，script MUST NOT 依赖 client mutation retry、`KEYS` 扫描或 Lua double 表示 uint64 fence。

#### Scenario: Starting assignment 原子 activate
- **WHEN** script 观察到完整 current starting stamp 匹配且 `observedAt` 严格早于 expiry
- **THEN** 它只把相同 assignment 转为 active，generation/fence/created time/expiry 保持不变并返回完整 snapshot

#### Scenario: Stale predecessor 延迟 revoke
- **WHEN** current 已是更高 generation 的 successor，而旧请求携带 predecessor 完整 stamp
- **THEN** script 返回 conflict，既不删除 successor，也不写入可误报旧 revoke 成功的 replay 结果

#### Scenario: UInt64 边界进入 Lua
- **WHEN** generation、fence 或 microsecond timestamp 超过 IEEE-754 精确整数范围
- **THEN** codec/script 仍以 canonical decimal string 做精确相等和次序判断，不截断、舍入或接受错误 stamp

### Requirement: Assignment TTL、replay 与写资格必须保持有界
Assignment Redis key MUST 以 lease expiry 设置绝对 TTL，并在 value/script 中使用精确 absolute expiry 再次判断；Redis 毫秒 TTL 的向上取整 MUST NOT 延长领域写资格。Transition replay MUST 绑定 operation 与稳定 command fingerprint，使用覆盖有界 retry window 的正 TTL，并只返回首次稳定结果；每次重试重新读取的 `observedAt` MUST NOT 改变 fingerprint，Renew 的目标 expiry 等真实 command 字段仍 MUST 参与。只有 current active、lease 未过期且完整 stamp 匹配时 `QualifyWrite` 才能返回 point-in-time `WriteFence`；不存在 assignment、starting、expired 或 stale request MUST 返回严格零 fence。

#### Scenario: Redis key 尚存但领域 lease 已到期
- **WHEN** assignment key 因毫秒向上取整仍存在，而受信 `observedAt` 已等于或晚于 value 中的精确 expiry
- **THEN** renew、activate 与 qualify 全部返回 expired/stale，不因物理 key 尚存延长资格

#### Scenario: Revoke 响应丢失后重试
- **WHEN** 首次 revoke 已删除 current 并写入未过期 replay record，调用方复用同一完整 request
- **THEN** adapter 返回首次 revoke 结果且不会误伤随后创建的 successor

#### Scenario: Replay record 已自然过期
- **WHEN** 极晚重复请求到达且既无 current 匹配也无 replay 证据
- **THEN** adapter 返回 not-found/conflict/commit-unknown 中有证据的保守结果，不猜测历史操作成功

### Requirement: 跨 MySQL/Redis 不确定结果必须使用稳定 identity 解析
新 assignment 发布 MUST 固定为先提交 MySQL allocation、再执行 Redis transition。只有本次新建 allocation 可以首次发布；既有 allocation 在 Redis 中缺少精确 current/replay 证据时 MUST NOT 被重新发布。Redis response loss 后 adapter MUST 以相同 candidate/stamp/transition digest 解析 current/replay；精确匹配 MAY 收敛为 replay，明确不同 current MUST 返回 conflict，其余 MUST 保持 commit-unknown。系统 MUST NOT 删除 allocation、静默后台重放或把 MySQL/Redis 顺序操作宣称为分布式 transaction。

#### Scenario: Acquire script 已执行但 response 丢失
- **WHEN** Redis 已保存 candidate current assignment，但 client 在读取 reply 前断开
- **THEN** adapter 以相同 candidate 解析到精确 current 后返回 replay/in-progress，不创建第二个 instance 或 allocation

#### Scenario: Response 丢失后又发生 Redis flush
- **WHEN** 原 candidate 已有 MySQL allocation，但 current/replay Redis 证据均已丢失
- **THEN** 相同 candidate 保持 commit-unknown 且不会被重新发布，后续新 candidate 必须取得更高 generation/fence

#### Scenario: Replace 响应丢失后重试时钟推进
- **WHEN** 首次 Replace 已提交 successor，而调用方复用相同 predecessor、successor WorldInstanceID 与 node 重试，但 `observedAt` 和临时候选时间已经推进
- **THEN** adapter 从 MySQL allocation 与 Redis current/replay 恢复首次 successor snapshot 并返回 replay，不把它误判为 stale conflict 或创建第二个 allocation

#### Scenario: MySQL allocation 无法提交
- **WHEN** adapter 无法取得或确认新 allocation
- **THEN** 它不执行 Redis transition、不产生 current assignment，并按已有 store outcome 返回 not-committed 或 commit-unknown

### Requirement: Adapter 边界必须保持可观测、可恢复且不提前接线业务
PersonalWorld/Placement adapter MUST 复用 storage runtime 共享 clients、transaction/error policy 和低基数 observability，不得创建独立 pool/client、lifecycle component 或 memory fallback。默认错误、日志和 metrics MUST NOT 包含 SQL、参数、完整 key/value、idempotency key、fencing token 或 player/world/instance identity。正式 Composition Root 在 RuntimeController、上层 service 与 transport 能力就绪前 MUST NOT 构造未使用的 PersonalWorld/Placement service graph 或开放 world API；storage migrator MAY 正常创建这些 adapters 拥有的 schema。

#### Scenario: 正式服务端应用新 migrations
- **WHEN** 新二进制启动且 storage runtime/migrations 成功，但业务 service 与 transport 尚未接线
- **THEN** diagnostic/storage graph 可以 ready，PersonalWorld/Placement table 存在但没有 world endpoint、后台 placement task 或 fake adapter 被注入

#### Scenario: Storage operation 返回敏感底层错误
- **WHEN** driver/client error 包含 endpoint、SQL、key 或参数片段
- **THEN** 普通日志只记录固定 adapter/operation/outcome，metrics 保持低基数，底层 cause 仅能通过受控 error chain 诊断

#### Scenario: Docker recovery 验收
- **WHEN** storage verify 在隔离 MySQL/Redis 中运行并注入并发、response loss、restart 与 flush
- **THEN** PersonalWorld 持久事实和 allocation high-watermark 可从 MySQL 恢复，Redis 只重建新的可失效运行态，测试结束后 harness 按 ownership 有界清理

### Requirement: Runtime-originated world state 必须由真实 owner 与最终 commit gate 管理
PersonalWorld ensure/archive SHALL 继续作为 Owner 授权、revision/idempotency 保护的控制面 lifecycle transaction，并允许在没有 WorldInstance 时执行；它们 MUST NOT 被错误包装为 runtime gameplay mutation。Storage adapter MUST NOT 创建尚无 domain owner 的通用 fenced-write table/API。未来任何由 WorldInstance 提交的地图、任务、探索或其他持久 world state MUST 通过独立 capability 定义实际 mutation owner，并在最终 MySQL commit boundary 重新验证 point-in-time `WriteFence`，不能只依赖曾经成功的 QualifyWrite。

#### Scenario: Owner 在世界休眠时 archive
- **WHEN** PersonalWorld 没有 current assignment，而 Owner 使用合法 revision/idempotency archive
- **THEN** MySQL repository 可以提交 lifecycle transaction，不要求伪造 placement fence 或启动 runtime

#### Scenario: 提议增加通用 world blob 写入
- **WHEN** implementation 尝试用这些 adapters 保存无 owner 地图/任务/奖励 payload，或只凭一次 qualification 永久授权写入
- **THEN** 评审必须拒绝该实现，并要求真实子领域与最终 fence commit gate 通过独立 OpenSpec change 定义
