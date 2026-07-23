## Context

`define-authoritative-gameplay-architecture` 已冻结 C++ Game Simulation Server、Go 控制/持久事实与 Unity replica 的 owner 边界；`define-battle-simulation-model` 又以 `shared/contracts/fixtures/battle/model/` 冻结了 `battle-model-v1` 的 Tick、输入、状态、查询、事件、history、overload 和 workload 维度。当前 model assumptions 明确把 simulation/input step、窗口、instance capacity、CPU、memory、queue 与 bandwidth 标为 `unmeasured`，因此 B0.3 不能合法选择固定步长、history 容量或网络相关内存布局。

本 change 是 `simulation model -> network profile -> C++ core` 的第二项。它要回答“在什么网络包络和资源预算内传送已冻结模型”，而不是实现 gameplay、wire codec 或 production transport。主要消费者是后续 C++ core、Go/C++ control、安全 UDP/KCP、battle network qualification 与 Unity gameplay runtime。

profile 必须覆盖三类 workload：solo Owner、默认 1 Owner + 4 Visitor，以及 1 Owner + 32 Visitor 的配置兼容性。第三类用于决定后续是否需要 battle admission capacity gate，不代表 profile 必须支持 33 人。

## Goals / Non-Goals

**Goals:**

- 绑定并验证唯一的 model manifest/case digest，防止 profile 使用漂移或删减后的 gameplay 输入。
- 通过确定性离散事件网络模拟选择 cadence、window、baseline、MTU、raw redundancy、KCP 与 queue 参数。
- 为每个逻辑 battle message class 冻结唯一 direction、lane、QoS、expiry、大小、频率、幂等和恢复语义。
- 分别报告各 workload/phase 在目标 fault matrix 下的 input freshness、snapshot age、baseline recovery、reliable delivery、queue、bandwidth 与放大率。
- 冻结后续实现必须满足的 CPU/Tick、memory/history、queue 和 instance capacity target budget，同时诚实标注尚未由真实 C++/UDP 实现资格验证的项目。
- 提供版本化 source corpus、canonical evidence、只读 validator 和可重复失败回归。
- 保持现有 Go/Unity v1、HTTPS/WSS/TLS-TCP、PersonalWorld/VisitSession owner 和 production 端口不变。

**Non-Goals:**

- 不创建 C++ 工程、CMake target、ECS、simulation worker、Jolt、Detour、Asio 或 KCP runtime。
- 不创建 `.proto`、generated code、numeric message ID、datagram header、加密 framing、ticket 或 production listener。
- 不用 synthetic simulator 声称真实 C++ CPU、真实 socket、真实 KCP library 或跨平台性能已经 qualified。
- 不定义 cookie、AEAD、nonce/key epoch、replay window、endpoint rebinding、抗放大实现或 UDP 端口。
- 不改写 battle model case、状态迁移、VisitSession capacity 或当前 Go admission。
- 不实现 Unity prediction/interpolation，也不开放 gameplay runtime 进入门。

## Decisions

### 1. Profile 独立于 model corpus，并以完整 digest 绑定

新增目标结构：

```text
shared/contracts/fixtures/battle/network-profile/
  README.md
  schema.json
  manifest.json
  model-binding.json
  profile.json
  message-inventory.json
  fault-matrix.json
  reports/
    qualification.json
  cases/
    cadence/
    baseline/
    reliable/
    mtu/
    overload/
    negative/
```

`model-binding.json` 记录 model format/model version、manifest SHA-256、assumptions SHA-256、每个 case ID/path/digest 和 required coverage。validator 每次先运行现有 battle-model validation，再重算 binding；任何缺失、额外、漂移或顺序变化都 fail closed。profile 不修改 `model/assumptions.json` 的 `unmeasured` source facts，而在自己的 `profile.json` 中给出选择结果、证据状态和 consumer contract。

