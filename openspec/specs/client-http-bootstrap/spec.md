# Client HTTP Bootstrap 规格

## Purpose

定义 Unity 客户端公开 HTTPS 启动、账号会话、连接 ticket、own-world bootstrap、安全凭据和错误处理行为。

## Requirements

### Requirement: HTTP 环境配置必须在产生网络副作用前验证

客户端 MUST 从 BootstrapScene 直接引用的非敏感环境定义构造不可变 HTTP 配置，并在创建请求或修改 session 状态前验证 base URI。Production MUST 只允许 `https`；显式 local/test 模式 MAY 使用 `http`，但 host MUST 是 loopback。Base URI MUST NOT 包含 userinfo、query、fragment 或业务 path，环境资产 MUST NOT 保存 password、token、ticket、admission 或在线账号事实。

#### Scenario: Production 配置明文 endpoint

- **WHEN** Production 环境定义使用 `http`，或 local/test 明文地址不是 loopback
- **THEN** Composition 在发送请求前失败并执行既有启动回滚，不创建降级连接或猜测替代 endpoint

#### Scenario: 环境资产包含不受支持的 URI 成分

- **WHEN** base URI 包含 userinfo、query、fragment 或非根 path
- **THEN** 配置验证返回明确的非敏感错误，HTTP adapter 不产生网络副作用

### Requirement: 客户端必须先验证版本与公开启动配置

客户端 MUST 通过 `getVersion` 和 `getBootstrapConfig` 建立只读启动投影，并验证 `protocolVersion`、`minimumClientVersion`、endpoint、port 与公开 limits。协议或客户端版本不兼容时 MUST 阻止后续认证调用；成功配置 MUST 由 App Scope 的 Configuration owner 原子替换，调用方不得从 ticket、payload、Scene 或本地猜测补全服务端 endpoint。

#### Scenario: 服务端要求更高客户端版本

- **WHEN** `minimumClientVersion` 高于当前客户端版本，或 `protocolVersion` 与锁定版本不同
- **THEN** bootstrap 返回稳定的 incompatibility 结果且不执行 register、login、refresh、ticket 或 world bootstrap

#### Scenario: 启动配置响应不完整

- **WHEN** config 缺少必填 limits、包含非法 channel/host/port 或超过契约集合上限
- **THEN** 客户端拒绝整份响应并保留原有有效配置，不发布部分 endpoint

### Requirement: HTTP adapter 必须精确实现本阶段冻结 operation

客户端 HTTP adapter MUST 以强类型方法实现 `getVersion`、`getBootstrapConfig`、`registerAccount`、`loginAccount`、`refreshSession`、`logoutSession`、`issueConnectionTicket`、`getWorldBootstrap`、`acceptVisitInvite` 与 `issueWorldAdmission` 的既有 method、path、认证、body、header、成功 status 和 JSON schema。请求 MUST 只包含 OpenAPI 声明字段；Bearer header MUST 只来自 Session owner 的当前 access token。`acceptVisitInvite` MUST 使用调用方显式提供的规范 idempotency key、VisitSessionID、InviteID 与正 expected revision；`issueWorldAdmission` MUST 使用调用方显式提供的规范 idempotency key，并把 own-world 与 visit-world target 表示为封闭强类型。HTTP 核心 MUST NOT 提供通用任意 path/body 接口绕过该边界。

#### Scenario: 调用认证 operation

- **WHEN** ticket、world bootstrap、invite accept 或 world admission 使用当前有效 session snapshot
- **THEN** adapter 只把 access token 写入该请求的 `Authorization: Bearer` header，不把 account、player、session epoch、world、instance 或 endpoint 写入请求 body

#### Scenario: 接受定向 Visit invite

- **WHEN** 调用方提供有效 VisitSessionID、InviteID、正 expected revision 与规范 idempotency key
- **THEN** adapter 精确发送 `POST /v1/visits/{visitSessionId}/invites/{inviteId}/accept`、`Idempotency-Key` 与仅含 `expectedRevision` 的 JSON，并只接受匹配 VisitSessionID、正 revision 和未过期 reservation 的登记 200 response schema

#### Scenario: Invite accept path identity 非法

- **WHEN** VisitSessionID 或 InviteID 为空、超长、含非法字符，或 expected revision 非正数
- **THEN** adapter 在创建 HTTP request 前返回稳定 validation/policy failure，不对 path 做猜测性修复或发送部分请求

#### Scenario: 签发 own-world admission

- **WHEN** 调用方以规范 idempotency key 请求 own-world admission
- **THEN** adapter 精确发送 `POST /v1/world/admissions`、`Idempotency-Key` 与 `{ "kind": "OWN_WORLD" }`，并只接受登记的 201 response schema

