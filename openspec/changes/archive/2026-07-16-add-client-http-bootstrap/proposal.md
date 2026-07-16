## Why

客户端运行时与 C# 协议基线已经完成，但 Unity 仍无法通过冻结的公开 HTTPS 契约取得版本、启动配置、账号会话和后续实时通道所需的一次性凭据。现在需要先建立独立、可测试且安全的 HTTP 启动边界，作为 WSS、TLS/TCP 和个人世界客户端服务的唯一前置入口。

## What Changes

- 增加基于冻结 OpenAPI 契约的客户端 HTTP adapter，覆盖 version、bootstrap config、register、login、refresh、logout、connection ticket 与 own-world bootstrap。
- 增加 App Scope 的启动配置、账号会话与敏感凭据所有权边界，明确 access token、session epoch、ticket 和服务端 endpoint 的更新、失效与清理规则。
- 统一请求 deadline、调用方取消、HTTP/协议错误映射、`Retry-After`、request ID correlation 和安全日志行为。
- 将 HTTP capability 接入现有 `AppComposition` 初始化、失败回滚与逆序关闭，不引入 UI、WSS/TLS-TCP 连接或个人世界业务状态机。
- 增加纯 C# EditMode contract tests，并以冻结 HTTP fixtures 验证请求、响应、错误、超限、取消和敏感信息不泄漏；保留既有 PlayMode 与 Windows Development build 回归。

## Capabilities

### New Capabilities

- `client-http-bootstrap`: 定义 Unity 客户端公开 HTTPS 启动、账号会话、连接 ticket、own-world bootstrap、安全凭据和错误处理行为。

### Modified Capabilities

无。

## Impact

- 影响 `client/Assets/App` 中的纯 C# runtime、基础设施 adapter、Composition Root 与 EditMode tests。
- 消费 `shared/contracts/http/v1/openapi.yaml`、HTTP fixtures、endpoint manifest、错误目录与既有服务端 HTTPS 行为，不修改服务端公开契约。
- 不新增第三方异步或 HTTP 依赖，不提交可推导 generated code；仅增加非敏感环境配置及 BootstrapScene 的直接引用，不引入业务 Prefab 或 UI 资产。
- 为后续 `add-client-websocket-control`、`add-client-tcp-gameplay` 和 `establish-client-personal-world-services` 提供受控前置能力，但不声明这些后续能力已经完成。
