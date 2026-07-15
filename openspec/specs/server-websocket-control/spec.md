# Server WebSocket Control 规格

## Purpose

定义服务端公开 WSS control endpoint、一次性 ticket 握手、连接 registry、可信 PUSH 投递、生命周期与低敏观测边界。

## Requirements

### Requirement: WSS control endpoint 必须复用公开安全 listener并冻结握手契约
服务端 MUST 在公开 HTTP/TLS listener 的顶层 mux 中仅将精确 `GET /v1/control` path 交给 WSS handler，其余请求 MUST 原样委托仍且只能实现 10 个冻结 OpenAPI operation 的既有 HTTP router。WSS MUST 协商 subprotocol `ihomeland.control.v1`，并且 MUST 只从 `Authorization: Ticket <32 位小写十六进制 nonce>` 读取 ConnectionTicket。Production MUST 复用 TLS 1.3；local/test 明文 MUST 只允许 loopback。Handler MUST 在消费 ticket 前校验 readiness、method/path、Upgrade headers、Host、可选 Origin、subprotocol、header grammar、pre-auth rate 与全局/remote reservation；消费产生 AuthContext 后、upgrade 前 MUST 再绑定 SessionID/PlayerID reservation。Upgrade 前失败 MUST 复用既有安全 HTTP error mapper 与 `ErrorResponse`，不得定义 WSS 同义错误。Ticket MUST NOT 出现在 query、cookie、日志、metrics、close reason 或 payload。Diagnostic listener MUST NOT 注册或转发该 route。

#### Scenario: 使用 query 提交 ticket
- **WHEN** 客户端请求 `/v1/control?ticket=...`，但没有符合语法的 Ticket Authorization header
- **THEN** handler在消费SessionStore记录和upgrade前返回稳定非成功响应，URL中的值不被解释、记录或迁移到认证上下文

#### Scenario: Production WSS 不满足安全握手
- **WHEN** production请求未使用TLS 1.3、Host不受信、Origin存在但不在allowlist，或未协商精确subprotocol
- **THEN** upgrade fail closed且ticket保持未消费，服务端不创建connection entry

#### Scenario: 请求 diagnostic listener 上的 control path
- **WHEN** 客户端向diagnostic地址请求 `/v1/control`
- **THEN** diagnostic router返回稳定非成功响应且不会调用WSS handler或SessionStore

#### Scenario: 普通 HTTP 请求经过顶层 mux
- **WHEN** 客户端请求任一冻结OpenAPI operation或未知非WSS path
- **THEN** 顶层mux原样委托既有HTTP router，10个operation的method/path/contract和unknown-path拒绝行为不发生漂移

### Requirement: WSS 认证必须原子消费单通道 ticket并产生只读 AuthContext
WSS handler MUST 解析既有16-byte nonce投影，并调用Session owner以 `ChannelWSS` 和签发时使用的受信advertised endpoint原子消费ticket。成功结果 MUST证明session current、epoch匹配、ticket未过期且未消费、endpoint/channel匹配，并且scope精确为 `CONTROL`；只有该结果才能构造不可伪造的只读AuthContext并接受连接。请求Host、remote address、payload或token字段 MUST NOT覆盖endpoint、principal、SessionID、epoch或scope。Ticket一旦成功消费，即使后续upgrade或网络失败也 MUST NOT恢复或自动重放。

#### Scenario: 两个连接并发消费同一 ticket
- **WHEN** 两个WSS握手并发提交同一合法ticket
- **THEN**至多一个连接获得CONTROL AuthContext并进入registry，另一个以不泄漏私有binding的稳定认证失败结束

#### Scenario: Ticket 绑定旧 epoch
- **WHEN** WSS ticket签发后session epoch已经递增
- **THEN** handler在创建连接前以 `AUTH_UNAUTHENTICATED` 和HTTP 401拒绝ticket，不从ticket公开字段恢复旧AuthContext或区分私有失败原因

#### Scenario: Ticket 消费后 upgrade 失败
- **WHEN** Redis已提交ticket consume，但客户端在upgrade完成前断开或WebSocket accept失败
- **THEN** ticket保持已消费，服务端清理局部资源；客户端只能通过HTTPS申请新ticket

### Requirement: Connection registry 必须有界且只拥有连接引用
WSS owner MUST以服务端CSPRNG ConnectionID索引active connection，并维护SessionID与PlayerID反向索引。Entry MUST只保存只读AuthContext摘要、连接句柄、发送队列与生命周期状态，MUST NOT保存PersonalWorld、VisitSession、assignment、presence或其他业务最终事实。全局、remote identity、SessionID与PlayerID连接数量 MUST具有启动时验证的硬上限；注册、移除、按session失效与并发投递 MUST线性化且不得遗留僵尸索引。Remote identity限流状态 MUST有数量和idle lifetime上限，且不得进入业务Redis。

