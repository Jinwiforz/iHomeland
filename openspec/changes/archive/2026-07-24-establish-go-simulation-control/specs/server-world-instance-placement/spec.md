## MODIFIED Requirements

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

## ADDED Requirements

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
