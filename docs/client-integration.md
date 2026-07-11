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
- `tools/proto/buf.gen.csharp.yaml`：已通过临时生成验证的 C# template。
- `shared/contracts/registry/`：消息、错误与路由源；运行时投影在内存构建。
- `shared/contracts/fixtures/`：HTTPS cases、realtime golden packets 与 negative coverage manifest。
- `versions.yaml`：Unity、协议与生成插件版本基线。

## 跨端生成

- 实时消息唯一协议源是 `shared/proto/`，HTTPS 唯一协议源是 `shared/contracts/http/v1/openapi.yaml`。
- Go/C# 使用 `versions.yaml` 锁定的 compiler/runtime/plugins。
- C# generated code 输出到已忽略的 `client/Assets/App/Generated/`，不得成为 Scene、Prefab 或 ScriptableObject 的序列化引用。
- 禁止手工修改 generated code。
- C# 必须通过 Go golden packets 验证 encode/decode。
- CI 从无 generated code 的检出状态开始，先生成再编译，并检查重复生成结果一致。
- 客户端协议 change 必须先运行 C# generation 和 golden parity tests，不得手写或复制 generated types。

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

### 3. WSS Control

- ticket binding
- maintenance/kick/endpoint update
- heartbeat/close reason
- session invalidation
- main-thread dispatch

### 4. TLS/TCP Business

- length framing
- unique receive pump and serialized writer
- pending request registry
- push dispatcher
- route/error mapping
- reconnect and backpressure

### 5. Account/PersonalWorld/VisitSession Services

- account state from HTTPS results
- world/visit snapshot with monotonic revision
- semantic commands
- no transport type in UI

### 6. UI Vertical Slice

- login
- home/shell
- own-world loading and state
- visit invite/list/detail and member state
- join/leave/kick/reconnect/safe-return commands
- server push and errors

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
  -> restore/refresh access token
  -> acquire WSS/TCP tickets
  -> connect control/business channels
  -> resolve own-world or active visit context
  -> open target screen
```

WSS 与 TCP 独立重连，但共享 session epoch。epoch 失效时必须停止业务、清理本地 session、关闭全部通道并回到登录流程。

## 世界与访问快照

- PersonalWorld/VisitSession Service 分别保存各自最高 revision。
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
  -> UI and interaction presentation
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
- token restore 与 refresh
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
- Visiting 返回 OwnWorld 后无旧场景订阅或 callback 回写

客户端验收必须与服务端 Go test client 对同一 contract fixtures 得出一致业务结果。
