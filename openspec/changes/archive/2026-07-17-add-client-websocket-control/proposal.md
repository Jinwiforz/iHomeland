## Why

客户端已经具备冻结协议生成与 HTTPS 会话/ticket 能力，但还无法接收服务端维护、强制下线、endpoint、assignment 和 visit 等控制通知。现在需要补齐独立 WSS control 通道，才能在不混入 TLS/TCP gameplay、个人世界业务状态或 UI 的前提下推进客户端网络主线。

## What Changes

- 新增只接收服务端 binary `ReliableEnvelope` 的 WSS control adapter，并固定 `/v1/control`、`ihomeland.control.v1` 与 Ticket Authorization 握手契约。
- 新增严格的控制消息目录与 generated Protobuf 解码，只允许已登记的 9 类 `SERVER_TO_CLIENT/PUSH`，拒绝错误协议版本、消息类型、关联字段、大小和 sequence。
- 新增主线程 push 分发、连接状态快照、heartbeat/close reason 与有界重连；每次连接尝试都通过 HTTPS 申请新的 WSS ticket，不缓存或重放已交付 ticket。
- 将 forced logout/session invalidation 与唯一 `SessionCoordinator` 联动，先失效本地 session，再终止 control 通道；普通 WSS 中断不得擅自清除仍有效的 HTTP session。
- 将 WSS 资源接入既有 App Scope 生命周期，并以可控 socket/ticket source/clock 覆盖握手、解码、序列、重连、失效、取消和关闭测试。
- 明确不实现客户端 application frame、TLS/TCP gameplay、PersonalWorld/VisitSession 状态 owner、自动登录、UI、Scene 或 Prefab。

## Capabilities

### New Capabilities

- `client-websocket-control`: 定义 Unity 客户端 WSS control 的握手、只读消息边界、主线程投递、session 失效、恢复与生命周期行为。

### Modified Capabilities

- 无。

## Impact

- 影响客户端 `Application/Session`、`Infrastructure/WebSocket`、Composition Root、AppLifetime 与 EditMode 测试。
- 复用 `client-http-bootstrap` 的 `SessionCoordinator`、connection ticket operation 与不可变 bootstrap endpoint，不修改共享 Protobuf、OpenAPI、route registry 或服务端实现。
- 不新增第三方依赖，运行时使用 Unity 当前 .NET profile 提供的 WebSocket 与既有生成协议程序集。
