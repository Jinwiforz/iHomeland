# Authoritative Gameplay Simulation 规格

## Purpose

定义 C++ Game Simulation Server 的权威边界、状态同步与帧同步融合、自研 ECS/GAS-like 约束、历史帧证据和首个可玩竖切验收行为。

## Requirements

### Requirement: 实时 gameplay 必须由服务器权威模拟
C++ Game Simulation Server MUST 是实例内移动、跳跃、物理、AI、Ability、Effect、命中、伤害、死亡与实时 entity state 的唯一权威。Unity 客户端 MAY 预测本地表现，但客户端位置、Camera、Animator、VFX、命中声明或 payload identity MUST NOT 覆盖服务器结果；Go MUST 继续拥有账号、Session、PersonalWorld、VisitSession、持久事实与结算边界。

#### Scenario: 客户端报告与服务器物理不一致的位置
- **WHEN** Unity 预测位置穿过服务器碰撞体且后续权威 snapshot 返回合法位置
- **THEN** 客户端校正本地预测并重演仍未确认输入，C++ 不接受客户端 Transform 作为最终状态，Go 持久事实不因该预测直接改变

#### Scenario: Visitor 尝试提交 Owner 权限的战斗外事实
- **WHEN** Visitor 在实时 payload 中携带 Owner identity、奖励或持久世界 mutation
- **THEN** 连接绑定身份不能被 payload 覆盖，C++ 拒绝未登记实时 mutation 且不得直接提交资产、奖励或持久世界事实

### Requirement: 状态同步必须融合明确的帧同步技术
系统 MUST 使用固定 `SimulationTick` 与带帧编号的 `InputTick` 组织实时模拟。输入 MUST 携带单调 sequence、Tick 和过期语义；服务器 snapshot MUST 携带 `ServerTick`、snapshot sequence、baseline identity 与 `LastProcessedInputTick` 或等价确认帧。Unity MUST 为本地可预测状态保存有界输入/状态历史，在确认帧发生可校正误差时回到权威状态并重演未确认输入；远端 entity MUST 使用 snapshot 插值或 profile 允许的有限外推。第一阶段 MUST NOT 要求 Unity 与 C++ 对完整世界执行逐位一致的跨平台 Lockstep。

#### Scenario: snapshot 确认部分本地输入
- **WHEN** 客户端已预测到 InputTick 108，收到确认至 InputTick 104 的权威 snapshot 且 Tick 104 状态超过校正容差
- **THEN** 客户端从 Tick 104 权威状态重演 105 至 108 的仍有效输入，不回滚服务器世界，也不把当前 GameObject Transform 上传为裁决依据

#### Scenario: 帧命令迟到
- **WHEN** 服务器收到一个 sequence 合法但已经超出允许 Tick 窗口的输入
- **THEN** 服务器按冻结 profile 拒绝或降为无副作用诊断，不在当前 Tick 补执行已经过期的移动或攻击

#### Scenario: 远端 entity 缺失一个 snapshot
- **WHEN** Unity 丢失一个远端玩家或怪物的连续 snapshot delta
- **THEN** 客户端使用已有快照插值并等待后续可用 baseline/delta，不要求 KCP 重传已经被新状态覆盖的旧 snapshot

### Requirement: 模拟管线必须固定顺序且单写
每个 `SimulationWorld` 在任一 Tick MUST 只有一个 simulation worker 写入。网络 I/O、控制面 callback 与异步任务 MUST 只向有界队列提交已验证 command/event，不得直接修改 ECS 或物理世界。输入提取、意图、AI、Ability、移动、物理、命中、Effect、Attribute、死亡与 Replication MUST 具有冻结的显式顺序；entity/component 结构变更 MUST 在安全阶段延迟提交。

#### Scenario: 网络线程在 Tick 中途收到攻击输入
- **WHEN** Asio I/O thread 在当前 Tick 的 Physics 阶段收到并验证新的攻击输入
- **THEN** 输入只进入目标实例的有界 inbox，并在后续允许的 Tick 边界按确定顺序消费，不直接创建 Ability、修改生命或调用 Jolt world

#### Scenario: System 在迭代中销毁 entity
- **WHEN** Death system 判定一个 entity 应当销毁且其他 system 仍持有当前 Tick view
- **THEN** 销毁进入 structural command buffer，并在冻结提交点使旧 generation 失效，不破坏当前 view 迭代

