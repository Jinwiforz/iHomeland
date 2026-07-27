## ADDED Requirements

### Requirement: B0.6 必须以真实网络资格 overlay 补齐冻结 profile

Battle network qualification MUST 只读绑定 `battle-network-profile-v2` 的完整 manifest、model binding、profile、message inventory、fault matrix、cases 和 canonical report，并以真实 Go/C++ process、secure wire、KCP adapter、fault injection 和 1/5/8 actor workload 生成独立 B0.6 overlay。Overlay MUST 对 profile 中每个 `implementation_required` 与 target budget 登记 source evidence、measured value、unit、workload、method、environment class 和 disposition；它 MUST NOT 回写 B0.2 corpus、改变 selected candidate、降低 fault 参数、缩短 mandatory coverage、调整 MTU/lane/KCP/security overhead 或把 33 actor compatibility workload伪装为已支持的 battle actor 数。

#### Scenario: 真实五人矩阵补齐 profile

- **WHEN** current secure transport 在全部 mandatory fault scenarios 下完成五人 workload，且真实带宽、CPU、memory、queue、retransmit、expiry 与 recovery 均在冻结预算内
- **THEN** B0.6 overlay 可把 default-coop 对应实现项标记 network-qualified，并保留 exact profile/source/environment binding

#### Scenario: 结果超限后尝试改写 profile

- **WHEN** 真实 KCP amplification、queue、CPU、memory 或 baseline recovery 超出 target，runner 尝试修改 source profile、seed、scenario、lane、MTU 或 actor 数后重跑
- **THEN** source digest gate 失败，原 qualification 保持 not-qualified，参数调整必须另提 OpenSpec change

#### Scenario: 33 人兼容 workload

- **WHEN** VisitSession 允许 33 人而 battle transport 只允许 8 个 installed/active actor
- **THEN** overlay 保持 `capacity_gate_required`，验证第九个 battle actor 被拒绝且不改变 VisitSession 事实，不要求创建 33 条 battle session
