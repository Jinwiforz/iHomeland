## Context

`internal/account` 已拥有 register/login 编排以及 `AccountRepository`、`CredentialHasher` 消费接口，`internal/session` 已拥有 token、refresh rotation、ticket、epoch invalidation 以及 `SessionStore` 原子契约。当前只有 `_test.go` reference implementations；storage runtime 只提供共享 MySQL/Redis clients、transaction policy、migration 与 keyspace，正式 Composition Root 因此仍不能构造可用的 Account/Session service graph。

P0 协议完成后，路线图原计划直接进入 `add-server-http-bootstrap`。但 register/login/refresh/logout/ticket 都依赖尚未实现的安全存储与密码哈希。把这些能力放进 HTTP change 会同时改变 data model、cryptography、Redis 原子状态机和公开 listener，失去独立回滚与故障验收边界。

## Goals / Non-Goals

**Goals:**

- 以一张最小 MySQL 表实现 AccountRepository，并忠实映射既有 create/find outcome、canonical identity 与 commit-unknown 契约。
- 实现固定安全 profile、受限并发、自描述编码且默认脱敏的 Argon2id CredentialHasher。
- 以 Redis typed schemas 与 Lua scripts 实现完整 SessionStore 原子接口，保证 token rotation、replay、ticket consume 和 epoch invalidation 在线性化点完成。
- 固定 schema/key owner、TTL、损坏数据、Redis flush、依赖失败、重试和可观测语义，并由 Docker integration tests 验收。
- 为后续 HTTP/WSS/TLS-TCP changes 提供 production-ready adapters，但保持正式业务入口未接线。

**Non-Goals:**

- 不实现 Gin router、HTTPS listener、bearer middleware、rate limiter、连接 registry 或任何公开账号/session API。
- 不实现 VisitSession Redis store、world admission issuer/verifier、RuntimeController 或 world/visit application composition。
- 不增加 session MySQL 表，不从 MySQL 恢复 Redis session，也不把 Redis 当作账号持久数据库。
- 不引入 password pepper、外部 KMS/HSM、密码升级 UI、封禁管理 API或多区域 Redis/Cluster/Sentinel。
- 不改变 Account/Session domain 接口、已发布 OpenAPI/Protobuf 字段或 stable error registry。

## Decisions

### 1. 在 HTTP 前增加独立 production auth storage capability

本 change 只实现既有消费接口及其安全依赖，migration 可由既有 storage runtime 正常执行，但 Composition Root 不提前构造未被公开 adapter 消费的业务 service。后续 `add-server-http-bootstrap` 只负责 decode、validate、authorize、调用 application service 和 encode，不再顺带创造 repository、hasher 或 SessionStore 语义。

**备选：**直接在 HTTP change 内实现全部依赖。拒绝该方案，因为密码算法、MySQL 提交不确定性、Redis Lua 原子性和 listener 生命周期无法形成单一可评审故障边界。

### 2. Account 使用一张规范化最小表

新增 `accounts` 表，由 `storage/account` owner 管理：

- `account_id`：持久主键；
- `player_id`：唯一玩家身份；
- `username`：已经由 domain 规范化的小写 ASCII 登录 key，使用 binary collation 唯一索引；
- `display_name`：NFC 且空白折叠后的公开展示名；
- `credential_hash`：ASCII PHC 自描述编码；
- `status`：`active`/`inactive` 封闭状态；
- `created_at`：UTC `DATETIME(6)`。

account、player 与 credential 在当前模型中是一对一且必须同原子操作创建，拆成多表不会增加独立生命周期或查询能力，只会扩大 transaction 和 migration 表面积。表与每列均使用符合项目规范的中文短注释。Repository 只执行明确列的 `INSERT` 与认证读取，不暴露通用 CRUD；hydration 对非法 ID、status、时间、credential encoding 和非 canonical username fail closed。

唯一冲突只有在命中已知 username 约束时映射为既有 `CreateOutcomeUsernameConflict`；server-generated AccountID/PlayerID 碰撞属于身份生成或数据缺陷，必须 fail closed 为 not committed dependency error，不能伪装成用户名占用。连接、context 或 commit acknowledgement 不确定继续复用 MySQL transaction policy 返回 commit unknown，禁止猜测为 not committed。

