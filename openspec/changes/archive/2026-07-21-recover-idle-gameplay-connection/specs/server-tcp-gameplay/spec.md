## ADDED Requirements

### Requirement: Server gameplay heartbeat 必须由 transport owner 有界处理

服务端 MUST 只允许已认证且处于 active target 的 gameplay connection 提交 heartbeat request。Dispatcher MUST 验证 route、direction、kind、sequence、request ID、rate policy 与 deadline，再直接产生空 heartbeat response；MUST NOT 调用 PersonalWorld 或 VisitSession application mutation。每个成功解码的合法 heartbeat MUST 与其他合法 C2S envelope 一样刷新 connection read deadline，非法、超速或 pending-target heartbeat MUST fail closed。

#### Scenario: Owner 静默超过原业务空闲窗口

- **WHEN** Owner connection 没有业务帧但持续在约定 interval 内完成合法 heartbeat request/response
- **THEN** server connection 保持 active，`idle_timeout` 不增加，world/visit revision 与 application dispatch 结果不变

#### Scenario: Pending Visitor 在首个 JOIN 前发送 heartbeat

- **WHEN** JOIN 或 RECONNECT admission connection 尚未完成规定的首个 activation command 就发送 heartbeat
- **THEN** server 按 target state policy 拒绝并关闭该 pending connection，heartbeat 不能绕过首帧 admission 约束

#### Scenario: Heartbeat 超过 rate policy

- **WHEN** active connection 以高于 registry policy 的速率持续发送 heartbeat
- **THEN** dispatcher 在业务 application 前产生稳定 rate outcome，并按 abuse policy 有界拒绝或关闭，不创建无界 timer 或 per-request 长期状态
