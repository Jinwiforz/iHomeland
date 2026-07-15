## Why

公开 HTTPS bootstrap、WSS control、生产 Session/WorldAdmission 运行时和冻结 world/visit 协议已经具备，但服务端仍没有承载权威 PersonalWorld/VisitSession 请求、响应与 safe-return 的 TLS/TCP 业务通道。现在需要补齐 N0 的最后一个 transport capability，使一次性 gameplay ticket 与 world admission 真正落到有界、可关闭、可验证的连接边界，同时继续把完整业务竖切留给下一条 change。

## What Changes

- 在独立 TLS 1.3 listener 上建立固定 4-byte unsigned big-endian framing 的可靠 gameplay 连接；local/test 明文只允许 loopback，不与 HTTPS/WSS 或 diagnostic listener 混用。
- 以 `Authorization` 之外的冻结 TCP 握手帧提交一次性 `TLS_TCP` ConnectionTicket，在任何业务 frame 前原子消费并构造只读 `GAMEPLAY` AuthContext；错误 endpoint/channel/epoch、过期、重放和部分握手全部 fail closed。
- 实现基于 message/route registry 的 closed-schema codec 与 dispatcher，只接受已登记 TLS/TCP world/visit REQUEST/COMMAND，验证 kind、direction、scope、size、request/command correlation、rate、timeout 和 idempotency 后才调用窄 application port。
- 在连接内原子消费 world admission，将 qualification 绑定到当前 connection、target、role、purpose 与完整 assignment；没有有效 admission 的 gameplay connection 不能读取 snapshot 或执行 world/visit command。
- 建立每连接单 reader、单 serialized writer、有界读缓冲/批量 dispatch/发送队列/内存预算、pending correlation、sequence、deadline、TCP keepalive/idle、背压与稳定 close reason，并支持可信 response/push 投递。
- 将 TCP registry、session epoch invalidation、draining、并行有界关闭、listener 回滚与 Redis/MySQL 生命周期接入唯一 Composition Root，不创建第二套 SessionStore、WorldAdmissionStore 或业务事实缓存。
- 增加 codec/framing、dispatcher、admission、重放、并发、slow consumer、connection storm、TLS、真实 Redis、race、fuzz、shutdown 与协议 fixture 验收；不在本 change 制造完整 own-world/visit-world producer 或 Go 业务协议客户端。

## Capabilities

### New Capabilities

- `server-tcp-gameplay`: 定义服务端 TLS/TCP gameplay listener、ticket/admission 握手、可靠 framing/codec/dispatcher、连接资源模型、可信投递、生命周期、安全观测与分层验收边界。

### Modified Capabilities

无。

## Impact

- 服务端新增 `internal/transport/tcpgameplay` 及其 framing、codec、registry、dispatcher、connection lifecycle 和 adapter tests，并扩展唯一 Composition Root、配置、metrics 与 storage integration harness。
- 复用 `internal/session`、`internal/worldadmission`、placement/PersonalWorld/VisitSession 窄接口、现有 Protobuf generated projection、message/route/error registries、TLS 配置和共享 Redis/MySQL runtime；不得复制 schema、DTO、credential 或 store owner。
- 新增独立 gameplay TCP 端口和受信 advertised endpoint 配置；production 证书、TLS 1.3、Host/endpoint identity、连接/内存/速率预算与关闭 deadline 成为启动时硬校验。
- 更新网络架构、端口分配、协议兼容、文件结构、路线图、服务端运行说明和长期 OpenSpec；本 change 完成只表示 TLS/TCP transport 可用，完整 PersonalWorld/VisitSession 端到端仍由 `complete-server-personal-world-slice` 交付。
