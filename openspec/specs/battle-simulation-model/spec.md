# Battle Simulation Model 规格

## Purpose

定义 PersonalWorld 服务器权威 gameplay 的离散时间线、输入与系统管线、移动/战斗/AI/历史/过载行为，以及可由后续 network profile 与无网络 C++ harness 消费的版本化模型证据。
## Requirements
### Requirement: 模拟 Tick 必须形成唯一离散时间线

每个 `SimulationInstance` MUST 以初始化状态 `S0` 开始；`SimulationTick T` MUST 只读取完整 `S(T-1)`、消费归一化到 T 的命令、按固定 pipeline 产生完整 `S(T)`，且 `ServerTick T` MUST 只指已经完成的后状态。Tick MUST 在当前 AssignmentStamp、SimulationInstanceID 与 model generation 内从 1 单调推进；任一 generation 变化 MUST 创建新时间线并拒绝旧 Tick、输入、history 和结果。模拟 MUST 使用固定 `dt`，不得因 wall-clock lag 改变步长或跳过 gameplay stage。

#### Scenario: 完成第一帧模拟
- **WHEN** 实例已产生 `S0` 且 worker 执行 `SimulationTick 1`
- **THEN** 全部 stage 只从 `S0` 和 Tick 1 的封存命令读取，唯一提交结果为 `S1`，任何对外 `ServerTick 1` projection 都不包含半完成阶段状态

#### Scenario: Assignment generation 变化
- **WHEN** 旧实例的 InputTick、history query 或 result 在更高 AssignmentStamp generation 已开始后到达
- **THEN** 新实例以独立 `S0` 和 Tick 1 开始，旧数据以稳定 `stale_generation` 终结且不能推进或修改新时间线

#### Scenario: Scheduler 产生 Tick debt
- **WHEN** worker 的 wall-clock 调度落后于固定模拟时间
- **THEN** runtime 只可按 profile 的有界 catch-up policy 执行相同固定 `dt` 的 Tick，不能扩大单步、跳过系统或用当前 wall clock 直接改 gameplay state

### Requirement: InputTick 映射与确认必须有界且可重演

`InputTick` MUST 是 battle session generation 内从 1 开始的单调采样索引。每个 mapping epoch MUST 固定 epoch identity、base InputTick、base SimulationTick、正整数 input step 与 simulation step，并使用 checked integer rational mapping 将 InputTick 唯一映射到目标 SimulationTick；精确 step 与允许窗口 MUST 由 network profile 提供。多个 InputTick 映射到同一 SimulationTick 时，连续意图 MUST 取稳定的最后有效样本，离散边沿 MUST 按 command sequence 各执行至多一次。`LastProcessedInputTick` MUST 只越过已应用或以稳定结果终结的连续 InputTick；未决 gap、旧 epoch、重复、提前或过期输入 MUST NOT 被静默确认。

#### Scenario: 多个采样落入同一模拟 Tick
- **WHEN** 同一 actor 的 InputTick 40 与 41 通过当前 mapping epoch 映射到 SimulationTick 20，且两者都含移动样本
- **THEN** InputIntent 使用稳定排序后的 InputTick 41 连续意图，仍分别处理两者的合法离散边沿，并使结果不依赖数据包到达顺序

#### Scenario: 输入确认前存在 gap
- **WHEN** InputTick 100、102 已终结但 101 仍在允许窗口内未决
- **THEN** `LastProcessedInputTick` 最多确认到 100；只有 101 被应用或按 missing/expiry policy 明确终结后才能继续推进

#### Scenario: Mapping epoch 被替换
- **WHEN** 重连或重新同步建立更高 mapping epoch，而旧 epoch 的未确认输入随后到达
- **THEN** 旧输入以 `stale_generation` 拒绝，客户端历史不能跨 epoch 重演，服务器不把旧 InputTick 重新映射到新时间线

### Requirement: 输入确认前沿必须形成不可变 replication projection

