# Secure Battle Transport 规格

## Purpose

定义 BattleTicket、安全握手、加密 UDP/KCP、会话绑定、失效与 B0.5 实现资格行为。
## Requirements
### Requirement: BattleTicket 必须绑定 current target 且只能安装和消费一次

Go MUST 只为有效 HTTPS AuthContext、current PersonalWorld/VisitSession role、current active `SimulationTarget` 和未超过 8-actor battle capacity 的 actor 签发短期 BattleTicket。Ticket MUST 绑定 SessionID/epoch、PlayerID、role、world/visit、完整 AssignmentStamp fingerprint、SimulationNodeID、SimulationInstanceID、mapping generation、target revision、actor slot、wire/model/profile/config identity、受信 advertised UDP endpoint、issuance identity 和绝对 expiry；它 MUST NOT 与 WSS/TCP ConnectionTicket、WorldAdmission 或 GAMEPLAY scope 互换。Go MUST 在 Redis 保存 digest/handle-only 的幂等 issuance record，并在 HTTPS 成功前把派生 proof key 与完整 binding 幂等安装到 exact C++ child；任一侧部分失败 MUST 有界撤销或返回非成功。Proof key MUST 只由 raw ticket secret、raw ticket ID 和 versioned public derivation domain 计算，使独立客户端可以仅凭 HTTPS response 派生相同 proof；完整 binding fingerprint MUST NOT 成为客户端派生 proof 的前置输入或经 Go parent 注入客户端。

#### Scenario: Owner 取得 BattleTicket

- **WHEN** 有效 Owner session 请求自己的 current PersonalWorld battle target，target healthy/ready 且 actor capacity 可用
- **THEN** Go 从权威 owner 派生完整 binding，exact child 预留 actor slot并确认 install 后，HTTPS 返回一次性 ticket secret、ticket ID、advertised endpoint、wire suite 与 expiry，客户端可从这些公开 response 字段独立派生 exact proof

#### Scenario: 第九个 actor 请求 battle

- **WHEN** 合法 VisitSession 已有 8 个 installed 或 active battle actor，第 9 个 Visitor 请求 BattleTicket
- **THEN** battle admission 以稳定 capacity reason 拒绝，但 VisitSession membership、revision 和个人世界 v1 联机保持不变

#### Scenario: Ticket 安装后 HTTP response 丢失

- **WHEN** Redis issuance 与 child install 已成功但 HTTPS response 丢失，客户端以相同 actor、target 和 idempotency key 重试
- **THEN** issuer 重放相同 credential/binding/expiry 且 child 返回相同 actor slot，不创建第二个 ticket 或容量占用，重放 response 仍派生相同 proof

#### Scenario: Assignment 在消费前被替换

- **WHEN** 已安装 ticket 绑定的 assignment、target revision、node 或 instance 已被 successor 替换
- **THEN** ticket 立即不可消费，旧 proof key、endpoint 或曾经成功的 install 不能恢复 battle 资格

### Requirement: 握手必须先验证地址再分配安全会话

C++ listener MUST 使用 stateless cookie retry 和 PSK-authenticated ephemeral X25519 handshake。Ticket secret MUST 只经 HTTPS 返回且不得出现在 UDP；ClientHello MUST 只携带非秘密 ticket ID、client nonce、ephemeral public key 和有界 padding。合法 cookie 前，server MUST NOT 查询或消费 ticket、执行 X25519、创建 BattleSession/KCP/actor queue，且返回 bytes MUST 不超过对应 request bytes。ClientAuth MUST 绑定原 ClientHello、cookie、remote endpoint 和由 raw ticket secret、raw ticket ID、versioned domain 派生的完整 transcript proof；成功后 MUST 原子消费 installed ticket并以 AEAD保护ServerAccept。ServerAccept MUST 认证携带 authoritative binding fingerprint，客户端只有在成功解密后才能锁定该 binding；Go parent MUST NOT向客户端注入 proof key、binding fingerprint 或其他 private control material。Exact retry MAY重放同一accept，字段漂移或新transcript MUST拒绝。

