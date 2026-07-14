# Server Storage Runtime 规格

## Purpose

定义服务端 MySQL/Redis 基础资源、secret、migration、transaction、Redis key/TTL、lifecycle、可观测与 Docker integration 的长期行为边界。

## Requirements

### Requirement: Storage 配置与 secret 必须在产生副作用前完整解析
Storage runtime MUST 使用类型化配置表达 MySQL/Redis endpoint、database、pool、timeout、probe 与 TLS policy，并在创建 client、连接、文件或 goroutine 前完成交叉校验。密码、TLS private key 和其他 secret MUST 由独立白名单 SecretProvider 提供，MUST NOT 进入普通 Config 快照、YAML、命令行、日志、metrics、错误文本或默认格式化；production 环境 MUST 为 MySQL 与 Redis 启用经过 CA 和 server name 校验的 TLS。

#### Scenario: 必需 storage secret 缺失
- **WHEN** production 配置引用的 MySQL 或 Redis secret 无法由 SecretProvider 解析
- **THEN** 服务端在创建 storage client 或 diagnostic listener 前以配置/启动错误失败，诊断只包含稳定 secret 键名而不包含值

#### Scenario: Production 禁用 storage TLS
- **WHEN** environment 为 production 且 MySQL 或 Redis 配置允许明文连接、跳过证书验证或缺少 server name
- **THEN** 配置校验 fail closed，进程不尝试网络连接也不进入 ready

### Requirement: MySQL runtime 必须拥有有界且可诊断的生产 pool
MySQL component MUST 使用受治理 driver 创建唯一共享 `database/sql` pool，固定 UTC、严格 SQL mode、连接/读写 timeout、最大 open/idle/lifetime 与 TLS policy。Start MUST 在 startup deadline 内建立并验证连接与 session invariants，成功后才转移资源所有权；Stop MUST 在共享 shutdown deadline 内拒绝新基础设施工作并关闭 pool。连接失败、session 配置不一致或关闭失败 MUST 保留稳定 operation/component 分类且不得泄露 DSN 或凭据。

#### Scenario: MySQL 正常启动和关闭
- **WHEN** 配置有效且目标 MySQL 满足版本、TLS 与 session invariant
- **THEN** component 在 deadline 内完成 ping/验证并暴露唯一 pool，关闭时释放全部 idle connection 且不遗留后台任务

#### Scenario: MySQL session invariant 不满足
- **WHEN** 连接建立但 UTC、strict SQL mode 或目标 database 校验失败
- **THEN** Start 关闭局部 pool、返回启动失败且不把该 component 登记为已启动

### Requirement: Migration 必须有序、不可变、互斥且对不确定状态 fail closed
Migrator MUST 从嵌入二进制的不可变顺序 migration catalog 初始化空数据库，并在同一专用 MySQL connection 上取得有界 advisory lock。History MUST 记录 version、稳定名称、SHA-256 checksum、状态和 applied time；全部受管 MySQL table/column MUST 提供中文短注释，时间、时长、容量与速率 MUST 标明真实时区、精度或单位。Migration SQL MUST 固定 LF，已应用 version 的名称或 checksum 不匹配、version 缺口、重复 version、dirty/in-progress 记录或 lock 超时 MUST 阻止服务进入 ready。每个 migration unit MUST 只有一个可独立执行的 statement，执行前记录 in-progress、成功后标记 applied，使 MySQL DDL 无法事务回滚时的中断保持可诊断而不被自动猜测为成功。

#### Scenario: 空库首次 migration
- **WHEN** MySQL database 不包含 history table 且 catalog 合法
- **THEN** migrator 创建带中文 table/column 注释的 history、按严格 version 顺序执行全部 migration 并记录 checksum，重复运行不再次执行已应用 statement

#### Scenario: 两个进程并发 migration
- **WHEN** 两个服务端实例同时对同一 database 启动 migrator
- **THEN** 只有持有 advisory lock 的实例执行 catalog，另一实例有界等待后验证相同 history 或因 timeout 失败，不会并发执行 DDL

#### Scenario: Migration 中断留下 dirty 记录
- **WHEN** statement 执行期间连接丢失或进程终止，history 无法确认 applied
- **THEN** 后续启动拒绝自动继续或回滚，报告 version 与 dirty 状态并要求显式修复流程，不把 database 宣称为 ready

### Requirement: MySQL transaction 与 retry policy 必须保留提交不确定性
Storage runtime MUST 提供窄 transaction runner 统一 BeginTx、callback、commit、rollback 与 error joining，但 MUST NOT 自动重放业务 callback。只有明确发生在 transaction 开始或 statement 提交之前、且操作被 owner 证明为只读或幂等的 transient failure 才能按有界 backoff 重试；context cancellation、连接断开、commit error 或 timeout MUST NOT 被解释为未提交。具体 repository MUST 继续返回其 owner 定义的 conflict、not-committed 或 commit-unknown 结果。

