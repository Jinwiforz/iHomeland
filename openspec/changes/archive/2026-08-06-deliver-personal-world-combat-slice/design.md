## Context

当前仓库已经具备可运行的 Go PersonalWorld/VisitSession 控制面、受监督 C++ `SimulationNode`、BattleTicket 与 secure UDP/KCP、固定 20 Hz simulation、Unity 40 Hz input/prediction、snapshot replication、Actor/HUD/Camera Scene hosts，以及一条已人工验证的 Owner/Visitor 移动和跳跃链路。C++ 中也已有 ECS、Ability、Effect、AI、HitDetection 等离线模型骨架，但 production `SimulationNode` 仍主要使用启动时 player state 与 flat-ground movement projection；客户端只显示通用 Actor，没有 production content identity、敌人、武器或战斗表现。

`define-gameplay-configuration-governance` 已提供 `gameplay-config-format-v1` closed schema、typed semantic ID、authority/presentation 分离和 governance-only reference package，但该 reference package 明确禁止 production 使用。现有 `battle.proto` 已预留 primary/secondary ability、reliable ability event、entity lifecycle、health 和 state flags；要支持重连后的完整内容投影，仍缺少武器切换 input、full snapshot 中的 archetype/equipped weapon/max health，以及 production semantic ID 与 numeric wire ID 的显式映射。

本 change 同时影响 Go、C++、Protobuf、Unity Scene/Prefab/ScriptableObject、内容 package 与质量工具，必须维持以下硬边界：C++ 独占实时伤害/死亡，Go 独占 PersonalWorld/VisitSession/assignment/settlement，Unity 只提交语义 intent 和呈现只读 projection；同一 command 不跨 transport 双写；普通 change 验证不得升级为完整最终资格。

## Goals / Non-Goals

**Goals:**

- 交付一份可部署、可回滚、三端 identity 一致的 PersonalWorld combat production package。
- 让 networked C++ `SimulationInstance` 在固定 pipeline 中真实执行剑、扇子、普通怪物、Boss phase、伤害和死亡。
- 让 current generation 的 Unity Player 能完整识别 actor/weapon/ability/cue，并以 Scene Scope 资产呈现战斗与 HUD。
- 让 Owner 从登录进入自己的 PersonalWorld，并可邀请 Visitor 在同一实例中协作击败 Boss。
- 用 closed validation plan、离线确定性测试、三端 contract/parity、Unity tests/build 和真实双 Player 场景证明当前新增行为。

**Non-Goals:**

- 不实现奖励、掉落、资产、持久战斗进度、Boss defeat settlement 或 PersonalWorld mutation。
- 不引入 ActivityInstance、Room、Party、匹配、副本、战场、观战或回放产品能力。
- 不实现通用 authoring editor、脚本 VM、行为树编辑器、完整客户端 GAS/ECS、Addressables 或 DOTS/ECS。
- 不新增 battle message ID、listener、端口或 transport；不运行完整 fault/capacity/security/lifecycle 矩阵、连续 verify、soak 或 finalize。
- 不宣称跨平台 physics 确定性、最终数值平衡、美术品质或发布资格。

## Decisions

### 1. Production package 与运行资产分层保存

跨 runtime 的 production source 放在 `shared/contracts/gameplay/battle/packages/personal-world-combat-v1/`，包含 `package.json`、`authority.json`、`presentation.json`、`bindings.json` 和 `wire-mapping.json`；schema 与通用 registry 继续由 `shared/contracts/fixtures/battle/gameplay-config/` 持有。package 使用 `production` classification、非 `fixture/` semantic namespace，并绑定 exact model/profile/wire、navigation 和 physics identity。

服务器专属 collision/nav source 或 baked data 放在 `simulation/content/personal-world-combat-v1/`，Unity Scene/Prefab/Animation/VFX/Audio/Sprite 和 tracked `ClientCombatResourceCatalog` 放在 `client/Assets/App/Modules/PersonalWorldCombat/Content/`。跨端 `presentation.json` 只登记 logical resource key；Unity catalog 负责 logical key 到真实 asset reference 的 build-time parity，不把 GUID/path 写回 authority package。

