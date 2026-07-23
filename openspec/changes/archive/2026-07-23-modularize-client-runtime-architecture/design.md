## Context

客户端 v1 已经完成从 HTTPS Session、WSS control、TLS/TCP gameplay、PersonalWorld/VisitSession、连接恢复到双 UI/Scene 的完整竖切。现有设计的状态 authority 与失败语义总体正确，但实现形态已经出现两个系统性问题。

第一，全部手写运行时代码位于 `IHomeland.Client.Runtime`：

```text
Core + Application + Infrastructure + Presentation + Unity Hosts + Scenes
                              |
                              v
                  IHomeland.Client.Runtime.asmdef
```

文件夹和 namespace 表达了层次，编译器却不能阻止 Application 引用 `Infrastructure.Http`、`Infrastructure.Tcp`、`Infrastructure.WebSocket`、generated Protobuf 或 Unity API。当前 Application 的 Session、Control、Gameplay、World 等代码均存在这类引用，Presentation 也直接依赖具体 channel、HTTP/WebSocket 结果与 Unity Scene adapter。

第二，状态正确性逐渐集中到少数超大类型。基线盘点如下，行数只用于说明规模，不作为单独拆分标准：

| 类型 | 基线行数约 | 当前混合职责 |
|---|---:|---|
| `ClientPersonalWorldExperience` | 2200 | lifecycle、actions、投影、失败、恢复、Scene/route |
| `WorldAdmissionCoordinator` | 1750 | target owner、own/visit/return/reconnect flow、映射 |
| `ClientGameplayChannel` | 1390 | generation、handshake、pending、reader、heartbeat、push |
| `SessionCoordinator` | 1380 | session owner、认证、refresh、credential lease、storage |
| `VisitSessionService` | 1330 | visit projection、inbox、退役、全部 mutation |
| `ClientUiRouter` | 1310 | route owner、queue、plan、Host transaction、input/focus |
| `ClientConnectionRecoveryCoordinator` | 1210 | recovery owner、计划、control/gameplay 执行、结果映射 |
| `ClientControlChannel` | 1050 | lifecycle、ticket/connect、receive、sequence、retry、dispatch |

这些类型并非因为名称叫 `Coordinator` 或 `Service` 而有问题，而是单个类型同时保存最终状态、决定迁移、执行外部副作用并处理多个独立失败来源。用户操作需要在这些类之间反复跳转，而既有注释主要解释不变量，不能替代清晰的模块和调用边界。

本 change 只改变客户端内部架构，不改变服务端、协议、存储或玩家可见行为。现有全部客户端 specs 是回归合同，`client-v1-qualification` 是最终资格门。

## Goals / Non-Goals

**Goals:**

- 用 asmdef 把依赖方向从文档约定变为编译期约束。
- 让纯 C# Application 和 Presentation 不依赖 UnityEngine、generated Protocol 或具体 transport。
- 保留每种事实的唯一 owner，同时将纯 transition/policy、用例 flow、协议映射和副作用 transaction 拆成独立可测组件。
- 在同一个 change 内完成 Session、WSS、TCP、World/Visit/Recovery、UI Router、Experience 与主要产品 View 的结构性拆分。
- 维持唯一 `AppBootstrap -> AppComposition -> AppRoot`、显式构造函数接线与 App/Scene Scope。
- 让每个主要用例都有稳定阅读入口，能够从 command 沿一条调用链到 owner commit，而不需要线性阅读千行类。
- 通过 characterization、架构验证、完整 Unity 测试、build 与资格回归证明重构行为等价。
- 所有迁移阶段都保持可运行提交，避免长时间存在不可编译分支或不可判定双实现。

**Non-Goals:**

