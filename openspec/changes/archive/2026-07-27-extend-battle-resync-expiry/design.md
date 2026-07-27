## Context

`battle-network-profile-v1` 把 KCP transport 参数和 application expiry 一并冻结为 lane 级 500 ms。B0.6 在 `baseline-gap`、15% loss、burst loss 与 120 ms RTT 包络中证明，500 ms 无法稳定完成 `resync.request -> resync.response -> next full baseline`。进一步诊断还发现，production adapter 与独立协议客户端都从首个 KCP `PUSH` 开始计算 receiver application reassembly deadline；该计时器会把 ordered stream 中等待前序 segment 的合法消息误判为过期，混淆 sender 业务价值窗口与 receiver 重组资源上界。

本变更跨越 B0.2 profile、B0.5 wire/adapter 与 B0.6 qualification binding。Profile、manifest 和报告均属于冻结源，任何语义变化都必须建立新消费者版本，旧报告不能自动继承。

## Goals / Non-Goals

**Goals:**

- 建立 `battle-network-profile-v2`，以 message route 为 sender application expiry 的唯一事实源。
- 将 `battle.resync.request` 与 `battle.resync.response` 的 sender expiry 扩展为 2250 ms，并保持 2/s rate limit 和严格 resync generation 幂等语义。
- 明确 receiver 不推导或覆盖 sender deadline；receiver reassembly 由 KCP window/queue 与 session lifecycle 有界管理。
- 保持其他六个 logical kind、KCP transport 参数、MTU、安全和 wire layout 不变。
- 用冻结 ARQ 上界、真实 sender inflight 反例、adapter parity 与 B0.6 `baseline-gap` 回归共同证明新 policy。

**Non-Goals:**

- 不修改 KCP RTO、window、fast resend、queue、dead-link、segment ceiling 或 update interval。
- 不增加 resync rate，不恢复定时无条件 resync，不把 snapshot 改走 KCP。
- 不改变 Protobuf 字段、numeric message ID、加密、握手、rebind 或 battle admission。
- 不把 receiver queue 变成无界缓存，也不允许 receiver 提交未完整重组或 route/sequence/Tick 非法的消息。
- 不在本变更中声明 B0.6 qualified，也不解锁 Unity battle runtime。

## Decisions

### 1. 使用新 profile 消费者版本

所有 profile corpus 文件升级到 `battle-network-profile-v2`，并重新计算 manifest、model binding、canonical report 与下游 binding。消费者版本与 JSON `format_version` 分离；只有结构变化的文档才提升格式版本。

Expiry 所有权会改变可观察的发送终结与接收重组语义。继续使用 v1 会让旧 B0.5/B0.6 报告错误匹配新契约，因此旧 digest 一律 fail closed。

### 2. Sender application expiry 由 route 独占

`message-inventory.json` 和 numeric route registry 登记每个 logical kind 的精确 sender expiry。KCP profile 只登记允许的最大 route expiry，不再提供所有 KCP 消息共享的默认 deadline。

生产 adapter 在 enqueue 时解析 immutable route policy，保存绝对 deadline，并在 queued/inflight sender 状态中执行过期终结。未知 route、零 deadline、超出 profile 最大值或 caller 覆盖值均 fail closed。单 session 继续只有一个 KCP conversation、一个队列和一个 owner。

### 3. Receiver reassembly 不复制 sender deadline

Sender enqueue 的本地单调时刻没有进入 wire，receiver 无法可靠重建同一绝对 deadline。从首个 `PUSH` 到达时另起 application timer 会产生不同语义：ordered KCP stream 中，后续消息即使完整到达，也可能因等待前序缺口而被错误终结。

因此 receiver 不维护 application reassembly expiry。未完整消息由 KCP 64-segment receive window、64-message queue、1000-byte message ceiling和 session lifecycle 共同有界；完整重组后仍必须校验 numeric route、lane、sequence、Tick、generation 与业务 freshness。Session shutdown、KCP dead-link 或资源上限继续稳定终结 receiver 状态。

### 4. Resync sender expiry 使用 2250 ms

冻结参数给出的保守发送上界为：

`dead-link 10 × maximum RTO 200 ms + maximum RTT 160 ms + update interval 10 ms = 2170 ms`

Route deadline 候选按 250 ms 合同网格向上取整，因此最小覆盖值为 2250 ms。500 ms 与 1000 ms 在同一真实 `baseline-gap` seed 下均以 `client-poll-kcp-inflight-expiry` 终结，证明旧值和一次较小放宽仍不足；2250 ms 必须在相同故障输入下通过恢复与 cleanup gate。该值不是 receiver 缓存时长，也不放宽登记的 2,000,000 µs baseline recovery 指标。

### 5. 只延长恢复事务

`battle.ability.reliable-event` 与 `battle.entity.lifecycle` 继续使用 500 ms。它们超过当前 simulation 价值窗口后必须终结，延长会增加 ordered head-of-line 驻留并可能提交陈旧事件。2250 ms 只授权 resync 控制事务产生新的 full baseline，不授权回放旧 snapshot 或 gameplay 事实。

### 6. 资格迁移采用失效后按需重建