既有 Unity 工程采用功能模块优先的物理结构：`Assets/App/Modules/{Core,AppShell,Session,Networking,PersonalWorld,PersonalWorldCombat}/`。所有手写脚本、测试、Editor 工具与非代码内容迁入其唯一 owner 模块，模块内部继续按 `Foundation`、`Application`、`Infrastructure`、`Presentation`、`Runtime`、`Editor`、`Tests` 与 `Content` 分层。`Core` 只保存删除任何业务 feature 后仍需存在的生命周期、时间、输入、渲染和全局 UI 基础；没有明确复用证据时不预建 `Shared` 垃圾场。稳定 production/test asmdef 集中放在 `Assets/App/Assemblies/`，各模块用 Unity `.asmref` 加入既有程序集，保持原有 assembly DAG、`noEngineReferences`、InternalsVisibleTo 与 generated protocol 边界；不得仅为目录整齐拆出循环 feature 程序集。

迁移必须由锁定 Unity Editor 的 `AssetDatabase` 执行，保留 `.cs`、Scene、Prefab、Material、UXML、InputActions、rendering settings 与 project vendor resource 的非目录 asset GUID；一对一移动的文件夹同样保留 GUID。只表达旧拓扑且已确认无资产、无 GUID 引用的聚合/过渡文件夹由 Unity 删除并显式退役其 folder GUID，不能为了保留空目录身份污染新结构。旧 `Scripts`、`Editor`、`Tests`、`Scenes`、`UI`、`Config` 及顶层内容目录必须在清空后消失。导入到 `Assets` 的 TextMesh Pro/UI Toolkit project resource 独立放在 `Assets/App/ThirdParty/`，不冒充 `Core` 或业务 feature；`Assets/App/Generated/` 仍由统一生成工具独占，不迁入手写模块。`ProjectSettings`、`Packages` 与本地缓存也不伪装为业务 module。

手写 namespace 同步采用 `IHomeland.Client.<Feature>.<Layer>[.<Subarea>]`，例如 `IHomeland.Client.PersonalWorldCombat.Application`、`IHomeland.Client.Networking.Infrastructure.Tcp` 与 `IHomeland.Client.AppShell.Runtime.Bootstrap`。现有五个 production asmdef 继续表达 Clean Architecture layer DAG，因此 assembly name 不伪装成 feature 边界；各 asmdef 的 `rootNamespace` 统一为中性的 `IHomeland.Client`。跨 feature Composition 使用各自 feature namespace并由 AppShell composition root显式导入，Editor execute method、owner registry、tests和工具中的类型字符串必须同步。Unity序列化类型继续保留原脚本GUID并由Editor重载、Scene/Prefab missing-script审计与build smoke证明可恢复，不依赖字符串类名猜测。

选择该分层，是为了让 JSON 契约可由 Go/C++/Unity 同时校验，又不把 Unity asset、Detour/Jolt 数据或本机路径放入 `shared/`。不把 production package 放进 `fixtures/`，避免 governance-only corpus 被 runtime 误选；也不把权威数值复制到 ScriptableObject，避免形成客户端权威副本。

### 2. Go 选择 package，C++ 独立校验，实例只绑定 digest

Go Composition Root 增加严格的 production package selector：启动时读取明确配置路径和 expected package identity，校验 classification、manifest/source digest、model/profile/wire/nav/physics binding，并在创建 `SimulationNode` 前冻结选择结果。受监督 child 通过进程启动配置获得同一 package root，在 hello/ready 前由 C++ authority loader 独立校验所需 source、重算 identity 并加载 immutable typed catalog；绝对路径只用于本机启动，不进入 control frame、日志或 evidence。

现有 start/target/ticket 继续只传递 `ConfigIdentity`、`NavigationIdentity` 与 `PhysicsIdentity`，不在 control/UDP payload 中传 JSON 或路径。每个 `SimulationInstance` 持有 immutable catalog snapshot；package 变化只能由 Go 选择完整 predecessor/successor package，并通过更高 assignment generation 重建实例。当前 timeline 不支持 watcher、partial merge 或 hot reload。

这保留了 Go 的部署选择权和 C++ 的权威消费校验，同时避免把 PowerShell validator 作为 production runtime 依赖。替代方案是只信任 Go 生成的 validation receipt，但那会使 C++ 无法证明本地实际 bytes 与 ticket/config binding 一致，因此不采用。