选择独立目录而不是向 model case 填网络字段，是为了保持 gameplay 语义 owner 单一，并允许未来 profile revision 在不改写同一模型的情况下重测。替代方案是把 profile 输出直接写回 assumptions；这会混淆模型输入与网络资格结果，因此不采用。

### 2. 资格状态分三层，不用合成结果冒充实现性能

每个 parameter 或 budget 必须属于以下状态之一：

| 状态 | 含义 |
|---|---|
| `target_budget` | 后续实现必须满足的上限或目标，不是已测实现结果 |
| `profile_qualified` | 已由本 change 的确定性 fault matrix 和静态大小模型重复验证 |
| `implementation_required` | 必须在 C++ core、安全 transport 或 network qualification 以真实实现补证 |

cadence、窗口、MTU payload budget、lane、baseline policy、fault envelope 和逻辑 queue 上限可成为 `profile_qualified`。真实 encode size、C++ CPU、allocator/memory、socket buffer、KCP implementation amplification、加密开销与平台网络行为必须保持 `implementation_required`，但 profile 要给出 hard upper budget，使后续 change 能 fail closed。

`qualification.json` 同时列出 selected candidates、rejected candidates、metric distributions、threshold、source digest、tool version、seed 和 classification。report 中出现 missing、skipped、stale source、overflow 或未分类 measurement 时，整体状态必须为 `not-qualified`。

### 3. 以整数离散事件模拟冻结 cadence 与网络窗口

工具目标入口为：

```powershell
tools/battle-network-profile/battle-network-profile.ps1 -Action validate
tools/battle-network-profile/battle-network-profile.ps1 -Action simulate
tools/battle-network-profile/battle-network-profile.tests.ps1
```

simulator 只处理逻辑 payload size、send tick、sequence、lane、loss/delay/reorder/duplicate、queue 与 deadline，不计算 gameplay state，也不实现第二套 ECS/Ability/physics。所有时间使用整数纳秒或微秒，概率采样使用 manifest 固定 seed 与明确 PRNG 算法，排序使用 `(delivery_time, direction, lane, sequence, copy_index)`；不得读取 wall clock、线程或 locale。

候选搜索同时选择：

- `simulation_step_ns` 与 `input_step_ns` 的整数比例；
- input early/late window、continuous hold、bundle depth 与 redundancy；
- snapshot cadence、full baseline interval、delta baseline fan-out；
- interpolation delay、maximum extrapolation、correction position/angle tolerance；
- history window 与 mapping epoch reset/drift threshold；
- raw/KCP queue limit、per-message deadline 与 backpressure threshold。

选择器先满足正确性 hard gate，再按明确排序最小化 per-player bandwidth、worst-case snapshot age、reliable amplification 与 memory budget；同分时使用参数 tuple 的规范升序，禁止人工依赖 map 枚举顺序。

替代方案是直接写一组经验默认值。它无法证明 fault matrix 和 workload coverage，也容易让 C++ core 固化隐藏假设，因此不采用。

### 4. Fault matrix 覆盖可重复的独立维度与组合压力

`fault-matrix.json` 登记目标 latency、jitter、loss、reorder、duplicate、burst length、direction、workload、phase、duration 和 seed。矩阵至少包含：

- clean baseline；
- 上下行独立 latency/jitter；
- raw loss 与 burst loss；
- reorder/duplicate；
- baseline 丢失与连续 delta 缺口；
- KCP segment loss/retransmit；
- MTU 边界与超限；
- queue saturation、slow consumer 和 disconnect/drain；
- 默认 coop 与 capacity compatibility workload 的组合压力。

每个 case 以有限事件数和整数 duration 运行，报告 input age、gap termination、snapshot age、baseline recovery time、reliable deadline success、duplicate suppression、queue high-watermark、drop/reject reason、bytes/player、bytes/instance 和 KCP amplification。fault 组合必须来自 manifest，不允许 CI 运行时随机扩展无法重放的矩阵。

