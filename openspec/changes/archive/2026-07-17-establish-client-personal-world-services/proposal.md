## Why

客户端已经具备强类型 HTTPS、WSS control 与 TLS/TCP gameplay 通道，但仍没有应用层 owner 将这些通道返回的 PersonalWorld、WorldInstance 与 VisitSession 权威事实整合为稳定状态。现在进入 C2，需要先建立与 UI、Scene 解耦的纯 C# Services 和访问状态机，避免后续竖切把 revision、admission、角色或安全返回逻辑散落到页面与场景对象中。

## What Changes

- 新增 `PersonalWorldService`，保存当前玩家 primary PersonalWorld identity、owner 与最高 world revision 的不可变投影，并统一处理 response/PUSH 的低 revision、同 revision 重放与冲突。
- 新增 `VisitSessionService`，保存定向 invite、当前 membership、Owner/Visitor role、expiry 与最高 visit revision；所有状态只接受服务端权威合同，不从 UI 或场景推断权限。
- 新增 `WorldAdmissionCoordinator`，显式编排 own-world 解析、invite accept、一次性 admission、gameplay connect/join、离开访问与安全返回，并维护 `OwnWorld`、`JoiningVisit`、`Visiting`、`ReturningOwnWorld` 等受控状态。
- 将 WSS control PUSH、TLS/TCP response/PUSH 与 HTTPS bootstrap/admission 接入上述 owner；使用 session generation、target generation、revision 和 cancellation 拒绝陈旧回调，safe-return 到达后立即关闭旧 target mutation gate。
- 把 Services 纳入 `AppComposition` 与 `AppLifetime` 的显式依赖和逆序关闭，保持默认启动零网络副作用，并提供只读快照/通知供后续 UI 与 Scene adapter 消费。
- 增加不依赖真实 listener、Unity Services、Scene、Prefab 或 UI 的 EditMode contract/state-machine tests，并保留既有 PlayMode 与 Windows Development build 验收。
- 本 change 不实现 UI 路由、页面、SceneContext world adapter、Prefab、资源加载、跨进程 token 恢复或完整自动重连；这些仍由后续 change 负责。

## Capabilities

### New Capabilities

- `client-personal-world-services`: 定义客户端 PersonalWorld/VisitSession 权威投影、world admission 编排、访问状态机、陈旧结果隔离、安全返回与 App Scope 生命周期。

### Modified Capabilities

- `client-http-bootstrap`: 将冻结 HTTP operation 从九个扩展为十个，新增强类型 `acceptVisitInvite` 与严格 path、idempotency、expected revision、响应校验和失败语义，供个人世界访问编排使用。

## Impact

- 主要影响 `client/Assets/App/Scripts/Application/World` 中职责分离的 PersonalWorld、VisitSession 与 world flow 纯 C# owner，以及 `Core/Composition` 的显式对象图和生命周期注册。
- 复用并窄幅扩展既有 `SessionCoordinator`、`IClientHttpApi`、`ClientControlChannel`、`ClientGameplayChannel` 与 generated protocol；不新增第三方依赖、不修改服务端 wire contract，也不提交生成代码。
- 测试主要位于 `client/Assets/App/Tests/EditMode`；Unity 序列化资产、Scene 与 Prefab 不在本 change 的实现范围内。