#### Scenario: 连接风暴达到全局上限
- **WHEN**并发合法或非法握手已经占满配置的全局连接预算
- **THEN**新握手在upgrade前被有界拒绝，不创建goroutine、队列或Redis connection key，并产生稳定低基数观测

#### Scenario: 同一 session 存在多个受控连接
- **WHEN**同一current SessionID在配置上限内建立多个独立WSS连接
- **THEN**registry为每个连接保留独立sequence/queue，同时使按SessionID失效能够查找并关闭全部旧epoch连接

#### Scenario: 连接异常退出
- **WHEN**reader、writer、peer close或heartbeat任一路径结束连接
- **THEN**registry恰好一次移除主索引和全部反向索引，并终止该连接拥有的任务与队列

### Requirement: WSS 只能投递 registry 允许的 binary PUSH envelope
Trusted publisher MUST且只能投递message registry与route registry共同声明为WSS、`CONTROL`、`SERVER_TO_CLIENT`、`PUSH`的消息：500-504、2003与2100-2102。Codec MUST校验message ID与精确generated payload type，确定性编码payload，使用protocol version 1、server Clock timestamp和每连接从1开始单调递增的sequence构造 `ReliableEnvelope`，并对完整encoded envelope同时执行route max size与全局realtime frame limit。每个envelope MUST作为一个未压缩binary WebSocket message写出；text、JSON同义DTO、unknown/wrong-channel message和客户端application data MUST被拒绝。

#### Scenario: 投递 Visit invite push
- **WHEN**受信Visit notifier向目标Player发布message 2100及匹配的 `VisitInvitePush`
- **THEN**每个匹配连接收到route允许大小内的deterministic binary envelope，sequence各自单调推进且payload不能覆盖目标身份

#### Scenario: 尝试通过 WSS 投递 safe-return
- **WHEN**publisher提交只登记在TLS/TCP的 `VISIT_SAFE_RETURN_PUSH` 或任一world/visit response
- **THEN**codec在进入发送队列前拒绝wrong-channel message，不把它改写成control notice或未知push

#### Scenario: 客户端发送 application frame
- **WHEN**已认证WSS客户端发送binary或text application message
- **THEN**reader以稳定protocol/policy reason关闭连接，不调用world/visit application service且不尝试按TLS/TCP route解析

### Requirement: 每条连接必须具备有界队列、serialized writer和liveness deadline
每条WSS连接 MUST只有一个reader loop与一个serialized writer。发送队列 MUST同时限制item数和累计encoded bytes；publisher MUST NOT直接并发写socket。Writer MUST按sequence顺序发送并为每次write设置deadline。达到queue预算、write timeout、frame预算、peer close、ping/pong或idle deadline时，连接 MUST以稳定安全reason关闭并解除索引；消息 MUST NOT因背压被静默丢弃、重排或无限等待。Permessage compression MUST默认禁用，且connection reader/writer/task内存与关闭等待 MUST有硬上限。

#### Scenario: Slow consumer 填满发送队列
- **WHEN**客户端停止读取使queue item或byte预算达到上限
- **THEN**publisher有界返回slow-consumer结果，服务端关闭该连接并清理队列，不阻塞业务owner或丢弃某条消息后继续伪装健康

#### Scenario: 单条 envelope 超过预算
- **WHEN**payload本身合法但完整envelope超过该route maxSize或公开realtime frame limit
- **THEN**codec拒绝入队并记录稳定oversize outcome，不拆分、压缩或截断消息

#### Scenario: Peer 未响应 heartbeat
- **WHEN**连接在配置的pong/idle deadline内没有证明存活
- **THEN**服务端关闭连接、回收全部任务和索引；后续恢复必须使用新WSS ticket重连

### Requirement: Session invalidation 必须通知并关闭全部旧 epoch 连接
Production WSS registry MUST实现Session owner的 `ConnectionInvalidator`。权威epoch已经提交后，invalidator MUST查找目标SessionID下epoch小于新值的全部连接，尝试在独立短deadline内投递匹配安全reason的 `CONTROL_SESSION_INVALIDATED_PUSH` 或 `CONTROL_FORCED_LOGOUT_PUSH`，随后无条件关闭并移除这些连接。通知、队列或socket失败 MUST NOT回滚epoch、恢复token/ticket、保留旧连接或阻塞到无界；相同invalidation重试 MUST保持幂等，且不得关闭epoch不小于新值的后续连接。