**备选：**accounts、players、credentials 三表。当前没有独立 player profile 或 credential history owner，故延后到真实需求出现后再通过 forward migration 拆分。

### 3. CredentialHasher 固定 Argon2id current profile

使用 `golang.org/x/crypto/argon2` 与 PHC string 表达，current profile 固定为 Argon2id v19、64 MiB memory、3 iterations、4 lanes、16-byte random salt 和 32-byte tag，对应 RFC 9106 的 memory-constrained recommended option。`Hash` 只生成 current profile；`Verify` 先以有界 parser 校验 algorithm/version/字段数量/base64 canonical form，并只接受显式登记且不超过安全资源上限的 profile，绝不直接信任持久字符串要求任意内存或 CPU。

Hasher constructor 接受经校验的最大并发数并使用 semaphore 限制同时计算的内存预算；等待 semaphore 前响应 context cancellation，一旦 Argon2 开始则同步完成，不为“可取消”创建无法停止的后台 goroutine。比较使用 constant-time compare。Constructor 同时生成或验证一个 current-profile dummy hash，供 unknown username 走同算法同成本路径。

本 change 不使用 pepper：当前没有独立密钥轮换、版本选择和不可用恢复设计，草率加入会让 secret 丢失永久锁死全部账号。PHC 编码、salt 和 tag 属于 credential hash，不是 plaintext，但仍按敏感材料禁止记录。未来 profile 升级通过“新 Hash 使用新 profile、Verify 保留有界旧 profile”演进，不原地重写登录失败的账号。

### 4. SessionStore 使用 Redis Hash schemas 与单操作 Lua 线性化

Redis 继续是可失效运行态，使用现有 keyspace builder 和以下 owner patterns：

- `session:record:<sessionID>`：principal、epoch、status、session expiry 和最后 invalidation；
- `session:access:<accessDigest>`：session/epoch/access expiry 索引；
- `session:refresh:<refreshDigest>`：session/epoch、对应 access digest、refresh expiry、consumed 状态和 replay invalidation；
- `session:ticket:<ticketDigest>`：session/epoch、channel、endpoint、scopes、ticket expiry 和 consumed 状态；
- `session:principal:<principalDigest>`：由 account/player identity 生成的固定摘要与有界 session IDs 索引，仅用于 all-or-nothing invalidation；value 仍保存完整受信 principal 以拒绝摘要碰撞。

Digest 在 key 中使用固定长度 lowercase hex；value 使用字段名固定的 Redis Hash，并包含 schema version。Typed codec 负责 Go value 与严格字段集合转换，Lua 只操作受信字段和 canonical scalar encoding。未知 version、缺字段、多余字段、非法 enum/identity/time 或不一致交叉引用都作为 corruption/dependency failure fail closed，不能伪装成 not found。

每个 SessionStore 方法由一个 Lua script 完成所需的多 key check-and-write：

- `Create` 原子创建 session、首枚 access/refresh 和 principal index；
- `ResolveAccess` 同时校验 access 与 current session status/epoch/expiry；
- `RotateRefresh` 原子消费旧 refresh、删除旧 access、写入新 pair，并把旧 refresh 保留为 replay tombstone；
- `IssueTicket` 只在 current session/epoch 有效时写入 ticket；
- `ConsumeTicket` 仅在 channel、endpoint、epoch 和 expiry 全匹配后标记 consumed，错误 listener 不烧毁 ticket；
- `InvalidateSession` 只在首次调用递增 epoch并保存稳定 invalidation，重试返回同一结果；
- `InvalidatePrincipal` 在 standalone Redis 单脚本内读取 principal index并撤销其全部现有 sessions，不能部分成功。

项目当前明确只支持 standalone Redis，因此不为 Redis Cluster hash slot 扭曲 key 或拆散原子语义。Principal index 大小、script 输入/返回和 encoded value 均设置防御上限；超限或损坏时 fail closed 并产生低基数诊断，而不是部分撤销。

### 5. 逻辑 expiry 使用 UTC 微秒，Redis TTL 只负责最终清理

Adapter 保存 domain 已规范化的 UTC Unix microseconds，并使用调用方一次读取的 `Now` 快照执行严格 `now < expiresAt` 判断。Redis physical TTL 向上取整到毫秒，不能因 Redis TTL 精度比 domain 时间低而提前接受或提前删除边界数据；授权结论始终来自 value 中的逻辑 expiry，而非仅依赖 `TTL` 命令。

