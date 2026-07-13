## Context

服务端已经具备严格配置、Composition Root、生命周期、诊断和受控关闭，跨端契约也已经定义 session id、session epoch、token pair、connection ticket、channel、endpoint 与 scopes。当前缺少的是执行这些契约的身份核心：如果账号、HTTPS、WSS 和 TLS/TCP 各自实现 token 或 ticket，会形成多个身份来源，无法原子处理 refresh 重放、一次性消费和全通道失效。

本 change 位于网络 adapter 与存储 adapter 之前。实现必须能依靠 fake clock、确定性 secret generator 和并发安全测试 store 独立验收，但生产运行时不得接入 memory store 或创建尚无真实消费者的配置。Redis、HTTP 登录与实时握手由后续 change 实现接口。

## Goals / Non-Goals

**Goals:**

- 建立 session、principal、epoch、scope、auth context 与失效原因的唯一领域语义。
- 建立 opaque access/refresh token 的签发、验证、原子轮换、expiry 与 replay 行为。
- 建立绑定 channel、endpoint、scope、epoch、16-byte nonce 和 expiry 的一次性 connection ticket。
- 让 logout、forced logout、ban 和安全失效统一递增 epoch，并通知连接边界撤销旧身份。
- 定义能由 Redis/transport adapter 实现的窄接口和明确原子操作，不泄漏 socket、generated type 或存储命令。
- 通过 deterministic、concurrency 与 race tests 证明 token/ticket 不能被重复消费或跨边界使用。

**Non-Goals:**

- 不校验用户名、密码或账号状态，不实现注册、登录 handler 和账号 repository。
- 不连接 MySQL/Redis，不向正式 Composition Root 注入 memory store。
- 不启动 HTTPS、WSS、TLS/TCP、UDP/KCP listener，不实现实际连接 registry。
- 不修改 OpenAPI、Protobuf、message/error registry 或现有 fixtures。
- 不实现 JWT、自签名 ticket、密钥轮换、OAuth/OIDC 或第三方身份提供商。
- 不实现个人世界、VisitSession、presence、reconnect、ban policy 来源或 Unity 客户端。

## Decisions

### 1. Session 是唯一身份事实，epoch 是全通道撤销屏障

Session record 使用服务端生成的 `session_id`、经过上游账号域确认的 principal、从 1 开始的 `session_epoch`、状态和绝对 expiry。Access token、refresh token、ticket 与 connection auth context 全部绑定同一 session id/epoch；授权时必须读取当前 session 状态和 epoch，不能只相信凭据自身携带的值。

Logout、forced logout、ban 输入和安全重放处理都通过 store 的原子 invalidation 操作递增 epoch并撤销该 epoch 的 token/ticket。Epoch 不回退、不复用；旧连接即使尚未收到通知，也会在下一次授权检查时失败。

替代方案是维护 token blacklist 或让每个 transport 单独注销。Blacklist 容易遗漏新凭据类型，分散注销无法证明跨通道一致性，因此拒绝。

### 2. Token 使用 opaque secret，connection ticket 使用结构化绑定与一次性 nonce

Access/refresh token 由 `crypto/rand` 生成 32-byte entropy，使用无 padding base64url 编码并带稳定类型前缀，便于拒绝错用凭据类型。Connection ticket 按既有跨端契约携带 session、epoch、channel、endpoint、scopes、时间与 16-byte one-time nonce；核心返回纯 Go 领域投影，后续 adapter 才转换为 Protobuf 与 HTTP `ticket` string，不把 generated type 引入身份核心。

Store 只保存 token secret 或 ticket nonce 的 SHA-256 digest 与必要绑定。明文只在签发、协议编码和入站认证边界短暂存在；相关类型的默认格式化与 `slog` 输出始终脱敏，错误不得包含原值。高熵随机材料使用 SHA-256 digest 足以抵抗离线反推，但该方案不用于密码等低熵输入；不得将明文作为 map key、Redis key、metrics label 或日志字段。

替代方案是 JWT 或自定义签名 envelope。当前仍需要服务端状态完成 epoch、refresh rotation 和一次性 ticket 消费，自包含签名不会消除存储依赖，却会增加 claim 演进、算法与 key rotation 边界，因此拒绝。

### 3. Store interface 直接表达必须原子的安全操作

Session core 不暴露通用 CRUD repository，而由消费侧定义 `SessionStore` 的安全操作：创建 session/token lineage、解析 access digest、原子轮换 refresh、原子消费 ticket、原子递增 epoch并撤销旧资格。返回稳定 outcome，区分成功、未知/过期、epoch 失配、已消费重放和依赖失败，但不向外部暴露记录是否曾属于某个账号。

Refresh rotation 在同一原子操作中撤销上一枚 access digest、把旧 refresh digest 变为 consumed tombstone并写入新 token pair。Tombstone 至少保留到原 session expiry，使并发刷新只有一个成功，旧 refresh 再次出现时能够识别 replay 并触发 session invalidation。Ticket consumption 同样必须是 compare-and-consume；“先读再删”不满足接口。

远程 store 的 context deadline 只限制等待，不能证明操作没有提交；因此错误时不返回预生成 credential，adapter 必须使用单个事务或原子脚本，并使重试收敛到 conflict、replay 或同一 invalidation。本 change 提供线程安全测试 store 证明接口语义，但不注册到正式 Composition Root。Redis key、TTL、事务脚本和故障模式由 storage change 按 `docs/redis-keys.md` 实现。

### 4. AuthContext 是不可扩权的只读值，adapter 只负责转换

