## Context

P0 已把 world admission 冻结为安全 ASCII opaque credential，V0/V1 已提供 `AdmissionIntent`、完整 VisitSession replay 与构造入口封闭的 `JoinQualification`。当前缺口是独立的生产 issuer/verifier：ConnectionTicket 只能认证 TLS/TCP connection 与 session lineage，不能授予 Owner/Visitor role 或 world target；Redis 也没有保存一次性 admission binding 与消费状态的 owner schema。

本 change 横跨安全 credential、VisitSession 受信桥接、placement current 检查、Redis key registry 和 semantic fixtures，但仍处于 listener 之前。所有时间在 domain/store 内使用 UTC 微秒，wire 毫秒投影留给后续 adapter。

## Goals / Non-Goals

**Goals:**

- 提供可由未来 HTTP application 调用的幂等 issuer，以及可由未来 TLS/TCP handshake/dispatcher 调用的单次 verifier。
- credential 对客户端完全 opaque，raw value 只在 issue result 和 verify input 短暂存在，默认格式化、错误、日志、Redis 和 metrics 均不泄漏。
- 在真实 Redis 中原子提交 issuance replay 与 credential binding，并使同一 consume identity 能解析 response-loss，其他重复消费稳定拒绝。
- verifier 同时比较认证 actor/session epoch、purpose、endpoint/channel 和 current full AssignmentStamp；Visitor qualification 继续由 VisitSession application 重新比较 membership/deadline。
- 让 semantic fixture 的每个条件都映射到生产 runtime 或 VisitSession 组合测试。

**Non-Goals:**

- 不实现 HTTP DTO/handler、TLS/TCP framing/dispatcher、WSS、公开 listener、连接 registry 或 Go/Unity 客户端。
- 不接入正式 Composition Root，不新增环境变量或 production secret 文件；后续接线必须从 secret owner 注入 derivation key。
- 不改变 OpenAPI/Protobuf/registry，不实现 Room、Party、ActivityInstance、world mutation 或 safe-return side effect。
- 不把 Redis 变成持久事实来源，也不在 flush 后恢复旧 admission。

## Decisions

### 使用 keyed deterministic opaque credential 与 digest-only Redis record

credential 使用固定版本前缀和 HMAC-SHA-256 的 base64url 结果。HMAC 输入采用 domain separator、稳定 issuance ID 与完整 binding fingerprint 的长度前缀编码；derivation key 必须由 secret owner 注入且至少 32 bytes。Redis key 和 Hash 只保存 credential SHA-256 digest，不保存 raw credential、HMAC key 或可逆密文。

这使相同 issuance ID 与相同语义在 Redis response-loss 后仍能重新推导首次 raw credential，同时相同 ID 改变 target/binding 会由 fingerprint conflict 拒绝。相比随机 handle 加明文 replay record，本方案不在 Redis 保存 bearer secret；相比 signed claims，服务端不会把内部 AssignmentStamp 布局暴露给客户端，也能由 Redis 原子撤销和消费。

### issuance 与 credential 使用两个短期 Hash

`worldadmission:issue:<issue-id-digest>` 保存 fingerprint、credential digest 与绝对 expiry；`worldadmission:credential:<credential-digest>` 保存完整受信 binding、状态和首次 consume identity。owner Lua 在同一 standalone Redis 线性化点创建两者；issuance replay 交叉验证两个 Hash，缺失、unknown schema、字段不完整或不一致均作为 defect fail closed。

两个 key 都使用 credential expiry 加有界 replay retention 的 physical expiry。业务有效性始终比较 `expires_us`，physical TTL 只保留 response-loss/replay 证据，不能延长资格。

### 原子消费先验证静态 binding，再由 service 复核 current assignment

consume Lua 在写入 consumed 状态前比较 credential digest、PlayerID、SessionID/epoch、purpose、TLS/TCP endpoint/channel 与业务 expiry。同一 consume ID/fingerprint 重试返回首次 binding；不同 consume identity 返回 replayed。Redis dependency error保持 commit-unknown，调用方只能用同一 consume identity解析。

