# iHomeland 总体架构

## 架构定位

iHomeland 是 Go 服务端与 Unity PC 客户端组成的在线游戏项目。第一阶段使用单 Go 进程承载清晰的逻辑模块，通过 MySQL 保存持久事实、Redis 保存可恢复运行态；Unity 只在服务端 v1 契约冻结后开始实现。

核心原则：

- 服务端权威
- 协议先于 listener
- 服务端先于客户端
- 领域与 transport 解耦
- 持久事实与缓存分离
- 单进程先行、指标驱动拆分
- 客户端应用作用域与场景作用域分离
- 个人世界默认私有，访客访问必须显式授权

## 目标拓扑

```text
                         +----------------------+
                         |   Go Test Clients    |
                         | Contract / Load / E2E|
                         +----------+-----------+
                                    |
                 +------------------+------------------+
                 |                  |                  |
              HTTPS                WSS              TLS/TCP
         bootstrap/account       control          reliable business
                 |                  |                  |
                 +------------------+------------------+
                                    |
                         +----------v-----------+
                         | Transport Adapters   |
                         +----------+-----------+
                                    |
                         +----------v-----------+
                         | Auth + Dispatcher    |
                         +----------+-----------+
                                    |
                  +-----------------+-----------------+
                  |                                   |
          +-------v--------+                  +-------v-------+
          | Account App    |                  | World / Visit |
          +-------+--------+                  | Applications  |
                  |                           +-------+-------+
          +-------v--------+                          |
          | Account Domain |          +---------------+---------------+
          +-------+--------+          |               |               |
                  |            +------v------+ +------v------+ +------v------+
                  |            |PersonalWorld| |WorldInstance| |VisitSession |
                  |            |   Domain    | | Placement   | |   Domain    |
                  |            +------+------+ +------+------+ +------+------+
                  |                   |               |               |
                  +-------------------+---------------+---------------+
                                    |
                         +----------v-----------+
                         | Repository Interfaces|
                         +-----+-----------+-----+
                               |           |
                            MySQL        Redis
                       persistent facts runtime cache
```

Unity 客户端在服务端 v1 资格验收后接入同一冻结契约，不参与服务端基础语义的试错。服务端 v1 的业务目标就是以下个人世界拓扑：

```text
PlayerID
  -> PersonalWorldID (persistent, immutable owner)
       -> WorldInstance (server-hosted, replaceable runtime)
            -> Owner connection
            -> VisitSession
                 -> Visitor connections
            -> Activity admission
                 -> ActivityInstance
```

Owner 是 PersonalWorld 的领域所有者，不是 P2P 网络主机。PersonalWorld、WorldInstance、VisitSession 与 ActivityInstance 分别拥有持久世界、运行承载、访客资格和活动运行事实，不能由 Room 或 transport connection 代管。

## 服务端 Composition Root

`cmd/server` 只负责进程入口、信号和退出码。`internal/app` 的 Composition Root 负责：

- 加载并校验配置
- 创建 logger、metrics、clock 和基础设施
- 创建 MySQL/Redis adapters
- 创建 application services 和 domain dependencies
- 创建 HTTP/WSS/TCP adapters
- 按依赖顺序初始化
- 失败时逆序回滚
- 进入 ready 状态
- 收到终止信号后按 deadline 逆序关闭

依赖只在 composition 层连接，不使用 package global 保存业务服务。

配置必须在 logger、listener 或 goroutine 创建前严格解码并完整校验。Lifecycle component 只表示真实资源所有权：组件按依赖顺序启动，只有 Start 成功后才进入 started stack；初始化失败与正常关闭都使用该栈逆序释放，并共享同一个总 shutdown deadline。

长生命周期任务必须登记稳定名称与 owner context。Component Stop 取消并等待自身任务，root task 在其规定阶段取消；任务异常返回或 panic 会撤销 readiness 并触发非零受控关闭，不能让进程继续以 ready 状态运行。

Readiness 固定单向迁移：

```text
starting -> ready -> draining -> stopped
```

