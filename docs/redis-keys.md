# Redis Key 规范

## 原则

Redis 只保存可恢复或可失效运行态。任何 key 在实现前必须通过对应 OpenSpec change 确认 owner、TTL、value schema、恢复来源、清理触发和故障语义。

## 命名

```text
ih:<env>:<owner>:<kind>:<identity...>
```

尖括号只表示文档占位。`Keyspace` 的真实输出使用普通冒号分段，例如
`ih:production:session:lease:01J...`，不会输出 `{}`，也不会把 environment
误设为 Redis Cluster hash tag。当前 runtime 只支持 standalone Redis；Cluster/Sentinel
必须由独立 OpenSpec change 根据已落地的 owner-specific Lua、multi-key lookup 与热点预算设计同 slot 策略。

要求：

- `env` 明确隔离 local、test、staging、production。
- owner 与代码模块一致。
- identity 使用稳定 ID，不使用未经处理的账号名或 token。
- 动态片段必须校验长度和字符集。
- key builder 集中实现并测试。

## 第一阶段规划

下表是允许的第一阶段 key 类型；实际创建必须由实现 change 固化 TTL 和 value。

| Key Pattern | Owner | 用途 | 恢复/失效原则 |
|---|---|---|---|
| `ih:<env>:session:record:<sessionID>` | session | principal、epoch、status 与 session expiry | 不恢复；丢失后要求重新登录 |
| `ih:<env>:session:access:<accessDigest>` | session | access digest 到 session/epoch 的短期索引 | 不恢复；丢失后 access 认证失败 |
| `ih:<env>:session:refresh:<refreshDigest>` | session | active refresh 或 consumed replay tombstone | 不恢复；consumed tombstone 保留到 session expiry |
| `ih:<env>:session:ticket:<nonceDigest>` | session | 一次性 connection ticket 绑定与消费状态 | 不恢复；短 TTL，consumed marker 保留到 ticket expiry |
| `ih:<env>:session:principal:<principalDigest>` | session | principal 到现有 session ids 的失效索引 | 不恢复；丢失或损坏时 principal 批量撤销 fail closed，单会话仍由 record 校验 |
| `ih:<env>:presence:player:<playerID>` | session | 在线连接摘要 | 从 connection registry 恢复 |
| `ih:<env>:placement:assignment:<personalWorldID>` | placement | schema v1 current WorldInstance stamp、starting/active phase 与精确 lease expiry | 不恢复旧 assignment；TTL 到期或 Redis flush 后，新 candidate 必须从 MySQL allocation ledger 获得更高 generation/fence |
| `ih:<env>:placement:transition:<transitionDigest>` | placement | schema v1 Acquire/Activate/Renew/Revoke/Replace 首次结果与完整 request fingerprint | 正 TTL 覆盖有界 retry window；丢失后既有 allocation 保持 commit-unknown，不猜测或重新发布 |
| `ih:<env>:visit:session:<visitSessionID>` | visit | Visitor membership、revision、expiry 与 Owner grace | 丢失后访问安全结束或按权威事实重建，不是持久世界事实 |
| `ih:<env>:visit:player:<playerID>` | visit | 玩家当前 visit membership 索引 | 从有效 VisitSession 重建，必须有 TTL 与 cleanup owner |
| `ih:<env>:rate:<scope>:<identity>` | owning adapter | 限流窗口 | 丢失后最多放宽一个窗口 |
| `ih:<env>:lock:<owner>:<resourceID>` | owning module | 必要短租约 | 不恢复，必须有 TTL 与 fencing/idempotency |

### 已实现的 session definitions

五类 key 均由 `internal/storage/session` 独占，使用 Redis Hash schema v1 和必需
`PEXPIREAT`。Hash 内的 `*_us` 是授权判断使用的 UTC Unix 微秒；physical TTL
向上取整到 Unix 毫秒，只负责最终清理，不能替代逻辑 expiry。Access、refresh 与
ticket digest 使用完整 SHA-256 的 64 位 lowercase hex；`principalDigest` 是对带长度
前缀的受信 account/player 组合取 SHA-256 前 128 bit，Hash 同时保存原 identity 以检测
摘要碰撞。任何 unknown version、字段缺失/多余、非法枚举、交叉绑定不一致或缺失 TTL
都按依赖损坏 fail closed。

`session_record`：

- Pattern：`ih:<env>:session:record:<sessionID>`
- TTL：session 绝对到期时间；不滑动续期
- 恢复/清理：不从 MySQL 或 memory 恢复；到期自然删除，Redis flush 后要求重新登录
- 指标：固定 `session` adapter、operation、outcome，不记录 key、identity 或 digest

