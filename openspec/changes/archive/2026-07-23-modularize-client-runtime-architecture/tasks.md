## 1. 固定基线与架构清单

- [x] 1.1 记录当前 Unity/contract/build digest、手写 asmdef、核心类型依赖、状态 owner、公开/internal action 与最后一次 client-v1 qualification 基线，形成可回滚起点。
- [x] 1.2 运行并记录协议 verify/parity、完整 EditMode、PlayMode、Windows Development/Release build 与现有资格入口；当前基线不绿时停止重构并先修正 artifacts 或基线缺陷。
- [x] 1.3 为 Session、Control、Gameplay、PersonalWorld、VisitSession、WorldAdmission、Recovery、Router 与 Experience 补足现有行为 characterization tests，覆盖 generation/revision、single-flight、failure、cancellation、shutdown 与资源计数。
- [x] 1.4 新建声明式客户端 owner registry，登记每类状态的唯一 owner、commands、snapshot、module、允许协作者和测试入口，并为现状生成只读差异报告。
- [x] 1.5 建立客户端架构验证入口的 report-only 版本，读取 asmdef、owner registry、禁止 namespace、`AppCompositionResult` 引用和 Unity 序列化脚本清单，不在迁移前误阻断现有违规。

## 2. 建立纯合同与协议适配边界

- [x] 2.1 整理 Foundation contracts，将 lifecycle、clock/delay、dispatcher 等无 Unity 通用契约与业务/transport 类型分离，并增加无 Engine/Protocol 编译测试。
- [x] 2.2 在 Application 定义 bootstrap、Session、secure storage、control、gameplay 所需的窄 ports 与不可变 request/result/snapshot，禁止任意 path、message ID、`IMessage` 或 credential 字符串入口。
- [x] 2.3 把 HTTP JSON/model 校验与 Application contract 之间的转换收敛到 Infrastructure mapper，并用现有 HTTP fixtures 证明 operation、错误和敏感信息行为等价。
- [x] 2.4 把 WSS generated envelope/push 与 Application control contract 的转换收敛到 Infrastructure protocol adapter，并覆盖全部登记 route 与负向 golden。
- [x] 2.5 把 TLS/TCP generated request/response/PUSH 与 Application gameplay contract 的转换收敛到 Infrastructure protocol adapter，并覆盖 correlation、route 和 malformed 输入。
- [x] 2.6 把 OS secure storage、HTTP transport、socket factory 与 main-thread concrete adapter 接到新 ports，确认 Application tests 不需要 Unity、真实网络或 Windows profile。
- [x] 2.7 删除已替代的跨层 DTO、generated mapper 和 concrete transport 引用，运行 credential/exception/source 扫描确认 password、token、ticket、admission 与原始 payload 未扩散。

## 3. 模块化 Session 与安全 lineage

- [x] 3.1 实现并测试纯 `SessionStateMachine`，覆盖 register/login/refresh/restore/logout/invalidation/unresolved 的 local generation、server epoch 和迟到提交决议。
- [x] 3.2 实现并测试 `SessionCredentialRegistry`，迁移 connection ticket 与 world admission lease 的 generation/expiry/单次 take/清理规则。
- [x] 3.3 提取 register/login authentication flow，通过 `IClientSessionGateway` 返回候选结果，并保持 password 只存在于当前调用栈。
- [x] 3.4 提取 refresh single-flight flow，覆盖后续等待方取消、secure record 先提交、commit-unknown 和旧 generation 丢弃。
- [x] 3.5 提取 secure restore flow 与 `ClientSessionRestoreCoordinator` 启动入口，覆盖无 record、unsupported、profile-in-use、损坏/binding mismatch 和成功轮换。
- [x] 3.6 提取 logout/forget/forced invalidation/unresolved termination flow，保持 secure record、credential registry、snapshot 与 invalidation event 原子收敛。
- [x] 3.7 将 `SessionCoordinator` 收窄为唯一 Session facade/owner，删除 HTTP/security concrete 依赖、旧流程字段与双写路径。
- [x] 3.8 运行 Session、HTTP、secure storage、restore、并发与 AppLifetime tests，确认外部行为、错误映射和停止顺序等价。

## 4. 模块化 WSS Control Channel

