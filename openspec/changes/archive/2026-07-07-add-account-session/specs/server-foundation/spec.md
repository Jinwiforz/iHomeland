## MODIFIED Requirements

### Requirement: 服务端启动必须校验关键配置
MUST:服务端启动必须校验 HTTP、版本文件、MySQL、Redis、gateway 和日志配置，并在关键配置缺失或格式非法时拒绝启动。

#### Scenario: MySQL secret 缺失
- **WHEN** server runtime 需要连接真实 MySQL
- **THEN** 配置必须支持从本地配置或环境变量读取 MySQL password，并在连接失败时拒绝启动

#### Scenario: Redis 配置非法
- **WHEN** Redis 地址、DB、密码或连接超时配置非法
- **THEN** 配置加载或依赖初始化必须返回明确错误，不得退回 fake/in-memory storage

### Requirement: 服务端必须管理外部依赖生命周期
MUST:服务端 runtime 创建的 MySQL 和 Redis client 必须由 server lifecycle 统一持有和关闭，避免连接泄漏。

#### Scenario: HTTP server 创建成功
- **WHEN** `NewHTTPServer` 成功返回
- **THEN** MySQL 和 Redis client 必须已通过 ping 或等价检查确认可用

#### Scenario: HTTP server 关闭
- **WHEN** HTTP server 被关闭
- **THEN** runtime 必须关闭 MySQL 和 Redis client，释放连接池资源
