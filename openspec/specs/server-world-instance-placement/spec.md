## Purpose

定义 PersonalWorld 运行实例的服务端 placement 核心契约，包括 assignment identity、lease fencing、两阶段发布、写资格、休眠与迁移，以及无基础设施环境下的独立验收边界。

## Requirements

### Requirement: Placement identity 必须与持久世界和网络地址分离
Placement MUST 直接使用 `personalworld.PersonalWorldID` 标识被承载的持久世界，并 MUST 为每次运行承载使用独立、不可变且严格校验的 `WorldInstanceID`。Placement MUST 使用受信 `RuntimeNodeID` 表达服务端运行节点，但 RuntimeNodeID 与 WorldInstanceID MUST NOT 包含或重新解释 PlayerID、WorldOwnerID、PersonalWorld revision、URL、端口或客户端 endpoint。

#### Scenario: 同一世界重建运行实例
- **WHEN** 一个 PersonalWorld 的旧 runtime 被撤销后重新启动
- **THEN** PersonalWorldID 与 WorldOwnerID 保持不变，新 assignment 使用不同 WorldInstanceID，且运行位置不写入持久世界 identity

#### Scenario: 客户端提交运行位置
- **WHEN** 不可信输入携带 WorldInstanceID、RuntimeNodeID 或 endpoint 并试图选择写入目标
- **THEN** placement 只使用服务端解析的 current assignment，不让 payload identity 覆盖受信 placement context

### Requirement: Current assignment snapshot 必须严格构造和 hydration
Current assignment MUST 包含有效 PersonalWorldID、WorldInstanceID、RuntimeNodeID、非零 assignment generation、封闭 phase、非零 UTC created time，以及有效 lease fencing token 与绝对 UTC expiry。Current phase MUST 只包含 `starting` 和 `active`；未知 phase、零 generation、零 token、expiry 不晚于 created time、字段不完整或相互矛盾的 snapshot MUST fail closed。新 assignment 的 initial expiry MUST 严格晚于创建时的受信观测时间；storage hydration MAY 恢复结构合法但在当前观测时间已经过期的 snapshot，使 application 能以原 stamp 原子替换或撤销它，但该 snapshot MUST NOT 续租、activate 或取得写资格。没有 current assignment MUST 表示世界尚未启动或已休眠，而不是构造 `sleeping` assignment。

#### Scenario: Hydrate 合法 starting assignment
- **WHEN** store 返回字段完整、generation 与 token 非零、lease 尚未过期的 `starting` snapshot
- **THEN** placement 恢复等价不可变值，且该 snapshot 在 activate 前不具备 gameplay 写资格

#### Scenario: Hydrate 已过期 current assignment
- **WHEN** store 返回 created time 与 expiry 顺序合法、但 expiry 等于或早于当前受信观测时间的 current snapshot
- **THEN** placement 恢复其完整 stamp 供条件替换或撤销，同时拒绝 renew、activate 与 write qualification，不把过期值误报为 malformed dependency result

#### Scenario: Hydrate malformed assignment
- **WHEN** snapshot 使用未知 phase、零 generation、expiry 不晚于 created time、错误 ID 前缀或缺少 node identity
- **THEN** constructor/hydration 拒绝该值且 application 将 repository 结果视为 dependency defect，不产生部分有效 assignment

### Requirement: 每个 PersonalWorld 最多只能有一个 current assignment
PlacementStore MUST 以 PersonalWorldID 为线性化作用域原子 acquire、replace 与 revoke current assignment。并发启动 MUST 最多创建一个 `starting` assignment；current active assignment 存在且 lease 有效期间，`EnsureActive` MUST 返回同一 assignment，不得创建第二个 WorldInstance。每个新 assignment 的 generation MUST 严格大于该 PersonalWorld 已发出的所有 generation，且已撤销 generation MUST NOT 被复用。

#### Scenario: 并发按需启动同一世界
- **WHEN** 多个 goroutine 同时对没有 current assignment 的同一 PersonalWorld 调用 `EnsureActive`
- **THEN** 只有一个调用原子获得新 `starting` assignment，其他调用观察同一 active 结果或明确 in-progress，且 runtime controller 不会启动第二个 instance

