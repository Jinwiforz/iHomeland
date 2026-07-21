# Server TCP Gameplay Specification

## Purpose

规定独立 TLS/TCP gameplay 通道的认证、准入、framing、路由、连接资源、投递、失效、关闭与分层验收边界。

## Requirements

### Requirement: Gameplay TCP 必须使用独立安全 listener 与冻结认证 preface
服务端 MUST 在独立 TCP listener 上提供 gameplay channel，不得与公开 HTTPS/WSS 或 diagnostic listener复用端口、router或协议探测。Production MUST只接受TLS 1.3；local/test明文MUST同时要求bind地址与remote地址均为loopback。TLS建立后第一帧 MUST是版本化authentication preface，使用4-byte unsigned big-endian长度前缀，包含固定magic/version、封闭purpose byte、ticket/admission长度与两段安全ASCII opaque credential；purpose只选择verifier消费指纹，最终授权仍以verifier返回binding为准。其格式、最大长度、读取deadline与fixture MUST冻结。完成认证前 MUST NOT解析 `ReliableEnvelope`、创建业务dispatcher任务或向peer回显私有失败原因。

#### Scenario: Production 客户端使用明文连接
- **WHEN** 非local/test配置下的client直接向gameplay端口发送明文preface或TLS版本低于1.3
- **THEN** listener在消费ticket/admission和创建connection entry前有界关闭连接，diagnostic与HTTP/WSS行为不受影响

#### Scenario: Authentication preface 被拆分读取
- **WHEN** 合法preface的长度前缀、固定header和credential bytes经多个socket read到达
- **THEN** framer在handshake deadline与buffer预算内准确重组一次preface，不把read边界当作frame边界

#### Scenario: 认证前发送业务 envelope
- **WHEN** peer把 `ReliableEnvelope` 或未知magic作为第一帧发送
- **THEN** transport以稳定protocol failure关闭连接，不查询application service且不尝试把payload当作ticket或admission

### Requirement: TCP 认证必须依次原子消费 ticket 与 world admission
Listener MUST先取得全局/remote reservation并验证preface语法，再以受信advertised endpoint、`ChannelTLSTCP` 和精确 `GAMEPLAY` scope原子消费ConnectionTicket，构造只读AuthContext并绑定SessionID/PlayerID预算。随后 MUST以服务端CSPRNG ConnectionID派生稳定consume identity，调用现有WorldAdmission verifier原子消费credential并复核current full AssignmentStamp。任一步失败 MUST释放全部未提交资源并关闭连接；已经提交的ticket/admission MUST NOT补偿、恢复或自动重放。Connection entry MUST只保存AuthContext与Qualification安全投影，不得保存raw credential、digest、完整principal或可变授权map。

#### Scenario: 同一 gameplay ticket 并发握手
- **WHEN** 两个TLS连接以相同未消费ticket和合法admission并发提交preface
- **THEN**至多一个连接取得GAMEPLAY AuthContext并继续admission验证，另一个稳定失败且不能区分私有ticket binding

#### Scenario: Ticket 合法但 admission stale
- **WHEN** ticket成功消费后admission绑定的assignment已被更高generation/fence替换
- **THEN** verifier烧毁旧admission并拒绝connection registration，ticket保持已消费且client必须经HTTPS重新取得资格

#### Scenario: Admission 复核依赖暂时不可用
- **WHEN** credential原子消费已提交但placement无法证明current full assignment
- **THEN**连接fail closed且不进入dispatcher；只有相同consume identity在credential业务expiry前才可解析已定义的response-loss重试，其他identity仍为replay

### Requirement: Admission 必须定义连接 target 状态且不能被 payload 替换
`OWN_WORLD` Qualification MUST只把连接绑定到actor自己的current PersonalWorld并直接进入active target。`JOIN`/`RECONNECT` Qualification MUST把连接置于有deadline的pending membership状态，且在成功执行匹配的首个VisitJoin/VisitReconnect command前不得读取snapshot或执行其他mutation。该command携带的credential MUST以同一consume identity调用verifier恢复Qualification，并与preface绑定逐字段相等；成功application mutation后才能原子转为active。Payload中的world、instance、role、session、actor、endpoint或另一credential MUST NOT替换connection binding。