- 不新增账号、世界、访问、UI、战斗、Room、Party、资源或经济功能。
- 不改变 HTTP/WSS/TCP operation、message id、allowed channel、frame、deadline、retry、ticket/admission、revision 或 safe-return 语义。
- 不修改服务端、Protobuf、OpenAPI、MySQL、Redis 或部署。
- 不引入第三方 DI、全局 event bus、service locator、响应式框架、UniTask 或通用 workflow engine。
- 不以 `partial class`、平均拆文件、一方法包装类或万能 `Manager` 作为拆分结果。
- 不在本 change 中设计客户端日志体系，也不修改代码注释规范；为满足程序集边界而移动既有 diagnostics adapter 不得扩张成新日志框架。
- 不强制所有文件小于任意行数；完成标准是 owner、依赖、状态与副作用边界。

## Decisions

### 1. 建立五层手写程序集 DAG，保留现有 Runtime 为 Unity Composition

目标依赖图：

```text
IHomeland.Client.Protocol.Generated
                 ^
                 |
IHomeland.Client.Infrastructure ---> IHomeland.Client.Application
                 |                              |
                 |                              v
                 +----------------> IHomeland.Client.Foundation

IHomeland.Client.Presentation -----> IHomeland.Client.Application
                 |                              |
                 +------------------------------+

IHomeland.Client.Runtime
  -> Foundation + Application + Infrastructure + Presentation
  -> Unity Input System / TextMeshPro / UnityEngine
```

程序集职责：

| 程序集 | 允许内容 | 禁止内容 |
|---|---|---|
| `IHomeland.Client.Foundation` | lifetime contracts、clock/delay contracts、低层无业务通用值 | Unity、Protocol、transport、业务 owner、UI |
| `IHomeland.Client.Application` | Session/World/Visit/Recovery 状态 owner、用例、纯模型、ports | Unity、generated message、HTTP/WSS/TCP concrete、Scene/View |
| `IHomeland.Client.Infrastructure` | HTTP/WSS/TCP/security concrete adapter、codec、generated mapping、具体 channel 资源 | UI、Scene、产品 View State、业务最终事实 |
| `IHomeland.Client.Presentation` | UI route owner、Experience、View State、纯 projector、presentation transactions contracts | UnityEngine、generated message、具体 transport、credential |
| `IHomeland.Client.Runtime` | AppBootstrap/AppRoot/AppComposition、Unity Hosts/Views/Scenes、ScriptableObject config、最终 adapter 接线 | 第二个 Composition Root、全局服务查询、业务事实副本 |

`Foundation`、`Application` 与 `Presentation` 使用 `noEngineReferences: true`。`Application` 和 `Presentation` 不引用 `IHomeland.Client.Protocol.Generated`。`Infrastructure` 可以引用 Unity transport API，但不能被 Application 或 Presentation 反向引用。现有 generated assembly 名称与生成流程不变。

保留 `IHomeland.Client.Runtime` 名称并让现有 MonoBehaviour、SceneContext、Unity View 和 Composition 继续属于它，降低 Unity 序列化资产丢失风险。测试 asmdef 显式引用实际被测模块；每个手写程序集只向登记测试程序集开放必要 `InternalsVisibleTo`。

替代方案：

- 继续单 asmdef，仅靠 namespace lint：不能阻止类型引用，拒绝。
- 每个 feature 建一个 asmdef：会在当前规模制造过细依赖图，并放大 Session/Channel/World 合法协作成本，拒绝。
- 更换根 Runtime 程序集名称：对序列化和资格收益不足，拒绝。

### 2. Application 拥有 ports 和业务模型，Infrastructure 完成协议翻译

Application 定义完成业务所需的窄 port，例如：

- Session bootstrap/auth/refresh/logout/ticket/admission gateway；
- secure session store；
- control channel lifecycle 与 typed push source；
- gameplay connection、typed command 与 push source；
- main-thread dispatch contract；
- clock/delay。

这些 port 使用 Application 自己的不可变 request/result/snapshot，不暴露 `UnityWebRequest`、`ClientWebSocketAdapter`、socket、generated Protobuf、原始 JSON/frame 或 credential 文本。Credential 使用现有单次 lease/opaque take 边界，只允许具体 adapter 在正确连接动作中取得。

Infrastructure 负责：

