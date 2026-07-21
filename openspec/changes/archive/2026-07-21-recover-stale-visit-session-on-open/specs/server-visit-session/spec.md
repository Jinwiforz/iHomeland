## MODIFIED Requirements

### Requirement: VisitSession identity 与 owner binding 必须稳定且不可转移
服务端 MUST 使用独立、受校验且由服务端生成的 VisitSessionID 标识一次临时访问 aggregate，并 MUST 不可变绑定 `account.PlayerID` Owner、Owner 的 active PersonalWorldID 与创建时完整 current AssignmentStamp。VisitSession MUST NOT 把 Owner client 视为网络主机，不得把 VisitSessionID、PlayerID、PersonalWorldID、WorldInstanceID、connection binding、PartyID、RoomID 或 ActivityInstanceID 相互转换。Owner MUST NOT 因 disconnect、join order、latency、capacity 或 Visitor 状态而转移。Owner 对 own-world gameplay connection 显式 Open 时，若 world active index 指向绑定旧 AssignmentStamp 的 VisitSession，application MUST 先通过已有 system invalidation 取得并提交权威终态结果、发布完整 safe-return，再以同一 Open CommandID 重新解析 current active session；不得就地改写旧 assignment、迁移旧资格或依赖客户端重复点击。

#### Scenario: Owner 为 current world 开启访问
- **WHEN** 受信 Owner AuthContext 对自己的 active PersonalWorld 请求开启 VisitSession，且 placement 返回 current active、lease 有效的完整 assignment
- **THEN** 服务端创建或解析该 world 唯一 active VisitSession，固定 Owner/world/assignment binding，Visitor 或客户端 payload 不能改写这些身份

#### Scenario: Assignment 已经改变
- **WHEN** 旧 VisitSession 绑定的完整 AssignmentStamp 与 PersonalWorld 当前 assignment 不同
- **THEN** 旧 VisitSession 不得迁移或复活到 successor，任何新 accept/join/reconnect 均被拒绝；独立 system invalidation 只有重新取得 missing、expired、非 active 或不同完整 stamp 的权威证据后才能关闭并产生安全返回结果

#### Scenario: 重启后 Owner 显式重新开放访问
- **WHEN** 进程退出前未执行 assignment loss callback，重启后的 world active index 仍指向旧 VisitSession，且 placement 已提供更高 generation/fence 的 current assignment
- **THEN** 首次 Owner Open 先以稳定 system CommandID 原子关闭旧 VisitSession并发布其完整 terminal result，再以原始 Open CommandID 创建或解析只绑定 current assignment 的唯一 active VisitSession；旧 Owner gameplay connection 已不存在时不得向 successor Owner 投递 predecessor assignment changed，客户端无需清理 Redis 或重复点击

#### Scenario: 恢复流程保持可观测且不会中断业务响应
- **WHEN** stale Open 恢复提交或解析旧 VisitSession 终态
- **THEN** 服务端以固定 `stale_open_reconcile` operation 记录 applied、ignored、stale 或 failed，metrics 封闭标签必须接受该 operation，且观测不得 panic、改变提交结论或阻止随后一次 Open

#### Scenario: 恢复提交结果不确定
- **WHEN** 旧 VisitSession invalidation 返回 dependency、dependency defect 或 commit-unknown
- **THEN** application 返回低敏依赖失败且不得继续创建新 VisitSession、替换 CommandID或猜测终态已提交；相同 system command 的后续权威 replay仍能返回首次完整结果

#### Scenario: 并发 Open 恢复同一旧 session
- **WHEN** 多个 Owner gameplay command 同时发现同一旧 AssignmentStamp VisitSession
- **THEN** revision CAS、稳定 invalidation identity与active-world唯一索引保证旧终态最多提交一次、safe-return副作用按首次结果去重且最多存在一个绑定current assignment的active VisitSession

#### Scenario: Visitor 被当作新 Owner
- **WHEN** Owner 断线而某个 Visitor 最早加入、延迟最低或是唯一在线成员
- **THEN** WorldOwnerID 与 VisitSession Owner 保持不变，Visitor 不获得 invite、kick、close 或 Owner world mutation 权限
