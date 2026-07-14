## MODIFIED Requirements

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

### Requirement: Readiness 必须表达进程接收业务的真实状态
服务端 MUST 使用显式 `starting`、`ready`、`draining` 与 `stopped` 状态管理 readiness。只有配置/secret 有效、MySQL migration 与 Redis startup probe 成功且全部必需组件启动后才能进入 `ready`；收到关闭请求、必需后台任务异常退出或 required storage probe 连续失败达到阈值时 MUST 在停止其他组件前进入 `draining`。Readiness MUST 保持单向迁移，不得在同一进程内从 draining 恢复为 ready。

#### Scenario: 全部组件启动成功
- **WHEN** Composition Root 完成 storage 与所有其他必需组件启动
- **THEN** readiness 从 `starting` 原子切换为 `ready`

#### Scenario: 进程开始关闭
- **WHEN** 收到受支持的 OS signal、运行时致命错误或 required storage probe 达到失败阈值
- **THEN** readiness 在业务组件停止前切换为 `draining` 且不再恢复为 `ready`