### Requirement: 自研 ECS 必须保持项目特定和范围受限
C++ 模拟核心 MUST 使用项目自研 ECS，至少提供 generation-safe EntityID、per-world component storage、类型安全 component 操作、最小 view/query、deferred structural command 与固定 system pipeline。ECS MUST NOT 自动拥有 socket、协议、持久化、配置加载或网络复制；没有独立 profile/change 证据时 MUST NOT 引入通用 archetype graph、反射、编辑器、查询 DSL、自动序列化、多线程 Job Scheduler、全局 EventBus 或 Service Locator。

#### Scenario: 旧 EntityID 在 slot 复用后到达
- **WHEN** 一个 entity 已销毁且相同 index 被新 generation 复用，旧 command 随后引用原 EntityID
- **THEN** ECS 通过 generation 拒绝旧引用且不修改新 entity

#### Scenario: gameplay system 需要物理查询
- **WHEN** Movement 或 Hit system 需要执行 sweep、overlap 或 ray query
- **THEN** system 通过项目物理 port/adapter 调用 Jolt，gameplay component 与公开 system contract 不暴露 Jolt 专有类型

### Requirement: GAS-like 必须建立在 ECS 之上
Attribute、GameplayTag、Ability、GameplayEffect、Cooldown、Cost 与 GameplayCue MUST 作为 ECS component、稳定 handle、临时 entity 或显式 system 语义实现，不得形成第二套 entity 生命周期或绕过 ECS 直接修改世界。服务器 MUST 裁决 Ability 激活、资源、冷却、Tag、Effect、命中与 Attribute 结果；Unity MAY 预测已登记的本地能力并播放 GameplayCue，但 MUST 以权威确认、拒绝或校正收敛。

#### Scenario: 剑 Ability 激活成功
- **WHEN** 当前 entity 已授予剑 Ability、资源充足、冷却完成且 Tag policy 允许激活
- **THEN** Ability system 在当前 Tick 产生明确激活实例/状态，由后续命中、Effect 与 Attribute system 按固定顺序结算并生成表现 cue

#### Scenario: 客户端预测扇子技能被拒绝
- **WHEN** Unity 已预测播放扇子技能，但服务器判定冷却未完成或输入已经过期
- **THEN** 服务器不产生伤害或投射物最终事实，客户端撤销或平滑收敛预测状态且保留权威拒绝对应的表现语义

### Requirement: 历史帧与重放证据必须有界
Game Simulation Server MUST 保存 profile 定义的有界历史状态和已接纳帧命令，用于延迟补偿、诊断和重放证据。历史查询 MUST 受当前连接身份、服务器 Tick 窗口与玩法 policy 约束；客户端时间戳 MUST NOT 任意选择回溯状态。重放输入 MUST 绑定模拟版本、配置/content hash、随机种子和 Tick 语义，并 MUST NOT 被解释为已经完成持久化或结算的证明。

#### Scenario: 高延迟近战请求延迟补偿
- **WHEN** 已认证玩家提交仍在允许窗口内的攻击 intent 且服务器需要检查目标历史 hit volume
- **THEN** 服务器只在受限历史窗口和当前权威身份下查询过去状态，按当前玩法 policy 产生结果并记录使用的 ServerTick

#### Scenario: 请求超出历史窗口
- **WHEN** 攻击 intent 指向已经淘汰的历史 Tick
- **THEN** 服务器拒绝延迟补偿，不扩张历史缓存、不接受客户端提供的旧位置，也不回滚已经提交的世界结果

### Requirement: 首个 gameplay 竖切必须以可玩行为验收
后续 gameplay changes MUST 依次形成可运行的移动/跳跃、剑近战、扇子远程、少量怪物、一只 Boss 与 Owner/少量 Visitor 协作闭环。每个阶段 MUST 提供服务器模拟测试、跨端 contract/fixture、网络条件测试和 Windows Player 可玩证据；通用编辑器、脚本 VM、完整经济、Party、Room、匹配、副本或战场 MUST NOT 成为该闭环前置条件。

#### Scenario: 第一条完整 gameplay 验收
- **WHEN** Owner 与 Visitor 通过现有 PersonalWorld/VisitSession 准入进入同一模拟实例
- **THEN** 双方能在服务器权威下移动、跳跃、使用剑和扇子攻击怪物与 Boss，并在预测校正、丢包和正常断线条件下看到一致可收敛结果
