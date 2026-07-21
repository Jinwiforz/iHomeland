# Client TCP Gameplay 规格

## Purpose

定义 Unity 客户端 TLS/TCP gameplay 双凭据连接、framing、typed route、pending correlation、可信 PUSH、背压与 App Scope 生命周期行为。

## Requirements

### Requirement: Gameplay 连接必须严格消费双凭据与 endpoint policy

客户端 MUST 只在 App Scope Running 后由显式调用建立 gameplay 连接。每次连接 MUST 从统一 Session owner 分别单次取得当前 generation 的 `TLS_TCP/GAMEPLAY` ticket 与 world admission，验证二者 endpoint 精确一致，并发送 `IHTP` v1 preface。Production MUST 使用 TLS 1.3 与平台默认主机/证书验证；显式 local/test plaintext MUST 仅允许 loopback。任一 connect、TLS 或 preface 失败后 MUST NOT 复用或自动重发任一 credential。

#### Scenario: 建立 own-world 连接

- **WHEN** current session 提供同 endpoint、未过期且未交付的 GAMEPLAY ticket 与 OWN_WORLD admission
- **THEN** 客户端先建立合规 transport，再发送与 fixture 相同布局的有界 preface，且不记录 credential

#### Scenario: 双凭据 endpoint 不一致

- **WHEN** ticket 与 admission 声明不同 host、port 或 channel
- **THEN** 客户端在打开 socket 前拒绝连接并使已取得 credential 不可再次使用

#### Scenario: Production 使用明文 TCP

- **WHEN** Production 配置或非 loopback local/test endpoint 请求 plaintext
- **THEN** transport 在连接前 fail closed，且不存在跳过证书验证的配置入口

### Requirement: Gameplay framing 与 envelope 必须精确且有界

客户端 MUST 使用 4-byte unsigned big-endian 长度读取和写入 preface 与每个 `ReliableEnvelope`，MUST 精确读取完整 frame，且 MUST 在按声明长度分配前验证 1 MiB 全局上限与 route payload 上限。Envelope MUST 使用 protocol version 1、16-byte request/command ID、严格递增的 C2S sequence 和合法 direction/kind；客户端 MUST 拒绝未知、wrong-channel、wrong-direction、wrong-kind、payload type mismatch、过大或 malformed frame。

#### Scenario: Socket 分段返回 frame

- **WHEN** prefix 与 body 被拆成任意多个 partial reads
- **THEN** framer 只在精确收齐完整 body 后交给 codec，不把一次 read 当作一个 frame

#### Scenario: Peer 声明超大 frame

- **WHEN** S2C prefix 声明长度超过全局上限而未发送 body
- **THEN** reader 不按该长度分配，立即以 protocol close reason 结束连接

#### Scenario: 收到未登记 push

- **WHEN** active connection 收到不是 2002、2121 或 2122 的 S2C PUSH
- **THEN** catalog 在解析业务 payload 前拒绝并关闭连接

### Requirement: Gameplay I/O 与 pending operation 必须单 owner 且有界

每个连接 MUST 只有一个 receive pump 和一个 serialized writer。所有 request/command MUST 通过冻结 descriptor 创建，使用 CSPRNG 16-byte ID，并在发送前登记有界 pending entry；response/error MUST 精确匹配 ID、expected message ID 与 kind，且每个 pending MUST 在 response、error、caller cancel、deadline、disconnect 或 shutdown 时恰好完成一次。Writer queue MUST 同时限制 item 数与 encoded bytes；达到 pending 或 queue 上限 MUST 有界拒绝或以 backpressure reason 关闭，MUST NOT 静默丢弃。

#### Scenario: Response 与 push 并发到达

- **WHEN** reader 连续收到一个匹配 response 和一个登记 push
- **THEN** pending 只完成一次，push 只投递给其强类型订阅，二者不并发读取 socket

#### Scenario: Caller 取消已发送 command

- **WHEN** command 已写入 transport 后调用方取消等待
- **THEN** 客户端只完成本地等待，并在有界 pending 中保留 correlation 直至迟到 response、断线或 shutdown，以安全消费结果；客户端不假定服务端未提交，也不自动以新 ID 重发

#### Scenario: Writer queue 达到字节上限

- **WHEN** 新 frame 会使 encoded-byte budget 超限，即使 item 数尚未超限
- **THEN** channel 返回稳定 backpressure 失败并按策略关闭连接，不写入部分 frame

### Requirement: Gameplay route 与 push 必须由冻结 typed catalog 约束

客户端 MUST 只发送 route registry 登记为 `TLS_TCP/GAMEPLAY/C2S` 的 request/command，并只接受对应 response/error 与 message 2002、2121、2122 typed push。Public/application API MUST NOT 接受任意 message ID、任意 parser 或未登记 payload。`JOIN` 与 `RECONNECT` connection 在 active 前 MUST 只允许首个对应 command，且该 command MUST 携带与 preface 同一 admission；OWN_WORLD 连接 MAY 直接发送登记 snapshot request。

#### Scenario: 调用方伪造 message ID

- **WHEN** application 尝试发送未登记 ID 或把一个 operation 的 payload 用于另一 operation
- **THEN** 编译期 API 或 codec 在入队前拒绝，不修改 ID 或猜测 payload 类型

#### Scenario: Pending JOIN 发送错误首帧

- **WHEN** JOIN purpose 连接的首个业务 envelope 不是 VisitJoinCommand 或 admission 不一致
- **THEN** channel 在写 socket 前拒绝并关闭该 pending target

