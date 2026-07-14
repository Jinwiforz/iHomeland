## Context

服务端已经拥有唯一 Composition Root、严格配置、lifecycle/readiness/task supervision，以及 Account、Session、PersonalWorld、Placement 四个稳定消费侧接口，但 production graph 仍只启动诊断 listener。各领域 change 有意没有决定 MySQL driver、Redis client、migration、secret、重试或 Docker 语义；如果下一步直接编写业务 adapter，这些基础行为会在多个 repository/store 中重复且难以统一验收。

路线图 D0 要先提供受控 MySQL/Redis runtime，再由 W2 和后续 Account/Session changes 实现业务 schema 与 adapter。本 change 因而会改变正式服务端启动条件：配置的 MySQL/Redis 都是必需资源，任一不可用时不进入 ready；但 ready 仍只表示已配置的服务端组件可服务，不代表尚未接线的业务 API 已存在。

## Goals / Non-Goals

**Goals:**

- 建立 MySQL pool、migration、transaction/error policy 和 Redis client、key/schema/TTL registry 的唯一 production owner。
- 在创建资源前完整校验非敏感配置与 secret reference，production 强制 storage TLS，所有默认日志和 metrics 脱敏。
- 让 storage 资源遵循现有 lifecycle、readiness、task supervision、startup rollback 与总 shutdown deadline。
- 提供空库、并发启动、dirty migration、Redis flush、依赖中断/重启和清理可重复的 Docker integration harness。
- 给后续业务 adapter 提供 concrete client/resource，而不反向拥有其 repository/cache interface 或业务 transaction。

**Non-Goals:**

- 不实现 AccountRepository、CredentialHasher、SessionStore、PersonalWorldRepository、PlacementStore、VisitSession、outbox 或任何业务 table/Lua script。
- 不实现 MySQL replica/read split、sharding、Vitess、Redis Cluster/Sentinel、跨区域复制、在线 schema change 或自动 failover orchestration。
- 不开放 HTTP/WSS/TCP 业务 listener，不修改协议/registry，不接入 Unity，也不宣称 server v1 已完成。
- 不提供 production memory fallback、generic `map[string]any` repository、全局 service locator 或自动重放未知业务 transaction。

## Decisions

### 1. Storage runtime 位于 infrastructure，并消费业务 owner 的接口而不拥有它们

新增 `internal/storage/mysql` 与 `internal/storage/redis`。前者拥有 `database/sql` pool、migration 和 transaction runner，后者拥有 concrete Redis client、keyspace/registry 与 probe；业务 adapter 后续仍放在 infrastructure 下，但必须显式实现 `account`、`session`、`personalworld` 或 `placement` 定义的接口。`internal/app` 只负责构造 concrete graph，不向 domain/application 暴露 driver type。

MySQL 使用标准 `database/sql` 配合锁定的 `github.com/go-sql-driver/mysql`；Redis 使用锁定的 `github.com/redis/go-redis/v9`。精确 module version 与 checksum 进入 `go.mod/go.sum`，作为共享运行基础的直接依赖同时登记到 `versions.yaml`，不依赖系统全局安装或浮动版本。

相比创建通用 `StorageManager` 或让各 adapter 自建 client，该布局有明确资源 owner、唯一 pool/client 和可验证关闭顺序，也保留消费侧 interface 原则。

### 2. 非敏感配置与 secret material 分离

`config.Config` 增加 MySQL/Redis endpoint、database、username、pool、timeout、probe 和 TLS policy；这些值可安全验证和有限记录。Password、TLS private key/credential 内容由注入 `SecretProvider` 按固定 reference 读取，production provider 只接受白名单环境变量或明确文件来源，不自动加载 `.env`。Secret 使用不可默认展开的值类型并只在构造 DSN/TLS config 的最短生命周期内暴露 bytes/string。

所有配置和 secret reference 在 `Run` 创建 logger/listener/client 前解析。`local`/`test` 允许 loopback Docker plaintext；`production` 必须启用 CA 与 server-name 验证，禁止 `skipVerify`。错误只标识配置键或 secret reference，DSN、password 和 key content 不进入 error chain 的安全外层、日志或 metrics。