诊断 listener 最先启动、最后关闭，只提供 `/healthz`、`/readyz`、`/version` 与 `/metrics`。公开 HTTP 与 WSS control 共用一个 TLS 入口，gameplay TLS/TCP 使用独立 listener；三者在 MySQL/Redis 之后启动、之前关闭。顶层 HTTP mux 只把精确 `/v1/control` 交给 WSS，其余请求仍由冻结的 10-operation HTTP router 处理，gameplay TCP 不复用 HTTP 或诊断 router。

Listener 的推荐默认值、环境覆盖、容器映射与端口冲突规则统一由 `docs/network-port-allocation.md` 管理。端口不是跨环境身份：客户端通过 bootstrap、服务发现或 ticket 获得实际业务端点，基础设施 adapter 从环境配置读取实际连接地址。

## 服务端分层

### Transport

HTTP、WSS 和 TCP adapter 只能：

- 读取 frame/request
- 校验格式、大小和协议版本
- 建立 auth context
- 校验 route registry
- 调用 application service
- 编码 response/push/error
- 管理连接级背压、deadline 和 close reason

Transport 不实现账号规则、个人世界/访客状态迁移或 repository 事务。

### Application

Application service 表达用例与事务边界：

- 账号与统一会话用例
- world lifecycle、placement、visit admission 与安全返回用例
- 将 domain event 投影为 repository 写入和 push
- 保持 command idempotency 与 auth scope

Application service 只依赖 domain、repository/cache interface、clock、ID generator 和 event publisher。

### Domain

Domain 是纯 Go 业务核心：

- account normalization 与状态
- personal world identity/owner、visit role 与 activity admission
- 显式 command、state transition 和 domain event
- 权限、容量、deadline、幂等与不变量

Domain 不依赖 Gin、socket、SQL、Redis、Protobuf 生成类型或日志框架。

### Infrastructure

Infrastructure 实现：

- MySQL repositories 与 migration
- Redis 可恢复运行态与必要短租约
- transport 进程内有界 rate limit；分布式配额必须由独立 change 设计
- 个人世界阶段的 instance assignment、lease/fencing 与 visit presence adapters
- token/password cryptography
- transport listeners
- metrics/tracing exporters
- system clock 与 ID generator

实现必须满足由 interface owner 定义的 contract tests。

## 身份与会话

唯一身份链：

```text
HTTPS credentials
  -> account register/login
  -> Session Core CreateSession
  -> access token + session_id + session_epoch
  -> one-time connection ticket
  -> WSS/TCP connection auth context
```

约束：

- payload 中的玩家 ID 不是身份来源
- access/refresh 使用带类型前缀的 32-byte entropy opaque secret；connection ticket 使用既有结构化字段与 16-byte one-time nonce
- 持久边界只保存 token secret 或 ticket nonce 的 SHA-256 digest，协议 adapter 负责纯 Go ticket 与 generated model 的转换
- refresh 轮换必须原子撤销上一枚 access、消费旧 refresh 并写入新 token pair；tombstone 保留到 session expiry
- ticket 必须短 TTL、一次性并绑定唯一 channel、受信 endpoint、scope 与 session epoch
- WSS ticket 只授予 control scope，TLS/TCP ticket 只授予 gameplay connection scope；具体 mutation 仍需 admission 与领域授权
- session epoch 更新必须先提交并撤销旧资格，再通知连接边界；通知失败不能回滚权威状态
- AuthContext 只能由 session 认证流程构造；业务服务只接收 auth context，不接收原始 token
- 日志不得记录密码、raw token、ticket nonce、完整 digest、principal 敏感字段或存储 key/value

`internal/account` 是账号身份、canonical username、display name、credential 验证以及 register/login 的唯一 owner。它只通过消费侧 `AccountRepository`、`CredentialHasher` 和 `SessionIssuer` 编排流程：repository 必须原子提交 account/player/credential，unknown username 必须执行同算法与成本的 dummy verification，持久账号提交后 session 失败不能触发跨存储补偿删除。公开 summary 不包含 username、player ID、status 或 credential hash。

