# 网络传输架构

## 文档职责

本文档定义 iHomeland 的 HTTPS、WSS、TLS/TCP、裸 UDP 和 KCP 职责，以及统一会话、消息路由、安全、故障和验收规则。协议编号与兼容规则见 `docs/protocol-compatibility.md`。

## 核心原则

- 按交付语义选择通道，不按业务模块或技术展示选择。
- 一个实时 message id 只有一个 allowed channel。
- 多通道共享唯一账号 session 与 session epoch。
- Transport adapter 不实现业务状态机。
- 生产可靠通道必须使用 TLS。
- UDP/KCP 必须与安全、限流和网络模拟同时交付。
- 第一阶段只启用 HTTPS、WSS 和 TLS/TCP。

## 目标拓扑

```text
                    HTTPS
          version / config / account / ticket
                       |
                       v
              +------------------+
              | HTTP Adapter     |
              +------------------+

                    WSS
       maintenance / kick / endpoint update
                       |
                       v
              +------------------+
              | Control Adapter  |
              +------------------+

                  TLS/TCP
          world / visit reliable business
                       |
                       v
              +------------------+
              | Business Adapter |
              +------------------+

        Go adapters -> Auth Context -> Dispatcher
                                  |
                        Application Services

Gameplay target on C++ Game Simulation Server:
        one UDP socket -> authenticated multiplexer
                         |                    |
                     raw UDP               KCP
```

当前 v1 个人世界只使用：

```text
HTTPS -> world bootstrap / invite accept / admission issue
WSS   -> invite / owner availability / assignment / close notice
TCP   -> PersonalWorld snapshot / VisitSession command / safe-return
```

Gameplay 目标增加：

```text
UDP   -> replaceable continuous input bundles and authoritative snapshots
KCP   -> registry-approved late-value discrete commands/events and resync
```

Owner 只拥有 PersonalWorld 业务事实，不是 P2P host。HTTPS/WSS/TLS-TCP 终止于 Go adapter，UDP/KCP 终止于 C++ Game Simulation Server adapter。一个 message id 只能选择一个登记通道，不能因为 Owner/Visitor 角色不同而跨 WSS、TCP、raw UDP 或 KCP 双写。

现有代码中的 `tcpgameplay`/`ClientGameplayChannel` 是历史稳定名称，实际只承载可靠的 world/visit business contract；它不是未来 C++ battle simulation transport，后者由独立 `BattleNetworkClient` 与 UDP/KCP registry 拥有。

## 通道职责

### HTTPS 启动与账号面

承担：

- build/version compatibility
- environment configuration
- endpoint manifest
- register/login/logout/token renewal
- connection ticket issuance
- patch/content manifest
- bounded diagnostics upload
- 个人世界阶段的 world bootstrap、invite accept 与一次性 admission issuance

不承担：

- 个人世界与访客会话实时状态
- 心跳型在线状态
- 高频战斗输入或快照

HTTP 请求必须有大小、超时、限流、幂等和结构化错误策略。

当前公开 HTTP component 已实现并接线冻结 OpenAPI 中的 10 个 operation：version/config、register/login/refresh/logout、connection ticket、world bootstrap、invite accept 和 world admission issue。它使用独立于 diagnostic 的 listener、production MySQL/Redis adapters、原子 Session 认证、按 operation deadline 与两阶段限流；production 只允许 TLS 1.3，本地明文仅允许 loopback。

### WSS 带外控制面

承担：

- maintenance notice
- forced logout / ban notice
- queue status
- endpoint or placement reassignment
- battle endpoint allocation
- session epoch invalidation notice
- visit invite、Owner availability、VisitSession close 与 world assignment update

不承担 world mutation、资产修改或经济操作。WSS 与 HTTPS 可以同进程或同端口托管，但逻辑路由必须独立。

当前服务端在公开 HTTPS listener 上实现精确 `GET /v1/control`，固定 subprotocol 为 `ihomeland.control.v1`。客户端通过 `Authorization: Ticket <32 位小写十六进制 nonce>` 提交一次性资格；query、cookie、payload、错误 Host/Origin、错误 subprotocol、非 TLS 1.3 与 production 明文全部在 upgrade 前拒绝。Native 客户端可以省略 Origin，提供时必须精确命中启动配置 allowlist。

