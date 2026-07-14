# iHomeland Server

`server/` 是 Go 服务端 module 根目录。第一阶段目标是由 Go 协议测试客户端独立验收的账号、统一会话、PersonalWorld、WorldInstance 和 VisitSession v1；ActivityInstance、Room、Party、UDP/KCP、battle server、匹配、观战和回放不在当前边界内。

服务端严格按 `docs/roadmap.md` 的基础能力、个人世界、访客联机、公开通道和资格验收顺序实现。架构依赖由 `docs/architecture.md` 定义，目录归属由 `docs/file-structure.md` 定义，代码与测试要求由 `docs/engineering-standards.md` 定义。目录只在对应 change 实现真实行为时创建。

当前 module 包含协议/fixture 校验、listener-independent codec、唯一 `cmd/server`、Composition Root、独立诊断 listener、必需 MySQL/Redis storage runtime，以及 transport-independent session、account、PersonalWorld 和 WorldInstance placement core。Session core 已实现 opaque access/refresh token、原子轮换契约、带 16-byte nonce 的结构化 connection ticket、构造入口封闭的 AuthContext 和 epoch 失效语义；account core 已实现 username/display name 规范化、凭据边界、账号原子 repository 契约以及 register/login 编排。这些能力尚未接入完整的 production store、连接 registry 或公开业务 listener，因此当前进程不会开放登录、刷新、PersonalWorld 或 realtime 业务 API。

Session core 位于 `internal/session/`。生产代码只定义消费侧接口和安全状态编排，复用 Composition Root 的 `crypto/rand` ID generator，并由 `SecretGenerator` 生成 token/nonce；并发内存 store、fake clock、确定性 generator 和 fake invalidator 只存在于 `_test.go`。后续 Redis 与 transport adapter 必须实现这些接口，不能另建 token、ticket 或 epoch 语义。

Account core 位于 `internal/account/`。Username 使用受限 ASCII lowercase canonical key，Unicode 展示需求由经过 NFC 和安全字符校验的 display name 承担；password 保持原始 bytes，只能进入 `CredentialHasher`，repository 只接收自描述 `CredentialHash`。Register 先原子提交账号再创建 session，后者失败不会删除账号；login 对 unknown username 使用同算法、同成本 dummy hash，并将 unknown、wrong password 与 inactive account 收敛为同一外部错误。Refresh、logout、ticket 和 epoch 始终由 `internal/session` 拥有。

Account production package 只定义消费侧接口，reference adapters 仅存在于 `_test.go`。后续 Account storage adapter change 必须实现 MySQL repository，并在目标硬件 benchmark 后选择 production memory-hard password hasher；在这两项完成前，Composition Root 不得接线 account service，也不得宣称 register/login 可用。

PersonalWorld core 位于 `internal/personalworld/`。它建立独立 `PersonalWorldID`、不可变 `account.PlayerID` owner、primary world 原子 ensure、严格 snapshot hydration、持久 revision、`active -> archived` 生命周期，以及带 expected revision、Owner-scoped idempotency fingerprint 和 commit-unknown 的归档契约。并发 reference repository、fake clock/ID generator 与故障注入只存在于 `_test.go`，生产 package 不提供 memory fallback。

WorldInstance placement core 位于 `internal/placement/`。它建立独立 `WorldInstanceID`、受信 `RuntimeNodeID`、单调 assignment generation/fencing token、`starting -> active` 发布、lease renew/write qualification，以及 revoke-before-stop、break-before-make 的休眠、重建和迁移编排。所有 store 条件操作绑定完整 assignment stamp；并发 reference store、fake runtime/clock/ID 与故障注入只存在于 `_test.go`，生产 package 不提供 memory fallback。

PersonalWorld 与 placement core 尚未接入 production MySQL/Redis adapter、Composition Root、协议、listener 或 Unity。它们不包含地图、任务、奖励、Visitor、connection presence 或 endpoint；这些能力必须继续遵守 `docs/roadmap.md` 的进入条件，由后续独立 change 实现和验收。

