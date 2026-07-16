# Client WebSocket Control 规格

## Purpose

定义 Unity 客户端只接收 WSS control 通道的握手、封闭消息目录、主线程投递、session 失效、有限恢复与 App Scope 生命周期契约。

## Requirements

### Requirement: Control channel 必须使用显式启动与一次性 WSS ticket

客户端 MUST 只在 App Scope 已初始化、bootstrap configuration Ready 且 `SessionCoordinator` 持有 current authenticated session 后显式启动 control channel。每次连接尝试 MUST 通过 HTTPS 签发新的 `WSS` ticket，并只使用 ticket 绑定 endpoint 构造 `/v1/control`；Production MUST 使用 `wss`，Local/Test 的 `ws` MUST 同时满足 loopback。握手 MUST 固定 `Authorization: Ticket <credential>` 与 subprotocol `ihomeland.control.v1`，不得使用 query、cookie、Bearer、旧 lease 或 fallback endpoint。App Scope 初始化本身 MUST NOT 自动签发 ticket 或建立连接。

#### Scenario: 显式建立 control connection

- **WHEN** current session 为 authenticated、配置 Ready 且 ticket endpoint/scopes 有效
- **THEN** 客户端单次取得 credential，以固定 path/header/subprotocol 建立连接，且 ticket 不进入日志、异常、事件或 `ToString()`

#### Scenario: Upgrade 后连接失败

- **WHEN** ticket 已交给 WebSocket connect，但 upgrade、subprotocol 或网络建立失败
- **THEN** 该 ticket 不得再次使用，任何后续尝试都必须重新通过 HTTPS 签发

#### Scenario: App Scope 只完成初始化

- **WHEN** AppRoot 进入 Running 但没有显式启动 control channel
- **THEN** 客户端不签发 WSS ticket、不创建 socket 且既有离线空场景行为保持不变

### Requirement: Control channel 必须保持只接收的封闭协议边界

客户端 MUST NOT 向 WSS 发送 text 或 binary application frame，也不得公开通用 send API。每个服务端 WebSocket message MUST 是未压缩 binary `ReliableEnvelope`，并同时满足 bootstrap realtime frame 上限与 route max size。Codec MUST 只接受 500-504、2003、2100-2102 的 protocol version 1 `SERVER_TO_CLIENT/PUSH`，拒绝 request/command correlation、非正 timestamp、unknown/wrong-channel ID、错误 generated payload 类型、malformed/oversized frame 与 JSON/text fallback。

#### Scenario: 收到合法 control PUSH

- **WHEN** binary message 是 route 允许大小内、kind 为 PUSH 且 payload 与登记 generated type 精确匹配的 envelope
- **THEN** codec 返回包含 message ID、sequence、timestamp 和强类型 generated payload 的不可变 control push

#### Scenario: 收到 TLS/TCP route 或错误 payload

- **WHEN** WSS frame 使用未登记 ID、TLS/TCP-only ID，或登记 ID 的 payload 无法按精确 generated parser 完整解析
- **THEN** 客户端以 terminal protocol failure 关闭当前连接，不转成 unknown event、不猜测类型且不触发业务状态

#### Scenario: 调用方尝试发送 application message

- **WHEN** 上层需要通过 control channel 提交 command、request、text 或 binary payload
- **THEN** 编译期 API 不提供该入口，业务必须等待其唯一 allowed channel capability

### Requirement: Receive pump 必须有界并验证连接内连续 sequence

每个 active control connection MUST 只有一个 receive pump。它 MUST 处理 fragmented WebSocket message，但在分配与拼接期间执行 realtime frame 硬上限，并要求每条完整 envelope 的 sequence 从 1 开始严格连续递增。重复、回退、缺口、非 binary message、异常 fragmentation 或超限 MUST 关闭当前连接；不得静默丢弃后继续接受更高 sequence。平台 heartbeat/peer close/cancellation MUST 有界结束 receive，不得遗留并行 reader 或后台任务。

#### Scenario: WebSocket message 被分片读取

- **WHEN** 一个合法 binary envelope 经多次 receive 才到达 EndOfMessage
- **THEN** receive pump 在同一有界 buffer 中完成重组并只解码、投递一次

#### Scenario: Sequence 出现缺口

- **WHEN** 当前连接已接受 sequence 1，下一条 envelope 的 sequence 为 3
- **THEN** 客户端报告稳定 protocol close、停止该 pump 且不投递 sequence 3

