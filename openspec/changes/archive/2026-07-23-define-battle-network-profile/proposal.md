## Why

`define-battle-simulation-model` 已冻结首个 PersonalWorld gameplay 的命令、状态、查询、事件、容量维度和机器可读 workloads，但 tick/snapshot cadence、输入与历史窗口、MTU、raw/KCP lane、基线恢复和带宽/队列预算仍未测量。现在需要把这些变量收敛为可重复验证的 network profile，才能在不让网络参数反向改写玩法语义的前提下解锁后续 C++ simulation core。

## What Changes

- 新增版本化 battle network profile，绑定已归档 model manifest、assumptions 与 cases 的 canonical digest，冻结 `SimulationTick`/`InputTick` cadence、mapping/window、continuous hold、history、interpolation/extrapolation 与 correction tolerance。
- 定义逻辑 battle message inventory 和唯一 lane：连续输入 bundle、snapshot/full baseline、delta、生命周期、重要事件与 resync 各自只能登记为 raw unreliable-sequenced 或 KCP reliable-ordered，并声明 direction、QoS、expiry、max size、rate、idempotency 与 baseline policy；本 change 不分配 production wire message ID。
- 通过可重复的 latency、jitter、loss、reorder、duplicate、burst 与 MTU 矩阵测量 InputBundle 冗余、snapshot/full-baseline cadence、delta recovery、KCP 参数、queue/backpressure 和上下行带宽，输出 per-player/per-instance 报告及通过/失败理由。
- 区分 `target_budget`、`profile_qualified` 与后续 implementation qualification；冻结 C++ core 必须满足的 CPU/Tick、memory/history、queue 和 instance capacity 上限，但不把无 C++ runtime 的合成测量伪报为实现性能。
- 对 solo Owner、默认 1 Owner + 4 Visitor 和 1 Owner + 32 Visitor compatibility workload 分别给出容量结论；若 profile 上限低于 Go VisitSession 可配置上限，则登记后续 battle admission capacity gate，且不修改当前 Go v1 admission。
- 新增只读 profile validator、确定性网络模拟入口、negative corpus、canonical report/digest 和重复运行不改写 source corpus 的回归测试。
- 更新 gameplay/network owner 文档与 roadmap 状态，明确 B0.2 完成 evidence、B0.3 进入门和仍关闭的 C++ dependency、wire、listener、端口、ticket/AEAD、Go/Unity runtime 门。

## Capabilities

### New Capabilities

- `battle-network-profile`: 定义 battle cadence、输入/快照/历史窗口、逻辑 lane inventory、MTU/KCP 参数、网络故障矩阵、容量预算、机器可读 evidence 与 profile 验证门。

### Modified Capabilities

- `delivery-sequencing`: 将经 strict 验证且绑定 model digest 的 battle network profile 规定为 C++ simulation core 的进入 evidence，并继续阻止 UDP/KCP wire、listener 与客户端 runtime 提前实现。

## Impact

- 影响 `shared/contracts/fixtures/battle/` 下新增的 profile source/evidence、`tools/battle-network-profile/` 验证与模拟入口，以及 gameplay、network、roadmap、file structure 和必要工程标准文档。
- 只消费 `battle-model-v1` 的冻结语义和 workloads；不得改写 model cases、创建第二套 gameplay engine，或把假设值标记为 qualified。
- 不创建 `simulation/`、CMake target、`.proto`、generated code、production message ID、UDP listener 或端口，不安装 Asio/Jolt/Detour/KCP，不修改现有 Go/Unity v1 行为。
- 协议影响限于未来 wire 的 profile 约束；存储无影响；安全边界继续要求后续 battle transport change 同时交付 ticket、cookie、AEAD、replay、限流与抗放大。
- 自动化覆盖 schema/manifest/reference/digest、lane 唯一性、单位与预算、故障矩阵 coverage、重复模拟确定性、MTU/queue/bandwidth 边界和拒绝未登记/漂移输入。
- 回滚点为本 change 开始前、`define-battle-simulation-model` 已归档且 Go/Unity v1 资格保持通过的可运行提交。