Storage runtime 位于 `internal/storage/`。MySQL component 拥有唯一 pool、UTC/strict session、advisory-lock migration 和一次性 transaction callback；Redis component 固定 standalone，禁用 mutation 隐式 retry，并拥有 Keyspace/registry/TTL policy。D0 只创建 `ih_schema_migrations`，尚未实现 Account/Session/PersonalWorld/Placement adapter 或业务 table。

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

Storage contract 与真实 Docker integration 入口：

```powershell
& .\tools\storage\storage.ps1 -Action contract
& .\tools\storage\storage.ps1 -Action verify -TimeoutSeconds 600
```

`verify` 使用 `versions.yaml` 锁定的 tag + linux/amd64 digest，覆盖空库/并发 migration、dirty/checksum/lock 拒绝、MySQL/Redis restart、Redis flush、依赖持续中断、commit-unknown、进程恢复与 ownership cleanup。所有状态和 secret 只存在于已忽略的 `.local/storage/<run-id>/`。

`TimeoutSeconds` 同时约束 setup/tests 与每次 Docker CLI 调用；显式 `down` 也使用同一总预算。`verify` 的 `finally` cleanup 另有最多 60 秒预算，避免主阶段耗尽 deadline 后跳过回收。若 Docker daemon 失联使 ownership 无法确认，入口会终止挂起的 `docker.exe`、保留 `.local/storage/<run-id>/state.json` 并返回非零；Docker 恢复后使用日志中的 RunId 执行 `-Action down -RunId <run-id>`，不得手工删除未知 Docker resource。

## 本地启动

从仓库根目录执行：

```powershell
& .\tools\proto\proto.ps1 generate
& .\tools\storage\storage.ps1 -Action up
$runId = "<上一步输出的 RunId>"
try {
    $state = & .\tools\storage\storage.ps1 -Action status -RunId $runId | ConvertFrom-Json
    $env:IHOMELAND_MYSQL_ADDRESS = "127.0.0.1:$($state.mysqlPort)"
    $env:IHOMELAND_REDIS_ADDRESS = "127.0.0.1:$($state.redisPort)"
    $env:IHOMELAND_MYSQL_PASSWORD = [IO.File]::ReadAllText(".local/storage/$runId/mysql-password")
    $env:IHOMELAND_REDIS_PASSWORD = [IO.File]::ReadAllText(".local/storage/$runId/redis-password")
    & .\tools\go\go.ps1 run ./cmd/server --config config/local.yaml
} finally {
    & .\tools\storage\storage.ps1 -Action down -RunId $runId
    Remove-Item Env:IHOMELAND_MYSQL_ADDRESS, Env:IHOMELAND_REDIS_ADDRESS, Env:IHOMELAND_MYSQL_PASSWORD, Env:IHOMELAND_REDIS_PASSWORD -ErrorAction SilentlyContinue
}
```

`--config` 必须显式提供。配置优先级固定为安全默认值、YAML 文件、白名单 `IHOMELAND_` 环境覆盖；未知字段、非法覆盖、缺失 secret 或 production plaintext storage 会在创建 logger、listener、client 和 goroutine 前失败。仓库只提交包含 `env:` reference 的非敏感 `server/config/local.yaml`，进程不会自动读取 `.env`。本地 `up` 使用随机固定端口，需把 manifest 的 `mysqlPort`/`redisPort` 映射到 `IHOMELAND_MYSQL_ADDRESS`/`IHOMELAND_REDIS_ADDRESS`，并将两个 ignored password 文件内容仅注入当前进程环境；不得复制回 YAML。

推荐诊断默认地址为 `127.0.0.1:8081`：

- `/healthz`：进程存活，不代表可以接收业务
- `/readyz`：仅 diagnostic、MySQL migration/pool 与 Redis 全部 ready 时返回 200；不表示 register/login/world API 已开放
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
