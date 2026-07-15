# Server World Admission Runtime 规格

## Purpose

定义短期 opaque world admission credential、幂等签发、原子消费、current assignment 复核、VisitSession 受信桥接、Redis 恢复边界与未接线约束。

## Requirements

### Requirement: World admission credential 必须 opaque、短期且默认脱敏
系统 MUST 只签发带稳定版本前缀的安全 ASCII opaque credential，raw value MUST NOT 包含可由客户端解释或修改的 claims。credential MUST 由至少 256-bit 的注入 secret key、稳定 issuance identity 与完整 binding fingerprint 派生；raw credential、derivation key MUST NOT进入 Redis、日志、错误、metrics 或默认格式化。业务 expiry MUST 不晚于配置上限、session/VisitSession deadline 与 assignment lease 的最早值。

#### Scenario: Credential 被格式化或持久化
- **WHEN** credential、issue result 或 qualification 被普通格式化、结构化记录或写入 Redis
- **THEN** 输出只包含固定脱敏占位符或 digest/binding metadata，不包含 raw credential、HMAC key 或可伪造 claims

#### Scenario: 超过权威 deadline 的签发
- **WHEN** 调用方请求的 expiry 超过配置 admission lifetime、VisitSession deadline 或 current assignment lease
- **THEN** issuer 拒绝该 binding，不截断后静默签发也不返回部分 credential

### Requirement: Admission issuance 必须按稳定 identity 幂等且可解析提交不确定性
Issuer MUST 以稳定 issuance ID 和完整授权 binding fingerprint 原子创建 issuance/credential records。Fingerprint MUST 绑定 actor、role、target、purpose、session epoch、assignment、endpoint/channel 等稳定授权事实；相同 ID 改变任一稳定授权事实 MUST 返回 idempotency conflict。`IssuedAt` 与由当前服务端时钟计算的 candidate expiry 不属于客户端可变语义：仅因重试时钟推进得到更晚 candidate deadline 时，issuer MUST 重放首次 raw credential 与首次较短 expiry，MUST NOT 延长资格；当前 session、membership、assignment lease 或其他权威 deadline 收紧到早于首次 expiry 时，MUST 返回 idempotency conflict。Redis mutation 无法证明提交结果时 MUST 返回 commit-unknown 且不返回 raw credential，调用方只能用同一 ID 与语义重试解析。

#### Scenario: Issuance response 丢失后时钟推进
- **WHEN** 首次 Redis mutation 已提交但调用方只观察到 commit-unknown，并以相同 issuance ID 与稳定授权 binding 重试，而服务端时钟推进产生更晚 candidate expiry
- **THEN** issuer 从注入 key 重新推导并返回与首次完全相同的 opaque credential 和首次较短 expiry，不创建第二条资格也不延长有效期

#### Scenario: 相同 issuance ID 改变目标
- **WHEN** 调用方复用 issuance ID 但把 own-world 改为另一个 VisitSession、purpose、assignment、endpoint 或其他稳定授权事实
- **THEN** store 返回稳定 idempotency conflict，旧 credential record 不被覆盖

#### Scenario: 权威 deadline 已经收紧
- **WHEN** 相同 issuance ID 重试时 current session、membership 或 assignment lease 的权威 deadline 早于首次签发 expiry
- **THEN** issuer 返回 idempotency conflict 而不重放已经超出当前权威边界的资格

### Requirement: Admission verifier 必须原子消费并绑定受信连接事实
verifier MUST 在同一 Redis 原子操作中比较 credential、PlayerID、SessionID/epoch、role/purpose、target、TLS/TCP endpoint/channel 与业务 expiry，并写入首次 consume identity。首次匹配消费 MUST返回只读 binding；同一 consume identity/fingerprint的 response-loss重试 MAY返回同一 binding，任何不同 consume identity对已消费 credential MUST返回 replayed。credential missing、corrupt、unknown schema、过期、错误 endpoint/channel/purpose/epoch MUST fail closed。

#### Scenario: 正常消费与精确重试
- **WHEN** 未消费 credential 的全部静态 binding 匹配，随后相同 consume identity 在业务 expiry 前因响应丢失重试
- **THEN** 首次调用原子标记 consumed，两次都恢复同一只读 binding 且不会建立第二次消费

#### Scenario: 精确重试到达业务 expiry
- **WHEN** 首次消费已提交，但相同 consume identity 在 observed time 到达 credential 业务 expiry 后重试
- **THEN** verifier 返回 expired 且不产生 qualification；physical replay retention 不能延长业务资格

#### Scenario: Credential 被重放
- **WHEN** 已消费 credential 由不同 consume identity再次提交
- **THEN** verifier 返回 world admission replayed，不降级为bearer、ConnectionTicket、invite或AdmissionIntent授权

#### Scenario: 错误连接 binding
- **WHEN** credential 在旧 session epoch、错误 endpoint、非 TLS/TCP channel 或不同 purpose 上提交
- **THEN** verifier 在产生 qualification 前拒绝，且不泄漏哪一个私有 binding字段不同

