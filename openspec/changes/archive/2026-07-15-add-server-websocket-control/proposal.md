## Why

公开 HTTPS 已能签发绑定 WSS endpoint 与 `CONTROL` scope 的一次性 ConnectionTicket，但服务端尚无可消费该 ticket 的 WSS listener，控制通知仍无法送达客户端。现在需要在 TLS/TCP gameplay 之前独立交付受控、可背压、可失效的带外控制面，继续保持控制通知与 world mutation 的通道边界。

## What Changes

- 在唯一 Composition Root 中接线 production WSS control component；复用公开 HTTPS 的 HTTP/TLS listener，并保持 diagnostic listener 与业务路由隔离。
- 使用 SessionStore 原子消费 WSS ConnectionTicket，构造只读 `CONTROL` AuthContext，并按 session、player 与 connection 建立有界连接索引。
- 只发送 registry 已登记到 WSS 的 control/world/visit push：maintenance、forced logout、queue status、endpoint update、session invalidated、world assignment changed、visit invite、Owner availability 与 VisitSession closed notice。
- 建立单 reader、单 serialized writer、有界发送队列、消息与连接预算、slow-consumer 关闭、心跳/deadline、Origin/Host/TLS 校验和稳定关闭原因。
- 将 Session `ConnectionInvalidator` 接到当前 WSS registry，使 logout/forced logout 在权威 epoch 提交后通知并关闭旧连接；通知失败不恢复旧 session。
- 增加无 listener 单元/契约测试、真实 TLS WSS 与 Redis ticket consume 集成测试，以及并发连接、ticket replay、epoch 失效、慢消费者、连接风暴、启动回滚和 graceful shutdown 验收。
- 明确本 change 不接收 client world/visit mutation，不消费 world admission，不实现 TLS/TCP gameplay，也不宣称 own-world/visit-world 竖切完成。

## Capabilities

### New Capabilities

- `server-websocket-control`: 定义 production WSS 控制面在认证握手、唯一消息路由、连接索引、受控 push、背压、会话失效、可观测、生命周期和分层验收方面的长期行为。

### Modified Capabilities

无。

## Impact

- 服务端配置、公开 TLS/HTTP component、Composition Root 生命周期与 readiness。
- Session production ticket consume、`ConnectionInvalidator` 和 Redis session adapter。
- 新增 WSS transport、connection registry、push publisher、codec 与结构化观测边界。
- 复用既有 Protobuf envelope、message/route registries、realtime fixtures 和 WSS endpoint manifest；不新增或重编号协议消息。
- 依赖目录需要锁定并验证一个 Go WebSocket implementation；不得引入第二套 HTTP router、session store、credential 或业务状态 owner。
