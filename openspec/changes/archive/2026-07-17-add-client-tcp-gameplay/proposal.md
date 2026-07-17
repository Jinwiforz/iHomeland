## Why

客户端已经具备统一 session owner、HTTPS bootstrap、生成协议和 WSS control，但仍无法消费服务端 v1 的 TLS/TCP gameplay 通道。现在需要在 PersonalWorld/VisitSession 业务 Services 之前建立一个有界、可验证且不持有业务最终事实的可靠连接基础层。

## What Changes

- 增加 TLS/TCP gameplay client：严格编码 `IHTP` v1 双凭据 preface，使用 4-byte big-endian frame 与生成的 `ReliableEnvelope`。
- 增加唯一 reader、serialized writer、有界发送队列、有界 pending operation、严格 sequence/correlation 校验和稳定关闭原因。
- 以冻结 route catalog 限制可发送 request/command、可接收 response/error/push；不提供任意 message ID 或 payload 入口。
- 把 `issueWorldAdmission` 作为第九个强类型 HTTPS operation 接入现有 adapter 和 Session owner，以 generation、expiry 与单次交付保护 opaque admission。
- 将 gameplay channel 纳入 App Scope 的显式初始化和逆序停止；初始化不联网，连接只能由后续 application flow 显式发起。
- 增加纯 C# EditMode contract/lifecycle/backpressure/race tests；不创建或修改 Unity Scene、Prefab、UI 与业务 Services。

## Capabilities

### New Capabilities

- `client-tcp-gameplay`: 客户端 gameplay 双凭据连接、framing、typed route、pending/push、背压和生命周期边界。

### Modified Capabilities

- `client-http-bootstrap`: 增加冻结的 `issueWorldAdmission` operation 与 admission 单次安全交付语义。

## Impact

- 影响 `client/Assets/App/Scripts/Infrastructure`、`Application/Session`、App Scope composition 和对应 EditMode tests。
- 消费现有 OpenAPI、Protobuf、route/error registry 与 realtime fixtures，不修改服务端公开协议。
- 不新增第三方依赖，不提交生成代码或本地凭据，不要求修改 Unity 序列化资产。
