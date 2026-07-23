## Context

`define-authoritative-gameplay-architecture` 已确定 C++ Game Simulation Server 是实例内移动、物理、AI、Ability、Effect、伤害、死亡和实时复制的唯一权威，Go 继续拥有 PersonalWorld、WorldInstance placement、VisitSession、admission、持久事实与结算，Unity 只拥有输入、预测副本和表现。当前文档仍把 tick rate、输入窗口、actor 数、physics 结果和能力细节留给后续阶段，尚不存在可由 C++、网络 profile 和 Unity 共同消费的纯模型 corpus。

本 change 位于 `simulation model -> network profile -> C++ core` 的第一项。它必须把“什么状态在什么 Tick 由谁修改、面对相同输入应产生什么结果”冻结下来，同时避免越界选择 wire layout、UDP/KCP lane、Jolt/Detour API、C++ 类型或最终性能数值。主要消费者是后续 network profile 作者、C++ simulation core、Unity prediction/replica、Go result handoff 和资格工具。

模型只覆盖首个 PersonalWorld gameplay：一个 immutable Owner 与当前 VisitSession 允许的 Visitor，共享一个绑定完整 `AssignmentStamp` 的 `SimulationInstance`；玩家移动、跳跃、使用剑和扇子，普通怪物与一只 Boss 由服务器 AI 控制。ActivityInstance、Room、Party、匹配、经济和奖励提交不进入本模型。

## Goals / Non-Goals

**Goals:**

- 冻结离散 Tick、输入映射、确认、缺失/重复/迟到命令和 generation reset 语义。
- 冻结单写 simulation pipeline，以及每个 gameplay state 的唯一 mutation stage。
- 给移动、跳跃、物理查询、剑、扇子、Effect、Attribute、Damage、Death、AI 与 Boss phase 提供可测试状态机。
- 区分纯规则确定性、physics adapter 确定性和跨平台非承诺，形成稳定 state/event hash 边界。
- 以版本化、机器可读 fixtures 表达初始状态、命令、记录化 physics/navigation 结果、随机流、预期状态、事件和拒绝原因。
- 给 network profile 暴露完整参数目录、单位、工作负载矩阵和测量点，而不猜测未经测量的最终数值。
- 保持 Go、Unity v1、现有 HTTPS/WSS/TLS-TCP、协议 registry 和生产端口无变化。

**Non-Goals:**

- 不实现 C++ ECS、simulation worker、Jolt、Detour、Asio、KCP、CMake 或 production listener。
- 不新增 `.proto`、message id、lane、framing、MTU、snapshot cadence、tick rate、插值窗口或 KCP 参数。
- 不承诺 Unity/C++、不同 CPU/编译器或不同 physics 版本之间逐位一致的完整世界 Lockstep。
- 不实现通用 ECS、行为树/技能编辑器、脚本 VM、反射、通用事件总线、Job Scheduler 或完整客户端 GAS。
- 不定义掉落、资产、奖励、任务、经济、持久 respawn 或结算公式。

## Decisions

### 1. Tick 表示已完成的离散后状态

每个 `SimulationInstance` 初始化完成后产生状态 `S0`。`SimulationTick T`（`T >= 1`）只从完整状态 `S(T-1)` 开始，消费归一化到 T 的命令，按固定 pipeline 执行并在唯一提交点产生 `S(T)`；`ServerTick T` 始终指已完成且可读取的 `S(T)`。Tick 使用实例内单调无符号整数，assignment、instance 或 model generation 变化时创建新时间线，旧 Tick 不得续接。

`InputTick` 是 battle session generation 内从 1 开始的单调采样索引，不等于 wall clock，也不直接等于 `SimulationTick`。一个 mapping epoch 固定：

```text
epoch_id
base_input_tick
base_simulation_tick
input_step_ns
simulation_step_ns

mapped_tick =
  base_simulation_tick
  + floor((input_tick - base_input_tick) * input_step_ns / simulation_step_ns)
```

计算使用 checked integer arithmetic；两个 step 的精确数值和 mapping epoch 的网络建立方式由 network profile 冻结。映射结果不得早于 epoch 的 base，epoch 变化使旧的未确认输入失效。多个 `InputTick` 映射到同一 `SimulationTick` 时，连续意图取该 actor 在该 Tick 的最后一个有效样本，离散边沿按 command sequence 各执行至多一次。

