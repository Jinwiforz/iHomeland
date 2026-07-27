## MODIFIED Requirements

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

### Requirement: 单 UDP multiplexer 必须有界承载 raw 与 KCP

每个 SimulationNode MUST 只由 C++ 拥有一个 Asio UDP listener；raw、KCP 和 transport-control MUST 共享该 socket 和 authenticated BattleSession。Listener MUST 同时拥有 receive 与 serialized send lifecycle，但不得拥有 ticket、crypto、session、KCP 或 gameplay state；node-global authenticated runtime MUST 对该 listener 收到的 ClientHello、ClientAuth 与 secure datagram 执行完整分派，并把 Retry、ServerAccept、raw/KCP/control output 送回同一 socket。Bind 与 advertised endpoint MUST分别由Go严格配置，production MUST拒绝port 0、隐式Host推导、地址冲突和自动换端口；本地推荐值为可覆盖的`58445/udp`，loopback test MAY使用`127.0.0.1:0`。KCP adapter MUST精确使用profile登记的10 ms update、window 64、fast resend 2、RTO 30–200 ms、dead-link 10、1000-byte ceiling、queue 64与500 ms expiry，并在reassembly后再次验证application route和deadline。Windows listener MUST 在首次 receive 前禁用把无连接 UDP 的 ICMP Port Unreachable 映射成 socket 级 `WSAECONNRESET` 的行为；远端不可达只属于对应 datagram，MUST NOT 终止 node-global listener。该 socket policy 无法建立时启动 MUST fail closed。

#### Scenario: Production UDP bind 失败

- **WHEN** 配置的 battle UDP address 被占用、advertised endpoint 非法或 listener identity 与 ticket endpoint 不一致
- **THEN** simulation node 启动失败、process 保持 non-ready 并逆序回滚，不随机选择其他 port 或下发不可达 endpoint

#### Scenario: 真实 listener 完成安全会话

- **WHEN** 已安装 ticket 的独立客户端通过 advertised endpoint 向 production child 唯一 listener 发送 ClientHello、Retry 后的 ClientAuth 和 secure raw/KCP packet
- **THEN** 同一 listener 返回 Retry、ServerAccept 与 secure output，runtime 只创建一个绑定 exact seed/session/endpoint/actor/instance 的 active session，且 packet 经过既有 multiplexer 后才进入 simulation

#### Scenario: 旧 UDP 远端不可达

- **WHEN** listener 已向随后关闭的客户端端口排队 terminal response 冗余副本，并在 ICMP 返回后收到另一个合法客户端的 ClientHello
- **THEN** listener 继续运行、socket receive failure 不增加且后续 ClientHello 正常进入唯一 authenticated runtime；旧远端 ICMP 不得关闭 listener 或阻断新 session

#### Scenario: KCP message 到达时已过期

- **WHEN** 可靠 message 经重传完成 reassembly 但已超过 registry application expiry
- **THEN** message 以稳定 expired 终结，不进入 simulation、不继续阻塞 queue 且不回退 raw/TCP/WSS

#### Scenario: 一个 instance 尝试创建第二 listener

- **WHEN** runtime、配置或 test 为 raw、KCP 或某个 SimulationInstance 单独 bind socket
- **THEN** architecture gate 失败；所有 lane 必须经 node-global authenticated multiplexer

#### Scenario: Listener shutdown 与 send completion 并发

- **WHEN** node drain/stop 或 listener failure 发生时仍有排队 send 与 active session
- **THEN** runtime 先停止新 admission/receive dispatch，按 deadline 终结 pending output，清零全部 session secret并等待同一 socket worker 退出，不在 cleanup 后执行迟到 callback

### Requirement: B0.5 必须通过跨语言安全 transport 实现资格门

本 capability MUST 提供 versioned schema/manifest、Go/C++/C# fixtures、threat model、dependency lock、真实 child/loopback UDP harness 和唯一 qualification report。Mandatory gates MUST覆盖客户端仅从HTTPS ticket ID/secret派生proof、production child唯一listener真实完成handshake和secure raw/KCP/control、wire/crypto/KCP parity、ticket response-loss/consume、forged/replay/amplification/reorder/duplicate/expiry/MTU/backpressure/rebind/rollover/shutdown、own/visit、8/9 actor、assignment/session失效、child crash/Go restart，以及B0.3/B0.4、server v1、client v1和OpenSpec strict regression。报告 MUST绑定source/binary/toolchain/dependency/model/profile/control/wire/registry/config/fixture digest且保持低敏；只可声明`secure-transport-qualified-windows-x64`，不得替代B0.6网络发布资格。任何只调用内部 handshake/transport 对象而未经过 production composition root 和 listener 的 loopback harness MUST NOT满足真实 socket gate。

#### Scenario: 完整 B0.5 verify 通过

- **WHEN** current source 的全部 mandatory gate 连续运行通过，真实 child/socket evidence 证明公开 credential 路径、唯一 listener 与 active session composition，且报告 digest 一致
- **THEN** B0.5 可声明 secure transport implementation-qualified 并仅解锁 `qualify-battle-network`

#### Scenario: 只有内部 loopback happy path 通过

- **WHEN** 内部对象 harness 能完成 handshake/input，但 production child listener 没有回包、没有 active session，或安全 negative、KCP parity、8/9 actor、失效、既有 v1 regression 任一未通过
- **THEN** qualification 保持未完成，UDP listener 不得作为 production-ready 或 Unity runtime 进入证据
