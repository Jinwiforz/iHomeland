## ADDED Requirements

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
- **THEN** issuer 重放相同 credential/binding/expiry且child返回相同 actor slot，不创建第二个 ticket或容量占用

#### Scenario: Assignment 在消费前被替换

- **WHEN** 已安装 ticket 绑定的 assignment、target revision、node或instance已被 successor替换
- **THEN** ticket立即不可消费，旧proof key、endpoint或曾经成功的install不能恢复battle资格

### Requirement: 握手必须先验证地址再分配安全会话

C++ listener MUST 使用 stateless cookie retry 和 PSK-authenticated ephemeral X25519 handshake。Ticket secret MUST 只经 HTTPS 返回且不得出现在 UDP；ClientHello MUST 只携带非秘密 ticket ID、client nonce、ephemeral public key和有界padding。合法cookie前，server MUST NOT查询或消费ticket、执行X25519、创建BattleSession/KCP/actor queue，且返回bytes MUST不超过对应request bytes。ClientAuth MUST绑定原ClientHello、cookie、remote endpoint和完整transcript proof；成功后 MUST 原子消费installed ticket并以AEAD保护ServerAccept。Exact retry MAY重放同一accept，字段漂移或新transcript MUST拒绝。

#### Scenario: 未验证地址发送小包

- **WHEN** 未知remote endpoint发送缺少padding或cookie的ClientHello
- **THEN** listener至多返回不大于请求的stateless Retry或静默丢弃，不分配session、KCP、actor queue或执行expensive key agreement

#### Scenario: 伪造 ticket proof

- **WHEN** ClientAuth携带合法cookie但transcript HMAC不是由exact installed ticket proof key产生
- **THEN** listener拒绝并保持ticket未消费，响应遵守限流/抗放大且不泄漏binding是否存在

#### Scenario: ServerAccept 丢失

- **WHEN** ticket已消费且ServerAccept丢失，客户端从同一endpoint重放完全相同的ClientAuth
- **THEN** C++从有界handshake replay state返回同一session generation与accept，不创建第二个actor或重置nonce/replay状态

### Requirement: 每个 datagram 必须加密认证并执行 nonce 与 replay discipline

BattleSession MUST 使用RFC 7748 X25519、RFC 5869 HKDF-SHA-256/HMAC-SHA-256和RFC 8439 ChaCha20-Poly1305组合派生方向隔离的traffic key、nonce prefix和rekey secret。握手后每个datagram MUST以48-byte secure header作为AAD、使用32-bit key epoch与64-bit严格单调packet sequence组成唯一96-bit nonce，并携带16-byte tag。Receive MUST在application dispatch前验证epoch、AEAD和固定256-packet replay window；duplicate、too-old、future-jump、sequence wrap、wrong direction或nonce reuse MUST fail closed。Secret MUST不得进入日志、metrics、fixture source、report、Redis或crash evidence。

#### Scenario: 重放已接受 packet

- **WHEN** 攻击者从同一或不同endpoint重放已认证并已提交replay window的datagram
- **THEN** packet在route/simulation前被拒绝，不再次推进InputTick、KCP或任何gameplay状态

#### Scenario: Ciphertext 或 AAD 被修改

- **WHEN** secure header、lane、session generation、epoch、sequence、payload或tag任一bit被篡改
- **THEN** AEAD验证失败且packet不改变replay window、endpoint binding或application queue

#### Scenario: 发送 sequence 即将耗尽

- **WHEN** 当前key epoch无法在安全上限内完成rollover或packet sequence可能wrap
- **THEN** session以稳定crypto reason关闭并要求新BattleTicket，不重置sequence继续使用旧key

### Requirement: Battle wire 与 numeric route 必须唯一、闭合且符合 MTU

Battle wire MUST提供versioned binary envelope、`battle/v1` Protobuf payload和唯一numeric registry。`battle.input.bundle`至`battle.resync.response`的8个logical kind MUST一一映射到`3000-3007`、固定direction和唯一raw或KCP lane；route MUST登记owner、QoS、max encoded/logical size、rate、expiry、tick/sequence、idempotency、baseline/recovery和assignment/session binding。Datagram MUST不超过1200 bytes并遵守48-byte IP/UDP、48-byte secure header、16-byte AEAD tag、16-byte raw或24-byte KCP budget；IP fragmentation、通用Any、未登记compression、lane fallback和payload identity override MUST禁止。

