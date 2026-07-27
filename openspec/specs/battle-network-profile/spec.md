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

Profile MUST 冻结 snapshot cadence、full baseline cadence、delta baseline identity/maximum age/fan-out、snapshot sequence、resync rate/deadline、interpolation delay、maximum extrapolation 与 correction tolerance。`battle.resync.request` 与 `battle.resync.response` MUST 各自使用 2250 ms sender application expiry 和 2/s rate limit；其 deadline 只覆盖 sender 恢复控制事务，不延长 raw full/delta snapshot 的 freshness，也不成为 receiver reassembly timer。Full/delta snapshot MUST 使用 raw unreliable-sequenced lane且 MUST NOT 通过 KCP 重传；缺少合法 baseline 时只能等待可解码的后续 snapshot 或使用登记的有界 resync 流程。

Snapshot publisher MUST 只按 committed SimulationTick 驱动 10 Hz cadence，每 2 个 20 Hz Tick 至多发布一次，并在每 10 个正常发布周期生成 full baseline。Ingress 持续繁忙 MUST NOT 饿死 periodic publication；调度延迟 MUST NOT 通过 catch-up burst 补发旧 snapshot。合法 resync MAY 在同一 committed Tick 强制一个新 full baseline，但仍受 resync generation、rate 和 route expiry 约束。

合法 resync 强制 full 后，publisher MUST 在固定 30 committed Tick 的 recovery window 内每 10 Tick 至多发布一个后继 full baseline；重复 resync MUST NOT 延长当前窗口，调度延迟 MUST NOT 追赶补发。所有 recovery full 继续使用 raw lane与既有 2/s 上限，MUST NOT 改变 snapshot freshness、fault、PRNG、KCP RTO 或 2 秒 recovery threshold。

#### Scenario: Full baseline 与后续 delta 连续丢失

- **WHEN** fault matrix 使客户端失去当前 baseline 且收到引用不可用 baseline 的 delta
- **THEN** 客户端模型丢弃不可解码 delta，按 profile rate 限制提交 resync，服务端在不续期的 recovery window 内按既有 2/s 上限发布有界 full，并在 2250 ms sender route expiry 与登记的 2 秒 recovery threshold 内获得合法 baseline 或使该 case 失败

#### Scenario: 旧 snapshot 重排到达

- **WHEN** 客户端已经处理更高 SnapshotSequence 后收到旧 full 或 delta snapshot
- **THEN** 旧 snapshot 被丢弃且不能替换当前 baseline、回退 ServerTick 或触发 KCP snapshot 重传

#### Scenario: Resync deadline 被用于延长 snapshot

- **WHEN** resync response 在 deadline 内到达，但随后收到的 raw full 或 delta snapshot 已超过自身 route expiry
- **THEN** 客户端仍按 snapshot route 拒绝陈旧数据，不能用 2250 ms resync expiry 覆盖 snapshot freshness

#### Scenario: 持续输入占满 transport worker

- **WHEN** ingress queue 在多个 snapshot 周期内持续包含合法 input datagram
- **THEN** periodic worker 仍按 committed Tick 发布 10 Hz snapshot 与每 10 个周期的 full baseline，不能退化为 input acceptance 驱动或饿死 baseline

#### Scenario: Worker 跨过多个 publication deadline

- **WHEN** OS 调度使 worker 从一个 committed Tick 跳到远后的 Tick
- **THEN** publisher 只生成当前最新 projection 的一次 publication，不补发中间旧 Tick 的 catch-up burst

### Requirement: 每个逻辑 battle message 必须只有一个 lane owner

Profile MUST 为每个逻辑 battle message kind 登记 owner、direction、唯一 `raw` 或 `kcp` lane、QoS、max logical payload、rate、精确 sender expiry/deadline、Tick/sequence、idempotency、baseline 与 recovery policy。Message inventory MUST 是 sender application expiry 的唯一 profile source；adapter 或 caller MUST NOT 用 lane 级默认值覆盖 route deadline，receiver MUST NOT 从首个 segment 到达时间推导同名 deadline。连续输入、probe 和 full/delta snapshot MUST 走 raw unreliable-sequenced lane；只有丢失不可接受且在 deadline 内仍有价值的生命周期、重要事件或 resync kind MAY 走 KCP。调用方 MUST NOT 动态换 lane或将同一 kind 双写到 raw、KCP、TLS/TCP 或 WSS。

