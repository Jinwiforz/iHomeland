# Secure Battle Transport 规格

## Purpose

定义 BattleTicket、安全握手、加密 UDP/KCP、会话绑定、失效与 B0.5 实现资格行为。

## Requirements

### Requirement: BattleTicket 必须绑定 current target 且只能安装和消费一次

Go MUST 只为有效 HTTPS AuthContext、current PersonalWorld/VisitSession role、current active `SimulationTarget` 和未超过 8-actor battle capacity 的 actor 签发短期 BattleTicket。Ticket MUST 绑定 SessionID/epoch、PlayerID、role、world/visit、完整 AssignmentStamp fingerprint、SimulationNodeID、SimulationInstanceID、mapping generation、target revision、actor slot、wire/model/profile/config identity、受信 advertised UDP endpoint、issuance identity 和绝对 expiry；它 MUST NOT 与 WSS/TCP ConnectionTicket、WorldAdmission 或 GAMEPLAY scope 互换。Go MUST 在 Redis 保存 digest/handle-only 的幂等 issuance record，并在 HTTPS 成功前把派生 proof key 与完整 binding 幂等安装到 exact C++ child；任一侧部分失败 MUST 有界撤销或返回非成功。

#### Scenario: Owner 取得 BattleTicket

- **WHEN** 有效 Owner session 请求自己的 current PersonalWorld battle target，target healthy/ready 且 actor capacity 可用
- **THEN** Go 从权威 owner 派生完整 binding，exact child 预留 actor slot 并确认 install 后，HTTPS 返回一次性 ticket secret、ticket ID、advertised endpoint、wire suite 与 expiry

#### Scenario: 第九个 actor 请求 battle

- **WHEN** 合法 VisitSession 已有 8 个 installed 或 active battle actor，第 9 个 Visitor 请求 BattleTicket
- **THEN** battle admission 以稳定 capacity reason 拒绝，但 VisitSession membership、revision 和个人世界 v1 联机保持不变

#### Scenario: Ticket 安装后 HTTP response 丢失

- **WHEN** Redis issuance 与 child install 已成功但 HTTPS response 丢失，客户端以相同 actor、target 和 idempotency key 重试
- **THEN** issuer 重放相同 credential/binding/expiry 且 child 返回相同 actor slot，不创建第二个 ticket 或容量占用

#### Scenario: Assignment 在消费前被替换

- **WHEN** 已安装 ticket 绑定的 assignment、target revision、node 或 instance 已被 successor 替换
- **THEN** ticket 立即不可消费，旧 proof key、endpoint 或曾经成功的 install 不能恢复 battle 资格

### Requirement: 握手必须先验证地址再分配安全会话

C++ listener MUST 使用 stateless cookie retry 和 PSK-authenticated ephemeral X25519 handshake。Ticket secret MUST 只经 HTTPS 返回且不得出现在 UDP；ClientHello MUST 只携带非秘密 ticket ID、client nonce、ephemeral public key 和有界 padding。合法 cookie 前，server MUST NOT 查询或消费 ticket、执行 X25519、创建 BattleSession/KCP/actor queue，且返回 bytes MUST 不超过对应 request bytes。ClientAuth MUST 绑定原 ClientHello、cookie、remote endpoint 和完整 transcript proof；成功后 MUST 原子消费 installed ticket 并以 AEAD 保护 ServerAccept。Exact retry MAY 重放同一 accept，字段漂移或新 transcript MUST 拒绝。

#### Scenario: 未验证地址发送小包

- **WHEN** 未知 remote endpoint 发送缺少 padding 或 cookie 的 ClientHello
- **THEN** listener 至多返回不大于请求的 stateless Retry 或静默丢弃，不分配 session、KCP、actor queue 或执行 expensive key agreement

#### Scenario: 伪造 ticket proof

- **WHEN** ClientAuth 携带合法 cookie 但 transcript HMAC 不是由 exact installed ticket proof key 产生
- **THEN** listener 拒绝并保持 ticket 未消费，响应遵守限流/抗放大且不泄漏 binding 是否存在

#### Scenario: ServerAccept 丢失

- **WHEN** ticket 已消费且 ServerAccept 丢失，客户端从同一 endpoint 重放完全相同的 ClientAuth
- **THEN** C++ 从有界 handshake replay state 返回同一 session generation 与 accept，不创建第二个 actor 或重置 nonce/replay 状态

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