#### Scenario: 签发 visit-world admission

- **WHEN** 调用方以有效 VisitSessionID 请求 visit-world admission
- **THEN** adapter 只发送 `kind` 与 `visitSessionId`，不允许调用方注入 PlayerID、WorldInstanceID、role、endpoint 或 assignment

#### Scenario: 调用未登记 operation

- **WHEN** 上层尝试通过 HTTP 核心发送任意自定义 method、path 或 body
- **THEN** 编译期 API 不提供该入口，后续 capability 必须显式扩展强类型 operation 与合同测试

#### Scenario: 解析 own-world bootstrap

- **WHEN** 服务端返回有效 world 与可选 current assignment
- **THEN** adapter 以不可变 projection 返回 world 与 OpenAPI 声明的可选 assignment 字段，不把投影保存为最终业务事实，也不据此自动建立连接

#### Scenario: Bootstrap assignment 指向其他 PersonalWorld

- **WHEN** assignment 的 PersonalWorldID 与响应中的 own-world PersonalWorldID 不一致
- **THEN** adapter 将响应拒绝为 malformed，不向上层返回混合两个 world 的投影

### Requirement: Session owner 必须单次交付 world admission

Session owner MUST 只用当前 authenticated generation 签发 admission lease。HTTP codec 与交付边界 MUST 共同验证 opaque credential 语法、`TLS_TCP` endpoint、role/purpose、generation 与 expiry；每个 lease MUST 最多成功交付一次。refresh、logout、forced invalidation、forget 或 shutdown MUST 使旧 generation 的未交付 lease 失效。Credential MUST NOT 进入日志、Scene、Prefab、ScriptableObject 或持久化存储。

#### Scenario: 同一 admission 被取得两次

- **WHEN** gameplay channel 已从 lease 成功取得 credential 后再次调用 take
- **THEN** 第二次调用失败且不返回 credential 文本

#### Scenario: Admission 响应晚于 session refresh

- **WHEN** `issueWorldAdmission` 发出后当前 session generation 已因 refresh 改变
- **THEN** Session owner 拒绝迟到响应，不创建可交付 lease

#### Scenario: Admission 在连接前到期

- **WHEN** 当前 generation 未变但 admission expiry 已达到或早于当前客户端时钟
- **THEN** 单次交付失败，gameplay channel 不打开 socket

### Requirement: 每次请求必须有界且尊重调用方取消

每个 operation MUST 使用 OpenAPI 登记的 deadline 与调用方 `CancellationToken` 共同约束请求，并以有界方式读取响应。客户端 MUST 区分调用方取消、operation timeout、DNS/connect/TLS/HTTP transport 失败和 malformed response。Adapter MUST NOT 自动重试任何 operation；是否重新执行 SAFE operation 由更高层显式策略决定，NON_IDEMPOTENT operation 不得因 transport 结果未知而隐式重发。

#### Scenario: 调用方先取消请求

- **WHEN** 调用方 cancellation 在 operation deadline 前触发
- **THEN** 请求立即终止并返回 caller-cancelled，不映射为 timeout、server error 或 retry success

#### Scenario: Login 响应超时

- **WHEN** login 在冻结的 operation deadline 内未获得完整响应
- **THEN** adapter 取消底层请求、返回 timeout 且只发送过一次 login，不假设服务端未提交 session

#### Scenario: 响应体超过 operation 上限

- **WHEN** Content-Length 或流式读取结果超过客户端为该 operation 登记的 response body hard cap
- **THEN** adapter 有界停止读取并返回 malformed/oversized response，不分配无界 buffer 或尝试解析前缀 JSON

### Requirement: 服务端错误与传输错误必须稳定、安全映射

客户端 MUST 把有效 `ErrorResponse` 映射为包含 code、category、messageKey、requestId、retryable、可选 retry-after 与有界 field details 的安全结果，并对照冻结 error registry 校验已知 code 的 HTTP status 与语义。未知但结构有效的服务端 code MUST 保留为 unknown-server-error；无效 JSON、错误 media type、必填字段缺失、非法 enum、status/body 矛盾或错误 success body MUST 映射为 protocol/malformed response。UI 与日志 MUST NOT 接收内部 exception 文本、响应原文或 credential。

#### Scenario: 服务端返回限流错误

- **WHEN** HTTP 429 携带有效 `RATE_LIMITED` 和有界 retry-after
- **THEN** 结果保留稳定 messageKey、requestId 与 retry delay，不自动睡眠或重试请求

