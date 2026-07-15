## MODIFIED Requirements

### Requirement: 项目必须按契约依赖顺序交付
项目 MUST 先完成基础协议治理、服务端 runtime、session 与 account，再依次完成 PersonalWorld domain、WorldInstance placement model、MySQL/Redis runtime、个人世界存储与 placement adapters、VisitSession domain、world/visit protocol、公开 adapter 所需的 production Account/Session storage 与 credential hashing、VisitSession production storage、独立 world admission issuer/verifier、HTTPS/WSS/TLS-TCP adapters 和 Go 协议测试客户端资格验收，最后开始 Unity 运行时实现。任何公开 transport change MUST 先具备其消费的 production store、cryptography 与安全资格组件，不得在 handler/listener change 内顺带创造未独立验收的数据或凭据语义。Room、Party 与 ActivityInstance MUST NOT 成为 PersonalWorld 或 VisitSession 的前置条件。

#### Scenario: 服务端尚未通过资格验收
- **WHEN** `qualify-server-v1` 仍有 own-world 或 visit-world 验收项未完成
- **THEN** Unity 只能维护架构文档和契约评审，不得实现依赖未冻结 world/visit 行为的运行时代码

#### Scenario: 路线尝试提前实现 Room
- **WHEN** PersonalWorld 与 VisitSession 服务端竖切尚未完成，而 proposal 引入 Room、Party 或 ActivityInstance runtime
- **THEN** 评审必须移除这些能力，除非存在独立产品需求和不依赖个人世界主线的验收边界

#### Scenario: 服务端 v1 后推进客户端
- **WHEN** own-world、visit-world、断线恢复、陈旧 admission 拒绝和持久化恢复均由 Go 测试客户端验收
- **THEN** Unity 按冻结契约实现 App Scope Services、Scene adapters 和双 UI 页面，不反向定义服务端语义

#### Scenario: 缺少 production auth storage 时实现 HTTP
- **WHEN** proposal 尝试开放register/login/refresh/logout或connection ticket，但AccountRepository、CredentialHasher或SessionStore仍只有测试实现
- **THEN** 评审必须先拆出并验收production account/session storage capability，HTTP handler不得注入memory adapter、fake hasher或未验证脚本

#### Scenario: 缺少 VisitSession storage 或 admission 时实现公开 world/visit route
- **WHEN** proposal 尝试实现world bootstrap、invite accept、world admission或gameplay join，但VisitSessionStore仍只有测试实现，或一次性admission issuer/verifier与credential原子消费尚未独立验收
- **THEN** 评审必须先分别完成production VisitSession storage与world admission capability，transport handler不得顺带创建Redis状态机、credential claims、第二套credential store或memory fallback
