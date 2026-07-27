## Why

B0.6 在冻结的 `baseline-gap`、15% loss、burst loss 与 120 ms RTT 包络中证明：`battle.resync.request` 和 `battle.resync.response` 共用 500 ms application expiry 时，真实 KCP 重传可能在恢复事务完成前稳定过期，导致缺失 baseline 无法在登记流程内收敛。该结果是冻结参数与真实实现之间的反例，必须先修订 network profile 和 secure transport 契约，不能在资格工具中降低故障或隐藏放宽 deadline。

## What Changes

- 将 battle network profile 升级为新的消费者契约版本，只把 `battle.resync.request` 与 `battle.resync.response` 的 sender application expiry 扩展到按冻结 ARQ 上界选择、并由真实反例回归验证的 2250 ms。
- 保持 `battle.ability.reliable-event`、`battle.entity.lifecycle`、raw snapshot/input/probe 的既有 expiry、lane、rate、MTU、KCP RTO/window/queue 和安全参数不变。
- 迁移 battle numeric route、C++ KCP adapter、独立协议测试客户端、fixture、manifest、报告和上游 binding，使每个 logical kind 使用 registry 驱动的 sender route expiry，不再依赖 KCP lane 级统一常量。
- 移除错误的 receiver application reassembly timer；receiver 由 KCP window/queue 和 session lifecycle 有界管理，只在完整重组后校验 route、sequence 与 Tick。
- 增加 500/1000 ms sender inflight 失败反例与 2250 ms 成功回归；旧 B0.6 报告因 identity 漂移失效，但本 change 只需完成受影响契约、消费者与代表性场景验证。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `battle-network-profile`：把 KCP sender application expiry 从 lane 级统一值改为 message route 级冻结值，并明确 receiver reassembly 的独立有界所有权。
- `secure-battle-transport`：KCP adapter 必须按 numeric route 执行 sender application expiry，允许 resync route 使用 2250 ms deadline，同时保持其他 KCP route 的 500 ms 行为。

## Impact

- 影响 `shared/contracts/fixtures/battle/network-profile/`、battle wire/registry fixtures、B0.3/B0.5/B0.6 的 profile binding、manifest 与 canonical report。
- 影响 C++ production KCP adapter、battle protocol client、Go/C++ contract/parity tests 和资格 fault regression。
- Protobuf payload、numeric message ID、UDP lane、MTU、加密套件、KCP 传输参数和公开 endpoint 均不变；本变更不引入 wire layout 破坏。
- `qualify-battle-network` 必须绑定新 profile 版本且旧报告不能继承 qualified 结论；全部 mandatory matrix 只在用户显式冻结最终候选并调用统一 `qualify` 时重跑。