`LastProcessedInputTick` 表示从 1 起连续区间内的每个 InputTick 都已被应用，或已因 missing/expired/invalid 以稳定结果终结；存在未决 gap 时不得越过它确认。缺失连续输入在 profile 参数 `continuous_hold_ticks` 内沿用最后样本，之后回到 neutral；离散边沿永不推测。网络 bundling、提前/迟到窗口和具体 hold 数值属于 profile。

替代方案是令 `InputTick == SimulationTick`。它更简单，但把输入采样率、模拟率和未来网络调优耦合，无法支持不同 cadence，因此不采用。

### 2. 输入先归一化为有限 command vocabulary

纯模型只接受已完成身份、assignment、session epoch、mapping epoch 和基本范围校验的不可变命令：

| command | 内容 | mutation owner |
|---|---|---|
| `ContinuousIntentSample` | 平面移动轴、look/aim 单位方向、可选 sprint 意图 | `InputIntent` |
| `JumpPressed` | 单次跳跃边沿 | `InputIntent`，由 `Movement` 消费 |
| `SwitchWeapon` | `sword` 或 `fan` 槽 | `AbilityActivation` |
| `ActivateAbility` | 已授予 ability slot 与 prediction identity | `AbilityActivation` |
| `LifecycleDirective` | 受信 spawn/remove/invalidate 控制输入 | `DrainInput`/deferred commit |

命令只引用当前 actor 和稳定 gameplay/config ID，不携带最终 Transform、target hit、damage、cooldown completion、reward 或任意可覆盖连接身份的 PlayerID。网络 adapter 将来负责 wire decode 和 anti-replay；simulation model 只返回 `accepted` 或稳定的 `stale_generation`、`duplicate`、`expired`、`invalid_state`、`not_granted`、`capacity` 等结果。

同一 Tick 内按 `(stage priority, ActorID, InputTick, CommandSequence, command-kind order)` 排序。`ActorID` 与所有 content/entity ID 使用规范字节序比较，不依赖指针、hash map 或到达顺序。

### 3. Pipeline 保持单写并明确 barrier

每个 world 的唯一 simulation worker 执行：

```text
DrainInput
-> InputIntent
-> AIIntent
-> AbilityActivation
-> Movement
-> Physics
-> HitDetection
-> Effect
-> Attribute
-> Death
-> Replication
-> CommitDeferredStructuralChanges
```

- `DrainInput` 封存本 Tick command batch、处理受信 lifecycle invalidation，并拒绝越界输入。
- `InputIntent` 与 `AIIntent` 只写各自 intent components；AI 不直接移动或造成伤害。
- `AbilityActivation` 裁决 grant、tag、cost、cooldown 和 phase transition，并产生 typed requests。
- `Movement` 计算期望位移/速度；`Physics` 是唯一提交 transform、velocity、grounded 和接触状态的 stage。
- `HitDetection` 只产生带 source/target/activation/query Tick 的命中或 damage intent。
- `Effect` 决议 immunity、stack 和 duration；`Attribute` 是 health/resource/stat 当前值的唯一写者。
- `Death` 是 alive/dead、kill cause 和死亡 cue 的唯一写者。
- `Replication` 产生只读 projection/event，不修改 gameplay state。
- create/destroy/add/remove component 只进入 typed deferred buffer，并按稳定 key 在最后 barrier 提交；因此新 entity 最早从下一 Tick 参与系统迭代。

网络、control、physics callback、navigation callback 和 evidence writer 均不得越过队列直接修改上述状态。

### 4. 移动采用配置驱动的 kinematic capsule 规则

玩家、普通怪物和 Boss 都使用项目 value types 表达 position、orientation、linear velocity、grounded、movement mode 和 capsule definition。玩家平面输入先按长度 1 clamp，再由 config 中带单位的 acceleration、deceleration、maximum speed、air control、gravity、jump impulse、slope、step 和 skin 参数推进固定步长。Jump 只消费一次 `JumpPressed`；actor 必须 alive、允许移动且在该 Tick Movement 开始时 grounded，首期不隐含二段跳、coyote time 或客户端指定 grounded。

`PhysicsWorld` port 只暴露稳定值：