每个 actor 的 `InputTimeline` MUST 在 simulation/replication 冻结点产生包含同一次 committed `ServerTick`、mapping generation 与 `LastProcessedInputTick` 的不可变 projection。该游标 MUST 只越过已经应用或以稳定 accepted/rejected/expired/missing 结果终结的连续 InputTick；网络线程和 snapshot encoder MUST 只消费同一个 commit 的 projection 副本，不得分别读取 Tick 与确认游标，也不得直接读取或修改可变 timeline。更高 mapping generation 建立时 MUST 创建从 0 开始的独立确认前沿，旧 generation 的未决、晚到或重放输入不得推进它。

#### Scenario: gap 被稳定终结

- **WHEN** InputTick 100 与 102 已终结、101 未决，随后 expiry policy 把 101 稳定终结
- **THEN** 下一 simulation/replication 冻结点可把 `LastProcessedInputTick` 从 100 连续推进到 102，并产生同 generation 的不可变 projection

#### Scenario: 网络线程并发发送 snapshot

- **WHEN** simulation worker 正在处理后续 InputTick，网络线程发送先前冻结的 snapshot projection
- **THEN** encoder 只观察同一次 commit 的固定 `ServerTick`、generation 与确认游标，不组合旧 Tick 和新游标、不读取半完成 timeline，也不因线程时序产生不同 wire 值

#### Scenario: mapping generation 被替换

- **WHEN** reconnect 建立更高 mapping generation 且旧 generation 仍有未决 InputTick
- **THEN** 新 generation 的 projection 从 `LastProcessedInputTick = 0` 开始，旧 generation 的后续终结不能改变新 projection

### Requirement: 输入命令必须只表达受限意图

模型 MUST 只接受 `ContinuousIntentSample`、`JumpPressed`、`SwitchWeapon`、`ActivateAbility` 与受信 `LifecycleDirective` 等登记 command kind。命令 MUST 绑定当前 actor、assignment/instance、session generation、mapping epoch、InputTick、command sequence 和 expiry，并 MUST NOT 携带最终 Transform、grounded、命中目标、伤害值、冷却完成、奖励或可覆盖连接身份的 actor identity。每个命令 MUST 由唯一 stage 决议为 accepted 或稳定 rejection；同一 Tick 的决议 MUST 按登记的 stage、ActorID、InputTick、sequence 与 kind 顺序执行，而不是按网络、容器或线程到达顺序。

#### Scenario: 客户端提交最终命中
- **WHEN** ability payload 附带客户端选择的 target、damage 或最终 projectile position
- **THEN** 归一化边界拒绝未登记字段或忽略其裁决含义，HitDetection 与 Attribute 仍只根据服务器权威状态产生结果

#### Scenario: 相同 command 重复到达
- **WHEN** 相同 session generation、InputTick 与 command sequence 的离散 ability command 被重复提交
- **THEN** command 最多执行一次，其余副本返回或记录稳定 duplicate，不重复扣除 Cost、创建 projectile 或产生伤害

#### Scenario: 不同 actor 的命令交换到达顺序
- **WHEN** 两次运行接收相同 command set，但 actor A 与 actor B 的网络到达顺序相反
- **THEN** simulation 依规范 ActorID 和 command key 排序，产生相同 state/event/rejection 结果

### Requirement: 模拟管线与 mutation owner 必须固定

每个 world MUST 只有一个 simulation worker 写入 ECS、physics binding 和 gameplay state，并 MUST 依次执行 `DrainInput -> InputIntent -> AIIntent -> AbilityActivation -> Movement -> Physics -> HitDetection -> Effect -> Attribute -> Death -> Replication -> CommitDeferredStructuralChanges`。网络、control、physics/navigation callback 和 evidence writer MUST 只提交不可变有界输入或结果，不得直接修改世界。Transform/velocity/grounded MUST 只由 Physics 提交，Attribute 当前值 MUST 只由 Attribute 提交，alive/dead 与 death cause MUST 只由 Death 提交；entity/component 结构变化 MUST 在末尾 barrier 按稳定顺序提交，新结构最早从下一 Tick 参与迭代。Live `SimulationInstance` MUST 将 `InputTimeline` 的 typed resolution接入上述 Movement/Physics stages，并在 Replication stage把同一 committed Tick的position、orientation、velocity、grounded与输入确认原子冻结为只读projection；启动时state不得在收到合法输入后继续冒充current snapshot。