### 3. 单独冻结 semantic-to-wire numeric mapping

`wire-mapping.json` 为需要上 wire 的 `actor`、`weapon`、`ability` 与 `projectile` semantic ID 分配非零 `uint32`，同一 package major lineage 内唯一、稳定且退役不复用。`projectile` numeric ID 作为其 lifecycle/full-state `archetype_id`，但仍保持 projectile semantic kind，不能伪装成 player/monster actor。C++ producer 只从该映射写出 `archetype_id`、`equipped_weapon_id` 和 `ability_id`；Unity build-time validator 必须证明每个 numeric ID 能唯一解析为 presentation semantic ID；Go 只绑定 mapping digest，不解释 gameplay 含义。

使用显式 registry 而不是对字符串做运行时 hash，可避免 collision、语言实现差异和退役 ID 复用；也不把 semantic string 放进每个 datagram，从而维持 1200-byte MTU 和现有 profile 预算模型。

### 4. 兼容扩展 battle wire，不新增 route

在 `BattleInputKind` 追加 `BATTLE_INPUT_KIND_SWITCH_WEAPON = 7`。它是离散 edge，不携带 target、weapon identity 或最终事实；服务器只在 current actor alive 且状态允许时按 production catalog 的稳定顺序在 sword/fan 间切换。`PRIMARY_ABILITY` 激活当前武器授予的 primary ability；`SECONDARY_ABILITY` 在本切片保持稳定 unsupported/rejected，不被挪作切换语义。

`BattleEntityState` 追加显式非零 `archetype_id`、允许 player 为非零且 non-player 为零的 `equipped_weapon_id`、以及正数 `max_health_milli`；`health_milli <= max_health_milli`。archetype 和 max health 在 entity generation 内不可变，只出现在 full/lifecycle initial state；`BattleEntityDelta` 增加 `equipped_weapon_id` 与对应 state-mask bit，武器切换可在 snapshot 中收敛。Lifecycle spawn 的外层 archetype 必须与 `initial_state.archetype_id` 相同。state flags 继续保留 bit 0-3 phase token、bit 4 grounded、bit 31 dead，Boss phase 只能使用已登记 phase token。

Message ID、direction、raw/KCP lane、AEAD/replay、KCP expiry 和 resync route不变。所有 proto、registry、profile size projection、canonical/malformed fixtures、Go/C++/C# generated binding 与 closed decoder 同步升级到新的 exact wire identity；旧 build 在发 BattleTicket/建 UDP 前因 binding 漂移 fail closed。选择扩展现有 full/delta/lifecycle，而不是新增 content snapshot route，因为这些字段属于 entity 当前状态且必须参与同一 baseline 原子提交。

### 5. Ability、命中与 cue 使用现有可靠事件和权威 snapshot 收敛

客户端首期不预测伤害、projectile 或 ability 结果；按 input 可做受限按键反馈，但正式动画/VFX/Audio 从 current generation 的权威 `BattleAbilityReliableEvent`、lifecycle 和 snapshot 触发。剑 activation 发布 started、带规范 target 集的 committed、completed；扇子发布 started、projectile spawn 时 committed，并在命中或 expiry 后发布带可选 target 的 completed。事件的 `ability_id` 通过 production mapping 解析为 cue；snapshot health/dead/phase 和 lifecycle 才是持续状态事实，cue 丢失或过期不能反向生成伤害或 entity。

不在本 change 新增通用 GameplayCue message。现有 ability/lifecycle event 已能表达本切片的稳定触发，新增泛化 cue payload 会扩大 registry、QoS 和兼容面；只有后续内容证明无法表达独立 cue 时，再提出单独协议 change。

### 6. C++ production encounter 复用唯一 Tick pipeline

`SimulationInstance` 启动后从 production encounter 配置创建一次确定的暂态 encounter：当前 BattleSession actor 使用 player archetype；固定少量普通怪物与一只 Boss 使用配置 spawn points、team、capsule、attribute、AI 和 phase。怪物/Boss/Projectile 与战斗状态都绑定 current AssignmentStamp/SimulationInstance，不创建 ActivityInstance，也不持久化 defeat。

