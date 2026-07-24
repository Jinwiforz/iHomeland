## MODIFIED Requirements

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
