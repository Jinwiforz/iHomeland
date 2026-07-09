## ADDED Requirements

### Requirement: 协议必须定义房间开始消息
MUST:实时协议必须为房间开始闸门定义稳定的 Protobuf 消息和 message id，消息必须使用 `2000-2999` 房间大厅号段并记录 owner 为 `room`。

#### Scenario: 新增开始房间消息
- **WHEN** 协议新增 `StartRoomRequest` 和 `StartRoomResponse`
- **THEN** 它们必须分配稳定 message id，并通过 request id 关联请求和响应

#### Scenario: 开始房间成功响应
- **WHEN** 服务端处理开始房间请求成功
- **THEN** `StartRoomResponse` payload 必须包含最新 `RoomSnapshot`

#### Scenario: 开始房间失败响应
- **WHEN** 服务端因权限、准备状态、连接状态或房间状态拒绝开始请求
- **THEN** 服务端必须返回结构化 `ErrorResponse` 并保留原请求的 request id

### Requirement: 房间快照必须表达已开始状态
MUST:房间快照协议必须能表达第一阶段占位开始状态，使客户端区分开放大厅、关闭房间和已通过开始闸门的房间。

#### Scenario: 开始后返回快照
- **WHEN** 房间开始闸门校验通过
- **THEN** 服务端返回的 `RoomSnapshot.state` 必须表达房间已开始