#### Scenario: Physics callback 在 Tick 中途返回
- **WHEN** 异步或 adapter callback 在 Attribute stage 返回新的碰撞结果
- **THEN** callback 不能修改当前 Transform 或 hit state；结果只能进入登记的后续 Tick 边界或使原有同步 query 按失败 policy 终结

#### Scenario: Ability 创建 projectile
- **WHEN** 扇子 activation 在 AbilityActivation stage 成功并请求创建 projectile
- **THEN** create 进入 typed deferred buffer，在末尾 barrier 获得稳定 EntityID，projectile 最早于下一 Tick 执行 Movement/Physics/HitDetection

#### Scenario: 同 Tick 多次伤害致死
- **WHEN** 多个已验证 DamageIntent 在同一 Tick 指向同一低生命 actor
- **THEN** Attribute 按稳定 key 应用到首次归零，Death 记录唯一 cause；后续 intent 不得替换 death owner 或让 actor 在同 Tick 再次激活能力

#### Scenario: Live runtime 收到移动输入
- **WHEN** `SimulationNode` current mapping generation的合法move或jump input在目标Tick被`InputTimeline`终结
- **THEN** 唯一worker执行Movement与Physics后发布动态committed projection，network thread不能继续读取启动时静态state或半完成Tick

#### Scenario: 仍在 late window 的输入刚错过目标 Tick
- **WHEN** `CommandIngress` 接纳一个仍在冻结 late window内、但其目标 Tick已提交的move、aim或jump command
- **THEN** `InputTimeline`在首个尚未提交的current Tick消费该ready command并推进对应确认；不得只确认而静默丢失，也不得接纳超出late window的过期输入

### Requirement: 移动与跳跃必须由权威 kinematic capsule 模型裁决

玩家、普通怪物与 Boss 的移动 MUST 使用配置驱动且带单位的 position、orientation、velocity、grounded、movement mode 与 capsule 定义。平面移动输入 MUST clamp 到单位长度，并以固定 Tick 的 acceleration、deceleration、maximum speed、air control、gravity、slope、step 与 skin policy 推进。Jump MUST 只消费一次合法 `JumpPressed`，且 actor 必须 alive、可移动并在 Movement stage 开始时 grounded；客户端声明的 Transform、velocity 或 grounded MUST NOT 参与裁决。Live runtime的aim yaw MUST 只来自已验证量化intent并在Physics commit边界规范化，不能由Unity Camera、Scene Transform或payload actor identity覆盖。

物理访问 MUST 只通过项目 `PhysicsWorld` value contract 提供的 ground probe、capsule move、shape/ray/overlap 与 projectile sweep；结果 MUST 规范排序且不得暴露 Jolt 类型或 callback 顺序。纯模型 fixtures MUST 能以记录化 physics result 替代真实物理库。尚未绑定正式地图碰撞内容的current PersonalWorld runtime MAY 使用由server `physics_identity`约束的固定平地adapter，但该adapter MUST 保持有界query、相同Physics mutation owner和明确替换边界，且不得读取Unity Scene。

#### Scenario: 玩家在地面跳跃
- **WHEN** alive 且允许移动的 grounded actor 在当前 Tick 提交唯一 JumpPressed
- **THEN** Movement 应用配置 jump impulse，Physics 根据记录化 capsule query 提交权威 velocity/grounded/position，并忽略客户端预测 Transform

#### Scenario: 空中重复跳跃
- **WHEN** actor 在 Movement stage 开始时不 grounded 且提交 JumpPressed
- **THEN** 首期模型以稳定 invalid-state 终结该边沿，不产生二段跳、额外 impulse 或延后到落地后补执行

#### Scenario: 两个物理命中具有相同 fraction
- **WHEN** physics adapter 对同一 query 返回相同规范 fraction 的多个 collider
- **THEN** model 按 ColliderID、SubshapeID 稳定排序并产生与 callback/容器顺序无关的移动或命中结果

#### Scenario: Current PersonalWorld 使用平地 adapter
- **WHEN** live instance尚未绑定正式地图碰撞资产且其已验证physics identity选择current平地adapter
- **THEN** server只在world Y=0与登记capsule/ground policy内裁决移动和落地，结果仍由Physics stage提交且不得外推为正式地图collision资格

### Requirement: 武器授予、Ability、Effect 与 Cooldown 必须具有显式状态机

