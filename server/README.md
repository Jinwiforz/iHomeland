# iHomeland 服务端

本目录是 iHomeland 的 Go 服务端模块。服务端依赖、缓存、配置和测试入口都应限制在 `server/` 内，避免污染项目根目录。

## 当前能力

当前服务端已经具备最小可运行骨架：

- 配置加载和校验
- 结构化日志
- Gin HTTP server
- `/healthz`
- `/readyz`，包含本地 MySQL 和 Redis 依赖状态
- `/version`
- Protobuf envelope 基础协议适配
- WebSocket 实时入口 `/ws`
- WebSocket 连接级 session、心跳响应、协议版本拒绝、结构化错误响应和空闲超时清理
- 自定义房间大厅基础能力：创建房间、加入房间、准备/取消准备、退出房间、房主转移、断线保留和重连恢复
- storage 边界：repository/cache interface、测试用 fake adapter、Redis key builder、MySQL 第一阶段迁移、账号 MySQL repository 和账号 Redis session cache
- 账号会话能力已接入服务端注册、登录、登出、会话恢复和 gateway 身份绑定；Unity 客户端已具备 C# Protobuf 生成代码、Google.Protobuf runtime 和账号 WebSocket envelope 调用入口
- 优雅关闭
- 基础单元测试

## 目录结构

```text
server/
  cmd/server/        服务端二进制入口
  config/            本地配置文件
  internal/app/      应用组装层
  internal/config/   配置结构、默认值、加载和校验
  internal/gateway/  WebSocket 实时网关和连接级 session
  internal/account/  第一阶段账号会话、注册、登录登出和玩家身份
  internal/logger/   项目级日志适配
  internal/ops/      健康检查、就绪检查、版本接口
  internal/protocol/ 协议适配和生成代码
  internal/room/     自定义房间大厅模型、状态机和服务
  internal/storage/  持久化和运行态缓存边界
  scripts/           服务端辅助脚本
  go.mod
  go.sum
  version.json
```

## 本地运行

首次在新机器运行时，先在仓库根目录初始化本机配置并诊断端口。默认 `--auto` 会优先探测本机安装的 MySQL/Redis 是否可连；如果不可连，则生成 Docker 本地依赖配置：

```powershell
.\server\scripts\setup-local-env.bat
```

也可以显式选择模式：

```powershell
.\server\scripts\setup-local-env.bat --docker
.\server\scripts\setup-local-env.bat --native
```

该脚本会生成或修复 `server/.env.local`，并检查所选模式需要的 Docker、端口占用和 Windows TCP excluded port range；Go 不可用时只给出提示，不阻塞依赖配置生成。`server/.env.local` 不提交到 Git；日常优先用该脚本生成配置，不建议修改 Windows 系统端口保留表。

本地默认宿主机端口使用项目专用端口：

```text
MySQL: 127.0.0.1:33306
Redis: 127.0.0.1:36379
```

然后启动本地基础设施：

```powershell
.\server\scripts\start-local-infra.bat
```

该脚本会加载 `server/.env.local`，使用 `server/compose.yaml` 启动 MySQL 和 Redis，并等待容器健康检查通过。停止本地基础设施：

```powershell
.\server\scripts\stop-local-infra.bat
```

停止脚本默认保留开发数据卷；需要清理数据时手动执行带 volume 删除的 Docker Compose 命令。

启动服务端：

执行：

```powershell
.\scripts\run.bat
```

默认配置文件：

```text
config/local.yaml
```

日常开发优先使用 `setup-local-env.bat` 生成 `server/.env.local`，保持 Docker 端口映射、服务端连接地址和验证脚本一致。`config/local.yaml` 保留服务端默认配置示例；环境变量只用于临时覆盖配置，不作为默认开发入口。

运行过程中产生的 Go 工具链缓存应保留在 `server/` 内。

本地服务启动后可从仓库根目录验证基础设施和基础接口：

```powershell
.\server\scripts\verify-local.bat
```

验证脚本会加载 `server/.env.local`，只检查服务端实际配置会连接的地址：`IHOMELAND_MYSQL_ADDR`、`IHOMELAND_REDIS_ADDR` 指向的 TCP 地址是否可连接，并请求 `/healthz`、`/readyz` 和 `/version`。未设置地址时默认检查 `127.0.0.1:33306` 和 `127.0.0.1:36379`。

因此 Docker 和本机安装的 MySQL、Redis 可以二选一使用；关键是服务端配置指向的端口必须可连接。Docker 容器是否 healthy 由 `start-local-infra.bat` 负责检查，`verify-local.bat` 不关心依赖是由 Docker 还是本机服务提供。不要让 Docker 和本机安装同时占用同一端口；如果端口冲突，优先修改 `server/.env.local` 中的项目端口。

`/readyz` 和 `verify-local.bat` 只表示依赖地址可达，不等同于房间摘要、重连资格或 Redis 丢失恢复已经通过。业务恢复能力需要通过 `internal/storage` 和 `internal/room` 的单元测试，或后续明确的 Redis/MySQL 集成测试验证。

本地 MySQL 建库、授权、迁移和 Redis 排障命令统一沉淀在：

```text
../docs/local-data-commands.md
scripts/mysql-local-dev.sql
../docs/redis-local-dev-commands.md
```

如果本机调试遇到 `Unknown database 'ihomeland'`，先按 `../docs/local-data-commands.md` 执行本地 MySQL 初始化命令。

## Storage 边界

`internal/storage` 定义第一阶段房间大厅需要的持久化和运行态接口：

- `RoomSummaryRepository`：保存/读取 MySQL 房间摘要事实。
- `PresenceCache`：保存玩家在线状态运行态。
- `RoomIndexCache`：保存房间索引缓存。
- `ReconnectTokenCache`：保存断线重连短期资格。

