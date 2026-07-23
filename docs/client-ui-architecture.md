# Unity 客户端 UI 架构

## 文档职责

本文档定义 UI Toolkit 与 uGUI 的页面选型、统一入口、状态、输入、生命周期和验收规则。服务端 v1、客户端网络/业务 Services、统一路由与个人世界首期产品页面均已落地；后续页面继续复用本文定义的单 owner、Host、输入和状态边界，不得建立第二套路由或业务事实。

## 选型原则

以完整逻辑 screen 或场景型 UI 为单位选择技术，不为展示技术栈而拆分控件。

### UI Toolkit 优先场景

- 登录与账号
- 主导航与 shell
- 邀请、访客列表和个人世界访问状态
- 设置、任务、背包、邮件、聊天
- 数据诊断与开发工具
- 大量列表、表单、筛选、标签页和共享样式

### uGUI 优先场景

- 世界空间血条与姓名牌
- 战斗 HUD、准星和技能反馈
- 与 Camera、Canvas、Animator、Timeline、Shader 或 Material 深度绑定的表现
- 随 SceneContext 创建和销毁的 overlay

最终选择必须考虑 Unity 版本、PC 分辨率、性能、输入和团队效率。

## 统一 UI 入口

业务按封闭逻辑 route id 导航，不直接选择框架：

```text
Open(ClientUiRouteId.WorldVisit)
Close(ClientUiRouteId.Settings)
Open(ClientUiRouteId.ConnectionLost)
```

Route definition 至少包含：

| 字段 | 说明 |
|---|---|
| `route_id` | 稳定逻辑标识 |
| `framework_owner` | UI Toolkit 或 uGUI |
| `layer` | screen、overlay、modal、system |
| `input_mode` | game、ui、text、modal |
| `lifecycle` | cached、recreate、scene-bound |

Route definition 不包含 UXML、Prefab 或资源地址。资源引用属于具体 Host 或后续独立资源系统，不能泄漏到导航接口。同一会话中的一个 route 只有一个 active owner，禁止两份可交互实现和 command 双发。

### 当前实现边界

- `ClientUiRouter` 是 App Scope 唯一 route owner，使用有界串行 transition、单调 navigation generation 与不可变 snapshot。
- `ClientUiRegistry` 在任何 Host 副作用前冻结 definition/Host 一对一关系；首期 production 只登记 Login、Shell、WorldVisit、WorldHud 与 ConnectionLost，Settings 保持未登记。
- Screen 与 System 各自只有一个 owner；Overlay 可叠加；Modal 严格按栈顶关闭；最高层且最后提交的 route 才能交互。
- `Cached` 只保留已初始化 Host，关闭时仍会 hide/unbind；`Recreate` 完整 dispose；`SceneBound` 必须绑定正 Scene generation。
- 未登记 route、错误 scene generation、队列过载、调用取消、停止、策略拒绝和 Host failure 都返回稳定结果；结果区分未提交拒绝、幂等未变化、已提交成功和 post-commit failure，不把内部异常文本暴露给页面。
- `ClientPersonalWorldExperience` 从既有 Session/World/Visit owner 派生不可变低敏 View State，并向页面提供窄语义 action；它不复制权威事实，不持有 credential 或 Unity object。
- Experience 明确呈现 `RestoringSession`、`RecoveringControl`、`RecoveringWorld`、`AwaitingScene` 与 terminal `ConnectionLost`。control 恢复期间健康 gameplay/HUD 保持显示但邀请动作冻结；gameplay 恢复立即撤销旧 Scene/input，只有新 Scene/HUD generation 提交后关闭 modal。
- Development资格soak只通过Experience现有菜单/取消intent反复打开关闭WorldVisit，并从Router/Scene owner读取低敏计数；它不直接操作VisualElement、GameObject、focus或cursor，也不能绕过route generation。真实UI Toolkit/uGUI、focus/cursor和Scene teardown仍由PlayMode mandatory场景证明。
- Login、Shell、WorldVisit 与 ConnectionLost 使用 UI Toolkit，WorldHud 使用 scene-bound uGUI。产品 UXML/USS、Prefab 与 Scene 必须由 Unity Editor 创建并以直接引用接线，不得把资源路径加入 route definition。
- 首期只使用一个项目 USS 表达 color、typography、spacing、focus、disabled、loading 与 error 语义；当前不引入 Theme manager、Resources 或 Addressables。