输入先经 `InputTimeline` 解析为 move/aim/jump/switch/primary intent；AI 只产生同构 intent。系统继续按 `DrainInput -> InputIntent -> AIIntent -> AbilityActivation -> Movement -> Physics -> HitDetection -> Effect -> Attribute -> Death -> Replication -> CommitDeferredStructuralChanges` 单写执行。剑只在 active Tick 做一次服务器 shape sweep；扇子在 barrier 延迟创建 projectile，下一 Tick 起移动并做 sweep；friendly fire 默认关闭。Boss phase 由 health threshold 在下一 Tick 转换。死亡设置权威 flag、停止 ability/AI，并按配置的有界 corpse lifetime 产生 despawn；Boss death 只产生 transient encounter-complete projection。

正式基础 arena 使用 versioned collision source 与预烘焙 Detour navigation identity；Jolt/Detour adapter 必须规范排序和 fail closed。Unity Scene 几何只做同 identity 的可视映射和本地表现碰撞，不能成为 C++ source。替代方案是继续扩张 flat-ground adapter，但它不能证明基础障碍、projectile collision 与 AI navigation，因此不满足 B0.8。

### 7. Unity 只新增 pure C# content projection 与 Scene presentation

App Scope 在既有 `GameplayReplica`/projector 上增加 immutable content view state：archetype、weapon、health/max-health、ability phase、Boss phase、dead/stale。Input System 的新 `SwitchWeapon` action 和既有 primary action仍经唯一 input/focus owner进入 `GameplayPrediction` semantic command；Scene、HUD 或 Animator 不直接访问 transport。

Scene Scope `ClientActorViewRegistry` 根据 production numeric mapping 与 `ClientCombatResourceCatalog` 创建 player/monster/Boss/projectile Prefab；Animator、VFX、Audio、world health bar、uGUI player/skill/Boss HUD 和 Cinemachine intent只消费 `ActorViewState`、`HudViewState`、`GameplayCue`。UI Toolkit 继续拥有页面/overlay，uGUI battle HUD 不创建第二个 Router 或 action owner。unknown mapping、缺失资源、旧 generation event 或 invalid field均 fail closed：input gate关闭或该 actor降级为明确 unavailable，不猜测 Prefab/数值。

本切片使用 tracked Scene/Prefab/ScriptableObject 和基础项目内资产，不引入 Addressables。这样可在当前单场景规模下保持依赖和生命周期可审计；资源规模或远程分发证据成立后再单独评估 Addressables。

### 8. 恢复、成员资格和结算保持既有 owner

Battle-only reconnect 仍取得新 ticket、新 battle generation 和 full baseline；successor baseline 必须携带全部 archetype/weapon/max-health 当前事实，旧 cue、projectile View、Boss HUD 和 prediction 不得跨 generation 复活。assignment replacement 创建新 encounter timeline；Visitor safe-return 先关闭 input，再清理 battle Scene 状态，不能因 Boss 战或客户端失败伪造 VisitSession leave。

Boss defeat 不产生 `ResultProposal` 的奖励或持久 mutation。可以输出低敏、不可结算的 encounter lifecycle/evidence 摘要，但 Go 不据此发奖。该选择让战斗权威闭环与未来 economy/settlement 原子性解耦；奖励必须在独立 change 中定义 Player ledger、PersonalWorld mutation、幂等和 commit-unknown 语义。

### 9. 定向验证成为独立 closed quality check

change 创建 closed `validation.json`，引用现有 `quality-contract`、`gameplay-config-validate`、`battle-wire-validate`、`simulation-control-validate`、`client-battle-runtime-validate`、`proto-verify`、`client-battle-runtime-unity` 与 `openspec-change-strict`，并在 catalog 登记一个 `personal-world-combat-targeted` targeted-expensive check。该新 check 统一编排 production package consumer parity、C++ 离线/真实 runtime combat cases、Unity EditMode/PlayMode/build smoke 和真实 Go parent + C++ child + Owner/Visitor Development Player 的登录、邀请、协作击败 Boss、disconnect/reconnect、safe-return、replacement 与 teardown 场景。

真实场景使用隔离 credential、endpoint、process、evidence 和 cleanup，只记录 identity digest、稳定 reason、Tick/actor/counter 与低敏完成信号。它不生成 qualified report，也不调用 `quality.ps1 qualify`；完整 fault/capacity/security/lifecycle、连续 verify、30 分钟 soak 与 finalize 继续等待使用者显式冻结 clean HEAD。

