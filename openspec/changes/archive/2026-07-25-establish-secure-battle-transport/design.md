## Context

B0.1/B0.2 已冻结 `battle-model-v1` 与 `battle-network-profile-v1`：20 Hz simulation、40 Hz input、10 Hz snapshot、1200-byte datagram、16-Tick history、8-actor qualified cap、raw/KCP 唯一 lane 和 8 个 logical message kind。B0.3 已交付无 listener 的 C++ `SimulationInstance`，B0.4 已交付由 Go 通过继承 stdin/stdout 监督的本机 C++ `SimulationNode`、current `SimulationTarget`、result replay 与连续一致的 `control-qualified-windows-x64` evidence。

当前仍缺少 battle wire、numeric registry、客户端可取得的短期凭据、真实 UDP/KCP、密钥与重放保护。现有 WSS/TLS-TCP `ConnectionTicket`、WorldAdmission 和 `GAMEPLAY` scope 只服务个人世界 v1，不能直接授予 battle actor 或复用为 UDP bearer。Go 继续拥有账号 Session、PersonalWorld/VisitSession policy、placement、HTTP 和 child lifecycle；C++ 是 simulation、battle socket、packet/session queue 与 actor ingress/replication 的唯一 runtime owner。

该 change 同时跨越 Go/C++/协议/存储/配置/部署和安全边界。所有新 C++ dependency 必须先在 `versions.yaml` 锁定精确 version/source identity/SHA-256/license/owner/rollback，普通 build 在恢复后必须可离线执行。Windows x64 是当前实现资格平台；Linux production 和 Unity runtime 仍未解锁。

## Goals / Non-Goals

**Goals:**

- 建立客户端只能经 HTTPS 获取、一次使用且绑定 current `SimulationTarget` 的 BattleTicket。
- 建立一个由 C++ child 拥有、Go lifecycle 管理的 UDP listener，复用 raw/KCP lane 和同一安全 session。
- 冻结可跨 Go/C++/C# 验证的 binary wire、numeric registry、握手、密钥、nonce、replay、rebinding 与 rollover 语义。
- 将已认证 battle input/replication 接到现有 `SimulationInstance`，且身份、Tick、route、容量和 assignment 全部 fail closed。
- 以真实 loopback socket、crypto/KCP parity 和安全负例补齐 B0.5 implementation evidence，只解锁 B0.6。

**Non-Goals:**

- 不执行 B0.6 的完整 latency/jitter/loss/reorder/bandwidth/soak 发布资格，不声明公网网络已 qualified。
- 不实现 Unity gameplay runtime、预测/校正/插值 View、Cinemachine、HUD、产品内容或 PC build。
- 不增加奖励、资产、结算、inventory、ActivityInstance、Room、Party、跨服、观战或 replay 下载。
- 不增加跨主机 Go/C++ control、多个在线 SimulationNode 调度、gRPC、QUIC 或 TCP fallback。
- 不让 UDP/KCP 携带账号密码、access/refresh token、WSS/TCP ticket、WorldAdmission、资产、奖励或结算事实。

## Decisions

### 1. Battle transport 使用独立 owner，并保持四类资格不可互换

资格链固定为：

```text
HTTPS access AuthContext
  -> PersonalWorld/VisitSession role policy
  -> current SimulationTarget + actor capacity
  -> BattleTicket installation
  -> cookie-validated authenticated handshake
  -> BattleSessionContext
  -> route/tick/actor authorization
```

`BattleTicket` 是独立 capability，不扩大现有 WSS/TCP `ConnectionTicket` 或 WorldAdmission。它绑定 SessionID/epoch、PlayerID、Owner/Visitor role、PersonalWorldID、可选 VisitSessionID、完整 AssignmentStamp fingerprint、RuntimeNodeID、SimulationNodeID、SimulationInstanceID、mapping generation、target revision、model/profile/config/wire identity、actor slot、受信 advertised UDP endpoint、ticket ID、issuance identity 和绝对 expiry。HTTP request 只能提供 target selector 与 idempotency key；所有身份、role、endpoint、capacity 和 assignment 均由服务端 owner 解析。

