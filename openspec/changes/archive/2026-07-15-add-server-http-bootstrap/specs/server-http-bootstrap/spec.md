## ADDED Requirements

### Requirement: 公开 HTTP 业务面必须独立、受控且默认安全
服务端 MUST 使用独立于 diagnostic listener 的公开 HTTP component承载已登记业务 operation。Production环境 MUST仅允许TLS 1.3并从SecretProvider取得private key；local/test明文 MUST仅在显式开发模式且bind地址为loopback时允许。公开listener的bind地址、TLS material、server timeout、header上限与advertised endpoint MUST在任何listener或storage client产生副作用前完成交叉验证；端口冲突或TLS错误 MUST使启动失败并逆序回滚。

#### Scenario: Production 尝试明文启动
- **WHEN** production配置禁用公开TLS、缺少证书/private-key secret或允许loopback开发例外
- **THEN** 配置在创建listener和storage client前失败，进程保持非ready且不开放业务route

#### Scenario: 公开端口被占用
- **WHEN** storage已经启动但public listener无法绑定已配置地址
- **THEN** lifecycle报告public HTTP component启动失败，逆序释放Redis、MySQL和diagnostic并以非零结果退出

#### Scenario: 请求诊断 listener 上的业务路径
- **WHEN** 客户端向diagnostic地址请求 `/v1/auth/login` 或任一world route
- **THEN** diagnostic listener返回稳定非成功响应且不会调用HTTP application service

### Requirement: HTTP router 必须精确实现冻结 OpenAPI operation
公开router MUST且只能实现 `getVersion`、`getBootstrapConfig`、`registerAccount`、`loginAccount`、`refreshSession`、`logoutSession`、`issueConnectionTicket`、`getWorldBootstrap`、`acceptVisitInvite` 与 `issueWorldAdmission` 对应的现有method/path、认证、status和schema。Runtime operation table的body limit、timeout与idempotency mode MUST与 `openapi.yaml` 精确一致；公开response/error字段、enum和Unix毫秒投影 MUST由集中versioned codec映射，handler MUST NOT定义同义DTO、错误或时间单位。

#### Scenario: OpenAPI metadata 与 runtime 漂移
- **WHEN** route的method/path/operationId/body limit/timeout/idempotency或认证要求与OpenAPI不一致，或router多注册未声明业务route
- **THEN** contract test失败且change不能归档

#### Scenario: 编码 world bootstrap
- **WHEN** application返回PersonalWorld和包含内部node/fence的current assignment
- **THEN** response只包含OpenAPI允许的world和client-safe assignment字段，时间向下转换为Unix毫秒且不泄漏完整AssignmentStamp

#### Scenario: 未知业务路径
- **WHEN** 客户端请求未登记HTTP path或错误method
- **THEN** router返回有界稳定非成功响应，不猜测相近route且不调用任何application service

### Requirement: 每个 HTTP 请求必须执行有界解析、deadline 与稳定 correlation
HTTP adapter MUST按operation metadata限制header/body并设置request-scoped deadline。有body的operation MUST只接受受支持JSON media type、单一完整JSON value、closed object与全部必填字段；无body的operation MUST拒绝非空body。Unknown field、trailing value、非法path/header/enum/identity、超限输入、客户端取消和timeout MUST在不泄漏输入的前提下稳定处理。每个响应 MUST携带一个有界安全request ID用于 `ErrorResponse.requestId` 和内部correlation，客户端不得用它覆盖认证或幂等identity。

#### Scenario: 请求体包含 actor 字段
- **WHEN** invite accept或admission body额外提交PlayerID、SessionID、role、world、endpoint或其他OpenAPI未声明字段
- **THEN** adapter在调用application前返回 `VALIDATION_FAILED`，不把该字段保存、记录或用于授权

#### Scenario: 请求体超过 operation 上限
- **WHEN** 客户端以chunked或Content-Length请求发送超过该operation body limit的内容
- **THEN** adapter有界停止解析并返回稳定非成功响应，不读取无界内容、不调用application且连接处理遵守server资源策略

#### Scenario: Application 超过 operation deadline
- **WHEN** storage、Argon2或application调用在OpenAPI timeout内未完成
- **THEN** request context被取消并返回安全dependency/timeout结果；mutation提交状态未知时不得声称未提交或自动换新幂等identity重试

