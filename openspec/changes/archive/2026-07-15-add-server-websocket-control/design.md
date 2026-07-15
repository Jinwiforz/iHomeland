## Context

`add-server-http-bootstrap` 已经在唯一 Composition Root 中构造 production Account、Session、PersonalWorld、Placement、VisitSession 与 WorldAdmission graph，并由一个独立于 diagnostic 的公开 HTTP/TLS listener 提供 10 个冻结 operation。HTTPS 可以签发绑定受信 WSS endpoint、`CONTROL` scope、session epoch 与 16-byte nonce 的一次性 ConnectionTicket；production Redis SessionStore 已提供原子 `ConsumeTicket`，但当前 graph 仍注入 `NoActiveRealtimeConnections`，且没有 WSS upgrade path、connection registry 或 push writer。

跨端 Protobuf 与 registries 已冻结 9 个 WSS server-to-client push：500-504、2003、2100-2102。WSS 不存在 client-to-server application route；world/visit command、snapshot 与 safe-return 均属于后续 TLS/TCP。公开 listener 在 production 只允许 TLS 1.3，本地明文只允许 loopback。HTTP 与 WSS 需要共享该安全入口，同时 `http.Server.Shutdown` 不会自动等待已经 hijack 的 WebSocket，因此连接生命周期必须由 WSS owner 显式管理。

## Goals / Non-Goals

**Goals:**

- 交付可由现有 HTTPS WSS ticket 建立的 production control connection。
- 精确编码并投递 registry 已登记的 WSS push，保持 route、payload、envelope、sequence 和大小限制一致。
- 建立有界 connection registry、serialized writer、背压、心跳、关闭、失效通知和低敏观测。
- 将 Session `ConnectionInvalidator` 替换为真实 WSS invalidator，并保持 epoch 先提交、连接后通知/关闭。
- 在同一公开 listener 和总 shutdown deadline 下完成启动回滚与 graceful shutdown。

**Non-Goals:**

- 不新增、重编号或改变 Protobuf message、error code、route channel 或 fixture 语义。
- 不接收 client application message，不实现 request/command dispatcher、world mutation、safe-return 或 world admission consume。
- 不实现 TLS/TCP、Unity client、跨进程 pub/sub、管理后台、presence 持久化或 Redis connection registry。
- 不因当前缺少业务 producer 而增加全局 event bus、空 manager 或伪造 maintenance/queue/world/visit 状态。

## Decisions

### 1. WSS 通过顶层 mux 复用公开 HTTP/TLS listener

公开 listener 前增加一个职责单一的顶层 mux：仅将精确 `GET /v1/control` upgrade path 交给 WSS handler，其余请求原样委托既有 HTTP router。HTTP router 仍且只能实现 10 个冻结 OpenAPI operation，diagnostic router 不注册或转发 WSS path。WSS 沿用当前 public bind、TLS 1.3、header limit、readiness 和 lifecycle owner；握手必须协商唯一 subprotocol `ihomeland.control.v1`。ConnectionTicket 通过 `Authorization: Ticket <32 位小写十六进制 nonce>` 提交，禁止 query、cookie、payload 或自定义 actor/session 字段携带凭据。

选择 header 而不是 query 是为了避免 ticket 进入 URL、访问日志、代理统计或浏览历史；选择固定 subprotocol 是为了在 upgrade 前拒绝错误代际。Unity PC 客户端可以设置 Authorization header，因此本阶段不为浏览器兼容性降低凭据边界。Origin 可以缺失以支持 native client；一旦存在，必须精确匹配启动时验证的 allowlist。Host 同样按受信部署配置验证，不能由请求决定 ticket endpoint。

公开HTTP component继续拥有唯一 `net.Listener` 与 `http.Server`。WSS transport拥有upgrade handler和hijacked connections，不创建第二listener。公开advertised WSS endpoint仍是部署事实，可以与进程bind地址因ingress/port mapping不同；ticket consume使用签发时同一个受信EndpointProvider值，绝不使用不受信Host或remote address重建endpoint。Upgrade前失败复用现有安全HTTP error mapper与 `ErrorResponse`，只允许 `VALIDATION_FAILED`/400、`AUTH_UNAUTHENTICATED`/401、`AUTH_FORBIDDEN`/403、`RATE_LIMITED`/429或 `DEPENDENCY_UNAVAILABLE`/503，不创建WSS同义错误或泄漏ticket细节。

### 2. 原子消费 ticket 后才建立 AuthContext 与索引

