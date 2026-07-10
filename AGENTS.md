# iHomeland 项目协作规范

本文档是仓库内开发者、AI 助手和自动化代理必须遵守的顶层规则。工程细则见 `docs/engineering-standards.md`，流程细则见 `docs/workflow.md`。

## 语言

- 项目文档、OpenSpec artifacts、评审和任务拆解默认使用中文。
- 代码标识符、协议字段、错误码、命令、路径和外部 API 使用英文。
- 外部英文资料必须用中文说明结论与项目影响。

## 项目边界

iHomeland 使用 Go 服务端与 Unity PC 客户端。

长期技术栈：

- Go、Gin、Protobuf
- MySQL、Redis
- HTTPS、WSS、TLS/TCP
- 裸 UDP、KCP（战斗模型完成后）
- Unity、Input System、UI Toolkit、uGUI
- Docker、本地自动化与结构化日志

第一业务里程碑固定为“进入自己的个人世界并支持受控访客联机”，包括 PersonalWorld、WorldInstance、VisitSession、MySQL/Redis、HTTPS/WSS/TLS-TCP 与 Go 协议测试客户端验收。ActivityInstance、Room、Party、匹配、正式战斗、battle server、跨服、观战、回放和完整经济系统不混入该阶段。

## 交付顺序硬规则

- 服务端 v1 必须先于 Unity 运行时代码完成。
- 服务端 v1 必须由 Go 协议测试客户端独立验收。
- 服务端 v1 必须覆盖 own-world、visit-world、断线恢复、陈旧 admission 拒绝和持久化恢复。
- `qualify-server-v1` 完成前不得实现 Unity HTTP/WSS/TCP、业务 Services 或 UI 页面。
- Room、Party 与 ActivityInstance 不得成为 PersonalWorld 或 VisitSession 的前置条件；只有独立产品需求和验收边界成立后才能提出。
- UDP/KCP 必须等待 battle simulation model 与 network profile。
- gRPC 和服务拆分必须有独立扩缩容、故障隔离或团队 ownership 证据。

## 必读文档

- 总体架构：`docs/architecture.md`
- 路线图：`docs/roadmap.md`
- 网络传输：`docs/network-transport-architecture.md`
- 协议治理：`docs/protocol-compatibility.md`
- 客户端运行时：`docs/client-architecture.md`
- 客户端 UI：`docs/client-ui-architecture.md`
- 客户端接入：`docs/client-integration.md`
- 文件结构：`docs/file-structure.md`
- 工程标准：`docs/engineering-standards.md`
- 代码注释：`docs/code-comment-convention.md`
- 项目流程：`docs/workflow.md`
- Git 提交：`docs/git-commit-convention.md`
- Redis key：`docs/redis-keys.md`
- 长期规格：`openspec/specs/`

## OpenSpec 硬规则

- 架构、协议、跨模块、房间、存储、部署、场景和流程变更必须先走 OpenSpec。
- 一个 change 只解决一个明确问题，并具有进入条件、产出和完成条件。
- 设计决策写入 `design.md`，长期行为写入 `specs/`，实现动作写入 `tasks.md`。
- 每个 requirement 必须包含可验收 scenario。
- 每完成一个 task 立即勾选；主 specs 同步且 strict 验证通过后才能归档。
- 不得以开发早期为理由绕过协议、安全、数据、测试或可观测设计。

## Git 提交硬规则

- 所有提交必须符合 `docs/git-commit-convention.md`。
- 标题使用 `<type>(<scope>): <subject>`；scope 可选，type 与冒号后的空格不可省略。
- 标题默认使用中文，type、scope、代码标识符和技术名词保留英文。
- 一个提交只表达一个主要意图，并保持实现、测试、文档和必要生成物一致。
- OpenSpec 实现提交应写 `OpenSpec: <change-name>` footer。
- 破坏性变更必须同时使用 `!` 和 `BREAKING CHANGE:`，并说明迁移与回滚。
- `WIP`、`fixup!`、`squash!` 不得进入共享历史。

## 注释硬规则

- 所有手写注释必须符合 `docs/code-comment-convention.md`。
- Go package、导出声明、业务类型、函数、方法、struct 字段和 interface 方法必须有 Go doc 或紧邻声明的契约注释。
- C# 所有手写类型和成员无论访问级别都必须使用 `///` XML documentation。
- Protobuf message、enum、service、RPC 和 field 必须在 `.proto` 源文件中注释；禁止手工修改生成代码。
- 注释先简明定位职责，再重点解释原因、不变量、所有权、失败、并发、安全和生命周期，禁止逐行复述实现。
- 显而易见的局部变量不逐个注释；单位、范围、快照、锁、线程、资源或安全语义不明显时必须解释。
- C# `#region` 只用于两个以上稳定职责成员，不嵌套、不用于掩盖过大类型；Go 禁止伪 region。
- 行为变化必须同步更新注释，过期或错误注释按代码缺陷处理。
- `TODO` 必须包含 owner、原因、触发条件和后续动作。

## 服务端硬规则

- 业务逻辑不得直接依赖 Gin handler、WebSocket connection 或 TCP socket。
- handler 只负责 decode、validate、authorize、调用 application service 和 encode。
- PersonalWorld、WorldInstance 与 VisitSession 必须由独立 domain owner 管理，状态迁移只能通过显式 command/policy。
- MySQL 保存持久事实；Redis 只保存可恢复运行态。
- 每个 table、Redis key、协议消息、listener 和 service interface 必须有 owner。
- 初始化必须支持失败回滚，关闭必须有顺序、deadline 和可观测原因。
- 单元测试不得依赖真实 listener；adapter 必须有 contract/integration test。

## 协议与网络硬规则

- 每个实时 message id 必须登记唯一 allowed channel、owner、direction、auth scope、QoS、max size、rate limit 和 idempotency。
- 同一业务消息禁止跨 WSS/TCP 双写或双入口。
- payload 玩家标识不得覆盖连接 session 身份。
- 生产 HTTP、WebSocket 和 TCP 必须使用 TLS。
- UDP/KCP 首次启用时必须同时具备 ticket、AEAD、重放保护、限流、抗放大和网络模拟。
- 禁止在 UDP 上发送账号凭据、资产、奖励和结算事实。

## Unity 硬规则

- 客户端使用 `AppBootstrap -> AppComposition -> AppRoot` 的唯一应用入口。
- App Scope 与 Scene Scope 必须分离，场景对象不得持有账号、socket、PersonalWorld、WorldInstance 或 VisitSession 最终事实。
- 不依赖 Unity 生命周期的业务逻辑优先使用纯 C# Service。
- MonoBehaviour 只承担引擎回调、GameObject 生命周期、Inspector 引用和必要 Unity Host。
- Scene/Prefab 用于内容表现，ScriptableObject 用于配置和共享定义。
- 一个逻辑 screen 只能有一个 active UI owner。
- UI Toolkit/uGUI 混合场景必须验证输入、焦点、层级、主线程和销毁后回写。

## 禁止事项

- 禁止无需求的空 manager、万能 interface、全局 event bus 或 service locator 扩散。
- 禁止业务模块各自维护 session 或连接身份。
- 禁止把 Redis 当作持久数据库。
- 禁止手工修改生成代码。
- 禁止无上下文 TODO、魔法值、静默失败或吞错。
- 禁止提交本地缓存、密钥、Unity Library/Temp/UserSettings 或 Go module/cache。
