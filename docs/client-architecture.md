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

`AppComposition` 是唯一了解 concrete types 的位置，负责：

- 读取环境配置与 endpoint manifest model
- 创建 logger、clock、main-thread dispatcher
- 创建 HTTP、WSS、TCP adapters
- 创建 Session、Account、PersonalWorld、VisitSession、WorldAdmission 和 UI Services
- 创建必要 Unity Hosts
- 显式连接依赖
- 生成初始化顺序、tickable 列表和关闭顺序
- 失败时逆序清理已成功项

当前规模不引入第三方 DI 容器。使用构造参数、初始化参数和窄接口即可。

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
- UI navigation/service
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
- UIDocument/Canvas/EventSystem
- SceneManager 与 SceneContext adapter

Host 不实现业务状态机，不持有第二份业务事实。

## 网络核心

网络核心包括：

- `SessionCoordinator`
- `MessageRouter`
- channel-specific receive pumps
- `PendingRequestRegistry`
- `PushDispatcher`
- `MainThreadDispatcher`
- `ChannelHealthMonitor`

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

HTTP 边界本身不实现 UI、自动网络 bootstrap、token 持久化、invite accept、world admission 或 TLS/TCP。Own-world bootstrap 只作为一次强类型查询返回，不在本层保存 PersonalWorld 最终事实；WSS control 由下述独立 owner 消费这里交付的一次性 ticket。

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
- 普通 PUSH 经既有有界 `MainThreadDispatcher` 进入 Unity 主线程；forced logout/session invalidation 先以来源 generation 与更高 epoch 清除唯一 Session owner，再终止自动恢复。
- 只有瞬时 transport/普通 peer close 消耗固定有限 backoff；协议、授权、失效、背压与停止均 fail closed，普通 WSS 中断不擅自清除 HTTP session。

该边界不拥有 PersonalWorld、VisitSession、assignment 或 UI 最终状态，也不实现 TLS/TCP、world admission、业务 request/response 或客户端 control command。

## 状态所有权

| 状态 | Owner |
|---|---|
| endpoint/config | Configuration Service |
| token/session/tickets | Session Coordinator |
| account/player | Account Service |
| PersonalWorld identity、owner 与最高 world revision | PersonalWorld Service |
| VisitSession、Owner/Visitor role、membership 与 expiry | VisitSession Service |
| current WorldInstance assignment/admission | World Admission Coordinator |
| active screen/modal | UI Service |
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
