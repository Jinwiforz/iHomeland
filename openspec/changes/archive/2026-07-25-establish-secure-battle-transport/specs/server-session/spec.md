## ADDED Requirements

### Requirement: Account session 失效必须撤销 BattleTicket 与 BattleSession

BattleTicket和BattleSession MUST绑定唯一Account SessionID/epoch且不得构造第二份身份事实。Logout、forced logout、ban、refresh replay或其他epoch递增 MUST先提交Session owner的权威失效，再通知BattleTicket/BattleSession invalidator撤销该lineage的installed ticket、proof key和active session；通知失败 MUST不得回滚epoch或恢复旧battle资格。UDP payload、ticket selector、endpoint rebind或actor字段 MUST不能覆盖AuthContext派生的PlayerID、role或epoch。

#### Scenario: Active battle 中 logout

- **WHEN**HTTPS AuthContext对active BattleSession所属账号执行logout
- **THEN**SessionStore先原子推进epoch并撤销旧token/ticket，随后C++停止旧lineage packet dispatch；旧BattleTicket、key或rebind不能继续使用

#### Scenario: Revoke 通知暂时失败

- **WHEN**epoch已提交但child control revoke超时或child不可达
- **THEN**Go保持session无效、撤销target/readiness并受控关闭，不把通知失败解释为旧BattleSession仍获授权

### Requirement: BattleTicket 必须与 ConnectionTicket 和 WorldAdmission 不可互换

Session policy MUST明确WSS ConnectionTicket只授予CONTROL、TLS/TCP ConnectionTicket只授予GAMEPLAY，而BattleTicket只允许在绑定UDP endpoint上建立exact SimulationTarget的BATTLE session。WorldAdmission继续只授权TLS/TCP PersonalWorld/VisitSession target。任一credential提交到错误listener/channel、错误endpoint、错误assignment或错误session epoch MUST fail closed且不得转换scope或消费为另一凭据。

#### Scenario: TLS/TCP ticket 提交到 battle UDP

- **WHEN**客户端把有效GAMEPLAY ConnectionTicket或WorldAdmission编码进battle handshake
- **THEN**UDP listener在建立session前拒绝且不把它转换为BattleTicket、不泄漏credential detail