相比把 password 写入 YAML 或通用环境反射绑定，独立 provider 可以测试缺失/读取失败而不让 Config、日志序列化或未知 `IHOMELAND_` 键扩散 secret。

### 3. MySQL component 在 Start 中完成 pool 验证与 migration

MySQL component 构造阶段只创建纯内存配置；`Start` 才创建 pool，设置 open/idle/lifetime、connect/read/write timeout、UTC 和 TLS，并在 startup context 下执行 ping、database/version、`@@session.time_zone` 与 strict SQL mode 验证。Migration 是 MySQL component Start 的内部阶段，不注册成独立 lifecycle component，因为 migrator不持有启动成功后的长期资源；只有 migration 完成，pool 才进入 component 所有权并可供后续 graph 消费。

`Stop` 标记 component closing、停止 probe owner task，再关闭 pool。当前 change 没有业务消费者，因此 graph 顺序为 diagnostic -> MySQL -> Redis；未来 adapters/services/listeners 插入其后，关闭自然变为 consumers -> Redis -> MySQL -> diagnostic。

相比在 `Run` 中散落 `Ping`/`Close` 或启动后异步 migration，该设计确保 ready 前 schema 已确定且失败由既有 started stack 精确回滚。

### 4. Migration catalog 使用单 statement unit、checksum history 与 dirty fail-closed

Migration 文件嵌入二进制并使用固定宽度递增版本、稳定名称与 `.sql` 后缀；`.gitattributes` 固定 SQL 为 LF，每个文件只允许一个 statement，禁止 driver `multiStatements` 和运行时从磁盘读取任意 SQL。Migrator 先用专用 `*sql.Conn` 获取命名 advisory lock，再创建/验证带中文 table/column 注释的 `ih_schema_migrations`。History 记录 version、name、SHA-256、`in_progress|applied`、started/applied UTC time，时间注释明确 UTC 微秒精度。

执行顺序是：验证完整 catalog/history -> 写入并提交 `in_progress` -> 执行一个 statement -> 标记 `applied`。MySQL DDL 不提供可依赖的跨 statement transaction rollback；若断线发生在 statement 与 history 更新之间，记录保持 dirty，下一次启动 fail closed。修复必须由独立、可审计命令在人工确认数据库实际状态后完成，本 change 不根据“table exists”等启发式自动猜测。

已合并 migration 永不修改。应用回滚优先保持 forward-compatible additive schema；自动 down migration 被拒绝，因为它可能在旧/新进程并存时破坏数据。需要 destructive rollback 时由独立 OpenSpec change 提供备份、兼容窗口、显式 forward repair 与回滚脚本。D0 自身只创建 migration history，移除 runtime wiring 时该空元数据表可安全保留。

### 5. Transaction runner 管理资源，不替业务决定是否重试

MySQL package 提供窄 `WithinTx(ctx, options, callback)`：创建 transaction、执行一次 callback、按结果 commit/rollback，并用 `errors.Join` 保留 cleanup failure。它不捕获 domain error、不自动重放 callback，也不把 context cancellation、driver bad connection 或 commit error解释为未提交。

共享 retry classifier 只区分明确 pre-commit transient、not-committed 与 commit-unknown，并生成有界 backoff 建议；只有业务 owner 能证明 read-only/idempotent 且传入稳定 identity 时才显式重试。Unique、revision、idempotency 与 fencing conflict 必须由具体 adapter 翻译为消费接口 outcome。

相比通用 transaction retry middleware，这避免在响应丢失时重复创建账号、推进 revision 或发放新 fencing token。

### 6. Redis v1 固定 standalone，Keyspace 不把占位花括号当 hash tag

本阶段使用单 endpoint standalone Redis，不创建 `ClusterClient` 或 Sentinel client。`docs/redis-keys.md` 中 `ih:{env}:...` 的花括号继续只表示文档占位；真实 key 为 `ih:<env>:<owner>:<kind>:<identity...>`，builder 拒绝冒号、花括号、控制字符、空值和超长 segment。未来启用 Cluster 必须先由独立 change 根据 Session/Placement 的真实 Lua 与 lookup key 重新设计同 slot strategy，不能把整个 environment 放进一个热点 hash tag。

