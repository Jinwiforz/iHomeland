## Context

当前服务端已经实现房间大厅协议、room service、gateway dispatcher 和 Go 集成测试。Unity 客户端已经具备 `NetworkSystem`、真实 `AccountSystem`、基础页面流转和 Editor smoke test，但 `HomePage.Start Game` 仍直接加载 `BattleScene`，玩家无法在 Unity 内完成创房、进房、准备、退房、房主转移和重连恢复。

第一里程碑目标是“自定义房间大厅”，因此本 change 应把 Unity 联机入口收敛到房间大厅 flow，而不是继续推进战斗场景或 battle server。

## Goals / Non-Goals

**Goals:**

- 让已登录玩家从 `HomePage.Start Game` 进入房间大厅入口，不再直接进入 `BattleScene`。
- 为 Unity 新增房间客户端状态边界，保存当前 `RoomSnapshot`、本地房间上下文和操作中状态。
- 为 `NetworkSystem` 增加创建、加入、准备/取消准备、退出、房主转移和重连恢复请求方法。
- 新增房间大厅 UI 页面，展示服务端快照并提供第一阶段房间操作入口。
- 使用服务端 `RoomSnapshot` 作为 UI 和客户端房间状态的唯一事实来源。
- 支持使用本地保存的 room id 与当前登录玩家身份尝试 `ReconnectRoomRequest`。
- 更新文档和验收步骤，确保 Unity 侧能手动完成最小端到端房间大厅联调。

**Non-Goals:**

- 不新增或修改 Protobuf schema、message id 或服务端房间协议。
- 不在本 change 中移除房间请求 payload 的显式 `player_id`；Unity 先使用服务端确认的 `CurrentPlayerID` 填充现有字段。
- 不实现匹配系统、房间列表、搜索、邀请、聊天、观战、回放或完整社交系统。
- 不实现“开始游戏”闸门，不从房间进入正式战斗模拟；该部分由 `add-room-start-gate` 承接。
- 不新增 MySQL migration、Redis key 或房间持久化 runtime adapter。

## Decisions

### 新增 RoomSystem 管理客户端房间状态

房间状态不应塞进 `HomePage` 或 `RoomPage`。新增 `RoomSystem` 或等价系统，由 `AppRoot` 创建并按统一生命周期初始化，负责调用 `NetworkSystem`、保存当前 `RoomSnapshot`、派发状态变化、记录最近 room id 和处理退出登录时的清理。

替代方案是让 `RoomPage` 直接调用 `NetworkSystem` 并保存所有状态。该方案短期更快，但会把 UI 与房间业务状态耦合，后续加入断线恢复、房主转移和开始游戏闸门时会难以维护。

### NetworkSystem 只扩展协议请求方法

`NetworkSystem` 继续负责 WebSocket、envelope、request/response、错误包装和 payload 解码。房间方法应保持薄封装，例如 `CreateRoomAsync`、`JoinRoomAsync`、`SetReadyAsync`、`LeaveRoomAsync`、`TransferHostAsync`、`ReconnectRoomAsync`，返回服务端响应或抛出 `NetworkRequestException`。

替代方案是在 `RoomSystem` 自行构造 envelope。该方案会绕过已有网络边界，重复 request id、timeout、socket 清理和错误处理逻辑。

### 继续使用显式 player_id，但来源必须是 AccountSystem

现有服务端房间协议仍要求 `player_id` 字段。Unity 发送房间请求时必须从 `AccountSystem.CurrentPlayerID` 读取，不允许 UI 输入或临时字符串充当玩家身份。后续如果服务端房间请求改为从 gateway session 读取身份，应创建单独 change 更新协议和兼容策略。

### RoomSnapshot 是唯一事实来源

客户端点击按钮后只进入 pending 状态，不能直接把本地按钮结果写成最终房间状态。只有服务端响应或服务端推送携带的 `RoomSnapshot` 可以刷新 `RoomSystem.CurrentRoom` 和 UI。

### 本地房间上下文只保存重连所需最小信息

客户端可以通过 `PlayerPrefs` 或当前项目已有本地状态入口保存最近 room id 和 player id，用于启动或重新登录后尝试 `ReconnectRoomRequest`。保存内容不得包含可伪造权限或房间成员状态；Redis 重连资格仍由服务端判断。

替代方案是保存完整 `RoomSnapshot`。该方案容易让 UI 展示过期状态，且无法代表服务端重连资格。

### UI 使用现有 prefab 与 UISystem 模式

新增 `RoomPage` 和对应 prefab，注册到 `AppPages`，由 `UISystem` 加载。`RoomPage` 是 `Start Game` 后的主流程页面，承载未进房入口和已进房快照展示；创建房间、输入 room id 加入、房主转移和退出确认可以实现为页面内子面板或轻量对话框，不要求拆成独立 page。复杂美术、动画和正式玩法入口不在本 change 内完成。

## Risks / Trade-offs

- [Risk] Unity 当前 `NetworkSystem` 同时用 `_requestLock` 串行心跳和业务请求，房间操作期间心跳可能延迟。→ Mitigation：第一阶段房间操作频率低，可接受串行请求；若后续需要并发 pending request，再单独设计请求分发。
- [Risk] 服务端房间请求仍显式携带 `player_id`，客户端存在伪造字段的协议表面。→ Mitigation：本 change 只从服务端确认的账号状态读取 `CurrentPlayerID`；服务端去除显式 `player_id` 另开 change。
- [Risk] Unity prefab 手工搭建容易漏引用。→ Mitigation：任务中要求在 Unity Editor 中手动验证创房、进房、准备、退房和重连路径，并记录未自动化项。
- [Risk] 单客户端难以完整验证加入和房主转移。→ Mitigation：验收步骤要求使用两个 Unity 实例或一个 Unity 实例加协议/服务端测试客户端进行双玩家联调。
- [Risk] 房间快照推送当前 `NetworkSystem` 只会在等待响应时跳过无 request id push，无法主动分发。→ Mitigation：本 change 优先以请求响应刷新 UI；如需要实时 push 分发，任务中单独评估并在不改变协议的前提下增加运行时接收循环，避免混进业务 UI。

## Migration Plan

1. 扩展 Unity `NetworkSystem` 房间请求方法，复用现有 envelope 请求通道。
2. 新增 `RoomSystem` 并接入 `AppRoot` 生命周期。
3. 新增 `RoomPage`、`AppPages.RoomPage` 和对应 prefab。
4. 将 `HomePage.Start Game` 改为打开房间大厅入口。
5. 实现创房、进房、准备/取消准备、退出、房主转移和重连恢复 UI 操作。
6. 更新文档和本地验收步骤。

回滚时可以移除 `RoomSystem`、房间大厅页面和 `NetworkSystem` 房间方法，并把 `HomePage.Start Game` 恢复到当前占位行为；服务端协议、数据库和 Redis 不需要回滚。

## Open Questions

- 房间大厅第一版是否只提供“创建房间”和“输入 room id 加入”，还是需要额外提供本地最近房间快捷重连按钮？
- 双玩家 Unity 联调是否使用两个 Editor/Player 实例，还是先补一个 Editor 菜单工具作为第二客户端？
