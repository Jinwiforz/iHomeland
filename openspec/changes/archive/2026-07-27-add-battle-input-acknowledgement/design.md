## Context

`InputTimeline` 已经能够按 mapping generation 维护连续终结的 `LastProcessedInputTick`，但当前 `BattleFullSnapshot` 与 `BattleDeltaSnapshot` 没有对应 wire 字段。结果是：

- 客户端只能看到权威状态，无法安全淘汰已经终结的本地输入；
- snapshot partition 之间没有确认游标一致性约束；
- B0.6 的 input→snapshot correlation 只能推测，不能由协议证据证明；
- reconnect 后旧 mapping generation 的游标可能被错误解释为当前 session 的确认。

该变更跨越纯模拟、C++ replication、Protobuf、独立协议客户端、Go qualification workload 及跨语言 fixtures，但不改变 simulation writer、transport lane 或持久化所有权。

## Goals / Non-Goals

**Goals:**

- 为 full/delta snapshot 提供明确、可验证且 actor-scoped 的连续输入确认游标。
- 保持游标与当前 BattleSession mapping generation、snapshot sequence 和 partition set 一致。
- 让 C++ 独立协议客户端与 Go observer 能在完整 snapshot 后进行 input correlation。
- 保持既有 MTU、route payload budget、raw lane 和安全封装不变。
- 通过 Go/C++/C# fixture parity 与 generation/gap/partition negative 测试固定语义。

**Non-Goals:**

- 不实现 Unity 预测、回滚、重演或 UI。
- 不让客户端输入结果覆盖服务器权威状态。
- 不在 payload 中增加 PlayerID、actor ID 或 assignment identity。
- 不跨 mapping generation 继承或合并确认游标。
- 不为新增字段扩大 datagram MTU、切换 KCP 或新增兼容旁路。

## Decisions

### 1. full 与 delta snapshot 都携带同名显式字段

在 `BattleFullSnapshot` 与 `BattleDeltaSnapshot` 增加 `uint64 last_processed_input_tick`。字段表达接收该 snapshot 的已认证 BattleSession actor 在当前 mapping generation 内，所有 `InputTick <= value` 均已经应用或以稳定结果终结。

选择在两类 snapshot 都携带字段，而不是仅在 full snapshot 携带，是因为 delta 是稳态主路径；依赖下一次 full 才能推进确认会无必要地扩大客户端历史与重演窗口。选择显式字段而不是由 `ServerTick` 推导，是因为 input/simulation rational mapping、gap 与稳定 rejection 使两者不存在一一对应关系。

### 2. `0` 是有效初始值，字段使用显式 presence

每个 mapping generation 的 InputTick 从 `1` 开始，因此 `0` 明确表示尚未连续终结任何输入。Edition 2024 生成代码必须保留字段 presence；缺失字段视为 incompatible snapshot，而不是把缺失静默解释为 `0`。

这使旧 wire producer 会被当前 B0.6 identity/parity gate 明确拒绝，避免旧版本与“合法初始零值”不可区分。

### 3. 游标属于 session actor 与 mapping generation

snapshot publisher 从当前 `BattleSessionContext` 获取 actor/mapping generation，并从该 actor 的 `InputTimeline` 读取连续终结游标。payload 不重复携带 actor identity；安全 session header 已经提供不可覆盖的接收者身份与 generation 范围。

reconnect 或更高 mapping generation 建立时，新 timeline 从 `0` 开始。旧 generation 的晚到输入和 snapshot 均按既有 generation policy 拒绝，不能推进新游标。

选择 per-recipient projection，而不是 world-wide min/max，是因为不同 actor 的输入到达、gap 与 stable rejection 独立；聚合游标会让一个 actor 错误淘汰另一个 actor 尚未终结的输入。

`ServerTick` 与 actor acknowledgement 由同一个 replication commit store 一次发布、一次冻结。禁止分别读取 `SimulationInstance::CommittedTick` 与 acknowledgement frontier；否则 simulation worker 在两次发布之间会暴露“旧 Tick + 新确认”，使客户端在对应权威状态进入 snapshot 前过早淘汰输入。读取旧 commit 或新 commit 都合法，但不能跨 commit 拼接。

