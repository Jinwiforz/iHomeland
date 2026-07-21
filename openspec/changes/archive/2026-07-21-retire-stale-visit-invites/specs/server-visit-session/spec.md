## ADDED Requirements

### Requirement: VisitSession mutation 必须原子保存邀请退役事实

VisitSession application service MUST 在首次成功 mutation 的 source 与 target snapshot 之间计算 pending invite retirement：source 中为 Pending、target 中不存在或不再 Pending 的 identity MUST 作为稳定排序、唯一且有界的 `retired_invites` 与 target snapshot、CommandID、fingerprint、operation payload 和 safe-return directives 原子保存。Store replay MUST 返回首次完整 retirement 集合；not-committed 与 commit-unknown MUST 不返回或发布部分集合。Result validation MUST 拒绝无效、重复、乱序或仍在 target 中保持 Pending 的 retirement。

#### Scenario: Owner 撤销 pending invite

- **WHEN** revoke command 首次提交并从 target snapshot 删除 matching pending invite
- **THEN** mutation result 原子保存该 invite 的完整非凭据 projection，application coordinator 向其 TargetVisitorID 发布 Retired push，重复相同 command replay 不推进 revision且只产生幂等同 identity tombstone

#### Scenario: Accept 或 deadline 退役 invite

- **WHEN** HTTP accept 把 pending invite 转为 Accepted，或 system deadline 精确删除到期 invite
- **THEN** 首次 result 保存对应 retirement，目标客户端立即失去 accept 能力；commit-unknown 不允许服务端伪造已发布结论

#### Scenario: Terminal close 清理多个 pending invite

- **WHEN** Owner close、Owner grace/session expiry、assignment invalidation 或 dependency loss terminal close VisitSession 并清除全部 pending invite
- **THEN** result 按 VisitSessionID/InviteID 稳定保存全部 retirement，coordinator 分别向各精确目标发布 tombstone，不从已经清空的 target snapshot 猜测接收者

#### Scenario: 解码历史 mutation result

- **WHEN** Redis 中已存在本 change 之前不含 `retired_invites` 字段的合法 replay result
- **THEN** codec 将缺失字段解释为空集合并保持旧 result 语义；新 result 的 retirement 集合必须确定性编码、解码和等价比较
