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
| `ih:<env>:visitsession:active:<personalWorldID>` | visitsession | PersonalWorld 当前 VisitSession 索引 | 进程重启可读取 Redis 仍保留的合法值；terminal transition 主动删除，丢失后旧访问资格失效 |
| `ih:<env>:visitsession:session:<visitSessionID>` | visitsession | Visitor membership、revision、expiry、Owner grace 与完整运行快照 | 进程重启只读取 Redis 仍保留的合法值；丢失后访问安全结束，不影响持久世界事实 |
| `ih:<env>:visitsession:command:<commandID>` | visitsession | Create/transition 首次完整结果与重放证据 | 进程重启只读取 Redis 仍保留的合法值；丢失后禁止猜测首次提交结果 |
| `ih:<env>:worldadmission:issue:<issueIdDigest>` | worldadmission | 签发 identity、binding fingerprint 与首次 credential digest | 不恢复；保留到业务 expiry 加 replay retention，用于解析 response loss |
| `ih:<env>:worldadmission:credential:<credentialDigest>` | worldadmission | 一次性 credential binding（含 Visitor 签发 revision）与 consume tombstone | 不恢复；业务到期即失效，physical TTL 只保留有界重放证据 |
| `ih:<env>:rate:<scope>:<identity>` | owning adapter | 限流窗口 | 丢失后最多放宽一个窗口 |
| `ih:<env>:lock:<owner>:<resourceID>` | owning module | 必要短租约 | 不恢复，必须有 TTL 与 fencing/idempotency |

当前公开 HTTP limiter 使用有容量上限的进程内 token bucket，并未创建规划中的 `rate` key；任何分布式限流落地都必须通过独立 change 明确 owner、TTL、原子操作与多实例语义。

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

### 已实现的 VisitSession definitions

三类 key 由 `internal/storage/visitsession` 独占，使用 Redis Hash schema v1。它们的
`PEXPIREAT` 统一等于 VisitSession `expires_us` 加 1 分钟至 24 小时的配置重放保留期，
转换为 Unix milliseconds 时向后取整。Hash 内的 `expires_us` 始终是领域 session
到期时间(UTC Unix微秒)，不是 physical cleanup 时间；invite、reservation、Owner/Visitor
grace 与 session 等于 deadline 即失效，即使 key 仍存在也不能延长资格。

Redis 进程重启时，adapter 可以从 Redis 自身仍保留的合法 Hash 恢复进程内投影。
Redis flush、key miss 或 physical TTL 到期后，不从 MySQL、日志或 memory 补回旧
VisitSession。当前不实现 `player` membership index：`VisitSessionStore` 没有该查询
consumer，预建索引会引入无 owner 双写。Semantic expiry 与 safe-return side effect
已由 `internal/app` 的唯一 coordinator/deadline owner 接线；admission credential 已由
独立 `worldadmission` owner 和下文两类 key 接线，均不归 VisitSession storage adapter
所有。已过领域 deadline 但仍在 physical retention 窗口内的 active index 必须 fail
closed，并由 current snapshot/reconciliation 路径提交匹配 cleanup，不得被 Create 直接覆盖。

`visitsession_active`：

- Pattern：`ih:<env>:visitsession:active:<personalWorldID>`
- Owner：`internal/storage/visitsession`
- 大小预算：Hash 全部 field name/value 累计不超过 1024 bytes
- 写入触发：Create 首次成功时与 session/command 在同一 Lua 线性化点写入
- 读取触发：Create existing 决议与 ResolveActive 原子读取
- 恢复/清理：进程重启只读取 Redis 仍保留的合法值；丢失后不从其他来源重建。Terminal Commit 只在索引仍指向目标 VisitSession 时原子删除，否则到 physical TTL 自然删除
- 故障行为：缺失表示没有 active session；unknown version、字段多余/缺失、world/session 交叉绑定矛盾或缺失 TTL 按 dependency defect fail closed
- 指标：固定 `visitsession` adapter、operation、outcome；PersonalWorldID 与 VisitSessionID 不进入 label

