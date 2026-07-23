## Context

iHomeland 已完成 Go 服务端与 Unity 客户端的个人世界、受控访客、HTTPS/WSS/TLS-TCP、MySQL/Redis 和资格验收。当前 `PersonalWorldScene` 仍是业务接入竖切，不具备服务器权威移动、跳跃、武器、怪物、Boss、物理、AI 或实时状态复制。

首个 gameplay 目标限定为：玩家进入自己的大世界后可以探索、移动、跳跃，使用一把近战剑和一把远程扇子攻击少量怪物与一只 Boss，并可邀请少量访客进入同一世界协作。暂不要求多人组队副本、匹配战场、跨服、观战、完整回放、经济闭环或大规模开放世界。

现有 Go 进程擅长控制面、持久事实和业务编排，但不应同时承担高频物理与战斗模拟。Unity Dedicated Server 不符合项目目标；C++ 模拟服需要独立演进，同时复用现有 Go 的账号、Session、PersonalWorld、VisitSession、准入和持久化能力。

本 change 只冻结架构和长期行为，不安装依赖、不开放 UDP 端口、不实现 gameplay。后续实现仍遵守 `simulation model -> network profile -> C++ simulation core -> Go/C++ control -> secure UDP/KCP -> network qualification -> Unity gameplay runtime -> content slice` 的有序进入条件，具体 change 名称与完成条件由 `docs/roadmap.md` 唯一维护。

## Goals / Non-Goals

**Goals:**

- 冻结 Go、C++ Game Simulation Server 与 Unity 的唯一权威和生命周期边界。
- 明确状态同步与帧同步技术如何组合，避免把“帧同步”误写成全世界 Lockstep 或完全遗漏。
- 冻结自研 ECS 与 GAS-like 的职责、最小特性和禁止扩张项。
- 明确 Asio、Jolt、Recast/Detour、KCP、CMake/CMake Presets 与 Cinemachine 的使用边界。
- 给后续网络 profile、协议、安全、模拟核心和客户端竖切提供可验收的前置契约。
- 保持现有 Go/Unity v1 兼容，并让第一条 gameplay 竖切优先产出可玩的移动和战斗。

**Non-Goals:**

- 本 change 不实现 C++ 工程、ECS、GAS-like、物理、导航、UDP/KCP 或 Unity 战斗代码。
- 不冻结精确 Tick、snapshot Hz、MTU、KCP 参数、插值延迟、历史帧窗口或带宽值；这些由 network profile 基于测量确定。
- 不采用要求 Unity 与 C++ 对完整世界产生逐位一致结果的确定性 Lockstep。
- 不建立通用 ECS 框架、反射系统、Job Scheduler、脚本 VM、通用事件总线或 Service Locator。
- 不引入 Unity Netcode for GameObjects、Mirror、FishNet、Photon、完整客户端 DOTS/ECS 或 Unity Dedicated Server。
- 不提前建立 Party、Room、ActivityInstance、匹配、战场、副本、观战、跨服和奖励结算。

## Decisions

### 1. 独立进程命名与所有权

正式能力名使用 `Game Simulation Server`，未来二进制名使用 `ihomeland-sim-server`；“Battle Server”可作为讨论简称，但不进入稳定协议类型。该命名覆盖个人世界、未来副本、战场和其他实时玩法，而不把进程限制成只处理战斗。

```text
Unity Client
  ├─ HTTPS/WSS/TLS-TCP ─> Go Control & Data Plane
  └─ Secure UDP/KCP ────> C++ Game Simulation Server

Go
  ├─ Account / Session
  ├─ PersonalWorld / VisitSession
  ├─ placement / admission / ticket
  ├─ MySQL / Redis
  └─ settlement owner

C++
  ├─ movement / jump / physics
  ├─ AI / navigation
  ├─ ability / effect / damage / death
  ├─ fixed-tick simulation
  └─ realtime replication
```