Key registry 是不可变 metadata，而不是 generic cache API。每个 definition 固定 owner、kind、用途、TTL policy、schema version、最大 encoded bytes、恢复、cleanup、failure 和 metrics；owner-specific adapter 后续提供 typed value codec 与原子 script，并在 `docs/redis-keys.md` 维护英文字段名、类型/编码、中文短注释与必填规则。日志只使用 definition name/owner，不调用完整 key 的默认格式化。

相比现在预造所有业务 Lua/value struct，这个边界完成 namespace、大小、TTL 和安全治理，又不在没有 adapter contract tests 时发明存储投影。

### 7. Redis 禁止隐式重放 state-changing operation

Redis component 在 Start 中验证 endpoint、TLS、认证、选择的 database 和 standalone mode，并显式配置 pool/command timeout。Client-level retry 对 mutation 默认关闭；只读 probe/read 可以由调用边界使用有界 retry。未来多 key mutation 必须由 owner 提供单个 Lua/transaction、稳定输入 identity 和 outcome parser，连接中断后返回 commit-unknown，由 owner resolve/retry。

Generic classifier 只有在单个 built-in command 收到明确 server rejection 时才能判定 not-applied。Lua/transaction 的运行时错误不会回滚此前已执行写入，即使错误来自 Redis server 也必须默认保持 commit-unknown；只有 owner-specific outcome parser 能依据稳定 identity 进一步收窄。

TTL helper 接受 absolute expiry/当前受信时间并拒绝非正 duration；registry 未声明主动清理的 key 必须有 TTL。Unknown version、oversize 或 decode failure 一律当 dependency/data defect fail closed。Redis flush 后 runtime 只观察空状态，不从本地 memory 恢复旧资格；Session、Placement、Visit 的具体安全结果由各 adapter change 实现。

### 8. Required probe 通过现有 TaskGroup 触发单向 draining

MySQL/Redis Start 各自完成一次 startup probe。成功后 component 可向自己的 TaskGroup owner 注册周期 probe；配置固定 interval、单次 timeout 和连续失败阈值，label 只使用 `mysql|redis` 与有界 outcome。阈值内 transient failure 记录 warning 并允许 driver 重新连；达到阈值返回 task error，由既有 `Run` select 进入 draining 和非零关闭。

不扩展 readiness 为可来回切换的 dependency state；现有 `starting -> ready -> draining -> stopped` 保持不变量。恢复路径是依赖恢复后由部署/开发 harness 重启进程并重新执行完整 startup/migration，而不是在部分业务可能已失败时把同一进程重新标 ready。

### 9. Docker harness 是唯一 storage integration 入口

新增 `tools/storage/storage.ps1`，从 `versions.yaml` 读取 MySQL/Redis tag 与 image digest，拒绝 `latest` 或未锁定 image。`verify` 为每次运行生成 GUID 前缀、loopback 随机 host port、network/volume/container 名和高熵 password，把临时配置/secret 放在 `.local/storage/<run-id>`；它等待容器健康后通过项目 Go 入口运行带明确 build tag/package 的 integration/recovery tests，并在 `finally` 中只清理该 run-id 资源。

入口提供非交互 `verify` 和有界 timeout；每次 Docker CLI 调用都由显式 native-process runner 消费当前阶段剩余 deadline，deadline 到达或脚本取消时终止对应子进程，不允许 `docker.exe` 绕过全局预算永久等待。`finally` cleanup 使用独立且更短的 deadline，避免主阶段耗尽预算后无法尝试回收。若 daemon 失联导致 ownership 无法确认，cleanup fail closed 并保留 `.local/storage/<run-id>` state，待 Docker 恢复后由显式 `down` 完成；它不会为了表面成功而跳过 label 校验或删除未知资源。

若为手工诊断提供 `up/down`，则 state manifest 也只存在 `.local` 且 `down` 必须先验证所有资源 label/run-id。CI 使用相同入口。镜像 tag 之外记录 digest，避免 registry tag 漂移让历史验证不可重复。