### 10. App shutdown 按 owner 分工并避开 Unity 已接管的 Scene unload

`ClientPersonalWorldExperience` 在 AppLifetime 逆序停止时只撤销自身 intent、subscriber、generation 与 lifetime cancellation，不再逐条调用 Router 关闭产品 route；`ClientUiRouter` 继续是 active/cached route、Host、focus 与 input mode 的唯一 teardown owner。这样业务表现协调器不会在 Router 自身停止前制造第二条 route transition 链，也不会让重复 UI 清理占用整组 shutdown deadline。

`AppRoot.OnApplicationQuit` 必须先通过 Composition 通知 `ClientWorldSceneTransitionHost` 进入 application-quitting 终态，再启动既有非阻塞 AppLifetime 停止。该 Host 必须立即使 current/candidate generation、Context 与 SceneLifetime 不可提交，但不得在 Unity 已经接管 Player 退出或 Editor 离开 Play Mode 时再次等待 `SceneManager.UnloadSceneAsync`；Unity 仍负责实际 Scene 销毁。普通 safe-return、replacement 与显式 App stop 继续执行并等待登记 Scene 的显式 unload。选择该分支是因为 Unity 官方明确说明 Editor 离开 Play Mode 会调用 `OnApplicationQuit`，且此时退出流程已经开始；不能用同步阻塞或 `Application.wantsToQuit` veto 伪造可等待窗口。

### 11. PC 输入副本限定受支持设备，权威死亡终止本 generation 预测

tracked Input Actions 继续保存内容制作时的完整设备描述，运行时唯一 input owner 只在私有 clone 上应用非持久 binding override：PC gameplay 允许 `Keyboard&Mouse` 与 `Gamepad`，禁用通用 `Joystick`、`XR` 和 `Touch` group。这样虚拟 HID 或未校准通用摇杆持续上报非零 stick 时不会伪造移动，同时不手改 `.inputactions`、不污染共享资产，也不影响明确受支持的手柄。runtime clone 在 Play Mode 中延迟 `Destroy`，而 Editor 已退出 Play Mode 时使用 `DestroyImmediate`，遵守 Unity 对 edit-mode object 销毁的生命周期约束。

current local actor 的权威 snapshot 一旦提交 dead flag，`GameplayPrediction` 必须在同一原子提交中清除 pending semantic sample、未确认 input/predicted history 与 clock credit，以权威 transform 作为最终表现基点并关闭 input gate。死亡在同一 battle generation 内是单调终态，后续乱序或不完整状态不得重新开放输入；只有 successor battle/entity generation 的完整 baseline 可以重新激活。服务器仍独占伤害、死亡与 command 拒绝，客户端 gate 只负责不再产生无效命令和错误本地预测。

`last_processed_input_tick`的语义是server已连续终结的frontier，不只代表实际收到的command；`InputTimeline`也会按冻结的6个SimulationTick严格gap-expiry规则把缺失InputTick确定为neutral并推进ack。因此客户端接受上限必须取本地sent/显式skip frontier与由同一committed ServerTick精确计算的gap-expired frontier之大者，随后把未发送InputTick前向对齐，不能复用已被authority终结的identity。超过这两个数学边界、ack回退或ServerTick回退仍fail closed。该规则直接投影authority算法，不依赖snapshot数量、等待时间或“看起来稳定”等启发式状态。

### 12. BattleSession 参战资格在 SimulationTick barrier 成为显式权威输入

Production encounter 预留的 player actor slot 只表达容量和稳定 identity，不等于当前有玩家参战。`BattleTransportRuntime` 是 authenticated BattleSession 的唯一创建、接管和终结 owner；它必须把 exact `SimulationInstanceID + ActorID + BattleSessionGeneration` 生命周期通知给 `SimulationNode`。Node 为每个 instance 维护有界、generation-safe 的参战注册表，simulation worker 在每个 Tick 开始时冻结一次规范有序的 active actor set，并把它同时交给 movement 与 encounter pipeline。