```text
HTTP JSON / generated Protobuf / socket event
                    |
                    v
        校验、复制、降敏、映射
                    |
                    v
        Application contract/result
```

协议映射失败返回封闭 protocol failure；Application 不捕获 codec/platform exception 来推断业务结果。禁止为了减少接口数量创建通用 `Send(path, body)`、`Send(messageId, IMessage)` 或 `Resolve<T>()`。

具体 `ClientControlChannel` 与 `ClientGameplayChannel` 归入 Infrastructure，因为它们拥有 socket、codec、receive/write pump 和 transport generation；Application 只依赖对应 channel port。Channel 可以调用 Application 定义的 Session invalidation sink 和 dispatcher port，但不能保存 Session、World 或 VisitSession 最终事实。

### 3. 所有超大协调器采用同一结构性拆分原则

每个重构目标必须明确四种角色：

```text
Facade / Owner
  - 唯一保存最终状态
  - 暴露 command、snapshot、event

Pure Policy / Reducer
  - 只根据输入决定合法迁移或派生结果
  - 无 I/O、无共享可变状态

Use Case / Flow
  - 编排一个明确业务意图
  - 持有单笔 lease/cancellation，不保存最终事实

Adapter / Transaction
  - 执行网络、存储、Host、Scene 等外部副作用
  - 返回封闭结果，由 Owner 决定是否提交
```

禁止拆出的组件复制 owner snapshot 作为长期可变状态。异步 flow 必须捕获包含相关 session/connection/target/navigation/presentation generation 的 lease，并在提交前重新验证。仅将方法移动到 `partial`、extension 或共享所有私有字段的 nested helper 不算完成。

### 4. SessionCoordinator 保留身份 authority，拆出会话用例与凭据组件

目标结构：

```text
SessionCoordinator                 唯一 Session owner/facade
  -> SessionStateMachine           纯状态迁移、epoch/generation 决议
  -> SessionAuthenticationFlow     register/login
  -> SessionRefreshFlow            single-flight refresh
  -> SessionRestoreFlow            secure lineage restore
  -> SessionTerminationFlow        logout/forget/invalidation/unresolved
  -> SessionCredentialRegistry     ticket/admission lease 与单次交付
  -> IClientSessionGateway         Infrastructure HTTP 实现
  -> IClientSecureSessionStore     Infrastructure OS store 实现
```

只有 `SessionCoordinator` 可以发布 current snapshot 和 invalidation generation。Flow 返回候选结果；StateMachine 根据 captured generation 决定提交或丢弃。Secure store commit 仍先于可继续认证的 current snapshot。Password 只在调用参数和 gateway 调用栈存在。

`ClientSessionRestoreCoordinator` 可以保留为 AppLifetime 启动入口，但不得复制 Session 状态；它只运行一次 restore flow 并发布低敏 restore 终态。

### 5. Control 与 Gameplay Channel 按连接资源职责拆分

Control：

```text
ClientControlChannel               WSS lifecycle/generation owner
  -> ControlConnectionAttempt      ticket、URI、handshake、socket ownership
  -> ControlReceivePump            fragment、size、sequence、peer close
  -> ControlRetryPolicy            纯 retry classification/budget/backoff
  -> ControlPushDispatcher         typed push/main-thread/session invalidation
  -> ControlProtocolAdapter        generated/Envelope 映射
```

Gameplay：

```text
ClientGameplayChannel              TLS/TCP lifecycle/generation owner
  -> GameplayConnectionAttempt     admission、connect、首 command
  -> GameplayReaderPump            frame decode 与 terminal classification
  -> GameplayWriter                唯一有界写 owner
  -> GameplayPendingRegistry       correlation、deadline、late response
  -> GameplayHeartbeat             每 generation 唯一 heartbeat
  -> GameplayRouteDispatcher       typed response/PUSH route
  -> GameplayProtocolAdapter       generated/Envelope 映射
```

