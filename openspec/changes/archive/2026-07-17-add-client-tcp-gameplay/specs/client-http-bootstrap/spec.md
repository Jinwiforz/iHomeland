## MODIFIED Requirements

### Requirement: HTTP adapter 必须精确实现本阶段冻结 operation

客户端 HTTP adapter MUST 以强类型方法实现 `getVersion`、`getBootstrapConfig`、`registerAccount`、`loginAccount`、`refreshSession`、`logoutSession`、`issueConnectionTicket`、`getWorldBootstrap` 与 `issueWorldAdmission` 的既有 method、path、认证、body、header、成功 status 和 JSON schema。请求 MUST 只包含 OpenAPI 声明字段；Bearer header MUST 只来自 Session owner 的当前 access token。`issueWorldAdmission` MUST 使用调用方显式提供的规范 idempotency key，并把 own-world 与 visit-world target 表示为封闭强类型。`acceptVisitInvite` 不属于本 capability，HTTP 核心不得以通用任意 path/body 接口绕过该边界。

#### Scenario: 调用认证 operation

- **WHEN** ticket、world bootstrap 或 world admission 使用当前有效 session snapshot
- **THEN** adapter 只把 access token 写入该请求的 `Authorization: Bearer` header，不把 account、player、session epoch 或 endpoint 写入请求 body

#### Scenario: 签发 own-world admission

- **WHEN** 调用方以规范 idempotency key 请求 own-world admission
- **THEN** adapter 精确发送 `POST /v1/world/admissions`、`Idempotency-Key` 与 `{ "kind": "OWN_WORLD" }`，并只接受登记的 201 response schema

#### Scenario: 签发 visit-world admission

- **WHEN** 调用方以有效 VisitSessionID 请求 visit-world admission
- **THEN** adapter 只发送 `kind` 与 `visitSessionId`，不允许调用方注入 PlayerID、WorldInstanceID、role、endpoint 或 assignment

#### Scenario: 调用未纳入本阶段的 operation

- **WHEN** 上层尝试通过 HTTP 核心直接发送 invite accept 或任意自定义 method/path
- **THEN** 编译期 API 不提供该入口，后续 capability 必须以独立强类型 operation 扩展

#### Scenario: 解析 own-world bootstrap

- **WHEN** 服务端返回有效 world 与可选 current assignment
- **THEN** adapter 以不可变 projection 返回 world 与 OpenAPI 声明的可选 assignment 字段，不把投影保存为最终业务事实，也不据此自动建立连接

#### Scenario: Bootstrap assignment 指向其他 PersonalWorld

- **WHEN** assignment 的 PersonalWorldID 与响应中的 own-world PersonalWorldID 不一致
- **THEN** adapter 将响应拒绝为 malformed，不向上层返回混合两个 world 的投影

## ADDED Requirements

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