`internal/session` 是 session、token、ticket、AuthContext 与 epoch 语义的唯一 owner。它通过消费侧 `SessionStore` 表达必须原子的 create、resolve、rotate、consume 与 invalidate 操作，通过 `EndpointProvider` 获取受信目标，通过不持有 socket 的 `ConnectionInvalidator` 发布已提交的新 epoch。Refresh、logout 和 ticket 不经 account facade 转发。

`PlayerID` 在一个独立产品部署中是稳定玩家身份。登录地点、物理部署位置、WorldInstance 或 ActivityInstance 变化不得创建、覆盖或重新解释 PlayerID。中国大陆版与国际版可以根据账号、发行或合规要求使用独立账号入口和数据部署。

`internal/storage/account` 已借用共享 MySQL pool 实现单表 repository，并以固定 Argon2id v19 profile、严格 PHC parser、constant-time compare 和有界并发实现 production hasher；`internal/storage/session` 已借用共享 standalone Redis client，以版本化 Hash、逻辑 UTC Unix 微秒 expiry 和 owner Lua scripts 实现 `SessionStore`。两者不拥有 pool/client、goroutine、listener 或 memory fallback。Redis flush 后旧 credential 全部 fail closed，只能基于仍在 MySQL 的账号重新登录创建新 session，不能恢复旧运行态。

这些 production adapters 已接入正式 Composition Root，提供 register/login/refresh/logout、connection ticket 签发以及认证 WSS/TLS-TCP 通道。WSS 与 TCP 都使用受信 `EndpointProvider` 值原子消费一次性 ticket；TCP 还消费绑定 current full AssignmentStamp 的 WorldAdmission。组合 `ConnectionInvalidator` 在 epoch 提交后并行关闭两个通道的旧连接，任一通道失败不跳过另一通道。`_test.go` reference adapters 永远不能进入生产接线。

## 个人世界与访客联机

每个已创建角色拥有一个 primary PersonalWorld：

- `PersonalWorldID` 是世界持久身份。
- `WorldOwnerID` 固定为创建该世界的 PlayerID，不因连接或访客变化转移。
- `WorldInstanceID` 标识当前服务端运行承载，可以启动、休眠、重建或迁移。
- 同一 PersonalWorld 最多有一个对 gameplay 开放的 active writable instance。
- lease/fencing 或等价机制必须拒绝旧实例继续提交世界 mutation。

PersonalWorld 不是包含全部玩法的巨大 aggregate。任务、探索、家园、NPC 和世界资源由后续领域分别拥有，但每个世界事实必须关联明确 PersonalWorld owner、revision/transaction 边界和恢复来源。

访客通过 VisitSession 加入 Owner 当前 WorldInstance：

```text
Owner opens invitation
  -> server creates bounded invite
  -> Visitor accepts with AuthContext
  -> eligibility + capacity + version validation
  -> placement resolves Owner WorldInstance
  -> one-time admission
  -> Visitor role connection
```

VisitSession 拥有 immutable owner、PersonalWorldID、Visitor membership、capacity、revision、connection state 与 expiry。Invite 不是 gameplay bearer credential；`internal/worldadmission` 独立签发安全 ASCII opaque credential，并绑定 PlayerID、SessionID/epoch、Owner/Visitor role、PersonalWorld/可选 VisitSession、purpose、完整 AssignmentStamp、TLS/TCP endpoint/channel 与绝对 expiry。客户端 payload 不能覆盖 owner、world、instance 或 actor identity。

状态按 owner 分离：

| 状态 | Owner |
|---|---|
| 角色、装备、背包、技能和个人奖励 | PlayerState owner |
| 世界环境、探索对象、世界任务和持久 revision | PersonalWorldState owner |
| 邀请、Visitor、临时权限、连接和 expiry | VisitSession owner |
| 副本、Boss、剧情位面或战斗运行状态 | ActivityInstance owner |