#### Scenario: 未验证地址发送小包

- **WHEN** 未知 remote endpoint 发送缺少 padding 或 cookie 的 ClientHello
- **THEN** listener 至多返回不大于请求的 stateless Retry 或静默丢弃，不分配 session、KCP、actor queue 或执行 expensive key agreement

#### Scenario: 公开客户端派生 proof

- **WHEN** 独立客户端取得 canonical HTTPS ticket ID/secret 且没有 private binding 或 control access
- **THEN** 客户端按 versioned derivation 生成与 C++ installed ticket 相同的 proof key，并能完成 ClientAuth；改变 ticket ID、secret 或 domain 任一项均失败

#### Scenario: 伪造 ticket proof

- **WHEN** ClientAuth 携带合法 cookie 但 transcript HMAC 不是由 exact installed ticket proof key 产生
- **THEN** listener 拒绝并保持 ticket 未消费，响应遵守限流/抗放大且不泄漏 binding 是否存在

#### Scenario: ServerAccept 丢失

- **WHEN** ticket 已消费且 ServerAccept 丢失，客户端从同一 endpoint 重放完全相同的 ClientAuth
- **THEN** C++ 从有界 handshake replay state 返回同一 session generation 与 accept，不创建第二个 actor、再次转移 session seed 或重置 nonce/replay 状态

### Requirement: 每个 datagram 必须加密认证并执行 nonce 与 replay discipline

BattleSession MUST 使用 RFC 7748 X25519、RFC 5869 HKDF-SHA-256/HMAC-SHA-256 和 RFC 8439 ChaCha20-Poly1305 组合派生方向隔离的 traffic key、nonce prefix 和 rekey secret。握手后每个 datagram MUST 以 48-byte secure header 作为 AAD、使用 32-bit key epoch 与 64-bit 严格单调 packet sequence 组成唯一 96-bit nonce，并携带 16-byte tag。Receive MUST 在 application dispatch 前验证 epoch、AEAD 和固定 256-packet replay window；duplicate、too-old、future-jump、sequence wrap、wrong direction 或 nonce reuse MUST fail closed。Secret MUST 不得进入日志、metrics、fixture source、report、Redis 或 crash evidence。

#### Scenario: 重放已接受 packet

- **WHEN** 攻击者从同一或不同 endpoint 重放已认证并已提交 replay window 的 datagram
- **THEN** packet 在 route/simulation 前被拒绝，不再次推进 InputTick、KCP 或任何 gameplay 状态

#### Scenario: Ciphertext 或 AAD 被修改

- **WHEN** secure header、lane、session generation、epoch、sequence、payload 或 tag 任一 bit 被篡改
- **THEN** AEAD 验证失败且 packet 不改变 replay window、endpoint binding 或 application queue

#### Scenario: 发送 sequence 即将耗尽

- **WHEN** 当前 key epoch 无法在安全上限内完成 rollover 或 packet sequence 可能 wrap
- **THEN** session 以稳定 crypto reason 关闭并要求新 BattleTicket，不重置 sequence 继续使用旧 key

### Requirement: Battle wire 与 numeric route 必须唯一、闭合且符合 MTU

Battle wire MUST 提供 versioned binary envelope、`battle/v1` Protobuf payload 和唯一 numeric registry。`battle.input.bundle` 至 `battle.resync.response` 的 8 个 logical kind MUST 一一映射到 `3000-3007`、固定 direction 和唯一 raw 或 KCP lane；route MUST 登记 owner、QoS、max encoded/logical size、rate、精确 sender expiry、tick/sequence、idempotency、baseline/recovery 和 assignment/session binding。Numeric registry 的 expiry MUST 与 `battle-network-profile-v2` message inventory 逐项一致，其中 message 3006 与 3007 使用 2250 ms，其他 route 保持各自既有值。Datagram MUST 不超过 1200 bytes 并遵守 48-byte IP/UDP、48-byte secure header、16-byte AEAD tag、16-byte raw 或 24-byte KCP budget；IP fragmentation、通用 Any、未登记 compression、lane fallback 和 payload identity override MUST 禁止。

