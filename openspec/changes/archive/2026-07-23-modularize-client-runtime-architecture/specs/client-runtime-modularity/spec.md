## ADDED Requirements

### Requirement: 客户端手写运行时必须具有编译期依赖边界

客户端 MUST 将手写 runtime 划分为 `IHomeland.Client.Foundation`、`IHomeland.Client.Application`、`IHomeland.Client.Infrastructure`、`IHomeland.Client.Presentation` 与保留 Unity 入口的 `IHomeland.Client.Runtime`。依赖 MUST 形成无环 DAG：Application 只依赖 Foundation；Presentation 只依赖 Application/Foundation；Infrastructure 依赖 Application/Foundation 与 `IHomeland.Client.Protocol.Generated`；Runtime Composition 可以依赖全部下层模块和 Unity packages。Foundation、Application 与 Presentation MUST 禁止 UnityEngine 引用，Application 与 Presentation MUST 禁止 generated Protocol、具体 HTTP/WSS/TCP/security adapter 和 Runtime 反向引用。

#### Scenario: Application 引入具体 transport

- **WHEN** Application 源码或 asmdef 新增对 Infrastructure、UnityEngine、generated Protocol 或 `IHomeland.Client.Runtime` 的引用
- **THEN** Unity 编译或客户端架构验证失败并指出具体程序集、源文件和禁止依赖

#### Scenario: 干净环境恢复全部程序集

- **WHEN** 在锁定 Unity Editor、已生成协议和无本机 Library 缓存的干净工程中导入客户端
- **THEN** 五个手写程序集与 generated 程序集按登记 DAG 编译成功，不依赖隐式 auto-reference、全局 DLL 或未跟踪源码

#### Scenario: Unity 序列化资产跨程序集迁移

- **WHEN** 完成模块迁移后打开 BootstrapScene、PersonalWorldScene、产品 Prefab、UXML/USS 与 ScriptableObject 环境定义
- **THEN** 所有序列化脚本引用、Inspector 接线和 `.meta` identity 保持有效，且不存在第二个 AppRoot、Composition、持久 UI root 或缺失脚本

### Requirement: Application port 与 Infrastructure adapter 必须隔离协议和凭据

Application MUST 以窄强类型 port 表达 bootstrap、Session、secure storage、control、gameplay 和 main-thread 能力；port request/result MUST 使用无 Unity、无 generated message 的不可变 Application contract。Infrastructure MUST 在 HTTP JSON、generated Protobuf、socket/frame 与 Application contract 之间完成校验、复制、降敏和失败映射。Password、token、ticket、admission credential 与原始 payload MUST NOT 进入 Presentation、Scene、View State、普通异常、owner registry 或架构报告。

#### Scenario: 业务用例消费网络结果

- **WHEN** Session、WorldAdmission 或 VisitSession 用例调用登记的 HTTP/gameplay port
- **THEN** 用例只接收 Application contract 和封闭 failure，不引用 codec、socket、generated response、任意 message ID 或原始 body

#### Scenario: 调用方尝试绕过强类型 port

- **WHEN** 新代码尝试通过通用 method/path/body、任意 `IMessage`、任意 message ID 或泛型 service resolution 发送未登记请求
- **THEN** 编译期 API 不提供该入口，架构验证不得以动态调用、反射或 service locator 放行

#### Scenario: Credential 到达 transport 边界

- **WHEN** control/gameplay adapter 为当前 session/connection generation 建立连接
- **THEN** adapter 只在登记的一次性 take 边界取得 credential，使用后不可再次读取，Application snapshot、Presentation、日志和异常均不包含 credential 文本

### Requirement: 超大协调器拆分必须保留唯一状态 owner

Session、control connection、gameplay connection、PersonalWorld、VisitSession、current target、recovery intent、route/navigation、presentation 与 Scene generation 的每类最终状态 MUST 恰好有一个 owner。每个 owner MUST 通过封闭 command、不可变 snapshot 和受控 event 暴露行为；拆出的 reducer/policy MUST 为纯计算，flow/transaction MUST 只持有单笔 lease/cancellation 和完成职责所需的窄依赖，不得保存第二份最终状态或直接发布 owner snapshot。

#### Scenario: 异步 flow 迟到完成

- **WHEN** flow 捕获的 session、connection、target、navigation 或 presentation generation 已被更高代际替换后才返回
- **THEN** 唯一 owner 拒绝提交结果，flow 不能覆盖当前 snapshot、恢复旧资源或自行提升 generation

