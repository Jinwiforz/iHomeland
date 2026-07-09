## Why

当前第一阶段房间大厅已经支持账号会话、创房、进房、准备、退出、房主转移和断线重连，但缺少“房主请求开始后，服务端决定是否允许房间离开大厅”的明确闸门。没有该闸门，第一里程碑仍停在大厅操作集合，无法形成从组队到进入后续占位场景的闭环。

`add-room-start-gate` 用最小范围补齐这一闭环：只校验房主、成员准备状态和房间状态，不实现正式战斗、battle server 或结算。

## What Changes

- 服务端 room model/service 新增开始房间的状态机入口。
- 开始房间请求必须来自当前房主，并且请求玩家身份必须继续通过 gateway session 身份边界校验。
- 房间必须处于可开始状态；关闭房间、空房间或非法成员状态必须拒绝开始。
- 除房主外的在线成员必须已准备；断线成员不得被视为已准备。
- 开始成功后房间进入第一阶段占位状态，用于表示大厅已经通过开始闸门。
- 实时协议新增 `StartRoomRequest` / `StartRoomResponse`，使用 room 号段并返回最新 `RoomSnapshot`。
- Unity `RoomSystem` / `RoomPage` 增加开始房间入口；成功后允许进入后续占位场景或展示开始成功状态。
- 补充服务端状态机、WebSocket 和 Unity 接入文档验收说明。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `room`: 增加房间开始闸门、开始状态和对应状态机不变量。
- `protocol`: 增加房间开始请求/响应消息和 message id。
- `client-integration`: 增加 Unity 房间页开始按钮、开始响应处理和占位场景进入约束。

## Impact

- 影响服务端 `server/internal/room` 状态机、service 和测试。
- 影响服务端 `server/internal/app` room dispatcher 与 WebSocket 集成测试。
- 影响共享 Protobuf schema、协议生成产物和协议兼容文档。
- 影响 Unity `NetworkSystem`、`RoomSystem`、`RoomPage` 和本地房间大厅联调说明。
- 不新增 MySQL schema、Redis key 或 gRPC 服务。
- 不实现高频战斗同步、服务端权威模拟、观战、回放、战斗结算或独立 battle server。