本阶段是 deterministic profile simulation，不替代 B0.6 的真实网络 fault injection。后者必须使用同一参数与 workload manifest 对 C++/Unity/UDP 实现补证。

### 5. Message inventory 使用稳定逻辑 kind，不提前分配 wire ID

`message-inventory.json` 使用稳定 logical kind，例如：

- `battle.input.bundle`
- `battle.snapshot.full`
- `battle.snapshot.delta`
- `battle.entity.lifecycle`
- `battle.ability.reliable-event`
- `battle.resync.request`
- `battle.resync.response`
- `battle.probe`

每项登记 owner、direction、唯一 `raw` 或 `kcp` lane、QoS、max logical payload、send rate、expiry/deadline、sequence、Tick、idempotency、baseline 和 recovery policy。full/delta snapshot 始终为 raw unreliable-sequenced，不能进入 KCP；KCP 只接受丢失不可接受且在 deadline 内仍有价值的登记 kind。InputBundle 是否携带离散边沿必须由 inventory 明确，不能由调用方动态换 lane。

这些 logical kind 是 B0.5 numeric message registry/schema 的强制输入，但不是 production wire ID。B0.5 必须为每个 kind 分配唯一 numeric ID 并验证 profile parity；不得在本 change 创建占位 `.proto` 或未来难以迁移的临时编号。

选择逻辑 inventory 是为了让 B0.2 能完成“每类消息唯一 lane”并计算预算，同时保持 wire、安全 framing 与兼容性由真正 transport change 一次性交付。

### 6. MTU 预算按完整 datagram 上限建模，禁止 IP 分片

profile 必须明确：

```text
max_datagram_bytes
- ip_udp_overhead_budget
- secure_session_header_budget
- aead_tag_budget
- lane_header_budget
= max_logical_payload_bytes
```

所有预算以 bytes 和 IP family assumption 标注，任何加法/乘法使用 checked integer。初始候选可包含文档已有的 1200-byte 假设，但只有 fault/MTU cases 通过后才能选为 `profile_qualified`。任何 logical message 超过其 lane payload 时必须在 source profile 中选择 bounded application split、减少 bundle/delta，或拒绝；禁止依赖 IPv4/IPv6 分片、隐式 codec 压缩或把 snapshot 转入 KCP。

因为本 change 尚无最终 AEAD/header/wire，安全与 framing 开销只能以保守 budget 预留，并标记 B0.5 必须验证的 `implementation_required` parity。若实际 wire 超出预留，B0.5 必须更新 profile change，不能静默缩减安全字段。

### 7. Snapshot 采用有界 full/delta baseline 图

每个 delta 只能引用 profile 允许且客户端已确认可用的 baseline identity；profile 冻结 maximum baseline age、允许 fan-out、full baseline cadence 和 resync rate。丢失 delta 时客户端等待后续可解码 delta 或 full baseline；不得通过 KCP 重传连续 snapshot。缺少任何合法 baseline 时，客户端发送登记的 KCP resync request，服务端按 rate/deadline 返回登记的恢复控制数据或安排 raw full baseline。

模拟必须覆盖 full baseline 丢失、多个 delta 缺口、旧 snapshot reorder、重复 snapshot、resync storm 和 slow consumer。通过门同时限制收敛时间、snapshot age、上行 resync rate、下行 burst 和 queue watermark。

替代方案是每个 snapshot 都发 full state，简单但无法满足 compatibility workload 带宽目标；另一替代是可靠重传 snapshot，会把已过时状态堆入 KCP，均不采用。

### 8. KCP profile 冻结参数与应用 deadline，但保留真实实现补证

profile 登记 KCP conversation scope、update interval、no-delay policy、send/receive window、fast resend、RTO bounds、dead-link、segment/message ceiling、queue limit 和 application expiry。simulator 以有限 ARQ 状态验证 loss/reorder/duplicate 下的 deadline delivery、去重、ordered head-of-line、重传字节和 queue；它不复制完整第三方 KCP core，也不声称第三方实现已通过。

