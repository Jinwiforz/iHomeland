# Server Architecture 规格

## Purpose

定义 Go 服务端 Composition Root、分层、领域、存储、生命周期、可观测和测试行为。

## Requirements

### Requirement: 服务端必须使用唯一 Composition Root
Go 服务端 MUST 由唯一 Composition Root 创建配置、secret provider、日志、metrics、clock、ID generator、MySQL/Redis 基础设施、repositories、application services 和 transport adapters。MySQL/migration 与 Redis MUST 作为必需 lifecycle resource 按显式依赖顺序启动并在成功后进入唯一 started stack；只有 storage startup invariant 全部满足后进程才能进入 ready。只有真实持有资源或后台生命周期的对象才能注册为 lifecycle component；初始化失败时只逆序停止已成功组件，正常关闭时也按业务消费者、Redis、MySQL、diagnostic 的逆序释放且每一步共享总关闭 deadline。

#### Scenario: 中间组件初始化失败
- **WHEN** 第 N 个 lifecycle component 启动失败
- **THEN** 服务端只按逆序停止前 N-1 个已成功组件、保持非 ready 并返回非零退出结果

#### Scenario: 存储初始化失败
- **WHEN** MySQL pool/migration 或 Redis client/probe 在启动阶段失败
- **THEN** 服务端通过同一 lifecycle 协议逆序释放已成功资源、拒绝进入 ready 状态并返回非零退出结果

#### Scenario: 必需 storage 全部成功
- **WHEN** diagnostic、MySQL migration 与 Redis startup probe 均在 startup deadline 内成功
- **THEN** Composition Root 才将 readiness 从 starting 切换为 ready，且后续业务组件只能消费该唯一资源图

### Requirement: 服务端配置必须在产生副作用前完整验证
服务端 MUST 从显式配置文件和受支持的环境覆盖加载类型化、启动后只读的非敏感配置快照，拒绝未知字段、格式错误、非法范围、冲突地址和缺失必填值。Storage password、TLS private key 与其他 secret MUST 通过独立白名单 provider 解析，不进入普通配置、日志或错误文本；production storage TLS policy MUST 在连接前 fail closed。除读取配置/secret 与构建纯内存对象外，任何 listener、storage client、文件写入或后台任务 MUST 等待全部配置验证成功。

#### Scenario: 配置包含未知字段
- **WHEN** 启动配置包含当前 schema 不认识的字段
- **THEN** 进程在创建 listener、storage client 或后台任务前返回配置错误和非零退出结果

#### Scenario: 环境覆盖无法解析
- **WHEN** 受支持的环境变量包含非法 duration、日志级别、网络地址、pool 或 TLS policy
- **THEN** 配置加载失败且诊断信息标识配置键但不输出敏感值

#### Scenario: 必需 secret 无法解析
- **WHEN** 配置合法但 SecretProvider 无法取得 MySQL/Redis 凭据或 production TLS material
- **THEN** bootstrap 在产生网络副作用前失败，错误只包含稳定 secret reference 而不包含原始值

#### Scenario: 推荐端口已被其他进程占用
- **WHEN** 已通过校验的 listener 地址在当前环境绑定失败
- **THEN** 服务端报告 listener 与绑定原因、逆序回滚已启动组件并以非零结果退出，不会自动选择其他端口

### Requirement: 诊断面必须与公开业务面隔离
服务端 MUST 使用独立诊断 listener 提供 `/healthz`、`/readyz`、`/version` 和 `/metrics`，并为该 listener 配置 header/read/write/idle timeout 与有界响应。诊断 handler MUST NOT 承载账号、session、个人世界或其他公开业务操作，也不得暴露密钥、凭据、完整配置或高基数用户标签。

#### Scenario: 启动尚未完成
- **WHEN** 诊断 listener 已启动但 Composition Root 尚未完成全部组件初始化
- **THEN** `/healthz` 返回存活，`/readyz` 返回非就绪，版本与 metrics 仍保持有界可读

#### Scenario: 请求未知诊断路径
- **WHEN** 客户端访问诊断 listener 上未登记的路径或业务路径
- **THEN** listener 返回稳定的非成功响应且不路由到任何 application service

