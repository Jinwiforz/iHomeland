# iHomeland Server

`server/` 是 Go 服务端 module 根目录。第一阶段目标是由 Go 协议测试客户端独立验收的账号、统一会话、PersonalWorld、WorldInstance 和 VisitSession v1；ActivityInstance、Room、Party、UDP/KCP、battle server、匹配、观战和回放不在当前边界内。

服务端严格按 `docs/roadmap.md` 的基础能力、个人世界、访客联机、公开通道和资格验收顺序实现。架构依赖由 `docs/architecture.md` 定义，目录归属由 `docs/file-structure.md` 定义，代码与测试要求由 `docs/engineering-standards.md` 定义。目录只在对应 change 实现真实行为时创建。

当前 module 包含协议/fixture 校验、listener-independent codec、唯一 `cmd/server`、Composition Root 和独立诊断 listener；公开业务 listener、账号、session、个人世界、访客会话、MySQL 与 Redis adapter 由各自 change 按路线接入。

## 命令规则

协议与契约统一入口：

```powershell
& .\tools\proto\proto.ps1 bootstrap
& .\tools\proto\proto.ps1 format
& .\tools\proto\proto.ps1 generate
& .\tools\proto\proto.ps1 fixtures
& .\tools\proto\proto.ps1 verify
```

命令自动读取根目录 `versions.yaml`，Buf、`protoc-gen-go` 与官方 `protoc` 按 `.local/<dependency>/<version>/` 安装到独立的已忽略目录并校验版本；具有官方资产摘要的依赖还必须通过 SHA-256。`protoc` 只由 Buf 调用，不是独立开发入口。

Go Protobuf code 位于已忽略的 `server/internal/generated/proto/`。干净检出后必须先执行 `tools/proto/proto.ps1 generate`，日常完整验收优先执行 `verify`；不得依赖仓库中存在 generated code。

日常 Go 命令使用项目局部入口。它会在 `.local/go/` 准备目录锁定且经过 SHA-256 校验的 Go SDK，并把 module 与 build cache 隔离到源码树外的 `.local/cache/go/`，避免 IDE 把第三方 module 识别为服务端源码：

```powershell
& .\tools\go\go.ps1 version
& .\tools\go\go.ps1 test ./...
& .\tools\go\go.ps1 mod tidy
```

执行 `tools/go/go.ps1 test ./...` 前，必须先成功运行 `tools/proto/proto.ps1 generate`。CI 必须从删除 generated 目录的状态开始验证该顺序。

## 本地启动

从仓库根目录执行：

```powershell
& .\tools\proto\proto.ps1 generate
& .\tools\go\go.ps1 run ./cmd/server --config config/local.yaml
```

`--config` 必须显式提供。配置优先级固定为安全默认值、YAML 文件、白名单 `IHOMELAND_` 环境覆盖；未知字段或非法覆盖会在创建 listener 和 goroutine 前失败。仓库只提交不含 secret 的 `server/config/local.yaml`，进程不会自动读取 `.env`。

推荐诊断默认地址为 `127.0.0.1:8081`：

- `/healthz`：进程存活，不代表可以接收业务
- `/readyz`：仅 `ready` 返回 200
- `/version`：有界构建身份
- `/metrics`：低基数 Prometheus/OpenMetrics 指标

该 listener 不承载账号、个人世界或其他公开业务 API。绑定非 loopback 地址时必须使用部署网络策略限制访问。

推荐值被占用时，通过所选配置文件或白名单环境变量 `IHOMELAND_DIAGNOSTIC_ADDRESS` 显式覆盖。进程不会静默寻找其他端口；绑定冲突会在启动阶段返回非零结果。完整规划见 `docs/network-port-allocation.md`。

正常结构化日志写入 `stdout`，参数、启动和强制退出等进程级失败写入 `stderr`。服务端不直接创建日志文件；本地可由终端或 IDE 保存控制台输出，部署环境由日志采集器持久化和轮转。

退出码：

| Code | 语义 |
|---|---|
| `0` | 受控 signal 且在 deadline 内干净关闭 |
| `2` | flag 或配置错误 |
| `3` | 组件初始化失败 |
| `4` | 必需后台任务或运行时致命失败 |
| `5` | graceful shutdown 失败或超时 |
| `6` | 第二次终止 signal 强制退出 |

所有开发入口都必须能够从仓库根目录通过终端直接执行。若本机 execution policy 阻止直接运行，可使用 `powershell.exe -NoProfile -ExecutionPolicy Bypass -File <script> <arguments>`。

任何 change 引入开发命令时必须同时提供：

- PowerShell/CI 可调用的非交互入口
- 配置来源与示例
- 超时、退出码和日志说明

引入 MySQL、Redis 或其他外部依赖的 runtime change 还必须提供本地初始化、验证与清理文档。

## 相关文档

- `../docs/roadmap.md`
- `../docs/architecture.md`
- `../docs/file-structure.md`
- `../docs/engineering-standards.md`
- `../docs/network-transport-architecture.md`
- `../docs/network-port-allocation.md`
- `../docs/protocol-compatibility.md`
- `../docs/technology-versions.md`
