## Context

服务端已经拥有唯一 Composition Root、公开 HTTPS/WSS、生产 MySQL/Redis runtime、Session ticket 原子消费、WorldAdmission issuer/verifier，以及冻结的 world/visit Protobuf、message/route/error registries。当前 `TLS_TCP` route 2000-2002、2103-2122 仍没有生产 listener、framing codec、connection registry 或 dispatcher，因此已签发的 gameplay ticket 与 world admission 无法形成真实权威业务连接。

该通道是 N0 的最后一个 transport capability，但不是完整业务竖切。它必须把网络、认证、admission、连接资源与窄 application port 接好，同时避免把 world/visit 最终事实、系统 lifecycle producer、跨通道通知编排或 Go 协议客户端塞进 transport owner。

## Goals / Non-Goals

**Goals:**

- 建立独立、production TLS 1.3、local/test 仅 loopback 可明文的 gameplay TCP listener，并冻结 pre-auth handshake 与 4-byte big-endian framing。
- 原子消费 `TLS_TCP` ConnectionTicket 与 world admission，构造不可由 payload 覆盖的 AuthContext、Qualification 和 connection target binding。
- 只按 registry 接收 2000、2103/2105/2107/2109/2111/2113/2115/2117/2119，产生匹配 response/error，并允许可信 owner 投递 2002、2121、2122 push。
- 对 read buffer、frame batch、writer queue、连接索引、deadline、rate 和 shutdown 建立可计算硬上限。
- 将 session invalidation、readiness、启动回滚、受监督 accept loop 与有界关闭接入现有进程生命周期。
- 通过无 listener 单元/fuzz/race、临时 TLS、真实 Redis 与 production graph 的分层验收证明安全边界。

**Non-Goals:**

- 不新增、重编号或跨通道复制 world/visit message，不定义 generic world action、chat、inventory、task、economy 或 battle 协议。
- 不实现完整 own-world/visit-world orchestration、WSS 业务 producer、Owner grace/expiry cleanup 调度、safe-return 目的地加载或 Go 协议测试客户端。
- 不让 transport 保存 PersonalWorld、VisitSession、assignment、presence、奖励或其他最终事实，也不建立 Redis connection key 或 memory fallback。
- 不启用 UDP/KCP，不拆分 gateway/game server，不增加 gRPC 或第二套 service graph。

## Decisions

### 1. TCP 使用独立 listener 与 transport-owned pre-auth handshake

Gameplay TCP 使用独立配置地址和 advertised `TLS_TCP` endpoint；不能复用 HTTP/WSS listener，也不能出现在 diagnostic listener。Production listener 在 accept 后立即执行 TLS 1.3 handshake，local/test 明文仅允许实际 bind address 与 remote address 都是 loopback。

TLS 后第一帧不是 `ReliableEnvelope`，而是 transport-owned、版本化的 authentication preface：固定 magic/version、封闭 purpose byte、ticket byte length、admission byte length，以及两段安全 ASCII opaque credential。purpose 仅选择 verifier 的消费指纹，最终授权仍必须与 verifier 返回的 binding 完整相等。整帧仍使用 4-byte unsigned big-endian长度前缀并受独立 handshake size/deadline 限制。成功后才切换为 `ReliableEnvelope` stream；失败只产生稳定 close/failure kind，不回显 credential 私有状态。

选择固定 preface 而不是新增业务 message ID，是因为连接 ticket 与 admission 都发生在 dispatcher 之前，不具备 GAMEPLAY AuthContext 的客户端不能先发送 registry message。相比 JSON、换行文本或在 TLS ALPN/SNI 中携带 secret，固定二进制 preface 更容易限制长度、处理半包并避免凭据进入 URL、header 或日志。该格式必须进入协议文档、contract fixture 与 Go/C# 后续实现基线。

### 2. 握手按 ticket 后 admission 的顺序建立两层资格

accept 前先取得全局/remote reservation；读取并完成语法校验后，以 listener 的受信 advertised endpoint 和 `ChannelTLSTCP` 原子消费 ticket。只有结果精确授予 `GAMEPLAY` scope，才构造只读 AuthContext 并绑定 SessionID/PlayerID 预算。随后以服务端 CSPRNG ConnectionID 派生稳定 consume identity，调用现有 WorldAdmission verifier 原子消费 admission，并复核 current full AssignmentStamp。