| Field | 类型/编码 | 中文短注释 | 规则 |
|---|---|---|---|
| `v` | canonical decimal `uint16` | Schema版本号 | 必填，固定为 `1` |
| `visit_id` | string | 访客会话ID | 必填，合法 `vses_` identity |
| `world` | string | 个人世界ID | 必填，必须等于 key identity 与 session payload |
| `expires_us` | canonical decimal `int64` | 会话到期时间(UTC Unix微秒) | 必填，等于 session snapshot 的领域到期时间 |

`visitsession_session`：

- Pattern：`ih:<env>:visitsession:session:<visitSessionID>`
- Owner：`internal/storage/visitsession`
- 大小预算：Hash 全部 field name/value 累计不超过 131072 bytes
- Value schema：Hash metadata 加 canonical JSON `payload`；JSON 使用固定 struct field order，不允许 unknown/重复/缺失字段或非规范空白
- 写入触发：Create 或 Commit 在 owner Lua script 内保存完整 current/terminal snapshot
- 读取触发：ResolveActive、FindByID、Create existing 与 Commit CAS
- 恢复/清理：进程重建只从合法 Hash hydrate；不从持久存储补回，physical TTL 到期自然删除
- 故障行为：metadata/payload矛盾、unknown enum/version、非法identity/时间、越界集合、超预算或缺失TTL全部fail closed，不自动修补

| Field / JSON path | 类型/编码 | 中文短注释 | 规则 |
|---|---|---|---|
| `v` | canonical decimal `uint16` | Schema版本号 | Hash必填，固定为`1` |
| `visit_id` | string | 访客会话ID | Hash必填，必须等于key与`payload.id` |
| `world` | string | 个人世界ID | Hash必填，必须等于active index与`payload.world_id` |
| `revision` | canonical decimal `uint64` | 会话版本号 | Hash必填，正整数，不经过Lua double |
| `lifecycle` | enum string | 会话生命周期 | Hash必填，`open`、`owner_grace`或`closed` |
| `expires_us` | canonical decimal `int64` | 会话到期时间(UTC Unix微秒) | Hash必填，等于`payload.expires_us` |
| `facts` | lowercase hex string | 不可变事实摘要(SHA-256,32字节) | Hash必填；覆盖ID、Owner、World、assignment、capacity及创建/到期时间，仅用于CAS一致性，不是MAC、凭据或授权证据 |
| `payload` | canonical JSON object | 完整会话快照 | Hash必填；与metadata共享131072 bytes累计上限 |
| `payload.id` | string | 访客会话ID | 合法`vses_` identity |
| `payload.owner_id` | string | 世界主人玩家ID | 合法`ply_` identity且不可转移 |
| `payload.world_id` | string | 个人世界ID | 合法`pworld_` identity |
| `payload.assignment.*` | object | 世界实例分配标记 | 必含`world_id`、`instance_id`、`node_id`、正`generation`与正`fencing_token` |
| `payload.lifecycle` | enum string | 会话生命周期 | 与Hash metadata相同 |
| `payload.revision` | JSON `uint64` | 会话版本号 | 正整数，与Hash metadata相同 |
| `payload.capacity` | JSON `uint8` | 访客容量(人) | 1至32 |
| `payload.created_us` | JSON `int64` | 创建时间(UTC Unix微秒) | 正值且早于`expires_us` |
| `payload.expires_us` | JSON `int64` | 会话到期时间(UTC Unix微秒) | 达到边界即失效 |
| `payload.owner_binding.*` | object | 主人连接绑定 | 必含`player_id`、`session_id`、正`epoch`与`connection_id` |
| `payload.owner_grace_generation` | JSON `uint64` | 主人恢复代际 | 仅`owner_grace`为正 |
| `payload.owner_grace_expires_us` | JSON `int64` | 主人恢复到期时间(UTC Unix微秒) | 非`owner_grace`固定为0 |
| `payload.invites[]` | array | 定向邀请集合 | pending最多64个，集合总数最多`64 + capacity`；每项含`id`、`target_id`、`state`、`created_revision`、`expires_us` |
| `payload.memberships[]` | array | 访客成员集合 | 最多capacity个；每项含Visitor/invite/session/epoch/state及状态专属binding/deadline |
| `payload.memberships[].reservation_expires_us` | JSON `int64` | 预留到期时间(UTC Unix微秒) | 仅`reserved`为正，否则为0 |
| `payload.memberships[].reconnect_generation` | JSON `uint64` | 访客恢复代际 | 仅`reconnecting`为正 |
| `payload.memberships[].reconnect_expires_us` | JSON `int64` | 访客恢复到期时间(UTC Unix微秒) | 仅`reconnecting`为正，否则为0 |