连接建立后只允许服务端发送 message ID 500-504、2003、2100-2102 的 deterministic binary `ReliableEnvelope`。客户端发送任何 application text/binary frame 都会收到稳定 policy close；control connection 不提供 command dispatcher。每连接拥有独立单 reader、单 serialized writer、从 1 开始的 sequence、item/byte 双重有界队列、write/ping/pong/idle deadline 和禁压缩策略。Registry 只索引 connection/session/player 引用，不保存世界、访客、presence 或奖励事实。

`publicApi.websocketControl` 的配置职责如下；默认开发值只由 `server/config/local.yaml` 维护，本文不复制数值：

| 配置组 | 字段 | 约束语义 |
|---|---|---|
| 握手 | `path`、`subprotocol`、`allowedHosts`、`allowedOrigins` | 前两项是冻结契约；Host 必须命中白名单，native client 可省略 Origin，提供时必须命中白名单 |
| 认证前资源 | `preAuthRate`、`maxRemoteEntries`、`remoteIdleTtl` | 以规范 remote IP 做进程内 token bucket；状态数量和空闲寿命均有硬上限，不写入 Redis |
| 连接预算 | `maxConnections`、`maxPerRemote`、`maxPerSession`、`maxPerPlayer` | active 与 upgrade 前 reservation 共同计入限制，超限时不消费 ticket |
| 队列预算 | `queueItems`、`queueBytes` | 同时限制待发送 envelope 数量和完整编码字节数；任一超限都关闭 slow consumer，不丢弃后继续伪装健康 |
| I/O 与关闭 | `writeTimeout`、`pingInterval`、`pongTimeout`、`idleTimeout`、`closeTimeout` | write/ping 使用独立 deadline；idle 是自上次成功 write/ping 起的兜底上限；所有连接并行进入 close，并在一个总 shutdown deadline 内等待 |

`VISIT_CLOSED_NOTICE_PUSH` 只收敛控制面 UI 和可见性，不驱动 gameplay connection 返回。`VISIT_SAFE_RETURN_PUSH` 只在 TLS/TCP 上表达当前连接的权威返回动作；两者不是同一消息的双通道副本。

### TLS/TCP 权威可靠业务面

承担：

- PersonalWorld/WorldInstance snapshot
- VisitSession join/leave/kick/reconnect 与 safe-return
- chat、inventory、task、economy 等未来可靠业务
- request/response 与 authoritative push
- 个人世界阶段的 world snapshot、VisitSession 控制命令与 safe-return

要求：

- length-prefix framing
- 最大帧、read chunk/partial frame/batch/connection memory budget 与 read/write deadline
- 每连接唯一 reader/writer
- bounded writer queue 与 backpressure
- request correlation 与 push dispatcher
- ticket/session binding
- route、scope、rate、idempotency 校验

当前 v1 TCP stream 先发送一条 transport authentication preface，再切换为 `ReliableEnvelope`：

```text
uint32_be frame_length
"IHTP" | uint16_be version=1 | purpose_byte
uint16_be ticket_length | uint16_be admission_length
ticket_ascii | admission_ascii
```

ticket 固定为 32 字节小写十六进制，admission 固定为 `wad1_` 开头的 48 字节安全 ASCII；purpose 只允许 `OWN_WORLD`、`JOIN`、`RECONNECT`。preface 不是业务 message，不占用 message ID。服务端在读取前先取得 global/remote reservation，按 ticket 后 admission 的顺序提交；后一步失败不补偿已提交 credential。`JOIN`/`RECONNECT` 连接先进入 pending，只能在 admission deadline 前提交匹配 command，application 成功后才转 active。

preface 和后续业务 frame 都使用 4-byte unsigned big-endian 长度前缀，但分别执行 handshake 与 realtime frame 预算。单次 `Read` 不代表完整 frame；reader 必须先验证声明长度再分配，并处理半包、粘包与有限批量 frame。每连接只有一个 reader 与 serialized writer，response、error、push 共享单调 S2C sequence 和双重有界发送队列。

Active gameplay connection 使用 registry 登记的 common heartbeat `1/2` 保持应用层活性。客户端每 15 秒通过同一 writer/pending owner 发送一次 typed request；服务端返回精确 correlation 的空 response，合法 heartbeat 与其他合法 C2S frame 一样刷新 read idle deadline。单次 heartbeat 使用 10 秒 operation deadline；超时、错误 response、协议错误或 transport failure 都终结当前 connection generation，由产品层显示一次可重连终态。heartbeat 只证明已认证连接存活，不读取或修改 PersonalWorld、VisitSession 等业务事实。

