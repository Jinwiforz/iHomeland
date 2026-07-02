# 文件结构规划

## 当前顶层目录

```text
client/      客户端工程或客户端版本信息
docs/        项目文档、架构、路线图、规范
openspec/    需求、设计、规格和变更任务
server/      Go 服务端代码和本地服务端基础设施配置
shared/      跨端共享协议和生成配置
tools/       开发、生成、构建和运维辅助工具
```

## 共享协议结构

```text
shared/
  proto/
    README.md
    realtime/
      v1/
        envelope.proto
```

### `shared/proto`

跨端 Protobuf 源文件目录。客户端和服务端都以这里的 schema 作为实时通信契约，生成代码不得手工修改。

## 服务端目标结构

```text
server/
  README.md
  .env.example
  compose.yaml
  go.mod
  go.sum
  version.json
  cmd/
    server/
      main.go
  config/
    local.yaml
  scripts/
    run.bat
    test.bat
    start-local-infra.bat
    stop-local-infra.bat
    verify-local.bat
  internal/
    app/
    config/
    infra/
    logger/
    ops/
    gateway/
    protocol/
      pb/
    room/
    storage/
```

### `cmd/server`

服务端二进制入口，只做启动编排：

- 加载配置
- 初始化 logger
- 初始化依赖
- 注册 HTTP 和 WebSocket
- 启动和优雅关闭服务

### `config`

服务端本地配置目录。`config/local.yaml` 用于本地开发默认配置，环境变量只作为临时覆盖或部署覆盖。

配置中包含服务端 HTTP 地址、版本文件路径、MySQL 地址和 Redis 地址。本地依赖既可以由 Docker Compose 提供，也可以由本机安装服务提供；服务端只依赖配置地址，不直接依赖 Docker。

### `.env.example` 和 `compose.yaml`

服务端本地基础设施配置文件。`.env.example` 只提供本地示例值，不包含真实密钥；`compose.yaml` 用于通过 Docker Compose 启动本地 MySQL 和 Redis。它们归属 `server/`，因为 MySQL、Redis 是服务端开发依赖。

### `README.md`

服务端模块说明，包含本地运行、测试、配置覆盖和模块内开发约束。根目录 README 只描述项目整体架构和文档入口，不承载服务端具体命令。

### `scripts`

服务端辅助脚本目录，例如本地运行、测试、检查和构建脚本。该目录不承载业务代码，也不与根目录 `tools/` 的跨模块工具职责重叠。

- `run.bat`：启动 Go 服务端。
- `test.bat`：运行服务端 Go 测试。
- `start-local-infra.bat`：使用 `server/compose.yaml` 启动本地 MySQL、Redis，并等待 Docker healthcheck 通过。
- `stop-local-infra.bat`：停止 Docker Compose 本地基础设施，默认保留开发数据卷。
- `verify-local.bat`：检查服务端实际配置的 MySQL、Redis 地址是否可连接，并验证 `/healthz`、`/readyz`、`/version`。

### `go.mod` 和 `go.sum`

服务端 Go module 文件归属 `server/`，避免将服务端依赖和缓存扩散到仓库根目录。

### `version.json`

服务端版本元数据文件，由 `/version` 接口读取。根目录 `release.json` 表示整体发布版本，`client/version.json` 表示客户端版本。

### `internal/config`

配置结构、默认值、加载和校验。

### `internal/infra`

本地基础设施依赖探测。当前通过 TCP 探测 MySQL 和 Redis 配置地址是否可连接，用于 `/readyz` 和本地验证。

### `internal/app`

应用组装层：

- 连接配置、日志和基础运维路由
- 创建 HTTP server
- 保持启动编排之外的依赖组装逻辑

### `internal/logger`

项目级日志适配层。业务代码只依赖该包，不直接依赖 `slog` 或 `zap`。

### `internal/ops`

运维接口：

- `/healthz`
- `/readyz`
- `/version`
- 后续 metrics hook

### `internal/gateway`

实时网关：

- WebSocket 连接
- 后续 TCP 连接
- Protobuf envelope 编解码
- session 管理
- 心跳和超时
- 消息分发

### `internal/room`

房间业务：

- 房间模型
- 状态机
- 权限
- 成员和座位
- 重连资格
- 房间大厅服务和内存 repository

### `internal/storage`

存储适配：

- MySQL repository
- Redis cache/session/index
- 事务边界
- 幂等处理

### `internal/protocol`

协议注册、消息 ID 映射、版本校验、envelope 编解码和生成代码适配。`.proto` 源文件放在 `shared/proto/`，生成代码归属 `internal/protocol/pb/`。

`gateway` 已具备第一阶段 WebSocket 基础入口、连接级 session、心跳、空闲超时、协议错误响应和分发边界。`room` 已具备第一阶段自定义房间大厅的内存实现，包括创建、加入、准备、退出、房主转移、断线保留和重连恢复。`storage` 的业务读写边界由后续 OpenSpec change 推进；TCP 传输也由后续 change 决定是否接入。

## 文档结构

```text
docs/
  architecture.md              总体架构
  engineering-standards.md     工程标准
  workflow.md                  项目流程规范
  roadmap.md                   路线图和 change 拆分
  file-structure.md            文件结构规划
  protocol-compatibility.md    协议兼容规则
  redis-keys.md                Redis key 规则
```

## OpenSpec 结构

```text
openspec/
  config.yaml
  specs/
    server-foundation/
    local-infra/
    protocol/
    gateway/
    room/
    storage/
  changes/
```

`openspec/specs/` 是长期行为契约。`openspec/changes/` 是一次次拟议变更，完成后归档并同步到主 specs。

## 工具结构

```text
tools/
  proto/
    generate.bat
```

### `tools/proto`

跨端协议生成工具目录。协议源文件位于 `shared/proto/`，当前生成 Go 服务端代码，后续 Unity 或 Godot 生成入口也归属此处。
