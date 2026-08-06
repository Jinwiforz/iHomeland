# iHomeland 路线图

## 文档职责

本文档定义 iHomeland 的严格交付顺序、OpenSpec change 边界、进入条件和完成条件。第一业务主线的 Go/Unity v1 已完成资格验收；该门禁作为回归基线保留，当前后续主线是 PersonalWorld 内的服务器权威 gameplay。

## 全局依赖

```text
A0 Project Baseline
  -> S0 Contract Foundation
  -> S1 Server Runtime
  -> S2 Session Core
  -> S3 Account Core
  -> W0 PersonalWorld Domain
  -> W1 WorldInstance Placement Model
  -> D0 MySQL / Redis Runtime
  -> W2 PersonalWorld Storage + Placement Adapters
  -> V0 VisitSession Domain
  -> P0 PersonalWorld + Visit Protocol
  -> D1 Account / Session Storage
  -> V1 VisitSession Storage
  -> N0 Admission + HTTPS + WSS + TLS-TCP
  -> Q0 Server v1 Qualification
  -> C0 Unity Runtime
  -> C1 Unity Network + Account
  -> C2 Own-world + Visit-world Services / UI
  -> C3 Client Qualification
  |
  +-> B0 Authoritative Gameplay Architecture
  |    -> Simulation Model -> Network Profile
  |    -> C++ Core -> Go/C++ Control -> Secure UDP/KCP
  |    -> Network Qualification -> Unity Runtime -> Content Slice
  |
  +-> optional A1 ActivityInstance Model
       -> optional Room / Party
```

## A0：项目基线

### `establish-project-baseline`

**目标：**建立个人世界产品边界、服务端优先顺序、Go 分层、五种通道、Unity 混合运行时、工程标准和文档体系。

**产出：**

- PersonalWorld、WorldInstance、VisitSession 与 ActivityInstance 所有权
- 服务端权威、持久事实/运行态分离和 transport-independent domain
- Unity AppBootstrap/AppComposition/AppRoot、App Scope/Scene Scope 与双 UI 原则
- 协议、网络、存储、文件结构、注释、流程和 Git 规范
- 长期 specs 与后续 changes 的进入/完成条件

**完成条件：**OpenSpec strict 通过，主 specs 已同步，文档入口完整且无运行时代码、Unity 资产或生成协议。

## S0：基础协议治理

### `establish-server-contracts`

**目标：**建立可重复生成和验证的跨端契约基础，不提前冻结业务领域消息。

**产出：**

- `common/account/session/control` Protobuf packages
- OpenAPI account/bootstrap contract
- message/error/route registries 与 owner 治理
- reliable envelope 与 TCP framing
- session、epoch、token、ticket、endpoint manifest
- WSS `CONTROL` 与 TLS/TCP `GAMEPLAY` connection scope
- contract fixtures、negative cases 与 deterministic golden
- `versions.yaml` 与统一 `tools/proto/proto.ps1`

**完成条件：**schema/registry/fixtures 可重复生成与验证，基础契约不包含 world、visit、activity、room、party 或 battle 占位消息。

## S1：服务端运行基础

### `establish-server-runtime`

**目标：**建立可启动、可诊断、可回滚和可关闭的唯一 Go Composition Root。

**产出：**严格配置、结构化日志、metrics、clock/ID、lifecycle、readiness、受控任务、独立诊断 listener、graceful shutdown 和 process tests。

**完成条件：**无业务 handler 也可启动/关闭；部分初始化失败逆序释放；退出码、日志、metrics 和 readiness 可诊断。

## S2：统一会话与安全

### `establish-server-session-core`

**目标：**建立 HTTPS、WSS、TLS/TCP 和未来 UDP/KCP 共用的唯一 session 身份事实。

**产出：**

- opaque access/refresh token、digest-only 边界和原子轮换
- session epoch、refresh replay lineage invalidation
- 一次性 connection ticket、16-byte nonce、endpoint/channel/epoch 绑定
- WSS control 与 TLS/TCP gameplay 固定 scope policy
- 只读 AuthContext 和 transport-independent connection invalidation
- expiry、重放、并发和脱敏测试

**完成条件：**payload 不能覆盖 AuthContext；ticket 不能跨通道、endpoint、epoch 或重复消费；`GAMEPLAY` 不替代任何业务 admission 或 mutation authorization。

## S3：账号核心

### `establish-server-account-domain`

**目标：**实现 transport-independent 账号领域与 register/login application service。

**产出：**AccountID/PlayerID、username/displayName 规范化、credential 边界、原子 repository、SessionIssuer、并发注册、枚举防护和部分成功恢复测试。

**完成条件：**纯 Go 测试不启动 listener/MySQL/Redis；正式 Composition Root 不注入 reference repository 或 fake hasher。

## W0：PersonalWorld 领域

### `establish-server-personal-world-domain`

**目标：**建立 `PersonalWorldID`、immutable `WorldOwnerID`、持久 revision、生命周期和子领域所有权。

**进入条件：**Account/Player identity 与 session principal 稳定。

**产出：**

- primary PersonalWorld 创建与 hydration invariants
- PlayerState/PersonalWorldState/ActivityInstanceState 分界
- world mutation owner、expected revision、idempotency 与 failure semantics
- 纯 Go domain/application tests

**完成条件：**PersonalWorld 不包含 socket、Visitor connection、地图模拟、奖励双写或万能 aggregate。

## W1：WorldInstance placement 模型

### `establish-server-world-instance-placement`

