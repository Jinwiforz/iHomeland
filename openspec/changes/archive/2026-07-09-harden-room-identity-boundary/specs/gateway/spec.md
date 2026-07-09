## ADDED Requirements

### Requirement: 房间业务分发必须使用连接身份授权
MUST:gateway 将房间大厅请求交给业务分发边界时，服务端必须使用 connection session 中已绑定的玩家身份作为授权依据；未绑定玩家身份的 connection 不得执行房间大厅状态变更。

#### Scenario: 未登录连接发送房间请求
- **WHEN** 未绑定玩家身份的 WebSocket connection 发送创建、加入、准备、退出、房主转移或重连房间请求
- **THEN** 服务端必须返回结构化 `UNAUTHENTICATED` 错误
- **AND** 不得调用 room service 修改房间状态

#### Scenario: 已登录连接发送房间请求
- **WHEN** 已绑定玩家身份的 WebSocket connection 发送房间大厅请求
- **THEN** 服务端必须把 connection session 中的玩家身份用于请求授权校验