- [x] 4.1 实现并测试 control connection state/attempt，迁移 ticket、URI/subprotocol、socket ownership、handshake 和 generation 规则。
- [x] 4.2 实现并测试单一 `ControlReceivePump`，迁移 fragment、frame cap、binary/envelope、sequence 与 peer close 处理。
- [x] 4.3 实现并测试纯 `ControlRetryPolicy`，迁移 failure classification、预算、backoff 与不可恢复 Session/protocol 决议。
- [x] 4.4 实现并测试 `ControlPushDispatcher`，迁移 typed push、MainThreadDispatcher backpressure 和 Session invalidation sink。
- [x] 4.5 将 `ClientControlChannel` 收窄为 WSS lifecycle/generation owner，确保 terminal 只发布一次、子组件不自行重连或修改业务 owner。
- [x] 4.6 删除旧 receive/retry/dispatch 实现并运行 control codec/channel/contract/fault/shutdown tests，确认只接收边界和全部 route 等价。

## 5. 模块化 TLS/TCP Gameplay Channel

- [x] 5.1 实现并测试 gameplay connection state/attempt，迁移 admission 单次 take、TLS connect、首 command 和 active generation 提交。
- [x] 5.2 实现并测试唯一有界 writer，覆盖顺序、backpressure、取消、terminal 与资源释放。
- [x] 5.3 实现并测试 reader pump，覆盖 frame cap、decode、response/PUSH 分类和 terminal cause。
- [x] 5.4 实现并测试 generation-scoped pending registry，覆盖 request ID、correlation、deadline、caller cancel、迟到 response 与 disconnect 清理。
- [x] 5.5 实现并测试每 generation 唯一 heartbeat owner，覆盖 idle、timeout、protocol failure、显式关闭与并发 terminal。
- [x] 5.6 实现并测试 typed gameplay route dispatcher，迁移 JOIN/RECONNECT、world/visit response 与 PUSH，不提供任意 generated message 入口。
- [x] 5.7 将 `ClientGameplayChannel` 收窄为 TLS/TCP lifecycle/generation owner，统一 reader/writer/heartbeat/pending 的关闭和单次 disconnect 发布。
- [x] 5.8 删除旧嵌套 pending/reader/heartbeat 实现并运行 gameplay protocol/channel/golden/network fault/backpressure/shutdown tests。

## 6. 模块化 PersonalWorld 与 VisitSession

- [x] 6.1 提取并测试 `PersonalWorldProjectionReducer`，覆盖 world revision、assignment generation、同代 identity、lease 续期、tombstone 与 control refresh hint。
- [x] 6.2 将 `PersonalWorldService` 收窄为唯一 world/assignment projection owner，删除 generated/concrete channel 依赖和旧映射路径。
- [x] 6.3 提取并测试 `VisitSessionProjectionReducer`，覆盖完整 snapshot、role/member/lifecycle/revision 和同 revision 冲突。
- [x] 6.4 提取并测试 invite inbox reducer 与 retirement policy，覆盖 expiry、replacement、accept、member replacement、retired PUSH 和 commit-unknown。
- [x] 6.5 分别提取 Open/CreateInvite/Revoke/Kick/Close/Leave typed command flows，保持 role/revision precondition、idempotency 与首次结果语义。
- [x] 6.6 将 `VisitSessionService` 收窄为唯一 visit/inbox owner，删除 generated response、concrete channel、重复映射和共享流程字段。
- [x] 6.7 运行 PersonalWorld/VisitSession reducer、command、PUSH、revision、safe-return 与 AppLifetime tests，确认没有第二份 projection owner。

## 7. 模块化 WorldAdmission 与 ConnectionRecovery

