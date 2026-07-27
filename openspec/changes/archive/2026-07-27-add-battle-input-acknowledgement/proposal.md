## Why

当前 battle snapshot wire 只携带服务器状态版本，未携带属于当前已认证 actor 与 mapping generation 的连续输入终结游标。即使模拟核心已经维护 `LastProcessedInputTick`，独立协议客户端也无法区分“已应用”“已稳定拒绝”和“仍存在 gap”的输入，因而无法完成 B0.6 所要求的输入确认、历史淘汰与重演验收。

## What Changes

- 在 full snapshot 与 delta snapshot 的 `battle/v1` Protobuf payload 中增加显式 `last_processed_input_tick`。
- 冻结该字段的身份与 generation 语义：它只表示接收该 snapshot 的当前 BattleSession actor、当前 mapping generation 内已经连续终结的最大 InputTick；初始值 `0` 表示尚未终结任何输入。
- 要求同一逻辑 snapshot 的全部 partition 携带一致的确认游标，并在完整重组后才向 observer 发布。
- 将模拟输入时间线的连续终结游标接入 replication projection、独立 C++ 协议客户端、Go workload observer、跨语言 fixtures 与按需 B0.6 证据能力。
- 保持既有 1200-byte datagram 和 route payload budget，不以新增字段为由扩大 MTU 或引入 lane fallback。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `secure-battle-transport`: 为 full/delta snapshot wire、partition、跨语言 parity 和 B0.6 验收补充 actor-scoped `last_processed_input_tick` 契约。
- `battle-simulation-model`: 明确模拟输入时间线必须向当前 BattleSession mapping generation 的 replication projection 提供连续终结游标，且 reconnect/generation 切换不得继承旧游标。

## Impact

- 受影响的协议与 registry：`shared/proto/battle/v1`、battle route/schema/manifest、canonical fixtures 与生成入口。
- 受影响的 C++：InputTimeline 到 snapshot publisher 的投影、full/delta 编解码、独立协议客户端 observer。
- 受影响的 Go：battle qualification workload/correlation 及报告字段；不改变 Go 对 gameplay 状态的所有权。
- 受影响的 C#：生成协议与跨语言 fixture/decoder parity；本 change 不实现 Unity runtime 或 UI。
- 兼容性：新增 Protobuf 字段对未知字段兼容；当前 wire identity 与 registry/fixture digest 必须同步更新，最终 B0.6 资格证据只在用户显式冻结候选时重建。