`visitsession_command`：

- Pattern：`ih:<env>:visitsession:command:<commandID>`
- Owner：`internal/storage/visitsession`
- 大小预算：Hash 全部 field name/value 累计不超过 196608 bytes
- 写入触发：Create/Commit首次成功时与snapshot/index在同一Lua线性化点写入；existing与确定性conflict不写入
- 读取触发：相同CommandID重试时必须先于active index、not-found与revision决议
- 恢复/清理：进程重启只读取 Redis 仍保留的合法值；丢失后不重建，与所属session共享absolute physical expiry
- 故障行为：fingerprint不同返回idempotency conflict；metadata/payload矛盾、unknown kind/version、超预算或缺失TTL按dependency defect fail closed

| Field / JSON path | 类型/编码 | 中文短注释 | 规则 |
|---|---|---|---|
| `v` | canonical decimal `uint16` | Schema版本号 | Hash必填，固定为`1` |
| `kind` | enum string | 重放结果类型 | Hash必填，`create`或`mutation` |
| `fingerprint` | lowercase hex | 命令指纹(SHA-256,32字节) | Hash必填，64个hex字符 |
| `visit_id` | string | 访客会话ID | Hash必填，必须等于result snapshot |
| `world` | string | 个人世界ID | Hash必填，必须等于result snapshot |
| `revision` | canonical decimal `uint64` | 结果会话版本号 | Hash必填，正整数 |
| `expires_us` | canonical decimal `int64` | 会话到期时间(UTC Unix微秒) | Hash必填，等于result snapshot |
| `payload` | canonical JSON object | 首次完整结果 | Hash必填；与metadata共享196608 bytes累计上限 |
| `payload.operation` | enum string | 变更类型 | mutation必填，必须是VisitSession已登记operation |
| `payload.snapshot` | snapshot object | 结果会话快照 | 字段与`visitsession_session.payload`完全相同 |
| `payload.command_id` | string | 命令ID | 必填，必须等于key identity |
| `payload.fingerprint` | lowercase hex | 命令指纹(SHA-256,32字节) | 必填，必须等于Hash metadata |
| `payload.invite` | object/omitted | 邀请结果 | 仅create-invite存在 |
| `payload.admission` | object/omitted | 非凭据准入意图 | 仅accept存在；含VisitSession/Visitor/session/epoch/assignment与`expires_us` |
| `payload.membership` | object/omitted | 访客成员结果 | 仅join/reconnect存在，字段与snapshot membership相同 |
| `payload.retired_invites[]` | array/omitted | 本次退役邀请集合 | 按完整邀请identity稳定排序且唯一；每项字段与snapshot invite相同，只允许source为pending且target已不存在或非pending的项；历史结果可缺失 |
| `payload.directives[]` | array | 安全返回指令集合 | 按VisitorID稳定排序；每项含`visit_session_id`、`visitor_id`与封闭`reason` |

`worldadmission_issue`：

- Pattern：`ih:<env>:worldadmission:issue:<issueIdDigest>`
- Owner：`internal/storage/worldadmission`
- Identity：`issueIdDigest` 是稳定 issuance ID 的 SHA-256 前 128 bit 小写十六进制；碰撞只会 fail closed 为 idempotency conflict
- TTL：credential 业务 expiry 加配置的有界 replay retention，使用绝对 `PEXPIREAT`
- 大小：Hash 全部 field/value 累计最多 1024 bytes，由 owner Lua 在读取和重放时强制校验
- 写入/读取：issue Lua 与对应 credential Hash 在同一线性化点创建；相同 issuance ID 重试交叉验证两类 Hash
- 恢复：仅重读 Redis 自身仍保留的合法值；key miss 后使用新 issuance identity，不从其他来源补回
- 故障：相同 identity 改变 fingerprint/digest/expiry 返回 conflict；缺字段、未知版本、缺 TTL 或 credential 侧不一致返回 defect