### Gameplay 阶段复用规则

Gameplay 不建立第二个 Router、`UIManager`、输入总管或网络驱动 UI。既有 `ClientUiRouter`、`ClientUiHostRoot`、route/layer/input/lifecycle 规则继续作为唯一 UI 入口：

- UI Toolkit 继续承载背包、武器/技能详情、设置、任务、访问管理、结算详情等完整 screen/overlay。
- scene-bound uGUI 承载血量/资源、技能槽/cooldown、准星、锁定提示、Boss 血条、伤害反馈和世界空间血条。
- 战斗 HUD 是既有 gameplay scene 的 scene-bound route/Host，不创建常驻第二份 HUD owner；Scene generation 变化时旧 HUD 必须 unbind/dispose。
- 同一逻辑能力只能有一个交互 owner。例如武器切换由 gameplay input action 提交时，UI Toolkit 与 uGUI 不能同时各发一次 command。
- UI Toolkit overlay 打开后由现有 Router/Input owner 决定 gameplay input、cursor、focus 和 raycast；HUD 不私自切 action map。

数据流固定为：

```text
GameplayReplica / GameplayPrediction
  -> GameplayPresentationProjector
      -> immutable HudViewState / MenuViewState / GameplayCue
          -> uGUI HUD Host / UI Toolkit page adapter

UI semantic action
  -> Experience/Presenter
      -> gameplay Application command
```

View/Host 只接收不可变 View State 和窄语义 action。它们不得访问 `BattleNetworkClient`、HTTP、WebSocket、TCP、UDP/KCP socket、generated packet、ticket 或 AEAD session，不得自行计算 authoritative damage/cooldown，也不得因动画完成而提交命中事实。View 可以保存选中槽位、hover、动画进度等局部表现状态，但权威 attribute/tag/ability/effect 仍来自 gameplay replica。

## Router、Experience 与 View 的拆分

```text
Application owners/snapshots
          ↓
ClientPersonalWorldExperience
  ├─ PersonalWorldPresentationState
  ├─ PersonalWorldViewStateProjector
  ├─ PersonalWorldFailureMapper
  ├─ SessionInvalidationPresentationTransaction
  ├─ RecoveryPresentationTransaction
  └─ PersonalWorldSceneTransaction
          ↓ semantic routes/actions
ClientUiRouter
  ├─ ClientUiTransitionPlanner
  ├─ ClientUiTransitionQueue
  ├─ ClientUiRouteState
  ├─ ClientUiInteractionResolver
  └─ ClientUiTransitionTransaction
          ↓
Runtime Hosts / Views / Scene adapters
```

Router 和 Experience 位于无 Unity 的 `IHomeland.Client.Presentation`。Router 只提交 route/navigation 事实；Host transaction 执行 initialize、bind、show、focus、rollback 与 dispose。Experience 只订阅 Application owner、派生不可变 View State 并暴露语义 action；它不能访问 HTTP、socket、generated message 或 credential。

`ClientPersonalWorldUiToolkitView` 是 Runtime 中唯一产品根 View owner，但 Login、Shell、WorldVisit、ConnectionLost 的节点校验与渲染分别交给页面 adapter。页面 adapter 可以保存选择高亮等页面局部状态，不得复制 Session、PersonalWorld、VisitSession 或连接状态。uGUI HUD 仍由独立 Host 绑定，同一逻辑 screen 不允许出现第二个 active owner。

Scene/route 收敛采用 transaction generation gate：旧 Scene 先失效，加载与 HUD 提交必须同时匹配 presentation、target 和 scene generation；Session invalidation 的优先级高于 recovery，最终只收敛到 Login。页面 route token 只取消页面等待，不能撤销已提交的 Application command。

## 状态边界

- Account/PersonalWorld/VisitSession Services 保存业务事实。
- 简单 view 直接读取只读状态并提交语义化 command。WorldVisit 不允许玩家手填 VisitSessionID、InviteID 或 member identity：列表项保存页面局部 selection，每次不可变 View State replacement 都重新核对完整 identity；旧项消失立即取消选择，多项时不按顺序静默选择。
- 每个按钮只读取 Experience 投影的精确 capability 和 current collection；空集合、角色不匹配、连接终止或 action single-flight 时保持 disabled。失败绑定产生它的 authority scope，新 world/visit snapshot 到达后确定性清除过期错误，不使用延时、Update/tick 修正或自动 mutation 重试。
- 多个 view 共享复杂派生数据时才引入纯 C# Presenter/View State。
- View State 必须可从业务事实派生。
- UI Toolkit view 与 uGUI view 不互相持有控件引用。
- View 不直接访问 HTTP、WebSocket、TCP、UDP/KCP 或 generated transport client。

