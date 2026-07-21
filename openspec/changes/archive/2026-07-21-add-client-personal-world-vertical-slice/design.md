## Context

服务端 v1、Go 资格客户端、Unity HTTP/WSS/TLS-TCP、PersonalWorld/VisitSession Services 与双 UI routing 已完成。当前 App Scope 能保存 session、world、visit、assignment 和 target flow 的权威客户端投影，但 production UI registry、产品 Host、World Scene 和业务入口仍为空；Windows Player 启动后只显示空窗口。

本 change 第一次引入产品 UXML/USS、uGUI Prefab 与 World Scene，因此必须同时冻结页面与 Services 的连接方式、场景 generation、输入/焦点、错误映射和资产边界。现有 Services、channel 和 router 继续拥有事实与状态机；新代码只能拥有玩家意图、派生 View State 和 Unity 表现生命周期。Unity 序列化资产需要在 Editor 中创建和接线，不手工编写 Scene/Prefab YAML 或 `.meta`。

## Goals / Non-Goals

**Goals:**

- 交付可操作的注册/登录、进入自己的世界、邀请访问、Owner/Visitor 状态、离开/踢出/关闭与安全返回流程。
- 让首批 UI Toolkit screen/overlay 和 uGUI HUD 服从既有 route、Host、Input/focus 与 lifecycle 契约。
- 建立一个实际 World Scene Scope，使场景切换、route binding 与迟到 callback 共同受 target/scene generation 约束。
- 使用既有强类型 operation、Services 与 coordinator，不让 View、Scene 或 Presenter 保存第二份 session/world/visit 事实。
- 在 PC 常用分辨率、mouse/keyboard/gamepad 和双客户端本地流程中完成可复验验收。

**Non-Goals:**

- 不实现角色移动、战斗、NPC、任务、背包、聊天、世界内容生产或最终视觉品质。
- 不增加协议、服务端 operation、存储迁移、Party、Room、ActivityInstance、UDP/KCP 或客户端 listen host。
- 不引入 Addressables、`Resources` 核心目录、通用 ResourceManager、第三方 DI/async 库或万能 UI framework。
- 不实现 token 跨进程持久化、WSS/TCP 独立自动恢复或 `qualify-client-v1` 的完整发布矩阵。

## Decisions

### 1. 以单一 Experience 协调表现意图，不复制既有业务状态机

新增纯 C# `ClientPersonalWorldExperience`，由 `AppComposition` 显式注入 bootstrap、Session、control、WorldAdmission、PersonalWorld、VisitSession、UI router 与 Scene transition 窄边界。它只拥有当前 presentation generation、页面级 intent/cancellation、派生不可变 View State 和已请求的 route/scene transition；不保存 token、credential、WorldInstance endpoint、world/visit snapshot 或 role 最终事实。

Experience 作为 AppLifetime 最后初始化、最先停止的参与者。初始化只打开本地 Login route，不联网；注册或登录按钮才显式执行 version/config、register/login、启动 control run，并在 control 首次进入 `Connected` 后调用 `EnterOwnWorldAsync`。该一次性 readiness 屏障防止客户端已经进入产品流程、服务端却仍把定向邀请判定为 offline；后续断线仍按既有有限恢复和 `ConnectionLost` 终态处理。Session invalidation、world flow、Service snapshot 与 route 通知在锁外收敛为新 View State，任何异步提交同时检查 App、presentation、target、route 和 scene generation。

替代方案是让各 View 直接串联 Session/channel/Services，或再建一套“客户端世界状态机”。前者会造成 command 双发和生命周期分散，后者会复制 `WorldAdmissionCoordinator`，均不采用。

### 2. 页面只接收不可变 View State 与语义化 action port

Login、Shell、WorldVisit 与 WorldHud 各自使用窄 action/binding，不取得 `AppCompositionResult`。View callback 只提交 `Register`、`Login`、`RetryEnterOwnWorld`、`OpenVisit`、`CreateInvite`、`AcceptInvite`、`Leave`、`Kick`、`CloseVisit`、`RetryReturn` 和 `Logout` 等语义化意图；Experience/既有 Services 决定权限、revision、idempotency 与调用顺序。

共享的账号、world flow、invite/member、role、deadline 和低敏错误投影集中派生为不可变 `ClientPersonalWorldViewState`，各页面只读取需要的切片。每类 mutation 同时最多一个 active intent，按钮状态由 intent 与权威 snapshot 推导；caller cancel、timeout 或 commit-unknown 不自动重发。World command 取得唯一 intent 后，状态发布可能立即关闭发起它的 route；该提交边界之后只由 App Scope、Session/target generation 和既有 owner lifecycle 取消，route binding token 不再传入 HTTP/TCP 流程，避免 `JoiningVisit` 等中间态把自身 command 取消。