#### Scenario: 拆分产生第二个状态副本

- **WHEN** 新 helper、manager、flow、View 或 qualification 类型长期保存 owner registry 已登记事实的可变副本并具备独立写入路径
- **THEN** owner/架构验证和对应并发测试失败，不能以缓存、兼容层或测试便利为由保留双写

#### Scenario: 仅使用 partial 拆文件

- **WHEN** 原协调器仍通过 `partial`、extension 或共享全部私有字段的 helper 同时承担状态、迁移、I/O 与多个流程
- **THEN** 该 owner 的模块化任务不能判定完成，即使单个源文件行数已经下降

### Requirement: Session owner 必须将状态迁移、用例与凭据资源分离

唯一 Session owner MUST 保留 current account/session/token lineage、local generation、server epoch、invalidation 与 unresolved authority。Register/login、refresh、restore、logout/forget/invalidation 和 ticket/admission lease MUST 由职责独立的 flow/state/registry 组件执行，并只由 Session owner 原子提交。Session Application 代码 MUST NOT 依赖具体 HTTP/security adapter 或 generated/JSON model。

#### Scenario: 旧 refresh 晚于新 login

- **WHEN** refresh flow 返回时新的 register/login 已由 Session owner 提交更高 generation
- **THEN** Session state machine 拒绝旧结果、删除旧 lineage 且不会覆盖新 snapshot、secure record、epoch 或 credential registry

#### Scenario: 安全存储提交失败

- **WHEN** register/login/refresh 候选成功但 secure session adapter 无法原子提交新 lineage
- **THEN** Session owner fail closed，不发布可继续认证的 current snapshot，并按现有低敏 failure 与 cleanup 契约收敛

#### Scenario: Session 组件独立验收

- **WHEN** EditMode tests 使用 fake gateway、secure store 和 clock 验证各 Session flow
- **THEN** 测试无需 Unity object、真实 HTTP、Windows profile 或 generated message，并能分别断言 state transition、single-flight、lease 和迟到提交

### Requirement: Control 与 Gameplay Channel 必须按连接资源职责模块化

Control channel MUST 保留每次 WSS lifecycle/generation 的唯一 owner，并将 ticket/connect attempt、receive pump、sequence/fragment 校验、retry policy 与 typed push dispatch 分离。Gameplay channel MUST 保留每次 TLS/TCP lifecycle/generation 的唯一 owner，并将 connection attempt、reader、writer、pending registry、heartbeat、route dispatch 与 protocol adapter 分离。所有子组件 MUST 绑定 current generation，terminal disconnect MUST 只由 channel owner 发布一次；拆分不得改变消息 allowed channel、framing、QoS、deadline、backpressure、reconnect 或安全语义。

#### Scenario: Control sequence 出现缺口

- **WHEN** receive pump 在 current WSS generation 接受 sequence 1 后收到 sequence 3
- **THEN** pump 返回 protocol terminal，channel owner 只关闭并发布该 generation 一次，不投递 sequence 3、不由 pump 自行重连

#### Scenario: Gameplay response 晚于 request timeout

- **WHEN** pending registry 已按 deadline 完成并移除 request，随后 reader 收到同 correlation response
- **THEN** 迟到 response 被当前协议规则安全消费或终止，不能完成新 generation 的 request、重复回调或修改业务 owner

#### Scenario: Heartbeat 与显式关闭并发

- **WHEN** current gameplay generation 的 heartbeat timeout 与显式 close、safe-return 或 Session invalidation 并发
- **THEN** reader/writer/heartbeat/pending 资源各清理一次，channel owner 产生唯一登记终态且不遗留后台 task

#### Scenario: Channel contract 回归

- **WHEN** 对拆分后的 Control/Gameplay 执行既有 fake socket、registry、golden、network fault 与 backpressure tests
- **THEN** 握手、frame、route、sequence、heartbeat、push、failure cause、重连和 shutdown 结果与现有 WSS/TCP specs 等价

### Requirement: World、Visit 与 Recovery 必须按权威状态和用例 flow 模块化

`PersonalWorldService` MUST 继续唯一拥有 world/assignment projection，`VisitSessionService` MUST 继续唯一拥有 visit/inbox/role/membership projection，`WorldAdmissionCoordinator` MUST 继续唯一拥有 current target/target generation，`ClientConnectionRecoveryCoordinator` MUST 继续唯一拥有 automatic/manual recovery intent。Projection reducer、invite retirement、own/visit/return/reconnect flow、recovery plan 和 failure mapper MUST 与 owner 状态分离；control hint、HTTP response、gameplay response/PUSH 与 Scene/UI 不得越过既有 authority。

