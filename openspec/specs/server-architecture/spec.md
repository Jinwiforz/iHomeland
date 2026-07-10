# Server Architecture 规格

## Purpose

定义 Go 服务端 Composition Root、分层、领域、存储、生命周期、可观测和测试行为。

## Requirements

### Requirement: 服务端必须使用唯一 Composition Root
Go 服务端 MUST 由唯一 Composition Root 创建配置、基础设施、repositories、application services 和 transport adapters，并管理初始化失败回滚与逆序关闭。

#### Scenario: 存储初始化失败
- **WHEN** MySQL 或 Redis adapter 在启动阶段初始化失败
- **THEN** 服务端逆序释放已成功资源、拒绝进入 ready 状态并返回非零退出结果

### Requirement: 业务逻辑必须与 transport 解耦
Account、PersonalWorld、WorldInstance 与 VisitSession 业务 MUST 通过 application service 与 domain model 实现，不得直接依赖 Gin handler、WebSocket connection 或 TCP socket。

#### Scenario: 个人世界命令由 TCP 调用
- **WHEN** TLS/TCP adapter 收到通过 AuthContext、admission 和 schema 校验的个人世界 command
- **THEN** adapter 只完成 decode、validate、authorize、调用 application service 和 encode，世界 mutation 由对应 domain owner 执行

### Requirement: 持久事实与运行态缓存必须分离
MySQL MUST 保存 Account、Player 与 PersonalWorld 等需要恢复的持久事实，Redis MUST 只保存 session、WorldInstance assignment/lease、VisitSession、presence 和 admission 等可恢复或可失效运行态。

#### Scenario: Redis 数据被清空
- **WHEN** Redis flush 后服务恢复
- **THEN** 持久账号、Player 与 PersonalWorld revision 仍可从 MySQL 恢复，WorldInstance 和 VisitSession 通过权威事实重建或安全结束

### Requirement: 服务端必须可观测并可受控关闭
服务端 MUST 提供结构化日志、metrics、健康/就绪检查、稳定关闭原因和有超时上限的 graceful shutdown。

#### Scenario: 进程收到终止信号
- **WHEN** 服务端进入关闭流程
- **THEN** 它停止接收新业务、通知或关闭现有连接、等待有界后台任务并在超时后强制退出

### Requirement: 服务端核心必须可独立测试
Domain 和 application service MUST 在不启动 listener、MySQL 或 Redis 的情况下运行单元测试，adapter 必须通过 contract/integration test 验证。

#### Scenario: 测试个人世界核心
- **WHEN** 测试 PersonalWorld identity/revision、WorldInstance lease/fencing、VisitSession 权限和断线恢复
- **THEN** 测试只构造纯 Go domain/application 对象并使用 fake repository、clock、placement 或 admission provider