替代方案是为每个控件创建 Presenter/Service 层，或让 View 直接读 generated message。前者在当前四个逻辑页面上增加无收益层级，后者破坏协议与表现边界，因此不采用。

### 3. 首批 production routes 固定为四个产品入口和一个场景 HUD

Production registry 登记：

| Route | Framework | Layer | Input | Lifecycle |
|---|---|---|---|---|
| `Login` | UI Toolkit | Screen | Text | Cached |
| `Shell` | UI Toolkit | Screen | UI | Cached |
| `WorldVisit` | UI Toolkit | Overlay | UI | Cached |
| `WorldHud` | uGUI | HUD | Gameplay | SceneBound |
| `ConnectionLost` | UI Toolkit | Modal | Modal | Recreate |

`Settings` 仍不登记。Login/Shell/WorldVisit/ConnectionLost 使用 BootstrapScene 直接引用的 `PanelRenderer` Host；WorldHud 使用持久 Host root 下的 uGUI Prefab，但每次只以 current Scene generation bind，场景失效时先关闭交互再 hide/unbind。Route definition 仍不保存 UXML、Prefab 或资源地址。

`PanelRenderer` 在运行时重建 VisualElement 树时，产品 View 必须把 callback 和当前不可变 View State 迁移到新根元素。UI reload 不得取消仍 current 的 route token、gameplay command 或 presentation generation，也不得让旧根元素继续持有 callback。

Gameplay 不持续显示 cursor，也不以每帧轮询打开产品页面。项目 Input System 资产在 `Player` map 中提供独立 `Menu` action，首期绑定 `Tab` 与 Gamepad `Start`；唯一输入 owner 只在 Gameplay mode 将其提升为“打开访问管理”语义意图，Experience 再通过既有 Router 打开 `WorldVisit`。Route 提交为 UI mode 后，Router 统一切换 action map、focus 与 cursor；`UI/Cancel` 只携带 current route identity，Experience 仅允许其关闭 `WorldVisit` 这类非权威 overlay，随后恢复 Gameplay policy。`Player/Interact` 保留给世界交互，不复用为菜单入口。

替代方案是新增第二套路由、字符串页面路径或让 World Scene 动态静态自注册 Host。它们会绕过已验收 registry 或引入顺序竞争，因此不采用。

### 4. BootstrapScene 常驻，PersonalWorldScene 作为唯一首批内容 Scene additive 加载

BootstrapScene 继续作为 build index 0 和唯一应用入口，保存 AppRoot、持久 UI Host 与非敏感场景目录；新增 `PersonalWorldScene` 作为 build 中的内容场景。轻量 `ClientWorldSceneTransitionHost` 使用登记的 build scene identity 执行 additive load/unload，不接受任意调用方路径，也不承担 world target 决策。

每次目标提交为 `OwnWorld` 或 `Visiting` 时，Experience 先使旧 scene generation 失效并关闭旧 SceneBound route，再加载内容 Scene。加载完成后只在该 Scene 的 root objects 中验证恰好一个 `PersonalWorldSceneContext`，显式注入 current `SceneLifetime` 和无 credential 的 world/visit View State，然后打开 `WorldHud`。旧 Scene 卸载、target 改变或 App 停止会取消 token、解除订阅并拒绝迟到写回。

同一个 PersonalWorldScene 同时承载 Owner 与 Visitor 的首期表现，角色差异来自权威 View State；不复制 scene、不根据本地按钮推断 Owner。替代方案是把 AppRoot 随单场景切换销毁，或以全局 `FindObject*`/静态 singleton 发现 SceneContext，均不采用。

### 5. UI 资产使用直接引用，主题保持最小

UI Toolkit 产品页面使用项目内 UXML 与少量 USS，BootstrapScene 直接引用 Unity 6.5 的 `PanelRenderer`/`PanelSettings`；不再新增已停止接收新特性的 `UIDocument`。uGUI WorldHud 使用一个直接引用 Prefab。首期样式语义集中在一个项目 USS 中，以 class/custom property 表达 color、typography、spacing、focus、disabled/loading/error；不新增 Theme manager、运行时主题切换或跨框架 token 生成器。uGUI HUD 的运行时文本统一使用 TextMeshPro，按钮继续使用锁定 uGUI 包提供的 `Button` 并以 `TextMeshProUGUI` 作为可见标签，不创建 Legacy Text。HUD 的少量视觉值保存在 Prefab 序列化字段中，并沿用同名语义角色。

