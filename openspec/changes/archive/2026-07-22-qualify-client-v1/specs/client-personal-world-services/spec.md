## ADDED Requirements

### Requirement: 通道恢复必须由唯一应用层协调者按权威目标独立收敛

App Scope MUST 由唯一 `ClientConnectionRecoveryCoordinator` 组合 Session、control、gameplay、PersonalWorld、VisitSession 与 target owner 的只读状态和窄恢复操作。Coordinator MUST 为 WSS control 与 gameplay 分别保存 generation-bound health，并确保任一时刻最多一个 automatic/manual recovery intent；channel adapter、UI、Scene 与 timer MUST NOT 自行重建完整产品目标。Control 瞬时中断 MUST 由既有有限 WSS recovery owner 处理而不关闭仍健康的 gameplay；旧 control generation 的 inbox/hint projection MUST 在恢复提交前 fail closed 失效，当前 gameplay target 的 world/visit 完整 snapshot MUST 重新读取并按最高 authority 收敛。既有协议无法重放的 control-only invite 不得由客户端猜测补回，后续只接受新 generation PUSH 或服务端明确拒绝后的精确退役。Gameplay 非预期中断 MUST 立即关闭旧 target mutation 与 Scene binding，并在冻结总预算内使用 current session 的新 ticket/admission：OwnWorld 重建当前自己的 assignment，Visiting 仅在权威 membership/grace 允许时以同一 VisitSession 的 `RECONNECT` 恢复。协议、授权、session invalidation、stale identity、预算耗尽或 commit-unknown MUST fail closed 到稳定 terminal snapshot，不得无限自动重试。

#### Scenario: Control 恢复而 gameplay 始终健康

- **WHEN** WSS 进入 Recovering 后以新 ticket 恢复，current gameplay generation 未关闭
- **THEN** coordinator 不签发 gameplay admission、不重建 Scene，先失效旧 control-only inbox/hint，再在 control Connected 后以 gameplay 完整 snapshot 收敛 current member/assignment；客户端不继续展示旧邀请，也不伪造断线期间从未收到的邀请

#### Scenario: OwnWorld gameplay 单独中断

- **WHEN** Session 与 control 仍 current，但 OwnWorld gameplay generation 产生 unexpected disconnect
- **THEN** 旧 mutation 与 Scene generation 立即失效，coordinator 只建立一笔新 own-world bootstrap/admission/connect/snapshot flow；若冻结的恢复目标包含 Owner VisitSession，则必须在 target 校验与 Scene commit 前通过新 gameplay generation 重新读取并收敛 World 与 VisitSession 完整 snapshot，允许同一 VisitSession revision 下更高的 assignment generation 替换旧 runtime identity；只有两类投影均与冻结目标一致后才提交新 target generation，缺少 VisitSession 重放不得误分类为协议不兼容

#### Scenario: Visitor 在 grace 内恢复 gameplay

- **WHEN** current Visiting target 的 gameplay 中断，服务端仍保留匹配 VisitSession membership 与 reconnect deadline
- **THEN** coordinator 使用新 `RECONNECT` admission 返回并冻结的权威 `visitRevision` 构造唯一首帧恢复同一 membership，验证完整 VisitSession/world snapshot 后才重新开放 Visitor mutation 与 Scene

#### Scenario: Visitor membership 已结束

- **WHEN** Visitor 恢复时服务端明确返回 session/member not-found、expired、closed 或 safe-return destination
- **THEN** coordinator 不重试旧 `RECONNECT`，退役旧 target并按权威结果进入 ReturningOwnWorld；失败 UI 不把该玩家继续显示为在线 Visitor

#### Scenario: 自动恢复与玩家重试并发

- **WHEN** automatic recovery 尚未完成时玩家点击重新连接，或 terminal failure 后 automatic completion 迟到
- **THEN** single intent/generation gate 拒绝并行凭据、socket 与 target；手工重试只能在稳定 terminal snapshot 后创建下一代 intent

#### Scenario: 旧 SafeReturn 在重新加入后迟到

- **WHEN** Visitor 已在同一 VisitSession 的更高 revision 重新加入，而旧 gameplay generation 的 `SafeReturnDirective` 随后到达
- **THEN** 客户端按指令携带的权威 revision 拒绝该旧 projection，保持新 membership 与 Visiting target，不得转入 OwnWorld
