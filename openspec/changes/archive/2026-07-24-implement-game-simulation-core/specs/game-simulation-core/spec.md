## ADDED Requirements

### Requirement: C++ 工程与第三方依赖必须可重复恢复

Game Simulation Core MUST 以 `simulation/` 中已跟踪的 CMake source、CMake Presets 和 `versions.yaml` 作为唯一构建事实，并 MUST 通过项目包装入口恢复和校验精确的 compiler、CMake、Jolt Physics、Detour 与 fixture JSON parser 版本、source identity、SHA-256、许可证和 adapter owner。普通 configure/build/test MUST 在依赖恢复完成后离线运行；缺失、漂移或未批准依赖 MUST fail closed，项目 MUST NOT 提交本机 IDE project、绝对路径、第三方缓存或改名复制的上游源码。

#### Scenario: 依赖 archive 与登记 checksum 不一致

- **WHEN** `tools/cpp/` 恢复到的 Jolt、Detour、JSON 或 CMake 资产与 `versions.yaml` 登记的 tag/commit/checksum 任一不一致
- **THEN** restore 在解压和 configure 前失败，不创建或复用部分 build tree，也不回退到系统同名 package

#### Scenario: 精确 MSVC toolset 不存在

- **WHEN** 主机安装了 Visual Studio 或其他 C++ compiler，但找不到 change 锁定的 MSVC component/full version
- **THEN** configure 明确报告 toolchain mismatch 并失败，不使用默认最新 toolset 产生资格 evidence

#### Scenario: 离线重复构建

- **WHEN** 精确依赖已恢复且随后禁用网络，再运行 clean configure、build 与 tests
- **THEN** 所有 source 从 `.local/cpp/` 的已校验缓存解析，构建不访问 package registry、Git host 或浮动 URL

### Requirement: SimulationInstance 必须唯一拥有生命周期与 Tick

每个 `SimulationInstance` MUST 不可变绑定完整 `AssignmentStamp`、独立 `SimulationInstanceID`、mapping generation、model/profile/config/build identity 和稳定 seed。实例 MUST 使用 `Created -> Starting -> Running -> Draining -> Stopped` 的显式生命周期；只有 `Running` 状态的唯一 simulation worker MAY 以 50 ms 固定步长推进 `SimulationTick` 并写 ECS、physics、navigation context、history 与 evidence。部分启动失败 MUST 逆序回滚，drain/stop MUST 有 deadline 和稳定失败原因。

#### Scenario: 旧 assignment 的 command 到达

- **WHEN** command 的任一 WorldInstanceID、generation、lease/fence 或 RuntimeNodeID 与实例不可变 `AssignmentStamp` 不一致
- **THEN** command 在进入 simulation inbox 前以稳定 stale-assignment reason 拒绝，不能推进 Tick、修改 entity 或污染 history

#### Scenario: 启动中 physics adapter 失败

- **WHEN** ECS 与 fixture source 已初始化但 Jolt world 创建失败
- **THEN** 实例按成功初始化栈逆序释放已创建资源并进入 `Failed`，不得留下可推进的 worker 或半初始化 world

#### Scenario: hard Tick debt 或停止 deadline 超限

- **WHEN** 实例达到冻结 hard Tick debt，或 drain 无法在 deadline 内完成
- **THEN** 实例停止接收新 command、保留有界截断 evidence 并以明确失败结束，不无限追帧或静默丢弃 owner 状态

### Requirement: 最小 ECS 必须 generation-safe、有界且单写

Game Simulation Core MUST 提供 per-world generation-safe `EntityID`、有界 component storages、类型安全 component 操作、冻结 systems 所需的最小 views 和 typed deferred structural command buffer。Entity index 复用 MUST 增加 generation，generation wrap 前 MUST retire slot；所有结构变化 MUST 在唯一 barrier 按稳定 tuple 提交。ECS MUST NOT 拥有 socket、协议、配置加载、持久化、自动 replication、反射、archetype graph、query DSL、Job Scheduler、EventBus、Service Locator 或 plugin ABI。

#### Scenario: stale EntityID 在 slot 复用后执行

- **WHEN** entity 已销毁且同一 index 被新 generation 复用，旧 command 随后引用原 `EntityID`
- **THEN** ECS 以稳定 stale-entity reason 拒绝该 command，新 entity 及其 components 保持不变

#### Scenario: System 在 view 迭代中请求销毁

