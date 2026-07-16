# 技术版本治理

## 单一来源

根目录 `versions.yaml` 是协议规范、工具链、语言、基础设施和客户端运行时版本的唯一治理源。`release.json`、`server/version.json` 与 `client/version.json` 记录产品发布和构建身份，不承担技术依赖治理。

版本目录按以下类别维护：

- `protocols`：OpenAPI、Protobuf edition、HTTP 与 TLS。
- `toolchains`：Buf 与 Protobuf 代码生成器。
- `libraries`：`Google.Protobuf` C# runtime、Prometheus client、MySQL driver、Redis client 等直接影响共享运行基础的核心 library。
- `languages`：Go 等编程语言工具链。
- `infrastructure`：MySQL、Redis 和后续基础设施镜像。
- `client`：Unity Editor 与后续客户端运行时基线。

每项必须包含精确版本、官方来源，并由顶层 `verified_at` 记录最近核验日期。可下载的固定工具资产与外部 validation schema 还必须记录 SHA-256。只选择正式 stable/LTS 版本，不选择 preview、RC、beta 或浮动 `latest` 标签。

## 重复声明

生态工具要求版本出现在各自配置中，因此以下重复是必要的：

- `go.mod` 的 Go 与 module dependency 版本。
- `.proto` 的 edition 与 Buf generation template 的插件版本。
- OpenAPI 根节点的规范版本。
- Unity `ProjectVersion.txt` 的 Editor 版本。
- `go.mod` 中 `github.com/go-sql-driver/mysql`、`github.com/redis/go-redis/v9`、`github.com/coder/websocket` 的直接依赖版本。
- Docker/Compose image tag 的 MySQL、Redis 与其他服务版本；storage harness 还必须使用 `linux_amd64_digest`，不能只信任可漂移 tag。

这些文件不是第二份治理源。`tools/proto/proto.ps1 verify` 和后续统一 CI 必须检查它们与 `versions.yaml` 一致。

`github.com/coder/websocket` 的版本 owner 是服务端 `transport/wscontrol`，只用于共享公开 listener 的 WebSocket 协议与连接 I/O；Session、业务 owner 和 storage 不得直接依赖它。当前锁定版本采用 ISC 风格许可且无传递 module 依赖，升级时必须复核上游 license、Go 版本要求、compression/origin 默认值及 close/ping 语义。

`Google.Protobuf` 的版本 owner 是客户端 generated protocol assembly，只用于生成消息、descriptor、JSON 与二进制序列化。当前官方 NuGet 包采用 BSD-3-Clause；发布打包必须保留上游 copyright/license notice。其 `netstandard2.0` 资产声明的 `System.Memory`、`System.Runtime.CompilerServices.Unsafe` 及其 `System.Buffers`、`System.Numerics` 等 BCL 能力由当前 Unity 兼容运行时提供，不把对应 NuGet DLL 副本复制进 Generated；升级 Unity 或该包时必须重新执行 EditMode parity 与 Windows build，确认程序集解析没有改变。

## 项目局部 Go 环境

仓库不要求系统安装 Go，也不使用全局 `go env -w`。`tools/go/go.ps1` 从 `versions.yaml` 读取版本与官方 SHA-256，下载并校验 Windows SDK，然后直接执行项目内 `go.exe`。

目录职责固定为：

- `.local/go/<version>/`：完整 Go SDK。
- `.local/buf/<version>/`：Buf CLI。
- `.local/protoc-gen-go/<version>/`：由项目 Go SDK 安装的 Go generator。
- `.local/protoc/<version>/`：经官方 SHA-256 校验的 Protobuf compiler、内置 C# generator 与标准 include。
- `.local/nuget/google.protobuf/<version>/`：经官方 nupkg SHA-256、目标路径和强名称身份校验的 C# runtime 缓存。
- `.local/openapi/<version>/`：官方 OpenAPI validation schema。
- `.local/cache/go/mod/`：第三方 Go modules，不存放自动下载的 Go toolchain。
- `.local/cache/go/build/`：Go 编译缓存。

Protobuf schema lint、breaking check 与 generator 调度统一由 Buf 完成，项目不维护第二套公开编译或生成命令，也不持久化 descriptor 中间产物。Go generator 与官方 `protoc` 按锁定版本安装在项目 `.local/`，C# 由 Buf 的 `protoc_builtin` 调度；生成程序集所需的官方 `Google.Protobuf` 从锁定 NuGet 包恢复，不通过 Unity Package Manager 或系统 NuGet 隐式解析。开发者和 CI 不直接调用 `protoc`，全部协议动作仍只通过已跟踪的 `tools/proto/` 入口执行。

包装入口在进程内设置：

- `GOMODCACHE=.local/cache/go/mod`
- `GOCACHE=.local/cache/go/build`
- `GOTOOLCHAIN=local`

因此 Go 不会通过 `golang.org/toolchain` 再下载第二份 SDK，系统 `PATH` 和用户级 Go cache 也不会参与项目命令。

```powershell
& .\tools\go\go.ps1 version
& .\tools\go\go.ps1 test ./...
& .\tools\go\go.ps1 mod tidy
```

项目开发和 CI 必须使用包装入口；直接调用系统 `go` 不属于受支持的仓库命令。

MySQL/Redis integration 统一使用 `tools/storage/storage.ps1`。入口从 `versions.yaml`
读取明确 tag 与目标平台 digest，为每次运行生成独立 network、volume、container、
loopback 随机固定端口和高熵 secret，并在清理前验证 run-id label：

```powershell
& .\tools\storage\storage.ps1 -Action contract
& .\tools\storage\storage.ps1 -Action verify -TimeoutSeconds 600
```

临时 manifest、secret 与配置只写入已忽略的 `.local/storage/<run-id>/`。

## 升级流程

1. 从官方发布页确认新的正式版本、支持窗口和安全公告。
2. 判断是否改变协议、生成代码语义、持久化格式、部署行为或客户端兼容性。
3. 在同一变更中更新 `versions.yaml`、全部必要生态声明、生成配置、fixtures 和测试。
4. 运行相关 format、lint、generation、contract、unit、integration 与 migration 验证。
5. 保留旧提交的锁定信息，使历史提交仍可重复构建。

仅补丁升级且不改变行为时可以使用依赖维护提交。影响协议、生成代码语义、数据库升级路径、Unity 序列化或部署兼容性的升级必须先走 OpenSpec。

## 禁止事项

- 禁止在 CI、Dockerfile、生成脚本或 Unity 配置中使用浮动 `latest`。
- 禁止只更新版本目录而不更新实际生态声明。
- 禁止因为最新工具暂不支持目标规范而静默降级规范版本。
- 禁止把产品发布号与技术依赖版本混入同一个字段。
- 禁止项目脚本把 Go toolchain、module cache 或 build cache 写入用户级目录。
