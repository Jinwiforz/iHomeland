## Requirements

### Requirement: 本地基础设施必须可一键启动
项目必须提供本地基础设施入口，用于通过 Docker Compose 启动第一里程碑需要的 MySQL 和 Redis 依赖。

#### Scenario: 启动本地依赖
- **WHEN** 开发者执行本地基础设施启动命令
- **THEN** MySQL 和 Redis 容器启动并暴露配置中声明的本地端口

#### Scenario: 重复执行启动命令
- **WHEN** 开发者在本地依赖已经存在时再次执行启动命令
- **THEN** 命令保持幂等并复用已有容器、网络和数据卷

### Requirement: 本地依赖必须声明健康检查
本地 MySQL 和 Redis 依赖必须在 Docker Compose 中声明健康检查，用于判断容器是否具备被服务端连接的条件。

#### Scenario: 依赖完成启动
- **WHEN** MySQL 和 Redis 均通过自身健康检查
- **THEN** Docker Compose 将对应服务标记为 healthy

#### Scenario: 依赖启动失败
- **WHEN** MySQL 或 Redis 未能在健康检查窗口内响应
- **THEN** 本地基础设施启动入口必须返回失败并指出失败的依赖名称

### Requirement: 本地配置示例不得包含真实密钥
项目必须在服务端目录提供本地环境变量或配置示例，示例值只用于开发环境，不得包含真实生产密钥、账号或连接信息。

#### Scenario: 查看配置示例
- **WHEN** 开发者查看 `server/.env.example` 或本地配置示例
- **THEN** 文件只包含示例端口、示例账号、示例数据库名和可被本地覆盖的配置项

#### Scenario: 使用环境变量覆盖示例值
- **WHEN** 开发者提供本地环境变量覆盖 MySQL 或 Redis 地址
- **THEN** 服务端使用环境变量中的地址连接本地依赖

### Requirement: 本地验证必须覆盖基础接口和依赖状态
项目必须提供本地验证入口，用于确认服务端实际配置指向的 MySQL、Redis 地址、服务端 `/healthz`、`/readyz` 和 `/version` 能在本地环境中协同工作。

#### Scenario: 本地验证成功
- **WHEN** MySQL、Redis 和服务端均正常运行
- **THEN** 本地验证入口确认配置中的 MySQL、Redis TCP 地址可连接，且 `/healthz`、`/readyz` 和 `/version` 返回成功

#### Scenario: Redis 不可用
- **WHEN** Redis 未启动或无法连接
- **THEN** 本地验证入口必须失败，且 `/readyz` 必须报告 Redis 依赖不可用

### Requirement: 本地基础设施不得创建业务持久化事实
本地基础设施只能提供 MySQL 数据库、Redis 实例和连接配置，不得在本 change 中创建房间、玩家、对局或经济系统的业务事实。

#### Scenario: 初始化本地数据库
- **WHEN** MySQL 容器首次初始化
- **THEN** 初始化过程只创建本地数据库、开发账号或基础连接条件，不创建第一里程碑业务表

#### Scenario: 使用 Redis
- **WHEN** 本地 Redis 容器启动
- **THEN** Redis 只作为短期运行态依赖存在，不成为持久数据事实来源
