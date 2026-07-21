## MODIFIED Requirements

### Requirement: Invite 必须有界、定向、绑定有效目标且永远不是 gameplay credential
只有 VisitSession Owner MAY 为非 Owner、由 Account owner 在首次提交前证明当前 active 且可邀请的目标 PlayerID 创建或撤销 pending invite。Application MUST 在确认受信 actor 拥有 active PersonalWorld 与 active VisitSession 后才解析目标可用性，并 MUST 将 self、missing 与 inactive 统一拒绝为低敏 validation failure，不能推进 revision、创建 InviteID、保存 invite 或发布副作用。Account 读取依赖失败 MUST fail closed，不能伪装为目标不存在。相同 CommandID/fingerprint 已经提交时，store MUST 在重新读取目标可用性之前重放首次完整结果。Invite MUST 绑定 VisitSessionID、目标 Visitor、创建 revision 与绝对 expiry，使用独立 InviteID，并受 session lifecycle/expiry、pending invite 上限和稳定 command identity 约束。Invite MUST NOT 包含 endpoint、socket、通用 gameplay ticket、admission bearer secret 或 Owner/Visitor 可转移权限；持有 InviteID 只允许目标 Visitor 请求 accept，不能直接 join 或执行 world command。

#### Scenario: Owner 创建目标邀请
- **WHEN** session open、未过期且 Owner 使用受信 AuthContext 邀请另一个由 Account owner 证明 active 的 Player
- **THEN** application 原子保存有界 pending invite，返回不具备 gameplay scope 或 admission 权限的 invite projection

#### Scenario: Owner 邀请自身
- **WHEN** target PlayerID 与受信 Owner actor 相同
- **THEN** application 返回统一 validation failure，revision、invite、membership 和网络副作用全部不变，且不需要查询 Account 目录

#### Scenario: 目标 Player 不存在或 inactive
- **WHEN** Account owner 对格式有效 target PlayerID 返回统一 unavailable
- **THEN** application 返回与 self 相同的低敏 validation failure，不创建伪邀请且不泄漏目标是否存在或停用

#### Scenario: 目标 Player 读取依赖失败
- **WHEN** MySQL 或 Account reader 无法权威证明 target available 或 unavailable
- **THEN** application 返回 dependency unavailable，VisitSession revision 与集合不变且不发布邀请

#### Scenario: 已提交邀请在目标状态改变后 replay
- **WHEN** 首次 CreateInvite 已提交，目标随后变为 inactive，并以相同 CommandID/fingerprint 重试
- **THEN** store 在重新读取目标可用性前重放首次 invite、snapshot 与 revision，不生成新 InviteID或第二次 mutation

#### Scenario: Visitor 邀请第三方
- **WHEN** Visitor 或非 Owner actor 尝试创建 invite、改变 capacity 或把 invite 转发给其他 Player accept
- **THEN** application 返回 forbidden/target mismatch，session revision、invite 与 membership 全部不变

#### Scenario: Pending invite 被撤销或到期
- **WHEN** Owner 撤销 matching pending invite，或 system expire command 精确匹配其 identity/deadline 且 observedAt 已到期
- **THEN** application 只移除该 pending invite并推进一次 revision，不创建 membership 或 safe-return；已 accepted、错误 identity 或旧 deadline 不能删除当前资格

#### Scenario: 使用 invite 直接 join
- **WHEN** 调用方只提交 InviteID 而没有后续 admission owner 验证的 join qualification
- **THEN** join 被拒绝，invite 不被解释为 bearer credential且不创建 connection membership