Handler 先完成 method/path、readiness、TLS/local-mode、Host、Origin、subprotocol、header grammar、全局/remote reservation 与 pre-auth rate 检查，再解析 nonce 并调用 Session service 的 `ConsumeTicket`，固定传入 `ChannelWSS` 和受信 advertised endpoint。Store 返回 current session/epoch、WSS channel、精确 CONTROL scope 且未消费/未过期后，handler 再以只读 AuthContext 在同一 reservation 中绑定 SessionID/PlayerID 预算；四类预算全部成立后才接受 upgrade。

Ticket 在 consume 成功后遇到 upgrade 或网络失败仍保持已消费；客户端必须通过 HTTPS 获取新 ticket，服务端不得恢复 Redis record或自动重放。这样避免同一 nonce 在两个竞态连接上获得资格。认证失败只返回有界 HTTP 非成功结果，不泄漏 ticket 是否存在、过期、重放或绑定哪个私有字段。

### 3. Connection registry 只拥有连接引用

新增 WSS control registry，以 CSPRNG ConnectionID 为主键，并建立 SessionID、PlayerID 的反向索引。Entry 只保存只读 AuthContext 摘要、连接句柄、发送队列和生命周期状态；PersonalWorld、VisitSession、assignment、presence 或业务最终事实不进入 registry。全局、每 remote identity、每 SessionID 和每 PlayerID 的连接数均有启动时验证的硬上限；超限在 upgrade 前拒绝，不通过替换旧连接制造隐式业务策略。

Registry 提供窄、同步安全的查找/投递/关闭能力。业务 owner 后续通过自己定义的 notifier 端口调用 typed WSS publisher；transport 不引入全局 event bus。当前 change 真实接线 Session invalidation，其他已登记 push 由 transport contract/integration tests 验证可投递，但不创建虚假业务 producer。

### 4. 只允许 binary PUSH envelope

Trusted publisher 必须提交已登记的 message ID、精确 generated payload 类型和明确 target。Codec 通过 runtime Catalog 验证消息属于 WSS、`CONTROL`、`SERVER_TO_CLIENT`、`PUSH`，确定性编码 payload，再生成 protocol version 1 的 `ReliableEnvelope`。每连接 sequence 从 1 单调递增；timestamp 从共享 server Clock 读取。编码后同时执行 route `maxSize` 与全局 realtime frame limit，禁止 text frame、JSON 同义 DTO、unknown message、错误 channel、压缩和拆分 application message。

Client 没有已登记的 WSS application route。Reader loop 只维持 WebSocket control frame、peer close 与 liveness；收到 binary/text application data 即以 protocol/policy close 结束连接，不尝试按 TLS/TCP route dispatch。

### 5. 每连接单 reader、单 serialized writer和双重队列预算

每条连接只有一个 read loop 和一个 write loop。Publisher 不直接写 socket，而是向有界队列提交 immutable encoded envelope；队列同时限制 item 数与累计 bytes，避免大量小包或少量大包绕过预算。Writer 按 sequence 顺序写 binary message，并为每次 write 使用独立 deadline。队列满、write timeout 或 peer 长期不响应时不丢弃/重排 control push，而是关闭 slow consumer 并解除全部索引。

Server 定期发起 WebSocket ping，并要求在 pong deadline 内完成匹配响应。`idleTimeout` 是“自上次成功 write 或 ping 起”的连接级兜底预算；每次 I/O deadline 都会收紧到剩余 idle 预算，避免阻塞的 write/ping 绕过连接上限。`permessage-deflate` 默认禁用，防止压缩状态和解压放大扩大连接预算。实现选择 `github.com/coder/websocket` 并由 `versions.yaml` 锁定版本：它提供原生 `context.Context`、明确 close/ping API、并发安全写支持且没有传递依赖；项目仍坚持单 writer 以集中 sequence、队列和关闭所有权。连接级 graceful shutdown 与 registry 等待由本项目实现，不依赖库提供全局管理器。

### 6. Session invalidation 是提交后的最终通知与强制关闭

WSS registry 实现 Session `ConnectionInvalidator`。当 Session service 已原子提交新 epoch 后，invalidator 查找旧 epoch 的全部连接，尝试在单独的短 deadline 内投递 `CONTROL_SESSION_INVALIDATED_PUSH` 或 `CONTROL_FORCED_LOGOUT_PUSH`，随后无条件关闭并移除连接。若队列已满、连接已断或通知失败，关闭仍必须发生；返回的可诊断错误不能回滚 epoch、恢复 token/ticket 或保留旧连接。

