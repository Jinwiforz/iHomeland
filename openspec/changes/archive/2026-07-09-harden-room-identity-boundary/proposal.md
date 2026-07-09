## Why

当前 gateway 已经在注册、登录或会话恢复成功后把玩家身份绑定到 WebSocket connection session，但房间大厅分发仍直接信任请求 payload 中的 `player_id`。这会让异常或恶意客户端绕过已登录身份，伪造其他玩家执行创房、加入、准备、退出、房主转移或重连请求。

该问题位于账号会话到房间大厅的安全边界上，应在推进 `add-room-start-gate` 前收口，避免后续开始游戏闸门建立在可伪造的房间身份之上。

## What Changes

- 房间大厅请求必须要求 gateway connection 已绑定服务端确认的玩家身份。
- 房间大厅请求 payload 中的 `player_id` 必须与 connection session 中的 `PlayerID` 完全一致。
- 未登录 connection 发送房间大厅请求时，服务端必须返回结构化 `UNAUTHENTICATED` 错误。
- 已登录 connection 使用不一致的 `player_id` 时，服务端必须返回结构化权限错误，不得调用 room service 修改房间状态。
- 保留现有房间协议 payload `player_id` 字段，本 change 不做协议字段删除或破坏性变更。
- 补充 WebSocket 集成测试覆盖未登录请求、伪造 `player_id` 和正常已登录房间请求路径。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `gateway`: 房间业务分发必须以 connection session 中的服务端身份作为授权边界。
- `room`: 房间大厅操作必须拒绝未登录或请求身份与 session 身份不一致的调用。
- `account-session`: 登录、注册和会话恢复绑定到 connection 的玩家身份必须成为后续房间请求的身份来源。

## Impact

- 影响服务端 `server/internal/app` 中 realtime/room dispatcher 的授权校验。
- 影响服务端 WebSocket 集成测试，测试房间请求前需要建立账号会话或显式验证未登录拒绝。
- 不新增 Redis key、MySQL schema、Protobuf 字段或 message id。
- 不影响 Unity 客户端正常路径；客户端仍使用 `AccountSystem.CurrentPlayerID` 填充房间请求 payload。
