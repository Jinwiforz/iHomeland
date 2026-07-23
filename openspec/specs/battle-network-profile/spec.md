# Battle Network Profile 规格

## Purpose

定义 battle cadence、输入/快照/历史窗口、逻辑 lane、MTU/KCP 参数、故障矩阵、容量预算、机器 evidence 与后续实现资格边界。

## Requirements

### Requirement: Network profile 必须绑定完整且未漂移的 battle model

Battle network profile MUST 记录并验证 model format/version、manifest digest、assumptions digest、全部 case identity/path/digest 与 required coverage。任一 model 文件缺失、额外、摘要漂移、排序漂移或既有 battle-model validation 失败时，profile MUST 保持 `not-qualified`，且 profile 工具 MUST NOT 改写 model source。

#### Scenario: Model case 在 profile 后发生漂移

- **WHEN** 已绑定 case 的内容或 digest 在未更新 profile binding 的情况下改变
- **THEN** profile validation 以稳定 case/path 报告 stale binding，拒绝输出 qualified 结果且不自动重写 digest

#### Scenario: Profile 消费完整模型

- **WHEN** model manifest、assumptions、全部 cases 与 required coverage 均通过现有 model validator且和 binding 一致
- **THEN** profile simulation 只消费这些冻结的 command/state/query/event/capacity/workload 维度，不建立第二套 gameplay 规则

### Requirement: Profile 参数必须具有明确资格分类和单位

每个 cadence、window、size、capacity、CPU、memory、queue 或 bandwidth 参数 MUST 声明稳定 ID、值、单位、适用 workload、source evidence 和 `target_budget`、`profile_qualified` 或 `implementation_required` 状态。未经本 profile fault matrix 证明的值 MUST NOT 标为 `profile_qualified`；没有真实 C++、wire、socket 或 KCP implementation 的指标 MUST NOT 标为 implementation-qualified。

#### Scenario: 合成结果尝试声明真实 CPU 资格

- **WHEN** qualification report 只有确定性网络模拟结果而没有 C++ core benchmark
- **THEN** C++ CPU/Tick 与 allocator/memory 结果保持 `implementation_required`，同时提供后续实现必须满足的显式 target budget

#### Scenario: 参数缺少单位或证据

- **WHEN** selected profile 参数没有登记单位、workload、threshold 或 supporting case
- **THEN** validator 拒绝该 profile，不允许 consumer 使用隐藏默认值补全

### Requirement: Tick、输入与历史窗口必须由可重复候选选择冻结

Profile MUST 冻结 `simulation_step_ns`、`input_step_ns`、整数 mapping ratio、input early/late window、gap expiry、continuous hold、InputBundle depth/redundancy、mapping epoch drift/reset、history window 与最大补偿回看。候选选择 MUST 先通过 freshness、expiry、gap、history 与固定 `dt` hard gate，再按规范化目标和稳定参数排序选择；相同 source、seed 与 tool version MUST 产生相同 selected/rejected 结果和 digest。

#### Scenario: Burst loss 造成 InputTick gap

- **WHEN** fault case 丢失连续 InputBundle 且冗余副本在 late window 内到达
- **THEN** simulator 按冻结 mapping/expiry 只接纳仍有效的 InputTick，连续确认不越过未决 gap，并报告 input age 与 recovery result

#### Scenario: 候选需要改变 simulation dt 才能通过

- **WHEN** 某网络候选只能通过扩大单次 simulation step、跳过 Tick 或按 wall clock 改变模型阶段
- **THEN** 候选被稳定拒绝，profile 不修改 battle model 的固定 `dt` 语义

### Requirement: Snapshot profile 必须保证有界 baseline 恢复与客户端收敛

Profile MUST 冻结 snapshot cadence、full baseline cadence、delta baseline identity/maximum age/fan-out、snapshot sequence、resync rate/deadline、interpolation delay、maximum extrapolation 与 correction tolerance。Full/delta snapshot MUST 使用 raw unreliable-sequenced lane且 MUST NOT 通过 KCP 重传；缺少合法 baseline 时只能等待可解码的后续 snapshot 或使用登记的有界 resync 流程。

#### Scenario: Full baseline 与后续 delta 连续丢失

- **WHEN** fault matrix 使客户端失去当前 baseline 且收到引用不可用 baseline 的 delta
- **THEN** 客户端模型丢弃不可解码 delta，按 profile rate 限制请求 resync，并在 threshold 内获得合法 full baseline或使该 case 失败

#### Scenario: 旧 snapshot 重排到达

- **WHEN** 客户端已经处理更高 SnapshotSequence 后收到旧 full 或 delta snapshot
- **THEN** 旧 snapshot 被丢弃且不能替换当前 baseline、回退 ServerTick 或触发 KCP snapshot 重传

### Requirement: 每个逻辑 battle message 必须只有一个 lane owner

Profile MUST 为每个逻辑 battle message kind 登记 owner、direction、唯一 `raw` 或 `kcp` lane、QoS、max logical payload、rate、expiry/deadline、Tick/sequence、idempotency、baseline 与 recovery policy。连续输入、probe 和 full/delta snapshot MUST 走 raw unreliable-sequenced lane；只有丢失不可接受且在 deadline 内仍有价值的生命周期、重要事件或 resync kind MAY 走 KCP。调用方 MUST NOT 动态换 lane或将同一 kind 双写到 raw、KCP、TLS/TCP 或 WSS。

#### Scenario: Snapshot 被登记到 KCP

- **WHEN** message inventory 将 full 或 delta snapshot 登记为 KCP 或同时登记多个 lane
- **THEN** contract validation 失败且 profile 保持 not-qualified

#### Scenario: Reliable message 超过应用 deadline

