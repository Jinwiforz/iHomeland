# iHomeland 路线图

## 文档职责

本文档定义 iHomeland 的严格交付顺序、OpenSpec change 边界、进入条件和完成条件。第一业务主线是个人持久世界与受控访客联机；Unity 必须等待服务端 v1 由 Go 协议测试客户端独立验收。

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
  +-> A1 ActivityInstance Model
  |    -> optional Room / Party
  |
  +-> B0 Battle Model / Network Profile
       -> Server UDP / KCP
       -> Client UDP / KCP
       -> First Battle Slice
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

**产出：**Unity PC baseline、minimal BootstrapScene、AppBootstrap/AppComposition/AppRoot、App/Scene Scope、纯 C# Services/Unity Hosts、rollback/reverse shutdown 和 EditMode/PlayMode lifecycle tests。

### `generate-client-protocol-baseline`

生成 C# protocol，验证 Go/C# golden parity；generated code 保持忽略且可在 Unity 编译前重建。

## C1：客户端网络与账号

### `add-client-http-bootstrap`

实现 version/config/register/login/refresh/logout/ticket/world bootstrap，含 timeout/cancel/error mapping 和安全凭据处理。

### `add-client-websocket-control`

实现 WSS receive pump、控制通知、visit invite、epoch invalidation、主线程投递、重连与关闭。

### `add-client-tcp-gameplay`

实现 TLS/TCP framing、single reader/serialized writer、pending requests/push dispatcher、gameplay admission、backpressure 和 close reasons。

## C2：个人世界客户端竖切

### `establish-client-personal-world-services`

实现纯 C# PersonalWorld、WorldInstance、VisitSession、WorldAdmission Services，以及 OwnWorld/JoiningVisit/Visiting/ReturningOwnWorld 状态机。

### `integrate-dual-ui-routing`

按页面适配度选择 UI Toolkit/uGUI，统一 screen owner、layer、input、focus 和 lifecycle；邀请/访问列表优先评估 UI Toolkit，世界空间 UI 使用 uGUI。

### `add-client-personal-world-vertical-slice`

交付登录、进入自己的世界、邀请/接受、Visitor 模式、Owner grace、离开/踢出、安全返回、SceneContext generation/cancellation 和 PC 输入/分辨率验收。

## C3：客户端资格验收

### `qualify-client-v1`

验证 clean install、token restore、WSS/TCP 独立恢复、双客户端 world visit、服务端重启、低/重复 revision、stale callback、Scene/UI 生命周期、Windows Development/Release build 和跨端 fixtures。

## A1：活动实例与可选协作结构

### `establish-server-activity-instance`

只有副本、Boss、剧情位面或战斗等活动规则冻结后，才建立 ActivityInstance lifecycle 与 admission owner。ActivityInstance 不接管 PersonalWorld、VisitSession、PlayerState 或奖励最终事实。

### 可选 `establish-server-room-domain`

仅在活动确实需要公开列表、seat、ready、leader/start 或活动前组装时提出。Room 只拥有准备状态，可请求创建 ActivityInstance，但不作为 PersonalWorld/VisitSession 前置，也不拥有活动运行和结算事实。

### 可选 `establish-server-party-domain`

仅在跨场景持续队伍、队长、聊天或连续活动需求成立时提出；直接访问个人世界不要求 Party。

## B0：战斗设计

依次推进 `define-battle-simulation-model`、`define-battle-network-profile`、server battle core、UDP/KCP 安全通道、网络资格验收和客户端战斗竖切。UDP/KCP 首次启用必须同时交付 ticket/cookie、AEAD、重放保护、限流、抗放大、网络模拟和带宽预算。

## 条件路线

gRPC、独立 gateway/game/battle server、跨地域运行路由、Addressables 和 DOTS/ECS 没有固定排期，只在扩缩容、故障隔离、资源分发、profile 或团队 ownership 证据成立后进入独立 OpenSpec change。