### Requirement: Verifier 必须复核 current full assignment
静态 binding 原子消费后，verifier MUST通过 placement owner 的窄 reader读取 current assignment，要求 phase active、lease在observed time仍有效且完整 AssignmentStamp相等。assignment not-found、starting、expired或任一world/instance/node/generation/fence改变 MUST返回 stale；dependency error MUST保持dependency unavailable并允许同一 consume identity重试复核，不能猜测成功或恢复旧 assignment。

#### Scenario: Admission 签发后 assignment 被替换
- **WHEN** credential绑定的任一 AssignmentStamp字段不再等于 current active assignment
- **THEN** verifier烧毁旧credential并返回stale，不能只比较公开generation、instance或endpoint后继续

#### Scenario: 消费后 placement 暂时不可用
- **WHEN** Redis已提交首次消费但placement reader暂时无法证明current assignment
- **THEN** verifier返回dependency unavailable；相同consume identity可重放原binding并重新复核，其他identity仍被视为replay

### Requirement: Qualification 必须保持 role/purpose 与领域二次校验
成功 verify MUST返回不可由payload构造的只读 qualification。`OWN_WORLD` MUST只绑定Owner与自身PersonalWorld且不得携带VisitSessionID；`JOIN` MUST只用于Visitor reserved membership；`RECONNECT` MUST只用于同一Visitor reconnecting membership。VisitSession Join/Reconnect MUST分别要求匹配purpose的qualification，并重新比较AuthContext、VisitSessionID、SessionID/epoch、current full assignment、membership state与deadline；generic gameplay scope、invite或AdmissionIntent MUST不能替代qualification。

#### Scenario: JOIN 与 RECONNECT 互换
- **WHEN** 调用方把JOIN qualification交给VisitorReconnect，或把RECONNECT qualification交给首次Join
- **THEN** VisitSession application在mutation前拒绝，membership与revision不改变

#### Scenario: Credential 消费后 membership 已失效
- **WHEN** verifier成功但reserved/reconnecting membership已过期、缺失或lineage改变
- **THEN** VisitSession application拒绝mutation，qualification不能恢复或创建membership

### Requirement: Redis admission 状态必须版本化、有界且可失效
worldadmission owner MUST登记 issuance 与 credential 两类 Hash schema、字段字典、owner、TTL、大小、恢复、cleanup与失败策略。业务 expiry使用UTC微秒绝对时间且等于即失效；physical expiry只保留有界replay证据。Redis restart MAY读取自身仍保留的合法值，flush/key miss MUST使旧admission永久失效，不得从MySQL、日志或memory补回。Adapter MUST借用共享standalone client/keyspace，不拥有client lifecycle、timer/goroutine或memory fallback。

#### Scenario: Redis value 损坏或版本未知
- **WHEN** issuance/credential Hash缺字段、字段矛盾、超过编码预算、缺失TTL或schema version未知
- **THEN** adapter返回dependency defect并fail closed，不覆盖、删除或猜测该值

#### Scenario: Redis flush 后提交旧 credential
- **WHEN** admission keys被flush后客户端再次提交旧raw credential
- **THEN** verifier返回无效资格且不从其他数据源恢复consume tombstone或binding

### Requirement: Semantic fixture 必须由 production runtime 实际验收
共享 admission semantic corpus MUST继续只描述membership state、purpose、condition、outcome与stable error，不得冻结credential claims布局。runtime完成后fixture MUST显式标记已实现，并由测试覆盖matching JOIN/RECONNECT、purpose mismatch、expiry、credential replay、stale epoch/full assignment、wrong endpoint/channel与missing membership；matching与missing-membership场景 MUST真实调用VisitSession Join/VisitorReconnect二次校验。fixture生成、验证与磁盘baseline MUST保持确定性。

#### Scenario: Semantic corpus 与实现漂移
- **WHEN** fixture新增、删除或改变一个condition，但runtime/VisitSession组合测试没有对应决议
- **THEN**测试或统一contract verify失败，change不能归档

#### Scenario: Fixture 泄漏 credential 布局
- **WHEN** semantic fixture出现raw credential、consume identity、session epoch、AssignmentStamp、signature或claims字段
- **THEN**fixture validator拒绝该baseline，即使production runtime已经实现

### Requirement: 本 change 不得提前开放 transport 或 service graph
实现 MUST只提供纯Go application/security component、production Redis adapter与测试，不得注册HTTP route、WSS/TLS-TCP listener/dispatcher、正式Composition Root service graph、cleanup task或客户端runtime。后续公开adapter MUST消费本issuer/verifier并映射已冻结OpenAPI/Protobuf/errors，不能建立第二套credential、store或consume state。

#### Scenario: Production server 在本 change 后启动
- **WHEN** 当前production配置启动唯一server进程
- **THEN**公开world/visit业务入口仍未注册，admission package不会自行创建Redis client、goroutine或listener

#### Scenario: 后续 handler 自建 admission
- **WHEN** HTTP或TLS/TCP change尝试自行签名claims、保存credential消费状态或绕过本verifier
- **THEN**architecture review与structure tests拒绝该重复owner
