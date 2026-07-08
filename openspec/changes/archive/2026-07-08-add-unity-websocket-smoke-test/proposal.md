## Why

服务端已经具备 WebSocket、Protobuf envelope、账号会话和心跳能力，Unity 客户端也已经通过 `NetworkSystem` 接入真实账号协议。但当前 Unity 侧缺少一个稳定、可重复的最小联调入口，开发者需要手动进入运行流程并观察 UI 才能确认 `/ws`、心跳和账号请求是否可用。

在继续推进完整房间大厅 UI 之前，需要先补齐 Unity 编辑器内的 WebSocket smoke test，用最小成本验证本地服务端、共享 Protobuf 生成代码、二进制 envelope 和账号会话链路。

## What Changes

- 新增 Unity Editor 菜单工具，用于在编辑器内发起本地 WebSocket smoke test。
- smoke test 连接 `AppConfig.WebSocketURL`，发送 `HeartbeatRequest` 并校验 `HeartbeatResponse`。
- smoke test 发送账号注册请求；若测试账号已存在，则回退到登录请求，确保重复执行不会持续创建新账号。
- smoke test 成功获得 session 后发送登出请求，避免本地 Redis 留下长期测试会话。
- 更新 Unity 接入文档，记录菜单入口、前置条件、成功路径和失败排查边界。
- 不实现房间大厅 UI，不发送房间请求，不改变现有 `HomePage.Start Game` 流程。
- 不新增协议字段、服务端接口、MySQL 表或 Redis key。

## Capabilities

### Modified Capabilities

- `client-integration`: 增加 Unity 编辑器内最小 WebSocket/账号 smoke test 的长期验收要求。

## Impact

- 影响客户端：新增 `client/Assets/App/Scripts/Editor/` 下的编辑器联调工具和 Editor assembly。
- 影响文档：更新 `docs/client-integration.md`，明确 smoke test 入口和验收步骤。
- 不影响服务端 runtime、共享协议、数据库 schema 和 Redis key。
