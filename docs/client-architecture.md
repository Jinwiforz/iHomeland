# Unity 客户端运行时架构

## 进入条件

Unity 运行时代码只能在 `qualify-server-v1` 完成并提供 `docs/client-integration.md` 定义的交付包后开始。客户端不得通过实现过程反向定义基础协议语义；需要改变冻结契约时，必须先创建服务端协议 change。

## 架构原则

> Unity 管内容与表现，纯 C# 管业务与状态，Composition Root 管对象图，AppRoot 管应用生命周期，SceneContext 管场景生命周期。

客户端采用混合架构，不使用全代码或全场景极端方案：

```text
BootstrapScene
  AppBootstrap
    -> AppComposition
        -> App Scope (persistent)
            AppRoot
            Application Services
            Infrastructure Adapters
            Unity Hosts
        -> Scene Flow
            Scene Scope (replaceable)
                ShellSceneContext
                GameplaySceneContext
                ToolSceneContext
```

## Composition Root

`AppComposition` 是唯一应用装配入口；各模块的 concrete types 只由对应子 Composition 了解。该装配层负责：

- 读取环境配置与 endpoint manifest model
- 创建 logger、clock、main-thread dispatcher
- 创建 HTTP、WSS、TCP adapters
- 创建 Session、Account、PersonalWorld、VisitSession、WorldAdmission 与唯一 `ClientUiRouter`
- 接入 BootstrapScene 直接引用的必要 Unity Hosts
- 显式连接依赖
- 生成初始化顺序、tickable 列表和关闭顺序
- 失败时逆序清理已成功项

当前规模不引入第三方 DI 容器。使用构造参数、初始化参数和窄接口即可。

### 编译期模块与分层 Composition

客户端生产代码固定为下面的单向程序集 DAG：

```text
Foundation
   ↓
Application
   ↓ ↘
Infrastructure  Presentation
          ↘     ↙
            Runtime
```

- `Foundation` 只包含 lifetime、clock、dispatcher 等无 Unity、无协议的基础契约。
- `Application` 只包含业务 owner、不可变 contracts、窄 ports、reducers、state machines 与 flows。
- `Infrastructure` 实现 HTTP/WSS/TLS-TCP、secure storage、generated protocol mapping。
- `Presentation` 是 `noEngineReferences` 的 Router、Experience、View State 与 presentation transactions。
- `Runtime` 只保留 Bootstrap、Composition、Unity Hosts、Views、Scenes 与 qualification adapter。

`AppComposition` 仍是唯一应用装配入口，但不再直接展开全部对象创建。它按
`FoundationComposition -> InfrastructureComposition -> SessionComposition -> ChannelComposition -> WorldComposition -> PresentationComposition -> RuntimeQualificationComposition`
依次取得封闭 bundle；bundle 只在 Composition 内传播，feature 不能访问容器或按类型解析服务。项目不引入 DI 框架和全局容器，依赖仍由构造函数在编译期显式验证。

`AppCompositionResult` 只跨越 `Runtime` 内部的 `AppRoot` 与 Development qualification 边界；业务 feature、SceneContext 和页面均不得持有它。新增对象时先确定 module、owner 与窄 port，再在对应子 Composition 装配，不把 `AppComposition` 重新增长为业务协调器。

### 大协调器拆分原则

拆分不是把一个类机械切成多个 partial 文件，而是转移状态所有权和可独立测试的决策：

