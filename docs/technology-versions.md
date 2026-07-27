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

Windows客户端secure Session使用操作系统DPAPI `CurrentUser`，不引入第三方加密库，也不自制密钥派生或密文格式。资格工具必须使用 `client/ProjectSettings/ProjectVersion.txt` 锁定的Unity Editor；Development与Release由同一Windows build owner生成，Release不得编译Development-only profile、诊断或故障注入入口。

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

## C++ Gameplay 工具链与依赖

`implement-game-simulation-core` 首次建立 `simulation/`，并已在创建 CMake target 前于 `versions.yaml` 锁定以下 Windows x64 reference toolchain 与无网络 core 依赖：

| 能力 | 精确版本 | 唯一 owner | 允许边界 |
|---|---|---|---|
| Build Tools | Visual Studio 2026 Build Tools 18.8.1 build 12021.73，`en-US` UI | `cpp-build` | 只允许显式 `bootstrap` 使用固定 installer/channel identity 安装；configure/build/test 不安装或升级系统软件 |
| 编译器 | MSVC 14.50.35717 LTS，`_MSC_VER=1950` | `cpp-build` | `v145` x64 C++20；configure 必须验证 exact toolset，禁止回退默认 compiler |
| Platform SDK | Windows SDK 10.0.26100.8876 | `cpp-build` | 锁定 26100 兼容系列的最新 servicing；仅 Windows x64 reference build，不代表 Linux production 已获资格 |
| 构建 | CMake 4.4.0 | `cpp-build` | tracked CMake sources、NMake Presets、CTest 与 dependency provider；包装入口先加载锁定 `VsDevCmd` |
| 服务端物理 | Jolt Physics 5.5.0 | `cpp-physics-adapter` | 只由 Jolt adapter 私有链接并实现项目 `PhysicsWorld` value contract |
| 运行时导航 | Recast/Detour 1.6.0 | `cpp-navigation-adapter` | 只构建 Detour runtime modules；不建立 Recast 离线烘焙 target |
| Fixture JSON | nlohmann/json 3.12.0 | `cpp-fixture-adapter` | 只在 fixture/config/evidence adapter 私有使用，不进入 gameplay public headers |

Build Tools bootstrapper、Windows SDK installer、CMake ZIP 和三项 library source archive 均登记不可漂移 URL 与 SHA-256；GitHub source archive 还同时登记 full commit。两个 Microsoft installer 必须通过 SHA-256 与 Microsoft Authenticode；MSVC 和 Windows SDK 由固定 installer、component ID、安装目录版本和编译期或已安装产品 identity 共同证明。`versions.yaml` 的 `verified_at` 是本轮外部身份核验日期，不代表未执行的 C++ benchmark 已通过。

### 本地恢复与离线构建

唯一包装入口是：

```powershell
& .\tools\cpp\cpp.ps1 bootstrap
& .\tools\cpp\cpp.ps1 restore
& .\tools\cpp\cpp.ps1 configure
& .\tools\cpp\cpp.ps1 build
& .\tools\cpp\cpp.ps1 test
& .\tools\cpp\cpp.ps1 verify
```

目录职责固定为：

- `.local/cpp/downloads/`：下载完成且 SHA-256 已验证的 immutable archives。
- `.local/cpp/toolchains/visual-studio/<version>/`：项目指定的 Build Tools 安装根；Visual Studio Installer metadata、系统 runtime 与 Windows SDK 仍按 Microsoft 支持边界注册到 Windows。
- `.local/cpp/sources/<name>/<version>/`：经路径逃逸检查解压的只读第三方 source 与 license。
- `.local/cpp/cmake/<version>/`：项目局部 CMake binary distribution。
- `.local/cpp/evidence/`：本机 toolchain discovery 临时 evidence。
- `simulation/out/build/<preset>/`：被忽略的 CMake binary tree、CTest 输出、build
  manifest 与包含 source digest 的 build identity。
- `simulation/reports/`：被忽略的 qualification、manifest 与 Release reference
  benchmark 本机报告。

`bootstrap` 是新机器的统一入口：它自动检测 exact Build Tools/MSVC/Windows SDK，缺失时下载固定 installer、校验 SHA-256 与 Microsoft Authenticode 后安装，并恢复 CMake 与第三方 source。Build Tools 文件使用 `.local/cpp/toolchains/` 作为安装根；由于 Microsoft installer、Windows SDK、UCRT 和系统注册组件不属于可复制 ZIP，少量 installer metadata 与 SDK 文件仍位于 Windows 管理的位置，首次安装可能触发 UAC。`restore` 只恢复项目局部依赖。普通 configure/build/test 必须使用 `FETCHCONTENT_FULLY_DISCONNECTED=ON` 和显式 local source directories；缺少缓存、checksum/commit/license 漂移或 partial marker 时 fail closed，不调用 package registry，不使用系统同名 Jolt/Detour/JSON，也不自动联网补齐。nlohmann/json 使用完整 tag source `tar.gz`，使 Windows PowerShell 5.1 无需 xz 仍能恢复 headers、CMake metadata 与 `LICENSE.MIT`。缓存全部被 Git 忽略，任何锁定信息、license notice、patch manifest、fixture 或 qualification freeze 仍必须进入 Git。

