# Game Simulation Core 规格

## Purpose

定义服务器权威 C++ gameplay simulation core 的可重复工程基线、实例生命周期、ECS、固定 pipeline、确定性、第三方 adapter、history/replay evidence 与离线资格门。

## Requirements

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

### Requirement: Networked SimulationInstance 必须加载并执行 production combat catalog

`SimulationNode` MUST 在ready前加载已验证production authority catalog、wire mapping与collision/navigation binding，并为每个SimulationInstance创建immutable typed catalog view。Current encounter MUST 在唯一simulation worker上确定生成player、少量ordinary monster与一只Boss；player BattleSession只能绑定既有actor slot，AI/projectile/effect/death entity必须受currentinstance hard capacity与deferred structural barrier约束。Flat-ground fixture/config、governance-only package或启动时静态projection MUST NOT充当production combat runtime。

#### Scenario: Production instance 正常启动
- **WHEN** child source、Go expected identity、model/profile/wire、nav/physics和capacity全部匹配
- **THEN** instance以确定seed与spawn plan建立player/monster/Boss current state，并在首个full snapshot发布真实archetype、weapon、health与phase projection

#### Scenario: Encounter peak 超过容量
- **WHEN** production package的player、AI、projectile、effect或query peak无法在current hard cap内保留完整lifecycle
- **THEN** node或instance在admission/activation前以稳定capacity reason拒绝，不隐藏spawn、不驱逐既有entity且不产生半扣Cost状态

### Requirement: Player 参战资格必须由 active BattleSession generation 驱动

预留 player actor slot MUST 只表达实例容量与稳定 actor identity。`BattleTransportRuntime` MUST 作为 authenticated BattleSession 创建、接管和终结的唯一 owner，把 exact instance、actor 与 session generation 生命周期提交给 `SimulationNode`；Simulation worker MUST 在每个 Tick barrier 冻结规范 active actor set，并让 Movement、Ability、AI target 和 Damage 使用同一份资格事实。未 active 的 player MUST 保留 current health/death 等权威状态，但 MUST 停止速度、忽略 intent 且不可成为 AI 或 damage target。Session 终结、断线或 successor takeover MUST NOT 回血、复活或重建 actor generation；旧 session 的迟到终结 MUST NOT 撤销更高 session generation。

#### Scenario: 实例在无人连接时持续推进
- **WHEN** production instance 已创建 player slots 和 AI，但尚无 actor 的 authenticated active BattleSession
- **THEN** simulation Tick 可以继续推进，所有 player 保持初始权威 health/position、速度为零，AI 不选择或攻击这些空 slot

#### Scenario: 同 actor 的 successor session 接管
- **WHEN** generation N+1 的 authenticated session 接管同一 actor，随后 generation N 的终结通知迟到
- **THEN** 下一 Tick 仍只把 N+1 视为 active，旧通知不能撤销 successor，且 actor 继续使用原有 health/death 与 entity generation

#### Scenario: 已死亡玩家断线后重连
- **WHEN** player 在 active session 中被权威伤害判定死亡，随后断线并以新 BattleSession generation 重连同一 instance
- **THEN** full baseline 仍投影该 actor 已死亡且输入关闭，不因 session activation 产生回血、复活或新 actor generation

### Requirement: 武器、Ability、AI、伤害与死亡必须接入 production 单写 pipeline

Current network input MUST 解析move、aim、jump、switch-weapon与primary-ability；switch MUST 原子撤销旧grant、应用新grant并按配置处理active ability，primary MUST 只激活current weapon授予的ability。Sword sweep、fan projectile、AI intent、hit、Effect、Attribute、Death、Boss phase、replication与structural change MUST 严格遵循冻结pipeline和整数/checked arithmetic。C++ MUST 发布单调reliable ability/lifecycle event及同Tick snapshot projection；客户端Collider、target、damage、health、phase、death或reward声明 MUST NOT 参与裁决。

#### Scenario: Sword sweep 命中重复 subshape
- **WHEN** current sword active Tick的规范sweep为同一enemy返回多个subshape hit
- **THEN** HitDetection按entity generation去重，只产生一次DamageIntent和一组规范target event，Attribute最多应用一次对应damage

#### Scenario: Fan projectile 命中 world 后终结
- **WHEN** fan activation在Tick T提交projectile且该projectile在后续Tick首次命中合法world或enemy collider
- **THEN** projectile不早于T+1移动，只对首次合法enemy hit产生damage，并在同Tick排入deferred destroy且不继续穿透造成未登记命中

#### Scenario: Boss phase 与死亡同 Tick 竞争
- **WHEN** damage在同一Tick既越过phase threshold又把Boss health降至零
- **THEN** Death提交唯一dead事实并阻止下一Tickphase/AI重入，event与snapshot以稳定顺序表达死亡而不先启动新phase

### Requirement: Combat projection 与 evidence 必须有界、低敏且不可结算

Replication MUST 从同一次committed Tick冻结entity archetype、equipped weapon、health/max health、phase/dead flags、transform、input acknowledgement及有界ability/lifecycle events。Unknown/unmapped ID、queue overflow、event expiry或partition failure MUST 产生稳定failure/backpressure/resync行为，不得改走其他transport。Replay/diagnostic evidence MAY 记录package/config/nav/physics/wire identity、seed、Tick、numeric semantic ID、capacity和稳定outcome；MUST NOT记录credential、PlayerID、完整package、奖励或把encounter-complete解释为settlement receipt。

#### Scenario: Reliable cue event 过期
- **WHEN** ability event在KCP sender application deadline前未能发送
- **THEN** event以稳定expiry终结且不回退到raw/WSS/TLS-TCP，后续snapshot仍使health/dead/phase持续事实收敛

#### Scenario: Boss defeat evidence 被提交
- **WHEN** runtime记录Boss首次死亡与encounter-complete摘要
- **THEN** evidence绑定exactinstance/config和Tick但不含玩家资料或reward，并且Go不得把该摘要当作资产、掉落或PersonalWorld mutation已提交证明