#### Scenario: Snapshot 被登记到 KCP

- **WHEN** message inventory 将 full 或 delta snapshot 登记为 KCP 或同时登记多个 lane
- **THEN** contract validation 失败且 profile 保持 not-qualified

#### Scenario: Reliable message 超过应用 deadline

- **WHEN** KCP 交付的 logical message 已超过其 route 登记的 Tick/expiry deadline
- **THEN** application model 以稳定 expired 终结且不把消息提交给 simulation，也不改走其他 transport

#### Scenario: Caller 覆盖 route expiry

- **WHEN** adapter caller 为 resync 或其他 KCP message 提交与 registry 不同的 deadline，或尝试使用 KCP lane 统一默认值
- **THEN** contract/runtime gate fail closed，不发送该消息且不修改 registry policy

### Requirement: MTU 与 payload budget 必须禁止隐式分片和安全缩水

Profile MUST 以 checked integer 明确 `max_datagram_bytes`、IP/UDP、future secure session、AEAD tag、lane header 与 logical payload budget。每个 message kind MUST 在其 lane 的 logical payload 上限内；超限时只允许登记的 bounded split、减少 bundle/delta 或稳定拒绝。Profile MUST NOT 依赖 IP 分片、未登记压缩、移除安全字段或把 snapshot 转入 KCP。

#### Scenario: InputBundle 超过 raw payload budget

- **WHEN** selected redundancy 与 command count 使 InputBundle 大于 profile 的 raw logical payload 上限
- **THEN** 该候选被拒绝或按登记的 bounded bundle split 重新评估，不能依赖 IP 分片或截断离散命令

#### Scenario: Future wire 开销超过预留

- **WHEN** 后续安全 transport 的真实 header 与 AEAD 开销使 datagram 超过 profile budget
- **THEN** transport qualification fail closed 并要求更新 profile，不得缩减 ticket、AEAD、replay 或 identity 字段

### Requirement: KCP 参数必须在有限 ARQ 模型中验证并等待真实实现补证

Profile MUST 冻结 conversation scope、update interval、no-delay policy、send/receive window、fast resend、RTO bounds、dead-link、segment/message ceiling、queue limit、允许的最大 sender application expiry 和 receiver reassembly policy。每个 KCP message 的精确 sender application expiry MUST 来自 message inventory：`battle.ability.reliable-event` 与 `battle.entity.lifecycle` 保持 500 ms，`battle.resync.request` 与 `battle.resync.response` 使用 2250 ms。Receiver MUST 由 KCP window/queue 与 session lifecycle 有界管理，不得复制 sender deadline。确定性 simulator MUST 验证 loss/reorder/duplicate 下的 sender route deadline、ordered blocking、去重、重传 bytes 与 queue watermark；真实 KCP adapter parity 和平台性能 MUST 标为 `implementation_required`。

#### Scenario: Sender 重传超过 route deadline

- **WHEN** sender queued/inflight message 经 burst loss 和重传后超过对应 logical kind 的绝对 application expiry
- **THEN** sender 以稳定 inflight expiry 终结，report 计入重传放大且该消息不能继续发送或提交给 simulation

#### Scenario: Receiver 等待 ordered stream 前序缺口

- **WHEN** 后续 KCP message 的 segment 已到达，但完整 delivery 仍等待前序 sequence 重传
- **THEN** receiver 仅受 KCP window/queue 与 session lifecycle 约束，不能从首个 segment 到达时间启动 application expiry，也不能提交未完整消息

#### Scenario: Profile 与真实 KCP 参数漂移

- **WHEN** 后续 adapter 使用的 KCP transport 参数、route expiry、时钟或 segment ceiling 与 profile 不一致
- **THEN** parity gate 失败，不得以真实实现“更快”或“通常可用”为由绕过 profile

