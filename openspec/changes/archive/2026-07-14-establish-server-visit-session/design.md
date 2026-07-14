## Context

PersonalWorld core/storage 已拥有持久 world identity、immutable Owner 与 revision，placement core/storage 已拥有 current active assignment、lease 与 fencing，session core 已拥有不可伪造 `AuthContext`、session ID/epoch 和通用 gameplay scope。当前缺口不是新的网络连接方式，而是一个能独立回答“谁被临时允许访问哪个 Owner 的哪个 current instance、资格何时失效、断线如何恢复、失败后去哪里”的 VisitSession owner。

VisitSession 是 Redis 可失效运行态的未来消费模型，但本 change 只冻结 domain/application 与 store 契约。它必须在无 Redis、listener 和 generated protocol 的环境中验收，并为后续 storage/protocol/transport change 提供稳定 projection。设计还必须避免三个常见混淆：invite 不是 admission credential，socket presence 不是 membership 最终事实，Owner 断线不触发 host succession。

## Goals / Non-Goals

**Goals:**

- 建立独立、严格构造且默认脱敏的 VisitSession、invite、command 与 connection-binding identities。
- 建立可并发验收的 session/invite/membership 状态机，覆盖容量、expiry、Owner grace、Visitor reconnect 与 stale callback/timer。
- 只从受信 AuthContext、PersonalWorld owner lookup 与 current placement snapshot取得 actor/world/assignment/epoch，拒绝 payload 覆盖。
- 定义 expected revision、稳定 command fingerprint、完整 replay result、commit-unknown 与 dependency defect 边界。
- 让 accept 产生非凭据 `AdmissionIntent`，让 join 只接受后续 admission owner 已验证的 qualification。
- 为可能仍在访客世界中的成员生成确定性 safe-return directives，而不在 domain 内发送消息或启动目标世界。

**Non-Goals:**

- 不实现 Redis/MySQL adapter、migration/key schema、TTL cleanup worker、outbox 或 production Composition Root wiring。
- 不定义 admission bearer token、签名/加密、nonce consume、Protobuf/OpenAPI、message ID、route、channel、endpoint 或 listener。
- 不实现 connection registry、socket binding、网络重连循环、RuntimeController、Go 协议客户端或 Unity。
- 不拥有 PersonalWorld/PlayerState mutation、地图、任务、资产、奖励、settlement、Party、Room 或 ActivityInstance。
- 不提供允许任意 gameplay command 的通用 ACL；未由后续 interaction spec 显式登记的行为保持拒绝。

## Decisions

### 1. 一个 active VisitSession 绑定一个 Owner world assignment，并容纳多个 Visitor

`VisitSession` 使用独立 `VisitSessionID`，不可变绑定 `account.PlayerID` Owner、`personalworld.PersonalWorldID` 与完整 `placement.AssignmentStamp`。Store 对同一 PersonalWorld 维持最多一个 active VisitSession index；session 关闭后可以在仍然 current 的 assignment 上创建新 identity，assignment 改变时旧 session 永远不能迁移或复活。

Visitor membership 以 `(VisitSessionID, Visitor PlayerID)` 为作用域，一个 aggregate 内唯一；不同 VisitSession、未来 Party/Room/Activity membership 不使用裸 PlayerID 全局互斥。相比“一名 Visitor 一个 VisitSession”，单 aggregate 能在线性化点统一执行 capacity、Owner close 和批量 safe-return；相比把 VisitSession 放入 PersonalWorld，独立 owner 不会把临时 presence 写成持久世界事实。

### 2. Snapshot 使用封闭状态、正 revision 和有界规范集合

Session lifecycle 只包含 `open`、`owner_grace` 与 terminal `closed`。Invite 只包含 `pending` 与 `accepted`，过期/撤销 invite 从 active 集合移除，但其 command replay 结果仍由 store 保留；具体有界 retention/TTL 由后续 adapter change 定义，且不得短于对外承诺的重试窗口。Membership 只包含 `reserved`、`joined` 与 `reconnecting`：accept 原子创建 reserved membership，join 进入 joined，disconnect 进入 reconnecting，leave/kick/close/expiry 删除 active membership。