- `GroundProbe`
- `MoveCapsule`
- `ShapeCast`
- `RayCast`
- `Overlap`
- `ProjectileSweep`

请求显式携带 world、Tick、query ID、shape/layer/filter 和起止状态；结果按 `(fraction, ColliderID, SubshapeID)` 规范排序，重复 collider 按 query policy 收敛。Jolt 专有类型、pointer、callback 顺序和未量化 error 不进入 model contract。纯模型 fixtures 注入记录化 query result；后续 Jolt adapter contract test 证明相同请求能规范化为同一结果形状。

选择 kinematic capsule 是因为首期主要是角色探索和战斗，能冻结清晰的 grounded/jump/step 语义；完整 dynamic character 或客户端 Transform 权威会扩大不可控物理差异，因此不采用。

### 5. 武器只授予有限 Ability，战斗使用固定 phase

装备 `sword` 只授予 `sword.primary`，装备 `fan` 只授予 `fan.primary`；切换在 `AbilityActivation` stage 原子撤销旧武器 grant、应用新 grant，并按 ability config 的 cancel policy 终止不再合法的 activation。角色 Attribute 仍只有一份，不随武器复制。

Ability activation 具有 `requested -> windup -> active -> recovery -> completed` 或 `canceled/rejected` phase。Cost 在配置声明的 commit phase 扣除，Cooldown 在成功 commit 时以绝对结束 Tick 记录；Tag requirement/block、Cost、Cooldown、alive 和 grant 校验具有冻结顺序。prediction identity 只用于对应客户端表现，不是权威 activation ID。

- 剑的 primary 在 active Tick 发出一次服务端 `ShapeCast`/sweep；同一 activation 对同一 target 最多命中一次。允许的历史补偿只改变目标查询投影，不移动当前世界。
- 扇子的 primary 在 activation commit 时排入一个有界 projectile spawn；projectile 在末尾 barrier 创建，下一 Tick 起按固定速度/lifetime 进行 `ProjectileSweep`，首次合法阻挡命中后排入销毁。

GameplayCue 是由已裁决 activation/hit/effect/death 产生的表现事件。Cue 丢失或重复不得改变 damage、health、cooldown 或 entity lifecycle。

### 6. Effect、Attribute、Damage 与 Death 使用整数化、稳定顺序

离散 gameplay 数值使用带 schema 版本的有符号 64-bit scaled integer；scale、单位、合法范围和 overflow policy 写入 model config。乘法使用 checked intermediate，并按 toward-zero 规则回到 scale；最终值 clamp 到属性声明范围，非法配置在 world 创建前失败。Transform/physics 可以使用 adapter 的浮点值，但不得参与纯规则的跨平台逐位承诺。

首期 damage 只允许版本化 typed formula：

```text
raw = flat_damage + attacker_power * power_coefficient
modified = ordered_tag_modifiers(raw)
mitigated = max(minimum_damage, modified - max(0, defender_defense))
```

每个 modifier 具有稳定 priority 与 ID，顺序为 additive、multiplicative、flat mitigation、final clamp；具体 coefficient 和 attribute 数值属于 content config。`HitDetection` 产生 `DamageIntent`，`Effect` 决定 immunity/stack，`Attribute` 按 `(target, source, activation, effect)` 稳定顺序应用。Health 首次到达 0 时，`Death` 记录唯一 cause 并使 actor 停止移动、AI 与新 activation；同 Tick 后续 damage 只能形成已忽略诊断，不能更换 death owner。

Effect 只支持 `instant`、`duration` 和 `periodic`，并显式声明 stack key、maximum stacks 与 `reject/replace/refresh/independent` policy。Expiry 使用绝对 SimulationTick；等于当前 Tick 时先 expiry，再执行当 Tick 新 apply，避免 wall-clock timer 和边界歧义。

### 7. AI 是确定性 intent producer

普通怪物使用 `idle -> acquire -> chase -> attack -> recover -> dead`，Boss 在相同 lifecycle 上增加由 health threshold 驱动的 phase。AI 在 `AIIntent` 只读取该 Tick 开始时的权威感知投影，输出与玩家同形的 movement/ability intent；导航和 physics 结果由窄 port 返回。

