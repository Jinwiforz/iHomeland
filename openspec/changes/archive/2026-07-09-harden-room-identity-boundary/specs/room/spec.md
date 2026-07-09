## ADDED Requirements

### Requirement: 房间大厅请求身份必须匹配服务端会话身份
MUST:房间大厅请求 payload 中的 `player_id` 必须与 gateway connection session 中服务端确认的玩家身份一致；不一致时必须拒绝请求并保持房间状态不变。

#### Scenario: 玩家伪造其他玩家身份
- **WHEN** 已登录玩家发送房间大厅请求，但 payload `player_id` 与 connection session `PlayerID` 不一致
- **THEN** 服务端必须返回结构化身份错误
- **AND** 房间成员、房主、准备状态和连接状态不得被该请求修改

#### Scenario: 玩家使用自身身份操作房间
- **WHEN** 已登录玩家发送房间大厅请求，且 payload `player_id` 等于 connection session `PlayerID`
- **THEN** 服务端可以继续执行原有房间状态机校验