| Field | 类型/编码 | 中文短注释 | 规则 |
|---|---|---|---|
| `v` | canonical decimal `uint16` | Schema版本号 | 必填，固定为 `1` |
| `account` | string | 账号ID | 必填，合法 `acc_` identity |
| `player` | string | 玩家ID | 必填，合法 `ply_` identity |
| `epoch` | canonical decimal `uint64` | 会话代际 | 必填，正整数，不经过 Lua double |
| `status` | enum string | 会话状态 | 必填，`active` 或 `invalidated` |
| `expires_us` | canonical decimal `int64` | 会话到期时间(UTC Unix微秒) | 必填，达到边界即失效 |
| `invalidation_epoch` | canonical decimal `uint64` | 失效会话代际 | active 时为 `0`；invalidated 时等于 `epoch` |
| `invalidation_reason` | enum string | 会话失效原因 | active 时为 `none`；否则为固定失效原因 |

`session_access`：

- Pattern：`ih:<env>:session:access:<accessDigest>`
- TTL：access 绝对到期时间，不得晚于 session 到期时间
- 恢复/清理：不恢复；到期自然删除，refresh 成功时原子删除上一枚 access

| Field | 类型/编码 | 中文短注释 | 规则 |
|---|---|---|---|
| `v` | canonical decimal `uint16` | Schema版本号 | 必填，固定为 `1` |
| `session` | string | 会话ID | 必填，合法 `ses_` identity |
| `epoch` | canonical decimal `uint64` | 会话代际 | 必填，必须匹配 current session |
| `expires_us` | canonical decimal `int64` | 访问凭据到期时间(UTC Unix微秒) | 必填，达到边界即失效 |

`session_refresh`：

- Pattern：`ih:<env>:session:refresh:<refreshDigest>`
- TTL：active 时为 refresh 到期时间；消费后延长到 session 到期时间
- 恢复/清理：不恢复；消费后保留 replay tombstone，重放会原子失效整条 session lineage

| Field | 类型/编码 | 中文短注释 | 规则 |
|---|---|---|---|
| `v` | canonical decimal `uint16` | Schema版本号 | 必填，固定为 `1` |
| `session` | string | 会话ID | 必填，合法 `ses_` identity |
| `epoch` | canonical decimal `uint64` | 会话代际 | 必填，必须匹配 current session |
| `access` | lowercase hex | 访问凭据摘要(SHA-256,32字节) | 必填，绑定本次 refresh 应撤销的 access |
| `expires_us` | canonical decimal `int64` | 刷新凭据到期时间(UTC Unix微秒) | 必填，达到边界即失效 |
| `session_expires_us` | canonical decimal `int64` | 会话到期时间(UTC Unix微秒) | 必填，决定 tombstone 最长寿命 |
| `consumed` | enum string | 消费标记 | 必填，`0` 或 `1` |
| `replay_epoch` | canonical decimal `uint64` | 重放失效代际 | 未发生重放时为 `0`，之后保存稳定结果 |
| `replay_reason` | enum string | 重放失效原因 | 未发生重放时为 `none`，之后保存既有失效原因 |

`session_ticket`：

- Pattern：`ih:<env>:session:ticket:<ticketDigest>`
- TTL：ticket 绝对到期时间，且不得晚于 session 到期时间
- 恢复/清理：不恢复；成功消费只写 `consumed=1` 并保留到 ticket 到期，错误 listener 不消费

| Field | 类型/编码 | 中文短注释 | 规则 |
|---|---|---|---|
| `v` | canonical decimal `uint16` | Schema版本号 | 必填，固定为 `1` |
| `session` | string | 会话ID | 必填，合法 `ses_` identity |
| `epoch` | canonical decimal `uint64` | 会话代际 | 必填，必须匹配 current session |
| `channel` | enum string | 接入通道 | 必填，`wss` 或 `tls_tcp` |
| `host` | lowercase ASCII | 监听地址 | 必填，规范化 IP 或 DNS name |
| `port` | canonical decimal `uint16` | 监听端口 | 必填，范围 `1..65535` |
| `scopes` | enum string | 授权范围 | WSS 固定 `control`；TLS/TCP 固定 `gameplay` |
| `expires_us` | canonical decimal `int64` | 票据到期时间(UTC Unix微秒) | 必填，达到边界即失效 |
| `consumed` | enum string | 消费标记 | 必填，`0` 或 `1` |

