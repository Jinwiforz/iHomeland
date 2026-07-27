## Why

当前 change 完成条件把受影响功能验证、历史阶段资格、完整网络矩阵、连续 verify、长时 soak
和最终报告绑定为同一默认链路。任何协议、C++ 或资格工具改动都会使宽泛 source identity
失效并触发重复 clean build，导致开发反馈以小时计、资格工具成为日常调试器，且历史
B0.3～B0.6 门禁无法扩展到长期路线。

## What Changes

- 新增项目级验证编排能力，将默认 change 验证与用户显式触发的完整最终资格分离。
- 提供唯一公开质量入口，支持影响面预览、change 定向检查、定点诊断和冻结 candidate
  的完整资格；其余现有工具降为内部 owner，不要求使用者理解调用链。
- 默认 change 检查只运行新增功能及机器判定影响面的增量 build、unit、contract、
  parity、定向 race/ASan 和必要端到端 smoke，禁止隐式升级为历史 B0.3～B0.6 整链、
  完整矩阵、连续 verify、soak 或 finalize。
- 完整最终资格只在用户明确要求或未来发布流程显式传入冻结 commit 时执行；它构建一次
  当前产品并运行当前 mandatory capability suites，而不是按历史 change 顺序重复构建。
- 保留现有 battle fault、capacity、security、lifecycle、verify、soak 和 finalize
  能力供随时全面验收；将 B0.6 从阻塞后续功能开发的发布资格门改为网络资格工具与代表性
  development-readiness 验证。
- 调整活跃衍生 change 的完成条件：各 change 只保留与自身行为直接相关的定向验收，
  删除重复的完整 B0.3/B0.5/B0.6、server/client 全资格、两次 verify、soak 与报告要求。
- 删除没有唯一职责的重复公开包装和重复任务定义；不重写 production 业务、网络、模拟或
  既有资格矩阵实现。

## Capabilities

### New Capabilities

- `project-validation`: 定义 change 影响面验证、统一质量入口、证据复用、显式最终资格触发和
  不允许自动扩大验证范围的长期规则。

### Modified Capabilities

- `battle-network-qualification`: 将完整矩阵与最终报告保留为按需资格能力，并把资格工具
  建立、代表性开发验证和实际最终资格结论解耦。
- `delivery-sequencing`: 后续功能开发不再等待连续两次完整 B0.6 verify、长时 soak 与
  finalize；这些门禁移动到用户明确选择的里程碑或发布候选。

## Impact

- 新增统一质量入口及机器可读影响面 registry，复用 `tools/go`、`tools/cpp`、
  `tools/proto`、`tools/battle-qualification` 等现有 owner。
- 修改 `docs/workflow.md`、工程标准、路线图、battle qualification runbook 与相关
  README，明确默认和显式模式。
- 修改 `qualify-battle-network`、`extend-battle-resync-expiry` 和
  `add-battle-input-acknowledgement` 的 artifacts，移除重复资格责任并保留定向证据。
- 不改变 production API、协议字段、listener、端口、存储 schema、BattleSession 行为、
  fault 参数、安全预算或最终资格判定严格性。
