# iHomeland Server

`server/` 是 Go 服务端入口。第一阶段目标是由 Go 协议测试客户端独立验收的账号、统一会话、PersonalWorld、WorldInstance 和 VisitSession v1；ActivityInstance、Room、Party、UDP/KCP、battle server、匹配、观战和回放不在当前边界内。

服务端严格按 `docs/roadmap.md` 的基础能力、个人世界、访客联机、公开通道和资格验收顺序实现。架构依赖由 `docs/architecture.md` 定义，目录归属由 `docs/file-structure.md` 定义，代码与测试要求由 `docs/engineering-standards.md` 定义。目录只在对应 change 实现真实行为时创建。

S0 已完成并归档。当前最小 Go module 只承载 Protobuf generation、契约/fixture 校验和 listener-independent codec，不包含 `cmd/server`、网络 listener、业务领域或存储 adapter；下一项 S1 `establish-server-runtime` 在同一 module 上扩展服务端运行时。

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
- `../docs/protocol-compatibility.md`
- `../docs/technology-versions.md`
