## ADDED Requirements

### Requirement: 服务端配置必须在产生副作用前完整验证
服务端 MUST 从显式配置文件和受支持的环境覆盖加载类型化、启动后只读的配置快照，拒绝未知字段、格式错误、非法范围、冲突地址和缺失必填值。除读取配置与构建纯内存对象外，任何 listener、文件写入或后台任务 MUST 等待配置验证成功。

#### Scenario: 配置包含未知字段
- **WHEN** 启动配置包含当前 schema 不认识的字段
- **THEN** 进程在创建 listener 或后台任务前返回配置错误和非零退出结果

#### Scenario: 环境覆盖无法解析
- **WHEN** 受支持的环境变量包含非法 duration、日志级别或网络地址
- **THEN** 配置加载失败且诊断信息标识配置键但不输出敏感值

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
服务端 MUST 使用显式 `starting`、`ready`、`draining` 与 `stopped` 状态管理 readiness。只有配置有效且全部必需组件启动成功后才能进入 `ready`；收到关闭请求或必需后台任务异常退出时 MUST 在停止其他组件前进入 `draining`。

#### Scenario: 全部组件启动成功
- **WHEN** Composition Root 完成所有必需组件启动
- **THEN** readiness 从 `starting` 原子切换为 `ready`

#### Scenario: 进程开始关闭
- **WHEN** 收到受支持的 OS signal 或运行时致命错误
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

## MODIFIED Requirements

### Requirement: 服务端必须使用唯一 Composition Root
Go 服务端 MUST 由唯一 Composition Root 创建配置、日志、metrics、clock、ID generator、基础设施、repositories、application services 和 transport adapters。只有真实持有资源或后台生命周期的对象才能注册为 lifecycle component；组件 MUST 按显式依赖顺序启动，初始化失败时只逆序停止已成功组件，正常关闭时也按逆序停止且每一步共享总关闭 deadline。

#### Scenario: 中间组件初始化失败
- **WHEN** 第 N 个 lifecycle component 启动失败
- **THEN** 服务端只按逆序停止前 N-1 个已成功组件、保持非 ready 并返回非零退出结果

#### Scenario: 存储初始化失败
- **WHEN** 后续 MySQL 或 Redis adapter 在启动阶段初始化失败
- **THEN** 服务端通过同一 lifecycle 协议逆序释放已成功资源、拒绝进入 ready 状态并返回非零退出结果

### Requirement: 服务端必须可观测并可受控关闭
服务端 MUST 提供结构化日志、低基数 metrics、健康/就绪/版本诊断、稳定关闭原因和有总超时上限的 graceful shutdown。关闭顺序 MUST 先撤销 readiness，再逆序停止组件；每个组件在 Stop 中取消并等待自身任务，root task 按所有权在规定阶段取消，最先启动的诊断 listener 最后关闭。任一步失败都必须保留原因并继续尝试释放剩余组件。

#### Scenario: 进程收到终止信号
- **WHEN** 服务端进入关闭流程
- **THEN** 它先进入 draining，随后按组件所有权取消并等待有界后台任务，在总 deadline 内逆序关闭组件且最后关闭诊断 listener

#### Scenario: 一个组件关闭失败
- **WHEN** 某 lifecycle component 的 Stop 返回错误
- **THEN** runtime 记录组件名与错误、继续停止其余组件，并在最终运行结果中报告关闭失败