**目标：**定义按需承载 PersonalWorld 的运行实例，并保证最多一个 active writable instance。

**进入条件：**PersonalWorld identity 与 lifecycle 稳定。

**产出：**WorldInstanceID、assignment generation、placement contracts、lease/fencing、启动/休眠/重建/迁移和 stale instance 拒绝测试。

**完成条件：**运行位置不进入 PlayerID/PersonalWorldID，客户端旧 endpoint 或 InstanceID 不能恢复写资格。

## D0：数据运行时

### `establish-server-storage-runtime`

**目标：**建立 MySQL/Redis 生产资源、migration、transaction、key registry 与恢复框架。

**进入条件：**Account、PersonalWorld、Session 与 Placement 消费侧接口稳定。

**产出：**MySQL pool/migrator、Redis client、transaction/retry policy、key builders、TTL/value schema、Docker integration harness 和 Composition Root lifecycle wiring。

**完成条件：**不注入 memory adapter；依赖失败不返回假成功；Redis flush 不删除持久事实；空库 migration 可重复执行。

## W2：个人世界持久化与实例适配

### `establish-server-personal-world-storage`

**目标：**实现 PersonalWorld 持久事实和 WorldInstance 可恢复运行态。

**产出：**

- PersonalWorld MySQL schema、repository、revision transaction
- WorldInstance assignment/lease/fencing Redis adapter
- duplicate instance、commit-unknown、Redis flush、MySQL restart tests
- archive idempotency replay；无已定义 consumer 时不预建 outbox

**完成条件：**只有 current active、lease 有效且完整 stamp 匹配的实例可以取得 point-in-time `WriteFence`；未来真实 world mutation 必须在最终 MySQL commit boundary 重新验证该 fence，Redis 丢失只能要求以更高 generation/fence 重建运行态。

## V0：访客会话领域

### `establish-server-visit-session`

**目标：**建立 Owner 邀请 Visitor 进入当前 WorldInstance 的 transport-independent 状态机。

**进入条件：**PersonalWorld owner 与 WorldInstance assignment 可独立查询和测试。

**产出：**VisitSessionID、immutable owner/world/assignment binding、Visitor membership/capacity/revision/expiry、invite/accept/join/leave/kick/reconnect/expire、Owner grace、safe-return、权限策略、expected revision 与完整 replay/commit-unknown store 契约。

**完成条件：**纯 Go domain/application 的 unit/table/fuzz/race 验收通过；Visitor 不能继承 Owner、伪造 world/instance、推进未授权世界事实或通过 invite/AdmissionIntent 直接取得 gameplay credential。Production Redis、protocol、admission、transport、cleanup 与 Composition Root 接线仍留给后续 change，V0 完成不表示 visit-world 已开放。

## P0：个人世界与访客协议

### `establish-server-personal-world-protocol`

**目标：**在领域语义稳定后冻结 own-world 与 visit-world 的唯一跨端契约。

**进入条件：**V0 的 snapshot、AdmissionIntent、safe-return、角色与 stale assignment/epoch 语义已归档；不得用协议字段重新定义这些 owner 事实。

**产出：**

- world/visit Protobuf packages 与 owner ranges
- bootstrap、assignment、invite、admission、snapshot、VisitSession command 和 safe-return schema
- message/error/route registry 与唯一 allowed channel
- gameplay ticket 与一次性 world/visit admission 的明确分层
- deterministic request/response/error/push fixtures 和 negative cases

**完成条件：**payload identity 不能覆盖 AuthContext/admission；同一业务消息无 WSS/TCP 双入口；fixtures 与 Go validator 全部通过。

**P0 完成边界：**该阶段只形成可生成、可登记、可严格验证的 source contract 与 deterministic fixtures；production adapter、admission 和 Composition Root 接线由 N0 交付，Go 协议客户端由 Q0 交付。未登记 generic world interaction 继续默认拒绝。

## D1：账号与会话存储

### `establish-server-account-session-storage`

**目标：**在公开 HTTP/WSS/TLS-TCP 之前交付账号持久化、密码哈希与会话原子运行态。

**进入条件：**Account/Session 消费侧接口、共享 MySQL/Redis runtime 与公开 world/visit protocol 已稳定。

**产出：**单表 Account MySQL repository、固定 Argon2id v19 profile、Session Redis Hash schemas 与 owner Lua scripts，以及密码资源上限、commit-unknown、refresh replay、ticket consume、epoch invalidation、restart/flush/corruption 集成测试。

**完成条件：**账号事实只进入 MySQL，session/token/ticket 只以 digest 和可失效运行态进入 Redis；所有原子操作经真实 Docker storage 验收。正式 Composition Root 仍只应用 migration，不构造账号/session service graph，也不开放业务 route。

## V1：访客会话生产存储

### `establish-server-visit-session-storage`

**目标：**在公开 world/visit transport 之前交付 VisitSession 可失效运行态与完整命令重放。

**产出：**VisitSession active/session/command Redis schemas、owner Lua create/CAS、absolute TTL、完整 safe-return replay，以及并发、response-loss、restart/flush/corruption 集成测试。

**完成条件：**production adapter 忠实实现 `VisitSessionStore`；Redis 进程重启可读取自身仍保留的合法运行态，丢失后不补回旧资格；正式 Composition Root 仍不构造 VisitSession service、cleanup task 或公开 listener。

## N0：公开通道与安全接入

### `establish-server-world-admission-runtime`