`BattleEntityState` 与 `BattleEntityDelta` 的transform MUST 显式提供position X/Y/Z、yaw和velocity X/Y/Z，yaw范围固定为`[-180000, 180000)`。`state_flags` registry MUST 保留bits 0–3作为phase token、登记bit 4为authority grounded、保留bit 31为dead，其余bit MUST 为零；delta `state_mask`与字段presence MUST 精确一致。Producer和全部consumer MUST 对缺失scalar、非法yaw、未知flag或mask/presence漂移fail closed，不得默认为零、掩码或重解释已有bit。以上字段仍使用现有field number、raw snapshot lane、payload ceiling和partition policy。

#### Scenario: Snapshot 通过 KCP 发送

- **WHEN** sender 或 registry 尝试把 full/delta snapshot 编码为 KCP、TLS/TCP 或 WSS route
- **THEN** contract/architecture gate 失败且运行时在 simulation dispatch 前拒绝错误 lane

#### Scenario: Payload 超过 route 或 MTU

- **WHEN** encoded message 超过其 registry max 或完整 datagram 超过 1200 bytes
- **THEN** sender 按登记 split policy 有界拆分或稳定拒绝，不依赖 IP fragmentation、不移除安全字段且不动态换 lane

#### Scenario: Go/C++/C# fixture parity

- **WHEN** 三种实现消费同一 header/protobuf/AAD/crypto/KCP canonical fixture
- **THEN** message ID、bytes、digest、decode result、route expiry、snapshot transform/state flags registry和negative disposition一致，unknown field/version或registry漂移使验证失败

#### Scenario: Resync route 仍使用旧 deadline

- **WHEN** numeric registry、wire fixture 或任一语言 projection 把 message 3006 或 3007 登记为 500 ms
- **THEN** profile parity 失败，不生成或启动不一致的 runtime route table

#### Scenario: Grounded bit 与 phase bit 冲突

- **WHEN** producer或consumer把bit 0重解释为grounded，或把bit 4解释为phase/dead以外状态
- **THEN** wire registry parity失败且snapshot不得发布，已有低四位phase语义保持不变

### Requirement: 单 UDP multiplexer 必须有界承载 raw 与 KCP

每个 SimulationNode MUST 只由 C++ 拥有一个 Asio UDP listener；raw、KCP 和 transport-control MUST 共享该 socket 和 authenticated BattleSession。Listener MUST 同时拥有 receive 与 serialized send lifecycle，但不得拥有 ticket、crypto、session、KCP 或 gameplay state；node-global authenticated runtime MUST 对该 listener 收到的 ClientHello、ClientAuth 与 secure datagram 执行完整分派，并把 Retry、ServerAccept、raw/KCP/control output 送回同一 socket。Bind 与 advertised endpoint MUST 分别由 Go 严格配置，production MUST 拒绝 port 0、隐式 Host 推导、地址冲突和自动换端口；本地推荐值为可覆盖的 `58445/udp`，loopback test MAY 使用 `127.0.0.1:0`。KCP adapter MUST 精确使用 profile 登记的 10 ms update、window 64、fast resend 2、RTO 30–200 ms、dead-link 10、1000-byte ceiling和 queue 64；sender application expiry MUST 从 immutable numeric route policy 解析，message 3004/3005 使用 500 ms，message 3006/3007 使用 2250 ms。Receiver reassembly MUST 由 KCP receive window、queue 与 session lifecycle 有界，完整重组后校验 exact route、sequence、Tick 与 generation，不得从首个 segment 推导 sender deadline。Windows listener MUST 在首次 receive 前禁用把无连接 UDP 的 ICMP Port Unreachable 映射成 socket 级 `WSAECONNRESET` 的行为；远端不可达只属于对应 datagram，MUST NOT 终止 node-global listener。该 socket policy 无法建立时启动 MUST fail closed。

