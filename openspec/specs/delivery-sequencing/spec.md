# Delivery Sequencing 规格

## Purpose

定义项目从协议基线到服务端 v1、Unity 客户端和战斗阶段的严格交付顺序，以及服务端资格门、第一里程碑范围、客户端进入条件和每个 change 的独立验收边界。
## Requirements
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

### Requirement: 服务端 v1 必须具备独立消费者验收
服务端 MUST 交付只依赖公开网络和已提交跨端契约的 Go 协议测试客户端、versioned qualification manifest、contract fixtures、golden packets 与可重复单一资格入口，使账号、会话、HTTP/WSS/TCP、own-world 与 visit-world 流程不依赖 Unity 或服务端内部业务包即可验证。Q0 MUST 同时聚合 contract、unit/integration、fuzz/race、真实storage、独立进程故障、资源背压、shutdown、cleanup与文档一致性证据，生成低敏机器报告，并提交冻结摘要和资格文档；任一 mandatory gate 未通过时 MUST 保持客户端运行时进入门关闭。

#### Scenario: 验证个人世界与访客联机
- **WHEN** Go 客户端完成登录、进入自己的 PersonalWorld、邀请 Visitor、断线重连并结束 VisitSession
- **THEN** 服务端维持单调 revision、唯一 writable WorldInstance、不可转移 Owner、幂等 membership 和明确 safe-return 结果

#### Scenario: 资格客户端导入服务端实现
- **WHEN** Go test client直接导入Composition Root、domain/application、storage、protocol codec或transport adapter来构造请求、预期或恢复状态
- **THEN** architecture gate失败，该结果不能作为独立消费者验收证据

#### Scenario: 部分质量门通过
- **WHEN** happy path或storage integration已通过，但任一mandatory fuzz/race、故障恢复、资源、shutdown、cleanup、contract freeze或report gate缺失/失败
- **THEN** `qualify-server-v1`不得归档，Unity C0仍只能维护文档和契约评审

#### Scenario: 服务端 v1 资格完成
- **WHEN** 独立client与全部mandatory分层矩阵在clean contract基线上重复通过，资格报告、主specs和owner docs已同步并归档
- **THEN** 当前v1 schema、registry、fixtures/golden与endpoint交付集成为C0输入，Unity runtime change可以开始但不得反向修改已冻结服务端语义

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

### Requirement: 第一业务里程碑客户端必须通过完整 C3 资格门

客户端 C3 MUST 在 C2 及其权威一致性修复归档后执行，并聚合 clean client restore、secure cross-process session restore、WSS/TCP independent recovery、全部 EditMode/PlayMode、cross-end fixtures、Windows Development/Release Player、双客户端产品流程、服务端故障、stale callback、Scene/UI lifecycle、bounded soak、cleanup 与文档一致性证据。只有同一冻结 contract/build evidence chain 上全部 mandatory gate 通过且资格报告声明 qualified，第一业务里程碑才可标记客户端 v1 qualified；人工 happy path、单一 build、部分绿色 tests 或旧 Player 证据 MUST NOT 解锁后续发布结论。

#### Scenario: 自动测试通过但 Release 与双 Player 缺失

- **WHEN** EditMode、PlayMode 与 Development build 通过，但 Release Player、当前 build 的双客户端故障矩阵或 soak 任一缺失
- **THEN** `qualify-client-v1` 保持未完成，里程碑不得声明客户端 qualified

#### Scenario: 完整 C3 资格通过

- **WHEN** 版本化 manifest 的全部 mandatory 自动与人工场景、两种 Player、低敏报告和 cleanup 在 current contract/build digest 上通过
- **THEN** `qualify-client-v1` 可以归档，后续 change 可引用该客户端 v1 基线但不得绕过新增 capability 的独立 OpenSpec 与回归资格

### Requirement: 服务器权威 gameplay 必须按模型证据顺序交付

