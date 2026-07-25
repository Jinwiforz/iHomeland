# Server HTTP Bootstrap 规格

## Purpose

定义公开 HTTPS 启动与账号、会话、个人世界入口的唯一 service graph，以及统一的 HTTP 安全边界、生命周期、可观测和分层验收行为。

## Requirements

### Requirement: 公开 HTTP 业务面必须独立、受控且默认安全
服务端 MUST 使用独立于 diagnostic listener 的公开 HTTP component 承载已登记业务 operation。Production 环境 MUST 仅允许 TLS 1.3 并从 SecretProvider 取得 private key；local/test 明文 MUST 仅在显式开发模式且 bind 地址为 loopback 时允许。公开 listener 的 bind 地址、TLS material、server timeout、header 上限与 advertised endpoint MUST 在任何 listener 或 storage client 产生副作用前完成交叉验证；端口冲突或 TLS 错误 MUST 使启动失败并逆序回滚。

#### Scenario: Production 尝试明文启动
- **WHEN** production 配置禁用公开 TLS、缺少证书/private-key secret 或允许 loopback 开发例外
- **THEN** 配置在创建 listener 和 storage client 前失败，进程保持非 ready 且不开放业务 route

#### Scenario: 公开端口被占用
- **WHEN** storage 已经启动但 public listener 无法绑定已配置地址
- **THEN** lifecycle 报告 public HTTP component 启动失败，逆序释放 Redis、MySQL 和 diagnostic 并以非零结果退出

#### Scenario: 请求诊断 listener 上的业务路径
- **WHEN** 客户端向 diagnostic 地址请求 `/v1/auth/login` 或任一 world route
- **THEN** diagnostic listener 返回稳定非成功响应且不会调用 HTTP application service

### Requirement: HTTP router 必须精确实现冻结 OpenAPI operation
公开 router MUST 且只能实现 `getVersion`、`getBootstrapConfig`、`registerAccount`、`loginAccount`、`refreshSession`、`logoutSession`、`issueConnectionTicket`、`getWorldBootstrap`、`acceptVisitInvite`、`issueWorldAdmission` 与 `issueBattleTicket` 对应的现有 method/path、认证、status 和 schema。Runtime operation table 的 body limit、timeout 与 idempotency mode MUST 与 `openapi.yaml` 精确一致；公开 response/error 字段、enum 和 Unix 毫秒投影 MUST 由集中 versioned codec 映射，handler MUST NOT 定义同义 DTO、错误或时间单位。

#### Scenario: OpenAPI metadata 与 runtime 漂移
- **WHEN** route 的 method/path/operationId/body limit/timeout/idempotency 或认证要求与 OpenAPI 不一致，或 router 多注册未声明业务 route
- **THEN** contract test 失败且 change 不能归档

#### Scenario: 编码 world bootstrap
- **WHEN** application 返回 PersonalWorld 和包含内部 node/fence 的 current assignment
- **THEN** response 只包含 OpenAPI 允许的 world 和 client-safe assignment 字段，时间向下转换为 Unix 毫秒且不泄漏完整 AssignmentStamp

#### Scenario: 未知业务路径
- **WHEN** 客户端请求未登记 HTTP path 或错误 method
- **THEN** router 返回有界稳定非成功响应，不猜测相近 route 且不调用任何 application service

### Requirement: 每个 HTTP 请求必须执行有界解析、deadline 与稳定 correlation
HTTP adapter MUST 按 operation metadata 限制 header/body 并设置 request-scoped deadline。有 body 的 operation MUST 只接受受支持 JSON media type、单一完整 JSON value、closed object 与全部必填字段；无 body 的 operation MUST 拒绝非空 body。Unknown field、trailing value、非法 path/header/enum/identity、超限输入、客户端取消和 timeout MUST 在不泄漏输入的前提下稳定处理。每个响应 MUST 携带一个有界安全 request ID 用于 `ErrorResponse.requestId` 和内部 correlation，客户端不得用它覆盖认证或幂等 identity。

#### Scenario: 请求体包含 actor 字段
- **WHEN** invite accept 或 admission body 额外提交 PlayerID、SessionID、role、world、endpoint 或其他 OpenAPI 未声明字段
- **THEN** adapter 在调用 application 前返回 `VALIDATION_FAILED`，不把该字段保存、记录或用于授权

#### Scenario: 请求体超过 operation 上限
- **WHEN** 客户端以 chunked 或 Content-Length 请求发送超过该 operation body limit 的内容
- **THEN** adapter 有界停止解析并返回稳定非成功响应，不读取无界内容、不调用 application 且连接处理遵守 server 资源策略