- **WHEN** KCP 交付的 logical message 已超过其 Tick/expiry deadline
- **THEN** application model 以稳定 expired 终结且不把消息提交给 simulation，也不改走其他 transport

### Requirement: MTU 与 payload budget 必须禁止隐式分片和安全缩水

Profile MUST 以 checked integer 明确 `max_datagram_bytes`、IP/UDP、future secure session、AEAD tag、lane header 与 logical payload budget。每个 message kind MUST 在其 lane 的 logical payload 上限内；超限时只允许登记的 bounded split、减少 bundle/delta 或稳定拒绝。Profile MUST NOT 依赖 IP 分片、未登记压缩、移除安全字段或把 snapshot 转入 KCP。

#### Scenario: InputBundle 超过 raw payload budget

- **WHEN** selected redundancy 与 command count 使 InputBundle 大于 profile 的 raw logical payload 上限
- **THEN** 该候选被拒绝或按登记的 bounded bundle split 重新评估，不能依赖 IP 分片或截断离散命令

#### Scenario: Future wire 开销超过预留

- **WHEN** 后续安全 transport 的真实 header 与 AEAD 开销使 datagram 超过 profile budget
- **THEN** transport qualification fail closed 并要求更新 profile，不得缩减 ticket、AEAD、replay 或 identity 字段

### Requirement: KCP 参数必须在有限 ARQ 模型中验证并等待真实实现补证

Profile MUST 冻结 conversation scope、update interval、no-delay policy、send/receive window、fast resend、RTO bounds、dead-link、segment/message ceiling、queue limit 和 application expiry。确定性 simulator MUST 验证 loss/reorder/duplicate 下的 deadline delivery、ordered blocking、去重、重传 bytes 与 queue watermark；真实 KCP adapter parity 和平台性能 MUST 标为 `implementation_required`。

#### Scenario: KCP 重传在 deadline 后到达

- **WHEN** segment 经 burst loss 和重传后组成完整 message，但 application expiry 已过
- **THEN** message 以 expired 终结，report 计入重传放大且 simulation 不消费该业务

#### Scenario: Profile 与真实 KCP 参数漂移

- **WHEN** 后续 adapter 使用的 KCP 参数、时钟或 segment ceiling 与 profile 不一致
- **THEN** parity gate 失败，不得以真实实现“更快”或“通常可用”为由绕过 profile

### Requirement: Fault matrix 必须覆盖目标网络包络并生成可重放报告

Profile MUST 使用有限、版本化且固定 seed 的 latency、jitter、loss、reorder、duplicate、burst、MTU、queue 与 disconnect/drain 矩阵，覆盖 solo Owner、默认 coop、capacity compatibility 及其 idle、movement-heavy、combat-heavy、Boss burst、disconnect/drain phases。每个 case MUST 报告 input/snapshot age、baseline recovery、reliable deadline、queue high-watermark、drop/reject、bytes/player、bytes/instance 与 amplification；missing、skipped、stale 或未分类结果 MUST 使整体资格失败。

#### Scenario: 同一矩阵重复运行

- **WHEN** 相同 corpus、tool version、seed 和参数连续运行两次
- **THEN** event ordering、metrics、selected/rejected candidates、qualification 状态与 canonical digest 完全一致，且两次运行均不修改 source corpus

#### Scenario: Fault coverage 缺少 Boss burst

- **WHEN** qualification report 没有 default coop 的 Boss burst 或任一 mandatory impairment 结果
- **THEN** report 标记 missing coverage 且整体保持 not-qualified

### Requirement: Capacity 与预算必须对默认和兼容负载分别作出结论

Profile MUST 至少支持并通过 1 Owner + 4 Visitor 的默认 workload，分别登记 qualified default players、evaluated max players、当前 1 Owner + 32 Visitor hard contract、per-player/per-instance bandwidth、queue、history、memory 与 CPU target budget。若 qualified player limit 低于当前配置上限，profile MUST 登记 `capacity_gate_required`、后续 owner 和 `required-not-implemented` 状态；本 change MUST NOT 修改当前 Go VisitSession admission。

#### Scenario: 默认 coop 无法满足 profile

- **WHEN** selected candidates 不能让默认 5 actors 在 mandatory fault matrix 与 budgets 内通过
- **THEN** B0.2 整体 not-qualified，不能通过降低默认 VisitSession capacity 来伪造完成

#### Scenario: 默认通过但 33 actors compatibility 失败

- **WHEN** 默认 coop 通过而 capacity compatibility workload 超出 bandwidth、queue 或 memory budget
- **THEN** report 明确最大 evaluated/qualified capacity 并要求后续 battle admission capacity gate，现有 Go v1 world/visit 流程保持不变

### Requirement: Profile corpus 与工具必须可验证、只读且无 runtime 依赖

仓库 MUST 提交 versioned schema、manifest、model binding、profile、message inventory、fault matrix、cases、canonical qualification report 和单一 validator/simulator 入口。Validator MUST 检查双向登记、ID/引用/单位/排序/digest、model binding、lane 唯一性、coverage、budget arithmetic 和 qualification completeness；工具 MUST NOT 启动 listener、Docker、Go server、Unity、C++ runtime或安装第三方依赖，也 MUST NOT 自动修复 source。

#### Scenario: Manifest 漏登记 profile case

- **WHEN** cases 目录含未登记文件或 manifest 引用不存在的 case
- **THEN** validation 失败并报告稳定 path，corpus 不能作为 C++ core 输入

#### Scenario: Corpus 含 production secret 或 wire 占位

- **WHEN** profile 文件包含 raw ticket、AEAD key、真实玩家资料、numeric production message ID、UDP 端口或 generated wire code
- **THEN** schema/安全校验失败，低敏 profile evidence 不得携带或提前冻结这些内容
