## Why

iHomeland 已完成架构基线，但仓库还没有可运行的 Go 服务端。需要先建立最小服务端骨架，为后续 Protobuf 协议、WebSocket 网关和房间大厅提供稳定入口。

## What Changes

- 初始化 Go module 和服务端基础目录。
- 添加 `server/cmd/server/main.go` 作为应用入口。
- 添加配置加载能力，覆盖 HTTP 地址、日志等级、协议版本和版本文件路径。
- 添加项目级 logger 封装，第一阶段使用 `slog`。
- 添加 Gin HTTP server。
- 实现 `/healthz`、`/readyz`、`/version`。
- 添加基础测试，覆盖配置默认值、版本响应和健康检查。
- 不实现 WebSocket、Protobuf envelope、房间业务、Redis/MySQL 接入或 gRPC 拆分。

## Capabilities

### New Capabilities

- `server-foundation`: Go 服务端启动、配置、日志、HTTP 基础接口、健康检查、就绪检查、版本元数据和基础测试。

### Modified Capabilities

- 无。

## Impact

- 影响 `server/`：新增 Go module、服务端入口和 `internal/` 基础包。
- 影响 `README.md` 或相关文档：补充本地运行和测试入口。
- 新增依赖：Gin。日志优先使用标准库 `slog`。
- 不改变现有 `protocol`、`gateway`、`room`、`storage` 主规格行为；后续 change 再分别实现协议、网关和房间业务。
