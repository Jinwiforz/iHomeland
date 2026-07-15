## Context

当前唯一服务进程已经拥有诊断 listener、MySQL/Redis lifecycle，以及 Account、Session、PersonalWorld、Placement、VisitSession、WorldAdmission 的纯 Go core 与 production adapters；这些业务组件仍未进入正式 Composition Root。共享 OpenAPI 已冻结 10 个启动/账号/world operation、稳定错误和 deterministic fixtures，但没有真实公开 listener。

本 change 是 N0 的第一个公开 transport。它必须同时满足以下约束：公开业务面与诊断面隔离；production 只允许受验证 TLS；handler 只承担 transport 适配；认证事实来自 SessionStore；world/visit 事实由既有 owner 派生；幂等 identity 不得泄漏或跨 operation 混用；启动、readiness、关闭与 storage lifecycle 保持单一所有权。WSS 和 TLS/TCP 尚未实现，因此本 change 只能发布其受信 advertised endpoint 并签发已冻结的短期资格，不能声称 realtime listener 已经可用。

## Goals / Non-Goals

**Goals:**

- 真实开放现有 OpenAPI 的全部 HTTPS operation，并以既有 fixtures、错误目录和领域结果为唯一行为基线。
- 用 production MySQL/Redis adapters 构造完整 HTTP 所需 service graph，不引入 memory fallback 或 transport-local credential/state machine。
- 在 transport-independent application 边界完成 own-world bootstrap、invite accept 和 admission binding 派生，保持 handler 的 decode/validate/authorize/call/encode 职责。
- 对 TLS、请求资源、认证、幂等、限流、日志、metrics、readiness 和 graceful shutdown 建立 fail-closed 运行边界。

**Non-Goals:**

- 不实现 WSS/TLS-TCP listener、ticket consume、admission consume、connection registry、realtime dispatcher 或 world mutation。
- 不创建 Room、Party、ActivityInstance、battle、Unity runtime 或 Go 端到端业务客户端。
- 不修改 OpenAPI operation、公开 DTO、错误编号或 world admission credential 语义。
- 不在本 change 启动 WorldInstance runtime；bootstrap 只读取 current placement，没有 active assignment 时返回不含 assignment 的 world 投影，admission 则返回 `WORLD_NOT_READY`。

## Decisions

### 1. 使用独立 `httpapi` transport 与标准 `http.Server` lifecycle

新增 `server/internal/transport/httpapi`，由 Gin 只承担路由和 middleware 组合，外层仍由显式 `http.Server`、`net.Listener` 和 `tls.Config` 管理资源。使用 `gin.New` 配置项目自己的 recovery、日志、request ID 和 error writer，不启用会记录请求体或凭据的默认 middleware。Domain/application/storage package 不导入 Gin 或 `net/http`。

公开 listener 是独立 lifecycle component，启动顺序为 diagnostic、MySQL、Redis、public HTTPS，关闭时先停止 public HTTPS 接受新请求，再释放 Redis、MySQL，最后停止 diagnostic。纯 service、repository wrapper 和 endpoint provider 不伪装成 component。

选择 Gin 是为了遵守项目长期技术栈并只在 transport 边界使用成熟路由能力；完全使用标准库虽可减少一个依赖，但会重复实现参数路由和 middleware 组合。Gin 及其传递依赖必须按 `versions.yaml` 的 latest-compatible policy 锁定、验证并进入 `go.mod/go.sum`。

### 2. 配置和 secret 在任何公开网络副作用前一次性准备

`internal/config` 增加启动后只读的 Public API 配置，包含 bind address、TLS policy、证书文件、private-key secret reference、header/timeouts、advertised WSS/TLS-TCP endpoints、公开 limits、各 operation 限流预算，以及 Session、VisitSession、placement replay、WorldAdmission policy。所有 operationId 必须有且只能有一份 policy，地址冲突、`0` 端口、非法 endpoint、未知字段、非法 TTL 顺序、缺少证书/key/derivation key 或 production plaintext 均在创建 listener/client 前失败。

