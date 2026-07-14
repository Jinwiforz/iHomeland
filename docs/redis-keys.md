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
| `ih:<env>:session:principal:<principalDigest>` | session | principal 到现有 session ids 的失效索引 | 可由活跃 session records 重建，成员随 session 清理 |
| `ih:<env>:presence:player:<playerID>` | session | 在线连接摘要 | 从 connection registry 恢复 |
| `ih:<env>:placement:assignment:<personalWorldID>` | placement | schema v1 current WorldInstance stamp、starting/active phase 与精确 lease expiry | 不恢复旧 assignment；TTL 到期或 Redis flush 后，新 candidate 必须从 MySQL allocation ledger 获得更高 generation/fence |
| `ih:<env>:placement:transition:<transitionDigest>` | placement | schema v1 Acquire/Activate/Renew/Revoke/Replace 首次结果与完整 request fingerprint | 正 TTL 覆盖有界 retry window；丢失后既有 allocation 保持 commit-unknown，不猜测或重新发布 |
| `ih:<env>:visit:session:<visitSessionID>` | visit | Visitor membership、revision、expiry 与 Owner grace | 丢失后访问安全结束或按权威事实重建，不是持久世界事实 |
| `ih:<env>:visit:player:<playerID>` | visit | 玩家当前 visit membership 索引 | 从有效 VisitSession 重建，必须有 TTL 与 cleanup owner |
| `ih:<env>:rate:<scope>:<identity>` | owning adapter | 限流窗口 | 丢失后最多放宽一个窗口 |
| `ih:<env>:lock:<owner>:<resourceID>` | owning module | 必要短租约 | 不恢复，必须有 TTL 与 fencing/idempotency |

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