Go 通过内部认证控制契约分配、启动、准入、踢出、排空和停止模拟实例，并接收健康、租约和可持久化结果。具体内部 transport 在后续 change 冻结；没有独立扩缩容或故障隔离证据前不引入 gRPC。C++ 不直接修改账号、资产、奖励或持久世界最终事实。

现有 Go `PersonalWorld`、`WorldInstance` placement、`VisitSession`、admission 与 storage owner 保持不变。`placement.RuntimeController` 是迁移接缝：后续把当前只维护本地逻辑 registry 的 `processWorldRuntime` 替换为远程 C++ control adapter，`RuntimeNodeID` 映射到健康 `SimulationNode`，并将完整 `AssignmentStamp` 绑定到新 `SimulationInstanceID`。C++ 不创建第二套 PersonalWorld repository、assignment generation 或 VisitSession membership。

首个 gameplay 直接运行在 Owner 当前 PersonalWorld 的 WorldInstance 内；其中暂态普通怪物、Boss 和战斗属于 SimulationInstance，不以 ActivityInstance 为前置。只有副本、战场、剧情位面等拥有独立 lifecycle、admission、结果或匹配边界时，才另建 ActivityInstance。

替代方案：

- 继续由 Go 承担模拟：能复用运行时，但与 Jolt、固定 Tick、高频复制及未来 C++ 战斗核心目标不匹配。
- Unity Dedicated Server：开发便利，但违背独立 C++ 服务目标并强化 Unity runtime 绑定。
- 一个通用 “Battle Server” 进程拥有匹配、结算与模拟：职责过宽，会与现有 Go owner 冲突。

### 2. 服务器权威状态同步与帧同步技术融合

采用服务器权威状态同步作为安全和最终裁决模型，同时采用帧同步技术组织输入、模拟、预测、校正和重放：

```text
Unity InputTick N
  -> 本地立即预测
  -> 发送带 Tick/Sequence 的 InputBundle

C++ SimulationTick
  -> 在 Tick 边界提取、排序、校验输入
  -> 固定顺序运行系统
  -> 生成 Snapshot(ServerTick, LastProcessedInputTick)

Unity
  -> 本地角色回到确认帧状态
  -> 重演尚未确认输入
  -> 远端实体按快照插值
```

每个输入具有 `InputTick`、单调 command sequence 和必要的 prediction identity。客户端保留有界输入历史与预测状态历史；服务器快照确认已经处理的输入 Tick。误差超过 profile 容差时，客户端只校正本地可预测状态并重演未确认输入，不回滚整个服务器世界。远端玩家、怪物和 Boss 使用插值/有限外推，不在客户端执行权威 AI。

服务器保存有界历史帧，用于近战、远程攻击的延迟补偿查询、诊断和 replay evidence；历史帧不能恢复已经提交的持久事实，也不能让客户端指定任意回溯时间。命令流、随机种子、配置版本和 Tick 结果可形成可重放记录，但第一阶段不承诺跨 Unity/C++ 的逐位确定性录像。

替代方案：

- 完整 Lockstep：要求跨平台物理、浮点、AI 和容器迭代顺序完全确定，当前成本与风险过高。
- 纯快照且无帧输入/预测：实现简单，但移动、跳跃和战斗在真实 RTT 下手感不足，也难以诊断。
- 客户端权威移动/命中：手感直接，但不能满足商业化安全和协作一致性。

### 3. 固定模拟管线与单写者

每个 `SimulationWorld` 在任一 Tick 只有一个 simulation worker 写入。网络 I/O 线程只完成有限解析、ticket/session 校验、AEAD、反重放、速率限制和有界入队；不得直接修改 ECS 或物理世界。

