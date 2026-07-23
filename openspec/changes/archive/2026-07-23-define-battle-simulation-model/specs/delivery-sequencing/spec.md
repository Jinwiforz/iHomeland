## ADDED Requirements

### Requirement: 服务器权威 gameplay 必须按模型证据顺序交付

服务器权威 gameplay MUST 在 `define-authoritative-gameplay-architecture` strict 验证和归档后，依次完成 battle simulation model、battle network profile、C++ simulation core、Go/C++ control、安全 battle transport、网络资格、Unity gameplay runtime 与 PersonalWorld combat slice。Simulation model MUST 先冻结 Tick/Input 映射、输入 vocabulary、pipeline、移动/跳跃、物理 port、Ability/Effect/Damage/Death、AI、history、overload、determinism、纯模型 fixtures 与预算假设；network profile MUST 以这些产物测量并冻结 cadence、window、容量和网络预算；C++ core MUST 以同一 corpus 进行无网络验收。前一阶段缺少 strict 规格、机器可读 evidence 或完成门时，后一阶段 MUST NOT 通过占位类型、隐藏默认值或临时 listener 绕过进入条件。

#### Scenario: 在模型前定义网络参数
- **WHEN** proposal 在 simulation model 尚未 strict 通过时尝试冻结 tick rate、snapshot rate、输入窗口、历史长度、KCP 参数或带宽预算
- **THEN** 评审必须先完成 B0.1 的 command/state/capacity/fixture 基线，网络 change 不得用假设消息量反向定义 gameplay

#### Scenario: 在 network profile 前实现 C++ core
- **WHEN** proposal 尝试安装 Jolt/Detour/Asio、创建 CMake simulation target 或实现 production gameplay loop，但 model corpus 或经测量 network profile 尚未完成
- **THEN** 评审必须保持实现门关闭；只允许在 B0.1 内提交无第三方、无 listener 的模型数据与校验工具

#### Scenario: C++ core 开始无网络验收
- **WHEN** battle simulation model 与 network profile 已分别 strict 验证且 C++ dependency 版本/来源/checksum/license 获批
- **THEN** C++ core 可以实现离线/loopback harness，并必须使用已版本化的 model fixtures、参数和确定性边界，而不是建立第二套未登记规则

#### Scenario: UDP listener 被提前开放
- **WHEN** simulation model、network profile、Go/C++ control、ticket/AEAD/replay/限流/抗放大或网络资格任一未完成
- **THEN** production UDP/KCP listener、端口和客户端 battle route 保持关闭，现有 HTTPS/WSS/TLS-TCP 不提供静默 gameplay fallback