Lua 不能安全读取由 placement owner 编码的多 key current state，也不应复制其 schema。因此 service 在原子消费后通过窄 `AssignmentReader` 读取 current active assignment，比较全部 stamp 与 lease。assignment 改变、过期或不存在时 credential 已被烧毁并返回 stale；dependency 暂时失败时同一 consume identity可重试并取得同一 binding后复核。相比先读 assignment 再消费，此顺序不会让并发迁移后的旧 credential 留在可用状态。

### Qualification 是 verifier 的只读结果，不替代领域二次校验

`worldadmission.Qualification` 只在成功 verify 后构造，包含受信 actor、role、target、purpose、full assignment 与 expiry，不包含 raw credential。Visitor 结果通过显式 bridge 构造 `visitsession.JoinQualification`；Join 与 VisitorReconnect 分别要求 `JOIN` 与 `RECONNECT` purpose，并继续重新读取 current assignment、比较 reserved/reconnecting membership、session lineage、deadline 与 connection binding。

Go 无法表达跨 package 的 friend constructor，因此 bridge 是导出的 hydration API，但它要求完整受校验字段并在注释、structure test 与调用点门禁中限定 owner；invite、`AdmissionIntent` 或 payload 本身仍不能直接调用 Join。

### issuer 接收已由 application 派生的完整 Binding

本 change 不提前实现 HTTP orchestration。`Binding` 的构造器只接受 domain value object，并强制 role/purpose/VisitSession 组合、TLS/TCP endpoint、issued/expiry、配置上限与 assignment lease。后续 HTTP application 必须从 AuthContext、PersonalWorld/VisitSession 与 placement reader派生这些值；handler 不得直接把请求字段映射到 Binding。

### semantic fixture 继续不包含 claims，但声明 runtime 已实现

fixture 保留抽象 state/purpose/condition/outcome，不新增 raw credential、内部 consume identity、epoch、assignment 或签名字段。`runtimeImplemented` 改为 `true`，validator 要求 corpus 完整；worldadmission 测试逐项覆盖成功、purpose mismatch、expiry、credential replay、stale epoch/assignment、wrong endpoint/channel 与 membership missing，并真实调用 VisitSession 的 Join/VisitorReconnect 二次校验。

## Risks / Trade-offs

- [derivation key 轮换会改变同一 issuance ID 的 credential] → 本 change 明确单 key、短 TTL 边界；正式接线前若需要无中断轮换，必须独立设计 key ID/keyring，不能静默改变 token layout。
- [消费成功后 placement dependency 短暂失败] → 保留 consumed binding 和首次 consume identity到 replay retention；相同请求可重试复核，不允许换 identity 绕过一次性限制。
- [导出的 VisitSession hydration bridge 可被错误调用] → constructor 验证全部字段/purpose，生产调用点由 structure test 限定为 worldadmission，service仍执行 auth、membership、deadline与current assignment二次校验。
- [两个 Redis Hash 增加短期内存] → value schema有严格字节预算，TTL上限短且无长期索引；不增加 player/world secondary index。
- [Redis flush 会丢失 issuance replay] → 旧 credential 与重放结果一并失效并 fail closed，调用方使用新 issuance identity重新获取资格；不从MySQL、日志或memory补回。

## Migration Plan

1. 先注册两类 Redis definition 和 codec/scripts，但不接入 Composition Root。
2. 交付 issuer/verifier、VisitSession bridge 与全部 unit/integration/semantic tests。
3. 更新文档并保持当前公开进程行为不变；后续 HTTP/TLS-TCP changes 注入 store、key 与 readers。
4. 回滚时移除未接线代码和 registry definitions即可；已存在的短期 key 会按 physical expiry自然删除，无持久数据迁移。

## Open Questions

无。key rotation、公开 adapter 错误映射和连接 registry 生命周期分别留给后续有明确 consumer 的 change。
