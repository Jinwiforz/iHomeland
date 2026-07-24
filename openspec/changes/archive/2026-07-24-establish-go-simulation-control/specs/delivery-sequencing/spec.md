## ADDED Requirements

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
