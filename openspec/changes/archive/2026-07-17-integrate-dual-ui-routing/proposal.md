## Why

客户端已经具备稳定的 App Scope、网络通道和 PersonalWorld/VisitSession Services，但尚无统一页面 owner；若下一步直接实现登录或个人世界页面，UI Toolkit 与 uGUI 容易分别管理导航、输入、焦点和生命周期，形成重复页面、command 双发与销毁后回写。现在需要先建立可独立验收的双 UI 路由边界，为后续个人世界客户端竖切提供唯一表现入口。

## What Changes

- 增加 App Scope 的纯 C# UI router，以封闭 `ClientUiRouteId`、显式 definition registry 和单一 active owner 管理 screen、overlay、modal 与 system layer。
- 为 UI Toolkit 与 uGUI 建立窄 Host adapter 契约，统一 `Initialize`、`Bind`、`Show`、`Hide`、`Unbind`、`Dispose` 语义；具体 view 不互相持有控件，也不能访问 transport、generated message 或全局容器。
- 统一 Input System action-map、modal 独占、gameplay command 屏蔽、cursor、keyboard/gamepad focus 与关闭后的 previous-focus 恢复，并固定跨框架 layer 顺序。
- 以 navigation generation、页面级 cancellation 和有界串行转换处理并发 open/close、Host 失败、应用停止和迟到 callback；隐藏或销毁的 view 不得重新显示、抢回焦点或提交 command。
- 通过纯 C# EditMode tests、实际 Host PlayMode tests 与 Windows Development build 验证唯一 owner、路由幂等、层级、输入、焦点、回滚、逆序停止和默认零业务网络副作用。
- 本 change 不交付登录、主界面、邀请/访问列表、HUD、世界空间 UI、SceneContext、Prefab/UXML 视觉内容、资源加载或个人世界业务流程。

## 进入条件与边界

- `qualify-server-v1`、客户端基础运行时、协议接入、HTTP/WSS/TLS-TCP 与 PersonalWorld/VisitSession Services 已完成，UI 基础设施可以只消费既有窄边界而不绕过交付顺序。
- `ClientUiRouter` 拥有 active route、layer、navigation generation 与页面 cancellation；`ClientUiHostRoot` 拥有 Input System clone、cursor 与 Unity Host；账号、PersonalWorld、WorldInstance 和 VisitSession 最终事实仍由既有 Services 持有。
- 本 change 不修改协议、存储或服务端安全边界，不在 UI 中保存 credential、generated payload 或连接身份。
- 自动验收覆盖纯 C# 路由事务和真实 Unity Host；人工验收只负责 BootstrapScene 直接引用、Unity 全量测试与 Windows Development Player。

## Capabilities

### New Capabilities

- `client-ui-routing`: 定义 UI Toolkit/uGUI 的统一路由、Host、层级、输入焦点、生命周期、状态边界和分层验收行为。

### Modified Capabilities

无。

## Impact

- 客户端 App Scope 将新增 UI router、definition registry、输入/焦点协调与可关闭生命周期参与者，并由 `AppComposition` 显式创建和注入。
- Unity 侧将新增轻量 UI Toolkit/uGUI Host 适配代码及测试装配，但不会改变既有 PersonalWorld、VisitSession、HTTP、WSS、TLS/TCP 或协议语义。
- 后续 `add-client-personal-world-vertical-slice` 必须通过本路由注册和打开业务页面，不能建立第二套导航、输入协调或页面状态 owner。

## 完成与回滚

- 完成条件为 tasks 全部勾选、delta spec 同步、OpenSpec strict 验证通过、Unity EditMode/PlayMode 全量测试通过、Windows Development Player 正常启动并留下无 UI 未观察异常的 `Player.log`。
- 回滚时同时移除 BootstrapScene 的 Host root 接线、`Presentation` 实现和 Composition 接入；本 change 不产生协议、存储或持久化数据迁移。