账号业务 runtime 已使用真实 MySQL 和 Redis：玩家基础资料写入 `account_player`，账号 session 写入 `ih:{env}:account:session:{sessionToken}`。服务端启动时会连接并 ping MySQL/Redis，失败则拒绝启动。

room service 当前仍使用进程内 repository 验证第一里程碑房间状态机；房间摘要、presence、room index 和 reconnect token 的真实持久化由后续房间存储 change 推进。测试使用 `storage.NewFakeStore()`，所以不需要启动 MySQL 或 Redis 也能验证状态机、幂等摘要写入和重连 token 管理。

## 本地测试

执行：

```powershell
.\scripts\test.bat
```

测试缓存保留在 `server/.gocache` 和 `server/.gomodcache`。测试入口会先确保项目本地 Protobuf 工具链可用，再重新生成协议代码并执行 Go 测试：

```powershell
..\tools\proto\generate.bat
go test ./...
```

协议生成代码属于可再生成产物。Go 生成代码和 Unity C# 生成代码都不提交到 Git；需要时通过 `tools\proto\generate.bat` 重新生成。`scripts\test.bat` 固定按上述顺序执行，保证测试使用当前 `shared/proto/` 生成出的 Go 代码。

预期结果应包含：

```text
?    ihomeland/server/cmd/server [no test files]
ok   ihomeland/server/internal/app
ok   ihomeland/server/internal/config
ok   ihomeland/server/internal/gateway
ok   ihomeland/server/internal/infra
?    ihomeland/server/internal/logger [no test files]
ok   ihomeland/server/internal/ops
ok   ihomeland/server/internal/protocol
?    ihomeland/server/internal/protocol/pb/realtime/v1 [no test files]
ok   ihomeland/server/internal/room
ok   ihomeland/server/internal/storage
```

`internal/gateway` 测试会启动临时 HTTP server，并用 Go WebSocket 测试客户端连接 `/ws`，覆盖连接注册、心跳响应、协议版本拒绝、非法 payload、缺失 request id、未知 message id、非二进制消息和空闲超时清理。

`internal/room` 测试覆盖房间创建、加入、重复加入、准备、退出、房主转移、断线保留和重连恢复。`internal/app` 中的房间网关测试会用 Go WebSocket 测试客户端发送房间大厅 Protobuf envelope，验证服务端请求响应链路。

`internal/storage` 测试覆盖 fake adapter 幂等写入、账号 MySQL repository、账号 Redis session cache、重连 token 覆盖语义、Redis key/TTL 和 migration 文件命名。room service 与 fake storage 的集成测试验证房间摘要和短期重连资格通过 storage interface 写入。

## 协议生成

协议生成由项目本地工具链管理，工具位于 `.tools\protoc\bin\protoc.exe` 和 `.tools\go\bin\protoc-gen-go.exe`。`tools\proto\generate.bat` 是唯一生成入口，会在生成前确保工具链就绪，并同时生成服务端 Go 代码和 Unity C# 代码。

```powershell
..\tools\proto\generate.bat
```

需要单独刷新协议工具链时，执行：

```powershell
..\tools\proto\setup.bat
```

协议源文件位于：

```text
..\shared\proto
```

服务端 Go 生成代码归属：

```text
internal\protocol\pb
```

Unity C# 生成代码归属：

```text
..\client\Assets\App\Scripts\Protocol\Pb\Realtime\V1
```

`internal\protocol\pb` 和 `..\client\Assets\App\Scripts\Protocol\Pb` 由 `tools\proto\generate.bat` 生成，并被 `.gitignore` 忽略。协议工具和下载缓存位于 `.tools/`，同样不提交到 Git；协议源文件仍以 `shared/proto/` 为准。

## 配置覆盖

服务端支持以下环境变量用于临时覆盖配置：

- `IHOMELAND_CONFIG`
- `IHOMELAND_ENV`
- `IHOMELAND_HTTP_ADDR`
- `IHOMELAND_LOG_LEVEL`
- `IHOMELAND_PROTOCOL_VERSION`
- `IHOMELAND_RELEASE_PATH`
- `IHOMELAND_SERVER_VERSION_PATH`
- `IHOMELAND_CLIENT_VERSION_PATH`
- `IHOMELAND_MYSQL_ADDR`
- `IHOMELAND_MYSQL_DATABASE`
- `IHOMELAND_MYSQL_USER`
- `IHOMELAND_MYSQL_PASSWORD`
- `IHOMELAND_MYSQL_MAX_OPEN_CONNS`
- `IHOMELAND_MYSQL_MAX_IDLE_CONNS`
- `IHOMELAND_MYSQL_CONN_MAX_LIFETIME`
- `IHOMELAND_REDIS_ADDR`
- `IHOMELAND_REDIS_PASSWORD`
- `IHOMELAND_REDIS_DB`
- `IHOMELAND_REDIS_DIAL_TIMEOUT`
- `IHOMELAND_GATEWAY_IDLE_TIMEOUT`

除临时调试、CI 或部署场景外，不建议把这些环境变量作为日常启动方式。

## 开发约束

- 新增服务端代码应保持在 `server/` 内。
- 不得在项目根目录生成 `go.mod`、`go.sum`、`.gocache` 或 `.gomodcache`。
- MySQL 和 Redis 的本地建库、授权、迁移、检查、清理和排障命令必须沉淀到 `server/scripts/` 或根目录 `docs/`，不得只保留在聊天记录或个人笔记中。
- 业务逻辑不得直接依赖 Gin handler、WebSocket connection 或 TCP socket。
- 导出的 Go 类型、函数、接口、常量和错误必须有中文 Go doc。
- 状态机、协议兼容、幂等、重连、权限和一致性逻辑必须写清设计意图与边界。