独立交付短期一次性 opaque world admission issuer/verifier、credential digest 与 consume identity 原子消费、session epoch/assignment/endpoint/channel 绑定及 semantic fixture + VisitSession 组合验收；不得在 HTTP 或 TLS/TCP handler 中顺带发明凭据语义。

### `add-server-http-bootstrap`

**进入条件：**D1 的 Account/Session storage、V1 的 VisitSession storage、P0 的冻结 HTTP 契约，以及 PersonalWorld、Placement、VisitSession、WorldAdmission runtime 均已完成并可由 production adapter 构造。

**产出：**交付 version/config/register/login/refresh/logout、connection ticket、world bootstrap、invite accept 和 admission issue；handler 只做 decode/validate/authorize/call/encode，公开 listener、storage 与 service graph 由唯一 Composition Root 管理。

**完成条件：**10 个冻结 operation 全部接入真实 production graph，并通过 handler、contract、race、真实 MySQL/Redis 与 HTTPS lifecycle 验收；WSS/TLS-TCP listener、credential consume 和 world mutation 仍明确未完成。

### `add-server-websocket-control`

交付认证 WSS control、maintenance/forced logout/endpoint update、visit invite/Owner availability/assignment/close notice，以及 bounded queue、deadline、slow consumer 和 connection storm tests。WSS 不接收 world mutation，也不发送 TLS/TCP safe-return 的替代消息。

**完成边界：**与 10-operation HTTP router 复用唯一公开 listener，以一次性 Redis ticket 建立 `CONTROL` 连接；9 类已登记 PUSH 通过 typed codec 可投递，production 只真实接线 Session invalidation，不制造尚不存在的业务 producer。连接 registry 只保存引用，并在 shutdown 时先于 HTTP 和 storage 关闭。TLS/TCP、world admission consume 与 world/visit mutation 继续留给后续 change。

### `add-server-tcp-gameplay`

交付 TLS/TCP framing、gameplay ticket consume、world/visit admission、dispatcher、pending target、push、backpressure、rate/idempotency 与 reconnect/shutdown tests。

**完成边界：**独立 TLS 1.3 listener（local/test loopback 明文例外）使用固定 `IHTP` preface 依次消费 GAMEPLAY ticket 与 WorldAdmission，按冻结 registry 接入 world/visit snapshot、mutation response/error 和三个 typed PUSH。Connection registry、双预算队列、组合 session invalidation、受监督生命周期及真实 Redis TCP `OWN_WORLD`/`JOIN`/`RECONNECT` 已接线；完整业务 producer、cleanup/orchestration 与 Go 资格客户端仍属于后续 change。

### `complete-server-personal-world-slice`

**进入条件：**Account、Session、PersonalWorld、Placement、VisitSession、WorldAdmission、MySQL/Redis runtime 与 HTTPS/WSS/TLS-TCP adapters 已分别完成并通过自身 contract/integration 验收。

**产出：**连接 Account、Session、PersonalWorld、Placement、Storage、VisitSession 与三个公开通道；增加有界 WorldInstance runtime、assignment lease owner、单 worker semantic deadline owner、TCP lifecycle coordinator、跨通道 result effect 与精确 safe-return，交付 own-world 和 visit-world 的端到端服务端竖切。

**完成条件：**真实 MySQL/Redis、临时 TLS 与 production Composition Root 下，wire client 可以完成 bootstrap/admission/snapshot、invite/accept/join、断线恢复、leave/kick/close；stale admission、assignment replacement、Redis flush、process reconstruction 和逆序清理 fail closed。该完成条件不包含独立资格客户端、全量压力/故障矩阵或 Unity runtime；前两项由 `qualify-server-v1` 接管，Unity runtime 必须等待 C0 之后的 change。

## Q0：服务端 v1 资格验收

### `qualify-server-v1`

**进入条件：**S0-S3、W0-W2、D0-D1、V0-V1、P0、N0 全部完成。

**验证：**

- Go HTTP/WSS/TCP clients 与 versioned fixtures
- register/login、进入自己的世界、重复进入与 assignment 重建
- invite/accept/join/leave/kick、Owner grace、safe-return
- stale ticket/admission/endpoint/instance/epoch/lease 全部 fail closed
- Redis flush、MySQL restart、duplicate instance、commit-unknown
- unit/integration/contract/fuzz/race、背压、slow consumer、connection storm
- graceful shutdown、process restart、logs/metrics 与文档一致性

**完成条件：**全量自动化通过，无未接线 production adapter 或无 owner message/table/key/listener，并输出允许启动 C0 的资格报告。

Q0 的唯一完整入口、冻结 digest、分层证据、报告语义与长期 regression client 演进规则见 `docs/server-v1-qualification.md`。`contract` 或 `blackbox` 排障动作不能产生资格结论；只有连续可重复的完整 `verify` 和全部 owner/文档门禁可以解锁 C0。

**当前状态：**`qualify-server-v1` 已通过完整资格验收并于 2026-07-16 归档，C0 进入条件已满足。

## C0：Unity 运行基础

### `establish-client-runtime`

**进入条件：**Q0 完成。

**产出：**Unity PC baseline、minimal BootstrapScene、AppBootstrap/AppComposition/AppRoot、App/Scene Scope 生命周期组件、Unity Host 边界、rollback/reverse shutdown 和 EditMode/PlayMode lifecycle tests。

**当前状态：**`establish-client-runtime` 已完成 EditMode 26/26、PlayMode 4/4 与 Windows Development build 验收，并于 2026-07-16 归档。