Production 强制 TLS 1.3、受验证证书/私钥和显式 public host；local/test 仅允许 loopback 明文的显式开发模式，不能被 production 环境变量隐式启用。World admission HMAC key 至少 32 bytes，通过 `SecretProvider` 解析，构造 service 时复制所需材料，并保证无论构图成功或失败都在 public component `Start` 返回前清零 bootstrap value；key、TLS private key、password、token、ticket、admission 和 Idempotency-Key 不进入普通配置、日志或错误。

### 3. Composition Root 只接入真实 adapters，并显式表达当前没有 realtime connection

MySQL component 的共享 DB 构造 Account/PersonalWorld repositories 和 Argon2id hasher；Redis component 的共享 client/keyspace 构造 Session/Placement/VisitSession/WorldAdmission stores。Clock、CSPRNG、observer、policy 和 endpoint provider 均来自唯一 root；所有 adapter 构造失败都在 listener 启动前终止。

Session `EndpointProvider` 读取已验证的不可变 advertised endpoint manifest，客户端只能选择 `WSS` 或 `TLS_TCP` channel，不能提交 host、port 或 scope。由于本 change 不存在 realtime listener，logout 使用一个语义明确的 stateless `NoActiveRealtimeConnections` invalidator：它只表示当前进程构成中不存在可关闭连接，不保存状态也不掩盖错误；下一条 WSS change 必须在同一 Composition Root 将其替换为真实 connection invalidator，不能并存两套实现。

### 4. 新增窄的 `worldentry` application coordinator

新增 transport-independent `server/internal/worldentry`，只编排三个公开 world 用例：

- `BootstrapOwnWorld`：从 HTTPS AuthContext 取得 PlayerID，调用 `PersonalWorld.EnsurePrimaryWorld`，读取 current placement，并只返回 client-safe projection；不存在 active assignment 时省略 assignment，依赖错误则 fail closed。
- `AcceptVisitInvite`：把已认证 actor、path identity、expected revision 和应用层 command identity交给 VisitSession owner。VisitSession service 负责读取一次权威 snapshot/assignment，并将 reservation deadline 取为配置 lifetime、invite expiry、VisitSession expiry、assignment lease 与 session deadline 的最早值，避免 handler 重现状态机。
- `IssueWorldAdmission`：own-world 从认证 actor、自有 world、current full assignment、TLS/TCP endpoint 和 session deadline派生 binding；visit-world 先由 VisitSession owner 解析当前 actor 的 `reserved` 或 `reconnecting` membership、purpose 和 deadline，再构造 binding并调用现有 WorldAdmission issuer。

Coordinator 只依赖各 owner 的窄接口，返回纯 Go 结果，不导入 Gin、OpenAPI map/generated type、MySQL/Redis 或 listener。它不拥有 PersonalWorld、Placement、VisitSession 或 credential 状态，也不启动 goroutine。将这些动作直接放在 handler 中会违反 transport 解耦；扩张现有 WorldAdmission service去读取全部业务 owner又会把 credential owner变成万能 facade，因此选择独立、有限的 world-entry 编排边界。

### 5. 认证结果携带不可公开的 session deadline

现有 `AuthenticateAccess` 只返回 AuthContext，而 admission expiry 还必须不晚于 session 绝对 expiry。SessionStore 的原子 access 解析结果将增加 session deadline，Session service 返回不可默认展开的 `AuthenticatedSession`（AuthContext + session expiry）；HTTP middleware只把该受信值放入单次 request context。公开响应仍不增加该 deadline，handler也不能从 token payload推断。

Session owner必须验证 access expiry不晚于session expiry、两者在认证时均有效；malformed或矛盾 Redis 结果映射为 dependency failure。这样 world-entry coordinator可以用 session、membership、assignment和policy的最早 deadline签发，而不放宽既有 credential 语义。