#### Scenario: Production UDP bind 失败

- **WHEN** 配置的 battle UDP address 被占用、advertised endpoint 非法或 listener identity 与 ticket endpoint 不一致
- **THEN** simulation node 启动失败、process 保持 non-ready 并逆序回滚，不随机选择其他 port 或下发不可达 endpoint

#### Scenario: 真实 listener 完成安全会话

- **WHEN** 已安装 ticket 的独立客户端通过 advertised endpoint 向 production child 唯一 listener 发送 ClientHello、Retry 后的 ClientAuth 和 secure raw/KCP packet
- **THEN** 同一 listener 返回 Retry、ServerAccept 与 secure output，runtime 只创建一个绑定 exact seed/session/endpoint/actor/instance 的 active session，且 packet 经过既有 multiplexer 后才进入 simulation

#### Scenario: 旧 UDP 远端不可达

- **WHEN** listener 已向随后关闭的客户端端口排队 terminal response 冗余副本，并在 ICMP 返回后收到另一个合法客户端的 ClientHello
- **THEN** listener 继续运行、socket receive failure 不增加且后续 ClientHello 正常进入唯一 authenticated runtime；旧远端 ICMP 不得关闭 listener 或阻断新 session

#### Scenario: KCP sender message 已过期

- **WHEN** 可靠 message 在 sender queued/inflight 状态超过 registry application expiry
- **THEN** sender 以稳定 inflight expiry 终结，不继续发送、不进入 simulation且不回退 raw/TCP/WSS

#### Scenario: Receiver 首个 segment 先于完整重组到达

- **WHEN** receiver 收到后续 ordered message 的首个 segment，但完整 delivery 等待前序 segment
- **THEN** receiver 不启动 application reassembly timer，继续受 KCP window/queue 与 session lifecycle 硬上限约束

#### Scenario: 一个 instance 尝试创建第二 listener

- **WHEN** runtime、配置或 test 为 raw、KCP 或某个 SimulationInstance 单独 bind socket
- **THEN** architecture gate 失败；所有 lane 必须经 node-global authenticated multiplexer

#### Scenario: KCP caller 尝试覆盖 deadline

- **WHEN** application caller 提供与 numeric route 不一致的 expiry 或绝对 deadline
- **THEN** adapter fail closed 并记录低敏 route policy reason，不发送、截断或延长该消息

#### Scenario: Listener shutdown 与 send completion 并发

- **WHEN** node drain/stop 或 listener failure 发生时仍有排队 send 与 active session
- **THEN** runtime 先停止新 admission/receive dispatch，按 deadline 终结 pending output，清零全部 session secret并等待同一 socket worker 退出，不在 cleanup 后执行迟到 callback

### Requirement: Snapshot 必须显式确认当前 actor 的连续输入前沿

`BattleFullSnapshot` 与 `BattleDeltaSnapshot` MUST 携带显式 presence 的 `last_processed_input_tick`，表示接收该 snapshot 的已认证 BattleSession actor 在当前 mapping generation 内已经应用或以稳定结果终结的最大连续 InputTick。值 `0` MUST 只表示该 generation 尚未终结任何从 1 开始的输入；字段缺失 MUST 被当前 wire identity 的 decoder 视为不兼容，而不是解释为 `0`。该字段 MUST 来自 simulation replication 冻结点，不得由 ServerTick、到达顺序、payload identity 或客户端声明推导。

#### Scenario: snapshot 确认 gap 之前的输入

- **WHEN** 当前 actor 的 InputTick 100 已终结、101 仍未决且 102 已终结，replication 生成 full 或 delta snapshot
- **THEN** `last_processed_input_tick` 必须为 100；只有 101 被应用或按稳定 policy 终结后，后续 snapshot 才能越过该 gap