Capacity 只计算 reserved/joined/reconnecting membership，不计算 Owner 或尚未 accept 的 invite；因此可以向多名候选发邀请，但并发 accept 只有容量范围内的请求成功。Aggregate 使用私有且有上限的集合，snapshot 按稳定 identity 排序并返回副本；hydrate 拒绝重复 Visitor/invite、未知状态、越界数量、非法时间关系、assignment 不一致和 revision 0。每个首次 mutation 只把 session revision 增加一，批量 close 也只增加一次。

### 3. Application 只消费受信 actor、world 与 current assignment

Owner/Visitor actor 从 `session.AuthContext` 提取 PlayerID、SessionID 与 epoch，command 不接受可替换这些字段的 payload identity。`OwnedWorldReader` 只解析 actor 的 active PersonalWorld，`CurrentAssignmentReader` 只返回权威 current assignment snapshot；Service 在 open、accept、join 和 reconnect 要求其为 active、lease 在 `observedAt` 仍有效且完整 stamp 匹配。Assignment invalidation 也必须重新读取该 port，只有权威结果为 missing、expired、非 active 或不同 stamp 时才能关闭旧 VisitSession；依赖报错不能伪装成 invalidation 证据。

`ConnectionBindingID` 是由未来 connection registry 生成的有界 opaque identity，只用于拒绝旧 disconnect/reconnect callback，不保存 socket、endpoint 或 connection 对象。Owner 与每个 joined/reconnecting Visitor 分别保存 `(SessionID, epoch, binding)`；epoch 或 assignment 不匹配一律不能恢复旧 membership。

相比让客户端提交 world/instance 或让 invite 内嵌 endpoint，这些 query ports 保持权威来源单一；相比让 VisitSession 自己启动 WorldInstance，placement 仍是运行承载唯一 owner。

### 4. Invite acceptance 只创建 reservation 与 AdmissionIntent

Owner 在 open session 中为一个非 Owner 目标创建有绝对 expiry 的 `InviteID`。Invite 不预占 capacity、不包含 bearer secret，也不能直接调用 join。Accept 必须由目标 Visitor 的受信 AuthContext 发起，在同一 store transition 中检查 invite pending/未过期、session open、Owner 可用、session 未过期、current assignment 完整匹配、Visitor 尚无 membership 和 capacity，然后把 invite 标为 accepted并创建 `reserved` membership。

Accept 结果包含 `AdmissionIntent`：VisitSessionID、Visitor、Visitor SessionID/epoch、assignment stamp 与不晚于 invite/session/assignment lease 的 reservation expiry。Intent 是后续 admission issuer 的输入投影，不具备签名、nonce 或连接权限，默认格式化必须脱敏。Join 只接受由未来 admission verifier 标记为受信的 qualification，并再次比较 membership、epoch、assignment、expiry 与 Owner availability；本 change 不定义如何在线上编码或验证该 qualification。

### 5. Disconnect/reconnect 使用 binding + absolute deadline 防止 ABA

Owner disconnect command 必须携带当前 owner binding；精确匹配时 session 从 open 进入 owner_grace，保存 grace generation/deadline。Owner 在 deadline 前使用 actor 仍为 immutable Owner 的当前有效 AuthContext 与新 binding 恢复 open，并以新的 SessionID/epoch 替换旧 Owner auth binding。ExpireOwnerGrace 必须同时比较 VisitSession revision、grace generation、旧 binding 与 deadline；旧 callback/timer 即使延迟到新连接之后也只能返回 stale/replay，不能关闭恢复后的 session。

Visitor disconnect 同样只对匹配 joined binding 生效，进入 reconnecting 并保存不晚于 session expiry 的 deadline。Reconnect 必须由同一 Visitor、匹配 session epoch、current assignment 与新 binding 在 deadline 前完成；旧 binding 的 leave/disconnect 不得删除新 binding。Visitor reconnect 到期只删除该 membership，并为可能仍停留在旧 instance 的 Visitor产生 safe-return directive，不关闭其他成员。

