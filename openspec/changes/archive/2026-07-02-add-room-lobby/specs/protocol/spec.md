## ADDED Requirements

### Requirement: 协议必须定义房间大厅消息
实时协议必须为自定义房间大厅定义稳定的 Protobuf 消息和 message id，所有房间大厅消息必须使用 `2000-2999` 号段并记录 owner 为 `room`。

#### Scenario: 新增创建房间消息
- **WHEN** 协议新增 `CreateRoomRequest` 和 `CreateRoomResponse`
- **THEN** 它们必须分配稳定 message id，并在协议注册表中记录用途和 owner

#### Scenario: 新增加入房间消息
- **WHEN** 协议新增 `JoinRoomRequest` 和 `JoinRoomResponse`
- **THEN** 它们必须使用房间大厅号段，并通过 request id 关联请求和响应

#### Scenario: 新增准备和退出消息
- **WHEN** 协议新增准备、取消准备、退出和房主转移消息
- **THEN** 每个可路由消息必须有稳定 message id，且响应必须返回相同 request id

### Requirement: 房间协议响应必须携带房间快照
房间大厅操作成功响应必须携带可供客户端刷新大厅界面的房间快照，快照必须包含房间状态、房主、成员、座位、阵营、准备状态和连接状态。

#### Scenario: 创建房间成功
- **WHEN** 服务端处理创建房间请求成功
- **THEN** 响应 payload 必须包含最新房间快照

#### Scenario: 成员状态变化
- **WHEN** 成员加入、准备、退出、房主转移或重连恢复成功
- **THEN** 响应 payload 必须包含变化后的房间快照

### Requirement: 房间协议请求必须携带玩家身份和请求关联信息
在账号系统接入前，房间大厅请求必须显式携带 `player_id`，并且需要响应的请求必须携带 envelope `request_id`。

#### Scenario: 请求缺少 player id
- **WHEN** 客户端发送缺少 `player_id` 的房间大厅请求
- **THEN** 服务端必须拒绝请求并返回结构化错误

#### Scenario: 请求缺少 request id
- **WHEN** 客户端发送需要响应但缺少 envelope `request_id` 的房间大厅请求
- **THEN** 网关或 room dispatcher 必须拒绝请求并返回 `REQUEST_ID_REQUIRED`

### Requirement: 房间协议错误必须复用结构化错误响应
房间大厅请求失败时必须复用实时协议的 `ErrorResponse`，并通过稳定错误码或可诊断 detail 表达权限、状态迁移、容量、成员不存在和重连资格错误。

#### Scenario: 加入已满房间失败
- **WHEN** 玩家请求加入已满房间
- **THEN** 服务端返回结构化错误响应并保留原请求的 request id

#### Scenario: 非房主转移房主失败
- **WHEN** 非房主请求转移房主
- **THEN** 服务端返回结构化错误响应并保持房间状态不变

### Requirement: 房间快照推送必须可追踪
当房间状态因成员操作发生变化时，服务端可以向相关连接发送房间快照推送；推送消息必须使用稳定 message id 并携带可诊断的 sequence。

#### Scenario: 成员加入后推送快照
- **WHEN** 成员加入导致房间状态变化
- **THEN** 服务端可以向房间内在线成员发送 `RoomSnapshotPushed`，并携带最新房间快照