#### Scenario: mapping generation 尚无输入

- **WHEN** 新 BattleSession mapping generation 已建立但尚未终结 InputTick 1
- **THEN** snapshot 必须显式编码 `last_processed_input_tick = 0`，receiver 能区分该值与字段缺失的旧 producer

#### Scenario: reconnect 后旧确认到达

- **WHEN** 当前 session 已进入更高 mapping generation，而旧 generation 的 snapshot 或输入随后到达
- **THEN** receiver 拒绝旧 generation 数据，新 generation 的确认游标从 0 独立推进且不得继承旧值

### Requirement: Snapshot partition 必须冻结一致的输入确认

同一逻辑 snapshot 的所有 partition MUST 冻结相同的 snapshot sequence、baseline identity、mapping generation 和 `last_processed_input_tick`。Receiver MUST 在完整 partition set 通过数量、索引、身份、大小与确认游标一致性校验后才发布确认；缺片、重复冲突或游标漂移 MUST fail closed。新增字段 MUST 保持现有 1200-byte datagram、route max encoded/logical size、raw lane 和安全封装预算；sender MUST 通过登记 partition policy 拆分或稳定拒绝，不得扩大 MTU 或切换 lane。

#### Scenario: partition 游标发生漂移

- **WHEN** 同一 snapshot sequence 的两个 partition 携带不同 `last_processed_input_tick`
- **THEN** receiver 拒绝整个 partition set，不选择任一片的值、不发布 observer state 且不淘汰客户端输入历史

#### Scenario: 新字段触发额外 partition

- **WHEN** 增加确认字段使 snapshot 无法在原 partition 数量内满足 route payload ceiling
- **THEN** sender 按冻结 split policy 增加 partition 或稳定拒绝，完整 datagram 仍不超过 1200 bytes 且继续使用登记 raw lane

### Requirement: 输入确认必须通过跨语言协议资格

Go、C++ 与 C# MUST 对 full/delta snapshot 的 `last_processed_input_tick` 字段编号、presence、零值、大 varint、partition identity、bytes、digest、decode result 和 negative disposition 保持一致。独立 C++ 协议客户端 MUST 只在完整且已认证的 snapshot 通过 generation/baseline/partition 校验后输出该游标；Go qualification correlation MUST 以 actor、mapping generation 与 InputTick 关联 input 和确认，不得从 ServerTick 或日志文本推测。B0.6 的 own/visit、loss/reorder/duplicate、reconnect 与 backpressure workload MUST 证明游标不越过未决 gap、不跨 generation 串联且能回收已终结输入。

#### Scenario: 跨语言显式零值 fixture

- **WHEN** Go、C++ 与 C# 消费显式编码 `last_processed_input_tick = 0` 的相同 canonical full/delta fixture
- **THEN** 三种实现报告相同 presence、值、canonical bytes 与 digest，删除该字段的 negative fixture 一致失败

#### Scenario: qualification 关联一批输入

- **WHEN** 一个已认证 snapshot 在当前 actor/mapping generation 内把确认游标从 40 推进到 44
- **THEN** correlation 可将 41 至 44 的已终结输入归入该确认窗口，但不得确认 44 之后或其他 actor/generation 的输入

### Requirement: BattleSessionContext 必须唯一绑定 actor 并保护 SimulationInstance

成功握手 MUST 产生不可变 BattleSessionContext，绑定 account session lineage、battle session/generation、endpoint generation、current AssignmentStamp/SimulationInstance、actor slot 和 route policy。UDP payload MUST 不能选择或覆盖 PlayerID、role、actor 或 assignment。C2S input MUST 依次通过 AEAD/replay、route/direction/size/rate、target/endpoint、InputTick/sequence/expiry 和 actor command allowlist，再进入 SimulationInstance 有界 inbox；最终 transform、hit、damage、effect、death、reward 或任意 history state 声明 MUST 拒绝。Simulation worker MUST 保持 ECS/physics/navigation/history 唯一 writer。