## Host 边界

### UI Toolkit Host

- 适配直接序列化的 `PanelRenderer` 与 panel
- 适配 show/hide/dispose
- 只在页面 change 明确需要时注册与解除 callback
- 与统一输入和层级表协调

### uGUI Host

- 适配直接序列化的 Canvas、CanvasGroup、EventSystem 和可选默认 focus
- 适配 show/hide/dispose
- 管理固定 sorting slot、raycast 和 scene binding
- 与 SceneContext 生命周期协调
- uGUI 运行时文本使用 TextMeshPro；交互按钮使用锁定 uGUI 包的 `Button`，可见标签使用 `TextMeshProUGUI`，不新增 Legacy Text

Host 只获得当前 route binding、页面 cancellation、focus 与必要 Unity 对象，不获得 transport、generated message、credential 或完整全局容器。Router 将 Host 失败映射为稳定、低敏且区分提交边界的 transition result。

## 输入与层级

客户端统一使用 Input System。持久 `ClientUiHostRoot` clone 项目 Input System 资产并成为 `Player`/`UI` action map 与 cursor 的唯一 owner；原资产保持只读。Router/Host 共同协调：

- modal 独占
- text input 与 keyboard focus
- game action 屏蔽
- cursor visible/lock state
- gamepad navigation 与 previous focus 恢复
- UI Toolkit panel 与 uGUI Canvas 排序

Gameplay input state 不绑定 UI route owner；只有 UI、Text 与 Modal mode 才绑定当前交互 route，避免 HUD 打开时把 gameplay 误判为 UI 输入。

Gameplay 中打开产品菜单必须使用项目 Input System 资产内的独立语义 action，不轮询具体键位，也不复用世界交互。首期 `Player/Menu` 绑定 Keyboard Tab 与 Gamepad Start，由 `ClientUiHostRoot` 在 Gameplay mode 提升为一次“打开访问管理”意图；Experience 只调用既有 Router。WorldVisit 提交后切换到 UI action map 并解锁显示 cursor；`UI/Cancel` 只提交 current route identity，Experience 仅关闭允许返回的 overlay，再原子恢复 Player action map 与锁定隐藏 cursor。

统一层级：

```text
Game World
HUD
Screen
Overlay
Modal
System Notice
```

页面不得通过任意超大 sorting order 抢占顶层。顶层 modal 激活时，底层 view 不接收指针或 gameplay command。

## 生命周期

统一语义：

- `Initialize`：一次性资源与 callback 准备
- `Bind`：绑定只读状态或 View State
- `Show`：进入显示与输入状态
- `Hide`：停止交互和页面级异步任务
- `Unbind`：解除订阅
- `Dispose`：释放资源

页面隐藏或销毁后，延迟响应可以更新业务 Service，但不得重新写入或隐式显示旧 view。页面级 cancellation 必须与 lifecycle 绑定，迟到回写还必须同时匹配 route、navigation generation、Host generation 与 Scene generation。

## 主线程

```text
Network Receive
  -> MainThreadDispatcher
  -> Application Service
  -> optional View State
  -> Active View
```

任何后台线程不得直接读写 `VisualElement`、GameObject、Component 或 UnityEngine.Object。

## 设计语义

两套 UI 共享语义，不共享控件：

- color roles
- typography roles
- spacing/radius/border
- layer/elevation
- motion duration/easing
- focus/disabled/loading/error states

语义可以通过 ScriptableObject、配置或生成 token 表达，再映射到 USS 与 uGUI theme。具体存储方式由首次 UI change 确定。

## PC 验收

- 常用 16:9、16:10、21:9 分辨率
- 窗口化、无边框和全屏
- DPI 与 UI scale
- mouse/keyboard/gamepad
- text input 与 IME
- modal、焦点和 cursor 恢复
- UI Toolkit/uGUI 同屏射线与遮挡
- 页面销毁后不回写
- loading、disabled、retry 和 error 状态
- PlayMode 与截图/视觉回归（具备 CI 条件后）