初始显式系统顺序为：

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
```

结构变更在 command buffer 中延迟提交，避免系统迭代中改变 storage。第一版允许一个 simulation worker 承载一个或少量实例；不得预先实现每实体线程、通用并行 scheduler 或无测量依据的 Job System。

### 4. 自研、范围受限的 ECS

不引入 EnTT。项目自研 ECS 只提供：

- index + generation 的 `EntityID`
- 每个 world 独立的 component storage
- sparse-set 或经基准证明等价的密集存储
- 类型安全的 add/remove/get/has
- 最小 view/query
- deferred structural command buffer
- 固定 system pipeline
- world reset、snapshot/debug dump 和确定性测试入口

第一阶段明确不提供 archetype graph、反射、编辑器、序列化魔法、查询 DSL、自动网络复制、多线程 job、通用消息总线、service locator 和 plugin ABI。ECS 是内存组织与执行模型，不拥有网络、持久化或玩法配置。

### 5. GAS-like 构建在 ECS 之上

GAS-like 不是第二套 ECS，也不替代 ECS。长期语义包括：

- `AttributeSet`：生命、攻击、防御、韧性等当前属性
- `GameplayTag`：状态、免疫、武器和控制语义
- `AbilityGrant/AbilitySpec`：已授予能力、等级、输入槽与激活状态
- `GameplayEffect`：即时、持续、周期性修改和 tag 施加
- `Cooldown/Cost`：冷却和资源消费
- `GameplayCue`：跨端表现提示，不作为伤害事实

玩家、怪物、投射物与 Boss 仍是 ECS entity；Ability/Effect MAY 作为临时 entity 或稳定 handle，但只能通过显式系统修改 component。客户端只实现能力输入、预测状态、权威结果消费与 Gameplay Cue 表现，不复制完整服务器 GAS 规则。

### 6. 第三方 C++ 库通过 adapter 收口

- Asio：UDP socket、timer、endpoint 和异步 I/O；不实现状态同步、KCP、安全或游戏逻辑。
- Jolt：碰撞查询、角色/刚体物理和场景物理；ECS system 通过稳定物理 port 使用，不让 gameplay component 暴露 Jolt 类型。
- Recast/Detour：Recast 离线或构建期生成导航数据，Detour 在服务端运行时查询路径；AI 规则仍属于项目。
- KCP：只复用 ARQ core；socket、clock、安全 session、拥塞预算和消息过期由项目提供。
- CMake + CMake Presets：作为 C++ 唯一可重复构建入口，固定开发、测试、sanitizer 和 CI preset；不把本机 IDE 工程作为源事实。

第三方源码不复制进业务命名空间后任意修改。依赖版本、补丁方式、许可证、升级和回滚必须在首次实现 change 中登记。项目代码只能依赖窄 adapter 或 port，避免 Jolt/Asio/Detour 类型扩散至 gameplay domain。

### 7. 裸 UDP 与 KCP 共享安全 session

同一 C++ UDP listener 承载认证后的多 lane：

| Lane | 内容 |
|---|---|
| raw unreliable-sequenced | `InputBundle`、snapshot delta、transform/aim delta、clock/probe |
| KCP reliable-ordered | entity create/despawn、仍具时效的重要状态切换、能力授予/撤销、重连 resync |

连续 snapshot 不进入 KCP，避免丢失旧 segment 阻塞更新状态。移动与攻击 intent 可以放入带最近若干 Tick 冗余的 `InputBundle`，服务器按 sequence/Tick 去重并拒绝过期命令。一个 message id 只能登记一个 lane，不得在 raw UDP 与 KCP 双发同一业务消息；同一 lane 内的输入冗余不是跨通道双写。

首次 UDP/KCP 实现必须同时具备 HTTPS 短期 ticket、cookie challenge、AEAD、nonce/key epoch、replay window、endpoint binding/rebinding、每 IP/session 限流、畸形包快速拒绝、抗放大、MTU/分片 policy、网络模拟和带宽预算。KCP 与 raw UDP 共享底层 UDP，因此 UDP 被阻断时不得静默切换 KCP 或 TCP。

### 8. Unity 客户端不复制完整服务器架构

Unity 新增纯 C# `GameplayReplica`，保存当前可见 entity 的网络 identity、权威/插值状态、本地预测状态和表现 cue；它不是完整 DOTS/ECS，也不运行服务端 AI、伤害或奖励规则。

```text
Input System
  -> InputSampler
  -> LocalPrediction
  -> BattleNetworkClient
  -> SnapshotReceiver
  -> Reconciliation / Interpolation
  -> ActorView / Animator / VFX / Audio / HUD
