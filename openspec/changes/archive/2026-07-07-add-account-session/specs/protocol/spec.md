## ADDED Requirements

### Requirement: 协议必须定义账号会话消息
MUST:实时协议必须为第一阶段账号会话定义稳定的 Protobuf 消息和 message id，所有账号会话消息必须使用 `1000-1999` 号段并记录 owner 为 `account`。

#### Scenario: 新增登录消息
- **WHEN** 协议新增 `LoginRequest` 和 `LoginResponse`
- **THEN** 它们必须使用账号会话号段，并通过 request id 关联请求和响应

#### Scenario: 新增注册消息
- **WHEN** 协议新增 `RegisterRequest` 和 `RegisterResponse`
- **THEN** 它们必须使用账号会话号段，并通过 request id 关联请求和响应

#### Scenario: 新增登出和恢复消息
- **WHEN** 协议新增 `LogoutRequest`、`ResumeSessionRequest` 和对应响应
- **THEN** 每个可路由消息必须有稳定 message id，且响应必须返回相同 request id

### Requirement: 账号协议响应必须携带玩家资料和会话状态
MUST:账号注册、登录和会话恢复成功响应必须携带客户端进入在线流程所需的玩家资料、session token 和过期时间。

#### Scenario: 注册成功响应
- **WHEN** 服务端处理注册请求成功
- **THEN** 响应 payload 必须包含 `PlayerProfile`、`session_token` 和 `expires_at_ms`

#### Scenario: 登录成功响应
- **WHEN** 服务端处理登录请求成功
- **THEN** 响应 payload 必须包含 `PlayerProfile`、`session_token` 和 `expires_at_ms`

#### Scenario: 会话恢复成功响应
- **WHEN** 服务端处理有效 session token 的恢复请求成功
- **THEN** 响应 payload 必须包含最新玩家资料和会话过期时间

### Requirement: 账号协议错误必须复用结构化错误响应
MUST:账号请求失败时必须复用实时协议的 `ErrorResponse`，并通过稳定错误码或 detail 表达账号已存在、凭据非法、未登录、session 过期和权限不足。

#### Scenario: 注册账号已存在
- **WHEN** 客户端提交已存在账号名进行注册
- **THEN** 服务端返回结构化错误并保留原请求的 request id

#### Scenario: 登录凭据非法
- **WHEN** 客户端提交非法账号凭据
- **THEN** 服务端返回结构化错误并保留原请求的 request id

#### Scenario: session token 无效
- **WHEN** 客户端提交无效或过期 session token
- **THEN** 服务端返回结构化错误，客户端不得继续视为已登录