备选方案是复用 TLS/TCP ticket 或 WorldAdmission。否决原因是二者的 audience、消费点和授权含义均不同，复用会让 `GAMEPLAY` scope 或 world membership 被误解释为 battle actor 权限。

### 2. Ticket 先安装到精确 C++ node，HTTPS 才返回成功

Go `BattleTicketIssuer` 以 deterministic secret derivation 和 digest-only issuance record 支持 HTTP response-loss replay。它先读取 current role/target，随后通过 versioned private control frame 向 target 所在 child 安装：

- immutable ticket binding；
- 从 ticket secret 与 binding 派生的 256-bit proof key；
- expiry、actor slot 与协议 identity；
- install request identity。

C++ ticket state 只能按 `Installed -> Consumed -> Revoked/Expired` 迁移。相同 install request 与完全相同 binding 幂等返回原 actor slot；ticket ID 或 request ID 字段漂移必须拒绝。C++ 同一 instance 对 installed + active actor slot 共同执行 8-actor hard cap。Go 只在 Redis issuance 成功且 exact child 返回 installed 后返回 ticket；部分成功通过 status/revoke 和有界 cleanup 收敛。

扩展后的 stdio control 仍是唯一 Go/C++ control channel，新增 ticket install/status/revoke、session revoke/closed 与 listener status frames。Lifecycle/health 使用高优先级 lane，ticket 操作使用独立有界低优先级队列，不能饿死 drain/stop/health；frame 仍受 64 KiB、sequence、nonce、closed schema 和 stdout-only 约束。proof key 标记为 secret field，只允许在继承 pipe 与 C++ locked memory 中存在，禁止进入 fixture、日志、report、crash dump 或 Redis。

备选方案是让 C++ 访问 Redis 或在 UDP 首包回调 Go 验证。前者制造第二个 session/storage owner，后者把公网握手压力引入 lifecycle control lane，均否决。自包含 bearer ticket也不能提供可靠的一次消费与 target 撤销，因此不采用。

### 3. 握手采用 stateless cookie + PSK-authenticated ephemeral X25519

HTTPS response 返回高熵 ticket ID、32-byte ticket secret、advertised endpoint、wire suite、expiry 和低敏 binding projection。ticket secret 永不通过 UDP 发送。客户端与 C++ 都以 HKDF-SHA-256 从 ticket secret/binding 派生 proof key，C++ 只保存 proof key。

握手固定为：

```text
ClientHello(ticket_id, client_nonce, client_ephemeral_x25519, padding)
  <- Retry(cookie, cookie_epoch)
ClientAuth(repeated hello, cookie, transcript_hmac)
  <- ServerAccept(server_nonce, server_ephemeral_x25519, encrypted session parameters)
```

- `Retry` cookie 是 HMAC-SHA-256 截断 128 bit，绑定 wire version、listener identity、remote IP/port、ticket ID、client nonce/public key 和短时间片；cookie key 仅在 C++ process memory 中轮换，最多接受 current/previous epoch。
- 收到合法 cookie 前不查询/消费 ticket、不做 X25519、不分配 session/KCP/actor queue，且 response bytes 不超过对应 request bytes。
- `ClientAuth` 先验证 cookie，再查找 exact installed ticket，验证 transcript HMAC，随后一次性消费 ticket并生成 server ephemeral。
- traffic secret 使用 X25519 shared secret、proof key 和完整 transcript hash 经 HKDF-SHA-256 extract/expand，按方向派生独立 ChaCha20-Poly1305 key、96-bit nonce salt 和 rekey secret。
- `ServerAccept` 是首个 AEAD 保护的响应；相同 ticket、endpoint 与 transcript 的 bounded retry 只重放同一 accept，不创建第二个 session。任一字段漂移或已消费 ticket 的新 transcript 均拒绝。

算法固定为 RFC 7748 X25519、RFC 5869 HKDF-SHA-256/HMAC-SHA-256 与 RFC 8439 ChaCha20-Poly1305。C++ crypto provider 置于项目 `CryptoProvider` adapter 后，先锁定经过维护且支持 constant-time primitive 的 exact dependency；Go/C# fixture 使用独立实现和 RFC vectors 验证，不把 provider 类型扩散到 domain、simulation component 或协议 API。