#### Scenario: 一般可靠事件尝试继承 resync deadline

- **WHEN** adapter 为 `battle.ability.reliable-event` 或 `battle.entity.lifecycle` 使用 2250 ms resync expiry
- **THEN** route parity 失败且消息不能入队，既有 500 ms 业务价值窗口保持不变

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

### Requirement: B0.5 implementation evidence 必须作为冻结 profile 的只读 overlay

Secure battle transport MUST读取并验证`battle-network-profile-v2`完整manifest与digest，使用真实wire、socket和KCP adapter输出独立versioned implementation report，补证`wire-encoded-size-parity`与`kcp-adapter-parity`等B0.5负责的`implementation_required`指标。实现报告 MUST绑定exact profile/source/binary/dependency/config identity并逐项记录measured value、unit、workload、method和disposition；它 MUST NOT改写B0.2 source corpus、降低MTU/安全开销、改变lane/KCP transport参数、覆盖message route expiry或把B0.6完整fault matrix标为已通过。

#### Scenario: Wire encoded size 超出 profile

- **WHEN**真实secure/raw/KCP envelope使任一登记message超过1200-byte datagram、lane logical payload或1000-byte KCP ceiling
- **THEN**B0.5 implementation report标记失败并定位message/header，不修改profile、不删除安全字段且不提高MTU绕过

#### Scenario: KCP adapter 与冻结参数一致

- **WHEN**exact KCP dependency和adapter对canonical corpus产生与profile一致的segment、route deadline、queue与retransmit disposition
- **THEN**独立report可将该adapter identity标记implementation-qualified，但显式最终 battle network qualification 仍保持未生成

#### Scenario: Profile corpus 被实现工具改写

- **WHEN**B0.5 verify前后任一B0.2 manifest/profile/inventory/case/report bytes或digest变化
- **THEN**资格失败且旧implementation report失效，工具不得自动接受新digest

#### Scenario: 旧 profile 报告尝试继承

- **WHEN** implementation report 绑定 `battle-network-profile-v1` 或旧 digest，而 source 已迁移至 v2
- **THEN** B0.5 binding gate 报告 stale profile，不能沿用旧 implementation-qualified disposition

### Requirement: B0.6 必须以真实网络资格 overlay 补齐冻结 profile

Battle network qualification MUST 只读绑定 `battle-network-profile-v2` 的完整 manifest、model binding、profile、message inventory、fault matrix、cases 和 canonical report，并以真实 Go/C++ process、secure wire、KCP adapter、fault injection 和 1/5/8 actor workload 生成独立 B0.6 overlay。Overlay MUST 对 profile 中每个 `implementation_required` 与 target budget 登记 source evidence、measured value、unit、workload、method、environment class 和 disposition；它 MUST NOT 回写 B0.2 corpus、改变 selected candidate、降低 fault 参数、缩短 mandatory coverage、调整 MTU/lane/KCP/security overhead 或把 33 actor compatibility workload伪装为已支持的 battle actor 数。

#### Scenario: 真实五人矩阵补齐 profile

- **WHEN** current secure transport 在全部 mandatory fault scenarios 下完成五人 workload，且真实带宽、CPU、memory、queue、retransmit、expiry 与 recovery 均在冻结预算内
- **THEN** B0.6 overlay 可把 default-coop 对应实现项标记 network-qualified，并保留 exact profile/source/environment binding

#### Scenario: 结果超限后尝试改写 profile

- **WHEN** 真实 KCP amplification、queue、CPU、memory 或 baseline recovery 超出 target，runner 尝试修改 source profile、seed、scenario、lane、MTU 或 actor 数后重跑
- **THEN** source digest gate 失败，原 qualification 保持 not-qualified，参数调整必须另提 OpenSpec change

#### Scenario: 33 人兼容 workload

- **WHEN** VisitSession 允许 33 人而 battle transport 只允许 8 个 installed/active actor
- **THEN** overlay 保持 `capacity_gate_required`，验证第九个 battle actor 被拒绝且不改变 VisitSession 事实，不要求创建 33 条 battle session
