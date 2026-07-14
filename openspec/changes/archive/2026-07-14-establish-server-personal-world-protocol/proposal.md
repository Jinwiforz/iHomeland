## Why

PersonalWorld、current assignment、PersonalWorld production storage 与 VisitSession domain/application 已分别归档，但客户端仍没有唯一、可生成且可验证的 own-world/visit-world 跨端契约。若在 transport change 中临时定义字段、message ID 或 admission 语义，极易产生 WSS/TCP 双入口、payload identity 覆盖 `AuthContext`、invite 被误当 credential，以及 gameplay connection ticket 越权代表 world admission。

## What Changes

- 新增 `ihomeland.world.v1` 与 `ihomeland.visit.v1` Protobuf 源，映射已经冻结的 PersonalWorld、client-safe assignment、VisitSession、invite/membership、Visitor state、revision、deadline 与 safe-return projection；wire 绝对时间统一使用 Unix epoch milliseconds，不能反向改变 domain 的 UTC 微秒事实，完整 assignment 只作为 opaque admission 的服务端 binding。
- 扩展 HTTPS OpenAPI，冻结 authenticated own-world bootstrap、目标 Visitor accept invite 与一次性 world/visit admission issuance 契约；所有 operation 明确 body limit、timeout、idempotency、稳定错误和 payload identity 边界。
- 为 `world` 与 `visit` 分配互不重叠的 realtime message owner range，登记 WSS control notice 与 TLS/TCP authoritative request/command/response/push 的唯一 message ID、direction、channel、auth scope、QoS、完整 envelope 大小、rate policy、timeout 和幂等语义。
- 明确 gameplay `ConnectionTicket` 只授权建立 TLS/TCP 连接；一次性短期 world/visit admission 另行绑定 Player/session epoch、role、PersonalWorld、VisitSession（Visitor 时）、完整 assignment、endpoint/channel、nonce 与 expiry。Invite、AdmissionIntent、payload world/instance/role 均不能替代 admission verifier 结果。
- 增加 deterministic HTTP cases、realtime golden packets、negative fixtures 与 admission semantic corpus，扩展 contract validator/descriptor/registry tests，覆盖错误通道、错误 kind/correlation、未知 envelope enum、越界大小、payload actor 字段，以及 stale assignment/epoch、admission replay/expiry 的后续 verifier 验收输入。
- 只冻结已有领域行为的 wire contract；不创建 generic world interaction command/message ID。未来交互必须由独立 OpenSpec 登记 actor role、mutation owner、settlement owner、idempotency、revision/transaction fence 后再增量加入协议。
- 本 change 不实现 HTTP/WSS/TCP handler、listener、connection registry、VisitSession Redis adapter、cleanup worker、admission issuer/verifier/nonce store、runtime wiring、Go 协议客户端或 Unity，也不提交可重新生成的 Go/C# generated code。

## Capabilities

### New Capabilities

- `server-personal-world-protocol`: 定义 own-world/visit-world 的 HTTPS、WSS 与 TLS/TCP wire schema、一次性 admission 分层、message/error/route registry、compatibility 与 deterministic fixtures。

### Modified Capabilities

无。`server-contracts`、`network-transport`、`server-personal-world`、`server-world-instance-placement` 与 `server-visit-session` 的长期边界保持不变；本 capability 只把这些既有事实映射为唯一跨端契约。

## Impact

- 新增 `shared/proto/ihomeland/world/v1/`、`shared/proto/ihomeland/visit/v1/` 协议源，并更新 `shared/contracts/http/v1/openapi.yaml`、message/error/route registries 与 versioned fixtures。
- 扩展 `server/internal/contract`、`server/internal/protocol` 与 `tools/proto/proto.ps1` 的 schema/descriptor/registry/fixture 校验，但 domain/application 不依赖 generated Protobuf type。
- 更新协议、网络、文件结构、路线图与服务端 README 中的 owner range、P0 完成边界及后续 N0 进入条件。
- 为后续 `add-server-http-bootstrap`、`add-server-websocket-control`、`add-server-tcp-gameplay` 提供冻结输入；当前 server Composition Root 与公开业务面保持不变。
