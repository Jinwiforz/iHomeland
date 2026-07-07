## ADDED Requirements

### Requirement: 网关 session 必须支持登录身份绑定
MUST:网关连接级 session 必须能在账号登录或会话恢复成功后绑定 `player_id`，并在登出、连接关闭或会话失效时清理该身份。

#### Scenario: 登录成功后绑定身份
- **WHEN** account service 确认登录成功
- **THEN** 网关 session 记录该连接对应的 `player_id` 和 session token 摘要

#### Scenario: 登出后清理身份
- **WHEN** 已登录连接完成登出
- **THEN** 网关 session 必须清理玩家身份，使后续需要登录的业务请求无法通过身份校验

### Requirement: 网关必须通过账号边界处理账号消息
MUST:网关不得直接校验账号凭据或维护账号业务状态，账号会话消息必须通过 account service 或 account dispatcher 处理。

#### Scenario: 收到登录请求
- **WHEN** 网关收到账号登录 message id
- **THEN** 网关将 session 摘要、envelope 元数据和 payload 转发给 account 边界处理

#### Scenario: 账号处理器缺失
- **WHEN** 网关收到账号会话 message id 但没有注册 account 处理器
- **THEN** 网关返回 `MESSAGE_ID_UNSUPPORTED` 结构化错误，不改变连接身份
