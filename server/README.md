# iHomeland 服务端

本目录是 iHomeland 的 Go 服务端模块。服务端依赖、缓存、配置和测试入口都应限制在 `server/` 内，避免污染项目根目录。

## 当前能力

当前服务端已经具备最小可运行骨架：

- 配置加载和校验
- 结构化日志
- Gin HTTP server
- `/healthz`
- `/readyz`
- `/version`
- Protobuf envelope 基础协议适配
- 优雅关闭
- 基础单元测试

## 目录结构

```text
server/
  cmd/server/        服务端二进制入口
  config/            本地配置文件
  internal/app/      应用组装层
  internal/config/   配置结构、默认值、加载和校验
  internal/logger/   项目级日志适配
  internal/ops/      健康检查、就绪检查、版本接口
  internal/protocol/ 协议适配和生成代码
  scripts/           服务端辅助脚本
  go.mod
  go.sum
  version.json
```

## 本地运行

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
?    ihomeland/server/internal/logger [no test files]
ok   ihomeland/server/internal/ops
ok   ihomeland/server/internal/protocol
?    ihomeland/server/internal/protocol/pb/realtime/v1 [no test files]
```

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

除临时调试、CI 或部署场景外，不建议把这些环境变量作为日常启动方式。

## 开发约束

- 新增服务端代码应保持在 `server/` 内。
- 不得在项目根目录生成 `go.mod`、`go.sum`、`.gocache` 或 `.gomodcache`。
- 业务逻辑不得直接依赖 Gin handler、WebSocket connection 或 TCP socket。
- 导出的 Go 类型、函数、接口、常量和错误必须有中文 Go doc。
- 状态机、协议兼容、幂等、重连、权限和一致性逻辑必须写清设计意图与边界。
