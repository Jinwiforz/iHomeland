## ADDED Requirements

### Requirement: Invite inbox 必须消费权威退役并明确拒绝残留邀请

`VisitSessionService` MUST 将 state 为 `RETIRED` 的 `VisitInvitePush` 作为 exact VisitSessionID + InviteID tombstone：匹配 Pending identity 必须立即从 inbox immutable snapshot 删除并使页面 selection/capability 同步失效；不存在或重复 identity MUST 幂等忽略。Pending invite MUST 继续经过 absolute expiry、identity 与容量校验，但 Retired tombstone 即使在原 expiry 之后到达也必须允许删除。客户端 MUST NOT 依据时间、页面开关或 Owner 本地按钮猜测远端撤销。

#### Scenario: Visitor 在线收到撤销通知

- **WHEN** Owner 撤销邀请且 Visitor 的 control channel 收到 matching Retired push
- **THEN** inbox replacement 删除 exact identity、接受按钮立即禁用，其他 VisitSession 或 InviteID 的条目保持不变

#### Scenario: VisitSession 关闭时清理多个邀请

- **WHEN** Visitor 收到同一或不同 session 的多个 Retired push
- **THEN** Service 对每个 exact identity 幂等收敛，页面不保留已关闭会话的可接受项，也不按 Owner 或列表位置误删其他邀请

#### Scenario: 点击残留邀请收到明确拒绝

- **WHEN** 客户端因断线窗口或旧版本状态仍显示某项邀请，玩家点击接受且服务端明确返回 visit/invite not-found、invite expired、state/revision conflict 或当前 actor 不可接受
- **THEN** Coordinator 退役该 exact inbox identity、保持现有 OwnWorld target/Scene且不签发 visit admission，并显示“邀请已撤销或失效，请选择最新邀请”的稳定低敏说明，不表现为接受成功进入自己的世界

#### Scenario: 接受结果无法确认

- **WHEN** accept 返回 timeout、transport、caller cancellation 或 commit-unknown
- **THEN** 客户端保持既有 commit-unknown fail-closed 语义，不删除 invite、不自动重试、不进入 OwnWorld fallback，也不声称邀请仍有效或已经失效

#### Scenario: 暂时性拒绝不删除邀请

- **WHEN** accept 因 capacity、Owner unavailable、rate limit 或 dependency unavailable 被拒绝
- **THEN** 客户端显示对应可恢复失败并保留 inbox identity，等待新的权威 push 或玩家显式重试