- `SessionCoordinator` 只拥有 Session facade，认证、refresh、restore、logout 分别进入 flow，credential 进入 `SessionCredentialRegistry`。
- `ClientControlChannel` 只拥有 WSS lifecycle/generation；attempt、receive pump、retry policy、push dispatch 各自封闭。
- `ClientGameplayChannel` 只拥有 TLS/TCP lifecycle/generation；attempt、reader、writer、pending、heartbeat、route dispatch 各自封闭。
- `WorldAdmissionCoordinator` 只拥有 current target/target generation；OwnWorld、Visit、Return、Reconnect 由具名 flow 编排，projection 由 reducer 提交。
- `ClientConnectionRecoveryCoordinator` 只拥有 recovery intent；state machine、plan builder、control/gameplay flow 分离。
- `ClientUiRouter` 只拥有 route/navigation；planner、bounded queue、route state、interaction resolver 与 host transaction 分离。
- `ClientPersonalWorldExperience` 只拥有 presentation intent 与订阅编排；View State projector、failure mapper、Scene/route、Session invalidation、recovery transaction 分离。

复杂度报告仅提示超过阈值的文件，不以行数替代职责判断。只要 owner 仍唯一、依赖方向稳定、状态提交有单一入口，协调器可以保留必要的编排代码。

## AppRoot

`AppRoot` 是薄的持久 Unity 生命周期宿主，只负责：

- 唯一性与跨场景持有
- 生命周期状态
- Coroutine 与 Unity 主线程 Host
- 持久 UI/Audio Host
- 驱动明确登记的 tickable
- 应用退出与逆序关闭

AppRoot 不实现账号、个人世界、网络协议或页面业务。全局 `AppRoot.Instance` 不能成为 feature 默认 service locator。

生命周期：

```text
Created -> Initializing -> Running -> Stopping -> Stopped
                  |
                  -> Failed -> rollback initialized entries
```

## App Scope

跨场景持有：

- Client configuration
- Session Coordinator
- HTTP client
- WSS control channel
- TLS/TCP business channel
- Message router、pending requests、push dispatcher
- Account Service
- PersonalWorld Service、VisitSession Service 与 World Admission Coordinator
- `ClientUiRouter`、`ClientUiHostRoot`、个人世界 production routes 与唯一 `ClientPersonalWorldExperience`
- `ClientWorldSceneTransitionHost` 与 current `PersonalWorldSceneContext`
- persistent audio/settings

这些对象不得引用已卸载场景中的 GameObject、Component、Camera 或 view。

## Scene Scope

场景负责空间内容和局部生命周期：

- camera 与 camera rig
- lighting、volume、environment
- map/world root
- actors、spawn points、scene effects
- world-space UI 与 scene-bound uGUI
- scene controllers 与 cancellation

需要应用服务时，场景使用轻量 `SceneContext` 接收窄接口。SceneContext 卸载时必须解除订阅、取消任务并清除 App Scope 中的场景引用。

简单场景不需要为了目录完整创建空 SceneContext。

## Service 与 Unity Host

### 纯 C# Service

适合：

- session/token/ticket 状态
- account 与 world/visit authoritative snapshot、role 与 admission 编排
- request/command 编排
- snapshot revision 和状态投影
- 重连策略
- domain-independent validation
- UI view state（复杂且共享时）

要求可在 EditMode 或普通 .NET 测试中实例化，不创建 GameObject。

### Unity Host

只用于：

- Unity lifecycle callbacks
- Coroutine
- main-thread dispatch
- GameObject/Component 生命周期
- AudioSource
- PanelRenderer/Canvas/EventSystem
- SceneManager 与 SceneContext adapter

Host 不实现业务状态机，不持有第二份业务事实。

## 网络核心

网络核心必须覆盖 session ownership、channel-specific receive pump、typed route、pending correlation、有界主线程投递和低敏连接状态。当前实现由 `SessionCoordinator`、各 channel owner 与 `MainThreadDispatcher` 直接承担这些职责；在两个以上通道出现可证明的稳定共性前，不预先创建通用 `MessageRouter`、`PendingRequestRegistry`、`PushDispatcher` 或 `ChannelHealthMonitor`。

每个 channel 一个 reader；writer 必须序列化并有界。网络线程不能直接写 Unity view。

### 当前 HTTP bootstrap 边界

客户端首段 HTTP 能力由以下显式对象图组成：