#### Scenario: Logout 后通知成功
- **WHEN**HTTPS logout已原子递增session epoch且旧WSS连接仍可写
- **THEN**旧连接收到包含新epoch的session-invalidated push后关闭，旧ticket/token和旧连接均不能继续授权

#### Scenario: Invalidation 时队列已满
- **WHEN**权威epoch已提交但旧连接的writer queue已满或write deadline到期
- **THEN**invalidator跳过伪成功通知并强制关闭连接，返回可诊断结果但不请求SessionStore补偿

#### Scenario: 重放相同 invalidation
- **WHEN**调用方以同一SessionID、新epoch与reason重试连接失效通知
- **THEN**已经移除的旧连接视为完成，仍残留的旧epoch连接再次关闭，新epoch连接保持不变

### Requirement: WSS 必须服从共享 readiness、启动回滚和关闭顺序
WSS codec、catalog、registry、配置和handler MUST在公开listener产生副作用前构造与验证。WSS upgrade MUST共享进程readiness；starting、draining或stopped状态不得创建新连接。进入draining后，public runtime MUST先拒绝upgrade，再停止publisher并在总shutdown deadline内关闭/等待全部hijacked WSS connection，然后关闭普通HTTP in-flight request，最后才允许Redis、MySQL与diagnostic逆序释放。系统性受监督任务失败 MUST撤销ready并触发非零受控关闭；单连接协议或网络错误只关闭对应连接。

#### Scenario: Listener bind 失败
- **WHEN**WSS handler与registry已构造，但共享public listener因端口冲突无法bind
- **THEN**启动失败并同步释放WSS资源、Redis、MySQL和diagnostic，不留下connection task或第二listener

#### Scenario: 进程带 active WSS connection 关闭
- **WHEN**进程从ready进入draining且存在HTTP请求与已认证WSS连接
- **THEN**新upgrade先被拒绝，WSS连接在子deadline内收到going-away或被强制关闭，随后HTTP和storage按顺序释放

#### Scenario: 单连接 reader 返回畸形输入
- **WHEN**一个客户端发送非法frame或意外application data
- **THEN**只关闭并清理该连接；其他连接和进程readiness不受影响，除非registry/task owner报告系统性不变量破坏

### Requirement: WSS 可观测必须低敏、低基数且覆盖资源结果
WSS metrics MUST覆盖handshake outcome、active/accepted/rejected connections、push outcome与bytes、queue budget、heartbeat、slow consumer、close reason和invalidation结果，并且label MUST限制为operation、message ID、status class与稳定reason。结构化日志 MAY包含随机ConnectionID、message ID与SessionID/PlayerID摘要，但 MUST NOT记录Authorization、ticket nonce/digest、完整principal、IP、Origin、payload、invite/admission、backend error文本或高基数metrics label。Panic与dependency error MUST映射为安全响应/close和稳定failure kind。

#### Scenario: 握手认证失败
- **WHEN**ticket不存在、过期、重放、epoch陈旧或Redis返回dependency error
- **THEN**客户端只观察稳定有界失败，日志与metrics不能区分或泄漏私有ticket binding、raw remote identity或backend文本

#### Scenario: Push 编码失败
- **WHEN**trusted caller提交message ID与payload type不匹配的通知
- **THEN**publisher返回稳定codec failure，观测只记录message ID和failure kind且不会格式化payload

### Requirement: WSS capability 必须由真实 TLS、Redis和并发边界分层验收
实现 MUST包含无listener的codec/registry/writer/invalidator单元与fuzz测试、registry/golden contract tests、race测试，以及使用临时TLS、真实public router与production Redis SessionStore的WSS integration tests。验收 MUST覆盖成功握手与9类push、wrong route/direction/type、ticket replay/expiry/epoch/endpoint、Redis failure、Host/Origin/subprotocol、oversize、slow consumer、heartbeat、连接风暴、invalidation、启动回滚与graceful shutdown。本 capability通过 MUST只表示WSS control连接和通知边界可用，不得表示TLS/TCP gameplay、world admission consume、safe-return或完整own/visit-world竖切已经完成。

#### Scenario: 执行 WSS storage integration harness
- **WHEN**开发者在隔离Redis与临时TLS环境运行统一WSS验收
- **THEN**握手通过真实HTTPS ticket签发和production SessionStore原子consume，所有资源在测试后有界清理且Composition Root不注入memory/fake store

#### Scenario: 只完成本 change
- **WHEN**客户端能建立WSS并收到invite、assignment或session invalidation通知，但TLS/TCP change尚未完成
- **THEN**项目只声明control-plane capability通过，不把通知投递当作world mutation、admission消费或服务端v1资格证据