Domain 不启动 timer 或 goroutine。调用方根据 snapshot deadline 调用显式 invite、reservation、Visitor reconnect、Owner grace 与 session expire command；invite/reservation expiry 分别移除 pending invite 或 reserved membership，reservation 尚未 join，因此不生成 safe-return。所有命令携带受信 `observedAt`，到达 deadline（`observedAt >= deadline`）即视为过期。Application 在首次生成 target 时执行配置 policy 上限，aggregate 仍执行全局安全闭区间；replay probe 不以推进后的 `observedAt` 重新解释已提交 deadline。所有时间在构造时移除单调分量并规范为 UTC 微秒，保证未来 Redis codec 和首次结果/replay 精确相等。

### 6. Store 以稳定 command identity 原子比较 expected revision

`VisitSessionStore` 提供 active session create/resolve、snapshot lookup 与 aggregate-specific transition commit。State-changing command 使用有界 `CommandID` 和由 operation、existing VisitSession、actor、expected revision、目标 identity、binding/deadline 等稳定字段生成的 SHA-256 fingerprint；open create 改为绑定目标 PersonalWorld、assignment、capacity 与 session lifetime，不绑定每次重试都可能重新生成的 candidate VisitSessionID。重试时重新读取的 `observedAt` 不进入 fingerprint，真实目标 deadline 进入 fingerprint。

Store 在单一线性化点先决议相同 CommandID 的 replay/conflict，再比较 active index、expected revision 与规范 target，提交 snapshot和完整 result/directives。Outcome 固定区分 created/existing、applied/replay、not-found、revision/idempotency/capacity/stale/invalid-state conflict、not-committed 与 commit-unknown。Application 严格校验 outcome、snapshot、fingerprint、revision 和 directives 组合；矛盾结果作为 dependency defect。Callback 不自动重放，commit-unknown 只能复用相同 command identity 解析。

相比让 Service load 后直接覆盖 store，这一契约能证明并发 accept 不超 capacity、旧 binding 不删除新连接；相比把每种业务 outcome 只塞进 error，显式结果能区分安全重试与未知提交。

### 7. Safe-return 是确定性结果，不是网络 side effect

`SafeReturnDirective` 绑定 VisitSessionID、Visitor、离开原因与目标偏好，原因封闭为 voluntary-leave、kicked、owner-closed、owner-unavailable、session-expired、assignment-changed、visitor-reconnect-expired 与 dependency-lost。目标只表达 `own_personal_world` 优先、不可用时 `safe_entry` fallback；它不包含 endpoint、ticket 或可由客户端选择的 world identity。

Leave/kick 针对单个 joined/reconnecting member返回一条 directive；close/Owner grace expiry/session expiry/assignment change 对全部 joined/reconnecting members返回按 VisitorID 稳定排序的 directives，reserved membership 只被取消，因为尚未获得 join 资格。Directives 是 store replay result 的一部分，响应丢失重试不得遗漏、重复扩展或改变原因。实际通知、own-world resolve/start 与连接迁移由后续 application/transport vertical slice 实现。

### 8. 权限 policy 只覆盖 VisitSession 控制面并默认拒绝

Owner-only operation 包括创建/撤销 pending invite、kick 与显式 close；Visitor 只能以自身身份 accept、join、leave、disconnect/reconnect。System expiry/assignment invalidation 使用独立 system command，不伪造 Owner。Visitor 永远不能成为 Owner、邀请第三方、修改 capacity、关闭其他 membership 或取得 PersonalWorld mutation 权限。

本 package 可以输出 `RoleOwner`/`RoleVisitor` projection 供后续 interaction authorization 使用，但 Owner 必须匹配当前 auth lineage，Visitor 必须已经 `joined` 或正在 `reconnecting`；terminal session、pending invite 与 `reserved` membership 均返回 `RoleUnspecified`。Package 不提供“Visitor 可以执行任意 world command”的 generic allow。未来每个 gameplay interaction 仍须独立登记 actor role、mutation owner、settlement owner、idempotency 与最终 transaction/fence；本 change 不创建 PlayerState/PersonalWorldState 双写。

### 9. 只交付 pure Go core，不提前接线 storage/transport