#### Scenario: Application 超过 operation deadline
- **WHEN** storage、Argon2 或 application 调用在 OpenAPI timeout 内未完成
- **THEN** request context 被取消并返回安全 dependency/timeout 结果；mutation 提交状态未知时不得声称未提交或自动换新幂等 identity 重试

### Requirement: 认证与 session operation 必须只消费 Session owner 的权威事实
Bearer 认证 MUST 调用 SessionStore 原子解析 access token 并取得不可伪造的 HTTPS AuthContext 及 session 绝对 deadline；token payload、账号字段或请求参数 MUST 不能构造身份。Register/login MUST 调用 Account service 并使用 production MySQL repository、Argon2id hasher 与 Session issuer；refresh/logout/ticket MUST 直接调用 Session service。Connection ticket MUST 只接受客户端选择 WSS 或 TLS_TCP channel，由受信 EndpointProvider 决定 endpoint 和固定 scope。Logout MUST 先提交 epoch 失效，再通知当前 Composition Root 中明确的 connection invalidator。

#### Scenario: Access 与 session 记录矛盾
- **WHEN** Redis access 记录缺失、过期、epoch 陈旧或其 expiry/session deadline 与 session record 不一致
- **THEN** 认证 fail closed 并返回稳定 unauthenticated 或 dependency error，不从 token 格式恢复 AuthContext

#### Scenario: 客户端选择 ticket endpoint 或 scope
- **WHEN** ticket 请求除 channel 外提交 host、port、scope 或 actor 字段
- **THEN** closed schema 拒绝请求；成功 ticket 的 endpoint 和 scope 只来自受信配置与 Session policy

#### Scenario: 当前尚无 realtime listener 时登出
- **WHEN** 本 change 的 Composition Root 没有 WSS/TLS-TCP component 且有效 HTTPS AuthContext 调用 logout
- **THEN** SessionStore 仍原子递增 epoch 并撤销旧 token/ticket，显式 no-active-connections invalidator 完成空集合通知且不伪造 connection 状态

### Requirement: World bootstrap 与准入签发必须由窄应用编排派生权威事实
Transport-independent world-entry application MUST 拥有 `BootstrapOwnWorld`、`AcceptVisitInvite` 与 `IssueWorldAdmission` 三个公开编排用例。Bootstrap MUST 确保认证 Player 唯一 primary PersonalWorld 存在，并通过 production Placement owner 幂等确保本进程可承载的 current active assignment；只有 runtime ready 且 assignment 已原子发布 active 后才返回 world 与 assignment，不得返回缺少 assignment 的成功投影。Invite accept MUST 由 VisitSession owner 验证目标 actor、invite、revision、capacity、owner availability 和 current assignment，并把 reservation deadline 限制为 session、invite、VisitSession、assignment 与配置上限的最早值。Admission issuance MUST 从认证 session、own-world 或 current Visitor membership、完整 current AssignmentStamp 及受信 TLS_TCP endpoint 派生既有 WorldAdmission binding；payload MUST 只能选择 own-world 或 VisitSessionID。

#### Scenario: 首次查询 own-world bootstrap
- **WHEN** 有效 HTTPS actor 尚无 primary PersonalWorld 且请求 bootstrap
- **THEN** application 幂等创建唯一 primary world、启动并发布唯一 current active assignment，再返回该 actor 的安全投影；不接受客户端指定 owner/world

#### Scenario: Bootstrap 启动 runtime 失败
- **WHEN** primary PersonalWorld 已存在但 runtime capacity、placement dependency、commit状态或ready条件无法证明active assignment
- **THEN** bootstrap返回既有稳定dependency/capacity error并省略伪成功结果，不签发credential、不留下可写starting assignment或memory fallback

#### Scenario: 并发重复 bootstrap
- **WHEN** 同一 Player 对相同 primary PersonalWorld并发或在response丢失后重复请求bootstrap
- **THEN** Placement原子决议并返回同一current active assignment，不启动第二个WorldInstance或改变已提交generation/fence

#### Scenario: 接受临近到期 invite
- **WHEN** 目标 Visitor 以正确 expected revision 接受 pending invite 且 invite、session 或 assignment 的剩余寿命短于默认 reservation lifetime
- **THEN** VisitSession owner 使用所有权威 deadline 的最早值创建一次 reservation，不由 handler 自行延长或猜测 expiry