#### Scenario: Input 携带另一个 actor identity

- **WHEN** 已认证 Visitor 在 input payload 中提交 PlayerID、actor ID 或 role 试图控制 Owner 或其他实体
- **THEN** payload 在 simulation 前以 identity/unsafe-payload reason 拒绝，BattleSessionContext 绑定的 actor 和 world 状态不变

#### Scenario: 合法 input bundle

- **WHEN** raw input bundle 的 session、endpoint、route、tick window、sequence、expiry 和 actor command 均有效
- **THEN** transport 只把规范化 command 交给绑定 SimulationInstance inbox，由固定 simulation pipeline 裁决结果

#### Scenario: Queue 达到 hard budget

- **WHEN** session/node ingress、egress、simulation inbox 或 KCP queue 达到登记上限
- **THEN** runtime 按 route policy 丢弃可替换 snapshot、终结过期数据或关闭 hard-backpressure session，不进行无界扩容

### Requirement: Rebind 与 key rollover 必须保持 generation 和 replay 连续

Endpoint rebind MUST 由现有 traffic key 认证并对新 remote IP/port 执行 stateless cookie challenge；成功 confirm MUST 递增 endpoint generation，且不得重置 packet sequence、replay window、KCP conversation、actor binding 或 key epoch。Key rollover MUST 在 10 分钟或 `2^20` datagrams 先到时通过 authenticated transport-control 协商 next epoch；send 只使用 current epoch，receive 最多在 3 秒有界 overlap 内接受 previous epoch 且分别执行 replay window。并发/旧 rebind、cookie replay、rollover deadline 和 epoch wrap MUST fail closed。

#### Scenario: NAT endpoint 合法变化

- **WHEN** 持有 current BattleSession key 的客户端从新 endpoint 完成 rebind cookie 与 authenticated confirm
- **THEN** server 原子递增 endpoint generation 并切换唯一 remote endpoint，后续旧 endpoint packet 被拒绝且 session sequence/KCP 状态连续

#### Scenario: 攻击者只重放旧 rebind confirm

- **WHEN** 不同 remote endpoint 重放旧 cookie、旧 endpoint generation 或已接受 confirm
- **THEN** rebind 拒绝且 current endpoint、key、replay 和 actor binding 均不改变

#### Scenario: Previous key overlap 到期

- **WHEN** previous epoch packet 在 3 秒 overlap 后到达或已在该 epoch 被接受过
- **THEN** packet 以 old-epoch/replay reason 拒绝，不延长 overlap 或回退 current replay window

### Requirement: Session、target 与 process 失效必须撤销 battle transport

Session epoch 递增、logout/forced logout/ban、assignment replacement/lease expiry、target revision 变化、membership/role 失效、Owner grace 终结、instance drain/stop、child/node unhealthy、control EOF、listener failure 或 hard protocol/crypto violation MUST 立即停止该 binding 的新 gameplay dispatch。Go 与 C++ MUST 各自 fail closed 并通过高优先级 revoke/closed control 收敛；任一侧先观察到失效时不得等待另一侧恢复旧 ticket/session。Shutdown MUST 先停止 HTTP ticket issuance 和新公开输入，再 revoke session、bounded drain egress/KCP，随后执行 simulation result/fence/instance/node/storage 逆序关闭。

#### Scenario: Session epoch 失效

- **WHEN** 账号 session epoch 在 BattleSession active 期间递增
- **THEN** Go 提交权威 epoch 后发送 revoke，C++ 立即停止该 lineage 所有 battle packet 并关闭 session；旧 ticket/key/endpoint 均不能重连

#### Scenario: C++ child crash

- **WHEN** 承载 active battle session 的 child 异常退出或 control EOF
- **THEN** Go 撤销 node readiness 与全部 SimulationTarget，已安装 ticket 和 active session 随不可复活 node identity 终结，不由新 child 恢复

#### Scenario: Shutdown deadline 到期

