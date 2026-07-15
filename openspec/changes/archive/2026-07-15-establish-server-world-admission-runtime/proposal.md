## Why

world/visit 协议与 VisitSession production storage 已经就绪，但 TLS/TCP gameplay scope 仍不能证明某条连接有权进入当前 PersonalWorld 或 VisitSession。若把 credential 生成、绑定与重放保护留给后续 HTTP/TLS-TCP handler 临时实现，会把安全状态机分散到 transport，并无法独立验收旧 epoch、旧 assignment、错误 endpoint/channel 与重复消费。

## What Changes

- 新增 transport-independent 的 world admission issuer/verifier，签发短期、安全 ASCII、opaque 且默认脱敏的一次性 credential。
- credential 绑定受信 PlayerID、SessionID/epoch、Owner/Visitor role、PersonalWorldID、可选 VisitSessionID、`OWN_WORLD`/`JOIN`/`RECONNECT` purpose、完整 AssignmentStamp、TLS/TCP endpoint/channel 与绝对 deadline。
- 新增 production Redis admission store 与 owner Lua scripts，原子保存 digest-only record、重放同一 issuance 结果、单次消费 credential，并对过期、重放、绑定不匹配、损坏 schema 和依赖不确定性 fail closed。
- 为 VisitSession 增加仅接收 verifier 已恢复完整绑定的 qualification 构造路径；invite、`AdmissionIntent`、ConnectionTicket 或 gameplay scope 仍不能自行提升为 join/reconnect 资格。
- 将 admission semantic fixture 从“仅冻结未来语义”升级为由 production issuer/verifier 测试实际执行的验收 corpus，并补齐 unit、contract、fuzz、race 与真实 Redis integration coverage。
- 更新 Redis key registry、owner 文档、服务端边界与验收说明；本 change 不注册 HTTP route、TLS/TCP listener/dispatcher、Composition Root service graph、cleanup task 或客户端实现。

## Capabilities

### New Capabilities

- `server-world-admission-runtime`: 定义 world admission 的 opaque credential、受信 binding、幂等签发、原子消费、错误语义、Redis 恢复边界与 semantic fixture 验收。

### Modified Capabilities

- `delivery-sequencing`: 将 transport 的 admission 前置条件统一为已独立验收的 credential store 与原子消费语义。
- `server-personal-world-protocol`: 将既有 admission 契约同步到已实现的 opaque credential runtime，同时保持 production transport 未接线边界。

## Impact

- 代码：新增 `server/internal/worldadmission` 与 `server/internal/storage/worldadmission`，并最小修改 `internal/visitsession`、共享 Redis registry 和 fixture validator。
- 数据：新增 owner 为 `worldadmission` 的短期 Redis issuance/credential Hash；不新增 MySQL table，不持久化 raw credential。
- 契约：不修改已发布 OpenAPI、Protobuf、message/error/route 编号或 wire 字段；复用现有 stable world/visit errors 与 admission semantic corpus。
- 运行时：不改变当前进程公开入口或启动图；后续 `add-server-http-bootstrap` 与 `add-server-tcp-gameplay` 只消费本 capability。
