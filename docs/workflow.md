# 项目流程规范

## 分支策略

- `main`：稳定项目基线，只接收经过评审和验证的架构或版本成果。
- `develop`：当前集成主线，按路线承载已通过 OpenSpec 的工作。
- 功能分支：一个 OpenSpec change 对应一条短生命周期分支，命名应表达业务目标。

分支不得混入本机缓存、密钥或 Go/C# generated code；fixtures/golden、lock/checksum 等兼容性与依赖基线按 owner 规则提交。

## Git 提交规范

Git 提交消息与提交粒度的唯一 owner 文档是 `docs/git-commit-convention.md`。

- 架构、协议、存储、安全、跨模块和破坏性变更必须写正文。
- 实现 OpenSpec change 的提交应使用 `OpenSpec: <change-name>` footer。
- 破坏性变更必须同时使用标题 `!` 与 `BREAKING CHANGE:` footer。
- `WIP`、`fixup!` 和 `squash!` 不得进入共享历史。

提交前必须检查 `git status`、`git diff --cached`、相关测试和 `git diff --check`；完整类型、scope、footer、revert 与示例以 owner 文档为准。

## 文档职责

- `README.md`：项目定位、阶段和入口。
- `AGENTS.md`：必须遵守的硬规则。
- `docs/architecture.md`：系统边界和状态所有权。
- `docs/roadmap.md`：严格交付顺序与 changes。
- `docs/network-transport-architecture.md`：通道、会话、安全和故障。
- `docs/protocol-compatibility.md`：schema、编号、路由和演进。
- `docs/client-architecture.md`：Unity runtime 与 scope。
- `docs/client-ui-architecture.md`：双 UI 与 view lifecycle。
- `docs/client-integration.md`：服务端交付包与客户端接入。
- `docs/file-structure.md`：目录 owner。
- `docs/engineering-standards.md`：代码、数据、测试和质量。
- `docs/code-comment-convention.md`：Go、C#/Unity、协议与测试注释。
- `docs/git-commit-convention.md`：提交消息、提交粒度和历史治理。
- `docs/redis-keys.md`：Redis key registry。

同一规则只有一个 owner 文档，其他文档只链接或摘要。

## OpenSpec 流程

架构、协议、跨模块、房间、存储、transport、场景、部署和流程变更必须使用 OpenSpec：

1. Proposal：为什么、范围、capabilities、影响。
2. Design：关键决策、替代方案、风险和交付计划。
3. Specs：长期 MUST/SHALL 行为和 scenarios。
4. Tasks：可执行、可验收动作。
5. Apply：逐项实现并即时勾选。
6. Sync：delta specs 同步到主 specs。
7. Validate：strict、测试、文档和 Git 检查。
8. Archive：全部完成后归档。

一个 change 只解决一个明确问题。跨多个交付阶段、无法独立验收或无法提交级回滚时必须拆分。

## 客户端进入门

项目实现顺序由 `docs/roadmap.md` 控制。

`qualify-server-v1` 完成前：

- 可以维护 Unity 架构与接入文档。
- 不得创建 Unity runtime、scene、prefab，或把 C# generated code 写入 Unity 工程和 Git；S0 只允许临时 generation qualification。
- 不得用 Unity 手工联调替代服务端 contract tests。

`qualify-server-v1` 必须冻结：

- schema/message ids/errors
- route registry
- endpoint manifest
- session/ticket behavior
- HTTP/WSS/TCP contract fixtures
- PersonalWorld、WorldInstance、VisitSession、admission 与 safe-return 语义

冻结后客户端依次实现 Composition、协议生成、HTTPS、WSS、TCP、业务 Services、双 UI 和 vertical slice。

## 个人世界服务端进入门

PersonalWorld、WorldInstance、VisitSession 与 placement 构成第一业务里程碑，但必须按路线分别实现和验收，不能作为 storage、transport 或 Unity change 的附带代码。ActivityInstance、Room 与 Party 不属于服务端 v1。