Reader、writer、heartbeat 和 pending registry 都绑定同一 connection generation。任何 terminal path 只由 channel owner 发布一次 disconnect；子组件不能自行重连或修改业务 Service。WSS 仍没有 application send API，Gameplay 每条消息仍只有既有 allowed channel。

### 6. World、Visit 与 Recovery 保留独立业务 owner，按用例拆 flow

PersonalWorld：

- `PersonalWorldService` 继续拥有 world/assignment 最高 revision/generation projection；
- 提取纯 `PersonalWorldProjectionReducer` 和 generated-to-application adapter；
- control hint 只产生 refresh intent，不直接替换 gameplay authority。

VisitSession：

```text
VisitSessionService               唯一 visit/inbox owner
  -> VisitSessionProjectionReducer
  -> VisitInviteInboxReducer
  -> VisitInviteRetirementPolicy
  -> Open/Create/Revoke/Kick/Close/Leave command flows
```

命令 flow 可以共享一个只接受登记 operation 的 typed executor，但不得形成任意 message ID/generic payload 入口。所有 mutation 仍由 Service 使用 current role/revision 预检并提交 response/push replacement。

WorldAdmission：

```text
WorldAdmissionCoordinator         current target/target generation owner
  -> WorldTargetStateMachine
  -> EnterOwnWorldFlow
  -> EnterVisitWorldFlow
  -> ReturnToOwnWorldFlow
  -> ReconnectWorldTargetFlow
  -> WorldAdmissionFailureMapper
```

Flow 负责取得 bootstrap/reservation/admission、连接、JOIN/RECONNECT 和等待 snapshot，但最终 target 只能由 Coordinator 使用 intent lease 提交。Own/visit/return/reconnect 不共享可任意修改的流程字段。

Recovery：

```text
ClientConnectionRecoveryCoordinator  recovery intent owner
  -> ConnectionRecoveryStateMachine
  -> ConnectionRecoveryPlanBuilder
  -> RecoverControlFlow
  -> RecoverGameplayFlow
  -> ConnectionRecoveryFailureMapper
```

Plan 由冻结低敏 target descriptor、session epoch 和 channel health 纯计算。Coordinator 继续拥有 automatic/manual single-flight 与总 deadline；Session invalidation 高于所有 recovery lease。

### 7. Router 与 Experience 分别保留唯一 UI/presentation owner

Router：

```text
ClientUiRouter                    route/navigation generation owner
  -> ClientUiTransitionPlanner    纯 layer/lifecycle/input/focus 决策
  -> ClientUiRouteState           唯一 active/cached/modal snapshot
  -> ClientUiTransitionQueue      有界串行请求
  -> ClientUiTransitionTransaction Host 副作用、rollback、commit
  -> ClientUiInteractionResolver  current interactive route/input plan
```

Router facade 仍提供既有强类型 open/close/invalidate API。Host transaction 不能修改业务 View State；Input/focus 仍由 Runtime 中 `ClientUiHostRoot` 实现 Presentation port。

Experience：

```text
ClientPersonalWorldExperience         唯一产品 action facade
  -> PersonalWorldPresentationState   presentation generation/intent/failure owner
  -> PersonalWorldViewStateProjector  纯 View State 派生
  -> PersonalWorldSceneTransaction    Scene/route/HUD 串行收敛
  -> SessionInvalidationPresentation  只返回 Login
  -> RecoveryPresentationTransaction  recovery 后提交 Scene/HUD
  -> 登记的 auth/world/visit actions   调用 Application ports
```

Experience 不再引用具体 HTTP/WSS/TCP 类型。View/Host 只看到 action interfaces 和不可变 View State。`ClientPersonalWorldUiToolkitView` 按完整逻辑页面拆为 Login、Shell、WorldVisit、ConnectionLost 的绑定/渲染 adapter；共享根 View 只管理 UI Toolkit 生命周期，不复制 View State 或 action single-flight。`ClientPersonalWorldHudView` 保持 scene-bound uGUI adapter。

### 8. Composition、生命周期与 Qualification 不得绕过新边界

