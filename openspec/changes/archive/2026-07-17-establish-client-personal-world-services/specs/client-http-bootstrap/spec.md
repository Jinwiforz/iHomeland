## MODIFIED Requirements

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
