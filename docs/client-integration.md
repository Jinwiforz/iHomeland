# Unity 客户端接入契约

## 文档职责

本文档定义 Unity 开始实现前服务端必须交付的 artifacts、客户端接入顺序和端到端验收。它不定义服务端基础语义。

## 服务端交付包

`qualify-server-v1` 必须提供：

- versioned Protobuf schema
- 可由 Go validator 消费的 schema 与 route registry
- message id 与错误码目录
- HTTP OpenAPI 或等价 contract fixtures
- WSS/TCP golden packets
- endpoint manifest 示例
- token、session epoch 和 connection ticket 说明
- TLS 本地开发证书策略
- Go test client 场景和预期结果
- 服务端本地启动、测试、验证和清理命令

缺少任一基础 artifact 时，不开始对应 Unity channel。

S0 及后续服务端 changes 共同维护以下契约入口：

- `shared/proto/ihomeland/`：跨端 Protobuf schema。
- `tools/proto/buf.gen.csharp.yaml`：由统一入口写入受控 staging 的 C# template。
- `shared/contracts/registry/`：消息、错误与路由源；运行时投影在内存构建。
- `shared/contracts/fixtures/`：HTTPS cases、realtime golden packets 与 negative coverage manifest。
- `versions.yaml`：Unity、协议、生成工具与 `Google.Protobuf` C# runtime 的版本和完整性基线。

## 跨端生成

- 实时消息唯一协议源是 `shared/proto/`，HTTPS 唯一协议源是 `shared/contracts/http/v1/openapi.yaml`。
- Go/C# 使用 `versions.yaml` 锁定的 compiler/runtime/plugins；版本、依赖恢复与本机缓存规则由 `docs/technology-versions.md` 负责。
- C# generated code、生成 asmdef 与锁定 runtime DLL 输出到已忽略的 `client/Assets/App/Generated/Protocol/`；该目录由 `tools/proto/` 独占，不得放手写文件，也不得成为 Scene、Prefab 或 ScriptableObject 的序列化引用。
- 禁止手工修改 generated code。
- 手写 asmdef 只按固定名称 `IHomeland.Client.Protocol.Generated` 引用协议程序集；缺失生成目录时必须先运行统一生成入口，不能回退到 `Assembly-CSharp` 或复制类型。
- `Google.Protobuf` 由统一入口恢复；不得手工导入 runtime DLL，完整供应链和故障诊断规则由 `docs/protocol-compatibility.md` 负责。
- C# 必须通过 Go golden packets 验证 payload、proto-name JSON、envelope 与摘要 parity。
- CI 从无 generated code 的检出状态开始，先执行 `tools/proto/proto.ps1 generate` 再编译，并检查重复生成结果一致及没有 tracked generated/.meta。
- 客户端协议 change 必须先运行 C# generation 和 Unity EditMode golden parity tests，不得手写或复制 generated types。

## 接入顺序

### 1. Composition 与配置

- AppBootstrap/AppComposition/AppRoot
- environment config
- endpoint manifest model
- logger、main-thread 和 lifecycle

### 2. HTTPS

- version/config
- register/login/refresh/logout
- access token/session epoch
- WSS/TCP connection ticket
- timeout、cancel、retryable error

当前 HTTP 边界固定接入 version、config、register、login、refresh、logout、connection ticket、own-world bootstrap、invite accept 与 world admission 十个 operation。客户端以手写不可变 projection 和显式 `System.Text.Json` codec 消费 OpenAPI/共享 fixtures，不生成或提交 HTTP C# 代码，也不提供任意 path/body escape hatch。