#### Scenario: Commit 响应丢失
- **WHEN** MySQL 在接收 commit 后连接断开且 driver 无法证明 transaction 是否提交
- **THEN** transaction runner 返回 commit-unknown 分类，不调用 callback 第二次，也不把 rollback 结果当作未提交证明

#### Scenario: Callback 返回业务冲突
- **WHEN** repository callback 返回 revision、unique 或 idempotency conflict
- **THEN** runner 尝试 rollback 并保留原业务错误，不把冲突包装成可重试 dependency failure

### Requirement: Redis keyspace 与 schema registry 必须集中、受 owner 约束且无敏感材料
Redis runtime MUST 使用集中 Keyspace 从经过校验的 environment、owner、kind 与 identity segment 构造 `ih:<env>:<owner>:<kind>:<identity...>`，拒绝空值、分隔符注入、超长或未知 owner/kind。Registry MUST 为每个允许 key pattern 记录唯一 owner、用途、TTL policy、value schema/version、最大 encoded size、恢复来源、清理触发、故障行为与低基数 metrics。Redis 没有原生 COMMENT；每个已实现 value schema MUST 在 `docs/redis-keys.md` 维护英文字段名、类型/编码、中文短注释与必填规则。Raw token、ticket nonce、密码、username、完整 key/value 或未经 owner 处理的 payload identity MUST NOT 进入 key、日志或 metrics。服务端 v1 MUST 只支持 standalone Redis，不生成 Redis Cluster hash tag；启用 Cluster 前必须由独立 change 重新证明多 key 原子操作的 slot strategy。

#### Scenario: 构造合法 session key
- **WHEN** session owner 使用已登记 kind 与严格校验的稳定 ID/digest segment 构造 key
- **THEN** Keyspace 返回确定性 namespace key，且日志值只暴露 pattern/owner 而不暴露完整 identity 或 digest

#### Scenario: 动态 segment 包含 namespace 分隔符
- **WHEN** 调用方提交包含冒号、花括号、控制字符或超出长度限制的 identity segment
- **THEN** builder 拒绝输入且不生成可跨 owner、跨 environment 或隐式 hash-tag 的 key

### Requirement: Redis runtime 必须保持有界 TTL、原子操作和可失效恢复语义
Redis component MUST 使用受治理 client 配置连接/TLS/pool/command timeout，Start MUST 在 deadline 内验证连接、database 与 standalone mode，Stop MUST 有界关闭 client。每个运行态写入 MUST 通过已登记 TTL policy 计算正 duration 或由 registry 明确主动清理 owner；unknown value version、oversized/corrupted value、key miss、expiry、flush 和 dependency failure MUST fail closed。State-changing command/script MUST NOT 依赖 client 隐式重试猜测提交结果；多 key mutation MUST 由消费 owner 的单个原子 script/transaction 与稳定 idempotency 定义。Generic classifier MUST 只把单个 built-in command 的明确 server rejection 判定为 not-applied；Lua/transaction 的 server error MUST 默认保持 commit-unknown，直到 owner-specific outcome parser 能证明最终状态。Redis 丢失 MUST NOT 创建持久事实、回退 memory adapter 或让失败的 MySQL/业务操作返回成功。

#### Scenario: Redis flush 后服务重启
- **WHEN** Redis namespace 被清空而 MySQL 持久事实仍存在
- **THEN** storage runtime 只恢复空的可失效运行态，Session 重新认证、Placement 重建和 VisitSession 安全结束由各 owner 后续 adapter 处理，不伪造原运行资格

#### Scenario: TTL 计算到达非正边界
- **WHEN** owner 尝试写入已到期 credential、lease 或 tombstone，使计算 TTL 等于或小于零
- **THEN** runtime 拒绝创建 key，不把零值解释为永久保存或使用默认 TTL

#### Scenario: Redis mutation 结果不确定
- **WHEN** state-changing script 发送后连接中断且无法确认是否执行
- **THEN** runtime 返回 commit-unknown/dependency 分类，由 owner 使用同一 identity resolve/retry，不在 client 层盲目重放

#### Scenario: Redis script 在部分写入后返回运行时错误
- **WHEN** owner-defined Lua/transaction 已执行部分 mutation 后返回 Redis server error，generic runtime 无法解析业务最终状态
- **THEN** runtime 保持 commit-unknown 而不是 not-applied，只有 owner-specific outcome parser 可以依据稳定 identity 收窄结果