#### Scenario: 接受邀请并进入 Visitor target

- **WHEN** current Session、invite revision 和 target lease 合法，accept/admission/JOIN/snapshot flow 依次完成
- **THEN** 各 flow 只返回候选结果，VisitSession 与 WorldAdmission owner 分别提交唯一 projection/target，credential 不离开 channel 边界

#### Scenario: Safe-return 晚于新 target

- **WHEN** 旧 Visitor generation 的 safe-return 或 reconnect completion 在更高 target generation 已提交后到达
- **THEN** target/revision gate 丢弃旧结果，不清除或覆盖新 target，也不由 reducer/flow 直接操作 Scene 或 UI

#### Scenario: Session 失效发生在 recovery 中

- **WHEN** recovery owner 正在执行 control/gameplay 恢复时 Session owner 发布更高 invalidation generation
- **THEN** recovery lease 失效、旧 target 清理并收敛到未认证状态，迟到恢复不能重建连接、world projection 或 presentation

#### Scenario: 各用例可独立测试

- **WHEN** EditMode tests 分别执行 own-world、visit-world、return、reconnect、invite retirement 和 recovery plan
- **THEN** 每项可以使用纯 reducer/fake ports 验证，不需要构造 UI Router、Scene、完整 AppComposition 或真实 listener

### Requirement: UI Router 与产品 Experience 必须分离纯决策和表现事务

唯一 `ClientUiRouter` MUST 保留 route/navigation generation、active/cached/modal snapshot 和有界 transition queue authority，并将 transition planning、layer/input/focus 决策与 Host 副作用 transaction 分离。唯一 `ClientPersonalWorldExperience` MUST 继续作为产品 View/Host action facade，并将 presentation state、不可变 View State projector、Scene/route transaction、Session 失效表现和 recovery 表现分离。Presentation MUST NOT 引用 UnityEngine、generated message、具体 channel/transport 或完整 Composition。

#### Scenario: Candidate Host 在 show 阶段失败

- **WHEN** transition planner 已生成合法计划但 Runtime Host 在 show 阶段失败
- **THEN** transaction 按计划逆序清理 candidate，Router owner 保留原 snapshot并返回既有稳定结果，planner 不持有或修改 Host

#### Scenario: 相同 snapshots 重建 View State

- **WHEN** projector 两次收到值等价且不含 credential 的 Session、world、visit、recovery、Scene 与 presentation snapshots
- **THEN** 两次产生值等价的完整 View State，过程不访问 Unity、router、network、clock、subscriber 或可变全局状态

#### Scenario: 旧 Scene transaction 迟到

- **WHEN** target、presentation 或 Scene generation 已前进后，旧 load/HUD/route completion 才返回
- **THEN** transaction 拒绝提交，不恢复旧 HUD、关闭新 modal、覆盖当前 Scene 或修改业务 owner

#### Scenario: 产品 View 被重建

- **WHEN** UI Toolkit 根或完整逻辑页面 adapter 在 command 存活期间重建
- **THEN** 新 View 只绑定当前不可变 View State 和窄 action，旧 callback 解除，command 生命周期仍由 App/owner lease 管理且不存在第二个 screen owner

### Requirement: Composition 与 Qualification 必须保持显式且只读

`AppBootstrap -> AppComposition -> AppRoot` MUST 继续是唯一应用入口。只有 AppComposition MAY 同时引用 ports 与 concrete adapters，并 MUST 通过构造函数显式创建、验证和登记对象图。AppComposition MUST 使用 Foundation、Infrastructure、Session、Channel、World、Presentation 与 Runtime/Qualification 的两级强类型 Composition：顶层只表达模块顺序、跨模块 ports、lifecycle participants 与 Unity 接线，模块内部创建私有 reducer/flow/registry/transaction 并返回仅供 Composition 使用的封闭 bundle。模块 Composition 和 bundle MUST NOT 成为运行时 service、业务状态 owner、`Resolve<T>()`、类型字典、反射扫描或自动注册入口。

`AppCompositionResult` MUST 收窄为 AppRoot 启动、受控 Unity Host 接线与 qualification 只读入口实际需要的结果；feature MUST NOT 取得完整 result、全局静态容器、按类型查询或隐藏自动注册。Development qualification MUST 只读取登记 owner 的低敏 snapshot/资源计数并通过既有 fault boundary 注入故障，不得直接修改 owner、调用 reducer/transaction 私有路径或进入 Release。

