## Context

iHomeland 当前只有架构基线和版本元数据，还没有可运行的 Go 服务端。后续 `add-protocol-envelope`、`add-websocket-gateway` 和 `add-room-lobby` 都需要一个稳定、可测试、可观测的服务端基础。

本 change 只建立服务端骨架，不实现实时网关、协议 envelope、房间业务、Redis/MySQL 连接或 gRPC 拆分。第一阶段采用单进程优先，服务端内部通过清晰包边界为后续能力预留扩展点。

## Goals / Non-Goals

**Goals:**

- 初始化 Go module。
- 建立 `server/cmd/server/main.go` 和 `server/internal/` 基础目录。
- 提供配置加载和校验。
- 通过项目级 logger 包封装 `slog`。
- 使用 Gin 提供 HTTP server。
- 实现 `/healthz`、`/readyz`、`/version`。
- 读取现有 `release.json`、`server/version.json`、`client/version.json` 中的版本元数据。
- 添加基础测试，确保服务端骨架可验证。

**Non-Goals:**

- 不实现 WebSocket endpoint。
- 不定义或生成 Protobuf envelope。
- 不实现房间、匹配、账号或客户端集成业务。
- 不连接 Redis 或 MySQL。
- 不引入 gRPC 服务拆分。
- 不添加 Docker Compose。

## Decisions

### Go module 位于 server 目录

Go module 应在 `server/` 目录初始化，使服务端依赖、测试缓存和构建入口都限制在服务端范围内。module path 暂定为 `ihomeland/server`，后续接入远程仓库后可通过单独 change 调整。

备选方案是在仓库根目录建 module。该方案能让未来 `shared/` 生成代码更容易被服务端引用，但会让服务端依赖和 Go 缓存污染项目根目录；当前阶段优先保持服务端边界清晰。

### 服务端入口只做启动编排

`server/cmd/server/main.go` 只负责加载配置、初始化 logger、创建 HTTP server、启动和优雅关闭。业务实现放在 `server/internal/` 下。

备选方案是把 handler、配置和启动逻辑都写进 `main.go`。这会降低初始文件数量，但会让后续拆分困难。

### 配置先支持本地文件，环境变量作为覆盖

配置包先提供结构体、默认值、本地 YAML 配置文件、环境变量覆盖和校验。默认配置文件为 `server/config/local.yaml`，日常开发通过该文件配置监听地址、日志等级、协议版本和版本文件路径；环境变量用于临时覆盖。

备选方案是只使用环境变量。该方案部署友好，但本地开发每次启动都要手动设置变量，不够顺手。另一个备选方案是立刻引入 Viper 等配置库；当前需求较小，直接使用 `yaml.v3` 更轻。

### 日志使用项目封装的 slog

第一阶段使用标准库 `slog`，并封装在 `server/internal/logger`。业务代码依赖项目 logger，而不是直接散落 `slog` 初始化细节。

备选方案是直接使用 `zap`。`zap` 适合更高吞吐日志场景，但当前先以简单、稳定和少依赖为主。

### HTTP 基础接口放在 ops 包

`server/internal/ops` 负责 `/healthz`、`/readyz` 和 `/version`。这些接口是控制面能力，不属于 gateway 或 room 业务。

备选方案是把这些接口放在 gateway。gateway 后续会承载实时连接，不应混入基础运维职责。

### readiness 暂不检查外部依赖

本 change 不连接 Redis/MySQL，因此 `/readyz` 只报告进程是否具备处理基础 HTTP 请求的能力。后续接入依赖时再扩展 readiness 详情。

## Risks / Trade-offs

- [Risk] server module path 未来可能变化 -> Mitigation: 暂用 `ihomeland/server`，远程仓库确定后通过单独 change 调整。
- [Risk] readiness 过于简单 -> Mitigation: 当前没有外部依赖，后续接入 Redis/MySQL 时扩展。
- [Risk] 配置能力过轻 -> Mitigation: 先用 YAML 文件和环境变量覆盖满足当前需求，避免过早引入复杂配置框架。
- [Risk] 版本文件路径在不同工作目录下解析失败 -> Mitigation: 默认以 `server/` 为运行目录，跨目录版本文件使用相对路径，并允许通过环境变量显式覆盖。

## Migration Plan

1. 在 `server/` 下初始化 Go module 和基础目录。
2. 添加 config、logger、ops、app/server 相关包和 `server/config/local.yaml`。
3. 实现 HTTP 基础接口。
4. 添加单元测试和 handler 测试。
5. 更新 README 本地运行入口。
6. 添加服务端测试脚本，封装 Go 工具链缓存环境变量，避免文档和命令中出现本机绝对路径。

回滚方式：删除本 change 新增 Go module、server 代码和 README 运行说明。

## Open Questions

- 远程仓库确定后，服务端 Go module path 是否改为完整仓库路径？
- 版本元数据是否在后续 release 流程中由工具自动生成？
