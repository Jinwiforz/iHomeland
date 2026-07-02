## MODIFIED Requirements

### Requirement: 配置必须支持本地文件、环境变量覆盖和校验
服务端配置必须提供默认值、本地配置文件、环境变量覆盖和必要字段校验，并必须包含 MySQL、Redis 本地连接参数的结构化配置。

#### Scenario: 未提供环境变量
- **WHEN** 服务端在未设置环境变量的本地环境启动
- **THEN** 服务端读取默认本地配置文件或使用内置默认值

#### Scenario: 环境变量覆盖配置文件
- **WHEN** 本地配置文件和环境变量同时提供 HTTP 地址
- **THEN** 服务端优先使用环境变量中的 HTTP 地址

#### Scenario: 环境变量覆盖基础设施连接
- **WHEN** 本地配置文件和环境变量同时提供 MySQL 或 Redis 地址
- **THEN** 服务端优先使用环境变量中的 MySQL 或 Redis 地址

#### Scenario: 配置值非法
- **WHEN** 配置包含非法 HTTP 地址、不支持的日志等级、非法 MySQL 地址或非法 Redis 地址
- **THEN** 服务端返回配置校验错误并拒绝启动

### Requirement: 服务端必须提供健康检查
服务端必须提供 `/healthz` 接口，用于报告进程存活状态。`/healthz` 不得因为 MySQL 或 Redis 短暂不可用而报告进程死亡。

#### Scenario: 调用 healthz
- **WHEN** 客户端请求 `/healthz`
- **THEN** 服务端返回成功状态和机器可读响应

#### Scenario: 基础设施依赖不可用
- **WHEN** MySQL 或 Redis 暂时不可连接但 HTTP server 仍在运行
- **THEN** `/healthz` 继续返回进程存活状态

### Requirement: 服务端必须提供就绪检查
服务端必须提供 `/readyz` 接口，用于报告当前进程是否具备处理基础 HTTP 请求的能力，并必须包含本地 MySQL、Redis 依赖的可用状态。

#### Scenario: 调用 readyz
- **WHEN** 客户端请求 `/readyz`
- **THEN** 服务端返回当前就绪状态和机器可读响应

#### Scenario: 基础设施依赖全部可用
- **WHEN** MySQL 和 Redis 均可连接
- **THEN** `/readyz` 返回就绪状态并标记 MySQL、Redis 可用

#### Scenario: 基础设施依赖不可用
- **WHEN** MySQL 或 Redis 不可连接
- **THEN** `/readyz` 返回非就绪状态并标记失败依赖名称

### Requirement: 基础接口必须可测试
配置加载、健康检查、就绪检查、版本接口和本地基础设施依赖状态输出必须有自动化测试覆盖。

#### Scenario: 运行服务端基础测试
- **WHEN** 开发者运行服务端测试
- **THEN** 配置默认值、健康检查、就绪检查、依赖状态输出和版本响应测试通过