- **WHEN** KCP/egress 无法在有界 deadline 内 drain
- **THEN** runtime 记录低敏 incomplete outcome 并继续撤销 ticket/session、assignment 和 node；未发送 transport 数据不被解释为持久提交

### Requirement: B0.5 必须通过跨语言安全 transport 实现资格门

本 capability MUST 提供 versioned schema/manifest、Go/C++/C# fixtures、threat model、dependency lock、真实 child/loopback UDP harness 和唯一 qualification report。Mandatory gates MUST 覆盖客户端仅从 HTTPS ticket ID/secret 派生 proof、production child 唯一 listener 真实完成 handshake 和 secure raw/KCP/control、`battle-network-profile-v2` binding、输入确认与 wire/crypto/KCP route-expiry parity、ticket response-loss/consume、forged/replay/amplification/reorder/duplicate/expiry/MTU/backpressure/rebind/rollover/shutdown、own/visit、8/9 actor、assignment/session 失效、child crash/Go restart，以及 B0.3/B0.4、server v1、client v1 和 OpenSpec strict regression。报告 MUST 绑定 source/binary/toolchain/dependency/model/profile/control/wire/registry/config/fixture digest 且保持低敏；只可声明 `secure-transport-qualified-windows-x64`，不得替代 B0.6 网络发布资格。任何只调用内部 handshake/transport 对象而未经过 production composition root 和 listener 的 loopback harness MUST NOT 满足真实 socket gate。

#### Scenario: 完整 B0.5 verify 通过

- **WHEN** current source 的全部 mandatory gate 连续运行通过，真实 child/socket evidence 证明公开 credential 路径、唯一 listener、active session composition、profile v2 route expiry 与输入确认 parity，且报告 digest 一致
- **THEN** B0.5 可声明 secure transport implementation-qualified 并仅解锁 `qualify-battle-network`

#### Scenario: 500 ms 与 2250 ms route parity

- **WHEN** adapter parity corpus 分别发送一般可靠事件和 resync request/response
- **THEN** 前者在 500 ms 后稳定 sender expired，后者只在 2250 ms 后 sender expired，且两类消息共享同一有界 KCP conversation

#### Scenario: 只有内部 loopback happy path 通过

- **WHEN** 内部对象 harness 能完成 handshake/input，但 production child listener 没有回包、没有 active session，或 profile/输入确认 parity、安全 negative、KCP parity、8/9 actor、失效、既有 v1 regression 任一未通过
- **THEN** qualification 保持未完成，UDP listener 不得作为 production-ready 或 Unity runtime 进入证据

### Requirement: 同 actor 的认证 successor 必须有界接管 predecessor

当新BattleSession已通过一次性ticket认证且绑定到与既有active session相同的SimulationInstance、mapping generation和ActorID时，runtime MUST 在发送新session首个full baseline前终结并移除predecessor。Predecessor MUST 以低敏lifecycle原因记账，旧secure route MUST 立即不可路由；其他actor、instance或mapping的session MUST NOT 被替换。Successor的首个公开projection MUST 只包含唯一actor identity，不得因重复actor永久等待baseline。

客户端从`ServerAccept`进入`AwaitingBaseline`后 MUST 使用有界deadline等待首个完整full snapshot；deadline到期 MUST 发布稳定可恢复失败并关闭current generation，MUST NOT 无限保持loading状态。

BattleTicket绝对expiry MUST 只终结尚未消费的`Installed`凭据并阻止其建立新BattleSession。一旦ticket已由成功握手原子转换为`Consumed`，该credential deadline MUST NOT 把active actor改为`Expired`、释放其actor slot或使其session authority失效；active actor只可由显式revoke、同actor successor或session、assignment、target、instance、node lifecycle终结。

#### Scenario: Client进程消失后同角色重新进入

- **WHEN** predecessor未发送close但同一角色持新ticket完成认证
- **THEN** runtime原子退休predecessor，active session数量不增加，successor收到完整baseline且旧route的数据包被拒绝