#### Scenario: 顶层装配新增模块内部组件

- **WHEN** Session、Channel、World 或 Presentation 新增只在本模块使用的 reducer、flow、registry 或 transaction
- **THEN** 该组件由对应模块 Composition 显式创建，顶层 AppComposition 只看到封闭模块 bundle 与跨模块 port，不增加全局注册或向 feature 暴露内部类型

#### Scenario: Feature 尝试取得完整容器

- **WHEN** Application、Infrastructure、Presentation、View 或 Scene feature 引用 `AppCompositionResult` 或新增全局 resolve API
- **THEN** 架构验证失败；依赖必须收窄为构造函数 port、action 或不可变 snapshot

#### Scenario: 初始化中途失败

- **WHEN** 新模块中的第 N 个 lifecycle participant 初始化失败
- **THEN** AppLifetime 仍只逆序停止前 N-1 个成功参与者，各资源 owner 最多清理一次，纯 reducer/policy 不进入清理栈

#### Scenario: Release 构建

- **WHEN** 构建锁定的 Windows Release Player
- **THEN** Development qualification profile、fault、soak 与私有诊断入口不进入产物，产品对象图、Scene/UI 和 channel 行为保持完整

### Requirement: 模块化架构必须具有可重复验证和阅读入口

仓库 MUST 提供单一客户端架构验证入口，检查 asmdef DAG、禁止引用、owner registry、Composition 容器边界、Unity 序列化引用和旧单体归属。Owner registry MUST 为 Session、control、gameplay、PersonalWorld、VisitSession、target、recovery、route、presentation 与 Scene 记录唯一 owner、状态、commands、snapshot、module 和测试入口。客户端架构/UI/文件结构文档 MUST 提供启动、登录进入 OwnWorld、接受访问和断线恢复四条从入口到 owner commit 的阅读路径。

#### Scenario: 新增未登记状态 owner

- **WHEN** 新类型发布与已登记状态重复或新的长期可变 snapshot，但 owner registry 未声明唯一 owner 和边界
- **THEN** 架构验证或评审门失败，不能通过增加 `Manager`、缓存或全局 event 隐藏 ownership

#### Scenario: asmdef 引用发生漂移

- **WHEN** 手写 asmdef 被修改为新增反向引用、auto-reference 绕过、错误 `noEngineReferences` 或重新把分层目录纳入单体 Runtime
- **THEN** 单一架构入口失败并输出预期/实际依赖差异

#### Scenario: 开发者按用例阅读

- **WHEN** 开发者从文档选择启动、OwnWorld、Visit 或 recovery 用例
- **THEN** 文档给出稳定 facade、flow、port/adapter、owner commit 与表现提交顺序，并明确哪些生成、资格和 Unity adapter 文件不是业务入口

### Requirement: 模块化重构必须保持全部既有客户端行为和资格

迁移 MUST 先建立 characterization baseline，再按 Session、Channels、World/Visit/Recovery、UI/Experience 和最终 asmdef 顺序形成可运行提交。每一阶段 MUST 删除已替代旧字段、接口和双写路径，不得以 feature flag 长期保留两套 owner。最终 MUST 通过现有 protocol verify/parity、客户端 EditMode/PlayMode、Windows Development/Release build、双 Player 矩阵、client-v1 qualification、OpenSpec strict 与仓库质量门。

#### Scenario: 单个迁移阶段完成

- **WHEN** 一个 owner 或程序集阶段宣告完成
- **THEN** 该阶段 characterization、模块 tests、架构验证、编译和相关 PlayMode 全部通过，旧实现已删除且存在可运行回滚提交

#### Scenario: 玩家可见流程回归

- **WHEN** 对重构后客户端执行恢复或登录、进入 OwnWorld、邀请/访问、safe-return、control/gameplay fault、manual recovery 与 logout
- **THEN** action result、route/Scene 顺序、Session authority、revision/generation、failure、single-flight、network message 和资格证据与既有 specs 等价

#### Scenario: 最终资格不通过

- **WHEN** 任一 mandatory 自动/人工资格项、build digest、cleanup 或契约检查失败
- **THEN** change 保持未完成且不得同步/归档，不通过降低门槛、删除日常 lineage、跳过场景或保留兼容双实现获得通过
