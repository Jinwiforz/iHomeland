## ADDED Requirements

### Requirement: Unity 客户端必须提供房间开始入口
MUST:Unity 客户端必须在房间大厅 UI 中为当前房主提供开始房间入口，并通过实时协议发送 `StartRoomRequest`，不得在本地绕过服务端直接进入正式战斗。

#### Scenario: 房主点击开始
- **WHEN** 当前玩家是房主并点击开始房间
- **THEN** Unity 客户端必须发送 `StartRoomRequest` 并等待服务端响应

#### Scenario: 非房主查看房间页
- **WHEN** 当前玩家不是房主
- **THEN** Unity 客户端不得允许其发起开始房间请求

#### Scenario: 开始成功
- **WHEN** Unity 客户端收到成功的 `StartRoomResponse`
- **THEN** 客户端必须保存响应中的最新 `RoomSnapshot`
- **AND** 客户端可以进入后续占位场景或展示房间已开始状态

#### Scenario: 开始失败
- **WHEN** Unity 客户端收到开始房间的结构化错误响应
- **THEN** 客户端必须保持在房间大厅 UI，并展示或记录失败原因

### Requirement: Unity 客户端不得把开始闸门当作正式战斗
MUST:Unity 客户端对开始房间成功的处理只能进入第一阶段占位流程，不得实现或暗含 battle server、高频战斗同步、战斗结算、观战或回放。

#### Scenario: 开始成功后进入占位流程
- **WHEN** 房间开始请求成功
- **THEN** 客户端进入的后续界面或场景必须被视为占位流程，而不是正式战斗模拟