服务器权威 gameplay MUST 在 `define-authoritative-gameplay-architecture` strict 验证和归档后，依次完成 battle simulation model、battle network profile、C++ simulation core、Go/C++ control、安全 battle transport、网络资格、Unity gameplay runtime 与 PersonalWorld combat slice。Simulation model MUST 先冻结 Tick/Input 映射、输入 vocabulary、pipeline、移动/跳跃、物理 port、Ability/Effect/Damage/Death、AI、history、overload、determinism、纯模型 fixtures 与预算假设；network profile MUST 绑定完整 model digest，以可重复 fault matrix 冻结 cadence、window、baseline、MTU、逻辑 lane、KCP、容量与网络预算，并显式区分 profile-qualified、target budget 和仍需真实实现补证的指标；C++ core MUST 以同一 model corpus 和 profile 参数进行无网络验收，并 MUST 通过精确 dependency/toolchain、全部 fixtures、Jolt/Detour parity、determinism、sanitizer 与 1/5/8 actor CPU/memory/history/queue evidence 后才能解锁 Go/C++ control。前一阶段缺少 strict 规格、机器可读 evidence 或完成门时，后一阶段 MUST NOT 通过占位类型、隐藏默认值、旧资格标签或临时 listener 绕过进入条件。

#### Scenario: 在模型前定义网络参数

- **WHEN** proposal 在 simulation model 尚未 strict 通过时尝试冻结 tick rate、snapshot rate、输入窗口、历史长度、KCP 参数或带宽预算
- **THEN** 评审必须先完成 B0.1 的 command/state/capacity/fixture 基线，网络 change 不得用假设消息量反向定义 gameplay

#### Scenario: Profile 使用漂移的模型 evidence

- **WHEN** battle network profile 未绑定完整 model manifest/assumptions/case digest，或绑定后 source 已漂移
- **THEN** B0.2 保持 not-qualified，不能把过期或删减 workload 的报告作为 C++ core 进入 evidence

#### Scenario: Profile 冒充真实实现资格

- **WHEN** B0.2 只有确定性网络模拟和静态 byte budget，却把 C++ CPU、真实 KCP、socket、AEAD 或平台性能标记为 qualified
- **THEN** strict/qualification gate 失败；这些项目必须保持 implementation-required 并由后续实现与 B0.6 补证

#### Scenario: C++ core 开始无网络验收

- **WHEN** battle simulation model 与 network profile 已分别 strict 验证，profile qualification report 完整且绑定当前 model digest，并且 C++ dependency 版本/来源/checksum/license 获批
- **THEN** C++ core 可以实现离线/loopback harness，并必须使用已版本化的 model fixtures、profile 参数、容量和预算，而不是建立第二套未登记规则或隐藏默认值

#### Scenario: 在 network profile 前实现 C++ core

- **WHEN** proposal 尝试安装 Jolt/Detour/Asio、创建 CMake simulation target 或实现 production gameplay loop，但 model corpus 或经测量 network profile 尚未完成
- **THEN** 评审必须保持实现门关闭；只允许在 B0.1/B0.2 内提交无第三方、无 listener 的模型/profile 数据与校验工具

#### Scenario: C++ core 只有 happy path

- **WHEN** 离线 harness 能运行部分 model cases，但 exact toolchain/dependency、全量 fixture、negative、Jolt/Detour parity、sanitizer、连续 determinism 或 1/5/8 actor budget evidence 任一缺失/失败
- **THEN** `implement-game-simulation-core` 不得完成，B0.4 Go/C++ control 进入门保持关闭

#### Scenario: C++ core 完整资格通过

- **WHEN** 同一 build/config/model/profile digest 下的全部离线 mandatory gates 通过，CPU/memory/history/queue 的 implementation evidence 与 owner docs 已同步且 strict 验证成功
- **THEN** `establish-go-simulation-control` 可以消费冻结 `SimulationInstance` lifecycle contract，但不得重写 Tick owner、gameplay rules、assignment 或已验收预算

#### Scenario: 离线 core 冒充 production battle runtime