```text
ClientEnvironmentProfile
  -> ClientEnvironment
      -> ClientHttpTransport
ClientHttpTransport + ClientHttpCodec + ClientHttpOperationCatalog
  -> ClientHttpApi
      -> ClientBootstrapService
      -> SessionCoordinator
```

- `AppBootstrap` 把非敏感环境资产与 build identity 复制为不可变 `ClientEnvironment`，`AppComposition` 显式创建并把可关闭资源交给既有 AppLifetime。
- Infrastructure 只承担冻结 HTTP operation 的传输与 codec；Application 的 `ClientBootstrapService` 和 `SessionCoordinator` 分别拥有启动配置流程与唯一 session/credential lineage。
- 初始化不自动访问网络，own-world bootstrap 也只返回一次查询投影；具体 operation、安全和失败语义由[客户端接入规范](client-integration.md)统一说明。

HTTP 边界本身不实现 UI、自动网络 bootstrap、token 持久化或 socket。第十个 `acceptVisitInvite` operation 只返回 generation-bound reservation；own-world bootstrap 只返回一次强类型查询投影，world admission 只形成 expiring、single-use lease。最终事实与流程分别由下述 Services/coordinator 持有，WSS/TCP 仍由独立 owner 消费凭据。

### 当前 WSS control 边界

```text
SessionCoordinator + ClientConfigurationStore
  -> ClientControlChannel
      -> ClientWebSocket (receive-only)
      -> ClientControlCodec + 9-route catalog
      -> MainThreadDispatcher
```

- `ClientControlChannel` 只在显式 `RunAsync` 后签发 WSS ticket；App Scope 初始化仍不访问网络。
- 每次 connection attempt 都单独签发并取得一次 ticket，固定连接 `/v1/control` 与 `ihomeland.control.v1`；旧 ticket、query、cookie 和 fallback endpoint 都没有入口。
- Runtime API 不提供 application `SendAsync`。每个 connection 只有一个 receive pump，负责有界 fragment 重组、严格连续 sequence 与 9 类 generated PUSH 解码。
- 普通 PUSH 经既有有界 `MainThreadDispatcher` 进入 Unity 主线程；forced logout/session invalidation 先以来源 generation 与更高 epoch 清除唯一 Session owner。`SessionCoordinator.Invalidated` 只发布一次 Authenticated 到非认证终态的权威边界，Experience 在主线程重读 current Session 后退役 world target、Scene/HUD 与全部产品 route 并回到 Login；迟到事件不能覆盖后续显式登录。
- 只有瞬时 transport/普通 peer close 消耗固定有限 backoff；协议、授权、失效、背压与停止均 fail closed，普通 WSS 中断不擅自清除 HTTP session。

该边界不拥有 PersonalWorld、VisitSession、assignment 或 UI 最终状态，也不实现 TLS/TCP、业务 request/response 或客户端 control command。

### 当前 TLS/TCP gameplay 边界

```text
SessionCoordinator + ClientConfigurationStore
  -> ClientGameplayChannel
      -> IClientGameplayConnection (single reader / serialized writer)
      -> ClientGameplayFramer + ClientGameplayCodec + frozen route catalog
      -> bounded pending + MainThreadDispatcher
```

