## Context

服务端已经具备 HTTP 基础接口、Protobuf envelope、WebSocket 网关和业务消息分发边界，但还没有任何房间业务。第一里程碑的目标是“自定义房间大厅”，因此本 change 需要在服务端进程内实现房间模型、状态机和实时协议消息，让客户端能够验证创建房间、加入房间、准备、退出、房主转移和断线重连恢复身份的核心链路。

当前阶段不接入 MySQL/Redis 作为房间事实来源。房间服务先使用进程内内存 repository，保证状态机和协议行为可测试；后续 `add-persistence-boundaries` 再定义 Redis presence、reconnect token、room index 和 MySQL 摘要等边界。

## Goals / Non-Goals

**Goals:**

- 在 `server/internal/room` 中实现房间模型、成员模型、显式状态机、服务接口和内存 repository。
- 支持创建房间、加入房间、准备/取消准备、退出房间、房主转移、房间解散、断线保留和重连恢复。
- 新增房间大厅 Protobuf 消息和 message id，复用现有 envelope、request id、错误响应和 WebSocket gateway。
- 将 room service 作为 gateway 的 `Dispatcher` 接入点，保持网关不直接修改房间状态。
- 用表驱动测试覆盖状态迁移、权限、不变量、重复成员、断线重连和协议请求响应。

**Non-Goals:**

- 不实现匹配系统、排队、自动组队或跨房间分配。
- 不实现 MOBA/RTS 高频战斗模拟、独立 battle server、观战或回放。
- 不实现完整持久化，不新增 MySQL schema，不新增 Redis key。
- 不实现账号系统；玩家身份先来自请求中的 `player_id`，后续再由 account/session change 绑定鉴权身份。
- 不新增 TCP 传输，不拆分 gRPC 服务。

## Decisions

### 1. 房间逻辑放在 `server/internal/room`，网关只分发

`room.Service` 暴露以业务语义命名的方法，例如 `CreateRoom`、`JoinRoom`、`SetReady`、`LeaveRoom`、`TransferHost`、`DisconnectMember` 和 `ReconnectMember`。gateway dispatcher 只负责把协议消息转换为 service 调用并返回 room snapshot，不直接读写房间内部状态。

替代方案是在 gateway 中 switch 并修改房间状态。该方案会让传输层承载业务状态机，违反现有 gateway 规格。

### 2. 使用显式状态机表达房间生命周期

房间生命周期先定义为 `Open`、`Closed` 两类最小状态，成员连接状态定义为 `Online`、`Disconnected`。状态迁移集中在 room 包内完成，所有操作必须校验当前状态、成员身份、房主权限、容量、重复成员和重连资格。

替代方案是用松散 boolean 字段直接判断。该方案短期代码少，但很容易在准备、退出、房主转移和重连之间留下隐式不变量。

### 3. 第一阶段使用内存 repository

房间 repository 先使用单进程内存 map，并通过 mutex 保护并发访问。它是服务端进程内事实来源，只用于第一阶段验证大厅业务。断线保留只在进程存活期间有效，恢复路径由后续持久化 change 定义。

替代方案是立即接入 Redis/MySQL。该方案会把 room lobby 与 persistence boundaries 混成一个过大的 change，并迫使当前阶段提前定义 Redis key 和 MySQL schema。

### 4. 房间快照作为协议响应主体

房间操作成功后返回统一 `RoomSnapshot`，包含 room id、房主、成员、座位、阵营、准备状态、连接状态和房间状态。客户端可以用同一结构刷新 UI；服务端错误继续复用 `ErrorResponse`。

替代方案是每个操作返回不同结构。该方案能减少部分字段，但会让客户端 UI 同步和测试断言更分散。

### 5. 房间消息使用 `2000-2999` 号段

房间大厅消息分配 `2000-2019` 的稳定 message id，owner 为 `room`。建议初始分配：

- `2000` `CreateRoomRequest`
- `2001` `CreateRoomResponse`
- `2002` `JoinRoomRequest`
- `2003` `JoinRoomResponse`
- `2004` `SetReadyRequest`
- `2005` `SetReadyResponse`
- `2006` `LeaveRoomRequest`
- `2007` `LeaveRoomResponse`
- `2008` `TransferHostRequest`
- `2009` `TransferHostResponse`
- `2010` `ReconnectRoomRequest`
- `2011` `ReconnectRoomResponse`
- `2012` `RoomSnapshotPushed`

替代方案是先用实验号段 `9000+`。本 change 是第一里程碑正式功能，应该直接使用正式 room 号段并记录 owner。

### 6. 玩家身份先来自协议字段，后续再接入鉴权

在没有 account/session change 前，请求消息必须携带 `player_id`。room service 使用 `player_id` 做成员去重和权限校验。后续接入鉴权后，gateway 可以用已认证身份覆盖或校验请求中的 `player_id`。

## Risks / Trade-offs

- [Risk] 内存 repository 在进程重启后丢失房间和重连资格。→ 明确记录为第一阶段限制；持久化和 Redis 恢复路径放到 `add-persistence-boundaries`。
- [Risk] 未接入账号系统时 `player_id` 可被客户端伪造。→ 当前只用于本地链路验证；后续 account/session change 必须绑定认证身份。
- [Risk] 状态机范围膨胀到游戏开始或战斗。→ 本 change 只做到房间大厅，不定义 battle server 或高频战斗状态。
- [Risk] 协议消息一次新增较多。→ 只新增大厅闭环所需消息，使用统一 `RoomSnapshot` 降低响应类型复杂度。
- [Risk] 并发操作可能导致房主转移或成员状态不一致。→ repository 更新必须在同一锁保护下执行，状态机测试覆盖并发敏感不变量。

## Migration Plan

1. 扩展 Protobuf schema 并重新生成 Go 协议代码。
2. 在 `protocol` 包注册 room message id 和 payload 类型。
3. 新增 `room` 包模型、状态机、service 和内存 repository。
4. 在 gateway/app 组装层接入 room dispatcher。
5. 补充 room 和 gateway 协议测试，确认现有健康检查、版本接口和 WebSocket 心跳不回归。

## Open Questions

- 房间默认容量建议先用配置或常量限定为小规模大厅，例如 2-8 人；具体默认值可在实现阶段按测试和配置结构确定。
- 阵营和座位的自动分配策略先保持简单确定性；更复杂的队伍规则可以在后续玩法需求明确后单独设计。