这使当前资产可审阅、可随仓库恢复，也避免在只有一个 HUD 时提前设计资源/主题系统。出现换肤、远程内容或大量跨框架共享 token 后再提出独立 change。

### 6. Flow、错误与安全返回由权威状态驱动

认证成功后 control run 与 own-world 解析由 Experience 启动并观察；control run 的 terminal fault、Session invalidation、WorldAdmission failure 和 Services conflict 映射为封闭低敏 presentation failure。UI 不显示 exception、URL、payload、ticket、admission、token 或 endpoint。

Owner 创建/撤销 invite、kick/close 与 Visitor accept/leave 继续调用既有 Service/coordinator，expected revision 和 idempotency key 不由 View提供。`VisitSafeReturnPush`、kick、close 或 Owner grace 到期先关闭旧 target mutation，再显示 Returning 状态、卸载旧 scene 并重新解析 own-world；失败时保留可重试的 Returning 状态，不能恢复旧 Visitor scene。

### 7. 验收分为纯 C#、Unity Host 和双客户端三层

EditMode 使用 fake action/scene/router/clock 验证 entry、单 intent、route/scene generation、View State、错误映射、session invalidation、Owner/Visitor policy 和 safe-return。PlayMode 使用真实 `PanelRenderer`、VisualElement、Canvas/EventSystem、SceneManager 与程序化 fixture 验证 product Host、输入焦点、additive scene、SceneContext、HUD 和 teardown。

人工 Unity 接线完成后，使用本地服务端与两个 Windows Development Player 验证注册/登录、Owner 建立邀请、Visitor 接受、访问、主动离开、kick/close 与 safe-return；同时检查常用 PC 分辨率和 Player.log。自动测试不依赖公共 listener、Unity Services 或远程资源。

双客户端人工流程可能长时间没有 gameplay application frame，而本 change 不引入未登记 heartbeat 或独立自动恢复。因此本地配置把 `gameplayTcp.idleTimeout` 设为有界的 30 分钟；配置验证只为该字段提供独立长连接上限，handshake/read/write/keepalive/close/shutdown timeout 仍受通用 1 分钟上限约束。人工切换窗口、复制玩家 ID 和检查日志也可能超过原五分钟邀请窗口，所以本地 `visitSession.inviteLifetime` 与客户端创建邀请请求统一为 30 分钟，仍低于服务端 v1 的一小时上限；过期、撤销或不存在的 inbox invite 必须形成可见拒绝，不能表现为按钮静默无响应。

### 8. 协议 identity 不进入自由文本动作，选择与能力由当前权威快照闭合

`VisitSessionID`、`InviteID` 与 current member identity 是 command correlation，不是玩家输入。WorldVisit View 只允许玩家输入创建邀请所需的目标 PlayerID；接受、撤销与移除动作必须从当前不可变 View State 的可选集合建立页面局部 selection，并在每次新 View State 到达时按完整 identity 重新校验。旧 selection 不在新集合中时必须同步失效；恰有一项时可以确定性选中，存在多项时必须由玩家明确选择，不能沿用上一次文本、按列表顺序静默提交或让 View 构造任意 identity。

按钮可用性由 Experience 从 current world flow、VisitSession role/lifecycle、有效 invite/member 集合、连接健康与 active intent 共同派生。View 不再用 `IsOwner` 等宽泛展示字段猜测 command policy：没有 active Owner VisitSession 时不能撤销、kick 或 close，没有选中有效邀请时不能 accept/revoke，没有选中 current member 时不能 kick，Visitor target 未提交时不能 leave。Service 仍在写入 channel 前执行最终 policy gate，因此 UI capability 只是同一权威状态的产品投影，不替代安全校验。

HTTP accept 成功是 invite 已从 pending 转为 accepted 的权威证明；客户端必须在继续 admission/JOIN 前从 inbox 线性化退役该 identity，即使后续连接失败也不能重新显示为可接受。明确 not-found/conflict/validation 拒绝可以退役已被服务器否定的同一 invite；timeout、transport 与 commit-unknown 不能猜测提交结果。新 `VisitInvitePush` 若以更高 created revision 证明同一 VisitSession/Owner/target 的 pending invite，必须替换本地旧 pending identity，因为服务端同一 target 只允许一个 pending invite。

Owner 不直接持有 Visitor 的 accept HTTP 结果，因此以通过 revision gate 的完整 member snapshot 作为消费证明：target 出现在 Visitors 集合时，立即退役同 VisitSession/target 的 outgoing pending identity。这样 accepted/leave 之后旧撤销操作不会继续显示；没有 member 证明时仍不能用时间或本地按钮状态猜测 invite 已消费。