#### Scenario: Snapshot 通过 KCP 发送

- **WHEN** sender或registry尝试把full/delta snapshot编码为KCP、TLS/TCP或WSS route
- **THEN** contract/architecture gate失败且运行时在simulation dispatch前拒绝错误lane

#### Scenario: Payload 超过 route 或 MTU

- **WHEN** encoded message超过其registry max或完整datagram超过1200 bytes
- **THEN** sender按登记split policy有界拆分或稳定拒绝，不依赖IP fragmentation、不移除安全字段且不动态换lane

#### Scenario: Go/C++/C# fixture parity

- **WHEN**三种实现消费同一header/protobuf/AAD/crypto/KCP canonical fixture
- **THEN** message ID、bytes、digest、decode result和negative disposition一致，unknown field/version或registry漂移使验证失败

### Requirement: 单 UDP multiplexer 必须有界承载 raw 与 KCP

每个SimulationNode MUST只由C++拥有一个Asio UDP listener；raw、KCP和transport-control MUST共享该socket和authenticated BattleSession。Bind与advertised endpoint MUST分别由Go严格配置，production MUST拒绝port 0、隐式Host推导、地址冲突和自动换端口；本地推荐值为可覆盖的`58445/udp`，loopback test MAY使用`127.0.0.1:0`。KCP adapter MUST精确使用profile登记的10 ms update、window 64、fast resend 2、RTO 30–200 ms、dead-link 10、1000-byte ceiling、queue 64与500 ms expiry，并在reassembly后再次验证application route和deadline。

#### Scenario: Production UDP bind 失败

- **WHEN**配置的battle UDP address被占用、advertised endpoint非法或listener identity与ticket endpoint不一致
- **THEN**simulation node启动失败、process保持non-ready并逆序回滚，不随机选择其他port或下发不可达endpoint

#### Scenario: KCP message 到达时已过期

- **WHEN**可靠message经重传完成reassembly但已超过registry application expiry
- **THEN**message以稳定expired终结，不进入simulation、不继续阻塞queue且不回退raw/TCP/WSS

#### Scenario: 一个 instance 尝试创建第二 listener

- **WHEN**runtime、配置或test为raw、KCP或某个SimulationInstance单独bind socket
- **THEN**architecture gate失败；所有lane必须经node-global authenticated multiplexer

### Requirement: BattleSessionContext 必须唯一绑定 actor 并保护 SimulationInstance

成功握手 MUST产生不可变BattleSessionContext，绑定account session lineage、battle session/generation、endpoint generation、current AssignmentStamp/SimulationInstance、actor slot和route policy。UDP payload MUST不能选择或覆盖PlayerID、role、actor或assignment。C2S input MUST依次通过AEAD/replay、route/direction/size/rate、target/endpoint、InputTick/sequence/expiry和actor command allowlist，再进入SimulationInstance有界inbox；最终transform、hit、damage、effect、death、reward或任意history state声明 MUST拒绝。Simulation worker MUST保持ECS/physics/navigation/history唯一writer。

#### Scenario: Input 携带另一个 actor identity

- **WHEN**已认证Visitor在input payload中提交PlayerID、actor ID或role试图控制Owner或其他实体
- **THEN**payload在simulation前以identity/unsafe-payload reason拒绝，BattleSessionContext绑定的actor和world状态不变

#### Scenario: 合法 input bundle

- **WHEN**raw input bundle的session、endpoint、route、tick window、sequence、expiry和actor command均有效
- **THEN**transport只把规范化command交给绑定SimulationInstance inbox，由固定simulation pipeline裁决结果

#### Scenario: Queue 达到 hard budget

- **WHEN**session/node ingress、egress、simulation inbox或KCP queue达到登记上限
- **THEN**runtime按route policy丢弃可替换snapshot、终结过期数据或关闭hard-backpressure session，不进行无界扩容

### Requirement: Rebind 与 key rollover 必须保持 generation 和 replay 连续

