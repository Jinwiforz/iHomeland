## 1. 网络请求封装

- [x] 1.1 在 `NetworkSystem` 中新增 `CreateRoomAsync`、`JoinRoomAsync`、`SetReadyAsync`、`LeaveRoomAsync`、`TransferHostAsync` 和 `ReconnectRoomAsync` 方法。
- [x] 1.2 确保所有房间请求复用现有 `SendRequestAsync`、`request_id`、timeout、payload 解码和 `ErrorResponse` 包装逻辑。
- [x] 1.3 为房间响应 payload 解码失败、服务端结构化错误和请求超时路径补充客户端日志或结果边界。

## 2. 客户端房间状态系统

- [x] 2.1 新增 `RoomSystem` 或等价系统，管理当前 `RoomSnapshot`、操作中状态和最近 room id。
- [x] 2.2 将 `RoomSystem` 接入 `AppRoot` 创建、初始化、Tick 和 Shutdown 生命周期。
- [x] 2.3 实现创建、加入、准备/取消准备、退出、房主转移和重连恢复的系统级方法。
- [x] 2.4 所有房间请求必须从 `AccountSystem.CurrentPlayerID` 读取玩家身份，身份缺失时拒绝发送请求。
- [x] 2.5 实现登出或会话恢复失败后的房间状态和本地重连上下文清理。
- [x] 2.6 保存最近 room id 的本地上下文，仅用于后续 `ReconnectRoomRequest`，不得保存可替代服务端快照的完整房间事实。

## 3. 房间大厅 UI

- [x] 3.1 新增 `RoomPage` 页面脚本，展示房间 ID、房间名、容量、房主、成员列表、座位、阵营、准备状态和连接状态。
- [x] 3.2 新增 `RoomPage` prefab 并放入 `Assets/App/Resources/UI/Pages/`。
- [x] 3.3 在 `AppPages` 中注册 `RoomPage` 页面名。
- [x] 3.4 在 `RoomPage` 中提供创建房间、输入 room id 加入、准备/取消准备、退出房间和房主转移操作；创建、加入、转移和退出确认可以使用页面内子面板或轻量对话框。
- [x] 3.5 UI 操作只触发请求和 pending 状态；最终 UI 状态必须以 `RoomSystem` 保存的 `RoomSnapshot` 刷新。
- [x] 3.6 为请求中、失败、未登录、无当前房间和服务端错误状态提供可见提示。

## 4. 主流程接入

- [x] 4.1 将 `HomePage.Start Game` 从加载 `BattleScene` 改为打开 `RoomPage`。
- [x] 4.2 保持未登录点击 `Start Game` 返回或停留在 `LoginPage`，并确认不会发送房间请求。
- [x] 4.3 登录或会话恢复成功后，如存在最近 room id，允许玩家手动触发房间重连恢复。
- [x] 4.4 主动退出房间成功后返回房间大厅入口或 `HomePage`，并清理当前房间快照。

## 5. 文档与规格同步

- [x] 5.1 更新 `docs/client-integration.md`，补充 Unity 房间大厅端到端联调步骤。
- [x] 5.2 更新 `docs/client-architecture.md`，记录 `RoomSystem` 和 `RoomPage` 职责边界。
- [x] 5.3 更新 `docs/roadmap.md` 和必要的 README，说明 `add-unity-room-lobby-flow` 当前状态和下一步。
- [x] 5.4 确认不新增协议消息、MySQL migration 或 Redis key；如实现中发现必须新增，先暂停并更新 OpenSpec artifacts。

## 6. 验证

- [x] 6.1 运行 `cmd /c server\scripts\test.bat`，确认服务端和协议生成未被破坏。
- [x] 6.2 运行 `cmd /c openspec validate add-unity-room-lobby-flow --strict`。
- [x] 6.3 在 Unity Editor 中确认项目无 C# 编译错误，`RoomPage` prefab 引用完整。
- [x] 6.4 使用本地服务端验证单玩家创建房间、准备/取消准备和退出房间路径。
- [x] 6.5 使用两个玩家连接验证加入房间、成员列表刷新和房主转移路径。
- [x] 6.6 验证断线后在保留期内发送 `ReconnectRoomRequest` 能恢复房间身份，保留期外失败时能清理本地上下文。
- [x] 6.7 记录暂未自动化的 Unity 验证项和后续 `add-room-start-gate` 边界。
