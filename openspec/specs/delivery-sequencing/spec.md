# Delivery Sequencing 规格

## Purpose

定义项目从协议基线到服务端 v1、Unity 客户端和战斗阶段的严格交付顺序，以及服务端资格门、第一里程碑范围、客户端进入条件和每个 change 的独立验收边界。

## Requirements

### Requirement: 项目必须按契约依赖顺序交付
项目 MUST 先完成基础协议治理、服务端 runtime、session 与 account，再依次完成 PersonalWorld domain、WorldInstance placement model、MySQL/Redis runtime、个人世界存储与 placement adapters、VisitSession domain、world/visit protocol、HTTPS/WSS/TLS-TCP adapters 和 Go 协议测试客户端资格验收，最后开始 Unity 运行时实现。Room、Party 与 ActivityInstance MUST NOT 成为 PersonalWorld 或 VisitSession 的前置条件。

#### Scenario: 服务端尚未通过资格验收
- **WHEN** `qualify-server-v1` 仍有 own-world 或 visit-world 验收项未完成
- **THEN** Unity 只能维护架构文档和契约评审，不得实现依赖未冻结 world/visit 行为的运行时代码

#### Scenario: 路线尝试提前实现 Room
- **WHEN** PersonalWorld 与 VisitSession 服务端竖切尚未完成，而 proposal 引入 Room、Party 或 ActivityInstance runtime
- **THEN** 评审必须移除这些能力，除非存在独立产品需求和不依赖个人世界主线的验收边界

#### Scenario: 服务端 v1 后推进客户端
- **WHEN** own-world、visit-world、断线恢复、陈旧 admission 拒绝和持久化恢复均由 Go 测试客户端验收
- **THEN** Unity 按冻结契约实现 App Scope Services、Scene adapters 和双 UI 页面，不反向定义服务端语义

### Requirement: 服务端 v1 必须具备独立消费者验收
服务端 MUST 交付 Go 协议测试客户端、contract fixtures 和 golden packets，使账号、会话、HTTP/WSS/TCP、own-world 与 visit-world 流程不依赖 Unity 即可验证。

#### Scenario: 验证个人世界与访客联机
- **WHEN** Go 客户端完成登录、进入自己的 PersonalWorld、邀请 Visitor、断线重连并结束 VisitSession
- **THEN** 服务端维持单调 revision、唯一 writable WorldInstance、不可转移 Owner、幂等 membership 和明确 safe-return 结果

### Requirement: 第一阶段范围必须保持有限
第一阶段 MUST 只包含账号、会话、PersonalWorld、WorldInstance、VisitSession、MySQL/Redis、HTTPS、WSS 和 TLS/TCP，不得混入 ActivityInstance、Room、Party、正式战斗、匹配、观战、回放或完整经济系统。

#### Scenario: 规划 UDP 或 KCP
- **WHEN** battle simulation model 和 network profile 尚未完成
- **THEN** 项目不得创建 UDP/KCP listener、占位协议或战斗运行时

### Requirement: 每个实现阶段必须独立验收
路线图中的每个实现 change MUST 有明确进入条件、产出、自动化测试、完成条件和提交级回滚点。

#### Scenario: 一个 change 同时涉及多个交付阶段
- **WHEN** proposal 同时实现协议、存储、多个 transport 和客户端 UI
- **THEN** 评审必须将其拆分为可独立运行和验收的 changes