Session/access/active refresh/ticket keys 分别按自身逻辑寿命清理；consumed refresh tombstone 延长到 session expiry，consumed ticket marker至少保留到 ticket expiry，principal index 保留到其最晚 session expiry。Redis flush 或 key eviction 后旧 token/ticket 全部 not found/fail closed，客户端必须重新登录；MySQL account 不受影响，也不会重建旧 session。

### 6. 复用共享资源但不提前接线业务 graph

Repository/Store 接收已有 MySQL/Redis clients、transaction runner/keyspace 和 observability，SessionStore 方法复用调用方单次读取的时间快照；Hasher 使用项目既定的系统 CSPRNG。它们不创建第二个 pool/client、listener、task、goroutine 或 memory fallback。MySQL migration 随 storage runtime 应用；repository/hasher/store constructors 只在 contract/integration tests 中直接使用，直到后续 HTTP change 具备完整配置、endpoint provider、connection invalidator 和 adapter lifecycle 后才进入 Composition Root。

这保持了“schema 已就绪”与“业务入口已开放”的区别，也避免仅为证明构造成功而创建无人消费的长期对象。

### 7. 验收以接口契约、真实依赖和故障恢复为主

MySQL integration tests 验证空库 migration、information_schema 中文注释、并发 canonical username 唯一性、严格 hydration、restart 和 commit outcome。Hasher tests 验证 PHC parser、随机 salt、known vectors、wrong password、dummy cost、资源上限、并发门和默认脱敏。Redis integration tests 以真实 Redis 并发运行全部 SessionStore methods，覆盖 response loss 可重试结果、refresh/ticket race、wrong binding 不消费、replay invalidation、principal 全量撤销、TTL 边界、corruption、restart 和 flush。

统一 storage harness 负责隔离 network/container/volume 和清理；unit/race/fuzz tests 不依赖 listener。公开 API、Go 协议客户端和 connection storm 测试保留给后续 transport/qualification changes。

## Risks / Trade-offs

- **[Argon2 造成内存或 CPU 峰值]** → 固定有界 profile、并发 semaphore、输入长度上限和基准测试；不从存储字符串接受任意成本。
- **[单表未来需要 credential history 或多角色 player]** → 当前一对一模型保持最小；未来由独立 owner 通过 forward migration 拆分，不预建空表。
- **[Redis Lua 脚本过大或 principal sessions 过多]** → 每个 operation 单一脚本、字段和结果有界、principal index 防御上限、真实 Redis 并发与延迟测试；Cluster 另走独立 change。
- **[Redis flush 导致全部登录失效]** → 这是安全设计而非数据恢复缺陷；所有旧 credential fail closed，账号持久事实仍可重新登录。
- **[Migration 已创建表但公开 API 尚不可用]** → 文档/readiness 不宣称业务 ready，Composition Root 不构造 service graph；表本身可安全空置并由 forward migration 演进。
- **[依赖响应丢失导致调用方不知是否提交]** → MySQL 返回 commit unknown；Redis 原子脚本重试收敛到既有 conflict/replay/invalidation，不返回未证明的新 credential。

## Migration Plan

1. 增加锁定依赖、Argon2id hasher 与 unit/fuzz/race tests，不接触 production graph。
2. 追加 `000006` MySQL forward migration和 AccountRepository integration tests；不得修改既有 migration。
3. 增加 Session key definitions、字段字典、typed codecs、Lua scripts 与真实 Redis contract tests。
4. 扩展 storage harness、版本/依赖校验和 owner 文档，执行 MySQL/Redis restart、flush 与 corruption 验收。
5. 保持公开 listener 未接线并归档；后续 HTTP change 再将 adapters 注入 Account/Session services。

回滚代码不会删除或降级 schema；`accounts` 表在未开放 API 时应为空。若测试环境已有数据，回滚只停止新代码消费并保留表，由显式运维决定数据处理，禁止 down migration 自动删除账号事实。

## Open Questions

无。本 change 的数据 owner、算法 profile、Redis topology、失败语义和接线边界均已固定；任何 pepper、Cluster/Sentinel、credential history 或公开 API 需求必须另行提出。