`session_principal`：

- Pattern：`ih:<env>:session:principal:<principalDigest>`
- TTL：该 principal 索引内最晚 session 到期时间
- 大小上限：最多 64 个 session ID；超限或损坏时禁止部分撤销
- 恢复/清理：不扫描或重建；到期自然删除，Redis flush 后旧 credential 已全部失效

| Field | 类型/编码 | 中文短注释 | 规则 |
|---|---|---|---|
| `v` | canonical decimal `uint16` | Schema版本号 | 必填，固定为 `1` |
| `account` | string | 账号ID | 必填，并用于拒绝 principal digest 碰撞 |
| `player` | string | 玩家ID | 必填，并用于拒绝 principal digest 碰撞 |
| `expires_us` | canonical decimal `int64` | 索引到期时间(UTC Unix微秒) | 必填，等于成员最晚 session expiry |
| `sessions` | canonical CSV | 活跃会话ID集合 | 必填，1 至 64 个唯一 `ses_` identity；创建新会话时原子移除已缺失或已失效成员 |

### 已实现的 placement definitions

`placement_assignment`：

- Pattern：`ih:<env>:placement:assignment:<personalWorldID>`
- Owner：`internal/storage/placement`
- TTL：使用 lease absolute expiry 的 `PEXPIREAT`，microseconds 向上取整为 milliseconds；Hash 内的 `expires_us` 仍是领域精确边界
- Value schema：Hash schema v1，完整保存 `world`、`instance`、`node`、canonical decimal `generation`/`fence`、`starting|active`、`created_us` 与 `expires_us`，累计 encoded budget 2048 bytes
- 写入触发：Acquire/Replace 首次发布，Activate/Renew 原子推进
- 读取触发：Resolve 以及所有完整 stamp 条件操作
- 恢复来源：不恢复旧 lease；MySQL `placement_sequences`/`placement_allocations` 只用于确保新 candidate 取得更高 generation/fence
- 清理触发：lease 到期、Revoke 或 Replace
- 故障行为：missing 表示无 current；unknown version、malformed/oversized Hash、缺失 required TTL 或 dependency failure 全部 fail closed
- 指标：固定 `placement` adapter、operation、outcome；key、identity、fence 与 value 不进入 label

Value 字段字典：

| Field | 类型/编码 | 中文短注释 | 规则 |
|---|---|---|---|
| `v` | canonical decimal `uint16` | Schema版本号 | 必填，固定为 `1` |
| `world` | string | 个人世界ID | 必填，合法 PersonalWorldID |
| `instance` | string | 世界实例ID | 必填，合法 WorldInstanceID |
| `node` | string | 运行节点ID | 必填，合法 RuntimeNodeID |
| `generation` | canonical decimal `uint64` | 实例代际 | 必填，正整数，不经过 Lua double |
| `fence` | canonical decimal `uint64` | 围栏令牌 | 必填，正整数，不经过 Lua double |
| `phase` | enum string | 实例阶段 | 必填，`starting` 或 `active` |
| `created_us` | canonical decimal `int64` | 创建时间(Unix微秒) | 必填，正 UTC Unix time |
| `expires_us` | canonical decimal `int64` | 租约到期时间(Unix微秒) | 必填，严格晚于创建时间 |

`placement_transition`：

- Pattern：`ih:<env>:placement:transition:<transitionDigest>`
- Owner：`internal/storage/placement`
- TTL：构造 Store 时显式提供 1 秒至 24 小时的 retry window，写入时使用正 `PEXPIRE`
- Value schema：Hash schema v1，保存固定 operation、SHA-256 stable command fingerprint、首次 `applied` outcome 与完整 assignment result，累计 encoded budget 3072 bytes
- 写入触发：Acquire/Activate/Renew/Revoke/Replace 在同一 Lua script 的线性化点写入
- 读取触发：相同 transition 重试或 Redis response loss 解析
- 恢复来源：不恢复；丢失后既有 MySQL allocation 不得重新发布
- 清理触发：retry window 到期自然删除
- 指纹规则：Acquire 绑定 allocation stamp，Activate/Revoke 绑定 expected stamp，Renew 额外绑定目标 expiry，Replace 绑定 predecessor/successor stamp；重试时重新读取的 `observedAt` 与临时候选时间不进入指纹
- 故障行为：fingerprint 不同返回 conflict；缺失 required TTL fail closed；missing 且无精确 current 证据保持 commit-unknown
- 指标：只记录固定 script/replay/burned/unknown 结果，不记录 digest 或 request 字段