Visitor 默认不能修改世界配置、推进 Owner 关键任务、消费不可恢复唯一资源、邀请更多玩家或提交奖励最终事实。每个开放交互必须登记 actor role、mutation owner、settlement owner、idempotency、revision/transaction 与失败语义；Visitor 奖励写入其自身 player ledger，世界 mutation 写入 Owner PersonalWorld，禁止 handler 顺序双写伪装原子成功。

Owner 断线后进入有绝对 deadline 的 reconnect grace。Owner 在 deadline 前恢复时可继续访问；主动关闭或 grace 到期时 VisitSession 关闭，Visitor 获得明确原因并返回自己的 PersonalWorld 或安全入口。Visitor 不继承 WorldOwnerID，个人世界不执行 Room 式 host succession。

`internal/storage/visitsession` 已以共享 standalone Redis 实现 production `VisitSessionStore`：active/session/command 三类 versioned Hash 在 owner Lua 线性化点内维护唯一索引、revision CAS 和完整重放结果。Adapter 不拥有 Redis client、后台 cleanup、admission credential 或 listener；正式 Composition Root 由 application coordinator 把 HTTP accept/admission、TLS/TCP realtime lifecycle command、semantic deadline、WSS notice、TCP snapshot 和精确 safe-return 组合为同一结果路径。Redis 进程重启只能恢复其自身仍保留的合法运行态；flush 或 key 丢失后不从 MySQL 补回旧访问资格。完整 key/field/TTL 字典由 `docs/redis-keys.md` 唯一管理。

World admission runtime 使用注入的至少 256-bit derivation key、稳定 issuance identity 与完整 binding fingerprint，以 HMAC-SHA-256 可重复推导短期 credential；Redis 只保存 credential digest、binding 和 consume tombstone。Issue/consume 由 `internal/storage/worldadmission` owner Lua 原子线性化，同一 consume identity 可解析响应丢失，其他重放拒绝；TCP 握手消费后 application 仍重新读取 current full assignment，VisitSession 仍二次验证 membership 与 deadline。Own-world/visit-world producer、cleanup 与 safe-return 已接线；`server/internal/testclient` 只经公开 HTTPS/WSS/TLS-TCP 验证独立 `cmd/server`，分层门禁、冻结摘要和故障 ownership 由 `docs/server-v1-qualification.md` 管理。

Party 只在需要跨场景持续队伍、队长、队伍聊天或连续活动时建立。直接访问好友个人世界只需要 VisitSession，不要求预先创建 Party 或 Room。

Room 只在未来活动确实需要公开列表、席位、房主、ready/start 或活动前组装时建立，并且只拥有活动准备事实。Room 不作为 PersonalWorld、VisitSession 或 ActivityInstance 的父模型，也不接管世界运行、活动运行或结算事实。

## 数据所有权

### MySQL

保存必须跨进程重启恢复的事实：

- account identity 与 credential hash
- account/player profile 最小信息
- 必要审计与幂等记录
- PersonalWorld identity、owner、持久 revision 与领域子系统事实

### Redis

保存可恢复或可失效运行态：

- account session/token metadata
- connection/session presence
- WorldInstance assignment、lease/fencing 与 presence
- VisitSession、invite/admission 与 reconnect qualification
- rate limit 和短租约

Redis key 必须有 owner、TTL、value schema、恢复来源和清理触发。Redis 不得成为持久事实唯一来源。

## 协议与路由

协议基线必须先于 transport 实现冻结，同一实时 message id 只有一个 allowed channel。协议源、编号和演进由 `docs/protocol-compatibility.md` 定义，通道路由、会话和 framing 由 `docs/network-transport-architecture.md` 定义。

## 客户端边界

Unity 使用 Composition Root + App Scope + Scene Scope：

- App Scope：网络、账号、PersonalWorld、VisitSession、应用流程和持久 UI/Audio hosts
- Scene Scope：camera、lighting、地图、角色、场景型 UI
- Application Services：纯 C# 状态与命令
- Unity Hosts：主线程、Coroutine、UI、Audio 和 SceneContext adapter

客户端不得把 Scene、Prefab 或 ScriptableObject 作为在线业务事实 owner。详细规则见 `docs/client-architecture.md`。