候选目标必须 alive、可攻击且位于配置感知范围。选择顺序为最高 threat、最近规范距离、最后按 ActorID；damage 可增加 threat，目标失效后重新选择。Boss phase threshold 只在 `Attribute` 完成后由下一 Tick AI 观察，不在同一 Tick 重入 pipeline。随机选择使用按 `(SimulationInstanceSeed, SystemID, EntityID, StreamID)` 派生的独立 PRNG stream；新增其他 entity 或 system 不得扰动既有 stream。

首期不引入行为树、脚本 VM、动态 navmesh 烘焙或机器学习决策。有限状态模型足以覆盖少量怪物和一只 Boss，也能形成可重复 fixtures。

### 8. 历史只保存命中查询所需投影

每个已完成 `S(T)` 之后记录最小 `HistoryProjection`：entity generation、alive/team、position/orientation、规范 hit volumes、pose/movement flags，以及查询 policy 明确要求的 ability/tag 状态。历史不复制完整 ECS、AI blackboard、Effect collection、网络状态或持久事实。

攻击输入携带的 observed Tick 先通过当前 mapping epoch 转为候选 ServerTick。查询必须：

1. 绑定当前 actor、activation、assignment 和 instance generation；
2. 拒绝早于保留窗口、缺帧或旧 generation 的候选；
3. 将未来 Tick clamp 到 current completed Tick 并记录原因；
4. 只读取登记的 collision layers/fields；
5. 在当前 Tick 产生 hit/damage intent，不改写历史状态。

窗口长度、每 actor 历史大小和最大回看量由 network profile 测量后填写。Overload 不得自动扩张历史；缺帧 fail closed 为无补偿命中或稳定拒绝，由 ability policy 明确选择。

### 9. 过载策略先保护既有权威状态

模型登记但不在本 change 猜测数值的容量包括：players、AI actors、projectiles、active effects、每 actor/Tick commands、world/Tick commands、deferred structural commands、physics/navigation queries、history frames/bytes、events、evidence bytes、Tick wall budget、catch-up steps 和 Tick debt。

所有集合必须在 world 创建前取得正上限。达到容量时按以下原则收敛：

1. 旧 assignment/generation 和非法输入先拒绝，不占用 gameplay 容量。
2. 新 admission、AI/projectile/effect spawn 在无法保证完整生命周期时拒绝，不挤出既有 actor。
3. 连续 intent 对同一 actor/InputTick 只保留稳定的最后样本；离散命令不静默合并。
4. 已接受且会修改权威状态的命令要么在声明 Tick 执行，要么产生稳定拒绝，不能留在无界 backlog。
5. history/evidence 到达上限时按固定最旧顺序淘汰并记录 truncation；不得删除当前状态或伪造完整 evidence。
6. scheduler 落后不得改变固定 `dt`、跳过 gameplay stage 或一次运行可变大步长。运行时只可执行 profile 限定的 catch-up steps；超过 hard Tick debt 后停止新 admission并请求受控 drain/failure。

精确阈值由 network profile 以本 change 的 workload matrix 测量；C++ core 不得用不同隐藏默认值改变模型。

### 10. 确定性分为纯规则、adapter 和 replay 三层

在相同 model/config version、初始状态、命令集、记录化 physics/navigation results 和 seed 下，纯模型必须产生字节规范化后一致的 state hash、event sequence、rejection set 和 query request sequence。纯规则禁止读取 wall clock、线程 ID、pointer/hash iteration order、locale 或未登记 entropy。

Jolt/Detour live adapter 只承诺在锁定 build/platform/profile 上满足 contract、容差和规范排序；本 change 不承诺跨平台 bitwise physics determinism。Replay evidence 必须注明 build、platform、dependency/config hash；如果 adapter trace 不同，诊断必须将差异定位到 query boundary，而不是声称纯规则 nondeterministic。

这比“完整世界任何平台逐位一致”更符合服务器权威架构，也比只记录最终 snapshot 更能定位输入、规则和 physics 的差异。

### 11. Fixture corpus 是模型源事实，不是 wire fixture

新增目录：

```text
shared/contracts/fixtures/battle/model/
  README.md
  schema.json
  manifest.json
  assumptions.json
  cases/
    tick-input/
    movement/
    combat/
    ai/
    history/
    overload/
    negative/
```