`AppComposition` 是唯一允许同时引用 Application ports 与 Infrastructure/Runtime concrete types的位置。它显式创建对象并冻结：

- lifecycle participants；
- tickables；
- UI route/Host registry；
- channel/service/owner dependencies；
- qualification read-only snapshot source。

`AppComposition` MUST 使用两级、强类型的模块化装配，而不是在单个 `Build` 方法逐一创建所有内部 reducer/flow/transaction：

```text
AppComposition
  -> FoundationComposition
  -> InfrastructureComposition
  -> SessionComposition
  -> ChannelComposition
  -> WorldComposition
  -> PresentationComposition
  -> RuntimeQualificationComposition
```

顶层只表达模块创建顺序、跨模块 ports、AppLifetime participants 与最终 Unity 接线；模块 Composition 只创建该模块的内部 state machine、policy、flow、registry、adapter 和 facade，并返回封闭的强类型 bundle。Bundle 只能由 Composition 使用，不得交给 feature，也不得提供 `Resolve<T>()`、类型字典、反射扫描、自动注册或运行期替换。模块 Composition 是一次性建图代码，不是 App Scope service，不登记 tick/stop，也不保存运行时业务状态。

`AppCompositionResult` 必须收窄为 AppRoot 启动、受控 Unity Host 接线和 qualification 只读入口真正需要的启动结果。Feature 不得通过它访问 Session、Channel、World、Router 或 Experience concrete owner；qualification 所需状态通过专门的低敏 snapshot source 汇聚。

Feature 不得取得 `AppCompositionResult`。`AppCompositionResult` 只为 `AppRoot`、受控 Unity Host 接线和 Development qualification 暴露必要强类型结果，不提供集合查询或泛型解析。

Qualification 可以读取已登记 owner 的锁内低敏 snapshot和资源计数，但不得取得 credential、原始 payload、私有 reducer 或 transaction，也不得调用修正方法。资格 fault 只能通过现有 production fault boundary 触发，Release 仍不包含 Development profile、fault 和 soak 入口。

初始化、失败回滚与逆序停止顺序保持现有 contract。子组件的资源由所属 owner 清理；纯 policy/reducer 不登记 AppLifetime。

### 9. 用架构门和 owner registry 控制再次增长

新增客户端架构验证入口，至少检查：

- asmdef 名称、引用 DAG、`noEngineReferences` 与禁止的反向依赖；
- Application/Presentation 源码不引用 Unity、Protocol、Infrastructure；
- Protocol/generated 只被 Infrastructure、Runtime Composition 和协议测试的登记边界引用；
- Feature 不引用 `AppCompositionResult`；
- 每个状态种类在 owner registry 中恰好有一个 owner；
- owner registry 记录 facade、状态、commands、snapshot、module、允许协作者和测试入口；
- Unity 资产引用的 MonoBehaviour、ScriptableObject、SceneContext 与 `.meta` 未因程序集迁移丢失；
- 原单体 runtime asmdef 不再吞并已分层源目录。

数值指标只用于报告：文件行数、成员数、构造依赖数和测试 fixture 大小出现显著增长时提示评审，但硬门基于依赖和所有权，不鼓励通过压缩代码或制造空包装规避。

新增 command、状态或依赖如果超出 owner registry，必须先更新架构文档/验证数据；跨层依赖会直接编译或验证失败。

### 10. 一个 change 内分阶段迁移，但禁止长期兼容层

用户明确要求本次一次解决客户端模块化和超大协调器问题，因此不再拆成多个 OpenSpec changes。实现仍按可运行阶段推进：

1. 固定行为与资格基线；
2. 建立 Foundation、Application contracts 和 adapter mapping；
3. 拆 Session；
4. 拆 Control/Gameplay；
5. 拆 PersonalWorld/Visit/Admission/Recovery；
6. 拆 Router/Experience/产品 View；
7. 添加最终 asmdef DAG 并移动程序集归属；
8. 清理旧接口、更新文档、执行完整资格。

