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
  -> N0 HTTPS + WSS + TLS-TCP + Admission
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

**产出：**VisitSessionID、immutable owner、Visitor membership/capacity/revision/expiry、invite/accept/join/leave/kick/reconnect/expire、Owner grace、safe-return 和权限策略。

**完成条件：**Visitor 不能继承 Owner、伪造 world/instance、推进未授权世界事实或通过 invite 直接取得 gameplay credential。

## P0：个人世界与访客协议

### `establish-server-personal-world-protocol`

**目标：**在领域语义稳定后冻结 own-world 与 visit-world 的唯一跨端契约。

**产出：**

- world/visit Protobuf packages 与 owner ranges
- bootstrap、assignment、invite、admission、snapshot、interaction 和 safe-return schema
- message/error/route registry 与唯一 allowed channel
- gameplay ticket 与一次性 world/visit admission 的明确分层
- deterministic request/response/error/push fixtures 和 negative cases

**完成条件：**payload identity 不能覆盖 AuthContext/admission；同一业务消息无 WSS/TCP 双入口；fixtures 与 Go validator 全部通过。

## N0：公开通道与安全接入

### `add-server-http-bootstrap`

交付 version/config/register/login/refresh/logout、connection ticket、world bootstrap、invite accept 和 admission issue；handler 只做 decode/validate/authorize/call/encode。

### `add-server-websocket-control`

交付认证 WSS control、maintenance/forced logout/endpoint update、visit invite/Owner availability/safe-return notice，以及 bounded queue、deadline、slow consumer 和 connection storm tests。WSS 不接收 world mutation。

### `add-server-tcp-gameplay`

交付 TLS/TCP framing、gameplay ticket consume、world/visit admission、dispatcher、pending correlation、push、backpressure、rate/idempotency 与 reconnect/shutdown tests。

### `complete-server-personal-world-slice`

连接 Account、Session、PersonalWorld、Placement、Storage、VisitSession 与三个公开通道，交付 own-world 和 visit-world 的端到端服务端竖切。

## Q0：服务端 v1 资格验收

### `qualify-server-v1`

**进入条件：**S0-S3、W0-W2、D0、V0、P0、N0 全部完成。

**验证：**

- Go HTTP/WSS/TCP clients 与 versioned fixtures
- register/login、进入自己的世界、重复进入与 assignment 重建
- invite/accept/join/leave/kick、Owner grace、safe-return
- stale ticket/admission/endpoint/instance/epoch/lease 全部 fail closed
- Redis flush、MySQL restart、duplicate instance、commit-unknown
- unit/integration/contract/fuzz/race、背压、slow consumer、connection storm
- graceful shutdown、process restart、logs/metrics 与文档一致性

**完成条件：**全量自动化通过，无未接线 production adapter 或无 owner message/table/key/listener，并输出允许启动 C0 的资格报告。

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