- Production base URI 必须为 HTTPS；Local/Test 的明文例外必须同时满足显式环境和 loopback host。
- App Scope 初始化不自动联网；后续 application flow 只能在 AppRoot Running 后显式调用 `ClientBootstrapService`。
- Version/config 两步全部通过后才发布配置；协议或最低客户端版本不兼容时阻止认证调用。
- Password 只存在于 register/login 调用；access token、refresh token 与 ticket 只由 `SessionCoordinator` 管理，不写入 Scene、Prefab、ScriptableObject、PlayerPrefs 或日志。refresh lineage 只有在 Windows DPAPI `CurrentUser`、environment binding、原子 replace 与 owner-specific mutex 全部成功后才能提交 current Session；access token、ticket 与 admission 永不持久化。
- Refresh single-flight，并以 session generation 拒绝迟到结果；后续等待方可以独立取消。Refresh/logout commit-unknown 进入 `Unresolved`，直到新的 register/login 或显式 forget 前不得继续 authenticated operation。
- Transport 不自动重试。Caller cancel、deadline、transport、oversized、malformed 与结构有效的 server error 保持不同结果；`Retry-After` 只作为事实返回。
- App 启动由一次性 `ClientSessionRestoreCoordinator` 执行 `Read -> bootstrap -> refresh -> secure replace -> Session commit`。无 record 或 Unsupported 进入 Login；明确 rejected/corrupt/commit-unknown 退役旧 lineage；无法确定的 transport/storage 结果只显示稳定低敏失败，不创建第二个 Session、control 或 target。

`acceptVisitInvite` 只接受由 current inbox selection 取得的 VisitSessionID、InviteID、expected revision 与稳定 idempotency key，并返回匹配且未过期的 reservation；页面不得提供 correlation 自由文本入口。Accept 成功后客户端退役该 invite，后续 admission/JOIN 失败也不能让已消费 identity 重新可点；commit-unknown 仍等待权威 replacement。`issueWorldAdmission` 只产生绑定 session generation、expiry 与单次交付的短期 lease；Visitor admission 还携带签发判断冻结的权威 `visitRevision`，JOIN/RECONNECT 首帧必须使用该值，不能使用断线前 projection、算术推导或冲突探测。两者都不能成为 PersonalWorld 或 VisitSession 最终事实，HTTP 对象图不拥有 socket。

### 3. WSS Control

- 先通过 HTTPS 获取 `WSS` ticket，再以 `Authorization: Ticket <32 位小写十六进制 nonce>` 连接 advertised endpoint 的 `/v1/control`
- 必须协商 `ihomeland.control.v1`，只接收 binary `ReliableEnvelope`，不得向 WSS 发送业务 application frame
- ticket 一次性使用；upgrade 后失败、断线或重放都必须重新通过 HTTPS 签发，不能缓存或恢复旧 ticket
- 接收 maintenance、forced logout、queue、endpoint/assignment、visit 与 session invalidation push
- 独立执行 heartbeat、close reason、sequence 缺口检测和有界重连，并把 generated payload 投递主线程
- session invalidation/forced logout 后关闭 WSS 与后续 TLS/TCP、清理本地 session 并回到登录流程