### `generate-client-protocol-baseline`

生成 C# protocol，验证 Go/C# golden parity；generated code 保持忽略且可在 Unity 编译前重建。

**当前状态：**已完成统一 Go/C# generation、锁定 `Google.Protobuf` 恢复、EditMode 32/32、PlayMode 4/4 与 Windows Development build 验收，并于 2026-07-16 归档；`add-client-http-bootstrap` 的进入条件已满足。

## C1：客户端网络与账号

### `add-client-http-bootstrap`

实现 version/config/register/login/refresh/logout/ticket/world bootstrap，含 timeout/cancel/error mapping 和安全凭据处理。

**当前状态：**已完成冻结八 operation、唯一 Session owner、ticket 单次交付、EditMode/PlayMode 与 Windows Development build 验收，并于 2026-07-16 归档；`add-client-websocket-control` 的进入条件已满足。

### `add-client-websocket-control`

实现 WSS receive pump、控制通知、visit invite、epoch invalidation、主线程投递、重连与关闭。

**当前状态：**已完成只接收 WSS control owner、9-route typed PUSH、有限自动恢复、session invalidation 与逆序关闭，并已归档；`add-client-tcp-gameplay` 的进入条件已满足。

### `add-client-tcp-gameplay`

实现 TLS/TCP framing、single reader/serialized writer、pending correlation/typed PUSH dispatch、gameplay admission、backpressure 和 close reasons。

**该 change 归档边界：**HTTP world admission、TLS 1.3/loopback transport、`IHTP` preface、冻结 gameplay route、双向 sequence、有界 pending/writer、typed PUSH、safe-return gate、session invalidation 与 App Scope 逆序停止已经落地；PersonalWorld/VisitSession 最终状态和业务流程由 C2 Services change 接续，UI、Scene 与自动恢复不属于该 change。

## C2：个人世界客户端竖切

### `establish-client-personal-world-services`

实现纯 C# PersonalWorld、WorldInstance、VisitSession、WorldAdmission Services，以及 OwnWorld/JoiningVisit/Visiting/ReturningOwnWorld 状态机。

**该 change 归档边界：**十个强类型 HTTP operation、不可变 world/visit/invite 投影、revision/generation gate、有界 control hint、Owner/Visitor command policy、credential 内聚的 JOIN/RECONNECT 窄入口、target 状态机与 App Scope 逆序停止已落地。默认初始化保持零网络副作用；UI、SceneContext、Prefab、资源加载、跨进程 token 恢复与独立通道自动恢复不属于该 change。

### `integrate-dual-ui-routing`

按页面适配度选择 UI Toolkit/uGUI，统一 screen owner、layer、input、focus 和 lifecycle；邀请/访问列表优先评估 UI Toolkit，世界空间 UI 使用 uGUI。

**当前状态：**已完成统一 route owner、双 Host、Input/focus、生命周期、Unity 全量测试与 Windows Development build 验收，并于 2026-07-17 归档；`add-client-personal-world-vertical-slice` 的进入条件已满足。

**该 change 归档边界：**已落地纯 C# 有界事务 route、空 production registry、稳定跨 framework layer、generation/cancellation、唯一 Input System clone owner，以及直接引用的 UI Toolkit/uGUI Host adapters；产品 UXML/USS/Prefab、Presenter、SceneContext、资源系统与业务网络动作由后续产品 change 接入。BootstrapScene 直接引用、Unity 全量测试与 Windows Development build 均已完成。

### `add-client-personal-world-vertical-slice`

交付登录、进入自己的世界、邀请/接受、Visitor 模式、Owner grace、离开/踢出、安全返回、SceneContext generation/cancellation 和 PC 输入/分辨率验收。

**当前状态：**已落地个人世界 Experience/View State/action boundary、五个 production routes、UI Toolkit/uGUI 产品 binding、封闭 PersonalWorldScene transition/Context、Composition 生命周期接入、产品 UXML/USS、WorldHud Prefab、PersonalWorldScene 与 BootstrapScene 接线；Unity 分层测试、Windows Development Player、双客户端访问闭环和显示输入矩阵已经验收。跨进程 token 恢复、独立通道自动恢复和内容资源系统不属于本 change。该 change 已于 2026-07-21 归档，C3 进入条件已满足。

### 竖切验收派生修复

`add-client-personal-world-vertical-slice` 的真实双客户端与故障恢复验收暴露了四个具有独立 owner、失败边界和验收场景的问题，因此分别建立 OpenSpec change，而没有把服务端、协议与客户端修复混入主竖切：

- `recover-stale-visit-session-on-open`：Owner 显式 Open 以 current assignment 证据确定性退役 Redis 中绑定旧 AssignmentStamp 的 active VisitSession，再以原 CommandID 解析唯一新会话；不依赖清库、timer、tick 或重复点击。
- `validate-visit-invite-target`：Account owner 在 CreateInvite 首次提交前判定目标是否为非 Owner 的 active Player；self、missing 与 inactive 统一低敏拒绝且不推进 revision，不暴露账号枚举信号。
- `retire-stale-visit-invites`：既有 WSS message 2100 以 `RETIRED` tombstone 发布精确邀请退役事实，客户端按完整 identity 删除 inbox/selection；明确 accept 拒绝保持 OwnWorld 且不伪装进入成功。
- `recover-idle-gameplay-connection`：TLS/TCP gameplay 增加独立 heartbeat `1/2`，静默连接使用同一 pending/writer/generation owner 保活；显式重连以 45 秒 single-flight deadline 收敛到成功或稳定可重试状态。