| Field | 类型/编码 | 中文短注释 | 规则 |
|---|---|---|---|
| `v` | canonical decimal `uint16` | Schema 版本号 | 固定为 `2` |
| `fingerprint` | lowercase hex | 签发语义指纹(SHA-256,32字节) | 绑定全部 actor/role/target/purpose/session/assignment/endpoint/deadline |
| `digest` | lowercase hex | 凭据摘要(SHA-256,32字节) | 对 raw opaque credential 计算；不保存 raw 值 |
| `expires_us` | canonical decimal `int64` | 凭据到期时间(UTC Unix微秒) | 等于即失效；必须与 credential Hash 一致 |

`worldadmission_credential`：

- Pattern：`ih:<env>:worldadmission:credential:<credentialDigest>`
- Owner：`internal/storage/worldadmission`
- Identity：raw credential 的完整 SHA-256 小写十六进制；Redis 不保存 raw credential 或 derivation key
- TTL：与对应 issue Hash 相同的业务 expiry 加 replay retention；physical TTL 只保留重放证据，不延长资格
- 大小：Hash 全部 field/value 累计最多 8192 bytes，由 owner Lua 在消费和重放时强制校验
- 写入触发：issue Lua 创建；consume Lua 在静态 binding 全部匹配时把 `issued` 原子改为 `consumed`
- 重放：相同 `consume_id`/`consume_fp` 返回首次 binding；其他 identity 对 consumed record 返回 replayed
- 恢复：Redis restart 只读自身仍保留的合法值；flush/key miss 使旧 credential 永久失效
- 故障：unknown/corrupt/oversized Hash、缺 TTL、role/purpose/visit 组合矛盾或时间/assignment 字段非法全部 fail closed

| Field | 类型/编码 | 中文短注释 | 规则 |
|---|---|---|---|
| `v` | canonical decimal `uint16` | Schema 版本号 | 固定为 `2` |
| `status` | enum string | 消费状态 | `issued` 或 `consumed` |
| `consume_id` | string/`none` | 首次消费 ID | issued 为 `none`；consumed 必填安全 ASCII |
| `consume_fp` | lowercase hex/`none` | 首次消费指纹(SHA-256,32字节) | consumed 必填，绑定 AuthContext、endpoint、purpose 与 consume ID |
| `player` | string | 玩家 ID | 来自受信 AuthContext/domain 事实 |
| `session` | string | 会话 ID | 绑定签发时 session lineage |
| `epoch` | canonical decimal `uint64` | 会话世代 | 正整数，消费时必须精确匹配 |
| `role` | enum string | 世界角色 | `owner`或`visitor` |
| `world` | string | 个人世界 ID | 必须等于完整 assignment 的 world |
| `visit` | string/`none` | 访客会话 ID | Owner 为 `none`；Visitor 必填 |
| `purpose` | enum string | 准入用途 | `own_world`、`join` 或 `reconnect`，与 role/visit 组合严格匹配 |
| `visit_revision` | canonical decimal `uint64` | Visitor 签发时的 VisitSession revision | Owner 固定为 `0`；Visitor 为正数并进入签发指纹与响应重放 |
| `instance` | string | 世界实例 ID | 完整 AssignmentStamp 字段 |
| `node` | string | 运行节点 ID | 完整 AssignmentStamp 内部字段，不投影给客户端 |
| `generation` | canonical decimal `uint64` | 分配世代 | 正整数 |
| `fence` | canonical decimal `uint64` | 隔离令牌 | 正整数，默认日志禁止输出 |
| `channel` | enum string | 传输通道 | 固定 `tls_tcp` |
| `host` | lowercase ASCII | 服务地址 | 规范化 DNS/IP，不含 scheme/path |
| `port` | canonical decimal `uint16` | 服务端口 | `1..65535` |
| `issued_us` | canonical decimal `int64` | 签发时间(UTC Unix微秒) | 必须早于expiry |
| `expires_us` | canonical decimal `int64` | 凭据到期时间(UTC Unix微秒) | 等于即失效 |

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
