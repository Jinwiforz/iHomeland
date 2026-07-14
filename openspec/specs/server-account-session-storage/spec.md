# Server Account Session Storage 规格

## Purpose

定义 Account 持久事实、production 密码哈希与 Session Redis 可失效运行态的 schema、原子操作、恢复、安全和正式接线边界。

## Requirements

### Requirement: Account 持久事实必须使用最小且严格的 MySQL schema
系统 MUST 使用 Account owner 的单张规范化 MySQL 表原子保存 AccountID、唯一 PlayerID、唯一 canonical username、normalized display name、self-describing credential hash、封闭 account status 与 UTC 微秒创建时间。表、列与索引 MUST 有明确 owner 和中文短注释；username/credential encoding MUST 使用确定性 ASCII binary 语义，display name MUST 保持 utf8mb4，绝对时间 MUST 使用可无损 hydration 的 UTC 微秒类型。当前没有独立生命周期的 player profile、credential history、role 或 audit table MUST NOT 预建。

#### Scenario: 空库应用 account migration
- **WHEN** storage migrator 对空 MySQL schema 应用本 change 的 forward migration
- **THEN** 只新增满足 AccountRepository 的最小账号表，所有表列注释、唯一约束、字符集、collation 和 UTC 微秒类型均通过 information_schema 验证

#### Scenario: Hydration 遇到损坏事实
- **WHEN** repository 读取非法 ID、非 canonical username、unknown status、无效 credential encoding 或零/越界时间
- **THEN** adapter 返回脱敏 dependency/corruption error且不构造部分 Account、默认状态或可认证记录

### Requirement: AccountRepository 必须忠实实现原子 outcome 与提交不确定性
AccountRepository `Create` MUST 在一个 MySQL 原子写入中提交 account、player 与 credential hash，并只把已知 username 唯一约束映射为 `CreateOutcomeUsernameConflict`。Server-generated AccountID/PlayerID 碰撞 MUST 作为 identity/data defect fail closed，不能伪装成 username conflict。连接错误、context deadline 或 commit acknowledgement 丢失 MUST 复用 transaction policy 区分明确未提交与 commit unknown，不得猜测回滚、重试生成新身份或补偿删除。`FindForAuthentication` MUST 按 exact canonical username 返回同一一致性认证快照，not found 与 dependency failure MUST 严格分离。

#### Scenario: 并发注册相同 canonical username
- **WHEN** 多个 goroutine 使用大小写等价 username 并发调用 Create
- **THEN** 数据库唯一约束最多提交一个完整账号，其余返回 username conflict，且不存在只有 account、player 或 credential 任一部分的残缺记录

#### Scenario: Create 结果无法确认
- **WHEN** 数据库可能已提交但调用方在收到 commit acknowledgement 前失去连接或 deadline 到期
- **THEN** repository 返回 commit unknown且不谎报 not committed、username conflict 或成功，不执行跨存储 session 补偿

#### Scenario: 认证读取依赖失败
- **WHEN** canonical username 合法但 MySQL 无法证明记录存在或不存在
- **THEN** repository 返回 dependency failure而不是 not found，account application不得把它降级为 invalid credentials

### Requirement: Production password hashing 必须有界、memory-hard 且默认脱敏
CredentialHasher MUST 使用 Argon2id v19 self-describing PHC encoding、每次独立 CSPRNG salt、受治理 current profile 和 constant-time tag comparison。Current profile MUST 固定 64 MiB memory、3 iterations、4 lanes、16-byte salt 与32-byte tag；parser MUST只接受canonical encoding和显式登记且不超过安全上限的profile，禁止由持久字符串请求任意资源。Hash/Verify MUST受并发内存门控制，unknown username dummy hash MUST使用相同算法与成本。Plaintext、完整 PHC hash、salt、tag、dummy material 与内部解析错误 MUST NOT进入日志、错误、metrics或公开响应。

#### Scenario: 相同 password 重复注册
- **WHEN** hasher 对相同原始 password 独立执行两次 Hash
- **THEN** 两个 PHC encoding 使用不同随机 salt且都能验证，adapter 不规范化、trim 或保留 plaintext

#### Scenario: 恶意 PHC 参数
- **WHEN** Verify 收到未知 algorithm/version、非 canonical base64、重复/缺失参数或超过内存/iteration/lane上限的持久 hash
- **THEN** hasher 在启动昂贵计算前返回脱敏格式故障，不分配攻击者声明的资源且不把该结果解释为 password mismatch

#### Scenario: 并发 hash 达到预算
- **WHEN** 同时到达的 Hash/Verify 数量超过配置的并发内存预算
- **THEN** 超出部分有界等待并响应 context cancellation，运行中的 Argon2计算不被复制到不可停止的后台 goroutine

### Requirement: Session Redis schema 必须有 owner、版本、TTL 与严格 corruption 边界
SessionStore MUST使用owner-specific Redis definitions登记session record、access、refresh、ticket与principal index key，完整digest MUST只以固定长度lowercase hex进入key且不得出现在日志。每个Hash value MUST有显式schema version、固定英文字段集合、中文字段字典、最大encoded size、逻辑UTC微秒expiry、physical TTL、清理触发和失效原则。未知version、缺失/多余字段、非法enum/identity/time、cross-record不一致或超限value MUST作为dependency corruption fail closed，不得伪装为not found、expired、replayed或成功。