#### Scenario: Frame 超过公开上限

- **WHEN** 累计 fragment bytes 超过 bootstrap realtime frame 上限
- **THEN** 客户端停止读取并关闭连接，不继续扩容、不截断解码且不记录原始 payload

### Requirement: Control PUSH 必须有界投递主线程并安全联动 session

普通 control push MUST 通过既有有界 `MainThreadDispatcher` 投递，网络 completion 不得直接更新 Unity object、Scene 或 view。Dispatcher 容量不足或生命周期停止 MUST 终止当前连接，而不是静默丢弃消息。`CONTROL_FORCED_LOGOUT_PUSH` 与 `CONTROL_SESSION_INVALIDATED_PUSH` MUST 先由唯一 `SessionCoordinator` 以来源 local generation 和更高 server epoch 原子清除旧 session lineage，再停止 control 重连并尝试投递通知；旧连接迟到 push MUST NOT 清除后发登录建立的新 session。

#### Scenario: 后台收到普通 PUSH

- **WHEN** receive pump 在线程池 completion 中解码合法 maintenance、queue、endpoint、assignment 或 visit PUSH
- **THEN** subscriber 只在捕获的 Unity 主线程 drain 时观察强类型消息，网络线程不直接调用 view

#### Scenario: Dispatcher 队列已满

- **WHEN** control push 无法转移到主线程队列
- **THEN** 通道关闭并产生稳定 backpressure reason，不丢弃该 sequence 后继续接收

#### Scenario: 旧连接迟到 session invalidation

- **WHEN** 玩家已重新登录并产生更高 local generation，旧 WSS 才收到其来源 generation 的 invalidation push
- **THEN** `SessionCoordinator` 拒绝清除新 snapshot，旧 control connection 仍终止且不自动重连

### Requirement: Control 恢复必须有限且区分连接故障与授权失效

在一次显式启动的生命周期内，客户端 MAY 只对瞬时 connect/receive transport failure 或没有 session 失效语义的 peer close 执行固定上限、可取消的 backoff 重连；每次尝试 MUST 重新签发 ticket 并重置该连接 sequence。协议错误、session invalidation/forced logout、本地安全配置错误、App Scope 停止与不可恢复 ticket/auth failure MUST NOT 自动重连。重试耗尽后 MUST 进入稳定 Disconnected，普通 WSS 中断 MUST NOT 擅自清除仍 current 的 HTTP session。

#### Scenario: 瞬时网络中断后恢复

- **WHEN** active connection 因瞬时 transport failure 结束且重试预算仍存在
- **THEN** 通道进入 Recovering、等待可取消 backoff、使用新 ticket 建立新连接并从 sequence 1 验证

#### Scenario: 重试预算耗尽

- **WHEN** 连续可恢复故障达到固定尝试上限
- **THEN** 通道停止后台重试并进入 Disconnected，session 保持 current 且允许后续再次显式启动

#### Scenario: 收到 forced logout

- **WHEN** 501 PUSH 使当前 session lineage 失效
- **THEN** 客户端清除该 lineage、关闭 WSS 并进入 SessionInvalidated，不再申请 ticket 或自动重连

### Requirement: WSS capability 必须服从 App Scope 生命周期并可独立验收

`AppComposition` MUST 显式创建 socket factory、codec 与 `ClientControlChannel`，并在 Session/HTTP 后初始化、在它们之前停止 control channel。停止与启动失败回滚 MUST 取消 connect、receive 和 backoff，执行有界 socket close/dispose，并让迟到 completion 无法提交状态或主线程 callback。实现 MUST 以可控 socket、ticket source、clock/delay 和 tracked registry/fixtures 验证握手、9 类 payload、fragment、size、sequence、主线程、失效、重连与关闭，同时继续通过协议 generation/parity、既有 PlayMode 和 Windows Development build。

#### Scenario: App Scope 在 receive 期间停止

- **WHEN** shutdown 或 rollback 发生且 socket 正在 connect、receive 或等待 backoff
- **THEN** 所有操作被取消并有界结束，control resource 只释放一次，Session 与 HTTP 随后按既有逆序规则停止

#### Scenario: 执行 control contract tests

- **WHEN** EditMode tests 用 tracked registry/golden 与 fake socket 覆盖全部允许 route 和负向输入
- **THEN** 客户端目录、generated parser、max size、envelope 规则和关闭分类与冻结契约一致，测试不依赖真实账号、Unity Services、UI 或 TLS/TCP