### 6. Idempotency-Key 在应用层分域派生，原值不进入 store identity

Transport先按 OpenAPI 校验 16-128 bytes 安全 ASCII，但不把原始 header直接当作 VisitSession `CommandID` 或 WorldAdmission `IssueID`。`worldentry` 使用固定 domain separator、operation、认证 SessionID/epoch/PlayerID 和原始 key 计算 SHA-256，并生成符合各 owner namespace 的稳定内部 identity。不同 actor、operation 或 session lineage不会碰撞；相同 actor/operation/key可解析 response loss；相同 identity改变 target/revision/binding由既有 fingerprint返回稳定 conflict。

此转换同时解决 OpenAPI 允许字符与内部 identifier grammar不完全相同的问题，并避免 raw Idempotency-Key进入 Redis key、日志或 metrics。它不是新的幂等状态机，提交/重放决议仍分别由 VisitSessionStore 和 WorldAdmissionStore 原子完成。`observedAt + TTL` 属于服务端首次结果而非客户端语义：VisitSession command fingerprint不包含每次重试都会变化的派生reservation deadline；WorldAdmission按IssueID解析首次binding，只在actor/target/purpose/assignment/endpoint变化或权威deadline收紧时冲突，并始终重放首次较短expiry，不能用稍后时钟延长资格。

### 7. OpenAPI operation table 是唯一 HTTP 执行策略入口

`httpapi` 维护一个集中、不可变的 operation table，逐项绑定 method/path/operationId、是否认证、body limit、timeout 和幂等模式。Contract test从 `openapi.yaml` 读取同一 metadata并要求 table精确一致，防止 handler散落魔法值或契约漂移。请求 DTO/响应 projection集中在 versioned codec边界，并由 OpenAPI schema、closed-object negative cases和 fixtures逐字段验证；不得在单个 handler各自定义同义结构。

有 body 的 operation只接受 `application/json`，使用有界 reader、拒绝非法UTF-8、unknown field/trailing JSON/重复或缺失必填组合；无 body operation拒绝非空 body。Register/login 在调用 application 前执行 OpenAPI 形状与既有 Account 值对象边界，password 同时遵守 schema character limit 与领域 128-byte 资源上限。Path/header/query在调用 application前完成严格解析。所有时间只在最后投影时从 UTC微秒确定性向下转换为 Unix毫秒，内部授权仍使用原精度。

### 8. Middleware 顺序固定并对公开错误脱敏

执行顺序固定为：panic recovery → request ID/安全响应头 → method/path匹配 → shared readiness gate → 声明长度/header资源门 → pre-auth rate limit → operation deadline → bearer authenticate（需要时）→ post-auth rate limit → 有界 JSON decode/validate → handler → stable error encode → access log/metrics。未知长度的 chunked body 仍由 operation table 指定的有界 reader 限制。public listener完成bind但Composition Root尚未标记ready的短窗口，以及进程进入draining后，gate均返回有界dependency-unavailable且不调用application。客户端断开或 deadline 会取消 application context；mutation commit-unknown 不能被改写为“肯定未提交”，响应只返回既有 `DEPENDENCY_UNAVAILABLE`，客户端按原幂等 identity重试。

Request ID 接受客户端值时只允许有界安全 ASCII，否则由 CSPRNG创建；它只用于 correlation，不参与授权。错误映射由集中 catalog完成，HTTP status/code/messageKey/retryable沿用 `errors.json`；响应和日志不得包含 password、Authorization、refresh/access token、ticket、admission、Idempotency-Key、principal、full assignment、store key/value或内部错误文本。

### 9. 限流和可观测状态有界且不是业务事实