服务端和当前 Unity 客户端均已支持 heartbeat `1/2`。服务端仍保留 30 分钟 idle safety 上限，用于回收旧/异常客户端、进程挂起或应用 heartbeat 未能运行的连接；OS TCP keepalive 不替代该应用层活性证明。未来改变 heartbeat cadence、deadline 或消息语义仍必须先保证服务端向后兼容，再发布客户端。

### 裸 UDP 不可靠时序面

初始 lane 分类如下；最终 message id、频率与大小由 battle network profile 和 registry 冻结：

| 方向 | 初始消息类别 | 交付语义 |
|---|---|---|
| C2S/S2C | latency、NAT、path、cookie probe | 可丢失、严格限量，不进入 gameplay state |
| C2S | 连续移动/视角等 `BattleInputBundle` | unreliable-sequenced；可冗余携带少量尚未确认 InputTick，过期即丢弃 |
| S2C | full/delta `BattleSnapshot`、transform/presentation state | unreliable-sequenced；新 snapshot 覆盖旧 snapshot |
| S2C | 允许丢失的 telemetry/presentation hint | unreliable-sequenced；不得成为伤害或结算事实 |

设计思想是“丢失后等待更新包”，不能通过重试把旧数据变成可靠业务。数据包以不触发 IP 分片为目标，`1200 bytes` 只作为初始预算，最终值必须通过 MTU 测试确认。

权威 full/delta snapshot 禁止进入 KCP。若客户端丢失 delta baseline，只能等待/请求按 profile 允许的后续 full baseline，不能可靠重传整条连续 snapshot 流。

### KCP 低延迟可靠战斗面

初始只允许承载：

| 方向 | 初始消息类别 | 交付语义 |
|---|---|---|
| C2S | 经玩法模型确认“丢失不可接受且晚到仍有意义”的离散 ability/weapon command | reliable-ordered；必须含 InputTick、command sequence 与 expiry |
| S2C | 必须可靠观察的实例内离散 lifecycle/ability result | reliable-ordered；必须可去重且不能替代 Go 结算事实 |
| C2S/S2C | profile 明确登记的 resync/control message | reliable-ordered；有独立大小、频率与超时预算 |

KCP 只提供 ARQ。握手、身份、加密、重放保护、拥塞预算、限流和 endpoint rebinding 仍由项目负责。应用层必须继续校验 tick、sequence、过期与合法性。

同一 message id 只能登记 raw UDP 或 KCP 其中一个 lane，禁止为了“保险”双写。调用方不得运行时选择 lane，也不得在 raw 超时后把相同消息静默转入 KCP/TCP；改变 QoS 必须变更 registry、兼容性和网络资格基线。

## Message Route Registry

每个实时 message 必须登记：

| 字段 | 说明 |
|---|---|
| `message_id` | 稳定唯一编号 |
| `owner` | account、session、control、world、visit、battle 等 |
| `direction` | c2s、s2c、bidirectional |
| `allowed_channel` | wss、tcp、udp、kcp |
| `auth_scope` | anonymous、control、gameplay、battle 等 |
| `qos` | reliable/unreliable、ordered/sequenced |
| `max_size` | 编码后最大字节数 |
| `rate_limit` | identity/connection/message 维度限制 |
| `idempotency` | none、request id、command id、tick/sequence |
| `timeout` | request 或 command 有效时间 |
| `assignment_binding` | 是否必须绑定完整 AssignmentStamp、SimulationInstanceID 与 connection generation |
| `tick_semantics` | `InputTick`/`ServerTick` 的产生者、单调范围、允许窗口与确认方式 |
| `snapshot_policy` | none、full、delta；delta 的 `BaselineTick`、恢复路径和可覆盖规则 |
| `input_ack` | 是否以及如何携带 `LastProcessedInputTick` |
| `expiry` | 发送者时间不可直接受信；服务端映射、clamp 和过期处理 |
| `history_policy` | none 或服务端内部 lag-compensation 查询；允许字段、最大窗口与过期语义 |

服务端入口必须在 decode 后、业务执行前校验 route。错误通道消息只能产生结构化协议错误或安全关闭，不能进入 application service。

