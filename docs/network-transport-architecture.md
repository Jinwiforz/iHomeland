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
        world / visit / reliable gameplay
                       |
                       v
              +------------------+
              | Business Adapter |
              +------------------+

        All adapters -> Auth Context -> Dispatcher
                              |
                    Application Services

Future battle endpoint:
        one UDP socket -> authenticated multiplexer
                         |                    |
                     raw UDP               KCP
```

个人世界阶段复用同一通道职责，不建立 P2P host：

```text
HTTPS -> world bootstrap / invite accept / admission issue
WSS   -> invite / owner availability / assignment / close notice
TCP   -> PersonalWorld snapshot / VisitSession command / safe-return
UDP   -> replaceable transform and presentation snapshots
KCP   -> reliable low-latency combat input and events
```

Owner 只拥有 PersonalWorld 业务事实；全部网络通道仍终止于服务端 adapter。一个 message id 只能选择表中一个通道，不能因为 Owner/Visitor 角色不同而在 WSS、TCP 或 KCP 双写。

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

### 裸 UDP 不可靠时序面

只承载：

- latency/NAT/path probe
- 晚到无价值的连续输入
- 可由新数据覆盖的位置、朝向和表现快照
- 允许丢失的观战或表现数据

设计思想是“丢失后等待更新包”，不能通过重试把旧数据变成可靠业务。数据包以不触发 IP 分片为目标，`1200 bytes` 只作为初始预算，最终值必须通过 MTU 测试确认。

### KCP 低延迟可靠战斗面

只承载：

- 丢失不可接受且晚到仍有意义的帧命令
- 关键战斗事件
- 玩法模型明确要求可靠交付的低延迟数据

KCP 只提供 ARQ。握手、身份、加密、重放保护、拥塞预算、限流和 endpoint rebinding 仍由项目负责。应用层必须继续校验 tick、sequence、过期与合法性。

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

服务端入口必须在 decode 后、业务执行前校验 route。错误通道消息只能产生结构化协议错误或安全关闭，不能进入 application service。

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

Admission 是短期、一次性 opaque credential。Issuer/verifier 必须绑定 PlayerID、SessionID/epoch、Owner/Visitor role、PersonalWorldID、可选 VisitSessionID、`OWN_WORLD`/`JOIN`/`RECONNECT` purpose、完整 current AssignmentStamp、endpoint、`TLS_TCP` channel、nonce、issued-at 与 expiry。`JOIN` 只允许 active reserved membership，`RECONNECT` 只允许 active reconnecting membership；expiry、replay、旧 assignment/epoch 或错误 endpoint/channel 均 fail closed。当前 P0 只冻结 OpenAPI public response 与 semantic corpus，不实现 production 签发或 nonce consume。

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

- HTTP contract 与 TLS 配置
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