Public HTTP component使用有容量上限和 idle expiry 的进程内 limiter。匿名 operation按规范化 remote IP，认证 operation再按 SessionID/epoch限流；register/login同时受更严格 IP预算，防止 credential stuffing与账号枚举放大。超过预算返回 `RATE_LIMITED` 和有界 `Retry-After`，bucket key不进入日志或 metrics label。多实例全局限流留给部署 edge；本地 limiter只提供单实例安全下限，不写 Redis、不成为账号/session事实。

Metrics只使用 operationId、status class和三值稳定 outcome；不使用 method、failure detail、URL原文、ID、IP或错误文本。Access log只记录 request ID、operationId、status、duration、response bytes和稳定 outcome。Bootstrap config只公开协议允许的 endpoints/limits，不公开 bind地址、secret reference、内部 node或 storage配置。

### 10. 测试分层并区分 HTTP ready 与 realtime 可连接

纯 handler/middleware测试使用 `httptest` 与 fake application ports，不启动 MySQL/Redis；worldentry测试使用各 owner fake覆盖 binding、deadline、幂等和错误映射。带 `storage_integration` tag 的 HTTPS测试使用隔离 MySQL/Redis、真实 migrations/adapters/services和临时测试证书，覆盖 register/login/refresh/logout/ticket/bootstrap/accept/admission的成功、重放、冲突、陈旧身份/assignment、Redis corruption/flush和关闭。

Contract fixture runner必须让 `shared/contracts/fixtures/http/cases.json` 的每个 case命中真实 router/codec/error mapper；world admission semantic corpus继续由已有 runtime组合测试负责，本 change只补签发入口场景。Diagnostic readiness在 public HTTPS成功启动后才进入 ready；其含义是“本进程必需 storage与HTTP API可服务”，advertised WSS/TLS-TCP endpoint在后续 listener change前仍只是部署配置，测试和文档不得把它描述为 realtime connectivity已通过。

## Risks / Trade-offs

- [在 WSS/TLS-TCP 实现前可签发无法完成连接的 ticket/admission] → 明确区分 advertised endpoint 与 realtime readiness；本 change只验收签发和绑定，后续 listener change完成前不宣称端到端可连接。
- [Public HTTP 接线一次涉及多个已有 owner] → 仅新增一个三用例 worldentry coordinator；账号和会话继续直接由原 service拥有，禁止 generic facade或跨存储补偿。
- [进程内限流不能形成多实例全局配额] → 将其定义为单实例安全下限并保持 bounded；生产 edge可追加全局策略，但不能关闭应用内最低门槛。
- [Argon2、storage I/O或慢客户端占用请求预算] → operation timeout、body/header上限、hasher并发门、server timeout和graceful shutdown共同限制资源；取消不被错误解释为 mutation未提交。
- [OpenAPI 与手写 transport projection漂移] → 集中 codec/operation table，并由 OpenAPI metadata、closed schema negative cases和全部 fixtures做精确契约测试。
- [临时 no-active-connections invalidator被遗忘] → structure/composition test明确要求它只能在无 realtime component时存在；WSS change必须替换且禁止同时接线。

## Migration Plan

1. 扩展配置、示例和 secret准备；local默认只绑定loopback开发地址，test使用动态端口，production强制显式TLS/public配置，并先验证纯配置无网络副作用。
2. 补齐 Session认证 deadline、VisitSession HTTP用例桥接与 worldentry coordinator，全部通过无 listener单元测试。
3. 构造 production adapters/services和 httpapi component，按 lifecycle末端接入；storage或 listener任一步失败均走现有逆序回滚。
4. 运行 contract、race、storage integration与真实 TLS fixture验收后更新 server/运维文档。
5. 部署时先准备证书、derivation key和advertised endpoints，再开放 public端口；回滚只需回退二进制/配置并关闭 public入口，既有 MySQL schema和Redis records无需回滚，已签发短期资格按TTL自然失效。

## Open Questions

无。Gin与其他新增 dependency的精确版本在 apply 时按 `versions.yaml` 的 latest-compatible规则选择、锁定并由统一依赖验证门确认，不改变上述设计。
