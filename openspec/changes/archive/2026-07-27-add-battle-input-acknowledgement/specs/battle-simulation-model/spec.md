## ADDED Requirements

### Requirement: 输入确认前沿必须形成不可变 replication projection

每个 actor 的 `InputTimeline` MUST 在 simulation/replication 冻结点产生包含同一次 committed `ServerTick`、mapping generation 与 `LastProcessedInputTick` 的不可变 projection。该游标 MUST 只越过已经应用或以稳定 accepted/rejected/expired/missing 结果终结的连续 InputTick；网络线程和 snapshot encoder MUST 只消费同一个 commit 的 projection 副本，不得分别读取 Tick 与确认游标，也不得直接读取或修改可变 timeline。更高 mapping generation 建立时 MUST 创建从 0 开始的独立确认前沿，旧 generation 的未决、晚到或重放输入不得推进它。

#### Scenario: gap 被稳定终结

- **WHEN** InputTick 100 与 102 已终结、101 未决，随后 expiry policy 把 101 稳定终结
- **THEN** 下一 simulation/replication 冻结点可把 `LastProcessedInputTick` 从 100 连续推进到 102，并产生同 generation 的不可变 projection

#### Scenario: 网络线程并发发送 snapshot

- **WHEN** simulation worker 正在处理后续 InputTick，网络线程发送先前冻结的 snapshot projection
- **THEN** encoder 只观察同一次 commit 的固定 `ServerTick`、generation 与确认游标，不组合旧 Tick 和新游标、不读取半完成 timeline，也不因线程时序产生不同 wire 值

#### Scenario: mapping generation 被替换

- **WHEN** reconnect 建立更高 mapping generation 且旧 generation 仍有未决 InputTick
- **THEN**新 generation 的 projection 从 `LastProcessedInputTick = 0` 开始，旧 generation 的后续终结不能改变新 projection
