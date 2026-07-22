## ADDED Requirements

### Requirement: 第一业务里程碑客户端必须通过完整 C3 资格门

客户端 C3 MUST 在 C2 及其权威一致性修复归档后执行，并聚合 clean client restore、secure cross-process session restore、WSS/TCP independent recovery、全部 EditMode/PlayMode、cross-end fixtures、Windows Development/Release Player、双客户端产品流程、服务端故障、stale callback、Scene/UI lifecycle、bounded soak、cleanup 与文档一致性证据。只有同一冻结 contract/build evidence chain 上全部 mandatory gate 通过且资格报告声明 qualified，第一业务里程碑才可标记客户端 v1 qualified；人工 happy path、单一 build、部分绿色 tests 或旧 Player 证据 MUST NOT 解锁后续发布结论。

#### Scenario: 自动测试通过但 Release 与双 Player 缺失

- **WHEN** EditMode、PlayMode 与 Development build 通过，但 Release Player、当前 build 的双客户端故障矩阵或 soak 任一缺失
- **THEN** `qualify-client-v1` 保持未完成，里程碑不得声明客户端 qualified

#### Scenario: 完整 C3 资格通过

- **WHEN** 版本化 manifest 的全部 mandatory 自动与人工场景、两种 Player、低敏报告和 cleanup 在 current contract/build digest 上通过
- **THEN** `qualify-client-v1` 可以归档，后续 change 可引用该客户端 v1 基线但不得绕过新增 capability 的独立 OpenSpec 与回归资格