- [x] 7.1 实现并测试纯 `WorldTargetStateMachine` 和 intent lease，覆盖 Inactive/OwnWorld/Joining/Visiting/Returning/ConnectionLost 的合法迁移与 target generation。
- [x] 7.2 提取 `EnterOwnWorldFlow`，覆盖 bootstrap、admission、connect/JOIN、snapshot 和候选 target 结果。
- [x] 7.3 提取 `EnterVisitWorldFlow`，覆盖 accept reservation、admission、JOIN、VisitSession/world snapshot 与失败后安全返回。
- [x] 7.4 提取 `ReturnToOwnWorldFlow` 和 `ReconnectWorldTargetFlow`，覆盖重新解析 assignment、Visitor RECONNECT、stale admission 与 late safe-return。
- [x] 7.5 提取并测试 world admission failure mapper，统一 HTTP/gameplay/protocol/policy/commit-unknown 到封闭 flow failure。
- [x] 7.6 将 `WorldAdmissionCoordinator` 收窄为 current target/target generation owner，删除各 flow 共享可变字段、generated/concrete transport 依赖和双写。
- [x] 7.7 实现并测试 `ConnectionRecoveryStateMachine`、低敏 target descriptor 与纯 recovery plan builder。
- [x] 7.8 分别提取 control recovery 与 gameplay recovery flow，保留 automatic/manual single-flight、总 deadline、session/target gate 和 terminal result。
- [x] 7.9 将 `ClientConnectionRecoveryCoordinator` 收窄为 recovery intent owner，删除 concrete WebSocket/channel failure 依赖和重复 world flow。
- [x] 7.10 运行 own/visit/return/reconnect/recovery/session-invalidation 的并发、故障、stale generation 和资源清理 tests。

## 8. 模块化 UI Router

- [x] 8.1 提取并测试纯 UI transition planner，覆盖 screen/overlay/modal、lifecycle、scene binding、input/focus 计划和幂等决议。
- [x] 8.2 提取并测试唯一 route state 与有界 transition queue，保持 navigation generation、active/cached/modal snapshot 和 overload 语义。
- [x] 8.3 实现并测试 Host transition transaction，覆盖 initialize/bind/show、原子 commit、post-commit failure、rollback、hide/unbind/dispose。
- [x] 8.4 提取 interaction resolver，把最高层 route 转换为无 Unity 的 input/cursor/focus plan，由 Runtime Host adapter 执行。
- [x] 8.5 将 `ClientUiRouter` 收窄为 route/navigation owner 与 facade，删除 Host 副作用细节、重复 planning 和共享 transaction 字段。
- [x] 8.6 运行 Router EditMode 与双 Host PlayMode tests，覆盖并发容量、modal、focus、cursor、scene-bound、stop、subscriber isolation 和重建。

## 9. 模块化 Experience 与产品 View

- [x] 9.1 实现并测试唯一 `PersonalWorldPresentationState`，迁移 lifecycle、presentation generation、active intent lease、failure scope、connection/login 标记与 View State commit。
- [x] 9.2 实现不含 Unity/transport/credential 的 projection input、View State projector 与 failure mapper，覆盖相同输入等价、collections、capabilities 和 authority replacement。
- [x] 9.3 实现并测试唯一 Scene/route transaction，迁移 old Scene invalidation、unload/load、HUD/Shell 提交和 presentation/target/scene generation gate。
- [x] 9.4 实现并测试 Session invalidation presentation transaction，确保其 authority 高于 recovery 并只收敛到 Login。
- [x] 9.5 实现并测试 recovery presentation transaction，确保同 generation Scene/HUD 提交后才关闭 `ConnectionLost`。
- [x] 9.6 迁移 register/login、world、visit、retry、logout 语义 actions 到 Application ports/flows，保持 route token 不自取消已提交 command。
- [x] 9.7 将 `ClientPersonalWorldExperience` 收窄为 AppLifetime、owner 订阅、action facade、快照捕获和 presentation transaction 编排，删除具体 HTTP/WSS/TCP/Scene adapter 依赖。
- [x] 9.8 按 Login、Shell、WorldVisit、ConnectionLost 拆分 `ClientPersonalWorldUiToolkitView` 的绑定/渲染 adapter，保留唯一逻辑 screen owner和统一根生命周期。
- [x] 9.9 运行 Experience/Presentation EditMode、产品 UI Toolkit/uGUI、SceneContext 与 UI reload PlayMode tests，确认旧 callback、single-flight 和 View State 行为等价。

## 10. 建立最终 asmdef 与 Composition