备选方案包括明文发送 opaque ticket、仅 PSK 无 ephemeral DH、长期 server static key 和自研 primitive。它们分别存在被动窃取竞速、缺少前向保密、长期 key 部署复杂度或不可接受的密码实现风险，因此否决。

### 4. 安全 datagram 使用固定 header、方向密钥和不可复用 nonce

握手后的 datagram 总长不得超过 1200 bytes。versioned wire schema 固定：

- 48-byte secure session header，作为 AEAD AAD，包含 magic/version、packet kind/lane、SessionID 摘要、battle session generation、key epoch、64-bit packet sequence、payload length 与必要 binding discriminator；
- raw lane 使用额外 16-byte route header；
- KCP segment 使用冻结的 24-byte KCP header，reassembled logical message 再使用有界 route envelope；
- 16-byte AEAD tag；IPv6 + UDP 预算按 48 bytes 计；
- 禁止 IP fragmentation、隐式压缩、通用 `Any`、未登记 padding 扩张或为适配 MTU 删除安全字段。

每个方向、每个 key epoch 使用独立 key。96-bit nonce由该方向的 32-bit epoch nonce prefix 与 64-bit packet sequence 组成；sequence 在同一 key 下严格单调且不得 wrap、回退或重置。每个 receive epoch 使用固定 256-packet sliding replay bitmap，在解密成功后提交窗口；too-old、duplicate、future-jump 超限和错误 epoch均在 application dispatch 前拒绝。

发送端在 10 分钟或 `2^20` datagrams（先到者）发起 rollover。transport-control frame 协商 next epoch 和随机 rekey nonce，新 key 从当前 rekey secret派生；发送只使用 current epoch，接收端最多保留 previous epoch 3 秒且仍应用独立 replay window。epoch wrap、rollover deadline、identity 漂移或 sequence exhaustion 关闭 session并要求重新申请 BattleTicket。

### 5. 一个 Asio UDP socket 承载 raw/KCP 与 transport control

每个 `SimulationNode` 只创建一个 Asio UDP listener。Go 配置同时提供 bind address 与 advertised endpoint；production 禁止 port `0`、通配 advertised host、自动递增或 bind/advertised 隐式推断。仓库提供可覆盖的本地推荐 `58445/udp`，production 实际端口仍由部署配置和 ticket 下发；loopback tests 使用 `127.0.0.1:0`。

C++ authenticated multiplexer按安全 header 的 packet kind 将数据送入：

- `raw`：input bundle、probe、full/delta snapshot；
- `kcp`：ability reliable event、entity lifecycle、resync request/response；
- `transport-control`：handshake 后的 key update、rebind、close/ack，不占 gameplay numeric message ID。

KCP 只承担有限 ARQ，不承担认证、加密、会话、rate limit 或业务过期。adapter 必须精确消费 profile 的 10 ms update、window 64、fast resend 2、RTO 30–200 ms、dead-link 10、segment/message ceiling 1000 bytes、queue 64 与 500 ms expiry。KCP 重组完成后仍执行 numeric route、direction、size、tick、expiry 和 idempotency 校验；过期消息不得转入 raw、TCP 或 WSS。

备选的 per-instance socket、raw/KCP 双 listener 和 QUIC 均否决：它们扩大端口、NAT、资源与运维面，且偏离已冻结 profile。

### 6. Battle Protobuf payload 与 binary envelope 分层

新增 `battle/v1` Protobuf source，统一入口生成 Go/C#/C++ code；C++ 使用 lite runtime并由项目 codec adapter隔离 generated types。numeric owner range新增 `battle: 3000-3199`，初始一一映射：

| ID | Logical kind | Direction | Lane |
| ---: | --- | --- | --- |
| 3000 | `battle.input.bundle` | c2s | raw |
| 3001 | `battle.probe` | c2s | raw |
| 3002 | `battle.snapshot.full` | s2c | raw |
| 3003 | `battle.snapshot.delta` | s2c | raw |
| 3004 | `battle.ability.reliable-event` | s2c | kcp |
| 3005 | `battle.entity.lifecycle` | s2c | kcp |
| 3006 | `battle.resync.request` | c2s | kcp |
| 3007 | `battle.resync.response` | s2c | kcp |