装备剑 MUST 只授予登记的剑 ability，装备扇子 MUST 只授予登记的扇子 ability；切换武器 MUST 在 AbilityActivation stage 原子撤销旧 grant、应用新 grant并执行配置声明的 active ability cancel policy，不得复制角色 Attribute owner。Ability MUST 使用 `requested`、`windup`、`active`、`recovery`、`completed`、`canceled` 或 `rejected` 等封闭 phase，并按固定顺序校验 alive、grant、required/blocked tag、Cost 与 Cooldown。Cost、Cooldown、Effect duration/period/stack/expiry MUST 使用权威 SimulationTick 和稳定 ID，不得依赖 wall-clock timer 或客户端完成声明。

#### Scenario: 剑 Ability 成功激活
- **WHEN** actor alive、已装备剑、grant/tag 合法、Cost 充足且 Cooldown 完成
- **THEN** AbilityActivation 创建唯一权威 activation，按配置进入 windup/active/recovery，在 commit phase 扣除 Cost 并记录绝对 Cooldown end Tick

#### Scenario: 冷却中的扇子预测
- **WHEN** Unity 已预测扇子表现，但服务器发现当前 Tick 早于 Cooldown end Tick
- **THEN** activation 以稳定 cooldown rejection 终结，不扣除重复 Cost、不创建 projectile、不产生 damage，并提供可关联 prediction identity 的表现拒绝事件

#### Scenario: Effect 在边界 Tick 过期并重新施加
- **WHEN** 既有 Effect 的 expiry Tick 等于当前 Tick，且同 Tick 又收到相同 stack key 的 apply
- **THEN** 模型先按固定 expiry 语义移除旧 Effect，再依据登记 stack policy 应用新 Effect，结果不依赖 timer callback 顺序

### Requirement: 剑、扇子、伤害和死亡必须由服务器完整裁决

剑 primary MUST 在登记 active Tick 发起一次权威 shape sweep，并保证同一 activation 对同一 target 最多命中一次。扇子 primary MUST 在 activation commit 后 deferred spawn 权威 projectile；projectile MUST 从下一 Tick 按配置速度、碰撞与 lifetime 执行 sweep，并在首次合法阻挡命中或 expiry 后 deferred destroy。HitDetection MUST 只产生 `DamageIntent`；Effect MUST 决议 immunity/stack；Attribute MUST 使用版本化 scaled-integer formula、checked arithmetic、固定 modifier 顺序和范围 clamp 修改 health/resource；Death MUST 在 health 首次到零时提交唯一死亡事实。GameplayCue MUST 只是表现事件，不能作为命中、伤害或死亡事实。

#### Scenario: 剑 sweep 重复覆盖同一目标
- **WHEN** 同一 sword activation 的记录化 sweep 通过多个 subshape 返回同一 target
- **THEN** HitDetection 按 target identity 去重，只产生一次合法 DamageIntent，并在 expected event 中保留规范 query identity

#### Scenario: 扇子 projectile 命中
- **WHEN** projectile 在当前 Tick 的 sweep 首次遇到合法敌对 collider
- **THEN** HitDetection 产生绑定 source、target、activation 与 Tick 的 DamageIntent，projectile 排入 deferred destroy，客户端不能通过 cue 或本地碰撞改变结果

#### Scenario: Damage 计算溢出
- **WHEN** 非法 content 参数使 checked intermediate 超出模型声明范围
- **THEN** world/config 在进入可运行状态前失败；runtime 不使用语言隐式 overflow、NaN 或 wraparound 继续模拟

### Requirement: AI 与 Boss phase 必须只产生确定性 intent

普通怪物 MUST 使用封闭的 `idle/acquire/chase/attack/recover/dead` lifecycle，Boss MUST 在相同权威 lifecycle 上使用由 health threshold 驱动的登记 phase。AIIntent MUST 只读取 Tick 开始时的感知、threat、navigation 与 gameplay state，输出与玩家 command 等价的 movement/ability intent，不得直接写 Transform、Attribute、Death 或 reward。目标选择 MUST 按最高 threat、最近规范距离、ActorID 的稳定顺序；随机选择 MUST 使用从 instance seed、SystemID、EntityID 与 StreamID 派生的独立 PRNG stream。

