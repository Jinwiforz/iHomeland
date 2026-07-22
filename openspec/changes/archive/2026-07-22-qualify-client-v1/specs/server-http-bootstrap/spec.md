## ADDED Requirements

### Requirement: World admission 必须公开并冻结 Visitor 首帧 revision

`issueWorldAdmission` MUST 在 Visitor JOIN/RECONNECT 资格解析时读取 current VisitSession revision，把该 revision 纳入幂等 WorldAdmission binding，并以 `visitRevision` 返回；`OWN_WORLD` MUST 返回 `0`。相同 IssueID 的 response-loss replay MUST 返回首次冻结的 revision，不能随之后的 VisitSession mutation 漂移。客户端 MUST 只用该 revision 构造 JOIN/RECONNECT 首帧，不得从断线前 projection 推导、递增或探测 current revision。

#### Scenario: Visitor gameplay 断开后 grace 内恢复

- **WHEN** 服务端已把 Visitor 从 joined 提交为 reconnecting 并推进 VisitSession revision，旧 gameplay 已无法接收 replacement PUSH
- **THEN** 新 `RECONNECT` admission 返回该次签发读取并纳入 credential binding 的 current `visitRevision`，Visitor 以该值提交首帧并恢复同一 membership

#### Scenario: Admission 响应丢失后重放

- **WHEN** 相同 actor、target 与 IssueID 重试一次已提交的 Visitor admission，而 VisitSession revision 在首次提交后继续推进
- **THEN** 服务端返回首次 credential 与首次冻结的 `visitRevision`，不得把新 revision 拼接到旧 credential response
