# iHomeland Server

`server/` 是 Go 服务端 module 根目录。第一阶段目标是由 Go 协议测试客户端独立验收的账号、统一会话、PersonalWorld、WorldInstance 和 VisitSession v1；ActivityInstance、Room、Party、UDP/KCP、battle server、匹配、观战和回放不在当前边界内。

服务端严格按 `docs/roadmap.md` 的基础能力、个人世界、访客联机、公开通道和资格验收顺序实现。架构依赖由 `docs/architecture.md` 定义，目录归属由 `docs/file-structure.md` 定义，代码与测试要求由 `docs/engineering-standards.md` 定义。目录只在对应 change 实现真实行为时创建。

当前 module 包含 world/visit Protobuf 与 HTTPS/WSS/TLS-TCP registry/fixture 契约、协议校验、唯一 `cmd/server`、Composition Root、独立诊断 listener、必需 MySQL/Redis storage runtime，以及 transport-independent session、account、PersonalWorld、WorldInstance placement、VisitSession 和 WorldAdmission core。公开 component 已用 production adapters 接线 10 个冻结 HTTP operation、认证 WSS control 和独立 gameplay TCP listener；TCP 使用固定 preface 依次消费一次性 GAMEPLAY ticket 与 WorldAdmission，并以有界 registry/queue/dispatcher 接入冻结的 world/visit route。完整 producer、cleanup/orchestration 与 Go 协议资格客户端仍未实现。

Session core 位于 `internal/session/`。生产代码只定义消费侧接口和安全状态编排，复用 Composition Root 的 `crypto/rand` ID generator，并由 `SecretGenerator` 生成 token/nonce；并发内存 store、fake clock、确定性 generator 和 fake invalidator 只存在于 `_test.go`。`internal/storage/session` 已用共享 Redis client、严格 key/Hash codec 与原子 Lua scripts 实现 `SessionStore`；后续 transport adapter 不能另建 token、ticket 或 epoch 语义。

Account core 位于 `internal/account/`。Username 使用受限 ASCII lowercase canonical key，Unicode 展示需求由经过 NFC 和安全字符校验的 display name 承担；password 保持原始 bytes，只能进入 `CredentialHasher`，repository 只接收自描述 `CredentialHash`。Register 先原子提交账号再创建 session，后者失败不会删除账号；login 对 unknown username 使用同算法、同成本 dummy hash，并将 unknown、wrong password 与 inactive account 收敛为同一外部错误。Refresh、logout、ticket 和 epoch 始终由 `internal/session` 拥有。

Account production package 只定义消费侧接口，reference adapters 仅存在于 `_test.go`。`internal/storage/account` 已借用共享 MySQL pool 实现单表 repository，并以固定 Argon2id v19 profile、严格 PHC parser 和有界并发实现 production hasher。正式 Composition Root 已将其接入公开 register/login；transport 不得建立第二套账号或凭据语义。

PersonalWorld core 位于 `internal/personalworld/`。它建立独立 `PersonalWorldID`、不可变 `account.PlayerID` owner、primary world 原子 ensure、严格 snapshot hydration、持久 revision、`active -> archived` 生命周期，以及带 expected revision、Owner-scoped idempotency fingerprint 和 commit-unknown 的归档契约。并发 reference repository、fake clock/ID generator 与故障注入只存在于 `_test.go`，生产 package 不提供 memory fallback。

WorldInstance placement core 位于 `internal/placement/`。它建立独立 `WorldInstanceID`、受信 `RuntimeNodeID`、单调 assignment generation/fencing token、`starting -> active` 发布、lease renew/write qualification，以及 revoke-before-stop、break-before-make 的休眠、重建和迁移编排。所有 store 条件操作绑定完整 assignment stamp；并发 reference store、fake runtime/clock/ID 与故障注入只存在于 `_test.go`，生产 package 不提供 memory fallback。

VisitSession core 位于 `internal/visitsession/`。它建立独立且默认脱敏的 session/invite/command/connection-binding identities，不可变绑定 Owner、PersonalWorld 与完整 current assignment stamp，并实现有界 invite、reservation、join、leave/kick、Owner/Visitor disconnect/reconnect/expiry、expected revision、command replay/commit-unknown 和确定性 safe-return。Invite 与 `AdmissionIntent` 都不是 gameplay credential；Join 与 VisitorReconnect 都要求由 worldadmission verifier hydration 的 purpose-scoped qualification，并继续二次验证 membership、lineage、deadline 与 current assignment。Visitor 对未登记 gameplay mutation 默认没有权限。

VisitSession 的并发 reference store 与 fake reader/clock/ID 只存在于 `_test.go`。`internal/storage/visitsession` 已用共享 standalone Redis client、versioned active/session/command schemas、完整 snapshot/result codec 与 owner Lua scripts 实现 production `VisitSessionStore`。Redis 进程重启只能读取自身仍保留的合法运行态；flush 或 key 丢失后旧 invite、membership、binding 与 replay 不从其他来源补回。Adapter 不拥有 client、timer/goroutine、admission credential、generated protocol、listener 或 memory fallback；正式 Composition Root 为 HTTP accept/admission 与 TLS/TCP snapshot/mutation 构造同一个 service，但尚不运行 semantic cleanup 或完整 producer 编排。

World admission runtime 位于 `internal/worldadmission/`。它用注入的至少 256-bit key、稳定 issuance ID 与完整 binding fingerprint 确定性派生短期 opaque credential；`internal/storage/worldadmission` 只保存 digest、受信 binding 与 consume tombstone，并用 owner Lua 原子处理幂等签发、精确 response-loss 重试和重放拒绝。Verifier 比较 AuthContext、purpose、TLS/TCP endpoint/channel 后烧毁 credential，再复核 current full assignment；Visitor qualification 仍交给 VisitSession 二次验证。正式 Composition Root 已接线 HTTP 签发与 TCP consume；这只证明 transport capability，不表示完整 visit-world producer 与流程已经可用。