### Requirement: Storage 必须作为必需 lifecycle 资源参与 readiness 与受控关闭
Composition Root MUST 以显式依赖顺序创建并启动 diagnostic、MySQL/migration、Redis 与各自受控 probe task；只有 MySQL migration history 与 Redis startup probe 均成功后才能进入 ready。任一 Start 失败 MUST 逆序释放已成功资源。Ready 后 required storage probe 连续失败达到配置阈值 MUST 作为受控 fatal task 触发 draining 与非零退出，由进程重启重新建立资源；readiness MUST NOT 在同一进程内从 draining 恢复。Shutdown MUST 先停止未来业务消费者，再停止 Redis、MySQL，最后停止 diagnostic，并共享总 deadline。

#### Scenario: Redis 在 MySQL 成功后启动失败
- **WHEN** MySQL pool 与 migrations 已成功但 Redis startup probe 失败
- **THEN** 进程保持非 ready，关闭 MySQL 后最后关闭 diagnostic，并返回稳定 startup failure

#### Scenario: Ready 后 MySQL 持续不可用
- **WHEN** required MySQL probe 连续失败达到配置阈值
- **THEN** supervised task 报告稳定 `mysql` owner 和 dependency reason，runtime 进入 draining、执行有界逆序关闭并返回非零结果

### Requirement: Storage 可观测性必须低基数且不暴露数据库内容
Storage runtime MUST 记录 component、operation、outcome、migration version 和安全 failure kind，并提供连接池摘要、probe、transaction、migration、Redis command/script 与 shutdown 的低基数 metrics。日志与 metrics MUST NOT 包含 DSN、password、TLS key、SQL 文本、参数、row/value、完整 Redis key、玩家/账号/session/world identity 或未经分类的 driver error 作为 label；driver error MUST 保留在受控 error chain 中供诊断但默认对外信息必须脱敏。

#### Scenario: Storage operation 失败
- **WHEN** MySQL statement 或 Redis command 返回包含 endpoint、query/key 或参数片段的底层错误
- **THEN** 结构化日志记录稳定 component/operation/failure kind，metrics 只增加有界 label，公开诊断和默认格式化不输出敏感内容

### Requirement: Docker integration harness 必须可重复、隔离且有界清理
项目 MUST 提供从仓库根目录可非交互执行的 PowerShell/CI storage 入口，使用 `versions.yaml` 中精确 tag 与受治理 digest 启动 MySQL/Redis 容器。Harness MUST 使用唯一容器/network/volume 名、loopback 随机 host port、运行时生成且仅存于已忽略目录/进程环境的 secret。每次 Docker CLI 调用 MUST 受当前阶段剩余 deadline 约束，超时或取消后 MUST 终止对应子进程并进入具有独立 deadline 的有界 cleanup。Cleanup MUST 只删除本次运行拥有的资源；若 Docker daemon 不可用导致无法确认或删除资源，harness MUST 保留已忽略 state manifest、报告 cleanup failure 并允许恢复后显式 `down`，不得无限等待或删除非本次运行创建的 volume/container。Integration tests MUST 覆盖空库、重复/并发 migration、checksum/dirty 拒绝、Redis flush、dependency restart、startup rollback、race 与 shutdown。

#### Scenario: Integration verify 成功
- **WHEN** 开发者从仓库根目录运行统一 storage verify 命令且 Docker 可用
- **THEN** harness 启动锁定版本依赖、运行全部 contract/integration/recovery tests、输出阶段结果并清理本次资源后以零退出

#### Scenario: Integration test 中途失败
- **WHEN** migration 或 recovery test 返回非零结果
- **THEN** harness 保留原失败、在 finally 边界清理本次 container/network/volume 和临时 secret，并以非零退出且不污染其他运行

#### Scenario: Docker CLI 调用无响应
- **WHEN** Docker Desktop 处于半启动或 daemon 失联状态，使任一 Docker CLI 调用在阶段 deadline 内没有返回
- **THEN** harness 终止该 CLI 子进程、使用独立 cleanup deadline 尝试按 ownership 清理；若 daemon 仍不可用则保留 ignored state 并返回同时包含主阶段与 cleanup 分类的非零结果，供恢复后使用同一 run-id 执行 `down`

### Requirement: D0 不得提前实现业务 storage adapter
D0 production code MUST 只拥有 MySQL/Redis resource runtime、migration、transaction policy、key/schema registry、probe 与 lifecycle wiring。AccountRepository、CredentialHasher、SessionStore、PersonalWorldRepository、PlacementStore、VisitSession、outbox、业务 table/script 和公开 transport adapter MUST 由后续 owner change 实现并通过各自 contract tests；在这些 adapter 完成前，Composition Root MUST NOT 注入业务 service 或开放业务 API。

#### Scenario: Storage runtime 已 ready
- **WHEN** MySQL/Redis 基础资源和 migration 已成功接入 Composition Root，但业务 adapter 尚未完成
- **THEN** 进程只保持基础设施与诊断面 ready，不提供 register/login、session、PersonalWorld、WorldInstance 或 VisitSession 业务 endpoint，也不使用 generic map repository 伪装实现