四个派生 change 的 delta specs 已同步到长期 specs，并与主竖切一同于 2026-07-21 完成 strict 验证和归档。它们修正首期里程碑内的权威一致性与连接生命周期，不扩展到 Party、Room、ActivityInstance、战斗或内容资源系统。

## C3：客户端资格验收

### `qualify-client-v1`

验证 clean install、token restore、WSS/TCP 独立恢复、双客户端 world visit、服务端重启、低/重复 revision、stale callback、Scene/UI 生命周期、Windows Development/Release build 和跨端 fixtures。

**当前状态：**`qualify-client-v1` 已归档；`client-runtime-modularity` 完成后又于 2026-07-23 以当前源码重新通过全部 20 项 mandatory 资格。当前冻结 digest 和唯一资格结论只由 `docs/client-v1-qualification.md` 维护；任一 contract 或 Player build digest 变化都必须重新运行完整资格链。

## A1：活动实例与可选协作结构

### `establish-server-activity-instance`

只有副本、战场、剧情位面等拥有独立 lifecycle、admission、结果边界或匹配语义的活动规则冻结后，才建立 ActivityInstance owner。PersonalWorld 当前 WorldInstance 内暂态生成的普通怪物、Boss 与战斗由绑定 AssignmentStamp 的 SimulationInstance 承载，不要求 ActivityInstance。ActivityInstance 不接管 PersonalWorld、VisitSession、PlayerState 或奖励最终事实。

### 可选 `establish-server-room-domain`

仅在活动确实需要公开列表、seat、ready、leader/start 或活动前组装时提出。Room 只拥有准备状态，可请求创建 ActivityInstance，但不作为 PersonalWorld/VisitSession 前置，也不拥有活动运行和结算事实。

### 可选 `establish-server-party-domain`

仅在跨场景持续队伍、队长、聊天或连续活动需求成立时提出；直接访问个人世界不要求 Party。

## B0：服务器权威 Gameplay

`define-authoritative-gameplay-architecture` 只冻结本路线的边界，不安装依赖或实现 gameplay。后续一个 change 只解决一个阶段，并依次推进；前一项 completion evidence 是后一项 entry evidence。

### B0.1 `define-battle-simulation-model`

**状态：**已完成并归档。

**进入条件：**权威 owner、首个 PersonalWorld gameplay 范围和 Go/C++/Unity 边界已由本架构 change strict 验证。

**产出：**冻结 SimulationTick/InputTick 映射、输入命令、系统顺序、角色运动/跳跃、物理查询、ability/effect/damage/death、AI、历史帧、过载与可测试确定性边界；给出纯模型 fixtures 和预算假设，不开放 listener。

**完成条件：**剑、扇子、普通怪物与 Boss 所需的全部权威行为可由无网络 simulation harness 验收；没有客户端权威命中/伤害，没有未定义 tick/expiry/rollback 语义。

**完成 evidence：**`shared/contracts/fixtures/battle/model/` 提供闭合 schema、manifest、预算假设与 10 个 deterministic cases；`tools/battle-model/` 的只读 validator 和 12 项隔离失败回归验证双向登记、引用/排序/单位、coverage、canonical digest、安全字段拒绝及连续运行不改写 corpus。该 evidence 不包含 C++ core、wire、listener、端口或第三方依赖。

### B0.2 `define-battle-network-profile`

**状态：**已完成并于 2026-07-23 归档。

**进入条件：**B0.1 delta 已同步主 specs 且 OpenSpec strict 通过；模型 validator 与失败回归通过；`manifest.json`、`assumptions.json` 和全部 cases 的 digest 无漂移。B0.2 只能消费已冻结的 command/state/query/event 维度、VisitSession 容量兼容 workload、Tick 消费与历史/容量需求，不得为迎合网络参数改写模型语义。

**产出：**基于可重复网络模拟冻结 tick/snapshot cadence、full/delta baseline、MTU、InputBundle 冗余、raw/KCP lane registry、KCP 参数、插值/外推窗口、correction tolerance、历史窗口及 per-player/per-instance 带宽/CPU/queue 预算。

**完成条件：**目标 latency、jitter、loss、reorder、duplicate 和 burst 矩阵有测量报告；每个 battle message 只有一个 channel，snapshot 不走 KCP，production UDP 端口仍未启用。

**完成 evidence：**`shared/contracts/fixtures/battle/network-profile/` 完整绑定 `battle-model-v1` digest，提供闭合 schema/manifest、profile、logical message inventory、fault matrix、6 个 cases 和 24-result canonical report；`tools/battle-network-profile/` 用固定 LCG、整数离散事件和稳定排序重放 12 个场景，28 项隔离回归验证 model/case 漂移、coverage、lane/MTU/KCP、资格分类、安全字段及连续运行不改写 corpus。Profile 冻结 20 Hz simulation、40 Hz input、10 Hz snapshot、1200-byte datagram、16-Tick history、默认 5 actors 与 8-actor qualified maximum；33 actors compatibility 要求后续 capacity gate。真实 C++/codec/socket/KCP/AEAD 性能仍标记 `implementation_required`。

B0.2 完成只解锁 B0.3 的离线/loopback C++ core 提案；Go/C++ control、UDP/KCP wire/listener、production 端口、安全 transport 和 Unity gameplay runtime 的后续门仍关闭。

### B0.3 `implement-game-simulation-core`