Value 字段字典：

| Field | 类型/编码 | 中文短注释 | 规则 |
|---|---|---|---|
| `v` | canonical decimal `uint16` | Schema版本号 | 必填，固定为 `1` |
| `operation` | enum string | 变更类型 | 必填，Acquire/Activate/Renew/Revoke/Replace 对应的小写稳定值 |
| `fingerprint` | lowercase hex | 请求指纹(SHA-256,32字节) | 必填，64 个 hex 字符 |
| `outcome` | enum string | 首次结果 | 必填，当前固定为 `applied` |
| `world` | string | 个人世界ID | 必填，合法 PersonalWorldID |
| `instance` | string | 世界实例ID | 必填，合法 WorldInstanceID |
| `node` | string | 运行节点ID | 必填，合法 RuntimeNodeID |
| `generation` | canonical decimal `uint64` | 实例代际 | 必填，正整数，不经过 Lua double |
| `fence` | canonical decimal `uint64` | 围栏令牌 | 必填，正整数，不经过 Lua double |
| `phase` | enum string | 实例阶段 | 必填，`starting` 或 `active` |
| `created_us` | canonical decimal `int64` | 创建时间(Unix微秒) | 必填，正 UTC Unix time |
| `expires_us` | canonical decimal `int64` | 租约到期时间(Unix微秒) | 必填，严格晚于创建时间 |

## Value 规则

- 每个已实现 key 必须登记不可变 definition：name、owner、kind、用途、TTL/cleanup、schema version、最大 encoded bytes、恢复、失败和 metrics。
- Redis 没有原生 COMMENT；每个已实现 Hash/JSON/二进制 schema 必须按 `docs/storage-schema-comment-convention.md` 在本文维护完整字段字典。
- Registry 只治理 metadata，不提供 generic cache API；typed codec、Lua/transaction 和 outcome parser 仍由消费 adapter owner 定义。
- 单个 built-in command 的明确 server rejection 可以分类为 `not_applied`；Lua/transaction 的运行时错误不会回滚此前写入，generic classifier 必须保持 `commit_unknown`，只有 owner-specific outcome parser 可以进一步收窄。
- Value 使用明确 schema，不存任意 `map[string]any`。
- JSON 字段使用稳定英文名；文档用中文说明含义。
- 时间字段包含单位。
- Value 必须有版本或能够兼容新增字段。
- 不存密码、完整 token、ticket 明文或 TLS key。
- 大对象和高频热 key 需要大小/带宽预算。

## TTL 规则

- 每个运行态 key 必须有 TTL 或明确的主动清理与恢复路径。
- ticket TTL 必须短且一次性。
- session record TTL 必须覆盖该 session 的最长剩余资格，不能短于 refresh/tombstone 生命周期。
- access、active refresh 与 ticket TTL 不得超过各自 expiry，也不得超过 session 剩余寿命。
- consumed refresh tombstone 保留到 session expiry；consumed ticket marker 保留到 ticket expiry。
- presence TTL 必须能容忍心跳抖动但不能无限延长。
- lock 禁止无过期时间。
- TTL refresh 由明确 owner 执行，不允许多个模块竞争续期。
- runtime 的 TTL helper 只接受 absolute expiry 与受信 `now`，结果小于等于零时拒绝写入。

## 清理与恢复

必须测试：

- key miss
- TTL 到期
- Redis flush
- Redis 暂时不可用
- value 损坏或版本未知
- 重复写入与乱序刷新
- 连接关闭、登出、VisitSession 关闭和进程重启

Redis 故障不能使已失败的持久操作对客户端返回成功。

## 文档模板

新增 key 时记录：

```text
Pattern:
Owner:
用途:
TTL:
Value schema:
Value 字段字典:
| Field | 类型/编码 | 中文短注释 | 规则 |
|---|---|---|---|
写入触发:
读取触发:
恢复来源:
清理触发:
故障行为:
指标:
```

## 禁止事项

- 无 owner key
- 无 TTL 且无恢复路径的运行态 key
- 将 Redis 作为账号、资产、PersonalWorld 或结算等唯一事实源
- 使用 `KEYS` 扫描生产 namespace
- 在日志中打印完整敏感 key/value
- 多个模块写同一 key 但没有并发与版本策略
- client-level mutation 隐式 retry；连接中断或 Lua/transaction generic error 后的 atomic mutation 必须返回 commit-unknown，由 owner 使用稳定 identity resolve