Battle wire MUST 提供 versioned binary envelope、`battle/v1` Protobuf payload 和唯一 numeric registry。`battle.input.bundle` 至 `battle.resync.response` 的 8 个 logical kind MUST 一一映射到 `3000-3007`、固定 direction 和唯一 raw 或 KCP lane；route MUST 登记 owner、QoS、max encoded/logical size、rate、expiry、tick/sequence、idempotency、baseline/recovery 和 assignment/session binding。Datagram MUST 不超过 1200 bytes 并遵守 48-byte IP/UDP、48-byte secure header、16-byte AEAD tag、16-byte raw 或 24-byte KCP budget；IP fragmentation、通用 Any、未登记 compression、lane fallback 和 payload identity override MUST 禁止。

#### Scenario: Snapshot 通过 KCP 发送

- **WHEN** sender 或 registry 尝试把 full/delta snapshot 编码为 KCP、TLS/TCP 或 WSS route
- **THEN** contract/architecture gate 失败且运行时在 simulation dispatch 前拒绝错误 lane

#### Scenario: Payload 超过 route 或 MTU

- **WHEN** encoded message 超过其 registry max 或完整 datagram 超过 1200 bytes
- **THEN** sender 按登记 split policy 有界拆分或稳定拒绝，不依赖 IP fragmentation、不移除安全字段且不动态换 lane

#### Scenario: Go/C++/C# fixture parity

- **WHEN** 三种实现消费同一 header/protobuf/AAD/crypto/KCP canonical fixture
- **THEN** message ID、bytes、digest、decode result 和 negative disposition 一致，unknown field/version 或 registry 漂移使验证失败

### Requirement: 单 UDP multiplexer 必须有界承载 raw 与 KCP

每个 SimulationNode MUST 只由 C++ 拥有一个 Asio UDP listener；raw、KCP 和 transport-control MUST 共享该 socket 和 authenticated BattleSession。Bind 与 advertised endpoint MUST 分别由 Go 严格配置，production MUST 拒绝 port 0、隐式 Host 推导、地址冲突和自动换端口；本地推荐值为可覆盖的 `58445/udp`，loopback test MAY 使用 `127.0.0.1:0`。KCP adapter MUST 精确使用 profile 登记的 10 ms update、window 64、fast resend 2、RTO 30–200 ms、dead-link 10、1000-byte ceiling、queue 64 与 500 ms expiry，并在 reassembly 后再次验证 application route 和 deadline。

#### Scenario: Production UDP bind 失败

- **WHEN** 配置的 battle UDP address 被占用、advertised endpoint 非法或 listener identity 与 ticket endpoint 不一致
- **THEN** simulation node 启动失败、process 保持 non-ready 并逆序回滚，不随机选择其他 port 或下发不可达 endpoint

#### Scenario: KCP message 到达时已过期

- **WHEN** 可靠 message 经重传完成 reassembly 但已超过 registry application expiry
- **THEN** message 以稳定 expired 终结，不进入 simulation、不继续阻塞 queue 且不回退 raw/TCP/WSS

#### Scenario: 一个 instance 尝试创建第二 listener

- **WHEN** runtime、配置或 test 为 raw、KCP 或某个 SimulationInstance 单独 bind socket
- **THEN** architecture gate 失败；所有 lane 必须经 node-global authenticated multiplexer

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

本 capability MUST 提供 versioned schema/manifest、Go/C++/C# fixtures、threat model、dependency lock、真实 child/loopback UDP harness 和唯一 qualification report。Mandatory gates MUST 覆盖 wire/crypto/KCP parity、ticket response-loss/consume、forged/replay/amplification/reorder/duplicate/expiry/MTU/backpressure/rebind/rollover/shutdown、own/visit、8/9 actor、assignment/session 失效、child crash/Go restart，以及 B0.3/B0.4、server v1、client v1 和 OpenSpec strict regression。报告 MUST 绑定 source/binary/toolchain/dependency/model/profile/control/wire/registry/config/fixture digest 且保持低敏；只可声明 `secure-transport-qualified-windows-x64`，不得替代 B0.6 网络发布资格。

#### Scenario: 完整 B0.5 verify 通过

- **WHEN** 当前源码的全部 mandatory gate 连续运行通过且报告 digest 一致
- **THEN** B0.5 可声明 secure transport implementation-qualified 并仅解锁 `qualify-battle-network`

#### Scenario: 只有 loopback happy path 通过

- **WHEN** 真实 socket 能完成 handshake 和 input，但安全 negative、KCP parity、8/9 actor、失效或既有 v1 regression 任一未通过
- **THEN** qualification 保持未完成，UDP listener 不得作为 production-ready 或 Unity runtime 进入证据