#### Scenario: Visitor 请求 world admission
- **WHEN** bearer actor 在指定 VisitSession 中具有 current `reserved` 或 `reconnecting` membership
- **THEN** application 分别派生 `VISITOR/JOIN` 或 `VISITOR/RECONNECT` binding，并把 expiry 限制到 session、membership、assignment lease 和 admission policy 最早 deadline

#### Scenario: Visitor 缺少 membership
- **WHEN** actor 只有 invite、已过期 reservation 或不属于指定 VisitSession
- **THEN** operation 返回既有 `VISIT_MEMBERSHIP_REQUIRED` 或更具体稳定错误，不把 invite、bearer 或 GAMEPLAY scope 提升为 world admission

### Requirement: HTTP 幂等键必须分域、脱敏并复用既有原子决议
`acceptVisitInvite` 与 `issueWorldAdmission` MUST 要求符合 OpenAPI 的 `Idempotency-Key`。应用层 MUST 以 operation、HTTPS AuthContext lineage 和原始 key 确定性派生符合目标 owner grammar 的内部 CommandID/IssueID；原始 key MUST NOT 进入 Redis key、日志、metrics 或公开错误。相同 actor/operation/key 和相同语义重试 MUST 返回首次结果；改变 target、revision、purpose、assignment、endpoint 或权威 deadline MUST 返回既有稳定 idempotency conflict。服务端重试时钟推进 MUST NOT 被误判为客户端语义变化，也 MUST NOT 延长首次 reservation/admission expiry。Adapter MUST NOT 建立第二套幂等表或在 commit-unknown 后换新 identity 自动重试。

#### Scenario: Accept response 丢失后重试
- **WHEN** 首次 accept 已提交但 response 丢失，客户端以同一 session lineage、path、body 和 Idempotency-Key 重试
- **THEN** 派生相同 VisitSession CommandID 并重放首次 reservation/revision，不再次占用 capacity

#### Scenario: 相同 key 跨 operation 使用
- **WHEN** 同一 actor 把相同 Idempotency-Key 分别用于 accept 和 admission issue
- **THEN** domain separation 产生不同内部 identity，两个 owner 不共享或覆盖幂等状态

#### Scenario: Admission target 改变
- **WHEN** 相同 actor/session/key 把 own-world 改为另一个 VisitSession，或 current 权威 binding 与首次语义不同
- **THEN** WorldAdmissionStore 按相同 IssueID 与不同 fingerprint 返回 `WORLD_IDEMPOTENCY_CONFLICT` 且不签发第二个 credential

### Requirement: World admission 必须公开并冻结 Visitor 首帧 revision

`issueWorldAdmission` MUST 在 Visitor JOIN/RECONNECT 资格解析时读取 current VisitSession revision，把该 revision 纳入幂等 WorldAdmission binding，并以 `visitRevision` 返回；`OWN_WORLD` MUST 返回 `0`。相同 IssueID 的 response-loss replay MUST 返回首次冻结的 revision，不能随之后的 VisitSession mutation 漂移。客户端 MUST 只用该 revision 构造 JOIN/RECONNECT 首帧，不得从断线前 projection 推导、递增或探测 current revision。

#### Scenario: Visitor gameplay 断开后 grace 内恢复

- **WHEN** 服务端已把 Visitor 从 joined 提交为 reconnecting 并推进 VisitSession revision，旧 gameplay 已无法接收 replacement PUSH
- **THEN** 新 `RECONNECT` admission 返回该次签发读取并纳入 credential binding 的 current `visitRevision`，Visitor 以该值提交首帧并恢复同一 membership

#### Scenario: Admission 响应丢失后重放

- **WHEN** 相同 actor、target 与 IssueID 重试一次已提交的 Visitor admission，而 VisitSession revision 在首次提交后继续推进
- **THEN** 服务端返回首次 credential 与首次冻结的 `visitRevision`，不得把新 revision 拼接到旧 credential response

### Requirement: 公开 HTTP 必须具备有界限流、脱敏可观测与受控关闭
每个 operation MUST 解析为启动时验证的有界 rate/burst policy。匿名请求 MUST 至少按规范化 remote IP 限流，认证请求 MUST 再按 SessionID/epoch 限流；limiter 条目数量和 idle lifetime MUST 有硬上限且不能写入业务 Redis。公开日志和 metrics MUST 只使用 operationId、status class、stable outcome 和其他低基数字段，不得记录 URL identity、IP、principal、password、Authorization、token/ticket/admission、Idempotency-Key、full assignment 或 backend 错误。Public middleware MUST 共享进程 readiness：listener 已 bind 但尚未 ready 或已经 draining/stopped 时拒绝新业务调用；进入 draining 后 public listener MUST 在共享 deadline 内等待 in-flight 请求，然后 storage 才可关闭。