**状态：**实现、归档前审计与主 specs 同步均已完成，change 已于 2026-07-24 归档；B0.4 进入条件已满足。

**进入条件：**simulation model 与 network profile delta 已同步主 specs且 strict 通过；model/profile validator、失败回归和 canonical report 无漂移；C++ compiler/CMake 和每个第三方依赖的精确版本、来源、checksum、许可证、adapter 与回滚方案获批。C++ core 必须消费 50 ms SimulationTick、16-Tick history、8-actor profile cap 与 CPU/memory target budget，并保持所有 `implementation_required` 指标为待补证门。

**产出：**`simulation/`、`ihomeland-sim-server` 离线/loopback harness、自研最小 ECS、固定单写 pipeline、GAS-like、Jolt physics adapter、Detour navigation adapter、有界 history/evidence 和 CMake Presets。

**完成条件：**unit/benchmark/sanitizer/determinism/profile tests 通过；外部类型未扩散到 gameplay components/contracts；进程仍不开放 production UDP，也不写 Go 持久库。

**完成 evidence：**`tools/cpp/cpp.ps1 bootstrap` 可在新 Windows 机器检测并恢复
锁定的 VS Build Tools/MSVC/en-US compiler UI/SDK/CMake/Jolt/Detour/JSON；Debug CTest 聚合
unit、contract、integration、negative、determinism、Jolt/Detour parity、replay、
comprehensive、benchmark 与 architecture gates。1/5/8 actor reference workload
验证 50 ms Tick、8-actor cap、2.5 ms/Tick、64 MiB instance、8 MiB history 和
256 queue targets。固定 Release binary 只在同一 source identity 的 clean CI 与
ASan、全部 model cases、连续确定性均通过后生成
`implementation-qualified-windows-x64`，并验证同源 CI/ASan gate receipt；报告继续明确排除 Linux、Go control、
socket/KCP/AEAD、production network 与 Unity runtime。

### B0.4 `establish-go-simulation-control`

**状态：**实现、主 specs 同步、server v1 完整回归、client v1 全部 20 项 mandatory
资格与 B0.4 完整资格均已通过；连续两份低敏 B0.4 report 字节一致并得到
`control-qualified-windows-x64`。change 已于 2026-07-24 完成归档，并解锁了 B0.5；
battle wire、UDP/KCP/AEAD 与 network development-readiness 已由后续独立 OpenSpec
冻结和验收，Unity battle runtime 已满足 B0.7 开发进入条件；最终产品资格仍需显式执行。

**进入条件：**B0.3 的 10 个冻结 model cases、CI/ASan/parity/benchmark、连续
determinism 与唯一 qualification report 全部通过，主 specs 已同步并归档；C++ core
提供稳定、幂等、无公网依赖的 SimulationInstance lifecycle contract，现有 placement
fencing/recovery 基线保持通过。

**产出：**Go `placement.RuntimeController` 的本机 C++ child adapter、SimulationNode registration/health/capacity、start/drain/stop、完整 AssignmentStamp binding、内部 SimulationTarget、result proposal/ack/replay 和 shutdown ordering。Control transport 固定为继承 stdin/stdout 上的 canonical JSON frame，不创建 listener、端口或 gRPC surface。

**完成条件：**own-world、visit-world、stale assignment、C++ crash/restart、Go restart、drain 和重复 result 的 contract/integration tests 通过；现有 PersonalWorld/VisitSession/Go v1 API 无双 owner 或回归。

**实现 evidence：**`shared/contracts/fixtures/simulation-control/` 与
`tools/simulation-control/` 冻结 16 种双向 frame、64 KiB frame、256 pending/outbox
上限和 B0.3 identity；C++ `ihomeland_sim_control_adapter`/`--control-stdio` 与 Go
`internal/simulationcontrol`/`simulation_node` 已接线 exact process、health/capacity、
placement、target 和 receipt-first MySQL result coordinator。Release 31/31、ASan
32/32、Go unit/race/fuzz、真实 child 无端口、真实 MySQL restart/replay/rollback 与
server v1 26/26 均通过；client v1 重新完成五分钟真实 Player soak、三项双 Player
operator 场景与 cleanup，绑定 contract/Development/Release digest
`541c50c6f24f8da6bd4878d328c86f5e9eff631d4e3786eca57415fb30d26ec5`、
`badaaa358119447b9f92368ea1163b90f4378fd0e7fdb49d880e15ee7d6db21e`、
`d1e8b6bb750acf486a6999d8ad1f91c73d52c1e793489d64dc9273e8e6419336`。
连续两份低敏 B0.4 report 的 SHA-256 均为
`2ec8d7461c04c0808922e6fee89c1366ee62efb1a15a5021f26261b07da7ed92`。

### B0.5 `establish-secure-battle-transport`

**状态：**实现、主 specs 同步、跨语言 wire/crypto/KCP、真实 child/loopback UDP、
server v1 与 client v1 回归均已通过，取得 `secure-transport-qualified-windows-x64`；
change 已于 2026-07-25 完成归档，仅解锁 B0.6，不代表公网或 Unity gameplay 可发布。
后续已归档的 `repair-battle-handshake-runtime` 重新打开该资格边界，修复公开 proof derivation、
同一生产 socket 的出站链路、handshake bootstrap ownership 与 node-global session
composition；旧 B0.5 report 不得作为当前最终资格证据复用。修复 change 已用真实 Go
parent、C++ child 和独立 C++ protocol client 完成直接影响面验证，完整报告由显式最终
资格针对冻结候选一次生成。

