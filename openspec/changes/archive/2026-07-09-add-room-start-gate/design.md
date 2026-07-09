## Context

项目当前已经完成第一阶段房间大厅的主要操作：账号会话、gateway 身份绑定、房间请求身份校验、创建房间、加入房间、准备/取消准备、退出、房主转移、断线保留和重连恢复。Unity 侧 `Start Game` 已进入 `RoomPage`，但房间内还没有“房主发起开始并由服务端确认”的闸门。

这个缺口会影响第一里程碑闭环：玩家可以在大厅内准备，却无法由服务端判断房间是否允许离开大厅。该 change 只补齐开始闸门，不定义正式战斗。

## Goals / Non-Goals

**Goals:**

- 在 room 状态机中新增第一阶段开始闸门。
- 只有当前房主可以请求开始房间。
- 房间必须处于可开始状态，成员准备和连接状态必须满足要求。
- 开始成功后返回最新 `RoomSnapshot`，让客户端能进入后续占位场景或展示开始成功状态。
- 补齐 Protobuf 消息、服务端分发、Unity 调用和基础 UI 入口。

**Non-Goals:**

- 不实现 battle server。
- 不实现高频战斗同步、服务端权威模拟、输入校验、战斗结算、观战或回放。
- 不实现匹配系统或房间列表。
- 不新增 Redis key、MySQL schema 或跨进程房间一致性。
- 不改变房间请求仍携带 `player_id` 的兼容策略。

## Decisions

### 开始闸门仍属于 room 状态机

开始房间是大厅状态迁移，不是战斗系统入口。它应作为 room model/service 的显式状态机方法实现，复用现有权限、不变量和快照返回模式。

替代方案：

- 在 Unity 客户端本地判断是否能开始：会绕过服务端权威和房主权限，不可接受。
- 直接加载 `BattleScene`：会把第一阶段大厅闭环和未来战斗架构耦合，不符合边界。

### 新增第一阶段占位状态，不引入战斗状态机

开始成功后房间进入一个“已开始/占位”状态，用于表达大厅闸门已经通过。该状态只阻止继续执行加入、准备、房主转移等大厅操作，不承载战斗 tick、输入、结算或对局事实。

替代方案：

- 继续使用 `open` 状态并只返回成功：状态不可追踪，客户端和后续逻辑难以区分大厅是否已开始。
- 引入完整 battle 状态：会扩大第一里程碑范围。

### 准备规则采用最小可解释版本

房主可以发起开始；除房主外的在线成员必须全部 ready；断线成员不能被视为 ready。房主自身不强制 ready，因为房主点击开始本身即表达发起意图。

替代方案：

- 要求房主也 ready：流程更严格，但会增加一次多余操作。
- 允许断线成员保留 ready：容易造成玩家不在场却进入后续流程。

### 协议新增 StartRoom 请求/响应

新增 `StartRoomRequest` 和 `StartRoomResponse`，继续使用 room 号段并携带 `player_id`、`room_id` 和响应 `RoomSnapshot`。服务端 room dispatcher 继续校验 gateway session 身份与 payload `player_id` 一致。

替代方案：

- 复用 `SetReadyRequest` 或其他已有消息：语义混乱，message id 追踪不清。
- 先做 HTTP API：会绕开实时 envelope 和现有客户端连接状态。

## Risks / Trade-offs

- [Risk] 成功后客户端进入的只是占位场景，玩家可能误以为正式战斗已完成。  
  Mitigation: 文档和 UI 文案明确这是第一阶段开始闸门，不代表 battle server 或正式战斗。

- [Risk] 房主不强制 ready 可能与部分游戏习惯不同。  
  Mitigation: 该规则简单且符合“房主点击开始即确认”的第一阶段取舍；后续可通过独立 change 调整准备规则。

- [Risk] 新增协议消息需要双端生成。  
  Mitigation: 继续使用唯一协议生成入口 `tools/proto/generate.bat`，并补服务端和 Unity 编译/联调说明。

## Migration Plan

无需数据迁移。实现时先扩展 `.proto` 和生成代码，再补服务端状态机/dispatcher，最后接入 Unity 请求和 UI 按钮。老客户端没有发送 `StartRoomRequest` 时行为不变；新客户端需要重新生成协议代码。

## Open Questions

无。未来正式 battle server 的 tick rate、权威模拟、结算、观战和回放仍需独立 change 设计。