任一步失败都关闭连接并释放 reservation；已经提交的 ticket 或 admission 不补偿、不恢复。Qualification 只以只读安全投影保存在 connection entry，不保存 raw ticket/admission。`OWN_WORLD` qualification 使连接进入 active target；`JOIN`/`RECONNECT` qualification 使连接进入 pending membership 状态，并且 admission deadline 前只允许匹配的首个 VisitJoin/VisitReconnect command。

现有 Join/Reconnect payload 仍携带 admission credential。Dispatcher 使用同一 connection consume identity再次调用 verifier，利用已定义的 response-loss 精确重试恢复相同 Qualification，再与握手绑定逐字段比较后调用 application service；成功才把 pending connection 转为 active。这样不改变已发布 schema，也不会产生第二次消费或允许 credential 切换 target。

WorldAdmission expiry 受短期 credential policy约束，可能早于 VisitSession reservation/reconnect deadline。VisitSession因此要求 qualification deadline不晚于对应membership deadline且调用时两者均有效；短期credential不能延长membership，也不能因为被安全收紧而与原reservation错误冲突。

### 3. Framer 与 typed codec 分层并完全由 registry 驱动

Framer 只负责 4-byte unsigned big-endian length、零长/超长/截断、partial read、batched frames 与每连接累计缓冲预算。Codec 再解析 `ReliableEnvelope`，校验 protocol version、message ID、kind、direction、channel、scope、route max size、request/command identifier、sequence/timestamp 和精确 generated payload type。

Catalog 从现有 contract runtime projection 构造，不复制 message table或本地 DTO。C2S 只接受 REQUEST/COMMAND route；RESPONSE/PUSH/ERROR、WSS message、未知 enum/ID、错误 correlation 和 payload type 在 application 调用前拒绝。S2C 使用 deterministic Protobuf encoding，并对完整 envelope 与 frame 同时执行 route/global budget。

### 4. Dispatcher 只依赖按 operation 收窄的 application ports

Dispatcher 把 AuthContext、bound Qualification、ConnectionBindingID、envelope identity 和 typed payload映射到现有 PersonalWorld/VisitSession application command。它不读取 Gin、socket、Redis adapter 或 generated type 之外的 transport state，也不根据 payload构造 actor、role、world、assignment 或 session lineage。

每个 route 具有固定 handler、rate policy、deadline 与允许 connection state。World snapshot/Visit snapshot 是 request；Visit mutation 是 command；响应使用输入 request ID 或 command ID 作为 correlation。唯一 reader 同步调用 dispatcher，使单连接 application in-flight 固定为 1 并保持输入顺序；transport 不维护第二份 pending fingerprint 或缓存已提交结果，跨重连幂等事实仍只由 application store 拥有。

系统 lifecycle command 不注册 C2S handler。Transport 可以接线已经存在的窄 application port，但业务 producer、定时 cleanup、跨通道通知与完整入口编排留给 `complete-server-personal-world-slice`。

### 5. Connection registry 只拥有连接引用与线性化 target binding

Registry 以 CSPRNG ConnectionID 为主键，维护 SessionID、PlayerID、PersonalWorldID 和可选 VisitSessionID 的发送索引。Entry 只保存 AuthContext摘要、Qualification/binding摘要、connection handle、队列、route rate state 和 lifecycle state；不保存 aggregate snapshot、application pending cache 或 adapter。

全局、remote、session、player 与 target 连接数均有启动时验证的上限。reserve/commit/release、pending-to-active、移除、按 session/target close 和投递必须线性化；所有退出路径恰好一次删除主/反向索引。Remote pre-auth rate state具有数量与 idle TTL 硬上限，只存在进程内。

相比全局 event bus 或按业务 package 自建连接表，单 owner registry 能确保背压、关闭和 session epoch 失效的一致语义，同时仍通过窄 publisher 接口隔离业务 owner。

### 6. 单 reader、单 writer 与共享连接内存预算

每连接只有一个 reader loop 和一个 serialized writer。Reader 使用固定上限 scratch buffer、未完成 frame buffer、每批最多 frame 数、同步 dispatch 和 read/idle deadline；它不为声明长度预分配无界内存。Writer queue 同时限制 item 和完整 encoded bytes，write deadline、OS TCP keepalive参数与总 idle deadline受配置约束；本change不创建未登记的application heartbeat frame。

队列满、write timeout、sequence violation、unexpected kind、frame budget、rate abuse、keepalive/idle failure 与 peer close映射到稳定 close reason。消息不能静默丢弃后继续伪装健康；slow consumer 必须关闭。全部 goroutine、timer、queue bytes 和 close wait 都由 connection owner 有界回收。

### 7. Response/push 投递保持目标和 correlation 不可伪造