### Requirement: Readiness 必须表达进程接收业务的真实状态
服务端 MUST 使用显式 `starting`、`ready`、`draining` 与 `stopped` 状态管理 readiness。只有配置/secret 有效、MySQL migration 与 Redis startup probe 成功且全部必需组件启动后才能进入 `ready`；收到关闭请求、必需后台任务异常退出或 required storage probe 连续失败达到阈值时 MUST 在停止其他组件前进入 `draining`。Readiness MUST 保持单向迁移，不得在同一进程内从 draining 恢复为 ready。

#### Scenario: 全部组件启动成功
- **WHEN** Composition Root 完成 storage 与所有其他必需组件启动
- **THEN** readiness 从 `starting` 原子切换为 `ready`

#### Scenario: 进程开始关闭
- **WHEN** 收到受支持的 OS signal、运行时致命错误或 required storage probe 达到失败阈值
- **THEN** readiness 在业务组件停止前切换为 `draining` 且不再恢复为 `ready`

### Requirement: 后台任务必须具有统一所有权和失败传播
每个长生命周期后台任务 MUST 向 Composition Root 的受控任务组登记，并由明确的 root 或 lifecycle component context 拥有，报告异常退出且在关闭 deadline 内完成。Component 的 Stop MUST 取消并等待自身任务，root task 在其规定阶段取消；不得启动无法等待、无法取消或错误无人消费的 goroutine。panic MUST 被记录为受控致命失败并触发进程关闭，而不是留下部分存活进程。

#### Scenario: 必需后台任务异常返回
- **WHEN** 必需后台任务在未取消时返回错误或 panic
- **THEN** runtime 记录稳定组件名与关闭原因、切换为 draining 并启动有界关闭

#### Scenario: 后台任务忽略取消
- **WHEN** 关闭 deadline 到期后仍有任务未退出
- **THEN** 进程报告超时组件、返回非零退出结果且不无限等待

### Requirement: 进程结果必须具有稳定退出语义
`cmd/server` MUST 只负责 bootstrap flag、OS signal、调用 Composition Root 和把稳定运行结果映射为退出码。正常受控关闭 MUST 返回零；配置错误、初始化失败、运行时致命错误和关闭超时 MUST 返回非零，并产生不含敏感值的结构化最终日志。

#### Scenario: 收到正常终止信号
- **WHEN** 已就绪进程收到一次受支持的终止信号且全部组件在 deadline 内停止
- **THEN** 进程记录受控 signal 关闭原因并以零退出码结束

#### Scenario: 初始化组件失败
- **WHEN** 任一必需组件启动失败
- **THEN** 已启动组件完成逆序回滚，失败组件不会执行 Stop，进程以非零退出码结束

### Requirement: 业务逻辑必须与 transport 解耦
Account、PersonalWorld、WorldInstance 与 VisitSession 业务 MUST 通过 application service 与 domain model 实现，不得直接依赖 Gin handler、WebSocket connection 或 TCP socket。

#### Scenario: 个人世界命令由 TCP 调用
- **WHEN** TLS/TCP adapter 收到通过 AuthContext、admission 和 schema 校验的个人世界 command
- **THEN** adapter 只完成 decode、validate、authorize、调用 application service 和 encode，世界 mutation 由对应 domain owner 执行

### Requirement: 持久事实与运行态缓存必须分离
MySQL MUST 保存 Account、Player 与 PersonalWorld 等需要恢复的持久事实，Redis MUST 只保存 session、WorldInstance assignment/lease、VisitSession、presence 和 admission 等可恢复或可失效运行态；业务模块不得把 Redis 作为账号、个人世界、资产或奖励事实的唯一来源。

#### Scenario: Redis 数据被清空
- **WHEN** Redis flush 后服务恢复
- **THEN** 持久账号、Player 与 PersonalWorld revision 仍可从 MySQL 恢复，WorldInstance 和 VisitSession 通过权威事实重建或安全结束

### Requirement: 服务端必须可观测并可受控关闭
服务端 MUST 提供结构化日志、低基数 metrics、健康/就绪/版本诊断、稳定关闭原因和有总超时上限的 graceful shutdown。关闭顺序 MUST 先撤销 readiness，再逆序停止组件；每个组件在 Stop 中取消并等待自身任务，root task 按所有权在规定阶段取消，最先启动的诊断 listener 最后关闭。任一步失败都必须保留原因并继续尝试释放剩余组件。