当前客户端已接入只接收 WSS control owner；实现结构与生命周期见[客户端运行时架构](client-architecture.md#当前-wss-control-边界)。初始化不自动连接，Runtime 不公开 WSS application send API；assignment/visit control PUSH 由 PersonalWorld/VisitSession Services 作为收敛 hint 消费。

### 4. TLS/TCP Business

- 先通过 HTTPS 分别取得 `TLS_TCP`/`GAMEPLAY` ticket 与 world admission；客户端连接 advertised endpoint，production 使用 TLS 1.3，本地明文只允许 loopback
- TLS 建立后先发送 `tcp-preface.json` 定义的 `IHTP` v1 preface：4-byte big-endian 长度、purpose、32 字节 ticket 与 48 字节 admission；认证失败不会向客户端公开提交阶段，客户端不得猜测或重用任一 credential，必须重新签发完整凭据组
- preface 成功后才发送 `ReliableEnvelope`；每条业务 frame 同样使用 4-byte big-endian 长度，不得假设一次 socket read 等于一个 frame
- 使用唯一 receive pump 与 serialized writer，维护严格单调的双向 sequence、有界 pending request/command registry 和 writer backpressure
- Active connection 每 15 秒通过同一 typed operation、pending correlation 与 serialized writer 发送 `GAMEPLAY_HEARTBEAT_REQUEST(1)`；10 秒内未收到匹配的 `GAMEPLAY_HEARTBEAT_RESPONSE(2)` 即终结 current generation，不允许另建 timer writer、延时猜测成功或以帧 tick 修正状态
- `OWN_WORLD` 可直接请求 snapshot；`JOIN`/`RECONNECT` 必须把同一 admission 放入首个匹配 command，并在 response 前保持 pending
- 只接收登记的 response/error 与 2002、2121、2122 push；`VISIT_SAFE_RETURN_PUSH` 到达后立即停止旧 target mutation，等待有界关闭并进入受控返回流程
- ticket、admission、完整 payload 和 assignment 私有字段不得进入客户端日志；session epoch 失效时同时关闭 WSS/TCP

当前客户端已接入独立 gameplay channel owner；实现结构与生命周期见[客户端运行时架构](client-architecture.md#当前-tlstcp-gameplay-边界)。该边界只交付 transport、强类型 operation 与可信 PUSH；最终 snapshot 与 target flow 由纯 C# Services/coordinator 保存，唯一 `ClientConnectionRecoveryCoordinator` 消费 typed channel lifecycle 并拥有 automatic/manual single-flight，channel 自身不猜测业务 target。

WSS 与 gameplay 独立恢复但共享 Session owner。WSS `Recovering` 只冻结依赖 inbox/hint 完整性的邀请动作，健康 gameplay 与 Scene 不重建；新 control generation 必须经 gameplay 请求完整 world/Visit snapshot 后才恢复能力。Gameplay unexpected disconnect 先撤销旧 mutation、HUD/Scene binding，再按冻结 descriptor 恢复 OwnWorld 或在 grace 内以 `VisitReconnectCommand` 作为 Visitor 新连接唯一首帧。冻结descriptor同时绑定服务端Session identity/epoch与本地generation：同一Session上的access refresh可把generation单调重绑定到current，不同Session、epoch或换账号必须拒绝继承。automatic terminal 后才开放 manual retry；两者共用 45 秒总 deadline 和 session/recovery/target/scene 四重提交 gate，不使用无限重试、延时猜测或 tick 修正。

C3资格按 `automatic -> soak -> prepare-manual -> finalize` 继续同一run：两种Player smoke必须观察App Scope的低敏Running标记；Development soak使用run内绝对存储根、环境传入的一次性测试凭据和三轮双通道故障；人工阶段固定两个隔离profile。Soak/人工secure record只能由Player内正式Session/store owner精确删除，Release smoke发现当前Windows用户已有default record时直接拒绝，不轮换或清理操作者数据。

### 5. Account/PersonalWorld/VisitSession Services

- `PersonalWorldService` 保存 primary/current world 与 assignment，完整 replacement 使用单调 revision，control assignment 只作为 refresh hint。
- `VisitSessionService` 保存 current VisitSession、role、定向 invite inbox 与 control hint，并统一施加 Owner/Visitor 与 expected revision 写入门。
- `WorldAdmissionCoordinator` 线性化 own-world、join visit、visiting 与 safe-return；迟到 completion 同时经过 session/target generation gate。
- Services 只公开不可变、无 credential snapshot；产品 UI 不接触 transport type、generated message 或第二份最终事实。

### 6. UI Vertical Slice

已于 2026-07-21 归档的 `add-client-personal-world-vertical-slice` 通过统一 routing/Host/Input 边界接入下列产品页面。未提交登录前只打开本地 Login route，不读取 credential 快照、不发业务网络请求，也不加载内容场景：

- login
- home/shell
- own-world loading and state
- visit invite/list/detail and member state
- join/leave/kick/reconnect/safe-return commands
- server push and errors

真实双客户端验收派生的四个独立 change 同日归档：stale VisitSession Open 由服务端按 current assignment 显式收敛；CreateInvite 只接受非 Owner 的 active Player；撤销、到期、接受或 terminal close 通过既有 message 2100 发布精确 `RETIRED` tombstone；静默 gameplay connection 由 TLS/TCP heartbeat 保活，断线后只允许玩家显式提交有界 single-flight 恢复。上述路径都不使用延时猜测、tick 修正或客户端伪造权威状态。

## 重构后的四条阅读路径

下面的路径用于定位职责，不表示调用方可以跨层取得 concrete owner。

### 启动

```text
AppBootstrap
  -> AppComposition
  -> seven module Compositions
  -> RuntimeQualificationComposition
  -> AppRoot.Initialize
  -> AppLifetime ordered participants
```

从 `Core/Bootstrap/AppBootstrap.cs` 开始，只在需要理解某个 module 的对象创建时进入对应 Composition。业务初始化语义分别阅读 Application owner/flow；不要从 `AppCompositionResult` 反向搜索全部服务。

### 登录并进入 OwnWorld

```text
Login page adapter
  -> ClientPersonalWorldExperience.LoginAsync
  -> SessionCoordinator / LoginSessionFlow
  -> ClientControlChannelPort
  -> EnterOwnWorldFlow
  -> PersonalWorldProjectionReducer
  -> WorldAdmissionCoordinator commit target generation
  -> PersonalWorldSceneTransaction
  -> ClientUiRouter commit Scene/HUD routes
```

页面只提交语义 action。Session、channel、world target、Scene 与 route 各自在自己的 owner 内提交，Experience 只在 generation 全部匹配后发布 View State。

### 接受访问邀请

```text
WorldVisit page adapter selected inbox identity
  -> ClientPersonalWorldExperience.AcceptInviteAsync
  -> EnterVisitWorldFlow
  -> HTTPS reservation/admission ports
  -> Gameplay JOIN typed port
  -> VisitSessionProjectionReducer + PersonalWorldProjectionReducer
  -> WorldAdmissionCoordinator commit Visiting
  -> Scene/route transaction
```

`VisitSessionID`、`InviteID` 与 expected revision 来自 current inbox，不提供自由协议输入；迟到响应必须同时通过 Session 和 target generation gate。

### 断线恢复

```text
channel terminal event
  -> ClientConnectionRecoveryCoordinator
  -> ConnectionRecoveryStateMachine + plan builder
  -> RecoverControlFlow or RecoverGameplayFlow
  -> EnterOwnWorldFlow or ReconnectWorldTargetFlow
  -> authoritative projections
  -> RecoveryPresentationTransaction
  -> new Scene/HUD generation commit
```

Control 与 Gameplay 独立恢复但共享唯一 Session authority。Session invalidation 会抢占恢复并通过 `SessionInvalidationPresentationTransaction` 清除 target、Scene 和 route，最终只保留 Login。

## 错误映射

客户端必须区分：

- protocol incompatibility
- unauthenticated/session expired
- permission denied
- validation
- world/visit conflict、stale assignment 或 permission transition
- not found
- rate limited/retry after
- dependency unavailable
- transport disconnected/timeout
- internal safe message

UI 不展示内部 exception、SQL、Redis 或完整凭据。

## 会话恢复

```text
App Start
  -> HTTPS version/config
  -> secure restore 成功则无密码进入 current Session
  -> 无可恢复 lineage 时 register/login
  -> acquire WSS/TCP tickets
  -> connect control/business channels
  -> resolve own-world or active visit context
  -> open target screen
```

WSS 与 TCP 独立恢复并共享 session owner；epoch 失效时停止业务、退役 secure lineage、关闭全部通道并回到登录流程。恢复 snapshot 只保存服务端Session identity/epoch、本地generation与低敏descriptor，不复制 credential、endpoint 或第二份业务事实；同一Session的refresh只推进本地generation，不同Session不能复活旧target。Scene 成功加载并提交 HUD 之前不能关闭恢复 modal。

## 世界与访问快照

- PersonalWorld/VisitSession Service 分别保存各自最高 revision。
- Assignment 完整撤销后保留最高 generation tombstone；同代或更旧 WorldInstance 不得复活。
- Response 与 push 使用同一 snapshot 语义。
- 低 revision snapshot 丢弃。
- 同 revision 重复消息保持幂等。
- 页面关闭不阻止 Service 更新。
- 新页面从 Service 当前状态派生展示。

## 个人世界进入门

只有服务端分别完成 PersonalWorld、WorldInstance placement、VisitSession、storage、admission 的实现与 Go 测试客户端资格验收，并冻结对应 OpenAPI/Protobuf、route/error registry、fixtures 和故障语义后，Unity 才能提出个人世界接入 change。

交付包必须额外提供：

- PersonalWorld identity/owner 与 world snapshot contract
- Owner/Visitor role 和交互 permission registry
- invite、accept/reject、admission、kick、leave 与 Owner unavailable contract
- WorldInstance assignment generation、endpoint 与 reconnect rules
- Owner disconnect grace、VisitSession close 和 Visitor safe-return fixtures
- interaction mutation owner、reward settlement owner、idempotency 与 revision matrix
- Go test client 的 own world、visit、disconnect、rebuild 和 stale admission scenarios

客户端接入顺序固定为：

```text
PersonalWorld projection
  -> Visit invite/control projection
  -> one-time world admission
  -> WorldInstance reliable channel
  -> OwnWorld/Visiting state machine
  -> SceneContext world adapter
  -> product UI routes and interaction presentation
```

进入自己的世界：

```text
Authenticated PlayerID
  -> query primary PersonalWorld
  -> resolve current WorldInstance assignment
  -> consume Owner admission
  -> apply authoritative world snapshot
```

访问他人世界：

```text
receive invite
  -> explicit accept
  -> server eligibility/capacity validation
  -> consume Visitor admission for Owner WorldInstance
  -> enter Visiting mode
  -> leave/kick/owner-unavailable
  -> resolve and return to own PersonalWorld
```

客户端不能把 invite 当作连接凭据，不能从好友 PlayerID 拼接 endpoint，也不能在 Owner 断线后本地选举新 Owner。VisitSession close 后必须先停止 command，再销毁场景和投影；迟到 push、response 或资源 callback 通过 session/instance generation 丢弃。

## 验收场景

- 新安装注册并登录
- 进程内 refresh；安全持久化 capability 交付后的 token restore
- WSS/TCP ticket 一次性使用
- 默认进入自己的 PersonalWorld
- 双客户端邀请、接受、访问、踢出和主动退出
- WSS 独立中断
- TCP 独立中断与 world/visit 恢复
- session epoch 强制失效
- 服务端 graceful shutdown
- Redis flush 后允许的恢复/失效行为
- 慢网络、timeout、重复 push 和低 revision
- Windows Development/Release build
- 直接邀请访问且不创建虚假 Party/Room
- Visitor permission 与 Owner-only command 拒绝
- Owner grace 内恢复和 deadline 到期安全返回
- stale invite/admission/WorldInstance 拒绝
- self、missing、inactive invite target 零 mutation 拒绝，合法 active Visitor 仍可受邀
- revoke、expire、accept 与 terminal close 后 Visitor inbox 应用精确 `RETIRED` tombstone
- 服务端重启遗留旧 AssignmentStamp VisitSession 后，Owner 首次显式 Open 收敛到 current session
- 静默 gameplay heartbeat、网络黑洞与显式重连 deadline 收敛
- Visiting 返回 OwnWorld 后无旧场景订阅或 callback 回写

客户端验收必须与服务端 Go test client 对同一 contract fixtures 得出一致业务结果。

## Go 资格客户端的长期职责

`server/internal/testclient` 与统一 qualification 入口在 `qualify-server-v1` 后继续保留，作为服务端公开契约的长期自动化消费者，不是 Unity 的临时替身或产品 SDK。服务端新增或修改公开 HTTP operation、实时 message/channel、credential、错误或恢复语义时，对应 change 必须在独立 capability group 中同步扩展资格 manifest 与 runner，并继续执行全部仍受支持的旧 mandatory 回归。只重构服务端内部 package、算法或 storage adapter 且公开行为不变时，不修改资格客户端来迁就内部结构。

未来 Activity、battle 等能力分别维护自己的 qualification group，完整发布门聚合这些 group；不得把全部业务塞进单个巨型 scenario。资格客户端只验证公开网络输入输出、兼容、安全、恢复和资源边界，不拥有 Scene、Prefab、GameObject、输入、UI、表现或玩家体验，也不复制服务端领域状态机和结算规则。正式游戏客户端始终由 Unity 实现。

已冻结版本需要破坏性演进时，先通过独立 OpenSpec 定义新版本、兼容窗口和迁移，再让资格客户端并行验证受支持版本。旧场景只有在对应版本正式退役后才能离开 active matrix，历史 manifest 和结果仍由 Git 保留。