Battle 输入、snapshot 与历史查询还必须遵守：

- C2S input 含 `InputTick`、单调 command sequence、assignment/instance binding 和 expiry evidence，不含最终 transform、target hit、damage 或 reward。
- S2C snapshot 含 `ServerTick`、`SnapshotSequence`、full/delta kind、适用时的 `BaselineTick`，以及本地 actor 的 `LastProcessedInputTick`。
- 客户端缺少 baseline、跨 generation 或收到旧 sequence 时不得猜测合并；恢复路径由 registry 唯一定义。
- history query 只由服务器 gameplay system 发起。客户端最多提供经 tick mapping 与 clamp 的观察 tick evidence，不能指定任意历史帧或读取历史状态。
- Tick 宽度、wrap/epoch、tick duration、输入窗口、snapshot cadence、baseline 周期和 history window 必须由 simulation model/network profile 冻结后进入 schema。

## 统一会话

### Account Session

HTTPS 登录建立：

- `session_id`
- `session_epoch`
- `player_id`
- access token
- expiry 和 auth scopes

账号 session 是身份事实来源。

### Connection Ticket

连接 WSS/TCP/UDP-KCP 前，通过 HTTPS 申请：

- short TTL
- one-time nonce
- target audience/channel
- endpoint/placement assignment
- session id/epoch
- authorized scopes

ticket 使用后立即失效，不能跨通道或跨 endpoint 重放。

### World Admission

World admission 与 session bearer、`ConnectionTicket`、invite、`AdmissionIntent` 分层且不可互换：bearer 只证明 account/session lineage；ticket 只允许建立目标 endpoint/channel 的连接；invite/intent 只表达领域资格；admission 才允许该连接进入一个 current PersonalWorld/VisitSession target。GAMEPLAY scope 本身不授予 Owner/Visitor role。

Admission 是短期、一次性 opaque credential。Issuer/verifier 必须绑定 PlayerID、SessionID/epoch、Owner/Visitor role、PersonalWorldID、可选 VisitSessionID、`OWN_WORLD`/`JOIN`/`RECONNECT` purpose、完整 current AssignmentStamp、endpoint、`TLS_TCP` channel、issued-at 与 expiry。原子消费使用 credential digest 与稳定 consume identity，不复用 ConnectionTicket nonce。`JOIN` 只允许 active reserved membership，`RECONNECT` 只允许 active reconnecting membership；expiry、replay、旧 assignment/epoch 或错误 endpoint/channel 均 fail closed。

`internal/worldadmission` 与 `internal/storage/worldadmission` 已实现并独立验收 issuer/verifier、digest-only Redis binding、单次消费、精确 response-loss 重试和 VisitSession 二次校验；production Composition Root 已接线 HTTP issuance、TLS/TCP consume、业务 producer、Owner/Visitor grace 与 expiry cleanup、assignment invalidation 和 safe-return。该竖切完成不替代 `qualify-server-v1` 的独立 Go 协议客户端与全量故障资格验收。

### Connection Context

每个已认证连接持有只读 context：

- connection id
- session id/epoch
- player id
- channel kind
- scopes
- connected/last activity time
- remote endpoint 摘要

业务 payload 不携带可覆盖 context 的身份字段。

## 可靠消息格式

WSS、TCP 和 KCP 共享逻辑 envelope 语义：

- protocol version
- message id
- request id 或 command id
- sequence
- server/client timestamp（明确单位）
- payload
- trace/correlation metadata（有界）

TCP 在线包外层增加固定 endian 的长度前缀。单次 `Read` 不等于一个 message；codec 必须处理半包、粘包、多个连续帧和非法长度。

KCP 是否复用完整 envelope 由 battle profile 决定，但逻辑 message id、session、tick、序列和错误语义必须一致。

## UDP Datagram

裸 UDP 使用紧凑 header，至少包含：

- magic/version
- battle session id 摘要
- channel kind
- message id
- sequence/tick
- nonce/key epoch
- authenticated payload

不使用通用 `Any` 包装。裸 UDP 与 KCP 默认共享 listener 和安全 session，通过认证后的 channel kind 分流。

## 服务端连接模型