未处于 active set 的 player slot 必须保留已有 health/death 等权威事实，但不得消费移动或 Ability intent、不得保留速度、不得成为 AI/伤害目标。Session 终结或被 successor 接管只改变下一 commit 的参战资格，不能回血、复活或重建 actor generation；同 actor 的旧 session 迟到终结也不能撤销更高 BattleSessionGeneration 的 successor。这样 disconnect/reconnect 仍恢复同一 encounter 当前事实，而 assignment replacement 才创建新的健康 S0。

不采用客户端进场回血、snapshot 到达后延迟 AI、按在线人数猜测、或让 `Consumed` ticket 永久代表连接存活。前两者会伪造服务器权威，后两者无法覆盖 UDP 无 close、protocol/backpressure 终结与 successor takeover。

### 13. PC gameplay 输入必须绑定单一设备集合，local Transform 只由 prediction presentation 驱动

Runtime private Input Actions clone 不再同时聚合系统中全部键盘、鼠标、实体手柄和虚拟 `Gamepad`。唯一 input owner 默认把 Player map 显式配对到 current Keyboard 与 Mouse；只有某个 exact Gamepad 产生新的明确按钮按下时才把 Player map 原子切换到该单一设备，后续键盘或鼠标明确输入再切回 Keyboard/Mouse。未配对设备即使属于允许的 `Gamepad` layout，也不能在当前键盘松开后成为 Move 的隐式后备值；通用 `Joystick`、`XR` 与 `Touch` 继续无法进入 Player map。设备切换在 Input System event callback 之后提交，并用 warmup frame 抑制重绑定产生的 aim/edge 瞬变。

设备限制必须作用于 Player map 的 runtime device set，而不是按 binding group 写空 override。Production Aim 的 `<Pointer>/delta` 同时登记 `Keyboard&Mouse;Touch`；按 `Touch` group 清空整个 binding 会连带删除 Mouse delta。显式只配对 Mouse 可让同一 tracked binding 保持可用，同时隔离 Touch 且不修改 `.inputactions` 或 `.meta`。

`GameplayPrediction` 输出的 local transform 是 Scene presentation 的唯一水平运动输入。`ClientActorViewRegistry` 只重建 immutable prediction transform 之间的有界表现轨迹，不能再并行读取 raw semantic Move 直接积分第二条 render motor；否则松键后的 authority reconciliation 会表现成角色自行追赶或反向滑动。Camera follow proxy继续读取同一个平滑Transform，semantic Aim只更新其朝向，不成为第二个位置owner。

不采用按设备名称屏蔽 `Nefarius`/`gvinput`、扩大摇杆 deadzone、松键时客户端强制改写 authority transform或按等待时间归零。名称黑名单不可移植，deadzone不能解决多设备所有权，后两者会隐藏而非修复输入与presentation双写。

### 14. Local prediction 的离散位置点必须重建为有界连续 render 轨迹

`GameplayPrediction` 按25 ms采样input，但同一50 ms SimulationTick只积分一次，因此稳定移动时local position通常以50 ms间隔提交新的immutable target。Scene registry不得把这些台阶直接交给Camera，也不得让`SmoothDamp`在每个台阶之间反复加速和减速。每个local actor entry必须保存唯一current presentation segment：新position或yaw target到达时，以当前可见pose为起点、immutable prediction pose为终点，在exact 50 ms SimulationTick窗口内按实际render delta推进；相同target不得重启segment，进度必须clamp到`[0,1]`并在终点精确停止。

该segment只在两个已知pose之间插值，不读取raw Move、不按velocity生成未知位置，也不在缺少新sample时越过最新target。Authority correction可以从当前可见pose原子retarget到新immutable endpoint，但不能硬写造成单帧跳变。Camera follow proxy与Actor继续消费同一个segment输出。选择该方案，是因为它把20 Hz位置台阶转换为frame-rate-independent连续轨迹，同时保留“停止后无新样本即不再移动”的可证明边界；不采用无限velocity extrapolation，也不采用追逐阶梯target的临界阻尼启停脉冲。

### 15. Fixed-timestep render 相位必须由 prediction owner 唯一拥有