- `ClientGameplayChannel` 初始化保持零网络副作用；显式 connect 原子消费同一 session generation 的 world admission lease，再签发并消费匹配 `TLS_TCP`/`GAMEPLAY` ticket。
- Wire、TLS、明文例外与 credential 规则由[客户端接入规范](client-integration.md#4-tlstcp-business)统一拥有；channel 对任何不匹配的 preface、frame、sequence、route 或 correlation fail closed。
- 每个 connection generation 只有一个 reader 与一个 serialized writer；pending、writer item、writer encoded bytes 和主线程投递均有硬上限，断线或背压会恰好完成等待方并撤销 generation。
- Active generation 由 channel 内唯一 heartbeat owner 每 15 秒提交 `GAMEPLAY_HEARTBEAT_REQUEST(1)`，并通过同一 correlation/pending、writer、codec 和 terminal close 路径等待 `GAMEPLAY_HEARTBEAT_RESPONSE(2)`；close、safe-return、session invalidation 或新 generation 建立后，旧 heartbeat 不得继续写入。
- Caller cancel 只结束本地等待，仍保留有界 correlation 以安全消费迟到 response；JOIN/RECONNECT 只允许携带当前 admission 的首个匹配 command。
- 三类登记 PUSH 才能进入主线程；safe-return 在投递前先关闭旧 target mutation gate，且其权威 VisitSession revision 必须不低于客户端已提交的同 session revision，重新加入后迟到的旧指令只会被丢弃。session epoch 失效会同步撤销匹配 gameplay generation。Remote、protocol、timeout、backpressure 或 transport 终止会在 reader/writer 都确认退出后，通过主线程保留槽发布一次 terminal disconnect；显式 close、safe-return、session invalidation 与 shutdown 不发布该事件。

该边界不保存 PersonalWorld、VisitSession、assignment 或 UI 最终状态，也不实现页面、Scene 或自动重试；它只向下述 Services/coordinator 交付 typed response/PUSH。

`ClientConnectionRecoveryCoordinator` 是 automatic/manual 恢复的唯一 intent owner，不是 tick 或后台无限修正。WSS 恢复只失效 control-only projection并通过健康 gameplay收敛完整 snapshot；gameplay恢复持有冻结低敏 target descriptor，OwnWorld重建 admission/connection/snapshot，Visitor以 typed RECONNECT首帧恢复同一 membership。Descriptor 以服务端 Session identity/epoch 约束血统；同一血统的access refresh只允许本地generation单调升代，不同session或换账号不能继承旧target。整笔恢复共享45秒总deadline与session/recovery/target/scene四重gate；只有Scene/HUD提交后回到Idle，terminal后才开放manual retry。

Session authority 高于 connection recovery。Session 一旦进入 `Unauthenticated`、`Unresolved` 或 `Stopped`，客户端不再把恢复快照仅映射为关闭弹窗；`WorldAdmissionCoordinator.InvalidateSession` 会使当前 intent 和 target generation 失效、清除 world/visit target 投影，Experience 串行失效 Scene route、卸载内容 Scene 并只保留 Login。Experience 只消费 `SessionCoordinator.Invalidated` 发布的单调 generation；target 清理产生的后续 `Changed` 只刷新投影，不能重新制造 Login/Scene 收敛事务。该迁移不发起无凭据的 leave，也不依赖按钮、延时或 frame tick。

Editor/Development Player 额外编译只读 `Core/Qualification` 边界。它从既有 owner 的锁内派生 AppRoot、channel generation、run/socket/heartbeat/recovery intent、pending、dispatcher、subscription 与 Scene owner 计数，不保存第二份状态，也不执行修正。五分钟 soak 的显式 transport fault 只取消 current control attempt 或提交 current gameplay generation 的既有 terminal 分类，后续仍由同一生产恢复状态机处理；Release 预处理后不包含 profile、存储根、诊断、fault 或 soak 入口。

### 当前 PersonalWorld/VisitSession Services 边界

```text
HTTP bootstrap/accept/admission + WSS control hints + TLS/TCP response/PUSH
  -> PersonalWorldService          (world/assignment 完整投影)
  -> VisitSessionService           (visit/invite/role/command 投影)
  -> WorldAdmissionCoordinator     (current target 与转换状态机)
```

- Generated message 只作为边界输入；提交前校验 identity、enum、assignment binding、revision、deadline 与集合上限，并复制为不可变 application model。
- PersonalWorld aggregate revision 与 runtime assignment generation 是两个独立的单调 authority gate：world revision 只比较 identity、owner、lifecycle 与创建事实；同一 world revision 可以接纳更高 assignment generation 的新 WorldInstance，例如服务端重启恢复。相同 assignment generation 只允许 identity 不变且 lease deadline 单调续期。低 generation、同 generation identity 漂移或 lease 回退必须 fail closed。
- 完整 VisitSession snapshot 使用最高 revision gate：低 revision 丢弃、同 revision 等价幂等、同 revision 冲突 fail closed；target 清除后仍保留不可见的 VisitSession revision gate。缺失 assignment 会清除旧投影并留下 generation tombstone，只有更高 generation 才能建立新实例。
- WSS assignment/availability/closed 只形成 refresh hint，不能冒充 gameplay snapshot 或 safe-return；定向 invite inbox 按 identity 去重、按 expiry 清理并限制为 128 项。message 2100 的 `RETIRED` projection 是 exact VisitSessionID、InviteID、Owner、Target 与 created revision tombstone，匹配项立即从 inbox/selection 删除，重复或不存在 identity 幂等忽略。Accept 成功也会退役 Visitor inbox 中被消费的 identity；Owner 完整 member snapshot 出现对应 Visitor 时会退役该 target 的 outgoing identity，避免继续暴露必然失败的撤销按钮。同 VisitSession、Owner、Target 的更高 created revision 会替换旧 pending identity，commit-unknown 不猜测消费结果。
- Coordinator 只允许 `Inactive -> ResolvingOwnWorld -> OwnWorld -> JoiningVisit -> Visiting -> ReturningOwnWorld -> OwnWorld` 的正常转换，并同时校验 session generation 与 target generation；active gameplay 非预期终止会进入 `ConnectionLost`、清除 current target 投影，只有显式进入 own-world 才建立新 generation。
- JOIN/RECONNECT admission credential 只由 gameplay channel 内部写入首个 command；Services、coordinator、snapshot、subscriber 与日志均不能读取。
- Owner/Visitor command 使用 current role 与 revision 在写入前 fail closed。Caller cancel、commit-unknown 或 revision conflict 不触发隐式 mutation 重试。
- App Scope 按安全存储、Session restore、channels/services、recovery、Scene/UI、Experience 的顺序初始化。启动存在合法 refresh lineage 时一次性轮换并直接进入 OwnWorld；无record/Unsupported才打开 Login。独立通道恢复已经由唯一 coordinator 接线；内容资源系统仍属于后续 change。

### 当前 UI routing/Host 边界

```text
AppBootstrap
  -> ClientUiHostRoot (Input System clone / explicit Hosts)
  -> AppComposition
      -> ClientPersonalWorldExperience (derived View State / semantic actions)
      -> ClientUiRegistry (Login / Shell / WorldVisit / WorldHud / ConnectionLost)
      -> ClientUiRouter
          -> UI Toolkit Host | uGUI Host
      -> ClientWorldSceneTransitionHost
          -> PersonalWorldSceneContext
```

- `AppBootstrap` 验证 BootstrapScene 的直接引用；`AppComposition` 创建唯一纯 C# `ClientUiRouter` 与 `ClientPersonalWorldExperience`，并把 router、Scene、Host/Input boundary 纳入既有 AppLifetime。
- Production registry 只登记 Login、Shell、WorldVisit、WorldHud 与 ConnectionLost；Settings 未交付。UI Toolkit/uGUI Host 只取得不可变 View State 和窄语义 action，不取得 transport、generated message、credential 或完整容器。
- `PersonalWorldScene` 同时承载 Owner/Visitor 表现，只允许一个轻量 Context；load 候选通过 target/scene/request generation 后才提交，旧候选必须回滚。
- route、layer、事务、输入、焦点、生命周期、失败和验收的唯一详细规则见 `docs/client-ui-architecture.md`，本文不重复维护。

## 状态所有权

| 状态 | Owner |
|---|---|
| endpoint/config | Configuration Service |
| token/session/tickets | Session Coordinator |
| account/player | Account Service |
| PersonalWorld identity、owner 与最高 world revision | PersonalWorld Service |
| VisitSession、Owner/Visitor role、membership 与 expiry | VisitSession Service |
| current WorldInstance assignment | PersonalWorld Service |
| current target、admission flow 与 target generation | World Admission Coordinator |
| active route、layer、input 与 focus generation | ClientUiRouter / ClientUiHostRoot |
| camera/map/scene actors | SceneContext |
| transient animation/focus | View/Host |

服务端 snapshot 是所属 aggregate 的权威投影。客户端收到高 revision snapshot 后覆盖对应业务投影，低 revision 不得回写。

## 个人世界访问生命周期

客户端在个人世界阶段只呈现服务端权威模式：

```text
OwnWorld
  -> AcceptInvite
  -> JoiningVisit
  -> Visiting(owner, personalWorld, visitSession, worldInstance)
  -> LeavingVisit / OwnerUnavailable
  -> ReturningOwnWorld
  -> OwnWorld
```

- `OwnWorld` 使用当前 PlayerID 对应的 primary PersonalWorld。
- 邀请只进入确认流程，不能直接创建连接或相信 payload endpoint。
- `JoiningVisit` 通过 HTTPS/WSS 控制结果取得一次性 admission，再由 business channel 进入 Owner 的服务端 WorldInstance。
- `Visiting` 中 UI、交互权限与任务入口只从 VisitSession role/policy 投影，不根据本地“房主”按钮或场景对象推断 Owner。
- Owner grace 到期、VisitSession close、kick 或 assignment 失效时，客户端停止提交世界 command，清理 Visitor 状态并执行服务端指定的安全返回。
- 返回自己的世界必须重新解析 own-world assignment，不能复用访问世界的 WorldInstanceID 或 endpoint。

PersonalWorld/VisitSession Services 属于 App Scope，可以跨加载场景保存最高 revision 与迁移状态；地图、NPC、Actor 和表现对象属于 Scene Scope。SceneContext 卸载后必须取消 world snapshot 订阅、交互任务和异步资源加载，迟到 callback 只能被 generation/cancellation guard 丢弃，不能写回已销毁 View 或旧 WorldInstance。

Owner 是领域角色而非 Unity 网络 host。客户端不启动 listen server、不接受 Visitor socket，也不保存可转让的 WorldOwnerID。

## Scene、Prefab 与 ScriptableObject

- Scene：空间布局、camera、lighting、地图和局部对象。
- Prefab：可复用 GameObject/view 结构与组件配置。
- ScriptableObject：环境定义、资源目录、UI theme 和共享设计数据。
- Runtime Service：session、socket、账号、world/visit/admission 和玩家资产的运行投影。

ScriptableObject 不保存在线 session、连接状态或 world/visit snapshot。

## 高级技术进入条件

- Addressables：出现远程内容、分包、按需卸载或复杂依赖管理时评估。
- DOTS/ECS：大量同构实体或 profile 证明 CPU/内存瓶颈时局部引入。
- 第三方 DI：composition 复杂度和测试收益明确高于依赖成本时评估。
- UniTask/其他 async 库：统一异步模型的收益经过独立 change 评估后引入。

## 验收重点

- 重复 bootstrap 不创建重复应用根或连接。
- 初始化失败逆序清理。
- 只有 tickable 参与逐帧更新。
- 纯 C# Services 可独立测试。
- WSS/TCP 独立断线与重连不改变消息语义。
- SceneContext 卸载后无持久引用。
- 网络 push 只在主线程更新业务状态和 active view。
- 应用退出有 deadline，不同步阻塞 Unity shutdown。
- 个人世界阶段 Owner/Visitor 模式切换不会残留旧 SceneContext、旧 admission 或可写 world callback。
- 未认证启动只创建 Login 页面且零业务网络副作用；双 Host 的 modal、focus、raycast、action map 与 teardown 保持单 owner。
