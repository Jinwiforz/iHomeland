## Context

当前客户端已经有 `LoginPage`、`HomePage` 和 `AccountSystem`，但登录只在本地校验非空账号密码并设置 fake 状态。服务端已有 WebSocket gateway、Protobuf envelope、房间大厅 dispatcher 和 storage 边界，但没有账号模块，房间请求仍要求客户端显式携带 `player_id`。

下一阶段要接入完整房间大厅前，必须先让客户端获得服务端确认的玩家身份，并让 gateway session 能保存该身份。否则创房、进房、退房、房主转移和断线重连都会继续依赖不可信的本地输入。

## Goals / Non-Goals

**Goals:**

- 提供第一阶段最小账号会话：注册、登录、登出、会话恢复、当前身份查询。
- 让服务端生成或查询稳定 `player_id`，并通过 session token 关联客户端会话。
- 让 gateway session 能绑定 `player_id`，为后续房间请求去除显式 `player_id` 做准备。
- 为 Unity `AccountSystem` 提供真实服务端注册、登录和登出入口。
- 补充账号协议、MySQL 玩家基础资料和 Redis session token 边界。
- 账号资料 runtime 使用真实 MySQL repository，账号 session runtime 使用真实 Redis cache。
- server 启动时根据配置创建并注入 MySQL/Redis client，关闭时释放依赖。
- fake/in-memory storage 仅保留给单元测试和无外部依赖测试。

**Non-Goals:**

- 不实现密码找回、邮箱/手机号验证、第三方登录、多角色体系或复杂权限。
- 不实现好友、背包、经济、战绩、匹配、观战、回放或 battle server。
- 不把账号 session 做成独立微服务；第一阶段仍在单进程内通过 Go interface 组合。
- 不在本 change 完成完整房间 UI，房间大厅接入由后续 change 承接。
- 不实现房间大厅 MySQL/Redis 正式 adapter。
- 不改变已定义的客户端账号协议字段和 message id。

## Decisions

### 账号模块采用进程内服务边界

新增 `server/internal/account`，暴露最小 service interface。`gateway` 和 `app` 只通过接口调用账号服务，不直接读写 storage。

替代方案是直接在 gateway 中处理登录，但这会让传输层承载业务规则，不符合现有架构约束。

### 第一阶段支持开发期账号密码注册和登录

注册和登录请求携带 `account` 和 `password`。注册成功时服务端创建最小玩家资料和密码哈希；登录时服务端校验账号密码并签发 session token。凭据处理必须通过明确接口表达，不能把固定账号密码散落在 handler 或 dispatcher 中。

替代方案是无密码 guest login。该方案实现更快，但无法覆盖注册、登出、恢复和基础凭据错误路径，不适合作为“完整登录登出”的验收目标。

### Session token 写入 Redis，玩家基础资料写入 MySQL

MySQL 保存 `player` 或等价玩家基础资料：`player_id`、`account_name`、密码哈希、显示名、创建/更新时间等最小事实。Redis 保存短期 `account:session:{sessionToken}`，包含 token、player id、账号名、签发时间、过期时间和连接信息。Redis session 必须有 TTL；Redis 丢失只导致需要重新登录，不丢失玩家基础资料。

替代方案是所有会话只存内存。内存实现可作为 fake adapter 和测试替身，但不能成为长期行为契约。

### 账号 runtime 使用真实 MySQL/Redis adapter

`NewHTTPServer` 负责创建 MySQL/Redis client 并注入 account service。账号资料通过 `database/sql` 和 MySQL driver 访问 `account_player`，业务层只依赖 `PlayerProfileRepository` interface；账号 session 通过 Redis cache 读写 `RedisKeys.AccountSession(sessionToken)`。fake adapter 不提供 runtime 开关，只能被单元测试显式使用，避免测试便利路径进入上线环境。

替代方案是通过配置开关选择 fake/real。该方式容易把 fake 带入真实环境，本 change 不提供 runtime fake 开关。

### 账号资料使用显式 SQL，账号 session 使用 Redis JSON value

账号资料 SQL 很少，使用 `database/sql` 可以清晰控制连接池、上下文取消、重复键错误和 not found 映射；当前不引入 ORM。Redis session value 采用 JSON 编码，字段包含 token、player id、account name、issued/expires 时间和 connection id。JSON 可读性高，便于开发期排查；后续如需压缩或版本化，可通过 value schema 单独演进。