#### Scenario: 进程收到终止信号
- **WHEN** 服务端进入关闭流程
- **THEN** 它先进入 draining，随后按组件所有权取消并等待有界后台任务，在总 deadline 内逆序关闭组件且最后关闭诊断 listener

#### Scenario: 一个组件关闭失败
- **WHEN** 某 lifecycle component 的 Stop 返回错误
- **THEN** runtime 记录组件名与错误、继续停止其余组件，并在最终运行结果中报告关闭失败

### Requirement: 服务端核心必须可独立测试
Domain 和 application service MUST 在不启动 listener、MySQL 或 Redis 的情况下运行单元测试，adapter 必须通过 contract/integration test 验证。

#### Scenario: 测试个人世界核心
- **WHEN** 测试 PersonalWorld identity/revision、WorldInstance lease/fencing、VisitSession 权限和断线恢复
- **THEN** 测试只构造纯 Go domain/application 对象并使用 fake repository、clock、placement 或 admission provider

### Requirement: Go 与 C++ 模拟服必须保持独立权威边界
Go Control/Data Plane MUST 继续拥有 Account、Session、PersonalWorld、VisitSession、placement、admission、MySQL/Redis 持久/运行事实与结算；独立 C++ Game Simulation Server MUST 只拥有已分配实例内的实时模拟。两者 MUST 通过版本化、认证且可取消的内部控制契约执行 allocate、start、admit、kick、invalidate、drain、stop、heartbeat/lease 与 result handoff，不得通过共享内存、cgo/FFI、客户端 payload 或重复数据库 owner 隐式共享最终事实。

#### Scenario: Go 分配个人世界模拟实例
- **WHEN** current PersonalWorld assignment、Session epoch、VisitSession membership 与模拟节点容量均通过 Go 权威校验
- **THEN** Go 通过内部契约向 C++ 提交绑定完整 identity/generation 的 allocate/admit command，C++ 只能创建对应实时实例和 player binding，不能改写 Go 的 assignment 或 membership

#### Scenario: 现有个人世界进入首个 gameplay 模拟
- **WHEN** Go 为 Owner 当前 PersonalWorld 的 WorldInstance 启动实时探索、普通怪物或 Boss
- **THEN** placement 通过 `RuntimeController` 的远程 adapter 将完整 AssignmentStamp 绑定到 C++ SimulationInstance，继续复用既有 PersonalWorld、VisitSession 与 admission owner，且不要求预先创建 ActivityInstance

#### Scenario: Session 被 Go 失效
- **WHEN** logout、forced logout 或更高 session epoch 已由 Go 提交
- **THEN** Go 向匹配 C++ connection/instance 传播 invalidation，C++ 停止接纳旧 epoch 输入并有界移除 actor，客户端 payload 不能恢复旧资格

#### Scenario: C++ 产生战斗结果
- **WHEN** 模拟服产生可能影响持久世界、资产、奖励或结算的结果
- **THEN** C++ 只提交带实例 generation、Tick、幂等 identity 和证据摘要的 result handoff，由 Go 的对应 owner 验证并提交最终事实，C++ 不直接写入其数据库表或 Redis owner key

### Requirement: C++ 模拟服必须拥有独立生命周期和可观测状态
Game Simulation Server MUST 使用独立 Composition Root、类型化配置、dependency validation、readiness、drain、结构化日志、低基数 metrics、受监督后台任务与有界逆序关闭。Asio I/O、simulation worker、physics/navigation resource、instance registry 与内部 control adapter MUST 有明确 owner；配置或依赖失败 MUST 在绑定 production UDP endpoint 或接受实例前 fail closed。

#### Scenario: UDP endpoint 绑定失败
- **WHEN** C++ 配置与依赖已验证但 UDP listener 端口冲突
- **THEN** 模拟服保持非 ready、逆序释放已启动组件并返回非零结果，Go 不把该节点分配为可接纳实例

#### Scenario: 模拟服进入 drain
- **WHEN** 部署、致命 worker failure 或受控关闭使节点进入 draining
- **THEN** 节点先拒绝新 instance/admission，再有界结束或迁出已有实例、停止网络接纳并按所有权逆序释放资源