个人世界服务端 change 必须按依赖分别证明：

- PersonalWorld identity、immutable owner、持久 revision 和子领域 owner
- WorldInstance assignment、lease/fencing、启动、休眠、重建和陈旧实例拒绝
- VisitSession invite/admission、Owner/Visitor role、capacity、grace、expiry 和安全返回
- PlayerState、PersonalWorldState、VisitSessionState 与 ActivityInstanceState 的 mutation/settlement owner
- MySQL 持久事实、Redis 可失效运行态、幂等、事务、outbox、恢复和清理
- Go test client 的 own-world、visit、disconnect、rebuild 和 stale-admission 验收

对应 Unity change 必须等待上述服务端能力和跨端契约冻结，并按 PersonalWorld/VisitSession pure C# Service、WorldAdmission、SceneContext adapter、UI 的顺序接入。Room/Party 只有活动准备或连续组队需求成立时才进入独立 change，不作为访问个人世界的前置条件。

## Change 进入条件

每个 proposal 必须说明：

- 上游 changes 和 artifacts
- 业务 owner 与状态 owner
- 协议、存储和安全影响
- 不做什么
- 自动化测试策略
- 完成门槛
- 回滚到哪个可运行提交
- 涉及个人世界时的 actor role、mutation owner、settlement owner、运行 assignment 与 safe-return 语义

进入条件不满足时只能补充设计或调查，不能预建占位运行时。

## Task 规则

- 使用 `- [ ] X.Y` 格式。
- 一个 task 产生一个清晰可验证结果。
- 决策写 design，不写成“决定是否使用”。
- 行为写 spec，不只存在 tasks。
- 目录/接口只在有真实实现时创建。
- 测试、文档和观测与实现 task 同步。

## 实现规则

- 开始前读取 proposal、design、specs、tasks 和 owner docs。
- 保持 scope 聚焦，不顺手重构无关模块。
- 每完成一个 task 立即勾选。
- 发现设计假设失效时先更新 artifacts。
- 不用 TODO、feature flag 或 adapter 掩盖未完成实现。
- 不手工修改生成代码。
- 不覆盖他人无关工作区改动。

## 协议与 Transport Change

必须回答：

- message delivery semantics
- unique allowed channel
- session/ticket/scope
- framing/max size/deadline
- idempotency/rate limit
- reconnect/failure behavior
- TLS/AEAD/replay/security
- metrics 与可重复网络模拟

禁止同一 command 的 WSS/TCP 双入口。

## 存储 Change

必须回答：

- 持久事实 owner
- table/key schema
- transaction/consistency
- TTL/recovery/cleanup
- migration 与 rollback
- dependency failure behavior
- contract/integration tests

## Unity Change

必须回答：

- App Scope/Scene Scope owner
- 纯 C# Service 与 Unity Host 边界
- bootstrap/lifecycle/rollback/shutdown
- Scene/Prefab/ScriptableObject 数据类型
- screen owner/input/layer/lifecycle
- main-thread 与 cancellation
- EditMode/PlayMode/build 验收

## 验收

Change 完成至少满足：

- 所有 tasks 勾选
- 主 specs 已同步
- OpenSpec strict 通过
- `docs/engineering-standards.md` 的质量门通过
- 提交消息符合 `docs/git-commit-convention.md`
- 无 unresolved conflict
- 无无 owner 协议、table、key、listener 或 interface
- 无密钥、缓存、本机配置和被 Git 跟踪的 generated code
- README/docs 与实现一致

服务端资格 change 额外要求 Go unit/integration/contract/race、恢复、并发、背压和 shutdown 全部通过。

## 归档

归档前确认：

- artifacts 完整
- tasks 无未完成项
- specs 同步
- 验证结果记录
- 路线图状态更新
- 后续 change 的进入条件明确

归档只表示该 change 的目标完成，不代表未实现的长期架构已经存在。
