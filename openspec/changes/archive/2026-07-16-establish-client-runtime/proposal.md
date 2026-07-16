## Why

服务端 v1 已通过资格验收，C0 的进入条件已经满足；当前 Unity 工程仍只有编辑器基线和模板资产，尚未形成可重复验收的唯一应用入口、对象组合、作用域所有权与失败清理机制。必须先建立这层运行基础，后续协议生成、网络通道、业务 Service 和 UI 才能在稳定生命周期边界上逐项接入。

## What Changes

- 固定并验证 Unity 6000.5.2f1、PC/URP、Windows Company Name/Product Name、Unity Services 断开状态、Input System、测试框架、构建场景与必要 Package 的最小客户端基线。
- 建立 `AppBootstrap -> AppComposition -> AppRoot` 唯一入口，使用显式构造与窄接口连接对象，不引入第三方 DI、静态 service locator 或空 manager。
- 建立 App Scope 生命周期状态、顺序初始化、失败逆序回滚、幂等逆序关闭、关闭 deadline 与显式 tick 登记。
- 建立轻量 Scene Scope generation/cancellation/cleanup 基础，使场景卸载后迟到回调不能写回旧场景对象，同时不预建没有实际职责的业务 SceneContext。
- 建立必要的 Unity Host 边界与纯 C# 生命周期实现，保证业务无关逻辑可在不创建 GameObject 的 EditMode 测试中验证。
- 增加 EditMode 与 PlayMode 验收，覆盖重复 bootstrap、部分初始化失败、逆序清理、重复停止、场景代际失效、主线程约束与退出清理。
- 本 change 不引入 Addressables、`Resources` 核心资源路径、第三方异步库、网络协议生成、HTTP/WSS/TCP、账号/个人世界业务、正式 UI 页面或内容 Prefab；BootstrapScene 只使用直接序列化引用和当前构建内资产。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `client-runtime`：把既有客户端架构约束具体化为可运行、可回滚、可关闭并可由 EditMode/PlayMode 自动验收的 C0 生命周期基线。

## Impact

- 上游 `qualify-server-v1` 已归档，冻结 schema、registry、fixtures、endpoint 与资格结果是本 change 的只读输入；客户端 Core runtime 是本 change owner，AppLifetime 与 SceneLifetimeOwner 分别拥有 App Scope 清理顺序和 Scene Scope generation。
- 影响 `client/Assets/App/` 下的运行时代码、测试与最小 BootstrapScene 接线，以及必要的 `client/Packages/`、`client/ProjectSettings/` 和客户端说明入口。
- Unity Editor 内的场景、组件、Inspector、Build Profiles 与序列化资产操作在 apply 阶段完成，正常 `.meta` 由 Unity 生成；仓库代码实现不伪造 `.meta` 或改写 Unity 序列化标识。
- 不修改服务端、公开协议、消息路由、存储、安全语义或服务端 v1 资格基线，不新增运行时第三方依赖。
- 完成门槛是客户端基线、EditMode/PlayMode、Windows Development build、OpenSpec change/all strict、文本质量和 Git 边界全部通过。
- 回滚边界是移除本 change 新增的 C0 代码、测试和 BootstrapScene 接线，恢复到已创建但尚无应用运行时的 Unity 工程基线。
