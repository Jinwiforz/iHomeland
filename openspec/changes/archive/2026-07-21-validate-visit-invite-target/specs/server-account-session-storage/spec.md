## MODIFIED Requirements

### Requirement: AccountRepository 必须忠实实现原子 outcome、提交不确定性与最小 Player 可用性读取
AccountRepository `Create` MUST 在一个 MySQL 原子写入中提交 account、player 与 credential hash，并只把已知 username 唯一约束映射为 `CreateOutcomeUsernameConflict`。Server-generated AccountID/PlayerID 碰撞 MUST 作为 identity/data defect fail closed，不能伪装成 username conflict。连接错误、context deadline 或 commit acknowledgement 丢失 MUST 复用 transaction policy 区分明确未提交与 commit unknown，不得猜测回滚、重试生成新身份或补偿删除。`FindForAuthentication` MUST 按 exact canonical username 返回同一一致性认证快照，not found 与 dependency failure MUST 严格分离。Account owner 还 MUST 提供按 exact PlayerID 判断目标当前是否 active 且可邀请的最小只读合同，只返回 available、统一 unavailable 或 dependency failure；missing 与 inactive MUST 合并，且不得向调用领域暴露 AccountID、username、credential 或具体停用原因。

#### Scenario: 并发注册相同 canonical username
- **WHEN** 多个 goroutine 使用大小写等价 username 并发调用 Create
- **THEN** 数据库唯一约束最多提交一个完整账号，其余返回 username conflict，且不存在只有 account、player 或 credential 任一部分的残缺记录

#### Scenario: Create 结果无法确认
- **WHEN** 数据库可能已提交但调用方在收到 commit acknowledgement 前失去连接或 deadline 到期
- **THEN** repository 返回 commit unknown且不谎报 not committed、username conflict 或成功，不执行跨存储 session 补偿

#### Scenario: 认证读取依赖失败
- **WHEN** canonical username 合法但 MySQL 无法证明记录存在或不存在
- **THEN** repository 返回 dependency failure而不是 not found，account application不得把它降级为 invalid credentials

#### Scenario: 按 PlayerID 读取邀请可用性
- **WHEN** 其他领域以格式有效的 exact PlayerID 查询 active 可邀请性
- **THEN** active account 返回 available，missing 与 inactive 统一返回 unavailable，unknown status 或数据库故障返回 dependency failure且不泄漏其他账号字段
