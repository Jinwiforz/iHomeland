## Why

客户端已经具备严格的状态 authority、恢复和资格语义，但全部手写运行时代码仍位于一个程序集，Application 直接依赖 transport/generated 类型，且八个核心 owner/coordinator 已增长到约 1000～2200 行。当前主要风险不再是功能缺失，而是架构只能靠文档约束、一次业务修改必须跨越多个超大状态机，项目 owner 难以阅读、评审和安全扩展。

## What Changes

- 把客户端手写 runtime 建立为有向无环的编译期模块：Foundation、Application、Infrastructure、Presentation 与保留 Unity 入口的 Runtime Composition；纯 C# Application/Presentation 不得引用 UnityEngine、generated Protocol 或具体 transport。
- 在 Application 定义窄业务 port 和无 credential contracts，由 Infrastructure 实现 HTTPS、WSS、TLS/TCP、codec、secure storage 与 generated message 映射；Composition 继续使用具体类型显式接线，不引入第三方 DI 或 service locator。
- 对 `SessionCoordinator`、`ClientControlChannel`、`ClientGameplayChannel`、`PersonalWorldService`、`VisitSessionService`、`WorldAdmissionCoordinator`、`ClientConnectionRecoveryCoordinator`、`ClientUiRouter` 与 `ClientPersonalWorldExperience` 执行结构性拆分：保留唯一状态 owner 和稳定 facade，把纯 transition/policy、pending registry、heartbeat/receive pump、用例 flow、projection、failure mapping 和外部副作用事务提取为窄组件。
- 保持现有 App/Scene Scope、route、action port、Session/token lineage、WSS/TCP 消息、world/visit projection、recovery、safe-return、Scene generation、资格诊断和玩家可见行为兼容；以 characterization tests 固定基线并分阶段迁移，禁止双写 owner、`partial class` 假拆分和长期双实现。
- 拆分 Development qualification 与产品运行时的依赖接线，确保资格代码只读生产 owner snapshot，不成为跨程序集绕过边界的后门。
- 新增可重复的架构门，验证 asmdef DAG、禁止引用、唯一 Composition Root、状态 owner 清单和序列化 Unity 资产完整性；更新客户端架构、UI、文件结构与四条核心业务阅读路径。
- 本 change 不新增业务功能，不修改服务端、协议、存储、transport 语义、Unity 资产视觉或输入设计；不引入日志体系，也不修改代码注释规范，这两项留给后续独立 change。

## Capabilities

### New Capabilities

- `client-runtime-modularity`: 定义客户端编译期依赖图、Application/Infrastructure/Presentation 边界、超大状态机结构性拆分、唯一状态 owner、架构门与行为等价迁移要求。

### Modified Capabilities

- 无。现有 `client-runtime`、`client-http-bootstrap`、`client-websocket-control`、`client-tcp-gameplay`、`client-personal-world-services`、`client-ui-routing`、`client-personal-world-vertical-slice` 与 `client-v1-qualification` 行为规格作为本次重构必须保持通过的回归契约。

## Impact

- 影响 `client/Assets/App/Scripts/` 下几乎全部手写 runtime 的程序集归属、依赖接口和内部实现，以及 EditMode/PlayMode test asmdef、Composition、qualification、架构文档和验证脚本。
- 既有 `IHomeland.Client.Runtime` 继续承载 `AppBootstrap`、`AppRoot`、Unity Hosts、Scenes 和最终 Composition，避免给序列化 MonoBehaviour 建立第二个应用入口；新增下层程序集不允许反向引用 Runtime。
- Session、control connection、gameplay connection、PersonalWorld、VisitSession、current target、recovery intent、route、presentation 与 Scene 的状态 owner 保持唯一；拆出的 policy/flow/transaction 只持有纯输入、单笔 lease 或技术资源，不复制最终事实。
- 协议、message id、allowed channel、endpoint、credential、安全存储 schema、MySQL、Redis 和服务端均无变化。Generated 类型只允许存在于 Protocol/Infrastructure adapter 边界，token、ticket、admission、password 与原始 payload 不得跨入 Presentation、Scene、日志或文档。
- 自动化包括每个现有 owner 的 characterization tests、新纯组件 tests、程序集/禁止引用验证、协议 parity、完整 EditMode/PlayMode、Windows Development/Release build 和 client-v1 qualification 回归。
- 完成门槛为全部 tasks 勾选、`client-runtime-modularity` 主 spec 同步、OpenSpec strict、架构门和现有客户端资格门全部通过，且没有旧程序集双实现、无 owner 接口、被忽略 generated code 或 Unity 序列化引用丢失。
- 实现按模块建立和 owner 迁移设置可运行提交；任一阶段失败时回滚到该阶段前最近一个通过完整客户端门的提交。若整体无法保持资格语义，则回滚到本 change 开始前最后一个已通过 client-v1 qualification 的可运行提交。