PersonalWorld 与 placement core 已有独立 production storage adapter：`internal/storage/personalworld`
借用共享 MySQL pool 保存 identity、immutable owner、lifecycle、revision 与 archive replay；
`internal/storage/placement` 由 MySQL allocation ledger 永久推进 generation/fence，再由 Redis
Lua 管理可失效 current assignment 与有界 transition replay。重试只复用稳定 world/instance/node
identity，首次 allocation 的时间结果不会被新时钟覆盖。Redis flush 不恢复旧 lease，旧
allocation 也不会重新发布；新 candidate 必须取得更高 fence。Adapter 不持有共享 client、
不注册独立 lifecycle component，由公开 HTTP graph 借用共享资源并按 owner 接口组合。
当前进程已开放 world bootstrap、invite accept 与 admission issue，但不包含地图、任务、奖励、
connection presence 或 realtime listener；这些能力必须继续遵守 `docs/roadmap.md` 的进入条件，由后续独立 change 实现和验收。

Storage runtime 位于 `internal/storage/`。MySQL component 拥有唯一 pool、UTC/strict session、advisory-lock migration 和一次性 transaction callback；Redis component 固定 standalone，禁用 mutation 隐式 retry，并拥有 Keyspace/registry/TTL policy。根 `storage` package 只组合 Session、Placement、VisitSession 与 WorldAdmission 已实现的 Redis definitions，不拥有 client 或业务 service。Migration catalog 除 `ih_schema_migrations` 外已创建单张 `accounts`、`personal_worlds`、`personal_world_idempotency`、`placement_sequences` 与 append-only `placement_allocations`；table owner 分别是 Account 与 PersonalWorld/Placement adapters。正式 Composition Root 只借用共享 pool/client 构造公开 HTTP 所需 adapters，不创建第二套连接或 memory fallback。

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

`verify` 使用 `versions.yaml` 锁定的 tag + linux/amd64 digest，覆盖空库/并发 migration、dirty/checksum/lock 拒绝、MySQL/Redis restart、Redis flush、依赖持续中断、commit-unknown、真实 WSS/TCP ticket 消费、TCP admission 与 `OWN_WORLD`/`JOIN`/`RECONNECT` wire、重放、logout 跨通道 invalidation、进程恢复与 ownership cleanup。所有状态和 secret 只存在于已忽略的 `.local/storage/<run-id>/`。

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
    $env:IHOMELAND_WORLD_ADMISSION_KEY = "replace-with-local-random-material-at-least-32-bytes"
    & .\tools\go\go.ps1 run ./cmd/server --config config/local.yaml
} finally {
    & .\tools\storage\storage.ps1 -Action down -RunId $runId
    Remove-Item Env:IHOMELAND_MYSQL_ADDRESS, Env:IHOMELAND_REDIS_ADDRESS, Env:IHOMELAND_MYSQL_PASSWORD, Env:IHOMELAND_REDIS_PASSWORD, Env:IHOMELAND_WORLD_ADMISSION_KEY -ErrorAction SilentlyContinue
}
```

`--config` 必须显式提供。配置优先级固定为安全默认值、YAML 文件、白名单 `IHOMELAND_` 环境覆盖；未知字段、非法覆盖、缺失 secret 或 production plaintext storage 会在创建 logger、listener、client 和 goroutine 前失败。仓库只提交包含 `env:` reference 的非敏感 `server/config/local.yaml`，进程不会自动读取 `.env`。本地 `up` 使用随机固定端口，需把 manifest 的 `mysqlPort`/`redisPort` 映射到 `IHOMELAND_MYSQL_ADDRESS`/`IHOMELAND_REDIS_ADDRESS`，并将两个 ignored password 文件内容仅注入当前进程环境；不得复制回 YAML。

推荐诊断默认地址为 `127.0.0.1:8081`：

- `/healthz`：进程存活，不代表可以接收业务
- `/readyz`：diagnostic、MySQL、Redis、公开 HTTP/WSS 与独立 gameplay TCP listener 全部 ready 后返回 200
- `/version`：有界构建身份
- `/metrics`：低基数 Prometheus/OpenMetrics 指标

该 listener 不承载账号、个人世界或其他公开业务 API。绑定非 loopback 地址时必须使用部署网络策略限制访问。

公开本地入口默认监听 `127.0.0.1:8080`：普通请求只承载 `shared/contracts/http/v1/openapi.yaml` 登记的 10 个 operation，精确 `/v1/control` 承载 WSS upgrade；gameplay TCP 独立监听 `127.0.0.1:8444`。客户端始终使用 `endpoints.wss` 与 `endpoints.tlsTcp` 的 advertised 值，二者可因 ingress/port mapping 与 bind 不同但必须显式配置。Production 必须启用 TLS 1.3，并通过 secret reference 提供 private key；本地明文 TCP 只接受 loopback remote。资源与 preface 语义由 `docs/network-transport-architecture.md` 统一维护。

推荐值被占用时，通过所选配置文件或白名单环境变量 `IHOMELAND_DIAGNOSTIC_ADDRESS`、`IHOMELAND_PUBLIC_ADDRESS` 显式覆盖对应 listener。进程不会静默寻找其他端口；绑定冲突会在启动阶段返回非零结果。完整规划见 `docs/network-port-allocation.md`。

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