- **WHEN** Death 或 projectile system 在当前 Tick view 迭代期间请求 destroy entity
- **THEN** 请求写入有界 deferred buffer，并只在 `CommitDeferredStructuralChanges` barrier 按规范顺序提交

#### Scenario: Structural buffer 达到容量

- **WHEN** 当前 Tick 的 create/destroy/add/remove 请求超过登记容量
- **THEN** 超限请求按冻结优先级稳定拒绝、记录 capacity token 并触发 overload policy，buffer 不进行无界扩容

### Requirement: 权威 gameplay 必须通过固定单写 pipeline 裁决

每个 Tick MUST 严格按 `DrainInput -> InputIntent -> AIIntent -> AbilityActivation -> Movement -> Physics -> HitDetection -> Effect -> Attribute -> Death -> Replication -> CommitDeferredStructuralChanges` 执行。移动、跳跃、剑 sweep、扇子 projectile、Ability grant/activation、cost/cooldown、Effect stack/expiry、Attribute、damage、death、AI intent 与 authoritative cue source MUST 只由其登记 system owner 修改；客户端式 transform、hit、damage、death、reward 或 payload identity 声明 MUST 被拒绝。

#### Scenario: 合法剑 Ability 激活

- **WHEN** actor 已授予剑 Ability、资源和 cooldown 合法、Tag policy 允许且 input 位于有效 Tick window
- **THEN** AbilityActivation 创建稳定 activation，后续 HitDetection、Effect、Attribute 与 Death 按固定 pipeline 裁决并输出规范事件

#### Scenario: 扇子 projectile 在创建 Tick

- **WHEN** 扇子 Ability 在 Tick T 的 AbilityActivation 请求 projectile spawn
- **THEN** spawn 在 deferred barrier 提交，projectile 最早从 Tick T+1 开始移动，不能在创建 Tick 提前命中

#### Scenario: 输入携带权威结果字段

- **WHEN** command 携带最终位置、命中对象、damage、effect、death、reward 或试图替换绑定 actor identity
- **THEN** command 以稳定 unsafe-payload/identity reason 拒绝，任何 gameplay component 和 evidence secret policy 都不被绕过

### Requirement: 输入、数值和随机行为必须可确定重放

Command MUST 在验证 assignment、mapping generation、actor binding、InputTick window、sequence、expiry 与容量后，按冻结的 `target_tick -> actor_id -> input_tick -> sequence -> kind` tuple 消费。Gameplay 时间、距离、Attribute 与伤害 MUST 使用冻结整数单位、checked arithmetic 和 toward-zero rounding；每个 entity/system random stream MUST 从版本化 seed 独立派生。Canonical 结果 MUST NOT 依赖 arrival order、hash-map/pointer order、thread scheduling、wall clock、locale 或第三方 callback order。

#### Scenario: 相同 command 以不同 arrival order 注入

- **WHEN** 同一合法 command 集以两种不同 arrival order 进入相同 build/config/seed 的实例
- **THEN** 两次运行产生相同规范 command order、state/event/rejection/capacity token 和 canonical digest

#### Scenario: 一个怪物额外消费随机数

- **WHEN** fixture 使怪物 A 的独立 AI stream 多消费一次随机值，而怪物 B 的输入和状态未变
- **THEN** 怪物 B 的 random stream 与规范结果不变，不发生全局 PRNG 串扰

#### Scenario: 算术溢出配置

- **WHEN** Ability/Effect/Attribute 配置可能使登记的 checked integer operation 溢出
- **THEN** 实例在启动前拒绝该配置，不在 Tick 内 wrap、saturate 为隐藏默认值或产生部分 mutation

### Requirement: Physics 与 Navigation 必须保持窄 adapter 边界

Gameplay systems MUST 只依赖项目定义的 `PhysicsWorld` 与 `NavigationWorld` value contracts。Jolt adapter MUST 固定坐标/单位/layer/shape/solver 配置，将 callback 结果复制、量化并按稳定 Collider/Subshape identity 规范排序；Detour adapter MUST 验证版本化 nav asset identity，只返回规范 path/query values。Jolt/Detour types、handles、allocators、status、callbacks 或 container order MUST NOT 出现在 ECS components、gameplay public contracts、fixtures 或 evidence schema。

#### Scenario: 两个 physics hit fraction 相同

- **WHEN** Jolt query 返回量化后 fraction 相同的多个命中且 callback arrival order 不同
- **THEN** adapter 按 `ColliderID`、`SubshapeID` 稳定排序后交给 HitDetection，两次 gameplay 结果一致