registry 是唯一 route source，包含 owner、direction、lane、QoS、max encoded/logical size、rate、expiry、sequence/tick/idempotency、baseline/recovery、assignment/session binding。payload 不携带可覆盖 Session/Player/actor/role/assignment 的权威身份；若为诊断携带 binding fingerprint，只能与 `BattleSessionContext` 比对，不能作为输入。

Wire fixtures 固定 header endian/width、protobuf bytes、AAD、handshake transcript、cookie、key derivation、AEAD ciphertext/tag、replay outcome、KCP segment、malformed cases 和 registry digest。Go/C++/C# 必须双向 decode/encode parity；任何一侧修改 expected golden 来适配本地默认值均失败。

### 7. Actor ingress 与 replication 复用 SimulationInstance owner

Ticket install 在 exact `SimulationInstance` 上预留 `BattleActorBinding`；PlayerID 只来自 Go control binding，UDP payload不含可选 player override。成功握手把网络 session generation、endpoint generation、actor binding 和 current AssignmentStamp固化为只读 `BattleSessionContext`。

输入处理顺序固定为：

```text
AEAD/replay
-> route/direction/size/rate
-> session/endpoint/target binding
-> InputTick/sequence/expiry
-> actor command allowlist
-> SimulationInstance bounded inbox
```

只有 model 已登记 command 字段能进入 simulation。最终 transform、hit、damage、effect、death、reward、任意 history state 或 actor override 直接拒绝。Simulation worker仍是 ECS/physics/navigation/history 的唯一 writer。

Replication从 simulation 的只读 projection/event queue生成，按 snapshot baseline/sequence和 registry 唯一 lane发送。full/delta snapshot 不进入 KCP；KCP reliable event过期后终结或触发登记 resync，不回退其他 transport。队列策略固定有界：raw snapshot保留最新可替换项，input按 tick/sequence去重并在过期后丢弃，KCP queue最大 64，node/session总 ingress/egress使用 profile 256-item hard budget。达到 hard budget时停止接收相应类别并记录稳定低基数 reason，不动态扩容。

### 8. Rebinding、失效和关闭都不恢复旧资格

新 endpoint 的 rebind 必须先用现有 session key发送 authenticated request，再完成绑定新 remote IP/port 的 stateless cookie challenge。成功 confirm递增 endpoint generation；packet sequence、replay window、KCP conversation和key epoch不重置，旧 endpoint随后全部拒绝。并发 rebind、cookie replay、旧 endpoint confirm和超出 per-session rate均 fail closed。

以下事件立即使 BattleTicket/BattleSession 不可继续发送新的 gameplay：

- Session epoch递增、logout、forced logout或ban；
- assignment replacement/lease expiry、target revision变化、instance drain/stop；
- VisitSession membership/role失效、Owner grace终结或safe-return；
- child/node unhealthy、control EOF、listener failure；
- protocol/crypto violation、key rollover failure或hard backpressure。

Go 通过高优先级 revoke control command驱动 C++ 关闭；C++ 自身观察到 instance/node terminal状态时先在本地撤销 packet dispatch，再报告 Go。两侧任一先观察到失效都 fail closed，不能等待另一侧恢复旧资格。

启动顺序为 storage → simulation child/control → battle listener ready → public HTTP issuance → readiness。关闭逆序为停止 HTTP ticket issuance/新 public input → revoke tickets/sessions → drain KCP 与有界 egress → placement/simulation drain/result/revoke/stop → UDP listener/control child → storage。deadline到期允许丢弃未发送的非持久 transport数据，但不得延长 assignment lease、复活 ticket或把未确认 gameplay解释为 committed。

### 9. B0.5 有独立 implementation qualification，不替代 B0.6

新增 `shared/contracts/fixtures/battle/wire/` 与 `tools/secure-battle-transport/`。唯一 `verify` 聚合：

- battle proto/registry/routes/HTTP contract、unknown field与golden parity；
- RFC crypto vectors、handshake transcript、cookie/AEAD/replay/rekey/rebind negative corpus；
- exact Asio/KCP/crypto/Protobuf C++ dependency restore、license/checksum和离线 build；
- C++ unit/ASan/architecture、Go unit/race/fuzz、C# reference fixture parity；
- 真实 child + loopback UDP + Redis ticket issuance/response-loss/one-time install/consume；
- forged/replay/amplification/reorder/duplicate/expiry/MTU/backpressure/rebind/key rollover/shutdown；
- own-world/visit-world、8/9 actor、assignment replacement、Session invalidation、child crash/Go restart；
- B0.3/B0.4、server v1、client v1 mandatory regression、secret/cache/docs/OpenSpec strict。