- **WHEN** B0.3 只具备 Windows x64 离线/loopback evidence，却声明 Linux production、Go control、真实 socket/KCP/AEAD、battle network 或 Unity runtime 已 qualified
- **THEN** 资格结论失败；未交付能力继续由 B0.4 至 B0.7 的独立 changes 和 evidence 解锁

#### Scenario: UDP listener 被提前开放

- **WHEN** simulation model、network profile、Go/C++ control、ticket/AEAD/replay/限流/抗放大或网络资格任一未完成
- **THEN** production UDP/KCP listener、端口和客户端 battle route 保持关闭，现有 HTTPS/WSS/TLS-TCP 不提供静默 gameplay fallback

### Requirement: Go/C++ control 必须先于安全 battle transport
B0.4 MUST 只在 B0.3 的同源 Release/ASan qualification、10 个 model cases、连续 determinism、Jolt/Detour parity、1/5/8 actor budget 与主 specs 归档证据无漂移时开始。B0.4 MUST 交付真实跨进程 SimulationNode lifecycle、完整 AssignmentStamp fencing、内部 SimulationTarget、result receipt/replay、C++ crash/Go restart 与现有 v1 regression evidence；只有这些证据在同一 control schema/build digest 下通过，B0.5 才能创建 battle wire、HTTPS battle ticket、Asio UDP listener、KCP、AEAD 或 production UDP 端口。

#### Scenario: B0.3 资格摘要漂移
- **WHEN** Go/C++ control 使用的 C++ binary、model/profile digest、dependency identity 或 qualification receipt 与已归档 B0.3 evidence 不一致
- **THEN** B0.4 qualification 在启动 SimulationNode 前失败，且不得用重新编译但未重新资格的 binary 继续

#### Scenario: Control 仍由进程内占位实现
- **WHEN** production Composition Root 仍注入 `processWorldRuntime`、fake node 或没有真实 C++ process 的 adapter
- **THEN** B0.4 不得完成，安全 battle transport、端口分配和 Unity gameplay runtime 继续关闭

#### Scenario: B0.4 完整通过
- **WHEN** 真实 Go/C++ process control、node health/capacity、start/drain/stop、target fencing、result replay、crash/restart、shutdown 和 v1 regression 全部通过
- **THEN** 项目只解锁 `establish-secure-battle-transport` 提案；battle network qualification 与 Unity gameplay runtime 仍必须等待各自后续 change

#### Scenario: Control change 提前实现 UDP
- **WHEN** B0.4 source、配置、fixture 或测试新增 battle numeric message、UDP/KCP/Asio listener、ticket、cookie、AEAD、replay window 或客户端 endpoint
- **THEN** scope gate 失败并要求将其移至 B0.5，不得以测试或未来占位为由保留

### Requirement: 安全 battle transport 必须先于网络资格与 Unity runtime 独立交付

项目 MUST 在 B0.4 主 specs 同步归档且 control contract 通过后，独立完成 BattleTicket、
numeric registry、wire、cookie/AEAD/replay、raw/KCP listener、capacity、lifecycle 和
implementation correctness。只有 public credential、真实 production listener、
independent protocol client、定向安全负例、current mapping/assignment fencing 和代表性
clean/loss/reconnect 网络 smoke 在 current model/profile/control identity 上通过，才可
开始 Unity battle runtime。完整 12-scenario、1/5/8 capacity、安全/lifecycle、连续
verify、长时 soak 与 finalize MUST 保留给用户显式冻结的最终资格 candidate，不得成为每个
后续功能 change 的默认进入或完成条件；Unity gameplay vertical slice 通过前仍不得声明
产品 combat 或公网网络 qualified。

#### Scenario: 直接提出 Unity battle runtime

- **WHEN** secure transport 只有设计、内部对象 happy path，公开 BattleTicket 到 production UDP listener 的 input/snapshot 路径或代表性网络 smoke 尚未通过
- **THEN** 评审拒绝 Unity network/gameplay runtime 实现，并要求先完成安全 transport 与 network development-readiness 定向证据

#### Scenario: B0.5 report 通过