B0.5 引入精确 KCP dependency 后必须以相同 traces 做 adapter parity，并在参数漂移时 fail closed。KCP 与 raw lane 共享未来安全 UDP session；它不是 UDP 阻断 fallback，不能把过期可靠 gameplay 消息交给 simulation。

### 9. Capacity 结论显式区分默认支持与配置兼容

`profile.json` 必须分别登记：

- `qualified_default_players`：至少覆盖默认 1 Owner + 4 Visitor 才能通过 B0.2；
- `evaluated_max_players`：矩阵实际评估上限；
- `visit_configured_max_players`：当前 hard contract 的 33 actors；
- `capacity_gate_required`：当 qualified 上限小于 configured 上限时为 true；
- `capacity_gate_owner`：后续 Go/C++ control/admission change；
- `capacity_gate_status`：本 change 固定为 `required-not-implemented` 或 `not-required`。

compatibility workload 失败不会强迫本 change 修改 VisitSession，但必须生成稳定结论并保持 production battle admission 关闭。后续 secure battle ticket/admission 必须在签发前检查 profile capacity；当前 Go v1 world/visit 行为不受影响。

### 10. Profile 通过门要求 canonical、只读且全覆盖

validator 检查：

- schema、manifest 双向登记、ID/单位/枚举、规范排序与 SHA-256；
- model binding 无漂移；
- 所有 logical kind 唯一 lane 且没有 raw/KCP/TCP/WSS 双写；
- snapshot 不走 KCP，过期 reliable message 不进入 simulation；
- fault matrix 覆盖所有 workload/phase/impairment requirement；
- selected parameter 有通过 evidence，rejected parameter 有稳定理由；
- budget 算术、MTU、queue、bandwidth、amplification 和 capacity gate 自洽；
- report 没有 missing/skipped/stale/unclassified；
- 连续两次 validate/simulate 不修改 corpus 且得到同一 digest。

校验失败不得自动修复 source、重写 digest 或保留部分 qualified 状态。工具不启动网络、Docker、Go server、Unity 或未来 C++ process。

## Risks / Trade-offs

- [合成网络模型与真实 socket/KCP 存在差距] → 将实现相关指标保持 `implementation_required`，B0.5/B0.6 复用同一 traces 做 adapter 与真实网络补证。
- [过早锁死 cadence 影响后续 C++ 性能] → cadence 是 B0.3 hard input；若真实 core 无法满足，必须回到独立 profile change 重测，不能在 C++ 中隐藏降频。
- [logical kind 与最终 wire schema 不一致] → B0.5 必须建立 kind-to-message-id 一一映射与 parity gate，未映射或拆分必须先更新 profile。
- [33 人 compatibility workload 导致默认 profile 无法收敛] → 默认 5 人为最低通过门；更低 qualified cap 禁止通过，5 至 32 Visitor 之间的差距由显式 capacity gate 管理。
- [预留安全/header 开销偏小] → 使用保守 checked budget；真实 wire 超限时 fail closed 并重走 profile，而不是删减安全字段或允许 IP 分片。
- [网络模拟工具演化成第二套 runtime] → 工具只消费逻辑事件与 byte budget，不执行 gameplay、真实 codec、socket 或完整 KCP dependency。

## Migration Plan

1. 提交 profile schema、manifest、model binding、message inventory、fault matrix 和初始 cases，不修改现有 model corpus。
2. 实现只读 validator 与确定性 simulator，先建立 negative regression，再生成并审查 canonical qualification report。
3. 将选定参数、预算、未完成 implementation qualification 与 capacity gate 结论同步到 owner docs 和 roadmap。
4. 运行 battle model/profile validation、相关文档检查、OpenSpec strict 与 `git diff --check`。
5. 回滚时整体移除本 change 新增的 profile corpus、工具与文档变更，恢复到已归档 B0.1 的可运行提交；现有 Go/Unity v1 无迁移或数据回滚。