Dispatcher 返回内部 result，由 connection writer统一分配 S2C sequence、server timestamp并构造 response/error；业务 adapter不能直接写 socket。Trusted publisher只允许 registry声明的 TLS/TCP S2C PUSH，并按 ConnectionID、PlayerID、PersonalWorldID或VisitSessionID选择目标。Publisher提交的 payload不能覆盖目标身份，wrong channel/type/target fail closed。

`VISIT_SAFE_RETURN_PUSH` 只发给 directive 中目标 Visitor当前绑定的 gameplay connection，并在排队后触发该 connection target进入 returning/closing 状态；WSS `VISIT_CLOSED_NOTICE_PUSH` 仍仅收敛控制面。`WORLD_SNAPSHOT_PUSH`/`VISIT_SNAPSHOT_PUSH` 使用完整替换投影，但不把 snapshot存进 registry。

### 8. Session invalidation 使用组合 invalidator，关闭服从共享 deadline

Session service仍只依赖一个 `ConnectionInvalidator`。Composition Root提供组合实现，在同一有界 deadline内并行尝试 WSS 与 TCP invalidator，聚合稳定结果且不会因一个 adapter失败跳过另一个。TCP invalidator先停止目标旧 epoch的新投递，再尽力发送安全 ERROR/close语义并无条件关闭；通知失败不回滚权威 epoch。

进入 draining 时先停止 TCP accept和HTTP/WSS新握手，再停止全部可信 publisher，并在共享 shutdown budget内并行关闭/等待 TCP 与 WSS连接；之后关闭普通 HTTP in-flight，最后逆序释放 Redis、MySQL 与 diagnostic。Listener bind、TLS config、catalog/dispatcher构造或受监督 accept loop失败都撤销 readiness并触发完整回滚；单连接协议/网络错误只影响该连接。

### 9. 观测只记录稳定低基数结果

Metrics覆盖 TLS/handshake、active/rejected、frame/message bytes、dispatch latency/result、in-flight、queue、rate、keepalive/idle、slow consumer、push、close、invalidation与shutdown，label只使用稳定 operation/message ID/status class/reason。日志可记录随机 ConnectionID和受控 identity摘要，但不得记录 raw ticket/admission、digest、IP、payload、完整 principal、full assignment、invite、backend error文本或未约束 target ID。

Panic、dependency error与畸形输入均由边界 recovery映射为稳定 failure kind；transport error不能泄漏Redis/MySQL/TLS内部文本，也不能被误报为业务 not-found或成功。

## Risks / Trade-offs

- [握手同时提交两类一次性凭据，任一后续失败会烧毁先提交凭据] → 明确不补偿语义、短 TTL 和 HTTPS 重新签发流程，并用相同 consume identity覆盖 response-loss重试。
- [JOIN/RECONNECT credential在pre-auth和首个command中出现两次] → 全程TLS、绝不存raw value；第二次只允许同consume identity恢复并逐字段匹配已绑定Qualification，后续command拒绝credential切换。
- [独立TCP listener增加端口、证书和关闭复杂度] → 复用统一TLS loader、TaskOwner与配置验证；listener/registry不拥有storage lifecycle，启动失败统一逆序回滚。
- [每连接并发dispatch可能破坏命令顺序] → command默认按reader顺序串行；只允许有硬上限的独立read request并发，且response correlation不改变S2C sequence。
- [完整业务producer尚未接线导致transport capability易被误称为竖切完成] → integration只证明握手、admission、dispatcher和真实adapter组合；roadmap、README和spec明确保留`complete-server-personal-world-slice`与`qualify-server-v1`门槛。

## Migration Plan

1. 先补齐配置、contract projection、authentication preface fixture与无listener codec/registry测试，不开放端口。
2. 实现ticket/admission handshake、connection registry、dispatcher和publisher，并以fake ports完成单元、fuzz与race验收。
3. 在Composition Root构造完整组件后再bind独立listener，接入组合invalidator、readiness、回滚和shutdown。
4. 扩展真实storage integration harness，以临时TLS和Go wire client覆盖三种purpose成功路径、ticket重放、失效和关闭；stale、wrong endpoint、framing与backpressure继续由相应storage/transport分层测试覆盖。
5. 更新owner文档并strict验证后开放配置。回滚时关闭TCP endpoint/配置即可恢复到HTTPS+WSS基线；一次性credential自然到期，不需要数据迁移或回填。

## Open Questions

无。Authentication preface版本、budget、deadline与close vocabulary必须在实现开始时由配置/contract tests固定，不能推迟到客户端 change 临时决定。
