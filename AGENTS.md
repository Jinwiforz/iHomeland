# iHomeland 项目协作规范

本文档是仓库内人工开发者、AI 助手和自动化代理必须遵守的顶层规则。详细工程标准以 `docs/engineering-standards.md` 为准，流程规则以 `docs/workflow.md` 为准。

## 语言

- 面向项目成员的说明、文档、OpenSpec artifacts、评审意见和任务拆解默认使用中文。
- 代码标识符、协议字段、错误码、命令、路径和外部 API 名称保留英文。
- 引用外部英文资料时，必须用中文说明结论和项目影响。

## 项目边界

iHomeland 是 Go 后端搭配 Unity 客户端的在线游戏项目。

第一阶段技术栈：

- Go
- Gin
- WebSocket / TCP
- Protobuf
- Redis
- MySQL
- gRPC
- `slog` 或 `zap`
- Docker
- 自研房间逻辑

第一里程碑固定为“自定义房间大厅”。允许做服务端启动、健康检查、版本接口、WebSocket 连接、Protobuf envelope、注册、登录、登出、会话恢复、创建房间、加入房间、准备、退出、房主转移、断线重连恢复身份。

第一里程碑禁止混入匹配系统、MOBA/RTS 高频战斗模拟、独立 battle server、跨服、观战、回放和完整经济系统。

## 必读文档

修改项目前先按任务类型阅读对应文档：

- 总体架构：`docs/architecture.md`
- 工程标准：`docs/engineering-standards.md`
- 项目流程：`docs/workflow.md`
- 文件结构：`docs/file-structure.md`
- 路线图：`docs/roadmap.md`
- 协议兼容：`docs/protocol-compatibility.md`
- Redis key：`docs/redis-keys.md`
- 长期规格：`openspec/specs/`

## OpenSpec 硬规则

- 架构、协议、跨模块、房间、存储、部署和流程变更必须先走 OpenSpec。
- 一个 change 只解决一个明确问题；跨多个方向时必须拆分。
- 设计决策写入 `design.md`。
- 长期系统行为写入 `specs/`。
- 实现动作写入 `tasks.md`。
- 长期路线写入 `docs/roadmap.md`。
- 每个 requirement 必须有可验收 scenario。
- 任务完成后立即勾选；全部完成且 specs 同步后才能归档。

## 代码质量硬规则

- 代码必须正确、清晰、精确、优雅、可测试、可维护。
- 不允许为了“先跑起来”留下无说明的临时分支、魔法值、静默失败或吞错逻辑。
- 不允许引入无实际收益的抽象层。
- 业务逻辑不得直接依赖 Gin handler、WebSocket connection 或 TCP socket。
- 房间状态迁移必须通过显式状态机完成。
- Redis 不得作为持久事实来源；Redis key 必须有 owner、TTL 或恢复路径。
- MySQL schema 变更必须有迁移和兼容策略。
- gRPC 只用于明确服务边界，第一阶段默认使用 Go interface 和 in-process adapter。

## Go 规则摘要

- 包名小写短名，不使用下划线、中划线或复数形式。
- 文件名使用小写蛇形，例如 `room_state.go`。
- 导出标识符使用 PascalCase，非导出标识符使用 camelCase。
- 常见缩写保持一致：`ID`、`URL`、`HTTP`、`RPC`、`TCP`、`UDP`、`JSON`、`SQL`。
- 错误变量使用 `Err` 前缀。
- `context.Context` 作为函数首参并命名为 `ctx`，不得存入 struct。
- 接口只在存在替换实现、测试隔离或边界抽象需求时创建。

## 注释规则摘要

- 注释默认使用中文。
- 导出的 Go 类型、函数、接口、常量和错误必须有 Go doc，且以标识符名称开头。
- 注释解释意图、边界、不变量和取舍，不复述代码。
- 状态机、协议兼容、幂等、重连、权限、分布式锁和一致性逻辑必须写清设计意图。
- `TODO` 必须说明原因、触发条件和后续动作，禁止无上下文 TODO。

## 禁止事项

- 禁止绕过 OpenSpec 做跨模块架构变更。
- 禁止在未设计 battle server 前实现高频权威战斗。
- 禁止让 handler 承载复杂业务状态机。
- 禁止手工修改生成代码。
- 禁止新增没有 owner 的表、Redis key、协议消息或服务接口。
- 禁止保留过期、重复、无意义的说明文档。