Production package 只包含 domain/application、ports、errors/outcomes、安全 projection，以及未来 admission verifier 必须消费但无法由包外伪造的 `JoinQualification` 类型。Reference store、fake clock/ID、fake world/placement reader 与 qualification 构造 fixture 只存在于 `_test.go`；在后续 admission change 前不得开放 public constructor。正式 Composition Root 不构造 VisitSession service，不新增 lifecycle component、background cleanup、listener、handler、Redis definition 或 protocol registry。

Unit/table/fuzz/race 验证 snapshot hydration、状态转移、容量竞争、幂等 replay、commit-unknown、deadline 边界和 stale callback。Production Redis adapter、TTL/replay key、cleanup owner 与 flush/restart integration 由后续独立 storage change 设计；P0 再冻结 admission credential 与 wire contract。

## Risks / Trade-offs

- [单 aggregate 管理多名 Visitor，snapshot 与 CAS 成本随 capacity 增长] → capacity/invite 数量设置硬上限并使用稳定有界集合；真实规模证据出现后才考虑分片，不提前引入分布式锁。
- [Invite 不预占 capacity，用户可能接受时才发现已满] → accept 在线性化点返回明确 capacity conflict；避免未响应 invite 长期占位和复杂 reservation cleanup。
- [Redis adapter 尚未实现，core 不能用于正式联机] → 正式 Composition Root 保持未接线；reference store 只验收契约，不作为 memory fallback。
- [AdmissionIntent 被误当 credential] → 类型默认脱敏且文档明确无签名/nonce/授权；join 必须依赖后续 verifier 的受信 qualification。
- [调用方漏调 expire command 导致逻辑记录滞留] → 每个读取/变更仍以绝对 deadline fail closed，过期值不能恢复资格；后续 adapter 必须同时设置 TTL 和 cleanup owner。
- [Owner/Visitor reconnect 与 delayed callback 竞争] → 所有 callback 比较 session epoch、binding、generation/deadline 和 expected revision，旧命令只能 replay/stale。
- [批量 close 的 safe-return 通知可能部分发送] → core 只原子保存完整 directive result；后续通知投递必须按 directive identity 幂等，不在本 change 伪装跨系统原子发送。
- [Assignment change 可能牺牲可用性] → 旧 VisitSession fail closed 并 safe-return；不把 membership 静默迁移到新 instance，后续重新邀请/accept 获得新资格。

## Migration Plan

1. 创建 `internal/visitsession` identity、time/capacity policy、角色、snapshot、invite/membership 与 hydration，不依赖 infrastructure。
2. 实现纯 aggregate transitions 与 safe-return projection，覆盖 owner/visitor binding、grace、expiry 和 assignment/epoch 校验。
3. 定义 Store/query/clock/ID ports、稳定 command fingerprint、outcomes/errors，并实现严格 application service result 验证。
4. 添加 reference store、deterministic fakes、table/fuzz/race tests，覆盖并发 capacity、replay/conflict/unknown 和 stale callback/timer。
5. 更新目录/README/roadmap，运行项目 Go 入口、vet/mod verify、OpenSpec strict 与仓库卫生检查；不接线 production graph。

回滚时删除未接线的 VisitSession core并回到 `f7728ae`。没有 production table/key、credential、listener 或外部协议需要迁移；后续 change 只能在本 capability 归档后实现 storage/protocol。

## 已确定参数

- VisitSession/Invite/Command/ConnectionBinding identity 使用独立 namespace、有界安全 ASCII，最大 128 bytes；command fingerprint 使用 SHA-256。
- Visitor capacity 为 1-32，pending invite 最多 64；Owner 不计入 capacity，pending invite 不预占 capacity。
- Session lifetime 为 1 分钟至 24 小时；invite lifetime 为 1 秒至 1 小时，join reservation 为 1 秒至 2 分钟，Owner grace 为 1 秒至 5 分钟，Visitor reconnect grace 为 1 秒至 2 分钟，这四项配置值分别作为 application deadline 上限。所有子 deadline 不得晚于 session expiry，admission intent 也不得晚于 current assignment lease。
- Adapter-facing absolute time 统一为 UTC 微秒；等于 deadline 即失效。
- Production package 不含 goroutine、timer、mutable global、memory store、generated type 或 backend client。