#### Scenario: Owner admission 后读取 world snapshot
- **WHEN**有效Owner以OWN_WORLD admission完成preface并发送message 2000 `WORLD_SNAPSHOT_REQUEST`
- **THEN**dispatcher只从AuthContext与bound Qualification取得actor/target，并返回当前PersonalWorld的correlated client-safe snapshot

#### Scenario: JOIN connection 在提交 join 前请求 snapshot
- **WHEN**Visitor以JOIN admission完成preface但尚未成功执行匹配的VisitJoin command
- **THEN**dispatcher拒绝world/visit snapshot和其他command，只允许deadline内同一binding的首个join尝试

#### Scenario: Join command 更换 admission credential
- **WHEN**pending JOIN connection在message 2109中提交另一张credential、不同target/purpose或不同consume identity
- **THEN**verifier/dispatcher在VisitSession mutation前拒绝，preface binding不改变且新credential不能接管连接

#### Scenario: Reconnect command 精确恢复消费结果
- **WHEN**preface已以RECONNECT credential提交消费，随后message 2115使用同一credential和connection consume identity
- **THEN**verifier恢复同一Qualification并完成VisitReconnect二次校验，成功后连接恰好一次转为active

### Requirement: TCP framer 与 codec 必须有界且只接受 registry 允许的 envelope
认证后每个frame MUST使用4-byte unsigned big-endian长度前缀；framer MUST拒绝零长度、超过1 MiB、截断、声明长度超过连接内存预算和shutdown后的残帧，并正确处理半包、粘包和单次读取多个frame。Codec MUST在解码payload前验证protocol version、message ID、kind、direction、唯一 `TLS_TCP` channel、`GAMEPLAY` scope、route max size、request/command identifier、sequence、timestamp与精确generated payload type。Client MUST只能发送registry登记的TLS/TCP REQUEST/COMMAND；WSS message、S2C RESPONSE/PUSH/ERROR、unknown enum/ID、错误correlation或type MUST在调用application前拒绝。

#### Scenario: 一个 read 包含两个完整 frame
- **WHEN**socket read同时返回两个合法length-prefixed envelope
- **THEN**framer按顺序产出两个独立frame，并受每批dispatch数量与连接总内存预算约束

#### Scenario: Client 在 TCP 发送 WSS invite push
- **WHEN**peer发送message 2100、错误direction/kind或伪造的server response
- **THEN**codec按wrong-channel/direction拒绝并关闭对应连接，不把payload路由到VisitSession或control owner

#### Scenario: Envelope 在 payload 后超过 route 预算
- **WHEN**generated payload类型合法但完整encoded envelope超过route maxSize或全局frame limit
- **THEN**codec拒绝该frame，不拆分、压缩、截断或绕过registry budget

### Requirement: Dispatcher 必须执行 route、correlation、rate、deadline 与幂等边界
Dispatcher MUST为每个已登记C2S message配置唯一typed handler，并在application调用前验证connection state、AuthContext/Qualification、route rate policy、operation deadline与request/command identity。REQUEST MUST使用16-byte request ID并产生匹配correlation的response/error；COMMAND MUST使用16-byte command ID，由唯一reader同步串行执行并把identity交给现有application/store幂等边界。Transport MUST NOT维护第二份application pending fingerprint、缓存或伪造已提交业务结果。系统disconnect、grace expiry、session expiry、assignment invalidation或dependency-loss MUST NOT存在C2S route。

#### Scenario: 同一 command ID 顺序改变 payload
- **WHEN**同一connection先后提交相同command ID但operation、target或expected revision不同
- **THEN**唯一reader按wire顺序调用application，store返回稳定idempotency conflict且transport不维护或覆盖首次结果

#### Scenario: Request 超过 route rate policy
- **WHEN**已认证active connection持续发送world/visit snapshot request超过registry登记速率
- **THEN**dispatcher在application前有界拒绝并记录稳定rate outcome；持续abuse触发连接关闭且不创建无界timer/state

#### Scenario: Client 伪造 lifecycle command
- **WHEN**client尝试以unknown message或现有payload模拟Owner disconnect、grace expiry、assignment invalidation或safe-return producer
- **THEN**registry/dispatcher fail closed，只有受信server-side owner可以调用对应application/system port