### 4. partition set 必须冻结同一游标

snapshot builder 在切分 payload 前捕获一次 `(snapshot_sequence, baseline, last_processed_input_tick)`。同一 partition set 的所有 fragment 必须携带完全一致的确认游标；receiver 只有在 partition count、index、identity 和游标全部一致且完整重组后，才发布 observer state。

不接受“以最后到达 partition 为准”，因为网络乱序会使确认结果依赖到达顺序。

### 5. 保持现有 MTU 与 route budget

新增字段占用一个 tag 与至多十字节 varint。snapshot partitioner 继续以 registry 中现有 encoded/logical ceiling 计算分片，安全 header、AEAD tag、raw lane 与 1200-byte datagram 上限不变。

若单个 snapshot 因新增字段超过当前 partition payload ceiling，sender 增加 partition 或稳定拒绝；不得扩大 MTU、移除认证字段或回退 KCP/WSS/TCP。

### 6. correlation 只消费完整且通过身份校验的 snapshot

C++ 独立协议客户端在 Protobuf decode、partition 重组、snapshot sequence/baseline 与 session generation 校验通过后，才输出 `last_processed_input_tick`。Go supervisor 将它作为结构化 observer state 交给 workload/correlation；它不从日志文本抓取，也不推测 server tick。

correlation 以 `(actor, mapping_generation, input_tick)` 为键，允许 snapshot 一次确认多个已终结输入，但不得跨 generation 归因。

### 7. 跨语言 fixture 固定 presence、零值与边界值

canonical fixture 覆盖：

- full/delta 的零值显式 presence；
- 单 partition 与多 partition；
- 大 varint 边界；
- gap 不推进与 gap 稳定终结后推进；
- partition 游标漂移；
- reconnect 后旧 generation 游标；
- Go/C++/C# 编码、解码、digest 与 negative disposition 一致。

## Risks / Trade-offs

- [显式 presence 在不同 Protobuf runtime 的 API 表达不同] → 统一从 `.proto` 生成并由跨语言 canonical fixture 检查“缺失”和“显式零值”的差异。
- [每个 snapshot 重复携带游标增加带宽] → 使用 varint 且不扩大 envelope；由现有 partition budget 吸收，并在 B0.6 报告实际 bytes。
- [publisher 读取 timeline 时产生跨线程竞态] → 只在 simulation/replication 冻结点原子发布同一次 commit 的 `ServerTick` 与 actor acknowledgement，由网络线程消费副本，不让网络线程读取可变 timeline 或跨 commit 拼接字段。
- [delta baseline 与确认游标被错误耦合] → 游标是独立单调字段；baseline loss 触发 resync，但不得回退已经发布的同 generation 确认游标。
- [旧 producer 的缺失字段被当作合法零值] → 当前 qualification wire identity 与 explicit-presence decoder fail closed；不提供隐式兼容开关。

## Migration Plan

1. 修改 `.proto`、registry/schema 与生成入口，重新生成 Go/C++/C# bindings。
2. 接通 `InputTimeline`→replication projection，并补齐 gap/generation/partition 单元测试。
3. 更新独立 C++ 协议客户端与 Go observer/correlation。
4. 更新 canonical fixtures、manifest digest 和 cross-language parity。
5. 运行相关 simulation/transport/protocol-client/Go targeted tests 与代表性 `diagnose`。B0.5/B0.6 完整矩阵、连续 verify、soak 与 finalize 只在用户显式冻结最终候选时由统一质量入口执行，不属于本 change 的完成条件。

回滚时必须同时回滚 `.proto`、生成代码、registry/fixtures、producer 和 consumer，并恢复前一 wire identity；不得只忽略字段而保留已更新的资格报告。

## Open Questions

无。字段身份、初始值、generation、partition 与预算语义在本 change 内冻结。
