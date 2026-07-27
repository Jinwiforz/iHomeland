## MODIFIED Requirements

### Requirement: Snapshot profile 必须保证有界 baseline 恢复与客户端收敛

Profile MUST 冻结 snapshot cadence、full baseline cadence、delta baseline identity/maximum age/fan-out、snapshot sequence、resync rate/deadline、interpolation delay、maximum extrapolation 与 correction tolerance。`battle.resync.request` 与 `battle.resync.response` MUST 各自使用 2250 ms sender application expiry 和 2/s rate limit；其 deadline 只覆盖 sender 恢复控制事务，不延长 raw full/delta snapshot 的 freshness，也不成为 receiver reassembly timer。Full/delta snapshot MUST 使用 raw unreliable-sequenced lane且 MUST NOT 通过 KCP 重传；缺少合法 baseline 时只能等待可解码的后续 snapshot 或使用登记的有界 resync 流程。

Snapshot publisher MUST 只按 committed SimulationTick 驱动 10 Hz cadence，每 2 个 20 Hz Tick 至多发布一次，并在每 10 个正常发布周期生成 full baseline。Ingress 持续繁忙 MUST NOT 饿死 periodic publication；调度延迟 MUST NOT 通过 catch-up burst 补发旧 snapshot。合法 resync MAY 在同一 committed Tick 强制一个新 full baseline，但仍受 resync generation、rate 和 route expiry 约束。

合法 resync 强制 full 后，publisher MUST 在固定 30 committed Tick 的 recovery window 内每 10 Tick 至多发布一个后继 full baseline；重复 resync MUST NOT 延长当前窗口，调度延迟 MUST NOT 追赶补发。所有 recovery full 继续使用 raw lane与既有 2/s 上限，MUST NOT 改变 snapshot freshness、fault、PRNG、KCP RTO 或 2 秒 recovery threshold。

#### Scenario: Full baseline 与后续 delta 连续丢失

- **WHEN** fault matrix 使客户端失去当前 baseline且收到引用不可用 baseline 的 delta
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

### Requirement: B0.5 implementation evidence 必须作为冻结 profile 的只读 overlay

Secure battle transport MUST读取并验证`battle-network-profile-v2`完整manifest与digest，使用真实wire、socket和KCP adapter输出独立versioned implementation report，补证`wire-encoded-size-parity`与`kcp-adapter-parity`等B0.5负责的`implementation_required`指标。实现报告 MUST绑定exact profile/source/binary/dependency/config identity并逐项记录measured value、unit、workload、method和disposition；它 MUST NOT改写B0.2 source corpus、降低MTU/安全开销、改变lane/KCP transport参数、覆盖message route expiry或把B0.6完整fault matrix标为已通过。

#### Scenario: Wire encoded size 超出 profile

- **WHEN**真实secure/raw/KCP envelope使任一登记message超过1200-byte datagram、lane logical payload或1000-byte KCP ceiling
- **THEN**B0.5 implementation report标记失败并定位message/header，不修改profile、不删除安全字段且不提高MTU绕过

#### Scenario: KCP adapter 与冻结参数一致

- **WHEN**exact KCP dependency和adapter对canonical corpus产生与profile一致的segment、route deadline、queue与retransmit disposition
- **THEN**独立report可将该adapter identity标记implementation-qualified，但B0.6网络qualification仍保持未完成

#### Scenario: Profile corpus 被实现工具改写

- **WHEN**B0.5 verify前后任一B0.2 manifest/profile/inventory/case/report bytes或digest变化
- **THEN**资格失败且旧implementation report失效，工具不得自动接受新digest

#### Scenario: 旧 profile 报告尝试继承

- **WHEN** implementation report 绑定 `battle-network-profile-v1` 或旧 digest，而 source 已迁移至 v2
- **THEN** B0.5 binding gate 报告 stale profile，不能沿用旧 implementation-qualified disposition