- [x] 10.1 创建 `IHomeland.Client.Foundation` asmdef，迁移纯基础 contracts 并启用 `noEngineReferences`。
- [x] 10.2 创建 `IHomeland.Client.Application` asmdef，迁移业务 owners/models/ports/flows，消除 Unity、Protocol、Infrastructure 和 Runtime 引用。
- [x] 10.3 创建 `IHomeland.Client.Infrastructure` asmdef，迁移 HTTP/WSS/TCP/security concrete adapters、channels、codecs 与 generated mapping。
- [x] 10.4 创建 `IHomeland.Client.Presentation` asmdef，迁移 Router、Experience、View State/projector/transactions，并启用 `noEngineReferences`。
- [x] 10.5 收窄现有 `IHomeland.Client.Runtime` 到 AppBootstrap/AppRoot/AppComposition、Unity config/Hosts/Views/Scenes 和最终 adapter 接线，保持序列化类型 `.meta` 与程序集身份。
- [x] 10.6 更新 EditMode/PlayMode asmdef、每模块 `InternalsVisibleTo` 和 Protocol 引用，使测试只显式引用被测模块且不扩大生产 public API。
- [x] 10.7 将 `AppComposition` 重写为 Foundation、Infrastructure、Session、Channel、World、Presentation、Runtime/Qualification 两级强类型 Composition，模块内部显式创建私有组件并返回封闭 bundle；同步收窄 `AppCompositionResult`，确认 feature 无法取得完整容器、内部 bundle 或按类型解析。
- [x] 10.8 验证新模块的初始化失败回滚、逆序停止、dispatcher/tickable 冻结和 Domain Reload 关闭后的唯一 AppRoot。
- [x] 10.9 删除旧单体归属、临时 adapter、过渡 namespace、未使用 interfaces 和兼容双实现，确保 runtime/protocol generated 仍由统一入口恢复。

## 11. 收敛 Qualification 与架构硬门

- [x] 11.1 将 qualification diagnostics 改为只消费 owner registry 登记的低敏 snapshot/resource counts，不访问 credential、reducer、transaction 私有状态或修正入口。
- [x] 11.2 保持 fault/soak 只通过 production fault boundary 与产品 actions 执行，验证 Release 预处理和构建不包含 Development profile、fault、soak 或本机路径。
- [x] 11.3 将架构验证从 report-only 切换为 hard gate，检查 asmdef DAG、`noEngineReferences`、禁止 namespace、container 引用、owner 唯一性和旧单体目录。
- [x] 11.4 增加 BootstrapScene、PersonalWorldScene、Prefab、UXML/USS、ScriptableObject 和 `.meta` 的序列化完整性验证，失败时报告精确资产/脚本。
- [x] 11.5 生成可审计模块化报告，列出程序集 DAG、owners、facades、flows、ports、adapters、测试入口和仅提示的复杂度指标。

## 12. 文档、完整回归与交付

- [x] 12.1 更新 `docs/client-architecture.md`，记录程序集 DAG、port/adapter、状态 owner、各大协调器拆分后的组件图、生命周期与禁止依赖。
- [x] 12.2 更新 `docs/client-ui-architecture.md` 与 `docs/file-structure.md`，记录 Router/Experience/View/Host/Scene 边界和新目录 owner。
- [x] 12.3 更新 `docs/client-integration.md` 和必要 README/roadmap，提供启动、登录进入 OwnWorld、接受访问、断线恢复四条从入口到 owner commit 的阅读路径。
- [x] 12.4 运行统一协议 generation/verify、Go/C# golden parity、全部客户端 EditMode/PlayMode 与架构 hard gate，修正全部失败。
- [x] 12.5 构建并启动锁定的 Windows Development 与 Release Player，验证 Bootstrap/Scene/Prefab/UI 序列化、退出 cleanup、Release 剪裁和无自动业务副作用。
- [x] 12.6 执行 client-v1 qualification 的 automatic、soak、prepare-manual、双 Player manual 与 finalize 全流程，保持同一 contract/build digest 并记录完整证据。
- [x] 12.7 运行 OpenSpec strict、注释规范、`git diff --check`、生成物/密钥/缓存扫描和相关仓库质量门，确认没有未登记 owner/interface、unresolved conflict 或无关改动。
- [x] 12.8 逐项核对 `client-runtime-modularity` scenarios 与全部既有客户端 specs；所有 tasks 和资格门通过后准备同步主 specs，任何 mandatory 失败均保持 change 未完成并回滚到最近绿色阶段。