#### Scenario: 服务端返回未知错误码

- **WHEN** error body 结构有效但 code 尚未进入当前客户端 registry
- **THEN** adapter 返回安全 unknown-server-error 并保留低敏 correlation，不把响应正文或内部解析异常展示给 UI

#### Scenario: 成功状态携带错误 schema

- **WHEN** operation 返回登记的成功 status 但 body 缺少必填字段、包含非法值，或 204 logout 携带非空 body
- **THEN** 客户端拒绝响应且不提交配置、session、ticket 或 world 投影

### Requirement: Session owner 必须原子管理 token lineage 与短期凭据

App Scope MUST 只有一个 Session owner 保存 account/session/token snapshot 与单调本地 generation。Register/login 成功 MUST 在完整响应验证后原子替换 snapshot；refresh MUST single-flight，并且只有发起时的 generation 仍为 current 才能提交轮换结果。Password MUST 只存在于当前调用参数，refresh token、access token 与 ticket MUST NOT 写入 Unity 序列化资产、PlayerPrefs、日志、exception、metrics 或普通 `ToString()`。在交付安全持久化 adapter 前，进程重启 MUST 回到未认证状态。

#### Scenario: 旧 refresh 在新 login 后返回

- **WHEN** refresh 尚未完成时新的 register/login 已提交更高 generation
- **THEN** 旧 refresh 结果被丢弃且不能覆盖新 session、token 或 epoch

#### Scenario: 并发 refresh 的后续等待方取消

- **WHEN** 已存在 single-flight refresh，后续调用方在共享请求完成前取消自己的等待
- **THEN** 后续调用方收到 caller-cancelled，首个调用拥有的请求继续执行且服务端只收到一次 refresh

#### Scenario: 当前 session 被判定未认证

- **WHEN** authenticated operation 返回有效 `AUTH_UNAUTHENTICATED`，或 logout 得到有效 204
- **THEN** Session owner 原子清除 token 与尚未交付的 ticket，递增 generation 并拒绝继续使用旧 snapshot

#### Scenario: Logout 提交结果未知

- **WHEN** logout 因 timeout、取消或 transport failure 未获得可判定响应
- **THEN** Session owner 进入 unresolved 状态、清除当前 snapshot 与短期 ticket，并阻止新的 authenticated operation，直到新的 register/login 或本地 forget 解决 lineage

#### Scenario: Refresh 提交结果未知

- **WHEN** refresh 因 timeout、取消、transport failure、停止或不可判定响应而无法确认服务端是否已轮换 token
- **THEN** Session owner 进入 unresolved 状态、撤销旧 snapshot，并阻止旧 access/refresh token 继续使用，直到新的 register/login 或本地 forget 解决 lineage

#### Scenario: Connection ticket 已过期或 session 已轮换

- **WHEN** ticket 超过 `expiresAtMs`，或其来源 generation 不再 current
- **THEN** ticket 不能交给后续 channel 使用且不得自动申请或复用替代 ticket

### Requirement: HTTP capability 必须服从 App Scope 生命周期并可独立验收

`AppComposition` MUST 显式创建环境配置、HTTP transport、codec、bootstrap service 与 Session owner，并把可关闭资源登记到既有 AppLifetime。初始化只验证本地配置和构造资源，不得隐式注册、登录、签发 ticket、查询 world 或建立 WSS/TLS-TCP；停止 MUST 先取消 in-flight 请求，再释放 HTTP 资源，并使迟到 callback 无法提交状态。实现 MUST 以注入的 HTTP handler/clock 验证冻结 fixtures、deadline、取消、并发轮换、敏感信息与生命周期，同时继续通过既有 EditMode、PlayMode、协议 parity 和 Windows Development build。

#### Scenario: App Scope 在请求进行中停止

- **WHEN** shutdown 或启动失败回滚发生且存在 in-flight HTTP 请求
- **THEN** adapter 取消请求、等待有界清理并拒绝迟到状态提交，HTTP 资源只释放一次

#### Scenario: 无服务端运行时启动客户端

- **WHEN** AppRoot 只完成 Composition 与生命周期初始化且没有上层调用 bootstrap service
- **THEN** 客户端不主动访问网络，既有空场景 PlayMode 与 Windows build 仍可独立启动

#### Scenario: 执行 HTTP contract tests

- **WHEN** EditMode tests 使用冻结 HTTP fixtures 与可控 handler 执行本阶段 operation
- **THEN** method、path、headers、JSON、status、error mapping 和安全边界与公开契约一致，测试不依赖真实账号、Unity Services、WSS 或 TLS/TCP