**进入条件（已满足）：**B0.4 主 specs 已同步并归档，连续资格报告为 qualified，且
SimulationTarget/8-actor gate、child crash/restart、result replay 和现有 world/visit 回归
均有证据；随后必须由独立 change 冻结 battle endpoint ownership、threat model、numeric
message registry、ticket/cookie/AEAD/replay/anti-amplification 与实际 UDP listener/端口。
该 change 实施前 Asio/KCP、UDP 配置、network qualification 和 Unity battle runtime
均保持关闭。

**产出：**Asio 单 UDP listener/authenticated multiplexer、raw/KCP lanes、HTTPS battle ticket、cookie challenge、AEAD/key epoch/nonce、replay window、endpoint binding/rebinding、限流、抗放大、有界 queue 和跨 C++/C#/Go wire fixtures。

**完成条件：**伪造、重放、放大、乱序、重复、过期、MTU、backpressure、rebind、key rollover 和 shutdown tests 全部通过；账号凭据、资产、奖励与结算不进入 UDP。

**完成 evidence：**B0.5 唯一 qualification report 覆盖 45/45 OpenSpec tasks 与全部
mandatory gates，并绑定 model/profile/control/wire/registry/config/fixture、Go/C++/C#
toolchain 和 binary identity。实现结论只适用于 Windows x64；B0.6 仍需独立验证
latency、jitter、loss、reorder、duplicate、burst、pause、NAT 与容量曲线。
修复后的 report 还必须证明 raw/KCP/control 共享生产 listener、KCP 无 ingress 时仍按
固定 cadence 推进，以及 replay、tamper、MTU、rebind、rekey、close 和 8/9 actor
边界均经真实 socket 到达生产 runtime。

后续已归档的 `extend-battle-resync-expiry` 将 profile 升级为
`battle-network-profile-v2`：`3004/3005` sender expiry 保持 500 ms，
`3006/3007` 调整为 2250 ms，receiver reassembly 改由 KCP window/queue 与 session
lifecycle 有界。旧 profile v1 binary/report/binding 必须全部失效；开发期只重建 current
consumer binding 与定向兼容证据，最终报告在使用者冻结当前产品候选时一次重建，不能只
替换 manifest digest。

### B0.6 `qualify-battle-network`

**进入条件：**真实 C++ 进程、安全 UDP/KCP 和测试 Unity/协议客户端可在隔离环境运行，profile 预算已机器可读。

**产出：**`tools/battle-qualification/`、可重复 network fault matrix、带宽/CPU/内存/queue/KCP 重传放大报告、安全 negative corpus、重连/迁移/Visitor soak 和低敏 evidence。

**完成条件：**完整矩阵、容量、安全、生命周期、soak 和 finalize 能由统一入口按需运行；
代表性 clean、baseline-gap、loss/reorder、1/5 actor、安全与 lifecycle 场景证明 runner、
metric、scope、cleanup 和 profile 漂移门可用。Tooling 完成不要求变化中的工作区生成
最终 qualified report。

**当前状态：**资格 corpus、opaque gateway、独立 C++ 协议客户端、Go 黑盒 harness、
qualification-only control snapshot、安全/lifecycle runner、统一入口与 finalize gate
均已实现；profile v2 consumer binding、input acknowledgement、failure regression 和
代表性 development-readiness 已通过。`qualify-battle-network` 及其四个衍生 change
已于 2026-07-27 同步主 specs 并归档，B0.7 功能开发进入条件已满足。当前没有生成
`battle-network-qualified-windows-x64-controlled` 结论；连续两次完整 verify、30 分钟
soak 与 finalize 保留给使用者显式冻结的最终候选。唯一运行顺序和 evidence/cleanup
规则见 `docs/battle-network-qualification.md`。

### B0.7 `implement-unity-gameplay-runtime`

**进入条件：**battle network qualification tooling 与代表性 network
development-readiness 通过，C++ 服务端先支持冻结 wire 与兼容策略；Unity package/版本
和 Cinemachine 回归基线获批。未执行完整最终资格时不得声明 battle network 或产品
combat 已 qualified。

**产出：**`BattleNetworkClient`、纯 C# GameplayReplica/InputHistory/PredictedStateHistory、reconciliation/interpolation、Actor Views、Input System actions、uGUI HUD、UI Toolkit 复用和 Cinemachine CameraIntent Host。

**完成条件：**Owner/Visitor 在真实 C++ 服务上完成移动、跳跃、延迟预测/校正、远端插值、断线/assignment 切换和 Scene teardown；没有第二个 Router、完整客户端 GAS/ECS 或 View 直连 transport。

**当前状态：**B0.7 source manifest、native C ABI/interop、BattleTicket/connect attempt、
managed secure UDP/KCP 主链路、纯 C# input/prediction/acknowledgement/replica/interpolation、
Composition/lifecycle、Scene Host、InputActions、Scene/Prefab/Cinemachine rig、定向质量
catalog 与 Windows Player 入口均已落地。`implement-authoritative-battle-movement-projection`
已把 C++ input timeline 接入 committed Movement/Physics projection，并发布动态位置、yaw、
velocity、bit 4 grounded、完整 active actor set 与同 Tick acknowledgement；Unity 侧按
50 ms authority group 预测、只重演 future group，并以 render motor、Actor smoothing 与
Scene-owned Camera/世界参照呈现。2026-08-03 已完成单人移动/跳跃/镜头手感和双客户端
Owner/Visitor 可见性、移动与跳跃的代表性人工验收，未发现阻断 B0.8 提案的问题。Current
physics 仍明确限制为 server-only Y=0 平地；正式地图 collision、技能和战斗内容属于后续
change。此次完成只代表定向开发验收，未运行也未生成完整 battle network 或产品 combat
最终 qualified 结论。