#### Scenario: Login 请求超过预算
- **WHEN** 同一 remote identity 在配置窗口内超过 login rate/burst
- **THEN** adapter 返回 `RATE_LIMITED` 与有界 retry 提示，不执行 Argon2 或 repository 调用且不产生 username 枚举差异

#### Scenario: 敏感请求发生内部错误
- **WHEN** register/login/refresh/admission 处理因 panic 或 dependency error 失败
- **THEN** response、结构化日志和 metrics 均不包含请求 body、credential、identity 或内部错误文本，只保留 request ID、operationId 和稳定 failure kind

#### Scenario: 进程开始关闭
- **WHEN** 服务端从 ready 进入 draining 且存在进行中的 HTTP 请求
- **THEN** public component 先停止接受新连接并在总 shutdown deadline 内等待或取消请求，随后 Redis、MySQL 和 diagnostic 按逆序释放

#### Scenario: Listener 已绑定但进程尚未 ready
- **WHEN** public component 已经 Start 成功但 Composition Root 尚未完成最终 ready 迁移
- **THEN** readiness gate 返回有界 dependency-unavailable 且不调用 application，MarkReady 后新请求才可进入 handler

### Requirement: HTTP capability 必须由真实 TLS 与 production storage 分层验收
实现 MUST 包含无 listener 的 handler/middleware/application 单元测试、race 测试、OpenAPI/fixture contract tests，以及使用隔离 MySQL/Redis、真实 production adapters 与临时 TLS 证书的 integration tests。`shared/contracts/fixtures/http/cases.json` 的每个 case MUST 命中真实 router/codec/error mapper；测试 MUST 覆盖 register/login/refresh/logout/ticket/bootstrap/accept/admission 成功和负向边界、幂等重放/冲突、dependency failure、Redis flush/corruption、启动回滚和 graceful shutdown。本 capability 通过 MUST 只表示 HTTPS API 和签发边界可用，不得表示 WSS/TLS-TCP connect、ticket/admission consume 或完整 own/visit-world 竖切已经验收。

#### Scenario: 执行 HTTP storage integration harness
- **WHEN** 开发者在隔离存储与临时 TLS 环境运行统一 HTTP 验收
- **THEN** 全部公开 operation 通过真实 service graph 访问 production adapters，测试结束后资源有界清理且没有 memory/fake adapter 进入 Composition Root

#### Scenario: 只完成本 change
- **WHEN** HTTPS 测试能够签发 connection ticket 和 world admission 但后续 WSS/TLS-TCP change 尚未完成
- **THEN** 项目只声明 HTTP bootstrap capability 通过，advertised realtime endpoint 不被当作 connectivity 或服务端 v1 资格验收证据

### Requirement: BattleTicket handler 必须只适配权威 battle admission application

`issueBattleTicket` handler MUST 只执行 closed decode、Bearer AuthContext、deadline/idempotency、调用 BattleTicket application 和集中 codec 映射。Application MUST 从 Session、PersonalWorld/VisitSession role、current SimulationTarget、capacity owner 与 trusted BattleEndpointProvider 派生 binding，并在 Redis issuance 和 exact child install 均成功后返回。Handler MUST 不访问 C++ handle、Redis、placement、socket 或 crypto provider，不从 Host/request body 推导 endpoint，不把 ConnectionTicket/WorldAdmission/GAMEPLAY scope 提升为 battle 资格。

#### Scenario: Valid bearer 但 target 尚未 ready

- **WHEN** actor session 有效但 SimulationTarget missing/stale、child listener 未 ready 或 capacity 不可证明
- **THEN** operation 返回稳定 dependency/capacity 结果且不签发或安装 credential

#### Scenario: Battle ticket response 丢失后重试

- **WHEN** 首次 issuance/install 已提交但 response 丢失，caller 以相同 lineage、target 和 Idempotency-Key 重试
- **THEN** application 重放首次 ticket/expiry/endpoint，不再次占用 actor slot；handler 不生成新 identity

#### Scenario: 进程进入 draining

- **WHEN** public runtime 已停止新 battle issuance 但旧 HTTP 连接仍提交 request
- **THEN** readiness/draining gate 在 application 前拒绝，不让即将撤销的 target 产生新 ticket
