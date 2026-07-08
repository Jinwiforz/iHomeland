## Why

服务端已经具备自定义房间大厅能力，Unity 客户端也已经具备真实账号会话、WebSocket envelope 和本地 smoke test，但 `HomePage.Start Game` 仍直接进入 `BattleScene`。现在需要把客户端下一步联机入口改为房间大厅流程，让第一里程碑从“服务端可用”推进到“Unity 可端到端操作房间大厅”。

## What Changes

- Unity 客户端新增房间大厅运行时边界，用于创建、加入、准备/取消准备、退出、房主转移、断线后恢复房间身份和按快照刷新状态。
- `HomePage.Start Game` 在已登录时进入房间大厅入口或创房/进房流程，不再直接加载 `BattleScene`。
- `NetworkSystem` 增加房间大厅请求方法，复用现有 WebSocket、Protobuf envelope、request_id、错误处理和心跳通道。
- 新增或调整房间大厅 UI 页面，展示房间 ID、房间名、房主、成员、座位、阵营、准备状态、在线/断线状态和可执行操作。
- 客户端房间状态以服务端 `RoomSnapshot` 为唯一事实来源，本地按钮点击只触发请求，不直接改变最终房间状态。
- 支持客户端启动或重新登录后，在有本地房间上下文且服务端仍允许时发送 `ReconnectRoomRequest` 恢复房间身份。
- 更新客户端接入文档、客户端架构文档和 OpenSpec 长期规格，记录 Unity 房间大厅 flow 的验收路径。
- 不新增协议消息、message id、MySQL 表或 Redis key；优先复用已存在的房间协议与服务端能力。
- 不实现匹配系统、正式战斗模拟、battle server、观战、回放、经济或背包系统。
- 不在本 change 中实现“开始游戏”闸门；房间内开始条件由后续 `add-room-start-gate` 承接。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `client-integration`: 补充 Unity 房间大厅端到端 flow、UI 状态来源、已登录入口、重连恢复和本地验收要求。

## Impact

- 影响 Unity 客户端：`NetworkSystem`、新增或调整的房间客户端状态系统、`HomePage`、房间大厅 UI 页面、UI prefab 和本地房间上下文保存策略。
- 影响文档：`docs/client-integration.md`、`docs/client-architecture.md`、`docs/roadmap.md` 和必要的客户端 README。
- 影响 OpenSpec：修改 `client-integration` 长期行为契约。
- 不影响服务端协议 schema、服务端房间状态机、数据库 migration 或 Redis key。