#### Scenario: 已有 active assignment
- **WHEN** `EnsureActive` 解析到 lease 有效的 current active assignment
- **THEN** service 返回该 assignment，不推进 generation、不续造 instance，也不再次调用 runtime start

#### Scenario: 生成值的可信高水位不可用
- **WHEN** store 在进程重启或运行态丢失后无法证明下一 generation 大于历史已发出值
- **THEN** acquire/replace fail closed，且不得从 1 或任意猜测值重新发号

### Requirement: Lease 与 fencing token 必须提供有界且不可复活的写者资格
每个 current assignment MUST 持有服务端 clock 计算的有界 lease。Fencing token MUST 按 PersonalWorld 严格单调增加，MUST NOT 从 wall clock、客户端输入或可回退状态直接推导；每次授予新的 assignment 写候选资格都必须产生大于历史值的 token，renew MUST 保持原 token。Renew MUST 原子比较完整 current stamp，并只允许在当前 expiry 之前把 deadline 推进；到期 token、已撤销 token 或 stale stamp MUST NOT 被续租或复活。

#### Scenario: 合法续租 active assignment
- **WHEN** current holder 在 expiry 前携带完整匹配的 world、instance、node、generation 和 fencing token 续租
- **THEN** store 原子延长 deadline，generation 与 fencing token 保持不变

#### Scenario: 续租请求到达 expiry 边界
- **WHEN** store 的受信当前时间等于或晚于 lease expiry
- **THEN** renew 返回 expired/stale 结果，不延长 deadline，也不恢复原 WorldInstance 的资格

#### Scenario: Lease 到期后重新承载
- **WHEN** expired assignment 被后续 `EnsureActive` 替代
- **THEN** replacement 使用新的 WorldInstanceID、更大的 generation 和更大的 fencing token，旧 runtime 的延迟 renew 与写资格校验均失败

#### Scenario: Fencing 高水位无法恢复
- **WHEN** placement runtime state 丢失且 store 无法证明下一 token 大于任何历史已发出 token
- **THEN** store 拒绝授予新 lease，直到可信高水位恢复，不得冒险复用 token

### Requirement: Runtime ready 与 active 发布必须分成两个阶段
`EnsureActive` MUST 先提交 `starting` assignment，再以该完整 stamp 调用幂等 runtime start；只有 runtime 明确 ready 后，store 才能条件 transition 到 `active`。Activate MUST 原子确认 assignment 仍为 current、phase 为 starting 且 lease 未过期。Runtime start 失败、调用取消、lease 过期、activate conflict 或明确未提交的 activate dependency failure MUST NOT 产生 active assignment；service MUST 以完整 stamp 尝试条件 revoke 并停止对应 runtime。若 caller context 已取消、清理依赖失败或提交状态仍未知，starting assignment 仍不可写，并由 lease expiry 与后续 reconciliation 提供最终回收边界。

#### Scenario: Runtime 成功启动
- **WHEN** service 获得 starting assignment、runtime controller 对相同 WorldInstanceID 报告 ready，且条件 activate 提交成功
- **THEN** current assignment 转为 active，generation 与 fence 不变，并可进入当前写资格校验

#### Scenario: Runtime 启动失败
- **WHEN** store 已提交 starting assignment 但 runtime start 返回失败
- **THEN** service 不报告 active，按完整 stamp 尝试 revoke；若清理结果未知，该 assignment 仍不能写并最终受 lease expiry 限制

#### Scenario: Ready 回调迟于 replacement
- **WHEN** 旧 runtime 的 ready 结果在 current assignment 已被替换后到达
- **THEN** activate 因 stamp 不匹配被拒绝，旧 runtime 不能覆盖 successor 或取得 gameplay 写资格

### Requirement: 写资格必须每次绑定完整 current stamp
Placement MUST 只在 assignment 为 current active、lease 未过期且 PersonalWorldID、WorldInstanceID、RuntimeNodeID、generation 与 fencing token 全部匹配时返回不可变 `WriteFence`。Qualification MUST NOT 接受只含 world、instance 或 generation 的部分 identity，也 MUST NOT 返回可跨 assignment 复用的永久布尔许可。连接身份、客户端 payload、旧 endpoint 或曾经成功的 qualification MUST NOT 恢复已失效写者。