### Requirement: Connection registry 与 I/O 任务必须具有统一硬预算
TCP owner MUST以CSPRNG ConnectionID索引连接，并维护SessionID、PlayerID、PersonalWorldID与可选VisitSessionID反向索引。Entry MUST只拥有连接引用、认证/target摘要、queue、route rate state与lifecycle状态，不得保存aggregate snapshot、application pending cache、storage adapter或业务最终事实。每连接 MUST只有一个同步dispatch的reader和一个serialized writer；全局/remote/session/player/target连接数、remote rate state、scratch/partial-frame buffer、每批frames、writer item/bytes、read/write/handshake/idle/close deadline、OS TCP keepalive参数与总goroutine等待 MUST具有启动时验证的硬上限。Listener MUST NOT发送未登记application heartbeat frame。注册、pending-to-active、移除、投递与关闭 MUST线性化并恰好一次清理全部索引和内存。

#### Scenario: Connection storm 达到全局预算
- **WHEN**合法或非法TCP/TLS握手占满全局/remote reservation
- **THEN**新连接在读取credential或创建per-connection goroutine/大buffer前被有界拒绝，已有HTTP/WSS/TCP连接继续受自身预算保护

#### Scenario: Slow consumer 填满 writer queue
- **WHEN**client停止读取使queue item或encoded byte预算达到上限
- **THEN**publisher/dispatcher得到有界slow-consumer结果，连接被关闭并释放queue，不静默丢弃一条消息后继续报告健康

#### Scenario: Reader 提交超大声明长度
- **WHEN**peer只发送一个超过frame或connection budget的4-byte长度而不发送body
- **THEN**reader不按声明值分配内存，立即以稳定oversize reason关闭并清理reservation/index

### Requirement: Response 与可信 push 必须串行投递并服从 target binding
Application result MUST先转换为已登记generated response/error，再由connection writer统一分配从1开始的S2C sequence、server timestamp与deterministic encoding；handler/publisher MUST NOT直接并发写socket。Trusted publisher MUST且只能投递registry声明为TLS/TCP、GAMEPLAY、S2C PUSH的2002、2121、2122，并按ConnectionID、PlayerID、PersonalWorldID或VisitSessionID的受信索引选择目标；payload target MUST不能扩大投递集合。`VISIT_SAFE_RETURN_PUSH` MUST只投递给directive目标Visitor当前bound gameplay connection，并使旧target停止接受后续mutation；WSS close notice不得替代它。

#### Scenario: 投递 Visit safe-return
- **WHEN**受信Visit owner产生message 2122与目标Visitor的确定性directive
- **THEN**只有匹配VisitSession/Visitor binding的active gameplay connection按sequence收到push并进入returning/closing状态，其他Owner/Visitor连接不受影响

#### Scenario: Publisher 提交 wrong-channel payload
- **WHEN**TCP publisher尝试发送WSS message 2102、C2S command或payload type不匹配的push
- **THEN**codec在入队前拒绝并返回稳定failure，不能改写message ID、广播到全Player或格式化payload

#### Scenario: Response 与 push 并发产生
- **WHEN**一个application response和一个可信push同时进入同一connection writer
- **THEN**serialized writer为两者分配唯一单调S2C sequence并逐frame发送，不交错字节、不重复correlation且不并发写socket

### Requirement: TCP 必须参与 session invalidation、readiness 与有界关闭
Composition Root MUST以单一组合 `ConnectionInvalidator` 在共享deadline内并行尝试WSS与TCP旧epoch连接失效；任一adapter失败 MUST NOT跳过另一个、回滚epoch或恢复credential。TCP registry MUST在invalidation时先停止目标旧连接的新投递，再无条件关闭并移除。Starting、draining、stopped状态 MUST拒绝新accept/handshake。进入draining后 MUST停止TCP accept与HTTP/WSS握手、停止可信publisher、在总shutdown budget内并行关闭/等待TCP与WSS连接，再关闭普通HTTP in-flight，最后释放Redis、MySQL与diagnostic。Listener/TLS/catalog/dispatcher构造或受监督accept loop失败 MUST撤销readiness并完整回滚；单连接错误只关闭该连接。

#### Scenario: Logout 同时存在 WSS 与 TCP 连接
- **WHEN**HTTPS logout已提交新session epoch且该session仍有control与gameplay连接
- **THEN**组合invalidator在同一有界调用中尝试两类registry，旧TCP无条件关闭且WSS失败不能使其保持授权