Endpoint rebind MUST由现有traffic key认证并对新remote IP/port执行stateless cookie challenge；成功confirm MUST递增endpoint generation，且不得重置packet sequence、replay window、KCP conversation、actor binding或key epoch。Key rollover MUST在10分钟或`2^20` datagrams先到时通过authenticated transport-control协商next epoch；send只使用current epoch，receive最多在3秒有界overlap内接受previous epoch且分别执行replay window。并发/旧rebind、cookie replay、rollover deadline和epoch wrap MUST fail closed。

#### Scenario: NAT endpoint 合法变化

- **WHEN**持有current BattleSession key的客户端从新endpoint完成rebind cookie与authenticated confirm
- **THEN**server原子递增endpoint generation并切换唯一remote endpoint，后续旧endpoint packet被拒绝且session sequence/KCP状态连续

#### Scenario: 攻击者只重放旧 rebind confirm

- **WHEN**不同remote endpoint重放旧cookie、旧endpoint generation或已接受confirm
- **THEN**rebind拒绝且current endpoint、key、replay和actor binding均不改变

#### Scenario: Previous key overlap 到期

- **WHEN**previous epoch packet在3秒overlap后到达或已在该epoch被接受过
- **THEN**packet以old-epoch/replay reason拒绝，不延长overlap或回退current replay window

### Requirement: Session、target 与 process 失效必须撤销 battle transport

Session epoch递增、logout/forced logout/ban、assignment replacement/lease expiry、target revision变化、membership/role失效、Owner grace终结、instance drain/stop、child/node unhealthy、control EOF、listener failure或hard protocol/crypto violation MUST立即停止该binding的新gameplay dispatch。Go与C++ MUST各自fail closed并通过高优先级revoke/closed control收敛；任一侧先观察到失效时不得等待另一侧恢复旧ticket/session。Shutdown MUST先停止HTTP ticket issuance和新公开输入，再revoke session、bounded drain egress/KCP，随后执行simulation result/fence/instance/node/storage逆序关闭。

#### Scenario: Session epoch 失效

- **WHEN**账号session epoch在BattleSession active期间递增
- **THEN**Go提交权威epoch后发送revoke，C++立即停止该lineage所有battle packet并关闭session；旧ticket/key/endpoint均不能重连

#### Scenario: C++ child crash

- **WHEN**承载active battle session的child异常退出或control EOF
- **THEN**Go撤销node readiness与全部SimulationTarget，已安装ticket和active session随不可复活node identity终结，不由新child恢复

#### Scenario: Shutdown deadline 到期

- **WHEN**KCP/egress无法在有界deadline内drain
- **THEN**runtime记录低敏incomplete outcome并继续撤销ticket/session、assignment和node；未发送transport数据不被解释为持久提交

### Requirement: B0.5 必须通过跨语言安全 transport 实现资格门

本capability MUST提供versioned schema/manifest、Go/C++/C# fixtures、threat model、dependency lock、真实child/loopback UDP harness和唯一qualification report。Mandatory gates MUST覆盖wire/crypto/KCP parity、ticket response-loss/consume、forged/replay/amplification/reorder/duplicate/expiry/MTU/backpressure/rebind/rollover/shutdown、own/visit、8/9 actor、assignment/session失效、child crash/Go restart，以及B0.3/B0.4、server v1、client v1和OpenSpec strict regression。报告 MUST绑定source/binary/toolchain/dependency/model/profile/control/wire/registry/config/fixture digest且保持低敏；只可声明`secure-transport-qualified-windows-x64`，不得替代B0.6网络发布资格。

#### Scenario: 完整 B0.5 verify 通过

- **WHEN**当前源码的全部mandatory gate连续运行通过且报告digest一致
- **THEN**B0.5可声明secure transport implementation-qualified并仅解锁`qualify-battle-network`

#### Scenario: 只有 loopback happy path 通过

- **WHEN**真实socket能完成handshake和input，但安全negative、KCP parity、8/9 actor、失效或既有v1 regression任一未通过
- **THEN**qualification保持未完成，UDP listener不得作为production-ready或Unity runtime进入证据