#### Scenario: Current active holder 请求资格
- **WHEN** runtime 使用完整 current stamp 在 lease 有效期内调用 write qualification
- **THEN** placement 返回绑定同一 world、instance、node、generation 与 fencing token 的 WriteFence

#### Scenario: 旧实例在迁移后继续写
- **WHEN** predecessor 使用旧 instance、generation 或 fencing token 请求 qualification，即使其进程和旧连接仍存活
- **THEN** placement 返回 stale/conflict 且不因旧 endpoint、session 或 payload identity 恢复资格

#### Scenario: Qualification 后 lease 失效
- **WHEN** 调用方曾获得 WriteFence，但在实际 mutation 之前 assignment 被撤销或 lease 到期
- **THEN** 既有 qualification 不得被解释为永久授权，调用方必须把完整 fence 交给后续持久提交边界重新验证

### Requirement: 休眠、重建和迁移必须先撤销旧写者
Sleep、Rebuild 与 migration MAY 在 current lease 有效时请求 runtime 执行有界 drain，使其停止接收新 simulation input、完成已接纳 Tick 并提交或终结有界 ResultProposal；drain 不得续租、授予新写资格或成为撤销前置的无限等待。Drain 成功、失败、调用取消或 deadline 到期后，Sleep MUST 仍以 expected current stamp 原子 revoke assignment，再停止 runtime；runtime stop 失败 MUST NOT 恢复 lease。Rebuild 与 migration MUST 以 expected current stamp、预生成 successor WorldInstanceID 和目标 RuntimeNodeID 原子 replace predecessor，先使 predecessor fence 失效，再创建 generation/token 更大的 starting successor；predecessor stop 必须发生在 revoke/replace 线性化点之后。相同 expected stamp、successor WorldInstanceID 与目标 RuntimeNodeID 的不确定结果重试 MUST 返回首次提交的同一 assignment；重试时重新读取的当前时间不得被当作首次 `createdAt` 或 initial expiry 的精确 replay 字段。Stale expected stamp MUST NOT drain、撤销或停止较新的 current assignment。

#### Scenario: 正常休眠世界
- **WHEN** service 以 current stamp 请求 sleep，runtime 在 deadline 内 drained 且 revoke 提交成功
- **THEN** 世界立即没有 current writable assignment，随后停止 runtime；即使 stop 失败，旧 fence 仍保持失效

#### Scenario: Drain 超时后休眠
- **WHEN** runtime drain 超过 deadline、返回 dependency failure 或 caller 已取消
- **THEN** service 保留可诊断 drain outcome，但仍以受控 cleanup context 尝试 revoke 与 stop；不得为等待 drain 恢复或无限延长旧 lease

#### Scenario: Stale sleep 请求竞争新 assignment
- **WHEN** 延迟 sleep 携带 predecessor stamp，而 current 已是更高 generation 的 successor
- **THEN** service 不向 successor 发送 drain/stop，revoke 返回 conflict，successor 保持 current 且不受影响

#### Scenario: 同节点重建
- **WHEN** service 以 current stamp replace 到同一 RuntimeNodeID 上的新 WorldInstanceID
- **THEN** predecessor 在有界 drain 后先失去写资格，successor 获得更高 generation/fence 并从 starting 重新完成 runtime ready 与 activate

#### Scenario: 跨节点迁移
- **WHEN** service 以 current stamp replace 到不同 RuntimeNodeID
- **THEN** cutover 线性化点之后旧节点无法通过 qualification，新节点在 ready/activate 前也不能写，任意时刻最多一个 active writable instance

#### Scenario: Replace 响应成功后调用方重试
- **WHEN** 调用方因响应丢失复用相同 expected stamp、successor WorldInstanceID 和目标 RuntimeNodeID 再次调用 replace，且 service 的当前时间已经推进
- **THEN** store 返回首次提交的 successor assignment，application 按稳定 identity 与单调 generation/fence 验证 replay，不生成新 successor，也不要求旧 `createdAt` 或 expiry 等于本次重建的候选时间

