## ADDED Requirements

### Requirement: Unity 客户端必须通过房间大厅进入联机流程
MUST:Unity 客户端在已登录玩家触发 `Start Game` 后，必须进入房间大厅入口或创建/加入房间流程，不得直接进入 `BattleScene` 或正式战斗模拟。

#### Scenario: 已登录玩家点击 Start Game
- **WHEN** 已登录玩家在 `HomePage` 触发 `Start Game`
- **THEN** Unity 客户端必须打开房间大厅入口或房间大厅页面
- **AND** 客户端不得加载 `BattleScene`

#### Scenario: 未登录玩家点击 Start Game
- **WHEN** 未登录玩家在 `HomePage` 触发 `Start Game`
- **THEN** Unity 客户端必须返回或停留在 `LoginPage`
- **AND** 客户端不得发送任何房间大厅请求

### Requirement: Unity 客户端必须提供房间大厅运行时状态边界
MUST:Unity 客户端必须通过独立房间状态边界管理当前 `RoomSnapshot`、房间操作结果和本地重连上下文，UI 页面不得自行维护最终房间事实状态。

#### Scenario: 房间操作成功
- **WHEN** Unity 客户端收到创建、加入、准备、退出、房主转移或重连响应
- **THEN** 房间状态边界必须保存响应中的最新 `RoomSnapshot`
- **AND** UI 必须从该快照刷新展示状态

#### Scenario: 登出或会话失效
- **WHEN** 玩家登出或账号会话恢复失败
- **THEN** Unity 客户端必须清理当前房间状态和本地重连上下文

### Requirement: Unity 客户端房间请求必须使用服务端确认的玩家身份
MUST:Unity 客户端发送现有房间大厅请求时，必须使用 `AccountSystem` 中服务端确认的当前玩家 ID 填充 payload `player_id`，不得允许 UI 输入或临时本地字符串决定玩家身份。

#### Scenario: 创建房间请求
- **WHEN** 已登录玩家请求创建房间
- **THEN** `CreateRoomRequest.player_id` 必须等于当前账号会话的 `CurrentPlayerID`

#### Scenario: 当前玩家身份缺失
- **WHEN** 客户端没有服务端确认的当前玩家 ID
- **THEN** 客户端必须拒绝发送房间大厅请求并提示需要重新登录

### Requirement: Unity 客户端必须提供房间大厅基础操作 UI
MUST:Unity 客户端必须提供可操作的房间大厅 UI，支持创建房间、输入 room id 加入房间、准备/取消准备、退出房间、房主转移和查看最新成员快照。

#### Scenario: 创建房间成功
- **WHEN** 玩家在房间大厅 UI 提交房间名和容量并且服务端创建成功
- **THEN** UI 必须展示服务端返回的房间 ID、房间名、容量、房主和成员列表

#### Scenario: 加入房间成功
- **WHEN** 玩家输入有效 room id 并且服务端加入成功
- **THEN** UI 必须展示服务端返回的最新成员列表、座位、阵营、准备状态和连接状态

#### Scenario: 设置准备状态
- **WHEN** 玩家点击准备或取消准备并且服务端处理成功
- **THEN** UI 必须以响应中的 `RoomSnapshot` 更新该玩家准备状态

#### Scenario: 房主转移成功
- **WHEN** 房主选择目标成员并且服务端转移成功
- **THEN** UI 必须以响应中的 `RoomSnapshot` 更新房主标记和可用操作

#### Scenario: 退出房间
- **WHEN** 玩家点击退出房间并且服务端处理成功
- **THEN** Unity 客户端必须清理当前房间快照并返回房间大厅入口或 `HomePage`

### Requirement: Unity 客户端必须支持房间身份重连恢复
MUST:Unity 客户端在本地存在最近 room id 且当前账号已登录时，必须允许尝试发送 `ReconnectRoomRequest` 恢复房间身份；恢复是否成功以服务端响应为准。

#### Scenario: 重连恢复成功
- **WHEN** 客户端使用当前玩家 ID 和最近 room id 发送 `ReconnectRoomRequest` 且服务端返回成功
- **THEN** 客户端必须保存响应中的 `RoomSnapshot` 并展示恢复后的房间大厅 UI

#### Scenario: 重连恢复失败
- **WHEN** 服务端拒绝 `ReconnectRoomRequest`
- **THEN** 客户端必须清理本地最近房间上下文
- **AND** 客户端必须停留在房间大厅入口或返回 `HomePage`

### Requirement: Unity 房间大厅联调文档必须覆盖最小端到端验收路径
MUST:Unity 客户端接入文档必须记录房间大厅端到端验收路径，覆盖本地服务端启动、登录、创建房间、第二玩家加入、准备/取消准备、退出、房主转移和重连恢复。

#### Scenario: 开发者执行本地房间大厅验收
- **WHEN** 开发者按文档启动本地服务端和 Unity 客户端
- **THEN** 文档必须能引导其完成至少一个创建房间、加入房间和准备状态刷新的成功链路