Build Tools 同时固定 `en-US` product language。包装入口在加载 `VsDevCmd` 后，仅在
受控 compiler/CMake 子动作期间设置 `VSLANG=1033` 与
`PreferredUILang=en-US`，使 MSVC `/showIncludes` 和 diagnostics 使用
ASCII/英文输出，避免中文系统代码页与 UTF-8 终端组合产生乱码；动作结束后恢复调用方环境。

Jolt 使用 static target、关闭 samples/viewer/install、关闭 RTTI 与 exceptions，并由 adapter 固定坐标、单位、collision layer、solver 和量化排序。Detour 只构建 runtime adapter 实际使用的 `Detour` target；不构建 `DetourCrowd`、`DetourTileCache`、demo、tests 或 Recast builder，首个 core 只消费版本化 nav fixture。nlohmann/json 关闭 tests/install/implicit conversions，只解析 closed fixture/config/evidence schema。三项依赖当前无项目补丁；出现补丁时必须在 tracked patch manifest 中记录上游基线、原因、diff digest、移除条件和升级处理。

B0.4 control codec 继续复用 B0.3 锁定的 nlohmann/json，不新增 Asio、KCP、gRPC、
Protobuf runtime 或 JSON 依赖。C++ 只通过私有 `ihomeland_sim_control_adapter` target
解析 closed canonical JSON；Go 使用标准库 `encoding/json` 并在业务 codec 层执行重复字段、
规范整数、UTF-8、64 KiB 与 closed payload 校验。任一方升级 JSON/toolchain 或改变
canonical 规则都必须重跑跨语言 golden、Release/ASan 和 real-child qualification。

`verify` 分别 clean 构建 Release CI 与 ASan preset。Debug/ASan 运行相同 workload
的结构与内存安全 smoke，但不裁决 CPU target；reference benchmark 和最终
qualification 只由 Release CI binary 生成，避免调试或 sanitizer instrumentation
改变性能口径。资格报告同时绑定 build manifest 与
`ihomeland-build-identity.json`，后者包含 source digest、preset 和完整依赖/编译策略。
`verify` 还会在两套 CTest 都成功后写入同源
`qualification-gate-receipt.json`；Release qualification binary 缺少该 receipt、receipt
与当前 source/CI target 不一致或 ASan target identity 缺失时必须拒绝生成 qualified 报告。

直接依赖 notice 与上游 license 必须由恢复测试验证存在，并由发布/资格 artifact 保留。升级任一版本时必须同时更新 URL、checksum、commit、notice、adapter parity、determinism、sanitizer 和 benchmark；回滚通过恢复 `versions.yaml` 与 CMake source 到前一提交并清除对应 `.local/cpp/` 版本目录完成，不能复用新版本 binary tree。

### B0.5 已引入的网络与密码依赖

| 能力 | 锁定依赖 | 唯一版本 owner | 约束 |
|---|---|---|---|
| 异步 UDP I/O | standalone Asio | `cpp-battle-udp-adapter` | 单 listener、显式取消/关闭、禁止系统 fallback |
| 可靠 ARQ | KCP core | `cpp-battle-kcp-adapter` | 项目 clock/output/session 包装、有界 queue 与重传预算 |
| X25519/HKDF/ChaCha20-Poly1305 | libsodium | `cpp-battle-crypto-adapter` | 固定 suite、secret 清理、跨语言 RFC vector parity |
| C++ battle payload | Protobuf lite + Abseil | `cpp-battle-protobuf-adapter` | 只消费统一 proto source，不提交 generated code |

精确 version、source identity、SHA-256、license、adapter owner 与 rollback 以
`versions.yaml` 为唯一机器可读基线。构建必须从 `tools/cpp/cpp.ps1` 校验恢复，禁止
系统依赖或未锁定 fallback。升级任一依赖必须重跑 wire/crypto/KCP parity、Release/ASan
与 B0.5 failure regression；B0.6 网络报告不能替代依赖完整性验证。

B0.6 不引入新的网络、抓包或故障注入依赖。Fault gateway 使用项目锁定 Go 工具链与
标准 UDP API，C++ 黑盒客户端复用同一组锁定 KCP/libsodium/Protobuf primitives，但由
architecture gate 禁止链接 production transport/gameplay adapter。真实资格需要本机
Windows x64、项目局部 Go/CMake/MSVC/Windows SDK 缓存与 storage harness 所需 Docker；
缺项应先通过各自统一入口恢复，不允许在资格脚本中临时使用系统同名工具或在线 fallback。

### 后续仍未引入的依赖

| 能力 | 计划依赖 | 唯一版本 owner | 引入门禁 |
|---|---|---|---|
| 客户端镜头 | Cinemachine | Unity camera host | Battle network qualification、Unity Package Manager 精确版本、兼容矩阵与 scene/prefab 回归 |

项目代码只能通过窄 adapter/port 使用第三方能力。业务 component、跨端 protocol、公开 application contract 和持久 schema 不得暴露 Asio、Jolt、Detour 或 KCP 类型。不得把上游源码片段改名复制进业务目录来规避版本和许可证治理；确需 vendoring 时必须保留上游身份、完整许可证和补丁清单。

`CMakeLists.txt` 与 checked-in `CMakePresets.json` 是 C++ 构建源事实，本机 IDE project 和绝对路径不是。presets 不得包含密钥、用户目录或环境特定 endpoint。具体 adapter 职责和禁止扩散规则见 `docs/gameplay-simulation-architecture.md`。

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