真实Gameplay复核证明Decision 14把50 ms segment计时放在Scene registry仍然形成了第二条时间轴：Prediction在跨过25 ms InputTick边界时已经知道本SimulationTick的精确余量，但Scene只看到target到达的render frame并从零重新计50 ms。Render delta不能整除InputTick/SimulationTick时，这个相位损失会周期性产生停顿与追赶；snapshot reconciliation即使重演出等价target，也可能再次改变Scene segment起点。该结构与single presentation owner原则冲突，因此Decision 15取代Decision 14的Scene计时所有权，Decision 14只保留“不外推、不读取raw Move、只在已知端点间有界重建”的约束。

`GameplayPrediction`必须同时拥有current SimulationTick的start/end transform、该组最新InputTick与未消费clock credit，并在immutable snapshot中直接发布按精确相位插值的`PresentationTransform`。同组首个/第二个25 ms InputTick分别对应`[0,25)`与`[25,50)`毫秒区间；新SimulationTick从上一endpoint连续开始，等价reconciliation不得清零相位，continuity loss冻结current可信表现，death/full baseline则原子锚定authority。`GameplayPresentationProjector`把该值作为local Actor唯一Scene输入；`ClientActorViewRegistry`只做同帧量化坐标到Unity Transform的提交，不再保存计时器、速度或平滑状态。Camera follow proxy读取提交后的同一Transform。

不在Scene增加coroutine、`SmoothDamp`、阈值吸附或基于帧号的修正，也不把Unity `Time`、Physics或Camera状态回写prediction。这样25 ms输入采样、50 ms模拟、reconciliation与render cadence只共享一只纯C# monotonic timeline，可由EditMode精确断言波动frame delta下的每个毫米结果；Scene/Camera测试只验证同帧消费，不再重复实现时间积分。

### 16. Local replay 必须覆盖10 Hz snapshot之间的每个SimulationTick

真实Gameplay复核再次否定了Decision 15把send accumulator直接解释为render相位的部分。冻结network profile规定Simulation为20 Hz、snapshot为10 Hz，因此相邻正常publication的`ServerTick`通常前进2。客户端为避免TooEarly把新InputTick前向对齐到`latestServerTick + 2`，但旧replay只积分有frame的最后一个SimulationTick，漏掉中间由C++ `InputTimeline`按`continuous_hold_ticks=4`继续应用last Move的Tick。稳定3 m/s移动由此每100 ms少预测150 mm，下一authority publication再补回一步；运行日志的`max_target_rebase_mm=151`与该数学结果一致。Decision 16取代Decision 15的“send credit即render相位”来源，保留prediction owner唯一发布`PresentationTransform`与Scene无计时器的边界。

`GameplayPrediction`必须在authority基点和最新future frame之间逐Tick重演：有frame的SimulationTick继续按last Move/Aim与OR Jump从共同组起点只积分一次；没有frame的中间Tick必须按C++相同的闭区间continuous hold复用最后已知Move，超过4 Tick才neutral，yaw保持最后authority/predicted方向。Authority提交前先从已发送且映射不晚于该`ServerTick`的frame推进continuous基点，再裁剪ack history；这样10 Hz publication跨过的held Tick不会因history裁剪而丢失。

Input send accumulator与presentation timeline是prediction owner内两个命名清晰、不可互换的时钟。Authority gate等待时send accumulator最多保留一个25 ms credit，使放行后立即恢复一个sample但禁止同帧catch-up burst；presentation从current visible pose连接到完整predicted horizon，duration按`horizon SimulationTick - latest ServerTick`的剩余monotonic时间计算。新horizon、同Tick第二sample和reconciliation都只能从current visible pose retarget，不能暴露send backlog、Scene delta或raw semantic input。10 Hz回归必须证明Sim12 Move在Sim13 held并于Sim14继续后从150 mm预测到450 mm、100 ms内每25 ms前进75 mm，且下一authority450 mm产生零rebase。

### 17. Locomotion Animator 必须与 actor 位移根节点分离

真实Gameplay在Decision 16落地后记录`target_rebase_mm=0`与`max_target_rebase_mm=0`，证明authority publication与prediction horizon已经等价；剩余“模型相对地面抖”来自content表现所有权：四个production controller只有Idle/Ability/Dead三态，Player移动时仍循环播放`PlayerIdle`，该Clip会把`VisualRoot`垂直移动30 mm并在Y轴缩放2%。这不是网络位置correction，继续调整prediction、Camera或增加`SmoothDamp`会把一个Animator状态缺失伪装成移动算法问题。

