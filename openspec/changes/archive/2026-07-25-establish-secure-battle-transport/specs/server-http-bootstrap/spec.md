## MODIFIED Requirements

### Requirement: HTTP router 必须精确实现冻结 OpenAPI operation

公开router MUST且只能实现`getVersion`、`getBootstrapConfig`、`registerAccount`、`loginAccount`、`refreshSession`、`logoutSession`、`issueConnectionTicket`、`getWorldBootstrap`、`acceptVisitInvite`、`issueWorldAdmission`与`issueBattleTicket`对应的现有method/path、认证、status和schema。Runtime operation table的body limit、timeout与idempotency mode MUST与`openapi.yaml`精确一致；公开response/error字段、enum和Unix毫秒投影 MUST由集中versioned codec映射，handler MUST NOT定义同义DTO、错误或时间单位。

#### Scenario: OpenAPI metadata 与 runtime 漂移

- **WHEN**route的method/path/operationId/body limit/timeout/idempotency或认证要求与OpenAPI不一致，或router多注册未声明业务route
- **THEN**contract test失败且change不能归档

#### Scenario: 编码 world bootstrap

- **WHEN**application返回PersonalWorld和包含内部node/fence的current assignment
- **THEN**response只包含OpenAPI允许的world和client-safe assignment字段，时间向下转换为Unix毫秒且不泄漏完整AssignmentStamp

#### Scenario: 未知业务路径

- **WHEN**客户端请求未登记HTTP path或错误method
- **THEN**router返回有界稳定非成功响应，不猜测相近route且不调用任何application service

## ADDED Requirements

### Requirement: BattleTicket handler 必须只适配权威 battle admission application

`issueBattleTicket` handler MUST只执行closed decode、Bearer AuthContext、deadline/idempotency、调用BattleTicket application和集中codec映射。Application MUST从Session、PersonalWorld/VisitSession role、current SimulationTarget、capacity owner与trusted BattleEndpointProvider派生binding，并在Redis issuance和exact child install均成功后返回。Handler MUST不访问C++ handle、Redis、placement、socket或crypto provider，不从Host/request body推导endpoint，不把ConnectionTicket/WorldAdmission/GAMEPLAY scope提升为battle资格。

#### Scenario: Valid bearer 但 target 尚未 ready

- **WHEN**actor session有效但SimulationTarget missing/stale、child listener未ready或capacity不可证明
- **THEN**operation返回稳定dependency/capacity结果且不签发或安装credential

#### Scenario: Battle ticket response 丢失后重试

- **WHEN**首次issuance/install已提交但response丢失，caller以相同lineage、target和Idempotency-Key重试
- **THEN**application重放首次ticket/expiry/endpoint，不再次占用actor slot；handler不生成新identity

#### Scenario: 进程进入 draining

- **WHEN**public runtime已停止新battle issuance但旧HTTP连接仍提交request
- **THEN**readiness/draining gate在application前拒绝，不让即将撤销的target产生新ticket