- **WHEN**BattleTicket、安全wire、真实UDP/KCP、失效与全部既有regression在同一identity下通过
- **THEN**项目只解锁`qualify-battle-network`，不据此声明公网网络或Unity gameplay可发布

#### Scenario: Development-readiness 通过

- **WHEN** 当前 secure transport 的公开 handshake、raw/KCP、input/snapshot、generation fencing、rebind、安全基本负例和代表性 clean/loss/reconnect smoke 通过
- **THEN** 项目可以开始 Unity gameplay runtime，但不得把未执行的完整 capacity/security/lifecycle/soak 标记为最终资格

### Requirement: 最终产品资格必须独立于历史 change 编号

项目 MUST 将 B0.3 至未来功能 changes 的 unit、contract、parity、sanitizer、integration
和 system tests维护为当前 capability suites。用户显式冻结里程碑或发布 candidate 时，
最终资格 MUST 构建一次当前产品并运行这些 current mandatory suites；MUST NOT 为同一
candidate 按历史 change 编号重复构建、逐层 finalize 或要求编号最大的 change 单独替代
完整产品验收。

#### Scenario: 长期路线推进到后续 change

- **WHEN** 项目已完成多个后续功能 change 并由用户请求完整最终验收
- **THEN** 统一工具对当前产品 capability manifest 执行一次完整资格链，历史 B0.x reports 只作为迁移与审计记录，不形成必须逐个重签的线性构建链

#### Scenario: 只验收最后一个新增功能

- **WHEN** 最后一个 change 的定向 tests 通过但当前产品 mandatory capability suites 尚未执行
- **THEN** 该 change 可以完成开发验收，但不能据此生成完整产品 qualified 结论

### Requirement: PersonalWorld combat slice 必须先冻结玩法配置治理

B0.8 `deliver-personal-world-combat-slice` MUST 只在 B0.7 Unity gameplay runtime 与权威移动投影的定向开发验收通过，且 `define-gameplay-configuration-governance` 的 strict specs、机器可读 corpus、reference package、只读 validator、隔离失败回归、owner 文档和 closed validation plan 均完成并归档后开始。配置治理 MUST 先冻结 package identity、typed semantic ID、authority/presentation owner、单位/范围、config/nav/physics binding、instance 不可变与回滚语义；B0.8 才能创建 production package、C++/Go/Unity consumer、地图碰撞/导航、剑/扇子、怪物/Boss 和表现内容。治理完成只解锁 B0.8 提案，MUST NOT 声称内容实现、battle network 或产品 combat qualified。

#### Scenario: 直接在 B0.7 后实现内容
- **WHEN** proposal 在 gameplay configuration schema、identity、owner/parity、validator 或 instance replacement 语义任一未冻结时，直接把伤害/冷却/AI 数值写入 C++ 常量、Unity ScriptableObject 或 Scene/Prefab
- **THEN** 评审必须先完成独立配置治理 change，不得让代码或 Unity 资产反向成为跨 runtime 配置事实

#### Scenario: 配置治理完成后提出 B0.8
- **WHEN** governance corpus 与 validator 完整通过，主 specs/owner docs 已同步归档，B0.7 movement/Owner/Visitor 代表性验收仍无回归
- **THEN** B0.8 可以基于同一 contract 创建非 fixture production package并实现 Go selection、C++ authority loader和Unity presentation mapping

#### Scenario: 治理 baseline 冒充可玩内容
- **WHEN** reference package 只有 governance coverage 与纯数据 validation 证据，却被用于声明剑、扇子、怪物、Boss、地图 collision/navmesh 或双人 Boss 流程已完成
- **THEN** 完成门失败；这些能力仍必须由 B0.8 的 runtime、Unity assets、定向测试与真实 PC build 验收

#### Scenario: B0.8 定向绿色后自动执行最终资格
- **WHEN** production package与combat slice的直接影响 checks通过，但使用者未显式冻结最终 candidate
- **THEN** change可以完成普通开发验收，但不得自动运行或生成完整12场景、1/5/8 actor、安全/生命周期、连续verify、长时soak或finalize结论
