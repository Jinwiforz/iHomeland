# 文件结构规划

## 当前顶层目录

```text
client/      Unity 客户端工程或客户端版本信息
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

跨端 Protobuf 源文件目录。Unity 客户端和服务端都以这里的 schema 作为实时通信契约，生成代码不得手工修改。

## 客户端目标结构

```text
client/
  README.md
  version.json
  Assets/
    App/
      Scripts/
        App.asmdef
        Core/
        Systems/
        UI/
        Editor/
          App.Editor.asmdef
          WebSocketSmokeTestMenu.cs
        Protocol/
          Pb/
    Resources/
      UI/
        Pages/
  Packages/
  ProjectSettings/
```

### `Assets/App/Scripts`

Unity 客户端运行时代码目录。`Core` 放置 `AppRoot`、`AppBootstrap` 和配置入口；`Systems` 放置 `NetworkSystem`、`AccountSystem`、`UISystem` 等受 `AppRoot` 管理的系统；`UI` 放置页面脚本和 UI 基类。

### `Assets/App/Scripts/Editor`

Unity Editor 专用工具目录。该目录必须通过 `App.Editor.asmdef` 限制为 `Editor` 平台编译，可以引用运行时 `App` assembly，但不得进入 Windows、Android、iOS 等玩家运行时构建。当前用于放置 `WebSocketSmokeTestMenu.cs`，提供 `iHomeland/Smoke Test/WebSocket Account` 本地联调入口。

### `Assets/App/Scripts/Protocol/Pb`

Unity C# Protobuf 生成代码输出目录，来源于 `shared/proto/`。该目录属于可再生成产物，不手工修改，不提交到 Git。

## 服务端目标结构

```text
server/
  README.md
  .env.example
  .env.local
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
    mysql-local-dev.sql
    run.bat
    test.bat
    setup-local-env.bat
    load-local-env.bat
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
    account/
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

配置中包含服务端 HTTP 地址、版本文件路径、环境名、MySQL 地址/账号/密码/连接池和 Redis 地址/密码/DB/超时。本地依赖既可以由 Docker Compose 提供，也可以由本机安装服务提供；服务端只依赖配置地址，不直接依赖 Docker。

### `.env.example`、`.env.local` 和 `compose.yaml`

服务端本地基础设施配置文件。`.env.example` 只提供本地示例值，不包含真实密钥；`.env.local` 是每台开发机器的私有覆盖配置，不提交到 Git；`compose.yaml` 用于通过 Docker Compose 启动本地 MySQL 和 Redis。它们归属 `server/`，因为 MySQL、Redis 是服务端开发依赖。

### `README.md`

服务端模块说明，包含本地运行、测试、配置覆盖和模块内开发约束。根目录 README 只描述项目整体架构和文档入口，不承载服务端具体命令。

### `scripts`

服务端辅助脚本目录，例如本地运行、测试、检查和构建脚本。该目录不承载业务代码，也不与根目录 `tools/` 的跨模块工具职责重叠。

- `mysql-local-dev.sql`：本地 MySQL 建库、授权和第一阶段 migration 执行命令，只用于本地开发。
- `run.bat`：启动 Go 服务端。
- `test.bat`：运行服务端 Go 测试。
- `setup-local-env.bat`：按 `--auto`、`--docker` 或 `--native` 生成 `server/.env.local`，并诊断所选模式需要的 Docker、端口占用和 Windows TCP excluded port range；Go 不可用时只提示，不阻塞依赖配置生成。
- `load-local-env.bat`：供本地脚本复用的环境加载片段，读取 `server/.env.local` 且不覆盖当前 shell 已有环境变量。
- `start-local-infra.bat`：使用 `server/compose.yaml` 启动本地 MySQL、Redis，并等待 Docker healthcheck 通过。
- `stop-local-infra.bat`：停止 Docker Compose 本地基础设施，默认保留开发数据卷。
- `verify-local.bat`：检查服务端实际配置的 MySQL、Redis 地址是否可连接，并验证 `/healthz`、`/readyz`、`/version`。

### `go.mod` 和 `go.sum`

服务端 Go module 文件归属 `server/`，避免将服务端依赖和缓存扩散到仓库根目录。

### `version.json`

服务端版本元数据文件，由 `/version` 接口读取。根目录 `release.json` 表示整体发布版本，`client/version.json` 表示 Unity 客户端版本。

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

### `internal/account`

第一阶段账号会话：

- 注册、登录和登出
- 会话恢复
- 当前玩家身份查询
- 玩家基础资料查询或创建
- session token 签发、恢复和失效

账号模块为房间大厅提供身份基础，不承载密码找回、第三方登录、好友、背包、经济或完整权限系统。

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

- repository/cache interface
- 测试用 fake/in-memory adapter
- MySQL account player repository
- Redis account session cache
- MySQL room summary repository 骨架
- Redis room runtime cache 骨架
- Redis key builder 和 TTL 常量
- MySQL migration 文件
- 幂等处理和恢复边界

### `internal/protocol`

协议注册、消息 ID 映射、版本校验、envelope 编解码和生成代码适配。`.proto` 源文件放在 `shared/proto/`，服务端生成代码本地输出到 `server/internal/protocol/pb/`，客户端生成代码本地输出到 `client/Assets/App/Scripts/Protocol/Pb/`。两个目录都属于可再生成产物，不提交到 Git。

`gateway` 已具备第一阶段 WebSocket 基础入口、连接级 session、心跳、空闲超时、协议错误响应和分发边界。`room` 已具备第一阶段自定义房间大厅的内存实现，包括创建、加入、准备、退出、房主转移、断线保留和重连恢复。`storage` 已具备账号资料 MySQL repository、账号 session Redis cache、测试用 fake adapter、Redis key builder、MySQL 迁移入口和房间 adapter 骨架；房间 Redis/MySQL 正式持久化由后续 change 继续推进。TCP 传输也由后续 change 决定是否接入。

## 文档结构

```text
docs/
  architecture.md              总体架构
  engineering-standards.md     工程标准
  local-data-commands.md       本地 MySQL/Redis 命令入口
  workflow.md                  项目流程规范
  roadmap.md                   路线图和 change 拆分
  file-structure.md            文件结构规划
  protocol-compatibility.md    协议兼容规则
  redis-local-dev-commands.md  本地 Redis 命令参考
  redis-keys.md                Redis key 规则
```

## OpenSpec 结构

```text
openspec/
  config.yaml
  specs/
    server-foundation/
    local-infra/
    account-session/
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
    setup.bat
    generate.bat
```

### `tools/proto`

跨端协议生成工具目录。`setup.bat` 准备项目本地 `protoc` 和 `protoc-gen-go`，输出到 `.tools/`；`generate.bat` 是唯一协议生成入口，必须同时生成 Go 服务端代码和 Unity C# 代码。协议源文件位于 `shared/proto/`。生成代码不得手工修改；Go 与 Unity C# 生成输出都不提交到 Git。
