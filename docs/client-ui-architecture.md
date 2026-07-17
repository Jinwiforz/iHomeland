# Unity 客户端 UI 架构

## 文档职责

本文档定义 UI Toolkit 与 uGUI 的页面选型、统一入口、状态、输入、生命周期和验收规则。服务端 v1 与客户端网络/业务 Services 已满足 UI 基础设施进入条件；当前只落地路由、Host 与输入边界，产品页面仍由个人世界竖切 change 交付。

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
- `ClientUiRegistry` 在任何 Host 副作用前冻结 definition/Host 一对一关系；production registry 当前为空，因此启动不会创建占位页面。
- Screen 与 System 各自只有一个 owner；Overlay 可叠加；Modal 严格按栈顶关闭；最高层且最后提交的 route 才能交互。
- `Cached` 只保留已初始化 Host，关闭时仍会 hide/unbind；`Recreate` 完整 dispose；`SceneBound` 必须绑定正 Scene generation。
- 未登记 route、错误 scene generation、队列过载、调用取消、停止、策略拒绝和 Host failure 都返回稳定结果；结果区分未提交拒绝、幂等未变化、已提交成功和 post-commit failure，不把内部异常文本暴露给页面。
- 当前没有产品 UXML、USS、Prefab、Presenter 或业务 route definition；`Login` 等 enum identity 只冻结后续接线名称，不代表页面已经实现。

## 状态边界

- Account/PersonalWorld/VisitSession Services 保存业务事实。
- 简单 view 直接读取只读状态并提交语义化 command。
- 多个 view 共享复杂派生数据时才引入纯 C# Presenter/View State。
- View State 必须可从业务事实派生。
- UI Toolkit view 与 uGUI view 不互相持有控件引用。
- View 不直接访问 HTTP、WebSocket、TCP 或 generated transport client。

## Host 边界

### UI Toolkit Host

- 适配直接序列化的 UIDocument 与 panel
- 适配 show/hide/dispose
- 只在页面 change 明确需要时注册与解除 callback
- 与统一输入和层级表协调

### uGUI Host

- 适配直接序列化的 Canvas、CanvasGroup、EventSystem 和可选默认 focus
- 适配 show/hide/dispose
- 管理固定 sorting slot、raycast 和 scene binding
- 与 SceneContext 生命周期协调

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
