## 1. 协议与生成

- [x] 1.1 在 `shared/proto/realtime/v1/envelope.proto` 中新增账号会话 message id、`PlayerProfile`、注册、登录、登出、恢复会话和查询当前身份消息。
- [x] 1.2 补充账号会话错误码或 detail 约定，并确保所有账号请求失败复用 `ErrorResponse`。
- [x] 1.3 运行协议生成脚本，更新 Go 协议生成代码。
- [x] 1.4 更新 `server/internal/protocol` 的消息注册、解码和版本兼容测试。

## 2. Storage 边界

- [x] 2.1 新增玩家基础资料 MySQL migration up/down 文件。
- [x] 2.2 在 `server/internal/storage` 新增玩家资料 repository interface、类型定义和 fake adapter。
- [x] 2.3 在 `server/internal/storage` 新增账号 session token cache interface、类型定义和 fake adapter。
- [x] 2.4 新增 Redis `account:session` key builder、TTL 常量和 key 单元测试。
- [x] 2.5 为玩家资料 repository 和 session cache 增加 storage 单元测试。

## 3. 服务端账号会话

- [x] 3.1 创建 `server/internal/account` 包，定义注册、登录、登出、恢复会话和当前身份请求/响应类型。
- [x] 3.2 实现 account service 的注册、输入校验、玩家资料查询、session token 签发、恢复和失效逻辑。
- [x] 3.3 为 account service 添加表驱动单元测试，覆盖注册成功、重复注册、登录成功、凭据非法、恢复成功、token 过期和登出失效。
- [x] 3.4 在 `server/internal/app` 新增 account dispatcher，将账号协议 payload 转换为 service 调用。

## 4. Gateway 集成

- [x] 4.1 扩展 gateway session snapshot，支持已登录 `player_id` 和 session token 摘要。
- [x] 4.2 在登录或恢复成功后绑定 gateway session 身份。
- [x] 4.3 在登出、连接关闭或 session 失效后清理 gateway session 身份。
- [x] 4.4 增加 gateway/account 集成测试，覆盖登录后 session 绑定和登出后业务身份清理。

## 5. Unity 客户端接入

- [x] 5.1 新增或补齐 `NetworkSystem`，支持 `/version`、WebSocket 二进制 envelope、pending request、心跳和错误分发的最小能力。
- [x] 5.2 将 `AccountSystem` 改为通过 `NetworkSystem` 调用注册、登录、登出和恢复会话，并移除本地假登录路径。
- [x] 5.3 更新 `LoginPage`，注册或登录成功后进入 `HomePage`，失败时保持未登录并显示错误。
- [x] 5.4 更新 `HomePage`，登出清理账号状态并返回 `LoginPage`，`Start Game` 在未登录时不得进入联机流程。
- [x] 5.5 增加客户端本地 session 保存和恢复入口；恢复失败时清理本地 token。
- [x] 5.6 加固 `NetworkSystem` 的连接超时、请求超时、WebSocket 异常包装、错包诊断、服务端推送区分、payload 解码错误和关闭清理流程。
- [x] 5.7 区分 Unity 生命周期强制中止和玩家登出优雅断开，并补充网络关闭与账号结果边界注释。
- [x] 5.8 为 Unity `NetworkSystem` 接入客户端心跳循环，复用 envelope 请求通道并记录 RTT 与服务端时间。

## 6. 文档与验证

- [x] 6.1 更新 `docs/architecture.md`、`docs/client-architecture.md`、`docs/client-integration.md`、`docs/roadmap.md`、`docs/protocol-compatibility.md`、`docs/redis-keys.md` 和 `docs/file-structure.md`。
- [x] 6.2 更新 `server/README.md` 和 `client/README.md` 的本地联调步骤。
- [x] 6.3 运行 `go test ./...`，确认服务端测试通过。
- [x] 6.4 在 Unity 编辑器中验证启动、注册失败、注册成功、登录失败、登录成功、登出和恢复失败路径。
- [x] 6.5 记录暂未自动化的 Unity 验证项和后续房间大厅接入 change 名称。

## 7. 真实账号存储 runtime

- [x] 7.1 为服务端新增 MySQL password、连接池参数、Redis password、Redis DB 和连接超时配置及环境变量覆盖。
- [x] 7.2 引入 MySQL driver 和 Redis client 依赖，并保持业务层不直接依赖具体 client。
- [x] 7.3 更新 `server/config/local.yaml` 与 README，明确本地真实 MySQL/Redis 启动要求。
- [x] 7.4 实现 MySQL `PlayerProfileRepository`，覆盖创建、按账号查询、按 player id 查询和错误映射。
- [x] 7.5 实现 Redis `AccountSessionCache`，覆盖 JSON 编码、TTL 写入、读取、删除和 Redis miss 映射。
- [x] 7.6 为 MySQL 账号 repository 增加 sqlmock 或等价单元测试。
- [x] 7.7 为 Redis account session cache 增加 redismock 或等价单元测试。
- [x] 7.8 在 server runtime 中创建 MySQL/Redis client，启动时 ping 依赖，失败时拒绝启动。
- [x] 7.9 将 account service runtime 依赖切换为真实 MySQL repository 和 Redis session cache，移除账号路径 `storage.NewFakeStore()` wiring。
- [x] 7.10 确保 HTTP server shutdown 时关闭 MySQL/Redis client，避免连接池泄漏。
- [x] 7.11 保留 fake/in-memory storage 仅用于单元测试，并更新相关测试命名避免误解为 runtime 方案。
- [x] 7.12 更新架构、文件结构、路线图、Redis key 和本地联调文档，说明账号存储已去 fake，房间持久化另行推进。
- [x] 7.13 运行 `go test ./...`，确认服务端测试通过。
- [x] 7.14 运行 `openspec validate add-account-session --strict`。
- [x] 7.15 沉淀本地 MySQL/Redis 建库、授权、迁移、检查和排障命令，并将命令必须项目化写入长期工程规则。

## 8. 协议生成产物规则

- [x] 8.1 将 Unity C# Protobuf 输出目录统一为 `client/Assets/App/Scripts/Protocol/Pb/Realtime/V1`，与服务端 `server/internal/protocol/pb/realtime/v1` 保持 protocol/pb 语义一致。
- [x] 8.2 更新 `.gitignore`，让客户端和服务端 Protobuf 生成产物都作为可再生成文件忽略。
- [x] 8.3 更新协议生成文档，明确双端生成代码都不得手工修改且不提交到 Git。
- [x] 8.4 补充 SQL IDE 执行 MySQL 本地命令的说明，明确 `SOURCE` 仅适用于 mysql 命令行客户端。

## 9. 数据注释规则

- [x] 9.1 为第一阶段 MySQL migration 的表和字段补充中文 `COMMENT`。
- [x] 9.2 将 MySQL 表/字段中文注释和 Redis key 中文说明要求写入长期工程规则。