同一 invalidation 重试必须幂等：已经关闭的连接视为完成，仍存在且 epoch 小于新值的连接再次被关闭，epoch 不小于通知值的新连接不受影响。普通 logout 使用 session-invalidated 语义；forced logout/ban 仅在上游已经提供对应安全 reason 时使用 forced-logout payload，transport 不自行判定封禁。

### 7. 共享 listener 的关闭顺序显式覆盖 hijacked connections

Root 仍先将共享 readiness 切到 draining，使新 HTTP operation和 WSS upgrade fail closed。`publicRuntimeComponent.Stop` 随后先停止 WSS 接受/投递、对 active connections 发送 going-away并在子 deadline 内等待 reader/writer退出，再调用 `http.Server.Shutdown` 等待普通 HTTP in-flight request，最后由外层 lifecycle 关闭 Redis、MySQL 和 diagnostic。WSS stop 超时会强制 CloseNow，并把错误纳入总 shutdown结果。

Start 过程中 WSS codec/registry/handler 在 listener bind 前构造；任一配置、catalog 或依赖错误都不产生网络副作用。Listener bind 或受监督 serve task 失败时，已构造 WSS owner 被同步释放。专属 `websocket_control` TaskOwner 监督 registry 生命周期，registry 自己以 `WaitGroup` 拥有并等待每连接唯一 reader/writer；单连接异常只关闭该连接，owner 级失败才进入进程受控关闭。

### 8. 观测只使用稳定低敏维度

Metrics 覆盖 handshake outcome、active/accepted/rejected connections、push outcome、encoded bytes、queue depth bucket、slow-consumer/heartbeat/close reason和 invalidation结果，label只使用 operation、message ID、status class和稳定 reason。结构化日志可以包含随机 ConnectionID、message ID、SessionID/PlayerID摘要与稳定 reason，但不得记录 Authorization、ticket nonce/digest、完整 principal、IP、Origin、payload、VisitSession invite或 backend error文本。Remote identity只用于进程内限流key，并在 idle后有界回收。

## Risks / Trade-offs

- [同 listener 下 `http.Server.Shutdown` 不管理 hijacked connection] → readiness先drain，WSS registry先关闭并等待全部连接，再关闭HTTP server和storage；超时强制关闭。
- [Ticket 已消费但 upgrade 失败] → 保持一次性安全语义，客户端重新走HTTPS签发；不设计危险的补偿恢复。
- [进程内 registry 无法跨节点广播] → 本 change 仅支持当前单进程 graph；跨节点 fan-out 必须有独立部署/ownership证据后另提 change。
- [慢消费者导致控制通知未送达] → 不丢弃或重排；连接被关闭，客户端通过重连和权威 bootstrap恢复，session invalidation即使通知失败仍强制断开。
- [业务 push 暂无 production producer] → 只交付经过contract测试的typed publisher和真实Session invalidator，不制造管理入口或虚假领域事件；后续TLS/TCP竖切从业务结果显式调用窄 notifier。
- [共享公开路由扩大攻击面] → 固定path/subprotocol、严格Host/Origin/header、pre-auth限流、全局连接预算、禁压缩和只出不进的application方向。

## Migration Plan

1. 在依赖目录锁定 WebSocket library，增加 WSS 配置与跨字段校验，但保持默认仅loopback开发可明文。
2. 实现无 listener 的 catalog codec、registry、writer/reader和 invalidator测试。
3. 用顶层public mux将 `/v1/control` handler与既有10-operation HTTP router组合，使用同一个SessionStore、EndpointProvider、TLS和readiness，且不改变HTTP router的封闭operation table。
4. 用真实Redis ticket和临时TLS完成握手、重放、epoch、背压、连接风暴与关闭集成测试，再替换 `NoActiveRealtimeConnections`。
5. 更新架构、网络、配置、文件结构与验收文档；通过 strict OpenSpec、contract/proto、unit/integration/race和process测试后归档。

回滚时移除 WSS route与真实 invalidator接线，恢复阶段性 no-active-connections adapter；HTTP 10个operation、既有schema和Redis数据无需迁移。已经签发的WSS ticket会按短TTL自然失效。

## Open Questions

无。WSS path、subprotocol、ticket header、消息方向、共享 listener、连接预算和关闭顺序均在本 change 中冻结；跨节点广播与业务 producer 属于后续独立边界。
