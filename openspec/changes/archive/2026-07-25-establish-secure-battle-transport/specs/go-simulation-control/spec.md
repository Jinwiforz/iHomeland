## ADDED Requirements

### Requirement: BattleTicket control 必须消费 current SimulationTarget 且不能阻塞 lifecycle

Go MUST在每次BattleTicket install/status/revoke前重新解析current `SimulationTarget`，并把exact target/actor capacity/binding通过现有继承stdio control发送到对应child。新增battle control frame MUST保持closed schema、bootstrap nonce、sequence、64 KiB frame与secret redaction；lifecycle/health/revoke MUST使用高优先级有界lane，ticket install/status MAY使用独立低优先级有界queue且不得饿死drain/stop或使health误判。C++ MUST只接受与本node/instance/current stamp一致的install，并在instance/node terminal时本地先撤销ticket/session。

#### Scenario: Target 在排队期间变为 stale

- **WHEN**BattleTicket install等待control lane时assignment被更高generation替换
- **THEN**Go在发送前或C++在接收时拒绝stale binding，不占用successor actor slot且HTTP不返回credential

#### Scenario: Ticket 安装突发

- **WHEN**多个合法actor并发请求ticket并填满低优先级control queue
- **THEN**额外install以稳定backpressure/capacity reason失败，health、revoke、drain和stop仍在deadline内执行

#### Scenario: Secret control frame 被诊断捕获

- **WHEN**proof key字段经过codec、日志、fixture、report或错误格式化路径
- **THEN**secret gate拒绝或清洗输出；只有受信继承pipe与C++有界secret memory可见原值

### Requirement: SimulationNode 生命周期必须包含 battle listener 与 session revoke

Go MUST只在exact child hello/control identity、battle listener bind/advertised identity和crypto/wire config全部验证后发布node ready。Node unhealthy、control EOF、listener failure、assignment revoke、instance drain/stop或Session invalidation MUST使相关BattleTicket/BattleSession target不可用，并通过同一supervised owner触发revoke与受控关闭。Battle listener MUST在instance/result/node/storage之前按逆序停止，且不得由C++静默rebind新端口或由Go进程内fallback替代。

#### Scenario: Listener 在 child hello 后绑定失败

- **WHEN**control child identity有效但UDP address冲突或listener config漂移
- **THEN**node不注册healthy target，Go逆序关闭child并保持non-ready，不退回无battle的占位node继续签发ticket

#### Scenario: Assignment drain

- **WHEN**Go开始drain承载activeBattleSession的exact assignment
- **THEN**先停止该target新ticket/packet ingress并revoke session，再有界drain transport和simulation result，最后撤销fence并stop instance
