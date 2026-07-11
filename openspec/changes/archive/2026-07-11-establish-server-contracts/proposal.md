## Why

服务端领域、存储和 transport changes 需要共享同一套可生成、可验证、可演进的基础契约，否则 message id、错误语义、会话身份和 framing 会在实现阶段分散定义。S0 建立机器可读契约与独立测试入口，为后续业务协议 changes 提供稳定治理边界。

## What Changes

- 建立基于仓库技术版本目录所选 Protobuf edition 的基础 `common/account/session/control` packages 与 OpenAPI HTTPS JSON contract。
- 建立全局 message id、error code、OpenAPI HTTP operation 目录和实时 Message Route Registry。
- 定义可靠消息 envelope、TLS/TCP length framing、大小限制、request/command/push 语义和未知值行为。
- 定义 session、session epoch、access token、一次性 connection ticket、endpoint manifest 与 auth scope 契约。
- 定义账号 HTTP 契约、WSS control push 与通用 TLS/TCP gameplay connection capability，不提前冻结具体业务消息。
- 建立协议生成、lint、Git-source breaking、registry、fixture 和 golden packet 验证入口，并让全部工具与规范版本统一受仓库技术版本目录治理。
- 生成并验证被 Git 忽略的 Go 协议代码；C# 生成配置与兼容性约束在本 change 中冻结并通过临时输出验证。
- 不实现 Gin handler、WSS/TCP listener、账号/个人世界/访客会话领域、MySQL/Redis adapter 或 Unity runtime。

## Capabilities

### New Capabilities

- `server-contracts`：规定基础协议源、HTTP 与实时契约、编号/错误/路由 registry、会话与 control 消息、framing、生成工具和跨端 fixtures。

### Modified Capabilities

无。

## Impact

- 新增 `shared/proto/`、`shared/contracts/` 与 `tools/proto/` 的协议源、registry、fixtures 和验证配置。
- 为 Go 协议生成与 codec/fixture 测试建立最小 Go module，但不创建服务端运行入口或业务实现。
- 更新 `docs/roadmap.md`、`docs/protocol-compatibility.md` 和文件结构说明，使 change 名称、生成阶段与产物 owner 一致。
- 后续 session、account、world/visit protocol、HTTP、WSS、TCP 和 Unity changes 必须消费本 change 建立的 artifacts，不得各自定义第二套基础契约治理。