#### Scenario: Gameplay listener bind 失败
- **WHEN**storage、HTTP/WSS与TCP组件已构造但gameplay端口冲突无法bind
- **THEN**启动失败并按owner顺序释放TCP/WSS/HTTP/storage/diagnostic，不留下accept loop、connection task或ready状态

#### Scenario: 进程带 active gameplay connections 关闭
- **WHEN**进程从ready进入draining且存在active/pending TCP与WSS连接
- **THEN**所有新握手先被拒绝，两类realtime连接在共享deadline内并行优雅或强制关闭，随后HTTP与storage按顺序释放

### Requirement: TCP 观测与验收必须低敏、分层且不夸大完成范围
Metrics MUST覆盖TLS/handshake、active/rejected、frame/message bytes、dispatch latency/result、in-flight、rate、queue、keepalive/idle、slow consumer、push、close、invalidation与shutdown，并且label MUST只使用稳定operation、message ID、status class与reason。日志 MAY包含随机ConnectionID和受控SessionID/PlayerID摘要，但 MUST NOT记录raw ticket/admission、digest、IP、payload、完整principal、full AssignmentStamp、invite、backend error文本或高基数target。实现 MUST包含framer/codec/registry/dispatcher/publisher/invalidation单元、table、fuzz与race测试，以及使用临时TLS、真实Session/WorldAdmission Redis adapter、production Composition Root和Go wire client的integration tests。通过本capability MUST只表示TLS/TCP认证、admission、dispatcher和连接资源边界可用，不得表示完整own-world/visit-world编排、全部业务producer、Go协议客户端资格或server v1已经完成。

#### Scenario: 执行 TCP storage integration harness
- **WHEN**开发者在隔离MySQL/Redis与临时TLS环境运行统一gameplay验收
- **THEN**harness以真实wire覆盖OWN_WORLD/JOIN/RECONNECT、ticket replay、logout invalidation与shutdown；stale、wrong endpoint、framing和backpressure由对应storage/transport分层测试覆盖，全部测试有界清理且production graph不注入fake store

#### Scenario: 记录认证失败
- **WHEN**ticket/admission missing、expired、replayed、stale、corrupt或依赖不可用
- **THEN**peer只观察稳定有界关闭，日志与metrics不能区分或泄漏私有binding、raw remote identity、credential内容或backend文本

#### Scenario: 只完成本 change
- **WHEN**wire test能够建立TLS/TCP、绑定admission并调用已接线窄handler，但跨通道业务producer、Owner lifecycle编排和端到端协议客户端尚未完成
- **THEN**项目只声明gameplay transport capability通过，`complete-server-personal-world-slice`与`qualify-server-v1`仍保持未完成

### Requirement: Server gameplay heartbeat 必须由 transport owner 有界处理

服务端 MUST 只允许已认证且处于 active target 的 gameplay connection 提交 heartbeat request。Dispatcher MUST 验证 route、direction、kind、sequence、request ID、rate policy 与 deadline，再直接产生空 heartbeat response；MUST NOT 调用 PersonalWorld 或 VisitSession application mutation。每个成功解码的合法 heartbeat MUST 与其他合法 C2S envelope 一样刷新 connection read deadline，非法、超速或 pending-target heartbeat MUST fail closed。

#### Scenario: Owner 静默超过原业务空闲窗口

- **WHEN** Owner connection 没有业务帧但持续在约定 interval 内完成合法 heartbeat request/response
- **THEN** server connection 保持 active，`idle_timeout` 不增加，world/visit revision 与 application dispatch 结果不变

#### Scenario: Pending Visitor 在首个 JOIN 前发送 heartbeat

- **WHEN** JOIN 或 RECONNECT admission connection 尚未完成规定的首个 activation command 就发送 heartbeat
- **THEN** server 按 target state policy 拒绝并关闭该 pending connection，heartbeat 不能绕过首帧 admission 约束

#### Scenario: Heartbeat 超过 rate policy

- **WHEN** active connection 以高于 registry policy 的速率持续发送 heartbeat
- **THEN** dispatcher 在业务 application 前产生稳定 rate outcome，并按 abuse policy 有界拒绝或关闭，不创建无界 timer 或 per-request 长期状态