替代方案是 ORM 与 Redis hash。当前阶段没有复杂查询或局部字段更新需求，这两者会增加不必要复杂度。

### 配置补齐 secret 与连接选项

MySQL 配置新增 password、parseTime、连接池参数；Redis 配置新增 password、DB 和连接超时。服务端启动时必须 ping MySQL/Redis，依赖不可用时拒绝启动；HTTP server 生命周期负责关闭 MySQL/Redis client。文档中的示例 secret 只能用于本地开发。

### 登录响应返回 PlayerProfile 和 session token

`RegisterResponse`、`LoginResponse` 和 `ResumeSessionResponse` 返回 `PlayerProfile`、`session_token` 和 `expires_at_ms`。Unity 客户端只把 token 当作不透明字符串保存，不解析 token 内部结构。

替代方案是把 `player_id` 直接作为后续身份凭证。该方案容易被客户端伪造，不满足后续房间操作的身份边界。

### Gateway session 绑定已登录身份

登录或恢复 session 成功后，gateway connection session 记录 `player_id` 和 `session_token` 的摘要。登出或 token 失效后必须清理绑定身份。后续房间请求可先兼容显式 `player_id`，再在房间接入 change 中切换为从 session 读取。

替代方案是让每条业务请求都携带 session token。该方案实现直接，但会把认证细节扩散到所有业务 payload；第一阶段更适合在 gateway session 统一处理。

### Unity 侧新增 NetworkSystem 作为后续前置

客户端需要真实登录时，`AccountSystem` 不应直接持有 WebSocket 细节。后续实现应新增 `NetworkSystem` 或等价网络系统，负责 `/version`、WebSocket envelope、pending request、心跳和错误分发；`AccountSystem` 调用网络系统发起登录/登出。

替代方案是把 WebSocket 写进 `AccountSystem`。该方案短期快，但会在房间、心跳、重连接入时造成重复和边界混乱。

## Risks / Trade-offs

- [Risk] 第一阶段账号密码实现容易被误认为生产级认证。→ Mitigation：文档和代码注释明确这是第一阶段开发期账号会话，不包含注册、找回、第三方登录和生产安全策略。
- [Risk] 新增账号协议会影响现有房间请求的 `player_id` 规则。→ Mitigation：本 change 只建立登录身份和 gateway 绑定，房间请求去除显式 `player_id` 放到后续房间接入 change。
- [Risk] Redis session 丢失导致客户端无法恢复会话。→ Mitigation：客户端收到恢复失败后回到 `LoginPage`，MySQL 玩家基础资料不丢失。
- [Risk] Unity 网络层一次性过大。→ Mitigation：先实现登录/登出所需 envelope request/response、心跳和错误处理，房间大厅 UI 在后续 change 扩展。
- [Risk] 本地没有 MySQL/Redis 时服务端无法启动。→ Mitigation：更新 README 和本地配置，明确启动依赖；业务单元测试继续使用 fake。
- [Risk] 真实 DB 错误映射不完整导致业务错误空泛。→ Mitigation：覆盖重复账号、未找到、Redis miss、上下文取消和依赖不可用测试。
- [Risk] 当前房间路径仍是 memory repository。→ Mitigation：本 change 只承诺账号存储正式化，房间持久化由后续 change 承接。

## Migration Plan

1. 新增账号注册/登录协议与 message id，重新生成 Go 协议代码，预留 Unity C# 生成入口。
2. 新增 storage migration、repository/cache interface、fake adapter 和 Redis key builder。
3. 新增 `account` service 和单元测试。
4. 将 account dispatcher 注册到 gateway，登录/恢复成功后绑定 session 身份。
5. 实现 MySQL account repository、Redis account session cache，并在 server runtime 注入真实依赖。
6. 客户端新增或补齐网络系统，移除本地假登录路径并接入注册按钮。
7. 更新文档和本地验收路径。

回滚时可以禁用账号 dispatcher，客户端回到未登录且不可进入联机流程的状态；已新增 MySQL 表通过 down migration 回滚，Redis session key 依赖 TTL 自动清理。真实依赖 wiring 回滚到上一版本后，已写入 MySQL 的账号资料保留。

## Open Questions

- Unity 本地 session token 保存使用 `PlayerPrefs` 还是后续封装的 `SaveSystem`？