Access 验证和 ticket 消费返回纯 Go `AuthContext`，包含 principal、session id/epoch、channel 与规范化 scopes。Context 不保存原 token/ticket、socket、HTTP request、generated Protobuf type 或可变 map。构造入口不导出，只有 session 认证流程能创建可信 context；scopes 使用去重、稳定排序的封闭集合并严格匹配 channel policy。

Application service 只接收 AuthContext 和业务输入。Payload 中出现 account/player/session 字段时只能作为目标或查询条件，不能替代 actor。替代方案是把 raw token 传入每个业务 service；这会复制认证逻辑并扩大凭据生命周期，因此拒绝。

### 5. Ticket policy 由受信任 channel/endpoint provider 决定

只有 HTTPS access AuthContext 可以请求 ticket，目标只允许现有契约支持的 WSS 或 TLS/TCP。Policy 固定 channel 到 scopes 的映射：WSS 只授予 control scope，TLS/TCP 只授予 gameplay scope；请求不能自带 scopes、host 或 port。Gameplay scope 只允许建立可靠业务连接，不授予 PersonalWorld、VisitSession、ActivityInstance 或其他 mutation。Endpoint 由 `EndpointProvider` 返回受信 manifest 项，签发结果与 store record 共同绑定 session、epoch、endpoint、channel、scope、nonce 和短 expiry。

Listener 消费 ticket 时必须提供自身 channel 与 endpoint identity，并通过单次原子操作校验和消费。Wrong-channel、wrong-endpoint、expired、wrong-epoch 和 replay 都 fail closed，不能降级为 access token 认证或切换 transport。

Policy 以经过验证的纯值对象构造，满足 `ticketTTL < accessTTL < refreshTTL <= sessionTTL` 且所有 duration 有安全上下界。真实部署配置等到 storage/transport 接线时加入，避免当前 runtime 接受无人消费字段。

### 6. Invalidation 先提交权威状态，再通过窄接口关闭连接

Session invalidation 首先在 store 原子递增 epoch并撤销旧 token/ticket，随后调用 `ConnectionInvalidator` 发布 session id、新 epoch 和安全 reason。普通 logout 只接受当前 `AuthContext`，forced logout 接受受信管理边界提供的 session id，调用方不能自行选择 reason；ban 输入针对 principal 撤销其全部现有 sessions。是否允许该 principal 后续重新创建 session 仍由账号域的 ban policy 决定。

ConnectionInvalidator 不暴露连接对象；WSS/TCP change 负责按 registry 索引关闭旧连接并发送适用 control push。

通知失败不能回滚已经生效的 epoch，否则旧凭据可能重新有效。Application 返回依赖错误并允许上层重试幂等通知；所有后续认证仍以 store 当前 epoch fail closed。日志只记录 session 安全摘要和 reason，不记录 token、ticket 或完整 key。

替代方案是先关闭连接再更新 session。两步之间的新 command 仍可能通过旧 epoch，因此拒绝。

### 7. 错误与测试保持 transport-independent

Session core 使用稳定错误 kind 表达 unauthenticated、forbidden、expired、replayed、conflict 与 dependency unavailable，但不直接选择 HTTP status、WebSocket close code 或实时 error message。Adapter 在既有 error registry 边界映射，内部 cause 只进入受控日志。

测试使用 fake clock、确定性 ID/secret generator、并发测试 store 和 fake invalidator，覆盖 expiry 边界、并发 refresh、ticket replay、wrong channel/endpoint、epoch invalidation、通知失败和 secret 脱敏。测试不启动 listener、数据库或 Docker；race test 必须覆盖所有原子消费路径。

## Risks / Trade-offs

- [Opaque 凭据要求每次认证查询 store] -> 后续 Redis adapter 提供有界超时与 metrics；依赖失败时 fail closed，不回退本地接受。
- [Refresh tombstone 增加短期存储] -> 只保存 digest、session id 和必要状态，并以 session expiry 作为最长 TTL。
- [Epoch 已提交但连接通知失败] -> 旧身份在权威校验上立即无效，通知幂等重试；transport 必须在 command 边界复核 epoch。
- [测试 store 被误用为生产实现] -> 放在 `_test.go` 或明确测试 package，不由 Composition Root 构造，也不提供运行配置开关。
- [固定 channel/scope matrix 随业务扩展] -> policy 使用封闭枚举和表驱动测试；新增 channel/scope 必须先更新契约和独立 OpenSpec change。
- [Session core 先于账号域] -> 创建 session 只接受上游已验证 principal，不推断账号存在性、凭据或 ban policy。
- [HTTP `ticket` string 与 Protobuf `ConnectionTicket` 尚无可执行编码映射] -> `add-server-http-bootstrap` 必须定义 deterministic mapping 和跨契约 fixture；adapter 不得临时拼 JSON、自创 JWT 或只返回裸 nonce。

## Migration Plan

1. 建立 session 值类型、policy、错误分类、token codec、ticket nonce/digest 与 deterministic tests，并复用 runtime 生产 clock/ID。
2. 定义原子 SessionStore、EndpointProvider、ConnectionInvalidator 接口和仅测试实现。
3. 实现 create/authenticate/refresh/invalidate application services，并验证 epoch 与 replay 行为。
4. 实现 ticket issue/consume、channel/scope policy 和 auth context 构造。
5. 执行 format、vet、unit/race、secret scan、协议 verify 与 OpenSpec strict；确认正式 Composition Root 未接入 fake store 或新 listener。

当前没有生产 session 数据或客户端消费者，失败时可整体回退本 change。后续 storage、account 与 transport changes 必须实现这些接口，不得并行建立第二套 token、ticket 或 epoch 语义。

## Open Questions

无。真实 TTL 数值、Redis key/value 和 transport close reason 在对应接线 change 中按本设计约束落地，不在尚无消费者时加入启动配置。