`ClientActorViewState`必须只从同一immutable prediction/remote interpolation Transform的量化水平速度派生`Moving`；不得读取raw Move、Unity Transform差分或输入设备状态。全部production controller必须具有exact `Moving/Bool`参数与Idle/Locomotion/Ability/Dead四态：Idle可保留纯表现呼吸，Locomotion必须使用共享in-place Clip把`VisualRoot` position固定为零、scale固定为一，移动中仍可进入Ability，Dead继续拥有最高优先级。`Animator.applyRootMotion`保持关闭，任何状态都不得回写actor root或authority。

该资产契约由幂等Unity Editor authoring入口通过`AssetDatabase`与Animator API生成和校验；不得手改`.anim`、`.controller`或`.meta`。Build-time validator同时检查`Moving`参数、共享Locomotion Clip、六条常量Transform曲线与零Animation Event，防止内容改动重新引入第二个位置写入者。该方案保留静止呼吸和攻击动画，又在移动期建立明确locomotion语义；不采用运行时重置`VisualRoot`、禁用Animator、冻结`Animator.speed`或删除Idle曲线。

## Risks / Trade-offs

- **[Wire 扩展使旧客户端 closed decoder 拒绝]** → 同步更新 proto、wire/profile digest、三端生成和 startup binding；旧 build 在请求 ticket 前 fail closed，不做双版本同场兼容。
- **[战斗内容横跨 Go/C++/Unity，change 体量偏大]** → 以 production package、C++ authority、wire projection、Unity presentation、端到端验收五个可独立提交并可回滚的阶段实施；不混入 settlement、经济或内容工具。
- **[JSON schema validator 与三端 consumer 可能产生重复规则]** → schema/registry/identity 算法保持唯一 source；consumer 只实现其运行所需的 closed projection，并用同一正负 corpus做 parity，不各自发明默认值。
- **[Jolt/Detour arena 与 Unity Scene 视觉几何可能漂移]** → 以 map/nav/physics identity、versioned anchors 和 build-time parity固定映射；服务器几何始终权威，Unity 不上传碰撞结果。
- **[可靠 ability event 过期导致表现缺失]** → 持续事实由 snapshot/lifecycle恢复，cue 只做瞬时表现；缺失 cue 不阻止 health/dead/Boss phase 收敛，也不跨 transport 重发。
- **[没有奖励会削弱产品完成感]** → HUD 提供明确 transient encounter-complete 状态，但规格禁止声称结算；这换取首个战斗切片不越过尚未设计的经济/账本边界。
- **[基础资产质量有限]** → 验收聚焦可读性、owner 边界和可重复玩法，不把最终美术品质或平衡列为完成结论。

## Migration Plan

1. 先扩展 configuration schema/validator 与 production package，冻结新的 Config/Nav/Physics/Wire identity；此时 runtime 仍保持 movement-only。
2. 同步更新 battle proto/registry/profile/canonical fixtures并重建三端 ignored generated code；旧 binding 立即 stale，但在新 runtime 接线前不发布候选。
3. 接入 Go selector、C++ authority loader 和 production encounter；仅在 package 与全部 identity 精确匹配时使 node ready。
4. 先通过锁定 Unity Editor 把全部手写脚本、测试、Editor 工具和非代码资产迁入功能模块，使用集中 asmdef 与模块 `.asmref` 保持程序集和 GUID，再接入 Unity presentation catalog、Scene/Prefab/HUD/Input 与 client mapping，完成 pure/Unity 定向测试。
5. 通过 `quality.ps1 impact` 和 `check-change` 执行 closed validation，最后做真实双 Player 代表性验收；不运行最终资格。

回滚时整体撤销 production selector/package、combat pipeline wiring、wire binding 与 Unity 内容，恢复已归档的 movement-only B0.7 基线。已经登记并发布过的 semantic/numeric/protobuf identity 保留为 retired/reserved，不能在另一语义中复用；若尚未形成共享提交，则可随同整个 change 原子回滚。

## Open Questions

无。默认内容数量、数值和按键映射由 production package 与 Input Actions 在本 change 内冻结，并通过定向可玩验收调整；任何奖励/持久化、额外技能或新 transport 需求都必须另开 change。