报告绑定 source、Go/C++ binary、toolchain/dependency、model/profile/control/wire/registry/config与fixture digest，并输出 wire encoded-size、真实 KCP adapter parity、socket CPU/memory/queue的 implementation evidence。它只可声明 `secure-transport-qualified-windows-x64`；完整 fault matrix、公网/NAT包络、长时重传带宽与Unity client表现仍是 B0.6 的结论。

## Risks / Trade-offs

- [自定义 UDP 握手比标准 QUIC 更容易出现协议缺陷] → 只组合标准 X25519/HKDF/ChaCha20-Poly1305 primitive，closed transcript、独立 provider parity、negative corpus与人工 threat-model review共同作为 mandatory gate；不自研密码 primitive。
- [Ticket proof key进入 C++ memory扩大 secret handling面] → 只经继承 pipe传递，使用有界生命周期和可清零容器，禁止日志/fixture/dump；ticket绑定 exact node/instance并短期一次消费，child死亡后不可复活。
- [HTTP issuance与C++ install跨 Redis/进程无法形成单事务] → deterministic issuance、idempotent install/status/revoke和有界补偿收敛；只有两侧都成功才向客户端返回credential。
- [单 UDP socket或单 child故障影响全部实例] → 当前B0.4只有单本机 node资格，故障时撤销全部target并受控关闭；多node隔离必须由真实扩缩容证据驱动后续change。
- [KCP重传造成head-of-line、带宽放大或过期数据堆积] → 仅4类登记消息使用KCP，message/queue/deadline硬上限和过期终结先在B0.5实现，再由B0.6 fault matrix决定发布资格。
- [58445/udp可能在某台开发机被占用] → 它只是可覆盖的本地推荐值；bind失败显式退出，测试用`127.0.0.1:0`，production由部署配置和ticket下发。
- [BattleActorBinding会与VisitSession 33人容量混淆] → battle installed+active slot硬限制8，只拒绝额外battle admission，不踢出Visitor、不修改VisitSession revision或v1流程。
- [B0.5测试客户端被误当作Unity runtime] → C#仅用于contract/crypto reference parity，不创建Unity network adapter、App Scope service、Scene owner、预测或表现代码。

## Migration Plan

1. 先新增 battle wire/threat-model artifacts、numeric registry delta、Protobuf source、HTTP schema和只读validators，不启用listener。
2. 锁定并恢复Asio、KCP、crypto provider与C++ Protobuf lite exact dependency，补齐checksum/license/adapter/rollback与离线build gate。
3. 实现Go BattleTicket policy/store、deterministic derivation、8-actor target admission和新增control frames，以fake child完成response-loss/rollback tests。
4. 实现C++ listener、ticket install state、cookie/handshake/AEAD/replay/rekey/rebind与raw/KCP adapters，先以fixture和loopback harness验收。
5. 接入SimulationInstance actor ingress/replication、有界queue和Go Composition Root；更新配置、`58445/udp`本地示例、容器/部署与shutdown order。
6. 运行wire/crypto/KCP、Go/C++/C#、真实Redis/child/socket、安全负例、B0.3/B0.4、server/client v1 regression和OpenSpec strict，生成连续可重复的B0.5报告。
7. 只有报告为`secure-transport-qualified-windows-x64`且主specs同步归档后，才允许提出`qualify-battle-network`。

部署回滚到前一版Go/C++ binary和不含battle listener的配置。新增Redis keys全部短TTL且不是持久事实；回滚时先停止ticket issuance并撤销/等待TTL，不做恢复或迁移到旧格式。新增proto/message IDs保留reserved，不复用；已发布过的wire version不得被旧binary解释。

## Open Questions

无阻塞问题。公网端口、NAT/运营商矩阵、Linux production、Unity crypto/provider和长期key托管由B0.6/B0.7及部署change基于本change evidence决定；不得在B0.5实现中临时扩张。