每个 case 固定 `format_version`、`model_version`、config/content hash、initial state、mapping epoch、commands by Tick、physics/navigation trace、random streams、expected query requests、state projections、events、rejections、capacity outcome 和 canonical SHA-256。ID、单位和枚举均显式登记；case 不包含 socket、lane、message id、raw credential、账号资料或第三方对象。

`tools/battle-model/battle-model.ps1` 提供 `validate`：检查 schema、manifest 双向完整性、case ID/版本/引用、稳定排序、单位、摘要、required coverage 和 canonical LF/UTF-8。它不实现第二套 gameplay engine；后续 C++ harness 消费同一 corpus 并产生实际结果比较。网络 profile 读取 `assumptions.json` 的 default 与 compatibility workloads，填充经测量参数而不改写既有 case 语义。

### 12. 预算假设使用场景矩阵，不伪装成资格值

`assumptions.json` 至少登记：

- `solo_owner`：1 名玩家的移动、跳跃和单武器行为；
- `default_coop`：当前默认 1 Owner + 4 Visitor；
- `visit_capacity_compatibility`：1 Owner + 配置允许的 Visitor 上限，用于发现 admission/profile 不兼容；
- 普通怪物、Boss、projectile、active effect 与 hit query 的目标/峰值维度；
- idle、movement-heavy、combat-heavy、Boss burst、disconnect/drain 等 workload phase；
- 每项待测的 CPU time/Tick、memory/instance、history bytes、query counts、event volume 和 state-change bytes。

数值标记为 `assumption`、`hard_contract` 或 `profile_output`。只有现有 VisitSession 范围和模型完整性属于 hard contract；性能、cadence、window 和 bandwidth 在 B0.2 测量前不得标记 qualified。

## Risks / Trade-offs

- [模型过度具体导致内容难以调优] → 冻结状态迁移、单位、排序和公式形状，把速度、伤害、冷却、阈值等保留为版本化 config 数据。
- [模型过度抽象使 C++ core 仍需重新决策] → fixtures 必须覆盖移动、跳跃、两种武器、Effect/Death、AI/Boss、history、overload 与负向边界，并提供预期 query/event/state。
- [InputTick 映射在真实时钟同步下不可用] → 本 change 只冻结整数 anchor/ratio 和 epoch reset；B0.2 必须用 latency/jitter/drift 测量选择 step、window 与 epoch 建立策略。
- [浮点 physics 破坏 deterministic replay] → 纯规则使用记录化 adapter trace；live physics 差异在 query boundary 以锁定版本和容差诊断，不承诺跨平台 bitwise world。
- [默认 4 Visitor 与允许上限 32 的负载差距过大] → assumptions 同时包含 default 与 compatibility workload；B0.2 必须显式决定发布上限，Go admission 不得超过已资格 profile。
- [fixture validator 演变成第二套模拟器] → 工具只验证结构、引用、排序、摘要和 coverage；规则执行只由后续 C++ harness 实现。
- [过载拒绝影响玩家体验] → 先保护既有 actor 和权威一致性，所有拒绝可观测且有稳定原因；体验层的重试/退出由后续 control/network/client change 设计。

## Migration Plan

1. 先提交模型文档、长期 specs、参数目录和 fixture schema/manifest，不修改现有运行时代码。
2. 增加完整 cases、assumptions 与只读 validator，运行 schema、coverage、摘要和文档一致性测试。
3. 同步路线图状态但保持 B0.2、C++ core、UDP/KCP 和 Unity gameplay 门关闭；执行 `openspec validate --strict`。
4. 归档后，B0.2 只能向 profile-owned 参数填入测量值，不得静默改写模型；需要改变 command/state/pipeline 时建立新的 OpenSpec delta。

回滚时删除本 change 新增的 fixture/validator 和文档增量，恢复到 `define-authoritative-gameplay-architecture` 已归档的基线。由于本 change 不接线 runtime、协议或 listener，回滚不需要数据迁移，也不影响 Go/Unity v1。

## Open Questions

无阻塞问题。精确 tick/input cadence、映射窗口、history 长度、actor/profile 发布上限、snapshot 状态量和网络带宽均明确留给 `define-battle-network-profile` 通过本模型 corpus 测量后决定。
