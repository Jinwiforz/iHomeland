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
  internal/logger/   项目级日志适配
  internal/ops/      健康检查、就绪检查、版本接口
  internal/protocol/ 协议适配和生成代码
  internal/room/     自定义房间大厅模型、状态机和服务
  scripts/           服务端辅助脚本
  go.mod
  go.sum
  version.json
```

## 本地运行

先在仓库根目录启动本地基础设施：

```powershell
.\server\scripts\start-local-infra.bat
```

该脚本使用 `server/compose.yaml` 启动 MySQL 和 Redis，并等待容器健康检查通过。停止本地基础设施：

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

日常开发优先修改 `config/local.yaml`。环境变量只用于临时覆盖配置，不作为默认开发入口。

运行过程中产生的 Go 工具链缓存应保留在 `server/` 内。

本地服务启动后可从仓库根目录验证基础设施和基础接口：

```powershell
.\server\scripts\verify-local.bat
```

验证脚本只检查服务端实际配置会连接的地址：`IHOMELAND_MYSQL_ADDR`、`IHOMELAND_REDIS_ADDR` 指向的 TCP 地址是否可连接，并请求 `/healthz`、`/readyz` 和 `/version`。未设置地址时默认检查 `127.0.0.1:3306` 和 `127.0.0.1:6379`。

因此 Docker 和本机安装的 MySQL、Redis 可以二选一使用；关键是服务端配置指向的端口必须可连接。Docker 容器是否 healthy 由 `start-local-infra.bat` 负责检查，`verify-local.bat` 不关心依赖是由 Docker 还是本机服务提供。不要让 Docker 和本机安装同时占用同一端口。

## 本地测试

执行：

```powershell
.\scripts\test.bat
```

测试缓存保留在 `server/.gocache` 和 `server/.gomodcache`。测试入口执行：

```powershell
go test ./...
```

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
```

`internal/gateway` 测试会启动临时 HTTP server，并用 Go WebSocket 测试客户端连接 `/ws`，覆盖连接注册、心跳响应、协议版本拒绝、非法 payload、缺失 request id、未知 message id、非二进制消息和空闲超时清理。

`internal/room` 测试覆盖房间创建、加入、重复加入、准备、退出、房主转移、断线保留和重连恢复。`internal/app` 中的房间网关测试会用 Go WebSocket 测试客户端发送房间大厅 Protobuf envelope，验证服务端请求响应链路。

## 协议生成

需要本机可用 `protoc` 和 `protoc-gen-go`。

执行：

```powershell
..\tools\proto\generate.bat
```

协议源文件位于：

```text
..\shared\proto
```

生成代码归属：

```text
internal\protocol\pb
```

## 配置覆盖

服务端支持以下环境变量用于临时覆盖配置：

- `IHOMELAND_CONFIG`
- `IHOMELAND_HTTP_ADDR`
- `IHOMELAND_LOG_LEVEL`
- `IHOMELAND_PROTOCOL_VERSION`
- `IHOMELAND_RELEASE_PATH`
- `IHOMELAND_SERVER_VERSION_PATH`
- `IHOMELAND_CLIENT_VERSION_PATH`
- `IHOMELAND_MYSQL_ADDR`
- `IHOMELAND_MYSQL_DATABASE`
- `IHOMELAND_MYSQL_USER`
- `IHOMELAND_REDIS_ADDR`
- `IHOMELAND_GATEWAY_IDLE_TIMEOUT`

除临时调试、CI 或部署场景外，不建议把这些环境变量作为日常启动方式。

## 开发约束

- 新增服务端代码应保持在 `server/` 内。
- 不得在项目根目录生成 `go.mod`、`go.sum`、`.gocache` 或 `.gomodcache`。
- 业务逻辑不得直接依赖 Gin handler、WebSocket connection 或 TCP socket。
- 导出的 Go 类型、函数、接口、常量和错误必须有中文 Go doc。
- 状态机、协议兼容、幂等、重连、权限和一致性逻辑必须写清设计意图与边界。