相比提交固定 compose password/port 或测试直接操作开发者已有数据库，该 harness 隔离并发运行，能安全注入 flush/restart/kill，且不会误删不属于自己的资源。

### 10. D0 只接线基础资源，不提前创建业务可用假象

Composition Root 在本 change 后会要求 storage ready，但不会构造 account/session/personalworld/placement service，因为它们的 concrete adapters 仍不存在。Diagnostic `/readyz` 表示当前已配置 graph（诊断 + storage）启动成功；README 必须明确它不表示 register/login/world API 已开放。

不创建空 repository、memory fallback、placeholder handler、业务 table 或万能 Redis value。W2 负责 PersonalWorld schema、placement high-watermark 与 Redis adapter；Account/Session production adapters 也必须通过各自独立 change 和 contract/integration tests 接入。

## Risks / Trade-offs

- [MySQL DDL 在 history 更新前后中断会留下不确定状态] → 先写 `in_progress`、每个 migration 限制为单 statement、dirty 启动 fail closed，并要求显式审计修复而不是自动猜测。
- [Storage 成为必需资源后本地 `cmd/server` 不能脱离可达的 MySQL/Redis 启动] → 提供基于 Docker 的统一本地 harness、loopback 随机端口和清晰 bootstrap/cleanup 文档；部署环境仍可使用满足配置与 TLS policy 的外部服务，禁止 optional memory mode 掩盖生产依赖。
- [Required probe 对短暂网络抖动过敏] → 配置单次 timeout 与连续失败阈值，阈值内允许 driver reconnect；达到阈值后保持单向 draining，避免带未知依赖状态继续 ready。
- [Standalone Redis 限制未来横向扩展] → 第一里程碑优先原子语义清晰；Cluster/Sentinel 必须根据真实 scripts、slot 和故障转移证据独立设计。
- [统一 key registry 可能被误用为业务 schema owner] → Registry 只保存 metadata/validation；typed value、script、recovery 与 outcome 始终由消费 adapter owner 定义。
- [镜像 tag 与 digest/多架构 manifest 可能变化] → `versions.yaml` 同时锁定 tag 和目标平台 digest，工具入口在启动前验证，升级走依赖维护与 integration tests。
- [Docker daemon 失联时 cleanup 无法确认资源状态] → 终止挂起 CLI 并让 cleanup 自身有界；保留 ignored state 与稳定 run-id，恢复后显式 `down`，不把无法确认误报为已清理。
- [Driver 底层错误可能包含 endpoint/query/key] → 对外与默认日志只使用稳定分类；原始 error 仅保留在受控 chain，不作为 metrics label 或普通响应。

## Migration Plan

1. 增加锁定 driver 依赖、storage config/secret 类型与纯验证测试，不创建资源。
2. 实现 MySQL pool、migration catalog/history/lock、transaction classifier 与 Docker MySQL integration tests。
3. 实现 Redis client、standalone validation、Keyspace/registry/TTL policy 与 Redis flush/restart integration tests。
4. 将 diagnostic、MySQL、Redis 按顺序接入 Composition Root，补齐 startup rollback、probe fatal、shutdown/process tests 和低基数 observability。
5. 更新 `versions.yaml` image digest、Redis key registry、存储 schema 注释规范、目录/README/本地命令，执行 format、unit/contract/integration/fuzz/race、dependency restart、OpenSpec strict 与仓库卫生验证。

回滚应用时移除 storage graph/config 并回到上一二进制；D0 不创建业务数据表，`ih_schema_migrations` 元数据表可以保留。已执行 migration 不自动 down 或修改；若 implementation 引入无法由旧二进制忽略的 schema，必须在合并前新增兼容 migration 和独立回滚说明。

## Open Questions

无。driver/module 版本与 image digest 已记录在 `versions.yaml`，pool、probe 和 Docker timeout 默认值已由配置、文档及 contract/integration tests 固定；后续调整继续遵守版本治理并通过独立 change 评估行为影响。