#### Scenario: 首个baseline未到达

- **WHEN** client已认证但在登记deadline内没有提交首个完整full snapshot
- **THEN** current generation以可恢复timeout终结并进入既有single-flight recovery，UI不得永久显示同步中

#### Scenario: 快速target替换收到旧handshake响应

- **WHEN** connected UDP endpoint复用使结构匹配的旧`Retry`或`ServerAccept`先于current handshake响应到达
- **THEN** client只可接受由current transcript认证的响应；旧候选必须原位清零并在原绝对deadline内继续等待，不能建立session、延长deadline或立即升级为终态Security

#### Scenario: 另一个actor在ticket expiry后仍保持在线

- **WHEN** Visitor已用短期ticket完成握手并保持active，原ticket deadline过去后Owner安装并认证同actor successor
- **THEN** Visitor仍为`Consumed`并继续占用原slot，Visitor secure route保持可用，Owner successor的首个full baseline仍包含Owner与Visitor且不得只剩当前本地actor

### Requirement: Battle wire 必须兼容表达武器切换与完整 content projection

`battle-wire-v1` MUST 在现有message `3000-3007`与唯一lane内兼容增加无target payload的`SWITCH_WEAPON` input kind，并在full entity state显式携带nonzero `archetype_id`、受类型约束的`equipped_weapon_id`与positive `max_health_milli`。`health_milli` MUST 不大于max health；archetype/max health在entity generation内不可变，equipped weapon MAY 通过登记delta field与唯一state-mask bit完整替换。Lifecycle spawn外层archetype MUST 与initial state一致；unknown enum、unknown state-mask/flag、zero/unmapped ID、presence/range或cross-field mismatch MUST 在application前fail closed。

#### Scenario: Full baseline 包含 current player 与 Boss
- **WHEN** server编码current generation的player、ordinary monster与Boss full state
- **THEN** 每个entity携带可映射archetype和合法health/max-health，只有player携带登记equipped weapon，所有partition共享相同Tick/baseline/ack并在1200-byte预算内

#### Scenario: Weapon delta 缺少 state mask
- **WHEN**delta携带equipped weapon但未设置对应mask，设置mask却缺字段，或引用unmapped weapon ID
- **THEN**C++/Go fixture/C# closed decoder以同一稳定wire reason拒绝，replica与HUD不部分更新

#### Scenario: Switch input 夹带 target
- **WHEN**`SWITCH_WEAPON` command的move、aim、interaction slot或其他不允许字段非零
- **THEN**normalization在进入simulation inbox前拒绝该command，不能让payload选择weapon、target或actor identity

### Requirement: Content wire mapping 与 profile budget 必须跨三端冻结

Battle wire source MUST 登记production actor/weapon/ability/projectile numeric ID的kind、semantic identity、范围、retired policy和mapping digest，并把新增字段的presence、state mask、canonical bytes、malformed corpus、encoded maximum与partition projection纳入exact wire/profile binding。Go、C++与C# generated descriptor/codec/fixture MUST 对同一source保持parity；旧wire identity、旧max-size projection或消费端硬编码 MUST 在ticket/connection前fail closed。Compatible字段扩展 MUST NOT 改变message ID、direction、raw/KCP lane、AEAD/replay、rate、expiry或resync语义。

#### Scenario: 三端 canonical content packet
- **WHEN**canonical fixture编码包含Boss archetype、fan weapon、ability event与max health的full/lifecycle/event集合
- **THEN**Go、C++和C#产生相同bytes/descriptor interpretation并解析到相同semantic mapping，且size/profile validator接受其budget

#### Scenario: 旧客户端缺少新增字段 binding
- **WHEN**Unity build仍使用扩展前wire digest或decoder，不理解content字段/state mask
- **THEN**startup compatibility在请求BattleTicket或创建UDP socket前失败，不允许忽略unknown field后加入current combat instance