#### Scenario: 两个玩家 threat 相同
- **WHEN** 两个 alive 玩家对怪物具有相同最高 threat 且规范距离相同
- **THEN** AI 选择 ActorID 排序更前的玩家，结果不依赖 ECS storage 或连接加入顺序

#### Scenario: Boss 在当前 Tick 越过 phase threshold
- **WHEN** Attribute stage 使 Boss health 首次低于下一 phase threshold
- **THEN** 当前 Tick 不重入 AIIntent；下一 Tick AI 观察新 health 并执行唯一 phase transition及其登记 intent

#### Scenario: 新增无关怪物
- **WHEN** world 增加一个不参与当前战斗的 entity
- **THEN** 既有怪物按独立 PRNG stream 产生相同随机选择，除非新增 entity 真实改变其感知或 gameplay 输入

### Requirement: 历史帧查询与 replay evidence 必须有界且只读

每个已完成 Tick 的 history MUST 只保存登记 entity generation、alive/team、position/orientation、hit volumes、pose/movement flags 和查询 policy 所需的最小 ability/tag 投影，不得复制完整 ECS、网络会话或持久事实。历史查询 MUST 绑定当前 actor、activation、assignment 和 instance generation，将客户端 observed Tick 通过当前 mapping epoch 转换并限制在已完成且仍保留的窗口；旧 generation、过期、缺帧或越权字段 MUST fail closed。查询结果 MUST 只在当前 Tick 产生 hit/damage intent，不得改写历史状态或恢复已提交结果。

#### Scenario: 合法近战延迟补偿
- **WHEN** 当前 sword activation 携带仍在保留窗口内且映射合法的 observed Tick
- **THEN** HitDetection 只读取该 Tick 登记的 target hit volume，在当前 Tick 产生命中或未命中结果，并记录实际使用的 ServerTick

#### Scenario: 请求已经淘汰的历史
- **WHEN** observed Tick 早于当前 history 最旧 Tick或目标帧缺失
- **THEN** 模型按 ability policy 返回无补偿命中或稳定拒绝，不扩大缓存、不使用客户端位置，也不回滚当前世界

#### Scenario: 客户端声明未来 Tick
- **WHEN** mapped observed Tick 晚于 current completed ServerTick
- **THEN** 查询 clamp 到 current completed Tick 并记录 clamp 原因，不能读取未完成状态或推进服务器时间

### Requirement: 容量与过载必须产生确定失败而非状态漂移

模型 MUST 为 player、AI、projectile、Effect、每 actor/Tick command、world/Tick command、deferred structural command、physics/navigation query、history、event、evidence、catch-up step 与 Tick debt 声明带单位的正容量参数。旧 generation/非法输入 MUST 在容量计数前拒绝；无法保证完整生命周期时 MUST 先拒绝新 admission 或 spawn，不得驱逐既有 actor。连续意图 MAY 按登记 key 收敛为最后样本，离散 command MUST NOT 静默合并；已接受 mutation MUST 在目标 Tick 执行或以稳定 rejection 终结，不得进入无界 backlog。History/evidence 淘汰 MUST 保留截断原因；hard Tick debt MUST 停止新 admission并请求受控 drain/failure，而不是改变 fixed `dt`。

#### Scenario: Projectile capacity 已满
- **WHEN** 一个合法扇子 activation 到达 commit phase，但 world 无法为 projectile 保留完整 lifecycle capacity
- **THEN** activation 按冻结 commit/cancel policy 返回稳定 capacity outcome，不创建半初始化 entity、不挤出既有 projectile，也不留下已扣 Cost 但无对应结果的未定义状态

#### Scenario: Command burst 超过每 actor 上限
- **WHEN** 同一 actor 在一个 SimulationTick 提交超过上限的连续与离散 command
- **THEN** 模型先按规范 key 收敛允许收敛的连续样本，再按稳定顺序接受至上限并明确拒绝其余离散 command，结果不依赖到达顺序

#### Scenario: Tick debt 超过 hard limit
- **WHEN** runtime 已执行允许的 catch-up steps 仍超过 profile hard Tick debt
- **THEN** instance 停止新 admission、产生低敏 overload reason 并请求受控 drain/failure，既有 Tick 不以可变 `dt` 或跳 stage 追赶

