## ADDED Requirements

### Requirement: Unity 客户端必须使用服务端注册登录登出
MUST:Unity 客户端不得把本地账号表单校验成功当作登录成功，必须通过服务端账号会话协议完成注册、登录、登出和会话恢复。

#### Scenario: 客户端注册成功
- **WHEN** 服务端返回注册成功响应
- **THEN** Unity 客户端记录玩家资料和 session 状态，并进入 `HomePage`

#### Scenario: 客户端登录成功
- **WHEN** 服务端返回登录成功响应
- **THEN** Unity 客户端记录玩家资料和 session 状态，并进入 `HomePage`

#### Scenario: 客户端登出成功
- **WHEN** 服务端返回登出成功响应
- **THEN** Unity 客户端清理本地账号状态并返回 `LoginPage`

### Requirement: Unity 客户端进入房间大厅前必须具备登录身份
MUST:Unity 客户端进入创房、进房、退房、房主转移等房间大厅流程前，必须拥有服务端确认的玩家身份。

#### Scenario: 未登录点击 Start Game
- **WHEN** 未登录客户端触发进入联机流程
- **THEN** 客户端必须返回或停留在 `LoginPage`，不得发送房间大厅请求

#### Scenario: 已登录点击 Start Game
- **WHEN** 已登录客户端触发进入联机流程
- **THEN** 客户端应进入房间大厅或创建/加入房间流程，而不是直接进入战斗模拟

### Requirement: Unity 客户端必须处理会话恢复失败
MUST:Unity 客户端启动时若尝试使用本地 session token 恢复会话失败，必须清理本地登录状态并展示登录入口。

#### Scenario: 本地 token 过期
- **WHEN** 客户端启动并提交过期 session token
- **THEN** 服务端拒绝恢复，客户端清理 token 并显示 `LoginPage`
