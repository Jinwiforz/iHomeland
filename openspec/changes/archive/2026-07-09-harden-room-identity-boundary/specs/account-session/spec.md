## ADDED Requirements

### Requirement: 账号会话身份必须作为后续房间请求身份来源
MUST:注册、登录或会话恢复成功后，服务端绑定到 gateway connection 的玩家身份必须成为该 connection 后续房间大厅请求的身份来源；客户端声明的 `player_id` 不得覆盖服务端绑定身份。

#### Scenario: 登录成功后进入房间大厅
- **WHEN** 玩家通过注册、登录或会话恢复成功绑定 connection 身份
- **THEN** 后续房间大厅请求必须以该绑定玩家身份作为服务端授权依据

#### Scenario: 登出后发送房间请求
- **WHEN** 玩家登出导致 connection session 清理玩家身份后继续发送房间大厅请求
- **THEN** 服务端必须拒绝请求并返回结构化 `UNAUTHENTICATED` 错误
