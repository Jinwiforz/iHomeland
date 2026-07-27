# Battle Network Qualification Corpus

本目录是 B0.6 `qualify-battle-network` 的版本化资格输入，不是 gameplay、wire 或
production 配置。它只读绑定 B0.1～B0.5 source/evidence，并描述 Windows x64
`controlled-local-fault-gateway` 环境中的真实进程 fault、capacity、security 与
lifecycle/soak 矩阵。

## 文件职责

- `schema.json`：manifest 与各 source document 的 closed schema。
- `report.schema.json`：run evidence、reproducibility summary 与 final report 的 closed schema。
- `profile-overlay.schema.json`：从 qualified final report 派生的 B0.2 只读 implementation overlay schema。
- `run-evidence.schema.json`：单次 diagnostic、verify 与 soak 的 identity、digest 和 cleanup 索引。
- `scenario-failure-evidence.schema.json`：真实场景失败时的 client KCP primitive 状态与同 slot gateway 双向终局计数；只用于定位，禁止进入 finalize。
- `manifest.json`：文件登记、摘要、domain coverage 与版本 identity。
- `upstream-binding.json`：B0.1～B0.5、toolchain 和配置的精确只读绑定。
- `fault-execution.json`：B0.2 十二种 fault shape 的双向真实网络执行计划。
- `workloads.json`：1/5/8 actor、overflow probe 与 33 人兼容策略。
- `metrics.json`：四源测量、冻结预算、聚合和复现容差。
- `lifecycle-security.json`：攻击、NAT、失效与 30 分钟 soak 矩阵。

## 不变量

- 所有路径均相对仓库或本 corpus，禁止本机绝对路径。
- source corpus 不保存 ticket、credential、proof/traffic/cookie key、PlayerID、IP、
  payload dump、PID 或运行目录。
- fault gateway 只观察 datagram 长度、方向、公开 packet kind 和 run-local 摘要，
  不解密或改写 payload。
- B0.2 的 source scenario、seed、impairment 和预算保持只读；B0.6 可以增加执行覆盖，
  不能降低或替换冻结输入。
- `diagnose` 证据不能进入资格结论。只有两次完整 `verify`、mandatory `soak`、全部
  regression 与 cleanup 通过后，`finalize` 才可声明
  `battle-network-qualified-windows-x64-controlled`。

唯一公开质量入口为 `tools/quality/quality.ps1`。本目录的
`tools/battle-qualification/battle-qualification.ps1` 只作为统一入口委托的内部 owner；
corpus 校验模块只供该 owner 和 failure regression 调用。环境准备、完整执行顺序、
指标口径、evidence 与 cleanup 规则见 `docs/battle-network-qualification.md`。