临时 adapter 只允许在同一阶段内存在，并必须在该阶段结束前删除。不得使用 feature flag 让新旧 owner 同时运行，也不得因为迁移困难降低 credential、generation、revision、cancellation 或资格门。

## Risks / Trade-offs

- [单个 change 范围大、回归面广] → 用八个可运行阶段、characterization-first、逐 owner 回滚点和完整资格门控制；任何阶段未绿不得进入下一阶段。
- [Application 去 generated/transport 依赖会引入过多 DTO] → 只为真实业务边界定义不可变 contract，由 adapter 集中映射；禁止逐字段镜像所有协议和通用 envelope。
- [拆分后文件与类型数量增加，跳转仍可能困难] → 每个 owner 保留单一 facade，文档提供 owner 表和用例调用链；只拆可独立测试、独立变化的职责。
- [状态被误拆成多 owner] → owner registry、lease/generation tests 和禁止双写评审；flow/reducer 不发布最终 snapshot。
- [程序集迁移破坏 Unity 序列化] → 保留 Runtime 程序集承载序列化类型、保留源 `.meta`、执行 BootstrapScene/PersonalWorldScene/Prefab/UXML PlayMode 与双 Player build 验证。
- [InternalsVisibleTo 与测试 asmdef 变复杂] → 每个模块维护明确 friend assembly，测试显式引用被测模块，不使用 public 扩张换取测试便利。
- [Channel 拆分破坏 terminal/close 幂等] → 先锁定每个 failure/close 分类、generation 和资源计数；子组件只回报结果，channel owner 单点发布 terminal。
- [Router/Experience transaction 交错产生旧 UI/Scene 回写] → 所有 transaction 使用 navigation/presentation/target/scene lease，并增加故障注入与交错测试。
- [架构脚本变成脆弱文本扫描] → 硬门优先读取 asmdef 和声明式 owner registry；源码禁止引用检查只覆盖稳定 namespace/import，不用格式相关正则判断业务正确性。
- [日志与注释问题仍存在] → 本 change 只保留现有日志行为并遵守当前注释硬规则；完成后分别提出日志与注释 change。

## Migration Plan

1. 记录仓库 commit、Unity/contract/build digest、现有 owner snapshots、核心类型依赖和全部客户端测试结果，补足 characterization gaps。
2. 创建架构 owner registry 与验证脚本的只读基线；先报告现有违规，不提前让尚未迁移模块阻断构建。
3. 在现有 Runtime 程序集内建立 Foundation/Application contracts、opaque credential lease 和 Infrastructure mappers，逐 API 消除 Application 对 concrete transport/generated result 的需要。
4. 按 Session → Channels → World/Visit/Recovery → UI/Experience 顺序结构性拆分；每完成一个 owner，删除旧字段/方法/双写并运行其单元、集成和 fault tests。
5. 当 Application、Presentation 和 Infrastructure 依赖已清洁后创建最终 asmdef DAG，移动源文件时保留 `.meta`；更新 test asmdef 与 friend assembly。
6. 收敛 AppComposition/AppCompositionResult/qualification 接线，确认唯一入口、初始化回滚、逆序停止和 Release 剪裁。
7. 启用架构验证硬门，删除旧单体引用、临时 adapter、过渡 namespace 和未使用接口。
8. 更新主架构/UI/文件结构/接入文档和阅读路径，执行 protocol verify/parity、完整 EditMode/PlayMode、Development/Release build、双 Player 和 client-v1 qualification。
9. 每一阶段形成独立可运行提交。失败时回滚到该阶段前最近绿色提交；若最终资格无法恢复，整体回滚到 change 开始前记录的已资格提交。

## Open Questions

无阻塞问题。具体内部类型名可以在实现时按项目命名规范调整，但程序集 DAG、唯一 owner、port/adapter 方向、拆分职责和行为等价门不得改变。若实现发现必须改变协议、服务端、存储、玩家行为、日志体系或注释规范，必须先更新 artifacts 并重新评审 scope。