#### Scenario: Redis value 被截断或篡改
- **WHEN** session/token/ticket Hash 缺字段、包含unknown schema version或其session/epoch绑定互相矛盾
- **THEN** adapter返回脱敏dependency error且不签发AuthContext、不消费credential、不修补或覆盖损坏记录

#### Scenario: Redis key 到达逻辑 expiry
- **WHEN** 调用方Now达到value记录的UTC微秒expiry但physical TTL因毫秒向上取整尚未删除key
- **THEN** script按逻辑时间返回expired且不授予资格，Redis TTL只承担最终清理而不改变授权边界

### Requirement: SessionStore 每个安全迁移必须在单一 Redis 原子操作完成
SessionStore `Create`、`ResolveAccess`、`RotateRefresh`、`IssueTicket`、`ConsumeTicket`、`InvalidateSession` 与 `InvalidatePrincipal` MUST各自由单个standalone Redis Lua operation实现全部check-and-write，不得拆成客户端可观察的GET/SET序列。Create MUST原子建立session、首对token和principal index；refresh rotation MUST至多消费一次旧refresh、撤销旧access、写入新pair并把旧refresh保留为replay tombstone；ticket MUST只在current session/epoch有效时签发并且只在channel、endpoint、epoch与expiry全部匹配时消费；invalidation MUST单调递增epoch并幂等返回首次事实，principal invalidation MUST all-or-nothing覆盖其全部现有sessions。

#### Scenario: 两个 refresh 并发轮换
- **WHEN** 两个客户端同时提交同一active refresh digest与不同的新token pair
- **THEN** 最多一个rotation原子成功，另一个命中consumed tombstone并提交或返回同一refresh-replay invalidation，旧access和任一未获胜pair均不能继续有效

#### Scenario: Ticket 提交到错误 listener
- **WHEN** ticket digest 的channel或endpoint与ConsumeTicket入参不匹配
- **THEN** operation fail closed且不标记consumed，随后在正确listener和expiry前仍可至多成功消费一次

#### Scenario: 重复撤销 session
- **WHEN** 相同session已经因logout或security reason提交invalidation后再次调用InvalidateSession
- **THEN** store返回首次提交的稳定epoch/reason且不再次递增epoch、不恢复旧token/ticket

#### Scenario: Principal 全量撤销期间依赖失败
- **WHEN** principal具有多个现有sessions且Redis operation未能证明脚本是否完整执行
- **THEN** adapter不得报告部分成功；安全重试必须收敛到全部sessions各自同一invalidation或明确dependency failure

### Requirement: Session 运行态丢失必须要求重新登录且不得伪造恢复
Session record、token、refresh tombstone、ticket与principal index MUST仅保存于Redis可失效运行态。Active keys MUST按各自绝对expiry设置TTL；consumed refresh tombstone MUST至少保留到session expiry，consumed ticket marker MUST至少保留到ticket expiry，principal index MUST覆盖其最晚session expiry。Redis flush、eviction或无法读取时，旧access/refresh/ticket MUST全部fail closed并要求重新登录；系统 MUST NOT从MySQL account、客户端token payload、日志或memory fallback重建旧session/epoch/consumed状态。

#### Scenario: Redis flush 后使用旧 refresh
- **WHEN** Redis运行态被清空后客户端提交此前签发的refresh token
- **THEN** store返回not found或dependency failure且不创建新pair，MySQL账号保持存在并只能通过重新登录建立新session

#### Scenario: Redis 重启保留完整运行态
- **WHEN** 测试Redis按受支持配置重启且全部keys仍完整存在
- **THEN** adapter继续按原epoch、expiry、tombstone和ticket consumed状态判断，不因Go进程或client重建而放宽资格

### Requirement: Production adapters 必须复用共享 runtime 且保持公开入口未接线
AccountRepository、CredentialHasher与SessionStore MUST复用Composition Root已有MySQL/Redis clients、transaction/keyspace policy、clock/CSPRNG、secret boundary和低基数observability，不得创建独立pool/client、listener、background task、global mutable、memory fallback或generated protocol依赖。Migration MAY创建account schema，但在后续HTTPS adapter、connection invalidator与完整配置通过独立验收前，正式Composition Root MUST NOT构造Account/Session service graph或开放register/login/refresh/logout/ticket endpoint。

#### Scenario: 本 change 完成后启动正式服务
- **WHEN** production配置启动已包含新migration和adapter代码但HTTP change尚未完成
- **THEN** storage readiness可验证MySQL/Redis，进程仍不注册账号/session业务route、不声称认证可用，也不注入测试store或fake hasher

#### Scenario: 执行 storage integration harness
- **WHEN** 开发者运行统一Docker storage verify
- **THEN** 它在隔离MySQL/Redis资源上验证migration、注释、repository、hasher、SessionStore并发/TTL/replay/restart/flush/corruption，结束后按run-id安全清理资源
