## Context

`add-client-http-bootstrap` 已提供不可变 endpoint/config、唯一 `SessionCoordinator`、WSS ticket 的签发与单次交付，以及 App Scope 生命周期。共享协议已冻结 WSS 的 9 类 PUSH、`ReliableEnvelope`、route size 与服务端 `/v1/control` 握手语义，但客户端尚无实时控制通道。

本 change 横跨 HTTP ticket、generated Protobuf、后台网络 I/O、Unity 主线程和 AppLifetime。安全边界要求客户端不能向 WSS 发送 application frame，旧连接也不能在新 session 建立后清除新 lineage。

## Goals / Non-Goals

**Goals:**

- 建立显式启动、只接收 binary PUSH 的 WSS control 通道。
- 严格验证 handshake、frame、envelope、route、payload type、大小与连接内 sequence。
- 把普通 PUSH 有界投递到 Unity 主线程，并让 session 失效先在唯一 owner 上 fail closed。
- 对可恢复网络中断执行有限重连，每次重新签发并单次交付 ticket。
- 让连接、receive pump、重连等待和回写全部服从 App Scope 停止与 rollback。

**Non-Goals:**

- 不实现客户端 application frame、request/response、writer queue 或 WSS command dispatcher。
- 不实现 TLS/TCP、world admission、PersonalWorld/VisitSession 投影 owner、UI、Scene 或 Prefab。
- 不实现进程重启后的 token/ticket 恢复，也不把断线等同于 session 失效。
- 不修改共享协议、registry、fixtures 或服务端。

## Decisions

### 使用 `ClientControlChannel` 统一拥有状态机

Application 层的 `ClientControlChannel` 负责显式 `StartAsync`、ticket 获取、连接代际、receive pump、有限重连、停止和安全状态快照。Infrastructure 层只提供窄 `IClientWebSocket`/factory 与 `ClientControlCodec`，不保存 session 或业务状态。

选择单 owner 而不是 event bus、多个 manager 或 service locator，可使“只有一个 active control connection”“旧 completion 不能覆盖新代际”在同一锁与 cancellation 边界内成立。

### 不创建 application writer

服务端契约规定客户端发送任意 text/binary application frame都会被 policy close。运行时 adapter 因此不公开 `SendAsync`，只在停止时执行协议关闭；WebSocket 控制帧交给平台实现。相比预先建立 serialized writer/queue，这一设计同时减少错误入口和无用复杂度。

### Ticket 在每次尝试内签发并立即单次移交

每个连接尝试调用 `SessionCoordinator.IssueConnectionTicketAsync(Wss)`，随后通过 `TryTakeConnectionTicket` 原子取得 credential。URI 只由 ticket 绑定 endpoint 与冻结 `/v1/control` 构造；Production 使用 `wss`，Local/Test 的 `ws` 只允许 loopback。Header 固定为 `Authorization: Ticket ...`，subprotocol 固定为 `ihomeland.control.v1`，credential 不进入对象文本、异常或事件。

任何 upgrade 成功或失败都视为 ticket 已交付；下一次尝试必须重新签发。不会为了重连缓存 lease、ticket 或自行改用 bootstrap 中其他 endpoint。

### Codec 使用封闭 route table 与 generated parser

`ClientControlCodec` 只登记 500-504、2003、2100-2102，并为每个 ID 固定 generated `MessageParser` 与 route max size。它先解析 `ReliableEnvelope`，再验证 protocol version 1、`PUSH`、无 request/command correlation、正数 timestamp、完整 frame/route 上限以及从 1 严格连续的 sequence，最后解析精确 payload。

未知 ID、错误 kind、关联字段、缺口/重复 sequence、malformed payload 或超限均为 terminal protocol failure；不提供 JSON、反射扫描或 unknown-message fallback。

### 主线程分发与安全失效分开处理

普通 PUSH 以不可变 `ClientControlPush` 通过既有 `MainThreadDispatcher.TryPost` 投递；队列已满时关闭连接，不能静默丢弃后继续伪装 sequence 连续。排队 callback 在主线程执行前再次核对 run generation，使 App Scope 停止或后发显式运行可以拒绝旧连接的迟到通知。

501/504 携带的 session epoch 在 receive pump 上先调用 `SessionCoordinator.TryInvalidateFromControl(sourceGeneration, newEpoch)`。该方法同时验证来源本地 generation 与更高服务端 epoch，只能清除对应旧 lineage；随后通道进入 terminal `SessionInvalidated` 并停止重连。通知仍尝试投递主线程，但 dispatcher 拒绝不能恢复 session。

### 有限重连只覆盖瞬时连接故障

显式启动后，connect/receive 的瞬时 transport failure 或无失效语义的 peer close 可以按固定、可取消且有上限的 backoff 重试。协议错误、session 失效、本地配置错误、停止或 ticket/auth policy failure 不自动重试。

重连期间保持可观察 `Recovering` 状态；达到上限后进入 `Disconnected`，不会无限后台循环。普通 WSS 中断不清除 HTTP session，后续可以再次显式启动。

### AppLifetime 初始化不产生网络副作用

`InitializeAsync` 只启用 owner；连接仍需上层显式调用。参与者顺序使 control channel 在 Session/HTTP 之后初始化，并在逆序停止时先取消重连与 receive pump、关闭 socket，再释放 Session 与 HTTP。所有后台 completion 都以连接代际和 lifetime cancellation 拒绝迟到提交。

## Risks / Trade-offs

- [不同 Unity/.NET 平台的 `ClientWebSocket` 行为存在差异] → runtime adapter 只使用基础 connect/receive/close API，核心状态机和 codec 通过 fake socket 独立验收，Windows Development build 作为平台门禁。
- [主线程队列短时拥塞会关闭 control 通道] → 选择 fail closed 保持 PUSH sequence 语义；后续容量调整必须依据观测而非静默丢包。
- [有限重连可能在短故障后仍耗尽] → 保留显式再次启动入口，不引入无限循环或隐藏 token 恢复。
- [route table 与共享 registry 可能漂移] → EditMode 测试读取 tracked registry/fixtures，验证 ID、类型和 max size 一致；协议变化必须经独立 change 更新两端。

## Migration Plan

1. 新增 codec、socket adapter 与 control owner，不改变现有 HTTP 公开行为。
2. 扩展 Session owner 的 generation-safe control invalidation，并接入 Composition/AppLifetime。
3. 运行静态编译、OpenSpec strict、协议 verify；Unity 导入生成 `.meta` 后运行 EditMode、PlayMode 与 Windows Development build。
4. 回滚时移除 control owner 接线与新增文件，HTTP bootstrap 仍可独立工作。

## Open Questions

无。TLS/TCP 与业务 Services 的恢复协同留给各自 change。
