## Why

客户端已经具备 `MainScene -> LoadingPage -> LoginPage -> HomePage -> LoadingPage -> BattleScene` 的基础链路，但当前登录仍是客户端 fake 状态，服务端房间请求也仍依赖客户端显式传入 `player_id`。在接入完整房间大厅前，需要先建立最小账号会话能力，让登录、登出、连接身份和后续房间操作有统一可信来源。

本 change 是第一里程碑“自定义房间大厅”的前置身份层，不进入战斗模块。

## What Changes

- 新增第一阶段账号会话能力：注册、登录、登出、会话恢复、当前身份查询和服务端会话失效处理。
- 新增账号会话协议消息，使用 `1000-1999` 号段，并记录 owner 为 `account`。
- 服务端新增 `account` 业务边界，负责账号注册、输入校验、玩家身份生成或查询、会话 token 签发与失效。
- Gateway session 支持绑定已登录玩家身份，后续房间请求可从连接会话读取玩家身份。
- Storage 新增最小玩家资料和会话持久/运行态边界：MySQL 保存玩家基础资料，Redis 保存短期 session token。
- 账号 runtime 使用真实 MySQL `PlayerProfileRepository` 和真实 Redis `AccountSessionCache`，不再通过 fake/in-memory storage 承载注册、登录、登出或会话恢复。
- 新增 MySQL/Redis 真实 client wiring、配置、启动 ping、错误映射和生命周期关闭逻辑；fake/in-memory storage 仅保留给单元测试。
- Unity 客户端将 `AccountSystem` 改为调用服务端注册、登录和登出，并持有当前 player/session 状态。
- 更新文档与规格，明确账号会话是房间大厅端到端接入前置 change。
- 不实现密码找回、第三方登录、复杂权限、好友、背包、经济、战绩、匹配、观战、回放或 battle server。
- 不把 `HomePage.Start Game` 直接推进到战斗模拟；后续应改为进入房间大厅或创建/加入房间流程。

## Capabilities

### New Capabilities

- `account-session`: 定义第一阶段账号注册、登录、登出、会话恢复、玩家身份、session token 和客户端账号状态的长期行为契约。

### Modified Capabilities

- `protocol`: 新增账号会话消息 ID、请求/响应结构和错误兼容要求。
- `gateway`: 网关 session 需要支持登录身份绑定、会话恢复和登出后的身份清理。
- `storage`: 新增玩家基础资料和短期账号 session 的持久化/运行态边界，并要求账号 runtime 使用真实 MySQL/Redis adapter。
- `server-foundation`: 服务端启动配置、依赖 wiring 和关闭流程必须支持真实 MySQL/Redis client。
- `client-integration`: Unity 客户端接入流程更新为服务端注册、登录和登出，并将登录身份作为房间接入前置条件。

## Impact

- 影响服务端：`server/internal/account`、`server/internal/app`、`server/internal/gateway`、`server/internal/protocol`、`server/internal/storage`、MySQL migration、Redis key builder、相关测试。
- 影响服务端基础设施：`server/internal/config`、HTTP server runtime wiring、MySQL/Redis client lifecycle 和本地配置。
- 影响依赖：新增 Go MySQL driver 和 Redis client。
- 影响共享协议：`shared/proto/realtime/v1/envelope.proto` 新增账号会话消息和 message id。
- 影响客户端：`client/Assets/App/Scripts/Systems/AccountSystem.cs`、后续 `NetworkSystem`、登录页、主页和本地 session 保存策略。
- 影响配置：MySQL/Redis 需要密码、DB、连接池/超时等上线级配置，并可通过环境变量覆盖。
- 影响文档：`docs/architecture.md`、`docs/client-architecture.md`、`docs/client-integration.md`、`docs/roadmap.md`、`docs/protocol-compatibility.md`、`docs/redis-keys.md`、`docs/file-structure.md`、`server/README.md`。
