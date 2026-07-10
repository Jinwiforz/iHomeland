# Unity 客户端 UI 架构

## 文档职责

本文档定义 UI Toolkit 与 uGUI 的页面选型、统一入口、状态、输入、生命周期和验收规则。UI 实现必须等待服务端 v1 与客户端网络/业务 Services 稳定。

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

业务按逻辑 screen id 导航，不直接选择框架：

```text
Open(ScreenID.WorldVisit)
Close(ScreenID.Settings)
ShowModal(ModalID.ConnectionLost)
```

Screen definition 至少包含：

| 字段 | 说明 |
|---|---|
| `screen_id` | 稳定逻辑标识 |
| `framework_owner` | UI Toolkit 或 uGUI |
| `layer` | screen、overlay、modal、system |
| `input_mode` | game、ui、text、modal |
| `lifecycle` | cached、recreate、scene-bound |
| `resource_key` | UXML/USS、prefab 或资源地址 |

同一会话中的一个 screen 只有一个 active owner，禁止两份可交互实现和 command 双发。

## 状态边界

- Account/PersonalWorld/VisitSession Services 保存业务事实。
- 简单 view 直接读取只读状态并提交语义化 command。
- 多个 view 共享复杂派生数据时才引入纯 C# Presenter/View State。
- View State 必须可从业务事实派生。
- UI Toolkit view 与 uGUI view 不互相持有控件引用。
- View 不直接访问 HTTP、WebSocket、TCP 或 generated transport client。

## Host 边界

### UI Toolkit Host

- 创建 UIDocument 与 panel
- 加载 UXML/USS/controller
- 适配 show/hide/dispose
- 注册与解除 callback
- 与统一输入和层级表协调

### uGUI Host

- 创建 Canvas、prefab 和 scene/world overlay
- 适配 show/hide/dispose
- 管理 camera、sorting layer 和 scene binding
- 与 SceneContext 生命周期协调

Host 只获得所需的状态、command、logger 和 main-thread 接口，不获得完整全局容器。

## 输入与层级

客户端统一使用 Input System。UI Service/Host 协调：

- modal 独占
- text input 与 keyboard focus
- game action 屏蔽
- cursor visible/lock state
- gamepad navigation 与 previous focus 恢复
- UI Toolkit panel 与 uGUI Canvas 排序

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

页面隐藏或销毁后，延迟响应可以更新业务 Service，但不得重新写入或隐式显示旧 view。页面级 cancellation 必须与 lifecycle 绑定。

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