#### Scenario: 收到 safe-return push

- **WHEN** active visit connection 收到合法 2122 `VISIT_SAFE_RETURN_PUSH`
- **THEN** channel 在投递回调前停止接受旧 target mutation，并以受控 application-return 语义进入关闭流程

### Requirement: Gameplay 生命周期必须与 session 和 App Scope 对齐

Gameplay channel MUST 在 App Scope 初始化时只验证配置与依赖而不联网；显式 connect、active、closing、stopped 状态转换 MUST 线性化。Session epoch 失效 MUST 关闭 current WSS 与 TCP；TCP disconnect MUST NOT 清除或伪造 session。App shutdown MUST 先停止新发送、取消 pending、关闭 transport、等待有界 I/O tasks，再释放 HTTP 与 runtime dependencies。Close reason MUST 使用稳定低基数枚举且 MUST NOT 包含 endpoint、credential、payload 或 backend 文本。

#### Scenario: 空 BootstrapScene 启动

- **WHEN** App Scope 初始化完成但没有 application flow 请求 gameplay connect
- **THEN** 客户端不解析 admission、不创建 socket 或后台 I/O task，既有空场景仍可运行

#### Scenario: Session forced invalidation

- **WHEN** current WSS control 公布更高权威 epoch
- **THEN** 统一 Session owner 清除旧 lineage，gameplay channel 以 session-invalidated reason 停止发送并关闭 current transport

#### Scenario: App 在 active connection 中关闭

- **WHEN** App lifetime 进入 reverse shutdown 且仍有 active gameplay connection 和 pending request
- **THEN** 新发送立即被拒绝，pending 有界完成，reader/writer 被取消并等待，关闭不会遗留 socket 或未观察 task

### Requirement: Gameplay 验收必须分层且不依赖业务 UI

实现 MUST 用纯 C# tests 覆盖 preface fixture、frame partial read/write、route catalog、sequence/correlation、pending race、queue item/byte backpressure、credential redaction、session invalidation 与 reverse shutdown。Tests MUST 不依赖真实账号、公共 listener、Unity Services、Scene 或 Prefab。Unity 验收 MUST 额外覆盖 EditMode、既有 PlayMode 与 Windows Development build，且默认启动不产生 gameplay 网络副作用。

#### Scenario: 执行 EditMode contract tests

- **WHEN** tests 使用冻结 fixtures 与可控 transport 执行 gameplay capability
- **THEN** wire bytes、typed route、故障语义和生命周期与服务端公开契约一致，且测试可重复、无外部网络依赖

#### Scenario: 执行 Windows Development build

- **WHEN** 构建并启动未触发业务 connect 的客户端
- **THEN** build 包含 runtime/protocol dependencies、可正常启动关闭，Player.log 不包含凭据、未观察异常或 gameplay 自动连接

### Requirement: Client gameplay channel 必须为每个 active generation 运行单一 heartbeat owner

客户端 MUST 在 OWN_WORLD connection 建立 active 状态后，或 JOIN/RECONNECT 首个 command 成功把 pending 提升为 active 后，启动且仅启动一个 heartbeat owner。heartbeat MUST 通过现有 typed operation、pending correlation 与 serialized writer 发送；任一时刻 MUST 至多存在一个 heartbeat pending。close、session invalidation、safe-return、generation replacement 与 App shutdown MUST 撤销并有界等待 heartbeat owner，旧 generation 的 delay、response 或 callback MUST NOT 修改新 generation。

#### Scenario: Own-world connection 没有业务操作

- **WHEN** active own-world generation 在一个 heartbeat interval 内没有其他可证明存活的 gameplay response
- **THEN** heartbeat owner 通过同一 writer 发送登记 request，并在匹配 response 后继续保持 current generation active

#### Scenario: Heartbeat 与显式 close 竞态

- **WHEN** heartbeat 正在 delay、排队、写入或等待 response 时调用 `CloseAsync`
- **THEN** channel 线性化撤销 generation、恰好完成 heartbeat pending、等待全部 I/O owner，并返回 Ready；不得遗留 socket、task、queue item 或 terminal callback

#### Scenario: Heartbeat 发现网络黑洞

- **WHEN** socket 没有立即报错但 heartbeat response 超过 operation deadline
- **THEN** channel 关闭 current generation、发布一次 unexpected disconnect，并使调用方能够立即建立新 generation，而不等待服务端 30 分钟 idle safety deadline

### Requirement: Client gameplay 失败诊断必须保留安全 cause

客户端 MUST 为 connect、preface write、reader、writer、heartbeat、close 与 pending timeout 产生稳定诊断事件。Development sink MUST 记录 component、operation、generation、stage、稳定 failure/close kind 与异常类型；MUST NOT 记录异常 message、endpoint、credential、payload、session/player/world/visit identity。诊断投递失败 MUST NOT 阻塞或改变 channel 状态机。

#### Scenario: Reader 因 socket exception 结束

- **WHEN** active reader 捕获底层 socket/IO 异常
- **THEN** channel 仍向产品层暴露稳定 Transport close reason，同时 Development 日志包含 reader stage 与安全异常类型，可区分 remote EOF、protocol、timeout 和本地 transport failure

#### Scenario: 诊断 sink 不可用

- **WHEN** sink 拒绝、抛错或主线程队列已关闭
- **THEN** connection generation 仍按原 terminal path 完成，诊断不得占用 terminal lifecycle 保留槽或成为重连、shutdown 的依赖