旧 profile digest 绑定的 B0.3/B0.5/B0.6 overlay 一律视为 stale。change 开发顺序为 source profile、wire registry、runtime/client、分层 parity、精确 `baseline-gap` 反例与相邻 KCP 回归；这足以证明当前修改及其直接影响面。资格报告、完整 mandatory matrix 和 soak 只在用户显式冻结最终候选时由统一质量入口重建，不作为本 change 的完成条件。

### 7. Baseline 恢复必须依赖 committed Tick 快照节拍

Production snapshot publisher 必须从 simulation owner 的 committed Tick 投影驱动 10 Hz 发布：每 2 个 20 Hz simulation Tick 至多发布一次 snapshot，每 10 个正常发布周期生成一次 full baseline。持续 ingress 不得饿死 periodic worker，长时间调度延迟也不得触发 catch-up burst；resync 可以在同一 committed Tick 强制一个新的 full baseline，并由既有 generation/rate policy 去重和限流。

该修复不提高 profile cadence，也不把 snapshot 绑定到 input 到达。此前按 raw input acceptance 才发布 snapshot 的实现会使 10 Hz contract 退化为 workload-dependent 频率，导致 2 秒 baseline recovery 即使扩大 KCP route deadline 仍不可达。

单个 resync response 不能把 raw full 的一次投递等同于恢复完成。在合法 resync 强制 full 后，publisher 启动固定 30 committed Tick（1.5 秒）的 recovery window，并在窗口内每 10 Tick（2/s）最多补充一个新 full baseline；重复 resync 只重排下一次 full，不延长活跃窗口。调度延迟越过窗口时不追赶补发。该冗余仍走 raw lane，既不提高登记 rate，也不改变 snapshot freshness、fault、PRNG、KCP RTO 或 2 秒恢复预算。

### 8. Delivery age 使用 gateway 实际投递时刻

`delivery-age` 只度量 fault gateway 已成功写入 socket 的 packet 从接收到实际投递的单调时长，计算为 `DeliveredAt - ReceivedAt`。Intentional loss、burst loss 或客户端相邻 snapshot receipt gap 不属于单个 packet 的 delivery age；后者保留为 baseline/cadence 诊断证据，不能替代 150 ms delivery budget。

## Risks / Trade-offs

- [2250 ms 增加 sender ordered queue 驻留] → 仅适用于最高 2/s 的两个 resync route，继续执行 64-message queue、per-route rate、session/node hard backpressure 与 sender expiry 清理。
- [移除 receiver application timer 被误解为无界等待] → KCP receive window、message/segment ceiling、queue、dead-link 与 session shutdown 仍提供独立硬上界，并增加对应 contract/parity 测试。
- [Profile v2 使既有实现报告失效] → 使用机器 binding 和 digest fail closed，在本 change 内验证 current consumers 与定向兼容证据；最终 B0.3/B0.5/B0.6 报告按显式候选一次重建。
- [客户端与服务端迁移不同步] → BattleTicket 继续绑定 exact profile/config identity，握手拒绝不一致版本，不提供隐式 v1/v2 混用。
- [2250 ms 真实回归通过但泛化不足] → 开发期保留 B0.2 确定性矩阵、B0.5 adapter parity 与代表性真实 `diagnose`；B0.6 全量 fault matrix 与 soak 仍作为最终资格能力，单个 counterexample 只作为当前 change 的最低回归。
- [持续 ingress 或调度暂停破坏 snapshot cadence] → runtime worker 每次处理 datagram 后仍检查单调 periodic deadline，延迟时从当前时刻重新排期而不补发突发 snapshot；确定性 cadence 测试覆盖 Tick 跳跃与同 Tick resync。
- [单个 resync full 在 raw loss 下无法稳定满足恢复预算] → 由 committed Tick owner 在固定 recovery window 内按既有 2/s 上限发布有界冗余 full；窗口不续期、不 catch-up，真实同 seed baseline-gap 必须逐 slot 小于 2,000,000 µs。
- [把故障导致的客户端收包间隔误判为 packet delivery age] → metric catalog、Go loader、PowerShell validator 与 failure regression 精确绑定 `fault-gateway/maximum-gateway-delivery-age`。

## Migration Plan

1. 提交 v2 delta spec、schema、profile、inventory、ARQ 上界和 sender 失败反例，重算 profile manifest 与 canonical report。
2. 更新 numeric route registry、wire fixtures、B0.3/B0.5 binding，并使生成/验证入口拒绝 v1/v2 混配。
3. 将 C++ adapter 和独立协议客户端改为 route-owned sender deadline，移除 receiver application timer，执行 unit、contract、KCP parity 与真实 loopback 回归。
4. 更新 B0.6 upstream binding，并通过定向 `diagnose` 在同一故障输入下验证 2250 ms 恢复、2 秒 recovery 指标、四源守恒与独立 cleanup。
5. 本 change 完成后保持旧最终报告 stale；完整 matrix、capacity、lifecycle、security、连续 verify、soak 与 finalize 只在用户显式最终验收时执行。若该资格失败，不回退已由定向测试证明正确的 v2 契约；新的参数修订仍必须另行提供证据。

回滚必须同时恢复 v1 source、registry、runtime 和 binding；不得只回滚 runtime 常量或只恢复 digest。

## Open Questions

无。