```

`BattleNetworkClient` 继续遵守一个 channel owner、一个 receive owner、有界队列、generation/session epoch、主线程提交和逆序停止。底层使用平台 socket 与经过验证的 C# KCP core；不采用 NGO/Mirror/FishNet 的 NetworkObject/RPC/NetworkTransform 权威模型。

本地玩家的 camera 和 Actor View 跟随预测后的 presentation transform；远端表现跟随插值 transform；任何 Unity GameObject、Animator 或 Camera transform 都不能反向覆盖权威 gameplay state。

### 9. UI 与 Cinemachine 保持表现边界

现有 `ClientUiRouter`、UI Toolkit 页面和 scene-bound uGUI HUD 保持不变。战斗技能栏、准星、生命条、Boss 血条和世界空间标识优先由 uGUI Scene Host 承担；菜单、背包、设置和邀请继续使用 UI Toolkit。

Cinemachine 作为官方镜头表现依赖，通过唯一 Scene Scope `CinemachineCameraHost` 或等价 Host 消费纯表现 `CameraIntent`：

```text
Exploration | MeleeCombat | RangedAim | Cinematic
FollowTarget | LookAtTarget | FOV | Shoulder | Blend | Impulse
```

镜头 Host 不选择合法目标、不决定瞄准方向、不判定命中、不访问 socket，也不保存 ability 最终状态。输入系统产生的 yaw/pitch 与角色 aim intent 进入 gameplay；Cinemachine 最终 Camera transform 只用于渲染和构图。

### 10. 首个可玩竖切作为后续验收目标

后续实现顺序以一条纵向闭环为中心：

1. 单实例固定 Tick 下的移动和跳跃。
2. Unity 本地预测、服务器校正、远端插值。
3. 剑的近战 ability、命中、伤害和 cue。
4. 扇子的远程 ability、投射物/查询、伤害和 cue。
5. 少量怪物 AI 与一只 Boss。
6. Owner 与少量 Visitor 共享同一权威世界。
7. 网络模拟、断线恢复、安全、带宽和性能资格。

每一步必须有可玩的 Player 验收，不以建立通用框架、编辑器或未来系统数量作为完成条件。

## Risks / Trade-offs

- [自研 ECS 演变为通用引擎工程] → 冻结最小能力和禁止项；新特性必须由真实 gameplay profile 与独立 change 证明。
- [状态同步被误称为没有帧同步] → 协议、spec 和测试显式登记 `InputTick`、`SimulationTick`、确认帧、历史帧、回滚重演与命令流。
- [团队误以为采用完整 Lockstep] → 文档和消息语义明确服务器权威，客户端不模拟完整权威世界，跨端不承诺逐位确定。
- [Asio 网络线程与模拟并发产生竞态] → 有界 inbox/outbox 和单写 simulation worker；I/O callback 禁止访问 ECS/Jolt world。
- [KCP 队头阻塞污染连续快照] → snapshot 只走 unreliable-sequenced lane；KCP message 必须仍具 late-arrival value 和 expiry。
- [Go 与 C++ 出现双权威] → owner 表、内部命令、ticket/session epoch 与 settlement boundary 全部显式登记；C++ 不直接写持久最终事实。
- [Jolt/Detour/Asio API 扩散导致锁定] → adapter 收口、禁止第三方类型进入 gameplay component、协议和 application port。
- [客户端预测与服务器 Jolt 结果长期漂移] → 预测只覆盖允许的本地状态，快照持续确认，误差按 profile 校正；不要求客户端复刻完整服务器物理。
- [Cinemachine 侵入瞄准或战斗逻辑] → Camera 只消费 `CameraIntent` 和 presentation transform，服务器始终裁决 aim/ability/hit。
- [change 再次只产出基础设施] → 后续路线以移动、跳跃、剑、扇子、怪物、Boss 的纵向 Player 验收驱动，不先实现通用编辑器和大规模框架。

## 文档收尾审计

归档前以当前源码、资格文档、现有 flat protocol registry 和本 change 决策为证据，完成了以下漂移清理：

- 从总体架构的“服务端 v1”实例链移除未实现的 ActivityInstance，分开当前基线与未来活动模型。
- 用完整的 simulation model、network profile、C++ core、Go/C++ control、secure UDP/KCP、qualification、Unity 和内容纵切路线替换笼统两步，并把 C3 状态更新到 2026-07-23 modularity 后重新资格。
- 删除重复且冲突的旧 Unity 明细树；按源码登记 `Core/Configuration`、`Core/Presentation` 与当前/目标目录。Go 树补入已实现的 `identity`、`worldadmission`、`wscontrol` 和 `tcpgameplay`，移除不存在的独立 `worldinstance` package。
- 移除 `shared/contracts/registry/battle/` 第二套 registry 设想；后续 battle message 扩展现有 flat `messages.json`、`routes.json`、`errors.json`。
- 将 Redis semantic expiry、TCP heartbeat、客户端 App Scope、Session account/player projection 等描述更新为当前实现，移除已失效的“后续接线”、通用 message router 和独立 Account Service 表述。
- 将 Gameplay 客户端 owner 统一为 `GameplayReplica`，并明确 Asio 只用于 C++ 服务端 adapter，Unity 只对接 wire。

清理原则是：当前实现状态以代码和资格 owner 文档为准；长期目标以架构/spec 为准；README 与路线图只保留摘要和链接；历史 change 的“当时完成边界”不改写成当前状态。

## Migration Plan

1. 先同步本 change 的长期 specs 与架构/路线图文档，不改变现有 v1 runtime。
2. 创建 `define-battle-simulation-model`，冻结 entity/component/system、Tick、输入和最小能力语义。
3. 创建 `define-battle-network-profile`，通过可执行原型和网络模拟冻结频率、MTU、lane、带宽、历史窗口与校正容差。
4. 以独立 change 建立 C++ 工程、依赖治理、自研 ECS/GAS-like、Jolt/Detour adapter 和无 listener 模拟测试。
5. 以独立 change 用 `placement.RuntimeController` 远程 adapter 交付 Go/C++ control plane，先完成实例 lifecycle、fencing 和 result handoff。
6. 以独立 change 交付 secure UDP/KCP；未通过安全和网络资格前不开放 production endpoint。
7. 以独立 qualification change 固化网络模拟、安全负例、带宽与资源预算。
8. 以独立 Unity change 交付 BattleNetworkClient、预测/校正、Actor View、战斗 HUD 和 Cinemachine Host。
9. 最后交付两把武器、怪物、Boss 与 Owner/Visitor Player 验收，并把 battle qualification 加入发布门。

回滚本架构定义只需在任何 runtime 实现开始前撤销对应 specs/docs。实现开始后，各后续 change 必须保持可独立关闭的 endpoint/feature gate；Go v1 的 HTTPS/WSS/TLS-TCP 路径继续作为稳定回归基线，不因 C++ 模拟服不可用而伪造 gameplay 成功。

## Open Questions

- `SimulationTick`、input Hz、snapshot Hz、插值延迟、历史帧窗口和校正阈值的测量结果。
- Jolt Character/rigid-body 能力与客户端近似预测之间的最小共享运动模型。
- KCP C++/C# core 的具体实现、版本、补丁和跨端 fixture。
- AEAD 算法、key derivation、rekey/key epoch 与 NAT rebinding 的 wire 细节。
- Go↔C++ 内部控制协议采用内部 HTTPS 还是 Protobuf over TLS/TCP。
- 单进程多实例的 worker 分配、实例上限、CPU/内存预算和 drain 策略。
- Recast navmesh 的构建产物格式、版本和 Unity/C++ 内容 pipeline。
- 武器、技能、怪物和成长配置的 source schema 与 content compiler；该问题由后续配置 change 处理。