### Requirement: 认证与 session operation 必须只消费 Session owner 的权威事实
Bearer认证 MUST调用SessionStore原子解析access token并取得不可伪造的HTTPS AuthContext及session绝对deadline；token payload、账号字段或请求参数 MUST不能构造身份。Register/login MUST调用Account service并使用production MySQL repository、Argon2id hasher与Session issuer；refresh/logout/ticket MUST直接调用Session service。Connection ticket MUST只接受客户端选择WSS或TLS_TCP channel，由受信EndpointProvider决定endpoint和固定scope。Logout MUST先提交epoch失效，再通知当前Composition Root中明确的connection invalidator。

#### Scenario: Access 与 session 记录矛盾
- **WHEN** Redis access记录缺失、过期、epoch陈旧或其expiry/session deadline与session record不一致
- **THEN**认证fail closed并返回稳定unauthenticated或dependency error，不从token格式恢复AuthContext

#### Scenario: 客户端选择 ticket endpoint 或 scope
- **WHEN** ticket请求除channel外提交host、port、scope或actor字段
- **THEN** closed schema拒绝请求；成功ticket的endpoint和scope只来自受信配置与Session policy

#### Scenario: 当前尚无 realtime listener 时登出
- **WHEN**本change的Composition Root没有WSS/TLS-TCP component且有效HTTPS AuthContext调用logout
- **THEN**SessionStore仍原子递增epoch并撤销旧token/ticket，显式no-active-connections invalidator完成空集合通知且不伪造connection状态

### Requirement: World bootstrap 与准入签发必须由窄应用编排派生权威事实
Transport-independent world-entry application MUST拥有 `BootstrapOwnWorld`、`AcceptVisitInvite` 与 `IssueWorldAdmission` 三个公开编排用例。Bootstrap MUST确保认证Player唯一primary PersonalWorld存在并只读取current placement；没有active assignment时 MUST返回world且省略assignment。Invite accept MUST由VisitSession owner验证目标actor、invite、revision、capacity、owner availability和current assignment，并把reservation deadline限制为session、invite、VisitSession、assignment与配置上限的最早值。Admission issuance MUST从认证session、own-world或current Visitor membership、完整current AssignmentStamp及受信TLS_TCP endpoint派生既有WorldAdmission binding；payload MUST只能选择own-world或VisitSessionID。

#### Scenario: 首次查询 own-world bootstrap
- **WHEN**有效HTTPS actor尚无primary PersonalWorld且请求bootstrap
- **THEN**application幂等创建唯一primary world并返回该actor的安全投影，不接受客户端指定owner/world且不启动WorldInstance

#### Scenario: Bootstrap 暂无 active assignment
- **WHEN**primary PersonalWorld存在但placement没有可证明的current active assignment
- **THEN**bootstrap返回world并省略可选assignment；同一状态下请求own-world admission返回 `WORLD_NOT_READY`且不签发credential

#### Scenario: 接受临近到期 invite
- **WHEN**目标Visitor以正确expected revision接受pending invite且invite、session或assignment的剩余寿命短于默认reservation lifetime
- **THEN**VisitSession owner使用所有权威deadline的最早值创建一次reservation，不由handler自行延长或猜测expiry

#### Scenario: Visitor 请求 world admission
- **WHEN**bearer actor在指定VisitSession中具有current `reserved` 或 `reconnecting` membership
- **THEN**application分别派生 `VISITOR/JOIN` 或 `VISITOR/RECONNECT` binding，并把expiry限制到session、membership、assignment lease和admission policy最早deadline

#### Scenario: Visitor 缺少 membership
- **WHEN**actor只有invite、已过期reservation或不属于指定VisitSession
- **THEN**operation返回既有 `VISIT_MEMBERSHIP_REQUIRED` 或更具体稳定错误，不把invite、bearer或GAMEPLAY scope提升为world admission

### Requirement: HTTP 幂等键必须分域、脱敏并复用既有原子决议
`acceptVisitInvite` 与 `issueWorldAdmission` MUST要求符合OpenAPI的 `Idempotency-Key`。应用层 MUST以operation、HTTPS AuthContext lineage和原始key确定性派生符合目标owner grammar的内部CommandID/IssueID；原始key MUST NOT进入Redis key、日志、metrics或公开错误。相同actor/operation/key和相同语义重试 MUST返回首次结果；改变target、revision、purpose、assignment、endpoint或权威deadline MUST返回既有稳定idempotency conflict。服务端重试时钟推进 MUST NOT被误判为客户端语义变化，也 MUST NOT延长首次reservation/admission expiry。Adapter MUST NOT建立第二套幂等表或在commit-unknown后换新identity自动重试。