- WSS/TCP 各连接只有一个 receive loop 和一个 serialized writer。
- Reader 必须限制单次读取、未完成 frame 缓冲、单批 dispatch 数和每连接总内存；单帧上限不能替代连接级资源预算。
- Connection Registry 按 connection/session/player 建索引。
- Connection Registry 只索引 PersonalWorld/VisitSession/WorldInstance 发送引用，不保存世界、访客或奖励最终事实。
- Writer queue 必须有容量、丢弃/关闭策略和指标。
- 慢消费者不得无限占用内存。
- 连接关闭必须产生稳定 reason，并解除全部索引。
- Session epoch 失效必须广播到所有绑定连接。

## 安全边界

HTTPS/WSS/TCP：

- production TLS
- 证书与私钥不进入仓库
- Origin/Host 与 endpoint validation
- request/frame size、deadline、rate limit
- token/ticket 脱敏
- structured auth failures

UDP/KCP：

- short-lived ticket
- cookie challenge 与抗放大
- AEAD
- key epoch 与 nonce discipline
- replay window
- endpoint binding/rebinding validation
- per-session/per-IP rate limit
- malformed packet fast reject

账号密码、长期 token、资产、奖励和结算不得通过裸 UDP。

## 故障语义

| 故障 | 行为 |
|---|---|
| HTTPS 不可用 | 禁止新登录/ticket；已建立实时连接按 session 有效期处理 |
| WSS 中断 | TCP 业务可在有限 grace 内继续，客户端必须重连控制面 |
| TCP 中断 | 权威业务 command 暂停，进入受控重连 |
| Owner 中断 | VisitSession 进入有界 grace；deadline 到期后关闭访客访问并安全返回，不迁移 Owner 权限 |
| WorldInstance lease 失效 | 旧实例停止签发 admission 和提交 mutation，由 placement 解析当前 assignment |
| Redis 不可用 | 根据 operation fail-closed 或降级，不能假装持久事实成功 |
| UDP/KCP 中断 | 按 battle 玩法重连、暂停或退出，不静默转发 TCP |

KCP 与裸 UDP 使用同一底层网络，因此 KCP 不是 UDP 被阻断时的 fallback。

## 可观测性

每个通道至少记录：

- active/accepted/rejected connections
- auth/ticket failures
- bytes/messages in/out
- request latency/timeout
- writer queue depth/backpressure closes
- protocol/route/rate errors
- reconnect 和 close reasons
- RTT、jitter、loss、retransmit（适用通道）

日志使用 connection id、session id 摘要、player id、message id、request id 和低敏 PersonalWorld/VisitSession/WorldInstance 关联值，不记录完整凭据、invite 或 admission secret。

## 验收

第一阶段：

- HTTP contract、closed-schema codec、TLS 配置与真实 MySQL/Redis integration
- WSS auth、control push、慢消费者和关闭
- TCP framing、并发请求、push、背压、重连和 shutdown
- 错误通道路由拒绝
- session epoch 全通道失效
- Go test clients own-world、visit-world、reconnect 与 safe-return 场景
- invite expiry、capacity、重复接受与越权 world/owner payload
- admission 一次性消费、session epoch、endpoint、instance 与 assignment generation 绑定
- Owner reconnect grace、旧 binding/timer、VisitSession close 与 Visitor 安全返回
- WorldInstance lease/fencing、重建、旧 endpoint 和陈旧实例拒绝

战斗阶段额外覆盖：

- latency、jitter、loss、reorder、duplicate
- bandwidth limit、burst、pause/resume
- endpoint change、replay 和 forged packet
- KCP 参数、重传和带宽放大

所有网络模拟必须可重复并记录参数。

当前统一真实存储入口为 `tools/storage/storage.ps1 -Action verify`。它覆盖 10 个公开 HTTP operation、真实 Redis WSS/TCP ticket 一次性消费、TCP world admission，以及真实 wire `OWN_WORLD` snapshot 与 VisitSession `OPEN`/`CREATE_INVITE`/HTTP `ACCEPT`/`JOIN`/断线 `RECONNECT`/`LEAVE`/`KICK`/`CLOSE`。同一 harness 还验证 response/push 顺序、精确 safe-return 后关闭、stale admission、assignment replacement、Redis flush、进程重建、跨通道 logout 失效和资源清理。通过表示个人世界服务端业务竖切可用，不表示 `qualify-server-v1` 的独立协议客户端、压力/故障全矩阵或 Unity gate 已完成。