### B0.8 前置 `define-gameplay-configuration-governance`

**状态：**实现、主 specs 同步、定向验证与归档前审计均已完成，change 已于 2026-08-03
归档；B0.8 提案进入条件已满足，但 production gameplay content 与最终产品资格仍未交付。

**进入条件：**B0.7 Unity gameplay runtime 与权威移动投影已完成代表性 Owner/Visitor
开发验收；不得用 C++ 常量、Unity ScriptableObject、Scene 或 Prefab 反向充当跨 runtime
配置事实。

**产出：**`gameplay-config-format-v1` closed schemas、semantic/numeric/coverage registries、
`governance-only` reference package、可重算 `ConfigIdentity`、独立 nav/physics bindings、
authority/presentation owner 与 instance replacement/rollback 语义，以及纯数据 validator、
隔离失败回归和 incremental quality check。

**完成条件：**manifest/source digest、typed reference/cycle、单位/范围/overflow、B0.8 required
role coverage、authority/presentation parity、fixture/production classification、低敏诊断与连续
只读验证全部通过；主 specs、owner docs 和 closed `validation.json` 同步。该 change 完成只
解锁 B0.8 提案，不交付 production package、runtime consumer、剑/扇子、怪物/Boss、地图
collision/navigation、表现资产或最终产品资格。

**当前 evidence：**`shared/contracts/fixtures/battle/gameplay-config/` 已登记 8 份 closed
schema、3 份 registry 与完整 reference package；唯一 validator 重算
`ConfigIdentity=2037d59b77dcf397030bf4fa729c82235f9ece42c15bcfb1fcd6712d754b5333`，
31 项隔离回归覆盖 schema/digest、reference/cycle、range/overflow、coverage/parity、fixture
误用、敏感输出和只读性。`gameplay-config-validate` 只属于普通 incremental check；主 specs
同步、current change strict/check-change 与全仓 OpenSpec strict 均已通过并完成归档。该
evidence 仅解锁 B0.8 提案，不代表可玩内容或最终资格；回滚可整体移除本 change
新增 corpus/validator/spec，并恢复到 B0.7 与权威移动投影的已归档基线，不改变现有 runtime。

### B0.8 `deliver-personal-world-combat-slice`

**进入条件：**Unity gameplay runtime 与 battle network development-readiness 稳定，
`define-gameplay-configuration-governance` 已完成主 specs 同步、定向验证并归档。

**产出：**一把近战剑、一把远程扇子、武器授予技能、少量怪物、一只 Boss、基础碰撞/导航/动画/VFX/Audio/HUD，以及 Owner 邀请 Visitor 协作的产品场景。

**完成条件：**从登录进入自己的 PersonalWorld 到双人协作击败 Boss 的可重复 PC build
验收通过；伤害/死亡由服务器权威，异常网络可恢复或明确失败。准备冻结里程碑或发布时，
再由使用者显式运行包含完整 battle 网络矩阵、1/5/8 actor、安全/生命周期、连续 verify、
长时 soak 与 finalize 的当前产品资格。

**当前状态（2026-08-06）：**B0.8 定向开发验收、主 specs 同步和归档前审计完成，
`deliver-personal-world-combat-slice` 已归档。production package、Go selector、
C++ Jolt/Detour arena、deterministic encounter、combat wire/replication、Unity pure C#
input/replica/projector、Prefab、Scene、Animator、VFX、Audio、Input Actions、16 项非空
resource catalog、HUD 与 Cinemachine 接线均已落地。当前
`ConfigIdentity=d6e3f016e4c29f4ad3916759e5ea059443fe37145dbe51621704180e50bc555b`；
Unity EditMode 63 项、PlayMode 16 项通过，Development/Release Windows Player build smoke
通过，双 Player 定向运行 `3afb3466377d48d584242e1c2841c5ff` 与 PersonalWorld combat
targeted 运行 `606588b4ac924d30ac16cd37d165e7ab` 均通过，closed validation 的 9 项
`quality.ps1 check-change` 全部成功且 cleanup 无残留。

该 evidence 只证明 `deliver-personal-world-combat-slice` 的定向 development-ready，不代表
完整 battle network、产品 combat 或发布候选已 qualified；本次未运行 `quality.ps1 qualify`、
12 场景全矩阵、连续 verify、长时 soak 或 finalize。回滚提交边界以本 change 的实现提交为
原子单位：production package及其 ConfigIdentity、三端 consumer/wire mapping、C++ encounter、
Unity content/runtime 与对应验证和文档必须一并回退，不允许只回退平衡数据、Prefab、wire字段
或任一消费端；已运行实例仍按不可变 identity 终结，回滚后的 package 只能通过更高 assignment
generation 建立新 SimulationInstance。

## 条件路线

额外 gateway、Go 微服务拆分、gRPC、跨地域运行路由、Addressables 和 Unity DOTS/ECS 没有固定排期，只在扩缩容、故障隔离、资源分发、profile 或团队 ownership 证据成立后进入独立 OpenSpec change。独立 C++ Game Simulation Server 已由 B0 路线确定，但它不自动证明还需要 gRPC、匹配服、Room、Party 或其他服务拆分。