### Requirement: 纯模型结果必须在声明边界内确定

在相同 model/config version、初始状态、规范命令集、记录化 physics/navigation result、seed 和容量参数下，纯模型 MUST 产生相同规范 query sequence、state projection、event sequence、rejection set 和 SHA-256。纯规则 MUST NOT 读取 wall clock、pointer、thread ID、locale、未登记 entropy 或不稳定容器迭代。Live physics/navigation 只需在锁定 build/platform/profile 下满足 adapter contract、排序和容差；规格 MUST NOT 声称不同平台或 Unity/C++ 对完整世界逐位 Lockstep。

#### Scenario: 相同 case 重复执行
- **WHEN** 无网络 harness 在相同锁定输入与记录化 adapter trace 上重复执行一个 fixture
- **THEN** 每个 checkpoint 的 query、state、event、rejection 和 canonical digest 完全一致

#### Scenario: Physics trace 产生差异
- **WHEN** live Jolt adapter 在另一个受支持平台返回超出登记容差或不同规范命中集
- **THEN** qualification 将差异定位为 physics adapter/profile mismatch，不把客户端结果设为权威，也不伪报纯规则 fixture 通过

### Requirement: 纯模型 fixtures 必须完整、版本化且无网络依赖

仓库 MUST 在 `shared/contracts/fixtures/battle/model/` 维护 versioned schema、manifest、assumptions、正向/拒绝/确定性 cases 与 canonical digest。每个 case MUST 声明 model/config version、单位、initial state、mapping epoch、commands by Tick、physics/navigation trace、random streams、expected query/state/event/rejection/capacity outcome；manifest 与 case MUST 双向完整、ID 唯一且稳定。单一 validator MUST 检查 schema、引用、排序、摘要、coverage 与 LF/UTF-8，并 MUST NOT 实现第二套 gameplay engine、启动 listener、安装第三方 C++ 依赖或依赖真实网络。

#### Scenario: Manifest 漏登记 case
- **WHEN** `cases/` 新增文件但 manifest 不含该 case，或 manifest 引用不存在的文件
- **THEN** battle model validation 失败并报告稳定 case/path，不允许该 corpus 作为 B0.2 或 C++ core 输入

#### Scenario: Fixture 包含 wire 或凭据
- **WHEN** case 尝试登记 message id、UDP lane、raw ticket、AEAD key、账号凭据或真实玩家资料
- **THEN** schema/安全校验失败；model fixtures 只保留纯 gameplay identity、低敏 assignment fingerprint 和记录化 port 值

#### Scenario: 后续 C++ harness 消费 corpus
- **WHEN** `implement-game-simulation-core` 执行无网络 simulation harness
- **THEN** harness 使用同一 manifest/cases 比较 query/state/event/rejection/digest，不导入 Go domain、Unity 类型或 production socket

### Requirement: 预算假设必须区分工作负载与资格结果

模型 MUST 为 solo Owner、当前默认 1 Owner + 4 Visitor、VisitSession 可配置上限兼容性、movement-heavy、combat-heavy、Boss burst、disconnect/drain 等 workload 登记 actor、AI、projectile、Effect、query、history、event 和 state-change 维度，并标明单位与 `assumption`、`hard_contract` 或 `profile_output`。未经 `define-battle-network-profile` 重复测量的 tick cadence、window、CPU、memory、queue、snapshot 与 bandwidth 数值 MUST NOT 标记为 qualified。若最终 gameplay player 上限低于 Go VisitSession 当前配置，Go admission MUST 在安全 UDP 启用前具有显式 profile capacity gate。

#### Scenario: Network profile 开始测量
- **WHEN** B0.2 读取 model assumptions 构造网络模拟矩阵
- **THEN** 它能区分现有 VisitSession 范围、首期默认负载和待测输出，不以模型作者猜测值替代测量报告

#### Scenario: 发布 profile 只支持更低玩家数
- **WHEN** 经资格的 simulation/network profile 上限低于某环境允许的 Owner + Visitor 数
- **THEN** 后续 Go/C++ control 与 admission 必须在签发 battle 资格前拒绝超限实例，不能让已进入玩家在 simulation queue 中被随机驱逐
