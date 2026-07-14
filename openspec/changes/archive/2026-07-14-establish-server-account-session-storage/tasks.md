## 1. Password hashing 与依赖治理

- [x] 1.1 将锁定的 `golang.org/x/crypto` 版本加入 `versions.yaml`、`server/go.mod`/`go.sum` 与版本一致性校验，确认只新增 Argon2id 所需直接依赖且 `go mod verify` 可重复通过
- [x] 1.2 在 owner-specific infrastructure package 实现 Argon2id v19 current profile、CSPRNG 16-byte salt、32-byte tag、canonical PHC encode/parser 和 constant-time Verify，并拒绝 unknown/duplicate/missing/over-budget 参数
- [x] 1.3 实现可校验的 hash 并发 semaphore 与 context 等待语义，保证运行中计算不复制到后台 goroutine、失败不返回 hash 且默认 String/GoString/slog/error 全部脱敏
- [x] 1.4 由 production hasher 生成或验证同 profile dummy hash，增加 known vector、随机 salt、wrong password、malformed PHC、resource ceiling、并发/cancel、fuzz、race 与 benchmark tests

## 2. Account MySQL schema 与 repository

- [x] 2.1 追加不可变 `000006` forward migration，以单张 `accounts` 表保存 account/player/username/display name/credential/status/created time，设置精确字符集/collation、唯一索引和符合规范的中文表列注释
- [x] 2.2 扩展 migration catalog/checksum/structure tests，验证每个 migration 仍只有一个 statement、既有 migration 未改动且 information_schema 中新表/列/时间单位注释完整
- [x] 2.3 新建 `internal/storage/account` repository 与严格 codec/hydration，复用共享 MySQL pool/transaction policy，只选择显式列并拒绝非法 ID、非 canonical username、unknown status、无效 PHC 或 UTC 微秒时间
- [x] 2.4 实现 Create 的已知唯一约束分类、not-committed/commit-unknown 映射和 FindForAuthentication 的 found/not-found/dependency 边界，禁止自动重试不确定事务或输出 SQL/参数/credential
- [x] 2.5 增加真实 MySQL contract/integration tests，覆盖并发 canonical username、AccountID/PlayerID碰撞fail closed、完整认证快照、损坏行、context/连接故障、response loss、restart 与无残缺记录

## 3. Session Redis schema、key 与 codec

- [x] 3.1 在 `internal/storage/session` 登记 session record、access、refresh、ticket、principal index definitions，固定 owner、kind、schema version、最大 encoded bytes、逻辑 expiry、physical TTL、cleanup、恢复、failure 和 metrics metadata
- [x] 3.2 扩展 `docs/redis-keys.md` 的全部 value 字段字典，使用英文线上字段名和中文短注释，明确 UTC/Unix 微秒、digest 编码、必填性、tombstone 与失效语义
- [x] 3.3 实现复用共享 Keyspace 的严格 key builders，digest 只使用固定长度 lowercase hex，拒绝非法 environment/identity/digest 且日志和 metrics 不暴露完整 key
- [x] 3.4 实现版本化 Redis Hash codecs 与交叉记录校验，拒绝 unknown version、缺失/多余字段、非法 enum/scope/endpoint/time、越界 value 和不一致 session/epoch，并增加 round-trip/corruption/fuzz tests
- [x] 3.5 实现 UTC 微秒逻辑 expiry 与向上取整毫秒 physical TTL policy，覆盖 expiry 边界、session上限、refresh tombstone、ticket consumed marker和principal index最晚寿命

## 4. SessionStore 原子 Lua operations

- [x] 4.1 实现并登记 Create script，原子校验全部 key 不冲突后写入 session、首枚 access/refresh 与 principal index，任一非法输入或碰撞均不留下部分状态
- [x] 4.2 实现 ResolveAccess 与 RotateRefresh scripts，在同一线性化点校验 session/status/epoch/expiry，至多消费一次 refresh、撤销旧 access、提交新 pair并把旧refresh保留为replay tombstone
- [x] 4.3 实现 IssueTicket 与 ConsumeTicket scripts，仅在 current session/epoch 有效时签发，并在 channel/endpoint/expiry 全匹配后才消费；错误 listener 不烧毁合法 ticket
- [x] 4.4 实现 InvalidateSession script，首次撤销单调递增 epoch 并保存稳定 reason，重复调用返回同一 invalidation且旧token/ticket永不恢复
- [x] 4.5 实现有防御数量上限的 InvalidatePrincipal standalone script，基于principal index all-or-nothing撤销全部现有sessions，依赖错误不得报告部分成功
- [x] 4.6 封装 production SessionStore adapter 的脚本加载、输入/返回分类和低基数观测，确保 Redis error/cancel/response loss/corruption 不被映射为 not found、expired、replay 或 success

## 5. Redis contract、恢复与安全验收

- [x] 5.1 以既有 SessionStore reference model scenario assertions 为基线，对真实 Redis adapter运行同一create/access/refresh/ticket/invalidation cases，验证 outcome、snapshot、epoch 和 expiry 一致
- [x] 5.2 增加高并发 refresh rotation、ticket consume、重复 invalidation 和principal multi-session race tests，确认脚本至多一次、重试收敛且 `go test -race` 无本地状态竞态
- [x] 5.3 增加 wrong channel/endpoint 不消费、stale epoch、expired session/token、consumed tombstone、script response loss、超限 principal index 与 value corruption tests
- [x] 5.4 扩展 storage harness 验证 Redis restart保留完整状态、Redis flush后全部旧credential fail closed并可通过MySQL账号重新登录建新session，且不出现memory/MySQL session恢复
- [x] 5.5 审计日志、错误、metrics和测试失败输出，确保 plaintext password、完整PHC/salt/tag/dummy、token/ticket、digest、Redis full key/value、SQL和无界identity不会泄漏或成为label

## 6. 架构边界与长期文档

- [x] 6.1 更新 `docs/architecture.md`、`docs/file-structure.md`、`docs/roadmap.md`、`server/README.md` 与存储 owner 文档，插入 production Account/Session storage资格门并说明仅一张账号表、Redis flush重新登录和后续HTTP进入条件
- [x] 6.2 增加静态/结构测试，禁止 storage adapters 反向依赖 generated protocol/Gin/listener，禁止创建独立MySQL/Redis client、goroutine、memory fallback或修改Account/Session domain接口
- [x] 6.3 验证正式 Composition Root 只应用migration而不构造Account/Session service graph、不注册register/login/refresh/logout/ticket route，也不把schema ready宣称为业务ready
- [x] 6.4 复核全部新增Go、PowerShell与SQL注释，确保中文定位职责并解释单位、算法资源、安全、原子性、损坏、重试、TTL和生命周期，无逐行复述或无上下文TODO

## 7. 质量门与归档准备

- [x] 7.1 通过项目入口执行Go format、全量unit、targeted fuzz、race、vet与mod verify，确认hasher/repository/store在并发和生成清洁检查下稳定
- [x] 7.2 执行统一Docker storage verify，覆盖空库migration、MySQL/Redis contract、restart、flush、failure injection、run-id隔离与cleanup，并确认测试不依赖公开listener
- [x] 7.3 运行 `git diff --check`、secret/generated/cache/dependency hygiene与全量OpenSpec strict validation，确认无本机文件、密钥或可推导generated projection进入提交
- [x] 7.4 将 `server-account-session-storage` 与修改后的 `delivery-sequencing` delta同步到主spec，复核proposal/design/tasks与实际实现一致，再次strict并满足归档条件