#### Scenario: Nav asset digest 漂移

- **WHEN** Detour nav data 的 version、digest、coordinate scale 或登记 map identity 不匹配实例配置
- **THEN** NavigationWorld 初始化失败并使实例启动回滚，不在 Tick 内临时烘焙 Recast 或使用旧 asset

#### Scenario: Live adapter 与记录化 fixture 超出容差

- **WHEN** Jolt/Detour live adapter 对 parity query 返回非有限值、不同规范 hit/path 集或超出登记量化容差
- **THEN** adapter parity gate 失败并定位到具体 query，系统不修改冻结 expected fixture 来适配第三方结果

### Requirement: History 与 replay evidence 必须有界、低敏且不可结算

Game Simulation Core MUST 在每个已提交 Tick 后保存最多 16 Tick 的最小只读 history projection，并 MUST 遵守 8 MiB history target。History query MUST 绑定 current assignment/mapping generation、当前 Tick、允许窗口、actor policy 与 query kind；future、expired、missing 或 stale-generation 查询 MUST 以稳定 reason 拒绝。Replay evidence MUST 绑定 build/compiler/dependency、model/profile/config/nav/physics digest、AssignmentStamp fingerprint、instance、Tick/input range、seed、规范结果 digest、预算和截断原因；它 MUST NOT 包含 credential/key 或被解释为持久事实、奖励或 settlement。

#### Scenario: 合法历史近战查询

- **WHEN** 已认证 actor 的近战 intent 映射到仍在 16-Tick 窗口内的历史 Tick
- **THEN** HitDetection 只读查询该 Tick 的最小 hit projection，并在当前 Tick 的正常 Effect/Attribute pipeline 提交结果，不改写历史

#### Scenario: 查询已淘汰 Tick

- **WHEN** query 指向 ring buffer 已淘汰、缺失或旧 assignment generation 的 Tick
- **THEN** 查询稳定拒绝，不扩张 history、不读取 stale slot，也不接受 command 携带的位置替代

#### Scenario: Evidence 输入包含 secret

- **WHEN** harness input 或 adapter diagnostic 含 raw token、ticket、AEAD key、账号凭据或未登记个人数据
- **THEN** evidence writer 拒绝或清洗该字段并使安全 gate 可诊断，secret 不进入 report、digest source 或日志

### Requirement: 离线 harness 必须完成确定性、预算与安全资格

`ihomeland-sim-server` MUST 作为无 listener 的文件/标准输入离线或进程内 loopback harness，先验证当前 battle model/profile corpus digest，再通过唯一 runtime 执行全部登记 cases。B0.3 完成前 MUST 通过 unit、contract、integration、determinism、negative、Jolt/Detour parity、sanitizer 和 1/5/8 actor benchmark；同一 case 连续运行 MUST 得到相同 digest 且不得修改 source corpus。50 ms Tick、8 actors 和所有 hard capacity MUST 通过，CPU 2.5 ms/Tick、64 MiB instance、8 MiB history 与 256-item queue target MUST 具有当前实现的明确测量 evidence。

#### Scenario: 冻结 model/profile corpus 漂移

- **WHEN** harness 发现 model/profile manifest、case、assumption、report 或 binding digest 与冻结 source of truth 不一致
- **THEN** 所有 simulation 资格动作在启动实例前失败，旧 report 不得继续声明 qualified

#### Scenario: 八 actor workload 超过 Tick budget

- **WHEN** 固定 release build 的 8-actor benchmark 在登记 reference environment 超过 2.5 ms/Tick target 或 50 ms hard Tick cadence
- **THEN** B0.3 保持未完成并保留测量 evidence，不得降低 actor cap、Tick rate、system coverage 或删除 outlier 来通过

#### Scenario: Harness 尝试开放网络

- **WHEN** executable、test 或 preset 尝试 bind TCP/UDP、分配 production port、消费 battle ticket 或注册 numeric battle message
- **THEN** scope/architecture gate 失败；该行为必须移至后续 control/secure transport change

#### Scenario: 完整离线资格通过

- **WHEN** exact toolchain/dependencies、全部冻结 cases、negative/parity/sanitizer、连续 determinism 和 1/5/8 actor budget evidence 在同一 build/config digest 上通过
- **THEN** C++ core 可标记 `implementation-qualified-windows-x64` 并作为 B0.4 输入，但不得据此声明 Go control、Linux production、secure transport 或 battle network 已 qualified
