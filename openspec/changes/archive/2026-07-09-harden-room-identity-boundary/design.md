## Context

账号会话 change 已经让 gateway 在注册、登录和会话恢复成功后，通过 `BindIdentity` 将 `PlayerID` 和 session token 绑定到 WebSocket connection session。房间大厅 change 早于完整账号接入，协议 payload 仍显式携带 `player_id`，当前 room dispatcher 直接把该字段传给 room service。

这形成了一个安全边界缺口：正常 Unity 客户端会用 `AccountSystem.CurrentPlayerID` 填充 `player_id`，但服务端不能把客户端 payload 当作最终身份事实。后续 `add-room-start-gate` 会基于房主和成员准备状态判断是否允许进入占位场景，因此必须先让房间操作以服务端确认的 connection identity 为准。

## Goals / Non-Goals

**Goals:**

- 房间大厅请求必须先通过 connection session 身份校验。
- payload `player_id` 与 session `PlayerID` 不一致时拒绝请求。
- 未登录 connection 发送房间大厅请求时返回结构化 `UNAUTHENTICATED` 错误。
- 测试覆盖未登录、身份不一致和正常已登录房间路径。

**Non-Goals:**

- 不删除或变更现有 Protobuf 房间请求中的 `player_id` 字段。
- 不新增 message id、协议版本或客户端协议生成。
- 不实现房间持久化、匹配系统、开始游戏闸门或 battle server。
- 不把 room service 改为直接依赖 gateway session；身份校验仍留在 app 分发边界。

## Decisions

### 在 room dispatcher 中校验身份

房间请求的授权校验放在 `server/internal/app/room_dispatcher.go`。该层同时可见 gateway `SessionSnapshot` 和已解码的房间 payload，是最小且明确的跨模块边界。

替代方案：

- 在 gateway 统一拦截所有 room message id：可以拒绝未登录，但无法不解码 payload 就比较 `player_id`，会把业务 payload 细节泄漏到 gateway。
- 在 room service 内校验 session：会让 room 业务层依赖传输连接身份，破坏当前 Room 可无网络测试的边界。

### 保留 payload `player_id`，但不信任它

本 change 不做破坏性协议变更。服务端仍读取 payload `player_id`，但只把它作为“客户端声明身份”，并要求它等于 session `PlayerID`。后续如果要移除显式 `player_id`，应创建独立协议兼容 change，处理字段废弃、reserved 和双端生成。

替代方案：

- 立即删除 `player_id`：会触发 Protobuf 兼容和 Unity 调整，扩大本 change 范围。
- 完全忽略 payload `player_id` 并改用 session `PlayerID`：能防伪造，但会掩盖客户端状态错乱；显式拒绝不一致更利于诊断。

### 错误码复用现有账号态错误

未登录请求返回 `UNAUTHENTICATED`。身份不一致目前协议没有专用 `PERMISSION_DENIED` 错误码，因此复用 `UNAUTHENTICATED` 表达“当前连接身份不能代表 payload 玩家”。detail 中保留可诊断原因，但不泄漏敏感 token。

替代方案：

- 新增 `PERMISSION_DENIED` error code：语义更精确，但需要协议 schema 和双端生成，超过本 change 的最小修复范围。
- 使用 `PAYLOAD_INVALID`：能表示请求非法，但会弱化这是身份边界拒绝。

## Risks / Trade-offs

- [Risk] 现有服务端房间 WebSocket 测试未登录直接发 room 请求会失败。  
  Mitigation: 测试先通过注册建立 connection 身份，另补未登录拒绝用例。

- [Risk] Unity 本地缓存玩家身份与服务端 session 不一致时，房间请求会失败。  
  Mitigation: 客户端已有会话恢复失败清理路径；错误响应会让 UI 保持当前房间状态，不写入错误快照。

- [Risk] 复用 `UNAUTHENTICATED` 覆盖身份不一致不够细。  
  Mitigation: 作为第一阶段安全收口接受该取舍；未来如需要更细权限码，单独做协议兼容 change。

## Migration Plan

无需数据迁移、Redis key 迁移或 MySQL schema 迁移。部署后，未登录或伪造 `player_id` 的房间请求会从“可能修改房间状态”变为结构化拒绝；正常 Unity 客户端路径不需要修改。

回滚策略是恢复 room dispatcher 对 payload `player_id` 的直接转发，但不建议回滚该身份边界收紧。

## Open Questions

无。未来是否移除房间请求 payload `player_id`，应由独立协议 change 决定。