#### Scenario: Accept response 丢失后重试
- **WHEN**首次accept已提交但response丢失，客户端以同一session lineage、path、body和Idempotency-Key重试
- **THEN**派生相同VisitSession CommandID并重放首次reservation/revision，不再次占用capacity

#### Scenario: 相同 key 跨 operation 使用
- **WHEN**同一actor把相同Idempotency-Key分别用于accept和admission issue
- **THEN**domain separation产生不同内部identity，两个owner不共享或覆盖幂等状态

#### Scenario: Admission target 改变
- **WHEN**相同actor/session/key把own-world改为另一个VisitSession，或current权威binding与首次语义不同
- **THEN**WorldAdmissionStore按相同IssueID与不同fingerprint返回 `WORLD_IDEMPOTENCY_CONFLICT`且不签发第二个credential

### Requirement: 公开 HTTP 必须具备有界限流、脱敏可观测与受控关闭
每个operation MUST解析为启动时验证的有界rate/burst policy。匿名请求 MUST至少按规范化remote IP限流，认证请求 MUST再按SessionID/epoch限流；limiter条目数量和idle lifetime MUST有硬上限且不能写入业务Redis。公开日志和metrics MUST只使用operationId、status class、stable outcome和其他低基数字段，不得记录URL identity、IP、principal、password、Authorization、token/ticket/admission、Idempotency-Key、full assignment或backend错误。Public middleware MUST共享进程readiness：listener已bind但尚未ready或已经draining/stopped时拒绝新业务调用；进入draining后public listener MUST在共享deadline内等待in-flight请求，然后storage才可关闭。

#### Scenario: Login 请求超过预算
- **WHEN**同一remote identity在配置窗口内超过login rate/burst
- **THEN**adapter返回 `RATE_LIMITED` 与有界retry提示，不执行Argon2或repository调用且不产生username枚举差异

#### Scenario: 敏感请求发生内部错误
- **WHEN**register/login/refresh/admission处理因panic或dependency error失败
- **THEN**response、结构化日志和metrics均不包含请求body、credential、identity或内部错误文本，只保留request ID、operationId和稳定failure kind

#### Scenario: 进程开始关闭
- **WHEN**服务端从ready进入draining且存在进行中的HTTP请求
- **THEN**public component先停止接受新连接并在总shutdown deadline内等待或取消请求，随后Redis、MySQL和diagnostic按逆序释放

#### Scenario: Listener 已绑定但进程尚未 ready
- **WHEN**public component已经Start成功但Composition Root尚未完成最终ready迁移
- **THEN**readiness gate返回有界dependency-unavailable且不调用application，MarkReady后新请求才可进入handler

### Requirement: HTTP capability 必须由真实 TLS 与 production storage 分层验收
实现 MUST包含无listener的handler/middleware/application单元测试、race测试、OpenAPI/fixture contract tests，以及使用隔离MySQL/Redis、真实production adapters与临时TLS证书的integration tests。`shared/contracts/fixtures/http/cases.json` 的每个case MUST命中真实router/codec/error mapper；测试 MUST覆盖register/login/refresh/logout/ticket/bootstrap/accept/admission成功和负向边界、幂等重放/冲突、dependency failure、Redis flush/corruption、启动回滚和graceful shutdown。本 capability通过 MUST只表示HTTPS API和签发边界可用，不得表示WSS/TLS-TCP connect、ticket/admission consume或完整own/visit-world竖切已经验收。

#### Scenario: 执行 HTTP storage integration harness
- **WHEN**开发者在隔离存储与临时TLS环境运行统一HTTP验收
- **THEN**全部公开operation通过真实service graph访问production adapters，测试结束后资源有界清理且没有memory/fake adapter进入Composition Root

#### Scenario: 只完成本 change
- **WHEN**HTTPS测试能够签发connection ticket和world admission但后续WSS/TLS-TCP change尚未完成
- **THEN**项目只声明HTTP bootstrap capability通过，advertised realtime endpoint不被当作connectivity或服务端v1资格验收证据