### Requirement: Placement runtime 接缝必须由远程模拟证据驱动
Production `placement.RuntimeController` MUST 使用已登记 SimulationNode 的跨进程 adapter，不得继续用只记录 map 的 `processWorldRuntime` 宣称 C++ runtime ready。Start MUST 等待绑定完整 starting stamp 的 C++ ready receipt；Drain/Stop MUST 使用完整 stamp，且 missing instance 只有在 status/replay 证明已完成时才能幂等成功。Node selector、runtime adapter 和 SimulationTarget resolver MUST 共享同一 registry snapshot，但 placement store 仍是 current assignment、generation、lease 与 fence 的唯一线性化 owner。

#### Scenario: Production 启动 WorldInstance
- **WHEN** placement 获得 starting assignment 并选择 healthy SimulationNode
- **THEN** RuntimeController 向该 node 启动真实 C++ SimulationInstance，验证 exact ready receipt 后才允许 store activate

#### Scenario: C++ 返回部分 identity
- **WHEN** ready/status/stop receipt 缺少完整 stamp、SimulationInstanceID 或 node incarnation binding
- **THEN** adapter 返回 dependency defect，placement 不发布 active、不构造 target，也不使用进程内 map 补全字段

#### Scenario: 现有 unit test 隔离
- **WHEN** placement domain/application unit、fuzz 或 race tests 运行
- **THEN** 测试仍可注入 deterministic fake RuntimeController 且不启动 C++、listener、MySQL 或 Redis；production Composition Root 不得注入该 fake

### Requirement: Store 结果必须区分冲突、未提交与提交不确定性
PlacementStore MUST 为 acquire、activate、renew、revoke 与 replace 明确表达 applied、existing/replay、in-progress、not found、conflict、not committed 与 commit unknown，不得只从 context error 或模糊 dependency error 猜测是否提交。Application MUST 验证 outcome、snapshot、expected stamp 与 error 的组合；矛盾或 malformed 结果 MUST 作为 dependency defect fail closed。Commit unknown 后只能 resolve 并匹配原 candidate/stamp，不能盲目生成新 successor 或报告成功。

#### Scenario: Acquire commit unknown 后解析到原 candidate
- **WHEN** acquire 响应丢失，但 resolve 返回精确匹配原 WorldInstanceID、node、generation/fence 关系的 current starting 或 active assignment
- **THEN** service 收敛到该 assignment，不创建第二个 candidate

#### Scenario: Replace commit unknown 且无法确认
- **WHEN** replace 返回 commit unknown，而 resolve 既不能确认 predecessor 仍 current，也不能确认原 successor 已成为 current
- **THEN** service 返回 phase-aware dependency failure，不报告迁移成功、不生成新 successor，也不让任一未知 stamp 取得资格

#### Scenario: Store 返回矛盾成功结果
- **WHEN** store 声称 activate applied 却返回不同 world、instance、generation、token 或非 active snapshot
- **THEN** application 返回 dependency defect，不向上游暴露伪成功 assignment

### Requirement: Placement core 必须在无基础设施环境独立验收
Placement domain/application MUST 只依赖消费侧定义的 PlacementStore、RuntimeController、clock 和 ID generator，以及 PersonalWorldID。测试 MUST 使用 deterministic fakes 与并发 reference store，不启动 listener、MySQL、Redis、Docker，不依赖 generated protocol type。正式 Composition Root MUST NOT 注入测试 store、启动 placement 后台任务或开放 world endpoint，直到对应 production storage 与 transport changes 完成。

#### Scenario: 独立运行 placement 测试
- **WHEN** 测试 identity/hydration、并发 ensure、lease boundary、stale renew、ready race、sleep、rebuild、migration、commit unknown 和 malformed store result
- **THEN** 测试只构造纯 Go 对象，并能在 unit、fuzz 与 race detector 下验证已登记的关键并发与失败路径

#### Scenario: 正式服务端启动
- **WHEN** production MySQL/Redis runtime、placement adapter 与公开 world transport 尚未完成对应 change
- **THEN** 当前进程不创建 placement production graph、不使用 memory fallback，也不宣称 PersonalWorld 已可在线承载