Control 或 gameplay 的非预期 terminal disconnect 必须通过 owner 发布的事件立即使当前产品状态不可交互并显示稳定连接失败；不得等待下一次按钮操作、依赖帧 tick 或延时探测才发现。显式 target 切换、safe-return、logout 与 App shutdown 引起的关闭不属于连接故障。

显式 reconnect 是一个完整恢复事务，不等同于 WSS socket 再次连通。Experience 必须使用 control owner 的一次性 readiness 信号等待 `Connected`，随后显式重进 own-world、提交新的 target/Scene/HUD generation，最后才关闭 `ConnectionLost`；禁止以 `Task.Yield`、Update/tick、固定延时或重复探测读取 channel state。ConnectionLost route token 属于即将关闭的 View，不能反向取消已经取得恢复 intent 的 control/world/scene 流程。任一步失败都保持 modal，并以该次恢复 scope 的稳定失败替换旧错误。

PersonalWorld aggregate revision 与 runtime assignment generation 必须使用独立单调门。服务端恢复可以保持 PersonalWorld revision 不变，同时以更高 assignment generation 发布新的 WorldInstance；客户端先校验 world identity/owner/lifecycle 等持久事实，再独立比较 assignment generation。同 generation 只接受 identity 不变的 lease 单调续期，不能把合法的新实例升代误报为 protocol conflict，也不能允许同 generation endpoint/instance 漂移。

### 9. 进程内 access 到期由 Session owner 在授权请求前单次刷新

Access 与 refresh 的绝对 expiry 已由唯一 Session owner 持有。任何需要 Bearer access 的强类型 HTTP operation 在捕获 current generation 后，必须先按受信 UTC clock 判断 access 是否仍有效；已经到达绝对 expiry 时只调用现有 single-flight `RefreshAsync` 一次，并使用刷新后新 generation 的 access 继续原始 operation。多个并发授权请求共享同一个 refresh，不使用定时器、Update/tick、固定延时、401 循环或替换业务 idempotency key。

Refresh 的 timeout、transport 或 commit-unknown 继续按既有契约使旧 lineage fail closed；服务端明确 unauthenticated 则回到 Login。尚未到期的 access 若被服务端明确拒绝，仍视为权威 session invalidation，不用 refresh 猜测覆盖。UI failure 必须显示玩家可理解的中文低敏文本，不得把 `client.personal_world.*` 内部 key 直接呈现给玩家。

## Risks / Trade-offs

- [首个产品竖切同时触及 UI、场景与网络流程，diff 较大] → 严格复用现有 owner，按 entry、View State、routes、scene、visit、验收分任务落地，不引入额外业务能力。
- [产品资产需要 Unity Editor 接线，文本 diff 不能完全验证] → Scene/Prefab/UXML 接线使用明确清单、EditMode 资产校验、PlayMode fixture 和 Windows Player smoke 共同验收。
- [Control run 与 world flow 并行时可能产生迟到 completion] → Experience 观察并持有 run task，所有提交经过 session/presentation/target generation；停止时先取消 Experience，再关闭 route/scene/channel。
- [单一内容 Scene 暂时不能代表真实开放世界加载] → 本 change 只验证 Scene Scope 与角色模式，不抽象地图流送；出现真实内容分区后另行设计。
- [最小主题方案可能在页面增多后需要演进] → 当前只冻结语义命名和单一 USS，不建立不可逆运行时 API。
- [Access 在显式重连前已到期] → 授权请求先通过绝对 expiry 触发一次 Session single-flight refresh；refresh 失败保持既有 fail-closed，不把认证失败误报为普通 transport。

## Migration Plan

1. 先实现纯 C# Experience、View State/action ports 和确定性 EditMode tests，不依赖产品资产。
2. 扩展 route identity/production registry，并实现四个 UI Toolkit 页面 Host 与 uGUI WorldHud Host 的程序化 PlayMode fixtures。
3. 实现 scene transition/context 边界与 scene generation tests，再在 Unity Editor 中创建并接线 UXML/USS、WorldHud Prefab 和 PersonalWorldScene。
4. 更新客户端 owner 文档与构建场景校验，执行全量 Unity tests、协议 verify、Windows Development build 和双客户端本地验收。
5. 回滚时先移除 production route/scene 接线和资产，再移除 Experience 与 View State；既有 Session、Services、channels、router 和服务端数据无需迁移。

## Open Questions

无。首期页面、route、单一内容 Scene、最小 USS 与不引入资源系统的边界在本 change 内冻结；视觉品质、地图内容、移动与战斗由后续独立 change 决定。
