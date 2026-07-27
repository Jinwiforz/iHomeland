## ADDED Requirements

### Requirement: Snapshot 必须显式确认当前 actor 的连续输入前沿

`BattleFullSnapshot` 与 `BattleDeltaSnapshot` MUST 携带显式 presence 的 `last_processed_input_tick`，表示接收该 snapshot 的已认证 BattleSession actor 在当前 mapping generation 内已经应用或以稳定结果终结的最大连续 InputTick。值 `0` MUST 只表示该 generation 尚未终结任何从 1 开始的输入；字段缺失 MUST 被当前 wire identity 的 decoder 视为不兼容，而不是解释为 `0`。该字段 MUST 来自 simulation replication 冻结点，不得由 ServerTick、到达顺序、payload identity 或客户端声明推导。

#### Scenario: snapshot 确认 gap 之前的输入

- **WHEN** 当前 actor 的 InputTick 100 已终结、101 仍未决且 102 已终结，replication 生成 full 或 delta snapshot
- **THEN** `last_processed_input_tick` 必须为 100；只有 101 被应用或按稳定 policy 终结后，后续 snapshot 才能越过该 gap

#### Scenario: mapping generation 尚无输入

- **WHEN** 新 BattleSession mapping generation 已建立但尚未终结 InputTick 1
- **THEN** snapshot 必须显式编码 `last_processed_input_tick = 0`，receiver 能区分该值与字段缺失的旧 producer

#### Scenario: reconnect 后旧确认到达

- **WHEN** 当前 session 已进入更高 mapping generation，而旧 generation 的 snapshot 或输入随后到达
- **THEN** receiver 拒绝旧 generation 数据，新 generation 的确认游标从 0 独立推进且不得继承旧值

### Requirement: Snapshot partition 必须冻结一致的输入确认

同一逻辑 snapshot 的所有 partition MUST 冻结相同的 snapshot sequence、baseline identity、mapping generation 和 `last_processed_input_tick`。Receiver MUST 在完整 partition set 通过数量、索引、身份、大小与确认游标一致性校验后才发布确认；缺片、重复冲突或游标漂移 MUST fail closed。新增字段 MUST 保持现有 1200-byte datagram、route max encoded/logical size、raw lane 和安全封装预算；sender MUST 通过登记 partition policy 拆分或稳定拒绝，不得扩大 MTU 或切换 lane。

#### Scenario: partition 游标发生漂移

- **WHEN** 同一 snapshot sequence 的两个 partition 携带不同 `last_processed_input_tick`
- **THEN** receiver 拒绝整个 partition set，不选择任一片的值、不发布 observer state 且不淘汰客户端输入历史

#### Scenario: 新字段触发额外 partition

- **WHEN** 增加确认字段使 snapshot 无法在原 partition 数量内满足 route payload ceiling
- **THEN** sender 按冻结 split policy 增加 partition 或稳定拒绝，完整 datagram 仍不超过 1200 bytes 且继续使用登记 raw lane

### Requirement: 输入确认必须通过跨语言协议资格

Go、C++ 与 C# MUST 对 full/delta snapshot 的 `last_processed_input_tick` 字段编号、presence、零值、大 varint、partition identity、bytes、digest、decode result 和 negative disposition 保持一致。独立 C++ 协议客户端 MUST 只在完整且已认证的 snapshot 通过 generation/baseline/partition 校验后输出该游标；Go qualification correlation MUST 以 actor、mapping generation 与 InputTick 关联 input 和确认，不得从 ServerTick 或日志文本推测。B0.6 的 own/visit、loss/reorder/duplicate、reconnect 与 backpressure workload MUST 证明游标不越过未决 gap、不跨 generation 串联且能回收已终结输入。

#### Scenario: 跨语言显式零值 fixture

- **WHEN** Go、C++ 与 C# 消费显式编码 `last_processed_input_tick = 0` 的相同 canonical full/delta fixture
- **THEN** 三种实现报告相同 presence、值、canonical bytes 与 digest，删除该字段的 negative fixture 一致失败

#### Scenario: qualification 关联一批输入

- **WHEN** 一个已认证 snapshot 在当前 actor/mapping generation 内把确认游标从 40 推进到 44
- **THEN** correlation 可将 41 至 44 的已终结输入归入该确认窗口，但不得确认 44 之后或其他 actor/generation 的输入
